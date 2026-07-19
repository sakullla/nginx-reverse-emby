//go:build !linux && !windows

package hostmetrics

import (
	"context"
	"fmt"
	"runtime"
)

func readCPUTimes(ctx context.Context) (cpuTimes, error) {
	return cpuTimes{}, unsupportedMetric(ctx, "CPU usage")
}

func readMemory(ctx context.Context) (*memorySnapshot, error) {
	return nil, unsupportedMetric(ctx, "memory")
}

func readDiskUsage(ctx context.Context, _ string) (*diskSnapshot, error) {
	return nil, unsupportedMetric(ctx, "disk")
}

func readNetworkCounters(ctx context.Context) ([]networkCounter, error) {
	return nil, unsupportedMetric(ctx, "network")
}

func unsupportedMetric(ctx context.Context, metric string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return fmt.Errorf("%s metrics are unsupported on %s", metric, runtime.GOOS)
}
