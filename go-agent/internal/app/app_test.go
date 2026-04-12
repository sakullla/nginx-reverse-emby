package app

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/store"
	agentupdate "github.com/sakullla/nginx-reverse-emby/go-agent/internal/update"
)

func TestNewBuildsRealWiring(t *testing.T) {
	cfg := Config{
		AgentID:        "agent",
		AgentName:      "agent",
		MasterURL:      "https://master.example.com",
		AgentToken:     "token",
		CurrentVersion: "0.1.0",
		DataDir:        t.TempDir(),
	}
	app, err := New(cfg)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	if _, ok := app.store.(*store.Filesystem); !ok {
		t.Fatalf("expected filesystem store, got %T", app.store)
	}
	if app.syncClient == nil {
		t.Fatal("expected sync client to be initialized")
	}
	if app.httpApplier == nil {
		t.Fatal("expected http applier to be initialized")
	}
	if app.certApplier == nil {
		t.Fatal("expected certificate applier to be initialized")
	}
	if app.l4Applier == nil {
		t.Fatal("expected l4 applier to be initialized")
	}
	if app.relayApplier == nil {
		t.Fatal("expected relay applier to be initialized")
	}
}

func TestRunReturnsErrorWithoutAppliedSnapshot(t *testing.T) {
	cfg := Config{HeartbeatInterval: 5 * time.Millisecond}
	mem := store.NewInMemory()
	errSync := errors.New("boom")
	client := newTestSyncClient([]syncResponse{{err: errSync}}, syncResponse{})
	app := newAppWithDeps(cfg, mem, client, nil, nil, nil)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	if err := app.Run(ctx); !errors.Is(err, errSync) {
		t.Fatalf("expected sync error, got %v", err)
	}

	state, err := mem.LoadRuntimeState()
	if err != nil {
		t.Fatalf("failed to load runtime state: %v", err)
	}
	if state.Metadata["last_sync_error"] != errSync.Error() {
		t.Fatalf("expected last_sync_error metadata, got %v", state.Metadata)
	}
}

func TestRunTracksCurrentRevisionFromSuccessfulApplies(t *testing.T) {
	cfg := Config{HeartbeatInterval: 5 * time.Millisecond}
	mem := store.NewInMemory()
	if err := mem.SaveAppliedSnapshot(Snapshot{DesiredVersion: "baseline", Revision: 100}); err != nil {
		t.Fatalf("failed to seed applied snapshot: %v", err)
	}
	if err := mem.SaveRuntimeState(store.RuntimeState{
		Metadata: map[string]string{"current_revision": "999"},
	}); err != nil {
		t.Fatalf("failed to seed runtime state: %v", err)
	}

	client := newTestSyncClient(nil, syncResponse{snapshot: Snapshot{DesiredVersion: "ok", Revision: 101}})
	app := newAppWithDeps(cfg, mem, client, nil, nil, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- app.Run(ctx)
	}()

	req1 := waitForRequest(t, client, time.Second)
	if req1.CurrentRevision != 100 {
		t.Fatalf("expected first request revision 100, got %d", req1.CurrentRevision)
	}

	req2 := waitForRequest(t, client, time.Second)
	if req2.CurrentRevision != 101 {
		t.Fatalf("expected second request revision 101 after successful apply, got %d", req2.CurrentRevision)
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
}

func TestRunKeepsRunningWhenAppliedSnapshotExists(t *testing.T) {
	cfg := Config{HeartbeatInterval: 5 * time.Millisecond}
	mem := store.NewInMemory()
	if err := mem.SaveAppliedSnapshot(Snapshot{DesiredVersion: "1.0"}); err != nil {
		t.Fatalf("failed to seed applied snapshot: %v", err)
	}
	client := newTestSyncClient(nil, syncResponse{err: errors.New("boom")})
	app := newAppWithDeps(cfg, mem, client, nil, nil, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- app.Run(ctx)
	}()

	waitForCalls(t, client, 1, time.Second)

	cancel()

	if err := <-done; err != nil {
		t.Fatalf("expected nil after cancellation, got %v", err)
	}
}

func TestRunPersistsDesiredSnapshot(t *testing.T) {
	cfg := Config{HeartbeatInterval: 5 * time.Millisecond}
	mem := store.NewInMemory()
	expected := Snapshot{
		DesiredVersion: "2.0",
		Revision:       9,
		Rules: []model.HTTPRule{{
			FrontendURL: "https://frontend.example.com",
			BackendURL:  "http://127.0.0.1:8096",
			Revision:    2,
		}},
		L4Rules: []model.L4Rule{{
			Protocol:     "tcp",
			ListenHost:   "127.0.0.1",
			ListenPort:   9000,
			UpstreamHost: "127.0.0.1",
			UpstreamPort: 9001,
			Revision:     4,
		}},
		RelayListeners: []model.RelayListener{{
			ID:         31,
			AgentID:    "remote-agent-5",
			Name:       "relay-a",
			ListenHost: "127.0.0.1",
			ListenPort: 9443,
			Enabled:    true,
			TLSMode:    "pin_only",
			PinSet: []model.RelayPin{{
				Type:  "sha256",
				Value: "pin-value",
			}},
			Revision: 7,
		}},
		Certificates: []model.ManagedCertificateBundle{{
			ID:       21,
			Domain:   "sync.example.com",
			Revision: 3,
			CertPEM:  "CERTIFICATE",
			KeyPEM:   "PRIVATEKEY",
		}},
		CertificatePolicies: []model.ManagedCertificatePolicy{{
			ID:              21,
			Domain:          "sync.example.com",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "local_http01",
			Status:          "issued",
			Revision:        3,
			Usage:           "relay_ca",
			CertificateType: "internal_ca",
			SelfSigned:      true,
		}},
	}
	client := newTestSyncClient(nil, syncResponse{snapshot: expected})
	app := newAppWithDeps(cfg, mem, client, nil, nil, nil)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- app.Run(ctx)
	}()

	waitForCalls(t, client, 1, time.Second)

	snap, err := mem.LoadDesiredSnapshot()
	if err != nil {
		t.Fatalf("failed to load desired snapshot: %v", err)
	}
	if !reflect.DeepEqual(snap, expected) {
		t.Fatalf("expected desired snapshot %+v, got %+v", expected, snap)
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
}

func TestRunPersistsAppliedSnapshotAfterSuccessfulSync(t *testing.T) {
	cfg := Config{HeartbeatInterval: 5 * time.Millisecond}
	mem := store.NewInMemory()
	previousApplied := Snapshot{
		DesiredVersion: "1.0",
		Revision:       4,
		Rules: []model.HTTPRule{{
			FrontendURL: "http://old.example.test:18080",
			BackendURL:  "http://127.0.0.1:8096",
			Revision:    1,
		}},
	}
	if err := mem.SaveAppliedSnapshot(previousApplied); err != nil {
		t.Fatalf("failed to seed applied snapshot: %v", err)
	}

	expected := Snapshot{
		DesiredVersion: "2.0",
		Revision:       9,
		Rules: []model.HTTPRule{{
			FrontendURL:   "http://edge.example.test:18080",
			BackendURL:    "http://127.0.0.1:8096",
			ProxyRedirect: true,
			Revision:      4,
		}},
	}
	client := newTestSyncClient(nil, syncResponse{snapshot: expected})
	httpApplier := &testHTTPApplier{}
	app := newAppWithHTTPDeps(cfg, mem, client, httpApplier, nil, nil, nil)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- app.Run(ctx)
	}()

	waitForCalls(t, client, 1, time.Second)

	applied, err := mem.LoadAppliedSnapshot()
	if err != nil {
		t.Fatalf("failed to load applied snapshot: %v", err)
	}
	if !reflect.DeepEqual(applied, expected) {
		t.Fatalf("expected applied snapshot %+v, got %+v", expected, applied)
	}

	state, err := mem.LoadRuntimeState()
	if err != nil {
		t.Fatalf("failed to load runtime state: %v", err)
	}
	if state.CurrentRevision != expected.Revision {
		t.Fatalf("expected current revision %d, got %d", expected.Revision, state.CurrentRevision)
	}
	if state.Metadata["current_revision"] != "9" {
		t.Fatalf("expected metadata current_revision 9, got %q", state.Metadata["current_revision"])
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
}

func TestRunMergesOmittedSyncFieldsOntoPreviouslyAppliedSnapshot(t *testing.T) {
	cfg := Config{HeartbeatInterval: 5 * time.Millisecond}
	mem := store.NewInMemory()
	previousApplied := Snapshot{
		DesiredVersion: "applied",
		Revision:       4,
		Rules: []model.HTTPRule{{
			FrontendURL: "http://applied.example.test:18080",
			BackendURL:  "http://127.0.0.1:8096",
			Revision:    1,
		}},
		L4Rules: []model.L4Rule{{
			Protocol:     "tcp",
			ListenHost:   "127.0.0.1",
			ListenPort:   9000,
			UpstreamHost: "127.0.0.1",
			UpstreamPort: 9001,
			Revision:     1,
		}},
	}
	if err := mem.SaveAppliedSnapshot(previousApplied); err != nil {
		t.Fatalf("failed to seed applied snapshot: %v", err)
	}
	previousDesired := Snapshot{
		DesiredVersion: "desired",
		Revision:       4,
		Rules: []model.HTTPRule{{
			FrontendURL: "http://desired.example.test:18080",
			BackendURL:  "http://127.0.0.1:8096",
			Revision:    2,
		}},
		L4Rules: previousApplied.L4Rules,
	}
	if err := mem.SaveDesiredSnapshot(previousDesired); err != nil {
		t.Fatalf("failed to seed desired snapshot: %v", err)
	}

	synced := Snapshot{
		DesiredVersion: "next",
		Revision:       5,
		L4Rules: []model.L4Rule{{
			Protocol:     "tcp",
			ListenHost:   "127.0.0.1",
			ListenPort:   9100,
			UpstreamHost: "127.0.0.1",
			UpstreamPort: 9101,
			Revision:     2,
		}},
	}
	client := newTestSyncClient(nil, syncResponse{snapshot: synced})
	httpApplier := &testHTTPApplier{}
	l4Applier := &testL4Applier{}
	app := newAppWithHTTPDeps(cfg, mem, client, httpApplier, nil, l4Applier, nil)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- app.Run(ctx)
	}()

	waitForCalls(t, client, 1, time.Second)

	if calls := httpApplier.snapshotCalls(); len(calls) != 1 {
		t.Fatalf("expected only startup http apply call for omitted http payload, got %d", len(calls))
	}

	l4Calls := l4Applier.snapshotCalls()
	if len(l4Calls) != 2 {
		t.Fatalf("expected startup and sync l4 apply calls, got %d", len(l4Calls))
	}
	if !reflect.DeepEqual(l4Calls[1].rules, synced.L4Rules) {
		t.Fatalf("unexpected synced l4 rules: %+v", l4Calls[1].rules)
	}

	desired, err := mem.LoadDesiredSnapshot()
	if err != nil {
		t.Fatalf("failed to load desired snapshot: %v", err)
	}
	if !reflect.DeepEqual(desired.Rules, previousDesired.Rules) {
		t.Fatalf("expected desired http rules preserved from previous desired snapshot, got %+v", desired.Rules)
	}

	applied, err := mem.LoadAppliedSnapshot()
	if err != nil {
		t.Fatalf("failed to load applied snapshot: %v", err)
	}
	if !reflect.DeepEqual(applied.Rules, previousApplied.Rules) {
		t.Fatalf("expected applied http rules preserved from previous applied snapshot, got %+v", applied.Rules)
	}
	if !reflect.DeepEqual(applied.L4Rules, synced.L4Rules) {
		t.Fatalf("expected applied l4 rules updated from sync payload, got %+v", applied.L4Rules)
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
}

func TestRunDoesNotAdvanceAppliedSnapshotOrCurrentRevisionOnApplyFailure(t *testing.T) {
	cfg := Config{HeartbeatInterval: 5 * time.Millisecond}
	mem := store.NewInMemory()
	previousApplied := Snapshot{
		DesiredVersion: "stable",
		Revision:       7,
		Rules: []model.HTTPRule{{
			FrontendURL: "http://stable.example.test:18080",
			BackendURL:  "http://127.0.0.1:8096",
			Revision:    1,
		}},
	}
	if err := mem.SaveAppliedSnapshot(previousApplied); err != nil {
		t.Fatalf("failed to seed applied snapshot: %v", err)
	}
	if err := mem.SaveRuntimeState(store.RuntimeState{
		CurrentRevision: previousApplied.Revision,
		Metadata: map[string]string{
			"current_revision": "7",
			"foo":              "bar",
		},
	}); err != nil {
		t.Fatalf("failed to seed runtime state: %v", err)
	}

	client := newTestSyncClient(nil, syncResponse{snapshot: Snapshot{
		DesiredVersion: "broken",
		Revision:       9,
		Rules: []model.HTTPRule{{
			FrontendURL: "http://broken.example.test:18080",
			BackendURL:  "http://127.0.0.1:8096",
			Revision:    2,
		}},
	}})
	httpApplier := &testHTTPApplier{
		applyErr:   errors.New("http apply failed"),
		failOnCall: 2,
	}
	app := newAppWithHTTPDeps(cfg, mem, client, httpApplier, nil, nil, nil)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- app.Run(ctx)
	}()

	waitForCalls(t, client, 1, time.Second)

	applied, err := mem.LoadAppliedSnapshot()
	if err != nil {
		t.Fatalf("failed to load applied snapshot: %v", err)
	}
	if !reflect.DeepEqual(applied, previousApplied) {
		t.Fatalf("expected applied snapshot to stay unchanged on failure, got %+v", applied)
	}

	state, err := mem.LoadRuntimeState()
	if err != nil {
		t.Fatalf("failed to load runtime state: %v", err)
	}
	if state.CurrentRevision != previousApplied.Revision {
		t.Fatalf("expected current revision %d after failed apply, got %d", previousApplied.Revision, state.CurrentRevision)
	}
	if state.Metadata["current_revision"] != "7" {
		t.Fatalf("expected metadata current_revision 7 after failed apply, got %q", state.Metadata["current_revision"])
	}
	if state.Metadata["last_sync_error"] != "http apply failed" {
		t.Fatalf("expected last_sync_error metadata, got %v", state.Metadata)
	}
	if state.Metadata["last_apply_revision"] != "9" || state.Metadata["last_apply_status"] != "error" {
		t.Fatalf("expected attempted apply revision/status recorded, got %v", state.Metadata)
	}
	if state.Metadata["foo"] != "bar" {
		t.Fatalf("expected unrelated metadata preserved, got %v", state.Metadata)
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
}

func TestRunClosesCertificateApplierOnShutdown(t *testing.T) {
	cfg := Config{HeartbeatInterval: 5 * time.Millisecond}
	mem := store.NewInMemory()
	if err := mem.SaveAppliedSnapshot(Snapshot{DesiredVersion: "baseline"}); err != nil {
		t.Fatalf("failed to seed applied snapshot: %v", err)
	}
	client := newTestSyncClient(nil, syncResponse{snapshot: Snapshot{DesiredVersion: "next", Revision: 2}})
	certApplier := &testCertificateApplier{}
	app := newAppWithDeps(cfg, mem, client, certApplier, nil, nil)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- app.Run(ctx)
	}()

	waitForCalls(t, client, 1, time.Second)
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("run returned error: %v", err)
	}
	if got := certApplier.closeCount(); got != 1 {
		t.Fatalf("expected certificate applier close to be called once, got %d", got)
	}
}

func TestAppRollsBackRuntimeAndPersistsLastSyncError(t *testing.T) {
	cfg := Config{HeartbeatInterval: time.Hour}
	mem := store.NewInMemory()
	previousApplied := Snapshot{
		DesiredVersion: "stable",
		Revision:       7,
		Rules: []model.HTTPRule{{
			FrontendURL: "http://stable.example.test:18080",
			BackendURL:  "http://127.0.0.1:8096",
			Revision:    1,
		}},
	}
	if err := mem.SaveAppliedSnapshot(previousApplied); err != nil {
		t.Fatalf("failed to seed applied snapshot: %v", err)
	}
	if err := mem.SaveRuntimeState(store.RuntimeState{
		CurrentRevision: previousApplied.Revision,
		Metadata: map[string]string{
			"current_revision": "7",
			"foo":              "bar",
		},
	}); err != nil {
		t.Fatalf("failed to seed runtime state: %v", err)
	}

	nextSnapshot := Snapshot{
		DesiredVersion: "next",
		Revision:       9,
		Rules: []model.HTTPRule{{
			FrontendURL: "http://next.example.test:18080",
			BackendURL:  "http://127.0.0.1:8096",
			Revision:    2,
		}},
	}
	client := newTestSyncClient(nil, syncResponse{snapshot: nextSnapshot})
	httpApplier := &testHTTPApplier{
		applyErr:   errors.New("activation failed"),
		failOnCall: 2,
	}
	app := newAppWithHTTPDeps(cfg, mem, client, httpApplier, nil, nil, nil)

	ctx := context.Background()
	if err := app.runtime.Apply(ctx, Snapshot{}, previousApplied); err != nil {
		t.Fatalf("failed to seed runtime: %v", err)
	}

	if err := app.performSync(ctx); err == nil || err.Error() != "activation failed" {
		t.Fatalf("expected activation failure, got %v", err)
	}

	calls := httpApplier.snapshotCalls()
	if len(calls) != 3 {
		t.Fatalf("expected startup, failed apply, and rollback apply calls, got %d", len(calls))
	}
	if !reflect.DeepEqual(calls[2].rules, previousApplied.Rules) {
		t.Fatalf("expected rollback call to restore previous rules, got %+v", calls[2].rules)
	}

	applied, err := mem.LoadAppliedSnapshot()
	if err != nil {
		t.Fatalf("failed to load applied snapshot: %v", err)
	}
	if !reflect.DeepEqual(applied, previousApplied) {
		t.Fatalf("expected applied snapshot unchanged after failed activation, got %+v", applied)
	}

	state, err := mem.LoadRuntimeState()
	if err != nil {
		t.Fatalf("failed to load runtime state: %v", err)
	}
	if state.CurrentRevision != previousApplied.Revision {
		t.Fatalf("expected persisted current revision %d, got %d", previousApplied.Revision, state.CurrentRevision)
	}
	if state.Metadata["current_revision"] != "7" {
		t.Fatalf("expected persisted metadata current_revision 7, got %q", state.Metadata["current_revision"])
	}
	if state.Metadata["last_sync_error"] != "activation failed" {
		t.Fatalf("expected activation failure metadata, got %v", state.Metadata)
	}
	if state.Metadata["foo"] != "bar" {
		t.Fatalf("expected unrelated metadata preserved, got %v", state.Metadata)
	}
}

func TestRunPersistsExplicitEmptyCertificatePayloads(t *testing.T) {
	cfg := Config{HeartbeatInterval: 5 * time.Millisecond}
	mem := store.NewInMemory()
	expected := Snapshot{
		DesiredVersion:      "2.0",
		Revision:            10,
		Certificates:        []model.ManagedCertificateBundle{},
		CertificatePolicies: []model.ManagedCertificatePolicy{},
	}
	client := newTestSyncClient(nil, syncResponse{snapshot: expected})
	app := newAppWithDeps(cfg, mem, client, nil, nil, nil)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- app.Run(ctx)
	}()

	waitForCalls(t, client, 1, time.Second)

	snap, err := mem.LoadDesiredSnapshot()
	if err != nil {
		t.Fatalf("failed to load desired snapshot: %v", err)
	}
	if snap.Certificates == nil || len(snap.Certificates) != 0 {
		t.Fatalf("expected explicit empty certificates slice, got %+v", snap.Certificates)
	}
	if snap.CertificatePolicies == nil || len(snap.CertificatePolicies) != 0 {
		t.Fatalf("expected explicit empty certificate policies slice, got %+v", snap.CertificatePolicies)
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
}

func TestRunRecordsSyncErrorsInRuntimeState(t *testing.T) {
	cfg := Config{HeartbeatInterval: 5 * time.Millisecond}
	mem := store.NewInMemory()
	if err := mem.SaveAppliedSnapshot(Snapshot{DesiredVersion: "baseline"}); err != nil {
		t.Fatalf("failed to seed applied snapshot: %v", err)
	}
	state := store.RuntimeState{
		Metadata: map[string]string{
			"current_revision": "7",
			"foo":              "bar",
		},
	}
	if err := mem.SaveRuntimeState(state); err != nil {
		t.Fatalf("failed to seed runtime state: %v", err)
	}

	client := newTestSyncClient([]syncResponse{
		{err: errors.New("boom")},
		{snapshot: Snapshot{DesiredVersion: "new"}},
	}, syncResponse{})

	app := newAppWithDeps(cfg, mem, client, nil, nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- app.Run(ctx)
	}()

	waitForCalls(t, client, 1, time.Second)
	current := waitForLastSyncError(t, time.Second, mem.LoadRuntimeState, "boom")
	if current.Metadata["last_sync_error"] != "boom" {
		t.Fatalf("expected failure metadata, got %v", current.Metadata)
	}
	if current.Metadata["foo"] != "bar" {
		t.Fatalf("expected other metadata preserved, got %v", current.Metadata)
	}

	waitForCalls(t, client, 2, time.Second)
	waitForRuntimeState(t, time.Second, func() bool {
		updated, err := mem.LoadRuntimeState()
		if err != nil {
			t.Fatalf("failed to load runtime state: %v", err)
		}
		current = updated
		_, ok := current.Metadata["last_sync_error"]
		return !ok
	}, func() string {
		return "expected failure metadata cleared"
	})

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
}

func TestRunRecordsSaveDesiredSnapshotFailures(t *testing.T) {
	cfg := Config{HeartbeatInterval: 5 * time.Millisecond}
	inner := store.NewInMemory()
	fs := &failingStore{delegate: inner, failOnNthSave: 2}
	if err := fs.SaveAppliedSnapshot(Snapshot{DesiredVersion: "baseline"}); err != nil {
		t.Fatalf("failed to seed applied snapshot: %v", err)
	}
	if err := fs.SaveRuntimeState(store.RuntimeState{
		Metadata: map[string]string{"current_revision": "1"},
	}); err != nil {
		t.Fatalf("failed to seed runtime state: %v", err)
	}

	client := newTestSyncClient(nil, syncResponse{snapshot: Snapshot{DesiredVersion: "ok"}})
	app := newAppWithDeps(cfg, fs, client, nil, nil, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- app.Run(ctx)
	}()

	waitForRequest(t, client, time.Second)  // initial request
	waitForCalls(t, client, 2, time.Second) // second request triggers failure

	state := waitForLastSyncError(t, time.Second, fs.LoadRuntimeState, "persistence fail")
	if state.Metadata["last_sync_error"] != "persistence fail" {
		t.Fatalf("expected persistence failure metadata, got %v", state.Metadata)
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
}

func TestRunDoesNotAdvancePersistedRuntimeStateWhenSaveAppliedSnapshotFails(t *testing.T) {
	cfg := Config{HeartbeatInterval: time.Hour}
	inner := store.NewInMemory()
	fs := &failingStore{delegate: inner, failOnNthAppliedSave: 2}

	previousApplied := Snapshot{
		DesiredVersion: "stable",
		Revision:       7,
		Rules: []model.HTTPRule{{
			FrontendURL: "http://stable.example.test:18080",
			BackendURL:  "http://127.0.0.1:8096",
			Revision:    1,
		}},
	}
	if err := fs.SaveAppliedSnapshot(previousApplied); err != nil {
		t.Fatalf("failed to seed applied snapshot: %v", err)
	}
	if err := fs.SaveRuntimeState(store.RuntimeState{
		CurrentRevision: previousApplied.Revision,
		Metadata: map[string]string{
			"current_revision": "7",
			"foo":              "bar",
		},
	}); err != nil {
		t.Fatalf("failed to seed runtime state: %v", err)
	}

	client := newTestSyncClient(nil, syncResponse{snapshot: Snapshot{
		DesiredVersion: "next",
		Revision:       9,
		Rules: []model.HTTPRule{{
			FrontendURL: "http://next.example.test:18080",
			BackendURL:  "http://127.0.0.1:8096",
			Revision:    2,
		}},
	}})
	httpApplier := &testHTTPApplier{}
	app := newAppWithHTTPDeps(cfg, fs, client, httpApplier, nil, nil, nil)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- app.Run(ctx)
	}()

	waitForCalls(t, client, 1, time.Second)

	applied, err := fs.LoadAppliedSnapshot()
	if err != nil {
		t.Fatalf("failed to load applied snapshot: %v", err)
	}
	if !reflect.DeepEqual(applied, previousApplied) {
		t.Fatalf("expected applied snapshot unchanged after applied persistence failure, got %+v", applied)
	}

	state, err := fs.LoadRuntimeState()
	if err != nil {
		t.Fatalf("failed to load runtime state: %v", err)
	}
	if state.CurrentRevision != previousApplied.Revision {
		t.Fatalf("expected persisted current revision %d, got %d", previousApplied.Revision, state.CurrentRevision)
	}
	if state.Metadata["current_revision"] != "7" {
		t.Fatalf("expected persisted metadata current_revision 7, got %q", state.Metadata["current_revision"])
	}
	if state.Metadata["last_sync_error"] != "applied persistence fail" {
		t.Fatalf("expected applied persistence error metadata, got %v", state.Metadata)
	}
	if state.Metadata["last_apply_revision"] != "9" || state.Metadata["last_apply_status"] != "error" {
		t.Fatalf("expected attempted apply revision/status recorded, got %v", state.Metadata)
	}
	if state.Metadata["foo"] != "bar" {
		t.Fatalf("expected unrelated metadata preserved, got %v", state.Metadata)
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
}

func TestRunDoesNotAdvancePersistedStateOrHeartbeatRevisionWhenRollbackFailsAfterSaveAppliedSnapshotFailure(t *testing.T) {
	cfg := Config{HeartbeatInterval: time.Hour}
	inner := store.NewInMemory()
	fs := &failingStore{delegate: inner, failOnNthAppliedSave: 2}

	previousApplied := Snapshot{
		DesiredVersion: "stable",
		Revision:       7,
		Rules: []model.HTTPRule{{
			FrontendURL: "http://stable.example.test:18080",
			BackendURL:  "http://127.0.0.1:8096",
			Revision:    1,
		}},
	}
	if err := fs.SaveAppliedSnapshot(previousApplied); err != nil {
		t.Fatalf("failed to seed applied snapshot: %v", err)
	}
	if err := fs.SaveRuntimeState(store.RuntimeState{
		CurrentRevision: previousApplied.Revision,
		Metadata: map[string]string{
			"current_revision": "7",
			"foo":              "bar",
		},
	}); err != nil {
		t.Fatalf("failed to seed runtime state: %v", err)
	}

	client := newTestSyncClient([]syncResponse{
		{
			snapshot: Snapshot{
				DesiredVersion: "next",
				Revision:       9,
				Rules: []model.HTTPRule{{
					FrontendURL: "http://next.example.test:18080",
					BackendURL:  "http://127.0.0.1:8096",
					Revision:    2,
				}},
			},
		},
		{snapshot: Snapshot{DesiredVersion: "steady", Revision: 7}},
	}, syncResponse{})
	httpApplier := &testHTTPApplier{
		applyErr:   errors.New("rollback failed"),
		failOnCall: 3,
	}
	app := newAppWithHTTPDeps(cfg, fs, client, httpApplier, nil, nil, nil)
	ctx := context.Background()
	if err := app.runtime.Apply(ctx, Snapshot{}, previousApplied); err != nil {
		t.Fatalf("failed to seed runtime: %v", err)
	}

	if err := app.performSync(ctx); err == nil || err.Error() != "applied persistence fail" {
		t.Fatalf("expected applied persistence failure, got %v", err)
	}

	req1 := waitForRequest(t, client, time.Second)
	if req1.CurrentRevision != 7 {
		t.Fatalf("expected first request revision 7, got %d", req1.CurrentRevision)
	}

	applied, err := fs.LoadAppliedSnapshot()
	if err != nil {
		t.Fatalf("failed to load applied snapshot: %v", err)
	}
	if !reflect.DeepEqual(applied, previousApplied) {
		t.Fatalf("expected applied snapshot unchanged after rollback failure, got %+v", applied)
	}

	state, err := fs.LoadRuntimeState()
	if err != nil {
		t.Fatalf("failed to load runtime state: %v", err)
	}
	if state.CurrentRevision != previousApplied.Revision {
		t.Fatalf("expected persisted current revision %d, got %d", previousApplied.Revision, state.CurrentRevision)
	}
	if state.Metadata["current_revision"] != "7" {
		t.Fatalf("expected persisted metadata current_revision 7, got %q", state.Metadata["current_revision"])
	}
	if state.Metadata["last_sync_error"] != "applied persistence fail" {
		t.Fatalf("expected applied persistence error metadata, got %v", state.Metadata)
	}

	if err := app.performSync(ctx); err != nil {
		t.Fatalf("second performSync returned error: %v", err)
	}

	req2 := waitForRequest(t, client, time.Second)
	if req2.CurrentRevision != 7 {
		t.Fatalf("expected next heartbeat revision to stay fail-closed at 7, got %d", req2.CurrentRevision)
	}
}

func TestPerformSyncTriggersUpdaterWhenDesiredVersionPackageIsPresent(t *testing.T) {
	cfg := Config{CurrentVersion: "1.0.0"}
	mem := store.NewInMemory()
	previousApplied := Snapshot{
		DesiredVersion: "1.0.0",
		Revision:       7,
	}
	if err := mem.SaveAppliedSnapshot(previousApplied); err != nil {
		t.Fatalf("failed to seed applied snapshot: %v", err)
	}
	if err := mem.SaveRuntimeState(store.RuntimeState{
		CurrentRevision: previousApplied.Revision,
		Metadata: map[string]string{
			"current_revision": "7",
		},
	}); err != nil {
		t.Fatalf("failed to seed runtime state: %v", err)
	}

	client := newTestSyncClient(nil, syncResponse{snapshot: Snapshot{
		DesiredVersion: "2.0.0",
		Revision:       9,
		VersionPackage: &model.VersionPackage{
			Platform: "linux-amd64",
			URL:      "https://example.com/nre-agent",
			SHA256:   "abc123",
		},
	}})
	updater := &testUpdater{}
	app := newAppWithDeps(cfg, mem, client, nil, nil, nil)
	app.updater = updater

	ctx := context.Background()
	if err := app.runtime.Apply(ctx, Snapshot{}, previousApplied); err != nil {
		t.Fatalf("failed to seed runtime: %v", err)
	}

	if err := app.performSync(ctx); !errors.Is(err, agentupdate.ErrRestartRequested) {
		t.Fatalf("expected restart request, got %v", err)
	}

	req := waitForRequest(t, client, time.Second)
	if req.CurrentRevision != 7 {
		t.Fatalf("expected heartbeat request to report current revision 7, got %d", req.CurrentRevision)
	}

	if len(updater.calls) != 1 {
		t.Fatalf("expected one updater call, got %d", len(updater.calls))
	}
	if updater.calls[0].desiredVersion != "2.0.0" {
		t.Fatalf("expected updater desired version 2.0.0, got %q", updater.calls[0].desiredVersion)
	}
	if !reflect.DeepEqual(updater.calls[0].pkg, model.VersionPackage{
		Platform: "linux-amd64",
		URL:      "https://example.com/nre-agent",
		SHA256:   "abc123",
	}) {
		t.Fatalf("unexpected updater package: %+v", updater.calls[0].pkg)
	}

	applied, err := mem.LoadAppliedSnapshot()
	if err != nil {
		t.Fatalf("failed to load applied snapshot: %v", err)
	}
	if !reflect.DeepEqual(applied, previousApplied) {
		t.Fatalf("expected applied snapshot unchanged while handoff is pending, got %+v", applied)
	}
	desired, err := mem.LoadDesiredSnapshot()
	if err != nil {
		t.Fatalf("failed to load desired snapshot: %v", err)
	}
	if !reflect.DeepEqual(desired.VersionPackage, &model.VersionPackage{
		Platform: "linux-amd64",
		URL:      "https://example.com/nre-agent",
		SHA256:   "abc123",
	}) {
		t.Fatalf("expected desired snapshot to retain version package, got %+v", desired.VersionPackage)
	}

	state, err := mem.LoadRuntimeState()
	if err != nil {
		t.Fatalf("failed to load runtime state: %v", err)
	}
	if state.CurrentRevision != previousApplied.Revision {
		t.Fatalf("expected current revision to remain %d, got %d", previousApplied.Revision, state.CurrentRevision)
	}
}

func TestPerformSyncUpdaterStageFailureRecordsErrorWithoutFalseStateAdvance(t *testing.T) {
	cfg := Config{CurrentVersion: "1.0.0"}
	mem := store.NewInMemory()
	previousApplied := Snapshot{
		DesiredVersion: "1.0.0",
		Revision:       7,
	}
	if err := mem.SaveAppliedSnapshot(previousApplied); err != nil {
		t.Fatalf("failed to seed applied snapshot: %v", err)
	}
	if err := mem.SaveRuntimeState(store.RuntimeState{
		CurrentRevision: previousApplied.Revision,
		Metadata: map[string]string{
			"current_revision": "7",
			"foo":              "bar",
		},
	}); err != nil {
		t.Fatalf("failed to seed runtime state: %v", err)
	}

	client := newTestSyncClient([]syncResponse{
		{snapshot: Snapshot{
			DesiredVersion: "2.0.0",
			Revision:       9,
			VersionPackage: &model.VersionPackage{
				Platform: "linux-amd64",
				URL:      "https://example.com/nre-agent",
				SHA256:   "abc123",
			},
		}},
		{snapshot: Snapshot{DesiredVersion: "2.0.0", Revision: 9}},
	}, syncResponse{})
	updater := &testUpdater{stageErr: errors.New("stage failed")}
	app := newAppWithDeps(cfg, mem, client, nil, nil, nil)
	app.updater = updater

	ctx := context.Background()
	if err := app.runtime.Apply(ctx, Snapshot{}, previousApplied); err != nil {
		t.Fatalf("failed to seed runtime: %v", err)
	}

	if err := app.performSync(ctx); err == nil || err.Error() != "stage failed" {
		t.Fatalf("expected staging failure, got %v", err)
	}

	req1 := waitForRequest(t, client, time.Second)
	if req1.CurrentRevision != 7 {
		t.Fatalf("expected first request revision 7, got %d", req1.CurrentRevision)
	}

	applied, err := mem.LoadAppliedSnapshot()
	if err != nil {
		t.Fatalf("failed to load applied snapshot: %v", err)
	}
	if !reflect.DeepEqual(applied, previousApplied) {
		t.Fatalf("expected applied snapshot unchanged after staging failure, got %+v", applied)
	}

	state, err := mem.LoadRuntimeState()
	if err != nil {
		t.Fatalf("failed to load runtime state: %v", err)
	}
	if state.CurrentRevision != previousApplied.Revision {
		t.Fatalf("expected persisted current revision %d, got %d", previousApplied.Revision, state.CurrentRevision)
	}
	if state.Metadata["current_revision"] != "7" {
		t.Fatalf("expected persisted metadata current_revision 7, got %q", state.Metadata["current_revision"])
	}
	if state.Metadata["last_sync_error"] != "stage failed" {
		t.Fatalf("expected staging failure metadata, got %v", state.Metadata)
	}
	if state.Metadata["foo"] != "bar" {
		t.Fatalf("expected unrelated metadata preserved, got %v", state.Metadata)
	}

	desired, err := mem.LoadDesiredSnapshot()
	if err != nil {
		t.Fatalf("failed to load desired snapshot: %v", err)
	}
	if !reflect.DeepEqual(desired.VersionPackage, &model.VersionPackage{
		Platform: "linux-amd64",
		URL:      "https://example.com/nre-agent",
		SHA256:   "abc123",
	}) {
		t.Fatalf("expected desired snapshot to persist version package across failures, got %+v", desired.VersionPackage)
	}

	if err := app.performSync(ctx); err == nil || err.Error() != "stage failed" {
		t.Fatalf("expected second staging failure retry, got %v", err)
	}

	req2 := waitForRequest(t, client, time.Second)
	if req2.CurrentRevision != 7 {
		t.Fatalf("expected next heartbeat revision to stay fail-closed at 7, got %d", req2.CurrentRevision)
	}
	if len(updater.calls) != 2 {
		t.Fatalf("expected updater retry on omitted package fields, got %d calls", len(updater.calls))
	}
}

func TestPerformSyncRelayListenerChangeReappliesHTTPRelayAndL4FromUnifiedSnapshotActivation(t *testing.T) {
	cfg := Config{HeartbeatInterval: time.Hour}
	mem := store.NewInMemory()

	previousApplied := Snapshot{
		DesiredVersion: "stable",
		Revision:       7,
		Rules: []model.HTTPRule{{
			FrontendURL: "https://relay-http.example.com",
			Backends: []model.HTTPBackend{
				{URL: "http://10.0.0.10:8096"},
			},
			RelayChain: []int{51},
		}},
		L4Rules: []model.L4Rule{{
			Protocol:   "tcp",
			ListenHost: "127.0.0.1",
			ListenPort: 19000,
			Backends: []model.L4Backend{
				{Host: "10.0.0.20", Port: 9000},
			},
			RelayChain: []int{51},
		}},
		RelayListeners: []model.RelayListener{{
			ID:         51,
			AgentID:    "agent-a",
			Name:       "relay-a",
			ListenHost: "127.0.0.1",
			BindHosts:  []string{"127.0.0.1"},
			ListenPort: 9443,
			PublicHost: "relay-a.example.com",
			PublicPort: 29443,
			Enabled:    true,
			TLSMode:    "pin_only",
			PinSet: []model.RelayPin{{
				Type:  "sha256",
				Value: "pin-value",
			}},
		}},
	}
	if err := mem.SaveAppliedSnapshot(previousApplied); err != nil {
		t.Fatalf("failed to seed applied snapshot: %v", err)
	}

	client := newTestSyncClient(nil, syncResponse{snapshot: Snapshot{
		DesiredVersion: "stable",
		Revision:       8,
		RelayListeners: []model.RelayListener{{
			ID:         51,
			AgentID:    "agent-a",
			Name:       "relay-a",
			ListenHost: "127.0.0.1",
			BindHosts:  []string{"127.0.0.1"},
			ListenPort: 9443,
			PublicHost: "relay-a.example.com",
			PublicPort: 39443,
			Enabled:    true,
			TLSMode:    "pin_only",
			PinSet: []model.RelayPin{{
				Type:  "sha256",
				Value: "pin-value",
			}},
		}},
	}})
	httpApplier := &testHTTPApplier{}
	l4Applier := &testL4Applier{}
	relayApplier := &testRelayApplier{}
	app := newAppWithHTTPDeps(cfg, mem, client, httpApplier, nil, l4Applier, relayApplier)

	ctx := context.Background()
	if err := app.runtime.Apply(ctx, Snapshot{}, previousApplied); err != nil {
		t.Fatalf("failed to seed runtime: %v", err)
	}

	if err := app.performSync(ctx); err != nil {
		t.Fatalf("performSync returned error: %v", err)
	}

	httpCalls := httpApplier.snapshotCalls()
	if len(httpCalls) != 2 {
		t.Fatalf("expected startup and relay-change http apply calls, got %d", len(httpCalls))
	}
	l4Calls := l4Applier.snapshotCalls()
	if len(l4Calls) != 2 {
		t.Fatalf("expected startup and relay-change l4 apply calls, got %d", len(l4Calls))
	}
	relayCalls := relayApplier.snapshotCalls()
	if len(relayCalls) != 2 {
		t.Fatalf("expected startup and relay-change relay apply calls, got %d", len(relayCalls))
	}
	if got := relayCalls[1].listeners[0].PublicPort; got != 39443 {
		t.Fatalf("expected updated relay listener to be applied, got public_port=%d", got)
	}
}

type syncResponse struct {
	snapshot Snapshot
	err      error
}

type applyCall struct {
	bundles  []model.ManagedCertificateBundle
	policies []model.ManagedCertificatePolicy
}

type l4ApplyCall struct {
	rules []model.L4Rule
}

type relayApplyCall struct {
	listeners []model.RelayListener
}

type updateCall struct {
	desiredVersion string
	pkg            model.VersionPackage
}

type httpApplyCall struct {
	rules []model.HTTPRule
}

type testCertificateApplier struct {
	mu        sync.Mutex
	calls     []applyCall
	applyErr  error
	reports   []model.ManagedCertificateReport
	reportErr error
	closed    int
}

func (a *testCertificateApplier) Apply(_ context.Context, bundles []model.ManagedCertificateBundle, policies []model.ManagedCertificatePolicy) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.calls = append(a.calls, applyCall{
		bundles:  append([]model.ManagedCertificateBundle(nil), bundles...),
		policies: append([]model.ManagedCertificatePolicy(nil), policies...),
	})
	return a.applyErr
}

func (a *testCertificateApplier) snapshotCalls() []applyCall {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]applyCall, len(a.calls))
	copy(out, a.calls)
	return out
}

func (a *testCertificateApplier) ManagedCertificateReports(context.Context) ([]model.ManagedCertificateReport, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.reportErr != nil {
		return nil, a.reportErr
	}
	out := make([]model.ManagedCertificateReport, len(a.reports))
	copy(out, a.reports)
	return out, nil
}

func (a *testCertificateApplier) Close() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.closed++
	return nil
}

func (a *testCertificateApplier) closeCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.closed
}

type testL4Applier struct {
	mu       sync.Mutex
	calls    []l4ApplyCall
	applyErr error
}

func (a *testL4Applier) Apply(_ context.Context, rules []model.L4Rule) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	var copied []model.L4Rule
	if rules != nil {
		copied = make([]model.L4Rule, len(rules))
		copy(copied, rules)
	}
	a.calls = append(a.calls, l4ApplyCall{
		rules: copied,
	})
	return a.applyErr
}

func (a *testL4Applier) snapshotCalls() []l4ApplyCall {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]l4ApplyCall, len(a.calls))
	copy(out, a.calls)
	return out
}

func (a *testL4Applier) Close() error {
	return nil
}

type testRelayApplier struct {
	mu       sync.Mutex
	calls    []relayApplyCall
	applyErr error
}

func (a *testRelayApplier) Apply(_ context.Context, listeners []model.RelayListener) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	var copied []model.RelayListener
	if listeners != nil {
		copied = make([]model.RelayListener, len(listeners))
		copy(copied, listeners)
	}
	a.calls = append(a.calls, relayApplyCall{
		listeners: copied,
	})
	return a.applyErr
}

func (a *testRelayApplier) snapshotCalls() []relayApplyCall {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]relayApplyCall, len(a.calls))
	copy(out, a.calls)
	return out
}

func (a *testRelayApplier) Close() error {
	return nil
}

type testHTTPApplier struct {
	mu         sync.Mutex
	calls      []httpApplyCall
	applyErr   error
	failOnCall int
}

func (a *testHTTPApplier) Apply(_ context.Context, rules []model.HTTPRule) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	var copied []model.HTTPRule
	if rules != nil {
		copied = make([]model.HTTPRule, len(rules))
		copy(copied, rules)
		for i, rule := range rules {
			if rule.CustomHeaders != nil {
				copied[i].CustomHeaders = make([]model.HTTPHeader, len(rule.CustomHeaders))
				copy(copied[i].CustomHeaders, rule.CustomHeaders)
			}
		}
	}
	a.calls = append(a.calls, httpApplyCall{
		rules: copied,
	})
	if a.applyErr != nil && (a.failOnCall == 0 || len(a.calls) == a.failOnCall) {
		return a.applyErr
	}
	return nil
}

func (a *testHTTPApplier) snapshotCalls() []httpApplyCall {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]httpApplyCall, len(a.calls))
	copy(out, a.calls)
	return out
}

func (a *testHTTPApplier) Close() error {
	return nil
}

type testUpdater struct {
	calls       []updateCall
	stagedPath  string
	stageErr    error
	activateErr error
}

func (u *testUpdater) Stage(_ context.Context, pkg model.VersionPackage) (string, error) {
	u.calls = append(u.calls, updateCall{
		pkg: pkg,
	})
	if u.stageErr != nil {
		return "", u.stageErr
	}
	if u.stagedPath == "" {
		u.stagedPath = "staged/nre-agent"
	}
	return u.stagedPath, nil
}

func (u *testUpdater) Activate(_ string, desiredVersion string) error {
	if len(u.calls) == 0 {
		u.calls = append(u.calls, updateCall{})
	}
	u.calls[len(u.calls)-1].desiredVersion = desiredVersion
	return u.activateErr
}

type testSyncClient struct {
	mu        sync.Mutex
	responses []syncResponse
	fallback  syncResponse
	callCount int32
	reqCh     chan SyncRequest
}

func newTestSyncClient(responses []syncResponse, fallback syncResponse) *testSyncClient {
	return &testSyncClient{
		responses: append([]syncResponse(nil), responses...),
		fallback:  fallback,
		reqCh:     make(chan SyncRequest, 16),
	}
}

func (c *testSyncClient) Sync(_ context.Context, request SyncRequest) (Snapshot, error) {
	atomic.AddInt32(&c.callCount, 1)
	select {
	case c.reqCh <- request:
	default:
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.responses) > 0 {
		resp := c.responses[0]
		c.responses = c.responses[1:]
		return resp.snapshot, resp.err
	}
	return c.fallback.snapshot, c.fallback.err
}

func waitForRequest(t *testing.T, client *testSyncClient, timeout time.Duration) SyncRequest {
	select {
	case req := <-client.reqCh:
		return req
	case <-time.After(timeout):
		t.Fatalf("timed out waiting for sync request")
	}
	return SyncRequest{}
}

func waitForCalls(t *testing.T, client *testSyncClient, target int, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if int(atomic.LoadInt32(&client.callCount)) >= target {
			return
		}
		time.Sleep(1 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d sync calls", target)
}

func waitForRuntimeState(t *testing.T, timeout time.Duration, predicate func() bool, failureMessage func() string) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if predicate() {
			return
		}
		time.Sleep(1 * time.Millisecond)
	}
	t.Fatal(failureMessage())
}

func waitForLastSyncError(t *testing.T, timeout time.Duration, load func() (store.RuntimeState, error), expected string) store.RuntimeState {
	t.Helper()

	var state store.RuntimeState
	waitForRuntimeState(t, timeout, func() bool {
		current, err := load()
		if err != nil {
			t.Fatalf("failed to load runtime state: %v", err)
		}
		state = current
		return current.Metadata["last_sync_error"] == expected
	}, func() string {
		return "expected last_sync_error metadata to be persisted"
	})

	return state
}

func TestPerformSyncIncludesApplyStatusAndManagedCertificateReports(t *testing.T) {
	cfg := Config{CurrentVersion: "1.0.0"}
	mem := store.NewInMemory()
	applied := Snapshot{
		DesiredVersion: "1.0.0",
		Revision:       7,
	}
	if err := mem.SaveAppliedSnapshot(applied); err != nil {
		t.Fatalf("failed to seed applied snapshot: %v", err)
	}
	if err := mem.SaveRuntimeState(store.RuntimeState{
		CurrentRevision: applied.Revision,
		Metadata: map[string]string{
			"current_revision":    "7",
			"last_apply_revision": "6",
			"last_apply_status":   "error",
			"last_apply_message":  "previous apply failed",
		},
	}); err != nil {
		t.Fatalf("failed to seed runtime state: %v", err)
	}

	client := newTestSyncClient(nil, syncResponse{snapshot: Snapshot{DesiredVersion: "ok", Revision: 7}})
	applier := &testCertificateApplier{
		reports: []model.ManagedCertificateReport{{
			ID:           21,
			Domain:       "sync.example.com",
			Status:       "active",
			MaterialHash: "hash-21",
		}},
	}
	app := newAppWithDeps(cfg, mem, client, applier, nil, nil)

	if err := app.performSync(context.Background()); err != nil {
		t.Fatalf("performSync() error = %v", err)
	}

	req := waitForRequest(t, client, time.Second)
	if req.CurrentRevision != 7 {
		t.Fatalf("CurrentRevision = %d", req.CurrentRevision)
	}
	if req.LastApplyRevision != 6 || req.LastApplyStatus != "error" || req.LastApplyMessage != "previous apply failed" {
		t.Fatalf("unexpected apply metadata in sync request: %+v", req)
	}
	if len(req.ManagedCertificateReports) != 1 || req.ManagedCertificateReports[0].ID != 21 {
		t.Fatalf("unexpected managed certificate reports in sync request: %+v", req.ManagedCertificateReports)
	}
}

func TestRunAppliesManagedCertificatesFromSyncedSnapshot(t *testing.T) {
	cfg := Config{HeartbeatInterval: 5 * time.Millisecond}
	mem := store.NewInMemory()
	expected := Snapshot{
		DesiredVersion: "2.0",
		Revision:       9,
		Certificates: []model.ManagedCertificateBundle{{
			ID:       21,
			Domain:   "sync.example.com",
			Revision: 3,
			CertPEM:  "CERTIFICATE",
			KeyPEM:   "PRIVATEKEY",
		}},
		CertificatePolicies: []model.ManagedCertificatePolicy{{
			ID:              21,
			Domain:          "sync.example.com",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "local_http01",
			Status:          "issued",
			Revision:        3,
			Usage:           "relay_ca",
			CertificateType: "internal_ca",
			SelfSigned:      true,
		}},
	}
	client := newTestSyncClient(nil, syncResponse{snapshot: expected})
	applier := &testCertificateApplier{}
	app := newAppWithDeps(cfg, mem, client, applier, nil, nil)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- app.Run(ctx)
	}()

	waitForCalls(t, client, 1, time.Second)

	calls := applier.snapshotCalls()
	if len(calls) != 1 {
		t.Fatalf("expected one certificate apply call, got %d", len(calls))
	}
	if !reflect.DeepEqual(calls[0].bundles, expected.Certificates) {
		t.Fatalf("unexpected bundles: %+v", calls[0].bundles)
	}
	if !reflect.DeepEqual(calls[0].policies, expected.CertificatePolicies) {
		t.Fatalf("unexpected policies: %+v", calls[0].policies)
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
}

func TestRunHydratesManagedCertificatesFromStoredAppliedSnapshot(t *testing.T) {
	cfg := Config{HeartbeatInterval: 5 * time.Millisecond}
	mem := store.NewInMemory()
	stored := Snapshot{
		DesiredVersion: "stored",
		Revision:       5,
		Rules: []model.HTTPRule{{
			FrontendURL: "https://frontend.example.com",
			BackendURL:  "http://127.0.0.1:8096",
			Revision:    2,
		}},
		L4Rules: []model.L4Rule{{
			Protocol:     "tcp",
			ListenHost:   "127.0.0.1",
			ListenPort:   9000,
			UpstreamHost: "127.0.0.1",
			UpstreamPort: 9001,
			Revision:     4,
		}},
		RelayListeners: []model.RelayListener{{
			ID:         31,
			AgentID:    "remote-agent-5",
			Name:       "relay-a",
			ListenHost: "127.0.0.1",
			ListenPort: 9443,
			Enabled:    true,
			TLSMode:    "pin_only",
			PinSet: []model.RelayPin{{
				Type:  "sha256",
				Value: "pin-value",
			}},
			Revision: 7,
		}},
		Certificates: []model.ManagedCertificateBundle{{
			ID:       41,
			Domain:   "stored.example.com",
			Revision: 1,
			CertPEM:  "CERTIFICATE",
			KeyPEM:   "PRIVATEKEY",
		}},
		CertificatePolicies: []model.ManagedCertificatePolicy{{
			ID:              41,
			Domain:          "stored.example.com",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "local_http01",
			Revision:        1,
			Usage:           "https",
			CertificateType: "uploaded",
		}},
	}
	if err := mem.SaveAppliedSnapshot(stored); err != nil {
		t.Fatalf("failed to seed applied snapshot: %v", err)
	}
	if err := mem.SaveDesiredSnapshot(Snapshot{
		DesiredVersion: "desired",
		Revision:       9,
		Certificates: []model.ManagedCertificateBundle{{
			ID:       99,
			Domain:   "desired.example.com",
			Revision: 9,
			CertPEM:  "OTHER_CERTIFICATE",
			KeyPEM:   "OTHER_PRIVATEKEY",
		}},
		CertificatePolicies: []model.ManagedCertificatePolicy{{
			ID:              99,
			Domain:          "desired.example.com",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "local_http01",
			Revision:        9,
			Usage:           "https",
			CertificateType: "uploaded",
		}},
	}); err != nil {
		t.Fatalf("failed to seed desired snapshot: %v", err)
	}

	client := newTestSyncClient(nil, syncResponse{snapshot: Snapshot{DesiredVersion: "ok"}})
	applier := &testCertificateApplier{}
	app := newAppWithDeps(cfg, mem, client, applier, nil, nil)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- app.Run(ctx)
	}()

	waitForCalls(t, client, 1, time.Second)

	calls := applier.snapshotCalls()
	if len(calls) < 1 {
		t.Fatal("expected at least one certificate apply call")
	}
	if !reflect.DeepEqual(calls[0].bundles, stored.Certificates) {
		t.Fatalf("unexpected hydrated bundles: %+v", calls[0].bundles)
	}
	if !reflect.DeepEqual(calls[0].policies, stored.CertificatePolicies) {
		t.Fatalf("unexpected hydrated policies: %+v", calls[0].policies)
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
}

func TestRunDoesNotApplyManagedCertificatesWhenHeartbeatOmitsPayload(t *testing.T) {
	cfg := Config{HeartbeatInterval: 5 * time.Millisecond}
	mem := store.NewInMemory()
	if err := mem.SaveAppliedSnapshot(Snapshot{DesiredVersion: "baseline"}); err != nil {
		t.Fatalf("failed to seed applied snapshot: %v", err)
	}

	client := newTestSyncClient(nil, syncResponse{snapshot: Snapshot{DesiredVersion: "ok", Revision: 7}})
	applier := &testCertificateApplier{}
	app := newAppWithDeps(cfg, mem, client, applier, nil, nil)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- app.Run(ctx)
	}()

	waitForCalls(t, client, 1, time.Second)

	if calls := applier.snapshotCalls(); len(calls) != 0 {
		t.Fatalf("expected no certificate apply calls for omitted payload, got %d", len(calls))
	}

	snap, err := mem.LoadDesiredSnapshot()
	if err != nil {
		t.Fatalf("failed to load desired snapshot: %v", err)
	}
	if snap.Certificates != nil {
		t.Fatalf("expected omitted certificate payload to stay nil when nothing was stored before, got %+v", snap.Certificates)
	}
	if snap.CertificatePolicies != nil {
		t.Fatalf("expected omitted certificate policy payload to stay nil when nothing was stored before, got %+v", snap.CertificatePolicies)
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
}

func TestRunPreservesStoredManagedCertificatePayloadWhenHeartbeatOmitsFields(t *testing.T) {
	cfg := Config{HeartbeatInterval: 5 * time.Millisecond}
	mem := store.NewInMemory()
	stored := Snapshot{
		DesiredVersion: "stored",
		Revision:       5,
		Certificates: []model.ManagedCertificateBundle{{
			ID:       41,
			Domain:   "stored.example.com",
			Revision: 1,
			CertPEM:  "CERTIFICATE",
			KeyPEM:   "PRIVATEKEY",
		}},
		CertificatePolicies: []model.ManagedCertificatePolicy{{
			ID:              41,
			Domain:          "stored.example.com",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "local_http01",
			Revision:        1,
			Usage:           "https",
			CertificateType: "uploaded",
		}},
	}
	if err := mem.SaveAppliedSnapshot(stored); err != nil {
		t.Fatalf("failed to seed applied snapshot: %v", err)
	}
	if err := mem.SaveDesiredSnapshot(stored); err != nil {
		t.Fatalf("failed to seed desired snapshot: %v", err)
	}

	client := newTestSyncClient(nil, syncResponse{snapshot: Snapshot{DesiredVersion: "ok", Revision: 7}})
	applier := &testCertificateApplier{}
	app := newAppWithDeps(cfg, mem, client, applier, nil, nil)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- app.Run(ctx)
	}()

	waitForCalls(t, client, 1, time.Second)

	calls := applier.snapshotCalls()
	if len(calls) < 1 {
		t.Fatal("expected startup hydration call")
	}
	if len(calls) != 1 {
		t.Fatalf("expected only startup hydration call when heartbeat omits payload, got %d", len(calls))
	}

	persisted, err := mem.LoadDesiredSnapshot()
	if err != nil {
		t.Fatalf("failed to load desired snapshot: %v", err)
	}
	if !reflect.DeepEqual(persisted.Certificates, stored.Certificates) {
		t.Fatalf("expected stored certificates to be preserved, got %+v", persisted.Certificates)
	}
	if !reflect.DeepEqual(persisted.CertificatePolicies, stored.CertificatePolicies) {
		t.Fatalf("expected stored certificate policies to be preserved, got %+v", persisted.CertificatePolicies)
	}
	if !reflect.DeepEqual(persisted.Rules, stored.Rules) {
		t.Fatalf("expected stored rules to be preserved, got %+v", persisted.Rules)
	}
	if !reflect.DeepEqual(persisted.L4Rules, stored.L4Rules) {
		t.Fatalf("expected stored l4 rules to be preserved, got %+v", persisted.L4Rules)
	}
	if !reflect.DeepEqual(persisted.RelayListeners, stored.RelayListeners) {
		t.Fatalf("expected stored relay listeners to be preserved, got %+v", persisted.RelayListeners)
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
}

func TestRunRecordsCertificateApplyFailuresInRuntimeState(t *testing.T) {
	cfg := Config{HeartbeatInterval: 5 * time.Millisecond}
	mem := store.NewInMemory()
	if err := mem.SaveAppliedSnapshot(Snapshot{DesiredVersion: "baseline"}); err != nil {
		t.Fatalf("failed to seed applied snapshot: %v", err)
	}

	client := newTestSyncClient(nil, syncResponse{snapshot: Snapshot{
		DesiredVersion: "2.0",
		Revision:       9,
		Certificates:   []model.ManagedCertificateBundle{},
	}})
	applier := &testCertificateApplier{applyErr: errors.New("cert apply failed")}
	app := newAppWithDeps(cfg, mem, client, applier, nil, nil)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- app.Run(ctx)
	}()

	waitForCalls(t, client, 1, time.Second)

	state := waitForLastSyncError(t, time.Second, mem.LoadRuntimeState, "cert apply failed")
	if state.Metadata["last_sync_error"] != "cert apply failed" {
		t.Fatalf("expected certificate apply failure metadata, got %v", state.Metadata)
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
}

func TestRunRecordsStartupCertificateHydrationFailuresInRuntimeState(t *testing.T) {
	cfg := Config{HeartbeatInterval: 5 * time.Millisecond}
	mem := store.NewInMemory()
	stored := Snapshot{
		DesiredVersion: "stored",
		Revision:       5,
		Certificates:   []model.ManagedCertificateBundle{},
	}
	if err := mem.SaveAppliedSnapshot(stored); err != nil {
		t.Fatalf("failed to seed applied snapshot: %v", err)
	}

	client := newTestSyncClient(nil, syncResponse{snapshot: Snapshot{DesiredVersion: "ok"}})
	applier := &testCertificateApplier{applyErr: errors.New("startup cert apply failed")}
	app := newAppWithDeps(cfg, mem, client, applier, nil, nil)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	err := app.Run(ctx)
	if err == nil || err.Error() != "startup cert apply failed" {
		t.Fatalf("expected startup certificate apply error, got %v", err)
	}

	state, loadErr := mem.LoadRuntimeState()
	if loadErr != nil {
		t.Fatalf("failed to load runtime state: %v", loadErr)
	}
	if state.Metadata["last_sync_error"] != "startup cert apply failed" {
		t.Fatalf("expected startup certificate apply failure metadata, got %v", state.Metadata)
	}
}

func TestRunAppliesHTTPRulesFromSyncedSnapshot(t *testing.T) {
	cfg := Config{HeartbeatInterval: 5 * time.Millisecond}
	mem := store.NewInMemory()
	expected := Snapshot{
		DesiredVersion: "2.0",
		Revision:       9,
		Rules: []model.HTTPRule{{
			FrontendURL:   "http://edge.example.test:18080",
			BackendURL:    "http://127.0.0.1:8096",
			ProxyRedirect: true,
			Revision:      4,
		}},
	}
	client := newTestSyncClient(nil, syncResponse{snapshot: expected})
	httpApplier := &testHTTPApplier{}
	app := newAppWithHTTPDeps(cfg, mem, client, httpApplier, nil, nil, nil)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- app.Run(ctx)
	}()

	waitForCalls(t, client, 1, time.Second)

	calls := httpApplier.snapshotCalls()
	if len(calls) != 1 {
		t.Fatalf("expected one http apply call, got %d", len(calls))
	}
	if !reflect.DeepEqual(calls[0].rules, expected.Rules) {
		t.Fatalf("unexpected http rules: %+v", calls[0].rules)
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
}

func TestRunHydratesHTTPRulesFromStoredAppliedSnapshot(t *testing.T) {
	cfg := Config{HeartbeatInterval: 5 * time.Millisecond}
	mem := store.NewInMemory()
	stored := Snapshot{
		DesiredVersion: "stored",
		Revision:       5,
		Rules: []model.HTTPRule{{
			FrontendURL:      "http://edge.example.test:18080",
			BackendURL:       "http://127.0.0.1:8096",
			PassProxyHeaders: true,
			Revision:         4,
		}},
	}
	if err := mem.SaveAppliedSnapshot(stored); err != nil {
		t.Fatalf("failed to seed applied snapshot: %v", err)
	}
	if err := mem.SaveDesiredSnapshot(Snapshot{
		DesiredVersion: "desired",
		Revision:       9,
		Rules: []model.HTTPRule{{
			FrontendURL: "http://desired.example.test:28080",
			BackendURL:  "http://127.0.0.1:8097",
			Revision:    9,
		}},
	}); err != nil {
		t.Fatalf("failed to seed desired snapshot: %v", err)
	}

	client := newTestSyncClient(nil, syncResponse{snapshot: Snapshot{DesiredVersion: "ok"}})
	httpApplier := &testHTTPApplier{}
	app := newAppWithHTTPDeps(cfg, mem, client, httpApplier, nil, nil, nil)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- app.Run(ctx)
	}()

	waitForCalls(t, client, 1, time.Second)

	calls := httpApplier.snapshotCalls()
	if len(calls) != 1 {
		t.Fatalf("expected one startup http apply call, got %d", len(calls))
	}
	if !reflect.DeepEqual(calls[0].rules, stored.Rules) {
		t.Fatalf("unexpected hydrated http rules: %+v", calls[0].rules)
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
}

func TestRunDoesNotApplyHTTPWhenHeartbeatOmitsPayload(t *testing.T) {
	cfg := Config{HeartbeatInterval: 5 * time.Millisecond}
	mem := store.NewInMemory()
	stored := Snapshot{
		DesiredVersion: "stored",
		Revision:       5,
		Rules: []model.HTTPRule{{
			FrontendURL: "http://edge.example.test:18080",
			BackendURL:  "http://127.0.0.1:8096",
			Revision:    4,
		}},
	}
	if err := mem.SaveAppliedSnapshot(stored); err != nil {
		t.Fatalf("failed to seed applied snapshot: %v", err)
	}
	if err := mem.SaveDesiredSnapshot(stored); err != nil {
		t.Fatalf("failed to seed desired snapshot: %v", err)
	}

	client := newTestSyncClient(nil, syncResponse{snapshot: Snapshot{DesiredVersion: "ok", Revision: 7}})
	httpApplier := &testHTTPApplier{}
	app := newAppWithHTTPDeps(cfg, mem, client, httpApplier, nil, nil, nil)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- app.Run(ctx)
	}()

	waitForCalls(t, client, 1, time.Second)

	calls := httpApplier.snapshotCalls()
	if len(calls) != 1 {
		t.Fatalf("expected only startup hydration call when heartbeat omits http rules, got %d", len(calls))
	}

	persisted, err := mem.LoadDesiredSnapshot()
	if err != nil {
		t.Fatalf("failed to load desired snapshot: %v", err)
	}
	if !reflect.DeepEqual(persisted.Rules, stored.Rules) {
		t.Fatalf("expected stored http rules to be preserved, got %+v", persisted.Rules)
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
}

func TestRunAppliesExplicitEmptyHTTPRules(t *testing.T) {
	cfg := Config{HeartbeatInterval: 5 * time.Millisecond}
	mem := store.NewInMemory()
	stored := Snapshot{
		DesiredVersion: "stored",
		Revision:       5,
		Rules: []model.HTTPRule{{
			FrontendURL: "http://edge.example.test:18080",
			BackendURL:  "http://127.0.0.1:8096",
			Revision:    4,
		}},
	}
	if err := mem.SaveAppliedSnapshot(stored); err != nil {
		t.Fatalf("failed to seed applied snapshot: %v", err)
	}

	client := newTestSyncClient(nil, syncResponse{snapshot: Snapshot{
		DesiredVersion: "ok",
		Revision:       7,
		Rules:          []model.HTTPRule{},
	}})
	httpApplier := &testHTTPApplier{}
	app := newAppWithHTTPDeps(cfg, mem, client, httpApplier, nil, nil, nil)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- app.Run(ctx)
	}()

	waitForCalls(t, client, 1, time.Second)

	calls := httpApplier.snapshotCalls()
	if len(calls) != 2 {
		t.Fatalf("expected startup and clear http apply calls, got %d", len(calls))
	}
	if calls[1].rules == nil || len(calls[1].rules) != 0 {
		t.Fatalf("expected explicit empty http rules on clear, got %+v", calls[1].rules)
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
}

func TestRunRecordsHTTPApplyFailuresInRuntimeState(t *testing.T) {
	cfg := Config{HeartbeatInterval: 5 * time.Millisecond}
	mem := store.NewInMemory()
	if err := mem.SaveAppliedSnapshot(Snapshot{DesiredVersion: "baseline"}); err != nil {
		t.Fatalf("failed to seed applied snapshot: %v", err)
	}

	client := newTestSyncClient(nil, syncResponse{snapshot: Snapshot{
		DesiredVersion: "2.0",
		Revision:       9,
		Rules:          []model.HTTPRule{},
	}})
	httpApplier := &testHTTPApplier{applyErr: errors.New("http apply failed")}
	app := newAppWithHTTPDeps(cfg, mem, client, httpApplier, nil, nil, nil)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- app.Run(ctx)
	}()

	waitForCalls(t, client, 1, time.Second)

	state := waitForLastSyncError(t, time.Second, mem.LoadRuntimeState, "http apply failed")
	if state.Metadata["last_sync_error"] != "http apply failed" {
		t.Fatalf("expected http apply failure metadata, got %v", state.Metadata)
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
}

func TestRunRecordsStartupHTTPHydrationFailuresInRuntimeState(t *testing.T) {
	cfg := Config{HeartbeatInterval: 5 * time.Millisecond}
	mem := store.NewInMemory()
	stored := Snapshot{
		DesiredVersion: "stored",
		Revision:       5,
		Rules:          []model.HTTPRule{},
	}
	if err := mem.SaveAppliedSnapshot(stored); err != nil {
		t.Fatalf("failed to seed applied snapshot: %v", err)
	}

	client := newTestSyncClient(nil, syncResponse{snapshot: Snapshot{DesiredVersion: "ok"}})
	httpApplier := &testHTTPApplier{applyErr: errors.New("startup http apply failed")}
	app := newAppWithHTTPDeps(cfg, mem, client, httpApplier, nil, nil, nil)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	err := app.Run(ctx)
	if err == nil || err.Error() != "startup http apply failed" {
		t.Fatalf("expected startup http apply error, got %v", err)
	}

	state, loadErr := mem.LoadRuntimeState()
	if loadErr != nil {
		t.Fatalf("failed to load runtime state: %v", loadErr)
	}
	if state.Metadata["last_sync_error"] != "startup http apply failed" {
		t.Fatalf("expected startup http apply failure metadata, got %v", state.Metadata)
	}
}

func TestRunAppliesL4RulesFromSyncedSnapshot(t *testing.T) {
	cfg := Config{HeartbeatInterval: 5 * time.Millisecond}
	mem := store.NewInMemory()
	expected := Snapshot{
		DesiredVersion: "2.0",
		Revision:       9,
		L4Rules: []model.L4Rule{{
			Protocol:     "tcp",
			ListenHost:   "127.0.0.1",
			ListenPort:   9000,
			UpstreamHost: "127.0.0.1",
			UpstreamPort: 9001,
			Revision:     4,
		}},
	}
	client := newTestSyncClient(nil, syncResponse{snapshot: expected})
	l4Applier := &testL4Applier{}
	app := newAppWithDeps(cfg, mem, client, nil, l4Applier, nil)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- app.Run(ctx)
	}()

	waitForCalls(t, client, 1, time.Second)

	calls := l4Applier.snapshotCalls()
	if len(calls) != 1 {
		t.Fatalf("expected one l4 apply call, got %d", len(calls))
	}
	if !reflect.DeepEqual(calls[0].rules, expected.L4Rules) {
		t.Fatalf("unexpected l4 rules: %+v", calls[0].rules)
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
}

func TestRunHydratesL4RulesFromStoredAppliedSnapshot(t *testing.T) {
	cfg := Config{HeartbeatInterval: 5 * time.Millisecond}
	mem := store.NewInMemory()
	stored := Snapshot{
		DesiredVersion: "stored",
		Revision:       5,
		L4Rules: []model.L4Rule{{
			Protocol:     "tcp",
			ListenHost:   "127.0.0.1",
			ListenPort:   9000,
			UpstreamHost: "127.0.0.1",
			UpstreamPort: 9001,
			Revision:     4,
		}},
	}
	if err := mem.SaveAppliedSnapshot(stored); err != nil {
		t.Fatalf("failed to seed applied snapshot: %v", err)
	}
	if err := mem.SaveDesiredSnapshot(Snapshot{
		DesiredVersion: "desired",
		Revision:       9,
		L4Rules: []model.L4Rule{{
			Protocol:     "tcp",
			ListenHost:   "127.0.0.2",
			ListenPort:   9900,
			UpstreamHost: "127.0.0.2",
			UpstreamPort: 9901,
			Revision:     9,
		}},
	}); err != nil {
		t.Fatalf("failed to seed desired snapshot: %v", err)
	}

	client := newTestSyncClient(nil, syncResponse{snapshot: Snapshot{DesiredVersion: "ok"}})
	l4Applier := &testL4Applier{}
	app := newAppWithDeps(cfg, mem, client, nil, l4Applier, nil)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- app.Run(ctx)
	}()

	waitForCalls(t, client, 1, time.Second)

	calls := l4Applier.snapshotCalls()
	if len(calls) != 1 {
		t.Fatalf("expected one startup l4 apply call, got %d", len(calls))
	}
	if !reflect.DeepEqual(calls[0].rules, stored.L4Rules) {
		t.Fatalf("unexpected hydrated l4 rules: %+v", calls[0].rules)
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
}

func TestRunAppliesRelayListenersFromSyncedSnapshot(t *testing.T) {
	cfg := Config{HeartbeatInterval: 5 * time.Millisecond}
	mem := store.NewInMemory()
	expected := Snapshot{
		DesiredVersion: "2.0",
		Revision:       9,
		RelayListeners: []model.RelayListener{{
			ID:         31,
			AgentID:    "remote-agent-5",
			Name:       "relay-a",
			ListenHost: "127.0.0.1",
			ListenPort: 9443,
			Enabled:    true,
			TLSMode:    "pin_only",
			PinSet: []model.RelayPin{{
				Type:  "sha256",
				Value: "pin-value",
			}},
			Revision: 7,
		}},
	}
	client := newTestSyncClient(nil, syncResponse{snapshot: expected})
	relayApplier := &testRelayApplier{}
	app := newAppWithDeps(cfg, mem, client, nil, nil, relayApplier)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- app.Run(ctx)
	}()

	waitForCalls(t, client, 1, time.Second)

	calls := relayApplier.snapshotCalls()
	if len(calls) != 1 {
		t.Fatalf("expected one relay apply call, got %d", len(calls))
	}
	if !reflect.DeepEqual(calls[0].listeners, expected.RelayListeners) {
		t.Fatalf("unexpected relay listeners: %+v", calls[0].listeners)
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
}

func TestRunHydratesRelayListenersFromStoredAppliedSnapshot(t *testing.T) {
	cfg := Config{HeartbeatInterval: 5 * time.Millisecond}
	mem := store.NewInMemory()
	stored := Snapshot{
		DesiredVersion: "stored",
		Revision:       5,
		RelayListeners: []model.RelayListener{{
			ID:         31,
			AgentID:    "remote-agent-5",
			Name:       "relay-a",
			ListenHost: "127.0.0.1",
			ListenPort: 9443,
			Enabled:    true,
			TLSMode:    "pin_only",
			PinSet: []model.RelayPin{{
				Type:  "sha256",
				Value: "pin-value",
			}},
			Revision: 7,
		}},
	}
	if err := mem.SaveAppliedSnapshot(stored); err != nil {
		t.Fatalf("failed to seed applied snapshot: %v", err)
	}
	if err := mem.SaveDesiredSnapshot(Snapshot{
		DesiredVersion: "desired",
		Revision:       9,
		RelayListeners: []model.RelayListener{{
			ID:         99,
			AgentID:    "desired-agent",
			Name:       "desired-relay",
			ListenHost: "127.0.0.2",
			ListenPort: 9444,
			Enabled:    true,
			TLSMode:    "pin_only",
			PinSet: []model.RelayPin{{
				Type:  "sha256",
				Value: "desired-pin",
			}},
			Revision: 9,
		}},
	}); err != nil {
		t.Fatalf("failed to seed desired snapshot: %v", err)
	}

	client := newTestSyncClient(nil, syncResponse{snapshot: Snapshot{DesiredVersion: "ok"}})
	relayApplier := &testRelayApplier{}
	app := newAppWithDeps(cfg, mem, client, nil, nil, relayApplier)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- app.Run(ctx)
	}()

	waitForCalls(t, client, 1, time.Second)

	calls := relayApplier.snapshotCalls()
	if len(calls) != 1 {
		t.Fatalf("expected one startup relay apply call, got %d", len(calls))
	}
	if !reflect.DeepEqual(calls[0].listeners, stored.RelayListeners) {
		t.Fatalf("unexpected hydrated relay listeners: %+v", calls[0].listeners)
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
}

func TestRunDoesNotApplyL4WhenHeartbeatOmitsPayload(t *testing.T) {
	cfg := Config{HeartbeatInterval: 5 * time.Millisecond}
	mem := store.NewInMemory()
	if err := mem.SaveAppliedSnapshot(Snapshot{DesiredVersion: "baseline"}); err != nil {
		t.Fatalf("failed to seed applied snapshot: %v", err)
	}

	client := newTestSyncClient(nil, syncResponse{snapshot: Snapshot{DesiredVersion: "ok", Revision: 7}})
	l4Applier := &testL4Applier{}
	app := newAppWithDeps(cfg, mem, client, nil, l4Applier, nil)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- app.Run(ctx)
	}()

	waitForCalls(t, client, 1, time.Second)

	if calls := l4Applier.snapshotCalls(); len(calls) != 0 {
		t.Fatalf("expected no l4 apply calls for omitted payload, got %d", len(calls))
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
}

func TestRunDoesNotApplyRelayWhenHeartbeatOmitsPayload(t *testing.T) {
	cfg := Config{HeartbeatInterval: 5 * time.Millisecond}
	mem := store.NewInMemory()
	if err := mem.SaveAppliedSnapshot(Snapshot{DesiredVersion: "baseline"}); err != nil {
		t.Fatalf("failed to seed applied snapshot: %v", err)
	}

	client := newTestSyncClient(nil, syncResponse{snapshot: Snapshot{DesiredVersion: "ok", Revision: 7}})
	relayApplier := &testRelayApplier{}
	app := newAppWithDeps(cfg, mem, client, nil, nil, relayApplier)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- app.Run(ctx)
	}()

	waitForCalls(t, client, 1, time.Second)

	if calls := relayApplier.snapshotCalls(); len(calls) != 0 {
		t.Fatalf("expected no relay apply calls for omitted payload, got %d", len(calls))
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
}

func TestRunAppliesExplicitEmptyL4Rules(t *testing.T) {
	cfg := Config{HeartbeatInterval: 5 * time.Millisecond}
	mem := store.NewInMemory()
	stored := Snapshot{
		DesiredVersion: "stored",
		Revision:       5,
		L4Rules: []model.L4Rule{{
			Protocol:     "tcp",
			ListenHost:   "127.0.0.1",
			ListenPort:   9000,
			UpstreamHost: "127.0.0.1",
			UpstreamPort: 9001,
			Revision:     4,
		}},
	}
	if err := mem.SaveAppliedSnapshot(stored); err != nil {
		t.Fatalf("failed to seed applied snapshot: %v", err)
	}

	client := newTestSyncClient(nil, syncResponse{snapshot: Snapshot{
		DesiredVersion: "ok",
		Revision:       7,
		L4Rules:        []model.L4Rule{},
	}})
	l4Applier := &testL4Applier{}
	app := newAppWithDeps(cfg, mem, client, nil, l4Applier, nil)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- app.Run(ctx)
	}()

	waitForCalls(t, client, 1, time.Second)

	calls := l4Applier.snapshotCalls()
	if len(calls) != 2 {
		t.Fatalf("expected startup and clear l4 apply calls, got %d", len(calls))
	}
	if calls[1].rules == nil || len(calls[1].rules) != 0 {
		t.Fatalf("expected explicit empty l4 rules on clear, got %+v", calls[1].rules)
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
}

func TestRunAppliesExplicitEmptyRelayListeners(t *testing.T) {
	cfg := Config{HeartbeatInterval: 5 * time.Millisecond}
	mem := store.NewInMemory()
	stored := Snapshot{
		DesiredVersion: "stored",
		Revision:       5,
		RelayListeners: []model.RelayListener{{
			ID:         31,
			AgentID:    "remote-agent-5",
			Name:       "relay-a",
			ListenHost: "127.0.0.1",
			ListenPort: 9443,
			Enabled:    true,
			TLSMode:    "pin_only",
			PinSet: []model.RelayPin{{
				Type:  "sha256",
				Value: "pin-value",
			}},
			Revision: 7,
		}},
	}
	if err := mem.SaveAppliedSnapshot(stored); err != nil {
		t.Fatalf("failed to seed applied snapshot: %v", err)
	}

	client := newTestSyncClient(nil, syncResponse{snapshot: Snapshot{
		DesiredVersion: "ok",
		Revision:       7,
		RelayListeners: []model.RelayListener{},
	}})
	relayApplier := &testRelayApplier{}
	app := newAppWithDeps(cfg, mem, client, nil, nil, relayApplier)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- app.Run(ctx)
	}()

	waitForCalls(t, client, 1, time.Second)

	calls := relayApplier.snapshotCalls()
	if len(calls) != 2 {
		t.Fatalf("expected startup and clear relay apply calls, got %d", len(calls))
	}
	if calls[1].listeners == nil || len(calls[1].listeners) != 0 {
		t.Fatalf("expected explicit empty relay listeners on clear, got %+v", calls[1].listeners)
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
}

func TestRunRecordsL4ApplyFailuresInRuntimeState(t *testing.T) {
	cfg := Config{HeartbeatInterval: 5 * time.Millisecond}
	mem := store.NewInMemory()
	if err := mem.SaveAppliedSnapshot(Snapshot{DesiredVersion: "baseline"}); err != nil {
		t.Fatalf("failed to seed applied snapshot: %v", err)
	}

	client := newTestSyncClient(nil, syncResponse{snapshot: Snapshot{
		DesiredVersion: "2.0",
		Revision:       9,
		L4Rules:        []model.L4Rule{},
	}})
	l4Applier := &testL4Applier{applyErr: errors.New("l4 apply failed")}
	app := newAppWithDeps(cfg, mem, client, nil, l4Applier, nil)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- app.Run(ctx)
	}()

	waitForCalls(t, client, 1, time.Second)

	state := waitForLastSyncError(t, time.Second, mem.LoadRuntimeState, "l4 apply failed")
	if state.Metadata["last_sync_error"] != "l4 apply failed" {
		t.Fatalf("expected l4 apply failure metadata, got %v", state.Metadata)
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
}

func TestRunRecordsRelayApplyFailuresInRuntimeState(t *testing.T) {
	cfg := Config{HeartbeatInterval: 5 * time.Millisecond}
	mem := store.NewInMemory()
	if err := mem.SaveAppliedSnapshot(Snapshot{DesiredVersion: "baseline"}); err != nil {
		t.Fatalf("failed to seed applied snapshot: %v", err)
	}

	client := newTestSyncClient(nil, syncResponse{snapshot: Snapshot{
		DesiredVersion: "2.0",
		Revision:       9,
		RelayListeners: []model.RelayListener{},
	}})
	relayApplier := &testRelayApplier{applyErr: errors.New("relay apply failed")}
	app := newAppWithDeps(cfg, mem, client, nil, nil, relayApplier)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- app.Run(ctx)
	}()

	waitForCalls(t, client, 1, time.Second)

	state := waitForLastSyncError(t, time.Second, mem.LoadRuntimeState, "relay apply failed")
	if state.Metadata["last_sync_error"] != "relay apply failed" {
		t.Fatalf("expected relay apply failure metadata, got %v", state.Metadata)
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
}

func TestRunRecordsStartupL4HydrationFailuresInRuntimeState(t *testing.T) {
	cfg := Config{HeartbeatInterval: 5 * time.Millisecond}
	mem := store.NewInMemory()
	stored := Snapshot{
		DesiredVersion: "stored",
		Revision:       5,
		L4Rules:        []model.L4Rule{},
	}
	if err := mem.SaveAppliedSnapshot(stored); err != nil {
		t.Fatalf("failed to seed applied snapshot: %v", err)
	}

	client := newTestSyncClient(nil, syncResponse{snapshot: Snapshot{DesiredVersion: "ok"}})
	l4Applier := &testL4Applier{applyErr: errors.New("startup l4 apply failed")}
	app := newAppWithDeps(cfg, mem, client, nil, l4Applier, nil)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	err := app.Run(ctx)
	if err == nil || err.Error() != "startup l4 apply failed" {
		t.Fatalf("expected startup l4 apply error, got %v", err)
	}

	state, loadErr := mem.LoadRuntimeState()
	if loadErr != nil {
		t.Fatalf("failed to load runtime state: %v", loadErr)
	}
	if state.Metadata["last_sync_error"] != "startup l4 apply failed" {
		t.Fatalf("expected startup l4 apply failure metadata, got %v", state.Metadata)
	}
}

func TestRunRecordsStartupRelayHydrationFailuresInRuntimeState(t *testing.T) {
	cfg := Config{HeartbeatInterval: 5 * time.Millisecond}
	mem := store.NewInMemory()
	stored := Snapshot{
		DesiredVersion: "stored",
		Revision:       5,
		RelayListeners: []model.RelayListener{},
	}
	if err := mem.SaveAppliedSnapshot(stored); err != nil {
		t.Fatalf("failed to seed applied snapshot: %v", err)
	}

	client := newTestSyncClient(nil, syncResponse{snapshot: Snapshot{DesiredVersion: "ok"}})
	relayApplier := &testRelayApplier{applyErr: errors.New("startup relay apply failed")}
	app := newAppWithDeps(cfg, mem, client, nil, nil, relayApplier)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	err := app.Run(ctx)
	if err == nil || err.Error() != "startup relay apply failed" {
		t.Fatalf("expected startup relay apply error, got %v", err)
	}

	state, loadErr := mem.LoadRuntimeState()
	if loadErr != nil {
		t.Fatalf("failed to load runtime state: %v", loadErr)
	}
	if state.Metadata["last_sync_error"] != "startup relay apply failed" {
		t.Fatalf("expected startup relay apply failure metadata, got %v", state.Metadata)
	}
}

type failingStore struct {
	delegate             store.Store
	failOnNthSave        int
	saveCount            int
	failOnNthAppliedSave int
	appliedSaveCount     int
}

func (f *failingStore) SaveDesiredSnapshot(snapshot Snapshot) error {
	f.saveCount++
	if f.failOnNthSave > 0 && f.saveCount == f.failOnNthSave {
		return errors.New("persistence fail")
	}
	return f.delegate.SaveDesiredSnapshot(snapshot)
}

func (f *failingStore) LoadDesiredSnapshot() (Snapshot, error) {
	return f.delegate.LoadDesiredSnapshot()
}

func (f *failingStore) SaveAppliedSnapshot(snapshot Snapshot) error {
	f.appliedSaveCount++
	if f.failOnNthAppliedSave > 0 && f.appliedSaveCount == f.failOnNthAppliedSave {
		return errors.New("applied persistence fail")
	}
	return f.delegate.SaveAppliedSnapshot(snapshot)
}

func (f *failingStore) LoadAppliedSnapshot() (Snapshot, error) {
	return f.delegate.LoadAppliedSnapshot()
}

func (f *failingStore) SaveRuntimeState(state store.RuntimeState) error {
	return f.delegate.SaveRuntimeState(state)
}

func (f *failingStore) LoadRuntimeState() (store.RuntimeState, error) {
	return f.delegate.LoadRuntimeState()
}
