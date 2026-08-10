package model

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"
)

const (
	PluginRuntimeWASMPolicy = "wasm-policy"
	PluginRuntimeRPCService = "rpc-service"
	PluginRPCABIV1          = "nre:rpc/v1"
)

// PluginGeneration is the complete, target-specific runtime projection for a
// single enabled plugin instance. It deliberately excludes marketplace and
// package-manifest metadata. LocalPath is populated only after the Agent has
// independently materialized and verified Artifact.
type PluginGeneration struct {
	ID              string                   `json:"id"`
	InstanceID      string                   `json:"instance_id"`
	OperationID     string                   `json:"operation_id,omitempty"`
	Revision        int64                    `json:"revision"`
	PluginID        string                   `json:"plugin_id"`
	PluginVersion   string                   `json:"plugin_version"`
	PackageDigest   string                   `json:"package_digest"`
	Runtime         PluginRuntimeDescriptor  `json:"runtime"`
	Artifact        PluginArtifactDescriptor `json:"artifact"`
	ExtensionPoints []string                 `json:"extension_points"`
	ConfigVersion   uint64                   `json:"config_version"`
	Config          json.RawMessage          `json:"config"`
	Grants          []PluginGrantProjection  `json:"grants"`
	SecretHandles   []PluginSecretHandle     `json:"secret_handles"`
	ResourceBudget  PluginResourceBudget     `json:"resource_budget"`
	Target          PluginTargetBinding      `json:"target"`
	FailurePolicy   PluginFailurePolicy      `json:"failure_policy"`
}

type PluginRuntimeDescriptor struct {
	Kind      string `json:"kind"`
	ABI       string `json:"abi"`
	HostScope string `json:"host_scope"`
	Entry     string `json:"entry"`
}

type PluginArtifactDescriptor struct {
	ArtifactID        string `json:"artifact_id"`
	PackageIdentity   string `json:"package_identity"`
	RelativePath      string `json:"relative_path"`
	SHA256            string `json:"sha256"`
	SizeBytes         int64  `json:"size_bytes"`
	Mode              string `json:"mode"`
	GOOS              string `json:"goos,omitempty"`
	GOARCH            string `json:"goarch,omitempty"`
	SignatureVerified bool   `json:"signature_verified"`
	SignerKeyID       string `json:"signer_key_id"`
	SignerFingerprint string `json:"signer_fingerprint"`
	LocalPath         string `json:"local_path,omitempty"`
}

type PluginGrantProjection struct {
	Name         string `json:"name"`
	ResourceKind string `json:"resource_kind,omitempty"`
	ResourceID   string `json:"resource_id,omitempty"`
}

type PluginSecretHandle struct {
	ID      string `json:"id"`
	Version uint64 `json:"version"`
	Digest  string `json:"digest"`
	Purpose string `json:"purpose,omitempty"`
}

type PluginResourceBudget struct {
	TimeoutMS   int64 `json:"timeout_ms"`
	MemoryBytes int64 `json:"memory_bytes"`
	Concurrency int   `json:"concurrency"`
	InputBytes  int64 `json:"input_bytes"`
	OutputBytes int64 `json:"output_bytes"`
	CPUMillis   int64 `json:"cpu_millis,omitempty"`
	Restarts    int   `json:"restarts,omitempty"`
}

type PluginTargetBinding struct {
	Kind            string `json:"kind"`
	ID              string `json:"id"`
	ResourceGroupID string `json:"resource_group_id"`
	Version         uint64 `json:"version"`
}

type PluginFailurePolicy struct {
	OnError      string `json:"on_error"`
	OnBudget     string `json:"on_budget"`
	Restart      string `json:"restart"`
	CoreFallback string `json:"core_fallback"`
}

// PluginRuntimeStatus is fenced by the lifecycle operation, immutable
// revision, and package digest. A controller must match all three values before
// treating the observation as completion of a pending lifecycle mutation.
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

func ValidatePluginGenerations(snapshot Snapshot, materialized bool) error {
	seen := make(map[string]struct{}, len(snapshot.PluginGenerations))
	seenIDs := make(map[string]struct{}, len(snapshot.PluginGenerations))
	for index := range snapshot.PluginGenerations {
		generation := snapshot.PluginGenerations[index]
		if _, duplicate := seen[generation.InstanceID]; duplicate {
			return fmt.Errorf("plugin generation %d duplicates instance %q", index, generation.InstanceID)
		}
		seen[generation.InstanceID] = struct{}{}
		if _, duplicate := seenIDs[generation.ID]; duplicate {
			return fmt.Errorf("plugin generation %d duplicates generation id %q", index, generation.ID)
		}
		seenIDs[generation.ID] = struct{}{}
		if err := generation.Validate(snapshot.Revision, materialized); err != nil {
			return fmt.Errorf("plugin generation %d (%q): %w", index, generation.InstanceID, err)
		}
	}
	return nil
}

func (generation PluginGeneration) Validate(snapshotRevision int64, materialized bool) error {
	for name, value := range map[string]string{
		"generation id": generation.ID, "instance id": generation.InstanceID, "operation id": generation.OperationID,
		"plugin id": generation.PluginID, "plugin version": generation.PluginVersion,
	} {
		if !validPluginIdentity(value) {
			return fmt.Errorf("%s is invalid", name)
		}
	}
	if snapshotRevision <= 0 || generation.Revision != snapshotRevision {
		return errors.New("revision does not match the immutable snapshot")
	}
	if !validPluginSHA256(generation.PackageDigest) {
		return errors.New("package digest is invalid")
	}
	if generation.Runtime.HostScope != "agent" {
		return errors.New("runtime host scope must be agent")
	}
	if !validPluginRelativePath(generation.Runtime.Entry) || generation.Runtime.Entry != generation.Artifact.RelativePath {
		return errors.New("runtime entry does not match the selected artifact")
	}
	switch generation.Runtime.Kind {
	case PluginRuntimeRPCService:
		if generation.Runtime.ABI != PluginRPCABIV1 || generation.Artifact.Mode != "executable" ||
			!validPluginIdentity(generation.Artifact.GOOS) || !validPluginIdentity(generation.Artifact.GOARCH) {
			return errors.New("rpc-service runtime projection is invalid")
		}
	case PluginRuntimeWASMPolicy:
		return errors.New("wasm-policy must use the compatibility plugin_policies projection")
	default:
		return errors.New("runtime kind is unsupported")
	}
	if !validPluginIdentity(generation.Artifact.ArtifactID) || strings.ContainsAny(generation.Artifact.ArtifactID, "/\\") ||
		!validPluginIdentity(generation.Artifact.PackageIdentity) ||
		!validPluginRelativePath(generation.Artifact.RelativePath) || !validPluginSHA256(generation.Artifact.SHA256) ||
		generation.Artifact.SizeBytes <= 0 || !generation.Artifact.SignatureVerified ||
		!validPluginIdentity(generation.Artifact.SignerKeyID) || !validPluginSHA256(generation.Artifact.SignerFingerprint) {
		return errors.New("artifact identity is invalid")
	}
	if materialized != (strings.TrimSpace(generation.Artifact.LocalPath) != "") {
		return errors.New("artifact materialization state is invalid")
	}
	if generation.ConfigVersion == 0 || len(generation.Config) == 0 || !json.Valid(generation.Config) || generation.Config[0] != '{' {
		return errors.New("config must be a JSON object")
	}
	if err := validatePluginStringSet("extension point", generation.ExtensionPoints, knownPluginExtensionPoint); err != nil {
		return err
	}
	if len(generation.ExtensionPoints) == 0 {
		return errors.New("extension points are required")
	}
	if err := validatePluginGrants(generation.Grants); err != nil {
		return err
	}
	if err := validatePluginSecretHandles(generation.SecretHandles); err != nil {
		return err
	}
	if err := validatePluginBudget(generation.Runtime.Kind, generation.ResourceBudget); err != nil {
		return err
	}
	if !validPluginIdentity(generation.Target.Kind) || !validPluginIdentity(generation.Target.ID) ||
		!validPluginIdentity(generation.Target.ResourceGroupID) || generation.Target.Version == 0 {
		return errors.New("target binding is invalid")
	}
	if !slices.Contains([]string{"fail-open", "fail-closed", "degraded"}, generation.FailurePolicy.OnError) ||
		!slices.Contains([]string{"fail-open", "fail-closed"}, generation.FailurePolicy.OnBudget) ||
		!slices.Contains([]string{"never", "on-failure"}, generation.FailurePolicy.Restart) ||
		generation.FailurePolicy.CoreFallback != "preserve" {
		return errors.New("failure policy is invalid")
	}
	return nil
}

func validPluginIdentity(value string) bool {
	if value == "" || len(value) > 256 || value != strings.TrimSpace(value) {
		return false
	}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || strings.ContainsRune("._:@+-/", r) {
			continue
		}
		return false
	}
	return true
}

func validPluginSHA256(value string) bool {
	if len(value) != sha256.Size*2 || value != strings.ToLower(value) {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size
}

func validPluginRelativePath(value string) bool {
	return value != "" && value == strings.TrimSpace(value) && !strings.Contains(value, "\\") &&
		path.Clean(value) == value && value != "." && !strings.HasPrefix(value, "/") &&
		value != ".." && !strings.HasPrefix(value, "../")
}

func validatePluginStringSet(label string, values []string, known func(string) bool) error {
	seen := map[string]struct{}{}
	for _, value := range values {
		if !known(value) {
			return fmt.Errorf("%s %q is not supported", label, value)
		}
		if _, duplicate := seen[value]; duplicate {
			return fmt.Errorf("%s %q is duplicated", label, value)
		}
		seen[value] = struct{}{}
	}
	return nil
}

func knownPluginExtensionPoint(value string) bool {
	switch value {
	case "http.request", "http.response", "l4.accept", "policy.provider", "dns.provider", "container.provider", "tunnel.provider", "ui.route":
		return true
	default:
		return false
	}
}

func knownPluginGrant(value string) bool {
	switch value {
	case "agent.read", "agent.configure", "event.emit", "http.inspect", "http.respond", "l4.inspect", "l4.respond",
		"policy.read", "policy.write", "secret.use", "storage.read", "storage.write", "container.read", "container.manage",
		"dns.manage", "policy.atomic_state", "policy.monotonic_clock", "policy.trusted_source",
		"service.revocable_resource_handle", "ui.dynamic_actions":
		return true
	default:
		return false
	}
}

func validatePluginGrants(grants []PluginGrantProjection) error {
	seen := map[string]struct{}{}
	for _, grant := range grants {
		if !knownPluginGrant(grant.Name) || (grant.ResourceKind == "") != (grant.ResourceID == "") ||
			(grant.ResourceKind != "" && (!validPluginIdentity(grant.ResourceKind) || !validPluginIdentity(grant.ResourceID))) {
			return fmt.Errorf("grant %q is invalid", grant.Name)
		}
		key := grant.Name + "\x00" + grant.ResourceKind + "\x00" + grant.ResourceID
		if _, duplicate := seen[key]; duplicate {
			return fmt.Errorf("grant %q is duplicated", grant.Name)
		}
		seen[key] = struct{}{}
	}
	return nil
}

func validatePluginSecretHandles(handles []PluginSecretHandle) error {
	seen := map[string]struct{}{}
	for _, handle := range handles {
		if !validPluginIdentity(handle.ID) || handle.Version == 0 || !validPluginSHA256(handle.Digest) ||
			(handle.Purpose != "" && !validPluginIdentity(handle.Purpose)) {
			return errors.New("secret handle is invalid")
		}
		if _, duplicate := seen[handle.ID]; duplicate {
			return fmt.Errorf("secret handle %q is duplicated", handle.ID)
		}
		seen[handle.ID] = struct{}{}
	}
	return nil
}

func validatePluginBudget(kind string, budget PluginResourceBudget) error {
	if budget.TimeoutMS <= 0 || budget.TimeoutMS > 300000 || budget.MemoryBytes < 65536 || budget.MemoryBytes > 4<<30 ||
		budget.Concurrency <= 0 || budget.Concurrency > 4096 || budget.InputBytes <= 0 || budget.InputBytes > 16<<20 ||
		budget.OutputBytes <= 0 || budget.OutputBytes > 16<<20 {
		return errors.New("resource budget is outside Agent bounds")
	}
	if kind == PluginRuntimeRPCService && (budget.CPUMillis <= 0 || budget.CPUMillis > 100000 || budget.Restarts < 0 || budget.Restarts > 100) {
		return errors.New("rpc-service resource budget is outside Agent bounds")
	}
	if kind == PluginRuntimeWASMPolicy && (budget.CPUMillis != 0 || budget.Restarts != 0) {
		return errors.New("wasm-policy resource budget contains rpc-only fields")
	}
	return nil
}
