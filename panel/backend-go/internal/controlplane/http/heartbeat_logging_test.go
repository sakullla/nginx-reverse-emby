package http

import (
	"strconv"
	"testing"
	"time"
)

func TestHeartbeatFailureLogLimiterSuppressesRepeatedUnknownAgent(t *testing.T) {
	limiter := &heartbeatFailureLogLimiter{}
	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)

	if !limiter.allow("deleted-agent", now) {
		t.Fatal("first unknown-agent failure was suppressed")
	}
	if limiter.allow("deleted-agent", now.Add(unknownAgentHeartbeatLogInterval-time.Nanosecond)) {
		t.Fatal("repeated unknown-agent failure was not suppressed")
	}
	if !limiter.allow("deleted-agent", now.Add(unknownAgentHeartbeatLogInterval)) {
		t.Fatal("unknown-agent failure was not allowed after the interval")
	}
	if !limiter.allow("other-deleted-agent", now) {
		t.Fatal("a different unknown agent was incorrectly suppressed")
	}
}

func TestHeartbeatFailureLogLimiterBoundsAgentCardinality(t *testing.T) {
	limiter := &heartbeatFailureLogLimiter{}
	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	for index := 0; index < unknownAgentHeartbeatLogCacheSize+10; index++ {
		if !limiter.allow("deleted-agent-"+strconv.Itoa(index), now) {
			t.Fatalf("agent %d was unexpectedly suppressed", index)
		}
	}
	if len(limiter.last) > unknownAgentHeartbeatLogCacheSize {
		t.Fatalf("limiter cardinality = %d, want <= %d", len(limiter.last), unknownAgentHeartbeatLogCacheSize)
	}
}
