package wireguard

import (
	"runtime"
	"runtime/debug"
	"sync"
	"time"
)

const (
	wireGuardMemoryScavengeInterval  = 45 * time.Second
	wireGuardHeapScavengeMinHeapSys  = 128 << 20
	wireGuardHeapScavengeMinRetained = 64 << 20
)

var wireGuardMemoryScavenger wireGuardMemoryScavengerState

type wireGuardMemoryScavengerState struct {
	mu     sync.Mutex
	refs   int
	stopCh chan struct{}
}

func retainWireGuardMemoryScavenger() func() {
	wireGuardMemoryScavenger.mu.Lock()
	defer wireGuardMemoryScavenger.mu.Unlock()

	if wireGuardMemoryScavenger.refs == 0 {
		wireGuardMemoryScavenger.stopCh = make(chan struct{})
		go runWireGuardMemoryScavenger(wireGuardMemoryScavenger.stopCh)
	}
	wireGuardMemoryScavenger.refs++

	var once sync.Once
	return func() {
		once.Do(func() {
			wireGuardMemoryScavenger.mu.Lock()
			defer wireGuardMemoryScavenger.mu.Unlock()

			if wireGuardMemoryScavenger.refs > 0 {
				wireGuardMemoryScavenger.refs--
			}
			if wireGuardMemoryScavenger.refs == 0 && wireGuardMemoryScavenger.stopCh != nil {
				close(wireGuardMemoryScavenger.stopCh)
				wireGuardMemoryScavenger.stopCh = nil
			}
		})
	}
}

func runWireGuardMemoryScavenger(stopCh <-chan struct{}) {
	ticker := time.NewTicker(wireGuardMemoryScavengeInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			var stats runtime.MemStats
			runtime.ReadMemStats(&stats)
			if wireGuardHeapScavengeNeeded(stats) {
				debug.FreeOSMemory()
			}
		case <-stopCh:
			return
		}
	}
}

func wireGuardHeapScavengeNeeded(stats runtime.MemStats) bool {
	if stats.HeapSys < wireGuardHeapScavengeMinHeapSys {
		return false
	}
	if stats.HeapSys <= stats.HeapReleased+stats.HeapAlloc {
		return false
	}
	return stats.HeapSys-stats.HeapReleased-stats.HeapAlloc >= wireGuardHeapScavengeMinRetained
}
