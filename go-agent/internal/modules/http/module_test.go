package http_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
	moduleegress "github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/egress"
	httpmodule "github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/http"
	modulewireguard "github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/wireguard"
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

func TestModuleUsesSnapshotCurrentEgressProfilesDuringRegistryApply(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer backend.Close()

	profileID := 77
	port := pickFreeTCPPort(t)
	registry := module.NewRegistry()
	mustRegister(t, registry, staticProviderModule{name: "certs", provides: module.ProviderTLSMaterial, provider: staticTLSMaterial{}})
	mustRegister(t, registry, moduleegress.NewModule(nil))
	mustRegister(t, registry, httpmodule.NewModule(httpmodule.Config{}))

	next := model.Snapshot{
		EgressProfiles: []model.EgressProfile{{
			ID:      profileID,
			Name:    "direct-now",
			Type:    "direct",
			Enabled: true,
		}},
		Rules: []model.HTTPRule{{
			ID:              1,
			FrontendURL:     "http://edge.example.test:" + port,
			Backends:        []model.HTTPBackend{{URL: backend.URL}},
			EgressProfileID: &profileID,
			Enabled:         true,
		}},
	}
	if err := registry.Apply(context.Background(), model.Snapshot{}, next); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	assertHTTPBody(t, port, "edge.example.test:"+port, "ok")
}

func TestModuleUsesEgressOwnedOverlayForWireGuardEgressProfiles(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("via-wg-egress"))
	}))
	defer backend.Close()

	profileID := 88
	port := pickFreeTCPPort(t)
	factory := &recordingWireGuardFactory{}
	registry := module.NewRegistry()
	mustRegister(t, registry, staticProviderModule{name: "certs", provides: module.ProviderTLSMaterial, provider: staticTLSMaterial{}})
	mustRegister(t, registry, moduleegress.NewModule(factory.Create))
	mustRegister(t, registry, httpmodule.NewModule(httpmodule.Config{}))

	next := model.Snapshot{
		EgressProfiles: []model.EgressProfile{validWireGuardEgressProfile(profileID)},
		Rules: []model.HTTPRule{{
			ID:              2,
			FrontendURL:     "http://edge.example.test:" + port,
			Backends:        []model.HTTPBackend{{URL: backend.URL}},
			EgressProfileID: &profileID,
			RelayLayers:     [][]int{{}},
			Enabled:         true,
		}},
	}
	if err := registry.Apply(context.Background(), model.Snapshot{}, next); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if factory.createdCount() != 1 {
		t.Fatalf("created WireGuard runtimes = %d, want 1", factory.createdCount())
	}
	assertHTTPBody(t, port, "edge.example.test:"+port, "via-wg-egress")
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

func assertHTTPBody(t *testing.T, port string, host string, want string) {
	t.Helper()
	url := "http://127.0.0.1:" + port + "/"
	deadline := time.Now().Add(2 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		req, err := http.NewRequest(http.MethodGet, url, nil)
		if err != nil {
			t.Fatalf("NewRequest() error = %v", err)
		}
		req.Host = host
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			lastErr = err
			time.Sleep(10 * time.Millisecond)
			continue
		}
		body, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if readErr == nil && string(body) == want {
			return
		}
		if readErr != nil {
			lastErr = readErr
		} else {
			lastErr = fmt.Errorf("body %q status %d", string(body), resp.StatusCode)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for HTTP body %q: %v", want, lastErr)
}

func validWireGuardEgressProfile(profileID int) model.EgressProfile {
	return model.EgressProfile{
		ID:      profileID,
		Name:    "wg-egress",
		Type:    "wireguard",
		Enabled: true,
		WireGuardConfig: &model.EgressWireGuardConfig{
			PrivateKey: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=",
			Addresses:  []string{"10.30.0.1/24"},
			DNS:        []string{"1.1.1.1"},
			Peers: []model.WireGuardPeer{{
				Name:       "peer-a",
				PublicKey:  "BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB=",
				Endpoint:   "127.0.0.1:51820",
				AllowedIPs: []string{"10.30.0.2/32"},
			}},
			MTU: 1420,
		},
		Revision: 1,
	}
}

type recordingWireGuardFactory struct {
	mu      sync.Mutex
	created []*recordingWireGuardRuntime
}

func (f *recordingWireGuardFactory) Create(_ context.Context, cfg modulewireguard.Config) (modulewireguard.RuntimeHandle, error) {
	runtime := &recordingWireGuardRuntime{profileID: cfg.ID}
	f.mu.Lock()
	f.created = append(f.created, runtime)
	f.mu.Unlock()
	return runtime, nil
}

func (f *recordingWireGuardFactory) createdCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.created)
}

type recordingWireGuardRuntime struct {
	profileID int
}

func (r *recordingWireGuardRuntime) DialContext(ctx context.Context, network string, address string) (net.Conn, error) {
	var dialer net.Dialer
	return dialer.DialContext(ctx, network, address)
}

func (*recordingWireGuardRuntime) ListenTCP(context.Context, string) (net.Listener, error) {
	return nil, fmt.Errorf("unexpected ListenTCP")
}

func (*recordingWireGuardRuntime) ListenTransparentTCP(context.Context) (net.Listener, error) {
	return nil, fmt.Errorf("unexpected ListenTransparentTCP")
}

func (*recordingWireGuardRuntime) ListenUDP(context.Context, string) (net.PacketConn, error) {
	return nil, fmt.Errorf("unexpected ListenUDP")
}

func (*recordingWireGuardRuntime) ListenTransparentUDP(context.Context, string) (modulewireguard.TransparentUDPConn, error) {
	return nil, fmt.Errorf("unexpected ListenTransparentUDP")
}

func (*recordingWireGuardRuntime) Close() error {
	return nil
}
