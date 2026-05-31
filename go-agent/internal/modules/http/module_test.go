package http_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net"
	"strconv"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
	httpmodule "github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/http"
)

func TestModuleAppliesHTTPRulesAndProvidesDiagnosticsSource(t *testing.T) {
	port := pickFreeTCPPort(t)
	mod := httpmodule.NewModule(httpmodule.Config{HTTP3Enabled: false})
	registry := module.NewRegistry()
	mustRegister(t, registry, staticProviderModule{name: "certs", provides: module.ProviderTLSMaterial, provider: staticTLSMaterial{}})
	mustRegister(t, registry, mod)

	next := model.Snapshot{Rules: []model.HTTPRule{{
		ID:          1,
		FrontendURL: "http://example.test:" + port,
		Backends:    []model.HTTPBackend{{URL: "http://127.0.0.1:8096"}},
		Enabled:     true,
	}}}
	if err := registry.Apply(context.Background(), model.Snapshot{}, next); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if _, ok := registry.Resolve(module.ProviderDiagnosticsHTTPSource); !ok {
		t.Fatal("diagnostics.http.source provider missing")
	}
}

type staticTLSMaterial struct{}

func (staticTLSMaterial) ServerCertificate(context.Context, int) (*tls.Certificate, error) {
	return nil, nil
}

func (staticTLSMaterial) TrustedCAPool(context.Context, []int) (*x509.CertPool, error) {
	return nil, nil
}

func (staticTLSMaterial) ServerCertificateForHost(context.Context, string) (*tls.Certificate, error) {
	return nil, nil
}

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

func pickFreeTCPPort(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen free tcp port: %v", err)
	}
	defer ln.Close()
	return strconv.Itoa(ln.Addr().(*net.TCPAddr).Port)
}
