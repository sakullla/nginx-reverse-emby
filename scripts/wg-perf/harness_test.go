package main

import (
	"os"
	"reflect"
	"strings"
	"testing"
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
	if rule.ListenPort != 7000 {
		t.Fatalf("relay-wg listen_port = %d, want 7000", rule.ListenPort)
	}
	if rule.ListenMode != "wireguard" || rule.WireGuardInboundMode != "address" || rule.WireGuardProfileID == nil || *rule.WireGuardProfileID != 1 {
		t.Fatalf("relay-wg WireGuard entry mode/profile = mode=%q inbound=%q profile=%#v", rule.ListenMode, rule.WireGuardInboundMode, rule.WireGuardProfileID)
	}
	if rule.WireGuardListenHost != "10.80.0.1" {
		t.Fatalf("relay-wg wireguard_listen_host = %q, want 10.80.0.1", rule.WireGuardListenHost)
	}
	if len(rule.Backends) != 1 || rule.Backends[0].Host != cfg.backendHost || rule.Backends[0].Port != cfg.backendPort {
		t.Fatalf("relay-wg backends = %+v, want backend host/port", rule.Backends)
	}
	if want := [][]int{{2, 3}, {4, 5}}; !reflect.DeepEqual(rule.RelayLayers, want) {
		t.Fatalf("relay-wg relay_layers = %#v, want %#v", rule.RelayLayers, want)
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

func TestWgPerfComposeUsesL4EntryPort(t *testing.T) {
	data, err := os.ReadFile("docker-compose.yaml")
	if err != nil {
		t.Fatalf("read compose: %v", err)
	}
	compose := string(data)
	if !strings.Contains(compose, "HARNESS_ENTRY_ADDRESS: ${HARNESS_ENTRY_ADDRESS:-10.80.0.1:7000}") {
		t.Fatal("compose does not default perf entry address to WireGuard tunnel L4 10.80.0.1:7000")
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
	for _, name := range []string{"nre-agent-b", "nre-backend-b"} {
		if !strings.Contains(compose, name) || !strings.Contains(compose, "NET_ADMIN") {
			t.Fatalf("compose must allow netem for %s", name)
		}
	}
}

func TestWgPerfRunScriptDefaultsToFortyMsOneWayDelay(t *testing.T) {
	data, err := os.ReadFile("run.ps1")
	if err != nil {
		t.Fatalf("read run script: %v", err)
	}
	script := string(data)
	for _, want := range []string{"$delayCliToWg = 40", "$delayWgToRelay = 40", "$delayRelayAToRelayB = 40", "nre-agent-b", "nre-backend-b"} {
		if !strings.Contains(script, want) {
			t.Fatalf("run.ps1 missing default %q", want)
		}
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
