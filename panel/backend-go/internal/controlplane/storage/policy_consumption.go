package storage

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"time"

	sdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type PluginPolicySettingsRow struct {
	InstanceID  string `gorm:"primaryKey;size:64"`
	Revision    uint64 `gorm:"not null"`
	DefaultMode string `gorm:"size:16;not null"`
}
type PluginPolicyEntryModeRow struct {
	InstanceID string `gorm:"primaryKey;size:64"`
	NodeID     string `gorm:"primaryKey;size:64"`
	Kind       string `gorm:"primaryKey;size:32"`
	EntryID    string `gorm:"primaryKey;size:190"`
	Mode       string `gorm:"size:16;not null"`
}
type PluginDatasetConsumptionRow struct {
	InstanceID string `gorm:"primaryKey;size:64"`
	SourceID   string `gorm:"primaryKey;size:128"`
	Revision   uint64 `gorm:"not null"`
	RecordJSON string `gorm:"type:text;not null"`
}
type PluginConsumptionOperationRow struct {
	ID              string `gorm:"primaryKey;size:64"`
	Kind            string `gorm:"size:32;not null"`
	RequestDigest   string `gorm:"size:71;not null"`
	ResponseJSON    string `gorm:"type:text;not null"`
	TargetsJSON     string `gorm:"type:text;not null"`
	ResourceGroupID string `gorm:"size:64;not null"`
}

func initializeNewIPPolicySettings(tx *gorm.DB, instance PluginInstanceRow) error {
	if instance.PluginID != "ip-policy" {
		return nil
	}
	var installed InstalledPluginRow
	if err := tx.Where("plugin_id = ?", instance.PluginID).First(&installed).Error; err != nil {
		return err
	}
	identity := installed.ActivePackageIdentity
	if installed.StagedPackageIdentity != "" {
		identity = installed.StagedPackageIdentity
	}
	var pkg PluginPackageRow
	if err := tx.Where("identity = ?", identity).First(&pkg).Error; err != nil {
		return err
	}
	var manifest sdk.Manifest
	if json.Unmarshal([]byte(pkg.ManifestJSON), &manifest) != nil {
		return ErrPluginConflict
	}
	handling, err := sdk.PolicyModeHandlingForManifest(manifest)
	if err != nil {
		return err
	}
	if handling != sdk.PolicyModeHandlingRaw {
		return nil
	}
	return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&PluginPolicySettingsRow{InstanceID: instance.ID, Revision: 1, DefaultMode: string(sdk.PolicyModeObserve)}).Error
}

func (s *GormStore) GetPluginPolicySettings(ctx context.Context, id string) (PluginPolicySettingsRow, error) {
	row := PluginPolicySettingsRow{InstanceID: id}
	err := s.db.WithContext(ctx).Where("instance_id = ?", id).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return PluginPolicySettingsRow{InstanceID: id}, nil
	}
	return row, err
}
func (s *GormStore) PutPluginPolicySettings(ctx context.Context, row PluginPolicySettingsRow) error {
	return s.db.WithContext(ctx).Save(&row).Error
}
func (s *GormStore) ListPluginPolicyEntryModes(ctx context.Context, id string) ([]PluginPolicyEntryModeRow, error) {
	var rows []PluginPolicyEntryModeRow
	err := s.db.WithContext(ctx).Where("instance_id = ?", id).Find(&rows).Error
	return rows, err
}
func (s *GormStore) PutPluginPolicyEntryMode(ctx context.Context, row PluginPolicyEntryModeRow, remove bool) error {
	if remove {
		return s.db.WithContext(ctx).Where("instance_id = ? AND node_id = ? AND kind = ? AND entry_id = ?", row.InstanceID, row.NodeID, row.Kind, row.EntryID).Delete(&PluginPolicyEntryModeRow{}).Error
	}
	return s.db.WithContext(ctx).Save(&row).Error
}

func deletePluginPolicyEntryModesTx(tx *gorm.DB, nodeID, kind string, entryIDs []string) error {
	nodeID, kind = strings.TrimSpace(nodeID), strings.TrimSpace(kind)
	if nodeID == "" || kind == "" || len(entryIDs) == 0 {
		return nil
	}
	return tx.Where("node_id = ? AND kind = ? AND entry_id IN ?", nodeID, kind, entryIDs).Delete(&PluginPolicyEntryModeRow{}).Error
}

func deleteManagedPluginPolicyEntryModesTx(tx *gorm.DB, entryID string, nodeIDs []string) error {
	entryID = strings.TrimSpace(entryID)
	if entryID == "" {
		return nil
	}
	query := tx.Where("entry_id = ? AND kind IN ?", entryID, []string{sdk.PolicyEntryManagedTCP, sdk.PolicyEntryManagedUDP})
	if nodeIDs != nil && len(nodeIDs) == 0 {
		return nil
	}
	if nodeIDs != nil {
		query = query.Where("node_id IN ?", nodeIDs)
	}
	return query.Delete(&PluginPolicyEntryModeRow{}).Error
}
func (s *GormStore) GetPluginDatasetConsumption(ctx context.Context, instance, source string) (PluginDatasetConsumptionRow, error) {
	var row PluginDatasetConsumptionRow
	err := s.db.WithContext(ctx).Where("instance_id = ? AND source_id = ?", instance, source).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return PluginDatasetConsumptionRow{InstanceID: instance, SourceID: source}, nil
	}
	return row, err
}
func (s *GormStore) PutPluginDatasetConsumption(ctx context.Context, row PluginDatasetConsumptionRow) error {
	return s.db.WithContext(ctx).Save(&row).Error
}
func (s *GormStore) GetPluginConsumptionOperation(ctx context.Context, id string) (PluginConsumptionOperationRow, bool, error) {
	var row PluginConsumptionOperationRow
	err := s.db.WithContext(ctx).Where("id = ?", id).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return row, false, nil
	}
	return row, err == nil, err
}
func (s *GormStore) PutPluginConsumptionOperation(ctx context.Context, row PluginConsumptionOperationRow) error {
	return s.db.WithContext(ctx).Create(&row).Error
}

// The instance StateVersion is the shared Config/settings/data-consumption CAS.
// A mode-only update also advances ConfigVersion so every execution face sees
// an updated immutable definition. All bundle comparisons precede this write.
func (s *GormStore) UpdatePluginConsumptionInstance(ctx context.Context, id string, expected uint64, config json.RawMessage) error {
	if expected == 0 || expected == math.MaxUint64 {
		return ErrPluginConflict
	}
	var row PluginInstanceRow
	if err := s.db.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", id).First(&row).Error; err != nil {
		return err
	}
	if row.StateVersion != expected || row.ConfigVersion == math.MaxUint64 || row.PendingOperationID != "" {
		return ErrPluginConflict
	}
	updates := map[string]any{"state_version": expected + 1, "config_version": row.ConfigVersion + 1, "updated_at": time.Now().UTC()}
	if len(config) > 0 {
		updates["config_json"] = string(config)
	}
	result := s.db.WithContext(ctx).Model(&PluginInstanceRow{}).Where("id = ? AND state_version = ?", id, expected).Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrPluginConflict
	}
	return nil
}

func (s *GormStore) ListInstanceDatasetBindings(ctx context.Context, instance, source string) ([]DatasetBindingRow, error) {
	var rows []DatasetBindingRow
	err := s.db.WithContext(ctx).Where("instance_id = ? AND source_id = ?", instance, source).Order("agent_id").Find(&rows).Error
	return rows, err
}

func (s *GormStore) PolicySettingsSnapshot(ctx context.Context, instance PluginInstanceRow, handling sdk.PolicyModeHandling, entry *sdk.PolicyEntryTarget) (sdk.PolicySettingsSnapshot, error) {
	row, err := s.GetPluginPolicySettings(ctx, instance.ID)
	if err != nil {
		return sdk.PolicySettingsSnapshot{}, err
	}
	result := sdk.PolicySettingsSnapshot{Version: sdk.PolicySettingsVersion{Revision: row.Revision, InstanceVersion: instance.StateVersion}, Settings: sdk.PolicyModeSettings{Handling: sdk.PolicyModeHandlingLegacy}}
	if row.DefaultMode != "" {
		mode := sdk.PolicyMode(row.DefaultMode)
		result.Settings = sdk.PolicyModeSettings{Handling: handling, DefaultMode: &mode}
	}
	if entry != nil {
		rows, err := s.ListPluginPolicyEntryModes(ctx, instance.ID)
		if err != nil {
			return result, err
		}
		for _, override := range rows {
			if override.NodeID == entry.NodeID && override.Kind == entry.Kind && override.EntryID == entry.ID {
				mode := sdk.PolicyMode(override.Mode)
				result.Settings.EntryMode = &mode
				break
			}
		}
	}
	return result, result.Validate()
}

func (s *GormStore) ImmutableAgentSnapshot(ctx context.Context, agent string, revision int64) (Snapshot, AgentRevisionRow, error) {
	row, found, err := s.GetCoordinatorRevision(ctx, agent, revision)
	if err != nil || !found {
		return Snapshot{}, row, ErrCoordinatorNotFound
	}
	artifact, found, err := s.GetGenerationArtifact(ctx, row.SnapshotArtifactID)
	if err != nil || !found {
		return Snapshot{}, row, ErrCoordinatorNotFound
	}
	var snapshot Snapshot
	if artifact.Kind != "agent_snapshot" || artifact.SHA256 != row.SnapshotDigest || validateGenerationArtifact(artifact) != nil || json.Unmarshal(artifact.Payload, &snapshot) != nil || snapshot.Revision != revision {
		return Snapshot{}, row, errors.New("immutable Agent snapshot is invalid")
	}
	return snapshot, row, nil
}
