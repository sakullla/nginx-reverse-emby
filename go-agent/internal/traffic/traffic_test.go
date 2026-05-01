package traffic

import (
	"sync"
	"testing"
)

func TestSnapshotAggregatesTrafficByCategory(t *testing.T) {
	Reset()
	AddHTTP(100, 200)
	AddL4(300, 400)
	AddRelay(500, 600)

	stats := Snapshot()
	traffic, ok := stats["traffic"].(map[string]any)
	if !ok {
		t.Fatalf("traffic stats missing or wrong type: %#v", stats["traffic"])
	}

	assertTrafficCounters(t, traffic["http"], 100, 200)
	assertTrafficCounters(t, traffic["l4"], 300, 400)
	assertTrafficCounters(t, traffic["relay"], 500, 600)
	assertTrafficCounters(t, traffic["total"], 900, 1200)
}

func TestAddIgnoresNegativeValues(t *testing.T) {
	Reset()
	AddHTTP(-1, -2)

	stats := Snapshot()
	traffic := stats["traffic"].(map[string]any)
	assertTrafficCounters(t, traffic["http"], 0, 0)
	assertTrafficCounters(t, traffic["total"], 0, 0)
}

func TestDisabledTrafficStatsDoNotRecordOrSnapshot(t *testing.T) {
	Reset()
	SetEnabled(false)
	t.Cleanup(func() {
		SetEnabled(true)
		Reset()
	})

	AddHTTP(100, 200)
	AddL4(300, 400)
	AddRelay(500, 600)

	if stats := Snapshot(); stats != nil {
		t.Fatalf("Snapshot() = %#v, want nil when disabled", stats)
	}
}

func TestRecorderBatchesTrafficUntilFlush(t *testing.T) {
	Reset()

	recorder := NewHTTPRecorder()
	recorder.Add(10, 20)
	recorder.Add(30, 40)

	stats := Snapshot()
	traffic := stats["traffic"].(map[string]any)
	assertTrafficCounters(t, traffic["http"], 0, 0)

	recorder.Flush()

	stats = Snapshot()
	traffic = stats["traffic"].(map[string]any)
	assertTrafficCounters(t, traffic["http"], 40, 60)
}

func TestRecorderCanBeSharedAcrossConcurrentDirections(t *testing.T) {
	Reset()

	recorder := NewL4Recorder()
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(direction int) {
			defer wg.Done()
			for j := 0; j < 1000; j++ {
				if direction == 0 {
					recorder.Add(7, 0)
					continue
				}
				recorder.Add(0, 11)
			}
		}(i)
	}
	wg.Wait()
	recorder.Flush()

	stats := Snapshot()
	traffic := stats["traffic"].(map[string]any)
	assertTrafficCounters(t, traffic["l4"], 7000, 11000)
}

func assertTrafficCounters(t *testing.T, raw any, wantRX uint64, wantTX uint64) {
	t.Helper()

	counters, ok := raw.(map[string]uint64)
	if !ok {
		t.Fatalf("counter type = %T, want map[string]uint64", raw)
	}
	if counters["rx_bytes"] != wantRX {
		t.Fatalf("rx_bytes = %d, want %d", counters["rx_bytes"], wantRX)
	}
	if counters["tx_bytes"] != wantTX {
		t.Fatalf("tx_bytes = %d, want %d", counters["tx_bytes"], wantTX)
	}
}
