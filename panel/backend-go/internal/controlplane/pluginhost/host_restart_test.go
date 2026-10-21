package pluginhost

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestWatchRestartsOwnedRuntimeWhenStatusPersistenceFails(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	prepared := make(chan struct{}, 1)
	host := &Host{
		ctx: ctx, cancel: cancel,
		active:         map[string]*Instance{},
		observerErrors: map[string]error{},
		observer: func(RuntimeStatus) error {
			return errors.New("runtime status write canceled")
		},
		authorize: func(Candidate) error {
			select {
			case prepared <- struct{}{}:
			default:
			}
			return errors.New("restart probe")
		},
	}
	instance, control := exitedOwnedRuntime(t, host, "on-failure", 3)
	control.wg.Add(1)
	go host.watch(control, instance)
	select {
	case <-prepared:
	case <-time.After(2 * time.Second):
		t.Fatal("status persistence failure abandoned the owned runtime")
	}
	control.cancel()
	waitWatch(t, control)
	if active, ok := host.Active(instance.ID); !ok || active != instance {
		t.Fatal("failed status write dropped the owned runtime")
	}
}

func TestWatchDoesNotRestartWithoutStatusObserver(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var prepareCalls atomic.Int32
	host := &Host{
		ctx: ctx, cancel: cancel,
		active:         map[string]*Instance{},
		observerErrors: map[string]error{},
		authorize: func(Candidate) error {
			prepareCalls.Add(1)
			return errors.New("restart probe")
		},
	}
	_, control := exitedOwnedRuntime(t, host, "on-failure", 3)
	control.wg.Add(1)
	go host.watch(control, host.active["shadowsocks-server-default"])
	waitWatch(t, control)
	if prepareCalls.Load() != 0 {
		t.Fatal("missing status observer restarted the runtime")
	}
}

func TestWatchDoesNotRestartAfterOwnershipLossOrCircuitOpen(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var prepareCalls atomic.Int32
	authorize := func(Candidate) error {
		prepareCalls.Add(1)
		return errors.New("restart probe")
	}
	lost := &Host{
		ctx: ctx, cancel: cancel,
		active:         map[string]*Instance{},
		observerErrors: map[string]error{},
		authorize:      authorize,
	}
	_, lostControl := exitedOwnedRuntime(t, lost, "on-failure", 3)
	lost.observer = func(RuntimeStatus) error {
		lostControl.cancel()
		return errors.New("runtime status write canceled")
	}
	lostControl.wg.Add(1)
	go lost.watch(lostControl, lost.active["shadowsocks-server-default"])
	waitWatch(t, lostControl)

	blocked := &Host{
		ctx: ctx, cancel: cancel,
		active:         map[string]*Instance{},
		observerErrors: map[string]error{},
		observer: func(RuntimeStatus) error {
			return errors.New("runtime status write canceled")
		},
		authorize: authorize,
	}
	blockedInstance, blockedControl := exitedOwnedRuntime(t, blocked, "never", 0)
	blockedControl.wg.Add(1)
	go blocked.watch(blockedControl, blockedInstance)
	waitWatch(t, blockedControl)
	if prepareCalls.Load() != 0 {
		t.Fatal("restart continued after ownership loss or circuit open")
	}
	blockedInstance.mu.RLock()
	state, circuit := blockedInstance.State, blockedInstance.CircuitOpen
	blockedInstance.mu.RUnlock()
	if state != "failed" || !circuit {
		t.Fatal("circuit-open runtime was not recorded as failed")
	}
}

func exitedOwnedRuntime(t *testing.T, host *Host, restart string, limit int) (*Instance, *runtimeControl) {
	t.Helper()
	done := make(chan struct{})
	close(done)
	runCtx, runCancel := context.WithCancel(host.ctx)
	t.Cleanup(runCancel)
	candidate := normalizeRestartCandidate(Candidate{
		InstanceID:     "shadowsocks-server-default",
		Identity:       Identity{PluginID: "shadowsocks-server", Generation: "gen-1"},
		Restart:        restart,
		RestartLimit:   limit,
		InitialBackoff: time.Millisecond,
		MaximumBackoff: time.Millisecond,
		RestartWindow:  time.Minute,
	})
	control := &runtimeControl{candidate: candidate, backoff: candidate.InitialBackoff, ctx: runCtx, cancel: runCancel}
	instance := &Instance{
		ID: "shadowsocks-server-default", Generation: "gen-1", State: "healthy",
		done: done, candidate: candidate, control: control,
	}
	host.active[instance.ID] = instance
	return instance, control
}

func waitWatch(t *testing.T, control *runtimeControl) {
	t.Helper()
	finished := make(chan struct{})
	go func() {
		control.wg.Wait()
		close(finished)
	}()
	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("plugin restart watch did not return")
	}
}
