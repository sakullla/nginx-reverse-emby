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

// ddnsStore is the narrow storage surface the DDNS reconciler needs. It is
// satisfied by the production store (ListAgents + SaveAgent). The reconciler
// cannot edit storage/**, so it reuses these existing full-row methods: it reads
// the agent row, mutates only DdnsStatusJSON, and writes the row back. A
// heartbeat landing between read and write only races the (redundant) IP
// columns, which the next heartbeat corrects — acceptable for MVP.
type ddnsStore interface {
	ListAgents(context.Context) ([]storage.AgentRow, error)
	SaveAgent(context.Context, storage.AgentRow) error
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
	ctx       context.Context
	cancel    context.CancelFunc
	done      chan struct{}
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
		done:       make(chan struct{}),
	}
}

// Start launches the dispatcher worker and the background sweep loop. It is
// idempotent. The goroutines run until Close cancels the internal context.
func (s *DDNSService) Start() {
	s.startOnce.Do(func() {
		if s.cf == nil {
			s.cf = newHTTPCloudflareClient(s.cfg.DDNS.APIBase, s.cfg.DDNS.Timeout)
		}
		s.ctx, s.cancel = context.WithCancel(context.Background())
		s.dispatcher.start(s.ctx, s.reconcileAgent)
		go s.runSweepLoop(s.ctx)
	})
}

// Close cancels the internal context and waits for the worker + sweep loop to
// stop. It is idempotent.
func (s *DDNSService) Close() {
	s.stopOnce.Do(func() {
		if s.cancel != nil {
			s.cancel()
		}
		s.dispatcher.stop()
		select {
		case <-s.done:
		default:
			close(s.done)
		}
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
	defer func() {
		select {
		case <-s.done:
		default:
			close(s.done)
		}
	}()
	interval := s.cfg.DDNS.Interval
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	initial := time.NewTimer(30 * time.Second)
	defer initial.Stop()
	select {
	case <-ctx.Done():
		return
	case <-initial.C:
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
	cfg := parseDDNSConfig(row.DdnsConfigJSON)

	// Disabled master (no token): record the reason once and stop. No Cloudflare
	// call is ever made without a credential.
	if !s.cfg.DDNS.Enabled || strings.TrimSpace(s.cfg.DDNS.Token) == "" {
		s.persistStatus(ctx, row, storage.DdnsStatus{Status: "disabled", LastError: "cloudflare token not configured"})
		return
	}
	if cfg == nil || strings.TrimSpace(cfg.Domain) == "" {
		s.persistStatus(ctx, row, storage.DdnsStatus{Status: "idle", LastError: ""})
		return
	}

	desired := s.desiredRecords(cfg, row)
	if len(desired) == 0 {
		s.persistStatus(ctx, row, storage.DdnsStatus{Status: "idle", LastError: ""})
		return
	}

	prior := parseDDNSStatus(row.DdnsStatusJSON)
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
			Status:            "error",
			LastError:         truncateDDNSError(err.Error()),
			RetryCount:        retryCount,
			NextRetryAtUnix:   s.now().Add(delay).Unix(),
			BackoffClass:      class,
			LastResolvedIPv4:  prior.LastResolvedIPv4,
			LastResolvedIPv6:  prior.LastResolvedIPv6,
		}
		s.persistStatus(ctx, row, status)
		return
	}

	status := storage.DdnsStatus{
		Status:            "ok",
		LastSuccessAtUnix: s.now().Unix(),
		LastResolvedIPv4:  resolvedContent(desired, "A"),
		LastResolvedIPv6:  resolvedContent(desired, "AAAA"),
	}
	s.persistStatus(ctx, row, status)
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

func (s *DDNSService) persistStatus(ctx context.Context, row storage.AgentRow, status storage.DdnsStatus) {
	encoded, err := json.Marshal(status)
	if err != nil {
		return
	}
	row.DdnsStatusJSON = string(encoded)
	if err := s.store.SaveAgent(ctx, row); err != nil {
		log.Printf("[ddns] persist status for agent %q failed: %v", row.ID, err)
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
