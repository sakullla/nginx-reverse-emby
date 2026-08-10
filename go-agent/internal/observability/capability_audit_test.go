package observability

import (
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
