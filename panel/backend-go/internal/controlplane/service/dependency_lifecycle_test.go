package service

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/revision"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
	"gorm.io/gorm"
)

func TestDependencyMutationCapabilityRejectionRollsBackEveryMutationTable(t *testing.T) {
	store, observer := newDependencyLifecycleAuditStore(t)
	if err := store.SaveAgent(t.Context(), storage.AgentRow{
		ID: "edge-egress", Name: "edge-egress", Platform: "linux-amd64",
		CapabilitiesJSON: `["egress_profiles"]`,
	}); err != nil {
		t.Fatalf("SaveAgent() error = %v", err)
	}
	before := dependencyLifecycleTableCounts(t, observer)
	executor := NewMutationExecutor(
		store,
		revision.WithClock(func() time.Time { return time.Date(2026, 7, 12, 23, 34, 0, 0, time.UTC) }),
		revision.WithOperationIDGenerator(func() (string, error) { return "operation-dependency-capability", nil }),
	)
	profileID := 11

	_, err := executor.Execute(t.Context(), revision.MutationRequest{
		Kind: "egress_profile.create", DependencyAction: revision.DependencyActionApply,
		IdempotencyKey: "dependency-capability", Request: map[string]any{"profile": profileID},
		Targets: []revision.Target{{
			AgentID:         "edge-egress",
			IntentResources: revision.IntentResourceSelection{EgressProfileIDs: []int{profileID}},
		}},
		ResourceState: func(ctx context.Context, tx *storage.GormStore, _ revision.Target) (any, error) {
			profiles, err := tx.ListEgressProfiles(ctx)
			if err != nil {
				return nil, err
			}
			for i := range profiles {
				profiles[i].Revision = 0
			}
			return profiles, nil
		},
		Mutate: func(ctx context.Context, tx *storage.GormStore, revisions map[string]int64) error {
			return tx.SaveEgressProfiles(ctx, []storage.EgressProfileRow{{
				ID: profileID, Name: "wg-egress", Type: "wireguard", WireGuardConfigJSON: `{}`,
				Enabled: true, Revision: revisions["edge-egress"],
			}})
		},
	})
	if revision.ErrorCodeOf(err) != revision.ErrorCodeUnprocessable {
		t.Fatalf("Execute() error = %v, code = %q, want unprocessable", err, revision.ErrorCodeOf(err))
	}
	if !strings.Contains(strings.ToLower(err.Error()), "capability") {
		t.Fatalf("Execute() error = %v, want capability rejection", err)
	}
	after := dependencyLifecycleTableCounts(t, observer)
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("mutation table counts changed after capability rejection: before=%v after=%v", before, after)
	}
}

func newDependencyLifecycleAuditStore(t *testing.T) (*storage.GormStore, *gorm.DB) {
	t.Helper()
	dataRoot := t.TempDir()
	store, err := storage.NewSQLiteStore(dataRoot, "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	observer, err := gorm.Open(sqlite.Open(filepath.Join(dataRoot, "panel.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("open audit observer: %v", err)
	}
	t.Cleanup(func() {
		if db, dbErr := observer.DB(); dbErr == nil {
			_ = db.Close()
		}
	})
	return store, observer
}

func dependencyLifecycleTableCounts(t *testing.T, db *gorm.DB) map[string]int64 {
	t.Helper()
	tables := []string{
		"egress_profiles", "operations", "agent_revisions", "agent_revision_pointers",
		"agent_revision_attempts", "agent_generations", "revision_events",
		"generation_artifacts", "agent_revision_artifacts", "idempotency_records",
	}
	counts := make(map[string]int64, len(tables))
	for _, table := range tables {
		var count int64
		if err := db.WithContext(t.Context()).Table(table).Count(&count).Error; err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		counts[table] = count
	}
	return counts
}
