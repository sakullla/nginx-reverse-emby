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
	ErrPluginAlreadyInstalled  = errors.New("plugin already installed")
	ErrPluginNotInstalled      = errors.New("plugin not installed")
	ErrMarketplaceSourceExists = errors.New("marketplace source already exists")
)

func backfillPluginOwnershipAndAcquisitions(ctx context.Context, db *gorm.DB) error {
	var instances []PluginInstanceRow
	if err := db.WithContext(ctx).Where("resource_group_id <> ?", "").Find(&instances).Error; err != nil {
		return err
	}
	now := time.Now().UTC()
	for _, instance := range instances {
		binding := ResourceBindingRow{ID: pluginStorageID("binding"), ResourceKind: "plugin_instance", ResourceID: instance.ID, ResourceGroupID: instance.ResourceGroupID, UpdatedAt: now}
		if err := db.WithContext(ctx).Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "resource_kind"}, {Name: "resource_id"}}, DoNothing: true}).Create(&binding).Error; err != nil {
			return err
		}
	}
	var snapshots []MarketSnapshotRow
	if err := db.WithContext(ctx).Find(&snapshots).Error; err != nil {
		return err
	}
	bySnapshot := make(map[string]string, len(snapshots))
	for _, snapshot := range snapshots {
		bySnapshot[snapshot.ID] = snapshot.SourceID
	}
	var entries []MarketEntryRow
	if err := db.WithContext(ctx).Find(&entries).Error; err != nil {
		return err
	}
	for _, entry := range entries {
		sourceID := bySnapshot[entry.SnapshotID]
		if sourceID == "" {
			continue
		}
		row := PluginPackageAcquisitionRow{SourceID: sourceID, Digest: strings.ToLower(entry.PackageDigest), Status: "catalog", UpdatedAt: now}
		if err := db.WithContext(ctx).Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "source_id"}, {Name: "digest"}}, DoNothing: true}).Create(&row).Error; err != nil {
			return err
		}
	}
	return nil
}

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
		if err := tx.Create(&row).Error; err != nil {
			if errors.Is(err, gorm.ErrDuplicatedKey) || strings.Contains(strings.ToLower(err.Error()), "unique") {
				return ErrMarketplaceSourceExists
			}
			return err
		}
		if source.Kind == marketplace.SourceKindCustom {
			actor, _ := QuotaActorFromContext(ctx)
			metadata, _ := json.Marshal(map[string]any{"kind": source.Kind, "risk_label": source.RiskLabel, "has_credential_ref": source.CredentialRef != ""})
			if err := tx.Create(&AuditEventRow{ID: pluginStorageID("audit"), ActorID: actor.UserID, SessionID: actor.SessionID, Action: "marketplace.source.add", TargetKind: "marketplace_source", TargetID: source.ID, CorrelationID: actor.CorrelationID, Result: "success", MetadataJSON: string(metadata), CreatedAt: time.Now().UTC()}).Error; err != nil {
				return err
			}
		}
		return nil
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
	if row.Deleting {
		return marketplace.Source{}, false, nil
	}
	return marketplaceSourceFromRow(row), true, nil
}

func (s *GormStore) DeleteMarketplaceSource(ctx context.Context, sourceID string) (marketplace.SourceDeletion, error) {
	if sourceID == marketplace.OfficialSourceID {
		return marketplace.SourceDeletion{}, errors.New("official marketplace source cannot be deleted")
	}
	var deletion marketplace.SourceDeletion
	err := s.writeTransaction(ctx, func(tx *gorm.DB) error {
		var source MarketplaceSourceRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND kind = ?", sourceID, marketplace.SourceKindCustom).First(&source).Error; err != nil {
			return err
		}
		if source.Deleting {
			var pending MarketplaceSourceDeletionRow
			if err := tx.Where("source_id = ?", sourceID).First(&pending).Error; err != nil {
				return err
			}
			if err := json.Unmarshal([]byte(pending.SnapshotPathsJSON), &deletion.SnapshotPaths); err != nil {
				return err
			}
			var intents []PluginCacheGCIntentRow
			if err := tx.Where("source_id = ?", sourceID).Find(&intents).Error; err != nil {
				return err
			}
			for _, intent := range intents {
				deletion.CacheDigests = append(deletion.CacheDigests, intent.Digest)
			}
			return nil
		}
		if err := tx.Model(&MarketplaceSourceRow{}).Where("id = ? AND deleting = ?", sourceID, false).Updates(map[string]any{"deleting": true, "current_snapshot_id": ""}).Error; err != nil {
			return err
		}
		var snapshots []MarketSnapshotRow
		if err := tx.Where("source_id = ?", sourceID).Find(&snapshots).Error; err != nil {
			return err
		}
		snapshotIDs := make([]string, 0, len(snapshots))
		for _, snapshot := range snapshots {
			snapshotIDs = append(snapshotIDs, snapshot.ID)
			deletion.SnapshotPaths = append(deletion.SnapshotPaths, snapshot.Path)
		}
		var entries []MarketEntryRow
		if len(snapshotIDs) > 0 {
			if err := tx.Where("snapshot_id IN ?", snapshotIDs).Find(&entries).Error; err != nil {
				return err
			}
			if err := tx.Where("snapshot_id IN ?", snapshotIDs).Delete(&MarketEntryRow{}).Error; err != nil {
				return err
			}
			if err := tx.Where("id IN ?", snapshotIDs).Delete(&MarketSnapshotRow{}).Error; err != nil {
				return err
			}
		}
		var acquisitions []PluginPackageAcquisitionRow
		if err := tx.Where("source_id = ?", sourceID).Find(&acquisitions).Error; err != nil {
			return err
		}
		seenDigests := map[string]struct{}{}
		for _, entry := range entries {
			seenDigests[strings.ToLower(entry.PackageDigest)] = struct{}{}
		}
		for _, acquisition := range acquisitions {
			seenDigests[strings.ToLower(acquisition.Digest)] = struct{}{}
		}
		now := time.Now().UTC()
		for digest := range seenDigests {
			if _, seen := seenDigests[digest]; seen {
				deletion.CacheDigests = append(deletion.CacheDigests, digest)
			}
			intent := PluginCacheGCIntentRow{SourceID: sourceID, Digest: digest, Status: "pending", UpdatedAt: now}
			if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "source_id"}, {Name: "digest"}}, DoUpdates: clause.AssignmentColumns([]string{"status", "updated_at"})}).Create(&intent).Error; err != nil {
				return err
			}
		}
		if err := tx.Where("source_id = ?", sourceID).Delete(&PluginPackageAcquisitionRow{}).Error; err != nil {
			return err
		}
		pathsJSON, _ := json.Marshal(deletion.SnapshotPaths)
		if err := tx.Create(&MarketplaceSourceDeletionRow{SourceID: sourceID, SnapshotPathsJSON: string(pathsJSON), UpdatedAt: now}).Error; err != nil {
			return err
		}
		actor, _ := QuotaActorFromContext(ctx)
		metadata, _ := json.Marshal(map[string]any{"kind": source.Kind, "risk_label": source.RiskLabel, "has_credential_ref": source.CredentialRef != ""})
		return tx.Create(&AuditEventRow{ID: pluginStorageID("audit"), ActorID: actor.UserID, SessionID: actor.SessionID, Action: "marketplace.source.delete", TargetKind: "marketplace_source", TargetID: source.ID, CorrelationID: actor.CorrelationID, Result: "accepted", MetadataJSON: string(metadata), CreatedAt: now}).Error
	})
	return deletion, err
}

func (s *GormStore) ClaimPackageGC(ctx context.Context, sourceID, digest string) (bool, error) {
	digest = strings.ToLower(digest)
	claimed := false
	err := s.writeTransaction(ctx, func(tx *gorm.DB) error {
		var intent PluginCacheGCIntentRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("source_id = ? AND digest = ?", sourceID, digest).First(&intent).Error; err != nil {
			if err == gorm.ErrRecordNotFound {
				return nil
			}
			return err
		}
		var catalog, lifecycle, acquisition int64
		if err := tx.Model(&MarketEntryRow{}).Where("package_digest = ?", digest).Count(&catalog).Error; err != nil {
			return err
		}
		if err := tx.Model(&InstalledPluginRow{}).Where("active_package_digest = ? OR staged_package_digest = ? OR rollback_package_digest = ?", digest, digest, digest).Count(&lifecycle).Error; err != nil {
			return err
		}
		if err := tx.Model(&PluginPackageAcquisitionRow{}).Where("digest = ?", digest).Count(&acquisition).Error; err != nil {
			return err
		}
		if catalog+lifecycle+acquisition > 0 {
			return tx.Delete(&intent).Error
		}
		if intent.Status == "deleting" {
			claimed = true
			return nil
		}
		result := tx.Model(&PluginCacheGCIntentRow{}).Where("source_id = ? AND digest = ? AND status = ?", sourceID, digest, "pending").Updates(map[string]any{"status": "deleting", "last_error": "", "updated_at": time.Now().UTC()})
		if result.Error != nil {
			return result.Error
		}
		claimed = result.RowsAffected == 1
		return nil
	})
	return claimed, err
}

func (s *GormStore) ListPackageGCIntents(ctx context.Context) ([]marketplace.PackageGCIntent, error) {
	var rows []PluginCacheGCIntentRow
	if err := s.db.WithContext(ctx).Order("source_id, digest").Find(&rows).Error; err != nil {
		return nil, err
	}
	result := make([]marketplace.PackageGCIntent, 0, len(rows))
	for _, row := range rows {
		result = append(result, marketplace.PackageGCIntent{SourceID: row.SourceID, Digest: row.Digest})
	}
	return result, nil
}

func (s *GormStore) CompletePackageGC(ctx context.Context, sourceID, digest, failure string) error {
	return s.writeTransaction(ctx, func(tx *gorm.DB) error {
		if failure != "" {
			return tx.Model(&PluginCacheGCIntentRow{}).Where("source_id = ? AND digest = ?", sourceID, digest).Updates(map[string]any{"status": "pending", "last_error": failure, "updated_at": time.Now().UTC()}).Error
		}
		if err := tx.Where("source_id = ? AND digest = ? AND status = ?", sourceID, digest, "deleting").Delete(&PluginCacheGCIntentRow{}).Error; err != nil {
			return err
		}
		return tx.Where("digest = ?", digest).Delete(&PluginPackageRow{}).Error
	})
}

func (s *GormStore) CompleteMarketplaceSourceDeletion(ctx context.Context, sourceID string, failure string) error {
	return s.writeTransaction(ctx, func(tx *gorm.DB) error {
		if failure != "" {
			return tx.Model(&MarketplaceSourceDeletionRow{}).Where("source_id = ?", sourceID).Updates(map[string]any{"last_error": failure, "updated_at": time.Now().UTC()}).Error
		}
		var pending int64
		if err := tx.Model(&PluginCacheGCIntentRow{}).Where("source_id = ?", sourceID).Count(&pending).Error; err != nil {
			return err
		}
		if pending != 0 {
			return errors.New("marketplace source cache cleanup is still pending")
		}
		if err := tx.Where("source_id = ?", sourceID).Delete(&MarketplaceSourceDeletionRow{}).Error; err != nil {
			return err
		}
		return tx.Where("id = ? AND deleting = ?", sourceID, true).Delete(&MarketplaceSourceRow{}).Error
	})
}

func (s *GormStore) AcquireRefreshLease(ctx context.Context, operation marketplace.RefreshOperation) error {
	if operation.ID == "" || operation.SourceID == "" || operation.LeaseToken == "" || !operation.LeaseExpiresAt.After(operation.StartedAt) || operation.Status != "running" {
		return errors.New("valid marketplace refresh lease is required")
	}
	row := marketplaceRefreshOperationToRow(operation)
	return s.writeTransaction(ctx, func(tx *gorm.DB) error {
		result := tx.Model(&MarketplaceSourceRow{}).
			Where("id = ? AND deleting = ? AND (refresh_lease_token = ? OR refresh_lease_expires_at <= ?)", operation.SourceID, false, "", operation.StartedAt).
			Updates(map[string]any{"refresh_lease_token": operation.LeaseToken, "refresh_lease_expires_at": operation.LeaseExpiresAt, "last_result": "running", "last_error": "", "updated_at": operation.StartedAt})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return marketplace.ErrRefreshLeaseHeld
		}
		var interrupted []MarketplaceRefreshOperationRow
		if err := tx.Where("source_id = ? AND status = ? AND lease_expires_at <= ?", operation.SourceID, "running", operation.StartedAt).Find(&interrupted).Error; err != nil {
			return err
		}
		for _, previous := range interrupted {
			if err := tx.Model(&MarketplaceRefreshOperationRow{}).Where("id = ? AND status = ?", previous.ID, "running").Updates(map[string]any{"status": "failed", "error_class": "interrupted", "error": "refresh lease expired before completion", "finished_at": operation.StartedAt}).Error; err != nil {
				return err
			}
			previous.Status, previous.ErrorClass, previous.Error, previous.FinishedAt = "failed", "interrupted", "refresh lease expired before completion", &operation.StartedAt
			if err := tx.Create(marketplaceRefreshAudit(marketplaceRefreshOperationFromRow(previous), "failure", operation.StartedAt)).Error; err != nil {
				return err
			}
		}
		return tx.Create(&row).Error
	})
}

func (s *GormStore) RenewRefreshLease(ctx context.Context, operation marketplace.RefreshOperation) error {
	return s.writeTransaction(ctx, func(tx *gorm.DB) error {
		result := tx.Model(&MarketplaceSourceRow{}).Where("id = ? AND deleting = ? AND refresh_lease_token = ?", operation.SourceID, false, operation.LeaseToken).Update("refresh_lease_expires_at", operation.LeaseExpiresAt)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return errors.New("refresh lease is stale or source is deleting")
		}
		result = tx.Model(&MarketplaceRefreshOperationRow{}).Where("id = ? AND source_id = ? AND lease_token = ? AND status = ?", operation.ID, operation.SourceID, operation.LeaseToken, "running").Update("lease_expires_at", operation.LeaseExpiresAt)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return errors.New("refresh operation lease is stale")
		}
		return nil
	})
}

func (s *GormStore) StagePackageAcquisition(ctx context.Context, sourceID, digest, operationID string) error {
	digest = strings.ToLower(strings.TrimSpace(digest))
	return s.writeTransaction(ctx, func(tx *gorm.DB) error {
		var source MarketplaceSourceRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND deleting = ?", sourceID, false).First(&source).Error; err != nil {
			return err
		}
		var deleting int64
		if err := tx.Model(&PluginCacheGCIntentRow{}).Where("digest = ? AND status = ?", digest, "deleting").Count(&deleting).Error; err != nil {
			return err
		}
		if deleting != 0 {
			return errors.New("package cache digest is being deleted")
		}
		row := PluginPackageAcquisitionRow{SourceID: sourceID, Digest: digest, OperationID: operationID, Status: "staging", UpdatedAt: time.Now().UTC()}
		return tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "source_id"}, {Name: "digest"}}, DoUpdates: clause.AssignmentColumns([]string{"operation_id", "status", "updated_at"})}).Create(&row).Error
	})
}

func (s *GormStore) CompletePackageAcquisitions(ctx context.Context, sourceID, operationID string, succeeded bool) error {
	return s.writeTransaction(ctx, func(tx *gorm.DB) error {
		var rows []PluginPackageAcquisitionRow
		if err := tx.Where("source_id = ? AND operation_id = ?", sourceID, operationID).Find(&rows).Error; err != nil {
			return err
		}
		now := time.Now().UTC()
		if succeeded {
			return tx.Model(&PluginPackageAcquisitionRow{}).Where("source_id = ? AND operation_id = ?", sourceID, operationID).Updates(map[string]any{"operation_id": "", "status": "catalog", "updated_at": now}).Error
		}
		for _, row := range rows {
			intent := PluginCacheGCIntentRow{SourceID: sourceID, Digest: row.Digest, Status: "pending", UpdatedAt: now}
			if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "source_id"}, {Name: "digest"}}, DoUpdates: clause.AssignmentColumns([]string{"status", "updated_at"})}).Create(&intent).Error; err != nil {
				return err
			}
		}
		return tx.Where("source_id = ? AND operation_id = ?", sourceID, operationID).Delete(&PluginPackageAcquisitionRow{}).Error
	})
}

func (s *GormStore) RecordRefreshRejection(ctx context.Context, sourceID string, actor marketplace.OperationActor, errorClass string) error {
	now := time.Now().UTC()
	metadata := `{"redacted":true}`
	return s.AppendAuditEvent(ctx, AuditEventRow{ID: pluginStorageID("audit"), ActorID: actor.ActorID, SessionID: actor.SessionID, Action: "marketplace.source.refresh", TargetKind: "marketplace_source", TargetID: sourceID, CorrelationID: actor.CorrelationID, Result: "failure", ErrorClass: errorClass, MetadataJSON: metadata, CreatedAt: now})
}

func (s *GormStore) SaveRefreshOperation(ctx context.Context, operation marketplace.RefreshOperation) error {
	if operation.Status == "running" && operation.LeaseToken != "" {
		return s.AcquireRefreshLease(ctx, operation)
	}
	row := marketplaceRefreshOperationToRow(operation)
	return s.writeTransaction(ctx, func(tx *gorm.DB) error {
		updatedAt := operation.StartedAt
		if operation.FinishedAt != nil {
			updatedAt = *operation.FinishedAt
		}
		if operation.LeaseToken == "" {
			if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "id"}}, DoUpdates: clause.AssignmentColumns([]string{"commit", "status", "error_class", "error", "diff_json", "finished_at"})}).Create(&row).Error; err != nil {
				return err
			}
			if err := tx.Model(&MarketplaceSourceRow{}).Where("id = ?", operation.SourceID).Updates(map[string]any{"last_result": operation.Status, "last_error": operation.Error, "updated_at": updatedAt}).Error; err != nil {
				return err
			}
		} else {
			result := tx.Model(&MarketplaceRefreshOperationRow{}).Where("id = ? AND source_id = ? AND lease_token = ? AND status = ?", operation.ID, operation.SourceID, operation.LeaseToken, "running").Updates(map[string]any{"commit": operation.Commit, "status": operation.Status, "error_class": operation.ErrorClass, "error": operation.Error, "diff_json": pluginDefaultJSON(operation.DiffJSON), "finished_at": operation.FinishedAt})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return errors.New("refresh operation lease is stale or already completed")
			}
			if err := tx.Model(&MarketplaceSourceRow{}).Where("id = ? AND refresh_lease_token = ?", operation.SourceID, operation.LeaseToken).Updates(map[string]any{"refresh_lease_token": "", "refresh_lease_expires_at": time.Time{}, "last_result": operation.Status, "last_error": operation.Error, "updated_at": updatedAt}).Error; err != nil {
				return err
			}
		}
		if operation.Status == "failed" {
			return tx.Create(marketplaceRefreshAudit(operation, "failure", updatedAt)).Error
		}
		return nil
	})
}

func (s *GormStore) PromoteSnapshotAndCompleteRefresh(ctx context.Context, source marketplace.Source, snapshot marketplace.Snapshot, operation marketplace.RefreshOperation) error {
	if err := marketplace.ValidateSource(source); err != nil {
		return err
	}
	entriesJSON, err := json.Marshal(snapshot.Entries)
	if err != nil {
		return err
	}
	return s.writeTransaction(ctx, func(tx *gorm.DB) error {
		if operation.SourceID != source.ID || operation.Commit != snapshot.Commit || operation.Status != "succeeded" || operation.FinishedAt == nil || operation.LeaseToken == "" {
			return errors.New("completed refresh operation does not match promoted snapshot")
		}
		sourceResult := tx.Model(&MarketplaceSourceRow{}).Where("id = ? AND deleting = ? AND refresh_lease_token = ? AND refresh_lease_expires_at >= ?", source.ID, false, operation.LeaseToken, *operation.FinishedAt).Updates(map[string]any{"current_snapshot_id": snapshot.ID, "last_result": "succeeded", "last_error": "", "updated_at": snapshot.ValidatedAt, "last_completed_at": *operation.FinishedAt, "refresh_lease_token": "", "refresh_lease_expires_at": time.Time{}})
		if sourceResult.Error != nil {
			return sourceResult.Error
		}
		if sourceResult.RowsAffected != 1 {
			return errors.New("marketplace source was deleted or refresh lease expired")
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
			acquisition := PluginPackageAcquisitionRow{SourceID: source.ID, Digest: row.PackageDigest, OperationID: operation.ID, Status: "catalog", UpdatedAt: snapshot.ValidatedAt}
			if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "source_id"}, {Name: "digest"}}, DoUpdates: clause.AssignmentColumns([]string{"operation_id", "status", "updated_at"})}).Create(&acquisition).Error; err != nil {
				return err
			}
		}
		result := tx.Model(&MarketplaceRefreshOperationRow{}).
			Where("id = ? AND source_id = ? AND lease_token = ? AND status = ?", operation.ID, source.ID, operation.LeaseToken, "running").
			Updates(map[string]any{"commit": operation.Commit, "status": operation.Status, "error_class": "", "error": "", "diff_json": pluginDefaultJSON(operation.DiffJSON), "finished_at": operation.FinishedAt})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return errors.New("refresh operation is stale or already completed")
		}
		return tx.Create(marketplaceRefreshAudit(operation, "success", *operation.FinishedAt)).Error
	})
}

// PromoteSnapshot is retained for internal fixtures that seed a catalog. Real
// refreshes must use PromoteSnapshotAndCompleteRefresh.
func (s *GormStore) PromoteSnapshot(ctx context.Context, source marketplace.Source, snapshot marketplace.Snapshot) error {
	now := snapshot.ValidatedAt
	op := marketplace.RefreshOperation{ID: pluginStorageID("refresh"), SourceID: source.ID, Commit: snapshot.Commit, Status: "running", StartedAt: now, LeaseToken: pluginStorageID("lease"), LeaseExpiresAt: now.Add(time.Minute)}
	if _, ok, err := s.GetMarketplaceSource(ctx, source.ID); err != nil {
		return err
	} else if !ok {
		if err := s.SaveMarketplaceSource(ctx, source); err != nil {
			return err
		}
	}
	if err := s.AcquireRefreshLease(ctx, op); err != nil {
		return err
	}
	op.Status, op.FinishedAt = "succeeded", &now
	return s.PromoteSnapshotAndCompleteRefresh(ctx, source, snapshot, op)
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
	Package             PluginPackageRow
	Installed           InstalledPluginRow
	Grants              []PluginGrantRow
	Operation           PluginOperationRow
	Audit               AuditEventRow
	RequireAcquisition  bool
	AcquisitionSourceID string
	AcquisitionDigest   string
}

func (s *GormStore) InstallPlugin(ctx context.Context, input PluginInstallTransaction) error {
	return s.writeTransaction(ctx, func(tx *gorm.DB) error {
		if input.RequireAcquisition {
			if err := validatePackageAcquisitionTx(tx, input.AcquisitionSourceID, input.AcquisitionDigest); err != nil {
				return err
			}
		}
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
	PluginID                   string
	ExpectedActive             string
	ExpectedStateVersion       uint64
	ExpectedPendingOperationID string
	Installed                  *InstalledPluginRow
	Package                    *PluginPackageRow
	ReplaceGrants              []PluginGrantRow
	ReplaceInstance            *PluginInstanceRow
	ReplaceInstances           []PluginInstanceRow
	DeletePlugin               bool
	DeleteInstances            bool
	DeleteGrants               bool
	Operation                  PluginOperationRow
	CompleteOperation          bool
	Audit                      AuditEventRow
	RequireAcquisition         bool
	AcquisitionSourceID        string
	AcquisitionDigest          string
	ValidateInstanceScope      bool
	PromoteInstanceBinding     bool
}

func (s *GormStore) ApplyPluginMutation(ctx context.Context, mutation PluginMutation) error {
	return s.writeTransaction(ctx, func(tx *gorm.DB) error {
		if mutation.RequireAcquisition {
			if err := validatePackageAcquisitionTx(tx, mutation.AcquisitionSourceID, mutation.AcquisitionDigest); err != nil {
				return err
			}
		}
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
		if mutation.ExpectedStateVersion != 0 && current.StateVersion != mutation.ExpectedStateVersion {
			return errors.New("plugin state changed concurrently")
		}
		if mutation.ExpectedPendingOperationID != "" && current.PendingOperationID != mutation.ExpectedPendingOperationID {
			return errors.New("plugin operation is stale or out of order")
		}
		if mutation.Package != nil {
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(mutation.Package).Error; err != nil {
				return err
			}
		}
		if mutation.DeletePlugin {
			if mutation.DeleteInstances {
				var instanceIDs []string
				if err := tx.Model(&PluginInstanceRow{}).Where("plugin_id = ?", mutation.PluginID).Pluck("id", &instanceIDs).Error; err != nil {
					return err
				}
				if len(instanceIDs) > 0 {
					if err := tx.Where("resource_kind = ? AND resource_id IN ?", "plugin_instance", instanceIDs).Delete(&ResourceBindingRow{}).Error; err != nil {
						return err
					}
				}
				if err := tx.Where("plugin_id = ?", mutation.PluginID).Delete(&PluginInstanceRow{}).Error; err != nil {
					return err
				}
			}
			if mutation.DeleteGrants {
				if err := tx.Where("plugin_id = ?", mutation.PluginID).Delete(&PluginGrantRow{}).Error; err != nil {
					return err
				}
			}
			result := tx.Where("plugin_id = ? AND state_version = ?", mutation.PluginID, current.StateVersion).Delete(&InstalledPluginRow{})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return errors.New("plugin state changed concurrently")
			}
		} else {
			if mutation.Installed == nil {
				return errors.New("installed plugin update is required")
			}
			if mutation.Installed.PluginID != mutation.PluginID {
				return errors.New("installed plugin identity differs from mutation target")
			}
			next := *mutation.Installed
			next.StateVersion = current.StateVersion + 1
			result := tx.Model(&InstalledPluginRow{}).Where("plugin_id = ? AND state_version = ?", mutation.PluginID, current.StateVersion).Select("*").Omit("plugin_id").Updates(&next)
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return errors.New("plugin state changed concurrently")
			}
			mutation.Installed.StateVersion = next.StateVersion
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
				if mutation.ValidateInstanceScope {
					if err := validatePluginInstanceScopeTx(tx, *mutation.ReplaceInstance, mutation.PromoteInstanceBinding); err != nil {
						return err
					}
				}
				if err := tx.Clauses(clause.OnConflict{UpdateAll: true}).Create(mutation.ReplaceInstance).Error; err != nil {
					return err
				}
			}
			if len(mutation.ReplaceInstances) > 0 {
				for index := range mutation.ReplaceInstances {
					if mutation.ReplaceInstances[index].PluginID != mutation.PluginID {
						return errors.New("plugin instance identity differs from mutation target")
					}
					if err := tx.Clauses(clause.OnConflict{UpdateAll: true}).Create(&mutation.ReplaceInstances[index]).Error; err != nil {
						return err
					}
				}
			}
		}
		if mutation.CompleteOperation {
			result := tx.Model(&PluginOperationRow{}).Where("id = ? AND plugin_id = ? AND completed_at IS NULL", mutation.Operation.ID, mutation.PluginID).Updates(map[string]any{"status": mutation.Operation.Status, "agent_results_json": mutation.Operation.AgentResultsJSON, "error_class": mutation.Operation.ErrorClass, "error": mutation.Operation.Error, "completed_at": mutation.Operation.CompletedAt})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return errors.New("plugin operation is stale, replayed, or already completed")
			}
			return tx.Create(&mutation.Audit).Error
		}
		return createPluginOperationAndAudit(tx, mutation.Operation, mutation.Audit)
	})
}

func validatePackageAcquisitionTx(tx *gorm.DB, sourceID, digest string) error {
	digest = strings.ToLower(strings.TrimSpace(digest))
	var source MarketplaceSourceRow
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND deleting = ?", sourceID, false).First(&source).Error; err != nil {
		return errors.New("marketplace package source is unavailable or deleting")
	}
	var acquisition PluginPackageAcquisitionRow
	if err := tx.Where("source_id = ? AND digest = ? AND status = ?", sourceID, digest, "catalog").First(&acquisition).Error; err != nil {
		return errors.New("verified package acquisition is unavailable")
	}
	var deleting int64
	if err := tx.Model(&PluginCacheGCIntentRow{}).Where("digest = ? AND status = ?", digest, "deleting").Count(&deleting).Error; err != nil {
		return err
	}
	if deleting != 0 {
		return errors.New("verified package cache is being deleted")
	}
	return nil
}

func validatePluginInstanceScopeTx(tx *gorm.DB, instance PluginInstanceRow, promote bool) error {
	groupID := instance.PendingResourceGroupID
	targetJSON := instance.PendingTargetJSON
	if promote || groupID == "" {
		groupID = instance.ResourceGroupID
		targetJSON = instance.TargetJSON
	}
	if strings.TrimSpace(groupID) == "" {
		return errors.New("plugin instance resource group is required")
	}
	var group ResourceGroupRow
	if err := tx.Where("id = ?", groupID).First(&group).Error; err != nil {
		return errors.New("plugin instance resource group does not exist")
	}
	var targets []string
	if strings.TrimSpace(targetJSON) != "" && strings.TrimSpace(targetJSON) != "null" {
		if err := json.Unmarshal([]byte(targetJSON), &targets); err != nil {
			return errors.New("plugin instance targets are invalid")
		}
	}
	if len(targets) == 0 {
		targets = []string{"local"}
	}
	for _, target := range targets {
		var binding ResourceBindingRow
		if err := tx.Where("resource_kind = ? AND resource_id = ?", "agent", strings.TrimSpace(target)).First(&binding).Error; err != nil || binding.ResourceGroupID != groupID {
			return errors.New("plugin instance target is outside the selected resource group")
		}
	}
	if promote || instance.ConfigVersion == 0 {
		binding := ResourceBindingRow{ID: pluginStorageID("binding"), ResourceKind: "plugin_instance", ResourceID: instance.ID, ResourceGroupID: groupID, UpdatedAt: time.Now().UTC()}
		return tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "resource_kind"}, {Name: "resource_id"}}, DoUpdates: clause.AssignmentColumns([]string{"resource_group_id", "updated_at"})}).Create(&binding).Error
	}
	return nil
}

func (s *GormStore) ListPluginInstances(ctx context.Context, pluginID string) ([]PluginInstanceRow, error) {
	var rows []PluginInstanceRow
	err := s.db.WithContext(ctx).Where("plugin_id = ?", pluginID).Order("id").Find(&rows).Error
	return rows, err
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
	return MarketplaceSourceRow{ID: source.ID, Kind: source.Kind, Name: source.Name, URL: source.URL, Reference: source.Reference, CredentialRef: source.CredentialRef, RefreshIntervalNS: int64(source.RefreshInterval), RiskLabel: source.RiskLabel, CurrentSnapshotID: source.CurrentSnapshot, LastResult: source.LastResult, LastError: source.LastError, UpdatedAt: source.UpdatedAt, LastCompletedAt: source.LastCompletedAt, RefreshLeaseExpiresAt: source.LeaseExpiresAt, Deleting: source.Deleting}
}

func marketplaceRefreshOperationToRow(operation marketplace.RefreshOperation) MarketplaceRefreshOperationRow {
	return MarketplaceRefreshOperationRow{ID: operation.ID, SourceID: operation.SourceID, Commit: operation.Commit, Status: operation.Status, ErrorClass: operation.ErrorClass, Error: operation.Error, DiffJSON: pluginDefaultJSON(operation.DiffJSON), StartedAt: operation.StartedAt, FinishedAt: operation.FinishedAt, ActorID: operation.Actor.ActorID, SessionID: operation.Actor.SessionID, CorrelationID: operation.Actor.CorrelationID, LeaseToken: operation.LeaseToken, LeaseExpiresAt: operation.LeaseExpiresAt}
}

func marketplaceRefreshOperationFromRow(row MarketplaceRefreshOperationRow) marketplace.RefreshOperation {
	return marketplace.RefreshOperation{ID: row.ID, SourceID: row.SourceID, Commit: row.Commit, Status: row.Status, ErrorClass: row.ErrorClass, Error: row.Error, DiffJSON: row.DiffJSON, StartedAt: row.StartedAt, FinishedAt: row.FinishedAt, Actor: marketplace.OperationActor{ActorID: row.ActorID, SessionID: row.SessionID, CorrelationID: row.CorrelationID}, LeaseToken: row.LeaseToken, LeaseExpiresAt: row.LeaseExpiresAt}
}

func marketplaceSourceFromRow(row MarketplaceSourceRow) marketplace.Source {
	return marketplace.Source{ID: row.ID, Kind: row.Kind, Name: row.Name, URL: row.URL, Reference: row.Reference, CredentialRef: row.CredentialRef, RefreshInterval: time.Duration(row.RefreshIntervalNS), RiskLabel: row.RiskLabel, CurrentSnapshot: row.CurrentSnapshotID, LastResult: row.LastResult, LastError: row.LastError, UpdatedAt: row.UpdatedAt, LastCompletedAt: row.LastCompletedAt, LeaseExpiresAt: row.RefreshLeaseExpiresAt, Deleting: row.Deleting}
}

func pluginDefaultJSON(value string) string {
	if strings.TrimSpace(value) == "" {
		return "{}"
	}
	return value
}

func pluginStorageID(prefix string) string { return securityID(prefix) }

func marketplaceRefreshAudit(operation marketplace.RefreshOperation, result string, now time.Time) AuditEventRow {
	metadata, _ := json.Marshal(map[string]any{"operation_id": operation.ID, "commit": operation.Commit, "diff": json.RawMessage(pluginDefaultJSON(operation.DiffJSON))})
	return AuditEventRow{ID: pluginStorageID("audit"), ActorID: operation.Actor.ActorID, SessionID: operation.Actor.SessionID, Action: "marketplace.source.refresh", TargetKind: "marketplace_source", TargetID: operation.SourceID, CorrelationID: operation.Actor.CorrelationID, Result: result, ErrorClass: operation.ErrorClass, MetadataJSON: string(metadata), CreatedAt: now}
}

func pluginAudit(id, actor, action, pluginID, result, errorClass string, metadata any, now time.Time) AuditEventRow {
	encoded, _ := json.Marshal(metadata)
	return AuditEventRow{ID: id, ActorID: actor, Action: action, TargetKind: "plugin", TargetID: pluginID, Result: result, ErrorClass: errorClass, MetadataJSON: string(encoded), CreatedAt: now}
}
