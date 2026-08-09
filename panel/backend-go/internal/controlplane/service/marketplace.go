package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/marketplace"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/plugins"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

var (
	ErrMarketplaceSourceNotFound = errors.New("marketplace source not found")
	ErrMarketplaceEntryNotFound  = errors.New("marketplace package entry not found in current snapshot")
	ErrMarketplaceSourceExists   = storage.ErrMarketplaceSourceExists
)

type marketplaceCatalogStore interface {
	SaveMarketplaceSource(context.Context, marketplace.Source) error
	UpdateMarketplaceSource(context.Context, marketplace.Source, uint64) (marketplace.Source, error)
	DeleteMarketplaceSource(context.Context, string) (marketplace.SourceDeletion, error)
	ListMarketplaceSources(context.Context) ([]marketplace.Source, error)
	GetMarketplaceSource(context.Context, string) (marketplace.Source, bool, error)
	CurrentMarketEntry(context.Context, string, string, string, string) (plugins.MarketEntry, bool, error)
	CurrentDirectPlugin(context.Context, string, string, string, string) (plugins.DirectPluginSnapshot, bool, error)
	CurrentPackageAcquisition(context.Context, string, string) (marketplace.PackageAcquisition, bool, error)
	CurrentSnapshot(context.Context, string) (marketplace.Snapshot, bool, error)
	AppendAuditEvent(context.Context, storage.AuditEventRow) error
	ClaimPackageGC(context.Context, string, string, string) (marketplace.PackageGCClaim, bool, error)
	PreparePackageGCObjects(context.Context, marketplace.PackageGCClaim, []marketplace.PackageGCObject) error
	PreparePackageGCQuarantine(context.Context, marketplace.PackageGCClaim, string) error
	WithPackageGCMutation(context.Context, marketplace.PackageGCClaim, func() error) error
	CompletePackageGC(context.Context, marketplace.PackageGCClaim, string) error
	RecordPackageGCFailure(context.Context, string, string, string, string) error
	CompleteMarketplaceSourceDeletion(context.Context, string, string) error
	ListPackageGCIntents(context.Context) ([]marketplace.PackageGCIntent, error)
	ClaimMarketplaceDirectoryCleanup(context.Context, string, time.Duration) (marketplace.DirectoryCleanupWork, bool, error)
	CompleteMarketplaceDirectoryCleanup(context.Context, marketplace.DirectoryCleanupWork, string) error
	ListMarketplaceSourceDeletions(context.Context) ([]string, error)
	GetPluginPackageByIdentity(context.Context, string) (storage.PluginPackageRow, bool, error)
}

type MarketplaceCatalog struct {
	Source   marketplace.Source   `json:"source"`
	Snapshot marketplace.Snapshot `json:"snapshot"`
}

type coherentMarketplaceCatalogStore interface {
	CurrentMarketplaceCatalog(context.Context, string) (marketplace.Source, marketplace.Snapshot, bool, error)
}

type MarketplaceService struct {
	store            marketplaceCatalogStore
	manager          *marketplace.Manager
	validator        *plugins.Validator
	validators       marketplace.SourceValidatorFactory
	cacheRoot        string
	marketplaceRoot  string
	removeAll        func(string) error
	rename           func(string, string) error
	mkdirAll         func(string, os.FileMode) error
	packageGC        func(string, string, string) error
	packageVariantGC func(string, marketplace.PackageGCClaim) error
	legacyPackageGC  func(string, marketplace.PackageGCClaim, *plugins.Validator, plugins.PackageExpectation) error
}

func NewMarketplaceService(store marketplaceCatalogStore, manager *marketplace.Manager, validator *plugins.Validator, cacheRoot string) *MarketplaceService {
	return NewMarketplaceServiceWithSourceValidators(store, manager, validator, cacheRoot, func(marketplace.Source) (*plugins.Validator, error) { return validator, nil })
}

func NewMarketplaceServiceWithSourceValidators(store marketplaceCatalogStore, manager *marketplace.Manager, validator *plugins.Validator, cacheRoot string, validators marketplace.SourceValidatorFactory) *MarketplaceService {
	dataRoot := filepath.Dir(filepath.Dir(cacheRoot))
	return &MarketplaceService{store: store, manager: manager, validator: validator, validators: validators, cacheRoot: cacheRoot, marketplaceRoot: filepath.Join(dataRoot, "marketplace"), removeAll: os.RemoveAll, rename: os.Rename, mkdirAll: os.MkdirAll, packageGC: marketplace.QuarantineAndDeleteVerifiedPackage, packageVariantGC: marketplace.QuarantineAndDeleteVerifiedPackageVariant, legacyPackageGC: marketplace.QuarantineAndDeleteLegacyVerifiedPackage}
}

func (s *MarketplaceService) ListSources(ctx context.Context) ([]marketplace.Source, error) {
	if _, err := s.ensureOfficialSource(ctx); err != nil {
		return nil, err
	}
	sources, err := s.store.ListMarketplaceSources(ctx)
	if err != nil {
		return nil, err
	}
	visible := sources[:0]
	for _, source := range sources {
		if !source.Deleting {
			visible = append(visible, source)
		}
	}
	return visible, nil
}

func (s *MarketplaceService) ensureOfficialSource(ctx context.Context) (marketplace.Source, error) {
	source, ok, err := s.store.GetMarketplaceSource(ctx, marketplace.OfficialSourceID)
	if err != nil {
		return marketplace.Source{}, err
	}
	if ok {
		return source, nil
	}
	requested := marketplace.OfficialSource()
	if err := s.store.SaveMarketplaceSource(ctx, requested); err == nil {
		return requested, nil
	} else if !errors.Is(err, ErrMarketplaceSourceExists) {
		return marketplace.Source{}, err
	}
	source, ok, err = s.store.GetMarketplaceSource(ctx, marketplace.OfficialSourceID)
	if err != nil {
		return marketplace.Source{}, err
	}
	if !ok {
		return marketplace.Source{}, errors.New("official marketplace source creation conflicted without a durable source")
	}
	return source, nil
}

func (s *MarketplaceService) Source(ctx context.Context, sourceID string) (marketplace.Source, error) {
	source, ok, err := s.store.GetMarketplaceSource(ctx, sourceID)
	if err != nil {
		return marketplace.Source{}, err
	}
	if !ok && sourceID == marketplace.OfficialSourceID {
		return marketplace.OfficialSource(), nil
	}
	if !ok || source.Deleting {
		return marketplace.Source{}, ErrMarketplaceSourceNotFound
	}
	return source, nil
}

func (s *MarketplaceService) CurrentCatalog(ctx context.Context, sourceID string) (MarketplaceCatalog, error) {
	if coherent, ok := s.store.(coherentMarketplaceCatalogStore); ok {
		source, snapshot, found, err := coherent.CurrentMarketplaceCatalog(ctx, sourceID)
		if err != nil {
			return MarketplaceCatalog{}, err
		}
		if !found || source.Deleting {
			return MarketplaceCatalog{}, ErrMarketplaceEntryNotFound
		}
		return MarketplaceCatalog{Source: source, Snapshot: snapshot}, nil
	}
	source, err := s.Source(ctx, sourceID)
	if err != nil {
		return MarketplaceCatalog{}, err
	}
	snapshot, ok, err := s.store.CurrentSnapshot(ctx, sourceID)
	if err != nil {
		return MarketplaceCatalog{}, err
	}
	if !ok || source.Deleting {
		return MarketplaceCatalog{}, ErrMarketplaceEntryNotFound
	}
	latest, err := s.Source(ctx, sourceID)
	if err != nil {
		return MarketplaceCatalog{}, err
	}
	if source.ConfigRevision != latest.ConfigRevision || source.CurrentSnapshot != latest.CurrentSnapshot || source.RefKind != latest.RefKind || source.RefName != latest.RefName || !strings.EqualFold(source.CurrentResolvedOID, latest.CurrentResolvedOID) || snapshot.ID != latest.CurrentSnapshot || snapshot.SourceRevision != latest.ConfigRevision || snapshot.RefKind != latest.RefKind || snapshot.RefName != latest.RefName || !strings.EqualFold(snapshot.Commit, latest.CurrentResolvedOID) {
		return MarketplaceCatalog{}, storage.ErrMarketplaceCatalogStale
	}
	return MarketplaceCatalog{Source: source, Snapshot: snapshot}, nil
}

func (s *MarketplaceService) AddCustomSource(ctx context.Context, id, name, remoteURL, branchName, credentialRef string, interval time.Duration, signer marketplace.SourceSigner) (marketplace.Source, error) {
	return s.AddGitRepositorySource(ctx, id, name, remoteURL, marketplace.SourcePurposeMarket, marketplace.GitRefKindBranch, branchName, credentialRef, interval, signer)
}

func (s *MarketplaceService) AddGitRepositorySource(ctx context.Context, id, name, remoteURL, purpose, refKind, refName, credentialRef string, interval time.Duration, signer marketplace.SourceSigner) (marketplace.Source, error) {
	source, err := marketplace.NewSignedGitRepositorySource(id, name, remoteURL, purpose, refKind, refName, credentialRef, interval, signer)
	if err != nil {
		targetID := strings.ToLower(strings.TrimSpace(id))
		if targetID == "" {
			targetID = "unknown"
		}
		if auditErr := s.AuditSourceFailure(ctx, "add", targetID, "validation"); auditErr != nil {
			return marketplace.Source{}, fmt.Errorf("persist marketplace failure audit: %w", auditErr)
		}
		return marketplace.Source{}, err
	}
	if source.CredentialRef != "" {
		if _, ok := marketplace.CredentialAuthorizationFromContext(ctx, source.CredentialRef); !ok {
			err := errors.New("marketplace credential authorization is required")
			if auditErr := s.AuditSourceFailure(ctx, "add", source.ID, "credential_authorization"); auditErr != nil {
				return marketplace.Source{}, fmt.Errorf("persist marketplace failure audit: %w", auditErr)
			}
			return marketplace.Source{}, err
		}
	}
	if err := s.store.SaveMarketplaceSource(ctx, source); err != nil {
		if auditErr := s.AuditSourceFailure(ctx, "add", source.ID, "persistence"); auditErr != nil {
			return marketplace.Source{}, fmt.Errorf("persist marketplace failure audit: %w", auditErr)
		}
		return marketplace.Source{}, err
	}
	return source, nil
}

func (s *MarketplaceService) UpdateGitRepositorySource(ctx context.Context, source marketplace.Source, expectedRevision uint64) (marketplace.Source, error) {
	if source.Kind != marketplace.SourceKindCustom || source.ConfigRevision != expectedRevision+1 {
		return marketplace.Source{}, fmt.Errorf("%w: invalid marketplace source update", ErrInvalidArgument)
	}
	if source.CredentialRef != "" {
		if _, ok := marketplace.CredentialAuthorizationFromContext(ctx, source.CredentialRef); !ok {
			return marketplace.Source{}, errors.New("marketplace credential authorization is required")
		}
	}
	if err := marketplace.ValidateSource(source); err != nil {
		return marketplace.Source{}, err
	}
	updated, err := s.store.UpdateMarketplaceSource(ctx, source, expectedRevision)
	if err != nil {
		if auditErr := s.AuditSourceFailure(ctx, "edit", source.ID, "persistence"); auditErr != nil {
			return marketplace.Source{}, fmt.Errorf("persist marketplace failure audit: %w", auditErr)
		}
		return marketplace.Source{}, err
	}
	return updated, nil
}

func (s *MarketplaceService) DeleteSource(ctx context.Context, sourceID string) error {
	_, err := s.store.DeleteMarketplaceSource(ctx, sourceID)
	if err != nil {
		if auditErr := s.AuditSourceFailure(ctx, "delete", sourceID, "persistence"); auditErr != nil {
			return fmt.Errorf("persist marketplace failure audit: %w", auditErr)
		}
	}
	if err != nil {
		return err
	}
	var cleanupErr error
	cleanupErr = errors.Join(cleanupErr, s.runPendingDirectoryCleanup(ctx, sourceID, nil))
	if cleanupErr != nil {
		_ = s.store.CompleteMarketplaceSourceDeletion(ctx, sourceID, cleanupErr.Error())
		return cleanupErr
	}
	intents, intentErr := s.store.ListPackageGCIntents(ctx)
	if intentErr != nil {
		return intentErr
	}
	for _, intent := range intents {
		if intent.SourceID != sourceID {
			continue
		}
		digest := intent.Digest
		cachePath, pathErr := marketplace.CachePath(s.cacheRoot, digest)
		if pathErr != nil {
			cleanupErr = errors.Join(cleanupErr, pathErr, s.store.RecordPackageGCFailure(ctx, sourceID, digest, intent.SignerFingerprint, "invalid package digest"))
			continue
		}
		claim, claimed, claimErr := s.store.ClaimPackageGC(ctx, sourceID, digest, intent.SignerFingerprint)
		if claimErr != nil {
			cleanupErr = errors.Join(cleanupErr, claimErr)
			continue
		}
		if !claimed {
			continue
		}
		cleanupErr = errors.Join(cleanupErr, s.executePackageGC(ctx, claim, cachePath))
	}
	if cleanupErr != nil {
		_ = s.store.CompleteMarketplaceSourceDeletion(ctx, sourceID, cleanupErr.Error())
		return cleanupErr
	}
	return s.store.CompleteMarketplaceSourceDeletion(ctx, sourceID, "")
}

func (s *MarketplaceService) RunPendingGC(ctx context.Context) error {
	deletingSources, err := s.store.ListMarketplaceSourceDeletions(ctx)
	if err != nil {
		return err
	}
	sourceIDs := map[string]struct{}{}
	for _, sourceID := range deletingSources {
		sourceIDs[sourceID] = struct{}{}
	}
	var result error
	result = errors.Join(result, s.runPendingDirectoryCleanup(ctx, "", sourceIDs))
	intents, err := s.store.ListPackageGCIntents(ctx)
	if err != nil {
		return errors.Join(result, err)
	}
	for _, intent := range intents {
		sourceIDs[intent.SourceID] = struct{}{}
		cachePath, pathErr := marketplace.CachePath(s.cacheRoot, intent.Digest)
		if pathErr != nil {
			result = errors.Join(result, pathErr, s.store.RecordPackageGCFailure(ctx, intent.SourceID, intent.Digest, intent.SignerFingerprint, "invalid package digest"))
			continue
		}
		claim, claimed, claimErr := s.store.ClaimPackageGC(ctx, intent.SourceID, intent.Digest, intent.SignerFingerprint)
		if claimErr != nil || !claimed {
			result = errors.Join(result, claimErr)
			continue
		}
		result = errors.Join(result, s.executePackageGC(ctx, claim, cachePath))
	}
	for sourceID := range sourceIDs {
		if err := s.store.CompleteMarketplaceSourceDeletion(ctx, sourceID, ""); err != nil && !strings.Contains(err.Error(), "still pending") {
			result = errors.Join(result, err)
		}
	}
	return result
}

func (s *MarketplaceService) executePackageGC(ctx context.Context, claim marketplace.PackageGCClaim, _ string) error {
	if claim.SignerFingerprint == "" {
		return s.executeDigestOnlyPackageGC(ctx, claim)
	}
	if !claim.ObjectsPrepared {
		objects, err := s.discoverPackageGCObjects(ctx, claim)
		if err != nil {
			return errors.Join(err, s.store.CompletePackageGC(ctx, claim, err.Error()))
		}
		if err := s.store.PreparePackageGCObjects(ctx, claim, objects); err != nil {
			return err
		}
		claim.Objects, claim.ObjectsPrepared = objects, true
	}
	var legacyValidator *plugins.Validator
	var legacyExpectation plugins.PackageExpectation
	for _, object := range claim.Objects {
		if err := marketplace.ValidatePackageGCObject(claim, object); err != nil {
			return errors.Join(err, s.store.CompletePackageGC(ctx, claim, err.Error()))
		}
		if _, liveOK := marketplaceCleanupPath(s.cacheRoot, object.Path); !liveOK {
			err := errors.New("package GC live path is outside the managed root")
			return errors.Join(err, s.store.CompletePackageGC(ctx, claim, err.Error()))
		}
		if object.Layout == marketplace.PackageGCLayoutLegacy && legacyValidator == nil {
			validator, expectation, _, err := s.packageGCValidator(ctx, claim)
			if err != nil {
				return errors.Join(err, s.store.CompletePackageGC(ctx, claim, err.Error()))
			}
			legacyValidator, legacyExpectation = validator, expectation
		}
	}
	var removeErr error
	for _, object := range claim.Objects {
		object := object
		removeErr = s.store.WithPackageGCMutation(ctx, claim, func() error {
			livePath, _ := marketplaceCleanupPath(s.cacheRoot, object.Path)
			claim.QuarantinePath = object.QuarantinePath
			var err error
			switch object.Layout {
			case marketplace.PackageGCLayoutSigner:
				err = s.packageVariantGC(s.cacheRoot, claim)
			case marketplace.PackageGCLayoutLegacy:
				err = s.legacyPackageGC(s.cacheRoot, claim, legacyValidator, legacyExpectation)
			default:
				err = errors.New("unknown package GC cache layout")
			}
			if err == nil {
				err = assertPackageGCPathsAbsent(s.cacheRoot, object.QuarantinePath, livePath)
			}
			return err
		})
		if removeErr != nil {
			break
		}
	}
	failure := ""
	if removeErr != nil {
		failure = removeErr.Error()
	}
	return errors.Join(removeErr, s.store.CompletePackageGC(ctx, claim, failure))
}

func (s *MarketplaceService) executeDigestOnlyPackageGC(ctx context.Context, claim marketplace.PackageGCClaim) error {
	relative := strings.TrimSpace(claim.QuarantinePath)
	if relative == "" {
		relative = filepath.ToSlash(filepath.Join(".gc", strings.ToLower(claim.Digest)+"-"+claim.QuarantineID+"-"+marketplace.PackageGCLayoutLegacy))
		if err := s.store.PreparePackageGCQuarantine(ctx, claim, relative); err != nil {
			return err
		}
	}
	legacyPath, err := marketplace.CachePath(s.cacheRoot, claim.Digest)
	if err == nil {
		err = s.store.WithPackageGCMutation(ctx, claim, func() error {
			if err := s.packageGC(s.cacheRoot, claim.Digest, relative); err != nil {
				return err
			}
			return assertPackageGCPathsAbsent(s.cacheRoot, relative, legacyPath)
		})
	}
	failure := ""
	if err != nil {
		failure = err.Error()
	}
	return errors.Join(err, s.store.CompletePackageGC(ctx, claim, failure))
}

func (s *MarketplaceService) discoverPackageGCObjects(ctx context.Context, claim marketplace.PackageGCClaim) ([]marketplace.PackageGCObject, error) {
	signerObject, err := marketplace.NewPackageGCObject(claim, marketplace.PackageGCLayoutSigner)
	if err != nil {
		return nil, err
	}
	legacyObject, err := marketplace.NewPackageGCObject(claim, marketplace.PackageGCLayoutLegacy)
	if err != nil {
		return nil, err
	}
	signerPath, _ := marketplaceCleanupPath(s.cacheRoot, signerObject.Path)
	legacyPath, _ := marketplaceCleanupPath(s.cacheRoot, legacyObject.Path)
	signerMissing, err := packageGCPathMissing(signerPath)
	if err != nil {
		return nil, err
	}
	legacyMissing, err := packageGCPathMissing(legacyPath)
	if err != nil {
		return nil, err
	}
	if signerMissing && legacyMissing {
		return []marketplace.PackageGCObject{}, nil
	}
	objects := make([]marketplace.PackageGCObject, 0, 2)
	if !signerMissing {
		objects = append(objects, signerObject)
	}
	if !legacyMissing {
		validator, expectation, persistedPath, err := s.packageGCValidator(ctx, claim)
		if err != nil {
			return nil, err
		}
		if _, err := validator.ValidatePackageIntegrity(legacyPath, expectation); err == nil {
			objects = append(objects, legacyObject)
		} else if signerMissing || filepath.Clean(persistedPath) == filepath.Clean(legacyPath) {
			return nil, fmt.Errorf("revalidate legacy verified cache trust: %w", err)
		}
	}
	return objects, nil
}

func (s *MarketplaceService) packageGCValidator(ctx context.Context, claim marketplace.PackageGCClaim) (*plugins.Validator, plugins.PackageExpectation, string, error) {
	identity := storage.PluginPackageIdentity(claim.Digest, claim.SourceID, claim.SignerFingerprint)
	row, ok, err := s.store.GetPluginPackageByIdentity(ctx, identity)
	if err != nil {
		return nil, plugins.PackageExpectation{}, "", err
	}
	if ok {
		if row.SourceID != claim.SourceID || !strings.EqualFold(row.Digest, claim.Digest) || !strings.EqualFold(row.SignatureFingerprint, claim.SignerFingerprint) {
			return nil, plugins.PackageExpectation{}, "", errors.New("package metadata differs from its GC claim")
		}
		trust := marketplace.SignatureTrust{SourceID: row.SourceID, SourceKind: row.SourceKind, KeyID: row.SignatureKeyID, PublicKey: row.SignaturePublicKey, Fingerprint: row.SignatureFingerprint}
		validator, err := marketplace.ValidatorForSignatureTrustWithBase(s.validator, trust)
		return validator, plugins.PackageExpectation{ID: row.PluginID, Version: row.Version, SHA256: row.Digest, SignatureKeyID: row.SignatureKeyID}, row.CachePath, err
	}
	if err := marketplace.ValidateSignatureTrust(claim.Trust); err == nil && claim.Trust.SourceID == claim.SourceID && strings.EqualFold(claim.Trust.Fingerprint, claim.SignerFingerprint) {
		validator, err := marketplace.ValidatorForSignatureTrustWithBase(s.validator, claim.Trust)
		return validator, plugins.PackageExpectation{SHA256: claim.Digest, SignatureKeyID: claim.Trust.KeyID}, "", err
	}
	source, ok, err := s.store.GetMarketplaceSource(ctx, claim.SourceID)
	if err != nil {
		return nil, plugins.PackageExpectation{}, "", err
	}
	if !ok {
		return nil, plugins.PackageExpectation{}, "", errors.New("package signer trust is unavailable")
	}
	trust, err := source.SignatureTrust()
	if err != nil {
		return nil, plugins.PackageExpectation{}, "", err
	}
	if !strings.EqualFold(trust.Fingerprint, claim.SignerFingerprint) {
		return nil, plugins.PackageExpectation{}, "", errors.New("package signer differs from its GC claim")
	}
	validator, err := marketplace.ValidatorForSignatureTrustWithBase(s.validator, trust)
	return validator, plugins.PackageExpectation{SHA256: claim.Digest, SignatureKeyID: trust.KeyID}, "", err
}

func packageGCPathMissing(path string) (bool, error) {
	_, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return true, nil
	}
	return false, err
}

func assertPackageGCPathsAbsent(root, relative string, livePaths ...string) error {
	quarantine, ok := marketplaceCleanupPath(root, relative)
	if !ok {
		return errors.New("package GC quarantine path is outside the managed root")
	}
	for _, path := range append(livePaths, quarantine) {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			if err == nil {
				return fmt.Errorf("package GC target still exists: %s", path)
			}
			return err
		}
	}
	return nil
}

func (s *MarketplaceService) runPendingDirectoryCleanup(ctx context.Context, onlySource string, sourceIDs map[string]struct{}) error {
	var result error
	for {
		work, ok, err := s.store.ClaimMarketplaceDirectoryCleanup(ctx, onlySource, 5*time.Minute)
		if err != nil {
			return errors.Join(result, err)
		}
		if !ok {
			break
		}
		if sourceIDs != nil {
			sourceIDs[work.SourceID] = struct{}{}
		}
		managedPath, ok := marketplaceCleanupPath(s.marketplaceRoot, work.Path)
		if !ok {
			pathErr := errors.New("marketplace directory cleanup path is outside the managed root")
			result = errors.Join(result, pathErr, s.store.CompleteMarketplaceDirectoryCleanup(ctx, work, pathErr.Error()))
			break
		}
		removeErr := s.removeAll(managedPath)
		failure := ""
		if removeErr != nil {
			failure = removeErr.Error()
		}
		result = errors.Join(result, removeErr, s.store.CompleteMarketplaceDirectoryCleanup(ctx, work, failure))
		if removeErr != nil {
			break
		}
	}
	return result
}

func marketplaceCleanupPath(root, candidate string) (string, bool) {
	root, rootErr := filepath.Abs(root)
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(root, filepath.FromSlash(candidate))
	}
	candidate, candidateErr := filepath.Abs(candidate)
	if rootErr != nil || candidateErr != nil {
		return "", false
	}
	relative, err := filepath.Rel(root, candidate)
	return candidate, err == nil && relative != "." && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func (s *MarketplaceService) Refresh(ctx context.Context, sourceID string) (marketplace.Snapshot, error) {
	if s.manager == nil {
		return marketplace.Snapshot{}, errors.New("marketplace refresh manager is unavailable")
	}
	source, ok, err := s.store.GetMarketplaceSource(ctx, sourceID)
	if err != nil {
		return marketplace.Snapshot{}, err
	}
	if !ok && sourceID == marketplace.OfficialSourceID {
		source, err = s.ensureOfficialSource(ctx)
		if err != nil {
			return marketplace.Snapshot{}, err
		}
		ok = true
	}
	if !ok {
		if auditErr := s.AuditSourceFailure(ctx, "refresh", sourceID, "not_found"); auditErr != nil {
			return marketplace.Snapshot{}, fmt.Errorf("persist marketplace failure audit: %w", auditErr)
		}
		return marketplace.Snapshot{}, ErrMarketplaceSourceNotFound
	}
	if err := marketplace.ValidateSource(source); err != nil {
		if auditErr := s.AuditSourceFailure(ctx, "refresh", sourceID, "validation"); auditErr != nil {
			return marketplace.Snapshot{}, fmt.Errorf("persist marketplace failure audit: %w", auditErr)
		}
		return marketplace.Snapshot{}, err
	}
	trusted, _ := storage.QuotaActorFromContext(ctx)
	return s.manager.Refresh(ctx, source, marketplace.OperationActor{ActorID: trusted.UserID, SessionID: trusted.SessionID, CorrelationID: trusted.CorrelationID})
}

// AbandonRefresh durably fences a timed-out scheduler operation. A worker that
// ignored cancellation can no longer promote after this transition.
func (s *MarketplaceService) AbandonRefresh(ctx context.Context, sourceID string, identity marketplace.RefreshIdentity, errorClass string) error {
	store, ok := s.store.(interface {
		AbandonMarketplaceRefresh(context.Context, string, string, string, string) error
	})
	if !ok {
		return errors.New("marketplace refresh abandonment is unavailable")
	}
	return store.AbandonMarketplaceRefresh(ctx, sourceID, identity.OperationID, identity.LeaseToken, errorClass)
}

func (s *MarketplaceService) AuditSourceFailure(ctx context.Context, action, sourceID, errorClass string) error {
	actor, _ := storage.QuotaActorFromContext(ctx)
	sourceID = safeMarketplaceAuditSourceID(sourceID)
	metadata := `{"redacted":true}`
	return s.store.AppendAuditEvent(ctx, storage.AuditEventRow{ID: marketplaceServiceID("audit"), ActorID: actor.UserID, SessionID: actor.SessionID, Action: "marketplace.source." + action, TargetKind: "marketplace_source", TargetID: sourceID, CorrelationID: actor.CorrelationID, Result: "failure", ErrorClass: errorClass, MetadataJSON: metadata, CreatedAt: time.Now().UTC()})
}

func safeMarketplaceAuditSourceID(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if len(value) == 0 || len(value) > 63 || value[0] < 'a' || value[0] > 'z' {
		return "unknown"
	}
	for _, char := range value[1:] {
		if (char < 'a' || char > 'z') && (char < '0' || char > '9') && char != '-' {
			return "unknown"
		}
	}
	return value
}

func marketplaceServiceID(prefix string) string {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		panic(err)
	}
	return prefix + "_" + hex.EncodeToString(value)
}

// ResolvePackage is the only HTTP-facing path from source/plugin/version to a
// lifecycle candidate. Callers never provide a filesystem path or manifest.
func (s *MarketplaceService) ResolvePackage(ctx context.Context, sourceID, pluginID, version, digest string) (PluginPackageCandidate, error) {
	source, err := s.Source(ctx, sourceID)
	if err != nil {
		return PluginPackageCandidate{}, err
	}
	requestedDigest := strings.ToLower(strings.TrimSpace(digest))
	var entryID, entryVersion, packageDigest, signatureKeyID string
	var compatibility plugins.Compatibility
	if source.Purpose == marketplace.SourcePurposePlugin {
		direct, ok, loadErr := s.store.CurrentDirectPlugin(ctx, sourceID, pluginID, version, requestedDigest)
		if loadErr != nil {
			return PluginPackageCandidate{}, loadErr
		}
		if !ok {
			return PluginPackageCandidate{}, ErrMarketplaceEntryNotFound
		}
		entryID, entryVersion, packageDigest, signatureKeyID, compatibility = direct.ID, direct.Version, direct.PackageSHA256, direct.SignatureKeyID, direct.Compatibility
	} else {
		entry, ok, loadErr := s.store.CurrentMarketEntry(ctx, sourceID, pluginID, version, requestedDigest)
		if loadErr != nil {
			return PluginPackageCandidate{}, loadErr
		}
		if !ok {
			return PluginPackageCandidate{}, ErrMarketplaceEntryNotFound
		}
		entryID, entryVersion, packageDigest, signatureKeyID, compatibility = entry.ID, entry.Version, entry.PackageSHA256, entry.SignatureKeyID, entry.Compatibility
	}
	acquisition, ok, err := s.store.CurrentPackageAcquisition(ctx, sourceID, packageDigest)
	if err != nil {
		return PluginPackageCandidate{}, err
	}
	if !ok || acquisition.SnapshotID != source.CurrentSnapshot || acquisition.Trust.SourceID != source.ID || acquisition.Trust.KeyID != signatureKeyID {
		return PluginPackageCandidate{}, errors.New("marketplace package acquisition signer binding is unavailable")
	}
	cachePath, err := marketplace.SignerCachePath(s.cacheRoot, packageDigest, acquisition.Trust.Fingerprint)
	if err != nil {
		return PluginPackageCandidate{}, err
	}
	if _, statErr := os.Stat(cachePath); errors.Is(statErr, os.ErrNotExist) {
		// Existing installations and pre-signature-identity migrations used one
		// digest-only directory. Exact source-bound signature verification below
		// makes this a safe read-only compatibility fallback; all new refreshes
		// enter the signer-aware layout.
		cachePath, err = marketplace.CachePath(s.cacheRoot, packageDigest)
		if err != nil {
			return PluginPackageCandidate{}, err
		}
	}
	validator := s.validator
	if s.validators != nil {
		validator, err = s.validators(source)
		if err != nil {
			return PluginPackageCandidate{}, err
		}
	}
	validated, err := validator.ValidatePackage(cachePath, plugins.PackageExpectation{ID: entryID, Version: entryVersion, SHA256: packageDigest, Compatibility: compatibility, SignatureKeyID: acquisition.Trust.KeyID})
	if err != nil {
		return PluginPackageCandidate{}, err
	}
	return PluginPackageCandidate{Package: validated, Runtime: validated.Manifest.Runtime, Artifacts: append([]plugins.Artifact(nil), validated.Manifest.Artifacts...), SignatureTrust: acquisition.Trust, CachePath: cachePath, validator: validator, sourceID: source.ID, sourceKind: source.Kind, sourceRiskLabel: source.RiskLabel, sourceRevision: acquisition.SourceRevision, sourceRefKind: acquisition.RefKind, sourceRefName: acquisition.RefName, sourceResolvedOID: acquisition.ResolvedOID, requireAcquisition: true}, nil
}
