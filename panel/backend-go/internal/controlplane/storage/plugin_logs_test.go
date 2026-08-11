package storage

import (
	"strings"
	"testing"
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
