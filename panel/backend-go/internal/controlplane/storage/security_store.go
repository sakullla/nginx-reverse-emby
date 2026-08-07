package storage

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrQuotaExceeded = errors.New("quota exceeded")

func (s *GormStore) SecurityStoreAvailable() bool {
	return s != nil && s.db != nil && s.databaseLifecycle != nil
}

// SecurityTransaction lets the authz and vault layers couple their durable
// audit event to the protected mutation. A transaction-scoped store reuses the
// existing transaction so security helpers can be composed inside revision
// mutations without opening nested transactions.
func (s *GormStore) SecurityTransaction(ctx context.Context, fn func(*GormStore) error) error {
	if s == nil || fn == nil {
		return gorm.ErrInvalidDB
	}
	if s.transactionScoped {
		return fn(s)
	}
	return s.writeTransaction(ctx, func(tx *gorm.DB) error {
		txStore := *s
		txStore.db = tx
		txStore.writeDB = nil
		txStore.transactionScoped = true
		return fn(&txStore)
	})
}

type QuotaDecision struct {
	Metric            string     `json:"metric"`
	Current           int64      `json:"current"`
	Limit             int64      `json:"limit"`
	Allowed           bool       `json:"allowed"`
	ExceedAction      string     `json:"exceed_action"`
	RecoveryCondition string     `json:"recovery_condition"`
	ResetAt           *time.Time `json:"reset_at,omitempty"`
}

type QuotaActor struct {
	UserID        string
	SessionID     string
	CorrelationID string
}

type quotaActorContextKey struct{}

func WithQuotaActor(ctx context.Context, actor QuotaActor) context.Context {
	return context.WithValue(ctx, quotaActorContextKey{}, actor)
}

func QuotaActorFromContext(ctx context.Context) (QuotaActor, bool) {
	actor, ok := ctx.Value(quotaActorContextKey{}).(QuotaActor)
	return actor, ok && strings.TrimSpace(actor.UserID) != ""
}

func (s *GormStore) CreateUser(ctx context.Context, row UserRow) error {
	return s.writeTransaction(ctx, func(tx *gorm.DB) error { return tx.Create(&row).Error })
}

func (s *GormStore) CreateUserWithRoleBindings(ctx context.Context, row UserRow, bindings []RoleBindingRow) error {
	return s.writeTransaction(ctx, func(tx *gorm.DB) error {
		if err := tx.Create(&row).Error; err != nil {
			return err
		}
		for _, binding := range bindings {
			if binding.UserID != row.ID {
				return fmt.Errorf("role binding user %q does not match created user %q", binding.UserID, row.ID)
			}
			if err := tx.Create(&binding).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *GormStore) SaveUser(ctx context.Context, row UserRow) error {
	row.UpdatedAt = time.Now().UTC()
	return s.writeTransaction(ctx, func(tx *gorm.DB) error {
		result := tx.Model(&UserRow{}).Where("id = ?", row.ID).Updates(map[string]any{
			"username": row.Username, "display_name": row.DisplayName, "password_hash": row.PasswordHash,
			"disabled": row.Disabled, "auth_revision": row.AuthRevision, "updated_at": row.UpdatedAt,
		})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		return nil
	})
}

func (s *GormStore) GetUser(ctx context.Context, id string) (UserRow, error) {
	var row UserRow
	err := s.db.WithContext(ctx).Where("id = ?", id).First(&row).Error
	return row, err
}

func (s *GormStore) GetUserByUsername(ctx context.Context, username string) (UserRow, error) {
	var row UserRow
	err := s.db.WithContext(ctx).Where("username = ?", strings.ToLower(strings.TrimSpace(username))).First(&row).Error
	return row, err
}

func (s *GormStore) ListUsers(ctx context.Context) ([]UserRow, error) {
	var rows []UserRow
	err := s.db.WithContext(ctx).Order("username ASC").Find(&rows).Error
	return rows, err
}

func (s *GormStore) CreateSession(ctx context.Context, row SessionRow) error {
	return s.writeTransaction(ctx, func(tx *gorm.DB) error { return tx.Create(&row).Error })
}

func (s *GormStore) GetSessionByTokenHash(ctx context.Context, tokenHash string) (SessionRow, error) {
	var row SessionRow
	err := s.db.WithContext(ctx).Where("token_hash = ?", tokenHash).First(&row).Error
	return row, err
}

func (s *GormStore) TouchSession(ctx context.Context, id string, at time.Time) error {
	return s.writeTransaction(ctx, func(tx *gorm.DB) error {
		return tx.Model(&SessionRow{}).Where("id = ? AND revoked_at IS NULL", id).Update("last_seen", at).Error
	})
}

func (s *GormStore) RevokeSession(ctx context.Context, id string, at time.Time) error {
	return s.writeTransaction(ctx, func(tx *gorm.DB) error {
		return tx.Model(&SessionRow{}).Where("id = ? AND revoked_at IS NULL", id).Update("revoked_at", at).Error
	})
}

func (s *GormStore) RevokeUserSessions(ctx context.Context, userID string, at time.Time) error {
	return s.writeTransaction(ctx, func(tx *gorm.DB) error {
		return tx.Model(&SessionRow{}).Where("user_id = ? AND revoked_at IS NULL", userID).Update("revoked_at", at).Error
	})
}

func (s *GormStore) UpsertPermission(ctx context.Context, row PermissionRow) error {
	return s.writeTransaction(ctx, func(tx *gorm.DB) error {
		return tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "id"}}, DoUpdates: clause.AssignmentColumns([]string{"description"})}).Create(&row).Error
	})
}

func (s *GormStore) ListPermissions(ctx context.Context) ([]PermissionRow, error) {
	var rows []PermissionRow
	err := s.db.WithContext(ctx).Order("id ASC").Find(&rows).Error
	return rows, err
}

func (s *GormStore) CreateRole(ctx context.Context, row RoleRow, permissionIDs []string) error {
	return s.writeTransaction(ctx, func(tx *gorm.DB) error {
		if err := tx.Create(&row).Error; err != nil {
			return err
		}
		return replaceRolePermissionsTx(tx, row.ID, permissionIDs)
	})
}

func (s *GormStore) UpsertBuiltinRole(ctx context.Context, row RoleRow, permissionIDs []string) error {
	return s.writeTransaction(ctx, func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "id"}}, DoUpdates: clause.AssignmentColumns([]string{"name", "description", "builtin", "updated_at"})}).Create(&row).Error; err != nil {
			return err
		}
		return replaceRolePermissionsTx(tx, row.ID, permissionIDs)
	})
}

func replaceRolePermissionsTx(tx *gorm.DB, roleID string, permissionIDs []string) error {
	if err := tx.Where("role_id = ?", roleID).Delete(&RolePermissionRow{}).Error; err != nil {
		return err
	}
	for _, permissionID := range permissionIDs {
		if err := tx.Create(&RolePermissionRow{RoleID: roleID, PermissionID: permissionID}).Error; err != nil {
			return err
		}
	}
	return nil
}

func (s *GormStore) ReplaceRolePermissions(ctx context.Context, roleID string, permissionIDs []string) error {
	return s.writeTransaction(ctx, func(tx *gorm.DB) error {
		var role RoleRow
		if err := tx.Where("id = ?", roleID).First(&role).Error; err != nil {
			return err
		}
		if err := replaceRolePermissionsTx(tx, roleID, permissionIDs); err != nil {
			return err
		}
		return tx.Model(&RoleRow{}).Where("id = ?", roleID).Updates(map[string]any{"revision": gorm.Expr("revision + 1"), "updated_at": time.Now().UTC()}).Error
	})
}

func (s *GormStore) ListRoles(ctx context.Context) ([]RoleRow, error) {
	var rows []RoleRow
	err := s.db.WithContext(ctx).Order("name ASC").Find(&rows).Error
	return rows, err
}

func (s *GormStore) GetRole(ctx context.Context, id string) (RoleRow, []string, error) {
	var row RoleRow
	if err := s.db.WithContext(ctx).Where("id = ?", id).First(&row).Error; err != nil {
		return RoleRow{}, nil, err
	}
	var permissions []string
	err := s.db.WithContext(ctx).Model(&RolePermissionRow{}).Where("role_id = ?", id).Order("permission_id ASC").Pluck("permission_id", &permissions).Error
	return row, permissions, err
}

func (s *GormStore) BindRole(ctx context.Context, row RoleBindingRow) error {
	return s.writeTransaction(ctx, func(tx *gorm.DB) error {
		return tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "user_id"}, {Name: "role_id"}}, DoNothing: true}).Create(&row).Error
	})
}

func (s *GormStore) UnbindRole(ctx context.Context, userID, roleID string) error {
	return s.writeTransaction(ctx, func(tx *gorm.DB) error {
		return tx.Where("user_id = ? AND role_id = ?", userID, roleID).Delete(&RoleBindingRow{}).Error
	})
}

func (s *GormStore) UserRoleIDs(ctx context.Context, userID string) ([]string, error) {
	var roleIDs []string
	err := s.db.WithContext(ctx).Model(&RoleBindingRow{}).Where("user_id = ?", userID).Order("role_id ASC").Pluck("role_id", &roleIDs).Error
	return roleIDs, err
}

func (s *GormStore) UserPermissions(ctx context.Context, userID string) ([]string, error) {
	var permissions []string
	err := s.db.WithContext(ctx).Table("role_permission_rows rp").
		Select("DISTINCT rp.permission_id").
		Joins("JOIN role_binding_rows rb ON rb.role_id = rp.role_id").
		Where("rb.user_id = ?", userID).Order("rp.permission_id ASC").Pluck("rp.permission_id", &permissions).Error
	return permissions, err
}

func (s *GormStore) CreateResourceGroup(ctx context.Context, row ResourceGroupRow) error {
	return s.writeTransaction(ctx, func(tx *gorm.DB) error { return tx.Create(&row).Error })
}

func (s *GormStore) UpsertBuiltinResourceGroup(ctx context.Context, row ResourceGroupRow) error {
	return s.writeTransaction(ctx, func(tx *gorm.DB) error {
		return tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "id"}}, DoUpdates: clause.AssignmentColumns([]string{"name", "description", "builtin", "updated_at"})}).Create(&row).Error
	})
}

func (s *GormStore) ListResourceGroups(ctx context.Context) ([]ResourceGroupRow, error) {
	var rows []ResourceGroupRow
	err := s.db.WithContext(ctx).Order("name ASC").Find(&rows).Error
	return rows, err
}

func (s *GormStore) GetResourceGroup(ctx context.Context, id string) (ResourceGroupRow, error) {
	var row ResourceGroupRow
	err := s.db.WithContext(ctx).Where("id = ?", id).First(&row).Error
	return row, err
}

func (s *GormStore) GrantResourceGroup(ctx context.Context, row ResourceGroupGrantRow) error {
	return s.writeTransaction(ctx, func(tx *gorm.DB) error {
		return tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "subject_kind"}, {Name: "subject_id"}, {Name: "resource_group_id"}}, DoNothing: true}).Create(&row).Error
	})
}

func (s *GormStore) VisibleResourceGroupIDs(ctx context.Context, userID string) ([]string, error) {
	roleIDs, err := s.UserRoleIDs(ctx, userID)
	if err != nil {
		return nil, err
	}
	query := s.db.WithContext(ctx).Model(&ResourceGroupGrantRow{}).Where("subject_kind = ? AND subject_id = ?", "user", userID)
	if len(roleIDs) > 0 {
		query = query.Or("subject_kind = ? AND subject_id IN ?", "role", roleIDs)
	}
	var ids []string
	err = query.Distinct("resource_group_id").Order("resource_group_id ASC").Pluck("resource_group_id", &ids).Error
	return ids, err
}

func (s *GormStore) BindResource(ctx context.Context, row ResourceBindingRow) error {
	return s.writeTransaction(ctx, func(tx *gorm.DB) error {
		return tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "resource_kind"}, {Name: "resource_id"}}, DoUpdates: clause.AssignmentColumns([]string{"resource_group_id", "updated_at"})}).Create(&row).Error
	})
}

func (s *GormStore) GetResourceBinding(ctx context.Context, kind, id string) (ResourceBindingRow, error) {
	var row ResourceBindingRow
	err := s.db.WithContext(ctx).Where("resource_kind = ? AND resource_id = ?", kind, id).First(&row).Error
	return row, err
}

func (s *GormStore) UpsertQuotaPolicy(ctx context.Context, row QuotaPolicyRow) error {
	return s.writeTransaction(ctx, func(tx *gorm.DB) error { return tx.Save(&row).Error })
}

func (s *GormStore) ListQuotaPolicies(ctx context.Context) ([]QuotaPolicyRow, error) {
	var rows []QuotaPolicyRow
	err := s.db.WithContext(ctx).Order("metric ASC, id ASC").Find(&rows).Error
	return rows, err
}

// ConsumeQuota serializes policy evaluation and usage mutation. SQLite uses the
// store's immediate-writer transaction; other databases additionally lock the
// usage row. Every applicable user, role, and resource-group policy participates,
// and the lowest limit wins.
func (s *GormStore) ConsumeQuota(ctx context.Context, userID, resourceGroupID, metric string, delta int64, now time.Time) (QuotaDecision, error) {
	var decision QuotaDecision
	err := s.writeTransaction(ctx, func(tx *gorm.DB) error {
		var roleIDs []string
		if err := tx.Model(&RoleBindingRow{}).Where("user_id = ?", userID).Pluck("role_id", &roleIDs).Error; err != nil {
			return err
		}
		query := tx.Where("metric = ? AND (resource_group_id = '' OR resource_group_id = ?)", metric, resourceGroupID).Where(
			"(subject_kind = ? AND subject_id = ?) OR (subject_kind = ? AND subject_id = ?)", "user", userID, "resource_group", resourceGroupID)
		if len(roleIDs) > 0 {
			query = query.Or("metric = ? AND (resource_group_id = '' OR resource_group_id = ?) AND subject_kind = ? AND subject_id IN ?", metric, resourceGroupID, "role", roleIDs)
		}
		var policies []QuotaPolicyRow
		if err := query.Find(&policies).Error; err != nil {
			return err
		}
		decision = QuotaDecision{Metric: metric, Limit: -1, Allowed: true}
		usages := make(map[string]*QuotaUsageRow)
		bestRemaining := int64(^uint64(0) >> 1)
		for i := range policies {
			policy := &policies[i]
			if policy.ResetAt != nil && !now.Before(*policy.ResetAt) {
				continue
			}
			scopeGroup := policy.ResourceGroupID
			if scopeGroup == "" {
				scopeGroup = resourceGroupID
			}
			usageKey := policy.SubjectKind + "\x00" + policy.SubjectID + "\x00" + scopeGroup + "\x00" + metric
			usage := usages[usageKey]
			if usage == nil {
				digest := sha256.Sum256([]byte(usageKey))
				usage = &QuotaUsageRow{ID: hex.EncodeToString(digest[:]), SubjectKind: policy.SubjectKind, SubjectID: policy.SubjectID, ResourceGroupID: scopeGroup, Metric: metric, UpdatedAt: now}
				err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", usage.ID).First(usage).Error
				if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
					return err
				}
				if usage.ResetAt != nil && !now.Before(*usage.ResetAt) {
					usage.Current = 0
					usage.ResetAt = nil
				}
				usages[usageKey] = usage
			}
			next := usage.Current + delta
			if next < 0 {
				next = 0
			}
			remaining := policy.Limit - next
			if remaining < bestRemaining {
				bestRemaining = remaining
				decision = QuotaDecision{Metric: metric, Current: next, Limit: policy.Limit, Allowed: next <= policy.Limit, ExceedAction: policy.ExceedAction, RecoveryCondition: policy.RecoveryCondition, ResetAt: policy.ResetAt}
			}
			if next > policy.Limit {
				decision.Current = usage.Current
				decision.Allowed = false
				return ErrQuotaExceeded
			}
			usage.Current = next
			usage.UpdatedAt = now
			if policy.ResetAt != nil {
				usage.ResetAt = policy.ResetAt
			}
		}
		for _, usage := range usages {
			if err := tx.Save(usage).Error; err != nil {
				return err
			}
		}
		return nil
	})
	return decision, err
}

// ConsumeQuotaForResource is the transaction-aware bridge used by governed
// resource mutations. The resource inherits its owning object's group (an
// HTTP/L4 rule inherits its agent group) and the caller's revision transaction
// also owns the quota counter and success audit event.
func (s *GormStore) ConsumeQuotaForResource(ctx context.Context, ownerKind, ownerID, metric string, delta int64) (QuotaDecision, error) {
	actor, ok := QuotaActorFromContext(ctx)
	if !ok {
		return QuotaDecision{Metric: metric, Limit: -1, Allowed: true}, nil
	}
	groupID := "default"
	binding, err := s.GetResourceBinding(ctx, ownerKind, ownerID)
	if err == nil {
		groupID = binding.ResourceGroupID
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return QuotaDecision{}, err
	}
	var decision QuotaDecision
	err = s.SecurityTransaction(ctx, func(tx *GormStore) error {
		var consumeErr error
		decision, consumeErr = tx.ConsumeQuota(ctx, actor.UserID, groupID, metric, delta, time.Now().UTC())
		if consumeErr != nil {
			return consumeErr
		}
		metadata, _ := json.Marshal(map[string]any{"metric": metric, "delta": delta, "current": decision.Current, "limit": decision.Limit, "recovery_condition": decision.RecoveryCondition})
		return tx.AppendAuditEvent(ctx, AuditEventRow{
			ID: securityID("audit"), ActorID: actor.UserID, SessionID: actor.SessionID, Action: "quota.consume",
			TargetKind: ownerKind, TargetID: ownerID, ResourceGroupID: groupID, CorrelationID: actor.CorrelationID,
			Result: "success", MetadataJSON: string(metadata), CreatedAt: time.Now().UTC(),
		})
	})
	return decision, err
}

func securityID(prefix string) string {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		panic(err)
	}
	return prefix + "_" + hex.EncodeToString(value)
}

func (s *GormStore) AppendAuditEvent(ctx context.Context, row AuditEventRow) error {
	return s.writeTransaction(ctx, func(tx *gorm.DB) error { return tx.Create(&row).Error })
}

func (s *GormStore) ListAuditEvents(ctx context.Context, limit int) ([]AuditEventRow, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	var rows []AuditEventRow
	err := s.db.WithContext(ctx).Order("created_at DESC, id DESC").Limit(limit).Find(&rows).Error
	return rows, err
}

func (s *GormStore) CreateSecret(ctx context.Context, secret SecretRow, version SecretVersionRow) error {
	return s.writeTransaction(ctx, func(tx *gorm.DB) error {
		if err := tx.Create(&secret).Error; err != nil {
			return err
		}
		return tx.Create(&version).Error
	})
}

func (s *GormStore) RotateSecret(ctx context.Context, secret SecretRow, version SecretVersionRow) error {
	return s.writeTransaction(ctx, func(tx *gorm.DB) error {
		var current SecretRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", secret.ID).First(&current).Error; err != nil {
			return err
		}
		if version.Version != current.ActiveVersion+1 {
			return fmt.Errorf("secret version conflict")
		}
		if err := tx.Create(&version).Error; err != nil {
			return err
		}
		return tx.Model(&SecretRow{}).Where("id = ? AND active_version = ?", secret.ID, current.ActiveVersion).Updates(map[string]any{
			"active_version": version.Version, "fingerprint": secret.Fingerprint, "rotated_at": secret.RotatedAt,
		}).Error
	})
}

func (s *GormStore) GetSecret(ctx context.Context, id string) (SecretRow, error) {
	var row SecretRow
	err := s.db.WithContext(ctx).Where("id = ?", id).First(&row).Error
	return row, err
}

func (s *GormStore) ListSecrets(ctx context.Context, resourceGroupIDs []string) ([]SecretRow, error) {
	query := s.db.WithContext(ctx).Order("name ASC")
	if resourceGroupIDs != nil {
		if len(resourceGroupIDs) == 0 {
			return []SecretRow{}, nil
		}
		query = query.Where("resource_group_id IN ?", resourceGroupIDs)
	}
	var rows []SecretRow
	err := query.Find(&rows).Error
	return rows, err
}

func (s *GormStore) GetSecretVersion(ctx context.Context, id string, version uint64) (SecretVersionRow, error) {
	var row SecretVersionRow
	err := s.db.WithContext(ctx).Where("secret_id = ? AND version = ? AND destroyed_at IS NULL", id, version).First(&row).Error
	return row, err
}

func (s *GormStore) MarkSecretUsed(ctx context.Context, id string, at time.Time) error {
	return s.writeTransaction(ctx, func(tx *gorm.DB) error {
		return tx.Model(&SecretRow{}).Where("id = ?", id).Update("last_used_at", at).Error
	})
}
