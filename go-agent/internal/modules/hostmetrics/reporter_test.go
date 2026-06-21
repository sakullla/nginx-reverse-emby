package hostmetrics

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/mem"
	"github.com/shirou/gopsutil/v4/net"
)

func TestReporterBuildsHostMetricsPayload(t *testing.T) {
	reporter := NewReporter(ReporterConfig{
		CPUPercent: func(context.Context, time.Duration, bool) ([]float64, error) {
			return []float64{12.5}, nil
		},
		Memory: func(context.Context) (*mem.VirtualMemoryStat, error) {
			return &mem.VirtualMemoryStat{UsedPercent: 64.25}, nil
		},
		DiskUsage: func(context.Context, string) (*disk.UsageStat, error) {
			return &disk.UsageStat{UsedPercent: 77.75}, nil
		},
		NetIO: func(context.Context, bool) ([]net.IOCountersStat, error) {
			return []net.IOCountersStat{{BytesRecv: 100, BytesSent: 200}, {BytesRecv: 3, BytesSent: 4}}, nil
		},
		Logf: func(string, ...any) {},
	})

	report, err := reporter.HostMetricsReport(context.Background())
	if err != nil {
		t.Fatalf("HostMetricsReport() error = %v", err)
	}
	if !report.StatsPresent {
		t.Fatal("StatsPresent = false, want true")
	}
	host := report.Stats["host"].(map[string]any)
	if got := host["cpu"].(map[string]any)["usage_percent"]; got != 12.5 {
		t.Fatalf("cpu usage = %v, want 12.5", got)
	}
	if got := host["memory"].(map[string]any)["usage_percent"]; got != 64.25 {
		t.Fatalf("memory usage = %v, want 64.25", got)
	}
	if got := host["disk"].(map[string]any)["usage_percent"]; got != 77.75 {
		t.Fatalf("disk usage = %v, want 77.75", got)
	}
	total := host["network"].(map[string]any)["total"].(map[string]uint64)
	if total["rx_bytes"] != 103 || total["tx_bytes"] != 204 {
		t.Fatalf("network total = %+v, want rx=103 tx=204", total)
	}
}

func TestReporterOmitsUnavailableMetrics(t *testing.T) {
	reporter := NewReporter(ReporterConfig{
		CPUPercent: func(context.Context, time.Duration, bool) ([]float64, error) {
			return nil, errors.New("cpu unavailable")
		},
		Memory: func(context.Context) (*mem.VirtualMemoryStat, error) {
			return &mem.VirtualMemoryStat{UsedPercent: 33}, nil
		},
		DiskUsage: func(context.Context, string) (*disk.UsageStat, error) {
			return nil, errors.New("disk unavailable")
		},
		NetIO: func(context.Context, bool) ([]net.IOCountersStat, error) {
			return nil, nil
		},
		Logf: func(string, ...any) {},
	})

	report, err := reporter.HostMetricsReport(context.Background())
	if err != nil {
		t.Fatalf("HostMetricsReport() error = %v", err)
	}
	host := report.Stats["host"].(map[string]any)
	if _, ok := host["cpu"]; ok {
		t.Fatalf("cpu metric present for unavailable cpu: %+v", host)
	}
	if _, ok := host["disk"]; ok {
		t.Fatalf("disk metric present for unavailable disk: %+v", host)
	}
	if _, ok := host["network"]; ok {
		t.Fatalf("network metric present for empty counters: %+v", host)
	}
	if got := host["memory"].(map[string]any)["usage_percent"]; got != float64(33) {
		t.Fatalf("memory usage = %v, want 33", got)
	}
}

func TestReporterKeepsAvailableZeroNetworkCounters(t *testing.T) {
	reporter := NewReporter(ReporterConfig{
		CPUPercent: func(context.Context, time.Duration, bool) ([]float64, error) {
			return nil, nil
		},
		Memory: func(context.Context) (*mem.VirtualMemoryStat, error) {
			return nil, nil
		},
		DiskUsage: func(context.Context, string) (*disk.UsageStat, error) {
			return nil, nil
		},
		NetIO: func(context.Context, bool) ([]net.IOCountersStat, error) {
			return []net.IOCountersStat{{Name: "eth0"}}, nil
		},
		Logf: func(string, ...any) {},
	})

	report, err := reporter.HostMetricsReport(context.Background())
	if err != nil {
		t.Fatalf("HostMetricsReport() error = %v", err)
	}
	if !report.StatsPresent {
		t.Fatal("StatsPresent = false, want true")
	}
	host := report.Stats["host"].(map[string]any)
	total := host["network"].(map[string]any)["total"].(map[string]uint64)
	if total["rx_bytes"] != 0 || total["tx_bytes"] != 0 {
		t.Fatalf("network total = %+v, want rx=0 tx=0", total)
	}
}

func TestNormalizePercentClampsInvalidValues(t *testing.T) {
	if got, ok := normalizePercent(-1); !ok || got != 0 {
		t.Fatalf("normalizePercent(-1) = %v %v, want 0 true", got, ok)
	}
	if got, ok := normalizePercent(101); !ok || got != 100 {
		t.Fatalf("normalizePercent(101) = %v %v, want 100 true", got, ok)
	}
}
