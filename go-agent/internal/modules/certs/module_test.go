//go:build integration

package certs

import (
	"context"
	"crypto/tls"
	"crypto/x509"
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
