//go:build linux

package localagent

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/pluginhost"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/service"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
	sdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
	"github.com/sakullla/nginx-reverse-emby/plugin-sdk/go/rpcplugin"
)

type scopedChildConfig struct {
	Reference    sdk.ScopedSecretReference `json:"reference"`
	ExpectedHash string                    `json:"expected_hash"`
}

// The snapshot launches the real test executable without test-run arguments.
// The private Host endpoint only exists inside that actual sandboxed child.
func init() {
	if os.Getenv(sdk.EnvPluginEndpoint) == "" {
		return
	}
	if err := runLocalScopedSDKChild(); err != nil {
		fmt.Fprintln(os.Stderr, "scoped SDK fixture failed")
		os.Exit(1)
	}
	os.Exit(0)
}

func runLocalScopedSDKChild() error {
	client, err := sdk.NewHostRuntimeClientFromEnvironment()
	if err != nil {
		return err
	}
	adapter, err := rpcplugin.NewAdapter(rpcplugin.Config{PluginID: "scoped-test", PluginVersion: "1.0.0", RequiredGrants: []string{sdk.PermissionScopedSecretRead}, SupportedFeatures: sdk.RequiredRPCFeatures([]string{sdk.PermissionScopedSecretRead}), Timeouts: rpcplugin.UniformTimeouts(10 * time.Second)}, rpcplugin.HookFuncs{
		PrepareFunc: func(ctx context.Context, generation *rpcplugin.Generation, raw []byte) error {
			var config scopedChildConfig
			if json.Unmarshal(raw, &config) != nil {
				return errors.New("invalid scoped fixture config")
			}
			response, err := client.ScopedSecret(ctx, sdk.ScopedSecretRequest{Action: sdk.ScopedSecretRead, Reference: config.Reference, Binding: sdk.ManagedBinding{InstanceID: "scoped-instance", EntryID: "scoped-instance", Generation: generation.ID()}})
			if err != nil {
				return err
			}
			defer response.Material.Close()
			return response.Material.WithBytes(func(value []byte) error {
				digest := sha256.Sum256(value)
				if hex.EncodeToString(digest[:]) != config.ExpectedHash {
					return errors.New("scoped fixture delivery mismatch")
				}
				return nil
			})
		},
	})
	if err != nil {
		return err
	}
	return sdk.ServeRPCPlugin(context.Background(), adapter)
}

type scopedHeartbeatStore struct{ *storage.GormStore }

func (s scopedHeartbeatStore) LoadLocalSnapshot(context.Context, string) (storage.Snapshot, error) {
	return storage.Snapshot{}, nil
}

func TestLocalActualScopedPrepareReadAndGenerationRevoke(t *testing.T) {
	f := newLocalScopedFixture(t)
	// Heartbeat data is deliberately empty; only the approved snapshot carried
	// by ApplyRevision can launch this fixture. Secret/lease/artifact owners are
	// the real durable store, Vault, and production PluginService.
	f.source = NewSyncSource(scopedHeartbeatStore{f.store}, f.agentID)
	f.source.SetPluginSecretSource(f.service)
	controller := pluginhost.Candidate{InstanceID: "scoped-instance", ResourceGroupID: "default", Identity: pluginhost.Identity{PluginID: "scoped-test", Generation: "controller-generation"}, Grants: []string{sdk.PermissionScopedSecretWrite}, GrantSelectors: map[string][]string{sdk.PermissionScopedSecretWrite: {"secret-scope:outbound-auth"}}}
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		t.Fatal(err)
	}
	defer clear(value)
	material, _ := sdk.NewManagedSecretMaterial(value)
	defer material.Close()
	create := sdk.ScopedSecretRequest{Action: sdk.ScopedSecretCreate, Binding: sdk.ManagedBinding{InstanceID: controller.InstanceID, Generation: controller.Identity.Generation, EntryID: controller.InstanceID}, Reference: sdk.ScopedSecretReference{InstanceID: controller.InstanceID, ID: "upstream", Scope: "outbound-auth"}, Material: material}
	wire, _ := sdk.EncodeScopedSecretRequest(create)
	response := f.manager.DispatchPluginHostResource(t.Context(), controller, sdk.HostRuntimeCall{Operation: sdk.HostRuntimeScopedSecret, Payload: wire})
	clear(wire)
	if response.Error != nil {
		t.Fatal(response.Error)
	}
	created, err := sdk.DecodeScopedSecretResponse(create, response.Payload)
	clear(response.Payload)
	if err != nil {
		t.Fatal(err)
	}
	valueDigest := sha256.Sum256(value)
	config, _ := json.Marshal(scopedChildConfig{Reference: created.Reference, ExpectedHash: hex.EncodeToString(valueDigest[:])})
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(binary)
	artifactDigest := hex.EncodeToString(digest[:])
	packageDigest, packageIdentity, signer := strings.Repeat("a", 64), strings.Repeat("b", 64), strings.Repeat("c", 64)
	cache := filepath.Join(f.root, "fixture-package")
	if err := os.MkdirAll(filepath.Join(cache, "bin"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cache, "bin", "plugin"), binary, 0o600); err != nil {
		t.Fatal(err)
	}
	budget := storage.PluginGenerationResourceBudget{TimeoutMS: 10000, MemoryBytes: 256 << 20, Concurrency: 4, InputBytes: 1 << 20, OutputBytes: 1 << 20, CPUMillis: 1000, Restarts: 1}
	generation := storage.PluginGeneration{InstanceID: controller.InstanceID, OperationID: "plugin-operation", Revision: 7, PluginID: controller.Identity.PluginID, PluginVersion: "1.0.0", PackageDigest: packageDigest,
		Runtime: storage.PluginGenerationRuntime{Kind: "rpc-service", ABI: sdk.RPCABIV1, HostScope: "agent", Entry: "bin/plugin"}, Artifact: storage.PluginGenerationArtifact{ArtifactID: "fixture-artifact", PackageIdentity: packageIdentity, RelativePath: "bin/plugin", SHA256: artifactDigest, SizeBytes: int64(len(binary)), Mode: "executable", GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, SignatureVerified: true, SignerKeyID: "fixture-key", SignerFingerprint: signer},
		ExtensionPoints: []string{"l4.accept"}, RequiredFeatures: sdk.RequiredRPCFeatures([]string{sdk.PermissionScopedSecretRead}), ConfigVersion: 1, Config: config, Grants: []storage.PluginGenerationGrant{{Name: sdk.PermissionScopedSecretRead, ResourceKind: "secret-scope", ResourceID: "outbound-auth"}}, ResourceBudget: budget,
		Target: storage.PluginGenerationTarget{Kind: "agent", ID: f.agentID, ResourceGroupID: "default", Version: 1}, FailurePolicy: storage.PluginGenerationFailurePolicy{OnError: "fail-closed", OnBudget: "fail-closed", Restart: "never", CoreFallback: "preserve"}}
	generation.ID, err = storage.PluginGenerationIdentity(generation)
	if err != nil {
		t.Fatal(err)
	}
	manifest := `{"runtime":{"kind":"rpc-service","abi":"nre:rpc/v1","host_scope":"agent","entry":"bin/plugin"}}`
	rows := []any{
		&storage.PluginPackageRow{Identity: packageIdentity, Digest: packageDigest, PluginID: generation.PluginID, Version: "1.0.0", CachePath: cache, ManifestJSON: manifest, SignatureVerdict: "verified", SignatureKeyID: "fixture-key", SignatureFingerprint: signer},
		&storage.PluginArtifactRow{ID: "fixture-artifact", PackageIdentity: packageIdentity, PackageDigest: packageDigest, Path: "bin/plugin", RuntimeKind: "rpc-service", RuntimeABI: sdk.RPCABIV1, HostScope: "agent", SHA256: artifactDigest, SizeBytes: int64(len(binary))},
		&storage.InstalledPluginRow{PluginID: generation.PluginID, ActivePackageIdentity: packageIdentity, ActivePackageDigest: packageDigest, CurrentLifecycle: "active", LastOperationID: generation.OperationID},
		&storage.PluginInstanceRow{ID: generation.InstanceID, PluginID: generation.PluginID, ResourceGroupID: "default", TargetJSON: `["` + f.agentID + `"]`, ConfigVersion: 1, DesiredEnabled: true},
		&storage.PluginOperationRow{ID: generation.OperationID, PluginID: generation.PluginID, Status: "succeeded"},
		&storage.PluginGrantRow{ID: "scoped-read", GrantKey: "scoped-read", PluginID: generation.PluginID, PackageIdentity: packageIdentity, PackageDigest: packageDigest, Permission: sdk.PermissionScopedSecretRead, ResourceSelector: "secret-scope:outbound-auth"},
		&storage.PluginAgentRuntimeStatusRow{OperationID: generation.OperationID, AgentID: f.agentID, InstanceID: generation.InstanceID, PluginID: generation.PluginID, Revision: 7, GenerationID: generation.ID, PackageDigest: packageDigest, ArtifactDigest: artifactDigest, ResourceGroupID: "default", ConfigVersion: 1, TargetVersion: 1, AuthoritySlot: "active", State: "active"},
	}
	for _, row := range rows {
		if err := f.db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	lease := f.startedLease(t, 7)
	outer, inner := f.startRuntime(t, f.store)
	session := NewLocalTaskSessionWithDiagnostics(f.agentID, f.tasks, f.store, outer)
	if err := session.Register(); err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	if err := applyRevisionWithinLease(ctx, outer, storage.Snapshot{Revision: 7, PluginGenerations: []storage.PluginGeneration{generation}}, lease); err != nil {
		t.Fatal("actual scoped Prepare/read", err)
	}
	revision, _, err := f.store.GetCoordinatorRevision(ctx, f.agentID, 7)
	if err != nil {
		t.Fatal(err)
	}
	if revision.RuntimeGenerationID == "" || revision.RuntimeGenerationID != inner.GenerationDrainSnapshot().ActiveGenerationID || revision.GenerationID == revision.RuntimeGenerationID {
		t.Fatal("actual runtime/attempt identity was not bound")
	}
	var delivered []storage.PluginScopedSecretDeliveryRow
	if err := f.db.Where("instance_id = ?", generation.InstanceID).Find(&delivered).Error; err != nil {
		t.Fatal(err)
	}
	if len(delivered) != 1 || delivered[0].GenerationID != revision.RuntimeGenerationID || delivered[0].ProviderGenerationID != generation.ID {
		_ = inner.SyncNow(ctx)
		logs, _ := f.store.ListPluginRuntimeLogs(ctx, storage.PluginRuntimeLogQuery{InstanceID: generation.InstanceID, AgentID: f.agentID, ResourceGroupID: "default", Limit: 10})
		for _, row := range logs.Rows {
			t.Log(row.Message)
		}
		t.Fatal("real child did not produce exact durable delivery evidence")
	}
	// A dataset/settings revision reuses the active deployment definition. Its
	// new runtime must redeem during Prepare while the deployment fence stays
	// at revision 7 and still authorizes the previous bound runtime.
	if _, err := f.store.ApplyAgentRevisionAttempt(ctx, storage.CoordinatorApplyRequest{Lease: storage.CoordinatorLease{AgentID: f.agentID, Revision: 7, RetryCycle: lease.RetryCycle, Attempt: lease.Attempt, LeaseID: lease.LeaseID}, GenerationID: embeddedGenerationID(lease), Now: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	nextGeneration := generation
	nextGeneration.Revision = 8
	nextSnapshot := storage.Snapshot{Revision: 8, PluginGenerations: []storage.PluginGeneration{nextGeneration}}
	nextLease := f.startedLease(t, 8, nextSnapshot)
	if err := applyRevisionWithinLease(ctx, outer, nextSnapshot, nextLease); err != nil {
		t.Fatal("inherited independent revision Prepare/read", err)
	}
	nextRevision, _, err := f.store.GetCoordinatorRevision(ctx, f.agentID, 8)
	if err != nil {
		t.Fatal(err)
	}
	var nextDeliveries []storage.PluginScopedSecretDeliveryRow
	if err := f.db.Where("instance_id = ? AND revision = ?", generation.InstanceID, 8).Find(&nextDeliveries).Error; err != nil {
		t.Fatal(err)
	}
	if len(nextDeliveries) != 1 || nextDeliveries[0].GenerationID != nextRevision.RuntimeGenerationID || nextDeliveries[0].ProviderGenerationID != generation.ID {
		t.Fatal("second actual SDK Prepare did not redeem inherited deployment")
	}
	read := sdk.ScopedSecretRequest{Action: sdk.ScopedSecretRead, Reference: created.Reference, Binding: sdk.ManagedBinding{InstanceID: generation.InstanceID, EntryID: generation.InstanceID, Generation: revision.RuntimeGenerationID}}
	oldWire, _ := sdk.EncodeScopedSecretRequest(read)
	defer clear(oldWire)
	oldRequest := service.PluginSecretRedemptionRequest{Revision: 7, GenerationID: generation.ID, RuntimeGenerationID: revision.RuntimeGenerationID, InstanceID: generation.InstanceID, PluginID: generation.PluginID, OperationID: generation.OperationID, PackageDigest: generation.PackageDigest, ArtifactDigest: generation.Artifact.SHA256, Scoped: oldWire}
	oldRead, err := f.service.RedeemAgentPluginSecrets(ctx, f.agentID, oldRequest)
	if err != nil {
		t.Fatal("independent revision displaced old authority", err)
	}
	clear(oldRead.Scoped)
	if err := inner.SyncNow(ctx); err != nil {
		t.Fatal("post-cutover telemetry", err)
	}
	read.Binding.Generation = nextRevision.RuntimeGenerationID
	nextWire, _ := sdk.EncodeScopedSecretRequest(read)
	defer clear(nextWire)
	nextRequest := oldRequest
	nextRequest.Revision = 8
	nextRequest.RuntimeGenerationID = nextRevision.RuntimeGenerationID
	nextRequest.Scoped = nextWire
	postTelemetry, err := f.service.RedeemAgentPluginSecrets(ctx, f.agentID, nextRequest)
	if err != nil {
		t.Fatal("inherited generation lost secret authority after telemetry", err)
	}
	clear(postTelemetry.Scoped)
	for _, forgery := range []string{"revision", "runtime"} {
		bad := oldRequest
		if forgery == "revision" {
			bad.Revision = 99
		} else {
			bad.RuntimeGenerationID = "generation-8-forged"
		}
		if result, err := f.service.RedeemAgentPluginSecrets(ctx, f.agentID, bad); err == nil {
			clear(result.Scoped)
			t.Fatal("forged inherited authority accepted", forgery)
		}
	}
	revoke := create
	revoke.Action, revoke.Reference, revoke.Material = sdk.ScopedSecretRevoke, created.Reference, nil
	wire, _ = sdk.EncodeScopedSecretRequest(revoke)
	response = f.manager.DispatchPluginHostResource(ctx, controller, sdk.HostRuntimeCall{Operation: sdk.HostRuntimeScopedSecret, Payload: wire})
	clear(wire)
	if response.Error != nil {
		t.Fatal("local task did not confirm actual process revocation", response.Error)
	}
	ack, err := sdk.DecodeScopedSecretResponse(revoke, response.Payload)
	clear(response.Payload)
	if err != nil || !ack.Revoked {
		t.Fatal("revoke acknowledgement", err)
	}
	var pending int64
	if err := f.db.Model(&storage.PluginScopedSecretDeliveryRow{}).Where("acknowledged = ?", false).Count(&pending).Error; err != nil || pending != 0 {
		t.Fatal("local task did not durably acknowledge exact delivery", err)
	}
}
