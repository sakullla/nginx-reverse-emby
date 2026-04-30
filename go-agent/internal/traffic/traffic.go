package traffic

import "sync/atomic"

type counters struct {
	rx atomic.Uint64
	tx atomic.Uint64
}

var (
	httpCounters  counters
	l4Counters    counters
	relayCounters counters
)

func AddHTTP(rxBytes, txBytes int64) {
	add(&httpCounters, rxBytes, txBytes)
}

func AddL4(rxBytes, txBytes int64) {
	add(&l4Counters, rxBytes, txBytes)
}

func AddRelay(rxBytes, txBytes int64) {
	add(&relayCounters, rxBytes, txBytes)
}

func Snapshot() map[string]any {
	http := snapshotCounters(&httpCounters)
	l4 := snapshotCounters(&l4Counters)
	relay := snapshotCounters(&relayCounters)
	total := map[string]uint64{
		"rx_bytes": http["rx_bytes"] + l4["rx_bytes"] + relay["rx_bytes"],
		"tx_bytes": http["tx_bytes"] + l4["tx_bytes"] + relay["tx_bytes"],
	}
	return map[string]any{
		"traffic": map[string]any{
			"total": total,
			"http":  http,
			"l4":    l4,
			"relay": relay,
		},
	}
}

func Reset() {
	httpCounters.rx.Store(0)
	httpCounters.tx.Store(0)
	l4Counters.rx.Store(0)
	l4Counters.tx.Store(0)
	relayCounters.rx.Store(0)
	relayCounters.tx.Store(0)
}

func add(counter *counters, rxBytes, txBytes int64) {
	if rxBytes > 0 {
		counter.rx.Add(uint64(rxBytes))
	}
	if txBytes > 0 {
		counter.tx.Add(uint64(txBytes))
	}
}

func snapshotCounters(counter *counters) map[string]uint64 {
	return map[string]uint64{
		"rx_bytes": counter.rx.Load(),
		"tx_bytes": counter.tx.Load(),
	}
}
