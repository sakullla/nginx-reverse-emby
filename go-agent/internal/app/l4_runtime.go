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
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/relay"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/relay/relayroute"
	modulewireguard "github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/wireguard"
)

type l4RuntimeManager struct {
	mu                 sync.Mutex
	server             *l4.Server
	cache              *backends.Cache
	provider           relay.TLSMaterialProvider
	wireGuardRuntime   *modulewireguard.Runtime
	egressWireGuard    *egressWireGuardRuntime
	wireGuardProvider  relayWireGuardProvider
	ownsWireGuard      bool
	localAgentID       string
	blockState         l4TrafficBlockStateValue
	lastRules          []model.L4Rule
	lastRelayListeners []model.RelayListener
	lastEgressProfiles []model.EgressProfile
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

func newL4RuntimeManagerWithRelayConfigAndWireGuard(provider relay.TLSMaterialProvider, cfg Config, wireGuardRuntime *modulewireguard.Runtime, ownsWireGuard ...bool) *l4RuntimeManager {
	if wireGuardRuntime == nil {
		wireGuardRuntime = newSharedWireGuardRuntime()
	}
	owns := len(ownsWireGuard) > 0 && ownsWireGuard[0]
	return &l4RuntimeManager{
		cache:             backends.NewCache(backendCacheConfigFromAppConfig(cfg)),
		provider:          provider,
		wireGuardRuntime:  wireGuardRuntime,
		egressWireGuard:   newEgressWireGuardRuntime(nil),
		wireGuardProvider: newWireGuardRuntimeProvider(wireGuardRuntime, cfg.AgentID),
		ownsWireGuard:     owns,
		localAgentID:      strings.TrimSpace(cfg.AgentID),
	}
}

func newL4RuntimeManagerWithWireGuardFactory(factory modulewireguard.Factory) *l4RuntimeManager {
	wireGuardRuntime := newSharedWireGuardRuntimeWithFactory(factory)
	return &l4RuntimeManager{
		cache:             backends.NewCache(backends.Config{}),
		wireGuardRuntime:  wireGuardRuntime,
		egressWireGuard:   newEgressWireGuardRuntime(factory),
		wireGuardProvider: newWireGuardRuntimeProvider(wireGuardRuntime, ""),
		ownsWireGuard:     true,
	}
}

func (m *l4RuntimeManager) Apply(ctx context.Context, rules []model.L4Rule) error {
	return m.ApplyWithRelayWireGuardAndEgressProfiles(ctx, rules, nil, nil, nil)
}

func (m *l4RuntimeManager) ApplyWithRelay(ctx context.Context, rules []model.L4Rule, relayListeners []model.RelayListener) error {
	return m.ApplyWithRelayWireGuardAndEgressProfiles(ctx, rules, relayListeners, nil, nil)
}

func (m *l4RuntimeManager) ApplyWithRelayAndWireGuardProfiles(
	ctx context.Context,
	rules []model.L4Rule,
	relayListeners []model.RelayListener,
	wireGuardProfiles []model.WireGuardProfile,
) error {
	return m.ApplyWithRelayWireGuardAndEgressProfiles(ctx, rules, relayListeners, wireGuardProfiles, nil)
}

func (m *l4RuntimeManager) ApplyWithRelayWireGuardAndEgressProfiles(
	ctx context.Context,
	rules []model.L4Rule,
	relayListeners []model.RelayListener,
	wireGuardProfiles []model.WireGuardProfile,
	egressProfiles []model.EgressProfile,
) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	localEgressProfiles := localL4EgressProfiles(rules, egressProfiles)
	if len(rules) == 0 {
		if err := m.applyWireGuardProfilesLocked(ctx, wireGuardProfiles); err != nil {
			return err
		}
		if err := m.applyEgressWireGuardProfilesLocked(ctx, localEgressProfiles); err != nil {
			return err
		}
		if m.server != nil {
			_ = m.server.Close()
			m.server = nil
		}
		m.storeLastAppliedInputsLocked(nil, nil, nil)
		return nil
	}
	if err := validateL4Rules(rules, relayListeners, m.provider); err != nil {
		return err
	}
	transaction, provider, err := m.prepareWireGuardProfilesLocked(ctx, wireGuardProfiles)
	if err != nil {
		return err
	}
	egressTransaction, egressProvider, err := m.prepareEgressWireGuardProfilesLocked(ctx, localEgressProfiles)
	if err != nil {
		if transaction != nil {
			transaction.Rollback()
		}
		return err
	}
	if transaction != nil {
		defer func() {
			if transaction != nil {
				transaction.Rollback()
			}
		}()
	}
	if egressTransaction != nil {
		defer func() {
			if egressTransaction != nil {
				egressTransaction.Rollback()
			}
		}()
	}
	if err := m.validateWireGuardReferencesLocked(rules, provider); err != nil {
		if restoreErr := m.rollbackWireGuardAndRestorePreviousServerLocked(ctx, &transaction, &egressTransaction); restoreErr != nil {
			return fmt.Errorf("%w; restore failed: %v", err, restoreErr)
		}
		return err
	}
	if err := validateEgressWireGuardReferences(rules, egressProfiles, egressProvider); err != nil {
		if restoreErr := m.rollbackWireGuardAndRestorePreviousServerLocked(ctx, &transaction, &egressTransaction); restoreErr != nil {
			return fmt.Errorf("%w; restore failed: %v", err, restoreErr)
		}
		return err
	}

	previous := m.server
	overlappingBindings := previous != nil && bindingKeysOverlap(l4ServerBindingKeys(previous), l4RuleBindingKeys(rules))
	if previous != nil && !overlappingBindings {
		server, err := l4.NewServerWithResourcesWireGuardAndEgressRuntime(ctx, rules, relayListeners, m.provider, m.cache, provider, egressProvider, egressProfiles)
		if err == nil {
			server.SetTrafficBlockState(m.currentTrafficBlockState())
			if transaction != nil {
				m.wireGuardRuntime.Commit(transaction, wireGuardProfiles)
				transaction = nil
			}
			if egressTransaction != nil {
				m.egressWireGuard.Commit(egressTransaction, localEgressProfiles)
				egressTransaction = nil
			}
			_ = previous.Close()
			m.server = server
			m.storeLastAppliedInputsLocked(rules, relayListeners, egressProfiles)
			return nil
		}
		if !overlappingBindings || !isRuntimeBindConflict(err) {
			if restoreErr := m.rollbackWireGuardAndRestorePreviousServerLocked(ctx, &transaction, &egressTransaction); restoreErr != nil {
				return fmt.Errorf("%w; restore failed: %v", err, restoreErr)
			}
			return err
		}
		overlappingBindings = true
	}
	if previous != nil && overlappingBindings {
		_ = previous.Close()
		m.server = nil
	}
	server, err := retryRuntimeBindConflict(ctx, func() (*l4.Server, error) {
		return l4.NewServerWithResourcesWireGuardAndEgressRuntime(ctx, rules, relayListeners, m.provider, m.cache, provider, egressProvider, egressProfiles)
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
				return l4.NewServerWithResourcesWireGuardAndEgressRuntime(ctx, rules, relayListeners, m.provider, m.cache, provider, egressProvider, egressProfiles)
			})
		}
	}
	if err != nil {
		if previous != nil {
			if restoreErr := m.rollbackWireGuardAndRestorePreviousServerLocked(ctx, &transaction, &egressTransaction); restoreErr != nil {
				return fmt.Errorf("%w; restore failed: %v", err, restoreErr)
			}
		}
		return err
	}
	server.SetTrafficBlockState(m.currentTrafficBlockState())
	if transaction != nil {
		m.wireGuardRuntime.Commit(transaction, wireGuardProfiles)
		transaction = nil
	}
	if egressTransaction != nil {
		m.egressWireGuard.Commit(egressTransaction, localEgressProfiles)
		egressTransaction = nil
	}
	m.server = server
	m.storeLastAppliedInputsLocked(rules, relayListeners, egressProfiles)
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

func (m *l4RuntimeManager) rollbackWireGuardAndRestorePreviousServerLocked(ctx context.Context, transaction **modulewireguard.Transaction, egressTransaction **modulewireguard.Transaction) error {
	if transaction != nil && *transaction != nil {
		(*transaction).Rollback()
		*transaction = nil
	}
	if egressTransaction != nil && *egressTransaction != nil {
		(*egressTransaction).Rollback()
		*egressTransaction = nil
	}
	return m.restorePreviousServerLocked(ctx)
}

func (m *l4RuntimeManager) restorePreviousServerLocked(ctx context.Context) error {
	if len(m.lastRules) == 0 {
		m.server = nil
		return nil
	}
	server, err := retryRuntimeBindConflict(ctx, func() (*l4.Server, error) {
		return l4.NewServerWithResourcesWireGuardAndEgressRuntime(ctx, m.lastRules, m.lastRelayListeners, m.provider, m.cache, m.wireGuardProvider, m.egressWireGuard.Provider(), m.lastEgressProfiles)
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

func (m *l4RuntimeManager) storeLastAppliedInputsLocked(rules []model.L4Rule, relayListeners []model.RelayListener, egressProfiles []model.EgressProfile) {
	m.lastRules = cloneL4Rules(rules)
	m.lastRelayListeners = cloneRelayListeners(relayListeners)
	m.lastEgressProfiles = cloneEgressProfiles(egressProfiles)
}

func localL4EgressProfiles(rules []model.L4Rule, profiles []model.EgressProfile) []model.EgressProfile {
	if profiles == nil {
		return nil
	}
	if len(rules) == 0 || len(profiles) == 0 {
		return []model.EgressProfile{}
	}
	referenced := make(map[int]struct{})
	for _, rule := range rules {
		if relayroute.UsesRelay(nil, rule.RelayLayers) || rule.EgressProfileID == nil || *rule.EgressProfileID <= 0 {
			continue
		}
		referenced[*rule.EgressProfileID] = struct{}{}
	}
	if len(referenced) == 0 {
		return []model.EgressProfile{}
	}
	out := make([]model.EgressProfile, 0, len(referenced))
	for _, profile := range profiles {
		if _, ok := referenced[profile.ID]; ok {
			out = append(out, profile)
		}
	}
	return out
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
		keys = append(keys, l4RuleBindingKey(rule))
	}
	return keys
}

func l4RuleListenAddress(rule model.L4Rule) string {
	host := rule.ListenHost
	if strings.EqualFold(strings.TrimSpace(rule.ListenMode), "wireguard") {
		if wireGuardTransparentInbound(rule) {
			host = ""
		} else if strings.TrimSpace(rule.WireGuardListenHost) != "" {
			host = rule.WireGuardListenHost
		}
	}
	return net.JoinHostPort(host, strconv.Itoa(rule.ListenPort))
}

func l4RuleBindingKey(rule model.L4Rule) string {
	protocol := "tcp"
	if strings.EqualFold(strings.TrimSpace(rule.Protocol), "udp") {
		protocol = "udp"
	}
	if strings.EqualFold(strings.TrimSpace(rule.ListenMode), "wireguard") {
		return "wireguard:" + strconv.Itoa(valueOrZeroWireGuardProfileID(rule.WireGuardProfileID)) + ":" + protocol + ":" + l4RuleListenAddress(rule)
	}
	return protocol + ":" + l4RuleListenAddress(rule)
}

func valueOrZeroWireGuardProfileID(value *int) int {
	if value == nil {
		return 0
	}
	return *value
}

func wireGuardTransparentInbound(rule model.L4Rule) bool {
	return strings.EqualFold(strings.TrimSpace(rule.ListenMode), "wireguard") &&
		strings.EqualFold(strings.TrimSpace(rule.WireGuardInboundMode), "transparent")
}

func (m *l4RuntimeManager) applyWireGuardProfilesLocked(ctx context.Context, profiles []model.WireGuardProfile) error {
	if m.wireGuardRuntime == nil || profiles == nil {
		return nil
	}
	return m.wireGuardRuntime.Apply(ctx, profiles)
}

func (m *l4RuntimeManager) prepareWireGuardProfilesLocked(ctx context.Context, profiles []model.WireGuardProfile) (*modulewireguard.Transaction, relayWireGuardProvider, error) {
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
	return transaction, wireGuardTransactionProvider{transaction: transaction, agentID: m.localAgentID, profiles: profiles}, nil
}

func (m *l4RuntimeManager) applyEgressWireGuardProfilesLocked(ctx context.Context, profiles []model.EgressProfile) error {
	if m.egressWireGuard == nil || profiles == nil {
		return nil
	}
	return m.egressWireGuard.Apply(ctx, profiles)
}

func (m *l4RuntimeManager) prepareEgressWireGuardProfilesLocked(ctx context.Context, profiles []model.EgressProfile) (*modulewireguard.Transaction, module.OverlayRuntime, error) {
	if m.egressWireGuard == nil || profiles == nil {
		return nil, nil, nil
	}
	return m.egressWireGuard.Prepare(ctx, profiles)
}

func (m *l4RuntimeManager) validateWireGuardReferencesLocked(rules []model.L4Rule, provider relayWireGuardProvider) error {
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
	if m.egressWireGuard != nil {
		if err := m.egressWireGuard.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (m *l4RuntimeManager) Cache() *backends.Cache {
	if m == nil {
		return nil
	}
	return m.cache
}
