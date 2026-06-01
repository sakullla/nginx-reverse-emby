package traffic

import (
	"context"
	"sync"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/core"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/traffic/hosttraffic"
)

type Config struct {
	Interfaces []string
	Enabled    bool
	EnabledSet bool
}

type Module struct {
	mu       sync.RWMutex
	reporter *Reporter
	meta     map[string]string

	blockState BlockStateValue
}

func NewModule(cfg ...Config) *Module {
	config := Config{}
	if len(cfg) > 0 {
		config = cfg[0]
	}
	if config.EnabledSet {
		SetEnabled(config.Enabled)
	}
	return &Module{
		reporter: NewReporter(ReporterConfig{
			HostSnapshotter: hosttraffic.NewCollector(config.Interfaces),
		}),
		meta: map[string]string{},
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
	effective := ensureStringMap(cloneStringMap(meta))
	m.mu.RLock()
	for key, value := range m.meta {
		if _, exists := effective[key]; !exists {
			effective[key] = value
		}
	}
	reporter := m.reporter
	m.mu.RUnlock()
	if reporter == nil {
		return core.TrafficReport{}, nil
	}
	return reporter.TrafficReport(ctx, effective)
}

func (m *Module) TrafficBlockState() BlockState {
	if m == nil {
		return BlockState{}
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.blockState.Load()
}

type transaction struct {
	module *Module

	previousEnabled    bool
	previousMeta       map[string]string
	previousBlockState BlockState

	nextEnabled    bool
	nextMeta       map[string]string
	nextBlockState BlockState

	rollbackCounters *counterState
}

func (tx *transaction) RegisterProviders(reg module.ProviderRegistry) error {
	return reg.Provide(module.ProviderTrafficSink, tx)
}

func (tx *transaction) Commit() error {
	if tx == nil || tx.module == nil {
		return nil
	}
	if tx.previousEnabled && !tx.nextEnabled {
		state := snapshotCounterState()
		tx.rollbackCounters = &state
	}
	tx.module.installState(tx.nextEnabled, tx.nextMeta, tx.nextBlockState)
	return nil
}

func (tx *transaction) Rollback() error {
	if tx == nil || tx.module == nil {
		return nil
	}
	tx.module.installState(tx.previousEnabled, tx.previousMeta, tx.previousBlockState)
	if tx.rollbackCounters != nil {
		restoreCounterState(*tx.rollbackCounters)
	}
	return nil
}

func (tx *transaction) TrafficBlockState() BlockState {
	if tx == nil {
		return BlockState{}
	}
	return tx.nextBlockState
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
