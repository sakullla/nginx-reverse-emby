package model

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestSnapshotDecodePreservesHTTPAndL4BackendFields(t *testing.T) {
	raw := []byte(`{
		"desired_version":"2.0.0",
		"desired_revision":88,
		"rules":[
			{
				"frontend_url":"https://edge.example.com",
				"backends":[
					{"url":"http://10.0.0.11:8096"},
					{"url":"http://10.0.0.12:8096"}
				],
				"load_balancing":{"strategy":"random"}
			}
		],
		"l4_rules":[
			{
				"protocol":"tcp",
				"listen_host":"0.0.0.0",
				"listen_port":9443,
				"backends":[
					{"host":"10.0.0.21","port":9001},
					{"host":"10.0.0.22","port":9002}
				],
				"load_balancing":{"strategy":"round_robin"},
				"tuning":{"proxy_protocol":{"decode":true,"send":false}}
			}
		]
	}`)

	var snapshot Snapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		t.Fatalf("decode snapshot: %v", err)
	}

	if len(snapshot.Rules) != 1 {
		t.Fatalf("expected one http rule, got %d", len(snapshot.Rules))
	}
	if !reflect.DeepEqual(snapshot.Rules[0].Backends, []HTTPBackend{
		{URL: "http://10.0.0.11:8096"},
		{URL: "http://10.0.0.12:8096"},
	}) {
		t.Fatalf("unexpected http backends: %+v", snapshot.Rules[0].Backends)
	}
	if snapshot.Rules[0].LoadBalancing.Strategy != "random" {
		t.Fatalf("expected http load_balancing strategy random, got %q", snapshot.Rules[0].LoadBalancing.Strategy)
	}

	if len(snapshot.L4Rules) != 1 {
		t.Fatalf("expected one l4 rule, got %d", len(snapshot.L4Rules))
	}
	if !reflect.DeepEqual(snapshot.L4Rules[0].Backends, []L4Backend{
		{Host: "10.0.0.21", Port: 9001},
		{Host: "10.0.0.22", Port: 9002},
	}) {
		t.Fatalf("unexpected l4 backends: %+v", snapshot.L4Rules[0].Backends)
	}
	if snapshot.L4Rules[0].LoadBalancing.Strategy != "round_robin" {
		t.Fatalf("expected l4 load_balancing strategy round_robin, got %q", snapshot.L4Rules[0].LoadBalancing.Strategy)
	}
	if !snapshot.L4Rules[0].Tuning.ProxyProtocol.Decode {
		t.Fatalf("expected proxy_protocol.decode=true")
	}
	if snapshot.L4Rules[0].Tuning.ProxyProtocol.Send {
		t.Fatalf("expected proxy_protocol.send=false")
	}
}

func TestSnapshotDecodeEnablesLegacyRuntimeRulesWhenFieldMissing(t *testing.T) {
	var snapshot Snapshot
	if err := json.Unmarshal([]byte(`{
		"rules":[{"id":1,"frontend_url":"https://panel.example.com","backends":[{"url":"http://127.0.0.1:8080"}]}],
		"l4_rules":[{"id":2,"protocol":"tcp","listen_host":"0.0.0.0","listen_port":8443,"backends":[{"host":"127.0.0.1","port":8080}]}]
	}`), &snapshot); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}

	if len(snapshot.Rules) != 1 || !snapshot.Rules[0].Enabled {
		t.Fatalf("legacy HTTP runtime rules = %+v, want one enabled rule", snapshot.Rules)
	}
	if len(snapshot.L4Rules) != 1 || !snapshot.L4Rules[0].Enabled {
		t.Fatalf("legacy L4 runtime rules = %+v, want one enabled rule", snapshot.L4Rules)
	}
}

func TestSnapshotJSONPreservesExplicitDisabledRuntimeRules(t *testing.T) {
	snapshot := Snapshot{
		Rules:   []HTTPRule{{ID: 1, FrontendURL: "https://panel.example.com", Enabled: false}},
		L4Rules: []L4Rule{{ID: 2, Protocol: "tcp", ListenHost: "0.0.0.0", ListenPort: 8443, Enabled: false}},
	}

	raw, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if got := strings.Count(string(raw), `"enabled":false`); got != 2 {
		t.Fatalf("Marshal() JSON = %s, explicit disabled field count = %d, want 2", raw, got)
	}

	var decoded Snapshot
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if decoded.Rules[0].Enabled || decoded.L4Rules[0].Enabled {
		t.Fatalf("decoded explicit disabled rules = HTTP %v L4 %v", decoded.Rules[0].Enabled, decoded.L4Rules[0].Enabled)
	}
}

func TestSnapshotDecodePreservesL4WireGuardInboundMode(t *testing.T) {
	raw := []byte(`{
		"l4_rules":[
			{
				"protocol":"udp",
				"listen_host":"0.0.0.0",
				"listen_port":51820,
				"listen_mode":"wireguard",
				"wireguard_inbound_mode":"transparent",
				"wireguard_profile_id":7,
				"backends":[{"host":"10.0.0.21","port":9001}]
			}
		]
	}`)

	var snapshot Snapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		t.Fatalf("decode snapshot: %v", err)
	}

	if len(snapshot.L4Rules) != 1 {
		t.Fatalf("expected one l4 rule, got %d", len(snapshot.L4Rules))
	}
	if snapshot.L4Rules[0].WireGuardInboundMode != "transparent" {
		t.Fatalf("WireGuardInboundMode = %q, want transparent", snapshot.L4Rules[0].WireGuardInboundMode)
	}
}

func TestSnapshotDecodePreservesHTTPWireGuardEntry(t *testing.T) {
	raw := []byte(`{
		"rules":[
			{
				"agent_id":"remote-http",
				"frontend_url":"http://app.internal",
				"backends":[{"url":"http://127.0.0.1:8096"}],
				"wireguard_entry_enabled":true,
				"wireguard_profile_id":7,
				"wireguard_entry_listen_host":"10.8.0.1",
				"wireguard_entry_listen_port":8080
			}
		]
	}`)

	var snapshot Snapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		t.Fatalf("decode snapshot: %v", err)
	}

	if len(snapshot.Rules) != 1 {
		t.Fatalf("expected one http rule, got %d", len(snapshot.Rules))
	}
	rule := snapshot.Rules[0]
	if !rule.WireGuardEntryEnabled || rule.WireGuardProfileID == nil || *rule.WireGuardProfileID != 7 || rule.WireGuardEntryListenHost != "10.8.0.1" || rule.WireGuardEntryListenPort != 8080 {
		t.Fatalf("HTTP WireGuard entry = %+v", rule)
	}
	if rule.AgentID != "remote-http" {
		t.Fatalf("HTTP rule AgentID = %q, want remote-http", rule.AgentID)
	}
}

func TestSnapshotDecodePreservesEgressProfilesAndRuleProfileIDs(t *testing.T) {
	raw := []byte(`{
		"egress_profiles":[
			{
				"id":11,
				"name":"direct-default",
				"type":"direct",
				"enabled":true,
				"description":"default path",
				"revision":5
			},
			{
				"id":12,
				"name":"wg-egress",
				"type":"wireguard",
				"wireguard_config":{
					"private_key":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=",
					"addresses":["10.20.0.1/24"],
					"peers":[{"name":"peer-a","public_key":"BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB=","endpoint":"peer.example.com:51820","allowed_ips":["10.20.0.2/32"]}],
					"dns":["1.1.1.1","8.8.8.8"],
					"mtu":1420
				},
				"enabled":false
			}
		],
		"rules":[{"frontend_url":"https://app.example.com","egress_profile_id":11}],
		"l4_rules":[{"agent_id":"l4-agent","protocol":"tcp","listen_host":"0.0.0.0","listen_port":8443,"egress_profile_id":12}]
	}`)

	var snapshot Snapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		t.Fatalf("decode snapshot: %v", err)
	}

	if len(snapshot.EgressProfiles) != 2 {
		t.Fatalf("expected two egress profiles, got %d", len(snapshot.EgressProfiles))
	}
	direct := snapshot.EgressProfiles[0]
	if direct.ID != 11 || direct.Type != "direct" || !direct.Enabled || direct.Description != "default path" || direct.Revision != 5 {
		t.Fatalf("unexpected direct egress profile: %+v", direct)
	}
	if snapshot.EgressProfiles[1].WireGuardConfig == nil {
		t.Fatal("expected wireguard config to decode")
	}
	wg := snapshot.EgressProfiles[1].WireGuardConfig
	if wg.PrivateKey != "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=" {
		t.Fatalf("wireguard private_key = %q", wg.PrivateKey)
	}
	if !reflect.DeepEqual(wg.Addresses, []string{"10.20.0.1/24"}) {
		t.Fatalf("wireguard addresses = %+v", wg.Addresses)
	}
	if len(wg.Peers) != 1 || wg.Peers[0].PublicKey != "BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB=" {
		t.Fatalf("wireguard peers = %+v", wg.Peers)
	}
	if !reflect.DeepEqual(wg.DNS, []string{"1.1.1.1", "8.8.8.8"}) || wg.MTU != 1420 {
		t.Fatalf("wireguard dns/mtu = %+v", wg)
	}

	if len(snapshot.Rules) != 1 {
		t.Fatalf("expected one http rule, got %d", len(snapshot.Rules))
	}
	if snapshot.Rules[0].EgressProfileID == nil || *snapshot.Rules[0].EgressProfileID != 11 {
		t.Fatalf("HTTP EgressProfileID = %+v, want 11", snapshot.Rules[0].EgressProfileID)
	}

	if len(snapshot.L4Rules) != 1 {
		t.Fatalf("expected one l4 rule, got %d", len(snapshot.L4Rules))
	}
	if snapshot.L4Rules[0].EgressProfileID == nil || *snapshot.L4Rules[0].EgressProfileID != 12 {
		t.Fatalf("L4 EgressProfileID = %+v, want 12", snapshot.L4Rules[0].EgressProfileID)
	}
	if snapshot.L4Rules[0].AgentID != "l4-agent" {
		t.Fatalf("L4 AgentID = %q, want l4-agent", snapshot.L4Rules[0].AgentID)
	}
}

func TestEgressProfileJSONShapeRetainsControlPlaneRequiredFields(t *testing.T) {
	raw, err := json.Marshal(EgressProfile{
		ID:       31,
		Name:     "disabled direct",
		Type:     "direct",
		Enabled:  false,
		Revision: 0,
		WireGuardConfig: &EgressWireGuardConfig{
			Addresses: nil,
			Peers:     nil,
		},
	})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	payload := string(raw)
	for _, field := range []string{`"enabled":false`, `"revision":0`, `"addresses":null`, `"peers":null`} {
		if !strings.Contains(payload, field) {
			t.Fatalf("egress profile JSON = %s, want field %s", payload, field)
		}
	}
}

func TestSnapshotDecodePreservesRelayBindAndPublicFields(t *testing.T) {
	raw := []byte(`{
		"desired_version":"2.1.0",
		"desired_revision":91,
		"relay_listeners":[
			{
				"id":31,
				"agent_id":"remote-agent-5",
				"name":"relay-a",
				"listen_host":"127.0.0.1",
				"bind_hosts":["127.0.0.1","127.0.0.2"],
				"listen_port":9443,
				"public_host":"relay.example.com",
				"public_port":443,
				"enabled":true,
				"tls_mode":"pin_only",
				"pin_set":[{"type":"sha256","value":"pin-value"}],
				"revision":7
			}
		]
	}`)

	var snapshot Snapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		t.Fatalf("decode snapshot: %v", err)
	}

	if len(snapshot.RelayListeners) != 1 {
		t.Fatalf("expected one relay listener, got %d", len(snapshot.RelayListeners))
	}
	if !reflect.DeepEqual(snapshot.RelayListeners[0].BindHosts, []string{"127.0.0.1", "127.0.0.2"}) {
		t.Fatalf("unexpected relay bind_hosts: %+v", snapshot.RelayListeners[0].BindHosts)
	}
	if snapshot.RelayListeners[0].PublicHost != "relay.example.com" {
		t.Fatalf("expected relay public_host relay.example.com, got %q", snapshot.RelayListeners[0].PublicHost)
	}
	if snapshot.RelayListeners[0].PublicPort != 443 {
		t.Fatalf("expected relay public_port 443, got %d", snapshot.RelayListeners[0].PublicPort)
	}
}

func TestSnapshotDecodePreservesWireGuardProfiles(t *testing.T) {
	raw := []byte(`{
		"wireguard_profiles":[
			{
				"id":7,
				"agent_id":"remote-wg",
				"name":"wg enabled",
				"mode":"generic_wireguard",
				"private_key":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=",
				"listen_port":51820,
				"public_endpoint":"wg.example.com:51820",
				"addresses":["10.10.0.1/24"],
				"peers":[{"name":"peer-a","public_key":"BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB=","preshared_key":"CCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCC=","endpoint":"peer.example.com:51820","allowed_ips":["10.10.0.2/32"],"persistent_keepalive_seconds":25}],
				"dns":["1.1.1.1"],
				"mtu":1420,
				"enabled":true,
				"tags":["edge"],
				"revision":9
			}
		]
	}`)

	var snapshot Snapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		t.Fatalf("decode snapshot: %v", err)
	}

	if len(snapshot.WireGuardProfiles) != 1 {
		t.Fatalf("expected one WireGuard profile, got %d", len(snapshot.WireGuardProfiles))
	}
	profile := snapshot.WireGuardProfiles[0]
	if profile.PrivateKey != "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=" {
		t.Fatalf("private_key = %q", profile.PrivateKey)
	}
	if profile.PublicEndpoint != "wg.example.com:51820" {
		t.Fatalf("public_endpoint = %q", profile.PublicEndpoint)
	}
	if !reflect.DeepEqual(profile.Addresses, []string{"10.10.0.1/24"}) {
		t.Fatalf("addresses = %+v", profile.Addresses)
	}
	if len(profile.Peers) != 1 || profile.Peers[0].PresharedKey != "CCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCC=" {
		t.Fatalf("peers = %+v", profile.Peers)
	}
	if profile.Revision != 9 || !profile.Enabled || profile.Mode != "generic_wireguard" {
		t.Fatalf("unexpected WireGuard profile metadata: %+v", profile)
	}
}

func TestSnapshotDecodePreservesAgentConfigAndL4ProxyEntryFields(t *testing.T) {
	raw := []byte(`{
		"agent_config":{"outbound_proxy_url":"socks://127.0.0.1:1080"},
		"l4_rules":[{
			"id":1,
			"protocol":"tcp",
			"listen_host":"127.0.0.1",
			"listen_port":1080,
			"listen_mode":"proxy",
			"proxy_entry_auth":{"enabled":true,"username":"u","password":"p"},
			"relay_layers":[[101]]
		}]
	}`)

	var snapshot Snapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		t.Fatalf("decode snapshot: %v", err)
	}

	if snapshot.AgentConfig.OutboundProxyURL != "socks://127.0.0.1:1080" {
		t.Fatalf("AgentConfig.OutboundProxyURL = %q", snapshot.AgentConfig.OutboundProxyURL)
	}
	if len(snapshot.L4Rules) != 1 {
		t.Fatalf("expected one l4 rule, got %d", len(snapshot.L4Rules))
	}
	rule := snapshot.L4Rules[0]
	if rule.ListenMode != "proxy" {
		t.Fatalf("ListenMode = %q", rule.ListenMode)
	}
	if !rule.ProxyEntryAuth.Enabled || rule.ProxyEntryAuth.Username != "u" || rule.ProxyEntryAuth.Password != "p" {
		t.Fatalf("ProxyEntryAuth = %+v", rule.ProxyEntryAuth)
	}
	if !reflect.DeepEqual(rule.RelayLayers, [][]int{{101}}) {
		t.Fatalf("RelayLayers = %+v", rule.RelayLayers)
	}
}

func TestSnapshotDecodePreservesTrafficStatsInterval(t *testing.T) {
	var snapshot Snapshot
	if err := json.Unmarshal([]byte(`{
		"desired_version":"next",
		"desired_revision":4,
		"agent_config":{"traffic_stats_interval":"30s"}
	}`), &snapshot); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}

	if !snapshot.HasAgentConfig() {
		t.Fatal("HasAgentConfig() = false, want true")
	}
	if snapshot.AgentConfig.TrafficStatsInterval != "30s" {
		t.Fatalf("AgentConfig.TrafficStatsInterval = %q, want 30s", snapshot.AgentConfig.TrafficStatsInterval)
	}
}

func TestSnapshotDecodePreservesTrafficBlockingConfig(t *testing.T) {
	var snapshot Snapshot
	err := json.Unmarshal([]byte(`{"agent_config":{"traffic_stats_enabled":false,"traffic_blocked":true,"traffic_block_reason":"monthly quota exceeded"}}`), &snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.AgentConfig.TrafficStatsEnabled == nil {
		t.Fatal("TrafficStatsEnabled = nil, want explicit false")
	}
	if *snapshot.AgentConfig.TrafficStatsEnabled {
		t.Fatal("TrafficStatsEnabled = true, want false")
	}
	if !snapshot.AgentConfig.TrafficBlocked {
		t.Fatal("TrafficBlocked = false, want true")
	}
	if snapshot.AgentConfig.TrafficBlockReason != "monthly quota exceeded" {
		t.Fatalf("TrafficBlockReason = %q", snapshot.AgentConfig.TrafficBlockReason)
	}
}

func TestSnapshotDecodePreservesDDNSConfig(t *testing.T) {
	raw := []byte(`{
		"ddns_config":{
			"domain":"edge.example.com",
			"ipv4":{"enabled":true,"source":"public_api"},
			"ipv6":{"enabled":true,"source":"interface","interface":"eth0"}
		}
	}`)

	var snapshot Snapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		t.Fatalf("decode snapshot: %v", err)
	}
	if snapshot.DDNSConfig == nil {
		t.Fatal("expected DDNSConfig to decode")
	}
	if snapshot.DDNSConfig.Domain != "edge.example.com" {
		t.Fatalf("DDNSConfig.Domain = %q, want edge.example.com", snapshot.DDNSConfig.Domain)
	}
	if !snapshot.DDNSConfig.IPv4.Enabled || snapshot.DDNSConfig.IPv4.Source != "public_api" || snapshot.DDNSConfig.IPv4.Interface != "" {
		t.Fatalf("unexpected ipv4 family: %+v", snapshot.DDNSConfig.IPv4)
	}
	if !snapshot.DDNSConfig.IPv6.Enabled || snapshot.DDNSConfig.IPv6.Source != "interface" || snapshot.DDNSConfig.IPv6.Interface != "eth0" {
		t.Fatalf("unexpected ipv6 family: %+v", snapshot.DDNSConfig.IPv6)
	}
}

// TestDDNSConfigJSONCarriesNoCredential enforces R7 at the agent wire layer:
// the dispatched DDNSExtractConfig is exactly enabled + domain + ipv4 + ipv6
// with no token/secret/key/password surface. Cloudflare credentials live only
// in the master environment and never reach the agent.
func TestDDNSConfigJSONCarriesNoCredential(t *testing.T) {
	raw, err := json.Marshal(DDNSExtractConfig{
		Enabled: true,
		Domain:  "edge.example.com",
		IPv4:    DDNSFamily{Enabled: true, Source: "public_api"},
		IPv6:    DDNSFamily{Enabled: true, Source: "interface", Interface: "eth0"},
	})
	if err != nil {
		t.Fatalf("json.Marshal(DDNSExtractConfig) error = %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("json.Unmarshal(DDNSExtractConfig) error = %v", err)
	}
	if len(decoded) != 4 {
		t.Fatalf("DDNSExtractConfig JSON top-level keys = %d, want exactly 4 (enabled+domain+ipv4+ipv6): %s", len(decoded), raw)
	}
	for _, key := range []string{"enabled", "domain", "ipv4", "ipv6"} {
		if _, ok := decoded[key]; !ok {
			t.Fatalf("DDNSExtractConfig JSON missing expected key %q: %s", key, raw)
		}
	}

	lower := strings.ToLower(string(raw))
	for _, forbidden := range []string{"token", "secret", "api_key", "apikey", "password", "credential"} {
		if strings.Contains(lower, forbidden) {
			t.Fatalf("DDNSExtractConfig wire JSON leaked credential-ish key %q: %s", forbidden, raw)
		}
	}
}

// TestDDNSConfigDecodeDerivesEnabledForLegacyDispatch locks the rollout
// default: a dispatch from a master predating the enabled switch carries no
// "enabled" key; the agent derives it from the per-family flags so upgrading
// the agent first never silently halts extraction. An explicit key always wins.
func TestDDNSConfigDecodeDerivesEnabledForLegacyDispatch(t *testing.T) {
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
			var cfg DDNSExtractConfig
			if err := json.Unmarshal([]byte(tc.raw), &cfg); err != nil {
				t.Fatalf("json.Unmarshal(%q) error = %v", tc.raw, err)
			}
			if cfg.Enabled != tc.want {
				t.Fatalf("Enabled = %v, want %v (raw %q)", cfg.Enabled, tc.want, tc.raw)
			}
		})
	}
}
