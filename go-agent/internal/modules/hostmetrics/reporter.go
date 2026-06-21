package hostmetrics

import (
	"context"
	"log"
	"math"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/core"
	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/mem"
	"github.com/shirou/gopsutil/v4/net"
)

type cpuPercentFunc func(context.Context, time.Duration, bool) ([]float64, error)
type memoryFunc func(context.Context) (*mem.VirtualMemoryStat, error)
type diskFunc func(context.Context, string) (*disk.UsageStat, error)
type netFunc func(context.Context, bool) ([]net.IOCountersStat, error)

type ReporterConfig struct {
	CPUPercent cpuPercentFunc
	Memory     memoryFunc
	DiskUsage  diskFunc
	NetIO      netFunc
	Logf       func(string, ...any)
}

type Reporter struct {
	cpuPercent cpuPercentFunc
	memory     memoryFunc
	diskUsage  diskFunc
	netIO      netFunc
	logf       func(string, ...any)
}

func NewReporter(cfg ReporterConfig) *Reporter {
	r := &Reporter{
		cpuPercent: cfg.CPUPercent,
		memory:     cfg.Memory,
		diskUsage:  cfg.DiskUsage,
		netIO:      cfg.NetIO,
		logf:       cfg.Logf,
	}
	if r.cpuPercent == nil {
		r.cpuPercent = cpu.PercentWithContext
	}
	if r.memory == nil {
		r.memory = mem.VirtualMemoryWithContext
	}
	if r.diskUsage == nil {
		r.diskUsage = disk.UsageWithContext
	}
	if r.netIO == nil {
		r.netIO = net.IOCountersWithContext
	}
	if r.logf == nil {
		r.logf = log.Printf
	}
	return r
}

func (r *Reporter) HostMetricsReport(ctx context.Context) (core.HostMetricsReport, error) {
	if r == nil {
		return core.HostMetricsReport{}, nil
	}
	stats := map[string]any{}
	if usage, ok := r.cpuUsage(ctx); ok {
		stats["cpu"] = map[string]any{"usage_percent": usage}
	}
	if usage, ok := r.memoryUsage(ctx); ok {
		stats["memory"] = map[string]any{"usage_percent": usage}
	}
	if usage, ok := r.diskUsagePercent(ctx); ok {
		stats["disk"] = map[string]any{"usage_percent": usage}
	}
	if total := r.networkCounters(ctx); total != nil {
		stats["network"] = map[string]any{"total": total}
	}
	if len(stats) == 0 {
		return core.HostMetricsReport{}, nil
	}
	return core.HostMetricsReport{Stats: map[string]any{"host": stats}, StatsPresent: true}, nil
}

func (r *Reporter) cpuUsage(ctx context.Context) (float64, bool) {
	values, err := r.cpuPercent(ctx, 0, false)
	if err != nil {
		r.logf("[agent] host metrics cpu snapshot error: %v", err)
		return 0, false
	}
	if len(values) == 0 {
		return 0, false
	}
	return normalizePercent(values[0])
}

func (r *Reporter) memoryUsage(ctx context.Context) (float64, bool) {
	stat, err := r.memory(ctx)
	if err != nil {
		r.logf("[agent] host metrics memory snapshot error: %v", err)
		return 0, false
	}
	if stat == nil {
		return 0, false
	}
	return normalizePercent(stat.UsedPercent)
}

func (r *Reporter) diskUsagePercent(ctx context.Context) (float64, bool) {
	stat, err := r.diskUsage(ctx, "/")
	if err != nil {
		r.logf("[agent] host metrics disk snapshot error: %v", err)
		return 0, false
	}
	if stat == nil {
		return 0, false
	}
	return normalizePercent(stat.UsedPercent)
}

func (r *Reporter) networkCounters(ctx context.Context) map[string]uint64 {
	counters, err := r.netIO(ctx, false)
	if err != nil {
		r.logf("[agent] host metrics network snapshot error: %v", err)
		return nil
	}
	if len(counters) == 0 {
		return nil
	}
	total := map[string]uint64{}
	for _, c := range counters {
		total["rx_bytes"] += c.BytesRecv
		total["tx_bytes"] += c.BytesSent
	}
	return total
}

func normalizePercent(value float64) (float64, bool) {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, false
	}
	if value < 0 {
		value = 0
	}
	if value > 100 {
		value = 100
	}
	return value, true
}

var _ core.HostMetricsReporter = (*Reporter)(nil)
