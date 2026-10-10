//go:build !integration

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
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/pkg/acmeflow"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/pluginhost"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
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
			if issuer.cfZoneToken != "zone-token-canary" {
				t.Fatalf("zone token = %q, want zone-token-canary", issuer.cfZoneToken)
			}
			if issuer.resolveToken == nil {
				t.Fatal("constructor did not bind ResolveCloudflareDNSToken")
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

func TestMasterCFDNSConstructorLeavesZoneTokenEmptyForIssueTimeFallback(t *testing.T) {
	for _, key := range []string{"CLOUDFLARE_DNS_API_TOKEN", "CF_DNS_API_TOKEN", "CF_TOKEN", "CF_Token", "CLOUDFLARE_ZONE_API_TOKEN", "CF_ZONE_API_TOKEN"} {
		t.Setenv(key, "")
	}
	t.Setenv("CF_TOKEN", "shared-token-canary")
	issuer := newMasterCFDNSManagedCertificateIssuer().(*masterCFDNSManagedCertificateIssuer)
	if issuer.cfZoneToken != "" {
		t.Fatalf("zone token = %q, want empty so Issue uses the resolved DNS token", issuer.cfZoneToken)
	}
}

func TestMasterCFDNSIssueUsesConfiguredResolver(t *testing.T) {
	t.Setenv("CLOUDFLARE_DNS_API_TOKEN", "")
	t.Setenv("CF_DNS_API_TOKEN", "")
	t.Setenv("CF_TOKEN", "token-b")
	now := time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)
	var gotDNS []string
	issuer := &masterCFDNSManagedCertificateIssuer{
		directoryURL: "https://ca.example/directory",
		dataDir:      t.TempDir(),
		engine:       &fakeMasterACMEEngine{now: now},
		openState: func(dataDir string) (masterACMEStateStore, error) {
			return openMasterACMEAccountStore(dataDir)
		},
		newSolver: func(_ masterACMEStateStore, dnsToken, _ string) (acmeflow.ChallengeSolver, error) {
			gotDNS = append(gotDNS, dnsToken)
			return fakeMasterDNS01Solver{}, nil
		},
		resolveToken: func(_ context.Context, domain string) (string, error) {
			if domain == "www.example.com" {
				return "token-a", nil
			}
			return "token-b", nil
		},
		now: func() time.Time { return now },
	}
	if _, err := issuer.Issue(context.Background(), ManagedCertificate{Domain: "www.example.com"}); err != nil {
		t.Fatalf("Issue(mapped) error = %v", err)
	}
	if _, err := issuer.Issue(context.Background(), ManagedCertificate{Domain: "other.test"}); err != nil {
		t.Fatalf("Issue(fallback) error = %v", err)
	}
	if len(gotDNS) != 2 || gotDNS[0] != "token-a" || gotDNS[1] != "token-b" {
		t.Fatalf("resolved tokens = %v, want [token-a token-b]", gotDNS)
	}
}

func TestMasterCFDNSIssueUsesMappedTokenNotEnvironment(t *testing.T) {
	t.Setenv("CF_TOKEN", "token-b")
	t.Setenv("CLOUDFLARE_ZONE_API_TOKEN", "zone-env")
	var gotDNS, gotZone string
	now := time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)
	issuer := &masterCFDNSManagedCertificateIssuer{
		directoryURL: "https://ca.example/directory",
		email:        "ops@example.com",
		cfZoneToken:  "zone-env",
		dataDir:      t.TempDir(),
		engine:       &fakeMasterACMEEngine{now: now},
		openState: func(dataDir string) (masterACMEStateStore, error) {
			return openMasterACMEAccountStore(dataDir)
		},
		newSolver: func(_ masterACMEStateStore, dnsToken, zoneToken string) (acmeflow.ChallengeSolver, error) {
			gotDNS, gotZone = dnsToken, zoneToken
			return fakeMasterDNS01Solver{}, nil
		},
		resolveToken: func(_ context.Context, domain string) (string, error) {
			if domain == "www.example.com" {
				return "token-a", nil
			}
			return firstNonEmptyEnv("CLOUDFLARE_DNS_API_TOKEN", "CF_DNS_API_TOKEN", "CF_TOKEN", "CF_Token"), nil
		},
		now: func() time.Time { return now },
	}
	if _, err := issuer.Issue(context.Background(), ManagedCertificate{Domain: "www.example.com", IssuerMode: "master_cf_dns"}); err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	if gotDNS != "token-a" || gotZone != "zone-env" {
		t.Fatalf("solver tokens = (%q, %q), want mapped DNS token-a and env zone", gotDNS, gotZone)
	}
}

func TestMasterCFDNSIssueFallsBackZoneTokenToResolvedDNSToken(t *testing.T) {
	var gotDNS, gotZone string
	now := time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)
	issuer := &masterCFDNSManagedCertificateIssuer{
		directoryURL: "https://ca.example/directory",
		email:        "ops@example.com",
		dataDir:      t.TempDir(),
		engine:       &fakeMasterACMEEngine{now: now},
		openState: func(dataDir string) (masterACMEStateStore, error) {
			return openMasterACMEAccountStore(dataDir)
		},
		newSolver: func(_ masterACMEStateStore, dnsToken, zoneToken string) (acmeflow.ChallengeSolver, error) {
			gotDNS, gotZone = dnsToken, zoneToken
			return fakeMasterDNS01Solver{}, nil
		},
		resolveToken: func(context.Context, string) (string, error) { return "resolved-dns", nil },
		now:          func() time.Time { return now },
	}
	if _, err := issuer.Issue(context.Background(), ManagedCertificate{Domain: "other.test"}); err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	if gotDNS != "resolved-dns" || gotZone != "resolved-dns" {
		t.Fatalf("solver tokens = (%q, %q), want resolved DNS for both", gotDNS, gotZone)
	}
}

func TestMasterCFDNSIssueFailsWhenDomainHasNoToken(t *testing.T) {
	errTokenUnavailable := errors.New("DNS API token unavailable")
	issuer := &masterCFDNSManagedCertificateIssuer{
		openState: func(string) (masterACMEStateStore, error) {
			t.Fatal("state must not open when token resolve fails")
			return nil, errors.New("unused")
		},
		resolveToken: func(context.Context, string) (string, error) {
			return "", fmt.Errorf("%w: missing.example", errTokenUnavailable)
		},
	}
	_, err := issuer.Issue(context.Background(), ManagedCertificate{Domain: "missing.example"})
	if err == nil || !errors.Is(err, errTokenUnavailable) {
		t.Fatalf("Issue() err = %v, want token unavailable", err)
	}
	if !strings.Contains(err.Error(), "missing.example") {
		t.Fatalf("Issue() err = %v, want domain in message", err)
	}
}

func TestMasterCFDNSIssueFailsWhenMappedTokenUnavailable(t *testing.T) {
	t.Setenv("CF_TOKEN", "token-b")
	errCredentialUnavailable := errors.New("mapped credential unavailable")
	issuer := &masterCFDNSManagedCertificateIssuer{
		openState: func(string) (masterACMEStateStore, error) {
			t.Fatal("state must not open when mapped token is unavailable")
			return nil, errors.New("unused")
		},
		resolveToken: func(context.Context, string) (string, error) {
			return "", fmt.Errorf("%w: example.com", errCredentialUnavailable)
		},
	}
	_, err := issuer.Issue(context.Background(), ManagedCertificate{Domain: "example.com"})
	if err == nil || !errors.Is(err, errCredentialUnavailable) {
		t.Fatalf("Issue() err = %v, want mapped token unavailable", err)
	}
	if !strings.Contains(err.Error(), "example.com") {
		t.Fatalf("Issue() err = %v, want domain in message", err)
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
	t.Setenv("CF_TOKEN", "concurrent-token")
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
			newSolver: func(masterACMEStateStore, string, string) (acmeflow.ChallengeSolver, error) {
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
	t.Setenv("CF_TOKEN", "dns-token-canary")
	t.Setenv("CLOUDFLARE_ZONE_API_TOKEN", "zone-token-canary")
	issuer := &masterCFDNSManagedCertificateIssuer{
		directoryURL: "https://ca.example/directory",
		email:        "ops@example.com",
		cfZoneToken:  "zone-token-canary",
		dataDir:      dataDir,
		engine:       engine,
		openState: func(dataDir string) (masterACMEStateStore, error) {
			return openMasterACMEAccountStore(dataDir)
		},
		newSolver: func(masterACMEStateStore, string, string) (acmeflow.ChallengeSolver, error) {
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
	assertDirectoryDoesNotContain(t, filepath.Join(dataDir, "acme", "master"), "dns-token-canary", "zone-token-canary", fakeMasterCrashCanary)
}

func TestMasterCFDNSIssueReconcilesCompletedChallengeIntents(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 26, 10, 0, 0, 0, time.UTC)
	dataDir := t.TempDir()
	state, err := openMasterACMEAccountStore(dataDir)
	if err != nil {
		t.Fatalf("openMasterACMEAccountStore(seed) error = %v", err)
	}
	intent, err := acmeflow.NewChallengeIntent("example.com", "_acme-challenge.example.com", "completed-token")
	if err != nil {
		t.Fatalf("NewChallengeIntent() error = %v", err)
	}
	if err := state.SaveChallengeIntent(context.Background(), intent); err != nil {
		t.Fatalf("SaveChallengeIntent() error = %v", err)
	}
	if err := state.CompleteChallengeIntent(context.Background(), intent.ID); err != nil {
		t.Fatalf("CompleteChallengeIntent() error = %v", err)
	}
	if err := state.Close(); err != nil {
		t.Fatalf("Close(seed state) error = %v", err)
	}

	issuer := &masterCFDNSManagedCertificateIssuer{
		directoryURL: "https://ca.example/directory",
		email:        "ops@example.com",
		dataDir:      dataDir,
		engine:       &fakeMasterACMEEngine{now: now},
		openState: func(dataDir string) (masterACMEStateStore, error) {
			return openMasterACMEAccountStore(dataDir)
		},
		newSolver: func(masterACMEStateStore, string, string) (acmeflow.ChallengeSolver, error) {
			return fakeMasterDNS01Solver{}, nil
		},
		resolveToken: func(context.Context, string) (string, error) { return "intent-token", nil },
		now:          func() time.Time { return now },
	}
	if _, err := issuer.Issue(context.Background(), ManagedCertificate{ID: 82, Domain: "example.com", IssuerMode: "master_cf_dns"}); err != nil {
		t.Fatalf("Issue() error = %v", err)
	}

	reopened, err := openMasterACMEAccountStore(dataDir)
	if err != nil {
		t.Fatalf("openMasterACMEAccountStore(verify) error = %v", err)
	}
	defer reopened.Close()
	intents, err := reopened.ListChallengeIntents(context.Background())
	if err != nil {
		t.Fatalf("ListChallengeIntents() error = %v", err)
	}
	if len(intents) != 0 {
		t.Fatalf("completed challenge intents were retained: %+v", intents)
	}
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

type invokingMasterACMEEngine struct {
	inner      *fakeMasterACMEEngine
	cleanupErr error
	presentErr error
}

func (engine *invokingMasterACMEEngine) Issue(ctx context.Context, request acmeflow.IssueRequest) (acmeflow.IssueResult, error) {
	challenge := acmeflow.Challenge{
		Type:       acmeflow.ChallengeDNS01,
		Identifier: request.Identifiers[0],
		DNSValue:   "challenge-digest",
	}
	if err := request.Solver.Present(ctx, challenge); err != nil {
		return acmeflow.IssueResult{}, err
	}
	if err := request.Solver.Wait(ctx, challenge); err != nil {
		return acmeflow.IssueResult{}, err
	}
	if err := request.Solver.Cleanup(ctx, challenge); err != nil || engine.cleanupErr != nil {
		if err == nil {
			err = engine.cleanupErr
		}
		return acmeflow.IssueResult{}, err
	}
	return engine.inner.Issue(ctx, request)
}

type scriptedDNSRecords struct {
	byDomain  map[string]DNSCredential
	err       error
	domainErr map[string]error
	ensureErr error
	deleteErr error
	ensured   []scriptedDNSCall
	deleted   []scriptedDNSCall
}

type scriptedDNSCall struct {
	domain, recordType, name, content string
	ttl                               int
}

func (s *scriptedDNSRecords) ResolveCredential(_ context.Context, domain string) (DNSCredential, error) {
	if s.err != nil {
		return DNSCredential{}, s.err
	}
	if err := s.domainErr[domain]; err != nil {
		return DNSCredential{}, err
	}
	if credential, ok := s.byDomain[domain]; ok {
		return credential, nil
	}
	return DNSCredential{}, fmt.Errorf("%w: %s", errDNSCredentialUnavailable, domain)
}

func (s *scriptedDNSRecords) EnsureRecord(_ context.Context, domain, recordType, name, content string, ttl int) error {
	s.ensured = append(s.ensured, scriptedDNSCall{domain, recordType, name, content, ttl})
	return s.ensureErr
}

func (s *scriptedDNSRecords) DeleteRecord(_ context.Context, domain, recordType, name, content string) error {
	s.deleted = append(s.deleted, scriptedDNSCall{domain: domain, recordType: recordType, name: name, content: content})
	return s.deleteErr
}

func TestDomesticMappingIssuesThroughPluginWithoutCloudflareSolver(t *testing.T) {
	records := &scriptedDNSRecords{byDomain: map[string]DNSCredential{
		"*.example.cn":  {Provider: pluginhost.DNSProviderAliyun, Mapped: true},
		"ok.example.cn": {Provider: pluginhost.DNSProviderTencentDNS, Mapped: true},
	}}
	propagation := &fakePluginDNSPropagation{}
	now := time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)
	issuer := &masterCFDNSManagedCertificateIssuer{
		directoryURL: "https://ca.example/directory",
		email:        "ops@example.com",
		dataDir:      t.TempDir(),
		engine:       &invokingMasterACMEEngine{inner: &fakeMasterACMEEngine{now: now}},
		openState: func(dataDir string) (masterACMEStateStore, error) {
			return openMasterACMEAccountStore(dataDir)
		},
		newSolver: func(masterACMEStateStore, string, string) (acmeflow.ChallengeSolver, error) {
			t.Fatal("domestic mapping used the Cloudflare solver")
			return nil, errors.New("cloudflare solver")
		},
		records:     records,
		propagation: propagation,
		now:         func() time.Time { return now },
	}
	issued, err := issuer.Issue(context.Background(), ManagedCertificate{Domain: "*.example.cn", IssuerMode: "master_cf_dns"})
	if err != nil {
		t.Fatal(err)
	}
	if !issued.Changed || len(records.ensured) != 1 || len(records.deleted) != 1 {
		t.Fatalf("issued=%v ensured=%+v deleted=%+v", issued.Changed, records.ensured, records.deleted)
	}
	if records.ensured[0].domain != "example.cn" || records.ensured[0].recordType != "TXT" || records.ensured[0].name != "_acme-challenge.example.cn" || records.ensured[0].content != "challenge-digest" || records.ensured[0].ttl != 120 {
		t.Fatalf("ensure = %+v", records.ensured[0])
	}
	if records.deleted[0].name != "_acme-challenge.example.cn" || records.deleted[0].content != "challenge-digest" {
		t.Fatalf("delete = %+v", records.deleted[0])
	}
	if len(propagation.waited) != 1 || !strings.Contains(propagation.waited[0], "_acme-challenge.example.cn") {
		t.Fatalf("wait = %+v", propagation.waited)
	}

	records.ensureErr = errors.New("permission denied token=super-secret-dns-token")
	if _, err := issuer.Issue(context.Background(), ManagedCertificate{Domain: "ok.example.cn"}); err == nil || !strings.Contains(err.Error(), "permission denied") || strings.Contains(err.Error(), "super-secret-dns-token") {
		t.Fatalf("provider failure = %v", err)
	}
	records.ensureErr = nil
	records.deleteErr = errors.New("cleanup refused")
	if _, err := issuer.Issue(context.Background(), ManagedCertificate{Domain: "ok.example.cn"}); err == nil || !strings.Contains(err.Error(), "cleanup refused") {
		t.Fatalf("cleanup failure = %v", err)
	}
}

func TestDomesticSubmissionDoesNotRequireCloudflareEnv(t *testing.T) {
	records := &scriptedDNSRecords{byDomain: map[string]DNSCredential{
		"www.example.cn":  {Provider: pluginhost.DNSProviderDNSPodCom, Mapped: true},
		"www.example.com": {Provider: pluginhost.DNSProviderCloudflare, Token: "env-token"},
	}}
	service := &certificateService{renewalIssuer: &masterCFDNSManagedCertificateIssuer{records: records}}
	if err := service.assertManagedDNSSubmissionAllowed(context.Background(), "www.example.cn"); err != nil {
		t.Fatal(err)
	}
	err := service.assertManagedDNSSubmissionAllowed(context.Background(), "www.example.com")
	if err == nil || !strings.Contains(err.Error(), "ACME_DNS_PROVIDER=cf") || strings.Contains(err.Error(), "env-token") {
		t.Fatalf("cloudflare submit without switch err = %v", err)
	}
	typed := service.renewalIssuer.(*masterCFDNSManagedCertificateIssuer)
	typed.cloudflareEnabled = true
	if err := service.assertManagedDNSSubmissionAllowed(context.Background(), "www.example.com"); err != nil {
		t.Fatal(err)
	}
	records.byDomain = nil
	err = service.assertManagedDNSSubmissionAllowed(context.Background(), "missing.example")
	if err == nil || !strings.Contains(err.Error(), "ACME_DNS_PROVIDER=cf") {
		t.Fatalf("unmapped submit err = %v", err)
	}
}

func TestCloudflareMappingStillUsesExistingSolver(t *testing.T) {
	var gotToken string
	records := &scriptedDNSRecords{byDomain: map[string]DNSCredential{
		"www.example.com": {Provider: pluginhost.DNSProviderCloudflare, Token: "mapped-token", Mapped: true},
	}}
	now := time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)
	issuer := &masterCFDNSManagedCertificateIssuer{
		directoryURL: "https://ca.example/directory",
		dataDir:      t.TempDir(),
		engine:       &fakeMasterACMEEngine{now: now},
		openState: func(dataDir string) (masterACMEStateStore, error) {
			return openMasterACMEAccountStore(dataDir)
		},
		newSolver: func(_ masterACMEStateStore, dnsToken, _ string) (acmeflow.ChallengeSolver, error) {
			gotToken = dnsToken
			return fakeMasterDNS01Solver{}, nil
		},
		records:           records,
		cloudflareEnabled: true,
		now:               func() time.Time { return now },
	}
	if _, err := issuer.Issue(context.Background(), ManagedCertificate{Domain: "www.example.com"}); err != nil {
		t.Fatal(err)
	}
	if gotToken != "mapped-token" || len(records.ensured) != 0 {
		t.Fatalf("token=%q ensured=%d", gotToken, len(records.ensured))
	}
}

func TestCloudflareCredentialDoesNotIssueWithoutProviderSwitch(t *testing.T) {
	const secret = "super-secret-dns-token"
	records := &scriptedDNSRecords{byDomain: map[string]DNSCredential{
		"www.example.com": {Provider: pluginhost.DNSProviderCloudflare, Token: secret, Mapped: true},
	}}
	issuer := &masterCFDNSManagedCertificateIssuer{
		records: records,
		newSolver: func(masterACMEStateStore, string, string) (acmeflow.ChallengeSolver, error) {
			t.Fatal("cloudflare solver used without ACME_DNS_PROVIDER=cf")
			return nil, errors.New("cloudflare solver")
		},
	}
	_, err := issuer.Issue(context.Background(), ManagedCertificate{Domain: "www.example.com", IssuerMode: "master_cf_dns"})
	if err == nil || !strings.Contains(err.Error(), "ACME_DNS_PROVIDER=cf") || strings.Contains(err.Error(), secret) {
		t.Fatalf("issue err = %v", err)
	}
	records.byDomain = nil
	records.err = fmt.Errorf("vault unavailable token=%s", secret)
	_, err = issuer.Issue(context.Background(), ManagedCertificate{Domain: "www.example.cn", IssuerMode: "master_cf_dns"})
	if err == nil || strings.Contains(err.Error(), secret) || !strings.Contains(err.Error(), "unavailable") {
		t.Fatalf("resolution err = %v", err)
	}
}

func TestManagedDNSResolutionFailureIsPersisted(t *testing.T) {
	if testing.Short() {
		t.Skip("SQLite-backed managed DNS scenarios run in the full test tier")
	}
	const secret = "super-secret-dns-token"
	now := time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)
	records := &scriptedDNSRecords{err: fmt.Errorf("vault unavailable token=%s", secret)}
	issuer := &masterCFDNSManagedCertificateIssuer{records: records}
	store := openManagedDNSTestStore(t)
	issuing := ManagedCertificate{
		ID: 11, Domain: "www.example.cn", Enabled: true, Scope: "domain", IssuerMode: "master_cf_dns",
		CertificateType: "acme", Status: "issuing", Revision: 1,
	}
	if err := store.SaveManagedCertificates(context.Background(), []storage.ManagedCertificateRow{managedCertificateToRow(issuing)}); err != nil {
		t.Fatal(err)
	}
	service := newManagedDNSTestService(store, issuer, now)
	_, err := service.issueManagedCertificateInBackground(context.Background(), nil, 0, issuing, 1)
	if err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("issue err = %v", err)
	}
	failed := mustManagedCertificate(t, store, issuing.ID)
	if failed.Status != "error" || failed.LastError == "" || strings.Contains(failed.LastError, secret) || !strings.Contains(failed.LastError, "unavailable") {
		t.Fatalf("issuing certificate = %+v", failed)
	}

	renewStore := openManagedDNSTestStore(t)
	solverCalls := 0
	renewRecords := &scriptedDNSRecords{
		byDomain: map[string]DNSCredential{
			"www.example.com": {Provider: pluginhost.DNSProviderCloudflare, Token: secret, Mapped: true},
		},
		domainErr: map[string]error{
			"broken.example.cn": fmt.Errorf("%w token=%s", pluginhost.ErrDNSProviderUnavailable, secret),
		},
	}
	renewIssuer := &masterCFDNSManagedCertificateIssuer{
		records: renewRecords,
		newSolver: func(masterACMEStateStore, string, string) (acmeflow.ChallengeSolver, error) {
			solverCalls++
			return nil, errors.New("cloudflare solver")
		},
	}
	renewAt := now.Add(-time.Hour).Format(time.RFC3339)
	if err := renewStore.SaveManagedCertificates(context.Background(), []storage.ManagedCertificateRow{
		managedCertificateToRow(ManagedCertificate{
			ID: 1, Domain: "broken.example.cn", Enabled: true, Scope: "domain", IssuerMode: "master_cf_dns",
			CertificateType: "acme", Status: "active", Revision: 1,
			ACMEInfo: ManagedCertificateACMEInfo{Renew: renewAt},
		}),
		managedCertificateToRow(ManagedCertificate{
			ID: 2, Domain: "www.example.com", Enabled: true, Scope: "domain", IssuerMode: "master_cf_dns",
			CertificateType: "acme", Status: "active", Revision: 2,
			ACMEInfo: ManagedCertificateACMEInfo{Renew: renewAt},
		}),
	}); err != nil {
		t.Fatal(err)
	}
	renewService := newManagedDNSTestService(renewStore, renewIssuer, now)
	err = renewService.RunRenewalPass(context.Background())
	if err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("renewal err = %v", err)
	}
	broken := mustManagedCertificate(t, renewStore, 1)
	if broken.Status != "error" || broken.LastError == "" || strings.Contains(broken.LastError, secret) || !strings.Contains(broken.LastError, "unavailable") {
		t.Fatalf("renewed certificate = %+v", broken)
	}
	cloudflare := mustManagedCertificate(t, renewStore, 2)
	if cloudflare.Status != "active" || cloudflare.LastError != "" || solverCalls != 0 || len(renewRecords.ensured) != 0 {
		t.Fatalf("cloudflare certificate = %+v solver=%d ensured=%d", cloudflare, solverCalls, len(renewRecords.ensured))
	}

	issueStore := openManagedDNSTestStore(t)
	cloudflareIssuing := ManagedCertificate{
		ID: 3, Domain: "www.example.com", Enabled: true, Scope: "domain", IssuerMode: "master_cf_dns",
		CertificateType: "acme", Status: "issuing", Revision: 1,
	}
	if err := issueStore.SaveManagedCertificates(context.Background(), []storage.ManagedCertificateRow{managedCertificateToRow(cloudflareIssuing)}); err != nil {
		t.Fatal(err)
	}
	_, err = newManagedDNSTestService(issueStore, renewIssuer, now).issueManagedCertificateInBackground(context.Background(), nil, 0, cloudflareIssuing, 1)
	if err == nil || strings.Contains(err.Error(), secret) || !strings.Contains(err.Error(), "ACME_DNS_PROVIDER=cf") {
		t.Fatalf("issuing cloudflare err = %v", err)
	}
	stuck := mustManagedCertificate(t, issueStore, cloudflareIssuing.ID)
	if stuck.Status != "error" || stuck.LastError == "" || strings.Contains(stuck.LastError, secret) {
		t.Fatalf("issuing cloudflare certificate = %+v", stuck)
	}
}

func openManagedDNSTestStore(t *testing.T) *storage.GormStore {
	t.Helper()
	store, err := newServiceSQLiteStore(t, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func newManagedDNSTestService(store storage.Store, issuer managedCertificateRenewalIssuer, now time.Time) *certificateService {
	service := newCertificateServiceWithRenewal(config.Config{}, store, issuer)
	service.revisionMutation = true
	service.now = func() time.Time { return now }
	return service
}

func mustManagedCertificate(t *testing.T, store storage.Store, id int) ManagedCertificate {
	t.Helper()
	rows, err := store.ListManagedCertificates(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	cert, _, found := findManagedCertificateByID(rows, id)
	if !found {
		t.Fatalf("certificate %d not found", id)
	}
	return cert
}

type fakePluginDNSPropagation struct {
	waited []string
}

func (f *fakePluginDNSPropagation) ResolveCNAME(context.Context, string) (string, error) {
	return "", nil
}

func (f *fakePluginDNSPropagation) WaitTXT(_ context.Context, name, value, zone string) error {
	f.waited = append(f.waited, name+" "+value+" "+zone)
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
