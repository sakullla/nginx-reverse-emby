package main

import (
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestHarnessDefinesHTTPAndL4Benchmarks(t *testing.T) {
	for _, concurrency := range []int{1, 8, 16} {
		benches := buildBenchmarks(config{c8Concurrency: concurrency})
		selected, err := selectBenchmarks("http_direct_c1,http_relay2_c8,l4_direct_c1,l4_relay2_c8", benches)
		if err != nil || len(selected) != 4 {
			t.Fatalf("selected=%v error=%v", selected, err)
		}
		if len(benches) != 12 {
			t.Fatalf("benchmarks=%d", len(benches))
		}
	}
}
func TestComposeWiresHTTPRelayServicesAndTuningEnv(t *testing.T) {
	data, err := os.ReadFile("docker-compose.yaml")
	if err != nil {
		t.Fatalf("read compose: %v", err)
	}

	compose := string(data)
	for _, want := range []string{
		"backend-b:",
		"relay-b:",
		"relay-c:",
		"agent-a:",
		"HARNESS_RELAY2_ADDRESS:",
		"NRE_HTTP_MAX_CONNS_PER_HOST: ${NRE_HTTP_MAX_CONNS_PER_HOST:-",
		"NRE_TRAFFIC_STATS_ENABLED: ${NRE_TRAFFIC_STATS_ENABLED:-true}",
	} {
		if !strings.Contains(compose, want) {
			t.Fatalf("docker-compose.yaml missing marker %q", want)
		}
	}
}

func TestBuildSnapshotsIncludesTwoHopHTTPRelayRule(t *testing.T) {
	cfg := config{
		directAddress:    "http://172.31.1.10:8081",
		relayAddress:     "http://172.31.1.10:8082",
		relay2Address:    "http://172.31.1.10:8083",
		backendURL:       "http://172.31.3.13:9002",
		relayPublicHost:  "172.31.2.11",
		relayPublicPort:  9443,
		relay2PublicHost: "172.31.2.12",
		relay2PublicPort: 9444,
		relayLayers:      [][]int{{1}},
		relay2Layers:     [][]int{{1}, {2}},
	}

	snapshots := buildSnapshots(cfg, "cert", "key", "pin")
	agent := snapshots["agent-a"]

	var found bool
	for _, rule := range agent.Rules {
		if rule.FrontendURL != cfg.relay2Address {
			continue
		}
		found = true
		if !reflect.DeepEqual(rule.RelayLayers, [][]int{{1}, {2}}) {
			t.Fatalf("relay2 layers = %#v, want [[1] [2]]", rule.RelayLayers)
		}
	}
	if !found {
		t.Fatal("two-hop relay HTTP rule not found")
	}
	if len(agent.RelayListeners) != 2 {
		t.Fatalf("agent-a relay listeners = %d, want 2", len(agent.RelayListeners))
	}
	if !snapshotHasRelayListener(snapshots["relay-b"], 1) {
		t.Fatalf("relay-b snapshot missing listener 1: %+v", snapshots["relay-b"].RelayListeners)
	}
	if !snapshotHasRelayListener(snapshots["relay-c"], 2) {
		t.Fatalf("relay-c snapshot missing listener 2: %+v", snapshots["relay-c"].RelayListeners)
	}
}

func snapshotHasRelayListener(snap snapshot, id int) bool {
	for _, listener := range snap.RelayListeners {
		if listener.ID == id {
			return true
		}
	}
	return false
}
