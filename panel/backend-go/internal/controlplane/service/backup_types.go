package service

import "time"

const (
	BackupPackageVersion          = 1
	BackupSourceArchitectureGo    = "pure-go"
	BackupSourceArchitectureMain  = "main-legacy"
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
	Agents          int `json:"agents"`
	HTTPRules       int `json:"http_rules"`
	L4Rules         int `json:"l4_rules"`
	RelayListeners  int `json:"relay_listeners"`
	Certificates    int `json:"certificates"`
	VersionPolicies int `json:"version_policies"`
}

type BackupBundle struct {
	Manifest        BackupManifest          `json:"manifest"`
	Agents          []BackupAgent           `json:"agents"`
	HTTPRules       []BackupHTTPRule        `json:"http_rules"`
	L4Rules         []BackupL4Rule          `json:"l4_rules"`
	RelayListeners  []BackupRelayListener   `json:"relay_listeners"`
	Certificates    []BackupCertificate     `json:"certificates"`
	VersionPolicies []BackupVersionPolicy   `json:"version_policies"`
	Materials       []BackupCertificateFile `json:"-"`
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
	Tags                   []string `json:"tags,omitempty"`
	Capabilities           []string `json:"capabilities,omitempty"`
	Mode                   string   `json:"mode,omitempty"`
}

type BackupHTTPRule = HTTPRule
type BackupL4Rule = L4Rule
type BackupRelayListener = RelayListener
type BackupCertificate = ManagedCertificate
type BackupVersionPolicy = VersionPolicy

type BackupCertificateFile struct {
	Domain  string `json:"domain"`
	CertPEM string `json:"cert_pem"`
	KeyPEM  string `json:"key_pem"`
}
