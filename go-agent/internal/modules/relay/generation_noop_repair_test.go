package relay

import (
	"context"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/generation"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
)

func TestRelayNoopGenerationsDoNotDuplicateRuntimeDrainOwnership(t *testing.T) {
	provider := newFakeTLSMaterialProvider()
	listener, hop := newRelayEndpoint(t, provider, 301, "relay-noop-generation", "pin_only", true, false)
	clock := &relayNoopDrainClock{now: time.Unix(100, 0)}
	drain := generation.NewDrainController(clock)
	mod := NewModule(Config{AgentID: listener.AgentID, DrainController: drain, DrainTimeout: time.Hour})
	resolver := relayNoopProviderResolver{tls: provider}

	first := model.Snapshot{Revision: 1, RelayListeners: []model.RelayListener{listener}}
	if err := mod.Apply(context.Background(), module.ApplyRequest{Next: first, Providers: resolver}); err != nil {
		t.Fatalf("first Apply() error = %v", err)
	}
	t.Cleanup(func() { _ = mod.Close() })

	target, closeTarget := startTCPEchoServer(t)
	defer closeTarget()
	stream, err := Dial(context.Background(), "tcp", target, []Hop{hop}, provider)
	if err != nil {
		t.Fatalf("open existing relay stream: %v", err)
	}
	defer stream.Close()
	assertRelayNoopRoundTrip(t, stream, "before-noops")

	mod.mu.Lock()
	firstRuntime := mod.runtime
	mod.mu.Unlock()
	poolConn, poolPeer := net.Pipe()
	defer poolPeer.Close()
	pooled := newTestGenerationTunnel(poolConn)
	firstRuntime.poolScope.tls.mu.Lock()
	firstRuntime.poolScope.tls.sessions["retained"] = []*tlsTCPTunnel{pooled}
	firstRuntime.poolScope.tls.mu.Unlock()

	previous := first
	for revision := int64(2); revision <= 4; revision++ {
		next := previous
		next.Revision = revision
		if err := mod.Apply(context.Background(), module.ApplyRequest{Previous: previous, Next: next, Providers: resolver}); err != nil {
			t.Fatalf("no-op Apply(%d) error = %v", revision, err)
		}
		previous = next
		assertRelayNoopRoundTrip(t, stream, "after-noop")
		select {
		case <-pooled.closed:
			t.Fatalf("no-op generation %d closed the active generation pool", revision)
		default:
		}
	}
	if snapshot := drain.Snapshot(); len(snapshot.Generations) != 1 {
		t.Fatalf("drain generations after three no-ops = %d, want 1: %+v", len(snapshot.Generations), snapshot)
	}

	secondListener := listener
	secondListener.Revision++
	second := previous
	second.Revision = 5
	second.RelayListeners = []model.RelayListener{secondListener}
	if err := mod.Apply(context.Background(), module.ApplyRequest{Previous: previous, Next: second, Providers: resolver}); err != nil {
		t.Fatalf("second runtime Apply() error = %v", err)
	}
	mod.mu.Lock()
	secondRuntime := mod.runtime
	mod.mu.Unlock()
	secondStream, err := Dial(context.Background(), "tcp", target, []Hop{{Address: hop.Address, ServerName: hop.ServerName, Listener: secondListener}}, provider)
	if err != nil {
		t.Fatalf("open second-generation stream: %v", err)
	}
	assertRelayNoopRoundTrip(t, secondStream, "second-generation")
	thirdListener := secondListener
	thirdListener.Revision++
	third := second
	third.Revision = 6
	third.RelayListeners = []model.RelayListener{thirdListener}
	if err := mod.Apply(context.Background(), module.ApplyRequest{Previous: second, Next: third, Providers: resolver}); err != nil {
		t.Fatalf("third runtime Apply() error = %v", err)
	}
	mod.mu.Lock()
	thirdRuntime := mod.runtime
	mod.mu.Unlock()
	if thirdRuntime == nil || thirdRuntime == firstRuntime || thirdRuntime == secondRuntime {
		t.Fatal("third runtime was not installed")
	}
	select {
	case <-firstRuntime.ctx.Done():
	default:
		t.Fatal("generation-limit did not release the oldest runtime")
	}
	select {
	case <-pooled.closed:
	default:
		t.Fatal("generation-limit did not release the oldest generation pool")
	}
	if err := secondStream.Close(); err != nil {
		t.Fatalf("close second-generation stream: %v", err)
	}
	select {
	case <-secondRuntime.ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("retired runtime was not cleaned immediately after becoming idle")
	}

	thirdStream, err := Dial(context.Background(), "tcp", target, []Hop{{Address: hop.Address, ServerName: hop.ServerName, Listener: thirdListener}}, provider)
	if err != nil {
		t.Fatalf("open third-generation stream: %v", err)
	}
	defer thirdStream.Close()
	assertRelayNoopRoundTrip(t, thirdStream, "before-timeout")
	fourthListener := thirdListener
	fourthListener.Revision++
	fourth := third
	fourth.Revision = 7
	fourth.RelayListeners = []model.RelayListener{fourthListener}
	if err := mod.Apply(context.Background(), module.ApplyRequest{Previous: third, Next: fourth, Providers: resolver}); err != nil {
		t.Fatalf("fourth runtime Apply() error = %v", err)
	}
	assertRelayNoopRoundTrip(t, thirdStream, "during-timeout-drain")
	mod.mu.Lock()
	currentRuntime := mod.runtime
	mod.mu.Unlock()
	if err := clock.fireAll(); err != nil {
		t.Fatalf("fire drain timeouts: %v", err)
	}
	select {
	case <-thirdRuntime.ctx.Done():
	default:
		t.Fatal("timeout did not release the draining third runtime")
	}
	select {
	case <-currentRuntime.ctx.Done():
		t.Fatal("generation-limit/timeout cleanup closed the current runtime")
	default:
	}
	currentPoolConn, currentPoolPeer := net.Pipe()
	defer currentPoolPeer.Close()
	currentPooled := newTestGenerationTunnel(currentPoolConn)
	currentRuntime.poolScope.tls.mu.Lock()
	currentRuntime.poolScope.tls.sessions["current"] = []*tlsTCPTunnel{currentPooled}
	currentRuntime.poolScope.tls.mu.Unlock()
	select {
	case <-currentPooled.closed:
		t.Fatal("current generation pool was already closed")
	default:
	}
	newStream, err := Dial(context.Background(), "tcp", target, []Hop{{Address: hop.Address, ServerName: hop.ServerName, Listener: fourthListener}}, provider)
	if err != nil {
		t.Fatalf("current listener unavailable after forced retired cleanup: %v", err)
	}
	defer newStream.Close()
	assertRelayNoopRoundTrip(t, newStream, "after-forced-cleanup")
}

func assertRelayNoopRoundTrip(t *testing.T, conn net.Conn, payload string) {
	t.Helper()
	if err := conn.SetDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write([]byte(payload)); err != nil {
		t.Fatalf("write relay stream: %v", err)
	}
	buffer := make([]byte, len(payload))
	if _, err := io.ReadFull(conn, buffer); err != nil {
		t.Fatalf("read relay stream: %v", err)
	}
	if string(buffer) != payload {
		t.Fatalf("relay echo = %q, want %q", buffer, payload)
	}
	_ = conn.SetDeadline(time.Time{})
}

type relayNoopProviderResolver struct{ tls TLSMaterialProvider }

func (r relayNoopProviderResolver) Resolve(ref module.ProviderRef) (any, bool) {
	if ref == module.ProviderTLSMaterial {
		return r.tls, true
	}
	return nil, false
}

type relayNoopDrainClock struct {
	mu     sync.Mutex
	now    time.Time
	timers []*relayNoopDrainTimer
}

func (c *relayNoopDrainClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *relayNoopDrainClock) AfterFunc(_ time.Duration, fn func()) generation.Timer {
	timer := &relayNoopDrainTimer{fn: fn}
	c.mu.Lock()
	c.timers = append(c.timers, timer)
	c.mu.Unlock()
	return timer
}

func (c *relayNoopDrainClock) fireAll() error {
	c.mu.Lock()
	timers := append([]*relayNoopDrainTimer(nil), c.timers...)
	c.mu.Unlock()
	for _, timer := range timers {
		timer.fire()
	}
	return nil
}

type relayNoopDrainTimer struct {
	mu      sync.Mutex
	fn      func()
	stopped bool
	fired   bool
}

func (t *relayNoopDrainTimer) Stop() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.stopped || t.fired {
		return false
	}
	t.stopped = true
	return true
}

func (t *relayNoopDrainTimer) fire() {
	t.mu.Lock()
	if t.stopped || t.fired {
		t.mu.Unlock()
		return
	}
	t.fired = true
	fn := t.fn
	t.mu.Unlock()
	fn()
}
