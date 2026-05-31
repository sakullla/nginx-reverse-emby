package http

import (
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
	return m.currentTrafficBlockStateLocked()
}

func trafficStateProviderFromResolver(resolver module.ProviderResolver) trafficStateProvider {
	if resolver == nil {
		return nil
	}
	provider, _ := resolver.Resolve(module.ProviderTrafficSink)
	trafficProvider, _ := provider.(trafficStateProvider)
	return trafficProvider
}
