package certs

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"

	"crypto/x509"
	"crypto/x509/pkix"

	"encoding/json"
	"encoding/pem"

	"math/big"
	"net"
	"os"
	"path/filepath"
	"reflect"

	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/pkg/acmeflow"
	"golang.org/x/crypto/acme"
)

func TestManagerApplyLoadsControlPlaneMaterial(t *testing.T) {
	t.Parallel()

	leaf := mustCreateTLSMaterial(t, certificateSpec{commonName: "control-plane.example.com"})
	manager := mustNewManager(t, t.TempDir())

	err := manager.Apply(context.Background(), []model.ManagedCertificateBundle{
		{
			ID:       11,
			Domain:   "control-plane.example.com",
			Revision: 3,
			CertPEM:  string(leaf.CertPEM),
			KeyPEM:   string(leaf.KeyPEM),
		},
	}, []model.ManagedCertificatePolicy{
		{
			ID:              11,
			Domain:          "control-plane.example.com",
			Enabled:         true,
			Usage:           "https",
			CertificateType: "uploaded",
			SelfSigned:      false,
			Revision:        3,
		},
	})
	if err != nil {
		t.Fatalf("apply failed: %v", err)
	}

	cert, err := manager.ServerCertificate(context.Background(), 11)
	if err != nil {
		t.Fatalf("server certificate failed: %v", err)
	}
	if cert == nil {
		t.Fatal("expected server certificate")
	}

	info, err := manager.CertificateInfo(11)
	if err != nil {
		t.Fatalf("certificate info failed: %v", err)
	}
	if info.Usage != "https" {
		t.Fatalf("unexpected usage: %q", info.Usage)
	}
	if info.CertificateType != "uploaded" {
		t.Fatalf("unexpected certificate type: %q", info.CertificateType)
	}
	if info.SelfSigned {
		t.Fatal("expected self_signed=false")
	}
	if info.Fingerprint != leaf.Fingerprint {
		t.Fatalf("unexpected fingerprint: got %q want %q", info.Fingerprint, leaf.Fingerprint)
	}
}

func TestManagerApplyRejectsInvalidMaterialWithoutDroppingPreviousState(t *testing.T) {
	t.Parallel()

	manager := mustNewManager(t, t.TempDir())
	previous := mustCreateTLSMaterial(t, certificateSpec{commonName: "stable.example.com"})

	if err := manager.Apply(context.Background(), []model.ManagedCertificateBundle{
		{
			ID:      12,
			Domain:  "stable.example.com",
			CertPEM: string(previous.CertPEM),
			KeyPEM:  string(previous.KeyPEM),
		},
	}, []model.ManagedCertificatePolicy{
		{
			ID:              12,
			Domain:          "stable.example.com",
			Enabled:         true,
			Usage:           "https",
			CertificateType: "uploaded",
		},
	}); err != nil {
		t.Fatalf("initial apply failed: %v", err)
	}

	invalid := mustCreateTLSMaterial(t, certificateSpec{commonName: "broken.example.com"})
	err := manager.Apply(context.Background(), []model.ManagedCertificateBundle{
		{
			ID:      12,
			Domain:  "stable.example.com",
			CertPEM: "not-a-certificate",
			KeyPEM:  string(invalid.KeyPEM),
		},
	}, []model.ManagedCertificatePolicy{
		{
			ID:              12,
			Domain:          "stable.example.com",
			Enabled:         true,
			Usage:           "https",
			CertificateType: "uploaded",
		},
	})
	if err == nil {
		t.Fatal("expected apply to fail")
	}

	info, err := manager.CertificateInfo(12)
	if err != nil {
		t.Fatalf("certificate info failed after rejected apply: %v", err)
	}
	if info.Fingerprint != previous.Fingerprint {
		t.Fatalf("previous state was not preserved: got %q want %q", info.Fingerprint, previous.Fingerprint)
	}
}

func TestIntegrationManagerApplyACMELifecyclePersistsReissuesRecoversAndRenews(t *testing.T) {
	requireCertificateLifecycle(t)
	t.Parallel()

	now := time.Date(2026, 4, 6, 12, 0, 0, 0, time.UTC)
	dataDir := t.TempDir()
	initial := mustCreateTLSMaterial(t, certificateSpec{
		commonName: "persist-acme.example.com",
		notBefore:  now.Add(-time.Hour),
		notAfter:   now.Add(90 * 24 * time.Hour),
	})
	domainChanged := mustCreateTLSMaterial(t, certificateSpec{
		commonName: "persist-acme-next.example.com",
		notBefore:  now.Add(-time.Hour),
		notAfter:   now.Add(90 * 24 * time.Hour),
	})
	corruptionRecovery := mustCreateTLSMaterial(t, certificateSpec{
		commonName: "persist-acme-next.example.com",
		notBefore:  now.Add(-time.Hour),
		notAfter:   now.Add(2 * time.Hour),
	})
	renewed := mustCreateTLSMaterial(t, certificateSpec{
		commonName: "persist-acme-next.example.com",
		notBefore:  now.Add(-time.Hour),
		notAfter:   now.Add(90 * 24 * time.Hour),
	})
	fake := &fakeACMEIssuer{results: []acmeIssueResult{
		{CertPEM: initial.CertPEM, KeyPEM: initial.KeyPEM},
		{CertPEM: domainChanged.CertPEM, KeyPEM: domainChanged.KeyPEM},
		{CertPEM: corruptionRecovery.CertPEM, KeyPEM: corruptionRecovery.KeyPEM},
		{CertPEM: renewed.CertPEM, KeyPEM: renewed.KeyPEM},
	}}
	manager := mustNewManager(
		t,
		dataDir,
		withNow(func() time.Time { return now }),
		withRenewBefore(24*time.Hour),
		withACMEIssuerFactory(func(request acmeIssueRequest) (acmeIssuer, error) {
			return fake, nil
		}),
	)
	policy := model.ManagedCertificatePolicy{
		ID:              55,
		Domain:          "persist-acme.example.com",
		Enabled:         true,
		Scope:           "domain",
		IssuerMode:      "local_http01",
		CertificateType: "acme",
		Usage:           "https",
	}

	if err := manager.Apply(context.Background(), nil, []model.ManagedCertificatePolicy{policy}); err != nil {
		t.Fatalf("initial apply failed: %v", err)
	}
	if len(fake.requests) != 1 {
		t.Fatalf("expected one initial issuance, got %d", len(fake.requests))
	}
	materialDir := filepath.Join(dataDir, "certs", "managed", "55")
	managedState, ok, err := manager.loadManagedCertificateState(policy.ID)
	if err != nil || !ok || managedState.ACME == nil || managedState.ACME.Account.Metadata == nil {
		t.Fatalf("initial managed ACME state = %#v, %v", managedState, err)
	}
	managedAccountKey := append([]byte(nil), managedState.ACME.Account.KeyPEM...)
	managedAccountURI := managedState.ACME.Account.Metadata.URI
	for _, legacyName := range []string{"acme_account_key.pem", "acme_account.json", "acme_registration.json", "local_metadata.json"} {
		if err := os.Remove(filepath.Join(materialDir, legacyName)); err != nil && !os.IsNotExist(err) {
			t.Fatalf("remove legacy projection %s: %v", legacyName, err)
		}
	}

	before, err := manager.CertificateInfo(55)
	if err != nil {
		t.Fatalf("initial certificate info failed: %v", err)
	}

	if err := manager.Close(); err != nil {
		t.Fatalf("initial manager close failed: %v", err)
	}
	recreated := mustNewManager(
		t,
		dataDir,
		withNow(func() time.Time { return now }),
		withRenewBefore(24*time.Hour),
		withACMEIssuerFactory(func(request acmeIssueRequest) (acmeIssuer, error) {
			return fake, nil
		}),
	)
	if err := recreated.Apply(context.Background(), nil, []model.ManagedCertificatePolicy{policy}); err != nil {
		t.Fatalf("recreated apply failed: %v", err)
	}
	if len(fake.requests) != 1 {
		t.Fatalf("expected persisted material to avoid reissuance, got %d issuer calls", len(fake.requests))
	}

	after, err := recreated.CertificateInfo(55)
	if err != nil {
		t.Fatalf("recreated certificate info failed: %v", err)
	}
	if after.Fingerprint != before.Fingerprint {
		t.Fatalf("expected persisted fingerprint, got %q want %q", after.Fingerprint, before.Fingerprint)
	}

	changedPolicy := policy
	changedPolicy.Domain = "persist-acme-next.example.com"
	if err := recreated.Apply(context.Background(), nil, []model.ManagedCertificatePolicy{changedPolicy}); err != nil {
		t.Fatalf("domain-change apply failed: %v", err)
	}
	if len(fake.requests) != 2 {
		t.Fatalf("expected domain change to trigger a second issuance, got %d", len(fake.requests))
	}
	if !bytes.Equal(fake.requests[1].AccountKeyPEM, managedAccountKey) || fake.requests[1].Account.URI != managedAccountURI {
		t.Fatalf("domain change did not reuse managed-only ACME account: key_match=%t uri=%q want %q", bytes.Equal(fake.requests[1].AccountKeyPEM, managedAccountKey), fake.requests[1].Account.URI, managedAccountURI)
	}
	changedInfo, err := recreated.CertificateInfo(55)
	if err != nil {
		t.Fatalf("domain-change certificate info failed: %v", err)
	}
	if changedInfo.Fingerprint != domainChanged.Fingerprint {
		t.Fatalf("unexpected domain-change fingerprint: got %q want %q", changedInfo.Fingerprint, domainChanged.Fingerprint)
	}

	if err := os.WriteFile(filepath.Join(materialDir, managedCertificateStateFileName), []byte("{not-json"), 0600); err != nil {
		t.Fatalf("corrupt managed state write failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(materialDir, "local_metadata.json"), []byte("{not-json"), 0600); err != nil {
		t.Fatalf("corrupt legacy metadata write failed: %v", err)
	}
	if err := recreated.Apply(context.Background(), nil, []model.ManagedCertificatePolicy{changedPolicy}); err != nil {
		t.Fatalf("corruption-recovery apply failed: %v", err)
	}
	if len(fake.requests) != 3 {
		t.Fatalf("expected corrupt metadata to trigger a third issuance, got %d", len(fake.requests))
	}
	recoveredInfo, err := recreated.CertificateInfo(55)
	if err != nil {
		t.Fatalf("recovered certificate info failed: %v", err)
	}
	if recoveredInfo.Fingerprint != corruptionRecovery.Fingerprint {
		t.Fatalf("unexpected recovery fingerprint: got %q want %q", recoveredInfo.Fingerprint, corruptionRecovery.Fingerprint)
	}

	if err := recreated.Apply(context.Background(), nil, []model.ManagedCertificatePolicy{changedPolicy}); err != nil {
		t.Fatalf("renewal apply failed: %v", err)
	}
	if len(fake.requests) != 4 {
		t.Fatalf("expected expiring material to trigger a fourth issuance, got %d", len(fake.requests))
	}
	renewedInfo, err := recreated.CertificateInfo(55)
	if err != nil {
		t.Fatalf("renewed certificate info failed: %v", err)
	}
	if renewedInfo.Fingerprint != renewed.Fingerprint {
		t.Fatalf("unexpected renewed fingerprint: got %q want %q", renewedInfo.Fingerprint, renewed.Fingerprint)
	}

	if err := recreated.saveManagedCertificateState(policy.ID, managedCertificateState{
		LocalMetadata: localMaterialMetadata{Domain: changedPolicy.Domain},
	}); err != nil {
		t.Fatalf("write partial managed metadata failed: %v", err)
	}
	if err := recreated.Close(); err != nil {
		t.Fatalf("recreated manager close failed: %v", err)
	}
	fallbackIssuer := &fakeACMEIssuer{}
	restarted := mustNewManager(
		t,
		dataDir,
		withNow(func() time.Time { return now }),
		withRenewBefore(24*time.Hour),
		withACMEIssuerFactory(func(request acmeIssueRequest) (acmeIssuer, error) {
			return fallbackIssuer, nil
		}),
	)
	t.Cleanup(func() { _ = restarted.Close() })
	if err := restarted.Apply(context.Background(), nil, []model.ManagedCertificatePolicy{changedPolicy}); err != nil {
		t.Fatalf("partial managed metadata fallback apply failed: %v", err)
	}
	if len(fallbackIssuer.requests) != 0 {
		t.Fatalf("partial managed metadata fallback unexpectedly issued %d certificate(s)", len(fallbackIssuer.requests))
	}
	fallbackInfo, err := restarted.CertificateInfo(policy.ID)
	if err != nil {
		t.Fatalf("partial managed metadata fallback certificate info failed: %v", err)
	}
	if fallbackInfo.Fingerprint != renewed.Fingerprint {
		t.Fatalf("partial managed metadata fallback fingerprint = %q, want %q", fallbackInfo.Fingerprint, renewed.Fingerprint)
	}
}

func TestManagedCertificateStateRoundTrip(t *testing.T) {
	t.Parallel()

	manager := mustNewManager(t, t.TempDir())
	certificateID := 9051
	expected := managedCertificateState{
		LocalMetadata: localMaterialMetadata{
			Domain:          "state-roundtrip.example.com",
			Scope:           "domain",
			IssuerMode:      "local_http01",
			CertificateType: "acme",
		},
		ACME: &model.ManagedCertificateACMEState{
			Account: model.ManagedCertificateACMEAccountState{
				KeyPEM:       []byte("account-key"),
				Registration: json.RawMessage(`{"uri":"https://acme-v02.api.letsencrypt.org/acme/acct/12345"}`),
			},
			Renewal: model.ManagedCertificateACMERenewalState{
				NotAfterUnix:        1924905600,
				RenewAtUnix:         1924041600,
				LastRenewedAtUnix:   1921453200,
				LastAttemptAtUnix:   1921453500,
				LastAttemptError:    "rate-limited",
				LastAttemptStatus:   "failed",
				LastAttemptNotAfter: 1924905600,
			},
		},
	}

	if err := manager.saveManagedCertificateState(certificateID, expected); err != nil {
		t.Fatalf("save managed certificate state failed: %v", err)
	}

	actual, ok, err := manager.loadManagedCertificateState(certificateID)
	if err != nil {
		t.Fatalf("load managed certificate state failed: %v", err)
	}
	if !ok {
		t.Fatal("expected managed certificate state to exist")
	}
	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf("managed certificate state mismatch: got %#v want %#v", actual, expected)
	}
}

func TestManagerApplyPreservesPreviousStateOnACMEFailure(t *testing.T) {
	t.Parallel()

	stable := mustCreateTLSMaterial(t, certificateSpec{commonName: "stable.example.com"})
	manager := mustNewManager(
		t,
		t.TempDir(),
		withACMEIssuerFactory(func(request acmeIssueRequest) (acmeIssuer, error) {
			return &fakeACMEIssuer{results: []acmeIssueResult{{Err: errSyntheticACMEFailure}}}, nil
		}),
	)

	if err := manager.Apply(context.Background(), []model.ManagedCertificateBundle{
		{
			ID:      57,
			Domain:  "stable.example.com",
			CertPEM: string(stable.CertPEM),
			KeyPEM:  string(stable.KeyPEM),
		},
	}, []model.ManagedCertificatePolicy{
		{
			ID:              57,
			Domain:          "stable.example.com",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "local_http01",
			CertificateType: "uploaded",
			Usage:           "https",
		},
	}); err != nil {
		t.Fatalf("initial apply failed: %v", err)
	}

	err := manager.Apply(context.Background(), []model.ManagedCertificateBundle{
		{
			ID:      57,
			Domain:  "stable.example.com",
			CertPEM: string(stable.CertPEM),
			KeyPEM:  string(stable.KeyPEM),
		},
	}, []model.ManagedCertificatePolicy{
		{
			ID:              57,
			Domain:          "stable.example.com",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "local_http01",
			CertificateType: "uploaded",
			Usage:           "https",
		},
		{
			ID:              58,
			Domain:          "broken-acme.example.com",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "local_http01",
			CertificateType: "acme",
			Usage:           "https",
		},
	})
	if err == nil {
		t.Fatal("expected acme apply failure")
	}

	info, err := manager.CertificateInfo(57)
	if err != nil {
		t.Fatalf("stable certificate info failed after acme error: %v", err)
	}
	if info.Fingerprint != stable.Fingerprint {
		t.Fatalf("expected stable state to be preserved, got %q want %q", info.Fingerprint, stable.Fingerprint)
	}
}

// Failure backoff (R5② / R4): the initial issuance failure records a backoff
// window, so a subsequent re-attempt inside that window is deferred. Drive both
// managers with a controllable clock so the recreated manager observes a `now`
// past the persistent-class backoff window and the re-issuance can proceed,
// exercising the account-state reuse this test is about.

// TestManagerApplyRecordsBackoffForNonIssuerFailures pins the fix for non-issuer
// renewal failures: an error raised OUTSIDE issuer.Issue (here, the issuer
// factory itself failing) must still record failure backoff, so the certificate
// is not retried every heartbeat without a backoff window. Before the fix only
// issuer.Issue errors were recorded in loadOrIssueACMEUnlocked; factory, request,
// parse and persist failures returned unrecorded.

type certificateSpec struct {
	commonName string
	isCA       bool
	notBefore  time.Time
	notAfter   time.Time
}

type tlsMaterial struct {
	CertPEM     []byte
	KeyPEM      []byte
	Fingerprint string
	Leaf        *x509.Certificate
}

func mustNewManager(t *testing.T, dataDir string, opts ...Option) *Manager {
	t.Helper()

	manager, err := NewManager(dataDir, opts...)
	if err != nil {
		t.Fatalf("new manager failed: %v", err)
	}
	return manager
}

func mustCreateTLSMaterial(t *testing.T, spec certificateSpec) tlsMaterial {
	t.Helper()

	// These tests exercise certificate lifecycle semantics, not RSA specifically.
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	template := &x509.Certificate{
		SerialNumber:          big.NewInt(time.Now().UnixNano()),
		Subject:               pkix.Name{CommonName: spec.commonName},
		NotBefore:             firstTime(spec.notBefore, time.Now().Add(-time.Hour)),
		NotAfter:              firstTime(spec.notAfter, time.Now().Add(24*time.Hour)),
		BasicConstraintsValid: true,
		IsCA:                  spec.isCA,
	}
	if spec.isCA {
		template.KeyUsage = x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature
		template.MaxPathLenZero = true
	} else {
		template.KeyUsage = x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment
		template.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
		if ip := net.ParseIP(spec.commonName); ip != nil {
			template.Subject = pkix.Name{}
			template.IPAddresses = []net.IP{ip}
		} else {
			template.DNSNames = []string{spec.commonName}
		}
	}

	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}

	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse certificate: %v", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("marshal private key: %v", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	fingerprint, err := FingerprintFromPEM(certPEM)
	if err != nil {
		t.Fatalf("fingerprint failed: %v", err)
	}

	return tlsMaterial{
		CertPEM:     certPEM,
		KeyPEM:      keyPEM,
		Fingerprint: fingerprint,
		Leaf:        leaf,
	}
}

func firstTime(value, fallback time.Time) time.Time {
	if value.IsZero() {
		return fallback
	}
	return value
}

type fakeACMEIssuer struct {
	requests []acmeIssueRequest
	results  []acmeIssueResult
}

func (f *fakeACMEIssuer) Issue(_ context.Context, request acmeIssueRequest) (acmeIssueResult, error) {
	f.requests = append(f.requests, request)
	if len(f.results) == 0 {
		return acmeIssueResult{}, assertUnreachableError{message: "unexpected acme issue call"}
	}

	result := f.results[0]
	f.results = f.results[1:]
	if result.Err != nil {
		return acmeIssueResult{}, result.Err
	}
	return populateTestACMEAccount(request, result)
}

func populateTestACMEAccount(request acmeIssueRequest, result acmeIssueResult) (acmeIssueResult, error) {
	if len(result.AccountKeyPEM) == 0 {
		if len(request.AccountKeyPEM) > 0 {
			result.AccountKeyPEM = append([]byte(nil), request.AccountKeyPEM...)
		} else {
			key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
			if err != nil {
				return acmeIssueResult{}, err
			}
			der, err := x509.MarshalECPrivateKey(key)
			if err != nil {
				return acmeIssueResult{}, err
			}
			result.AccountKeyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der})
		}
	}
	if result.Account.URI == "" {
		result.Account = acmeflow.AccountMetadata{
			Version:      acmeflow.AccountMetadataVersion,
			DirectoryURL: firstNonEmpty(request.DirectoryURL, acme.LetsEncryptURL),
			Email:        request.Email,
			URI:          "https://acme.test/account/fake",
		}
	}
	return result, nil
}

type assertUnreachableError struct {
	message string
}

func (e assertUnreachableError) Error() string {
	return e.message
}

var errSyntheticACMEFailure = assertUnreachableError{message: "synthetic acme failure"}

type partialStateACMEIssuer struct {
	result acmeIssueResult
	err    error
}

func (i partialStateACMEIssuer) Issue(_ context.Context, _ acmeIssueRequest) (acmeIssueResult, error) {
	return i.result, i.err
}
