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
	instance := PluginInstanceRow{ID: "instance", PluginID: "official.rpc", ResourceGroupID: "group-a", TargetJSON: `["edge-a"]`, PolicyChainsJSON: `[]`, SecretHandlesJSON: `[]`, BindingsJSON: `[]`, ConfigJSON: `{}`, PendingConfigJSON: ``, PendingTargetJSON: ``, PendingPolicyChainsJSON: `[]`, PendingBindingsJSON: `[]`, PendingSecretHandlesJSON: `[]`, RollbackConfigJSON: ``, RollbackPolicyChainsJSON: `[]`, RollbackBindingsJSON: `[]`, RollbackSecretHandlesJSON: `[]`, CurrentState: "active", StatusSummaryJSON: `{}`, UpdatedAt: now}
	status := PluginAgentRuntimeStatusRow{OperationID: "operation", AgentID: "edge-a", InstanceID: instance.ID, PluginID: instance.PluginID, Revision: 7, GenerationID: "generation-7", PackageDigest: strings.Repeat("a", 64), ArtifactDigest: strings.Repeat("b", 64), State: "active", UpdatedAt: now}
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
}
