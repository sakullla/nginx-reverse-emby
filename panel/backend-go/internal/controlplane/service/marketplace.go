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
	DeleteMarketplaceSource(context.Context, string) (marketplace.SourceDeletion, error)
	ListMarketplaceSources(context.Context) ([]marketplace.Source, error)
	GetMarketplaceSource(context.Context, string) (marketplace.Source, bool, error)
	CurrentMarketEntry(context.Context, string, string, string, string) (plugins.MarketEntry, bool, error)
	CurrentPackageAcquisition(context.Context, string, string) (marketplace.PackageAcquisition, bool, error)
	CurrentSnapshot(context.Context, string) (marketplace.Snapshot, bool, error)
	AppendAuditEvent(context.Context, storage.AuditEventRow) error
	ClaimPackageGC(context.Context, string, string) (marketplace.PackageGCClaim, bool, error)
	PreparePackageGCQuarantine(context.Context, marketplace.PackageGCClaim, string) error
	CompletePackageGC(context.Context, marketplace.PackageGCClaim, string) error
	RecordPackageGCFailure(context.Context, string, string, string) error
	CompleteMarketplaceSourceDeletion(context.Context, string, string) error
	ListPackageGCIntents(context.Context) ([]marketplace.PackageGCIntent, error)
	ClaimMarketplaceDirectoryCleanup(context.Context, string, time.Duration) (marketplace.DirectoryCleanupWork, bool, error)
	CompleteMarketplaceDirectoryCleanup(context.Context, marketplace.DirectoryCleanupWork, string) error
	ListMarketplaceSourceDeletions(context.Context) ([]string, error)
}

type MarketplaceCatalog struct {
	Source   marketplace.Source   `json:"source"`
	Snapshot marketplace.Snapshot `json:"snapshot"`
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
}

func NewMarketplaceService(store marketplaceCatalogStore, manager *marketplace.Manager, validator *plugins.Validator, cacheRoot string) *MarketplaceService {
	return NewMarketplaceServiceWithSourceValidators(store, manager, validator, cacheRoot, func(marketplace.Source) (*plugins.Validator, error) { return validator, nil })
}

func NewMarketplaceServiceWithSourceValidators(store marketplaceCatalogStore, manager *marketplace.Manager, validator *plugins.Validator, cacheRoot string, validators marketplace.SourceValidatorFactory) *MarketplaceService {
	dataRoot := filepath.Dir(filepath.Dir(cacheRoot))
	return &MarketplaceService{store: store, manager: manager, validator: validator, validators: validators, cacheRoot: cacheRoot, marketplaceRoot: filepath.Join(dataRoot, "marketplace"), removeAll: os.RemoveAll, rename: os.Rename, mkdirAll: os.MkdirAll, packageGC: marketplace.QuarantineAndDeleteVerifiedPackage, packageVariantGC: marketplace.QuarantineAndDeleteVerifiedPackageVariant}
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
	return MarketplaceCatalog{Source: source, Snapshot: snapshot}, nil
}

func (s *MarketplaceService) AddCustomSource(ctx context.Context, id, name, remoteURL, reference, credentialRef string, interval time.Duration, signer marketplace.SourceSigner) (marketplace.Source, error) {
	source, err := marketplace.NewSignedCustomSource(id, name, remoteURL, reference, credentialRef, interval, signer)
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

func (s *MarketplaceService) DeleteSource(ctx context.Context, sourceID string) error {
	deletion, err := s.store.DeleteMarketplaceSource(ctx, sourceID)
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
	for _, digest := range deletion.CacheDigests {
		cachePath, pathErr := marketplace.CachePath(s.cacheRoot, digest)
		if pathErr != nil {
			cleanupErr = errors.Join(cleanupErr, pathErr, s.store.RecordPackageGCFailure(ctx, sourceID, digest, "invalid package digest"))
			continue
		}
		claim, claimed, claimErr := s.store.ClaimPackageGC(ctx, sourceID, digest)
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
			result = errors.Join(result, pathErr, s.store.RecordPackageGCFailure(ctx, intent.SourceID, intent.Digest, "invalid package digest"))
			continue
		}
		claim, claimed, claimErr := s.store.ClaimPackageGC(ctx, intent.SourceID, intent.Digest)
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
	relative := strings.TrimSpace(claim.QuarantinePath)
	if relative == "" {
		if claim.SignerFingerprint != "" {
			var err error
			relative, err = marketplace.PackageGCQuarantinePath(claim)
			if err != nil {
				return err
			}
		} else {
			relative = filepath.ToSlash(filepath.Join(".gc", strings.ToLower(claim.Digest)+"-"+claim.Token))
		}
		if err := s.store.PreparePackageGCQuarantine(ctx, claim, relative); err != nil {
			return err
		}
	}
	_, ok := marketplaceCleanupPath(s.cacheRoot, relative)
	if !ok {
		failure := "package GC quarantine path is outside the managed root"
		return errors.Join(errors.New(failure), s.store.CompletePackageGC(ctx, claim, failure))
	}
	var removeErr error
	if claim.SignerFingerprint != "" {
		claim.QuarantinePath = relative
		removeErr = s.packageVariantGC(s.cacheRoot, claim)
	} else {
		removeErr = s.packageGC(s.cacheRoot, claim.Digest, relative)
	}
	failure := ""
	if removeErr != nil {
		failure = removeErr.Error()
	}
	return errors.Join(removeErr, s.store.CompletePackageGC(ctx, claim, failure))
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
	entry, ok, err := s.store.CurrentMarketEntry(ctx, sourceID, pluginID, version, strings.ToLower(strings.TrimSpace(digest)))
	if err != nil {
		return PluginPackageCandidate{}, err
	}
	if !ok {
		return PluginPackageCandidate{}, ErrMarketplaceEntryNotFound
	}
	acquisition, ok, err := s.store.CurrentPackageAcquisition(ctx, sourceID, entry.PackageSHA256)
	if err != nil {
		return PluginPackageCandidate{}, err
	}
	if !ok || acquisition.SnapshotID != source.CurrentSnapshot || acquisition.Trust.SourceID != source.ID || acquisition.Trust.KeyID != entry.SignatureKeyID {
		return PluginPackageCandidate{}, errors.New("marketplace package acquisition signer binding is unavailable")
	}
	cachePath, err := marketplace.SignerCachePath(s.cacheRoot, entry.PackageSHA256, acquisition.Trust.Fingerprint)
	if err != nil {
		return PluginPackageCandidate{}, err
	}
	if _, statErr := os.Stat(cachePath); errors.Is(statErr, os.ErrNotExist) {
		// Existing installations and pre-signature-identity migrations used one
		// digest-only directory. Exact source-bound signature verification below
		// makes this a safe read-only compatibility fallback; all new refreshes
		// enter the signer-aware layout.
		cachePath, err = marketplace.CachePath(s.cacheRoot, entry.PackageSHA256)
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
	validated, err := validator.ValidatePackage(cachePath, plugins.PackageExpectation{ID: entry.ID, Version: entry.Version, SHA256: entry.PackageSHA256, Compatibility: entry.Compatibility, SignatureKeyID: acquisition.Trust.KeyID})
	if err != nil {
		return PluginPackageCandidate{}, err
	}
	return PluginPackageCandidate{Package: validated, Runtime: validated.Manifest.Runtime, Artifacts: append([]plugins.Artifact(nil), validated.Manifest.Artifacts...), SignatureTrust: acquisition.Trust, CachePath: cachePath, validator: validator, sourceID: source.ID, sourceKind: source.Kind, sourceRiskLabel: source.RiskLabel, requireAcquisition: true}, nil
}
