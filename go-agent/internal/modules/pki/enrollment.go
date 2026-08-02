package pki

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"crypto/subtle"
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
	if err := ensureDurablePrivateSubdir(filepath.Join(s.root, identitiesDirName), normalized.StorageIdentity, s.random); err != nil {
		return PendingEnrollment{}, err
	}
	if err := ensureDurablePrivateSubdir(identityRoot, generationsDirName, s.random); err != nil {
		return PendingEnrollment{}, err
	}
	pendingRoot := filepath.Join(identityRoot, pendingDirName)
	if pending, loadErr := loadPendingAt(pendingRoot); loadErr == nil {
		if !pendingMatchesSpec(pending, normalized) {
			return PendingEnrollment{}, ErrPendingConflict
		}
		if err := validatePendingFiles(pendingRoot, pending); err != nil {
			return PendingEnrollment{}, classifyPendingValidationError(err)
		}
		// A prior attempt may have published the directory and then failed while
		// flushing its parent. Re-establish both barriers before the request is
		// eligible to leave the execution plane.
		if err := syncDirectory(pendingRoot); err != nil {
			return PendingEnrollment{}, err
		}
		if err := syncDirectory(identityRoot); err != nil {
			return PendingEnrollment{}, err
		}
		return clonePendingEnrollment(pending), nil
	} else if !errors.Is(loadErr, os.ErrNotExist) {
		return PendingEnrollment{}, loadErr
	}
	// A published pending directory without its journal is corruption. Never
	// delete it automatically: it may contain the only copy of a private key
	// whose directory publication completed before a process loss.
	if info, statErr := os.Lstat(pendingRoot); statErr == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return PendingEnrollment{}, fmt.Errorf("pending PKI path is unsafe")
		}
		return PendingEnrollment{}, fmt.Errorf("%w: pending enrollment journal is missing", ErrCredentialInvalid)
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
	if err := publishDirectory(temporaryRoot, pendingRoot); err != nil {
		return PendingEnrollment{}, err
	}
	cleanup = false
	if err := s.persistenceCheckpoint("enrollment.after_publish"); err != nil {
		return PendingEnrollment{}, err
	}
	if err := syncDirectory(identityRoot); err != nil {
		return PendingEnrollment{}, err
	}
	return clonePendingEnrollment(pending), nil
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
		return PendingEnrollment{}, classifyPendingLoadError(pendingRoot, err)
	}
	if err := validatePendingFiles(pendingRoot, pending); err != nil {
		return PendingEnrollment{}, classifyPendingValidationError(err)
	}
	return clonePendingEnrollment(pending), nil
}

func classifyPendingLoadError(pendingRoot string, err error) error {
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	info, statErr := os.Lstat(pendingRoot)
	if errors.Is(statErr, os.ErrNotExist) {
		return ErrPendingNotFound
	}
	if statErr != nil {
		return statErr
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("%w: pending enrollment path is unsafe", ErrCredentialInvalid)
	}
	return fmt.Errorf("%w: pending enrollment journal is missing", ErrCredentialInvalid)
}

func classifyPendingValidationError(err error) error {
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("%w: pending enrollment key or CSR is missing", ErrCredentialInvalid)
	}
	return err
}

// PendingEnrollments returns a stable, public-only view of every replayable
// request. Enumeration is fail closed: one unsafe identity, corrupt journal,
// mismatched key/CSR, or invalid fingerprint rejects the whole result.
func (s *Store) PendingEnrollments() ([]PendingEnrollment, error) {
	if s == nil {
		return nil, errors.New("PKI store is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	identitiesRoot := filepath.Join(s.root, identitiesDirName)
	entries, err := os.ReadDir(identitiesRoot)
	if err != nil {
		return nil, err
	}
	result := make([]PendingEnrollment, 0, len(entries))
	for _, entry := range entries {
		if entry.Type()&os.ModeSymlink != 0 || !entry.IsDir() || !validStorageIdentity(entry.Name()) {
			return nil, fmt.Errorf("%w: unsafe PKI identity entry %q", ErrInvalidIdentity, entry.Name())
		}
		pendingRoot := filepath.Join(identitiesRoot, entry.Name(), pendingDirName)
		pending, loadErr := loadPendingAt(pendingRoot)
		if errors.Is(loadErr, os.ErrNotExist) {
			if _, statErr := os.Lstat(pendingRoot); statErr == nil {
				return nil, fmt.Errorf("%w: pending enrollment journal is missing for %q", ErrCredentialInvalid, entry.Name())
			} else if !errors.Is(statErr, os.ErrNotExist) {
				return nil, statErr
			}
			continue
		}
		if loadErr != nil {
			return nil, loadErr
		}
		if pending.StorageIdentity != entry.Name() {
			return nil, fmt.Errorf("%w: pending enrollment storage identity is inconsistent", ErrCredentialInvalid)
		}
		if err := validatePendingFiles(pendingRoot, pending); err != nil {
			return nil, classifyPendingValidationError(err)
		}
		result = append(result, clonePendingEnrollment(pending))
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].StorageIdentity == result[j].StorageIdentity {
			return result[i].Request.RequestID < result[j].Request.RequestID
		}
		return result[i].StorageIdentity < result[j].StorageIdentity
	})
	return result, nil
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
		!validStorageIdentity(pending.StorageIdentity) || len(pending.Request.RequestID) != 32 || !validLowerHex(pending.Request.RequestID) ||
		len(pending.RequestFingerprint) != sha256.Size*2 || !validLowerHex(pending.RequestFingerprint) ||
		len(pending.PublicKeyFingerprint) != sha256.Size*2 || !validLowerHex(pending.PublicKeyFingerprint) || pending.CreatedAt.IsZero() {
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
	if !constantTimeHexEqual(hex.EncodeToString(digest[:]), pending.PublicKeyFingerprint) {
		return fmt.Errorf("%w: pending public key fingerprint is inconsistent", ErrCredentialInvalid)
	}
	spec, err := normalizeEnrollmentSpec(EnrollmentSpec{
		StorageIdentity: pending.StorageIdentity,
		DomainID:        pending.DomainID,
		AgentID:         pending.AgentID,
		Kind:            pending.Request.Kind,
		ListenerID:      pending.Request.ListenerID,
		Purpose:         pending.Request.Purpose,
		DNSNames:        pending.Request.DNSNames,
		IPAddresses:     pending.Request.IPAddresses,
	})
	if err != nil || !pendingMatchesSpec(pending, spec) {
		return fmt.Errorf("%w: pending enrollment metadata is not canonical", ErrCredentialInvalid)
	}
	if err := validateCSRMetadata(request, spec); err != nil {
		return err
	}
	expectedFingerprint, err := enrollmentFingerprint(spec, pending.Request)
	if err != nil {
		return err
	}
	if !constantTimeHexEqual(expectedFingerprint, pending.RequestFingerprint) {
		// Version-one shell journals created before the canonical fingerprint
		// contract hashed the CSR file. Accept that exact recomputed value only
		// when its legacy request-id sidecar is present and consistent; all owner
		// metadata is still independently bound to the CSR above.
		legacyID, legacyErr := readPrivateFile(filepath.Join(root, "request-id"))
		legacyFingerprint := sha256Hex(csrPEM)
		if legacyErr != nil || strings.TrimSpace(string(legacyID)) != pending.Request.RequestID ||
			!constantTimeHexEqual(legacyFingerprint, pending.RequestFingerprint) {
			return fmt.Errorf("%w: pending request fingerprint is inconsistent", ErrCredentialInvalid)
		}
	}
	return nil
}

func validateCSRMetadata(request *x509.CertificateRequest, spec EnrollmentSpec) error {
	if request == nil {
		return fmt.Errorf("%w: pending CSR is unavailable", ErrCredentialInvalid)
	}
	if spec.DomainID == "" && spec.AgentID == "" {
		if request.Subject.String() != "" || len(request.URIs) != 0 || len(request.DNSNames) != 0 || len(request.IPAddresses) != 0 {
			return fmt.Errorf("%w: anonymous pending CSR contains identity metadata", ErrCredentialInvalid)
		}
		return nil
	}
	expectedURI := &url.URL{Scheme: "spiffe", Host: spec.DomainID, Path: "/agent/" + spec.AgentID}
	if spec.Kind == model.PKIIdentityKindListener {
		expectedURI.Path += "/listener/" + spec.ListenerID
	}
	if request.Subject.CommonName != expectedURI.String() || len(request.URIs) != 1 || request.URIs[0] == nil || request.URIs[0].String() != expectedURI.String() {
		return fmt.Errorf("%w: pending CSR URI identity differs from its journal", ErrCredentialInvalid)
	}
	if !equalDNSNames(request.DNSNames, spec.DNSNames) || !equalIPAddresses(request.IPAddresses, spec.IPAddresses) {
		return fmt.Errorf("%w: pending CSR SANs differ from its journal", ErrCredentialInvalid)
	}
	return nil
}

func constantTimeHexEqual(left, right string) bool {
	left = strings.ToLower(strings.TrimSpace(left))
	right = strings.ToLower(strings.TrimSpace(right))
	return len(left) == len(right) && subtle.ConstantTimeCompare([]byte(left), []byte(right)) == 1
}

func clonePendingEnrollment(pending PendingEnrollment) PendingEnrollment {
	cloned := pending
	cloned.Request.DNSNames = slices.Clone(pending.Request.DNSNames)
	cloned.Request.IPAddresses = slices.Clone(pending.Request.IPAddresses)
	return cloned
}

func normalizeEnrollmentSpec(spec EnrollmentSpec) (EnrollmentSpec, error) {
	spec.StorageIdentity = strings.TrimSpace(spec.StorageIdentity)
	spec.DomainID = strings.TrimSpace(spec.DomainID)
	spec.AgentID = strings.TrimSpace(spec.AgentID)
	spec.Kind = strings.TrimSpace(spec.Kind)
	spec.ListenerID = strings.TrimSpace(spec.ListenerID)
	spec.Purpose = strings.TrimSpace(spec.Purpose)
	if !validStorageIdentity(spec.StorageIdentity) {
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
