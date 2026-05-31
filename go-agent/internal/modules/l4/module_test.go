package l4_test

import (
	"context"
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
	l4module "github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/l4"
)

func TestModuleAppliesL4RuleAndUsesFinalHopDialer(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("via-final-hop"))
	}))
	defer backend.Close()

	profileID := 42
	port := pickFreeTCPPort(t)
	listenPort, err := strconv.Atoi(port)
	if err != nil {
		t.Fatalf("parse port %q: %v", port, err)
	}
	finalHop := &recordingFinalHopDialer{backendTarget: backend.Listener.Addr().String()}

	mod := l4module.NewModule(l4module.Config{})
	registry := module.NewRegistry()
	mustRegister(t, registry, staticProviderModule{name: "egress", provides: module.ProviderEgressResolver, provider: moduleegress.NewResolver([]model.EgressProfile{{
		ID:      profileID,
		Name:    "final-hop",
		Type:    "direct",
		Enabled: true,
	}})})
	mustRegister(t, registry, staticProviderModule{name: "final-hop", provides: module.ProviderFinalHopDialer, provider: finalHop})
	mustRegister(t, registry, mod)

	next := model.Snapshot{
		EgressProfiles: []model.EgressProfile{{
			ID:      profileID,
			Name:    "final-hop",
			Type:    "direct",
			Enabled: true,
		}},
		L4Rules: []model.L4Rule{{
			ID:              1,
			Protocol:        "tcp",
			ListenHost:      "127.0.0.1",
			ListenPort:      listenPort,
			Backends:        []model.L4Backend{{Host: "127.0.0.1", Port: 1}},
			EgressProfileID: &profileID,
			Enabled:         true,
		}},
	}
	if err := registry.Apply(context.Background(), model.Snapshot{}, next); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if _, ok := registry.Resolve(module.ProviderDiagnosticsL4Source); !ok {
		t.Fatal("diagnostics.l4.source provider missing")
	}
	assertTCPBody(t, port, "127.0.0.1:"+port, "via-final-hop")
	if got := finalHop.calls(); got != 1 {
		t.Fatalf("final-hop dial calls = %d, want 1", got)
	}
}

func assertTCPBody(t *testing.T, port string, host string, want string) {
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
	t.Fatalf("timed out waiting for TCP body %q: %v", want, lastErr)
}

type recordingFinalHopDialer struct {
	mu            sync.Mutex
	tcpCalls      int
	backendTarget string
}

func (d *recordingFinalHopDialer) DialTCP(ctx context.Context, target string, _ *int) (net.Conn, error) {
	d.mu.Lock()
	d.tcpCalls++
	backend := d.backendTarget
	d.mu.Unlock()
	if backend == "" {
		backend = target
	}
	var dialer net.Dialer
	return dialer.DialContext(ctx, "tcp", backend)
}

func (d *recordingFinalHopDialer) OpenUDP(context.Context, string, *int) (module.UDPPeer, error) {
	return nil, fmt.Errorf("unexpected UDP final-hop")
}

func (d *recordingFinalHopDialer) calls() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.tcpCalls
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
