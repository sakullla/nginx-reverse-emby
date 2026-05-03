package proxy

import (
	"strings"
	"sync/atomic"
)

type TrafficBlockState struct {
	Blocked bool
	Reason  string
}

func (s TrafficBlockState) normalized() TrafficBlockState {
	s.Reason = strings.TrimSpace(s.Reason)
	return s
}

type trafficBlockStateValue struct {
	value atomic.Value
}

func (v *trafficBlockStateValue) Store(state TrafficBlockState) {
	v.value.Store(state.normalized())
}

func (v *trafficBlockStateValue) Load() TrafficBlockState {
	if v == nil {
		return TrafficBlockState{}
	}
	if raw := v.value.Load(); raw != nil {
		if state, ok := raw.(TrafficBlockState); ok {
			return state.normalized()
		}
	}
	return TrafficBlockState{}
}
