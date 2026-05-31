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

func (m *Module) Apply(_ context.Context, req module.ApplyRequest) error {
	if m == nil {
		return nil
	}
	if _, err := core.ParseTrafficStatsInterval(req.Next.AgentConfig.TrafficStatsInterval); err != nil {
		return err
	}
	if req.Next.AgentConfig.TrafficStatsEnabled != nil {
		SetEnabled(*req.Next.AgentConfig.TrafficStatsEnabled)
	}

	meta := map[string]string{}
	if err := core.SetTrafficStatsIntervalMetadata(meta, req.Next.AgentConfig.TrafficStatsInterval); err != nil {
		return err
	}
	m.mu.Lock()
	m.meta = meta
	m.blockState.Store(BlockState{
		Blocked: req.Next.AgentConfig.TrafficBlocked,
		Reason:  req.Next.AgentConfig.TrafficBlockReason,
	})
	m.mu.Unlock()
	return nil
}

func (m *Module) Stop(context.Context) error {
	return nil
}

func (m *Module) TrafficReport(ctx context.Context, meta map[string]string) (core.TrafficReport, error) {
	if m == nil {
		return core.TrafficReport{}, nil
	}
	effective := copyStringMap(meta)
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

func copyStringMap(src map[string]string) map[string]string {
	dst := make(map[string]string, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}

var _ module.Module = (*Module)(nil)
