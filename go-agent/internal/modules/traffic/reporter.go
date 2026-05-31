package traffic

import (
	"context"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/core"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/traffic/hosttraffic"
)

const (
	runtimeMetaTrafficStatsInterval       = "traffic_stats_interval"
	runtimeMetaLastTrafficStatsReportUnix = "last_traffic_stats_report_unix"
)

type HostSnapshotter interface {
	Snapshot() (hosttraffic.Snapshot, error)
}

type ReporterConfig struct {
	Enabled         func() bool
	SnapshotNonZero func() map[string]any
	HostSnapshotter HostSnapshotter
	Now             func() time.Time
	Logf            func(string, ...any)
}

type Reporter struct {
	enabled         func() bool
	snapshotNonZero func() map[string]any
	hostSnapshotter HostSnapshotter
	now             func() time.Time
	logf            func(string, ...any)
}

func NewReporter(cfg ReporterConfig) *Reporter {
	reporter := &Reporter{
		enabled:         cfg.Enabled,
		snapshotNonZero: cfg.SnapshotNonZero,
		hostSnapshotter: cfg.HostSnapshotter,
		now:             cfg.Now,
		logf:            cfg.Logf,
	}
	if reporter.enabled == nil {
		reporter.enabled = Enabled
	}
	if reporter.snapshotNonZero == nil {
		reporter.snapshotNonZero = SnapshotNonZero
	}
	if reporter.now == nil {
		reporter.now = time.Now
	}
	if reporter.logf == nil {
		reporter.logf = log.Printf
	}
	return reporter
}

func (r *Reporter) TrafficReport(_ context.Context, meta map[string]string) (core.TrafficReport, error) {
	if r == nil {
		return core.TrafficReport{}, nil
	}
	if !r.enabled() {
		return core.TrafficReport{Stats: map[string]any{}, StatsPresent: true}, nil
	}
	now := r.now()
	if !ShouldReportTrafficStats(meta, now) {
		return core.TrafficReport{}, nil
	}

	stats := r.snapshotNonZero()
	if hostStats, err := r.hostTrafficSnapshot(); err != nil {
		r.logf("[agent] host traffic snapshot error: %v", err)
	} else if hostStats != nil {
		stats = MergeTrafficStats(stats, hostStats)
	}
	if stats == nil {
		return core.TrafficReport{}, nil
	}

	report := core.TrafficReport{Stats: stats, StatsPresent: true}
	if HasTrafficStatsInterval(meta) {
		report.RuntimeMetadata = map[string]string{
			runtimeMetaLastTrafficStatsReportUnix: strconv.FormatInt(now.Unix(), 10),
		}
	}
	return report, nil
}

func (r *Reporter) hostTrafficSnapshot() (map[string]any, error) {
	if r.hostSnapshotter == nil {
		return nil, nil
	}
	snapshot, err := r.hostSnapshotter.Snapshot()
	if err != nil {
		return nil, err
	}
	return HostTrafficPayload(snapshot), nil
}

func HostTrafficPayload(snapshot hosttraffic.Snapshot) map[string]any {
	if snapshot.Total.RXBytes == 0 && snapshot.Total.TXBytes == 0 && len(snapshot.Interfaces) == 0 {
		return nil
	}
	return snapshot.Payload()
}

func MergeTrafficStats(base, extra map[string]any) map[string]any {
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

func ShouldReportTrafficStats(meta map[string]string, now time.Time) bool {
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

func HasTrafficStatsInterval(meta map[string]string) bool {
	interval, err := time.ParseDuration(strings.TrimSpace(meta[runtimeMetaTrafficStatsInterval]))
	return err == nil && interval > 0
}
