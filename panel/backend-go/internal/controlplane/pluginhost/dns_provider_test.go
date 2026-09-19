//go:build !integration

package pluginhost

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

func TestResolveDNSTokenUsesPrivateActiveProvider(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set(internalDNSProviderVersionHeader, "1")
		if request.URL.Path != internalDNSResolvePath || request.Method != http.MethodPost {
			http.NotFound(writer, request)
			return
		}
		if request.Header.Get(pluginsdk.HeaderPluginUICredential) != "private-cookie" || request.Header.Get("X-NRE-Actor") != "system/dns-provider" || request.Header.Get("X-NRE-Resource-Group") != "resource-group/cloudflare-dns" {
			http.Error(writer, "forbidden", http.StatusForbidden)
			return
		}
		var body struct {
			Domain string `json:"domain"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil || body.Domain != "edge.example.com" {
			http.Error(writer, "bad request", http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(writer).Encode(struct {
			Token []byte `json:"token"`
		}{Token: []byte("mapped-provider-token")})
	}))
	defer server.Close()
	address := server.Listener.Addr().String()
	if host, port, err := net.SplitHostPort(address); err == nil && host == "" {
		address = net.JoinHostPort("127.0.0.1", port)
	}

	instance := &Instance{ID: "cloudflare-main", Generation: "generation-1"}
	instance.candidate = Candidate{
		ResourceGroupID: "group/main",
		Identity:        Identity{Generation: "generation-1"},
		Declaration:     Declaration{ExtensionPoints: []string{extensionDNSProvider}, Metadata: map[string]string{"resource.group.ref": "resource-group/cloudflare-dns"}},
		uiEndpoint:      Endpoint{Network: "tcp", Address: address, Cookie: "private-cookie"},
	}
	host := &Host{active: map[string]*Instance{instance.ID: instance}}
	if !host.HasActiveDNSProvider() {
		t.Fatal("active DNS provider was not detected")
	}
	token, err := host.ResolveDNSToken(t.Context(), "Edge.Example.COM.")
	if err != nil {
		t.Fatal(err)
	}
	if token != "mapped-provider-token" {
		t.Fatalf("token = %q", token)
	}
}

func TestResolveDNSTokenNormalizesWildcardCertificateDomain(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set(internalDNSProviderVersionHeader, "1")
		var body struct {
			Domain string `json:"domain"`
		}
		_ = json.NewDecoder(request.Body).Decode(&body)
		if body.Domain != "example.com" {
			http.Error(writer, body.Domain, http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(writer).Encode(struct {
			Token []byte `json:"token"`
		}{Token: []byte("wildcard-token")})
	}))
	defer server.Close()
	instance := &Instance{ID: "cloudflare-main", Generation: "generation-1"}
	instance.candidate = Candidate{Identity: Identity{Generation: "generation-1"}, Declaration: Declaration{ExtensionPoints: []string{extensionDNSProvider}}, uiEndpoint: Endpoint{Network: "tcp", Address: server.Listener.Addr().String(), Cookie: "cookie"}}
	host := &Host{active: map[string]*Instance{instance.ID: instance}}
	token, err := host.ResolveDNSToken(t.Context(), "*.Example.COM.")
	if err != nil || token != "wildcard-token" {
		t.Fatalf("token=%q err=%v", token, err)
	}
}

func TestPluginUIProxyNeverExposesReservedProviderPath(t *testing.T) {
	t.Parallel()
	request := httptest.NewRequest(http.MethodPost, "http://panel"+internalDNSResolvePath, nil)
	response := httptest.NewRecorder()
	(&Host{}).proxyPluginUI(&Instance{}, response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusNotFound)
	}
}

func TestResolveDNSTokenRejectsLegacyProviderWithoutContractVersion(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	instance := &Instance{ID: "legacy-cloudflare", Generation: "generation-1"}
	instance.candidate = Candidate{Identity: Identity{Generation: "generation-1"}, Declaration: Declaration{ExtensionPoints: []string{extensionDNSProvider}}, uiEndpoint: Endpoint{Network: "tcp", Address: server.Listener.Addr().String(), Cookie: "cookie"}}
	host := &Host{active: map[string]*Instance{instance.ID: instance}}
	_, err := host.ResolveDNSToken(t.Context(), "example.com")
	if err == nil || !strings.Contains(err.Error(), "contract v1") {
		t.Fatalf("error = %v", err)
	}
}
