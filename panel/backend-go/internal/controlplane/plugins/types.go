package plugins

import (
	"encoding/json"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	PackageManifestFile = "plugin.yaml"
	ConfigSchemaFile    = "config.schema.json"
	PackageDigestFile   = "package.sha256"
)

// Manifest is the single control-plane contract for a data-only plugin package.
// Plugins deliberately have no executable entry point: runtime behavior is
// selected through allow-listed extension points and declarative assets.
type Manifest struct {
	SchemaVersion   int               `yaml:"schema_version" json:"schema_version"`
	ID              string            `yaml:"id" json:"id"`
	Version         string            `yaml:"version" json:"version"`
	Name            string            `yaml:"name" json:"name"`
	Description     string            `yaml:"description,omitempty" json:"description,omitempty"`
	Compatibility   Compatibility     `yaml:"compatibility" json:"compatibility"`
	ExtensionPoints []string          `yaml:"extension_points" json:"extension_points"`
	Permissions     []Permission      `yaml:"permissions" json:"permissions"`
	ConfigSchema    string            `yaml:"config_schema" json:"config_schema"`
	Assets          []string          `yaml:"assets,omitempty" json:"assets,omitempty"`
	Migrations      []Migration       `yaml:"migrations,omitempty" json:"migrations,omitempty"`
	Cleanup         CleanupPolicy     `yaml:"cleanup" json:"cleanup"`
	UIRouteID       string            `yaml:"ui_route_id,omitempty" json:"ui_route_id,omitempty"`
	Metadata        map[string]string `yaml:"metadata,omitempty" json:"metadata,omitempty"`
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
// the object form, while retaining one normalized permission representation.
func (p *Permission) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode {
		p.Name = strings.TrimSpace(node.Value)
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
	ID            string        `yaml:"id" json:"id"`
	Version       string        `yaml:"version" json:"version"`
	Description   string        `yaml:"description,omitempty" json:"description,omitempty"`
	Capabilities  []string      `yaml:"capabilities,omitempty" json:"capabilities,omitempty"`
	Compatibility Compatibility `yaml:"compatibility" json:"compatibility"`
	PackagePath   string        `yaml:"package" json:"package"`
	PackageSHA256 string        `yaml:"sha256" json:"sha256"`
	Official      bool          `yaml:"official,omitempty" json:"official,omitempty"`
}

type PackageExpectation struct {
	ID            string
	Version       string
	SHA256        string
	Compatibility Compatibility
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
