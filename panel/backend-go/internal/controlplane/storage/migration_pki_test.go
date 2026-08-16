//go:build integration

package storage

import (
	"testing"
)

func TestIntegrationCopyDefaultMigrationRowsOmitsLivePKILeaseAndJobs(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	source := newTrafficTestStore(t, true)
	target := newTrafficTestStore(t, true)
	if err := source.SaveAgent(ctx, AgentRow{ID: "edge-1", Name: "edge-1"}); err != nil {
		t.Fatal(err)
	}
	if err := CopyDefaultMigrationRows(ctx, source, target); err != nil {
		t.Fatal(err)
	}
	sourceState, err := source.LoadPKICanonicalState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	targetState, err := target.LoadPKICanonicalState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if sourceState.Settings != nil || targetState.Settings != nil {
		t.Fatalf("unexpected PKI settings: source=%+v target=%+v", sourceState.Settings, targetState.Settings)
	}
	if sourceState.InstanceLease != nil || targetState.InstanceLease != nil {
		t.Fatal("process-local PKI lease was copied")
	}
	if len(targetState.LifecycleJobs) != 0 {
		t.Fatalf("lifecycle jobs copied = %+v", targetState.LifecycleJobs)
	}
	if !target.db.Migrator().HasTable(&PKISettingsRow{}) {
		t.Fatal("target lost PKI schema")
	}
}
