package cloudflare

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/pkg/acmeflow"
)

func TestCloudflareClientContractsPaginationAndTokenScopes(t *testing.T) {
	const (
		dnsToken  = "dns-token-canary"
		zoneToken = "zone-token-canary"
	)
	type requestEvent struct {
		method string
		path   string
		query  url.Values
		token  string
	}
	var (
		mu     sync.Mutex
		events []requestEvent
	)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		mu.Lock()
		events = append(events, requestEvent{
			method: request.Method,
			path:   request.URL.Path,
			query:  request.URL.Query(),
			token:  request.Header.Get("Authorization"),
		})
		mu.Unlock()

		response.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/client/v4/zones":
			if request.Header.Get("Authorization") != "Bearer "+zoneToken {
				t.Errorf("zone lookup Authorization = %q", request.Header.Get("Authorization"))
			}
			name := request.URL.Query().Get("name")
			page := request.URL.Query().Get("page")
			switch {
			case name == "service.example.com":
				writeCloudflareEnvelope(t, response, []Zone{}, 1, 1)
			case name == "example.com" && page == "1":
				writeCloudflareEnvelope(t, response, []Zone{}, 1, 2)
			case name == "example.com" && page == "2":
				writeCloudflareEnvelope(t, response, []Zone{{ID: "zone-id", Name: "Example.COM", Status: "active"}}, 2, 2)
			default:
				writeCloudflareEnvelope(t, response, []Zone{}, 1, 1)
			}
		case request.Method == http.MethodGet && request.URL.Path == "/client/v4/zones/zone-id/dns_records":
			if request.Header.Get("Authorization") != "Bearer "+dnsToken {
				t.Errorf("record list Authorization = %q", request.Header.Get("Authorization"))
			}
			if request.URL.Query().Get("type") != "TXT" || request.URL.Query().Get("name.exact") != "_acme-challenge.service.example.com" || request.URL.Query().Get("match") != "all" {
				t.Errorf("record list query = %q", request.URL.RawQuery)
			}
			if request.URL.Query().Get("page") == "1" {
				writeCloudflareEnvelope(t, response, []TXTRecord{{ID: "existing-id", Name: "_acme-challenge.service.example.com", Content: `"existing"`, TTL: 300, Type: "TXT"}}, 1, 2)
				return
			}
			writeCloudflareEnvelope(t, response, []TXTRecord{{ID: "created-id", Name: "_acme-challenge.service.example.com", Content: `"challenge-value"`, TTL: DefaultRecordTTL, Type: "TXT"}}, 2, 2)
		case request.Method == http.MethodPost && request.URL.Path == "/client/v4/zones/zone-id/dns_records":
			if request.Header.Get("Authorization") != "Bearer "+dnsToken {
				t.Errorf("record create Authorization = %q", request.Header.Get("Authorization"))
			}
			var input struct {
				Type    string `json:"type"`
				Name    string `json:"name"`
				Content string `json:"content"`
				TTL     int    `json:"ttl"`
			}
			if err := json.NewDecoder(request.Body).Decode(&input); err != nil {
				t.Errorf("decode create request: %v", err)
			}
			if input.Type != "TXT" || input.Name != "_acme-challenge.service.example.com" || input.Content != `"challenge-value"` || input.TTL != DefaultRecordTTL {
				t.Errorf("create input = %#v", input)
			}
			writeCloudflareEnvelope(t, response, TXTRecord{ID: "created-id", Name: input.Name, Content: input.Content, TTL: input.TTL, Type: "TXT"}, 1, 1)
		case request.Method == http.MethodDelete && request.URL.Path == "/client/v4/zones/zone-id/dns_records/created-id":
			if request.Header.Get("Authorization") != "Bearer "+dnsToken {
				t.Errorf("record delete Authorization = %q", request.Header.Get("Authorization"))
			}
			if err := json.NewEncoder(response).Encode(map[string]any{"result": map[string]string{"id": "created-id"}}); err != nil {
				t.Errorf("encode delete response: %v", err)
			}
		default:
			http.Error(response, "unexpected request", http.StatusNotFound)
		}
	}))
	defer server.Close()

	client, err := NewClient(ClientConfig{
		BaseURL:      server.URL + "/client/v4",
		DNSAPIToken:  dnsToken,
		ZoneAPIToken: zoneToken,
		HTTPClient:   server.Client(),
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	if client.apiTimeout != DefaultAPITimeout {
		t.Fatalf("api timeout = %v, want %v", client.apiTimeout, DefaultAPITimeout)
	}

	ctx := context.Background()
	zone, err := client.FindZone(ctx, "*.Service.Example.COM.")
	if err != nil {
		t.Fatalf("FindZone() error = %v", err)
	}
	if zone.ID != "zone-id" || zone.Name != "example.com" {
		t.Fatalf("FindZone() = %#v", zone)
	}
	records, err := client.ListTXTRecords(ctx, zone.ID, "_acme-challenge.Service.Example.COM.")
	if err != nil {
		t.Fatalf("ListTXTRecords() error = %v", err)
	}
	if len(records) != 2 || records[0].ID != "existing-id" || records[0].Content != "existing" || records[1].ID != "created-id" || records[1].Content != "challenge-value" {
		t.Fatalf("ListTXTRecords() = %#v", records)
	}
	created, err := client.CreateTXTRecord(ctx, zone.ID, "_acme-challenge.Service.Example.COM.", "challenge-value")
	if err != nil {
		t.Fatalf("CreateTXTRecord() error = %v", err)
	}
	if created.ID != "created-id" || created.Content != "challenge-value" || created.TTL != DefaultRecordTTL {
		t.Fatalf("CreateTXTRecord() = %#v", created)
	}
	if err := client.DeleteRecord(ctx, zone.ID, created.ID); err != nil {
		t.Fatalf("DeleteRecord() error = %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(events) != 7 {
		t.Fatalf("request count = %d, want 7: %#v", len(events), events)
	}
	for _, event := range events {
		if event.path == "/client/v4/zones" {
			if event.token != "Bearer "+zoneToken {
				t.Fatalf("zone event token = %q", event.token)
			}
			continue
		}
		if event.token != "Bearer "+dnsToken {
			t.Fatalf("record event token = %q", event.token)
		}
	}
}

func TestCloudflareClientFallbackErrorsRetryAfterCancellationAndRedaction(t *testing.T) {
	const token = "provider-token-canary"
	const providerBody = "provider-raw-body-canary"
	var zoneAuth string
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		zoneAuth = request.Header.Get("Authorization")
		name := request.URL.Query().Get("name")
		switch name {
		case "forbidden.example":
			response.WriteHeader(http.StatusForbidden)
			_, _ = response.Write([]byte(`{"success":false,"errors":[{"message":"` + providerBody + ` ` + token + `"}]}`))
		case "limited.example":
			response.Header().Set("Retry-After", "17")
			response.WriteHeader(http.StatusTooManyRequests)
			_, _ = response.Write([]byte(providerBody))
		case "broken.example":
			response.WriteHeader(http.StatusServiceUnavailable)
			_, _ = response.Write([]byte(providerBody))
		case "missing.example":
			response.WriteHeader(http.StatusNotFound)
			_, _ = response.Write([]byte(providerBody))
		default:
			writeCloudflareEnvelope(t, response, []Zone{{ID: "zone-id", Name: name, Status: "active"}}, 1, 1)
		}
	}))
	defer server.Close()

	client, err := NewClient(ClientConfig{BaseURL: server.URL, DNSAPIToken: token, HTTPClient: server.Client()})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	if _, err := client.FindZone(context.Background(), "fallback.example"); err != nil {
		t.Fatalf("fallback FindZone() error = %v", err)
	}
	if zoneAuth != "Bearer "+token {
		t.Fatalf("fallback zone Authorization = %q", zoneAuth)
	}

	tests := []struct {
		name       string
		domain     string
		category   acmeflow.ErrorCategory
		retryAfter time.Duration
	}{
		{name: "permission", domain: "forbidden.example", category: acmeflow.CategoryAuthorization},
		{name: "rate limit", domain: "limited.example", category: acmeflow.CategoryRateLimited, retryAfter: 17 * time.Second},
		{name: "server", domain: "broken.example", category: acmeflow.CategoryNetwork},
		{name: "missing", domain: "missing.example", category: acmeflow.CategoryChallenge},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := client.FindZone(context.Background(), test.domain)
			if err == nil {
				t.Fatal("FindZone() error = nil")
			}
			if got := acmeflow.ErrorCategoryOf(err); got != test.category {
				t.Fatalf("category = %q, want %q; err=%v", got, test.category, err)
			}
			var safe *acmeflow.SafeError
			if !errors.As(err, &safe) {
				t.Fatalf("error type = %T, want *acmeflow.SafeError", err)
			}
			if safe.RetryAfter != test.retryAfter {
				t.Fatalf("RetryAfter = %v, want %v", safe.RetryAfter, test.retryAfter)
			}
			for _, secret := range []string{token, providerBody, "Authorization"} {
				if strings.Contains(err.Error(), secret) {
					t.Fatalf("safe error leaked %q: %v", secret, err)
				}
			}
		})
	}

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = client.FindZone(cancelled, "cancelled.example")
	if got := acmeflow.ErrorCategoryOf(err); got != acmeflow.CategoryCancelled {
		t.Fatalf("cancelled category = %q, want %q; err=%v", got, acmeflow.CategoryCancelled, err)
	}
}

func TestCloudflareClientDoesNotFollowRedirectsWithAuthorization(t *testing.T) {
	const token = "redirect-token-canary"
	var redirectedAuthorization string
	target := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		redirectedAuthorization = request.Header.Get("Authorization")
		writeCloudflareEnvelope(t, response, []Zone{{ID: "zone-id", Name: "example.com"}}, 1, 1)
	}))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Location", target.URL+"/zones")
		response.WriteHeader(http.StatusFound)
	}))
	defer source.Close()
	client, err := NewClient(ClientConfig{BaseURL: source.URL, DNSAPIToken: token, HTTPClient: source.Client()})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	if _, err := client.FindZone(context.Background(), "example.com"); err == nil {
		t.Fatal("FindZone(redirect) error = nil")
	}
	if redirectedAuthorization != "" {
		t.Fatalf("redirect target received Authorization = %q", redirectedAuthorization)
	}
}

func TestCloudflareClientRejectsUnsafeConfigurationAndIdentifiers(t *testing.T) {
	tests := []ClientConfig{
		{},
		{DNSAPIToken: "token\nleak"},
		{DNSAPIToken: "token", BaseURL: "ftp://api.example.test"},
		{DNSAPIToken: "token", BaseURL: "https://user@example.test"},
	}
	for _, config := range tests {
		if _, err := NewClient(config); err == nil {
			t.Fatalf("NewClient(%#v) error = nil", config)
		}
	}
	client, err := NewClient(ClientConfig{DNSAPIToken: "token"})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	if _, err := client.FindZone(context.Background(), "bad/name.example"); err == nil {
		t.Fatal("FindZone(invalid) error = nil")
	}
	if err := client.DeleteRecord(context.Background(), "../zone", "record"); err == nil {
		t.Fatal("DeleteRecord(invalid zone ID) error = nil")
	}
	if got := parseProviderRetryAfter("9223372036854775807", time.Now()); got != 0 {
		t.Fatalf("parseProviderRetryAfter(overflow) = %v, want 0", got)
	}
}

func writeCloudflareEnvelope(t *testing.T, response http.ResponseWriter, result any, page, totalPages int) {
	t.Helper()
	response.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(response).Encode(map[string]any{
		"success": true,
		"errors":  []any{},
		"result":  result,
		"result_info": map[string]int{
			"page":        page,
			"total_pages": totalPages,
		},
	}); err != nil {
		t.Errorf("encode response: %v", err)
	}
}
