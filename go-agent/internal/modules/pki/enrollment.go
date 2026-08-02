package pki

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

func (s *Store) PrepareEnrollment(ctx context.Context, spec EnrollmentSpec) (PendingEnrollment, error) {
	if s == nil {
		return PendingEnrollment{}, errors.New("PKI store is required")
	}
	if err := ctx.Err(); err != nil {
		return PendingEnrollment{}, err
	}
	normalized, err := normalizeEnrollmentSpec(spec)
	if err != nil {
		return PendingEnrollment{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	identityRoot, err := s.identityRoot(normalized.StorageIdentity)
	if err != nil {
		return PendingEnrollment{}, err
	}
	if err := ensurePrivateDir(identityRoot); err != nil {
		return PendingEnrollment{}, err
	}
	if err := ensurePrivateDir(filepath.Join(identityRoot, generationsDirName)); err != nil {
		return PendingEnrollment{}, err
	}
	pendingRoot := filepath.Join(identityRoot, pendingDirName)
	if pending, loadErr := loadPendingAt(pendingRoot); loadErr == nil {
		if !pendingMatchesSpec(pending, normalized) {
			return PendingEnrollment{}, ErrPendingConflict
		}
		if err := validatePendingFiles(pendingRoot, pending); err != nil {
			return PendingEnrollment{}, err
		}
		return pending, nil
	} else if !errors.Is(loadErr, os.ErrNotExist) {
		return PendingEnrollment{}, loadErr
	}
	// A directory without its durable journal was never eligible to be sent.
	if info, statErr := os.Lstat(pendingRoot); statErr == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return PendingEnrollment{}, fmt.Errorf("pending PKI path is unsafe")
		}
		if err := os.RemoveAll(pendingRoot); err != nil {
			return PendingEnrollment{}, err
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return PendingEnrollment{}, statErr
	}

	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), s.random)
	if err != nil {
		return PendingEnrollment{}, fmt.Errorf("generate tunnel private key: %w", err)
	}
	requestID, err := randomHex(s.random, 16)
	if err != nil {
		return PendingEnrollment{}, fmt.Errorf("generate enrollment request ID: %w", err)
	}
	csrPEM, publicFingerprint, err := createEnrollmentCSR(privateKey, normalized, s.random)
	if err != nil {
		return PendingEnrollment{}, err
	}
	privateDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		return PendingEnrollment{}, fmt.Errorf("marshal tunnel private key: %w", err)
	}
	privatePEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateDER})
	request := model.PKIEnrollmentRequest{
		RequestID: requestID, Kind: normalized.Kind, ListenerID: normalized.ListenerID,
		Purpose: normalized.Purpose, CSRPEM: string(csrPEM),
		DNSNames: slices.Clone(normalized.DNSNames), IPAddresses: slices.Clone(normalized.IPAddresses),
	}
	fingerprint, err := enrollmentFingerprint(normalized, request)
	if err != nil {
		return PendingEnrollment{}, err
	}
	pending := PendingEnrollment{
		Version: 1, StorageIdentity: normalized.StorageIdentity, Request: request,
		DomainID: normalized.DomainID, AgentID: normalized.AgentID,
		RequestFingerprint: fingerprint, PublicKeyFingerprint: publicFingerprint,
		CreatedAt: s.clock().UTC(),
	}

	suffix, err := randomHex(s.random, 8)
	if err != nil {
		return PendingEnrollment{}, err
	}
	temporaryRoot := filepath.Join(identityRoot, ".pending-"+suffix)
	if err := ensurePrivateDir(temporaryRoot); err != nil {
		return PendingEnrollment{}, err
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.RemoveAll(temporaryRoot)
		}
	}()
	if err := writePrivateFile(filepath.Join(temporaryRoot, pendingKeyName), privatePEM); err != nil {
		return PendingEnrollment{}, err
	}
	if err := writePrivateFile(filepath.Join(temporaryRoot, pendingCSRName), csrPEM); err != nil {
		return PendingEnrollment{}, err
	}
	if _, err := writePrivateJSON(filepath.Join(temporaryRoot, pendingJournalName), pending); err != nil {
		return PendingEnrollment{}, err
	}
	if err := syncDirectory(temporaryRoot); err != nil {
		return PendingEnrollment{}, err
	}
	if err := os.Rename(temporaryRoot, pendingRoot); err != nil {
		return PendingEnrollment{}, err
	}
	cleanup = false
	if err := syncDirectory(identityRoot); err != nil {
		return PendingEnrollment{}, err
	}
	return pending, nil
}

func (s *Store) LoadPending(storageIdentity string) (PendingEnrollment, error) {
	if s == nil {
		return PendingEnrollment{}, errors.New("PKI store is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	identityRoot, err := s.identityRoot(storageIdentity)
	if err != nil {
		return PendingEnrollment{}, err
	}
	pendingRoot := filepath.Join(identityRoot, pendingDirName)
	pending, err := loadPendingAt(pendingRoot)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return PendingEnrollment{}, ErrPendingNotFound
		}
		return PendingEnrollment{}, err
	}
	if err := validatePendingFiles(pendingRoot, pending); err != nil {
		return PendingEnrollment{}, err
	}
	return pending, nil
}

func loadPendingAt(root string) (PendingEnrollment, error) {
	encoded, err := readPrivateFile(filepath.Join(root, pendingJournalName))
	if err != nil {
		return PendingEnrollment{}, err
	}
	var pending PendingEnrollment
	if err := decodeStrictJSON(encoded, &pending); err != nil {
		return PendingEnrollment{}, fmt.Errorf("decode pending PKI enrollment: %w", err)
	}
	if pending.Version != 1 || pending.Request.RequestID == "" || pending.Request.CSRPEM == "" ||
		pending.RequestFingerprint == "" || pending.PublicKeyFingerprint == "" {
		return PendingEnrollment{}, fmt.Errorf("%w: pending enrollment journal is incomplete", ErrCredentialInvalid)
	}
	return pending, nil
}

func validatePendingFiles(root string, pending PendingEnrollment) error {
	privatePEM, err := readPrivateFile(filepath.Join(root, pendingKeyName))
	if err != nil {
		return fmt.Errorf("read pending tunnel private key: %w", err)
	}
	csrPEM, err := readPrivateFile(filepath.Join(root, pendingCSRName))
	if err != nil {
		return fmt.Errorf("read pending tunnel CSR: %w", err)
	}
	if string(csrPEM) != pending.Request.CSRPEM {
		return fmt.Errorf("%w: pending CSR does not match its journal", ErrCredentialInvalid)
	}
	privateKey, err := parseECPrivateKeyPEM(privatePEM)
	if err != nil {
		return err
	}
	request, err := parseCSRPEM(csrPEM)
	if err != nil {
		return err
	}
	privatePublic, err := x509.MarshalPKIXPublicKey(&privateKey.PublicKey)
	if err != nil {
		return err
	}
	if !slices.Equal(privatePublic, request.RawSubjectPublicKeyInfo) {
		return fmt.Errorf("%w: pending key does not match CSR", ErrCredentialInvalid)
	}
	digest := sha256.Sum256(request.RawSubjectPublicKeyInfo)
	if !strings.EqualFold(hex.EncodeToString(digest[:]), pending.PublicKeyFingerprint) {
		return fmt.Errorf("%w: pending public key fingerprint is inconsistent", ErrCredentialInvalid)
	}
	return nil
}

func normalizeEnrollmentSpec(spec EnrollmentSpec) (EnrollmentSpec, error) {
	spec.StorageIdentity = strings.TrimSpace(spec.StorageIdentity)
	spec.DomainID = strings.TrimSpace(spec.DomainID)
	spec.AgentID = strings.TrimSpace(spec.AgentID)
	spec.Kind = strings.TrimSpace(spec.Kind)
	spec.ListenerID = strings.TrimSpace(spec.ListenerID)
	spec.Purpose = strings.TrimSpace(spec.Purpose)
	if !safeIdentityPattern.MatchString(spec.StorageIdentity) {
		return EnrollmentSpec{}, ErrInvalidIdentity
	}
	if spec.Kind == "" {
		spec.Kind = model.PKIIdentityKindAgent
	}
	if spec.Purpose == "" && spec.Kind == model.PKIIdentityKindAgent {
		spec.Purpose = model.PKICertificatePurposeClient
	}
	var err error
	spec.DNSNames, err = normalizeDNSNames(spec.DNSNames)
	if err != nil {
		return EnrollmentSpec{}, err
	}
	spec.IPAddresses, err = normalizeIPAddresses(spec.IPAddresses)
	if err != nil {
		return EnrollmentSpec{}, err
	}
	anonymous := spec.DomainID == "" && spec.AgentID == ""
	if anonymous {
		if spec.Kind != model.PKIIdentityKindAgent || spec.Purpose != model.PKICertificatePurposeClient ||
			spec.ListenerID != "" || len(spec.DNSNames) != 0 || len(spec.IPAddresses) != 0 {
			return EnrollmentSpec{}, fmt.Errorf("%w: anonymous enrollment must be an agent client without SANs", ErrInvalidIdentity)
		}
		return spec, nil
	}
	if err := validateURISegment(spec.DomainID); err != nil {
		return EnrollmentSpec{}, fmt.Errorf("%w: invalid PKI domain", ErrInvalidIdentity)
	}
	if err := validateURISegment(spec.AgentID); err != nil {
		return EnrollmentSpec{}, fmt.Errorf("%w: invalid agent ID", ErrInvalidIdentity)
	}
	switch spec.Kind {
	case model.PKIIdentityKindAgent:
		if spec.Purpose != model.PKICertificatePurposeClient || spec.ListenerID != "" || len(spec.DNSNames) != 0 || len(spec.IPAddresses) != 0 {
			return EnrollmentSpec{}, fmt.Errorf("%w: agent identity requires client purpose and no listener SANs", ErrInvalidIdentity)
		}
	case model.PKIIdentityKindListener:
		if spec.Purpose != model.PKICertificatePurposeServer || validateURISegment(spec.ListenerID) != nil {
			return EnrollmentSpec{}, fmt.Errorf("%w: listener identity requires a safe listener ID and server purpose", ErrInvalidIdentity)
		}
	default:
		return EnrollmentSpec{}, fmt.Errorf("%w: unsupported identity kind", ErrInvalidIdentity)
	}
	return spec, nil
}

func createEnrollmentCSR(privateKey *ecdsa.PrivateKey, spec EnrollmentSpec, randomSource interface{ Read([]byte) (int, error) }) ([]byte, string, error) {
	template := &x509.CertificateRequest{SignatureAlgorithm: x509.ECDSAWithSHA256}
	if spec.DomainID != "" {
		identityURI := &url.URL{Scheme: "spiffe", Host: spec.DomainID, Path: "/agent/" + spec.AgentID}
		if spec.Kind == model.PKIIdentityKindListener {
			identityURI.Path += "/listener/" + spec.ListenerID
		}
		template.Subject = pkix.Name{CommonName: identityURI.String()}
		template.URIs = []*url.URL{identityURI}
		template.DNSNames = slices.Clone(spec.DNSNames)
		for _, value := range spec.IPAddresses {
			template.IPAddresses = append(template.IPAddresses, net.ParseIP(value))
		}
	}
	der, err := x509.CreateCertificateRequest(randomSource, template, privateKey)
	if err != nil {
		return nil, "", fmt.Errorf("create tunnel CSR: %w", err)
	}
	request, err := x509.ParseCertificateRequest(der)
	if err != nil || request.CheckSignature() != nil {
		return nil, "", fmt.Errorf("create tunnel CSR: generated request is invalid")
	}
	digest := sha256.Sum256(request.RawSubjectPublicKeyInfo)
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der}), hex.EncodeToString(digest[:]), nil
}

func enrollmentFingerprint(spec EnrollmentSpec, request model.PKIEnrollmentRequest) (string, error) {
	canonical := struct {
		StorageIdentity string                     `json:"storage_identity"`
		DomainID        string                     `json:"pki_domain_id"`
		AgentID         string                     `json:"agent_id"`
		Request         model.PKIEnrollmentRequest `json:"request"`
	}{spec.StorageIdentity, spec.DomainID, spec.AgentID, request}
	encoded, err := json.Marshal(canonical)
	if err != nil {
		return "", err
	}
	return sha256Hex(encoded), nil
}

func pendingMatchesSpec(pending PendingEnrollment, spec EnrollmentSpec) bool {
	return pending.StorageIdentity == spec.StorageIdentity && pending.DomainID == spec.DomainID && pending.AgentID == spec.AgentID &&
		pending.Request.Kind == spec.Kind && pending.Request.ListenerID == spec.ListenerID && pending.Request.Purpose == spec.Purpose &&
		slices.Equal(pending.Request.DNSNames, spec.DNSNames) && slices.Equal(pending.Request.IPAddresses, spec.IPAddresses)
}

func validateURISegment(value string) error {
	if value == "" || value == "." || value == ".." || url.PathEscape(value) != value || strings.ContainsAny(value, "/\\:@?#[]") {
		return ErrInvalidIdentity
	}
	return nil
}

func normalizeDNSNames(values []string) ([]string, error) {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(value), "."))
		if value == "" || strings.ContainsAny(value, " /\\:@?#[]") || net.ParseIP(value) != nil {
			return nil, fmt.Errorf("%w: listener DNS name is invalid", ErrInvalidIdentity)
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result, nil
}

func normalizeIPAddresses(values []string) ([]string, error) {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		parsed := net.ParseIP(strings.TrimSpace(value))
		if parsed == nil {
			return nil, fmt.Errorf("%w: listener IP address is invalid", ErrInvalidIdentity)
		}
		canonical := parsed.String()
		if _, exists := seen[canonical]; exists {
			continue
		}
		seen[canonical] = struct{}{}
		result = append(result, canonical)
	}
	sort.Strings(result)
	return result, nil
}

func parseECPrivateKeyPEM(encoded []byte) (*ecdsa.PrivateKey, error) {
	block, rest := pem.Decode(encoded)
	if block == nil || len(strings.TrimSpace(string(rest))) != 0 {
		return nil, fmt.Errorf("%w: private key PEM is invalid", ErrCredentialInvalid)
	}
	var parsed any
	var err error
	switch block.Type {
	case "PRIVATE KEY":
		parsed, err = x509.ParsePKCS8PrivateKey(block.Bytes)
	case "EC PRIVATE KEY":
		parsed, err = x509.ParseECPrivateKey(block.Bytes)
	default:
		return nil, fmt.Errorf("%w: private key PEM type is unsupported", ErrCredentialInvalid)
	}
	if err != nil {
		return nil, fmt.Errorf("%w: parse private key: %v", ErrCredentialInvalid, err)
	}
	privateKey, ok := parsed.(*ecdsa.PrivateKey)
	if !ok || privateKey.Curve != elliptic.P256() {
		return nil, fmt.Errorf("%w: private key must use ECDSA P-256", ErrCredentialInvalid)
	}
	return privateKey, nil
}

func parseCSRPEM(encoded []byte) (*x509.CertificateRequest, error) {
	block, rest := pem.Decode(encoded)
	if block == nil || block.Type != "CERTIFICATE REQUEST" || len(block.Headers) != 0 || len(strings.TrimSpace(string(rest))) != 0 {
		return nil, fmt.Errorf("%w: CSR PEM is invalid", ErrCredentialInvalid)
	}
	request, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil || request.CheckSignature() != nil {
		return nil, fmt.Errorf("%w: CSR is invalid", ErrCredentialInvalid)
	}
	return request, nil
}
