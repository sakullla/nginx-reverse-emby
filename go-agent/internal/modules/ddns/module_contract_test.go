//go:build !integration

package ddns

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
)

func TestModuleGenerationPublicationThrottleAndRefresh(t *testing.T) {
	var hits atomic.Int32
	var addressMu sync.RWMutex
	address := "203.0.113.10"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		addressMu.RLock()
		defer addressMu.RUnlock()
		_, _ = w.Write([]byte(address))
	}))
	t.Cleanup(server.Close)

	now := time.Unix(1_000, 0)
	registry := module.NewRegistry()
	owner := NewModule(Config{
		Client: server.Client(), IPv4PublicAPIURL: server.URL, IPv6PublicAPIURL: server.URL,
		MinExtractInterval: time.Minute, GenerationSelector: registry, now: func() time.Time { return now },
	})
	if err := registry.Register(owner); err != nil {
		t.Fatal(err)
	}

	first := prepareDDNSGeneration(t, registry, model.Snapshot{}, ddnsSnapshot(1, "edge.example.com"))
	if got, _ := owner.LastSeenIPs(t.Context()); got != "" {
		t.Fatalf("candidate address was visible before publish: %q", got)
	}
	first.Publish()
	assertDDNSAddress(t, owner, "203.0.113.10", 1, &hits)

	setDDNSTestAddress(&addressMu, &address, "203.0.113.11")
	now = now.Add(30 * time.Second)
	assertDDNSAddress(t, owner, "203.0.113.10", 1, &hits)

	second := prepareDDNSGeneration(t, registry, ddnsSnapshot(1, "edge.example.com"), ddnsSnapshot(2, "rotated.example.com"))
	assertDDNSAddress(t, owner, "203.0.113.10", 2, &hits)
	second.Publish()
	assertDDNSAddress(t, owner, "203.0.113.11", 2, &hits)

	setDDNSTestAddress(&addressMu, &address, "203.0.113.12")
	now = now.Add(2 * time.Minute)
	assertDDNSAddress(t, owner, "203.0.113.12", 3, &hits)
}

func prepareDDNSGeneration(t *testing.T, registry *module.Registry, previous, next model.Snapshot) module.PreparedGeneration {
	t.Helper()
	generation, err := module.NewGenerationContext(previous, next)
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := registry.PrepareGeneration(context.Background(), generation)
	if err != nil {
		t.Fatal(err)
	}
	if err := candidate.Ready(context.Background()); err != nil {
		t.Fatal(err)
	}
	return candidate
}

func ddnsSnapshot(revision int64, domain string) model.Snapshot {
	return model.Snapshot{Revision: revision, DDNSConfig: &model.DDNSExtractConfig{
		Enabled: true, Domain: domain, IPv4: model.DDNSFamily{Enabled: true, Source: sourcePublicAPI},
	}}
}

func setDDNSTestAddress(mu *sync.RWMutex, address *string, next string) {
	mu.Lock()
	*address = next
	mu.Unlock()
}

func assertDDNSAddress(t *testing.T, owner *Module, want string, wantHits int32, hits *atomic.Int32) {
	t.Helper()
	got, ipv6 := owner.LastSeenIPs(t.Context())
	if got != want || ipv6 != "" || hits.Load() != wantHits {
		t.Fatalf("LastSeenIPs() = %q/%q with %d hits, want %q/empty with %d", got, ipv6, hits.Load(), want, wantHits)
	}
}
