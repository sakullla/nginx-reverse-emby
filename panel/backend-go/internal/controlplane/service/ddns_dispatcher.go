package service

import (
	"context"
	"log"
	"sync"
)

// ddnsDispatcher coalesces heartbeat-triggered reconciliations so that at most
// one reconcile is queued or running per agent at a time. ReconcileAfterHeartbeat
// only enqueues (and returns immediately): the heartbeat main path must never
// block on DNS. An enqueue that arrives during in-flight work marks the agent
// dirty, causing one fresh-state rerun before the reservation is released.
type ddnsDispatcher struct {
	queue chan string

	mu       sync.Mutex
	inflight map[string]struct{}
	dirty    map[string]struct{}
	wg       sync.WaitGroup
}

func newDDNSDispatcher(queueDepth int) *ddnsDispatcher {
	if queueDepth < 1 {
		queueDepth = 1
	}
	return &ddnsDispatcher{
		queue:    make(chan string, queueDepth),
		inflight: make(map[string]struct{}),
		dirty:    make(map[string]struct{}),
	}
}

// start launches the single worker goroutine that drains the queue. It runs
// until ctx is cancelled; stop() must be called to wait for in-flight work.
// Each reconcile runs inside a recover guard so a panic in process (nil client,
// malformed response, future bug) is contained to one agent and never crashes
// the control-plane process — the worker keeps draining.
func (d *ddnsDispatcher) start(ctx context.Context, process func(context.Context, string)) {
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		for {
			select {
			case <-ctx.Done():
				return
			case agentID := <-d.queue:
				for {
					d.processSafely(ctx, process, agentID)
					if ctx.Err() != nil || !d.finishOrRerun(agentID) {
						break
					}
				}
			}
		}
	}()
}

// enqueue marks agentID as pending and queues it. If agentID is already pending
// or processing, the call coalesces into one dirty rerun. If the queue is
// saturated the enqueue is dropped (best-effort) rather than blocking the caller.
func (d *ddnsDispatcher) enqueue(agentID string) bool {
	d.mu.Lock()
	if _, queued := d.inflight[agentID]; queued {
		d.dirty[agentID] = struct{}{}
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
	delete(d.dirty, agentID)
	d.mu.Unlock()
}

func (d *ddnsDispatcher) processSafely(ctx context.Context, process func(context.Context, string), agentID string) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[ddns] reconcile for agent %q panicked (contained, worker continues): %v", agentID, r)
		}
	}()
	process(ctx, agentID)
}

func (d *ddnsDispatcher) finishOrRerun(agentID string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, rerun := d.dirty[agentID]; rerun {
		delete(d.dirty, agentID)
		return true
	}
	delete(d.inflight, agentID)
	return false
}

// stop waits for the worker to drain after its context is cancelled.
func (d *ddnsDispatcher) stop() {
	d.wg.Wait()
}
