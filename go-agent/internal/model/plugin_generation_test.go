package model

import (
	"encoding/json"
	"runtime"
	"strings"
	"testing"
)

func TestPluginGenerationStrictValidationAndWirePresence(t *testing.T) {
	generation := validPluginGenerationForTest()
	snapshot := Snapshot{Revision: 7, PluginGenerations: []PluginGeneration{generation}}
	if err := ValidatePluginGenerations(snapshot, false); err != nil {
		t.Fatalf("ValidatePluginGenerations() error = %v", err)
	}
	wire, err := json.Marshal(Snapshot{PluginGenerations: []PluginGeneration{}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(wire), `"plugin_generations":[]`) {
		t.Fatalf("empty PluginGenerations wire = %s", wire)
	}

	for name, mutate := range map[string]func(*PluginGeneration){
		"operation": func(value *PluginGeneration) { value.OperationID = "" },
		"revision":  func(value *PluginGeneration) { value.Revision++ },
		"digest":    func(value *PluginGeneration) { value.PackageDigest = strings.Repeat("A", 64) },
		"path":      func(value *PluginGeneration) { value.Artifact.RelativePath = "../plugin" },
		"signature": func(value *PluginGeneration) { value.Artifact.SignatureVerified = false },
		"runtime":   func(value *PluginGeneration) { value.Runtime.Kind = PluginRuntimeWASMPolicy },
		"local":     func(value *PluginGeneration) { value.Artifact.LocalPath = "already-materialized" },
	} {
		t.Run(name, func(t *testing.T) {
			invalid := generation
			mutate(&invalid)
			if err := invalid.Validate(7, false); err == nil {
				t.Fatal("Validate() accepted invalid projection")
			}
		})
	}
}

func TestPluginGenerationMaterializedValidationRequiresLocalPath(t *testing.T) {
	generation := validPluginGenerationForTest()
	if err := generation.Validate(7, true); err == nil {
		t.Fatal("Validate(materialized) accepted empty local path")
	}
	generation.Artifact.LocalPath = "/agent/cache/plugin"
	if err := generation.Validate(7, true); err != nil {
		t.Fatalf("Validate(materialized) error = %v", err)
	}
}

func validPluginGenerationForTest() PluginGeneration {
	return PluginGeneration{
		ID: "generation-7", InstanceID: "instance-7", OperationID: "operation-7", Revision: 7,
		PluginID: "example.rpc", PluginVersion: "1.2.3", PackageDigest: strings.Repeat("a", 64),
		Runtime: PluginRuntimeDescriptor{Kind: PluginRuntimeRPCService, ABI: PluginRPCABIV1, HostScope: "agent", Entry: "artifacts/plugin"},
		Artifact: PluginArtifactDescriptor{ArtifactID: "artifact-7", PackageIdentity: "example.rpc@1.2.3", RelativePath: "artifacts/plugin",
			SHA256: strings.Repeat("b", 64), SizeBytes: 42, Mode: "executable", GOOS: runtime.GOOS, GOARCH: runtime.GOARCH,
			SignatureVerified: true, SignerKeyID: "release-key", SignerFingerprint: strings.Repeat("c", 64)},
		ExtensionPoints: []string{"http.request"}, ConfigVersion: 1, Config: json.RawMessage(`{"enabled":true}`),
		Grants:         []PluginGrantProjection{{Name: "agent.read"}},
		SecretHandles:  []PluginSecretHandle{{ID: "secret-7", Version: 1, Digest: strings.Repeat("d", 64), Purpose: "upstream"}},
		ResourceBudget: PluginResourceBudget{TimeoutMS: 1000, MemoryBytes: 1 << 20, Concurrency: 2, InputBytes: 4096, OutputBytes: 4096, CPUMillis: 100, Restarts: 2},
		Target:         PluginTargetBinding{Kind: "agent", ID: "edge-7", ResourceGroupID: "default", Version: 1},
		FailurePolicy:  PluginFailurePolicy{OnError: "degraded", OnBudget: "fail-closed", Restart: "on-failure", CoreFallback: "preserve"},
	}
}
