package upstream

import "time"

type TimeoutPolicy struct {
	Base       time.Duration
	Multiplier int
	Floor      time.Duration
	Ceiling    time.Duration
}

func EstimateTimeout(policy TimeoutPolicy, estimate time.Duration) time.Duration {
	timeout := policy.Base + time.Duration(policy.Multiplier)*estimate
	if timeout < policy.Floor {
		return policy.Floor
	}
	if policy.Ceiling > 0 && timeout > policy.Ceiling {
		return policy.Ceiling
	}
	return timeout
}

func UDPReplyTimeoutPolicy() TimeoutPolicy {
	return TimeoutPolicy{
		Base:       time.Second,
		Multiplier: 5,
		Floor:      500 * time.Millisecond,
		Ceiling:    5 * time.Second,
	}
}
