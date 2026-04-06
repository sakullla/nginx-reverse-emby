package certs

import "testing"

func TestFingerprintFromPEMReturnsValue(t *testing.T) {
	if _, err := FingerprintFromPEM([]byte("invalid")); err == nil {
		t.Fatal("expected invalid cert pem to fail")
	}
}
