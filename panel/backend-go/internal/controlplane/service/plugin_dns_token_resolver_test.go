//go:build !integration

package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/pluginhost"
)

type fakePluginDNSTokenHost struct {
	active bool
	token  string
	err    error
}

func (h fakePluginDNSTokenHost) HasActiveDNSProvider() bool { return h.active }
func (h fakePluginDNSTokenHost) ResolveDNSToken(context.Context, string) (string, error) {
	return h.token, h.err
}

type fakePluginDNSProviderHost struct {
	fakePluginDNSTokenHost
	resolution pluginhost.DNSProviderResolution
	ensured    int
	deleted    int
}

func (h *fakePluginDNSProviderHost) ResolveDNSProvider(context.Context, string) (pluginhost.DNSProviderResolution, error) {
	if h.err != nil {
		return pluginhost.DNSProviderResolution{}, h.err
	}
	return h.resolution, nil
}

func (h *fakePluginDNSProviderHost) EnsureDNSRecord(context.Context, string, string, string, string, int) error {
	h.ensured++
	return nil
}

func (h *fakePluginDNSProviderHost) DeleteDNSRecord(context.Context, string, string, string, string) error {
	h.deleted++
	return nil
}

func TestPluginDNSTokenResolverPrecedenceAndFallback(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		host      fakePluginDNSTokenHost
		fallback  string
		wantToken string
		wantError bool
	}{
		{name: "mapped plugin wins", host: fakePluginDNSTokenHost{active: true, token: "mapped-token"}, fallback: "env-token", wantToken: "mapped-token"},
		{name: "mapping miss falls back", host: fakePluginDNSTokenHost{active: true, err: pluginhost.ErrDNSTokenNotMapped}, fallback: "env-token", wantToken: "env-token"},
		{name: "inactive plugin falls back", host: fakePluginDNSTokenHost{}, fallback: "env-token", wantToken: "env-token"},
		{name: "mapped provider failure is fail closed", host: fakePluginDNSTokenHost{active: true, err: errors.New("vault unavailable")}, fallback: "env-token", wantError: true},
		{name: "provider drain is fail closed", host: fakePluginDNSTokenHost{active: true, err: pluginhost.ErrDNSProviderUnavailable}, fallback: "env-token", wantError: true},
		{name: "no source fails", host: fakePluginDNSTokenHost{}, wantError: true},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			resolver := NewPluginDNSTokenResolver(test.host, test.fallback)
			token, err := resolver.Resolve(t.Context(), "edge.example.com")
			if (err != nil) != test.wantError || token != test.wantToken {
				t.Fatalf("token=%q err=%v", token, err)
			}
		})
	}
}

func TestPluginDNSCredentialDoesNotHandDomesticProviderToCloudflare(t *testing.T) {
	t.Parallel()
	host := &fakePluginDNSProviderHost{
		fakePluginDNSTokenHost: fakePluginDNSTokenHost{active: true, token: "must-not-leak"},
		resolution:             pluginhost.DNSProviderResolution{Provider: pluginhost.DNSProviderAliyun},
	}
	resolver := NewPluginDNSTokenResolver(host, "env-token")
	credential, err := resolver.ResolveCredential(t.Context(), "edge.example.cn")
	if err != nil || credential.Provider != pluginhost.DNSProviderAliyun || credential.Token != "" || !credential.Mapped {
		t.Fatalf("credential=%+v err=%v", credential, err)
	}
	token, err := resolver.Resolve(t.Context(), "edge.example.cn")
	if err == nil || token != "" || strings.Contains(err.Error(), "env-token") || strings.Contains(err.Error(), "must-not-leak") {
		t.Fatalf("token=%q err=%v", token, err)
	}

	host.resolution = pluginhost.DNSProviderResolution{Provider: pluginhost.DNSProviderCloudflare, Token: "mapped-token"}
	token, err = resolver.Resolve(t.Context(), "edge.example.com")
	if err != nil || token != "mapped-token" {
		t.Fatalf("cloudflare token=%q err=%v", token, err)
	}
	host.err = errors.New("vault unavailable")
	if _, err := resolver.ResolveCredential(t.Context(), "edge.example.cn"); err == nil || strings.Contains(err.Error(), "env-token") {
		t.Fatalf("mapped failure err=%v", err)
	}
}

func TestManagedCertificateIssuerModeFollowsDomesticMapping(t *testing.T) {
	t.Parallel()
	domestic, err := managedCertificateIssuerModeForDomain(true, func(context.Context, string) (DNSCredential, error) {
		return DNSCredential{Provider: pluginhost.DNSProviderDNSPodCN, Mapped: true}, nil
	}, t.Context(), "www.example.cn", false)
	if err != nil || domestic != "master_cf_dns" {
		t.Fatalf("domestic mode=%q err=%v", domestic, err)
	}
	for _, provider := range []string{pluginhost.DNSProviderAliyun, pluginhost.DNSProviderDNSPodCom, pluginhost.DNSProviderTencentDNS} {
		mode, err := managedCertificateIssuerModeForDomain(false, func(context.Context, string) (DNSCredential, error) {
			return DNSCredential{Provider: provider, Mapped: true}, nil
		}, t.Context(), "www.example.cn", false)
		if err != nil || mode != "master_cf_dns" {
			t.Fatalf("%s mode=%q err=%v", provider, mode, err)
		}
	}
	if _, err := managedCertificateIssuerModeForDomain(true, func(context.Context, string) (DNSCredential, error) {
		return DNSCredential{Provider: pluginhost.DNSProviderAliyun, Token: "must-not-leak", Mapped: true}, nil
	}, t.Context(), "www.example.cn", false); err == nil || strings.Contains(err.Error(), "must-not-leak") {
		t.Fatalf("domestic token err=%v", err)
	}
	cloudflareOff, err := managedCertificateIssuerModeForDomain(true, func(context.Context, string) (DNSCredential, error) {
		return DNSCredential{Provider: pluginhost.DNSProviderCloudflare, Token: "env-token"}, nil
	}, t.Context(), "www.example.com", false)
	if err != nil || cloudflareOff != "local_http01" {
		t.Fatalf("cloudflare without switch mode=%q err=%v", cloudflareOff, err)
	}
	cloudflareOn, err := managedCertificateIssuerModeForDomain(true, func(context.Context, string) (DNSCredential, error) {
		return DNSCredential{Provider: pluginhost.DNSProviderCloudflare, Token: "env-token", Mapped: true}, nil
	}, t.Context(), "www.example.com", true)
	if err != nil || cloudflareOn != "master_cf_dns" {
		t.Fatalf("cloudflare with switch mode=%q err=%v", cloudflareOn, err)
	}
	http01, err := managedCertificateIssuerModeForDomain(true, func(context.Context, string) (DNSCredential, error) {
		return DNSCredential{}, fmt.Errorf("%w: missing.example", errDNSCredentialUnavailable)
	}, t.Context(), "missing.example", true)
	if err != nil || http01 != "local_http01" {
		t.Fatalf("unmapped mode=%q err=%v", http01, err)
	}
	if _, err := managedCertificateIssuerModeForDomain(true, func(context.Context, string) (DNSCredential, error) {
		return DNSCredential{}, errors.New("vault unavailable")
	}, t.Context(), "edge.example.cn", true); err == nil {
		t.Fatal("mapped provider failure fell through")
	}
}
