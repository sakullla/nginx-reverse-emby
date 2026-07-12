package service

import (
	"context"
	"sync"
)

// ddnsDispatcher coalesces heartbeat-triggered reconciliations so that at most
// one reconcile is queued or running per agent at a time. ReconcileAfterHeartbeat
// only enqueues (and returns immediately): the heartbeat main path must never
// block on DNS. Because the worker always reads fresh state from the store, a
// dropped enqueue is harmless — the in-flight (or next sweep) reconcile observes
// the latest reported IPs.
type ddnsDispatcher struct {
	queue chan string

	mu       sync.Mutex
	inflight map[string]struct{}
	wg       sync.WaitGroup
}

func newDDNSDispatcher(queueDepth int) *ddnsDispatcher {
	if queueDepth < 1 {
		queueDepth = 1
	}
	return &ddnsDispatcher{
		queue:    make(chan string, queueDepth),
		inflight: make(map[string]struct{}),
	}
}

// start launches the single worker goroutine that drains the queue. It runs
// until ctx is cancelled; stop() must be called to wait for in-flight work.
func (d *ddnsDispatcher) start(ctx context.Context, process func(context.Context, string)) {
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		for {
			select {
			case <-ctx.Done():
				return
			case agentID := <-d.queue:
				process(ctx, agentID)
				d.release(agentID)
			}
		}
	}()
}

// enqueue marks agentID as pending and queues it. If agentID is already pending
// or processing, the call is a no-op (dedup). If the queue is saturated the
// enqueue is dropped (best-effort) rather than blocking the caller.
func (d *ddnsDispatcher) enqueue(agentID string) bool {
	d.mu.Lock()
	if _, queued := d.inflight[agentID]; queued {
		d.mu.Unlock()
		return false
	}
	d.inflight[agentID] = struct{}{}
	d.mu.Unlock()

	select {
	case d.queue <- agentID:
		return true
	default:
		// Saturated: release the reservation so the next heartbeat can retry.
		d.release(agentID)
		return false
	}
}

func (d *ddnsDispatcher) release(agentID string) {
	d.mu.Lock()
	delete(d.inflight, agentID)
	d.mu.Unlock()
}

// stop waits for the worker to drain after its context is cancelled.
func (d *ddnsDispatcher) stop() {
	d.wg.Wait()
}
