package core

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

const (
	runtimeMetaTrafficStatsInterval       = "traffic_stats_interval"
	runtimeMetaTrafficStatsEnabled        = "traffic_stats_enabled"
	runtimeMetaLastTrafficStatsReportUnix = "last_traffic_stats_report_unix"
	runtimeMetaTrafficBlocked             = "traffic_blocked"
	runtimeMetaTrafficBlockReason         = "traffic_block_reason"
)

func (c *SyncController) RecordRuntimeErrorWithRevision(syncErr error, revision int64) error {
	return c.recordRuntimeErrorWithRevision(syncErr, revision)
}

func (c *SyncController) PersistTrafficStatsInterval(raw string) error {
	state, err := c.Store.LoadRuntimeState()
	if err != nil {
		return err
	}
	state.Metadata = ensureMetadata(state.Metadata)
	if err := SetTrafficStatsIntervalMetadata(state.Metadata, raw); err != nil {
		return err
	}
	return c.Store.SaveRuntimeState(state)
}

func ParseTrafficStatsInterval(raw string) (string, error) {
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

func SetTrafficStatsIntervalMetadata(meta map[string]string, raw string) error {
	interval, err := ParseTrafficStatsInterval(raw)
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

func SetTrafficBlockedMetadata(meta map[string]string, cfg model.AgentConfig) {
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

func SetTrafficRuntimeMetadata(meta map[string]string, cfg model.AgentConfig) {
	if cfg.TrafficStatsEnabled != nil {
		meta[runtimeMetaTrafficStatsEnabled] = strconv.FormatBool(*cfg.TrafficStatsEnabled)
	}
	SetTrafficBlockedMetadata(meta, cfg)
}

func trafficRuntimeConfigFromMetadata(meta map[string]string, legacyConfig model.AgentConfig) (model.AgentConfig, bool, error) {
	enabledText, hasEnabled := meta[runtimeMetaTrafficStatsEnabled]
	blockedText, hasBlocked := meta[runtimeMetaTrafficBlocked]
	reason, hasReason := meta[runtimeMetaTrafficBlockReason]
	if !hasEnabled && !hasBlocked && !hasReason {
		return model.AgentConfig{}, false, nil
	}
	enabled := false
	var err error
	if hasEnabled {
		enabled, err = strconv.ParseBool(strings.TrimSpace(enabledText))
		if err != nil {
			return model.AgentConfig{}, false, fmt.Errorf("traffic_stats_enabled metadata: %w", err)
		}
	} else if legacyConfig.TrafficStatsEnabled != nil {
		// Runtime metadata written before traffic_stats_enabled was introduced
		// only proves block state. Recover enabled from the immutable applied
		// artifact that originally configured that runtime instead of guessing.
		enabled = *legacyConfig.TrafficStatsEnabled
	}
	blocked := false
	if hasBlocked {
		blocked, err = strconv.ParseBool(strings.TrimSpace(blockedText))
		if err != nil {
			return model.AgentConfig{}, false, fmt.Errorf("traffic_blocked metadata: %w", err)
		}
	}
	// When neither metadata nor the old artifact proves enabled, false is the
	// only safe upgrade value. This prevents module preparation from falling
	// back to a process-global true default while heartbeat remains unavailable.
	return model.AgentConfig{
		TrafficStatsEnabled: &enabled,
		TrafficBlocked:      blocked,
		TrafficBlockReason:  strings.TrimSpace(reason),
	}, true, nil
}

func (c *SyncController) recordRuntimeError(syncErr error) error {
	return c.recordRuntimeErrorWithRevision(syncErr, c.Runtime.ActiveSnapshot().Revision)
}

func (c *SyncController) recordSyncError(syncErr error) error {
	state, err := c.runtimeStateForPersistence()
	if err != nil {
		return syncErr
	}
	state.Metadata = ensureMetadata(state.Metadata)
	state.Metadata["last_sync_error"] = syncErr.Error()
	if err := c.Store.SaveRuntimeState(state); err != nil {
		return syncErr
	}
	return syncErr
}

func (c *SyncController) clearLastSyncErrorAfterSuccessfulSync() error {
	state, err := c.runtimeStateForPersistence()
	if err != nil {
		return err
	}
	state.Metadata = ensureMetadata(state.Metadata)
	lastSyncError := strings.TrimSpace(state.Metadata["last_sync_error"])
	if lastSyncError == "" {
		if !hasLegacyHeartbeatApplyError(state.Metadata) {
			return nil
		}
		lastSyncError = strings.TrimSpace(state.Metadata["last_apply_message"])
	}
	delete(state.Metadata, "last_sync_error")
	if isRecoverableSyncApplyError(state.Metadata, lastSyncError) {
		setApplyMetadata(state.Metadata, c.Runtime.ActiveSnapshot().Revision, "success", "")
	}
	return c.Store.SaveRuntimeState(state)
}

func isRecoverableSyncApplyError(metadata map[string]string, lastSyncError string) bool {
	normalizedError := strings.ToLower(strings.TrimSpace(lastSyncError))
	restartRequested := strings.ToLower(ErrRestartRequested.Error())
	recovered := isLegacyHeartbeatSyncError(normalizedError) ||
		strings.HasPrefix(normalizedError, "durable generation is not ready for hot restart") ||
		strings.HasPrefix(normalizedError, "open current executable:") ||
		normalizedError == restartRequested ||
		strings.HasSuffix(normalizedError, "\n"+restartRequested) ||
		strings.HasSuffix(normalizedError, ": "+restartRequested)
	return recovered &&
		strings.EqualFold(strings.TrimSpace(metadata["last_apply_status"]), "error") &&
		strings.TrimSpace(metadata["last_apply_message"]) == lastSyncError
}

func isLegacyHeartbeatSyncError(message string) bool {
	normalized := strings.ToLower(strings.TrimSpace(message))
	return strings.HasPrefix(normalized, "heartbeat failed:") || isLegacyHeartbeatTransportError(normalized)
}

func hasLegacyHeartbeatApplyError(metadata map[string]string) bool {
	return strings.EqualFold(strings.TrimSpace(metadata["last_apply_status"]), "error") &&
		isLegacyHeartbeatSyncError(metadata["last_apply_message"])
}

func isLegacyHeartbeatTransportError(message string) bool {
	const methodPrefix = `post "`
	if !strings.HasPrefix(message, methodPrefix) {
		return false
	}
	remainder := strings.TrimPrefix(message, methodPrefix)
	quote := strings.IndexByte(remainder, '"')
	if quote < 0 || !strings.HasPrefix(remainder[quote+1:], ":") {
		return false
	}
	endpoint, err := url.Parse(remainder[:quote])
	if err != nil {
		return false
	}
	return strings.HasSuffix(strings.TrimRight(endpoint.Path, "/"), "/api/agents/heartbeat")
}

func (c *SyncController) recordRuntimeErrorWithRevision(syncErr error, revision int64) error {
	return c.recordRuntimeErrorFromState(syncErr, revision, c.runtimeStateForPersistence)
}

func (c *SyncController) recordPersistedRuntimeErrorWithRevision(syncErr error, revision int64) error {
	return c.recordRuntimeErrorFromState(syncErr, revision, c.Store.LoadRuntimeState)
}

func (c *SyncController) recordRuntimeErrorFromState(syncErr error, revision int64, load func() (RuntimeState, error)) error {
	state, err := load()
	if err != nil {
		return syncErr
	}
	state.Metadata = ensureMetadata(state.Metadata)
	state.Metadata["last_sync_error"] = syncErr.Error()
	setApplyMetadata(state.Metadata, revision, "error", syncErr.Error())
	if err := c.Store.SaveRuntimeState(state); err != nil {
		return syncErr
	}
	return syncErr
}

func (c *SyncController) persistRuntimeMetadata(metadata map[string]string) error {
	state, err := c.Store.LoadRuntimeState()
	if err != nil {
		return err
	}
	state.Metadata = ensureMetadata(state.Metadata)
	for key, value := range metadata {
		state.Metadata[key] = value
	}
	return c.Store.SaveRuntimeState(state)
}

func (c *SyncController) persistRuntimeState(clearLastSyncError bool) error {
	state, err := c.runtimeStateForPersistence()
	if err != nil {
		return err
	}
	state.Metadata = ensureMetadata(state.Metadata)
	setApplyMetadata(state.Metadata, c.Runtime.ActiveSnapshot().Revision, "success", "")
	activeConfig := c.Runtime.ActiveSnapshot().AgentConfig
	if err := SetTrafficStatsIntervalMetadata(state.Metadata, activeConfig.TrafficStatsInterval); err != nil {
		return err
	}
	SetTrafficRuntimeMetadata(state.Metadata, activeConfig)
	if clearLastSyncError {
		delete(state.Metadata, "last_sync_error")
	}
	return c.Store.SaveRuntimeState(state)
}

func (c *SyncController) runtimeStateForPersistence() (RuntimeState, error) {
	existing, err := c.Store.LoadRuntimeState()
	if err != nil {
		return RuntimeState{}, err
	}

	current := c.Runtime.State()
	state := existing
	state.Status = current.Status
	state.CurrentRevision = current.CurrentRevision
	state.Metadata = ensureMetadata(existing.Metadata)
	for key, value := range current.Metadata {
		state.Metadata[key] = value
	}
	return state, nil
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

func cloneStringMap(src map[string]string) map[string]string {
	if src == nil {
		return nil
	}
	dst := make(map[string]string, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}

func parseInt64Default(raw string, fallback int64) int64 {
	value, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil {
		return fallback
	}
	return value
}
