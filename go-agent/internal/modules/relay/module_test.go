package relay_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"fmt"
	"math/big"
	"net"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
	relaymodule "github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/relay"
	modulewireguard "github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/wireguard"
)

func TestModuleAppliesLocalRelayListenersAndConsumesProviders(t *testing.T) {
	certificateID := 1
	tlsProvider := fakeTLSMaterialProvider{
		certificates: map[int]tls.Certificate{
			certificateID: mustIssueTestTLSCertificate(t),
		},
	}
	mod := relaymodule.NewModule(relaymodule.Config{AgentID: "agent-a", AgentName: "node-a"})
	registry := module.NewRegistry()
	mustRegister(t, registry, staticProviderModule{name: "certs", provides: module.ProviderTLSMaterial, provider: &tlsProvider})
	mustRegister(t, registry, staticProviderModule{name: "egress", provides: module.ProviderFinalHopDialer, provider: fakeFinalHopDialer{}})
	mustRegister(t, registry, mod)

	next := model.Snapshot{RelayListeners: []model.RelayListener{{
		ID:            1,
		AgentID:       "agent-a",
		ListenHost:    "127.0.0.1",
		ListenPort:    pickFreeTCPPort(t),
		Enabled:       true,
		CertificateID: &certificateID,
		TLSMode:       "pin_only",
		PinSet: []model.RelayPin{{
			Type:  "sha256",
			Value: "pin-value",
		}},
	}}}
	if err := registry.Apply(context.Background(), model.Snapshot{}, next); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if _, ok := registry.Resolve(module.ProviderDiagnosticsRelaySource); !ok {
		t.Fatal("diagnostics.relay.source provider missing")
	}
}

func TestModuleAppliesListenerMatchedByAgentName(t *testing.T) {
	certificateID := 1
	cert := mustIssueTestTLSCertificate(t)
	tlsProvider := fakeTLSMaterialProvider{
		certificates: map[int]tls.Certificate{certificateID: cert},
	}
	mod := relaymodule.NewModule(relaymodule.Config{AgentID: "agent-a", AgentName: "node-a"})
	registry := module.NewRegistry()
	mustRegister(t, registry, staticProviderModule{name: "certs", provides: module.ProviderTLSMaterial, provider: &tlsProvider})
	mustRegister(t, registry, mod)

	port := pickFreeTCPPort(t)
	next := model.Snapshot{RelayListeners: []model.RelayListener{testRelayListener(11, "remote-id", "node-a", port, certificateID)}}
	if err := registry.Apply(context.Background(), model.Snapshot{}, next); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	got := dialServedCertificate(t, port)
	if !certificateDEREqual(got, cert) {
		t.Fatal("relay listener matched by agent_name did not start with expected certificate")
	}
}

func TestModuleReappliesSameAddressRelayListener(t *testing.T) {
	firstCertificateID := 1
	secondCertificateID := 2
	firstCert := mustIssueTestTLSCertificate(t)
	secondCert := mustIssueTestTLSCertificate(t)
	tlsProvider := fakeTLSMaterialProvider{certificates: map[int]tls.Certificate{
		firstCertificateID:  firstCert,
		secondCertificateID: secondCert,
	}}
	mod := relaymodule.NewModule(relaymodule.Config{AgentID: "agent-a", AgentName: "node-a"})
	registry := module.NewRegistry()
	mustRegister(t, registry, staticProviderModule{name: "certs", provides: module.ProviderTLSMaterial, provider: &tlsProvider})
	mustRegister(t, registry, mod)

	port := pickFreeTCPPort(t)
	previous := model.Snapshot{RelayListeners: []model.RelayListener{testRelayListener(21, "agent-a", "node-a", port, firstCertificateID)}}
	if err := registry.Apply(context.Background(), model.Snapshot{}, previous); err != nil {
		t.Fatalf("initial Apply() error = %v", err)
	}

	nextListener := testRelayListener(21, "agent-a", "node-a", port, secondCertificateID)
	nextListener.Revision = 2
	next := model.Snapshot{RelayListeners: []model.RelayListener{nextListener}}
	if err := registry.Apply(context.Background(), previous, next); err != nil {
		t.Fatalf("same-address Apply() error = %v", err)
	}

	got := dialServedCertificate(t, port)
	if !certificateDEREqual(got, secondCert) {
		t.Fatal("same-address relay reapply did not replace the served certificate")
	}
}

func TestModuleApplyNoopsWhenEffectiveInputsUnchanged(t *testing.T) {
	certificateID := 1
	cert := mustIssueTestTLSCertificate(t)
	tlsProvider := fakeTLSMaterialProvider{certificates: map[int]tls.Certificate{certificateID: cert}}
	mod := relaymodule.NewModule(relaymodule.Config{AgentID: "agent-a", AgentName: "node-a"})
	registry := module.NewRegistry()
	mustRegister(t, registry, staticProviderModule{name: "certs", provides: module.ProviderTLSMaterial, provider: &tlsProvider})
	mustRegister(t, registry, mod)

	port := pickFreeTCPPort(t)
	previous := model.Snapshot{
		RelayListeners: []model.RelayListener{testRelayListener(51, "agent-a", "node-a", port, certificateID)},
		EgressProfiles: []model.EgressProfile{{ID: 1, Name: "direct", Type: "direct", Enabled: true}},
	}
	if err := registry.Apply(context.Background(), model.Snapshot{}, previous); err != nil {
		t.Fatalf("initial Apply() error = %v", err)
	}
	got := dialServedCertificate(t, port)
	if !certificateDEREqual(got, cert) {
		t.Fatal("initial relay listener did not serve expected certificate")
	}
	initialLookups := tlsProvider.lookups

	next := previous
	next.Rules = []model.HTTPRule{{ID: 99, FrontendURL: "http://example.test"}}
	if err := registry.Apply(context.Background(), previous, next); err != nil {
		t.Fatalf("unchanged relay Apply() error = %v", err)
	}
	if tlsProvider.lookups != initialLookups {
		t.Fatalf("unchanged relay inputs looked up TLS material %d times after initial apply, want %d", tlsProvider.lookups, initialLookups)
	}

	got = dialServedCertificate(t, port)
	if !certificateDEREqual(got, cert) {
		t.Fatal("unchanged relay inputs should keep the active listener serving")
	}
}

func TestModuleRollbackRestoresPreviousRuntimeAfterSameAddressPrepare(t *testing.T) {
	firstCertificateID := 1
	secondCertificateID := 2
	firstCert := mustIssueTestTLSCertificate(t)
	secondCert := mustIssueTestTLSCertificate(t)
	tlsProvider := fakeTLSMaterialProvider{certificates: map[int]tls.Certificate{
		firstCertificateID:  firstCert,
		secondCertificateID: secondCert,
	}}
	mod := relaymodule.NewModule(relaymodule.Config{AgentID: "agent-a", AgentName: "node-a"})
	registry := module.NewRegistry()
	mustRegister(t, registry, staticProviderModule{name: "certs", provides: module.ProviderTLSMaterial, provider: &tlsProvider})
	mustRegister(t, registry, mod)

	port := pickFreeTCPPort(t)
	previous := model.Snapshot{RelayListeners: []model.RelayListener{testRelayListener(31, "agent-a", "node-a", port, firstCertificateID)}}
	if err := registry.Apply(context.Background(), model.Snapshot{}, previous); err != nil {
		t.Fatalf("initial Apply() error = %v", err)
	}

	failErr := errors.New("later module failed")
	mustRegister(t, registry, failingModule{name: "later", err: failErr})
	nextListener := testRelayListener(31, "agent-a", "node-a", port, secondCertificateID)
	nextListener.Revision = 2
	next := model.Snapshot{RelayListeners: []model.RelayListener{nextListener}}
	err := registry.Apply(context.Background(), previous, next)
	if !errors.Is(err, failErr) {
		t.Fatalf("Apply() error = %v, want later module failure", err)
	}

	got := dialServedCertificate(t, port)
	if !certificateDEREqual(got, firstCert) {
		t.Fatal("rollback did not restore the previous relay runtime certificate")
	}
	if certificateDEREqual(got, secondCert) {
		t.Fatal("rollback left the replacement relay runtime serving")
	}
}

func TestModuleRollbackAfterCommitRestoresPreviousRuntime(t *testing.T) {
	firstCertificateID := 1
	secondCertificateID := 2
	firstCert := mustIssueTestTLSCertificate(t)
	secondCert := mustIssueTestTLSCertificate(t)
	tlsProvider := fakeTLSMaterialProvider{certificates: map[int]tls.Certificate{
		firstCertificateID:  firstCert,
		secondCertificateID: secondCert,
	}}
	mod := relaymodule.NewModule(relaymodule.Config{AgentID: "agent-a", AgentName: "node-a"})
	registry := module.NewRegistry()
	mustRegister(t, registry, staticProviderModule{name: "certs", provides: module.ProviderTLSMaterial, provider: &tlsProvider})
	mustRegister(t, registry, mod)

	firstPort := pickFreeTCPPort(t)
	secondPort := pickFreeTCPPort(t)
	previous := model.Snapshot{RelayListeners: []model.RelayListener{testRelayListener(41, "agent-a", "node-a", firstPort, firstCertificateID)}}
	if err := registry.Apply(context.Background(), model.Snapshot{}, previous); err != nil {
		t.Fatalf("initial Apply() error = %v", err)
	}

	failErr := errors.New("later commit failed")
	mustRegister(t, registry, commitFailingModule{name: "later-transaction", err: failErr})
	next := model.Snapshot{RelayListeners: []model.RelayListener{testRelayListener(42, "agent-a", "node-a", secondPort, secondCertificateID)}}
	err := registry.Apply(context.Background(), previous, next)
	if !errors.Is(err, failErr) {
		t.Fatalf("Apply() error = %v, want later commit failure", err)
	}

	restored := dialServedCertificate(t, firstPort)
	if !certificateDEREqual(restored, firstCert) {
		t.Fatal("post-commit rollback did not restore the previous relay runtime")
	}
	if _, err := tls.DialWithDialer(&net.Dialer{Timeout: 50 * time.Millisecond}, "tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(secondPort)), &tls.Config{InsecureSkipVerify: true}); err == nil {
		t.Fatal("post-commit rollback left the replacement relay runtime serving")
	}
}

func TestModulePrepareUsesPendingWireGuardOverlayRuntime(t *testing.T) {
	certificateID := 1
	cert := mustIssueTestTLSCertificate(t)
	tlsProvider := fakeTLSMaterialProvider{certificates: map[int]tls.Certificate{certificateID: cert}}
	wireGuardRuntime := modulewireguard.NewRuntime(func(context.Context, modulewireguard.Config) (modulewireguard.RuntimeHandle, error) {
		return &relayWireGuardRuntime{}, nil
	})
	defer wireGuardRuntime.Close()
	relayModule := relaymodule.NewModule(relaymodule.Config{AgentID: "agent-a", AgentName: "node-a"})
	registry := module.NewRegistry()
	mustRegister(t, registry, staticProviderModule{name: "certs", provides: module.ProviderTLSMaterial, provider: &tlsProvider})
	mustRegister(t, registry, modulewireguard.NewModule(wireGuardRuntime))
	mustRegister(t, registry, relayModule)

	profileID := 91
	next := model.Snapshot{
		WireGuardProfiles: []model.WireGuardProfile{testWireGuardProfile(profileID, "agent-a")},
		RelayListeners: []model.RelayListener{func() model.RelayListener {
			listener := testRelayListener(91, "agent-a", "node-a", pickFreeTCPPort(t), certificateID)
			listener.TransportMode = relaymodule.ListenerTransportModeWireGuard
			listener.WireGuardProfileID = &profileID
			return listener
		}()},
	}
	if err := registry.Apply(context.Background(), model.Snapshot{}, next); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
}

func TestModuleRollbackRestoresWireGuardRelayOnPreviousOverlayRuntime(t *testing.T) {
	certificateID := 1
	cert := mustIssueTestTLSCertificate(t)
	tlsProvider := fakeTLSMaterialProvider{certificates: map[int]tls.Certificate{certificateID: cert}}
	wireGuardRuntime := modulewireguard.NewRuntime(func(_ context.Context, cfg modulewireguard.Config) (modulewireguard.RuntimeHandle, error) {
		return &relayWireGuardRuntime{profileID: cfg.ID}, nil
	})
	defer wireGuardRuntime.Close()
	relayModule := relaymodule.NewModule(relaymodule.Config{AgentID: "agent-a", AgentName: "node-a"})
	registry := module.NewRegistry()
	mustRegister(t, registry, staticProviderModule{name: "certs", provides: module.ProviderTLSMaterial, provider: &tlsProvider})
	mustRegister(t, registry, modulewireguard.NewModule(wireGuardRuntime))
	mustRegister(t, registry, relayModule)

	profileID := 92
	port := pickFreeTCPPort(t)
	listener := testRelayListener(92, "agent-a", "node-a", port, certificateID)
	listener.TransportMode = relaymodule.ListenerTransportModeWireGuard
	listener.WireGuardProfileID = &profileID
	previous := model.Snapshot{
		WireGuardProfiles: []model.WireGuardProfile{testWireGuardProfile(profileID, "agent-a")},
		RelayListeners:    []model.RelayListener{listener},
	}
	if err := registry.Apply(context.Background(), model.Snapshot{}, previous); err != nil {
		t.Fatalf("initial Apply() error = %v", err)
	}
	if got := dialServedCertificate(t, port); !certificateDEREqual(got, cert) {
		t.Fatal("initial WireGuard relay listener did not serve expected certificate")
	}

	failErr := errors.New("later commit failed")
	mustRegister(t, registry, commitFailingModule{name: "later-transaction", err: failErr})
	nextProfile := testWireGuardProfile(profileID, "agent-a")
	nextProfile.Peers[0].Endpoint = "127.0.0.1:51821"
	next := model.Snapshot{
		WireGuardProfiles: []model.WireGuardProfile{nextProfile},
		RelayListeners:    []model.RelayListener{listener},
	}
	err := registry.Apply(context.Background(), previous, next)
	if !errors.Is(err, failErr) {
		t.Fatalf("Apply() error = %v, want later commit failure", err)
	}

	if got := dialServedCertificate(t, port); !certificateDEREqual(got, cert) {
		t.Fatal("rollback did not restore WireGuard relay listener on the previous overlay runtime")
	}
}

type fakeTLSMaterialProvider struct {
	certificates map[int]tls.Certificate
	lookups      int
}

func (p *fakeTLSMaterialProvider) ServerCertificate(_ context.Context, certificateID int) (*tls.Certificate, error) {
	p.lookups++
	cert := p.certificates[certificateID]
	return &cert, nil
}

func (*fakeTLSMaterialProvider) TrustedCAPool(context.Context, []int) (*x509.CertPool, error) {
	return nil, nil
}

type fakeFinalHopDialer struct{}

func (fakeFinalHopDialer) DialTCP(context.Context, string, *int) (net.Conn, error) {
	return nil, nil
}

func (fakeFinalHopDialer) OpenUDP(context.Context, string, *int) (module.UDPPeer, error) {
	return fakeUDPPeer{}, nil
}

type fakeUDPPeer struct{}

func (fakeUDPPeer) Close() error                     { return nil }
func (fakeUDPPeer) SetReadDeadline(time.Time) error  { return nil }
func (fakeUDPPeer) SetWriteDeadline(time.Time) error { return nil }
func (fakeUDPPeer) ReadPacket() ([]byte, error)      { return nil, nil }
func (fakeUDPPeer) WritePacket([]byte) error         { return nil }

type staticProviderModule struct {
	name     string
	provides module.ProviderRef
	provider any
}

func (m staticProviderModule) Name() string { return m.name }

func (m staticProviderModule) Descriptor() module.ModuleDescriptor {
	return module.ModuleDescriptor{Name: m.name, Provides: []module.ProviderRef{m.provides}}
}

func (m staticProviderModule) RegisterProviders(reg module.ProviderRegistry) error {
	return reg.Provide(m.provides, m.provider)
}

func (staticProviderModule) Capabilities(module.SnapshotView) []module.Capability { return nil }
func (staticProviderModule) Apply(context.Context, module.ApplyRequest) error     { return nil }
func (staticProviderModule) Stop(context.Context) error                           { return nil }

type failingModule struct {
	name string
	err  error
}

func (m failingModule) Name() string { return m.name }

func (m failingModule) Descriptor() module.ModuleDescriptor {
	return module.ModuleDescriptor{Name: m.name}
}

func (failingModule) RegisterProviders(module.ProviderRegistry) error { return nil }
func (failingModule) Capabilities(module.SnapshotView) []module.Capability {
	return nil
}
func (m failingModule) Apply(context.Context, module.ApplyRequest) error { return m.err }
func (failingModule) Stop(context.Context) error                         { return nil }

type commitFailingModule struct {
	name string
	err  error
}

func (m commitFailingModule) Name() string { return m.name }

func (m commitFailingModule) Descriptor() module.ModuleDescriptor {
	return module.ModuleDescriptor{Name: m.name}
}

func (commitFailingModule) RegisterProviders(module.ProviderRegistry) error { return nil }
func (commitFailingModule) Capabilities(module.SnapshotView) []module.Capability {
	return nil
}
func (commitFailingModule) Apply(context.Context, module.ApplyRequest) error { return nil }
func (m commitFailingModule) Prepare(context.Context, module.ApplyRequest) (module.ModuleTransaction, error) {
	return module.TransactionFuncs{CommitFunc: func() error { return m.err }}, nil
}
func (commitFailingModule) Stop(context.Context) error { return nil }

type relayWireGuardRuntime struct {
	mu        sync.Mutex
	profileID int
	closed    bool
	listeners []net.Listener
}

func (*relayWireGuardRuntime) DialContext(context.Context, string, string) (net.Conn, error) {
	return nil, fmt.Errorf("unexpected wireguard dial")
}

func (r *relayWireGuardRuntime) ListenTCP(_ context.Context, address string) (net.Listener, error) {
	ln, err := net.Listen("tcp", address)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		_ = ln.Close()
		return nil, net.ErrClosed
	}
	r.listeners = append(r.listeners, ln)
	return ln, nil
}

func (*relayWireGuardRuntime) ListenTransparentTCP(context.Context) (net.Listener, error) {
	return nil, fmt.Errorf("unexpected transparent tcp listen")
}

func (*relayWireGuardRuntime) ListenUDP(context.Context, string) (net.PacketConn, error) {
	return nil, fmt.Errorf("unexpected udp listen")
}

func (*relayWireGuardRuntime) ListenTransparentUDP(context.Context, string) (modulewireguard.TransparentUDPConn, error) {
	return nil, fmt.Errorf("unexpected transparent udp listen")
}

func (r *relayWireGuardRuntime) Close() error {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil
	}
	r.closed = true
	listeners := append([]net.Listener(nil), r.listeners...)
	r.mu.Unlock()
	for _, ln := range listeners {
		_ = ln.Close()
	}
	return nil
}

func mustRegister(t *testing.T, registry *module.Registry, mod any) {
	t.Helper()
	if err := registry.Register(mod); err != nil {
		t.Fatalf("Register(%T) error = %v", mod, err)
	}
}

func testRelayListener(id int, agentID string, agentName string, port int, certificateID int) model.RelayListener {
	return model.RelayListener{
		ID:            id,
		AgentID:       agentID,
		AgentName:     agentName,
		ListenHost:    "127.0.0.1",
		ListenPort:    port,
		Enabled:       true,
		CertificateID: &certificateID,
		TLSMode:       "pin_only",
		PinSet: []model.RelayPin{{
			Type:  "sha256",
			Value: "pin-value",
		}},
		Revision: 1,
	}
}

func testWireGuardProfile(id int, agentID string) model.WireGuardProfile {
	return model.WireGuardProfile{
		ID:         id,
		AgentID:    agentID,
		Name:       "wg",
		Mode:       modulewireguard.ModeGenericWireGuard,
		PrivateKey: wireGuardTestKey,
		Addresses:  []string{"10.90.0.2/32"},
		Peers: []model.WireGuardPeer{{
			Name:       "peer",
			PublicKey:  wireGuardTestKey,
			Endpoint:   "127.0.0.1:51820",
			AllowedIPs: []string{"10.90.0.0/24"},
		}},
		Enabled: true,
	}
}

const wireGuardTestKey = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="

func dialServedCertificate(t *testing.T, port int) tls.Certificate {
	t.Helper()
	address := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	var lastErr error
	for deadline := time.Now().Add(time.Second); time.Now().Before(deadline); {
		conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 50 * time.Millisecond}, "tcp", address, &tls.Config{InsecureSkipVerify: true})
		if err != nil {
			lastErr = err
			time.Sleep(10 * time.Millisecond)
			continue
		}
		state := conn.ConnectionState()
		_ = conn.Close()
		if len(state.PeerCertificates) == 0 {
			t.Fatal("relay server did not present a peer certificate")
		}
		return tls.Certificate{Certificate: [][]byte{state.PeerCertificates[0].Raw}}
	}
	t.Fatalf("dial relay listener %s: %v", address, lastErr)
	return tls.Certificate{}
}

func certificateDEREqual(left tls.Certificate, right tls.Certificate) bool {
	if len(left.Certificate) == 0 || len(right.Certificate) == 0 {
		return false
	}
	return sha256.Sum256(left.Certificate[0]) == sha256.Sum256(right.Certificate[0])
}

func pickFreeTCPPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen free tcp port: %v", err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func mustIssueTestTLSCertificate(t *testing.T) tls.Certificate {
	t.Helper()
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate private key: %v", err)
	}
	serial, err := rand.Int(rand.Reader, big.NewInt(1<<62))
	if err != nil {
		t.Fatalf("failed to generate certificate serial: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "127.0.0.1"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	if err != nil {
		t.Fatalf("failed to create certificate: %v", err)
	}
	if len(der) == 0 {
		t.Fatal("created empty certificate")
	}
	return tls.Certificate{
		Certificate: [][]byte{der},
		PrivateKey:  privateKey,
	}
}

func TestCertificateDEREqualRejectsDifferentCertificates(t *testing.T) {
	first := mustIssueTestTLSCertificate(t)
	second := mustIssueTestTLSCertificate(t)
	if certificateDEREqual(first, second) {
		t.Fatalf("test certificates unexpectedly match: %s", fmt.Sprintf("%x", sha256.Sum256(first.Certificate[0])))
	}
}
