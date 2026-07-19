package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/control"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/generation"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	agentmodule "github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
)

func TestUpdateManagerStagesLegacyPolicyWithoutManifestMetadata(t *testing.T) {
	payload := []byte("legacy-policy-agent")
	source := filepath.Join(t.TempDir(), "nre-agent-linux-amd64")
	if err := os.WriteFile(source, payload, 0o755); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(payload)
	mgr := NewUpdateManager(t.TempDir(), "", nil, nil, nil, nil)
	mgr.platform = "linux-amd64"

	staged, err := mgr.Stage(t.Context(), model.VersionPackage{
		URL: "file:///" + filepath.ToSlash(source), SHA256: hex.EncodeToString(digest[:]), Platform: "linux-amd64",
	})
	if err != nil {
		t.Fatalf("Stage() error = %v", err)
	}
	manifestPayload, err := os.ReadFile(filepath.Join(filepath.Dir(staged), packageManifestFile))
	if err != nil {
		t.Fatal(err)
	}
	var manifest model.PackageManifest
	if err := json.Unmarshal(manifestPayload, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Filename != filepath.Base(source) || manifest.Size != int64(len(payload)) {
		t.Fatalf("derived manifest = %+v", manifest)
	}
}

func TestRevisionSyncAppliesHeartbeatPackageWithoutRevision(t *testing.T) {
	store := &reviewJournalStore{InMemory: NewInMemory()}
	updater := &reviewUpdater{}
	client := &reviewRevisionClient{heartbeat: model.Snapshot{
		DesiredVersion: "bundled", VersionPackage: &model.VersionPackage{URL: "https://example.test/nre-agent", SHA256: "digest"},
	}}
	controller := &SyncController{
		Store: store, Runtime: NewRuntime(), SyncClient: client, Updater: updater,
	}

	err := controller.PerformSyncPlan(t.Context(), SyncPlan{Request: control.SyncRequest{}})
	if !errors.Is(err, ErrRestartRequested) {
		t.Fatalf("PerformSyncPlan() error = %v, want ErrRestartRequested", err)
	}
	if updater.preflightCalls != 1 || updater.stageCalls != 1 || updater.activateCalls != 1 {
		t.Fatalf("package calls = %d/%d/%d, want 1/1/1", updater.preflightCalls, updater.stageCalls, updater.activateCalls)
	}
}

func TestPendingUpdateSkipsPreflightForRunningPackage(t *testing.T) {
	updater := &reviewUpdater{preflightErr: errors.New("unsupported platform")}
	controller := &SyncController{Updater: updater, CurrentPackageSHA256: "abc123"}

	err := controller.HandlePendingUpdate(t.Context(), model.Snapshot{
		VersionPackage: &model.VersionPackage{SHA256: " ABC123 ", Platform: "darwin-arm64"},
	})
	if err != nil {
		t.Fatalf("HandlePendingUpdate() error = %v", err)
	}
	if updater.preflightCalls != 0 || updater.stageCalls != 0 || updater.activateCalls != 0 {
		t.Fatalf("package calls = %d/%d/%d, want no update work", updater.preflightCalls, updater.stageCalls, updater.activateCalls)
	}
}

func TestUpdateManagerPromotesAndRestoresInstalledExecutable(t *testing.T) {
	root := t.TempDir()
	installedPath := filepath.Join(root, "bin", "nre-agent")
	if err := os.MkdirAll(filepath.Dir(installedPath), 0o755); err != nil {
		t.Fatal(err)
	}
	oldPayload := []byte("old-agent")
	newPayload := []byte("new-agent")
	if err := os.WriteFile(installedPath, oldPayload, 0o755); err != nil {
		t.Fatal(err)
	}
	sourcePath := filepath.Join(root, "release-agent")
	if err := os.WriteFile(sourcePath, newPayload, 0o755); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(newPayload)
	pkg := model.VersionPackage{
		URL: "file:///" + filepath.ToSlash(sourcePath), SHA256: hex.EncodeToString(digest[:]),
		Platform: "linux-amd64", Filename: "nre-agent-linux-amd64", Size: int64(len(newPayload)),
	}

	var launchedEnv []string
	mgr := NewUpdateManager(root, installedPath, []string{installedPath, "serve"}, []string{"PATH=/bin"}, func(binary string, argv, env []string) error {
		installed, err := os.ReadFile(installedPath)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(installed, newPayload) {
			t.Fatalf("installed executable at launch = %q, want %q", installed, newPayload)
		}
		launchedEnv = append([]string(nil), env...)
		return ErrRestartRequested
	}, nil)
	mgr.platform = "linux-amd64"
	stagedPath, err := mgr.Stage(t.Context(), pkg)
	if err != nil {
		t.Fatal(err)
	}
	if err := mgr.Activate(stagedPath, "2.0.0"); !errors.Is(err, ErrRestartRequested) {
		t.Fatalf("Activate() error = %v", err)
	}
	childManager := NewUpdateManager(root, stagedPath, []string{stagedPath}, launchedEnv, func(string, []string, []string) error { return nil }, nil)
	if childManager.executablePath != installedPath {
		t.Fatalf("child install target = %q, want %q", childManager.executablePath, installedPath)
	}

	activationErr := errors.New("child failed")
	failing := NewUpdateManager(root, installedPath, []string{installedPath}, nil, func(string, []string, []string) error {
		return activationErr
	}, nil)
	failing.platform = "linux-amd64"
	oldSource := filepath.Join(root, "rollback-agent")
	if err := os.WriteFile(oldSource, oldPayload, 0o755); err != nil {
		t.Fatal(err)
	}
	oldDigest := sha256.Sum256(oldPayload)
	oldStaged, err := failing.Stage(t.Context(), model.VersionPackage{
		URL: "file:///" + filepath.ToSlash(oldSource), SHA256: hex.EncodeToString(oldDigest[:]),
		Platform: "linux-amd64", Filename: "nre-agent", Size: int64(len(oldPayload)),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := failing.Activate(oldStaged, "1.0.0"); !errors.Is(err, activationErr) {
		t.Fatalf("Activate(rollback) error = %v", err)
	}
	installed, err := os.ReadFile(installedPath)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(installed, newPayload) {
		t.Fatalf("installed executable after rollback = %q, want %q", installed, newPayload)
	}
}

func TestUpdateManagerReconcilesPointerAheadCrashBeforeRetry(t *testing.T) {
	root := t.TempDir()
	installedPath := filepath.Join(root, "bin", "nre-agent")
	if err := os.MkdirAll(filepath.Dir(installedPath), 0o755); err != nil {
		t.Fatal(err)
	}
	oldPayload := []byte("old-agent")
	newPayload := []byte("failed-new-agent")
	if err := os.WriteFile(installedPath, oldPayload, 0o755); err != nil {
		t.Fatal(err)
	}
	newSource := filepath.Join(root, "new-agent")
	if err := os.WriteFile(newSource, newPayload, 0o755); err != nil {
		t.Fatal(err)
	}
	newDigest := sha256.Sum256(newPayload)

	setup := NewUpdateManager(root, installedPath, nil, nil, func(string, []string, []string) error { return nil }, nil)
	setup.platform = "linux-amd64"
	oldPointer, err := setup.importExecutable(installedPath)
	if err != nil {
		t.Fatal(err)
	}
	oldPointer.DesiredVersion = "1.0.0"
	stagedPath, err := setup.Stage(t.Context(), model.VersionPackage{
		URL: "file:///" + filepath.ToSlash(newSource), SHA256: hex.EncodeToString(newDigest[:]),
		Platform: "linux-amd64", Filename: "nre-agent", Size: int64(len(newPayload)),
	})
	if err != nil {
		t.Fatal(err)
	}
	newPointer, err := setup.readPackage(stagedPath)
	if err != nil {
		t.Fatal(err)
	}
	newPointer.DesiredVersion = "2.0.0"
	if err := setup.writePointerConvergent(previousPointerFile, oldPointer); err != nil {
		t.Fatal(err)
	}
	if err := setup.writePointerConvergent(currentPointerFile, newPointer); err != nil {
		t.Fatal(err)
	}

	activationErr := errors.New("new agent failed to start")
	retrying := NewUpdateManager(root, installedPath, nil, nil, func(string, []string, []string) error {
		return activationErr
	}, nil)
	retrying.platform = "linux-amd64"
	if err := retrying.Activate(stagedPath, "2.0.0"); !errors.Is(err, activationErr) {
		t.Fatalf("Activate() error = %v, want %v", err, activationErr)
	}
	installed, err := os.ReadFile(installedPath)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(installed, oldPayload) {
		t.Fatalf("installed executable after retry rollback = %q, want %q", installed, oldPayload)
	}
	current, err := retrying.CurrentPackage()
	if err != nil {
		t.Fatal(err)
	}
	previous, err := retrying.PreviousPackage()
	if err != nil {
		t.Fatal(err)
	}
	if current.Manifest.SHA256 != oldPointer.Manifest.SHA256 || previous.Manifest.SHA256 != newPointer.Manifest.SHA256 {
		t.Fatalf("reconciled pointers current/previous = %s/%s", current.Manifest.SHA256, previous.Manifest.SHA256)
	}
}

func TestRuntimeApplyWithDrainTimeoutOverridesManagerDefault(t *testing.T) {
	clock := &reviewDrainClock{now: time.Now().UTC()}
	drain := NewGenerationDrain(generation.NewDrainController(clock))
	manager := NewManagedGenerationManager(agentmodule.NewRegistry(), drain, 10*time.Minute)
	runtime := NewRuntimeWithGenerationManager(manager)
	first := model.Snapshot{Revision: 1, DesiredVersion: "v1"}
	second := model.Snapshot{Revision: 2, DesiredVersion: "v2"}
	if err := runtime.Apply(t.Context(), model.Snapshot{}, first); err != nil {
		t.Fatal(err)
	}
	if err := runtime.ApplyWithDrainTimeout(t.Context(), first, second, 17*time.Second); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(clock.durations, []time.Duration{17 * time.Second}) {
		t.Fatalf("scheduled drain timeouts = %v, want [17s]", clock.durations)
	}
}

func TestRevisionSyncCarriesLeaseDrainTimeoutIntoGenerationCutover(t *testing.T) {
	clock := &reviewDrainClock{now: time.Now().UTC()}
	drain := NewGenerationDrain(generation.NewDrainController(clock))
	runtime := NewRuntimeWithGenerationManager(NewManagedGenerationManager(agentmodule.NewRegistry(), drain, 10*time.Minute))
	store := &reviewJournalStore{InMemory: NewInMemory()}
	client := &reviewQueuedRevisionClient{}
	for index, drainSeconds := range []int{90, 17} {
		number := int64(index + 1)
		snapshot := model.Snapshot{
			Revision: number, DesiredVersion: []string{"v1", "v2"}[index],
			AgentConfig: model.AgentConfig{TrafficStatsInterval: "10s"},
			Rules:       []model.HTTPRule{}, L4Rules: []model.L4Rule{}, RelayListeners: []model.RelayListener{},
			WireGuardProfiles: []model.WireGuardProfile{}, EgressProfiles: []model.EgressProfile{},
			Certificates: []model.ManagedCertificateBundle{}, CertificatePolicies: []model.ManagedCertificatePolicy{},
		}
		digest, err := revisionSnapshotDigest(snapshot)
		if err != nil {
			t.Fatal(err)
		}
		lease := model.RevisionLease{
			AgentID: "edge-1", Revision: number, Attempt: 1, LeaseID: []string{"lease-1", "lease-2"}[index],
			SnapshotDigest: digest, DesiredVersion: snapshot.DesiredVersion,
			ApplyTimeoutSeconds: 60, DrainTimeoutSeconds: drainSeconds, DeadlineAt: time.Now().Add(time.Minute),
		}
		client.pulls = append(client.pulls, model.RevisionPull{
			HasUpdate: true, DesiredRevision: number, Lease: &lease, Snapshot: &snapshot, VerifiedSnapshotDigest: digest,
		})
	}
	controller := &SyncController{Store: store, Runtime: runtime, SyncClient: client}
	for range 2 {
		if err := controller.performRevisionSyncPlan(t.Context(), SyncPlan{}, client, store); err != nil {
			t.Fatal(err)
		}
	}
	if !reflect.DeepEqual(clock.durations, []time.Duration{17 * time.Second}) {
		t.Fatalf("scheduled drain timeouts = %v, want lease value [17s]", clock.durations)
	}
}

func TestRevisionSyncBoundsActivationByLeaseDeadline(t *testing.T) {
	store := &reviewJournalStore{InMemory: NewInMemory()}
	snapshot := model.Snapshot{
		Revision: 1, DesiredVersion: "v1",
		AgentConfig: model.AgentConfig{TrafficStatsInterval: "10s"},
		Rules:       []model.HTTPRule{}, L4Rules: []model.L4Rule{}, RelayListeners: []model.RelayListener{},
		WireGuardProfiles: []model.WireGuardProfile{}, EgressProfiles: []model.EgressProfile{},
		Certificates: []model.ManagedCertificateBundle{}, CertificatePolicies: []model.ManagedCertificatePolicy{},
	}
	digest, err := revisionSnapshotDigest(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().UTC().Add(time.Minute)
	lease := model.RevisionLease{
		AgentID: "edge-1", Revision: 1, Attempt: 1, LeaseID: "lease-1",
		SnapshotDigest: digest, DesiredVersion: snapshot.DesiredVersion,
		ApplyTimeoutSeconds: 60, DrainTimeoutSeconds: 30, DeadlineAt: deadline,
	}
	client := &reviewQueuedRevisionClient{pulls: []model.RevisionPull{{
		HasUpdate: true, DesiredRevision: 1, Lease: &lease, Snapshot: &snapshot, VerifiedSnapshotDigest: digest,
	}}}
	var activationDeadline time.Time
	runtime := NewRuntimeWithActivator(func(ctx context.Context, _, _ model.Snapshot) error {
		var ok bool
		activationDeadline, ok = ctx.Deadline()
		if !ok {
			return errors.New("activation context has no lease deadline")
		}
		return nil
	})
	controller := &SyncController{Store: store, Runtime: runtime, SyncClient: client}
	if err := controller.performRevisionSyncPlan(t.Context(), SyncPlan{}, client, store); err != nil {
		t.Fatal(err)
	}
	if !activationDeadline.Equal(deadline) {
		t.Fatalf("activation deadline = %s, want %s", activationDeadline, deadline)
	}
}

func TestGenerationManagerDoesNotPublishAfterContextDeadline(t *testing.T) {
	candidate := &reviewPreparedGeneration{readyDelay: 20 * time.Millisecond}
	manager := NewGenerationManager(&reviewGenerationSource{candidate: candidate})
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Millisecond)
	defer cancel()

	_, err := manager.ApplyWithDrainTimeout(ctx, model.Snapshot{}, model.Snapshot{Revision: 1}, 0)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("ApplyWithDrainTimeout() error = %v, want deadline exceeded", err)
	}
	if candidate.publishCalls != 0 || candidate.destroyCalls != 1 {
		t.Fatalf("candidate publish/destroy calls = %d/%d, want 0/1", candidate.publishCalls, candidate.destroyCalls)
	}
}

func TestGenerationManagerCloseReleasesDrainOwnedGenerations(t *testing.T) {
	controller := generation.NewDrainController(nil)
	first := &reviewGenerationResource{}
	second := &reviewGenerationResource{}
	if err := controller.Activate(t.Context(), generation.Generation{
		ID: "generation-1", Revision: 1, Resource: first,
	}, nil, time.Hour); err != nil {
		t.Fatal(err)
	}
	session := &reviewGenerationSession{}
	if _, err := controller.RegisterSession(
		"generation-1",
		generation.EntityKey{Module: "http", ID: "rule-1"},
		"session-1",
		session,
	); err != nil {
		t.Fatal(err)
	}
	if err := controller.Activate(t.Context(), generation.Generation{
		ID: "generation-2", Revision: 2, Resource: second,
	}, nil, time.Hour); err != nil {
		t.Fatal(err)
	}

	manager := &GenerationManager{
		source: &reviewGenerationSource{},
		drain:  NewGenerationDrain(controller),
	}
	if err := manager.Close(t.Context()); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if first.destroyCalls != 1 || second.destroyCalls != 1 {
		t.Fatalf("generation destroy calls = %d/%d, want 1/1", first.destroyCalls, second.destroyCalls)
	}
	if session.forceCalls != 1 || session.reason != "shutdown" {
		t.Fatalf("session force = %d %q, want 1 shutdown", session.forceCalls, session.reason)
	}
}

func TestCompletedRuntimeDrainWaitsForCleanup(t *testing.T) {
	active := model.GenerationRecord{Revision: 2, RuntimeGenerationID: "runtime-2"}
	predecessor := model.GenerationRecord{Revision: 1, RuntimeGenerationID: "runtime-1"}
	snapshot := model.GenerationDrainSnapshot{Generations: []model.GenerationDrainStatus{{
		GenerationID: "runtime-1", Revision: 1, State: model.GenerationDrainStateDrained,
	}}}
	if _, ok := completedRuntimeDrain(snapshot, true, predecessor, active); ok {
		t.Fatal("drain reported before runtime cleanup completed")
	}
	snapshot.Generations[0].CompletedAt = time.Now().UTC()
	if _, ok := completedRuntimeDrain(snapshot, true, predecessor, active); !ok {
		t.Fatal("terminal runtime drain was not reportable")
	}
	snapshot = model.GenerationDrainSnapshot{Generations: []model.GenerationDrainStatus{{
		GenerationID: "runtime-2", Revision: 2, State: model.GenerationDrainStateApplied,
	}}}
	if _, ok := completedRuntimeDrain(snapshot, true, predecessor, active); ok {
		t.Fatal("absent predecessor was reported while a hot-restart supervisor may still own its sessions")
	}
}

func TestCompletedDrainUsesActiveAuthoritativeLease(t *testing.T) {
	store := &reviewJournalStore{InMemory: NewInMemory()}
	client := &reviewRevisionClient{}
	journal := model.GenerationJournal{
		Version: 1,
		Active: &model.GenerationRecord{
			GenerationID: "protocol-2", Revision: 2, Phase: model.GenerationPhaseActive, Acknowledged: true,
			Lease: model.RevisionLease{AgentID: "edge-1", Revision: 2, RetryCycle: 3, Attempt: 4, LeaseID: "lease-2"},
		},
		Draining: []model.GenerationRecord{{GenerationID: "protocol-1", Revision: 1, Phase: model.GenerationPhaseActive}},
	}
	controller := &SyncController{Runtime: NewRuntime()}
	if err := controller.reportCompletedGenerationDrains(t.Context(), client, store, &journal); err != nil {
		t.Fatal(err)
	}
	if len(client.reports) != 1 {
		t.Fatalf("reports = %+v", client.reports)
	}
	report := client.reports[0]
	if report.Revision != 2 || report.RetryCycle != 3 || report.Attempt != 4 || report.LeaseID != "lease-2" ||
		report.GenerationID != "protocol-1" || report.Status != model.GenerationDrainStateDrained {
		t.Fatalf("drain report = %+v", report)
	}
	if len(store.journal.Draining) != 0 {
		t.Fatalf("persisted draining journal = %+v", store.journal.Draining)
	}
}

func TestRevisionSyncRecoversLostAppliedAcknowledgementBeforeDrain(t *testing.T) {
	store := &reviewJournalStore{InMemory: NewInMemory(), journal: model.GenerationJournal{
		Version: 1,
		Active: &model.GenerationRecord{
			GenerationID: "protocol-2", Revision: 2, Phase: model.GenerationPhaseActive,
			Lease: model.RevisionLease{AgentID: "edge-1", Revision: 2, Attempt: 1, LeaseID: "lease-2"},
		},
		Draining: []model.GenerationRecord{{
			GenerationID: "protocol-1", Revision: 1, Phase: model.GenerationPhaseActive,
		}},
	}}
	client := &reviewRevisionClient{}
	controller := &SyncController{Store: store, Runtime: NewRuntime(), SyncClient: client}

	if err := controller.performRevisionSyncPlan(t.Context(), SyncPlan{}, client, store); err != nil {
		t.Fatal(err)
	}
	if !store.journal.Active.Acknowledged || len(store.journal.Draining) != 0 {
		t.Fatalf("recovered journal = %+v", store.journal)
	}
	if len(client.reports) != 2 || client.reports[0].Status != "applied" ||
		client.reports[1].Status != model.GenerationDrainStateDrained {
		t.Fatalf("replayed reports = %+v", client.reports)
	}
}

type reviewJournalStore struct {
	*InMemory
	journal model.GenerationJournal
	lkg     model.Snapshot
}

func (s *reviewJournalStore) SaveGenerationJournal(journal model.GenerationJournal) error {
	s.journal = journal
	return nil
}

func (s *reviewJournalStore) LoadGenerationJournal() (model.GenerationJournal, error) {
	return s.journal, nil
}

func (s *reviewJournalStore) SaveLastKnownGoodSnapshot(snapshot model.Snapshot) error {
	s.lkg = snapshot
	return nil
}

func (s *reviewJournalStore) LoadLastKnownGoodSnapshot() (model.Snapshot, error) {
	return s.lkg, nil
}

type reviewRevisionClient struct {
	heartbeat model.Snapshot
	reports   []model.RevisionReport
}

type reviewQueuedRevisionClient struct {
	pulls   []model.RevisionPull
	reports []model.RevisionReport
}

func (*reviewQueuedRevisionClient) Sync(context.Context, control.SyncRequest) (model.Snapshot, error) {
	return model.Snapshot{}, nil
}

func (c *reviewQueuedRevisionClient) PullRevision(context.Context) (model.RevisionPull, error) {
	pull := c.pulls[0]
	c.pulls = c.pulls[1:]
	return pull, nil
}

func (*reviewQueuedRevisionClient) StartRevision(context.Context, model.RevisionStart) error {
	return nil
}
func (c *reviewQueuedRevisionClient) ReportRevision(_ context.Context, report model.RevisionReport) error {
	c.reports = append(c.reports, report)
	return nil
}

func (c *reviewRevisionClient) Sync(context.Context, control.SyncRequest) (model.Snapshot, error) {
	return c.heartbeat, nil
}

func (*reviewRevisionClient) PullRevision(context.Context) (model.RevisionPull, error) {
	return model.RevisionPull{}, nil
}

func (*reviewRevisionClient) StartRevision(context.Context, model.RevisionStart) error { return nil }
func (c *reviewRevisionClient) ReportRevision(_ context.Context, report model.RevisionReport) error {
	c.reports = append(c.reports, report)
	return nil
}

type reviewUpdater struct {
	preflightCalls int
	stageCalls     int
	activateCalls  int
	preflightErr   error
}

func (u *reviewUpdater) Preflight(model.VersionPackage) error {
	u.preflightCalls++
	return u.preflightErr
}

func (u *reviewUpdater) Stage(context.Context, model.VersionPackage) (string, error) {
	u.stageCalls++
	return "staged", nil
}

func (u *reviewUpdater) Activate(string, string) error {
	u.activateCalls++
	return ErrRestartRequested
}

type reviewDrainClock struct {
	now       time.Time
	durations []time.Duration
}

func (c *reviewDrainClock) Now() time.Time { return c.now }
func (c *reviewDrainClock) AfterFunc(duration time.Duration, _ func()) generation.Timer {
	c.durations = append(c.durations, duration)
	return reviewDrainTimer{}
}

type reviewDrainTimer struct{}

func (reviewDrainTimer) Stop() bool { return true }

type reviewGenerationSource struct {
	candidate *reviewPreparedGeneration
}

func (s *reviewGenerationSource) PrepareGeneration(context.Context, agentmodule.GenerationContext) (agentmodule.PreparedGeneration, error) {
	return s.candidate, nil
}

func (*reviewGenerationSource) ActiveGeneration() *agentmodule.GenerationView { return nil }

type reviewPreparedGeneration struct {
	readyDelay   time.Duration
	publishCalls int
	destroyCalls int
}

type reviewGenerationResource struct {
	destroyCalls int
}

func (r *reviewGenerationResource) Destroy(context.Context) error {
	r.destroyCalls++
	return nil
}

type reviewGenerationSession struct {
	forceCalls int
	reason     string
}

func (s *reviewGenerationSession) ForceClose(_ context.Context, reason string) error {
	s.forceCalls++
	s.reason = reason
	return nil
}

func (*reviewPreparedGeneration) Context() agentmodule.GenerationContext {
	return agentmodule.GenerationContext{}
}
func (c *reviewPreparedGeneration) Ready(context.Context) error {
	time.Sleep(c.readyDelay)
	return nil
}
func (c *reviewPreparedGeneration) Publish() (*agentmodule.GenerationView, *agentmodule.GenerationView) {
	c.publishCalls++
	return nil, nil
}
func (c *reviewPreparedGeneration) Destroy(context.Context) error {
	c.destroyCalls++
	return nil
}
