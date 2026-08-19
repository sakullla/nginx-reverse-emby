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
	for _, historical := range []string{
		"HTTP://127.0.0.1:8096",
		"http://user:pass@127.0.0.1:8096/path#fragment",
	} {
		if err := ValidateHTTPBackends([]HTTPBackend{{URL: historical}, {URL: historical}}); err != nil {
			t.Fatalf("historical duplicate URL backend %q rejected: %v", historical, err)
		}
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
		{name: "duplicate provider", backends: []HTTPBackend{provider, provider}},
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

	for name, permissions := range map[string][]Permission{
		"permission only":                  {{Name: PermissionHTTPOutbound}},
		"resource scoped outbound":         {{Name: PermissionHTTPOutbound, Resource: "tenant-a"}},
		"mixed scoped outbound permission": {{Name: PermissionHTTPOutbound}, {Name: PermissionHTTPOutbound, Resource: "tenant-a"}},
	} {
		t.Run(name, func(t *testing.T) {
			candidate := Manifest{Permissions: permissions}
			if err := ValidateHTTPBackendProviderManifest(candidate); err != nil {
				t.Fatalf("general outbound permission was treated as a provider contract: %v", err)
			}
		})
	}
}

func TestHTTPBackendProviderIDByteLimitAppliesToDescriptorsAndDurableRefs(t *testing.T) {
	for _, test := range []struct {
		name    string
		id      string
		wantErr bool
	}{
		{name: "64 bytes", id: "a" + strings.Repeat("b", ProviderIDMaxBytes-1)},
		{name: "65 bytes", id: "a" + strings.Repeat("b", ProviderIDMaxBytes), wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := len([]byte(test.id)); got != ProviderIDMaxBytes && !test.wantErr || got != ProviderIDMaxBytes+1 && test.wantErr {
				t.Fatalf("fixture byte length = %d", got)
			}
			descriptorErr := (HTTPBackendProviderDescriptor{ID: test.id, DisplayName: "Provider"}).Validate()
			backendErr := (HTTPBackend{
				Kind:           HTTPBackendKindPluginProvider,
				PluginProvider: &HTTPPluginProviderRef{InstanceID: "provider-1", ProviderID: test.id},
			}).Validate()
			if test.wantErr && (descriptorErr == nil || backendErr == nil) {
				t.Fatalf("oversized provider id accepted: descriptor=%v backend=%v", descriptorErr, backendErr)
			}
			if !test.wantErr && (descriptorErr != nil || backendErr != nil) {
				t.Fatalf("maximum provider id rejected: descriptor=%v backend=%v", descriptorErr, backendErr)
			}
		})
	}
}
