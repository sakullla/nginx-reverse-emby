package l4_test

import (
	"context"
	"errors"
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

func TestModuleRollbackAfterLaterCommitFailureRestoresOverlayProviderState(t *testing.T) {
	profileID := 7
	tests := []struct {
		name string
		rule func() model.L4Rule
		log  func(*statefulOverlayProviderModule) []string
	}{
		{
			name: "address listener",
			rule: func() model.L4Rule {
				port, err := strconv.Atoi(pickFreeTCPPort(t))
				if err != nil {
					t.Fatalf("parse port: %v", err)
				}
				return model.L4Rule{
					ID:                 10,
					Protocol:           "tcp",
					ListenHost:         "127.0.0.1",
					ListenPort:         port,
					ListenMode:         "wireguard",
					WireGuardProfileID: &profileID,
					Backends:           []model.L4Backend{{Host: "127.0.0.1", Port: 1}},
					Enabled:            true,
				}
			},
			log: func(m *statefulOverlayProviderModule) []string { return m.listenLabels() },
		},
		{
			name: "transparent listener",
			rule: func() model.L4Rule {
				return model.L4Rule{
					ID:                   11,
					Protocol:             "tcp",
					ListenHost:           "127.0.0.1",
					ListenPort:           0,
					ListenMode:           "wireguard",
					WireGuardInboundMode: "transparent",
					WireGuardProfileID:   &profileID,
					Enabled:              true,
				}
			},
			log: func(m *statefulOverlayProviderModule) []string { return m.transparentListenLabels() },
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			overlay := &statefulOverlayProviderModule{nextLabel: "previous"}
			l4mod := l4module.NewModule(l4module.Config{})
			failer := &commitFailingModule{name: "after-l4"}
			registry := module.NewRegistry()
			mustRegister(t, registry, overlay)
			mustRegister(t, registry, l4mod)
			mustRegister(t, registry, failer)

			previous := model.Snapshot{L4Rules: []model.L4Rule{tt.rule()}}
			if err := registry.Apply(context.Background(), model.Snapshot{}, previous); err != nil {
				t.Fatalf("Apply(previous) error = %v", err)
			}
			overlay.setNextLabel("next")
			failer.commitErr = errors.New("later commit failed")
			if err := registry.Apply(context.Background(), previous, model.Snapshot{}); err == nil {
				t.Fatal("Apply(next) error = nil, want later commit failure")
			}
			if got := tt.log(overlay); !stringSlicesEqual(got, []string{"previous", "previous"}) {
				t.Fatalf("listener provider labels = %v, want [previous previous]", got)
			}
			if got := overlay.restoreCalls(); got == 0 {
				t.Fatal("overlay RestorePreviousRuntimeForRollback calls = 0, want at least 1")
			}
		})
	}
}

func TestModuleRollbackAfterLaterCommitFailurePreservesCustomFinalHopProvider(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("previous-final-hop"))
	}))
	defer backend.Close()

	profileID := 17
	port, err := strconv.Atoi(pickFreeTCPPort(t))
	if err != nil {
		t.Fatalf("parse port: %v", err)
	}
	previousDialer := &recordingFinalHopDialer{backendTarget: backend.Listener.Addr().String()}
	nextDialer := &recordingFinalHopDialer{backendTarget: "127.0.0.1:1"}
	finalHop := &switchingFinalHopProviderModule{next: previousDialer}
	l4mod := l4module.NewModule(l4module.Config{})
	failer := &commitFailingModule{name: "after-l4"}
	registry := module.NewRegistry()
	mustRegister(t, registry, finalHop)
	mustRegister(t, registry, l4mod)
	mustRegister(t, registry, failer)

	previous := model.Snapshot{
		EgressProfiles: []model.EgressProfile{{
			ID:      profileID,
			Name:    "custom",
			Type:    "direct",
			Enabled: true,
		}},
		L4Rules: []model.L4Rule{{
			ID:              20,
			Protocol:        "tcp",
			ListenHost:      "127.0.0.1",
			ListenPort:      port,
			Backends:        []model.L4Backend{{Host: "127.0.0.1", Port: 1}},
			EgressProfileID: &profileID,
			Enabled:         true,
		}},
	}
	if err := registry.Apply(context.Background(), model.Snapshot{}, previous); err != nil {
		t.Fatalf("Apply(previous) error = %v", err)
	}
	finalHop.setNext(nextDialer)
	failer.commitErr = errors.New("later commit failed")
	if err := registry.Apply(context.Background(), previous, model.Snapshot{}); err == nil {
		t.Fatal("Apply(next) error = nil, want later commit failure")
	}
	assertTCPBody(t, strconv.Itoa(port), "127.0.0.1:"+strconv.Itoa(port), "previous-final-hop")
	if got := previousDialer.calls(); got != 1 {
		t.Fatalf("previous final-hop calls = %d, want 1", got)
	}
	if got := nextDialer.calls(); got != 0 {
		t.Fatalf("next final-hop calls = %d, want 0", got)
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

type statefulOverlayProviderModule struct {
	mu               sync.Mutex
	currentLabel     string
	pendingLabel     string
	rollbackLabel    string
	nextLabel        string
	listenLog        []string
	transparentLog   []string
	restoreCallCount int
}

func (m *statefulOverlayProviderModule) Name() string { return "overlay" }

func (m *statefulOverlayProviderModule) Descriptor() module.ModuleDescriptor {
	return module.ModuleDescriptor{
		Name:     m.Name(),
		Provides: []module.ProviderRef{module.ProviderOverlayRuntime, module.ProviderTransparentListener},
	}
}

func (m *statefulOverlayProviderModule) RegisterProviders(reg module.ProviderRegistry) error {
	if err := reg.Provide(module.ProviderOverlayRuntime, m); err != nil {
		return err
	}
	return reg.Provide(module.ProviderTransparentListener, m)
}

func (m *statefulOverlayProviderModule) Capabilities(module.SnapshotView) []module.Capability {
	return nil
}

func (m *statefulOverlayProviderModule) Apply(context.Context, module.ApplyRequest) error {
	return nil
}

func (m *statefulOverlayProviderModule) Stop(context.Context) error { return nil }

func (m *statefulOverlayProviderModule) Prepare(context.Context, module.ApplyRequest) (module.ModuleTransaction, error) {
	m.mu.Lock()
	m.rollbackLabel = m.currentLabel
	m.pendingLabel = m.nextLabel
	m.mu.Unlock()
	return overlayProviderTransaction{module: m}, nil
}

func (m *statefulOverlayProviderModule) setNextLabel(label string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nextLabel = label
}

func (m *statefulOverlayProviderModule) activeLabel() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if strings.TrimSpace(m.pendingLabel) != "" {
		return m.pendingLabel
	}
	return m.currentLabel
}

func (m *statefulOverlayProviderModule) recordListen(label string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.listenLog = append(m.listenLog, label)
}

func (m *statefulOverlayProviderModule) recordTransparentListen(label string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.transparentLog = append(m.transparentLog, label)
}

func (m *statefulOverlayProviderModule) listenLabels() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.listenLog...)
}

func (m *statefulOverlayProviderModule) transparentListenLabels() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.transparentLog...)
}

func (m *statefulOverlayProviderModule) restoreCalls() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.restoreCallCount
}

func (m *statefulOverlayProviderModule) RestorePreviousRuntimeForRollback(context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.currentLabel = m.rollbackLabel
	m.pendingLabel = ""
	m.restoreCallCount++
	return nil
}

func (m *statefulOverlayProviderModule) DialContext(context.Context, string, int, string, string) (net.Conn, error) {
	return nil, fmt.Errorf("unexpected overlay dial")
}

func (m *statefulOverlayProviderModule) ListenTCP(_ context.Context, _ string, _ int, address string) (net.Listener, error) {
	label := m.activeLabel()
	ln, err := net.Listen("tcp", address)
	if err != nil {
		return nil, err
	}
	m.recordListen(label)
	return ln, nil
}

func (m *statefulOverlayProviderModule) ListenUDP(context.Context, string, int, string) (net.PacketConn, error) {
	return nil, fmt.Errorf("unexpected overlay udp listen")
}

func (m *statefulOverlayProviderModule) ListenTransparentTCP(context.Context, string, int) (net.Listener, error) {
	label := m.activeLabel()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	m.recordTransparentListen(label)
	return ln, nil
}

func (m *statefulOverlayProviderModule) ListenTransparentUDP(context.Context, string, int, string) (module.TransparentUDPConn, error) {
	return nil, fmt.Errorf("unexpected transparent udp listen")
}

type overlayProviderTransaction struct {
	module *statefulOverlayProviderModule
}

func (tx overlayProviderTransaction) RegisterProviders(reg module.ProviderRegistry) error {
	if err := reg.Provide(module.ProviderOverlayRuntime, tx.module); err != nil {
		return err
	}
	return reg.Provide(module.ProviderTransparentListener, tx.module)
}

func (tx overlayProviderTransaction) Commit() error {
	if tx.module == nil {
		return nil
	}
	tx.module.mu.Lock()
	defer tx.module.mu.Unlock()
	tx.module.currentLabel = tx.module.pendingLabel
	tx.module.pendingLabel = ""
	return nil
}

func (tx overlayProviderTransaction) Rollback() error {
	if tx.module == nil {
		return nil
	}
	tx.module.mu.Lock()
	defer tx.module.mu.Unlock()
	tx.module.currentLabel = tx.module.rollbackLabel
	tx.module.pendingLabel = ""
	return nil
}

type switchingFinalHopProviderModule struct {
	mu       sync.Mutex
	current  relayFinalHopDialer
	next     relayFinalHopDialer
	rollback relayFinalHopDialer
}

type relayFinalHopDialer interface {
	DialTCP(context.Context, string, *int) (net.Conn, error)
	OpenUDP(context.Context, string, *int) (module.UDPPeer, error)
}

func (m *switchingFinalHopProviderModule) Name() string { return "final-hop" }

func (m *switchingFinalHopProviderModule) Descriptor() module.ModuleDescriptor {
	return module.ModuleDescriptor{Name: m.Name(), Provides: []module.ProviderRef{module.ProviderFinalHopDialer}}
}

func (m *switchingFinalHopProviderModule) RegisterProviders(reg module.ProviderRegistry) error {
	return reg.Provide(module.ProviderFinalHopDialer, m.current)
}

func (m *switchingFinalHopProviderModule) Capabilities(module.SnapshotView) []module.Capability {
	return nil
}

func (m *switchingFinalHopProviderModule) Apply(context.Context, module.ApplyRequest) error {
	return nil
}

func (m *switchingFinalHopProviderModule) Stop(context.Context) error { return nil }

func (m *switchingFinalHopProviderModule) Prepare(context.Context, module.ApplyRequest) (module.ModuleTransaction, error) {
	m.mu.Lock()
	m.rollback = m.current
	next := m.next
	m.mu.Unlock()
	return finalHopProviderTransaction{module: m, provider: next}, nil
}

func (m *switchingFinalHopProviderModule) setNext(provider relayFinalHopDialer) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.next = provider
}

type finalHopProviderTransaction struct {
	module   *switchingFinalHopProviderModule
	provider relayFinalHopDialer
}

func (tx finalHopProviderTransaction) RegisterProviders(reg module.ProviderRegistry) error {
	return reg.Provide(module.ProviderFinalHopDialer, tx.provider)
}

func (tx finalHopProviderTransaction) Commit() error {
	if tx.module == nil {
		return nil
	}
	tx.module.mu.Lock()
	defer tx.module.mu.Unlock()
	tx.module.current = tx.provider
	return nil
}

func (tx finalHopProviderTransaction) Rollback() error {
	if tx.module == nil {
		return nil
	}
	tx.module.mu.Lock()
	defer tx.module.mu.Unlock()
	tx.module.current = tx.module.rollback
	return nil
}

type commitFailingModule struct {
	name      string
	commitErr error
}

func (m *commitFailingModule) Name() string { return m.name }

func (m *commitFailingModule) Descriptor() module.ModuleDescriptor {
	return module.ModuleDescriptor{Name: m.name}
}

func (m *commitFailingModule) RegisterProviders(module.ProviderRegistry) error { return nil }
func (m *commitFailingModule) Capabilities(module.SnapshotView) []module.Capability {
	return nil
}
func (m *commitFailingModule) Apply(context.Context, module.ApplyRequest) error { return nil }
func (m *commitFailingModule) Stop(context.Context) error                       { return nil }

func (m *commitFailingModule) Prepare(context.Context, module.ApplyRequest) (module.ModuleTransaction, error) {
	return module.TransactionFuncs{
		CommitFunc: func() error { return m.commitErr },
	}, nil
}

func stringSlicesEqual(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
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
