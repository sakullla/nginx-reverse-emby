//go:build !integration

package egress

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
)

const socks5UDPTestTimeout = time.Second

var _ module.EgressResolver = (*Module)(nil)
var _ module.TransactionalModule = (*Module)(nil)
var _ module.UDPPeer = udpPacketConn{}

func TestModulePublishesFinalHopDialerAndResolver(t *testing.T) {
	mod := NewModule(nil)
	registry := module.NewRegistry()
	mustRegister(t, registry, mod)
	next := model.Snapshot{EgressProfiles: []model.EgressProfile{{ID: 11, Type: "direct", Enabled: true}}}
	if err := registry.Apply(context.Background(), model.Snapshot{}, next); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	provider, ok := registry.Resolve(module.ProviderFinalHopDialer)
	if !ok {
		t.Fatal("finalhop.dialer provider missing")
	}
	if _, ok := provider.(module.FinalHopDialer); !ok {
		t.Fatalf("finalhop.dialer provider type = %T, want module.FinalHopDialer", provider)
	}
	resolverProvider, ok := registry.Resolve(module.ProviderEgressResolver)
	if !ok {
		t.Fatal("egress.resolver provider missing")
	}
	resolver, ok := resolverProvider.(module.EgressResolver)
	if !ok {
		t.Fatalf("egress.resolver provider type = %T, want module.EgressResolver", resolverProvider)
	}
	profileID := 11
	profile, found, err := resolver.Resolve(&profileID, "tcp")
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if !found || profile.ID != profileID || profile.Type != "direct" {
		t.Fatalf("Resolve() = %+v, %v; want direct profile %d", profile, found, profileID)
	}
}

func TestModuleKeepsPreparedEgressResolverInvisibleUntilPublish(t *testing.T) {
	mod := NewModule(nil)
	registry := module.NewRegistry()
	mustRegister(t, registry, mod)
	first := model.Snapshot{Revision: 1, EgressProfiles: []model.EgressProfile{{ID: 11, Type: "direct", Enabled: true}}}
	if err := registry.Apply(context.Background(), model.Snapshot{}, first); err != nil {
		t.Fatalf("Apply(first) error = %v", err)
	}
	firstView := registry.ActiveGeneration()

	second := model.Snapshot{Revision: 2, EgressProfiles: []model.EgressProfile{{ID: 12, Type: "direct", Enabled: true}}}
	generationContext, err := module.NewGenerationContext(first, second)
	if err != nil {
		t.Fatalf("NewGenerationContext() error = %v", err)
	}
	candidate, err := registry.PrepareGeneration(context.Background(), generationContext)
	if err != nil {
		t.Fatalf("PrepareGeneration() error = %v", err)
	}
	if err := candidate.Ready(context.Background()); err != nil {
		t.Fatalf("Ready() error = %v", err)
	}

	if registry.ActiveGeneration() != firstView {
		t.Fatal("egress candidate replaced active generation before publish")
	}
	assertResolvedEgressProfile(t, registry, 11)
	if _, _, err := mod.Resolve(intPtr(12), "tcp"); err == nil {
		t.Fatal("module exposed prepared egress profile before publish")
	}

	candidate.Publish()
	assertResolvedEgressProfile(t, registry, 12)
}

func TestModuleStateDoesNotAdvanceWhenLaterModuleApplyFails(t *testing.T) {
	mod := NewModule(nil)
	previous := model.Snapshot{EgressProfiles: []model.EgressProfile{{
		ID:      61,
		Name:    "previous",
		Type:    "direct",
		Enabled: true,
	}}}
	if err := mod.Apply(context.Background(), module.ApplyRequest{Next: previous}); err != nil {
		t.Fatalf("initial Apply() error = %v", err)
	}

	registry := module.NewRegistry()
	mustRegister(t, registry, mod)
	mustRegister(t, registry, failingModule{name: "later"})

	next := model.Snapshot{EgressProfiles: []model.EgressProfile{{
		ID:      62,
		Name:    "next",
		Type:    "direct",
		Enabled: true,
	}}}
	if err := registry.Apply(context.Background(), previous, next); err == nil {
		t.Fatal("registry.Apply() error = nil, want later module failure")
	}

	previousID := 61
	profile, found, err := mod.Resolve(&previousID, "tcp")
	if err != nil {
		t.Fatalf("Resolve(previous) error = %v", err)
	}
	if !found || profile.ID != previousID {
		t.Fatalf("Resolve(previous) = %+v, %v; want profile %d", profile, found, previousID)
	}
	nextID := 62
	if _, _, err := mod.Resolve(&nextID, "tcp"); err == nil {
		t.Fatal("Resolve(next) error = nil, want state not advanced")
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
	}})

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

func assertResolvedEgressProfile(t *testing.T, resolver module.ProviderResolver, id int) {
	t.Helper()
	provider, ok := resolver.Resolve(module.ProviderEgressResolver)
	if !ok {
		t.Fatal("egress.resolver provider is missing")
	}
	egressResolver, ok := provider.(module.EgressResolver)
	if !ok {
		t.Fatalf("egress.resolver provider = %T, want module.EgressResolver", provider)
	}
	profile, found, err := egressResolver.Resolve(&id, "tcp")
	if err != nil || !found || profile.ID != id {
		t.Fatalf("Resolve(%d) = %+v/%v/%v", id, profile, found, err)
	}
}

func startObservingSOCKS5UDPProxy(t *testing.T) (string, <-chan model.SOCKS5UDPPacket) {
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

	packetCh := make(chan model.SOCKS5UDPPacket, 1)
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
		req, err := model.ReadClientRequest(context.Background(), client, model.EntryAuth{})
		if err != nil {
			t.Errorf("ReadClientRequest() error = %v", err)
			return
		}
		if req.Protocol != "socks5-udp" {
			t.Errorf("req.Protocol = %q, want socks5-udp", req.Protocol)
			return
		}
		if err := model.WriteClientRequestSuccessWithBind(client, req, udpLn.LocalAddr()); err != nil {
			t.Errorf("WriteClientRequestSuccessWithBind() error = %v", err)
			return
		}

		buf := make([]byte, 64*1024)
		n, _, err := udpLn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		packet, err := model.ParseSOCKS5UDPPacket(buf[:n])
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

func waitForSOCKS5UDPPacket(t *testing.T, packetCh <-chan model.SOCKS5UDPPacket) model.SOCKS5UDPPacket {
	t.Helper()

	select {
	case packet, ok := <-packetCh:
		if !ok {
			t.Fatal("SOCKS5 UDP proxy closed before packet was observed")
		}
		return packet
	case <-time.After(socks5UDPTestTimeout):
		t.Fatal("timed out waiting for SOCKS5 UDP packet")
		return model.SOCKS5UDPPacket{}
	}
}

func mustRegister(t *testing.T, registry *module.Registry, mod module.Module) {
	t.Helper()

	if err := registry.Register(mod); err != nil {
		t.Fatalf("Register(%s) error = %v", mod.Name(), err)
	}
}

type failingModule struct {
	name string
}

func (m failingModule) Name() string { return m.name }

func (m failingModule) Descriptor() module.ModuleDescriptor {
	return module.ModuleDescriptor{Name: m.name}
}

func (m failingModule) RegisterProviders(module.ProviderRegistry) error { return nil }

func (m failingModule) Capabilities(module.SnapshotView) []module.Capability { return nil }

func (m failingModule) Apply(context.Context, module.ApplyRequest) error {
	return errors.New("later module failed")
}

func (m failingModule) Stop(context.Context) error { return nil }

type commitFailingModule struct {
	name string
	err  error
}

func (m commitFailingModule) Name() string { return m.name }

func (m commitFailingModule) Descriptor() module.ModuleDescriptor {
	return module.ModuleDescriptor{Name: m.name}
}

func (m commitFailingModule) RegisterProviders(module.ProviderRegistry) error { return nil }

func (m commitFailingModule) Capabilities(module.SnapshotView) []module.Capability {
	return nil
}

func (m commitFailingModule) Apply(context.Context, module.ApplyRequest) error { return nil }

func (m commitFailingModule) Prepare(context.Context, module.ApplyRequest) (module.ModuleTransaction, error) {
	return module.TransactionFuncs{CommitFunc: func() error { return m.err }}, nil
}

func (m commitFailingModule) Stop(context.Context) error { return nil }
