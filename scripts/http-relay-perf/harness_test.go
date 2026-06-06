package main

import (
	"os"
	"strings"
	"testing"
)

func TestHarnessDefinesHTTPRelayThroughputBenchmarks(t *testing.T) {
	data, err := os.ReadFile("harness.go")
	if err != nil {
		t.Fatalf("read harness: %v", err)
	}

	harness := string(data)
	for _, want := range []string{
		"http_direct_c1",
		"http_relay_c1",
		"http_direct_c8",
		"http_relay_c8",
		"HARNESS_DOWNLOAD_BYTES",
		"HARNESS_RELAY_LAYER_IDS",
	} {
		if !strings.Contains(harness, want) {
			t.Fatalf("harness.go missing marker %q", want)
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
		"agent-a:",
		"NRE_HTTP_MAX_CONNS_PER_HOST: ${NRE_HTTP_MAX_CONNS_PER_HOST:-",
		"NRE_TRAFFIC_STATS_ENABLED: ${NRE_TRAFFIC_STATS_ENABLED:-true}",
	} {
		if !strings.Contains(compose, want) {
			t.Fatalf("docker-compose.yaml missing marker %q", want)
		}
	}
}

func TestRunScriptTargetsHTTPRelayPerfContainers(t *testing.T) {
	data, err := os.ReadFile("run.ps1")
	if err != nil {
		t.Fatalf("read run.ps1: %v", err)
	}

	script := string(data)
	for _, want := range []string{
		"nre-http-relay-perf",
		"nre-relay-b",
		"nre-backend-b",
		"docker stats --no-stream",
		"HARNESS_DELAY_CLI_TO_HTTP_MS",
		"HARNESS_DELAY_HTTP_TO_BACKEND_MS",
		"HARNESS_NETEM_DELAY_MS",
		"$defaultDelayCliToHttp = 30",
		"$defaultDelayHttpToBackend = 10",
		"Applying ${DelayMs}ms netem delay",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("run.ps1 missing marker %q", want)
		}
	}

	for _, want := range []string{
		"$exitCode =",
		".State.ExitCode",
		"exit $exitCode",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("run.ps1 missing exit-code marker %q", want)
		}
	}
}
