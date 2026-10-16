//go:build !integration

package model

import (
	"context"
	"errors"
	"net"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type lookupFunc func(context.Context, string) ([]net.IPAddr, error)

func (f lookupFunc) LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error) {
	return f(ctx, host)
}

// Done is evaluated when a follower enters the wait select, so tests need no
// scheduler sleeps to ensure concurrent requests have joined the same lookup.
type waitingContext struct {
	context.Context
	joined chan struct{}
	once   sync.Once
}

func (c *waitingContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.joined) })
	return c.Context.Done()
}
func waitSignal(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for lookup")
	}
}

func TestDNSCoalescesConcurrentMissesAndIsolatesResults(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	cache := NewCache(BackendCacheConfig{Resolver: lookupFunc(func(ctx context.Context, host string) ([]net.IPAddr, error) {
		if calls.Add(1) == 1 {
			close(started)
		}
		select {
		case <-release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return []net.IPAddr{{IP: net.ParseIP("10.0.0.1")}, {IP: net.ParseIP("10.0.0.1")}, {IP: net.ParseIP("::1")}, {}}, nil
	})})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	type result struct {
		candidates []Candidate
		err        error
	}
	results := make(chan result, 17)
	resolve := func(ctx context.Context) {
		cs, err := cache.Resolve(ctx, Endpoint{Host: "BACKEND.test", Port: 8096})
		results <- result{cs, err}
	}
	go resolve(ctx)
	waitSignal(t, started)
	for i := 0; i < 16; i++ {
		follower := &waitingContext{Context: ctx, joined: make(chan struct{})}
		go resolve(follower)
		waitSignal(t, follower.joined)
	}
	close(release)
	for i := 0; i < 17; i++ {
		r := <-results
		if r.err != nil {
			t.Fatal(r.err)
		}
		if got := addresses(r.candidates); !reflect.DeepEqual(got, []string{"10.0.0.1:8096", "[::1]:8096"}) {
			t.Fatal(got)
		}
		r.candidates[0].Address = "mutated"
	}
	if calls.Load() != 1 {
		t.Fatalf("resolver calls = %d", calls.Load())
	}
	cs, err := cache.Resolve(ctx, Endpoint{Host: "backend.test", Port: 8096})
	if err != nil || cs[0].Address != "10.0.0.1:8096" {
		t.Fatalf("cache result: %v, %v", cs, err)
	}
}

func TestDNSWaiterCancellationDoesNotCancelLeader(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cache := NewCache(BackendCacheConfig{Resolver: lookupFunc(func(ctx context.Context, _ string) ([]net.IPAddr, error) {
		close(started)
		select {
		case <-release:
			return []net.IPAddr{{IP: net.ParseIP("10.0.0.1")}}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	})})
	leader := make(chan error, 1)
	go func() { _, err := cache.lookupHost(ctx, "test"); leader <- err }()
	waitSignal(t, started)
	fctx, fcancel := context.WithCancel(ctx)
	follower := &waitingContext{Context: fctx, joined: make(chan struct{})}
	result := make(chan error, 1)
	go func() { _, err := cache.lookupHost(follower, "test"); result <- err }()
	waitSignal(t, follower.joined)
	fcancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("follower: %v", err)
	}
	close(release)
	if err := <-leader; err != nil {
		t.Fatalf("leader: %v", err)
	}
}

func TestDNSLeaderCancellationAllowsFollowerRetry(t *testing.T) {
	started := make(chan struct{})
	var calls atomic.Int32
	cache := NewCache(BackendCacheConfig{Resolver: lookupFunc(func(ctx context.Context, _ string) ([]net.IPAddr, error) {
		if calls.Add(1) == 1 {
			close(started)
			<-ctx.Done()
			return nil, ctx.Err()
		}
		return []net.IPAddr{{IP: net.ParseIP("10.0.0.2")}}, nil
	})})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	leaderCtx, stopLeader := context.WithCancel(ctx)
	defer stopLeader()
	leader := make(chan error, 1)
	go func() { _, err := cache.lookupHost(leaderCtx, "test"); leader <- err }()
	waitSignal(t, started)
	follower := &waitingContext{Context: ctx, joined: make(chan struct{})}
	result := make(chan error, 1)
	go func() { _, err := cache.lookupHost(follower, "test"); result <- err }()
	waitSignal(t, follower.joined)
	stopLeader()
	if err := <-leader; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatalf("calls = %d", calls.Load())
	}
}

func TestDNSTTLAndStaleWindowUseCompletionTime(t *testing.T) {
	base := time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC)
	now := base
	failure := errors.New("DNS unavailable")
	calls := 0
	cache := NewCache(BackendCacheConfig{Now: func() time.Time { return now }, Resolver: lookupFunc(func(context.Context, string) ([]net.IPAddr, error) {
		calls++
		if calls == 1 {
			now = now.Add(20 * time.Second)
			return []net.IPAddr{{IP: net.ParseIP("10.0.0.1")}}, nil
		}
		now = now.Add(dnsCacheStaleIfErrorTTL + time.Second)
		return nil, failure
	})})
	if _, err := cache.lookupHost(context.Background(), "test"); err != nil {
		t.Fatal(err)
	}
	now = base.Add(40 * time.Second)
	if _, err := cache.lookupHost(context.Background(), "test"); err != nil || calls != 1 {
		t.Fatalf("cache expired prematurely: %v, calls=%d", err, calls)
	}
	now = base.Add(51 * time.Second)
	if _, err := cache.lookupHost(context.Background(), "test"); !errors.Is(err, failure) {
		t.Fatalf("expired stale data served: %v", err)
	}
}

func TestDNSCloneDoesNotShareInflightWork(t *testing.T) {
	cache := NewCache(BackendCacheConfig{Resolver: &stubResolver{results: [][]net.IPAddr{{{IP: net.ParseIP("10.0.0.1")}}}}})
	cache.dnsLookups = map[string]*dnsLookup{"test": {done: make(chan struct{})}}
	clone := cache.Clone()
	if len(clone.dnsLookups) != 0 {
		t.Fatal("clone inherited in-flight work")
	}
	if _, err := clone.lookupHost(context.Background(), "test"); err != nil {
		t.Fatal(err)
	}
}

func TestDNSDifferentHostsResolveIndependently(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cache := NewCache(BackendCacheConfig{Resolver: lookupFunc(func(ctx context.Context, host string) ([]net.IPAddr, error) {
		if host == "slow" {
			close(started)
			select {
			case <-release:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		return []net.IPAddr{{IP: net.ParseIP("10.0.0.1")}}, nil
	})})
	result := make(chan error, 1)
	go func() { _, err := cache.lookupHost(ctx, "slow"); result <- err }()
	waitSignal(t, started)
	if _, err := cache.lookupHost(ctx, "fast"); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-result; err != nil {
		t.Fatal(err)
	}
}

func TestDNSFailureIsSharedButNotCached(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	failure := errors.New("temporary DNS failure")
	var calls atomic.Int32
	cache := NewCache(BackendCacheConfig{Resolver: lookupFunc(func(ctx context.Context, _ string) ([]net.IPAddr, error) {
		if calls.Add(1) == 1 {
			close(started)
			select {
			case <-release:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			return nil, failure
		}
		return []net.IPAddr{{IP: net.ParseIP("10.0.0.2")}}, nil
	})})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result := make(chan error, 2)
	go func() { _, err := cache.lookupHost(ctx, "test"); result <- err }()
	waitSignal(t, started)
	follower := &waitingContext{Context: ctx, joined: make(chan struct{})}
	go func() { _, err := cache.lookupHost(follower, "test"); result <- err }()
	waitSignal(t, follower.joined)
	close(release)
	for i := 0; i < 2; i++ {
		if err := <-result; !errors.Is(err, failure) {
			t.Fatal(err)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("calls=%d", calls.Load())
	}
	if _, err := cache.lookupHost(ctx, "test"); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatalf("failure was cached: calls=%d", calls.Load())
	}
}
