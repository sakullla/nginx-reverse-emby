package acmeflow

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"io"
	"net"
	"strings"
	"time"
)

type CertificateMaterial struct {
	CertificatePEM []byte
	PrivateKeyPEM  []byte
	Profile        string
}

type MaterialPolicy struct {
	Identifiers  []Identifier
	Profile      string
	Now          time.Time
	MaxClockSkew time.Duration
}

func prepareCertificateKey(existingPEM []byte, random io.Reader) (crypto.Signer, []byte, bool, error) {
	if len(existingPEM) != 0 {
		key, err := parsePrivateKeyPEM(existingPEM)
		if err != nil {
			return nil, nil, false, err
		}
		return key, append([]byte(nil), existingPEM...), true, nil
	}
	if random == nil {
		random = rand.Reader
	}
	key, err := rsa.GenerateKey(random, 2048)
	if err != nil {
		return nil, nil, false, err
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	})
	return key, keyPEM, false, nil
}

func parsePrivateKeyPEM(keyPEM []byte) (crypto.Signer, error) {
	block, rest := pem.Decode(keyPEM)
	if block == nil || len(bytes.TrimSpace(rest)) != 0 || len(block.Headers) != 0 {
		return nil, errors.New("invalid private-key PEM")
	}
	var key any
	var err error
	switch block.Type {
	case "RSA PRIVATE KEY":
		key, err = x509.ParsePKCS1PrivateKey(block.Bytes)
	case "EC PRIVATE KEY":
		key, err = x509.ParseECPrivateKey(block.Bytes)
	case "PRIVATE KEY":
		key, err = x509.ParsePKCS8PrivateKey(block.Bytes)
	default:
		return nil, errors.New("unsupported private-key PEM type")
	}
	if err != nil {
		return nil, errors.New("invalid private key")
	}
	signer, ok := key.(crypto.Signer)
	if !ok {
		return nil, errors.New("private key is not a signer")
	}
	switch publicKey := signer.Public().(type) {
	case *rsa.PublicKey:
		if publicKey.N == nil || publicKey.N.BitLen() < 2048 {
			return nil, errors.New("RSA private key is smaller than 2048 bits")
		}
	case *ecdsa.PublicKey:
		if publicKey.Curve == nil || publicKey.Curve.Params().BitSize < 256 {
			return nil, errors.New("ECDSA private key is too small")
		}
	default:
		return nil, errors.New("unsupported private-key algorithm")
	}
	return signer, nil
}

// ValidateMaterial verifies the leaf/chain, key pair, exact SAN set, validity,
// and the profile associated with the issuance result before owner persistence.
func ValidateMaterial(material CertificateMaterial, policy MaterialPolicy) (*x509.Certificate, error) {
	chain, err := parseCertificateChain(material.CertificatePEM)
	if err != nil {
		return nil, WrapError(CategoryMaterial, "material_certificate", err)
	}
	key, err := parsePrivateKeyPEM(material.PrivateKeyPEM)
	if err != nil {
		return nil, WrapError(CategoryMaterial, "material_private_key", err)
	}
	if err := validateKeyPair(chain[0], key); err != nil {
		return nil, WrapError(CategoryMaterial, "material_key_pair", err)
	}
	for index := 0; index+1 < len(chain); index++ {
		if err := chain[index].CheckSignatureFrom(chain[index+1]); err != nil {
			return nil, WrapError(CategoryMaterial, "material_chain", err)
		}
	}
	if chain[0].IsCA {
		return nil, WrapError(CategoryMaterial, "material_certificate", errors.New("leaf certificate is a CA"))
	}

	now := policy.Now
	if now.IsZero() {
		now = time.Now()
	}
	maxClockSkew := policy.MaxClockSkew
	if maxClockSkew == 0 {
		maxClockSkew = 5 * time.Minute
	}
	if maxClockSkew < 0 {
		maxClockSkew = 0
	}
	leaf := chain[0]
	if !leaf.NotAfter.After(now) || !leaf.NotAfter.After(leaf.NotBefore) || leaf.NotBefore.After(now.Add(maxClockSkew)) {
		return nil, WrapError(CategoryMaterial, "material_validity", errors.New("certificate validity window is unusable"))
	}

	if strings.TrimSpace(material.Profile) != strings.TrimSpace(policy.Profile) {
		return nil, WrapError(CategoryMaterial, "material_profile", errors.New("certificate profile mismatch"))
	}
	if err := validateCertificateIdentifiers(leaf, policy.Identifiers); err != nil {
		return nil, WrapError(CategoryMaterial, "material_identifiers", err)
	}
	return leaf, nil
}

func parseCertificateChain(certificatePEM []byte) ([]*x509.Certificate, error) {
	remaining := bytes.TrimSpace(certificatePEM)
	var chain []*x509.Certificate
	for len(remaining) > 0 {
		block, rest := pem.Decode(remaining)
		if block == nil || block.Type != "CERTIFICATE" || len(block.Headers) != 0 {
			return nil, errors.New("invalid certificate PEM chain")
		}
		certificate, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, errors.New("invalid certificate")
		}
		chain = append(chain, certificate)
		remaining = bytes.TrimSpace(rest)
	}
	if len(chain) == 0 {
		return nil, errors.New("certificate chain is empty")
	}
	return chain, nil
}

func firstCertificate(certificatePEM []byte) (*x509.Certificate, error) {
	chain, err := parseCertificateChain(certificatePEM)
	if err != nil {
		return nil, err
	}
	return chain[0], nil
}

func validateKeyPair(certificate *x509.Certificate, key crypto.Signer) error {
	certificatePublicKey, err := x509.MarshalPKIXPublicKey(certificate.PublicKey)
	if err != nil {
		return errors.New("invalid certificate public key")
	}
	privatePublicKey, err := x509.MarshalPKIXPublicKey(key.Public())
	if err != nil {
		return errors.New("invalid private-key public key")
	}
	if !bytes.Equal(certificatePublicKey, privatePublicKey) {
		return errors.New("certificate and private key do not match")
	}
	return nil
}

func validateCertificateIdentifiers(certificate *x509.Certificate, expected []Identifier) error {
	normalized, err := normalizeIdentifiers(expected)
	if err != nil {
		return errors.New("invalid expected identifiers")
	}
	expectedDNS := make(map[string]struct{})
	expectedIP := make(map[string]struct{})
	for _, identifier := range normalized {
		switch identifier.Type {
		case IdentifierDNS:
			expectedDNS[identifier.Value] = struct{}{}
		case IdentifierIP:
			expectedIP[net.ParseIP(identifier.Value).String()] = struct{}{}
		}
	}
	actualDNS := make(map[string]struct{}, len(certificate.DNSNames))
	for _, name := range certificate.DNSNames {
		name = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(name), "."))
		actualDNS[name] = struct{}{}
	}
	actualIP := make(map[string]struct{}, len(certificate.IPAddresses))
	for _, ip := range certificate.IPAddresses {
		if ip == nil {
			return errors.New("certificate contains an invalid IP SAN")
		}
		actualIP[ip.String()] = struct{}{}
	}
	if !sameStringSet(expectedDNS, actualDNS) || !sameStringSet(expectedIP, actualIP) {
		return errors.New("certificate SANs do not match requested identifiers")
	}

	commonName := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(certificate.Subject.CommonName), "."))
	if len(expectedDNS) == 0 && commonName != "" {
		return errors.New("IP-only certificate has a Common Name")
	}
	if commonName != "" {
		if _, ok := expectedDNS[commonName]; !ok {
			return errors.New("certificate Common Name is not a requested DNS identifier")
		}
	}
	return nil
}

func sameStringSet(left, right map[string]struct{}) bool {
	if len(left) != len(right) {
		return false
	}
	for value := range left {
		if _, exists := right[value]; !exists {
			return false
		}
	}
	return true
}

func encodeCertificateChain(chain [][]byte) ([]byte, error) {
	if len(chain) == 0 {
		return nil, errors.New("certificate chain is empty")
	}
	var result []byte
	for _, certificateDER := range chain {
		if len(certificateDER) == 0 {
			return nil, errors.New("certificate chain contains an empty certificate")
		}
		if _, err := x509.ParseCertificate(certificateDER); err != nil {
			return nil, errors.New("certificate chain contains an invalid certificate")
		}
		result = append(result, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificateDER})...)
	}
	return result, nil
}
