package traffic

import (
	"strings"
	"sync/atomic"
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
