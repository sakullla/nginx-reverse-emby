package plugins

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidatorExactNumberBoundsDecimalComplexityBeforeRationalConstruction(t *testing.T) {
	for _, raw := range []string{"1e4097", "1e-4097", "1e1000000000", "1e-1000000000"} {
		if _, ok := exactNumber(json.Number(raw)); ok {
			t.Fatalf("exactNumber(%s) accepted an out-of-budget exponent", raw)
		}
	}
	for _, raw := range []string{"1e4096", "1e-4096"} {
		if _, ok := exactNumber(json.Number(raw)); !ok {
			t.Fatalf("exactNumber(%s) rejected the supported exponent boundary", raw)
		}
	}

	schema, err := DecodeConfigSchema([]byte(`{"type":"object","properties":{"value":{"type":"number","minimum":1e1000000000}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateConfig(schema, json.RawMessage(`{"value":1}`)); err == nil || !strings.Contains(err.Error(), "finite JSON number") {
		t.Fatalf("schema exponent budget error = %v", err)
	}

	configureSchema, err := DecodeConfigSchema([]byte(`{"type":"object","properties":{"value":{"type":"number"}},"required":["value"]}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateConfig(configureSchema, json.RawMessage(`{"value":1e1000000000}`)); err == nil || !strings.Contains(err.Error(), "must be a number") {
		t.Fatalf("configure exponent budget error = %v", err)
	}

	root := newPackageFixture(t)
	writeFixture(t, root, ConfigSchemaFile, `{"type":"object","properties":{"value":{"type":"number","maximum":1e1000000000}}}`)
	refreshFixtureDigest(t, root)
	_, err = newTestValidator(ValidatorOptions{}).ValidatePackage(root, PackageExpectation{})
	assertValidationCode(t, err, "config_schema")
}

func TestWASMPolicyABIRejectsHugeTableBeforeInstantiation(t *testing.T) {
	// One funcref table with a UINT32_MAX minimum. Policy v1 has no table ABI,
	// so the declaration is rejected without allocating or instantiating it.
	tablePayload := []byte{0x01, 0x70, 0x00, 0xff, 0xff, 0xff, 0xff, 0x0f}
	artifact := insertTestWASMSectionBefore(t, testWASMArtifact(), 5, 4, tablePayload)
	name := filepath.Join(t.TempDir(), "huge-table.wasm")
	if err := os.WriteFile(name, artifact, 0o600); err != nil {
		t.Fatal(err)
	}
	err := validatePolicyWASMArtifact(name, 16*int64(wasmPageSizeBytes))
	if err == nil || !strings.Contains(err.Error(), "tables are not allowed") {
		t.Fatalf("huge table rejection error = %v", err)
	}
}

func TestValidatorRejectsSignedPackageWithInvalidMigrationGraph(t *testing.T) {
	tests := []struct {
		name       string
		migrations string
		files      []string
		marker     string
	}{
		{
			name: "duplicate from",
			migrations: "migrations:\n" +
				"  - {from: 0.8.0, to: 0.9.0, file: migrations/a.json}\n" +
				"  - {from: 0.8.0, to: 1.0.0, file: migrations/b.json}\n",
			files:  []string{"migrations/a.json", "migrations/b.json"},
			marker: "duplicate migration from 0.8.0",
		},
		{
			name: "cycle",
			migrations: "migrations:\n" +
				"  - {from: 0.8.0, to: 0.9.0, file: migrations/a.json}\n" +
				"  - {from: 0.9.0, to: 0.8.0, file: migrations/b.json}\n",
			files:  []string{"migrations/a.json", "migrations/b.json"},
			marker: "contains a cycle",
		},
		{
			name:       "chain misses package version",
			migrations: "migrations:\n  - {from: 0.8.0, to: 0.9.0, file: migrations/a.json}\n",
			files:      []string{"migrations/a.json"},
			marker:     "instead of package version 1.0.0",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := newPackageFixture(t)
			writeFixture(t, root, PackageManifestFile, validManifestYAML(ConfigSchemaFile)+test.migrations)
			for _, name := range test.files {
				writeFixture(t, root, name, `{"operations":[]}`)
			}
			refreshFixtureDigest(t, root)
			_, err := newTestValidator(ValidatorOptions{}).ValidatePackage(root, PackageExpectation{})
			assertValidationCode(t, err, "migration")
			if !strings.Contains(err.Error(), test.marker) {
				t.Fatalf("migration graph error = %v, want containing %q", err, test.marker)
			}
		})
	}
}

func TestValidatorAcceptsSignedPackageAndMarketWithMigrationChainsToTarget(t *testing.T) {
	migrations := "migrations:\n" +
		"  - {from: 0.8.0, to: 0.9.0, file: migrations/a.json}\n" +
		"  - {from: 0.9.0, to: 1.0.0, file: migrations/b.json}\n"

	root := newPackageFixture(t)
	writeFixture(t, root, PackageManifestFile, validManifestYAML(ConfigSchemaFile)+migrations)
	writeFixture(t, root, "migrations/a.json", `{"operations":[{"op":"set","path":"/migration_a","value":true}]}`)
	writeFixture(t, root, "migrations/b.json", `{"operations":[{"op":"set","path":"/migration_b","value":true}]}`)
	refreshFixtureDigest(t, root)
	if _, err := newTestValidator(ValidatorOptions{}).ValidatePackage(root, PackageExpectation{}); err != nil {
		t.Fatalf("signed package migration chain rejected: %v", err)
	}

	marketRoot := newSignedMarketFixture(t, []string{"http.request"}, []string{"http.request"})
	setSignedMarketMigrations(t, marketRoot, migrations, []string{"migrations/a.json", "migrations/b.json"})
	if _, err := newTestValidator(ValidatorOptions{}).ValidateMarket(marketRoot, false); err != nil {
		t.Fatalf("signed market migration chain rejected: %v", err)
	}
}

func TestValidatorRejectsSignedMarketWithMigrationChainMissingTarget(t *testing.T) {
	marketRoot := newSignedMarketFixture(t, []string{"http.request"}, []string{"http.request"})
	setSignedMarketMigrations(t, marketRoot,
		"migrations:\n  - {from: 0.8.0, to: 0.9.0, file: migrations/a.json}\n",
		[]string{"migrations/a.json"},
	)
	_, err := newTestValidator(ValidatorOptions{}).ValidateMarket(marketRoot, false)
	assertValidationCode(t, err, "migration")
	if !strings.Contains(err.Error(), "instead of package version 1.0.0") {
		t.Fatalf("signed market migration graph error = %v", err)
	}
}

func TestValidatorProvesNonEmptyExactSchemaDomain(t *testing.T) {
	tests := []struct {
		name   string
		schema string
		marker string
	}{
		{name: "enum type mismatch", schema: `{"type":"object","properties":{"value":{"type":"integer","enum":[1.5]}}}`, marker: "enum has no value"},
		{name: "enum sibling mismatch", schema: `{"type":"object","properties":{"value":{"type":"string","enum":["x"],"minLength":2}}}`, marker: "enum has no value"},
		{name: "exact integer gap", schema: `{"type":"object","properties":{"value":{"type":"integer","minimum":9007199254740992.1,"maximum":9007199254740992.9}}}`, marker: "contains no integer value"},
		{name: "number multiple intersection", schema: `{"type":"object","properties":{"value":{"type":"number","minimum":0.1,"maximum":0.2,"multipleOf":0.3}}}`, marker: "no exact multipleOf value"},
		{name: "integer multiple intersection", schema: `{"type":"object","properties":{"value":{"type":"integer","minimum":2,"maximum":2,"multipleOf":1.5}}}`, marker: "no exact multipleOf value"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := newPackageFixture(t)
			writeFixture(t, root, ConfigSchemaFile, test.schema)
			refreshFixtureDigest(t, root)
			_, err := newTestValidator(ValidatorOptions{}).ValidatePackage(root, PackageExpectation{})
			assertValidationCode(t, err, "config_schema")
			if !strings.Contains(err.Error(), test.marker) {
				t.Fatalf("schema domain error = %v, want containing %q", err, test.marker)
			}
		})
	}
}

func TestValidatorAcceptsExactBoundarySchemaDomainAndRuntimeValue(t *testing.T) {
	root := newPackageFixture(t)
	writeFixture(t, root, ConfigSchemaFile, `{
		"type":"object",
		"properties":{
			"integer":{"type":"integer","enum":[9007199254740993],"minimum":9007199254740992.1,"maximum":9007199254740993,"multipleOf":0.5},
			"number":{"type":"number","minimum":0.1,"maximum":0.1,"multipleOf":0.1}
		},
		"required":["integer","number"],
		"additionalProperties":false
	}`)
	refreshFixtureDigest(t, root)
	validated, err := newTestValidator(ValidatorOptions{}).ValidatePackage(root, PackageExpectation{})
	if err != nil {
		t.Fatalf("exact boundary schema rejected: %v", err)
	}
	if err := ValidateConfig(validated.ConfigSchema, json.RawMessage(`{"integer":9007199254740993,"number":0.1}`)); err != nil {
		t.Fatalf("exact boundary runtime value rejected: %v", err)
	}
}

func insertTestWASMSectionBefore(t *testing.T, module []byte, beforeID, sectionID byte, payload []byte) []byte {
	t.Helper()
	for offset := len(wasmV1Header); offset < len(module); {
		start := offset
		currentID := module[offset]
		offset++
		size, payloadStart := decodeTestULEB128(t, module, offset)
		end := payloadStart + int(size)
		if end > len(module) {
			t.Fatal("fixture section exceeds module")
		}
		if currentID == beforeID {
			section := []byte{sectionID}
			section = append(section, encodeTestULEB128(uint32(len(payload)))...)
			section = append(section, payload...)
			result := append([]byte(nil), module[:start]...)
			result = append(result, section...)
			return append(result, module[start:]...)
		}
		offset = end
	}
	t.Fatalf("fixture section %d not found", beforeID)
	return nil
}

func setSignedMarketMigrations(t *testing.T, marketRoot, migrations string, files []string) {
	t.Helper()
	packageRoot := filepath.Join(marketRoot, "plugins", "official.waf", "1.0.0")
	oldDigestData, err := os.ReadFile(filepath.Join(packageRoot, PackageDigestFile))
	if err != nil {
		t.Fatal(err)
	}
	writeFixture(t, packageRoot, PackageManifestFile, validManifestYAML(ConfigSchemaFile)+migrations)
	for _, name := range files {
		writeFixture(t, packageRoot, name, `{"operations":[{"op":"set","path":"/migrated","value":true}]}`)
	}
	refreshFixtureDigest(t, packageRoot)
	newDigestData, err := os.ReadFile(filepath.Join(packageRoot, PackageDigestFile))
	if err != nil {
		t.Fatal(err)
	}
	marketData, err := os.ReadFile(filepath.Join(marketRoot, MarketManifestFile))
	if err != nil {
		t.Fatal(err)
	}
	oldDigest := strings.TrimSpace(string(oldDigestData))
	newDigest := strings.TrimSpace(string(newDigestData))
	updated := strings.Replace(string(marketData), oldDigest, newDigest, 1)
	if updated == string(marketData) {
		t.Fatal("market fixture package digest was not updated")
	}
	writeFixture(t, marketRoot, MarketManifestFile, updated)
}
