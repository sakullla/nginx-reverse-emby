//go:build !integration

package plugins

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"debug/macho"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
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

func TestRuntimePolicyKindIsRequiredOnlyForWASMPolicy(t *testing.T) {
	tests := []struct {
		name, runtimeKind, policyKind string
		wantErr                       bool
	}{
		{name: "waf", runtimeKind: pluginsdk.RuntimeWASMPolicy, policyKind: "waf"},
		{name: "ip", runtimeKind: pluginsdk.RuntimeWASMPolicy, policyKind: "ip"},
		{name: "rate", runtimeKind: pluginsdk.RuntimeWASMPolicy, policyKind: "rate"},
		{name: "missing", runtimeKind: pluginsdk.RuntimeWASMPolicy, wantErr: true},
		{name: "unknown", runtimeKind: pluginsdk.RuntimeWASMPolicy, policyKind: "firewall", wantErr: true},
		{name: "rpc empty", runtimeKind: pluginsdk.RuntimeRPCService},
		{name: "rpc policy kind", runtimeKind: pluginsdk.RuntimeRPCService, policyKind: "waf", wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateRuntimePolicyKind(test.runtimeKind, test.policyKind)
			if (err != nil) != test.wantErr {
				t.Fatalf("validateRuntimePolicyKind(%q, %q) error = %v, wantErr %v", test.runtimeKind, test.policyKind, err, test.wantErr)
			}
		})
	}
}

func TestSignatureCanonicalKeyIDAndExactTrustValidator(t *testing.T) {
	official := OfficialSignerIdentity()
	if official.KeyID != OfficialSignatureKeyID || len(official.PublicKey) != ed25519.PublicKeySize {
		t.Fatalf("official signer identity = %+v", official)
	}
	if got := hex.EncodeToString(official.PublicKey); got != "9edfaf2a05f9eb3aeff6e9c68f587f8c330a497eadd6f80d4dacb21eb9ff47ce" {
		t.Fatalf("official signer public key = %s", got)
	}
	official.PublicKey[0] ^= 0xff
	if bytes.Equal(official.PublicKey, OfficialSignerIdentity().PublicKey) {
		t.Fatal("official signer identity returned mutable shared key storage")
	}
	forgedOfficial := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{0x7f}, ed25519.SeedSize)).Public().(ed25519.PublicKey)
	validatorWithForgery := NewValidator(ValidatorOptions{TrustedSigners: map[string]ed25519.PublicKey{OfficialSignatureKeyID: forgedOfficial}})
	if bytes.Equal(validatorWithForgery.trustedSigners[OfficialSignatureKeyID], forgedOfficial) {
		t.Fatal("caller replaced the canonical official signing root")
	}

	for _, value := range []string{"release-key", "release.key-2026", OfficialSignatureKeyID} {
		if err := ValidateSignerKeyID(value); err != nil {
			t.Errorf("ValidateSignerKeyID(%q): %v", value, err)
		}
	}
	for _, value := range []string{"", "Release_Key", "release_key", "1release", " release-key", "release-key ", "release..key", strings.Repeat("a", MaxPermissionNameBytes+1)} {
		if err := ValidateSignerKeyID(value); err == nil {
			t.Errorf("ValidateSignerKeyID(%q) unexpectedly succeeded", value)
		}
	}

	oldKey := testSigningKey().Public().(ed25519.PublicKey)
	newKey := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{0x2a}, ed25519.SeedSize)).Public().(ed25519.PublicKey)
	base := NewValidator(ValidatorOptions{
		HostVersion: "1.2.3", AgentVersion: "2.3.4", TargetGOOS: "linux", TargetGOARCH: "amd64",
		MaxFiles: 17, MaxPackageBytes: 18, MaxFileBytes: 19, MaxMarketFiles: 20, MaxMarketBytes: 21, MaxMarketPackages: 22,
		AllowedPermissions: []string{"agent.read"}, AllowedExtensionPoints: []string{"dns.provider"}, SupportedABIs: []string{pluginsdk.RPCABIV1},
		TrustedSigners: map[string]ed25519.PublicKey{"old-key": oldKey},
	})
	clone := base.WithTrustedSigners(map[string]ed25519.PublicKey{"package-key": newKey}, TrustedSignerPolicyExact)
	if clone.options.HostVersion != "1.2.3" || clone.options.AgentVersion != "2.3.4" || clone.options.TargetGOOS != "linux" || clone.options.TargetGOARCH != "amd64" || clone.options.MaxFiles != 17 || clone.options.MaxPackageBytes != 18 || clone.options.MaxFileBytes != 19 || clone.options.MaxMarketFiles != 20 || clone.options.MaxMarketBytes != 21 || clone.options.MaxMarketPackages != 22 {
		t.Fatalf("clone did not preserve validator options: %+v", clone.options)
	}
	if clone.options.TrustedSignerPolicy != TrustedSignerPolicyExact {
		t.Fatalf("clone trust policy = %v", clone.options.TrustedSignerPolicy)
	}
	if _, ok := clone.trustedSigners[OfficialSignatureKeyID]; ok {
		t.Fatal("exact-trust clone inherited the built-in official root")
	}
	if _, ok := clone.trustedSigners["old-key"]; ok {
		t.Fatal("exact-trust clone retained the replaced custom signer")
	}
	if got := clone.trustedSigners["package-key"]; !got.Equal(newKey) {
		t.Fatal("exact-trust clone did not retain the package-bound signer")
	}
	if _, ok := base.trustedSigners[OfficialSignatureKeyID]; !ok {
		t.Fatal("base official validator lost its built-in official root")
	}
	officialExact := base.WithTrustedSigners(DefaultTrustedSigners(), TrustedSignerPolicyExact)
	if _, ok := officialExact.trustedSigners[OfficialSignatureKeyID]; !ok {
		t.Fatal("exact official-source clone rejected the immutable built-in key")
	}
	wrongOfficial := base.WithTrustedSigners(map[string]ed25519.PublicKey{OfficialSignatureKeyID: newKey}, TrustedSignerPolicyExact)
	if _, ok := wrongOfficial.trustedSigners[OfficialSignatureKeyID]; ok {
		t.Fatal("exact clone allowed the built-in official root to be overridden")
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

func replaceWASMFixtureBytes(t *testing.T, fixture, old, replacement []byte) []byte {
	t.Helper()
	if bytes.Count(fixture, old) != 1 {
		t.Fatalf("fixture marker %x is not unique", old)
	}
	return bytes.Replace(fixture, old, replacement, 1)
}

func replaceFirstWASMFunctionBody(t *testing.T, fixture, body []byte) []byte {
	t.Helper()
	for offset := pluginsdk.WASMModuleV1HeaderSize; offset < len(fixture); {
		sectionStart := offset
		sectionID := fixture[offset]
		offset++
		sectionSize, payloadStart := decodeTestULEB128(t, fixture, offset)
		sectionEnd := payloadStart + int(sectionSize)
		if sectionEnd > len(fixture) {
			t.Fatal("fixture section exceeds input")
		}
		if sectionID != 10 {
			offset = sectionEnd
			continue
		}
		count, bodySizeStart := decodeTestULEB128(t, fixture, payloadStart)
		if count == 0 {
			t.Fatal("fixture has no function bodies")
		}
		bodySize, bodyStart := decodeTestULEB128(t, fixture, bodySizeStart)
		bodyEnd := bodyStart + int(bodySize)
		if bodyEnd > sectionEnd {
			t.Fatal("fixture function body exceeds code section")
		}
		payload := append([]byte(nil), fixture[payloadStart:bodySizeStart]...)
		payload = append(payload, encodeTestULEB128(uint32(len(body)))...)
		payload = append(payload, body...)
		payload = append(payload, fixture[bodyEnd:sectionEnd]...)
		result := append([]byte(nil), fixture[:sectionStart]...)
		result = append(result, sectionID)
		result = append(result, encodeTestULEB128(uint32(len(payload)))...)
		result = append(result, payload...)
		return append(result, fixture[sectionEnd:]...)
	}
	t.Fatal("fixture code section not found")
	return nil
}

func decodeTestULEB128(t *testing.T, data []byte, offset int) (uint32, int) {
	t.Helper()
	var value uint32
	for shift := uint(0); shift < 35; shift += 7 {
		if offset >= len(data) {
			t.Fatal("truncated LEB128")
		}
		current := data[offset]
		offset++
		value |= uint32(current&0x7f) << shift
		if current&0x80 == 0 {
			return value, offset
		}
	}
	t.Fatal("oversized LEB128")
	return 0, offset
}

func encodeTestULEB128(value uint32) []byte {
	var encoded []byte
	for {
		current := byte(value & 0x7f)
		value >>= 7
		if value != 0 {
			current |= 0x80
		}
		encoded = append(encoded, current)
		if value == 0 {
			return encoded
		}
	}
}

func TestRuntimeRPCArtifactPlatformMatrixValidator(t *testing.T) {
	root := newRPCPackageFixture(t)
	if _, err := newTestValidator(ValidatorOptions{TargetGOOS: "linux", TargetGOARCH: "amd64"}).ValidatePackage(root, PackageExpectation{}); err != nil {
		t.Fatal(err)
	}
	_, err := newTestValidator(ValidatorOptions{TargetGOOS: "windows", TargetGOARCH: "amd64"}).ValidatePackage(root, PackageExpectation{})
	assertValidationCode(t, err, "artifact")
}

func mutateELFEntryIntoZeroFillTail(t *testing.T, input []byte) []byte {
	t.Helper()
	data := append([]byte(nil), input...)
	if len(data) < 64 || !bytes.Equal(data[:4], []byte{0x7f, 'E', 'L', 'F'}) || data[4] != 2 || data[5] != 1 {
		t.Fatal("ELF fixture lacks a 64-bit little-endian header")
	}
	programOffset := binary.LittleEndian.Uint64(data[32:40])
	programSize := uint64(binary.LittleEndian.Uint16(data[54:56]))
	programCount := uint64(binary.LittleEndian.Uint16(data[56:58]))
	for index := uint64(0); index < programCount; index++ {
		offset := programOffset + index*programSize
		if programSize < 56 || offset > uint64(len(data)) || programSize > uint64(len(data))-offset {
			t.Fatal("ELF fixture has a truncated program header")
		}
		header := data[offset : offset+programSize]
		if binary.LittleEndian.Uint32(header[:4]) != 1 || binary.LittleEndian.Uint32(header[4:8])&1 == 0 {
			continue
		}
		virtualAddress := binary.LittleEndian.Uint64(header[16:24])
		fileSize := binary.LittleEndian.Uint64(header[32:40])
		if fileSize == 0 || virtualAddress > ^uint64(0)-fileSize-4096 {
			continue
		}
		binary.LittleEndian.PutUint64(header[40:48], fileSize+4096)
		binary.LittleEndian.PutUint64(data[24:32], virtualAddress+fileSize)
		return data
	}
	t.Fatal("ELF fixture has no executable file-backed load segment")
	return nil
}

func mutatePEEntryIntoZeroFillTail(t *testing.T, input []byte) []byte {
	t.Helper()
	data := append([]byte(nil), input...)
	if len(data) < 0x40 || !bytes.Equal(data[:2], []byte{'M', 'Z'}) {
		t.Fatal("PE fixture lacks a DOS header")
	}
	peOffset := uint64(binary.LittleEndian.Uint32(data[0x3c:0x40]))
	if peOffset > uint64(len(data)) || uint64(len(data))-peOffset < 24 || !bytes.Equal(data[peOffset:peOffset+4], []byte{'P', 'E', 0, 0}) {
		t.Fatal("PE fixture lacks a complete COFF header")
	}
	sectionCount := uint64(binary.LittleEndian.Uint16(data[peOffset+6 : peOffset+8]))
	optionalSize := uint64(binary.LittleEndian.Uint16(data[peOffset+20 : peOffset+22]))
	optionalOffset := peOffset + 24
	sectionOffset := optionalOffset + optionalSize
	if optionalSize < 20 || sectionOffset > uint64(len(data)) {
		t.Fatal("PE fixture lacks a complete optional header")
	}
	for index := uint64(0); index < sectionCount; index++ {
		offset := sectionOffset + index*40
		if offset > uint64(len(data)) || uint64(len(data))-offset < 40 {
			t.Fatal("PE fixture has a truncated section header")
		}
		header := data[offset : offset+40]
		if binary.LittleEndian.Uint32(header[36:40])&0x20000000 == 0 {
			continue
		}
		rawSize := binary.LittleEndian.Uint32(header[16:20])
		virtualAddress := binary.LittleEndian.Uint32(header[12:16])
		if rawSize == 0 || virtualAddress > ^uint32(0)-rawSize-4096 {
			continue
		}
		binary.LittleEndian.PutUint32(header[8:12], rawSize+4096)
		binary.LittleEndian.PutUint32(data[optionalOffset+16:optionalOffset+20], virtualAddress+rawSize)
		return data
	}
	t.Fatal("PE fixture has no executable file-backed section")
	return nil
}

func mutateMachOEntryCommand(t *testing.T, input []byte, mutate func(uint32, []byte)) []byte {
	t.Helper()
	data := append([]byte(nil), input...)
	if len(data) < 32 {
		t.Fatal("Mach-O fixture lacks a 64-bit header")
	}
	commands := binary.LittleEndian.Uint32(data[16:20])
	offset := 32
	for index := uint32(0); index < commands; index++ {
		if offset+8 > len(data) {
			t.Fatal("Mach-O fixture load command is truncated")
		}
		size := int(binary.LittleEndian.Uint32(data[offset+4 : offset+8]))
		if size < 8 || offset+size > len(data) {
			t.Fatal("Mach-O fixture load command has an invalid size")
		}
		kind := binary.LittleEndian.Uint32(data[offset : offset+4])
		if kind == machOLoadMain || kind == uint32(macho.LoadCmdUnixThread) {
			mutate(kind, data[offset:offset+size])
			return data
		}
		offset += size
	}
	t.Fatal("Mach-O fixture has no LC_MAIN or LC_UNIXTHREAD command")
	return nil
}

func assertRPCArtifactBytesRejected(t *testing.T, data []byte, artifact Artifact) {
	t.Helper()
	name := filepath.Join(t.TempDir(), "invalid")
	if err := os.WriteFile(name, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validateRPCExecutable(name, artifact); err == nil {
		t.Fatal("non-executable RPC artifact was accepted")
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

func newPackageFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeFixture(t, root, PackageManifestFile, validManifestYAML(ConfigSchemaFile))
	writeFixture(t, root, ConfigSchemaFile, `{"type":"object","properties":{"mode":{"type":"string"}},"additionalProperties":false}`)
	writeFixtureBytes(t, root, "artifacts/policy.wasm", testWASMArtifact())
	refreshFixtureDigest(t, root)
	return root
}

func newSignedMarketFixture(t *testing.T, packageCapabilities, marketCapabilities []string) string {
	t.Helper()
	packageRoot := newPackageFixture(t)
	manifest := strings.Replace(
		validManifestYAML(ConfigSchemaFile),
		"extension_points: [http.request]",
		"extension_points: ["+strings.Join(packageCapabilities, ", ")+"]",
		1,
	)
	writeFixture(t, packageRoot, PackageManifestFile, manifest)
	refreshFixtureDigest(t, packageRoot)
	digestData, err := os.ReadFile(filepath.Join(packageRoot, PackageDigestFile))
	if err != nil {
		t.Fatal(err)
	}

	marketRoot := t.TempDir()
	packagePath := filepath.Join("plugins", "official.waf", "1.0.0")
	target := filepath.Join(marketRoot, packagePath)
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(packageRoot, target); err != nil {
		t.Fatal(err)
	}
	artifact := testWASMArtifact()
	artifactDigest := sha256.Sum256(artifact)
	market := fmt.Sprintf(`schema_version: 1
name: Custom
plugins:
  - id: official.waf
    version: 1.0.0
    capabilities: [%s]
    compatibility: {host: ">=1.0.0 <2.0.0", agent: ">=1.0.0 <2.0.0"}
    runtime: {kind: wasm-policy, abi: "nre:policy/v1", host_scope: agent, policy_kind: waf}
    artifacts: [{sha256: %x, size: %d}]
    package: %s
    sha256: %s
    signature_key_id: test-fixture
    provenance: custom
    official: false
`, strings.Join(marketCapabilities, ", "), artifactDigest, len(artifact), filepath.ToSlash(packagePath), strings.TrimSpace(string(digestData)))
	writeFixture(t, marketRoot, MarketManifestFile, market)
	return marketRoot
}

func newRPCPackageFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	_, artifact := buildTestRPCExecutable(t, "linux", "amd64")
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

type testRPCExecutableFixture struct {
	once sync.Once
	data []byte
	err  error
}

var testRPCExecutableFixtures sync.Map

// buildTestRPCExecutable uses the active Go toolchain once per target. Each
// caller still gets an isolated file so mutation tests cannot share state.
func buildTestRPCExecutable(t *testing.T, goos, goarch string) (string, []byte) {
	t.Helper()
	key := goos + "/" + goarch
	value, _ := testRPCExecutableFixtures.LoadOrStore(key, &testRPCExecutableFixture{})
	fixture := value.(*testRPCExecutableFixture)
	fixture.once.Do(func() {
		root, err := os.MkdirTemp("", "nre-rpc-fixture-")
		if err != nil {
			fixture.err = err
			return
		}
		defer os.RemoveAll(root)
		source := filepath.Join(root, "main.go")
		if err := os.WriteFile(source, []byte("package main\nfunc main() {}\n"), 0o600); err != nil {
			fixture.err = err
			return
		}
		output := filepath.Join(root, "plugin")
		if goos == "windows" {
			output += ".exe"
		}
		command := exec.Command("go", "build", "-trimpath", "-ldflags=-s -w", "-o", output, source)
		command.Env = append(os.Environ(), "GOOS="+goos, "GOARCH="+goarch, "CGO_ENABLED=0")
		if buildOutput, err := command.CombinedOutput(); err != nil {
			fixture.err = fmt.Errorf("build real %s/%s RPC fixture: %w: %s", goos, goarch, err, buildOutput)
			return
		}
		fixture.data, fixture.err = os.ReadFile(output)
	})
	if fixture.err != nil {
		t.Fatal(fixture.err)
	}

	root := t.TempDir()
	output := filepath.Join(root, "plugin")
	if goos == "windows" {
		output += ".exe"
	}
	if err := os.WriteFile(output, fixture.data, 0o700); err != nil {
		t.Fatal(err)
	}
	return output, append([]byte(nil), fixture.data...)
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
  policy_kind: waf
artifacts:
  - path: artifacts/policy.wasm
    sha256: %x
    size: %d
    mode: wasm
extension_points: [http.request]
permissions: [http.inspect]
config_schema: %s
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

func testWASMArtifact() []byte {
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
