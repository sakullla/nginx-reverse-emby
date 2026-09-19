package pluginsdk

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHTTPBackendProviderMiddlewareAuthenticatesAndHidesCapability(t *testing.T) {
	endpoint := HTTPBackendProviderEndpoint{InstanceID: "instance-1", ProviderID: "default", Generation: "generation-1", Endpoint: filepath.Join(t.TempDir(), "provider.sock"), Credential: strings.Repeat("a", 64)}
	called := 0
	handler := authenticatedHTTPBackendProvider(endpoint, http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		called++
		for _, header := range []string{HeaderHTTPBackendProviderCredential, HeaderHTTPBackendProviderInstance, HeaderHTTPBackendProviderID, HeaderHTTPBackendProviderGeneration, HeaderHTTPBackendProviderProbe} {
			if request.Header.Get(header) != "" {
				t.Fatalf("handler observed internal header %s", header)
			}
		}
		response.WriteHeader(http.StatusAccepted)
	}))

	business := httptest.NewRequest(http.MethodGet, HTTPBackendProviderReadyPath, nil)
	setSDKProviderHeaders(business.Header, endpoint, false)
	businessResult := httptest.NewRecorder()
	handler.ServeHTTP(businessResult, business)
	if businessResult.Code != http.StatusAccepted || called != 1 {
		t.Fatalf("reserved path without probe = %d/called %d, want handler", businessResult.Code, called)
	}
	if businessResult.Header().Get(HeaderHTTPBackendProviderGeneration) != "" {
		t.Fatal("business request exposed readiness identity")
	}

	probe := httptest.NewRequest(http.MethodGet, HTTPBackendProviderReadyPath, nil)
	setSDKProviderHeaders(probe.Header, endpoint, true)
	probeResult := httptest.NewRecorder()
	handler.ServeHTTP(probeResult, probe)
	if probeResult.Code != http.StatusNoContent || probeResult.Header().Get(HeaderHTTPBackendProviderGeneration) != endpoint.Generation || called != 1 {
		t.Fatalf("probe = %d/%q/called %d", probeResult.Code, probeResult.Header().Get(HeaderHTTPBackendProviderGeneration), called)
	}

	wrong := httptest.NewRequest(http.MethodGet, "/", nil)
	setSDKProviderHeaders(wrong.Header, endpoint, false)
	wrong.Header.Set(HeaderHTTPBackendProviderCredential, strings.Repeat("b", 64))
	wrongResult := httptest.NewRecorder()
	handler.ServeHTTP(wrongResult, wrong)
	if wrongResult.Code != http.StatusUnauthorized || called != 1 {
		t.Fatalf("wrong credential = %d/called %d", wrongResult.Code, called)
	}
}

func TestLoadHTTPBackendProviderEndpointConfigUsesProtectedPathsNotTokenEnvironment(t *testing.T) {
	directory := t.TempDir()
	configPath := filepath.Join(directory, "providers.json")
	config := HTTPBackendProviderEndpointConfig{Version: HTTPBackendProviderEndpointConfigVersion, Providers: []HTTPBackendProviderEndpoint{{
		InstanceID: "instance-1", ProviderID: "default", Generation: "generation-1", Endpoint: "provider.sock", Credential: strings.Repeat("c", 64),
	}}}
	payload, _ := json.Marshal(config)
	if err := os.WriteFile(configPath, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(EnvHTTPBackendProviderConfigFile, configPath)
	t.Setenv(EnvHTTPBackendProviderEndpointDirectory, directory)
	loaded, err := LoadHTTPBackendProviderEndpointConfig()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Providers[0].Endpoint != filepath.Join(directory, "provider.sock") || strings.Contains(os.Getenv(EnvHTTPBackendProviderConfigFile), loaded.Providers[0].Credential) {
		t.Fatalf("loaded endpoint/config env = %#v/%q", loaded.Providers[0], os.Getenv(EnvHTTPBackendProviderConfigFile))
	}
}

func TestLoadHTTPBackendProviderEndpointConfigRejectsTrailingJSON(t *testing.T) {
	directory := t.TempDir()
	configPath := filepath.Join(directory, "providers.json")
	config := HTTPBackendProviderEndpointConfig{Version: HTTPBackendProviderEndpointConfigVersion, Providers: []HTTPBackendProviderEndpoint{{
		InstanceID: "instance-1", ProviderID: "default", Generation: "generation-1", Endpoint: "provider.sock", Credential: strings.Repeat("c", 64),
	}}}
	payload, _ := json.Marshal(config)
	payload = append(payload, []byte(` {}`)...)
	if err := os.WriteFile(configPath, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(EnvHTTPBackendProviderConfigFile, configPath)
	t.Setenv(EnvHTTPBackendProviderEndpointDirectory, directory)
	if _, err := LoadHTTPBackendProviderEndpointConfig(); err == nil {
		t.Fatal("LoadHTTPBackendProviderEndpointConfig accepted trailing JSON data")
	}
}

func TestServeHTTPBackendProviderConfigRejectsStaleNonSocket(t *testing.T) {
	endpointPath := filepath.Join(t.TempDir(), "provider.sock")
	if err := os.WriteFile(endpointPath, []byte("not a socket"), 0o600); err != nil {
		t.Fatal(err)
	}
	config := HTTPBackendProviderEndpointConfig{Version: HTTPBackendProviderEndpointConfigVersion, Providers: []HTTPBackendProviderEndpoint{{
		InstanceID: "instance-1", ProviderID: "default", Generation: "generation-1", Endpoint: endpointPath, Credential: strings.Repeat("d", 64),
	}}}
	err := ServeHTTPBackendProviderConfig(context.Background(), config, map[string]http.Handler{"default": http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})})
	if err == nil || !strings.Contains(err.Error(), "not a socket") {
		t.Fatalf("ServeHTTPBackendProviderConfig() error = %v", err)
	}
}

func setSDKProviderHeaders(header http.Header, endpoint HTTPBackendProviderEndpoint, probe bool) {
	header.Set(HeaderHTTPBackendProviderCredential, endpoint.Credential)
	header.Set(HeaderHTTPBackendProviderInstance, endpoint.InstanceID)
	header.Set(HeaderHTTPBackendProviderID, endpoint.ProviderID)
	header.Set(HeaderHTTPBackendProviderGeneration, endpoint.Generation)
	if probe {
		header.Set(HeaderHTTPBackendProviderProbe, "ready-v1")
	}
}
