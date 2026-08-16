//go:build !integration

package plugins

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

// One funcref table with a UINT32_MAX minimum. Policy v1 has no table ABI,
// so the declaration is rejected without allocating or instantiating it.

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

func insertTestWASMSectionBefore(t *testing.T, module []byte, beforeID, sectionID byte, payload []byte) []byte {
	t.Helper()
	for offset := pluginsdk.WASMModuleV1HeaderSize; offset < len(module); {
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
