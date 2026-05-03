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
	Agents           int `json:"agents"`
	HTTPRules        int `json:"http_rules"`
	L4Rules          int `json:"l4_rules"`
	RelayListeners   int `json:"relay_listeners"`
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
	RelayListeners   []BackupRelayListener   `json:"relay_listeners"`
	Certificates     []BackupCertificate     `json:"certificates"`
	VersionPolicies  []BackupVersionPolicy   `json:"version_policies"`
	TrafficPolicies  []BackupTrafficPolicy   `json:"traffic_policies,omitempty"`
	TrafficBaselines []BackupTrafficBaseline `json:"traffic_baselines,omitempty"`
	Materials        []BackupCertificateFile `json:"-"`
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

// Backup payload types intentionally mirror the current service JSON shape so
// legacy and pure-Go exports stay aligned. Keep compatibility in mind before
// changing the underlying service structs or their JSON tags.
type BackupHTTPRule = HTTPRule
type BackupL4Rule = L4Rule
type BackupRelayListener = RelayListener
type BackupCertificate = ManagedCertificate
type BackupVersionPolicy = VersionPolicy

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
