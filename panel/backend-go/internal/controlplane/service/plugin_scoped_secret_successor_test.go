package service

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestScopedSecretSuccessorDebounceCoalescesBurst(t *testing.T) {
	manager := &PluginCapabilityManager{successorDelay: 30 * time.Millisecond}
	var mu sync.Mutex
	calls := 0
	done := make(chan struct{})
	manager.successorOverride = func(context.Context, string) error {
		mu.Lock()
		calls++
		count := calls
		mu.Unlock()
		if count == 1 {
			close(done)
		}
		return nil
	}
	manager.scheduleScopedSecretSuccessor("instance-a")
	manager.scheduleScopedSecretSuccessor("instance-a")
	manager.scheduleScopedSecretSuccessor("  instance-a  ")
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("successor was not scheduled")
	}
	time.Sleep(80 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if calls != 1 {
		t.Fatalf("successor calls = %d", calls)
	}
}

func TestScopedSecretSuccessorDebounceKeepsDistinctInstances(t *testing.T) {
	manager := &PluginCapabilityManager{successorDelay: 20 * time.Millisecond}
	var mu sync.Mutex
	seen := map[string]int{}
	done := make(chan struct{})
	manager.successorOverride = func(_ context.Context, instanceID string) error {
		mu.Lock()
		seen[instanceID]++
		if len(seen) == 2 {
			select {
			case <-done:
			default:
				close(done)
			}
		}
		mu.Unlock()
		return nil
	}
	manager.scheduleScopedSecretSuccessor("instance-a")
	manager.scheduleScopedSecretSuccessor("instance-b")
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("both successors were not scheduled")
	}
	time.Sleep(60 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if seen["instance-a"] != 1 || seen["instance-b"] != 1 {
		t.Fatalf("successor calls = %#v", seen)
	}
}
