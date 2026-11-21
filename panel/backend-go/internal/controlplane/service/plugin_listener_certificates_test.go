//go:build !fast && !integration

package service

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/authz"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/pluginhost"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/secrets"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type listenerTestRuntime struct {
	PluginCapabilityRuntime
	generation string
}

func (r *listenerTestRuntime) ActiveGeneration(string) (string, bool) { return r.generation, true }

type listenerTestAuthorizer struct{}

func (listenerTestAuthorizer) AuthorizeResource(_ context.Context, actor authz.Actor, permission, kind, id string) error {
	if actor.ID == "admin" || (actor.ID == "viewer" && permission == authz.PermissionResourceRead) {
		return nil
	}
	return authz.ErrForbidden
}

func TestListenerCertificateGrantPublishesAcknowledgedRevision(t *testing.T) {
	if testing.Short() {
		t.Skip("durable revision lifecycle")
	}
	root := t.TempDir()
	store, err := newServiceSQLiteStore(t, root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	db, err := gorm.Open(sqlite.Open(filepath.Join(root, "panel.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	conn, _ := db.DB()
	t.Cleanup(func() { conn.Close() })
	const plugin = storage.ListenerCertificatePlugin
	const instance = "ss-instance"
	const agent = "edge-a"
	const domain = "leaf.example"
	for _, row := range []any{
		&storage.PluginInstanceRow{ID: instance, IncarnationID: "inc", PluginID: plugin, ConfigVersion: 1, ResourceGroupID: "default", TargetJSON: `["edge-a"]`, DesiredEnabled: true, ConfigJSON: "{}", StatusSummaryJSON: "{}"},
		&storage.InstalledPluginRow{PluginID: plugin, DesiredLifecycle: "enabled", ActivePackageIdentity: "package", ActivePackageDigest: "digest"},
		&storage.PluginPackageRow{Identity: "package", PluginID: plugin, Digest: "digest", Version: "1.0", ManifestJSON: `{"id":"shadowsocks-server","version":"1.0"}`},
		&storage.PluginGrantRow{ID: "read", GrantKey: "read", PluginID: plugin, PackageIdentity: "package", PackageDigest: "digest", Permission: "certificate.public.read"},
		&storage.PluginGrantRow{ID: "tls", GrantKey: "tls", PluginID: plugin, PackageIdentity: "package", PackageDigest: "digest", Permission: "certificate.public.terminate"},
		&storage.AgentRow{ID: agent, CapabilitiesJSON: `["cert_install"]`},
		&storage.ManagedCertificateRow{ID: 7, Domain: domain, Enabled: true, CertificateType: "uploaded", Usage: "https"},
		&storage.ResourceBindingRow{ID: "cert7", ResourceKind: "certificate", ResourceID: "7", ResourceGroupID: "default"},
		&storage.ResourceBindingRow{ID: "node", ResourceKind: "agent", ResourceID: agent, ResourceGroupID: "default"},
	} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	cert, key, err := generateTestMasterCertificate(domain, time.Now(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveManagedCertificateMaterial(t.Context(), domain, storage.ManagedCertificateBundle{Domain: domain, CertPEM: string(cert), KeyPEM: string(key)}); err != nil {
		t.Fatal(err)
	}
	runtime := &listenerTestRuntime{generation: "gen"}
	plugins := &PluginService{}
	plugins.ConfigureRevisionMutations(config.Default(), store)
	manager := &PluginCapabilityManager{store: store, runtime: runtime, resourceAuthorizer: listenerTestAuthorizer{}, plugins: plugins}
	ctx := storage.WithQuotaActor(t.Context(), storage.QuotaActor{UserID: "admin"})
	actor := authz.Actor{ID: "admin"}
	if _, _, _, err := manager.BeginListenerCertificateGrant(ctx, authz.Actor{ID: "viewer"}, instance, "gen", agent, "", 7); !errors.Is(err, authz.ErrForbidden) {
		t.Fatalf("viewer create %v", err)
	}
	token, ref, end, err := manager.BeginListenerCertificateGrant(ctx, actor, instance, "gen", agent, "", 7)
	if err != nil {
		t.Fatal(err)
	}
	defer end()
	candidate := pluginhost.Candidate{InstanceID: instance, IncarnationID: "inc", ResourceGroupID: "default", Identity: pluginhost.Identity{PluginID: plugin, Generation: "gen"}}
	t.Run("scoped metadata recovery", func(t *testing.T) {
		const scope = "shadowsocks-inbound"
		const id = "ss-password-recovery"
		if err := db.Create(&storage.PluginGrantRow{ID: "scoped", GrantKey: "scoped", PluginID: plugin, PackageIdentity: "package", PackageDigest: "digest", Permission: pluginsdk.PermissionScopedSecretRead, ResourceSelector: "secret-scope:" + scope}).Error; err != nil {
			t.Fatal(err)
		}
		manager.secretVault, err = secrets.NewVault(store, secrets.Keyring{CurrentKeyID: "test", Keys: map[string][]byte{"test": []byte("0123456789abcdef0123456789abcdef")}})
		if err != nil {
			t.Fatal(err)
		}
		c := candidate
		c.Grants = []string{pluginsdk.PermissionScopedSecretRead, pluginsdk.PermissionScopedSecretWrite}
		c.GrantSelectors = map[string][]string{pluginsdk.PermissionScopedSecretRead: {scope}, pluginsdk.PermissionScopedSecretWrite: {scope}}
		check := func(want string) {
			t.Helper()
			data, err := manager.listenerScopedSecretVersion(ctx, c, id)
			if err != nil || data["found"] != true || data["value"] != want || len(data) != 2 {
				t.Fatal("lookup returned incorrect metadata", err)
			}
		}
		check("")
		material, err := pluginsdk.NewManagedSecretMaterial([]byte("synthetic-test-password"))
		if err != nil {
			t.Fatal(err)
		}
		defer material.Close()
		create := pluginsdk.ScopedSecretRequest{Action: pluginsdk.ScopedSecretCreate, Binding: pluginsdk.ManagedBinding{InstanceID: instance, EntryID: instance, Generation: "gen"}, Reference: pluginsdk.ScopedSecretReference{InstanceID: instance, ID: id, Scope: scope}, Material: material}
		created, failure := callScopedSecret(t, manager, c, create)
		if failure != nil {
			t.Fatal(failure)
		}
		check(created.Reference.Version)
		wrong := c
		wrong.ResourceGroupID = "other"
		if _, err := manager.listenerScopedSecretVersion(ctx, wrong, id); err == nil {
			t.Fatal("cross-group lookup allowed")
		}
		wrong = c
		wrong.Grants = nil
		if _, err := manager.listenerScopedSecretVersion(ctx, wrong, id); err == nil {
			t.Fatal("missing scoped read grant allowed")
		}
		create.Material = nil
		create.Action = pluginsdk.ScopedSecretRevoke
		create.Reference = created.Reference
		if _, failure := callScopedSecret(t, manager, c, create); failure != nil {
			t.Fatal(failure)
		}
		check("")
	})
	put := func(candidate pluginhost.Candidate, token string, entries ...storage.ListenerCertificateRef) error {
		type entry struct {
			storage.ListenerCertificateRef
			Protocol string `json:"protocol"`
		}
		rows := make([]entry, 0, len(entries))
		for _, e := range entries {
			rows = append(rows, entry{e, "anytls"})
		}
		value, _ := json.Marshal(rows)
		payload, _ := json.Marshal(pluginHostStatePayload{ListenerGrant: token, Key: "listens", Value: value})
		_, err := manager.pluginHostStatePut(t.Context(), candidate, payload)
		return err
	}
	changed := ref
	changed.Domain = "different.example"
	if err := put(candidate, token, changed); err == nil {
		t.Fatal("different certificate domain accepted")
	}
	stale := candidate
	stale.Identity.Generation = "stale"
	if err := put(stale, token, ref); err == nil {
		t.Fatal("different generation accepted")
	}
	if err := put(candidate, token, ref); err != nil {
		t.Fatal(err)
	}
	pointer, ok, err := store.GetAgentRevisionPointer(ctx, agent)
	if err != nil || !ok || pointer.DesiredRevision < 1 {
		t.Fatal("missing desired certificate revision", err)
	}
	checkReady := func(want bool) {
		t.Helper()
		payload, _ := json.Marshal(pluginHostStatePayload{Key: listenerCertificateReadyPrefix + ref.ID})
		response, err := manager.pluginHostStateGet(ctx, candidate, payload)
		if err != nil || response["found"] != true || response["value"] != want {
			t.Fatalf("ready %v err %v", response, err)
		}
	}
	checkReady(false)
	claim, err := store.ClaimLatestAgentRevision(ctx, storage.CoordinatorClaimRequest{AgentID: agent, LeaseID: "lease", Now: time.Now()})
	if err != nil || claim.Lease == nil {
		t.Fatal("claim", err)
	}
	if _, err := store.StartAgentRevisionAttempt(ctx, storage.CoordinatorStartRequest{Lease: *claim.Lease, GenerationID: "applied-gen", Now: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ApplyAgentRevisionAttempt(ctx, storage.CoordinatorApplyRequest{Lease: *claim.Lease, GenerationID: "applied-gen", Now: time.Now()}); err != nil {
		t.Fatal(err)
	}
	checkReady(true)
	renewal := ManagedCertificate{ID: 7, Domain: domain, Enabled: true, Scope: "domain", IssuerMode: "master_cf_dns", CertificateType: "acme"}
	certSvc := &certificateService{cfg: config.Config{LocalAgentID: "local"}, store: store}
	if eligible, err := certSvc.isManagedCertificateRenewalTarget(ctx, renewal); err != nil || !eligible {
		t.Fatal("listener-only ACME renewal omitted", err)
	}
	rotatedCert, rotatedKey, err := generateTestMasterCertificate(domain, time.Now(), 2)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.StageManagedCertificateGeneration(ctx, domain, storage.ManagedCertificateBundle{Domain: domain, CertPEM: string(rotatedCert), KeyPEM: string(rotatedKey)}); err != nil {
		t.Fatal(err)
	}
	pending, err := OverlayPendingManagedCertificateGenerationsForConfig(ctx, config.Default(), store, agent, storage.Snapshot{})
	checkReady(false) // the newly staged material has not been acknowledged
	if err != nil || len(pending.Certificates) != 1 || !pending.Certificates[0].ListenerBound || pending.Certificates[0].CertPEM != string(rotatedCert) {
		t.Fatal("rotation not sent to selected node", err)
	}
	end()
	if err := put(candidate, token); err == nil {
		t.Fatal("expired grant removed binding")
	}
	deletion, _, finish, err := manager.BeginListenerCertificateGrant(ctx, actor, instance, "gen", agent, ref.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer finish()
	if err := put(candidate, deletion); err != nil {
		t.Fatal(err)
	}
	pending, err = OverlayPendingManagedCertificateGenerationsForConfig(ctx, config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, store, "local", storage.Snapshot{})
	if err != nil || len(pending.Certificates) != 0 {
		t.Fatal("default revision target received released key", err)
	}
	if _, err := store.CopyLastKnownGoodCoordinatorRevision(ctx, storage.CoordinatorRollbackRequest{AgentID: agent, OperationID: "forbidden-rollback", Now: time.Now()}); err == nil {
		t.Fatal("rollback restored a released private key")
	}
	next, ok, err := store.GetAgentRevisionPointer(ctx, agent)
	if err != nil || !ok || next.DesiredRevision <= pointer.DesiredRevision {
		t.Fatal("missing removal revision", err)
	}
	snapshot, err := store.LoadAgentSnapshot(ctx, agent, storage.AgentSnapshotInput{})
	if err != nil || len(snapshot.Certificates) != 0 {
		t.Fatal("deleted listener still distributes certificate", err)
	}
}
