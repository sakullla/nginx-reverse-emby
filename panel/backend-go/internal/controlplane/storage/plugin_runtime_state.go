package storage

import (
	"context"
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (s *GormStore) GetPluginRuntimeState(ctx context.Context, instanceID, key string) ([]byte, bool, error) {
	if s == nil || strings.TrimSpace(instanceID) == "" || strings.TrimSpace(key) == "" {
		return nil, false, errors.New("plugin runtime state identity is required")
	}
	var row PluginRuntimeStateRow
	err := s.db.WithContext(ctx).Where("instance_id = ? AND key = ?", instanceID, key).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return append([]byte(nil), row.Value...), true, nil
}

func (s *GormStore) PutPluginRuntimeState(ctx context.Context, row PluginRuntimeStateRow) error {
	if s == nil || strings.TrimSpace(row.InstanceID) == "" || strings.TrimSpace(row.Key) == "" || strings.TrimSpace(row.PluginID) == "" || strings.TrimSpace(row.ResourceGroupID) == "" || len(row.Value) > 1<<20 {
		return errors.New("plugin runtime state is invalid")
	}
	row.Value = append([]byte(nil), row.Value...)
	row.UpdatedAt = time.Now().UTC()
	return s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "instance_id"}, {Name: "key"}},
		DoUpdates: clause.AssignmentColumns([]string{"plugin_id", "resource_group_id", "value", "updated_at"}),
	}).Create(&row).Error
}
