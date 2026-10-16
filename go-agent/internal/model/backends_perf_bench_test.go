//go:build !integration

package model

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"
)

func BenchmarkBackendSelection(b *testing.B) {
	for _, size := range []int{1, 4, 16, 64} {
		for _, strategy := range []string{StrategyRoundRobin, StrategyAdaptive, "resolved"} {
			b.Run(fmt.Sprintf("%s/%d", strategy, size), func(b *testing.B) {
				now := time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC)
				cache := NewCache(BackendCacheConfig{Now: func() time.Time { return now }, RandomIntn: func(n int) int { return n - 1 }})
				candidates := make([]Candidate, size)
				for i := range candidates {
					address := fmt.Sprintf("10.0.0.%d:8096", i+1)
					candidates[i] = Candidate{Address: address}
					for sample := 0; sample < 4; sample++ {
						cache.ObserveBackendSuccess(BackendObservationKey("bench", address), time.Duration(size-i)*time.Millisecond, time.Second, 2<<20)
						cache.ObserveTransferSuccess(address, time.Duration(size-i)*time.Millisecond, time.Second, 2<<20)
					}
				}
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					if strategy == "resolved" {
						cache.PreferResolvedCandidates(candidates)
					} else {
						cache.Order("bench", strategy, candidates)
					}
				}
			})
		}
	}
}

func BenchmarkDNSCacheHit(b *testing.B) {
	cache := NewCache(BackendCacheConfig{Resolver: &stubResolver{results: [][]net.IPAddr{{{IP: net.ParseIP("10.0.0.1")}, {IP: net.ParseIP("::1")}}}}})
	endpoint := Endpoint{Host: "bench.test", Port: 8096}
	if _, err := cache.Resolve(context.Background(), endpoint); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := cache.Resolve(context.Background(), endpoint); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkObservationSummary(b *testing.B) {
	cache := NewCache(BackendCacheConfig{})
	for i := 0; i < 5; i++ {
		cache.ObserveTransferSuccess("backend", time.Millisecond, time.Second, 2<<20)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cache.Summary("backend")
	}
}
