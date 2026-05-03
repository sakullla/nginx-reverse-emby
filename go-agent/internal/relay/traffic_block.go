package relay

import (
	"fmt"
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

func (s TrafficBlockState) errorMessage() string {
	s = s.normalized()
	if s.Reason != "" {
		return s.Reason
	}
	return "traffic blocked"
}

func (s TrafficBlockState) err() error {
	return fmt.Errorf("%s", s.errorMessage())
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
