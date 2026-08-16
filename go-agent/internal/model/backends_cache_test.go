//go:build !integration

package model

import (
	"context"
	"errors"

	"net"
	"reflect"

	"testing"
	"time"
)

func TestCacheResolveUsesFixedDNSCacheTTL(t *testing.T) {
	base := time.Date(2026, 4, 9, 10, 0, 0, 0, time.UTC)
	now := base
	resolver := &stubResolver{
		results: [][]net.IPAddr{
			{{IP: net.ParseIP("10.0.0.1")}},
			{{IP: net.ParseIP("10.0.0.2")}},
		},
	}

	cache := NewCache(BackendCacheConfig{
		Resolver: resolver,
		Now: func() time.Time {
			return now
		},
	})

	endpoint := Endpoint{Host: "backend.example.internal", Port: 8096}
	first, err := cache.Resolve(context.Background(), endpoint)
	if err != nil {
		t.Fatalf("resolve #1: %v", err)
	}
	if got := first[0].Address; got != "10.0.0.1:8096" {
		t.Fatalf("unexpected first resolved address: %q", got)
	}

	now = now.Add(29 * time.Second)
	second, err := cache.Resolve(context.Background(), endpoint)
	if err != nil {
		t.Fatalf("resolve #2: %v", err)
	}
	if got := second[0].Address; got != "10.0.0.1:8096" {
		t.Fatalf("expected cached address before TTL expiry, got %q", got)
	}

	now = now.Add(2 * time.Second)
	third, err := cache.Resolve(context.Background(), endpoint)
	if err != nil {
		t.Fatalf("resolve #3: %v", err)
	}
	if got := third[0].Address; got != "10.0.0.2:8096" {
		t.Fatalf("expected refreshed address after TTL expiry, got %q", got)
	}
	if resolver.calls != 2 {
		t.Fatalf("expected resolver to be called exactly twice, got %d", resolver.calls)
	}
}

func TestCacheResolveUsesStaleDNSResultWhenRefreshFails(t *testing.T) {
	base := time.Date(2026, 4, 9, 10, 0, 0, 0, time.UTC)
	now := base
	resolver := &stubResolver{
		results: [][]net.IPAddr{
			{{IP: net.ParseIP("10.0.0.1")}},
		},
		errs: []error{nil, errors.New("dns refresh failed")},
	}

	cache := NewCache(BackendCacheConfig{
		Resolver: resolver,
		Now: func() time.Time {
			return now
		},
	})

	endpoint := Endpoint{Host: "backend.example.internal", Port: 8096}
	if _, err := cache.Resolve(context.Background(), endpoint); err != nil {
		t.Fatalf("resolve #1: %v", err)
	}

	now = now.Add(dnsCacheTTL + time.Second)
	stale, err := cache.Resolve(context.Background(), endpoint)
	if err != nil {
		t.Fatalf("resolve stale after refresh error: %v", err)
	}
	if got := stale[0].Address; got != "10.0.0.1:8096" {
		t.Fatalf("stale resolved address = %q, want previous IP", got)
	}
	if resolver.calls != 2 {
		t.Fatalf("resolver calls = %d, want refresh attempt", resolver.calls)
	}
}

func TestCacheResolveReturnsRefreshErrorAfterStaleDNSWindowExpires(t *testing.T) {
	base := time.Date(2026, 4, 9, 10, 0, 0, 0, time.UTC)
	now := base
	refreshErr := errors.New("dns refresh failed")
	resolver := &stubResolver{
		results: [][]net.IPAddr{
			{{IP: net.ParseIP("10.0.0.1")}},
		},
		errs: []error{nil, refreshErr},
	}

	cache := NewCache(BackendCacheConfig{
		Resolver: resolver,
		Now: func() time.Time {
			return now
		},
	})

	endpoint := Endpoint{Host: "backend.example.internal", Port: 8096}
	if _, err := cache.Resolve(context.Background(), endpoint); err != nil {
		t.Fatalf("resolve #1: %v", err)
	}

	now = now.Add(dnsCacheTTL + dnsCacheStaleIfErrorTTL + time.Second)
	if _, err := cache.Resolve(context.Background(), endpoint); !errors.Is(err, refreshErr) {
		t.Fatalf("resolve after stale window error = %v, want %v", err, refreshErr)
	}
}

func TestCacheOrderAdaptiveUsesCombinedPerformanceNotLatencyOnly(t *testing.T) {
	base := time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC)
	cache := NewCache(BackendCacheConfig{
		Now: func() time.Time {
			return base
		},
		RandomIntn: func(n int) int {
			return n - 1
		},
	})
	scope := "http:rule-adaptive-performance"
	candidates := []Candidate{
		{Address: "bulk"},
		{Address: "fast"},
	}

	bulkKey := BackendObservationKey(scope, "bulk")
	fastKey := BackendObservationKey(scope, "fast")
	cache.ObserveBackendSuccess(bulkKey, 12*time.Millisecond, 100*time.Millisecond, 512*1024)
	cache.ObserveBackendSuccess(bulkKey, 12*time.Millisecond, 100*time.Millisecond, 512*1024)
	cache.ObserveBackendSuccess(fastKey, 18*time.Millisecond, 100*time.Millisecond, 2*1024*1024)
	cache.ObserveBackendSuccess(fastKey, 18*time.Millisecond, 100*time.Millisecond, 2*1024*1024)

	got := cache.Order(scope, StrategyAdaptive, candidates)
	if ordered := addresses(got); !reflect.DeepEqual(ordered, []string{"fast", "bulk"}) {
		t.Fatalf("unexpected adaptive order with combined performance scoring: %v", ordered)
	}
}

func TestCacheOrderLatencyOnlyIgnoresBackendThroughput(t *testing.T) {
	base := time.Date(2026, 4, 18, 12, 0, 0, 0, time.UTC)
	cache := NewCache(BackendCacheConfig{
		Now: func() time.Time {
			return base
		},
	})
	scope := "tcp:rule-placeholder-latency-only"
	candidates := []Candidate{
		{Address: "slow"},
		{Address: "fast"},
	}

	for i := 0; i < 3; i++ {
		cache.ObserveBackendSuccess(BackendObservationKey(scope, "slow"), 45*time.Millisecond, 120*time.Millisecond, 2*1024*1024)
		cache.ObserveBackendSuccess(BackendObservationKey(scope, "fast"), 10*time.Millisecond, 350*time.Millisecond, 512*1024)
	}

	if got := cache.Order(scope, StrategyAdaptive, candidates); !reflect.DeepEqual(addresses(got), []string{"slow", "fast"}) {
		t.Fatalf("fixture must diverge under throughput-aware ordering: %v", addresses(got))
	}

	got := cache.OrderLatencyOnly(scope, StrategyAdaptive, candidates)
	if ordered := addresses(got); !reflect.DeepEqual(ordered, []string{"fast", "slow"}) {
		t.Fatalf("latency-only adaptive ordering must ignore backend throughput history: %v", ordered)
	}
}

func TestCacheFailureBackoffCapsAndSuccessResetsState(t *testing.T) {
	base := time.Date(2026, 4, 9, 10, 0, 0, 0, time.UTC)
	now := base
	cache := NewCache(BackendCacheConfig{
		Now: func() time.Time {
			return now
		},
	})

	addrA := "10.0.0.21:9001"
	addrB := "10.0.0.22:9001"
	if cache.IsInBackoff(addrA) {
		t.Fatalf("address should not start in backoff")
	}

	backoff := cache.MarkFailure(addrA)
	if backoff != time.Second {
		t.Fatalf("expected first backoff 1s, got %s", backoff)
	}
	if !cache.IsInBackoff(addrA) {
		t.Fatalf("expected failed address to be in backoff")
	}
	if cache.IsInBackoff(addrB) {
		t.Fatalf("failure cache must be keyed by actual IP:port")
	}

	now = now.Add(1100 * time.Millisecond)
	if cache.IsInBackoff(addrA) {
		t.Fatalf("expected first backoff window to expire")
	}

	backoff = cache.MarkFailure(addrA)
	if backoff != 2*time.Second {
		t.Fatalf("expected second backoff 2s, got %s", backoff)
	}

	var last time.Duration
	for i := 0; i < 12; i++ {
		now = now.Add(last + time.Second)
		last = cache.MarkFailure(addrA)
	}
	if last != 60*time.Second {
		t.Fatalf("expected capped backoff of 60s, got %s", last)
	}

	cache.MarkSuccess(addrA)
	if cache.IsInBackoff(addrA) {
		t.Fatalf("expected mark success to clear backoff state")
	}

	if reset := cache.MarkFailure(addrA); reset != time.Second {
		t.Fatalf("expected backoff to reset to 1s after success, got %s", reset)
	}
}

func addresses(candidates []Candidate) []string {
	out := make([]string, len(candidates))
	for i := range candidates {
		out[i] = candidates[i].Address
	}
	return out
}

type stubResolver struct {
	results [][]net.IPAddr
	errs    []error
	calls   int
}

func (s *stubResolver) LookupIPAddr(_ context.Context, host string) ([]net.IPAddr, error) {
	if host == "" {
		return nil, nil
	}
	call := s.calls
	s.calls++
	if call < len(s.errs) && s.errs[call] != nil {
		return nil, s.errs[call]
	}
	idx := call
	if idx >= len(s.results) {
		idx = len(s.results) - 1
	}
	return s.results[idx], nil
}
