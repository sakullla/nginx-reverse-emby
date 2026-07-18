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

func TestLastSeenIPsRefreshesPublishedGenerationAfterInterval(t *testing.T) {
	var hits atomic.Int32
	var mu sync.RWMutex
	address := "203.0.113.10"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		mu.RLock()
		defer mu.RUnlock()
		_, _ = w.Write([]byte(address))
	}))
	defer server.Close()

	now := time.Unix(1_000, 0)
	registry := module.NewRegistry()
	m := NewModule(Config{
		Client: server.Client(), IPv4PublicAPIURL: server.URL, IPv6PublicAPIURL: server.URL,
		MinExtractInterval: time.Minute, GenerationSelector: registry, now: func() time.Time { return now },
	})
	if err := registry.Register(m); err != nil {
		t.Fatal(err)
	}
	next := model.Snapshot{Revision: 1, DDNSConfig: &model.DDNSExtractConfig{
		Enabled: true, Domain: "media.example.com", IPv4: model.DDNSFamily{Enabled: true, Source: sourcePublicAPI},
	}}
	generationContext, err := module.NewGenerationContext(model.Snapshot{}, next)
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := registry.PrepareGeneration(context.Background(), generationContext)
	if err != nil {
		t.Fatal(err)
	}
	if err := candidate.Ready(context.Background()); err != nil {
		t.Fatal(err)
	}
	candidate.Publish()

	mu.Lock()
	address = "203.0.113.11"
	mu.Unlock()
	now = now.Add(2 * time.Minute)
	got, _ := m.LastSeenIPs(context.Background())
	if got != "203.0.113.11" || hits.Load() != 2 {
		t.Fatalf("LastSeenIPs() = %q with %d probes, want refreshed address with 2 probes", got, hits.Load())
	}
}

func TestExtractBoundsAllPublicAPIProbesWithOneDeadline(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer server.Close()

	urls := server.URL + "/one," + server.URL + "/two," + server.URL + "/three"
	m := NewModule(Config{
		Client:               server.Client(),
		IPv4PublicAPIURL:     urls,
		IPv6PublicAPIURL:     urls,
		MinExtractInterval:   time.Minute,
		publicExtractTimeout: 40 * time.Millisecond,
	})
	cfg := &model.DDNSExtractConfig{
		Enabled: true,
		IPv4:    model.DDNSFamily{Enabled: true, Source: sourcePublicAPI},
		IPv6:    model.DDNSFamily{Enabled: true, Source: sourcePublicAPI},
	}

	started := time.Now()
	ipv4, ipv6 := m.extract(t.Context(), cfg)
	if elapsed := time.Since(started); elapsed > 200*time.Millisecond {
		t.Fatalf("extract elapsed = %s, want one bounded probe deadline", elapsed)
	}
	if ipv4 != "" || ipv6 != "" {
		t.Fatalf("extract results = %q/%q, want empty", ipv4, ipv6)
	}
}
