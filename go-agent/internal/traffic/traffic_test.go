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

func TestScopedRecorderFlushesAfterThresholdWithoutManualFlush(t *testing.T) {
	Reset()

	recorder := NewHTTPRuleRecorder(11)
	recorder.Add(int64(recorderFlushThreshold/2), 0)

	stats := Snapshot()["traffic"].(map[string]any)
	httpRules := stats["http_rules"].(map[string]map[string]uint64)
	if got := httpRules["11"]; got != nil {
		t.Fatalf("http_rules[11] = %#v, want no early flush before threshold", got)
	}

	recorder.Add(int64(recorderFlushThreshold/2), 0)

	stats = Snapshot()["traffic"].(map[string]any)
	httpRules = stats["http_rules"].(map[string]map[string]uint64)
	assertTrafficCounters(t, httpRules["11"], recorderFlushThreshold, 0)
	assertTrafficCounters(t, stats["http"], recorderFlushThreshold, 0)
}

func TestScopedRecorderBatchesBulkChunksBelowThreshold(t *testing.T) {
	Reset()

	recorder := NewL4RuleRecorder(42)
	recorder.Add(64*1024, 0)

	stats := Snapshot()["traffic"].(map[string]any)
	l4Rules := stats["l4_rules"].(map[string]map[string]uint64)
	if got := l4Rules["42"]; got != nil {
		t.Fatalf("l4_rules[42] = %#v, want 64KiB bulk chunk batched below threshold", got)
	}

	recorder.Flush()

	stats = Snapshot()["traffic"].(map[string]any)
	l4Rules = stats["l4_rules"].(map[string]map[string]uint64)
	assertTrafficCounters(t, l4Rules["42"], 64*1024, 0)
}

func TestSnapshotNonZeroFlushesPendingScopedRecorderTraffic(t *testing.T) {
	Reset()
	SetEnabled(true)
	defer Reset()

	recorder := NewL4RuleRecorder(42)
	recorder.Add(123, 456)

	stats := SnapshotNonZero()
	if stats == nil {
		t.Fatal("SnapshotNonZero() = nil, want pending recorder traffic")
	}
	traffic := stats["traffic"].(map[string]any)
	assertTrafficCounters(t, traffic["total"], 123, 456)
	assertTrafficCounters(t, traffic["l4"], 123, 456)
	l4Rules := traffic["l4_rules"].(map[string]map[string]uint64)
	assertTrafficCounters(t, l4Rules["42"], 123, 456)
}

func TestSnapshotNonZeroFlushesPendingRecordersOnce(t *testing.T) {
	Reset()
	SetEnabled(true)
	defer Reset()

	recorder := NewRelayListenerRecorder(31)
	recorder.Add(10, 20)

	first := SnapshotNonZero()["traffic"].(map[string]any)
	assertTrafficCounters(t, first["total"], 10, 20)

	second := SnapshotNonZero()["traffic"].(map[string]any)
	assertTrafficCounters(t, second["total"], 10, 20)
	relayListeners := second["relay_listeners"].(map[string]map[string]uint64)
	assertTrafficCounters(t, relayListeners["31"], 10, 20)
}

func TestSnapshotNonZeroDiscardsPendingRecorderTrafficAfterReset(t *testing.T) {
	Reset()
	SetEnabled(true)
	defer Reset()

	recorder := NewHTTPRuleRecorder(11)
	recorder.Add(100, 200)

	Reset()
	recorder.Add(7, 9)

	stats := SnapshotNonZero()
	if stats == nil {
		t.Fatal("SnapshotNonZero() = nil, want post-reset pending recorder traffic")
	}
	traffic := stats["traffic"].(map[string]any)
	assertTrafficCounters(t, traffic["total"], 7, 9)
	assertTrafficCounters(t, traffic["http"], 7, 9)
	httpRules := traffic["http_rules"].(map[string]map[string]uint64)
	assertTrafficCounters(t, httpRules["11"], 7, 9)
}

func TestDisablingTrafficStatsDropsPendingRecorderTraffic(t *testing.T) {
	Reset()
	SetEnabled(true)
	t.Cleanup(func() {
		SetEnabled(true)
		Reset()
	})

	recorder := NewHTTPRuleRecorder(11)
	recorder.Add(100, 200)

	SetEnabled(false)
	SetEnabled(true)
	recorder.Add(7, 9)

	stats := SnapshotNonZero()
	if stats == nil {
		t.Fatal("SnapshotNonZero() = nil, want traffic recorded after re-enable")
	}
	traffic := stats["traffic"].(map[string]any)
	assertTrafficCounters(t, traffic["total"], 7, 9)
	assertTrafficCounters(t, traffic["http"], 7, 9)
	httpRules := traffic["http_rules"].(map[string]map[string]uint64)
	assertTrafficCounters(t, httpRules["11"], 7, 9)
}

func TestDisablingTrafficStatsClearsFlushedCounters(t *testing.T) {
	Reset()
	SetEnabled(true)
	t.Cleanup(func() {
		SetEnabled(true)
		Reset()
	})

	recorder := NewHTTPRuleRecorder(11)
	recorder.Add(100, 200)
	recorder.Flush()

	SetEnabled(false)
	SetEnabled(true)

	if stats := SnapshotNonZero(); stats != nil {
		t.Fatalf("SnapshotNonZero() = %#v, want nil after disabled counters are cleared", stats)
	}

	recorder.Add(7, 9)
	stats := SnapshotNonZero()
	if stats == nil {
		t.Fatal("SnapshotNonZero() = nil, want traffic recorded after re-enable")
	}
	traffic := stats["traffic"].(map[string]any)
	assertTrafficCounters(t, traffic["total"], 7, 9)
	assertTrafficCounters(t, traffic["http"], 7, 9)
	httpRules := traffic["http_rules"].(map[string]map[string]uint64)
	assertTrafficCounters(t, httpRules["11"], 7, 9)
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

	httpRecorder := NewHTTPRuleRecorder(11)
	l4Recorder := NewL4RuleRecorder(21)
	relayRecorder := NewRelayListenerRecorder(31)
	httpRecorder.Add(100, 200)
	l4Recorder.Add(300, 400)
	relayRecorder.Add(500, 600)
	httpRecorder.Flush()
	l4Recorder.Flush()
	relayRecorder.Flush()

	stats := Snapshot()["traffic"].(map[string]any)
	total := stats["total"].(map[string]uint64)
	if total["rx_bytes"] != 900 || total["tx_bytes"] != 1200 {
		t.Fatalf("total = %+v", total)
	}
	assertTrafficCounters(t, stats["http"], 100, 200)
	assertTrafficCounters(t, stats["l4"], 300, 400)
	assertTrafficCounters(t, stats["relay"], 500, 600)
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

func TestResetKeepsLiveScopedRecorderVisible(t *testing.T) {
	Reset()
	SetEnabled(true)
	defer Reset()

	recorder := NewRelayListenerRecorder(31)
	recorder.Add(1, 2)

	Reset()

	recorder.Add(5, 6)
	recorder.Flush()

	stats := Snapshot()["traffic"].(map[string]any)
	assertTrafficCounters(t, stats["relay"], 5, 6)
	relayListeners := stats["relay_listeners"].(map[string]map[string]uint64)
	assertTrafficCounters(t, relayListeners["31"], 5, 6)
}

func TestResetClearsScopedBuckets(t *testing.T) {
	Reset()
	SetEnabled(true)
	httpRecorder := NewHTTPRuleRecorder(11)
	l4Recorder := NewL4RuleRecorder(21)
	relayRecorder := NewRelayListenerRecorder(31)
	httpRecorder.Add(1, 2)
	l4Recorder.Add(3, 4)
	relayRecorder.Add(5, 6)
	httpRecorder.Flush()
	l4Recorder.Flush()
	relayRecorder.Flush()

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
