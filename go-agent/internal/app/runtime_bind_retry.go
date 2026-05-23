package app

import (
	"context"
	"time"
)

const (
	runtimeBindRetryTimeout  = 2 * time.Second
	runtimeBindRetryInterval = 25 * time.Millisecond
)

func retryRuntimeBindConflict[T any](ctx context.Context, start func() (T, error)) (T, error) {
	deadline := time.Now().Add(runtimeBindRetryTimeout)
	for {
		value, err := start()
		if err == nil || !isRuntimeBindConflict(err) || time.Now().After(deadline) {
			return value, err
		}
		timer := time.NewTimer(runtimeBindRetryInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			var zero T
			return zero, ctx.Err()
		case <-timer.C:
		}
	}
}
