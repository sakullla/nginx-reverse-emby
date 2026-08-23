package service

import (
	"context"
	"log"
	"sync"
	"time"
)

const defaultRevisionReconcileInterval = 5 * time.Second

// RevisionReconciler advances expired coordinator attempts independently of
// agent pull traffic. This guarantees that a vanished agent cannot leave an
// applying operation permanently nonterminal.
type RevisionReconciler struct {
	interval  time.Duration
	logger    *log.Logger
	reconcile func(context.Context) error

	startOnce sync.Once
	stopOnce  sync.Once
	cancel    context.CancelFunc
	wg        sync.WaitGroup
}

func NewRevisionReconciler(api *RevisionAPI, logger *log.Logger) *RevisionReconciler {
	var reconcile func(context.Context) error
	if api != nil && api.coordinator != nil {
		reconcile = api.reconcileStartup
	}
	return newRevisionReconciler(defaultRevisionReconcileInterval, logger, reconcile)
}

func newRevisionReconciler(interval time.Duration, logger *log.Logger, reconcile func(context.Context) error) *RevisionReconciler {
	if interval <= 0 {
		interval = defaultRevisionReconcileInterval
	}
	if logger == nil {
		logger = log.Default()
	}
	return &RevisionReconciler{interval: interval, logger: logger, reconcile: reconcile}
}

func (r *RevisionReconciler) Start() {
	if r == nil || r.reconcile == nil {
		return
	}
	r.startOnce.Do(func() {
		ctx, cancel := context.WithCancel(context.Background())
		r.cancel = cancel
		r.wg.Add(1)
		go func() {
			defer r.wg.Done()
			r.run(ctx)
		}()
	})
}

func (r *RevisionReconciler) run(ctx context.Context) {
	r.reconcileOnce(ctx)
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.reconcileOnce(ctx)
		}
	}
}

func (r *RevisionReconciler) reconcileOnce(ctx context.Context) {
	if err := r.reconcile(ctx); err != nil && ctx.Err() == nil {
		r.logger.Printf("[revision-coordinator] reconciliation pass failed: %v", err)
	}
}

func (r *RevisionReconciler) Close() {
	if r == nil {
		return
	}
	r.stopOnce.Do(func() {
		if r.cancel != nil {
			r.cancel()
		}
		r.wg.Wait()
	})
}
