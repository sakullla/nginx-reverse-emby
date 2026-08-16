//go:build !integration

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

func TestCapabilityAuditJournalDurableRecoveryLifecycle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit", "capabilities.jsonl")
	redactedEvent := capabilityAuditTestEvent("secret=must-not-leak", "provider secret=must-not-leak")

	journal, err := newCapabilityAuditJournal(path, 2_048, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := journal.Audit(t.Context(), redactedEvent); err != nil {
		t.Fatal(err)
	}
	if err := journal.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || strings.Contains(string(data), "must-not-leak") || !strings.Contains(string(data), `"plugin_id":"invalid"`) || !strings.HasSuffix(string(data), "\n") {
		t.Fatalf("durable redacted journal = %q, error = %v", data, err)
	}
	if err := journal.Audit(t.Context(), redactedEvent); err == nil {
		t.Fatal("closed journal acknowledged an audit event")
	}

	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fmt.Fprint(file, `{"partial":"must-not-survive"}`); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := newCapabilityAuditJournal(path, 2_048, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := reopened.Audit(t.Context(), capabilityAuditTestEvent("official.policy", "allowed")); err != nil {
		t.Fatal(err)
	}
	if err := reopened.Close(); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(path)
	if err != nil || strings.Contains(string(data), "must-not-survive") || strings.Count(string(data), "\n") != 2 {
		t.Fatalf("recovered journal = %q, error = %v", data, err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	rotating, err := newCapabilityAuditJournal(path, info.Size()+1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := rotating.Audit(t.Context(), capabilityAuditTestEvent("official.policy", "rotated")); err != nil {
		t.Fatal(err)
	}
	if err := rotating.Close(); err != nil {
		t.Fatal(err)
	}
	for _, retained := range []string{path, path + ".1"} {
		if retainedInfo, err := os.Stat(retained); err != nil || retainedInfo.Size() == 0 {
			t.Fatalf("retained audit %s: size=%v error=%v", retained, retainedInfo, err)
		}
	}
	if _, err := os.Stat(path + ".2"); !os.IsNotExist(err) {
		t.Fatalf("audit retention exceeded one archive: %v", err)
	}
}

func capabilityAuditTestEvent(pluginID, reason string) hostapi.AuditEvent {
	return hostapi.AuditEvent{Call: pluginsdk.HostCapabilityCall{
		PluginID: pluginID, InstanceID: "instance-1", Generation: "generation-1",
		Capability: pluginsdk.CapabilityPolicyTrustedSource,
		Actor:      pluginsdk.HostActor{ID: "actor-1", ResourceGroupID: "group-1"},
		Target:     pluginsdk.HostTarget{Kind: "plugin.instance", ID: "instance-1", ResourceGroupID: "group-1"},
	}, Outcome: "allowed", Reason: reason}
}
