//go:build !integration

package plugins

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDeclarativeUIProjectionRejectsExecutableMarkupAndWriteOnlyMismatch(t *testing.T) {
	schema := map[string]any{"type": "object", "properties": map[string]any{"name": map[string]any{"type": "string"}, "token": map[string]any{"type": "string", "writeOnly": true}}}
	valid := map[string]any{
		"schema_version": 1, "title": "Plugin settings",
		"components": []any{
			map[string]any{"type": "text", "id": "name", "label": "Name", "binding": "/name"},
			map[string]any{"type": "secret", "id": "token", "label": "Token", "binding": "/token"},
		},
		"actions": []any{map[string]any{"type": "submit", "id": "save", "label": "Save"}},
	}
	data, err := json.Marshal(valid)
	if err != nil {
		t.Fatal(err)
	}
	if document, err := ProjectDeclarativeUI(data, schema, nil); err != nil || len(document.Components) != 2 {
		t.Fatalf("valid=%+v err=%v", document, err)
	}
	valid["title"] = "<script>run()</script>"
	bad, err := json.Marshal(valid)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ProjectDeclarativeUI(bad, schema, nil); err == nil {
		t.Fatal("executable markup accepted")
	}

	mismatch := map[string]any{
		"schema_version": 1, "title": "Plugin settings",
		"components": []any{map[string]any{"type": "text", "id": "token", "label": "Token", "binding": "/token"}},
		"actions":    []any{map[string]any{"type": "submit", "id": "save", "label": "Save"}},
	}
	mismatchData, err := json.Marshal(mismatch)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ProjectDeclarativeUI(mismatchData, schema, nil); err == nil || !strings.Contains(err.Error(), "writeOnly") {
		t.Fatalf("writeOnly mismatch error = %v", err)
	}

	unbound := map[string]any{
		"schema_version": 1, "title": "Plugin settings",
		"components": []any{map[string]any{"type": "text", "id": "missing", "label": "Missing", "binding": "/missing"}},
		"actions":    []any{map[string]any{"type": "submit", "id": "save", "label": "Save"}},
	}
	unboundData, err := json.Marshal(unbound)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ProjectDeclarativeUI(unboundData, schema, nil); err == nil {
		t.Fatal("undeclared binding accepted")
	}

	dynamic := map[string]any{
		"schema_version": 1, "title": "Plugin settings",
		"components": []any{map[string]any{"type": "text", "id": "name", "label": "Name", "binding": "/name"}},
		"actions": []any{map[string]any{
			"type": "dynamic", "id": "refresh", "label": "Refresh",
			"capability": "policy.atomic-state", "target_kind": "agent",
		}},
	}
	dynamicData, err := json.Marshal(dynamic)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ProjectDeclarativeUI(dynamicData, schema, nil); err == nil || !strings.Contains(err.Error(), "ui.dynamic-actions") {
		t.Fatalf("missing dynamic-actions error = %v", err)
	}
}

func TestValidatorRuntimeConfigExactNumericConstraints(t *testing.T) {
	schema, err := DecodeConfigSchema([]byte(`{"type":"object","properties":{"value":{"type":"number","minimum":-1.25,"maximum":2.5,"multipleOf":0.125}},"required":["value"],"additionalProperties":false}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"-1.25", "2.5", "0.375"} {
		if err := ValidateConfig(schema, json.RawMessage(`{"value":`+value+`}`)); err != nil {
			t.Fatalf("ValidateConfig(%s) = %v", value, err)
		}
	}
	if err := ValidateConfig(schema, json.RawMessage(`{"value":-1.251}`)); err == nil || !strings.Contains(err.Error(), "below minimum") {
		t.Fatalf("below minimum error = %v", err)
	}
	if err := ValidateConfig(schema, json.RawMessage(`{"value":0.38}`)); err == nil || !strings.Contains(err.Error(), "not an exact multipleOf value") {
		t.Fatalf("multipleOf error = %v", err)
	}
	zeroSchema, err := DecodeConfigSchema([]byte(`{"type":"object","properties":{"value":{"type":"number","multipleOf":0}},"required":["value"]}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateConfig(zeroSchema, json.RawMessage(`{"value":1}`)); err == nil || !strings.Contains(err.Error(), "multipleOf must be") {
		t.Fatalf("non-positive multipleOf error = %v", err)
	}
}

func TestValidatePackageRejectsIndependentSecurityFailures(t *testing.T) {
	t.Parallel()
	write := func(t *testing.T, root, name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	missing := t.TempDir()
	write(t, missing, "README", "not a plugin")
	if _, err := NewValidator(ValidatorOptions{}).ValidatePackage(missing, PackageExpectation{}); err == nil {
		t.Fatal("missing package manifest accepted")
	}

	official := NewValidator(ValidatorOptions{})
	exact := official.WithTrustedSigners(nil, TrustedSignerPolicyExact)
	if exact == official {
		t.Fatal("WithTrustedSigners returned the same validator")
	}

	manifest := t.TempDir()
	write(t, manifest, PackageManifestFile, "schema_version: 1\nid: demo\nversion: 1.0.0\nname: demo\n")
	if _, err := NewValidator(ValidatorOptions{}).ValidatePackage(manifest, PackageExpectation{}); err == nil {
		t.Fatal("incomplete signed package accepted")
	}

	abi := t.TempDir()
	write(t, abi, PackageManifestFile, strings.Join([]string{
		"schema_version: 1",
		"id: demo-plugin",
		"version: 1.0.0",
		"name: Demo",
		"compatibility:",
		"  control_plane: \">=1.0.0\"",
		"runtime:",
		"  kind: rpc-service",
		"  abi: not-an-abi",
		"  host_scope: control-plane",
		"  entry: plugin.exe",
		"artifacts:",
		"  - path: plugin.exe",
		"    sha256: " + strings.Repeat("a", 64),
		"    size: 1",
		"    mode: \"0755\"",
		"extension_points: [http.request]",
		"permissions: [{name: agent.read}]",
		"config_schema: config.schema.json",
		"resource_budget: {timeout_ms: 1000, memory_bytes: 65536, concurrency: 1, input_bytes: 1024, output_bytes: 1024, cpu_millis: 100, restarts: 1}",
		"failure_policy: {on_crash: disable, on_timeout: disable}",
		"signature: {key_id: custom-signer, algorithm: ed25519}",
		"cleanup: {on_disable: retain}",
	}, "\n"))
	write(t, abi, "config.schema.json", `{"type":"object"}`)
	write(t, abi, "plugin.exe", "x")
	if _, err := NewValidator(ValidatorOptions{}).ValidatePackage(abi, PackageExpectation{}); err == nil {
		t.Fatal("invalid ABI accepted")
	}
}
