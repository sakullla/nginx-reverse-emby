package config

import (
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
