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

func TestScopedRecordersPopulatePerObjectBuckets(t *testing.T) {
	Reset()
	SetEnabled(true)
	defer Reset()

	NewHTTPRuleRecorder(11).Add(100, 200)
	NewL4RuleRecorder(21).Add(300, 400)
	NewRelayListenerRecorder(31).Add(500, 600)

	stats := Snapshot()["traffic"].(map[string]any)
	total := stats["total"].(map[string]uint64)
	if total["rx_bytes"] != 900 || total["tx_bytes"] != 1200 {
		t.Fatalf("total = %+v", total)
	}
	httpRules := stats["http_rules"].(map[string]map[string]uint64)
	l4Rules := stats["l4_rules"].(map[string]map[string]uint64)
	relayListeners := stats["relay_listeners"].(map[string]map[string]uint64)
	if httpRules["11"]["rx_bytes"] != 100 || httpRules["11"]["tx_bytes"] != 200 {
		t.Fatalf("http_rules[11] = %+v", httpRules["11"])
	}
	if l4Rules["21"]["rx_bytes"] != 300 || l4Rules["21"]["tx_bytes"] != 400 {
		t.Fatalf("l4_rules[21] = %+v", l4Rules["21"])
	}
	if relayListeners["31"]["rx_bytes"] != 500 || relayListeners["31"]["tx_bytes"] != 600 {
		t.Fatalf("relay_listeners[31] = %+v", relayListeners["31"])
	}
}

func TestResetClearsScopedBuckets(t *testing.T) {
	Reset()
	SetEnabled(true)
	NewHTTPRuleRecorder(11).Add(1, 2)
	NewL4RuleRecorder(21).Add(3, 4)
	NewRelayListenerRecorder(31).Add(5, 6)

	Reset()

	stats := Snapshot()["traffic"].(map[string]any)
	if got := len(stats["http_rules"].(map[string]map[string]uint64)); got != 0 {
		t.Fatalf("http_rules len = %d", got)
	}
	if got := len(stats["l4_rules"].(map[string]map[string]uint64)); got != 0 {
		t.Fatalf("l4_rules len = %d", got)
	}
	if got := len(stats["relay_listeners"].(map[string]map[string]uint64)); got != 0 {
		t.Fatalf("relay_listeners len = %d", got)
	}
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
