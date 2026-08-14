package plugins

import (
	"encoding/json"
	"fmt"

	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
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
	UIComponentArray    = "array"

	UIActionSubmit  = "submit"
	UIActionReset   = "reset"
	UIActionDynamic = "dynamic"
)

// The SDK owns the extended schema_version 1 vocabulary. Aliases keep the
// control-plane enforcement on the same canonical constants.
const (
	UIComponentGrid        = pluginsdk.UIComponentGrid
	UIComponentRadio       = pluginsdk.UIComponentRadio
	UIComponentMultiselect = pluginsdk.UIComponentMultiselect
	UIComponentKeyValue    = pluginsdk.UIComponentKeyValue
)

// The SDK owns the one plugin.yaml v1 contract. Aliases keep the existing
// control-plane API stable while preventing a second field/tag definition.
type Manifest = pluginsdk.Manifest
type Runtime = pluginsdk.Runtime
type Artifact = pluginsdk.Artifact
type ResourceBudget = pluginsdk.ResourceBudget
type FailurePolicy = pluginsdk.FailurePolicy
type Signature = pluginsdk.Signature
type Compatibility = pluginsdk.Compatibility
type Permission = pluginsdk.Permission
type Migration = pluginsdk.Migration
type CleanupPolicy = pluginsdk.CleanupPolicy

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
	Manifest       Manifest
	Digest         string
	Root           string
	FileCount      int
	Size           int64
	ConfigSchema   map[string]any
	DynamicActions []pluginsdk.DynamicAction
	DeclarativeUI  *DeclarativeUIDocument
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
