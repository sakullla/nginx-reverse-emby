package pki

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

// ActivateCredential validates and publishes a complete immutable credential
// generation, then atomically switches the single active pointer. The signed
// security snapshot is made durable first; an activation failure never moves
// the credential pointer or acknowledges the response.
func (s *Store) ActivateCredential(ctx context.Context, request ActivateRequest) (CredentialMetadata, error) {
	return s.activateCredential(ctx, request, false)
}

// ActivateRegistrationCredential is the explicit trust boundary for a
// response authenticated by a one-time registration flow (or the trusted
// embedded control-plane bridge). Unlike ordinary heartbeat activation, this
// path may consume a constrained emergency full trust reset.
func (s *Store) ActivateRegistrationCredential(ctx context.Context, request ActivateRequest) (CredentialMetadata, error) {
	return s.activateCredential(ctx, request, true)
}

func (s *Store) activateCredential(ctx context.Context, request ActivateRequest, registrationAuthorized bool) (CredentialMetadata, error) {
	if s == nil {
		return CredentialMetadata{}, errors.New("PKI store is required")
	}
	if err := ctx.Err(); err != nil {
		return CredentialMetadata{}, err
	}
	request.StorageIdentity = strings.TrimSpace(request.StorageIdentity)
	request.RequestID = strings.TrimSpace(request.RequestID)
	if request.StorageIdentity == "" || request.RequestID == "" {
		return CredentialMetadata{}, fmt.Errorf("%w: storage identity and request ID are required", ErrCredentialInvalid)
	}
	expectation, err := normalizeCredentialExpectation(request.Expectation)
	if err != nil {
		return CredentialMetadata{}, err
	}
	request.Expectation = expectation

	s.mu.Lock()
	defer s.mu.Unlock()
	validationTime := s.clock().UTC()
	if registrationAuthorized {
		current, hasCurrent, loadErr := s.registrationPreflightSecurityStateLocked()
		if loadErr != nil {
			return CredentialMetadata{}, loadErr
		}
		if hasCurrent {
			if _, continuityErr := validateSecuritySnapshot(request.Security, &current.Snapshot, validationTime); continuityErr != nil {
				reset, resetErr := validateRegistrationSecurityReset(request.Security, current.Snapshot, validationTime)
				if resetErr == nil {
					if err := s.rejectHistoricalRegistrationSignerReuseLocked(reset, current.Hash); err != nil {
						return CredentialMetadata{}, err
					}
					if err := s.preflightRegistrationTrustResetLocked(request, expectation, reset, validationTime); err != nil {
						return CredentialMetadata{}, err
					}
				}
			}
		}
	}
	securityState, err := s.applySecuritySnapshotLockedWithRegistration(request.Security, registrationAuthorized)
	if err != nil {
		return CredentialMetadata{}, err
	}
	if err := s.persistenceCheckpoint("credential.security_selected"); err != nil {
		return CredentialMetadata{}, err
	}
	if err := ctx.Err(); err != nil {
		return CredentialMetadata{}, err
	}
	identityRoot, err := s.identityRoot(request.StorageIdentity)
	if err != nil {
		return CredentialMetadata{}, err
	}
	pendingRoot := filepath.Join(identityRoot, pendingDirName)
	pendingDirectory, pending, err := s.openValidatedPending(request.StorageIdentity)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			classified := classifyPendingLoadError(pendingRoot, err)
			if !errors.Is(classified, ErrPendingNotFound) {
				return CredentialMetadata{}, classified
			}
			// A replay after a successful cutover is idempotent if the currently
			// active generation is the exact credential response.
			active, activeErr := s.loadActiveCredentialLocked(request.StorageIdentity)
			if activeErr == nil && active.metadata.Manifest.RequestID == request.RequestID &&
				credentialResponsesEqual(active.metadata.Manifest.Credential, request.Credential) &&
				expectationsEqual(active.metadata.Manifest.Expectation, expectation) {
				if expectation.Kind == model.PKIIdentityKindAgent {
					if _, ackErr := s.persistSecurityAcknowledgementLocked(request.StorageIdentity); ackErr != nil {
						return cloneCredentialMetadata(active.metadata), committedActivationError("acknowledgement reconciliation", ackErr)
					}
				}
				if cleanupErr := cleanupAbandonedPrivateTrees(identityRoot, pendingTombstonePattern.MatchString, map[string]struct{}{
					pendingJournalName: {}, pendingKeyName: {}, pendingCSRName: {}, "request-id": {}, "response.json": {},
				}); cleanupErr != nil {
					return cloneCredentialMetadata(active.metadata), committedActivationError("pending enrollment tombstone reconciliation", cleanupErr)
				}
				if syncErr := syncDirectory(identityRoot); syncErr != nil {
					return cloneCredentialMetadata(active.metadata), committedActivationError("identity directory reconciliation", syncErr)
				}
				return cloneCredentialMetadata(active.metadata), nil
			}
			return CredentialMetadata{}, ErrPendingNotFound
		}
		return CredentialMetadata{}, err
	}
	if pending.Request.RequestID != request.RequestID || pending.StorageIdentity != request.StorageIdentity {
		_ = pendingDirectory.Close()
		return CredentialMetadata{}, ErrPendingConflict
	}
	if err := bindExpectationToPending(pending, expectation); err != nil {
		_ = pendingDirectory.Close()
		return CredentialMetadata{}, err
	}
	privatePEM, err := readPrivateRootFile(pendingDirectory, pendingKeyName)
	if err != nil {
		_ = pendingDirectory.Close()
		return CredentialMetadata{}, err
	}
	if err := pendingDirectory.Close(); err != nil {
		return CredentialMetadata{}, err
	}
	allowRetiring, err := s.retiringCredentialReplayAllowedLocked(pending, request.Credential, securityState)
	if err != nil {
		return CredentialMetadata{}, err
	}
	validated, err := validateCredential(privatePEM, request.Credential, securityState.Snapshot, expectation, validationTime, allowRetiring)
	if err != nil {
		return CredentialMetadata{}, err
	}

	generationDigest := sha256.Sum256([]byte(request.RequestID + "\x00" + request.Credential.CertificateID + "\x00" + request.Credential.PublicKeyFingerprint + "\x00" + securityState.Hash))
	generation := fmt.Sprintf("g%d-%s", request.Credential.CAGeneration, hex.EncodeToString(generationDigest[:12]))
	manifest := CredentialManifest{
		Version: 1, Generation: generation, RequestID: request.RequestID,
		RequestFingerprint: pending.RequestFingerprint, Credential: request.Credential,
		PKIDomainID: securityState.Snapshot.PKIDomainID, PKIEpoch: securityState.Snapshot.PKIEpoch,
		SecurityRevision: securityState.Snapshot.SecurityRevision, SecuritySnapshotHash: securityState.Hash,
		Expectation: expectation, ActivatedAt: validationTime,
	}
	manifestEncoded, err := json.Marshal(manifest)
	if err != nil {
		return CredentialMetadata{}, err
	}
	manifestHash := sha256Hex(manifestEncoded)
	generationsRoot := filepath.Join(identityRoot, generationsDirName)
	if err := ensureDurablePrivateSubdir(identityRoot, generationsDirName, s.random); err != nil {
		return CredentialMetadata{}, err
	}
	generationRoot := filepath.Join(generationsRoot, generation)
	if info, statErr := os.Lstat(generationRoot); statErr == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return CredentialMetadata{}, fmt.Errorf("%w: immutable generation path is unsafe", ErrCredentialInvalid)
		}
		existingManifest, readErr := readPrivateFile(filepath.Join(generationRoot, manifestName))
		if readErr != nil {
			return CredentialMetadata{}, readErr
		}
		// ActivatedAt is not part of the server response. Preserve the first
		// immutable generation rather than manufacturing a second variant.
		var existing CredentialManifest
		if decodeStrictJSON(existingManifest, &existing) != nil || existing.ActivatedAt.IsZero() || !credentialManifestsEquivalent(existing, manifest) {
			return CredentialMetadata{}, fmt.Errorf("%w: immutable generation collision", ErrCredentialInvalid)
		}
		if err := validatePublishedGeneration(generationRoot, existing, privatePEM, request.Credential.CertificatePEM, securityState); err != nil {
			return CredentialMetadata{}, err
		}
		manifest = existing
		manifestEncoded = existingManifest
		manifestHash = sha256Hex(existingManifest)
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return CredentialMetadata{}, statErr
	} else if err := s.publishCredentialGeneration(generationsRoot, generationRoot, manifestEncoded, privatePEM, request.Credential.CertificatePEM, securityState); err != nil {
		return CredentialMetadata{}, err
	}
	if err := syncDirectory(generationRoot); err != nil {
		return CredentialMetadata{}, err
	}
	if err := syncDirectory(generationsRoot); err != nil {
		return CredentialMetadata{}, err
	}
	if err := s.persistenceCheckpoint("credential.after_generation_publish"); err != nil {
		return CredentialMetadata{}, err
	}
	validated.metadata = CredentialMetadata{Manifest: manifest, Security: securityState.Snapshot}
	pointer := ActivePointer{Version: 1, Generation: generation, ManifestHash: manifestHash, ActivatedAt: manifest.ActivatedAt}
	if _, err := writeAtomicPrivateJSON(identityRoot, activePointerName, pointer, s.random); err != nil {
		if active, loadErr := s.loadActiveCredentialLocked(request.StorageIdentity); loadErr == nil && active.metadata.Manifest.Generation == generation {
			return cloneCredentialMetadata(active.metadata), committedActivationError("active pointer publication", err)
		}
		return CredentialMetadata{}, fmt.Errorf("activate credential generation: %w", err)
	}
	if err := s.persistenceCheckpoint("credential.after_pointer_publish"); err != nil {
		return cloneCredentialMetadata(validated.metadata), committedActivationError("active pointer publication", err)
	}
	if expectation.Kind == model.PKIIdentityKindAgent {
		if _, err := s.persistSecurityAcknowledgementLocked(request.StorageIdentity); err != nil {
			return cloneCredentialMetadata(validated.metadata), committedActivationError("security acknowledgement", err)
		}
	}
	// The complete pending directory first becomes a tombstone. Once that
	// rename and its parent barrier commit, interruption during recursive
	// cleanup can no longer manufacture a corrupt replayable pending request.
	if err := s.tombstoneCommittedPendingLocked(identityRoot, pendingRoot, pending.Request.RequestID); err != nil {
		return cloneCredentialMetadata(validated.metadata), committedActivationError("pending enrollment cleanup", err)
	}
	if err := syncDirectory(identityRoot); err != nil {
		return cloneCredentialMetadata(validated.metadata), committedActivationError("identity directory sync", err)
	}
	return cloneCredentialMetadata(validated.metadata), nil
}

func (s *Store) registrationPreflightSecurityStateLocked() (SecurityState, bool, error) {
	current, err := s.loadSecurityStateLocked()
	if err == nil {
		return current, true, nil
	}
	securityRoot := filepath.Join(s.root, securityDirName)
	pointerPath := filepath.Join(securityRoot, activePointerName)
	if _, pointerErr := os.Lstat(pointerPath); pointerErr == nil {
		return SecurityState{}, false, fmt.Errorf("%w: active security state is corrupt: %v", ErrSecurityInvalid, err)
	} else if !errors.Is(pointerErr, os.ErrNotExist) {
		return SecurityState{}, false, pointerErr
	}
	recovered, hasTrace, recoverErr := s.latestRecoverableSecurityStateLocked()
	if recoverErr != nil {
		return SecurityState{}, false, recoverErr
	}
	if hasTrace {
		return recovered, true, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return SecurityState{}, false, err
	}
	return SecurityState{}, false, nil
}

func (s *Store) rejectHistoricalRegistrationSignerReuseLocked(snapshot model.PKISecuritySnapshot, currentHash string) error {
	if len(snapshot.TrustRoots) != 1 {
		return nil
	}
	candidate, err := parseCertificatePEM(snapshot.TrustRoots[0].CertificatePEM)
	if err != nil {
		return fmt.Errorf("%w: registration trust reset signer is invalid", ErrSecurityInvalid)
	}
	states, err := s.validatedSecurityHistoryLocked(currentHash)
	if err != nil {
		return err
	}
	for _, state := range states {
		for _, root := range state.Snapshot.TrustRoots {
			historical, err := parseCertificatePEM(root.CertificatePEM)
			if err != nil {
				return fmt.Errorf("%w: historical trust root is invalid", ErrSecurityInvalid)
			}
			if bytes.Equal(candidate.Raw, historical.Raw) || bytes.Equal(candidate.RawSubjectPublicKeyInfo, historical.RawSubjectPublicKeyInfo) {
				return fmt.Errorf("%w: registration trust reset reuses a historical signer", ErrSecurityInvalid)
			}
		}
	}
	return nil
}

func (s *Store) preflightRegistrationTrustResetLocked(request ActivateRequest, expectation CredentialExpectation, security model.PKISecuritySnapshot, validationTime time.Time) error {
	pendingDirectory, pending, err := s.openValidatedPending(request.StorageIdentity)
	if err != nil {
		identityRoot, identityErr := s.identityRoot(request.StorageIdentity)
		if identityErr != nil {
			return identityErr
		}
		return classifyPendingLoadError(filepath.Join(identityRoot, pendingDirName), err)
	}
	defer pendingDirectory.Close()
	if pending.Request.RequestID != request.RequestID || pending.StorageIdentity != request.StorageIdentity {
		return ErrPendingConflict
	}
	if err := bindExpectationToPending(pending, expectation); err != nil {
		return err
	}
	privatePEM, err := readPrivateRootFile(pendingDirectory, pendingKeyName)
	if err != nil {
		return err
	}
	if _, err := validateCredential(privatePEM, request.Credential, security, expectation, validationTime, false); err != nil {
		return fmt.Errorf("preflight registration trust reset credential: %w", err)
	}
	return nil
}

// ActivateStagedRegistration consumes the sanitized response produced by
// join-agent.sh. It derives the server-bound agent expectation from the signed
// snapshot and the stable agent ID, then follows the same validated generation
// activation path as an in-process control response.
func (s *Store) ActivateStagedRegistration(ctx context.Context, storageIdentity string) (CredentialMetadata, error) {
	staged, pending, err := s.LoadStagedRegistration(storageIdentity)
	if err != nil {
		return CredentialMetadata{}, err
	}
	return s.ActivateRegistrationCredential(ctx, ActivateRequest{
		StorageIdentity: storageIdentity,
		RequestID:       pending.Request.RequestID,
		Credential:      staged.TunnelCredential,
		Security:        staged.SecuritySnapshot,
		Expectation: CredentialExpectation{
			DomainID: staged.SecuritySnapshot.PKIDomainID, AgentID: staged.AgentID,
			Kind: pending.Request.Kind, ListenerID: pending.Request.ListenerID,
			Purpose: pending.Request.Purpose, DNSNames: pending.Request.DNSNames,
			IPAddresses: pending.Request.IPAddresses,
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
	pendingDirectory, pending, err := s.openValidatedPending(storageIdentity)
	if err != nil {
		return StagedRegistration{}, PendingEnrollment{}, classifyPendingLoadError(pendingRoot, err)
	}
	defer pendingDirectory.Close()
	encoded, err := readPrivateRootFile(pendingDirectory, "response.json")
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return StagedRegistration{}, PendingEnrollment{}, ErrStagedRegistrationNotFound
		}
		return StagedRegistration{}, PendingEnrollment{}, err
	}
	var staged StagedRegistration
	if err := decodeStrictJSON(encoded, &staged); err != nil {
		return StagedRegistration{}, PendingEnrollment{}, fmt.Errorf("%w: decode staged PKI registration: %v", ErrCredentialInvalid, err)
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
	if err := publishDirectory(temporaryRoot, generationRoot); err != nil {
		return err
	}
	cleanup = false
	return syncDirectory(generationsRoot)
}

func (s *Store) tombstoneCommittedPendingLocked(identityRoot, pendingRoot, requestID string) error {
	suffix, err := randomHex(s.random, 8)
	if err != nil {
		return err
	}
	tombstone := filepath.Join(identityRoot, ".pending-tombstone-"+requestID+"-"+suffix)
	if err := publishDirectory(pendingRoot, tombstone); err != nil {
		return err
	}
	if err := syncDirectory(identityRoot); err != nil {
		return err
	}
	if err := s.persistenceCheckpoint("credential.after_pending_tombstone_publish"); err != nil {
		return err
	}
	if err := os.RemoveAll(tombstone); err != nil {
		return err
	}
	if err := s.persistenceCheckpoint("credential.after_pending_remove"); err != nil {
		return err
	}
	return syncDirectory(identityRoot)
}

func validatePublishedGeneration(root string, manifest CredentialManifest, privateKey []byte, certificatePEM string, security SecurityState) error {
	storedKey, err := readPrivateFile(filepath.Join(root, privateKeyName))
	if err != nil || !slices.Equal(storedKey, privateKey) {
		return fmt.Errorf("%w: immutable generation private key is inconsistent", ErrCredentialInvalid)
	}
	storedCertificate, err := readPrivateFile(filepath.Join(root, certificateName))
	if err != nil || strings.TrimSpace(string(storedCertificate)) != strings.TrimSpace(certificatePEM) {
		return fmt.Errorf("%w: immutable generation certificate is inconsistent", ErrCredentialInvalid)
	}
	storedSecurity, err := readPrivateFile(filepath.Join(root, securityName))
	if err != nil {
		return fmt.Errorf("%w: immutable generation security state is missing", ErrCredentialInvalid)
	}
	var decodedSecurity SecurityState
	if decodeStrictJSON(storedSecurity, &decodedSecurity) != nil || !reflect.DeepEqual(decodedSecurity, security) {
		return fmt.Errorf("%w: immutable generation security state is inconsistent", ErrCredentialInvalid)
	}
	if manifest.SecuritySnapshotHash != security.Hash {
		return fmt.Errorf("%w: immutable generation manifest is inconsistent", ErrCredentialInvalid)
	}
	return nil
}

func (s *Store) LoadActiveCredential(storageIdentity string) (CredentialMetadata, error) {
	if s == nil {
		return CredentialMetadata{}, errors.New("PKI store is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	active, err := s.loadActiveCredentialLocked(storageIdentity)
	if errors.Is(err, os.ErrNotExist) {
		return CredentialMetadata{}, ErrActiveCredential
	}
	if err != nil {
		return CredentialMetadata{}, err
	}
	return cloneCredentialMetadata(active.metadata), nil
}

// InstallTLSCertificate installs the key-bearing generation directly into a
// relay-owned tls.Config. Private-key material is captured by the appropriate
// TLS callback and is never returned through the public metadata API.
func (s *Store) InstallTLSCertificate(storageIdentity string, config *tls.Config) (CredentialMetadata, error) {
	if s == nil || config == nil {
		return CredentialMetadata{}, errors.New("PKI store and TLS config are required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	active, err := s.loadActiveCredentialLocked(storageIdentity)
	if errors.Is(err, os.ErrNotExist) {
		return CredentialMetadata{}, ErrActiveCredential
	}
	if err != nil {
		return CredentialMetadata{}, err
	}
	certificate := active.tlsCertificate
	config.Certificates = nil
	config.GetClientCertificate = nil
	config.GetCertificate = nil
	switch active.metadata.Manifest.Expectation.Purpose {
	case model.PKICertificatePurposeClient:
		config.GetClientCertificate = func(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
			copyValue := certificate
			return &copyValue, nil
		}
	case model.PKICertificatePurposeServer:
		config.GetCertificate = func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
			copyValue := certificate
			return &copyValue, nil
		}
	default:
		return CredentialMetadata{}, fmt.Errorf("%w: active credential purpose is unsupported", ErrActiveCredential)
	}
	return cloneCredentialMetadata(active.metadata), nil
}

func (s *Store) loadActiveCredentialLocked(storageIdentity string) (activeCredential, error) {
	identityRoot, err := s.identityRoot(storageIdentity)
	if err != nil {
		return activeCredential{}, err
	}
	pointerEncoded, err := readPrivateFile(filepath.Join(identityRoot, activePointerName))
	if err != nil {
		return activeCredential{}, err
	}
	var pointer ActivePointer
	if err := decodeStrictJSON(pointerEncoded, &pointer); err != nil {
		return activeCredential{}, fmt.Errorf("decode active credential pointer: %w", err)
	}
	if pointer.Version != 1 || !safeIdentityPattern.MatchString(pointer.Generation) || len(pointer.ManifestHash) != sha256.Size*2 ||
		!validLowerHex(pointer.ManifestHash) || pointer.ActivatedAt.IsZero() {
		return activeCredential{}, fmt.Errorf("%w: active credential pointer is invalid", ErrActiveCredential)
	}
	generationRoot := filepath.Join(identityRoot, generationsDirName, pointer.Generation)
	manifestEncoded, err := readPrivateFile(filepath.Join(generationRoot, manifestName))
	if err != nil {
		return activeCredential{}, err
	}
	var manifest CredentialManifest
	if err := decodeStrictJSON(manifestEncoded, &manifest); err != nil {
		return activeCredential{}, fmt.Errorf("decode credential manifest: %w", err)
	}
	if manifest.Version != 1 || manifest.Generation != pointer.Generation || sha256Hex(manifestEncoded) != pointer.ManifestHash ||
		!manifest.ActivatedAt.Equal(pointer.ActivatedAt) || len(manifest.SecuritySnapshotHash) != sha256.Size*2 ||
		!validLowerHex(manifest.SecuritySnapshotHash) {
		return activeCredential{}, fmt.Errorf("%w: active credential manifest is inconsistent", ErrActiveCredential)
	}
	privatePEM, err := readPrivateFile(filepath.Join(generationRoot, privateKeyName))
	if err != nil {
		return activeCredential{}, err
	}
	certificatePEM, err := readPrivateFile(filepath.Join(generationRoot, certificateName))
	if err != nil {
		return activeCredential{}, err
	}
	if strings.TrimSpace(string(certificatePEM)) != strings.TrimSpace(manifest.Credential.CertificatePEM) {
		return activeCredential{}, fmt.Errorf("%w: active certificate differs from manifest", ErrActiveCredential)
	}
	storedSecurity, err := readPrivateFile(filepath.Join(generationRoot, securityName))
	if err != nil {
		return activeCredential{}, err
	}
	var generationSecurity SecurityState
	if err := decodeStrictJSON(storedSecurity, &generationSecurity); err != nil || generationSecurity.Hash != manifest.SecuritySnapshotHash ||
		generationSecurity.Snapshot.PKIDomainID != manifest.PKIDomainID || generationSecurity.Snapshot.PKIEpoch != manifest.PKIEpoch ||
		generationSecurity.Snapshot.SecurityRevision != manifest.SecurityRevision {
		return activeCredential{}, fmt.Errorf("%w: active generation security binding is inconsistent", ErrActiveCredential)
	}
	security, err := s.loadSecurityStateLocked()
	if err != nil {
		return activeCredential{}, err
	}
	if manifest.PKIDomainID != security.Snapshot.PKIDomainID || manifest.PKIEpoch > security.Snapshot.PKIEpoch ||
		(manifest.PKIEpoch == security.Snapshot.PKIEpoch && manifest.SecurityRevision > security.Snapshot.SecurityRevision) {
		return activeCredential{}, fmt.Errorf("%w: active credential security generation is unavailable", ErrActiveCredential)
	}
	expectation := manifest.Expectation
	active, err := validateCredential(privatePEM, manifest.Credential, security.Snapshot, expectation, s.clock().UTC(), true)
	if err != nil {
		return activeCredential{}, err
	}
	active.metadata = CredentialMetadata{Manifest: manifest, Security: security.Snapshot}
	return active, nil
}

func (s *Store) persistSecurityAcknowledgementLocked(storageIdentity string) (model.PKISecurityAcknowledgement, error) {
	active, err := s.loadActiveCredentialLocked(storageIdentity)
	if errors.Is(err, os.ErrNotExist) {
		return model.PKISecurityAcknowledgement{}, ErrActiveCredential
	}
	if err != nil {
		return model.PKISecurityAcknowledgement{}, err
	}
	if active.metadata.Manifest.Expectation.Kind != model.PKIIdentityKindAgent {
		return model.PKISecurityAcknowledgement{}, fmt.Errorf("%w: security acknowledgement requires the active agent identity", ErrActiveCredential)
	}
	acknowledgement := securityAcknowledgement(
		active.metadata.Security,
		active.metadata.Manifest.Credential.CertificateID,
	)
	securityRoot := filepath.Join(s.root, securityDirName)
	if s.ackNeedsSync {
		if err := syncDirectory(securityRoot); err != nil {
			return model.PKISecurityAcknowledgement{}, err
		}
		s.ackNeedsSync = false
	}
	acknowledgementPath := filepath.Join(securityRoot, acknowledgementName)
	if encoded, readErr := readPrivateFile(acknowledgementPath); readErr == nil {
		var existing model.PKISecurityAcknowledgement
		if decodeErr := decodeStrictJSON(encoded, &existing); decodeErr != nil {
			return model.PKISecurityAcknowledgement{}, fmt.Errorf("%w: decode durable security acknowledgement: %v", ErrSecurityInvalid, decodeErr)
		}
		existing, validateErr := validateSecurityAcknowledgement(existing)
		if validateErr != nil {
			return model.PKISecurityAcknowledgement{}, validateErr
		}
		if existing.PKIDomainID != acknowledgement.PKIDomainID {
			return model.PKISecurityAcknowledgement{}, fmt.Errorf("%w: active security domain conflicts with the durable acknowledgement", ErrSecurityInvalid)
		}
		versionOrder := compareSecurityVersion(
			acknowledgement.PKIEpoch, acknowledgement.SecurityRevision,
			existing.PKIEpoch, existing.SecurityRevision,
		)
		if versionOrder < 0 {
			return model.PKISecurityAcknowledgement{}, fmt.Errorf("%w: active security state is below the durable acknowledgement", ErrSecurityDowngrade)
		}
		if versionOrder == 0 && (existing.PKIDomainID != acknowledgement.PKIDomainID || existing.Full != acknowledgement.Full ||
			!slices.Equal(existing.TrustGenerations, acknowledgement.TrustGenerations)) {
			return model.PKISecurityAcknowledgement{}, fmt.Errorf("%w: active security state conflicts with the durable acknowledgement", ErrSecurityInvalid)
		}
		if reflect.DeepEqual(existing, acknowledgement) {
			return acknowledgement, nil
		}
	} else if !errors.Is(readErr, os.ErrNotExist) {
		return model.PKISecurityAcknowledgement{}, readErr
	}
	s.ackNeedsSync = true
	if _, err := writeAtomicPrivateJSON(securityRoot, acknowledgementName, acknowledgement, s.random); err != nil {
		return model.PKISecurityAcknowledgement{}, fmt.Errorf("persist durable security acknowledgement: %w", err)
	}
	s.ackNeedsSync = false
	if err := s.persistenceCheckpoint("credential.after_ack_publish"); err != nil {
		return model.PKISecurityAcknowledgement{}, err
	}
	return acknowledgement, nil
}

func bindExpectationToPending(pending PendingEnrollment, expectation CredentialExpectation) error {
	if pending.DomainID != "" || pending.AgentID != "" {
		if pending.DomainID != expectation.DomainID || pending.AgentID != expectation.AgentID {
			return fmt.Errorf("%w: credential owner differs from durable enrollment", ErrPendingConflict)
		}
	} else if expectation.DomainID == "" || expectation.AgentID == "" {
		return fmt.Errorf("%w: anonymous enrollment was not bound by the server", ErrPendingConflict)
	}
	if pending.Request.Kind != expectation.Kind || pending.Request.ListenerID != expectation.ListenerID ||
		pending.Request.Purpose != expectation.Purpose || !slices.Equal(pending.Request.DNSNames, expectation.DNSNames) ||
		!slices.Equal(pending.Request.IPAddresses, expectation.IPAddresses) {
		return fmt.Errorf("%w: credential shape differs from durable enrollment", ErrPendingConflict)
	}
	return nil
}

func credentialResponsesEqual(left, right model.PKITunnelCredential) bool {
	return reflect.DeepEqual(left, right)
}

func expectationsEqual(left, right CredentialExpectation) bool {
	return left.DomainID == right.DomainID && left.AgentID == right.AgentID && left.Kind == right.Kind &&
		left.ListenerID == right.ListenerID && left.Purpose == right.Purpose &&
		slices.Equal(left.DNSNames, right.DNSNames) && slices.Equal(left.IPAddresses, right.IPAddresses)
}

func credentialManifestsEquivalent(existing, candidate CredentialManifest) bool {
	return existing.Version == candidate.Version && existing.Generation == candidate.Generation &&
		existing.RequestID == candidate.RequestID && existing.RequestFingerprint == candidate.RequestFingerprint &&
		credentialResponsesEqual(existing.Credential, candidate.Credential) && existing.PKIDomainID == candidate.PKIDomainID &&
		existing.PKIEpoch == candidate.PKIEpoch && existing.SecurityRevision == candidate.SecurityRevision &&
		existing.SecuritySnapshotHash == candidate.SecuritySnapshotHash && expectationsEqual(existing.Expectation, candidate.Expectation)
}

func cloneCredentialMetadata(metadata CredentialMetadata) CredentialMetadata {
	cloned := metadata
	cloned.Manifest.Expectation.DNSNames = slices.Clone(metadata.Manifest.Expectation.DNSNames)
	cloned.Manifest.Expectation.IPAddresses = slices.Clone(metadata.Manifest.Expectation.IPAddresses)
	cloned.Security = cloneSecuritySnapshot(metadata.Security)
	return cloned
}

func committedActivationError(stage string, cause error) error {
	return &ActivationCommittedError{Stage: stage, Cause: cause}
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
	return expectation, nil
}

func validateCredential(privatePEM []byte, credential model.PKITunnelCredential, security model.PKISecuritySnapshot, expectation CredentialExpectation, now time.Time, allowRetiring bool) (activeCredential, error) {
	if credential.IdentityID == "" || credential.CertificateID == "" || credential.AuthorityID == "" || credential.CAGeneration <= 0 ||
		credential.Purpose != expectation.Purpose || credential.NotBefore.IsZero() || credential.NotAfter.IsZero() ||
		security.PKIDomainID != expectation.DomainID {
		return activeCredential{}, credentialInvalidf(CredentialInvalidProfile, "credential metadata is incomplete")
	}
	if strings.TrimSpace(credential.IdentityID) != credential.IdentityID || strings.TrimSpace(credential.CertificateID) != credential.CertificateID ||
		strings.TrimSpace(credential.AuthorityID) != credential.AuthorityID || len(credential.PublicKeyFingerprint) != sha256.Size*2 ||
		!validLowerHex(credential.PublicKeyFingerprint) {
		return activeCredential{}, credentialInvalidf(CredentialInvalidProfile, "credential identifiers are not canonical")
	}
	if slices.Contains(security.RevokedIdentityIDs, credential.IdentityID) {
		return activeCredential{}, credentialInvalidf(CredentialInvalidRevokedIdentity, "identity is revoked")
	}
	privateKey, err := parseECPrivateKeyPEM(privatePEM)
	if err != nil {
		return activeCredential{}, credentialInvalidf(CredentialInvalidProfile, "private key is invalid: %v", err)
	}
	leaf, err := parseCertificatePEM(credential.CertificatePEM)
	if err != nil {
		return activeCredential{}, credentialInvalidf(CredentialInvalidProfile, "parse endpoint certificate: %v", err)
	}
	if leaf.IsCA || !leaf.BasicConstraintsValid {
		return activeCredential{}, credentialInvalidf(CredentialInvalidProfile, "endpoint certificate basic constraints are invalid")
	}
	if now.Before(leaf.NotBefore) {
		return activeCredential{}, credentialInvalidf(CredentialInvalidNotYetValid, "endpoint certificate is not yet valid")
	}
	if now.After(leaf.NotAfter) {
		return activeCredential{}, credentialInvalidf(CredentialInvalidExpired, "endpoint certificate is expired")
	}
	if !leaf.NotBefore.Equal(credential.NotBefore) || !leaf.NotAfter.Equal(credential.NotAfter) {
		return activeCredential{}, credentialInvalidf(CredentialInvalidProfile, "endpoint certificate lifetime metadata is inconsistent")
	}
	publicKey, ok := leaf.PublicKey.(*ecdsa.PublicKey)
	if !ok || publicKey.Curve != elliptic.P256() || leaf.SignatureAlgorithm != x509.ECDSAWithSHA256 ||
		leaf.SerialNumber == nil || leaf.SerialNumber.Sign() <= 0 || leaf.SerialNumber.BitLen() < 128 || leaf.SerialNumber.BitLen() > 159 ||
		leaf.KeyUsage != x509.KeyUsageDigitalSignature {
		return activeCredential{}, credentialInvalidf(CredentialInvalidProfile, "endpoint certificate cryptographic profile is invalid")
	}
	serial := strings.ToLower(leaf.SerialNumber.Text(16))
	if slices.Contains(security.RevokedSerials, serial) {
		return activeCredential{}, credentialInvalidf(CredentialInvalidRevokedSerial, "endpoint certificate is revoked")
	}
	privatePublic, err := x509.MarshalPKIXPublicKey(&privateKey.PublicKey)
	if err != nil {
		return activeCredential{}, credentialInvalidf(CredentialInvalidProfile, "marshal private public key: %v", err)
	}
	if !slices.Equal(privatePublic, leaf.RawSubjectPublicKeyInfo) {
		return activeCredential{}, credentialInvalidf(CredentialInvalidProfile, "private key does not match endpoint certificate")
	}
	fingerprint := sha256.Sum256(leaf.RawSubjectPublicKeyInfo)
	if !constantTimeHexEqual(hex.EncodeToString(fingerprint[:]), credential.PublicKeyFingerprint) {
		return activeCredential{}, credentialInvalidf(CredentialInvalidProfile, "public key fingerprint is inconsistent")
	}
	expectedURI := &url.URL{Scheme: "spiffe", Host: expectation.DomainID, Path: "/agent/" + expectation.AgentID}
	if expectation.Kind == model.PKIIdentityKindListener {
		expectedURI.Path += "/listener/" + expectation.ListenerID
	}
	if !pkixNameMatchesOnlyCommonName(leaf.Subject, expectedURI.String()) || len(leaf.URIs) != 1 || leaf.URIs[0] == nil || leaf.URIs[0].String() != expectedURI.String() {
		return activeCredential{}, credentialInvalidf(CredentialInvalidProfile, "endpoint URI identity is inconsistent")
	}
	if len(leaf.EmailAddresses) != 0 || len(leaf.DNSNames) != len(expectation.DNSNames) || len(leaf.IPAddresses) != len(expectation.IPAddresses) ||
		!equalDNSNames(leaf.DNSNames, expectation.DNSNames) || !equalIPAddresses(leaf.IPAddresses, expectation.IPAddresses) {
		return activeCredential{}, credentialInvalidf(CredentialInvalidProfile, "endpoint SANs are inconsistent")
	}
	if err := validateSubjectAlternativeNameShape(leaf.Extensions, len(expectation.DNSNames), len(expectation.IPAddresses), 1); err != nil {
		return activeCredential{}, credentialInvalidf(CredentialInvalidProfile, "endpoint SAN shape is invalid: %v", err)
	}
	expectedUsage := x509.ExtKeyUsageClientAuth
	if expectation.Purpose == model.PKICertificatePurposeServer {
		expectedUsage = x509.ExtKeyUsageServerAuth
	}
	if len(leaf.ExtKeyUsage) != 1 || leaf.ExtKeyUsage[0] != expectedUsage || len(leaf.UnknownExtKeyUsage) != 0 {
		return activeCredential{}, credentialInvalidf(CredentialInvalidProfile, "endpoint certificate EKU is invalid")
	}
	roots := x509.NewCertPool()
	rootByRaw := make(map[string]model.PKITrustRoot, len(security.TrustRoots))
	issuerMetadataAvailable := false
	issuerLifecycleAllowed := false
	for _, root := range security.TrustRoots {
		certificate, parseErr := parseCertificatePEM(root.CertificatePEM)
		if parseErr != nil {
			return activeCredential{}, credentialInvalidf(CredentialInvalidProfile, "parse trust root: %v", parseErr)
		}
		roots.AddCert(certificate)
		rootByRaw[string(certificate.Raw)] = root
		if root.Generation == credential.CAGeneration && root.AuthorityID == credential.AuthorityID {
			issuerMetadataAvailable = true
			issuerLifecycleAllowed = credentialIssuerLifecycleAllowed(root.Status, allowRetiring)
		}
	}
	if !issuerMetadataAvailable || !issuerLifecycleAllowed {
		return activeCredential{}, credentialInvalidf(CredentialInvalidSignerLifecycle, "endpoint CA generation is unavailable in the allowed lifecycle")
	}
	chains, err := leaf.Verify(x509.VerifyOptions{Roots: roots, CurrentTime: now, KeyUsages: []x509.ExtKeyUsage{expectedUsage}})
	if err != nil {
		return activeCredential{}, credentialInvalidf(CredentialInvalidProfile, "endpoint chain verification failed: %v", err)
	}
	issuerMatches := false
	for _, chain := range chains {
		if len(chain) < 2 {
			continue
		}
		root, ok := rootByRaw[string(chain[len(chain)-1].Raw)]
		statusAllowed := ok && credentialIssuerLifecycleAllowed(root.Status, allowRetiring)
		if statusAllowed && root.Generation == credential.CAGeneration && root.AuthorityID == credential.AuthorityID {
			issuerMatches = true
			break
		}
	}
	if !issuerMatches {
		return activeCredential{}, credentialInvalidf(CredentialInvalidSignerLifecycle, "endpoint CA generation is unavailable in the allowed lifecycle")
	}
	keyPair, err := tls.X509KeyPair([]byte(credential.CertificatePEM), privatePEM)
	if err != nil {
		return activeCredential{}, credentialInvalidf(CredentialInvalidProfile, "load TLS credential: %v", err)
	}
	keyPair.Leaf = leaf
	return activeCredential{tlsCertificate: keyPair, leaf: leaf}, nil
}

// A prepared authority is already part of the signed dual-trust set and is
// therefore usable for endpoint reissue before it becomes the snapshot
// signer. Retiring authorities remain restricted to response-loss recovery.
func credentialIssuerLifecycleAllowed(status string, allowRetiring bool) bool {
	return status == "active" || status == "prepared" || allowRetiring && status == "retiring"
}

func (s *Store) retiringCredentialReplayAllowedLocked(pending PendingEnrollment, credential model.PKITunnelCredential, security SecurityState) (bool, error) {
	if pending.CreatedAt.IsZero() || credential.NotBefore.IsZero() {
		return false, nil
	}
	retiring := false
	for _, root := range security.Snapshot.TrustRoots {
		if root.Generation == credential.CAGeneration && root.AuthorityID == credential.AuthorityID {
			retiring = root.Status == "retiring"
			break
		}
	}
	if !retiring {
		return false, nil
	}
	states, err := s.validatedSecurityHistoryLocked(security.Hash)
	if err != nil {
		return false, err
	}
	for _, state := range states {
		for _, root := range state.Snapshot.TrustRoots {
			if root.Generation == credential.CAGeneration && root.AuthorityID == credential.AuthorityID && root.Status == "retiring" {
				cutover := state.Snapshot.IssuedAt
				return !pending.CreatedAt.After(cutover) && !credential.NotBefore.After(cutover), nil
			}
		}
	}
	return false, fmt.Errorf("%w: retiring signer transition is unavailable", ErrSecurityInvalid)
}

func credentialInvalidf(reason CredentialInvalidReason, format string, arguments ...any) error {
	return &CredentialInvalidError{Reason: reason, Detail: fmt.Sprintf(format, arguments...)}
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
