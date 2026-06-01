package core

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

const (
	runtimeMetaTrafficStatsInterval       = "traffic_stats_interval"
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

func (c *SyncController) recordRuntimeError(syncErr error) error {
	return c.recordRuntimeErrorWithRevision(syncErr, c.Runtime.ActiveSnapshot().Revision)
}

func (c *SyncController) recordRuntimeErrorWithRevision(syncErr error, revision int64) error {
	state, err := c.runtimeStateForPersistence()
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

func (c *SyncController) recordPersistedRuntimeErrorWithRevision(syncErr error, revision int64) error {
	state, err := c.Store.LoadRuntimeState()
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
	SetTrafficBlockedMetadata(state.Metadata, activeConfig)
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
