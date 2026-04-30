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
	Mode                   string `gorm:"column:mode"`
	DesiredRevision        int    `gorm:"column:desired_revision"`
	CurrentRevision        int    `gorm:"column:current_revision"`
	LastApplyRevision      int    `gorm:"column:last_apply_revision"`
	LastApplyStatus        string `gorm:"column:last_apply_status"`
	LastApplyMessage       string `gorm:"column:last_apply_message"`
	LastReportedStatsJSON  string `gorm:"column:last_reported_stats"`
	LastSeenAt             string `gorm:"column:last_seen_at"`
	LastSeenIP             string `gorm:"column:last_seen_ip"`
	IsLocal                bool   `gorm:"column:is_local"`
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
	Revision          int    `gorm:"column:revision"`
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
	ProxyEntryAuthJSON string `gorm:"column:proxy_entry_auth;not null;default:'{}'"`
	ProxyEgressMode    string `gorm:"column:proxy_egress_mode;not null;default:''"`
	ProxyEgressURL     string `gorm:"column:proxy_egress_url;not null;default:''"`
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

func (ManagedCertificateRow) TableName() string {
	return "managed_certificates"
}

func (MetaRow) TableName() string {
	return "meta"
}
