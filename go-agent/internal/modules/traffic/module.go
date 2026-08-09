package traffic

import (
	"context"
	"sync"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/core"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/traffic/hosttraffic"
)

type Config struct {
	Interfaces         []string
	Enabled            bool
	EnabledSet         bool
	GenerationSelector interface{ ActiveGeneration() *module.GenerationView }
}

type Module struct {
	mu       sync.RWMutex
	reporter *Reporter
	meta     map[string]string
	selector interface{ ActiveGeneration() *module.GenerationView }
	// Used by package tests to exercise the controller's fail-closed path.
	reconcileTrafficRuntime func(context.Context, model.AgentConfig) error

	blockState BlockStateValue
}

func NewModule(cfg ...Config) *Module {
	config := Config{}
	if len(cfg) > 0 {
		config = cfg[0]
	}
	if config.GenerationSelector != nil {
		SetEnabled(true)
	} else if config.EnabledSet {
		SetEnabled(config.Enabled)
	}
	return &Module{
		reporter: NewReporter(ReporterConfig{
			HostSnapshotter: hosttraffic.NewCollector(config.Interfaces),
		}),
		meta:     map[string]string{},
		selector: config.GenerationSelector,
	}
}

func (m *Module) Name() string {
	return "traffic"
}

func (m *Module) Descriptor() module.ModuleDescriptor {
	return module.ModuleDescriptor{
		Name:     m.Name(),
		Provides: []module.ProviderRef{module.ProviderTrafficSink},
	}
}

func (m *Module) RegisterProviders(reg module.ProviderRegistry) error {
	return reg.Provide(module.ProviderTrafficSink, m)
}

func (m *Module) Capabilities(module.SnapshotView) []module.Capability {
	return []module.Capability{{Name: "traffic_stats", Enabled: true}}
}

func (m *Module) Apply(ctx context.Context, req module.ApplyRequest) error {
	tx, err := m.Prepare(ctx, req)
	if err != nil {
		return err
	}
	if tx == nil {
		return nil
	}
	return tx.Commit()
}

func (m *Module) Prepare(_ context.Context, req module.ApplyRequest) (module.ModuleTransaction, error) {
	if m == nil {
		return nil, nil
	}
	if _, err := core.ParseTrafficStatsInterval(req.Next.AgentConfig.TrafficStatsInterval); err != nil {
		return nil, err
	}

	meta := map[string]string{}
	if err := core.SetTrafficStatsIntervalMetadata(meta, req.Next.AgentConfig.TrafficStatsInterval); err != nil {
		return nil, err
	}

	nextEnabled := Enabled()
	if req.Next.AgentConfig.TrafficStatsEnabled != nil {
		nextEnabled = *req.Next.AgentConfig.TrafficStatsEnabled
	}
	nextBlockState := BlockState{
		Blocked: req.Next.AgentConfig.TrafficBlocked,
		Reason:  req.Next.AgentConfig.TrafficBlockReason,
	}.Normalized()

	m.mu.RLock()
	previousMeta := cloneStringMap(m.meta)
	previousBlockState := m.blockState.Load()
	m.mu.RUnlock()
	return &transaction{
		module:             m,
		previousEnabled:    Enabled(),
		previousMeta:       previousMeta,
		previousBlockState: previousBlockState,
		nextEnabled:        nextEnabled,
		nextMeta:           meta,
		nextBlockState:     nextBlockState,
	}, nil
}

func (m *Module) Stop(context.Context) error {
	return nil
}

func (m *Module) TrafficReport(ctx context.Context, meta map[string]string) (core.TrafficReport, error) {
	if m == nil {
		return core.TrafficReport{}, nil
	}
	if provider := m.activeProvider(); provider != nil {
		return provider.TrafficReport(ctx, meta)
	}
	if m.selector != nil {
		return core.TrafficReport{}, nil
	}
	return m.trafficReport(ctx, meta, Enabled(), m.committedMeta())
}

func (m *Module) trafficReport(ctx context.Context, meta map[string]string, enabled bool, configuredMeta map[string]string) (core.TrafficReport, error) {
	effective := ensureStringMap(cloneStringMap(meta))
	for key, value := range configuredMeta {
		if _, exists := effective[key]; !exists {
			effective[key] = value
		}
	}
	m.mu.RLock()
	reporter := m.reporter
	m.mu.RUnlock()
	if reporter == nil {
		return core.TrafficReport{}, nil
	}
	if !enabled {
		return core.TrafficReport{Stats: map[string]any{}, StatsPresent: true}, nil
	}
	return reporter.TrafficReport(ctx, effective)
}

func (m *Module) TrafficBlockState() BlockState {
	if m == nil {
		return BlockState{}
	}
	if provider := m.activeProvider(); provider != nil {
		return provider.TrafficBlockState()
	}
	if m.selector != nil {
		return BlockState{}
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.blockState.Load()
}

type transaction struct {
	mu     sync.RWMutex
	module *Module

	previousEnabled    bool
	previousMeta       map[string]string
	previousBlockState BlockState

	nextEnabled    bool
	nextMeta       map[string]string
	nextBlockState BlockState

	rollbackCounters *counterState
	published        bool
}

func (tx *transaction) RegisterProviders(reg module.ProviderRegistry) error {
	return reg.Provide(module.ProviderTrafficSink, tx)
}

func (*transaction) Ready(context.Context) error { return nil }

func (tx *transaction) Publish() {
	if tx == nil || tx.module == nil {
		return
	}
	tx.mu.Lock()
	defer tx.mu.Unlock()
	if tx.published {
		return
	}
	if tx.previousEnabled && !tx.nextEnabled {
		state := snapshotCounterState()
		tx.rollbackCounters = &state
	}
	tx.module.installState(tx.nextEnabled, tx.nextMeta, tx.nextBlockState)
	tx.published = true
	return
}

func (*transaction) Destroy(context.Context) error { return nil }

func (tx *transaction) Commit() error {
	if err := tx.Ready(context.Background()); err != nil {
		return err
	}
	tx.Publish()
	return nil
}

func (tx *transaction) Rollback() error {
	if tx == nil || tx.module == nil {
		return nil
	}
	tx.mu.Lock()
	defer tx.mu.Unlock()
	if !tx.published {
		return nil
	}
	tx.module.installState(tx.previousEnabled, tx.previousMeta, tx.previousBlockState)
	if tx.rollbackCounters != nil {
		restoreCounterState(*tx.rollbackCounters)
	}
	tx.published = false
	return nil
}

func (tx *transaction) TrafficBlockState() BlockState {
	if tx == nil {
		return BlockState{}
	}
	tx.mu.RLock()
	defer tx.mu.RUnlock()
	return tx.nextBlockState
}

func (tx *transaction) TrafficReport(ctx context.Context, meta map[string]string) (core.TrafficReport, error) {
	if tx == nil || tx.module == nil {
		return core.TrafficReport{}, nil
	}
	tx.mu.RLock()
	enabled := tx.nextEnabled
	configuredMeta := cloneStringMap(tx.nextMeta)
	tx.mu.RUnlock()
	return tx.module.trafficReport(ctx, meta, enabled, configuredMeta)
}

func (tx *transaction) ReconcileTrafficRuntime(ctx context.Context, config model.AgentConfig) error {
	if tx == nil || tx.module == nil || config.TrafficStatsEnabled == nil {
		return nil
	}
	if hook := tx.module.reconcileTrafficRuntime; hook != nil {
		if err := hook(ctx, config); err != nil {
			return err
		}
	}
	nextEnabled := *config.TrafficStatsEnabled
	nextBlockState := BlockState{Blocked: config.TrafficBlocked, Reason: config.TrafficBlockReason}.Normalized()
	tx.mu.Lock()
	tx.nextEnabled = nextEnabled
	tx.nextBlockState = nextBlockState
	tx.mu.Unlock()
	return nil
}

func (tx *transaction) FailClosedTrafficRuntime(config model.AgentConfig) {
	if tx == nil || tx.module == nil {
		return
	}
	tx.mu.Lock()
	if config.TrafficStatsEnabled != nil {
		tx.nextEnabled = *config.TrafficStatsEnabled
	}
	tx.nextBlockState = BlockState{Blocked: true, Reason: config.TrafficBlockReason}.Normalized()
	tx.mu.Unlock()
}

func (m *Module) committedMeta() map[string]string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return cloneStringMap(m.meta)
}

func (m *Module) activeProvider() interface {
	TrafficReport(context.Context, map[string]string) (core.TrafficReport, error)
	TrafficBlockState() BlockState
} {
	if m == nil || m.selector == nil {
		return nil
	}
	active := m.selector.ActiveGeneration()
	if active == nil {
		return nil
	}
	provider, _ := active.Resolve(module.ProviderTrafficSink)
	resolved, _ := provider.(interface {
		TrafficReport(context.Context, map[string]string) (core.TrafficReport, error)
		TrafficBlockState() BlockState
	})
	return resolved
}

func (m *Module) installState(enabled bool, meta map[string]string, blockState BlockState) {
	SetEnabled(enabled)
	m.mu.Lock()
	m.meta = cloneStringMap(meta)
	m.blockState.Store(blockState)
	m.mu.Unlock()
}

func cloneStringMap(src map[string]string) map[string]string {
	if src == nil {
		return nil
	}
	dst := make(map[string]string, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}

func ensureStringMap(src map[string]string) map[string]string {
	if src == nil {
		return make(map[string]string)
	}
	return src
}

var _ module.Module = (*Module)(nil)
var _ module.TransactionalModule = (*Module)(nil)
var _ module.GenerationTransaction = (*transaction)(nil)
var _ core.TrafficReporter = (*transaction)(nil)
