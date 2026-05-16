package storage

type AgentRow struct {
	ID                     string `gorm:"column:id;primaryKey"`
	Name                   string `gorm:"column:name"`
	AgentURL               string `gorm:"column:agent_url"`
	AgentToken             string `gorm:"column:agent_token"`
	Version                string `gorm:"column:version"`
	Platform               string `gorm:"column:platform"`
	RuntimePackageVersion  string `gorm:"column:runtime_package_version"`
	RuntimePackagePlatform string `gorm:"column:runtime_package_platform"`
	RuntimePackageArch     string `gorm:"column:runtime_package_arch"`
	RuntimePackageSHA256   string `gorm:"column:runtime_package_sha256"`
	DesiredVersion         string `gorm:"column:desired_version"`
	TagsJSON               string `gorm:"column:tags"`
	CapabilitiesJSON       string `gorm:"column:capabilities"`
	OutboundProxyURL       string `gorm:"column:outbound_proxy_url;not null;default:''"`
	TrafficStatsInterval   string `gorm:"column:traffic_stats_interval;not null;default:''"`
	Mode                   string `gorm:"column:mode"`
	DesiredRevision        int    `gorm:"column:desired_revision"`
	CurrentRevision        int    `gorm:"column:current_revision"`
	LastApplyRevision      int    `gorm:"column:last_apply_revision"`
	LastApplyStatus        string `gorm:"column:last_apply_status"`
	LastApplyMessage       string `gorm:"column:last_apply_message"`
	LastReportedStatsJSON  string `gorm:"column:last_reported_stats"`
	TrafficBlocked         bool   `gorm:"column:traffic_blocked;not null;default:false"`
	TrafficBlockReason     string `gorm:"column:traffic_block_reason;not null;default:''"`
	LastSeenAt             string `gorm:"column:last_seen_at"`
	LastSeenIP             string `gorm:"column:last_seen_ip"`
	IsLocal                bool   `gorm:"column:is_local"`
}

type HTTPRuleRow struct {
	ID                       int    `gorm:"column:id;primaryKey"`
	AgentID                  string `gorm:"column:agent_id;primaryKey;index:idx_rules_agent"`
	FrontendURL              string `gorm:"column:frontend_url"`
	BackendURL               string `gorm:"column:backend_url"`
	BackendsJSON             string `gorm:"column:backends"`
	LoadBalancingJSON        string `gorm:"column:load_balancing"`
	Enabled                  bool   `gorm:"column:enabled"`
	TagsJSON                 string `gorm:"column:tags"`
	ProxyRedirect            bool   `gorm:"column:proxy_redirect"`
	RelayChainJSON           string `gorm:"column:relay_chain"`
	RelayLayersJSON          string `gorm:"column:relay_layers"`
	RelayObfs                bool   `gorm:"column:relay_obfs"`
	PassProxyHeaders         bool   `gorm:"column:pass_proxy_headers"`
	UserAgent                string `gorm:"column:user_agent"`
	CustomHeadersJSON        string `gorm:"column:custom_headers"`
	WireGuardEntryEnabled    bool   `gorm:"column:wireguard_entry_enabled;not null;default:false"`
	WireGuardProfileID       *int   `gorm:"column:wireguard_profile_id"`
	WireGuardEntryListenHost string `gorm:"column:wireguard_entry_listen_host;not null;default:''"`
	WireGuardEntryListenPort int    `gorm:"column:wireguard_entry_listen_port;not null;default:0"`
	Revision                 int    `gorm:"column:revision"`
}

type LocalAgentStateRow struct {
	ID                int    `gorm:"column:id;primaryKey;check:id = 1"`
	DesiredRevision   int    `gorm:"column:desired_revision"`
	CurrentRevision   int    `gorm:"column:current_revision"`
	LastApplyRevision int    `gorm:"column:last_apply_revision"`
	LastApplyStatus   string `gorm:"column:last_apply_status"`
	LastApplyMessage  string `gorm:"column:last_apply_message"`
	DesiredVersion    string `gorm:"column:desired_version"`
}

type L4RuleRow struct {
	ID                   int    `gorm:"column:id;primaryKey"`
	AgentID              string `gorm:"column:agent_id;primaryKey;index:idx_l4_rules_agent"`
	Name                 string `gorm:"column:name"`
	Protocol             string `gorm:"column:protocol"`
	ListenHost           string `gorm:"column:listen_host"`
	ListenPort           int    `gorm:"column:listen_port"`
	UpstreamHost         string `gorm:"column:upstream_host"`
	UpstreamPort         int    `gorm:"column:upstream_port"`
	BackendsJSON         string `gorm:"column:backends"`
	LoadBalancingJSON    string `gorm:"column:load_balancing"`
	TuningJSON           string `gorm:"column:tuning"`
	RelayChainJSON       string `gorm:"column:relay_chain"`
	RelayLayersJSON      string `gorm:"column:relay_layers"`
	RelayObfs            bool   `gorm:"column:relay_obfs"`
	ListenMode           string `gorm:"column:listen_mode;not null;default:'tcp'"`
	WireGuardProfileID   *int   `gorm:"column:wireguard_profile_id"`
	WireGuardInboundMode string `gorm:"column:wireguard_inbound_mode;not null;default:'address'"`
	WireGuardListenHost  string `gorm:"column:wireguard_listen_host;not null;default:''"`
	ProxyEntryAuthJSON   string `gorm:"column:proxy_entry_auth;not null;default:'{}'"`
	ProxyEgressMode      string `gorm:"column:proxy_egress_mode;not null;default:''"`
	ProxyEgressURL       string `gorm:"column:proxy_egress_url;not null;default:''"`
	Enabled              bool   `gorm:"column:enabled"`
	TagsJSON             string `gorm:"column:tags"`
	Revision             int    `gorm:"column:revision"`
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
	WireGuardProfileID      *int   `gorm:"column:wireguard_profile_id"`
	AllowTransportFallback  bool   `gorm:"column:allow_transport_fallback"`
	ObfsMode                string `gorm:"column:obfs_mode"`
	PinSetJSON              string `gorm:"column:pin_set"`
	TrustedCACertificateIDs string `gorm:"column:trusted_ca_certificate_ids"`
	AllowSelfSigned         bool   `gorm:"column:allow_self_signed"`
	TagsJSON                string `gorm:"column:tags"`
	Revision                int    `gorm:"column:revision"`
}

type WireGuardProfileRow struct {
	ID            int    `gorm:"column:id;primaryKey"`
	AgentID       string `gorm:"column:agent_id;primaryKey;index:idx_wireguard_profiles_agent"`
	Name          string `gorm:"column:name"`
	Mode          string `gorm:"column:mode"`
	PrivateKey    string `gorm:"column:private_key"`
	ListenPort    int    `gorm:"column:listen_port"`
	AddressesJSON string `gorm:"column:addresses"`
	PeersJSON     string `gorm:"column:peers"`
	DNSJSON       string `gorm:"column:dns"`
	MTU           int    `gorm:"column:mtu"`
	Enabled       bool   `gorm:"column:enabled"`
	TagsJSON      string `gorm:"column:tags"`
	Revision      int    `gorm:"column:revision"`
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

func (WireGuardProfileRow) TableName() string {
	return "wireguard_profiles"
}

func (ManagedCertificateRow) TableName() string {
	return "managed_certificates"
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
