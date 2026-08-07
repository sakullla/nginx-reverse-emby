package storage

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/marketplace"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/plugins"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrPluginAlreadyInstalled = errors.New("plugin already installed")
	ErrPluginNotInstalled     = errors.New("plugin not installed")
)

func (s *GormStore) SaveMarketplaceSource(ctx context.Context, source marketplace.Source) error {
	if err := marketplace.ValidateSource(source); err != nil {
		return err
	}
	row := marketplaceSourceToRow(source)
	return s.writeTransaction(ctx, func(tx *gorm.DB) error {
		if source.Kind == marketplace.SourceKindCustom {
			var existing MarketplaceSourceRow
			if err := tx.Where("id = ?", marketplace.OfficialSourceID).First(&existing).Error; err != nil && err != gorm.ErrRecordNotFound {
				return err
			}
		}
		return tx.Clauses(clause.OnConflict{UpdateAll: true}).Create(&row).Error
	})
}

func (s *GormStore) ListMarketplaceSources(ctx context.Context) ([]marketplace.Source, error) {
	var rows []MarketplaceSourceRow
	if err := s.db.WithContext(ctx).Order("kind, id").Find(&rows).Error; err != nil {
		return nil, err
	}
	result := make([]marketplace.Source, 0, len(rows))
	for _, row := range rows {
		result = append(result, marketplaceSourceFromRow(row))
	}
	return result, nil
}

func (s *GormStore) GetMarketplaceSource(ctx context.Context, sourceID string) (marketplace.Source, bool, error) {
	var row MarketplaceSourceRow
	err := s.db.WithContext(ctx).Where("id = ?", sourceID).First(&row).Error
	if err == gorm.ErrRecordNotFound {
		return marketplace.Source{}, false, nil
	}
	if err != nil {
		return marketplace.Source{}, false, err
	}
	return marketplaceSourceFromRow(row), true, nil
}

func (s *GormStore) DeleteMarketplaceSource(ctx context.Context, sourceID string) error {
	if sourceID == marketplace.OfficialSourceID {
		return errors.New("official marketplace source cannot be deleted")
	}
	return s.writeTransaction(ctx, func(tx *gorm.DB) error {
		var source MarketplaceSourceRow
		if err := tx.Where("id = ? AND kind = ?", sourceID, marketplace.SourceKindCustom).First(&source).Error; err != nil {
			return err
		}
		// InstalledPluginRow and PluginPackageRow are intentionally untouched.
		return tx.Delete(&source).Error
	})
}

func (s *GormStore) SaveRefreshOperation(ctx context.Context, operation marketplace.RefreshOperation) error {
	row := MarketplaceRefreshOperationRow{ID: operation.ID, SourceID: operation.SourceID, Commit: operation.Commit, Status: operation.Status, ErrorClass: operation.ErrorClass, Error: operation.Error, DiffJSON: pluginDefaultJSON(operation.DiffJSON), StartedAt: operation.StartedAt, FinishedAt: operation.FinishedAt}
	return s.writeTransaction(ctx, func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "id"}}, DoUpdates: clause.AssignmentColumns([]string{"commit", "status", "error_class", "error", "diff_json", "finished_at"})}).Create(&row).Error; err != nil {
			return err
		}
		updatedAt := operation.StartedAt
		if operation.FinishedAt != nil {
			updatedAt = *operation.FinishedAt
		}
		return tx.Model(&MarketplaceSourceRow{}).Where("id = ?", operation.SourceID).Updates(map[string]any{
			"last_result": operation.Status,
			"last_error":  operation.Error,
			"updated_at":  updatedAt,
		}).Error
	})
}

func (s *GormStore) PromoteSnapshot(ctx context.Context, source marketplace.Source, snapshot marketplace.Snapshot) error {
	if err := marketplace.ValidateSource(source); err != nil {
		return err
	}
	entriesJSON, err := json.Marshal(snapshot.Entries)
	if err != nil {
		return err
	}
	return s.writeTransaction(ctx, func(tx *gorm.DB) error {
		source.CurrentSnapshot = snapshot.ID
		source.LastResult = "succeeded"
		source.LastError = ""
		source.UpdatedAt = snapshot.ValidatedAt
		sourceRow := marketplaceSourceToRow(source)
		if err := tx.Clauses(clause.OnConflict{UpdateAll: true}).Create(&sourceRow).Error; err != nil {
			return err
		}
		snapshotRow := MarketSnapshotRow{ID: snapshot.ID, SourceID: snapshot.SourceID, Commit: snapshot.Commit, Path: snapshot.Path, EntriesJSON: string(entriesJSON), ValidatedAt: snapshot.ValidatedAt}
		if err := tx.Create(&snapshotRow).Error; err != nil {
			return err
		}
		for _, entry := range snapshot.Entries {
			capabilities, _ := json.Marshal(entry.Capabilities)
			compatibility, _ := json.Marshal(entry.Compatibility)
			row := MarketEntryRow{ID: pluginStorageID("entry"), SnapshotID: snapshot.ID, PluginID: entry.ID, Version: entry.Version, Description: entry.Description, CapabilitiesJSON: string(capabilities), CompatibilityJSON: string(compatibility), PackagePath: entry.PackagePath, PackageDigest: strings.ToLower(entry.PackageSHA256), Official: entry.Official}
			if err := tx.Create(&row).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *GormStore) CurrentSnapshot(ctx context.Context, sourceID string) (marketplace.Snapshot, bool, error) {
	var source MarketplaceSourceRow
	if err := s.db.WithContext(ctx).Where("id = ?", sourceID).First(&source).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return marketplace.Snapshot{}, false, nil
		}
		return marketplace.Snapshot{}, false, err
	}
	if source.CurrentSnapshotID == "" {
		return marketplace.Snapshot{}, false, nil
	}
	var row MarketSnapshotRow
	if err := s.db.WithContext(ctx).Where("id = ?", source.CurrentSnapshotID).First(&row).Error; err != nil {
		return marketplace.Snapshot{}, false, err
	}
	var entries []plugins.MarketEntry
	if err := json.Unmarshal([]byte(row.EntriesJSON), &entries); err != nil {
		return marketplace.Snapshot{}, false, err
	}
	return marketplace.Snapshot{ID: row.ID, SourceID: row.SourceID, Commit: row.Commit, Path: row.Path, ValidatedAt: row.ValidatedAt, Entries: entries}, true, nil
}

func (s *GormStore) CurrentMarketEntry(ctx context.Context, sourceID, pluginID, version, digest string) (plugins.MarketEntry, bool, error) {
	snapshot, ok, err := s.CurrentSnapshot(ctx, sourceID)
	if err != nil || !ok {
		return plugins.MarketEntry{}, false, err
	}
	for _, entry := range snapshot.Entries {
		if entry.ID == pluginID && entry.Version == version && strings.EqualFold(entry.PackageSHA256, digest) {
			return entry, true, nil
		}
	}
	return plugins.MarketEntry{}, false, nil
}

func (s *GormStore) PackageReferenced(ctx context.Context, digest string) (bool, error) {
	var count int64
	err := s.db.WithContext(ctx).Model(&InstalledPluginRow{}).
		Where("active_package_digest = ? OR staged_package_digest = ? OR rollback_package_digest = ?", digest, digest, digest).
		Count(&count).Error
	return count > 0, err
}

type PluginInstallTransaction struct {
	Package   PluginPackageRow
	Installed InstalledPluginRow
	Grants    []PluginGrantRow
	Operation PluginOperationRow
	Audit     AuditEventRow
}

func (s *GormStore) InstallPlugin(ctx context.Context, input PluginInstallTransaction) error {
	return s.writeTransaction(ctx, func(tx *gorm.DB) error {
		var count int64
		if err := tx.Model(&InstalledPluginRow{}).Where("plugin_id = ?", input.Installed.PluginID).Count(&count).Error; err != nil {
			return err
		}
		if count != 0 {
			return ErrPluginAlreadyInstalled
		}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&input.Package).Error; err != nil {
			return err
		}
		if err := tx.Create(&input.Installed).Error; err != nil {
			return err
		}
		if len(input.Grants) > 0 {
			if err := tx.Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "plugin_id"}, {Name: "package_digest"}, {Name: "permission"}, {Name: "resource_selector"}},
				DoUpdates: clause.AssignmentColumns([]string{"granted_by", "granted_at"}),
			}).Create(&input.Grants).Error; err != nil {
				return err
			}
		}
		return createPluginOperationAndAudit(tx, input.Operation, input.Audit)
	})
}

type PluginMutation struct {
	PluginID        string
	ExpectedActive  string
	Installed       *InstalledPluginRow
	Package         *PluginPackageRow
	ReplaceGrants   []PluginGrantRow
	ReplaceInstance *PluginInstanceRow
	DeletePlugin    bool
	DeleteInstances bool
	DeleteGrants    bool
	Operation       PluginOperationRow
	Audit           AuditEventRow
}

func (s *GormStore) ApplyPluginMutation(ctx context.Context, mutation PluginMutation) error {
	return s.writeTransaction(ctx, func(tx *gorm.DB) error {
		var current InstalledPluginRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("plugin_id = ?", mutation.PluginID).First(&current).Error; err != nil {
			if err == gorm.ErrRecordNotFound {
				return ErrPluginNotInstalled
			}
			return err
		}
		if mutation.ExpectedActive != "" && current.ActivePackageDigest != mutation.ExpectedActive {
			return errors.New("active plugin package changed concurrently")
		}
		if mutation.Package != nil {
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(mutation.Package).Error; err != nil {
				return err
			}
		}
		if mutation.DeletePlugin {
			if mutation.DeleteInstances {
				if err := tx.Where("plugin_id = ?", mutation.PluginID).Delete(&PluginInstanceRow{}).Error; err != nil {
					return err
				}
			}
			if mutation.DeleteGrants {
				if err := tx.Where("plugin_id = ?", mutation.PluginID).Delete(&PluginGrantRow{}).Error; err != nil {
					return err
				}
			}
			if err := tx.Delete(&current).Error; err != nil {
				return err
			}
		} else {
			if mutation.Installed == nil {
				return errors.New("installed plugin update is required")
			}
			if mutation.Installed.PluginID != mutation.PluginID {
				return errors.New("installed plugin identity differs from mutation target")
			}
			if err := tx.Save(mutation.Installed).Error; err != nil {
				return err
			}
			if mutation.ReplaceGrants != nil {
				if err := tx.Where("plugin_id = ?", mutation.PluginID).Delete(&PluginGrantRow{}).Error; err != nil {
					return err
				}
				if len(mutation.ReplaceGrants) > 0 {
					if err := tx.Create(&mutation.ReplaceGrants).Error; err != nil {
						return err
					}
				}
			}
			if mutation.ReplaceInstance != nil {
				if err := tx.Clauses(clause.OnConflict{UpdateAll: true}).Create(mutation.ReplaceInstance).Error; err != nil {
					return err
				}
			}
		}
		return createPluginOperationAndAudit(tx, mutation.Operation, mutation.Audit)
	})
}

func (s *GormStore) RecordPluginOperation(ctx context.Context, operation PluginOperationRow, audit AuditEventRow) error {
	return s.writeTransaction(ctx, func(tx *gorm.DB) error { return createPluginOperationAndAudit(tx, operation, audit) })
}

func (s *GormStore) GetInstalledPlugin(ctx context.Context, pluginID string) (InstalledPluginRow, bool, error) {
	var row InstalledPluginRow
	err := s.db.WithContext(ctx).Where("plugin_id = ?", pluginID).First(&row).Error
	if err == gorm.ErrRecordNotFound {
		return InstalledPluginRow{}, false, nil
	}
	return row, err == nil, err
}

func (s *GormStore) GetPluginPackage(ctx context.Context, digest string) (PluginPackageRow, bool, error) {
	var row PluginPackageRow
	err := s.db.WithContext(ctx).Where("digest = ?", strings.ToLower(digest)).First(&row).Error
	if err == gorm.ErrRecordNotFound {
		return PluginPackageRow{}, false, nil
	}
	return row, err == nil, err
}

func (s *GormStore) ListPluginGrants(ctx context.Context, pluginID string) ([]PluginGrantRow, error) {
	var rows []PluginGrantRow
	err := s.db.WithContext(ctx).Where("plugin_id = ?", pluginID).Order("permission, resource_selector").Find(&rows).Error
	return rows, err
}

func (s *GormStore) GetPluginInstance(ctx context.Context, id string) (PluginInstanceRow, bool, error) {
	var row PluginInstanceRow
	err := s.db.WithContext(ctx).Where("id = ?", id).First(&row).Error
	if err == gorm.ErrRecordNotFound {
		return PluginInstanceRow{}, false, nil
	}
	return row, err == nil, err
}

func (s *GormStore) ListPluginOperations(ctx context.Context, pluginID string) ([]PluginOperationRow, error) {
	var rows []PluginOperationRow
	err := s.db.WithContext(ctx).Where("plugin_id = ?", pluginID).Order("created_at, id").Find(&rows).Error
	return rows, err
}

func createPluginOperationAndAudit(tx *gorm.DB, operation PluginOperationRow, audit AuditEventRow) error {
	if err := tx.Create(&operation).Error; err != nil {
		return err
	}
	return tx.Create(&audit).Error
}

func marketplaceSourceToRow(source marketplace.Source) MarketplaceSourceRow {
	return MarketplaceSourceRow{ID: source.ID, Kind: source.Kind, Name: source.Name, URL: source.URL, Reference: source.Reference, CredentialRef: source.CredentialRef, RefreshIntervalNS: int64(source.RefreshInterval), RiskLabel: source.RiskLabel, CurrentSnapshotID: source.CurrentSnapshot, LastResult: source.LastResult, LastError: source.LastError, UpdatedAt: source.UpdatedAt}
}

func marketplaceSourceFromRow(row MarketplaceSourceRow) marketplace.Source {
	return marketplace.Source{ID: row.ID, Kind: row.Kind, Name: row.Name, URL: row.URL, Reference: row.Reference, CredentialRef: row.CredentialRef, RefreshInterval: time.Duration(row.RefreshIntervalNS), RiskLabel: row.RiskLabel, CurrentSnapshot: row.CurrentSnapshotID, LastResult: row.LastResult, LastError: row.LastError, UpdatedAt: row.UpdatedAt}
}

func pluginDefaultJSON(value string) string {
	if strings.TrimSpace(value) == "" {
		return "{}"
	}
	return value
}

func pluginStorageID(prefix string) string { return securityID(prefix) }

func pluginAudit(id, actor, action, pluginID, result, errorClass string, metadata any, now time.Time) AuditEventRow {
	encoded, _ := json.Marshal(metadata)
	return AuditEventRow{ID: id, ActorID: actor, Action: action, TargetKind: "plugin", TargetID: pluginID, Result: result, ErrorClass: errorClass, MetadataJSON: string(encoded), CreatedAt: now}
}
