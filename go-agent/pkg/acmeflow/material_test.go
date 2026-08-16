package acmeflow

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"testing"
	"time"
)

func TestMaterialValidationContract(t *testing.T) {
	now := time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)
	certKey := mustTestRSAKey(t)
	otherKey := mustOtherTestRSAKey(t)
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(certKey)})
	otherKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(otherKey)})
	certPEM := issueTestCertificate(t, certKey, []string{"example.com"}, []net.IP{net.ParseIP("192.0.2.20")}, "example.com", now.Add(-time.Minute), now.Add(time.Hour))

	baseMaterial := CertificateMaterial{CertificatePEM: certPEM, PrivateKeyPEM: keyPEM, Profile: "shortlived"}
	basePolicy := MaterialPolicy{
		Identifiers: []Identifier{{Type: IdentifierDNS, Value: "example.com"}, {Type: IdentifierIP, Value: "192.0.2.20"}},
		Profile:     "shortlived",
		Now:         now,
	}
	for _, test := range []struct {
		name     string
		material CertificateMaterial
		policy   MaterialPolicy
		wantErr  bool
	}{
		{name: "valid", material: baseMaterial, policy: basePolicy},
		{name: "key mismatch", material: CertificateMaterial{CertificatePEM: certPEM, PrivateKeyPEM: otherKeyPEM, Profile: "shortlived"}, policy: basePolicy, wantErr: true},
		{name: "identifier mismatch", material: baseMaterial, policy: MaterialPolicy{Identifiers: []Identifier{{Type: IdentifierDNS, Value: "other.example.com"}}, Profile: "shortlived", Now: now}, wantErr: true},
		{name: "profile mismatch", material: CertificateMaterial{CertificatePEM: certPEM, PrivateKeyPEM: keyPEM, Profile: "default"}, policy: basePolicy, wantErr: true},
		{name: "expired", material: baseMaterial, policy: MaterialPolicy{Identifiers: basePolicy.Identifiers, Profile: "shortlived", Now: now.Add(2 * time.Hour)}, wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			leaf, err := ValidateMaterial(test.material, test.policy)
			if test.wantErr {
				if ErrorCategoryOf(err) != CategoryMaterial {
					t.Fatalf("ValidateMaterial() error = %v, category = %q", err, ErrorCategoryOf(err))
				}
				return
			}
			if err != nil || leaf.Subject.CommonName != "example.com" {
				t.Fatalf("ValidateMaterial() leaf = %#v, error = %v", leaf, err)
			}
		})
	}
}

func issueTestCertificate(t *testing.T, leafKey *rsa.PrivateKey, dnsNames []string, ipAddresses []net.IP, commonName string, notBefore, notAfter time.Time) []byte {
	t.Helper()
	caKey := mustOtherTestRSAKey(t)
	caTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Test CA"},
		NotBefore: notBefore.Add(-time.Hour), NotAfter: notAfter.Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
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
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: commonName},
		DNSNames: dnsNames, IPAddresses: ipAddresses,
		NotBefore: notBefore, NotAfter: notAfter,
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
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
