package pki

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"crypto/x509"
	"encoding/asn1"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

type securityPointer struct {
	Version     int       `json:"version"`
	File        string    `json:"file"`
	Hash        string    `json:"sha256"`
	ActivatedAt time.Time `json:"activated_at"`
}

type securityVersion struct {
	PKIEpoch         int64 `json:"pki_epoch"`
	SecurityRevision int64 `json:"security_revision"`
}

type securitySnapshotVersion struct {
	Version securityVersion `json:"version"`
	Full    bool            `json:"full"`
}

type securityTrustDescriptor struct {
	AuthorityID       string    `json:"authority_id"`
	Generation        int64     `json:"generation"`
	Status            string    `json:"status"`
	FingerprintSHA256 string    `json:"fingerprint_sha256"`
	NotBefore         time.Time `json:"not_before"`
	NotAfter          time.Time `json:"not_after"`
}

type securitySignaturePayload struct {
	PKIDomainID        string                    `json:"pki_domain_id"`
	Version            securitySnapshotVersion   `json:"version"`
	IssuedAt           time.Time                 `json:"issued_at"`
	TrustGenerations   []int64                   `json:"trust_generations"`
	TrustRoots         []securityTrustDescriptor `json:"trust_roots"`
	RevokedIdentityIDs []string                  `json:"revoked_identity_ids"`
	RevokedSerials     []string                  `json:"revoked_serials"`
}

var securityStateFilePattern = regexp.MustCompile(`^(0|[1-9][0-9]*)-(0|[1-9][0-9]*)-[0-9a-f]{16}\.json$`)

// ApplySecuritySnapshot verifies monotonicity, signature, public trust
// metadata, and bootstrap continuity before atomically advancing the durable
// active pointer. A byte-identical replay is idempotent.
func (s *Store) ApplySecuritySnapshot(snapshot model.PKISecuritySnapshot) (SecurityState, error) {
	if s == nil {
		return SecurityState{}, errors.New("PKI store is required")
	}
	if err := s.persistenceCheckpoint("security.before_lock"); err != nil {
		return SecurityState{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.persistenceCheckpoint("security.lock_acquired"); err != nil {
		return SecurityState{}, err
	}
	return s.applySecuritySnapshotLocked(snapshot)
}

func (s *Store) applySecuritySnapshotLocked(snapshot model.PKISecuritySnapshot) (SecurityState, error) {
	var previous *model.PKISecuritySnapshot
	current, err := s.loadSecurityStateLocked()
	if err == nil {
		previous = &current.Snapshot
	} else {
		securityRoot := filepath.Join(s.root, securityDirName)
		pointerPath := filepath.Join(securityRoot, activePointerName)
		_, pointerErr := os.Lstat(pointerPath)
		if pointerErr == nil {
			// Once a pointer exists, a missing/corrupt target is initialized-store
			// corruption, not a fresh bootstrap. Never let a self-signed candidate
			// reset the trust domain in this state.
			return SecurityState{}, fmt.Errorf("%w: active security state is corrupt: %v", ErrSecurityInvalid, err)
		}
		if !errors.Is(pointerErr, os.ErrNotExist) {
			return SecurityState{}, pointerErr
		}
		recovered, hasTrace, recoverErr := s.latestRecoverableSecurityStateLocked()
		if recoverErr != nil {
			return SecurityState{}, recoverErr
		}
		if hasTrace {
			if err := s.publishSecurityPointerLocked(recovered); err != nil {
				if errors.Is(err, ErrActivationCommitted) {
					return cloneSecurityState(recovered), err
				}
				return SecurityState{}, err
			}
			current = recovered
			previous = &current.Snapshot
		}
		if !hasTrace && !errors.Is(err, os.ErrNotExist) {
			return SecurityState{}, err
		}
	}
	if previous == nil && !snapshot.Full {
		return SecurityState{}, fmt.Errorf("%w: initial snapshot must be full", ErrSecurityInvalid)
	}
	normalized, err := validateSecuritySnapshot(snapshot, previous, s.clock().UTC())
	if err != nil {
		return SecurityState{}, err
	}
	encodedSnapshot, err := json.Marshal(normalized)
	if err != nil {
		return SecurityState{}, err
	}
	hash := sha256Hex(encodedSnapshot)
	if previous != nil && current.Hash == hash {
		if err := s.publishSecurityPointerLocked(current); err != nil {
			if errors.Is(err, ErrActivationCommitted) {
				return cloneSecurityState(current), err
			}
			return SecurityState{}, err
		}
		return cloneSecurityState(current), nil
	}
	state := SecurityState{Version: 1, Hash: hash, Snapshot: normalized, ActivatedAt: s.clock().UTC()}
	encodedState, err := json.Marshal(state)
	if err != nil {
		return SecurityState{}, err
	}
	securityRoot := filepath.Join(s.root, securityDirName)
	if err := ensurePrivateDir(securityRoot); err != nil {
		return SecurityState{}, err
	}
	snapshotsRoot := filepath.Join(securityRoot, "snapshots")
	if err := ensureDurablePrivateSubdir(securityRoot, "snapshots", s.random); err != nil {
		return SecurityState{}, err
	}
	if err := s.persistenceCheckpoint("security.after_snapshots_publish"); err != nil {
		return SecurityState{}, err
	}
	fileName := fmt.Sprintf("%d-%d-%s.json", normalized.PKIEpoch, normalized.SecurityRevision, hash[:16])
	statePath := filepath.Join(snapshotsRoot, fileName)
	if _, statErr := os.Lstat(statePath); errors.Is(statErr, os.ErrNotExist) {
		if err := writeImmutablePrivateFile(snapshotsRoot, fileName, encodedState, s.random); err != nil {
			existing, existingEncoded, loadErr := loadSecurityStateFile(statePath)
			if loadErr != nil || existing.Hash != hash {
				return SecurityState{}, fmt.Errorf("%w: immutable security snapshot collision: %v", ErrSecurityInvalid, err)
			}
			state = existing
			encodedState = existingEncoded
		} else if err := s.persistenceCheckpoint("security.after_state_publish"); err != nil {
			return SecurityState{}, err
		}
	} else if statErr != nil {
		return SecurityState{}, statErr
	} else {
		existing, existingEncoded, loadErr := loadSecurityStateFile(statePath)
		if loadErr != nil || existing.Hash != hash {
			return SecurityState{}, fmt.Errorf("%w: immutable security snapshot collision", ErrSecurityInvalid)
		}
		state = existing
		encodedState = existingEncoded
	}
	_ = encodedState
	if err := s.publishSecurityPointerLocked(state); err != nil {
		if errors.Is(err, ErrActivationCommitted) {
			return cloneSecurityState(state), err
		}
		return SecurityState{}, err
	}
	return cloneSecurityState(state), nil
}

func (s *Store) LoadSecuritySnapshot() (SecurityState, error) {
	if s == nil {
		return SecurityState{}, errors.New("PKI store is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := s.loadSecurityStateLocked()
	if errors.Is(err, os.ErrNotExist) {
		return SecurityState{}, ErrSecurityInvalid
	}
	return state, err
}

func (s *Store) SecurityAcknowledgement(storageIdentity string) (model.PKISecurityAcknowledgement, error) {
	if s == nil {
		return model.PKISecurityAcknowledgement{}, errors.New("PKI store is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.persistSecurityAcknowledgementLocked(storageIdentity)
}

func securityAcknowledgement(snapshot model.PKISecuritySnapshot, certificateID string) model.PKISecurityAcknowledgement {
	generations := make([]int64, 0, len(snapshot.TrustRoots))
	for _, root := range snapshot.TrustRoots {
		generations = append(generations, root.Generation)
	}
	slices.Sort(generations)
	return model.PKISecurityAcknowledgement{
		PKIDomainID: snapshot.PKIDomainID, PKIEpoch: snapshot.PKIEpoch,
		SecurityRevision: snapshot.SecurityRevision, Full: snapshot.Full,
		CertificateID: strings.TrimSpace(certificateID), TrustGenerations: generations,
	}
}

func (s *Store) loadSecurityStateLocked() (SecurityState, error) {
	securityRoot := filepath.Join(s.root, securityDirName)
	pointerData, err := readPrivateFile(filepath.Join(securityRoot, activePointerName))
	if err != nil {
		return SecurityState{}, err
	}
	var pointer securityPointer
	if err := decodeStrictJSON(pointerData, &pointer); err != nil {
		return SecurityState{}, fmt.Errorf("decode active security pointer: %w", err)
	}
	if pointer.Version != 1 || pointer.Hash == "" || !safeSecurityFile(pointer.File) {
		return SecurityState{}, fmt.Errorf("%w: active security pointer is invalid", ErrSecurityInvalid)
	}
	state, _, err := loadSecurityStateFile(filepath.Join(securityRoot, "snapshots", pointer.File))
	if err != nil {
		return SecurityState{}, fmt.Errorf("read active security snapshot: %w", err)
	}
	if state.Hash != pointer.Hash || pointer.File != securityStateFileName(state) || !pointer.ActivatedAt.Equal(state.ActivatedAt) {
		return SecurityState{}, fmt.Errorf("%w: active security snapshot hash is inconsistent", ErrSecurityInvalid)
	}
	return cloneSecurityState(state), nil
}

func loadSecurityStateFile(path string) (SecurityState, []byte, error) {
	stateData, err := readPrivateFile(path)
	if err != nil {
		return SecurityState{}, nil, err
	}
	var state SecurityState
	if err := decodeStrictJSON(stateData, &state); err != nil {
		return SecurityState{}, nil, fmt.Errorf("decode security snapshot: %w", err)
	}
	encodedSnapshot, err := json.Marshal(state.Snapshot)
	if err != nil {
		return SecurityState{}, nil, err
	}
	if state.Version != 1 || len(state.Hash) != sha256.Size*2 || !validLowerHex(state.Hash) ||
		state.Hash != sha256Hex(encodedSnapshot) || state.ActivatedAt.IsZero() {
		return SecurityState{}, nil, fmt.Errorf("%w: security snapshot state is inconsistent", ErrSecurityInvalid)
	}
	validated, err := validateSecuritySnapshotAt(state.Snapshot, nil, state.Snapshot.IssuedAt, false)
	if err != nil {
		return SecurityState{}, nil, err
	}
	validatedEncoded, err := json.Marshal(validated)
	if err != nil || sha256Hex(validatedEncoded) != state.Hash {
		return SecurityState{}, nil, fmt.Errorf("%w: security snapshot is not canonical", ErrSecurityInvalid)
	}
	state.Snapshot = validated
	return state, stateData, nil
}

func (s *Store) publishSecurityPointerLocked(state SecurityState) error {
	securityRoot := filepath.Join(s.root, securityDirName)
	snapshotsRoot := filepath.Join(securityRoot, "snapshots")
	if err := syncDirectory(snapshotsRoot); err != nil {
		return fmt.Errorf("sync security snapshots: %w", err)
	}
	fileName := securityStateFileName(state)
	pointer := securityPointer{Version: 1, File: fileName, Hash: state.Hash, ActivatedAt: state.ActivatedAt}
	if _, err := writeAtomicPrivateJSON(securityRoot, activePointerName, pointer, s.random); err != nil {
		if errors.Is(err, ErrActivationCommitted) {
			if loaded, loadErr := s.loadSecurityStateLocked(); loadErr == nil && loaded.Hash == state.Hash {
				return &ActivationCommittedError{Stage: "security pointer publication", Cause: err}
			}
		}
		return fmt.Errorf("activate security snapshot: %w", err)
	}
	if err := s.persistenceCheckpoint("security.after_pointer_publish"); err != nil {
		return &ActivationCommittedError{Stage: "security pointer publication", Cause: err}
	}
	return nil
}

func securityStateFileName(state SecurityState) string {
	return fmt.Sprintf("%d-%d-%s.json", state.Snapshot.PKIEpoch, state.Snapshot.SecurityRevision, state.Hash[:16])
}

func (s *Store) latestRecoverableSecurityStateLocked() (SecurityState, bool, error) {
	securityRoot := filepath.Join(s.root, securityDirName)
	if err := cleanupAbandonedPrivateSubdirs(securityRoot, func(name string) bool { return name == "snapshots" }); err != nil {
		return SecurityState{}, true, err
	}
	if err := cleanupAbandonedPrivateFiles(securityRoot, func(name string) bool {
		return name == activePointerName || name == acknowledgementName
	}); err != nil {
		return SecurityState{}, true, err
	}
	entries, err := os.ReadDir(securityRoot)
	if err != nil {
		return SecurityState{}, false, err
	}
	hasAcknowledgement := false
	hasSnapshots := false
	for _, entry := range entries {
		if entry.Name() == acknowledgementName && entry.Type()&os.ModeSymlink == 0 && !entry.IsDir() {
			hasAcknowledgement = true
			continue
		}
		if entry.Name() != "snapshots" {
			return SecurityState{}, true, fmt.Errorf("%w: security store has an incomplete initialization trace", ErrSecurityInvalid)
		}
		if entry.Type()&os.ModeSymlink != 0 || !entry.IsDir() {
			return SecurityState{}, true, fmt.Errorf("%w: security snapshots path is unsafe", ErrSecurityInvalid)
		}
		hasSnapshots = true
	}
	if !hasSnapshots {
		if hasAcknowledgement {
			return SecurityState{}, true, fmt.Errorf("%w: acknowledgement exists without security history", ErrSecurityInvalid)
		}
		return SecurityState{}, false, nil
	}
	snapshotsRoot := filepath.Join(securityRoot, "snapshots")
	if err := cleanupAbandonedPrivateFiles(snapshotsRoot, validSecurityStateFileName); err != nil {
		return SecurityState{}, true, err
	}
	snapshotEntries, err := os.ReadDir(snapshotsRoot)
	if err != nil {
		return SecurityState{}, true, err
	}
	if len(snapshotEntries) == 0 {
		if hasAcknowledgement {
			return SecurityState{}, true, fmt.Errorf("%w: acknowledgement exists without a recoverable snapshot", ErrSecurityInvalid)
		}
		// A snapshots directory is published before its first immutable state.
		// Its empty, marker-free form is an uncommitted bootstrap trace and may
		// be safely reused by the retrying full snapshot.
		return SecurityState{}, false, nil
	}
	states := make([]SecurityState, 0, len(snapshotEntries))
	for _, entry := range snapshotEntries {
		if entry.Type()&os.ModeSymlink != 0 || entry.IsDir() || !safeSecurityFile(entry.Name()) {
			return SecurityState{}, true, fmt.Errorf("%w: unsafe immutable security snapshot", ErrSecurityInvalid)
		}
		state, _, loadErr := loadSecurityStateFile(filepath.Join(snapshotsRoot, entry.Name()))
		if loadErr != nil || securityStateFileName(state) != entry.Name() {
			return SecurityState{}, true, fmt.Errorf("%w: invalid immutable security snapshot %q", ErrSecurityInvalid, entry.Name())
		}
		states = append(states, state)
	}
	sort.Slice(states, func(i, j int) bool {
		if states[i].Snapshot.PKIEpoch == states[j].Snapshot.PKIEpoch {
			return states[i].Snapshot.SecurityRevision < states[j].Snapshot.SecurityRevision
		}
		return states[i].Snapshot.PKIEpoch < states[j].Snapshot.PKIEpoch
	})
	if !states[0].Snapshot.Full {
		return SecurityState{}, true, fmt.Errorf("%w: first recoverable snapshot is not full", ErrSecurityInvalid)
	}
	for index := 1; index < len(states); index++ {
		previous := states[index-1].Snapshot
		validated, validateErr := validateSecuritySnapshotAt(states[index].Snapshot, &previous, states[index].Snapshot.IssuedAt, false)
		if validateErr != nil {
			return SecurityState{}, true, fmt.Errorf("%w: security history continuity failed", ErrSecurityInvalid)
		}
		encoded, marshalErr := json.Marshal(validated)
		if marshalErr != nil || sha256Hex(encoded) != states[index].Hash {
			return SecurityState{}, true, fmt.Errorf("%w: security history is not canonical", ErrSecurityInvalid)
		}
	}
	return cloneSecurityState(states[len(states)-1]), true, nil
}

func cleanupAbandonedPrivateFiles(parent string, allowed func(string) bool) error {
	entries, err := os.ReadDir(parent)
	if err != nil {
		return err
	}
	removed := false
	for _, entry := range entries {
		target, ok := atomicTemporaryTarget(entry.Name())
		if !ok || allowed == nil || !allowed(target) {
			continue
		}
		path := filepath.Join(parent, entry.Name())
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return fmt.Errorf("%w: abandoned security temporary path is unsafe", ErrSecurityInvalid)
		}
		if err := os.Remove(path); err != nil {
			return err
		}
		removed = true
	}
	if removed {
		return syncDirectory(parent)
	}
	return nil
}

func atomicTemporaryTarget(name string) (string, bool) {
	if !strings.HasPrefix(name, ".") {
		return "", false
	}
	body := strings.TrimPrefix(name, ".")
	separator := strings.LastIndex(body, ".tmp-")
	if separator <= 0 {
		return "", false
	}
	target, suffix := body[:separator], body[separator+len(".tmp-"):]
	if len(suffix) != 16 || !validLowerHex(suffix) || filepath.Base(target) != target {
		return "", false
	}
	return target, true
}

func cloneSecurityState(state SecurityState) SecurityState {
	cloned := state
	cloned.Snapshot = cloneSecuritySnapshot(state.Snapshot)
	return cloned
}

func cloneSecuritySnapshot(snapshot model.PKISecuritySnapshot) model.PKISecuritySnapshot {
	cloned := snapshot
	cloned.TrustRoots = slices.Clone(snapshot.TrustRoots)
	cloned.RevokedIdentityIDs = slices.Clone(snapshot.RevokedIdentityIDs)
	cloned.RevokedSerials = slices.Clone(snapshot.RevokedSerials)
	cloned.Signature = slices.Clone(snapshot.Signature)
	return cloned
}

func safeSecurityFile(name string) bool {
	return validSecurityStateFileName(name)
}

func validSecurityStateFileName(name string) bool {
	return filepath.Base(name) == name && securityStateFilePattern.MatchString(name)
}

// validateSecuritySnapshot returns the normalized, signature-equivalent
// snapshot. previous is nil only for the first trust bootstrap or for loading
// an already hash-bound durable state.
func validateSecuritySnapshot(snapshot model.PKISecuritySnapshot, previous *model.PKISecuritySnapshot, now time.Time) (model.PKISecuritySnapshot, error) {
	return validateSecuritySnapshotAt(snapshot, previous, now, true)
}

func validateSecuritySnapshotAt(snapshot model.PKISecuritySnapshot, previous *model.PKISecuritySnapshot, validationTime time.Time, canonicalizeSignature bool) (model.PKISecuritySnapshot, error) {
	snapshot.PKIDomainID = strings.TrimSpace(snapshot.PKIDomainID)
	if validateURISegment(snapshot.PKIDomainID) != nil || snapshot.PKIEpoch < 0 || snapshot.SecurityRevision < 0 ||
		snapshot.IssuedAt.IsZero() || snapshot.SignerGeneration <= 0 || len(snapshot.Signature) == 0 || len(snapshot.TrustRoots) == 0 {
		return model.PKISecuritySnapshot{}, fmt.Errorf("%w: signed snapshot metadata is incomplete", ErrSecurityInvalid)
	}
	if snapshot.IssuedAt.After(validationTime.Add(5 * time.Minute)) {
		return model.PKISecuritySnapshot{}, fmt.Errorf("%w: signed snapshot is issued in the future", ErrSecurityInvalid)
	}
	replay := false
	if previous != nil {
		if snapshot.PKIDomainID != previous.PKIDomainID || snapshot.PKIEpoch < previous.PKIEpoch {
			return model.PKISecuritySnapshot{}, ErrSecurityDowngrade
		}
		if snapshot.PKIEpoch == previous.PKIEpoch {
			if snapshot.SecurityRevision < previous.SecurityRevision {
				return model.PKISecuritySnapshot{}, ErrSecurityDowngrade
			}
			replay = snapshot.SecurityRevision == previous.SecurityRevision
		}
		if snapshot.PKIEpoch > previous.PKIEpoch && (!snapshot.Full || snapshot.SecurityRevision != 0) {
			return model.PKISecuritySnapshot{}, fmt.Errorf("%w: higher epoch requires a full revision zero snapshot", ErrSecurityDowngrade)
		}
	}

	snapshot.TrustRoots = slices.Clone(snapshot.TrustRoots)
	sort.Slice(snapshot.TrustRoots, func(i, j int) bool { return snapshot.TrustRoots[i].Generation < snapshot.TrustRoots[j].Generation })
	var err error
	snapshot.RevokedIdentityIDs, err = normalizeRevokedValues(snapshot.RevokedIdentityIDs, false)
	if err != nil {
		return model.PKISecuritySnapshot{}, err
	}
	snapshot.RevokedSerials, err = normalizeRevokedValues(snapshot.RevokedSerials, true)
	if err != nil {
		return model.PKISecuritySnapshot{}, err
	}
	if previous != nil && snapshot.PKIEpoch == previous.PKIEpoch {
		if !containsAllSorted(snapshot.RevokedIdentityIDs, previous.RevokedIdentityIDs) || !containsAllSorted(snapshot.RevokedSerials, previous.RevokedSerials) {
			return model.PKISecuritySnapshot{}, fmt.Errorf("%w: same-epoch revocations are not monotonic", ErrSecurityInvalid)
		}
	}
	descriptors := make([]securityTrustDescriptor, len(snapshot.TrustRoots))
	trustGenerations := make([]int64, len(snapshot.TrustRoots))
	seenGeneration := make(map[int64]struct{}, len(snapshot.TrustRoots))
	var signerCertificate *x509.Certificate
	for index := range snapshot.TrustRoots {
		root := &snapshot.TrustRoots[index]
		root.AuthorityID = strings.TrimSpace(root.AuthorityID)
		root.Status = strings.TrimSpace(root.Status)
		root.FingerprintSHA256 = strings.ToLower(strings.TrimSpace(root.FingerprintSHA256))
		if root.AuthorityID == "" || root.Generation <= 0 || root.NotBefore.IsZero() || root.NotAfter.IsZero() || !root.NotBefore.Before(root.NotAfter) {
			return model.PKISecuritySnapshot{}, fmt.Errorf("%w: trust root metadata is invalid", ErrSecurityInvalid)
		}
		if _, duplicate := seenGeneration[root.Generation]; duplicate {
			return model.PKISecuritySnapshot{}, fmt.Errorf("%w: trust generations are not unique", ErrSecurityInvalid)
		}
		seenGeneration[root.Generation] = struct{}{}
		switch root.Status {
		case "active", "prepared", "retiring":
		default:
			return model.PKISecuritySnapshot{}, fmt.Errorf("%w: trust root status is invalid", ErrSecurityInvalid)
		}
		certificate, err := parseCertificatePEM(root.CertificatePEM)
		if err != nil {
			return model.PKISecuritySnapshot{}, fmt.Errorf("%w: trust root certificate is invalid", ErrSecurityInvalid)
		}
		rootPublicKey, keyOK := certificate.PublicKey.(*ecdsa.PublicKey)
		if !certificate.IsCA || !certificate.BasicConstraintsValid || certificate.KeyUsage&x509.KeyUsageCertSign == 0 ||
			certificate.CheckSignatureFrom(certificate) != nil || !validCertificateSerial(certificate.SerialNumber) ||
			!keyOK || rootPublicKey.Curve == nil || rootPublicKey.Curve.Params().Name != elliptic.P256().Params().Name ||
			certificate.SignatureAlgorithm != x509.ECDSAWithSHA256 {
			return model.PKISecuritySnapshot{}, fmt.Errorf("%w: trust root certificate is invalid", ErrSecurityInvalid)
		}
		fingerprint := sha256.Sum256(certificate.Raw)
		if root.FingerprintSHA256 != hex.EncodeToString(fingerprint[:]) || !certificate.NotBefore.Equal(root.NotBefore) || !certificate.NotAfter.Equal(root.NotAfter) {
			return model.PKISecuritySnapshot{}, fmt.Errorf("%w: trust root certificate metadata is inconsistent", ErrSecurityInvalid)
		}
		root.NotBefore = root.NotBefore.UTC()
		root.NotAfter = root.NotAfter.UTC()
		trustGenerations[index] = root.Generation
		descriptors[index] = securityTrustDescriptor{
			AuthorityID: root.AuthorityID, Generation: root.Generation, Status: root.Status,
			FingerprintSHA256: root.FingerprintSHA256, NotBefore: root.NotBefore, NotAfter: root.NotAfter,
		}
		if root.Generation == snapshot.SignerGeneration {
			if root.Status != "active" || validationTime.Before(certificate.NotBefore) || validationTime.After(certificate.NotAfter) {
				return model.PKISecuritySnapshot{}, fmt.Errorf("%w: snapshot signer is not an active valid root", ErrSecurityInvalid)
			}
			signerCertificate = certificate
		}
	}
	if signerCertificate == nil {
		return model.PKISecuritySnapshot{}, fmt.Errorf("%w: snapshot signer generation is not trusted", ErrSecurityInvalid)
	}
	if previous != nil && !previousTrustsSigner(*previous, snapshot) {
		return model.PKISecuritySnapshot{}, fmt.Errorf("%w: snapshot signer has no last-known-good trust continuity", ErrSecurityInvalid)
	}
	if previous != nil {
		if err := validateTrustRootTransitions(*previous, snapshot); err != nil {
			return model.PKISecuritySnapshot{}, err
		}
	}
	payload, err := json.Marshal(securitySignaturePayload{
		PKIDomainID: snapshot.PKIDomainID,
		Version:     securitySnapshotVersion{Version: securityVersion{PKIEpoch: snapshot.PKIEpoch, SecurityRevision: snapshot.SecurityRevision}, Full: snapshot.Full},
		IssuedAt:    snapshot.IssuedAt.UTC(), TrustGenerations: trustGenerations, TrustRoots: descriptors,
		RevokedIdentityIDs: snapshot.RevokedIdentityIDs, RevokedSerials: snapshot.RevokedSerials,
	})
	if err != nil {
		return model.PKISecuritySnapshot{}, err
	}
	publicKey, ok := signerCertificate.PublicKey.(*ecdsa.PublicKey)
	if !ok {
		return model.PKISecuritySnapshot{}, fmt.Errorf("%w: snapshot signer key is not ECDSA", ErrSecurityInvalid)
	}
	digest := sha256.Sum256(payload)
	if !ecdsa.VerifyASN1(publicKey, digest[:], snapshot.Signature) {
		return model.PKISecuritySnapshot{}, fmt.Errorf("%w: signature verification failed", ErrSecurityInvalid)
	}
	canonicalSignature, err := canonicalizeECDSASignature(publicKey, snapshot.Signature)
	if err != nil {
		return model.PKISecuritySnapshot{}, fmt.Errorf("%w: signature encoding is invalid", ErrSecurityInvalid)
	}
	comparison := cloneSecuritySnapshot(snapshot)
	comparison.Signature = canonicalSignature
	comparison.IssuedAt = comparison.IssuedAt.UTC()
	if canonicalizeSignature {
		snapshot.Signature = canonicalSignature
	}
	snapshot.IssuedAt = snapshot.IssuedAt.UTC()
	if replay {
		prior := cloneSecuritySnapshot(*previous)
		priorSignature, err := canonicalizeECDSASignature(publicKey, prior.Signature)
		if err != nil {
			return model.PKISecuritySnapshot{}, fmt.Errorf("%w: previous signature encoding is invalid", ErrSecurityInvalid)
		}
		prior.Signature = priorSignature
		prior.IssuedAt = prior.IssuedAt.UTC()
		candidateEncoded, candidateErr := json.Marshal(comparison)
		previousEncoded, previousErr := json.Marshal(prior)
		if candidateErr == nil && previousErr == nil && slices.Equal(candidateEncoded, previousEncoded) {
			return cloneSecuritySnapshot(*previous), nil
		}
		return model.PKISecuritySnapshot{}, ErrSecurityDowngrade
	}
	return snapshot, nil
}

func canonicalizeECDSASignature(publicKey *ecdsa.PublicKey, signature []byte) ([]byte, error) {
	if publicKey == nil || publicKey.Curve == nil || publicKey.Curve.Params() == nil {
		return nil, errors.New("ECDSA public key is unavailable")
	}
	parsed := struct {
		R *big.Int
		S *big.Int
	}{}
	rest, err := asn1.Unmarshal(signature, &parsed)
	order := publicKey.Curve.Params().N
	if err != nil || len(rest) != 0 || parsed.R == nil || parsed.S == nil || parsed.R.Sign() <= 0 || parsed.S.Sign() <= 0 ||
		parsed.R.Cmp(order) >= 0 || parsed.S.Cmp(order) >= 0 {
		return nil, errors.New("ECDSA signature is out of range")
	}
	halfOrder := new(big.Int).Rsh(new(big.Int).Set(order), 1)
	if parsed.S.Cmp(halfOrder) > 0 {
		parsed.S = new(big.Int).Sub(order, parsed.S)
	}
	return asn1.Marshal(parsed)
}

func previousTrustsSigner(previous model.PKISecuritySnapshot, candidate model.PKISecuritySnapshot) bool {
	var signer *model.PKITrustRoot
	for index := range candidate.TrustRoots {
		if candidate.TrustRoots[index].Generation == candidate.SignerGeneration {
			signer = &candidate.TrustRoots[index]
			break
		}
	}
	if signer == nil {
		return false
	}
	for _, root := range previous.TrustRoots {
		if root.Generation == signer.Generation && root.AuthorityID == signer.AuthorityID &&
			strings.EqualFold(root.FingerprintSHA256, signer.FingerprintSHA256) &&
			(root.Status == "active" || root.Status == "prepared") {
			return true
		}
	}
	return false
}

func validateTrustRootTransitions(previous, candidate model.PKISecuritySnapshot) error {
	if candidate.PKIEpoch != previous.PKIEpoch {
		return nil
	}
	previousByGeneration := make(map[int64]model.PKITrustRoot, len(previous.TrustRoots))
	candidateByGeneration := make(map[int64]model.PKITrustRoot, len(candidate.TrustRoots))
	var maximumPreviousGeneration int64
	for _, root := range previous.TrustRoots {
		previousByGeneration[root.Generation] = root
		if root.Generation > maximumPreviousGeneration {
			maximumPreviousGeneration = root.Generation
		}
	}
	for _, root := range candidate.TrustRoots {
		candidateByGeneration[root.Generation] = root
		prior, existed := previousByGeneration[root.Generation]
		if !existed {
			if root.Generation <= maximumPreviousGeneration {
				return fmt.Errorf("%w: removed trust generation cannot be reintroduced", ErrSecurityInvalid)
			}
			if root.Status != "prepared" {
				return fmt.Errorf("%w: a new trust generation must enter as prepared", ErrSecurityInvalid)
			}
			continue
		}
		if prior.AuthorityID != root.AuthorityID || !strings.EqualFold(prior.FingerprintSHA256, root.FingerprintSHA256) {
			return fmt.Errorf("%w: trust generation identity changed", ErrSecurityInvalid)
		}
		allowed := prior.Status == root.Status ||
			(prior.Status == "prepared" && root.Status == "active") ||
			(prior.Status == "active" && root.Status == "retiring")
		if !allowed {
			return fmt.Errorf("%w: invalid trust generation lifecycle transition", ErrSecurityInvalid)
		}
	}
	for _, prior := range previous.TrustRoots {
		if _, retained := candidateByGeneration[prior.Generation]; retained {
			continue
		}
		if prior.Status != "retiring" {
			return fmt.Errorf("%w: active or prepared trust generation cannot disappear", ErrSecurityInvalid)
		}
	}
	return nil
}

func normalizeRevokedValues(values []string, lower bool) ([]string, error) {
	if values == nil {
		return nil, nil
	}
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		original := value
		value = strings.TrimSpace(value)
		if lower {
			if value != original || value != strings.ToLower(value) {
				return nil, fmt.Errorf("%w: revoked certificate serial is not canonical hex", ErrSecurityInvalid)
			}
		}
		if value == "" {
			return nil, fmt.Errorf("%w: revocation identifier is empty", ErrSecurityInvalid)
		}
		if lower {
			serial, ok := new(big.Int).SetString(value, 16)
			if !ok || !validCertificateSerial(serial) || serial.Text(16) != value {
				return nil, fmt.Errorf("%w: revoked certificate serial is not canonical hex", ErrSecurityInvalid)
			}
		}
		if _, duplicate := seen[value]; duplicate {
			return nil, fmt.Errorf("%w: revocation identifiers are not unique", ErrSecurityInvalid)
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result, nil
}

func containsAllSorted(candidate, previous []string) bool {
	if len(previous) == 0 {
		return true
	}
	known := make(map[string]struct{}, len(candidate))
	for _, value := range candidate {
		known[value] = struct{}{}
	}
	for _, value := range previous {
		if _, ok := known[value]; !ok {
			return false
		}
	}
	return true
}

func validCertificateSerial(serial *big.Int) bool {
	if serial == nil || serial.Sign() <= 0 {
		return false
	}
	bits := serial.BitLen()
	return bits >= 128 && bits <= 159
}

func validLowerHex(value string) bool {
	if value == "" || value != strings.ToLower(value) {
		return false
	}
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func parseCertificatePEM(value string) (*x509.Certificate, error) {
	block, rest := pem.Decode([]byte(strings.TrimSpace(value)))
	if block == nil || block.Type != "CERTIFICATE" || len(block.Headers) != 0 || len(strings.TrimSpace(string(rest))) != 0 {
		return nil, fmt.Errorf("certificate PEM must contain exactly one certificate")
	}
	return x509.ParseCertificate(block.Bytes)
}
