package storage

import (
	"context"
	"errors"

	"gorm.io/gorm"
)

func (s *GormStore) ListAccessServices(ctx context.Context) ([]AccessServiceRow, error) {
	var rows []AccessServiceRow
	err := s.db.WithContext(ctx).Order("created_at, id").Find(&rows).Error
	return rows, err
}

func (s *GormStore) GetAccessService(ctx context.Context, id string) (AccessServiceRow, bool, error) {
	var row AccessServiceRow
	err := s.db.WithContext(ctx).Where("id = ?", id).First(&row).Error
	if err == nil {
		return row, true, nil
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return AccessServiceRow{}, false, nil
	}
	return AccessServiceRow{}, false, err
}

func (s *GormStore) SaveAccessService(ctx context.Context, row AccessServiceRow) error {
	return s.db.WithContext(ctx).Save(&row).Error
}

func (s *GormStore) DeleteAccessService(ctx context.Context, id string) error {
	return s.db.WithContext(ctx).Where("id = ?", id).Delete(&AccessServiceRow{}).Error
}

func (s *GormStore) GetAccessServiceOperation(ctx context.Context, key string) (AccessServiceOperationRow, bool, error) {
	var row AccessServiceOperationRow
	err := s.db.WithContext(ctx).Where("idempotency_key = ?", key).First(&row).Error
	if err == nil {
		return row, true, nil
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return AccessServiceOperationRow{}, false, nil
	}
	return AccessServiceOperationRow{}, false, err
}

func (s *GormStore) SaveAccessServiceOperation(ctx context.Context, row AccessServiceOperationRow) error {
	return s.db.WithContext(ctx).Save(&row).Error
}
