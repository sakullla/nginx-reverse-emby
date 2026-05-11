package backends

import (
	"strconv"
	"testing"
	"time"
)

func BenchmarkCacheOrderAdaptive64Candidates(b *testing.B) {
	now := time.Date(2026, time.May, 11, 0, 0, 0, 0, time.UTC)
	cache := NewCache(Config{
		Now:        func() time.Time { return now },
		RandomIntn: func(n int) int { return 0 },
	})
	candidates := benchmarkCandidates(64)
	for i, candidate := range candidates {
		key := BackendObservationKey("http:bench", candidate.Address)
		latency := time.Duration(10+i%20) * time.Millisecond
		cache.ObserveBackendSuccess(key, latency, 120*time.Millisecond, int64(256*1024+i*1024))
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ordered := cache.Order("http:bench", StrategyAdaptive, candidates)
		if len(ordered) != len(candidates) {
			b.Fatalf("Order() candidates = %d, want %d", len(ordered), len(candidates))
		}
	}
}

func BenchmarkCacheOrderRoundRobin64Candidates(b *testing.B) {
	cache := NewCache(Config{
		RandomIntn: func(n int) int { return 0 },
	})
	candidates := benchmarkCandidates(64)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ordered := cache.Order("http:bench", StrategyRoundRobin, candidates)
		if len(ordered) != len(candidates) {
			b.Fatalf("Order() candidates = %d, want %d", len(ordered), len(candidates))
		}
	}
}

func BenchmarkBackendObservationKey(b *testing.B) {
	scopes := benchmarkStrings("http:rule-", 32, "")
	backends := benchmarkStrings("backend-", 64, ".example:8096")

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if key := BackendObservationKey(scopes[i%len(scopes)], backends[i%len(backends)]); key == "" {
			b.Fatal("BackendObservationKey() returned empty key")
		}
	}
}

func BenchmarkRelayBackoffKeyForLayers(b *testing.B) {
	layers := [][]int{{1, 2, 3}, {4, 5, 6}, {7, 8, 9}}
	backends := benchmarkStrings("backend-", 64, ".example:443")

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if key := RelayBackoffKeyForLayers(nil, layers, backends[i%len(backends)]); key == "" {
			b.Fatal("RelayBackoffKeyForLayers() returned empty key")
		}
	}
}

func benchmarkCandidates(total int) []Candidate {
	candidates := make([]Candidate, 0, total)
	for i := 0; i < total; i++ {
		host := "backend-" + strconv.Itoa(i) + ".example"
		candidates = append(candidates, Candidate{
			Endpoint: Endpoint{Host: host, Port: 8096},
			Address:  host + ":8096",
		})
	}
	return candidates
}

func benchmarkStrings(prefix string, total int, suffix string) []string {
	values := make([]string, 0, total)
	for i := 0; i < total; i++ {
		values = append(values, prefix+strconv.Itoa(i)+suffix)
	}
	return values
}
