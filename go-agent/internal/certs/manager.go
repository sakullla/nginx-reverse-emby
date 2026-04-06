package certs

import (
	"bytes"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"fmt"
)

func FingerprintFromPEM(certPEM []byte) (string, error) {
	block, rest := pem.Decode(certPEM)
	if block == nil {
		return "", fmt.Errorf("invalid certificate PEM")
	}
	if block.Type != "CERTIFICATE" {
		return "", fmt.Errorf("expected CERTIFICATE PEM block")
	}
	if len(bytes.TrimSpace(rest)) > 0 {
		return "", fmt.Errorf("certificate PEM must contain exactly one block")
	}

	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return "", err
	}

	sum := sha256.Sum256(cert.Raw)
	return hex.EncodeToString(sum[:]), nil
}
