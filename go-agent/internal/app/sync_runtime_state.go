package app

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/core"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	moduletraffic "github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/traffic"
)

const (
	runtimeMetaTrafficStatsInterval       = "traffic_stats_interval"
	runtimeMetaLastTrafficStatsReportUnix = "last_traffic_stats_report_unix"
	runtimeMetaTrafficBlocked             = "traffic_blocked"
	runtimeMetaTrafficBlockReason         = "traffic_block_reason"
)

func (a *App) syncRequest(ctx context.Context, applied Snapshot) (SyncRequest, error) {
	plan, err := a.syncController().BuildSyncPlan(ctx, applied)
	if err != nil {
		return SyncRequest{}, err
	}
	a.syncMu.Lock()
	a.pendingSyncMetadata = copyStringMap(plan.RuntimeMetadata)
	a.syncMu.Unlock()
	return plan.Request, nil
}

func (a *App) syncOnce(ctx context.Context, req SyncRequest) error {
	a.syncMu.Lock()
	metadata := copyStringMap(a.pendingSyncMetadata)
	a.pendingSyncMetadata = nil
	defer a.syncMu.Unlock()
	return a.syncController().PerformSyncPlan(ctx, core.SyncPlan{Request: req, RuntimeMetadata: metadata})
}

func (a *App) syncController() *core.SyncController {
	controller := &core.SyncController{
		Store:                a.store,
		Runtime:              a.runtime,
		SyncClient:           a.syncClient,
		Updater:              a.updater,
		Traffic:              a.trafficReporter(),
		CurrentPackageSHA256: a.cfg.RuntimePackageSHA256,
	}
	controller.CertReports = a.certReports
	return controller
}

func (a *App) trafficReporter() core.TrafficReporter {
	if a == nil || a.trafficReports == nil {
		return moduletraffic.NewReporter(moduletraffic.ReporterConfig{})
	}
	return a.trafficReports
}

func mergeTrafficStats(base, extra map[string]any) map[string]any {
	return moduletraffic.MergeTrafficStats(base, extra)
}

func shouldReportTrafficStats(meta map[string]string, now time.Time) bool {
	return moduletraffic.ShouldReportTrafficStats(meta, now)
}

func hasTrafficStatsInterval(meta map[string]string) bool {
	return moduletraffic.HasTrafficStatsInterval(meta)
}

func (a *App) persistTrafficStatsInterval(raw string) error {
	return a.syncController().PersistTrafficStatsInterval(raw)
}

func parseTrafficStatsInterval(raw string) (string, error) {
	return core.ParseTrafficStatsInterval(raw)
}

func setTrafficStatsIntervalMetadata(meta map[string]string, raw string) error {
	return core.SetTrafficStatsIntervalMetadata(meta, raw)
}

func setTrafficBlockedMetadata(meta map[string]string, cfg model.AgentConfig) {
	core.SetTrafficBlockedMetadata(meta, cfg)
}

func (a *App) recordRuntimeErrorWithRevision(syncErr error, revision int64) error {
	return a.syncController().RecordRuntimeErrorWithRevision(syncErr, revision)
}

func ensureMetadata(meta map[string]string) map[string]string {
	if meta == nil {
		return make(map[string]string)
	}
	return meta
}

func copyStringMap(src map[string]string) map[string]string {
	if src == nil {
		return nil
	}
	dst := make(map[string]string, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}

func parseInt64(raw string, fallback int64) int64 {
	value, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil {
		return fallback
	}
	return value
}
