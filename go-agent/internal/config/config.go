package config

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	defaultAgentID      = "linux-agent"
	defaultAgentName    = "linux-agent"
	defaultDataDir      = "/var/lib/nre-agent"
	defaultHeartbeat    = 10 * time.Second
	defaultAgentVersion = "0.0.0"
)

type Config struct {
	AgentID              string
	AgentName            string
	AgentToken           string
	MasterURL            string
	DataDir              string
	HeartbeatInterval    time.Duration
	HTTP3Enabled         bool
	CurrentVersion       string
	RuntimePackageSHA256 string
}

func Default() Config {
	return Config{
		AgentID:           defaultAgentID,
		AgentName:         defaultAgentName,
		DataDir:           defaultDataDir,
		HeartbeatInterval: defaultHeartbeat,
		CurrentVersion:    defaultAgentVersion,
	}
}

func LoadFromEnv() (Config, error) {
	return loadFromEnvForExecutable("")
}

func loadFromEnvForExecutable(executablePath string) (Config, error) {
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
	trimmed := strings.TrimRight(master, "/")
	if trimmed == "" {
		trimmed = "/"
	}
	cfg.MasterURL = trimmed

	token := strings.TrimSpace(os.Getenv("NRE_AGENT_TOKEN"))
	if token == "" {
		return Config{}, errors.New("NRE_AGENT_TOKEN is required")
	}
	cfg.AgentToken = token

	if val := strings.TrimSpace(os.Getenv("NRE_DATA_DIR")); val != "" {
		cfg.DataDir = val
	}
	if val := strings.TrimSpace(os.Getenv("NRE_HEARTBEAT_INTERVAL")); val != "" {
		dur, err := time.ParseDuration(val)
		if err != nil {
			return Config{}, fmt.Errorf("invalid NRE_HEARTBEAT_INTERVAL: %w", err)
		}
		if dur <= 0 {
			return Config{}, errors.New("NRE_HEARTBEAT_INTERVAL must be positive")
		}
		cfg.HeartbeatInterval = dur
	}
	if val := strings.TrimSpace(os.Getenv("NRE_HTTP3_ENABLED")); val != "" {
		enabled, err := strconv.ParseBool(val)
		if err != nil {
			return Config{}, fmt.Errorf("invalid NRE_HTTP3_ENABLED: %w", err)
		}
		cfg.HTTP3Enabled = enabled
	}

	cfg.RuntimePackageSHA256 = executableSHA256(executablePath)

	return cfg, nil
}

func executableSHA256(executablePath string) string {
	resolvedPath := strings.TrimSpace(executablePath)
	if resolvedPath == "" {
		path, err := os.Executable()
		if err != nil {
			return ""
		}
		resolvedPath = path
	}
	resolvedPath, err := filepath.EvalSymlinks(resolvedPath)
	if err != nil {
		resolvedPath = strings.TrimSpace(executablePath)
		if resolvedPath == "" {
			if fallback, fallbackErr := os.Executable(); fallbackErr == nil {
				resolvedPath = fallback
			}
		}
	}
	if strings.TrimSpace(resolvedPath) == "" {
		return ""
	}

	file, err := os.Open(resolvedPath)
	if err != nil {
		return ""
	}
	defer file.Close()

	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return ""
	}
	return hex.EncodeToString(hash.Sum(nil))
}
