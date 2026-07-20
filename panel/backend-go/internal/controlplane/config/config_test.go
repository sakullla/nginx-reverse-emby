package config

import (
	"strings"
	"testing"
	"time"
)

func TestLoadFromEnvDefaultsMasterRuntime(t *testing.T) {
	t.Setenv("NRE_CONTROL_PLANE_ADDR", "0.0.0.0:8080")
	t.Setenv("NRE_CONTROL_PLANE_DATA_DIR", "/tmp/nre-data")
	t.Setenv("NRE_PANEL_TOKEN", "secret")
	t.Setenv("NRE_REGISTER_TOKEN", "register-secret")
	t.Setenv("NRE_FRONTEND_DIST_DIR", "/tmp/frontend-dist")
	t.Setenv("NRE_PUBLIC_AGENT_ASSETS_DIR", "/tmp/assets")

	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("LoadFromEnv() error = %v", err)
	}
	if cfg.ListenAddr != "0.0.0.0:8080" || !cfg.EnableLocalAgent || cfg.LocalAgentID != "local" {
		t.Fatalf("unexpected config: %+v", cfg)
	}
}

func TestDefaultUsesNormalizedControlPlaneDataDir(t *testing.T) {
	cfg := Default()
	if cfg.DataDir != "/opt/nginx-reverse-emby/panel/data" {
		t.Fatalf("DataDir = %q", cfg.DataDir)
	}
	if cfg.RevisionCoordinator.ApplyTimeout != time.Minute {
		t.Fatalf("RevisionCoordinator.ApplyTimeout = %v, want 1m", cfg.RevisionCoordinator.ApplyTimeout)
	}
	if cfg.RevisionCoordinator.DrainTimeout != 10*time.Minute {
		t.Fatalf("RevisionCoordinator.DrainTimeout = %v, want 10m", cfg.RevisionCoordinator.DrainTimeout)
	}
}

func TestLoadFromEnvParsesRevisionCoordinatorSettings(t *testing.T) {
	t.Setenv("NRE_PANEL_TOKEN", "secret")
	t.Setenv("NRE_REGISTER_TOKEN", "register-secret")
	t.Setenv("NRE_REVISION_APPLY_TIMEOUT", "45s")
	t.Setenv("NRE_REVISION_DRAIN_TIMEOUT", "7m")

	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("LoadFromEnv() error = %v", err)
	}
	if cfg.RevisionCoordinator.ApplyTimeout != 45*time.Second || cfg.RevisionCoordinator.DrainTimeout != 7*time.Minute {
		t.Fatalf("revision timeouts = (%v,%v)", cfg.RevisionCoordinator.ApplyTimeout, cfg.RevisionCoordinator.DrainTimeout)
	}
}

func TestLoadFromEnvRejectsInvalidRevisionCoordinatorSettings(t *testing.T) {
	testCases := []struct {
		name  string
		key   string
		value string
	}{
		{name: "apply timeout", key: "NRE_REVISION_APPLY_TIMEOUT", value: "0s"},
		{name: "drain timeout", key: "NRE_REVISION_DRAIN_TIMEOUT", value: "bad"},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("NRE_PANEL_TOKEN", "secret")
			t.Setenv("NRE_REGISTER_TOKEN", "register-secret")
			t.Setenv(tc.key, tc.value)
			_, err := LoadFromEnv()
			if err == nil || !strings.Contains(err.Error(), tc.key) {
				t.Fatalf("LoadFromEnv() error = %v, want %s validation", err, tc.key)
			}
		})
	}
}

func TestLoadFromEnvParsesRevisionAgentTimeoutOverrides(t *testing.T) {
	t.Setenv("NRE_PANEL_TOKEN", "secret")
	t.Setenv("NRE_REGISTER_TOKEN", "register-secret")
	t.Setenv("NRE_REVISION_AGENT_TIMEOUT_OVERRIDES", `{
		"edge-a":{"apply_timeout":"90s"},
		"edge-b":{"drain_timeout":"12m"},
		"local":{"apply_timeout":"2m","drain_timeout":"15m"}
	}`)

	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("LoadFromEnv() error = %v", err)
	}
	overrides := cfg.RevisionCoordinator.AgentTimeoutOverrides
	if got := overrides["edge-a"]; got.ApplyTimeout != 90*time.Second || got.DrainTimeout != 0 {
		t.Fatalf("edge-a override = %+v", got)
	}
	if got := overrides["edge-b"]; got.ApplyTimeout != 0 || got.DrainTimeout != 12*time.Minute {
		t.Fatalf("edge-b override = %+v", got)
	}
	if got := overrides["local"]; got.ApplyTimeout != 2*time.Minute || got.DrainTimeout != 15*time.Minute {
		t.Fatalf("local override = %+v", got)
	}
}

func TestLoadFromEnvRejectsInvalidRevisionAgentTimeoutOverrides(t *testing.T) {
	testCases := []struct {
		name      string
		value     string
		wantAgent string
	}{
		{name: "malformed JSON", value: `{`},
		{name: "top level array", value: `[]`},
		{name: "empty agent id", value: `{" ":{"apply_timeout":"30s"}}`},
		{name: "unknown field", value: `{"edge-unknown":{"timeout":"30s"}}`, wantAgent: "edge-unknown"},
		{name: "non-positive apply", value: `{"edge-zero":{"apply_timeout":"0s"}}`, wantAgent: "edge-zero"},
		{name: "invalid drain", value: `{"edge-invalid":{"drain_timeout":"soon"}}`, wantAgent: "edge-invalid"},
		{name: "null override", value: `{"edge-null":null}`, wantAgent: "edge-null"},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("NRE_PANEL_TOKEN", "secret")
			t.Setenv("NRE_REGISTER_TOKEN", "register-secret")
			t.Setenv("NRE_REVISION_AGENT_TIMEOUT_OVERRIDES", tc.value)

			_, err := LoadFromEnv()
			if err == nil || !strings.Contains(err.Error(), "NRE_REVISION_AGENT_TIMEOUT_OVERRIDES") {
				t.Fatalf("LoadFromEnv() error = %v, want env validation", err)
			}
			if tc.wantAgent != "" && !strings.Contains(err.Error(), tc.wantAgent) {
				t.Fatalf("LoadFromEnv() error = %v, want agent %q", err, tc.wantAgent)
			}
		})
	}
}

func TestLoadFromEnvInfersRuntimeAssetDefaults(t *testing.T) {
	t.Setenv("NRE_CONTROL_PLANE_ADDR", "0.0.0.0:8080")
	t.Setenv("NRE_CONTROL_PLANE_DATA_DIR", "/tmp/nre-data")
	t.Setenv("NRE_PANEL_TOKEN", "secret")
	t.Setenv("NRE_REGISTER_TOKEN", "register-secret")

	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("LoadFromEnv() error = %v", err)
	}
	if cfg.FrontendDistDir != "/opt/nginx-reverse-emby/panel/frontend/dist" {
		t.Fatalf("FrontendDistDir = %q, want %q", cfg.FrontendDistDir, "/opt/nginx-reverse-emby/panel/frontend/dist")
	}
	if cfg.PublicAgentAssetsDir != "/opt/nginx-reverse-emby/panel/public/agent-assets" {
		t.Fatalf("PublicAgentAssetsDir = %q, want %q", cfg.PublicAgentAssetsDir, "/opt/nginx-reverse-emby/panel/public/agent-assets")
	}
}

func TestLoadFromEnvSupportsLegacyPanelEnvironmentVariables(t *testing.T) {
	t.Setenv("PANEL_BACKEND_HOST", "0.0.0.0")
	t.Setenv("PANEL_BACKEND_PORT", "8080")
	t.Setenv("PANEL_DATA_ROOT", "/tmp/legacy-data")
	t.Setenv("API_TOKEN", "secret")
	t.Setenv("MASTER_REGISTER_TOKEN", "register-secret")
	t.Setenv("PANEL_FRONTEND_DIST_DIR", "/tmp/legacy-dist")
	t.Setenv("PANEL_PUBLIC_AGENT_ASSETS_DIR", "/tmp/legacy-assets")
	t.Setenv("MASTER_LOCAL_AGENT_ENABLED", "1")
	t.Setenv("MASTER_LOCAL_AGENT_ID", "legacy-local-id")
	t.Setenv("MASTER_LOCAL_AGENT_NAME", "Legacy Local")

	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("LoadFromEnv() error = %v", err)
	}
	if cfg.ListenAddr != "0.0.0.0:8080" {
		t.Fatalf("ListenAddr = %q", cfg.ListenAddr)
	}
	if cfg.DataDir != "/tmp/legacy-data" || cfg.PanelToken != "secret" || cfg.RegisterToken != "register-secret" {
		t.Fatalf("unexpected config: %+v", cfg)
	}
	if cfg.FrontendDistDir != "/tmp/legacy-dist" || cfg.PublicAgentAssetsDir != "/tmp/legacy-assets" {
		t.Fatalf("unexpected asset dirs: %+v", cfg)
	}
	if !cfg.EnableLocalAgent {
		t.Fatalf("EnableLocalAgent = false, want true")
	}
	if cfg.LocalAgentID != "legacy-local-id" {
		t.Fatalf("LocalAgentID = %q, want legacy-local-id", cfg.LocalAgentID)
	}
	if cfg.LocalAgentName != "Legacy Local" {
		t.Fatalf("LocalAgentName = %q, want Legacy Local", cfg.LocalAgentName)
	}
}

func TestLoadFromEnvRejectsPlaceholderTokens(t *testing.T) {
	testCases := []struct {
		name          string
		panelToken    string
		registerToken string
		wantError     string
	}{
		{
			name:          "panel token",
			panelToken:    "change-this-token",
			registerToken: "register-secret",
			wantError:     "NRE_PANEL_TOKEN",
		},
		{
			name:          "register token",
			panelToken:    "secret",
			registerToken: "change-this-register-token",
			wantError:     "NRE_REGISTER_TOKEN",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("NRE_PANEL_TOKEN", tc.panelToken)
			t.Setenv("NRE_REGISTER_TOKEN", tc.registerToken)

			_, err := LoadFromEnv()
			if err == nil || !strings.Contains(err.Error(), tc.wantError) {
				t.Fatalf("LoadFromEnv() error = %v, want %s placeholder rejection", err, tc.wantError)
			}
		})
	}
}

func TestLoadFromEnvParsesPublicURLAndTrustForwardedHeaders(t *testing.T) {
	t.Setenv("NRE_PANEL_TOKEN", "secret")
	t.Setenv("NRE_REGISTER_TOKEN", "register-secret")
	t.Setenv("NRE_PUBLIC_URL", "https://panel.example.com/")
	t.Setenv("NRE_PANEL_PUBLIC_PATH", "/panel-a1b2c3")
	t.Setenv("NRE_TRUST_FORWARDED_HEADERS", "true")

	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("LoadFromEnv() error = %v", err)
	}
	if cfg.PublicURL != "https://panel.example.com" {
		t.Fatalf("PublicURL = %q", cfg.PublicURL)
	}
	if cfg.PanelPublicPath != "/panel-a1b2c3" {
		t.Fatalf("PanelPublicPath = %q", cfg.PanelPublicPath)
	}
	if !cfg.TrustForwardedHeaders {
		t.Fatal("TrustForwardedHeaders = false, want true")
	}
}

func TestLoadFromEnvRejectsInvalidPublicURL(t *testing.T) {
	t.Setenv("NRE_PANEL_TOKEN", "secret")
	t.Setenv("NRE_REGISTER_TOKEN", "register-secret")
	t.Setenv("NRE_PUBLIC_URL", "javascript:alert(1)")

	_, err := LoadFromEnv()
	if err == nil || !strings.Contains(err.Error(), "NRE_PUBLIC_URL") {
		t.Fatalf("LoadFromEnv() error = %v, want NRE_PUBLIC_URL validation error", err)
	}
}

func TestLoadFromEnvRejectsInvalidPanelPublicPath(t *testing.T) {
	testCases := []string{
		"panel",
		"/panel/../admin",
		"/panel?token=1",
		"/panel-api/hidden",
		"/api/hidden",
		"/assets/panel",
	}

	for _, tc := range testCases {
		t.Run(tc, func(t *testing.T) {
			t.Setenv("NRE_PANEL_TOKEN", "secret")
			t.Setenv("NRE_REGISTER_TOKEN", "register-secret")
			t.Setenv("NRE_PANEL_PUBLIC_PATH", tc)

			_, err := LoadFromEnv()
			if err == nil || !strings.Contains(err.Error(), "NRE_PANEL_PUBLIC_PATH") {
				t.Fatalf("LoadFromEnv() error = %v, want NRE_PANEL_PUBLIC_PATH validation error", err)
			}
		})
	}
}

func TestLoadFromEnvMissingRequiredEnvVars(t *testing.T) {
	t.Setenv("NRE_CONTROL_PLANE_ADDR", "0.0.0.0:8080")
	t.Setenv("NRE_CONTROL_PLANE_DATA_DIR", "/tmp/nre-data")

	_, err := LoadFromEnv()
	if err == nil {
		t.Fatalf("LoadFromEnv() expected error for missing required env vars")
	}
	if !strings.Contains(err.Error(), "NRE_PANEL_TOKEN") {
		t.Fatalf("expected NRE_PANEL_TOKEN error, got %v", err)
	}
}

func TestLoadFromEnvRejectsInvalidHeartbeatInterval(t *testing.T) {
	testCases := []string{"not-a-duration", "0s", "-1s"}
	for _, tc := range testCases {
		t.Run(tc, func(t *testing.T) {
			t.Setenv("NRE_PANEL_TOKEN", "secret")
			t.Setenv("NRE_REGISTER_TOKEN", "register-secret")
			t.Setenv("NRE_HEARTBEAT_INTERVAL", tc)

			_, err := LoadFromEnv()
			if err == nil {
				t.Fatalf("LoadFromEnv() expected error for NRE_HEARTBEAT_INTERVAL=%q", tc)
			}
		})
	}
}

func TestLoadFromEnvManagedDNSCertificatesEnabled(t *testing.T) {
	testCases := []struct {
		name   string
		setEnv func(*testing.T)
	}{
		{
			name: "CF_Token",
			setEnv: func(t *testing.T) {
				t.Setenv("ACME_DNS_PROVIDER", "cf")
				t.Setenv("CF_Token", "token")
			},
		},
		{
			name: "CF_TOKEN",
			setEnv: func(t *testing.T) {
				t.Setenv("ACME_DNS_PROVIDER", "cf")
				t.Setenv("CF_TOKEN", "token")
			},
		},
		{
			name: "CLOUDFLARE_DNS_API_TOKEN",
			setEnv: func(t *testing.T) {
				t.Setenv("ACME_DNS_PROVIDER", "cf")
				t.Setenv("CLOUDFLARE_DNS_API_TOKEN", "token")
			},
		},
		{
			name: "CF_DNS_API_TOKEN",
			setEnv: func(t *testing.T) {
				t.Setenv("ACME_DNS_PROVIDER", "cf")
				t.Setenv("CF_DNS_API_TOKEN", "token")
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("NRE_PANEL_TOKEN", "secret")
			t.Setenv("NRE_REGISTER_TOKEN", "register-secret")
			tc.setEnv(t)

			cfg, err := LoadFromEnv()
			if err != nil {
				t.Fatalf("LoadFromEnv() error = %v", err)
			}
			if !cfg.ManagedDNSCertificatesEnabled {
				t.Fatalf("ManagedDNSCertificatesEnabled = false, want true")
			}
		})
	}
}

func TestLoadFromEnvManagedDNSCertificatesDisabledWithoutCompleteCloudflareConfig(t *testing.T) {
	testCases := []struct {
		name   string
		setEnv func(*testing.T)
	}{
		{
			name: "missing provider",
			setEnv: func(t *testing.T) {
				t.Setenv("CF_Token", "token")
			},
		},
		{
			name: "wrong provider",
			setEnv: func(t *testing.T) {
				t.Setenv("ACME_DNS_PROVIDER", "route53")
				t.Setenv("CF_Token", "token")
			},
		},
		{
			name: "missing token",
			setEnv: func(t *testing.T) {
				t.Setenv("ACME_DNS_PROVIDER", "cf")
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("NRE_PANEL_TOKEN", "secret")
			t.Setenv("NRE_REGISTER_TOKEN", "register-secret")
			tc.setEnv(t)

			cfg, err := LoadFromEnv()
			if err != nil {
				t.Fatalf("LoadFromEnv() error = %v", err)
			}
			if cfg.ManagedDNSCertificatesEnabled {
				t.Fatalf("ManagedDNSCertificatesEnabled = true, want false")
			}
		})
	}
}

func TestLoadFromEnvParsesLegacyManagedCertificateRenewIntervalMillis(t *testing.T) {
	t.Setenv("NRE_PANEL_TOKEN", "secret")
	t.Setenv("NRE_REGISTER_TOKEN", "register-secret")
	t.Setenv("PANEL_MANAGED_CERT_RENEW_INTERVAL_MS", "60000")

	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("LoadFromEnv() error = %v", err)
	}
	if cfg.ManagedCertificateRenewInterval != time.Minute {
		t.Fatalf("ManagedCertificateRenewInterval = %v", cfg.ManagedCertificateRenewInterval)
	}
}

func TestLoadFromEnvParsesDatabaseConfig(t *testing.T) {
	t.Setenv("NRE_PANEL_TOKEN", "panel")
	t.Setenv("NRE_REGISTER_TOKEN", "register")
	t.Setenv("NRE_DATABASE_DRIVER", "postgres")
	t.Setenv("NRE_DATABASE_DSN", "postgres://nre:nre@postgres:5432/nre?sslmode=disable")
	t.Setenv("NRE_TRAFFIC_STATS_ENABLED", "false")

	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("LoadFromEnv() error = %v", err)
	}
	if cfg.DatabaseDriver != "postgres" {
		t.Fatalf("DatabaseDriver = %q", cfg.DatabaseDriver)
	}
	if cfg.DatabaseDSN != "postgres://nre:nre@postgres:5432/nre?sslmode=disable" {
		t.Fatalf("DatabaseDSN = %q", cfg.DatabaseDSN)
	}
	if cfg.TrafficStatsEnabled {
		t.Fatal("TrafficStatsEnabled = true, want false")
	}
}

func TestLoadFromEnvTrafficCleanupInterval(t *testing.T) {
	t.Setenv("NRE_PANEL_TOKEN", "panel")
	t.Setenv("NRE_REGISTER_TOKEN", "register")
	t.Setenv("NRE_TRAFFIC_CLEANUP_INTERVAL", "6h")

	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("LoadFromEnv() error = %v", err)
	}
	if cfg.TrafficCleanupInterval != 6*time.Hour {
		t.Fatalf("TrafficCleanupInterval = %v, want 6h", cfg.TrafficCleanupInterval)
	}

	t.Setenv("NRE_TRAFFIC_CLEANUP_INTERVAL", "off")
	cfg, err = LoadFromEnv()
	if err != nil {
		t.Fatalf("LoadFromEnv() error = %v", err)
	}
	if cfg.TrafficCleanupInterval != 0 {
		t.Fatalf("TrafficCleanupInterval = %v, want disabled", cfg.TrafficCleanupInterval)
	}
}

func TestLoadFromEnvRejectsInvalidDatabaseDriver(t *testing.T) {
	t.Setenv("NRE_PANEL_TOKEN", "panel")
	t.Setenv("NRE_REGISTER_TOKEN", "register")
	t.Setenv("NRE_DATABASE_DRIVER", "oracle")

	_, err := LoadFromEnv()
	if err == nil || !strings.Contains(err.Error(), "NRE_DATABASE_DRIVER") {
		t.Fatalf("LoadFromEnv() error = %v, want NRE_DATABASE_DRIVER error", err)
	}
}

func TestLoadFromEnvTrafficStatsEnabledForLocalAgent(t *testing.T) {
	t.Setenv("NRE_PANEL_TOKEN", "secret")
	t.Setenv("NRE_REGISTER_TOKEN", "register-secret")
	t.Setenv("NRE_TRAFFIC_STATS_ENABLED", "false")

	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("LoadFromEnv() error = %v", err)
	}
	if cfg.LocalAgentTrafficStatsEnabled {
		t.Fatal("expected LocalAgentTrafficStatsEnabled to be false")
	}

	t.Setenv("NRE_TRAFFIC_STATS_ENABLED", "maybe")
	_, err = LoadFromEnv()
	if err == nil {
		t.Fatal("expected invalid NRE_TRAFFIC_STATS_ENABLED error")
	}
	if !strings.Contains(err.Error(), "NRE_TRAFFIC_STATS_ENABLED") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadFromEnvTimezone(t *testing.T) {
	t.Setenv("NRE_PANEL_TOKEN", "secret")
	t.Setenv("NRE_REGISTER_TOKEN", "register-secret")
	t.Setenv("NRE_TIMEZONE", "Asia/Shanghai")

	cfg, err := LoadFromEnv()
	if err != nil || cfg.Timezone != "Asia/Shanghai" {
		t.Fatalf("Timezone = %q, %v", cfg.Timezone, err)
	}

	t.Setenv("NRE_TIMEZONE", "Not/AZone")
	_, err = LoadFromEnv()
	if err == nil || !strings.Contains(err.Error(), "NRE_TIMEZONE") {
		t.Fatalf("LoadFromEnv() error = %v, want NRE_TIMEZONE error", err)
	}
}

func TestLoadFromEnvParsesLocalAgentRuntimeResilienceSettings(t *testing.T) {
	t.Setenv("NRE_PANEL_TOKEN", "secret")
	t.Setenv("NRE_REGISTER_TOKEN", "register-secret")
	t.Setenv("NRE_HTTP3_ENABLED", "true")
	t.Setenv("NRE_HTTP_DIAL_TIMEOUT", "7s")
	t.Setenv("NRE_HTTP_TLS_HANDSHAKE_TIMEOUT", "8s")
	t.Setenv("NRE_HTTP_RESPONSE_HEADER_TIMEOUT", "9s")
	t.Setenv("NRE_HTTP_IDLE_CONN_TIMEOUT", "10s")
	t.Setenv("NRE_HTTP_KEEP_ALIVE", "11s")
	t.Setenv("NRE_HTTP_STREAM_RESUME_ENABLED", "true")
	t.Setenv("NRE_HTTP_STREAM_RESUME_MAX_ATTEMPTS", "4")
	t.Setenv("NRE_HTTP_SAME_BACKEND_RETRY_ATTEMPTS", "3")
	t.Setenv("NRE_BACKEND_FAILURE_BACKOFF_BASE", "1s")
	t.Setenv("NRE_BACKEND_FAILURE_BACKOFF_LIMIT", "15s")
	t.Setenv("NRE_RELAY_DIAL_TIMEOUT", "12s")
	t.Setenv("NRE_RELAY_HANDSHAKE_TIMEOUT", "13s")
	t.Setenv("NRE_RELAY_FRAME_TIMEOUT", "14s")
	t.Setenv("NRE_RELAY_IDLE_TIMEOUT", "15s")

	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("LoadFromEnv() error = %v", err)
	}
	if !cfg.LocalAgentHTTP3Enabled {
		t.Fatal("expected LocalAgentHTTP3Enabled")
	}
	if cfg.LocalAgentHTTPTransport.DialTimeout != 7*time.Second {
		t.Fatalf("DialTimeout = %v", cfg.LocalAgentHTTPTransport.DialTimeout)
	}
	if cfg.LocalAgentHTTPResilience.ResumeMaxAttempts != 4 {
		t.Fatalf("ResumeMaxAttempts = %d", cfg.LocalAgentHTTPResilience.ResumeMaxAttempts)
	}
	if !cfg.LocalAgentBackendFailuresExplicit {
		t.Fatal("expected LocalAgentBackendFailuresExplicit")
	}
	if cfg.LocalAgentRelayTimeouts.IdleTimeout != 15*time.Second {
		t.Fatalf("IdleTimeout = %v", cfg.LocalAgentRelayTimeouts.IdleTimeout)
	}
}
