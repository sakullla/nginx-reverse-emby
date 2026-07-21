package hostmetrics

import (
	"math"
	"testing"
)

func TestParseDarwinCPUUsage(t *testing.T) {
	usage, err := parseDarwinCPUUsage([]byte("Processes: 500 total\nCPU usage: 7.82% user, 6.15% sys, 86.03% idle\n"))
	if err != nil {
		t.Fatalf("parseDarwinCPUUsage() error = %v", err)
	}
	if math.Abs(usage-13.97) > 0.0001 {
		t.Fatalf("usage = %v, want 13.97", usage)
	}
}

func TestParseDarwinAvailableMemory(t *testing.T) {
	available, err := parseDarwinAvailableMemory([]byte("Mach Virtual Memory Statistics: (page size of 16384 bytes)\nPages free: 10.\nPages inactive: 20.\n"))
	if err != nil {
		t.Fatalf("parseDarwinAvailableMemory() error = %v", err)
	}
	if available != 30*16384 {
		t.Fatalf("available = %d, want %d", available, 30*16384)
	}
}

func TestParseDarwinNetworkCounters(t *testing.T) {
	output := "Name Mtu Network Address Ipkts Ierrs Ibytes Opkts Oerrs Obytes Coll Drop\n" +
		"lo0 16384 <Link#1> 10 0 100 11 0 200 0 0\n" +
		"en0 1500 <Link#4> aa:bb:cc:dd:ee:ff 30 0 300 31 0 400 0 0\n"
	counters, err := parseDarwinNetworkCounters([]byte(output))
	if err != nil {
		t.Fatalf("parseDarwinNetworkCounters() error = %v", err)
	}
	if len(counters) != 2 {
		t.Fatalf("counter count = %d, want 2", len(counters))
	}
	if counters[0] != (networkCounter{Name: "lo0", BytesRecv: 100, BytesSent: 200}) {
		t.Fatalf("lo0 counter = %+v", counters[0])
	}
	if counters[1] != (networkCounter{Name: "en0", BytesRecv: 300, BytesSent: 400}) {
		t.Fatalf("en0 counter = %+v", counters[1])
	}
}

func TestParseDarwinNetworkCountersPreservesTruncatedInterfaceNames(t *testing.T) {
	output := "Name Mtu Network Address Ipkts Ierrs Ibytes Opkts Oerrs Obytes Coll Drop\n" +
		"utun8 1380 <Link#20> 10 0 100 11 0 200 0 0\n" +
		"utun8 1380 <Link#21> 30 0 300 31 0 400 0 0\n"
	counters, err := parseDarwinNetworkCounters([]byte(output))
	if err != nil {
		t.Fatalf("parseDarwinNetworkCounters() error = %v", err)
	}
	if len(counters) != 2 {
		t.Fatalf("counter count = %d, want 2: %+v", len(counters), counters)
	}
	var bytesRecv uint64
	var bytesSent uint64
	for _, counter := range counters {
		bytesRecv += counter.BytesRecv
		bytesSent += counter.BytesSent
	}
	if bytesRecv != 400 || bytesSent != 600 {
		t.Fatalf("network totals = rx %d tx %d, want rx 400 tx 600", bytesRecv, bytesSent)
	}
}
