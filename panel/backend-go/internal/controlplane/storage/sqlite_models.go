package storage

import "time"

type AgentRow struct {
	ID                     string     `gorm:"column:id;primaryKey"`
	Name                   string     `gorm:"column:name"`
	AgentURL               string     `gorm:"column:agent_url"`
	AgentToken             string     `gorm:"column:agent_token"`
	Version                string     `gorm:"column:version"`
	Platform               string     `gorm:"column:platform"`
	RuntimePackageVersion  string     `gorm:"column:runtime_package_version"`
	RuntimePackagePlatform string     `gorm:"column:runtime_package_platform"`
	RuntimePackageArch     string     `gorm:"column:runtime_package_arch"`
	RuntimePackageSHA256   string     `gorm:"column:runtime_package_sha256"`
	DesiredVersion         string     `gorm:"column:desired_version"`
	TagsJSON               string     `gorm:"column:tags"`
	CapabilitiesJSON       string     `gorm:"column:capabilities"`
	OutboundProxyURL       string     `gorm:"column:outbound_proxy_url;not null;default:''"`
	TrafficStatsInterval   string     `gorm:"column:traffic_stats_interval;not null;default:''"`
	Mode                   string     `gorm:"column:mode"`
	DesiredRevision        int        `gorm:"column:desired_revision"`
	CurrentRevision        int        `gorm:"column:current_revision"`
	LastApplyRevision      int        `gorm:"column:last_apply_revision"`
	LastApplyStatus        string     `gorm:"column:last_apply_status"`
	LastApplyMessage       string     `gorm:"column:last_apply_message"`
	LastReportedStatsJSON  string     `gorm:"column:last_reported_stats"`
	TrafficBlocked         bool       `gorm:"column:traffic_blocked;not null;default:false"`
	TrafficBlockReason     string     `gorm:"column:traffic_block_reason;not null;default:''"`
	LastSeenAt             string     `gorm:"column:last_seen_at"`
	LastSeenIP             string     `gorm:"column:last_seen_ip"`
	LastSeenIPv4           string     `gorm:"column:last_seen_ipv4;not null;default:''"`
	LastSeenIPv6           string     `gorm:"column:last_seen_ipv6;not null;default:''"`
	DdnsConfigJSON         string     `gorm:"column:ddns_config;not null;default:''"`
	DdnsStatusJSON         string     `gorm:"column:ddns_status;not null;default:''"`
	PKISecurityAckJSON     string     `gorm:"column:pki_security_ack;not null;default:''"`
	PKISecurityAckAt       *time.Time `gorm:"column:pki_security_ack_at"`
	IsLocal                bool       `gorm:"column:is_local"`
}

type HTTPRuleRow struct {
	ID                int    `gorm:"column:id;primaryKey"`
	AgentID           string `gorm:"column:agent_id;primaryKey;index:idx_rules_agent"`
	FrontendURL       string `gorm:"column:frontend_url"`
	BackendURL        string `gorm:"column:backend_url"`
	BackendsJSON      string `gorm:"column:backends"`
	LoadBalancingJSON string `gorm:"column:load_balancing"`
	Enabled           bool   `gorm:"column:enabled"`
	TagsJSON          string `gorm:"column:tags"`
	ProxyRedirect     bool   `gorm:"column:proxy_redirect"`
	RelayChainJSON    string `gorm:"column:relay_chain"`
	RelayLayersJSON   string `gorm:"column:relay_layers"`
	RelayObfs         bool   `gorm:"column:relay_obfs"`
	PassProxyHeaders  bool   `gorm:"column:pass_proxy_headers"`
	UserAgent         string `gorm:"column:user_agent"`
	CustomHeadersJSON string `gorm:"column:custom_headers"`
	EgressProfileID   *int   `gorm:"column:egress_profile_id"`
	Revision          int    `gorm:"column:revision"`
}

type LocalAgentStateRow struct {
	ID                 int        `gorm:"column:id;primaryKey;check:id = 1"`
	DesiredRevision    int        `gorm:"column:desired_revision"`
	CurrentRevision    int        `gorm:"column:current_revision"`
	LastApplyRevision  int        `gorm:"column:last_apply_revision"`
	LastApplyStatus    string     `gorm:"column:last_apply_status"`
	LastApplyMessage   string     `gorm:"column:last_apply_message"`
	DesiredVersion     string     `gorm:"column:desired_version"`
	PKISecurityAckJSON string     `gorm:"column:pki_security_ack;not null;default:''"`
	PKISecurityAckAt   *time.Time `gorm:"column:pki_security_ack_at"`
}

type L4RuleRow struct {
	ID                 int    `gorm:"column:id;primaryKey"`
	AgentID            string `gorm:"column:agent_id;primaryKey;index:idx_l4_rules_agent"`
	Name               string `gorm:"column:name"`
	Protocol           string `gorm:"column:protocol"`
	ListenHost         string `gorm:"column:listen_host"`
	ListenPort         int    `gorm:"column:listen_port"`
	UpstreamHost       string `gorm:"column:upstream_host"`
	UpstreamPort       int    `gorm:"column:upstream_port"`
	BackendsJSON       string `gorm:"column:backends"`
	LoadBalancingJSON  string `gorm:"column:load_balancing"`
	TuningJSON         string `gorm:"column:tuning"`
	RelayChainJSON     string `gorm:"column:relay_chain"`
	RelayLayersJSON    string `gorm:"column:relay_layers"`
	RelayObfs          bool   `gorm:"column:relay_obfs"`
	ListenMode         string `gorm:"column:listen_mode;not null;default:'tcp'"`
	EgressProfileID    *int   `gorm:"column:egress_profile_id"`
	ProxyEntryAuthJSON string `gorm:"column:proxy_entry_auth;not null;default:'{}'"`
	Enabled            bool   `gorm:"column:enabled"`
	TagsJSON           string `gorm:"column:tags"`
	Revision           int    `gorm:"column:revision"`
}

type VersionPolicyRow struct {
	ID             string `gorm:"column:id;primaryKey"`
	Channel        string `gorm:"column:channel"`
	DesiredVersion string `gorm:"column:desired_version"`
	PackagesJSON   string `gorm:"column:packages"`
	TagsJSON       string `gorm:"column:tags"`
}

type RelayListenerRow struct {
	ID                      int    `gorm:"column:id;primaryKey"`
	AgentID                 string `gorm:"column:agent_id;index:idx_relay_listeners_agent"`
	Name                    string `gorm:"column:name"`
	BindHostsJSON           string `gorm:"column:bind_hosts"`
	ListenHost              string `gorm:"column:listen_host"`
	ListenPort              int    `gorm:"column:listen_port"`
	PublicHost              string `gorm:"column:public_host"`
	PublicPort              int    `gorm:"column:public_port"`
	Enabled                 bool   `gorm:"column:enabled"`
	CertificateID           *int   `gorm:"column:certificate_id"`
	TLSMode                 string `gorm:"column:tls_mode"`
	TransportMode           string `gorm:"column:transport_mode"`
	AllowTransportFallback  bool   `gorm:"column:allow_transport_fallback"`
	ObfsMode                string `gorm:"column:obfs_mode"`
	PinSetJSON              string `gorm:"column:pin_set"`
	TrustedCACertificateIDs string `gorm:"column:trusted_ca_certificate_ids"`
	AllowSelfSigned         bool   `gorm:"column:allow_self_signed"`
	TagsJSON                string `gorm:"column:tags"`
	Revision                int    `gorm:"column:revision"`
}

type EgressProfileRow struct {
	ID          int    `gorm:"column:id;primaryKey;not null"`
	Name        string `gorm:"column:name;not null"`
	Type        string `gorm:"column:type;not null"`
	ProxyURL    string `gorm:"column:proxy_url;not null;default:''"`
	Enabled     bool   `gorm:"column:enabled;not null;default:1"`
	Description string `gorm:"column:description;not null;default:''"`
	Revision    int64  `gorm:"column:revision;not null;default:0"`
}

type ManagedCertificateRow struct {
	ID              int    `gorm:"column:id;primaryKey"`
	Domain          string `gorm:"column:domain"`
	Enabled         bool   `gorm:"column:enabled"`
	Scope           string `gorm:"column:scope"`
	IssuerMode      string `gorm:"column:issuer_mode"`
	TargetAgentIDs  string `gorm:"column:target_agent_ids"`
	Status          string `gorm:"column:status"`
	LastIssueAt     string `gorm:"column:last_issue_at"`
	LastError       string `gorm:"column:last_error"`
	MaterialHash    string `gorm:"column:material_hash"`
	AgentReports    string `gorm:"column:agent_reports"`
	ACMEInfo        string `gorm:"column:acme_info"`
	Usage           string `gorm:"column:usage"`
	CertificateType string `gorm:"column:certificate_type"`
	SelfSigned      bool   `gorm:"column:self_signed"`
	TagsJSON        string `gorm:"column:tags"`
	Revision        int    `gorm:"column:revision"`
	// Failure-backoff fields shared by the async issue path and the periodic renewal
	// loop (see service.isManagedCertificateRenewalCandidate and
	// service.applyManagedCertificateRenewalFailureBackoff). Zero values encode the
	// "no outstanding backoff / retry immediately" contract:
	//   - NextRetryAtUnix == 0 -> eligible on the next renewal pass or re-dispatch
	//   - RetryCount == 0      -> no failed attempt recorded yet
	//   - BackoffClass == ""   -> no active backoff class
	// Fresh rows, certs that issued/renewed successfully, and pre-migration legacy
	// rows all carry zero values, so they are treated as healthy and retryable.
	// On failure the issue/renew paths populate all three together; the renewal
	// candidate guard treats NextRetryAtUnix > 0 && now < NextRetryAtUnix as "skip".
	NextRetryAtUnix     int64  `gorm:"column:next_retry_at_unix"`
	RetryCount          int    `gorm:"column:retry_count"`
	BackoffClass        string `gorm:"column:backoff_class"`
	NotAfter            string `gorm:"column:not_after"`
	ActiveGenerationID  string `gorm:"column:active_generation_id;not null;default:''"`
	PendingGenerationID string `gorm:"column:pending_generation_id;not null;default:''"`
}

type ManagedCertificateGenerationRow struct {
	ID           string `gorm:"column:id;primaryKey"`
	Domain       string `gorm:"column:domain;not null;index:idx_managed_certificate_generations_domain"`
	State        string `gorm:"column:state;not null;index:idx_managed_certificate_generations_domain_state"`
	MaterialHash string `gorm:"column:material_hash;not null"`
	CreatedAt    string `gorm:"column:created_at;not null"`
	PromotedAt   string `gorm:"column:promoted_at;not null;default:''"`
}

type MetaRow struct {
	Key   string `gorm:"column:key;primaryKey"`
	Value string `gorm:"column:value"`
}

type AgentTrafficPolicyRow struct {
	AgentID                string `gorm:"column:agent_id;primaryKey"`
	Direction              string `gorm:"column:direction;not null;default:'both'"`
	CycleStartDay          int    `gorm:"column:cycle_start_day;not null;default:1"`
	MonthlyQuotaBytes      *int64 `gorm:"column:monthly_quota_bytes"`
	BlockWhenExceeded      bool   `gorm:"column:block_when_exceeded;not null;default:false"`
	HourlyRetentionDays    int    `gorm:"column:hourly_retention_days;not null;default:30"`
	DailyRetentionMonths   int    `gorm:"column:daily_retention_months;not null;default:3"`
	MonthlyRetentionMonths *int   `gorm:"column:monthly_retention_months"`
	UpdatedAt              string `gorm:"column:updated_at"`
	CreatedAt              string `gorm:"column:created_at"`
}

type AgentTrafficBaselineRow struct {
	AgentID           string `gorm:"column:agent_id;primaryKey"`
	CycleStart        string `gorm:"column:cycle_start;primaryKey"`
	RawRXBytes        uint64 `gorm:"column:raw_rx_bytes"`
	RawTXBytes        uint64 `gorm:"column:raw_tx_bytes"`
	RawAccountedBytes uint64 `gorm:"column:raw_accounted_bytes"`
	AdjustUsedBytes   int64  `gorm:"column:adjust_used_bytes"`
	UpdatedAt         string `gorm:"column:updated_at"`
	CreatedAt         string `gorm:"column:created_at"`
}

type AgentTrafficAgentRow struct {
	AgentID   string `gorm:"column:agent_id;primaryKey"`
	UpdatedAt string `gorm:"column:updated_at"`
	CreatedAt string `gorm:"column:created_at"`
}

type AgentTrafficRawCursorRow struct {
	AgentID    string `gorm:"column:agent_id;primaryKey"`
	ScopeType  string `gorm:"column:scope_type;primaryKey"`
	ScopeID    string `gorm:"column:scope_id;primaryKey"`
	RXBytes    uint64 `gorm:"column:rx_bytes"`
	TXBytes    uint64 `gorm:"column:tx_bytes"`
	BootID     string `gorm:"column:boot_id"`
	ObservedAt string `gorm:"column:observed_at"`
}

type AgentTrafficHourlyBucketRow struct {
	AgentID     string `gorm:"column:agent_id;primaryKey;index:idx_agent_traffic_hourly_agent_bucket;index:idx_agent_traffic_hourly_aggregate,priority:1"`
	ScopeType   string `gorm:"column:scope_type;primaryKey;index:idx_agent_traffic_hourly_aggregate,priority:2"`
	ScopeID     string `gorm:"column:scope_id;primaryKey"`
	BucketStart string `gorm:"column:bucket_start;primaryKey;index:idx_agent_traffic_hourly_agent_bucket;index:idx_agent_traffic_hourly_aggregate,priority:3"`
	RXBytes     uint64 `gorm:"column:rx_bytes"`
	TXBytes     uint64 `gorm:"column:tx_bytes"`
	UpdatedAt   string `gorm:"column:updated_at"`
	CreatedAt   string `gorm:"column:created_at"`
}

type AgentTrafficDailySummaryRow struct {
	AgentID     string `gorm:"column:agent_id;primaryKey;index:idx_agent_traffic_daily_agent_period;index:idx_agent_traffic_daily_aggregate,priority:1"`
	ScopeType   string `gorm:"column:scope_type;primaryKey;index:idx_agent_traffic_daily_aggregate,priority:2"`
	ScopeID     string `gorm:"column:scope_id;primaryKey"`
	PeriodStart string `gorm:"column:period_start;primaryKey;index:idx_agent_traffic_daily_agent_period;index:idx_agent_traffic_daily_aggregate,priority:3"`
	RXBytes     uint64 `gorm:"column:rx_bytes"`
	TXBytes     uint64 `gorm:"column:tx_bytes"`
	UpdatedAt   string `gorm:"column:updated_at"`
	CreatedAt   string `gorm:"column:created_at"`
}

type AgentTrafficMonthlySummaryRow struct {
	AgentID     string `gorm:"column:agent_id;primaryKey;index:idx_agent_traffic_monthly_agent_period;index:idx_agent_traffic_monthly_aggregate,priority:1"`
	ScopeType   string `gorm:"column:scope_type;primaryKey;index:idx_agent_traffic_monthly_aggregate,priority:2"`
	ScopeID     string `gorm:"column:scope_id;primaryKey"`
	PeriodStart string `gorm:"column:period_start;primaryKey;index:idx_agent_traffic_monthly_agent_period;index:idx_agent_traffic_monthly_aggregate,priority:3"`
	RXBytes     uint64 `gorm:"column:rx_bytes"`
	TXBytes     uint64 `gorm:"column:tx_bytes"`
	UpdatedAt   string `gorm:"column:updated_at"`
	CreatedAt   string `gorm:"column:created_at"`
}

type AgentTrafficEventRow struct {
	ID        uint64 `gorm:"column:id;primaryKey;autoIncrement"`
	AgentID   string `gorm:"column:agent_id;index:idx_agent_traffic_events_agent_time"`
	EventType string `gorm:"column:event_type;index:idx_agent_traffic_events_type"`
	Message   string `gorm:"column:message"`
	Payload   string `gorm:"column:payload"`
	CreatedAt string `gorm:"column:created_at;index:idx_agent_traffic_events_agent_time"`
}

type OperationRow struct {
	ID                 string     `gorm:"column:id;primaryKey"`
	Kind               string     `gorm:"column:kind;not null;index:idx_operations_kind"`
	Status             string     `gorm:"column:status;not null;index:idx_operations_status_updated"`
	PrimaryAgentID     string     `gorm:"column:primary_agent_id;not null;default:'';index:idx_operations_primary_agent"`
	RequestFingerprint string     `gorm:"column:request_fingerprint;not null;default:''"`
	NoOp               bool       `gorm:"column:no_op;not null;default:false"`
	ErrorCode          string     `gorm:"column:error_code;not null;default:''"`
	ErrorMessage       string     `gorm:"column:error_message;not null;default:''"`
	CreatedAt          time.Time  `gorm:"column:created_at;not null;index:idx_operations_created"`
	UpdatedAt          time.Time  `gorm:"column:updated_at;not null;index:idx_operations_status_updated"`
	CompletedAt        *time.Time `gorm:"column:completed_at"`
}

type AgentRevisionRow struct {
	AgentID             string     `gorm:"column:agent_id;primaryKey;index:idx_agent_revisions_state_next,priority:1"`
	Revision            int64      `gorm:"column:revision;primaryKey;autoIncrement:false"`
	OperationID         string     `gorm:"column:operation_id;not null;index:idx_agent_revisions_operation"`
	State               string     `gorm:"column:state;not null;index:idx_agent_revisions_state_next,priority:2"`
	SnapshotArtifactID  string     `gorm:"column:snapshot_artifact_id;not null;default:'';index:idx_agent_revisions_snapshot_artifact"`
	SnapshotDigest      string     `gorm:"column:snapshot_digest;not null;default:''"`
	DesiredVersion      string     `gorm:"column:desired_version;not null;default:''"`
	ApplyTimeoutSeconds int        `gorm:"column:apply_timeout_seconds;not null;default:60"`
	DrainTimeoutSeconds int        `gorm:"column:drain_timeout_seconds;not null;default:600"`
	RetryCycle          int        `gorm:"column:retry_cycle;not null;default:0"`
	AttemptCount        int        `gorm:"column:attempt_count;not null;default:0"`
	NextAttemptAt       *time.Time `gorm:"column:next_attempt_at;index:idx_agent_revisions_state_next,priority:3"`
	GenerationID        string     `gorm:"column:generation_id;not null;default:''"`
	DrainState          string     `gorm:"column:drain_state;not null;default:''"`
	ErrorCode           string     `gorm:"column:error_code;not null;default:''"`
	ErrorMessage        string     `gorm:"column:error_message;not null;default:''"`
	LegacyBaseline      bool       `gorm:"column:legacy_baseline;not null;default:false"`
	CreatedAt           time.Time  `gorm:"column:created_at;not null;index:idx_agent_revisions_created"`
	UpdatedAt           time.Time  `gorm:"column:updated_at;not null"`
	AppliedAt           *time.Time `gorm:"column:applied_at"`
	FailedAt            *time.Time `gorm:"column:failed_at"`
}

type AgentRevisionPointerRow struct {
	AgentID               string    `gorm:"column:agent_id;primaryKey"`
	DesiredRevision       int64     `gorm:"column:desired_revision;not null;default:0"`
	AppliedRevision       int64     `gorm:"column:applied_revision;not null;default:0"`
	LastKnownGoodRevision int64     `gorm:"column:last_known_good_revision;not null;default:0"`
	UpdatedAt             time.Time `gorm:"column:updated_at;not null"`
}

type AgentRevisionAttemptRow struct {
	AgentID    string     `gorm:"column:agent_id;primaryKey"`
	Revision   int64      `gorm:"column:revision;primaryKey;autoIncrement:false"`
	RetryCycle int        `gorm:"column:retry_cycle;primaryKey;autoIncrement:false"`
	Attempt    int        `gorm:"column:attempt;primaryKey;autoIncrement:false"`
	LeaseID    string     `gorm:"column:lease_id;not null;uniqueIndex:idx_agent_revision_attempt_lease"`
	State      string     `gorm:"column:state;not null;index:idx_agent_revision_attempt_state"`
	StartedAt  time.Time  `gorm:"column:started_at;not null"`
	DeadlineAt time.Time  `gorm:"column:deadline_at;not null"`
	FinishedAt *time.Time `gorm:"column:finished_at"`
	ErrorCode  string     `gorm:"column:error_code;not null;default:''"`
	Error      string     `gorm:"column:error_message;not null;default:''"`
}

type AgentGenerationRow struct {
	AgentID      string     `gorm:"column:agent_id;primaryKey;index:idx_agent_generations_state,priority:1"`
	GenerationID string     `gorm:"column:generation_id;primaryKey"`
	Revision     int64      `gorm:"column:revision;not null;index:idx_agent_generations_revision"`
	State        string     `gorm:"column:state;not null;index:idx_agent_generations_state,priority:2"`
	SessionCount int64      `gorm:"column:session_count;not null;default:0"`
	Forced       bool       `gorm:"column:forced;not null;default:false"`
	ForceReason  string     `gorm:"column:force_reason;not null;default:''"`
	CreatedAt    time.Time  `gorm:"column:created_at;not null"`
	UpdatedAt    time.Time  `gorm:"column:updated_at;not null"`
	DrainedAt    *time.Time `gorm:"column:drained_at"`
}

type RevisionEventRow struct {
	ID          uint64    `gorm:"column:id;primaryKey;autoIncrement"`
	OperationID string    `gorm:"column:operation_id;not null;default:'';index:idx_revision_events_operation_id"`
	AgentID     string    `gorm:"column:agent_id;not null;default:'';index:idx_revision_events_agent_revision,priority:1"`
	Revision    int64     `gorm:"column:revision;not null;default:0;index:idx_revision_events_agent_revision,priority:2"`
	EventType   string    `gorm:"column:event_type;not null;index:idx_revision_events_type"`
	PayloadJSON string    `gorm:"column:payload_json;not null;default:'{}'"`
	CreatedAt   time.Time `gorm:"column:created_at;not null;index:idx_revision_events_created"`
}

type IdempotencyRecordRow struct {
	Scope              string    `gorm:"column:scope;primaryKey"`
	Key                string    `gorm:"column:key;primaryKey"`
	RequestFingerprint string    `gorm:"column:request_fingerprint;not null"`
	OperationID        string    `gorm:"column:operation_id;not null;index:idx_idempotency_operation"`
	ResponseJSON       string    `gorm:"column:response_json;not null;default:''"`
	CreatedAt          time.Time `gorm:"column:created_at;not null"`
	ExpiresAt          time.Time `gorm:"column:expires_at;not null;index:idx_idempotency_expires"`
}

type GenerationArtifactRow struct {
	ID        string    `gorm:"column:id;primaryKey"`
	Kind      string    `gorm:"column:kind;not null;index:idx_generation_artifacts_kind"`
	SHA256    string    `gorm:"column:sha256;not null;uniqueIndex:idx_generation_artifacts_sha256"`
	Payload   []byte    `gorm:"column:payload;not null"`
	SizeBytes int64     `gorm:"column:size_bytes;not null"`
	CreatedAt time.Time `gorm:"column:created_at;not null"`
}

type AgentRevisionArtifactRow struct {
	AgentID    string    `gorm:"column:agent_id;primaryKey"`
	Revision   int64     `gorm:"column:revision;primaryKey;autoIncrement:false"`
	ArtifactID string    `gorm:"column:artifact_id;primaryKey;index:idx_agent_revision_artifacts_artifact"`
	Role       string    `gorm:"column:role;primaryKey"`
	CreatedAt  time.Time `gorm:"column:created_at;not null"`
}

func (AgentRow) TableName() string {
	return "agents"
}

func (HTTPRuleRow) TableName() string {
	return "rules"
}

func (LocalAgentStateRow) TableName() string {
	return "local_agent_state"
}

func (L4RuleRow) TableName() string {
	return "l4_rules"
}

func (VersionPolicyRow) TableName() string {
	return "version_policy"
}

func (RelayListenerRow) TableName() string {
	return "relay_listeners"
}

func (EgressProfileRow) TableName() string {
	return "egress_profiles"
}

func (ManagedCertificateRow) TableName() string {
	return "managed_certificates"
}

func (ManagedCertificateGenerationRow) TableName() string {
	return "managed_certificate_generations"
}

func (MetaRow) TableName() string {
	return "meta"
}

func (AgentTrafficPolicyRow) TableName() string {
	return "agent_traffic_policies"
}

func (AgentTrafficBaselineRow) TableName() string {
	return "agent_traffic_baselines"
}

func (AgentTrafficAgentRow) TableName() string {
	return "agent_traffic_agents"
}

func (AgentTrafficRawCursorRow) TableName() string {
	return "agent_traffic_raw_cursors"
}

func (AgentTrafficHourlyBucketRow) TableName() string {
	return "agent_traffic_hourly_buckets"
}

func (AgentTrafficDailySummaryRow) TableName() string {
	return "agent_traffic_daily_summaries"
}

func (AgentTrafficMonthlySummaryRow) TableName() string {
	return "agent_traffic_monthly_summaries"
}

func (AgentTrafficEventRow) TableName() string {
	return "agent_traffic_events"
}

func (OperationRow) TableName() string {
	return "operations"
}

func (AgentRevisionRow) TableName() string {
	return "agent_revisions"
}

func (AgentRevisionPointerRow) TableName() string {
	return "agent_revision_pointers"
}

func (AgentRevisionAttemptRow) TableName() string {
	return "agent_revision_attempts"
}

func (AgentGenerationRow) TableName() string {
	return "agent_generations"
}

func (RevisionEventRow) TableName() string {
	return "revision_events"
}

func (IdempotencyRecordRow) TableName() string {
	return "idempotency_records"
}

func (GenerationArtifactRow) TableName() string {
	return "generation_artifacts"
}

func (AgentRevisionArtifactRow) TableName() string {
	return "agent_revision_artifacts"
}
