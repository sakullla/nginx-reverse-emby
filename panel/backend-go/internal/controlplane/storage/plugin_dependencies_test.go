package storage

import (
	"testing"

	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

func TestFilterUnavailablePluginProviderRulesPreservesIndependentRules(t *testing.T) {
	generation := PluginGeneration{
		InstanceID:       "provider-ready",
		ExtensionPoints:  []string{pluginsdk.ExtensionHTTPBackendProvider},
		RequiredFeatures: []string{pluginsdk.RPCFeatureHTTPBackendProviderV1},
		HTTPBackendProviders: []pluginsdk.HTTPBackendProviderDescriptor{
			{ID: "default", DisplayName: "Default"},
		},
	}
	rules := []HTTPRule{
		{ID: 1, Backends: []HTTPBackend{{URL: "http://127.0.0.1:8096"}}},
		{ID: 2, Backends: []HTTPBackend{{Kind: pluginsdk.HTTPBackendKindPluginProvider, PluginProvider: &pluginsdk.HTTPPluginProviderRef{InstanceID: "provider-ready", ProviderID: "default"}}}},
		{ID: 3, Backends: []HTTPBackend{{Kind: pluginsdk.HTTPBackendKindPluginProvider, PluginProvider: &pluginsdk.HTTPPluginProviderRef{InstanceID: "provider-missing", ProviderID: "default"}}}},
		{ID: 4, Backends: []HTTPBackend{{Kind: pluginsdk.HTTPBackendKindPluginProvider, PluginProvider: &pluginsdk.HTTPPluginProviderRef{InstanceID: "provider-ready", ProviderID: "undeclared"}}}},
	}

	filtered := filterUnavailablePluginProviderRules(rules, []PluginGeneration{generation})
	if len(filtered) != 2 || filtered[0].ID != 1 || filtered[1].ID != 2 {
		t.Fatalf("filtered rules = %+v, want independent URL rule and ready provider rule", filtered)
	}
}
