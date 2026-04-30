package traffic

import "sync/atomic"

const recorderFlushThreshold uint64 = 64 * 1024

type counters struct {
	rx atomic.Uint64
	tx atomic.Uint64
}

type Recorder struct {
	counter *counters
	rx      uint64
	tx      uint64
}

var (
	httpCounters  counters
	l4Counters    counters
	relayCounters counters
	enabled       atomic.Bool
)

func init() {
	enabled.Store(true)
}

func SetEnabled(value bool) {
	enabled.Store(value)
}

func Enabled() bool {
	return enabled.Load()
}

func AddHTTP(rxBytes, txBytes int64) {
	add(&httpCounters, rxBytes, txBytes)
}

func AddL4(rxBytes, txBytes int64) {
	add(&l4Counters, rxBytes, txBytes)
}

func AddRelay(rxBytes, txBytes int64) {
	add(&relayCounters, rxBytes, txBytes)
}

func NewHTTPRecorder() *Recorder {
	return &Recorder{counter: &httpCounters}
}

func NewL4Recorder() *Recorder {
	return &Recorder{counter: &l4Counters}
}

func NewRelayRecorder() *Recorder {
	return &Recorder{counter: &relayCounters}
}

func (r *Recorder) Add(rxBytes, txBytes int64) {
	if r == nil || r.counter == nil || !Enabled() {
		return
	}
	if rxBytes > 0 {
		r.rx += uint64(rxBytes)
	}
	if txBytes > 0 {
		r.tx += uint64(txBytes)
	}
	if r.rx+r.tx >= recorderFlushThreshold {
		r.Flush()
	}
}

func (r *Recorder) Flush() {
	if r == nil || r.counter == nil || !Enabled() {
		return
	}
	addUint64(r.counter, r.rx, r.tx)
	r.rx = 0
	r.tx = 0
}

func Snapshot() map[string]any {
	if !Enabled() {
		return nil
	}
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
	if !Enabled() {
		return
	}
	var rx uint64
	var tx uint64
	if rxBytes > 0 {
		rx = uint64(rxBytes)
	}
	if txBytes > 0 {
		tx = uint64(txBytes)
	}
	addUint64(counter, rx, tx)
}

func addUint64(counter *counters, rxBytes, txBytes uint64) {
	if rxBytes > 0 {
		counter.rx.Add(rxBytes)
	}
	if txBytes > 0 {
		counter.tx.Add(txBytes)
	}
}

func snapshotCounters(counter *counters) map[string]uint64 {
	return map[string]uint64{
		"rx_bytes": counter.rx.Load(),
		"tx_bytes": counter.tx.Load(),
	}
}
