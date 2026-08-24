package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata"
)

const (
	defaultListenAddr         = "0.0.0.0:8080"
	defaultDataDir            = "/opt/nginx-reverse-emby/panel/data"
	defaultFrontendDistDir    = "/opt/nginx-reverse-emby/panel/frontend/dist"
	defaultPublicAssetsDir    = "/opt/nginx-reverse-emby/panel/public/agent-assets"
	defaultEnableLocalAgent   = true
	defaultLocalAgentID       = "local"
	defaultLocalAgentName     = "local"
	defaultDatabaseDriver     = "sqlite"
	defaultHeartbeatInterval  = 30 * time.Second
	defaultDDNSIPProbe        = 5 * time.Minute
	defaultManagedCertRenew   = 24 * time.Hour
	defaultTrafficCleanup     = 24 * time.Hour
	defaultMarketplaceRefresh = 30 * time.Minute
	defaultRevisionApply      = 60 * time.Second
	defaultRevisionDrain      = 10 * time.Minute
)

type Config struct {
	ListenAddr                        string
	DataDir                           string
	PKIMasterKeyFile                  string
	PanelToken                        string
	RegisterToken                     string
	PublicURL                         string
	PanelPublicPath                   string
	TrustForwardedHeaders             bool
	FrontendDistDir                   string
	PublicAgentAssetsDir              string
	DatabaseDriver                    string
	DatabaseDSN                       string
	TrafficStatsEnabled               bool
	Timezone                          string
	EnableLocalAgent                  bool
	LocalAgentID                      string
	LocalAgentName                    string
	HeartbeatInterval                 time.Duration
	LocalAgentDDNSIPProbeInterval     time.Duration
	LocalAgentHTTP3Enabled            bool
	LocalAgentHTTPTransport           HTTPTransportConfig
	LocalAgentHTTPResilience          HTTPResilienceConfig
	LocalAgentBackendFailures         BackendFailureConfig
	LocalAgentBackendFailuresExplicit bool
	LocalAgentRelayTimeouts           RelayTimeoutConfig
	LocalAgentTrafficStatsEnabled     bool
	LocalAgentTrafficStatsExplicit    bool
	TrafficCleanupInterval            time.Duration
	ManagedCertificateRenewInterval   time.Duration
	MarketplaceRefreshTimeout         time.Duration
	ACMEDNSProvider                   string
	ManagedDNSCertificatesEnabled     bool
	RevisionCoordinator               RevisionCoordinatorConfig
	DDNS                              DDNSRuntimeConfig
	AppVersion                        string
	BuildTime                         string
	GoVersion                         string
	ProjectURL                        string
}

// DDNSRuntimeConfig configures the master-side dynamic DNS reconciler that
// upserts Cloudflare A/AAAA records from the IPv4/IPv6 addresses agents report
// in their heartbeats.
//
// SECURITY (R7): Token is sourced only from environment variables
// (CLOUDFLARE_DNS_API_TOKEN & aliases). It is never persisted to the database, never
// included in backups, never exposed via AgentSummary/API responses, and never
// dispatched to agents.
type DDNSRuntimeConfig struct {
	Enabled  bool
	Token    string
	APIBase  string
	Interval time.Duration
	Timeout  time.Duration
	TTL      int
}

// ManagedCloudflareDNSReady is true when ACME DNS-01 may be attempted with
// explicitly configured Cloudflare credentials.
func (c Config) ManagedCloudflareDNSReady() bool {
	return strings.EqualFold(strings.TrimSpace(c.ACMEDNSProvider), "cf") && strings.TrimSpace(c.DDNS.Token) != ""
}

// DDNSReady is true when DDNS may attempt Cloudflare upserts.
func (c Config) DDNSReady() bool {
	return c.DDNS.Enabled || strings.TrimSpace(c.DDNS.Token) != ""
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

type RevisionCoordinatorConfig struct {
	ApplyTimeout          time.Duration
	DrainTimeout          time.Duration
	AgentTimeoutOverrides map[string]RevisionAgentTimeoutOverride
}

type RevisionAgentTimeoutOverride struct {
	ApplyTimeout time.Duration
	DrainTimeout time.Duration
}

func Default() Config {
	return Config{
		ListenAddr:                    defaultListenAddr,
		DataDir:                       defaultDataDir,
		FrontendDistDir:               defaultFrontendDistDir,
		PublicAgentAssetsDir:          defaultPublicAssetsDir,
		DatabaseDriver:                defaultDatabaseDriver,
		TrafficStatsEnabled:           true,
		Timezone:                      "UTC",
		EnableLocalAgent:              defaultEnableLocalAgent,
		LocalAgentID:                  defaultLocalAgentID,
		LocalAgentName:                defaultLocalAgentName,
		HeartbeatInterval:             defaultHeartbeatInterval,
		LocalAgentDDNSIPProbeInterval: defaultDDNSIPProbe,
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
		TrafficCleanupInterval:          defaultTrafficCleanup,
		ManagedCertificateRenewInterval: defaultManagedCertRenew,
		MarketplaceRefreshTimeout:       defaultMarketplaceRefresh,
		RevisionCoordinator: RevisionCoordinatorConfig{
			ApplyTimeout:          defaultRevisionApply,
			DrainTimeout:          defaultRevisionDrain,
			AgentTimeoutOverrides: make(map[string]RevisionAgentTimeoutOverride),
		},
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
	if val := strings.TrimSpace(os.Getenv("NRE_PKI_MASTER_KEY_FILE")); val != "" {
		cfg.PKIMasterKeyFile = val
	}
	if val := strings.TrimSpace(os.Getenv("NRE_DATABASE_DRIVER")); val != "" {
		driver := strings.ToLower(val)
		switch driver {
		case "sqlite", "postgres", "mysql":
			cfg.DatabaseDriver = driver
		default:
			return Config{}, fmt.Errorf("invalid NRE_DATABASE_DRIVER: %q", val)
		}
	}
	if val := strings.TrimSpace(os.Getenv("NRE_DATABASE_DSN")); val != "" {
		cfg.DatabaseDSN = val
	}

	panelToken := strings.TrimSpace(firstEnv("NRE_PANEL_TOKEN", "API_TOKEN"))
	if panelToken == "" {
		return Config{}, errors.New("NRE_PANEL_TOKEN is required")
	}
	if isPlaceholderToken(panelToken) {
		return Config{}, errors.New("NRE_PANEL_TOKEN must be changed from the example placeholder value")
	}
	cfg.PanelToken = panelToken

	registerToken := strings.TrimSpace(firstEnv("NRE_REGISTER_TOKEN", "MASTER_REGISTER_TOKEN", "PANEL_REGISTER_TOKEN", "API_TOKEN"))
	if registerToken == "" {
		return Config{}, errors.New("NRE_REGISTER_TOKEN is required")
	}
	if isPlaceholderToken(registerToken) {
		return Config{}, errors.New("NRE_REGISTER_TOKEN must be changed from the example placeholder value")
	}
	cfg.RegisterToken = registerToken

	if val := strings.TrimSpace(os.Getenv("NRE_PUBLIC_URL")); val != "" {
		publicURL, err := normalizePublicURL(val)
		if err != nil {
			return Config{}, err
		}
		cfg.PublicURL = publicURL
	}
	if val := strings.TrimSpace(os.Getenv("NRE_PANEL_PUBLIC_PATH")); val != "" {
		panelPath, err := normalizePanelPublicPath(val)
		if err != nil {
			return Config{}, err
		}
		cfg.PanelPublicPath = panelPath
	}
	if val := strings.TrimSpace(os.Getenv("NRE_TRUST_FORWARDED_HEADERS")); val != "" {
		trust, err := parseBool(val)
		if err != nil {
			return Config{}, fmt.Errorf("invalid NRE_TRUST_FORWARDED_HEADERS: %w", err)
		}
		cfg.TrustForwardedHeaders = trust
	}

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
	if val := strings.TrimSpace(os.Getenv("NRE_DDNS_IP_PROBE_INTERVAL")); val != "" {
		dur, err := parsePositiveDurationEnv("NRE_DDNS_IP_PROBE_INTERVAL", val)
		if err != nil {
			return Config{}, err
		}
		cfg.LocalAgentDDNSIPProbeInterval = dur
	}
	if val := strings.TrimSpace(os.Getenv("NRE_REVISION_APPLY_TIMEOUT")); val != "" {
		dur, err := parsePositiveDurationEnv("NRE_REVISION_APPLY_TIMEOUT", val)
		if err != nil {
			return Config{}, err
		}
		cfg.RevisionCoordinator.ApplyTimeout = dur
	}
	if val := strings.TrimSpace(os.Getenv("NRE_REVISION_DRAIN_TIMEOUT")); val != "" {
		dur, err := parsePositiveDurationEnv("NRE_REVISION_DRAIN_TIMEOUT", val)
		if err != nil {
			return Config{}, err
		}
		cfg.RevisionCoordinator.DrainTimeout = dur
	}
	if val := strings.TrimSpace(os.Getenv("NRE_REVISION_AGENT_TIMEOUT_OVERRIDES")); val != "" {
		overrides, err := parseRevisionAgentTimeoutOverrides(val)
		if err != nil {
			return Config{}, err
		}
		cfg.RevisionCoordinator.AgentTimeoutOverrides = overrides
	}
	if val := strings.TrimSpace(os.Getenv("NRE_MARKETPLACE_REFRESH_TIMEOUT")); val != "" {
		dur, err := parsePositiveDurationEnv("NRE_MARKETPLACE_REFRESH_TIMEOUT", val)
		if err != nil {
			return Config{}, err
		}
		cfg.MarketplaceRefreshTimeout = dur
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
		cfg.TrafficStatsEnabled = enabled
		cfg.LocalAgentTrafficStatsEnabled = enabled
		cfg.LocalAgentTrafficStatsExplicit = true
	}
	if val := strings.TrimSpace(os.Getenv("NRE_TIMEZONE")); val != "" {
		if _, err := time.LoadLocation(val); err != nil {
			return Config{}, fmt.Errorf("invalid NRE_TIMEZONE: %w", err)
		}
		cfg.Timezone = val
	}
	if val := strings.TrimSpace(os.Getenv("NRE_TRAFFIC_CLEANUP_INTERVAL")); val != "" {
		dur, err := parseOptionalDurationEnv("NRE_TRAFFIC_CLEANUP_INTERVAL", val)
		if err != nil {
			return Config{}, err
		}
		cfg.TrafficCleanupInterval = dur
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
	cfg.ACMEDNSProvider = acmeDNSProvider
	// Selecting the provider enables the background lifecycle. Credential
	// readiness is evaluated dynamically because a dns.provider plugin may be
	// activated after process startup without an environment fallback token.
	cfg.ManagedDNSCertificatesEnabled = strings.EqualFold(acmeDNSProvider, "cf")

	// DDNS.Token is an environment-only snapshot.
	cfg.DDNS.Token = cfToken
	cfg.DDNS.Enabled = cfToken != ""
	cfg.DDNS.APIBase = strings.TrimSpace(firstEnv("NRE_DDNS_API_BASE", "DDNS_API_BASE"))
	if cfg.DDNS.APIBase == "" {
		cfg.DDNS.APIBase = "https://api.cloudflare.com/client/v4"
	}
	cfg.DDNS.TTL = 120
	if val := strings.TrimSpace(firstEnv("NRE_DDNS_TTL", "DDNS_TTL")); val != "" {
		ttl, err := strconv.Atoi(val)
		if err != nil || ttl < 1 {
			return Config{}, fmt.Errorf("invalid NRE_DDNS_TTL: %w", err)
		}
		cfg.DDNS.TTL = ttl
	}
	cfg.DDNS.Timeout = 15 * time.Second
	if val := strings.TrimSpace(firstEnv("NRE_DDNS_TIMEOUT_MS", "DDNS_TIMEOUT_MS")); val != "" {
		ms, err := strconv.Atoi(val)
		if err != nil || ms <= 0 {
			return Config{}, fmt.Errorf("invalid NRE_DDNS_TIMEOUT_MS: %w", err)
		}
		cfg.DDNS.Timeout = time.Duration(ms) * time.Millisecond
	}
	cfg.DDNS.Interval = 5 * time.Minute
	if val := strings.TrimSpace(firstEnv("NRE_DDNS_INTERVAL_MS", "DDNS_INTERVAL_MS")); val != "" {
		ms, err := strconv.Atoi(val)
		if err != nil || ms <= 0 {
			return Config{}, fmt.Errorf("invalid NRE_DDNS_INTERVAL_MS: %w", err)
		}
		cfg.DDNS.Interval = time.Duration(ms) * time.Millisecond
	}

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

func splitCommaList(value string) []string {
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func isPlaceholderToken(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "change-this-token",
		"change-this-register-token",
		"your-secure-token",
		"your-register-token",
		"changeme",
		"change-me":
		return true
	default:
		return false
	}
}

func normalizePublicURL(value string) (string, error) {
	trimmed := strings.TrimRight(strings.TrimSpace(value), "/")
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", fmt.Errorf("invalid NRE_PUBLIC_URL: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("invalid NRE_PUBLIC_URL: scheme must be http or https")
	}
	if parsed.Host == "" {
		return "", fmt.Errorf("invalid NRE_PUBLIC_URL: host is required")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("invalid NRE_PUBLIC_URL: query and fragment are not supported")
	}
	return trimmed, nil
}

func normalizePanelPublicPath(value string) (string, error) {
	trimmed := strings.TrimRight(strings.TrimSpace(value), "/")
	if trimmed == "" || trimmed == "/" {
		return "", nil
	}
	if !strings.HasPrefix(trimmed, "/") {
		return "", errors.New("invalid NRE_PANEL_PUBLIC_PATH: path must start with /")
	}
	if strings.Contains(trimmed, "?") || strings.Contains(trimmed, "#") {
		return "", errors.New("invalid NRE_PANEL_PUBLIC_PATH: query and fragment are not supported")
	}
	cleaned := pathClean(trimmed)
	if cleaned != trimmed {
		return "", errors.New("invalid NRE_PANEL_PUBLIC_PATH: path must be normalized")
	}
	for _, reserved := range []string{"/api", "/panel-api", "/agent-api", "/assets"} {
		if cleaned == reserved || strings.HasPrefix(cleaned, reserved+"/") {
			return "", fmt.Errorf("invalid NRE_PANEL_PUBLIC_PATH: %s is reserved", reserved)
		}
	}
	return cleaned, nil
}

func pathClean(value string) string {
	parts := strings.Split(value, "/")
	cleaned := make([]string, 0, len(parts))
	for _, part := range parts {
		switch part {
		case "", ".":
			continue
		case "..":
			if len(cleaned) > 0 {
				cleaned = cleaned[:len(cleaned)-1]
			}
		default:
			cleaned = append(cleaned, part)
		}
	}
	if len(cleaned) == 0 {
		return "/"
	}
	return "/" + strings.Join(cleaned, "/")
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

func parseOptionalDurationEnv(name, value string) (time.Duration, error) {
	trimmed := strings.ToLower(strings.TrimSpace(value))
	switch trimmed {
	case "", "0", "0s", "off", "false", "disabled", "disable":
		return 0, nil
	}
	dur, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("invalid %s: %w", name, err)
	}
	if dur < 0 {
		return 0, fmt.Errorf("%s must be non-negative", name)
	}
	return dur, nil
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

func parseRevisionAgentTimeoutOverrides(value string) (map[string]RevisionAgentTimeoutOverride, error) {
	const envName = "NRE_REVISION_AGENT_TIMEOUT_OVERRIDES"
	var entries map[string]json.RawMessage
	decoder := json.NewDecoder(strings.NewReader(value))
	if err := decoder.Decode(&entries); err != nil {
		return nil, fmt.Errorf("invalid %s: expected JSON object: %w", envName, err)
	}
	if entries == nil {
		return nil, fmt.Errorf("invalid %s: expected JSON object", envName)
	}
	if err := expectJSONEOF(decoder); err != nil {
		return nil, fmt.Errorf("invalid %s: %w", envName, err)
	}

	result := make(map[string]RevisionAgentTimeoutOverride, len(entries))
	for rawAgentID, raw := range entries {
		agentID := strings.TrimSpace(rawAgentID)
		if agentID == "" {
			return nil, fmt.Errorf("invalid %s: agent id must not be empty", envName)
		}
		if _, exists := result[agentID]; exists {
			return nil, fmt.Errorf("invalid %s: duplicate normalized agent %q", envName, agentID)
		}
		override, err := parseRevisionAgentTimeoutOverride(envName, agentID, raw)
		if err != nil {
			return nil, err
		}
		result[agentID] = override
	}
	return result, nil
}

func parseRevisionAgentTimeoutOverride(envName, agentID string, raw json.RawMessage) (RevisionAgentTimeoutOverride, error) {
	if strings.TrimSpace(string(raw)) == "null" {
		return RevisionAgentTimeoutOverride{}, fmt.Errorf("invalid %s for agent %q: override must be an object", envName, agentID)
	}
	var fields struct {
		ApplyTimeout json.RawMessage `json:"apply_timeout"`
		DrainTimeout json.RawMessage `json:"drain_timeout"`
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&fields); err != nil {
		return RevisionAgentTimeoutOverride{}, fmt.Errorf("invalid %s for agent %q: %w", envName, agentID, err)
	}
	if err := expectJSONEOF(decoder); err != nil {
		return RevisionAgentTimeoutOverride{}, fmt.Errorf("invalid %s for agent %q: %w", envName, agentID, err)
	}

	var override RevisionAgentTimeoutOverride
	var err error
	if len(fields.ApplyTimeout) > 0 {
		override.ApplyTimeout, err = parseRevisionAgentTimeoutField(envName, agentID, "apply_timeout", fields.ApplyTimeout)
		if err != nil {
			return RevisionAgentTimeoutOverride{}, err
		}
	}
	if len(fields.DrainTimeout) > 0 {
		override.DrainTimeout, err = parseRevisionAgentTimeoutField(envName, agentID, "drain_timeout", fields.DrainTimeout)
		if err != nil {
			return RevisionAgentTimeoutOverride{}, err
		}
	}
	return override, nil
}

func parseRevisionAgentTimeoutField(envName, agentID, field string, raw json.RawMessage) (time.Duration, error) {
	var value string
	if strings.TrimSpace(string(raw)) == "null" || json.Unmarshal(raw, &value) != nil {
		return 0, fmt.Errorf("invalid %s for agent %q field %s: duration must be a string", envName, agentID, field)
	}
	duration, err := parsePositiveDurationEnv(envName, value)
	if err != nil {
		return 0, fmt.Errorf("invalid %s for agent %q field %s: %w", envName, agentID, field, err)
	}
	return duration, nil
}

func expectJSONEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return errors.New("unexpected trailing JSON value")
		}
		return err
	}
	return nil
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
