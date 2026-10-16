//go:build !integration

package pluginhost

import (
	"encoding/json"
	"errors"
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
	for _, path := range []string{internalDNSResolvePath, internalDNSEnsurePath, internalDNSDeletePath} {
		request := httptest.NewRequest(http.MethodPost, "http://panel"+path, nil)
		response := httptest.NewRecorder()
		(&Host{}).proxyPluginUI(&Instance{}, response, request)
		if response.Code != http.StatusNotFound {
			t.Fatalf("%s status = %d, want %d", path, response.Code, http.StatusNotFound)
		}
	}
}

func TestResolveDNSProviderKeepsDomesticTokenOffCloudflare(t *testing.T) {
	t.Parallel()
	const secret = "domestic-provider-secret"
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set(internalDNSProviderVersionHeader, "1")
		var body struct {
			Domain string `json:"domain"`
		}
		_ = json.NewDecoder(request.Body).Decode(&body)
		if body.Domain != "example.cn" {
			http.Error(writer, body.Domain, http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(writer).Encode(struct {
			Token    []byte `json:"token"`
			Provider string `json:"provider"`
		}{Token: []byte(secret), Provider: "aliyun"})
	}))
	defer server.Close()
	host := dnsProviderTestHost(t, server)
	resolved, err := host.ResolveDNSProvider(t.Context(), "*.Example.CN.")
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Provider != DNSProviderAliyun || resolved.Token != "" || strings.Contains(resolved.Provider, secret) {
		t.Fatalf("resolution = %+v", resolved)
	}
	if _, err := host.ResolveDNSToken(t.Context(), "example.cn"); !errors.Is(err, ErrDNSProviderNotCloudflare) || strings.Contains(err.Error(), secret) {
		t.Fatalf("token error = %v", err)
	}
}

func TestEnsureAndDeleteDNSRecordUseDomesticProviderOnly(t *testing.T) {
	t.Parallel()
	var ensured, deleted int
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set(internalDNSProviderVersionHeader, "1")
		var body struct {
			Domain  string `json:"domain"`
			Type    string `json:"type"`
			Name    string `json:"name"`
			Content string `json:"content"`
			TTL     int    `json:"ttl"`
		}
		_ = json.NewDecoder(request.Body).Decode(&body)
		switch request.URL.Path {
		case internalDNSResolvePath:
			if body.Domain != "edge.example.cn" {
				http.Error(writer, "domain", http.StatusBadRequest)
				return
			}
			_ = json.NewEncoder(writer).Encode(map[string]string{"provider": "dnspod-cn"})
		case internalDNSEnsurePath:
			ensured++
			if body.Domain != "edge.example.cn" || body.Type != "A" || body.Name != "edge.example.cn" || body.Content != "203.0.113.10" || body.TTL != 120 {
				http.Error(writer, "ensure", http.StatusBadRequest)
				return
			}
			writer.WriteHeader(http.StatusNoContent)
		case internalDNSDeletePath:
			deleted++
			if body.Domain != "edge.example.cn" || body.Type != "TXT" || body.Name != "_acme-challenge.edge.example.cn" || body.Content != "digest" || body.TTL != 0 {
				http.Error(writer, "delete", http.StatusBadRequest)
				return
			}
			writer.WriteHeader(http.StatusOK)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	host := dnsProviderTestHost(t, server)
	if err := host.EnsureDNSRecord(t.Context(), "edge.example.cn", "A", "edge.example.cn", "203.0.113.10", 120); err != nil {
		t.Fatal(err)
	}
	if err := host.DeleteDNSRecord(t.Context(), "edge.example.cn", "TXT", "_acme-challenge.edge.example.cn", "digest"); err != nil {
		t.Fatal(err)
	}
	if ensured != 1 || deleted != 1 {
		t.Fatalf("ensure=%d delete=%d", ensured, deleted)
	}
}

func TestDNSRecordRoutesDoNotCallCloudflareOrUnmappedProviders(t *testing.T) {
	t.Parallel()
	var recordCalls int
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set(internalDNSProviderVersionHeader, "1")
		if request.URL.Path != internalDNSResolvePath {
			recordCalls++
			http.NotFound(writer, request)
			return
		}
		_ = json.NewEncoder(writer).Encode(struct {
			Token    []byte `json:"token"`
			Provider string `json:"provider"`
		}{Token: []byte("cf-token"), Provider: "cloudflare"})
	}))
	defer server.Close()
	host := dnsProviderTestHost(t, server)
	err := host.EnsureDNSRecord(t.Context(), "example.com", "A", "example.com", "203.0.113.10", 120)
	if !errors.Is(err, ErrDNSProviderNotCloudflare) || recordCalls != 0 {
		t.Fatalf("ensure err=%v recordCalls=%d", err, recordCalls)
	}
}

func TestResolveDNSProviderFailsClosedWhenTwoPluginsMapOneDomain(t *testing.T) {
	t.Parallel()
	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set(internalDNSProviderVersionHeader, "1")
		_ = json.NewEncoder(writer).Encode(map[string]string{"provider": "tencent-dns"})
	})
	left := httptest.NewServer(handler)
	defer left.Close()
	right := httptest.NewServer(handler)
	defer right.Close()
	first := dnsProviderInstance("provider-a", left)
	second := dnsProviderInstance("provider-b", right)
	host := &Host{active: map[string]*Instance{first.ID: first, second.ID: second}}
	if _, err := host.ResolveDNSProvider(t.Context(), "example.cn"); err == nil || !strings.Contains(err.Error(), "multiple DNS providers") {
		t.Fatalf("error = %v", err)
	}
}

func TestDNSRecordErrorDoesNotEchoCredentials(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set(internalDNSProviderVersionHeader, "1")
		if request.URL.Path == internalDNSResolvePath {
			_ = json.NewEncoder(writer).Encode(map[string]string{"provider": "dnspod-com"})
			return
		}
		http.Error(writer, `{"error":"permission denied","token":"super-secret-dns-token"}`, http.StatusForbidden)
	}))
	defer server.Close()
	host := dnsProviderTestHost(t, server)
	err := host.EnsureDNSRecord(t.Context(), "example.com", "TXT", "_acme-challenge.example.com", "digest", 120)
	if err == nil || !strings.Contains(err.Error(), "permission denied") || strings.Contains(err.Error(), "super-secret-dns-token") {
		t.Fatalf("error = %v", err)
	}
}

func dnsProviderTestHost(t *testing.T, server *httptest.Server) *Host {
	t.Helper()
	instance := dnsProviderInstance("domestic-main", server)
	return &Host{active: map[string]*Instance{instance.ID: instance}}
}

func dnsProviderInstance(id string, server *httptest.Server) *Instance {
	address := server.Listener.Addr().String()
	if host, port, err := net.SplitHostPort(address); err == nil && host == "" {
		address = net.JoinHostPort("127.0.0.1", port)
	}
	instance := &Instance{ID: id, Generation: "generation-1"}
	instance.candidate = Candidate{
		Identity:    Identity{Generation: "generation-1"},
		Declaration: Declaration{ExtensionPoints: []string{extensionDNSProvider}},
		uiEndpoint:  Endpoint{Network: "tcp", Address: address, Cookie: "private-cookie"},
	}
	return instance
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
