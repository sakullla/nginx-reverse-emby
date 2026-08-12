package plugins

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
	"github.com/sakullla/nginx-reverse-emby/plugin-sdk/go/protoschema"
)

func TestOfficialMarketV1ValidatesNineCanonicalPackages(t *testing.T) {
	root, publicKey, _ := buildOfficialMarketV1Fixture(t)
	validator := officialFixtureValidator(publicKey)
	validated, err := validator.ValidateMarket(root, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(validated.Packages) != 9 || len(validated.Manifest.Entries) != 9 {
		t.Fatalf("validated official package count = %d/%d", len(validated.Packages), len(validated.Manifest.Entries))
	}
	for index, pkg := range validated.Packages {
		entry := validated.Manifest.Entries[index]
		if pkg.Manifest.ID != entry.ID || pkg.Manifest.Signature.File != OfficialPackageSignatureFile || entry.SignatureKeyID != OfficialSignatureKeyID || !entry.Official {
			t.Fatalf("official package %d projection mismatch: %+v / %+v", index, pkg.Manifest, entry)
		}
	}
}

func TestOfficialMarketV1PackagesSupportStandaloneRevalidation(t *testing.T) {
	root, publicKey, _ := buildOfficialMarketV1Fixture(t)
	validator := officialFixtureValidator(publicKey)
	validated, err := validator.ValidateMarket(root, true)
	if err != nil {
		t.Fatal(err)
	}

	for index, candidate := range validated.Packages {
		entry := validated.Manifest.Entries[index]
		revalidated, err := validator.ValidatePackage(candidate.Root, PackageExpectation{
			ID:      entry.ID,
			Version: entry.Version,
			SHA256:  entry.PackageSHA256,
		})
		if err != nil {
			t.Fatalf("ValidatePackage(%s@%s) error = %v", entry.ID, entry.Version, err)
		}
		if revalidated.Digest != candidate.Digest {
			t.Fatalf("ValidatePackage(%s@%s) digest = %s, want %s", entry.ID, entry.Version, revalidated.Digest, candidate.Digest)
		}
	}
}

func TestOfficialMarketV1AllowsKnownRootMetadataOnly(t *testing.T) {
	root, publicKey, _ := buildOfficialMarketV1Fixture(t)
	writeFixture(t, root, OfficialMarketAgentsFile, "# Official release projection\n")
	if _, err := officialFixtureValidator(publicKey).ValidateMarket(root, true); err != nil {
		t.Fatalf("ValidateMarket() rejected known inert root metadata: %v", err)
	}

	writeFixture(t, root, "unreferenced.txt", "not part of the market contract\n")
	_, err := officialFixtureValidator(publicKey).ValidateMarket(root, true)
	assertValidationCode(t, err, "market_tree")
}

func TestOfficialMarketV1RejectsEnvelopeAndPayloadTampering(t *testing.T) {
	root, publicKey, _ := buildOfficialMarketV1Fixture(t)
	packageRoot := filepath.Join(root, "packages", "accelerator-sources", "1.0.0")
	artifactPath := filepath.Join(packageRoot, "artifacts", "linux-amd64", "accelerator-sources")

	tests := map[string]func(string){
		"market manifest": func(copyRoot string) {
			name := filepath.Join(copyRoot, MarketManifestFile)
			file, err := os.OpenFile(name, os.O_APPEND|os.O_WRONLY, 0)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := file.Write([]byte("\n")); err != nil {
				t.Fatal(err)
			}
			if err := file.Close(); err != nil {
				t.Fatal(err)
			}
		},
		"plugin manifest": func(copyRoot string) {
			name := filepath.Join(copyRoot, "packages", "accelerator-sources", "1.0.0", PackageManifestFile)
			data, err := os.ReadFile(name)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(name, append(data, []byte("metadata:\n  tampered: yes\n")...), 0o644); err != nil {
				t.Fatal(err)
			}
		},
		"package files": func(copyRoot string) {
			name := filepath.Join(copyRoot, "packages", "accelerator-sources", "1.0.0", OfficialPackageFilesFile)
			data, err := os.ReadFile(name)
			if err != nil {
				t.Fatal(err)
			}
			data = []byte(strings.Replace(string(data), `"payload_sha256":"`, `"payload_sha256":"0`, 1))
			if err := os.WriteFile(name, data, 0o644); err != nil {
				t.Fatal(err)
			}
		},
		"artifact": func(copyRoot string) {
			name := filepath.Join(copyRoot, strings.TrimPrefix(artifactPath, root+string(filepath.Separator)))
			file, err := os.OpenFile(name, os.O_APPEND|os.O_WRONLY, 0)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := file.Write([]byte("tampered")); err != nil {
				t.Fatal(err)
			}
			if err := file.Close(); err != nil {
				t.Fatal(err)
			}
		},
		"signature": func(copyRoot string) {
			name := filepath.Join(copyRoot, "packages", "accelerator-sources", "1.0.0", OfficialPackageSignatureFile)
			data, err := os.ReadFile(name)
			if err != nil {
				t.Fatal(err)
			}
			var signature officialPackageSignatureV1
			if err := json.Unmarshal(data, &signature); err != nil {
				t.Fatal(err)
			}
			if signature.Signature[0] == 'A' {
				signature.Signature = "B" + signature.Signature[1:]
			} else {
				signature.Signature = "A" + signature.Signature[1:]
			}
			writeOfficialJSONFixture(t, name, signature)
		},
		"complete package digest": func(copyRoot string) {
			name := filepath.Join(copyRoot, "packages", "accelerator-sources", "1.0.0", OfficialPackageSignatureFile)
			file, err := os.OpenFile(name, os.O_APPEND|os.O_WRONLY, 0)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := file.Write([]byte("\n")); err != nil {
				t.Fatal(err)
			}
			if err := file.Close(); err != nil {
				t.Fatal(err)
			}
		},
	}

	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			copyRoot := t.TempDir()
			if err := os.CopyFS(copyRoot, os.DirFS(root)); err != nil {
				t.Fatal(err)
			}
			mutate(copyRoot)
			if _, err := officialFixtureValidator(publicKey).ValidateMarket(copyRoot, true); err == nil {
				t.Fatal("tampered official package was accepted")
			}
		})
	}
}

func TestOfficialMarketV1RejectsASCIIHexDigestSignature(t *testing.T) {
	root, publicKey, privateKey := buildOfficialMarketV1Fixture(t)
	packageRoot := filepath.Join(root, "packages", "accelerator-sources", "1.0.0")
	filesData, err := os.ReadFile(filepath.Join(packageRoot, OfficialPackageFilesFile))
	if err != nil {
		t.Fatal(err)
	}
	var files officialPackageFileManifestV1
	if err := json.Unmarshal(filesData, &files); err != nil {
		t.Fatal(err)
	}
	signature := officialPackageSignatureV1{
		SchemaVersion: 1, Algorithm: "ed25519", Identity: OfficialSignatureKeyID,
		PayloadSHA256: files.PayloadSHA256,
		Signature:     base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, []byte(files.PayloadSHA256))),
	}
	writeOfficialJSONFixture(t, filepath.Join(packageRoot, OfficialPackageSignatureFile), signature)
	if _, err := officialFixtureValidator(publicKey).ValidateMarket(root, true); err == nil {
		t.Fatal("signature over ASCII hexadecimal digest was accepted")
	}
}

func TestOfficialMarketV1AcceptsSDKCommitMovementOnlyWithStableDescriptor(t *testing.T) {
	root, publicKey, privateKey := buildOfficialMarketV1Fixture(t)
	provenancePath := filepath.Join(root, OfficialMarketProvenanceFile)
	data, err := os.ReadFile(provenancePath)
	if err != nil {
		t.Fatal(err)
	}
	var provenance officialMarketProvenanceV1
	if err := json.Unmarshal(data, &provenance); err != nil {
		t.Fatal(err)
	}
	provenance.SDKRepositoryCommit = strings.Repeat("c", 40)
	writeOfficialJSONFixture(t, provenancePath, provenance)
	signOfficialProvenanceFixture(t, root, privateKey)
	if _, err := officialFixtureValidator(publicKey).ValidateMarket(root, true); err != nil {
		t.Fatalf("ValidateMarket() rejected a new full SDK commit with the stable descriptor: %v", err)
	}

	for name, mutate := range map[string]func(*officialMarketProvenanceV1){
		"abbreviated SDK commit": func(value *officialMarketProvenanceV1) { value.SDKRepositoryCommit = "9e2d915c" },
		"zero SDK commit":        func(value *officialMarketProvenanceV1) { value.SDKRepositoryCommit = strings.Repeat("0", 40) },
		"changed descriptor":     func(value *officialMarketProvenanceV1) { value.SDKDescriptorSHA256 = strings.Repeat("d", 64) },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := provenance
			mutate(&candidate)
			writeOfficialJSONFixture(t, provenancePath, candidate)
			signOfficialProvenanceFixture(t, root, privateKey)
			if _, err := officialFixtureValidator(publicKey).ValidateMarket(root, true); err == nil {
				t.Fatal("invalid SDK provenance was accepted")
			}
		})
	}
}

func TestOfficialMarketV1RejectsUnsignedProvenanceMutation(t *testing.T) {
	root, publicKey, _ := buildOfficialMarketV1Fixture(t)
	provenancePath := filepath.Join(root, OfficialMarketProvenanceFile)
	data, err := os.ReadFile(provenancePath)
	if err != nil {
		t.Fatal(err)
	}
	var provenance officialMarketProvenanceV1
	if err := json.Unmarshal(data, &provenance); err != nil {
		t.Fatal(err)
	}
	provenance.SDKRepositoryCommit = strings.Repeat("c", 40)
	writeOfficialJSONFixture(t, provenancePath, provenance)
	_, err = officialFixtureValidator(publicKey).ValidateMarket(root, true)
	assertValidationCode(t, err, "market_signature")
}

func officialFixtureValidator(publicKey ed25519.PublicKey) *Validator {
	validator := NewValidator(ValidatorOptions{})
	validator.trustedSigners[OfficialSignatureKeyID] = append(ed25519.PublicKey(nil), publicKey...)
	return validator
}

func buildOfficialMarketV1Fixture(t *testing.T) (string, ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	_, rpcArtifact := buildTestRPCExecutable(t, "linux", "amd64")
	wasmArtifact := testWASMArtifact()
	type packageSpec struct {
		id, runtime, abi, policyKind string
		artifact                     []byte
	}
	specs := []packageSpec{
		{id: "accelerator-sources", runtime: pluginsdk.RuntimeRPCService, abi: pluginsdk.RPCABIV1, artifact: rpcArtifact},
		{id: "cloudflare-dns", runtime: pluginsdk.RuntimeRPCService, abi: pluginsdk.RPCABIV1, artifact: rpcArtifact},
		{id: "docker-app", runtime: pluginsdk.RuntimeRPCService, abi: pluginsdk.RPCABIV1, artifact: rpcArtifact},
		{id: "doh", runtime: pluginsdk.RuntimeRPCService, abi: pluginsdk.RPCABIV1, artifact: rpcArtifact},
		{id: "ip-policy", runtime: pluginsdk.RuntimeWASMPolicy, abi: pluginsdk.PolicyABIV1, policyKind: "ip", artifact: wasmArtifact},
		{id: "rate-limit", runtime: pluginsdk.RuntimeWASMPolicy, abi: pluginsdk.PolicyABIV1, policyKind: "rate", artifact: wasmArtifact},
		{id: "reverse-l4", runtime: pluginsdk.RuntimeRPCService, abi: pluginsdk.RPCABIV1, artifact: rpcArtifact},
		{id: "shadowsocks-server", runtime: pluginsdk.RuntimeRPCService, abi: pluginsdk.RPCABIV1, artifact: rpcArtifact},
		{id: "waf", runtime: pluginsdk.RuntimeWASMPolicy, abi: pluginsdk.PolicyABIV1, policyKind: "waf", artifact: wasmArtifact},
	}
	marketEntries := make([]officialMarketPackageV1, 0, len(specs))
	for _, spec := range specs {
		packageRoot := filepath.Join(root, "packages", spec.id, "1.0.0")
		artifactDigest := sha256.Sum256(spec.artifact)
		artifactPath := "artifacts/" + spec.id + ".wasm"
		artifactYAML := fmt.Sprintf("  - {path: %s, sha256: %x, size: %d, mode: wasm}\n", artifactPath, artifactDigest, len(spec.artifact))
		runtimeYAML := fmt.Sprintf("runtime: {kind: wasm-policy, abi: \"nre:policy/v1\", host_scope: agent, entry: %s, policy_kind: %s}\n", artifactPath, spec.policyKind)
		budgetYAML := "resource_budget: {timeout_ms: 2, memory_bytes: 1048576, concurrency: 8, input_bytes: 65536, output_bytes: 4096}\n"
		failureYAML := "failure_policy: {on_error: fail-open, on_budget: fail-open, restart: never, core_fallback: preserve}\n"
		extensionsYAML := "extension_points: [http.request]\npermissions: [{name: http.inspect}]\n"
		if spec.runtime == pluginsdk.RuntimeRPCService {
			artifactPath = "artifacts/linux-amd64/" + spec.id
			artifactYAML = fmt.Sprintf("  - {path: %s, sha256: %x, size: %d, mode: executable, goos: linux, goarch: amd64}\n", artifactPath, artifactDigest, len(spec.artifact))
			runtimeYAML = fmt.Sprintf("runtime: {kind: rpc-service, abi: \"nre:rpc/v1\", host_scope: agent, entry: %s}\n", spec.id)
			budgetYAML = "resource_budget: {timeout_ms: 30000, memory_bytes: 268435456, concurrency: 8, input_bytes: 1048576, output_bytes: 1048576, cpu_millis: 1000, restarts: 3}\n"
			failureYAML = "failure_policy: {on_error: fail-closed, on_budget: fail-closed, restart: on-failure, core_fallback: preserve}\n"
			extensionsYAML = "extension_points: [dns.provider]\npermissions: [{name: dns.manage}]\n"
		}
		manifest := fmt.Sprintf("schema_version: 1\nid: %s\nversion: 1.0.0\nname: %s\ncompatibility: {host: \"*\", agent: \"*\"}\n%sartifacts:\n%s%sconfig_schema: config.schema.json\n%s%ssignature: {algorithm: ed25519, key_id: %s, file: signature.json}\ncleanup: {instances: delete, config: delete, owned_data: delete, grants: delete, shared_refs: retain, audit_events: retain}\n", spec.id, spec.id, runtimeYAML, artifactYAML, extensionsYAML, budgetYAML, failureYAML, OfficialSignatureKeyID)
		writeFixture(t, packageRoot, PackageManifestFile, manifest)
		writeFixture(t, packageRoot, ConfigSchemaFile, `{"type":"object","additionalProperties":false}`)
		writeFixture(t, packageRoot, "NOTICE", "Sakullla official fixture\n")
		writeFixture(t, packageRoot, "sbom.spdx.json", `{"spdxVersion":"SPDX-2.3"}`)
		writeFixtureBytes(t, packageRoot, artifactPath, spec.artifact)

		records := officialTestPayloadRecords(t, packageRoot, artifactPath)
		payloadDigest := digestOfficialFileRecordsV1(records)
		files := officialPackageFileManifestV1{SchemaVersion: 1, PayloadSHA256: payloadDigest, Files: records}
		writeOfficialJSONFixture(t, filepath.Join(packageRoot, OfficialPackageFilesFile), files)
		payload, err := hex.DecodeString(payloadDigest)
		if err != nil {
			t.Fatal(err)
		}
		signature := officialPackageSignatureV1{
			SchemaVersion: 1, Algorithm: "ed25519", Identity: OfficialSignatureKeyID, PayloadSHA256: payloadDigest,
			Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, payload)),
		}
		writeOfficialJSONFixture(t, filepath.Join(packageRoot, OfficialPackageSignatureFile), signature)
		allRecords := officialTestAllRecords(t, packageRoot, records)
		marketEntries = append(marketEntries, officialMarketPackageV1{
			ID: spec.id, Version: "1.0.0", Runtime: spec.runtime, ABI: spec.abi,
			PackageSHA256: digestOfficialFileRecordsV1(allRecords), PackageURL: "packages/" + spec.id + "/1.0.0", SignerIdentity: OfficialSignatureKeyID,
		})
	}

	var market strings.Builder
	fmt.Fprintf(&market, "schema_version: 1\ncommit: %s\nsdk_abi: %s,%s\npackages:\n", strings.Repeat("a", 40), pluginsdk.PolicyABIV1, pluginsdk.RPCABIV1)
	for _, entry := range marketEntries {
		fmt.Fprintf(&market, "  - id: %s\n    version: %s\n    runtime: %s\n    abi: %s\n    package_sha256: %s\n    package_url: %s\n    signer_identity: %s\n", entry.ID, entry.Version, entry.Runtime, entry.ABI, entry.PackageSHA256, entry.PackageURL, entry.SignerIdentity)
	}
	marketData := []byte(market.String())
	writeFixtureBytes(t, root, MarketManifestFile, marketData)
	marketDigest := sha256.Sum256(marketData)
	provenancePackages := make([]officialPackageProvenanceV1, 0, len(marketEntries))
	for _, entry := range marketEntries {
		provenancePackages = append(provenancePackages, officialPackageProvenanceV1{ID: entry.ID, Version: entry.Version, Path: entry.PackageURL, PackageSHA256: entry.PackageSHA256})
	}
	provenance := officialMarketProvenanceV1{
		SchemaVersion: 1, RepositoryCommit: strings.Repeat("a", 40), MarketSHA256: hex.EncodeToString(marketDigest[:]),
		SDKRepositoryCommit: strings.Repeat("b", 40), SDKDescriptorSHA256: protoschema.CanonicalDescriptorSetSHA256,
		SDKABIs: []string{pluginsdk.PolicyABIV1, pluginsdk.RPCABIV1}, SignerIdentity: OfficialSignatureKeyID, Packages: provenancePackages,
	}
	writeOfficialJSONFixture(t, filepath.Join(root, OfficialMarketProvenanceFile), provenance)
	signOfficialProvenanceFixture(t, root, privateKey)
	return root, publicKey, privateKey
}

func signOfficialProvenanceFixture(t *testing.T, root string, privateKey ed25519.PrivateKey) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, OfficialMarketProvenanceFile))
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(data)
	signature := officialPackageSignatureV1{
		SchemaVersion: 1,
		Algorithm:     "ed25519",
		Identity:      OfficialSignatureKeyID,
		PayloadSHA256: hex.EncodeToString(digest[:]),
		Signature:     base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, digest[:])),
	}
	writeOfficialJSONFixture(t, filepath.Join(root, OfficialMarketSignatureFile), signature)
}

func officialTestPayloadRecords(t *testing.T, root, executablePath string) []officialFileRecordV1 {
	t.Helper()
	var records []officialFileRecordV1
	err := filepath.Walk(root, func(name string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		relative, err := filepath.Rel(root, name)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(name)
		if err != nil {
			return err
		}
		digest := sha256.Sum256(data)
		mode := "0644"
		if filepath.ToSlash(relative) == executablePath && !strings.HasSuffix(executablePath, ".wasm") {
			mode = "0755"
		}
		records = append(records, officialFileRecordV1{Path: filepath.ToSlash(relative), Mode: mode, SHA256: hex.EncodeToString(digest[:]), Size: info.Size()})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Slice(records, func(i, j int) bool { return records[i].Path < records[j].Path })
	return records
}

func officialTestAllRecords(t *testing.T, root string, payload []officialFileRecordV1) []officialFileRecordV1 {
	t.Helper()
	records := append([]officialFileRecordV1(nil), payload...)
	for _, name := range []string{OfficialPackageFilesFile, OfficialPackageSignatureFile} {
		data, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256(data)
		records = append(records, officialFileRecordV1{Path: name, Mode: "0644", SHA256: hex.EncodeToString(digest[:]), Size: int64(len(data))})
	}
	sort.Slice(records, func(i, j int) bool { return records[i].Path < records[j].Path })
	return records
}

func writeOfficialJSONFixture(t *testing.T, name string, value any) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, data, 0o644); err != nil {
		t.Fatal(err)
	}
}
