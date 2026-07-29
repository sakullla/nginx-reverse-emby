package certs

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/pkg/acmeflow"
	"golang.org/x/crypto/acme"
)

func TestFingerprintFromPEMRejectsInvalidPEM(t *testing.T) {
	t.Parallel()
	if _, err := FingerprintFromPEM([]byte("invalid")); err == nil {
		t.Fatal("expected invalid cert pem to fail")
	}
}

func TestFingerprintFromPEMReturnsSHA256OfDER(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
	block := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: []byte{1, 2, 3}})
	if _, err := FingerprintFromPEM(block); err == nil {
		t.Fatal("expected non-certificate pem block to fail")
	}
}

func TestFingerprintFromPEMRejectsExtraDataAfterCertificate(t *testing.T) {
	t.Parallel()
	_, certPEM := mustCreateSelfSignedCertPEM(t, certificateSpec{commonName: "task9-extra"})
	withExtra := append(certPEM, []byte("extra")...)

	if _, err := FingerprintFromPEM(withExtra); err == nil {
		t.Fatal("expected extra data after certificate pem to fail")
	}
}

func TestManagedCertificateReportsExposeLocalHTTP01MaterialState(t *testing.T) {
	t.Parallel()
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
	if reports[0].NotAfter == "" {
		t.Fatalf("expected report to expose leaf not_after: %+v", reports[0])
	}
	if parsed, parseErr := time.Parse(time.RFC3339, reports[0].NotAfter); parseErr != nil || !parsed.After(time.Now()) {
		t.Fatalf("unexpected not_after %q (parse error %v)", reports[0].NotAfter, parseErr)
	}
	if reports[0].ACMEInfo.MainDomain != "sync.example.com" {
		t.Fatalf("unexpected ACME info: %+v", reports[0].ACMEInfo)
	}
}

func TestManagedCertificateReportsExposeMasterCFDNSPublishedMaterial(t *testing.T) {
	t.Parallel()
	material := mustCreateTLSMaterial(t, certificateSpec{commonName: "master-report.example.com"})
	dataDir := t.TempDir()
	const dnsTokenCanary = "dns-token-report-canary"
	const zoneTokenCanary = "zone-token-report-canary"
	manager := mustNewManager(t, dataDir, WithCloudflareAPITokens(dnsTokenCanary, zoneTokenCanary))
	t.Cleanup(func() { _ = manager.Close() })
	policy := masterCFDNSPolicy(22, "master-report.example.com")
	bundle := model.ManagedCertificateBundle{
		ID:       policy.ID,
		Domain:   policy.Domain,
		Revision: 4,
		CertPEM:  string(material.CertPEM),
		KeyPEM:   string(material.KeyPEM),
	}

	if err := manager.Apply(context.Background(), []model.ManagedCertificateBundle{bundle}, []model.ManagedCertificatePolicy{policy}); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	reports, err := manager.ManagedCertificateReports(context.Background())
	if err != nil || len(reports) != 1 {
		t.Fatalf("ManagedCertificateReports() = %+v, %v", reports, err)
	}
	report := reports[0]
	if report.ID != policy.ID || report.Domain != policy.Domain || report.Status != "active" {
		t.Fatalf("unexpected report metadata: %+v", report)
	}
	if report.MaterialHash != hashManagedCertificateMaterial(material.CertPEM, material.KeyPEM) {
		t.Fatalf("unexpected material hash: %q", report.MaterialHash)
	}
	payload, err := json.Marshal(reports)
	if err != nil {
		t.Fatalf("marshal reports: %v", err)
	}
	for label, secret := range map[string]string{
		"certificate PEM": string(material.CertPEM),
		"private key":     string(material.KeyPEM),
		"DNS token":       dnsTokenCanary,
		"zone token":      zoneTokenCanary,
	} {
		if strings.Contains(string(payload), secret) {
			t.Fatalf("report JSON contains %s", label)
		}
	}
}

func TestManagedCertificateReportsIgnorePersistedLocalRenewalStateForMasterMaterial(t *testing.T) {
	t.Parallel()
	material := mustCreateTLSMaterial(t, certificateSpec{commonName: "master-transition.example.com"})
	manager := mustNewManager(t, t.TempDir())
	t.Cleanup(func() { _ = manager.Close() })
	policy := masterCFDNSPolicy(25, "master-transition.example.com")
	if err := manager.saveManagedCertificateState(policy.ID, managedCertificateState{
		LocalMetadata: localMaterialMetadata{
			Domain:          policy.Domain,
			Scope:           "domain",
			IssuerMode:      "local_http01",
			CertificateType: "acme",
		},
		ACME: &model.ManagedCertificateACMEState{Renewal: model.ManagedCertificateACMERenewalState{
			LastRenewedAtUnix: 1710000000,
			LastAttemptAtUnix: 1710000300,
			LastAttemptError:  "stale local renewal failure",
			LastAttemptStatus: "error",
		}},
	}); err != nil {
		t.Fatalf("saveManagedCertificateState() error = %v", err)
	}
	bundle := model.ManagedCertificateBundle{
		ID: policy.ID, Domain: policy.Domain, Revision: 2,
		CertPEM: string(material.CertPEM), KeyPEM: string(material.KeyPEM),
	}
	if err := manager.Apply(context.Background(), []model.ManagedCertificateBundle{bundle}, []model.ManagedCertificatePolicy{policy}); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	reporters := []struct {
		name string
		load func() ([]model.ManagedCertificateReport, error)
	}{
		{name: "manager", load: func() ([]model.ManagedCertificateReport, error) {
			return manager.ManagedCertificateReports(context.Background())
		}},
		{name: "active generation", load: func() ([]model.ManagedCertificateReport, error) {
			return manager.managedCertificateReports(context.Background(), manager.activeState())
		}},
	}
	for _, reporter := range reporters {
		reports, err := reporter.load()
		if err != nil || len(reports) != 1 {
			t.Fatalf("%s reports = %+v, %v", reporter.name, reports, err)
		}
		report := reports[0]
		if report.Status != "active" || report.UpdatedAt != "" || report.LastIssueAt != "" || report.LastError != "" {
			t.Fatalf("%s report overlaid stale local renewal state: %+v", reporter.name, report)
		}
	}
}

func TestManagedCertificateReportsRetainMasterCFDNSActiveOnApplyFailureAndRestart(t *testing.T) {
	t.Parallel()
	activeMaterial := mustCreateTLSMaterial(t, certificateSpec{commonName: "master-retained.example.com"})
	pendingMaterial := mustCreateTLSMaterial(t, certificateSpec{commonName: "master-retained.example.com"})
	dataDir := t.TempDir()
	manager := mustNewManager(t, dataDir)
	policy := masterCFDNSPolicy(23, "master-retained.example.com")
	activeBundle := model.ManagedCertificateBundle{
		ID: policy.ID, Domain: policy.Domain, Revision: 1,
		CertPEM: string(activeMaterial.CertPEM), KeyPEM: string(activeMaterial.KeyPEM),
	}
	if err := manager.Apply(context.Background(), []model.ManagedCertificateBundle{activeBundle}, []model.ManagedCertificatePolicy{policy}); err != nil {
		t.Fatalf("initial Apply() error = %v", err)
	}

	pendingBundle := model.ManagedCertificateBundle{
		ID: policy.ID, Domain: policy.Domain, Revision: 2,
		CertPEM: string(pendingMaterial.CertPEM), KeyPEM: string(pendingMaterial.KeyPEM),
	}
	brokenPolicy := model.ManagedCertificatePolicy{
		ID: 24, Domain: "broken-report.example.com", Enabled: true, Scope: "domain",
		IssuerMode: "local_http01", CertificateType: "uploaded", Usage: "https",
	}
	brokenBundle := model.ManagedCertificateBundle{
		ID: brokenPolicy.ID, Domain: brokenPolicy.Domain,
		CertPEM: "not-a-certificate", KeyPEM: string(pendingMaterial.KeyPEM),
	}
	if err := manager.Apply(
		context.Background(),
		[]model.ManagedCertificateBundle{pendingBundle, brokenBundle},
		[]model.ManagedCertificatePolicy{policy, brokenPolicy},
	); err == nil {
		t.Fatal("Apply() succeeded with invalid staged material")
	}
	reports, err := manager.ManagedCertificateReports(context.Background())
	if err != nil || len(reports) != 1 {
		t.Fatalf("retained ManagedCertificateReports() = %+v, %v", reports, err)
	}
	activeHash := hashManagedCertificateMaterial(activeMaterial.CertPEM, activeMaterial.KeyPEM)
	pendingHash := hashManagedCertificateMaterial(pendingMaterial.CertPEM, pendingMaterial.KeyPEM)
	if reports[0].MaterialHash != activeHash || reports[0].MaterialHash == pendingHash {
		t.Fatalf("retained report material hash = %q, want active hash", reports[0].MaterialHash)
	}

	if err := manager.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	restarted := mustNewManager(t, dataDir)
	t.Cleanup(func() { _ = restarted.Close() })
	reports, err = restarted.ManagedCertificateReports(context.Background())
	if err != nil {
		t.Fatalf("restarted ManagedCertificateReports() error = %v", err)
	}
	if len(reports) != 0 {
		t.Fatalf("restarted manager exposed unactivated reports: %+v", reports)
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

func TestACMEFlowACMEIssuerRespectsContextCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := acmeflowACMEIssuer{}.Issue(ctx, acmeIssueRequest{})
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

func TestIntegrationManagerApplyInternalCALifecycle(t *testing.T) {
	requireCertificateLifecycle(t)
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

	changedPolicy := policy
	changedPolicy.Domain = "internal-ca-next.example.com"
	if err := recreated.Apply(context.Background(), nil, []model.ManagedCertificatePolicy{changedPolicy}); err != nil {
		t.Fatalf("domain-change apply failed: %v", err)
	}
	changedInfo, err := recreated.CertificateInfo(31)
	if err != nil {
		t.Fatalf("domain-change certificate info failed: %v", err)
	}
	if changedInfo.Fingerprint == infoAfter.Fingerprint {
		t.Fatal("expected internal_ca material to regenerate when policy domain changes")
	}

	if err := os.WriteFile(persistedCert, []byte("broken"), 0600); err != nil {
		t.Fatalf("corrupt cert write failed: %v", err)
	}
	if err := recreated.Apply(context.Background(), nil, []model.ManagedCertificatePolicy{changedPolicy}); err != nil {
		t.Fatalf("recovery apply failed: %v", err)
	}
	recoveredInfo, err := recreated.CertificateInfo(31)
	if err != nil {
		t.Fatalf("recovered certificate info failed: %v", err)
	}
	if recoveredInfo.Fingerprint == changedInfo.Fingerprint {
		t.Fatal("expected corrupt internal_ca material to be regenerated")
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

func TestIntegrationManagerApplySelectsACMEChallengeBindingsAndProfiles(t *testing.T) {
	requireCertificateLifecycle(t)
	t.Parallel()

	now := time.Date(2026, 4, 13, 12, 0, 0, 0, time.UTC)
	issued := mustCreateTLSMaterial(t, certificateSpec{
		commonName: "acme-v6.example.com",
		notBefore:  now.Add(-time.Hour),
		notAfter:   now.Add(90 * 24 * time.Hour),
	})
	ipIssued := mustCreateTLSMaterial(t, certificateSpec{
		commonName: "203.0.113.9",
		notBefore:  now.Add(-time.Hour),
		notAfter:   now.Add(6 * 24 * time.Hour),
	})
	dnsIssued := mustCreateTLSMaterial(t, certificateSpec{
		commonName: "acme-dns.example.com",
		notBefore:  now.Add(-time.Hour),
		notAfter:   now.Add(90 * 24 * time.Hour),
	})
	fake := &fakeACMEIssuer{results: []acmeIssueResult{
		{CertPEM: issued.CertPEM, KeyPEM: issued.KeyPEM},
		{CertPEM: ipIssued.CertPEM, KeyPEM: ipIssued.KeyPEM},
		{CertPEM: dnsIssued.CertPEM, KeyPEM: dnsIssued.KeyPEM},
	}}
	manager := mustNewManager(
		t,
		t.TempDir(),
		withNow(func() time.Time { return now }),
		WithACMEHTTP01Address("::1", "8080"),
		WithNodeRole("master"),
		WithLocalAgent(true),
		WithCloudflareAPITokens("dns-token", ""),
		withACMEIssuerFactory(func(request acmeIssueRequest) (acmeIssuer, error) {
			return fake, nil
		}),
	)

	policies := []model.ManagedCertificatePolicy{
		{
			ID:              52,
			Domain:          "acme-v6.example.com",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "local_http01",
			CertificateType: "acme",
			Usage:           "https",
		},
		{
			ID:              5101,
			Domain:          "203.0.113.9",
			Enabled:         true,
			Scope:           "ip",
			IssuerMode:      "local_http01",
			CertificateType: "acme",
			Usage:           "https",
		},
		{
			ID:              5203,
			Domain:          "acme-dns.example.com",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "master_cf_dns",
			CertificateType: "acme",
			Usage:           "https",
		},
	}
	err := manager.Apply(context.Background(), nil, policies)
	if err != nil {
		t.Fatalf("apply failed: %v", err)
	}

	if len(fake.requests) != 3 {
		t.Fatalf("expected three acme issuance requests, got %d", len(fake.requests))
	}
	if fake.requests[0].ChallengeType != challengeTypeHTTP01 {
		t.Fatalf("unexpected challenge type: %q", fake.requests[0].ChallengeType)
	}
	if fake.requests[0].IssuerMode != "local_http01" {
		t.Fatalf("unexpected issuer mode: %q", fake.requests[0].IssuerMode)
	}
	if fake.requests[0].HTTP01Interface != "::1" {
		t.Fatalf("unexpected http-01 interface: %q", fake.requests[0].HTTP01Interface)
	}
	if fake.requests[0].HTTP01Port != "8080" {
		t.Fatalf("unexpected http-01 port: %q", fake.requests[0].HTTP01Port)
	}
	if fake.requests[0].Scope != "domain" {
		t.Fatalf("unexpected scope: %q", fake.requests[0].Scope)
	}
	if fake.requests[0].Profile != "" {
		t.Fatalf("unexpected profile for domain certificate: %q", fake.requests[0].Profile)
	}

	info, err := manager.CertificateInfo(52)
	if err != nil {
		t.Fatalf("certificate info failed: %v", err)
	}
	if info.Fingerprint != issued.Fingerprint {
		t.Fatalf("unexpected fingerprint: got %q want %q", info.Fingerprint, issued.Fingerprint)
	}

	ipRequest := fake.requests[1]
	if ipRequest.CertificateID != 5101 || ipRequest.ChallengeType != challengeTypeHTTP01 || ipRequest.Scope != "ip" || ipRequest.Profile != "shortlived" {
		t.Fatalf("unexpected IP issuance request: %+v", ipRequest)
	}
	ipInfo, err := manager.CertificateInfo(5101)
	if err != nil {
		t.Fatalf("IP certificate info failed: %v", err)
	}
	if ipInfo.Fingerprint != ipIssued.Fingerprint {
		t.Fatalf("unexpected IP fingerprint: got %q want %q", ipInfo.Fingerprint, ipIssued.Fingerprint)
	}

	dnsRequest := fake.requests[2]
	if dnsRequest.CertificateID != 5203 || dnsRequest.ChallengeType != challengeTypeDNS01Cloudflare {
		t.Fatalf("unexpected DNS issuance request: %+v", dnsRequest)
	}
	if dnsRequest.CloudflareDNSAPIToken != "dns-token" || dnsRequest.CloudflareZoneAPIToken != "dns-token" {
		t.Fatalf("unexpected cloudflare tokens: dns=%q zone=%q", dnsRequest.CloudflareDNSAPIToken, dnsRequest.CloudflareZoneAPIToken)
	}

	if err := manager.Apply(context.Background(), nil, policies); err != nil {
		t.Fatalf("fresh-material reuse apply failed: %v", err)
	}
	if len(fake.requests) != 3 {
		t.Fatalf("expected fresh ACME material to avoid reissuance, got %d requests", len(fake.requests))
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

func TestIntegrationManagerApplyPersistsACMEAccountStateAfterIssuanceFailure(t *testing.T) {
	requireCertificateLifecycle(t)
	t.Parallel()

	dataDir := t.TempDir()
	accountKey := mustCreateAccountKeyPEM(t)
	accountMetadata := acmeflow.AccountMetadata{
		Version:      acmeflow.AccountMetadataVersion,
		DirectoryURL: acme.LetsEncryptURL,
		URI:          "https://acme-v02.api.letsencrypt.org/acme/acct/9999",
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

	// Failure backoff (R5② / R4): the initial issuance failure records a backoff
	// window, so a subsequent re-attempt inside that window is deferred. Drive both
	// managers with a controllable clock so the recreated manager observes a `now`
	// past the persistent-class backoff window and the re-issuance can proceed,
	// exercising the account-state reuse this test is about.
	failureAt := time.Now()
	initial := mustNewManager(
		t,
		dataDir,
		withNow(func() time.Time { return failureAt }),
		withACMEIssuerFactory(func(request acmeIssueRequest) (acmeIssuer, error) {
			return partialStateACMEIssuer{
				result: acmeIssueResult{
					AccountKeyPEM: accountKey,
					Account:       accountMetadata,
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
		withNow(func() time.Time { return failureAt.Add(2 * time.Hour) }),
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
	if recreatedFake.requests[0].Account.URI != accountMetadata.URI {
		t.Fatalf("expected persisted account metadata, got %+v", recreatedFake.requests[0].Account)
	}
}

// TestManagerApplyRecordsBackoffForNonIssuerFailures pins the fix for non-issuer
// renewal failures: an error raised OUTSIDE issuer.Issue (here, the issuer
// factory itself failing) must still record failure backoff, so the certificate
// is not retried every heartbeat without a backoff window. Before the fix only
// issuer.Issue errors were recorded in loadOrIssueACMEUnlocked; factory, request,
// parse and persist failures returned unrecorded.
func TestIntegrationManagerApplyRecordsBackoffForNonIssuerFailures(t *testing.T) {
	requireCertificateLifecycle(t)
	t.Parallel()

	now := time.Now()
	manager := mustNewManager(
		t,
		t.TempDir(),
		withNow(func() time.Time { return now }),
		withACMEIssuerFactory(func(request acmeIssueRequest) (acmeIssuer, error) {
			return nil, errSyntheticACMEFailure
		}),
	)
	policy := model.ManagedCertificatePolicy{
		ID:              5801,
		Domain:          "factory-error.example.com",
		Enabled:         true,
		Scope:           "domain",
		IssuerMode:      "local_http01",
		CertificateType: "acme",
		Usage:           "https",
	}

	err := manager.Apply(context.Background(), nil, []model.ManagedCertificatePolicy{policy})
	if !errors.Is(err, errSyntheticACMEFailure) {
		t.Fatalf("expected synthetic acme failure from issuer factory, got %v", err)
	}

	state, ok, err := manager.loadManagedCertificateState(5801)
	if err != nil {
		t.Fatalf("load state failed: %v", err)
	}
	if !ok || state.ACME == nil {
		t.Fatalf("expected managed acme state after non-issuer failure, got ok=%v state=%+v", ok, state)
	}
	if got := state.ACME.Renewal.LastAttemptStatus; got != "error" {
		t.Fatalf("expected non-issuer failure to record last attempt status error, got %q", got)
	}
	if got := state.ACME.Renewal.BackoffClass; got == "" {
		t.Fatal("expected non-issuer failure to record a backoff class; pre-fix only issuer.Issue errors were recorded")
	}
	if got := state.ACME.Renewal.BackoffRetryNext; got <= now.Unix() {
		t.Fatalf("expected non-issuer failure to schedule a future retry, got %d (now=%d)", got, now.Unix())
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

func mustCreateAccountKeyPEM(t *testing.T) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate account key: %v", err)
	}
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("marshal account key: %v", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der})
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
