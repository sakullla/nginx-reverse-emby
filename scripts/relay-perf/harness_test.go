package main

import (
	"context"
	"io"
	"net"
	"os"
	"strings"
	"testing"
	"time"
)

func TestLoadConfigParsesThroughputDurations(t *testing.T) {
	t.Setenv("HARNESS_C1_DURATION_SECONDS", "30")
	t.Setenv("HARNESS_C8_DURATION_SECONDS", "45")

	cfg := loadConfig()

	if cfg.c1Duration != 30*time.Second {
		t.Fatalf("c1Duration = %v, want %v", cfg.c1Duration, 30*time.Second)
	}
	if cfg.c8Duration != 45*time.Second {
		t.Fatalf("c8Duration = %v, want %v", cfg.c8Duration, 45*time.Second)
	}
}

func TestRelayPerfBenchmarkFilterSelectsNamedBenchmarks(t *testing.T) {
	benches := []benchmarkCase{
		{name: "direct_b_c1"},
		{name: "relay_a_to_b_c1"},
		{name: "relay_a_to_b_c8"},
	}
	selected, err := selectBenchmarks("relay_a_to_b_c1,relay_a_to_b_c8", benches)
	if err != nil {
		t.Fatalf("selectBenchmarks() error = %v", err)
	}
	var names []string
	for _, bench := range selected {
		names = append(names, bench.name)
	}
	if got := strings.Join(names, ","); got != "relay_a_to_b_c1,relay_a_to_b_c8" {
		t.Fatalf("selected benchmarks = %q", got)
	}
}

func TestRelayPerfBenchmarkFilterRejectsUnknownName(t *testing.T) {
	_, err := selectBenchmarks("missing", []benchmarkCase{{name: "relay_a_to_b_c1"}})
	if err == nil {
		t.Fatal("selectBenchmarks() error = nil, want error for unknown benchmark")
	}
}

func TestRelayPerfSnapshotsUseStructuredL4Backends(t *testing.T) {
	cfg := loadConfig()
	snapshots := buildSnapshots(cfg, "cert", "key", "pin")
	for agentID, ruleIndex := range map[string]int{"agent-a": 0, "agent-b": 0} {
		rule := snapshots[agentID].L4Rules[ruleIndex]
		if len(rule.Backends) != 1 {
			t.Fatalf("%s backends = %#v, want one backend", agentID, rule.Backends)
		}
		if rule.Backends[0].Host != cfg.backendHost || rule.Backends[0].Port != cfg.backendPort {
			t.Fatalf("%s backend = %#v, want %s:%d", agentID, rule.Backends[0], cfg.backendHost, cfg.backendPort)
		}
	}
}

func TestHandleBackendConnStreamsUnlimitedDownloadUntilClientClose(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer clientConn.Close()

	done := make(chan struct{})
	go func() {
		handleBackendConn(serverConn)
		close(done)
	}()

	if _, err := clientConn.Write([]byte{protocolModeDownloadUnlimited}); err != nil {
		t.Fatalf("client write mode: %v", err)
	}

	buf := make([]byte, 128*1024)
	n, err := io.ReadFull(clientConn, buf)
	if err != nil {
		t.Fatalf("client read download payload: %v", err)
	}
	if n != len(buf) {
		t.Fatalf("client read bytes = %d, want %d", n, len(buf))
	}

	_ = clientConn.Close()

	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("backend unlimited download did not stop after client close")
	}
}

func TestTransferForDurationDownloadsUntilDeadline(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()

	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		handleBackendConn(conn)
	}()

	deadline := time.Now().Add(150 * time.Millisecond)
	n, err := transferForDuration(ln.Addr().String(), deadline)
	if err != nil {
		t.Fatalf("transferForDuration: %v", err)
	}
	if n <= 0 {
		t.Fatalf("transferForDuration bytes = %d, want > 0", n)
	}

	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("backend connection did not exit after timed transfer")
	}
}

func TestRelayPerfComposePassesTrafficStatsToggleToAgents(t *testing.T) {
	data, err := os.ReadFile("docker-compose.yaml")
	if err != nil {
		t.Fatalf("read compose: %v", err)
	}

	compose := string(data)
	for _, service := range []string{"agent-a", "relay-a1", "relay-a2", "relay-b3", "relay-b4", "agent-b"} {
		serviceBlock := yamlTopLevelBlock(t, compose, "  "+service+":")
		if !strings.Contains(serviceBlock, "NRE_TRAFFIC_STATS_ENABLED: ${NRE_TRAFFIC_STATS_ENABLED:-true}") {
			t.Fatalf("%s does not pass NRE_TRAFFIC_STATS_ENABLED through", service)
		}
	}
}

func TestRunScriptCollectsStatsForActualRelayContainers(t *testing.T) {
	data, err := os.ReadFile("run.ps1")
	if err != nil {
		t.Fatalf("read run script: %v", err)
	}

	script := string(data)
	if strings.Contains(script, "nre-relay-b ") {
		t.Fatal("run.ps1 still references nonexistent nre-relay-b container")
	}
	if strings.Contains(script, "'nre-relay-b'") {
		t.Fatal("run.ps1 still applies delay to nonexistent nre-relay-b container")
	}
	for _, container := range []string{"nre-relay-a1", "nre-relay-a2", "nre-relay-b3", "nre-relay-b4"} {
		if !strings.Contains(script, container) {
			t.Fatalf("run.ps1 does not collect docker stats for %s", container)
		}
	}
}

func TestRunScriptDefaultsToDelayedRelayModel(t *testing.T) {
	data, err := os.ReadFile("run.ps1")
	if err != nil {
		t.Fatalf("read run script: %v", err)
	}

	script := string(data)
	for _, want := range []string{
		"$defaultDelayCliToA = 30",
		"$defaultDelayAToRelay = 10",
		"$env:HARNESS_PRE_MEASURE_DELAY_MS = '8000'",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("run.ps1 missing default delayed relay model marker %q", want)
		}
	}
}

func yamlTopLevelBlock(t *testing.T, yaml, header string) string {
	t.Helper()

	start := strings.Index(yaml, header)
	if start < 0 {
		t.Fatalf("compose block %q not found", header)
	}
	block := yaml[start:]
	lines := strings.Split(block, "\n")
	for i := 1; i < len(lines); i++ {
		line := lines[i]
		if strings.HasPrefix(line, "  ") && !strings.HasPrefix(line, "    ") && strings.HasSuffix(strings.TrimSpace(line), ":") {
			return strings.Join(lines[:i], "\n")
		}
	}
	return block
}
