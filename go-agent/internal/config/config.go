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
	AgentID                 string
	AgentName               string
	AgentToken              string
	MasterURL               string
	DataDir                 string
	HeartbeatInterval       time.Duration
	HTTPTransport           HTTPTransportConfig
	HTTPResilience          HTTPResilienceConfig
	BackendFailures         BackendFailureConfig
	BackendFailuresExplicit bool
	RelayTimeouts           RelayTimeoutConfig
	HTTP3Enabled            bool
	CurrentVersion          string
	RuntimePackageSHA256    string
}

type HTTPTransportConfig struct {
	DialTimeout           time.Duration
	TLSHandshakeTimeout   time.Duration
	ResponseHeaderTimeout time.Duration
	IdleConnTimeout       time.Duration
	KeepAlive             time.Duration
}

type HTTPResilienceConfig struct {
	ResumeEnabled            bool
	ResumeMaxAttempts        int
	SameBackendRetryAttempts int
}

type BackendFailureConfig struct {
	BackoffBase  time.Duration
	BackoffLimit time.Duration
}

type RelayTimeoutConfig struct {
	DialTimeout      time.Duration
	HandshakeTimeout time.Duration
	FrameTimeout     time.Duration
	IdleTimeout      time.Duration
}

func Default() Config {
	return Config{
		AgentID:           defaultAgentID,
		AgentName:         defaultAgentName,
		DataDir:           defaultDataDir,
		HeartbeatInterval: defaultHeartbeat,
		HTTPTransport: HTTPTransportConfig{
			DialTimeout:           30 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: 30 * time.Second,
			IdleConnTimeout:       90 * time.Second,
			KeepAlive:             30 * time.Second,
		},
		HTTPResilience: HTTPResilienceConfig{
			ResumeEnabled:            true,
			ResumeMaxAttempts:        2,
			SameBackendRetryAttempts: 1,
		},
		BackendFailures: BackendFailureConfig{
			BackoffBase:  1 * time.Second,
			BackoffLimit: 15 * time.Second,
		},
		RelayTimeouts: RelayTimeoutConfig{
			DialTimeout:      5 * time.Second,
			HandshakeTimeout: 5 * time.Second,
			FrameTimeout:     5 * time.Second,
			IdleTimeout:      2 * time.Minute,
		},
		CurrentVersion: defaultAgentVersion,
	}
}

func LoadFromEnv() (Config, error) {
	return loadFromEnvForExecutable("")
}

func (c Config) HasExplicitBackendFailureOverrides() bool {
	return c.BackendFailuresExplicit
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
	if val := strings.TrimSpace(os.Getenv("NRE_HTTP_DIAL_TIMEOUT")); val != "" {
		dur, err := parsePositiveDurationEnv("NRE_HTTP_DIAL_TIMEOUT", val)
		if err != nil {
			return Config{}, err
		}
		cfg.HTTPTransport.DialTimeout = dur
	}
	if val := strings.TrimSpace(os.Getenv("NRE_HTTP_TLS_HANDSHAKE_TIMEOUT")); val != "" {
		dur, err := parsePositiveDurationEnv("NRE_HTTP_TLS_HANDSHAKE_TIMEOUT", val)
		if err != nil {
			return Config{}, err
		}
		cfg.HTTPTransport.TLSHandshakeTimeout = dur
	}
	if val := strings.TrimSpace(os.Getenv("NRE_HTTP_RESPONSE_HEADER_TIMEOUT")); val != "" {
		dur, err := parsePositiveDurationEnv("NRE_HTTP_RESPONSE_HEADER_TIMEOUT", val)
		if err != nil {
			return Config{}, err
		}
		cfg.HTTPTransport.ResponseHeaderTimeout = dur
	}
	if val := strings.TrimSpace(os.Getenv("NRE_HTTP_IDLE_CONN_TIMEOUT")); val != "" {
		dur, err := parsePositiveDurationEnv("NRE_HTTP_IDLE_CONN_TIMEOUT", val)
		if err != nil {
			return Config{}, err
		}
		cfg.HTTPTransport.IdleConnTimeout = dur
	}
	if val := strings.TrimSpace(os.Getenv("NRE_HTTP_KEEP_ALIVE")); val != "" {
		dur, err := parsePositiveDurationEnv("NRE_HTTP_KEEP_ALIVE", val)
		if err != nil {
			return Config{}, err
		}
		cfg.HTTPTransport.KeepAlive = dur
	}
	if val := strings.TrimSpace(os.Getenv("NRE_HTTP_STREAM_RESUME_ENABLED")); val != "" {
		enabled, err := strconv.ParseBool(val)
		if err != nil {
			return Config{}, fmt.Errorf("invalid NRE_HTTP_STREAM_RESUME_ENABLED: %w", err)
		}
		cfg.HTTPResilience.ResumeEnabled = enabled
	}
	if val := strings.TrimSpace(os.Getenv("NRE_HTTP_STREAM_RESUME_MAX_ATTEMPTS")); val != "" {
		attempts, err := parsePositiveIntEnv("NRE_HTTP_STREAM_RESUME_MAX_ATTEMPTS", val)
		if err != nil {
			return Config{}, err
		}
		cfg.HTTPResilience.ResumeMaxAttempts = attempts
	}
	if val := strings.TrimSpace(os.Getenv("NRE_HTTP_SAME_BACKEND_RETRY_ATTEMPTS")); val != "" {
		attempts, err := parseNonNegativeIntEnv("NRE_HTTP_SAME_BACKEND_RETRY_ATTEMPTS", val)
		if err != nil {
			return Config{}, err
		}
		cfg.HTTPResilience.SameBackendRetryAttempts = attempts
	}
	if val := strings.TrimSpace(os.Getenv("NRE_BACKEND_FAILURE_BACKOFF_BASE")); val != "" {
		dur, err := parsePositiveDurationEnv("NRE_BACKEND_FAILURE_BACKOFF_BASE", val)
		if err != nil {
			return Config{}, err
		}
		cfg.BackendFailures.BackoffBase = dur
		cfg.BackendFailuresExplicit = true
	}
	if val := strings.TrimSpace(os.Getenv("NRE_BACKEND_FAILURE_BACKOFF_LIMIT")); val != "" {
		dur, err := parsePositiveDurationEnv("NRE_BACKEND_FAILURE_BACKOFF_LIMIT", val)
		if err != nil {
			return Config{}, err
		}
		cfg.BackendFailures.BackoffLimit = dur
		cfg.BackendFailuresExplicit = true
	}
	if val := strings.TrimSpace(os.Getenv("NRE_RELAY_DIAL_TIMEOUT")); val != "" {
		dur, err := parsePositiveDurationEnv("NRE_RELAY_DIAL_TIMEOUT", val)
		if err != nil {
			return Config{}, err
		}
		cfg.RelayTimeouts.DialTimeout = dur
	}
	if val := strings.TrimSpace(os.Getenv("NRE_RELAY_HANDSHAKE_TIMEOUT")); val != "" {
		dur, err := parsePositiveDurationEnv("NRE_RELAY_HANDSHAKE_TIMEOUT", val)
		if err != nil {
			return Config{}, err
		}
		cfg.RelayTimeouts.HandshakeTimeout = dur
	}
	if val := strings.TrimSpace(os.Getenv("NRE_RELAY_FRAME_TIMEOUT")); val != "" {
		dur, err := parsePositiveDurationEnv("NRE_RELAY_FRAME_TIMEOUT", val)
		if err != nil {
			return Config{}, err
		}
		cfg.RelayTimeouts.FrameTimeout = dur
	}
	if val := strings.TrimSpace(os.Getenv("NRE_RELAY_IDLE_TIMEOUT")); val != "" {
		dur, err := parsePositiveDurationEnv("NRE_RELAY_IDLE_TIMEOUT", val)
		if err != nil {
			return Config{}, err
		}
		cfg.RelayTimeouts.IdleTimeout = dur
	}
	if cfg.BackendFailures.BackoffBase > cfg.BackendFailures.BackoffLimit {
		return Config{}, errors.New("NRE_BACKEND_FAILURE_BACKOFF_BASE must be less than or equal to NRE_BACKEND_FAILURE_BACKOFF_LIMIT")
	}

	cfg.RuntimePackageSHA256 = executableSHA256(executablePath)

	return cfg, nil
}

func parsePositiveDurationEnv(name, value string) (time.Duration, error) {
	dur, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("invalid %s: %w", name, err)
	}
	if dur <= 0 {
		return 0, fmt.Errorf("%s must be positive", name)
	}
	return dur, nil
}

func parsePositiveIntEnv(name, value string) (int, error) {
	num, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("invalid %s: %w", name, err)
	}
	if num <= 0 {
		return 0, fmt.Errorf("%s must be positive", name)
	}
	return num, nil
}

func parseNonNegativeIntEnv(name, value string) (int, error) {
	num, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("invalid %s: %w", name, err)
	}
	if num < 0 {
		return 0, fmt.Errorf("%s must be non-negative", name)
	}
	return num, nil
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
