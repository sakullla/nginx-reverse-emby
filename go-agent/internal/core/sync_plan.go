package core

import (
	"context"
	"math"
	"strings"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/control"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

func (c *SyncController) BuildSyncRequest(ctx context.Context, applied model.Snapshot) (control.SyncRequest, error) {
	plan, err := c.BuildSyncPlan(ctx, applied)
	if err != nil {
		return control.SyncRequest{}, err
	}
	return plan.Request, nil
}

func (c *SyncController) BuildSyncPlan(ctx context.Context, applied model.Snapshot) (SyncPlan, error) {
	plan := SyncPlan{Request: control.SyncRequest{CurrentRevision: boundedInt64Revision(applied.Revision)}}

	state, err := c.Store.LoadRuntimeState()
	if err != nil {
		return SyncPlan{}, err
	}
	meta := ensureMetadata(state.Metadata)
	plan.Request.LastApplyRevision = boundedInt64Revision(parseInt64Default(meta["last_apply_revision"], applied.Revision))
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

	if c.HostMetrics != nil {
		report, err := c.HostMetrics.HostMetricsReport(ctx)
		if err != nil {
			return SyncPlan{}, err
		}
		if report.StatsPresent || report.Stats != nil {
			plan.Request.Stats = mergeStats(plan.Request.Stats, report.Stats)
			plan.Request.StatsPresent = plan.Request.StatsPresent || report.StatsPresent
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

func mergeStats(base, extra map[string]any) map[string]any {
	if extra == nil {
		return base
	}
	if base == nil {
		base = map[string]any{}
	}
	for key, value := range extra {
		base[key] = value
	}
	return base
}

func boundedInt64Revision(value int64) int {
	if value <= 0 {
		return 0
	}
	if value > int64(math.MaxInt) {
		return math.MaxInt
	}
	return int(value)
}
