//go:build !fast && !integration

package storage

import (
	"errors"
	"strings"
	"testing"
)

func TestPluginRuntimeLogMissingAuthorityIsStale(t *testing.T) {
	store, err := newStorageTestSQLiteStore(t, t.TempDir(), "local", true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	report := PluginRuntimeLogReport{
		Revision: 1, GenerationID: "generation-1", InstanceID: "instance-1", PluginID: "docker-app", AgentID: "local",
		PackageDigest: strings.Repeat("a", 64), ArtifactDigest: strings.Repeat("b", 64), Sequence: 1,
		Entries: []PluginRuntimeLogEntry{{Level: "error", Message: "stale candidate output"}},
	}
	if _, err := store.RecordPluginRuntimeLogReport(t.Context(), "local", report); !errors.Is(err, ErrPluginGenerationStale) {
		t.Fatalf("missing runtime-log authority error = %v, want ErrPluginGenerationStale", err)
	}
}
