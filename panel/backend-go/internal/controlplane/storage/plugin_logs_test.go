package storage

import (
	"strings"
	"testing"
	"time"
)

func TestPluginRuntimeLogsAreRedactedBoundedAndCursorPaginated(t *testing.T) {
	store, err := NewSQLiteStore(t.TempDir(), "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	for _, message := range []string{"oldest", "ready token=plaintext-credential", strings.Repeat("x", pluginRuntimeLogMessageMax+20)} {
		if _, err := store.AppendPluginRuntimeLog(t.Context(), PluginRuntimeLogRow{InstanceID: "instance", PluginID: "official.rpc", AgentID: "edge-a", ResourceGroupID: "group-a", Level: "warning", Message: message}); err != nil {
			t.Fatal(err)
		}
	}
	first, err := store.ListPluginRuntimeLogs(t.Context(), PluginRuntimeLogQuery{InstanceID: "instance", AgentID: "edge-a", Limit: 2})
	if err != nil || len(first.Rows) != 2 || first.NextCursor == "" {
		t.Fatalf("first page=%+v error=%v", first, err)
	}
	if strings.Contains(first.Rows[1].Message, "plaintext-credential") || !strings.Contains(first.Rows[1].Message, "[REDACTED]") {
		t.Fatalf("credential leaked in %q", first.Rows[1].Message)
	}
	if !first.Rows[0].Truncated || len(first.Rows[0].Message) != pluginRuntimeLogMessageMax {
		t.Fatalf("oversized log was not bounded: %+v", first.Rows[0])
	}
	second, err := store.ListPluginRuntimeLogs(t.Context(), PluginRuntimeLogQuery{InstanceID: "instance", Cursor: first.NextCursor, Limit: 2})
	if err != nil || len(second.Rows) != 1 || second.Rows[0].Message != "oldest" || second.NextCursor != "" {
		t.Fatalf("second page=%+v error=%v", second, err)
	}
	if _, err := store.ListPluginRuntimeLogs(t.Context(), PluginRuntimeLogQuery{InstanceID: "instance", Cursor: "caller-controlled"}); err == nil {
		t.Fatal("invalid cursor was accepted")
	}
}

func TestPluginRuntimeLogReportFencesOwnershipSequenceAndReplay(t *testing.T) {
	store, err := NewSQLiteStore(t.TempDir(), "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Now().UTC()
	instance := PluginInstanceRow{ID: "instance", PluginID: "official.rpc", ResourceGroupID: "group-a", TargetJSON: `["edge-a"]`, PolicyChainsJSON: `[]`, SecretHandlesJSON: `[]`, BindingsJSON: `[]`, ConfigJSON: `{}`, ConfigVersion: 1, PendingConfigJSON: ``, PendingTargetJSON: ``, PendingPolicyChainsJSON: `[]`, PendingBindingsJSON: `[]`, PendingSecretHandlesJSON: `[]`, RollbackConfigJSON: ``, RollbackPolicyChainsJSON: `[]`, RollbackBindingsJSON: `[]`, RollbackSecretHandlesJSON: `[]`, DesiredEnabled: true, CurrentState: "active", StatusSummaryJSON: `{}`, UpdatedAt: now}
	status := PluginAgentRuntimeStatusRow{OperationID: "operation", AgentID: "edge-a", InstanceID: instance.ID, PluginID: instance.PluginID, ResourceGroupID: instance.ResourceGroupID, TargetVersion: instance.ConfigVersion, ConfigVersion: instance.ConfigVersion, Revision: 7, GenerationID: "generation-7", PackageDigest: strings.Repeat("a", 64), ArtifactDigest: strings.Repeat("b", 64), State: "active", AuthoritySlot: "active", UpdatedAt: now}
	if err := store.db.Create(&AgentRow{ID: "edge-a", Name: "edge", AgentToken: "token"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.db.Create(&InstalledPluginRow{PluginID: instance.PluginID, ActivePackageDigest: status.PackageDigest, ActivePackageIdentity: status.PackageDigest, DesiredLifecycle: "enabled", CurrentLifecycle: "active", CleanupPolicyJSON: `{}`, LastOperationID: status.OperationID, InstalledAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.db.Create(&instance).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.db.Create(&status).Error; err != nil {
		t.Fatal(err)
	}
	report := PluginRuntimeLogReport{Revision: 7, GenerationID: status.GenerationID, InstanceID: instance.ID, PluginID: instance.PluginID, AgentID: "edge-a", PackageDigest: status.PackageDigest, ArtifactDigest: status.ArtifactDigest, Sequence: 1, Entries: []PluginRuntimeLogEntry{{Level: "error", Message: `{"nested":{"token":"plaintext"},"message":"safe"}`, Truncated: false}}}
	if accepted, err := store.RecordPluginRuntimeLogReport(t.Context(), "edge-a", report); err != nil || !accepted {
		t.Fatalf("record accepted=%t error=%v", accepted, err)
	}
	if accepted, err := store.RecordPluginRuntimeLogReport(t.Context(), "edge-a", report); err != nil || accepted {
		t.Fatalf("replay accepted=%t error=%v", accepted, err)
	}
	page, err := store.ListPluginRuntimeLogs(t.Context(), PluginRuntimeLogQuery{InstanceID: instance.ID})
	if err != nil || len(page.Rows) != 1 || strings.Contains(page.Rows[0].Message, "plaintext") || !strings.Contains(page.Rows[0].Message, "[REDACTED]") || page.Rows[0].ResourceGroupID != "group-a" {
		t.Fatalf("page=%+v error=%v", page, err)
	}
	report.Entries[0].Message = "different"
	if _, err := store.RecordPluginRuntimeLogReport(t.Context(), "edge-a", report); err == nil {
		t.Fatal("same sequence with different digest was accepted")
	}
	report.Sequence = 2
	report.AgentID = "edge-b"
	if _, err := store.RecordPluginRuntimeLogReport(t.Context(), "edge-a", report); err == nil {
		t.Fatal("cross-agent report was accepted")
	}
	report.AgentID, report.Sequence = "edge-a", 2
	if err := store.db.Model(&PluginInstanceRow{}).Where("id = ?", instance.ID).Update("resource_group_id", "group-b").Error; err != nil {
		t.Fatal(err)
	}
	if _, err := store.RecordPluginRuntimeLogReport(t.Context(), "edge-a", report); err == nil {
		t.Fatal("historical generation was accepted after resource-group rebind")
	}
	if err := store.db.Model(&PluginInstanceRow{}).Where("id = ?", instance.ID).Update("resource_group_id", "group-a").Error; err != nil {
		t.Fatal(err)
	}
	if err := store.db.Delete(&AgentRow{}, "id = ?", "edge-a").Error; err != nil {
		t.Fatal(err)
	}
	if _, err := store.RecordPluginRuntimeLogReport(t.Context(), "edge-a", report); err == nil {
		t.Fatal("removed Agent replay was accepted")
	}
}

func TestControlPlanePluginLogOutboxIsDurableIdempotentAndImmutable(t *testing.T) {
	root := t.TempDir()
	store, err := NewSQLiteStore(root, "local")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	packageDigest, artifactDigest := strings.Repeat("a", 64), strings.Repeat("b", 64)
	runtime := PluginRuntimeInstanceRow{InstanceID: "instance", PluginID: "plugin", HostScope: "control-plane", CandidateGeneration: "generation", CandidatePackageDigest: packageDigest, CandidateArtifactDigest: artifactDigest, CandidateOperationID: "operation", CandidateResourceGroupID: "group-a", CandidateRevision: 9, CandidateState: "starting", State: "stopped", UpdatedAt: now}
	if err := store.StagePluginRuntime(t.Context(), runtime); err != nil {
		t.Fatal(err)
	}
	event := PluginControlPlaneLogOutboxRow{EventID: "event-1", InstanceID: runtime.InstanceID, PluginID: runtime.PluginID, OperationID: runtime.CandidateOperationID, GenerationID: runtime.CandidateGeneration, ResourceGroupID: runtime.CandidateResourceGroupID, Revision: runtime.CandidateRevision, PackageDigest: packageDigest, ArtifactDigest: artifactDigest, Message: `{"api-key":"plaintext"}`, CreatedAt: now}
	if err := store.EnqueueControlPlanePluginRuntimeLog(t.Context(), event); err != nil {
		t.Fatal(err)
	}
	if err := store.EnqueueControlPlanePluginRuntimeLog(t.Context(), event); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = NewSQLiteStore(root, "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	outbox, err := store.ListControlPlanePluginRuntimeLogOutbox(t.Context(), 10)
	if err != nil || len(outbox) != 1 || strings.Contains(outbox[0].Message, "plaintext") {
		t.Fatalf("recovered outbox=%+v err=%v", outbox, err)
	}
	if err := store.FlushControlPlanePluginRuntimeLog(t.Context(), event.EventID); err != nil {
		t.Fatal(err)
	}
	if err := store.FlushControlPlanePluginRuntimeLog(t.Context(), event.EventID); err != nil {
		t.Fatal(err)
	}
	page, err := store.ListPluginRuntimeLogs(t.Context(), PluginRuntimeLogQuery{InstanceID: event.InstanceID})
	if err != nil || len(page.Rows) != 1 || page.Rows[0].ResourceGroupID != event.ResourceGroupID || page.Rows[0].GenerationID != event.GenerationID || strings.Contains(page.Rows[0].Message, "plaintext") {
		t.Fatalf("flushed page=%+v err=%v", page, err)
	}
	event.EventID, event.ResourceGroupID = "event-stale", "group-b"
	if err := store.EnqueueControlPlanePluginRuntimeLog(t.Context(), event); err == nil {
		t.Fatal("mismatched immutable log authority was accepted")
	}
}
