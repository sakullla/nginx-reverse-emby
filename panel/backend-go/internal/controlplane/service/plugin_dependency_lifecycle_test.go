package service

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/marketplace"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/plugins"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

func TestConfigurePluginBindingRejectsLegacyHTTPRuleConsumers(t *testing.T) {
	ctx := WithSystemMutationPrincipal(t.Context(), "test")
	dataRoot := t.TempDir()
	store, err := storage.NewSQLiteStore(dataRoot, "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	seedPluginAgent(t, ctx, store)
	now := time.Now().UTC()
	if err := store.CreateResourceGroup(ctx, storage.ResourceGroupRow{ID: "other", Name: "Other", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveHTTPRules(ctx, "local", []storage.HTTPRuleRow{
		{ID: 1, FrontendURL: "https://owned.example.test", BackendsJSON: `[{"url":"http://127.0.0.1:8096"}]`, Enabled: true, Revision: 1},
		{ID: 2, FrontendURL: "https://foreign.example.test", BackendsJSON: `[{"url":"http://127.0.0.1:8097"}]`, Enabled: true, Revision: 1},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.BindResource(ctx, storage.ResourceBindingRow{ID: "owned-rule", ResourceKind: "http_rule", ResourceID: "local:1", ResourceGroupID: "default", ParentResourceKind: "agent", ParentResourceID: "local", UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.BindResource(ctx, storage.ResourceBindingRow{ID: "foreign-rule", ResourceKind: "http_rule", ResourceID: "local:2", ResourceGroupID: "other", ParentResourceKind: "agent", ParentResourceID: "local", UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	cacheRoot := filepath.Join(dataRoot, "plugins", "packages")
	svc := newPluginTestServiceAtRoot(t, store, cacheRoot)
	candidate := rpcPluginCandidateFixtureAtRoot(t, cacheRoot, "official.rpc-binding", "1.0.0")
	installed, err := svc.Install(ctx, PluginInstallRequest{Package: candidate, ActorID: "admin", ConfirmedPermissions: []string{"http.inspect"}, RiskAccepted: true})
	if err != nil {
		t.Fatal(err)
	}
	foreign := []storage.PluginInstanceBindingRequest{{Consumer: storage.PluginInstanceBindingConsumer{Kind: "http_rule", ID: "2"}, TargetAgentID: "local"}}
	if _, err := svc.Configure(ctx, PluginConfigureRequest{PluginID: installed.PluginID, InstanceID: "provider", ResourceGroupID: "default", Targets: []string{"local"}, Bindings: &foreign, Config: json.RawMessage(`{"mode":"observe"}`), ActorID: "admin"}); err == nil {
		t.Fatal("cross-group core consumer binding was accepted")
	}
	owned := []storage.PluginInstanceBindingRequest{{Consumer: storage.PluginInstanceBindingConsumer{Kind: "http_rule", ID: "1"}, TargetAgentID: "local"}}
	if _, err := svc.Configure(ctx, PluginConfigureRequest{PluginID: installed.PluginID, InstanceID: "provider", ResourceGroupID: "default", Targets: []string{"local"}, Bindings: &owned, Config: json.RawMessage(`{"mode":"observe"}`), ActorID: "admin"}); err == nil {
		t.Fatal("legacy HTTP rule binding was accepted")
	}
}

func rpcPluginCandidateFixtureAtRoot(t *testing.T, cacheRoot, id, version string) PluginPackageCandidate {
	t.Helper()
	staging := t.TempDir()
	buildRoot := t.TempDir()
	sourcePath := filepath.Join(buildRoot, "main.go")
	writePluginCandidateFile(t, buildRoot, "main.go", "package main\nfunc main() {}\n")
	executablePath := filepath.Join(buildRoot, "plugin")
	command := exec.Command("go", "build", "-trimpath", "-ldflags=-s -w", "-o", executablePath, sourcePath)
	command.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH=amd64")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build RPC fixture: %v: %s", err, output)
	}
	artifact, err := os.ReadFile(executablePath)
	if err != nil {
		t.Fatal(err)
	}
	artifactDigest := sha256.Sum256(artifact)
	artifactPath := "artifacts/linux-amd64/plugin"
	manifest := "schema_version: 1\nid: " + id + "\nversion: " + version + "\nname: RPC Test\ncompatibility: {host: \"*\", agent: \"*\"}\nruntime: {kind: rpc-service, abi: \"nre:rpc/v1\", host_scope: agent, entry: plugin}\nartifacts:\n  - {path: " + artifactPath + ", sha256: " + hexDigest(artifactDigest[:]) + ", size: " + strconv.Itoa(len(artifact)) + ", mode: executable, goos: linux, goarch: amd64}\nextension_points: [http.request]\npermissions: [http.inspect]\nconfig_schema: config.schema.json\nresource_budget: {timeout_ms: 1000, memory_bytes: 67108864, concurrency: 4, input_bytes: 65536, output_bytes: 65536, cpu_millis: 500, restarts: 3}\nfailure_policy: {on_error: degraded, on_budget: fail-closed, restart: on-failure, core_fallback: preserve}\nsignature: {algorithm: ed25519, key_id: test-fixture, file: package.sig}\ncleanup: {instances: retain, config: retain, owned_data: retain, grants: retain, shared_refs: retain, audit_events: retain}\n"
	writePluginCandidateFile(t, staging, plugins.PackageManifestFile, manifest)
	writePluginCandidateFile(t, staging, plugins.ConfigSchemaFile, `{"type":"object","properties":{"mode":{"type":"string"}},"required":["mode"],"additionalProperties":false}`)
	writePluginCandidateBytes(t, staging, artifactPath, artifact)
	digest, err := plugins.ComputePackageDigest(staging)
	if err != nil {
		t.Fatal(err)
	}
	writePluginCandidateFile(t, staging, plugins.PackageDigestFile, digest)
	writePluginCandidateFile(t, staging, plugins.PackageSignatureFile, base64.StdEncoding.EncodeToString(ed25519.Sign(pluginTestSigningKey(), []byte(digest))))
	validated, err := pluginTestValidator().ValidatePackage(staging, plugins.PackageExpectation{ID: id, Version: version, SHA256: digest})
	if err != nil {
		t.Fatal(err)
	}
	trust, err := pluginTestSignatureTrust()
	if err != nil {
		t.Fatal(err)
	}
	target, err := marketplace.SignerCachePath(cacheRoot, digest, trust.Fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(staging, target); err != nil {
		t.Fatal(err)
	}
	validated.Root = target
	return PluginPackageCandidate{Package: validated, Runtime: validated.Manifest.Runtime, Artifacts: append([]plugins.Artifact(nil), validated.Manifest.Artifacts...), SignatureTrust: trust, CachePath: target, sourceID: trust.SourceID, sourceKind: trust.SourceKind, sourceRiskLabel: marketplace.UntrustedRiskLabel}
}
