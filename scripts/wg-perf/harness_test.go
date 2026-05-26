package main

import (
	"encoding/binary"
	"errors"
	"io"
	"net"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestWgPerfRunScriptTargetsWgPerfHarness(t *testing.T) {
	data, err := os.ReadFile("run.ps1")
	if err != nil {
		t.Fatalf("read run script: %v", err)
	}
	if !strings.Contains(strings.ToLower(string(data)), "wg-perf") {
		t.Fatal("run.ps1 does not target wg-perf harness")
	}
}

func TestWgPerfHarnessIncludesWireGuardRelay(t *testing.T) {
	cfg := loadConfig()
	snapshots := buildSnapshots(cfg, "cert", "key", "pin")

	if _, ok := snapshots["agent-a"]; ok {
		t.Fatal("agent-a snapshot should not exist; WireGuard entry replaces agent-a")
	}
	relayWG := snapshots["relay-wg"]
	if len(relayWG.L4Rules) != 1 {
		t.Fatalf("relay-wg L4Rules = %d, want WireGuard entry rule", len(relayWG.L4Rules))
	}
	rule := relayWG.L4Rules[0]
	if rule.ListenPort != 0 {
		t.Fatalf("relay-wg listen_port = %d, want transparent wildcard port 0", rule.ListenPort)
	}
	if rule.ListenMode != "wireguard" || rule.WireGuardInboundMode != "transparent" || rule.WireGuardProfileID == nil || *rule.WireGuardProfileID != 1 {
		t.Fatalf("relay-wg WireGuard entry mode/profile = mode=%q inbound=%q profile=%#v", rule.ListenMode, rule.WireGuardInboundMode, rule.WireGuardProfileID)
	}
	if rule.ProxyEgressMode != "relay" {
		t.Fatalf("relay-wg proxy_egress_mode = %q, want relay", rule.ProxyEgressMode)
	}
	if rule.WireGuardListenHost != "" {
		t.Fatalf("relay-wg wireguard_listen_host = %q, want empty transparent listener host", rule.WireGuardListenHost)
	}
	if len(rule.Backends) != 0 {
		t.Fatalf("relay-wg backends = %+v, want transparent target from original destination", rule.Backends)
	}
	if want := [][]int{{2, 3}, {4, 5}}; !reflect.DeepEqual(rule.RelayLayers, want) {
		t.Fatalf("relay-wg relay_layers = %#v, want %#v", rule.RelayLayers, want)
	}
	if relayWG.RelayListeners[0].PublicHost != "relay-a1.wg-perf.test" {
		t.Fatalf("relay-a1 public_host = %q, want DNS name", relayWG.RelayListeners[0].PublicHost)
	}
	if relayWG.RelayListeners[2].PublicHost != "relay-b3.wg-perf.test" {
		t.Fatalf("relay-b3 public_host = %q, want DNS name", relayWG.RelayListeners[2].PublicHost)
	}
	if len(relayWG.WireGuardProfiles) != 1 {
		t.Fatalf("relay-wg WireGuardProfiles = %d, want owner profile", len(relayWG.WireGuardProfiles))
	}
	if relayWG.WireGuardProfiles[0].ID != *rule.WireGuardProfileID {
		t.Fatalf("relay-wg profile id = %d, L4 rule references %d", relayWG.WireGuardProfiles[0].ID, *rule.WireGuardProfileID)
	}
	agentB := snapshots["agent-b"]
	if len(agentB.L4Rules) != 1 || len(agentB.L4Rules[0].Backends) != 1 {
		t.Fatalf("agent-b L4 rule backends = %+v, want 1 backend", agentB.L4Rules)
	}
}

func TestWgPerfBenchmarkFilterSelectsNamedBenchmarks(t *testing.T) {
	benches := []benchmarkCase{
		{name: "direct_b_c1"},
		{name: "wg_to_b_c1"},
		{name: "wg_to_b_upload_c1"},
		{name: "wg_to_b_c8"},
	}
	selected, err := selectBenchmarks("wg_to_b_c1,wg_to_b_upload_c1,wg_to_b_c8", benches)
	if err != nil {
		t.Fatalf("selectBenchmarks() error = %v", err)
	}
	var names []string
	for _, bench := range selected {
		names = append(names, bench.name)
	}
	if want := []string{"wg_to_b_c1", "wg_to_b_upload_c1", "wg_to_b_c8"}; !reflect.DeepEqual(names, want) {
		t.Fatalf("selected benchmarks = %#v, want %#v", names, want)
	}
}

func TestWgPerfBenchmarkFilterRejectsUnknownName(t *testing.T) {
	_, err := selectBenchmarks("missing", []benchmarkCase{{name: "wg_to_b_c1"}})
	if err == nil {
		t.Fatal("selectBenchmarks() error = nil, want error for unknown benchmark")
	}
}

func TestWgPerfComposeUsesL4EntryPort(t *testing.T) {
	data, err := os.ReadFile("docker-compose.yaml")
	if err != nil {
		t.Fatalf("read compose: %v", err)
	}
	compose := string(data)
	if !strings.Contains(compose, "HARNESS_ENTRY_ADDRESS: ${HARNESS_ENTRY_ADDRESS:-172.30.3.12:9001}") {
		t.Fatal("compose does not default perf entry address to final agent-b L4 IP through WireGuard")
	}
	if !strings.Contains(compose, "HARNESS_WG_ALLOWED_IPS: ${HARNESS_WG_ALLOWED_IPS:-172.30.3.12/32}") {
		t.Fatal("compose does not route the final target IP through WireGuard")
	}
	if !strings.Contains(compose, "dockerfile: scripts/wg-perf/Dockerfile.agent") {
		t.Fatal("compose does not use wg-perf agent Dockerfile")
	}
	if strings.Contains(compose, "container_name: nre-agent-a") {
		t.Fatal("compose should not include agent-a; WireGuard entry replaces it")
	}
	if !strings.Contains(compose, "ip link add wg0 type wireguard") {
		t.Fatal("compose perf runner does not create wg0 client interface")
	}
	for _, want := range []string{"relay-a1.wg-perf.test", "relay-a2.wg-perf.test", "relay-b3.wg-perf.test", "relay-b4.wg-perf.test"} {
		if !strings.Contains(compose, want) {
			t.Fatalf("compose missing relay DNS alias %q", want)
		}
	}
	for _, name := range []string{"nre-agent-b", "nre-backend-b"} {
		if !strings.Contains(compose, name) || !strings.Contains(compose, "NET_ADMIN") {
			t.Fatalf("compose must allow netem for %s", name)
		}
	}
}

func TestWgPerfRunScriptDefaultsToFortyMsRTTPerHop(t *testing.T) {
	data, err := os.ReadFile("run.ps1")
	if err != nil {
		t.Fatalf("read run script: %v", err)
	}
	script := string(data)
	for _, want := range []string{"$delayCliToWg = 20", "$delayWgToRelay = 20", "$delayRelayAToRelayB = 20", "nre-agent-b", "nre-backend-b"} {
		if !strings.Contains(script, want) {
			t.Fatalf("run.ps1 missing default %q", want)
		}
	}
}

func TestWgPerfRunScriptReportsProgressAndTimesOut(t *testing.T) {
	data, err := os.ReadFile("run.ps1")
	if err != nil {
		t.Fatalf("read run script: %v", err)
	}
	script := string(data)
	for _, want := range []string{"HARNESS_RUN_TIMEOUT_SECONDS", "wg-perf running", "wg-perf timed out"} {
		if !strings.Contains(script, want) {
			t.Fatalf("run.ps1 missing progress/timeout marker %q", want)
		}
	}
}

func TestWgPerfHarnessDefaultsThroughputBenchmarksToBoundedDuration(t *testing.T) {
	t.Setenv("HARNESS_C1_DURATION_SECONDS", "")
	t.Setenv("HARNESS_C8_DURATION_SECONDS", "")
	cfg := loadConfig()
	if cfg.c1Duration <= 0 || cfg.c8Duration <= 0 {
		t.Fatalf("throughput durations = c1 %s c8 %s, want bounded defaults", cfg.c1Duration, cfg.c8Duration)
	}
}

func TestWgPerfHarnessIncludesUploadBenchmarks(t *testing.T) {
	data, err := os.ReadFile("harness.go")
	if err != nil {
		t.Fatalf("read harness: %v", err)
	}
	harness := string(data)
	for _, want := range []string{"protocolModeUploadUnlimited", "direct_b_upload_c1", "wg_to_b_upload_c1"} {
		if !strings.Contains(harness, want) {
			t.Fatalf("harness.go missing upload benchmark marker %q", want)
		}
	}
}

func TestUploadFixedBytesSendsExpectedPayload(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	done := make(chan error, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			done <- err
			return
		}
		defer conn.Close()
		var header [9]byte
		if _, err := io.ReadFull(conn, header[:]); err != nil {
			done <- err
			return
		}
		if header[0] != protocolModeUpload {
			t.Errorf("mode = %d, want %d", header[0], protocolModeUpload)
		}
		if got := binary.BigEndian.Uint64(header[1:]); got != uint64(96*1024) {
			t.Errorf("upload size = %d, want %d", got, 96*1024)
		}
		if _, err := io.CopyN(io.Discard, conn, 96*1024); err != nil {
			done <- err
			return
		}
		done <- nil
	}()

	n, err := upload(ln.Addr().String(), 96*1024)
	if err != nil {
		t.Fatalf("upload() error = %v", err)
	}
	if n != 96*1024 {
		t.Fatalf("upload() = %d, want %d", n, 96*1024)
	}
	if err := <-done; err != nil {
		t.Fatalf("server error: %v", err)
	}
}

func TestUploadDeadlineTerminalErrorIsSuccessfulOnlyAfterDeadline(t *testing.T) {
	err := errors.New("write: broken pipe")
	if !isUploadDeadlineTerminalError(err, time.Now().Add(-time.Second)) {
		t.Fatal("broken pipe after deadline should end duration upload successfully")
	}
	if isUploadDeadlineTerminalError(err, time.Now().Add(time.Second)) {
		t.Fatal("broken pipe before deadline should remain an error")
	}
	if isUploadDeadlineTerminalError(errors.New("permission denied"), time.Now().Add(-time.Second)) {
		t.Fatal("unrelated errors should remain errors")
	}
}

func TestWgPerfHarnessConfiguresWireGuardBindAddresses(t *testing.T) {
	t.Setenv("HARNESS_WG_BIND_ADDRESSES", "172.30.2.15")
	cfg := loadConfig()
	snapshots := buildSnapshots(cfg, "cert", "key", "pin")
	profile := snapshots["relay-wg"].WireGuardProfiles[0]
	if !reflect.DeepEqual(profile.BindAddresses, []string{"172.30.2.15"}) {
		t.Fatalf("relay-wg bind_addresses = %#v, want docker-local bind address", profile.BindAddresses)
	}
}

func TestWgPerfHarnessCanDisableRelayLayers(t *testing.T) {
	t.Setenv("HARNESS_WG_RELAY_LAYERS", "none")
	cfg := loadConfig()
	snapshots := buildSnapshots(cfg, "cert", "key", "pin")
	rule := snapshots["relay-wg"].L4Rules[0]
	if len(rule.RelayLayers) != 0 {
		t.Fatalf("relay layers = %#v, want direct WG-to-backend path", rule.RelayLayers)
	}
}

func TestWgPerfComposeExposesWireGuardBindAddressOverride(t *testing.T) {
	data, err := os.ReadFile("docker-compose.yaml")
	if err != nil {
		t.Fatalf("read compose: %v", err)
	}
	if !strings.Contains(string(data), "HARNESS_WG_BIND_ADDRESSES") {
		t.Fatal("docker-compose.yaml does not expose HARNESS_WG_BIND_ADDRESSES")
	}
}

func TestWgPerfRunScriptStatsHeaderMatchesRows(t *testing.T) {
	data, err := os.ReadFile("run.ps1")
	if err != nil {
		t.Fatalf("read run script: %v", err)
	}
	script := string(data)
	if !strings.Contains(script, "$statsRows.Add('ts,name,cpu,mem,net')") {
		t.Fatal("run.ps1 stats header does not match docker stats row format")
	}
	if strings.Contains(script, "$statsRows.Add('ts,name,cpu,mem,net,ps,threads')") {
		t.Fatal("run.ps1 stats header advertises ps/threads columns that rows do not contain")
	}
}

func TestEchoOnceRejectsMismatchedPayload(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	done := make(chan error, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			done <- err
			return
		}
		defer conn.Close()
		var mode [1]byte
		if _, err := io.ReadFull(conn, mode[:]); err != nil {
			done <- err
			return
		}
		payload := make([]byte, len("payload"))
		if _, err := io.ReadFull(conn, payload); err != nil {
			done <- err
			return
		}
		_, err = conn.Write([]byte("PAYLOAD"))
		done <- err
	}()

	if err := echoOnce(ln.Addr().String(), []byte("payload")); err == nil {
		t.Fatal("echoOnce() error = nil, want mismatch error")
	}
	if err := <-done; err != nil {
		t.Fatalf("server error: %v", err)
	}
}

func TestWgPerfComposeIncludesWireGuardRelay(t *testing.T) {
	data, err := os.ReadFile("docker-compose.yaml")
	if err != nil {
		t.Fatalf("read compose: %v", err)
	}
	compose := string(data)
	if !strings.Contains(compose, "relay-wg") {
		t.Fatal("docker-compose.yaml does not include relay-wg container")
	}
}

func TestWgPerfComposeExposesRelayWgPprof(t *testing.T) {
	data, err := os.ReadFile("docker-compose.yaml")
	if err != nil {
		t.Fatalf("read compose: %v", err)
	}
	compose := string(data)
	cases := []struct {
		service string
		env     string
		port    string
	}{
		{"relay-wg", "HARNESS_RELAY_WG_PPROF_ADDR", "HARNESS_RELAY_WG_PPROF_PORT:-6060"},
		{"relay-a1", "HARNESS_RELAY_A1_PPROF_ADDR", "HARNESS_RELAY_A1_PPROF_PORT:-6061"},
		{"relay-a2", "HARNESS_RELAY_A2_PPROF_ADDR", "HARNESS_RELAY_A2_PPROF_PORT:-6062"},
		{"relay-b3", "HARNESS_RELAY_B3_PPROF_ADDR", "HARNESS_RELAY_B3_PPROF_PORT:-6063"},
		{"relay-b4", "HARNESS_RELAY_B4_PPROF_ADDR", "HARNESS_RELAY_B4_PPROF_PORT:-6064"},
		{"agent-b", "HARNESS_AGENT_B_PPROF_ADDR", "HARNESS_AGENT_B_PPROF_PORT:-6065"},
	}
	for i, tc := range cases {
		block := composeServiceBlock(t, compose, tc.service)
		for _, want := range []string{
			"NRE_PPROF_ADDR: ${" + tc.env + ":-:6060}",
			"${" + tc.port + "}:6060",
		} {
			if !strings.Contains(block, want) {
				t.Fatalf("docker-compose.yaml service %s missing pprof setting %q", tc.service, want)
			}
		}
		if i == 0 && !strings.Contains(block, "container_name: nre-relay-wg") {
			t.Fatal("relay-wg pprof assertion matched the wrong service block")
		}
	}
}

func composeServiceBlock(t *testing.T, compose, service string) string {
	t.Helper()
	lines := strings.Split(compose, "\n")
	var b strings.Builder
	inBlock := false
	for _, line := range lines {
		if line == "  "+service+":" {
			inBlock = true
		} else if inBlock && strings.HasPrefix(line, "  ") && len(line) > 2 && line[2] != ' ' {
			break
		}
		if inBlock {
			b.WriteString(line)
			b.WriteByte('\n')
		}
	}
	if b.Len() == 0 {
		t.Fatalf("docker-compose.yaml missing %s service", service)
	}
	return b.String()
}
