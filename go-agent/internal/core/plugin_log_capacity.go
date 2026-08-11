package core

import (
	"context"
	"sync"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

// pluginLogCapacitySignal is an edge-triggered notification used only to
// unblock writers after an exact durable ACK releases outbox capacity.
// Taking the channel snapshot before checking capacity prevents lost wakes.
type pluginLogCapacitySignal struct {
	mu      sync.Mutex
	changed chan struct{}
}

func (signal *pluginLogCapacitySignal) snapshot() <-chan struct{} {
	signal.mu.Lock()
	defer signal.mu.Unlock()
	if signal.changed == nil {
		signal.changed = make(chan struct{})
	}
	return signal.changed
}

func (signal *pluginLogCapacitySignal) notify() {
	signal.mu.Lock()
	if signal.changed != nil {
		close(signal.changed)
	}
	signal.changed = make(chan struct{})
	signal.mu.Unlock()
}

func waitPluginLogCapacity(ctx context.Context, signal *pluginLogCapacitySignal, pending func() (int, error)) error {
	if ctx == nil {
		ctx = context.Background()
	}
	for {
		changed := signal.snapshot()
		count, err := pending()
		if err != nil {
			return err
		}
		if count < model.MaxPendingPluginLogReports {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-changed:
		}
	}
}
