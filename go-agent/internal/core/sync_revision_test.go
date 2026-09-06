//go:build !integration

package core

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"

	"strings"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/control"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
)

func TestRevisionSyncPersistsCutoverBeforeAppliedAcknowledgement(t *testing.T) {
	events := []string{}
	store := newRevisionTestStore(&events)
	runtime := NewRuntimeWithActivator(func(_ context.Context, _, next model.Snapshot) error {
		events = append(events, fmt.Sprintf("runtime:apply:%d", next.Revision))
		return nil
	})
	pull := revisionPull(7, "lease-7", "digest-7")
	client := &revisionClientStub{events: &events, pull: pull}
	controller := &SyncController{Store: store, Runtime: runtime, SyncClient: client}

	if err := controller.PerformSync(t.Context(), control.SyncRequest{}); err != nil {
		t.Fatalf("PerformSync() error = %v", err)
	}
	assertEventOrder(t, events,
		"journal:prepared",
		"journal:starting",
		"start:7",
		"journal:started",
		"runtime:apply:7",
		"journal:cutover",
		"applied:7",
		"runtime-state:7",
		"lkg:7",
		"journal:active",
		"report:applied:7",
		"journal:active:acknowledged",
	)
	if store.journal.Active == nil || !store.journal.Active.Acknowledged || store.journal.Candidate != nil {
		t.Fatalf("final journal = %+v, want acknowledged active generation", store.journal)
	}
}

func TestRevisionSyncManagedGenerationUsesVerifiedSnapshotDigest(t *testing.T) {
	events := []string{}
	store := newRevisionTestStore(&events)
	pull := revisionPull(7, "lease-7", "verified-digest-7")
	runtime := newManagedRevisionRuntime(t)
	controller := &SyncController{
		Store: store, Runtime: runtime,
		SyncClient: &revisionClientStub{events: &events, pull: pull},
	}

	if err := controller.PerformSync(t.Context(), control.SyncRequest{}); err != nil {
		t.Fatalf("PerformSync() error = %v", err)
	}
	identity, managed := runtime.ActiveGenerationIdentity()
	if !managed || identity.SnapshotHash != pull.VerifiedSnapshotDigest {
		t.Fatalf("active generation identity = %+v, managed = %t, want verified digest %q", identity, managed, pull.VerifiedSnapshotDigest)
	}
	if store.journal.Active == nil || store.journal.Active.RuntimeSnapshotHash != pull.VerifiedSnapshotDigest {
		t.Fatalf("active generation journal = %+v, want verified runtime snapshot identity", store.journal.Active)
	}
}

func TestRestoreDurableRevisionRuntimePreservesIdentityAcrossSnapshotSchemaChanges(t *testing.T) {
	events := []string{}
	store := newRevisionTestStore(&events)
	store.applied = revisionSnapshot(61)
	store.lkg = store.applied
	store.runtime = RuntimeState{CurrentRevision: 61, Metadata: map[string]string{"current_revision": "61"}}
	durableHash := strings.Repeat("a", sha256.Size*2)
	currentHash, err := revisionSnapshotDigest(store.applied)
	if err != nil {
		t.Fatal(err)
	}
	if currentHash == durableHash {
		t.Fatal("test precondition failed: current schema encoding retained the durable hash")
	}
	record := model.GenerationRecord{
		GenerationID: "attempt-generation-61", RuntimeGenerationID: "generation-61-" + durableHash[:16],
		RuntimeSnapshotHash: durableHash, Revision: 61, SnapshotDigest: strings.Repeat("b", sha256.Size*2),
		Phase: model.GenerationPhaseActive,
	}
	store.journal = model.GenerationJournal{Version: 1, Active: &record, LastKnownGood: &record}
	runtime := newManagedRevisionRuntime(t)
	controller := &SyncController{Store: store, Runtime: runtime}

	if err := controller.restoreDurableRevisionRuntime(t.Context()); err != nil {
		t.Fatalf("restoreDurableRevisionRuntime() error = %v", err)
	}
	identity, managed := runtime.ActiveGenerationIdentity()
	if !managed || identity.SnapshotHash != durableHash || identity.ID != "generation-61-"+durableHash[:16] {
		t.Fatalf("restored identity = %+v, managed = %t, want durable journal identity", identity, managed)
	}
}

func TestRevisionSyncManagedCutoverRecoversEveryPostPublishPersistenceFailure(t *testing.T) {
	tests := []struct {
		name   string
		inject func(*revisionTestStore)
		clear  func(*revisionTestStore)
	}{
		{
			name:   "cutover journal",
			inject: func(store *revisionTestStore) { store.failGenerationPhase = model.GenerationPhaseCutover },
			clear:  func(store *revisionTestStore) { store.failGenerationPhase = "" },
		},
		{
			name:   "applied snapshot",
			inject: func(store *revisionTestStore) { store.failOnAppliedSave = 1 },
			clear:  func(store *revisionTestStore) { store.failOnAppliedSave = 0 },
		},
		{
			name:   "applied snapshot uncertain commit",
			inject: func(store *revisionTestStore) { store.uncertainAppliedSave = true },
			clear:  func(store *revisionTestStore) { store.uncertainAppliedSave = false },
		},
		{
			name:   "runtime state",
			inject: func(store *revisionTestStore) { store.failRuntimeState = true },
			clear:  func(store *revisionTestStore) { store.failRuntimeState = false },
		},
		{
			name:   "last known good",
			inject: func(store *revisionTestStore) { store.failLastKnownGood = true },
			clear:  func(store *revisionTestStore) { store.failLastKnownGood = false },
		},
		{
			name:   "active journal",
			inject: func(store *revisionTestStore) { store.failGenerationPhase = model.GenerationPhaseActive },
			clear:  func(store *revisionTestStore) { store.failGenerationPhase = "" },
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			events := []string{}
			store := newRevisionTestStore(&events)
			tc.inject(store)
			client := &revisionClientStub{events: &events, pull: revisionPull(7, "lease-7", "digest-7")}
			firstRuntime := newManagedRevisionRuntime(t)
			controller := &SyncController{Store: store, Runtime: firstRuntime, SyncClient: client}

			if err := controller.PerformSync(t.Context(), control.SyncRequest{}); err == nil {
				t.Fatal("PerformSync() error = nil, want injected persistence failure")
			}
			if got := firstRuntime.ActiveSnapshot().Revision; got != 7 {
				t.Fatalf("active revision after persistence failure = %d, want irreversible cutover 7", got)
			}
			if len(client.reports) != 0 {
				t.Fatalf("reports before durable recovery = %+v, want none", client.reports)
			}
			if len(client.starts) != 1 {
				t.Fatalf("start calls before recovery = %d, want 1", len(client.starts))
			}

			tc.clear(store)
			restartedRuntime := newManagedRevisionRuntime(t)
			restartedController := &SyncController{Store: store, Runtime: restartedRuntime, SyncClient: client}
			if err := restartedController.PerformSync(t.Context(), control.SyncRequest{}); err != nil {
				t.Fatalf("PerformSync(restart) error = %v", err)
			}
			if got := restartedRuntime.ActiveSnapshot().Revision; got != 7 {
				t.Fatalf("restart active revision = %d, want 7", got)
			}
			if len(client.starts) != 1 || len(client.reports) != 1 || client.reports[0].Status != "applied" {
				t.Fatalf("restart start/report = %+v/%+v, want one start and one applied report", client.starts, client.reports)
			}
			identity, _ := restartedRuntime.ActiveGenerationIdentity()
			active := store.journal.Active
			if active == nil || !active.Acknowledged || active.RuntimeGenerationID != identity.ID || active.RuntimeSnapshotHash != identity.SnapshotHash || store.journal.Candidate != nil {
				t.Fatalf("recovered journal = %+v, runtime identity = %+v", store.journal, identity)
			}
			if store.applied.Revision != 7 || store.lkg.Revision != 7 {
				t.Fatalf("recovered applied/LKG = %d/%d, want 7/7", store.applied.Revision, store.lkg.Revision)
			}
		})
	}
}

func TestRevisionSyncRuntimeStateFailureDoesNotOverwriteLastKnownGood(t *testing.T) {
	events := []string{}
	store := newRevisionTestStore(&events)
	previous := revisionSnapshot(6)
	store.applied = previous
	store.lkg = previous
	store.runtime = RuntimeState{CurrentRevision: 6, Metadata: map[string]string{"current_revision": "6"}}
	store.failRuntimeState = true
	runtime := NewRuntime()
	if err := runtime.Apply(t.Context(), model.Snapshot{}, previous); err != nil {
		t.Fatalf("seed runtime: %v", err)
	}
	client := &revisionClientStub{events: &events, pull: revisionPull(7, "lease-7", "digest-7")}
	controller := &SyncController{Store: store, Runtime: runtime, SyncClient: client}

	if err := controller.PerformSync(t.Context(), control.SyncRequest{}); err == nil {
		t.Fatal("PerformSync() error = nil, want runtime state persistence failure")
	}
	if store.lkg.Revision != previous.Revision || store.applied.Revision != previous.Revision {
		t.Fatalf("LKG/applied revisions = %d/%d, want preserved revision %d", store.lkg.Revision, store.applied.Revision, previous.Revision)
	}
	if runtime.ActiveSnapshot().Revision != previous.Revision {
		t.Fatalf("active revision = %d, want rollback to %d", runtime.ActiveSnapshot().Revision, previous.Revision)
	}
}

func TestRevisionSyncKeepsCutoverForPostRenameCommitUncertainty(t *testing.T) {
	events := []string{}
	store := newRevisionTestStore(&events)
	store.uncertainAppliedSave = true
	runtime := NewRuntime()
	client := &revisionClientStub{events: &events, pull: revisionPull(7, "lease-7", "digest-7")}
	controller := &SyncController{Store: store, Runtime: runtime, SyncClient: client}

	err := controller.PerformSync(t.Context(), control.SyncRequest{})
	if err == nil || !isFilesystemCommitUncertain(err) {
		t.Fatalf("PerformSync() error = %v, want commit uncertainty", err)
	}
	if runtime.ActiveSnapshot().Revision != 7 || store.applied.Revision != 7 {
		t.Fatalf("runtime/applied revisions = %d/%d, want retained cutover 7", runtime.ActiveSnapshot().Revision, store.applied.Revision)
	}
	if store.journal.Candidate == nil || store.journal.Candidate.Phase != model.GenerationPhaseCutover {
		t.Fatalf("journal = %+v, want recoverable cutover candidate", store.journal)
	}
	if len(client.reports) != 0 {
		t.Fatalf("reports = %+v, want no false failed report", client.reports)
	}

	store.uncertainAppliedSave = false
	if err := controller.PerformSync(t.Context(), control.SyncRequest{}); err != nil {
		t.Fatalf("PerformSync(recovery) error = %v", err)
	}
	if len(client.starts) != 1 || len(client.reports) != 1 || client.reports[0].Status != "applied" {
		t.Fatalf("start/report recovery = %+v/%+v, want single start and applied report", client.starts, client.reports)
	}
}

func TestRevisionSyncPullFailureDoesNotStartAttempt(t *testing.T) {
	events := []string{}
	store := newRevisionTestStore(&events)
	client := &revisionClientStub{events: &events, pullErr: errors.New("control plane offline")}
	controller := &SyncController{Store: store, Runtime: NewRuntime(), SyncClient: client}

	err := controller.PerformSync(t.Context(), control.SyncRequest{})
	if err == nil || !strings.Contains(err.Error(), "control plane offline") {
		t.Fatalf("PerformSync() error = %v, want offline error", err)
	}
	if len(client.starts) != 0 || len(client.reports) != 0 || store.journal.Candidate != nil {
		t.Fatalf("offline pull started/reported/journaled = %d/%d/%+v", len(client.starts), len(client.reports), store.journal.Candidate)
	}
}

func TestRevisionSyncStagesPackageBeforePullingFreshLease(t *testing.T) {
	events := []string{}
	store := newRevisionTestStore(&events)
	previous := revisionSnapshot(6)
	store.applied = previous
	store.lkg = previous
	store.runtime = RuntimeState{CurrentRevision: 6, Metadata: map[string]string{
		"current_revision": "6", "last_apply_revision": "6", "last_apply_status": "success",
	}}
	runtime := NewRuntime()
	if err := runtime.Apply(t.Context(), model.Snapshot{}, previous); err != nil {
		t.Fatal(err)
	}
	packageURL := "https://updates.example.test/nre-agent-linux-amd64"
	pkg := coordinatorTestPackage(packageURL, "a")
	heartbeat := revisionSnapshot(7)
	heartbeat.VersionPackage = &pkg
	updater := newCoordinatorTestUpdater(packageURL)
	client := &revisionClientStub{
		events: &events, heartbeatSnapshot: heartbeat,
		pullFunc: func() model.RevisionPull { return revisionPull(7, "fresh-lease-7", "digest-7") },
	}
	controller := &SyncController{
		Store: store, Runtime: runtime, SyncClient: client, Updater: updater,
		PackageStages: NewPackageStageCoordinator(), CurrentPackageSHA256: strings.Repeat("f", 64),
	}

	for heartbeatIndex := 0; heartbeatIndex < 4; heartbeatIndex++ {
		if err := controller.PerformSync(t.Context(), control.SyncRequest{}); err != nil {
			t.Fatalf("PerformSync(staging %d) error = %v", heartbeatIndex, err)
		}
		if heartbeatIndex == 0 {
			waitForCoordinatorSignal(t, updater.started[packageURL])
		}
	}
	if countRevisionEvent(events, "heartbeat") != 4 || countRevisionEvent(events, "pull") != 0 {
		t.Fatalf("staging heartbeat/pull events = %v, want four heartbeats and no lease pull", events)
	}
	if updater.calls(packageURL) != 1 || len(updater.activations()) != 0 {
		t.Fatalf("staging Stage/Activate calls = %d/%d, want 1/0", updater.calls(packageURL), len(updater.activations()))
	}
	if len(client.starts) != 0 || len(client.reports) != 0 || store.journal.Candidate != nil {
		t.Fatalf("staging started/reported/journaled revision = %d/%d/%+v", len(client.starts), len(client.reports), store.journal.Candidate)
	}
	if store.applied.Revision != 6 || runtime.ActiveSnapshot().Revision != 6 || store.runtime.CurrentRevision != 6 {
		t.Fatalf("staging advanced durable/runtime/state revision = %d/%d/%d", store.applied.Revision, runtime.ActiveSnapshot().Revision, store.runtime.CurrentRevision)
	}
	if store.runtime.Metadata["last_apply_revision"] != "6" || store.runtime.Metadata["last_apply_status"] != "success" ||
		controller.CurrentPackageSHA256 != strings.Repeat("f", 64) {
		t.Fatalf("staging changed apply/package facts: metadata=%v package=%q", store.runtime.Metadata, controller.CurrentPackageSHA256)
	}

	close(updater.release[packageURL])
	waitForRevisionRestart(t, func() error {
		return controller.PerformSync(t.Context(), control.SyncRequest{})
	})
	if countRevisionEvent(events, "pull") != 1 || len(client.starts) != 1 || len(client.reports) != 0 {
		t.Fatalf("ready pull/start/report = %d/%d/%d, want 1/1/0", countRevisionEvent(events, "pull"), len(client.starts), len(client.reports))
	}
	if client.starts[0].LeaseID != "fresh-lease-7" {
		t.Fatalf("started lease = %+v, want fresh-lease-7", client.starts[0])
	}
	if got := updater.activations(); len(got) != 1 || got[0].desiredVersion != "v7" {
		t.Fatalf("ready activations = %+v, want current revision desired version", got)
	}
}

func TestRevisionSyncStageFailureDoesNotConsumeLeaseOrApplyAttempt(t *testing.T) {
	events := []string{}
	store := newRevisionTestStore(&events)
	previous := revisionSnapshot(6)
	store.applied = previous
	store.lkg = previous
	packageURL := "https://updates.example.test/bad-agent"
	pkg := coordinatorTestPackage(packageURL, "b")
	heartbeat := revisionSnapshot(7)
	heartbeat.VersionPackage = &pkg
	stageErr := errors.New("sha256 mismatch")
	updater := newCoordinatorTestUpdater(packageURL)
	updater.stageErr[packageURL] = stageErr
	client := &revisionClientStub{
		events: &events, heartbeatSnapshot: heartbeat,
		pullFunc: func() model.RevisionPull { return revisionPull(7, "unused-lease", "digest-7") },
	}
	controller := &SyncController{
		Store: store, Runtime: NewRuntime(), SyncClient: client, Updater: updater,
		PackageStages: NewPackageStageCoordinator(), CurrentPackageSHA256: strings.Repeat("f", 64),
	}

	if err := controller.PerformSync(t.Context(), control.SyncRequest{}); err != nil {
		t.Fatalf("PerformSync(start Stage) error = %v", err)
	}
	waitForCoordinatorSignal(t, updater.started[packageURL])
	deadline := time.Now().Add(time.Second)
	for {
		err := controller.PerformSync(t.Context(), control.SyncRequest{})
		if errors.Is(err, stageErr) || (err != nil && strings.Contains(err.Error(), stageErr.Error())) {
			break
		}
		if err != nil {
			t.Fatalf("PerformSync(failed Stage) error = %v, want %v", err, stageErr)
		}
		if time.Now().After(deadline) {
			t.Fatal("Stage failure was not consumed before timeout")
		}
		time.Sleep(time.Millisecond)
	}
	if countRevisionEvent(events, "pull") != 0 || len(client.starts) != 0 || len(client.reports) != 0 || store.journal.Candidate != nil {
		t.Fatalf("failed Stage pulled/started/reported/journaled = %d/%d/%d/%+v",
			countRevisionEvent(events, "pull"), len(client.starts), len(client.reports), store.journal.Candidate)
	}
	if store.applied.Revision != previous.Revision {
		t.Fatalf("failed Stage changed applied revision = %d, want %d", store.applied.Revision, previous.Revision)
	}
}

func countRevisionEvent(events []string, want string) int {
	count := 0
	for _, event := range events {
		if event == want {
			count++
		}
	}
	return count
}

func waitForRevisionRestart(t *testing.T, operation func() error) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		err := operation()
		if errors.Is(err, ErrRestartRequested) {
			return
		}
		if err != nil {
			t.Fatalf("revision ready result = %v, want restart requested", err)
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("ready revision package was not activated before timeout")
}

type revisionClientStub struct {
	events            *[]string
	heartbeatSnapshot model.Snapshot
	heartbeatErr      error
	pull              model.RevisionPull
	pullFunc          func() model.RevisionPull
	pullErr           error
	startErr          error
	reportErr         error
	starts            []model.RevisionStart
	reports           []model.RevisionReport
}

func (c *revisionClientStub) Sync(context.Context, control.SyncRequest) (model.Snapshot, error) {
	c.record("heartbeat")
	return c.heartbeatSnapshot, c.heartbeatErr
}

func (c *revisionClientStub) PullRevision(context.Context) (model.RevisionPull, error) {
	c.record("pull")
	if c.pullFunc != nil {
		return c.pullFunc(), c.pullErr
	}
	return c.pull, c.pullErr
}

func (c *revisionClientStub) StartRevision(_ context.Context, input model.RevisionStart) error {
	c.starts = append(c.starts, input)
	c.record(fmt.Sprintf("start:%d", input.Revision))
	return c.startErr
}

func (c *revisionClientStub) ReportRevision(_ context.Context, input model.RevisionReport) error {
	c.reports = append(c.reports, input)
	c.record(fmt.Sprintf("report:%s:%d", input.Status, input.Revision))
	return c.reportErr
}

func (c *revisionClientStub) record(event string) {
	if c.events != nil {
		*c.events = append(*c.events, event)
	}
}

type revisionTestStore struct {
	*syncControllerStore
	events               *[]string
	journal              model.GenerationJournal
	lkg                  model.Snapshot
	failRuntimeState     bool
	uncertainAppliedSave bool
	failGenerationPhase  string
	failLastKnownGood    bool
}

func newRevisionTestStore(events *[]string) *revisionTestStore {
	return &revisionTestStore{syncControllerStore: newSyncControllerStore(), events: events}
}

func (s *revisionTestStore) SaveDesiredSnapshot(snapshot model.Snapshot) error {
	s.record(fmt.Sprintf("desired:%d", snapshot.Revision))
	return s.syncControllerStore.SaveDesiredSnapshot(snapshot)
}

func (s *revisionTestStore) SaveAppliedSnapshot(snapshot model.Snapshot) error {
	s.record(fmt.Sprintf("applied:%d", snapshot.Revision))
	if err := s.syncControllerStore.SaveAppliedSnapshot(snapshot); err != nil {
		return err
	}
	if s.uncertainAppliedSave {
		return &filesystemCommitUncertainError{err: errors.New("injected directory sync failure")}
	}
	return nil
}

func (s *revisionTestStore) SaveRuntimeState(state RuntimeState) error {
	s.record(fmt.Sprintf("runtime-state:%d", state.CurrentRevision))
	if s.failRuntimeState {
		return errors.New("runtime state persistence fail")
	}
	return s.syncControllerStore.SaveRuntimeState(state)
}

func (s *revisionTestStore) SaveGenerationJournal(journal model.GenerationJournal) error {
	phase := "empty"
	acknowledged := false
	if journal.Candidate != nil {
		phase = journal.Candidate.Phase
	} else if journal.Active != nil {
		phase = journal.Active.Phase
		acknowledged = journal.Active.Acknowledged
	}
	if acknowledged {
		phase += ":acknowledged"
	}
	s.record("journal:" + phase)
	if phase == s.failGenerationPhase {
		return errors.New("failed journal persistence")
	}
	s.journal = cloneGenerationJournal(journal)
	return nil
}

func (s *revisionTestStore) LoadGenerationJournal() (model.GenerationJournal, error) {
	return cloneGenerationJournal(s.journal), nil
}

func cloneGenerationJournal(journal model.GenerationJournal) model.GenerationJournal {
	cloned := journal
	if journal.Active != nil {
		record := *journal.Active
		cloned.Active = &record
	}
	if journal.Candidate != nil {
		record := *journal.Candidate
		cloned.Candidate = &record
	}
	if journal.LastKnownGood != nil {
		record := *journal.LastKnownGood
		cloned.LastKnownGood = &record
	}
	return cloned
}

func (s *revisionTestStore) SaveLastKnownGoodSnapshot(snapshot model.Snapshot) error {
	if s.failLastKnownGood {
		return errors.New("last known good persistence fail")
	}
	s.lkg = snapshot
	s.record(fmt.Sprintf("lkg:%d", snapshot.Revision))
	return nil
}

func newManagedRevisionRuntime(t *testing.T) *Runtime {
	t.Helper()
	runtime, _ := newTrackedManagedRevisionRuntime(t)
	return runtime
}

func newTrackedManagedRevisionRuntime(t *testing.T) (*Runtime, *revisionGenerationTracker) {
	t.Helper()
	tracker := &revisionGenerationTracker{}
	registry := module.NewRegistry()
	if err := registry.Register(revisionGenerationModule{tracker: tracker}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	return NewRuntimeWithGenerationManager(NewGenerationManager(registry)), tracker
}

type revisionGenerationTracker struct {
	prepares int
	commits  int
	destroys int
}

type revisionGenerationModule struct {
	tracker *revisionGenerationTracker
}

func (revisionGenerationModule) Name() string { return "revision-generation" }

func (revisionGenerationModule) Descriptor() module.ModuleDescriptor {
	return module.ModuleDescriptor{Name: "revision-generation"}
}

func (revisionGenerationModule) RegisterProviders(module.ProviderRegistry) error      { return nil }
func (revisionGenerationModule) Capabilities(module.SnapshotView) []module.Capability { return nil }
func (revisionGenerationModule) Stop(context.Context) error                           { return nil }

func (m revisionGenerationModule) Apply(ctx context.Context, req module.ApplyRequest) error {
	tx, err := m.Prepare(ctx, req)
	if err != nil || tx == nil {
		return err
	}
	return tx.Commit()
}

func (m revisionGenerationModule) Prepare(context.Context, module.ApplyRequest) (module.ModuleTransaction, error) {
	if m.tracker != nil {
		m.tracker.prepares++
	}
	return revisionGenerationTransaction{tracker: m.tracker}, nil
}

type revisionGenerationTransaction struct {
	tracker *revisionGenerationTracker
}

func (revisionGenerationTransaction) Ready(context.Context) error { return nil }
func (tx revisionGenerationTransaction) Destroy(context.Context) error {
	if tx.tracker != nil {
		tx.tracker.destroys++
	}
	return nil
}
func (tx revisionGenerationTransaction) Commit() error {
	if tx.tracker != nil {
		tx.tracker.commits++
	}
	return nil
}
func (revisionGenerationTransaction) Rollback() error { return nil }

type revisionRetirementModule struct {
	store    PluginLogRetirementIntentStore
	intentID string
}

func (revisionRetirementModule) Name() string { return "revision-retirement" }
func (revisionRetirementModule) Descriptor() module.ModuleDescriptor {
	return module.ModuleDescriptor{Name: "revision-retirement"}
}
func (revisionRetirementModule) RegisterProviders(module.ProviderRegistry) error      { return nil }
func (revisionRetirementModule) Capabilities(module.SnapshotView) []module.Capability { return nil }
func (revisionRetirementModule) Stop(context.Context) error                           { return nil }
func (m revisionRetirementModule) Apply(context.Context, module.ApplyRequest) error   { return nil }
func (m revisionRetirementModule) Prepare(context.Context, module.ApplyRequest) (module.ModuleTransaction, error) {
	return revisionRetirementTransaction{store: m.store, intentID: m.intentID}, nil
}

type revisionRetirementTransaction struct {
	store    PluginLogRetirementIntentStore
	intentID string
}

func (revisionRetirementTransaction) Ready(context.Context) error { return nil }
func (tx revisionRetirementTransaction) Destroy(context.Context) error {
	return tx.store.MarkPluginRuntimeLogRetirementIntentDrained(tx.intentID)
}
func (revisionRetirementTransaction) Commit() error   { return nil }
func (revisionRetirementTransaction) Rollback() error { return nil }

func (s *revisionTestStore) LoadLastKnownGoodSnapshot() (model.Snapshot, error) {
	return s.lkg, nil
}

func (s *revisionTestStore) record(event string) {
	if s.events != nil {
		*s.events = append(*s.events, event)
	}
}

func revisionPull(revision int64, leaseID, digest string) model.RevisionPull {
	digest = fmt.Sprintf("%x", sha256.Sum256([]byte(digest)))
	snapshot := revisionSnapshot(revision)
	lease := revisionLease(revision, leaseID, digest)
	return model.RevisionPull{
		HasUpdate: true, DesiredRevision: revision, Lease: &lease, Snapshot: &snapshot,
		VerifiedSnapshotDigest: digest,
	}
}

func revisionSnapshot(revision int64) model.Snapshot {
	payload := fmt.Sprintf(`{"desired_version":"v%d","desired_revision":%d,"agent_config":{},"rules":[],"l4_rules":[],"relay_listeners":[],"egress_profiles":[],"certificates":[],"certificate_policies":[],"plugin_generations":[],"plugin_dependencies":[],"plugin_policies":[]}`, revision, revision)
	var snapshot model.Snapshot
	if err := json.Unmarshal([]byte(payload), &snapshot); err != nil {
		panic(err)
	}
	return snapshot
}

func revisionLease(revision int64, leaseID, digest string) model.RevisionLease {
	return model.RevisionLease{
		AgentID: "edge-1", Revision: revision, Attempt: 1, LeaseID: leaseID,
		SnapshotDigest: digest, DesiredVersion: fmt.Sprintf("v%d", revision),
		ApplyTimeoutSeconds: 60, DrainTimeoutSeconds: 600,
		DeadlineAt: time.Now().Add(time.Hour),
	}
}

func assertEventOrder(t *testing.T, events []string, expected ...string) {
	t.Helper()
	next := 0
	for _, event := range events {
		if next < len(expected) && event == expected[next] {
			next++
		}
	}
	if next != len(expected) {
		t.Fatalf("events = %v, missing ordered suffix from %v", events, expected[next:])
	}
}

func TestRevisionStartBindsActualRuntimeIdentityBeforePrepare(t *testing.T) {
	events := []string{}
	store := newRevisionTestStore(&events)
	pull := revisionPull(7, "lease-actual", "verified-digest-7")
	runtime := newManagedRevisionRuntime(t)
	client := &revisionClientStub{events: &events, pull: pull}
	controller := &SyncController{Store: store, Runtime: runtime, SyncClient: client}
	observed := false
	ctx := WithRuntimeGenerationBinder(t.Context(), func(ctx context.Context, identity GenerationIdentity) error {
		observed = true
		if len(client.starts) != 1 {
			t.Fatal("Prepare preceded authenticated Start")
		}
		start := client.starts[0]
		if start.RuntimeGenerationID != identity.ID || start.RuntimeSnapshotHash != identity.SnapshotHash || start.GenerationID == identity.ID {
			t.Fatalf("attempt/runtime authority collapsed: %+v %+v", start, identity)
		}
		return nil
	})
	if err := controller.PerformSync(ctx, control.SyncRequest{}); err != nil {
		t.Fatal(err)
	}
	if !observed {
		t.Fatal("actual runtime boundary was not bound")
	}
	deniedRuntime := newManagedRevisionRuntime(t)
	denied := errors.New("lease rejected")
	ctx = WithRuntimeGenerationBinder(t.Context(), func(context.Context, GenerationIdentity) error { return denied })
	if err := deniedRuntime.Apply(ctx, model.Snapshot{}, *pull.Snapshot); !errors.Is(err, denied) {
		t.Fatalf("binding refusal ignored: %v", err)
	}
	if identity, _ := deniedRuntime.ActiveGenerationIdentity(); identity.ID != "" {
		t.Fatal("denied binding published candidate")
	}
}
