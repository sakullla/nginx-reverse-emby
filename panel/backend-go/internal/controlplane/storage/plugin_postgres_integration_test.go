//go:build integration

package storage

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/marketplace"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestIntegrationPostgresPluginRuntimeStateUsesByteaAndPreservesLegacyData(t *testing.T) {
	dsn := postgresIntegrationSchemaDSN(t)
	legacy, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := legacy.Exec(`CREATE DOMAIN blob AS bytea`).Error; err != nil {
		t.Fatal(err)
	}
	if err := legacy.Exec(`
		CREATE TABLE plugin_runtime_state (
			instance_id varchar(64) NOT NULL,
			key varchar(190) NOT NULL,
			plugin_id varchar(190) NOT NULL,
			resource_group_id varchar(64) NOT NULL,
			value blob NOT NULL,
			updated_at timestamptz NOT NULL,
			PRIMARY KEY (instance_id, key)
		)
	`).Error; err != nil {
		t.Fatal(err)
	}
	legacyValue := []byte{0, 1, 2, 0xff}
	if err := legacy.Exec(`
		INSERT INTO plugin_runtime_state (instance_id, key, plugin_id, resource_group_id, value, updated_at)
		VALUES (?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
	`, "legacy-instance", "legacy-key", "legacy-plugin", "legacy-group", legacyValue).Error; err != nil {
		t.Fatal(err)
	}
	if sqlDB, dbErr := legacy.DB(); dbErr != nil {
		t.Fatal(dbErr)
	} else if closeErr := sqlDB.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}

	store, err := NewStore(StoreConfig{Driver: "postgres", DSN: dsn, DataRoot: t.TempDir(), LocalAgentID: "local"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	loaded, found, err := store.GetPluginRuntimeState(t.Context(), "legacy-instance", "legacy-key")
	if err != nil || !found || !bytes.Equal(loaded, legacyValue) {
		t.Fatalf("legacy runtime state = %x, found=%v, err=%v", loaded, found, err)
	}
	currentValue := []byte{0xde, 0xad, 0, 0xbe, 0xef}
	if err := store.PutPluginRuntimeState(t.Context(), PluginRuntimeStateRow{
		InstanceID: "current-instance", Key: "current-key", PluginID: "current-plugin",
		ResourceGroupID: "current-group", Value: currentValue,
	}); err != nil {
		t.Fatal(err)
	}
	loaded, found, err = store.GetPluginRuntimeState(t.Context(), "current-instance", "current-key")
	if err != nil || !found || !bytes.Equal(loaded, currentValue) {
		t.Fatalf("current runtime state = %x, found=%v, err=%v", loaded, found, err)
	}
	var udtName string
	if err := store.db.Raw(`
		SELECT udt_name
		FROM information_schema.columns
		WHERE table_schema = current_schema()
		  AND table_name = 'plugin_runtime_state'
		  AND column_name = 'value'
	`).Scan(&udtName).Error; err != nil {
		t.Fatal(err)
	}
	if udtName != "bytea" {
		t.Fatalf("plugin_runtime_state.value type = %q, want bytea", udtName)
	}
}

func TestIntegrationPostgresPackagePublicationFencesSourceDeletion(t *testing.T) {
	dsn := postgresIntegrationSchemaDSN(t)
	store, err := NewStore(StoreConfig{Driver: "postgres", DSN: dsn, DataRoot: t.TempDir(), LocalAgentID: "local"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	source := newSignedStorageMarketplaceSource(t, "postgres-publish-delete", "Postgres Publish Delete", "https://example.com/postgres-publish-delete.git", "main", "")
	if err := store.SaveMarketplaceSource(ctx, source); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	operation := refreshOperationForSource(source, marketplace.RefreshOperation{
		ID: "postgres-publish-delete-op", SourceID: source.ID, Status: "running", StartedAt: now,
		LeaseToken: "postgres-publish-delete-lease", LeaseExpiresAt: now.Add(time.Minute),
	})
	if err := store.AcquireRefreshLease(ctx, operation); err != nil {
		t.Fatal(err)
	}
	digest := pluginTestDigest("c")
	trust := marketplaceTrustForTest(t, source)
	if err := store.StagePackageAcquisition(ctx, source.ID, digest, operation.ID, trust); err != nil {
		t.Fatal(err)
	}
	publishEntered := make(chan struct{})
	releasePublish := make(chan struct{})
	publishDone := make(chan error, 1)
	go func() {
		publishDone <- store.PublishPackageAcquisition(ctx, source.ID, digest, operation.ID, trust, func() error {
			close(publishEntered)
			<-releasePublish
			return nil
		})
	}()
	<-publishEntered
	deleteStarted := make(chan struct{})
	deleteDone := make(chan error, 1)
	go func() {
		close(deleteStarted)
		_, err := store.DeleteMarketplaceSource(ctx, source.ID)
		deleteDone <- err
	}()
	<-deleteStarted
	select {
	case err := <-deleteDone:
		t.Fatalf("PostgreSQL source deletion crossed an active package publication: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(releasePublish)
	if err := <-publishDone; err != nil {
		t.Fatal(err)
	}
	if err := <-deleteDone; err != nil {
		t.Fatal(err)
	}
	var staged int64
	if err := store.db.Model(&PluginPackageStagingRow{}).Where("source_id = ? AND operation_id = ?", source.ID, operation.ID).Count(&staged).Error; err != nil || staged != 0 {
		t.Fatalf("PostgreSQL source deletion left publication marker count=%d err=%v", staged, err)
	}
}

func newSignedStorageMarketplaceSource(t *testing.T, id, name, remoteURL, branch, credentialRef string) marketplace.Source {
	t.Helper()
	source, err := marketplace.NewSignedCustomSource(id, name, remoteURL, branch, credentialRef, 0, marketplace.SourceSigner{KeyID: id + "-release", SecretRef: "vault-" + id, PublicKey: base64.StdEncoding.EncodeToString(make([]byte, 32))})
	if err != nil {
		t.Fatal(err)
	}
	return source
}

func refreshOperationForSource(source marketplace.Source, operation marketplace.RefreshOperation) marketplace.RefreshOperation {
	operation.SourceRevision = source.ConfigRevision
	operation.RefKind = source.RefKind
	operation.RefName = source.RefName
	trust, err := source.SignatureTrust()
	if err != nil {
		panic(err)
	}
	operation.SignerSourceKind = trust.SourceKind
	operation.SignerKeyID = trust.KeyID
	operation.SignerPublicKey = trust.PublicKey
	operation.SignerFingerprint = trust.Fingerprint
	if operation.Actor.ActorID == "" {
		operation.Actor.ActorID = "test.marketplace"
	}
	if operation.Actor.CorrelationID == "" {
		operation.Actor.CorrelationID = operation.ID
	}
	return operation
}

func marketplaceTrustForTest(t *testing.T, source marketplace.Source) marketplace.SignatureTrust {
	t.Helper()
	trust, err := source.SignatureTrust()
	if err != nil {
		t.Fatal(err)
	}
	return trust
}

func pluginTestDigest(value string) string { return strings.Repeat(value, 64) }

func postgresIntegrationSchemaDSN(t *testing.T) string {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv("NRE_TEST_POSTGRES_DSN"))
	if dsn == "" {
		t.Fatal("NRE_TEST_POSTGRES_DSN is required for the canonical integration gate")
	}
	admin, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := admin.DB()
	if err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("nre_storage_%d", time.Now().UnixNano())
	if err := admin.Exec(`CREATE SCHEMA "` + schema + `"`).Error; err != nil {
		_ = sqlDB.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = admin.Exec(`DROP SCHEMA IF EXISTS "` + schema + `" CASCADE`).Error
		_ = sqlDB.Close()
	})
	parsed, parseErr := url.Parse(dsn)
	if parseErr == nil && (parsed.Scheme == "postgres" || parsed.Scheme == "postgresql") {
		query := parsed.Query()
		query.Set("search_path", schema)
		parsed.RawQuery = query.Encode()
		return parsed.String()
	}
	return dsn + " search_path=" + schema
}
