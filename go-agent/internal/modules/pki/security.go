package pki

import (
	"crypto/ecdsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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

// ApplySecuritySnapshot verifies monotonicity, signature, public trust
// metadata, and bootstrap continuity before atomically advancing the durable
// active pointer. A byte-identical replay is idempotent.
func (s *Store) ApplySecuritySnapshot(snapshot model.PKISecuritySnapshot) (SecurityState, error) {
	if s == nil {
		return SecurityState{}, errors.New("PKI store is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	var previous *model.PKISecuritySnapshot
	current, err := s.loadSecurityStateLocked()
	if err == nil {
		previous = &current.Snapshot
	} else if !errors.Is(err, os.ErrNotExist) {
		return SecurityState{}, err
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
		return current, nil
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
	if err := ensurePrivateDir(snapshotsRoot); err != nil {
		return SecurityState{}, err
	}
	fileName := fmt.Sprintf("%d-%d-%s.json", normalized.PKIEpoch, normalized.SecurityRevision, hash[:16])
	statePath := filepath.Join(snapshotsRoot, fileName)
	if _, statErr := os.Lstat(statePath); errors.Is(statErr, os.ErrNotExist) {
		if err := writePrivateFile(statePath, encodedState); err != nil {
			return SecurityState{}, err
		}
		if err := syncDirectory(snapshotsRoot); err != nil {
			return SecurityState{}, err
		}
	} else if statErr != nil {
		return SecurityState{}, statErr
	} else if !sameFileContent(statePath, encodedState) {
		return SecurityState{}, fmt.Errorf("%w: immutable security snapshot collision", ErrSecurityInvalid)
	}
	pointer := securityPointer{Version: 1, File: fileName, Hash: hash, ActivatedAt: state.ActivatedAt}
	if _, err := writeAtomicPrivateJSON(securityRoot, activePointerName, pointer, s.random); err != nil {
		return SecurityState{}, fmt.Errorf("activate security snapshot: %w", err)
	}
	return state, nil
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

func (s *Store) SecurityAcknowledgement(certificateID string) (model.PKISecurityAcknowledgement, error) {
	state, err := s.LoadSecuritySnapshot()
	if err != nil {
		return model.PKISecurityAcknowledgement{}, err
	}
	return securityAcknowledgement(state.Snapshot, certificateID), nil
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
	stateData, err := readPrivateFile(filepath.Join(securityRoot, "snapshots", pointer.File))
	if err != nil {
		return SecurityState{}, fmt.Errorf("read active security snapshot: %w", err)
	}
	var state SecurityState
	if err := decodeStrictJSON(stateData, &state); err != nil {
		return SecurityState{}, fmt.Errorf("decode active security snapshot: %w", err)
	}
	encodedSnapshot, err := json.Marshal(state.Snapshot)
	if err != nil {
		return SecurityState{}, err
	}
	if state.Version != 1 || state.Hash != pointer.Hash || state.Hash != sha256Hex(encodedSnapshot) {
		return SecurityState{}, fmt.Errorf("%w: active security snapshot hash is inconsistent", ErrSecurityInvalid)
	}
	validated, err := validateSecuritySnapshot(state.Snapshot, nil, s.clock().UTC())
	if err != nil {
		return SecurityState{}, err
	}
	state.Snapshot = validated
	return state, nil
}

func safeSecurityFile(name string) bool {
	return name != "" && filepath.Base(name) == name && strings.HasSuffix(name, ".json") && !strings.ContainsAny(name, `/\\`)
}

// validateSecuritySnapshot returns the normalized, signature-equivalent
// snapshot. previous is nil only for the first trust bootstrap or for loading
// an already hash-bound durable state.
func validateSecuritySnapshot(snapshot model.PKISecuritySnapshot, previous *model.PKISecuritySnapshot, now time.Time) (model.PKISecuritySnapshot, error) {
	snapshot.PKIDomainID = strings.TrimSpace(snapshot.PKIDomainID)
	if validateURISegment(snapshot.PKIDomainID) != nil || snapshot.PKIEpoch < 0 || snapshot.SecurityRevision < 0 ||
		snapshot.IssuedAt.IsZero() || snapshot.SignerGeneration <= 0 || len(snapshot.Signature) == 0 || len(snapshot.TrustRoots) == 0 {
		return model.PKISecuritySnapshot{}, fmt.Errorf("%w: signed snapshot metadata is incomplete", ErrSecurityInvalid)
	}
	if snapshot.IssuedAt.After(now.Add(5 * time.Minute)) {
		return model.PKISecuritySnapshot{}, fmt.Errorf("%w: signed snapshot is issued in the future", ErrSecurityInvalid)
	}
	if previous != nil {
		if snapshot.PKIDomainID != previous.PKIDomainID || snapshot.PKIEpoch < previous.PKIEpoch {
			return model.PKISecuritySnapshot{}, ErrSecurityDowngrade
		}
		if snapshot.PKIEpoch == previous.PKIEpoch && snapshot.SecurityRevision <= previous.SecurityRevision {
			previousEncoded, _ := json.Marshal(previous)
			candidateEncoded, _ := json.Marshal(snapshot)
			if snapshot.SecurityRevision == previous.SecurityRevision && sha256Hex(previousEncoded) == sha256Hex(candidateEncoded) {
				return *previous, nil
			}
			return model.PKISecuritySnapshot{}, ErrSecurityDowngrade
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
		if err != nil || !certificate.IsCA || certificate.KeyUsage&x509.KeyUsageCertSign == 0 || certificate.CheckSignatureFrom(certificate) != nil {
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
			if root.Status != "active" || now.Before(certificate.NotBefore) || now.After(certificate.NotAfter) {
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
	snapshot.IssuedAt = snapshot.IssuedAt.UTC()
	return snapshot, nil
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
			strings.EqualFold(root.FingerprintSHA256, signer.FingerprintSHA256) {
			return true
		}
	}
	return false
}

func normalizeRevokedValues(values []string, lower bool) ([]string, error) {
	if values == nil {
		return nil, nil
	}
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if lower {
			value = strings.ToLower(value)
		}
		if value == "" {
			return nil, fmt.Errorf("%w: revocation identifier is empty", ErrSecurityInvalid)
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

func parseCertificatePEM(value string) (*x509.Certificate, error) {
	block, rest := pem.Decode([]byte(strings.TrimSpace(value)))
	if block == nil || block.Type != "CERTIFICATE" || len(block.Headers) != 0 || len(strings.TrimSpace(string(rest))) != 0 {
		return nil, fmt.Errorf("certificate PEM must contain exactly one certificate")
	}
	return x509.ParseCertificate(block.Bytes)
}
