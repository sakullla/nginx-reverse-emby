package app

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/store"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/traffic"
)

const (
	runtimeMetaTrafficStatsInterval       = "traffic_stats_interval"
	runtimeMetaLastTrafficStatsReportUnix = "last_traffic_stats_report_unix"
	runtimeMetaTrafficBlocked             = "traffic_blocked"
	runtimeMetaTrafficBlockReason         = "traffic_block_reason"
)

func (a *App) syncRequest(ctx context.Context, applied Snapshot) (SyncRequest, error) {
	req := SyncRequest{CurrentRevision: int(applied.Revision)}
	a.pendingTrafficStatsReportUnix = ""

	state, err := a.store.LoadRuntimeState()
	if err != nil {
		return SyncRequest{}, err
	}
	meta := ensureMetadata(state.Metadata)
	req.LastApplyRevision = int(parseInt64(meta["last_apply_revision"], applied.Revision))
	req.LastApplyStatus = strings.TrimSpace(meta["last_apply_status"])
	req.LastApplyMessage = meta["last_apply_message"]
	if req.LastApplyStatus == "" {
		req.LastApplyStatus = "success"
	}
	if !traffic.Enabled() {
		req.Stats = map[string]any{}
		req.StatsPresent = true
	} else if now := time.Now(); shouldReportTrafficStats(meta, now) {
		stats := traffic.SnapshotNonZero()
		if hostStats, err := a.hostTrafficSnapshot(); err != nil {
			log.Printf("[agent] host traffic snapshot error: %v", err)
		} else if hostStats != nil {
			stats = mergeTrafficStats(stats, hostStats)
		}
		if stats != nil {
			req.Stats = stats
			req.StatsPresent = true
			if hasTrafficStatsInterval(meta) {
				a.pendingTrafficStatsReportUnix = strconv.FormatInt(now.Unix(), 10)
			}
		}
	}

	if reporter, ok := a.certApplier.(ManagedCertificateReporter); ok {
		reports, err := reporter.ManagedCertificateReports(ctx)
		if err != nil {
			return SyncRequest{}, err
		}
		req.ManagedCertificateReports = reports
	}

	return req, nil
}

func (a *App) hostTrafficSnapshot() (map[string]any, error) {
	if a.hostTrafficCollector == nil {
		return nil, nil
	}
	snapshot, err := a.hostTrafficCollector.Snapshot()
	if err != nil {
		return nil, err
	}
	if snapshot.Total.RXBytes == 0 && snapshot.Total.TXBytes == 0 && len(snapshot.Interfaces) == 0 {
		return nil, nil
	}
	return snapshot.Payload(), nil
}

func mergeTrafficStats(base, extra map[string]any) map[string]any {
	if extra == nil {
		return base
	}
	if base == nil {
		base = map[string]any{}
	}
	baseTraffic, _ := base["traffic"].(map[string]any)
	if baseTraffic == nil {
		baseTraffic = map[string]any{}
		base["traffic"] = baseTraffic
	}
	extraTraffic, _ := extra["traffic"].(map[string]any)
	for key, value := range extraTraffic {
		baseTraffic[key] = value
	}
	return base
}

func shouldReportTrafficStats(meta map[string]string, now time.Time) bool {
	interval, err := time.ParseDuration(strings.TrimSpace(meta[runtimeMetaTrafficStatsInterval]))
	if err != nil || interval <= 0 {
		return true
	}
	lastReportUnix, err := strconv.ParseInt(strings.TrimSpace(meta[runtimeMetaLastTrafficStatsReportUnix]), 10, 64)
	if err != nil || lastReportUnix <= 0 {
		return true
	}
	return !now.Before(time.Unix(lastReportUnix, 0).Add(interval))
}

func hasTrafficStatsInterval(meta map[string]string) bool {
	interval, err := time.ParseDuration(strings.TrimSpace(meta[runtimeMetaTrafficStatsInterval]))
	return err == nil && interval > 0
}

func (a *App) persistTrafficStatsInterval(raw string) error {
	state, err := a.store.LoadRuntimeState()
	if err != nil {
		return err
	}
	state.Metadata = ensureMetadata(state.Metadata)
	if err := setTrafficStatsIntervalMetadata(state.Metadata, raw); err != nil {
		return err
	}
	return a.store.SaveRuntimeState(state)
}

func parseTrafficStatsInterval(raw string) (string, error) {
	interval := strings.TrimSpace(raw)
	if interval == "" {
		return "", nil
	}
	parsed, err := time.ParseDuration(interval)
	if err != nil {
		return "", fmt.Errorf("traffic_stats_interval: %w", err)
	}
	if parsed <= 0 {
		return "", fmt.Errorf("traffic_stats_interval must be positive")
	}
	return interval, nil
}

func setTrafficStatsIntervalMetadata(meta map[string]string, raw string) error {
	interval, err := parseTrafficStatsInterval(raw)
	if err != nil {
		return err
	}
	if interval == "" {
		delete(meta, runtimeMetaTrafficStatsInterval)
		return nil
	}
	meta[runtimeMetaTrafficStatsInterval] = interval
	return nil
}

func (a *App) syncOnce(ctx context.Context, req SyncRequest) error {
	snapshot, err := a.syncClient.Sync(ctx, req)
	if err != nil {
		log.Printf("[agent] sync error: %v", err)
		return a.recordRuntimeError(err)
	}
	if req.Stats != nil && a.pendingTrafficStatsReportUnix != "" {
		if err := a.persistLastTrafficStatsReportUnix(a.pendingTrafficStatsReportUnix); err != nil {
			return a.recordRuntimeError(err)
		}
		a.pendingTrafficStatsReportUnix = ""
	}
	existingDesired, err := a.store.LoadDesiredSnapshot()
	if err != nil {
		return a.recordRuntimeError(err)
	}
	persistedSnapshot := mergeSnapshotPayload(snapshot, existingDesired)
	if err := a.store.SaveDesiredSnapshot(persistedSnapshot); err != nil {
		return a.recordRuntimeError(err)
	}
	if err := a.handlePendingUpdate(ctx, persistedSnapshot); err != nil {
		return err
	}
	previousApplied := a.runtime.ActiveSnapshot()
	candidateApplied := mergeSnapshotPayload(snapshot, previousApplied)
	if err := a.runtime.Apply(ctx, previousApplied, candidateApplied); err != nil {
		log.Printf("[agent] runtime apply error at revision %d: %v", candidateApplied.Revision, err)
		a.rollbackRuntime(ctx, candidateApplied, previousApplied)
		return a.recordRuntimeErrorWithRevision(err, candidateApplied.Revision)
	}
	if err := a.store.SaveAppliedSnapshot(candidateApplied); err != nil {
		log.Printf("[agent] save applied snapshot error at revision %d: %v", candidateApplied.Revision, err)
		a.rollbackRuntime(ctx, candidateApplied, previousApplied)
		return a.recordPersistedRuntimeErrorWithRevision(err, candidateApplied.Revision)
	}
	if err := a.persistRuntimeState(true); err != nil {
		a.rollbackRuntime(ctx, candidateApplied, previousApplied)
		_ = a.store.SaveAppliedSnapshot(previousApplied)
		return a.recordPersistedRuntimeErrorWithRevision(err, candidateApplied.Revision)
	}
	return nil
}

func (a *App) recordRuntimeError(syncErr error) error {
	return a.recordRuntimeErrorWithRevision(syncErr, a.runtime.ActiveSnapshot().Revision)
}

func (a *App) persistLastTrafficStatsReportUnix(timestamp string) error {
	state, err := a.store.LoadRuntimeState()
	if err != nil {
		return err
	}
	state.Metadata = ensureMetadata(state.Metadata)
	state.Metadata[runtimeMetaLastTrafficStatsReportUnix] = timestamp
	return a.store.SaveRuntimeState(state)
}

func (a *App) recordRuntimeErrorWithRevision(syncErr error, revision int64) error {
	state, err := a.runtimeStateForPersistence()
	if err != nil {
		return syncErr
	}
	state.Metadata = ensureMetadata(state.Metadata)
	state.Metadata["last_sync_error"] = syncErr.Error()
	setApplyMetadata(state.Metadata, revision, "error", syncErr.Error())
	if err := a.store.SaveRuntimeState(state); err != nil {
		return syncErr
	}
	return syncErr
}

func (a *App) persistRuntimeState(clearLastSyncError bool) error {
	state, err := a.runtimeStateForPersistence()
	if err != nil {
		return err
	}
	state.Metadata = ensureMetadata(state.Metadata)
	setApplyMetadata(state.Metadata, a.runtime.ActiveSnapshot().Revision, "success", "")
	activeConfig := a.runtime.ActiveSnapshot().AgentConfig
	if err := setTrafficStatsIntervalMetadata(state.Metadata, activeConfig.TrafficStatsInterval); err != nil {
		return err
	}
	setTrafficBlockedMetadata(state.Metadata, activeConfig)
	if clearLastSyncError {
		delete(state.Metadata, "last_sync_error")
	}
	if err := a.store.SaveRuntimeState(state); err != nil {
		return err
	}
	return nil
}

func setTrafficBlockedMetadata(meta map[string]string, cfg model.AgentConfig) {
	if cfg.TrafficBlocked {
		meta[runtimeMetaTrafficBlocked] = "true"
	} else {
		meta[runtimeMetaTrafficBlocked] = "false"
	}
	if strings.TrimSpace(cfg.TrafficBlockReason) == "" {
		delete(meta, runtimeMetaTrafficBlockReason)
		return
	}
	meta[runtimeMetaTrafficBlockReason] = cfg.TrafficBlockReason
}

func (a *App) recordPersistedRuntimeError(syncErr error) error {
	state, err := a.store.LoadRuntimeState()
	if err != nil {
		return syncErr
	}
	currentRevision := parseInt64(state.Metadata["current_revision"], state.CurrentRevision)
	return a.recordPersistedRuntimeErrorWithRevision(syncErr, currentRevision)
}

func (a *App) recordPersistedRuntimeErrorWithRevision(syncErr error, revision int64) error {
	state, err := a.store.LoadRuntimeState()
	if err != nil {
		return syncErr
	}
	state.Metadata = ensureMetadata(state.Metadata)
	state.Metadata["last_sync_error"] = syncErr.Error()
	setApplyMetadata(state.Metadata, revision, "error", syncErr.Error())
	if err := a.store.SaveRuntimeState(state); err != nil {
		return syncErr
	}
	return syncErr
}

func ensureMetadata(meta map[string]string) map[string]string {
	if meta == nil {
		return make(map[string]string)
	}
	return meta
}

func setApplyMetadata(meta map[string]string, revision int64, status string, message string) {
	meta["last_apply_revision"] = strconv.FormatInt(revision, 10)
	meta["last_apply_status"] = status
	meta["last_apply_message"] = message
}

func parseInt64(raw string, fallback int64) int64 {
	value, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil {
		return fallback
	}
	return value
}

func (a *App) runtimeStateForPersistence() (store.RuntimeState, error) {
	existing, err := a.store.LoadRuntimeState()
	if err != nil {
		return store.RuntimeState{}, err
	}

	current := a.runtime.State()
	state := existing
	state.Status = current.Status
	state.CurrentRevision = current.CurrentRevision
	state.Metadata = ensureMetadata(existing.Metadata)
	for key, value := range current.Metadata {
		state.Metadata[key] = value
	}
	return state, nil
}
