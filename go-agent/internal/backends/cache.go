package backends

import (
	"context"
	"fmt"
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
	successes   int
	failures    int
	lastLatency time.Duration
	lastUpdated time.Time
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
	key := strings.TrimSpace(address)
	if key == "" {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	entry := c.observed[key]
	entry.successes++
	entry.lastLatency = latency
	entry.lastUpdated = c.now()
	c.observed[key] = entry
	delete(c.failures, key)
}

func (c *Cache) PreferResolvedCandidates(candidates []Candidate) []Candidate {
	ordered := make([]Candidate, len(candidates))
	copy(ordered, candidates)

	sort.SliceStable(ordered, func(i, j int) bool {
		left := c.observationFor(ordered[i].Address)
		right := c.observationFor(ordered[j].Address)
		if left.successes != right.successes {
			return left.successes > right.successes
		}
		if left.failures != right.failures {
			return left.failures < right.failures
		}
		if left.successes > 0 && right.successes > 0 && left.lastLatency != right.lastLatency {
			return left.lastLatency < right.lastLatency
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
	observed.failures++
	observed.lastUpdated = now
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
