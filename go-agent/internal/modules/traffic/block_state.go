package traffic

import (
	"strings"
	"sync/atomic"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
)

type BlockState struct {
	Blocked bool
	Reason  string
}

func (s BlockState) Normalized() BlockState {
	s.Reason = strings.TrimSpace(s.Reason)
	return s
}

type BlockStateValue struct {
	value atomic.Value
}

func (v *BlockStateValue) Store(state BlockState) {
	v.value.Store(state.Normalized())
}

func (v *BlockStateValue) Load() BlockState {
	if v == nil {
		return BlockState{}
	}
	if raw := v.value.Load(); raw != nil {
		if state, ok := raw.(BlockState); ok {
			return state.Normalized()
		}
	}
	return BlockState{}
}

type BlockStateProvider interface {
	TrafficBlockState() BlockState
}

func BlockStateFromProvider(resolver module.ProviderResolver) (BlockState, bool) {
	if resolver == nil {
		return BlockState{}, false
	}
	provider, _ := resolver.Resolve(module.ProviderTrafficSink)
	trafficProvider, _ := provider.(BlockStateProvider)
	if trafficProvider == nil {
		return BlockState{}, false
	}
	return trafficProvider.TrafficBlockState().Normalized(), true
}

func BlockStateTransaction(previous, next BlockState, apply func(BlockState)) module.ModuleTransaction {
	previous = previous.Normalized()
	next = next.Normalized()
	if previous == next {
		return module.TransactionFuncs{}
	}
	return module.TransactionFuncs{
		CommitFunc: func() error {
			apply(next)
			return nil
		},
		RollbackFunc: func() error {
			apply(previous)
			return nil
		},
	}
}
