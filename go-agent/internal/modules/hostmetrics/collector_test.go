package hostmetrics

import (
	"context"
	"math"
	"strings"
	"testing"
)

func TestCPUPercentSamplerUsesCounterDeltas(t *testing.T) {
	snapshots := []cpuTimes{
		{Total: 100, Idle: 20},
		{Total: 180, Idle: 40},
	}
	next := 0
	percent := newCPUPercentSampler(func(context.Context) (cpuTimes, error) {
		snapshot := snapshots[next]
		next++
		return snapshot, nil
	})

	first, err := percent(context.Background())
	if err != nil {
		t.Fatalf("first sample error = %v", err)
	}
	if first != 80 {
		t.Fatalf("first sample = %v, want 80", first)
	}

	second, err := percent(context.Background())
	if err != nil {
		t.Fatalf("second sample error = %v", err)
	}
	if second != 75 {
		t.Fatalf("second sample = %v, want 75", second)
	}
}

func TestParseProcStatExcludesGuestTimeFromTotal(t *testing.T) {
	snapshot, err := parseProcStat(strings.NewReader("cpu 100 2 30 60 5 2 1 0 20 3\n"))
	if err != nil {
		t.Fatalf("parseProcStat() error = %v", err)
	}
	if snapshot.Total != 200 || snapshot.Idle != 65 {
		t.Fatalf("snapshot = %+v, want total=200 idle=65", snapshot)
	}
}

func TestParseProcMeminfoUsesAvailableMemory(t *testing.T) {
	input := "MemTotal:       1000 kB\nMemFree:         100 kB\nMemAvailable:    250 kB\nCached:          120 kB\nSReclaimable:     30 kB\n"
	snapshot, err := parseProcMeminfo(strings.NewReader(input))
	if err != nil {
		t.Fatalf("parseProcMeminfo() error = %v", err)
	}
	if snapshot.Total != 1000*1024 || snapshot.Used != 750*1024 {
		t.Fatalf("snapshot = %+v, want total=1000 KiB used=750 KiB", snapshot)
	}
	if math.Abs(snapshot.UsedPercent-75) > 0.0001 {
		t.Fatalf("used percent = %v, want 75", snapshot.UsedPercent)
	}
}

func TestParseProcMeminfoFallsBackWithoutAvailableMemory(t *testing.T) {
	input := "MemTotal:       1000 kB\nMemFree:         100 kB\nCached:          120 kB\nSReclaimable:     30 kB\n"
	snapshot, err := parseProcMeminfo(strings.NewReader(input))
	if err != nil {
		t.Fatalf("parseProcMeminfo() error = %v", err)
	}
	if snapshot.Used != 750*1024 {
		t.Fatalf("used = %d, want 750 KiB", snapshot.Used)
	}
}

func TestParseProcNetDevReadsInterfaceCounters(t *testing.T) {
	input := "Inter-| Receive | Transmit\n face |bytes packets errs drop fifo frame compressed multicast|bytes packets errs drop fifo colls carrier compressed\n" +
		"    lo: 100 1 0 0 0 0 0 0 200 2 0 0 0 0 0 0\n" +
		"  eth0: 3 1 0 0 0 0 0 0 4 2 0 0 0 0 0 0\n"
	counters, err := parseProcNetDev(strings.NewReader(input))
	if err != nil {
		t.Fatalf("parseProcNetDev() error = %v", err)
	}
	if len(counters) != 2 {
		t.Fatalf("counter count = %d, want 2", len(counters))
	}
	if counters[0].Name != "lo" || counters[0].BytesRecv != 100 || counters[0].BytesSent != 200 {
		t.Fatalf("first counter = %+v", counters[0])
	}
	if counters[1].Name != "eth0" || counters[1].BytesRecv != 3 || counters[1].BytesSent != 4 {
		t.Fatalf("second counter = %+v", counters[1])
	}
}
