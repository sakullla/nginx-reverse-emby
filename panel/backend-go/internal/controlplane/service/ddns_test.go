package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

// fakeDDNSStore is an in-memory ddnsStore for service-level tests.
type fakeDDNSStore struct {
	mu    sync.Mutex
	rows  map[string]storage.AgentRow
	saved []storage.AgentRow
}

func newFakeDDNSStore(rows ...storage.AgentRow) *fakeDDNSStore {
	m := make(map[string]storage.AgentRow, len(rows))
	for _, r := range rows {
		m[r.ID] = r
	}
	return &fakeDDNSStore{rows: m}
}

func (f *fakeDDNSStore) ListAgents(context.Context) ([]storage.AgentRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]storage.AgentRow, 0, len(f.rows))
	for _, r := range f.rows {
		out = append(out, r)
	}
	return out, nil
}

func (f *fakeDDNSStore) SaveAgent(_ context.Context, row storage.AgentRow) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rows[row.ID] = row
	f.saved = append(f.saved, row)
	return nil
}

func (f *fakeDDNSStore) status(id string) storage.DdnsStatus {
	f.mu.Lock()
	defer f.mu.Unlock()
	row, ok := f.rows[id]
	if !ok {
		return storage.DdnsStatus{}
	}
	var s storage.DdnsStatus
	_ = json.Unmarshal([]byte(row.DdnsStatusJSON), &s)
	return s
}

// fakeCFClient records EnsureRecord calls and returns scripted outcomes/errors.
type fakeCFClient struct {
	mu      sync.Mutex
	calls   []fakeCFCall
	outcome cloudflareRecordOutcome
	err     error
	total   int32
}

type fakeCFCall struct {
	token      string
	fqdn       string
	recordType string
	content    string
	ttl        int
}

func (c *fakeCFClient) EnsureRecord(_ context.Context, token, fqdn, recordType, content string, ttl int) (cloudflareRecordOutcome, error) {
	atomic.AddInt32(&c.total, 1)
	c.mu.Lock()
	c.calls = append(c.calls, fakeCFCall{token, fqdn, recordType, content, ttl})
	outcome, err := c.outcome, c.err
	c.mu.Unlock()
	if err != nil {
		return cloudflareRecordOutcome{}, err
	}
	return outcome, nil
}

func (c *fakeCFClient) callCount() int { return int(atomic.LoadInt32(&c.total)) }

func ddnsConfigRow(id string, cfg storage.DDNSConfig, v4, v6 string) storage.AgentRow {
	raw, _ := json.Marshal(cfg)
	return storage.AgentRow{ID: id, DdnsConfigJSON: string(raw), LastSeenIPv4: v4, LastSeenIPv6: v6}
}

func enabledDDNSConfig() config.Config {
	return config.Config{DDNS: config.DDNSRuntimeConfig{
		Enabled: true, Token: "cf-token", TTL: 120,
	}}
}

func TestDDNSDisabledWithoutTokenMakesNoCloudflareCall(t *testing.T) {
	cf := &fakeCFClient{outcome: cloudflareRecordOutcome{Action: "created"}}
	store := newFakeDDNSStore(ddnsConfigRow("a1", storage.DDNSConfig{
		Domain: "host.example.com",
		IPv4:   storage.DDNSFamily{Enabled: true, Source: "public_api"},
	}, "203.0.113.10", ""))
	cfg := enabledDDNSConfig()
	cfg.DDNS.Enabled = false
	cfg.DDNS.Token = ""
	svc := NewDDNSService(cfg, store, cf, func() time.Time { return time.Unix(1700, 0) })

	svc.reconcileAgent(context.Background(), "a1")

	if got := cf.callCount(); got != 0 {
		t.Fatalf("expected no Cloudflare call when disabled, got %d", got)
	}
	if status := store.status("a1"); status.Status != "disabled" {
		t.Fatalf("expected status=disabled, got %+v", status)
	}
}

func TestDDNSIdleWhenNoConfigOrNoReportedIP(t *testing.T) {
	cf := &fakeCFClient{outcome: cloudflareRecordOutcome{Action: "created"}}

	// No DDNS config at all.
	storeNoCfg := newFakeDDNSStore(storage.AgentRow{ID: "a1", LastSeenIPv4: "203.0.113.10"})
	svc := NewDDNSService(enabledDDNSConfig(), storeNoCfg, cf, time.Now)
	svc.reconcileAgent(context.Background(), "a1")
	if got := cf.callCount(); got != 0 || storeNoCfg.status("a1").Status != "idle" {
		t.Fatalf("no-config: expected idle + 0 calls, got calls=%d status=%+v", got, storeNoCfg.status("a1"))
	}

	// Config present but no reported IPs.
	storeNoIP := newFakeDDNSStore(ddnsConfigRow("a2", storage.DDNSConfig{
		Domain: "host.example.com",
		IPv4:   storage.DDNSFamily{Enabled: true, Source: "public_api"},
	}, "", ""))
	svc2 := NewDDNSService(enabledDDNSConfig(), storeNoIP, cf, time.Now)
	svc2.reconcileAgent(context.Background(), "a2")
	if got := cf.callCount(); got != 0 || storeNoIP.status("a2").Status != "idle" {
		t.Fatalf("no-ip: expected idle + 0 calls, got calls=%d status=%+v", got, storeNoIP.status("a2"))
	}
}

func TestDDNSUpsertBothFamiliesSetsOKStatus(t *testing.T) {
	cf := &fakeCFClient{outcome: cloudflareRecordOutcome{Action: "updated", RecordID: "rec-1"}}
	store := newFakeDDNSStore(ddnsConfigRow("a1", storage.DDNSConfig{
		Domain: "host.example.com",
		IPv4:   storage.DDNSFamily{Enabled: true, Source: "public_api"},
		IPv6:   storage.DDNSFamily{Enabled: true, Source: "public_api"},
	}, "203.0.113.10", "2001:db8::1"))
	svc := NewDDNSService(enabledDDNSConfig(), store, cf, func() time.Time { return time.Unix(1700, 0) })

	svc.reconcileAgent(context.Background(), "a1")

	if got, want := cf.callCount(), 2; got != want {
		t.Fatalf("expected %d Cloudflare calls (A+AAAA), got %d", want, got)
	}
	if cf.calls[0].recordType != "A" || cf.calls[1].recordType != "AAAA" {
		t.Fatalf("expected A then AAAA, got %s then %s", cf.calls[0].recordType, cf.calls[1].recordType)
	}
	if cf.calls[0].token != "cf-token" {
		t.Fatalf("token must be passed from cfg, got %q", cf.calls[0].token)
	}
	status := store.status("a1")
	if status.Status != "ok" {
		t.Fatalf("expected status=ok, got %+v", status)
	}
	if status.LastResolvedIPv4 != "203.0.113.10" || status.LastResolvedIPv6 != "2001:db8::1" {
		t.Fatalf("expected resolved IPs recorded, got %+v", status)
	}
	if status.LastSuccessAtUnix != 1700 {
		t.Fatalf("expected LastSuccessAtUnix=1700, got %d", status.LastSuccessAtUnix)
	}
	if status.RetryCount != 0 || status.NextRetryAtUnix != 0 {
		t.Fatalf("expected reset retry fields, got %+v", status)
	}
}

func TestDDNSErrorsGrowBackoffAndRetryCount(t *testing.T) {
	cf := &fakeCFClient{err: fmt.Errorf("ddns: cloudflare returned status 429: rate_limited")}
	store := newFakeDDNSStore(ddnsConfigRow("a1", storage.DDNSConfig{
		Domain: "host.example.com",
		IPv4:   storage.DDNSFamily{Enabled: true, Source: "public_api"},
	}, "203.0.113.10", ""))
	now := time.Unix(1700, 0)
	svc := NewDDNSService(enabledDDNSConfig(), store, cf, func() time.Time { return now })

	svc.reconcileAgent(context.Background(), "a1")
	status := store.status("a1")
	if status.Status != "error" {
		t.Fatalf("expected status=error, got %+v", status)
	}
	if status.RetryCount != 1 {
		t.Fatalf("expected RetryCount=1, got %d", status.RetryCount)
	}
	if status.BackoffClass != "rate_limited" {
		t.Fatalf("expected BackoffClass=rate_limited, got %q", status.BackoffClass)
	}
	if status.NextRetryAtUnix <= now.Unix() {
		t.Fatalf("expected NextRetryAtUnix in the future, got %d (now=%d)", status.NextRetryAtUnix, now.Unix())
	}
}

func TestDDNSBackoffGateSkipsCloudflareCall(t *testing.T) {
	cf := &fakeCFClient{outcome: cloudflareRecordOutcome{Action: "created"}}
	future := time.Now().Add(1 * time.Hour).Unix()
	priorStatus, _ := json.Marshal(storage.DdnsStatus{Status: "error", RetryCount: 1, NextRetryAtUnix: future})
	row := ddnsConfigRow("a1", storage.DDNSConfig{
		Domain: "host.example.com",
		IPv4:   storage.DDNSFamily{Enabled: true, Source: "public_api"},
	}, "203.0.113.10", "")
	row.DdnsStatusJSON = string(priorStatus)
	store := newFakeDDNSStore(row)
	svc := NewDDNSService(enabledDDNSConfig(), store, cf, time.Now)

	svc.reconcileAgent(context.Background(), "a1")

	if got := cf.callCount(); got != 0 {
		t.Fatalf("backoff gate must skip Cloudflare, got %d calls", got)
	}
	// Status is unchanged: reconcileAgent returns before persisting.
	if store.status("a1").RetryCount != 1 {
		t.Fatalf("expected status untouched during backoff, got %+v", store.status("a1"))
	}
}

func TestDDNSReconcileAfterHeartbeatDedupsAndProcesses(t *testing.T) {
	cf := &fakeCFClient{outcome: cloudflareRecordOutcome{Action: "created"}}
	store := newFakeDDNSStore(ddnsConfigRow("a1", storage.DDNSConfig{
		Domain: "host.example.com",
		IPv4:   storage.DDNSFamily{Enabled: true, Source: "public_api"},
	}, "203.0.113.10", ""))
	svc := NewDDNSService(enabledDDNSConfig(), store, cf, time.Now)
	svc.cf = cf
	svc.Start()
	defer svc.Close()

	// Enqueue the same agent three times rapidly: dedup keeps only one in flight.
	svc.ReconcileAfterHeartbeat(context.Background(), "a1")
	svc.ReconcileAfterHeartbeat(context.Background(), "a1")
	svc.ReconcileAfterHeartbeat(context.Background(), "a1")

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cf.callCount() >= 1 && store.status("a1").Status == "ok" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := cf.callCount(); got != 1 {
		t.Fatalf("expected exactly 1 processed Cloudflare call (dedup), got %d", got)
	}
	if store.status("a1").Status != "ok" {
		t.Fatalf("expected processed status=ok, got %+v", store.status("a1"))
	}
}

func TestDDNSDispatcherDedupsInflightAgent(t *testing.T) {
	d := newDDNSDispatcher(8)
	var processed atomic.Int32
	block := make(chan struct{})
	d.start(context.Background(), func(context.Context, string) {
		<-block // hold the worker so the second enqueue is dropped while inflight
		processed.Add(1)
	})
	defer close(block)

	if !d.enqueue("a1") {
		t.Fatal("first enqueue should be accepted")
	}
	// Give the worker a moment to pick up the queued item (it is now inflight).
	time.Sleep(20 * time.Millisecond)
	if d.enqueue("a1") {
		t.Fatal("second enqueue while inflight should be dropped")
	}
	// A different agent is accepted.
	if !d.enqueue("a2") {
		t.Fatal("enqueue of a different agent should be accepted")
	}
}

func TestDDNSBackoffDelayGrowthAndCap(t *testing.T) {
	base := ddnsBackoffDelay("transient", 0, 0)
	steps := ddnsBackoffDelay("transient", 0, 3)
	if steps <= base {
		t.Fatalf("expected backoff to grow with retryCount: base=%v steps=%v", base, steps)
	}
	capped := ddnsBackoffDelay("transient", 0, 100)
	if capped > 10*time.Minute {
		t.Fatalf("transient backoff must be capped at 10m, got %v", capped)
	}
	rateLimited := ddnsBackoffDelay("rate_limited", 0, 100)
	if rateLimited > time.Hour {
		t.Fatalf("rate_limited backoff must be capped at 1h, got %v", rateLimited)
	}
}

func TestDDNSExtractRetryAfterParsesHint(t *testing.T) {
	cases := map[string]time.Duration{
		"ddns: rate_limited (retry_after_seconds=30)":  30 * time.Second,
		"ddns: retry_after_seconds=120 extra":          120 * time.Second,
		"ddns: no hint here":                           0,
		"":                                             0,
	}
	for msg, want := range cases {
		if got := extractDDNSRetryAfter(errors.New(msg)); got != want {
			t.Errorf("extractDDNSRetryAfter(%q) = %v, want %v", msg, got, want)
		}
	}
	// retryAfter overrides upward but is still capped.
	delay := ddnsBackoffDelay("transient", 7*time.Minute, 0)
	if delay != 7*time.Minute {
		t.Errorf("expected retryAfter (7m) to override base within cap, got %v", delay)
	}
}

// TestHTTPCloudflareClientEnsureRecord exercises the real REST client against a
// stubbed Cloudflare API for the create / update / unchanged branches plus zone
// resolution.
func TestHTTPCloudflareClientEnsureRecord(t *testing.T) {
	cases := []struct {
		name        string
		existing    *cfDNSRecord // pre-seeded record for the fqdn/type
		content     string
		wantAction  string
		wantWrite   bool // expect POST or PATCH
	}{
		{name: "create", existing: nil, content: "203.0.113.10", wantAction: "created", wantWrite: true},
		{name: "update", existing: &cfDNSRecord{ID: "rec-9", Type: "A", Name: "host.example.com", Content: "203.0.113.1"}, content: "203.0.113.10", wantAction: "updated", wantWrite: true},
		{name: "unchanged", existing: &cfDNSRecord{ID: "rec-9", Type: "A", Name: "host.example.com", Content: "203.0.113.10"}, content: "203.0.113.10", wantAction: "unchanged", wantWrite: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var writeCount atomic.Int32
			records := map[string]cfDNSRecord{}
			if tc.existing != nil {
				records["A:host.example.com"] = *tc.existing
			}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.URL.Path == "/zones":
					writeZones(w)
				case strings.Contains(r.URL.Path, "/dns_records"):
					handleRecords(w, r, records, &writeCount)
				default:
					http.NotFound(w, r)
				}
			}))
			defer srv.Close()

			client := newHTTPCloudflareClient(srv.URL, 5*time.Second)
			outcome, err := client.EnsureRecord(context.Background(), "cf-token", "host.example.com", "A", tc.content, 120)
			if err != nil {
				t.Fatalf("EnsureRecord error: %v", err)
			}
			if outcome.Action != tc.wantAction {
				t.Fatalf("action = %q, want %q", outcome.Action, tc.wantAction)
			}
			if got := writeCount.Load(); (got > 0) != tc.wantWrite {
				t.Fatalf("write happened=%v, want write=%v (count=%d)", got > 0, tc.wantWrite, got)
			}
		})
	}
}

func writeZones(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"success": true,
		"errors":  []any{},
		"result":  []cfZone{{ID: "zone-1", Name: "example.com"}},
		"result_info": map[string]int{"total_pages": 1},
	})
}

func handleRecords(w http.ResponseWriter, r *http.Request, records map[string]cfDNSRecord, writeCount *atomic.Int32) {
	w.Header().Set("Content-Type", "application/json")
	key := "A:host.example.com"
	switch r.Method {
	case http.MethodGet:
		var list []cfDNSRecord
		if rec, ok := records[key]; ok {
			list = append(list, rec)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "errors": []any{}, "result": list, "result_info": map[string]int{"total_pages": 1}})
	case http.MethodPost:
		writeCount.Add(1)
		records[key] = cfDNSRecord{ID: "rec-new", Type: "A", Name: "host.example.com", Content: readContent(r)}
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "errors": []any{}, "result": records[key]})
	case http.MethodPatch:
		writeCount.Add(1)
		rec := records[key]
		rec.Content = readContent(r)
		records[key] = rec
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "errors": []any{}, "result": rec})
	default:
		http.NotFound(w, r)
	}
}

func readContent(r *http.Request) string {
	var body struct {
		Content string `json:"content"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	return body.Content
}
