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
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/pkg/acmeflow"
	"github.com/sakullla/nginx-reverse-emby/go-agent/pkg/acmeflow/cloudflare"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
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
		t.Fatal("cloudflare integration client configuration failed")
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

	issued := issueManagedCertificateIntegration(t, issuer, "master.example.test")
	accountBefore := loadMasterIntegrationAccount(t, dataDir, directoryURL, issuer.email)
	renewed, err := issuer.Renew(t.Context(), ManagedCertificate{ID: 701, Domain: "master.example.test", IssuerMode: "master_cf_dns"})
	if err != nil {
		t.Fatalf("Renew(real Pebble) failed with category %q", acmeflow.ErrorCategoryOf(err))
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
	wildcard := issueManagedCertificateIntegration(t, issuer, "*.wildcard.example.test")
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

	provider.failNextZoneLookup(strings.Join([]string{integrationProviderBody, integrationDNSAPIToken, integrationZoneAPIToken}, ":"))
	assertManagedCertificateIntegrationFailureObservablesDoNotContain(t, dataDir, issuer, provider)
}

func assertManagedCertificateIntegrationFailureObservablesDoNotContain(
	t *testing.T,
	dataDir string,
	issuer managedCertificateRenewalIssuer,
	provider *integrationCloudflareAPI,
) {
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
		ID: certificateID, Domain: "provider-failure.example.test", Enabled: true, Scope: "domain", IssuerMode: "master_cf_dns",
		TargetAgentIDs: `["local"]`, Status: "issuing",
		CertificateType: "acme", Usage: "https", Revision: 1,
	}
	if err := store.SaveManagedCertificates(t.Context(), []storage.ManagedCertificateRow{row}); err != nil {
		t.Fatalf("SaveManagedCertificates() error = %v", err)
	}
	svc := newCertificateServiceWithRenewal(config.Config{
		EnableLocalAgent:              true,
		LocalAgentID:                  "local",
		ManagedDNSCertificatesEnabled: true,
	}, store, issuer)
	dispatcher := newManagedCertificateDispatcher()
	var logBuffer bytes.Buffer
	dispatcher.SetLogger(log.New(&logBuffer, "", 0))
	var issueErr error
	dispatcher.SetSignFunc(func(ctx context.Context, certID int) error {
		rows, err := store.ListManagedCertificates(ctx)
		if err != nil {
			issueErr = err
			return err
		}
		current, index, found := findManagedCertificateByID(rows, certID)
		if !found {
			issueErr = ErrCertificateNotFound
			return issueErr
		}
		_, issueErr = svc.issueManagedCertificateInBackground(ctx, rows, index, current, highestManagedCertificateRevisionForService(rows))
		return issueErr
	})
	if !dispatcher.Submit(certificateID) {
		t.Fatal("provider failure issuance was not dispatched")
	}
	dispatcher.Wait()
	if issueErr == nil || provider.failureCount() != 1 {
		t.Fatal("real provider failure did not traverse the background issuance path")
	}

	publicRows, err := svc.List(t.Context(), "")
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	dbRows, err := store.ListManagedCertificates(t.Context())
	if err != nil {
		t.Fatalf("ListManagedCertificates() error = %v", err)
	}
	if len(publicRows) != 1 || len(dbRows) != 1 || publicRows[0].Status != "error" || dbRows[0].Status != "error" || publicRows[0].LastError == "" {
		t.Fatal("provider failure was not persisted through the service error path")
	}
	snapshot, err := store.LoadAgentSnapshot(t.Context(), "local", storage.AgentSnapshotInput{})
	if err != nil {
		t.Fatalf("LoadAgentSnapshot() error = %v", err)
	}
	snapshot, err = overlayPendingManagedCertificateGenerations(t.Context(), store, "local", snapshot)
	if err != nil {
		t.Fatalf("overlayPendingManagedCertificateGenerations() error = %v", err)
	}
	observables, err := json.Marshal(struct {
		Error    string                          `json:"error"`
		Log      string                          `json:"log"`
		Public   []ManagedCertificate            `json:"public"`
		Database []storage.ManagedCertificateRow `json:"database"`
		Snapshot storage.Snapshot                `json:"snapshot"`
	}{Error: issueErr.Error(), Log: logBuffer.String(), Public: publicRows, Database: dbRows, Snapshot: snapshot})
	if err != nil {
		t.Fatalf("marshal integration observables: %v", err)
	}
	assertIntegrationBytesDoNotContainSensitiveFixture(t, "error/log/API/SQLite snapshot", observables)
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	closed = true
	assertIntegrationTreeDoesNotContain(t, dataDir, integrationDNSAPIToken, integrationZoneAPIToken, integrationProviderBody)

	store, err = storage.NewSQLiteStore(dataDir, "local")
	if err != nil {
		t.Fatalf("restart storage.NewSQLiteStore() error = %v", err)
	}
	closed = false
	restartedService := newCertificateServiceWithRenewal(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, store, issuer)
	publicRows, err = restartedService.List(t.Context(), "")
	if err != nil {
		t.Fatalf("List() after restart error = %v", err)
	}
	dbRows, err = store.ListManagedCertificates(t.Context())
	if err != nil {
		t.Fatalf("ListManagedCertificates() after restart error = %v", err)
	}
	snapshot, err = store.LoadAgentSnapshot(t.Context(), "local", storage.AgentSnapshotInput{})
	if err != nil {
		t.Fatalf("LoadAgentSnapshot() after restart error = %v", err)
	}
	restartedObservables, err := json.Marshal(struct {
		Public   []ManagedCertificate            `json:"public"`
		Database []storage.ManagedCertificateRow `json:"database"`
		Snapshot storage.Snapshot                `json:"snapshot"`
	}{Public: publicRows, Database: dbRows, Snapshot: snapshot})
	if err != nil {
		t.Fatalf("marshal restarted observables: %v", err)
	}
	assertIntegrationBytesDoNotContainSensitiveFixture(t, "restarted API/SQLite snapshot", restartedObservables)
	if err := store.Close(); err != nil {
		t.Fatalf("Close() after restart error = %v", err)
	}
	closed = true
	assertIntegrationTreeDoesNotContain(t, dataDir, integrationDNSAPIToken, integrationZoneAPIToken, integrationProviderBody)
}

func issueManagedCertificateIntegration(t *testing.T, issuer *masterCFDNSManagedCertificateIssuer, domain string) managedCertificateRenewalResult {
	t.Helper()
	result, err := issuer.Issue(t.Context(), ManagedCertificate{ID: 701, Domain: domain, IssuerMode: "master_cf_dns"})
	if err != nil {
		t.Fatalf("Issue(%s, real Pebble) failed with category %q", domain, acmeflow.ErrorCategoryOf(err))
	}
	assertManagedCertificateIntegrationResult(t, result, domain)
	return result
}

func assertManagedCertificateIntegrationResult(t *testing.T, result managedCertificateRenewalResult, domain string) {
	t.Helper()
	if !result.Changed || result.Material.Domain != domain || result.Material.CertPEM == "" || result.Material.KeyPEM == "" {
		t.Fatalf("issued result for %s is missing changed certificate material", domain)
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
	for name, rawURL := range map[string]string{"directory": directoryURL, "challtestsrv": challengeURL} {
		if err := validateManagedCertificateACMEIntegrationFixtureURL(rawURL); err != nil {
			t.Fatalf("invalid %s fixture URL: %v", name, err)
		}
	}
	return directoryURL, challengeURL
}

func TestManagedCertificateACMEIntegrationFixtureURLRequiresExplicitLoopback(t *testing.T) {
	testCases := []struct {
		name    string
		url     string
		wantErr bool
	}{
		{name: "IPv4 loopback", url: "https://127.0.0.1:14000/dir"},
		{name: "IPv6 loopback", url: "http://[::1]:8055"},
		{name: "localhost name", url: "http://localhost:8055", wantErr: true},
		{name: "other IPv4 loopback", url: "http://127.0.0.2:8055", wantErr: true},
		{name: "external host", url: "https://acme.example.com/dir", wantErr: true},
		{name: "userinfo", url: "https://user@127.0.0.1:14000/dir", wantErr: true},
		{name: "unsupported scheme", url: "ftp://127.0.0.1:14000/dir", wantErr: true},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			err := validateManagedCertificateACMEIntegrationFixtureURL(testCase.url)
			if (err != nil) != testCase.wantErr {
				t.Fatalf("fixture URL validation error = %v, wantErr %v", err, testCase.wantErr)
			}
		})
	}
}

func validateManagedCertificateACMEIntegrationFixtureURL(rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil {
		return fmt.Errorf("must be an absolute HTTP(S) URL without userinfo")
	}
	switch parsed.Hostname() {
	case "127.0.0.1", "::1":
		return nil
	default:
		return fmt.Errorf("host must be explicit loopback 127.0.0.1 or ::1")
	}
}

type integrationMasterACMEEngine struct {
	client *http.Client
}

func (engine *integrationMasterACMEEngine) Issue(ctx context.Context, request acmeflow.IssueRequest) (acmeflow.IssueResult, error) {
	request.HTTPClient = engine.client
	return (acmeflow.Engine{}).Issue(ctx, request)
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
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}
	t.Cleanup(transport.CloseIdleConnections)
	return &http.Client{
		Transport: transport,
		Timeout:   30 * time.Second,
	}
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
	failures       int
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
		api.failures++
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

func (api *integrationCloudflareAPI) failureCount() int {
	api.mu.Lock()
	defer api.mu.Unlock()
	return api.failures
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
				return fmt.Errorf("%s contains sensitive fixture data", path)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func assertIntegrationBytesDoNotContainSensitiveFixture(t *testing.T, surface string, data []byte) {
	t.Helper()
	for _, canary := range []string{integrationDNSAPIToken, integrationZoneAPIToken, integrationProviderBody} {
		if bytes.Contains(data, []byte(canary)) {
			t.Fatalf("%s contains sensitive fixture data", surface)
		}
	}
}
