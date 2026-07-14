package hotrestart

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/platform"
)

func TestSupervisorReadinessActivationAndAuthorityOrdering(t *testing.T) {
	requireProcessHandoff(t)
	process, err := (Supervisor{ReadyTimeout: 5 * time.Second, CommandTimeout: 5 * time.Second}).Start(t.Context(), helperLaunch(t, "ready"))
	if err != nil {
		t.Fatal(err)
	}
	if err := process.Activate(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := process.Activate(t.Context()); err != nil {
		t.Fatalf("idempotent Activate() error = %v", err)
	}
	if err := process.TransferAuthority(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := process.TransferAuthority(t.Context()); err != nil {
		t.Fatalf("idempotent TransferAuthority() error = %v", err)
	}
	if err := process.Wait(); err != nil {
		t.Fatalf("child Wait() error = %v", err)
	}
}

func TestSupervisorRejectsChildCrashBeforeReadiness(t *testing.T) {
	requireProcessHandoff(t)
	var stderr bytes.Buffer
	launch := helperLaunch(t, "crash")
	launch.Stderr = &stderr
	_, err := (Supervisor{ReadyTimeout: 5 * time.Second}).Start(t.Context(), launch)
	if err == nil || !strings.Contains(err.Error(), "readiness") {
		t.Fatalf("Start() error = %v, want readiness failure", err)
	}
}

func TestSupervisorKillsChildOnReadinessTimeout(t *testing.T) {
	requireProcessHandoff(t)
	started := time.Now()
	_, err := (Supervisor{ReadyTimeout: 100 * time.Millisecond}).Start(t.Context(), helperLaunch(t, "hang"))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Start() error = %v, want deadline exceeded", err)
	}
	if time.Since(started) > 5*time.Second {
		t.Fatalf("timeout cleanup took %s", time.Since(started))
	}
}

func TestSupervisorGeneratesUniqueEpochAndRejectsOverlappingLaunch(t *testing.T) {
	requireProcessHandoff(t)
	launch := helperLaunch(t, "hang")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	started := make(chan error, 1)
	go func() {
		_, err := (Supervisor{ReadyTimeout: 10 * time.Second}).Start(ctx, launch)
		started <- err
	}()

	journal := NewFileAuthorityJournal(launch.AuthorityJournal)
	var first AuthorityRecord
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		record, err := journal.Load()
		if err == nil && record.LaunchPending && record.ChildPID > 0 {
			first = record
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if first.Identity.LaunchEpoch == "" || first.Identity.LaunchEpoch == launch.Identity.LaunchEpoch {
		t.Fatalf("generated launch identity = %+v", first.Identity)
	}
	if _, err := (Supervisor{ReadyTimeout: time.Second}).Start(t.Context(), launch); err == nil {
		t.Fatal("overlapping launch succeeded before child readiness")
	}
	cancel()
	select {
	case err := <-started:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("first Start() error = %v, want context cancellation", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled launch did not stop")
	}

	readyLaunch := launch
	readyLaunch.Env = setEnv(readyLaunch.Env, "NRE_HOT_RESTART_TEST_MODE", "parent_loss_child")
	process, err := (Supervisor{ReadyTimeout: 5 * time.Second}).Start(t.Context(), readyLaunch)
	if err != nil {
		t.Fatal(err)
	}
	if process.identity.LaunchEpoch == first.Identity.LaunchEpoch {
		t.Fatal("retry reused the previous launch epoch")
	}
	_ = process.Abort()
}

func TestConcurrentTransitionsShareOneDurableResult(t *testing.T) {
	requireProcessHandoff(t)
	launch := helperLaunch(t, "ready")
	process, err := (Supervisor{ReadyTimeout: 5 * time.Second, CommandTimeout: 5 * time.Second}).Start(t.Context(), launch)
	if err != nil {
		t.Fatal(err)
	}
	for _, transition := range []func(context.Context) error{process.Activate, process.TransferAuthority} {
		var wg sync.WaitGroup
		errs := make(chan error, 8)
		for range 8 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				errs <- transition(t.Context())
			}()
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatalf("concurrent transition error = %v", err)
			}
		}
	}
	if err := process.Wait(); err != nil {
		t.Fatal(err)
	}
	record, err := NewFileAuthorityJournal(launch.AuthorityJournal).Load()
	if err != nil {
		t.Fatal(err)
	}
	if record.Phase != AuthorityPhaseChild {
		t.Fatalf("authority phase = %q, want %q", record.Phase, AuthorityPhaseChild)
	}
}

func TestPostReadinessFailuresAbortChildAndRecoverParentAuthority(t *testing.T) {
	requireProcessHandoff(t)
	for _, tc := range []struct {
		name       string
		mode       string
		transition func(*ChildProcess, context.Context) error
	}{
		{name: "child crash", mode: "post_ready_crash", transition: (*ChildProcess).Activate},
		{name: "activation timeout", mode: "activation_hang", transition: (*ChildProcess).Activate},
		{name: "authority timeout", mode: "authority_hang", transition: func(process *ChildProcess, ctx context.Context) error {
			if err := process.Activate(ctx); err != nil {
				return err
			}
			return process.TransferAuthority(ctx)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			launch := helperLaunch(t, tc.mode)
			process, err := (Supervisor{ReadyTimeout: 5 * time.Second, CommandTimeout: 100 * time.Millisecond}).Start(t.Context(), launch)
			if err != nil {
				t.Fatal(err)
			}
			if err := tc.transition(process, t.Context()); err == nil {
				t.Fatal("transition succeeded, want post-readiness failure")
			}
			record, err := NewFileAuthorityJournal(launch.AuthorityJournal).Load()
			if err != nil {
				t.Fatal(err)
			}
			if record.Phase != AuthorityPhaseParent || record.ChildPID != 0 {
				t.Fatalf("recovered authority = %+v, want parent with no child", record)
			}
		})
	}
}

func TestLostAcknowledgementsConvergeFromDurablePhases(t *testing.T) {
	requireProcessHandoff(t)
	launch := helperLaunch(t, "acks_lost")
	process, err := (Supervisor{ReadyTimeout: 5 * time.Second, CommandTimeout: 100 * time.Millisecond}).Start(t.Context(), launch)
	if err != nil {
		t.Fatal(err)
	}
	if err := process.Activate(t.Context()); err != nil {
		t.Fatalf("Activate() did not reconcile durable phase: %v", err)
	}
	if err := process.TransferAuthority(t.Context()); err != nil {
		t.Fatalf("TransferAuthority() did not reconcile durable phase: %v", err)
	}
	if err := process.Wait(); err != nil {
		t.Fatal(err)
	}
}

func TestAuthorityJournalRecoveryConvergesOnOneLiveOwner(t *testing.T) {
	journal := NewFileAuthorityJournal(t.TempDir() + string(os.PathSeparator) + "authority.json")
	identity := testIdentity()
	if err := journal.Begin(identity, 100); err != nil {
		t.Fatal(err)
	}
	if err := journal.Advance(identity, 200, AuthorityPhaseReady); err != nil {
		t.Fatal(err)
	}
	if err := journal.Advance(identity, 200, AuthorityPhaseActive); err != nil {
		t.Fatal(err)
	}
	owner, record, err := journal.Recover(identity, func(pid int) bool { return pid == 100 || pid == 200 })
	if err != nil || owner != AuthorityOwnerParent || record.Phase != AuthorityPhaseActive {
		t.Fatalf("live parent/child recovery = %q, %v", owner, err)
	}
	owner, record, err = journal.Recover(identity, func(pid int) bool { return pid == 100 })
	if err != nil || owner != AuthorityOwnerParent || record.Phase != AuthorityPhaseParent || record.ChildPID != 0 {
		t.Fatalf("dead child recovery = %q, %+v, %v", owner, record, err)
	}
	journal = NewFileAuthorityJournal(filepath.Join(t.TempDir(), "authority.json"))
	if err := journal.Begin(identity, 100); err != nil {
		t.Fatal(err)
	}
	if err := journal.Advance(identity, 200, AuthorityPhaseReady); err != nil {
		t.Fatal(err)
	}
	owner, record, err = journal.Recover(identity, func(pid int) bool { return pid == 200 })
	if err != nil || owner != AuthorityOwnerChild || record.Phase != AuthorityPhaseReady {
		t.Fatalf("dead parent recovery = %q, %+v, %v", owner, record, err)
	}
}

func TestAbortWithLiveParentDoesNotTriggerChildRecovery(t *testing.T) {
	requireProcessHandoff(t)
	for _, checkpoint := range []string{"ready", "active"} {
		t.Run(checkpoint, func(t *testing.T) {
			resultPath := filepath.Join(t.TempDir(), "recovery.log")
			launch := helperLaunch(t, "parent_loss_child")
			launch.Env = setEnv(launch.Env, "NRE_HOT_RESTART_RECOVERY_RESULT", resultPath)
			process, err := (Supervisor{ReadyTimeout: 5 * time.Second, CommandTimeout: 5 * time.Second}).Start(t.Context(), launch)
			if err != nil {
				t.Fatal(err)
			}
			if checkpoint == "active" {
				if err := process.Activate(t.Context()); err != nil {
					t.Fatal(err)
				}
			}
			_ = process.Abort()

			payload, err := os.ReadFile(resultPath)
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				t.Fatal(err)
			}
			want := ""
			if checkpoint == "active" {
				want = "activate\n"
			}
			if string(payload) != want {
				t.Fatalf("abort callbacks = %q, want %q", payload, want)
			}
			record, err := NewFileAuthorityJournal(launch.AuthorityJournal).Load()
			if err != nil || record.Phase != AuthorityPhaseParent || record.ChildPID != 0 {
				t.Fatalf("abort authority = %+v, %v", record, err)
			}
		})
	}
}

func TestChildRecoversActivationAndAuthorityAfterRealParentExit(t *testing.T) {
	requireProcessHandoff(t)
	for _, checkpoint := range []string{"ready", "active"} {
		t.Run(checkpoint, func(t *testing.T) {
			dir := t.TempDir()
			resultPath := filepath.Join(dir, "recovery.log")
			journalPath := filepath.Join(dir, "authority.json")
			cmd := exec.Command(os.Args[0], "-test.run=^TestHotRestartHelperProcess$")
			cmd.Env = setEnv(os.Environ(), "NRE_HOT_RESTART_PARENT_MODE", checkpoint)
			cmd.Env = setEnv(cmd.Env, "NRE_HOT_RESTART_RECOVERY_RESULT", resultPath)
			cmd.Env = setEnv(cmd.Env, "NRE_HOT_RESTART_RECOVERY_JOURNAL", journalPath)
			if err := cmd.Run(); err != nil {
				t.Fatalf("parent helper error = %v", err)
			}
			deadline := time.Now().Add(10 * time.Second)
			var result string
			for time.Now().Before(deadline) {
				payload, err := os.ReadFile(resultPath)
				if err == nil {
					result = string(payload)
					if strings.Contains(result, "done\n") {
						break
					}
				}
				time.Sleep(20 * time.Millisecond)
			}
			if !strings.Contains(result, "activate\n") || !strings.Contains(result, "authority\n") || !strings.Contains(result, "done\n") {
				t.Fatalf("child recovery events = %q", result)
			}
			record, err := NewFileAuthorityJournal(journalPath).Load()
			if err != nil || record.Phase != AuthorityPhaseChild {
				t.Fatalf("recovered record = %+v, %v", record, err)
			}
		})
	}
}

func TestConcurrentBrokenPipeTransitionSharesFailureAndRecoversParent(t *testing.T) {
	requireProcessHandoff(t)
	launch := helperLaunch(t, "close_commands")
	process, err := (Supervisor{ReadyTimeout: 5 * time.Second, CommandTimeout: 500 * time.Millisecond}).Start(t.Context(), launch)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- process.Activate(t.Context())
		}()
	}
	wg.Wait()
	close(errs)
	var first string
	for err := range errs {
		if err == nil {
			t.Fatal("Activate() succeeded on broken command pipe")
		}
		if first == "" {
			first = err.Error()
		} else if err.Error() != first {
			t.Fatalf("concurrent errors differ: %q and %q", first, err)
		}
	}
	record, err := NewFileAuthorityJournal(launch.AuthorityJournal).Load()
	if err != nil || record.Phase != AuthorityPhaseParent || record.ChildPID != 0 {
		t.Fatalf("broken-pipe recovery = %+v, %v", record, err)
	}
}

func TestSuccessfulWaitClosesParentControlDescriptors(t *testing.T) {
	requireProcessHandoff(t)
	process, err := (Supervisor{ReadyTimeout: 5 * time.Second, CommandTimeout: 5 * time.Second}).Start(t.Context(), helperLaunch(t, "ready"))
	if err != nil {
		t.Fatal(err)
	}
	if err := process.Activate(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := process.TransferAuthority(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := process.Wait(); err != nil {
		t.Fatal(err)
	}
	if _, err := process.commands.Stat(); err == nil {
		t.Fatal("parent command descriptor remains open after Wait")
	}
	if _, err := process.eventFile.Stat(); err == nil {
		t.Fatal("parent event descriptor remains open after Wait")
	}
}

func TestAuthorityJournalNextIdentityRequiresCurrentProcessOwnership(t *testing.T) {
	journal := NewFileAuthorityJournal(t.TempDir() + string(os.PathSeparator) + "authority.json")
	oldIdentity := testIdentity()
	if err := journal.Begin(oldIdentity, 100); err != nil {
		t.Fatal(err)
	}
	if err := journal.Advance(oldIdentity, 200, AuthorityPhaseReady); err != nil {
		t.Fatal(err)
	}
	if err := journal.Advance(oldIdentity, 200, AuthorityPhaseActive); err != nil {
		t.Fatal(err)
	}
	newIdentity := oldIdentity
	newIdentity.Revision++
	newIdentity.GenerationID = "generation-18"
	newIdentity.LeaseID = "lease-18"
	newIdentity.LaunchEpoch = "launch-18"
	if err := journal.BeginOwned(newIdentity, 100, func(pid int) bool { return pid == 200 }); err == nil {
		t.Fatal("stale parent replaced live child authority")
	}
	if err := journal.BeginOwned(newIdentity, 200, func(pid int) bool { return pid == 200 }); err == nil {
		t.Fatal("child began the next launch before final authority")
	}
	if err := journal.Advance(oldIdentity, 200, AuthorityPhaseChild); err != nil {
		t.Fatal(err)
	}
	if err := journal.BeginOwned(newIdentity, 200, func(pid int) bool { return pid == 200 }); err != nil {
		t.Fatalf("current child owner could not begin next identity: %v", err)
	}
	record, err := journal.Load()
	if err != nil {
		t.Fatal(err)
	}
	if record.Identity != newIdentity || record.Phase != AuthorityPhaseParent || record.ParentPID != 200 || !record.LaunchPending {
		t.Fatalf("next authority record = %+v", record)
	}
}

func TestAuthorityJournalRejectsOverlappingReadyAndActiveLaunches(t *testing.T) {
	for _, phase := range []AuthorityPhase{AuthorityPhaseReady, AuthorityPhaseActive} {
		t.Run(string(phase), func(t *testing.T) {
			journal := NewFileAuthorityJournal(filepath.Join(t.TempDir(), "authority.json"))
			current := testIdentity()
			if err := journal.Begin(current, 100); err != nil {
				t.Fatal(err)
			}
			if err := journal.Advance(current, 200, AuthorityPhaseReady); err != nil {
				t.Fatal(err)
			}
			if phase == AuthorityPhaseActive {
				if err := journal.Advance(current, 200, AuthorityPhaseActive); err != nil {
					t.Fatal(err)
				}
			}
			next := current
			next.Revision++
			next.LaunchEpoch = "replacement-launch"
			if err := journal.BeginOwned(next, 100, func(pid int) bool { return pid == 100 || pid == 200 }); err == nil {
				t.Fatalf("BeginOwned() replaced live %s launch", phase)
			}
		})
	}
}

func TestAuthorityJournalLaunchEpochFencesStaleChild(t *testing.T) {
	journal := NewFileAuthorityJournal(filepath.Join(t.TempDir(), "authority.json"))
	stale := testIdentity()
	if err := journal.Begin(stale, 100); err != nil {
		t.Fatal(err)
	}
	for _, phase := range []AuthorityPhase{AuthorityPhaseReady, AuthorityPhaseActive, AuthorityPhaseChild} {
		if err := journal.Advance(stale, 200, phase); err != nil {
			t.Fatal(err)
		}
	}
	replacement := stale
	replacement.LaunchEpoch = "replacement-launch"
	if err := journal.BeginOwned(replacement, 200, func(pid int) bool { return pid == 200 }); err != nil {
		t.Fatal(err)
	}
	if err := journal.AttachChild(replacement, 200, 300); err != nil {
		t.Fatal(err)
	}
	if err := journal.Advance(stale, 200, AuthorityPhaseReady); err == nil {
		t.Fatal("stale child advanced replacement launch")
	}
	if err := journal.Advance(replacement, 300, AuthorityPhaseReady); err != nil {
		t.Fatalf("replacement child could not advance: %v", err)
	}
}

func TestAuthorityJournalSerializesCrossProcessOperations(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("cross-process lock liveness is supported on linux")
	}
	dir := t.TempDir()
	journalPath := filepath.Join(dir, "authority.json")
	readyPath := filepath.Join(dir, "lock-ready")
	releasePath := filepath.Join(dir, "lock-release")
	journal := NewFileAuthorityJournal(journalPath)
	if err := journal.Begin(testIdentity(), os.Getpid()); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestHotRestartHelperProcess$")
	cmd.Env = setEnv(os.Environ(), "NRE_HOT_RESTART_LOCK_JOURNAL", journalPath)
	cmd.Env = setEnv(cmd.Env, "NRE_HOT_RESTART_LOCK_READY", readyPath)
	cmd.Env = setEnv(cmd.Env, "NRE_HOT_RESTART_LOCK_RELEASE", releasePath)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	waitForFile(t, readyPath)

	loaded := make(chan error, 1)
	go func() {
		_, err := NewFileAuthorityJournal(journalPath).Load()
		loaded <- err
	}()
	select {
	case err := <-loaded:
		t.Fatalf("Load() bypassed cross-process journal lock: %v", err)
	case <-time.After(150 * time.Millisecond):
	}
	if err := os.WriteFile(releasePath, []byte("release"), 0o600); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-loaded:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Load() did not resume after cross-process journal lock release")
	}
	if err := cmd.Wait(); err != nil {
		t.Fatal(err)
	}
}

func TestAuthorityJournalRecoversLockCrashBeforeHolderWrite(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("cross-process lock liveness is supported on linux")
	}
	journalPath := filepath.Join(t.TempDir(), "authority.json")
	journal := NewFileAuthorityJournal(journalPath)
	if err := journal.Begin(testIdentity(), os.Getpid()); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestHotRestartHelperProcess$")
	cmd.Env = setEnv(os.Environ(), "NRE_HOT_RESTART_CRASH_LOCK_JOURNAL", journalPath)
	if err := cmd.Run(); err == nil {
		t.Fatal("lock crash helper exited successfully")
	}
	if _, err := NewFileAuthorityJournal(journalPath).Load(); err != nil {
		t.Fatalf("journal remained wedged after partial lock crash: %v", err)
	}
}

func TestAuthorityJournalColdProcessAdoptsOrphanedAuthority(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("process liveness recovery is supported on linux")
	}
	for _, phase := range []string{"pending", "child_authority"} {
		t.Run(phase, func(t *testing.T) {
			journalPath := filepath.Join(t.TempDir(), "authority.json")
			cmd := exec.Command(os.Args[0], "-test.run=^TestHotRestartHelperProcess$")
			cmd.Env = setEnv(os.Environ(), "NRE_HOT_RESTART_COLD_AUTHORITY_JOURNAL", journalPath)
			cmd.Env = setEnv(cmd.Env, "NRE_HOT_RESTART_COLD_AUTHORITY_PHASE", phase)
			if err := cmd.Run(); err != nil {
				t.Fatal(err)
			}
			next := testIdentity()
			next.Revision++
			next.GenerationID = "generation-18"
			next.LeaseID = "lease-18"
			next.LaunchEpoch = "cold-restart-launch"
			journal := NewFileAuthorityJournal(journalPath)
			if err := journal.BeginOwned(next, os.Getpid(), platform.ProcessAlive); err != nil {
				t.Fatalf("cold process could not adopt %s authority: %v", phase, err)
			}
			record, err := journal.Load()
			if err != nil || record.ParentPID != os.Getpid() || record.Phase != AuthorityPhaseParent || !record.LaunchPending {
				t.Fatalf("adopted authority = %+v, %v", record, err)
			}
		})
	}
}

func TestAuthorityJournalGuardReclaimsStaleLockWithLiveReusedPID(t *testing.T) {
	journalPath := filepath.Join(t.TempDir(), "authority.json")
	journal := NewFileAuthorityJournal(journalPath)
	if err := journal.Begin(testIdentity(), os.Getpid()); err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(authorityLockRecord{PID: os.Getpid(), Token: "stale-live-pid"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(journalPath+".lock", payload, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := journal.Load(); err != nil {
		t.Fatalf("guard did not reclaim stale live-pid lock: %v", err)
	}
}

func TestValidateMessageBindsVersionTypeAndIdentity(t *testing.T) {
	identity := testIdentity()
	for _, tc := range []struct {
		name string
		msg  message
	}{
		{name: "version", msg: message{Version: ProtocolVersion + 1, Type: messageReady, Identity: identity}},
		{name: "type", msg: message{Version: ProtocolVersion, Type: messageActivated, Identity: identity}},
		{name: "identity", msg: message{Version: ProtocolVersion, Type: messageReady, Identity: Identity{Revision: identity.Revision + 1}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := validateMessage(tc.msg, messageReady, identity); err == nil {
				t.Fatal("validateMessage() succeeded, want rejection")
			}
		})
	}
}

func TestHotRestartHelperProcess(t *testing.T) {
	if journalPath := os.Getenv("NRE_HOT_RESTART_COLD_AUTHORITY_JOURNAL"); journalPath != "" {
		journal := NewFileAuthorityJournal(journalPath)
		identity := testIdentity()
		if err := journal.BeginOwned(identity, os.Getpid(), platform.ProcessAlive); err != nil {
			os.Exit(53)
		}
		if os.Getenv("NRE_HOT_RESTART_COLD_AUTHORITY_PHASE") == "child_authority" {
			for _, phase := range []AuthorityPhase{AuthorityPhaseReady, AuthorityPhaseActive, AuthorityPhaseChild} {
				if err := journal.Advance(identity, os.Getpid(), phase); err != nil {
					os.Exit(54)
				}
			}
		}
		return
	}
	if journalPath := os.Getenv("NRE_HOT_RESTART_CRASH_LOCK_JOURNAL"); journalPath != "" {
		journal := NewFileAuthorityJournal(journalPath)
		journal.lockCreated = func() { os.Exit(51) }
		_, _ = journal.Load()
		os.Exit(52)
	}
	if journalPath := os.Getenv("NRE_HOT_RESTART_LOCK_JOURNAL"); journalPath != "" {
		runJournalLockHelper(journalPath)
		return
	}
	if parentMode := os.Getenv("NRE_HOT_RESTART_PARENT_MODE"); parentMode != "" {
		runRecoveryParentHelper(parentMode)
		return
	}
	mode := os.Getenv("NRE_HOT_RESTART_TEST_MODE")
	if mode == "" {
		return
	}
	if mode == "crash" {
		os.Exit(23)
	}
	session, child, err := OpenChildSessionFromEnvironment()
	if err != nil || !child {
		os.Exit(24)
	}
	defer session.Close()
	if mode == "hang" {
		select {}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := session.Ready(); err != nil {
		os.Exit(25)
	}
	if mode == "close_commands" {
		_ = session.commands.Close()
		time.Sleep(5 * time.Second)
		return
	}
	if mode == "post_ready_crash" {
		os.Exit(28)
	}
	if mode == "acks_lost" {
		var activate message
		if err := session.decoder.Decode(&activate); err != nil || validateMessage(activate, messageActivate, session.Identity) != nil {
			os.Exit(29)
		}
		if err := session.journal.Advance(session.Identity, os.Getpid(), AuthorityPhaseActive); err != nil {
			os.Exit(30)
		}
		_ = session.events.Close()
		var authority message
		if err := session.decoder.Decode(&authority); err != nil || validateMessage(authority, messageAuthority, session.Identity) != nil {
			os.Exit(31)
		}
		if err := session.journal.Advance(session.Identity, os.Getpid(), AuthorityPhaseChild); err != nil {
			os.Exit(32)
		}
		time.Sleep(500 * time.Millisecond)
		return
	}
	activation := func() error { return appendRecoveryEvent("activate") }
	if mode == "activation_hang" {
		activation = func() error { select {} }
	}
	if err := session.AwaitActivation(ctx, activation); err != nil {
		os.Exit(26)
	}
	authority := func() error { return appendRecoveryEvent("authority") }
	if mode == "authority_hang" {
		authority = func() error { select {} }
	}
	if err := session.AwaitAuthority(ctx, authority); err != nil {
		os.Exit(27)
	}
	if mode == "parent_loss_child" {
		if err := appendRecoveryEvent("done"); err != nil {
			os.Exit(33)
		}
	}
	// A real child enters the long-running agent loop after authority transfer.
	time.Sleep(250 * time.Millisecond)
}

func runJournalLockHelper(journalPath string) {
	journal := NewFileAuthorityJournal(journalPath)
	err := journal.withLock(func() error {
		if err := os.WriteFile(os.Getenv("NRE_HOT_RESTART_LOCK_READY"), []byte("ready"), 0o600); err != nil {
			return err
		}
		for {
			if _, err := os.Stat(os.Getenv("NRE_HOT_RESTART_LOCK_RELEASE")); err == nil {
				return nil
			}
			time.Sleep(10 * time.Millisecond)
		}
	})
	if err != nil {
		os.Exit(50)
	}
}

func waitForFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", path)
}

func runRecoveryParentHelper(checkpoint string) {
	journalPath := os.Getenv("NRE_HOT_RESTART_RECOVERY_JOURNAL")
	resultPath := os.Getenv("NRE_HOT_RESTART_RECOVERY_RESULT")
	env := setEnv(os.Environ(), "NRE_HOT_RESTART_PARENT_MODE", "")
	env = setEnv(env, "NRE_HOT_RESTART_TEST_MODE", "parent_loss_child")
	env = setEnv(env, "NRE_HOT_RESTART_RECOVERY_RESULT", resultPath)
	launch := Launch{
		Binary: os.Args[0], Argv: []string{os.Args[0], "-test.run=^TestHotRestartHelperProcess$"},
		Env: env, Identity: testIdentity(), AuthorityJournal: journalPath,
	}
	process, err := (Supervisor{ReadyTimeout: 5 * time.Second, CommandTimeout: 5 * time.Second}).Start(context.Background(), launch)
	if err != nil {
		os.Exit(40)
	}
	if checkpoint == "active" {
		if err := process.Activate(context.Background()); err != nil {
			os.Exit(41)
		}
	}
	os.Exit(0)
}

func appendRecoveryEvent(event string) error {
	path := os.Getenv("NRE_HOT_RESTART_RECOVERY_RESULT")
	if path == "" {
		return nil
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, writeErr := file.WriteString(event + "\n")
	return errors.Join(writeErr, file.Close())
}

func helperLaunch(t *testing.T, mode string) Launch {
	t.Helper()
	return Launch{
		Binary:           os.Args[0],
		Argv:             []string{os.Args[0], "-test.run=^TestHotRestartHelperProcess$"},
		Env:              setEnv(os.Environ(), "NRE_HOT_RESTART_TEST_MODE", mode),
		Identity:         testIdentity(),
		AuthorityJournal: t.TempDir() + string(os.PathSeparator) + "authority.json",
	}
}

func testIdentity() Identity {
	return Identity{Revision: 17, SnapshotDigest: strings.Repeat("a", 64), GenerationID: "generation-17", LeaseID: "lease-17", LaunchEpoch: "launch-17"}
}

func requireProcessHandoff(t *testing.T) {
	t.Helper()
	if !platform.SupportsHotRestart() {
		t.Skipf("process FD handoff is unsupported on %s/%s", runtime.GOOS, runtime.GOARCH)
	}
}
