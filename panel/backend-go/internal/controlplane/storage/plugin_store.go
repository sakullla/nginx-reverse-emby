package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"sort"
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
	ErrPluginInstanceScope     = errors.New("invalid plugin instance scope")
)

func backfillPluginOwnershipAndAcquisitions(ctx context.Context, db *gorm.DB) error {
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&PluginInstanceRow{}).Where("state_version = 0").Update("state_version", 1).Error; err != nil {
			return err
		}
		now := time.Now().UTC()
		if err := backfillPluginInstanceOwnershipTx(tx, now); err != nil {
			return err
		}
		if err := backfillPluginLifecycleProvenanceTx(tx); err != nil {
			return err
		}
		var sources []MarketplaceSourceRow
		if err := tx.Where("current_snapshot_id <> ?", "").Find(&sources).Error; err != nil {
			return err
		}
		bySnapshot := make(map[string]string, len(sources))
		currentIDs := make([]string, 0, len(sources))
		for _, source := range sources {
			bySnapshot[source.CurrentSnapshotID] = source.ID
			currentIDs = append(currentIDs, source.CurrentSnapshotID)
		}
		var entries []MarketEntryRow
		if len(currentIDs) > 0 {
			if err := tx.Where("snapshot_id IN ?", currentIDs).Find(&entries).Error; err != nil {
				return err
			}
		}
		if tx.Migrator().HasColumn("plugin_package_acquisitions", "operation_id") {
			var legacy []struct{ SourceID, Digest, OperationID, Status string }
			if err := tx.Table("plugin_package_acquisitions").Select("source_id, digest, operation_id, status").Where("status = ? AND operation_id <> ?", "staging", "").Scan(&legacy).Error; err != nil {
				return err
			}
			for _, row := range legacy {
				var count int64
				if err := tx.Model(&MarketplaceRefreshOperationRow{}).Where("id = ? AND source_id = ? AND status = ? AND lease_expires_at > ?", row.OperationID, row.SourceID, "running", now).Count(&count).Error; err != nil {
					return err
				}
				if count == 1 {
					staging := PluginPackageStagingRow{SourceID: row.SourceID, OperationID: row.OperationID, Digest: strings.ToLower(row.Digest), UpdatedAt: now}
					if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&staging).Error; err != nil {
						return err
					}
				} else if err := schedulePackageGCTx(tx, row.SourceID, row.Digest, now); err != nil {
					return err
				}
			}
		}
		if err := tx.Where("1 = 1").Delete(&PluginPackageAcquisitionRow{}).Error; err != nil {
			return err
		}
		for _, entry := range entries {
			sourceID := bySnapshot[entry.SnapshotID]
			if sourceID == "" {
				continue
			}
			row := PluginPackageAcquisitionRow{SourceID: sourceID, Digest: strings.ToLower(entry.PackageDigest), SnapshotID: entry.SnapshotID, Status: "catalog", UpdatedAt: now}
			if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "source_id"}, {Name: "digest"}}, DoNothing: true}).Create(&row).Error; err != nil {
				return err
			}
		}
		return recomputeCountQuotaUsageTx(tx, now)
	})
}

func backfillPluginInstanceOwnershipTx(tx *gorm.DB, now time.Time) error {
	var instances []PluginInstanceRow
	if err := tx.Order("id").Find(&instances).Error; err != nil {
		return err
	}
	for _, instance := range instances {
		groupID := strings.TrimSpace(instance.ResourceGroupID)
		if groupID == "" {
			return fmt.Errorf("plugin instance %s has no resource group", instance.ID)
		}
		var group ResourceGroupRow
		if err := tx.Where("id = ?", groupID).First(&group).Error; err != nil {
			return fmt.Errorf("plugin instance %s resource group %s is unavailable", instance.ID, groupID)
		}
		targets, err := pluginInstanceTargets(instance.TargetJSON)
		if err != nil {
			return fmt.Errorf("plugin instance %s targets: %w", instance.ID, err)
		}
		for _, target := range targets {
			var targetBinding ResourceBindingRow
			if err := tx.Where("resource_kind = ? AND resource_id = ?", "agent", target).First(&targetBinding).Error; err != nil || targetBinding.ResourceGroupID != groupID {
				return fmt.Errorf("plugin instance %s target %s is outside resource group %s", instance.ID, target, groupID)
			}
		}
		var existing ResourceBindingRow
		err = tx.Where("resource_kind = ? AND resource_id = ?", "plugin_instance", instance.ID).First(&existing).Error
		if err == nil && existing.ResourceGroupID != groupID {
			return fmt.Errorf("plugin instance %s binding conflicts with resource group %s", instance.ID, groupID)
		}
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		binding := ResourceBindingRow{ID: pluginStorageID("binding"), ResourceKind: "plugin_instance", ResourceID: instance.ID, ResourceGroupID: groupID, UpdatedAt: now}
		if err == nil {
			binding.ID = existing.ID
		}
		if len(targets) == 1 {
			binding.ParentResourceKind, binding.ParentResourceID = "agent", targets[0]
		}
		if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "resource_kind"}, {Name: "resource_id"}}, DoUpdates: clause.AssignmentColumns([]string{"resource_group_id", "parent_resource_kind", "parent_resource_id", "updated_at"})}).Create(&binding).Error; err != nil {
			return err
		}
		scope := quotaScope{SubjectKind: "resource_group", SubjectID: groupID, ResourceGroupID: groupID}
		allocation := QuotaAllocationRow{ID: quotaAllocationID("plugin_instance", instance.ID, "application_count", scope), ResourceKind: "plugin_instance", ResourceID: instance.ID, Metric: "application_count", SubjectKind: scope.SubjectKind, SubjectID: scope.SubjectID, ResourceGroupID: scope.ResourceGroupID, Amount: 1, CreatedAt: now}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&allocation).Error; err != nil {
			return err
		}
	}
	var policies []QuotaPolicyRow
	if err := tx.Where("metric = ? AND resource_group_id <> ?", "application_count", "").Find(&policies).Error; err != nil {
		return err
	}
	for _, policy := range policies {
		if policy.SubjectKind != "resource_group" || policy.SubjectID != policy.ResourceGroupID {
			continue
		}
		var current int64
		if err := tx.Model(&QuotaAllocationRow{}).Where("metric = ? AND subject_kind = ? AND subject_id = ? AND resource_group_id = ?", "application_count", "resource_group", policy.SubjectID, policy.ResourceGroupID).Select("COALESCE(SUM(amount), 0)").Scan(&current).Error; err != nil {
			return err
		}
		if current > policy.Limit {
			return fmt.Errorf("plugin instance application_count %d exceeds resource group %s limit %d", current, policy.ResourceGroupID, policy.Limit)
		}
	}
	return nil
}

type pluginSourceProvenance struct {
	ID, Kind, Risk string
}

func backfillPluginLifecycleProvenanceTx(tx *gorm.DB) error {
	var installed []InstalledPluginRow
	if err := tx.Find(&installed).Error; err != nil {
		return err
	}
	for index := range installed {
		changed := false
		for _, slot := range []struct {
			digest      string
			preferredID string
			id          *string
			kind        *string
			risk        *string
		}{
			{installed[index].ActivePackageDigest, installed[index].LastOperationID, &installed[index].ActiveSourceID, &installed[index].ActiveSourceKind, &installed[index].ActiveSourceRiskLabel},
			{installed[index].StagedPackageDigest, installed[index].PendingOperationID, &installed[index].StagedSourceID, &installed[index].StagedSourceKind, &installed[index].StagedSourceRiskLabel},
			{installed[index].RollbackPackageDigest, "", &installed[index].RollbackSourceID, &installed[index].RollbackSourceKind, &installed[index].RollbackSourceRiskLabel},
		} {
			if slot.digest == "" || (*slot.id != "" && *slot.kind != "") {
				continue
			}
			provenance, err := resolvePluginProvenanceTx(tx, installed[index].PluginID, slot.digest, slot.preferredID)
			if err != nil {
				return err
			}
			*slot.id, *slot.kind, *slot.risk = provenance.ID, provenance.Kind, provenance.Risk
			changed = true
		}
		if changed {
			if err := tx.Model(&InstalledPluginRow{}).Where("plugin_id = ?", installed[index].PluginID).Updates(map[string]any{
				"active_source_id": installed[index].ActiveSourceID, "active_source_kind": installed[index].ActiveSourceKind, "active_source_risk_label": installed[index].ActiveSourceRiskLabel,
				"staged_source_id": installed[index].StagedSourceID, "staged_source_kind": installed[index].StagedSourceKind, "staged_source_risk_label": installed[index].StagedSourceRiskLabel,
				"rollback_source_id": installed[index].RollbackSourceID, "rollback_source_kind": installed[index].RollbackSourceKind, "rollback_source_risk_label": installed[index].RollbackSourceRiskLabel,
			}).Error; err != nil {
				return err
			}
		}
	}
	return nil
}

func resolvePluginProvenanceTx(tx *gorm.DB, pluginID, digest, preferredOperationID string) (pluginSourceProvenance, error) {
	if preferredOperationID != "" {
		var operation PluginOperationRow
		if err := tx.Where("id = ? AND plugin_id = ? AND target_package_digest = ?", preferredOperationID, pluginID, digest).First(&operation).Error; err == nil && operation.SourceID != "" && operation.SourceKind != "" {
			return pluginSourceProvenance{operation.SourceID, operation.SourceKind, operation.SourceRiskLabel}, nil
		}
	}
	candidates := map[pluginSourceProvenance]struct{}{}
	var operations []PluginOperationRow
	if err := tx.Where("plugin_id = ? AND target_package_digest = ? AND source_id <> ? AND source_kind <> ?", pluginID, digest, "", "").Find(&operations).Error; err != nil {
		return pluginSourceProvenance{}, err
	}
	for _, operation := range operations {
		candidates[pluginSourceProvenance{operation.SourceID, operation.SourceKind, operation.SourceRiskLabel}] = struct{}{}
	}
	if tx.Migrator().HasColumn("plugin_packages", "source_id") {
		var legacy struct{ SourceID, SourceKind, SourceRiskLabel string }
		if err := tx.Table("plugin_packages").Select("source_id, source_kind, source_risk_label").Where("digest = ?", digest).Scan(&legacy).Error; err != nil {
			return pluginSourceProvenance{}, err
		}
		if legacy.SourceID != "" && legacy.SourceKind != "" {
			candidates[pluginSourceProvenance{legacy.SourceID, legacy.SourceKind, legacy.SourceRiskLabel}] = struct{}{}
		}
	}
	if len(candidates) == 1 {
		for candidate := range candidates {
			return candidate, nil
		}
	}
	return pluginSourceProvenance{ID: "unknown", Kind: "unknown", Risk: marketplace.UntrustedRiskLabel}, nil
}

func pluginInstanceTargets(raw string) ([]string, error) {
	var targets []string
	if strings.TrimSpace(raw) != "" && strings.TrimSpace(raw) != "null" {
		if err := json.Unmarshal([]byte(raw), &targets); err != nil {
			return nil, err
		}
	}
	if len(targets) == 0 {
		targets = []string{"local"}
	}
	for index := range targets {
		targets[index] = strings.TrimSpace(targets[index])
		if targets[index] == "" {
			return nil, errors.New("empty target")
		}
	}
	sort.Strings(targets)
	return targets, nil
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

func backfillMarketplaceDirectoryCleanup(ctx context.Context, db *gorm.DB) error {
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var deletions []MarketplaceSourceDeletionRow
		if err := tx.Find(&deletions).Error; err != nil {
			return err
		}
		now := time.Now().UTC()
		for _, deletion := range deletions {
			var paths []string
			if strings.TrimSpace(deletion.SnapshotPathsJSON) != "" && deletion.SnapshotPathsJSON != "[]" {
				if err := json.Unmarshal([]byte(deletion.SnapshotPathsJSON), &paths); err != nil {
					return err
				}
			}
			for _, candidate := range paths {
				candidate = filepath.ToSlash(filepath.Clean(filepath.FromSlash(candidate)))
				if candidate == "." || candidate == ".." || strings.HasPrefix(candidate, "../") || filepath.IsAbs(candidate) {
					return errors.New("invalid marketplace snapshot cleanup path")
				}
				row := MarketplaceDirectoryCleanupRow{ID: pluginStorageID("dirgc"), SourceID: deletion.SourceID, Path: path.Join("snapshots", candidate), UpdatedAt: now}
				if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error; err != nil {
					return err
				}
			}
			if len(paths) > 0 {
				if err := tx.Model(&MarketplaceSourceDeletionRow{}).Where("source_id = ?", deletion.SourceID).Update("snapshot_paths_json", "[]").Error; err != nil {
					return err
				}
			}
		}
		return nil
	})
}

func (s *GormStore) RegisterMarketplaceDirectoryCleanup(ctx context.Context, sourceID, operationID string, candidates []string) error {
	return s.writeTransaction(ctx, func(tx *gorm.DB) error {
		now := time.Now().UTC()
		for _, candidate := range candidates {
			relative, err := relativeMarketplaceDirectoryPath(s.dataRoot, candidate)
			if err != nil {
				return err
			}
			row := MarketplaceDirectoryCleanupRow{ID: pluginStorageID("dirgc"), SourceID: sourceID, OperationID: operationID, Path: relative, UpdatedAt: now}
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *GormStore) ListMarketplaceDirectoryCleanup(ctx context.Context) ([]marketplace.DirectoryCleanupWork, error) {
	var rows []MarketplaceDirectoryCleanupRow
	if err := s.db.WithContext(ctx).Order("source_id, path").Find(&rows).Error; err != nil {
		return nil, err
	}
	result := make([]marketplace.DirectoryCleanupWork, 0, len(rows))
	for _, row := range rows {
		result = append(result, marketplace.DirectoryCleanupWork{ID: row.ID, SourceID: row.SourceID, Path: row.Path})
	}
	return result, nil
}

func (s *GormStore) CompleteMarketplaceDirectoryCleanup(ctx context.Context, work marketplace.DirectoryCleanupWork, failure string) error {
	if strings.TrimSpace(work.ID) == "" {
		return errors.New("marketplace directory cleanup identity is required")
	}
	if failure != "" {
		return s.db.WithContext(ctx).Model(&MarketplaceDirectoryCleanupRow{}).Where("id = ?", work.ID).Updates(map[string]any{"last_error": failure, "updated_at": time.Now().UTC()}).Error
	}
	return s.db.WithContext(ctx).Where("id = ?", work.ID).Delete(&MarketplaceDirectoryCleanupRow{}).Error
}

func (s *GormStore) ListMarketplaceSourceDeletions(ctx context.Context) ([]string, error) {
	var sourceIDs []string
	if err := s.db.WithContext(ctx).Model(&MarketplaceSourceDeletionRow{}).Order("source_id").Pluck("source_id", &sourceIDs).Error; err != nil {
		return nil, err
	}
	return sourceIDs, nil
}

func relativeMarketplaceDirectoryPath(dataRoot, candidate string) (string, error) {
	root, err := filepath.Abs(filepath.Join(dataRoot, "marketplace"))
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(root, filepath.FromSlash(candidate))
	}
	candidate, err = filepath.Abs(candidate)
	if err != nil {
		return "", err
	}
	relative, err := filepath.Rel(root, candidate)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", errors.New("marketplace cleanup path is outside the managed root")
	}
	return filepath.ToSlash(relative), nil
}

func relativeMarketplaceSnapshotDirectoryPath(dataRoot, candidate string) (string, error) {
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(dataRoot, "marketplace", "snapshots", filepath.FromSlash(candidate))
	}
	return relativeMarketplaceDirectoryPath(dataRoot, candidate)
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
			relative, err := relativeMarketplaceSnapshotDirectoryPath(s.dataRoot, snapshot.Path)
			if err != nil {
				return err
			}
			deletion.SnapshotPaths = append(deletion.SnapshotPaths, relative)
			work := MarketplaceDirectoryCleanupRow{ID: pluginStorageID("dirgc"), SourceID: sourceID, Path: relative, UpdatedAt: time.Now().UTC()}
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&work).Error; err != nil {
				return err
			}
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
		var staging []PluginPackageStagingRow
		if err := tx.Where("source_id = ?", sourceID).Find(&staging).Error; err != nil {
			return err
		}
		for _, acquisition := range staging {
			seenDigests[strings.ToLower(acquisition.Digest)] = struct{}{}
		}
		now := time.Now().UTC()
		for digest := range seenDigests {
			if _, seen := seenDigests[digest]; seen {
				deletion.CacheDigests = append(deletion.CacheDigests, digest)
			}
			if err := schedulePackageGCTx(tx, sourceID, digest, now); err != nil {
				return err
			}
		}
		if err := tx.Where("source_id = ?", sourceID).Delete(&PluginPackageAcquisitionRow{}).Error; err != nil {
			return err
		}
		if err := tx.Where("source_id = ?", sourceID).Delete(&PluginPackageStagingRow{}).Error; err != nil {
			return err
		}
		if err := tx.Create(&MarketplaceSourceDeletionRow{SourceID: sourceID, SnapshotPathsJSON: "[]", UpdatedAt: now}).Error; err != nil {
			return err
		}
		actor, _ := QuotaActorFromContext(ctx)
		metadata, _ := json.Marshal(map[string]any{"kind": source.Kind, "risk_label": source.RiskLabel, "has_credential_ref": source.CredentialRef != ""})
		return tx.Create(&AuditEventRow{ID: pluginStorageID("audit"), ActorID: actor.UserID, SessionID: actor.SessionID, Action: "marketplace.source.delete", TargetKind: "marketplace_source", TargetID: source.ID, CorrelationID: actor.CorrelationID, Result: "accepted", MetadataJSON: string(metadata), CreatedAt: now}).Error
	})
	return deletion, err
}

func (s *GormStore) ClaimPackageGC(ctx context.Context, sourceID, digest string) (marketplace.PackageGCClaim, bool, error) {
	digest = strings.ToLower(digest)
	if !marketplace.IsDigest(digest) {
		return marketplace.PackageGCClaim{}, false, errors.New("invalid package digest")
	}
	var claim marketplace.PackageGCClaim
	claimed := false
	err := s.writeTransaction(ctx, func(tx *gorm.DB) error {
		now := time.Now().UTC()
		fence, err := lockPackageDigestFenceTx(tx, digest, now)
		if err != nil {
			return err
		}
		if fence.ClaimToken != "" {
			if fence.ClaimExpiresAt.After(now) {
				return nil
			}
			if err := tx.Model(&PluginCacheGCIntentRow{}).Where("digest = ? AND claim_token = ?", digest, fence.ClaimToken).Updates(map[string]any{"status": "pending", "claim_token": "", "claim_expires_at": time.Time{}, "last_error": "package GC claim expired", "updated_at": now}).Error; err != nil {
				return err
			}
			if err := tx.Model(&PluginDigestFenceRow{}).Where("digest = ? AND claim_token = ?", digest, fence.ClaimToken).Updates(map[string]any{"claim_token": "", "claim_expires_at": time.Time{}, "updated_at": now}).Error; err != nil {
				return err
			}
		}
		var intent PluginCacheGCIntentRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("source_id = ? AND digest = ?", sourceID, digest).First(&intent).Error; err != nil {
			if err == gorm.ErrRecordNotFound {
				return nil
			}
			return err
		}
		var catalog, lifecycle, staging int64
		if err := tx.Model(&PluginPackageAcquisitionRow{}).Where("digest = ?", digest).Count(&catalog).Error; err != nil {
			return err
		}
		if err := tx.Model(&InstalledPluginRow{}).Where("active_package_digest = ? OR staged_package_digest = ? OR rollback_package_digest = ?", digest, digest, digest).Count(&lifecycle).Error; err != nil {
			return err
		}
		if err := tx.Model(&PluginPackageStagingRow{}).Where("digest = ?", digest).Count(&staging).Error; err != nil {
			return err
		}
		if catalog+lifecycle+staging > 0 {
			return tx.Model(&PluginCacheGCIntentRow{}).Where("source_id = ? AND digest = ?", sourceID, digest).Updates(map[string]any{"status": "pending", "deferred": true, "claim_token": "", "claim_expires_at": time.Time{}, "updated_at": now}).Error
		}
		token := pluginStorageID("gc")
		expires := now.Add(5 * time.Minute)
		result := tx.Model(&PluginCacheGCIntentRow{}).Where("source_id = ? AND digest = ?", sourceID, digest).Updates(map[string]any{"status": "deleting", "deferred": false, "claim_token": token, "claim_expires_at": expires, "last_error": "", "updated_at": now})
		if result.Error != nil {
			return result.Error
		}
		claimed = result.RowsAffected == 1
		if claimed {
			if err := tx.Model(&PluginDigestFenceRow{}).Where("digest = ?", digest).Updates(map[string]any{"claim_token": token, "claim_expires_at": expires, "updated_at": now}).Error; err != nil {
				return err
			}
			claim = marketplace.PackageGCClaim{SourceID: sourceID, Digest: digest, Token: token, QuarantinePath: intent.QuarantinePath}
		}
		return nil
	})
	return claim, claimed, err
}

func (s *GormStore) PreparePackageGCQuarantine(ctx context.Context, claim marketplace.PackageGCClaim, quarantinePath string) error {
	digest := strings.ToLower(claim.Digest)
	if !marketplace.IsDigest(digest) || claim.Token == "" || strings.TrimSpace(quarantinePath) == "" {
		return errors.New("valid package GC quarantine is required")
	}
	return s.writeTransaction(ctx, func(tx *gorm.DB) error {
		now := time.Now().UTC()
		expires := now.Add(5 * time.Minute)
		result := tx.Model(&PluginCacheGCIntentRow{}).Where("source_id = ? AND digest = ? AND claim_token = ? AND status = ?", claim.SourceID, digest, claim.Token, "deleting").Updates(map[string]any{"quarantine_path": quarantinePath, "claim_expires_at": expires, "updated_at": now})
		if result.Error != nil || result.RowsAffected != 1 {
			return errors.New("package GC claim is stale")
		}
		result = tx.Model(&PluginDigestFenceRow{}).Where("digest = ? AND claim_token = ?", digest, claim.Token).Updates(map[string]any{"claim_expires_at": expires, "updated_at": now})
		if result.Error != nil || result.RowsAffected != 1 {
			return errors.New("package GC claim is stale")
		}
		return nil
	})
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

func (s *GormStore) RecordPackageGCFailure(ctx context.Context, sourceID, digest, failure string) error {
	return s.db.WithContext(ctx).Model(&PluginCacheGCIntentRow{}).Where("source_id = ? AND digest = ?", sourceID, digest).Updates(map[string]any{"status": "pending", "last_error": failure, "updated_at": time.Now().UTC()}).Error
}

func (s *GormStore) CompletePackageGC(ctx context.Context, claim marketplace.PackageGCClaim, failure string) error {
	digest := strings.ToLower(claim.Digest)
	if !marketplace.IsDigest(digest) || claim.Token == "" {
		return errors.New("valid package GC claim is required")
	}
	return s.writeTransaction(ctx, func(tx *gorm.DB) error {
		now := time.Now().UTC()
		var fence PluginDigestFenceRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("digest = ? AND claim_token = ?", digest, claim.Token).First(&fence).Error; err != nil {
			return errors.New("package GC claim is stale")
		}
		if failure != "" {
			result := tx.Model(&PluginCacheGCIntentRow{}).Where("source_id = ? AND digest = ? AND claim_token = ?", claim.SourceID, digest, claim.Token).Updates(map[string]any{"status": "pending", "claim_token": "", "claim_expires_at": time.Time{}, "last_error": failure, "updated_at": now})
			if result.Error != nil || result.RowsAffected != 1 {
				return errors.New("package GC claim is stale")
			}
			return tx.Model(&PluginDigestFenceRow{}).Where("digest = ? AND claim_token = ?", digest, claim.Token).Updates(map[string]any{"claim_token": "", "claim_expires_at": time.Time{}, "updated_at": now}).Error
		}
		result := tx.Where("source_id = ? AND digest = ? AND status = ? AND claim_token = ?", claim.SourceID, digest, "deleting", claim.Token).Delete(&PluginCacheGCIntentRow{})
		if result.Error != nil || result.RowsAffected != 1 {
			return errors.New("package GC claim is stale")
		}
		if err := tx.Where("digest = ?", digest).Delete(&PluginPackageRow{}).Error; err != nil {
			return err
		}
		return tx.Model(&PluginDigestFenceRow{}).Where("digest = ? AND claim_token = ?", digest, claim.Token).Updates(map[string]any{"claim_token": "", "claim_expires_at": time.Time{}, "updated_at": now}).Error
	})
}

func (s *GormStore) CompleteMarketplaceSourceDeletion(ctx context.Context, sourceID string, failure string) error {
	return s.writeTransaction(ctx, func(tx *gorm.DB) error {
		if failure != "" {
			return tx.Model(&MarketplaceSourceDeletionRow{}).Where("source_id = ?", sourceID).Updates(map[string]any{"last_error": failure, "updated_at": time.Now().UTC()}).Error
		}
		var pending int64
		if err := tx.Model(&PluginCacheGCIntentRow{}).Where("source_id = ? AND deferred = ?", sourceID, false).Count(&pending).Error; err != nil {
			return err
		}
		if pending != 0 {
			return errors.New("marketplace source cache cleanup is still pending")
		}
		var directories int64
		if err := tx.Model(&MarketplaceDirectoryCleanupRow{}).Where("source_id = ?", sourceID).Count(&directories).Error; err != nil {
			return err
		}
		if directories != 0 {
			return errors.New("marketplace source directory cleanup is still pending")
		}
		if err := tx.Where("source_id = ?", sourceID).Delete(&MarketplaceSourceDeletionRow{}).Error; err != nil {
			return err
		}
		return tx.Where("id = ? AND deleting = ?", sourceID, true).Delete(&MarketplaceSourceRow{}).Error
	})
}

func lockPackageDigestFenceTx(tx *gorm.DB, digest string, now time.Time) (PluginDigestFenceRow, error) {
	row := PluginDigestFenceRow{Digest: digest, UpdatedAt: now}
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error; err != nil {
		return PluginDigestFenceRow{}, err
	}
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("digest = ?", digest).First(&row).Error; err != nil {
		return PluginDigestFenceRow{}, err
	}
	return row, nil
}

func schedulePackageGCTx(tx *gorm.DB, sourceID, digest string, now time.Time) error {
	digest = strings.ToLower(strings.TrimSpace(digest))
	if !marketplace.IsDigest(digest) {
		return errors.New("invalid package digest")
	}
	if _, err := lockPackageDigestFenceTx(tx, digest, now); err != nil {
		return err
	}
	intent := PluginCacheGCIntentRow{SourceID: sourceID, Digest: digest, Status: "pending", UpdatedAt: now}
	return tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "source_id"}, {Name: "digest"}}, DoNothing: true}).Create(&intent).Error
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
			var abandoned []PluginPackageStagingRow
			if err := tx.Where("source_id = ? AND operation_id = ?", previous.SourceID, previous.ID).Find(&abandoned).Error; err != nil {
				return err
			}
			for _, row := range abandoned {
				if err := schedulePackageGCTx(tx, previous.SourceID, row.Digest, operation.StartedAt); err != nil {
					return err
				}
			}
			if err := tx.Where("source_id = ? AND operation_id = ?", previous.SourceID, previous.ID).Delete(&PluginPackageStagingRow{}).Error; err != nil {
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
	if !marketplace.IsDigest(digest) || strings.TrimSpace(operationID) == "" {
		return errors.New("valid package digest and refresh operation are required")
	}
	return s.writeTransaction(ctx, func(tx *gorm.DB) error {
		var source MarketplaceSourceRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND deleting = ?", sourceID, false).First(&source).Error; err != nil {
			return err
		}
		now := time.Now().UTC()
		fence, err := lockPackageDigestFenceTx(tx, digest, now)
		if err != nil {
			return err
		}
		if fence.ClaimToken != "" {
			return errors.New("package cache digest is being deleted")
		}
		var operation MarketplaceRefreshOperationRow
		if err := tx.Where("id = ? AND source_id = ? AND status = ? AND lease_expires_at > ?", operationID, sourceID, "running", now).First(&operation).Error; err != nil {
			return errors.New("refresh operation is unavailable or expired")
		}
		row := PluginPackageStagingRow{SourceID: sourceID, Digest: digest, OperationID: operationID, UpdatedAt: now}
		return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error
	})
}

func (s *GormStore) CompletePackageAcquisitions(ctx context.Context, sourceID, operationID string, succeeded bool) error {
	return s.writeTransaction(ctx, func(tx *gorm.DB) error {
		var rows []PluginPackageStagingRow
		if err := tx.Where("source_id = ? AND operation_id = ?", sourceID, operationID).Find(&rows).Error; err != nil {
			return err
		}
		now := time.Now().UTC()
		if succeeded {
			return errors.New("successful package acquisition finalization belongs to snapshot promotion")
		}
		for _, row := range rows {
			if err := schedulePackageGCTx(tx, sourceID, row.Digest, now); err != nil {
				return err
			}
		}
		return tx.Where("source_id = ? AND operation_id = ?", sourceID, operationID).Delete(&PluginPackageStagingRow{}).Error
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
		now := time.Now().UTC()
		sourceResult := tx.Model(&MarketplaceSourceRow{}).Where("id = ? AND deleting = ? AND refresh_lease_token = ? AND refresh_lease_expires_at >= ?", source.ID, false, operation.LeaseToken, now).Updates(map[string]any{"current_snapshot_id": snapshot.ID, "last_result": "succeeded", "last_error": "", "updated_at": snapshot.ValidatedAt, "last_completed_at": *operation.FinishedAt, "refresh_lease_token": "", "refresh_lease_expires_at": time.Time{}})
		if sourceResult.Error != nil {
			return sourceResult.Error
		}
		if sourceResult.RowsAffected != 1 {
			return errors.New("marketplace source was deleted or refresh lease expired")
		}
		var retired []MarketSnapshotRow
		if err := tx.Where("source_id = ? AND id <> ?", source.ID, snapshot.ID).Find(&retired).Error; err != nil {
			return err
		}
		snapshotRow := MarketSnapshotRow{ID: snapshot.ID, SourceID: snapshot.SourceID, Commit: snapshot.Commit, Path: snapshot.Path, EntriesJSON: string(entriesJSON), ValidatedAt: snapshot.ValidatedAt}
		if err := tx.Create(&snapshotRow).Error; err != nil {
			return err
		}
		currentRelative, err := relativeMarketplaceSnapshotDirectoryPath(s.dataRoot, snapshot.Path)
		if err != nil {
			return err
		}
		if err := tx.Where("operation_id = ? AND path = ?", operation.ID, currentRelative).Delete(&MarketplaceDirectoryCleanupRow{}).Error; err != nil {
			return err
		}
		for _, previous := range retired {
			relative, err := relativeMarketplaceSnapshotDirectoryPath(s.dataRoot, previous.Path)
			if err != nil {
				return err
			}
			if relative != currentRelative {
				work := MarketplaceDirectoryCleanupRow{ID: pluginStorageID("dirgc"), SourceID: source.ID, OperationID: operation.ID, Path: relative, UpdatedAt: snapshot.ValidatedAt}
				if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&work).Error; err != nil {
					return err
				}
			}
			if err := tx.Where("snapshot_id = ?", previous.ID).Delete(&MarketEntryRow{}).Error; err != nil {
				return err
			}
			if err := tx.Where("id = ?", previous.ID).Delete(&MarketSnapshotRow{}).Error; err != nil {
				return err
			}
		}
		if err := tx.Where("source_id = ?", source.ID).Delete(&PluginPackageAcquisitionRow{}).Error; err != nil {
			return err
		}
		for _, entry := range snapshot.Entries {
			capabilities, _ := json.Marshal(entry.Capabilities)
			compatibility, _ := json.Marshal(entry.Compatibility)
			row := MarketEntryRow{ID: pluginStorageID("entry"), SnapshotID: snapshot.ID, PluginID: entry.ID, Version: entry.Version, Description: entry.Description, CapabilitiesJSON: string(capabilities), CompatibilityJSON: string(compatibility), PackagePath: entry.PackagePath, PackageDigest: strings.ToLower(entry.PackageSHA256), Official: entry.Official}
			if err := tx.Create(&row).Error; err != nil {
				return err
			}
			acquisition := PluginPackageAcquisitionRow{SourceID: source.ID, Digest: row.PackageDigest, SnapshotID: snapshot.ID, Status: "catalog", UpdatedAt: snapshot.ValidatedAt}
			if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "source_id"}, {Name: "digest"}}, DoUpdates: clause.AssignmentColumns([]string{"snapshot_id", "status", "updated_at"})}).Create(&acquisition).Error; err != nil {
				return err
			}
		}
		if err := tx.Where("source_id = ? AND operation_id = ?", source.ID, operation.ID).Delete(&PluginPackageStagingRow{}).Error; err != nil {
			return err
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
					if err := tx.Where("resource_kind = ? AND resource_id IN ?", "plugin_instance", instanceIDs).Delete(&QuotaAllocationRow{}).Error; err != nil {
						return err
					}
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
				if err := s.replacePluginInstanceTx(ctx, tx, mutation.PluginID, mutation.ReplaceInstance, mutation.ValidateInstanceScope, mutation.PromoteInstanceBinding); err != nil {
					return err
				}
			}
			if len(mutation.ReplaceInstances) > 0 {
				for index := range mutation.ReplaceInstances {
					if err := s.replacePluginInstanceTx(ctx, tx, mutation.PluginID, &mutation.ReplaceInstances[index], false, false); err != nil {
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

func (s *GormStore) replacePluginInstanceTx(ctx context.Context, tx *gorm.DB, pluginID string, instance *PluginInstanceRow, validateScope, promote bool) error {
	if instance == nil || instance.PluginID != pluginID {
		return errors.New("plugin instance identity differs from mutation target")
	}
	var current PluginInstanceRow
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", instance.ID).First(&current).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	if err == nil {
		if current.PluginID != pluginID || instance.StateVersion != current.StateVersion {
			return errors.New("plugin instance changed concurrently")
		}
	} else if instance.StateVersion != 0 {
		return errors.New("plugin instance changed concurrently")
	}
	if validateScope {
		if err := s.validatePluginInstanceScopeTx(ctx, tx, *instance, promote); err != nil {
			return err
		}
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		instance.StateVersion = 1
		return tx.Create(instance).Error
	}
	next := *instance
	next.StateVersion = current.StateVersion + 1
	result := tx.Model(&PluginInstanceRow{}).Where("id = ? AND state_version = ?", instance.ID, current.StateVersion).Select("*").Omit("id").Updates(&next)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return errors.New("plugin instance changed concurrently")
	}
	instance.StateVersion = next.StateVersion
	return nil
}

func validatePackageAcquisitionTx(tx *gorm.DB, sourceID, digest string) error {
	digest = strings.ToLower(strings.TrimSpace(digest))
	if !marketplace.IsDigest(digest) {
		return errors.New("verified package digest is invalid")
	}
	var source MarketplaceSourceRow
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND deleting = ?", sourceID, false).First(&source).Error; err != nil {
		return errors.New("marketplace package source is unavailable or deleting")
	}
	now := time.Now().UTC()
	fence, err := lockPackageDigestFenceTx(tx, digest, now)
	if err != nil {
		return err
	}
	if fence.ClaimToken != "" {
		return errors.New("verified package cache is being deleted")
	}
	var acquisition PluginPackageAcquisitionRow
	if err := tx.Where("source_id = ? AND digest = ? AND snapshot_id = ? AND status = ?", sourceID, digest, source.CurrentSnapshotID, "catalog").First(&acquisition).Error; err != nil {
		return errors.New("verified package acquisition is unavailable")
	}
	return nil
}

func (s *GormStore) validatePluginInstanceScopeTx(ctx context.Context, tx *gorm.DB, instance PluginInstanceRow, promote bool) error {
	groupID := instance.PendingResourceGroupID
	targetJSON := instance.PendingTargetJSON
	if promote || groupID == "" {
		groupID = instance.ResourceGroupID
		targetJSON = instance.TargetJSON
	}
	if strings.TrimSpace(groupID) == "" {
		return fmt.Errorf("%w: resource group is required", ErrPluginInstanceScope)
	}
	var group ResourceGroupRow
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", groupID).First(&group).Error; err != nil {
		return fmt.Errorf("%w: resource group does not exist", ErrPluginInstanceScope)
	}
	targets, err := pluginInstanceTargets(targetJSON)
	if err != nil {
		return fmt.Errorf("%w: targets are invalid", ErrPluginInstanceScope)
	}
	for _, target := range targets {
		var binding ResourceBindingRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("resource_kind = ? AND resource_id = ?", "agent", target).First(&binding).Error; err != nil || binding.ResourceGroupID != groupID {
			return fmt.Errorf("%w: target is outside the selected resource group", ErrPluginInstanceScope)
		}
	}
	if promote || instance.ConfigVersion == 0 {
		now := time.Now().UTC()
		var current ResourceBindingRow
		currentErr := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("resource_kind = ? AND resource_id = ?", "plugin_instance", instance.ID).First(&current).Error
		if currentErr != nil && !errors.Is(currentErr, gorm.ErrRecordNotFound) {
			return currentErr
		}
		needsAllocation := currentErr != nil || current.ResourceGroupID != groupID
		var allocations []QuotaAllocationRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("resource_kind = ? AND resource_id = ? AND metric = ?", "plugin_instance", instance.ID, "application_count").Find(&allocations).Error; err != nil {
			return err
		}
		if len(allocations) == 0 {
			needsAllocation = true
		}
		if needsAllocation {
			actor, ok := QuotaActorFromContext(ctx)
			if !ok {
				return ErrQuotaActorRequired
			}
			if err := tx.Where("resource_kind = ? AND resource_id = ? AND metric = ?", "plugin_instance", instance.ID, "application_count").Delete(&QuotaAllocationRow{}).Error; err != nil {
				return err
			}
			var scopes []quotaScope
			if actor.Bootstrap {
				scopes = []quotaScope{{SubjectKind: "resource_group", SubjectID: groupID, ResourceGroupID: groupID}}
			} else {
				var err error
				scopes, err = s.quotaScopesTx(tx, actor.UserID, groupID)
				if err != nil {
					return err
				}
			}
			if _, err := s.consumeQuotaScopesTx(tx, scopes, "application_count", 1, now, true, false); err != nil {
				return err
			}
			for _, scope := range scopes {
				allocation := QuotaAllocationRow{ID: quotaAllocationID("plugin_instance", instance.ID, "application_count", scope), ResourceKind: "plugin_instance", ResourceID: instance.ID, Metric: "application_count", SubjectKind: scope.SubjectKind, SubjectID: scope.SubjectID, ResourceGroupID: scope.ResourceGroupID, Amount: 1, CreatedAt: now}
				if err := tx.Create(&allocation).Error; err != nil {
					return err
				}
			}
		}
		binding := ResourceBindingRow{ID: pluginStorageID("binding"), ResourceKind: "plugin_instance", ResourceID: instance.ID, ResourceGroupID: groupID, UpdatedAt: now}
		if currentErr == nil {
			binding.ID = current.ID
		}
		if len(targets) == 1 {
			binding.ParentResourceKind, binding.ParentResourceID = "agent", targets[0]
		}
		if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "resource_kind"}, {Name: "resource_id"}}, DoUpdates: clause.AssignmentColumns([]string{"resource_group_id", "parent_resource_kind", "parent_resource_id", "updated_at"})}).Create(&binding).Error; err != nil {
			return err
		}
		return recomputeCountQuotaUsageTx(tx, now)
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
