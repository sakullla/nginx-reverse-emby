package app

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/backends"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/l4"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/relay"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/wireguard"
)

type l4RuntimeManager struct {
	mu                 sync.Mutex
	server             *l4.Server
	cache              *backends.Cache
	provider           relay.TLSMaterialProvider
	wireGuardRuntime   *sharedWireGuardRuntime
	wireGuardProvider  relay.WireGuardRuntimeProvider
	ownsWireGuard      bool
	blockState         l4TrafficBlockStateValue
	lastRules          []model.L4Rule
	lastRelayListeners []model.RelayListener
}

func newL4RuntimeManager() *l4RuntimeManager {
	return newL4RuntimeManagerWithConfig(Config{})
}

func newL4RuntimeManagerWithRelay(provider relay.TLSMaterialProvider) *l4RuntimeManager {
	return newL4RuntimeManagerWithRelayAndConfig(provider, Config{})
}

func newL4RuntimeManagerWithConfig(cfg Config) *l4RuntimeManager {
	return newL4RuntimeManagerWithRelayAndConfig(nil, cfg)
}

func newL4RuntimeManagerWithRelayAndConfig(provider relay.TLSMaterialProvider, cfg Config) *l4RuntimeManager {
	return newL4RuntimeManagerWithRelayConfigAndWireGuard(provider, cfg, newSharedWireGuardRuntime(), true)
}

func newL4RuntimeManagerWithRelayConfigAndWireGuard(provider relay.TLSMaterialProvider, cfg Config, wireGuardRuntime *sharedWireGuardRuntime, ownsWireGuard ...bool) *l4RuntimeManager {
	if wireGuardRuntime == nil {
		wireGuardRuntime = newSharedWireGuardRuntime()
	}
	owns := len(ownsWireGuard) > 0 && ownsWireGuard[0]
	return &l4RuntimeManager{
		cache:             backends.NewCache(backendCacheConfigFromAppConfig(cfg)),
		provider:          provider,
		wireGuardRuntime:  wireGuardRuntime,
		wireGuardProvider: wireGuardRuntime.provider(),
		ownsWireGuard:     owns,
	}
}

func newL4RuntimeManagerWithWireGuardFactory(factory wireguard.Factory) *l4RuntimeManager {
	wireGuardRuntime := newSharedWireGuardRuntimeWithFactory(factory)
	return &l4RuntimeManager{
		cache:             backends.NewCache(backends.Config{}),
		wireGuardRuntime:  wireGuardRuntime,
		wireGuardProvider: wireGuardRuntime.provider(),
		ownsWireGuard:     true,
	}
}

func (m *l4RuntimeManager) Apply(ctx context.Context, rules []model.L4Rule) error {
	return m.ApplyWithRelayAndWireGuardProfiles(ctx, rules, nil, nil)
}

func (m *l4RuntimeManager) ApplyWithRelay(ctx context.Context, rules []model.L4Rule, relayListeners []model.RelayListener) error {
	return m.ApplyWithRelayAndWireGuardProfiles(ctx, rules, relayListeners, nil)
}

func (m *l4RuntimeManager) ApplyWithRelayAndWireGuardProfiles(
	ctx context.Context,
	rules []model.L4Rule,
	relayListeners []model.RelayListener,
	wireGuardProfiles []model.WireGuardProfile,
) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(rules) == 0 {
		if err := m.applyWireGuardProfilesLocked(ctx, wireGuardProfiles); err != nil {
			return err
		}
		if m.server != nil {
			_ = m.server.Close()
			m.server = nil
		}
		m.storeLastAppliedInputsLocked(nil, nil)
		return nil
	}
	if err := validateL4Rules(rules, relayListeners, m.provider); err != nil {
		return err
	}
	transaction, provider, err := m.prepareWireGuardProfilesLocked(ctx, wireGuardProfiles)
	if err != nil {
		return err
	}
	if transaction != nil {
		defer func() {
			if transaction != nil {
				transaction.Rollback()
			}
		}()
	}
	if err := m.validateWireGuardReferencesLocked(rules, provider); err != nil {
		if restoreErr := m.rollbackWireGuardAndRestorePreviousServerLocked(ctx, &transaction); restoreErr != nil {
			return fmt.Errorf("%w; restore failed: %v", err, restoreErr)
		}
		return err
	}

	previous := m.server
	if previous != nil {
		server, err := l4.NewServerWithResourcesAndWireGuardProvider(ctx, rules, relayListeners, m.provider, m.cache, provider)
		if err == nil {
			server.SetTrafficBlockState(m.currentTrafficBlockState())
			if transaction != nil {
				transaction.Commit()
				transaction = nil
			}
			_ = previous.Close()
			m.server = server
			m.storeLastAppliedInputsLocked(rules, relayListeners)
			return nil
		}
		if !bindingKeysOverlap(l4ServerBindingKeys(previous), l4RuleBindingKeys(rules)) || !isRuntimeBindConflict(err) {
			if restoreErr := m.rollbackWireGuardAndRestorePreviousServerLocked(ctx, &transaction); restoreErr != nil {
				return fmt.Errorf("%w; restore failed: %v", err, restoreErr)
			}
			return err
		}

		_ = previous.Close()
		m.server = nil
	}
	server, err := retryRuntimeBindConflict(ctx, func() (*l4.Server, error) {
		return l4.NewServerWithResourcesAndWireGuardProvider(ctx, rules, relayListeners, m.provider, m.cache, provider)
	})
	if err != nil && previous != nil && m.canRecreateWireGuardRuntimeForBindConflict(err, rules, wireGuardProfiles) {
		if transaction != nil {
			transaction.Rollback()
			transaction = nil
		}
		if recreateErr := m.wireGuardRuntime.Recreate(ctx, wireGuardProfiles); recreateErr != nil {
			err = fmt.Errorf("%w; wireguard runtime recreate failed: %v", err, recreateErr)
		} else {
			provider = m.wireGuardProvider
			server, err = retryRuntimeBindConflict(ctx, func() (*l4.Server, error) {
				return l4.NewServerWithResourcesAndWireGuardProvider(ctx, rules, relayListeners, m.provider, m.cache, provider)
			})
		}
	}
	if err != nil {
		if previous != nil {
			if restoreErr := m.rollbackWireGuardAndRestorePreviousServerLocked(ctx, &transaction); restoreErr != nil {
				return fmt.Errorf("%w; restore failed: %v", err, restoreErr)
			}
		}
		return err
	}
	server.SetTrafficBlockState(m.currentTrafficBlockState())
	if transaction != nil {
		transaction.Commit()
		transaction = nil
	}
	m.server = server
	m.storeLastAppliedInputsLocked(rules, relayListeners)
	return nil
}

func (m *l4RuntimeManager) canRecreateWireGuardRuntimeForBindConflict(err error, rules []model.L4Rule, profiles []model.WireGuardProfile) bool {
	if m.wireGuardRuntime == nil || len(profiles) == 0 || !isRuntimeBindConflict(err) {
		return false
	}
	for _, rule := range rules {
		if strings.EqualFold(strings.TrimSpace(rule.ListenMode), "wireguard") {
			return true
		}
	}
	return false
}

func (m *l4RuntimeManager) rollbackWireGuardAndRestorePreviousServerLocked(ctx context.Context, transaction **wireguard.Transaction) error {
	if transaction != nil && *transaction != nil {
		(*transaction).Rollback()
		*transaction = nil
	}
	return m.restorePreviousServerLocked(ctx)
}

func (m *l4RuntimeManager) restorePreviousServerLocked(ctx context.Context) error {
	if len(m.lastRules) == 0 {
		m.server = nil
		return nil
	}
	server, err := retryRuntimeBindConflict(ctx, func() (*l4.Server, error) {
		return l4.NewServerWithResourcesAndWireGuardProvider(ctx, m.lastRules, m.lastRelayListeners, m.provider, m.cache, m.wireGuardProvider)
	})
	if err != nil {
		if m.server != nil && isRuntimeBindConflict(err) {
			return nil
		}
		return err
	}
	server.SetTrafficBlockState(m.currentTrafficBlockState())
	abandoned := m.server
	m.server = server
	if abandoned != nil {
		_ = abandoned.Close()
	}
	return nil
}

func (m *l4RuntimeManager) storeLastAppliedInputsLocked(rules []model.L4Rule, relayListeners []model.RelayListener) {
	m.lastRules = cloneL4Rules(rules)
	m.lastRelayListeners = cloneRelayListeners(relayListeners)
}

func l4ServerBindingKeys(server *l4.Server) []string {
	if server == nil {
		return nil
	}
	return server.BindingKeys()
}

func l4RuleBindingKeys(rules []model.L4Rule) []string {
	keys := make([]string, 0, len(rules))
	for _, rule := range rules {
		if strings.EqualFold(strings.TrimSpace(rule.Protocol), "udp") {
			keys = append(keys, "udp:"+l4RuleListenAddress(rule))
			continue
		}
		keys = append(keys, "tcp:"+l4RuleListenAddress(rule))
	}
	return keys
}

func l4RuleListenAddress(rule model.L4Rule) string {
	host := rule.ListenHost
	if strings.EqualFold(strings.TrimSpace(rule.ListenMode), "wireguard") && strings.TrimSpace(rule.WireGuardListenHost) != "" {
		host = rule.WireGuardListenHost
	}
	return net.JoinHostPort(host, strconv.Itoa(rule.ListenPort))
}

func (m *l4RuntimeManager) applyWireGuardProfilesLocked(ctx context.Context, profiles []model.WireGuardProfile) error {
	if m.wireGuardRuntime == nil || profiles == nil {
		return nil
	}
	return m.wireGuardRuntime.Apply(ctx, profiles)
}

func (m *l4RuntimeManager) prepareWireGuardProfilesLocked(ctx context.Context, profiles []model.WireGuardProfile) (*wireguard.Transaction, relay.WireGuardRuntimeProvider, error) {
	if m.wireGuardRuntime == nil || profiles == nil {
		return nil, m.wireGuardProvider, nil
	}
	transaction, err := m.wireGuardRuntime.Prepare(ctx, profiles)
	if err != nil {
		return nil, nil, err
	}
	if transaction == nil {
		return nil, m.wireGuardProvider, nil
	}
	return transaction, wireGuardTransactionProvider{transaction: transaction}, nil
}

func (m *l4RuntimeManager) validateWireGuardReferencesLocked(rules []model.L4Rule, provider relay.WireGuardRuntimeProvider) error {
	for _, rule := range rules {
		if !l4RuleUsesWireGuard(rule) {
			continue
		}
		if rule.WireGuardProfileID == nil || *rule.WireGuardProfileID <= 0 {
			continue
		}
		if provider == nil {
			return fmt.Errorf("wireguard runtime provider is required")
		}
		runtime, ok := provider.WireGuardRuntime(*rule.WireGuardProfileID)
		if !ok || runtime == nil {
			return fmt.Errorf("wireguard profile %d runtime not found", *rule.WireGuardProfileID)
		}
	}
	return nil
}

func (m *l4RuntimeManager) UpdateTrafficBlockState(state l4.TrafficBlockState) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.blockState.Store(state)
	if m.server != nil {
		m.server.SetTrafficBlockState(m.currentTrafficBlockState())
	}
}

func (m *l4RuntimeManager) currentTrafficBlockState() l4.TrafficBlockState {
	return m.blockState.Load()
}

func (m *l4RuntimeManager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	var firstErr error
	if m.server != nil {
		if err := m.server.Close(); err != nil {
			firstErr = err
		}
		m.server = nil
	}
	if m.ownsWireGuard && m.wireGuardRuntime != nil {
		if err := m.wireGuardRuntime.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
