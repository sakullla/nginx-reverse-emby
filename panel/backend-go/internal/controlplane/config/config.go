package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	defaultListenAddr        = "0.0.0.0:8080"
	defaultDataDir           = "/opt/nginx-reverse-emby/panel/data"
	defaultFrontendDistDir   = "/opt/nginx-reverse-emby/panel/frontend/dist"
	defaultPublicAssetsDir   = "/opt/nginx-reverse-emby/panel/public/agent-assets"
	defaultEnableLocalAgent  = true
	defaultLocalAgentID      = "local"
	defaultLocalAgentName    = "local"
	defaultHeartbeatInterval = 30 * time.Second
	defaultManagedCertRenew  = 24 * time.Hour
)

type Config struct {
	ListenAddr                        string
	DataDir                           string
	PanelToken                        string
	RegisterToken                     string
	FrontendDistDir                   string
	PublicAgentAssetsDir              string
	EnableLocalAgent                  bool
	LocalAgentID                      string
	LocalAgentName                    string
	HeartbeatInterval                 time.Duration
	LocalAgentHTTP3Enabled            bool
	LocalAgentHTTPTransport           HTTPTransportConfig
	LocalAgentHTTPResilience          HTTPResilienceConfig
	LocalAgentBackendFailures         BackendFailureConfig
	LocalAgentBackendFailuresExplicit bool
	LocalAgentRelayTimeouts           RelayTimeoutConfig
	LocalAgentTrafficStatsEnabled     bool
	LocalAgentTrafficStatsExplicit    bool
	ManagedCertificateRenewInterval   time.Duration
	ManagedDNSCertificatesEnabled     bool
	AppVersion                        string
	BuildTime                         string
	GoVersion                         string
	ProjectURL                        string
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
		ListenAddr:           defaultListenAddr,
		DataDir:              defaultDataDir,
		FrontendDistDir:      defaultFrontendDistDir,
		PublicAgentAssetsDir: defaultPublicAssetsDir,
		EnableLocalAgent:     defaultEnableLocalAgent,
		LocalAgentID:         defaultLocalAgentID,
		LocalAgentName:       defaultLocalAgentName,
		HeartbeatInterval:    defaultHeartbeatInterval,
		LocalAgentHTTPTransport: HTTPTransportConfig{
			DialTimeout:           30 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: 30 * time.Second,
			IdleConnTimeout:       90 * time.Second,
			KeepAlive:             30 * time.Second,
		},
		LocalAgentHTTPResilience: HTTPResilienceConfig{
			ResumeEnabled:            true,
			ResumeMaxAttempts:        2,
			SameBackendRetryAttempts: 1,
		},
		LocalAgentBackendFailures: BackendFailureConfig{
			BackoffBase:  1 * time.Second,
			BackoffLimit: 15 * time.Second,
		},
		LocalAgentRelayTimeouts: RelayTimeoutConfig{
			DialTimeout:      5 * time.Second,
			HandshakeTimeout: 5 * time.Second,
			FrameTimeout:     5 * time.Second,
			IdleTimeout:      2 * time.Minute,
		},
		LocalAgentTrafficStatsEnabled:   true,
		ManagedCertificateRenewInterval: defaultManagedCertRenew,
	}
}

func LoadFromEnv() (Config, error) {
	cfg := Default()

	if val := strings.TrimSpace(firstEnv("NRE_CONTROL_PLANE_ADDR", "")); val != "" {
		cfg.ListenAddr = val
	} else {
		host := strings.TrimSpace(firstEnv("PANEL_BACKEND_HOST", ""))
		port := strings.TrimSpace(firstEnv("PANEL_BACKEND_PORT", ""))
		if host != "" || port != "" {
			if host == "" {
				host = "127.0.0.1"
			}
			if port == "" {
				port = "8080"
			}
			cfg.ListenAddr = fmt.Sprintf("%s:%s", host, port)
		}
	}
	if val := strings.TrimSpace(firstEnv("NRE_CONTROL_PLANE_DATA_DIR", "PANEL_DATA_ROOT")); val != "" {
		cfg.DataDir = val
	}

	panelToken := strings.TrimSpace(firstEnv("NRE_PANEL_TOKEN", "API_TOKEN"))
	if panelToken == "" {
		return Config{}, errors.New("NRE_PANEL_TOKEN is required")
	}
	cfg.PanelToken = panelToken

	registerToken := strings.TrimSpace(firstEnv("NRE_REGISTER_TOKEN", "MASTER_REGISTER_TOKEN", "PANEL_REGISTER_TOKEN", "API_TOKEN"))
	if registerToken == "" {
		return Config{}, errors.New("NRE_REGISTER_TOKEN is required")
	}
	cfg.RegisterToken = registerToken

	frontendDistDir := strings.TrimSpace(firstEnv("NRE_FRONTEND_DIST_DIR", "PANEL_FRONTEND_DIST_DIR"))
	if frontendDistDir != "" {
		cfg.FrontendDistDir = frontendDistDir
	}

	publicAssetsDir := strings.TrimSpace(firstEnv("NRE_PUBLIC_AGENT_ASSETS_DIR", "PANEL_PUBLIC_AGENT_ASSETS_DIR"))
	if publicAssetsDir != "" {
		cfg.PublicAgentAssetsDir = publicAssetsDir
	}

	if val := strings.TrimSpace(firstEnv("NRE_ENABLE_LOCAL_AGENT", "MASTER_LOCAL_AGENT_ENABLED")); val != "" {
		enabled, err := parseBool(val)
		if err != nil {
			return Config{}, fmt.Errorf("invalid NRE_ENABLE_LOCAL_AGENT: %w", err)
		}
		cfg.EnableLocalAgent = enabled
	}
	if val := strings.TrimSpace(firstEnv("NRE_LOCAL_AGENT_ID", "MASTER_LOCAL_AGENT_ID")); val != "" {
		cfg.LocalAgentID = val
	}
	if val := strings.TrimSpace(firstEnv("NRE_LOCAL_AGENT_NAME", "MASTER_LOCAL_AGENT_NAME")); val != "" {
		cfg.LocalAgentName = val
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
		cfg.LocalAgentHTTP3Enabled = enabled
	}
	if val := strings.TrimSpace(os.Getenv("NRE_TRAFFIC_STATS_ENABLED")); val != "" {
		enabled, err := strconv.ParseBool(val)
		if err != nil {
			return Config{}, fmt.Errorf("invalid NRE_TRAFFIC_STATS_ENABLED: %w", err)
		}
		cfg.LocalAgentTrafficStatsEnabled = enabled
		cfg.LocalAgentTrafficStatsExplicit = true
	}
	if val := strings.TrimSpace(os.Getenv("NRE_HTTP_DIAL_TIMEOUT")); val != "" {
		dur, err := parsePositiveDurationEnv("NRE_HTTP_DIAL_TIMEOUT", val)
		if err != nil {
			return Config{}, err
		}
		cfg.LocalAgentHTTPTransport.DialTimeout = dur
	}
	if val := strings.TrimSpace(os.Getenv("NRE_HTTP_TLS_HANDSHAKE_TIMEOUT")); val != "" {
		dur, err := parsePositiveDurationEnv("NRE_HTTP_TLS_HANDSHAKE_TIMEOUT", val)
		if err != nil {
			return Config{}, err
		}
		cfg.LocalAgentHTTPTransport.TLSHandshakeTimeout = dur
	}
	if val := strings.TrimSpace(os.Getenv("NRE_HTTP_RESPONSE_HEADER_TIMEOUT")); val != "" {
		dur, err := parsePositiveDurationEnv("NRE_HTTP_RESPONSE_HEADER_TIMEOUT", val)
		if err != nil {
			return Config{}, err
		}
		cfg.LocalAgentHTTPTransport.ResponseHeaderTimeout = dur
	}
	if val := strings.TrimSpace(os.Getenv("NRE_HTTP_IDLE_CONN_TIMEOUT")); val != "" {
		dur, err := parsePositiveDurationEnv("NRE_HTTP_IDLE_CONN_TIMEOUT", val)
		if err != nil {
			return Config{}, err
		}
		cfg.LocalAgentHTTPTransport.IdleConnTimeout = dur
	}
	if val := strings.TrimSpace(os.Getenv("NRE_HTTP_KEEP_ALIVE")); val != "" {
		dur, err := parsePositiveDurationEnv("NRE_HTTP_KEEP_ALIVE", val)
		if err != nil {
			return Config{}, err
		}
		cfg.LocalAgentHTTPTransport.KeepAlive = dur
	}
	if val := strings.TrimSpace(os.Getenv("NRE_HTTP_STREAM_RESUME_ENABLED")); val != "" {
		enabled, err := strconv.ParseBool(val)
		if err != nil {
			return Config{}, fmt.Errorf("invalid NRE_HTTP_STREAM_RESUME_ENABLED: %w", err)
		}
		cfg.LocalAgentHTTPResilience.ResumeEnabled = enabled
	}
	if val := strings.TrimSpace(os.Getenv("NRE_HTTP_STREAM_RESUME_MAX_ATTEMPTS")); val != "" {
		attempts, err := parsePositiveIntEnv("NRE_HTTP_STREAM_RESUME_MAX_ATTEMPTS", val)
		if err != nil {
			return Config{}, err
		}
		cfg.LocalAgentHTTPResilience.ResumeMaxAttempts = attempts
	}
	if val := strings.TrimSpace(os.Getenv("NRE_HTTP_SAME_BACKEND_RETRY_ATTEMPTS")); val != "" {
		attempts, err := parseNonNegativeIntEnv("NRE_HTTP_SAME_BACKEND_RETRY_ATTEMPTS", val)
		if err != nil {
			return Config{}, err
		}
		cfg.LocalAgentHTTPResilience.SameBackendRetryAttempts = attempts
	}
	if val := strings.TrimSpace(os.Getenv("NRE_BACKEND_FAILURE_BACKOFF_BASE")); val != "" {
		dur, err := parsePositiveDurationEnv("NRE_BACKEND_FAILURE_BACKOFF_BASE", val)
		if err != nil {
			return Config{}, err
		}
		cfg.LocalAgentBackendFailures.BackoffBase = dur
		cfg.LocalAgentBackendFailuresExplicit = true
	}
	if val := strings.TrimSpace(os.Getenv("NRE_BACKEND_FAILURE_BACKOFF_LIMIT")); val != "" {
		dur, err := parsePositiveDurationEnv("NRE_BACKEND_FAILURE_BACKOFF_LIMIT", val)
		if err != nil {
			return Config{}, err
		}
		cfg.LocalAgentBackendFailures.BackoffLimit = dur
		cfg.LocalAgentBackendFailuresExplicit = true
	}
	if cfg.LocalAgentBackendFailures.BackoffBase > cfg.LocalAgentBackendFailures.BackoffLimit {
		return Config{}, errors.New("NRE_BACKEND_FAILURE_BACKOFF_BASE must be less than or equal to NRE_BACKEND_FAILURE_BACKOFF_LIMIT")
	}
	if val := strings.TrimSpace(os.Getenv("NRE_RELAY_DIAL_TIMEOUT")); val != "" {
		dur, err := parsePositiveDurationEnv("NRE_RELAY_DIAL_TIMEOUT", val)
		if err != nil {
			return Config{}, err
		}
		cfg.LocalAgentRelayTimeouts.DialTimeout = dur
	}
	if val := strings.TrimSpace(os.Getenv("NRE_RELAY_HANDSHAKE_TIMEOUT")); val != "" {
		dur, err := parsePositiveDurationEnv("NRE_RELAY_HANDSHAKE_TIMEOUT", val)
		if err != nil {
			return Config{}, err
		}
		cfg.LocalAgentRelayTimeouts.HandshakeTimeout = dur
	}
	if val := strings.TrimSpace(os.Getenv("NRE_RELAY_FRAME_TIMEOUT")); val != "" {
		dur, err := parsePositiveDurationEnv("NRE_RELAY_FRAME_TIMEOUT", val)
		if err != nil {
			return Config{}, err
		}
		cfg.LocalAgentRelayTimeouts.FrameTimeout = dur
	}
	if val := strings.TrimSpace(os.Getenv("NRE_RELAY_IDLE_TIMEOUT")); val != "" {
		dur, err := parsePositiveDurationEnv("NRE_RELAY_IDLE_TIMEOUT", val)
		if err != nil {
			return Config{}, err
		}
		cfg.LocalAgentRelayTimeouts.IdleTimeout = dur
	}
	if val := strings.TrimSpace(os.Getenv("NRE_MANAGED_CERT_RENEW_INTERVAL")); val != "" {
		dur, err := time.ParseDuration(val)
		if err != nil {
			return Config{}, fmt.Errorf("invalid NRE_MANAGED_CERT_RENEW_INTERVAL: %w", err)
		}
		if dur <= 0 {
			return Config{}, errors.New("NRE_MANAGED_CERT_RENEW_INTERVAL must be positive")
		}
		cfg.ManagedCertificateRenewInterval = dur
	} else if val := strings.TrimSpace(firstEnv("PANEL_MANAGED_CERT_RENEW_INTERVAL_MS")); val != "" {
		ms, err := strconv.ParseInt(val, 10, 64)
		if err != nil {
			return Config{}, fmt.Errorf("invalid PANEL_MANAGED_CERT_RENEW_INTERVAL_MS: %w", err)
		}
		if ms <= 0 {
			return Config{}, errors.New("PANEL_MANAGED_CERT_RENEW_INTERVAL_MS must be positive")
		}
		cfg.ManagedCertificateRenewInterval = time.Duration(ms) * time.Millisecond
	}

	acmeDNSProvider := strings.TrimSpace(firstEnv("ACME_DNS_PROVIDER"))
	cfToken := strings.TrimSpace(firstEnv("CLOUDFLARE_DNS_API_TOKEN", "CF_DNS_API_TOKEN", "CF_TOKEN", "CF_Token"))
	cfg.ManagedDNSCertificatesEnabled = strings.EqualFold(acmeDNSProvider, "cf") && cfToken != ""

	cfg.ProjectURL = strings.TrimSpace(os.Getenv("NRE_PROJECT_URL"))

	if cfg.AppVersion == "" {
		cfg.AppVersion = "dev"
	}
	if cfg.BuildTime == "" {
		cfg.BuildTime = time.Now().UTC().Format(time.RFC3339)
	}
	if cfg.GoVersion == "" {
		cfg.GoVersion = "dev"
	}

	return cfg, nil
}

func firstEnv(keys ...string) string {
	for _, key := range keys {
		if value := os.Getenv(key); strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func parseBool(value string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true, nil
	case "0", "false", "no", "off":
		return false, nil
	default:
		return false, fmt.Errorf("unsupported boolean value %q", value)
	}
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
