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
	if len(client.starts) != 0 || len(client.reports) != 0 || applyCalls != 0 {
		t.Fatalf("failed lease replayed start/report/apply = %d/%d/%d", len(client.starts), len(client.reports), applyCalls)
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
	s.journal = journal
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
	return nil
}

func (s *revisionTestStore) LoadGenerationJournal() (model.GenerationJournal, error) {
	return s.journal, nil
}

func (s *revisionTestStore) SaveLastKnownGoodSnapshot(snapshot model.Snapshot) error {
	s.lkg = snapshot
	s.record(fmt.Sprintf("lkg:%d", snapshot.Revision))
	return nil
}

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

func revisionLease(revision int64, leaseID, digest string) model.RevisionLease {
	return model.RevisionLease{
		AgentID: "edge-1", Revision: revision, Attempt: 1, LeaseID: leaseID,
		SnapshotDigest: digest, DesiredVersion: fmt.Sprintf("v%d", revision),
		ApplyTimeoutSeconds: 60, DrainTimeoutSeconds: 600,
		DeadlineAt: time.Now().Add(time.Hour),
	}
}

func revisionSnapshot(revision int64) model.Snapshot {
	payload := fmt.Sprintf(`{"desired_version":"v%d","desired_revision":%d,"agent_config":{},"rules":[],"l4_rules":[],"relay_listeners":[],"wireguard_profiles":[],"egress_profiles":[],"certificates":[],"certificate_policies":[]}`, revision, revision)
	var snapshot model.Snapshot
	if err := json.Unmarshal([]byte(payload), &snapshot); err != nil {
		panic(err)
	}
	return snapshot
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
