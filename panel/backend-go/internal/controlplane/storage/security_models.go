package storage

import "time"

// Access-control rows are intentionally kept in the control-plane database.
// Plugins refer to these rows by stable IDs instead of maintaining parallel
// identity, resource-scope, quota, or audit stores.
type UserRow struct {
	ID           string    `gorm:"primaryKey;size:64"`
	Username     string    `gorm:"uniqueIndex;size:190;not null"`
	DisplayName  string    `gorm:"size:255"`
	PasswordHash string    `gorm:"size:255;not null"`
	Disabled     bool      `gorm:"not null;default:false"`
	AuthRevision uint64    `gorm:"not null;default:1"`
	CreatedAt    time.Time `gorm:"not null"`
	UpdatedAt    time.Time `gorm:"not null"`
}

type SessionRow struct {
	ID        string     `gorm:"primaryKey;size:64"`
	TokenHash string     `gorm:"uniqueIndex;size:64;not null"`
	UserID    string     `gorm:"index;size:64;not null"`
	CreatedAt time.Time  `gorm:"not null"`
	ExpiresAt time.Time  `gorm:"index;not null"`
	LastSeen  time.Time  `gorm:"not null"`
	RevokedAt *time.Time `gorm:"index"`
}

type RoleRow struct {
	ID          string    `gorm:"primaryKey;size:64"`
	Name        string    `gorm:"uniqueIndex;size:190;not null"`
	Description string    `gorm:"size:500"`
	Builtin     bool      `gorm:"not null;default:false"`
	Revision    uint64    `gorm:"not null;default:1"`
	CreatedAt   time.Time `gorm:"not null"`
	UpdatedAt   time.Time `gorm:"not null"`
}

type PermissionRow struct {
	ID          string `gorm:"primaryKey;size:190" json:"id"`
	Description string `gorm:"size:500" json:"description"`
}

type RolePermissionRow struct {
	RoleID       string `gorm:"primaryKey;size:64"`
	PermissionID string `gorm:"primaryKey;size:190"`
}

type RoleBindingRow struct {
	ID        string    `gorm:"primaryKey;size:64"`
	UserID    string    `gorm:"uniqueIndex:idx_role_binding;size:64;not null"`
	RoleID    string    `gorm:"uniqueIndex:idx_role_binding;size:64;not null"`
	CreatedAt time.Time `gorm:"not null"`
}

type ResourceGroupRow struct {
	ID          string    `gorm:"primaryKey;size:64"`
	Name        string    `gorm:"uniqueIndex;size:190;not null"`
	Description string    `gorm:"size:500"`
	Builtin     bool      `gorm:"not null;default:false"`
	CreatedAt   time.Time `gorm:"not null"`
	UpdatedAt   time.Time `gorm:"not null"`
}

type ResourceGroupGrantRow struct {
	ID              string    `gorm:"primaryKey;size:64"`
	SubjectKind     string    `gorm:"uniqueIndex:idx_group_grant;size:16;not null"`
	SubjectID       string    `gorm:"uniqueIndex:idx_group_grant;size:64;not null"`
	ResourceGroupID string    `gorm:"uniqueIndex:idx_group_grant;size:64;not null"`
	CreatedAt       time.Time `gorm:"not null"`
}

type ResourceBindingRow struct {
	ID                 string    `gorm:"primaryKey;size:64"`
	ResourceKind       string    `gorm:"uniqueIndex:idx_resource_owner;size:64;not null"`
	ResourceID         string    `gorm:"uniqueIndex:idx_resource_owner;size:190;not null"`
	ResourceGroupID    string    `gorm:"index;size:64;not null"`
	ParentResourceKind string    `gorm:"index:idx_resource_binding_parent;size:64"`
	ParentResourceID   string    `gorm:"index:idx_resource_binding_parent;size:190"`
	UpdatedAt          time.Time `gorm:"not null"`
}

type QuotaPolicyRow struct {
	ID                string     `gorm:"primaryKey;size:64" json:"id"`
	SubjectKind       string     `gorm:"index:idx_quota_lookup;size:16;not null" json:"subject_kind"`
	SubjectID         string     `gorm:"index:idx_quota_lookup;size:64;not null" json:"subject_id"`
	ResourceGroupID   string     `gorm:"index:idx_quota_lookup;size:64" json:"resource_group_id,omitempty"`
	Metric            string     `gorm:"index:idx_quota_lookup;size:64;not null" json:"metric"`
	Limit             int64      `gorm:"not null" json:"limit"`
	ExceedAction      string     `gorm:"size:32;not null;default:reject" json:"exceed_action"`
	RecoveryCondition string     `gorm:"size:255" json:"recovery_condition"`
	ResetAt           *time.Time `gorm:"index" json:"reset_at,omitempty"`
	CreatedAt         time.Time  `gorm:"not null" json:"created_at"`
	UpdatedAt         time.Time  `gorm:"not null" json:"updated_at"`
}

type QuotaUsageRow struct {
	ID              string     `gorm:"primaryKey;size:64" json:"id"`
	SubjectKind     string     `gorm:"uniqueIndex:idx_quota_usage_scope;size:16;not null" json:"subject_kind"`
	SubjectID       string     `gorm:"uniqueIndex:idx_quota_usage_scope;size:64;not null" json:"subject_id"`
	ResourceGroupID string     `gorm:"uniqueIndex:idx_quota_usage_scope;size:64;not null" json:"resource_group_id,omitempty"`
	Metric          string     `gorm:"uniqueIndex:idx_quota_usage_scope;size:64;not null" json:"metric"`
	Current         int64      `gorm:"not null;default:0" json:"current"`
	ResetAt         *time.Time `gorm:"index" json:"reset_at,omitempty"`
	UpdatedAt       time.Time  `gorm:"not null" json:"updated_at"`
}

// QuotaPolicyUsageRow keeps resettable metrics isolated per policy window.
// Count metrics remain derived from durable QuotaAllocationRow records.
type QuotaPolicyUsageRow struct {
	ID              string     `gorm:"primaryKey;size:64" json:"id"`
	PolicyID        string     `gorm:"uniqueIndex:idx_quota_policy_usage;size:64;not null" json:"policy_id"`
	ResourceGroupID string     `gorm:"uniqueIndex:idx_quota_policy_usage;size:64;not null" json:"resource_group_id,omitempty"`
	Current         int64      `gorm:"not null;default:0" json:"current"`
	ResetAt         *time.Time `gorm:"index" json:"reset_at,omitempty"`
	UpdatedAt       time.Time  `gorm:"not null" json:"updated_at"`
}

type QuotaAllocationRow struct {
	ID              string    `gorm:"primaryKey;size:64"`
	ResourceKind    string    `gorm:"uniqueIndex:idx_quota_allocation;size:64;not null"`
	ResourceID      string    `gorm:"uniqueIndex:idx_quota_allocation;size:190;not null"`
	Metric          string    `gorm:"uniqueIndex:idx_quota_allocation;size:64;not null"`
	SubjectKind     string    `gorm:"uniqueIndex:idx_quota_allocation;size:16;not null"`
	SubjectID       string    `gorm:"uniqueIndex:idx_quota_allocation;size:64;not null"`
	ResourceGroupID string    `gorm:"uniqueIndex:idx_quota_allocation;size:64;not null"`
	Amount          int64     `gorm:"not null"`
	CreatedAt       time.Time `gorm:"not null"`
}

type AuditEventRow struct {
	ID              string    `gorm:"primaryKey;size:64"`
	ActorID         string    `gorm:"index;size:64"`
	SessionID       string    `gorm:"index;size:64"`
	Action          string    `gorm:"index;size:190;not null"`
	TargetKind      string    `gorm:"index;size:64"`
	TargetID        string    `gorm:"index;size:190"`
	ResourceGroupID string    `gorm:"index;size:64"`
	CorrelationID   string    `gorm:"index;size:128"`
	Result          string    `gorm:"index;size:32;not null"`
	ErrorClass      string    `gorm:"size:128"`
	MetadataJSON    string    `gorm:"type:text;not null"`
	CreatedAt       time.Time `gorm:"index;not null"`
}

type SecretRow struct {
	ID              string    `gorm:"primaryKey;size:64"`
	Name            string    `gorm:"uniqueIndex;size:190;not null"`
	Purpose         string    `gorm:"index;size:190"`
	OwnerUserID     string    `gorm:"index;size:64"`
	ResourceGroupID string    `gorm:"index;size:64;not null"`
	ActiveVersion   uint64    `gorm:"not null"`
	Fingerprint     string    `gorm:"size:32;not null"`
	CreatedAt       time.Time `gorm:"not null"`
	RotatedAt       time.Time `gorm:"not null"`
	LastUsedAt      *time.Time
	RetiredAt       *time.Time `gorm:"index"`
}

type SecretVersionRow struct {
	SecretID    string    `gorm:"primaryKey;size:64"`
	Version     uint64    `gorm:"primaryKey"`
	KeyID       string    `gorm:"size:64;not null"`
	Nonce       []byte    `gorm:"not null"`
	Ciphertext  []byte    `gorm:"not null"`
	CreatedAt   time.Time `gorm:"not null"`
	DestroyedAt *time.Time
}
