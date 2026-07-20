package model

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestLoadFromEnv(t *testing.T) {
	t.Setenv("NRE_AGENT_ID", "agent-42")
	t.Setenv("NRE_AGENT_NAME", "linux-agent")
	t.Setenv("NRE_AGENT_TOKEN", "secret")
	t.Setenv("NRE_MASTER_URL", "https://master.example.com/")
	t.Setenv("NRE_DATA_DIR", "/tmp/nre-data")
	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if cfg.AgentID != "agent-42" {
		t.Fatalf("expected AgentID, got %q", cfg.AgentID)
	}
	if cfg.AgentName != "linux-agent" {
		t.Fatalf("expected AgentName, got %q", cfg.AgentName)
	}
	if cfg.MasterURL != "https://master.example.com" {
		t.Fatalf("expected trimmed master URL, got %q", cfg.MasterURL)
	}
	if cfg.DataDir != "/tmp/nre-data" {
		t.Fatalf("expected data directory from env, got %q", cfg.DataDir)
	}
	if cfg.HeartbeatInterval != 10*time.Second {
		t.Fatalf("expected default heartbeat, got %v", cfg.HeartbeatInterval)
	}
}

func TestLoadFromEnvNormalizesMasterURLWithApiPrefix(t *testing.T) {
	t.Setenv("NRE_AGENT_TOKEN", "secret")
	t.Setenv("NRE_MASTER_URL", "https://master.example.com/panel-api/")

	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if cfg.MasterURL != "https://master.example.com/panel-api" {
		t.Fatalf("expected env loader to keep explicit prefix, got %q", cfg.MasterURL)
	}
}

func TestLoadFromEnvRequiresMasterURLAndToken(t *testing.T) {
	t.Setenv("NRE_AGENT_TOKEN", "secret")
	if _, err := LoadFromEnv(); err == nil {
		t.Fatal("expected error when NRE_MASTER_URL missing")
	}
	t.Setenv("NRE_MASTER_URL", "https://master.example.com")
	t.Setenv("NRE_AGENT_TOKEN", "")
	if _, err := LoadFromEnv(); err == nil {
		t.Fatal("expected error when NRE_AGENT_TOKEN missing")
	}
}

func TestLoadFromEnvRejectsNonPositiveHeartbeat(t *testing.T) {
	t.Setenv("NRE_MASTER_URL", "https://master.example.com")
	t.Setenv("NRE_AGENT_TOKEN", "secret")
	t.Setenv("NRE_HEARTBEAT_INTERVAL", "-5s")
	if _, err := LoadFromEnv(); err == nil {
		t.Fatal("expected error for non-positive heartbeat interval")
	}
	t.Setenv("NRE_HEARTBEAT_INTERVAL", "0s")
	if _, err := LoadFromEnv(); err == nil {
		t.Fatal("expected error for zero heartbeat interval")
	}
}

func TestLoadFromEnvFeatureFlags(t *testing.T) {
	t.Setenv("NRE_MASTER_URL", "https://master.example.com")
	t.Setenv("NRE_AGENT_TOKEN", "secret")
	flags := []string{"NRE_HTTP3_ENABLED", "NRE_TRAFFIC_STATS_ENABLED"}
	for _, name := range flags {
		t.Setenv(name, "")
	}

	cases := []struct {
		name      string
		value     string
		wantHTTP3 bool
		wantStats bool
		wantError bool
	}{
		{wantStats: true},
		{name: "NRE_HTTP3_ENABLED", value: "true", wantHTTP3: true, wantStats: true},
		{name: "NRE_TRAFFIC_STATS_ENABLED", value: "false"},
		{name: "NRE_HTTP3_ENABLED", value: "maybe", wantError: true},
		{name: "NRE_TRAFFIC_STATS_ENABLED", value: "maybe", wantError: true},
	}

	for _, tc := range cases {
		for _, name := range flags {
			if err := os.Setenv(name, ""); err != nil {
				t.Fatal(err)
			}
		}
		if tc.name != "" {
			if err := os.Setenv(tc.name, tc.value); err != nil {
				t.Fatal(err)
			}
		}

		cfg, err := LoadFromEnv()
		if tc.wantError {
			if err == nil || !strings.Contains(err.Error(), tc.name) {
				t.Fatalf("%s=%q error = %v", tc.name, tc.value, err)
			}
			continue
		}
		if err != nil {
			t.Fatalf("%s=%q LoadFromEnv() error = %v", tc.name, tc.value, err)
		}
		if cfg.HTTP3Enabled != tc.wantHTTP3 || cfg.TrafficStatsEnabled != tc.wantStats {
			t.Fatalf("%s=%q flags = http3:%t stats:%t", tc.name, tc.value, cfg.HTTP3Enabled, cfg.TrafficStatsEnabled)
		}
	}
}
func TestLoadFromEnvParsesTrafficInterfaces(t *testing.T) {
	t.Setenv("NRE_MASTER_URL", "https://master.example.com")
	t.Setenv("NRE_AGENT_TOKEN", "secret")
	t.Setenv("NRE_TRAFFIC_INTERFACES", " eth0,ens3 ,, eth1 ")

	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("LoadFromEnv() error = %v", err)
	}
	want := []string{"eth0", "ens3", "eth1"}
	if !reflect.DeepEqual(cfg.TrafficInterfaces, want) {
		t.Fatalf("TrafficInterfaces = %#v, want %#v", cfg.TrafficInterfaces, want)
	}
}

func TestLoadFromEnvRuntimePackageSHA256(t *testing.T) {
	execPath := filepath.Join(t.TempDir(), "nre-agent")
	payload := []byte("agent-binary")
	if err := os.WriteFile(execPath, payload, 0o755); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	t.Setenv("NRE_MASTER_URL", "https://master.example.com")
	t.Setenv("NRE_AGENT_TOKEN", "secret")
	t.Setenv("NRE_AGENT_VERSION", "1.2.3")

	cfg, err := loadFromEnvForExecutable(execPath)
	if err != nil {
		t.Fatalf("loadFromEnvForExecutable returned error: %v", err)
	}
	if cfg.RuntimePackageSHA256 != sumSHA256Hex(payload) {
		t.Fatalf("expected runtime package sha %q, got %q", sumSHA256Hex(payload), cfg.RuntimePackageSHA256)
	}

	cfg, err = loadFromEnvForExecutable(filepath.Join(t.TempDir(), "missing-agent"))
	if err != nil {
		t.Fatalf("loadFromEnvForExecutable returned error: %v", err)
	}
	if cfg.RuntimePackageSHA256 != "" {
		t.Fatalf("expected empty runtime package sha, got %q", cfg.RuntimePackageSHA256)
	}
}

func TestLoadFromEnvParsesHTTPResilienceSettings(t *testing.T) {
	t.Setenv("NRE_MASTER_URL", "https://master.example.com")
	t.Setenv("NRE_AGENT_TOKEN", "secret")
	t.Setenv("NRE_HTTP_DIAL_TIMEOUT", "7s")
	t.Setenv("NRE_HTTP_TLS_HANDSHAKE_TIMEOUT", "9s")
	t.Setenv("NRE_HTTP_RESPONSE_HEADER_TIMEOUT", "45s")
	t.Setenv("NRE_HTTP_IDLE_CONN_TIMEOUT", "3m")
	t.Setenv("NRE_HTTP_KEEP_ALIVE", "25s")
	t.Setenv("NRE_HTTP_MAX_CONNS_PER_HOST", "24")
	t.Setenv("NRE_HTTP_STREAM_RESUME_ENABLED", "true")
	t.Setenv("NRE_HTTP_STREAM_RESUME_MAX_ATTEMPTS", "2")
	t.Setenv("NRE_HTTP_SAME_BACKEND_RETRY_ATTEMPTS", "1")
	t.Setenv("NRE_BACKEND_FAILURE_BACKOFF_BASE", "250ms")
	t.Setenv("NRE_BACKEND_FAILURE_BACKOFF_LIMIT", "10s")
	t.Setenv("NRE_RELAY_DIAL_TIMEOUT", "6s")
	t.Setenv("NRE_RELAY_HANDSHAKE_TIMEOUT", "8s")
	t.Setenv("NRE_RELAY_FRAME_TIMEOUT", "4s")
	t.Setenv("NRE_RELAY_IDLE_TIMEOUT", "75s")

	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("LoadFromEnv() error = %v", err)
	}
	if cfg.HTTPTransport.DialTimeout != 7*time.Second {
		t.Fatalf("DialTimeout = %v", cfg.HTTPTransport.DialTimeout)
	}
	if cfg.HTTPTransport.TLSHandshakeTimeout != 9*time.Second {
		t.Fatalf("TLSHandshakeTimeout = %v", cfg.HTTPTransport.TLSHandshakeTimeout)
	}
	if cfg.HTTPTransport.ResponseHeaderTimeout != 45*time.Second {
		t.Fatalf("ResponseHeaderTimeout = %v", cfg.HTTPTransport.ResponseHeaderTimeout)
	}
	if cfg.HTTPTransport.IdleConnTimeout != 3*time.Minute {
		t.Fatalf("IdleConnTimeout = %v", cfg.HTTPTransport.IdleConnTimeout)
	}
	if cfg.HTTPTransport.KeepAlive != 25*time.Second {
		t.Fatalf("KeepAlive = %v", cfg.HTTPTransport.KeepAlive)
	}
	if cfg.HTTPTransport.MaxConnsPerHost != 24 {
		t.Fatalf("MaxConnsPerHost = %d", cfg.HTTPTransport.MaxConnsPerHost)
	}
	if !cfg.HTTPResilience.ResumeEnabled {
		t.Fatal("expected ResumeEnabled")
	}
	if cfg.HTTPResilience.ResumeMaxAttempts != 2 {
		t.Fatalf("ResumeMaxAttempts = %d", cfg.HTTPResilience.ResumeMaxAttempts)
	}
	if cfg.HTTPResilience.SameBackendRetryAttempts != 1 {
		t.Fatalf("SameBackendRetryAttempts = %d", cfg.HTTPResilience.SameBackendRetryAttempts)
	}
	if cfg.BackendFailures.BackoffBase != 250*time.Millisecond {
		t.Fatalf("BackoffBase = %v", cfg.BackendFailures.BackoffBase)
	}
	if cfg.BackendFailures.BackoffLimit != 10*time.Second {
		t.Fatalf("BackoffLimit = %v", cfg.BackendFailures.BackoffLimit)
	}
	if cfg.RelayTimeouts.DialTimeout != 6*time.Second {
		t.Fatalf("DialTimeout = %v", cfg.RelayTimeouts.DialTimeout)
	}
	if cfg.RelayTimeouts.HandshakeTimeout != 8*time.Second {
		t.Fatalf("HandshakeTimeout = %v", cfg.RelayTimeouts.HandshakeTimeout)
	}
	if cfg.RelayTimeouts.FrameTimeout != 4*time.Second {
		t.Fatalf("FrameTimeout = %v", cfg.RelayTimeouts.FrameTimeout)
	}
	if cfg.RelayTimeouts.IdleTimeout != 75*time.Second {
		t.Fatalf("IdleTimeout = %v", cfg.RelayTimeouts.IdleTimeout)
	}
}

func TestLoadFromEnvRejectsInvalidHTTPResilienceSettings(t *testing.T) {
	t.Setenv("NRE_MASTER_URL", "https://master.example.com")
	t.Setenv("NRE_AGENT_TOKEN", "secret")
	names := []string{
		"NRE_HTTP_STREAM_RESUME_MAX_ATTEMPTS",
		"NRE_HTTP_STREAM_RESUME_ENABLED",
		"NRE_HTTP_DIAL_TIMEOUT",
		"NRE_HTTP_MAX_CONNS_PER_HOST",
		"NRE_HTTP_SAME_BACKEND_RETRY_ATTEMPTS",
		"NRE_BACKEND_FAILURE_BACKOFF_BASE",
		"NRE_BACKEND_FAILURE_BACKOFF_LIMIT",
	}
	for _, name := range names {
		t.Setenv(name, "")
	}
	cases := []map[string]string{
		{"NRE_HTTP_STREAM_RESUME_MAX_ATTEMPTS": "0"},
		{"NRE_HTTP_STREAM_RESUME_ENABLED": "maybe"},
		{"NRE_HTTP_DIAL_TIMEOUT": "bogus"},
		{"NRE_HTTP_MAX_CONNS_PER_HOST": "0"},
		{"NRE_HTTP_SAME_BACKEND_RETRY_ATTEMPTS": "-1"},
		{"NRE_BACKEND_FAILURE_BACKOFF_BASE": "16s", "NRE_BACKEND_FAILURE_BACKOFF_LIMIT": "15s"},
	}

	for _, values := range cases {
		for _, name := range names {
			if err := os.Setenv(name, ""); err != nil {
				t.Fatal(err)
			}
		}
		for name, value := range values {
			if err := os.Setenv(name, value); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := LoadFromEnv(); err == nil {
			t.Fatalf("LoadFromEnv() accepted invalid settings %v", values)
		}
	}

	for _, name := range names {
		if err := os.Setenv(name, ""); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Setenv("NRE_HTTP_SAME_BACKEND_RETRY_ATTEMPTS", "0"); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadFromEnv()
	if err != nil || cfg.HTTPResilience.SameBackendRetryAttempts != 0 {
		t.Fatalf("zero same-backend retries = %d, %v", cfg.HTTPResilience.SameBackendRetryAttempts, err)
	}
}

func TestLoadFromEnvBackendFailureOverrideExplicitWhenProvidedAtDefaultValues(t *testing.T) {
	if Default().HasExplicitBackendFailureOverrides() {
		t.Fatal("default config unexpectedly marks backend failure overrides explicit")
	}
	t.Setenv("NRE_MASTER_URL", "https://master.example.com")
	t.Setenv("NRE_AGENT_TOKEN", "secret")
	t.Setenv("NRE_BACKEND_FAILURE_BACKOFF_BASE", "1s")
	t.Setenv("NRE_BACKEND_FAILURE_BACKOFF_LIMIT", "15s")

	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("LoadFromEnv() error = %v", err)
	}
	if !cfg.HasExplicitBackendFailureOverrides() {
		t.Fatal("expected backend failure overrides to be explicit")
	}
}

func TestLoadFromEnvDDNSPublicAPIURLs(t *testing.T) {
	t.Setenv("NRE_MASTER_URL", "https://master.example.com")
	t.Setenv("NRE_AGENT_TOKEN", "secret")
	t.Setenv("NRE_DDNS_IPV4_PUBLIC_API_URL", "https://v4.example.net/ip")
	t.Setenv("NRE_DDNS_IPV6_PUBLIC_API_URL", "  https://v6.example.net/ip  ")

	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("LoadFromEnv() error = %v", err)
	}
	if cfg.DDNS.IPv4PublicAPIURL != "https://v4.example.net/ip" {
		t.Fatalf("IPv4PublicAPIURL = %q", cfg.DDNS.IPv4PublicAPIURL)
	}
	if cfg.DDNS.IPv6PublicAPIURL != "https://v6.example.net/ip" {
		t.Fatalf("IPv6PublicAPIURL = %q", cfg.DDNS.IPv6PublicAPIURL)
	}

	if err := os.Setenv("NRE_DDNS_IPV4_PUBLIC_API_URL", ""); err != nil {
		t.Fatal(err)
	}
	if err := os.Setenv("NRE_DDNS_IPV6_PUBLIC_API_URL", ""); err != nil {
		t.Fatal(err)
	}
	cfg, err = LoadFromEnv()
	if err != nil {
		t.Fatalf("LoadFromEnv() error = %v", err)
	}
	if cfg.DDNS.IPv4PublicAPIURL != "" || cfg.DDNS.IPv6PublicAPIURL != "" {
		t.Fatalf("expected empty DDNS public API URLs, got %#v", cfg.DDNS)
	}

	if err := os.Setenv("NRE_DDNS_IPV4_PUBLIC_API_URL", "https://4.ipw.cn,https://api.ipify.org"); err != nil {
		t.Fatal(err)
	}
	cfg, err = LoadFromEnv()
	if err != nil {
		t.Fatalf("LoadFromEnv() error = %v", err)
	}
	if cfg.DDNS.IPv4PublicAPIURL != "https://4.ipw.cn,https://api.ipify.org" {
		t.Fatalf("IPv4PublicAPIURL = %q", cfg.DDNS.IPv4PublicAPIURL)
	}
}

func sumSHA256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
