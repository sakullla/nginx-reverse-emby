package storage

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrQuotaExceeded                = errors.New("quota exceeded")
	ErrQuotaActorRequired           = errors.New("quota actor required")
	ErrCertificateTargetsCrossGroup = errors.New("certificate targets span resource groups")
	ErrCertificateGroupMismatch     = errors.New("certificate resource group does not match target agents")
)

const (
	agentBandwidthMetric    = "bandwidth_bytes_per_second"
	agentBandwidthFreshness = 2 * time.Minute
)

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
	ResourceGroupID   string     `json:"resource_group_id"`
	Current           int64      `json:"current"`
	Limit             int64      `json:"limit"`
	Allowed           bool       `json:"allowed"`
	ExceedAction      string     `json:"exceed_action"`
	RecoveryCondition string     `json:"recovery_condition"`
	ResetAt           *time.Time `json:"reset_at,omitempty"`
}

type QuotaExceededError struct {
	Decision QuotaDecision
}

func (e *QuotaExceededError) Error() string { return ErrQuotaExceeded.Error() }

func (e *QuotaExceededError) Unwrap() error { return ErrQuotaExceeded }

type QuotaActor struct {
	UserID        string
	SessionID     string
	CorrelationID string
	Bootstrap     bool
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

func (s *GormStore) ListResourceGroupGrants(ctx context.Context) ([]ResourceGroupGrantRow, error) {
	var rows []ResourceGroupGrantRow
	err := s.db.WithContext(ctx).Order("resource_group_id ASC, subject_kind ASC, subject_id ASC").Find(&rows).Error
	return rows, err
}

func (s *GormStore) RevokeResourceGroupGrant(ctx context.Context, subjectKind, subjectID, resourceGroupID string) error {
	return s.writeTransaction(ctx, func(tx *gorm.DB) error {
		result := tx.Where("subject_kind = ? AND subject_id = ? AND resource_group_id = ?", subjectKind, subjectID, resourceGroupID).Delete(&ResourceGroupGrantRow{})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		return nil
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
		if row.ResourceKind == "certificate" {
			certificateID, err := strconv.Atoi(strings.TrimSpace(row.ResourceID))
			if err != nil || certificateID <= 0 {
				return gorm.ErrRecordNotFound
			}
			var certificate ManagedCertificateRow
			if err := tx.Where("id = ?", certificateID).First(&certificate).Error; err != nil {
				return err
			}
			groupID, err := managedCertificateResourceGroupTx(tx, certificate, "", "")
			if err != nil {
				return err
			}
			if groupID == crossGroupCertificateGroupID && row.ResourceGroupID != groupID {
				return fmt.Errorf("%w: certificate %d", ErrCertificateTargetsCrossGroup, certificate.ID)
			}
			if row.ResourceGroupID != groupID {
				return fmt.Errorf("%w: certificate %d requires %s", ErrCertificateGroupMismatch, certificate.ID, groupID)
			}
		}
		var current ResourceBindingRow
		currentErr := gorm.ErrRecordNotFound
		if row.ResourceKind == "agent" {
			// Agent bindings are locked as one deterministic ordered class before
			// plugin instances. Configure follows the same binding->instance order.
			var agentBindings []ResourceBindingRow
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("resource_kind = ?", "agent").Order("resource_id").Find(&agentBindings).Error; err != nil {
				return err
			}
			for _, binding := range agentBindings {
				if binding.ResourceID == row.ResourceID {
					current, currentErr = binding, nil
					break
				}
			}
		} else {
			currentErr = tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("resource_kind = ? AND resource_id = ?", row.ResourceKind, row.ResourceID).First(&current).Error
		}
		if currentErr != nil && !errors.Is(currentErr, gorm.ErrRecordNotFound) {
			return currentErr
		}
		currentGroupID := "default"
		if currentErr == nil {
			currentGroupID = current.ResourceGroupID
		}
		movingGroups := currentGroupID != row.ResourceGroupID
		affected := map[string]ResourceBindingRow{row.ResourceKind + "\x00" + row.ResourceID: row}
		if row.ResourceKind == "agent" {
			var children []ResourceBindingRow
			if err := tx.Where("parent_resource_kind = ? AND parent_resource_id = ?", "agent", row.ResourceID).Find(&children).Error; err != nil {
				return err
			}
			for _, child := range children {
				child.ResourceGroupID = row.ResourceGroupID
				child.UpdatedAt = row.UpdatedAt
				affected[child.ResourceKind+"\x00"+child.ResourceID] = child
			}
			if movingGroups {
				if err := addAgentCertificateBindingsTx(tx, row, affected); err != nil {
					return err
				}
				if err := addAgentPluginBindingsTx(tx, row, affected); err != nil {
					return err
				}
			}
		}
		if movingGroups {
			var policies []QuotaPolicyRow
			if err := tx.Where("resource_group_id = ?", row.ResourceGroupID).Order("subject_kind ASC, subject_id ASC, metric ASC, id ASC").Find(&policies).Error; err != nil {
				return err
			}
			if err := lockTargetQuotaScopesTx(tx, policies, row.ResourceGroupID, row.UpdatedAt); err != nil {
				return err
			}
			var allocations []QuotaAllocationRow
			if err := tx.Find(&allocations).Error; err != nil {
				return err
			}
			movingByScope := make(map[string]int64)
			targetByScope := make(map[string]int64)
			for _, allocation := range allocations {
				_, moving := affected[allocation.ResourceKind+"\x00"+allocation.ResourceID]
				scope := quotaScope{SubjectKind: allocation.SubjectKind, SubjectID: allocation.SubjectID, ResourceGroupID: allocation.ResourceGroupID}
				if allocation.ResourceGroupID == row.ResourceGroupID && !moving {
					targetByScope[scope.key(allocation.Metric)] += allocation.Amount
				}
				if moving && allocation.ResourceGroupID != "" {
					scope.ResourceGroupID = row.ResourceGroupID
					if scope.SubjectKind == "resource_group" {
						scope.SubjectID = row.ResourceGroupID
					}
					movingByScope[scope.key(allocation.Metric)] += allocation.Amount
				}
			}
			for _, policy := range policies {
				scope := quotaScope{SubjectKind: policy.SubjectKind, SubjectID: policy.SubjectID, ResourceGroupID: row.ResourceGroupID}
				key := scope.key(policy.Metric)
				next := targetByScope[key] + movingByScope[key]
				if next > policy.Limit {
					return &QuotaExceededError{Decision: QuotaDecision{Metric: policy.Metric, ResourceGroupID: row.ResourceGroupID, Current: next, Limit: policy.Limit, Allowed: false, ExceedAction: policy.ExceedAction, RecoveryCondition: policy.RecoveryCondition}}
				}
			}
		}
		if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "resource_kind"}, {Name: "resource_id"}}, DoUpdates: clause.AssignmentColumns([]string{"resource_group_id", "parent_resource_kind", "parent_resource_id", "updated_at"})}).Create(&row).Error; err != nil {
			return err
		}
		for key, binding := range affected {
			if key == row.ResourceKind+"\x00"+row.ResourceID {
				continue
			}
			if err := tx.Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "resource_kind"}, {Name: "resource_id"}},
				DoUpdates: clause.AssignmentColumns([]string{"resource_group_id", "parent_resource_kind", "parent_resource_id", "updated_at"}),
			}).Create(&binding).Error; err != nil {
				return err
			}
		}
		if movingGroups {
			for key := range affected {
				parts := strings.SplitN(key, "\x00", 2)
				var allocations []QuotaAllocationRow
				if err := tx.Where("resource_kind = ? AND resource_id = ? AND resource_group_id <> ''", parts[0], parts[1]).Find(&allocations).Error; err != nil {
					return err
				}
				for _, allocation := range allocations {
					next := allocation
					next.ResourceGroupID = row.ResourceGroupID
					if next.SubjectKind == "resource_group" {
						next.SubjectID = row.ResourceGroupID
					}
					nextScope := quotaScope{SubjectKind: next.SubjectKind, SubjectID: next.SubjectID, ResourceGroupID: next.ResourceGroupID}
					next.ID = quotaAllocationID(next.ResourceKind, next.ResourceID, next.Metric, nextScope)
					if err := tx.Delete(&QuotaAllocationRow{}, "id = ?", allocation.ID).Error; err != nil {
						return err
					}
					if err := tx.Clauses(clause.OnConflict{
						Columns:   []clause.Column{{Name: "resource_kind"}, {Name: "resource_id"}, {Name: "metric"}, {Name: "subject_kind"}, {Name: "subject_id"}, {Name: "resource_group_id"}},
						DoUpdates: clause.AssignmentColumns([]string{"amount"}),
					}).Create(&next).Error; err != nil {
						return err
					}
				}
			}
			if err := recomputeCountQuotaUsageTx(tx, row.UpdatedAt); err != nil {
				return err
			}
		}
		return nil
	})
}

func addAgentPluginBindingsTx(tx *gorm.DB, agentBinding ResourceBindingRow, affected map[string]ResourceBindingRow) error {
	var instances []PluginInstanceRow
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Order("id").Find(&instances).Error; err != nil {
		return err
	}
	for index := range instances {
		targets, err := pluginInstanceTargets(instances[index].TargetJSON)
		if err != nil {
			return fmt.Errorf("plugin instance %s targets: %w", instances[index].ID, err)
		}
		includesMoving := false
		groupID := ""
		for _, target := range targets {
			if target == agentBinding.ResourceID {
				includesMoving = true
				if groupID == "" {
					groupID = agentBinding.ResourceGroupID
				} else if groupID != agentBinding.ResourceGroupID {
					return fmt.Errorf("plugin instance %s targets would cross resource groups", instances[index].ID)
				}
				continue
			}
			var targetBinding ResourceBindingRow
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("resource_kind = ? AND resource_id = ?", "agent", target).First(&targetBinding).Error; err != nil {
				return err
			}
			if groupID == "" {
				groupID = targetBinding.ResourceGroupID
			} else if groupID != targetBinding.ResourceGroupID {
				return fmt.Errorf("plugin instance %s targets would cross resource groups", instances[index].ID)
			}
		}
		includesPending := false
		if instances[index].PendingOperationID != "" && (strings.TrimSpace(instances[index].PendingTargetJSON) != "" || strings.TrimSpace(instances[index].PendingResourceGroupID) != "") {
			pendingTargets, err := pluginInstanceTargets(instances[index].PendingTargetJSON)
			if err != nil {
				return fmt.Errorf("plugin instance %s pending targets: %w", instances[index].ID, err)
			}
			pendingGroup := ""
			for _, target := range pendingTargets {
				targetGroup := ""
				if target == agentBinding.ResourceID {
					includesPending = true
					targetGroup = agentBinding.ResourceGroupID
				} else {
					var targetBinding ResourceBindingRow
					if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("resource_kind = ? AND resource_id = ?", "agent", target).First(&targetBinding).Error; err != nil {
						return err
					}
					targetGroup = targetBinding.ResourceGroupID
				}
				if pendingGroup == "" {
					pendingGroup = targetGroup
				} else if pendingGroup != targetGroup {
					return fmt.Errorf("plugin instance %s pending targets would cross resource groups", instances[index].ID)
				}
			}
			if includesPending && pendingGroup != instances[index].PendingResourceGroupID {
				return fmt.Errorf("plugin instance %s has a pending cross-group target", instances[index].ID)
			}
		}
		if !includesMoving {
			continue
		}
		instances[index].ResourceGroupID = groupID
		instances[index].UpdatedAt = agentBinding.UpdatedAt
		if err := tx.Model(&PluginInstanceRow{}).Where("id = ?", instances[index].ID).Updates(map[string]any{"resource_group_id": groupID, "state_version": gorm.Expr("state_version + 1"), "updated_at": agentBinding.UpdatedAt}).Error; err != nil {
			return err
		}
		binding := ResourceBindingRow{ID: securityID("res"), ResourceKind: "plugin_instance", ResourceID: instances[index].ID, ResourceGroupID: groupID, UpdatedAt: agentBinding.UpdatedAt}
		if len(targets) == 1 {
			binding.ParentResourceKind, binding.ParentResourceID = "agent", targets[0]
		}
		var current ResourceBindingRow
		if err := tx.Where("resource_kind = ? AND resource_id = ?", "plugin_instance", instances[index].ID).First(&current).Error; err == nil {
			binding.ID = current.ID
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		affected["plugin_instance\x00"+instances[index].ID] = binding
	}
	return nil
}

func lockTargetQuotaScopesTx(tx *gorm.DB, policies []QuotaPolicyRow, resourceGroupID string, now time.Time) error {
	type lockTarget struct {
		scope  quotaScope
		metric string
	}
	targets := make(map[string]lockTarget, len(policies))
	keys := make([]string, 0, len(policies))
	for _, policy := range policies {
		scope := quotaScope{SubjectKind: policy.SubjectKind, SubjectID: policy.SubjectID, ResourceGroupID: resourceGroupID}
		key := scope.key(policy.Metric)
		if _, found := targets[key]; found {
			continue
		}
		targets[key] = lockTarget{scope: scope, metric: policy.Metric}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		target := targets[key]
		if _, err := ensureQuotaUsageTx(tx, target.scope, target.metric, now); err != nil {
			return err
		}
	}
	return nil
}

func addAgentCertificateBindingsTx(tx *gorm.DB, agentBinding ResourceBindingRow, affected map[string]ResourceBindingRow) error {
	var certificates []ManagedCertificateRow
	if err := tx.Find(&certificates).Error; err != nil {
		return err
	}
	for _, certificate := range certificates {
		var targetAgentIDs []string
		if err := json.Unmarshal([]byte(defaultJSON(certificate.TargetAgentIDs, "[]")), &targetAgentIDs); err != nil {
			return fmt.Errorf("certificate %d targets: %w", certificate.ID, err)
		}
		includesMovingAgent := false
		for _, targetAgentID := range targetAgentIDs {
			if strings.TrimSpace(targetAgentID) == agentBinding.ResourceID {
				includesMovingAgent = true
				break
			}
		}
		if !includesMovingAgent {
			continue
		}
		groupID, err := managedCertificateResourceGroupTx(tx, certificate, agentBinding.ResourceID, agentBinding.ResourceGroupID)
		if err != nil {
			return err
		}
		if groupID == crossGroupCertificateGroupID {
			return fmt.Errorf("%w: certificate %d", ErrCertificateTargetsCrossGroup, certificate.ID)
		}
		resourceID := strconv.Itoa(certificate.ID)
		binding := ResourceBindingRow{
			ID: securityID("res"), ResourceKind: "certificate", ResourceID: resourceID,
			ResourceGroupID: groupID, UpdatedAt: agentBinding.UpdatedAt,
		}
		var current ResourceBindingRow
		if err := tx.Where("resource_kind = ? AND resource_id = ?", "certificate", resourceID).First(&current).Error; err == nil {
			binding.ID = current.ID
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		affected["certificate\x00"+resourceID] = binding
	}
	return nil
}

func managedCertificateResourceGroupTx(tx *gorm.DB, certificate ManagedCertificateRow, overrideAgentID, overrideGroupID string) (string, error) {
	var targetAgentIDs []string
	if err := json.Unmarshal([]byte(defaultJSON(certificate.TargetAgentIDs, "[]")), &targetAgentIDs); err != nil {
		return "", fmt.Errorf("certificate %d targets: %w", certificate.ID, err)
	}
	groupID := "default"
	for index, targetAgentID := range targetAgentIDs {
		targetAgentID = strings.TrimSpace(targetAgentID)
		targetGroupID := "default"
		if targetAgentID == strings.TrimSpace(overrideAgentID) && targetAgentID != "" {
			targetGroupID = overrideGroupID
		} else {
			var targetBinding ResourceBindingRow
			err := tx.Where("resource_kind = ? AND resource_id = ?", "agent", targetAgentID).First(&targetBinding).Error
			if err == nil {
				targetGroupID = targetBinding.ResourceGroupID
			} else if !errors.Is(err, gorm.ErrRecordNotFound) {
				return "", err
			}
		}
		if index == 0 {
			groupID = targetGroupID
			continue
		}
		if groupID != targetGroupID {
			return crossGroupCertificateGroupID, nil
		}
	}
	return groupID, nil
}

func recomputeCountQuotaUsageTx(tx *gorm.DB, now time.Time) error {
	metrics := []string{"rule_count", "application_count", "public_port_count"}
	if err := tx.Model(&QuotaUsageRow{}).Where("metric IN ?", metrics).Updates(map[string]any{"current": 0, "reset_at": nil, "updated_at": now}).Error; err != nil {
		return err
	}
	type total struct {
		SubjectKind, SubjectID, ResourceGroupID, Metric string
		Current                                         int64
	}
	var totals []total
	if err := tx.Model(&QuotaAllocationRow{}).Select("subject_kind, subject_id, resource_group_id, metric, SUM(amount) AS current").Where("metric IN ?", metrics).Group("subject_kind, subject_id, resource_group_id, metric").Scan(&totals).Error; err != nil {
		return err
	}
	for _, item := range totals {
		scope := quotaScope{SubjectKind: item.SubjectKind, SubjectID: item.SubjectID, ResourceGroupID: item.ResourceGroupID}
		usage := QuotaUsageRow{ID: quotaUsageID(scope, item.Metric), SubjectKind: item.SubjectKind, SubjectID: item.SubjectID, ResourceGroupID: item.ResourceGroupID, Metric: item.Metric, Current: item.Current, UpdatedAt: now}
		if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "subject_kind"}, {Name: "subject_id"}, {Name: "resource_group_id"}, {Name: "metric"}}, DoUpdates: clause.AssignmentColumns([]string{"current", "reset_at", "updated_at"})}).Create(&usage).Error; err != nil {
			return err
		}
	}
	return nil
}

func (s *GormStore) GetResourceBinding(ctx context.Context, kind, id string) (ResourceBindingRow, error) {
	var row ResourceBindingRow
	err := s.db.WithContext(ctx).Where("resource_kind = ? AND resource_id = ?", kind, id).First(&row).Error
	return row, err
}

func (s *GormStore) ResourceExists(ctx context.Context, kind, id string) (bool, error) {
	db := s.db.WithContext(ctx)
	var count int64
	switch strings.TrimSpace(kind) {
	case "agent":
		err := db.Model(&AgentRow{}).Where("id = ?", strings.TrimSpace(id)).Count(&count).Error
		return count > 0, err
	case "http_rule", "l4_rule", "relay_listener":
		agentID, numericID, ok := splitBoundResourceID(id)
		if !ok {
			return false, nil
		}
		var model any
		switch kind {
		case "http_rule":
			model = &HTTPRuleRow{}
		case "l4_rule":
			model = &L4RuleRow{}
		default:
			model = &RelayListenerRow{}
		}
		err := db.Model(model).Where("agent_id = ? AND id = ?", agentID, numericID).Count(&count).Error
		return count > 0, err
	case "certificate", "egress_profile":
		numericID, err := strconv.Atoi(strings.TrimSpace(id))
		if err != nil || numericID <= 0 {
			return false, nil
		}
		var model any = &ManagedCertificateRow{}
		if kind == "egress_profile" {
			model = &EgressProfileRow{}
		}
		err = db.Model(model).Where("id = ?", numericID).Count(&count).Error
		return count > 0, err
	default:
		return false, nil
	}
}

func splitBoundResourceID(id string) (string, int, bool) {
	index := strings.LastIndex(strings.TrimSpace(id), ":")
	if index <= 0 || index == len(id)-1 {
		return "", 0, false
	}
	numericID, err := strconv.Atoi(id[index+1:])
	if err != nil || numericID <= 0 {
		return "", 0, false
	}
	return id[:index], numericID, true
}

func (s *GormStore) UpsertQuotaPolicy(ctx context.Context, row QuotaPolicyRow) error {
	if isCountQuotaMetric(row.Metric) {
		row.ResetAt = nil
	}
	return s.writeTransaction(ctx, func(tx *gorm.DB) error {
		var current QuotaPolicyRow
		err := tx.Where("id = ?", row.ID).First(&current).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err == nil && (current.SubjectKind != row.SubjectKind || current.SubjectID != row.SubjectID || current.ResourceGroupID != row.ResourceGroupID || current.Metric != row.Metric) {
			if err := tx.Where("policy_id = ?", row.ID).Delete(&QuotaPolicyUsageRow{}).Error; err != nil {
				return err
			}
		}
		return tx.Save(&row).Error
	})
}

func (s *GormStore) GetQuotaPolicy(ctx context.Context, id string) (QuotaPolicyRow, error) {
	var row QuotaPolicyRow
	err := s.db.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", id).First(&row).Error
	return row, err
}

func (s *GormStore) ListQuotaPolicies(ctx context.Context) ([]QuotaPolicyRow, error) {
	var rows []QuotaPolicyRow
	err := s.db.WithContext(ctx).Order("metric ASC, id ASC").Find(&rows).Error
	return rows, err
}

func (s *GormStore) ListQuotaUsage(ctx context.Context) ([]QuotaUsageRow, error) {
	var rows []QuotaUsageRow
	err := s.db.WithContext(ctx).Order("metric ASC, subject_kind ASC, subject_id ASC, resource_group_id ASC").Find(&rows).Error
	return rows, err
}

func (s *GormStore) ListQuotaPolicyUsage(ctx context.Context) ([]QuotaPolicyUsageRow, error) {
	var rows []QuotaPolicyUsageRow
	err := s.db.WithContext(ctx).Order("policy_id ASC, resource_group_id ASC").Find(&rows).Error
	return rows, err
}

func (s *GormStore) ResourceGroupQuotaStatus(ctx context.Context, resourceGroupID, metric string) (QuotaDecision, error) {
	decision := QuotaDecision{Metric: metric, ResourceGroupID: resourceGroupID, Limit: -1, Allowed: true}
	scope := quotaScope{SubjectKind: "resource_group", SubjectID: resourceGroupID, ResourceGroupID: resourceGroupID}
	var usage QuotaUsageRow
	err := s.db.WithContext(ctx).Where("id = ?", quotaUsageID(scope, metric)).First(&usage).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return QuotaDecision{}, err
	}
	var policies []QuotaPolicyRow
	if err := s.db.WithContext(ctx).Where("subject_kind = ? AND subject_id = ? AND resource_group_id = ? AND metric = ?", "resource_group", resourceGroupID, resourceGroupID, metric).Order("id ASC").Find(&policies).Error; err != nil {
		return QuotaDecision{}, err
	}
	bestRemaining := int64(^uint64(0) >> 1)
	bestAction, bestPolicyID := "", ""
	for _, policy := range policies {
		current := usage.Current
		if !isCountQuotaMetric(metric) {
			var policyUsage QuotaPolicyUsageRow
			policyUsageErr := s.db.WithContext(ctx).Where("id = ?", quotaPolicyUsageID(policy.ID, resourceGroupID)).First(&policyUsage).Error
			if policyUsageErr != nil && !errors.Is(policyUsageErr, gorm.ErrRecordNotFound) {
				return QuotaDecision{}, policyUsageErr
			}
			current = policyUsage.Current
			if policy.ResetAt != nil && !time.Now().UTC().Before(*policy.ResetAt) {
				current = 0
			}
		}
		remaining := policy.Limit - current
		if quotaPolicyPreferred(remaining, policy, bestRemaining, bestAction, bestPolicyID) {
			bestRemaining, bestAction, bestPolicyID = remaining, policy.ExceedAction, policy.ID
			decision.Current = current
			decision.Limit, decision.ExceedAction, decision.RecoveryCondition, decision.ResetAt = policy.Limit, policy.ExceedAction, policy.RecoveryCondition, policy.ResetAt
		}
	}
	if len(policies) == 0 {
		decision.Current = usage.Current
	}
	decision.Allowed = decision.Limit < 0 || decision.Current <= decision.Limit
	return decision, nil
}

type quotaScope struct {
	SubjectKind     string
	SubjectID       string
	ResourceGroupID string
}

func (scope quotaScope) key(metric string) string {
	return scope.SubjectKind + "\x00" + scope.SubjectID + "\x00" + scope.ResourceGroupID + "\x00" + metric
}

func quotaUsageID(scope quotaScope, metric string) string {
	digest := sha256.Sum256([]byte(scope.key(metric)))
	return hex.EncodeToString(digest[:])
}

func quotaAllocationID(resourceKind, resourceID, metric string, scope quotaScope) string {
	key := resourceKind + "\x00" + resourceID + "\x00" + metric + "\x00" + scope.key(metric)
	digest := sha256.Sum256([]byte(key))
	return hex.EncodeToString(digest[:])
}

func (s *GormStore) quotaScopesTx(tx *gorm.DB, userID, resourceGroupID string) ([]quotaScope, error) {
	var roleIDs []string
	if userID != "" {
		if err := tx.Model(&RoleBindingRow{}).Where("user_id = ?", userID).Order("role_id ASC").Pluck("role_id", &roleIDs).Error; err != nil {
			return nil, err
		}
	}
	scopes := make([]quotaScope, 0, len(roleIDs)*2+3)
	if userID != "" {
		scopes = append(scopes, quotaScope{SubjectKind: "user", SubjectID: userID, ResourceGroupID: resourceGroupID})
		if resourceGroupID != "" {
			scopes = append(scopes, quotaScope{SubjectKind: "user", SubjectID: userID})
		}
	}
	for _, roleID := range roleIDs {
		scopes = append(scopes, quotaScope{SubjectKind: "role", SubjectID: roleID, ResourceGroupID: resourceGroupID})
		if resourceGroupID != "" {
			scopes = append(scopes, quotaScope{SubjectKind: "role", SubjectID: roleID})
		}
	}
	if resourceGroupID != "" {
		scopes = append(scopes, quotaScope{SubjectKind: "resource_group", SubjectID: resourceGroupID, ResourceGroupID: resourceGroupID})
	}
	sort.Slice(scopes, func(i, j int) bool {
		return scopes[i].key("") < scopes[j].key("")
	})
	return scopes, nil
}

func ensureQuotaUsageTx(tx *gorm.DB, scope quotaScope, metric string, now time.Time) (*QuotaUsageRow, error) {
	usage := QuotaUsageRow{
		ID: quotaUsageID(scope, metric), SubjectKind: scope.SubjectKind, SubjectID: scope.SubjectID,
		ResourceGroupID: scope.ResourceGroupID, Metric: metric, UpdatedAt: now,
	}
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&usage).Error; err != nil {
		return nil, err
	}
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", usage.ID).First(&usage).Error; err != nil {
		return nil, err
	}
	return &usage, nil
}

func isCountQuotaMetric(metric string) bool {
	switch metric {
	case "rule_count", "application_count", "public_port_count":
		return true
	default:
		return false
	}
}

func quotaPolicyUsageID(policyID, resourceGroupID string) string {
	digest := sha256.Sum256([]byte(policyID + "\x00" + resourceGroupID))
	return hex.EncodeToString(digest[:])
}

func ensureQuotaPolicyUsageTx(tx *gorm.DB, policy QuotaPolicyRow, resourceGroupID string, now time.Time) (*QuotaPolicyUsageRow, error) {
	usage := QuotaPolicyUsageRow{
		ID: quotaPolicyUsageID(policy.ID, resourceGroupID), PolicyID: policy.ID,
		ResourceGroupID: resourceGroupID, ResetAt: policy.ResetAt, UpdatedAt: now,
	}
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&usage).Error; err != nil {
		return nil, err
	}
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", usage.ID).First(&usage).Error; err != nil {
		return nil, err
	}
	return &usage, nil
}

func countAllocationCurrentTx(tx *gorm.DB, scope quotaScope, metric string) (int64, error) {
	var current int64
	err := tx.Model(&QuotaAllocationRow{}).
		Where("subject_kind = ? AND subject_id = ? AND resource_group_id = ? AND metric = ?", scope.SubjectKind, scope.SubjectID, scope.ResourceGroupID, metric).
		Select("COALESCE(SUM(amount), 0)").Scan(&current).Error
	return current, err
}

func quotaActionPriority(action string) int {
	switch action {
	case "disable":
		return 3
	case "reject":
		return 2
	case "limit":
		return 1
	default:
		return 0
	}
}

func quotaPolicyPreferred(remaining int64, policy QuotaPolicyRow, bestRemaining int64, bestAction, bestPolicyID string) bool {
	if remaining != bestRemaining {
		return remaining < bestRemaining
	}
	if quotaActionPriority(policy.ExceedAction) != quotaActionPriority(bestAction) {
		return quotaActionPriority(policy.ExceedAction) > quotaActionPriority(bestAction)
	}
	return bestPolicyID == "" || policy.ID < bestPolicyID
}

func (s *GormStore) consumeQuotaScopesTx(tx *gorm.DB, scopes []quotaScope, metric string, delta int64, now time.Time, allocationBacked, persistExceeded bool) (QuotaDecision, error) {
	scopes = append([]quotaScope(nil), scopes...)
	sort.Slice(scopes, func(i, j int) bool {
		return scopes[i].key(metric) < scopes[j].key(metric)
	})
	decision := QuotaDecision{Metric: metric, Limit: -1, Allowed: true}
	if len(scopes) == 0 {
		return decision, nil
	}
	decision.ResourceGroupID = scopes[0].ResourceGroupID

	var policies []QuotaPolicyRow
	if err := tx.Where("metric = ? AND (resource_group_id = '' OR resource_group_id = ?)", metric, decision.ResourceGroupID).Order("id ASC").Find(&policies).Error; err != nil {
		return QuotaDecision{}, err
	}
	policiesByScope := make(map[string][]QuotaPolicyRow)
	validScopes := make(map[string]struct{}, len(scopes))
	for _, scope := range scopes {
		validScopes[scope.SubjectKind+"\x00"+scope.SubjectID] = struct{}{}
	}
	for i := range policies {
		policy := policies[i]
		if _, ok := validScopes[policy.SubjectKind+"\x00"+policy.SubjectID]; !ok {
			continue
		}
		key := quotaScope{SubjectKind: policy.SubjectKind, SubjectID: policy.SubjectID, ResourceGroupID: policy.ResourceGroupID}.key(metric)
		policiesByScope[key] = append(policiesByScope[key], policy)
	}

	usages := make(map[string]*QuotaUsageRow, len(scopes))
	policyUsages := make([]*QuotaPolicyUsageRow, 0)
	bestRemaining := int64(^uint64(0) >> 1)
	bestAction := ""
	bestPolicyID := ""
	for _, scope := range scopes {
		usage, err := ensureQuotaUsageTx(tx, scope, metric, now)
		if err != nil {
			return QuotaDecision{}, err
		}
		key := scope.key(metric)
		scopePolicies := policiesByScope[key]
		if isCountQuotaMetric(metric) {
			current := usage.Current
			if allocationBacked {
				current, err = countAllocationCurrentTx(tx, scope, metric)
				if err != nil {
					return QuotaDecision{}, err
				}
			}
			usage.Current = current
			usage.ResetAt = nil
			for i := range scopePolicies {
				policy := &scopePolicies[i]
				if policy.ResetAt != nil && !now.Before(*policy.ResetAt) {
					policy.ResetAt = nil
					if err := tx.Model(&QuotaPolicyRow{}).Where("id = ?", policy.ID).Updates(map[string]any{"reset_at": nil, "updated_at": now}).Error; err != nil {
						return QuotaDecision{}, err
					}
				}
			}
			next := current + delta
			if next < 0 {
				next = 0
			}
			for _, policy := range scopePolicies {
				remaining := policy.Limit - next
				if quotaPolicyPreferred(remaining, policy, bestRemaining, bestAction, bestPolicyID) {
					bestRemaining = remaining
					bestAction = policy.ExceedAction
					bestPolicyID = policy.ID
					decision = QuotaDecision{Metric: metric, ResourceGroupID: decision.ResourceGroupID, Current: next, Limit: policy.Limit, Allowed: delta <= 0 || next <= policy.Limit, ExceedAction: policy.ExceedAction, RecoveryCondition: policy.RecoveryCondition, ResetAt: policy.ResetAt}
				}
			}
			usage.Current = next
			usage.UpdatedAt = now
			usages[key] = usage
			continue
		}

		if len(scopePolicies) == 0 {
			next := usage.Current + delta
			if next < 0 {
				next = 0
			}
			usage.Current = next
			usage.UpdatedAt = now
			usages[key] = usage
			continue
		}

		maxCurrent := int64(0)
		for i := range scopePolicies {
			policy := &scopePolicies[i]
			policyUsage, err := ensureQuotaPolicyUsageTx(tx, *policy, scope.ResourceGroupID, now)
			if err != nil {
				return QuotaDecision{}, err
			}
			if policy.ResetAt != nil && !now.Before(*policy.ResetAt) {
				policyUsage.Current = 0
				policy.ResetAt = nil
				if err := tx.Model(&QuotaPolicyRow{}).Where("id = ?", policy.ID).Updates(map[string]any{"reset_at": nil, "updated_at": now}).Error; err != nil {
					return QuotaDecision{}, err
				}
			}
			policyUsage.ResetAt = policy.ResetAt
			next := policyUsage.Current + delta
			if next < 0 {
				next = 0
			}
			remaining := policy.Limit - next
			if quotaPolicyPreferred(remaining, *policy, bestRemaining, bestAction, bestPolicyID) {
				bestRemaining = remaining
				bestAction = policy.ExceedAction
				bestPolicyID = policy.ID
				decision = QuotaDecision{Metric: metric, ResourceGroupID: decision.ResourceGroupID, Current: next, Limit: policy.Limit, Allowed: delta <= 0 || next <= policy.Limit, ExceedAction: policy.ExceedAction, RecoveryCondition: policy.RecoveryCondition, ResetAt: policy.ResetAt}
			}
			policyUsage.Current = next
			policyUsage.UpdatedAt = now
			policyUsages = append(policyUsages, policyUsage)
			if next > maxCurrent {
				maxCurrent = next
			}
		}
		usage.Current = maxCurrent
		usage.UpdatedAt = now
		usages[key] = usage
	}
	exceeded := delta > 0 && !decision.Allowed
	if exceeded && !persistExceeded {
		return decision, &QuotaExceededError{Decision: decision}
	}
	for _, usage := range usages {
		if err := tx.Save(usage).Error; err != nil {
			return QuotaDecision{}, err
		}
	}
	for _, usage := range policyUsages {
		if err := tx.Save(usage).Error; err != nil {
			return QuotaDecision{}, err
		}
	}
	if decision.Limit < 0 {
		decision.Current = usages[scopes[0].key(metric)].Current
	}
	if !exceeded {
		decision.Allowed = true
	}
	return decision, nil
}

// ConsumeQuota serializes policy evaluation and usage mutation. Usage is
// maintained once per subject scope even when multiple policies overlap.
func (s *GormStore) ConsumeQuota(ctx context.Context, userID, resourceGroupID, metric string, delta int64, now time.Time) (QuotaDecision, error) {
	if _, ok := QuotaActorFromContext(ctx); !ok {
		return QuotaDecision{}, ErrQuotaActorRequired
	}
	var decision QuotaDecision
	err := s.writeTransaction(ctx, func(tx *gorm.DB) error {
		scopes, err := s.quotaScopesTx(tx, userID, resourceGroupID)
		if err != nil {
			return err
		}
		decision, err = s.consumeQuotaScopesTx(tx, scopes, metric, delta, now, false, false)
		return err
	})
	return decision, err
}

// ObserveQuota records an already-observed cumulative delta even when it
// exceeds a policy. Telemetry cannot be rejected retroactively, so the usage
// commits first and the caller receives the denied decision afterwards.
func (s *GormStore) ObserveQuota(ctx context.Context, userID, resourceGroupID, metric string, delta int64, now time.Time) (QuotaDecision, error) {
	if _, ok := QuotaActorFromContext(ctx); !ok {
		return QuotaDecision{}, ErrQuotaActorRequired
	}
	var decision QuotaDecision
	err := s.writeTransaction(ctx, func(tx *gorm.DB) error {
		scopes, err := s.quotaScopesTx(tx, userID, resourceGroupID)
		if err != nil {
			return err
		}
		decision, err = s.consumeQuotaScopesTx(tx, scopes, metric, delta, now, false, true)
		return err
	})
	if err == nil && !decision.Allowed {
		err = &QuotaExceededError{Decision: decision}
	}
	return decision, err
}

// ReconcileResourceGroupQuota projects a sampled gauge (for example current
// bandwidth) into the same strict-policy engine used by cumulative quotas.
func (s *GormStore) ReconcileResourceGroupQuota(ctx context.Context, resourceGroupID, metric string, current int64, now time.Time) (QuotaDecision, error) {
	if _, ok := QuotaActorFromContext(ctx); !ok {
		return QuotaDecision{}, ErrQuotaActorRequired
	}
	if current < 0 {
		current = 0
	}
	var decision QuotaDecision
	err := s.writeTransaction(ctx, func(tx *gorm.DB) error {
		var err error
		decision, err = reconcileResourceGroupQuotaTx(tx, resourceGroupID, metric, current, now)
		return err
	})
	if err == nil && !decision.Allowed {
		err = &QuotaExceededError{Decision: decision}
	}
	return decision, err
}

// ReconcileAgentBandwidth persists one agent's gauge contribution and applies
// resource-group policies to the sum of all member contributions atomically.
func (s *GormStore) ReconcileAgentBandwidth(ctx context.Context, agentID, resourceGroupID string, current int64, now time.Time) (QuotaDecision, error) {
	if _, ok := QuotaActorFromContext(ctx); !ok {
		return QuotaDecision{}, ErrQuotaActorRequired
	}
	if current < 0 {
		current = 0
	}
	var decision QuotaDecision
	err := s.writeTransaction(ctx, func(tx *gorm.DB) error {
		var err error
		decision, err = reconcileAgentBandwidthTx(tx, agentID, resourceGroupID, current, now)
		return err
	})
	if err == nil && !decision.Allowed {
		err = &QuotaExceededError{Decision: decision}
	}
	return decision, err
}

func (s *GormStore) RefreshResourceGroupBandwidth(ctx context.Context, resourceGroupID string, now time.Time) (QuotaDecision, error) {
	var decision QuotaDecision
	err := s.writeTransaction(ctx, func(tx *gorm.DB) error {
		if err := lockBandwidthGroupsTx(tx, []string{resourceGroupID}, now); err != nil {
			return err
		}
		if err := deleteStaleAgentBandwidthTx(tx, []string{resourceGroupID}, now); err != nil {
			return err
		}
		current, err := agentBandwidthTotalTx(tx, resourceGroupID, now)
		if err != nil {
			return err
		}
		decision, err = reconcileResourceGroupQuotaTx(tx, resourceGroupID, agentBandwidthMetric, current, now)
		return err
	})
	return decision, err
}

func reconcileAgentBandwidthTx(tx *gorm.DB, agentID, resourceGroupID string, current int64, now time.Time) (QuotaDecision, error) {
	coordinationScope := quotaScope{SubjectKind: "agent", SubjectID: agentID, ResourceGroupID: ""}
	if _, err := ensureQuotaUsageTx(tx, coordinationScope, agentBandwidthMetric, now); err != nil {
		return QuotaDecision{}, err
	}
	var agent AgentRow
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", agentID).First(&agent).Error; err != nil {
		return QuotaDecision{}, err
	}
	var previous []QuotaUsageRow
	if err := tx.Where("subject_kind = ? AND subject_id = ? AND metric = ? AND resource_group_id <> ''", "agent", agentID, agentBandwidthMetric).Find(&previous).Error; err != nil {
		return QuotaDecision{}, err
	}
	groupSet := map[string]struct{}{resourceGroupID: {}}
	for _, usage := range previous {
		groupSet[usage.ResourceGroupID] = struct{}{}
	}
	groups := make([]string, 0, len(groupSet))
	for groupID := range groupSet {
		groups = append(groups, groupID)
	}
	sort.Strings(groups)
	if err := lockBandwidthGroupsTx(tx, groups, now); err != nil {
		return QuotaDecision{}, err
	}
	if err := deleteStaleAgentBandwidthTx(tx, groups, now); err != nil {
		return QuotaDecision{}, err
	}
	if err := tx.Where("subject_kind = ? AND subject_id = ? AND metric = ? AND resource_group_id <> ? AND resource_group_id <> ''", "agent", agentID, agentBandwidthMetric, resourceGroupID).Delete(&QuotaUsageRow{}).Error; err != nil {
		return QuotaDecision{}, err
	}
	scope := quotaScope{SubjectKind: "agent", SubjectID: agentID, ResourceGroupID: resourceGroupID}
	usage, err := ensureQuotaUsageTx(tx, scope, agentBandwidthMetric, now)
	if err != nil {
		return QuotaDecision{}, err
	}
	usage.Current, usage.ResetAt, usage.UpdatedAt = current, nil, now
	if err := tx.Save(usage).Error; err != nil {
		return QuotaDecision{}, err
	}
	var decision QuotaDecision
	for _, groupID := range groups {
		total, err := agentBandwidthTotalTx(tx, groupID, now)
		if err != nil {
			return QuotaDecision{}, err
		}
		groupDecision, err := reconcileResourceGroupQuotaTx(tx, groupID, agentBandwidthMetric, total, now)
		if err != nil {
			return QuotaDecision{}, err
		}
		if groupID == resourceGroupID {
			decision = groupDecision
		}
	}
	return decision, nil
}

func removeAgentBandwidthTx(tx *gorm.DB, agentID string, now time.Time) error {
	coordinationScope := quotaScope{SubjectKind: "agent", SubjectID: agentID, ResourceGroupID: ""}
	if _, err := ensureQuotaUsageTx(tx, coordinationScope, agentBandwidthMetric, now); err != nil {
		return err
	}
	var previous []QuotaUsageRow
	if err := tx.Where("subject_kind = ? AND subject_id = ? AND metric = ? AND resource_group_id <> ''", "agent", agentID, agentBandwidthMetric).Find(&previous).Error; err != nil {
		return err
	}
	groups := make([]string, 0, len(previous))
	seen := make(map[string]struct{}, len(previous))
	for _, usage := range previous {
		if _, found := seen[usage.ResourceGroupID]; found {
			continue
		}
		seen[usage.ResourceGroupID] = struct{}{}
		groups = append(groups, usage.ResourceGroupID)
	}
	sort.Strings(groups)
	if err := lockBandwidthGroupsTx(tx, groups, now); err != nil {
		return err
	}
	if err := tx.Where("subject_kind = ? AND subject_id = ? AND metric = ?", "agent", agentID, agentBandwidthMetric).Delete(&QuotaUsageRow{}).Error; err != nil {
		return err
	}
	if len(groups) > 0 {
		if err := deleteStaleAgentBandwidthTx(tx, groups, now); err != nil {
			return err
		}
	}
	for _, groupID := range groups {
		total, err := agentBandwidthTotalTx(tx, groupID, now)
		if err != nil {
			return err
		}
		if _, err := reconcileResourceGroupQuotaTx(tx, groupID, agentBandwidthMetric, total, now); err != nil {
			return err
		}
	}
	return nil
}

func lockBandwidthGroupsTx(tx *gorm.DB, groups []string, now time.Time) error {
	groups = append([]string(nil), groups...)
	sort.Strings(groups)
	for _, groupID := range groups {
		scope := quotaScope{SubjectKind: "resource_group", SubjectID: groupID, ResourceGroupID: groupID}
		if _, err := ensureQuotaUsageTx(tx, scope, agentBandwidthMetric, now); err != nil {
			return err
		}
	}
	return nil
}

func deleteStaleAgentBandwidthTx(tx *gorm.DB, groups []string, now time.Time) error {
	return tx.Where("subject_kind = ? AND metric = ? AND resource_group_id IN ? AND updated_at < ?", "agent", agentBandwidthMetric, groups, now.Add(-agentBandwidthFreshness)).Delete(&QuotaUsageRow{}).Error
}

func agentBandwidthTotalTx(tx *gorm.DB, resourceGroupID string, now time.Time) (int64, error) {
	var total int64
	err := tx.Model(&QuotaUsageRow{}).
		Where("subject_kind = ? AND resource_group_id = ? AND metric = ? AND updated_at >= ?", "agent", resourceGroupID, agentBandwidthMetric, now.Add(-agentBandwidthFreshness)).
		Select("COALESCE(SUM(current), 0)").Scan(&total).Error
	return total, err
}

func reconcileResourceGroupQuotaTx(tx *gorm.DB, resourceGroupID, metric string, current int64, now time.Time) (QuotaDecision, error) {
	scope := quotaScope{SubjectKind: "resource_group", SubjectID: resourceGroupID, ResourceGroupID: resourceGroupID}
	var policies []QuotaPolicyRow
	if err := tx.Where("subject_kind = ? AND subject_id = ? AND resource_group_id = ? AND metric = ?", scope.SubjectKind, scope.SubjectID, scope.ResourceGroupID, metric).Order("id ASC").Find(&policies).Error; err != nil {
		return QuotaDecision{}, err
	}
	usage, err := ensureQuotaUsageTx(tx, scope, metric, now)
	if err != nil {
		return QuotaDecision{}, err
	}
	usage.Current, usage.ResetAt, usage.UpdatedAt = current, nil, now
	if err := tx.Save(usage).Error; err != nil {
		return QuotaDecision{}, err
	}
	decision := QuotaDecision{Metric: metric, ResourceGroupID: resourceGroupID, Current: current, Limit: -1, Allowed: true}
	bestRemaining := int64(^uint64(0) >> 1)
	bestAction, bestPolicyID := "", ""
	for _, policy := range policies {
		if policy.ResetAt != nil && !now.Before(*policy.ResetAt) {
			policy.ResetAt = nil
			if err := tx.Model(&QuotaPolicyRow{}).Where("id = ?", policy.ID).Updates(map[string]any{"reset_at": nil, "updated_at": now}).Error; err != nil {
				return QuotaDecision{}, err
			}
		}
		policyUsage, err := ensureQuotaPolicyUsageTx(tx, policy, resourceGroupID, now)
		if err != nil {
			return QuotaDecision{}, err
		}
		policyUsage.Current, policyUsage.ResetAt, policyUsage.UpdatedAt = current, policy.ResetAt, now
		if err := tx.Save(policyUsage).Error; err != nil {
			return QuotaDecision{}, err
		}
		remaining := policy.Limit - current
		if quotaPolicyPreferred(remaining, policy, bestRemaining, bestAction, bestPolicyID) {
			bestRemaining, bestAction, bestPolicyID = remaining, policy.ExceedAction, policy.ID
			decision.Limit, decision.ExceedAction, decision.RecoveryCondition, decision.ResetAt = policy.Limit, policy.ExceedAction, policy.RecoveryCondition, policy.ResetAt
		}
	}
	decision.Allowed = decision.Limit < 0 || current <= decision.Limit
	return decision, nil
}

// ConsumeQuotaForResource is the transaction-aware bridge used by governed
// resource mutations. The resource inherits its owning object's group (an
// HTTP/L4 rule inherits its agent group) and the caller's revision transaction
// also owns the quota counter and success audit event.
func (s *GormStore) ConsumeQuotaForResource(ctx context.Context, resourceKind, resourceID, ownerKind, ownerID, metric string, delta int64) (QuotaDecision, error) {
	actor, ok := QuotaActorFromContext(ctx)
	if !ok {
		return QuotaDecision{}, ErrQuotaActorRequired
	}
	var decision QuotaDecision
	err := s.SecurityTransaction(ctx, func(tx *GormStore) error {
		now := time.Now().UTC()
		groupID := "default"
		var ownerBinding ResourceBindingRow
		bindingErr := tx.db.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where("resource_kind = ? AND resource_id = ?", ownerKind, ownerID).First(&ownerBinding).Error
		if bindingErr == nil {
			groupID = ownerBinding.ResourceGroupID
		} else if !errors.Is(bindingErr, gorm.ErrRecordNotFound) {
			return bindingErr
		}
		if delta > 0 {
			var resourceBinding ResourceBindingRow
			resourceBindingErr := tx.db.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where("resource_kind = ? AND resource_id = ?", resourceKind, resourceID).First(&resourceBinding).Error
			switch {
			case resourceBindingErr == nil:
				groupID = resourceBinding.ResourceGroupID
			case errors.Is(resourceBindingErr, gorm.ErrRecordNotFound):
				binding := ResourceBindingRow{
					ID: securityID("res"), ResourceKind: resourceKind, ResourceID: resourceID, ResourceGroupID: groupID,
					ParentResourceKind: ownerKind, ParentResourceID: ownerID, UpdatedAt: now,
				}
				if err := tx.db.WithContext(ctx).Create(&binding).Error; err != nil {
					return err
				}
			default:
				return resourceBindingErr
			}
		}
		var allocations []QuotaAllocationRow
		if err := tx.db.WithContext(ctx).Where("resource_kind = ? AND resource_id = ? AND metric = ?", resourceKind, resourceID, metric).Find(&allocations).Error; err != nil {
			return err
		}
		if delta > 0 && len(allocations) > 0 {
			return nil
		}
		var scopes []quotaScope
		if delta <= 0 {
			for _, allocation := range allocations {
				scopes = append(scopes, quotaScope{SubjectKind: allocation.SubjectKind, SubjectID: allocation.SubjectID, ResourceGroupID: allocation.ResourceGroupID})
			}
		} else {
			if actor.Bootstrap {
				scopes = []quotaScope{{SubjectKind: "resource_group", SubjectID: groupID, ResourceGroupID: groupID}}
			} else {
				var err error
				scopes, err = tx.quotaScopesTx(tx.db.WithContext(ctx), actor.UserID, groupID)
				if err != nil {
					return err
				}
			}
		}
		if len(scopes) == 0 {
			decision = QuotaDecision{Metric: metric, ResourceGroupID: groupID, Limit: -1, Allowed: true}
		} else {
			var consumeErr error
			decision, consumeErr = tx.consumeQuotaScopesTx(tx.db.WithContext(ctx), scopes, metric, delta, now, true, false)
			if consumeErr != nil {
				return consumeErr
			}
		}
		if delta > 0 {
			rows := make([]QuotaAllocationRow, 0, len(scopes))
			for _, scope := range scopes {
				rows = append(rows, QuotaAllocationRow{
					ID: quotaAllocationID(resourceKind, resourceID, metric, scope), ResourceKind: resourceKind, ResourceID: resourceID, Metric: metric,
					SubjectKind: scope.SubjectKind, SubjectID: scope.SubjectID, ResourceGroupID: scope.ResourceGroupID,
					Amount: delta, CreatedAt: now,
				})
			}
			if len(rows) > 0 {
				if err := tx.db.WithContext(ctx).Create(&rows).Error; err != nil {
					return err
				}
			}
		} else if len(allocations) > 0 {
			if err := tx.db.WithContext(ctx).Where("resource_kind = ? AND resource_id = ? AND metric = ?", resourceKind, resourceID, metric).Delete(&QuotaAllocationRow{}).Error; err != nil {
				return err
			}
		}
		if delta <= 0 {
			if err := tx.db.WithContext(ctx).Where("resource_kind = ? AND resource_id = ?", resourceKind, resourceID).Delete(&ResourceBindingRow{}).Error; err != nil {
				return err
			}
		}
		metadata, _ := json.Marshal(map[string]any{"metric": metric, "delta": delta, "current": decision.Current, "limit": decision.Limit, "recovery_condition": decision.RecoveryCondition})
		return tx.AppendAuditEvent(ctx, AuditEventRow{
			ID: securityID("audit"), ActorID: actor.UserID, SessionID: actor.SessionID, Action: "quota.consume",
			TargetKind: resourceKind, TargetID: resourceID, ResourceGroupID: groupID, CorrelationID: actor.CorrelationID,
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
