package egress

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
	basewireguard "github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/wireguard"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/proxyproto"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/relay"
)

const socks5UDPTestTimeout = time.Second

func TestModulePublishesFinalHopDialerAndResolver(t *testing.T) {
	mod := NewModule(nil)
	registry := module.NewRegistry()
	mustRegister(t, registry, mod)
	next := model.Snapshot{EgressProfiles: []model.EgressProfile{{ID: 11, Type: "direct", Enabled: true}}}
	if err := registry.Apply(context.Background(), model.Snapshot{}, next); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if _, ok := registry.Resolve(module.ProviderFinalHopDialer); !ok {
		t.Fatal("finalhop.dialer provider missing")
	}
	if _, ok := registry.Resolve(module.ProviderEgressResolver); !ok {
		t.Fatal("egress.resolver provider missing")
	}
}

func TestModuleFinalHopDialerUsesOverlayRuntimeProviderForWireGuardEgress(t *testing.T) {
	profileID := 23
	overlay := &recordingOverlayRuntime{}
	factory := &recordingFactory{}
	egressModule := NewModule(factory.Create)
	registry := module.NewRegistry()
	mustRegister(t, registry, &overlayRuntimeModule{runtime: overlay})
	mustRegister(t, registry, egressModule)

	next := model.Snapshot{EgressProfiles: []model.EgressProfile{validWireGuardEgressProfile(profileID)}}
	if err := registry.Apply(context.Background(), model.Snapshot{}, next); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	provider, ok := registry.Resolve(module.ProviderFinalHopDialer)
	if !ok {
		t.Fatal("finalhop.dialer provider missing")
	}
	dialer, ok := provider.(relay.FinalHopDialer)
	if !ok {
		t.Fatalf("finalhop.dialer provider type = %T, want relay.FinalHopDialer", provider)
	}

	conn, err := dialer.DialTCP(context.Background(), "10.0.0.10:443", &profileID)
	if err != nil {
		t.Fatalf("DialTCP() error = %v", err)
	}
	_ = conn.Close()

	if overlay.profileID != profileID {
		t.Fatalf("overlay profileID = %d, want %d", overlay.profileID, profileID)
	}
	if overlay.network != "tcp" || overlay.address != "10.0.0.10:443" {
		t.Fatalf("overlay dial = %s %s, want tcp target", overlay.network, overlay.address)
	}
}

func TestFinalHopDialerDelegatesWireGuardProfilesToOverlayRuntime(t *testing.T) {
	t.Parallel()

	profileID := 23
	overlay := &recordingOverlayRuntime{}
	mod := NewModule(nil)
	dialer := mod.FinalHopDialer([]model.EgressProfile{validWireGuardEgressProfile(profileID)}, overlay)

	conn, err := dialer.DialTCP(context.Background(), "10.0.0.10:443", &profileID)
	if err != nil {
		t.Fatalf("DialTCP() error = %v", err)
	}
	_ = conn.Close()

	if overlay.profileID != profileID {
		t.Fatalf("overlay profileID = %d, want %d", overlay.profileID, profileID)
	}
	if overlay.network != "tcp" || overlay.address != "10.0.0.10:443" {
		t.Fatalf("overlay dial = %s %s, want tcp target", overlay.network, overlay.address)
	}
}

func TestFinalHopDialerUDPEgressPreservesTargetForSOCKS5(t *testing.T) {
	proxyAddr, packetCh := startObservingSOCKS5UDPProxy(t)
	profileID := 17
	dialer := NewFinalHopDialer([]model.EgressProfile{{
		ID:       profileID,
		Name:     "socks-udp",
		Type:     "socks",
		ProxyURL: "socks5h://" + proxyAddr,
		Enabled:  true,
	}}, nil)

	peer, err := dialer.OpenUDP(context.Background(), "backend.example:5300", &profileID)
	if err != nil {
		t.Fatalf("OpenUDP() error = %v", err)
	}
	defer peer.Close()

	if err := peer.WritePacket([]byte("ping")); err != nil {
		t.Fatalf("WritePacket() error = %v", err)
	}

	packet := waitForSOCKS5UDPPacket(t, packetCh)
	if packet.Target != "backend.example:5300" {
		t.Fatalf("SOCKS5 UDP target = %q, want backend.example:5300", packet.Target)
	}
	if string(packet.Payload) != "ping" {
		t.Fatalf("SOCKS5 UDP payload = %q, want ping", string(packet.Payload))
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
	conn, err := runtime.Provider().DialContext(context.Background(), "", 41, "tcp", "10.0.0.10:443")
	if err != nil {
		t.Fatalf("Provider().DialContext() error = %v", err)
	}
	_ = conn.Close()
	if factory.runtimes[0].network != "tcp" || factory.runtimes[0].address != "10.0.0.10:443" {
		t.Fatalf("provider runtime dial = %s %s, want tcp target", factory.runtimes[0].network, factory.runtimes[0].address)
	}
}

func TestModuleIdentityAndCapabilityAreStable(t *testing.T) {
	t.Parallel()

	mod := NewModule(nil)
	if got := mod.Name(); got != "egress" {
		t.Fatalf("Name() = %q, want egress", got)
	}
	caps := mod.Capabilities(model.Snapshot{})
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

func (f *recordingFactory) Create(_ context.Context, cfg basewireguard.Config) (basewireguard.RuntimeHandle, error) {
	runtime := &recordingWireGuardRuntime{}
	f.configs = append(f.configs, cfg)
	f.runtimes = append(f.runtimes, runtime)
	return runtime, nil
}

const wireGuardTestKey = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="

func startObservingSOCKS5UDPProxy(t *testing.T) (string, <-chan proxyproto.SOCKS5UDPPacket) {
	t.Helper()

	tcpLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen tcp proxy: %v", err)
	}
	udpLn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		_ = tcpLn.Close()
		t.Fatalf("listen udp proxy: %v", err)
	}

	packetCh := make(chan proxyproto.SOCKS5UDPPacket, 1)
	done := make(chan struct{})

	go func() {
		defer close(done)
		defer close(packetCh)

		client, err := tcpLn.Accept()
		if err != nil {
			return
		}
		defer client.Close()
		if err := client.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
			t.Errorf("set tcp deadline: %v", err)
			return
		}
		req, err := proxyproto.ReadClientRequest(context.Background(), client, proxyproto.EntryAuth{})
		if err != nil {
			t.Errorf("ReadClientRequest() error = %v", err)
			return
		}
		if req.Protocol != "socks5-udp" {
			t.Errorf("req.Protocol = %q, want socks5-udp", req.Protocol)
			return
		}
		if err := proxyproto.WriteClientRequestSuccessWithBind(client, req, udpLn.LocalAddr()); err != nil {
			t.Errorf("WriteClientRequestSuccessWithBind() error = %v", err)
			return
		}

		buf := make([]byte, 64*1024)
		n, _, err := udpLn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		packet, err := proxyproto.ParseSOCKS5UDPPacket(buf[:n])
		if err != nil {
			t.Errorf("ParseSOCKS5UDPPacket() error = %v", err)
			return
		}
		packetCh <- packet
	}()

	t.Cleanup(func() {
		_ = tcpLn.Close()
		_ = udpLn.Close()
		select {
		case <-done:
		case <-time.After(socks5UDPTestTimeout):
			t.Fatal("timed out waiting for SOCKS5 UDP proxy goroutine")
		}
	})

	return tcpLn.Addr().String(), packetCh
}

func waitForSOCKS5UDPPacket(t *testing.T, packetCh <-chan proxyproto.SOCKS5UDPPacket) proxyproto.SOCKS5UDPPacket {
	t.Helper()

	select {
	case packet, ok := <-packetCh:
		if !ok {
			t.Fatal("SOCKS5 UDP proxy closed before packet was observed")
		}
		return packet
	case <-time.After(socks5UDPTestTimeout):
		t.Fatal("timed out waiting for SOCKS5 UDP packet")
		return proxyproto.SOCKS5UDPPacket{}
	}
}

func mustRegister(t *testing.T, registry *module.Registry, mod module.Module) {
	t.Helper()

	if err := registry.Register(mod); err != nil {
		t.Fatalf("Register(%s) error = %v", mod.Name(), err)
	}
}

type overlayRuntimeModule struct {
	runtime module.OverlayRuntime
}

func (m *overlayRuntimeModule) Name() string {
	return "overlay"
}

func (m *overlayRuntimeModule) Descriptor() module.ModuleDescriptor {
	return module.ModuleDescriptor{
		Name:     m.Name(),
		Provides: []module.ProviderRef{module.ProviderOverlayRuntime},
	}
}

func (m *overlayRuntimeModule) RegisterProviders(reg module.ProviderRegistry) error {
	return reg.Provide(module.ProviderOverlayRuntime, m.runtime)
}

func (m *overlayRuntimeModule) Capabilities(module.SnapshotView) []module.Capability {
	return nil
}

func (m *overlayRuntimeModule) Apply(context.Context, module.ApplyRequest) error {
	return nil
}

func (m *overlayRuntimeModule) Stop(context.Context) error {
	return nil
}

type recordingOverlayRuntime struct {
	agentID   string
	profileID int
	network   string
	address   string
}

func (r *recordingOverlayRuntime) DialContext(_ context.Context, agentID string, profileID int, network string, address string) (net.Conn, error) {
	r.agentID = agentID
	r.profileID = profileID
	r.network = network
	r.address = address
	left, right := net.Pipe()
	_ = right.Close()
	return left, nil
}

func (r *recordingOverlayRuntime) ListenTCP(context.Context, string, int, string) (net.Listener, error) {
	return nil, errors.New("not implemented")
}

func (r *recordingOverlayRuntime) ListenUDP(context.Context, string, int, string) (net.PacketConn, error) {
	return nil, errors.New("not implemented")
}
