package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
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
	DeleteMarketplaceSource(context.Context, string) error
	ListMarketplaceSources(context.Context) ([]marketplace.Source, error)
	GetMarketplaceSource(context.Context, string) (marketplace.Source, bool, error)
	CurrentMarketEntry(context.Context, string, string, string, string) (plugins.MarketEntry, bool, error)
	CurrentSnapshot(context.Context, string) (marketplace.Snapshot, bool, error)
	AppendAuditEvent(context.Context, storage.AuditEventRow) error
}

type MarketplaceCatalog struct {
	Source   marketplace.Source   `json:"source"`
	Snapshot marketplace.Snapshot `json:"snapshot"`
}

type MarketplaceService struct {
	store     marketplaceCatalogStore
	manager   *marketplace.Manager
	validator *plugins.Validator
	cacheRoot string
}

func NewMarketplaceService(store marketplaceCatalogStore, manager *marketplace.Manager, validator *plugins.Validator, cacheRoot string) *MarketplaceService {
	return &MarketplaceService{store: store, manager: manager, validator: validator, cacheRoot: cacheRoot}
}

func (s *MarketplaceService) ListSources(ctx context.Context) ([]marketplace.Source, error) {
	if _, ok, err := s.store.GetMarketplaceSource(ctx, marketplace.OfficialSourceID); err != nil {
		return nil, err
	} else if !ok {
		if err := s.store.SaveMarketplaceSource(ctx, marketplace.OfficialSource()); err != nil {
			return nil, err
		}
	}
	return s.store.ListMarketplaceSources(ctx)
}

func (s *MarketplaceService) Source(ctx context.Context, sourceID string) (marketplace.Source, error) {
	source, ok, err := s.store.GetMarketplaceSource(ctx, sourceID)
	if err != nil {
		return marketplace.Source{}, err
	}
	if !ok && sourceID == marketplace.OfficialSourceID {
		return marketplace.OfficialSource(), nil
	}
	if !ok {
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
	if !ok {
		return MarketplaceCatalog{}, ErrMarketplaceEntryNotFound
	}
	return MarketplaceCatalog{Source: source, Snapshot: snapshot}, nil
}

func (s *MarketplaceService) AddCustomSource(ctx context.Context, id, name, remoteURL, reference, credentialRef string, interval time.Duration) (marketplace.Source, error) {
	source, err := marketplace.NewCustomSource(id, name, remoteURL, reference, credentialRef, interval)
	if err != nil {
		targetID := strings.ToLower(strings.TrimSpace(id))
		if targetID == "" {
			targetID = "unknown"
		}
		if auditErr := s.AuditSourceFailure(ctx, "add", targetID, "validation"); auditErr != nil {
			return marketplace.Source{}, fmt.Errorf("%w (persist failure audit: %v)", err, auditErr)
		}
		return marketplace.Source{}, err
	}
	if source.CredentialRef != "" {
		if _, ok := marketplace.CredentialAuthorizationFromContext(ctx, source.CredentialRef); !ok {
			err := errors.New("marketplace credential authorization is required")
			if auditErr := s.AuditSourceFailure(ctx, "add", source.ID, "credential_authorization"); auditErr != nil {
				return marketplace.Source{}, fmt.Errorf("%w (persist failure audit: %v)", err, auditErr)
			}
			return marketplace.Source{}, err
		}
	}
	if err := s.store.SaveMarketplaceSource(ctx, source); err != nil {
		if auditErr := s.AuditSourceFailure(ctx, "add", source.ID, "persistence"); auditErr != nil {
			return marketplace.Source{}, fmt.Errorf("%w (persist failure audit: %v)", err, auditErr)
		}
		return marketplace.Source{}, err
	}
	return source, nil
}

func (s *MarketplaceService) DeleteSource(ctx context.Context, sourceID string) error {
	err := s.store.DeleteMarketplaceSource(ctx, sourceID)
	if err != nil {
		if auditErr := s.AuditSourceFailure(ctx, "delete", sourceID, "persistence"); auditErr != nil {
			return fmt.Errorf("%w (persist failure audit: %v)", err, auditErr)
		}
	}
	return err
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
		source = marketplace.OfficialSource()
		if err := s.store.SaveMarketplaceSource(ctx, source); err != nil {
			return marketplace.Snapshot{}, err
		}
		ok = true
	}
	if !ok {
		if auditErr := s.AuditSourceFailure(ctx, "refresh", sourceID, "not_found"); auditErr != nil {
			return marketplace.Snapshot{}, fmt.Errorf("%w (persist failure audit: %v)", ErrMarketplaceSourceNotFound, auditErr)
		}
		return marketplace.Snapshot{}, ErrMarketplaceSourceNotFound
	}
	if err := marketplace.ValidateSource(source); err != nil {
		if auditErr := s.AuditSourceFailure(ctx, "refresh", sourceID, "validation"); auditErr != nil {
			return marketplace.Snapshot{}, fmt.Errorf("%w (persist failure audit: %v)", err, auditErr)
		}
		return marketplace.Snapshot{}, err
	}
	trusted, _ := storage.QuotaActorFromContext(ctx)
	return s.manager.Refresh(ctx, source, marketplace.OperationActor{ActorID: trusted.UserID, SessionID: trusted.SessionID, CorrelationID: trusted.CorrelationID})
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
	entry, ok, err := s.store.CurrentMarketEntry(ctx, sourceID, pluginID, version, strings.ToLower(strings.TrimSpace(digest)))
	if err != nil {
		return PluginPackageCandidate{}, err
	}
	if !ok {
		return PluginPackageCandidate{}, ErrMarketplaceEntryNotFound
	}
	cachePath := filepath.Join(s.cacheRoot, strings.ToLower(entry.PackageSHA256))
	validated, err := s.validator.ValidatePackage(cachePath, plugins.PackageExpectation{ID: entry.ID, Version: entry.Version, SHA256: entry.PackageSHA256, Compatibility: entry.Compatibility})
	if err != nil {
		return PluginPackageCandidate{}, err
	}
	return PluginPackageCandidate{Package: validated, CachePath: cachePath}, nil
}
