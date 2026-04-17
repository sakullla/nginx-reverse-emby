package backends

import (
	"context"
	"fmt"
	"math"
	"math/rand"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	dnsCacheTTL                         = 30 * time.Second
	failureBackoffBase                  = time.Second
	failureBackoffLimit                 = 60 * time.Second
	observationWindow                   = 24 * time.Hour
	observationBuckets                  = 24
	observationAlpha                    = 0.35
	recoveryWindow                      = 2 * time.Minute
	slowStartDuration                   = 60 * time.Second
	resolvedSlowStart                   = 30 * time.Second
	minRecentSamples                    = 3
	minRecoverSuccesses                 = 2
	minConfidence                       = 0.25
	coldExplorationPct                  = 10
	recoveringExplPct                   = 15
	combinedExplPct                     = 20
	slowStartMinFactor                  = 0.30
	outlierPenaltyFactor                = 0.35
	throughputSmallBytes          int64 = 128 * 1024
	throughputLargeBytes          int64 = 1024 * 1024
	throughputMinDuration               = 80 * time.Millisecond
	mediumThroughputWeight              = 0.5
	largeThroughputWeight               = 1.0
	minQualifiedThroughputSamples       = 2
	minQualifiedThroughputWeight        = 1.5
)

type Cache struct {
	mu           sync.Mutex
	resolver     Resolver
	now          func() time.Time
	randomIntn   func(n int) int
	backoffBase  time.Duration
	backoffLimit time.Duration

	dnsCache   map[string]dnsCacheEntry
	failures   map[string]failureEntry
	roundRobin map[string]int
	observed   map[string]candidateObservation
}

type dnsCacheEntry struct {
	ips       []string
	expiresAt time.Time
}

type failureEntry struct {
	consecutive int
	retryAfter  time.Time
}

type candidateObservation struct {
	counts             [observationBuckets]observationBucket
	lastLatency        time.Duration
	latencyEstimate    time.Duration
	lastSuccessAt      time.Time
	lastSuccessCount   int
	hadBackoff         bool
	recoveryUntil      time.Time
	recoverySuccesses  int
	slowStartUntil     time.Time
	slowStartStartedAt time.Time
	outlierUntil       time.Time
	lastThroughput     float64
	throughputEstimate float64
	lastThroughputAt   time.Time
	outlierThroughput  bool
	lastUpdated        time.Time
}

type observationBucket struct {
	hour                       int64
	successes                  int
	failures                   int
	smallWeight                float64
	mediumWeight               float64
	largeWeight                float64
	qualifiedThroughputSamples int
	qualifiedThroughputWeight  float64
}

type candidatePreference struct {
	inBackoff        bool
	state            string
	stability        float64
	latency          time.Duration
	hasLatency       bool
	bandwidth        float64
	hasBandwidth     bool
	confidence       float64
	outlier          bool
	slowStartActive  bool
	slowStartFactor  float64
	performance      float64
	trafficShareHint string
}

type trafficMix struct {
	small float64
	bulk  float64
}

func NewCache(cfg Config) *Cache {
	resolver := cfg.Resolver
	if resolver == nil {
		resolver = net.DefaultResolver
	}

	nowFn := cfg.Now
	if nowFn == nil {
		nowFn = time.Now
	}

	randomIntn := cfg.RandomIntn
	if randomIntn == nil {
		source := rand.New(rand.NewSource(time.Now().UnixNano()))
		var randomMu sync.Mutex
		randomIntn = func(n int) int {
			randomMu.Lock()
			defer randomMu.Unlock()
			return source.Intn(n)
		}
	}

	backoffBase := cfg.FailureBackoffBase
	if backoffBase <= 0 {
		backoffBase = failureBackoffBase
	}
	backoffLimit := cfg.FailureBackoffLimit
	if backoffLimit <= 0 {
		backoffLimit = failureBackoffLimit
	}
	if backoffBase > backoffLimit {
		backoffBase = backoffLimit
	}

	return &Cache{
		resolver:     resolver,
		now:          nowFn,
		randomIntn:   randomIntn,
		backoffBase:  backoffBase,
		backoffLimit: backoffLimit,
		dnsCache:     make(map[string]dnsCacheEntry),
		failures:     make(map[string]failureEntry),
		roundRobin:   make(map[string]int),
		observed:     make(map[string]candidateObservation),
	}
}

func (c *Cache) Clone() *Cache {
	if c == nil {
		return nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	clone := &Cache{
		resolver:     c.resolver,
		now:          c.now,
		randomIntn:   c.randomIntn,
		backoffBase:  c.backoffBase,
		backoffLimit: c.backoffLimit,
		dnsCache:     make(map[string]dnsCacheEntry, len(c.dnsCache)),
		failures:     make(map[string]failureEntry, len(c.failures)),
		roundRobin:   make(map[string]int, len(c.roundRobin)),
		observed:     make(map[string]candidateObservation, len(c.observed)),
	}
	for key, entry := range c.dnsCache {
		clone.dnsCache[key] = dnsCacheEntry{
			ips:       append([]string(nil), entry.ips...),
			expiresAt: entry.expiresAt,
		}
	}
	for key, entry := range c.failures {
		clone.failures[key] = entry
	}
	for key, entry := range c.roundRobin {
		clone.roundRobin[key] = entry
	}
	for key, entry := range c.observed {
		clone.observed[key] = entry
	}
	return clone
}

func (c *Cache) Resolve(ctx context.Context, endpoint Endpoint) ([]Candidate, error) {
	host := strings.TrimSpace(endpoint.Host)
	if host == "" {
		return nil, fmt.Errorf("backend host is required")
	}
	if endpoint.Port <= 0 || endpoint.Port > 65535 {
		return nil, fmt.Errorf("backend port is out of range: %d", endpoint.Port)
	}

	ip := net.ParseIP(host)
	if ip != nil {
		address := net.JoinHostPort(ip.String(), strconv.Itoa(endpoint.Port))
		return []Candidate{{
			Endpoint: endpoint,
			Address:  address,
		}}, nil
	}

	ips, err := c.lookupHost(ctx, strings.ToLower(host))
	if err != nil {
		return nil, err
	}

	candidates := make([]Candidate, 0, len(ips))
	for _, resolvedIP := range ips {
		candidates = append(candidates, Candidate{
			Endpoint: endpoint,
			Address:  net.JoinHostPort(resolvedIP, strconv.Itoa(endpoint.Port)),
		})
	}
	return candidates, nil
}

func (c *Cache) Order(scope, strategy string, candidates []Candidate) []Candidate {
	ordered := make([]Candidate, len(candidates))
	copy(ordered, candidates)

	if len(ordered) <= 1 {
		return ordered
	}

	switch normalizeStrategy(strategy) {
	case StrategyRandom:
		for i := len(ordered) - 1; i > 0; i-- {
			j := c.randomIntn(i + 1)
			if j < 0 || j > i {
				j = 0
			}
			ordered[i], ordered[j] = ordered[j], ordered[i]
		}
		return ordered
	case StrategyAdaptive:
		now := c.now()
		preferenceState := make(map[string]candidatePreference, len(ordered))
		hasCold := false
		hasRecovering := false

		c.mu.Lock()
		for _, candidate := range ordered {
			key := strings.TrimSpace(candidate.Address)
			observationKey := BackendObservationKey(scope, key)
			observation := c.observed[observationKey]
			preference := observation.preference(now, true)
			preferenceState[key] = preference
			switch preference.state {
			case ObservationStateCold:
				hasCold = true
			case ObservationStateRecovering:
				hasRecovering = true
			}
		}
		c.mu.Unlock()

		sort.SliceStable(ordered, func(i, j int) bool {
			leftKey := strings.TrimSpace(ordered[i].Address)
			rightKey := strings.TrimSpace(ordered[j].Address)
			left := preferenceState[leftKey]
			right := preferenceState[rightKey]
			if left.inBackoff != right.inBackoff {
				return !left.inBackoff
			}
			if left.stability != right.stability {
				return left.stability > right.stability
			}
			if left.performance != right.performance {
				return left.performance > right.performance
			}
			return false
		})
		ordered = c.maybePromoteExplorationCandidate(ordered, preferenceState, hasCold, hasRecovering)
		return ordered
	default:
		offset := c.roundRobinOffset(scope, len(ordered))
		if offset == 0 {
			return ordered
		}
		rotated := make([]Candidate, 0, len(ordered))
		rotated = append(rotated, ordered[offset:]...)
		rotated = append(rotated, ordered[:offset]...)
		return rotated
	}
}

func (c *Cache) ObserveSuccess(address string, latency time.Duration) {
	c.ObserveTransferSuccess(address, latency, 0, 0)
}

func (c *Cache) ObserveBackendSuccess(scope string, latency time.Duration, totalDuration time.Duration, bytesTransferred int64) {
	key := strings.TrimSpace(scope)
	if key == "" {
		return
	}

	now := c.now()

	c.mu.Lock()
	defer c.mu.Unlock()

	entry := c.observed[key]
	entry.recordSuccess(now, latency, totalDuration, bytesTransferred, c.slowStartWindowForKey(key))
	c.observed[key] = entry
	delete(c.failures, key)
}

func (c *Cache) ObserveBackendFailure(scope string) {
	key := strings.TrimSpace(scope)
	if key == "" {
		return
	}

	now := c.now()

	c.mu.Lock()
	defer c.mu.Unlock()

	c.applyFailureLocked(key, now)
}

func (c *Cache) ObserveTransferSuccess(address string, latency time.Duration, totalDuration time.Duration, bytesTransferred int64) {
	key := strings.TrimSpace(address)
	if key == "" {
		return
	}

	now := c.now()

	c.mu.Lock()
	defer c.mu.Unlock()

	entry := c.observed[key]
	entry.recordSuccess(now, latency, totalDuration, bytesTransferred, c.slowStartWindowForKey(key))
	c.observed[key] = entry
	delete(c.failures, key)
}

func (c *Cache) PreferResolvedCandidates(candidates []Candidate) []Candidate {
	return c.preferResolvedCandidates(candidates, true)
}

func (c *Cache) PreferResolvedCandidatesLatencyOnly(candidates []Candidate) []Candidate {
	return c.preferResolvedCandidates(candidates, false)
}

func (c *Cache) preferResolvedCandidates(candidates []Candidate, allowThroughput bool) []Candidate {
	ordered := make([]Candidate, len(candidates))
	copy(ordered, candidates)

	now := c.now()
	preferenceState := make(map[string]candidatePreference, len(ordered))

	c.mu.Lock()
	for _, candidate := range ordered {
		key := strings.TrimSpace(candidate.Address)
		observation := c.observed[key]
		preference := observation.preference(now, allowThroughput)
		preferenceState[key] = preference
	}
	c.mu.Unlock()

	sort.SliceStable(ordered, func(i, j int) bool {
		leftKey := strings.TrimSpace(ordered[i].Address)
		rightKey := strings.TrimSpace(ordered[j].Address)
		left := preferenceState[leftKey]
		right := preferenceState[rightKey]
		if left.inBackoff != right.inBackoff {
			return !left.inBackoff
		}
		if left.stability != right.stability {
			return left.stability > right.stability
		}
		if left.performance != right.performance {
			return left.performance > right.performance
		}
		return false
	})
	ordered = c.maybePromoteExplorationCandidate(ordered, preferenceState, c.hasState(preferenceState, ObservationStateCold), c.hasState(preferenceState, ObservationStateRecovering))

	return ordered
}

func (c *Cache) MarkFailure(address string) time.Duration {
	key := strings.TrimSpace(address)
	if key == "" {
		return 0
	}

	now := c.now()

	c.mu.Lock()
	defer c.mu.Unlock()

	return c.applyFailureLocked(key, now)
}

func (c *Cache) MarkSuccess(address string) {
	key := strings.TrimSpace(address)
	if key == "" {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.failures, key)
}

func (c *Cache) IsInBackoff(address string) bool {
	key := strings.TrimSpace(address)
	if key == "" {
		return false
	}

	now := c.now()

	c.mu.Lock()
	defer c.mu.Unlock()

	entry, ok := c.failures[key]
	if !ok {
		return false
	}
	return now.Before(entry.retryAfter)
}

func (c *Cache) lookupHost(ctx context.Context, host string) ([]string, error) {
	now := c.now()

	c.mu.Lock()
	entry, ok := c.dnsCache[host]
	if ok && now.Before(entry.expiresAt) {
		ips := append([]string(nil), entry.ips...)
		c.mu.Unlock()
		return ips, nil
	}
	c.mu.Unlock()

	resolved, err := c.resolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	if len(resolved) == 0 {
		return nil, fmt.Errorf("no IPs resolved for host %q", host)
	}

	dedup := make(map[string]struct{}, len(resolved))
	ips := make([]string, 0, len(resolved))
	for _, candidate := range resolved {
		if candidate.IP == nil {
			continue
		}
		ip := candidate.IP.String()
		if _, exists := dedup[ip]; exists {
			continue
		}
		dedup[ip] = struct{}{}
		ips = append(ips, ip)
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("no valid IPs resolved for host %q", host)
	}

	c.mu.Lock()
	c.dnsCache[host] = dnsCacheEntry{
		ips:       append([]string(nil), ips...),
		expiresAt: now.Add(dnsCacheTTL),
	}
	c.mu.Unlock()

	return ips, nil
}

func (c *Cache) roundRobinOffset(scope string, total int) int {
	c.mu.Lock()
	defer c.mu.Unlock()

	key := strings.TrimSpace(scope)
	offset := c.roundRobin[key]
	if total > 0 {
		offset %= total
	}
	c.roundRobin[key] = offset + 1
	return offset
}

func normalizeStrategy(strategy string) string {
	switch strings.ToLower(strings.TrimSpace(strategy)) {
	case StrategyRandom:
		return StrategyRandom
	case StrategyAdaptive:
		return StrategyAdaptive
	default:
		return StrategyRoundRobin
	}
}

func (c *Cache) observationFor(address string) candidateObservation {
	key := strings.TrimSpace(address)
	if key == "" {
		return candidateObservation{}
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	return c.observed[key]
}

func (c *Cache) Summary(key string) ObservationSummary {
	normalized := strings.TrimSpace(key)
	if normalized == "" {
		return ObservationSummary{}
	}

	now := c.now()

	c.mu.Lock()
	observation := c.observed[normalized]
	c.mu.Unlock()

	successes, failures := observation.recentCounts(now)
	preference := observation.preference(now, true)

	return ObservationSummary{
		Stability:        preference.stability,
		RecentSucceeded:  successes,
		RecentFailed:     failures,
		Latency:          preference.latency,
		HasLatency:       preference.hasLatency,
		Bandwidth:        preference.bandwidth,
		HasBandwidth:     preference.hasBandwidth,
		PerformanceScore: preference.performance,
		InBackoff:        preference.inBackoff,
		State:            preference.state,
		SampleConfidence: preference.confidence,
		SlowStartActive:  preference.slowStartActive,
		Outlier:          preference.outlier,
		TrafficShareHint: preference.trafficShareHint,
	}
}

func (o *candidateObservation) recordSuccess(now time.Time, latency time.Duration, totalDuration time.Duration, bytesTransferred int64, slowStartWindow time.Duration) {
	o.recordOutcome(now, true)
	o.lastSuccessCount++
	successes, failures := o.recentCounts(now)
	totalRecent := successes + failures
	if o.hadBackoff && !o.recoveryUntil.IsZero() && now.After(o.slowStartStartedAt) && now.Before(o.recoveryUntil) {
		o.recoverySuccesses++
		if o.recoverySuccesses >= minRecoverSuccesses && totalRecent >= minRecentSamples {
			o.hadBackoff = false
			o.recoveryUntil = time.Time{}
			o.recoverySuccesses = 0
			o.slowStartStartedAt = now
			o.slowStartUntil = now.Add(slowStartWindow)
		}
	}
	if !o.outlierUntil.IsZero() && now.After(o.outlierUntil) {
		o.outlierUntil = time.Time{}
	}
	if latency > 0 {
		o.lastLatency = latency
		o.latencyEstimate = blendDuration(o.latencyEstimate, latency)
		o.lastSuccessAt = now
	}
	weight, qualified, bucket := classifyThroughputSample(totalDuration, bytesTransferred)
	o.recordTrafficMix(now, bucket, weight)
	if qualified {
		wasReady := o.qualifiedThroughputReady(now)
		throughput := float64(bytesTransferred) / totalDuration.Seconds()
		if throughput > 0 {
			previousEstimate := o.throughputEstimate
			o.lastThroughput = throughput
			o.throughputEstimate = blendFloat(o.throughputEstimate, throughput)
			o.lastThroughputAt = now
			o.recordQualifiedThroughput(now, weight)
			o.outlierThroughput = wasReady && o.qualifiedThroughputReady(now)
			if o.outlierThroughput && previousEstimate > 0 && throughput < 0.5*previousEstimate {
				o.outlierUntil = now.Add(30 * time.Second)
			}
		}
	}
	o.lastUpdated = now
}

func (o *candidateObservation) recordFailure(now time.Time) {
	o.recordOutcome(now, false)
	o.lastSuccessCount = 0
	if o.hadBackoff && !o.recoveryUntil.IsZero() && now.Before(o.recoveryUntil) {
		o.recoverySuccesses = 0
	}
	o.lastUpdated = now
}

func (o *candidateObservation) recordOutcome(now time.Time, success bool) {
	hour := now.UTC().Unix() / int64(time.Hour/time.Second)
	index := int(hour % observationBuckets)
	if o.counts[index].hour != hour {
		o.counts[index] = observationBucket{hour: hour}
	}
	if success {
		o.counts[index].successes++
		return
	}
	o.counts[index].failures++
}

func (o *candidateObservation) recordTrafficMix(now time.Time, bucket string, weight float64) {
	if bucket == "" || weight <= 0 {
		return
	}

	entry := o.bucketFor(now)
	switch bucket {
	case "small":
		entry.smallWeight += weight
	case "medium":
		entry.mediumWeight += weight
	case "large":
		entry.largeWeight += weight
	}
}

func (o *candidateObservation) recordQualifiedThroughput(now time.Time, weight float64) {
	if weight <= 0 {
		return
	}

	entry := o.bucketFor(now)
	entry.qualifiedThroughputSamples++
	entry.qualifiedThroughputWeight += weight
}

func (o *candidateObservation) bucketFor(now time.Time) *observationBucket {
	hour := now.UTC().Unix() / int64(time.Hour/time.Second)
	index := int(hour % observationBuckets)
	if o.counts[index].hour != hour {
		o.counts[index] = observationBucket{hour: hour}
	}
	return &o.counts[index]
}

func (o candidateObservation) preference(now time.Time, allowThroughput bool) candidatePreference {
	successes, failures := o.recentCounts(now)
	inBackoff := o.inBackoff(now)
	state := o.state(now, successes, failures, inBackoff)
	confidence := sampleConfidence(successes, failures)
	slowFactor, slowStartActive := slowStartFactor(now, o.slowStartStartedAt, o.slowStartUntil)
	preference := candidatePreference{
		inBackoff:       inBackoff,
		stability:       stabilityScore(successes, failures),
		state:           state,
		confidence:      confidence,
		slowStartActive: slowStartActive,
		slowStartFactor: slowFactor,
	}
	if latency, ok := o.latencyFor(now); ok {
		preference.latency = latency
		preference.hasLatency = true
	}
	if allowThroughput {
		preference.outlier = o.isOutlier(now, failures)
		if bandwidth, ok := o.bandwidthFor(now); ok {
			preference.bandwidth = bandwidth
			preference.hasBandwidth = true
		}
	}
	preference.performance = effectivePerformance(preference, allowThroughput, o.recentTrafficMix(now))
	switch {
	case preference.inBackoff:
		preference.trafficShareHint = "blocked"
	case preference.slowStartActive:
		preference.trafficShareHint = "recovery"
	case preference.state == ObservationStateCold:
		preference.trafficShareHint = "cold"
	default:
		preference.trafficShareHint = "normal"
	}
	return preference
}

func stabilityScore(successes, failures int) float64 {
	switch {
	case successes <= 0 && failures <= 0:
		return 0.5
	case successes <= 0:
		return 0
	default:
		return float64(successes) / float64(successes+failures)
	}
}

func sampleConfidence(successes, failures int) float64 {
	total := successes + failures
	switch {
	case total <= 0:
		return 0
	case total == 1:
		return 0.05
	case total == 2:
		return 0.1
	case total == 3:
		return 0.55
	case total == 4:
		return 0.8
	default:
		return 1
	}
}

func slowStartFactor(now, startedAt, until time.Time) (float64, bool) {
	if startedAt.IsZero() || until.IsZero() || now.Before(startedAt) || !now.Before(until) {
		return 1, false
	}
	total := until.Sub(startedAt)
	if total <= 0 {
		return 1, false
	}
	progress := float64(now.Sub(startedAt)) / float64(total)
	if progress < 0 {
		progress = 0
	}
	if progress > 1 {
		progress = 1
	}
	return slowStartMinFactor + (1-slowStartMinFactor)*progress, true
}

func outlierPenalty(outlier bool) float64 {
	if outlier {
		return outlierPenaltyFactor
	}
	return 1
}

func (o candidateObservation) state(now time.Time, recentSuccesses, recentFailures int, inBackoff bool) string {
	if o.hadBackoff && !o.recoveryUntil.IsZero() && now.After(o.slowStartStartedAt) && now.Before(o.recoveryUntil) {
		return ObservationStateRecovering
	}
	if inBackoff || recentSuccesses <= 0 || recentSuccesses+recentFailures < minRecentSamples {
		return ObservationStateCold
	}
	return ObservationStateWarm
}

func (o candidateObservation) isOutlier(now time.Time, failures int) bool {
	if !o.qualifiedThroughputReady(now) || !o.outlierThroughput {
		return false
	}
	if !o.outlierUntil.IsZero() && now.Before(o.outlierUntil) {
		return true
	}
	if o.lastThroughput > 0 && o.throughputEstimate > 0 {
		return o.lastThroughput < 0.5*o.throughputEstimate || o.lastThroughput > 2.5*o.throughputEstimate
	}
	return false
}

func effectivePerformance(preference candidatePreference, allowThroughput bool, mix trafficMix) float64 {
	if !preference.hasLatency && !preference.hasBandwidth {
		return 0
	}
	slowStart := preference.slowStartFactor
	if slowStart <= 0 {
		slowStart = 1
	}
	performance := performanceScore(preference, allowThroughput, mix)
	performance *= confidenceFactor(preference.confidence)
	return performance * slowStart * outlierPenalty(preference.outlier)
}

func (o candidateObservation) recentCounts(now time.Time) (int, int) {
	currentHour := now.UTC().Unix() / int64(time.Hour/time.Second)
	var successes int
	var failures int
	for _, bucket := range o.counts {
		age := currentHour - bucket.hour
		if age < 0 || age >= observationBuckets {
			continue
		}
		successes += bucket.successes
		failures += bucket.failures
	}
	return successes, failures
}

func (o candidateObservation) recentTrafficMix(now time.Time) trafficMix {
	currentHour := now.UTC().Unix() / int64(time.Hour/time.Second)
	var mix trafficMix
	for _, bucket := range o.counts {
		age := currentHour - bucket.hour
		if age < 0 || age >= observationBuckets {
			continue
		}
		mix.small += bucket.smallWeight
		mix.bulk += bucket.mediumWeight + bucket.largeWeight
	}
	return mix
}

func (o candidateObservation) latencyFor(now time.Time) (time.Duration, bool) {
	if o.lastSuccessAt.IsZero() || now.Sub(o.lastSuccessAt) >= observationWindow || o.latencyEstimate <= 0 {
		return 0, false
	}
	return o.latencyEstimate, true
}

func (o candidateObservation) bandwidthFor(now time.Time) (float64, bool) {
	if !o.qualifiedThroughputReady(now) {
		return 0, false
	}
	return o.throughputEstimate, true
}

func (o candidateObservation) qualifiedThroughputReady(now time.Time) bool {
	samples, weight := o.recentQualifiedThroughput(now)
	if samples < minQualifiedThroughputSamples || weight < minQualifiedThroughputWeight {
		return false
	}
	if o.lastThroughputAt.IsZero() || now.Sub(o.lastThroughputAt) >= observationWindow || o.throughputEstimate <= 0 {
		return false
	}
	return true
}

func (o candidateObservation) recentQualifiedThroughput(now time.Time) (int, float64) {
	currentHour := now.UTC().Unix() / int64(time.Hour/time.Second)
	var samples int
	var weight float64
	for _, bucket := range o.counts {
		age := currentHour - bucket.hour
		if age < 0 || age >= observationBuckets {
			continue
		}
		samples += bucket.qualifiedThroughputSamples
		weight += bucket.qualifiedThroughputWeight
	}
	return samples, weight
}

func classifyThroughputSample(transferDuration time.Duration, bytesTransferred int64) (float64, bool, string) {
	if bytesTransferred <= 0 || transferDuration <= 0 {
		return 0, false, ""
	}
	if bytesTransferred < throughputSmallBytes || transferDuration < throughputMinDuration {
		return 1.0, false, "small"
	}
	if bytesTransferred < throughputLargeBytes {
		return mediumThroughputWeight, true, "medium"
	}
	return largeThroughputWeight, true, "large"
}

func throughputWeights(mix trafficMix) (latencyWeight float64, throughputWeight float64) {
	total := mix.small + mix.bulk
	if total <= 0 {
		return 0.75, 0.25
	}
	bulkBias := math.Max(0, math.Min(1, mix.bulk/total))
	return 0.75 - 0.40*bulkBias, 0.25 + 0.40*bulkBias
}

func performanceScore(preference candidatePreference, allowThroughput bool, mix trafficMix) float64 {
	latencyScore := 0.0
	if preference.hasLatency && preference.latency > 0 {
		latencyMillis := float64(preference.latency) / float64(time.Millisecond)
		latencyScore = 1 / (1 + latencyMillis/50.0)
	}
	if !allowThroughput || !preference.hasBandwidth || preference.bandwidth <= 0 {
		return latencyScore
	}
	throughputMBps := preference.bandwidth / (1024.0 * 1024.0)
	throughputScore := math.Log1p(throughputMBps) / math.Log1p(16)
	if !preference.hasLatency || preference.latency <= 0 {
		return throughputScore
	}
	latencyWeight, throughputWeight := throughputWeights(mix)
	return latencyWeight*latencyScore + throughputWeight*throughputScore
}

func confidenceFactor(confidence float64) float64 {
	if confidence <= 0 {
		return 0.25
	}
	if confidence > 1 {
		confidence = 1
	}
	return 0.25 + 0.75*confidence
}

func (c *Cache) maybePromoteExplorationCandidate(ordered []Candidate, preferences map[string]candidatePreference, hasCold bool, hasRecovering bool) []Candidate {
	budget := c.chooseExplorationBudget(hasRecovering, hasCold)
	if budget == 0 {
		return ordered
	}
	if c.randomIntn(100) >= budget {
		return ordered
	}

	target := ObservationStateRecovering
	if !hasRecovering {
		target = ObservationStateCold
	}
	for i, candidate := range ordered {
		pref := preferences[strings.TrimSpace(candidate.Address)]
		if pref.inBackoff || pref.state != target {
			continue
		}
		if i == 0 {
			return ordered
		}
		rotated := make([]Candidate, 0, len(ordered))
		rotated = append(rotated, ordered[i:]...)
		rotated = append(rotated, ordered[:i]...)
		return rotated
	}
	return ordered
}

func (c *Cache) hasState(preferences map[string]candidatePreference, state string) bool {
	for _, preference := range preferences {
		if preference.state == state {
			return true
		}
	}
	return false
}

func (c *Cache) chooseExplorationBudget(hasRecovering bool, hasCold bool) int {
	switch {
	case hasRecovering && hasCold:
		return combinedExplPct
	case hasRecovering:
		return recoveringExplPct
	case hasCold:
		return coldExplorationPct
	default:
		return 0
	}
}

func (c *Cache) slowStartWindowForKey(key string) time.Duration {
	if strings.HasPrefix(strings.TrimSpace(key), backendObservationPrefix) {
		return slowStartDuration
	}
	return resolvedSlowStart
}

func (c *Cache) backoffDuration(consecutive int) time.Duration {
	backoff := c.backoffBase
	for i := 1; i < consecutive; i++ {
		if backoff >= c.backoffLimit/2 {
			backoff = c.backoffLimit
			break
		}
		backoff *= 2
	}
	if backoff > c.backoffLimit {
		backoff = c.backoffLimit
	}
	return backoff
}

func (c *Cache) applyFailureLocked(key string, now time.Time) time.Duration {
	entry := c.failures[key]
	entry.consecutive++

	backoff := c.backoffDuration(entry.consecutive)
	entry.retryAfter = now.Add(backoff)
	c.failures[key] = entry

	observed := c.observed[key]
	observed.recordFailure(now)
	observed.hadBackoff = true
	observed.recoveryUntil = entry.retryAfter.Add(recoveryWindow)
	observed.recoverySuccesses = 0
	observed.slowStartStartedAt = observed.recoveryUntil.Add(-recoveryWindow)
	observed.slowStartUntil = observed.recoveryUntil
	c.observed[key] = observed

	return backoff
}

func (o candidateObservation) inBackoff(now time.Time) bool {
	return !o.recoveryUntil.IsZero() && now.Before(o.slowStartStartedAt)
}

func blendDuration(current, next time.Duration) time.Duration {
	if current <= 0 {
		return next
	}
	return time.Duration((1.0-observationAlpha)*float64(current) + observationAlpha*float64(next))
}

func blendFloat(current, next float64) float64 {
	if current <= 0 {
		return next
	}
	return (1.0-observationAlpha)*current + observationAlpha*next
}
