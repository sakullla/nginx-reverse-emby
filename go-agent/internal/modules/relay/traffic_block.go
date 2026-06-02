package relay

import (
	"fmt"

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
	return m.currentTrafficBlockState()
}

func (m *Module) trafficBlockStateTransaction(previous, next TrafficBlockState) module.ModuleTransaction {
	return traffic.BlockStateTransaction(previous, next, m.UpdateTrafficBlockState)
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
