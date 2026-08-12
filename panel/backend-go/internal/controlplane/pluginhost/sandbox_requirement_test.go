package pluginhost

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/plugins"
	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

func testControlRequirement(budget ProcessBudget, privileged, networkBound bool) SandboxRequirement {
	return SandboxRequirement{packageDigest: "test-package", budget: budget, privileged: privileged, networkBound: networkBound}
}

func validatedSandboxPackage(digest string, permissions []string, extensions []string) plugins.ValidatedPackage {
	manifestPermissions := make([]plugins.Permission, 0, len(permissions))
	for _, permission := range permissions {
		manifestPermissions = append(manifestPermissions, plugins.Permission{Name: permission})
	}
	return plugins.ValidatedPackage{Digest: digest, Manifest: plugins.Manifest{
		Runtime:         plugins.Runtime{Kind: pluginsdk.RuntimeRPCService, ABI: pluginsdk.RPCABIV1, HostScope: "control-plane", Entry: "plugin"},
		Permissions:     manifestPermissions,
		ExtensionPoints: append([]string(nil), extensions...),
		ResourceBudget:  plugins.ResourceBudget{TimeoutMS: 1000, MemoryBytes: 256 << 20, Concurrency: 2, InputBytes: 4096, OutputBytes: 4096, CPUMillis: 1000, Restarts: 2},
	}}
}

func mustValidatedSandboxRequirement(t *testing.T, digest string) SandboxRequirement {
	t.Helper()
	requirement, err := SandboxRequirementFromValidatedPackage(validatedSandboxPackage(digest, []string{"agent.read"}, []string{"http.request"}))
	if err != nil {
		t.Fatal(err)
	}
	return requirement
}

func TestPluginHostSandboxRequirementUsesValidatedCanonicalManifest(t *testing.T) {
	digest := strings.Repeat("a", 64)
	base := validatedSandboxPackage(digest, []string{"agent.read"}, []string{"http.request"})
	requirement, err := SandboxRequirementFromValidatedPackage(base)
	if err != nil || requirement.Budget().Processes != 8 || requirement.Budget().Files != 64 || !requirement.HighRisk() {
		t.Fatalf("validated requirement = %+v, %v", requirement, err)
	}
	for _, permission := range []string{"container.manage", "dns.manage", "secret.use", "storage.write"} {
		got, err := SandboxRequirementFromValidatedPackage(validatedSandboxPackage(digest, []string{permission}, []string{"http.request"}))
		if err != nil || !got.RequiresPrivilegeBoundary() {
			t.Fatalf("permission %q requirement = %+v, %v", permission, got, err)
		}
	}
	for _, capability := range []pluginsdk.HostCapability{
		pluginsdk.CapabilityPolicyAtomicState,
		pluginsdk.CapabilityPolicyMonotonicClock,
		pluginsdk.CapabilityPolicyTrustedSource,
		pluginsdk.CapabilityServiceRevocableResourceHandle,
		pluginsdk.CapabilityUIDynamicActions,
	} {
		got, err := SandboxRequirementFromValidatedPackage(validatedSandboxPackage(digest, []string{string(capability)}, []string{"http.request"}))
		if err != nil || got.RequiresPrivilegeBoundary() || got.RequiresFilesystemBoundary() || got.Budget().Network {
			t.Fatalf("host capability %q requirement = %+v, %v", capability, got, err)
		}
	}
	for _, extension := range []string{"container.provider", "dns.provider", "tunnel.provider"} {
		got, err := SandboxRequirementFromValidatedPackage(validatedSandboxPackage(digest, []string{"agent.read"}, []string{extension}))
		if err != nil || !got.RequiresPrivilegeBoundary() || !got.Budget().Network {
			t.Fatalf("extension %q requirement = %+v, %v", extension, got, err)
		}
	}
	invalid := validatedSandboxPackage(digest, nil, []string{"invalid.extension"})
	if _, err := SandboxRequirementFromValidatedPackage(invalid); err == nil {
		t.Fatal("non-validator extension authority was accepted")
	}
	if err := requirement.validatePackageDigest(strings.Repeat("b", 64)); err == nil {
		t.Fatal("requirement was rebound to another package digest")
	}
	overflow := base
	overflow.Manifest.ResourceBudget.Concurrency = 4097
	if _, err := SandboxRequirementFromValidatedPackage(overflow); err == nil {
		t.Fatal("out-of-validator resource projection was accepted")
	}
}

func TestPluginHostSandboxRequirementProjectsSignedValidatorResult(t *testing.T) {
	root, key := writeSignedSandboxPackage(t, "secret.use", "container.provider")
	validator := plugins.NewValidator(plugins.ValidatorOptions{
		HostVersion: "1.0.0", AgentVersion: "1.0.0", TargetGOOS: runtime.GOOS, TargetGOARCH: runtime.GOARCH,
		MaxPackageBytes: 256 << 20, MaxFileBytes: 256 << 20,
		TrustedSigners: map[string]ed25519.PublicKey{"sandbox-test": key.Public().(ed25519.PublicKey)}, TrustedSignerPolicy: plugins.TrustedSignerPolicyExact,
	})
	validated, err := validator.ValidatePackage(root, plugins.PackageExpectation{})
	if err != nil {
		t.Fatal(err)
	}
	requirement, err := SandboxRequirementFromValidatedPackage(validated)
	if err != nil || !requirement.RequiresPrivilegeBoundary() || !requirement.Budget().Network {
		t.Fatalf("signed validator projection = %+v, %v", requirement, err)
	}

	invalidRoot, invalidKey := writeSignedSandboxPackage(t, "agent.read", "invalid.extension")
	invalidValidator := plugins.NewValidator(plugins.ValidatorOptions{
		HostVersion: "1.0.0", AgentVersion: "1.0.0", TargetGOOS: runtime.GOOS, TargetGOARCH: runtime.GOARCH,
		MaxPackageBytes: 256 << 20, MaxFileBytes: 256 << 20,
		TrustedSigners: map[string]ed25519.PublicKey{"sandbox-test": invalidKey.Public().(ed25519.PublicKey)}, TrustedSignerPolicy: plugins.TrustedSignerPolicyExact,
	})
	if _, err := invalidValidator.ValidatePackage(invalidRoot, plugins.PackageExpectation{}); err == nil {
		t.Fatal("validator accepted a non-canonical sandbox extension")
	}
}

func TestPluginHostSandboxRequirementAcceptsSignedCanonicalHostCapabilities(t *testing.T) {
	for _, capability := range []pluginsdk.HostCapability{
		pluginsdk.CapabilityPolicyAtomicState,
		pluginsdk.CapabilityPolicyMonotonicClock,
		pluginsdk.CapabilityPolicyTrustedSource,
		pluginsdk.CapabilityServiceRevocableResourceHandle,
		pluginsdk.CapabilityUIDynamicActions,
	} {
		t.Run(string(capability), func(t *testing.T) {
			root, key := writeSignedSandboxPackage(t, string(capability), "http.request")
			validator := plugins.NewValidator(plugins.ValidatorOptions{
				HostVersion: "1.0.0", AgentVersion: "1.0.0", TargetGOOS: runtime.GOOS, TargetGOARCH: runtime.GOARCH,
				MaxPackageBytes: 256 << 20, MaxFileBytes: 256 << 20,
				TrustedSigners: map[string]ed25519.PublicKey{"sandbox-test": key.Public().(ed25519.PublicKey)}, TrustedSignerPolicy: plugins.TrustedSignerPolicyExact,
			})
			validated, err := validator.ValidatePackage(root, plugins.PackageExpectation{})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := SandboxRequirementFromValidatedPackage(validated); err != nil {
				t.Fatalf("signed capability %q was rejected by sandbox projection: %v", capability, err)
			}
		})
	}
}

var sandboxArtifactFixture struct {
	once sync.Once
	data []byte
	err  error
}

func sandboxArtifact(t *testing.T) []byte {
	t.Helper()
	sandboxArtifactFixture.once.Do(func() {
		root, err := os.MkdirTemp("", "nre-sandbox-artifact-")
		if err != nil {
			sandboxArtifactFixture.err = err
			return
		}
		defer os.RemoveAll(root)
		source := filepath.Join(root, "main.go")
		if err := os.WriteFile(source, []byte("package main\nfunc main() {}\n"), 0o600); err != nil {
			sandboxArtifactFixture.err = err
			return
		}
		output := filepath.Join(root, "plugin")
		if runtime.GOOS == "windows" {
			output += ".exe"
		}
		command := exec.Command("go", "build", "-trimpath", "-ldflags=-s -w", "-o", output, source)
		command.Env = append(os.Environ(), "CGO_ENABLED=0")
		if buildOutput, err := command.CombinedOutput(); err != nil {
			sandboxArtifactFixture.err = fmt.Errorf("build sandbox artifact: %w: %s", err, buildOutput)
			return
		}
		sandboxArtifactFixture.data, sandboxArtifactFixture.err = os.ReadFile(output)
	})
	if sandboxArtifactFixture.err != nil {
		t.Fatal(sandboxArtifactFixture.err)
	}
	return sandboxArtifactFixture.data
}

func writeSignedSandboxPackage(t *testing.T, permission, extension string) (string, ed25519.PrivateKey) {
	t.Helper()
	root := t.TempDir()
	artifact := sandboxArtifact(t)
	artifactDigest := sha256.Sum256(artifact)
	artifactName := "plugin"
	if runtime.GOOS == "windows" {
		artifactName += ".exe"
	}
	artifactPath := filepath.ToSlash(filepath.Join("artifacts", runtime.GOOS+"-"+runtime.GOARCH, artifactName))
	manifest := fmt.Sprintf(`schema_version: 1
id: sandbox.validator
version: 1.0.0
name: Sandbox Validator
compatibility: {host: "*", agent: "*"}
runtime: {kind: rpc-service, abi: "nre:rpc/v1", host_scope: control-plane, entry: plugin}
artifacts:
  - {path: %s, sha256: %s, size: %d, mode: executable, goos: %s, goarch: %s}
extension_points: [%s]
permissions: [%s]
config_schema: config.schema.json
resource_budget: {timeout_ms: 1000, memory_bytes: 268435456, concurrency: 2, input_bytes: 4096, output_bytes: 4096, cpu_millis: 1000, restarts: 2}
failure_policy: {on_error: degraded, on_budget: fail-closed, restart: on-failure, core_fallback: preserve}
signature: {algorithm: ed25519, key_id: sandbox-test, file: package.sig}
cleanup: {instances: delete, config: delete, owned_data: delete, grants: delete, shared_refs: retain, audit_events: retain}
`, artifactPath, hex.EncodeToString(artifactDigest[:]), len(artifact), runtime.GOOS, runtime.GOARCH, extension, permission)
	writeSandboxFixture(t, root, plugins.PackageManifestFile, []byte(manifest))
	writeSandboxFixture(t, root, plugins.ConfigSchemaFile, []byte(`{"type":"object"}`))
	writeSandboxFixture(t, root, artifactPath, artifact)
	digest, err := plugins.ComputePackageDigest(root)
	if err != nil {
		t.Fatal(err)
	}
	seed := sha256.Sum256([]byte("sandbox-validator-test"))
	key := ed25519.NewKeyFromSeed(seed[:])
	writeSandboxFixture(t, root, plugins.PackageDigestFile, []byte(digest+"\n"))
	writeSandboxFixture(t, root, plugins.PackageSignatureFile, []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(key, []byte(digest)))+"\n"))
	return root, key
}

func validatedSignedSandboxPackage(t *testing.T, permission, extension string) plugins.ValidatedPackage {
	t.Helper()
	root, key := writeSignedSandboxPackage(t, permission, extension)
	validator := plugins.NewValidator(plugins.ValidatorOptions{
		HostVersion: "1.0.0", AgentVersion: "1.0.0", TargetGOOS: runtime.GOOS, TargetGOARCH: runtime.GOARCH,
		MaxPackageBytes: 256 << 20, MaxFileBytes: 256 << 20,
		TrustedSigners: map[string]ed25519.PublicKey{"sandbox-test": key.Public().(ed25519.PublicKey)}, TrustedSignerPolicy: plugins.TrustedSignerPolicyExact,
	})
	validated, err := validator.ValidatePackage(root, plugins.PackageExpectation{})
	if err != nil {
		t.Fatal(err)
	}
	return validated
}

func writeSandboxFixture(t *testing.T, root, name string, content []byte) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
}
