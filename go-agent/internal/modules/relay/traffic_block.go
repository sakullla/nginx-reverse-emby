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

func (m *Module) trafficBlockStateFromProvider(resolver module.ProviderResolver) TrafficBlockState {
	if provider := trafficStateProviderFromResolver(resolver); provider != nil {
		return provider.TrafficBlockState().Normalized()
	}
	if m == nil {
		return TrafficBlockState{}
	}
	return m.currentTrafficBlockState()
}

func (m *Module) trafficBlockStateTransaction(previous, next TrafficBlockState) module.ModuleTransaction {
	previous = previous.Normalized()
	next = next.Normalized()
	if previous == next {
		return module.TransactionFuncs{}
	}
	return module.TransactionFuncs{
		CommitFunc: func() error {
			m.UpdateTrafficBlockState(next)
			return nil
		},
		RollbackFunc: func() error {
			m.UpdateTrafficBlockState(previous)
			return nil
		},
	}
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
