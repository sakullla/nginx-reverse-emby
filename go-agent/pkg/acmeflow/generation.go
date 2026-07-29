package acmeflow

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"os"
	"sort"
	"strings"
	"time"
)

const (
	GenerationManifestVersion = 1
	CurrentReferenceVersion   = 1
	PendingReferenceVersion   = 1

	stagingDirectory          = ".staging"
	generationsDirectory      = "generations"
	currentDirectory          = "current"
	pendingDirectory          = "pending"
	pendingReferenceFile      = "reference.json"
	generationCertificateFile = "certificate.pem"
	generationPrivateKeyFile  = "private-key.pem"
	generationAccountFile     = "account.json"
	generationManifestFile    = "manifest.json"
)

var (
	ErrNoCurrentGeneration = errors.New("acmeflow: no current generation")
	ErrNoPendingGeneration = errors.New("acmeflow: no pending generation")
)

// PendingGenerationInput binds a staged generation to the owner policy that
// may safely recover it after a restart. The reference is made durable before
// the immutable generation directory is committed.
type PendingGenerationInput struct {
	PreviousGenerationID string
	PolicySHA256         string
	RecordRenewal        bool
}

// GenerationInput is material that has not yet crossed the immutable stage
// boundary. Policy is persisted in normalized form through the manifest.
type GenerationInput struct {
	Material CertificateMaterial
	Policy   MaterialPolicy
	Account  AccountMetadata
	Pending  *PendingGenerationInput
}

// GenerationManifest is written last in a staged generation and binds all
// three persisted files. Complete must be true before a generation can be
// committed or referenced.
type GenerationManifest struct {
	Version           int          `json:"version"`
	ID                string       `json:"id"`
	CreatedAt         time.Time    `json:"created_at"`
	Complete          bool         `json:"complete"`
	Profile           string       `json:"profile,omitempty"`
	Identifiers       []Identifier `json:"identifiers"`
	CertificateFile   string       `json:"certificate_file"`
	PrivateKeyFile    string       `json:"private_key_file"`
	AccountFile       string       `json:"account_file"`
	CertificateSHA256 string       `json:"certificate_sha256"`
	PrivateKeySHA256  string       `json:"private_key_sha256"`
	AccountSHA256     string       `json:"account_sha256"`
	MaterialSHA256    string       `json:"material_sha256"`
}

// CurrentReference occupies one of two alternating slots. A loader validates
// both slots and falls back to the previous complete revision if the latest
// reference or generation was truncated.
type CurrentReference struct {
	Version        int       `json:"version"`
	Revision       uint64    `json:"revision"`
	GenerationID   string    `json:"generation_id"`
	ManifestSHA256 string    `json:"manifest_sha256"`
	PromotedAt     time.Time `json:"promoted_at"`
}

type PendingGenerationReference struct {
	Version              int    `json:"version"`
	GenerationID         string `json:"generation_id"`
	ManifestSHA256       string `json:"manifest_sha256"`
	PreviousGenerationID string `json:"previous_generation_id,omitempty"`
	PolicySHA256         string `json:"policy_sha256"`
	RecordRenewal        bool   `json:"record_renewal"`
}

type Generation struct {
	Manifest GenerationManifest
	Material CertificateMaterial
	Account  AccountMetadata
}

type PendingGeneration struct {
	Reference  PendingGenerationReference
	Generation Generation
}

type LegacyProjection interface {
	ProjectGeneration(context.Context, Generation) error
}

type LegacyProjectionFunc func(context.Context, Generation) error

func (projection LegacyProjectionFunc) ProjectGeneration(ctx context.Context, generation Generation) error {
	return projection(ctx, generation)
}

func (store *StateStore) StageGeneration(ctx context.Context, input GenerationInput) (GenerationManifest, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.stageGenerationLocked(ctx, input)
}

func (store *StateStore) stageGenerationLocked(ctx context.Context, input GenerationInput) (GenerationManifest, error) {
	var empty GenerationManifest
	if err := contextError(ctx); err != nil {
		return empty, normalizeError("generation_stage", err)
	}
	input.Material.CertificatePEM = append([]byte(nil), input.Material.CertificatePEM...)
	input.Material.PrivateKeyPEM = append([]byte(nil), input.Material.PrivateKeyPEM...)
	input.Material.Profile = strings.TrimSpace(input.Material.Profile)
	input.Policy.Profile = strings.TrimSpace(input.Policy.Profile)
	pending, err := normalizePendingGenerationInput(input.Pending)
	if err != nil {
		return empty, WrapError(CategoryMaterial, "generation_stage", err)
	}
	now := store.clock().UTC()
	if input.Policy.Now.IsZero() {
		input.Policy.Now = now
	}
	if _, err := ValidateMaterial(input.Material, input.Policy); err != nil {
		return empty, err
	}
	account, err := normalizeAccountMetadata(input.Account)
	if err != nil {
		return empty, WrapError(CategoryMaterial, "generation_stage", err)
	}
	identifiers, err := normalizeIdentifiers(input.Policy.Identifiers)
	if err != nil {
		return empty, WrapError(CategoryMaterial, "generation_stage", err)
	}
	sort.Slice(identifiers, func(i, j int) bool {
		if identifiers[i].Type != identifiers[j].Type {
			return identifiers[i].Type < identifiers[j].Type
		}
		return identifiers[i].Value < identifiers[j].Value
	})
	accountJSON, err := encodeStateJSON(account)
	if err != nil {
		return empty, WrapError(CategoryMaterial, "generation_stage", err)
	}
	identifierJSON, err := encodeStateJSON(identifiers)
	if err != nil {
		return empty, WrapError(CategoryMaterial, "generation_stage", err)
	}
	certificateHash := sha256Hex(input.Material.CertificatePEM)
	privateKeyHash := sha256Hex(input.Material.PrivateKeyPEM)
	accountHash := sha256Hex(accountJSON)
	materialHash := hashParts(input.Material.CertificatePEM, input.Material.PrivateKeyPEM)
	id := generationID(certificateHash, privateKeyHash, accountHash, input.Material.Profile, identifierJSON)
	manifest := GenerationManifest{
		Version:           GenerationManifestVersion,
		ID:                id,
		CreatedAt:         now,
		Complete:          true,
		Profile:           input.Material.Profile,
		Identifiers:       append([]Identifier(nil), identifiers...),
		CertificateFile:   generationCertificateFile,
		PrivateKeyFile:    generationPrivateKeyFile,
		AccountFile:       generationAccountFile,
		CertificateSHA256: certificateHash,
		PrivateKeySHA256:  privateKeyHash,
		AccountSHA256:     accountHash,
		MaterialSHA256:    materialHash,
	}

	if committed, err := store.loadGenerationLocked(ctx, id); err == nil {
		if pending != nil {
			manifestJSON, err := store.fs.readRegularFile(statePath(generationsDirectory, id, generationManifestFile), 1<<20)
			if err != nil {
				return empty, WrapError(CategoryMaterial, "generation_stage", err)
			}
			if err := store.savePendingGenerationReferenceLocked(committed.Manifest, manifestJSON, pending); err != nil {
				return empty, WrapError(CategoryMaterial, "generation_stage", err)
			}
		}
		return committed.Manifest, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		if _, exists, inspectErr := store.fs.readOptionalRegularFile(statePath(generationsDirectory, id, generationManifestFile), 1<<20); inspectErr != nil || exists {
			return empty, WrapError(CategoryMaterial, "generation_stage", errors.New("immutable generation already exists but is invalid"))
		}
	}

	stagePath := statePath(stagingDirectory, id)
	if _, err := store.fs.root.Lstat(stagePath); err == nil {
		if err := store.fs.removeAll(stagePath, "generation.stale_stage"); err != nil {
			return empty, WrapError(CategoryMaterial, "generation_stage", err)
		}
	} else if !os.IsNotExist(err) {
		return empty, WrapError(CategoryMaterial, "generation_stage", errors.New("staged generation could not be inspected"))
	}
	if err := store.fs.makeDirectory(stagePath, "generation.stage"); err != nil {
		return empty, WrapError(CategoryMaterial, "generation_stage", err)
	}
	if err := store.fs.writeFileAtomic(
		statePath(stagePath, generationCertificateFile),
		input.Material.CertificatePEM,
		"generation.certificate",
	); err != nil {
		return empty, WrapError(CategoryMaterial, "generation_stage", err)
	}
	if err := store.fs.writeFileAtomic(
		statePath(stagePath, generationPrivateKeyFile),
		input.Material.PrivateKeyPEM,
		"generation.private_key",
	); err != nil {
		return empty, WrapError(CategoryMaterial, "generation_stage", err)
	}
	if err := store.fs.writeFileAtomic(
		statePath(stagePath, generationAccountFile),
		accountJSON,
		"generation.account",
	); err != nil {
		return empty, WrapError(CategoryMaterial, "generation_stage", err)
	}
	manifestJSON, err := encodeStateJSON(manifest)
	if err != nil {
		return empty, WrapError(CategoryMaterial, "generation_stage", err)
	}
	if err := store.fs.writeFileAtomic(
		statePath(stagePath, generationManifestFile),
		manifestJSON,
		"generation.manifest",
	); err != nil {
		return empty, WrapError(CategoryMaterial, "generation_stage", err)
	}
	if err := store.savePendingGenerationReferenceLocked(manifest, manifestJSON, pending); err != nil {
		return empty, WrapError(CategoryMaterial, "generation_stage", err)
	}
	if err := store.fs.renameDirectory(stagePath, statePath(generationsDirectory, id), "generation.commit"); err != nil {
		return empty, WrapError(CategoryMaterial, "generation_stage", err)
	}
	return cloneGenerationManifest(manifest), nil
}

func (store *StateStore) LoadPendingGeneration(ctx context.Context) (PendingGeneration, error) {
	store.mu.Lock()
	defer store.mu.Unlock()

	pending, err := store.loadPendingGenerationLocked(ctx)
	if err != nil {
		if errors.Is(err, ErrNoPendingGeneration) {
			return PendingGeneration{}, ErrNoPendingGeneration
		}
		return PendingGeneration{}, WrapError(CategoryMaterial, "generation_pending_load", err)
	}
	pending.Generation = cloneGeneration(pending.Generation)
	return pending, nil
}

func (store *StateStore) ClearPendingGeneration(ctx context.Context, id string) error {
	store.mu.Lock()
	defer store.mu.Unlock()

	if err := contextError(ctx); err != nil {
		return normalizeError("generation_pending_clear", err)
	}
	if !isLowerHex(id, sha256.Size*2) {
		return WrapError(CategoryMaterial, "generation_pending_clear", errors.New("generation identifier is invalid"))
	}
	reference, exists, err := store.loadPendingGenerationReferenceLocked()
	if err != nil {
		return WrapError(CategoryMaterial, "generation_pending_clear", err)
	}
	if !exists || reference.GenerationID != id {
		return nil
	}
	if err := store.fs.removeFile(statePath(pendingDirectory, pendingReferenceFile), "pending.reference_clear"); err != nil {
		return WrapError(CategoryMaterial, "generation_pending_clear", err)
	}
	return nil
}

func (store *StateStore) savePendingGenerationReferenceLocked(manifest GenerationManifest, manifestJSON []byte, input *PendingGenerationInput) error {
	if input == nil {
		return nil
	}
	reference := PendingGenerationReference{
		Version:              PendingReferenceVersion,
		GenerationID:         manifest.ID,
		ManifestSHA256:       sha256Hex(manifestJSON),
		PreviousGenerationID: input.PreviousGenerationID,
		PolicySHA256:         input.PolicySHA256,
		RecordRenewal:        input.RecordRenewal,
	}
	data, err := encodeStateJSON(reference)
	if err != nil {
		return err
	}
	return store.fs.writeFileAtomic(statePath(pendingDirectory, pendingReferenceFile), data, "pending.reference")
}

func (store *StateStore) loadPendingGenerationLocked(ctx context.Context) (PendingGeneration, error) {
	if err := contextError(ctx); err != nil {
		return PendingGeneration{}, err
	}
	reference, exists, err := store.loadPendingGenerationReferenceLocked()
	if err != nil {
		return PendingGeneration{}, err
	}
	if !exists {
		return PendingGeneration{}, ErrNoPendingGeneration
	}
	generation, err := store.loadGenerationLocked(ctx, reference.GenerationID)
	if err != nil {
		return PendingGeneration{}, err
	}
	manifestJSON, err := store.fs.readRegularFile(
		statePath(generationsDirectory, reference.GenerationID, generationManifestFile),
		1<<20,
	)
	if err != nil || sha256Hex(manifestJSON) != reference.ManifestSHA256 {
		return PendingGeneration{}, errors.New("pending reference manifest mismatch")
	}
	return PendingGeneration{Reference: reference, Generation: generation}, nil
}

func (store *StateStore) loadPendingGenerationReferenceLocked() (PendingGenerationReference, bool, error) {
	data, exists, err := store.fs.readOptionalRegularFile(statePath(pendingDirectory, pendingReferenceFile), 1<<20)
	if err != nil || !exists {
		return PendingGenerationReference{}, exists, err
	}
	var reference PendingGenerationReference
	if err := decodeStateJSON(data, &reference); err != nil {
		return PendingGenerationReference{}, true, err
	}
	if err := validatePendingGenerationReference(reference); err != nil {
		return PendingGenerationReference{}, true, err
	}
	return reference, true, nil
}

func (store *StateStore) PromoteGeneration(ctx context.Context, id string, projection LegacyProjection) error {
	store.mu.Lock()
	defer store.mu.Unlock()

	if err := contextError(ctx); err != nil {
		return normalizeError("generation_promote", err)
	}
	generation, err := store.loadGenerationLocked(ctx, id)
	if err != nil {
		return WrapError(CategoryMaterial, "generation_promote", err)
	}
	references, _ := store.loadCurrentReferencesLocked()
	var completeCurrent *CurrentReference
	for index := range references {
		candidate, err := store.loadGenerationForReferenceLocked(ctx, references[index])
		if err != nil {
			continue
		}
		completeCurrent = &references[index]
		if candidate.Manifest.ID == id {
			return nil
		}
		break
	}
	if projection != nil {
		if err := projection.ProjectGeneration(ctx, cloneGeneration(generation)); err != nil {
			return WrapError(CategoryMaterial, "generation_projection", err)
		}
	}
	if err := contextError(ctx); err != nil {
		return normalizeError("generation_promote", err)
	}
	var revision uint64 = 1
	if len(references) > 0 {
		if references[0].Revision >= ^uint64(0)-1 {
			return WrapError(CategoryMaterial, "generation_promote", errors.New("current generation revision overflow"))
		}
		revision = references[0].Revision + 1
	}
	if completeCurrent != nil && currentSlotName(revision) == currentSlotName(completeCurrent.Revision) {
		revision++
	}
	manifestJSON, err := store.fs.readRegularFile(statePath(generationsDirectory, id, generationManifestFile), 1<<20)
	if err != nil {
		return WrapError(CategoryMaterial, "generation_promote", err)
	}
	reference := CurrentReference{
		Version:        CurrentReferenceVersion,
		Revision:       revision,
		GenerationID:   id,
		ManifestSHA256: sha256Hex(manifestJSON),
		PromotedAt:     store.clock().UTC(),
	}
	data, err := encodeStateJSON(reference)
	if err != nil {
		return WrapError(CategoryMaterial, "generation_promote", err)
	}
	if err := store.fs.writeFileAtomic(
		statePath(currentDirectory, currentSlotName(revision)),
		data,
		"current.slot",
	); err != nil {
		return WrapError(CategoryMaterial, "generation_promote", err)
	}
	return nil
}

func (store *StateStore) LoadGeneration(ctx context.Context, id string) (Generation, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	generation, err := store.loadGenerationLocked(ctx, id)
	if err != nil {
		return Generation{}, WrapError(CategoryMaterial, "generation_load", err)
	}
	return generation, nil
}

func (store *StateStore) loadGenerationLocked(ctx context.Context, id string) (Generation, error) {
	if err := contextError(ctx); err != nil {
		return Generation{}, err
	}
	if !isLowerHex(id, sha256.Size*2) {
		return Generation{}, errors.New("generation identifier is invalid")
	}
	directory := statePath(generationsDirectory, id)
	manifestJSON, err := store.fs.readRegularFile(statePath(directory, generationManifestFile), 1<<20)
	if err != nil {
		return Generation{}, err
	}
	var manifest GenerationManifest
	if err := decodeStateJSON(manifestJSON, &manifest); err != nil {
		return Generation{}, err
	}
	manifest, err = normalizeGenerationManifest(manifest, id)
	if err != nil {
		return Generation{}, err
	}
	certificatePEM, err := store.fs.readRegularFile(statePath(directory, manifest.CertificateFile), maxStateFileSize)
	if err != nil {
		return Generation{}, err
	}
	privateKeyPEM, err := store.fs.readRegularFile(statePath(directory, manifest.PrivateKeyFile), maxStateFileSize)
	if err != nil {
		return Generation{}, err
	}
	accountJSON, err := store.fs.readRegularFile(statePath(directory, manifest.AccountFile), 1<<20)
	if err != nil {
		return Generation{}, err
	}
	if sha256Hex(certificatePEM) != manifest.CertificateSHA256 ||
		sha256Hex(privateKeyPEM) != manifest.PrivateKeySHA256 ||
		sha256Hex(accountJSON) != manifest.AccountSHA256 ||
		hashParts(certificatePEM, privateKeyPEM) != manifest.MaterialSHA256 {
		return Generation{}, errors.New("generation file hash mismatch")
	}
	identifierJSON, err := encodeStateJSON(manifest.Identifiers)
	if err != nil || generationID(
		manifest.CertificateSHA256,
		manifest.PrivateKeySHA256,
		manifest.AccountSHA256,
		manifest.Profile,
		identifierJSON,
	) != manifest.ID {
		return Generation{}, errors.New("generation identity hash mismatch")
	}
	var account AccountMetadata
	if err := decodeStateJSON(accountJSON, &account); err != nil {
		return Generation{}, err
	}
	account, err = normalizeAccountMetadata(account)
	if err != nil {
		return Generation{}, err
	}
	material := CertificateMaterial{
		CertificatePEM: append([]byte(nil), certificatePEM...),
		PrivateKeyPEM:  append([]byte(nil), privateKeyPEM...),
		Profile:        manifest.Profile,
	}
	if _, err := ValidateMaterial(material, MaterialPolicy{
		Identifiers:  append([]Identifier(nil), manifest.Identifiers...),
		Profile:      manifest.Profile,
		Now:          manifest.CreatedAt,
		MaxClockSkew: 0,
	}); err != nil {
		return Generation{}, err
	}
	return Generation{
		Manifest: cloneGenerationManifest(manifest),
		Material: material,
		Account:  cloneAccountMetadata(account),
	}, nil
}

func (store *StateStore) LoadCurrent(ctx context.Context) (Generation, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.loadCurrentLocked(ctx)
}

func (store *StateStore) loadCurrentLocked(ctx context.Context) (Generation, error) {
	if err := contextError(ctx); err != nil {
		return Generation{}, normalizeError("generation_current_load", err)
	}
	references, filesPresent := store.loadCurrentReferencesLocked()
	if !filesPresent {
		return Generation{}, ErrNoCurrentGeneration
	}
	for _, reference := range references {
		generation, err := store.loadGenerationForReferenceLocked(ctx, reference)
		if err == nil {
			return generation, nil
		}
	}
	return Generation{}, WrapError(CategoryMaterial, "generation_current_load", errors.New("no complete current generation"))
}

func (store *StateStore) loadGenerationForReferenceLocked(ctx context.Context, reference CurrentReference) (Generation, error) {
	generation, err := store.loadGenerationLocked(ctx, reference.GenerationID)
	if err != nil {
		return Generation{}, err
	}
	manifestJSON, err := store.fs.readRegularFile(
		statePath(generationsDirectory, reference.GenerationID, generationManifestFile),
		1<<20,
	)
	if err != nil || sha256Hex(manifestJSON) != reference.ManifestSHA256 {
		return Generation{}, errors.New("current reference manifest mismatch")
	}
	return generation, nil
}

func (store *StateStore) loadCurrentReferencesLocked() ([]CurrentReference, bool) {
	references := make([]CurrentReference, 0, 2)
	filesPresent := false
	for slot := 0; slot < 2; slot++ {
		data, exists, err := store.fs.readOptionalRegularFile(statePath(currentDirectory, currentSlotNameForSlot(slot)), 1<<20)
		if err != nil || !exists {
			filesPresent = filesPresent || exists || err != nil
			continue
		}
		filesPresent = true
		var reference CurrentReference
		if err := decodeStateJSON(data, &reference); err != nil {
			continue
		}
		if err := validateCurrentReference(reference); err != nil {
			continue
		}
		if currentSlotName(reference.Revision) != currentSlotNameForSlot(slot) {
			continue
		}
		references = append(references, reference)
	}
	sort.Slice(references, func(i, j int) bool { return references[i].Revision > references[j].Revision })
	return references, filesPresent
}

func normalizeGenerationManifest(manifest GenerationManifest, id string) (GenerationManifest, error) {
	if manifest.Version != GenerationManifestVersion || !manifest.Complete || manifest.ID != id || !isLowerHex(id, sha256.Size*2) {
		return GenerationManifest{}, errors.New("generation manifest identity is invalid")
	}
	if manifest.CreatedAt.IsZero() || manifest.CreatedAt.Location() != time.UTC {
		return GenerationManifest{}, errors.New("generation manifest time is invalid")
	}
	if manifest.CertificateFile != generationCertificateFile ||
		manifest.PrivateKeyFile != generationPrivateKeyFile ||
		manifest.AccountFile != generationAccountFile {
		return GenerationManifest{}, errors.New("generation manifest file names are invalid")
	}
	if !isLowerHex(manifest.CertificateSHA256, sha256.Size*2) ||
		!isLowerHex(manifest.PrivateKeySHA256, sha256.Size*2) ||
		!isLowerHex(manifest.AccountSHA256, sha256.Size*2) ||
		!isLowerHex(manifest.MaterialSHA256, sha256.Size*2) {
		return GenerationManifest{}, errors.New("generation manifest hash is invalid")
	}
	identifiers, err := normalizeIdentifiers(manifest.Identifiers)
	if err != nil {
		return GenerationManifest{}, errors.New("generation manifest identifiers are invalid")
	}
	sort.Slice(identifiers, func(i, j int) bool {
		if identifiers[i].Type != identifiers[j].Type {
			return identifiers[i].Type < identifiers[j].Type
		}
		return identifiers[i].Value < identifiers[j].Value
	})
	if len(identifiers) != len(manifest.Identifiers) {
		return GenerationManifest{}, errors.New("generation manifest identifiers are invalid")
	}
	for index := range identifiers {
		if identifiers[index] != manifest.Identifiers[index] {
			return GenerationManifest{}, errors.New("generation manifest identifiers are not canonical")
		}
	}
	manifest.Profile = strings.TrimSpace(manifest.Profile)
	manifest.Identifiers = identifiers
	return manifest, nil
}

func validateCurrentReference(reference CurrentReference) error {
	if reference.Version != CurrentReferenceVersion || reference.Revision == 0 ||
		!isLowerHex(reference.GenerationID, sha256.Size*2) ||
		!isLowerHex(reference.ManifestSHA256, sha256.Size*2) ||
		reference.PromotedAt.IsZero() || reference.PromotedAt.Location() != time.UTC {
		return errors.New("current generation reference is invalid")
	}
	return nil
}

func normalizePendingGenerationInput(input *PendingGenerationInput) (*PendingGenerationInput, error) {
	if input == nil {
		return nil, nil
	}
	result := *input
	result.PreviousGenerationID = strings.TrimSpace(result.PreviousGenerationID)
	result.PolicySHA256 = strings.TrimSpace(result.PolicySHA256)
	if result.PreviousGenerationID != "" && !isLowerHex(result.PreviousGenerationID, sha256.Size*2) {
		return nil, errors.New("previous generation identifier is invalid")
	}
	if !isLowerHex(result.PolicySHA256, sha256.Size*2) {
		return nil, errors.New("pending generation policy hash is invalid")
	}
	return &result, nil
}

func validatePendingGenerationReference(reference PendingGenerationReference) error {
	if reference.Version != PendingReferenceVersion ||
		!isLowerHex(reference.GenerationID, sha256.Size*2) ||
		!isLowerHex(reference.ManifestSHA256, sha256.Size*2) ||
		!isLowerHex(reference.PolicySHA256, sha256.Size*2) ||
		reference.PreviousGenerationID != "" && !isLowerHex(reference.PreviousGenerationID, sha256.Size*2) {
		return errors.New("pending generation reference is invalid")
	}
	return nil
}

func currentSlotName(revision uint64) string {
	return currentSlotNameForSlot(int(revision % 2))
}

func currentSlotNameForSlot(slot int) string {
	if slot == 0 {
		return "slot-0.json"
	}
	return "slot-1.json"
}

func sha256Hex(data []byte) string {
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

func hashParts(parts ...[]byte) string {
	hash := sha256.New()
	var length [8]byte
	for _, part := range parts {
		binary.BigEndian.PutUint64(length[:], uint64(len(part)))
		_, _ = hash.Write(length[:])
		_, _ = hash.Write(part)
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func generationID(certificateHash, privateKeyHash, accountHash, profile string, identifierJSON []byte) string {
	return hashParts(
		[]byte(certificateHash),
		[]byte(privateKeyHash),
		[]byte(accountHash),
		[]byte(profile),
		identifierJSON,
	)
}

func cloneGenerationManifest(manifest GenerationManifest) GenerationManifest {
	manifest.Identifiers = append([]Identifier(nil), manifest.Identifiers...)
	return manifest
}

func cloneGeneration(generation Generation) Generation {
	generation.Manifest = cloneGenerationManifest(generation.Manifest)
	generation.Material.CertificatePEM = bytes.Clone(generation.Material.CertificatePEM)
	generation.Material.PrivateKeyPEM = bytes.Clone(generation.Material.PrivateKeyPEM)
	generation.Account = cloneAccountMetadata(generation.Account)
	return generation
}
