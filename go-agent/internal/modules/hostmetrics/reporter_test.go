//go:build !integration

package hostmetrics

import (
	"context"
	"errors"
	"math"
	"testing"
)

type reporterFixture struct {
	cpu        float64
	cores      int
	cpuErr     error
	coresErr   error
	memory     *memorySnapshot
	memoryErr  error
	disk       *diskSnapshot
	diskErr    error
	network    []networkCounter
	networkErr error
}

func (fixture reporterFixture) config() ReporterConfig {
	return ReporterConfig{
		CPUPercent: func(context.Context) (float64, error) { return fixture.cpu, fixture.cpuErr },
		CPUCounts:  func(context.Context) (int, error) { return fixture.cores, fixture.coresErr },
		Memory:     func(context.Context) (*memorySnapshot, error) { return fixture.memory, fixture.memoryErr },
		DiskUsage:  func(context.Context, string) (*diskSnapshot, error) { return fixture.disk, fixture.diskErr },
		NetIO:      func(context.Context) ([]networkCounter, error) { return fixture.network, fixture.networkErr },
		Logf:       func(string, ...any) {},
	}
}

func TestReporterPayloadAvailabilityAndBounds(t *testing.T) {
	unavailable := errors.New("collector unavailable")
	tests := []struct {
		name    string
		fixture reporterFixture
		check   func(*testing.T, map[string]any)
	}{
		{
			name: "full payload",
			fixture: reporterFixture{
				cpu: 12.5, cores: 8,
				memory:  &memorySnapshot{Total: 16 << 30, Used: 10 << 30, UsedPercent: 64.25},
				disk:    &diskSnapshot{Total: 512 << 30, Used: 398 << 30, UsedPercent: 77.75},
				network: []networkCounter{{BytesRecv: 100, BytesSent: 200}, {BytesRecv: 3, BytesSent: 4}},
			},
			check: func(t *testing.T, host map[string]any) {
				cpu := host["cpu"].(map[string]any)
				if cpu["usage_percent"] != 12.5 || cpu["total_cores"] != 8 || cpu["used_cores"] != 1.0 {
					t.Fatalf("cpu payload = %#v", cpu)
				}
				if memory := host["memory"].(map[string]any); memory["usage_percent"] != 64.25 || memory["used_bytes"] != uint64(10<<30) {
					t.Fatalf("memory payload = %#v", memory)
				}
				if disk := host["disk"].(map[string]any); disk["usage_percent"] != 77.75 || disk["total_bytes"] != uint64(512<<30) {
					t.Fatalf("disk payload = %#v", disk)
				}
				total := host["network"].(map[string]any)["total"].(map[string]uint64)
				if total["rx_bytes"] != 103 || total["tx_bytes"] != 204 {
					t.Fatalf("network payload = %#v", total)
				}
			},
		},
		{
			name: "partial unavailable",
			fixture: reporterFixture{
				cpuErr: unavailable, coresErr: unavailable,
				memory: &memorySnapshot{UsedPercent: 33}, diskErr: unavailable,
			},
			check: func(t *testing.T, host map[string]any) {
				if _, ok := host["cpu"]; ok {
					t.Fatalf("unavailable cpu present: %#v", host)
				}
				if _, ok := host["disk"]; ok {
					t.Fatalf("unavailable disk present: %#v", host)
				}
				if _, ok := host["network"]; ok {
					t.Fatalf("empty network present: %#v", host)
				}
				if host["memory"].(map[string]any)["usage_percent"] != float64(33) {
					t.Fatalf("memory payload = %#v", host["memory"])
				}
			},
		},
		{
			name: "available zero and invalid percent",
			fixture: reporterFixture{
				cpu: math.NaN(), cores: 4,
				memory:  &memorySnapshot{UsedPercent: -1},
				disk:    &diskSnapshot{UsedPercent: 101},
				network: []networkCounter{{Name: "eth0"}},
			},
			check: func(t *testing.T, host map[string]any) {
				cpu := host["cpu"].(map[string]any)
				if _, ok := cpu["usage_percent"]; ok || cpu["total_cores"] != 4 {
					t.Fatalf("bounded cpu payload = %#v", cpu)
				}
				if host["memory"].(map[string]any)["usage_percent"] != float64(0) {
					t.Fatalf("memory bounds = %#v", host["memory"])
				}
				if host["disk"].(map[string]any)["usage_percent"] != float64(100) {
					t.Fatalf("disk bounds = %#v", host["disk"])
				}
				total := host["network"].(map[string]any)["total"].(map[string]uint64)
				if total["rx_bytes"] != 0 || total["tx_bytes"] != 0 {
					t.Fatalf("zero network = %#v", total)
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			report, err := NewReporter(test.fixture.config()).HostMetricsReport(t.Context())
			if err != nil || !report.StatsPresent {
				t.Fatalf("HostMetricsReport() = %#v, %v", report, err)
			}
			test.check(t, report.Stats["host"].(map[string]any))
		})
	}
}
