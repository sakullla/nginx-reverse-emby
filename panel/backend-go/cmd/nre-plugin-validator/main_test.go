package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/plugins"
)

func TestRunMarketFlagAcceptsMarketYAMLPathAndDefaultsOfficial(t *testing.T) {
	root := t.TempDir()
	packageRoot := filepath.Join(root, "plugins", "official.example", "1.0.0")
	writeValidatorFixture(t, packageRoot, plugins.PackageManifestFile, `schema_version: 1
id: official.example
version: 1.0.0
name: Example
compatibility: {host: "*", agent: "*"}
extension_points: [http.request]
permissions: [http.inspect]
config_schema: config.schema.json
cleanup: {instances: delete, config: delete, owned_data: delete, grants: delete, shared_refs: retain, audit_events: retain}
`)
	writeValidatorFixture(t, packageRoot, plugins.ConfigSchemaFile, `{"type":"object"}`)
	digest, err := plugins.ComputePackageDigest(packageRoot)
	if err != nil {
		t.Fatal(err)
	}
	writeValidatorFixture(t, packageRoot, plugins.PackageDigestFile, digest)
	marketPath := filepath.Join(root, plugins.MarketManifestFile)
	writeValidatorFixture(t, root, plugins.MarketManifestFile, `schema_version: 1
name: Official
plugins:
  - id: official.example
    version: 1.0.0
    compatibility: {host: "*", agent: "*"}
    package: plugins/official.example/1.0.0
    sha256: `+digest+`
    official: true
`)
	var stdout, stderr bytes.Buffer
	if code := run([]string{"--market", marketPath}, &stdout, &stderr); code != 0 {
		t.Fatalf("run code=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	if !strings.Contains(stdout.String(), `"valid":true`) || !strings.Contains(stdout.String(), `"packages":1`) {
		t.Fatalf("unexpected output: %s", stdout.String())
	}
}

func TestMarketRootFromPathRejectsDirectory(t *testing.T) {
	if _, err := marketRootFromPath(t.TempDir()); err == nil {
		t.Fatal("market directory was accepted; market.yaml path is required")
	}
}

func writeValidatorFixture(t *testing.T, root, name, value string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(value), 0o644); err != nil {
		t.Fatal(err)
	}
}
