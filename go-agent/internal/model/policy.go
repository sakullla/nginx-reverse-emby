package model

import "encoding/json"

const PolicyABIV1 = "nre:policy/v1"

type PolicyKind string

const (
	PolicyKindIP   PolicyKind = "ip"
	PolicyKindRate PolicyKind = "rate"
	PolicyKindWAF  PolicyKind = "waf"
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
	InputBytes  int64 `json:"input_bytes"`
	OutputBytes int64 `json:"output_bytes"`
}

type PolicyFailurePolicy struct {
	OnError      string `json:"on_error"`
	OnBudget     string `json:"on_budget"`
	Restart      string `json:"restart"`
	CoreFallback string `json:"core_fallback"`
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
	SignatureVerified bool                 `json:"signature_verified"`
	SignerKeyID       string               `json:"signer_key_id"`
	SignerFingerprint string               `json:"signer_fingerprint"`
	ABI               string               `json:"abi"`
	ExtensionPoints   []string             `json:"extension_points"`
	GrantedScopes     []string             `json:"granted_scopes"`
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
