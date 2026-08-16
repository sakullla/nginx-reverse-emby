//go:build !integration

package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

// fakeDDNSStore is an in-memory ddnsStore for service-level tests. Its
// UpdateDdnsStatusColumn mirrors the production narrow update: it touches ONLY
// the ddns_status column, leaving every other field (admin config, reported IPs,
// token, etc.) untouched so tests can assert no clobbering.
type fakeDDNSStore struct {
	mu            sync.Mutex
	rows          map[string]storage.AgentRow
	statusWrites  int
	statusUpdated chan<- string
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

func (f *fakeDDNSStore) UpdateDdnsStatusColumn(_ context.Context, agentID, statusJSON string) error {
	f.mu.Lock()
	row := f.rows[agentID]
	row.DdnsStatusJSON = statusJSON
	f.rows[agentID] = row
	f.statusWrites++
	f.mu.Unlock()
	if f.statusUpdated != nil {
		f.statusUpdated <- agentID
	}
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
	entered chan<- struct{}
	release <-chan struct{}
}

type fakeCFCall struct {
	token      string
	fqdn       string
	recordType string
	content    string
	ttl        int
}

func (c *fakeCFClient) EnsureRecord(ctx context.Context, token, fqdn, recordType, content string, ttl int) (cloudflareRecordOutcome, error) {
	atomic.AddInt32(&c.total, 1)
	c.mu.Lock()
	c.calls = append(c.calls, fakeCFCall{token, fqdn, recordType, content, ttl})
	outcome, err := c.outcome, c.err
	c.mu.Unlock()
	if c.entered != nil {
		select {
		case c.entered <- struct{}{}:
		default:
		}
	}
	if c.release != nil {
		select {
		case <-c.release:
		case <-ctx.Done():
			return cloudflareRecordOutcome{}, ctx.Err()
		}
	}
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
	t.Parallel()
	cf := &fakeCFClient{outcome: cloudflareRecordOutcome{Action: "created"}}
	store := newFakeDDNSStore(ddnsConfigRow("a1", storage.DDNSConfig{
		Enabled: true,
		Domain:  "host.example.com",
		IPv4:    storage.DDNSFamily{Enabled: true, Source: "public_api"},
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

func TestDDNSUpsertBothFamiliesSetsOKStatus(t *testing.T) {
	t.Parallel()
	cf := &fakeCFClient{outcome: cloudflareRecordOutcome{Action: "updated", RecordID: "rec-1"}}
	store := newFakeDDNSStore(ddnsConfigRow("a1", storage.DDNSConfig{
		Enabled: true,
		Domain:  "host.example.com",
		IPv4:    storage.DDNSFamily{Enabled: true, Source: "public_api"},
		IPv6:    storage.DDNSFamily{Enabled: true, Source: "public_api"},
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
	t.Parallel()
	cf := &fakeCFClient{err: fmt.Errorf("ddns: cloudflare returned status 429: rate_limited")}
	store := newFakeDDNSStore(ddnsConfigRow("a1", storage.DDNSConfig{
		Enabled: true,
		Domain:  "host.example.com",
		IPv4:    storage.DDNSFamily{Enabled: true, Source: "public_api"},
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

// Hold the first request in flight so duplicate heartbeat notifications are
// deterministically coalesced by the dispatcher.

// hold the worker so the second enqueue is dropped while inflight

// A different agent is accepted.

// TestHTTPCloudflareClientSurfacesRetryAfterHeader proves F3 end-to-end: a 429
// with a Retry-After header is decoded by do(), embedded in the error message,
// parsed back by extractDDNSRetryAfter, and classified as rate_limited — so the
// reconciler honors the server's requested wait rather than a fixed backoff.
// TestDDNSSweepReconcilesConfiguredAgentAndSkipsEmpty covers F4 directly: a
// single sweep reconciles agents with a DDNS config + reported IP and leaves
// agents without a config untouched (no Cloudflare call).
// TestDDNSSweepLoopRunsPeriodicallyAndStopsOnCancel covers F4 loop coverage:
// with an injected millisecond cadence the loop reconciles repeatedly, then
// Close() returns within a deadline (the loop observed ctx.Done and exited).

// Wait for the loop to reconcile more than once (periodic), proving the
// ticker re-fires and each tick re-sweeps the configured agent.

// Close must observe ctx.Done and return; if the loop doesn't honor
// cancellation this blocks until the test timeout fails it.

// TestDDNSPersistStatusDoesNotClobberConcurrentColumns covers F5: persistStatus
// writes ONLY ddns_status. A concurrent full-row write (simulated by a fresh
// admin value for an unrelated column) landed after the reconciler read the row
// must survive the DDNS status persist.

// Simulate a concurrent admin edit to an unrelated field after the reconcile
// read its (stale) row, then trigger another reconcile. The ddns_config the
// admin wrote must survive — persistStatus only touches ddns_status.

// TestHTTPCloudflareClientEnsureRecord exercises the real REST client against a
// stubbed Cloudflare API for the create / update / unchanged branches plus zone
// resolution.
func writeZones(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"success":     true,
		"errors":      []any{},
		"result":      []cfZone{{ID: "zone-1", Name: "example.com"}},
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
