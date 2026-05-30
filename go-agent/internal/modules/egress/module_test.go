package egress

import (
	"context"
	"errors"
	"net"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/relay"
	basewireguard "github.com/sakullla/nginx-reverse-emby/go-agent/internal/wireguard"
)

func TestFinalHopDialerDelegatesWireGuardProfilesToProvider(t *testing.T) {
	t.Parallel()

	profileID := 23
	runtime := &recordingWireGuardRuntime{}
	provider := &recordingProvider{runtimes: map[int]relay.WireGuardRuntime{profileID: runtime}}
	mod := NewModule(nil)
	dialer := mod.FinalHopDialer([]model.EgressProfile{validWireGuardEgressProfile(profileID)}, provider)

	conn, err := dialer.DialTCP(context.Background(), "10.0.0.10:443", &profileID)
	if err != nil {
		t.Fatalf("DialTCP() error = %v", err)
	}
	_ = conn.Close()

	if provider.lookups[0] != profileID {
		t.Fatalf("provider lookups = %+v, want profile %d", provider.lookups, profileID)
	}
	if runtime.network != "tcp" || runtime.address != "10.0.0.10:443" {
		t.Fatalf("runtime dial = %s %s, want tcp target", runtime.network, runtime.address)
	}
}

func TestWireGuardRuntimeAppliesInlineEgressProfiles(t *testing.T) {
	t.Parallel()

	factory := &recordingFactory{}
	runtime := NewWireGuardRuntime(factory.Create)
	defer runtime.Close()

	if err := runtime.Apply(context.Background(), []model.EgressProfile{validWireGuardEgressProfile(41)}); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if len(factory.configs) != 1 {
		t.Fatalf("created configs = %d, want 1", len(factory.configs))
	}
	cfg := factory.configs[0]
	if cfg.ID != 41 || cfg.Name != "egress-wg" || cfg.PrivateKey != wireGuardTestKey {
		t.Fatalf("wireguard config = %+v, want converted egress profile", cfg.WireGuardProfile)
	}
	if got, ok := runtime.Provider().WireGuardRuntime(41); !ok || got != factory.runtimes[0] {
		t.Fatalf("Provider().WireGuardRuntime(41) = %v, %v; want created runtime", got, ok)
	}
}

func TestModuleIdentityAndCapabilityAreStable(t *testing.T) {
	t.Parallel()

	mod := NewModule(nil)
	if got := mod.Name(); got != "egress" {
		t.Fatalf("Name() = %q, want egress", got)
	}
	caps := mod.Capabilities()
	if len(caps) != 1 || caps[0].Name != "egress_profiles" || !caps[0].Enabled {
		t.Fatalf("Capabilities() = %+v, want egress_profiles capability", caps)
	}
	if mod.WireGuardRuntime() == nil {
		t.Fatal("WireGuardRuntime() = nil")
	}
}

func validWireGuardEgressProfile(id int) model.EgressProfile {
	return model.EgressProfile{
		ID:      id,
		Name:    "egress-wg",
		Type:    "wireguard",
		Enabled: true,
		WireGuardConfig: &model.EgressWireGuardConfig{
			PrivateKey: wireGuardTestKey,
			Addresses:  []string{"10.30.0.1/24"},
			Peers: []model.WireGuardPeer{{
				Name:       "peer",
				PublicKey:  wireGuardTestKey,
				Endpoint:   "127.0.0.1:51820",
				AllowedIPs: []string{"10.30.0.2/32"},
			}},
		},
	}
}

type recordingProvider struct {
	runtimes map[int]relay.WireGuardRuntime
	lookups  []int
}

func (p *recordingProvider) WireGuardRuntime(profileID int) (relay.WireGuardRuntime, bool) {
	p.lookups = append(p.lookups, profileID)
	runtime, ok := p.runtimes[profileID]
	return runtime, ok
}

type recordingWireGuardRuntime struct {
	network string
	address string
}

func (r *recordingWireGuardRuntime) DialContext(_ context.Context, network string, address string) (net.Conn, error) {
	r.network = network
	r.address = address
	left, right := net.Pipe()
	_ = right.Close()
	return left, nil
}

func (r *recordingWireGuardRuntime) ListenTCP(context.Context, string) (net.Listener, error) {
	return nil, errors.New("not implemented")
}

func (r *recordingWireGuardRuntime) ListenTransparentTCP(context.Context) (net.Listener, error) {
	return nil, errors.New("not implemented")
}

func (r *recordingWireGuardRuntime) ListenUDP(context.Context, string) (net.PacketConn, error) {
	return nil, errors.New("not implemented")
}

func (r *recordingWireGuardRuntime) ListenTransparentUDP(context.Context, string) (basewireguard.TransparentUDPConn, error) {
	return nil, errors.New("not implemented")
}

func (r *recordingWireGuardRuntime) Close() error {
	return nil
}

type recordingFactory struct {
	configs  []basewireguard.Config
	runtimes []*recordingWireGuardRuntime
}

func (f *recordingFactory) Create(_ context.Context, cfg basewireguard.Config) (basewireguard.Runtime, error) {
	runtime := &recordingWireGuardRuntime{}
	f.configs = append(f.configs, cfg)
	f.runtimes = append(f.runtimes, runtime)
	return runtime, nil
}

const wireGuardTestKey = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
