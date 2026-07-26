package acmeflow

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/pem"
	"math/big"
	"net"
	"testing"
	"time"
)

func TestMaterialReusesExistingRSAKey(t *testing.T) {
	existing := mustTestRSAKey(t)
	existingPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(existing)})

	key, keyPEM, reused, err := prepareCertificateKey(existingPEM, rand.Reader)
	if err != nil {
		t.Fatalf("prepareCertificateKey() error = %v", err)
	}
	if !reused {
		t.Fatal("reused = false, want true")
	}
	if string(keyPEM) != string(existingPEM) {
		t.Fatal("existing private-key PEM was not preserved")
	}
	if key.Public().(*rsa.PublicKey).N.Cmp(existing.PublicKey.N) != 0 {
		t.Fatal("returned signer does not reuse the existing RSA key")
	}
}

func TestMaterialGeneratesRSA2048WhenKeyMissing(t *testing.T) {
	key, keyPEM, reused, err := prepareCertificateKey(nil, rand.Reader)
	if err != nil {
		t.Fatalf("prepareCertificateKey() error = %v", err)
	}
	if reused {
		t.Fatal("reused = true, want false")
	}
	rsaKey, ok := key.(*rsa.PrivateKey)
	if !ok {
		t.Fatalf("key type = %T, want *rsa.PrivateKey", key)
	}
	if rsaKey.N.BitLen() != 2048 {
		t.Fatalf("RSA key bits = %d, want 2048", rsaKey.N.BitLen())
	}
	parsed, err := parsePrivateKeyPEM(keyPEM)
	if err != nil {
		t.Fatalf("parse generated key: %v", err)
	}
	if parsed.Public().(*rsa.PublicKey).N.Cmp(rsaKey.PublicKey.N) != 0 {
		t.Fatal("encoded key differs from generated signer")
	}
}

func TestMaterialValidatesKeyIdentifiersValidityAndProfile(t *testing.T) {
	now := time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)
	key := mustTestRSAKey(t)
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	certPEM := issueTestCertificate(t, key, []string{"example.com"}, []net.IP{net.ParseIP("192.0.2.20")}, "example.com", now.Add(-time.Minute), now.Add(24*time.Hour))

	leaf, err := ValidateMaterial(CertificateMaterial{
		CertificatePEM: certPEM,
		PrivateKeyPEM:  keyPEM,
		Profile:        "shortlived",
	}, MaterialPolicy{
		Identifiers: []Identifier{
			{Type: IdentifierDNS, Value: "example.com"},
			{Type: IdentifierIP, Value: "192.0.2.20"},
		},
		Profile: "shortlived",
		Now:     now,
	})
	if err != nil {
		t.Fatalf("ValidateMaterial() error = %v", err)
	}
	if leaf.Subject.CommonName != "example.com" {
		t.Fatalf("leaf CommonName = %q", leaf.Subject.CommonName)
	}
}

func TestMaterialRejectsMismatchedKeyIdentifierAndProfile(t *testing.T) {
	now := time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)
	certKey := mustTestRSAKey(t)
	otherKey := mustOtherTestRSAKey(t)
	certPEM := issueTestCertificate(t, certKey, []string{"example.com"}, nil, "example.com", now.Add(-time.Minute), now.Add(time.Hour))
	otherKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(otherKey)})

	tests := []struct {
		name     string
		material CertificateMaterial
		policy   MaterialPolicy
	}{
		{
			name:     "key",
			material: CertificateMaterial{CertificatePEM: certPEM, PrivateKeyPEM: otherKeyPEM},
			policy:   MaterialPolicy{Identifiers: []Identifier{{Type: IdentifierDNS, Value: "example.com"}}, Now: now},
		},
		{
			name:     "identifier",
			material: CertificateMaterial{CertificatePEM: certPEM, PrivateKeyPEM: pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(certKey)})},
			policy:   MaterialPolicy{Identifiers: []Identifier{{Type: IdentifierDNS, Value: "other.example.com"}}, Now: now},
		},
		{
			name:     "profile",
			material: CertificateMaterial{CertificatePEM: certPEM, PrivateKeyPEM: pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(certKey)}), Profile: "default"},
			policy:   MaterialPolicy{Identifiers: []Identifier{{Type: IdentifierDNS, Value: "example.com"}}, Profile: "shortlived", Now: now},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := ValidateMaterial(test.material, test.policy)
			if got := ErrorCategoryOf(err); got != CategoryMaterial {
				t.Fatalf("error category = %q, want %q (err=%v)", got, CategoryMaterial, err)
			}
		})
	}
}

func TestMaterialRejectsNonServerUsageAndUnhandledCriticalExtensions(t *testing.T) {
	now := time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)
	key := mustTestRSAKey(t)
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})

	tests := []struct {
		name   string
		mutate func(*x509.Certificate)
	}{
		{
			name: "client-auth-only",
			mutate: func(template *x509.Certificate) {
				template.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
			},
		},
		{
			name: "unhandled-critical-extension",
			mutate: func(template *x509.Certificate) {
				template.ExtraExtensions = []pkix.Extension{{
					Id:       asn1.ObjectIdentifier{1, 2, 3, 4, 5, 6, 7},
					Critical: true,
					Value:    []byte{0x05, 0x00},
				}}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			certPEM := issueTestCertificateWithMutator(
				t,
				key,
				[]string{"example.com"},
				nil,
				"example.com",
				now.Add(-time.Minute),
				now.Add(time.Hour),
				test.mutate,
			)
			_, err := ValidateMaterial(CertificateMaterial{
				CertificatePEM: certPEM,
				PrivateKeyPEM:  keyPEM,
			}, MaterialPolicy{
				Identifiers: []Identifier{{Type: IdentifierDNS, Value: "example.com"}},
				Now:         now,
			})
			if got := ErrorCategoryOf(err); got != CategoryMaterial {
				t.Fatalf("error category = %q, want %q (err=%v)", got, CategoryMaterial, err)
			}
		})
	}
}

func issueTestCertificate(t *testing.T, leafKey *rsa.PrivateKey, dnsNames []string, ipAddresses []net.IP, commonName string, notBefore, notAfter time.Time) []byte {
	return issueTestCertificateWithMutator(t, leafKey, dnsNames, ipAddresses, commonName, notBefore, notAfter, nil)
}

func issueTestCertificateWithMutator(t *testing.T, leafKey *rsa.PrivateKey, dnsNames []string, ipAddresses []net.IP, commonName string, notBefore, notAfter time.Time, mutate func(*x509.Certificate)) []byte {
	t.Helper()
	caKey := mustOtherTestRSAKey(t)
	caTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Test CA"},
		NotBefore:             notBefore.Add(-time.Hour),
		NotAfter:              notAfter.Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	ca, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatal(err)
	}
	leafTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: commonName},
		DNSNames:     dnsNames,
		IPAddresses:  ipAddresses,
		NotBefore:    notBefore,
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	if mutate != nil {
		mutate(leafTemplate)
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTemplate, ca, &leafKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	return append(
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER}),
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})...,
	)
}
