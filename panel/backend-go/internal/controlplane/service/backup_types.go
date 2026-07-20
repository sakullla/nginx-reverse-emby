package service

import (
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

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
	Agents           int `json:"agents"`
	HTTPRules        int `json:"http_rules"`
	L4Rules          int `json:"l4_rules"`
	RelayListeners   int `json:"relay_listeners"`
	EgressProfiles   int `json:"egress_profiles,omitempty"`
	Certificates     int `json:"certificates"`
	VersionPolicies  int `json:"version_policies"`
	TrafficPolicies  int `json:"traffic_policies,omitempty"`
	TrafficBaselines int `json:"traffic_baselines,omitempty"`
}

type BackupBundle struct {
	Manifest         BackupManifest          `json:"manifest"`
	Agents           []BackupAgent           `json:"agents"`
	HTTPRules        []BackupHTTPRule        `json:"http_rules"`
	L4Rules          []BackupL4Rule          `json:"l4_rules"`
	EgressProfiles   []BackupEgressProfile   `json:"egress_profiles,omitempty"`
	RelayListeners   []BackupRelayListener   `json:"relay_listeners"`
	Certificates     []BackupCertificate     `json:"certificates"`
	VersionPolicies  []BackupVersionPolicy   `json:"version_policies"`
	TrafficPolicies  []BackupTrafficPolicy   `json:"traffic_policies,omitempty"`
	TrafficBaselines []BackupTrafficBaseline `json:"traffic_baselines,omitempty"`
	Materials        []BackupCertificateFile `json:"-"`
}

type BackupAgent struct {
	ID                     string              `json:"id"`
	Name                   string              `json:"name"`
	AgentURL               string              `json:"agent_url,omitempty"`
	AgentToken             string              `json:"agent_token,omitempty"`
	Version                string              `json:"version,omitempty"`
	Platform               string              `json:"platform,omitempty"`
	RuntimePackageVersion  string              `json:"runtime_package_version,omitempty"`
	RuntimePackagePlatform string              `json:"runtime_package_platform,omitempty"`
	RuntimePackageArch     string              `json:"runtime_package_arch,omitempty"`
	RuntimePackageSHA256   string              `json:"runtime_package_sha256,omitempty"`
	DesiredVersion         string              `json:"desired_version,omitempty"`
	DesiredRevision        int                 `json:"desired_revision,omitempty"`
	OutboundProxyURL       string              `json:"outbound_proxy_url,omitempty"`
	TrafficStatsInterval   string              `json:"traffic_stats_interval,omitempty"`
	Tags                   []string            `json:"tags,omitempty"`
	Capabilities           []string            `json:"capabilities,omitempty"`
	Mode                   string              `json:"mode,omitempty"`
	DdnsConfig             *storage.DDNSConfig `json:"ddns_config,omitempty"`
}

type BackupHTTPRule struct {
	ID               int                `json:"id"`
	AgentID          string             `json:"agent_id"`
	FrontendURL      string             `json:"frontend_url"`
	BackendURL       string             `json:"backend_url,omitempty"`
	Backends         []HTTPRuleBackend  `json:"backends,omitempty"`
	LoadBalancing    HTTPLoadBalancing  `json:"load_balancing,omitempty"`
	Enabled          bool               `json:"enabled"`
	Tags             []string           `json:"tags,omitempty"`
	ProxyRedirect    bool               `json:"proxy_redirect"`
	RelayChain       []int              `json:"relay_chain,omitempty"`
	RelayLayers      [][]int            `json:"relay_layers,omitempty"`
	RelayObfs        bool               `json:"relay_obfs,omitempty"`
	PassProxyHeaders bool               `json:"pass_proxy_headers"`
	UserAgent        string             `json:"user_agent,omitempty"`
	CustomHeaders    []HTTPCustomHeader `json:"custom_headers,omitempty"`
	EgressProfileID  *int               `json:"egress_profile_id,omitempty"`
	Revision         int                `json:"revision,omitempty"`
}

type BackupL4Rule struct {
	ID              int              `json:"id"`
	AgentID         string           `json:"agent_id"`
	Name            string           `json:"name"`
	Protocol        string           `json:"protocol"`
	ListenHost      string           `json:"listen_host"`
	ListenPort      int              `json:"listen_port"`
	UpstreamHost    string           `json:"upstream_host,omitempty"`
	UpstreamPort    int              `json:"upstream_port,omitempty"`
	Backends        []L4Backend      `json:"backends,omitempty"`
	LoadBalancing   L4LoadBalancing  `json:"load_balancing,omitempty"`
	Tuning          L4Tuning         `json:"tuning,omitempty"`
	RelayChain      []int            `json:"relay_chain,omitempty"`
	RelayLayers     [][]int          `json:"relay_layers,omitempty"`
	RelayObfs       bool             `json:"relay_obfs,omitempty"`
	ListenMode      string           `json:"listen_mode,omitempty"`
	EgressProfileID *int             `json:"egress_profile_id,omitempty"`
	ProxyEgressMode string           `json:"proxy_egress_mode,omitempty"`
	ProxyEgressURL  string           `json:"proxy_egress_url,omitempty"`
	ProxyEntryAuth  L4ProxyEntryAuth `json:"proxy_entry_auth,omitempty"`
	Enabled         bool             `json:"enabled"`
	Tags            []string         `json:"tags,omitempty"`
	Revision        int              `json:"revision,omitempty"`
}

type BackupRelayListener struct {
	ID                      int        `json:"id"`
	AgentID                 string     `json:"agent_id"`
	AgentName               string     `json:"agent_name,omitempty"`
	Name                    string     `json:"name"`
	ListenHost              string     `json:"listen_host"`
	BindHosts               []string   `json:"bind_hosts"`
	ListenPort              int        `json:"listen_port"`
	PublicHost              string     `json:"public_host"`
	PublicPort              int        `json:"public_port"`
	Enabled                 bool       `json:"enabled"`
	CertificateID           *int       `json:"certificate_id"`
	TLSMode                 string     `json:"tls_mode"`
	TransportMode           string     `json:"transport_mode"`
	AllowTransportFallback  bool       `json:"allow_transport_fallback"`
	ObfsMode                string     `json:"obfs_mode"`
	PinSet                  []RelayPin `json:"pin_set"`
	TrustedCACertificateIDs []int      `json:"trusted_ca_certificate_ids"`
	AllowSelfSigned         bool       `json:"allow_self_signed"`
	Tags                    []string   `json:"tags"`
	Revision                int        `json:"revision"`
}

type BackupCertificate = ManagedCertificate
type BackupVersionPolicy = VersionPolicy

type BackupEgressProfile struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	Type        string `json:"type"`
	ProxyURL    string `json:"proxy_url,omitempty"`
	Enabled     bool   `json:"enabled"`
	Description string `json:"description,omitempty"`
	Revision    int    `json:"revision,omitempty"`
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
