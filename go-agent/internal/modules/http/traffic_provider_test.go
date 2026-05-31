package http

import (
	"context"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
)

func TestModuleSyncsTrafficBlockStateFromProviderOnAgentConfigOnlyApply(t *testing.T) {
	mod := NewModule(Config{})
	providers := httpTrafficProviderResolver{provider: httpTrafficStateProvider{
		state: TrafficBlockState{Blocked: true, Reason: "monthly quota exceeded"},
	}}

	if err := mod.Apply(context.Background(), module.ApplyRequest{
		Previous:  model.Snapshot{AgentConfig: model.AgentConfig{TrafficBlocked: false}},
		Next:      model.Snapshot{AgentConfig: model.AgentConfig{TrafficBlocked: true, TrafficBlockReason: "monthly quota exceeded"}},
		Providers: providers,
	}); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	if got := mod.currentTrafficBlockStateLocked(); !got.Blocked || got.Reason != "monthly quota exceeded" {
		t.Fatalf("traffic block state = %+v, want provider state", got)
	}
}

type httpTrafficProviderResolver struct {
	provider httpTrafficStateProvider
}

func (r httpTrafficProviderResolver) Resolve(ref module.ProviderRef) (any, bool) {
	if ref == module.ProviderTrafficSink {
		return r.provider, true
	}
	return nil, false
}

type httpTrafficStateProvider struct {
	state TrafficBlockState
}

func (p httpTrafficStateProvider) TrafficBlockState() TrafficBlockState {
	return p.state
}
