package storage

import (
	"math"
	"testing"
	"time"
)

func TestCoordinatorRetryDelayUsesCappedFullJitter(t *testing.T) {
	testCases := []struct {
		attempt int
		jitter  float64
		want    time.Duration
	}{
		{attempt: 1, jitter: 0, want: 0},
		{attempt: 1, jitter: 0.5, want: 500 * time.Millisecond},
		{attempt: 2, jitter: 0.5, want: time.Second},
		{attempt: 5, jitter: 0.5, want: 8 * time.Second},
		{attempt: 6, jitter: 0.5, want: 15 * time.Second},
		{attempt: 20, jitter: 0.5, want: 15 * time.Second},
		{attempt: 1, jitter: -1, want: 0},
		{attempt: 1, jitter: math.NaN(), want: 0},
	}
	for _, tc := range testCases {
		if got := coordinatorRetryDelay(tc.attempt, time.Second, 30*time.Second, tc.jitter); got != tc.want {
			t.Fatalf("coordinatorRetryDelay(%d, %v) = %v, want %v", tc.attempt, tc.jitter, got, tc.want)
		}
	}
	if got := coordinatorRetryDelay(20, time.Second, 30*time.Second, 1); got >= 30*time.Second || got < 29*time.Second {
		t.Fatalf("jitter=1 delay = %v, want clamped below 30s", got)
	}
}
