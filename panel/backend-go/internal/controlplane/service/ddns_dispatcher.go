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
			agentID, ok := d.next(ctx)
			if !ok {
				return
			}
			d.processSafely(ctx, process, agentID)
			d.finish(ctx, agentID)
		}
	}()
}

func (d *ddnsDispatcher) next(ctx context.Context) (string, bool) {
	if ctx.Err() != nil {
		return "", false
	}
	select {
	case agentID := <-d.queue:
		return agentID, true
	default:
	}

	select {
	case <-ctx.Done():
		return "", false
	case agentID := <-d.queue:
		return agentID, true
	}
}

// enqueue marks agentID as pending and queues it. If agentID is already pending
// or processing, the call coalesces into one dirty rerun. If the queue is
// saturated the enqueue is dropped (best-effort) rather than blocking the caller.
func (d *ddnsDispatcher) enqueue(agentID string) bool {
	if !d.reserve(agentID) {
		return false
	}

	select {
	case d.queue <- agentID:
		return true
	default:
		// Saturated: release the reservation so the next heartbeat can retry.
		d.release(agentID)
		return false
	}
}

// enqueueContext is the lossless sweep path. It may wait for queue capacity,
// but cancellation always releases its reservation so shutdown cannot hang.
func (d *ddnsDispatcher) enqueueContext(ctx context.Context, agentID string) bool {
	if !d.reserve(agentID) {
		return false
	}
	select {
	case d.queue <- agentID:
		return true
	case <-ctx.Done():
		d.release(agentID)
		return false
	}
}

func (d *ddnsDispatcher) reserve(agentID string) bool {
	if agentID == "" {
		return false
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, queued := d.inflight[agentID]; queued {
		d.dirty[agentID] = struct{}{}
		return false
	}
	d.inflight[agentID] = struct{}{}
	return true
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

func (d *ddnsDispatcher) finish(ctx context.Context, agentID string) {
	d.mu.Lock()
	if _, rerun := d.dirty[agentID]; rerun {
		delete(d.dirty, agentID)
		d.mu.Unlock()
		select {
		case d.queue <- agentID:
		default:
			d.wg.Add(1)
			go func() {
				defer d.wg.Done()
				select {
				case d.queue <- agentID:
				case <-ctx.Done():
					d.release(agentID)
				}
			}()
		}
		return
	}
	delete(d.inflight, agentID)
	d.mu.Unlock()
}

// stop waits for the worker to drain after its context is cancelled.
func (d *ddnsDispatcher) stop() {
	d.wg.Wait()
}
