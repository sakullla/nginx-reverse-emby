package service

import (
	"encoding/json"
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
