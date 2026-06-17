package relay

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"fmt"
	"net"
	"strings"
)

func serverTLSConfig(ctx context.Context, provider TLSMaterialProvider, listener Listener) (*tls.Config, error) {
	if provider == nil {
		return nil, fmt.Errorf("tls material provider is required")
	}
	if listener.CertificateID == nil {
		return nil, fmt.Errorf("certificate_id is required")
	}

	cert, err := provider.ServerCertificate(ctx, *listener.CertificateID)
	if err != nil {
		return nil, err
	}
	if cert == nil {
		return nil, fmt.Errorf("server certificate %d not found", *listener.CertificateID)
	}

	return &tls.Config{
		Certificates:                []tls.Certificate{*cert},
		MinVersion:                  tls.VersionTLS12,
		DynamicRecordSizingDisabled: true,
	}, nil
}

func clientTLSConfig(ctx context.Context, provider TLSMaterialProvider, listener Listener, address, serverNameOverride string) (*tls.Config, error) {
	if provider == nil {
		return nil, fmt.Errorf("tls material provider is required")
	}

	mode, err := normalizeTLSMode(listener.TLSMode)
	if err != nil {
		return nil, err
	}

	rootCAs, err := provider.TrustedCAPool(ctx, listener.TrustedCACertificateIDs)
	if err != nil {
		return nil, err
	}

	serverName, err := verificationServerName(address, serverNameOverride)
	if err != nil {
		return nil, err
	}

	return &tls.Config{
		// codeql[go/disabled-certificate-check]
		// Relay TLS is verified by pin/CA policy in VerifyConnection.
		InsecureSkipVerify:          true,
		MinVersion:                  tls.VersionTLS12,
		ServerName:                  serverName,
		DynamicRecordSizingDisabled: true,
		VerifyConnection: func(state tls.ConnectionState) error {
			peerCertificates := state.PeerCertificates
			pinErr := verifyPinSet(listener, peerCertificates)
			caErr := verifyCertificateAuthority(listener, rootCAs, serverName, peerCertificates)

			switch mode {
			case tlsModePinOnly:
				return pinErr
			case tlsModeCAOnly:
				return caErr
			case tlsModePinOrCA:
				if pinErr == nil || caErr == nil {
					return nil
				}
				return fmt.Errorf("pin_or_ca verification failed: pin=%v ca=%v", pinErr, caErr)
			case tlsModePinAndCA:
				if pinErr != nil {
					return pinErr
				}
				if caErr == nil {
					return nil
				}
				if net.ParseIP(serverName) != nil && leafCertificateLacksIPSAN(peerCertificates) {
					return verifyCertificateAuthorityChain(listener, rootCAs, peerCertificates)
				}
				return caErr
			default:
				return fmt.Errorf("unsupported tls_mode")
			}
		},
	}, nil
}

func verificationServerName(address, override string) (string, error) {
	if trimmed := strings.TrimSpace(override); trimmed != "" {
		return trimmed, nil
	}

	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return "", fmt.Errorf("invalid relay address %q: %w", address, err)
	}
	return host, nil
}

func leafCertificateLacksIPSAN(peerCertificates []*x509.Certificate) bool {
	if len(peerCertificates) == 0 || peerCertificates[0] == nil {
		return false
	}
	return len(peerCertificates[0].IPAddresses) == 0
}

func verifyPinSet(listener Listener, peerCertificates []*x509.Certificate) error {
	if len(listener.PinSet) == 0 {
		return fmt.Errorf("pin_set is required")
	}
	if len(peerCertificates) == 0 {
		return fmt.Errorf("peer certificate is required")
	}

	leaf := peerCertificates[0]
	sum := sha256.Sum256(leaf.RawSubjectPublicKeyInfo)
	got := base64.StdEncoding.EncodeToString(sum[:])
	for _, pin := range listener.PinSet {
		if !isSupportedPinType(pin.Type) {
			continue
		}
		if strings.TrimSpace(pin.Value) == got {
			return nil
		}
	}
	return fmt.Errorf("pin verification failed")
}

func verifyCertificateAuthorityChain(listener Listener, rootCAs *x509.CertPool, peerCertificates []*x509.Certificate) error {
	return verifyCertificateAuthorityWithDNSName(listener, rootCAs, "", peerCertificates)
}

func verifyCertificateAuthority(listener Listener, rootCAs *x509.CertPool, serverName string, peerCertificates []*x509.Certificate) error {
	return verifyCertificateAuthorityWithDNSName(listener, rootCAs, serverName, peerCertificates)
}

func verifyCertificateAuthorityWithDNSName(listener Listener, rootCAs *x509.CertPool, serverName string, peerCertificates []*x509.Certificate) error {
	if len(listener.TrustedCACertificateIDs) == 0 {
		return fmt.Errorf("trusted_ca_certificate_ids is required")
	}
	if rootCAs == nil {
		return fmt.Errorf("trusted CA pool is empty")
	}
	if len(peerCertificates) == 0 {
		return fmt.Errorf("peer certificate is required")
	}

	leaf := peerCertificates[0]
	if isSelfSigned(leaf) && !listener.AllowSelfSigned {
		return fmt.Errorf("self-signed certificate is not allowed")
	}

	intermediates := x509.NewCertPool()
	for _, cert := range peerCertificates[1:] {
		intermediates.AddCert(cert)
	}

	_, err := leaf.Verify(x509.VerifyOptions{
		DNSName:       serverName,
		Roots:         rootCAs,
		Intermediates: intermediates,
		KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	})
	if err != nil {
		return fmt.Errorf("ca verification failed: %w", err)
	}
	return nil
}

func isSelfSigned(cert *x509.Certificate) bool {
	if cert == nil {
		return false
	}
	return cert.CheckSignatureFrom(cert) == nil
}
