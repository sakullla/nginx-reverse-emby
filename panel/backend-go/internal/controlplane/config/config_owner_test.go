//go:build !integration

package config

import (
	"strings"
	"testing"
	"time"
)

func requiredTokens(t *testing.T) {
	t.Helper()
	t.Setenv("NRE_PANEL_TOKEN", "secret")
	t.Setenv("NRE_REGISTER_TOKEN", "register-secret")
}

func TestLoadFromEnvDefaultsAndRejectsUnsafeCombinations(t *testing.T) {
	requiredTokens(t)
	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ListenAddr == "" || cfg.DatabaseDriver != "sqlite" || !cfg.EnableLocalAgent {
		t.Fatalf("defaults = %+v", cfg)
	}

	t.Setenv("NRE_PANEL_TOKEN", "change-this-token")
	if _, err := LoadFromEnv(); err == nil || !strings.Contains(err.Error(), "placeholder") {
		t.Fatalf("placeholder token err=%v", err)
	}
	t.Setenv("NRE_PANEL_TOKEN", "secret")
	t.Setenv("NRE_DATABASE_DRIVER", "oracle")
	if _, err := LoadFromEnv(); err == nil || !strings.Contains(err.Error(), "NRE_DATABASE_DRIVER") {
		t.Fatalf("invalid driver err=%v", err)
	}
	t.Setenv("NRE_DATABASE_DRIVER", "")
	t.Setenv("NRE_PUBLIC_URL", "not-a-url")
	if _, err := LoadFromEnv(); err == nil {
		t.Fatal("invalid public URL accepted")
	}
	t.Setenv("NRE_PUBLIC_URL", "")
	t.Setenv("NRE_PANEL_PUBLIC_PATH", "no-leading-slash")
	if _, err := LoadFromEnv(); err == nil {
		t.Fatal("invalid public path accepted")
	}
	t.Setenv("NRE_PANEL_PUBLIC_PATH", "")
	t.Setenv("NRE_DDNS_IP_PROBE_INTERVAL", "30s")
	cfg, err = LoadFromEnv()
	if err != nil || cfg.LocalAgentDDNSIPProbeInterval != 30*time.Second {
		t.Fatalf("ddns interval = %+v err=%v", cfg.LocalAgentDDNSIPProbeInterval, err)
	}
}

func TestLoadFromEnvSupportsLegacyAliases(t *testing.T) {
	t.Setenv("NRE_PANEL_TOKEN", "")
	t.Setenv("NRE_REGISTER_TOKEN", "")
	t.Setenv("API_TOKEN", "legacy-secret")
	t.Setenv("MASTER_REGISTER_TOKEN", "legacy-register")
	t.Setenv("PANEL_BACKEND_HOST", "127.0.0.1")
	t.Setenv("PANEL_BACKEND_PORT", "18080")
	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PanelToken != "legacy-secret" || cfg.RegisterToken != "legacy-register" || cfg.ListenAddr != "127.0.0.1:18080" {
		t.Fatalf("legacy aliases = %+v", cfg)
	}
}

func TestCloudflareReadinessRequiresConfiguredToken(t *testing.T) {
	cfg := Config{ACMEDNSProvider: "cf"}
	if cfg.ManagedCloudflareDNSReady() || cfg.DDNSReady() {
		t.Fatal("ready flags true without configured credentials")
	}
	cfg.DDNS.Token = "configured-token"
	if !cfg.ManagedCloudflareDNSReady() || !cfg.DDNSReady() {
		t.Fatal("ready flags false with configured credentials")
	}
}

func TestCloudflareProviderSelectionEnablesPluginBackedLifecycleWithoutEnvToken(t *testing.T) {
	requiredTokens(t)
	t.Setenv("ACME_DNS_PROVIDER", "cf")
	t.Setenv("CLOUDFLARE_DNS_API_TOKEN", "")
	t.Setenv("CF_DNS_API_TOKEN", "")
	t.Setenv("CF_TOKEN", "")
	t.Setenv("CF_Token", "")
	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.ManagedDNSCertificatesEnabled {
		t.Fatal("selected Cloudflare provider did not enable the managed certificate lifecycle")
	}
	if cfg.ManagedCloudflareDNSReady() {
		t.Fatal("environment readiness must remain false without an environment token")
	}
}
