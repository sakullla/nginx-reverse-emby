package localagent

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

type pluginReportSyncStore struct {
	reportErr error
	logErr    error
}

func (s pluginReportSyncStore) LoadLocalSnapshot(context.Context, string) (storage.Snapshot, error) {
	return storage.Snapshot{Revision: 7}, nil
}

func (s pluginReportSyncStore) RecordPluginAgentRuntimeReport(context.Context, storage.PluginGenerationReport) (storage.PluginAgentRuntimeStatusRow, bool, error) {
	return storage.PluginAgentRuntimeStatusRow{}, false, s.reportErr
}

func (s pluginReportSyncStore) RecordPluginRuntimeLogReport(context.Context, string, storage.PluginRuntimeLogReport) (bool, error) {
	return false, s.logErr
}

func TestLocalSyncSourceDiscardsStalePluginTelemetry(t *testing.T) {
	snapshot, err := NewSyncSource(pluginReportSyncStore{
		reportErr: storage.ErrPluginGenerationStale,
		logErr:    storage.ErrPluginGenerationStale,
	}, "local").Sync(t.Context(), SyncRequest{
		PluginStatuses: []storage.PluginRuntimeStatus{{InstanceID: "retired-instance"}},
		PluginLogs:     []storage.PluginRuntimeLogReport{{InstanceID: "retired-instance"}},
	})
	if err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	if snapshot.Revision != 7 {
		t.Fatalf("snapshot revision = %d", snapshot.Revision)
	}
}

func TestLocalSyncSourceDiscardsConflictingPluginTelemetry(t *testing.T) {
	snapshot, err := NewSyncSource(pluginReportSyncStore{
		reportErr: storage.ErrPluginGenerationConflict,
	}, "local").Sync(t.Context(), SyncRequest{
		PluginStatuses: []storage.PluginRuntimeStatus{{InstanceID: "conflicting-instance"}},
	})
	if err != nil || snapshot.Revision != 7 {
		t.Fatalf("Sync() error = %v", err)
	}
}

func TestLocalSyncSourceKeepsUnexpectedPluginTelemetryErrorsFatal(t *testing.T) {
	expected := errors.New("telemetry storage unavailable")
	_, err := NewSyncSource(pluginReportSyncStore{reportErr: expected}, "local").Sync(t.Context(), SyncRequest{
		PluginStatuses: []storage.PluginRuntimeStatus{{InstanceID: "instance"}},
	})
	if !errors.Is(err, expected) {
		t.Fatalf("Sync() error = %v", err)
	}
}

func TestLocalSyncSourceOverlaysPendingManagedCertificateGeneration(t *testing.T) {
	dataRoot := t.TempDir()
	dsn := filepath.Join(dataRoot, "panel.db") +
		"?_pragma=journal_mode(MEMORY)&_pragma=synchronous(OFF)&_pragma=busy_timeout(5000)&_pragma=cache_size(-65536)&_pragma=temp_store(MEMORY)"
	store, err := storage.NewStore(storage.StoreConfig{
		Driver: "sqlite", DSN: dsn, DataRoot: dataRoot, LocalAgentID: "local", TrafficStatsEnabled: true,
	})
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	const domain = "local-pending.example.test"
	if err := store.SaveManagedCertificates(t.Context(), []storage.ManagedCertificateRow{{
		ID: 301, Domain: domain, Enabled: true, Scope: "domain", IssuerMode: "master_cf_dns",
		TargetAgentIDs: `[]`, Status: "pending", CertificateType: "acme", Usage: "https", Revision: 3,
	}}); err != nil {
		t.Fatalf("SaveManagedCertificates() error = %v", err)
	}
	pending, err := store.StageManagedCertificateGeneration(t.Context(), domain, storage.ManagedCertificateBundle{
		ID: 301, Domain: domain, Revision: 3, CertPEM: "pending-cert", KeyPEM: "pending-key",
	})
	if err != nil {
		t.Fatalf("StageManagedCertificateGeneration() error = %v", err)
	}

	snapshot, err := NewSyncSource(store, "local").Sync(t.Context(), SyncRequest{})
	if err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	if len(snapshot.Certificates) != 1 || snapshot.Certificates[0].CertPEM != pending.Material.CertPEM || snapshot.Certificates[0].KeyPEM != pending.Material.KeyPEM {
		t.Fatalf("snapshot certificates = %+v", snapshot.Certificates)
	}
	if len(snapshot.CertificatePolicies) != 1 || snapshot.CertificatePolicies[0].ID != 301 || snapshot.CertificatePolicies[0].IssuerMode != "master_cf_dns" {
		t.Fatalf("snapshot certificate policies = %+v", snapshot.CertificatePolicies)
	}
	if _, found, err := store.LoadActiveManagedCertificateGeneration(t.Context(), domain); err != nil || found {
		t.Fatalf("active generation before local report found=%v error=%v", found, err)
	}
}
