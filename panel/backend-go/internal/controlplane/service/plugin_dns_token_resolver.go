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
	if r == nil {
		return "", fmt.Errorf("Cloudflare domain %s has no available token", strings.TrimSpace(domain))
	}
	if r.host != nil && r.host.HasActiveDNSProvider() {
		token, err := r.host.ResolveDNSToken(ctx, domain)
		if err == nil {
			return token, nil
		}
		if !errors.Is(err, pluginhost.ErrDNSTokenNotMapped) {
			return "", err
		}
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if r.fallback != "" {
		return r.fallback, nil
	}
	return "", fmt.Errorf("Cloudflare domain %s has no available token", strings.TrimSpace(domain))
}
