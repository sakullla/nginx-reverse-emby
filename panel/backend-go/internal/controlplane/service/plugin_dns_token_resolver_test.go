//go:build !integration

package service

import (
	"context"
	"errors"
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
