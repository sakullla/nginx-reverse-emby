package storage

import (
	"fmt"
	"testing"
	"time"

	"gorm.io/gorm"
)

func TestPluginSecretReferenceGraphRetiresOnlyUnreferencedVersions(t *testing.T) {
	store, err := NewSQLiteStore(t.TempDir(), "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Now().UTC()
	for _, id := range []string{"active", "pending", "rollback", "orphan", "failed"} {
		secret := SecretRow{ID: id, Name: "plugin-" + id, Purpose: "plugin-config:instance:/token", ResourceGroupID: "group-a", ActiveVersion: 1, Fingerprint: "fingerprint", CreatedAt: now, RotatedAt: now}
		version := SecretVersionRow{SecretID: id, Version: 1, KeyID: "key", Nonce: []byte{1}, Ciphertext: []byte{2}, CreatedAt: now}
		if err := store.db.Create(&secret).Error; err != nil {
			t.Fatal(err)
		}
		if err := store.db.Create(&version).Error; err != nil {
			t.Fatal(err)
		}
	}
	instance := PluginInstanceRow{ID: "instance", PluginID: "plugin", ResourceGroupID: "group-a", TargetJSON: `[]`, PolicyChainsJSON: `[]`, BindingsJSON: `[]`, ConfigJSON: `{}`, SecretHandlesJSON: secretHandleFixture("active"), PendingConfigJSON: `{}`, PendingTargetJSON: `[]`, PendingPolicyChainsJSON: `[]`, PendingBindingsJSON: `[]`, PendingSecretHandlesJSON: secretHandleFixture("pending"), RollbackConfigJSON: `{}`, RollbackPolicyChainsJSON: `[]`, RollbackBindingsJSON: `[]`, RollbackSecretHandlesJSON: secretHandleFixture("rollback"), StatusSummaryJSON: `{}`, UpdatedAt: now}
	if err := store.db.Create(&instance).Error; err != nil {
		t.Fatal(err)
	}
	candidates := map[string]string{"active": instance.ID, "pending": instance.ID, "rollback": instance.ID, "orphan": instance.ID}
	if err := store.writeTransaction(t.Context(), func(tx *gorm.DB) error {
		return reconcilePluginSecretOwnershipTx(tx, PluginMutation{PluginID: instance.PluginID, Operation: PluginOperationRow{ID: "operation-1", PluginID: instance.PluginID, CreatedAt: now}}, candidates)
	}); err != nil {
		t.Fatal(err)
	}
	assertPluginSecretRetired(t, store, "orphan", true)
	for _, id := range []string{"active", "pending", "rollback"} {
		assertPluginSecretRetired(t, store, id, false)
	}

	if err := store.db.Model(&PluginInstanceRow{}).Where("id = ?", instance.ID).Updates(map[string]any{"secret_handles_json": secretHandleFixture("pending"), "pending_secret_handles_json": "[]", "rollback_secret_handles_json": secretHandleFixture("active")}).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.writeTransaction(t.Context(), func(tx *gorm.DB) error {
		return reconcilePluginSecretOwnershipTx(tx, PluginMutation{PluginID: instance.PluginID, Operation: PluginOperationRow{ID: "operation-2", PluginID: instance.PluginID, CreatedAt: now.Add(time.Second)}}, map[string]string{"active": instance.ID, "pending": instance.ID, "rollback": instance.ID})
	}); err != nil {
		t.Fatal(err)
	}
	assertPluginSecretRetired(t, store, "rollback", true)
	assertPluginSecretRetired(t, store, "active", false)
	assertPluginSecretRetired(t, store, "pending", false)

	if err := store.db.Model(&PluginInstanceRow{}).Where("id = ?", instance.ID).Update("pending_secret_handles_json", secretHandleFixture("failed")).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.db.Model(&PluginInstanceRow{}).Where("id = ?", instance.ID).Update("pending_secret_handles_json", "[]").Error; err != nil {
		t.Fatal(err)
	}
	if err := store.writeTransaction(t.Context(), func(tx *gorm.DB) error {
		return reconcilePluginSecretOwnershipTx(tx, PluginMutation{PluginID: instance.PluginID, Operation: PluginOperationRow{ID: "operation-3", PluginID: instance.PluginID, CreatedAt: now.Add(2 * time.Second)}}, map[string]string{"failed": instance.ID})
	}); err != nil {
		t.Fatal(err)
	}
	assertPluginSecretRetired(t, store, "failed", true)

	if err := store.db.Delete(&PluginInstanceRow{}, "id = ?", instance.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.writeTransaction(t.Context(), func(tx *gorm.DB) error {
		return reconcilePluginSecretOwnershipTx(tx, PluginMutation{PluginID: instance.PluginID, Operation: PluginOperationRow{ID: "operation-4", PluginID: instance.PluginID, CreatedAt: now.Add(3 * time.Second)}}, map[string]string{"active": instance.ID, "pending": instance.ID})
	}); err != nil {
		t.Fatal(err)
	}
	assertPluginSecretRetired(t, store, "active", true)
	assertPluginSecretRetired(t, store, "pending", true)
}

func secretHandleFixture(id string) string {
	return fmt.Sprintf(`[{"pointer":"/token","id":%q,"version":1,"digest":"%064d","purpose":"plugin-config:instance:/token"}]`, id, 0)
}

func assertPluginSecretRetired(t *testing.T, store *GormStore, id string, retired bool) {
	t.Helper()
	var secret SecretRow
	if err := store.db.Where("id = ?", id).First(&secret).Error; err != nil {
		t.Fatal(err)
	}
	if (secret.RetiredAt != nil) != retired {
		t.Fatalf("secret %s retired=%t, want %t", id, secret.RetiredAt != nil, retired)
	}
	var version SecretVersionRow
	if err := store.db.Where("secret_id = ? AND version = 1", id).First(&version).Error; err != nil {
		t.Fatal(err)
	}
	if retired && (version.DestroyedAt == nil || len(version.Ciphertext) != 0 || len(version.Nonce) != 0) {
		t.Fatalf("secret %s version was not destroyed: %+v", id, version)
	}
}

func TestPluginOperationScopesPreserveMultiGroupOwnership(t *testing.T) {
	store, err := NewSQLiteStore(t.TempDir(), "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Now().UTC()
	for _, instance := range []PluginInstanceRow{{ID: "a", PluginID: "plugin", ResourceGroupID: "group-a"}, {ID: "b", PluginID: "plugin", ResourceGroupID: "group-b"}} {
		instance.TargetJSON, instance.PolicyChainsJSON, instance.BindingsJSON, instance.ConfigJSON, instance.SecretHandlesJSON = `[]`, `[]`, `[]`, `{}`, `[]`
		instance.PendingPolicyChainsJSON, instance.PendingBindingsJSON, instance.PendingSecretHandlesJSON = `[]`, `[]`, `[]`
		instance.RollbackPolicyChainsJSON, instance.RollbackBindingsJSON, instance.RollbackSecretHandlesJSON = `[]`, `[]`, `[]`
		instance.StatusSummaryJSON, instance.UpdatedAt = `{}`, now
		if err := store.db.Create(&instance).Error; err != nil {
			t.Fatal(err)
		}
	}
	operation := PluginOperationRow{ID: "operation", PluginID: "plugin", Kind: "upgrade", CreatedAt: now}
	rows, err := pluginOperationScopesForPluginTx(store.db, "plugin", operation)
	if err != nil || len(rows) != 2 || rows[0].ResourceGroupID == rows[1].ResourceGroupID {
		t.Fatalf("multi-group scopes=%+v err=%v", rows, err)
	}
	scalar := operation
	scalar.InstanceID, scalar.ResourceGroupID = "a", "group-a"
	rows, err = pluginOperationScopesForPluginTx(store.db, "plugin", scalar)
	if err != nil || len(rows) != 1 || rows[0].InstanceID != "a" {
		t.Fatalf("scalar scopes=%+v err=%v", rows, err)
	}
}

func TestLegacyPluginSecretMigrationPersistsActiveAndStagedOwnership(t *testing.T) {
	store, err := NewSQLiteStore(t.TempDir(), "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Now().UTC()
	instance := PluginInstanceRow{ID: "legacy", PluginID: "plugin", ResourceGroupID: "group-a", TargetJSON: `[]`, PolicyChainsJSON: `[]`, BindingsJSON: `[]`, ConfigJSON: `{"token":"plaintext"}`, SecretHandlesJSON: `[]`, PendingConfigJSON: `{"token":"pending"}`, PendingTargetJSON: `[]`, PendingPolicyChainsJSON: `[]`, PendingBindingsJSON: `[]`, PendingSecretHandlesJSON: `[]`, RollbackConfigJSON: `{}`, RollbackPolicyChainsJSON: `[]`, RollbackBindingsJSON: `[]`, RollbackSecretHandlesJSON: `[]`, StatusSummaryJSON: `{}`, UpdatedAt: now}
	if err := store.db.Create(&instance).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.db.Where("id = ?", instance.ID).First(&instance).Error; err != nil {
		t.Fatal(err)
	}
	instance.ConfigJSON, instance.SecretHandlesJSON = `{}`, secretHandleFixture("migrated-active")
	instance.PendingConfigJSON, instance.PendingSecretHandlesJSON = `{}`, secretHandleFixture("migrated-pending")
	writes := make([]PluginSecretWrite, 0, 2)
	for _, id := range []string{"migrated-active", "migrated-pending"} {
		writes = append(writes, PluginSecretWrite{
			Secret:  SecretRow{ID: id, Name: id, Purpose: "plugin-config:legacy:/token", ResourceGroupID: "group-a", ActiveVersion: 1, Fingerprint: id, CreatedAt: now, RotatedAt: now},
			Version: SecretVersionRow{SecretID: id, Version: 1, KeyID: "key", Nonce: []byte{1}, Ciphertext: []byte{2}, CreatedAt: now},
			Audit:   AuditEventRow{ID: "audit-" + id, Action: "secret.create", TargetKind: "secret", TargetID: id, ResourceGroupID: "group-a", Result: "success", MetadataJSON: `{}`, CreatedAt: now},
		})
	}
	if err := store.MigrateLegacyPluginInstanceSecrets(t.Context(), instance.StateVersion, instance, writes); err != nil {
		t.Fatal(err)
	}
	var ownership []PluginOperationSecretRow
	if err := store.db.Order("secret_id").Find(&ownership).Error; err != nil {
		t.Fatal(err)
	}
	if len(ownership) != 2 || ownership[0].State != "active" || ownership[1].State != "staged" || ownership[0].OperationID != ownership[1].OperationID {
		t.Fatalf("legacy ownership = %+v", ownership)
	}
}
