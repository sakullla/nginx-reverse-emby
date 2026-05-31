package relay_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
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

func (fakeUDPPeer) Close() error                       { return nil }
func (fakeUDPPeer) SetReadDeadline(time.Time) error    { return nil }
func (fakeUDPPeer) SetWriteDeadline(time.Time) error   { return nil }
func (fakeUDPPeer) ReadPacket() ([]byte, error)        { return nil, nil }
func (fakeUDPPeer) WritePacket([]byte) error           { return nil }

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

func mustRegister(t *testing.T, registry *module.Registry, mod any) {
	t.Helper()
	if err := registry.Register(mod); err != nil {
		t.Fatalf("Register(%T) error = %v", mod, err)
	}
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
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
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
	return tls.Certificate{
		Certificate: [][]byte{der},
		PrivateKey:  privateKey,
	}
}
