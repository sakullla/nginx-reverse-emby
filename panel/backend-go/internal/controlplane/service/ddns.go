package service

import (
	"context"
	"encoding/json"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

// ddnsStore is the narrow storage surface the DDNS reconciler needs.
// ListAgents reads rows for the sweep; UpdateDdnsStatusColumn writes ONLY the
// ddns_status column via a targeted update (not a full-row upsert), so the
// reconciler can never clobber concurrent full-row writes from heartbeats or
// admin edits during the Cloudflare call window.
type ddnsStore interface {
	ListAgents(context.Context) ([]storage.AgentRow, error)
	UpdateDdnsStatusColumn(ctx context.Context, agentID, statusJSON string) error
}

// DDNSService is the master-side dynamic DNS reconciler. It implements
// service.DDNSReconciler (ReconcileAfterHeartbeat) for the heartbeat trigger and
// runs a background sweep loop as a fallback for retries and agents whose
// heartbeats have gone quiet. The Cloudflare token lives only in cfg (env); this
// struct never persists, logs, or dispatches a credential (R7).
type DDNSService struct {
	cfg   config.Config
	store ddnsStore
	cf    cloudflareDNSClient
	now   func() time.Time

	dispatcher *ddnsDispatcher

	startOnce sync.Once
	stopOnce  sync.Once
	cancel    context.CancelFunc
	sweepWG   sync.WaitGroup

	// sweepInitialDelay / sweepInterval override the sweep cadence; they default
	// to 30s initial + cfg.DDNS.Interval in runSweepLoop and exist so tests can
	// drive the loop on a millisecond timescale without sleeping for 30s.
	sweepInitialDelay time.Duration
	sweepInterval     time.Duration
}

// NewDDNSService constructs a reconciler that uses cf to upsert Cloudflare
// records. cf may be nil, in which case Start builds the real HTTP client from
// cfg (production wiring); tests inject a fake.
func NewDDNSService(cfg config.Config, store ddnsStore, cf cloudflareDNSClient, now func() time.Time) *DDNSService {
	if now == nil {
		now = time.Now
	}
	return &DDNSService{
		cfg:        cfg,
		store:      store,
		cf:         cf,
		now:        now,
		dispatcher: newDDNSDispatcher(64),
	}
}

// Start launches the dispatcher worker and the background sweep loop. It is
// idempotent. The goroutines run under a single internal context that Close
// cancels; Close then waits for both to exit before returning.
func (s *DDNSService) Start() {
	s.startOnce.Do(func() {
		if s.cf == nil {
			s.cf = newHTTPCloudflareClient(s.cfg.DDNS.APIBase, s.cfg.DDNS.Timeout)
		}
		ctx, cancel := context.WithCancel(context.Background())
		s.cancel = cancel
		s.dispatcher.start(ctx, s.reconcileAgent)
		s.sweepWG.Add(1)
		go func() {
			defer s.sweepWG.Done()
			s.runSweepLoop(ctx)
		}()
	})
}

// Close cancels the internal context and blocks until the dispatcher worker and
// sweep loop have both exited, so shutdown never races a goroutine that still
// holds the store. It is idempotent and safe to call when Start was never called.
func (s *DDNSService) Close() {
	s.stopOnce.Do(func() {
		if s.cancel != nil {
			s.cancel()
		}
		s.dispatcher.stop() // waits for the worker to observe ctx.Done
		s.sweepWG.Wait()    // waits for the sweep loop to observe ctx.Done
	})
}

// ReconcileAfterHeartbeat is the fire-and-forget heartbeat trigger required by
// service.DDNSReconciler. It only enqueues the agent ID and returns immediately;
// the dispatcher worker performs the actual Cloudflare work asynchronously so
// DNS failures can never break the heartbeat main path.
func (s *DDNSService) ReconcileAfterHeartbeat(_ context.Context, agentID string) {
	if agentID == "" {
		return
	}
	s.dispatcher.enqueue(agentID)
}

// runSweepLoop is the fallback reconciler: on each interval it re-reconciles
// every agent with a DDNS config. This picks up retries (honoring backoff) and
// agents whose heartbeats have paused but still need their records refreshed.
func (s *DDNSService) runSweepLoop(ctx context.Context) {
	interval := s.sweepInterval
	if interval <= 0 {
		interval = s.cfg.DDNS.Interval
	}
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	initial := s.sweepInitialDelay
	if initial <= 0 {
		initial = 30 * time.Second
	}
	initialTimer := time.NewTimer(initial)
	defer initialTimer.Stop()
	select {
	case <-ctx.Done():
		return
	case <-initialTimer.C:
		s.sweep(ctx)
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.sweep(ctx)
		}
	}
}

// sweep re-reconciles every agent whose DDNS config targets a domain.
func (s *DDNSService) sweep(ctx context.Context) {
	if s.store == nil {
		return
	}
	rows, err := s.store.ListAgents(ctx)
	if err != nil {
		log.Printf("[ddns] sweep list agents failed: %v", err)
		return
	}
	for _, row := range rows {
		if ctx.Err() != nil {
			return
		}
		cfg := parseDDNSConfig(row.DdnsConfigJSON)
		if cfg == nil || strings.TrimSpace(cfg.Domain) == "" {
			continue
		}
		s.reconcileAgent(ctx, row.ID)
	}
}

// reconcileAgent is the single entry point for all DDNS work (dispatcher worker
// and sweep). It reads fresh state, decides which records to publish, performs
// the Cloudflare upsert honoring backoff, and persists the resulting DdnsStatus.
// All errors are contained here: the dispatcher wraps it and the heartbeat path
// recovers any panic, so DNS issues never escape.
func (s *DDNSService) reconcileAgent(ctx context.Context, agentID string) {
	if s.store == nil || agentID == "" {
		return
	}
	row, ok := s.lookupAgent(ctx, agentID)
	if !ok {
		return
	}
	prior := parseDDNSStatus(row.DdnsStatusJSON)
	cfg := parseDDNSConfig(row.DdnsConfigJSON)

	// Disabled master (no token): record the reason and stop. No Cloudflare call
	// is ever made without a credential. Skip the write once it has settled so a
	// busy agent (per-heartbeat enqueue + per-sweep) does not amplify DB writes.
	if !s.cfg.DDNS.Enabled || strings.TrimSpace(s.cfg.DDNS.Token) == "" {
		if prior.Status != "disabled" {
			s.persistStatus(ctx, agentID, storage.DdnsStatus{Status: "disabled", LastError: "cloudflare token not configured"})
		}
		return
	}
	if cfg == nil || strings.TrimSpace(cfg.Domain) == "" {
		if prior.Status != "idle" {
			s.persistStatus(ctx, agentID, storage.DdnsStatus{Status: "idle", LastError: ""})
		}
		return
	}

	// Per-agent switch off: keep the historical resolution on display, mark the
	// status disabled, and never touch Cloudflare. Skip the write once settled
	// so a busy agent does not amplify DB writes.
	if !cfg.Enabled {
		if prior.Status != "disabled" {
			s.persistStatus(ctx, agentID, storage.DdnsStatus{
				Status:            "disabled",
				LastError:         "ddns disabled by agent switch",
				LastSuccessAtUnix: prior.LastSuccessAtUnix,
				LastResolvedIPv4:  prior.LastResolvedIPv4,
				LastResolvedIPv6:  prior.LastResolvedIPv6,
			})
		}
		return
	}

	desired := s.desiredRecords(cfg, row)
	if len(desired) == 0 {
		if prior.Status != "idle" {
			s.persistStatus(ctx, agentID, storage.DdnsStatus{Status: "idle", LastError: ""})
		}
		return
	}

	// Backoff gate: a failed agent waits until NextRetryAtUnix before touching
	// Cloudflare again, preventing retry storms.
	if prior.RetryCount > 0 && prior.NextRetryAtUnix > s.now().Unix() {
		return
	}

	if err := s.upsertRecords(ctx, cfg.Domain, desired); err != nil {
		retryCount := prior.RetryCount + 1
		class := ddnsBackoffClass(err)
		delay := ddnsBackoffDelay(class, extractDDNSRetryAfter(err), retryCount)
		status := storage.DdnsStatus{
			Status:           "error",
			LastError:        truncateDDNSError(err.Error()),
			RetryCount:       retryCount,
			NextRetryAtUnix:  s.now().Add(delay).Unix(),
			BackoffClass:     class,
			LastResolvedIPv4: prior.LastResolvedIPv4,
			LastResolvedIPv6: prior.LastResolvedIPv6,
		}
		s.persistStatus(ctx, agentID, status)
		return
	}

	status := storage.DdnsStatus{
		Status:           "ok",
		LastSuccessAtUnix: s.now().Unix(),
		LastResolvedIPv4:  resolvedContent(desired, "A"),
		LastResolvedIPv6:  resolvedContent(desired, "AAAA"),
	}
	s.persistStatus(ctx, agentID, status)
}

// desiredRecord pairs a Cloudflare record type with the address to publish.
type desiredRecord struct {
	recordType string
	content    string
}

func (s *DDNSService) desiredRecords(cfg *storage.DDNSConfig, row storage.AgentRow) []desiredRecord {
	var out []desiredRecord
	if cfg.IPv4.Enabled && strings.TrimSpace(row.LastSeenIPv4) != "" {
		out = append(out, desiredRecord{recordType: "A", content: strings.TrimSpace(row.LastSeenIPv4)})
	}
	if cfg.IPv6.Enabled && strings.TrimSpace(row.LastSeenIPv6) != "" {
		out = append(out, desiredRecord{recordType: "AAAA", content: strings.TrimSpace(row.LastSeenIPv6)})
	}
	return out
}

func (s *DDNSService) upsertRecords(ctx context.Context, domain string, desired []desiredRecord) error {
	for _, rec := range desired {
		if _, err := s.cf.EnsureRecord(ctx, s.cfg.DDNS.Token, domain, rec.recordType, rec.content, s.cfg.DDNS.TTL); err != nil {
			return err
		}
	}
	return nil
}

func (s *DDNSService) lookupAgent(ctx context.Context, agentID string) (storage.AgentRow, bool) {
	rows, err := s.store.ListAgents(ctx)
	if err != nil {
		return storage.AgentRow{}, false
	}
	for _, row := range rows {
		if row.ID == agentID {
			return row, true
		}
	}
	return storage.AgentRow{}, false
}

// persistStatus writes only the ddns_status column for agentID. It deliberately
// uses a narrow column update (not a full-row upsert) so a reconcile that read a
// stale row cannot clobber concurrent full-row writes from heartbeats or admin
// edits (agent config, desired_revision, token rotation) landed during the
// Cloudflare call window.
func (s *DDNSService) persistStatus(ctx context.Context, agentID string, status storage.DdnsStatus) {
	encoded, err := json.Marshal(status)
	if err != nil {
		return
	}
	if err := s.store.UpdateDdnsStatusColumn(ctx, agentID, string(encoded)); err != nil {
		log.Printf("[ddns] persist status for agent %q failed: %v", agentID, err)
	}
}

func resolvedContent(desired []desiredRecord, recordType string) string {
	for _, rec := range desired {
		if rec.recordType == recordType {
			return rec.content
		}
	}
	return ""
}

// parseDDNSConfig decodes a stored DDNSConfig JSON column. It returns nil when
// the column is empty or unparseable (treated as "no DDNS configured").
func parseDDNSConfig(raw string) *storage.DDNSConfig {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var cfg storage.DDNSConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return nil
	}
	return &cfg
}

// parseDDNSStatus decodes a stored DdnsStatus JSON column (defensive mirror of
// the service-layer helper so this package stays self-contained).
func parseDDNSStatus(raw string) storage.DdnsStatus {
	if strings.TrimSpace(raw) == "" {
		return storage.DdnsStatus{}
	}
	var status storage.DdnsStatus
	if err := json.Unmarshal([]byte(raw), &status); err != nil {
		return storage.DdnsStatus{}
	}
	return status
}

func truncateDDNSError(msg string) string {
	const limit = 500
	if len(msg) <= limit {
		return msg
	}
	return msg[:limit]
}

// ddnsBackoffClass categorizes an upsert failure so the delay schedule can
// distinguish rate limiting (long backoff) from ordinary transient errors.
func ddnsBackoffClass(err error) string {
	if err == nil {
		return ""
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "rate_limited") || strings.Contains(msg, "status 429") || strings.Contains(msg, "429") {
		return "rate_limited"
	}
	return "transient"
}

func ddnsBackoffClassBaseAndCap(class string) (time.Duration, time.Duration) {
	switch class {
	case "rate_limited":
		return 60 * time.Second, time.Hour
	default:
		return 5 * time.Second, 10 * time.Minute
	}
}

// ddnsBackoffDelay computes an exponential backoff capped at the class ceiling.
// retryAfter (parsed from a Cloudflare Retry-After hint) overrides upward when
// the server explicitly asks us to wait longer.
func ddnsBackoffDelay(class string, retryAfter time.Duration, retryCount int) time.Duration {
	base, ceiling := ddnsBackoffClassBaseAndCap(class)
	delay := base
	for i := 0; i < retryCount && delay < ceiling; i++ {
		delay *= 2
	}
	if delay > ceiling {
		delay = ceiling
	}
	if retryAfter > delay {
		delay = retryAfter
	}
	if delay > ceiling {
		delay = ceiling
	}
	if delay < time.Second {
		delay = time.Second
	}
	return delay
}

// extractDDNSRetryAfter best-effort parses a "retry_after_seconds=N" hint
// embedded in an error message (e.g. emitted by the Cloudflare client on 429).
func extractDDNSRetryAfter(err error) time.Duration {
	if err == nil {
		return 0
	}
	msg := err.Error()
	const marker = "retry_after_seconds="
	idx := strings.Index(msg, marker)
	if idx < 0 {
		return 0
	}
	rest := msg[idx+len(marker):]
	num := strings.Builder{}
	for _, r := range rest {
		if r < '0' || r > '9' {
			break
		}
		num.WriteRune(r)
	}
	if num.Len() == 0 {
		return 0
	}
	n, err := strconv.Atoi(num.String())
	if err != nil || n <= 0 {
		return 0
	}
	return time.Duration(n) * time.Second
}
