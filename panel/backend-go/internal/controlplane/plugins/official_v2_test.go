package plugins

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
	"github.com/sakullla/nginx-reverse-emby/plugin-sdk/go/protoschema"
)

func TestOfficialMarketV2ValidatesThinIndexWithoutPackages(t *testing.T) {
	root, publicKey, _ := buildOfficialMarketV2Fixture(t)
	validated, err := officialFixtureValidator(publicKey).ValidateMarket(root, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(validated.Packages) != 0 || len(validated.Manifest.Entries) != 1 {
		t.Fatalf("thin index projection = %d cached packages / %d entries", len(validated.Packages), len(validated.Manifest.Entries))
	}
	entry := validated.Manifest.Entries[0]
	if entry.BlobFormat != officialPackageBlobFormatV1 || entry.BlobSize != 123 || !entry.Official || !strings.HasPrefix(entry.PackagePath, "https://github.com/sakullla/sakullla-plugins/releases/download/official-") {
		t.Fatalf("thin index transport projection = %+v", entry)
	}
	if _, err := os.Stat(filepath.Join(root, "packages")); !os.IsNotExist(err) {
		t.Fatal("thin index fixture unexpectedly contains package payloads")
	}
}

func TestOfficialMarketV2SignatureCoversExactIndexBytes(t *testing.T) {
	root, publicKey, _ := buildOfficialMarketV2Fixture(t)
	name := filepath.Join(root, OfficialMarketProvenanceFile)
	data, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, append(data, '\r', '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := officialFixtureValidator(publicKey).ValidateMarket(root, true); err == nil || !strings.Contains(err.Error(), "market_signature") {
		t.Fatalf("CRLF-altered signed provenance was accepted: %v", err)
	}
}

func buildOfficialMarketV2Fixture(t *testing.T) (string, ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	v1, publicKey, privateKey := buildOfficialMarketV1Fixture(t)
	validated, err := officialFixtureValidator(publicKey).ValidateMarket(v1, true)
	if err != nil {
		t.Fatal(err)
	}
	entry := validated.Manifest.Entries[0]
	root := t.TempDir()
	commit := strings.Repeat("a", 40)
	entry.Description = "Official fixture"
	entry.BlobSHA256 = strings.Repeat("e", 64)
	entry.BlobSize = 123
	entry.BlobFormat = officialPackageBlobFormatV1
	entry.PackagePath = officialPackageBlobURL(commit, entry.ID, entry.Version, entry.BlobSHA256)
	var market strings.Builder
	fmt.Fprintf(&market, "schema_version: 2\ncommit: %s\nsdk_abi: %s,%s\npackages:\n", commit, pluginsdk.PolicyABIV1, pluginsdk.RPCABIV1)
	fmt.Fprintf(&market, "  - id: %s\n    version: %s\n    description: %q\n    capabilities:\n", entry.ID, entry.Version, entry.Description)
	for _, capability := range entry.Capabilities {
		fmt.Fprintf(&market, "      - %q\n", capability)
	}
	fmt.Fprintf(&market, "    compatibility: {host: %q, agent: %q}\n    runtime: %s\n    abi: %q\n    host_scope: %s\n", entry.Compatibility.Host, entry.Compatibility.Agent, entry.Runtime.Kind, entry.Runtime.ABI, entry.Runtime.HostScope)
	if entry.Runtime.PolicyKind != "" {
		fmt.Fprintf(&market, "    policy_kind: %s\n", entry.Runtime.PolicyKind)
	}
	market.WriteString("    artifacts:\n")
	for _, artifact := range entry.Artifacts {
		fmt.Fprintf(&market, "      - sha256: %s\n        size: %d\n", artifact.SHA256, artifact.Size)
		if artifact.GOOS != "" {
			fmt.Fprintf(&market, "        goos: %s\n", artifact.GOOS)
		}
		if artifact.GOARCH != "" {
			fmt.Fprintf(&market, "        goarch: %s\n", artifact.GOARCH)
		}
	}
	fmt.Fprintf(&market, "    package_sha256: %s\n    package_url: %q\n    blob_sha256: %s\n    blob_size: %d\n    blob_format: %s\n    signer_identity: %s\n", entry.PackageSHA256, entry.PackagePath, entry.BlobSHA256, entry.BlobSize, entry.BlobFormat, entry.SignatureKeyID)
	marketData := []byte(market.String())
	writeFixtureBytes(t, root, MarketManifestFile, marketData)
	marketDigest := sha256.Sum256(marketData)
	provenance := officialMarketProvenanceV2{
		SchemaVersion: 2, RepositoryCommit: commit, MarketSHA256: hex.EncodeToString(marketDigest[:]), SDKRepositoryCommit: strings.Repeat("b", 40), SDKDescriptorSHA256: protoschema.CanonicalDescriptorSetSHA256,
		SDKABIs: []string{pluginsdk.PolicyABIV1, pluginsdk.RPCABIV1}, SignerIdentity: OfficialSignatureKeyID,
		Packages: []officialPackageProvenanceV2{{ID: entry.ID, Version: entry.Version, PackageSHA256: entry.PackageSHA256, PackageURL: entry.PackagePath, BlobSHA256: entry.BlobSHA256, BlobSize: entry.BlobSize, BlobFormat: entry.BlobFormat}},
	}
	writeOfficialJSONFixture(t, filepath.Join(root, OfficialMarketProvenanceFile), provenance)
	signOfficialProvenanceFixture(t, root, privateKey)
	return root, publicKey, privateKey
}
