package service

import (
	"bytes"
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

var (
	ErrPKIEnrollmentRequest              = errors.New("invalid PKI enrollment request")
	errPKIEnrollmentClientRequest        = fmt.Errorf("%w: invalid client request", ErrPKIEnrollmentRequest)
	ErrPKIEnrollmentCSR                  = errors.New("invalid PKI enrollment CSR")
	ErrPKIEnrollmentOwnerMismatch        = errors.New("PKI enrollment owner mismatch")
	ErrPKIEnrollmentPublicKeyReuse       = errors.New("PKI enrollment public key must change")
	ErrPKIEnrollmentAuthorityUnavailable = errors.New("PKI enrollment authority unavailable")
)

type PKIEnrollmentAuthoritySigner interface {
	LoadSigner(context.Context, storage.PKIAuthorityRow) (crypto.Signer, error)
}

type PKIVaultKeyReader interface {
	OpenCAKey(reference, pkiDomainID string, generation int64, purpose string) ([]byte, error)
}

// PKIVaultAuthoritySigner is the production adapter between enrollment and the
// encrypted CA-key vault. Enrollment only receives a crypto.Signer and never a
// persisted plaintext private-key field.
type PKIVaultAuthoritySigner struct {
	vault PKIVaultKeyReader
}

func NewPKIVaultAuthoritySigner(vault PKIVaultKeyReader) (*PKIVaultAuthoritySigner, error) {
	if vault == nil {
		return nil, fmt.Errorf("%w: PKI vault is required", ErrPKIEnrollmentAuthorityUnavailable)
	}
	return &PKIVaultAuthoritySigner{vault: vault}, nil
}

func (s *PKIVaultAuthoritySigner) LoadSigner(ctx context.Context, authority storage.PKIAuthorityRow) (crypto.Signer, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if authority.EncryptedKeyRef == nil || strings.TrimSpace(*authority.EncryptedKeyRef) == "" {
		return nil, fmt.Errorf("%w: authority has no live key reference", ErrPKIEnrollmentAuthorityUnavailable)
	}
	plaintext, err := s.vault.OpenCAKey(strings.TrimSpace(*authority.EncryptedKeyRef), authority.PKIDomainID, authority.Generation, "ca-signing")
	if err != nil {
		return nil, fmt.Errorf("%w: open authority key: %v", ErrPKIEnrollmentAuthorityUnavailable, err)
	}
	defer func() {
		for index := range plaintext {
			plaintext[index] = 0
		}
	}()
	signer, err := parsePKIAuthorityPrivateKey(plaintext)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrPKIEnrollmentAuthorityUnavailable, err)
	}
	return signer, nil
}

type pkiIdentityBinding struct {
	DomainID    string
	Kind        string
	AgentID     string
	ListenerID  string
	Purpose     string
	URI         *url.URL
	DNSNames    []string
	IPAddresses []net.IP
}

type parsedPKIEnrollmentCSR struct {
	request              *x509.CertificateRequest
	publicKeyFingerprint string
}

func parsePKIEnrollmentCSR(value string) (parsedPKIEnrollmentCSR, error) {
	encoded := bytes.TrimSpace([]byte(value))
	if !bytes.HasPrefix(encoded, []byte("-----BEGIN CERTIFICATE REQUEST-----")) {
		return parsedPKIEnrollmentCSR{}, fmt.Errorf("%w: PEM must begin with a certificate request", ErrPKIEnrollmentCSR)
	}
	block, rest := pem.Decode(encoded)
	if block == nil || block.Type != "CERTIFICATE REQUEST" || len(block.Headers) != 0 || len(bytes.TrimSpace(rest)) != 0 {
		return parsedPKIEnrollmentCSR{}, fmt.Errorf("%w: PEM must contain exactly one certificate request", ErrPKIEnrollmentCSR)
	}
	request, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil {
		return parsedPKIEnrollmentCSR{}, fmt.Errorf("%w: malformed certificate request", ErrPKIEnrollmentCSR)
	}
	if err := request.CheckSignature(); err != nil {
		return parsedPKIEnrollmentCSR{}, fmt.Errorf("%w: invalid request signature", ErrPKIEnrollmentCSR)
	}
	publicKey, ok := request.PublicKey.(*ecdsa.PublicKey)
	if !ok || publicKey.Curve == nil || publicKey.Curve.Params().Name != elliptic.P256().Params().Name {
		return parsedPKIEnrollmentCSR{}, fmt.Errorf("%w: public key must use ECDSA P-256", ErrPKIEnrollmentCSR)
	}
	if request.SignatureAlgorithm != x509.ECDSAWithSHA256 {
		return parsedPKIEnrollmentCSR{}, fmt.Errorf("%w: request signature must use ECDSA with SHA-256", ErrPKIEnrollmentCSR)
	}
	fingerprint := sha256.Sum256(request.RawSubjectPublicKeyInfo)
	return parsedPKIEnrollmentCSR{
		request:              request,
		publicKeyFingerprint: hex.EncodeToString(fingerprint[:]),
	}, nil
}

func newPKIIdentityBinding(domainID, kind, agentID, listenerID, purpose string, dnsNames, ipAddresses []string) (pkiIdentityBinding, error) {
	domainID = strings.TrimSpace(domainID)
	kind = strings.TrimSpace(kind)
	agentID = strings.TrimSpace(agentID)
	listenerID = strings.TrimSpace(listenerID)
	purpose = strings.TrimSpace(purpose)
	if err := validatePKIURISegment("PKI domain", domainID); err != nil {
		return pkiIdentityBinding{}, err
	}
	if err := validatePKIURISegment("agent", agentID); err != nil {
		return pkiIdentityBinding{}, err
	}
	var path string
	switch kind {
	case storage.PKIIdentityKindAgent:
		if listenerID != "" || purpose != storage.PKICertificatePurposeClient || len(dnsNames) != 0 || len(ipAddresses) != 0 {
			return pkiIdentityBinding{}, fmt.Errorf("%w: agent identity requires client purpose and no listener server names", ErrPKIEnrollmentRequest)
		}
		path = "/agent/" + agentID
	case storage.PKIIdentityKindListener:
		if err := validatePKIURISegment("listener", listenerID); err != nil {
			return pkiIdentityBinding{}, err
		}
		if purpose != storage.PKICertificatePurposeServer {
			return pkiIdentityBinding{}, fmt.Errorf("%w: listener identity requires server purpose", ErrPKIEnrollmentRequest)
		}
		path = "/agent/" + agentID + "/listener/" + listenerID
	default:
		return pkiIdentityBinding{}, fmt.Errorf("%w: identity kind must be agent or listener", ErrPKIEnrollmentRequest)
	}
	normalizedDNS, err := normalizePKIDNSNames(dnsNames)
	if err != nil {
		return pkiIdentityBinding{}, err
	}
	normalizedIPs, err := normalizePKIIPAddresses(ipAddresses)
	if err != nil {
		return pkiIdentityBinding{}, err
	}
	identityURI := &url.URL{Scheme: "spiffe", Host: domainID, Path: path}
	return pkiIdentityBinding{
		DomainID: domainID, Kind: kind, AgentID: agentID, ListenerID: listenerID, Purpose: purpose,
		URI: identityURI, DNSNames: normalizedDNS, IPAddresses: normalizedIPs,
	}, nil
}

func validatePKIEnrollmentCSRBinding(csr parsedPKIEnrollmentCSR, binding pkiIdentityBinding, anonymousNewAgent bool) error {
	if csr.request == nil {
		return fmt.Errorf("%w: request is missing", ErrPKIEnrollmentCSR)
	}
	if anonymousNewAgent {
		if !pkixNameMatchesCommonName(csr.request.Subject, "") || len(csr.request.Extensions) != 0 || len(csr.request.DNSNames) != 0 || len(csr.request.EmailAddresses) != 0 || len(csr.request.IPAddresses) != 0 || len(csr.request.URIs) != 0 {
			return fmt.Errorf("%w: new-agent CSR subject must be empty and server-bound", ErrPKIEnrollmentOwnerMismatch)
		}
		return nil
	}
	expectedURI := binding.URI.String()
	if !pkixNameMatchesCommonName(csr.request.Subject, expectedURI) {
		return fmt.Errorf("%w: CSR common name does not match token owner", ErrPKIEnrollmentOwnerMismatch)
	}
	if len(csr.request.URIs) != 1 || csr.request.URIs[0] == nil || csr.request.URIs[0].String() != expectedURI {
		return fmt.Errorf("%w: CSR URI identity does not match token owner", ErrPKIEnrollmentOwnerMismatch)
	}
	if len(csr.request.EmailAddresses) != 0 || !equalPKIStrings(csr.request.DNSNames, binding.DNSNames) || !equalPKIIPs(csr.request.IPAddresses, binding.IPAddresses) {
		return fmt.Errorf("%w: CSR subject alternative names do not match the identity owner", ErrPKIEnrollmentOwnerMismatch)
	}
	for _, extension := range csr.request.Extensions {
		if !extension.Id.Equal([]int{2, 5, 29, 17}) {
			return fmt.Errorf("%w: CSR requests an unsupported extension", ErrPKIEnrollmentCSR)
		}
	}
	return nil
}

func issuePKIIdentityCertificate(randomSource io.Reader, now time.Time, endpointLifetime time.Duration, authority storage.PKIAuthorityRow, preparedAuthority bool, signer crypto.Signer, csr parsedPKIEnrollmentCSR, binding pkiIdentityBinding) (storage.PKICertificateRow, error) {
	if randomSource == nil || signer == nil || csr.request == nil || endpointLifetime <= 0 {
		return storage.PKICertificateRow{}, fmt.Errorf("%w: signing inputs are incomplete", ErrPKIEnrollmentAuthorityUnavailable)
	}
	authorityCertificate, err := parsePKIAuthorityCertificate(authority.CertificatePEM)
	if err != nil {
		return storage.PKICertificateRow{}, err
	}
	statusAllowed := authority.Status == "active" || preparedAuthority && authority.Status == "prepared"
	if !statusAllowed || authority.PKIDomainID != binding.DomainID || authority.Generation <= 0 || !authorityCertificate.IsCA || authorityCertificate.KeyUsage&x509.KeyUsageCertSign == 0 {
		return storage.PKICertificateRow{}, fmt.Errorf("%w: issuance authority metadata is invalid", ErrPKIEnrollmentAuthorityUnavailable)
	}
	if err := validatePKIAuthoritySigner(signer, authorityCertificate); err != nil {
		return storage.PKICertificateRow{}, err
	}
	serialBytes := make([]byte, 16)
	if _, err := io.ReadFull(randomSource, serialBytes); err != nil {
		return storage.PKICertificateRow{}, fmt.Errorf("generate endpoint certificate serial: %w", err)
	}
	serialBytes[0] |= 0x80
	serial := new(big.Int).SetBytes(serialBytes)
	now = now.UTC()
	notBefore := now.Add(-DefaultInternalPKIPolicy().NotBeforeSkew)
	if notBefore.Before(authorityCertificate.NotBefore) {
		notBefore = authorityCertificate.NotBefore
	}
	notAfter := now.Add(endpointLifetime)
	if notAfter.After(authorityCertificate.NotAfter) {
		notAfter = authorityCertificate.NotAfter
	}
	if !notAfter.After(now) || !notAfter.After(notBefore) {
		return storage.PKICertificateRow{}, fmt.Errorf("%w: authority validity cannot cover an endpoint certificate", ErrPKIEnrollmentAuthorityUnavailable)
	}
	extendedUsage := x509.ExtKeyUsageClientAuth
	if binding.Purpose == storage.PKICertificatePurposeServer {
		extendedUsage = x509.ExtKeyUsageServerAuth
	}
	template := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: binding.URI.String()},
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{extendedUsage},
		BasicConstraintsValid: true,
		SignatureAlgorithm:    x509.ECDSAWithSHA256,
		AuthorityKeyId:        append([]byte(nil), authorityCertificate.SubjectKeyId...),
		URIs:                  []*url.URL{binding.URI},
		DNSNames:              append([]string(nil), binding.DNSNames...),
		IPAddresses:           clonePKIIPs(binding.IPAddresses),
	}
	der, err := x509.CreateCertificate(randomSource, template, authorityCertificate, csr.request.PublicKey, signer)
	if err != nil {
		return storage.PKICertificateRow{}, fmt.Errorf("sign endpoint certificate: %w", err)
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		return storage.PKICertificateRow{}, fmt.Errorf("parse signed endpoint certificate: %w", err)
	}
	return storage.PKICertificateRow{
		SerialHex:            strings.ToLower(certificate.SerialNumber.Text(16)),
		Purpose:              binding.Purpose,
		AuthorityID:          authority.ID,
		CAGeneration:         authority.Generation,
		CertificatePEM:       string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})),
		PublicKeyFingerprint: csr.publicKeyFingerprint,
		NotBefore:            certificate.NotBefore,
		NotAfter:             certificate.NotAfter,
		Status:               storage.PKICertificateStatusActive,
	}, nil
}

func parsePKIAuthorityCertificate(value string) (*x509.Certificate, error) {
	encoded := bytes.TrimSpace([]byte(value))
	block, rest := pem.Decode(encoded)
	if block == nil || block.Type != "CERTIFICATE" || len(block.Headers) != 0 || len(bytes.TrimSpace(rest)) != 0 {
		return nil, fmt.Errorf("%w: authority certificate is malformed", ErrPKIEnrollmentAuthorityUnavailable)
	}
	certificate, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("%w: authority certificate is malformed", ErrPKIEnrollmentAuthorityUnavailable)
	}
	return certificate, nil
}

func validatePKIAuthoritySigner(signer crypto.Signer, certificate *x509.Certificate) error {
	privatePublicKey, ok := signer.Public().(*ecdsa.PublicKey)
	if !ok || privatePublicKey.Curve == nil || privatePublicKey.Curve.Params().Name != elliptic.P256().Params().Name {
		return fmt.Errorf("%w: authority signer must use ECDSA P-256", ErrPKIEnrollmentAuthorityUnavailable)
	}
	certificatePublicKey, ok := certificate.PublicKey.(*ecdsa.PublicKey)
	if !ok || !privatePublicKey.Equal(certificatePublicKey) {
		return fmt.Errorf("%w: authority signer does not match certificate", ErrPKIEnrollmentAuthorityUnavailable)
	}
	return nil
}

func parsePKIAuthorityPrivateKey(value []byte) (crypto.Signer, error) {
	der := value
	if block, rest := pem.Decode(bytes.TrimSpace(value)); block != nil {
		if len(block.Headers) != 0 || len(bytes.TrimSpace(rest)) != 0 {
			return nil, errors.New("authority key must contain exactly one PEM block")
		}
		der = block.Bytes
	}
	if key, err := x509.ParsePKCS8PrivateKey(der); err == nil {
		if signer, ok := key.(crypto.Signer); ok {
			return signer, nil
		}
		return nil, errors.New("authority PKCS#8 key is not a signer")
	}
	if key, err := x509.ParseECPrivateKey(der); err == nil {
		return key, nil
	}
	return nil, errors.New("authority key is not a supported EC private key")
}

func validatePKIURISegment(label, value string) error {
	if value == "" || value == "." || value == ".." || url.PathEscape(value) != value || strings.ContainsAny(value, "/\\:@?#[]") {
		return fmt.Errorf("%w: %s identity is not a safe URI segment", ErrPKIEnrollmentRequest, label)
	}
	return nil
}

func normalizePKIDNSNames(values []string) ([]string, error) {
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(value), "."))
		if !validPKIDNSName(value) || net.ParseIP(value) != nil {
			return nil, fmt.Errorf("%w: listener DNS name is invalid", ErrPKIEnrollmentRequest)
		}
		if _, duplicate := seen[value]; duplicate {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result, nil
}

func validPKIDNSName(value string) bool {
	if value == "" || len(value) > 253 {
		return false
	}
	for _, label := range strings.Split(value, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for index := 0; index < len(label); index++ {
			character := label[index]
			if character != '-' && (character < 'a' || character > 'z') && (character < '0' || character > '9') {
				return false
			}
		}
	}
	return true
}

func normalizePKIIPAddresses(values []string) ([]net.IP, error) {
	result := make([]net.IP, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		parsed := net.ParseIP(strings.TrimSpace(value))
		if parsed == nil {
			return nil, fmt.Errorf("%w: listener IP address is invalid", ErrPKIEnrollmentRequest)
		}
		canonical := parsed.String()
		if _, duplicate := seen[canonical]; duplicate {
			continue
		}
		seen[canonical] = struct{}{}
		result = append(result, append(net.IP(nil), parsed...))
	}
	sort.Slice(result, func(left, right int) bool { return result[left].String() < result[right].String() })
	return result, nil
}

func pkixNameMatchesCommonName(name pkix.Name, commonName string) bool {
	if len(name.Country)+len(name.Organization)+len(name.OrganizationalUnit)+len(name.Locality)+len(name.Province)+len(name.StreetAddress)+len(name.PostalCode)+len(name.ExtraNames) != 0 || name.SerialNumber != "" || name.CommonName != commonName {
		return false
	}
	if commonName == "" {
		return len(name.Names) == 0
	}
	return len(name.Names) == 1 && name.Names[0].Type.Equal([]int{2, 5, 4, 3})
}

func equalPKIStrings(left, right []string) bool {
	leftCopy := append([]string(nil), left...)
	rightCopy := append([]string(nil), right...)
	for index := range leftCopy {
		leftCopy[index] = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(leftCopy[index]), "."))
	}
	sort.Strings(leftCopy)
	sort.Strings(rightCopy)
	if len(leftCopy) != len(rightCopy) {
		return false
	}
	for index := range leftCopy {
		if leftCopy[index] != rightCopy[index] {
			return false
		}
	}
	return true
}

func equalPKIIPs(left, right []net.IP) bool {
	if len(left) != len(right) {
		return false
	}
	leftValues := make([]string, len(left))
	rightValues := make([]string, len(right))
	for index := range left {
		leftValues[index] = left[index].String()
	}
	for index := range right {
		rightValues[index] = right[index].String()
	}
	sort.Strings(leftValues)
	sort.Strings(rightValues)
	return equalPKIStrings(leftValues, rightValues)
}

func clonePKIIPs(values []net.IP) []net.IP {
	result := make([]net.IP, len(values))
	for index := range values {
		result[index] = append(net.IP(nil), values[index]...)
	}
	return result
}
