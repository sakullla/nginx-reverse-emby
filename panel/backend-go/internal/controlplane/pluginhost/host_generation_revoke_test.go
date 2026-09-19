package pluginhost

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"testing"
	"time"
)

type generationRevokeProcess struct {
	command *exec.Cmd
	stop    io.WriteCloser
}

func (process generationRevokeProcess) PID() int    { return process.command.Process.Pid }
func (process generationRevokeProcess) Wait() error { return process.command.Wait() }
func (process generationRevokeProcess) Signal(signal os.Signal) error {
	// The test child uses stdin EOF as a portable graceful signal; it really
	// exits and is joined by monitor, with Kill as the bounded fallback.
	return process.stop.Close()
}
func (process generationRevokeProcess) Kill() error { return process.command.Process.Kill() }

func TestGenerationRevokeChildProcess(t *testing.T) {
	if os.Getenv("NRE_TEST_GENERATION_REVOKE_CHILD") != "1" {
		return
	}
	_, _ = io.Copy(io.Discard, os.Stdin)
	os.Exit(0)
}

func generationRevokeInstance(t *testing.T, generation string) *Instance {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(executable, "-test.run=^TestGenerationRevokeChildProcess$")
	command.Env = append(os.Environ(), "NRE_TEST_GENERATION_REVOKE_CHILD=1")
	command.Stdout, command.Stderr = io.Discard, io.Discard
	stop, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	instance := &Instance{ID: "instance-a", Generation: generation, PID: command.Process.Pid, process: generationRevokeProcess{command: command, stop: stop},
		State: "healthy", grace: 250 * time.Millisecond, done: make(chan struct{})}
	go instance.monitor()
	t.Cleanup(func() { _ = instance.Stop(context.Background()) })
	return instance
}

func TestStopExactGenerationPreservesReplacementAndClosesAllOldProcesses(t *testing.T) {
	oldActive := generationRevokeInstance(t, "old-generation")
	oldPrepared := generationRevokeInstance(t, "old-generation")
	newActive := generationRevokeInstance(t, "new-generation")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	oldControlCtx, cancelOld := context.WithCancel(ctx)
	defer cancelOld()
	oldActive.control = &runtimeControl{ctx: oldControlCtx, cancel: cancelOld}
	host := &Host{ctx: ctx, cancel: cancel, active: map[string]*Instance{"instance-a": newActive},
		prepared: map[*Instance]struct{}{oldActive: {}, oldPrepared: {}}}
	results, err := host.StopGenerationWithResults(t.Context(), "instance-a", "old-generation")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 || !oldActive.Terminated() || !oldPrepared.Terminated() || newActive.Terminated() {
		t.Fatal("exact revoke failed to close both old processes while preserving the replacement")
	}
	if oldControlCtx.Err() == nil {
		t.Fatal("old restart owner was not cancelled")
	}
	if active, found := host.Active("instance-a"); !found || active != newActive {
		t.Fatal("new active process lost ownership")
	}
	if _, err := host.StopGenerationWithResults(t.Context(), "instance-a", "old-generation"); err != nil {
		t.Fatal("repeated revoke failed", err)
	}
	if _, err := host.PrepareCandidate(t.Context(), Candidate{InstanceID: "instance-a", Identity: Identity{Generation: "old-generation"}}); err == nil {
		t.Fatal("revoked generation could restart")
	}
	if _, err := host.StopGenerationWithResults(t.Context(), "instance-a", "new-generation"); err != nil {
		t.Fatal(err)
	}
	if !newActive.Terminated() {
		t.Fatal("exact active process did not exit")
	}
}

func TestStopExactGenerationWaitsForPreparingOwnerAndKeepsFenceOnTimeout(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	preparing := &hostGenerationPreparation{count: 1}
	preparing.wg.Add(1)
	host := &Host{ctx: ctx, cancel: cancel, active: map[string]*Instance{}, prepared: map[*Instance]struct{}{},
		generationPreparations: map[hostGenerationIdentity]*hostGenerationPreparation{{"instance-a", "old-generation"}: preparing}}
	deadline, stop := context.WithTimeout(t.Context(), 10*time.Millisecond)
	defer stop()
	if _, err := host.StopGenerationWithResults(deadline, "instance-a", "old-generation"); err == nil {
		t.Fatal("preparing generation was acknowledged before its owner joined")
	}
	if _, err := host.PrepareCandidate(t.Context(), Candidate{InstanceID: "instance-a", Identity: Identity{Generation: "old-generation"}}); err == nil {
		t.Fatal("timeout removed the revocation fence")
	}
	preparing.wg.Done()
	if _, err := host.StopGenerationWithResults(t.Context(), "instance-a", "old-generation"); err != nil {
		t.Fatal(err)
	}
}

func TestStopExactGenerationRequiresCleanupAcknowledgement(t *testing.T) {
	instance := generationRevokeInstance(t, "old-generation")
	failCleanup := true
	instance.securityCleanup = func() error {
		if failCleanup {
			return errors.New("test cleanup still pending")
		}
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	host := &Host{ctx: ctx, cancel: cancel, active: map[string]*Instance{"instance-a": instance}, prepared: map[*Instance]struct{}{}}
	if _, err := host.StopGenerationWithResults(t.Context(), "instance-a", "old-generation"); err == nil {
		t.Fatal("unacknowledged cleanup was reported successful")
	}
	if instance.Terminated() {
		t.Fatal("process exit alone was treated as complete cleanup")
	}
	if active, found := host.Active("instance-a"); !found || active != instance {
		t.Fatal("failed cleanup lost its owner")
	}
	failCleanup = false
	if _, err := host.StopGenerationWithResults(t.Context(), "instance-a", "old-generation"); err != nil {
		t.Fatal("cleanup retry failed", err)
	}
	if !instance.Terminated() {
		t.Fatal("cleanup retry did not confirm terminal state")
	}
}
