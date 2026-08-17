// Package pluginhost is the control-plane adapter for the delivered
// cloudflare-dns plugin. Token mappings stay in the plugin; this package
// only asks ResolveToken and owns the host-side environment fallback.
package pluginhost

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
)

var (
	// ErrPluginUnavailable means the plugin is not installed or cannot be asked.
	// The host treats this as no mapping and may fall back to environment tokens.
	ErrPluginUnavailable = errors.New("cloudflare-dns plugin is unavailable")
	// ErrMappingMiss means the plugin is available but no suffix mapping hit.
	// The host may fall back to environment tokens.
	ErrMappingMiss = errors.New("cloudflare-dns mapping not found")
	// ErrMappedTokenUnavailable means a mapping hit but its Token material cannot
	// be redeemed. The host must not mix in an environment Token.
	ErrMappedTokenUnavailable = errors.New("cloudflare mapped token unavailable")
	// ErrTokenUnavailable means the domain has no mapping hit and no environment Token.
	ErrTokenUnavailable = errors.New("Cloudflare domain has no available token")
)

// TokenLookup is the plugin ResolveToken surface consumed by the host.
// Implementations must not apply environment-variable fallback; the host owns that.
type TokenLookup interface {
	ResolveToken(ctx context.Context, domain string) (token string, err error)
}

type cloudflareDNSHost struct {
	mu      sync.RWMutex
	lookup  TokenLookup
	handler http.Handler
}

var defaultCloudflareDNSHost cloudflareDNSHost

func SetCloudflareDNSLookup(lookup TokenLookup) {
	defaultCloudflareDNSHost.mu.Lock()
	defer defaultCloudflareDNSHost.mu.Unlock()
	defaultCloudflareDNSHost.lookup = lookup
}

func CloudflareDNSLookup() TokenLookup {
	defaultCloudflareDNSHost.mu.RLock()
	defer defaultCloudflareDNSHost.mu.RUnlock()
	return defaultCloudflareDNSHost.lookup
}

// CloudflareDNSAvailable reports whether a cloudflare-dns lookup handle is installed.
func CloudflareDNSAvailable() bool {
	return CloudflareDNSLookup() != nil
}

func SetCloudflareDNSHandler(handler http.Handler) {
	defaultCloudflareDNSHost.mu.Lock()
	defer defaultCloudflareDNSHost.mu.Unlock()
	defaultCloudflareDNSHost.handler = handler
}

func CloudflareDNSHandler() http.Handler {
	defaultCloudflareDNSHost.mu.RLock()
	defer defaultCloudflareDNSHost.mu.RUnlock()
	return defaultCloudflareDNSHost.handler
}

func CloudflareDNSAPIToken() string {
	for _, key := range []string{"CLOUDFLARE_DNS_API_TOKEN", "CF_DNS_API_TOKEN", "CF_TOKEN", "CF_Token"} {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value
		}
	}
	return ""
}

// ResolveCloudflareDNSToken is the control-plane entry used by ACME and DDNS.
// It asks the plugin for the involved domain, then falls back to the current
// environment Token aliases. Mapping hits never mix in the environment Token.
func ResolveCloudflareDNSToken(ctx context.Context, domain string) (string, error) {
	return ResolveCloudflareDNSTokenWithFallback(ctx, domain, CloudflareDNSAPIToken())
}

func ResolveCloudflareDNSTokenWithFallback(ctx context.Context, domain, fallback string) (string, error) {
	domain = strings.TrimSpace(domain)
	if domain == "" {
		return "", fmt.Errorf("%w: domain is empty", ErrTokenUnavailable)
	}
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return "", err
		}
	}

	lookup := CloudflareDNSLookup()
	if lookup != nil {
		token, err := lookup.ResolveToken(ctx, domain)
		switch {
		case err == nil && strings.TrimSpace(token) != "":
			return strings.TrimSpace(token), nil
		case err == nil, errors.Is(err, ErrMappedTokenUnavailable):
			return "", fmt.Errorf("%w: %s", ErrMappedTokenUnavailable, domain)
		case errors.Is(err, ErrMappingMiss), errors.Is(err, ErrPluginUnavailable):
			// Host-owned fallback.
		default:
			// Installed plugin that cannot be asked is treated as no mapping.
		}
	}

	fallback = strings.TrimSpace(fallback)
	if fallback != "" {
		return fallback, nil
	}
	return "", fmt.Errorf("%w: %s", ErrTokenUnavailable, domain)
}
