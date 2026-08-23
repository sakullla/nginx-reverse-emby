package pluginsdk

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestHostRuntimeClientUsesPrivateEndpointAndCredential(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix sockets are exercised by the packaged Linux runtime")
	}
	directory := t.TempDir()
	socket := filepath.Join(directory, "host.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != PluginHostCallPath || request.Header.Get(HeaderPluginHostCredential) != "attempt-cookie" {
			http.Error(writer, "denied", http.StatusForbidden)
			return
		}
		_ = json.NewEncoder(writer).Encode(HostRuntimeResponse{Payload: json.RawMessage(`{"ready":true}`)})
	})}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })
	cookieFile := filepath.Join(directory, "cookie")
	if err := os.WriteFile(cookieFile, []byte("attempt-cookie"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(EnvPluginHostEndpoint, "unix:"+socket)
	t.Setenv("NRE_PLUGIN_COOKIE_FILE", cookieFile)
	client, err := NewHostRuntimeClientFromEnvironment()
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Ready bool `json:"ready"`
	}
	if err := client.Call(context.Background(), HostRuntimeCall{Operation: "state.get", Payload: json.RawMessage(`{"key":"catalog"}`)}, &result); err != nil {
		t.Fatal(err)
	}
	if !result.Ready {
		t.Fatal("host runtime response was not decoded")
	}
}

func TestHostRuntimeCallRejectsUnboundedOrInvalidPayload(t *testing.T) {
	if err := (HostRuntimeCall{Operation: "state.get", Payload: json.RawMessage(`{"key":`)}).Validate(); err == nil {
		t.Fatal("invalid JSON payload was accepted")
	}
	if err := (HostRuntimeCall{Operation: "state.get", OperationID: "contains\nnewline"}).Validate(); err == nil {
		t.Fatal("invalid operation identity was accepted")
	}
	if err := (&HostRuntimeClient{}).Call(context.Background(), HostRuntimeCall{Operation: "state.get"}, nil); err == nil {
		t.Fatal("unconfigured host runtime client was accepted")
	}
}

func TestPluginCallAndHTTPRuleRequestsValidateWithoutInterpretingActionNames(t *testing.T) {
	call := PluginCallRequest{AgentID: "edge-a", Name: "compose.apply", Payload: json.RawMessage(`{"yaml":"services:\n  app:\n    image: example\n"}`)}
	if err := call.Validate(); err != nil {
		t.Fatalf("plugin.call envelope: %v", err)
	}
	if err := (HostRuntimeCall{Operation: HostRuntimePluginCall, Payload: json.RawMessage(`{"agent_id":"edge-a","name":"engine.report"}`)}).Validate(); err != nil {
		t.Fatalf("plugin.call host operation: %v", err)
	}
	if err := (PluginCallRequest{AgentID: "edge-a", Name: "contains\nnewline"}).Validate(); err == nil {
		t.Fatal("plugin.call accepted a non-canonical name")
	}
	if err := (HTTPRuleRequest{Action: HTTPRuleActionCreate, AgentID: "edge-a", Domain: "app.example.com", Port: 8096}).Validate(); err != nil {
		t.Fatalf("http.rule create: %v", err)
	}
	if err := (HTTPRuleRequest{Action: HTTPRuleActionCreate, AgentID: "edge-a", Domain: "https://app.example.com/path", Port: 8096}).Validate(); err != nil {
		t.Fatalf("http.rule create https url: %v", err)
	}
	if err := (HTTPRuleRequest{Action: HTTPRuleActionCreate, AgentID: "edge-a", Domain: "https://", Port: 8096}).Validate(); err == nil {
		t.Fatal("http.rule create accepted an empty https url")
	}
	if err := (HTTPRuleRequest{Action: HTTPRuleActionCutover, RuleRef: ""}).Validate(); err == nil {
		t.Fatal("empty rule_ref cutover was accepted")
	}
	if err := (HTTPRuleRequest{Action: HTTPRuleActionCutover, RuleRef: "12"}).Validate(); err != nil {
		t.Fatalf("http.rule cutover: %v", err)
	}
	if err := (HTTPRuleRequest{Action: HTTPRuleActionList, AgentID: "edge-a"}).Validate(); err != nil {
		t.Fatalf("http.rule list: %v", err)
	}
	if err := (HTTPRuleRequest{Action: HTTPRuleActionList, AgentID: "edge-a", Domain: "app.example.com"}).Validate(); err == nil {
		t.Fatal("http.rule list accepted a domain")
	}
	if err := (HostRuntimeCall{Operation: HostRuntimeHTTPRule}).Validate(); err != nil {
		t.Fatalf("http.rule host operation: %v", err)
	}
	if err := (HostRuntimeCall{Operation: HostRuntimeHTTPBackendOffer}).Validate(); err != nil {
		t.Fatalf("http.backend-offer host operation: %v", err)
	}
	if err := (HostRuntimeCall{Operation: HostRuntimeEventEmit}).Validate(); err != nil {
		t.Fatalf("event.emit host operation: %v", err)
	}
}

func TestNormalizeHTTPRuleFrontendPreservesHTTPSAndStripsPath(t *testing.T) {
	for _, test := range []struct {
		input string
		want  string
	}{
		{input: "app.example.com", want: "http://app.example.com"},
		{input: "http://app.example.com", want: "http://app.example.com"},
		{input: "https://app.example.com", want: "https://app.example.com"},
		{input: "https://app.example.com/path?q=1", want: "https://app.example.com"},
		{input: "https://app.example.com:8443/ingress", want: "https://app.example.com:8443"},
		{input: "app.example.com/root", want: "http://app.example.com"},
	} {
		got, err := NormalizeHTTPRuleFrontend(test.input)
		if err != nil {
			t.Fatalf("NormalizeHTTPRuleFrontend(%q): %v", test.input, err)
		}
		if got != test.want {
			t.Fatalf("NormalizeHTTPRuleFrontend(%q) = %q, want %q", test.input, got, test.want)
		}
	}
	for _, input := range []string{"", "https://", "https:///", "ftp://app.example.com", "/app.example.com"} {
		if _, err := NormalizeHTTPRuleFrontend(input); err == nil {
			t.Fatalf("NormalizeHTTPRuleFrontend(%q) was accepted", input)
		}
	}
}

func TestL4RuleRequestCoversFullLifecycle(t *testing.T) {
	enabled := true
	create := L4RuleRequest{
		Action:     L4RuleActionCreate,
		AgentID:    "edge-a",
		Name:       "mapping-1",
		Protocol:   L4RuleProtocolTCP,
		ListenPort: 18096,
		Backends:   []L4RuleBackend{{Host: "127.0.0.1", Port: 19096}},
		Tuning:     &L4RuleTuning{ProxyProtocol: L4RuleProxyProtocolTuning{Send: true}},
		Enabled:    &enabled,
		Tags:       []string{"plugin/example", "mapping-1"},
	}
	if err := create.Validate(); err != nil {
		t.Fatalf("l4.rule create: %v", err)
	}
	encoded, err := json.Marshal(create)
	if err != nil {
		t.Fatal(err)
	}
	var decoded L4RuleRequest
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if err := decoded.Validate(); err != nil {
		t.Fatalf("l4.rule create round-trip: %v", err)
	}
	if decoded.Backends[0].Port != 19096 || decoded.Enabled == nil || !*decoded.Enabled {
		t.Fatal("l4.rule create round-trip lost fields")
	}
	for _, action := range []string{L4RuleActionUpdate, L4RuleActionEnable, L4RuleActionDisable, L4RuleActionDelete} {
		if err := (L4RuleRequest{Action: action, RuleRef: "12"}).Validate(); err != nil {
			t.Fatalf("l4.rule %s: %v", action, err)
		}
		if err := (L4RuleRequest{Action: action}).Validate(); err == nil {
			t.Fatalf("l4.rule %s without rule_ref was accepted", action)
		}
	}
	if err := (L4RuleRequest{Action: L4RuleActionCreate, AgentID: "edge-a", Protocol: L4RuleProtocolTCP, ListenPort: 18096, RuleRef: "12"}).Validate(); err == nil {
		t.Fatal("l4.rule create accepted a rule_ref")
	}
	if err := (L4RuleRequest{Action: L4RuleActionCreate, AgentID: "edge-a", Protocol: "icmp", ListenPort: 18096}).Validate(); err == nil {
		t.Fatal("l4.rule accepted an unsupported protocol")
	}
	if err := (L4RuleRequest{Action: L4RuleActionCreate, AgentID: "edge-a", Protocol: L4RuleProtocolUDP, ListenPort: 53, Backends: []L4RuleBackend{{Host: "127.0.0.1"}}}).Validate(); err == nil {
		t.Fatal("l4.rule accepted a backend without a port")
	}
	if err := (L4RuleRequest{Action: "retire", RuleRef: "12"}).Validate(); err == nil {
		t.Fatal("l4.rule accepted an unsupported action")
	}
	if err := (L4RuleResponse{RuleRef: "12", Enabled: true}).Validate(); err != nil {
		t.Fatalf("l4.rule response: %v", err)
	}
	if err := (HostRuntimeCall{Operation: HostRuntimeL4Rule, Payload: encoded}).Validate(); err != nil {
		t.Fatalf("l4.rule host operation: %v", err)
	}
}

func TestChannelReverseRequestLifecycleAndStatus(t *testing.T) {
	ensure := ChannelReverseRequest{
		Action:       ChannelReverseActionEnsure,
		EntryAgentID: "edge-a",
		ExitAgentID:  "edge-b",
		Protocol:     L4RuleProtocolUDP,
		BackendHost:  "192.168.1.10",
		BackendPort:  8096,
		RelayChain:   []int{3, 4},
	}
	if err := ensure.Validate(); err != nil {
		t.Fatalf("channel.reverse ensure: %v", err)
	}
	encoded, err := json.Marshal(ensure)
	if err != nil {
		t.Fatal(err)
	}
	var decoded ChannelReverseRequest
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if err := decoded.Validate(); err != nil {
		t.Fatalf("channel.reverse ensure round-trip: %v", err)
	}
	if len(decoded.RelayChain) != 2 || decoded.BackendPort != 8096 {
		t.Fatal("channel.reverse ensure round-trip lost fields")
	}
	for _, action := range []string{ChannelReverseActionStatus, ChannelReverseActionTeardown} {
		if err := (ChannelReverseRequest{Action: action, SessionRef: "session-1"}).Validate(); err != nil {
			t.Fatalf("channel.reverse %s: %v", action, err)
		}
		if err := (ChannelReverseRequest{Action: action}).Validate(); err == nil {
			t.Fatalf("channel.reverse %s without session_ref was accepted", action)
		}
	}
	if err := (ChannelReverseRequest{Action: ChannelReverseActionEnsure, EntryAgentID: "edge-a", ExitAgentID: "edge-b", Protocol: L4RuleProtocolTCP, BackendPort: 8096}).Validate(); err == nil {
		t.Fatal("channel.reverse ensure accepted a missing backend host")
	}
	if err := (ChannelReverseRequest{Action: "hold", SessionRef: "session-1"}).Validate(); err == nil {
		t.Fatal("channel.reverse accepted an unsupported action")
	}
	for _, state := range []string{ChannelReverseStateOnline, ChannelReverseStateOffline} {
		if err := (ChannelReverseResponse{SessionRef: "session-1", State: state}).Validate(); err != nil {
			t.Fatalf("channel.reverse status %s: %v", state, err)
		}
	}
	if err := (ChannelReverseResponse{SessionRef: "session-1", State: "degraded"}).Validate(); err == nil {
		t.Fatal("channel.reverse accepted an unsupported state")
	}
	if err := (ChannelReverseResponse{SessionRef: "session-1", State: ChannelReverseStateOnline, BridgeHost: "127.0.0.1", BridgePort: 19096}).Validate(); err != nil {
		t.Fatalf("channel.reverse bridge endpoint: %v", err)
	}
	if err := (ChannelReverseResponse{SessionRef: "session-1", State: ChannelReverseStateOffline, BridgePort: 19096}).Validate(); err == nil {
		t.Fatal("channel.reverse accepted a bridge port without a bridge host")
	}
	if err := (HostRuntimeCall{Operation: HostRuntimeChannelReverse, Payload: encoded}).Validate(); err != nil {
		t.Fatalf("channel.reverse host operation: %v", err)
	}
}
