//go:build !integration

package config

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/pluginhost"
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

type stubCloudflareLookup struct{}

func (stubCloudflareLookup) ResolveToken(context.Context, string) (string, error) {
	return "", pluginhost.ErrMappingMiss
}

func TestLoadFromEnvEnablesCloudflareWhenPluginAvailableWithoutToken(t *testing.T) {
	pluginhost.SetCloudflareDNSLookup(stubCloudflareLookup{})
	t.Cleanup(func() { pluginhost.SetCloudflareDNSLookup(nil) })
	requiredTokens(t)
	t.Setenv("ACME_DNS_PROVIDER", "cf")
	t.Setenv("CLOUDFLARE_DNS_API_TOKEN", "")
	t.Setenv("CF_DNS_API_TOKEN", "")
	t.Setenv("CF_TOKEN", "")
	t.Setenv("CF_Token", "")

	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("LoadFromEnv() error = %v", err)
	}
	if cfg.ACMEDNSProvider != "cf" {
		t.Fatalf("ACMEDNSProvider = %q, want cf", cfg.ACMEDNSProvider)
	}
	if !cfg.ManagedDNSCertificatesEnabled || !cfg.DDNS.Enabled {
		t.Fatalf("plugin-only enablement = certs %v ddns %v, want true true", cfg.ManagedDNSCertificatesEnabled, cfg.DDNS.Enabled)
	}
	if cfg.DDNS.Token != "" {
		t.Fatalf("DDNS.Token = %q, want empty env fallback", cfg.DDNS.Token)
	}
}

func TestManagedCloudflareDNSReadyRechecksInstalledPlugin(t *testing.T) {
	cfg := Config{ACMEDNSProvider: "cf"}
	if cfg.ManagedCloudflareDNSReady() || cfg.DDNSReady() {
		t.Fatal("ready flags true without token or plugin")
	}
	pluginhost.SetCloudflareDNSLookup(stubCloudflareLookup{})
	t.Cleanup(func() { pluginhost.SetCloudflareDNSLookup(nil) })
	if !cfg.ManagedCloudflareDNSReady() || !cfg.DDNSReady() {
		t.Fatal("ready flags false after plugin install")
	}
}
