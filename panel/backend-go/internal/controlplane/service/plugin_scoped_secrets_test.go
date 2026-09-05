//go:build !fast && !integration

package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/coordinator"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/pluginhost"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/secrets"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func scopedSecretFixture(t *testing.T, roots ...string) (*PluginCapabilityManager, *storage.GormStore, pluginhost.Candidate, pluginsdk.ScopedSecretRequest) {
	t.Helper()
	root := t.TempDir()
	if len(roots) != 0 {
		root = roots[0]
	}
	store, err := storage.NewStore(storage.StoreConfig{Driver: "sqlite", DataRoot: root, LocalAgentID: "local", TrafficStatsEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	vault, err := secrets.NewVault(store, secrets.Keyring{CurrentKeyID: "test", Keys: map[string][]byte{"test": []byte("0123456789abcdef0123456789abcdef")}})
	if err != nil {
		t.Fatal(err)
	}
	candidate := pluginhost.Candidate{InstanceID: "instance-a", ResourceGroupID: "default", Identity: pluginhost.Identity{PluginID: "plugin-a", Generation: "generation-a"},
		Grants:         []string{pluginsdk.PermissionScopedSecretRead, pluginsdk.PermissionScopedSecretWrite},
		GrantSelectors: map[string][]string{pluginsdk.PermissionScopedSecretRead: {"secret-scope:outbound-auth"}, pluginsdk.PermissionScopedSecretWrite: {"outbound-auth"}}}
	material, err := pluginsdk.NewManagedSecretMaterial([]byte("sensitive-test-material"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(material.Close)
	request := pluginsdk.ScopedSecretRequest{Action: pluginsdk.ScopedSecretCreate,
		Binding:   pluginsdk.ManagedBinding{InstanceID: candidate.InstanceID, Generation: candidate.Identity.Generation, EntryID: candidate.InstanceID},
		Reference: pluginsdk.ScopedSecretReference{InstanceID: candidate.InstanceID, ID: "upstream-a", Scope: "outbound-auth"}, Material: material}
	return &PluginCapabilityManager{store: store, secretVault: vault}, store, candidate, request
}

func TestScopedSecretAuthenticatedAgentRedemption(t *testing.T) {
	root := t.TempDir()
	manager, store, candidate, create := scopedSecretFixture(t, root)
	created, failure := callScopedSecret(t, manager, candidate, create)
	if failure != nil {
		t.Fatal(failure)
	}
	db, err := gorm.Open(sqlite.Open(filepath.Join(root, "panel.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	defer sqlDB.Close()
	digest, artifact, identity, provider := strings.Repeat("a", 64), strings.Repeat("b", 64), strings.Repeat("c", 64), strings.Repeat("d", 64)
	runtimeGeneration := "generation-7-" + strings.Repeat("e", 16)
	rows := []any{
		&storage.PluginPackageRow{Identity: identity, Digest: digest, PluginID: candidate.Identity.PluginID, Version: "1.0.0"},
		&storage.InstalledPluginRow{PluginID: candidate.Identity.PluginID, ActivePackageIdentity: identity, ActivePackageDigest: digest, CurrentLifecycle: "active", LastOperationID: "operation-a"},
		&storage.PluginInstanceRow{ID: candidate.InstanceID, PluginID: candidate.Identity.PluginID, ResourceGroupID: "default", TargetJSON: `["agent-a"]`, ConfigVersion: 1, DesiredEnabled: true},
		&storage.PluginOperationRow{ID: "operation-a", PluginID: candidate.Identity.PluginID, Status: "succeeded"},
		&storage.PluginAgentRuntimeStatusRow{OperationID: "operation-a", AgentID: "agent-a", InstanceID: candidate.InstanceID, PluginID: candidate.Identity.PluginID,
			Revision: 7, GenerationID: provider, PackageDigest: digest, ArtifactDigest: artifact, ResourceGroupID: "default", ConfigVersion: 1, TargetVersion: 1, AuthoritySlot: "active", State: "active"},
		&storage.PluginGrantRow{ID: "read-grant", GrantKey: "read-grant", PluginID: candidate.Identity.PluginID, PackageIdentity: identity, PackageDigest: digest,
			Permission: pluginsdk.PermissionScopedSecretRead, ResourceSelector: "secret-scope:outbound-auth"},
	}
	for _, row := range rows {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now().UTC()
	snapshotBytes := []byte(`{"revision":7}`)
	snapshotSum := sha256.Sum256(snapshotBytes)
	snapshotHash := hex.EncodeToString(snapshotSum[:])
	if err := store.CreateRevisionLedger(t.Context(), storage.RevisionLedgerWrite{
		Operation: storage.OperationRow{ID: "revision-operation", Kind: "test", Status: storage.OperationStatusPending, PrimaryAgentID: "agent-a", CreatedAt: now, UpdatedAt: now},
		Revisions: []storage.AgentRevisionRow{{AgentID: "agent-a", Revision: 7, OperationID: "revision-operation", State: storage.AgentRevisionStatePending, SnapshotArtifactID: "snapshot-" + snapshotHash, SnapshotDigest: snapshotHash, ApplyTimeoutSeconds: 60, DrainTimeoutSeconds: 5, CreatedAt: now, UpdatedAt: now}},
		Artifacts: []storage.GenerationArtifactRow{{ID: "snapshot-" + snapshotHash, Kind: "agent_snapshot", SHA256: snapshotHash, Payload: snapshotBytes, SizeBytes: int64(len(snapshotBytes)), CreatedAt: now}},
		Pointers:  []storage.AgentRevisionPointerRow{{AgentID: "agent-a", DesiredRevision: 7, UpdatedAt: now}},
	}); err != nil {
		t.Fatal(err)
	}
	coord, err := coordinator.New(store, coordinator.Options{})
	if err != nil {
		t.Fatal(err)
	}
	claim, err := coord.Claim(t.Context(), "agent-a")
	if err != nil || claim.Lease == nil {
		t.Fatal("actual coordinator claim", err)
	}
	api := NewRevisionAPI(store, coord)
	service := NewPluginService(store, t.TempDir())
	service.SetSecretVault(manager.secretVault)
	read := pluginsdk.ScopedSecretRequest{Action: pluginsdk.ScopedSecretRead, Reference: created.Reference,
		Binding: pluginsdk.ManagedBinding{InstanceID: candidate.InstanceID, EntryID: candidate.InstanceID, Generation: runtimeGeneration}}
	wire, _ := pluginsdk.EncodeScopedSecretRequest(read)
	defer clear(wire)
	request := PluginSecretRedemptionRequest{Revision: 7, GenerationID: provider, RuntimeGenerationID: runtimeGeneration, InstanceID: candidate.InstanceID, PluginID: candidate.Identity.PluginID,
		OperationID: "operation-a", PackageDigest: digest, ArtifactDigest: artifact, Scoped: wire}
	if _, err := service.RedeemAgentPluginSecrets(t.Context(), "agent-a", request); err == nil {
		t.Fatal("secret delivered before actual runtime binding")
	}
	start := RemoteRevisionStart{AgentID: "agent-a", Revision: 7, RetryCycle: claim.Lease.RetryCycle, Attempt: claim.Lease.Attempt, LeaseID: claim.Lease.LeaseID,
		GenerationID: "attempt-generation-7", RuntimeGenerationID: runtimeGeneration, RuntimeSnapshotHash: strings.Repeat("e", 64)}
	if _, err := api.StartRemoteRevision(t.Context(), "agent-a", start); err != nil {
		t.Fatal("actual remote Start", err)
	}
	if _, err := api.StartRemoteRevision(t.Context(), "agent-a", start); err != nil {
		t.Fatal("same remote Start retry", err)
	}
	conflict := start
	conflict.RuntimeSnapshotHash = strings.Repeat("c", 64)
	conflict.RuntimeGenerationID = "generation-7-" + strings.Repeat("c", 16)
	if _, err := api.StartRemoteRevision(t.Context(), "agent-a", conflict); err == nil {
		t.Fatal("remote runtime binding changed after Start")
	}
	for _, test := range []string{"foreign-agent", "provider-as-runtime", "unknown-runtime", "old-provider", "bad-revision", "wrong-package", "mixed-handles"} {
		t.Run(test, func(t *testing.T) {
			changed, agentID := request, "agent-a"
			switch test {
			case "foreign-agent":
				agentID = "agent-b"
			case "provider-as-runtime":
				changed.RuntimeGenerationID = provider
			case "unknown-runtime":
				changed.RuntimeGenerationID = "generation-7-ffffffffffffffff"
			case "old-provider":
				changed.GenerationID = strings.Repeat("f", 64)
			case "bad-revision":
				changed.Revision = 8
			case "wrong-package":
				changed.PackageDigest = strings.Repeat("f", 64)
			case "mixed-handles":
				changed.Handles = []storage.PluginGenerationSecretHandle{{ID: "extra"}}
			}
			if _, err := service.RedeemAgentPluginSecrets(t.Context(), agentID, changed); err == nil {
				t.Fatal("unauthorized redemption succeeded")
			}
		})
	}
	response, err := service.RedeemAgentPluginSecrets(t.Context(), "agent-a", request)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(response.Scoped)
	decoded, err := pluginsdk.DecodeScopedSecretResponse(read, response.Scoped)
	if err != nil {
		t.Fatal(err)
	}
	defer decoded.Material.Close()
	if err := decoded.Material.WithBytes(func(value []byte) error {
		if !bytes.Equal(value, []byte("sensitive-test-material")) {
			t.Fatal("authenticated delivery changed material")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&storage.PluginGrantRow{}).Where("id = ?", "read-grant").Update("resource_selector", "secret-scope:other").Error; err != nil {
		t.Fatal(err)
	}
	if _, err := service.RedeemAgentPluginSecrets(t.Context(), "agent-a", request); err == nil {
		t.Fatal("revoked purpose grant still delivered")
	}
}

type scopedSecretTaskSession struct {
	tasks  *TaskService
	check  func(TaskEnvelope)
	badACK bool
}

func (session *scopedSecretTaskSession) Close() error { return nil }
func (session *scopedSecretTaskSession) SendTask(envelope TaskEnvelope) error {
	session.check(envelope)
	fence := envelope.Payload["fence_id"]
	if session.badACK {
		fence = "wrong-fence"
	}
	return session.tasks.ApplyUpdate(context.Background(), TaskUpdateInput{AgentID: "agent-a", TaskID: envelope.ID, State: "completed",
		Result: map[string]any{"generation_id": envelope.Payload["generation_id"], "fence_id": fence, "revoked": true}})
}

func TestScopedSecretRotationWaitsForExactFenceAndRecovers(t *testing.T) {
	manager, store, candidate, request := scopedSecretFixture(t)
	created, failure := callScopedSecret(t, manager, candidate, request)
	if failure != nil {
		t.Fatal(failure)
	}
	read := request
	read.Action, read.Reference, read.Material = pluginsdk.ScopedSecretRead, created.Reference, nil
	// The same transactional delivery path is used after the Agent's outer
	// revision authentication. This test focuses on durable coordinator ordering.
	if err := store.SecurityTransaction(t.Context(), func(tx *storage.GormStore) error {
		vault, _ := manager.secretVault.WithStore(tx)
		response, err := executePluginScopedSecret(t.Context(), tx, vault, candidate, read, scopedSecretRecipient{AgentID: "agent-a", ProviderGenerationID: strings.Repeat("a", 64), Revision: 7})
		response.Material.Close()
		return err
	}); err != nil {
		t.Fatal(err)
	}
	request.Action, request.Reference = pluginsdk.ScopedSecretRotate, created.Reference
	if _, failure := callScopedSecret(t, manager, candidate, request); failure == nil || failure.Code != pluginsdk.ErrorUnavailable || !failure.Retryable {
		t.Fatal("offline recipient was acknowledged as revoked")
	}
	if _, failure := callScopedSecret(t, manager, candidate, read); failure == nil {
		t.Fatal("new material delivery admitted during pending revocation")
	}
	row, _, _ := store.GetSecretByName(t.Context(), pluginScopedSecretName(candidate, created.Reference))
	if row.ActiveVersion != 1 {
		t.Fatal("version rotated before recipient acknowledgement")
	}
	// Restart the manager: only persisted reference/intent/recipient metadata
	// remains. A wrong ACK must not release the pending operation.
	manager = &PluginCapabilityManager{store: store, secretVault: manager.secretVault}
	tasks := NewTaskService(TaskServiceConfig{TaskTTL: time.Second})
	t.Cleanup(func() { _ = tasks.Close() })
	manager.SetTaskService(tasks)
	var expectedFence string
	session := &scopedSecretTaskSession{tasks: tasks, badACK: true, check: func(envelope TaskEnvelope) {
		if envelope.Type != TaskTypePluginGenerationRevoke || envelope.Payload["instance_id"] != candidate.InstanceID || envelope.Payload["generation_id"] != candidate.Identity.Generation || envelope.Payload["provider_generation_id"] != strings.Repeat("a", 64) {
			t.Fatal("revocation command lost its exact identity")
		}
		fence, _ := envelope.Payload["fence_id"].(string)
		if expectedFence != "" && expectedFence != fence {
			t.Fatal("retry replaced the durable fence")
		}
		expectedFence = fence
		// A competing storage transaction during dispatch proves the network
		// wait does not hold the database writer transaction.
		if err := store.SecurityTransaction(t.Context(), func(tx *storage.GormStore) error {
			blocked, err := tx.ScopedSecretReadBlocked(t.Context(), pluginScopedSecretName(candidate, created.Reference), "agent-a", candidate.InstanceID, candidate.Identity.Generation)
			if !blocked {
				t.Fatal("pending fence is not durable before dispatch")
			}
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}}
	if err := tasks.RegisterSession(TaskSessionRegistration{AgentID: "agent-a", SessionID: "session-a", Session: session}); err != nil {
		t.Fatal(err)
	}
	if _, failure := callScopedSecret(t, manager, candidate, request); failure == nil {
		t.Fatal("wrong fence acknowledgement released rotation")
	}
	session.badACK = false
	rotated, failure := callScopedSecret(t, manager, candidate, request)
	if failure != nil || rotated.Reference.Version == created.Reference.Version {
		t.Fatalf("acknowledged retry did not complete: %v", failure)
	}
	// Lose the successful response and reconnect under a new generation.
	candidate.Identity.Generation = "generation-b"
	request.Binding.Generation = candidate.Identity.Generation
	replayed, failure := callScopedSecret(t, manager, candidate, request)
	if failure != nil || replayed.Reference != rotated.Reference {
		t.Fatal("lost rotation response could not recover the original result")
	}
	read.Binding.Generation = candidate.Identity.Generation
	if _, failure := callScopedSecret(t, manager, candidate, read); failure == nil {
		t.Fatal("old version remained readable after acknowledged rotation")
	}
	read.Reference = rotated.Reference
	if _, failure := callScopedSecret(t, manager, candidate, read); failure != nil {
		t.Fatal("new generation could not use the rotated reference")
	}
}

func callScopedSecret(t *testing.T, manager *PluginCapabilityManager, candidate pluginhost.Candidate, request pluginsdk.ScopedSecretRequest) (pluginsdk.ScopedSecretResponse, *pluginsdk.RuntimeError) {
	t.Helper()
	wire, err := pluginsdk.EncodeScopedSecretRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(wire)
	response := manager.DispatchPluginHostResource(t.Context(), candidate, pluginsdk.HostRuntimeCall{Operation: pluginsdk.HostRuntimeScopedSecret, Payload: wire})
	defer clear(response.Payload)
	if response.Error != nil {
		return pluginsdk.ScopedSecretResponse{}, response.Error
	}
	decoded, err := pluginsdk.DecodeScopedSecretResponse(request, response.Payload)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(decoded.Material.Close)
	return decoded, nil
}

func TestScopedSecretVaultLifecycle(t *testing.T) {
	manager, store, candidate, request := scopedSecretFixture(t)
	created, failure := callScopedSecret(t, manager, candidate, request)
	if failure != nil {
		t.Fatal(failure)
	}
	if replayed, failure := callScopedSecret(t, manager, candidate, request); failure != nil || replayed.Reference != created.Reference {
		t.Fatal("identical create did not recover its original reference")
	}
	if _, err := json.Marshal(request); err == nil {
		t.Fatal("material acquired an ordinary JSON representation")
	}
	row, found, err := store.GetSecretByName(t.Context(), pluginScopedSecretName(candidate, created.Reference))
	if err != nil || !found {
		t.Fatalf("encrypted vault identity missing: %v", err)
	}
	version, err := store.GetSecretVersion(t.Context(), row.ID, row.ActiveVersion)
	if err != nil || bytes.Contains(version.Ciphertext, []byte("sensitive-test-material")) || len(version.Nonce) == 0 {
		t.Fatal("secret was not encrypted at rest")
	}
	request.Reference = created.Reference
	request.Action = pluginsdk.ScopedSecretRotate
	replaced, failure := callScopedSecret(t, manager, candidate, request)
	if failure != nil || replaced.Reference.Version == created.Reference.Version {
		t.Fatalf("replace failed: %v", failure)
	}
	if replayed, failure := callScopedSecret(t, manager, candidate, request); failure != nil || replayed.Reference != replaced.Reference {
		t.Fatal("identical rotation did not recover its original reference")
	}
	distinct, _ := pluginsdk.NewManagedSecretMaterial([]byte("different-material"))
	defer distinct.Close()
	stale := request
	stale.Material = distinct
	if _, failure := callScopedSecret(t, manager, candidate, stale); failure == nil {
		t.Fatal("distinct stale replacement succeeded")
	}
	request.Action, request.Material = pluginsdk.ScopedSecretRead, nil
	if _, failure := callScopedSecret(t, manager, candidate, request); failure == nil {
		t.Fatal("old version read succeeded")
	}
	request.Reference = replaced.Reference
	read, failure := callScopedSecret(t, manager, candidate, request)
	if failure != nil {
		t.Fatal(failure)
	}
	if err := read.Material.WithBytes(func(value []byte) error {
		if !bytes.Equal(value, []byte("sensitive-test-material")) {
			t.Fatal("secure delivery changed bytes")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	read.Material.Close()
	// Stored state and audits contain only encrypted rows and safe metadata.
	state, found, err := store.GetPluginRuntimeState(t.Context(), candidate.Identity.PluginID, candidate.InstanceID)
	if err != nil || found || len(state) != 0 {
		t.Fatal("scoped material or response was copied to runtime state")
	}
	audits, err := store.ListAuditEvents(t.Context(), 100)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(audits)
	if bytes.Contains(encoded, []byte("sensitive-test-material")) || bytes.Contains(encoded, []byte("c2Vuc2l0aXZlLXRlc3QtbWF0ZXJpYWw=")) {
		t.Fatal("audit leaked material")
	}
}

func TestScopedSecretAuthorizationAndNoDurableReplay(t *testing.T) {
	manager, _, candidate, request := scopedSecretFixture(t)
	created, failure := callScopedSecret(t, manager, candidate, request)
	if failure != nil {
		t.Fatal(failure)
	}
	request.Action, request.Material, request.Reference = pluginsdk.ScopedSecretRead, nil, created.Reference
	for _, name := range []string{"generation", "entry", "instance", "plugin", "group", "permission", "scope", "empty-scopes", "wildcard-scope"} {
		t.Run(name, func(t *testing.T) {
			changed, call := candidate, request
			switch name {
			case "generation":
				call.Binding.Generation = "old-generation"
			case "entry":
				call.Binding.EntryID = "another-entry"
			case "instance":
				changed.InstanceID = "instance-b"
				call.Binding.InstanceID, call.Binding.EntryID, call.Reference.InstanceID = changed.InstanceID, changed.InstanceID, changed.InstanceID
			case "plugin":
				changed.Identity.PluginID = "plugin-b"
			case "group":
				changed.ResourceGroupID = "another-group"
			case "permission":
				changed.Grants = []string{pluginsdk.PermissionScopedSecretWrite}
			case "scope":
				call.Reference.Scope = "other-purpose"
			case "empty-scopes":
				changed.GrantSelectors = nil
			case "wildcard-scope":
				changed.GrantSelectors = map[string][]string{pluginsdk.PermissionScopedSecretRead: {"*"}}
			}
			if _, failure := callScopedSecret(t, manager, changed, call); failure == nil {
				t.Fatal("unauthorized delivery succeeded")
			} else if strings.Contains(failure.Message, "sensitive") {
				t.Fatal("failure leaked material")
			}
		})
	}
	wire, _ := pluginsdk.EncodeScopedSecretRequest(request)
	defer clear(wire)
	response := manager.DispatchPluginHostResource(t.Context(), candidate, pluginsdk.HostRuntimeCall{Operation: pluginsdk.HostRuntimeScopedSecret, OperationID: "cache-me", Payload: wire})
	if response.Error == nil {
		t.Fatal("secret response admitted to durable operation cache")
	}
}

func TestScopedSecretRevokeDestroysVersionsAndCannotRevive(t *testing.T) {
	manager, store, candidate, request := scopedSecretFixture(t)
	created, failure := callScopedSecret(t, manager, candidate, request)
	if failure != nil {
		t.Fatal(failure)
	}
	request.Reference, request.Action, request.Material = created.Reference, pluginsdk.ScopedSecretRevoke, nil
	if _, failure := callScopedSecret(t, manager, candidate, request); failure != nil {
		t.Fatal(failure)
	}
	request.Action = pluginsdk.ScopedSecretRead
	if _, failure := callScopedSecret(t, manager, candidate, request); failure == nil {
		t.Fatal("revoked read succeeded")
	}
	row, _, _ := store.GetSecretByName(t.Context(), pluginScopedSecretName(candidate, request.Reference))
	if row.RetiredAt == nil {
		t.Fatal("revocation was not durable")
	}
	if _, err := store.GetSecretVersion(t.Context(), row.ID, row.ActiveVersion); err == nil {
		t.Fatal("revoked ciphertext still resolvable")
	}
	material, _ := pluginsdk.NewManagedSecretMaterial([]byte("replacement"))
	defer material.Close()
	request.Action, request.Reference.Version, request.Material = pluginsdk.ScopedSecretCreate, "", material
	if _, failure := callScopedSecret(t, manager, candidate, request); failure == nil {
		t.Fatal("create revived a revoked identity")
	}
}

func TestScopedSecretIntentRecoverySurvivesVaultMasterKeyMigration(t *testing.T) {
	manager, store, candidate, request := scopedSecretFixture(t)
	created, failure := callScopedSecret(t, manager, candidate, request)
	if failure != nil {
		t.Fatal(failure)
	}
	vault, err := secrets.NewVault(store, secrets.Keyring{CurrentKeyID: "next", Keys: map[string][]byte{
		"test": []byte("0123456789abcdef0123456789abcdef"), "next": []byte("abcdef0123456789abcdef0123456789")}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := vault.MigrateToCurrentKey(t.Context()); err != nil {
		t.Fatal(err)
	}
	manager = &PluginCapabilityManager{store: store, secretVault: vault}
	replayed, failure := callScopedSecret(t, manager, candidate, request)
	if failure != nil || replayed.Reference != created.Reference {
		t.Fatal("key migration lost the original safe operation result", failure)
	}
}

func TestScopedSecretPendingLocalRevocationPreventsGenerationRestaging(t *testing.T) {
	manager, store, candidate, request := scopedSecretFixture(t)
	created, failure := callScopedSecret(t, manager, candidate, request)
	if failure != nil {
		t.Fatal(failure)
	}
	read := request
	read.Action, read.Reference, read.Material = pluginsdk.ScopedSecretRead, created.Reference, nil
	if _, failure := callScopedSecret(t, manager, candidate, read); failure != nil {
		t.Fatal(failure)
	}
	request.Action, request.Reference = pluginsdk.ScopedSecretRotate, created.Reference
	if _, failure := callScopedSecret(t, manager, candidate, request); failure == nil || failure.Code != pluginsdk.ErrorUnavailable {
		t.Fatal("missing local termination was acknowledged")
	}
	unrelated := request
	unrelated.Action, unrelated.Reference.ID, unrelated.Reference.Version = pluginsdk.ScopedSecretCreate, "another-secret", ""
	if _, failure := callScopedSecret(t, manager, candidate, unrelated); failure == nil {
		t.Fatal("fenced generation created new secret intent")
	}
	old := storage.PluginRuntimeInstanceRow{InstanceID: candidate.InstanceID, PluginID: candidate.Identity.PluginID, CandidateGeneration: candidate.Identity.Generation}
	if err := store.StagePluginRuntime(t.Context(), old); err == nil {
		t.Fatal("fenced generation restaged")
	}
	if err := store.StagePluginRuntimeBatch(t.Context(), []storage.PluginRuntimeInstanceRow{old}); err == nil {
		t.Fatal("fenced generation restaged in batch")
	}
	old.CandidateGeneration = "generation-b"
	if err := store.StagePluginRuntime(t.Context(), old); err != nil {
		t.Fatal("replacement generation incorrectly fenced", err)
	}
}
