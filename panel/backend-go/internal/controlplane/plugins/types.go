package plugins

import (
	"encoding/json"
	"fmt"

	"gopkg.in/yaml.v3"
)

const (
	PackageManifestFile  = "plugin.yaml"
	ConfigSchemaFile     = "config.schema.json"
	UISchemaFile         = "ui.schema.json"
	PackageDigestFile    = "package.sha256"
	PackageSignatureFile = "package.sig"
)

// DeclarativeUISchemaVersion is the single host-rendered UI contract. Plugin
// packages provide data for these fixed components and actions; they never
// provide executable renderers, markup, links, or event handlers.
const DeclarativeUISchemaVersion = 1

const (
	UIComponentSection  = "section"
	UIComponentText     = "text"
	UIComponentTextarea = "textarea"
	UIComponentSecret   = "secret"
	UIComponentNumber   = "number"
	UIComponentToggle   = "toggle"
	UIComponentSelect   = "select"
	UIComponentNotice   = "notice"

	UIActionSubmit = "submit"
	UIActionReset  = "reset"
)

// Manifest is the single runtime-aware control-plane contract. There is no
// legacy data-only or parallel v2 manifest branch.
type Manifest struct {
	SchemaVersion   int               `yaml:"schema_version" json:"schema_version"`
	ID              string            `yaml:"id" json:"id"`
	Version         string            `yaml:"version" json:"version"`
	Name            string            `yaml:"name" json:"name"`
	Description     string            `yaml:"description,omitempty" json:"description,omitempty"`
	Compatibility   Compatibility     `yaml:"compatibility" json:"compatibility"`
	Runtime         Runtime           `yaml:"runtime" json:"runtime"`
	Artifacts       []Artifact        `yaml:"artifacts" json:"artifacts"`
	ExtensionPoints []string          `yaml:"extension_points" json:"extension_points"`
	Permissions     []Permission      `yaml:"permissions" json:"permissions"`
	ConfigSchema    string            `yaml:"config_schema" json:"config_schema"`
	UISchema        string            `yaml:"ui_schema,omitempty" json:"ui_schema,omitempty"`
	Assets          []string          `yaml:"assets,omitempty" json:"assets,omitempty"`
	ResourceBudget  ResourceBudget    `yaml:"resource_budget" json:"resource_budget"`
	FailurePolicy   FailurePolicy     `yaml:"failure_policy" json:"failure_policy"`
	Signature       Signature         `yaml:"signature" json:"signature"`
	Migrations      []Migration       `yaml:"migrations,omitempty" json:"migrations,omitempty"`
	Cleanup         CleanupPolicy     `yaml:"cleanup" json:"cleanup"`
	UIRouteID       string            `yaml:"ui_route_id,omitempty" json:"ui_route_id,omitempty"`
	Metadata        map[string]string `yaml:"metadata,omitempty" json:"metadata,omitempty"`
}

type Runtime struct {
	Kind       string `yaml:"kind" json:"kind"`
	ABI        string `yaml:"abi" json:"abi"`
	HostScope  string `yaml:"host_scope" json:"host_scope"`
	Entry      string `yaml:"entry" json:"entry"`
	PolicyKind string `yaml:"policy_kind,omitempty" json:"policy_kind,omitempty"`
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

// UnmarshalYAML accepts the concise "resource.read" spelling in addition to
// the object form. Canonicalization is validated after decoding so every YAML
// representation follows the same whitespace rules.
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

type MarketManifest struct {
	SchemaVersion int           `yaml:"schema_version" json:"schema_version"`
	Name          string        `yaml:"name" json:"name"`
	Entries       []MarketEntry `yaml:"plugins" json:"plugins"`
}

type MarketEntry struct {
	ID             string          `yaml:"id" json:"id"`
	Version        string          `yaml:"version" json:"version"`
	Description    string          `yaml:"description,omitempty" json:"description,omitempty"`
	Capabilities   []string        `yaml:"capabilities,omitempty" json:"capabilities,omitempty"`
	Compatibility  Compatibility   `yaml:"compatibility" json:"compatibility"`
	Runtime        RuntimeIndex    `yaml:"runtime" json:"runtime"`
	Artifacts      []ArtifactIndex `yaml:"artifacts" json:"artifacts"`
	PackagePath    string          `yaml:"package" json:"package"`
	PackageSHA256  string          `yaml:"sha256" json:"sha256"`
	SignatureKeyID string          `yaml:"signature_key_id" json:"signature_key_id"`
	Provenance     string          `yaml:"provenance" json:"provenance"`
	Official       bool            `yaml:"official,omitempty" json:"official,omitempty"`
}

// DirectPluginSnapshot is the signed one-package projection for a repository
// with plugin.yaml at its root. It is intentionally not a MarketEntry.
type DirectPluginSnapshot struct {
	ID             string          `json:"id"`
	Version        string          `json:"version"`
	Description    string          `json:"description,omitempty"`
	Capabilities   []string        `json:"capabilities,omitempty"`
	Compatibility  Compatibility   `json:"compatibility"`
	Runtime        RuntimeIndex    `json:"runtime"`
	Artifacts      []ArtifactIndex `json:"artifacts"`
	PackageSHA256  string          `json:"sha256"`
	SignatureKeyID string          `json:"signature_key_id"`
	Provenance     string          `json:"provenance"`
	Official       bool            `json:"official,omitempty"`
}

type ValidatedDirectPlugin struct {
	Projection DirectPluginSnapshot
	Package    ValidatedPackage
}

type RuntimeIndex struct {
	Kind       string `yaml:"kind" json:"kind"`
	ABI        string `yaml:"abi" json:"abi"`
	HostScope  string `yaml:"host_scope" json:"host_scope"`
	PolicyKind string `yaml:"policy_kind,omitempty" json:"policy_kind,omitempty"`
}

type ArtifactIndex struct {
	SHA256 string `yaml:"sha256" json:"sha256"`
	Size   int64  `yaml:"size" json:"size"`
	GOOS   string `yaml:"goos,omitempty" json:"goos,omitempty"`
	GOARCH string `yaml:"goarch,omitempty" json:"goarch,omitempty"`
}

type PackageExpectation struct {
	ID             string
	Version        string
	SHA256         string
	Capabilities   []string
	Compatibility  Compatibility
	Runtime        RuntimeIndex
	Artifacts      []ArtifactIndex
	SignatureKeyID string
}

type ValidatedPackage struct {
	Manifest     Manifest
	Digest       string
	Root         string
	FileCount    int
	Size         int64
	ConfigSchema map[string]any
}

type ValidatedMarket struct {
	Manifest MarketManifest
	Packages []ValidatedPackage
}

type ValidationError struct {
	Code string
	Path string
	Err  error
}

func (e *ValidationError) Error() string {
	if e.Path == "" {
		return fmt.Sprintf("plugin validation %s: %v", e.Code, e.Err)
	}
	return fmt.Sprintf("plugin validation %s at %s: %v", e.Code, e.Path, e.Err)
}

func (e *ValidationError) Unwrap() error { return e.Err }

func canonicalJSON(value any) ([]byte, error) {
	return json.Marshal(value)
}
