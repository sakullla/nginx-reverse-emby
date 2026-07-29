package app

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/core"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/generation"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/hotrestart"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	agentmodule "github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
)

func TestHotRestartAbortResumesParentWhenChildWaitFails(t *testing.T) {
	activateErr := errors.New("child activation failed")
	waitErr := errors.New("child exited")
	process := &lifecycleHotRestartProcess{activateErr: activateErr, abortErr: waitErr}
	streams := &lifecycleStreamAuthority{}
	packets := &lifecyclePacketAuthority{}
	wrapper := &hotRestartResourceProcess{
		hotRestartProcess: process,
		streams:           streams,
		packets:           packets,
	}

	err := wrapper.Activate(t.Context())
	if !errors.Is(err, activateErr) || !errors.Is(err, waitErr) {
		t.Fatalf("Activate() error = %v, want activation and child wait errors", err)
	}
	if streams.resumeCalls != 1 || packets.resumeCalls != 1 {
		t.Fatalf("parent resume calls = streams:%d packets:%d, want 1/1", streams.resumeCalls, packets.resumeCalls)
	}
	_ = wrapper.Abort()
	if streams.resumeCalls != 1 || packets.resumeCalls != 1 {
		t.Fatalf("replayed abort resumed parent more than once: streams:%d packets:%d", streams.resumeCalls, packets.resumeCalls)
	}
}

func TestPackageOnlyHotRestartSynthesizesRevisionZeroIdentity(t *testing.T) {
	store, err := core.NewFilesystem(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	runtime := core.NewRuntimeWithGenerationManager(core.NewManagedGenerationManager(
		agentmodule.NewRegistry(), core.NewGenerationDrain(nil), time.Minute,
	))
	desired := Snapshot{}
	if err := runtime.Apply(t.Context(), Snapshot{}, desired); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveDesiredSnapshot(desired); err != nil {
		t.Fatal(err)
	}
	active, managed := runtime.ActiveGenerationIdentity()
	if !managed || active.ID == "" || active.Revision != 0 {
		t.Fatalf("bootstrap runtime identity = %+v, managed=%t", active, managed)
	}

	app := &App{store: store, runtime: runtime}
	identity, _, err := app.hotRestartLaunchState()
	if err != nil {
		t.Fatalf("hotRestartLaunchState() error = %v", err)
	}
	if identity.Revision != 0 || identity.GenerationID != active.ID || identity.SnapshotDigest != active.SnapshotHash || identity.LeaseID == "" {
		t.Fatalf("bootstrap hot restart identity = %+v, active=%+v", identity, active)
	}
	identity.LaunchEpoch = "bootstrap-epoch"
	if err := app.validateHotRestartIdentity(identity, desired); err != nil {
		t.Fatalf("validateHotRestartIdentity(revision zero) error = %v", err)
	}
	if err := app.validateActiveHotRestartRuntime(identity); err != nil {
		t.Fatalf("validateActiveHotRestartRuntime(revision zero) error = %v", err)
	}
}

func TestPackageOnlyUpgradeFallsBackToColdExecForLegacyGeneration(t *testing.T) {
	store, err := core.NewFilesystem(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	desired := Snapshot{Revision: 389}
	if err := store.SaveDesiredSnapshot(desired); err != nil {
		t.Fatal(err)
	}

	var coldRestartCalls int
	app := &App{store: store}
	app.hotRestartStart = func(context.Context, hotrestart.Launch) (hotRestartProcess, error) {
		t.Fatal("legacy package upgrade attempted a hot restart")
		return nil, nil
	}
	app.coldRestart = func(binary string, argv, env []string) error {
		coldRestartCalls++
		if binary != "/updates/new/nre-agent" || len(argv) != 1 || argv[0] != binary || len(env) != 1 || env[0] != "NRE_AGENT_VERSION=2" {
			t.Fatalf("cold restart inputs = %q %v %v", binary, argv, env)
		}
		return nil
	}

	err = app.hotRestartReplacement(
		t.Context(),
		"/updates/new/nre-agent",
		[]string{"/updates/new/nre-agent"},
		[]string{"NRE_AGENT_VERSION=2"},
	)
	if !errors.Is(err, core.ErrRestartRequested) {
		t.Fatalf("hotRestartReplacement() error = %v, want restart requested", err)
	}
	if coldRestartCalls != 1 {
		t.Fatalf("cold restart calls = %d, want 1", coldRestartCalls)
	}
}

func TestPackageOnlyUpgradeDoesNotColdExecNonemptyUnreadyJournal(t *testing.T) {
	store, err := core.NewFilesystem(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	desired := Snapshot{Revision: 389}
	if err := store.SaveDesiredSnapshot(desired); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveGenerationJournal(model.GenerationJournal{
		Version: 1,
		Candidate: &model.GenerationRecord{
			Revision: 389,
			Phase:    model.GenerationPhasePrepared,
		},
	}); err != nil {
		t.Fatal(err)
	}

	app := &App{store: store}
	app.hotRestartStart = func(context.Context, hotrestart.Launch) (hotRestartProcess, error) {
		t.Fatal("nonempty unready journal attempted a hot restart")
		return nil, nil
	}
	app.coldRestart = func(string, []string, []string) error {
		t.Fatal("nonempty unready journal triggered a cold restart")
		return nil
	}
	err = app.hotRestartReplacement(t.Context(), "/updates/new/nre-agent", nil, nil)
	if err == nil || errors.Is(err, core.ErrRestartRequested) || !strings.Contains(err.Error(), "durable generation is not ready") {
		t.Fatalf("hotRestartReplacement() error = %v, want readiness rejection", err)
	}
}

func TestPackageOnlyHotRestartUsesDurableActiveGeneration(t *testing.T) {
	store, err := core.NewFilesystem(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	desired := Snapshot{Revision: 12, DesiredVersion: "1.0.0"}
	runtimeDigest, err := hotRestartSnapshotDigest(desired)
	if err != nil {
		t.Fatal(err)
	}
	canonicalDigest := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if err := store.SaveDesiredSnapshot(desired); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveGenerationJournal(model.GenerationJournal{Version: 1, Active: &model.GenerationRecord{
		GenerationID: "protocol-12", RuntimeGenerationID: "runtime-12", RuntimeSnapshotHash: runtimeDigest, Revision: 12,
		SnapshotDigest: canonicalDigest, Phase: model.GenerationPhaseActive, Acknowledged: true,
		Lease: model.RevisionLease{Revision: 12, LeaseID: "lease-12", DrainTimeoutSeconds: 23},
	}}); err != nil {
		t.Fatal(err)
	}

	process := &lifecycleHotRestartProcess{}
	app := &App{cfg: Config{DataDir: t.TempDir()}, store: store, runCtx: t.Context(), hotRestartChild: true}
	app.hotRestartStart = func(_ context.Context, launch hotrestart.Launch) (hotRestartProcess, error) {
		if launch.Identity.Revision != 12 || launch.Identity.GenerationID != "runtime-12" || launch.Identity.LeaseID != "lease-12" {
			t.Fatalf("package-only launch identity = %+v", launch.Identity)
		}
		if launch.Identity.SnapshotDigest != canonicalDigest {
			t.Fatalf("package-only canonical digest = %q", launch.Identity.SnapshotDigest)
		}
		if launch.Stdout != os.Stdout || launch.Stderr != os.Stderr {
			t.Fatal("hot restart child did not inherit stdout/stderr")
		}
		return process, nil
	}
	app.hotRestartDrain = func(context.Context, hotrestart.Identity) error {
		if process.transferCalls != 1 {
			t.Fatalf("authority transfer calls before drain = %d, want 1", process.transferCalls)
		}
		if app.hotRestartDrainTimeout != 23*time.Second {
			t.Fatalf("hot restart drain timeout = %s, want 23s", app.hotRestartDrainTimeout)
		}
		return nil
	}

	err = app.hotRestartReplacement(t.Context(), "/updates/new/nre-agent", nil, nil)
	if !errors.Is(err, core.ErrRestartRequested) {
		t.Fatalf("hotRestartReplacement() error = %v, want restart requested", err)
	}
	if process.waitCalls != 0 {
		t.Fatalf("retired parent waited for authoritative child %d time(s)", process.waitCalls)
	}
	if process.abortCalls != 0 {
		t.Fatalf("authoritative child abort calls = %d, want 0", process.abortCalls)
	}
	identity := hotrestart.Identity{
		Revision: 12, SnapshotDigest: canonicalDigest, GenerationID: "runtime-12", LeaseID: "lease-12", LaunchEpoch: "epoch-12",
	}
	if err := app.validateHotRestartIdentity(identity, desired); err != nil {
		t.Fatalf("validateHotRestartIdentity(active) error = %v", err)
	}
}

func TestHotRestartDrainFailureRetainsAuthoritativeChild(t *testing.T) {
	store, err := core.NewFilesystem(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	desired := Snapshot{Revision: 7, DesiredVersion: "2.0.0"}
	runtimeDigest, err := hotRestartSnapshotDigest(desired)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveDesiredSnapshot(desired); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveGenerationJournal(model.GenerationJournal{Version: 1, Active: &model.GenerationRecord{
		GenerationID: "protocol-7", RuntimeGenerationID: "runtime-7", RuntimeSnapshotHash: runtimeDigest,
		Revision: 7, SnapshotDigest: "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
		Phase: model.GenerationPhaseActive, Acknowledged: true,
		Lease: model.RevisionLease{Revision: 7, LeaseID: "lease-7", DrainTimeoutSeconds: 10},
	}}); err != nil {
		t.Fatal(err)
	}

	process := &lifecycleHotRestartProcess{}
	app := &App{cfg: Config{DataDir: t.TempDir()}, store: store, runCtx: t.Context(), hotRestartChild: true}
	app.hotRestartStart = func(context.Context, hotrestart.Launch) (hotRestartProcess, error) { return process, nil }
	app.hotRestartDrain = func(context.Context, hotrestart.Identity) error {
		return errors.New("retired generation cleanup failed")
	}

	if err := app.hotRestartReplacement(t.Context(), "/updates/new/nre-agent", nil, nil); !errors.Is(err, core.ErrRestartRequested) {
		t.Fatalf("hotRestartReplacement() error = %v", err)
	}
	if process.transferCalls != 1 || process.abortCalls != 0 {
		t.Fatalf("authority transfer/abort calls = %d/%d, want 1/0", process.transferCalls, process.abortCalls)
	}
}

func TestHotRestartDrainTimeoutForcesRetiredParent(t *testing.T) {
	clock := newLifecycleGenerationClock(time.Unix(100, 0))
	controller := generation.NewDrainController(clock)
	resource := &lifecycleGenerationResource{}
	if err := controller.Activate(t.Context(), generation.Generation{
		ID: "generation-old", Revision: 1, Resource: resource,
	}, nil, time.Minute); err != nil {
		t.Fatal(err)
	}
	session := &lifecycleGenerationSession{}
	if _, err := controller.RegisterSession(
		"generation-old", generation.EntityKey{Module: "http", ID: "1"}, "session-1", session,
	); err != nil {
		t.Fatal(err)
	}
	manager := core.NewManagedGenerationManager(nil, core.NewGenerationDrain(controller), time.Minute)
	app := &App{generations: manager, hotRestartDrainTimeout: time.Minute}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()

	drainDone := make(chan error, 1)
	go func() {
		drainDone <- app.drainHotRestartParent(ctx, hotrestart.Identity{})
	}()
	select {
	case <-clock.scheduled:
	case err := <-drainDone:
		t.Fatalf("drainHotRestartParent() returned before scheduling timeout: %v", err)
	case <-ctx.Done():
		t.Fatalf("hot restart drain timeout was not scheduled: %v", ctx.Err())
	}
	clock.Advance(time.Minute)
	if err := <-drainDone; err != nil {
		t.Fatalf("drainHotRestartParent() error = %v", err)
	}
	var status model.GenerationDrainStatus
	for _, candidate := range controller.Snapshot().Generations {
		if candidate.GenerationID == "generation-old" {
			status = candidate
		}
	}
	if status.State != model.GenerationDrainStateForced || status.ForceReason != model.GenerationForceReasonTimeout {
		t.Fatalf("retired parent status = %+v", status)
	}
	if session.forceCalls != 1 || resource.destroyCalls != 1 {
		t.Fatalf("retired parent force/destroy calls = %d/%d, want 1/1", session.forceCalls, resource.destroyCalls)
	}
}

func TestServiceMainProcessSupervisesAuthoritativeHotRestartChild(t *testing.T) {
	store, err := core.NewFilesystem(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	desired := Snapshot{Revision: 4, DesiredVersion: "2.0.0"}
	runtimeDigest, err := hotRestartSnapshotDigest(desired)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveDesiredSnapshot(desired); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveGenerationJournal(model.GenerationJournal{Version: 1, Active: &model.GenerationRecord{
		GenerationID: "protocol-4", RuntimeGenerationID: "runtime-4", RuntimeSnapshotHash: runtimeDigest,
		Revision: 4, SnapshotDigest: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		Phase: model.GenerationPhaseActive, Acknowledged: true,
		Lease: model.RevisionLease{Revision: 4, LeaseID: "lease-4", DrainTimeoutSeconds: 10},
	}}); err != nil {
		t.Fatal(err)
	}

	process := &lifecycleHotRestartProcess{}
	app := &App{cfg: Config{DataDir: t.TempDir()}, store: store, runCtx: t.Context()}
	app.hotRestartStart = func(context.Context, hotrestart.Launch) (hotRestartProcess, error) { return process, nil }
	app.hotRestartSupervise = func(_ context.Context, got hotRestartProcess, journalPath string, identity hotrestart.Identity) error {
		if got != process || journalPath == "" || identity.GenerationID != "runtime-4" {
			t.Fatalf("supervisor inputs = %T %q %+v", got, journalPath, identity)
		}
		process.waitCalls++
		return context.Canceled
	}

	if err := app.hotRestartReplacement(t.Context(), "/updates/new/nre-agent", nil, nil); !errors.Is(err, core.ErrRestartRequested) {
		t.Fatalf("hotRestartReplacement() error = %v", err)
	}
	if process.waitCalls != 1 {
		t.Fatalf("service supervisor calls = %d, want 1", process.waitCalls)
	}
}

func TestHotRestartShutdownFollowsAuthorityTransfers(t *testing.T) {
	identity := hotrestart.Identity{
		Revision:       4,
		SnapshotDigest: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		GenerationID:   "runtime-4",
		LeaseID:        "lease-4",
		LaunchEpoch:    "epoch-4",
	}
	journal := &lifecycleAuthorityJournal{
		identity: identity,
		owner:    hotrestart.AuthorityOwnerChild,
		pid:      101,
	}
	var stopped []int
	err := stopHotRestartAuthorityLineage(journal, 1, func(pid int) bool {
		return journal.owner != hotrestart.AuthorityOwnerNone && pid == journal.pid
	}, func(pid int) error {
		stopped = append(stopped, pid)
		switch pid {
		case 101:
			journal.pid = 202
		case 202:
			journal.owner = hotrestart.AuthorityOwnerNone
			journal.pid = 0
		}
		return nil
	})
	if err != nil {
		t.Fatalf("stopHotRestartAuthorityLineage() error = %v", err)
	}
	if len(stopped) != 2 || stopped[0] != 101 || stopped[1] != 202 {
		t.Fatalf("stopped authority pids = %v, want [101 202]", stopped)
	}
}

type lifecycleHotRestartProcess struct {
	waitCalls     int
	transferCalls int
	abortCalls    int
	activateErr   error
	abortErr      error
}

func (p *lifecycleHotRestartProcess) Activate(context.Context) error { return p.activateErr }
func (p *lifecycleHotRestartProcess) TransferAuthority(context.Context) error {
	p.transferCalls++
	return nil
}
func (p *lifecycleHotRestartProcess) Wait() error          { p.waitCalls++; return nil }
func (*lifecycleHotRestartProcess) Signal(os.Signal) error { return nil }
func (p *lifecycleHotRestartProcess) Abort() error         { p.abortCalls++; return p.abortErr }

type lifecycleStreamAuthority struct {
	pauseCalls  int
	resumeCalls int
}

func (a *lifecycleStreamAuthority) Pause() error  { a.pauseCalls++; return nil }
func (a *lifecycleStreamAuthority) Resume() error { a.resumeCalls++; return nil }

type lifecyclePacketAuthority struct {
	resumeCalls int
}

func (*lifecyclePacketAuthority) BeginForwarding() error    { return nil }
func (*lifecyclePacketAuthority) Pause() error              { return nil }
func (*lifecyclePacketAuthority) FlushForwarding() error    { return nil }
func (a *lifecyclePacketAuthority) Resume() error           { a.resumeCalls++; return nil }
func (*lifecyclePacketAuthority) FinalizeForwarding() error { return nil }

type lifecycleGenerationResource struct{ destroyCalls int }

func (r *lifecycleGenerationResource) Destroy(context.Context) error {
	r.destroyCalls++
	return nil
}

type lifecycleGenerationSession struct{ forceCalls int }

func (s *lifecycleGenerationSession) ForceClose(context.Context, string) error {
	s.forceCalls++
	return nil
}

type lifecycleGenerationClock struct {
	mu        sync.Mutex
	now       time.Time
	scheduled chan struct{}
	timers    []*lifecycleGenerationTimer
}

func newLifecycleGenerationClock(now time.Time) *lifecycleGenerationClock {
	return &lifecycleGenerationClock{
		now:       now,
		scheduled: make(chan struct{}, 1),
	}
}

func (c *lifecycleGenerationClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *lifecycleGenerationClock) AfterFunc(delay time.Duration, fn func()) generation.Timer {
	c.mu.Lock()
	timer := &lifecycleGenerationTimer{clock: c, at: c.now.Add(delay), fn: fn}
	c.timers = append(c.timers, timer)
	c.mu.Unlock()
	select {
	case c.scheduled <- struct{}{}:
	default:
	}
	return timer
}

func (c *lifecycleGenerationClock) Advance(elapsed time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(elapsed)
	var callbacks []func()
	for _, timer := range c.timers {
		if timer.stopped || timer.fired || timer.at.After(c.now) {
			continue
		}
		timer.fired = true
		callbacks = append(callbacks, timer.fn)
	}
	c.mu.Unlock()
	for _, callback := range callbacks {
		callback()
	}
}

type lifecycleGenerationTimer struct {
	clock   *lifecycleGenerationClock
	at      time.Time
	fn      func()
	stopped bool
	fired   bool
}

func (t *lifecycleGenerationTimer) Stop() bool {
	t.clock.mu.Lock()
	defer t.clock.mu.Unlock()
	if t.stopped || t.fired {
		return false
	}
	t.stopped = true
	return true
}

type lifecycleAuthorityJournal struct {
	identity hotrestart.Identity
	owner    string
	pid      int
}

func (j *lifecycleAuthorityJournal) Load() (hotrestart.AuthorityRecord, error) {
	return j.record(), nil
}

func (j *lifecycleAuthorityJournal) Recover(hotrestart.Identity, func(int) bool) (string, hotrestart.AuthorityRecord, error) {
	return j.owner, j.record(), nil
}

func (j *lifecycleAuthorityJournal) record() hotrestart.AuthorityRecord {
	record := hotrestart.AuthorityRecord{Identity: j.identity}
	if j.owner == hotrestart.AuthorityOwnerParent {
		record.ParentPID = j.pid
	} else if j.owner == hotrestart.AuthorityOwnerChild {
		record.ChildPID = j.pid
	}
	return record
}
