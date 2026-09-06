package model

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	stdruntime "runtime"
	"strconv"
	"strings"
	"time"
)

const (
	defaultAgentID      = "linux-agent"
	defaultAgentName    = "linux-agent"
	defaultDataDir      = "/var/lib/nre-agent"
	defaultHeartbeat    = 10 * time.Second
	defaultDDNSIPProbe  = 5 * time.Minute
	defaultAgentVersion = "0.0.0"

	DefaultCapabilityAuditQueueSize     = 256
	DefaultCapabilityAuditBatchSize     = 32
	DefaultCapabilityAuditFlushInterval = 250 * time.Millisecond
	DefaultCapabilityAuditRetention     = 24 * time.Hour
	DefaultCapabilityAuditMaxBytes      = int64(16 << 20)
	DefaultCapabilityAuditMinFreeBytes  = uint64(64 << 20)
	DefaultCapabilityAuditCloseTimeout  = 2 * time.Second
)

type Config struct {
	AgentID                 string
	AgentName               string
	AgentToken              string
	MasterURL               string
	DataDir                 string
	TrafficInterfaces       []string
	HeartbeatInterval       time.Duration
	HTTPTransport           HTTPTransportConfig
	HTTPResilience          HTTPResilienceConfig
	BackendFailures         BackendFailureConfig
	BackendFailuresExplicit bool
	RelayTimeouts           RelayTimeoutConfig
	HTTP3Enabled            bool
	TrafficStatsEnabled     bool
	TrafficStatsExplicit    bool
	DDNS                    DDNSRuntimeConfig
	CurrentVersion          string
	RuntimePackageSHA256    string
	CapabilityAudit         CapabilityAuditConfig
}

type CapabilityAuditConfig struct {
	Enabled       bool
	QueueSize     int
	BatchSize     int
	FlushInterval time.Duration
	Retention     time.Duration
	MaxBytes      int64
	MinFreeBytes  uint64
	CloseTimeout  time.Duration
}

func DefaultCapabilityAuditConfig() CapabilityAuditConfig {
	return CapabilityAuditConfig{
		QueueSize: DefaultCapabilityAuditQueueSize, BatchSize: DefaultCapabilityAuditBatchSize,
		FlushInterval: DefaultCapabilityAuditFlushInterval, Retention: DefaultCapabilityAuditRetention,
		MaxBytes: DefaultCapabilityAuditMaxBytes, MinFreeBytes: DefaultCapabilityAuditMinFreeBytes,
		CloseTimeout: DefaultCapabilityAuditCloseTimeout,
	}
}

func NormalizeCapabilityAuditConfig(cfg CapabilityAuditConfig) CapabilityAuditConfig {
	defaults := DefaultCapabilityAuditConfig()
	if cfg.QueueSize == 0 {
		cfg.QueueSize = defaults.QueueSize
	}
	if cfg.BatchSize == 0 {
		cfg.BatchSize = defaults.BatchSize
	}
	if cfg.FlushInterval == 0 {
		cfg.FlushInterval = defaults.FlushInterval
	}
	if cfg.Retention == 0 {
		cfg.Retention = defaults.Retention
	}
	if cfg.MaxBytes == 0 {
		cfg.MaxBytes = defaults.MaxBytes
	}
	if cfg.MinFreeBytes == 0 {
		cfg.MinFreeBytes = defaults.MinFreeBytes
	}
	if cfg.CloseTimeout == 0 {
		cfg.CloseTimeout = defaults.CloseTimeout
	}
	return cfg
}

func (cfg CapabilityAuditConfig) Validate() error {
	if cfg.QueueSize < 1 || cfg.QueueSize > 65_536 {
		return errors.New("capability audit queue size must be between 1 and 65536")
	}
	if cfg.BatchSize < 1 || cfg.BatchSize > 1_024 || cfg.BatchSize > cfg.QueueSize {
		return errors.New("capability audit batch size must be between 1 and queue size, with a maximum of 1024")
	}
	if cfg.FlushInterval < 10*time.Millisecond || cfg.FlushInterval > time.Minute {
		return errors.New("capability audit flush interval must be between 10ms and 1m")
	}
	if cfg.Retention < time.Minute || cfg.Retention > 30*24*time.Hour {
		return errors.New("capability audit retention must be between 1m and 720h")
	}
	if cfg.MaxBytes < 64<<10 || cfg.MaxBytes > 1<<30 {
		return errors.New("capability audit max bytes must be between 65536 and 1073741824")
	}
	if cfg.MinFreeBytes < 1<<20 || cfg.MinFreeBytes > 1<<40 {
		return errors.New("capability audit minimum free bytes must be between 1048576 and 1099511627776")
	}
	if cfg.CloseTimeout < 10*time.Millisecond || cfg.CloseTimeout > 30*time.Second {
		return errors.New("capability audit close timeout must be between 10ms and 30s")
	}
	return nil
}

// DDNSRuntimeConfig holds agent-local DDNS extraction overrides. These are
// runtime/transport knobs (not security-sensitive), distinct from the
// master-dispatched per-agent DDNSExtractConfig. Empty values fall back to the
// DDNS module's built-in public echo endpoints.
type DDNSRuntimeConfig struct {
	IPv4PublicAPIURL string
	IPv6PublicAPIURL string
	IPProbeInterval  time.Duration
}

type HTTPTransportConfig struct {
	DialTimeout           time.Duration
	TLSHandshakeTimeout   time.Duration
	ResponseHeaderTimeout time.Duration
	IdleConnTimeout       time.Duration
	KeepAlive             time.Duration
	MaxConnsPerHost       int
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
			MaxConnsPerHost:       64,
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
		TrafficStatsEnabled: true,
		DDNS: DDNSRuntimeConfig{
			IPProbeInterval: defaultDDNSIPProbe,
		},
		CurrentVersion:  defaultAgentVersion,
		CapabilityAudit: DefaultCapabilityAuditConfig(),
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
	var err error
	if cfg.CapabilityAudit, err = loadCapabilityAuditConfigFromEnv(cfg.CapabilityAudit, "NRE_PLUGIN_CAPABILITY_AUDIT_"); err != nil {
		return Config{}, err
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
	if val := strings.TrimSpace(os.Getenv("NRE_TRAFFIC_STATS_ENABLED")); val != "" {
		enabled, err := strconv.ParseBool(val)
		if err != nil {
			return Config{}, fmt.Errorf("invalid NRE_TRAFFIC_STATS_ENABLED: %w", err)
		}
		cfg.TrafficStatsEnabled = enabled
		cfg.TrafficStatsExplicit = true
	}
	if val := strings.TrimSpace(os.Getenv("NRE_TRAFFIC_INTERFACES")); val != "" {
		cfg.TrafficInterfaces = parseTrafficInterfaces(val)
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
	if val := strings.TrimSpace(os.Getenv("NRE_HTTP_MAX_CONNS_PER_HOST")); val != "" {
		maxConns, err := parsePositiveIntEnv("NRE_HTTP_MAX_CONNS_PER_HOST", val)
		if err != nil {
			return Config{}, err
		}
		cfg.HTTPTransport.MaxConnsPerHost = maxConns
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
	if val := strings.TrimSpace(os.Getenv("NRE_DDNS_IPV4_PUBLIC_API_URL")); val != "" {
		cfg.DDNS.IPv4PublicAPIURL = val
	}
	if val := strings.TrimSpace(os.Getenv("NRE_DDNS_IPV6_PUBLIC_API_URL")); val != "" {
		cfg.DDNS.IPv6PublicAPIURL = val
	}
	if val := strings.TrimSpace(os.Getenv("NRE_DDNS_IP_PROBE_INTERVAL")); val != "" {
		dur, err := parsePositiveDurationEnv("NRE_DDNS_IP_PROBE_INTERVAL", val)
		if err != nil {
			return Config{}, err
		}
		cfg.DDNS.IPProbeInterval = dur
	}
	if cfg.BackendFailures.BackoffBase > cfg.BackendFailures.BackoffLimit {
		return Config{}, errors.New("NRE_BACKEND_FAILURE_BACKOFF_BASE must be less than or equal to NRE_BACKEND_FAILURE_BACKOFF_LIMIT")
	}

	cfg.RuntimePackageSHA256 = RunningExecutableSHA256(executablePath)

	return cfg, nil
}

func loadCapabilityAuditConfigFromEnv(cfg CapabilityAuditConfig, prefix string) (CapabilityAuditConfig, error) {
	parseDuration := func(suffix string, target *time.Duration) error {
		name := prefix + suffix
		value, present := os.LookupEnv(name)
		if !present {
			return nil
		}
		duration, err := time.ParseDuration(value)
		if err != nil {
			return fmt.Errorf("invalid %s: %w", name, err)
		}
		*target = duration
		return nil
	}
	parseInt := func(suffix string, target *int) error {
		name := prefix + suffix
		value, present := os.LookupEnv(name)
		if !present {
			return nil
		}
		parsed, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("invalid %s: %w", name, err)
		}
		*target = parsed
		return nil
	}
	if value, present := os.LookupEnv(prefix + "ENABLED"); present {
		switch value {
		case "true":
			cfg.Enabled = true
		case "false":
			cfg.Enabled = false
		default:
			return CapabilityAuditConfig{}, fmt.Errorf("invalid %sENABLED: expected true or false", prefix)
		}
	}
	if err := parseInt("QUEUE_SIZE", &cfg.QueueSize); err != nil {
		return CapabilityAuditConfig{}, err
	}
	if err := parseInt("BATCH_SIZE", &cfg.BatchSize); err != nil {
		return CapabilityAuditConfig{}, err
	}
	if err := parseDuration("FLUSH_INTERVAL", &cfg.FlushInterval); err != nil {
		return CapabilityAuditConfig{}, err
	}
	if err := parseDuration("RETENTION", &cfg.Retention); err != nil {
		return CapabilityAuditConfig{}, err
	}
	if err := parseDuration("CLOSE_TIMEOUT", &cfg.CloseTimeout); err != nil {
		return CapabilityAuditConfig{}, err
	}
	if value, present := os.LookupEnv(prefix + "MAX_BYTES"); present {
		parsed, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return CapabilityAuditConfig{}, fmt.Errorf("invalid %sMAX_BYTES: %w", prefix, err)
		}
		cfg.MaxBytes = parsed
	}
	if value, present := os.LookupEnv(prefix + "MIN_FREE_BYTES"); present {
		parsed, err := strconv.ParseUint(value, 10, 64)
		if err != nil {
			return CapabilityAuditConfig{}, fmt.Errorf("invalid %sMIN_FREE_BYTES: %w", prefix, err)
		}
		cfg.MinFreeBytes = parsed
	}
	if err := cfg.Validate(); err != nil {
		return CapabilityAuditConfig{}, fmt.Errorf("invalid %scapability audit config: %w", prefix, err)
	}
	return cfg, nil
}

// RunningExecutableSHA256 hashes the process image that is actually running.
// On Linux that is /proc/self/exe, so replacing the on-disk install path
// during a staged upgrade cannot make heartbeats claim the candidate digest.
func RunningExecutableSHA256(executablePath string) string {
	return executableSHA256(executablePath)
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

func parseTrafficInterfaces(value string) []string {
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	seen := map[string]struct{}{}
	for _, part := range parts {
		name := strings.TrimSpace(part)
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	return out
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
	if digest := hashFile(runningImagePath()); digest != "" {
		return digest
	}
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
	return hashFile(resolvedPath)
}

func runningImagePath() string {
	if stdruntime.GOOS == "linux" {
		return "/proc/self/exe"
	}
	return ""
}

func hashFile(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	file, err := os.Open(path)
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
