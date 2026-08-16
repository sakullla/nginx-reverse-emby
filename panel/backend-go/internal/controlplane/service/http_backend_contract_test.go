//go:build !integration

package service

import (
	"encoding/json"
	"strings"
	"testing"

	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

func TestNormalizeHTTPBackendsKeepsURLWireAndTagsProviders(t *testing.T) {
	for _, backend := range []HTTPRuleBackend{
		{URL: " http://127.0.0.1:8096 "},
		{Kind: pluginsdk.HTTPBackendKindURL, URL: " http://127.0.0.1:8096 "},
	} {
		normalized, err := normalizeHTTPBackends([]HTTPRuleBackend{backend})
		if err != nil {
			t.Fatal(err)
		}
		wire, err := json.Marshal(normalized)
		if err != nil {
			t.Fatal(err)
		}
		if string(wire) != `[{"url":"http://127.0.0.1:8096"}]` {
			t.Fatalf("URL backend wire changed: %s", wire)
		}
	}
	historical := "HTTP://user:pass@127.0.0.1:8096/path#fragment"
	normalized, err := normalizeHTTPBackends([]HTTPRuleBackend{
		{URL: "ftp://127.0.0.1/file"},
		{URL: historical},
		{URL: historical},
	})
	if err != nil {
		t.Fatalf("historical URL matrix rejected: %v", err)
	}
	wire, err := json.Marshal(normalized)
	if err != nil {
		t.Fatal(err)
	}
	want := `[{"url":"HTTP://user:pass@127.0.0.1:8096/path#fragment"},{"url":"HTTP://user:pass@127.0.0.1:8096/path#fragment"}]`
	if string(wire) != want {
		t.Fatalf("historical URL normalization/wire = %s, want %s", wire, want)
	}

	provider := HTTPRuleBackend{
		Kind: pluginsdk.HTTPBackendKindPluginProvider,
		PluginProvider: &pluginsdk.HTTPPluginProviderRef{
			InstanceID: "provider-1",
			ProviderID: "default",
		},
	}
	if _, err := normalizeHTTPBackends([]HTTPRuleBackend{{URL: "http://127.0.0.1:8096"}, provider}); err != nil {
		t.Fatalf("mixed URL/provider backends rejected: %v", err)
	}

	for name, backend := range map[string]HTTPRuleBackend{
		"untagged provider": {PluginProvider: provider.PluginProvider},
		"unknown kind":      {Kind: "unknown", URL: "http://127.0.0.1:8096"},
		"mixed payload":     {Kind: pluginsdk.HTTPBackendKindPluginProvider, URL: "http://127.0.0.1:8096", PluginProvider: provider.PluginProvider},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := normalizeHTTPBackends([]HTTPRuleBackend{backend}); err == nil {
				t.Fatal("invalid provider backend was accepted")
			}
		})
	}
}

func TestNormalizeHTTPBackendsEnforcesProviderIDByteLimit(t *testing.T) {
	for _, test := range []struct {
		name    string
		id      string
		wantErr bool
	}{
		{name: "64 bytes", id: "a" + strings.Repeat("b", pluginsdk.ProviderIDMaxBytes-1)},
		{name: "65 bytes", id: "a" + strings.Repeat("b", pluginsdk.ProviderIDMaxBytes), wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := normalizeHTTPBackends([]HTTPRuleBackend{{
				Kind:           pluginsdk.HTTPBackendKindPluginProvider,
				PluginProvider: &pluginsdk.HTTPPluginProviderRef{InstanceID: "provider-1", ProviderID: test.id},
			}})
			if (err != nil) != test.wantErr {
				t.Fatalf("normalizeHTTPBackends() error = %v, wantErr %v", err, test.wantErr)
			}
		})
	}
}
