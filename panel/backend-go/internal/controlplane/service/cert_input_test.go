package service

import "testing"

func TestNormalizeManagedCertificateInputClearsSelfSignedForACME(t *testing.T) {
	domain := "192.0.2.1"
	scope := "ip"
	issuerMode := "local_http01"
	certificateType := "acme"
	selfSigned := true

	cert, err := normalizeManagedCertificateInput(ManagedCertificateInput{
		Domain:          &domain,
		Scope:           &scope,
		IssuerMode:      &issuerMode,
		CertificateType: &certificateType,
		SelfSigned:      &selfSigned,
	}, ManagedCertificate{}, 1, "local", false)
	if err != nil {
		t.Fatalf("normalize ACME IP certificate: %v", err)
	}
	if cert.SelfSigned {
		t.Fatal("ACME certificate retained conflicting self_signed=true")
	}
}
