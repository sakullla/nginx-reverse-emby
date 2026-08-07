package storage

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/marketplace"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/plugins"
)

func TestPluginDurableRowsSurviveDefaultMigration(t *testing.T) {
	ctx := context.Background()
	source, err := NewSQLiteStore(t.TempDir(), "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = source.Close() })
	target, err := NewSQLiteStore(t.TempDir(), "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = target.Close() })
	now := time.Now().UTC().Truncate(time.Millisecond)
	custom, err := marketplace.NewCustomSource("community", "Community", "https://example.com/plugins.git", "main", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := marketplace.Snapshot{ID: "snapshot-1", SourceID: custom.ID, Commit: "commit-1", Path: "snapshot/path", ValidatedAt: now, Entries: []plugins.MarketEntry{{ID: "example.plugin", Version: "1.0.0", PackagePath: "plugins/example.plugin/1.0.0", PackageSHA256: pluginTestDigest("a")}}}
	if err := source.PromoteSnapshot(ctx, custom, snapshot); err != nil {
		t.Fatal(err)
	}
	finished := now.Add(time.Second)
	if err := source.SaveRefreshOperation(ctx, marketplace.RefreshOperation{ID: "refresh-1", SourceID: custom.ID, Commit: snapshot.Commit, Status: "succeeded", StartedAt: now, FinishedAt: &finished}); err != nil {
		t.Fatal(err)
	}
	install := PluginInstallTransaction{
		Package:   PluginPackageRow{Digest: pluginTestDigest("a"), PluginID: "example.plugin", Version: "1.0.0", CachePath: "packages/a", ManifestJSON: "{}", ConfigSchemaJSON: `{"type":"object"}`, VerifiedAt: now},
		Installed: InstalledPluginRow{PluginID: "example.plugin", ActivePackageDigest: pluginTestDigest("a"), DesiredLifecycle: "disabled", CurrentLifecycle: "disabled", CleanupPolicyJSON: "{}", LastOperationID: "op-install", InstalledAt: now, UpdatedAt: now},
		Grants:    []PluginGrantRow{{ID: "grant-1", PluginID: "example.plugin", PackageDigest: pluginTestDigest("a"), Permission: "http.inspect", GrantedBy: "admin", GrantedAt: now}},
		Operation: PluginOperationRow{ID: "op-install", PluginID: "example.plugin", Kind: "install", Status: "succeeded", TargetPackageDigest: pluginTestDigest("a"), AgentResultsJSON: "{}", ActorID: "admin", CreatedAt: now, CompletedAt: &now},
		Audit:     AuditEventRow{ID: "audit-install", ActorID: "admin", Action: "plugin.install", TargetKind: "plugin", TargetID: "example.plugin", Result: "success", MetadataJSON: "{}", CreatedAt: now},
	}
	if err := source.InstallPlugin(ctx, install); err != nil {
		t.Fatal(err)
	}
	instance := PluginInstanceRow{ID: "instance-1", PluginID: "example.plugin", ResourceGroupID: "default", TargetJSON: "[]", ConfigJSON: "{}", PendingConfigJSON: "", StatusSummaryJSON: "{}", CurrentState: "disabled", UpdatedAt: now}
	installed := install.Installed
	installed.LastOperationID = "op-configure"
	if err := source.ApplyPluginMutation(ctx, PluginMutation{PluginID: installed.PluginID, ExpectedActive: installed.ActivePackageDigest, Installed: &installed, ReplaceInstance: &instance, Operation: PluginOperationRow{ID: "op-configure", PluginID: installed.PluginID, Kind: "configure", Status: "succeeded", AgentResultsJSON: "{}", ActorID: "admin", CreatedAt: now}, Audit: AuditEventRow{ID: "audit-configure", ActorID: "admin", Action: "plugin.configure", TargetKind: "plugin", TargetID: installed.PluginID, Result: "success", MetadataJSON: "{}", CreatedAt: now}}); err != nil {
		t.Fatal(err)
	}

	if err := CopyDefaultMigrationRows(ctx, source, target); err != nil {
		t.Fatal(err)
	}
	if migrated, ok, err := target.GetInstalledPlugin(ctx, installed.PluginID); err != nil || !ok || migrated.ActivePackageDigest != installed.ActivePackageDigest {
		t.Fatalf("migrated installed plugin = %+v, %v, %v", migrated, ok, err)
	}
	if migrated, ok, err := target.GetPluginInstance(ctx, instance.ID); err != nil || !ok || migrated.PluginID != installed.PluginID {
		t.Fatalf("migrated plugin instance = %+v, %v, %v", migrated, ok, err)
	}
	if operations, err := target.ListPluginOperations(ctx, installed.PluginID); err != nil || len(operations) != 2 {
		t.Fatalf("migrated operations = %+v, %v", operations, err)
	}
	if current, ok, err := target.CurrentSnapshot(ctx, custom.ID); err != nil || !ok || current.ID != snapshot.ID {
		t.Fatalf("migrated market snapshot = %+v, %v, %v", current, ok, err)
	}
}

func TestDeleteMarketplaceSourcePreservesRefreshHistoryAndInstalledPackage(t *testing.T) {
	ctx := context.Background()
	store, err := NewSQLiteStore(t.TempDir(), "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	source, _ := marketplace.NewCustomSource("community", "Community", "https://example.com/plugins.git", "main", "", 0)
	if err := store.SaveMarketplaceSource(ctx, source); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	stable := marketplace.Snapshot{ID: "stable-snapshot", SourceID: source.ID, Commit: "stable-commit", Path: "stable/path", ValidatedAt: now}
	if err := store.PromoteSnapshot(ctx, source, stable); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveRefreshOperation(ctx, marketplace.RefreshOperation{ID: "refresh-kept", SourceID: source.ID, Status: "failed", ErrorClass: "fetch", Error: "offline", StartedAt: now, FinishedAt: &now}); err != nil {
		t.Fatal(err)
	}
	if updated, ok, err := store.GetMarketplaceSource(ctx, source.ID); err != nil || !ok || updated.LastResult != "failed" || updated.LastError != "offline" {
		t.Fatalf("refresh failure not projected to source: %+v, %v, %v", updated, ok, err)
	}
	if current, ok, err := store.CurrentSnapshot(ctx, source.ID); err != nil || !ok || current.ID != stable.ID || current.Commit != stable.Commit {
		t.Fatalf("refresh failure replaced current snapshot: %+v, %v, %v", current, ok, err)
	}
	if err := store.DeleteMarketplaceSource(ctx, source.ID); err != nil {
		t.Fatal(err)
	}
	var operation MarketplaceRefreshOperationRow
	if err := store.db.WithContext(ctx).Where("id = ?", "refresh-kept").First(&operation).Error; err != nil {
		t.Fatalf("source deletion removed refresh history: %v", err)
	}
	if err := store.DeleteMarketplaceSource(ctx, marketplace.OfficialSourceID); err == nil {
		t.Fatal("official source deletion was accepted")
	}
}

func TestApplyPluginMutationRejectsMismatchedInstalledIdentity(t *testing.T) {
	ctx := context.Background()
	store, err := NewSQLiteStore(t.TempDir(), "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Now().UTC()
	install := PluginInstallTransaction{Package: PluginPackageRow{Digest: pluginTestDigest("a"), PluginID: "one.plugin", Version: "1.0.0", CachePath: "packages/a", ManifestJSON: "{}", ConfigSchemaJSON: "{}", VerifiedAt: now}, Installed: InstalledPluginRow{PluginID: "one.plugin", ActivePackageDigest: pluginTestDigest("a"), DesiredLifecycle: "disabled", CurrentLifecycle: "disabled", CleanupPolicyJSON: "{}", LastOperationID: "install", InstalledAt: now, UpdatedAt: now}, Operation: PluginOperationRow{ID: "install", PluginID: "one.plugin", Kind: "install", Status: "succeeded", AgentResultsJSON: "{}", ActorID: "admin", CreatedAt: now}, Audit: AuditEventRow{ID: "audit", Action: "plugin.install", TargetKind: "plugin", TargetID: "one.plugin", Result: "success", MetadataJSON: "{}", CreatedAt: now}}
	if err := store.InstallPlugin(ctx, install); err != nil {
		t.Fatal(err)
	}
	wrong := install.Installed
	wrong.PluginID = "other.plugin"
	err = store.ApplyPluginMutation(ctx, PluginMutation{PluginID: "one.plugin", Installed: &wrong, Operation: PluginOperationRow{ID: "bad", PluginID: "one.plugin", Kind: "configure", Status: "failed", AgentResultsJSON: "{}", ActorID: "admin", CreatedAt: now}, Audit: AuditEventRow{ID: "bad-audit", Action: "plugin.configure", TargetKind: "plugin", TargetID: "one.plugin", Result: "failure", MetadataJSON: "{}", CreatedAt: now}})
	if err == nil {
		t.Fatal("mismatched installed plugin identity was accepted")
	}
}

func TestMarketplacePromotionAndRefreshCompletionAreAtomic(t *testing.T) {
	ctx := context.Background()
	store, err := NewSQLiteStore(t.TempDir(), "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	source, _ := marketplace.NewCustomSource("atomic", "Atomic", "https://example.com/plugins.git", "main", "", 0)
	now := time.Now().UTC()
	stable := marketplace.Snapshot{ID: "stable", SourceID: source.ID, Commit: "stable-commit", Path: "stable", ValidatedAt: now}
	if err := store.PromoteSnapshot(ctx, source, stable); err != nil {
		t.Fatal(err)
	}
	op := marketplace.RefreshOperation{ID: "refresh-next", SourceID: source.ID, Commit: "next-commit", Status: "running", StartedAt: now}
	if err := store.SaveRefreshOperation(ctx, op); err != nil {
		t.Fatal(err)
	}
	if err := store.db.Exec(`CREATE TRIGGER fail_refresh_completion BEFORE UPDATE OF status ON marketplace_refresh_operations WHEN NEW.status = 'succeeded' BEGIN SELECT RAISE(ABORT, 'injected completion failure'); END`).Error; err != nil {
		t.Fatal(err)
	}
	finished := now.Add(time.Minute)
	op.Status, op.FinishedAt = "succeeded", &finished
	next := marketplace.Snapshot{ID: "next", SourceID: source.ID, Commit: op.Commit, Path: "next", ValidatedAt: finished}
	if err := store.PromoteSnapshotAndCompleteRefresh(ctx, source, next, op); err == nil {
		t.Fatal("injected refresh completion failure was ignored")
	}
	current, ok, err := store.CurrentSnapshot(ctx, source.ID)
	if err != nil || !ok || current.ID != stable.ID {
		t.Fatalf("failed atomic promotion changed current snapshot: %+v, %v, %v", current, ok, err)
	}
	var persisted MarketplaceRefreshOperationRow
	if err := store.db.Where("id = ?", op.ID).First(&persisted).Error; err != nil || persisted.Status != "running" {
		t.Fatalf("failed atomic promotion changed operation: %+v, %v", persisted, err)
	}
}

func TestDuplicateMarketplaceSourcePreservesRuntimeStateAndAuditsWithoutCredential(t *testing.T) {
	ctx := WithQuotaActor(context.Background(), QuotaActor{UserID: "admin", SessionID: "session", CorrelationID: "request"})
	store, err := NewSQLiteStore(t.TempDir(), "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	source, _ := marketplace.NewCustomSource("duplicate", "Duplicate", "https://example.com/plugins.git", "main", "vault-secret-ref", 0)
	if err := store.SaveMarketplaceSource(ctx, source); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	snapshot := marketplace.Snapshot{ID: "current", SourceID: source.ID, Commit: "commit", Path: "current", ValidatedAt: now}
	refresh := marketplace.RefreshOperation{ID: "refresh-audited", SourceID: source.ID, Commit: snapshot.Commit, Status: "running", StartedAt: now, Actor: marketplace.OperationActor{ActorID: "admin", SessionID: "session", CorrelationID: "request"}}
	if err := store.SaveRefreshOperation(ctx, refresh); err != nil {
		t.Fatal(err)
	}
	refresh.Status, refresh.FinishedAt = "succeeded", &now
	if err := store.PromoteSnapshotAndCompleteRefresh(ctx, source, snapshot, refresh); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveMarketplaceSource(ctx, source); !errors.Is(err, ErrMarketplaceSourceExists) {
		t.Fatalf("duplicate source error = %v", err)
	}
	current, ok, err := store.CurrentSnapshot(ctx, source.ID)
	if err != nil || !ok || current.ID != snapshot.ID {
		t.Fatalf("duplicate source cleared current snapshot: %+v, %v, %v", current, ok, err)
	}
	var audits []AuditEventRow
	if err := store.db.Where("target_kind = ? AND target_id = ? AND action = ?", "marketplace_source", source.ID, "marketplace.source.add").Find(&audits).Error; err != nil || len(audits) == 0 {
		t.Fatalf("source audit missing: %+v, %v", audits, err)
	}
	for _, audit := range audits {
		if strings.Contains(audit.MetadataJSON, source.CredentialRef) || audit.ActorID != "admin" || audit.SessionID != "session" || audit.CorrelationID != "request" {
			t.Fatalf("source audit leaked credential or lost provenance: %+v", audit)
		}
	}
	var refreshAudit AuditEventRow
	if err := store.db.Where("action = ? AND target_id = ?", "marketplace.source.refresh", source.ID).First(&refreshAudit).Error; err != nil || refreshAudit.ActorID != "admin" || refreshAudit.SessionID != "session" || refreshAudit.CorrelationID != "request" || strings.Contains(refreshAudit.MetadataJSON, source.CredentialRef) {
		t.Fatalf("refresh audit lost provenance or leaked credential: %+v, %v", refreshAudit, err)
	}
}

func TestApplyPluginMutationUsesRowVersionCASForSameDigest(t *testing.T) {
	ctx := context.Background()
	store, err := NewSQLiteStore(t.TempDir(), "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Now().UTC()
	digest := pluginTestDigest("c")
	install := PluginInstallTransaction{Package: PluginPackageRow{Digest: digest, PluginID: "cas.plugin", Version: "1.0.0", CachePath: "packages/c", ManifestJSON: "{}", ConfigSchemaJSON: "{}", VerifiedAt: now}, Installed: InstalledPluginRow{PluginID: "cas.plugin", ActivePackageDigest: digest, DesiredLifecycle: "disabled", CurrentLifecycle: "disabled", CleanupPolicyJSON: "{}", LastOperationID: "install-cas", StateVersion: 1, InstalledAt: now, UpdatedAt: now}, Operation: PluginOperationRow{ID: "install-cas", PluginID: "cas.plugin", Kind: "install", Status: "succeeded", AgentResultsJSON: "{}", ActorID: "admin", CreatedAt: now}, Audit: AuditEventRow{ID: "audit-cas", Action: "plugin.install", TargetKind: "plugin", TargetID: "cas.plugin", Result: "success", MetadataJSON: "{}", CreatedAt: now}}
	if err := store.InstallPlugin(ctx, install); err != nil {
		t.Fatal(err)
	}
	first := install.Installed
	first.DesiredLifecycle, first.CurrentLifecycle, first.LastOperationID = "enabled", "applying", "enable-cas"
	second := install.Installed
	second.DesiredLifecycle, second.CurrentLifecycle, second.LastOperationID = "disabled", "applying", "disable-cas"
	mutation := func(row *InstalledPluginRow, id, kind string) PluginMutation {
		return PluginMutation{PluginID: row.PluginID, ExpectedActive: digest, ExpectedStateVersion: 1, Installed: row, Operation: PluginOperationRow{ID: id, PluginID: row.PluginID, Kind: kind, Status: "applying", AgentResultsJSON: "{}", ActorID: "admin", CreatedAt: now}, Audit: AuditEventRow{ID: "audit-" + id, Action: "plugin." + kind, TargetKind: "plugin", TargetID: row.PluginID, Result: "accepted", MetadataJSON: "{}", CreatedAt: now}}
	}
	if err := store.ApplyPluginMutation(ctx, mutation(&first, "enable-cas", "enable")); err != nil {
		t.Fatal(err)
	}
	if err := store.ApplyPluginMutation(ctx, mutation(&second, "disable-cas", "disable")); err == nil {
		t.Fatal("stale same-digest mutation overwrote a concurrent state transition")
	}
	persisted, _, _ := store.GetInstalledPlugin(ctx, install.Installed.PluginID)
	if persisted.DesiredLifecycle != "enabled" || persisted.StateVersion != 2 {
		t.Fatalf("CAS result = %+v", persisted)
	}
}

func pluginTestDigest(value string) string { return strings.Repeat(value, 64) }
