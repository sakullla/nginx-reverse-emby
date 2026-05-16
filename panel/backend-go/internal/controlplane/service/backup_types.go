package service

import "time"

const (
	BackupPackageVersion         = 1
	BackupSourceArchitectureGo   = "pure-go"
	BackupSourceArchitectureMain = "main-legacy"
)

type BackupManifest struct {
	PackageVersion       int          `json:"package_version"`
	SourceArchitecture   string       `json:"source_architecture"`
	SourceAppVersion     string       `json:"source_app_version,omitempty"`
	SourceLocalAgentID   string       `json:"source_local_agent_id,omitempty"`
	ExportedAt           time.Time    `json:"exported_at"`
	IncludesCertificates bool         `json:"includes_certificates"`
	Counts               BackupCounts `json:"counts"`
}

type BackupCounts struct {
	Agents            int `json:"agents"`
	HTTPRules         int `json:"http_rules"`
	L4Rules           int `json:"l4_rules"`
	RelayListeners    int `json:"relay_listeners"`
	WireGuardProfiles int `json:"wireguard_profiles,omitempty"`
	WireGuardClients  int `json:"wireguard_clients,omitempty"`
	Certificates      int `json:"certificates"`
	VersionPolicies   int `json:"version_policies"`
	TrafficPolicies   int `json:"traffic_policies,omitempty"`
	TrafficBaselines  int `json:"traffic_baselines,omitempty"`
}

type BackupBundle struct {
	Manifest          BackupManifest           `json:"manifest"`
	Agents            []BackupAgent            `json:"agents"`
	HTTPRules         []BackupHTTPRule         `json:"http_rules"`
	L4Rules           []BackupL4Rule           `json:"l4_rules"`
	WireGuardProfiles []BackupWireGuardProfile `json:"wireguard_profiles,omitempty"`
	WireGuardClients  []BackupWireGuardClient  `json:"wireguard_clients,omitempty"`
	RelayListeners    []BackupRelayListener    `json:"relay_listeners"`
	Certificates      []BackupCertificate      `json:"certificates"`
	VersionPolicies   []BackupVersionPolicy    `json:"version_policies"`
	TrafficPolicies   []BackupTrafficPolicy    `json:"traffic_policies,omitempty"`
	TrafficBaselines  []BackupTrafficBaseline  `json:"traffic_baselines,omitempty"`
	Materials         []BackupCertificateFile  `json:"-"`
}

type BackupAgent struct {
	ID                     string   `json:"id"`
	Name                   string   `json:"name"`
	AgentURL               string   `json:"agent_url,omitempty"`
	AgentToken             string   `json:"agent_token,omitempty"`
	Version                string   `json:"version,omitempty"`
	Platform               string   `json:"platform,omitempty"`
	RuntimePackageVersion  string   `json:"runtime_package_version,omitempty"`
	RuntimePackagePlatform string   `json:"runtime_package_platform,omitempty"`
	RuntimePackageArch     string   `json:"runtime_package_arch,omitempty"`
	RuntimePackageSHA256   string   `json:"runtime_package_sha256,omitempty"`
	DesiredVersion         string   `json:"desired_version,omitempty"`
	DesiredRevision        int      `json:"desired_revision,omitempty"`
	OutboundProxyURL       string   `json:"outbound_proxy_url,omitempty"`
	TrafficStatsInterval   string   `json:"traffic_stats_interval,omitempty"`
	Tags                   []string `json:"tags,omitempty"`
	Capabilities           []string `json:"capabilities,omitempty"`
	Mode                   string   `json:"mode,omitempty"`
}

type BackupHTTPRule struct {
	ID                       int                `json:"id"`
	AgentID                  string             `json:"agent_id"`
	FrontendURL              string             `json:"frontend_url"`
	BackendURL               string             `json:"backend_url,omitempty"`
	Backends                 []HTTPRuleBackend  `json:"backends,omitempty"`
	LoadBalancing            HTTPLoadBalancing  `json:"load_balancing,omitempty"`
	Enabled                  bool               `json:"enabled"`
	Tags                     []string           `json:"tags,omitempty"`
	ProxyRedirect            bool               `json:"proxy_redirect"`
	RelayChain               []int              `json:"relay_chain,omitempty"`
	RelayLayers              [][]int            `json:"relay_layers,omitempty"`
	RelayObfs                bool               `json:"relay_obfs,omitempty"`
	PassProxyHeaders         bool               `json:"pass_proxy_headers"`
	UserAgent                string             `json:"user_agent,omitempty"`
	CustomHeaders            []HTTPCustomHeader `json:"custom_headers,omitempty"`
	WireGuardEntryEnabled    bool               `json:"wireguard_entry_enabled,omitempty"`
	WireGuardProfileID       *int               `json:"wireguard_profile_id,omitempty"`
	WireGuardEntryListenHost string             `json:"wireguard_entry_listen_host,omitempty"`
	WireGuardEntryListenPort int                `json:"wireguard_entry_listen_port,omitempty"`
	Revision                 int                `json:"revision,omitempty"`
}

type BackupL4Rule struct {
	ID                   int              `json:"id"`
	AgentID              string           `json:"agent_id"`
	Name                 string           `json:"name"`
	Protocol             string           `json:"protocol"`
	ListenHost           string           `json:"listen_host"`
	ListenPort           int              `json:"listen_port"`
	UpstreamHost         string           `json:"upstream_host,omitempty"`
	UpstreamPort         int              `json:"upstream_port,omitempty"`
	Backends             []L4Backend      `json:"backends,omitempty"`
	LoadBalancing        L4LoadBalancing  `json:"load_balancing,omitempty"`
	Tuning               L4Tuning         `json:"tuning,omitempty"`
	RelayChain           []int            `json:"relay_chain,omitempty"`
	RelayLayers          [][]int          `json:"relay_layers,omitempty"`
	RelayObfs            bool             `json:"relay_obfs,omitempty"`
	ListenMode           string           `json:"listen_mode,omitempty"`
	WireGuardProfileID   *int             `json:"wireguard_profile_id,omitempty"`
	WireGuardInboundMode string           `json:"wireguard_inbound_mode,omitempty"`
	WireGuardListenHost  string           `json:"wireguard_listen_host,omitempty"`
	ProxyEntryAuth       L4ProxyEntryAuth `json:"proxy_entry_auth,omitempty"`
	ProxyEgressMode      string           `json:"proxy_egress_mode,omitempty"`
	ProxyEgressURL       string           `json:"proxy_egress_url,omitempty"`
	Enabled              bool             `json:"enabled"`
	Tags                 []string         `json:"tags,omitempty"`
	Revision             int              `json:"revision,omitempty"`
}

type BackupRelayListener = RelayListener
type BackupCertificate = ManagedCertificate
type BackupVersionPolicy = VersionPolicy

type BackupWireGuardProfile struct {
	ID             int             `json:"id"`
	AgentID        string          `json:"agent_id"`
	Name           string          `json:"name"`
	Mode           string          `json:"mode"`
	PrivateKey     string          `json:"private_key,omitempty"`
	ListenPort     int             `json:"listen_port"`
	PublicEndpoint string          `json:"public_endpoint,omitempty"`
	Addresses      []string        `json:"addresses"`
	Peers          []WireGuardPeer `json:"peers"`
	DNS            []string        `json:"dns"`
	MTU            int             `json:"mtu"`
	Enabled        bool            `json:"enabled"`
	Tags           []string        `json:"tags,omitempty"`
	Revision       int             `json:"revision,omitempty"`
}

type BackupWireGuardClient struct {
	ID           int      `json:"id"`
	AgentID      string   `json:"agent_id"`
	ProfileID    int      `json:"profile_id"`
	Name         string   `json:"name"`
	PrivateKey   string   `json:"private_key,omitempty"`
	PublicKey    string   `json:"public_key"`
	PresharedKey string   `json:"preshared_key,omitempty"`
	Address      string   `json:"address"`
	AllowedIPs   []string `json:"allowed_ips"`
	DNS          []string `json:"dns"`
	Enabled      bool     `json:"enabled"`
	CreatedAt    string   `json:"created_at,omitempty"`
	UpdatedAt    string   `json:"updated_at,omitempty"`
}

type BackupTrafficPolicy struct {
	AgentID                string `json:"agent_id"`
	Direction              string `json:"direction"`
	CycleStartDay          int    `json:"cycle_start_day"`
	MonthlyQuotaBytes      *int64 `json:"monthly_quota_bytes,omitempty"`
	BlockWhenExceeded      bool   `json:"block_when_exceeded"`
	HourlyRetentionDays    int    `json:"hourly_retention_days"`
	DailyRetentionMonths   int    `json:"daily_retention_months"`
	MonthlyRetentionMonths *int   `json:"monthly_retention_months,omitempty"`
	UpdatedAt              string `json:"updated_at,omitempty"`
	CreatedAt              string `json:"created_at,omitempty"`
}

type BackupTrafficBaseline struct {
	AgentID           string `json:"agent_id"`
	CycleStart        string `json:"cycle_start"`
	RawRXBytes        uint64 `json:"raw_rx_bytes"`
	RawTXBytes        uint64 `json:"raw_tx_bytes"`
	RawAccountedBytes uint64 `json:"raw_accounted_bytes"`
	AdjustUsedBytes   int64  `json:"adjust_used_bytes"`
	UpdatedAt         string `json:"updated_at,omitempty"`
	CreatedAt         string `json:"created_at,omitempty"`
}

type BackupCertificateFile struct {
	Domain  string `json:"domain"`
	CertPEM string `json:"cert_pem"`
	KeyPEM  string `json:"key_pem"`
}
