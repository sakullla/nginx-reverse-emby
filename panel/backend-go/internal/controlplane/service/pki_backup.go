package service

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
	"golang.org/x/crypto/argon2"
)

const (
	PKIProtectedBackupFormat = "nre-pki-protected-backup-v1"
	PKIBackupFormatVersion   = 1

	pkiBackupKDFAlgorithm = "argon2id"
	pkiBackupKDFMemoryKiB = uint32(64 * 1024)
	pkiBackupKDFTime      = uint32(3)
	pkiBackupKDFThreads   = uint8(1)
	pkiBackupKDFKeyBytes  = uint32(32)
	pkiBackupSaltBytes    = 32

	pkiBackupCipherAlgorithm = "aes-256-gcm"
	pkiBackupMaxEnvelopeSize = 1 << 30
	pkiBackupMaxSnapshotSize = storage.MaxProtectedPKISnapshotBytes
	pkiBackupCAPurpose       = "ca-signing"
	pkiBackupTokenPolicy     = "excluded_all"
)

var (
	ErrPKIBackupInvalid        = errors.New("invalid protected PKI backup")
	ErrPKIBackupAuthentication = errors.New("protected PKI backup authentication failed")
	ErrPKIBackupIntegrity      = errors.New("protected PKI backup integrity validation failed")
	ErrPKIBackupSchema         = errors.New("protected PKI backup schema validation failed")
	ErrPKIBackupActivation     = errors.New("protected PKI backup activation failed")
)

// PKIBackupSnapshotSource must return a transactionally consistent standalone
// SQLite image. In particular, callers must not return a main database file
// whose committed state still depends on an adjacent WAL file.
type PKIBackupSnapshotSource interface {
	CaptureConsistentPKISQLite(context.Context) ([]byte, error)
}

// PKIBackupAuthorityKeySource returns the decrypted authority key named by the
// canonical row. The service checks the lease immediately before and after
// each call, verifies the key against the authority certificate, and erases its
// copy after sealing the envelope.
type PKIBackupAuthorityKeySource interface {
	ExportPKIAuthorityKey(context.Context, storage.PKIAuthorityRow) ([]byte, error)
}

type PKIBackupTargetState struct {
	Initialized         bool
	PKIDomainID         string
	Version             PKISecurityVersion
	SQLiteSchemaVersion int
	SQLiteSchemaSHA256  string
}

type PKIBackupActivationRequest struct {
	ExpectedTarget    PKIBackupTargetState
	PKIDomainID       string
	Version           PKISecurityVersion
	Full              bool
	Forced            bool
	SQLiteSnapshot    []byte
	AuthorityKeys     []PKIBackupAuthorityKey
	AuthenticatedFrom PKIBackupManifest
}

// PKIBackupRestoreTarget is the production activation boundary. Current state
// must always include the target binary's trusted complete SQLite schema version
// and hash, including before PKI initialization; backup metadata cannot serve as
// its own compatibility baseline. Activation must compare ExpectedTarget with
// the current canonical state, create a new
// local vault master key, seal every supplied CA key under that master, and
// atomically replace/reopen the staged SQLite and vault directory. If it
// returns an error, the previously active database and vault must be unchanged.
// Implementations consume the request synchronously and must not retain the
// plaintext AuthorityKeys slices.
type PKIBackupRestoreTarget interface {
	CurrentPKIBackupTarget(context.Context) (PKIBackupTargetState, error)
	ActivateProtectedPKIBackup(context.Context, PKIBackupActivationRequest) error
}

type PKIBackupKDFManifest struct {
	Algorithm   string `json:"algorithm"`
	MemoryKiB   uint32 `json:"memory_kib"`
	Iterations  uint32 `json:"iterations"`
	Parallelism uint8  `json:"parallelism"`
	KeyBytes    uint32 `json:"key_bytes"`
	Salt        []byte `json:"salt"`
}

type PKIBackupCipherManifest struct {
	Algorithm string `json:"algorithm"`
	Nonce     []byte `json:"nonce"`
}

type PKIBackupAuthorityManifest struct {
	AuthorityID string `json:"authority_id"`
	Generation  int64  `json:"generation"`
	Status      string `json:"status"`
	PKCS8Bytes  int64  `json:"pkcs8_bytes"`
	PKCS8SHA256 string `json:"pkcs8_sha256"`
}

type PKIBackupManifest struct {
	FormatVersion      int                          `json:"format_version"`
	PKIDomainID        string                       `json:"pki_domain_id"`
	Version            PKISecurityVersion           `json:"version"`
	Full               bool                         `json:"full"`
	ExportedAt         time.Time                    `json:"exported_at"`
	SQLiteBytes        int64                        `json:"sqlite_bytes"`
	SQLiteSHA256       string                       `json:"sqlite_sha256"`
	SQLiteSchema       int                          `json:"sqlite_schema_version"`
	SQLiteSchemaSHA256 string                       `json:"sqlite_schema_sha256"`
	EnrollmentPolicy   string                       `json:"enrollment_token_policy"`
	EnrollmentTokens   int64                        `json:"enrollment_token_rows"`
	AuthorityKeys      []PKIBackupAuthorityManifest `json:"authority_keys"`
}

type PKIProtectedBackupEnvelope struct {
	Format     string                  `json:"format"`
	KDF        PKIBackupKDFManifest    `json:"kdf"`
	Cipher     PKIBackupCipherManifest `json:"cipher"`
	Manifest   PKIBackupManifest       `json:"manifest"`
	Ciphertext []byte                  `json:"ciphertext"`
}

type PKIBackupAuthorityKey struct {
	AuthorityID string `json:"authority_id"`
	Generation  int64  `json:"generation"`
	PKCS8       []byte `json:"pkcs8"`
}

type PKIBackupExport struct {
	Envelope []byte
	Manifest PKIBackupManifest
}

type PKIBackupRestoreOptions struct {
	// Force is the disaster-recovery path. The caller must protect it with the
	// documented local second confirmation. It deliberately does not depend on
	// a live old-domain lease and always activates a new epoch at revision zero.
	Force bool
}

type PKIBackupRestoreResult struct {
	PKIDomainID string
	Version     PKISecurityVersion
	Forced      bool
}

type PKIBackupServiceOptions struct {
	LeaseGate          PKILeaseGate
	SnapshotSource     PKIBackupSnapshotSource
	AuthorityKeySource PKIBackupAuthorityKeySource
	RestoreTarget      PKIBackupRestoreTarget
	Clock              func() time.Time
	Random             io.Reader
}

type PKIBackupService struct {
	leaseGate          PKILeaseGate
	snapshotSource     PKIBackupSnapshotSource
	authorityKeySource PKIBackupAuthorityKeySource
	restoreTarget      PKIBackupRestoreTarget
	clock              func() time.Time
	random             io.Reader
}

func NewPKIBackupService(options PKIBackupServiceOptions) (*PKIBackupService, error) {
	if options.LeaseGate == nil || options.SnapshotSource == nil || options.AuthorityKeySource == nil {
		return nil, fmt.Errorf("%w: lease gate, snapshot source, and authority key source are required", ErrPKIBackupInvalid)
	}
	if options.Clock == nil {
		options.Clock = time.Now
	}
	if options.Random == nil {
		options.Random = rand.Reader
	}
	return &PKIBackupService{
		leaseGate: options.LeaseGate, snapshotSource: options.SnapshotSource,
		authorityKeySource: options.AuthorityKeySource, restoreTarget: options.RestoreTarget,
		clock: options.Clock, random: options.Random,
	}, nil
}

func (s *PKIBackupService) ExportProtected(ctx context.Context, passphrase []byte) (PKIBackupExport, error) {
	if err := validatePKIBackupPassphrase(passphrase); err != nil {
		return PKIBackupExport{}, err
	}
	grant, err := s.leaseGate.RequirePKILease(ctx)
	if err != nil {
		return PKIBackupExport{}, fmt.Errorf("authorize PKI backup export: %w", err)
	}
	rawSnapshot, err := s.snapshotSource.CaptureConsistentPKISQLite(ctx)
	if err != nil {
		return PKIBackupExport{}, fmt.Errorf("capture PKI SQLite snapshot: %w", err)
	}
	if len(rawSnapshot) == 0 || len(rawSnapshot) > pkiBackupMaxSnapshotSize {
		return PKIBackupExport{}, fmt.Errorf("%w: SQLite snapshot size is invalid", ErrPKIBackupInvalid)
	}
	staged, err := stagePKIBackupSQLite(ctx, rawSnapshot, pkiBackupStageOptions{Sanitize: true})
	clear(rawSnapshot)
	if err != nil {
		return PKIBackupExport{}, err
	}
	if staged.State.Settings == nil || staged.State.Settings.PKIDomainID != grant.PKIDomainID || staged.State.Settings.PKIEpoch != grant.PKIEpoch {
		clear(staged.Snapshot)
		return PKIBackupExport{}, fmt.Errorf("authorize captured PKI snapshot: %w", ErrPKILeaseNotHeld)
	}

	keys, err := s.exportAuthorityKeys(ctx, staged.State, grant)
	if err != nil {
		clear(staged.Snapshot)
		return PKIBackupExport{}, err
	}
	defer clearPKIBackupAuthorityKeys(keys)

	manifest := buildPKIBackupManifest(staged, keys, s.clock().UTC())
	envelope, err := s.sealPKIBackup(passphrase, manifest, staged.Snapshot, keys)
	clear(staged.Snapshot)
	if err != nil {
		return PKIBackupExport{}, err
	}
	finalGrant, err := s.leaseGate.RequirePKILease(ctx)
	if err != nil || !samePKILeaseAuthority(grant, finalGrant) {
		clear(envelope)
		if err != nil {
			return PKIBackupExport{}, fmt.Errorf("recheck PKI backup export lease: %w", err)
		}
		return PKIBackupExport{}, fmt.Errorf("recheck PKI backup export lease: %w", ErrPKILeaseNotHeld)
	}
	return PKIBackupExport{Envelope: envelope, Manifest: manifest}, nil
}

func (s *PKIBackupService) RestoreProtected(ctx context.Context, archive, passphrase []byte, options PKIBackupRestoreOptions) (PKIBackupRestoreResult, error) {
	if s.restoreTarget == nil {
		return PKIBackupRestoreResult{}, fmt.Errorf("%w: protected restore executor is unavailable", ErrPKIBackupActivation)
	}
	if err := validatePKIBackupPassphrase(passphrase); err != nil {
		return PKIBackupRestoreResult{}, err
	}
	var initialGrant PKILeaseGrant
	if !options.Force {
		var err error
		initialGrant, err = s.leaseGate.RequirePKILease(ctx)
		if err != nil {
			return PKIBackupRestoreResult{}, fmt.Errorf("authorize PKI backup restore: %w", err)
		}
	}

	manifest, payload, err := openPKIBackup(passphrase, archive)
	if err != nil {
		return PKIBackupRestoreResult{}, err
	}
	defer clearPKIBackupPayload(&payload)
	if !options.Force {
		postDecryptGrant, leaseErr := s.leaseGate.RequirePKILease(ctx)
		if leaseErr != nil || !samePKILeaseAuthority(initialGrant, postDecryptGrant) {
			if leaseErr != nil {
				return PKIBackupRestoreResult{}, fmt.Errorf("recheck PKI backup decryption lease: %w", leaseErr)
			}
			return PKIBackupRestoreResult{}, fmt.Errorf("recheck PKI backup decryption lease: %w", ErrPKILeaseNotHeld)
		}
	}
	staged, err := stagePKIBackupSQLite(ctx, payload.SQLiteSnapshot, pkiBackupStageOptions{Sanitize: false})
	if err != nil {
		return PKIBackupRestoreResult{}, err
	}
	defer clear(staged.Snapshot)
	if err := validatePKIBackupManifest(manifest, staged, payload.AuthorityKeys); err != nil {
		return PKIBackupRestoreResult{}, err
	}
	if err := validatePKIBackupAuthorityKeys(staged.State, payload.AuthorityKeys); err != nil {
		return PKIBackupRestoreResult{}, err
	}

	current, err := s.restoreTarget.CurrentPKIBackupTarget(ctx)
	if err != nil {
		return PKIBackupRestoreResult{}, fmt.Errorf("read current PKI restore target: %w", err)
	}
	if err := validatePKIBackupTargetState(current); err != nil {
		return PKIBackupRestoreResult{}, err
	}
	if staged.SchemaVersion != current.SQLiteSchemaVersion || !equalPKIBackupEncodedDigest(staged.SchemaSHA256, current.SQLiteSchemaSHA256) {
		return PKIBackupRestoreResult{}, fmt.Errorf("%w: backup schema does not match the trusted target schema", ErrPKIBackupSchema)
	}
	if !options.Force && current.Initialized && current.PKIDomainID != manifest.PKIDomainID {
		return PKIBackupRestoreResult{}, fmt.Errorf("%w: backup and target PKI domains differ", ErrPKIBackupActivation)
	}
	if !options.Force && !current.Initialized {
		return PKIBackupRestoreResult{}, fmt.Errorf("%w: an uninitialized target requires force activation", ErrPKIBackupActivation)
	}

	activationVersion := manifest.Version
	activationSnapshot := staged.Snapshot
	if options.Force {
		activationVersion, err = NextForcedPKISecurityVersion(current.Version, manifest.Version)
		if err != nil {
			return PKIBackupRestoreResult{}, err
		}
		forced, forceErr := stagePKIBackupSQLite(ctx, staged.Snapshot, pkiBackupStageOptions{
			Sanitize: false, ForceVersion: &activationVersion,
		})
		if forceErr != nil {
			return PKIBackupRestoreResult{}, forceErr
		}
		defer clear(forced.Snapshot)
		activationSnapshot = forced.Snapshot
		if forced.State.Settings == nil || forced.State.Settings.PKIEpoch != activationVersion.PKIEpoch || forced.State.Settings.SecurityRevision != 0 {
			return PKIBackupRestoreResult{}, fmt.Errorf("%w: forced staging version was not applied", ErrPKIBackupIntegrity)
		}
	} else {
		if current.Initialized {
			if err := ValidatePKISecuritySnapshot(current.Version, PKISecuritySnapshotVersion{Version: manifest.Version, Full: manifest.Full}); err != nil {
				return PKIBackupRestoreResult{}, err
			}
			if initialGrant.PKIDomainID != current.PKIDomainID || initialGrant.PKIEpoch != current.Version.PKIEpoch {
				return PKIBackupRestoreResult{}, fmt.Errorf("authorize PKI backup restore target: %w", ErrPKILeaseNotHeld)
			}
		}
		finalGrant, leaseErr := s.leaseGate.RequirePKILease(ctx)
		if leaseErr != nil || !samePKILeaseAuthority(initialGrant, finalGrant) {
			if leaseErr != nil {
				return PKIBackupRestoreResult{}, fmt.Errorf("recheck PKI backup restore lease: %w", leaseErr)
			}
			return PKIBackupRestoreResult{}, fmt.Errorf("recheck PKI backup restore lease: %w", ErrPKILeaseNotHeld)
		}
	}

	request := PKIBackupActivationRequest{
		ExpectedTarget: current, PKIDomainID: manifest.PKIDomainID, Version: activationVersion,
		Full: true, Forced: options.Force, SQLiteSnapshot: activationSnapshot,
		AuthorityKeys: payload.AuthorityKeys, AuthenticatedFrom: manifest,
	}
	if err := s.restoreTarget.ActivateProtectedPKIBackup(ctx, request); err != nil {
		return PKIBackupRestoreResult{}, fmt.Errorf("%w: %v", ErrPKIBackupActivation, err)
	}
	return PKIBackupRestoreResult{PKIDomainID: manifest.PKIDomainID, Version: activationVersion, Forced: options.Force}, nil
}

func (s *PKIBackupService) exportAuthorityKeys(ctx context.Context, state storage.PKICanonicalState, grant PKILeaseGrant) ([]PKIBackupAuthorityKey, error) {
	authorities := append([]storage.PKIAuthorityRow(nil), state.Authorities...)
	sort.Slice(authorities, func(i, j int) bool {
		if authorities[i].Generation != authorities[j].Generation {
			return authorities[i].Generation < authorities[j].Generation
		}
		return authorities[i].ID < authorities[j].ID
	})
	keys := make([]PKIBackupAuthorityKey, 0, len(authorities))
	for _, authority := range authorities {
		if !pkiBackupAuthorityRequiresKey(authority) {
			continue
		}
		if authority.EncryptedKeyRef == nil || strings.TrimSpace(*authority.EncryptedKeyRef) == "" || authority.PrivateKeyDestroyedAt != nil {
			clearPKIBackupAuthorityKeys(keys)
			return nil, fmt.Errorf("%w: authority %q has no restorable private key", ErrPKIBackupIntegrity, authority.ID)
		}
		before, err := s.leaseGate.RequirePKILease(ctx)
		if err != nil || !samePKILeaseAuthority(grant, before) {
			clearPKIBackupAuthorityKeys(keys)
			if err != nil {
				return nil, fmt.Errorf("authorize authority %q export: %w", authority.ID, err)
			}
			return nil, fmt.Errorf("authorize authority %q export: %w", authority.ID, ErrPKILeaseNotHeld)
		}
		plaintext, err := s.authorityKeySource.ExportPKIAuthorityKey(ctx, authority)
		if err != nil {
			clearPKIBackupAuthorityKeys(keys)
			return nil, fmt.Errorf("export authority %q key: %w", authority.ID, err)
		}
		after, leaseErr := s.leaseGate.RequirePKILease(ctx)
		if leaseErr != nil || !samePKILeaseAuthority(grant, after) {
			clear(plaintext)
			clearPKIBackupAuthorityKeys(keys)
			if leaseErr != nil {
				return nil, fmt.Errorf("recheck authority %q export lease: %w", authority.ID, leaseErr)
			}
			return nil, fmt.Errorf("recheck authority %q export lease: %w", authority.ID, ErrPKILeaseNotHeld)
		}
		signer, parseErr := parsePKIAuthorityPrivateKey(plaintext)
		clear(plaintext)
		if parseErr != nil {
			clearPKIBackupAuthorityKeys(keys)
			return nil, fmt.Errorf("%w: parse authority %q key: %v", ErrPKIBackupIntegrity, authority.ID, parseErr)
		}
		certificate, certificateErr := parsePKIAuthorityCertificate(authority.CertificatePEM)
		if certificateErr != nil {
			clearPKIBackupAuthorityKeys(keys)
			return nil, fmt.Errorf("%w: parse authority %q certificate: %v", ErrPKIBackupIntegrity, authority.ID, certificateErr)
		}
		if signerErr := validatePKIAuthoritySigner(signer, certificate); signerErr != nil {
			clearPKIBackupAuthorityKeys(keys)
			return nil, fmt.Errorf("%w: authority %q key does not match its certificate", ErrPKIBackupIntegrity, authority.ID)
		}
		pkcs8, marshalErr := x509.MarshalPKCS8PrivateKey(signer)
		if marshalErr != nil {
			clearPKIBackupAuthorityKeys(keys)
			return nil, fmt.Errorf("marshal authority %q key: %w", authority.ID, marshalErr)
		}
		keys = append(keys, PKIBackupAuthorityKey{AuthorityID: authority.ID, Generation: authority.Generation, PKCS8: pkcs8})
	}
	return keys, nil
}

func (s *PKIBackupService) sealPKIBackup(passphrase []byte, manifest PKIBackupManifest, snapshot []byte, keys []PKIBackupAuthorityKey) ([]byte, error) {
	payloadBytes, err := json.Marshal(pkiBackupPayload{SQLiteSnapshot: snapshot, AuthorityKeys: keys})
	if err != nil {
		return nil, fmt.Errorf("encode PKI backup payload: %w", err)
	}
	defer clear(payloadBytes)

	salt := make([]byte, pkiBackupSaltBytes)
	if _, err := io.ReadFull(s.random, salt); err != nil {
		return nil, fmt.Errorf("generate PKI backup salt: %w", err)
	}
	kdf := PKIBackupKDFManifest{
		Algorithm: pkiBackupKDFAlgorithm, MemoryKiB: pkiBackupKDFMemoryKiB,
		Iterations: pkiBackupKDFTime, Parallelism: pkiBackupKDFThreads,
		KeyBytes: pkiBackupKDFKeyBytes, Salt: salt,
	}
	key := argon2.IDKey(passphrase, salt, kdf.Iterations, kdf.MemoryKiB, kdf.Parallelism, kdf.KeyBytes)
	defer clear(key)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(s.random, nonce); err != nil {
		return nil, fmt.Errorf("generate PKI backup nonce: %w", err)
	}
	envelope := PKIProtectedBackupEnvelope{
		Format: PKIProtectedBackupFormat, KDF: kdf,
		Cipher:   PKIBackupCipherManifest{Algorithm: pkiBackupCipherAlgorithm, Nonce: nonce},
		Manifest: manifest,
	}
	aad, err := pkiBackupAAD(envelope)
	if err != nil {
		return nil, err
	}
	envelope.Ciphertext = gcm.Seal(nil, nonce, payloadBytes, aad)
	encoded, err := json.Marshal(envelope)
	if err != nil {
		return nil, fmt.Errorf("encode protected PKI backup: %w", err)
	}
	return encoded, nil
}

func openPKIBackup(passphrase, archive []byte) (PKIBackupManifest, pkiBackupPayload, error) {
	if len(archive) == 0 || len(archive) > pkiBackupMaxEnvelopeSize {
		return PKIBackupManifest{}, pkiBackupPayload{}, fmt.Errorf("%w: envelope size is invalid", ErrPKIBackupInvalid)
	}
	var envelope PKIProtectedBackupEnvelope
	decoder := json.NewDecoder(bytes.NewReader(archive))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&envelope); err != nil {
		return PKIBackupManifest{}, pkiBackupPayload{}, fmt.Errorf("%w: decode envelope", ErrPKIBackupInvalid)
	}
	if err := ensurePKIBackupJSONEOF(decoder); err != nil {
		return PKIBackupManifest{}, pkiBackupPayload{}, err
	}
	if err := validatePKIBackupEnvelopeMetadata(envelope); err != nil {
		return PKIBackupManifest{}, pkiBackupPayload{}, err
	}
	key := argon2.IDKey(passphrase, envelope.KDF.Salt, envelope.KDF.Iterations, envelope.KDF.MemoryKiB, envelope.KDF.Parallelism, envelope.KDF.KeyBytes)
	defer clear(key)
	block, err := aes.NewCipher(key)
	if err != nil {
		return PKIBackupManifest{}, pkiBackupPayload{}, fmt.Errorf("%w: initialize cipher", ErrPKIBackupInvalid)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return PKIBackupManifest{}, pkiBackupPayload{}, fmt.Errorf("%w: initialize AEAD", ErrPKIBackupInvalid)
	}
	aad, err := pkiBackupAAD(envelope)
	if err != nil {
		return PKIBackupManifest{}, pkiBackupPayload{}, err
	}
	plaintext, err := gcm.Open(nil, envelope.Cipher.Nonce, envelope.Ciphertext, aad)
	if err != nil {
		return PKIBackupManifest{}, pkiBackupPayload{}, ErrPKIBackupAuthentication
	}
	defer clear(plaintext)
	var payload pkiBackupPayload
	payloadDecoder := json.NewDecoder(bytes.NewReader(plaintext))
	payloadDecoder.DisallowUnknownFields()
	if err := payloadDecoder.Decode(&payload); err != nil {
		return PKIBackupManifest{}, pkiBackupPayload{}, fmt.Errorf("%w: decode authenticated payload", ErrPKIBackupIntegrity)
	}
	if err := ensurePKIBackupJSONEOF(payloadDecoder); err != nil {
		clearPKIBackupPayload(&payload)
		return PKIBackupManifest{}, pkiBackupPayload{}, fmt.Errorf("%w: trailing authenticated payload", ErrPKIBackupIntegrity)
	}
	if len(payload.SQLiteSnapshot) == 0 || len(payload.SQLiteSnapshot) > pkiBackupMaxSnapshotSize {
		clearPKIBackupPayload(&payload)
		return PKIBackupManifest{}, pkiBackupPayload{}, fmt.Errorf("%w: SQLite payload size is invalid", ErrPKIBackupIntegrity)
	}
	return envelope.Manifest, payload, nil
}

type pkiBackupPayload struct {
	SQLiteSnapshot []byte                  `json:"sqlite_snapshot"`
	AuthorityKeys  []PKIBackupAuthorityKey `json:"authority_keys"`
}

type pkiBackupAADEnvelope struct {
	Format   string                  `json:"format"`
	KDF      PKIBackupKDFManifest    `json:"kdf"`
	Cipher   PKIBackupCipherManifest `json:"cipher"`
	Manifest PKIBackupManifest       `json:"manifest"`
}

func pkiBackupAAD(envelope PKIProtectedBackupEnvelope) ([]byte, error) {
	encoded, err := json.Marshal(pkiBackupAADEnvelope{
		Format: envelope.Format, KDF: envelope.KDF, Cipher: envelope.Cipher, Manifest: envelope.Manifest,
	})
	if err != nil {
		return nil, fmt.Errorf("encode PKI backup authenticated metadata: %w", err)
	}
	return encoded, nil
}

func validatePKIBackupEnvelopeMetadata(envelope PKIProtectedBackupEnvelope) error {
	if envelope.Format != PKIProtectedBackupFormat || envelope.KDF.Algorithm != pkiBackupKDFAlgorithm ||
		envelope.KDF.MemoryKiB != pkiBackupKDFMemoryKiB || envelope.KDF.Iterations != pkiBackupKDFTime ||
		envelope.KDF.Parallelism != pkiBackupKDFThreads || envelope.KDF.KeyBytes != pkiBackupKDFKeyBytes ||
		len(envelope.KDF.Salt) != pkiBackupSaltBytes || envelope.Cipher.Algorithm != pkiBackupCipherAlgorithm ||
		len(envelope.Cipher.Nonce) != 12 || len(envelope.Ciphertext) < 16 {
		return fmt.Errorf("%w: unsupported or malformed cryptographic parameters", ErrPKIBackupInvalid)
	}
	if envelope.Manifest.FormatVersion != PKIBackupFormatVersion || strings.TrimSpace(envelope.Manifest.PKIDomainID) == "" || envelope.Manifest.Version.PKIEpoch < 0 ||
		envelope.Manifest.Version.SecurityRevision < 0 || !envelope.Manifest.Full || envelope.Manifest.ExportedAt.IsZero() ||
		envelope.Manifest.EnrollmentPolicy != pkiBackupTokenPolicy || envelope.Manifest.EnrollmentTokens != 0 {
		return fmt.Errorf("%w: manifest metadata is incomplete", ErrPKIBackupInvalid)
	}
	return nil
}

func validatePKIBackupManifest(manifest PKIBackupManifest, staged pkiBackupSQLiteStage, keys []PKIBackupAuthorityKey) error {
	if manifest.EnrollmentTokens != 0 || staged.EnrollmentTokens != 0 {
		return fmt.Errorf("%w: enrollment tokens are forbidden in a PKI backup", ErrPKIBackupIntegrity)
	}
	if manifest.SQLiteBytes != int64(len(staged.Snapshot)) || !equalPKIBackupDigest(manifest.SQLiteSHA256, staged.Snapshot) ||
		manifest.SQLiteSchema != staged.SchemaVersion || !strings.EqualFold(manifest.SQLiteSchemaSHA256, staged.SchemaSHA256) {
		return fmt.Errorf("%w: SQLite snapshot metadata does not match", ErrPKIBackupIntegrity)
	}
	if staged.State.Settings == nil || staged.State.Settings.PKIDomainID != manifest.PKIDomainID ||
		staged.State.Settings.PKIEpoch != manifest.Version.PKIEpoch || staged.State.Settings.SecurityRevision != manifest.Version.SecurityRevision {
		return fmt.Errorf("%w: canonical PKI settings do not match the manifest", ErrPKIBackupIntegrity)
	}
	want := append([]PKIBackupAuthorityManifest(nil), manifest.AuthorityKeys...)
	sort.Slice(want, func(i, j int) bool { return pkiBackupAuthorityManifestLess(want[i], want[j]) })
	got := authorityKeyManifest(keys, staged.State.Authorities)
	if len(want) != len(got) {
		return fmt.Errorf("%w: authority key count does not match", ErrPKIBackupIntegrity)
	}
	for index := range want {
		if want[index] != got[index] {
			return fmt.Errorf("%w: authority key metadata does not match", ErrPKIBackupIntegrity)
		}
	}
	return nil
}

func validatePKIBackupAuthorityKeys(state storage.PKICanonicalState, keys []PKIBackupAuthorityKey) error {
	authorities := make(map[string]storage.PKIAuthorityRow, len(state.Authorities))
	required := 0
	for _, authority := range state.Authorities {
		authorities[authority.ID] = authority
		if pkiBackupAuthorityRequiresKey(authority) {
			required++
		}
	}
	if len(keys) != required {
		return fmt.Errorf("%w: restorable authority key set is incomplete", ErrPKIBackupIntegrity)
	}
	seen := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		if _, duplicate := seen[key.AuthorityID]; duplicate {
			return fmt.Errorf("%w: duplicate authority key %q", ErrPKIBackupIntegrity, key.AuthorityID)
		}
		seen[key.AuthorityID] = struct{}{}
		authority, found := authorities[key.AuthorityID]
		if !found || !pkiBackupAuthorityRequiresKey(authority) || authority.Generation != key.Generation || authority.PrivateKeyDestroyedAt != nil {
			return fmt.Errorf("%w: authority key identity does not match canonical state", ErrPKIBackupIntegrity)
		}
		signer, err := parsePKIAuthorityPrivateKey(key.PKCS8)
		if err != nil {
			return fmt.Errorf("%w: authority %q key is malformed", ErrPKIBackupIntegrity, key.AuthorityID)
		}
		certificate, err := parsePKIAuthorityCertificate(authority.CertificatePEM)
		if err != nil || validatePKIAuthoritySigner(signer, certificate) != nil {
			return fmt.Errorf("%w: authority %q key does not match its certificate", ErrPKIBackupIntegrity, key.AuthorityID)
		}
	}
	return nil
}

func buildPKIBackupManifest(staged pkiBackupSQLiteStage, keys []PKIBackupAuthorityKey, exportedAt time.Time) PKIBackupManifest {
	settings := staged.State.Settings
	return PKIBackupManifest{
		FormatVersion: PKIBackupFormatVersion,
		PKIDomainID:   settings.PKIDomainID,
		Version:       PKISecurityVersion{PKIEpoch: settings.PKIEpoch, SecurityRevision: settings.SecurityRevision},
		Full:          true, ExportedAt: exportedAt, SQLiteBytes: int64(len(staged.Snapshot)),
		SQLiteSHA256: pkiBackupDigest(staged.Snapshot), SQLiteSchema: staged.SchemaVersion,
		SQLiteSchemaSHA256: staged.SchemaSHA256, EnrollmentPolicy: pkiBackupTokenPolicy, EnrollmentTokens: staged.EnrollmentTokens,
		AuthorityKeys: authorityKeyManifest(keys, staged.State.Authorities),
	}
}

func authorityKeyManifest(keys []PKIBackupAuthorityKey, authorities []storage.PKIAuthorityRow) []PKIBackupAuthorityManifest {
	statuses := make(map[string]string, len(authorities))
	for _, authority := range authorities {
		statuses[authority.ID] = authority.Status
	}
	result := make([]PKIBackupAuthorityManifest, 0, len(keys))
	for _, key := range keys {
		result = append(result, PKIBackupAuthorityManifest{
			AuthorityID: key.AuthorityID, Generation: key.Generation, Status: statuses[key.AuthorityID],
			PKCS8Bytes: int64(len(key.PKCS8)), PKCS8SHA256: pkiBackupDigest(key.PKCS8),
		})
	}
	sort.Slice(result, func(i, j int) bool { return pkiBackupAuthorityManifestLess(result[i], result[j]) })
	return result
}

func pkiBackupAuthorityManifestLess(left, right PKIBackupAuthorityManifest) bool {
	if left.Generation != right.Generation {
		return left.Generation < right.Generation
	}
	return left.AuthorityID < right.AuthorityID
}

func pkiBackupAuthorityRequiresKey(authority storage.PKIAuthorityRow) bool {
	switch strings.ToLower(strings.TrimSpace(authority.Status)) {
	case "active", "retiring":
		return true
	default:
		return false
	}
}

func validatePKIBackupTargetState(state PKIBackupTargetState) error {
	if state.SQLiteSchemaVersion < 0 || !validPKIBackupEncodedDigest(state.SQLiteSchemaSHA256) {
		return fmt.Errorf("%w: target schema baseline is missing or malformed", ErrPKIBackupSchema)
	}
	if !state.Initialized {
		if state.PKIDomainID != "" || state.Version != (PKISecurityVersion{}) {
			return fmt.Errorf("%w: uninitialized target carries canonical PKI state", ErrPKIBackupActivation)
		}
		return nil
	}
	if strings.TrimSpace(state.PKIDomainID) == "" || state.Version.PKIEpoch < 0 || state.Version.SecurityRevision < 0 {
		return fmt.Errorf("%w: current target state is malformed", ErrPKIBackupActivation)
	}
	return nil
}

func validatePKIBackupPassphrase(passphrase []byte) error {
	if len(passphrase) == 0 || len(passphrase) > 1<<20 {
		return fmt.Errorf("%w: passphrase size is invalid", ErrPKIBackupInvalid)
	}
	return nil
}

func pkiBackupDigest(value []byte) string {
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:])
}

func equalPKIBackupDigest(encoded string, value []byte) bool {
	want, err := hex.DecodeString(strings.TrimSpace(encoded))
	if err != nil || len(want) != sha256.Size {
		return false
	}
	got := sha256.Sum256(value)
	return subtle.ConstantTimeCompare(want, got[:]) == 1
}

func validPKIBackupEncodedDigest(value string) bool {
	decoded, err := hex.DecodeString(strings.TrimSpace(value))
	return err == nil && len(decoded) == sha256.Size
}

func equalPKIBackupEncodedDigest(left, right string) bool {
	leftBytes, leftErr := hex.DecodeString(strings.TrimSpace(left))
	rightBytes, rightErr := hex.DecodeString(strings.TrimSpace(right))
	if leftErr != nil || rightErr != nil || len(leftBytes) != sha256.Size || len(rightBytes) != sha256.Size {
		return false
	}
	return subtle.ConstantTimeCompare(leftBytes, rightBytes) == 1
}

func clearPKIBackupAuthorityKeys(keys []PKIBackupAuthorityKey) {
	for index := range keys {
		clear(keys[index].PKCS8)
	}
}

func clearPKIBackupPayload(payload *pkiBackupPayload) {
	if payload == nil {
		return
	}
	clear(payload.SQLiteSnapshot)
	clearPKIBackupAuthorityKeys(payload.AuthorityKeys)
}

func ensurePKIBackupJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return fmt.Errorf("%w: JSON document has trailing content", ErrPKIBackupInvalid)
	}
	return nil
}

// PKIVaultBackupKeySource adapts the existing encrypted vault to the narrow
// backup key-source contract without exposing its master key.
type PKIVaultBackupKeySource struct {
	vault PKIVaultKeyReader
}

func NewPKIVaultBackupKeySource(vault PKIVaultKeyReader) (*PKIVaultBackupKeySource, error) {
	if vault == nil {
		return nil, fmt.Errorf("%w: PKI vault is required", ErrPKIBackupInvalid)
	}
	return &PKIVaultBackupKeySource{vault: vault}, nil
}

func (s *PKIVaultBackupKeySource) ExportPKIAuthorityKey(_ context.Context, authority storage.PKIAuthorityRow) ([]byte, error) {
	if s == nil || s.vault == nil || authority.EncryptedKeyRef == nil {
		return nil, fmt.Errorf("%w: authority key reference is unavailable", ErrPKIBackupIntegrity)
	}
	return s.vault.OpenCAKey(strings.TrimSpace(*authority.EncryptedKeyRef), authority.PKIDomainID, authority.Generation, pkiBackupCAPurpose)
}
