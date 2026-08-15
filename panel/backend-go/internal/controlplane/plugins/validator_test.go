package plugins

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"debug/macho"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
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

func TestDirectPluginRepositoryProjectsRootManifestWithoutMarketEntry(t *testing.T) {
	root := newPackageFixture(t)
	validated, err := newTestValidator(ValidatorOptions{HostVersion: "1.2.0", AgentVersion: "1.3.0"}).ValidateDirectPlugin(root, false)
	if err != nil {
		t.Fatal(err)
	}
	projection := validated.Projection
	if projection.ID != "official.waf" || projection.PackageSHA256 != validated.Package.Digest || projection.Provenance != "custom" {
		t.Fatalf("direct projection = %+v", projection)
	}
}

func TestMarketEntryStrictlyRejectsRepositoryAndRefFields(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, MarketManifestFile, "schema_version: 1\nname: Test\nplugins:\n  - id: example.plugin\n    version: 1.0.0\n    repository: https://example.com/plugin.git\n    ref: main\n    package: plugin\n    sha256: "+strings.Repeat("a", 64)+"\n")
	if _, err := newTestValidator(ValidatorOptions{}).ValidateMarket(root, false); err == nil || !strings.Contains(err.Error(), "field repository") {
		t.Fatalf("market entry repository/ref error = %v", err)
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

func TestWASMPolicyABIValidator(t *testing.T) {
	fixture := testWASMArtifact()
	assertArtifact := func(t *testing.T, artifact []byte, wantError string) {
		t.Helper()
		name := filepath.Join(t.TempDir(), "policy.wasm")
		if err := os.WriteFile(name, artifact, 0o600); err != nil {
			t.Fatal(err)
		}
		err := validatePolicyWASMArtifact(name, 16*int64(wasmPageSizeBytes))
		if wantError == "" {
			if err != nil {
				t.Fatal(err)
			}
			return
		}
		if err == nil || !strings.Contains(err.Error(), wantError) {
			t.Fatalf("expected error containing %q, got %v", wantError, err)
		}
	}

	t.Run("compatible golden module", func(t *testing.T) {
		assertArtifact(t, fixture, "")
	})
	t.Run("wrong ABI major", func(t *testing.T) {
		artifact := replaceWASMFixtureBytes(t, fixture,
			[]byte{0x04, 0x00, 0x41, 0x01, 0x0b},
			[]byte{0x04, 0x00, 0x41, 0x02, 0x0b},
		)
		assertArtifact(t, artifact, `"nre_policy_version" declares ABI major 2, want 1`)
	})
	t.Run("non-terminating ABI major", func(t *testing.T) {
		artifact := replaceFirstWASMFunctionBody(t, fixture, []byte{0x00, 0x03, 0x40, 0x0c, 0x00, 0x0b, 0x00, 0x0b})
		assertArtifact(t, artifact, `must use the canonical static ABI major declaration`)
	})
	t.Run("header-only empty module", func(t *testing.T) {
		assertArtifact(t, fixture[:8], "module is empty")
	})
	t.Run("missing export", func(t *testing.T) {
		artifact := bytes.Replace(fixture, []byte("nre_policy_reset"), []byte("nre_policy_resXX"), 1)
		assertArtifact(t, artifact, `required function export "nre_policy_reset" is missing`)
	})
	t.Run("wrong export signature", func(t *testing.T) {
		artifact := append([]byte(nil), fixture...)
		name := []byte(pluginsdk.PolicyExportEvaluate)
		index := bytes.Index(artifact, name)
		if index < 0 {
			t.Fatal("evaluate export not found in fixture")
		}
		exportDescriptor := index + len(name)
		if exportDescriptor+2 > len(artifact) || artifact[exportDescriptor] != 0x00 || artifact[exportDescriptor+1] != 0x0a {
			t.Fatal("evaluate export descriptor is not canonical")
		}
		artifact[exportDescriptor+1] = 0x09
		assertArtifact(t, artifact, `function export "nre_policy_evaluate" has the wrong signature`)
	})
	t.Run("wrong host import signature", func(t *testing.T) {
		artifact := append([]byte(nil), fixture...)
		name := []byte(pluginsdk.PolicyHostStateGet)
		index := bytes.Index(artifact, name)
		if index < 0 {
			t.Fatal("host import not found in fixture")
		}
		importDescriptor := index + len(name)
		if importDescriptor+2 > len(artifact) || artifact[importDescriptor] != 0x00 || artifact[importDescriptor+1] != 0x00 {
			t.Fatal("host import descriptor is not canonical")
		}
		artifact[importDescriptor+1] = 0x01
		assertArtifact(t, artifact, `host import "nre_host_state_get" has the wrong function signature`)
	})
	t.Run("dangerous import", func(t *testing.T) {
		artifact := bytes.Replace(fixture, []byte(pluginsdk.PolicyHostModule), []byte("wasi:policy/1"), 1)
		assertArtifact(t, artifact, `dangerous import "wasi:policy/1"`)
	})
	t.Run("invalid opcode", func(t *testing.T) {
		artifact := replaceWASMFixtureBytes(t, fixture,
			[]byte{0x04, 0x00, 0x41, 0x01, 0x0b},
			[]byte{0x04, 0x00, 0xff, 0x01, 0x0b},
		)
		assertArtifact(t, artifact, "invalid WebAssembly module")
	})
	t.Run("operand stack underflow", func(t *testing.T) {
		artifact := replaceWASMFixtureBytes(t, fixture,
			[]byte{0x04, 0x00, 0x41, 0x01, 0x0b},
			[]byte{0x04, 0x00, 0x6a, 0x01, 0x0b},
		)
		assertArtifact(t, artifact, "invalid WebAssembly module")
	})
	t.Run("function reference out of range", func(t *testing.T) {
		artifact := replaceWASMFixtureBytes(t, fixture,
			[]byte{0x04, 0x00, 0x41, 0x01, 0x0b},
			[]byte{0x04, 0x00, 0x10, 0x7f, 0x0b},
		)
		assertArtifact(t, artifact, "invalid WebAssembly module")
	})
	t.Run("control label out of range", func(t *testing.T) {
		artifact := replaceWASMFixtureBytes(t, fixture,
			[]byte{0x04, 0x00, 0x41, 0x01, 0x0b},
			[]byte{0x04, 0x00, 0x0c, 0x01, 0x0b},
		)
		assertArtifact(t, artifact, "invalid WebAssembly module")
	})
	t.Run("memory maximum is required", func(t *testing.T) {
		artifact := replaceWASMFixtureBytes(t, fixture,
			[]byte{0x05, 0x04, 0x01, 0x01, 0x01, 0x10},
			[]byte{0x05, 0x03, 0x01, 0x00, 0x01},
		)
		assertArtifact(t, artifact, "must declare an explicit maximum")
	})
	t.Run("memory maximum exceeds manifest budget", func(t *testing.T) {
		name := filepath.Join(t.TempDir(), "policy.wasm")
		if err := os.WriteFile(name, fixture, 0o600); err != nil {
			t.Fatal(err)
		}
		err := validatePolicyWASMArtifact(name, 15*int64(wasmPageSizeBytes))
		if err == nil || !strings.Contains(err.Error(), "exceeds manifest resource budget") {
			t.Fatalf("expected memory budget error, got %v", err)
		}
	})
	t.Run("memory maximum above policy ceiling is rejected", func(t *testing.T) {
		artifact := replaceWASMFixtureBytes(t, fixture,
			[]byte{0x05, 0x04, 0x01, 0x01, 0x01, 0x10},
			[]byte{0x05, 0x05, 0x01, 0x01, 0x01, 0x80, 0x04},
		)
		name := filepath.Join(t.TempDir(), "policy.wasm")
		if err := os.WriteFile(name, artifact, 0o600); err != nil {
			t.Fatal(err)
		}
		err := validatePolicyWASMArtifact(name, pluginsdk.PolicyV1MaxMemoryBytes)
		if err == nil || !strings.Contains(err.Error(), "exceeds manifest resource budget") {
			t.Fatalf("high-maximum module error = %v", err)
		}
	})
	t.Run("four GiB declared maximum is rejected without instantiation", func(t *testing.T) {
		artifact := replaceWASMFixtureBytes(t, fixture,
			[]byte{0x05, 0x04, 0x01, 0x01, 0x01, 0x10},
			[]byte{0x05, 0x06, 0x01, 0x01, 0x01, 0x80, 0x80, 0x04},
		)
		name := filepath.Join(t.TempDir(), "policy.wasm")
		if err := os.WriteFile(name, artifact, 0o600); err != nil {
			t.Fatal(err)
		}
		err := validatePolicyWASMArtifact(name, pluginsdk.PolicyV1MaxMemoryBytes)
		if err == nil || !strings.Contains(err.Error(), "exceeds manifest resource budget") {
			t.Fatalf("4 GiB maximum error = %v", err)
		}
	})
	t.Run("version memory grow is rejected statically", func(t *testing.T) {
		artifact := replaceFirstWASMFunctionBody(t, fixture, []byte{0x00, 0x41, 0xff, 0xff, 0x03, 0x40, 0x00, 0x1a, 0x41, 0x01, 0x0b})
		name := filepath.Join(t.TempDir(), "memory-grow-version.wasm")
		if err := os.WriteFile(name, artifact, 0o600); err != nil {
			t.Fatal(err)
		}
		err := validatePolicyWASMArtifact(name, 16*int64(wasmPageSizeBytes))
		if err == nil || !strings.Contains(err.Error(), "canonical static ABI major declaration") {
			t.Fatalf("version memory.grow rejection = %v", err)
		}
	})
	t.Run("oversized initial memory is rejected before ABI instantiation", func(t *testing.T) {
		artifact := replaceWASMFixtureBytes(t, fixture,
			[]byte{0x05, 0x04, 0x01, 0x01, 0x01, 0x10},
			[]byte{0x05, 0x06, 0x01, 0x01, 0x81, 0x02, 0x81, 0x02},
		)
		name := filepath.Join(t.TempDir(), "policy.wasm")
		if err := os.WriteFile(name, artifact, 0o600); err != nil {
			t.Fatal(err)
		}
		err := validatePolicyWASMArtifact(name, pluginsdk.PolicyV1MaxMemoryBytes)
		if err == nil || !strings.Contains(err.Error(), "initial memory") || !strings.Contains(err.Error(), "ABI validation ceiling") {
			t.Fatalf("initial memory ceiling error = %v", err)
		}
	})
	t.Run("truncated artifact", func(t *testing.T) {
		assertArtifact(t, fixture[:len(fixture)-1], "unexpected")
	})
}

func TestWASMMemoryBudgetUsesManifestAndChecksPageOverflow(t *testing.T) {
	root := newPackageFixture(t)
	manifestName := filepath.Join(root, PackageManifestFile)
	manifestData, err := os.ReadFile(manifestName)
	if err != nil {
		t.Fatal(err)
	}
	manifest := strings.Replace(string(manifestData), "memory_bytes: 1048576", "memory_bytes: 983040", 1)
	writeFixture(t, root, PackageManifestFile, manifest)
	refreshFixtureDigest(t, root)
	_, err = newTestValidator(ValidatorOptions{}).ValidatePackage(root, PackageExpectation{})
	assertValidationCode(t, err, "artifact_format")
	if !strings.Contains(err.Error(), "exceeds manifest resource budget") {
		t.Fatalf("manifest memory budget error = %v", err)
	}

	if _, err := wasmPagesToBytes(^uint64(0)); err == nil || !strings.Contains(err.Error(), "overflows") {
		t.Fatalf("page conversion overflow error = %v", err)
	}
}

func TestWASMPolicyResourceBudgetCeilingsAndProjection(t *testing.T) {
	maximum := ResourceBudget{
		TimeoutMS:   pluginsdk.PolicyV1MaxTimeoutMilliseconds,
		MemoryBytes: pluginsdk.PolicyV1MaxMemoryBytes,
		Concurrency: pluginsdk.PolicyV1MaxConcurrency,
		InputBytes:  pluginsdk.PolicyV1MaxInputFrameBytes,
		OutputBytes: pluginsdk.PolicyV1MaxOutputFrameBytes,
	}
	if err := validateResourceBudget(pluginsdk.RuntimeWASMPolicy, maximum); err != nil {
		t.Fatalf("policy maximum budget rejected: %v", err)
	}
	for _, test := range []struct {
		name   string
		mutate func(*ResourceBudget)
	}{
		{"timeout", func(b *ResourceBudget) { b.TimeoutMS++ }},
		{"memory", func(b *ResourceBudget) { b.MemoryBytes++ }},
		{"concurrency", func(b *ResourceBudget) { b.Concurrency++ }},
		{"input frame", func(b *ResourceBudget) { b.InputBytes++ }},
		{"output frame", func(b *ResourceBudget) { b.OutputBytes++ }},
	} {
		t.Run(test.name, func(t *testing.T) {
			invalid := maximum
			test.mutate(&invalid)
			if err := validateResourceBudget(pluginsdk.RuntimeWASMPolicy, invalid); err == nil {
				t.Fatal("control-plane accepted a budget above the canonical Agent ceiling")
			}
		})
	}

	root := newPackageFixture(t)
	manifest := validManifestYAML(ConfigSchemaFile)
	manifest = strings.Replace(manifest, "memory_bytes: 1048576", fmt.Sprintf("memory_bytes: %d", maximum.MemoryBytes), 1)
	manifest = strings.Replace(manifest, "concurrency: 8", fmt.Sprintf("concurrency: %d", maximum.Concurrency), 1)
	manifest = strings.Replace(manifest, "input_bytes: 65536", fmt.Sprintf("input_bytes: %d", maximum.InputBytes), 1)
	writeFixture(t, root, PackageManifestFile, manifest)
	refreshFixtureDigest(t, root)
	validated, err := newTestValidator(ValidatorOptions{}).ValidatePackage(root, PackageExpectation{})
	if err != nil {
		t.Fatalf("package at canonical policy ceilings rejected: %v", err)
	}
	projected := validated.Manifest.ResourceBudget
	if projected != maximum {
		t.Fatalf("validated policy budget projection = %+v, want %+v", projected, maximum)
	}
	if err := (pluginsdk.PolicyV1ResourceBudget{
		TimeoutMilliseconds: projected.TimeoutMS,
		MemoryBytes:         projected.MemoryBytes,
		Concurrency:         projected.Concurrency,
		InputFrameBytes:     projected.InputBytes,
		OutputFrameBytes:    projected.OutputBytes,
	}).Validate(); err != nil {
		t.Fatalf("control-plane accepted budget cannot prepare under shared Agent contract: %v", err)
	}

	rpcBudget := ResourceBudget{TimeoutMS: 300000, MemoryBytes: MaxRuntimeMemoryBytes, Concurrency: 4096, InputBytes: MaxRuntimeIOBytes, OutputBytes: MaxRuntimeIOBytes, CPUMillis: 100000, Restarts: 100}
	if err := validateResourceBudget(pluginsdk.RuntimeRPCService, rpcBudget); err != nil {
		t.Fatalf("independent RPC ceilings were narrowed with wasm-policy: %v", err)
	}
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

func TestHTTPBackendProviderManifestAdmission(t *testing.T) {
	providerManifest := func(t *testing.T) string {
		t.Helper()
		root := newRPCPackageFixture(t)
		path := filepath.Join(root, PackageManifestFile)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		manifest := strings.Replace(string(data),
			"extension_points: [dns.provider]\npermissions: [agent.read]",
			"extension_points: [http.backend-provider]\nhttp_backend_providers: [{id: default, display_name: Default}]\npermissions: [http.outbound]", 1)
		writeFixture(t, root, PackageManifestFile, manifest)
		refreshFixtureDigest(t, root)
		return root
	}

	if _, err := newTestValidator(ValidatorOptions{TargetGOOS: "linux", TargetGOARCH: "amd64"}).ValidatePackage(providerManifest(t), PackageExpectation{}); err != nil {
		t.Fatalf("complete HTTP backend provider manifest rejected: %v", err)
	}
	for _, test := range []struct {
		name    string
		id      string
		wantErr bool
	}{
		{name: "64 byte provider id", id: "a" + strings.Repeat("b", pluginsdk.ProviderIDMaxBytes-1)},
		{name: "65 byte provider id", id: "a" + strings.Repeat("b", pluginsdk.ProviderIDMaxBytes), wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := providerManifest(t)
			path := filepath.Join(root, PackageManifestFile)
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			manifest := strings.Replace(string(data), "id: default", "id: "+test.id, 1)
			writeFixture(t, root, PackageManifestFile, manifest)
			refreshFixtureDigest(t, root)
			_, err = newTestValidator(ValidatorOptions{TargetGOOS: "linux", TargetGOARCH: "amd64"}).ValidatePackage(root, PackageExpectation{})
			if (err != nil) != test.wantErr {
				t.Fatalf("ValidatePackage() error = %v, wantErr %v", err, test.wantErr)
			}
		})
	}
	t.Run("permission only", func(t *testing.T) {
		root := newRPCPackageFixture(t)
		path := filepath.Join(root, PackageManifestFile)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		writeFixture(t, root, PackageManifestFile, strings.Replace(string(data), "permissions: [agent.read]", "permissions: [http.outbound]", 1))
		refreshFixtureDigest(t, root)
		_, err = newTestValidator(ValidatorOptions{TargetGOOS: "linux", TargetGOARCH: "amd64"}).ValidatePackage(root, PackageExpectation{})
		assertValidationCode(t, err, "http_backend_provider")
	})

	for name, rewrite := range map[string]func(string) string{
		"missing descriptor": func(value string) string {
			return strings.Replace(value, "http_backend_providers: [{id: default, display_name: Default}]\n", "", 1)
		},
		"missing outbound permission": func(value string) string {
			return strings.Replace(value, "permissions: [http.outbound]", "permissions: [agent.read]", 1)
		},
		"wrong host scope": func(value string) string {
			return strings.Replace(value, "host_scope: agent", "host_scope: control-plane", 1)
		},
		"resource scoped outbound": func(value string) string {
			return strings.Replace(value, "permissions: [http.outbound]", "permissions: [{name: http.outbound, resource: tenant-a}]", 1)
		},
	} {
		t.Run(name, func(t *testing.T) {
			root := providerManifest(t)
			path := filepath.Join(root, PackageManifestFile)
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			writeFixture(t, root, PackageManifestFile, rewrite(string(data)))
			refreshFixtureDigest(t, root)
			_, err = newTestValidator(ValidatorOptions{TargetGOOS: "linux", TargetGOARCH: "amd64"}).ValidatePackage(root, PackageExpectation{})
			assertValidationCode(t, err, "http_backend_provider")
		})
	}

	for name, test := range map[string]struct {
		from string
		to   string
		code string
	}{
		"unknown extension":  {from: "http.backend-provider", to: "unknown.provider", code: "extension_point"},
		"unknown permission": {from: "http.outbound", to: "unknown.permission", code: "permission"},
	} {
		t.Run(name, func(t *testing.T) {
			root := providerManifest(t)
			path := filepath.Join(root, PackageManifestFile)
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			writeFixture(t, root, PackageManifestFile, strings.Replace(string(data), test.from, test.to, 1))
			refreshFixtureDigest(t, root)
			_, err = newTestValidator(ValidatorOptions{TargetGOOS: "linux", TargetGOARCH: "amd64"}).ValidatePackage(root, PackageExpectation{})
			assertValidationCode(t, err, test.code)
		})
	}
}

func TestRPCArtifactExecutableParserMatrix(t *testing.T) {
	type format struct {
		name   string
		goos   string
		goarch string
		mutate func([]byte)
		header int
	}
	formats := []format{
		{name: "ELF object", goos: "linux", goarch: "amd64", header: 64, mutate: func(data []byte) { binary.LittleEndian.PutUint16(data[16:18], 1) }},
		{name: "PE DLL", goos: "windows", goarch: "amd64", header: 512, mutate: func(data []byte) {
			offset := int(binary.LittleEndian.Uint32(data[0x3c:0x40]))
			characteristics := binary.LittleEndian.Uint16(data[offset+22 : offset+24])
			binary.LittleEndian.PutUint16(data[offset+22:offset+24], characteristics|0x2000)
		}},
		{name: "Mach-O dylib", goos: "darwin", goarch: "amd64", header: 32, mutate: func(data []byte) { binary.LittleEndian.PutUint32(data[12:16], 6) }},
	}
	for _, format := range formats {
		format := format
		t.Run(format.goos+"/real executable", func(t *testing.T) {
			name, data := buildTestRPCExecutable(t, format.goos, format.goarch)
			artifact := Artifact{GOOS: format.goos, GOARCH: format.goarch}
			if err := validateRPCExecutable(name, artifact); err != nil {
				t.Fatalf("real cross-compiled executable rejected: %v", err)
			}

			t.Run("wrong architecture", func(t *testing.T) {
				err := validateRPCExecutable(name, Artifact{GOOS: format.goos, GOARCH: "arm64"})
				if err == nil || !strings.Contains(err.Error(), "does not match declared GOARCH") {
					t.Fatalf("wrong architecture error = %v", err)
				}
			})
			if format.goos == "linux" {
				t.Run("wrong ELF operating system", func(t *testing.T) {
					err := validateRPCExecutable(name, Artifact{GOOS: "freebsd", GOARCH: format.goarch})
					if err == nil || !strings.Contains(err.Error(), "does not match declared GOOS") {
						t.Fatalf("wrong operating-system error = %v", err)
					}
				})
			}
			t.Run(format.name, func(t *testing.T) {
				invalid := append([]byte(nil), data...)
				format.mutate(invalid)
				invalidName := filepath.Join(t.TempDir(), "invalid")
				if err := os.WriteFile(invalidName, invalid, 0o600); err != nil {
					t.Fatal(err)
				}
				if err := validateRPCExecutable(invalidName, artifact); err == nil {
					t.Fatalf("%s was accepted", format.name)
				}
			})
			switch format.goos {
			case "linux":
				t.Run("ELF entry in zero-fill tail", func(t *testing.T) {
					assertRPCArtifactBytesRejected(t, mutateELFEntryIntoZeroFillTail(t, data), artifact)
				})
				t.Run("ELF shared object", func(t *testing.T) {
					invalid := append([]byte(nil), data...)
					binary.LittleEndian.PutUint16(invalid[16:18], 3)
					binary.LittleEndian.PutUint64(invalid[24:32], 0)
					assertRPCArtifactBytesRejected(t, invalid, artifact)
				})
			case "windows":
				t.Run("PE entry in zero-fill tail", func(t *testing.T) {
					assertRPCArtifactBytesRejected(t, mutatePEEntryIntoZeroFillTail(t, data), artifact)
				})
				t.Run("PE non-executable image", func(t *testing.T) {
					invalid := append([]byte(nil), data...)
					offset := int(binary.LittleEndian.Uint32(invalid[0x3c:0x40]))
					characteristics := binary.LittleEndian.Uint16(invalid[offset+22 : offset+24])
					binary.LittleEndian.PutUint16(invalid[offset+22:offset+24], characteristics & ^uint16(0x0002))
					assertRPCArtifactBytesRejected(t, invalid, artifact)
				})
				t.Run("PE system image", func(t *testing.T) {
					invalid := append([]byte(nil), data...)
					offset := int(binary.LittleEndian.Uint32(invalid[0x3c:0x40]))
					characteristics := binary.LittleEndian.Uint16(invalid[offset+22 : offset+24])
					binary.LittleEndian.PutUint16(invalid[offset+22:offset+24], characteristics|0x1000)
					assertRPCArtifactBytesRejected(t, invalid, artifact)
				})
				for name, subsystem := range map[string]uint16{"Native": 1, "Native Windows": 8, "EFI application": 10, "EFI driver": 11} {
					t.Run("PE "+name+" subsystem", func(t *testing.T) {
						invalid := append([]byte(nil), data...)
						offset := int(binary.LittleEndian.Uint32(invalid[0x3c:0x40]))
						optionalHeader := offset + 24
						binary.LittleEndian.PutUint16(invalid[optionalHeader+68:optionalHeader+70], subsystem)
						assertRPCArtifactBytesRejected(t, invalid, artifact)
					})
				}
			case "darwin":
				t.Run("Mach-O object", func(t *testing.T) {
					invalid := append([]byte(nil), data...)
					binary.LittleEndian.PutUint32(invalid[12:16], 1)
					assertRPCArtifactBytesRejected(t, invalid, artifact)
				})
				t.Run("Mach-O missing entry command", func(t *testing.T) {
					invalid := mutateMachOEntryCommand(t, data, func(_ uint32, command []byte) {
						binary.LittleEndian.PutUint32(command[:4], 0x77777777)
					})
					assertRPCArtifactBytesRejected(t, invalid, artifact)
				})
				t.Run("Mach-O entry outside file-backed executable segment", func(t *testing.T) {
					invalid := mutateMachOEntryCommand(t, data, func(kind uint32, command []byte) {
						switch kind {
						case machOLoadMain:
							binary.LittleEndian.PutUint64(command[8:16], uint64(len(data)+4096))
						case uint32(macho.LoadCmdUnixThread):
							registerOffset := 16 + machOX86ThreadRIP*8
							binary.LittleEndian.PutUint64(command[registerOffset:registerOffset+8], ^uint64(0))
						}
					})
					assertRPCArtifactBytesRejected(t, invalid, artifact)
				})
			}
			t.Run("header only", func(t *testing.T) {
				invalidName := filepath.Join(t.TempDir(), "header-only")
				if err := os.WriteFile(invalidName, data[:format.header], 0o600); err != nil {
					t.Fatal(err)
				}
				if err := validateRPCExecutable(invalidName, artifact); err == nil {
					t.Fatal("header-only executable was accepted")
				}
			})
			t.Run("truncated", func(t *testing.T) {
				invalidName := filepath.Join(t.TempDir(), "truncated")
				if err := os.WriteFile(invalidName, data[:len(data)/2], 0o600); err != nil {
					t.Fatal(err)
				}
				if err := validateRPCExecutable(invalidName, artifact); err == nil {
					t.Fatal("truncated executable was accepted")
				}
			})
		})
	}
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

func TestRuntimeHardlinkIdentityUsesLinearStableKeys(t *testing.T) {
	root := t.TempDir()
	original := filepath.Join(root, "original")
	linked := filepath.Join(root, "linked")
	other := filepath.Join(root, "other")
	if err := os.WriteFile(original, []byte("same inode"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(original, linked); err != nil {
		t.Skipf("hardlinks are unavailable on this filesystem: %v", err)
	}
	if err := os.WriteFile(other, []byte("other inode"), 0o600); err != nil {
		t.Fatal(err)
	}
	originalInfo, err := os.Stat(original)
	if err != nil {
		t.Fatal(err)
	}
	linkedInfo, err := os.Stat(linked)
	if err != nil {
		t.Fatal(err)
	}
	otherInfo, err := os.Stat(other)
	if err != nil {
		t.Fatal(err)
	}
	originalKey, err := stableRegularFileKey(original, originalInfo)
	if err != nil {
		t.Fatal(err)
	}
	linkedKey, err := stableRegularFileKey(linked, linkedInfo)
	if err != nil {
		t.Fatal(err)
	}
	otherKey, err := stableRegularFileKey(other, otherInfo)
	if err != nil {
		t.Fatal(err)
	}
	if originalKey != linkedKey || originalKey == otherKey {
		t.Fatalf("unstable file identities: original=%+v linked=%+v other=%+v", originalKey, linkedKey, otherKey)
	}

	seen := newStableFileSet(DefaultMaxMarketFiles)
	for index := 0; index < DefaultMaxMarketFiles; index++ {
		key := stableFileKey{volume: 1, high: uint64(index >> 8), low: uint64(index)}
		if !seen.add(key) {
			t.Fatalf("identity %d unexpectedly collided", index)
		}
	}
	if len(seen) != DefaultMaxMarketFiles {
		t.Fatalf("identity set size = %d, want %d", len(seen), DefaultMaxMarketFiles)
	}
}

func TestRuntimePackageAndMarketRejectPlatformHardlinks(t *testing.T) {
	t.Run("package", func(t *testing.T) {
		root := newPackageFixture(t)
		if err := os.Link(filepath.Join(root, ConfigSchemaFile), filepath.Join(root, "linked.schema.json")); err != nil {
			t.Skipf("hardlinks are unavailable on this filesystem: %v", err)
		}
		_, err := newTestValidator(ValidatorOptions{}).ValidatePackage(root, PackageExpectation{})
		assertValidationCode(t, err, "hardlink")
	})
	t.Run("market", func(t *testing.T) {
		root := t.TempDir()
		packageRoot := filepath.Join(root, "plugins", "example", "1.0.0")
		if err := os.MkdirAll(packageRoot, 0o755); err != nil {
			t.Fatal(err)
		}
		writeFixture(t, root, MarketManifestFile, "schema_version: 1\nname: test\nplugins: []\n")
		original := filepath.Join(packageRoot, "original")
		if err := os.WriteFile(original, []byte("hardlink"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Link(original, filepath.Join(packageRoot, "linked")); err != nil {
			t.Skipf("hardlinks are unavailable on this filesystem: %v", err)
		}
		manifest := MarketManifest{Entries: []MarketEntry{{PackagePath: "plugins/example/1.0.0"}}}
		err := inspectMarketTree(root, manifest, NewValidator(ValidatorOptions{}).options)
		assertValidationCode(t, err, "hardlink")
	})
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

func TestRuntimeMarketSignatureAndCapabilityContract(t *testing.T) {
	baseEntry := `  - id: official.example
    version: 1.0.0
    capabilities: [http.request]
    compatibility: {host: "*", agent: "*"}
    runtime: {kind: wasm-policy, abi: "nre:policy/v1", host_scope: agent, policy_kind: waf}
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
	assertValidationCode(t, err, "market_schema")

	root = t.TempDir()
	entryWithoutCapability := strings.Replace(baseEntry, "    capabilities: [http.request]\n", "", 1)
	entryWithoutCapability = strings.Replace(entryWithoutCapability, "    provenance: sakullla-plugins", "    provenance: custom", 1)
	entryWithoutCapability = strings.Replace(entryWithoutCapability, "    official: true", "    official: false", 1)
	writeFixture(t, root, MarketManifestFile, "schema_version: 1\nname: Custom\nplugins:\n"+entryWithoutCapability)
	_, err = newTestValidator(ValidatorOptions{}).ValidateMarket(root, false)
	assertValidationCode(t, err, "market_entry")
}

func TestRuntimeMarketCapabilitiesMatchSignedManifest(t *testing.T) {
	t.Run("matching set is projected in signed manifest order", func(t *testing.T) {
		root := newSignedMarketFixture(t, []string{"http.request", "dns.provider"}, []string{"dns.provider", "http.request"})
		validated, err := newTestValidator(ValidatorOptions{}).ValidateMarket(root, false)
		if err != nil {
			t.Fatal(err)
		}
		got := validated.Manifest.Entries[0].Capabilities
		if len(got) != 2 || got[0] != "http.request" || got[1] != "dns.provider" {
			t.Fatalf("canonical capabilities = %v", got)
		}
	})
	for _, test := range []struct {
		name                string
		packageCapabilities []string
		marketCapabilities  []string
	}{
		{name: "market advertises unsigned capability", packageCapabilities: []string{"http.request"}, marketCapabilities: []string{"http.request", "dns.provider"}},
		{name: "market hides signed capability", packageCapabilities: []string{"http.request", "dns.provider"}, marketCapabilities: []string{"http.request"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := newSignedMarketFixture(t, test.packageCapabilities, test.marketCapabilities)
			validated, err := newTestValidator(ValidatorOptions{}).ValidateMarket(root, false)
			assertValidationCode(t, err, "capability_mismatch")
			if len(validated.Packages) != 0 || len(validated.Manifest.Entries) != 0 {
				t.Fatal("capability mismatch returned a candidate market that could replace current state")
			}
		})
	}
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

func TestValidatorRejectsNonCanonicalPermissionAndCapabilityWhitespace(t *testing.T) {
	tests := []struct {
		name        string
		replaceFrom string
		replaceTo   string
		code        string
	}{
		{name: "permission name", replaceFrom: "permissions: [http.inspect]", replaceTo: "permissions: [{name: ' http.inspect ', resource: tenant-a}]", code: "permission"},
		{name: "scalar permission name", replaceFrom: "permissions: [http.inspect]", replaceTo: "permissions: [' http.inspect ']", code: "permission"},
		{name: "permission resource", replaceFrom: "permissions: [http.inspect]", replaceTo: "permissions: [{name: http.inspect, resource: ' tenant-a '}]", code: "permission"},
		{name: "extension point", replaceFrom: "extension_points: [http.request]", replaceTo: "extension_points: [' http.request ']", code: "extension_point"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := newPackageFixture(t)
			manifest := strings.Replace(validManifestYAML(ConfigSchemaFile), test.replaceFrom, test.replaceTo, 1)
			writeFixture(t, root, PackageManifestFile, manifest)
			refreshFixtureDigest(t, root)
			_, err := newTestValidator(ValidatorOptions{}).ValidatePackage(root, PackageExpectation{})
			assertValidationCode(t, err, test.code)
		})
	}

	marketRoot := newSignedMarketFixture(t, []string{"http.request"}, []string{"' http.request '"})
	_, err := newTestValidator(ValidatorOptions{}).ValidateMarket(marketRoot, false)
	assertValidationCode(t, err, "market_entry")
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

func TestValidatorRejectsSignedSymlinkAndNonDirectoryPackageRoots(t *testing.T) {
	signedRoot := newPackageFixture(t)
	linkRoot := filepath.Join(t.TempDir(), "signed-package")
	if err := os.Symlink(signedRoot, linkRoot); err != nil {
		t.Logf("package-root symlink is unavailable on this filesystem: %v", err)
	} else {
		_, err := newTestValidator(ValidatorOptions{}).ValidatePackage(linkRoot, PackageExpectation{})
		assertValidationCode(t, err, "symlink")
		_, err = ComputePackageDigest(linkRoot)
		assertValidationCode(t, err, "symlink")
	}
	ancestorAlias := filepath.Join(t.TempDir(), "packages")
	if err := os.Symlink(filepath.Dir(signedRoot), ancestorAlias); err != nil {
		t.Logf("package ancestor symlink is unavailable on this filesystem: %v", err)
	} else {
		validated, err := newTestValidator(ValidatorOptions{}).ValidatePackage(filepath.Join(ancestorAlias, filepath.Base(signedRoot)), PackageExpectation{})
		if err != nil {
			t.Fatal(err)
		}
		resolvedRoot, err := filepath.EvalSymlinks(signedRoot)
		if err != nil {
			t.Fatal(err)
		}
		if validated.Root != filepath.Clean(resolvedRoot) {
			t.Fatalf("validated root = %q, want resolved anchor %q", validated.Root, filepath.Clean(resolvedRoot))
		}
	}

	fileRoot := filepath.Join(t.TempDir(), "package-file")
	if err := os.WriteFile(fileRoot, []byte("not a package directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := newTestValidator(ValidatorOptions{}).ValidatePackage(fileRoot, PackageExpectation{})
	assertValidationCode(t, err, "file_type")
	_, err = ComputePackageDigest(fileRoot)
	assertValidationCode(t, err, "file_type")
}

func TestValidatePackageSnapshotRejectsStageReplacementAndCleansUp(t *testing.T) {
	t.Run("single file after manifest", func(t *testing.T) {
		root := newPackageFixture(t)
		validator := newTestValidator(ValidatorOptions{})
		var snapshotRoot string
		replaced := false
		validator.snapshotHook = func(stage, sourceRoot, currentSnapshot string) {
			snapshotRoot = currentSnapshot
			if stage != "manifest" || replaced {
				return
			}
			replaced = true
			name := filepath.Join(sourceRoot, ConfigSchemaFile)
			backup := name + ".replaced"
			if err := os.Rename(name, backup); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(backup)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(name, data, 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(backup); err != nil {
				t.Fatal(err)
			}
		}
		_, err := validator.ValidatePackage(root, PackageExpectation{})
		assertValidationCode(t, err, "snapshot_changed")
		if !replaced {
			t.Fatal("deterministic manifest-stage replacement hook did not run")
		}
		if _, statErr := os.Stat(snapshotRoot); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("validation snapshot was not cleaned up: %v", statErr)
		}
	})

	t.Run("entire root after digest", func(t *testing.T) {
		root := newPackageFixture(t)
		replacement := newPackageFixture(t)
		validator := newTestValidator(ValidatorOptions{})
		replaced := false
		validator.snapshotHook = func(stage, sourceRoot, _ string) {
			if stage != "digest" || replaced {
				return
			}
			replaced = true
			backup := sourceRoot + ".replaced"
			if err := os.Rename(sourceRoot, backup); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(replacement, sourceRoot); err != nil {
				t.Fatal(err)
			}
		}
		_, err := validator.ValidatePackage(root, PackageExpectation{})
		if cleanupErr := os.RemoveAll(root); cleanupErr != nil {
			t.Fatal(cleanupErr)
		}
		if cleanupErr := os.Rename(root+".replaced", root); cleanupErr != nil {
			t.Fatal(cleanupErr)
		}
		assertValidationCode(t, err, "snapshot_changed")
		if !replaced {
			t.Fatal("deterministic digest-stage root replacement hook did not run")
		}
	})
}

func TestValidatorMarketSnapshotBindsDigestTreeAndAllPackages(t *testing.T) {
	for _, test := range []struct {
		name        string
		stage       string
		relative    string
		replaceRoot bool
	}{
		{name: "checkout root after snapshot", stage: "market_copied", replaceRoot: true},
		{name: "market manifest after digest", stage: "market_manifest", relative: MarketManifestFile},
		{name: "package tree after inspection", stage: "market_tree", relative: "plugins/official.waf/1.0.0/plugin.yaml"},
		{name: "package content during validation", stage: "manifest", relative: "plugins/official.waf/1.0.0/config.schema.json"},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := newSignedMarketFixture(t, []string{"http.request"}, []string{"http.request"})
			manifestData, err := os.ReadFile(filepath.Join(root, MarketManifestFile))
			if err != nil {
				t.Fatal(err)
			}
			digest := sha256.Sum256(manifestData)
			validator := newTestValidator(ValidatorOptions{})
			replaced := false
			validator.snapshotHook = func(stage, _, _ string) {
				if stage != test.stage || replaced {
					return
				}
				replaced = true
				if test.replaceRoot {
					replacement := newSignedMarketFixture(t, []string{"http.request"}, []string{"http.request"})
					if err := os.Rename(root, root+".original"); err != nil {
						t.Fatal(err)
					}
					if err := os.Rename(replacement, root); err != nil {
						t.Fatal(err)
					}
					return
				}
				name := filepath.Join(root, filepath.FromSlash(test.relative))
				backup := filepath.Join(t.TempDir(), filepath.Base(name))
				if err := os.Rename(name, backup); err != nil {
					t.Fatal(err)
				}
				data, err := os.ReadFile(backup)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(name, data, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			_, err = validator.ValidateMarketWithManifestDigest(root, false, hex.EncodeToString(digest[:]))
			if test.replaceRoot {
				if cleanupErr := os.RemoveAll(root); cleanupErr != nil {
					t.Fatal(cleanupErr)
				}
				if cleanupErr := os.Rename(root+".original", root); cleanupErr != nil {
					t.Fatal(cleanupErr)
				}
			}
			assertValidationCode(t, err, "snapshot_changed")
			if !replaced {
				t.Fatalf("snapshot hook %q did not run", test.stage)
			}
		})
	}

	root := newSignedMarketFixture(t, []string{"http.request"}, []string{"http.request"})
	_, err := newTestValidator(ValidatorOptions{}).ValidateMarketWithManifestDigest(root, false, strings.Repeat("0", 64))
	assertValidationCode(t, err, "market_digest")

	_, err = newTestValidator(ValidatorOptions{MaxMarketFiles: 1, MaxMarketBytes: 1}).ValidateMarket(root, false)
	assertValidationCode(t, err, "size_limit")
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

func TestConfigWritableInputRejectsNestedReadOnlyProperties(t *testing.T) {
	schema, err := DecodeConfigSchema([]byte(`{
		"type":"object",
		"properties":{
			"name":{"type":"string"},
			"status":{"type":"string","readOnly":true},
			"metadata":{"type":"object","properties":{"status":{"type":"string","readOnly":true}}},
			"items":{"type":"array","items":{"type":"object","properties":{"status":{"type":"string","readOnly":true}}}}
		}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateConfigWritableInput(schema, json.RawMessage(`{"name":"safe","metadata":{},"items":[{}]}`)); err != nil {
		t.Fatalf("ordinary writable config rejected: %v", err)
	}
	for _, test := range []struct {
		raw, pointer string
	}{
		{`{"name":"safe","status":"forged"}`, "/status"},
		{`{"name":"safe","metadata":{"status":"forged"}}`, "/metadata/status"},
		{`{"name":"safe","items":[{"status":"forged"}]}`, "/items/0/status"},
	} {
		if err := ValidateConfigWritableInput(schema, json.RawMessage(test.raw)); err == nil || !strings.Contains(err.Error(), test.pointer) {
			t.Fatalf("readOnly input %s error = %v, want pointer %q", test.raw, err, test.pointer)
		}
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

func TestValidatorAcceptsOnlyCanonicalRootJSONSchemaDialect(t *testing.T) {
	root := newPackageFixture(t)
	writeFixture(t, root, ConfigSchemaFile, `{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","additionalProperties":false}`)
	refreshFixtureDigest(t, root)
	if _, err := newTestValidator(ValidatorOptions{}).ValidatePackage(root, PackageExpectation{}); err != nil {
		t.Fatalf("ValidatePackage() rejected the canonical JSON Schema dialect: %v", err)
	}

	for name, schema := range map[string]string{
		"unsupported dialect": `{"$schema":"http://json-schema.org/draft-07/schema#","type":"object"}`,
		"nested dialect":      `{"type":"object","properties":{"value":{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"string"}}}`,
	} {
		t.Run(name, func(t *testing.T) {
			root := newPackageFixture(t)
			writeFixture(t, root, ConfigSchemaFile, schema)
			refreshFixtureDigest(t, root)
			_, err := newTestValidator(ValidatorOptions{}).ValidatePackage(root, PackageExpectation{})
			assertValidationCode(t, err, "config_schema")
		})
	}
}

func TestValidatorEnforcesBoundedRE2StringPatterns(t *testing.T) {
	root := newPackageFixture(t)
	writeFixture(t, root, ConfigSchemaFile, `{"type":"object","properties":{"id":{"type":"string","pattern":"^[a-z][a-z0-9-]{0,31}$"}},"required":["id"],"additionalProperties":false}`)
	refreshFixtureDigest(t, root)
	validated, err := newTestValidator(ValidatorOptions{}).ValidatePackage(root, PackageExpectation{})
	if err != nil {
		t.Fatalf("ValidatePackage() rejected a bounded RE2 pattern: %v", err)
	}
	if err := ValidateConfig(validated.ConfigSchema, json.RawMessage(`{"id":"valid-id"}`)); err != nil {
		t.Fatalf("ValidateConfig() rejected a matching value: %v", err)
	}
	if err := ValidateConfig(validated.ConfigSchema, json.RawMessage(`{"id":"INVALID"}`)); err == nil {
		t.Fatal("ValidateConfig() accepted a non-matching value")
	}

	for name, schema := range map[string]string{
		"invalid expression": `{"type":"object","properties":{"id":{"type":"string","pattern":"["}}}`,
		"non-string schema":  `{"type":"object","properties":{"id":{"type":"integer","pattern":"^[0-9]+$"}}}`,
	} {
		t.Run(name, func(t *testing.T) {
			root := newPackageFixture(t)
			writeFixture(t, root, ConfigSchemaFile, schema)
			refreshFixtureDigest(t, root)
			_, err := newTestValidator(ValidatorOptions{}).ValidatePackage(root, PackageExpectation{})
			assertValidationCode(t, err, "config_schema")
		})
	}
}

func TestValidatorEnforcesUniqueItemsWithJSONNumberSemantics(t *testing.T) {
	root := newPackageFixture(t)
	writeFixture(t, root, ConfigSchemaFile, `{"type":"object","properties":{"values":{"type":"array","maxItems":1024,"uniqueItems":true,"items":{"type":"number"}}},"required":["values"],"additionalProperties":false}`)
	refreshFixtureDigest(t, root)
	validated, err := newTestValidator(ValidatorOptions{}).ValidatePackage(root, PackageExpectation{})
	if err != nil {
		t.Fatalf("ValidatePackage() rejected uniqueItems: %v", err)
	}
	if err := ValidateConfig(validated.ConfigSchema, json.RawMessage(`{"values":[1,2]}`)); err != nil {
		t.Fatalf("ValidateConfig() rejected unique values: %v", err)
	}
	if err := ValidateConfig(validated.ConfigSchema, json.RawMessage(`{"values":[1,1.0]}`)); err == nil {
		t.Fatal("ValidateConfig() accepted numerically equal duplicate values")
	}

	root = newPackageFixture(t)
	writeFixture(t, root, ConfigSchemaFile, `{"type":"object","uniqueItems":true}`)
	refreshFixtureDigest(t, root)
	_, err = newTestValidator(ValidatorOptions{}).ValidatePackage(root, PackageExpectation{})
	assertValidationCode(t, err, "config_schema")
}

func TestValidatorBoundsUniqueItemsAndEnumWork(t *testing.T) {
	for name, schema := range map[string]string{
		"missing maxItems":   `{"type":"object","properties":{"values":{"type":"array","uniqueItems":true}}}`,
		"oversized maxItems": `{"type":"object","properties":{"values":{"type":"array","maxItems":1025,"uniqueItems":true}}}`,
	} {
		t.Run(name, func(t *testing.T) {
			root := newPackageFixture(t)
			writeFixture(t, root, ConfigSchemaFile, schema)
			refreshFixtureDigest(t, root)
			_, err := newTestValidator(ValidatorOptions{}).ValidatePackage(root, PackageExpectation{})
			assertValidationCode(t, err, "config_schema")
		})
	}

	values := make([]any, maxUniqueConfigItems)
	for index := range values {
		values[index] = map[string]any{"id": index, "nested": []any{map[string]any{"value": index}}}
	}
	config, err := json.Marshal(map[string]any{"values": values})
	if err != nil {
		t.Fatal(err)
	}
	schema, err := DecodeConfigSchema([]byte(`{"type":"object","properties":{"values":{"type":"array","maxItems":1024,"uniqueItems":true,"items":{"type":"object"}}},"required":["values"]}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateConfig(schema, config); err != nil {
		t.Fatalf("ValidateConfig() rejected %d distinct nested values: %v", maxUniqueConfigItems, err)
	}

	enumValues := make([]any, maxConfigEnumValues+1)
	for index := range enumValues {
		enumValues[index] = index
	}
	oversizedEnum := map[string]any{"type": "object", "properties": map[string]any{"value": map[string]any{"type": "integer", "enum": enumValues}}}
	if err := validateJSONSchema(oversizedEnum); err == nil {
		t.Fatal("validateJSONSchema() accepted an oversized enum")
	}
	duplicateEnum := map[string]any{"type": "object", "properties": map[string]any{"value": map[string]any{"type": "number", "enum": []any{json.Number("1"), json.Number("1.0")}}}}
	if err := validateJSONSchema(duplicateEnum); err == nil {
		t.Fatal("validateJSONSchema() accepted numerically equal enum values")
	}
}

func TestValidatorRejectsSignedPackageWithUnsatisfiableWritableSchema(t *testing.T) {
	tests := []struct {
		name   string
		schema string
		marker string
	}{
		{
			name:   "root readOnly",
			schema: `{"type":"object","readOnly":true}`,
			marker: "root config schema cannot be readOnly",
		},
		{
			name:   "required readOnly property",
			schema: `{"type":"object","properties":{"status":{"type":"string","readOnly":true}},"required":["status"]}`,
			marker: `required property "status" cannot be readOnly`,
		},
		{
			name:   "recursively mandatory readOnly property",
			schema: `{"type":"object","properties":{"settings":{"type":"object","properties":{"display":{"type":"object","properties":{"status":{"type":"string","readOnly":true}},"required":["status"]}},"required":["display"]}},"required":["settings"]}`,
			marker: `required property "status" cannot be readOnly`,
		},
		{
			name:   "required nonempty array with readOnly items",
			schema: `{"type":"object","properties":{"items":{"type":"array","minItems":1,"items":{"type":"object","readOnly":true}}},"required":["items"]}`,
			marker: "readOnly is only valid on named object properties",
		},
		{
			name:   "readOnly additionalProperties schema",
			schema: `{"type":"object","additionalProperties":{"type":"string","readOnly":true}}`,
			marker: "additionalProperties must be boolean",
		},
		{
			name:   "required property absent from closed properties",
			schema: `{"type":"object","properties":{"known":{"type":"string"}},"required":["missing"],"additionalProperties":false}`,
			marker: `required property "missing" is absent from properties`,
		},
		{
			name:   "array length range is reversed",
			schema: `{"type":"object","properties":{"items":{"type":"array","minItems":2e0,"maxItems":1.0,"items":{"type":"string"}}}}`,
			marker: "minItems exceeds maxItems",
		},
		{
			name:   "string length range is reversed",
			schema: `{"type":"object","properties":{"name":{"type":"string","minLength":3e0,"maxLength":2.0}}}`,
			marker: "minLength exceeds maxLength",
		},
		{
			name:   "numeric range remains fail closed",
			schema: `{"type":"object","properties":{"level":{"type":"number","minimum":2e0,"maximum":1.0}}}`,
			marker: "minimum exceeds maximum",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := newPackageFixture(t)
			writeFixture(t, root, ConfigSchemaFile, test.schema)
			refreshFixtureDigest(t, root)
			_, err := newTestValidator(ValidatorOptions{}).ValidatePackage(root, PackageExpectation{})
			assertValidationCode(t, err, "config_schema")
			if !strings.Contains(err.Error(), test.marker) {
				t.Fatalf("ValidatePackage() error = %v, want containing %q", err, test.marker)
			}
		})
	}
}

func TestValidatorAcceptsSignedPackageWithExactConsistentBoundarySchema(t *testing.T) {
	root := newPackageFixture(t)
	writeFixture(t, root, ConfigSchemaFile, `{
		"type":"object",
		"properties":{
			"items":{"type":"array","minItems":1e0,"maxItems":1.0,"items":{"type":"string"}},
			"name":{"type":"string","minLength":3e0,"maxLength":3.0},
			"level":{"type":"number","minimum":1e0,"maximum":1.0},
			"status":{"type":"string","readOnly":true}
		},
		"required":["items","name","level"],
		"additionalProperties":false
	}`)
	refreshFixtureDigest(t, root)
	validated, err := newTestValidator(ValidatorOptions{}).ValidatePackage(root, PackageExpectation{})
	if err != nil {
		t.Fatalf("ValidatePackage() rejected exact equal boundaries: %v", err)
	}
	if err := ValidateConfig(validated.ConfigSchema, json.RawMessage(`{"items":["one"],"name":"one","level":1}`)); err != nil {
		t.Fatalf("ValidateConfig() rejected boundary values: %v", err)
	}
}

func TestValidatorRuntimeConfigureCreateUpdateSchemaSatisfiability(t *testing.T) {
	validateConfigure := func(schema map[string]any, raw json.RawMessage) error {
		if err := ValidateConfigWritableInput(schema, raw); err != nil {
			return err
		}
		return ValidateConfig(schema, raw)
	}

	invalid, err := DecodeConfigSchema([]byte(`{
		"type":"object",
		"properties":{
			"name":{"type":"string"},
			"settings":{"type":"object","properties":{"status":{"type":"string","readOnly":true}},"required":["status"]}
		},
		"required":["name","settings"],
		"additionalProperties":false
	}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, operation := range []struct {
		name string
		raw  json.RawMessage
	}{
		{name: "create", raw: json.RawMessage(`{"name":"created","settings":{}}`)},
		{name: "update", raw: json.RawMessage(`{"name":"updated","settings":{}}`)},
	} {
		t.Run(operation.name+" rejects impossible schema", func(t *testing.T) {
			if err := validateConfigure(invalid, operation.raw); err == nil || !strings.Contains(err.Error(), `required property "status" cannot be readOnly`) {
				t.Fatalf("configure error = %v, want required/readOnly contradiction", err)
			}
		})
	}

	valid, err := DecodeConfigSchema([]byte(`{
		"type":"object",
		"properties":{
			"name":{"type":"string"},
			"status":{"type":"string","readOnly":true}
		},
		"required":["name"],
		"additionalProperties":false
	}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, operation := range []struct {
		name string
		raw  json.RawMessage
	}{
		{name: "create", raw: json.RawMessage(`{"name":"created"}`)},
		{name: "update", raw: json.RawMessage(`{"name":"updated"}`)},
	} {
		t.Run(operation.name+" accepts optional readOnly display field", func(t *testing.T) {
			if err := validateConfigure(valid, operation.raw); err != nil {
				t.Fatalf("configure rejected writable input: %v", err)
			}
		})
	}
	if err := validateConfigure(valid, json.RawMessage(`{"name":"updated","status":"forged"}`)); err == nil || !strings.Contains(err.Error(), "/status") {
		t.Fatalf("configure accepted client-owned optional readOnly field: %v", err)
	}
}

func TestValidatorAcceptsSignedPackageWithOptionalReadOnlyDisplayField(t *testing.T) {
	root := newPackageFixture(t)
	writeFixture(t, root, ConfigSchemaFile, `{"type":"object","properties":{"name":{"type":"string"},"status":{"type":"string","readOnly":true}},"required":["name"],"additionalProperties":false}`)
	refreshFixtureDigest(t, root)
	if _, err := newTestValidator(ValidatorOptions{}).ValidatePackage(root, PackageExpectation{}); err != nil {
		t.Fatalf("ValidatePackage() rejected optional readOnly display field: %v", err)
	}
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
	writeFixture(t, root, PackageManifestFile, validManifestYAML(ConfigSchemaFile)+"migrations:\n  - from: 0.9.0\n    to: 1.0.0\n    file: migrations/0.9-to-1.json\n")
	writeFixture(t, root, "migrations/0.9-to-1.json", `{"script":"fetch('https://example.com')"}`)
	refreshFixtureDigest(t, root)
	_, err := newTestValidator(ValidatorOptions{}).ValidatePackage(root, PackageExpectation{})
	assertValidationCode(t, err, "migration")

	writeFixture(t, root, "migrations/0.9-to-1.json", `{"operations":[{"op":"set","path":"/mode","value":"observe"},{"op":"remove","path":"/legacy"}]}`)
	refreshFixtureDigest(t, root)
	if _, err := newTestValidator(ValidatorOptions{}).ValidatePackage(root, PackageExpectation{}); err != nil {
		t.Fatalf("restricted migration was rejected: %v", err)
	}
}

func TestValidatorRejectsUnsupportedSchemaAndExecutesEveryAcceptedConstraint(t *testing.T) {
	for name, schema := range map[string]string{
		"reference": `{"type":"object","properties":{"mode":{"$ref":"#/definitions/mode"}},"definitions":{"mode":{"type":"string"}}}`,
		"one-of":    `{"type":"object","properties":{"mode":{"oneOf":[{"type":"string"},{"type":"number"}]}}}`,
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
