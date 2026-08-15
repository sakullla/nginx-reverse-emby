package pluginsdk

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestValidateHTTPBackendsPreservesURLWireAndRequiresTaggedProvider(t *testing.T) {
	legacyURL := HTTPBackend{URL: "http://127.0.0.1:8096"}
	provider := HTTPBackend{
		Kind: HTTPBackendKindPluginProvider,
		PluginProvider: &HTTPPluginProviderRef{
			InstanceID: "provider-1",
			ProviderID: "default",
		},
	}
	if err := ValidateHTTPBackends([]HTTPBackend{legacyURL, provider}); err != nil {
		t.Fatalf("mixed URL/provider backends rejected: %v", err)
	}
	wire, err := json.Marshal(legacyURL)
	if err != nil {
		t.Fatal(err)
	}
	if string(wire) != `{"url":"http://127.0.0.1:8096"}` {
		t.Fatalf("legacy URL wire changed: %s", wire)
	}

	tests := []struct {
		name     string
		backends []HTTPBackend
	}{
		{name: "empty"},
		{name: "unknown kind", backends: []HTTPBackend{{Kind: "unknown", URL: legacyURL.URL}}},
		{name: "untagged provider", backends: []HTTPBackend{{PluginProvider: provider.PluginProvider}}},
		{name: "mixed payload", backends: []HTTPBackend{{Kind: HTTPBackendKindPluginProvider, URL: legacyURL.URL, PluginProvider: provider.PluginProvider}}},
		{name: "invalid URL", backends: []HTTPBackend{{URL: "ftp://127.0.0.1/file"}}},
		{name: "duplicate URL spellings", backends: []HTTPBackend{legacyURL, {Kind: HTTPBackendKindURL, URL: legacyURL.URL}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := ValidateHTTPBackends(test.backends); err == nil {
				t.Fatal("invalid backends were accepted")
			}
		})
	}
}

func TestValidateHTTPBackendProviderManifestRequiresIndivisibleContract(t *testing.T) {
	valid := Manifest{
		Runtime:              Runtime{Kind: RuntimeRPCService, ABI: RPCABIV1, HostScope: HostScopeAgent},
		ExtensionPoints:      []string{ExtensionHTTPBackendProvider},
		HTTPBackendProviders: []HTTPBackendProviderDescriptor{{ID: "default", DisplayName: "Default"}},
		Permissions:          []Permission{{Name: PermissionHTTPOutbound}},
	}
	if err := ValidateHTTPBackendProviderManifest(valid); err != nil {
		t.Fatalf("valid provider manifest rejected: %v", err)
	}

	for name, mutate := range map[string]func(*Manifest){
		"missing extension":  func(value *Manifest) { value.ExtensionPoints = nil },
		"missing descriptor": func(value *Manifest) { value.HTTPBackendProviders = nil },
		"missing outbound":   func(value *Manifest) { value.Permissions = nil },
		"host runtime":       func(value *Manifest) { value.Runtime.HostScope = HostScopeControlPlane },
		"duplicate provider": func(value *Manifest) {
			value.HTTPBackendProviders = append(value.HTTPBackendProviders, value.HTTPBackendProviders[0])
		},
	} {
		t.Run(name, func(t *testing.T) {
			candidate := valid
			candidate.ExtensionPoints = append([]string(nil), valid.ExtensionPoints...)
			candidate.HTTPBackendProviders = append([]HTTPBackendProviderDescriptor(nil), valid.HTTPBackendProviders...)
			candidate.Permissions = append([]Permission(nil), valid.Permissions...)
			mutate(&candidate)
			if err := ValidateHTTPBackendProviderManifest(candidate); err == nil || strings.TrimSpace(err.Error()) == "" {
				t.Fatal("incomplete provider manifest was accepted")
			}
		})
	}
}
