package wireguard

import (
	"context"
	"errors"
	"net"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/relay"
	basewireguard "github.com/sakullla/nginx-reverse-emby/go-agent/internal/wireguard"
)

func TestRuntimePrepareCommitAndRollback(t *testing.T) {
	t.Parallel()

	factory := &recordingFactory{}
	runtime := NewRuntime(factory.Create)
	defer runtime.Close()

	initial := testWireGuardProfile(7, "local-agent", "peer.example.com:51820", "10.10.0.0/24")
	updated := initial
	updated.Peers = append([]model.WireGuardPeer(nil), initial.Peers...)
	updated.Peers[0].Endpoint = "peer.example.com:51821"

	if err := runtime.Apply(context.Background(), []model.WireGuardProfile{initial}); err != nil {
		t.Fatalf("Apply(initial) error = %v", err)
	}
	if len(factory.created) != 1 {
		t.Fatalf("created runtimes = %d, want 1", len(factory.created))
	}

	provider := runtime.ProviderForAgent("local-agent")
	got, ok := provider.WireGuardRuntime(initial.ID)
	if !ok || got != factory.created[0] {
		t.Fatalf("ProviderForAgent(initial) = %v, %v; want initial runtime", got, ok)
	}

	tx, err := runtime.Prepare(context.Background(), []model.WireGuardProfile{updated})
	if err != nil {
		t.Fatalf("Prepare(updated) error = %v", err)
	}
	if len(factory.created) != 2 {
		t.Fatalf("created runtimes = %d, want 2", len(factory.created))
	}

	txProvider := runtime.TransactionProviderForAgent(tx, "local-agent", []model.WireGuardProfile{updated})
	candidate, ok := txProvider.WireGuardRuntime(updated.ID)
	if !ok || candidate != factory.created[1] {
		t.Fatalf("transaction provider runtime = %v, %v; want candidate runtime", candidate, ok)
	}

	tx.Rollback()
	if !factory.created[1].closed {
		t.Fatal("candidate runtime was not closed on rollback")
	}
	got, ok = runtime.ProviderForAgent("local-agent").WireGuardRuntime(initial.ID)
	if !ok || got != factory.created[0] {
		t.Fatalf("ProviderForAgent(after rollback) = %v, %v; want original runtime", got, ok)
	}

	tx, err = runtime.Prepare(context.Background(), []model.WireGuardProfile{updated})
	if err != nil {
		t.Fatalf("Prepare(updated second time) error = %v", err)
	}
	if len(factory.created) != 3 {
		t.Fatalf("created runtimes = %d, want 3", len(factory.created))
	}

	runtime.Commit(tx, []model.WireGuardProfile{updated})
	if !factory.created[0].closed {
		t.Fatal("original runtime was not closed on commit")
	}
	got, ok = runtime.ProviderForAgent("local-agent").WireGuardRuntime(updated.ID)
	if !ok || got != factory.created[2] {
		t.Fatalf("ProviderForAgent(after commit) = %v, %v; want committed runtime", got, ok)
	}
}

func TestRuntimeProviderFiltersByLocalAgentIDAndRoutesRelayHop(t *testing.T) {
	t.Parallel()

	factory := &recordingFactory{}
	runtime := NewRuntime(factory.Create)
	defer runtime.Close()

	local := testWireGuardProfile(7, "local-agent", "peer.example.com:51820", "10.10.0.0/24")
	remote := testWireGuardProfile(7, "remote-agent", "peer.example.com:51820", "10.20.0.0/24")

	if err := runtime.Apply(context.Background(), []model.WireGuardProfile{local, remote}); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	if got, ok := runtime.Runtime(local.ID); ok || got != nil {
		t.Fatalf("Runtime(unfiltered) = %v, %v; want ambiguous miss", got, ok)
	}
	got, ok := runtime.RuntimeForAgent("local-agent", local.ID)
	if !ok || got != factory.created[0] {
		t.Fatalf("RuntimeForAgent(local) = %v, %v; want local runtime", got, ok)
	}

	provider := runtime.ProviderForAgent("local-agent")
	hop := relay.Hop{
		Address: "10.10.0.5:443",
		Listener: model.RelayListener{
			AgentID: "local-agent",
		},
	}
	hopProvider, ok := provider.(relay.HopWireGuardRuntimeProvider)
	if !ok {
		t.Fatalf("ProviderForAgent() does not implement HopWireGuardRuntimeProvider: %T", provider)
	}
	gotHop, ok := hopProvider.WireGuardRuntimeForHop(hop)
	if !ok || gotHop != factory.created[0] {
		t.Fatalf("WireGuardRuntimeForHop() = %v, %v; want local runtime via route match", gotHop, ok)
	}
}

func TestRuntimeCloseDelegatesToUnderlyingManager(t *testing.T) {
	t.Parallel()

	factory := &recordingFactory{}
	runtime := NewRuntime(factory.Create)

	if err := runtime.Apply(context.Background(), []model.WireGuardProfile{
		testWireGuardProfile(7, "local-agent", "peer.example.com:51820", "10.10.0.0/24"),
	}); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	if err := runtime.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if !factory.created[0].closed {
		t.Fatal("underlying runtime was not closed")
	}
	if err := runtime.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
	if factory.created[0].closeCount != 1 {
		t.Fatalf("underlying close count = %d, want 1", factory.created[0].closeCount)
	}
}

func TestModuleExposesWireGuardCapabilityAndDelegatesLifecycle(t *testing.T) {
	t.Parallel()

	factory := &recordingFactory{}
	runtime := NewRuntime(factory.Create)
	mod := NewModule(runtime)

	caps := mod.Capabilities()
	if len(caps) != 1 || caps[0].Name != "wireguard" || !caps[0].Enabled {
		t.Fatalf("Capabilities() = %+v, want wireguard capability", caps)
	}

	snapshot := model.Snapshot{WireGuardProfiles: []model.WireGuardProfile{
		testWireGuardProfile(7, "local-agent", "peer.example.com:51820", "10.10.0.0/24"),
	}}
	if err := mod.Start(context.Background(), snapshot); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if len(factory.created) != 1 {
		t.Fatalf("created runtimes = %d, want 1", len(factory.created))
	}
	if err := mod.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if !factory.created[0].closed {
		t.Fatal("module stop did not close the runtime")
	}
}

func testWireGuardProfile(id int, agentID string, endpoint string, allowedIPs ...string) model.WireGuardProfile {
	return model.WireGuardProfile{
		ID:         id,
		AgentID:    agentID,
		Name:       "wireguard",
		Mode:       basewireguard.ModeGenericWireGuard,
		PrivateKey: wireGuardTestKey,
		Addresses:  []string{"10.10.0.2/32"},
		Peers: []model.WireGuardPeer{{
			Name:       "peer",
			PublicKey:  wireGuardTestKey,
			Endpoint:    endpoint,
			AllowedIPs:  append([]string(nil), allowedIPs...),
			Reserved:    nil,
		}},
		Enabled: true,
	}
}

type recordingFactory struct {
	created []*recordingRuntime
}

func (f *recordingFactory) Create(context.Context, basewireguard.Config) (basewireguard.Runtime, error) {
	runtime := &recordingRuntime{}
	f.created = append(f.created, runtime)
	return runtime, nil
}

type recordingRuntime struct {
	closed     bool
	closeCount int
}

func (r *recordingRuntime) DialContext(context.Context, string, string) (net.Conn, error) {
	return nil, errors.New("not implemented")
}

func (r *recordingRuntime) ListenTCP(context.Context, string) (net.Listener, error) {
	return nil, errors.New("not implemented")
}

func (r *recordingRuntime) ListenTransparentTCP(context.Context) (net.Listener, error) {
	return nil, errors.New("not implemented")
}

func (r *recordingRuntime) ListenUDP(context.Context, string) (net.PacketConn, error) {
	return nil, errors.New("not implemented")
}

func (r *recordingRuntime) ListenTransparentUDP(context.Context, string) (basewireguard.TransparentUDPConn, error) {
	return nil, errors.New("not implemented")
}

func (r *recordingRuntime) Close() error {
	r.closeCount++
	r.closed = true
	return nil
}

const wireGuardTestKey = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
