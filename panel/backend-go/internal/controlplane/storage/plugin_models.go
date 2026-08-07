package storage

import "time"

type MarketplaceSourceRow struct {
	ID                    string    `gorm:"primaryKey;size:64"`
	Kind                  string    `gorm:"index;size:32;not null"`
	Name                  string    `gorm:"size:190;not null"`
	URL                   string    `gorm:"size:2048;not null"`
	Reference             string    `gorm:"size:512;not null"`
	CredentialRef         string    `gorm:"size:190;not null;default:''"`
	RefreshIntervalNS     int64     `gorm:"not null;default:0"`
	RiskLabel             string    `gorm:"size:190;not null;default:''"`
	CurrentSnapshotID     string    `gorm:"index;size:64;not null;default:''"`
	LastResult            string    `gorm:"size:32;not null;default:''"`
	LastError             string    `gorm:"type:text;not null"`
	UpdatedAt             time.Time `gorm:"not null"`
	RefreshLeaseToken     string    `gorm:"index;size:64;not null;default:''"`
	RefreshLeaseExpiresAt time.Time `gorm:"index"`
	LastCompletedAt       time.Time `gorm:"index"`
	Deleting              bool      `gorm:"index;not null;default:false"`
}

func (MarketplaceSourceRow) TableName() string { return "marketplace_sources" }

type MarketSnapshotRow struct {
	ID          string    `gorm:"primaryKey;size:64"`
	SourceID    string    `gorm:"index;size:64;not null"`
	Commit      string    `gorm:"size:128;not null"`
	Path        string    `gorm:"size:2048;not null"`
	EntriesJSON string    `gorm:"type:text;not null"`
	ValidatedAt time.Time `gorm:"index;not null"`
}

func (MarketSnapshotRow) TableName() string { return "market_snapshots" }

type MarketEntryRow struct {
	ID                string `gorm:"primaryKey;size:64"`
	SnapshotID        string `gorm:"uniqueIndex:idx_market_entry_version;size:64;not null"`
	PluginID          string `gorm:"uniqueIndex:idx_market_entry_version;size:190;not null"`
	Version           string `gorm:"uniqueIndex:idx_market_entry_version;size:64;not null"`
	Description       string `gorm:"type:text;not null"`
	CapabilitiesJSON  string `gorm:"type:text;not null"`
	CompatibilityJSON string `gorm:"type:text;not null"`
	PackagePath       string `gorm:"size:2048;not null"`
	PackageDigest     string `gorm:"index;size:64;not null"`
	Official          bool   `gorm:"not null;default:false"`
}

func (MarketEntryRow) TableName() string { return "market_entries" }

type MarketplaceRefreshOperationRow struct {
	ID             string     `gorm:"primaryKey;size:64"`
	SourceID       string     `gorm:"index;size:64;not null"`
	Commit         string     `gorm:"size:128;not null;default:''"`
	Status         string     `gorm:"index;size:32;not null"`
	ErrorClass     string     `gorm:"size:128;not null;default:''"`
	Error          string     `gorm:"type:text;not null"`
	DiffJSON       string     `gorm:"type:text;not null"`
	StartedAt      time.Time  `gorm:"index;not null"`
	FinishedAt     *time.Time `gorm:"index"`
	ActorID        string     `gorm:"index;size:64"`
	SessionID      string     `gorm:"index;size:64"`
	CorrelationID  string     `gorm:"index;size:128"`
	LeaseToken     string     `gorm:"index;size:64;not null;default:''"`
	LeaseExpiresAt time.Time  `gorm:"index"`
}

func (MarketplaceRefreshOperationRow) TableName() string { return "marketplace_refresh_operations" }

type PluginPackageRow struct {
	Digest           string    `gorm:"primaryKey;size:64"`
	PluginID         string    `gorm:"index;size:190;not null"`
	Version          string    `gorm:"index;size:64;not null"`
	CachePath        string    `gorm:"size:2048;not null"`
	ManifestJSON     string    `gorm:"type:text;not null"`
	ConfigSchemaJSON string    `gorm:"type:text;not null"`
	VerifiedAt       time.Time `gorm:"not null"`
}

func (PluginPackageRow) TableName() string { return "plugin_packages" }

type PluginPackageAcquisitionRow struct {
	SourceID   string    `gorm:"primaryKey;size:64"`
	Digest     string    `gorm:"primaryKey;size:64"`
	SnapshotID string    `gorm:"index;size:64;not null;default:''"`
	Status     string    `gorm:"index;size:32;not null"`
	UpdatedAt  time.Time `gorm:"not null"`
}

func (PluginPackageAcquisitionRow) TableName() string { return "plugin_package_acquisitions" }

type PluginPackageStagingRow struct {
	SourceID    string    `gorm:"primaryKey;size:64"`
	OperationID string    `gorm:"primaryKey;size:64"`
	Digest      string    `gorm:"primaryKey;size:64"`
	UpdatedAt   time.Time `gorm:"not null"`
}

func (PluginPackageStagingRow) TableName() string { return "plugin_package_staging" }

type PluginCacheGCIntentRow struct {
	SourceID       string    `gorm:"primaryKey;size:64"`
	Digest         string    `gorm:"primaryKey;size:64"`
	Status         string    `gorm:"index;size:32;not null"`
	Deferred       bool      `gorm:"index;not null;default:false"`
	ClaimToken     string    `gorm:"index;size:64;not null;default:''"`
	ClaimExpiresAt time.Time `gorm:"index"`
	QuarantinePath string    `gorm:"size:2048;not null;default:''"`
	LastError      string    `gorm:"type:text;not null"`
	UpdatedAt      time.Time `gorm:"not null"`
}

func (PluginCacheGCIntentRow) TableName() string { return "plugin_cache_gc_intents" }

type PluginDigestFenceRow struct {
	Digest         string    `gorm:"primaryKey;size:64"`
	ClaimToken     string    `gorm:"index;size:64;not null;default:''"`
	ClaimExpiresAt time.Time `gorm:"index"`
	UpdatedAt      time.Time `gorm:"not null"`
}

func (PluginDigestFenceRow) TableName() string { return "plugin_digest_fences" }

type MarketplaceSourceDeletionRow struct {
	SourceID          string    `gorm:"primaryKey;size:64"`
	SnapshotPathsJSON string    `gorm:"type:text;not null"`
	LastError         string    `gorm:"type:text;not null"`
	UpdatedAt         time.Time `gorm:"not null"`
}

func (MarketplaceSourceDeletionRow) TableName() string { return "marketplace_source_deletions" }

type MarketplaceDirectoryCleanupRow struct {
	ID          string    `gorm:"primaryKey;size:64"`
	SourceID    string    `gorm:"index;size:64;not null"`
	OperationID string    `gorm:"index;size:64;not null;default:''"`
	Path        string    `gorm:"uniqueIndex;size:2048;not null"`
	LastError   string    `gorm:"type:text;not null"`
	UpdatedAt   time.Time `gorm:"not null"`
}

func (MarketplaceDirectoryCleanupRow) TableName() string {
	return "marketplace_directory_cleanup"
}

type InstalledPluginRow struct {
	PluginID                string    `gorm:"primaryKey;size:190" json:"plugin_id"`
	ActivePackageDigest     string    `gorm:"index;size:64;not null" json:"active_package_digest"`
	ActiveSourceID          string    `gorm:"index;size:64;not null;default:''" json:"active_source_id,omitempty"`
	ActiveSourceKind        string    `gorm:"size:32;not null;default:''" json:"active_source_kind,omitempty"`
	ActiveSourceRiskLabel   string    `gorm:"size:190;not null;default:''" json:"active_source_risk_label,omitempty"`
	StagedPackageDigest     string    `gorm:"index;size:64;not null;default:''" json:"staged_package_digest,omitempty"`
	StagedSourceID          string    `gorm:"index;size:64;not null;default:''" json:"staged_source_id,omitempty"`
	StagedSourceKind        string    `gorm:"size:32;not null;default:''" json:"staged_source_kind,omitempty"`
	StagedSourceRiskLabel   string    `gorm:"size:190;not null;default:''" json:"staged_source_risk_label,omitempty"`
	RollbackPackageDigest   string    `gorm:"index;size:64;not null;default:''" json:"rollback_package_digest,omitempty"`
	RollbackSourceID        string    `gorm:"index;size:64;not null;default:''" json:"rollback_source_id,omitempty"`
	RollbackSourceKind      string    `gorm:"size:32;not null;default:''" json:"rollback_source_kind,omitempty"`
	RollbackSourceRiskLabel string    `gorm:"size:190;not null;default:''" json:"rollback_source_risk_label,omitempty"`
	DesiredLifecycle        string    `gorm:"index;size:32;not null" json:"desired_lifecycle"`
	CurrentLifecycle        string    `gorm:"index;size:32;not null" json:"current_lifecycle"`
	CleanupPolicyJSON       string    `gorm:"type:text;not null" json:"-"`
	LastOperationID         string    `gorm:"index;size:64;not null" json:"last_operation_id"`
	StateVersion            uint64    `gorm:"not null;default:1" json:"state_version"`
	PendingOperationID      string    `gorm:"index;size:64;not null;default:''" json:"pending_operation_id,omitempty"`
	PendingKind             string    `gorm:"size:32;not null;default:''" json:"pending_kind,omitempty"`
	PendingTargetDigest     string    `gorm:"size:64;not null;default:''" json:"pending_target_digest,omitempty"`
	PendingRevision         int64     `gorm:"not null;default:0" json:"pending_revision,omitempty"`
	InstalledAt             time.Time `gorm:"not null" json:"installed_at"`
	UpdatedAt               time.Time `gorm:"not null" json:"updated_at"`
}

func (InstalledPluginRow) TableName() string { return "installed_plugins" }

type PluginInstanceRow struct {
	ID                     string    `gorm:"primaryKey;size:64" json:"id"`
	PluginID               string    `gorm:"index;size:190;not null" json:"plugin_id"`
	ResourceGroupID        string    `gorm:"index;size:64;not null" json:"resource_group_id"`
	TargetJSON             string    `gorm:"type:text;not null" json:"targets"`
	ConfigJSON             string    `gorm:"type:text;not null" json:"config"`
	ConfigVersion          uint64    `gorm:"not null;default:0" json:"config_version"`
	PendingConfigJSON      string    `gorm:"type:text;not null" json:"pending_config,omitempty"`
	PendingVersion         uint64    `gorm:"not null;default:0" json:"pending_version,omitempty"`
	PendingOperationID     string    `gorm:"index;size:64;not null;default:''" json:"pending_operation_id,omitempty"`
	PendingResourceGroupID string    `gorm:"size:64;not null;default:''" json:"pending_resource_group_id,omitempty"`
	PendingTargetJSON      string    `gorm:"type:text;not null" json:"pending_targets,omitempty"`
	RollbackConfigJSON     string    `gorm:"type:text;not null" json:"-"`
	RollbackVersion        uint64    `gorm:"not null;default:0" json:"-"`
	DesiredEnabled         bool      `gorm:"not null;default:false" json:"desired_enabled"`
	CurrentState           string    `gorm:"index;size:32;not null" json:"current_state"`
	StatusSummaryJSON      string    `gorm:"type:text;not null" json:"status_summary"`
	StateVersion           uint64    `gorm:"not null;default:1" json:"state_version"`
	UpdatedAt              time.Time `gorm:"not null" json:"updated_at"`
}

func (PluginInstanceRow) TableName() string { return "plugin_instances" }

type PluginGrantRow struct {
	ID               string    `gorm:"primaryKey;size:64"`
	PluginID         string    `gorm:"uniqueIndex:idx_plugin_grant;size:190;not null"`
	PackageDigest    string    `gorm:"uniqueIndex:idx_plugin_grant;size:64;not null"`
	Permission       string    `gorm:"uniqueIndex:idx_plugin_grant;size:190;not null"`
	ResourceSelector string    `gorm:"uniqueIndex:idx_plugin_grant;size:512;not null;default:''"`
	GrantedBy        string    `gorm:"index;size:64;not null"`
	GrantedAt        time.Time `gorm:"not null"`
}

func (PluginGrantRow) TableName() string { return "plugin_grants" }

type PluginOperationRow struct {
	ID                  string     `gorm:"primaryKey;size:64" json:"id"`
	PluginID            string     `gorm:"index;size:190;not null" json:"plugin_id"`
	Kind                string     `gorm:"index;size:32;not null" json:"kind"`
	Status              string     `gorm:"index;size:32;not null" json:"status"`
	TargetPackageDigest string     `gorm:"size:64;not null;default:''" json:"target_package_digest,omitempty"`
	TargetRevision      int64      `gorm:"not null;default:0" json:"target_revision,omitempty"`
	AgentResultsJSON    string     `gorm:"type:text;not null" json:"agent_results"`
	ErrorClass          string     `gorm:"size:128;not null;default:''" json:"error_class,omitempty"`
	Error               string     `gorm:"type:text;not null" json:"error,omitempty"`
	ActorID             string     `gorm:"index;size:64;not null" json:"actor_id"`
	SessionID           string     `gorm:"index;size:64" json:"session_id,omitempty"`
	CorrelationID       string     `gorm:"index;size:128" json:"correlation_id,omitempty"`
	SourceID            string     `gorm:"index;size:64;not null;default:''" json:"source_id,omitempty"`
	SourceKind          string     `gorm:"index;size:32;not null;default:''" json:"source_kind,omitempty"`
	SourceRiskLabel     string     `gorm:"size:190;not null;default:''" json:"source_risk_label,omitempty"`
	CreatedAt           time.Time  `gorm:"index;not null" json:"created_at"`
	CompletedAt         *time.Time `gorm:"index" json:"completed_at,omitempty"`
}

func (PluginOperationRow) TableName() string { return "plugin_operations" }
