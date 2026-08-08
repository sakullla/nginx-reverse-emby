package plugins

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidatorAcceptsCanonicalRuntimePackage(t *testing.T) {
	root := newPackageFixture(t)
	validated, err := newTestValidator(ValidatorOptions{HostVersion: "1.2.0", AgentVersion: "1.3.0"}).ValidatePackage(root, PackageExpectation{ID: "official.waf", Version: "1.0.0"})
	if err != nil {
		t.Fatal(err)
	}
	if validated.Manifest.ID != "official.waf" || len(validated.Digest) != 64 || validated.FileCount != 5 {
		t.Fatalf("unexpected validation result: %+v", validated)
	}
}

func TestRuntimeArtifactSignatureABIValidator(t *testing.T) {
	t.Run("signature tamper", func(t *testing.T) {
		root := newPackageFixture(t)
		writeFixture(t, root, PackageSignatureFile, base64.StdEncoding.EncodeToString(make([]byte, ed25519.SignatureSize)))
		_, err := newTestValidator(ValidatorOptions{}).ValidatePackage(root, PackageExpectation{})
		assertValidationCode(t, err, "signature_mismatch")
	})
	t.Run("official root cannot be overridden", func(t *testing.T) {
		root := newPackageFixture(t)
		manifest := strings.Replace(validManifestYAML(ConfigSchemaFile), "key_id: test-fixture", "key_id: "+OfficialSignatureKeyID, 1)
		writeFixture(t, root, PackageManifestFile, manifest)
		refreshFixtureDigest(t, root)
		validator := NewValidator(ValidatorOptions{TrustedSigners: map[string]ed25519.PublicKey{OfficialSignatureKeyID: testSigningKey().Public().(ed25519.PublicKey)}})
		_, err := validator.ValidatePackage(root, PackageExpectation{})
		assertValidationCode(t, err, "signature_mismatch")
	})
	t.Run("ABI mismatch", func(t *testing.T) {
		root := newPackageFixture(t)
		manifest := strings.Replace(validManifestYAML(ConfigSchemaFile), "abi: nre:policy/v1", "abi: nre:policy/v9", 1)
		writeFixture(t, root, PackageManifestFile, manifest)
		refreshFixtureDigest(t, root)
		_, err := newTestValidator(ValidatorOptions{}).ValidatePackage(root, PackageExpectation{})
		assertValidationCode(t, err, "runtime")
	})
	t.Run("artifact digest tamper", func(t *testing.T) {
		root := newPackageFixture(t)
		writeFixtureBytes(t, root, "artifacts/policy.wasm", append(testWASMArtifact(), 0))
		_, err := newTestValidator(ValidatorOptions{}).ValidatePackage(root, PackageExpectation{})
		assertValidationCode(t, err, "artifact_size")
	})
	t.Run("filesystem executable bit", func(t *testing.T) {
		root := newPackageFixture(t)
		if err := os.Chmod(filepath.Join(root, "artifacts", "policy.wasm"), 0o755); err != nil {
			t.Fatal(err)
		}
		if info, err := os.Stat(filepath.Join(root, "artifacts", "policy.wasm")); err != nil || info.Mode().Perm()&0o111 == 0 {
			t.Skip("filesystem does not preserve POSIX execute bits")
		}
		_, err := newTestValidator(ValidatorOptions{}).ValidatePackage(root, PackageExpectation{})
		assertValidationCode(t, err, "artifact_mode")
	})
	t.Run("declared mode is digest material", func(t *testing.T) {
		root := newPackageFixture(t)
		before, err := ComputePackageDigest(root)
		if err != nil {
			t.Fatal(err)
		}
		manifest := strings.Replace(validManifestYAML(ConfigSchemaFile), "mode: wasm", "mode: executable", 1)
		writeFixture(t, root, PackageManifestFile, manifest)
		after, err := ComputePackageDigest(root)
		if err != nil {
			t.Fatal(err)
		}
		if before == after {
			t.Fatal("declared artifact mode did not affect package digest")
		}
	})
}

func TestRuntimeRPCArtifactPlatformMatrixValidator(t *testing.T) {
	root := newRPCPackageFixture(t)
	if _, err := newTestValidator(ValidatorOptions{TargetGOOS: "linux", TargetGOARCH: "amd64"}).ValidatePackage(root, PackageExpectation{}); err != nil {
		t.Fatal(err)
	}
	_, err := newTestValidator(ValidatorOptions{TargetGOOS: "windows", TargetGOARCH: "amd64"}).ValidatePackage(root, PackageExpectation{})
	assertValidationCode(t, err, "artifact")
}

func TestRuntimeMarketSignatureAndCapabilityContract(t *testing.T) {
	baseEntry := `  - id: official.example
    version: 1.0.0
    capabilities: [http.request]
    compatibility: {host: "*", agent: "*"}
    runtime: {kind: wasm-policy, abi: "nre:policy/v1", host_scope: agent}
    artifacts: [{sha256: ` + strings.Repeat("a", 64) + `, size: 8}]
    package: plugins/official.example/1.0.0
    sha256: ` + strings.Repeat("b", 64) + `
    signature_key_id: test-fixture
    provenance: sakullla-plugins
    official: true
`
	root := t.TempDir()
	writeFixture(t, root, MarketManifestFile, "schema_version: 1\nname: Official\nplugins:\n"+baseEntry)
	_, err := newTestValidator(ValidatorOptions{}).ValidateMarket(root, true)
	assertValidationCode(t, err, "signature_identity")

	root = t.TempDir()
	entryWithoutCapability := strings.Replace(baseEntry, "    capabilities: [http.request]\n", "", 1)
	entryWithoutCapability = strings.Replace(entryWithoutCapability, "    provenance: sakullla-plugins", "    provenance: custom", 1)
	entryWithoutCapability = strings.Replace(entryWithoutCapability, "    official: true", "    official: false", 1)
	writeFixture(t, root, MarketManifestFile, "schema_version: 1\nname: Custom\nplugins:\n"+entryWithoutCapability)
	_, err = newTestValidator(ValidatorOptions{}).ValidateMarket(root, false)
	assertValidationCode(t, err, "market_entry")
}

func TestNestedPermissionFieldsRemainStrict(t *testing.T) {
	root := newPackageFixture(t)
	manifest := strings.Replace(validManifestYAML(ConfigSchemaFile), "permissions: [http.inspect]", "permissions:\n  - name: http.inspect\n    resources: tenant-a", 1)
	writeFixture(t, root, PackageManifestFile, manifest)
	refreshFixtureDigest(t, root)
	_, err := newTestValidator(ValidatorOptions{}).ValidatePackage(root, PackageExpectation{})
	if err == nil || !strings.Contains(err.Error(), `unknown permission field "resources"`) {
		t.Fatalf("unknown nested permission field error = %v", err)
	}
}

func TestRenameMigrationRejectsArrayIndicesAndOverlap(t *testing.T) {
	for name, document := range map[string]string{
		"forward":             `{"operations":[{"op":"rename","from":"/0","path":"/2"}]}`,
		"backward":            `{"operations":[{"op":"rename","from":"/2","path":"/0"}]}`,
		"same":                `{"operations":[{"op":"rename","from":"/1","path":"/1"}]}`,
		"leading zero":        `{"operations":[{"op":"rename","from":"/01","path":"/name"}]}`,
		"explicit plus":       `{"operations":[{"op":"rename","from":"/+1","path":"/name"}]}`,
		"same index alias":    `{"operations":[{"op":"rename","from":"/01","path":"/1"}]}`,
		"nested leading zero": `{"operations":[{"op":"rename","from":"/items/001/name","path":"/name"}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateMigrationDocument([]byte(document)); err == nil || !strings.Contains(err.Error(), "does not support array indices") {
				t.Fatalf("array rename error = %v", err)
			}
		})
	}
	if err := validateMigrationDocument([]byte(`{"operations":[{"op":"rename","from":"/a","path":"/a/b"}]}`)); err == nil {
		t.Fatal("overlapping rename was accepted")
	}
}

func TestNormalizeBuildVersionAcceptsOnlyExplicitTagPrefix(t *testing.T) {
	for input, want := range map[string]string{"v1.4.1": "1.4.1", "1.4.1": "1.4.1", "dev": "0.0.0-dev"} {
		got, err := NormalizeBuildVersion(input)
		if err != nil || got != want {
			t.Fatalf("NormalizeBuildVersion(%q) = %q, %v", input, got, err)
		}
	}
	if _, err := NormalizeBuildVersion("release-1.4.1"); err == nil {
		t.Fatal("invalid release build version was normalized")
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
		_, err = newTestValidator(ValidatorOptions{}).ValidatePackage(root, PackageExpectation{})
		assertValidationCode(t, err, "symlink")
	}

	root = newPackageFixture(t)
	manifest := validManifestYAML("../schema.json")
	writeFixture(t, root, PackageManifestFile, manifest)
	refreshFixtureDigest(t, root)
	_, err := newTestValidator(ValidatorOptions{}).ValidatePackage(root, PackageExpectation{})
	assertValidationCode(t, err, "path")
}

func TestValidatorRejectsLargeExtensionlessExecutableMagic(t *testing.T) {
	for name, magic := range map[string][]byte{
		"elf": {0x7f, 'E', 'L', 'F'}, "pe": {'M', 'Z', 0, 0}, "macho64": {0xcf, 0xfa, 0xed, 0xfe},
		"macho-fat": {0xca, 0xfe, 0xba, 0xbe}, "wasm": {0x00, 0x61, 0x73, 0x6d}, "llvm": {'B', 'C', 0xc0, 0xde},
		"lua": {0x1b, 'L', 'u', 'a'}, "dex": {'d', 'e', 'x', '\n'},
		"ar": {'!', '<', 'a', 'r', 'c', 'h', '>', '\n'}, "zip": {'P', 'K', 0x03, 0x04},
		"cpython": {0xa7, 0x0d, '\r', '\n'},
		"coff":    {0x64, 0x86, 0, 0},
	} {
		t.Run(name, func(t *testing.T) {
			root := newPackageFixture(t)
			writeFixture(t, root, PackageManifestFile, validManifestYAML(ConfigSchemaFile)+"assets: [assets/payload.txt]\n")
			payload := append(magic, make([]byte, 8192)...)
			writeFixtureBytes(t, root, "assets/payload.txt", payload)
			refreshFixtureDigest(t, root)
			_, err := newTestValidator(ValidatorOptions{}).ValidatePackage(root, PackageExpectation{})
			assertValidationCode(t, err, "executable")
		})
	}
}

func TestStrictSemVerIsEnforcedAtPackageMarketAndMigrationEntrypoints(t *testing.T) {
	for _, invalid := range []string{"1.0.0-01", "1.0.0+build..bad", "v1.0.0", " 1.0.0 "} {
		root := newPackageFixture(t)
		writeFixture(t, root, PackageManifestFile, strings.Replace(validManifestYAML(ConfigSchemaFile), "version: 1.0.0", "version: \""+invalid+"\"", 1))
		refreshFixtureDigest(t, root)
		_, err := newTestValidator(ValidatorOptions{}).ValidatePackage(root, PackageExpectation{})
		assertValidationCode(t, err, "manifest_schema")
	}
	root := newPackageFixture(t)
	writeFixture(t, root, PackageManifestFile, validManifestYAML(ConfigSchemaFile)+"migrations:\n  - from: 1.0.0-01\n    to: 2.0.0\n    file: migrations/bad.json\n")
	writeFixture(t, root, "migrations/bad.json", `{"operations":[]}`)
	refreshFixtureDigest(t, root)
	_, err := newTestValidator(ValidatorOptions{}).ValidatePackage(root, PackageExpectation{})
	assertValidationCode(t, err, "migration")

	for _, invalid := range []string{"1.0.0-01", "v1.0.0", " 1.0.0 "} {
		marketRoot := marketplaceFixtureForStrictVersion(t, invalid)
		_, err = newTestValidator(ValidatorOptions{}).ValidateMarket(marketRoot, false)
		assertValidationCode(t, err, "market_entry")
	}
}

func marketplaceFixtureForStrictVersion(t *testing.T, version string) string {
	t.Helper()
	root := newPackageFixture(t)
	packageRoot := filepath.Join(root, "plugins", "official.waf", version)
	if err := os.MkdirAll(filepath.Dir(packageRoot), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(root, PackageManifestFile), filepath.Join(root, "manifest.tmp")); err != nil {
		t.Fatal(err)
	}
	// The entry is rejected before its package path is resolved.
	writeFixture(t, root, MarketManifestFile, "schema_version: 1\nname: Test\nplugins:\n  - id: official.waf\n    version: \""+version+"\"\n    compatibility: {host: \"*\", agent: \"*\"}\n    package: plugins/official.waf/invalid\n    sha256: "+strings.Repeat("a", 64)+"\n    official: false\n")
	return root
}

func TestSemVerPrereleaseOrderingAndLargeNumericIdentifiers(t *testing.T) {
	for _, test := range []struct{ version, constraint string }{
		{"1.0.0-alpha.10", ">1.0.0-alpha.2 <1.0.0"},
		{"1.0.0-999999999999999999999999999999", ">1.0.0-9 <1.0.0"},
		{"1.0.0", ">1.0.0-rc.99"},
		{"999999999999999999999999.0.0", ">2.0.0"},
	} {
		if !versionSatisfies(test.version, test.constraint) {
			t.Fatalf("%s should satisfy %s", test.version, test.constraint)
		}
	}
	if IsSemanticVersion("1.0.0-01") || IsSemanticVersion("1.0.0+build..bad") || IsSemanticVersion("v1.0.0") || IsSemanticVersion(" 1.0.0 ") || versionSatisfies("v1.0.0", "*") || versionSatisfies("1.0.0-alpha", ">=1.0.0") {
		t.Fatal("invalid or incorrectly ordered prerelease was accepted")
	}
}

func TestValidatorRejectsUndeclaredPayloadAndUnsafeDeclaredAsset(t *testing.T) {
	root := newPackageFixture(t)
	writeFixture(t, root, "assets/hidden.txt", "hidden")
	refreshFixtureDigest(t, root)
	_, err := newTestValidator(ValidatorOptions{}).ValidatePackage(root, PackageExpectation{})
	assertValidationCode(t, err, "undeclared_payload")

	root = newPackageFixture(t)
	writeFixture(t, root, PackageManifestFile, validManifestYAML(ConfigSchemaFile)+"assets: [assets/object.obj]\n")
	writeFixtureBytes(t, root, "assets/object.obj", []byte{0x64, 0x86, 0, 0})
	refreshFixtureDigest(t, root)
	_, err = newTestValidator(ValidatorOptions{}).ValidatePackage(root, PackageExpectation{})
	assertValidationCode(t, err, "asset")
}

func TestValidatorRequiresExactDeclaredSchemaAndFullyParsesAssets(t *testing.T) {
	validator := newTestValidator(ValidatorOptions{})
	t.Run("alternate schema does not implicitly allow default schema", func(t *testing.T) {
		root := t.TempDir()
		writeFixture(t, root, PackageManifestFile, validManifestYAML("schema/custom.json"))
		writeFixture(t, root, "schema/custom.json", `{"type":"object"}`)
		writeFixture(t, root, ConfigSchemaFile, `{"type":"object"}`)
		writeFixture(t, root, PackageDigestFile, strings.Repeat("0", 64))
		_, err := validator.ValidatePackage(root, PackageExpectation{})
		assertValidationCode(t, err, "undeclared_payload")
	})
	t.Run("prefixed binary text", func(t *testing.T) {
		root := newPackageFixture(t)
		writeFixture(t, root, PackageManifestFile, validManifestYAML(ConfigSchemaFile)+"assets: [assets/payload.txt]\n")
		payload := append(bytes.Repeat([]byte("safe-prefix"), 32), []byte{0, 0x7f, 'E', 'L', 'F'}...)
		if err := os.MkdirAll(filepath.Join(root, "assets"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "assets", "payload.txt"), payload, 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := validator.ValidatePackage(root, PackageExpectation{})
		assertValidationCode(t, err, "asset")
	})
	t.Run("invalid image body", func(t *testing.T) {
		root := newPackageFixture(t)
		writeFixture(t, root, PackageManifestFile, validManifestYAML(ConfigSchemaFile)+"assets: [assets/picture.png]\n")
		writeFixture(t, root, "assets/picture.png", "safe-prefix-not-a-png")
		_, err := validator.ValidatePackage(root, PackageExpectation{})
		assertValidationCode(t, err, "asset")
	})
}

func TestConfigEnumPreservesLargeJSONNumbers(t *testing.T) {
	schema, err := DecodeConfigSchema([]byte(`{"type":"object","properties":{"id":{"enum":[9007199254740993]}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateConfig(schema, json.RawMessage(`{"id":9007199254740993}`)); err != nil {
		t.Fatalf("exact large enum was rejected: %v", err)
	}
	if err := ValidateConfig(schema, json.RawMessage(`{"id":9007199254740992}`)); err == nil {
		t.Fatal("adjacent large JSON number matched enum through float rounding")
	}
}

func TestConfigIntegerUsesArbitraryPrecisionValueSemantics(t *testing.T) {
	schema, err := DecodeConfigSchema([]byte(`{"type":"object","properties":{"value":{"type":"integer","enum":[9223372036854775808,1e30]}}}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{`{"value":9223372036854775808}`, `{"value":1e30}`, `{"value":10e29}`} {
		if err := ValidateConfig(schema, json.RawMessage(raw)); err != nil {
			t.Fatalf("arbitrary-precision integer %s was rejected: %v", raw, err)
		}
	}
	integerOnly, err := DecodeConfigSchema([]byte(`{"type":"integer"}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{`1e-3`, `1.5`} {
		if err := ValidateConfig(integerOnly, json.RawMessage(raw)); err == nil {
			t.Fatalf("non-integral exact number %s was accepted", raw)
		}
	}
}

func TestConfigNumberUsesArbitraryPrecisionValueSemantics(t *testing.T) {
	schema, err := DecodeConfigSchema([]byte(`{"type":"object","properties":{"value":{"type":"number"}},"required":["value"]}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{`1e400`, `1e-400`, `-123456789012345678901234567890.123456789`} {
		if err := ValidateConfig(schema, json.RawMessage(`{"value":`+raw+`}`)); err != nil {
			t.Fatalf("arbitrary-precision number %s was rejected: %v", raw, err)
		}
	}
}

func TestValidatorRejectsTrailingConfigSchemaValue(t *testing.T) {
	root := newPackageFixture(t)
	writeFixture(t, root, ConfigSchemaFile, `{"type":"object"} {"type":"object"}`)
	refreshFixtureDigest(t, root)
	_, err := newTestValidator(ValidatorOptions{}).ValidatePackage(root, PackageExpectation{})
	assertValidationCode(t, err, "config_schema")
}

func TestValidatorRejectsArbitraryScriptAndUnsafeCleanup(t *testing.T) {
	root := newPackageFixture(t)
	writeFixture(t, root, PackageManifestFile, validManifestYAML(ConfigSchemaFile)+"assets: [assets/run.sh]\n")
	writeFixture(t, root, "assets/run.sh", "#!/bin/sh\nexit 0\n")
	refreshFixtureDigest(t, root)
	_, err := newTestValidator(ValidatorOptions{}).ValidatePackage(root, PackageExpectation{})
	assertValidationCode(t, err, "executable")

	root = newPackageFixture(t)
	writeFixture(t, root, PackageManifestFile, strings.Replace(validManifestYAML(ConfigSchemaFile), "audit_events: retain", "audit_events: delete", 1))
	refreshFixtureDigest(t, root)
	_, err = newTestValidator(ValidatorOptions{}).ValidatePackage(root, PackageExpectation{})
	assertValidationCode(t, err, "cleanup")
}

func TestValidatorAcceptsOnlyRestrictedDeclarativeMigrationOperations(t *testing.T) {
	root := newPackageFixture(t)
	writeFixture(t, root, PackageManifestFile, validManifestYAML(ConfigSchemaFile)+"migrations:\n  - from: 1.0.0\n    to: 2.0.0\n    file: migrations/1-to-2.json\n")
	writeFixture(t, root, "migrations/1-to-2.json", `{"script":"fetch('https://example.com')"}`)
	refreshFixtureDigest(t, root)
	_, err := newTestValidator(ValidatorOptions{}).ValidatePackage(root, PackageExpectation{})
	assertValidationCode(t, err, "migration")

	writeFixture(t, root, "migrations/1-to-2.json", `{"operations":[{"op":"set","path":"/mode","value":"observe"},{"op":"remove","path":"/legacy"}]}`)
	refreshFixtureDigest(t, root)
	if _, err := newTestValidator(ValidatorOptions{}).ValidatePackage(root, PackageExpectation{}); err != nil {
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
			_, err := newTestValidator(ValidatorOptions{}).ValidatePackage(root, PackageExpectation{})
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

func TestConfigLengthBoundsUseExactNonNegativeIntegers(t *testing.T) {
	maximum := new(big.Int).SetUint64(uint64(^uint(0) >> 1))
	overflow := new(big.Int).Add(maximum, big.NewInt(1)).String()
	for _, bound := range []string{"1e-400", "-1e-400", overflow} {
		schema, err := DecodeConfigSchema([]byte(`{"type":"object","properties":{"value":{"type":"array","minItems":` + bound + `}}}`))
		if err != nil {
			t.Fatal(err)
		}
		if err := ValidateConfig(schema, json.RawMessage(`{"value":[]}`)); err == nil || !strings.Contains(err.Error(), "non-negative integer") {
			t.Fatalf("invalid exact minItems %s error = %v", bound, err)
		}
	}
	for _, bound := range []string{"-0", "-0.0", "-0e400"} {
		schema, err := DecodeConfigSchema([]byte(`{"type":"object","properties":{"value":{"type":"array","minItems":` + bound + `}}}`))
		if err != nil {
			t.Fatal(err)
		}
		if err := ValidateConfig(schema, json.RawMessage(`{"value":[]}`)); err != nil {
			t.Fatalf("numeric zero minItems %s rejected: %v", bound, err)
		}
	}
	hugeExponent := strings.Repeat("9", 256)
	for _, bound := range []string{"0e" + hugeExponent, "0e-" + hugeExponent, "-0e+" + hugeExponent, "-0.000e-" + hugeExponent} {
		schema, err := DecodeConfigSchema([]byte(`{"type":"object","properties":{"value":{"type":"array","minItems":` + bound + `}}}`))
		if err != nil {
			t.Fatal(err)
		}
		if err := ValidateConfig(schema, json.RawMessage(`{"value":[]}`)); err != nil {
			t.Fatalf("arbitrary-exponent numeric zero %s rejected: %v", bound, err)
		}
	}
	for _, bound := range []string{"1e" + hugeExponent, "-1e-" + hugeExponent} {
		schema, err := DecodeConfigSchema([]byte(`{"type":"object","properties":{"value":{"type":"array","minItems":` + bound + `}}}`))
		if err != nil {
			t.Fatal(err)
		}
		if err := ValidateConfig(schema, json.RawMessage(`{"value":[]}`)); err == nil {
			t.Fatalf("non-zero arbitrary-exponent bound %s accepted", bound)
		}
	}

	for name, rawSchema := range map[string]string{
		"items":  `{"type":"object","properties":{"value":{"type":"array","minItems":1e0}}}`,
		"length": `{"type":"object","properties":{"value":{"type":"string","minLength":1e0}}}`,
	} {
		t.Run(name, func(t *testing.T) {
			schema, err := DecodeConfigSchema([]byte(rawSchema))
			if err != nil {
				t.Fatal(err)
			}
			invalid := json.RawMessage(`{"value":[]}`)
			if name == "length" {
				invalid = json.RawMessage(`{"value":""}`)
			}
			if err := ValidateConfig(schema, invalid); err == nil {
				t.Fatalf("runtime ignored exact positive %s bound", name)
			}
		})
	}

	schema, err := DecodeConfigSchema([]byte(`{"type":"object","properties":{"value":{"type":"array","maxItems":` + maximum.String() + `}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateConfig(schema, json.RawMessage(`{"value":[]}`)); err != nil {
		t.Fatalf("supported maximum exact bound rejected: %v", err)
	}
}

func TestValidatorRejectsMalformedCompatibilityAndUnsupportedCleanupMix(t *testing.T) {
	root := newPackageFixture(t)
	writeFixture(t, root, PackageManifestFile, strings.Replace(validManifestYAML(ConfigSchemaFile), `host: ">=1.0.0 <2.0.0"`, `host: "banana"`, 1))
	refreshFixtureDigest(t, root)
	_, err := newTestValidator(ValidatorOptions{}).ValidatePackage(root, PackageExpectation{})
	assertValidationCode(t, err, "compatibility")

	root = newPackageFixture(t)
	writeFixture(t, root, PackageManifestFile, strings.Replace(validManifestYAML(ConfigSchemaFile), "config: delete", "config: retain", 1))
	refreshFixtureDigest(t, root)
	_, err = newTestValidator(ValidatorOptions{}).ValidatePackage(root, PackageExpectation{})
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

func TestApplyMigrationChainRejectsOutputAmplification(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "migrations/amplify.json", `{"operations":[{"op":"copy","from":"/payload","path":"/copy"}]}`)
	manifest := Manifest{Version: "2.0.0", Migrations: []Migration{{From: "1.0.0", To: "2.0.0", File: "migrations/amplify.json"}}}
	raw := json.RawMessage(`{"payload":"` + strings.Repeat("a", 600<<10) + `"}`)
	if _, err := ApplyMigrationChain(root, manifest, "1.0.0", raw); err == nil || !strings.Contains(err.Error(), "byte budget") {
		t.Fatalf("amplifying migration error = %v", err)
	}
}

func TestValidatorAndRunnerShareMigrationDocumentBudget(t *testing.T) {
	root := newPackageFixture(t)
	manifest := validManifestYAML(ConfigSchemaFile) + "migrations:\n  - from: 0.9.0\n    to: 1.0.0\n    file: migrations/large.json\n"
	writeFixture(t, root, PackageManifestFile, manifest)
	writeFixture(t, root, "migrations/large.json", `{"operations":[]}`+strings.Repeat(" ", MaxMigrationDocumentBytes))
	refreshFixtureDigest(t, root)
	if _, err := newTestValidator(ValidatorOptions{}).ValidatePackage(root, PackageExpectation{ID: "official.waf", Version: "1.0.0"}); err == nil || !strings.Contains(err.Error(), "exceeds limit") {
		t.Fatalf("oversized migration validation error = %v", err)
	}
	boundary := append([]byte(`{"operations":[]}`), bytes.Repeat([]byte(" "), MaxMigrationDocumentBytes-len(`{"operations":[]}`))...)
	writeFixtureBytes(t, root, "migrations/large.json", boundary)
	if _, err := ApplyMigrationChain(root, Manifest{Version: "1.0.0", Migrations: []Migration{{From: "0.9.0", To: "1.0.0", File: "migrations/large.json"}}}, "0.9.0", json.RawMessage(`{}`)); err != nil {
		t.Fatalf("boundary migration rejected: %v", err)
	}
}

func TestImageAssetsRejectTrailingPayloadAndExpansion(t *testing.T) {
	imageValue := image.NewRGBA(image.Rect(0, 0, 2, 2))
	imageValue.Set(0, 0, color.White)
	var pngData, jpegData bytes.Buffer
	if err := png.Encode(&pngData, imageValue); err != nil {
		t.Fatal(err)
	}
	if err := jpeg.Encode(&jpegData, imageValue, nil); err != nil {
		t.Fatal(err)
	}
	gifData := []byte{0x47, 0x49, 0x46, 0x38, 0x39, 0x61, 0x01, 0x00, 0x01, 0x00, 0x80, 0x00, 0x00, 0x00, 0x00, 0x00, 0xff, 0xff, 0xff, 0x2c, 0x00, 0x00, 0x00, 0x00, 0x01, 0x00, 0x01, 0x00, 0x00, 0x02, 0x02, 0x44, 0x01, 0x00, 0x3b}
	for _, test := range []struct {
		name string
		data []byte
		tail []byte
	}{{"asset.png", pngData.Bytes(), []byte{0, 0, 0, 0, 'I', 'E', 'N', 'D', 0xae, 0x42, 0x60, 0x82}}, {"asset.jpg", jpegData.Bytes(), []byte{0xff, 0xd9}}, {"asset.gif", gifData, []byte{0x3b}}} {
		full := filepath.Join(t.TempDir(), test.name)
		payload := append(append(append([]byte{}, test.data...), []byte("payload")...), test.tail...)
		if err := os.WriteFile(full, payload, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := validateAssetContent(full, test.name, 1<<20); err == nil {
			t.Fatalf("%s accepted trailing payload and second terminator", test.name)
		}
	}
	expanded := append([]byte{}, pngData.Bytes()...)
	binary.BigEndian.PutUint32(expanded[16:20], 9000)
	binary.BigEndian.PutUint32(expanded[20:24], 9000)
	binary.BigEndian.PutUint32(expanded[29:33], crc32.ChecksumIEEE(expanded[12:29]))
	full := filepath.Join(t.TempDir(), "expanded.png")
	if err := os.WriteFile(full, expanded, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := validateAssetContent(full, "expanded.png", 1<<20); err == nil || !strings.Contains(err.Error(), "budget") {
		t.Fatalf("expanded PNG error = %v", err)
	}
}

func TestMarketTreeRejectsUnreferencedContentAndPackageBudget(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, MarketManifestFile, "schema_version: 1\nname: Empty\nplugins: []\n")
	writeFixture(t, root, "unreferenced.bin", "data")
	if _, err := newTestValidator(ValidatorOptions{}).ValidateMarket(root, false); err == nil || !strings.Contains(err.Error(), "unreferenced") {
		t.Fatalf("unreferenced market content error = %v", err)
	}
	writeFixture(t, root, MarketManifestFile, "schema_version: 1\nname: Many\nplugins:\n  - {id: one.plugin, version: 1.0.0, compatibility: {host: '*', agent: '*'}, package: p1, sha256: "+strings.Repeat("a", 64)+", official: false}\n  - {id: two.plugin, version: 1.0.0, compatibility: {host: '*', agent: '*'}, package: p2, sha256: "+strings.Repeat("b", 64)+", official: false}\n")
	if _, err := newTestValidator(ValidatorOptions{MaxMarketPackages: 1}).ValidateMarket(root, false); err == nil || !strings.Contains(err.Error(), "package count") {
		t.Fatalf("market package budget error = %v", err)
	}
}

func newPackageFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeFixture(t, root, PackageManifestFile, validManifestYAML(ConfigSchemaFile))
	writeFixture(t, root, ConfigSchemaFile, `{"type":"object","properties":{"mode":{"type":"string"}},"additionalProperties":false}`)
	writeFixtureBytes(t, root, "artifacts/policy.wasm", testWASMArtifact())
	refreshFixtureDigest(t, root)
	return root
}

func newRPCPackageFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	artifact := make([]byte, 20)
	copy(artifact, []byte{0x7f, 'E', 'L', 'F', 2, 1, 1, 0})
	binary.LittleEndian.PutUint16(artifact[18:20], 62)
	digest := sha256.Sum256(artifact)
	manifest := fmt.Sprintf(`schema_version: 1
id: example.rpc
version: 1.0.0
name: RPC
compatibility: {host: "*", agent: "*"}
runtime: {kind: rpc-service, abi: "nre:rpc/v1", host_scope: agent, entry: plugin}
artifacts:
  - {path: artifacts/linux-amd64/plugin, sha256: %x, size: %d, mode: executable, goos: linux, goarch: amd64}
extension_points: [dns.provider]
permissions: [agent.read]
config_schema: config.schema.json
resource_budget: {timeout_ms: 1000, memory_bytes: 67108864, concurrency: 4, input_bytes: 65536, output_bytes: 65536, cpu_millis: 500, restarts: 3}
failure_policy: {on_error: degraded, on_budget: fail-closed, restart: on-failure, core_fallback: preserve}
signature: {algorithm: ed25519, key_id: test-fixture, file: package.sig}
cleanup: {instances: delete, config: delete, owned_data: delete, grants: delete, shared_refs: retain, audit_events: retain}
`, digest, len(artifact))
	writeFixture(t, root, PackageManifestFile, manifest)
	writeFixture(t, root, ConfigSchemaFile, `{"type":"object"}`)
	writeFixtureBytes(t, root, "artifacts/linux-amd64/plugin", artifact)
	refreshFixtureDigest(t, root)
	return root
}

func validManifestYAML(schema string) string {
	artifact := testWASMArtifact()
	artifactDigest := sha256.Sum256(artifact)
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
artifacts:
  - path: artifacts/policy.wasm
    sha256: %x
    size: %d
    mode: wasm
extension_points: [http.request]
permissions: [http.inspect]
config_schema: %s
resource_budget:
  timeout_ms: 10
  memory_bytes: 1048576
  concurrency: 8
  input_bytes: 65536
  output_bytes: 65536
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
`, artifactDigest, len(artifact), schema)
}

func refreshFixtureDigest(t *testing.T, root string) {
	t.Helper()
	digest, err := ComputePackageDigest(root)
	if err != nil {
		t.Fatal(err)
	}
	writeFixture(t, root, PackageDigestFile, digest+"\n")
	writeFixture(t, root, PackageSignatureFile, base64.StdEncoding.EncodeToString(ed25519.Sign(testSigningKey(), []byte(digest)))+"\n")
}

func testWASMArtifact() []byte { return []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00} }

func testSigningKey() ed25519.PrivateKey {
	seed := sha256.Sum256([]byte("nre-validator-test-fixture"))
	return ed25519.NewKeyFromSeed(seed[:])
}

func newTestValidator(options ValidatorOptions) *Validator {
	if options.TrustedSigners == nil {
		options.TrustedSigners = map[string]ed25519.PublicKey{}
	}
	options.TrustedSigners["test-fixture"] = testSigningKey().Public().(ed25519.PublicKey)
	return NewValidator(options)
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
