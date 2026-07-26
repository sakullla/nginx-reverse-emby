//go:build integration

package certs

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/pkg/acmeflow"
	"golang.org/x/crypto/acme"
)

const defaultACMEIntegrationValidationIP = "10.30.50.3"

var acmeIntegrationDomainSequence atomic.Uint64

type acmeIntegrationFixture struct {
	directoryURL string
	challengeURL string
	validationIP string
	acmeClient   *http.Client
	challenge    *http.Client
}

type acmeIntegrationClock struct {
	mu  sync.RWMutex
	now time.Time
}

func newACMEIntegrationClock(now time.Time) *acmeIntegrationClock {
	return &acmeIntegrationClock{now: now.UTC()}
}

func (clock *acmeIntegrationClock) Now() time.Time {
	clock.mu.RLock()
	defer clock.mu.RUnlock()
	return clock.now
}

func (clock *acmeIntegrationClock) Set(now time.Time) {
	clock.mu.Lock()
	clock.now = now.UTC()
	clock.mu.Unlock()
}

type acmeIntegrationIssueObservation struct {
	CertificateID  int
	Scope          string
	Profile        string
	ExistingKey    bool
	ExistingKeySHA [sha256.Size]byte
}

type acmeIntegrationIssueRecorder struct {
	mu           sync.Mutex
	observations []acmeIntegrationIssueObservation
}

func (recorder *acmeIntegrationIssueRecorder) Record(request acmeIssueRequest) {
	if recorder == nil {
		return
	}
	observation := acmeIntegrationIssueObservation{
		CertificateID: request.CertificateID,
		Scope:         request.Scope,
		Profile:       request.Profile,
		ExistingKey:   len(request.ExistingKeyPEM) > 0,
	}
	if observation.ExistingKey {
		observation.ExistingKeySHA = sha256.Sum256(request.ExistingKeyPEM)
	}
	recorder.mu.Lock()
	recorder.observations = append(recorder.observations, observation)
	recorder.mu.Unlock()
}

func (recorder *acmeIntegrationIssueRecorder) Snapshot() []acmeIntegrationIssueObservation {
	if recorder == nil {
		return nil
	}
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	return append([]acmeIntegrationIssueObservation(nil), recorder.observations...)
}

type challtestsrvHTTP01Solver struct {
	baseURL string
	client  *http.Client
}

func (*challtestsrvHTTP01Solver) ChallengeType() string {
	return acmeflow.ChallengeHTTP01
}

func (solver *challtestsrvHTTP01Solver) Present(ctx context.Context, challenge acmeflow.Challenge) error {
	return solver.post(ctx, "/add-http01", struct {
		Token   string `json:"token"`
		Content string `json:"content"`
	}{Token: challenge.Token, Content: challenge.KeyAuthorization})
}

func (*challtestsrvHTTP01Solver) Wait(ctx context.Context, _ acmeflow.Challenge) error {
	return ctx.Err()
}

func (solver *challtestsrvHTTP01Solver) Cleanup(ctx context.Context, challenge acmeflow.Challenge) error {
	return solver.post(ctx, "/del-http01", struct {
		Token string `json:"token"`
	}{Token: challenge.Token})
}

func (solver *challtestsrvHTTP01Solver) post(ctx context.Context, endpoint string, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode challtestsrv request: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(solver.baseURL, "/")+endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build challtestsrv request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := solver.client.Do(request)
	if err != nil {
		return fmt.Errorf("call challtestsrv %s: %w", endpoint, err)
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("challtestsrv %s returned %s", endpoint, response.Status)
	}
	return nil
}

func TestIntegrationACMEIntegrationDomainAndIPIssuanceAndRenewal(t *testing.T) {
	t.Parallel()
	fixture := requireACMEIntegrationFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	clock := newACMEIntegrationClock(time.Now().UTC().Truncate(time.Second))
	recorder := &acmeIntegrationIssueRecorder{}
	dataDir := t.TempDir()
	manager := newACMEIntegrationManager(t, dataDir, fixture, clock, recorder)

	domainPolicy := localHTTP01Policy(7201, uniqueACMEIntegrationDomain("domain"))
	ipPolicy := localHTTP01Policy(7202, fixture.validationIP)
	ipPolicy.Scope = "ip"
	if err := manager.Apply(ctx, nil, []model.ManagedCertificatePolicy{domainPolicy, ipPolicy}); err != nil {
		t.Fatalf("issue domain and IP certificates: %v", err)
	}

	domainCertificate := requireServerCertificate(t, manager, domainPolicy)
	if err := domainCertificate.Leaf.VerifyHostname(domainPolicy.Domain); err != nil {
		t.Fatalf("domain certificate does not cover %q: %v", domainPolicy.Domain, err)
	}
	domainGeneration := loadCurrentGeneration(t, manager, domainPolicy.ID, clock.Now())
	if domainGeneration.Manifest.Profile != "" {
		t.Fatalf("domain generation profile = %q, want empty default profile", domainGeneration.Manifest.Profile)
	}

	initialIPCertificate := requireServerCertificate(t, manager, ipPolicy)
	if err := initialIPCertificate.Leaf.VerifyHostname(ipPolicy.Domain); err != nil {
		t.Fatalf("IP certificate does not cover %q: %v", ipPolicy.Domain, err)
	}
	if initialIPCertificate.Leaf.Subject.CommonName != "" {
		t.Fatalf("IP certificate common name = %q, want empty", initialIPCertificate.Leaf.Subject.CommonName)
	}
	if lifetime := initialIPCertificate.Leaf.NotAfter.Sub(initialIPCertificate.Leaf.NotBefore); lifetime > 7*24*time.Hour {
		t.Fatalf("IP certificate lifetime = %s, want shortlived profile", lifetime)
	}
	initialIPGeneration := loadCurrentGeneration(t, manager, ipPolicy.ID, clock.Now())
	if initialIPGeneration.Manifest.Profile != "shortlived" {
		t.Fatalf("IP generation profile = %q, want shortlived", initialIPGeneration.Manifest.Profile)
	}

	initialObservations := recorder.Snapshot()
	if len(initialObservations) != 2 {
		t.Fatalf("initial issuance observations = %#v, want two", initialObservations)
	}
	assertInitialACMEIntegrationProfile(t, initialObservations, domainPolicy.ID, "")
	assertInitialACMEIntegrationProfile(t, initialObservations, ipPolicy.ID, "shortlived")
	// Keep the single-iteration renewal assertion focused on the IP policy. An
	// unprofiled order may legitimately receive Pebble's shortlived server
	// default, which would also make the domain policy immediately renewable
	// under the Agent's ordinary 30-day threshold.
	if err := manager.Apply(ctx, nil, []model.ManagedCertificatePolicy{ipPolicy}); err != nil {
		t.Fatalf("focus active state on the IP certificate: %v", err)
	}

	renewAt := initialIPCertificate.Leaf.NotAfter.Add(-manager.renewBeforeForScope(initialIPCertificate.Leaf, ipPolicy.Scope)).Add(time.Second)
	clock.Set(renewAt)
	if err := manager.runRenewalLoopIteration(ctx); err != nil {
		t.Fatalf("renew shortlived IP certificate: %v", err)
	}

	renewedIPCertificate := requireServerCertificate(t, manager, ipPolicy)
	if bytes.Equal(renewedIPCertificate.Leaf.Raw, initialIPCertificate.Leaf.Raw) {
		t.Fatal("IP renewal kept the previous leaf certificate")
	}
	if err := renewedIPCertificate.Leaf.VerifyHostname(ipPolicy.Domain); err != nil {
		t.Fatalf("renewed IP certificate does not cover %q: %v", ipPolicy.Domain, err)
	}
	renewedIPGeneration := loadCurrentGeneration(t, manager, ipPolicy.ID, clock.Now())
	if renewedIPGeneration.Manifest.ID == initialIPGeneration.Manifest.ID {
		t.Fatalf("IP renewal generation remained %q", renewedIPGeneration.Manifest.ID)
	}
	if renewedIPGeneration.Manifest.Profile != "shortlived" {
		t.Fatalf("renewed IP profile = %q, want shortlived", renewedIPGeneration.Manifest.Profile)
	}
	if !bytes.Equal(renewedIPGeneration.Material.PrivateKeyPEM, initialIPGeneration.Material.PrivateKeyPEM) {
		t.Fatal("IP renewal did not reuse the active certificate private key")
	}
	if currentDomain := loadCurrentGeneration(t, manager, domainPolicy.ID, clock.Now()); currentDomain.Manifest.ID != domainGeneration.Manifest.ID {
		t.Fatalf("IP renewal unexpectedly changed domain generation: %q -> %q", domainGeneration.Manifest.ID, currentDomain.Manifest.ID)
	}

	observations := recorder.Snapshot()
	if len(observations) != 3 {
		t.Fatalf("issuance observations after renewal = %#v, want three", observations)
	}
	renewalObservation := observations[2]
	wantKeyHash := sha256.Sum256(initialIPGeneration.Material.PrivateKeyPEM)
	if renewalObservation.CertificateID != ipPolicy.ID || renewalObservation.Scope != "ip" || renewalObservation.Profile != "shortlived" || !renewalObservation.ExistingKey || renewalObservation.ExistingKeySHA != wantKeyHash {
		t.Fatalf("IP renewal request = %#v, want shortlived request with reused key", renewalObservation)
	}

	report := requireManagedCertificateReport(t, manager, ipPolicy.ID)
	wantMaterialHash := hashManagedCertificateMaterial(renewedIPGeneration.Material.CertificatePEM, renewedIPGeneration.Material.PrivateKeyPEM)
	if report.MaterialHash != wantMaterialHash {
		t.Fatalf("renewed report material hash = %q, want %q", report.MaterialHash, wantMaterialHash)
	}
	assertACMEIntegrationSensitiveModes(t, dataDir)
}

func requireACMEIntegrationFixture(t *testing.T) acmeIntegrationFixture {
	t.Helper()
	if testing.Short() {
		t.Skip("real Pebble ACME integration is disabled in the short tier")
	}

	directoryURL := strings.TrimSpace(os.Getenv("NRE_ACME_TEST_DIRECTORY_URL"))
	managementURL := strings.TrimSpace(os.Getenv("NRE_ACME_TEST_MANAGEMENT_URL"))
	challengeURL := strings.TrimSpace(os.Getenv("NRE_ACME_TEST_CHALLTESTSRV_URL"))
	caPath := strings.TrimSpace(os.Getenv("SSL_CERT_FILE"))
	if directoryURL == "" || managementURL == "" || challengeURL == "" || caPath == "" {
		t.Skip("Pebble fixture contract is not configured")
	}
	for name, rawURL := range map[string]string{
		"directory":    directoryURL,
		"management":   managementURL,
		"challtestsrv": challengeURL,
	} {
		if err := validateACMEIntegrationFixtureURL(rawURL); err != nil {
			t.Fatalf("invalid %s fixture URL %q: %v", name, rawURL, err)
		}
	}

	caPEM, err := os.ReadFile(caPath)
	if err != nil {
		t.Fatalf("read Pebble test CA: %v", err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caPEM) {
		t.Fatal("Pebble test CA contains no certificates")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}
	acmeClient := &http.Client{Transport: transport, Timeout: 20 * time.Second}
	challengeClient := &http.Client{Timeout: 10 * time.Second}
	t.Cleanup(func() {
		transport.CloseIdleConnections()
		challengeClient.CloseIdleConnections()
	})

	probeACMEIntegrationEndpoint(t, acmeClient, directoryURL, http.StatusOK)
	probeACMEIntegrationEndpoint(t, acmeClient, managementURL, 0)
	probeACMEIntegrationEndpoint(t, challengeClient, challengeURL, 0)

	validationIP := strings.TrimSpace(os.Getenv("NRE_ACME_TEST_IP"))
	if validationIP == "" {
		validationIP = defaultACMEIntegrationValidationIP
	}
	if net.ParseIP(validationIP) == nil {
		t.Fatalf("invalid Pebble validation IP %q", validationIP)
	}
	return acmeIntegrationFixture{
		directoryURL: directoryURL,
		challengeURL: challengeURL,
		validationIP: validationIP,
		acmeClient:   acmeClient,
		challenge:    challengeClient,
	}
}

func probeACMEIntegrationEndpoint(t *testing.T, client *http.Client, endpoint string, wantStatus int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		t.Fatalf("build fixture probe for %q: %v", endpoint, err)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("probe fixture endpoint %q: %v", endpoint, err)
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	if wantStatus != 0 && response.StatusCode != wantStatus {
		t.Fatalf("fixture endpoint %q returned %s, want %d", endpoint, response.Status, wantStatus)
	}
	if wantStatus == 0 && response.StatusCode >= http.StatusInternalServerError {
		t.Fatalf("fixture endpoint %q returned %s", endpoint, response.Status)
	}
}

func TestIntegrationACMEIntegrationFixtureURLRequiresExplicitLoopback(t *testing.T) {
	t.Parallel()
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
			err := validateACMEIntegrationFixtureURL(testCase.url)
			if (err != nil) != testCase.wantErr {
				t.Fatalf("validateACMEIntegrationFixtureURL(%q) error = %v, wantErr %v", testCase.url, err, testCase.wantErr)
			}
		})
	}
}

func validateACMEIntegrationFixtureURL(rawURL string) error {
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

func newACMEIntegrationManager(
	t *testing.T,
	dataDir string,
	fixture acmeIntegrationFixture,
	clock *acmeIntegrationClock,
	recorder *acmeIntegrationIssueRecorder,
	extra ...Option,
) *Manager {
	t.Helper()
	options := []Option{
		WithACMEDirectoryURL(fixture.directoryURL),
		WithACMEEmail(""),
		withNow(clock.Now),
		withRenewalLoopInterval(24 * time.Hour),
		withRenewalAttemptTimeout(90 * time.Second),
		withACMEIssuerFactory(fixture.issuerFactory(clock.Now, recorder)),
	}
	options = append(options, extra...)
	manager := mustNewManager(t, dataDir, options...)
	t.Cleanup(func() { _ = manager.Close() })
	return manager
}

func (fixture acmeIntegrationFixture) issuerFactory(now func() time.Time, recorder *acmeIntegrationIssueRecorder) acmeIssuerFactory {
	return func(request acmeIssueRequest) (acmeIssuer, error) {
		recorder.Record(request)
		engine := acmeflow.Engine{
			ClientFactory: func(config acmeflow.ClientConfig) acmeflow.ProtocolClient {
				config.HTTPClient = fixture.acmeClient
				return acmeflow.NewProtocolClient(config)
			},
			OrderStarter: acmeflow.OrderStarterFunc(func(ctx context.Context, order acmeflow.OrderStartRequest) (*acme.Order, error) {
				order.HTTPClient = fixture.acmeClient
				return (acmeflow.DefaultOrderStarter{}).StartOrder(ctx, order)
			}),
			Now:            now,
			CleanupTimeout: 10 * time.Second,
		}
		return acmeflowACMEIssuer{
			engine: engine,
			solverFactory: func(issueRequest acmeIssueRequest) (acmeflow.ChallengeSolver, error) {
				if issueRequest.ChallengeType != challengeTypeHTTP01 {
					return nil, fmt.Errorf("integration solver does not support %q", issueRequest.ChallengeType)
				}
				return &challtestsrvHTTP01Solver{baseURL: fixture.challengeURL, client: fixture.challenge}, nil
			},
		}, nil
	}
}

func uniqueACMEIntegrationDomain(prefix string) string {
	sequence := acmeIntegrationDomainSequence.Add(1)
	return fmt.Sprintf("%s-%d-%d.example.test", prefix, os.Getpid(), sequence)
}

func requireServerCertificate(t *testing.T, manager *Manager, policy model.ManagedCertificatePolicy) *tls.Certificate {
	t.Helper()
	certificate, err := manager.ServerCertificate(context.Background(), policy.ID)
	if err != nil {
		t.Fatalf("load server certificate %d: %v", policy.ID, err)
	}
	if certificate.Leaf == nil {
		t.Fatalf("server certificate %d has no parsed leaf", policy.ID)
	}
	return certificate
}

func requireManagedCertificateReport(t *testing.T, manager *Manager, certificateID int) model.ManagedCertificateReport {
	t.Helper()
	reports, err := manager.ManagedCertificateReports(context.Background())
	if err != nil {
		t.Fatalf("load managed certificate reports: %v", err)
	}
	for _, report := range reports {
		if report.ID == certificateID {
			return report
		}
	}
	t.Fatalf("managed certificate report %d is absent: %#v", certificateID, reports)
	return model.ManagedCertificateReport{}
}

func assertInitialACMEIntegrationProfile(t *testing.T, observations []acmeIntegrationIssueObservation, certificateID int, profile string) {
	t.Helper()
	for _, observation := range observations {
		if observation.CertificateID != certificateID {
			continue
		}
		if observation.Profile != profile || observation.ExistingKey {
			t.Fatalf("initial issuance observation = %#v, want profile %q without an existing key", observation, profile)
		}
		return
	}
	t.Fatalf("initial issuance observation for certificate %d is absent", certificateID)
}

func assertACMEIntegrationSensitiveModes(t *testing.T, root string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		return
	}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode().Perm()&0o077 != 0 {
			return fmt.Errorf("sensitive state file %s has permissions %o", path, info.Mode().Perm())
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
