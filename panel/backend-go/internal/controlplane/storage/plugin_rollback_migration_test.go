package storage

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestPluginRollbackResourceGroupMigrationUpgradesPreA5Database(t *testing.T) {
	root := t.TempDir()
	store, err := NewSQLiteStore(root, "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Now().UTC()
	for _, group := range []ResourceGroupRow{{ID: "group-a", Name: "A", CreatedAt: now, UpdatedAt: now}, {ID: "group-b", Name: "B", CreatedAt: now, UpdatedAt: now}, {ID: "group-new", Name: "New", CreatedAt: now, UpdatedAt: now}} {
		if err := store.db.Create(&group).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := store.db.Create(&AgentRow{ID: "edge-b", Name: "Edge B", CapabilitiesJSON: `[]`}).Error; err != nil {
		t.Fatal(err)
	}
	agentOwner := ResourceBindingRow{ID: "edge-b-owner", ResourceKind: "agent", ResourceID: "edge-b", ResourceGroupID: "group-b", UpdatedAt: now}
	consumerOwner := ResourceBindingRow{ID: "rule-owner", ResourceKind: PluginDependencyConsumerHTTPRule, ResourceID: "edge-b:1", ResourceGroupID: "group-b", ParentResourceKind: "agent", ParentResourceID: "edge-b", UpdatedAt: now}
	for _, row := range []any{&agentOwner, &consumerOwner, &HTTPRuleRow{ID: 1, AgentID: "edge-b", Enabled: true, Revision: 1}} {
		if err := store.db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	bindingJSON, err := EncodePluginInstanceBindings([]PluginInstanceBinding{{
		Consumer:      PluginDependencyConsumer{Kind: PluginDependencyConsumerHTTPRule, ID: "1", ResourceGroupID: "group-b", Version: pluginDependencyConsumerOwnershipVersion(consumerOwner)},
		TargetAgentID: "edge-b",
	}})
	if err != nil {
		t.Fatal(err)
	}

	seed := func(pluginID, instanceID, rollbackHandles, rollbackBindings string) {
		t.Helper()
		activeSum, rollbackSum := sha256.Sum256([]byte(pluginID+":active")), sha256.Sum256([]byte(pluginID+":rollback"))
		activeDigest, rollbackDigest := hex.EncodeToString(activeSum[:]), hex.EncodeToString(rollbackSum[:])
		for _, row := range []PluginPackageRow{
			{Identity: activeDigest, Digest: activeDigest, PluginID: pluginID, Version: "2.0.0", CachePath: "cache/" + activeDigest, ManifestJSON: `{}`, ConfigSchemaJSON: `{}`, VerifiedAt: now},
			{Identity: rollbackDigest, Digest: rollbackDigest, PluginID: pluginID, Version: "1.0.0", CachePath: "cache/" + rollbackDigest, ManifestJSON: `{}`, ConfigSchemaJSON: `{}`, VerifiedAt: now},
		} {
			if err := store.db.Create(&row).Error; err != nil {
				t.Fatal(err)
			}
		}
		installed := InstalledPluginRow{PluginID: pluginID, ActivePackageDigest: activeDigest, ActivePackageIdentity: activeDigest, RollbackPackageDigest: rollbackDigest, RollbackPackageIdentity: rollbackDigest, DesiredLifecycle: "enabled", CurrentLifecycle: "active", CleanupPolicyJSON: `{}`, LastOperationID: "operation-" + pluginID, StateVersion: 1, InstalledAt: now, UpdatedAt: now}
		instance := PluginInstanceRow{ID: instanceID, PluginID: pluginID, ResourceGroupID: "group-new", TargetJSON: `[]`, PolicyChainsJSON: `[]`, SecretHandlesJSON: `[]`, BindingsJSON: `[]`, ConfigJSON: `{}`, ConfigVersion: 2, PendingConfigJSON: "", PendingTargetJSON: "", PendingPolicyChainsJSON: `[]`, PendingBindingsJSON: `[]`, PendingSecretHandlesJSON: `[]`, RollbackConfigJSON: `{}`, RollbackVersion: 1, RollbackPolicyChainsJSON: `[]`, RollbackBindingsJSON: rollbackBindings, RollbackSecretHandlesJSON: rollbackHandles, CurrentState: "active", StatusSummaryJSON: `{}`, StateVersion: 1, UpdatedAt: now}
		if err := store.db.Create(&installed).Error; err != nil {
			t.Fatal(err)
		}
		if err := store.db.Create(&instance).Error; err != nil {
			t.Fatal(err)
		}
	}
	seedSecret := func(id, instanceID, groupID string) string {
		t.Helper()
		purpose := "plugin-config:" + instanceID + ":/token"
		secret := SecretRow{ID: id, Name: id, Purpose: purpose, ResourceGroupID: groupID, ActiveVersion: 1, Fingerprint: id, CreatedAt: now, RotatedAt: now}
		version := SecretVersionRow{SecretID: id, Version: 1, KeyID: "key", Nonce: []byte{1}, Ciphertext: []byte{2}, CreatedAt: now}
		if err := store.db.Create(&secret).Error; err != nil {
			t.Fatal(err)
		}
		if err := store.db.Create(&version).Error; err != nil {
			t.Fatal(err)
		}
		return fmt.Sprintf(`[{"pointer":"/token","id":%q,"version":1,"digest":%q,"purpose":%q}]`, id, strings.Repeat("0", 64), purpose)
	}
	seed("plugin-secret", "instance-secret", seedSecret("secret-derived", "instance-secret", "group-a"), `[]`)
	seed("plugin-binding", "instance-binding", `[]`, bindingJSON)
	seed("plugin-ambiguous", "instance-ambiguous", `[]`, `[]`)
	seed("plugin-conflict", "instance-conflict", seedSecret("secret-conflict", "instance-conflict", "group-a"), bindingJSON)

	if err := store.db.Exec("ALTER TABLE plugin_instances DROP COLUMN rollback_resource_group_id").Error; err != nil {
		t.Fatal(err)
	}
	if store.db.Migrator().HasColumn(&PluginInstanceRow{}, "RollbackResourceGroupID") {
		t.Fatal("pre-A5 fixture retained rollback_resource_group_id")
	}
	var legacyInstances int64
	if err := store.db.Table("plugin_instances").Count(&legacyInstances).Error; err != nil || legacyInstances != 4 {
		t.Fatalf("pre-A5 fixture instances=%d err=%v", legacyInstances, err)
	}
	if err := prepareLegacyPluginRollbackResourceGroupColumn(store.db); err != nil {
		t.Fatalf("prepare pre-A5 database: %v", err)
	}
	if err := backfillPluginRollbackResourceGroups(t.Context(), store.db); err != nil {
		t.Fatalf("migrate pre-A5 rollback ownership: %v", err)
	}
	reopened := store
	for instanceID, wantGroup := range map[string]string{"instance-secret": "group-a", "instance-binding": "group-b"} {
		var instance PluginInstanceRow
		if err := reopened.db.Where("id = ?", instanceID).First(&instance).Error; err != nil {
			t.Fatal(err)
		}
		if instance.RollbackResourceGroupID != wantGroup || instance.RollbackVersion != 1 {
			t.Fatalf("derived rollback %s = %+v", instanceID, instance)
		}
		var installed InstalledPluginRow
		if err := reopened.db.Where("plugin_id = ?", instance.PluginID).First(&installed).Error; err != nil || installed.RollbackPackageDigest == "" {
			t.Fatalf("derived rollback package %s = %+v err=%v", instance.PluginID, installed, err)
		}
	}
	for _, instanceID := range []string{"instance-ambiguous", "instance-conflict"} {
		var instance PluginInstanceRow
		if err := reopened.db.Where("id = ?", instanceID).First(&instance).Error; err != nil {
			t.Fatal(err)
		}
		if instance.RollbackVersion != 0 || instance.RollbackConfigJSON != "" || instance.RollbackSecretHandlesJSON != `[]` || instance.RollbackBindingsJSON != `[]` {
			t.Fatalf("ambiguous rollback was not retired: %+v", instance)
		}
		var installed InstalledPluginRow
		if err := reopened.db.Where("plugin_id = ?", instance.PluginID).First(&installed).Error; err != nil || installed.RollbackPackageDigest != "" {
			t.Fatalf("ambiguous rollback package %s = %+v err=%v", instance.PluginID, installed, err)
		}
	}
	var conflict SecretRow
	if err := reopened.db.Where("id = ?", "secret-conflict").First(&conflict).Error; err != nil || conflict.RetiredAt == nil {
		t.Fatalf("ambiguous rollback secret = %+v err=%v", conflict, err)
	}
	var conflictVersion SecretVersionRow
	if err := reopened.db.Where("secret_id = ? AND version = ?", "secret-conflict", 1).First(&conflictVersion).Error; err != nil || conflictVersion.DestroyedAt == nil || len(conflictVersion.Ciphertext) != 0 {
		t.Fatalf("ambiguous rollback secret version = %+v err=%v", conflictVersion, err)
	}
}
