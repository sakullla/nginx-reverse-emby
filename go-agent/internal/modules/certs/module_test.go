package certs

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
)

type contractCertificateManager struct {
	bundles  int
	policies int
}

func (manager *contractCertificateManager) Apply(_ context.Context, bundles []model.ManagedCertificateBundle, policies []model.ManagedCertificatePolicy) error {
	manager.bundles = len(bundles)
	manager.policies = len(policies)
	return nil
}

func (*contractCertificateManager) ServerCertificate(context.Context, int) (*tls.Certificate, error) {
	return nil, nil
}

func (*contractCertificateManager) TrustedCAPool(context.Context, []int) (*x509.CertPool, error) {
	return nil, nil
}

func TestModuleAppliesCertificatesAndPublishesTLSMaterial(t *testing.T) {
	manager := &contractCertificateManager{}
	registry := module.NewRegistry()
	if err := registry.Register(NewModule(manager)); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	next := model.Snapshot{
		Certificates:        []model.ManagedCertificateBundle{{ID: 7}},
		CertificatePolicies: []model.ManagedCertificatePolicy{{ID: 8}},
	}
	if err := registry.Apply(context.Background(), model.Snapshot{}, next); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if manager.bundles != 1 || manager.policies != 1 {
		t.Fatalf("applied bundles/policies = %d/%d", manager.bundles, manager.policies)
	}
	if _, ok := registry.Resolve(module.ProviderTLSMaterial); !ok {
		t.Fatal("tls.material provider not registered")
	}
}

func TestModuleBindPublicCertificateTLSRequiresManager(t *testing.T) {
	called := false
	NewModule(nil).BindPublicCertificateTLS(func(interface {
		HandshakeServer(context.Context, int, net.Conn) (net.Conn, error)
	}) {
		called = true
	})
	if called {
		t.Fatal("nil certificate manager was bound")
	}
	manager := mustNewManager(t, t.TempDir())
	t.Cleanup(func() { _ = manager.Close() })
	var got interface {
		HandshakeServer(context.Context, int, net.Conn) (net.Conn, error)
	}
	NewModule(manager).BindPublicCertificateTLS(func(terminator interface {
		HandshakeServer(context.Context, int, net.Conn) (net.Conn, error)
	}) {
		got = terminator
	})
	if got == nil {
		t.Fatal("certificate module was not bound")
	}
}
