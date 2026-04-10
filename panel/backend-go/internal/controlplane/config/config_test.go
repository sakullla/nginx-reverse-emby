package config

import (
	"strings"
	"testing"
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
	if cfg.FrontendDistDir == "" {
		t.Fatalf("FrontendDistDir should not be empty")
	}
	if cfg.PublicAgentAssetsDir == "" {
		t.Fatalf("PublicAgentAssetsDir should not be empty")
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
