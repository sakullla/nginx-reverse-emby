package storage

import "time"

type MarketplaceSourceRow struct {
	ID                    string    `gorm:"primaryKey;size:64"`
	Kind                  string    `gorm:"index;size:32;not null"`
	Purpose               string    `gorm:"index;size:16;not null;default:'market'"`
	Name                  string    `gorm:"size:190;not null"`
	NameKey               string    `gorm:"size:190;not null;default:''"`
	URL                   string    `gorm:"size:2048;not null"`
	RefKind               string    `gorm:"index;size:16;not null;default:'branch'"`
	RefName               string    `gorm:"size:512;not null"`
	ConfigRevision        uint64    `gorm:"not null;default:1"`
	CurrentResolvedOID    string    `gorm:"column:current_resolved_oid;size:128;not null;default:''"`
	CredentialRef         string    `gorm:"size:190;not null;default:''"`
	SignerKeyID           string    `gorm:"size:190;not null;default:''"`
	SignerSecretRef       string    `gorm:"size:190;not null;default:''"`
	SignerPublicKey       string    `gorm:"size:64;not null;default:''"`
	SignerFingerprint     string    `gorm:"size:64;not null;default:''"`
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
	ID               string    `gorm:"primaryKey;size:64"`
	SourceID         string    `gorm:"index;size:64;not null"`
	Commit           string    `gorm:"size:128;not null"`
	SourceRevision   uint64    `gorm:"not null;default:1"`
	RefKind          string    `gorm:"size:16;not null;default:'branch'"`
	RefName          string    `gorm:"size:512;not null;default:''"`
	Path             string    `gorm:"size:2048;not null"`
	EntriesJSON      string    `gorm:"type:text;not null"`
	DirectPluginJSON string    `gorm:"type:text;not null;default:''"`
	ValidatedAt      time.Time `gorm:"index;not null"`
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
	RuntimeKind       string `gorm:"index;size:32;not null;default:''"`
	RuntimeABI        string `gorm:"size:128;not null;default:''"`
	HostScope         string `gorm:"index;size:32;not null;default:''"`
	PolicyKind        string `gorm:"index;size:16;not null;default:''"`
	ArtifactsJSON     string `gorm:"type:text;not null;default:''"`
	PackagePath       string `gorm:"size:2048;not null"`
	PackageDigest     string `gorm:"index;size:64;not null"`
	SignatureKeyID    string `gorm:"size:190;not null;default:''"`
	Provenance        string `gorm:"size:190;not null;default:''"`
	Official          bool   `gorm:"not null;default:false"`
}

func (MarketEntryRow) TableName() string { return "market_entries" }

type MarketplaceRefreshOperationRow struct {
	ID                string     `gorm:"primaryKey;size:64"`
	SourceID          string     `gorm:"index;size:64;not null"`
	Commit            string     `gorm:"size:128;not null;default:''"`
	SourceRevision    uint64     `gorm:"not null;default:1"`
	RefKind           string     `gorm:"size:16;not null;default:'branch'"`
	RefName           string     `gorm:"size:512;not null;default:''"`
	SignerSourceKind  string     `gorm:"size:32;not null;default:''"`
	SignerKeyID       string     `gorm:"size:190;not null;default:''"`
	SignerPublicKey   string     `gorm:"size:64;not null;default:''"`
	SignerFingerprint string     `gorm:"size:64;not null;default:''"`
	Status            string     `gorm:"index;size:32;not null"`
	ErrorClass        string     `gorm:"size:128;not null;default:''"`
	Error             string     `gorm:"type:text;not null"`
	DiffJSON          string     `gorm:"type:text;not null"`
	StartedAt         time.Time  `gorm:"index;not null"`
	FinishedAt        *time.Time `gorm:"index"`
	ActorID           string     `gorm:"index;size:64"`
	SessionID         string     `gorm:"index;size:64"`
	CorrelationID     string     `gorm:"index;size:128"`
	LeaseToken        string     `gorm:"index;size:64;not null;default:''"`
	LeaseExpiresAt    time.Time  `gorm:"index"`
}

func (MarketplaceRefreshOperationRow) TableName() string { return "marketplace_refresh_operations" }

type PluginPackageRow struct {
	Identity             string    `gorm:"primaryKey;size:64"`
	Digest               string    `gorm:"index;size:64;not null"`
	PluginID             string    `gorm:"index;size:190;not null"`
	Version              string    `gorm:"index;size:64;not null"`
	RuntimeKind          string    `gorm:"index;size:32;not null;default:''"`
	RuntimeABI           string    `gorm:"size:128;not null;default:''"`
	HostScope            string    `gorm:"index;size:32;not null;default:''"`
	PolicyKind           string    `gorm:"index;size:16;not null;default:''"`
	EntryPath            string    `gorm:"size:2048;not null;default:''"`
	SignatureKeyID       string    `gorm:"size:190;not null;default:''"`
	SignaturePublicKey   string    `gorm:"size:64;not null;default:''"`
	SignatureFingerprint string    `gorm:"size:64;not null;default:''"`
	SourceID             string    `gorm:"index;size:64;not null;default:''"`
	SourceKind           string    `gorm:"index;size:32;not null;default:''"`
	SourceRiskLabel      string    `gorm:"size:190;not null;default:''"`
	SourceRevision       uint64    `gorm:"not null;default:0"`
	SourceRefKind        string    `gorm:"size:16;not null;default:''"`
	SourceRefName        string    `gorm:"size:512;not null;default:''"`
	SourceResolvedOID    string    `gorm:"column:source_resolved_oid;size:128;not null;default:''"`
	SignatureVerdict     string    `gorm:"size:32;not null;default:''"`
	ResourceBudgetJSON   string    `gorm:"type:text;not null;default:''"`
	FailurePolicyJSON    string    `gorm:"type:text;not null;default:''"`
	CachePath            string    `gorm:"size:2048;not null"`
	ManifestJSON         string    `gorm:"type:text;not null"`
	ConfigSchemaJSON     string    `gorm:"type:text;not null"`
	UISchemaJSON         string    `gorm:"type:text;not null;default:''"`
	VerifiedAt           time.Time `gorm:"not null"`
}

func (PluginPackageRow) TableName() string { return "plugin_packages" }

// PluginArtifactRow is the immutable runtime projection of a verified package
// artifact. Runtime paths are deliberately not persisted here: the cache stays
// non-executable and a later installer must materialize an execution copy.
type PluginArtifactRow struct {
	ID              string `gorm:"primaryKey;size:64"`
	PackageIdentity string `gorm:"index;size:64;not null;default:''"`
	PackageDigest   string `gorm:"index;size:64;not null"`
	Path            string `gorm:"size:2048;not null"`
	SHA256          string `gorm:"index;size:64;not null"`
	SizeBytes       int64  `gorm:"not null"`
	Mode            string `gorm:"size:16;not null"`
	RuntimeKind     string `gorm:"index;size:32;not null"`
	RuntimeABI      string `gorm:"size:128;not null"`
	HostScope       string `gorm:"index;size:32;not null"`
	GOOS            string `gorm:"index;size:32;not null;default:''"`
	GOARCH          string `gorm:"index;size:32;not null;default:''"`
}

func (PluginArtifactRow) TableName() string { return "plugin_artifacts" }

type PluginPackageAcquisitionRow struct {
	SourceID             string    `gorm:"primaryKey;size:64"`
	Digest               string    `gorm:"primaryKey;size:64"`
	SnapshotID           string    `gorm:"index;size:64;not null;default:''"`
	SourceRevision       uint64    `gorm:"not null;default:0"`
	RefKind              string    `gorm:"size:16;not null;default:''"`
	RefName              string    `gorm:"size:512;not null;default:''"`
	ResolvedOID          string    `gorm:"column:resolved_oid;size:128;not null;default:''"`
	SourceKind           string    `gorm:"index;size:32;not null;default:''"`
	SignatureKeyID       string    `gorm:"size:190;not null;default:''"`
	SignaturePublicKey   string    `gorm:"size:64;not null;default:''"`
	SignatureFingerprint string    `gorm:"size:64;not null;default:''"`
	Status               string    `gorm:"index;size:32;not null"`
	UpdatedAt            time.Time `gorm:"not null"`
}

func (PluginPackageAcquisitionRow) TableName() string { return "plugin_package_acquisitions" }

type PluginPackageStagingRow struct {
	SourceID          string    `gorm:"primaryKey;size:64"`
	OperationID       string    `gorm:"primaryKey;size:64"`
	Digest            string    `gorm:"primaryKey;size:64"`
	SignerFingerprint string    `gorm:"index;size:64;not null;default:''"`
	UpdatedAt         time.Time `gorm:"not null"`
}

func (PluginPackageStagingRow) TableName() string { return "plugin_package_staging" }

type PluginCacheGCIntentRow struct {
	SourceID          string    `gorm:"primaryKey;size:64"`
	Digest            string    `gorm:"primaryKey;size:64"`
	SignerFingerprint string    `gorm:"primaryKey;size:64"`
	SignerSourceKind  string    `gorm:"size:32;not null;default:''"`
	SignerKeyID       string    `gorm:"size:190;not null;default:''"`
	SignerPublicKey   string    `gorm:"size:64;not null;default:''"`
	Status            string    `gorm:"index;size:32;not null"`
	Deferred          bool      `gorm:"index;not null;default:false"`
	ClaimToken        string    `gorm:"index;size:64;not null;default:''"`
	ClaimExpiresAt    time.Time `gorm:"index"`
	QuarantineID      string    `gorm:"index;size:64;not null;default:''"`
	QuarantinePath    string    `gorm:"size:2048;not null;default:''"`
	ObjectsPrepared   bool      `gorm:"not null;default:false"`
	CacheObjectsJSON  string    `gorm:"type:text;not null;default:'[]'"`
	LastError         string    `gorm:"type:text;not null"`
	UpdatedAt         time.Time `gorm:"not null"`
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
	ID             string    `gorm:"primaryKey;size:64"`
	SourceID       string    `gorm:"index;size:64;not null"`
	OperationID    string    `gorm:"index;size:64;not null;default:''"`
	Path           string    `gorm:"size:2048;not null"`
	PathDigest     string    `gorm:"uniqueIndex;size:64;not null;default:''"`
	State          string    `gorm:"index;size:32;not null;default:'retired'"`
	ClaimToken     string    `gorm:"index;size:64;not null;default:''"`
	ClaimExpiresAt time.Time `gorm:"index"`
	LastError      string    `gorm:"type:text;not null"`
	UpdatedAt      time.Time `gorm:"not null"`
}

func (MarketplaceDirectoryCleanupRow) TableName() string {
	return "marketplace_directory_cleanup"
}

type InstalledPluginRow struct {
	PluginID                     string    `gorm:"primaryKey;size:190" json:"plugin_id"`
	ActivePackageDigest          string    `gorm:"index;size:64;not null" json:"active_package_digest"`
	ActivePackageIdentity        string    `gorm:"index;size:64;not null;default:''" json:"-"`
	RuntimeKind                  string    `gorm:"index;size:32;not null;default:''" json:"runtime_kind"`
	RuntimeABI                   string    `gorm:"size:128;not null;default:''" json:"runtime_abi"`
	HostScope                    string    `gorm:"index;size:32;not null;default:''" json:"host_scope"`
	ActiveSourceID               string    `gorm:"index;size:64;not null;default:''" json:"active_source_id,omitempty"`
	ActiveSourceKind             string    `gorm:"size:32;not null;default:''" json:"active_source_kind,omitempty"`
	ActiveSourceRiskLabel        string    `gorm:"size:190;not null;default:''" json:"active_source_risk_label,omitempty"`
	ActiveSourceRevision         uint64    `gorm:"not null;default:0" json:"active_source_revision,omitempty"`
	ActiveSourceRefKind          string    `gorm:"size:16;not null;default:''" json:"active_source_ref_kind,omitempty"`
	ActiveSourceRefName          string    `gorm:"size:512;not null;default:''" json:"active_source_ref_name,omitempty"`
	ActiveSourceResolvedOID      string    `gorm:"column:active_source_resolved_oid;size:128;not null;default:''" json:"active_source_resolved_oid,omitempty"`
	ActiveSignatureKeyID         string    `gorm:"size:190;not null;default:''" json:"-"`
	ActiveSignaturePublicKey     string    `gorm:"size:64;not null;default:''" json:"-"`
	ActiveSignatureFingerprint   string    `gorm:"size:64;not null;default:''" json:"-"`
	StagedPackageDigest          string    `gorm:"index;size:64;not null;default:''" json:"staged_package_digest,omitempty"`
	StagedPackageIdentity        string    `gorm:"index;size:64;not null;default:''" json:"-"`
	StagedSourceID               string    `gorm:"index;size:64;not null;default:''" json:"staged_source_id,omitempty"`
	StagedSourceKind             string    `gorm:"size:32;not null;default:''" json:"staged_source_kind,omitempty"`
	StagedSourceRiskLabel        string    `gorm:"size:190;not null;default:''" json:"staged_source_risk_label,omitempty"`
	StagedSourceRevision         uint64    `gorm:"not null;default:0" json:"staged_source_revision,omitempty"`
	StagedSourceRefKind          string    `gorm:"size:16;not null;default:''" json:"staged_source_ref_kind,omitempty"`
	StagedSourceRefName          string    `gorm:"size:512;not null;default:''" json:"staged_source_ref_name,omitempty"`
	StagedSourceResolvedOID      string    `gorm:"column:staged_source_resolved_oid;size:128;not null;default:''" json:"staged_source_resolved_oid,omitempty"`
	StagedSignatureKeyID         string    `gorm:"size:190;not null;default:''" json:"-"`
	StagedSignaturePublicKey     string    `gorm:"size:64;not null;default:''" json:"-"`
	StagedSignatureFingerprint   string    `gorm:"size:64;not null;default:''" json:"-"`
	RollbackPackageDigest        string    `gorm:"index;size:64;not null;default:''" json:"rollback_package_digest,omitempty"`
	RollbackPackageIdentity      string    `gorm:"index;size:64;not null;default:''" json:"-"`
	RollbackSourceID             string    `gorm:"index;size:64;not null;default:''" json:"rollback_source_id,omitempty"`
	RollbackSourceKind           string    `gorm:"size:32;not null;default:''" json:"rollback_source_kind,omitempty"`
	RollbackSourceRiskLabel      string    `gorm:"size:190;not null;default:''" json:"rollback_source_risk_label,omitempty"`
	RollbackSourceRevision       uint64    `gorm:"not null;default:0" json:"rollback_source_revision,omitempty"`
	RollbackSourceRefKind        string    `gorm:"size:16;not null;default:''" json:"rollback_source_ref_kind,omitempty"`
	RollbackSourceRefName        string    `gorm:"size:512;not null;default:''" json:"rollback_source_ref_name,omitempty"`
	RollbackSourceResolvedOID    string    `gorm:"column:rollback_source_resolved_oid;size:128;not null;default:''" json:"rollback_source_resolved_oid,omitempty"`
	RollbackSignatureKeyID       string    `gorm:"size:190;not null;default:''" json:"-"`
	RollbackSignaturePublicKey   string    `gorm:"size:64;not null;default:''" json:"-"`
	RollbackSignatureFingerprint string    `gorm:"size:64;not null;default:''" json:"-"`
	DesiredLifecycle             string    `gorm:"index;size:32;not null" json:"desired_lifecycle"`
	CurrentLifecycle             string    `gorm:"index;size:32;not null" json:"current_lifecycle"`
	CleanupPolicyJSON            string    `gorm:"type:text;not null" json:"-"`
	LastOperationID              string    `gorm:"index;size:64;not null" json:"last_operation_id"`
	StateVersion                 uint64    `gorm:"not null;default:1" json:"state_version"`
	PendingOperationID           string    `gorm:"index;size:64;not null;default:''" json:"pending_operation_id,omitempty"`
	PendingKind                  string    `gorm:"size:32;not null;default:''" json:"pending_kind,omitempty"`
	PendingTargetDigest          string    `gorm:"size:64;not null;default:''" json:"pending_target_digest,omitempty"`
	PendingTargetIdentity        string    `gorm:"size:64;not null;default:''" json:"-"`
	PendingRevision              int64     `gorm:"not null;default:0" json:"pending_revision,omitempty"`
	PendingGrantsJSON            string    `gorm:"type:text;not null;default:'[]'" json:"-"`
	InstalledAt                  time.Time `gorm:"not null" json:"installed_at"`
	UpdatedAt                    time.Time `gorm:"not null" json:"updated_at"`
}

func (InstalledPluginRow) TableName() string { return "installed_plugins" }

type PluginInstanceRow struct {
	ID                        string    `gorm:"primaryKey;size:64" json:"id"`
	PluginID                  string    `gorm:"index;size:190;not null" json:"plugin_id"`
	ResourceGroupID           string    `gorm:"index;size:64;not null" json:"resource_group_id"`
	TargetJSON                string    `gorm:"type:text;not null" json:"targets"`
	PolicyChainsJSON          string    `gorm:"type:text;not null;default:'[]'" json:"policy_chains"`
	SecretHandlesJSON         string    `gorm:"type:text;not null;default:'[]'" json:"-"`
	BindingsJSON              string    `gorm:"type:text;not null;default:'[]'" json:"-"`
	ConfigJSON                string    `gorm:"type:text;not null" json:"config"`
	ConfigVersion             uint64    `gorm:"not null;default:0" json:"config_version"`
	PendingConfigJSON         string    `gorm:"type:text;not null" json:"pending_config,omitempty"`
	PendingVersion            uint64    `gorm:"not null;default:0" json:"pending_version,omitempty"`
	PendingOperationID        string    `gorm:"index;size:64;not null;default:''" json:"pending_operation_id,omitempty"`
	PendingResourceGroupID    string    `gorm:"size:64;not null;default:''" json:"pending_resource_group_id,omitempty"`
	PendingTargetJSON         string    `gorm:"type:text;not null" json:"pending_targets,omitempty"`
	PendingPolicyChainsJSON   string    `gorm:"type:text;not null;default:'[]'" json:"pending_policy_chains,omitempty"`
	PendingBindingsJSON       string    `gorm:"type:text;not null;default:'[]'" json:"-"`
	PendingSecretHandlesJSON  string    `gorm:"type:text;not null;default:'[]'" json:"-"`
	RollbackConfigJSON        string    `gorm:"type:text;not null" json:"-"`
	RollbackVersion           uint64    `gorm:"not null;default:0" json:"-"`
	RollbackResourceGroupID   string    `gorm:"size:64;not null;default:''" json:"-"`
	RollbackPolicyChainsJSON  string    `gorm:"type:text;not null;default:'[]'" json:"-"`
	RollbackBindingsJSON      string    `gorm:"type:text;not null;default:'[]'" json:"-"`
	RollbackSecretHandlesJSON string    `gorm:"type:text;not null;default:'[]'" json:"-"`
	DesiredEnabled            bool      `gorm:"not null;default:false" json:"desired_enabled"`
	CurrentState              string    `gorm:"index;size:32;not null" json:"current_state"`
	StatusSummaryJSON         string    `gorm:"type:text;not null" json:"status_summary"`
	StateVersion              uint64    `gorm:"not null;default:1" json:"state_version"`
	UpdatedAt                 time.Time `gorm:"not null" json:"updated_at"`
}

func (PluginInstanceRow) TableName() string { return "plugin_instances" }

// PluginPolicyAgentRevisionRow owns the globally monotonic policy-catalog
// revision space for one Agent. It is deliberately independent from plugin,
// package, instance, and grant-local row versions.
type PluginPolicyAgentRevisionRow struct {
	AgentID   string    `gorm:"primaryKey;size:64"`
	Revision  int64     `gorm:"not null;default:0"`
	UpdatedAt time.Time `gorm:"not null"`
}

func (PluginPolicyAgentRevisionRow) TableName() string { return "plugin_policy_agent_revisions" }

type PluginGrantRow struct {
	ID               string    `gorm:"primaryKey;size:64" json:"id"`
	GrantKey         string    `gorm:"uniqueIndex;size:64;not null;default:''" json:"grant_key"`
	PluginID         string    `gorm:"index;size:190;not null" json:"plugin_id"`
	PackageDigest    string    `gorm:"index;size:64;not null" json:"package_digest"`
	PackageIdentity  string    `gorm:"index;size:64;not null;default:''" json:"-"`
	Permission       string    `gorm:"size:190;not null" json:"permission"`
	ResourceSelector string    `gorm:"size:512;not null;default:''" json:"resource_selector,omitempty"`
	GrantedBy        string    `gorm:"index;size:64;not null" json:"granted_by"`
	GrantedAt        time.Time `gorm:"not null" json:"granted_at"`
}

func (PluginGrantRow) TableName() string { return "plugin_grants" }

type PluginOperationRow struct {
	ID                         string     `gorm:"primaryKey;size:64" json:"id"`
	PluginID                   string     `gorm:"index;size:190;not null" json:"plugin_id"`
	InstanceID                 string     `gorm:"index;size:64;not null;default:''" json:"instance_id,omitempty"`
	ResourceGroupID            string     `gorm:"index;size:64;not null;default:''" json:"resource_group_id,omitempty"`
	Kind                       string     `gorm:"index;size:32;not null" json:"kind"`
	Status                     string     `gorm:"index;size:32;not null" json:"status"`
	TargetPackageDigest        string     `gorm:"size:64;not null;default:''" json:"target_package_digest,omitempty"`
	TargetPackageIdentity      string     `gorm:"size:64;not null;default:''" json:"-"`
	TargetSignatureKeyID       string     `gorm:"size:190;not null;default:''" json:"-"`
	TargetSignaturePublicKey   string     `gorm:"size:64;not null;default:''" json:"-"`
	TargetSignatureFingerprint string     `gorm:"size:64;not null;default:''" json:"-"`
	TargetRevision             int64      `gorm:"not null;default:0" json:"target_revision,omitempty"`
	AgentResultsJSON           string     `gorm:"type:text;not null" json:"agent_results"`
	ErrorClass                 string     `gorm:"size:128;not null;default:''" json:"error_class,omitempty"`
	Error                      string     `gorm:"type:text;not null" json:"error,omitempty"`
	ActorID                    string     `gorm:"index;size:64;not null" json:"actor_id"`
	SessionID                  string     `gorm:"index;size:64" json:"session_id,omitempty"`
	CorrelationID              string     `gorm:"index;size:128" json:"correlation_id,omitempty"`
	SourceID                   string     `gorm:"index;size:64;not null;default:''" json:"source_id,omitempty"`
	SourceKind                 string     `gorm:"index;size:32;not null;default:''" json:"source_kind,omitempty"`
	SourceRiskLabel            string     `gorm:"size:190;not null;default:''" json:"source_risk_label,omitempty"`
	SourceRevision             uint64     `gorm:"not null;default:0" json:"source_revision,omitempty"`
	SourceRefKind              string     `gorm:"size:16;not null;default:''" json:"source_ref_kind,omitempty"`
	SourceRefName              string     `gorm:"size:512;not null;default:''" json:"source_ref_name,omitempty"`
	SourceResolvedOID          string     `gorm:"column:source_resolved_oid;size:128;not null;default:''" json:"source_resolved_oid,omitempty"`
	CreatedAt                  time.Time  `gorm:"index;not null" json:"created_at"`
	CompletedAt                *time.Time `gorm:"index" json:"completed_at,omitempty"`
}

func (PluginOperationRow) TableName() string { return "plugin_operations" }

// PluginOperationScopeRow is the immutable resource ownership projection for
// one lifecycle operation. A plugin-wide operation has one row per affected
// instance so scoped readers never infer ownership from mutable instances.
type PluginOperationScopeRow struct {
	OperationID     string    `gorm:"primaryKey;size:64" json:"operation_id"`
	InstanceID      string    `gorm:"primaryKey;size:64" json:"instance_id"`
	PluginID        string    `gorm:"index;size:190;not null" json:"plugin_id"`
	ResourceGroupID string    `gorm:"index;size:64;not null" json:"resource_group_id"`
	CreatedAt       time.Time `gorm:"not null" json:"created_at"`
}

func (PluginOperationScopeRow) TableName() string { return "plugin_operation_scopes" }

// PluginOperationSecretRow records staging and retirement without retaining
// plaintext. State advances from staged to active or retired transactionally.
type PluginOperationSecretRow struct {
	OperationID string     `gorm:"primaryKey;size:64"`
	SecretID    string     `gorm:"primaryKey;size:64"`
	InstanceID  string     `gorm:"index;size:64;not null"`
	Role        string     `gorm:"size:16;not null"`
	State       string     `gorm:"index;size:16;not null"`
	CreatedAt   time.Time  `gorm:"not null"`
	RetiredAt   *time.Time `gorm:"index"`
}

func (PluginOperationSecretRow) TableName() string { return "plugin_operation_secrets" }

// PluginAgentRuntimeStatusRow is the durable, ownership-fenced view of one
// target Agent applying one plugin instance operation. Historical operations
// remain queryable and cannot be advanced by a report for another generation.
type PluginAgentRuntimeStatusRow struct {
	OperationID     string     `gorm:"primaryKey;size:64" json:"operation_id"`
	AgentID         string     `gorm:"primaryKey;size:64" json:"agent_id"`
	InstanceID      string     `gorm:"primaryKey;size:64" json:"instance_id"`
	PluginID        string     `gorm:"index;size:190;not null" json:"plugin_id"`
	ResourceGroupID string     `gorm:"index;size:64;not null;default:''" json:"resource_group_id"`
	TargetVersion   uint64     `gorm:"not null;default:0" json:"target_version"`
	AuthoritySlot   string     `gorm:"index;size:16;not null;default:''" json:"-"`
	Revision        int64      `gorm:"index;not null" json:"revision"`
	GenerationID    string     `gorm:"size:64;not null" json:"generation_id"`
	PackageDigest   string     `gorm:"size:64;not null" json:"package_digest"`
	ArtifactDigest  string     `gorm:"size:64;not null" json:"artifact_digest"`
	ConfigVersion   uint64     `gorm:"not null;default:0" json:"config_version"`
	State           string     `gorm:"index;size:32;not null" json:"state"`
	ReportSequence  uint64     `gorm:"not null;default:0" json:"report_sequence"`
	ReportDigest    string     `gorm:"size:64;not null;default:''" json:"-"`
	ErrorCode       string     `gorm:"size:128;not null;default:''" json:"error_code,omitempty"`
	DetailsJSON     string     `gorm:"type:text;not null;default:'{}'" json:"details"`
	BudgetJSON      string     `gorm:"type:text;not null;default:'{}'" json:"budget"`
	ReportedAt      *time.Time `gorm:"index" json:"reported_at,omitempty"`
	UpdatedAt       time.Time  `gorm:"not null" json:"updated_at"`
}

func (PluginAgentRuntimeStatusRow) TableName() string { return "plugin_agent_runtime_statuses" }

type PluginRuntimeLogRow struct {
	ID              uint64    `gorm:"primaryKey;autoIncrement" json:"-"`
	EventID         *string   `gorm:"uniqueIndex;size:64" json:"-"`
	InstanceID      string    `gorm:"index;size:64;not null" json:"instance_id"`
	PluginID        string    `gorm:"index;size:190;not null" json:"plugin_id"`
	AgentID         string    `gorm:"index;size:64;not null" json:"agent_id"`
	ResourceGroupID string    `gorm:"index;size:64;not null" json:"resource_group_id"`
	OperationID     string    `gorm:"index;size:64;not null;default:''" json:"operation_id"`
	GenerationID    string    `gorm:"index;size:128;not null;default:''" json:"generation_id"`
	Revision        int64     `gorm:"not null;default:0" json:"revision"`
	PackageDigest   string    `gorm:"size:64;not null;default:''" json:"-"`
	ArtifactDigest  string    `gorm:"size:64;not null;default:''" json:"-"`
	Level           string    `gorm:"size:16;not null" json:"level"`
	Message         string    `gorm:"type:text;not null" json:"message"`
	Truncated       bool      `gorm:"not null;default:false" json:"truncated"`
	CreatedAt       time.Time `gorm:"index;not null" json:"created_at"`
}

type PluginRuntimeStateRow struct {
	InstanceID      string    `gorm:"primaryKey;size:64"`
	Key             string    `gorm:"primaryKey;size:190"`
	PluginID        string    `gorm:"index;size:190;not null"`
	ResourceGroupID string    `gorm:"index;size:64;not null"`
	Value           []byte    `gorm:"type:blob;not null"`
	UpdatedAt       time.Time `gorm:"not null"`
}

func (PluginRuntimeStateRow) TableName() string { return "plugin_runtime_state" }

func (PluginRuntimeLogRow) TableName() string { return "plugin_runtime_logs" }

type PluginControlPlaneLogOutboxRow struct {
	EventID         string    `gorm:"primaryKey;size:64"`
	InstanceID      string    `gorm:"index;size:64;not null"`
	PluginID        string    `gorm:"index;size:190;not null"`
	OperationID     string    `gorm:"index;size:64;not null"`
	GenerationID    string    `gorm:"index;size:128;not null"`
	ResourceGroupID string    `gorm:"index;size:64;not null"`
	Revision        int64     `gorm:"not null"`
	PackageDigest   string    `gorm:"size:64;not null"`
	ArtifactDigest  string    `gorm:"size:64;not null"`
	Level           string    `gorm:"size:16;not null"`
	Message         string    `gorm:"type:text;not null"`
	Truncated       bool      `gorm:"not null;default:false"`
	CreatedAt       time.Time `gorm:"index;not null"`
}

func (PluginControlPlaneLogOutboxRow) TableName() string { return "plugin_control_plane_log_outbox" }

// PluginRuntimeLogReportRow is the authenticated replay fence for one Agent
// runtime generation. Log fragments are stored separately only after the
// immutable generation identity and digest have matched.
type PluginRuntimeLogReportRow struct {
	AgentID        string    `gorm:"primaryKey;size:64"`
	InstanceID     string    `gorm:"primaryKey;size:64"`
	GenerationID   string    `gorm:"primaryKey;size:64"`
	Revision       int64     `gorm:"index;not null"`
	PluginID       string    `gorm:"index;size:190;not null"`
	PackageDigest  string    `gorm:"size:64;not null"`
	ArtifactDigest string    `gorm:"size:64;not null"`
	Sequence       uint64    `gorm:"not null"`
	ReportDigest   string    `gorm:"size:64;not null"`
	UpdatedAt      time.Time `gorm:"not null"`
}

func (PluginRuntimeLogReportRow) TableName() string { return "plugin_runtime_log_reports" }
