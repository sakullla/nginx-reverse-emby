//go:build !fast

package storage

import (
	"encoding/json"
	sdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
	"reflect"
	"testing"
)

func TestComposedEntryPolicyIsolationAndCompatibility(t *testing.T) {
	store := newTrafficTestStore(t, true)
	mode := sdk.PolicyModeObserve
	ip := PolicyStage{Kind: "ip", PolicyID: "ip-default", InstanceID: "ip-default", Automatic: true, Config: json.RawMessage(`{"global_deny":["cn-44"]}`), PolicySettings: &sdk.PolicySettingsSnapshot{Version: sdk.PolicySettingsVersion{Revision: 1, InstanceVersion: 1}, Settings: sdk.PolicyModeSettings{Handling: sdk.PolicyModeHandlingRaw, DefaultMode: &mode}}}
	waf := PolicyStage{Kind: "waf", PolicyID: "waf", InstanceID: "waf", Automatic: true, Config: json.RawMessage(`{"unchanged":true}`)}
	rate := PolicyStage{Kind: "rate", PolicyID: "rate", InstanceID: "rate", Config: json.RawMessage(`{"limit":4}`)}
	catalog := []PluginPolicy{{ID: "ip-default", Revision: 1, Stages: []PolicyStage{ip}}, {ID: "existing", Revision: 1, Stages: []PolicyStage{rate, waf}}}
	original := &PolicyRef{ID: "existing", Overlay: json.RawMessage(`{"mode":"deny"}`)}
	before := ClonePolicyRef(original)
	entry := sdk.PolicyEntryTarget{NodeID: "local", Kind: sdk.PolicyEntryHTTP, ID: "1"}
	ref, policy, err := store.ComposeEntryPolicy(t.Context(), entry, original, catalog)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(original, before) || len(policy.Stages) != 3 || policy.Stages[0].Kind != "ip" || policy.Stages[1].Kind != "rate" || policy.Stages[2].Kind != "waf" {
		t.Fatal("composition overwrote original or order", policy)
	}
	envelope, err := sdk.DecodePolicyOverlay(ref.Overlay, sdk.PolicyOverlayDecodeContext{Format: ref.OverlayFormat})
	if err != nil || len(envelope.Stages) != 1 || envelope.Stages[0].PolicyID != "waf" || string(envelope.Stages[0].Payload) != `{"mode":"deny"}` {
		t.Fatal("legacy WAF overlay leaked into other stages", envelope, err)
	}
	for _, kind := range []string{sdk.PolicyEntryTCP, sdk.PolicyEntryUDP, sdk.PolicyEntryManagedTCP, sdk.PolicyEntryManagedUDP} {
		entry.Kind = kind
		got, stages, err := store.ComposeEntryPolicy(t.Context(), entry, nil, catalog)
		if err != nil || got == nil || len(stages.Stages) != 1 || stages.Stages[0].Kind != "ip" {
			t.Fatal("nonHTTP default attachment", kind, got, err)
		}
		if _, _, err := store.ComposeEntryPolicy(t.Context(), entry, original, catalog); err == nil {
			t.Fatal("WAF applied outside HTTP")
		}
	}
	entry.Kind = sdk.PolicyEntryHTTP
	duplicate := ip
	duplicate.PolicyID = "independent-ip"
	duplicate.InstanceID = "independent-ip"
	duplicate.Automatic = false
	catalog = append(catalog, PluginPolicy{ID: "conflict", Stages: []PolicyStage{duplicate}})
	if _, _, err := store.ComposeEntryPolicy(t.Context(), entry, &PolicyRef{ID: "conflict"}, catalog); err == nil {
		t.Fatal("duplicate independent IP accepted")
	}
	legacy := catalog[1:2]
	kept, composed, err := store.ComposeEntryPolicy(t.Context(), entry, original, legacy)
	if err != nil || composed != nil || !reflect.DeepEqual(kept, original) {
		t.Fatal("legacy chain changed without new IP", kept, err)
	}
}

func TestEntryPolicyModesAreRetiredWithExactStoredEntry(t *testing.T) {
	store := newTrafficTestStore(t, true)
	httpOne := HTTPRuleRow{ID: 1, AgentID: "local"}
	httpTwo := HTTPRuleRow{ID: 2, AgentID: "local"}
	tcp := L4RuleRow{ID: 3, AgentID: "local", Protocol: "tcp"}
	udp := L4RuleRow{ID: 4, AgentID: "local", Protocol: "udp"}
	if err := store.SaveHTTPRules(t.Context(), "local", []HTTPRuleRow{httpOne, httpTwo}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveL4Rules(t.Context(), "local", []L4RuleRow{tcp, udp}); err != nil {
		t.Fatal(err)
	}
	rows := []PluginPolicyEntryModeRow{
		{InstanceID: "ip-a", NodeID: "local", Kind: sdk.PolicyEntryHTTP, EntryID: "1", Mode: "observe"},
		{InstanceID: "ip-b", NodeID: "local", Kind: sdk.PolicyEntryHTTP, EntryID: "1", Mode: "observe"},
		{InstanceID: "ip-a", NodeID: "local", Kind: sdk.PolicyEntryHTTP, EntryID: "2", Mode: "observe"},
		{InstanceID: "ip-a", NodeID: "edge", Kind: sdk.PolicyEntryHTTP, EntryID: "1", Mode: "observe"},
		{InstanceID: "ip-a", NodeID: "local", Kind: sdk.PolicyEntryTCP, EntryID: "3", Mode: "observe"},
		{InstanceID: "ip-a", NodeID: "local", Kind: sdk.PolicyEntryUDP, EntryID: "4", Mode: "observe"},
	}
	for _, row := range rows {
		if err := store.PutPluginPolicyEntryMode(t.Context(), row, false); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.SaveHTTPRules(t.Context(), "local", []HTTPRuleRow{httpTwo}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveL4Rules(t.Context(), "local", []L4RuleRow{{ID: 3, AgentID: "local", Protocol: "udp"}, udp}); err != nil {
		t.Fatal(err)
	}
	for _, instanceID := range []string{"ip-a", "ip-b"} {
		got, err := store.ListPluginPolicyEntryModes(t.Context(), instanceID)
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range got {
			if row.NodeID == "local" && row.EntryID == "1" && row.Kind == sdk.PolicyEntryHTTP {
				t.Fatalf("deleted HTTP incarnation retained mode: %+v", row)
			}
			if row.NodeID == "local" && row.EntryID == "3" && row.Kind == sdk.PolicyEntryTCP {
				t.Fatalf("replaced TCP incarnation retained mode: %+v", row)
			}
		}
	}
	kept, err := store.ListPluginPolicyEntryModes(t.Context(), "ip-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(kept) != 3 {
		t.Fatalf("entry cleanup removed unrelated modes: %+v", kept)
	}
	if err := store.SaveL4Rules(t.Context(), "local", []L4RuleRow{{ID: 3, AgentID: "local", Protocol: "udp"}}); err != nil {
		t.Fatal(err)
	}
	kept, err = store.ListPluginPolicyEntryModes(t.Context(), "ip-a")
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range kept {
		if row.NodeID == "local" && row.EntryID == "4" && row.Kind == sdk.PolicyEntryUDP {
			t.Fatalf("deleted UDP incarnation retained mode: %+v", row)
		}
	}
}
