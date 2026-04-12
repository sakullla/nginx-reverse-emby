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
	t.Setenv("MASTER_LOCAL_AGENT_ENABLED", "0")

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
	if cfg.EnableLocalAgent {
		t.Fatalf("EnableLocalAgent = true, want false")
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

func TestLoadFromEnvManagedCertificateRenewIntervalDefaultsTo24Hours(t *testing.T) {
	t.Setenv("NRE_PANEL_TOKEN", "secret")
	t.Setenv("NRE_REGISTER_TOKEN", "register-secret")

	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("LoadFromEnv() error = %v", err)
	}
	if cfg.ManagedCertificateRenewInterval != 24*time.Hour {
		t.Fatalf("ManagedCertificateRenewInterval = %v", cfg.ManagedCertificateRenewInterval)
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
