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
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
	relaymodule "github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/relay"
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
	mustRegister(t, registry, staticProviderModule{name: "certs", provides: module.ProviderTLSMaterial, provider: tlsProvider})
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
	mustRegister(t, registry, staticProviderModule{name: "certs", provides: module.ProviderTLSMaterial, provider: tlsProvider})
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
	mustRegister(t, registry, staticProviderModule{name: "certs", provides: module.ProviderTLSMaterial, provider: tlsProvider})
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
	mustRegister(t, registry, staticProviderModule{name: "certs", provides: module.ProviderTLSMaterial, provider: tlsProvider})
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

type fakeTLSMaterialProvider struct {
	certificates map[int]tls.Certificate
}

func (p fakeTLSMaterialProvider) ServerCertificate(_ context.Context, certificateID int) (*tls.Certificate, error) {
	cert := p.certificates[certificateID]
	return &cert, nil
}

func (fakeTLSMaterialProvider) TrustedCAPool(context.Context, []int) (*x509.CertPool, error) {
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
