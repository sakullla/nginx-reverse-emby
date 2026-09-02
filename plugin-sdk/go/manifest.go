package pluginsdk

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	PluginManifestSchemaVersion = 1
	ExtensionUIRoute            = "ui.route"
	ExtensionResourceGroup      = "resource.group"
)

//go:embed schema/plugin-manifest-v1.schema.json
var pluginManifestSchemaV1 []byte

// PluginManifestSchemaV1 returns an immutable copy of the canonical JSON
// Schema used by publishers and host-side tooling for plugin.yaml v1.
func PluginManifestSchemaV1() []byte {
	return append([]byte(nil), pluginManifestSchemaV1...)
}

func PluginManifestSchemaV1SHA256() string {
	digest := sha256.Sum256(pluginManifestSchemaV1)
	return hex.EncodeToString(digest[:])
}

// Manifest is the one runtime-aware plugin.yaml contract. Runtime-specific
// rules and the official package signing profile are enforced in addition to
// the structural schema by the host validator.
type Manifest struct {
	SchemaVersion        int                             `yaml:"schema_version" json:"schema_version"`
	ID                   string                          `yaml:"id" json:"id"`
	Version              string                          `yaml:"version" json:"version"`
	Name                 string                          `yaml:"name" json:"name"`
	Description          string                          `yaml:"description,omitempty" json:"description,omitempty"`
	Compatibility        Compatibility                   `yaml:"compatibility" json:"compatibility"`
	Runtime              Runtime                         `yaml:"runtime" json:"runtime"`
	Artifacts            []Artifact                      `yaml:"artifacts" json:"artifacts"`
	ExtensionPoints      []string                        `yaml:"extension_points" json:"extension_points"`
	HTTPBackendProviders []HTTPBackendProviderDescriptor `yaml:"http_backend_providers,omitempty" json:"http_backend_providers,omitempty"`
	Permissions          []Permission                    `yaml:"permissions" json:"permissions"`
	ConfigSchema         string                          `yaml:"config_schema" json:"config_schema"`
	UISchema             string                          `yaml:"ui_schema,omitempty" json:"ui_schema,omitempty"`
	Assets               []string                        `yaml:"assets,omitempty" json:"assets,omitempty"`
	ResourceBudget       ResourceBudget                  `yaml:"resource_budget" json:"resource_budget"`
	FailurePolicy        FailurePolicy                   `yaml:"failure_policy" json:"failure_policy"`
	Signature            Signature                       `yaml:"signature" json:"signature"`
	Migrations           []Migration                     `yaml:"migrations,omitempty" json:"migrations,omitempty"`
	Cleanup              CleanupPolicy                   `yaml:"cleanup" json:"cleanup"`
	UIRouteID            string                          `yaml:"ui_route_id,omitempty" json:"ui_route_id,omitempty"`
	ResourceGroupID      string                          `yaml:"resource_group_id,omitempty" json:"resource_group_id,omitempty"`
	Metadata             map[string]string               `yaml:"metadata,omitempty" json:"metadata,omitempty"`
}

type Runtime struct {
	Kind       string         `yaml:"kind" json:"kind"`
	ABI        string         `yaml:"abi" json:"abi"`
	HostScope  string         `yaml:"host_scope" json:"host_scope"`
	HostScopes []string       `yaml:"host_scopes,omitempty" json:"host_scopes,omitempty"`
	Entry      string         `yaml:"entry" json:"entry"`
	PolicyKind string         `yaml:"policy_kind,omitempty" json:"policy_kind,omitempty"`
	Policy     *RuntimePolicy `yaml:"policy,omitempty" json:"policy,omitempty"`
}

// RuntimePolicy is the Agent wasm-policy face nested under a control-plane
// rpc-service. wasm-policy-only packages keep kind=wasm-policy and omit this
// object; they cannot become a control-plane process.
type RuntimePolicy struct {
	Kind           string         `yaml:"kind" json:"kind"`
	ABI            string         `yaml:"abi" json:"abi"`
	HostScope      string         `yaml:"host_scope" json:"host_scope"`
	Entry          string         `yaml:"entry" json:"entry"`
	ResourceBudget ResourceBudget `yaml:"resource_budget" json:"resource_budget"`
	FailurePolicy  FailurePolicy  `yaml:"failure_policy" json:"failure_policy"`
}

// RuntimeDeclaredHostScopes returns the unique host faces this runtime
// declares. host_scope remains the primary face for durable rows; host_scopes
// extends that contract so one plugin_id can also run on additional hosts.
func RuntimeDeclaredHostScopes(runtime Runtime) []string {
	seen := make(map[string]struct{}, 2)
	result := make([]string, 0, 2)
	add := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		if _, exists := seen[value]; exists {
			return
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	add(runtime.HostScope)
	for _, scope := range runtime.HostScopes {
		add(scope)
	}
	return result
}

func RuntimeDeclaresHostScope(runtime Runtime, scope string) bool {
	scope = strings.TrimSpace(scope)
	if scope == "" {
		return false
	}
	for _, declared := range RuntimeDeclaredHostScopes(runtime) {
		if declared == scope {
			return true
		}
	}
	return false
}

// RuntimeAgentFaceHostScope is the host_scope stamped onto Agent execution
// snapshots. host_scope stays the primary durable face; dual-face packages
// still project the Agent face as "agent" so online Agents receive an
// execution instance without a second durable artifact row.
func RuntimeAgentFaceHostScope(runtime Runtime) string {
	if RuntimeDeclaresHostScope(runtime, HostScopeAgent) {
		return HostScopeAgent
	}
	return strings.TrimSpace(runtime.HostScope)
}

// RuntimeDurableArtifactHostScopeMatches is the revision-issuance contract.
// Agent snapshots keep HostScope=agent. Dual-face packages persist only the
// primary host_scope artifact row, so a generation HostScope=agent matches that
// primary durable row when host_scopes includes agent.
func RuntimeDurableArtifactHostScopeMatches(runtime Runtime, generationHostScope, artifactHostScope string) bool {
	generationHostScope = strings.TrimSpace(generationHostScope)
	artifactHostScope = strings.TrimSpace(artifactHostScope)
	if generationHostScope == "" || artifactHostScope == "" {
		return false
	}
	if generationHostScope == artifactHostScope {
		return true
	}
	return generationHostScope == HostScopeAgent &&
		RuntimeDeclaresHostScope(runtime, HostScopeAgent) &&
		artifactHostScope == strings.TrimSpace(runtime.HostScope)
}

type Artifact struct {
	Path   string `yaml:"path" json:"path"`
	SHA256 string `yaml:"sha256" json:"sha256"`
	Size   int64  `yaml:"size" json:"size"`
	Mode   string `yaml:"mode" json:"mode"`
	GOOS   string `yaml:"goos,omitempty" json:"goos,omitempty"`
	GOARCH string `yaml:"goarch,omitempty" json:"goarch,omitempty"`
}

type ResourceBudget struct {
	TimeoutMS   int64 `yaml:"timeout_ms" json:"timeout_ms"`
	MemoryBytes int64 `yaml:"memory_bytes" json:"memory_bytes"`
	Concurrency int   `yaml:"concurrency" json:"concurrency"`
	InputBytes  int64 `yaml:"input_bytes" json:"input_bytes"`
	OutputBytes int64 `yaml:"output_bytes" json:"output_bytes"`
	CPUMillis   int64 `yaml:"cpu_millis,omitempty" json:"cpu_millis,omitempty"`
	Restarts    int   `yaml:"restarts,omitempty" json:"restarts,omitempty"`
}

type FailurePolicy struct {
	OnError      string `yaml:"on_error" json:"on_error"`
	OnBudget     string `yaml:"on_budget" json:"on_budget"`
	Restart      string `yaml:"restart" json:"restart"`
	CoreFallback string `yaml:"core_fallback" json:"core_fallback"`
}

type Signature struct {
	Algorithm string `yaml:"algorithm" json:"algorithm"`
	KeyID     string `yaml:"key_id" json:"key_id"`
	File      string `yaml:"file" json:"file"`
}

type Compatibility struct {
	Host  string `yaml:"host" json:"host"`
	Agent string `yaml:"agent" json:"agent"`
}

type Permission struct {
	Name     string `yaml:"name" json:"name"`
	Resource string `yaml:"resource,omitempty" json:"resource,omitempty"`
}

// UnmarshalYAML retains the concise scalar spelling for existing custom
// packages. Official publishers should emit the canonical object spelling.
func (p *Permission) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode {
		p.Name = node.Value
		return nil
	}
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("permission must be a string or object")
	}
	for index := 0; index < len(node.Content); index += 2 {
		key := node.Content[index].Value
		if key != "name" && key != "resource" {
			return fmt.Errorf("unknown permission field %q", key)
		}
	}
	type plain Permission
	var value plain
	if err := node.Decode(&value); err != nil {
		return err
	}
	*p = Permission(value)
	return nil
}

type Migration struct {
	From string `yaml:"from" json:"from"`
	To   string `yaml:"to" json:"to"`
	File string `yaml:"file" json:"file"`
}

type CleanupPolicy struct {
	Instances   string `yaml:"instances" json:"instances"`
	Config      string `yaml:"config" json:"config"`
	OwnedData   string `yaml:"owned_data" json:"owned_data"`
	Grants      string `yaml:"grants" json:"grants"`
	SharedRefs  string `yaml:"shared_refs" json:"shared_refs"`
	AuditEvents string `yaml:"audit_events" json:"audit_events"`
}
