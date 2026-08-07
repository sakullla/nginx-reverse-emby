package plugins

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidatorAcceptsCanonicalDataOnlyPackage(t *testing.T) {
	root := newPackageFixture(t)
	validated, err := NewValidator(ValidatorOptions{HostVersion: "1.2.0", AgentVersion: "1.3.0"}).ValidatePackage(root, PackageExpectation{ID: "official.waf", Version: "1.0.0"})
	if err != nil {
		t.Fatal(err)
	}
	if validated.Manifest.ID != "official.waf" || len(validated.Digest) != 64 || validated.FileCount != 3 {
		t.Fatalf("unexpected validation result: %+v", validated)
	}
}

func TestComputePackageDigestUsesUTF8PathOrderAndExcludesItself(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "z.txt", "last")
	writeFixture(t, root, "é.txt", "utf8")
	writeFixture(t, root, PackageDigestFile, strings.Repeat("0", 64))
	first, err := ComputePackageDigest(root)
	if err != nil {
		t.Fatal(err)
	}
	writeFixture(t, root, PackageDigestFile, strings.Repeat("f", 64))
	second, err := ComputePackageDigest(root)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("package digest included %s: %s != %s", PackageDigestFile, first, second)
	}
}

func TestValidatorRejectsSymlinkAndTraversal(t *testing.T) {
	root := newPackageFixture(t)
	if err := os.Symlink(filepath.Join(root, ConfigSchemaFile), filepath.Join(root, "asset-link")); err == nil {
		_, err = NewValidator(ValidatorOptions{}).ValidatePackage(root, PackageExpectation{})
		assertValidationCode(t, err, "symlink")
	}

	root = newPackageFixture(t)
	manifest := validManifestYAML("../schema.json")
	writeFixture(t, root, PackageManifestFile, manifest)
	refreshFixtureDigest(t, root)
	_, err := NewValidator(ValidatorOptions{}).ValidatePackage(root, PackageExpectation{})
	assertValidationCode(t, err, "path")
}

func TestValidatorRejectsLargeExtensionlessExecutableMagic(t *testing.T) {
	for name, magic := range map[string][]byte{"elf": {0x7f, 'E', 'L', 'F'}, "pe": {'M', 'Z', 0, 0}} {
		t.Run(name, func(t *testing.T) {
			root := newPackageFixture(t)
			payload := append(magic, make([]byte, 8192)...)
			writeFixtureBytes(t, root, "assets/payload", payload)
			refreshFixtureDigest(t, root)
			_, err := NewValidator(ValidatorOptions{}).ValidatePackage(root, PackageExpectation{})
			assertValidationCode(t, err, "executable")
		})
	}
}

func TestValidatorRejectsTrailingConfigSchemaValue(t *testing.T) {
	root := newPackageFixture(t)
	writeFixture(t, root, ConfigSchemaFile, `{"type":"object"} {"type":"object"}`)
	refreshFixtureDigest(t, root)
	_, err := NewValidator(ValidatorOptions{}).ValidatePackage(root, PackageExpectation{})
	assertValidationCode(t, err, "config_schema")
}

func TestValidatorRejectsArbitraryScriptAndUnsafeCleanup(t *testing.T) {
	root := newPackageFixture(t)
	writeFixture(t, root, "assets/run.sh", "#!/bin/sh\nexit 0\n")
	refreshFixtureDigest(t, root)
	_, err := NewValidator(ValidatorOptions{}).ValidatePackage(root, PackageExpectation{})
	assertValidationCode(t, err, "executable")

	root = newPackageFixture(t)
	writeFixture(t, root, PackageManifestFile, strings.Replace(validManifestYAML(ConfigSchemaFile), "audit_events: retain", "audit_events: delete", 1))
	refreshFixtureDigest(t, root)
	_, err = NewValidator(ValidatorOptions{}).ValidatePackage(root, PackageExpectation{})
	assertValidationCode(t, err, "cleanup")
}

func TestValidatorAcceptsOnlyRestrictedDeclarativeMigrationOperations(t *testing.T) {
	root := newPackageFixture(t)
	writeFixture(t, root, PackageManifestFile, validManifestYAML(ConfigSchemaFile)+"migrations:\n  - from: 1.0.0\n    to: 2.0.0\n    file: migrations/1-to-2.json\n")
	writeFixture(t, root, "migrations/1-to-2.json", `{"script":"fetch('https://example.com')"}`)
	refreshFixtureDigest(t, root)
	_, err := NewValidator(ValidatorOptions{}).ValidatePackage(root, PackageExpectation{})
	assertValidationCode(t, err, "migration")

	writeFixture(t, root, "migrations/1-to-2.json", `{"operations":[{"op":"set","path":"/mode","value":"observe"},{"op":"remove","path":"/legacy"}]}`)
	refreshFixtureDigest(t, root)
	if _, err := NewValidator(ValidatorOptions{}).ValidatePackage(root, PackageExpectation{}); err != nil {
		t.Fatalf("restricted migration was rejected: %v", err)
	}
}

func TestValidatorRejectsUnsupportedSchemaAndExecutesEveryAcceptedConstraint(t *testing.T) {
	for name, schema := range map[string]string{
		"reference": `{"type":"object","properties":{"mode":{"$ref":"#/definitions/mode"}},"definitions":{"mode":{"type":"string"}}}`,
		"one-of":    `{"type":"object","properties":{"mode":{"oneOf":[{"type":"string"},{"type":"number"}]}}}`,
		"pattern":   `{"type":"object","properties":{"mode":{"type":"string","pattern":"x"}}}`,
	} {
		t.Run(name, func(t *testing.T) {
			root := newPackageFixture(t)
			writeFixture(t, root, ConfigSchemaFile, schema)
			refreshFixtureDigest(t, root)
			_, err := NewValidator(ValidatorOptions{}).ValidatePackage(root, PackageExpectation{})
			assertValidationCode(t, err, "config_schema")
		})
	}
	schema := map[string]any{
		"type": "object", "required": []any{"names"}, "additionalProperties": false,
		"properties": map[string]any{"names": map[string]any{"type": "array", "minItems": float64(1), "maxItems": float64(2), "items": map[string]any{"type": "string", "minLength": float64(2), "maxLength": float64(3)}}},
	}
	for _, raw := range []string{`{}`, `{"names":[]}`, `{"names":["x"]}`, `{"names":["good"]}`, `{"names":["ok"],"extra":true}`} {
		if err := ValidateConfig(schema, json.RawMessage(raw)); err == nil {
			t.Fatalf("accepted config violating an accepted schema constraint: %s", raw)
		}
	}
	if err := ValidateConfig(schema, json.RawMessage(`{"names":["ok","yes"]}`)); err != nil {
		t.Fatalf("valid constrained config rejected: %v", err)
	}
}

func TestValidatorRejectsMalformedCompatibilityAndUnsupportedCleanupMix(t *testing.T) {
	root := newPackageFixture(t)
	writeFixture(t, root, PackageManifestFile, strings.Replace(validManifestYAML(ConfigSchemaFile), `host: ">=1.0.0 <2.0.0"`, `host: "banana"`, 1))
	refreshFixtureDigest(t, root)
	_, err := NewValidator(ValidatorOptions{}).ValidatePackage(root, PackageExpectation{})
	assertValidationCode(t, err, "compatibility")

	root = newPackageFixture(t)
	writeFixture(t, root, PackageManifestFile, strings.Replace(validManifestYAML(ConfigSchemaFile), "config: delete", "config: retain", 1))
	refreshFixtureDigest(t, root)
	_, err = NewValidator(ValidatorOptions{}).ValidatePackage(root, PackageExpectation{})
	assertValidationCode(t, err, "cleanup")
}

func TestApplyMigrationChainIsDeterministicAndFailsClosed(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "migrations/1-to-2.json", `{"operations":[{"op":"rename","from":"/mode","path":"/behavior"},{"op":"set","path":"/count","value":2}]}`)
	manifest := Manifest{Version: "2.0.0", Migrations: []Migration{{From: "1.0.0", To: "2.0.0", File: "migrations/1-to-2.json"}}}
	migrated, err := ApplyMigrationChain(root, manifest, "1.0.0", json.RawMessage(`{"mode":"observe"}`))
	if err != nil || string(migrated) != `{"behavior":"observe","count":2}` {
		t.Fatalf("migration result = %s, %v", migrated, err)
	}
	if _, err := ApplyMigrationChain(root, Manifest{Version: "3.0.0"}, "2.0.0", migrated); err == nil {
		t.Fatal("missing migration chain was accepted")
	}
}

func newPackageFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeFixture(t, root, PackageManifestFile, validManifestYAML(ConfigSchemaFile))
	writeFixture(t, root, ConfigSchemaFile, `{"type":"object","properties":{"mode":{"type":"string"}},"additionalProperties":false}`)
	refreshFixtureDigest(t, root)
	return root
}

func validManifestYAML(schema string) string {
	return `schema_version: 1
id: official.waf
version: 1.0.0
name: WAF
compatibility:
  host: ">=1.0.0 <2.0.0"
  agent: ">=1.0.0 <2.0.0"
extension_points: [http.request]
permissions: [http.inspect]
config_schema: ` + schema + `
cleanup:
  instances: delete
  config: delete
  owned_data: delete
  grants: delete
  shared_refs: retain
  audit_events: retain
`
}

func refreshFixtureDigest(t *testing.T, root string) {
	t.Helper()
	digest, err := ComputePackageDigest(root)
	if err != nil {
		t.Fatal(err)
	}
	writeFixture(t, root, PackageDigestFile, digest+"\n")
}

func writeFixture(t *testing.T, root, name, value string) {
	t.Helper()
	writeFixtureBytes(t, root, name, []byte(value))
}

func writeFixtureBytes(t *testing.T, root, name string, value []byte) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, value, 0o644); err != nil {
		t.Fatal(err)
	}
}

func assertValidationCode(t *testing.T, err error, code string) {
	t.Helper()
	var validationErr *ValidationError
	if !errors.As(err, &validationErr) || validationErr.Code != code {
		t.Fatalf("expected validation code %q, got %v", code, err)
	}
}
