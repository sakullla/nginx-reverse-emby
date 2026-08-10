package observability

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/plugins/hostapi"
	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

func TestCapabilityAuditJournalAcknowledgesDurableRedactedWhitelist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit", "capabilities.jsonl")
	journal, err := NewCapabilityAuditJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	event := hostapi.AuditEvent{Call: pluginsdk.HostCapabilityCall{
		PluginID: "secret=must-not-leak", InstanceID: "instance-1", Generation: "generation-1",
		Capability: pluginsdk.CapabilityPolicyTrustedSource, Actor: pluginsdk.HostActor{ID: "actor-1", ResourceGroupID: "group-1"},
		Target: pluginsdk.HostTarget{Kind: "plugin.instance", ID: "instance-1", ResourceGroupID: "group-1"},
	}, Outcome: "denied", Reason: "provider secret=must-not-leak"}
	if err := journal.Audit(t.Context(), event); err != nil {
		t.Fatal(err)
	}
	if err := journal.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "must-not-leak") || !strings.Contains(string(data), `"plugin_id":"invalid"`) || !strings.Contains(string(data), `"reason":"invalid"`) {
		t.Fatalf("audit journal was not redacted: %s", data)
	}
	if err := journal.Audit(t.Context(), event); err == nil {
		t.Fatal("closed audit journal acknowledged a call")
	}
}

func TestCapabilityAuditJournalDurablyCreatesAndReopensInitialJournal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit", "capabilities.jsonl")
	event := hostapi.AuditEvent{Call: pluginsdk.HostCapabilityCall{
		PluginID: "official.policy", InstanceID: "instance-1", Generation: "generation-1",
		Capability: pluginsdk.CapabilityPolicyTrustedSource,
		Actor:      pluginsdk.HostActor{ID: "actor-1", ResourceGroupID: "group-1"},
		Target:     pluginsdk.HostTarget{Kind: "plugin.instance", ID: "instance-1", ResourceGroupID: "group-1"},
	}, Outcome: "allowed"}
	for run := 0; run < 2; run++ {
		journal, err := NewCapabilityAuditJournal(path)
		if err != nil {
			t.Fatalf("NewCapabilityAuditJournal(%d) error = %v", run, err)
		}
		if err := journal.Audit(t.Context(), event); err != nil {
			t.Fatalf("Audit(%d) error = %v", run, err)
		}
		if err := journal.Close(); err != nil {
			t.Fatalf("Close(%d) error = %v", run, err)
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if lines := strings.Count(string(data), "\n"); lines != 2 {
		t.Fatalf("durable first-create/reopen lines = %d, want 2", lines)
	}
	leftovers, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".capabilities.jsonl.create-*"))
	if err != nil || len(leftovers) != 0 {
		t.Fatalf("first-create temporary files = %v, error = %v", leftovers, err)
	}
}

func TestCapabilityAuditJournalRotatesWithBoundedRetentionAndRestarts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit", "capabilities.jsonl")
	event := hostapi.AuditEvent{Call: pluginsdk.HostCapabilityCall{PluginID: "official.policy", InstanceID: "instance-1", Generation: "generation-1", Capability: pluginsdk.CapabilityPolicyTrustedSource, Actor: pluginsdk.HostActor{ID: "actor-1", ResourceGroupID: "group-1"}, Target: pluginsdk.HostTarget{Kind: "plugin.instance", ID: "instance-1", ResourceGroupID: "group-1"}}, Outcome: "allowed"}
	journal, err := newCapabilityAuditJournal(path, 400, 2)
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 12; index++ {
		if err := journal.Audit(t.Context(), event); err != nil {
			t.Fatalf("Audit(%d) error = %v", index, err)
		}
	}
	if err := journal.Close(); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{path, path + ".1", path + ".2"} {
		data, readErr := os.ReadFile(name)
		if readErr != nil || len(data) == 0 || len(data) > 400 || strings.Contains(string(data), "must-not-leak") {
			t.Fatalf("retained audit %s bytes=%d error=%v", name, len(data), readErr)
		}
	}
	if _, err := os.Stat(path + ".3"); !os.IsNotExist(err) {
		t.Fatalf("retention exceeded: %v", err)
	}

	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fmt.Fprint(file, `{"partial":"must-not-survive"}`); err != nil {
		t.Fatal(err)
	}
	_ = file.Close()
	restarted, err := newCapabilityAuditJournal(path, 400, 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := restarted.Audit(t.Context(), event); err != nil {
		t.Fatal(err)
	}
	if err := restarted.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || strings.Contains(string(data), "must-not-survive") || !strings.HasSuffix(string(data), "\n") {
		t.Fatalf("restart recovery data=%q error=%v", data, err)
	}
}
