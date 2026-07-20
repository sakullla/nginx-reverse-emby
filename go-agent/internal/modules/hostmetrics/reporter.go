package hostmetrics

import (
	"context"
	"log"
	"math"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/core"
)

type cpuPercentFunc func(context.Context) (float64, error)
type cpuCountsFunc func(context.Context) (int, error)
type memoryFunc func(context.Context) (*memorySnapshot, error)
type diskFunc func(context.Context, string) (*diskSnapshot, error)
type netFunc func(context.Context) ([]networkCounter, error)

type ReporterConfig struct {
	CPUPercent cpuPercentFunc
	CPUCounts  cpuCountsFunc
	Memory     memoryFunc
	DiskUsage  diskFunc
	NetIO      netFunc
	Logf       func(string, ...any)
}

type Reporter struct {
	cpuPercent cpuPercentFunc
	cpuCounts  cpuCountsFunc
	memory     memoryFunc
	diskUsage  diskFunc
	netIO      netFunc
	logf       func(string, ...any)
}

func NewReporter(cfg ReporterConfig) *Reporter {
	r := &Reporter{
		cpuPercent: cfg.CPUPercent,
		cpuCounts:  cfg.CPUCounts,
		memory:     cfg.Memory,
		diskUsage:  cfg.DiskUsage,
		netIO:      cfg.NetIO,
		logf:       cfg.Logf,
	}
	if r.cpuPercent == nil {
		r.cpuPercent = newCPUPercentSampler(readCPUTimes)
	}
	if r.cpuCounts == nil {
		r.cpuCounts = logicalCPUCount
	}
	if r.memory == nil {
		r.memory = readMemory
	}
	if r.diskUsage == nil {
		r.diskUsage = readDiskUsage
	}
	if r.netIO == nil {
		r.netIO = readNetworkCounters
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
	if cpuStats := r.cpuStats(ctx); cpuStats != nil {
		stats["cpu"] = cpuStats
	}
	if memoryStats := r.memoryStats(ctx); memoryStats != nil {
		stats["memory"] = memoryStats
	}
	if diskStats := r.diskStats(ctx); diskStats != nil {
		stats["disk"] = diskStats
	}
	if total := r.networkCounters(ctx); total != nil {
		stats["network"] = map[string]any{"total": total}
	}
	if len(stats) == 0 {
		return core.HostMetricsReport{}, nil
	}
	return core.HostMetricsReport{Stats: map[string]any{"host": stats}, StatsPresent: true}, nil
}

func (r *Reporter) cpuStats(ctx context.Context) map[string]any {
	usage, usageOK := r.cpuUsage(ctx)
	totalCores, coresOK := r.cpuCoreCount(ctx)
	if !usageOK && !coresOK {
		return nil
	}
	stats := map[string]any{}
	if usageOK {
		stats["usage_percent"] = usage
	}
	if coresOK {
		stats["total_cores"] = totalCores
		if usageOK {
			stats["used_cores"] = usage / 100 * float64(totalCores)
		}
	}
	return stats
}

func (r *Reporter) cpuUsage(ctx context.Context) (float64, bool) {
	value, err := r.cpuPercent(ctx)
	if err != nil {
		r.logf("[agent] host metrics cpu snapshot error: %v", err)
		return 0, false
	}
	return normalizePercent(value)
}

func (r *Reporter) cpuCoreCount(ctx context.Context) (int, bool) {
	count, err := r.cpuCounts(ctx)
	if err != nil {
		r.logf("[agent] host metrics cpu count snapshot error: %v", err)
		return 0, false
	}
	if count <= 0 {
		return 0, false
	}
	return count, true
}

func (r *Reporter) memoryStats(ctx context.Context) map[string]any {
	stat, err := r.memory(ctx)
	if err != nil {
		r.logf("[agent] host metrics memory snapshot error: %v", err)
		return nil
	}
	if stat == nil {
		return nil
	}
	stats := map[string]any{
		"used_bytes":  stat.Used,
		"total_bytes": stat.Total,
	}
	if usage, ok := normalizePercent(stat.UsedPercent); ok {
		stats["usage_percent"] = usage
	}
	return stats
}

func (r *Reporter) diskStats(ctx context.Context) map[string]any {
	stat, err := r.diskUsage(ctx, "/")
	if err != nil {
		r.logf("[agent] host metrics disk snapshot error: %v", err)
		return nil
	}
	if stat == nil {
		return nil
	}
	stats := map[string]any{
		"used_bytes":  stat.Used,
		"total_bytes": stat.Total,
	}
	if usage, ok := normalizePercent(stat.UsedPercent); ok {
		stats["usage_percent"] = usage
	}
	return stats
}

func (r *Reporter) networkCounters(ctx context.Context) map[string]uint64 {
	counters, err := r.netIO(ctx)
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
