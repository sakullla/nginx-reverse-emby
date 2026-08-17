package pluginhost

import (
	"context"
	"errors"
	"testing"
)

type stubLookup struct {
	tokens map[string]string
	err    map[string]error
}

func (s stubLookup) ResolveToken(_ context.Context, domain string) (string, error) {
	if err, ok := s.err[domain]; ok {
		return "", err
	}
	if token, ok := s.tokens[domain]; ok {
		return token, nil
	}
	return "", ErrMappingMiss
}

func TestResolveCloudflareDNSTokenUsesMappingWithoutEnvMix(t *testing.T) {
	SetCloudflareDNSLookup(stubLookup{tokens: map[string]string{
		"www.example.com": "token-a",
	}})
	t.Cleanup(func() { SetCloudflareDNSLookup(nil) })
	t.Setenv("CLOUDFLARE_DNS_API_TOKEN", "token-b")
	t.Setenv("CF_DNS_API_TOKEN", "")
	t.Setenv("CF_TOKEN", "")
	t.Setenv("CF_Token", "")

	got, err := ResolveCloudflareDNSToken(context.Background(), "www.example.com")
	if err != nil || got != "token-a" {
		t.Fatalf("mapped resolve = %q, %v, want token-a", got, err)
	}
	got, err = ResolveCloudflareDNSToken(context.Background(), "other.test")
	if err != nil || got != "token-b" {
		t.Fatalf("miss fallback = %q, %v, want token-b", got, err)
	}
}

func TestResolveCloudflareDNSTokenMappedUnavailableDoesNotFallback(t *testing.T) {
	SetCloudflareDNSLookup(stubLookup{err: map[string]error{
		"example.com": ErrMappedTokenUnavailable,
	}})
	t.Cleanup(func() { SetCloudflareDNSLookup(nil) })
	t.Setenv("CF_TOKEN", "token-b")

	got, err := ResolveCloudflareDNSToken(context.Background(), "example.com")
	if !errors.Is(err, ErrMappedTokenUnavailable) {
		t.Fatalf("mapped unavailable err = %v, want %v", err, ErrMappedTokenUnavailable)
	}
	if got != "" {
		t.Fatalf("mapped unavailable token = %q, want empty", got)
	}
	if err == nil || err.Error() == "" || err.Error() == ErrMappedTokenUnavailable.Error() {
		t.Fatalf("mapped unavailable error must name the domain, got %v", err)
	}
}

func TestResolveCloudflareDNSTokenMissingPluginAndEnvFailsWithDomain(t *testing.T) {
	SetCloudflareDNSLookup(nil)
	t.Setenv("CLOUDFLARE_DNS_API_TOKEN", "")
	t.Setenv("CF_DNS_API_TOKEN", "")
	t.Setenv("CF_TOKEN", "")
	t.Setenv("CF_Token", "")

	_, err := ResolveCloudflareDNSToken(context.Background(), "missing.example")
	if !errors.Is(err, ErrTokenUnavailable) {
		t.Fatalf("err = %v, want %v", err, ErrTokenUnavailable)
	}
	if err == nil || !errors.Is(err, ErrTokenUnavailable) || err.Error() == ErrTokenUnavailable.Error() {
		t.Fatalf("error must name the domain, got %v", err)
	}
}

func TestResolveCloudflareDNSTokenUnavailablePluginFallsBackToEnv(t *testing.T) {
	SetCloudflareDNSLookup(stubLookup{err: map[string]error{
		"example.com": ErrPluginUnavailable,
	}})
	t.Cleanup(func() { SetCloudflareDNSLookup(nil) })
	t.Setenv("CF_TOKEN", "env-token")

	got, err := ResolveCloudflareDNSToken(context.Background(), "example.com")
	if err != nil || got != "env-token" {
		t.Fatalf("unavailable plugin fallback = %q, %v, want env-token", got, err)
	}
}

func TestCloudflareDNSAPITokenAliasOrder(t *testing.T) {
	t.Setenv("CLOUDFLARE_DNS_API_TOKEN", "first")
	t.Setenv("CF_DNS_API_TOKEN", "second")
	t.Setenv("CF_TOKEN", "third")
	t.Setenv("CF_Token", "fourth")
	if got := CloudflareDNSAPIToken(); got != "first" {
		t.Fatalf("alias order = %q, want first", got)
	}
	t.Setenv("CLOUDFLARE_DNS_API_TOKEN", "")
	if got := CloudflareDNSAPIToken(); got != "second" {
		t.Fatalf("alias order after clearing first = %q, want second", got)
	}
}
