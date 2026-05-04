package traffic

import (
	"strconv"
	"sync"
	"sync/atomic"
)

const recorderFlushThreshold uint64 = 1024 * 1024

type counters struct {
	rx atomic.Uint64
	tx atomic.Uint64
}

type Recorder struct {
	counter    *counters
	scoped     *counters
	rx         atomic.Uint64
	tx         atomic.Uint64
	generation atomic.Uint64
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
	recorderRegistry      = recorderSet{recorders: map[*Recorder]struct{}{}}
	enabled               atomic.Bool
	resetGeneration       atomic.Uint64
)

func init() {
	enabled.Store(true)
}

func SetEnabled(value bool) {
	if !value {
		Reset()
	}
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
	return newRecorder(&httpCounters, nil)
}

func NewL4Recorder() *Recorder {
	return newRecorder(&l4Counters, nil)
}

func NewRelayRecorder() *Recorder {
	return newRecorder(&relayCounters, nil)
}

func NewHTTPRuleRecorder(ruleID int) *Recorder {
	return newRecorder(&httpCounters, httpRuleCounters.counterFor(ruleID))
}

func NewL4RuleRecorder(ruleID int) *Recorder {
	return newRecorder(&l4Counters, l4RuleCounters.counterFor(ruleID))
}

func NewRelayListenerRecorder(listenerID int) *Recorder {
	return newRecorder(&relayCounters, relayListenerCounters.counterFor(listenerID))
}

func newRecorder(counter *counters, scoped *counters) *Recorder {
	recorder := &Recorder{counter: counter, scoped: scoped}
	recorder.generation.Store(resetGeneration.Load())
	return recorder
}

func (r *Recorder) Add(rxBytes, txBytes int64) {
	if r == nil || r.counter == nil || !Enabled() {
		return
	}
	r.discardPendingAfterReset()
	var added uint64
	if rxBytes > 0 {
		added += uint64(rxBytes)
		r.rx.Add(uint64(rxBytes))
	}
	if txBytes > 0 {
		added += uint64(txBytes)
		r.tx.Add(uint64(txBytes))
	}
	if added > 0 && r.rx.Load()+r.tx.Load() >= recorderFlushThreshold {
		r.Flush()
		return
	}
	if added > 0 {
		recorderRegistry.add(r)
	}
}

func (r *Recorder) Flush() {
	if r == nil || r.counter == nil || !Enabled() {
		return
	}
	r.discardPendingAfterReset()
	recorderRegistry.delete(r)
	rx := r.rx.Swap(0)
	tx := r.tx.Swap(0)
	addUint64(r.counter, rx, tx)
	if r.scoped != nil {
		addUint64(r.scoped, rx, tx)
	}
}

func (r *Recorder) Close() {
	if r == nil || r.counter == nil {
		return
	}
	r.Flush()
}

func (r *Recorder) FlushIfPendingBelow(maxBytes uint64) {
	if r == nil || r.counter == nil || !Enabled() {
		return
	}
	r.discardPendingAfterReset()
	if r.rx.Load()+r.tx.Load() <= maxBytes {
		r.Flush()
	}
}

func (r *Recorder) discardPendingAfterReset() {
	current := resetGeneration.Load()
	if r.generation.Load() == current {
		return
	}
	r.rx.Store(0)
	r.tx.Store(0)
	r.generation.Store(current)
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
	recorderRegistry.flushDirty()
	stats := snapshot()
	trafficStats := stats["traffic"].(map[string]any)
	total := trafficStats["total"].(map[string]uint64)
	if total["rx_bytes"] == 0 && total["tx_bytes"] == 0 {
		return nil
	}
	return stats
}

func Reset() {
	resetGeneration.Add(1)
	recorderRegistry.clear()
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

type recorderSet struct {
	mu        sync.RWMutex
	recorders map[*Recorder]struct{}
}

func (s *recorderSet) add(recorder *Recorder) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.recorders[recorder] = struct{}{}
}

func (s *recorderSet) delete(recorder *Recorder) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.recorders, recorder)
}

func (s *recorderSet) flushDirty() {
	s.mu.RLock()
	recorders := make([]*Recorder, 0, len(s.recorders))
	for recorder := range s.recorders {
		recorders = append(recorders, recorder)
	}
	s.mu.RUnlock()
	for _, recorder := range recorders {
		recorder.Flush()
	}
}

func (s *recorderSet) clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.recorders = map[*Recorder]struct{}{}
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
	for _, counter := range k.byID {
		counter.rx.Store(0)
		counter.tx.Store(0)
	}
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
