package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/plugins"
)

func TestRuntimeValidatorCLIValidatesSignedCustomMarket(t *testing.T) {
	root := t.TempDir()
	packageRoot := filepath.Join(root, "plugins", "official.example", "1.0.0")
	artifact := validatorPolicyWASMFixture(t)
	artifactDigest := sha256.Sum256(artifact)
	writeValidatorFixture(t, packageRoot, plugins.PackageManifestFile, fmt.Sprintf(`schema_version: 1
id: official.example
version: 1.0.0
name: Example
compatibility: {host: "*", agent: "*"}
runtime: {kind: wasm-policy, abi: "nre:policy/v1", host_scope: agent, entry: artifacts/policy.wasm, policy_kind: waf}
artifacts:
  - {path: artifacts/policy.wasm, sha256: %x, size: %d, mode: wasm}
extension_points: [http.request]
permissions: [http.inspect]
config_schema: config.schema.json
resource_budget: {timeout_ms: 2, memory_bytes: 1048576, concurrency: 8, input_bytes: 65536, output_bytes: 4096}
failure_policy: {on_error: fail-open, on_budget: fail-open, restart: never, core_fallback: preserve}
signature: {algorithm: ed25519, key_id: test-cli, file: package.sig}
cleanup: {instances: delete, config: delete, owned_data: delete, grants: delete, shared_refs: retain, audit_events: retain}
`, artifactDigest, len(artifact)))
	writeValidatorFixture(t, packageRoot, plugins.ConfigSchemaFile, `{"type":"object"}`)
	writeValidatorFixtureBytes(t, packageRoot, "artifacts/policy.wasm", artifact)
	digest, err := plugins.ComputePackageDigest(packageRoot)
	if err != nil {
		t.Fatal(err)
	}
	writeValidatorFixture(t, packageRoot, plugins.PackageDigestFile, digest)
	privateKey := validatorTestSigningKey()
	writeValidatorFixture(t, packageRoot, plugins.PackageSignatureFile, base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, []byte(digest))))
	marketPath := filepath.Join(root, plugins.MarketManifestFile)
	writeValidatorFixture(t, root, plugins.MarketManifestFile, `schema_version: 1
name: Official
plugins:
  - id: official.example
    version: 1.0.0
    capabilities: [http.request]
    compatibility: {host: "*", agent: "*"}
    runtime: {kind: wasm-policy, abi: "nre:policy/v1", host_scope: agent, policy_kind: waf}
    artifacts:
      - {sha256: `+fmt.Sprintf("%x", artifactDigest)+`, size: `+fmt.Sprintf("%d", len(artifact))+`}
    package: plugins/official.example/1.0.0
    sha256: `+digest+`
    signature_key_id: test-cli
    provenance: custom
    official: false
`)
	var stdout, stderr bytes.Buffer
	publicKey := base64.StdEncoding.EncodeToString(privateKey.Public().(ed25519.PublicKey))
	if code := run([]string{"--market", marketPath, "--official=false", "--trusted-key", "test-cli=" + publicKey}, &stdout, &stderr); code != 0 {
		t.Fatalf("run code=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	if !strings.Contains(stdout.String(), `"valid":true`) || !strings.Contains(stdout.String(), `"packages":1`) {
		t.Fatalf("unexpected output: %s", stdout.String())
	}
	var output validationOutput
	if err := json.Unmarshal(stdout.Bytes(), &output); err != nil {
		t.Fatal(err)
	}
	wantDetails := []validationPackageOutput{{
		ID: "official.example", Version: "1.0.0", PackagePath: "plugins/official.example/1.0.0",
		RuntimeKind: "wasm-policy", RuntimeABI: "nre:policy/v1", RuntimeEntry: "artifacts/policy.wasm",
		ArtifactSHA256: fmt.Sprintf("%x", artifactDigest), Artifacts: []validationArtifactOutput{{
			Path: "artifacts/policy.wasm", SHA256: fmt.Sprintf("%x", artifactDigest), Size: int64(len(artifact)), Mode: "wasm",
		}},
	}}
	if !reflect.DeepEqual(output.PackageDetails, wantDetails) {
		t.Fatalf("package details = %+v", output.PackageDetails)
	}
}

func validatorPolicyWASMFixture(t *testing.T) []byte {
	t.Helper()
	encoded, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "plugin-sdk", "policy", "v1", "testdata", "compatible_guest.wasm.hex"))
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := hex.DecodeString(strings.TrimSpace(string(encoded)))
	if err != nil {
		t.Fatal(err)
	}
	return decoded
}

func TestValidatorCLIMarketFlagDefaultsOfficial(t *testing.T) {
	root := t.TempDir()
	marketPath := filepath.Join(root, plugins.MarketManifestFile)
	writeValidatorFixture(t, root, plugins.MarketManifestFile, "schema_version: 1\nname: Official\nplugins: []\n")
	var stdout, stderr bytes.Buffer
	if code := run([]string{"--market", marketPath}, &stdout, &stderr); code != 1 || !strings.Contains(stdout.String(), `"valid":false`) || !strings.Contains(stdout.String(), `"market_schema"`) {
		t.Fatalf("run code=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
}

func TestMarketRootFromPathRejectsDirectory(t *testing.T) {
	if _, err := marketRootFromPath(t.TempDir()); err == nil {
		t.Fatal("market directory was accepted; market.yaml path is required")
	}
}

func TestValidatorCLITrustedSignatureKeyParsing(t *testing.T) {
	publicKey := base64.StdEncoding.EncodeToString(validatorTestSigningKey().Public().(ed25519.PublicKey))
	parsed, err := parseTrustedSigners([]string{"release=" + publicKey})
	if err != nil || len(parsed["release"]) != ed25519.PublicKeySize {
		t.Fatalf("trusted signer parse = %v, %v", parsed, err)
	}
	if _, err := parseTrustedSigners([]string{"release=" + publicKey, "release=" + publicKey}); err == nil {
		t.Fatal("duplicate trusted signer was accepted")
	}
	if _, err := parseTrustedSigners([]string{"release=not-base64"}); err == nil {
		t.Fatal("malformed trusted signer was accepted")
	}
	if _, err := parseTrustedSigners([]string{plugins.OfficialSignatureKeyID + "=" + publicKey}); err == nil {
		t.Fatal("official signature root override was accepted")
	}
	for _, keyID := range []string{"Uppercase", "under_score", "1starts-with-digit", " release "} {
		if _, err := parseTrustedSigners([]string{keyID + "=" + publicKey}); err == nil {
			t.Fatalf("non-canonical trusted key identity %q was accepted", keyID)
		}
	}
	for _, encoded := range []string{" " + publicKey, publicKey + " "} {
		if _, err := parseTrustedSigners([]string{"release=" + encoded}); err == nil {
			t.Fatalf("non-canonical trusted public key %q was accepted", encoded)
		}
	}
}

func writeValidatorFixture(t *testing.T, root, name, value string) {
	t.Helper()
	writeValidatorFixtureBytes(t, root, name, []byte(value))
}

func writeValidatorFixtureBytes(t *testing.T, root, name string, value []byte) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, value, 0o644); err != nil {
		t.Fatal(err)
	}
}

func validatorTestSigningKey() ed25519.PrivateKey {
	seed := sha256.Sum256([]byte("nre-cli-validator-test-fixture"))
	return ed25519.NewKeyFromSeed(seed[:])
}
