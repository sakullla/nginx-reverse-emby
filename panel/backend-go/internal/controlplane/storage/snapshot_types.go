package storage

type Snapshot struct {
	DesiredVersion      string                     `json:"desired_version"`
	Revision            int64                      `json:"desired_revision"`
	VersionPackage      *VersionPackage            `json:"version_package,omitempty"`
	AgentConfig         AgentConfig                `json:"agent_config,omitempty"`
	Rules               []HTTPRule                 `json:"rules"`
	L4Rules             []L4Rule                   `json:"l4_rules"`
	RelayListeners      []RelayListener            `json:"relay_listeners"`
	WireGuardProfiles   []WireGuardProfile         `json:"wireguard_profiles"`
	Certificates        []ManagedCertificateBundle `json:"certificates"`
	CertificatePolicies []ManagedCertificatePolicy `json:"certificate_policies"`
}

type AgentConfig struct {
	OutboundProxyURL     string `json:"outbound_proxy_url,omitempty"`
	TrafficStatsInterval string `json:"traffic_stats_interval,omitempty"`
	TrafficStatsEnabled  *bool  `json:"traffic_stats_enabled,omitempty"`
	TrafficBlocked       bool   `json:"traffic_blocked,omitempty"`
	TrafficBlockReason   string `json:"traffic_block_reason,omitempty"`
}

type AgentSnapshotInput struct {
	DesiredVersion  string
	DesiredRevision int
	CurrentRevision int
	Platform        string
}

type RuntimeState struct {
	NodeID                    string                     `json:"node_id,omitempty"`
	CurrentRevision           int64                      `json:"current_revision,omitempty"`
	Status                    string                     `json:"status,omitempty"`
	LastApplyRevision         int64                      `json:"last_apply_revision,omitempty"`
	LastApplyStatus           string                     `json:"last_apply_status,omitempty"`
	LastApplyMessage          string                     `json:"last_apply_message,omitempty"`
	ManagedCertificateReports []ManagedCertificateReport `json:"managed_certificate_reports,omitempty"`
	Metadata                  map[string]string          `json:"metadata,omitempty"`
}

type VersionPackage struct {
	URL      string `json:"url"`
	SHA256   string `json:"sha256"`
	Platform string `json:"platform,omitempty"`
	Filename string `json:"filename,omitempty"`
	Size     int64  `json:"size,omitempty"`
}

type HTTPHeader struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type HTTPBackend struct {
	URL string `json:"url"`
}

type LoadBalancing struct {
	Strategy string `json:"strategy,omitempty"`
}

type HTTPRule struct {
	ID               int           `json:"id,omitempty"`
	AgentID          string        `json:"agent_id,omitempty"`
	FrontendURL      string        `json:"frontend_url"`
	BackendURL       string        `json:"-"`
	Backends         []HTTPBackend `json:"backends,omitempty"`
	LoadBalancing    LoadBalancing `json:"load_balancing,omitempty"`
	ProxyRedirect    bool          `json:"proxy_redirect,omitempty"`
	PassProxyHeaders bool          `json:"pass_proxy_headers,omitempty"`
	UserAgent        string        `json:"user_agent,omitempty"`
	CustomHeaders    []HTTPHeader  `json:"custom_headers,omitempty"`
	RelayChain       []int         `json:"-"`
	RelayLayers      [][]int       `json:"relay_layers,omitempty"`
	RelayObfs        bool          `json:"relay_obfs,omitempty"`
	Revision         int64         `json:"revision,omitempty"`
}

type L4Backend struct {
	Host string `json:"host"`
	Port int    `json:"port"`
}

type L4ProxyProtocolTuning struct {
	Decode bool `json:"decode,omitempty"`
	Send   bool `json:"send,omitempty"`
}

type L4ProxyEntryAuth struct {
	Enabled  bool   `json:"enabled,omitempty"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
}

type L4Tuning struct {
	ProxyProtocol L4ProxyProtocolTuning `json:"proxy_protocol,omitempty"`
}

type L4Rule struct {
	ID                  int              `json:"id,omitempty"`
	AgentID             string           `json:"agent_id,omitempty"`
	Name                string           `json:"name,omitempty"`
	Protocol            string           `json:"protocol"`
	ListenHost          string           `json:"listen_host"`
	ListenPort          int              `json:"listen_port"`
	UpstreamHost        string           `json:"-"`
	UpstreamPort        int              `json:"-"`
	Backends            []L4Backend      `json:"backends,omitempty"`
	LoadBalancing       LoadBalancing    `json:"load_balancing,omitempty"`
	Tuning              L4Tuning         `json:"tuning,omitempty"`
	RelayChain          []int            `json:"-"`
	RelayLayers         [][]int          `json:"relay_layers,omitempty"`
	RelayObfs           bool             `json:"relay_obfs,omitempty"`
	ListenMode          string           `json:"listen_mode,omitempty"`
	WireGuardProfileID  *int             `json:"wireguard_profile_id,omitempty"`
	WireGuardListenHost string           `json:"wireguard_listen_host,omitempty"`
	ProxyEntryAuth      L4ProxyEntryAuth `json:"proxy_entry_auth,omitempty"`
	ProxyEgressMode     string           `json:"proxy_egress_mode,omitempty"`
	ProxyEgressURL      string           `json:"proxy_egress_url,omitempty"`
	Revision            int64            `json:"revision,omitempty"`
}

type RelayPin struct {
	Type  string `json:"type"`
	Value string `json:"value"`
}

type RelayListener struct {
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
	WireGuardProfileID      *int       `json:"wireguard_profile_id,omitempty"`
	AllowTransportFallback  bool       `json:"allow_transport_fallback"`
	ObfsMode                string     `json:"obfs_mode"`
	PinSet                  []RelayPin `json:"pin_set"`
	TrustedCACertificateIDs []int      `json:"trusted_ca_certificate_ids"`
	AllowSelfSigned         bool       `json:"allow_self_signed"`
	Tags                    []string   `json:"tags"`
	Revision                int64      `json:"revision"`
}

type WireGuardPeer struct {
	Name                       string   `json:"name"`
	PublicKey                  string   `json:"public_key"`
	PresharedKey               string   `json:"preshared_key,omitempty"`
	Endpoint                   string   `json:"endpoint"`
	AllowedIPs                 []string `json:"allowed_ips"`
	PersistentKeepaliveSeconds int      `json:"persistent_keepalive_seconds,omitempty"`
}

type WireGuardProfile struct {
	ID         int             `json:"id"`
	AgentID    string          `json:"agent_id"`
	Name       string          `json:"name"`
	Mode       string          `json:"mode"`
	PrivateKey string          `json:"private_key,omitempty"`
	ListenPort int             `json:"listen_port"`
	Addresses  []string        `json:"addresses"`
	Peers      []WireGuardPeer `json:"peers"`
	DNS        []string        `json:"dns"`
	MTU        int             `json:"mtu"`
	Enabled    bool            `json:"enabled"`
	Tags       []string        `json:"tags"`
	Revision   int64           `json:"revision"`
}

type ManagedCertificateBundle struct {
	ID       int    `json:"id"`
	Domain   string `json:"domain"`
	Revision int64  `json:"revision"`
	CertPEM  string `json:"cert_pem"`
	KeyPEM   string `json:"key_pem"`
}

type ManagedCertificateReport struct {
	ID           int                        `json:"id,omitempty"`
	Domain       string                     `json:"domain,omitempty"`
	Status       string                     `json:"status,omitempty"`
	LastIssueAt  string                     `json:"last_issue_at,omitempty"`
	LastError    string                     `json:"last_error,omitempty"`
	MaterialHash string                     `json:"material_hash,omitempty"`
	ACMEInfo     ManagedCertificateACMEInfo `json:"acme_info,omitempty"`
	UpdatedAt    string                     `json:"updated_at,omitempty"`
}

type ManagedCertificateACMEInfo struct {
	MainDomain string `json:"Main_Domain"`
	KeyLength  string `json:"KeyLength"`
	SANDomains string `json:"SAN_Domains"`
	Profile    string `json:"Profile"`
	CA         string `json:"CA"`
	Created    string `json:"Created"`
	Renew      string `json:"Renew"`
}

type ManagedCertificatePolicy struct {
	ID              int                        `json:"id"`
	Domain          string                     `json:"domain"`
	Enabled         bool                       `json:"enabled"`
	Scope           string                     `json:"scope"`
	IssuerMode      string                     `json:"issuer_mode"`
	Status          string                     `json:"status"`
	LastIssueAt     string                     `json:"last_issue_at"`
	LastError       string                     `json:"last_error"`
	ACMEInfo        ManagedCertificateACMEInfo `json:"acme_info"`
	Tags            []string                   `json:"tags"`
	Revision        int64                      `json:"revision"`
	Usage           string                     `json:"usage"`
	CertificateType string                     `json:"certificate_type"`
	SelfSigned      bool                       `json:"self_signed"`
}
