package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/pluginhost"
)

type pluginDNSTokenHost interface {
	HasActiveDNSProvider() bool
	ResolveDNSToken(context.Context, string) (string, error)
}

type pluginDNSProviderHost interface {
	ResolveDNSProvider(context.Context, string) (pluginhost.DNSProviderResolution, error)
	EnsureDNSRecord(context.Context, string, string, string, string, int) error
	DeleteDNSRecord(context.Context, string, string, string, string) error
}

// DNSCredential is the control-plane view of one domain. Domestic mappings set
// Provider and leave Token empty so the value cannot be handed to Cloudflare.
type DNSCredential struct {
	Provider string
	Token    string
	Mapped   bool
}

type managedDNSRecordProvider interface {
	ResolveCredential(context.Context, string) (DNSCredential, error)
	EnsureRecord(context.Context, string, string, string, string, int) error
	DeleteRecord(context.Context, string, string, string, string) error
}

var errDNSCredentialUnavailable = errors.New("dns credential is unavailable")

// PluginDNSTokenResolver gives an active dns.provider mapping precedence over
// the environment token. Only a missing provider/mapping may use the fallback;
// an active provider failure remains fail-closed.
type PluginDNSTokenResolver struct {
	host     pluginDNSTokenHost
	fallback string
}

func NewPluginDNSTokenResolver(host pluginDNSTokenHost, fallback string) *PluginDNSTokenResolver {
	return &PluginDNSTokenResolver{host: host, fallback: strings.TrimSpace(fallback)}
}

func (r *PluginDNSTokenResolver) Ready() bool {
	return r != nil && (r.fallback != "" || (r.host != nil && r.host.HasActiveDNSProvider()))
}

func (r *PluginDNSTokenResolver) Resolve(ctx context.Context, domain string) (string, error) {
	credential, err := r.ResolveCredential(ctx, domain)
	if err != nil {
		return "", err
	}
	if pluginhost.IsDomesticDNSProvider(credential.Provider) || credential.Token == "" {
		return "", fmt.Errorf("%w: DNS provider %s does not provide a Cloudflare token for %s", pluginhost.ErrDNSProviderNotCloudflare, credential.Provider, strings.TrimSpace(domain))
	}
	return credential.Token, nil
}

// ResolveCredential reports the winning mapping. A domestic provider is not
// converted into an environment Cloudflare token.
func (r *PluginDNSTokenResolver) ResolveCredential(ctx context.Context, domain string) (DNSCredential, error) {
	if r == nil {
		return DNSCredential{}, fmt.Errorf("%w: Cloudflare domain %s has no available token", errDNSCredentialUnavailable, strings.TrimSpace(domain))
	}
	if r.host != nil && r.host.HasActiveDNSProvider() {
		if providerHost, ok := r.host.(pluginDNSProviderHost); ok {
			resolved, err := providerHost.ResolveDNSProvider(ctx, domain)
			if err == nil {
				return credentialFromResolution(resolved)
			}
			if !errors.Is(err, pluginhost.ErrDNSTokenNotMapped) {
				return DNSCredential{}, err
			}
		} else {
			token, err := r.host.ResolveDNSToken(ctx, domain)
			if err == nil {
				return DNSCredential{Provider: pluginhost.DNSProviderCloudflare, Token: token, Mapped: true}, nil
			}
			if !errors.Is(err, pluginhost.ErrDNSTokenNotMapped) {
				return DNSCredential{}, err
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return DNSCredential{}, err
	}
	if r.fallback != "" {
		return DNSCredential{Provider: pluginhost.DNSProviderCloudflare, Token: r.fallback, Mapped: false}, nil
	}
	return DNSCredential{}, fmt.Errorf("%w: Cloudflare domain %s has no available token", errDNSCredentialUnavailable, strings.TrimSpace(domain))
}

func (r *PluginDNSTokenResolver) EnsureRecord(ctx context.Context, domain, recordType, name, content string, ttl int) error {
	providerHost, ok := r.providerHost()
	if !ok {
		return pluginhost.ErrDNSProviderUnavailable
	}
	return providerHost.EnsureDNSRecord(ctx, domain, recordType, name, content, ttl)
}

func (r *PluginDNSTokenResolver) DeleteRecord(ctx context.Context, domain, recordType, name, content string) error {
	providerHost, ok := r.providerHost()
	if !ok {
		return pluginhost.ErrDNSProviderUnavailable
	}
	return providerHost.DeleteDNSRecord(ctx, domain, recordType, name, content)
}

func (r *PluginDNSTokenResolver) providerHost() (pluginDNSProviderHost, bool) {
	if r == nil || r.host == nil {
		return nil, false
	}
	providerHost, ok := r.host.(pluginDNSProviderHost)
	return providerHost, ok
}

func credentialFromResolution(resolved pluginhost.DNSProviderResolution) (DNSCredential, error) {
	provider := strings.ToLower(strings.TrimSpace(resolved.Provider))
	if provider == "" {
		provider = pluginhost.DNSProviderCloudflare
	}
	if pluginhost.IsDomesticDNSProvider(provider) {
		if resolved.Token != "" {
			return DNSCredential{}, errors.New("domestic DNS provider returned a token")
		}
		return DNSCredential{Provider: provider, Mapped: true}, nil
	}
	if provider != pluginhost.DNSProviderCloudflare || resolved.Token == "" {
		return DNSCredential{}, errors.New("DNS provider returned an invalid token")
	}
	return DNSCredential{Provider: pluginhost.DNSProviderCloudflare, Token: resolved.Token, Mapped: true}, nil
}
