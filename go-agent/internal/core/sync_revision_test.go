package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/control"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
)

func TestRevisionSyncHeartbeatIsTelemetryOnlyWithoutLease(t *testing.T) {
	events := []string{}
	store := newRevisionTestStore(&events)
	runtime := NewRuntimeWithActivator(func(_ context.Context, _, _ model.Snapshot) error {
		events = append(events, "runtime:apply")
		return nil
	})
	client := &revisionClientStub{
		events: &events,
		heartbeatSnapshot: model.Snapshot{
			Revision: 99, Rules: []model.HTTPRule{{FrontendURL: "https://must-not-apply.example"}},
		},
		pull: model.RevisionPull{DesiredRevision: 99},
	}
	controller := &SyncController{Store: store, Runtime: runtime, SyncClient: client}

	if err := controller.PerformSync(t.Context(), control.SyncRequest{}); err != nil {
		t.Fatalf("PerformSync() error = %v", err)
	}
	if got := runtime.ActiveSnapshot().Revision; got != 0 {
		t.Fatalf("active revision = %d, want no apply without lease", got)
	}
	if len(client.starts) != 0 || len(client.reports) != 0 {
		t.Fatalf("start/report calls = %d/%d, want zero", len(client.starts), len(client.reports))
	}
	if !reflect.DeepEqual(events, []string{"heartbeat", "pull"}) {
		t.Fatalf("events = %v, want telemetry heartbeat then pull only", events)
	}
}

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

func TestRevisionSyncBindsJournalToManagedRuntimeGeneration(t *testing.T) {
	events := []string{}
	store := newRevisionTestStore(&events)
	runtime := newManagedRevisionRuntime(t)
	client := &revisionClientStub{events: &events, pull: revisionPull(7, "lease-7", "digest-7")}
	controller := &SyncController{Store: store, Runtime: runtime, SyncClient: client}

	if err := controller.PerformSync(t.Context(), control.SyncRequest{}); err != nil {
		t.Fatalf("PerformSync() error = %v", err)
	}
	identity, ok := runtime.ActiveGenerationIdentity()
	if !ok || identity.ID == "" || identity.SnapshotHash == "" {
		t.Fatalf("active generation identity = %+v/%v", identity, ok)
	}
	active := store.journal.Active
	if active == nil || active.RuntimeGenerationID != identity.ID || active.RuntimeSnapshotHash != identity.SnapshotHash || active.Revision != identity.Revision {
		t.Fatalf("journal active = %+v, want runtime identity %+v", active, identity)
	}
	if active.SnapshotDigest != "digest-7" {
		t.Fatalf("journal control-plane digest = %q, want digest-7", active.SnapshotDigest)
	}
	if len(client.starts) != 1 || client.starts[0].GenerationID != active.GenerationID || len(client.reports) != 1 || client.reports[0].GenerationID != active.GenerationID {
		t.Fatalf("start/report generation IDs = %+v/%+v, want journal operation generation %q", client.starts, client.reports, active.GenerationID)
	}
	state, err := store.LoadRuntimeState()
	if err != nil {
		t.Fatalf("LoadRuntimeState() error = %v", err)
	}
	if state.Metadata["generation_id"] != identity.ID || state.Metadata["snapshot_hash"] != identity.SnapshotHash {
		t.Fatalf("runtime metadata = %+v, want active generation identity", state.Metadata)
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

func TestRevisionSyncManagedCutoverRetryNeverReportsFailedOrRepublishes(t *testing.T) {
	events := []string{}
	store := newRevisionTestStore(&events)
	store.failGenerationPhase = model.GenerationPhaseCutover
	runtime, tracker := newTrackedManagedRevisionRuntime(t)
	client := &revisionClientStub{events: &events, pull: revisionPull(7, "lease-7", "digest-7")}
	controller := &SyncController{Store: store, Runtime: runtime, SyncClient: client}

	if err := controller.PerformSync(t.Context(), control.SyncRequest{}); err == nil {
		t.Fatal("PerformSync() error = nil, want cutover journal failure")
	}
	if runtime.ActiveSnapshot().Revision != 7 || tracker.prepares != 1 {
		t.Fatalf("first cutover active/prepares = %d/%d, want 7/1", runtime.ActiveSnapshot().Revision, tracker.prepares)
	}
	if store.journal.Candidate == nil || store.journal.Candidate.Phase != model.GenerationPhaseStarted {
		t.Fatalf("journal after cutover save failure = %+v, want durable started", store.journal)
	}

	store.failGenerationPhase = ""
	store.failOnDesiredSave = store.desiredSaveCount + 1
	if err := controller.PerformSync(t.Context(), control.SyncRequest{}); err == nil {
		t.Fatal("PerformSync(retry) error = nil, want desired persistence failure")
	}
	if store.journal.Candidate == nil || store.journal.Candidate.Phase != model.GenerationPhaseCutover {
		t.Fatalf("journal after post-publish retry failure = %+v, want recoverable cutover", store.journal)
	}
	if len(client.reports) != 0 {
		t.Fatalf("post-publish retry reports = %+v, want no false failed report", client.reports)
	}
	if tracker.prepares != 1 || tracker.destroys != 0 {
		t.Fatalf("retry lifecycle prepares/destroys = %d/%d, want 1/0", tracker.prepares, tracker.destroys)
	}

	store.failOnDesiredSave = 0
	if err := controller.PerformSync(t.Context(), control.SyncRequest{}); err != nil {
		t.Fatalf("PerformSync(recovery) error = %v", err)
	}
	if len(client.starts) != 1 || len(client.reports) != 1 || client.reports[0].Status != "applied" {
		t.Fatalf("recovery start/report = %+v/%+v, want one start and one applied report", client.starts, client.reports)
	}
	if tracker.prepares != 1 || tracker.destroys != 0 {
		t.Fatalf("recovery lifecycle prepares/destroys = %d/%d, want 1/0", tracker.prepares, tracker.destroys)
	}
}

func TestRevisionSyncRetriesAcknowledgedJournalWithoutRepublishing(t *testing.T) {
	events := []string{}
	store := newRevisionTestStore(&events)
	store.failGenerationPhase = model.GenerationPhaseActive + ":acknowledged"
	runtime, tracker := newTrackedManagedRevisionRuntime(t)
	client := &revisionClientStub{events: &events, pull: revisionPull(7, "lease-7", "digest-7")}
	controller := &SyncController{Store: store, Runtime: runtime, SyncClient: client}

	if err := controller.PerformSync(t.Context(), control.SyncRequest{}); err == nil {
		t.Fatal("PerformSync() error = nil, want acknowledged journal failure")
	}
	if store.journal.Active == nil || store.journal.Active.Acknowledged || len(client.reports) != 1 {
		t.Fatalf("first acknowledgement journal/reports = %+v/%+v, want unacknowledged active and one applied report", store.journal, client.reports)
	}

	store.failGenerationPhase = ""
	if err := controller.PerformSync(t.Context(), control.SyncRequest{}); err != nil {
		t.Fatalf("PerformSync(retry) error = %v", err)
	}
	if store.journal.Active == nil || !store.journal.Active.Acknowledged || len(client.reports) != 2 {
		t.Fatalf("retried acknowledgement journal/reports = %+v/%+v, want acknowledged active and idempotent report retry", store.journal, client.reports)
	}
	for _, report := range client.reports {
		if report.Status != "applied" || report.GenerationID != store.journal.Active.GenerationID {
			t.Fatalf("retried report = %+v, want applied for stable operation generation", report)
		}
	}
	if tracker.prepares != 1 || tracker.destroys != 0 {
		t.Fatalf("ack retry lifecycle prepares/destroys = %d/%d, want 1/0", tracker.prepares, tracker.destroys)
	}
}

func TestRevisionSyncRejectsManagedRuntimeIdentityMismatchWithoutAcknowledgement(t *testing.T) {
	events := []string{}
	store := newRevisionTestStore(&events)
	lease := revisionLease(7, "lease-7", "digest-7")
	store.journal = model.GenerationJournal{
		Version: 1,
		AgentID: "edge-1",
		Candidate: &model.GenerationRecord{
			GenerationID: "operation-7", RuntimeGenerationID: "wrong-runtime-generation",
			RuntimeSnapshotHash: "wrong-runtime-hash", Revision: 7, SnapshotDigest: "digest-7",
			Phase: model.GenerationPhaseStarted, Lease: lease,
		},
	}
	client := &revisionClientStub{events: &events, pull: revisionPull(7, "lease-7", "digest-7")}
	runtime := newManagedRevisionRuntime(t)
	controller := &SyncController{Store: store, Runtime: runtime, SyncClient: client}

	err := controller.PerformSync(t.Context(), control.SyncRequest{})
	if err == nil || !strings.Contains(err.Error(), "runtime generation changed") {
		t.Fatalf("PerformSync() error = %v, want runtime identity mismatch", err)
	}
	if len(client.starts) != 0 || len(client.reports) != 0 || runtime.ActiveSnapshot().Revision != 0 {
		t.Fatalf("mismatch start/report/active = %d/%d/%d, want no publication or acknowledgement", len(client.starts), len(client.reports), runtime.ActiveSnapshot().Revision)
	}
}

func TestLegacySyncManagedRuntimeRetriesPersistenceWithoutRollback(t *testing.T) {
	store := newSyncControllerStore()
	previous := model.Snapshot{DesiredVersion: "v6", Revision: 6}
	next := model.Snapshot{DesiredVersion: "v7", Revision: 7}
	if err := store.SaveAppliedSnapshot(previous); err != nil {
		t.Fatalf("seed applied snapshot: %v", err)
	}
	store.failOnAppliedSave = 2
	runtime := newManagedRevisionRuntime(t)
	if err := runtime.Apply(t.Context(), model.Snapshot{}, previous); err != nil {
		t.Fatalf("seed runtime: %v", err)
	}
	controller := &SyncController{Store: store, Runtime: runtime, SyncClient: &syncControllerClient{snapshot: next}}

	if err := controller.PerformSync(t.Context(), control.SyncRequest{}); err == nil {
		t.Fatal("PerformSync() error = nil, want applied persistence failure")
	}
	if runtime.ActiveSnapshot().Revision != 7 || store.applied.Revision != 6 {
		t.Fatalf("active/applied after failure = %d/%d, want irreversible active 7 and durable applied 6", runtime.ActiveSnapshot().Revision, store.applied.Revision)
	}

	store.failOnAppliedSave = 0
	if err := controller.PerformSync(t.Context(), control.SyncRequest{}); err != nil {
		t.Fatalf("PerformSync(retry) error = %v", err)
	}
	if runtime.ActiveSnapshot().Revision != 7 || store.applied.Revision != 7 {
		t.Fatalf("active/applied after retry = %d/%d, want 7/7", runtime.ActiveSnapshot().Revision, store.applied.Revision)
	}
}

func TestRevisionSyncDoesNotReplayUncertainStartAfterRestart(t *testing.T) {
	events := []string{}
	store := newRevisionTestStore(&events)
	lease := revisionLease(7, "lease-7", "digest-7")
	store.journal = model.GenerationJournal{
		Version: 1,
		AgentID: "edge-1",
		Candidate: &model.GenerationRecord{
			GenerationID: revisionGenerationID(lease), Revision: 7, SnapshotDigest: "digest-7",
			Phase: model.GenerationPhaseStarting, Lease: lease,
		},
	}
	applyCalls := 0
	runtime := NewRuntimeWithActivator(func(_ context.Context, _, _ model.Snapshot) error {
		applyCalls++
		return nil
	})
	client := &revisionClientStub{events: &events, pull: revisionPull(7, "lease-7", "digest-7")}
	controller := &SyncController{Store: store, Runtime: runtime, SyncClient: client}

	if err := controller.PerformSync(t.Context(), control.SyncRequest{}); err != nil {
		t.Fatalf("PerformSync() error = %v", err)
	}
	if len(client.starts) != 0 || len(client.reports) != 0 || applyCalls != 0 {
		t.Fatalf("uncertain start replayed start/report/apply = %d/%d/%d", len(client.starts), len(client.reports), applyCalls)
	}
	if store.journal.Candidate == nil || store.journal.Candidate.Phase != model.GenerationPhaseStarting {
		t.Fatalf("journal = %+v, want durable starting intent retained", store.journal)
	}
}

func TestRevisionSyncRestartRequestKeepsStartedCandidate(t *testing.T) {
	events := []string{}
	store := newRevisionTestStore(&events)
	pull := revisionPull(7, "lease-7", "digest-7")
	pull.Snapshot.VersionPackage = &model.VersionPackage{
		URL: "https://downloads.example/nre-agent", SHA256: "new-package",
	}
	client := &revisionClientStub{events: &events, pull: pull}
	controller := &SyncController{
		Store: store, Runtime: NewRuntime(), SyncClient: client,
		Updater: &syncControllerUpdater{}, CurrentPackageSHA256: "old-package",
	}

	err := controller.PerformSync(t.Context(), control.SyncRequest{})
	if !errors.Is(err, ErrRestartRequested) {
		t.Fatalf("PerformSync() error = %v, want ErrRestartRequested", err)
	}
	if store.journal.Candidate == nil || store.journal.Candidate.Phase != model.GenerationPhaseStarted {
		t.Fatalf("journal = %+v, want durable started candidate", store.journal)
	}
	if len(client.reports) != 0 || store.lkg.Revision != 0 {
		t.Fatalf("restart request reports/LKG = %+v/%+v, want no failed report or cutover", client.reports, store.lkg)
	}
}

func TestRevisionSyncRejectsUpdatePackageBeforeAttemptStart(t *testing.T) {
	events := []string{}
	store := newRevisionTestStore(&events)
	pull := revisionPull(7, "lease-7", "digest-7")
	pull.Snapshot.VersionPackage = &model.VersionPackage{
		URL: "https://downloads.example/nre-agent", SHA256: strings.Repeat("a", 64),
		Platform: "darwin-amd64", Filename: "nre-agent-darwin-amd64", Size: 1024,
	}
	client := &revisionClientStub{events: &events, pull: pull}
	updater := &syncControllerUpdater{preflightErr: errors.New("hot upgrade is unsupported on platform darwin-amd64")}
	controller := &SyncController{Store: store, Runtime: NewRuntime(), SyncClient: client, Updater: updater}

	err := controller.PerformSync(t.Context(), control.SyncRequest{})
	if err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("PerformSync() error = %v, want package preflight rejection", err)
	}
	if updater.preflightCalls != 1 || updater.stageCalls != 0 || updater.activateCalls != 0 {
		t.Fatalf("updater preflight/stage/activate = %d/%d/%d, want 1/0/0", updater.preflightCalls, updater.stageCalls, updater.activateCalls)
	}
	if len(client.starts) != 0 || len(client.reports) != 0 || store.journal.Version != 0 || store.journal.Candidate != nil {
		t.Fatalf("preflight mutated attempt start/report/journal = %d/%d/%+v", len(client.starts), len(client.reports), store.journal)
	}
	if store.desired.Revision != 0 || controller.Runtime.ActiveSnapshot().Revision != 0 {
		t.Fatalf("preflight mutated desired/runtime = %d/%d", store.desired.Revision, controller.Runtime.ActiveSnapshot().Revision)
	}
}

func TestRevisionSyncPreflightsPendingUpdatePackageBeforeAttemptStart(t *testing.T) {
	for _, tc := range []struct {
		name       string
		pkg        model.VersionPackage
		currentSHA string
	}{
		{
			name: "incomplete package",
			pkg: model.VersionPackage{
				SHA256: strings.Repeat("a", 64),
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			events := []string{}
			store := newRevisionTestStore(&events)
			pull := revisionPull(7, "lease-7", "digest-7")
			pull.Snapshot.VersionPackage = &tc.pkg
			client := &revisionClientStub{events: &events, pull: pull}
			updater := &syncControllerUpdater{preflightErr: errors.New("invalid version package")}
			controller := &SyncController{
				Store: store, Runtime: NewRuntime(), SyncClient: client, Updater: updater,
				CurrentPackageSHA256: tc.currentSHA,
			}

			err := controller.PerformSync(t.Context(), control.SyncRequest{})
			if err == nil || !strings.Contains(err.Error(), "invalid version package") {
				t.Fatalf("PerformSync() error = %v, want package preflight rejection", err)
			}
			if updater.preflightCalls != 1 || updater.stageCalls != 0 || updater.activateCalls != 0 {
				t.Fatalf("updater preflight/stage/activate = %d/%d/%d, want 1/0/0", updater.preflightCalls, updater.stageCalls, updater.activateCalls)
			}
			if len(client.starts) != 0 || len(client.reports) != 0 || store.journal.Version != 0 || store.journal.Candidate != nil {
				t.Fatalf("preflight mutated attempt start/report/journal = %d/%d/%+v", len(client.starts), len(client.reports), store.journal)
			}
			if store.desired.Revision != 0 || controller.Runtime.ActiveSnapshot().Revision != 0 {
				t.Fatalf("preflight mutated desired/runtime = %d/%d", store.desired.Revision, controller.Runtime.ActiveSnapshot().Revision)
			}
		})
	}
}

func TestRevisionSyncSkipsPackagePreflightWhenRuntimeDigestAlreadyMatches(t *testing.T) {
	events := []string{}
	store := newRevisionTestStore(&events)
	pull := revisionPull(7, "lease-7", "digest-7")
	pull.Snapshot.VersionPackage = &model.VersionPackage{
		URL: "https://downloads.example/nre-agent", SHA256: strings.Repeat("b", 64),
	}
	client := &revisionClientStub{events: &events, pull: pull}
	updater := &syncControllerUpdater{preflightErr: errors.New("unused package metadata must not block config apply")}
	controller := &SyncController{
		Store: store, Runtime: NewRuntime(), SyncClient: client, Updater: updater,
		CurrentPackageSHA256: strings.Repeat("b", 64),
	}

	if err := controller.PerformSync(t.Context(), control.SyncRequest{}); err != nil {
		t.Fatalf("PerformSync() error = %v", err)
	}
	if updater.preflightCalls != 0 || updater.stageCalls != 0 || updater.activateCalls != 0 {
		t.Fatalf("updater preflight/stage/activate = %d/%d/%d, want 0/0/0", updater.preflightCalls, updater.stageCalls, updater.activateCalls)
	}
	if len(client.starts) != 1 || len(client.reports) != 1 || controller.Runtime.ActiveSnapshot().Revision != 7 {
		t.Fatalf("config revision start/report/runtime = %d/%d/%d", len(client.starts), len(client.reports), controller.Runtime.ActiveSnapshot().Revision)
	}
}

func TestRevisionSyncRejectsRevisionOlderThanDurableActiveGeneration(t *testing.T) {
	events := []string{}
	store := newRevisionTestStore(&events)
	activeLease := revisionLease(10, "lease-10", "digest-10")
	store.journal = model.GenerationJournal{
		Version: 1,
		AgentID: "edge-1",
		Active: &model.GenerationRecord{
			GenerationID: "generation-10", Revision: 10, SnapshotDigest: "digest-10",
			Phase: model.GenerationPhaseActive, Lease: activeLease, Acknowledged: true,
		},
	}
	store.lkg = revisionSnapshot(10)
	client := &revisionClientStub{events: &events, pull: revisionPull(9, "lease-9", "digest-9")}
	runtime := NewRuntime()
	controller := &SyncController{Store: store, Runtime: runtime, SyncClient: client}

	err := controller.PerformSync(t.Context(), control.SyncRequest{})
	if err == nil || !strings.Contains(err.Error(), "stale revision") {
		t.Fatalf("PerformSync() error = %v, want stale revision rejection", err)
	}
	if len(client.starts) != 0 || runtime.ActiveSnapshot().Revision != 0 {
		t.Fatalf("stale pull started=%d active=%d, want no apply", len(client.starts), runtime.ActiveSnapshot().Revision)
	}
}

func TestRevisionSyncRejectsRevisionOlderThanStartedCandidate(t *testing.T) {
	events := []string{}
	store := newRevisionTestStore(&events)
	lease := revisionLease(10, "lease-10", "digest-10")
	store.journal = model.GenerationJournal{
		Version: 1, AgentID: "edge-1",
		Candidate: &model.GenerationRecord{
			GenerationID: revisionGenerationID(lease), Revision: 10, SnapshotDigest: "digest-10",
			Phase: model.GenerationPhaseStarted, Lease: lease,
		},
	}
	client := &revisionClientStub{events: &events, pull: revisionPull(9, "lease-9", "digest-9")}
	controller := &SyncController{Store: store, Runtime: NewRuntime(), SyncClient: client}

	err := controller.PerformSync(t.Context(), control.SyncRequest{})
	if err == nil || !strings.Contains(err.Error(), "stale revision") {
		t.Fatalf("PerformSync() error = %v, want started candidate revision floor", err)
	}
	if len(client.starts) != 0 {
		t.Fatalf("start calls = %d, want no stale start", len(client.starts))
	}
}

func TestRevisionSyncRejectsRevisionOlderThanManagedCutover(t *testing.T) {
	events := []string{}
	store := newRevisionTestStore(&events)
	runtime := newManagedRevisionRuntime(t)
	snapshot := revisionSnapshot(10)
	identity, managed, err := runtime.CandidateGenerationIdentity(model.Snapshot{}, snapshot)
	if err != nil || !managed {
		t.Fatalf("CandidateGenerationIdentity() = %+v/%v/%v", identity, managed, err)
	}
	lease := revisionLease(10, "lease-10", "digest-10")
	store.journal = model.GenerationJournal{
		Version: 1, AgentID: "edge-1",
		Candidate: &model.GenerationRecord{
			GenerationID: revisionGenerationID(lease), RuntimeGenerationID: identity.ID,
			RuntimeSnapshotHash: identity.SnapshotHash, Revision: 10, SnapshotDigest: "digest-10",
			Phase: model.GenerationPhaseCutover, Lease: lease,
		},
	}
	client := &revisionClientStub{events: &events, pull: revisionPull(9, "lease-9", "digest-9")}
	controller := &SyncController{Store: store, Runtime: runtime, SyncClient: client}

	err = controller.PerformSync(t.Context(), control.SyncRequest{})
	if err == nil || !strings.Contains(err.Error(), "stale revision") {
		t.Fatalf("PerformSync() error = %v, want managed cutover revision floor", err)
	}
	if len(client.starts) != 0 || len(client.reports) != 0 || runtime.ActiveSnapshot().Revision != 0 {
		t.Fatalf("stale cutover start/report/active = %d/%d/%d, want no effects", len(client.starts), len(client.reports), runtime.ActiveSnapshot().Revision)
	}
	if store.journal.Candidate == nil || store.journal.Candidate.Phase != model.GenerationPhaseCutover {
		t.Fatalf("stale pull overwrote cutover journal: %+v", store.journal)
	}
}

func TestRevisionSyncRejectsDifferentDigestForDurableRevision(t *testing.T) {
	events := []string{}
	store := newRevisionTestStore(&events)
	lease := revisionLease(7, "lease-old", "digest-old")
	store.journal = model.GenerationJournal{
		Version: 1, AgentID: "edge-1",
		Active: &model.GenerationRecord{
			GenerationID: "generation-old", Revision: 7, SnapshotDigest: "digest-old",
			Phase: model.GenerationPhaseActive, Lease: lease, Acknowledged: true,
		},
	}
	store.lkg = revisionSnapshot(7)
	client := &revisionClientStub{events: &events, pull: revisionPull(7, "lease-new", "digest-new")}
	controller := &SyncController{Store: store, Runtime: NewRuntime(), SyncClient: client}

	err := controller.PerformSync(t.Context(), control.SyncRequest{})
	if err == nil || !strings.Contains(err.Error(), "immutable revision") {
		t.Fatalf("PerformSync() error = %v, want immutable revision digest rejection", err)
	}
	if len(client.starts) != 0 {
		t.Fatalf("start calls = %d, want no same-revision digest replacement", len(client.starts))
	}
}

func TestRevisionSyncReusesActiveDigestUnderNewLeaseWithoutReapply(t *testing.T) {
	events := []string{}
	store := newRevisionTestStore(&events)
	activeSnapshot := revisionSnapshot(7)
	store.applied = activeSnapshot
	store.lkg = activeSnapshot
	store.journal = model.GenerationJournal{
		Version: 1,
		AgentID: "edge-1",
		Active: &model.GenerationRecord{
			GenerationID: "generation-old", Revision: 7, SnapshotDigest: "digest-7",
			Phase: model.GenerationPhaseActive, Lease: revisionLease(7, "lease-old", "digest-7"), Acknowledged: true,
		},
	}
	runtimeApplyCalls := 0
	runtime := NewRuntimeWithActivator(func(_ context.Context, _, _ model.Snapshot) error {
		runtimeApplyCalls++
		return nil
	})
	if err := runtime.Apply(t.Context(), model.Snapshot{}, activeSnapshot); err != nil {
		t.Fatalf("seed runtime: %v", err)
	}
	runtimeApplyCalls = 0
	client := &revisionClientStub{events: &events, pull: revisionPull(7, "lease-new", "digest-7")}
	controller := &SyncController{Store: store, Runtime: runtime, SyncClient: client}

	if err := controller.PerformSync(t.Context(), control.SyncRequest{}); err != nil {
		t.Fatalf("PerformSync() error = %v", err)
	}
	if runtimeApplyCalls != 0 {
		t.Fatalf("runtime apply calls = %d, want digest-idempotent acknowledgement", runtimeApplyCalls)
	}
	if len(client.starts) != 1 || len(client.reports) != 1 || client.reports[0].Status != "applied" {
		t.Fatalf("start/report calls = %+v/%+v, want new lease start and applied report", client.starts, client.reports)
	}
}

func TestRevisionSyncResumesStartedLeaseWithoutDuplicateStart(t *testing.T) {
	events := []string{}
	store := newRevisionTestStore(&events)
	lease := revisionLease(7, "lease-7", "digest-7")
	store.journal = model.GenerationJournal{
		Version: 1,
		AgentID: "edge-1",
		Candidate: &model.GenerationRecord{
			GenerationID: revisionGenerationID(lease), Revision: 7, SnapshotDigest: "digest-7",
			Phase: model.GenerationPhaseStarted, Lease: lease,
		},
	}
	client := &revisionClientStub{events: &events, pull: revisionPull(7, "lease-7", "digest-7")}
	controller := &SyncController{Store: store, Runtime: NewRuntime(), SyncClient: client}

	if err := controller.PerformSync(t.Context(), control.SyncRequest{}); err != nil {
		t.Fatalf("PerformSync() error = %v", err)
	}
	if len(client.starts) != 0 || len(client.reports) != 1 {
		t.Fatalf("start/report calls = %d/%d, want resumed report without duplicate start", len(client.starts), len(client.reports))
	}
}

func TestRevisionSyncFailedCandidateWaitsForNewLease(t *testing.T) {
	events := []string{}
	store := newRevisionTestStore(&events)
	lease := revisionLease(7, "lease-7", "digest-7")
	store.journal = model.GenerationJournal{
		Version: 1, AgentID: "edge-1",
		Candidate: &model.GenerationRecord{
			GenerationID: revisionGenerationID(lease), Revision: 7, SnapshotDigest: "digest-7",
			Phase: model.GenerationPhaseFailed, Lease: lease,
		},
	}
	applyCalls := 0
	runtime := NewRuntimeWithActivator(func(_ context.Context, _, _ model.Snapshot) error {
		applyCalls++
		return nil
	})
	client := &revisionClientStub{events: &events, pull: revisionPull(7, "lease-7", "digest-7")}
	controller := &SyncController{Store: store, Runtime: runtime, SyncClient: client}

	if err := controller.PerformSync(t.Context(), control.SyncRequest{}); err != nil {
		t.Fatalf("PerformSync() error = %v", err)
	}
	if len(client.starts) != 0 || len(client.reports) != 1 || applyCalls != 0 {
		t.Fatalf("failed lease replay start/report/apply = %d/%d/%d, want 0/1/0", len(client.starts), len(client.reports), applyCalls)
	}
}

func TestRevisionSyncRecoversCutoverWithoutDuplicateStartOrApply(t *testing.T) {
	events := []string{}
	store := newRevisionTestStore(&events)
	snapshot := revisionSnapshot(7)
	lease := revisionLease(7, "lease-7", "digest-7")
	store.applied = snapshot
	store.journal = model.GenerationJournal{
		Version: 1,
		AgentID: "edge-1",
		Candidate: &model.GenerationRecord{
			GenerationID: revisionGenerationID(lease), Revision: 7, SnapshotDigest: "digest-7",
			Phase: model.GenerationPhaseCutover, Lease: lease,
		},
	}
	applyCalls := 0
	runtime := NewRuntimeWithActivator(func(_ context.Context, _, _ model.Snapshot) error {
		applyCalls++
		return nil
	})
	if err := runtime.Apply(t.Context(), model.Snapshot{}, snapshot); err != nil {
		t.Fatalf("seed runtime: %v", err)
	}
	applyCalls = 0
	client := &revisionClientStub{events: &events, pull: revisionPull(7, "lease-7", "digest-7")}
	controller := &SyncController{Store: store, Runtime: runtime, SyncClient: client}

	if err := controller.PerformSync(t.Context(), control.SyncRequest{}); err != nil {
		t.Fatalf("PerformSync() error = %v", err)
	}
	if len(client.starts) != 0 || applyCalls != 0 || len(client.reports) != 1 {
		t.Fatalf("start/apply/report calls = %d/%d/%d, want recovered acknowledgement only", len(client.starts), applyCalls, len(client.reports))
	}
	assertEventOrder(t, events, "runtime-state:7", "lkg:7", "journal:active", "report:applied:7")
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

func TestRevisionSyncDoesNotReportFailedBeforeFailedJournalIsDurable(t *testing.T) {
	events := []string{}
	store := newRevisionTestStore(&events)
	store.failGenerationPhase = model.GenerationPhaseFailed
	runtime := NewRuntimeWithActivator(func(_ context.Context, _, next model.Snapshot) error {
		if next.Revision == 7 {
			return errors.New("candidate apply failed")
		}
		return nil
	})
	client := &revisionClientStub{events: &events, pull: revisionPull(7, "lease-7", "digest-7")}
	controller := &SyncController{Store: store, Runtime: runtime, SyncClient: client}

	err := controller.PerformSync(t.Context(), control.SyncRequest{})
	if err == nil || !strings.Contains(err.Error(), "failed journal persistence") {
		t.Fatalf("PerformSync() error = %v, want failed journal persistence error", err)
	}
	if len(client.reports) != 0 {
		t.Fatalf("reports = %+v, want no failed report before durable failed journal", client.reports)
	}
	if store.journal.Candidate == nil || store.journal.Candidate.Phase != model.GenerationPhaseStarted {
		t.Fatalf("durable journal = %+v, want prior started phase", store.journal)
	}
}

func TestRevisionSyncKeepsCutoverWhenPreviousAppliedRestoreFails(t *testing.T) {
	events := []string{}
	store := newRevisionTestStore(&events)
	previous := revisionSnapshot(6)
	store.applied = previous
	store.lkg = previous
	store.runtime = RuntimeState{CurrentRevision: 6, Metadata: map[string]string{"current_revision": "6"}}
	store.failRuntimeState = true
	store.failOnAppliedSave = 2
	runtime := NewRuntime()
	if err := runtime.Apply(t.Context(), model.Snapshot{}, previous); err != nil {
		t.Fatalf("seed runtime: %v", err)
	}
	client := &revisionClientStub{events: &events, pull: revisionPull(7, "lease-7", "digest-7")}
	controller := &SyncController{Store: store, Runtime: runtime, SyncClient: client}

	err := controller.PerformSync(t.Context(), control.SyncRequest{})
	if err == nil || !strings.Contains(err.Error(), "applied persistence fail") {
		t.Fatalf("PerformSync() error = %v, want previous applied restore failure", err)
	}
	if len(client.reports) != 0 {
		t.Fatalf("reports = %+v, want no failed report while candidate remains durable applied", client.reports)
	}
	if store.applied.Revision != 7 || store.journal.Candidate == nil || store.journal.Candidate.Phase != model.GenerationPhaseCutover {
		t.Fatalf("applied/journal = %d/%+v, want recoverable candidate cutover", store.applied.Revision, store.journal)
	}

	store.failRuntimeState = false
	store.failOnAppliedSave = 0
	restartedRuntime := NewRuntime()
	restartedController := &SyncController{Store: store, Runtime: restartedRuntime, SyncClient: client}
	if err := restartedController.PerformSync(t.Context(), control.SyncRequest{}); err != nil {
		t.Fatalf("PerformSync(restart) error = %v", err)
	}
	if restartedRuntime.ActiveSnapshot().Revision != 7 || len(client.starts) != 1 || len(client.reports) != 1 || client.reports[0].Status != "applied" {
		t.Fatalf("restart active/start/report = %d/%d/%+v, want recovered applied revision 7", restartedRuntime.ActiveSnapshot().Revision, len(client.starts), client.reports)
	}
}

func TestRevisionSyncDoesNotReportFailedWhenRuntimeRollbackFails(t *testing.T) {
	events := []string{}
	store := newRevisionTestStore(&events)
	previous := revisionSnapshot(6)
	store.applied = previous
	store.lkg = previous
	runtime := NewRuntimeWithActivator(func(_ context.Context, prior, next model.Snapshot) error {
		if next.Revision == 7 || prior.Revision == 7 {
			return errors.New("runtime transition failed")
		}
		return nil
	})
	if err := runtime.Apply(t.Context(), model.Snapshot{}, previous); err != nil {
		t.Fatalf("seed runtime: %v", err)
	}
	client := &revisionClientStub{events: &events, pull: revisionPull(7, "lease-7", "digest-7")}
	controller := &SyncController{Store: store, Runtime: runtime, SyncClient: client}

	err := controller.PerformSync(t.Context(), control.SyncRequest{})
	if err == nil || !strings.Contains(err.Error(), "runtime rollback") {
		t.Fatalf("PerformSync() error = %v, want runtime rollback failure", err)
	}
	if len(client.reports) != 0 {
		t.Fatalf("reports = %+v, want no failed report before rollback is confirmed", client.reports)
	}
	if store.journal.Candidate == nil || store.journal.Candidate.Phase != model.GenerationPhaseStarted {
		t.Fatalf("journal = %+v, want prior started phase retained", store.journal)
	}
}

func TestRevisionSyncRestoresPersistedAppliedSnapshotBeforeNoUpdate(t *testing.T) {
	events := []string{}
	store := newRevisionTestStore(&events)
	stable := revisionSnapshot(6)
	store.applied = stable
	store.lkg = stable
	store.journal = model.GenerationJournal{
		Version: 1, AgentID: "edge-1",
		Active: &model.GenerationRecord{
			GenerationID: "generation-6", Revision: 6, SnapshotDigest: "digest-6",
			Phase: model.GenerationPhaseActive, Lease: revisionLease(6, "lease-6", "digest-6"), Acknowledged: true,
		},
	}
	applyCalls := 0
	runtime := NewRuntimeWithActivator(func(_ context.Context, _, next model.Snapshot) error {
		applyCalls++
		events = append(events, fmt.Sprintf("runtime:restore:%d", next.Revision))
		return nil
	})
	client := &revisionClientStub{events: &events, pull: model.RevisionPull{DesiredRevision: 6}}
	controller := &SyncController{Store: store, Runtime: runtime, SyncClient: client}

	if err := controller.PerformSync(t.Context(), control.SyncRequest{}); err != nil {
		t.Fatalf("PerformSync() error = %v", err)
	}
	if applyCalls != 1 || runtime.ActiveSnapshot().Revision != 6 {
		t.Fatalf("restore calls/active revision = %d/%d, want restored revision 6", applyCalls, runtime.ActiveSnapshot().Revision)
	}
	assertEventOrder(t, events, "runtime:restore:6", "heartbeat", "pull")
}

func TestRevisionSyncBootstrapsFreshAndLegacyFilesystemStores(t *testing.T) {
	for _, tc := range []struct {
		name             string
		seedLegacy       bool
		wantRuntimeApply int
	}{
		{name: "fresh directory", wantRuntimeApply: 1},
		{name: "legacy applied only", seedLegacy: true, wantRuntimeApply: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, err := NewFilesystem(t.TempDir())
			if err != nil {
				t.Fatalf("NewFilesystem() error = %v", err)
			}
			if tc.seedLegacy {
				if err := store.SaveAppliedSnapshot(revisionSnapshot(6)); err != nil {
					t.Fatalf("seed legacy applied snapshot: %v", err)
				}
			}
			applyCalls := 0
			runtime := NewRuntimeWithActivator(func(_ context.Context, _, _ model.Snapshot) error {
				applyCalls++
				return nil
			})
			client := &revisionClientStub{pull: revisionPull(7, "lease-7", "digest-7")}
			controller := &SyncController{Store: store, Runtime: runtime, SyncClient: client}

			if err := controller.PerformSync(t.Context(), control.SyncRequest{}); err != nil {
				t.Fatalf("PerformSync() error = %v", err)
			}
			if applyCalls != tc.wantRuntimeApply || runtime.ActiveSnapshot().Revision != 7 {
				t.Fatalf("apply calls/active = %d/%d, want %d/7", applyCalls, runtime.ActiveSnapshot().Revision, tc.wantRuntimeApply)
			}
			journal, err := store.LoadGenerationJournal()
			if err != nil || journal.Active == nil || !journal.Active.Acknowledged {
				t.Fatalf("final journal = %+v error=%v, want acknowledged active", journal, err)
			}
			lastKnownGood, err := store.LoadLastKnownGoodSnapshot()
			if err != nil || lastKnownGood.Revision != 7 {
				t.Fatalf("final LKG = %+v error=%v, want revision 7", lastKnownGood, err)
			}
		})
	}
}

func TestRevisionSyncRejectsZeroDeadlineLease(t *testing.T) {
	events := []string{}
	store := newRevisionTestStore(&events)
	pull := revisionPull(7, "lease-7", "digest-7")
	pull.Lease.DeadlineAt = time.Time{}
	client := &revisionClientStub{events: &events, pull: pull}
	controller := &SyncController{Store: store, Runtime: NewRuntime(), SyncClient: client}

	err := controller.PerformSync(t.Context(), control.SyncRequest{})
	if err == nil || !strings.Contains(err.Error(), "deadline") {
		t.Fatalf("PerformSync() error = %v, want zero deadline rejection", err)
	}
	if len(client.starts) != 0 {
		t.Fatalf("start calls = %d, want no start for zero deadline", len(client.starts))
	}
}

func TestRevisionSyncRejectsUnsupportedJournalVersion(t *testing.T) {
	events := []string{}
	store := newRevisionTestStore(&events)
	store.journal = model.GenerationJournal{Version: 99, AgentID: "edge-1"}
	client := &revisionClientStub{events: &events, pull: model.RevisionPull{}}
	controller := &SyncController{Store: store, Runtime: NewRuntime(), SyncClient: client}

	err := controller.PerformSync(t.Context(), control.SyncRequest{})
	if err == nil || !strings.Contains(err.Error(), "journal version") {
		t.Fatalf("PerformSync() error = %v, want unsupported journal version rejection", err)
	}
}

func TestRevisionSyncRejectsUnknownCandidatePhase(t *testing.T) {
	events := []string{}
	store := newRevisionTestStore(&events)
	store.journal = model.GenerationJournal{
		Version: 1, AgentID: "edge-1",
		Candidate: &model.GenerationRecord{Revision: 7, Phase: "unknown"},
	}
	client := &revisionClientStub{events: &events, pull: model.RevisionPull{}}
	controller := &SyncController{Store: store, Runtime: NewRuntime(), SyncClient: client}

	err := controller.PerformSync(t.Context(), control.SyncRequest{})
	if err == nil || !strings.Contains(err.Error(), "invalid phase") {
		t.Fatalf("PerformSync() error = %v, want unknown phase rejection", err)
	}
	if len(client.starts) != 0 {
		t.Fatalf("start calls = %d, want no start from unknown phase", len(client.starts))
	}
}

func TestRevisionSyncRejectsPartialRuntimeGenerationIdentity(t *testing.T) {
	events := []string{}
	store := newRevisionTestStore(&events)
	store.journal = model.GenerationJournal{
		Version: 1, AgentID: "edge-1",
		Candidate: &model.GenerationRecord{
			GenerationID: "operation-7", RuntimeGenerationID: "runtime-7", Revision: 7,
			SnapshotDigest: "digest-7", Phase: model.GenerationPhaseStarted,
		},
	}
	client := &revisionClientStub{events: &events, pull: revisionPull(7, "lease-7", "digest-7")}
	controller := &SyncController{Store: store, Runtime: newManagedRevisionRuntime(t), SyncClient: client}

	err := controller.PerformSync(t.Context(), control.SyncRequest{})
	if err == nil || !strings.Contains(err.Error(), "incomplete runtime identity") {
		t.Fatalf("PerformSync() error = %v, want partial runtime identity rejection", err)
	}
	if len(client.starts) != 0 || len(client.reports) != 0 {
		t.Fatalf("partial identity start/report = %d/%d, want no remote effects", len(client.starts), len(client.reports))
	}
}

func TestRevisionSyncRejectsRuntimeGenerationIdentityWithoutManager(t *testing.T) {
	events := []string{}
	store := newRevisionTestStore(&events)
	lease := revisionLease(7, "lease-7", "digest-7")
	store.journal = model.GenerationJournal{
		Version: 1, AgentID: "edge-1",
		Candidate: &model.GenerationRecord{
			GenerationID: "operation-7", RuntimeGenerationID: "runtime-7", RuntimeSnapshotHash: "hash-7",
			Revision: 7, SnapshotDigest: "digest-7", Phase: model.GenerationPhaseStarted, Lease: lease,
		},
	}
	client := &revisionClientStub{events: &events, pull: revisionPull(7, "lease-7", "digest-7")}
	controller := &SyncController{Store: store, Runtime: NewRuntime(), SyncClient: client}

	err := controller.PerformSync(t.Context(), control.SyncRequest{})
	if err == nil || !strings.Contains(err.Error(), "requires a generation manager") {
		t.Fatalf("PerformSync() error = %v, want incompatible runtime rejection", err)
	}
	if len(client.starts) != 0 || len(client.reports) != 0 || controller.Runtime.ActiveSnapshot().Revision != 0 {
		t.Fatalf("incompatible runtime start/report/active = %d/%d/%d, want no effects", len(client.starts), len(client.reports), controller.Runtime.ActiveSnapshot().Revision)
	}
}

func TestRevisionSyncAppliesFullSnapshotWithoutLegacyMerge(t *testing.T) {
	events := []string{}
	store := newRevisionTestStore(&events)
	previous := revisionSnapshot(6)
	previous.Rules = []model.HTTPRule{{FrontendURL: "https://stale.example"}}
	store.desired = previous
	store.applied = previous
	store.lkg = previous
	runtime := NewRuntime()
	if err := runtime.Apply(t.Context(), model.Snapshot{}, previous); err != nil {
		t.Fatalf("seed runtime: %v", err)
	}
	client := &revisionClientStub{events: &events, pull: revisionPull(7, "lease-7", "digest-7")}
	controller := &SyncController{Store: store, Runtime: runtime, SyncClient: client}

	if err := controller.PerformSync(t.Context(), control.SyncRequest{}); err != nil {
		t.Fatalf("PerformSync() error = %v", err)
	}
	if store.desired.Rules == nil || len(store.desired.Rules) != 0 || store.applied.Rules == nil || len(store.applied.Rules) != 0 {
		t.Fatalf("desired/applied rules = %+v/%+v, want explicit empty full snapshot", store.desired.Rules, store.applied.Rules)
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

func TestRevisionSyncHeartbeatFailurePreservesApplySuccessAndClearsOnRecovery(t *testing.T) {
	events := []string{}
	store := newRevisionTestStore(&events)
	applied := revisionSnapshot(7)
	if err := store.SaveAppliedSnapshot(applied); err != nil {
		t.Fatalf("SaveAppliedSnapshot() error = %v", err)
	}
	if err := store.SaveRuntimeState(RuntimeState{
		CurrentRevision: applied.Revision,
		Metadata: map[string]string{
			"last_apply_revision": "7",
			"last_apply_status":   "success",
			"last_apply_message":  "",
		},
	}); err != nil {
		t.Fatalf("SaveRuntimeState() error = %v", err)
	}
	runtime := NewRuntime()
	if err := runtime.Apply(t.Context(), model.Snapshot{}, applied); err != nil {
		t.Fatalf("seed runtime: %v", err)
	}
	heartbeatErr := errors.New("heartbeat failed: 503 Service Unavailable")
	client := &revisionClientStub{events: &events, heartbeatErr: heartbeatErr}
	controller := &SyncController{Store: store, Runtime: runtime, SyncClient: client}

	if err := controller.PerformSync(t.Context(), control.SyncRequest{}); !errors.Is(err, heartbeatErr) {
		t.Fatalf("PerformSync() error = %v, want heartbeat failure", err)
	}
	state, err := store.LoadRuntimeState()
	if err != nil {
		t.Fatalf("LoadRuntimeState() error = %v", err)
	}
	if state.Metadata["last_sync_error"] != heartbeatErr.Error() {
		t.Fatalf("last_sync_error = %q, want %q", state.Metadata["last_sync_error"], heartbeatErr)
	}
	if state.Metadata["last_apply_revision"] != "7" || state.Metadata["last_apply_status"] != "success" || state.Metadata["last_apply_message"] != "" {
		t.Fatalf("apply metadata after heartbeat failure = %+v, want preserved success", state.Metadata)
	}

	client.heartbeatErr = nil
	if err := controller.PerformSync(t.Context(), control.SyncRequest{}); err != nil {
		t.Fatalf("PerformSync(recovery) error = %v", err)
	}
	state, err = store.LoadRuntimeState()
	if err != nil {
		t.Fatalf("LoadRuntimeState(recovery) error = %v", err)
	}
	if _, ok := state.Metadata["last_sync_error"]; ok {
		t.Fatalf("last_sync_error not cleared after recovery: %+v", state.Metadata)
	}
	if state.Metadata["last_apply_status"] != "success" {
		t.Fatalf("last_apply_status = %q, want success", state.Metadata["last_apply_status"])
	}
}

func TestRevisionSyncSuccessfulNoUpdateRepairsLegacyHeartbeatApplyError(t *testing.T) {
	events := []string{}
	store := newRevisionTestStore(&events)
	applied := revisionSnapshot(7)
	runtime := NewRuntime()
	if err := runtime.Apply(t.Context(), model.Snapshot{}, applied); err != nil {
		t.Fatalf("seed runtime: %v", err)
	}
	heartbeatMessage := "heartbeat failed: 503 Service Unavailable"
	if err := store.SaveRuntimeState(RuntimeState{
		CurrentRevision: applied.Revision,
		Metadata: map[string]string{
			"last_sync_error":     heartbeatMessage,
			"last_apply_revision": "7",
			"last_apply_status":   "error",
			"last_apply_message":  heartbeatMessage,
		},
	}); err != nil {
		t.Fatalf("SaveRuntimeState() error = %v", err)
	}
	controller := &SyncController{
		Store: store, Runtime: runtime,
		SyncClient: &revisionClientStub{events: &events},
	}

	if err := controller.PerformSync(t.Context(), control.SyncRequest{}); err != nil {
		t.Fatalf("PerformSync() error = %v", err)
	}
	state, err := store.LoadRuntimeState()
	if err != nil {
		t.Fatalf("LoadRuntimeState() error = %v", err)
	}
	if _, ok := state.Metadata["last_sync_error"]; ok {
		t.Fatalf("last_sync_error not cleared: %+v", state.Metadata)
	}
	if state.Metadata["last_apply_revision"] != "7" || state.Metadata["last_apply_status"] != "success" || state.Metadata["last_apply_message"] != "" {
		t.Fatalf("legacy heartbeat apply metadata = %+v, want repaired success", state.Metadata)
	}
}

func TestRevisionSyncSuccessfulNoUpdateRepairsLegacyPackageApplyError(t *testing.T) {
	events := []string{}
	store := newRevisionTestStore(&events)
	applied := revisionSnapshot(7)
	runtime := NewRuntime()
	if err := runtime.Apply(t.Context(), model.Snapshot{}, applied); err != nil {
		t.Fatalf("seed runtime: %v", err)
	}
	packageMessage := "durable generation is not ready for hot restart"
	if err := store.SaveRuntimeState(RuntimeState{
		CurrentRevision: applied.Revision,
		Metadata: map[string]string{
			"last_sync_error":     packageMessage,
			"last_apply_revision": "7",
			"last_apply_status":   "error",
			"last_apply_message":  packageMessage,
		},
	}); err != nil {
		t.Fatalf("SaveRuntimeState() error = %v", err)
	}
	controller := &SyncController{
		Store: store, Runtime: runtime,
		SyncClient: &revisionClientStub{events: &events},
	}

	if err := controller.PerformSync(t.Context(), control.SyncRequest{}); err != nil {
		t.Fatalf("PerformSync() error = %v", err)
	}
	state, err := store.LoadRuntimeState()
	if err != nil {
		t.Fatalf("LoadRuntimeState() error = %v", err)
	}
	if _, ok := state.Metadata["last_sync_error"]; ok {
		t.Fatalf("last_sync_error not cleared: %+v", state.Metadata)
	}
	if state.Metadata["last_apply_revision"] != "7" || state.Metadata["last_apply_status"] != "success" || state.Metadata["last_apply_message"] != "" {
		t.Fatalf("legacy package apply metadata = %+v, want repaired success", state.Metadata)
	}
}

func TestRevisionSyncSuccessfulNoUpdatePreservesRealApplyError(t *testing.T) {
	events := []string{}
	store := newRevisionTestStore(&events)
	applied := revisionSnapshot(7)
	runtime := NewRuntime()
	if err := runtime.Apply(t.Context(), model.Snapshot{}, applied); err != nil {
		t.Fatalf("seed runtime: %v", err)
	}
	applyMessage := "runtime apply failed"
	if err := store.SaveRuntimeState(RuntimeState{
		CurrentRevision: applied.Revision,
		Metadata: map[string]string{
			"last_sync_error":     applyMessage,
			"last_apply_revision": "7",
			"last_apply_status":   "error",
			"last_apply_message":  applyMessage,
		},
	}); err != nil {
		t.Fatalf("SaveRuntimeState() error = %v", err)
	}
	controller := &SyncController{
		Store: store, Runtime: runtime,
		SyncClient: &revisionClientStub{events: &events},
	}

	if err := controller.PerformSync(t.Context(), control.SyncRequest{}); err != nil {
		t.Fatalf("PerformSync() error = %v", err)
	}
	state, err := store.LoadRuntimeState()
	if err != nil {
		t.Fatalf("LoadRuntimeState() error = %v", err)
	}
	if _, ok := state.Metadata["last_sync_error"]; ok {
		t.Fatalf("last_sync_error not cleared: %+v", state.Metadata)
	}
	if state.Metadata["last_apply_revision"] != "7" || state.Metadata["last_apply_status"] != "error" || state.Metadata["last_apply_message"] != applyMessage {
		t.Fatalf("real apply metadata = %+v, want preserved failure", state.Metadata)
	}
}

type revisionClientStub struct {
	events            *[]string
	heartbeatSnapshot model.Snapshot
	heartbeatErr      error
	pull              model.RevisionPull
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

func (s *revisionTestStore) LoadLastKnownGoodSnapshot() (model.Snapshot, error) {
	return s.lkg, nil
}

func (s *revisionTestStore) record(event string) {
	if s.events != nil {
		*s.events = append(*s.events, event)
	}
}

func revisionPull(revision int64, leaseID, digest string) model.RevisionPull {
	snapshot := revisionSnapshot(revision)
	lease := revisionLease(revision, leaseID, digest)
	return model.RevisionPull{
		HasUpdate: true, DesiredRevision: revision, Lease: &lease, Snapshot: &snapshot,
		VerifiedSnapshotDigest: digest,
	}
}

func revisionSnapshot(revision int64) model.Snapshot {
	payload := fmt.Sprintf(`{"desired_version":"v%d","desired_revision":%d,"agent_config":{},"rules":[],"l4_rules":[],"relay_listeners":[],"egress_profiles":[],"certificates":[],"certificate_policies":[]}`, revision, revision)
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
