//go:build !fast && !integration

package storage

import (
	"crypto/sha256"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/marketplace"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/plugins"
	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

func TestLoadAgentPluginPoliciesProjectsDualFaceWAFWithoutSelectingNodes(t *testing.T) {
	store, err := newStorageTestSQLiteStore(t, t.TempDir(), "local", true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ctx := t.Context()
	now := time.Date(2026, 9, 2, 8, 0, 0, 0, time.UTC)
	cacheRoot := filepath.Join(store.dataRoot, "plugins", "packages")

	dual := seedPolicyCatalogPackage(t, store, cacheRoot, policyCatalogSeed{
		pluginID: "official.waf", instanceID: "official.waf-default", digestSeed: "a",
		runtime: plugins.Runtime{
			Kind: pluginsdk.RuntimeRPCService, ABI: pluginsdk.RPCABIV1,
			HostScope: pluginsdk.HostScopeControlPlane, Entry: "plugin", PolicyKind: "waf",
			Policy: &pluginsdk.RuntimePolicy{
				Kind: pluginsdk.RuntimeWASMPolicy, ABI: pluginsdk.PolicyABIV1, HostScope: pluginsdk.HostScopeAgent,
				Entry:          "artifacts/policy.wasm",
				ResourceBudget: plugins.ResourceBudget{TimeoutMS: 2, MemoryBytes: 1048576, Concurrency: 8, InputBytes: 65536, OutputBytes: 4096},
				FailurePolicy:  plugins.FailurePolicy{OnError: "fail-closed", OnBudget: "fail-closed", Restart: "never", CoreFallback: "preserve"},
			},
		},
		extensions:   []string{pluginsdk.ExtensionUIRoute, "http.request"},
		targets:      `[]`,
		policyChains: `[]`,
		desired:      "enabled",
		current:      "active",
	}, now)
	seedPolicyCatalogPackage(t, store, cacheRoot, policyCatalogSeed{
		pluginID: "official.ip", instanceID: "official.ip-1", digestSeed: "b",
		runtime: plugins.Runtime{
			Kind: pluginsdk.RuntimeWASMPolicy, ABI: pluginsdk.PolicyABIV1,
			HostScope: pluginsdk.HostScopeAgent, Entry: "artifacts/policy.wasm", PolicyKind: "ip",
		},
		extensions:   []string{"http.request"},
		targets:      `["edge-a"]`,
		policyChains: `[]`,
		desired:      "enabled",
		current:      "active",
	}, now)
	seedPolicyCatalogPackage(t, store, cacheRoot, policyCatalogSeed{
		pluginID: "docker-app", instanceID: "docker-app-default", digestSeed: "c",
		runtime: plugins.Runtime{
			Kind: pluginsdk.RuntimeRPCService, ABI: pluginsdk.RPCABIV1,
			HostScope: pluginsdk.HostScopeControlPlane, HostScopes: []string{pluginsdk.HostScopeAgent}, Entry: "plugin",
		},
		extensions:   []string{pluginsdk.ExtensionUIRoute},
		targets:      `["edge-a"]`,
		policyChains: `["docker-app-default"]`,
		desired:      "enabled",
		current:      "active",
	}, now)
	if _, err := marketplace.NewVerifiedCache(cacheRoot, plugins.NewValidator(plugins.ValidatorOptions{}), nil); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = marketplace.DiscardVerifiedCacheRoot(cacheRoot) })

	policies, err := store.LoadAgentPluginPolicies(ctx, "edge-a")
	if err != nil {
		t.Fatalf("LoadAgentPluginPolicies(edge-a) = %v", err)
	}
	if len(policies) != 1 || policies[0].ID != "official.waf-default" || len(policies[0].Stages) != 1 {
		t.Fatalf("dual-face catalog = %+v", policies)
	}
	stage := policies[0].Stages[0]
	if stage.Kind != "waf" || stage.PluginID != "official.waf" || stage.InstanceID != "official.waf-default" || stage.ABI != pluginsdk.PolicyABIV1 || stage.ResourceBudget.TimeoutMS != 2 {
		t.Fatalf("dual-face stage = %+v", stage)
	}
	if stage.ArtifactSource.RelativePath != "artifacts/policy.wasm" {
		t.Fatalf("dual-face wasm entry = %+v", stage.ArtifactSource)
	}

	local, err := store.LoadAgentPluginPolicies(ctx, "local")
	if err != nil {
		t.Fatalf("LoadAgentPluginPolicies(local) = %v", err)
	}
	if len(local) != 1 || local[0].ID != dual.instanceID {
		t.Fatalf("local dual-face catalog = %+v", local)
	}
}

type policyCatalogSeed struct {
	pluginID, instanceID, digestSeed string
	runtime                          plugins.Runtime
	extensions                       []string
	targets, policyChains            string
	desired, current                 string
}

func seedPolicyCatalogPackage(t *testing.T, store *GormStore, cacheRoot string, seed policyCatalogSeed, now time.Time) policyCatalogSeed {
	t.Helper()
	digest := strings.Repeat(seed.digestSeed, 64)
	identity := strings.Repeat(strings.ToUpper(seed.digestSeed), 64)
	fingerprint := strings.Repeat("e", 64)
	wasm := []byte("waf-policy-wasm-" + seed.pluginID)
	wasmDigest := sha256.Sum256(wasm)
	rpcDigest := sha256.Sum256([]byte("rpc-" + seed.pluginID))
	artifacts := []plugins.Artifact{{
		Path: "artifacts/policy.wasm", SHA256: encodeCatalogDigest(wasmDigest), Size: int64(len(wasm)), Mode: "wasm",
	}}
	if seed.runtime.Kind == pluginsdk.RuntimeRPCService {
		artifacts = append([]plugins.Artifact{{
			Path: "artifacts/linux-amd64/plugin", SHA256: encodeCatalogDigest(rpcDigest), Size: 12, Mode: "executable", GOOS: "linux", GOARCH: "amd64",
		}}, artifacts...)
	}
	budget := plugins.ResourceBudget{TimeoutMS: 2, MemoryBytes: 1048576, Concurrency: 8, InputBytes: 65536, OutputBytes: 4096}
	failure := plugins.FailurePolicy{OnError: "fail-closed", OnBudget: "fail-closed", Restart: "never", CoreFallback: "preserve"}
	if seed.runtime.Kind == pluginsdk.RuntimeRPCService {
		budget = plugins.ResourceBudget{TimeoutMS: 2000, MemoryBytes: 1048576, Concurrency: 8, InputBytes: 65536, OutputBytes: 4096, CPUMillis: 100, Restarts: 1}
		failure = plugins.FailurePolicy{OnError: "fail-closed", OnBudget: "fail-closed", Restart: "on-failure", CoreFallback: "preserve"}
	}
	manifest := plugins.Manifest{
		SchemaVersion: 1, ID: seed.pluginID, Version: "1.0.0", Name: seed.pluginID,
		Runtime: seed.runtime, Artifacts: artifacts, ExtensionPoints: append([]string(nil), seed.extensions...),
		Permissions:    []plugins.Permission{{Name: "http.inspect"}},
		ResourceBudget: budget, FailurePolicy: failure,
		Signature: plugins.Signature{Algorithm: "ed25519", KeyID: "fixture"},
	}
	manifestJSON, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	row := PluginPackageRow{
		Identity: identity, Digest: digest, PluginID: seed.pluginID, Version: manifest.Version,
		SignatureKeyID: "fixture", SignaturePublicKey: "pub", SignatureFingerprint: fingerprint, SignatureVerdict: "verified",
		SourceID: "fixture", SourceKind: "custom", CachePath: "", ManifestJSON: string(manifestJSON), ConfigSchemaJSON: `{}`, VerifiedAt: now,
	}
	projected, projectedArtifacts, err := ProjectPluginPackage(row, manifest)
	if err != nil {
		t.Fatal(err)
	}
	cachePath, err := marketplace.SignerCachePath(cacheRoot, digest, fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(cachePath, "artifacts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cachePath, "artifacts", "policy.wasm"), wasm, 0o600); err != nil {
		t.Fatal(err)
	}
	projected.CachePath = cachePath
	if err := store.db.Create(&projected).Error; err != nil {
		t.Fatal(err)
	}
	for index := range projectedArtifacts {
		if err := store.db.Create(&projectedArtifacts[index]).Error; err != nil {
			t.Fatal(err)
		}
	}
	installed := InstalledPluginRow{
		PluginID: seed.pluginID, ActivePackageDigest: digest, ActivePackageIdentity: identity,
		RuntimeKind: projected.RuntimeKind, RuntimeABI: projected.RuntimeABI, HostScope: projected.HostScope,
		ActiveSourceID: projected.SourceID, ActiveSourceKind: projected.SourceKind,
		ActiveSignatureKeyID: projected.SignatureKeyID, ActiveSignaturePublicKey: projected.SignaturePublicKey,
		ActiveSignatureFingerprint: projected.SignatureFingerprint, DesiredLifecycle: seed.desired, CurrentLifecycle: seed.current,
		CleanupPolicyJSON: `{}`, LastOperationID: "install-" + seed.pluginID, StateVersion: 1, InstalledAt: now, UpdatedAt: now,
	}
	if err := store.db.Create(&installed).Error; err != nil {
		t.Fatal(err)
	}
	instance := PluginInstanceRow{
		ID: seed.instanceID, PluginID: seed.pluginID, ResourceGroupID: "default", TargetJSON: seed.targets,
		PolicyChainsJSON: seed.policyChains, SecretHandlesJSON: `[]`, BindingsJSON: `[]`, ConfigJSON: `{}`,
		ConfigVersion: 1, PendingConfigJSON: "", PendingTargetJSON: "", PendingPolicyChainsJSON: `[]`,
		PendingBindingsJSON: `[]`, PendingSecretHandlesJSON: `[]`, RollbackConfigJSON: "", RollbackPolicyChainsJSON: `[]`,
		RollbackBindingsJSON: `[]`, RollbackSecretHandlesJSON: `[]`, DesiredEnabled: seed.desired == "enabled",
		CurrentState: "active", StatusSummaryJSON: `{}`, StateVersion: 1, UpdatedAt: now,
	}
	if err := store.db.Create(&instance).Error; err != nil {
		t.Fatal(err)
	}
	return seed
}

func encodeCatalogDigest(sum [32]byte) string {
	const hexdigits = "0123456789abcdef"
	out := make([]byte, 64)
	for i, b := range sum {
		out[i*2] = hexdigits[b>>4]
		out[i*2+1] = hexdigits[b&0x0f]
	}
	return string(out)
}
