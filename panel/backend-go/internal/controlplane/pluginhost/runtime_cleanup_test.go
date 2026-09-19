package pluginhost

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPruneRuntimeDirectoriesKeepsReferencedAndRecentGenerations(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, time.August, 26, 12, 0, 0, 0, time.UTC)
	old := now.Add(-48 * time.Hour)
	recent := now.Add(-time.Hour)
	activeGeneration := strings.Repeat("a", 64)
	staleGeneration := strings.Repeat("b", 64)
	recentGeneration := strings.Repeat("c", 64)
	unknown := "operator-owned"

	for name, modified := range map[string]time.Time{
		"active-instance-" + activeGeneration: old,
		"stale-instance-" + staleGeneration:   old,
		"recent-instance-" + recentGeneration: recent,
		unknown:                               old,
	} {
		path := filepath.Join(root, name)
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
		if err := os.Chtimes(path, modified, modified); err != nil {
			t.Fatalf("set %s time: %v", name, err)
		}
	}

	deleted, err := PruneRuntimeDirectories(root, []RuntimeDirectoryReference{{InstanceID: "active-instance", Generation: activeGeneration}}, now.Add(-24*time.Hour))
	if err != nil {
		t.Fatalf("prune runtime directories: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("deleted directories = %d, want 1", deleted)
	}
	for _, name := range []string{"active-instance-" + activeGeneration, "recent-instance-" + recentGeneration, unknown} {
		if _, err := os.Stat(filepath.Join(root, name)); err != nil {
			t.Fatalf("kept directory %s: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "stale-instance-"+staleGeneration)); !os.IsNotExist(err) {
		t.Fatalf("stale directory still exists: %v", err)
	}
}
