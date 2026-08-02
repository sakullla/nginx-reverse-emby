package pki

import (
	"context"
	"crypto/ecdsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
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
	"strings"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

// ActivateCredential validates and publishes a complete immutable credential
// generation, then atomically switches the single active pointer. The signed
// security snapshot is made durable first; an activation failure never moves
// the credential pointer or acknowledges the response.
func (s *Store) ActivateCredential(ctx context.Context, request ActivateRequest) (ActiveCredential, error) {
	if s == nil {
		return ActiveCredential{}, errors.New("PKI store is required")
	}
	if err := ctx.Err(); err != nil {
		return ActiveCredential{}, err
	}
	request.StorageIdentity = strings.TrimSpace(request.StorageIdentity)
	request.RequestID = strings.TrimSpace(request.RequestID)
	if request.StorageIdentity == "" || request.RequestID == "" {
		return ActiveCredential{}, fmt.Errorf("%w: storage identity and request ID are required", ErrCredentialInvalid)
	}
	expectation, err := normalizeCredentialExpectation(request.Expectation)
	if err != nil {
		return ActiveCredential{}, err
	}
	request.Expectation = expectation
	securityState, err := s.ApplySecuritySnapshot(request.Security)
	if err != nil {
		return ActiveCredential{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	identityRoot, err := s.identityRoot(request.StorageIdentity)
	if err != nil {
		return ActiveCredential{}, err
	}
	pendingRoot := filepath.Join(identityRoot, pendingDirName)
	pending, err := loadPendingAt(pendingRoot)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// A replay after a successful cutover is idempotent if the currently
			// active generation is the exact credential response.
			active, activeErr := s.loadActiveCredentialLocked(request.StorageIdentity)
			if activeErr == nil && active.Manifest.RequestID == request.RequestID &&
				active.Manifest.Credential.CertificateID == request.Credential.CertificateID {
				return active, nil
			}
			return ActiveCredential{}, ErrPendingNotFound
		}
		return ActiveCredential{}, err
	}
	if pending.Request.RequestID != request.RequestID || pending.StorageIdentity != request.StorageIdentity {
		return ActiveCredential{}, ErrPendingConflict
	}
	if err := validatePendingFiles(pendingRoot, pending); err != nil {
		return ActiveCredential{}, err
	}
	privatePEM, err := readPrivateFile(filepath.Join(pendingRoot, pendingKeyName))
	if err != nil {
		return ActiveCredential{}, err
	}
	validated, err := validateCredential(privatePEM, request.Credential, securityState.Snapshot, expectation)
	if err != nil {
		return ActiveCredential{}, err
	}

	generationDigest := sha256.Sum256([]byte(request.RequestID + "\x00" + request.Credential.CertificateID + "\x00" + request.Credential.PublicKeyFingerprint))
	generation := fmt.Sprintf("g%d-%s", request.Credential.CAGeneration, hex.EncodeToString(generationDigest[:12]))
	manifest := CredentialManifest{
		Version: 1, Generation: generation, RequestID: request.RequestID,
		RequestFingerprint: pending.RequestFingerprint, Credential: request.Credential,
		PKIDomainID: securityState.Snapshot.PKIDomainID, PKIEpoch: securityState.Snapshot.PKIEpoch,
		SecurityRevision: securityState.Snapshot.SecurityRevision, SecuritySnapshotHash: securityState.Hash,
		Expectation: expectation, ActivatedAt: s.clock().UTC(),
	}
	manifestEncoded, err := json.Marshal(manifest)
	if err != nil {
		return ActiveCredential{}, err
	}
	manifestHash := sha256Hex(manifestEncoded)
	generationsRoot := filepath.Join(identityRoot, generationsDirName)
	if err := ensurePrivateDir(generationsRoot); err != nil {
		return ActiveCredential{}, err
	}
	generationRoot := filepath.Join(generationsRoot, generation)
	if info, statErr := os.Lstat(generationRoot); statErr == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return ActiveCredential{}, fmt.Errorf("%w: immutable generation path is unsafe", ErrCredentialInvalid)
		}
		existingManifest, readErr := readPrivateFile(filepath.Join(generationRoot, manifestName))
		if readErr != nil {
			return ActiveCredential{}, readErr
		}
		// ActivatedAt is not part of the server response. Preserve the first
		// immutable generation rather than manufacturing a second variant.
		var existing CredentialManifest
		if decodeStrictJSON(existingManifest, &existing) != nil || existing.RequestID != manifest.RequestID ||
			existing.Credential.CertificateID != manifest.Credential.CertificateID || existing.SecuritySnapshotHash != manifest.SecuritySnapshotHash {
			return ActiveCredential{}, fmt.Errorf("%w: immutable generation collision", ErrCredentialInvalid)
		}
		manifest = existing
		manifestEncoded = existingManifest
		manifestHash = sha256Hex(existingManifest)
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return ActiveCredential{}, statErr
	} else if err := s.publishCredentialGeneration(generationsRoot, generationRoot, manifestEncoded, privatePEM, request.Credential.CertificatePEM, securityState); err != nil {
		return ActiveCredential{}, err
	}
	pointer := ActivePointer{Version: 1, Generation: generation, ManifestHash: manifestHash, ActivatedAt: s.clock().UTC()}
	if _, err := writeAtomicPrivateJSON(identityRoot, activePointerName, pointer, s.random); err != nil {
		return ActiveCredential{}, fmt.Errorf("activate credential generation: %w", err)
	}
	acknowledgement := securityAcknowledgement(securityState.Snapshot, request.Credential.CertificateID)
	if _, err := writeAtomicPrivateJSON(filepath.Join(s.root, securityDirName), "ack.json", acknowledgement, s.random); err != nil {
		return ActiveCredential{}, fmt.Errorf("persist durable security acknowledgement: %w", err)
	}
	// Pending data is removed only after the complete generation and pointer are
	// durable. A crash before this point leaves a replayable request.
	if err := os.RemoveAll(pendingRoot); err != nil {
		return ActiveCredential{}, fmt.Errorf("remove completed enrollment journal: %w", err)
	}
	if err := syncDirectory(identityRoot); err != nil {
		return ActiveCredential{}, err
	}
	validated.Manifest = manifest
	validated.Security = securityState.Snapshot
	return validated, nil
}

// ActivateStagedRegistration consumes the sanitized response produced by
// join-agent.sh. It derives the server-bound agent expectation from the signed
// snapshot and the stable agent ID, then follows the same validated generation
// activation path as an in-process control response.
func (s *Store) ActivateStagedRegistration(ctx context.Context, storageIdentity string) (ActiveCredential, error) {
	staged, pending, err := s.LoadStagedRegistration(storageIdentity)
	if err != nil {
		return ActiveCredential{}, err
	}
	return s.ActivateCredential(ctx, ActivateRequest{
		StorageIdentity: storageIdentity,
		RequestID:       pending.Request.RequestID,
		Credential:      staged.TunnelCredential,
		Security:        staged.SecuritySnapshot,
		Expectation: CredentialExpectation{
			DomainID: staged.SecuritySnapshot.PKIDomainID, AgentID: staged.AgentID,
			Kind: pending.Request.Kind, ListenerID: pending.Request.ListenerID,
			Purpose: pending.Request.Purpose, DNSNames: pending.Request.DNSNames,
			IPAddresses: pending.Request.IPAddresses, Now: s.clock().UTC(),
		},
	})
}

func (s *Store) LoadStagedRegistration(storageIdentity string) (StagedRegistration, PendingEnrollment, error) {
	if s == nil {
		return StagedRegistration{}, PendingEnrollment{}, errors.New("PKI store is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	identityRoot, err := s.identityRoot(storageIdentity)
	if err != nil {
		return StagedRegistration{}, PendingEnrollment{}, err
	}
	pendingRoot := filepath.Join(identityRoot, pendingDirName)
	pending, err := loadPendingAt(pendingRoot)
	if err != nil {
		return StagedRegistration{}, PendingEnrollment{}, err
	}
	if err := validatePendingFiles(pendingRoot, pending); err != nil {
		return StagedRegistration{}, PendingEnrollment{}, err
	}
	encoded, err := readPrivateFile(filepath.Join(pendingRoot, "response.json"))
	if err != nil {
		return StagedRegistration{}, PendingEnrollment{}, err
	}
	var staged StagedRegistration
	if err := decodeStrictJSON(encoded, &staged); err != nil {
		return StagedRegistration{}, PendingEnrollment{}, fmt.Errorf("decode staged PKI registration: %w", err)
	}
	if strings.TrimSpace(staged.AgentID) == "" || strings.TrimSpace(staged.TunnelCredential.CertificateID) == "" ||
		strings.TrimSpace(staged.SecuritySnapshot.PKIDomainID) == "" {
		return StagedRegistration{}, PendingEnrollment{}, fmt.Errorf("%w: staged registration is incomplete", ErrCredentialInvalid)
	}
	return staged, pending, nil
}

func (s *Store) publishCredentialGeneration(generationsRoot, generationRoot string, manifest, privateKey []byte, certificatePEM string, security SecurityState) error {
	suffix, err := randomHex(s.random, 8)
	if err != nil {
		return err
	}
	temporaryRoot := filepath.Join(generationsRoot, ".candidate-"+suffix)
	if err := ensurePrivateDir(temporaryRoot); err != nil {
		return err
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.RemoveAll(temporaryRoot)
		}
	}()
	securityEncoded, err := json.Marshal(security)
	if err != nil {
		return err
	}
	for name, value := range map[string][]byte{
		manifestName: manifest, privateKeyName: privateKey,
		certificateName: []byte(strings.TrimSpace(certificatePEM) + "\n"), securityName: securityEncoded,
	} {
		if err := writePrivateFile(filepath.Join(temporaryRoot, name), value); err != nil {
			return err
		}
	}
	if err := syncDirectory(temporaryRoot); err != nil {
		return err
	}
	if err := os.Rename(temporaryRoot, generationRoot); err != nil {
		return err
	}
	cleanup = false
	return syncDirectory(generationsRoot)
}

func (s *Store) LoadActiveCredential(storageIdentity string) (ActiveCredential, error) {
	if s == nil {
		return ActiveCredential{}, errors.New("PKI store is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	active, err := s.loadActiveCredentialLocked(storageIdentity)
	if errors.Is(err, os.ErrNotExist) {
		return ActiveCredential{}, ErrActiveCredential
	}
	return active, err
}

func (s *Store) loadActiveCredentialLocked(storageIdentity string) (ActiveCredential, error) {
	identityRoot, err := s.identityRoot(storageIdentity)
	if err != nil {
		return ActiveCredential{}, err
	}
	pointerEncoded, err := readPrivateFile(filepath.Join(identityRoot, activePointerName))
	if err != nil {
		return ActiveCredential{}, err
	}
	var pointer ActivePointer
	if err := decodeStrictJSON(pointerEncoded, &pointer); err != nil {
		return ActiveCredential{}, fmt.Errorf("decode active credential pointer: %w", err)
	}
	if pointer.Version != 1 || !safeIdentityPattern.MatchString(pointer.Generation) || pointer.ManifestHash == "" {
		return ActiveCredential{}, fmt.Errorf("%w: active credential pointer is invalid", ErrActiveCredential)
	}
	generationRoot := filepath.Join(identityRoot, generationsDirName, pointer.Generation)
	manifestEncoded, err := readPrivateFile(filepath.Join(generationRoot, manifestName))
	if err != nil {
		return ActiveCredential{}, err
	}
	var manifest CredentialManifest
	if err := decodeStrictJSON(manifestEncoded, &manifest); err != nil {
		return ActiveCredential{}, fmt.Errorf("decode credential manifest: %w", err)
	}
	if manifest.Version != 1 || manifest.Generation != pointer.Generation || sha256Hex(manifestEncoded) != pointer.ManifestHash {
		return ActiveCredential{}, fmt.Errorf("%w: active credential manifest is inconsistent", ErrActiveCredential)
	}
	privatePEM, err := readPrivateFile(filepath.Join(generationRoot, privateKeyName))
	if err != nil {
		return ActiveCredential{}, err
	}
	certificatePEM, err := readPrivateFile(filepath.Join(generationRoot, certificateName))
	if err != nil {
		return ActiveCredential{}, err
	}
	if strings.TrimSpace(string(certificatePEM)) != strings.TrimSpace(manifest.Credential.CertificatePEM) {
		return ActiveCredential{}, fmt.Errorf("%w: active certificate differs from manifest", ErrActiveCredential)
	}
	security, err := s.loadSecurityStateLocked()
	if err != nil {
		return ActiveCredential{}, err
	}
	if manifest.PKIDomainID != security.Snapshot.PKIDomainID || manifest.PKIEpoch > security.Snapshot.PKIEpoch ||
		(manifest.PKIEpoch == security.Snapshot.PKIEpoch && manifest.SecurityRevision > security.Snapshot.SecurityRevision) {
		return ActiveCredential{}, fmt.Errorf("%w: active credential security generation is unavailable", ErrActiveCredential)
	}
	expectation := manifest.Expectation
	expectation.Now = s.clock().UTC()
	active, err := validateCredential(privatePEM, manifest.Credential, security.Snapshot, expectation)
	if err != nil {
		return ActiveCredential{}, err
	}
	active.Manifest = manifest
	active.Security = security.Snapshot
	return active, nil
}

func normalizeCredentialExpectation(expectation CredentialExpectation) (CredentialExpectation, error) {
	expectation.DomainID = strings.TrimSpace(expectation.DomainID)
	expectation.AgentID = strings.TrimSpace(expectation.AgentID)
	expectation.Kind = strings.TrimSpace(expectation.Kind)
	expectation.ListenerID = strings.TrimSpace(expectation.ListenerID)
	expectation.Purpose = strings.TrimSpace(expectation.Purpose)
	if expectation.Kind == "" {
		expectation.Kind = model.PKIIdentityKindAgent
	}
	if expectation.Purpose == "" && expectation.Kind == model.PKIIdentityKindAgent {
		expectation.Purpose = model.PKICertificatePurposeClient
	}
	if validateURISegment(expectation.DomainID) != nil || validateURISegment(expectation.AgentID) != nil {
		return CredentialExpectation{}, fmt.Errorf("%w: expected PKI domain or agent identity is invalid", ErrCredentialInvalid)
	}
	var err error
	expectation.DNSNames, err = normalizeDNSNames(expectation.DNSNames)
	if err != nil {
		return CredentialExpectation{}, err
	}
	expectation.IPAddresses, err = normalizeIPAddresses(expectation.IPAddresses)
	if err != nil {
		return CredentialExpectation{}, err
	}
	switch expectation.Kind {
	case model.PKIIdentityKindAgent:
		if expectation.Purpose != model.PKICertificatePurposeClient || expectation.ListenerID != "" || len(expectation.DNSNames) != 0 || len(expectation.IPAddresses) != 0 {
			return CredentialExpectation{}, fmt.Errorf("%w: expected agent credential shape is invalid", ErrCredentialInvalid)
		}
	case model.PKIIdentityKindListener:
		if expectation.Purpose != model.PKICertificatePurposeServer || validateURISegment(expectation.ListenerID) != nil {
			return CredentialExpectation{}, fmt.Errorf("%w: expected listener credential shape is invalid", ErrCredentialInvalid)
		}
	default:
		return CredentialExpectation{}, fmt.Errorf("%w: expected identity kind is unsupported", ErrCredentialInvalid)
	}
	if expectation.Now.IsZero() {
		expectation.Now = time.Now().UTC()
	} else {
		expectation.Now = expectation.Now.UTC()
	}
	return expectation, nil
}

func validateCredential(privatePEM []byte, credential model.PKITunnelCredential, security model.PKISecuritySnapshot, expectation CredentialExpectation) (ActiveCredential, error) {
	if credential.IdentityID == "" || credential.CertificateID == "" || credential.AuthorityID == "" || credential.CAGeneration <= 0 ||
		credential.Purpose != expectation.Purpose || credential.NotBefore.IsZero() || credential.NotAfter.IsZero() ||
		security.PKIDomainID != expectation.DomainID {
		return ActiveCredential{}, fmt.Errorf("%w: credential metadata is incomplete", ErrCredentialInvalid)
	}
	if slices.Contains(security.RevokedIdentityIDs, credential.IdentityID) {
		return ActiveCredential{}, fmt.Errorf("%w: identity is revoked", ErrCredentialInvalid)
	}
	privateKey, err := parseECPrivateKeyPEM(privatePEM)
	if err != nil {
		return ActiveCredential{}, err
	}
	leaf, err := parseCertificatePEM(credential.CertificatePEM)
	if err != nil {
		return ActiveCredential{}, fmt.Errorf("%w: parse endpoint certificate: %v", ErrCredentialInvalid, err)
	}
	if leaf.IsCA || expectation.Now.Before(leaf.NotBefore) || expectation.Now.After(leaf.NotAfter) ||
		!leaf.NotBefore.Equal(credential.NotBefore) || !leaf.NotAfter.Equal(credential.NotAfter) {
		return ActiveCredential{}, fmt.Errorf("%w: endpoint certificate lifetime is invalid", ErrCredentialInvalid)
	}
	serial := strings.ToLower(leaf.SerialNumber.Text(16))
	if slices.Contains(security.RevokedSerials, serial) {
		return ActiveCredential{}, fmt.Errorf("%w: endpoint certificate is revoked", ErrCredentialInvalid)
	}
	privatePublic, err := x509.MarshalPKIXPublicKey(&privateKey.PublicKey)
	if err != nil {
		return ActiveCredential{}, err
	}
	if !slices.Equal(privatePublic, leaf.RawSubjectPublicKeyInfo) {
		return ActiveCredential{}, fmt.Errorf("%w: private key does not match endpoint certificate", ErrCredentialInvalid)
	}
	fingerprint := sha256.Sum256(leaf.RawSubjectPublicKeyInfo)
	if !strings.EqualFold(hex.EncodeToString(fingerprint[:]), credential.PublicKeyFingerprint) {
		return ActiveCredential{}, fmt.Errorf("%w: public key fingerprint is inconsistent", ErrCredentialInvalid)
	}
	expectedURI := &url.URL{Scheme: "spiffe", Host: expectation.DomainID, Path: "/agent/" + expectation.AgentID}
	if expectation.Kind == model.PKIIdentityKindListener {
		expectedURI.Path += "/listener/" + expectation.ListenerID
	}
	if leaf.Subject.CommonName != expectedURI.String() || len(leaf.URIs) != 1 || leaf.URIs[0] == nil || leaf.URIs[0].String() != expectedURI.String() {
		return ActiveCredential{}, fmt.Errorf("%w: endpoint URI identity is inconsistent", ErrCredentialInvalid)
	}
	if !equalDNSNames(leaf.DNSNames, expectation.DNSNames) || !equalIPAddresses(leaf.IPAddresses, expectation.IPAddresses) {
		return ActiveCredential{}, fmt.Errorf("%w: endpoint SANs are inconsistent", ErrCredentialInvalid)
	}
	expectedUsage := x509.ExtKeyUsageClientAuth
	if expectation.Purpose == model.PKICertificatePurposeServer {
		expectedUsage = x509.ExtKeyUsageServerAuth
	}
	if !slices.Contains(leaf.ExtKeyUsage, expectedUsage) {
		return ActiveCredential{}, fmt.Errorf("%w: endpoint certificate EKU is invalid", ErrCredentialInvalid)
	}
	roots := x509.NewCertPool()
	rootByRaw := make(map[string]model.PKITrustRoot, len(security.TrustRoots))
	for _, root := range security.TrustRoots {
		certificate, parseErr := parseCertificatePEM(root.CertificatePEM)
		if parseErr != nil {
			return ActiveCredential{}, fmt.Errorf("%w: parse trust root", ErrCredentialInvalid)
		}
		roots.AddCert(certificate)
		rootByRaw[string(certificate.Raw)] = root
	}
	chains, err := leaf.Verify(x509.VerifyOptions{Roots: roots, CurrentTime: expectation.Now, KeyUsages: []x509.ExtKeyUsage{expectedUsage}})
	if err != nil {
		return ActiveCredential{}, fmt.Errorf("%w: endpoint chain verification failed: %v", ErrCredentialInvalid, err)
	}
	issuerMatches := false
	for _, chain := range chains {
		if len(chain) < 2 {
			continue
		}
		root, ok := rootByRaw[string(chain[len(chain)-1].Raw)]
		if ok && root.Generation == credential.CAGeneration && root.AuthorityID == credential.AuthorityID {
			issuerMatches = true
			break
		}
	}
	if !issuerMatches {
		return ActiveCredential{}, fmt.Errorf("%w: endpoint CA generation is inconsistent", ErrCredentialInvalid)
	}
	keyPair, err := tls.X509KeyPair([]byte(credential.CertificatePEM), privatePEM)
	if err != nil {
		return ActiveCredential{}, fmt.Errorf("%w: load TLS credential: %v", ErrCredentialInvalid, err)
	}
	keyPair.Leaf = leaf
	return ActiveCredential{TLSCertificate: keyPair, Leaf: leaf, Security: security}, nil
}

func equalDNSNames(actual, expected []string) bool {
	normalized, err := normalizeDNSNames(actual)
	return err == nil && slices.Equal(normalized, expected)
}

func equalIPAddresses(actual []net.IP, expected []string) bool {
	values := make([]string, 0, len(actual))
	for _, value := range actual {
		values = append(values, value.String())
	}
	normalized, err := normalizeIPAddresses(values)
	return err == nil && slices.Equal(normalized, expected)
}

func encodePrivateKey(privateKey *ecdsa.PrivateKey) ([]byte, error) {
	der, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), nil
}
