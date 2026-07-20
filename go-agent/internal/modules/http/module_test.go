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
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
	moduleegress "github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/egress"
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

func TestModuleConsumesPendingEgressFinalHopDialerDuringRegistryApply(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("via-pending-final-hop"))
	}))
	defer backend.Close()

	profileID := 90
	port := pickFreeTCPPort(t)
	registry := module.NewRegistry()
	mustRegister(t, registry, staticProviderModule{name: "certs", provides: module.ProviderTLSMaterial, provider: staticTLSMaterial{}})
	mustRegister(t, registry, moduleegress.NewModule(nil))
	mustRegister(t, registry, httpmodule.NewModule(httpmodule.Config{}))

	next := model.Snapshot{
		EgressProfiles: []model.EgressProfile{{
			ID:      profileID,
			Name:    "pending-direct-final-hop",
			Type:    "direct",
			Enabled: true,
		}},
		Rules: []model.HTTPRule{{
			ID:              4,
			FrontendURL:     "http://edge.example.test:" + port,
			Backends:        []model.HTTPBackend{{URL: backend.URL}},
			EgressProfileID: &profileID,
			Enabled:         true,
		}},
	}
	if err := registry.Apply(context.Background(), model.Snapshot{}, next); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	assertHTTPBody(t, port, "edge.example.test:"+port, "via-pending-final-hop")
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

func TestModuleRollbackRestoresPreviousRuntimeAfterDeletingAllRules(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("still-here"))
	}))
	defer backend.Close()

	port := pickFreeTCPPort(t)
	registry := module.NewRegistry()
	mustRegister(t, registry, staticProviderModule{name: "certs", provides: module.ProviderTLSMaterial, provider: staticTLSMaterial{}})
	mustRegister(t, registry, httpmodule.NewModule(httpmodule.Config{}))
	failer := &commitFailingModule{name: "after-http"}
	mustRegister(t, registry, failer)

	previous := model.Snapshot{Rules: []model.HTTPRule{{
		ID:          6,
		FrontendURL: "http://edge.example.test:" + port,
		Backends:    []model.HTTPBackend{{URL: backend.URL}},
		Enabled:     true,
	}}}
	if err := registry.Apply(context.Background(), model.Snapshot{}, previous); err != nil {
		t.Fatalf("initial Apply() error = %v", err)
	}
	assertHTTPBody(t, port, "edge.example.test:"+port, "still-here")

	failer.failCommit = true
	if err := registry.Apply(context.Background(), previous, model.Snapshot{}); err == nil {
		t.Fatal("Apply() error = nil, want later commit failure")
	}
	assertHTTPBody(t, port, "edge.example.test:"+port, "still-here")
}

func TestModuleRollbackRestoresPreviousProviderStateAfterLaterCommitFailure(t *testing.T) {
	oldBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("old-provider"))
	}))
	defer oldBackend.Close()
	newBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("new-provider"))
	}))
	defer newBackend.Close()

	profileID := 91
	port := pickFreeTCPPort(t)
	unusedBackend := httptest.NewServer(http.NotFoundHandler())
	unusedBackend.Close()
	registry := module.NewRegistry()
	mustRegister(t, registry, staticProviderModule{name: "certs", provides: module.ProviderTLSMaterial, provider: staticTLSMaterial{}})
	mustRegister(t, registry, moduleegress.NewModule(nil))
	mustRegister(t, registry, httpmodule.NewModule(httpmodule.Config{}))
	failer := &commitFailingModule{name: "after-http"}
	mustRegister(t, registry, failer)

	previous := model.Snapshot{
		EgressProfiles: []model.EgressProfile{{
			ID:       profileID,
			Name:     "old-socks",
			Type:     "socks",
			ProxyURL: startForwardingSOCKS5Proxy(t, oldBackend.URL),
			Enabled:  true,
		}},
		Rules: []model.HTTPRule{{
			ID:              7,
			FrontendURL:     "http://edge.example.test:" + port,
			Backends:        []model.HTTPBackend{{URL: unusedBackend.URL}},
			EgressProfileID: &profileID,
			Enabled:         true,
		}},
	}
	if err := registry.Apply(context.Background(), model.Snapshot{}, previous); err != nil {
		t.Fatalf("initial Apply() error = %v", err)
	}
	assertHTTPBody(t, port, "edge.example.test:"+port, "old-provider")

	next := previous
	next.EgressProfiles = []model.EgressProfile{{
		ID:       profileID,
		Name:     "new-socks",
		Type:     "socks",
		ProxyURL: startForwardingSOCKS5Proxy(t, newBackend.URL),
		Enabled:  true,
	}}
	failer.failCommit = true
	if err := registry.Apply(context.Background(), previous, next); err == nil {
		t.Fatal("Apply() error = nil, want later commit failure")
	}
	assertHTTPBody(t, port, "edge.example.test:"+port, "old-provider")
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

func startForwardingSOCKS5Proxy(t *testing.T, backendURL string) string {
	t.Helper()
	parsed, err := url.Parse(backendURL)
	if err != nil {
		t.Fatalf("parse backend URL: %v", err)
	}
	target := parsed.Host
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen socks proxy: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go handleForwardingSOCKS5Conn(conn, target)
		}
	}()
	return "socks5://" + ln.Addr().String()
}

func handleForwardingSOCKS5Conn(client net.Conn, target string) {
	defer client.Close()
	if err := readSOCKS5Greeting(client); err != nil {
		return
	}
	if _, err := client.Write([]byte{0x05, 0x00}); err != nil {
		return
	}
	if err := readSOCKS5ConnectRequest(client); err != nil {
		return
	}
	upstream, err := net.Dial("tcp", target)
	if err != nil {
		_, _ = client.Write([]byte{0x05, 0x01, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
		return
	}
	defer upstream.Close()
	if _, err := client.Write([]byte{0x05, 0x00, 0x00, 0x01, 0, 0, 0, 0, 0, 0}); err != nil {
		return
	}
	errCh := make(chan error, 2)
	go func() {
		_, err := io.Copy(upstream, client)
		errCh <- err
	}()
	go func() {
		_, err := io.Copy(client, upstream)
		errCh <- err
	}()
	<-errCh
}

func readSOCKS5Greeting(conn net.Conn) error {
	header := make([]byte, 2)
	if _, err := io.ReadFull(conn, header); err != nil {
		return err
	}
	if header[0] != 0x05 {
		return fmt.Errorf("unsupported socks version %d", header[0])
	}
	methods := make([]byte, int(header[1]))
	_, err := io.ReadFull(conn, methods)
	return err
}

func readSOCKS5ConnectRequest(conn net.Conn) error {
	header := make([]byte, 4)
	if _, err := io.ReadFull(conn, header); err != nil {
		return err
	}
	if header[0] != 0x05 || header[1] != 0x01 {
		return fmt.Errorf("unsupported socks request")
	}
	switch header[3] {
	case 0x01:
		_, err := io.ReadFull(conn, make([]byte, net.IPv4len+2))
		return err
	case 0x03:
		var length [1]byte
		if _, err := io.ReadFull(conn, length[:]); err != nil {
			return err
		}
		_, err := io.ReadFull(conn, make([]byte, int(length[0])+2))
		return err
	case 0x04:
		_, err := io.ReadFull(conn, make([]byte, net.IPv6len+2))
		return err
	default:
		return fmt.Errorf("unsupported socks address type %d", header[3])
	}
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
