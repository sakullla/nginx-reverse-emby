package l4

import (
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/traffic"
)

type TrafficBlockState = traffic.BlockState
type trafficBlockStateValue = traffic.BlockStateValue

func (m *Module) trafficBlockStateFromProvider(resolver module.ProviderResolver) TrafficBlockState {
	if state, ok := traffic.BlockStateFromProvider(resolver); ok {
		return state
	}
	if m == nil {
		return TrafficBlockState{}
	}
	return m.currentTrafficBlockStateLocked()
}

func (m *Module) trafficBlockStateTransaction(previous, next TrafficBlockState) module.ModuleTransaction {
	return traffic.BlockStateTransaction(previous, next, m.UpdateTrafficBlockState)
}
