package traffic

import (
	"strconv"
	"sync"
	"sync/atomic"
)

const recorderFlushThreshold uint64 = 64 * 1024

type counters struct {
	rx atomic.Uint64
	tx atomic.Uint64
}

type Recorder struct {
	counter *counters
	scoped  *counters
	rx      atomic.Uint64
	tx      atomic.Uint64
}

type keyedCounters struct {
	mu   sync.RWMutex
	byID map[int]*counters
}

var (
	httpCounters          counters
	l4Counters            counters
	relayCounters         counters
	httpRuleCounters      keyedCounters
	l4RuleCounters        keyedCounters
	relayListenerCounters keyedCounters
	enabled               atomic.Bool
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

func NewHTTPRuleRecorder(ruleID int) *Recorder {
	return &Recorder{counter: &httpCounters, scoped: httpRuleCounters.counterFor(ruleID)}
}

func NewL4RuleRecorder(ruleID int) *Recorder {
	return &Recorder{counter: &l4Counters, scoped: l4RuleCounters.counterFor(ruleID)}
}

func NewRelayListenerRecorder(listenerID int) *Recorder {
	return &Recorder{counter: &relayCounters, scoped: relayListenerCounters.counterFor(listenerID)}
}

func (r *Recorder) Add(rxBytes, txBytes int64) {
	if r == nil || r.counter == nil || !Enabled() {
		return
	}
	var added uint64
	if rxBytes > 0 {
		added += uint64(rxBytes)
		r.rx.Add(uint64(rxBytes))
	}
	if txBytes > 0 {
		added += uint64(txBytes)
		r.tx.Add(uint64(txBytes))
	}
	if added > 0 && (r.scoped != nil || r.rx.Load()+r.tx.Load() >= recorderFlushThreshold) {
		r.Flush()
	}
}

func (r *Recorder) Flush() {
	if r == nil || r.counter == nil || !Enabled() {
		return
	}
	rx := r.rx.Swap(0)
	tx := r.tx.Swap(0)
	addUint64(r.counter, rx, tx)
	if r.scoped != nil {
		addUint64(r.scoped, rx, tx)
	}
}

func Snapshot() map[string]any {
	if !Enabled() {
		return nil
	}
	return snapshot()
}

func SnapshotNonZero() map[string]any {
	if !Enabled() {
		return nil
	}
	stats := snapshot()
	trafficStats := stats["traffic"].(map[string]any)
	total := trafficStats["total"].(map[string]uint64)
	if total["rx_bytes"] == 0 && total["tx_bytes"] == 0 {
		return nil
	}
	return stats
}

func Reset() {
	httpCounters.rx.Store(0)
	httpCounters.tx.Store(0)
	l4Counters.rx.Store(0)
	l4Counters.tx.Store(0)
	relayCounters.rx.Store(0)
	relayCounters.tx.Store(0)
	httpRuleCounters.reset()
	l4RuleCounters.reset()
	relayListenerCounters.reset()
}

func (k *keyedCounters) counterFor(id int) *counters {
	if id <= 0 {
		return nil
	}
	k.mu.RLock()
	counter := k.byID[id]
	k.mu.RUnlock()
	if counter != nil {
		return counter
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.byID == nil {
		k.byID = make(map[int]*counters)
	}
	if counter = k.byID[id]; counter == nil {
		counter = &counters{}
		k.byID[id] = counter
	}
	return counter
}

func (k *keyedCounters) reset() {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.byID = nil
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

func snapshotKeyedCounters(k *keyedCounters) map[string]map[string]uint64 {
	out := map[string]map[string]uint64{}
	k.mu.RLock()
	defer k.mu.RUnlock()
	for id, counter := range k.byID {
		bucket := snapshotCounters(counter)
		if bucket["rx_bytes"] == 0 && bucket["tx_bytes"] == 0 {
			continue
		}
		out[strconv.Itoa(id)] = bucket
	}
	return out
}

func snapshot() map[string]any {
	http := snapshotCounters(&httpCounters)
	l4 := snapshotCounters(&l4Counters)
	relay := snapshotCounters(&relayCounters)
	total := map[string]uint64{
		"rx_bytes": http["rx_bytes"] + l4["rx_bytes"] + relay["rx_bytes"],
		"tx_bytes": http["tx_bytes"] + l4["tx_bytes"] + relay["tx_bytes"],
	}
	return map[string]any{
		"traffic": map[string]any{
			"total":           total,
			"http":            http,
			"l4":              l4,
			"relay":           relay,
			"http_rules":      snapshotKeyedCounters(&httpRuleCounters),
			"l4_rules":        snapshotKeyedCounters(&l4RuleCounters),
			"relay_listeners": snapshotKeyedCounters(&relayListenerCounters),
		},
	}
}
