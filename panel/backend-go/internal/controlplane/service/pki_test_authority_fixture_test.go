//go:build !integration

package service

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"math/big"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

func newPKIBackupAuthority(t *testing.T, now time.Time) (*ecdsa.PrivateKey, storage.PKIAuthorityRow) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey(authority) error = %v", err)
	}
	publicDER, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatalf("MarshalPKIXPublicKey(authority) error = %v", err)
	}
	subjectKeyID := sha256.Sum256(publicDER)
	template := &x509.Certificate{
		SerialNumber: new(big.Int).SetBytes(append([]byte{0x80}, make([]byte, 15)...)),
		Subject:      pkix.Name{CommonName: "NRE backup test CA"}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true, IsCA: true, SubjectKeyId: append([]byte(nil), subjectKeyID[:20]...), SignatureAlgorithm: x509.ECDSAWithSHA256,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("CreateCertificate(authority) error = %v", err)
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("ParseCertificate(authority) error = %v", err)
	}
	fingerprint := sha256.Sum256(certificate.Raw)
	keyRef := "ca-1-test.vault"
	return key, storage.PKIAuthorityRow{
		ID: "authority-1", PKIDomainID: "domain-1", Generation: 1, Status: "active",
		CertificatePEM: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})), EncryptedKeyRef: &keyRef,
		FingerprintSHA256: hex.EncodeToString(fingerprint[:]), NotBefore: certificate.NotBefore, NotAfter: certificate.NotAfter,
		CreatedReason: "bootstrap", CreatedAt: now, UpdatedAt: now,
	}
}
