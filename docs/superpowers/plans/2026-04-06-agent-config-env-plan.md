# Linux Agent Config Implementation Plan

I'm using the writing-plans skill to create the implementation plan.

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the bootstrap config stub with a production-ready Linux agent configuration loader driven by environment variables.

**Architecture:** The config package defines an enriched `Config` struct plus `LoadFromEnv`, tests drive every parsing and validation rule, and `cmd/nre-agent/main.go` boots the runtime with env-driven configuration.

**Tech Stack:** Go 1.x, the standard library, and `go test` as the verification harness.

---

### Task 1: Add env-driven config tests

**Files:**
- Modify: `go-agent/internal/config/config_test.go`

- [ ] **Step 1: Write the failing tests**

```go
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
    if cfg.MasterURL != "https://master.example.com" {
        t.Fatalf("expected trimmed master URL, got %q", cfg.MasterURL)
    }
    if cfg.HeartbeatInterval != 30*time.Second {
        t.Fatalf("expected default heartbeat, got %v", cfg.HeartbeatInterval)
    }
    if cfg.DataDir != "/tmp/nre-data" {
        t.Fatalf("expected data dir from env, got %q", cfg.DataDir)
    }
}

func TestLoadFromEnvRequiresMasterURLAndToken(t *testing.T) {
    t.Setenv("NRE_AGENT_TOKEN", "secret")
    if _, err := LoadFromEnv(); err == nil {
        t.Fatal("expected failure when NRE_MASTER_URL missing")
    }

    t.Setenv("NRE_MASTER_URL", "https://master.example.com")
    t.Setenv("NRE_AGENT_TOKEN", "")
    if _, err := LoadFromEnv(); err == nil {
        t.Fatal("expected failure when NRE_AGENT_TOKEN missing")
    }
}
```

- [ ] **Step 2: Run the test to confirm RED**

Run: `cd go-agent && go test ./internal/config -run TestLoadFromEnv -v`

Expected: FAIL because `LoadFromEnv` is not implemented yet.

### Task 2: Implement Config struct and env loader

**Files:**
- Modify: `go-agent/internal/config/config.go`

- [ ] **Step 1: Introduce the rich Config, defaults, and `LoadFromEnv`**

```go
type Config struct {
    AgentID           string
    AgentName         string
    AgentToken        string
    MasterURL         string
    DataDir           string
    HeartbeatInterval time.Duration
    CurrentVersion    string
}

const (
    defaultDataDir       = "/var/lib/nre-agent"
    defaultHeartbeat     = 30 * time.Second
    defaultAgentID       = "linux-agent"
    defaultCurrentVersion = "0.0.0"
)

func Default() Config {
    return Config{
        AgentID:           defaultAgentID,
        AgentName:         defaultAgentID,
        DataDir:           defaultDataDir,
        HeartbeatInterval: defaultHeartbeat,
        CurrentVersion:    defaultCurrentVersion,
    }
}

func LoadFromEnv() (Config, error) {
    cfg := Default()

    if val := strings.TrimSpace(os.Getenv("NRE_AGENT_ID")); val != "" {
        cfg.AgentID = val
    }
    if val := strings.TrimSpace(os.Getenv("NRE_AGENT_NAME")); val != "" {
        cfg.AgentName = val
    }
    if val := strings.TrimSpace(os.Getenv("NRE_AGENT_VERSION")); val != "" {
        cfg.CurrentVersion = val
    }

    master := strings.TrimSpace(os.Getenv("NRE_MASTER_URL"))
    if master == "" {
        return Config{}, errors.New("NRE_MASTER_URL is required")
    }
    cfg.MasterURL = strings.TrimRight(master, "/")

    token := strings.TrimSpace(os.Getenv("NRE_AGENT_TOKEN"))
    if token == "" {
        return Config{}, errors.New("NRE_AGENT_TOKEN is required")
    }
    cfg.AgentToken = token

    if val := strings.TrimSpace(os.Getenv("NRE_DATA_DIR")); val != "" {
        cfg.DataDir = val
    }

    if val := strings.TrimSpace(os.Getenv("NRE_HEARTBEAT_INTERVAL")); val != "" {
        parsed, err := time.ParseDuration(val)
        if err != nil {
            return Config{}, fmt.Errorf("invalid NRE_HEARTBEAT_INTERVAL: %w", err)
        }
        cfg.HeartbeatInterval = parsed
    }

    return cfg, nil
}
```

- [ ] **Step 2: Run the test to confirm GREEN**

Run: `cd go-agent && go test ./internal/config -run TestLoadFromEnv -v`

Expected: PASS

### Task 3: Bootstrap with env-driven config

**Files:**
- Modify: `go-agent/cmd/nre-agent/main.go`

- [ ] **Step 1: Load config via `config.LoadFromEnv()` instead of `struct.Default()` and `log.Fatal` on error**

- [ ] **Step 2: Re-run `cd go-agent && go test ./internal/config -run TestLoadFromEnv -v` to keep tests green**

### Task 4: Commit the work

**Files:**
- `docs/superpowers/plans/2026-04-06-agent-config-env-plan.md`
- `go-agent/internal/config/config.go`
- `go-agent/internal/config/config_test.go`
- `go-agent/cmd/nre-agent/main.go`

- [ ] **Step 1: Inspect git status**

Run: `git status -sb`

- [ ] **Step 2: Commit**

Run: `git commit -am "feat(go-agent): load config from env"`

Plan complete and saved to `docs/superpowers/plans/2026-04-06-agent-config-env-plan.md`. Two execution options:
1. Subagent-Driven (recommended) - dispatch new subagents per task with review checkpoints.
2. Inline Execution - continue working in this session using superpowers:executing-plans.
I will follow Inline Execution; no subagent dispatch.
