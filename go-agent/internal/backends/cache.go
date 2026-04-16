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
	dnsCacheTTL         = 30 * time.Second
	failureBackoffBase  = time.Second
	failureBackoffLimit = 60 * time.Second
	observationWindow   = 24 * time.Hour
	observationBuckets  = 24
	observationAlpha    = 0.35
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
	counts            [observationBuckets]observationBucket
	lastLatency       time.Duration
	latencyEstimate   time.Duration
	lastSuccessAt     time.Time
	lastBandwidth     float64
	bandwidthEstimate float64
	lastBandwidthAt   time.Time
	lastUpdated       time.Time
}

type observationBucket struct {
	hour      int64
	successes int
	failures  int
}

type candidatePreference struct {
	inBackoff    bool
	stability    float64
	latency      time.Duration
	hasLatency   bool
	bandwidth    float64
	hasBandwidth bool
	performance  float64
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
		randomIntn = rand.Intn
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

		c.mu.Lock()
		for _, candidate := range ordered {
			key := strings.TrimSpace(candidate.Address)
			observationKey := BackendObservationKey(scope, key)
			preferenceState[key] = c.observed[observationKey].preference(now)
		}
		c.mu.Unlock()

		sort.SliceStable(ordered, func(i, j int) bool {
			leftKey := strings.TrimSpace(ordered[i].Address)
			rightKey := strings.TrimSpace(ordered[j].Address)
			left := preferenceState[leftKey]
			right := preferenceState[rightKey]
			if left.stability != right.stability {
				return left.stability > right.stability
			}
			if left.performance != right.performance {
				return left.performance > right.performance
			}
			return false
		})
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
	entry.recordSuccess(now, latency, totalDuration, bytesTransferred)
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

	entry := c.observed[key]
	entry.recordFailure(now)
	c.observed[key] = entry
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
	entry.recordSuccess(now, latency, totalDuration, bytesTransferred)
	c.observed[key] = entry
	delete(c.failures, key)
}

func (c *Cache) PreferResolvedCandidates(candidates []Candidate) []Candidate {
	ordered := make([]Candidate, len(candidates))
	copy(ordered, candidates)

	now := c.now()
	backoffState := make(map[string]bool, len(ordered))
	preferenceState := make(map[string]candidatePreference, len(ordered))

	c.mu.Lock()
	for _, candidate := range ordered {
		key := strings.TrimSpace(candidate.Address)
		preferenceState[key] = c.observed[key].preference(now)
		if entry, ok := c.failures[key]; ok {
			backoffState[key] = now.Before(entry.retryAfter)
		}
	}
	c.mu.Unlock()

	sort.SliceStable(ordered, func(i, j int) bool {
		leftKey := strings.TrimSpace(ordered[i].Address)
		rightKey := strings.TrimSpace(ordered[j].Address)
		leftBackoff := backoffState[leftKey]
		rightBackoff := backoffState[rightKey]
		if leftBackoff != rightBackoff {
			return !leftBackoff
		}

		left := preferenceState[leftKey]
		right := preferenceState[rightKey]
		if left.stability != right.stability {
			return left.stability > right.stability
		}
		if left.performance != right.performance {
			return left.performance > right.performance
		}
		return false
	})

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

	entry := c.failures[key]
	entry.consecutive++

	observed := c.observed[key]
	observed.recordFailure(now)
	c.observed[key] = observed

	backoff := c.backoffBase
	for i := 1; i < entry.consecutive; i++ {
		if backoff >= c.backoffLimit/2 {
			backoff = c.backoffLimit
			break
		}
		backoff *= 2
	}
	if backoff > c.backoffLimit {
		backoff = c.backoffLimit
	}

	entry.retryAfter = now.Add(backoff)
	c.failures[key] = entry
	return backoff
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
	inBackoff := false
	if entry, ok := c.failures[normalized]; ok {
		inBackoff = now.Before(entry.retryAfter)
	}
	c.mu.Unlock()

	successes, failures := observation.recentCounts(now)
	preference := observation.preference(now)

	return ObservationSummary{
		Stability:        preference.stability,
		RecentSucceeded:  successes,
		RecentFailed:     failures,
		Latency:          preference.latency,
		HasLatency:       preference.hasLatency,
		Bandwidth:        preference.bandwidth,
		HasBandwidth:     preference.hasBandwidth,
		PerformanceScore: preference.performance,
		InBackoff:        inBackoff,
	}
}

func (o *candidateObservation) recordSuccess(now time.Time, latency time.Duration, totalDuration time.Duration, bytesTransferred int64) {
	o.recordOutcome(now, true)
	if latency > 0 {
		o.lastLatency = latency
		o.latencyEstimate = blendDuration(o.latencyEstimate, latency)
		o.lastSuccessAt = now
	}
	if totalDuration > 0 && bytesTransferred > 0 {
		bandwidth := float64(bytesTransferred) / totalDuration.Seconds()
		if bandwidth > 0 {
			o.lastBandwidth = bandwidth
			o.bandwidthEstimate = blendFloat(o.bandwidthEstimate, bandwidth)
			o.lastBandwidthAt = now
		}
	}
	o.lastUpdated = now
}

func (o *candidateObservation) recordFailure(now time.Time) {
	o.recordOutcome(now, false)
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

func (o candidateObservation) preference(now time.Time) candidatePreference {
	successes, failures := o.recentCounts(now)
	preference := candidatePreference{
		stability: stabilityScore(successes, failures),
	}
	if latency, ok := o.latencyFor(now); ok {
		preference.latency = latency
		preference.hasLatency = true
	}
	if bandwidth, ok := o.bandwidthFor(now); ok {
		preference.bandwidth = bandwidth
		preference.hasBandwidth = true
	}
	preference.performance = performanceScore(preference)
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

func (o candidateObservation) latencyFor(now time.Time) (time.Duration, bool) {
	if o.lastSuccessAt.IsZero() || now.Sub(o.lastSuccessAt) >= observationWindow || o.latencyEstimate <= 0 {
		return 0, false
	}
	return o.latencyEstimate, true
}

func (o candidateObservation) bandwidthFor(now time.Time) (float64, bool) {
	if o.lastBandwidthAt.IsZero() || now.Sub(o.lastBandwidthAt) >= observationWindow || o.bandwidthEstimate <= 0 {
		return 0, false
	}
	return o.bandwidthEstimate, true
}

func performanceScore(preference candidatePreference) float64 {
	latencyScore := 0.0
	switch {
	case preference.hasLatency && preference.latency > 0:
		latencyMillis := float64(preference.latency) / float64(time.Millisecond)
		latencyScore = 1 / (1 + latencyMillis/50.0)
	}

	bandwidthScore := 0.0
	switch {
	case preference.hasBandwidth && preference.bandwidth > 0:
		bandwidthMBps := preference.bandwidth / (1024.0 * 1024.0)
		bandwidthScore = math.Log1p(bandwidthMBps) / math.Log1p(16)
	}

	if preference.hasLatency && preference.hasBandwidth {
		return 0.45*latencyScore + 0.55*bandwidthScore
	}
	if preference.hasLatency {
		return latencyScore
	}
	if preference.hasBandwidth {
		return bandwidthScore
	}
	return 0
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
