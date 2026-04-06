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

func TestRunRefreshesCurrentRevisionFromRuntimeState(t *testing.T) {
	cfg := Config{HeartbeatInterval: 5 * time.Millisecond}
	mem := store.NewInMemory()
	if err := mem.SaveAppliedSnapshot(Snapshot{DesiredVersion: "baseline"}); err != nil {
		t.Fatalf("failed to seed applied snapshot: %v", err)
	}
	if err := mem.SaveRuntimeState(store.RuntimeState{
		Metadata: map[string]string{"current_revision": "100"},
	}); err != nil {
		t.Fatalf("failed to seed runtime state: %v", err)
	}

	client := newTestSyncClient(nil, syncResponse{snapshot: Snapshot{DesiredVersion: "ok"}})
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

	if err := mem.SaveRuntimeState(store.RuntimeState{
		Metadata: map[string]string{"current_revision": "200"},
	}); err != nil {
		t.Fatalf("failed to update runtime state: %v", err)
	}

	req2 := waitForRequest(t, client, time.Second)
	if req2.CurrentRevision != 200 {
		t.Fatalf("expected second request revision 200, got %d", req2.CurrentRevision)
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
	current, err := mem.LoadRuntimeState()
	if err != nil {
		t.Fatalf("failed to load runtime state: %v", err)
	}
	if current.Metadata["last_sync_error"] != "boom" {
		t.Fatalf("expected failure metadata, got %v", current.Metadata)
	}
	if current.Metadata["foo"] != "bar" {
		t.Fatalf("expected other metadata preserved, got %v", current.Metadata)
	}

	waitForCalls(t, client, 2, time.Second)
	current, err = mem.LoadRuntimeState()
	if err != nil {
		t.Fatalf("failed to load runtime state: %v", err)
	}
	if _, ok := current.Metadata["last_sync_error"]; ok {
		t.Fatalf("expected failure metadata cleared, got %v", current.Metadata)
	}

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

	state, err := fs.LoadRuntimeState()
	if err != nil {
		t.Fatalf("failed to load runtime state: %v", err)
	}
	if state.Metadata["last_sync_error"] != "persistence fail" {
		t.Fatalf("expected persistence failure metadata, got %v", state.Metadata)
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run returned error: %v", err)
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

type testCertificateApplier struct {
	mu       sync.Mutex
	calls    []applyCall
	applyErr error
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

func (a *testCertificateApplier) Close() error {
	return nil
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

func TestRunHydratesManagedCertificatesFromStoredDesiredSnapshot(t *testing.T) {
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
	if err := mem.SaveAppliedSnapshot(Snapshot{DesiredVersion: "baseline"}); err != nil {
		t.Fatalf("failed to seed applied snapshot: %v", err)
	}
	if err := mem.SaveDesiredSnapshot(stored); err != nil {
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
	if err := mem.SaveAppliedSnapshot(Snapshot{DesiredVersion: "baseline"}); err != nil {
		t.Fatalf("failed to seed applied snapshot: %v", err)
	}
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

	state, err := mem.LoadRuntimeState()
	if err != nil {
		t.Fatalf("failed to load runtime state: %v", err)
	}
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
	if err := mem.SaveDesiredSnapshot(stored); err != nil {
		t.Fatalf("failed to seed desired snapshot: %v", err)
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

func TestRunHydratesL4RulesFromStoredDesiredSnapshot(t *testing.T) {
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
	if err := mem.SaveAppliedSnapshot(Snapshot{DesiredVersion: "baseline"}); err != nil {
		t.Fatalf("failed to seed applied snapshot: %v", err)
	}
	if err := mem.SaveDesiredSnapshot(stored); err != nil {
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

func TestRunHydratesRelayListenersFromStoredDesiredSnapshot(t *testing.T) {
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
	if err := mem.SaveAppliedSnapshot(Snapshot{DesiredVersion: "baseline"}); err != nil {
		t.Fatalf("failed to seed applied snapshot: %v", err)
	}
	if err := mem.SaveDesiredSnapshot(stored); err != nil {
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
	if err := mem.SaveAppliedSnapshot(Snapshot{DesiredVersion: "baseline"}); err != nil {
		t.Fatalf("failed to seed applied snapshot: %v", err)
	}
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
	if err := mem.SaveDesiredSnapshot(stored); err != nil {
		t.Fatalf("failed to seed desired snapshot: %v", err)
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
	if err := mem.SaveAppliedSnapshot(Snapshot{DesiredVersion: "baseline"}); err != nil {
		t.Fatalf("failed to seed applied snapshot: %v", err)
	}
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
	if err := mem.SaveDesiredSnapshot(stored); err != nil {
		t.Fatalf("failed to seed desired snapshot: %v", err)
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

	state, err := mem.LoadRuntimeState()
	if err != nil {
		t.Fatalf("failed to load runtime state: %v", err)
	}
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

	state, err := mem.LoadRuntimeState()
	if err != nil {
		t.Fatalf("failed to load runtime state: %v", err)
	}
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
	if err := mem.SaveDesiredSnapshot(stored); err != nil {
		t.Fatalf("failed to seed desired snapshot: %v", err)
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
	if err := mem.SaveDesiredSnapshot(stored); err != nil {
		t.Fatalf("failed to seed desired snapshot: %v", err)
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
	delegate      store.Store
	failOnNthSave int
	saveCount     int
}

func (f *failingStore) SaveDesiredSnapshot(snapshot Snapshot) error {
	f.saveCount++
	if f.failOnNthSave > 0 && f.saveCount >= f.failOnNthSave {
		return errors.New("persistence fail")
	}
	return f.delegate.SaveDesiredSnapshot(snapshot)
}

func (f *failingStore) LoadDesiredSnapshot() (Snapshot, error) {
	return f.delegate.LoadDesiredSnapshot()
}

func (f *failingStore) SaveAppliedSnapshot(snapshot Snapshot) error {
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
