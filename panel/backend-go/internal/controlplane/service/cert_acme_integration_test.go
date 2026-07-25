//go:build integration

package service

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/pkg/acmeflow"
	"github.com/sakullla/nginx-reverse-emby/go-agent/pkg/acmeflow/cloudflare"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

const (
	integrationDNSAPIToken  = "t13-dns-token-canary"
	integrationZoneAPIToken = "t13-zone-token-canary"
	integrationProviderBody = "t13-raw-provider-body-canary"
)

func TestManagedCertificateACMEIntegrationRealPebbleDNS01(t *testing.T) {
	directoryURL, challengeURL := requireManagedCertificateACMEFixture(t)
	dataDir := t.TempDir()
	provider := newIntegrationCloudflareAPI(t, challengeURL)
	server := httptest.NewServer(http.HandlerFunc(provider.serveHTTP))
	t.Cleanup(server.Close)
	client, err := cloudflare.NewClient(cloudflare.ClientConfig{
		DNSAPIToken:  integrationDNSAPIToken,
		ZoneAPIToken: integrationZoneAPIToken,
		BaseURL:      server.URL + "/client/v4",
		APITimeout:   10 * time.Second,
	})
	if err != nil {
		t.Fatalf("cloudflare.NewClient() error = %v", err)
	}
	propagation := &integrationDNSPropagation{provider: provider}
	realEngine := &integrationMasterACMEEngine{client: integrationACMEHTTPClient(t)}
	issuer := &masterCFDNSManagedCertificateIssuer{
		directoryURL: directoryURL,
		email:        "integration@example.com",
		cfToken:      integrationDNSAPIToken,
		cfZoneToken:  integrationZoneAPIToken,
		dataDir:      dataDir,
		engine:       realEngine,
		openState: func(root string) (masterACMEStateStore, error) {
			return openMasterACMEAccountStore(root)
		},
		now: time.Now,
	}
	issuer.newSolver = func(state masterACMEStateStore) (masterACMESolver, error) {
		return cloudflare.NewDNS01Solver(cloudflare.DNS01Config{
			Client:      client,
			Propagation: propagation,
			Intents:     state,
		})
	}

	issued := issueManagedCertificateIntegration(t, issuer, realEngine, "master.example.test")
	accountBefore := loadMasterIntegrationAccount(t, dataDir, directoryURL, issuer.email)
	renewed, err := issuer.Renew(t.Context(), ManagedCertificate{ID: 701, Domain: "master.example.test", IssuerMode: "master_cf_dns"})
	if err != nil {
		t.Fatalf("Renew(real Pebble) error = %v", err)
	}
	accountAfter := loadMasterIntegrationAccount(t, dataDir, directoryURL, issuer.email)
	if !bytes.Equal(accountBefore.KeyPEM, accountAfter.KeyPEM) || accountBefore.Metadata.URI != accountAfter.Metadata.URI {
		t.Fatal("real Pebble renewal did not reuse the persisted master account")
	}
	if issued.Material.KeyPEM == renewed.Material.KeyPEM {
		t.Fatal("master renewal reused the certificate private key")
	}
	assertManagedCertificateIntegrationResult(t, renewed, "master.example.test")

	propagation.cnameSource = "_acme-challenge.wildcard.example.test"
	propagation.cnameTarget = "_acme-challenge.delegated.example.test"
	t.Cleanup(func() {
		_ = postChallengeManagement(context.Background(), challengeURL+"/clear-cname", map[string]any{"host": propagation.cnameSource})
	})
	wildcard := issueManagedCertificateIntegration(t, issuer, realEngine, "*.wildcard.example.test")
	if !strings.Contains(wildcard.ACMEInfo.SANDomains, "*.wildcard.example.test") {
		t.Fatalf("wildcard SANs = %q", wildcard.ACMEInfo.SANDomains)
	}

	if provider.createdCount() < 3 || provider.deletedCount() != provider.createdCount() || provider.recordCount() != 0 {
		t.Fatalf("Cloudflare lifecycle = created %d deleted %d active %d", provider.createdCount(), provider.deletedCount(), provider.recordCount())
	}
	if !provider.sawScopedAuthorization() {
		t.Fatal("Cloudflare zone and DNS requests did not preserve token scope")
	}
	if propagation.waitCount() < 3 {
		t.Fatalf("authoritative propagation checks = %d, want at least 3", propagation.waitCount())
	}

	provider.failNextZoneLookup(integrationProviderBody + ":" + integrationDNSAPIToken)
	_, err = issuer.Issue(t.Context(), ManagedCertificate{ID: 702, Domain: "provider-failure.example.test", IssuerMode: "master_cf_dns"})
	if err == nil {
		t.Fatal("Issue(provider failure) error = nil")
	}
	for _, canary := range []string{integrationDNSAPIToken, integrationZoneAPIToken, integrationProviderBody} {
		if strings.Contains(err.Error(), canary) {
			t.Fatalf("provider failure exposed canary %q: %v", canary, err)
		}
	}
	assertIntegrationTreeDoesNotContain(t, filepath.Join(dataDir, "acme", "master"), integrationDNSAPIToken, integrationZoneAPIToken, integrationProviderBody)
	assertManagedCertificateIntegrationObservablesDoNotContain(t, dataDir, issued, err)
}

func assertManagedCertificateIntegrationObservablesDoNotContain(t *testing.T, dataDir string, issued managedCertificateRenewalResult, providerErr error) {
	t.Helper()
	store, err := storage.NewSQLiteStore(dataDir, "local")
	if err != nil {
		t.Fatalf("storage.NewSQLiteStore() error = %v", err)
	}
	closed := false
	t.Cleanup(func() {
		if !closed {
			_ = store.Close()
		}
	})
	const certificateID = 703
	row := storage.ManagedCertificateRow{
		ID: certificateID, Domain: issued.Material.Domain, Enabled: true, Scope: "domain", IssuerMode: "master_cf_dns",
		TargetAgentIDs: `["edge-a"]`, Status: "pending", LastError: providerErr.Error(),
		CertificateType: "acme", Usage: "https", Revision: 1,
	}
	if err := store.SaveManagedCertificates(t.Context(), []storage.ManagedCertificateRow{row}); err != nil {
		t.Fatalf("SaveManagedCertificates() error = %v", err)
	}
	if _, err := store.StageManagedCertificateGeneration(t.Context(), issued.Material.Domain, issued.Material); err != nil {
		t.Fatalf("StageManagedCertificateGeneration() error = %v", err)
	}
	snapshot, err := overlayPendingManagedCertificateGenerations(t.Context(), store, "edge-a", storage.Snapshot{})
	if err != nil {
		t.Fatalf("overlayPendingManagedCertificateGenerations() error = %v", err)
	}
	rows, err := store.ListManagedCertificates(t.Context())
	if err != nil {
		t.Fatalf("ListManagedCertificates() error = %v", err)
	}
	observables, err := json.Marshal(struct {
		Rows     []storage.ManagedCertificateRow `json:"rows"`
		Snapshot storage.Snapshot                `json:"snapshot"`
	}{Rows: rows, Snapshot: snapshot})
	if err != nil {
		t.Fatalf("marshal integration observables: %v", err)
	}
	for _, canary := range []string{integrationDNSAPIToken, integrationZoneAPIToken, integrationProviderBody} {
		if bytes.Contains(observables, []byte(canary)) {
			t.Fatalf("API row or snapshot exposed canary %q", canary)
		}
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	closed = true
	assertIntegrationTreeDoesNotContain(t, dataDir, integrationDNSAPIToken, integrationZoneAPIToken, integrationProviderBody)
}

func issueManagedCertificateIntegration(t *testing.T, issuer *masterCFDNSManagedCertificateIssuer, engine *integrationMasterACMEEngine, domain string) managedCertificateRenewalResult {
	t.Helper()
	result, err := issuer.Issue(t.Context(), ManagedCertificate{ID: 701, Domain: domain, IssuerMode: "master_cf_dns"})
	if err != nil {
		trace, _ := engine.client.Transport.(*integrationTraceTransport)
		finalizeBody := ""
		if trace != nil {
			finalizeBody = trace.lastFinalizeBody()
		}
		t.Fatalf("Issue(%s, real Pebble) error = %v; engine cause = %s; finalize response = %s", domain, err, integrationErrorChain(engine.lastErr), finalizeBody)
	}
	assertManagedCertificateIntegrationResult(t, result, domain)
	return result
}

func assertManagedCertificateIntegrationResult(t *testing.T, result managedCertificateRenewalResult, domain string) {
	t.Helper()
	if !result.Changed || result.Material.Domain != domain || result.Material.CertPEM == "" || result.Material.KeyPEM == "" {
		t.Fatalf("issued result for %s = %+v", domain, result)
	}
	if result.MaterialHash != hashManagedCertificateMaterial(result.Material.CertPEM, result.Material.KeyPEM) {
		t.Fatalf("material hash for %s does not match returned material", domain)
	}
	leaf, err := parseManagedCertificateLeaf([]byte(result.Material.CertPEM))
	if err != nil {
		t.Fatalf("parse issued certificate for %s: %v", domain, err)
	}
	if err := leaf.VerifyHostname(domain); err != nil {
		t.Fatalf("issued certificate hostname %s: %v", domain, err)
	}
	if result.ACMEInfo.MainDomain != domain || result.ACMEInfo.CA == "" || result.NotAfter != leaf.NotAfter.UTC().Format(time.RFC3339) {
		t.Fatalf("issued metadata for %s = %+v, not_after=%q", domain, result.ACMEInfo, result.NotAfter)
	}
}

func requireManagedCertificateACMEFixture(t *testing.T) (string, string) {
	t.Helper()
	if testing.Short() {
		t.Skip("real ACME lifecycle runs in the full integration tier")
	}
	directoryURL := strings.TrimSpace(os.Getenv("NRE_ACME_TEST_DIRECTORY_URL"))
	challengeURL := strings.TrimRight(strings.TrimSpace(os.Getenv("NRE_ACME_TEST_CHALLTESTSRV_URL")), "/")
	if directoryURL == "" || challengeURL == "" || strings.TrimSpace(os.Getenv("SSL_CERT_FILE")) == "" {
		t.Skip("local ACME fixture contract is not configured")
	}
	return directoryURL, challengeURL
}

type integrationMasterACMEEngine struct {
	client  *http.Client
	lastErr error
}

func (engine *integrationMasterACMEEngine) Issue(ctx context.Context, request acmeflow.IssueRequest) (acmeflow.IssueResult, error) {
	request.HTTPClient = engine.client
	result, err := (acmeflow.Engine{}).Issue(ctx, request)
	engine.lastErr = err
	return result, err
}

func integrationErrorChain(err error) string {
	parts := make([]string, 0, 4)
	for err != nil && len(parts) < 8 {
		parts = append(parts, fmt.Sprintf("%T: %v", err, err))
		err = errors.Unwrap(err)
	}
	return strings.Join(parts, " <- ")
}

func integrationACMEHTTPClient(t *testing.T) *http.Client {
	t.Helper()
	caPEM, err := os.ReadFile(strings.TrimSpace(os.Getenv("SSL_CERT_FILE")))
	if err != nil {
		t.Fatalf("read Pebble CA: %v", err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caPEM) {
		t.Fatal("Pebble CA file contains no certificate")
	}
	transport := &integrationTraceTransport{next: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}}
	return &http.Client{
		Transport: transport,
		Timeout:   30 * time.Second,
	}
}

type integrationTraceTransport struct {
	next         http.RoundTripper
	mu           sync.Mutex
	finalizeBody string
}

func (transport *integrationTraceTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	response, err := transport.next.RoundTrip(request)
	if err != nil || response == nil || !strings.Contains(request.URL.Path, "/finalize-order/") {
		return response, err
	}
	body, readErr := io.ReadAll(response.Body)
	if readErr != nil {
		return nil, readErr
	}
	_ = response.Body.Close()
	response.Body = io.NopCloser(bytes.NewReader(body))
	transport.mu.Lock()
	transport.finalizeBody = string(body)
	transport.mu.Unlock()
	return response, nil
}

func (transport *integrationTraceTransport) lastFinalizeBody() string {
	transport.mu.Lock()
	defer transport.mu.Unlock()
	return transport.finalizeBody
}

func loadMasterIntegrationAccount(t *testing.T, dataDir, directoryURL, email string) acmeflow.AccountRecord {
	t.Helper()
	store, err := openMasterACMEAccountStore(dataDir)
	if err != nil {
		t.Fatalf("open master account store: %v", err)
	}
	defer store.Close()
	record, err := store.LoadAccount(t.Context(), acmeflow.AccountLookup{DirectoryURL: directoryURL, Email: email})
	if err != nil {
		t.Fatalf("load master account: %v", err)
	}
	return record
}

type integrationCloudflareAPI struct {
	t              *testing.T
	challengeURL   string
	mu             sync.Mutex
	records        map[string]cloudflare.TXTRecord
	created        int
	deleted        int
	nextID         int
	authorizations []string
	failBody       string
}

func newIntegrationCloudflareAPI(t *testing.T, challengeURL string) *integrationCloudflareAPI {
	return &integrationCloudflareAPI{t: t, challengeURL: challengeURL, records: make(map[string]cloudflare.TXTRecord)}
}

func (api *integrationCloudflareAPI) serveHTTP(response http.ResponseWriter, request *http.Request) {
	api.mu.Lock()
	api.authorizations = append(api.authorizations, request.URL.Path+"="+request.Header.Get("Authorization"))
	if api.failBody != "" && request.URL.Path == "/client/v4/zones" {
		body := api.failBody
		api.failBody = ""
		api.mu.Unlock()
		http.Error(response, body, http.StatusInternalServerError)
		return
	}
	api.mu.Unlock()

	switch {
	case request.Method == http.MethodGet && request.URL.Path == "/client/v4/zones":
		name := strings.TrimSuffix(request.URL.Query().Get("name"), ".")
		result := []cloudflare.Zone{}
		if name == "example.test" {
			result = append(result, cloudflare.Zone{ID: "zone-example-test", Name: name, Status: "active"})
		}
		writeIntegrationProviderJSON(response, result, true)
	case request.URL.Path == "/client/v4/zones/zone-example-test/dns_records" && request.Method == http.MethodGet:
		api.mu.Lock()
		result := make([]cloudflare.TXTRecord, 0, len(api.records))
		for _, record := range api.records {
			if record.Name == request.URL.Query().Get("name.exact") {
				result = append(result, record)
			}
		}
		api.mu.Unlock()
		writeIntegrationProviderJSON(response, result, true)
	case request.URL.Path == "/client/v4/zones/zone-example-test/dns_records" && request.Method == http.MethodPost:
		var input struct {
			Type    string `json:"type"`
			Name    string `json:"name"`
			Content string `json:"content"`
			TTL     int    `json:"ttl"`
		}
		if err := json.NewDecoder(io.LimitReader(request.Body, 1<<20)).Decode(&input); err != nil {
			http.Error(response, "bad request", http.StatusBadRequest)
			return
		}
		value := strings.TrimSuffix(strings.TrimPrefix(input.Content, "\""), "\"")
		api.mu.Lock()
		api.nextID++
		record := cloudflare.TXTRecord{ID: fmt.Sprintf("record-%d", api.nextID), Name: input.Name, Content: input.Content, TTL: input.TTL, Type: input.Type}
		api.records[record.ID] = record
		api.created++
		api.mu.Unlock()
		if err := postChallengeManagement(request.Context(), api.challengeURL+"/set-txt", map[string]any{"host": input.Name, "value": value}); err != nil {
			http.Error(response, "authoritative update failed", http.StatusBadGateway)
			return
		}
		writeIntegrationProviderJSON(response, record, false)
	case request.Method == http.MethodDelete && strings.HasPrefix(request.URL.Path, "/client/v4/zones/zone-example-test/dns_records/"):
		recordID := strings.TrimPrefix(request.URL.Path, "/client/v4/zones/zone-example-test/dns_records/")
		api.mu.Lock()
		record, ok := api.records[recordID]
		if ok {
			delete(api.records, recordID)
			api.deleted++
		}
		api.mu.Unlock()
		if !ok {
			http.NotFound(response, request)
			return
		}
		if err := postChallengeManagement(request.Context(), api.challengeURL+"/clear-txt", map[string]any{"host": record.Name}); err != nil {
			http.Error(response, "authoritative cleanup failed", http.StatusBadGateway)
			return
		}
		writeIntegrationProviderJSON(response, map[string]string{"id": recordID}, false)
	default:
		http.NotFound(response, request)
	}
}

func writeIntegrationProviderJSON(response http.ResponseWriter, result any, paged bool) {
	response.Header().Set("Content-Type", "application/json")
	payload := map[string]any{"success": true, "result": result}
	if paged {
		payload["result_info"] = map[string]int{"page": 1, "total_pages": 1}
	}
	_ = json.NewEncoder(response).Encode(payload)
}

func (api *integrationCloudflareAPI) failNextZoneLookup(body string) {
	api.mu.Lock()
	defer api.mu.Unlock()
	api.failBody = body
}

func (api *integrationCloudflareAPI) hasTXT(name, value string) bool {
	api.mu.Lock()
	defer api.mu.Unlock()
	for _, record := range api.records {
		if record.Name == name && strings.Trim(record.Content, "\"") == value {
			return true
		}
	}
	return false
}

func (api *integrationCloudflareAPI) createdCount() int {
	api.mu.Lock()
	defer api.mu.Unlock()
	return api.created
}

func (api *integrationCloudflareAPI) deletedCount() int {
	api.mu.Lock()
	defer api.mu.Unlock()
	return api.deleted
}

func (api *integrationCloudflareAPI) recordCount() int {
	api.mu.Lock()
	defer api.mu.Unlock()
	return len(api.records)
}

func (api *integrationCloudflareAPI) sawScopedAuthorization() bool {
	api.mu.Lock()
	defer api.mu.Unlock()
	zone, dns := false, false
	for _, observed := range api.authorizations {
		if observed == "/client/v4/zones=Bearer "+integrationZoneAPIToken {
			zone = true
		}
		if strings.HasPrefix(observed, "/client/v4/zones/zone-example-test/dns_records=Bearer "+integrationDNSAPIToken) {
			dns = true
		}
	}
	return zone && dns
}

type integrationDNSPropagation struct {
	provider    *integrationCloudflareAPI
	mu          sync.Mutex
	cnameSource string
	cnameTarget string
	waits       int
}

func (propagation *integrationDNSPropagation) ResolveCNAME(ctx context.Context, name string) (string, error) {
	propagation.mu.Lock()
	source, target := propagation.cnameSource, propagation.cnameTarget
	propagation.mu.Unlock()
	if source != "" && strings.TrimSuffix(name, ".") == source {
		if err := postChallengeManagement(ctx, propagation.provider.challengeURL+"/set-cname", map[string]any{"host": source, "target": target}); err != nil {
			return "", err
		}
		return target, nil
	}
	return strings.TrimSuffix(name, "."), nil
}

func (propagation *integrationDNSPropagation) WaitTXT(_ context.Context, name, value, _ string) error {
	if !propagation.provider.hasTXT(name, value) {
		return errors.New("authoritative TXT fixture was not updated")
	}
	propagation.mu.Lock()
	propagation.waits++
	propagation.mu.Unlock()
	return nil
}

func (propagation *integrationDNSPropagation) waitCount() int {
	propagation.mu.Lock()
	defer propagation.mu.Unlock()
	return propagation.waits
}

func postChallengeManagement(ctx context.Context, endpoint string, input any) error {
	payload, err := json.Marshal(input)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("challenge management returned %s", response.Status)
	}
	return nil
}

func assertIntegrationTreeDoesNotContain(t *testing.T, root string, canaries ...string) {
	t.Helper()
	if err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, canary := range canaries {
			if canary != "" && bytes.Contains(data, []byte(canary)) {
				return fmt.Errorf("%s contains canary %q", path, canary)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
