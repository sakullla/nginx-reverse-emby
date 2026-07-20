package hostmetrics

import (
	"context"
	"errors"
	"runtime"
	"sync"
)

type cpuTimes struct {
	Total uint64
	Idle  uint64
}

type memorySnapshot struct {
	Total       uint64
	Used        uint64
	UsedPercent float64
}

type diskSnapshot struct {
	Total       uint64
	Used        uint64
	UsedPercent float64
}

type networkCounter struct {
	Name      string
	BytesRecv uint64
	BytesSent uint64
}

type cpuTimesFunc func(context.Context) (cpuTimes, error)

func newCPUPercentSampler(snapshot cpuTimesFunc) cpuPercentFunc {
	var mu sync.Mutex
	var previous cpuTimes
	initialized := false

	return func(ctx context.Context) (float64, error) {
		mu.Lock()
		defer mu.Unlock()

		current, err := snapshot(ctx)
		if err != nil {
			return 0, err
		}

		baseline := cpuTimes{}
		if initialized && current.Total >= previous.Total && current.Idle >= previous.Idle {
			baseline = previous
		}
		previous = current
		initialized = true

		total := current.Total - baseline.Total
		idle := current.Idle - baseline.Idle
		if total == 0 {
			return 0, nil
		}
		if idle > total {
			idle = total
		}
		return float64(total-idle) / float64(total) * 100, nil
	}
}

func logicalCPUCount(ctx context.Context) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	count := runtime.NumCPU()
	if count <= 0 {
		return 0, errors.New("logical CPU count is unavailable")
	}
	return count, nil
}
