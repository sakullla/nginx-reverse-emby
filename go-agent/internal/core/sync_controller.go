package core

import (
	"context"
	"errors"
	"fmt"
	"log"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/control"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

const (
	runtimeMetaTrafficStatsInterval       = "traffic_stats_interval"
	runtimeMetaLastTrafficStatsReportUnix = "last_traffic_stats_report_unix"
	runtimeMetaTrafficBlocked             = "traffic_blocked"
	runtimeMetaTrafficBlockReason         = "traffic_block_reason"
)

type SyncClient interface {
	Sync(context.Context, control.SyncRequest) (model.Snapshot, error)
}

type Updater interface {
	Stage(context.Context, model.VersionPackage) (string, error)
	Activate(stagedPath string, desiredVersion string) error
}

type TrafficReporter interface {
	TrafficReport(context.Context, map[string]string) (TrafficReport, error)
}

type TrafficReport struct {
	Stats           map[string]any
	StatsPresent    bool
	RuntimeMetadata map[string]string
}

type SyncPlan struct {
	Request         control.SyncRequest
	RuntimeMetadata map[string]string
}

type ManagedCertificateReporter interface {
	ManagedCertificateReports(context.Context) ([]model.ManagedCertificateReport, error)
}

type SyncController struct {
	Store                Store
	Runtime              *Runtime
	SyncClient           SyncClient
	Updater              Updater
	Traffic              TrafficReporter
	CertReports          ManagedCertificateReporter
	CurrentPackageSHA256 string
}

func (c *SyncController) BuildSyncRequest(ctx context.Context, applied model.Snapshot) (control.SyncRequest, error) {
	plan, err := c.BuildSyncPlan(ctx, applied)
	if err != nil {
		return control.SyncRequest{}, err
	}
	return plan.Request, nil
}

func (c *SyncController) BuildSyncPlan(ctx context.Context, applied model.Snapshot) (SyncPlan, error) {
	plan := SyncPlan{Request: control.SyncRequest{CurrentRevision: int(applied.Revision)}}

	state, err := c.Store.LoadRuntimeState()
	if err != nil {
		return SyncPlan{}, err
	}
	meta := ensureMetadata(state.Metadata)
	plan.Request.LastApplyRevision = int(parseInt64Default(meta["last_apply_revision"], applied.Revision))
	plan.Request.LastApplyStatus = strings.TrimSpace(meta["last_apply_status"])
	plan.Request.LastApplyMessage = meta["last_apply_message"]
	if plan.Request.LastApplyStatus == "" {
		plan.Request.LastApplyStatus = "success"
	}

	if c.Traffic != nil {
		report, err := c.Traffic.TrafficReport(ctx, meta)
		if err != nil {
			return SyncPlan{}, err
		}
		if report.StatsPresent || report.Stats != nil {
			plan.Request.Stats = report.Stats
			plan.Request.StatsPresent = report.StatsPresent
		}
		if len(report.RuntimeMetadata) > 0 {
			plan.RuntimeMetadata = cloneStringMap(report.RuntimeMetadata)
		}
	}

	if c.CertReports != nil {
		reports, err := c.CertReports.ManagedCertificateReports(ctx)
		if err != nil {
			return SyncPlan{}, err
		}
		plan.Request.ManagedCertificateReports = reports
	}

	return plan, nil
}

func (c *SyncController) PerformSync(ctx context.Context, req control.SyncRequest) error {
	return c.PerformSyncPlan(ctx, SyncPlan{Request: req})
}

func (c *SyncController) PerformSyncPlan(ctx context.Context, plan SyncPlan) error {
	snapshot, err := c.SyncClient.Sync(ctx, plan.Request)
	if err != nil {
		log.Printf("[agent] sync error: %v", err)
		return c.recordRuntimeError(err)
	}
	if len(plan.RuntimeMetadata) > 0 {
		if err := c.persistRuntimeMetadata(plan.RuntimeMetadata); err != nil {
			return c.recordRuntimeError(err)
		}
	}
	existingDesired, err := c.Store.LoadDesiredSnapshot()
	if err != nil {
		return c.recordRuntimeError(err)
	}
	persistedSnapshot := MergeSnapshotPayload(snapshot, existingDesired)
	if err := c.Store.SaveDesiredSnapshot(persistedSnapshot); err != nil {
		return c.recordRuntimeError(err)
	}
	if err := c.handlePendingUpdate(ctx, persistedSnapshot); err != nil {
		return err
	}

	previousApplied := c.Runtime.ActiveSnapshot()
	candidateApplied := MergeSnapshotPayload(snapshot, previousApplied)
	if err := c.Runtime.Apply(ctx, previousApplied, candidateApplied); err != nil {
		log.Printf("[agent] runtime apply error at revision %d: %v", candidateApplied.Revision, err)
		rollbackErr := c.rollbackRuntime(ctx, candidateApplied, previousApplied)
		return c.recordRuntimeErrorWithRevision(errors.Join(err, rollbackErr), candidateApplied.Revision)
	}
	if err := c.Store.SaveAppliedSnapshot(candidateApplied); err != nil {
		log.Printf("[agent] save applied snapshot error at revision %d: %v", candidateApplied.Revision, err)
		rollbackErr := c.rollbackRuntime(ctx, candidateApplied, previousApplied)
		return c.recordPersistedRuntimeErrorWithRevision(errors.Join(err, rollbackErr), candidateApplied.Revision)
	}
	if err := c.persistRuntimeState(true); err != nil {
		rollbackErr := c.rollbackRuntime(ctx, candidateApplied, previousApplied)
		restoreErr := c.Store.SaveAppliedSnapshot(previousApplied)
		return c.recordPersistedRuntimeErrorWithRevision(errors.Join(err, rollbackErr, restoreErr), candidateApplied.Revision)
	}
	return nil
}

func (c *SyncController) HandlePendingUpdate(ctx context.Context, snapshot model.Snapshot) error {
	return c.handlePendingUpdate(ctx, snapshot)
}

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

func MergeSnapshotPayload(next, previous model.Snapshot) model.Snapshot {
	merged := next
	if next.VersionPackage == nil {
		merged.VersionPackage = previous.VersionPackage
	}
	if !next.HasAgentConfig() {
		merged.AgentConfig = previous.AgentConfig
	}
	if next.Rules == nil {
		merged.Rules = previous.Rules
	}
	if next.L4Rules == nil {
		merged.L4Rules = previous.L4Rules
	}
	if next.RelayListeners == nil {
		merged.RelayListeners = previous.RelayListeners
	}
	if next.WireGuardProfiles == nil {
		merged.WireGuardProfiles = previous.WireGuardProfiles
	}
	if next.EgressProfiles == nil {
		merged.EgressProfiles = previous.EgressProfiles
	}
	if next.Certificates == nil {
		merged.Certificates = previous.Certificates
	}
	if next.CertificatePolicies == nil {
		merged.CertificatePolicies = previous.CertificatePolicies
	}
	return merged
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

func (c *SyncController) handlePendingUpdate(ctx context.Context, snapshot model.Snapshot) error {
	if !HasValidPackage(snapshot.VersionPackage) {
		return nil
	}
	desiredSHA := strings.TrimSpace(snapshot.VersionPackage.SHA256)
	if desiredSHA == "" {
		return nil
	}
	currentSHA := strings.TrimSpace(c.CurrentPackageSHA256)
	if currentSHA != "" && strings.EqualFold(currentSHA, desiredSHA) {
		return nil
	}
	if c.Updater == nil {
		return c.recordRuntimeError(errors.New("updater unavailable"))
	}

	stagedPath, err := c.Updater.Stage(ctx, *snapshot.VersionPackage)
	if err != nil {
		return c.recordRuntimeError(err)
	}
	if err := c.Updater.Activate(stagedPath, snapshot.DesiredVersion); err != nil {
		if errors.Is(err, ErrRestartRequested) {
			return err
		}
		return c.recordRuntimeError(err)
	}
	return ErrRestartRequested
}

func (c *SyncController) rollbackRuntime(ctx context.Context, previousApplied, targetApplied model.Snapshot) error {
	if reflect.DeepEqual(previousApplied, targetApplied) {
		return nil
	}
	var errs []error
	if err := c.Runtime.Rollback(ctx, previousApplied, targetApplied); err != nil {
		errs = append(errs, fmt.Errorf("runtime rollback: %w", err))
	}
	return errors.Join(errs...)
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
