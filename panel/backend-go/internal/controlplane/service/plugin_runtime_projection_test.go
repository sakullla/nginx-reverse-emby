package service

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/plugins"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

func TestPluginRuntimePackageDTOUsesDurableArtifactProjection(t *testing.T) {
	manifest := plugins.Manifest{
		SchemaVersion: 1, ID: "dto.runtime", Version: "1.0.0", Name: "Runtime DTO",
		Runtime:        plugins.Runtime{Kind: "rpc-service", ABI: "nre:rpc/v1", HostScope: "control-plane", Entry: "artifacts/linux-amd64/plugin"},
		Artifacts:      []plugins.Artifact{{Path: "artifacts/linux-amd64/plugin", SHA256: strings.Repeat("b", 64), Size: 42, Mode: "executable", GOOS: "linux", GOARCH: "amd64"}},
		ResourceBudget: plugins.ResourceBudget{TimeoutMS: 500, MemoryBytes: 1 << 20, Concurrency: 1, InputBytes: 4096, OutputBytes: 4096, CPUMillis: 100, Restarts: 3},
		FailurePolicy:  plugins.FailurePolicy{OnError: "preserve-old", OnBudget: "preserve-old", Restart: "bounded", CoreFallback: "continue"},
		Signature:      plugins.Signature{Algorithm: "ed25519", KeyID: "release-key", File: "package.sig"},
	}
	manifestJSON, _ := json.Marshal(manifest)
	row, artifacts, err := storage.ProjectPluginPackage(storage.PluginPackageRow{Digest: strings.Repeat("a", 64), PluginID: manifest.ID, Version: manifest.Version, CachePath: `C:\panel\data\plugins\packages\secret`, ManifestJSON: string(manifestJSON), ConfigSchemaJSON: `{}`, VerifiedAt: time.Now().UTC()}, manifest)
	if err != nil {
		t.Fatal(err)
	}
	detail, err := pluginPackageDetail(row, artifacts, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if detail.Runtime != manifest.Runtime || len(detail.Artifacts) != 1 || detail.Artifacts[0].GOOS != "linux" || detail.ResourceBudget != manifest.ResourceBudget || detail.FailurePolicy != manifest.FailurePolicy || detail.Signature != manifest.Signature {
		t.Fatalf("runtime package detail = %+v", detail)
	}
	encoded, err := json.Marshal(detail)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "C:\\\\panel") || !strings.Contains(string(encoded), `"runtime"`) || !strings.Contains(string(encoded), `"artifacts"`) || !strings.Contains(string(encoded), `"signature"`) {
		t.Fatalf("runtime package JSON projection = %s", encoded)
	}

	summary := pluginSummary(storage.InstalledPluginRow{PluginID: manifest.ID, ActivePackageDigest: row.Digest, RuntimeKind: manifest.Runtime.Kind, RuntimeABI: manifest.Runtime.ABI, HostScope: manifest.Runtime.HostScope})
	if summary.RuntimeKind != "rpc-service" || summary.RuntimeABI != "nre:rpc/v1" || summary.HostScope != "control-plane" {
		t.Fatalf("installed runtime summary = %+v", summary)
	}
}

func TestPluginRuntimeProjectionMismatchRejectsStoredArtifactMutation(t *testing.T) {
	manifest := plugins.Manifest{
		ID: "integrity.runtime", Version: "1.0.0", Runtime: plugins.Runtime{Kind: "wasm-policy", ABI: "nre:policy/v1", HostScope: "agent", Entry: "artifacts/policy.wasm", PolicyKind: "waf"},
		Artifacts: []plugins.Artifact{{Path: "artifacts/policy.wasm", SHA256: strings.Repeat("c", 64), Size: 8, Mode: "readonly"}},
		Signature: plugins.Signature{Algorithm: "ed25519", KeyID: "release-key", File: "package.sig"},
	}
	manifestJSON, _ := json.Marshal(manifest)
	row, artifacts, err := storage.ProjectPluginPackage(storage.PluginPackageRow{Digest: strings.Repeat("d", 64), PluginID: manifest.ID, Version: manifest.Version, CachePath: "packages/integrity", ManifestJSON: string(manifestJSON), ConfigSchemaJSON: `{}`}, manifest)
	if err != nil {
		t.Fatal(err)
	}
	artifacts[0].SHA256 = strings.Repeat("e", 64)
	validated := plugins.ValidatedPackage{Manifest: manifest, ConfigSchema: map[string]any{}}
	if err := validateStoredPackageProjection(row, artifacts, validated); err == nil || !strings.Contains(err.Error(), "runtime projection") {
		t.Fatalf("stored artifact mutation error = %v", err)
	}
}
