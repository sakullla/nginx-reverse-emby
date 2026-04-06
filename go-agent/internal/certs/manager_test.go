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
	"encoding/pem"
	"math/big"
	"path/filepath"
	"testing"
	"time"

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

type certificateSpec struct {
	commonName string
	isCA       bool
}

type tlsMaterial struct {
	CertPEM     []byte
	KeyPEM      []byte
	Fingerprint string
	Leaf        *x509.Certificate
}

func mustNewManager(t *testing.T, dataDir string) *Manager {
	t.Helper()

	manager, err := NewManager(dataDir)
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
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
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
