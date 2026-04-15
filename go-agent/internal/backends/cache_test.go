package backends

import (
	"context"
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

	cache := NewCache(Config{
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

func TestCacheOrderRoundRobinTracksPerScope(t *testing.T) {
	cache := NewCache(Config{})
	candidates := []Candidate{
		{Address: "10.0.0.1:80"},
		{Address: "10.0.0.2:80"},
		{Address: "10.0.0.3:80"},
	}

	first := cache.Order("http:rule-a", "round_robin", candidates)
	second := cache.Order("http:rule-a", "round_robin", candidates)
	third := cache.Order("http:rule-a", "round_robin", candidates)
	otherScope := cache.Order("http:rule-b", "round_robin", candidates)

	if got := addresses(first); !reflect.DeepEqual(got, []string{"10.0.0.1:80", "10.0.0.2:80", "10.0.0.3:80"}) {
		t.Fatalf("unexpected round_robin order #1: %v", got)
	}
	if got := addresses(second); !reflect.DeepEqual(got, []string{"10.0.0.2:80", "10.0.0.3:80", "10.0.0.1:80"}) {
		t.Fatalf("unexpected round_robin order #2: %v", got)
	}
	if got := addresses(third); !reflect.DeepEqual(got, []string{"10.0.0.3:80", "10.0.0.1:80", "10.0.0.2:80"}) {
		t.Fatalf("unexpected round_robin order #3: %v", got)
	}
	if got := addresses(otherScope); !reflect.DeepEqual(got, []string{"10.0.0.1:80", "10.0.0.2:80", "10.0.0.3:80"}) {
		t.Fatalf("unexpected first order for independent scope: %v", got)
	}
}

func TestCacheOrderRandomUsesHook(t *testing.T) {
	calls := 0
	cache := NewCache(Config{
		RandomIntn: func(n int) int {
			calls++
			switch calls {
			case 1:
				if n != 3 {
					t.Fatalf("unexpected first random bound: %d", n)
				}
				return 1
			case 2:
				if n != 2 {
					t.Fatalf("unexpected second random bound: %d", n)
				}
				return 0
			default:
				return 0
			}
		},
	})
	candidates := []Candidate{
		{Address: "10.0.0.1:80"},
		{Address: "10.0.0.2:80"},
		{Address: "10.0.0.3:80"},
	}

	got := cache.Order("http:rule-a", "random", candidates)
	if calls != 2 {
		t.Fatalf("expected random hook to be called twice, got %d", calls)
	}
	if ordered := addresses(got); !reflect.DeepEqual(ordered, []string{"10.0.0.3:80", "10.0.0.1:80", "10.0.0.2:80"}) {
		t.Fatalf("unexpected random order: %v", ordered)
	}
}

func TestCacheFailureBackoffCapsAndSuccessResetsState(t *testing.T) {
	base := time.Date(2026, 4, 9, 10, 0, 0, 0, time.UTC)
	now := base
	cache := NewCache(Config{
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

func TestCacheFailureBackoffUsesConfiguredBaseAndLimit(t *testing.T) {
	cache := NewCache(Config{
		FailureBackoffBase:  500 * time.Millisecond,
		FailureBackoffLimit: 4 * time.Second,
	})

	addr := "10.0.0.99:9001"
	if got := cache.MarkFailure(addr); got != 500*time.Millisecond {
		t.Fatalf("first MarkFailure() = %v", got)
	}
	if got := cache.MarkFailure(addr); got != time.Second {
		t.Fatalf("second MarkFailure() = %v", got)
	}
	if got := cache.MarkFailure(addr); got != 2*time.Second {
		t.Fatalf("third MarkFailure() = %v", got)
	}
	if got := cache.MarkFailure(addr); got != 4*time.Second {
		t.Fatalf("fourth MarkFailure() = %v", got)
	}
	if got := cache.MarkFailure(addr); got != 4*time.Second {
		t.Fatalf("capped MarkFailure() = %v", got)
	}
}

func TestCacheObserveSuccessStoresLatencyAndRanksCandidates(t *testing.T) {
	cache := NewCache(Config{
		Now: func() time.Time {
			return time.Date(2026, 4, 16, 12, 0, 0, 0, time.UTC)
		},
	})
	candidates := []Candidate{
		{Address: "10.0.0.1:443"},
		{Address: "10.0.0.2:443"},
	}

	cache.ObserveSuccess("10.0.0.1:443", 180*time.Millisecond)
	cache.ObserveSuccess("10.0.0.2:443", 40*time.Millisecond)

	got := cache.PreferResolvedCandidates(candidates)
	if !reflect.DeepEqual(addresses(got), []string{"10.0.0.2:443", "10.0.0.1:443"}) {
		t.Fatalf("unexpected preferred order: %v", addresses(got))
	}
}

func TestCachePreferResolvedCandidatesPreservesInputOrderWithoutObservations(t *testing.T) {
	cache := NewCache(Config{})
	candidates := []Candidate{
		{Address: "10.0.0.3:443"},
		{Address: "10.0.0.4:443"},
		{Address: "10.0.0.5:443"},
	}

	got := cache.PreferResolvedCandidates(candidates)
	if !reflect.DeepEqual(addresses(got), []string{"10.0.0.3:443", "10.0.0.4:443", "10.0.0.5:443"}) {
		t.Fatalf("unexpected preserved order: %v", addresses(got))
	}
}

func TestCacheMarkFailureDemotesPreferredCandidate(t *testing.T) {
	cache := NewCache(Config{
		Now: func() time.Time {
			return time.Date(2026, 4, 16, 12, 0, 0, 0, time.UTC)
		},
	})
	candidates := []Candidate{
		{Address: "10.0.0.6:443"},
		{Address: "10.0.0.7:443"},
	}

	cache.ObserveSuccess("10.0.0.6:443", 25*time.Millisecond)
	cache.ObserveSuccess("10.0.0.7:443", 90*time.Millisecond)
	cache.MarkFailure("10.0.0.6:443")

	got := cache.PreferResolvedCandidates(candidates)
	if !reflect.DeepEqual(addresses(got), []string{"10.0.0.7:443", "10.0.0.6:443"}) {
		t.Fatalf("unexpected order after failure: %v", addresses(got))
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
	calls   int
}

func (s *stubResolver) LookupIPAddr(_ context.Context, host string) ([]net.IPAddr, error) {
	if host == "" {
		return nil, nil
	}
	idx := s.calls
	if idx >= len(s.results) {
		idx = len(s.results) - 1
	}
	s.calls++
	return s.results[idx], nil
}
