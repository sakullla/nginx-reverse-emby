package model

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"slices"
	"sort"
	"strconv"
	"strings"

	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
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
	ID                   string                                    `json:"id"`
	InstanceID           string                                    `json:"instance_id"`
	OperationID          string                                    `json:"operation_id,omitempty"`
	Revision             int64                                     `json:"revision"`
	PluginID             string                                    `json:"plugin_id"`
	PluginVersion        string                                    `json:"plugin_version"`
	PackageDigest        string                                    `json:"package_digest"`
	Runtime              PluginRuntimeDescriptor                   `json:"runtime"`
	Artifact             PluginArtifactDescriptor                  `json:"artifact"`
	ExtensionPoints      []string                                  `json:"extension_points"`
	RequiredFeatures     []string                                  `json:"required_features"`
	HTTPBackendProviders []pluginsdk.HTTPBackendProviderDescriptor `json:"http_backend_providers,omitempty"`
	ConfigVersion        uint64                                    `json:"config_version"`
	Config               json.RawMessage                           `json:"config"`
	Grants               []PluginGrantProjection                   `json:"grants"`
	SecretHandles        []PluginSecretHandle                      `json:"secret_handles"`
	ResourceBudget       PluginResourceBudget                      `json:"resource_budget"`
	Target               PluginTargetBinding                       `json:"target"`
	FailurePolicy        PluginFailurePolicy                       `json:"failure_policy"`
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

// PluginGenerationSecretHandle preserves the controller wire name while the
// Agent keeps PluginSecretHandle as its established internal symbol.
type PluginGenerationSecretHandle = PluginSecretHandle

type PluginSecretRedemptionRequest struct {
	Revision       uint64                         `json:"revision"`
	GenerationID   string                         `json:"generation_id"`
	InstanceID     string                         `json:"instance_id"`
	PluginID       string                         `json:"plugin_id"`
	OperationID    string                         `json:"operation_id"`
	PackageDigest  string                         `json:"package_digest"`
	ArtifactDigest string                         `json:"artifact_digest"`
	Handles        []PluginGenerationSecretHandle `json:"handles"`
}

type PluginRedeemedSecret struct {
	ID      string `json:"id"`
	Version uint64 `json:"version"`
	Digest  string `json:"digest"`
	Purpose string `json:"purpose"`
	Value   string `json:"value"`
}

type PluginSecretRedemptionResponse struct {
	Secrets []PluginRedeemedSecret `json:"secrets"`
}

func (request PluginSecretRedemptionRequest) Validate() error {
	if request.Revision == 0 || !validPluginIdentity(request.GenerationID) || !validPluginIdentity(request.InstanceID) ||
		!validPluginIdentity(request.PluginID) || !validPluginIdentity(request.OperationID) ||
		!validPluginSHA256(request.PackageDigest) || !validPluginSHA256(request.ArtifactDigest) || len(request.Handles) == 0 {
		return errors.New("plugin secret redemption fence is invalid")
	}
	return validatePluginSecretHandles(request.InstanceID, request.Handles)
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

// PluginDependencyEdge is an immutable direct dependency from one enabled
// core traffic consumer to an Agent-hosted rpc-service instance. The target
// fence prevents an edge issued for another Agent, resource group, or config
// generation from making a local provider required.
type PluginDependencyEdge struct {
	Consumer           PluginDependencyConsumer `json:"consumer"`
	ProviderInstanceID string                   `json:"provider_instance_id"`
	Target             PluginDependencyTarget   `json:"target"`
}

type PluginDependencyConsumer struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
	// ResourceGroupID and Version are the signed ownership fence resolved by
	// the controller. Version is an opaque lowercase SHA-256 digest; the Agent
	// validates its canonical shape but does not reproduce controller storage
	// identity inputs such as binding_id.
	ResourceGroupID string `json:"resource_group_id"`
	Version         string `json:"version"`
}

type PluginDependencyTarget struct {
	AgentID         string `json:"agent_id"`
	ResourceGroupID string `json:"resource_group_id"`
	Version         uint64 `json:"version"`
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

const (
	MaxPluginRuntimeLogEntries = 32
	MaxPluginRuntimeLogMessage = 4 << 10
	MaxPendingPluginLogReports = 256
)

type PluginRuntimeLogEntry struct {
	Level     string `json:"level"`
	Message   string `json:"message"`
	Truncated bool   `json:"truncated"`
}

type PluginRuntimeLogReport struct {
	Revision       int64                   `json:"revision"`
	GenerationID   string                  `json:"generation_id"`
	InstanceID     string                  `json:"instance_id"`
	PluginID       string                  `json:"plugin_id"`
	AgentID        string                  `json:"agent_id"`
	PackageDigest  string                  `json:"package_digest"`
	ArtifactDigest string                  `json:"artifact_digest"`
	Sequence       uint64                  `json:"sequence"`
	Entries        []PluginRuntimeLogEntry `json:"entries"`
}

func (report PluginRuntimeLogReport) Validate() error {
	if report.Revision <= 0 || report.Sequence == 0 ||
		!validPluginIdentity(report.GenerationID) || !validPluginIdentity(report.InstanceID) ||
		!validPluginIdentity(report.PluginID) || !validPluginIdentity(report.AgentID) ||
		!validPluginSHA256(report.PackageDigest) || !validPluginSHA256(report.ArtifactDigest) {
		return errors.New("plugin runtime log report fence is invalid")
	}
	if len(report.Entries) == 0 || len(report.Entries) > MaxPluginRuntimeLogEntries {
		return errors.New("plugin runtime log report entries are invalid")
	}
	for _, entry := range report.Entries {
		if entry.Level != "debug" && entry.Level != "info" && entry.Level != "warn" && entry.Level != "error" {
			return errors.New("plugin runtime log entry level is invalid")
		}
		if entry.Message == "" || len(entry.Message) > MaxPluginRuntimeLogMessage {
			return errors.New("plugin runtime log entry message is invalid")
		}
	}
	return nil
}

func ClonePluginRuntimeLogReports(reports []PluginRuntimeLogReport) []PluginRuntimeLogReport {
	if reports == nil {
		return nil
	}
	cloned := append([]PluginRuntimeLogReport(nil), reports...)
	for index := range cloned {
		cloned[index].Entries = append([]PluginRuntimeLogEntry(nil), reports[index].Entries...)
	}
	return cloned
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
	return ValidatePluginDependencies(snapshot)
}

// RequiredPluginInstanceIDs validates the signed dependency graph before
// returning its stable provider set, so required state cannot be derived from
// dangling, cross-target, or otherwise malformed edges.
func RequiredPluginInstanceIDs(snapshot Snapshot) ([]string, error) {
	if err := ValidatePluginDependencies(snapshot); err != nil {
		return nil, err
	}
	requiredInstances := make(map[string]struct{})
	for _, edge := range snapshot.PluginDependencies {
		if edge.ProviderInstanceID != "" {
			requiredInstances[edge.ProviderInstanceID] = struct{}{}
		}
	}
	ids := make([]string, 0, len(requiredInstances))
	for id := range requiredInstances {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids, nil
}

func ValidatePluginDependencies(snapshot Snapshot) error {
	providers := make(map[string]PluginGeneration, len(snapshot.PluginGenerations))
	for _, generation := range snapshot.PluginGenerations {
		providers[generation.InstanceID] = generation
	}
	seen := make(map[string]struct{}, len(snapshot.PluginDependencies))
	for index, edge := range snapshot.PluginDependencies {
		if !validPluginIdentity(edge.ProviderInstanceID) || !validPluginIdentity(edge.Consumer.Kind) ||
			!validPluginIdentity(edge.Consumer.ID) || !validPluginIdentity(edge.Consumer.ResourceGroupID) ||
			!validPluginIdentity(edge.Target.AgentID) ||
			!validPluginIdentity(edge.Target.ResourceGroupID) || edge.Target.Version == 0 {
			return fmt.Errorf("plugin dependency %d has an invalid identity", index)
		}
		if !validPluginSHA256(edge.Consumer.Version) {
			return fmt.Errorf("plugin dependency %d has an invalid consumer authority version", index)
		}
		provider, exists := providers[edge.ProviderInstanceID]
		if !exists {
			return fmt.Errorf("plugin dependency %d references missing provider %q", index, edge.ProviderInstanceID)
		}
		if provider.Runtime.Kind != PluginRuntimeRPCService || provider.Runtime.HostScope != "agent" {
			return fmt.Errorf("plugin dependency %d provider %q is not rpc-service", index, edge.ProviderInstanceID)
		}
		if provider.Target.Kind != "agent" || provider.Target.ID != edge.Target.AgentID ||
			provider.Target.ResourceGroupID != edge.Target.ResourceGroupID || provider.Target.Version != edge.Target.Version {
			return fmt.Errorf("plugin dependency %d provider %q crosses its target fence", index, edge.ProviderInstanceID)
		}
		if edge.Consumer.ResourceGroupID != provider.Target.ResourceGroupID {
			return fmt.Errorf("plugin dependency %d consumer %s/%s crosses provider resource group", index, edge.Consumer.Kind, edge.Consumer.ID)
		}
		key := edge.Consumer.Kind + "\x00" + edge.Consumer.ID + "\x00" + edge.ProviderInstanceID
		if _, duplicate := seen[key]; duplicate {
			return fmt.Errorf("plugin dependency %d duplicates consumer %s/%s and provider %q", index, edge.Consumer.Kind, edge.Consumer.ID, edge.ProviderInstanceID)
		}
		seen[key] = struct{}{}
		if err := validatePluginDependencyConsumer(snapshot, edge, provider); err != nil {
			return fmt.Errorf("plugin dependency %d: %w", index, err)
		}
	}
	expected := make(map[string]struct{})
	for _, rule := range snapshot.Rules {
		if len(rule.Backends) > 0 {
			if err := pluginsdk.ValidateHTTPBackends(rule.Backends); err != nil {
				return fmt.Errorf("HTTP rule %d backends: %w", rule.ID, err)
			}
		}
		for _, backend := range rule.Backends {
			if backend.Kind == pluginsdk.HTTPBackendKindPluginProvider && backend.PluginProvider != nil {
				expected[strconv.Itoa(rule.ID)+"\x00"+backend.PluginProvider.InstanceID] = struct{}{}
			}
		}
	}
	actual := make(map[string]struct{})
	for _, edge := range snapshot.PluginDependencies {
		provider, found := providers[edge.ProviderInstanceID]
		if edge.Consumer.Kind == "http_rule" && found && slices.Contains(provider.ExtensionPoints, pluginsdk.ExtensionHTTPBackendProvider) {
			actual[edge.Consumer.ID+"\x00"+edge.ProviderInstanceID] = struct{}{}
		}
	}
	for key := range expected {
		if _, found := actual[key]; !found {
			return errors.New("HTTP provider relationship is missing its dependency")
		}
	}
	return nil
}

func validatePluginDependencyConsumer(snapshot Snapshot, edge PluginDependencyEdge, provider PluginGeneration) error {
	id, err := strconv.Atoi(edge.Consumer.ID)
	if err != nil || id <= 0 || strconv.Itoa(id) != edge.Consumer.ID {
		return errors.New("consumer id must be a positive canonical decimal")
	}
	switch edge.Consumer.Kind {
	case "http_rule":
		hasProviderContract := slices.Contains(provider.ExtensionPoints, pluginsdk.ExtensionHTTPBackendProvider) && slices.Contains(provider.RequiredFeatures, pluginsdk.RPCFeatureHTTPBackendProviderV1)
		hasExplicitBindingContract := slices.Contains(provider.ExtensionPoints, "http.request") || slices.Contains(provider.ExtensionPoints, "http.response")
		if !hasProviderContract && !hasExplicitBindingContract {
			return errors.New("http rule provider lacks an HTTP dependency contract")
		}
		for _, rule := range snapshot.Rules {
			if rule.ID == id && rule.Enabled {
				if rule.AgentID != "" && rule.AgentID != edge.Target.AgentID {
					return errors.New("http rule belongs to another Agent")
				}
				if hasProviderContract {
					for _, backend := range rule.Backends {
						if backend.Kind != pluginsdk.HTTPBackendKindPluginProvider || backend.PluginProvider == nil || backend.PluginProvider.InstanceID != provider.InstanceID {
							continue
						}
						for _, descriptor := range provider.HTTPBackendProviders {
							if descriptor.ID == backend.PluginProvider.ProviderID {
								return nil
							}
						}
						return errors.New("http rule references an undeclared provider id")
					}
				}
				if hasExplicitBindingContract {
					return nil
				}
				return errors.New("http rule does not own this provider relationship")
			}
		}
		return fmt.Errorf("http rule %d is missing or disabled", id)
	case "l4_rule":
		if !slices.Contains(provider.ExtensionPoints, "l4.accept") {
			return errors.New("l4 rule provider lacks l4.accept")
		}
		for _, rule := range snapshot.L4Rules {
			if rule.ID == id && rule.Enabled {
				if rule.AgentID != "" && rule.AgentID != edge.Target.AgentID {
					return errors.New("l4 rule belongs to another Agent")
				}
				return nil
			}
		}
		return fmt.Errorf("l4 rule %d is missing or disabled", id)
	default:
		return fmt.Errorf("consumer kind %q is unsupported", edge.Consumer.Kind)
	}
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
	if err := validatePluginStringSet("required feature", generation.RequiredFeatures, knownPluginRequiredFeature); err != nil {
		return err
	}
	hasProviderExtension := slices.Contains(generation.ExtensionPoints, pluginsdk.ExtensionHTTPBackendProvider)
	if hasProviderExtension != (len(generation.HTTPBackendProviders) > 0) {
		return errors.New("HTTP backend provider extension and descriptors must be declared together")
	}
	if hasProviderExtension {
		if !slices.Contains(generation.RequiredFeatures, pluginsdk.RPCFeatureHTTPBackendProviderV1) {
			return errors.New("HTTP backend provider RPC feature is required")
		}
		if err := pluginsdk.ValidateHTTPBackendProviderDescriptors(generation.HTTPBackendProviders); err != nil {
			return err
		}
	}
	if err := validatePluginGrants(generation.Grants); err != nil {
		return err
	}
	if err := validatePluginSecretHandles(generation.InstanceID, generation.SecretHandles); err != nil {
		return err
	}
	if err := validatePluginBudget(generation.Runtime.Kind, generation.ResourceBudget); err != nil {
		return err
	}
	if !validPluginIdentity(generation.Target.Kind) || !validPluginIdentity(generation.Target.ID) ||
		!validPluginIdentity(generation.Target.ResourceGroupID) || generation.Target.Version == 0 || generation.Target.Version != generation.ConfigVersion {
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
	case "http.request", "http.response", pluginsdk.ExtensionHTTPBackendProvider, "l4.accept", "policy.provider", "dns.provider", "container.provider", "tunnel.provider", "ui.route":
		return true
	default:
		return false
	}
}

func knownPluginRequiredFeature(value string) bool {
	return value == pluginsdk.RPCFeatureDurableActionsV1 || value == pluginsdk.RPCFeatureHTTPBackendProviderV1
}

func knownPluginGrant(value string) bool {
	switch value {
	case "agent.read", "agent.configure", "event.emit", "http.inspect", "http.respond", "l4.inspect", "l4.respond",
		"policy.read", "policy.write", "secret.use", "storage.read", "storage.write", "container.read", "container.manage",
		"dns.manage", string(pluginsdk.CapabilityContainerCompose), string(pluginsdk.CapabilityHTTPRule),
		string(pluginsdk.CapabilityUIDynamic):
		return true
	default:
		return pluginsdk.HostCapability(value).Validate() == nil
	}
}

func validatePluginGrants(grants []PluginGrantProjection) error {
	seen := map[string]struct{}{}
	for _, grant := range grants {
		if !knownPluginGrant(grant.Name) ||
			(grant.ResourceKind != "" && (!validPluginIdentity(grant.ResourceKind) || grant.ResourceID == "")) ||
			(grant.ResourceID != "" && !validPluginIdentity(grant.ResourceID)) {
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

func validatePluginSecretHandles(instanceID string, handles []PluginSecretHandle) error {
	if len(handles) > 64 {
		return errors.New("secret handle set exceeds Agent bounds")
	}
	seen := map[string]struct{}{}
	for _, handle := range handles {
		purposeValid := handle.Purpose == "" || validPluginIdentity(handle.Purpose)
		if strings.HasPrefix(handle.Purpose, "plugin-config:") {
			purposeValid = validPluginConfigSecretPurpose(instanceID, handle.Purpose)
		}
		if !validPluginIdentity(handle.ID) || handle.Version == 0 || !validPluginSHA256(handle.Digest) || !purposeValid {
			return errors.New("secret handle is invalid")
		}
		if _, duplicate := seen[handle.ID]; duplicate {
			return fmt.Errorf("secret handle %q is duplicated", handle.ID)
		}
		seen[handle.ID] = struct{}{}
	}
	return nil
}

func validPluginConfigSecretPurpose(instanceID, purpose string) bool {
	prefix := "plugin-config:" + instanceID + ":"
	pointer := strings.TrimPrefix(purpose, prefix)
	if pointer == purpose || len(pointer) == 0 || len(pointer) > 2048 || pointer[0] != '/' {
		return false
	}
	for index := 0; index < len(pointer); index++ {
		if pointer[index] != '~' {
			continue
		}
		if index+1 >= len(pointer) || (pointer[index+1] != '0' && pointer[index+1] != '1') {
			return false
		}
		index++
	}
	return true
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
