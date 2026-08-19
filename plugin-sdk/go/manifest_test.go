package pluginsdk

import (
	"encoding/json"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestPluginManifestSchemaV1DeclaresResourceGroup(t *testing.T) {
	schema := string(PluginManifestSchemaV1())
	if !strings.Contains(schema, `"resource.group"`) {
		t.Fatal("manifest schema omits resource.group extension point")
	}
	if !strings.Contains(schema, `"resource_group_id"`) {
		t.Fatal("manifest schema omits resource_group_id")
	}
	data := []byte("resource_group_id: sample-plugin\n")
	var manifest Manifest
	if err := yaml.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.ResourceGroupID != "sample-plugin" {
		t.Fatalf("resource_group_id = %q", manifest.ResourceGroupID)
	}
}

func TestPluginManifestSchemaV1IsEmbeddedAndImmutable(t *testing.T) {
	first := PluginManifestSchemaV1()
	var schema map[string]any
	if err := json.Unmarshal(first, &schema); err != nil {
		t.Fatal(err)
	}
	if schema["$id"] != "https://github.com/sakullla/nginx-reverse-emby/plugin-sdk/schema/plugin-manifest-v1.schema.json" || schema["type"] != "object" {
		t.Fatalf("manifest schema identity = %+v", schema)
	}
	digest := PluginManifestSchemaV1SHA256()
	if len(digest) != 64 {
		t.Fatalf("manifest schema digest = %q", digest)
	}
	first[0] ^= 0xff
	if PluginManifestSchemaV1()[0] == first[0] || PluginManifestSchemaV1SHA256() != digest {
		t.Fatal("manifest schema storage is mutable")
	}
}

func TestPluginManifestV1PermissionYAMLUsesOneTypedProjection(t *testing.T) {
	data := []byte(`schema_version: 1
id: example
version: 1.0.0
name: Example
permissions:
  - agent.read
  - name: secret.use
    resource: secret-1
`)
	decoder := yaml.NewDecoder(strings.NewReader(string(data)))
	decoder.KnownFields(true)
	var manifest Manifest
	if err := decoder.Decode(&manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.SchemaVersion != PluginManifestSchemaVersion || len(manifest.Permissions) != 2 || manifest.Permissions[0].Name != "agent.read" || manifest.Permissions[1].Resource != "secret-1" {
		t.Fatalf("manifest projection = %+v", manifest)
	}
	var permission Permission
	if err := yaml.Unmarshal([]byte("name: agent.read\nunknown: value\n"), &permission); err == nil {
		t.Fatal("unknown permission field was accepted")
	}
}
