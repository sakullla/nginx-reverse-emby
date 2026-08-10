package storage

import (
	"context"
	"encoding/json"
	"strings"
	"time"
)

type Snapshot struct {
	DesiredVersion      string                     `json:"desired_version"`
	Revision            int64                      `json:"desired_revision"`
	VersionPackage      *VersionPackage            `json:"version_package,omitempty"`
	AgentConfig         AgentConfig                `json:"agent_config,omitempty"`
	DDNSConfig          *DDNSConfig                `json:"ddns_config,omitempty"`
	Rules               []HTTPRule                 `json:"rules"`
	L4Rules             []L4Rule                   `json:"l4_rules"`
	PluginGenerations   []PluginGeneration         `json:"plugin_generations"`
	PluginPolicies      []PluginPolicy             `json:"plugin_policies"`
	RelayListeners      []RelayListener            `json:"relay_listeners"`
	EgressProfiles      []EgressProfile            `json:"egress_profiles"`
	Certificates        []ManagedCertificateBundle `json:"certificates"`
	CertificatePolicies []ManagedCertificatePolicy `json:"certificate_policies"`
	PKISecurity         *PKISecuritySnapshot       `json:"pki_security,omitempty"`
}

// PluginGeneration is the complete, target-specific runtime projection. It
// deliberately contains no marketplace source, cache path, manifest, UI
// metadata, or secret plaintext.
type PluginGeneration struct {
	ID              string                         `json:"id"`
	InstanceID      string                         `json:"instance_id"`
	OperationID     string                         `json:"operation_id,omitempty"`
	Revision        int64                          `json:"revision"`
	PluginID        string                         `json:"plugin_id"`
	PluginVersion   string                         `json:"plugin_version"`
	PackageDigest   string                         `json:"package_digest"`
	Runtime         PluginGenerationRuntime        `json:"runtime"`
	Artifact        PluginGenerationArtifact       `json:"artifact"`
	ExtensionPoints []string                       `json:"extension_points"`
	ConfigVersion   uint64                         `json:"config_version"`
	Config          json.RawMessage                `json:"config"`
	Grants          []PluginGenerationGrant        `json:"grants"`
	SecretHandles   []PluginGenerationSecretHandle `json:"secret_handles"`
	ResourceBudget  PluginGenerationResourceBudget `json:"resource_budget"`
	Target          PluginGenerationTarget         `json:"target"`
	FailurePolicy   PluginGenerationFailurePolicy  `json:"failure_policy"`
}

type PluginGenerationArtifact struct {
	ArtifactID        string `json:"artifact_id"`
	PackageIdentity   string `json:"package_identity"`
	RelativePath      string `json:"relative_path"`
	SHA256            string `json:"sha256"`
	SizeBytes         int64  `json:"size_bytes"`
	Mode              string `json:"mode"`
	GOOS              string `json:"goos,omitempty"`
	GOARCH            string `json:"goarch,omitempty"`
	LocalPath         string `json:"local_path,omitempty"`
	SignatureVerified bool   `json:"signature_verified"`
	SignerKeyID       string `json:"signer_key_id"`
	SignerFingerprint string `json:"signer_fingerprint"`
}

type PluginGenerationRuntime struct {
	Kind      string `json:"kind"`
	ABI       string `json:"abi"`
	HostScope string `json:"host_scope"`
	Entry     string `json:"entry"`
}

type PluginGenerationGrant struct {
	Name         string `json:"name"`
	ResourceKind string `json:"resource_kind,omitempty"`
	ResourceID   string `json:"resource_id,omitempty"`
}

// PluginGenerationSecretHandle is a revocable reference. Value is never part
// of a snapshot; secret delivery remains owned by the authenticated lease.
type PluginGenerationSecretHandle struct {
	ID      string `json:"id"`
	Version uint64 `json:"version"`
	Digest  string `json:"digest"`
	Purpose string `json:"purpose,omitempty"`
}

type PluginGenerationResourceBudget struct {
	TimeoutMS   int64 `json:"timeout_ms"`
	MemoryBytes int64 `json:"memory_bytes"`
	Concurrency int   `json:"concurrency"`
	InputBytes  int64 `json:"input_bytes"`
	OutputBytes int64 `json:"output_bytes"`
	CPUMillis   int64 `json:"cpu_millis,omitempty"`
	Restarts    int   `json:"restarts,omitempty"`
}

type PluginGenerationTarget struct {
	Kind            string `json:"kind"`
	ID              string `json:"id"`
	ResourceGroupID string `json:"resource_group_id"`
	Version         uint64 `json:"version"`
}

type PluginGenerationFailurePolicy struct {
	OnError      string `json:"on_error"`
	OnBudget     string `json:"on_budget"`
	Restart      string `json:"restart"`
	CoreFallback string `json:"core_fallback"`
}

// AgentSnapshotMetadata carries database-owned values needed to finish a
// heartbeat response without consulting an AgentRow captured before the
// snapshot transaction. It is deliberately separate from the JSON snapshot.
type AgentSnapshotMetadata struct {
	Platform           string
	DesiredVersion     string
	DesiredRevision    int
	CurrentRevision    int
	LastApplyStatus    string
	OutboundProxyURL   string
	TrafficInterval    string
	TrafficBlocked     bool
	TrafficBlockReason string
}

type AgentHeartbeatSnapshot struct {
	Snapshot Snapshot
	Metadata AgentSnapshotMetadata `json:"-"`
}

// AgentHeartbeatSnapshotOverlay runs inside the same stable read transaction
// as the base snapshot. It is used by the service layer to project pending
// certificate generations without creating a storage-to-service dependency.
type AgentHeartbeatSnapshotOverlay func(context.Context, *GormStore, string, Snapshot) (Snapshot, error)

// PKISecurityAcknowledgement is reported over the existing authenticated
// control channel. It never authenticates that channel; X-Agent-Token remains
// the control-plane credential while this value only advances PKI delivery
// state.
type PKISecurityAcknowledgement struct {
	PKIDomainID         string                                 `json:"pki_domain_id"`
	PKIEpoch            int64                                  `json:"pki_epoch"`
	SecurityRevision    int64                                  `json:"security_revision"`
	Full                bool                                   `json:"full"`
	CertificateID       string                                 `json:"certificate_id,omitempty"`
	TrustGenerations    []int64                                `json:"trust_generations,omitempty"`
	ListenerCredentials []PKIListenerCredentialAcknowledgement `json:"listener_credentials,omitempty"`
}

type PKIListenerCredentialAcknowledgement struct {
	ListenerID    string `json:"listener_id"`
	IdentityID    string `json:"identity_id"`
	CertificateID string `json:"certificate_id"`
	CAGeneration  int64  `json:"ca_generation"`
}

// PKITrustRoot is public trust material only. Endpoint and listener private
// keys are deliberately absent from every control snapshot.
type PKITrustRoot struct {
	AuthorityID       string    `json:"authority_id"`
	Generation        int64     `json:"generation"`
	Status            string    `json:"status"`
	CertificatePEM    string    `json:"certificate_pem"`
	FingerprintSHA256 string    `json:"fingerprint_sha256"`
	NotBefore         time.Time `json:"not_before"`
	NotAfter          time.Time `json:"not_after"`
}

// PKISecuritySnapshot is carried by registration, heartbeat and revision
// responses on the existing control listener. Signature is base64 encoded by
// encoding/json; no endpoint private key is part of this contract.
type PKISecuritySnapshot struct {
	PKIDomainID        string         `json:"pki_domain_id"`
	PKIEpoch           int64          `json:"pki_epoch"`
	SecurityRevision   int64          `json:"security_revision"`
	Full               bool           `json:"full"`
	IssuedAt           time.Time      `json:"issued_at"`
	TrustRoots         []PKITrustRoot `json:"trust_roots"`
	RevokedIdentityIDs []string       `json:"revoked_identity_ids"`
	RevokedSerials     []string       `json:"revoked_serials"`
	SignerGeneration   int64          `json:"signer_generation"`
	Signature          []byte         `json:"signature"`
}

// PKITunnelCredential is the public half of an enrolled relay identity. The
// matching private key is generated and retained by the owning agent.
type PKITunnelCredential struct {
	IdentityID           string    `json:"identity_id"`
	CertificateID        string    `json:"certificate_id"`
	Purpose              string    `json:"purpose"`
	CertificatePEM       string    `json:"certificate_pem"`
	PublicKeyFingerprint string    `json:"public_key_fingerprint_sha256"`
	AuthorityID          string    `json:"authority_id"`
	CAGeneration         int64     `json:"ca_generation"`
	NotBefore            time.Time `json:"not_before"`
	NotAfter             time.Time `json:"not_after"`
}

// FilterSupportedSnapshotResources removes resource kinds retired by the
// current runtime together with rules that still reference those resources.
// The returned snapshot does not share top-level slice storage with the input.
func FilterSupportedSnapshotResources(snapshot Snapshot) (Snapshot, bool) {
	filtered, changed := FilterSupportedSnapshotResourceGraph([]Snapshot{snapshot})
	return filtered[0], changed
}

// FilterSupportedSnapshotResourceGraph applies the supported-resource filter
// across snapshots that participate in the same operation. This removes
// cross-agent references to a retired shared resource as one atomic graph.
func FilterSupportedSnapshotResourceGraph(snapshots []Snapshot) ([]Snapshot, bool) {
	excludedRelayIDs := make(map[int]struct{})
	excludedEgressIDs := make(map[int]struct{})
	for _, snapshot := range snapshots {
		for _, listener := range snapshot.RelayListeners {
			if !snapshotRelayTransportSupported(listener.TransportMode) {
				excludedRelayIDs[listener.ID] = struct{}{}
			}
		}
		for _, profile := range snapshot.EgressProfiles {
			if !snapshotEgressProfileTypeSupported(profile.Type) {
				excludedEgressIDs[profile.ID] = struct{}{}
			}
		}
	}

	filtered := make([]Snapshot, len(snapshots))
	changed := false
	for i := range snapshots {
		var snapshotChanged bool
		filtered[i], snapshotChanged = filterSupportedSnapshotResources(snapshots[i], excludedRelayIDs, excludedEgressIDs)
		changed = changed || snapshotChanged
	}
	return filtered, changed
}

func filterSupportedSnapshotResources(snapshot Snapshot, excludedRelayIDs, excludedEgressIDs map[int]struct{}) (Snapshot, bool) {
	filtered := snapshot
	changed := false

	filtered.RelayListeners = nil
	if snapshot.RelayListeners != nil {
		filtered.RelayListeners = make([]RelayListener, 0, len(snapshot.RelayListeners))
	}
	for _, listener := range snapshot.RelayListeners {
		if snapshotRelayTransportSupported(listener.TransportMode) {
			filtered.RelayListeners = append(filtered.RelayListeners, listener)
			continue
		}
		changed = true
	}

	filtered.EgressProfiles = nil
	if snapshot.EgressProfiles != nil {
		filtered.EgressProfiles = make([]EgressProfile, 0, len(snapshot.EgressProfiles))
	}
	for _, profile := range snapshot.EgressProfiles {
		if snapshotEgressProfileTypeSupported(profile.Type) {
			filtered.EgressProfiles = append(filtered.EgressProfiles, profile)
			continue
		}
		changed = true
	}

	filtered.Rules = nil
	if snapshot.Rules != nil {
		filtered.Rules = make([]HTTPRule, 0, len(snapshot.Rules))
	}
	for _, rule := range snapshot.Rules {
		if typedSnapshotRuleReferencesExcludedResource(rule.RelayChain, rule.RelayLayers, rule.EgressProfileID, excludedRelayIDs, excludedEgressIDs) {
			changed = true
			continue
		}
		filtered.Rules = append(filtered.Rules, rule)
	}

	filtered.L4Rules = nil
	if snapshot.L4Rules != nil {
		filtered.L4Rules = make([]L4Rule, 0, len(snapshot.L4Rules))
	}
	for _, rule := range snapshot.L4Rules {
		if !snapshotL4RuleSupported(rule) || typedSnapshotRuleReferencesExcludedResource(rule.RelayChain, rule.RelayLayers, rule.EgressProfileID, excludedRelayIDs, excludedEgressIDs) {
			changed = true
			continue
		}
		filtered.L4Rules = append(filtered.L4Rules, rule)
	}

	return filtered, changed
}

func snapshotRelayTransportSupported(transportMode string) bool {
	switch strings.ToLower(strings.TrimSpace(transportMode)) {
	case "", "tls_tcp", "quic":
		return true
	default:
		return false
	}
}

func snapshotEgressProfileTypeSupported(profileType string) bool {
	switch strings.ToLower(strings.TrimSpace(profileType)) {
	case "direct", "socks", "http":
		return true
	default:
		return false
	}
}

func snapshotL4RuleSupported(rule L4Rule) bool {
	protocol := strings.ToLower(strings.TrimSpace(rule.Protocol))
	if protocol != "" && protocol != "tcp" && protocol != "udp" {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(rule.ListenMode)) {
	case "", "tcp", "proxy":
		return true
	default:
		return false
	}
}

func typedSnapshotRuleReferencesExcludedResource(
	relayChain []int,
	relayLayers [][]int,
	egressProfileID *int,
	excludedRelayIDs, excludedEgressIDs map[int]struct{},
) bool {
	if egressProfileID != nil {
		if _, excluded := excludedEgressIDs[*egressProfileID]; excluded {
			return true
		}
	}
	for _, listenerID := range relayChain {
		if _, excluded := excludedRelayIDs[listenerID]; excluded {
			return true
		}
	}
	for _, layer := range relayLayers {
		for _, listenerID := range layer {
			if _, excluded := excludedRelayIDs[listenerID]; excluded {
				return true
			}
		}
	}
	return false
}

type AgentConfig struct {
	OutboundProxyURL     string `json:"outbound_proxy_url,omitempty"`
	TrafficStatsInterval string `json:"traffic_stats_interval,omitempty"`
	TrafficStatsEnabled  *bool  `json:"-"`
	TrafficBlocked       bool   `json:"-"`
	TrafficBlockReason   string `json:"-"`
}

// DDNSConfig is the per-agent dynamic DNS extraction configuration. It is the
// wire contract dispatched to agents (via Snapshot) and persisted on AgentRow.
//
// SECURITY (R7): this struct MUST NOT carry any Cloudflare credential. CF
// tokens live only in the master process environment, are never persisted to
// the database, never included in backups, never exposed via AgentSummary, and
// never dispatched to agents. Only the domain plus per-family extraction
// strategy travel here.
type DDNSConfig struct {
	// Enabled is the per-agent master switch. It always serializes (no
	// omitempty) so an explicit off survives persist/dispatch round trips and
	// can be told apart from legacy rows that predate the field.
	Enabled bool       `json:"enabled"`
	Domain  string     `json:"domain,omitempty"`
	IPv4    DDNSFamily `json:"ipv4,omitempty"`
	IPv6    DDNSFamily `json:"ipv6,omitempty"`
}

// UnmarshalJSON derives Enabled for rows persisted before the switch existed:
// a config with no "enabled" key is treated as enabled when any family
// extraction was on, so upgrading never silently disables working DDNS. An
// explicit "enabled" key always wins.
func (c *DDNSConfig) UnmarshalJSON(data []byte) error {
	type wire DDNSConfig
	var decoded wire
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(data, &keys); err != nil {
		return err
	}
	if _, present := keys["enabled"]; !present {
		decoded.Enabled = decoded.IPv4.Enabled || decoded.IPv6.Enabled
	}
	*c = DDNSConfig(decoded)
	return nil
}

// DDNSFamily describes how one address family is extracted on the agent.
// Source is "public_api" (probe a public echo endpoint) or "interface" (read
// the address of the named network interface). IPv4 and IPv6 are independent.
type DDNSFamily struct {
	Enabled   bool   `json:"enabled"`
	Source    string `json:"source,omitempty"`
	Interface string `json:"interface,omitempty"`
}

// DdnsStatus is the runtime DDNS resolution state written by the master DDNS
// service (A/AAAA upsert result, backoff, last resolved IPs). It is runtime
// state only: persisted on AgentRow for display, but intentionally excluded
// from backups. Status is one of: ok | error | disabled | idle.
type DdnsStatus struct {
	Status            string `json:"status,omitempty"`
	LastError         string `json:"last_error,omitempty"`
	LastSuccessAtUnix int64  `json:"last_success_at_unix,omitempty"`
	NextRetryAtUnix   int64  `json:"next_retry_at_unix,omitempty"`
	RetryCount        int    `json:"retry_count,omitempty"`
	BackoffClass      string `json:"backoff_class,omitempty"`
	LastResolvedIPv4  string `json:"last_resolved_ipv4,omitempty"`
	LastResolvedIPv6  string `json:"last_resolved_ipv6,omitempty"`
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
	PluginStatuses            []PluginRuntimeStatus      `json:"plugin_statuses,omitempty"`
	Metadata                  map[string]string          `json:"metadata,omitempty"`
}

// PluginRuntimeStatus is the Agent-reported, generation-fenced runtime view.
// Agent identity is supplied by the authenticated transport and is never
// accepted from this payload.
type PluginRuntimeStatus struct {
	InstanceID      string          `json:"instance_id"`
	PluginID        string          `json:"plugin_id"`
	OperationID     string          `json:"operation_id"`
	Revision        int64           `json:"revision"`
	GenerationID    string          `json:"generation_id"`
	PackageDigest   string          `json:"package_digest"`
	ArtifactDigest  string          `json:"artifact_digest"`
	ConfigVersion   uint64          `json:"config_version"`
	RuntimeKind     string          `json:"runtime_kind"`
	State           string          `json:"state"`
	Sequence        uint64          `json:"sequence"`
	ErrorCode       string          `json:"error_code,omitempty"`
	SafeDetail      string          `json:"safe_detail,omitempty"`
	Details         json.RawMessage `json:"details,omitempty"`
	Budget          json.RawMessage `json:"budget,omitempty"`
	SandboxProvider string          `json:"sandbox_provider,omitempty"`
	RestartCount    int             `json:"restart_count,omitempty"`
	CircuitOpen     bool            `json:"circuit_open,omitempty"`
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

type PolicyRef struct {
	ID      string          `json:"id"`
	Overlay json.RawMessage `json:"overlay,omitempty"`
}

type PolicyResourceBudget struct {
	TimeoutMS   int64 `json:"timeout_ms"`
	MemoryBytes int64 `json:"memory_bytes"`
	Concurrency int   `json:"concurrency"`
	InputBytes  int64 `json:"input_bytes"`
	OutputBytes int64 `json:"output_bytes"`
}

type PolicyFailurePolicy struct {
	OnError      string `json:"on_error"`
	OnBudget     string `json:"on_budget"`
	Restart      string `json:"restart"`
	CoreFallback string `json:"core_fallback"`
}

// PolicyArtifactSource is the durable, location-independent identity of a
// policy artifact. ArtifactPath remains an optional embedded-Agent execution
// hint and is empty for remotes.
type PolicyArtifactSource struct {
	ArtifactID      string `json:"artifact_id"`
	PackageIdentity string `json:"package_identity"`
	PackageDigest   string `json:"package_digest"`
	RelativePath    string `json:"relative_path"`
	SHA256          string `json:"sha256"`
	SizeBytes       int64  `json:"size_bytes"`
}

type PolicyStage struct {
	Kind              string               `json:"kind"`
	PolicyID          string               `json:"policy_id"`
	PluginID          string               `json:"plugin_id"`
	PluginVersion     string               `json:"plugin_version"`
	InstanceID        string               `json:"instance_id"`
	PackageDigest     string               `json:"package_digest"`
	ArtifactPath      string               `json:"artifact_path"`
	ArtifactDigest    string               `json:"artifact_digest"`
	ArtifactSource    PolicyArtifactSource `json:"artifact_source"`
	SignatureVerified bool                 `json:"signature_verified"`
	SignerKeyID       string               `json:"signer_key_id"`
	SignerFingerprint string               `json:"signer_fingerprint"`
	ABI               string               `json:"abi"`
	ExtensionPoints   []string             `json:"extension_points"`
	DeclaredScopes    []string             `json:"declared_scopes"`
	GrantedScopes     []string             `json:"granted_scopes"`
	ResourceGroupID   string               `json:"resource_group_id"`
	Config            json.RawMessage      `json:"config,omitempty"`
	ResourceBudget    PolicyResourceBudget `json:"resource_budget"`
	FailurePolicy     PolicyFailurePolicy  `json:"failure_policy"`
}

type PluginPolicy struct {
	ID       string        `json:"id"`
	Revision int64         `json:"revision"`
	Stages   []PolicyStage `json:"stages"`
}

type HTTPRule struct {
	ID                 int           `json:"id,omitempty"`
	AgentID            string        `json:"agent_id,omitempty"`
	FrontendURL        string        `json:"frontend_url"`
	BackendURL         string        `json:"-"`
	Backends           []HTTPBackend `json:"backends,omitempty"`
	LoadBalancing      LoadBalancing `json:"load_balancing,omitempty"`
	ProxyRedirect      bool          `json:"proxy_redirect,omitempty"`
	PassProxyHeaders   bool          `json:"pass_proxy_headers,omitempty"`
	UserAgent          string        `json:"user_agent,omitempty"`
	CustomHeaders      []HTTPHeader  `json:"custom_headers,omitempty"`
	EgressProfileID    *int          `json:"egress_profile_id,omitempty"`
	TrustedProxyRanges []string      `json:"trusted_proxy_ranges,omitempty"`
	RelayChain         []int         `json:"-"`
	RelayLayers        [][]int       `json:"relay_layers,omitempty"`
	RelayObfs          bool          `json:"relay_obfs,omitempty"`
	PolicyRef          *PolicyRef    `json:"policy_ref,omitempty"`
	Revision           int64         `json:"revision,omitempty"`
}

type L4Backend struct {
	Host string `json:"host"`
	Port int    `json:"port"`
}

type L4ProxyProtocolTuning struct {
	Decode       bool     `json:"decode,omitempty"`
	Send         bool     `json:"send,omitempty"`
	TrustedPeers []string `json:"trusted_peers,omitempty"`
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
	ID              int              `json:"id,omitempty"`
	AgentID         string           `json:"agent_id,omitempty"`
	Name            string           `json:"name,omitempty"`
	Protocol        string           `json:"protocol"`
	ListenHost      string           `json:"listen_host"`
	ListenPort      int              `json:"listen_port"`
	UpstreamHost    string           `json:"-"`
	UpstreamPort    int              `json:"-"`
	Backends        []L4Backend      `json:"backends,omitempty"`
	LoadBalancing   LoadBalancing    `json:"load_balancing,omitempty"`
	Tuning          L4Tuning         `json:"tuning,omitempty"`
	RelayChain      []int            `json:"-"`
	RelayLayers     [][]int          `json:"relay_layers,omitempty"`
	RelayObfs       bool             `json:"relay_obfs,omitempty"`
	ListenMode      string           `json:"listen_mode,omitempty"`
	EgressProfileID *int             `json:"egress_profile_id,omitempty"`
	ProxyEntryAuth  L4ProxyEntryAuth `json:"proxy_entry_auth,omitempty"`
	PolicyRef       *PolicyRef       `json:"policy_ref,omitempty"`
	Revision        int64            `json:"revision,omitempty"`
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
	AllowTransportFallback  bool       `json:"allow_transport_fallback"`
	ObfsMode                string     `json:"obfs_mode"`
	PinSet                  []RelayPin `json:"pin_set"`
	TrustedCACertificateIDs []int      `json:"trusted_ca_certificate_ids"`
	AllowSelfSigned         bool       `json:"allow_self_signed"`
	PKIIdentityID           string     `json:"pki_identity_id,omitempty"`
	PKIIdentityState        string     `json:"pki_identity_state,omitempty"`
	PKICertificateID        string     `json:"pki_certificate_id,omitempty"`
	Tags                    []string   `json:"tags"`
	Revision                int64      `json:"revision"`
}

type EgressProfile struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	Type        string `json:"type"`
	ProxyURL    string `json:"proxy_url,omitempty"`
	Enabled     bool   `json:"enabled"`
	Description string `json:"description,omitempty"`
	Revision    int64  `json:"revision"`
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
	NotAfter     string                     `json:"not_after,omitempty"`
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
