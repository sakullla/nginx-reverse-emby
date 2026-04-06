package certs

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"math/big"
	"testing"
	"time"
)

func TestFingerprintFromPEMReturnsValue(t *testing.T) {
	if _, err := FingerprintFromPEM([]byte("invalid")); err == nil {
		t.Fatal("expected invalid cert pem to fail")
	}
}

func TestFingerprintFromPEMReturnsSHA256OfDER(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "task9-test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
	}

	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create cert: %v", err)
	}

	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
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
