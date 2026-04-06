package certs

import (
	"crypto/x509"
	"encoding/pem"
	"fmt"
)

func FingerprintFromPEM(certPEM []byte) (string, error) {
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return "", fmt.Errorf("invalid pem")
	}

	if _, err := x509.ParseCertificate(block.Bytes); err != nil {
		return "", err
	}

	return "ok", nil
}
