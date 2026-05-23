package storage

import (
	"encoding/json"
	"testing"
)

func TestSnapshotRuleJSONOmitsLegacyFields(t *testing.T) {
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

func TestSnapshotWireGuardProfileJSONPreservesPublicEndpoint(t *testing.T) {
	raw, err := json.Marshal(Snapshot{
		WireGuardProfiles: []WireGuardProfile{{
			ID:             7,
			AgentID:        "remote-wg",
			Name:           "wg",
			Mode:           "generic_wireguard",
			ListenPort:     51820,
			PublicEndpoint: "wg.example.com:51820",
			Addresses:      []string{"10.10.0.1/24"},
			Enabled:        true,
			Revision:       9,
		}},
	})
	if err != nil {
		t.Fatalf("json.Marshal(Snapshot) error = %v", err)
	}

	var payload struct {
		WireGuardProfiles []map[string]any `json:"wireguard_profiles"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("json.Unmarshal(Snapshot) error = %v", err)
	}
	if len(payload.WireGuardProfiles) != 1 {
		t.Fatalf("wireguard profile count = %d, want 1", len(payload.WireGuardProfiles))
	}
	if got := payload.WireGuardProfiles[0]["public_endpoint"]; got != "wg.example.com:51820" {
		t.Fatalf("public_endpoint = %#v, want wg.example.com:51820; raw=%s", got, raw)
	}
}
