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

func TestDDNSReconcileSkipsCloudflareWhenAgentSwitchDisabled(t *testing.T) {
	t.Parallel()
	cf := &fakeCFClient{outcome: cloudflareRecordOutcome{Action: "created"}}
	row := ddnsConfigRow("a1", storage.DDNSConfig{
		Enabled: false,
		Domain:  "host.example.com",
		IPv4:    storage.DDNSFamily{Enabled: true, Source: "public_api"},
	}, "203.0.113.10", "")
	// A previously-working agent keeps its historical resolution on display:
	// flipping the switch off must not erase it.
	prior, _ := json.Marshal(storage.DdnsStatus{Status: "ok", LastSuccessAtUnix: 1600, LastResolvedIPv4: "203.0.113.9"})
	row.DdnsStatusJSON = string(prior)
	store := newFakeDDNSStore(row)
	svc := NewDDNSService(enabledDDNSConfig(), store, cf, func() time.Time { return time.Unix(1700, 0) })

	svc.reconcileAgent(context.Background(), "a1")

	if got := cf.callCount(); got != 0 {
		t.Fatalf("expected no Cloudflare call when agent switch disabled, got %d", got)
	}
	status := store.status("a1")
	if status.Status != "disabled" {
		t.Fatalf("expected status=disabled, got %+v", status)
	}
	if status.LastResolvedIPv4 != "203.0.113.9" || status.LastSuccessAtUnix != 1600 {
		t.Fatalf("expected historical resolution preserved, got %+v", status)
	}

	// Settled skip: a second reconcile must not rewrite the same disabled status.
	writes := store.statusWrites
	svc.reconcileAgent(context.Background(), "a1")
	if store.statusWrites != writes {
		t.Fatalf("disabled status should settle after one write, got %d total", store.statusWrites)
	}
}

func TestDDNSIdleWhenNoConfigOrNoReportedIP(t *testing.T) {
	t.Parallel()
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
		Enabled: true,
		Domain:  "host.example.com",
		IPv4:    storage.DDNSFamily{Enabled: true, Source: "public_api"},
	}, "", ""))
	svc2 := NewDDNSService(enabledDDNSConfig(), storeNoIP, cf, time.Now)
	svc2.reconcileAgent(context.Background(), "a2")
	if got := cf.callCount(); got != 0 || storeNoIP.status("a2").Status != "idle" {
		t.Fatalf("no-ip: expected idle + 0 calls, got calls=%d status=%+v", got, storeNoIP.status("a2"))
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

func TestDDNSUpsertsEveryConfiguredDomainOnce(t *testing.T) {
	t.Parallel()
	cf := &fakeCFClient{outcome: cloudflareRecordOutcome{Action: "updated", RecordID: "rec-1"}}
	store := newFakeDDNSStore(ddnsConfigRow("a1", storage.DDNSConfig{
		Enabled: true,
		Domain:  " Host.Example.com., backup.example.net\nhost.example.com，third.example.org ",
		IPv4:    storage.DDNSFamily{Enabled: true, Source: "public_api"},
	}, "203.0.113.10", ""))
	svc := NewDDNSService(enabledDDNSConfig(), store, cf, func() time.Time { return time.Unix(1700, 0) })

	svc.reconcileAgent(context.Background(), "a1")

	wantDomains := []string{"host.example.com", "backup.example.net", "third.example.org"}
	if got := cf.callCount(); got != len(wantDomains) {
		t.Fatalf("expected one Cloudflare call per unique domain, got %d calls: %+v", got, cf.calls)
	}
	for i, want := range wantDomains {
		if cf.calls[i].fqdn != want || cf.calls[i].recordType != "A" {
			t.Fatalf("call[%d] = %+v, want fqdn=%q type=A", i, cf.calls[i], want)
		}
	}
	if status := store.status("a1"); status.Status != "ok" {
		t.Fatalf("expected status=ok, got %+v", status)
	}
}

func TestDDNSRejectsConflictingRecordOwners(t *testing.T) {
	t.Parallel()
	cf := &fakeCFClient{outcome: cloudflareRecordOutcome{Action: "updated"}}
	store := newFakeDDNSStore(
		ddnsConfigRow("a1", storage.DDNSConfig{
			Enabled: true,
			Domain:  "Host.Example.com.",
			IPv4:    storage.DDNSFamily{Enabled: true, Source: "public_api"},
		}, "203.0.113.10", ""),
		ddnsConfigRow("a2", storage.DDNSConfig{
			Enabled: true,
			Domain:  "host.example.com",
			IPv4:    storage.DDNSFamily{Enabled: true, Source: "public_api"},
		}, "203.0.113.20", ""),
	)
	svc := NewDDNSService(enabledDDNSConfig(), store, cf, time.Now)

	svc.reconcileAgent(context.Background(), "a1")
	svc.reconcileAgent(context.Background(), "a2")

	if got := cf.callCount(); got != 0 {
		t.Fatalf("conflicting owners must not update Cloudflare, got %d calls", got)
	}
	for _, agentID := range []string{"a1", "a2"} {
		status := store.status(agentID)
		if status.Status != "error" || !strings.Contains(strings.ToLower(status.LastError), "conflict") {
			t.Fatalf("status(%s) = %+v, want ownership conflict", agentID, status)
		}
	}
}

func TestDDNSRejectsOverlappingDomainLists(t *testing.T) {
	t.Parallel()
	cf := &fakeCFClient{outcome: cloudflareRecordOutcome{Action: "updated"}}
	store := newFakeDDNSStore(
		ddnsConfigRow("a1", storage.DDNSConfig{
			Enabled: true,
			Domain:  "one.example.com, shared.example.com",
			IPv4:    storage.DDNSFamily{Enabled: true, Source: "public_api"},
		}, "203.0.113.10", ""),
		ddnsConfigRow("a2", storage.DDNSConfig{
			Enabled: true,
			Domain:  "shared.example.com\ntwo.example.com",
			IPv4:    storage.DDNSFamily{Enabled: true, Source: "public_api"},
		}, "203.0.113.20", ""),
	)
	svc := NewDDNSService(enabledDDNSConfig(), store, cf, time.Now)

	svc.reconcileAgent(context.Background(), "a1")
	svc.reconcileAgent(context.Background(), "a2")

	if got := cf.callCount(); got != 0 {
		t.Fatalf("overlapping domain ownership must not update Cloudflare, got %d calls", got)
	}
	for _, agentID := range []string{"a1", "a2"} {
		status := store.status(agentID)
		if status.Status != "error" || !strings.Contains(status.LastError, "shared.example.com") {
			t.Fatalf("status(%s) = %+v, want shared-domain ownership conflict", agentID, status)
		}
	}
}

func TestDDNSAllowsDifferentRecordTypesOnSameDomain(t *testing.T) {
	t.Parallel()
	cf := &fakeCFClient{outcome: cloudflareRecordOutcome{Action: "updated"}}
	store := newFakeDDNSStore(
		ddnsConfigRow("v4-owner", storage.DDNSConfig{
			Enabled: true,
			Domain:  "host.example.com",
			IPv4:    storage.DDNSFamily{Enabled: true, Source: "public_api"},
		}, "203.0.113.10", ""),
		ddnsConfigRow("v6-owner", storage.DDNSConfig{
			Enabled: true,
			Domain:  "host.example.com",
			IPv6:    storage.DDNSFamily{Enabled: true, Source: "public_api"},
		}, "", "2001:db8::10"),
	)
	svc := NewDDNSService(enabledDDNSConfig(), store, cf, time.Now)

	svc.reconcileAgent(context.Background(), "v4-owner")
	svc.reconcileAgent(context.Background(), "v6-owner")

	if got := cf.callCount(); got != 2 {
		t.Fatalf("different record types should reconcile independently, got %d calls", got)
	}
	if store.status("v4-owner").Status != "ok" || store.status("v6-owner").Status != "ok" {
		t.Fatalf("statuses = v4:%+v v6:%+v", store.status("v4-owner"), store.status("v6-owner"))
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

func TestDDNSBackoffGateSkipsCloudflareCall(t *testing.T) {
	t.Parallel()
	cf := &fakeCFClient{outcome: cloudflareRecordOutcome{Action: "created"}}
	future := time.Now().Add(1 * time.Hour).Unix()
	priorStatus, _ := json.Marshal(storage.DdnsStatus{Status: "error", RetryCount: 1, NextRetryAtUnix: future})
	row := ddnsConfigRow("a1", storage.DDNSConfig{
		Enabled: true,
		Domain:  "host.example.com",
		IPv4:    storage.DDNSFamily{Enabled: true, Source: "public_api"},
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
	t.Parallel()
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	cf := &fakeCFClient{outcome: cloudflareRecordOutcome{Action: "created"}, entered: entered, release: release}
	store := newFakeDDNSStore(ddnsConfigRow("a1", storage.DDNSConfig{
		Enabled: true,
		Domain:  "host.example.com",
		IPv4:    storage.DDNSFamily{Enabled: true, Source: "public_api"},
	}, "203.0.113.10", ""))
	statusUpdated := make(chan string, 2)
	store.statusUpdated = statusUpdated
	svc := NewDDNSService(enabledDDNSConfig(), store, cf, time.Now)
	svc.cf = cf
	svc.Start()
	defer svc.Close()

	// Hold the first request in flight so duplicate heartbeat notifications are
	// deterministically coalesced by the dispatcher.
	svc.ReconcileAfterHeartbeat(context.Background(), "a1")
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("first Cloudflare request did not start")
	}
	svc.ReconcileAfterHeartbeat(context.Background(), "a1")
	svc.ReconcileAfterHeartbeat(context.Background(), "a1")
	close(release)
	select {
	case <-statusUpdated:
	case <-time.After(2 * time.Second):
		t.Fatal("first DDNS reconciliation did not persist status")
	}
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("coalesced DDNS reconciliation did not start")
	}
	select {
	case <-statusUpdated:
	case <-time.After(2 * time.Second):
		t.Fatal("coalesced DDNS reconciliation did not persist status")
	}
	if got := cf.callCount(); got != 2 {
		t.Fatalf("expected one in-flight call plus one coalesced dirty rerun, got %d", got)
	}
	if store.status("a1").Status != "ok" {
		t.Fatalf("expected processed status=ok, got %+v", store.status("a1"))
	}
}

func TestDDNSDispatcherDedupsInflightAgent(t *testing.T) {
	t.Parallel()
	d := newDDNSDispatcher(8)
	var processed atomic.Int32
	entered := make(chan struct{}, 1)
	block := make(chan struct{})
	d.start(context.Background(), func(context.Context, string) {
		select {
		case entered <- struct{}{}:
		default:
		}
		<-block // hold the worker so the second enqueue is dropped while inflight
		processed.Add(1)
	})
	defer close(block)

	if !d.enqueue("a1") {
		t.Fatal("first enqueue should be accepted")
	}
	<-entered
	if d.enqueue("a1") {
		t.Fatal("second enqueue while inflight should be dropped")
	}
	// A different agent is accepted.
	if !d.enqueue("a2") {
		t.Fatal("enqueue of a different agent should be accepted")
	}
}

func TestDDNSBackoffDelayGrowthAndCap(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
	cases := map[string]time.Duration{
		"ddns: rate_limited (retry_after_seconds=30)": 30 * time.Second,
		"ddns: retry_after_seconds=120 extra":         120 * time.Second,
		"ddns: no hint here":                          0,
		"":                                            0,
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

// TestHTTPCloudflareClientSurfacesRetryAfterHeader proves F3 end-to-end: a 429
// with a Retry-After header is decoded by do(), embedded in the error message,
// parsed back by extractDDNSRetryAfter, and classified as rate_limited — so the
// reconciler honors the server's requested wait rather than a fixed backoff.
func TestHTTPCloudflareClientSurfacesRetryAfterHeader(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/zones" {
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "errors": []any{}, "result": []cfZone{{ID: "zone-1", Name: "example.com"}}, "result_info": map[string]int{"total_pages": 1}})
			return
		}
		w.Header().Set("Retry-After", "45")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"success":false,"errors":[{"code":1015,"message":"You are being rate limited"}],"result":null}`))
	}))
	defer srv.Close()

	client := newHTTPCloudflareClient(srv.URL, 5*time.Second)
	_, err := client.EnsureRecord(context.Background(), "tok", "host.example.com", "A", "203.0.113.10", 120)
	if err == nil {
		t.Fatal("expected a 429 error, got nil")
	}
	if got := extractDDNSRetryAfter(err); got != 45*time.Second {
		t.Fatalf("extractDDNSRetryAfter = %v, want 45s (err=%v)", got, err)
	}
	if class := ddnsBackoffClass(err); class != "rate_limited" {
		t.Fatalf("ddnsBackoffClass = %q, want rate_limited (err=%v)", class, err)
	}
	if !strings.Contains(err.Error(), "retry_after_seconds=45") {
		t.Fatalf("error message must embed the retry_after_seconds hint, got %q", err.Error())
	}
}

// TestDDNSSweepReconcilesConfiguredAgentAndSkipsEmpty covers F4 directly: a
// single sweep reconciles agents with a DDNS config + reported IP and leaves
// agents without a config untouched (no Cloudflare call).
func TestDDNSSweepReconcilesConfiguredAgentAndSkipsEmpty(t *testing.T) {
	t.Parallel()
	cf := &fakeCFClient{outcome: cloudflareRecordOutcome{Action: "created"}}
	configured := ddnsConfigRow("a1", storage.DDNSConfig{
		Enabled: true,
		Domain:  "host.example.com",
		IPv4:    storage.DDNSFamily{Enabled: true, Source: "public_api"},
	}, "203.0.113.10", "")
	empty := storage.AgentRow{ID: "a2", LastSeenIPv4: "203.0.113.99"}
	store := newFakeDDNSStore(configured, empty)
	svc := NewDDNSService(enabledDDNSConfig(), store, cf, time.Now)
	svc.sweepInitialDelay = time.Hour
	svc.Start()
	defer svc.Close()

	svc.sweep(context.Background())
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && store.status("a1").Status != "ok" {
		time.Sleep(10 * time.Millisecond)
	}

	if status := store.status("a1"); status.Status != "ok" {
		t.Fatalf("sweep must reconcile configured agent to ok, got %+v", status)
	}
	if store.status("a2").Status != "" {
		t.Fatalf("sweep must not write status for unconfigured agent, got %+v", store.status("a2"))
	}
	if got := cf.callCount(); got != 1 {
		t.Fatalf("expected exactly 1 Cloudflare call (configured agent only), got %d", got)
	}
}

// TestDDNSSweepLoopRunsPeriodicallyAndStopsOnCancel covers F4 loop coverage:
// with an injected millisecond cadence the loop reconciles repeatedly, then
// Close() returns within a deadline (the loop observed ctx.Done and exited).
func TestDDNSSweepLoopRunsPeriodicallyAndStopsOnCancel(t *testing.T) {
	t.Parallel()
	cf := &fakeCFClient{outcome: cloudflareRecordOutcome{Action: "created"}}
	store := newFakeDDNSStore(ddnsConfigRow("a1", storage.DDNSConfig{
		Enabled: true,
		Domain:  "host.example.com",
		IPv4:    storage.DDNSFamily{Enabled: true, Source: "public_api"},
	}, "203.0.113.10", ""))
	svc := NewDDNSService(enabledDDNSConfig(), store, cf, time.Now)
	svc.sweepInitialDelay = 5 * time.Millisecond
	svc.sweepInterval = 15 * time.Millisecond
	svc.Start()

	// Wait for the loop to reconcile more than once (periodic), proving the
	// ticker re-fires and each tick re-sweeps the configured agent.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && cf.callCount() < 3 {
		time.Sleep(10 * time.Millisecond)
	}
	if got := cf.callCount(); got < 3 {
		t.Fatalf("expected the sweep loop to fire periodically (>=3 reconciles), got %d", got)
	}

	// Close must observe ctx.Done and return; if the loop doesn't honor
	// cancellation this blocks until the test timeout fails it.
	done := make(chan struct{})
	go func() { svc.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Close() did not return within 2s; sweep loop failed to observe ctx.Done")
	}
}

// TestDDNSPersistStatusDoesNotClobberConcurrentColumns covers F5: persistStatus
// writes ONLY ddns_status. A concurrent full-row write (simulated by a fresh
// admin value for an unrelated column) landed after the reconciler read the row
// must survive the DDNS status persist.
func TestDDNSPersistStatusDoesNotClobberConcurrentColumns(t *testing.T) {
	t.Parallel()
	cf := &fakeCFClient{outcome: cloudflareRecordOutcome{Action: "created"}}
	store := newFakeDDNSStore(ddnsConfigRow("a1", storage.DDNSConfig{
		Enabled: true,
		Domain:  "host.example.com",
		IPv4:    storage.DDNSFamily{Enabled: true, Source: "public_api"},
	}, "203.0.113.10", ""))
	svc := NewDDNSService(enabledDDNSConfig(), store, cf, time.Now)

	svc.reconcileAgent(context.Background(), "a1")

	// Simulate a concurrent admin edit to an unrelated field after the reconcile
	// read its (stale) row, then trigger another reconcile. The ddns_config the
	// admin wrote must survive — persistStatus only touches ddns_status.
	store.mu.Lock()
	row := store.rows["a1"]
	row.DdnsConfigJSON = `{"domain":"admin-edited.example.com","ipv4":{"enabled":true}}`
	store.rows["a1"] = row
	store.mu.Unlock()

	svc.reconcileAgent(context.Background(), "a1")

	store.mu.Lock()
	got := store.rows["a1"]
	store.mu.Unlock()
	if !strings.Contains(got.DdnsConfigJSON, "admin-edited.example.com") {
		t.Fatalf("narrow persist must not clobber admin ddns_config edit, got %q", got.DdnsConfigJSON)
	}
	if got.LastSeenIPv4 != "203.0.113.10" {
		t.Fatalf("narrow persist must not clobber reported IP, got %q", got.LastSeenIPv4)
	}
	if status := store.status("a1"); status.Status != "ok" {
		t.Fatalf("status should still be ok, got %+v", status)
	}
}

func TestDDNSDisabledIdleWritesSkipOnceSettled(t *testing.T) {
	t.Parallel()
	// F6: a disabled master records status once; subsequent reconciles (per
	// heartbeat / per sweep) must not re-write the same disabled status.
	cf := &fakeCFClient{}
	store := newFakeDDNSStore(ddnsConfigRow("a1", storage.DDNSConfig{
		Enabled: true,
		Domain:  "host.example.com",
		IPv4:    storage.DDNSFamily{Enabled: true, Source: "public_api"},
	}, "203.0.113.10", ""))
	cfg := enabledDDNSConfig()
	cfg.DDNS.Enabled = false
	cfg.DDNS.Token = ""
	svc := NewDDNSService(cfg, store, cf, time.Now)

	svc.reconcileAgent(context.Background(), "a1")
	firstWrites := store.statusWrites
	svc.reconcileAgent(context.Background(), "a1")
	svc.reconcileAgent(context.Background(), "a1")

	if store.statusWrites != firstWrites {
		t.Fatalf("disabled status should be written once, got %d writes after first (%d)", store.statusWrites, firstWrites)
	}
	if got := cf.callCount(); got != 0 {
		t.Fatalf("disabled master must never call Cloudflare, got %d", got)
	}
}

// TestHTTPCloudflareClientEnsureRecord exercises the real REST client against a
// stubbed Cloudflare API for the create / update / unchanged branches plus zone
// resolution.
func TestHTTPCloudflareClientEnsureRecord(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		existing   *cfDNSRecord // pre-seeded record for the fqdn/type
		content    string
		wantAction string
		wantWrite  bool // expect POST or PATCH
	}{
		{name: "create", existing: nil, content: "203.0.113.10", wantAction: "created", wantWrite: true},
		{name: "update", existing: &cfDNSRecord{ID: "rec-9", Type: "A", Name: "host.example.com", Content: "203.0.113.1", TTL: 120}, content: "203.0.113.10", wantAction: "updated", wantWrite: true},
		{name: "unchanged", existing: &cfDNSRecord{ID: "rec-9", Type: "A", Name: "host.example.com", Content: "203.0.113.10", TTL: 120}, content: "203.0.113.10", wantAction: "unchanged", wantWrite: false},
		{name: "proxied automatic ttl", existing: &cfDNSRecord{ID: "rec-9", Type: "A", Name: "host.example.com", Content: "203.0.113.10", TTL: 1, Proxied: true}, content: "203.0.113.10", wantAction: "unchanged", wantWrite: false},
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

func TestHTTPCloudflareClientEnsureRecordReconcilesDuplicateRecords(t *testing.T) {
	t.Parallel()
	records := []cfDNSRecord{
		{ID: "rec-current", Type: "A", Name: "host.example.com", Content: "203.0.113.10", TTL: 120},
		{ID: "rec-stale", Type: "A", Name: "host.example.com", Content: "203.0.113.20", TTL: 120},
	}
	var patched []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/zones":
			writeZones(w)
		case r.Method == http.MethodGet && r.URL.Path == "/zones/zone-1/dns_records":
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "errors": []any{}, "result": records, "result_info": map[string]int{"total_pages": 1}})
		case r.Method == http.MethodPatch && strings.HasPrefix(r.URL.Path, "/zones/zone-1/dns_records/"):
			recordID := strings.TrimPrefix(r.URL.Path, "/zones/zone-1/dns_records/")
			content := readContent(r)
			for index := range records {
				if records[index].ID == recordID {
					records[index].Content = content
				}
			}
			patched = append(patched, recordID)
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "errors": []any{}, "result": map[string]any{}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	client := newHTTPCloudflareClient(srv.URL, 5*time.Second)
	outcome, err := client.EnsureRecord(context.Background(), "cf-token", "host.example.com", "A", "203.0.113.10", 120)
	if err != nil {
		t.Fatalf("EnsureRecord error: %v", err)
	}
	if outcome.Action != "updated" {
		t.Fatalf("action = %q, want updated", outcome.Action)
	}
	if len(patched) != 1 || patched[0] != "rec-stale" {
		t.Fatalf("patched records = %v, want only rec-stale", patched)
	}
	for _, record := range records {
		if record.Content != "203.0.113.10" {
			t.Fatalf("record %s content = %q, want reconciled address", record.ID, record.Content)
		}
	}
}

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
