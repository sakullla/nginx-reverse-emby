package pluginsdk

import (
	"encoding/json"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestPluginManifestSchemaV1OmitsRemovedDockerComposeContracts(t *testing.T) {
	schema := string(PluginManifestSchemaV1())
	for _, name := range []string{"container.compose", "container.read", "container.manage", "container.provider"} {
		if strings.Contains(schema, `"`+name+`"`) {
			t.Fatalf("manifest schema still declares %q", name)
		}
	}
}

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

func TestPluginManifestRuntimeDeclaresControlPlaneAndAgentFaces(t *testing.T) {
	schema := string(PluginManifestSchemaV1())
	if !strings.Contains(schema, `"host_scopes"`) {
		t.Fatal("manifest schema omits host_scopes")
	}
	data := []byte(`schema_version: 1
id: dual-face
version: 1.0.0
name: Dual Face
runtime:
  kind: rpc-service
  abi: nre:rpc/v1
  host_scope: control-plane
  host_scopes:
    - control-plane
    - agent
  entry: plugin
`)
	decoder := yaml.NewDecoder(strings.NewReader(string(data)))
	decoder.KnownFields(true)
	var manifest Manifest
	if err := decoder.Decode(&manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Runtime.HostScope != HostScopeControlPlane {
		t.Fatalf("primary host_scope = %q", manifest.Runtime.HostScope)
	}
	if !RuntimeDeclaresHostScope(manifest.Runtime, HostScopeControlPlane) || !RuntimeDeclaresHostScope(manifest.Runtime, HostScopeAgent) {
		t.Fatalf("declared host scopes = %v", RuntimeDeclaredHostScopes(manifest.Runtime))
	}
	agentOnly := Runtime{HostScope: HostScopeAgent}
	if !RuntimeDeclaresHostScope(agentOnly, HostScopeAgent) || RuntimeDeclaresHostScope(agentOnly, HostScopeControlPlane) {
		t.Fatalf("agent-only scopes = %v", RuntimeDeclaredHostScopes(agentOnly))
	}
	if RuntimeAgentFaceHostScope(manifest.Runtime) != HostScopeAgent {
		t.Fatalf("dual-face agent snapshot host_scope = %q", RuntimeAgentFaceHostScope(manifest.Runtime))
	}
	if !RuntimeDurableArtifactHostScopeMatches(manifest.Runtime, HostScopeAgent, HostScopeAgent) {
		t.Fatal("agent-face artifact host_scope must equal generation host_scope")
	}
	if !RuntimeDurableArtifactHostScopeMatches(manifest.Runtime, HostScopeAgent, manifest.Runtime.HostScope) {
		t.Fatal("dual-face primary host_scope must satisfy agent generation issuance")
	}
	if RuntimeDurableArtifactHostScopeMatches(agentOnly, HostScopeAgent, HostScopeControlPlane) {
		t.Fatal("agent-only runtime accepted a control-plane artifact host_scope")
	}
}

func TestPluginManifestRuntimeDeclaresNestedAgentPolicyFace(t *testing.T) {
	schema := string(PluginManifestSchemaV1())
	if !strings.Contains(schema, `"runtime_policy"`) {
		t.Fatal("manifest schema omits nested runtime.policy")
	}
	data := []byte(`schema_version: 1
id: waf
version: 1.0.0
name: WAF
runtime:
  kind: rpc-service
  abi: nre:rpc/v1
  host_scope: control-plane
  entry: plugin
  policy_kind: waf
  policy:
    kind: wasm-policy
    abi: nre:policy/v1
    host_scope: agent
    entry: artifacts/waf.wasm
    resource_budget:
      timeout_ms: 2
      memory_bytes: 1048576
      concurrency: 8
      input_bytes: 65536
      output_bytes: 4096
    failure_policy:
      on_error: fail-closed
      on_budget: fail-closed
      restart: never
      core_fallback: preserve
`)
	decoder := yaml.NewDecoder(strings.NewReader(string(data)))
	decoder.KnownFields(true)
	var manifest Manifest
	if err := decoder.Decode(&manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Runtime.Kind != RuntimeRPCService || manifest.Runtime.HostScope != HostScopeControlPlane || manifest.Runtime.PolicyKind != "waf" {
		t.Fatalf("dual-face primary runtime = %+v", manifest.Runtime)
	}
	if !RuntimeProjectsControlPlaneUIAndAgentPolicy(manifest.Runtime) || RuntimeProjectsAgentRPC(manifest.Runtime) {
		t.Fatalf("dual-face projection = rpc:%v policy:%v ui+policy:%v", RuntimeProjectsAgentRPC(manifest.Runtime), RuntimeProjectsAgentPolicy(manifest.Runtime), RuntimeProjectsControlPlaneUIAndAgentPolicy(manifest.Runtime))
	}
	projection, ok := ProjectAgentPolicy(manifest)
	if !ok || projection.Entry != "artifacts/waf.wasm" || projection.PolicyKind != "waf" || projection.ResourceBudget.TimeoutMS != 2 || projection.FailurePolicy.Restart != "never" {
		t.Fatalf("dual-face policy projection = %+v ok=%v", projection, ok)
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
