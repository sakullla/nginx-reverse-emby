//go:build !integration

package plugins

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
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

func TestConfigSchemaVocabularyAcceptsHostInjectedAndRejectsWriteOnlyConflict(t *testing.T) {
	schema, err := DecodeConfigSchema([]byte(`{"type":"object","properties":{"mode":{"type":"string"},"generation":{"type":"string","hostInjected":true},"apps":{"type":"array","items":{"type":"object","properties":{"id":{"type":"string","hostInjected":true},"name":{"type":"string"}},"required":["id","name"]}}},"required":["mode","generation"],"additionalProperties":false}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateConfig(schema, json.RawMessage(`{"mode":"strict","generation":"gen-1","apps":[{"id":"app-1","name":"web"}]}`)); err != nil {
		t.Fatalf("hostInjected schema = %v", err)
	}
	if err := ValidateConfig(schema, json.RawMessage(`{"mode":"strict"}`)); err == nil || !strings.Contains(err.Error(), "generation") {
		t.Fatalf("required hostInjected field error = %v", err)
	}

	falseFlag, err := DecodeConfigSchema([]byte(`{"type":"object","properties":{"mode":{"type":"string","hostInjected":false}},"required":["mode"]}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateConfig(falseFlag, json.RawMessage(`{"mode":"strict"}`)); err != nil {
		t.Fatalf("hostInjected false = %v", err)
	}

	conflict, err := DecodeConfigSchema([]byte(`{"type":"object","properties":{"token":{"type":"string","writeOnly":true,"hostInjected":true}},"required":["token"]}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateConfig(conflict, json.RawMessage(`{"token":"secret"}`)); err == nil || !strings.Contains(err.Error(), "hostInjected") || !strings.Contains(err.Error(), "writeOnly") {
		t.Fatalf("hostInjected+writeOnly error = %v", err)
	}

	rootInjected, err := DecodeConfigSchema([]byte(`{"type":"object","hostInjected":true,"properties":{"mode":{"type":"string"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateConfig(rootInjected, json.RawMessage(`{"mode":"strict"}`)); err == nil || !strings.Contains(err.Error(), "root") {
		t.Fatalf("root hostInjected error = %v", err)
	}

	itemsInjected, err := DecodeConfigSchema([]byte(`{"type":"object","properties":{"tags":{"type":"array","items":{"type":"string","hostInjected":true}}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateConfig(itemsInjected, json.RawMessage(`{"tags":["a"]}`)); err == nil || !strings.Contains(err.Error(), "named object properties") {
		t.Fatalf("items hostInjected error = %v", err)
	}

	notBool, err := DecodeConfigSchema([]byte(`{"type":"object","properties":{"generation":{"type":"string","hostInjected":"yes"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateConfig(notBool, json.RawMessage(`{"generation":"g"}`)); err == nil || !strings.Contains(err.Error(), "hostInjected must be boolean") {
		t.Fatalf("non-boolean hostInjected error = %v", err)
	}

	unmarked, err := DecodeConfigSchema([]byte(`{"type":"object","properties":{"mode":{"type":"string"}},"required":["mode"],"additionalProperties":false}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateConfig(unmarked, json.RawMessage(`{"mode":"strict"}`)); err != nil {
		t.Fatalf("unmarked schema = %v", err)
	}
	if err := ValidateConfig(unmarked, json.RawMessage(`{}`)); err == nil || !strings.Contains(err.Error(), "mode") {
		t.Fatalf("unmarked required error = %v", err)
	}

	root := newSignedWASMPackage(t, "")
	writeOwnerFile(t, root, ConfigSchemaFile, `{"type":"object","properties":{"mode":{"type":"string"},"generation":{"type":"string","hostInjected":true}},"required":["mode","generation"],"additionalProperties":false}`)
	refreshOwnerPackage(t, root)
	got, err := newOwnerValidator().ValidatePackage(root, PackageExpectation{})
	if err != nil {
		t.Fatalf("package with hostInjected schema = %v", err)
	}
	properties, _ := got.ConfigSchema["properties"].(map[string]any)
	generation, _ := properties["generation"].(map[string]any)
	if injected, _ := generation["hostInjected"].(bool); !injected {
		t.Fatalf("validated schema lost hostInjected: %+v", got.ConfigSchema)
	}
}

func TestValidatePackageRejectsRemovedDockerComposeContracts(t *testing.T) {
	t.Parallel()
	assertCode := func(t *testing.T, err error, code string) {
		t.Helper()
		var validationErr *ValidationError
		if !errors.As(err, &validationErr) || validationErr.Code != code {
			t.Fatalf("expected validation code %q, got %v", code, err)
		}
	}

	compose := newSignedWASMPackage(t, "")
	writeOwnerFile(t, compose, PackageManifestFile, strings.Replace(validOwnerManifestYAML(), "permissions: [http.inspect]", "permissions: [http.inspect, container.compose]", 1))
	refreshOwnerPackage(t, compose)
	_, err := newOwnerValidator().ValidatePackage(compose, PackageExpectation{})
	assertCode(t, err, "permission")
	if !strings.Contains(err.Error(), "container.compose") {
		t.Fatalf("container.compose error = %v", err)
	}

	provider := newSignedWASMPackage(t, "")
	writeOwnerFile(t, provider, PackageManifestFile, strings.Replace(validOwnerManifestYAML(), "extension_points: [http.request]", "extension_points: [http.request, container.provider]", 1))
	refreshOwnerPackage(t, provider)
	_, err = newOwnerValidator().ValidatePackage(provider, PackageExpectation{})
	assertCode(t, err, "extension_point")
	if !strings.Contains(err.Error(), "container.provider") {
		t.Fatalf("container.provider error = %v", err)
	}
}

func TestValidatePackageAllowsResourceGroupExtension(t *testing.T) {
	t.Parallel()

	root := newSignedWASMPackage(t, "")
	manifest := strings.Replace(validOwnerManifestYAML(), "extension_points: [http.request]", "extension_points: [http.request, resource.group]", 1)
	writeOwnerFile(t, root, PackageManifestFile, manifest)
	refreshOwnerPackage(t, root)
	if _, err := newOwnerValidator().ValidatePackage(root, PackageExpectation{}); err != nil {
		t.Fatalf("package with resource.group extension = %v", err)
	}
}

func TestValidatePackageRejectsIndependentSecurityFailures(t *testing.T) {
	t.Parallel()
	assertCode := func(t *testing.T, err error, code string) {
		t.Helper()
		var validationErr *ValidationError
		if !errors.As(err, &validationErr) || validationErr.Code != code {
			t.Fatalf("expected validation code %q, got %v", code, err)
		}
	}

	root := newSignedWASMPackage(t, "")
	got, err := newOwnerValidator().ValidatePackage(root, PackageExpectation{})
	if err != nil || got.Digest == "" || got.Manifest.ID != "official.waf" {
		t.Fatalf("valid package = %+v err=%v", got, err)
	}

	tampered := newSignedWASMPackage(t, "")
	if err := os.WriteFile(filepath.Join(tampered, PackageSignatureFile), []byte(base64.StdEncoding.EncodeToString(make([]byte, ed25519.SignatureSize))+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = newOwnerValidator().ValidatePackage(tampered, PackageExpectation{})
	assertCode(t, err, "signature_mismatch")

	official := newSignedWASMPackage(t, "")
	manifest := strings.Replace(validOwnerManifestYAML(), "key_id: test-fixture", "key_id: "+OfficialSignatureKeyID, 1)
	if err := os.WriteFile(filepath.Join(official, PackageManifestFile), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	refreshOwnerPackage(t, official)
	overridden := NewValidator(ValidatorOptions{TrustedSigners: map[string]ed25519.PublicKey{OfficialSignatureKeyID: ownerSigningKey().Public().(ed25519.PublicKey)}})
	_, err = overridden.ValidatePackage(official, PackageExpectation{})
	assertCode(t, err, "signature_mismatch")

	digestRoot := newSignedWASMPackage(t, "")
	if err := os.WriteFile(filepath.Join(digestRoot, "artifacts", "policy.wasm"), append(ownerWASMArtifact(), 0), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = newOwnerValidator().ValidatePackage(digestRoot, PackageExpectation{})
	assertCode(t, err, "artifact_size")

	modeRoot := newSignedWASMPackage(t, "")
	if err := os.Chmod(filepath.Join(modeRoot, "artifacts", "policy.wasm"), 0o755); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(filepath.Join(modeRoot, "artifacts", "policy.wasm")); err != nil || info.Mode().Perm()&0o111 == 0 {
		t.Skip("filesystem does not preserve POSIX execute bits")
	}
	_, err = newOwnerValidator().ValidatePackage(modeRoot, PackageExpectation{})
	assertCode(t, err, "artifact_mode")

	cycle := newSignedWASMPackage(t, "migrations:\n  - {from: 0.8.0, to: 0.9.0, file: migrations/a.json}\n  - {from: 0.9.0, to: 0.8.0, file: migrations/b.json}\n")
	writeOwnerFile(t, cycle, "migrations/a.json", `{"operations":[{"op":"set","path":"/a","value":true}]}`)
	writeOwnerFile(t, cycle, "migrations/b.json", `{"operations":[{"op":"set","path":"/b","value":true}]}`)
	refreshOwnerPackage(t, cycle)
	_, err = newOwnerValidator().ValidatePackage(cycle, PackageExpectation{})
	assertCode(t, err, "migration")
	if !strings.Contains(err.Error(), "contains a cycle") {
		t.Fatalf("cycle error = %v", err)
	}
}

func newSignedWASMPackage(t *testing.T, extraManifest string) string {
	t.Helper()
	root := t.TempDir()
	writeOwnerFile(t, root, PackageManifestFile, validOwnerManifestYAML()+extraManifest)
	writeOwnerFile(t, root, ConfigSchemaFile, `{"type":"object","properties":{"mode":{"type":"string"}},"additionalProperties":false}`)
	writeOwnerBytes(t, root, "artifacts/policy.wasm", ownerWASMArtifact())
	refreshOwnerPackage(t, root)
	return root
}

func validOwnerManifestYAML() string {
	artifact := ownerWASMArtifact()
	digest := sha256.Sum256(artifact)
	return fmt.Sprintf(`schema_version: 1
id: official.waf
version: 1.0.0
name: WAF
compatibility:
  host: ">=1.0.0 <2.0.0"
  agent: ">=1.0.0 <2.0.0"
runtime:
  kind: wasm-policy
  abi: nre:policy/v1
  host_scope: agent
  entry: artifacts/policy.wasm
  policy_kind: waf
artifacts:
  - path: artifacts/policy.wasm
    sha256: %x
    size: %d
    mode: wasm
extension_points: [http.request]
permissions: [http.inspect]
config_schema: config.schema.json
resource_budget:
  timeout_ms: 2
  memory_bytes: 1048576
  concurrency: 8
  input_bytes: 65536
  output_bytes: 4096
failure_policy:
  on_error: fail-open
  on_budget: fail-open
  restart: never
  core_fallback: preserve
signature:
  algorithm: ed25519
  key_id: test-fixture
  file: package.sig
cleanup:
  instances: delete
  config: delete
  owned_data: delete
  grants: delete
  shared_refs: retain
  audit_events: retain
`, digest, len(artifact))
}

func refreshOwnerPackage(t *testing.T, root string) {
	t.Helper()
	digest, err := ComputePackageDigest(root)
	if err != nil {
		t.Fatal(err)
	}
	writeOwnerFile(t, root, PackageDigestFile, digest+"\n")
	writeOwnerFile(t, root, PackageSignatureFile, base64.StdEncoding.EncodeToString(ed25519.Sign(ownerSigningKey(), []byte(digest)))+"\n")
}

func newOwnerValidator() *Validator {
	return NewValidator(ValidatorOptions{TrustedSigners: map[string]ed25519.PublicKey{"test-fixture": ownerSigningKey().Public().(ed25519.PublicKey)}})
}

func ownerSigningKey() ed25519.PrivateKey {
	seed := sha256.Sum256([]byte("nre-validator-test-fixture"))
	return ed25519.NewKeyFromSeed(seed[:])
}

func ownerWASMArtifact() []byte {
	name := filepath.Join("..", "..", "..", "..", "..", "plugin-sdk", "policy", "v1", "testdata", "compatible_guest.wasm.hex")
	encoded, err := os.ReadFile(name)
	if err != nil {
		panic(err)
	}
	artifact, err := hex.DecodeString(strings.TrimSpace(string(encoded)))
	if err != nil {
		panic(err)
	}
	return artifact
}

func writeOwnerFile(t *testing.T, root, name, value string) {
	t.Helper()
	writeOwnerBytes(t, root, name, []byte(value))
}

func writeOwnerBytes(t *testing.T, root, name string, value []byte) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, value, 0o644); err != nil {
		t.Fatal(err)
	}
}
