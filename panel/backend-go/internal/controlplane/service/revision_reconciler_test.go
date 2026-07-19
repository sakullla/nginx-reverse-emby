package service

import (
	"context"
	"testing"
	"time"
)

func TestRevisionReconcilerRunsWithoutAgentPulls(t *testing.T) {
	calls := make(chan struct{}, 3)
	reconciler := newRevisionReconciler(5*time.Millisecond, nil, func(context.Context) error {
		select {
		case calls <- struct{}{}:
		default:
		}
		return nil
	})
	reconciler.Start()
	t.Cleanup(reconciler.Close)

	for index := 0; index < 2; index++ {
		select {
		case <-calls:
		case <-time.After(time.Second):
			t.Fatalf("reconciliation call %d did not run", index+1)
		}
	}
}
