package cloudflare

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/pkg/acmeflow"
)

func TestDNSCNAMEChainLoopAndFiftyHopLimit(t *testing.T) {
	chain := make(map[string]string)
	for index := 0; index < MaxCNAMEHops; index++ {
		chain[fmt.Sprintf("n%d.example.com", index)] = fmt.Sprintf("n%d.example.com", index+1)
	}
	querier := &fakeDNSQuerier{query: func(_ string, name string, recordType RRType, _ int) (DNSMessage, error) {
		if recordType != TypeCNAME {
			return DNSMessage{}, nil
		}
		target := chain[name]
		if target == "" {
			return DNSMessage{}, nil
		}
		return DNSMessage{Answers: []DNSRecord{{Name: name, Type: TypeCNAME, Value: target}}}, nil
	}}
	propagation, err := NewPropagation(PropagationConfig{Resolver: querier, RecursiveServers: []string{"recursive"}})
	if err != nil {
		t.Fatalf("NewPropagation() error = %v", err)
	}
	target, err := propagation.ResolveCNAME(context.Background(), "N0.Example.COM.")
	if err != nil {
		t.Fatalf("ResolveCNAME(50 hops) error = %v", err)
	}
	if target != "n50.example.com" {
		t.Fatalf("ResolveCNAME(50 hops) = %q", target)
	}

	chain["n50.example.com"] = "n51.example.com"
	if _, err := propagation.ResolveCNAME(context.Background(), "n0.example.com"); err == nil {
		t.Fatal("ResolveCNAME(51 hops) error = nil")
	}
	loopQuerier := &fakeDNSQuerier{query: func(_ string, name string, recordType RRType, _ int) (DNSMessage, error) {
		if recordType != TypeCNAME {
			return DNSMessage{}, nil
		}
		target := map[string]string{"a.example.com": "b.example.com", "b.example.com": "a.example.com"}[name]
		return DNSMessage{Answers: []DNSRecord{{Name: name, Type: TypeCNAME, Value: target}}}, nil
	}}
	loopPropagation, err := NewPropagation(PropagationConfig{Resolver: loopQuerier, RecursiveServers: []string{"recursive"}})
	if err != nil {
		t.Fatalf("NewPropagation(loop) error = %v", err)
	}
	if _, err := loopPropagation.ResolveCNAME(context.Background(), "a.example.com"); err == nil {
		t.Fatal("ResolveCNAME(loop) error = nil")
	}
}

func TestDNSAuthorityDiscoveryAndTXTPropagation(t *testing.T) {
	clock := newFakePropagationClock()
	var authoritativePolls int
	querier := &fakeDNSQuerier{query: func(server, name string, recordType RRType, _ int) (DNSMessage, error) {
		switch {
		case server == "recursive" && recordType == TypeSOA:
			return DNSMessage{Authorities: []DNSRecord{{Name: "example.com", Type: TypeSOA, SOA: &SOAData{MName: "ns1.example.com", RName: "hostmaster.example.com"}}}}, nil
		case server == "recursive" && name == "example.com" && recordType == TypeNS:
			return DNSMessage{Answers: []DNSRecord{
				{Name: "example.com", Type: TypeNS, Value: "ns1.example.com"},
				{Name: "example.com", Type: TypeNS, Value: "ns2.example.com"},
			}}, nil
		case (server == "ns1.example.com" || server == "ns2.example.com") && recordType == TypeTXT:
			if server == "ns1.example.com" || authoritativePolls > 0 {
				if server == "ns2.example.com" {
					authoritativePolls++
				}
				return DNSMessage{Answers: []DNSRecord{{Name: name, Type: TypeTXT, Value: "expected-value", Text: []string{"expected-value"}}}}, nil
			}
			authoritativePolls++
			return DNSMessage{}, nil
		default:
			return DNSMessage{}, nil
		}
	}}
	propagation, err := NewPropagation(PropagationConfig{
		Resolver:         querier,
		RecursiveServers: []string{"recursive"},
		Now:              clock.Now,
		Wait:             clock.Wait,
		AuthorityAddress: func(name string) string { return name },
	})
	if err != nil {
		t.Fatalf("NewPropagation() error = %v", err)
	}
	if propagation.timeout != DefaultPropagationTimeout || propagation.pollInterval != DefaultPollInterval {
		t.Fatalf("defaults = %v/%v", propagation.timeout, propagation.pollInterval)
	}
	zone, servers, err := propagation.DiscoverAuthority(context.Background(), "_acme-challenge.service.example.com")
	if err != nil {
		t.Fatalf("DiscoverAuthority() error = %v", err)
	}
	if zone != "example.com" || fmt.Sprint(servers) != "[ns1.example.com ns2.example.com]" {
		t.Fatalf("DiscoverAuthority() = %q %#v", zone, servers)
	}
	if err := propagation.WaitTXT(context.Background(), "_acme-challenge.service.example.com", "expected-value", "example.com"); err != nil {
		t.Fatalf("WaitTXT() error = %v", err)
	}
	if clock.Elapsed() != DefaultPollInterval {
		t.Fatalf("propagation elapsed = %v, want %v", clock.Elapsed(), DefaultPollInterval)
	}
}

func TestDNSPropagationTimeoutCancellationAndExactTXT(t *testing.T) {
	clock := newFakePropagationClock()
	querier := &fakeDNSQuerier{query: func(server, name string, recordType RRType, _ int) (DNSMessage, error) {
		switch recordType {
		case TypeSOA:
			return DNSMessage{Answers: []DNSRecord{{Name: "example.com", Type: TypeSOA, SOA: &SOAData{MName: "ns.example.com"}}}}, nil
		case TypeNS:
			return DNSMessage{Answers: []DNSRecord{{Name: "example.com", Type: TypeNS, Value: "ns.example.com"}}}, nil
		case TypeTXT:
			return DNSMessage{Answers: []DNSRecord{{Name: name, Type: TypeTXT, Value: "wrong-value"}}}, nil
		default:
			return DNSMessage{}, nil
		}
	}}
	propagation, err := NewPropagation(PropagationConfig{
		Resolver:         querier,
		RecursiveServers: []string{"recursive"},
		Now:              clock.Now,
		Wait:             clock.Wait,
		AuthorityAddress: func(name string) string { return name },
	})
	if err != nil {
		t.Fatalf("NewPropagation() error = %v", err)
	}
	err = propagation.WaitTXT(context.Background(), "_acme-challenge.example.com", "expected-value", "example.com")
	if got := acmeflow.ErrorCategoryOf(err); got != acmeflow.CategoryTimeout {
		t.Fatalf("timeout category = %q, want %q; err=%v", got, acmeflow.CategoryTimeout, err)
	}
	if clock.Elapsed() != DefaultPropagationTimeout {
		t.Fatalf("timeout elapsed = %v, want %v", clock.Elapsed(), DefaultPropagationTimeout)
	}

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	err = propagation.WaitTXT(cancelled, "_acme-challenge.example.com", "expected-value", "example.com")
	if got := acmeflow.ErrorCategoryOf(err); got != acmeflow.CategoryCancelled {
		t.Fatalf("cancel category = %q, want %q; err=%v", got, acmeflow.CategoryCancelled, err)
	}
}

type fakeDNSQuerier struct {
	mu    sync.Mutex
	calls int
	query func(server, name string, recordType RRType, call int) (DNSMessage, error)
}

func (resolver *fakeDNSQuerier) Query(_ context.Context, server, name string, recordType RRType) (DNSMessage, error) {
	resolver.mu.Lock()
	defer resolver.mu.Unlock()
	resolver.calls++
	return resolver.query(server, name, recordType, resolver.calls)
}

type fakePropagationClock struct {
	mu      sync.Mutex
	start   time.Time
	current time.Time
}

func newFakePropagationClock() *fakePropagationClock {
	start := time.Date(2026, 7, 25, 0, 0, 0, 0, time.UTC)
	return &fakePropagationClock{start: start, current: start}
}

func (clock *fakePropagationClock) Now() time.Time {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	return clock.current
}

func (clock *fakePropagationClock) Wait(ctx context.Context, duration time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	clock.mu.Lock()
	clock.current = clock.current.Add(duration)
	clock.mu.Unlock()
	return nil
}

func (clock *fakePropagationClock) Elapsed() time.Duration {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	return clock.current.Sub(clock.start)
}
