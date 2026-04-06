package app

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/l4"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/proxy"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/relay"
)

type L4Applier interface {
	Apply(context.Context, []model.L4Rule) error
	Close() error
}

type RelayApplier interface {
	Apply(context.Context, []model.RelayListener) error
	Close() error
}

type httpRuntimeManager struct {
	mu      sync.Mutex
	runtime *proxy.Runtime
}

func newHTTPRuntimeManager() *httpRuntimeManager {
	return &httpRuntimeManager{}
}

func (m *httpRuntimeManager) Apply(ctx context.Context, rules []model.HTTPRule) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(rules) == 0 {
		if m.runtime != nil {
			_ = m.runtime.Close()
			m.runtime = nil
		}
		return nil
	}
	bindings, err := proxy.BindingKeys(rules)
	if err != nil {
		return err
	}

	previous := m.runtime
	if previous != nil && !httpBindingsOverlap(previous.BindingKeys(), bindings) {
		runtime, err := proxy.Start(ctx, rules)
		if err != nil {
			return err
		}
		_ = previous.Close()
		m.runtime = runtime
		return nil
	}
	if previous != nil {
		_ = previous.Close()
		m.runtime = nil
	}

	runtime, err := proxy.Start(ctx, rules)
	if err != nil {
		return err
	}
	m.runtime = runtime
	return nil
}

func (m *httpRuntimeManager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.runtime == nil {
		return nil
	}
	err := m.runtime.Close()
	m.runtime = nil
	return err
}

type l4RuntimeManager struct {
	mu     sync.Mutex
	server *l4.Server
}

func newL4RuntimeManager() *l4RuntimeManager {
	return &l4RuntimeManager{}
}

func (m *l4RuntimeManager) Apply(ctx context.Context, rules []model.L4Rule) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(rules) == 0 {
		if m.server != nil {
			_ = m.server.Close()
			m.server = nil
		}
		return nil
	}
	if err := validateL4Rules(rules); err != nil {
		return err
	}

	previous := m.server
	if previous != nil {
		_ = previous.Close()
		m.server = nil
	}

	server, err := l4.NewServer(ctx, rules)
	if err != nil {
		return err
	}
	m.server = server
	return nil
}

func (m *l4RuntimeManager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.server == nil {
		return nil
	}
	err := m.server.Close()
	m.server = nil
	return err
}

type relayRuntimeManager struct {
	mu       sync.Mutex
	server   *relay.Server
	provider relay.TLSMaterialProvider
}

func newRelayRuntimeManager(provider relay.TLSMaterialProvider) *relayRuntimeManager {
	return &relayRuntimeManager{provider: provider}
}

func (m *relayRuntimeManager) Apply(ctx context.Context, listeners []model.RelayListener) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(listeners) == 0 {
		if m.server != nil {
			_ = m.server.Close()
			m.server = nil
		}
		return nil
	}
	if err := validateRelayListeners(ctx, listeners, m.provider); err != nil {
		return err
	}

	previous := m.server
	if previous != nil {
		_ = previous.Close()
		m.server = nil
	}

	server, err := relay.Start(ctx, listeners, m.provider)
	if err != nil {
		return err
	}
	m.server = server
	return nil
}

func (m *relayRuntimeManager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.server == nil {
		return nil
	}
	err := m.server.Close()
	m.server = nil
	return err
}

func validateL4Rules(rules []model.L4Rule) error {
	for _, rule := range rules {
		if err := l4.ValidateRule(rule); err != nil {
			return err
		}
		switch strings.ToLower(rule.Protocol) {
		case "tcp", "udp":
		default:
			return fmt.Errorf("unsupported protocol %q", rule.Protocol)
		}
	}
	return nil
}

func validateRelayListeners(ctx context.Context, listeners []model.RelayListener, provider relay.TLSMaterialProvider) error {
	if provider == nil {
		return fmt.Errorf("tls material provider is required")
	}
	for _, listener := range listeners {
		if !listener.Enabled {
			continue
		}
		if err := relay.ValidateListener(listener); err != nil {
			return fmt.Errorf("relay listener %d: %w", listener.ID, err)
		}
		if listener.CertificateID == nil {
			return fmt.Errorf("relay listener %d: certificate_id is required", listener.ID)
		}
		if _, err := provider.ServerCertificate(ctx, *listener.CertificateID); err != nil {
			return fmt.Errorf("relay listener %d: %w", listener.ID, err)
		}
	}
	return nil
}

func httpBindingsOverlap(left, right []string) bool {
	if len(left) == 0 || len(right) == 0 {
		return false
	}

	seen := make(map[string]struct{}, len(left))
	for _, binding := range left {
		seen[binding] = struct{}{}
	}
	for _, binding := range right {
		if _, ok := seen[binding]; ok {
			return true
		}
	}
	return false
}
