package relay

import (
	"fmt"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/traffic"
)

type TrafficBlockState = traffic.BlockState
type trafficBlockStateValue = traffic.BlockStateValue

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
