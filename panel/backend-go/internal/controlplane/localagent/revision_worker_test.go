package localagent

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	goagentembedded "github.com/sakullla/nginx-reverse-emby/go-agent/embedded"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/service"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

type revisionClientStub struct {
	mu          sync.Mutex
	pulls       []service.RemoteRevisionPull
	pullCalls   int
	startCalls  []service.RemoteRevisionStart
	reportCalls []service.RemoteRevisionReport
	status      service.AgentRevisionStatus
	onPull      func(int)
	reported    chan service.RemoteRevisionReport
	reportErrs  map[string]error
}

func (s *revisionClientStub) PullRemoteRevision(context.Context, string) (service.RemoteRevisionPull, error) {
	s.mu.Lock()
	s.pullCalls++
	call := s.pullCalls
	var result service.RemoteRevisionPull
	if len(s.pulls) > 0 {
		result = s.pulls[0]
		s.pulls = s.pulls[1:]
	}
	onPull := s.onPull
	s.mu.Unlock()
	if onPull != nil {
		onPull(call)
	}
	return result, nil
}

func (s *revisionClientStub) GetAgentRevisionStatus(context.Context, string, int64) (service.AgentRevisionStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status, nil
}

func (s *revisionClientStub) StartRemoteRevision(_ context.Context, _ string, input service.RemoteRevisionStart) (service.AgentRevisionStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.startCalls = append(s.startCalls, input)
	return s.status, nil
}

func (s *revisionClientStub) ReportRemoteRevision(_ context.Context, _ string, input service.RemoteRevisionReport) (service.AgentRevisionStatus, error) {
	s.mu.Lock()
	s.reportCalls = append(s.reportCalls, input)
	status := s.status
	reported := s.reported
	reportErr := s.reportErrs[input.Status]
	s.mu.Unlock()
	if reported != nil {
		select {
		case reported <- input:
		default:
		}
	}
	return status, reportErr
}

type revisionLedgerStub struct {
	pointer      storage.AgentRevisionPointerRow
	pointerOK    bool
	revision     storage.AgentRevisionRow
	revisionOK   bool
	attempts     []storage.AgentRevisionAttemptRow
	generations  []storage.AgentGenerationRow
	runtimeState storage.RuntimeState
}

func (s revisionLedgerStub) GetAgentRevisionPointer(context.Context, string) (storage.AgentRevisionPointerRow, bool, error) {
	return s.pointer, s.pointerOK, nil
}

func (s revisionLedgerStub) GetCoordinatorRevision(context.Context, string, int64) (storage.AgentRevisionRow, bool, error) {
	return s.revision, s.revisionOK, nil
}

func (s revisionLedgerStub) ListCoordinatorAttempts(context.Context, string, int64) ([]storage.AgentRevisionAttemptRow, error) {
	return append([]storage.AgentRevisionAttemptRow(nil), s.attempts...), nil
}

func (s revisionLedgerStub) ListCoordinatorGenerations(context.Context, string) ([]storage.AgentGenerationRow, error) {
	return append([]storage.AgentGenerationRow(nil), s.generations...), nil
}

func (s revisionLedgerStub) LoadLocalRuntimeState(context.Context) (storage.RuntimeState, error) {
	return s.runtimeState, nil
}

type revisionRuntimeStub struct {
	mu            sync.Mutex
	snapshots     []storage.Snapshot
	applied       chan struct{}
	drainSnapshot goagentembedded.GenerationDrainSnapshot
}

func (s *revisionRuntimeStub) ApplyRevision(_ context.Context, snapshot storage.Snapshot) error {
	s.mu.Lock()
	s.snapshots = append(s.snapshots, snapshot)
	s.mu.Unlock()
	select {
	case s.applied <- struct{}{}:
	default:
	}
	return nil
}

func (s *revisionRuntimeStub) ApplyRevisionWithDrainTimeout(ctx context.Context, snapshot storage.Snapshot, _ time.Duration) error {
	return s.ApplyRevision(ctx, snapshot)
}

func (s *revisionRuntimeStub) GenerationDrainSnapshot() goagentembedded.GenerationDrainSnapshot {
	return s.drainSnapshot
}

func TestRevisionWorkerClaimsStartsAppliesAndReports(t *testing.T) {
	lease := service.RemoteRevisionLease{
		AgentID: "local", Revision: 4, RetryCycle: 1, Attempt: 2,
		LeaseID: "lease-4", ApplyTimeoutSeconds: 60, DrainTimeoutSeconds: 600,
		DeadlineAt: time.Now().Add(time.Minute),
	}
	client := &revisionClientStub{
		pulls: []service.RemoteRevisionPull{{
			HasUpdate: true, DesiredRevision: 4, Lease: &lease,
			Snapshot: &storage.Snapshot{Revision: 4},
		}},
		status: service.AgentRevisionStatus{Attempts: []service.RevisionAttempt{{
			RetryCycle: 1, Attempt: 2, State: storage.AgentRevisionAttemptStateLeased,
		}}},
		reported: make(chan service.RemoteRevisionReport, 2),
	}
	runtime := &revisionRuntimeStub{applied: make(chan struct{}, 1)}
	worker, err := NewRevisionWorker("local", client, revisionLedgerStub{}, runtime)
	if err != nil {
		t.Fatalf("NewRevisionWorker() error = %v", err)
	}
	worker.pollInterval = time.Hour

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx) }()

	select {
	case report := <-client.reported:
		if report.Status != storage.AgentRevisionStateApplied {
			t.Fatalf("first report status = %q, want applied", report.Status)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not report the claimed revision")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	client.mu.Lock()
	defer client.mu.Unlock()
	if len(client.startCalls) != 1 {
		t.Fatalf("start calls = %d, want 1", len(client.startCalls))
	}
	if len(client.reportCalls) == 0 || client.reportCalls[0].Status != storage.AgentRevisionStateApplied {
		t.Fatalf("report calls = %+v, want applied report", client.reportCalls)
	}
	if client.startCalls[0].GenerationID == "" || client.startCalls[0].GenerationID != client.reportCalls[0].GenerationID {
		t.Fatalf("generation ids = start %q report %q", client.startCalls[0].GenerationID, client.reportCalls[0].GenerationID)
	}
}

func TestRevisionWorkerReportsExactPluginStatusAndRetriesCommittedReconciliation(t *testing.T) {
	digest := strings.Repeat("a", 64)
	lease := service.RemoteRevisionLease{
		AgentID: "local", Revision: 4, RetryCycle: 1, Attempt: 2,
		LeaseID: "lease-4", ApplyTimeoutSeconds: 60, DrainTimeoutSeconds: 600,
		DeadlineAt: time.Now().Add(time.Minute),
	}
	client := &revisionClientStub{
		pulls: []service.RemoteRevisionPull{{
			HasUpdate: true, DesiredRevision: 4, Lease: &lease,
			Snapshot: &storage.Snapshot{Revision: 4},
		}},
		status: service.AgentRevisionStatus{Attempts: []service.RevisionAttempt{{
			RetryCycle: 1, Attempt: 2, State: storage.AgentRevisionAttemptStateLeased,
		}}},
		reportErrs: map[string]error{storage.AgentRevisionStateApplied: errors.New("injected lifecycle reconciliation failure")},
	}
	pluginStatus := storage.PluginRuntimeStatus{
		InstanceID: "instance", PluginID: "plugin", OperationID: "operation", Revision: 4,
		GenerationID: digest, PackageDigest: digest, ArtifactDigest: digest, ConfigVersion: 2,
		RuntimeKind: "rpc-service", State: "active", Sequence: 1, SafeDetail: "runtime ready",
	}
	ledger := &revisionLedgerStub{runtimeState: storage.RuntimeState{CurrentRevision: 4, PluginStatuses: []storage.PluginRuntimeStatus{pluginStatus}}}
	worker, err := NewRevisionWorker("local", client, ledger, &revisionRuntimeStub{applied: make(chan struct{}, 1)})
	if err != nil {
		t.Fatal(err)
	}
	if processed, err := worker.processNext(t.Context()); err == nil || processed {
		t.Fatalf("first processNext() = processed %v, error %v", processed, err)
	}
	client.mu.Lock()
	if len(client.reportCalls) != 1 || len(client.reportCalls[0].PluginStatuses) != 1 || client.reportCalls[0].PluginStatuses[0].SafeDetail != "runtime ready" {
		client.mu.Unlock()
		t.Fatalf("first applied report = %+v", client.reportCalls)
	}
	client.reportErrs[storage.AgentRevisionStateApplied] = nil
	client.mu.Unlock()
	ledger.pointer = storage.AgentRevisionPointerRow{AgentID: "local", AppliedRevision: 4, DesiredRevision: 4}
	ledger.pointerOK = true
	ledger.revision = storage.AgentRevisionRow{
		AgentID: "local", Revision: 4, RetryCycle: 1, AttemptCount: 2,
		State: storage.AgentRevisionStateApplied, GenerationID: embeddedGenerationID(lease),
	}
	ledger.revisionOK = true
	ledger.attempts = []storage.AgentRevisionAttemptRow{{
		AgentID: "local", Revision: 4, RetryCycle: 1, Attempt: 2,
		LeaseID: lease.LeaseID, State: storage.AgentRevisionAttemptStateApplied,
	}}
	if err := worker.runCycle(t.Context()); err != nil {
		t.Fatalf("retry runCycle() error = %v", err)
	}
	client.mu.Lock()
	defer client.mu.Unlock()
	if len(client.reportCalls) != 2 || len(client.reportCalls[1].PluginStatuses) != 1 || client.reportCalls[1].PluginStatuses[0].Sequence != 1 {
		t.Fatalf("retried applied reports = %+v", client.reportCalls)
	}
}

func TestRevisionWorkerResumesStartedLeaseAfterRestart(t *testing.T) {
	lease := service.RemoteRevisionLease{
		AgentID: "local", Revision: 8, RetryCycle: 0, Attempt: 1,
		LeaseID: "lease-restart", ApplyTimeoutSeconds: 60, DrainTimeoutSeconds: 600,
		DeadlineAt: time.Now().Add(time.Minute),
	}
	client := &revisionClientStub{
		pulls: []service.RemoteRevisionPull{{
			HasUpdate: true, DesiredRevision: 8, Lease: &lease,
			Snapshot: &storage.Snapshot{Revision: 8},
		}},
		status: service.AgentRevisionStatus{Attempts: []service.RevisionAttempt{{
			RetryCycle: 0, Attempt: 1, State: storage.AgentRevisionAttemptStateStarted,
		}}},
		reported: make(chan service.RemoteRevisionReport, 1),
	}
	worker, err := NewRevisionWorker("local", client, revisionLedgerStub{}, &revisionRuntimeStub{applied: make(chan struct{}, 1)})
	if err != nil {
		t.Fatalf("NewRevisionWorker() error = %v", err)
	}
	worker.pollInterval = time.Hour
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx) }()
	select {
	case <-client.reported:
	case <-time.After(2 * time.Second):
		t.Fatal("restarted worker did not resume the started lease")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	client.mu.Lock()
	defer client.mu.Unlock()
	if len(client.startCalls) != 0 {
		t.Fatalf("start calls for resumed lease = %d, want 0", len(client.startCalls))
	}
}

func TestRevisionWorkerRecoversOutstandingDrainFromLedger(t *testing.T) {
	client := &revisionClientStub{reported: make(chan service.RemoteRevisionReport, 1)}
	ledger := revisionLedgerStub{
		pointerOK:  true,
		pointer:    storage.AgentRevisionPointerRow{AgentID: "local", AppliedRevision: 6, DesiredRevision: 6},
		revisionOK: true,
		revision: storage.AgentRevisionRow{
			AgentID: "local", Revision: 6, State: storage.AgentRevisionStateApplied,
			DrainState: storage.AgentRevisionDrainStateDraining, RetryCycle: 0, AttemptCount: 1,
		},
		attempts: []storage.AgentRevisionAttemptRow{{
			AgentID: "local", Revision: 6, RetryCycle: 0, Attempt: 1,
			LeaseID: "lease-drain", State: storage.AgentRevisionAttemptStateApplied,
		}},
		generations: []storage.AgentGenerationRow{{
			AgentID: "local", GenerationID: "embedded-previous", Revision: 5, State: storage.GenerationStateDraining,
		}},
	}
	runtime := &revisionRuntimeStub{
		applied: make(chan struct{}, 1),
		drainSnapshot: goagentembedded.GenerationDrainSnapshot{Generations: []goagentembedded.GenerationDrainStatus{{
			GenerationID: "embedded-previous", Revision: 5,
			State: goagentembedded.GenerationDrainStateDrained, CompletedAt: time.Now().UTC(),
		}}},
	}
	worker, err := NewRevisionWorker("local", client, ledger, runtime)
	if err != nil {
		t.Fatalf("NewRevisionWorker() error = %v", err)
	}
	worker.pollInterval = time.Hour
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx) }()
	select {
	case report := <-client.reported:
		if report.Status != storage.AgentRevisionDrainStateDrained || report.GenerationID != "embedded-previous" {
			t.Fatalf("drain report = %+v", report)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not recover the outstanding drain")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run() error = %v", err)
	}
}

func TestRevisionWorkerStaleDrainDoesNotBlockNewDesiredRevision(t *testing.T) {
	lease := service.RemoteRevisionLease{
		AgentID: "local", Revision: 7, RetryCycle: 0, Attempt: 1,
		LeaseID: "lease-new", ApplyTimeoutSeconds: 60, DrainTimeoutSeconds: 600,
		DeadlineAt: time.Now().Add(time.Minute),
	}
	client := &revisionClientStub{
		pulls: []service.RemoteRevisionPull{{
			HasUpdate: true, DesiredRevision: 7, Lease: &lease,
			Snapshot: &storage.Snapshot{Revision: 7},
		}},
		status: service.AgentRevisionStatus{Attempts: []service.RevisionAttempt{{
			RetryCycle: 0, Attempt: 1, State: storage.AgentRevisionAttemptStateLeased,
		}}},
		reportErrs: map[string]error{storage.AgentRevisionDrainStateDrained: errors.New("drain lease expired")},
	}
	ledger := revisionLedgerStub{
		pointerOK:  true,
		pointer:    storage.AgentRevisionPointerRow{AgentID: "local", AppliedRevision: 6, DesiredRevision: 7},
		revisionOK: true,
		revision: storage.AgentRevisionRow{
			AgentID: "local", Revision: 6, State: storage.AgentRevisionStateApplied,
			DrainState: storage.AgentRevisionDrainStateDraining, RetryCycle: 0, AttemptCount: 1,
		},
		attempts: []storage.AgentRevisionAttemptRow{{
			AgentID: "local", Revision: 6, RetryCycle: 0, Attempt: 1,
			LeaseID: "lease-old", State: storage.AgentRevisionAttemptStateApplied,
		}},
		generations: []storage.AgentGenerationRow{{
			AgentID: "local", GenerationID: "embedded-stale", Revision: 5, State: storage.GenerationStateDraining,
		}},
	}
	runtime := &revisionRuntimeStub{
		applied: make(chan struct{}, 1),
		drainSnapshot: goagentembedded.GenerationDrainSnapshot{Generations: []goagentembedded.GenerationDrainStatus{{
			GenerationID: "embedded-stale", Revision: 5,
			State: goagentembedded.GenerationDrainStateDrained, CompletedAt: time.Now().UTC(),
		}}},
	}
	worker, err := NewRevisionWorker("local", client, ledger, runtime)
	if err != nil {
		t.Fatalf("NewRevisionWorker() error = %v", err)
	}
	if err := worker.runCycle(t.Context()); err == nil || !strings.Contains(err.Error(), "drain lease expired") {
		t.Fatalf("runCycle() error = %v, want stale drain context", err)
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if len(runtime.snapshots) != 1 || runtime.snapshots[0].Revision != 7 {
		t.Fatalf("applied snapshots = %+v, want revision 7 despite stale drain", runtime.snapshots)
	}
}

func TestRevisionWorkerWakeRechecksDependencyFrontierWithoutLosingSignal(t *testing.T) {
	secondPull := make(chan struct{}, 1)
	client := &revisionClientStub{onPull: func(call int) {
		if call == 2 {
			secondPull <- struct{}{}
		}
	}}
	worker, err := NewRevisionWorker("local", client, revisionLedgerStub{}, &revisionRuntimeStub{applied: make(chan struct{}, 1)})
	if err != nil {
		t.Fatalf("NewRevisionWorker() error = %v", err)
	}
	worker.pollInterval = time.Hour
	worker.Wake()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx) }()
	select {
	case <-secondPull:
	case <-time.After(2 * time.Second):
		t.Fatal("wake signal was lost before the worker started")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run() error = %v", err)
	}
}

func TestRevisionWorkerDoesNotClaimAfterShutdown(t *testing.T) {
	client := &revisionClientStub{}
	worker, err := NewRevisionWorker("local", client, revisionLedgerStub{}, &revisionRuntimeStub{applied: make(chan struct{}, 1)})
	if err != nil {
		t.Fatalf("NewRevisionWorker() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := worker.Run(ctx); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	client.mu.Lock()
	defer client.mu.Unlock()
	if client.pullCalls != 0 {
		t.Fatalf("pull calls after shutdown = %d, want 0", client.pullCalls)
	}
}
