package ddns

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
)

func newCountingServer(t *testing.T, status int, body string) (*httptest.Server, *http.Client, *int32) {
	t.Helper()
	var hits int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(server.Close)
	return server, server.Client(), &hits
}

func v4Config() *model.DDNSExtractConfig {
	return &model.DDNSExtractConfig{
		Domain: "edge.example.com",
		IPv4:   model.DDNSFamily{Enabled: true, Source: "public_api"},
	}
}

func TestModuleApplyExtractsAndCaches(t *testing.T) {
	server, client, hits := newCountingServer(t, http.StatusOK, "203.0.113.77")
	m := NewModule(Config{
		Client:             client,
		IPv4PublicAPIURL:   server.URL,
		IPv6PublicAPIURL:   server.URL,
		MinExtractInterval: time.Minute,
	})

	if err := m.Apply(context.Background(), module.ApplyRequest{Next: model.Snapshot{DDNSConfig: v4Config()}}); err != nil {
		t.Fatalf("Apply returned error: %v", err)
	}
	if atomic.LoadInt32(hits) != 1 {
		t.Fatalf("expected 1 extraction, got %d", atomic.LoadInt32(hits))
	}
	v4, v6 := m.LastSeenIPs(context.Background())
	if v4 != "203.0.113.77" {
		t.Fatalf("LastSeenIPs v4 = %q, want 203.0.113.77", v4)
	}
	if v6 != "" {
		t.Fatalf("LastSeenIPs v6 = %q, want empty (ipv6 disabled)", v6)
	}
}

func TestModuleDisabledConfigReportsEmpty(t *testing.T) {
	server, client, hits := newCountingServer(t, http.StatusOK, "203.0.113.77")
	m := NewModule(Config{Client: client, IPv4PublicAPIURL: server.URL, IPv6PublicAPIURL: server.URL})

	// Both families disabled: nothing to extract.
	if err := m.Apply(context.Background(), module.ApplyRequest{Next: model.Snapshot{DDNSConfig: &model.DDNSExtractConfig{Domain: "edge.example.com"}}}); err != nil {
		t.Fatalf("Apply returned error: %v", err)
	}
	if atomic.LoadInt32(hits) != 0 {
		t.Fatalf("expected no extraction for disabled config, got %d", atomic.LoadInt32(hits))
	}
	v4, v6 := m.LastSeenIPs(context.Background())
	if v4 != "" || v6 != "" {
		t.Fatalf("LastSeenIPs = (%q,%q), want empty", v4, v6)
	}
}

func TestModuleNilConfigIsNoop(t *testing.T) {
	server, client, hits := newCountingServer(t, http.StatusOK, "203.0.113.77")
	m := NewModule(Config{Client: client, IPv4PublicAPIURL: server.URL, IPv6PublicAPIURL: server.URL})

	if err := m.Apply(context.Background(), module.ApplyRequest{Next: model.Snapshot{DDNSConfig: nil}}); err != nil {
		t.Fatalf("Apply returned error: %v", err)
	}
	if atomic.LoadInt32(hits) != 0 {
		t.Fatalf("expected no extraction for nil config, got %d", atomic.LoadInt32(hits))
	}
}

func TestModuleThrottleSuppressesRepeatedExtraction(t *testing.T) {
	server, client, hits := newCountingServer(t, http.StatusOK, "203.0.113.77")
	now := time.Unix(1000, 0)
	clock := func() time.Time { return now }
	m := NewModule(Config{
		Client:             client,
		IPv4PublicAPIURL:   server.URL,
		IPv6PublicAPIURL:   server.URL,
		MinExtractInterval: 5 * time.Minute,
		now:                clock,
	})
	cfg := v4Config()

	if err := m.Apply(context.Background(), module.ApplyRequest{Next: model.Snapshot{DDNSConfig: cfg}}); err != nil {
		t.Fatalf("Apply #1 error: %v", err)
	}
	if got := atomic.LoadInt32(hits); got != 1 {
		t.Fatalf("after first apply, hits = %d, want 1", got)
	}

	// Advance only 1 minute (< 5 minute throttle) with the same config: no re-extract.
	now = now.Add(time.Minute)
	if err := m.Apply(context.Background(), module.ApplyRequest{Next: model.Snapshot{DDNSConfig: cfg}}); err != nil {
		t.Fatalf("Apply #2 error: %v", err)
	}
	if got := atomic.LoadInt32(hits); got != 1 {
		t.Fatalf("after throttled apply, hits = %d, want 1", got)
	}

	// Cross the throttle boundary: re-extract.
	now = now.Add(5 * time.Minute)
	if err := m.Apply(context.Background(), module.ApplyRequest{Next: model.Snapshot{DDNSConfig: cfg}}); err != nil {
		t.Fatalf("Apply #3 error: %v", err)
	}
	if got := atomic.LoadInt32(hits); got != 2 {
		t.Fatalf("after interval apply, hits = %d, want 2", got)
	}
}

func TestModuleConfigChangeForcesExtraction(t *testing.T) {
	server, client, hits := newCountingServer(t, http.StatusOK, "203.0.113.77")
	now := time.Unix(1000, 0)
	clock := func() time.Time { return now }
	m := NewModule(Config{
		Client:             client,
		IPv4PublicAPIURL:   server.URL,
		IPv6PublicAPIURL:   server.URL,
		MinExtractInterval: 5 * time.Minute,
		now:                clock,
	})

	first := v4Config()
	if err := m.Apply(context.Background(), module.ApplyRequest{Next: model.Snapshot{DDNSConfig: first}}); err != nil {
		t.Fatalf("Apply #1 error: %v", err)
	}
	if got := atomic.LoadInt32(hits); got != 1 {
		t.Fatalf("after first apply, hits = %d, want 1", got)
	}

	// A different config forces an immediate re-extract even inside the throttle
	// window. Domain differs but IPv4 stays the only enabled family so each
	// extracting Apply still costs exactly one probe.
	changed := &model.DDNSExtractConfig{
		Domain: "rotated.example.com",
		IPv4:   model.DDNSFamily{Enabled: true, Source: "public_api"},
	}
	if err := m.Apply(context.Background(), module.ApplyRequest{Next: model.Snapshot{DDNSConfig: changed}}); err != nil {
		t.Fatalf("Apply #2 error: %v", err)
	}
	if got := atomic.LoadInt32(hits); got != 2 {
		t.Fatalf("after config-change apply, hits = %d, want 2", got)
	}
}

func TestModuleApplyNeverErrorsOnExtractionFailure(t *testing.T) {
	server, client, _ := newCountingServer(t, http.StatusInternalServerError, "")
	m := NewModule(Config{Client: client, IPv4PublicAPIURL: server.URL, IPv6PublicAPIURL: server.URL})

	if err := m.Apply(context.Background(), module.ApplyRequest{Next: model.Snapshot{DDNSConfig: v4Config()}}); err != nil {
		t.Fatalf("Apply on extraction failure must return nil, got %v", err)
	}
	v4, v6 := m.LastSeenIPs(context.Background())
	if v4 != "" || v6 != "" {
		t.Fatalf("LastSeenIPs after failure = (%q,%q), want empty", v4, v6)
	}
}

func TestModuleNameAndDescriptor(t *testing.T) {
	m := NewModule(Config{})
	if m.Name() != "ddns" {
		t.Fatalf("Name = %q, want ddns", m.Name())
	}
	if m.Descriptor().Name != "ddns" {
		t.Fatalf("Descriptor.Name = %q, want ddns", m.Descriptor().Name)
	}
	if caps := m.Capabilities(model.Snapshot{}); len(caps) != 1 || caps[0].Name != "ddns_extract" {
		t.Fatalf("Capabilities = %+v, want ddns_extract", caps)
	}
}

func TestNewModuleDefaultsToMultiplePublicAPIEndpoints(t *testing.T) {
	// Out of the box the default echo set must carry more than one provider so a
	// single hung/blacklisted upstream can't black-hole DDNS extraction before
	// the operator sets NRE_DDNS_*_PUBLIC_API_URL.
	m := NewModule(Config{})
	if v4 := splitPublicAPIURLs(m.cfg.IPv4PublicAPIURL); len(v4) < 2 {
		t.Fatalf("default IPv4 endpoints = %v, want at least 2 distinct providers", v4)
	}
	if v6 := splitPublicAPIURLs(m.cfg.IPv6PublicAPIURL); len(v6) < 2 {
		t.Fatalf("default IPv6 endpoints = %v, want at least 2 distinct providers", v6)
	}
}
