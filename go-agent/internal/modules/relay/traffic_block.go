package relay

import (
	"fmt"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/traffic"
)

type TrafficBlockState = traffic.BlockState
type trafficBlockStateValue = traffic.BlockStateValue

type trafficStateProvider interface {
	TrafficBlockState() TrafficBlockState
}

func (m *Module) syncTrafficBlockState(resolver module.ProviderResolver) TrafficBlockState {
	if provider := trafficStateProviderFromResolver(resolver); provider != nil {
		m.UpdateTrafficBlockState(provider.TrafficBlockState())
	}
	if m == nil {
		return TrafficBlockState{}
	}
	return m.currentTrafficBlockState()
}

func trafficStateProviderFromResolver(resolver module.ProviderResolver) trafficStateProvider {
	if resolver == nil {
		return nil
	}
	provider, _ := resolver.Resolve(module.ProviderTrafficSink)
	trafficProvider, _ := provider.(trafficStateProvider)
	return trafficProvider
}

func trafficBlockErrorMessage(state TrafficBlockState) string {
	state = state.Normalized()
	if state.Reason != "" {
		return state.Reason
	}
	return "traffic blocked"
}

func trafficBlockErr(state TrafficBlockState) error {
	return fmt.Errorf("%s", trafficBlockErrorMessage(state))
}
