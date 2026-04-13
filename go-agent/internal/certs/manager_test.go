package certs

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/go-acme/lego/v4/registration"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

func TestFingerprintFromPEMRejectsInvalidPEM(t *testing.T) {
	if _, err := FingerprintFromPEM([]byte("invalid")); err == nil {
		t.Fatal("expected invalid cert pem to fail")
	}
}

func TestFingerprintFromPEMReturnsSHA256OfDER(t *testing.T) {
	der, pemBytes := mustCreateSelfSignedCertPEM(t, certificateSpec{commonName: "task9-test"})
	sum := sha256.Sum256(der)
	expected := hex.EncodeToString(sum[:])

	got, err := FingerprintFromPEM(pemBytes)
	if err != nil {
		t.Fatalf("fingerprint failed: %v", err)
	}
	if got != expected {
		t.Fatalf("unexpected fingerprint: got %q want %q", got, expected)
	}
}

func TestFingerprintFromPEMRejectsNonCertificateBlock(t *testing.T) {
	block := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: []byte{1, 2, 3}})
	if _, err := FingerprintFromPEM(block); err == nil {
		t.Fatal("expected non-certificate pem block to fail")
	}
}

func TestFingerprintFromPEMRejectsExtraDataAfterCertificate(t *testing.T) {
	_, certPEM := mustCreateSelfSignedCertPEM(t, certificateSpec{commonName: "task9-extra"})
	withExtra := append(certPEM, []byte("extra")...)

	if _, err := FingerprintFromPEM(withExtra); err == nil {
		t.Fatal("expected extra data after certificate pem to fail")
	}
}

func TestManagedCertificateReportsExposeLocalHTTP01MaterialState(t *testing.T) {
	material := mustCreateTLSMaterial(t, certificateSpec{commonName: "sync.example.com"})
	manager := mustNewManager(t, t.TempDir())

	err := manager.Apply(context.Background(), []model.ManagedCertificateBundle{{
		ID:       21,
		Domain:   "sync.example.com",
		Revision: 3,
		CertPEM:  string(material.CertPEM),
		KeyPEM:   string(material.KeyPEM),
	}}, []model.ManagedCertificatePolicy{{
		ID:              21,
		Domain:          "sync.example.com",
		Enabled:         true,
		Scope:           "domain",
		IssuerMode:      "local_http01",
		Status:          "pending",
		Revision:        3,
		Usage:           "https",
		CertificateType: "uploaded",
		ACMEInfo: model.ManagedCertificateACMEInfo{
			MainDomain: "sync.example.com",
		},
	}})
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	reports, err := manager.ManagedCertificateReports(context.Background())
	if err != nil {
		t.Fatalf("ManagedCertificateReports() error = %v", err)
	}
	if len(reports) != 1 {
		t.Fatalf("len(reports) = %d", len(reports))
	}
	if reports[0].ID != 21 || reports[0].Status != "active" {
		t.Fatalf("unexpected report metadata: %+v", reports[0])
	}
	if reports[0].MaterialHash != hashManagedCertificateMaterial(material.CertPEM, material.KeyPEM) {
		t.Fatalf("unexpected material hash: %+v", reports[0])
	}
	if reports[0].ACMEInfo.MainDomain != "sync.example.com" {
		t.Fatalf("unexpected ACME info: %+v", reports[0].ACMEInfo)
	}
}

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

func TestManagerServerCertificateForHostPrefersExactMatch(t *testing.T) {
	t.Parallel()

	exact := mustCreateTLSMaterial(t, certificateSpec{commonName: "api.example.com"})
	wildcard := mustCreateTLSMaterial(t, certificateSpec{commonName: "*.example.com"})
	manager := mustNewManager(t, t.TempDir())

	err := manager.Apply(context.Background(), []model.ManagedCertificateBundle{
		{ID: 21, Domain: "*.example.com", Revision: 1, CertPEM: string(wildcard.CertPEM), KeyPEM: string(wildcard.KeyPEM)},
		{ID: 22, Domain: "api.example.com", Revision: 2, CertPEM: string(exact.CertPEM), KeyPEM: string(exact.KeyPEM)},
	}, []model.ManagedCertificatePolicy{
		{ID: 21, Domain: "*.example.com", Enabled: true, Usage: "https", CertificateType: "uploaded", Scope: "domain", Revision: 1},
		{ID: 22, Domain: "api.example.com", Enabled: true, Usage: "https", CertificateType: "uploaded", Scope: "domain", Revision: 2},
	})
	if err != nil {
		t.Fatalf("apply failed: %v", err)
	}

	cert, err := manager.ServerCertificateForHost(context.Background(), "api.example.com")
	if err != nil {
		t.Fatalf("ServerCertificateForHost failed: %v", err)
	}
	if cert.Leaf == nil || cert.Leaf.Subject.CommonName != "api.example.com" {
		t.Fatalf("expected exact-match certificate, got %+v", cert.Leaf)
	}
}

func TestManagerServerCertificateForHostMatchesWildcard(t *testing.T) {
	t.Parallel()

	wildcard := mustCreateTLSMaterial(t, certificateSpec{commonName: "*.example.com"})
	manager := mustNewManager(t, t.TempDir())

	err := manager.Apply(context.Background(), []model.ManagedCertificateBundle{
		{ID: 31, Domain: "*.example.com", Revision: 1, CertPEM: string(wildcard.CertPEM), KeyPEM: string(wildcard.KeyPEM)},
	}, []model.ManagedCertificatePolicy{
		{ID: 31, Domain: "*.example.com", Enabled: true, Usage: "https", CertificateType: "uploaded", Scope: "domain", Revision: 1},
	})
	if err != nil {
		t.Fatalf("apply failed: %v", err)
	}

	cert, err := manager.ServerCertificateForHost(context.Background(), "edge.example.com")
	if err != nil {
		t.Fatalf("ServerCertificateForHost failed: %v", err)
	}
	if cert.Leaf == nil || cert.Leaf.Subject.CommonName != "*.example.com" {
		t.Fatalf("expected wildcard certificate, got %+v", cert.Leaf)
	}
}

func TestLegoACMEIssuerRespectsContextCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := legoACMEIssuer{}.Issue(ctx, acmeIssueRequest{})
	if err == nil {
		t.Fatal("expected canceled context to return an error")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context cancellation error, got %v", err)
	}
}

func TestManagerApplyRejectsUploadedCertificateWithoutBundlePEM(t *testing.T) {
	t.Parallel()

	manager := mustNewManager(t, t.TempDir())

	err := manager.Apply(context.Background(), nil, []model.ManagedCertificatePolicy{
		{
			ID:              111,
			Domain:          "uploaded-missing.example.com",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "local_http01",
			CertificateType: "uploaded",
			Usage:           "https",
		},
	})
	if err == nil {
		t.Fatal("expected uploaded certificate without bundle pem to fail")
	}
	if got := err.Error(); got != "certificate 111: uploaded certificates require control-plane PEM material" {
		t.Fatalf("unexpected error: %q", got)
	}
}

func TestManagerApplyUsesBundledInternalCAMaterialWhenBundlePEMExists(t *testing.T) {
	t.Parallel()

	bundle := mustCreateTLSMaterial(t, certificateSpec{commonName: "bundle.example.com"})
	manager := mustNewManager(t, t.TempDir())

	err := manager.Apply(context.Background(), []model.ManagedCertificateBundle{
		{
			ID:      112,
			Domain:  "bundle.example.com",
			CertPEM: string(bundle.CertPEM),
			KeyPEM:  string(bundle.KeyPEM),
		},
	}, []model.ManagedCertificatePolicy{
		{
			ID:              112,
			Domain:          "internal.example.com",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "local_http01",
			CertificateType: "internal_ca",
			Usage:           "relay_ca",
			SelfSigned:      true,
		},
	})
	if err != nil {
		t.Fatalf("apply failed: %v", err)
	}

	info, err := manager.CertificateInfo(112)
	if err != nil {
		t.Fatalf("certificate info failed: %v", err)
	}
	if info.Fingerprint != bundle.Fingerprint {
		t.Fatal("expected bundled internal_ca material to be preserved")
	}
}

func TestManagerApplyUsesACMEPathEvenWhenBundlePEMExists(t *testing.T) {
	t.Parallel()

	bundle := mustCreateTLSMaterial(t, certificateSpec{commonName: "bundle.example.com"})
	issued := mustCreateTLSMaterial(t, certificateSpec{commonName: "acme.example.com"})
	fake := &fakeACMEIssuer{results: []acmeIssueResult{{CertPEM: issued.CertPEM, KeyPEM: issued.KeyPEM}}}
	manager := mustNewManager(
		t,
		t.TempDir(),
		withACMEIssuerFactory(func(request acmeIssueRequest) (acmeIssuer, error) {
			return fake, nil
		}),
	)

	err := manager.Apply(context.Background(), []model.ManagedCertificateBundle{
		{
			ID:      113,
			Domain:  "bundle.example.com",
			CertPEM: string(bundle.CertPEM),
			KeyPEM:  string(bundle.KeyPEM),
		},
	}, []model.ManagedCertificatePolicy{
		{
			ID:              113,
			Domain:          "acme.example.com",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "local_http01",
			CertificateType: "acme",
			Usage:           "https",
		},
	})
	if err != nil {
		t.Fatalf("apply failed: %v", err)
	}
	if len(fake.requests) != 1 {
		t.Fatalf("expected one acme issuance request, got %d", len(fake.requests))
	}

	info, err := manager.CertificateInfo(113)
	if err != nil {
		t.Fatalf("certificate info failed: %v", err)
	}
	if info.Fingerprint != issued.Fingerprint {
		t.Fatalf("expected acme-issued fingerprint, got %q want %q", info.Fingerprint, issued.Fingerprint)
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

func TestManagerTrustedCAPoolBuildsPoolFromCertificateIDs(t *testing.T) {
	t.Parallel()

	caOne := mustCreateTLSMaterial(t, certificateSpec{commonName: "ca-one", isCA: true})
	caTwo := mustCreateTLSMaterial(t, certificateSpec{commonName: "ca-two", isCA: true})
	manager := mustNewManager(t, t.TempDir())

	err := manager.Apply(context.Background(), []model.ManagedCertificateBundle{
		{ID: 21, Domain: "ca-one", CertPEM: string(caOne.CertPEM), KeyPEM: string(caOne.KeyPEM)},
		{ID: 22, Domain: "ca-two", CertPEM: string(caTwo.CertPEM), KeyPEM: string(caTwo.KeyPEM)},
	}, []model.ManagedCertificatePolicy{
		{ID: 21, Domain: "ca-one", Enabled: true, Usage: "relay_ca", CertificateType: "uploaded", SelfSigned: true},
		{ID: 22, Domain: "ca-two", Enabled: true, Usage: "relay_ca", CertificateType: "uploaded", SelfSigned: true},
	})
	if err != nil {
		t.Fatalf("apply failed: %v", err)
	}

	pool, err := manager.TrustedCAPool(context.Background(), []int{21, 22})
	if err != nil {
		t.Fatalf("trusted ca pool failed: %v", err)
	}
	if pool == nil {
		t.Fatal("expected cert pool")
	}

	subjects := pool.Subjects()
	if len(subjects) != 2 {
		t.Fatalf("unexpected subject count: got %d want 2", len(subjects))
	}
	if !containsSubject(subjects, caOne.Leaf.RawSubject) {
		t.Fatal("expected first CA subject in pool")
	}
	if !containsSubject(subjects, caTwo.Leaf.RawSubject) {
		t.Fatal("expected second CA subject in pool")
	}
}

func TestManagerServerCertificateRejectsCAOnlyUsage(t *testing.T) {
	t.Parallel()

	cert := mustCreateTLSMaterial(t, certificateSpec{commonName: "ca-only.example.com", isCA: true})
	manager := mustNewManager(t, t.TempDir())

	err := manager.Apply(context.Background(), []model.ManagedCertificateBundle{
		{ID: 23, Domain: "ca-only.example.com", CertPEM: string(cert.CertPEM), KeyPEM: string(cert.KeyPEM)},
	}, []model.ManagedCertificatePolicy{
		{ID: 23, Domain: "ca-only.example.com", Enabled: true, Usage: "relay_ca", CertificateType: "uploaded"},
	})
	if err != nil {
		t.Fatalf("apply failed: %v", err)
	}

	if _, err := manager.ServerCertificate(context.Background(), 23); err == nil {
		t.Fatal("expected server certificate lookup to reject relay_ca usage")
	}
}

func TestManagerTrustedCAPoolRejectsServerOnlyUsage(t *testing.T) {
	t.Parallel()

	cert := mustCreateTLSMaterial(t, certificateSpec{commonName: "https-only.example.com"})
	manager := mustNewManager(t, t.TempDir())

	err := manager.Apply(context.Background(), []model.ManagedCertificateBundle{
		{ID: 24, Domain: "https-only.example.com", CertPEM: string(cert.CertPEM), KeyPEM: string(cert.KeyPEM)},
	}, []model.ManagedCertificatePolicy{
		{ID: 24, Domain: "https-only.example.com", Enabled: true, Usage: "https", CertificateType: "uploaded"},
	})
	if err != nil {
		t.Fatalf("apply failed: %v", err)
	}

	if _, err := manager.TrustedCAPool(context.Background(), []int{24}); err == nil {
		t.Fatal("expected trusted ca pool lookup to reject https-only usage")
	}
}

func TestManagerApplyGeneratesAndPersistsInternalCA(t *testing.T) {
	t.Parallel()

	dataDir := t.TempDir()
	manager := mustNewManager(t, dataDir)
	policy := model.ManagedCertificatePolicy{
		ID:              31,
		Domain:          "internal-ca.example.com",
		Enabled:         true,
		Usage:           "relay_ca",
		CertificateType: "internal_ca",
		SelfSigned:      true,
	}

	if err := manager.Apply(context.Background(), nil, []model.ManagedCertificatePolicy{policy}); err != nil {
		t.Fatalf("apply failed: %v", err)
	}

	infoBefore, err := manager.CertificateInfo(31)
	if err != nil {
		t.Fatalf("certificate info failed: %v", err)
	}
	if infoBefore.CertificateType != "internal_ca" {
		t.Fatalf("unexpected certificate type: %q", infoBefore.CertificateType)
	}

	persistedCert := filepath.Join(dataDir, "certs", "managed", "31", "cert.pem")
	if _, err := tls.LoadX509KeyPair(persistedCert, filepath.Join(dataDir, "certs", "managed", "31", "key.pem")); err != nil {
		t.Fatalf("expected persisted internal_ca material: %v", err)
	}

	recreated := mustNewManager(t, dataDir)
	if err := recreated.Apply(context.Background(), nil, []model.ManagedCertificatePolicy{policy}); err != nil {
		t.Fatalf("recreated apply failed: %v", err)
	}

	infoAfter, err := recreated.CertificateInfo(31)
	if err != nil {
		t.Fatalf("recreated certificate info failed: %v", err)
	}
	if infoAfter.Fingerprint != infoBefore.Fingerprint {
		t.Fatalf("expected persisted fingerprint, got %q want %q", infoAfter.Fingerprint, infoBefore.Fingerprint)
	}
}

func TestManagerApplyHotReloadSwapsActiveMaterial(t *testing.T) {
	t.Parallel()

	manager := mustNewManager(t, t.TempDir())
	first := mustCreateTLSMaterial(t, certificateSpec{commonName: "reload-one.example.com"})
	second := mustCreateTLSMaterial(t, certificateSpec{commonName: "reload-two.example.com"})
	policies := []model.ManagedCertificatePolicy{
		{
			ID:              41,
			Domain:          "reload.example.com",
			Enabled:         true,
			Usage:           "https",
			CertificateType: "uploaded",
		},
	}

	if err := manager.Apply(context.Background(), []model.ManagedCertificateBundle{
		{ID: 41, Domain: "reload.example.com", CertPEM: string(first.CertPEM), KeyPEM: string(first.KeyPEM)},
	}, policies); err != nil {
		t.Fatalf("first apply failed: %v", err)
	}

	before, err := manager.CertificateInfo(41)
	if err != nil {
		t.Fatalf("first certificate info failed: %v", err)
	}

	if err := manager.Apply(context.Background(), []model.ManagedCertificateBundle{
		{ID: 41, Domain: "reload.example.com", CertPEM: string(second.CertPEM), KeyPEM: string(second.KeyPEM)},
	}, policies); err != nil {
		t.Fatalf("second apply failed: %v", err)
	}

	after, err := manager.CertificateInfo(41)
	if err != nil {
		t.Fatalf("second certificate info failed: %v", err)
	}
	if before.Fingerprint == after.Fingerprint {
		t.Fatal("expected active certificate fingerprint to change after reload")
	}
	if after.Fingerprint != second.Fingerprint {
		t.Fatalf("unexpected post-reload fingerprint: got %q want %q", after.Fingerprint, second.Fingerprint)
	}
}

func TestManagerApplyIssuesACMECertificateUsingLocalHTTP01(t *testing.T) {
	t.Parallel()

	issued := mustCreateTLSMaterial(t, certificateSpec{commonName: "acme-http.example.com"})
	fake := &fakeACMEIssuer{results: []acmeIssueResult{{CertPEM: issued.CertPEM, KeyPEM: issued.KeyPEM}}}
	manager := mustNewManager(
		t,
		t.TempDir(),
		withACMEIssuerFactory(func(request acmeIssueRequest) (acmeIssuer, error) {
			return fake, nil
		}),
	)

	err := manager.Apply(context.Background(), nil, []model.ManagedCertificatePolicy{
		{
			ID:              51,
			Domain:          "acme-http.example.com",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "local_http01",
			CertificateType: "acme",
			Usage:           "https",
		},
	})
	if err != nil {
		t.Fatalf("apply failed: %v", err)
	}

	if len(fake.requests) != 1 {
		t.Fatalf("expected one acme issuance request, got %d", len(fake.requests))
	}
	if fake.requests[0].ChallengeType != challengeTypeHTTP01 {
		t.Fatalf("unexpected challenge type: %q", fake.requests[0].ChallengeType)
	}
	if fake.requests[0].IssuerMode != "local_http01" {
		t.Fatalf("unexpected issuer mode: %q", fake.requests[0].IssuerMode)
	}
	if fake.requests[0].Profile != "" {
		t.Fatalf("unexpected profile for domain certificate: %q", fake.requests[0].Profile)
	}

	info, err := manager.CertificateInfo(51)
	if err != nil {
		t.Fatalf("certificate info failed: %v", err)
	}
	if info.Fingerprint != issued.Fingerprint {
		t.Fatalf("unexpected fingerprint: got %q want %q", info.Fingerprint, issued.Fingerprint)
	}
}

func TestManagerApplyIssuesIPACMECertificateUsingShortLivedProfile(t *testing.T) {
	t.Parallel()

	issued := mustCreateTLSMaterial(t, certificateSpec{commonName: "203.0.113.9"})
	fake := &fakeACMEIssuer{results: []acmeIssueResult{{CertPEM: issued.CertPEM, KeyPEM: issued.KeyPEM}}}
	manager := mustNewManager(
		t,
		t.TempDir(),
		withACMEIssuerFactory(func(request acmeIssueRequest) (acmeIssuer, error) {
			return fake, nil
		}),
	)

	err := manager.Apply(context.Background(), nil, []model.ManagedCertificatePolicy{
		{
			ID:              5101,
			Domain:          "203.0.113.9",
			Enabled:         true,
			Scope:           "ip",
			IssuerMode:      "local_http01",
			CertificateType: "acme",
			Usage:           "https",
		},
	})
	if err != nil {
		t.Fatalf("apply failed: %v", err)
	}

	if len(fake.requests) != 1 {
		t.Fatalf("expected one acme issuance request, got %d", len(fake.requests))
	}
	if fake.requests[0].ChallengeType != challengeTypeHTTP01 {
		t.Fatalf("unexpected challenge type: %q", fake.requests[0].ChallengeType)
	}
	if fake.requests[0].Profile != "shortlived" {
		t.Fatalf("unexpected profile for ip certificate: %q", fake.requests[0].Profile)
	}
}

func TestManagerApplyReusesFreshShortLivedIPACMECertificate(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 13, 12, 0, 0, 0, time.UTC)
	first := mustCreateTLSMaterial(t, certificateSpec{
		commonName: "203.0.113.10",
		notBefore:  now.Add(-time.Hour),
		notAfter:   now.Add(6 * 24 * time.Hour),
	})
	second := mustCreateTLSMaterial(t, certificateSpec{
		commonName: "203.0.113.10",
		notBefore:  now.Add(-time.Hour),
		notAfter:   now.Add(6 * 24 * time.Hour),
	})
	fake := &fakeACMEIssuer{
		results: []acmeIssueResult{
			{CertPEM: first.CertPEM, KeyPEM: first.KeyPEM},
			{CertPEM: second.CertPEM, KeyPEM: second.KeyPEM},
		},
	}
	manager := mustNewManager(
		t,
		t.TempDir(),
		withNow(func() time.Time { return now }),
		withACMEIssuerFactory(func(request acmeIssueRequest) (acmeIssuer, error) {
			return fake, nil
		}),
	)

	policy := model.ManagedCertificatePolicy{
		ID:              5102,
		Domain:          "203.0.113.10",
		Enabled:         true,
		Scope:           "ip",
		IssuerMode:      "local_http01",
		CertificateType: "acme",
		Usage:           "https",
	}

	if err := manager.Apply(context.Background(), nil, []model.ManagedCertificatePolicy{policy}); err != nil {
		t.Fatalf("initial apply failed: %v", err)
	}
	if err := manager.Apply(context.Background(), nil, []model.ManagedCertificatePolicy{policy}); err != nil {
		t.Fatalf("second apply failed: %v", err)
	}

	if len(fake.requests) != 1 {
		t.Fatalf("expected fresh short-lived ip cert to be reused, got %d issuance calls", len(fake.requests))
	}
	info, err := manager.CertificateInfo(policy.ID)
	if err != nil {
		t.Fatalf("certificate info failed: %v", err)
	}
	if info.Fingerprint != first.Fingerprint {
		t.Fatalf("expected first fingerprint to remain active, got %q want %q", info.Fingerprint, first.Fingerprint)
	}
}

func TestManagerApplyUsesDNSChallengeForMasterCFDNSOnLocalMaster(t *testing.T) {
	t.Parallel()

	issued := mustCreateTLSMaterial(t, certificateSpec{commonName: "acme-dns.example.com"})
	fake := &fakeACMEIssuer{results: []acmeIssueResult{{CertPEM: issued.CertPEM, KeyPEM: issued.KeyPEM}}}
	manager := mustNewManager(
		t,
		t.TempDir(),
		WithNodeRole("master"),
		WithLocalAgent(true),
		WithCloudflareAPITokens("dns-token", ""),
		withACMEIssuerFactory(func(request acmeIssueRequest) (acmeIssuer, error) {
			return fake, nil
		}),
	)

	err := manager.Apply(context.Background(), nil, []model.ManagedCertificatePolicy{
		{
			ID:              52,
			Domain:          "acme-dns.example.com",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "master_cf_dns",
			CertificateType: "acme",
			Usage:           "https",
		},
	})
	if err != nil {
		t.Fatalf("apply failed: %v", err)
	}

	if len(fake.requests) != 1 {
		t.Fatalf("expected one acme issuance request, got %d", len(fake.requests))
	}
	if fake.requests[0].ChallengeType != challengeTypeDNS01Cloudflare {
		t.Fatalf("unexpected challenge type: %q", fake.requests[0].ChallengeType)
	}
	if fake.requests[0].CloudflareDNSAPIToken != "dns-token" {
		t.Fatalf("unexpected cloudflare dns token: %q", fake.requests[0].CloudflareDNSAPIToken)
	}
	if fake.requests[0].CloudflareZoneAPIToken != "dns-token" {
		t.Fatalf("unexpected cloudflare zone token: %q", fake.requests[0].CloudflareZoneAPIToken)
	}
}

func TestManagerApplyRejectsMasterCFDNSOutsideLocalMaster(t *testing.T) {
	t.Parallel()

	manager := mustNewManager(t, t.TempDir())

	err := manager.Apply(context.Background(), nil, []model.ManagedCertificatePolicy{
		{
			ID:              53,
			Domain:          "remote-dns.example.com",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "master_cf_dns",
			CertificateType: "acme",
			Usage:           "https",
		},
	})
	if err == nil {
		t.Fatal("expected master_cf_dns apply to fail outside local master")
	}
	if got := err.Error(); got != "certificate 53: master_cf_dns issuance is only allowed on the local master agent" {
		t.Fatalf("unexpected error: %q", got)
	}
}

func TestManagerApplyRejectsMasterCFDNSWhenCloudflareCredentialsMissing(t *testing.T) {
	t.Parallel()

	manager := mustNewManager(
		t,
		t.TempDir(),
		WithNodeRole("master"),
		WithLocalAgent(true),
	)

	err := manager.Apply(context.Background(), nil, []model.ManagedCertificatePolicy{
		{
			ID:              54,
			Domain:          "missing-creds.example.com",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "master_cf_dns",
			CertificateType: "acme",
			Usage:           "https",
		},
	})
	if err == nil {
		t.Fatal("expected missing credentials error")
	}
	if got := err.Error(); got != "certificate 54: cloudflare credentials are required for master_cf_dns issuance" {
		t.Fatalf("unexpected error: %q", got)
	}
}

func TestManagerApplyPersistsLocallyIssuedACMEMaterialAcrossRecreation(t *testing.T) {
	t.Parallel()

	dataDir := t.TempDir()
	issued := mustCreateTLSMaterial(t, certificateSpec{
		commonName: "persist-acme.example.com",
		notBefore:  time.Now().Add(-time.Hour),
		notAfter:   time.Now().Add(90 * 24 * time.Hour),
	})
	initialFactoryCalls := 0
	manager := mustNewManager(
		t,
		dataDir,
		withACMEIssuerFactory(func(request acmeIssueRequest) (acmeIssuer, error) {
			initialFactoryCalls++
			return &fakeACMEIssuer{results: []acmeIssueResult{{CertPEM: issued.CertPEM, KeyPEM: issued.KeyPEM}}}, nil
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
	if initialFactoryCalls != 1 {
		t.Fatalf("expected one initial issuance, got %d", initialFactoryCalls)
	}

	before, err := manager.CertificateInfo(55)
	if err != nil {
		t.Fatalf("initial certificate info failed: %v", err)
	}

	recreatedFactoryCalls := 0
	recreated := mustNewManager(
		t,
		dataDir,
		withACMEIssuerFactory(func(request acmeIssueRequest) (acmeIssuer, error) {
			recreatedFactoryCalls++
			return &fakeACMEIssuer{results: []acmeIssueResult{{Err: assertUnreachableError{message: "issuer should not be called when persisted acme material exists"}}}}, nil
		}),
	)
	if err := recreated.Apply(context.Background(), nil, []model.ManagedCertificatePolicy{policy}); err != nil {
		t.Fatalf("recreated apply failed: %v", err)
	}
	if recreatedFactoryCalls != 0 {
		t.Fatalf("expected persisted material to avoid reissuance, got %d issuer calls", recreatedFactoryCalls)
	}

	after, err := recreated.CertificateInfo(55)
	if err != nil {
		t.Fatalf("recreated certificate info failed: %v", err)
	}
	if after.Fingerprint != before.Fingerprint {
		t.Fatalf("expected persisted fingerprint, got %q want %q", after.Fingerprint, before.Fingerprint)
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

func TestManagerApplyReusesManagedACMEStateOnRecreation(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 10, 12, 0, 0, 0, time.UTC)
	first := mustCreateTLSMaterial(t, certificateSpec{
		commonName: "managed-state.example.com",
		notBefore:  now.Add(-24 * time.Hour),
		notAfter:   now.Add(2 * time.Hour),
	})
	second := mustCreateTLSMaterial(t, certificateSpec{
		commonName: "managed-state.example.com",
		notBefore:  now.Add(-time.Hour),
		notAfter:   now.Add(90 * 24 * time.Hour),
	})
	initialAccountKey := []byte("acme-account-key")
	initialRegistration := &registration.Resource{
		URI: "https://acme-v02.api.letsencrypt.org/acme/acct/4242",
	}
	dataDir := t.TempDir()
	policy := model.ManagedCertificatePolicy{
		ID:              9052,
		Domain:          "managed-state.example.com",
		Enabled:         true,
		Scope:           "domain",
		IssuerMode:      "local_http01",
		CertificateType: "acme",
		Usage:           "https",
	}

	initialManager := mustNewManager(
		t,
		dataDir,
		withNow(func() time.Time { return now }),
		withRenewBefore(24*time.Hour),
		withACMEIssuerFactory(func(request acmeIssueRequest) (acmeIssuer, error) {
			return &fakeACMEIssuer{
				results: []acmeIssueResult{
					{
						CertPEM:       first.CertPEM,
						KeyPEM:        first.KeyPEM,
						AccountKeyPEM: initialAccountKey,
						Registration:  initialRegistration,
					},
				},
			}, nil
		}),
	)
	if err := initialManager.Apply(context.Background(), nil, []model.ManagedCertificatePolicy{policy}); err != nil {
		t.Fatalf("initial apply failed: %v", err)
	}

	materialDir := filepath.Join(dataDir, "certs", "managed", "9052")
	if _, err := os.Stat(filepath.Join(materialDir, managedCertificateStateFileName)); err != nil {
		t.Fatalf("expected managed state file to exist: %v", err)
	}
	managedState, ok, err := initialManager.loadManagedCertificateState(9052)
	if err != nil {
		t.Fatalf("load managed state failed: %v", err)
	}
	if !ok || managedState.ACME == nil {
		t.Fatal("expected managed acme state to exist")
	}
	if got := string(managedState.ACME.Account.KeyPEM); got != string(initialAccountKey) {
		t.Fatalf("expected managed state account key, got %q", got)
	}
	if managedState.ACME.Renewal.NotAfterUnix == 0 || managedState.ACME.Renewal.RenewAtUnix == 0 {
		t.Fatalf("expected renewal metadata to be persisted, got %+v", managedState.ACME.Renewal)
	}

	for _, name := range []string{"acme_account_key.pem", "acme_registration.json", "local_metadata.json"} {
		if err := os.Remove(filepath.Join(materialDir, name)); err != nil && !os.IsNotExist(err) {
			t.Fatalf("remove legacy acme state file %s failed: %v", name, err)
		}
	}

	recreatedFake := &fakeACMEIssuer{
		results: []acmeIssueResult{
			{
				CertPEM: second.CertPEM,
				KeyPEM:  second.KeyPEM,
			},
		},
	}
	recreated := mustNewManager(
		t,
		dataDir,
		withNow(func() time.Time { return now }),
		withRenewBefore(24*time.Hour),
		withACMEIssuerFactory(func(request acmeIssueRequest) (acmeIssuer, error) {
			return recreatedFake, nil
		}),
	)
	if err := recreated.Apply(context.Background(), nil, []model.ManagedCertificatePolicy{policy}); err != nil {
		t.Fatalf("recreated apply failed: %v", err)
	}

	if len(recreatedFake.requests) != 1 {
		t.Fatalf("expected one issuance call after recreation, got %d", len(recreatedFake.requests))
	}
	if got := string(recreatedFake.requests[0].AccountKeyPEM); got != string(initialAccountKey) {
		t.Fatalf("expected account key from managed state, got %q", got)
	}
	if recreatedFake.requests[0].Registration == nil || recreatedFake.requests[0].Registration.URI != initialRegistration.URI {
		t.Fatalf("expected registration from managed state, got %+v", recreatedFake.requests[0].Registration)
	}
}

func TestManagerApplyFallsBackToLegacyMetadataWhenManagedMetadataIsPartial(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 10, 12, 0, 0, 0, time.UTC)
	issued := mustCreateTLSMaterial(t, certificateSpec{
		commonName: "partial-managed-metadata.example.com",
		notBefore:  now.Add(-time.Hour),
		notAfter:   now.Add(90 * 24 * time.Hour),
	})
	dataDir := t.TempDir()
	policy := model.ManagedCertificatePolicy{
		ID:              9053,
		Domain:          "partial-managed-metadata.example.com",
		Enabled:         true,
		Scope:           "domain",
		IssuerMode:      "local_http01",
		CertificateType: "acme",
		Usage:           "https",
	}

	initial := mustNewManager(
		t,
		dataDir,
		withNow(func() time.Time { return now }),
		withACMEIssuerFactory(func(request acmeIssueRequest) (acmeIssuer, error) {
			return &fakeACMEIssuer{
				results: []acmeIssueResult{
					{CertPEM: issued.CertPEM, KeyPEM: issued.KeyPEM},
				},
			}, nil
		}),
	)
	if err := initial.Apply(context.Background(), nil, []model.ManagedCertificatePolicy{policy}); err != nil {
		t.Fatalf("initial apply failed: %v", err)
	}

	if err := initial.saveManagedCertificateState(policy.ID, managedCertificateState{
		LocalMetadata: localMaterialMetadata{
			Domain: policy.Domain,
		},
	}); err != nil {
		t.Fatalf("write partial managed metadata failed: %v", err)
	}

	recreatedIssuerCalls := 0
	recreated := mustNewManager(
		t,
		dataDir,
		withNow(func() time.Time { return now }),
		withACMEIssuerFactory(func(request acmeIssueRequest) (acmeIssuer, error) {
			recreatedIssuerCalls++
			return &fakeACMEIssuer{
				results: []acmeIssueResult{
					{Err: assertUnreachableError{message: "issuer should not be called when legacy metadata is complete"}},
				},
			}, nil
		}),
	)
	if err := recreated.Apply(context.Background(), nil, []model.ManagedCertificatePolicy{policy}); err != nil {
		t.Fatalf("recreated apply failed: %v", err)
	}
	if recreatedIssuerCalls != 0 {
		t.Fatalf("expected zero issuer calls, got %d", recreatedIssuerCalls)
	}
}

func TestManagerApplyRegeneratesInternalCAWhenPolicyDomainChanges(t *testing.T) {
	t.Parallel()

	dataDir := t.TempDir()
	manager := mustNewManager(t, dataDir)

	firstPolicy := model.ManagedCertificatePolicy{
		ID:              551,
		Domain:          "internal-one.example.com",
		Enabled:         true,
		Scope:           "domain",
		IssuerMode:      "local_http01",
		CertificateType: "internal_ca",
		Usage:           "relay_ca",
		SelfSigned:      true,
	}
	secondPolicy := firstPolicy
	secondPolicy.Domain = "internal-two.example.com"

	if err := manager.Apply(context.Background(), nil, []model.ManagedCertificatePolicy{firstPolicy}); err != nil {
		t.Fatalf("first apply failed: %v", err)
	}
	firstInfo, err := manager.CertificateInfo(551)
	if err != nil {
		t.Fatalf("first certificate info failed: %v", err)
	}

	if err := manager.Apply(context.Background(), nil, []model.ManagedCertificatePolicy{secondPolicy}); err != nil {
		t.Fatalf("second apply failed: %v", err)
	}
	secondInfo, err := manager.CertificateInfo(551)
	if err != nil {
		t.Fatalf("second certificate info failed: %v", err)
	}

	if firstInfo.Fingerprint == secondInfo.Fingerprint {
		t.Fatal("expected internal_ca material to regenerate when policy domain changes")
	}
}

func TestManagerApplyRecoversFromCorruptPersistedInternalCAMaterial(t *testing.T) {
	t.Parallel()

	dataDir := t.TempDir()
	manager := mustNewManager(t, dataDir)
	policy := model.ManagedCertificatePolicy{
		ID:              553,
		Domain:          "recover-internal.example.com",
		Enabled:         true,
		Scope:           "domain",
		IssuerMode:      "local_http01",
		CertificateType: "internal_ca",
		Usage:           "relay_ca",
		SelfSigned:      true,
	}

	if err := manager.Apply(context.Background(), nil, []model.ManagedCertificatePolicy{policy}); err != nil {
		t.Fatalf("initial apply failed: %v", err)
	}
	before, err := manager.CertificateInfo(553)
	if err != nil {
		t.Fatalf("initial certificate info failed: %v", err)
	}

	if err := os.WriteFile(filepath.Join(dataDir, "certs", "managed", "553", "cert.pem"), []byte("broken"), 0600); err != nil {
		t.Fatalf("corrupt cert write failed: %v", err)
	}

	if err := manager.Apply(context.Background(), nil, []model.ManagedCertificatePolicy{policy}); err != nil {
		t.Fatalf("recovery apply failed: %v", err)
	}
	after, err := manager.CertificateInfo(553)
	if err != nil {
		t.Fatalf("recovered certificate info failed: %v", err)
	}
	if after.Fingerprint == before.Fingerprint {
		t.Fatal("expected corrupt internal_ca material to be regenerated")
	}
}

func TestManagerApplyReissuesACMEWhenPolicyDomainChanges(t *testing.T) {
	t.Parallel()

	first := mustCreateTLSMaterial(t, certificateSpec{
		commonName: "acme-old.example.com",
		notBefore:  time.Now().Add(-time.Hour),
		notAfter:   time.Now().Add(90 * 24 * time.Hour),
	})
	second := mustCreateTLSMaterial(t, certificateSpec{
		commonName: "acme-new.example.com",
		notBefore:  time.Now().Add(-time.Hour),
		notAfter:   time.Now().Add(90 * 24 * time.Hour),
	})
	fake := &fakeACMEIssuer{
		results: []acmeIssueResult{
			{CertPEM: first.CertPEM, KeyPEM: first.KeyPEM},
			{CertPEM: second.CertPEM, KeyPEM: second.KeyPEM},
		},
	}
	manager := mustNewManager(
		t,
		t.TempDir(),
		withACMEIssuerFactory(func(request acmeIssueRequest) (acmeIssuer, error) {
			return fake, nil
		}),
	)

	firstPolicy := model.ManagedCertificatePolicy{
		ID:              552,
		Domain:          "acme-old.example.com",
		Enabled:         true,
		Scope:           "domain",
		IssuerMode:      "local_http01",
		CertificateType: "acme",
		Usage:           "https",
	}
	secondPolicy := firstPolicy
	secondPolicy.Domain = "acme-new.example.com"

	if err := manager.Apply(context.Background(), nil, []model.ManagedCertificatePolicy{firstPolicy}); err != nil {
		t.Fatalf("first apply failed: %v", err)
	}
	if err := manager.Apply(context.Background(), nil, []model.ManagedCertificatePolicy{secondPolicy}); err != nil {
		t.Fatalf("second apply failed: %v", err)
	}

	if len(fake.requests) != 2 {
		t.Fatalf("expected domain change to trigger a second acme issuance, got %d calls", len(fake.requests))
	}
	info, err := manager.CertificateInfo(552)
	if err != nil {
		t.Fatalf("certificate info failed: %v", err)
	}
	if info.Fingerprint != second.Fingerprint {
		t.Fatalf("expected reissued fingerprint after domain change, got %q want %q", info.Fingerprint, second.Fingerprint)
	}
}

func TestManagerApplyRecoversFromCorruptPersistedACMEMetadata(t *testing.T) {
	t.Parallel()

	first := mustCreateTLSMaterial(t, certificateSpec{
		commonName: "recover-acme.example.com",
		notBefore:  time.Now().Add(-time.Hour),
		notAfter:   time.Now().Add(90 * 24 * time.Hour),
	})
	second := mustCreateTLSMaterial(t, certificateSpec{
		commonName: "recover-acme.example.com",
		notBefore:  time.Now().Add(-time.Hour),
		notAfter:   time.Now().Add(90 * 24 * time.Hour),
	})
	fake := &fakeACMEIssuer{
		results: []acmeIssueResult{
			{CertPEM: first.CertPEM, KeyPEM: first.KeyPEM},
			{CertPEM: second.CertPEM, KeyPEM: second.KeyPEM},
		},
	}
	dataDir := t.TempDir()
	manager := mustNewManager(
		t,
		dataDir,
		withACMEIssuerFactory(func(request acmeIssueRequest) (acmeIssuer, error) {
			return fake, nil
		}),
	)
	policy := model.ManagedCertificatePolicy{
		ID:              554,
		Domain:          "recover-acme.example.com",
		Enabled:         true,
		Scope:           "domain",
		IssuerMode:      "local_http01",
		CertificateType: "acme",
		Usage:           "https",
	}

	if err := manager.Apply(context.Background(), nil, []model.ManagedCertificatePolicy{policy}); err != nil {
		t.Fatalf("initial apply failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "certs", "managed", "554", managedCertificateStateFileName), []byte("{not-json"), 0600); err != nil {
		t.Fatalf("corrupt managed state write failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "certs", "managed", "554", "local_metadata.json"), []byte("{not-json"), 0600); err != nil {
		t.Fatalf("corrupt legacy metadata write failed: %v", err)
	}

	if err := manager.Apply(context.Background(), nil, []model.ManagedCertificatePolicy{policy}); err != nil {
		t.Fatalf("recovery apply failed: %v", err)
	}
	if len(fake.requests) != 2 {
		t.Fatalf("expected corrupt managed state to trigger reissuance, got %d calls", len(fake.requests))
	}
	info, err := manager.CertificateInfo(554)
	if err != nil {
		t.Fatalf("certificate info failed: %v", err)
	}
	if info.Fingerprint != second.Fingerprint {
		t.Fatalf("expected reissued fingerprint after metadata corruption, got %q want %q", info.Fingerprint, second.Fingerprint)
	}
}

func TestManagerApplyRenewsExpiringACMECertificate(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 6, 12, 0, 0, 0, time.UTC)
	first := mustCreateTLSMaterial(t, certificateSpec{
		commonName: "renew-acme.example.com",
		notBefore:  now.Add(-24 * time.Hour),
		notAfter:   now.Add(2 * time.Hour),
	})
	second := mustCreateTLSMaterial(t, certificateSpec{
		commonName: "renew-acme.example.com",
		notBefore:  now.Add(-time.Hour),
		notAfter:   now.Add(90 * 24 * time.Hour),
	})
	fake := &fakeACMEIssuer{
		results: []acmeIssueResult{
			{CertPEM: first.CertPEM, KeyPEM: first.KeyPEM},
			{CertPEM: second.CertPEM, KeyPEM: second.KeyPEM},
		},
	}
	dataDir := t.TempDir()
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
		ID:              56,
		Domain:          "renew-acme.example.com",
		Enabled:         true,
		Scope:           "domain",
		IssuerMode:      "local_http01",
		CertificateType: "acme",
		Usage:           "https",
	}

	if err := manager.Apply(context.Background(), nil, []model.ManagedCertificatePolicy{policy}); err != nil {
		t.Fatalf("initial apply failed: %v", err)
	}
	if err := manager.Apply(context.Background(), nil, []model.ManagedCertificatePolicy{policy}); err != nil {
		t.Fatalf("renewal apply failed: %v", err)
	}

	if len(fake.requests) != 2 {
		t.Fatalf("expected two acme issuance calls, got %d", len(fake.requests))
	}
	info, err := manager.CertificateInfo(56)
	if err != nil {
		t.Fatalf("certificate info failed: %v", err)
	}
	if info.Fingerprint != second.Fingerprint {
		t.Fatalf("expected renewed fingerprint, got %q want %q", info.Fingerprint, second.Fingerprint)
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

func TestManagerApplyPersistsACMEAccountStateAfterIssuanceFailure(t *testing.T) {
	t.Parallel()

	dataDir := t.TempDir()
	accountKey := []byte("persisted-account-key")
	registrationResource := &registration.Resource{
		URI: "https://acme-v02.api.letsencrypt.org/acme/acct/9999",
	}
	policy := model.ManagedCertificatePolicy{
		ID:              5701,
		Domain:          "persist-failure.example.com",
		Enabled:         true,
		Scope:           "domain",
		IssuerMode:      "local_http01",
		CertificateType: "acme",
		Usage:           "https",
	}

	initial := mustNewManager(
		t,
		dataDir,
		withACMEIssuerFactory(func(request acmeIssueRequest) (acmeIssuer, error) {
			return partialStateACMEIssuer{
				result: acmeIssueResult{
					AccountKeyPEM: accountKey,
					Registration:  registrationResource,
				},
				err: errSyntheticACMEFailure,
			}, nil
		}),
	)
	err := initial.Apply(context.Background(), nil, []model.ManagedCertificatePolicy{policy})
	if !errors.Is(err, errSyntheticACMEFailure) {
		t.Fatalf("expected synthetic acme failure, got %v", err)
	}

	reissued := mustCreateTLSMaterial(t, certificateSpec{commonName: "persist-failure.example.com"})
	recreatedFake := &fakeACMEIssuer{
		results: []acmeIssueResult{
			{CertPEM: reissued.CertPEM, KeyPEM: reissued.KeyPEM},
		},
	}
	recreated := mustNewManager(
		t,
		dataDir,
		withACMEIssuerFactory(func(request acmeIssueRequest) (acmeIssuer, error) {
			return recreatedFake, nil
		}),
	)
	if err := recreated.Apply(context.Background(), nil, []model.ManagedCertificatePolicy{policy}); err != nil {
		t.Fatalf("recreated apply failed: %v", err)
	}

	if len(recreatedFake.requests) != 1 {
		t.Fatalf("expected one issuance call after persisted failure state, got %d", len(recreatedFake.requests))
	}
	if got := string(recreatedFake.requests[0].AccountKeyPEM); got != string(accountKey) {
		t.Fatalf("expected persisted account key, got %q", got)
	}
	if recreatedFake.requests[0].Registration == nil || recreatedFake.requests[0].Registration.URI != registrationResource.URI {
		t.Fatalf("expected persisted registration, got %+v", recreatedFake.requests[0].Registration)
	}
}

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

	key, err := rsa.GenerateKey(rand.Reader, 2048)
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
		template.DNSNames = []string{spec.commonName}
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
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
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

func mustCreateSelfSignedCertPEM(t *testing.T, spec certificateSpec) ([]byte, []byte) {
	t.Helper()
	material := mustCreateTLSMaterial(t, spec)
	block, _ := pem.Decode(material.CertPEM)
	if block == nil {
		t.Fatal("expected certificate pem block")
	}
	return block.Bytes, material.CertPEM
}

func containsSubject(subjects [][]byte, subject []byte) bool {
	for _, candidate := range subjects {
		if string(candidate) == string(subject) {
			return true
		}
	}
	return false
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
