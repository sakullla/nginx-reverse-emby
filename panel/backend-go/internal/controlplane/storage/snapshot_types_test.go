package storage

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSnapshotRuleJSONOmitsLegacyFields(t *testing.T) {
	t.Parallel()
	raw, err := json.Marshal(Snapshot{
		Revision: 12,
		Rules: []HTTPRule{{
			ID:          1,
			AgentID:     "local",
			FrontendURL: "https://emby.example.com",
			BackendURL:  "http://legacy:8096",
			Backends:    []HTTPBackend{{URL: "http://emby:8096"}},
			RelayChain:  []int{7},
			RelayLayers: [][]int{{7}},
		}},
		L4Rules: []L4Rule{{
			ID:           2,
			AgentID:      "local",
			Name:         "tcp",
			Protocol:     "tcp",
			ListenHost:   "0.0.0.0",
			ListenPort:   25565,
			UpstreamHost: "legacy",
			UpstreamPort: 25566,
			Backends:     []L4Backend{{Host: "upstream", Port: 25567}},
			RelayChain:   []int{8},
			RelayLayers:  [][]int{{8}},
		}},
	})
	if err != nil {
		t.Fatalf("json.Marshal(Snapshot) error = %v", err)
	}

	var payload struct {
		Rules   []map[string]any `json:"rules"`
		L4Rules []map[string]any `json:"l4_rules"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("json.Unmarshal(Snapshot) error = %v", err)
	}
	if len(payload.Rules) != 1 || len(payload.L4Rules) != 1 {
		t.Fatalf("snapshot rule counts = http %d, l4 %d", len(payload.Rules), len(payload.L4Rules))
	}
	for _, key := range []string{"backend_url", "relay_chain"} {
		if _, ok := payload.Rules[0][key]; ok {
			t.Fatalf("snapshot HTTP rule JSON exposed legacy field %q: %s", key, raw)
		}
	}
	for _, key := range []string{"upstream_host", "upstream_port", "relay_chain"} {
		if _, ok := payload.L4Rules[0][key]; ok {
			t.Fatalf("snapshot L4 rule JSON exposed legacy field %q: %s", key, raw)
		}
	}
}

// TestDDNSConfigJSONCarriesNoCredential enforces R7 at the wire-format layer:
// the dispatched DDNSConfig is exactly enabled + domain + ipv4 + ipv6, with no
// token, secret, key, or password surface. CF credentials live only in the
// master env. The enabled switch always serializes (no omitempty) so an
// explicit off survives persist/dispatch round trips.
func TestDDNSConfigJSONCarriesNoCredential(t *testing.T) {
	t.Parallel()
	raw, err := json.Marshal(DDNSConfig{
		Enabled: true,
		Domain:  "edge.example.com",
		IPv4:    DDNSFamily{Enabled: true, Source: "public_api"},
		IPv6:    DDNSFamily{Enabled: true, Source: "interface", Interface: "eth0"},
	})
	if err != nil {
		t.Fatalf("json.Marshal(DDNSConfig) error = %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("json.Unmarshal(DDNSConfig) error = %v", err)
	}
	if len(decoded) != 4 {
		t.Fatalf("DDNSConfig JSON top-level keys = %d, want exactly 4 (enabled+domain+ipv4+ipv6): %s", len(decoded), raw)
	}
	for _, key := range []string{"enabled", "domain", "ipv4", "ipv6"} {
		if _, ok := decoded[key]; !ok {
			t.Fatalf("DDNSConfig JSON missing expected key %q: %s", key, raw)
		}
	}

	lower := strings.ToLower(string(raw))
	for _, forbidden := range []string{"token", "secret", "api_key", "apikey", "password", "credential"} {
		if strings.Contains(lower, forbidden) {
			t.Fatalf("DDNSConfig wire JSON leaked credential-ish key %q: %s", forbidden, raw)
		}
	}
}

// TestDDNSConfigUnmarshalDerivesEnabledForLegacyJSON locks the migration
// default: rows persisted before the enabled switch existed carry no "enabled"
// key and derive it from the per-family flags, so upgrading never silently
// disables working DDNS. An explicit "enabled" key always wins.
func TestDDNSConfigUnmarshalDerivesEnabledForLegacyJSON(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		raw  string
		want bool
	}{
		{"legacy family on", `{"domain":"edge.example.com","ipv4":{"enabled":true},"ipv6":{"enabled":false}}`, true},
		{"legacy all families off", `{"domain":"edge.example.com","ipv4":{"enabled":false},"ipv6":{"enabled":false}}`, false},
		{"explicit disabled wins over family", `{"enabled":false,"domain":"edge.example.com","ipv4":{"enabled":true}}`, false},
		{"explicit enabled respected", `{"enabled":true,"domain":"edge.example.com"}`, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var cfg DDNSConfig
			if err := json.Unmarshal([]byte(tc.raw), &cfg); err != nil {
				t.Fatalf("json.Unmarshal(%q) error = %v", tc.raw, err)
			}
			if cfg.Enabled != tc.want {
				t.Fatalf("Enabled = %v, want %v (raw %q)", cfg.Enabled, tc.want, tc.raw)
			}
		})
	}
}

func TestSnapshotEgressProfileJSONShape(t *testing.T) {
	t.Parallel()
	egressProfileID := 41
	raw, err := json.Marshal(Snapshot{
		Rules: []HTTPRule{{
			ID:              1,
			FrontendURL:     "https://emby.example.com",
			EgressProfileID: &egressProfileID,
		}},
		L4Rules: []L4Rule{{
			ID:              2,
			Protocol:        "tcp",
			ListenHost:      "0.0.0.0",
			ListenPort:      25565,
			EgressProfileID: &egressProfileID,
		}},
		EgressProfiles: []EgressProfile{{
			ID:       egressProfileID,
			Name:     "socks exit",
			Type:     "socks",
			ProxyURL: "socks5://proxy.example.com:1080",
			Enabled:  true,
			Revision: 7,
		}},
	})
	if err != nil {
		t.Fatalf("json.Marshal(Snapshot) error = %v", err)
	}

	var payload struct {
		Rules          []map[string]any `json:"rules"`
		L4Rules        []map[string]any `json:"l4_rules"`
		EgressProfiles []map[string]any `json:"egress_profiles"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("json.Unmarshal(Snapshot) error = %v", err)
	}
	if got := payload.Rules[0]["egress_profile_id"]; got != float64(egressProfileID) {
		t.Fatalf("HTTP egress_profile_id = %#v, want %d; raw=%s", got, egressProfileID, raw)
	}
	if got := payload.L4Rules[0]["egress_profile_id"]; got != float64(egressProfileID) {
		t.Fatalf("L4 egress_profile_id = %#v, want %d; raw=%s", got, egressProfileID, raw)
	}
	if len(payload.EgressProfiles) != 1 {
		t.Fatalf("egress profile count = %d, want 1; raw=%s", len(payload.EgressProfiles), raw)
	}
	if got := payload.EgressProfiles[0]["proxy_url"]; got != "socks5://proxy.example.com:1080" {
		t.Fatalf("proxy_url = %#v, want socks endpoint; raw=%s", got, raw)
	}
}

func TestSnapshotEgressProfilesIgnoreUnsupportedStoredRows(t *testing.T) {
	t.Parallel()
	rows := []EgressProfileRow{
		{ID: 1, Name: "enabled", Type: "direct", Enabled: true},
		{ID: 2, Name: "disabled", Type: "http", ProxyURL: "http://127.0.0.1:8080", Enabled: false},
		{ID: 3, Name: "retired", Type: "unsupported", Enabled: true},
	}

	runtimeProfiles := SnapshotEgressProfiles(rows)
	if len(runtimeProfiles) != 1 || runtimeProfiles[0].ID != 1 {
		t.Fatalf("runtime profiles = %+v, want only enabled supported row", runtimeProfiles)
	}
	intentProfiles := SnapshotEgressProfilesForIntent(rows)
	if len(intentProfiles) != 2 || intentProfiles[0].ID != 1 || intentProfiles[1].ID != 2 {
		t.Fatalf("intent profiles = %+v, want enabled and disabled supported rows", intentProfiles)
	}
}

func TestFilterSupportedSnapshotResourcesRemovesUnsupportedGraph(t *testing.T) {
	t.Parallel()
	directID := 1
	unsupportedID := 99
	snapshot := Snapshot{
		Revision: 7,
		Rules: []HTTPRule{
			{ID: 1, EgressProfileID: &directID},
			{ID: 2, EgressProfileID: &unsupportedID},
			{ID: 3, RelayLayers: [][]int{{90}}},
		},
		L4Rules: []L4Rule{
			{ID: 4, Protocol: "tcp", ListenMode: "tcp"},
			{ID: 5, Protocol: "tcp", ListenMode: "unsupported"},
			{ID: 6, Protocol: "tcp", ListenMode: "tcp", RelayLayers: [][]int{{90}}},
		},
		RelayListeners: []RelayListener{
			{ID: 10, TransportMode: "tls_tcp"},
			{ID: 90, TransportMode: "unsupported"},
		},
		EgressProfiles: []EgressProfile{
			{ID: directID, Type: "direct"},
			{ID: unsupportedID, Type: "unsupported"},
		},
	}

	filtered, changed := FilterSupportedSnapshotResources(snapshot)
	if !changed {
		t.Fatal("FilterSupportedSnapshotResources() changed = false, want true")
	}
	if len(filtered.Rules) != 1 || filtered.Rules[0].ID != 1 {
		t.Fatalf("filtered HTTP rules = %+v, want only rule 1", filtered.Rules)
	}
	if len(filtered.L4Rules) != 1 || filtered.L4Rules[0].ID != 4 {
		t.Fatalf("filtered L4 rules = %+v, want only rule 4", filtered.L4Rules)
	}
	if len(filtered.RelayListeners) != 1 || filtered.RelayListeners[0].ID != 10 {
		t.Fatalf("filtered relays = %+v, want only relay 10", filtered.RelayListeners)
	}
	if len(filtered.EgressProfiles) != 1 || filtered.EgressProfiles[0].ID != directID {
		t.Fatalf("filtered egress profiles = %+v, want only profile %d", filtered.EgressProfiles, directID)
	}
	if len(snapshot.Rules) != 3 || len(snapshot.L4Rules) != 3 || len(snapshot.RelayListeners) != 2 || len(snapshot.EgressProfiles) != 2 {
		t.Fatal("FilterSupportedSnapshotResources() mutated its input")
	}
	if _, changed := FilterSupportedSnapshotResources(filtered); changed {
		t.Fatal("FilterSupportedSnapshotResources() is not idempotent")
	}
}

func TestFilterSupportedSnapshotResourceGraphRemovesCrossSnapshotReferences(t *testing.T) {
	t.Parallel()
	filtered, changed := FilterSupportedSnapshotResourceGraph([]Snapshot{
		{Revision: 1, Rules: []HTTPRule{{ID: 1, RelayLayers: [][]int{{90}}}}},
		{Revision: 1, RelayListeners: []RelayListener{{ID: 90, TransportMode: "unsupported"}}},
	})
	if !changed {
		t.Fatal("FilterSupportedSnapshotResourceGraph() changed = false, want true")
	}
	if len(filtered) != 2 || len(filtered[0].Rules) != 0 || len(filtered[1].RelayListeners) != 0 {
		t.Fatalf("filtered graph = %+v", filtered)
	}
}
