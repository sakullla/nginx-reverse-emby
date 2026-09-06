//go:build !fast

package storage

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	goruntime "runtime"
	"sort"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/pkg/datasets"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/marketplace"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/plugins"
	sdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
	"github.com/sakullla/nginx-reverse-emby/plugin-sdk/go/compatfixture"
)

func TestPolicyConsumptionCopyDefaultMigrationRows(t *testing.T) {
	for _, populated := range []bool{false, true} {
		t.Run(map[bool]string{false: "empty", true: "populated"}[populated], func(t *testing.T) {
			source, target := newTrafficTestStore(t, true), newTrafficTestStore(t, true)
			consumptionWritableCache(t, source)
			consumptionWritableCache(t, target)
			if populated {
				instance := consumptionStoreInstance(t, source, "policy-probe", true, `[]`)
				entry := consumptionStoreInstance(t, source, "rpc-probe", false, `["local"]`)
				version := consumptionStoreDataset(t, source, "192.0.2.0/24")
				consumptionStoreBind(t, source, instance.ID, version.Digest, 1, sdk.ExecutionTargetSelection{Mode: sdk.ExecutionTargetsEffective})
				if err := source.SecurityTransaction(t.Context(), func(tx *GormStore) error {
					if err := tx.UpdatePluginConsumptionInstance(t.Context(), instance.ID, instance.StateVersion, json.RawMessage(`{"rules":["cn-44"]}`)); err != nil {
						return err
					}
					if err := tx.PutPluginPolicySettings(t.Context(), PluginPolicySettingsRow{InstanceID: instance.ID, Revision: 2, DefaultMode: "observe"}); err != nil {
						return err
					}
					for _, kind := range []string{"plugin-tcp", "plugin-udp"} {
						if err := tx.PutPluginPolicyEntryMode(t.Context(), PluginPolicyEntryModeRow{InstanceID: instance.ID, NodeID: "local", Kind: kind, EntryID: entry.ID, Mode: "enforce"}, false); err != nil {
							return err
						}
					}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
			}
			if err := CopyDefaultMigrationRows(t.Context(), source, target); err != nil {
				t.Fatal(err)
			}
			if populated {
				sourceInstance, sourceFound, sourceErr := source.GetPluginInstance(t.Context(), "instance-policy-probe")
				targetInstance, targetFound, targetErr := target.GetPluginInstance(t.Context(), "instance-policy-probe")
				if sourceErr != nil || targetErr != nil || !sourceFound || !targetFound || sourceInstance.IncarnationID == "" || targetInstance.IncarnationID != sourceInstance.IncarnationID {
					t.Fatalf("migration changed instance incarnation: source=%+v found=%v err=%v target=%+v found=%v err=%v", sourceInstance, sourceFound, sourceErr, targetInstance, targetFound, targetErr)
				}
			}
			// Read all four tables, rather than testing only a hand-picked field.
			for _, rowSet := range []struct {
				name, order    string
				source, target any
			}{
				{"settings", "instance_id", &[]PluginPolicySettingsRow{}, &[]PluginPolicySettingsRow{}},
				{"entry modes", "instance_id, node_id, kind, entry_id", &[]PluginPolicyEntryModeRow{}, &[]PluginPolicyEntryModeRow{}},
				{"bindings", "instance_id, source_id", &[]PluginDatasetConsumptionRow{}, &[]PluginDatasetConsumptionRow{}},
				{"operations", "id", &[]PluginConsumptionOperationRow{}, &[]PluginConsumptionOperationRow{}},
			} {
				if err := source.db.Order(rowSet.order).Find(rowSet.source).Error; err != nil {
					t.Fatal(err)
				}
				if err := target.db.Order(rowSet.order).Find(rowSet.target).Error; err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(rowSet.source, rowSet.target) {
					t.Errorf("migration changed %s: source=%+v target=%+v", rowSet.name, rowSet.source, rowSet.target)
				}
				count := reflect.ValueOf(rowSet.target).Elem().Len()
				if populated && count == 0 || !populated && count != 0 {
					t.Errorf("migration %s count=%d populated=%v", rowSet.name, count, populated)
				}
			}
		})
	}
}

func TestPolicyConsumptionEmptyTargetsProtectVersionUntilUnbind(t *testing.T) {
	store := newTrafficTestStore(t, true)
	consumptionWritableCache(t, store)
	instance := consumptionStoreInstance(t, store, "rpc-probe", false, `[]`)
	old := consumptionStoreDataset(t, store, "192.0.2.0/24")
	// The old version must be outside the three-version retention window, so
	// a deletion rejection proves a logical reference, not history retention.
	for _, prefix := range []string{"198.51.100.0/24", "203.0.113.0/24", "10.0.0.0/8"} {
		consumptionStoreDataset(t, store, prefix)
	}
	bound := consumptionStoreBind(t, store, instance.ID, old.Digest, 1, sdk.ExecutionTargetSelection{Mode: sdk.ExecutionTargetsEffective})
	for _, node := range []string{"local", "edge-new"} {
		rows, err := store.ResolveDatasetBindings(t.Context(), node)
		if err != nil || len(rows) != 0 {
			t.Fatalf("empty RPC targets expanded on %s: %+v %v", node, rows, err)
		}
	}
	physical, err := store.DatasetBindings(t.Context(), "regions")
	if err != nil || len(physical) != 0 {
		t.Fatalf("logical empty-target bind invented physical delivery: %+v %v", physical, err)
	}
	if err := store.DeleteDatasetVersion(t.Context(), "regions", old.Digest); !errors.Is(err, ErrDatasetInUse) {
		t.Fatalf("logical version reference was ignored: %v", err)
	}
	if err := store.DeleteDatasetSource(t.Context(), "regions"); !errors.Is(err, ErrDatasetInUse) {
		t.Fatalf("logical source reference was ignored: %v", err)
	}
	unbound := consumptionStoreBind(t, store, instance.ID, "", 2, sdk.ExecutionTargetSelection{Mode: sdk.ExecutionTargetsEffective})
	row, err := store.GetPluginDatasetConsumption(t.Context(), instance.ID, "regions")
	if err != nil || row.Revision != 2 || row.RecordJSON != "" {
		t.Fatalf("unbind lost its revision tombstone: %+v %v", row, err)
	}
	if err := store.DeleteDatasetVersion(t.Context(), "regions", old.Digest); err != nil {
		t.Fatalf("unbound old version remained protected: %v", err)
	}
	if err := store.DeleteDatasetSource(t.Context(), "regions"); err != nil {
		t.Fatalf("unbound source remained protected: %v", err)
	}
	for _, want := range []PluginConsumptionOperationRow{bound, unbound} {
		got, found, err := store.GetPluginConsumptionOperation(t.Context(), want.ID)
		if err != nil || !found || got != want {
			t.Fatalf("deletion rewrote original replay record: %+v found=%v err=%v", got, found, err)
		}
		if err := store.PutPluginConsumptionOperation(t.Context(), want); err == nil {
			t.Fatal("duplicate operation identity overwrote durable replay")
		}
	}
	row, err = store.GetPluginDatasetConsumption(t.Context(), instance.ID, "regions")
	if err != nil || row.Revision != 2 || row.RecordJSON != "" {
		t.Fatalf("source deletion reset binding revision: %+v %v", row, err)
	}
}

func TestPolicyConsumptionTargetExpansionFollowsExecutionFace(t *testing.T) {
	for _, policyFace := range []bool{false, true} {
		t.Run(map[bool]string{false: "Agent RPC", true: "control-plane RPC and Agent policy"}[policyFace], func(t *testing.T) {
			store := newTrafficTestStore(t, true)
			consumptionWritableCache(t, store)
			if err := store.SaveAgent(t.Context(), AgentRow{ID: "edge-a", Name: "edge-a"}); err != nil {
				t.Fatal(err)
			}
			instance := consumptionStoreInstance(t, store, "execution-probe", policyFace, `[]`)
			version := consumptionStoreDataset(t, store, "192.0.2.0/24")
			consumptionStoreBind(t, store, instance.ID, version.Digest, 1, sdk.ExecutionTargetSelection{Mode: sdk.ExecutionTargetsEffective})
			assertNodes := func(want []string) {
				t.Helper()
				before, err := store.DatasetBindings(t.Context(), "regions")
				if err != nil {
					t.Fatal(err)
				}
				rows, err := store.DatasetConsumerBindings(t.Context(), "regions")
				if err != nil {
					t.Fatal(err)
				}
				got := []string{}
				for _, row := range rows {
					if row.InstanceID != instance.ID || row.SourceID != "regions" || row.VersionDigest != version.Digest || row.ClassificationsJSON != `[{"name":"cn-44","kind":"region"}]` {
						t.Fatalf("expanded binding changed selection: %+v", row)
					}
					got = append(got, row.AgentID)
				}
				sort.Strings(got)
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("actual consumer nodes=%v want=%v", got, want)
				}
				after, err := store.DatasetBindings(t.Context(), "regions")
				if err != nil || !reflect.DeepEqual(before, after) {
					t.Fatalf("read-side expansion changed physical bindings: before=%+v after=%+v err=%v", before, after, err)
				}
			}
			if policyFace {
				assertNodes([]string{"edge-a", "local"})
			} else {
				assertNodes([]string{})
			}
			consumptionStoreTargets(t, store, &instance, `["local"]`)
			if policyFace {
				assertNodes([]string{"edge-a", "local"})
			} else {
				assertNodes([]string{"local"})
			}
			if err := store.SaveAgent(t.Context(), AgentRow{ID: "edge-new", Name: "edge-new"}); err != nil {
				t.Fatal(err)
			}
			consumptionStoreTargets(t, store, &instance, `["edge-new"]`)
			if policyFace {
				assertNodes([]string{"edge-a", "edge-new", "local"})
			} else {
				assertNodes([]string{"edge-new"})
			}
			// An explicit consumption subset remains a filter over producer targets.
			consumptionStoreBind(t, store, instance.ID, version.Digest, 2, sdk.ExecutionTargetSelection{Mode: sdk.ExecutionTargetsSubset, AgentIDs: []string{"edge-new"}})
			assertNodes([]string{"edge-new"})
		})
	}
}

func TestPolicyConsumptionManagedEntryCleanupAndInstanceIncarnation(t *testing.T) {
	store := newTrafficTestStore(t, true)
	consumptionWritableCache(t, store)
	if err := store.SaveAgent(t.Context(), AgentRow{ID: "edge-a", Name: "edge-a"}); err != nil {
		t.Fatal(err)
	}
	policy := consumptionStoreInstance(t, store, "policy-owner", true, `[]`)
	managed := consumptionStoreInstance(t, store, "managed-owner", false, `["local","edge-a"]`)
	firstIncarnation := managed.IncarnationID
	if firstIncarnation == "" {
		t.Fatal("new instance has no incarnation")
	}
	for _, node := range []string{"local", "edge-a"} {
		for _, kind := range []string{sdk.PolicyEntryManagedTCP, sdk.PolicyEntryManagedUDP} {
			if err := store.PutPluginPolicyEntryMode(t.Context(), PluginPolicyEntryModeRow{InstanceID: policy.ID, NodeID: node, Kind: kind, EntryID: managed.ID, Mode: "observe"}, false); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := store.PutPluginPolicyEntryMode(t.Context(), PluginPolicyEntryModeRow{InstanceID: policy.ID, NodeID: "local", Kind: sdk.PolicyEntryManagedTCP, EntryID: "unrelated", Mode: "observe"}, false); err != nil {
		t.Fatal(err)
	}
	consumptionStoreTargets(t, store, &managed, `["edge-a"]`)
	rows, err := store.ListPluginPolicyEntryModes(t.Context(), policy.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.EntryID == managed.ID && row.NodeID == "local" {
			t.Fatalf("removed managed target retained mode: %+v", row)
		}
	}
	installed, found, err := store.GetInstalledPlugin(t.Context(), managed.PluginID)
	if err != nil || !found {
		t.Fatal("managed plugin unavailable", err)
	}
	deleteOperation := pluginTargetNormalizationOperation("delete-managed-incarnation", managed.PluginID, managed.ID, "succeeded", time.Now().UTC())
	if err := store.ApplyPluginMutation(t.Context(), PluginMutation{PluginID: managed.PluginID, ExpectedActive: installed.ActivePackageDigest, ExpectedStateVersion: installed.StateVersion, Installed: &installed, DeleteInstanceID: managed.ID, ExpectedInstanceVersion: managed.StateVersion, Operation: deleteOperation, Audit: AuditEventRow{ID: deleteOperation.ID, ActorID: "admin", Action: "plugin.delete-instance", TargetKind: "plugin", TargetID: managed.PluginID, Result: "success", MetadataJSON: `{}`, CreatedAt: deleteOperation.CreatedAt}}); err != nil {
		t.Fatal(err)
	}
	rows, err = store.ListPluginPolicyEntryModes(t.Context(), policy.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].EntryID != "unrelated" {
		t.Fatalf("managed deletion retained stale or removed unrelated modes: %+v", rows)
	}
	recreated := managed
	recreated.StateVersion = 0
	recreated.IncarnationID = ""
	recreated.ConfigVersion++
	recreated.TargetJSON = `["edge-a"]`
	recreateOperation := pluginTargetNormalizationOperation("recreate-managed-incarnation", recreated.PluginID, recreated.ID, "succeeded", time.Now().UTC())
	installed, _, _ = store.GetInstalledPlugin(t.Context(), recreated.PluginID)
	if err := store.ApplyPluginMutation(t.Context(), PluginMutation{PluginID: recreated.PluginID, ExpectedActive: installed.ActivePackageDigest, ExpectedStateVersion: installed.StateVersion, Installed: &installed, ReplaceInstance: &recreated, Operation: recreateOperation, Audit: AuditEventRow{ID: recreateOperation.ID, ActorID: "admin", Action: "plugin.configure", TargetKind: "plugin", TargetID: recreated.PluginID, Result: "success", MetadataJSON: `{}`, CreatedAt: recreateOperation.CreatedAt}}); err != nil {
		t.Fatal(err)
	}
	if recreated.IncarnationID == "" || recreated.IncarnationID == firstIncarnation {
		t.Fatalf("recreated instance reused incarnation: old=%q new=%q", firstIncarnation, recreated.IncarnationID)
	}
}

// These tests use the Store mutation boundaries and SDK-valid records. Service
// ownership checks and publication transaction coverage belong to service tests.
func consumptionStoreInstance(t *testing.T, store *GormStore, pluginID string, policyFace bool, targets string) PluginInstanceRow {
	t.Helper()
	now := time.Now().UTC()
	if err := store.UpsertBuiltinResourceGroup(t.Context(), ResourceGroupRow{ID: "default", Name: "Default", Builtin: true, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	runtime := sdk.Runtime{Kind: sdk.RuntimeRPCService, ABI: sdk.RPCABIV1, HostScope: sdk.HostScopeAgent, Entry: "plugin"}
	rpcPath := "artifacts/" + goruntime.GOOS + "-" + goruntime.GOARCH + "/plugin"
	executable := "/usr/bin/true"
	if goruntime.GOOS == "windows" {
		executable = filepath.Join(os.Getenv("SystemRoot"), "System32", "where.exe")
		rpcPath += ".exe"
	}
	if policyFace {
		runtime.HostScope, runtime.PolicyKind = sdk.HostScopeControlPlane, "ip"
		runtime.Policy = &sdk.RuntimePolicy{Kind: sdk.RuntimeWASMPolicy, ABI: sdk.PolicyABIV1, HostScope: sdk.HostScopeAgent, Entry: "artifacts/policy.wasm",
			ResourceBudget: sdk.ResourceBudget{TimeoutMS: 2, MemoryBytes: 1 << 20, Concurrency: 1, InputBytes: 4096, OutputBytes: 4096},
			FailurePolicy:  sdk.FailurePolicy{OnError: "fail-closed", OnBudget: "fail-closed", Restart: "never", CoreFallback: "preserve"}}
	}
	metadata := map[string]string{}
	if policyFace {
		metadata[sdk.PolicyModeHandlingMetadataKey] = string(sdk.PolicyModeHandlingRaw)
	}
	rpc, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	rpcSum := sha256.Sum256(rpc)
	manifest := sdk.Manifest{SchemaVersion: 1, ID: pluginID, Name: pluginID, Version: "1.0.0", Runtime: runtime, Metadata: metadata,
		Compatibility: sdk.Compatibility{Host: ">=1.0.0 <2.0.0", Agent: ">=1.0.0 <2.0.0"}, ConfigSchema: plugins.ConfigSchemaFile,
		Cleanup:         sdk.CleanupPolicy{Instances: "delete", Config: "delete", OwnedData: "delete", Grants: "delete", SharedRefs: "retain", AuditEvents: "retain"},
		Artifacts:       []sdk.Artifact{{Path: rpcPath, SHA256: hex.EncodeToString(rpcSum[:]), Size: int64(len(rpc)), Mode: "executable", GOOS: goruntime.GOOS, GOARCH: goruntime.GOARCH}},
		ExtensionPoints: []string{sdk.ExtensionL4Accept}, ResourceBudget: sdk.ResourceBudget{TimeoutMS: 1000, MemoryBytes: 1 << 20, Concurrency: 1, InputBytes: 4096, OutputBytes: 4096, CPUMillis: 100, Restarts: 1},
		FailurePolicy: sdk.FailurePolicy{OnError: "fail-closed", OnBudget: "fail-closed", Restart: "never", CoreFallback: "preserve"}, Signature: sdk.Signature{Algorithm: "ed25519", KeyID: "fixture", File: plugins.PackageSignatureFile}}
	wasm := compatfixture.PolicyV1GuestWASM()
	if policyFace {
		wasmSum := sha256.Sum256(wasm)
		manifest.Artifacts = append(manifest.Artifacts, sdk.Artifact{Path: runtime.Policy.Entry, SHA256: hex.EncodeToString(wasmSum[:]), Size: int64(len(wasm)), Mode: "wasm"})
		manifest.ExtensionPoints = append(manifest.ExtensionPoints, sdk.ExtensionUIRoute, sdk.ExtensionHTTPRequest)
		manifest.UIRouteID = pluginID
	}
	manifestJSON, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	packageRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(packageRoot, "artifacts"), 0o700); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string][]byte{plugins.PackageManifestFile: manifestJSON, plugins.ConfigSchemaFile: []byte(`{"type":"object"}`), rpcPath: rpc} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(packageRoot, name)), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(packageRoot, name), content, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if policyFace {
		if err := os.WriteFile(filepath.Join(packageRoot, runtime.Policy.Entry), wasm, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	digest, err := plugins.ComputePackageDigest(packageRoot)
	if err != nil {
		t.Fatal(err)
	}
	seed := sha256.Sum256([]byte("policy-consumption-storage-test-only-signing-key"))
	key := ed25519.NewKeyFromSeed(seed[:])
	publicKey := base64.StdEncoding.EncodeToString(key.Public().(ed25519.PublicKey))
	fingerprint, err := marketplace.SourceSignerFingerprint(publicKey)
	if err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{plugins.PackageDigestFile: digest + "\n", plugins.PackageSignatureFile: base64.StdEncoding.EncodeToString(ed25519.Sign(key, []byte(digest))) + "\n"} {
		if err := os.WriteFile(filepath.Join(packageRoot, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	trust := marketplace.SignatureTrust{SourceID: "fixture", SourceKind: "custom", KeyID: "fixture", PublicKey: publicKey, Fingerprint: fingerprint}
	validator, err := marketplace.ValidatorForSignatureTrust(trust)
	if err != nil {
		t.Fatal(err)
	}
	validated, err := validator.ValidatePackageIntegrity(packageRoot, plugins.PackageExpectation{SHA256: digest, SignatureKeyID: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	cachePath, err := marketplace.ImportVerifiedPackage(filepath.Join(store.dataRoot, "plugins", "packages"), validated, validator, trust)
	if err != nil {
		t.Fatal(err)
	}
	pkg, artifacts, err := ProjectPluginPackage(PluginPackageRow{Digest: digest, PluginID: pluginID, Version: "1.0.0", SourceID: trust.SourceID, SourceKind: trust.SourceKind, SignatureKeyID: "fixture", SignaturePublicKey: publicKey, SignatureFingerprint: fingerprint, SignatureVerdict: "verified", ManifestJSON: string(manifestJSON), ConfigSchemaJSON: `{"type":"object"}`, VerifiedAt: now}, manifest)
	if err != nil {
		t.Fatal(err)
	}
	pkg.CachePath = cachePath
	installed := InstalledPluginRow{PluginID: pluginID, ActivePackageDigest: digest, ActivePackageIdentity: pkg.Identity, ActiveSourceID: trust.SourceID, ActiveSourceKind: trust.SourceKind, ActiveSignatureKeyID: pkg.SignatureKeyID, ActiveSignaturePublicKey: publicKey, ActiveSignatureFingerprint: pkg.SignatureFingerprint, RuntimeKind: runtime.Kind, RuntimeABI: runtime.ABI, HostScope: runtime.HostScope, DesiredLifecycle: "enabled", CurrentLifecycle: "active", CleanupPolicyJSON: `{}`, StateVersion: 1, InstalledAt: now, UpdatedAt: now}
	operation := pluginTargetNormalizationOperation("install-"+pluginID, pluginID, "", "succeeded", now)
	operation.Kind = "install"
	if err := store.InstallPlugin(t.Context(), PluginInstallTransaction{Package: pkg, Artifacts: artifacts, Installed: installed, Operation: operation, Audit: AuditEventRow{ID: operation.ID, ActorID: "admin", Action: "plugin.install", TargetKind: "plugin", TargetID: pluginID, Result: "success", MetadataJSON: `{}`, CreatedAt: now}}); err != nil {
		t.Fatal(err)
	}
	instance := pluginTargetNormalizationInstance("instance-"+pluginID, pluginID, now)
	instance.StateVersion, instance.TargetJSON = 0, targets
	consumptionStoreTargets(t, store, &instance, targets)
	return instance
}

func consumptionStoreTargets(t *testing.T, store *GormStore, instance *PluginInstanceRow, targets string) {
	t.Helper()
	instance.TargetJSON = targets
	if instance.StateVersion != 0 {
		instance.ConfigVersion++
	}
	op := pluginTargetNormalizationOperation(fmt.Sprintf("configure-%s-%d", instance.ID, instance.ConfigVersion), instance.PluginID, instance.ID, "succeeded", time.Now().UTC())
	installed, found, err := store.GetInstalledPlugin(t.Context(), instance.PluginID)
	if err != nil || !found {
		t.Fatalf("installed plugin unavailable: %+v %v", installed, err)
	}
	if err := store.ApplyPluginMutation(t.Context(), PluginMutation{PluginID: instance.PluginID, Installed: &installed, ExpectedStateVersion: installed.StateVersion, ReplaceInstance: instance, ExpectedInstanceVersion: instance.StateVersion, Operation: op, Audit: AuditEventRow{ID: op.ID, ActorID: "admin", Action: "plugin.configure", TargetKind: "plugin", TargetID: instance.PluginID, Result: "success", MetadataJSON: `{}`, CreatedAt: op.CreatedAt}}); err != nil {
		t.Fatal(err)
	}
	got, found, err := store.GetPluginInstance(t.Context(), instance.ID)
	if err != nil || !found {
		t.Fatalf("stored instance unavailable: %+v %v", got, err)
	}
	*instance = got
}

func consumptionWritableCache(t *testing.T, store *GormStore) {
	t.Helper()
	// No package processes run in this Store test. Use the cache owner's
	// teardown for the test-owned root, including Windows immutable ACLs.
	t.Cleanup(func() {
		root := filepath.Join(store.dataRoot, "plugins", "packages")
		if _, err := os.Stat(root); errors.Is(err, os.ErrNotExist) {
			return
		}
		if err := marketplace.DiscardVerifiedCacheRoot(root); err != nil {
			t.Errorf("discard fixture cache: %v", err)
		}
	})
}

func consumptionStoreDataset(t *testing.T, store *GormStore, prefix string) DatasetVersionRow {
	t.Helper()
	source := sdk.DatasetSource{ID: "regions", Name: "Regions", Format: sdk.DatasetFormatCIDR}
	encodedSource, _ := json.Marshal(source)
	if err := store.PutDatasetSource(t.Context(), DatasetSourceRow{ID: source.ID, ResourceGroupID: "default", SourceJSON: string(encodedSource), RetrievalJSON: `{}`}); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(datasets.CIDRDocument{Schema: datasets.CIDRSchema, Classifications: []datasets.CIDRClassification{{Name: "cn-44", Kind: sdk.DatasetClassificationRegion, DisplayName: "广东省", CIDRs: []string{prefix}}}})
	sum := sha256.Sum256(data)
	index, err := datasets.Compile(t.Context(), datasets.Input{Source: source, Revision: prefix, FetchedAt: "2026-09-05T00:00:00Z", ExpectedDigest: "sha256:" + hex.EncodeToString(sum[:]), Data: data}, datasets.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := index.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	row, err := store.StoreDatasetVersion(t.Context(), index.Version(), encoded)
	if err != nil {
		t.Fatal(err)
	}
	return row
}

func consumptionStoreBind(t *testing.T, store *GormStore, instance, digest string, revision uint64, targets sdk.ExecutionTargetSelection) PluginConsumptionOperationRow {
	t.Helper()
	request := sdk.DatasetBindingRequest{Action: sdk.DatasetBindingBind, OperationID: fmt.Sprintf("binding-%s-%d", instance, revision), InstanceID: instance, SourceID: "regions", ExpectedRevision: revision - 1, Targets: targets}
	if revision > 1 {
		request.Action = sdk.DatasetBindingReplace
	}
	row := PluginDatasetConsumptionRow{InstanceID: instance, SourceID: "regions", Revision: revision}
	response := sdk.DatasetBindingResponse{OperationID: request.OperationID, InstanceID: instance, SourceID: "regions", Revision: revision, Targets: []sdk.DatasetBindingTargetStatus{}}
	if digest == "" {
		request.Action = sdk.DatasetBindingUnbind
	} else {
		request.Spec = &sdk.DatasetBindingSpec{VersionDigest: digest, Classifications: []sdk.DatasetClassification{{Name: "cn-44", Kind: sdk.DatasetClassificationRegion}}}
		response.Desired = &sdk.DatasetBindingRecord{InstanceID: instance, SourceID: "regions", Revision: revision, Targets: targets, Spec: *request.Spec}
		encoded, err := json.Marshal(response.Desired)
		if err != nil {
			t.Fatal(err)
		}
		row.RecordJSON = string(encoded)
	}
	requestDigest, err := sdk.DatasetBindingRequestDigest(request)
	if err != nil {
		t.Fatal(err)
	}
	op := PluginConsumptionOperationRow{ID: request.OperationID, Kind: sdk.HostRuntimeDatasetBinding, RequestDigest: requestDigest, ResourceGroupID: "default"}
	if err := store.SecurityTransaction(t.Context(), func(tx *GormStore) error {
		if err := tx.PutPluginDatasetConsumption(t.Context(), row); err != nil {
			return err
		}
		previous, err := tx.ListInstanceDatasetBindings(t.Context(), instance, "regions")
		if err != nil {
			return err
		}
		for _, old := range previous {
			if err := tx.RemoveDatasetBinding(t.Context(), "regions", old.AgentID, instance); err != nil {
				return err
			}
		}
		nodes, err := tx.ListAgents(t.Context())
		if err != nil {
			return err
		}
		candidates := map[string]bool{tx.LocalAgentID(): true}
		for _, node := range nodes {
			candidates[node.ID] = true
		}
		selected := []string{}
		for node := range candidates {
			bindings, err := tx.ResolveDatasetBindings(t.Context(), node)
			if err != nil {
				return err
			}
			for _, binding := range bindings {
				if binding.InstanceID != instance || binding.SourceID != "regions" {
					continue
				}
				binding.Revision = int64(revision)
				if err := tx.PutDatasetBinding(t.Context(), binding); err != nil {
					return err
				}
				selected = append(selected, node)
				response.Targets = append(response.Targets, sdk.DatasetBindingTargetStatus{AgentID: node, State: "pending", Desired: request.Spec})
			}
		}
		sort.Strings(selected)
		if err := response.ValidateFor(request); err != nil {
			return err
		}
		encoded, err := json.Marshal(response)
		if err != nil {
			return err
		}
		resolved, err := json.Marshal(selected)
		if err != nil {
			return err
		}
		op.ResponseJSON, op.TargetsJSON = string(encoded), string(resolved)
		return tx.PutPluginConsumptionOperation(t.Context(), op)
	}); err != nil {
		t.Fatal(err)
	}
	persisted, err := store.GetPluginDatasetConsumption(t.Context(), instance, "regions")
	if err != nil || persisted != row {
		t.Fatalf("binding write changed its revision or logical intent: %+v err=%v", persisted, err)
	}
	if persisted.RecordJSON != "" {
		var record sdk.DatasetBindingRecord
		if err := json.Unmarshal([]byte(persisted.RecordJSON), &record); err != nil || record.Validate() != nil || record.Revision != persisted.Revision {
			t.Fatalf("record and row revisions diverged: %+v row=%+v err=%v", record, persisted, err)
		}
	}
	return op
}
