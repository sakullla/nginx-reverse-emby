package model

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestSnapshotDecodePreservesHTTPAndL4BackendFields(t *testing.T) {
	raw := []byte(`{
		"desired_version":"2.0.0",
		"desired_revision":88,
		"rules":[
			{
				"frontend_url":"https://edge.example.com",
				"backend_url":"http://legacy.example.internal:8096",
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
				"upstream_host":"legacy-upstream.internal",
				"upstream_port":9001,
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
			"proxy_egress_mode":"relay",
			"relay_chain":[101]
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
	if rule.ProxyEgressMode != "relay" {
		t.Fatalf("ProxyEgressMode = %q", rule.ProxyEgressMode)
	}
	if !reflect.DeepEqual(rule.RelayChain, []int{101}) {
		t.Fatalf("RelayChain = %+v", rule.RelayChain)
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
