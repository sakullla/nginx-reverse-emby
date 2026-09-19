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

func TestPluginRuntimeLogReportRejectsValuesOutsideAgentContract(t *testing.T) {
	valid := PluginRuntimeLogReport{
		Revision: 1, GenerationID: strings.Repeat("c", 64), InstanceID: "instance-1", PluginID: "docker-app", AgentID: "local",
		PackageDigest: strings.Repeat("a", 64), ArtifactDigest: strings.Repeat("b", 64), Sequence: 1,
		Entries: []PluginRuntimeLogEntry{{Level: "warn", Message: "safe warning"}},
	}
	if err := validatePluginRuntimeLogReport(valid); err != nil {
		t.Fatalf("valid Agent report = %v", err)
	}

	tests := map[string]PluginRuntimeLogReport{
		"non canonical identity": func() PluginRuntimeLogReport { value := valid; value.InstanceID = " instance-1"; return value }(),
		"non hex digest": func() PluginRuntimeLogReport {
			value := valid
			value.PackageDigest = strings.Repeat("z", 64)
			return value
		}(),
		"unknown level": func() PluginRuntimeLogReport {
			value := valid
			value.Entries = []PluginRuntimeLogEntry{{Level: "warning", Message: "wrong wire value"}}
			return value
		}(),
		"empty message": func() PluginRuntimeLogReport {
			value := valid
			value.Entries = []PluginRuntimeLogEntry{{Level: "info"}}
			return value
		}(),
		"oversized message": func() PluginRuntimeLogReport {
			value := valid
			value.Entries = []PluginRuntimeLogEntry{{Level: "info", Message: strings.Repeat("x", pluginRuntimeLogMessageMax+1)}}
			return value
		}(),
	}
	for name, report := range tests {
		t.Run(name, func(t *testing.T) {
			if err := validatePluginRuntimeLogReport(report); err == nil {
				t.Fatal("invalid Agent report was accepted")
			}
		})
	}
}
