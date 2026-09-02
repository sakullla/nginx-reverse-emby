package model

import "encoding/json"

const PolicyABIV1 = "nre:policy/v1"

type PolicyKind string

const (
	PolicyKindIP   PolicyKind = "ip"
	PolicyKindRate PolicyKind = "rate"
	PolicyKindWAF  PolicyKind = "waf"
)

const (
	PolicyOverlayModeObserve = "observe"
	PolicyOverlayModeDeny    = "deny"
)

func (kind PolicyKind) Valid() bool {
	switch kind {
	case PolicyKindIP, PolicyKindRate, PolicyKindWAF:
		return true
	default:
		return false
	}
}

// PolicyRef is the only policy attachment carried by an HTTP or L4 rule. The
// referenced PluginPolicy owns the ordered IP/rate/WAF chain; Overlay is
// request-rule-local input and never mutates that shared policy.
type PolicyRef struct {
	ID      string          `json:"id"`
	Overlay json.RawMessage `json:"overlay,omitempty"`
}

type PolicyResourceBudget struct {
	TimeoutMS   int64 `json:"timeout_ms"`
	MemoryBytes int64 `json:"memory_bytes"`
	Concurrency int   `json:"concurrency"`
	// InputBytes and OutputBytes count complete deterministic nre:policy/v1
	// protobuf wire frames, including tags, lengths, envelopes, and host-added
	// metadata. They are not raw Config, Overlay, or response payload limits.
	InputBytes  int64 `json:"input_bytes"`
	OutputBytes int64 `json:"output_bytes"`
}

type PolicyFailurePolicy struct {
	OnError      string `json:"on_error"`
	OnBudget     string `json:"on_budget"`
	Restart      string `json:"restart"`
	CoreFallback string `json:"core_fallback"`
}

// PolicyArtifactSource is the location-independent, durable artifact identity
// issued by the control plane. ArtifactPath is populated only after the Agent
// has materialized and verified this source into its own cache.
type PolicyArtifactSource struct {
	ArtifactID      string `json:"artifact_id"`
	PackageIdentity string `json:"package_identity"`
	PackageDigest   string `json:"package_digest"`
	RelativePath    string `json:"relative_path"`
	SHA256          string `json:"sha256"`
	SizeBytes       int64  `json:"size_bytes"`
}

// PolicyStage is a single authority in a shared policy chain. ArtifactPath is
// a host-verified, generation-scoped reference; policy evaluation never opens
// or resolves it itself.
type PolicyStage struct {
	Kind              PolicyKind           `json:"kind"`
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

// PluginPolicy is immutable within one Snapshot generation. Stages are
// validated into the only supported order: IP, rate, WAF.
type PluginPolicy struct {
	ID       string        `json:"id"`
	Revision int64         `json:"revision"`
	Stages   []PolicyStage `json:"stages"`
}
