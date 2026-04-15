package config

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
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

func TestLoadFromEnvHTTP3EnabledDefaultsFalse(t *testing.T) {
	t.Setenv("NRE_MASTER_URL", "https://master.example.com")
	t.Setenv("NRE_AGENT_TOKEN", "secret")

	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("LoadFromEnv() error = %v", err)
	}
	if cfg.HTTP3Enabled {
		t.Fatal("expected HTTP3Enabled to default to false")
	}
}

func TestLoadFromEnvHTTP3EnabledParsesTrue(t *testing.T) {
	t.Setenv("NRE_MASTER_URL", "https://master.example.com")
	t.Setenv("NRE_AGENT_TOKEN", "secret")
	t.Setenv("NRE_HTTP3_ENABLED", "true")

	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("LoadFromEnv() error = %v", err)
	}
	if !cfg.HTTP3Enabled {
		t.Fatal("expected HTTP3Enabled to be true")
	}
}

func TestLoadFromEnvRejectsInvalidHTTP3Enabled(t *testing.T) {
	t.Setenv("NRE_MASTER_URL", "https://master.example.com")
	t.Setenv("NRE_AGENT_TOKEN", "secret")
	t.Setenv("NRE_HTTP3_ENABLED", "maybe")

	_, err := LoadFromEnv()
	if err == nil {
		t.Fatal("expected invalid NRE_HTTP3_ENABLED error")
	}
	if !strings.Contains(err.Error(), "NRE_HTTP3_ENABLED") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadFromEnvComputesRuntimePackageSHA256FromExecutable(t *testing.T) {
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
}

func TestLoadFromEnvLeavesRuntimePackageSHA256EmptyWhenExecutableMissing(t *testing.T) {
	t.Setenv("NRE_MASTER_URL", "https://master.example.com")
	t.Setenv("NRE_AGENT_TOKEN", "secret")

	cfg, err := loadFromEnvForExecutable(filepath.Join(t.TempDir(), "missing-agent"))
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
	t.Setenv("NRE_HTTP_STREAM_RESUME_MAX_ATTEMPTS", "0")

	if _, err := LoadFromEnv(); err == nil {
		t.Fatal("expected error for zero resume attempts")
	}
}

func TestLoadFromEnvUsesDefaultNetworkResilienceConfigWhenUnset(t *testing.T) {
	t.Setenv("NRE_MASTER_URL", "https://master.example.com")
	t.Setenv("NRE_AGENT_TOKEN", "secret")

	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("LoadFromEnv() error = %v", err)
	}
	if cfg.HTTPTransport.DialTimeout != 30*time.Second {
		t.Fatalf("DialTimeout = %v", cfg.HTTPTransport.DialTimeout)
	}
	if cfg.HTTPTransport.TLSHandshakeTimeout != 10*time.Second {
		t.Fatalf("TLSHandshakeTimeout = %v", cfg.HTTPTransport.TLSHandshakeTimeout)
	}
	if cfg.HTTPTransport.ResponseHeaderTimeout != 30*time.Second {
		t.Fatalf("ResponseHeaderTimeout = %v", cfg.HTTPTransport.ResponseHeaderTimeout)
	}
	if cfg.HTTPTransport.IdleConnTimeout != 90*time.Second {
		t.Fatalf("IdleConnTimeout = %v", cfg.HTTPTransport.IdleConnTimeout)
	}
	if cfg.HTTPTransport.KeepAlive != 30*time.Second {
		t.Fatalf("KeepAlive = %v", cfg.HTTPTransport.KeepAlive)
	}
	if !cfg.HTTPResilience.ResumeEnabled {
		t.Fatal("expected ResumeEnabled to default true")
	}
	if cfg.HTTPResilience.ResumeMaxAttempts != 2 {
		t.Fatalf("ResumeMaxAttempts = %d", cfg.HTTPResilience.ResumeMaxAttempts)
	}
	if cfg.HTTPResilience.SameBackendRetryAttempts != 1 {
		t.Fatalf("SameBackendRetryAttempts = %d", cfg.HTTPResilience.SameBackendRetryAttempts)
	}
	if cfg.BackendFailures.BackoffBase != 1*time.Second {
		t.Fatalf("BackoffBase = %v", cfg.BackendFailures.BackoffBase)
	}
	if cfg.BackendFailures.BackoffLimit != 15*time.Second {
		t.Fatalf("BackoffLimit = %v", cfg.BackendFailures.BackoffLimit)
	}
	if cfg.RelayTimeouts.DialTimeout != 5*time.Second {
		t.Fatalf("DialTimeout = %v", cfg.RelayTimeouts.DialTimeout)
	}
	if cfg.RelayTimeouts.HandshakeTimeout != 5*time.Second {
		t.Fatalf("HandshakeTimeout = %v", cfg.RelayTimeouts.HandshakeTimeout)
	}
	if cfg.RelayTimeouts.FrameTimeout != 5*time.Second {
		t.Fatalf("FrameTimeout = %v", cfg.RelayTimeouts.FrameTimeout)
	}
	if cfg.RelayTimeouts.IdleTimeout != 2*time.Minute {
		t.Fatalf("IdleTimeout = %v", cfg.RelayTimeouts.IdleTimeout)
	}
}

func TestLoadFromEnvRejectsInvalidHTTPStreamResumeEnabled(t *testing.T) {
	t.Setenv("NRE_MASTER_URL", "https://master.example.com")
	t.Setenv("NRE_AGENT_TOKEN", "secret")
	t.Setenv("NRE_HTTP_STREAM_RESUME_ENABLED", "maybe")

	_, err := LoadFromEnv()
	if err == nil {
		t.Fatal("expected invalid NRE_HTTP_STREAM_RESUME_ENABLED error")
	}
	if !strings.Contains(err.Error(), "NRE_HTTP_STREAM_RESUME_ENABLED") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadFromEnvRejectsInvalidHTTPDialTimeout(t *testing.T) {
	t.Setenv("NRE_MASTER_URL", "https://master.example.com")
	t.Setenv("NRE_AGENT_TOKEN", "secret")
	t.Setenv("NRE_HTTP_DIAL_TIMEOUT", "bogus")

	_, err := LoadFromEnv()
	if err == nil {
		t.Fatal("expected invalid NRE_HTTP_DIAL_TIMEOUT error")
	}
	if !strings.Contains(err.Error(), "NRE_HTTP_DIAL_TIMEOUT") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadFromEnvRejectsInvalidSameBackendRetryAttempts(t *testing.T) {
	t.Setenv("NRE_MASTER_URL", "https://master.example.com")
	t.Setenv("NRE_AGENT_TOKEN", "secret")
	t.Setenv("NRE_HTTP_SAME_BACKEND_RETRY_ATTEMPTS", "0")

	_, err := LoadFromEnv()
	if err == nil {
		t.Fatal("expected error for non-positive same backend retry attempts")
	}
	if !strings.Contains(err.Error(), "NRE_HTTP_SAME_BACKEND_RETRY_ATTEMPTS") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadFromEnvRejectsBackendFailureBackoffBaseGreaterThanLimit(t *testing.T) {
	t.Setenv("NRE_MASTER_URL", "https://master.example.com")
	t.Setenv("NRE_AGENT_TOKEN", "secret")
	t.Setenv("NRE_BACKEND_FAILURE_BACKOFF_BASE", "16s")
	t.Setenv("NRE_BACKEND_FAILURE_BACKOFF_LIMIT", "15s")

	if _, err := LoadFromEnv(); err == nil {
		t.Fatal("expected error when backoff base is greater than limit")
	}
}

func TestLoadFromEnvBackendFailureOverrideExplicitWhenProvidedAtDefaultValues(t *testing.T) {
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

func TestDefaultConfigBackendFailureOverrideNotExplicit(t *testing.T) {
	if Default().HasExplicitBackendFailureOverrides() {
		t.Fatal("expected default config to have no explicit backend failure overrides")
	}
}

func sumSHA256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
