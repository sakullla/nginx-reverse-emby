package config

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
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

func sumSHA256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
