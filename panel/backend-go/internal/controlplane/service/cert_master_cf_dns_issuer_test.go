package service

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/pkg/acmeflow"
)

func TestMasterCFDNSConstructorPreservesTokenAliasesAndScope(t *testing.T) {
	dnsAliases := []string{"CLOUDFLARE_DNS_API_TOKEN", "CF_DNS_API_TOKEN", "CF_TOKEN", "CF_Token"}
	zoneAliases := []string{"CLOUDFLARE_ZONE_API_TOKEN", "CF_ZONE_API_TOKEN"}
	for index, dnsAlias := range dnsAliases {
		t.Run(dnsAlias, func(t *testing.T) {
			for _, key := range append(append([]string(nil), dnsAliases...), zoneAliases...) {
				t.Setenv(key, "")
			}
			dataDir := t.TempDir()
			t.Setenv(dnsAlias, " dns-token-canary ")
			zoneAlias := zoneAliases[index%len(zoneAliases)]
			t.Setenv(zoneAlias, " zone-token-canary ")
			t.Setenv("NRE_CONTROL_PLANE_DATA_DIR", dataDir)
			t.Setenv("PANEL_DATA_ROOT", "")
			t.Setenv("NRE_ACME_DIRECTORY_URL", " https://ca.example/directory ")
			t.Setenv("NRE_ACME_EMAIL", " ops@example.com ")

			issuer, ok := newMasterCFDNSManagedCertificateIssuer().(*masterCFDNSManagedCertificateIssuer)
			if !ok || issuer == nil {
				t.Fatalf("newMasterCFDNSManagedCertificateIssuer() = %T, want concrete issuer", issuer)
			}
			if issuer.cfToken != "dns-token-canary" || issuer.cfZoneToken != "zone-token-canary" {
				t.Fatalf("token scope = (%q, %q)", issuer.cfToken, issuer.cfZoneToken)
			}
			if issuer.dataDir != dataDir || issuer.directoryURL != "https://ca.example/directory" || issuer.email != "ops@example.com" {
				t.Fatalf("issuer config = dataDir %q, directory %q, email %q", issuer.dataDir, issuer.directoryURL, issuer.email)
			}
		})
	}

	for _, key := range append(append([]string(nil), dnsAliases...), zoneAliases...) {
		t.Setenv(key, "")
	}
	if issuer := newMasterCFDNSManagedCertificateIssuer(); issuer != nil {
		t.Fatalf("issuer without DNS token = %T, want nil", issuer)
	}
}

func TestMasterCFDNSConstructorFallsBackToDNSAPITokenForZoneScope(t *testing.T) {
	for _, key := range []string{"CLOUDFLARE_DNS_API_TOKEN", "CF_DNS_API_TOKEN", "CF_TOKEN", "CF_Token", "CLOUDFLARE_ZONE_API_TOKEN", "CF_ZONE_API_TOKEN"} {
		t.Setenv(key, "")
	}
	t.Setenv("CF_TOKEN", "shared-token-canary")
	issuer := newMasterCFDNSManagedCertificateIssuer().(*masterCFDNSManagedCertificateIssuer)
	if issuer.cfToken != "shared-token-canary" || issuer.cfZoneToken != issuer.cfToken {
		t.Fatalf("fallback token scope = (%q, %q)", issuer.cfToken, issuer.cfZoneToken)
	}
}

func TestMasterCFDNSContextErrorsStayTypedWithoutOpeningState(t *testing.T) {
	openCalls := 0
	issuer := &masterCFDNSManagedCertificateIssuer{
		openState: func(string) (masterACMEStateStore, error) {
			openCalls++
			return nil, errors.New("state must not be opened")
		},
	}
	cert := ManagedCertificate{Domain: "example.com"}

	if _, err := issuer.Issue(nil, cert); acmeflow.ErrorCategoryOf(err) != acmeflow.CategoryProtocol {
		t.Fatalf("Issue(nil) category = %q, want %q (err=%v)", acmeflow.ErrorCategoryOf(err), acmeflow.CategoryProtocol, err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := issuer.Renew(cancelled, cert); acmeflow.ErrorCategoryOf(err) != acmeflow.CategoryCancelled {
		t.Fatalf("Renew(cancelled) category = %q, want %q (err=%v)", acmeflow.ErrorCategoryOf(err), acmeflow.CategoryCancelled, err)
	}
	if openCalls != 0 {
		t.Fatalf("state open calls = %d, want 0", openCalls)
	}
}

func TestMasterCFDNSConcurrentIssuersSerializeAccountLifecycle(t *testing.T) {
	now := time.Date(2026, 7, 25, 13, 0, 0, 0, time.UTC)
	dataDir := t.TempDir()
	firstStateOpened := make(chan struct{})
	releaseFirstState := make(chan struct{})
	secondStateOpened := make(chan struct{})
	var openCalls atomic.Int32
	openState := func(dataDir string) (masterACMEStateStore, error) {
		store, err := openMasterACMEAccountStore(dataDir)
		if err != nil {
			return nil, err
		}
		switch openCalls.Add(1) {
		case 1:
			close(firstStateOpened)
			<-releaseFirstState
		case 2:
			close(secondStateOpened)
		}
		return store, nil
	}
	newIssuer := func(engine masterACMEEngine) *masterCFDNSManagedCertificateIssuer {
		return &masterCFDNSManagedCertificateIssuer{
			directoryURL: "https://ca.example/directory",
			email:        "ops@example.com",
			dataDir:      dataDir,
			engine:       engine,
			openState:    openState,
			newSolver: func(masterACMEStateStore) (acmeflow.ChallengeSolver, error) {
				return fakeMasterDNS01Solver{}, nil
			},
			now: func() time.Time { return now },
		}
	}
	firstEngine := &fakeMasterACMEEngine{now: now}
	secondEngine := &fakeMasterACMEEngine{now: now}
	cert := ManagedCertificate{ID: 8, Domain: "concurrent.example.com", IssuerMode: "master_cf_dns"}
	type issueOutcome struct {
		result managedCertificateRenewalResult
		err    error
	}
	firstOutcome := make(chan issueOutcome, 1)
	secondOutcome := make(chan issueOutcome, 1)
	go func() {
		result, err := newIssuer(firstEngine).Issue(context.Background(), cert)
		firstOutcome <- issueOutcome{result: result, err: err}
	}()
	select {
	case <-firstStateOpened:
	case <-time.After(5 * time.Second):
		close(releaseFirstState)
		t.Fatal("first issuer did not reach the account-state barrier")
	}
	go func() {
		result, err := newIssuer(secondEngine).Issue(context.Background(), cert)
		secondOutcome <- issueOutcome{result: result, err: err}
	}()

	overlapped := false
	barrierTimer := time.NewTimer(150 * time.Millisecond)
	select {
	case <-secondStateOpened:
		overlapped = true
		if !barrierTimer.Stop() {
			<-barrierTimer.C
		}
	case <-barrierTimer.C:
	}
	close(releaseFirstState)
	first := <-firstOutcome
	second := <-secondOutcome
	if overlapped {
		t.Fatal("a second issuer opened the same account state while the first account lifecycle was active")
	}
	if first.err != nil || second.err != nil {
		t.Fatalf("concurrent Issue() errors = (%v, %v)", first.err, second.err)
	}
	if !bytes.Equal(firstEngine.accountKey, secondEngine.accountKey) {
		t.Fatal("concurrent issuers did not reuse the same persisted account key")
	}
	if len(firstEngine.accountURIs) != 1 || len(secondEngine.accountURIs) != 1 ||
		firstEngine.accountURIs[0] == "" || firstEngine.accountURIs[0] != secondEngine.accountURIs[0] {
		t.Fatalf("concurrent issuer account URIs = (%v, %v)", firstEngine.accountURIs, secondEngine.accountURIs)
	}
	if first.result.Material.KeyPEM == second.result.Material.KeyPEM {
		t.Fatal("serialized account lifecycle reused a certificate private key")
	}
}

func TestMasterCFDNSIssueRecoversAccountAndRotatesCertificateKeys(t *testing.T) {
	now := time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)
	engine := &fakeMasterACMEEngine{crashFirst: true, now: now}
	dataDir := t.TempDir()
	issuer := &masterCFDNSManagedCertificateIssuer{
		directoryURL: "https://ca.example/directory",
		email:        "ops@example.com",
		cfToken:      "dns-token-canary",
		cfZoneToken:  "zone-token-canary",
		dataDir:      dataDir,
		engine:       engine,
		openState: func(dataDir string) (masterACMEStateStore, error) {
			return openMasterACMEAccountStore(dataDir)
		},
		newSolver: func(masterACMEStateStore) (acmeflow.ChallengeSolver, error) {
			return fakeMasterDNS01Solver{}, nil
		},
		now: func() time.Time { return now },
	}
	cert := ManagedCertificate{ID: 7, Domain: "*.example.com", IssuerMode: "master_cf_dns"}

	_, err := issuer.Issue(context.Background(), cert)
	if err == nil {
		t.Fatal("first Issue() error = nil, want injected registration crash")
	}
	if category := acmeflow.ErrorCategoryOf(err); category != acmeflow.CategoryAccount {
		t.Fatalf("first Issue() category = %q, want %q", category, acmeflow.CategoryAccount)
	}
	if strings.Contains(err.Error(), fakeMasterCrashCanary) {
		t.Fatalf("first Issue() exposed crash/provider detail: %v", err)
	}

	issued, err := issuer.Issue(context.Background(), cert)
	if err != nil {
		t.Fatalf("second Issue() error = %v", err)
	}
	renewed, err := issuer.Renew(context.Background(), cert)
	if err != nil {
		t.Fatalf("Renew() error = %v", err)
	}
	if !issued.Changed || !renewed.Changed || issued.Material.Domain != cert.Domain || renewed.Material.Domain != cert.Domain {
		t.Fatalf("issuer results = issued %#v, renewed %#v", issued, renewed)
	}
	if issued.Material.KeyPEM == renewed.Material.KeyPEM {
		t.Fatal("Issue() and Renew() reused the certificate private key")
	}
	if issued.MaterialHash != hashManagedCertificateMaterial(issued.Material.CertPEM, issued.Material.KeyPEM) ||
		renewed.MaterialHash != hashManagedCertificateMaterial(renewed.Material.CertPEM, renewed.Material.KeyPEM) {
		t.Fatal("issuer result material hash does not match returned material")
	}
	if issued.LastIssueAt != now.Format(time.RFC3339) || issued.ACMEInfo.MainDomain != cert.Domain || issued.ACMEInfo.CA != "Test CA" || issued.ACMEInfo.KeyLength != "ecdsa" {
		t.Fatalf("issued metadata = lastIssue %q, ACME %#v", issued.LastIssueAt, issued.ACMEInfo)
	}
	if len(engine.requests) != 3 {
		t.Fatalf("engine request count = %d, want 3", len(engine.requests))
	}
	for _, request := range engine.requests {
		if request.DirectoryURL != issuer.directoryURL || request.Email != issuer.email || request.ChallengeType != acmeflow.ChallengeDNS01 || len(request.ExistingKeyPEM) != 0 {
			t.Fatalf("engine request did not preserve master policy: %#v", request)
		}
		if len(request.Identifiers) != 1 || request.Identifiers[0] != (acmeflow.Identifier{Type: acmeflow.IdentifierDNS, Value: cert.Domain}) {
			t.Fatalf("engine identifiers = %#v", request.Identifiers)
		}
	}
	if len(engine.successfulCertificateKeys) != 2 || bytes.Equal(engine.successfulCertificateKeys[0], engine.successfulCertificateKeys[1]) {
		t.Fatal("fake engine did not observe certificate-key rotation")
	}
	assertDirectoryDoesNotContain(t, filepath.Join(dataDir, "acme", "master"), issuer.cfToken, issuer.cfZoneToken, fakeMasterCrashCanary)
}

const fakeMasterCrashCanary = "raw-registration-provider-body-canary"

type fakeMasterACMEEngine struct {
	crashFirst                bool
	now                       time.Time
	requests                  []acmeflow.IssueRequest
	accountKey                []byte
	accountURIs               []string
	successfulCertificateKeys [][]byte
}

func (engine *fakeMasterACMEEngine) Issue(ctx context.Context, request acmeflow.IssueRequest) (acmeflow.IssueResult, error) {
	engine.requests = append(engine.requests, request)
	lookup := acmeflow.AccountLookup{DirectoryURL: request.DirectoryURL, Email: request.Email}
	record, err := request.AccountStore.LoadAccount(ctx, lookup)
	if errors.Is(err, acmeflow.ErrAccountNotFound) {
		if len(engine.accountKey) == 0 {
			engine.accountKey, err = generateTestPrivateKeyPEM()
			if err != nil {
				return acmeflow.IssueResult{}, err
			}
		}
		if err := request.AccountStore.SaveAccountKey(ctx, lookup, engine.accountKey); err != nil {
			return acmeflow.IssueResult{}, err
		}
		record.KeyPEM = append([]byte(nil), engine.accountKey...)
	} else if err != nil {
		return acmeflow.IssueResult{}, err
	}
	if len(engine.accountKey) == 0 {
		engine.accountKey = append([]byte(nil), record.KeyPEM...)
	}
	if !bytes.Equal(record.KeyPEM, engine.accountKey) {
		return acmeflow.IssueResult{}, errors.New("account key changed during recovery")
	}
	if engine.crashFirst && len(engine.requests) == 1 {
		return acmeflow.IssueResult{}, acmeflow.WrapError(acmeflow.CategoryAccount, "account_recover", errors.New(fakeMasterCrashCanary))
	}
	metadata := record.Metadata
	if metadata.URI == "" {
		metadata = acmeflow.AccountMetadata{
			Version:      acmeflow.AccountMetadataVersion,
			DirectoryURL: lookup.DirectoryURL,
			Email:        lookup.Email,
			URI:          "https://ca.example/account/7",
			Contact:      []string{"mailto:" + lookup.Email},
		}
		if err := request.AccountStore.SaveAccountMetadata(ctx, metadata); err != nil {
			return acmeflow.IssueResult{}, err
		}
	}
	if len(request.Identifiers) != 1 {
		return acmeflow.IssueResult{}, errors.New("unexpected identifier count")
	}
	engine.accountURIs = append(engine.accountURIs, metadata.URI)
	certificatePEM, privateKeyPEM, err := generateTestMasterCertificate(request.Identifiers[0].Value, engine.now, int64(len(engine.successfulCertificateKeys)+1))
	if err != nil {
		return acmeflow.IssueResult{}, err
	}
	engine.successfulCertificateKeys = append(engine.successfulCertificateKeys, append([]byte(nil), privateKeyPEM...))
	return acmeflow.IssueResult{
		CertificatePEM: certificatePEM,
		PrivateKeyPEM:  privateKeyPEM,
		AccountKeyPEM:  append([]byte(nil), engine.accountKey...),
		Account:        metadata,
	}, nil
}

type fakeMasterDNS01Solver struct{}

func (fakeMasterDNS01Solver) ChallengeType() string { return acmeflow.ChallengeDNS01 }
func (fakeMasterDNS01Solver) Present(context.Context, acmeflow.Challenge) error {
	return nil
}
func (fakeMasterDNS01Solver) Wait(context.Context, acmeflow.Challenge) error { return nil }
func (fakeMasterDNS01Solver) Cleanup(context.Context, acmeflow.Challenge) error {
	return nil
}

func generateTestPrivateKeyPEM() ([]byte, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), nil
}

func generateTestMasterCertificate(domain string, now time.Time, serial int64) ([]byte, []byte, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(serial),
		Subject:      pkix.Name{CommonName: domain},
		DNSNames:     []string{domain},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(90 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	parent := &x509.Certificate{
		SerialNumber:          big.NewInt(serial + 1000),
		Subject:               pkix.Name{CommonName: "Test CA"},
		NotBefore:             template.NotBefore,
		NotAfter:              template.NotAfter,
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, parent, &key.PublicKey, key)
	if err != nil {
		return nil, nil, err
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), nil
}

func assertDirectoryDoesNotContain(t *testing.T, root string, secrets ...string) {
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
		for _, secret := range secrets {
			if secret != "" && bytes.Contains(data, []byte(secret)) {
				t.Errorf("persisted state %s contains secret %q", path, secret)
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("Walk(%s) error = %v", root, err)
	}
}
