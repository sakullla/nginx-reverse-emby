//go:build integration

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
	"strings"
	"sync"
	"testing"
	"time"

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

func TestModuleConsumesFinalHopDialerForEgressProfiles(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("via-final-hop"))
	}))
	defer backend.Close()

	profileID := 89
	port := pickFreeTCPPort(t)
	unusedProxyPort := pickFreeTCPPort(t)
	finalHop := &recordingFinalHopDialer{}
	registry := module.NewRegistry()
	mustRegister(t, registry, staticProviderModule{name: "certs", provides: module.ProviderTLSMaterial, provider: staticTLSMaterial{}})
	mustRegister(t, registry, staticProviderModule{name: "final-hop", provides: module.ProviderFinalHopDialer, provider: finalHop})
	mustRegister(t, registry, httpmodule.NewModule(httpmodule.Config{}))

	next := model.Snapshot{
		EgressProfiles: []model.EgressProfile{{
			ID:       profileID,
			Name:     "socks-via-final-hop",
			Type:     "socks",
			ProxyURL: "socks5://127.0.0.1:" + unusedProxyPort,
			Enabled:  true,
		}},
		Rules: []model.HTTPRule{{
			ID:              3,
			FrontendURL:     "http://edge.example.test:" + port,
			Backends:        []model.HTTPBackend{{URL: backend.URL}},
			EgressProfileID: &profileID,
			Enabled:         true,
		}},
	}
	if err := registry.Apply(context.Background(), model.Snapshot{}, next); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	assertHTTPBody(t, port, "edge.example.test:"+port, "via-final-hop")

	target, gotProfileID := finalHop.lastTCP()
	if gotProfileID != profileID {
		t.Fatalf("final hop profile id = %d, want %d", gotProfileID, profileID)
	}
	if strings.TrimSpace(target) == "" {
		t.Fatal("final hop target was empty")
	}
}

func TestModuleRollbackRestoresPreviousRuntimeAfterLaterCommitFailure(t *testing.T) {
	oldBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("old-runtime"))
	}))
	defer oldBackend.Close()
	newBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("new-runtime"))
	}))
	defer newBackend.Close()

	port := pickFreeTCPPort(t)
	registry := module.NewRegistry()
	mustRegister(t, registry, staticProviderModule{name: "certs", provides: module.ProviderTLSMaterial, provider: staticTLSMaterial{}})
	mustRegister(t, registry, httpmodule.NewModule(httpmodule.Config{}))
	failer := &commitFailingModule{name: "after-http"}
	mustRegister(t, registry, failer)

	previous := model.Snapshot{Rules: []model.HTTPRule{{
		ID:          5,
		FrontendURL: "http://edge.example.test:" + port,
		Backends:    []model.HTTPBackend{{URL: oldBackend.URL}},
		Enabled:     true,
	}}}
	if err := registry.Apply(context.Background(), model.Snapshot{}, previous); err != nil {
		t.Fatalf("initial Apply() error = %v", err)
	}
	assertHTTPBody(t, port, "edge.example.test:"+port, "old-runtime")

	failer.failCommit = true
	next := model.Snapshot{Rules: []model.HTTPRule{{
		ID:          5,
		FrontendURL: "http://edge.example.test:" + port,
		Backends:    []model.HTTPBackend{{URL: newBackend.URL}},
		Enabled:     true,
	}}}
	if err := registry.Apply(context.Background(), previous, next); err == nil {
		t.Fatal("Apply() error = nil, want later commit failure")
	}
	assertHTTPBody(t, port, "edge.example.test:"+port, "old-runtime")
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

type commitFailingModule struct {
	name       string
	failCommit bool
}

func (m *commitFailingModule) Name() string { return m.name }

func (m *commitFailingModule) Descriptor() module.ModuleDescriptor {
	return module.ModuleDescriptor{Name: m.name}
}

func (*commitFailingModule) RegisterProviders(module.ProviderRegistry) error { return nil }
func (*commitFailingModule) Capabilities(module.SnapshotView) []module.Capability {
	return nil
}
func (*commitFailingModule) Apply(context.Context, module.ApplyRequest) error { return nil }
func (*commitFailingModule) Stop(context.Context) error                       { return nil }

func (m *commitFailingModule) Prepare(context.Context, module.ApplyRequest) (module.ModuleTransaction, error) {
	return module.TransactionFuncs{
		CommitFunc: func() error {
			if m.failCommit {
				return fmt.Errorf("synthetic commit failure")
			}
			return nil
		},
	}, nil
}

func mustRegister(t *testing.T, registry *module.Registry, mod module.Module) {
	t.Helper()
	if err := registry.Register(mod); err != nil {
		t.Fatalf("Register(%s) error = %v", mod.Name(), err)
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

type recordingFinalHopDialer struct {
	mu        sync.Mutex
	tcpTarget string
	tcpID     int
}

func (d *recordingFinalHopDialer) DialTCP(ctx context.Context, target string, id *int) (net.Conn, error) {
	var profileID int
	if id != nil {
		profileID = *id
	}
	d.mu.Lock()
	d.tcpTarget = target
	d.tcpID = profileID
	d.mu.Unlock()

	var dialer net.Dialer
	return dialer.DialContext(ctx, "tcp", target)
}

func (*recordingFinalHopDialer) OpenUDP(context.Context, string, *int) (module.UDPPeer, error) {
	return nil, fmt.Errorf("unexpected OpenUDP")
}

func (d *recordingFinalHopDialer) lastTCP() (string, int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.tcpTarget, d.tcpID
}
