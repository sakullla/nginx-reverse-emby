package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

type fakeL4Store struct {
	agents             []storage.AgentRow
	httpRulesByID      map[string][]storage.HTTPRuleRow
	l4RulesByID        map[string][]storage.L4RuleRow
	relayByAgent       map[string][]storage.RelayListenerRow
	wireGuardByAgent   map[string][]storage.WireGuardProfileRow
	savedAgent         storage.AgentRow
	loadSnapshotCalls  int
	listL4RulesErr     error
	saveL4RulesErr     error
	listWireGuardErr   error
	listWireGuardCalls int
	listWireGuardHook  func()
	getL4RuleCalls     int
	trafficDeletes     []trafficScopeDeleteCall
	trafficDeleteErr   error
	trafficDeleteHook  func()
}

func (f *fakeL4Store) ListAgents(context.Context) ([]storage.AgentRow, error) {
	return append([]storage.AgentRow(nil), f.agents...), nil
}

func (f *fakeL4Store) ListHTTPRules(_ context.Context, agentID string) ([]storage.HTTPRuleRow, error) {
	return append([]storage.HTTPRuleRow(nil), f.httpRulesByID[agentID]...), nil
}

func (f *fakeL4Store) GetHTTPRule(context.Context, string, int) (storage.HTTPRuleRow, bool, error) {
	return storage.HTTPRuleRow{}, false, nil
}

func (f *fakeL4Store) ListL4Rules(_ context.Context, agentID string) ([]storage.L4RuleRow, error) {
	if f.listL4RulesErr != nil {
		return nil, f.listL4RulesErr
	}
	return append([]storage.L4RuleRow(nil), f.l4RulesByID[agentID]...), nil
}

func (f *fakeL4Store) GetL4Rule(_ context.Context, agentID string, id int) (storage.L4RuleRow, bool, error) {
	f.getL4RuleCalls++
	for _, row := range f.l4RulesByID[agentID] {
		if row.ID == id {
			return row, true, nil
		}
	}
	return storage.L4RuleRow{}, false, nil
}

func (f *fakeL4Store) ListRelayListeners(_ context.Context, agentID string) ([]storage.RelayListenerRow, error) {
	if agentID == "" {
		rows := make([]storage.RelayListenerRow, 0)
		for _, listeners := range f.relayByAgent {
			rows = append(rows, listeners...)
		}
		return rows, nil
	}
	return append([]storage.RelayListenerRow(nil), f.relayByAgent[agentID]...), nil
}

func (f *fakeL4Store) ListWireGuardProfiles(_ context.Context, agentID string) ([]storage.WireGuardProfileRow, error) {
	f.listWireGuardCalls++
	if f.listWireGuardHook != nil {
		f.listWireGuardHook()
	}
	if f.listWireGuardErr != nil {
		return nil, f.listWireGuardErr
	}
	return append([]storage.WireGuardProfileRow(nil), f.wireGuardByAgent[agentID]...), nil
}

func (f *fakeL4Store) LoadLocalAgentState(context.Context) (storage.LocalAgentStateRow, error) {
	return storage.LocalAgentStateRow{}, nil
}

func (f *fakeL4Store) LoadAgentSnapshot(context.Context, string, storage.AgentSnapshotInput) (storage.Snapshot, error) {
	f.loadSnapshotCalls++
	return storage.Snapshot{}, nil
}

func (f *fakeL4Store) ListVersionPolicies(context.Context) ([]storage.VersionPolicyRow, error) {
	return nil, nil
}

func (f *fakeL4Store) ListManagedCertificates(context.Context) ([]storage.ManagedCertificateRow, error) {
	return nil, nil
}

func (f *fakeL4Store) SaveAgent(_ context.Context, row storage.AgentRow) error {
	f.savedAgent = row
	for i := range f.agents {
		if f.agents[i].ID == row.ID {
			f.agents[i] = row
			return nil
		}
	}
	f.agents = append(f.agents, row)
	return nil
}

func (f *fakeL4Store) SaveL4Rules(_ context.Context, agentID string, rows []storage.L4RuleRow) error {
	if f.saveL4RulesErr != nil {
		return f.saveL4RulesErr
	}
	f.l4RulesByID[agentID] = append([]storage.L4RuleRow(nil), rows...)
	return nil
}

func (f *fakeL4Store) SaveRelayListeners(context.Context, string, []storage.RelayListenerRow) error {
	return nil
}

func (f *fakeL4Store) SaveWireGuardProfiles(_ context.Context, agentID string, rows []storage.WireGuardProfileRow) error {
	if f.wireGuardByAgent == nil {
		f.wireGuardByAgent = map[string][]storage.WireGuardProfileRow{}
	}
	f.wireGuardByAgent[agentID] = append([]storage.WireGuardProfileRow(nil), rows...)
	return nil
}

func (f *fakeL4Store) MutateWireGuardClientProfile(_ context.Context, agentID string, profileID int, mutate func(storage.WireGuardClientProfileMutation) (storage.WireGuardClientProfileMutation, error)) error {
	profiles := append([]storage.WireGuardProfileRow(nil), f.wireGuardByAgent[agentID]...)
	index := -1
	for i, row := range profiles {
		if row.ID == profileID {
			index = i
			break
		}
	}
	if index < 0 {
		return fmt.Errorf("profile not found")
	}
	next, err := mutate(storage.WireGuardClientProfileMutation{
		Profiles:     profiles,
		ProfileIndex: index,
	})
	if err != nil {
		return err
	}
	f.wireGuardByAgent[agentID] = append([]storage.WireGuardProfileRow(nil), next.Profiles...)
	return nil
}

func (f *fakeL4Store) SaveVersionPolicies(context.Context, []storage.VersionPolicyRow) error {
	return nil
}

func (f *fakeL4Store) SaveManagedCertificates(context.Context, []storage.ManagedCertificateRow) error {
	return nil
}

func (f *fakeL4Store) LoadManagedCertificateMaterial(context.Context, string) (storage.ManagedCertificateBundle, bool, error) {
	return storage.ManagedCertificateBundle{}, false, nil
}

func (f *fakeL4Store) SaveManagedCertificateMaterial(context.Context, string, storage.ManagedCertificateBundle) error {
	return nil
}

func (f *fakeL4Store) CleanupManagedCertificateMaterial(context.Context, []storage.ManagedCertificateRow, []storage.ManagedCertificateRow) error {
	return nil
}

func (f *fakeL4Store) DeleteTrafficByScope(_ context.Context, agentID, scopeType, scopeID string) (int64, error) {
	f.trafficDeletes = append(f.trafficDeletes, trafficScopeDeleteCall{
		agentID:   agentID,
		scopeType: scopeType,
		scopeID:   scopeID,
	})
	if f.trafficDeleteHook != nil {
		f.trafficDeleteHook()
	}
	if f.trafficDeleteErr != nil {
		return 0, f.trafficDeleteErr
	}
	return 0, nil
}

func TestL4RuleServiceCreateAllowsRelayLayersForUDP(t *testing.T) {
	store := &fakeL4Store{
		l4RulesByID: map[string][]storage.L4RuleRow{},
		relayByAgent: map[string][]storage.RelayListenerRow{
			"local": {
				{ID: 7, AgentID: "local", Enabled: true},
				{ID: 8, AgentID: "local", Enabled: true},
				{ID: 9, AgentID: "local", Enabled: true},
			},
		},
	}
	svc := NewL4RuleService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	rule, err := svc.Create(context.Background(), "local", L4RuleInput{
		Protocol:    stringPtrL4("udp"),
		ListenPort:  intPtrL4(9000),
		Backends:    &[]L4Backend{{Host: "upstream", Port: 9001}},
		RelayLayers: &[][]int{{7}, {8, 9}},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if len(rule.RelayChain) != 0 {
		t.Fatalf("RelayChain = %+v", rule.RelayChain)
	}
	if len(rule.RelayLayers) != 2 || len(rule.RelayLayers[1]) != 2 || rule.RelayLayers[1][1] != 9 {
		t.Fatalf("RelayLayers = %+v", rule.RelayLayers)
	}
	if got := store.l4RulesByID["local"][0].RelayLayersJSON; got != `[[7],[8,9]]` {
		t.Fatalf("persisted relay_layers = %s", got)
	}
	if got := store.l4RulesByID["local"][0].RelayChainJSON; got != `[]` {
		t.Fatalf("persisted relay_chain = %s", got)
	}
	if row := store.l4RulesByID["local"][0]; row.UpstreamHost != "" || row.UpstreamPort != 0 {
		t.Fatalf("persisted upstream fields = %q:%d", row.UpstreamHost, row.UpstreamPort)
	}
}

func TestL4RuleServiceCreateRejectsUpstreamOnlyForTCPMode(t *testing.T) {
	store := &fakeL4Store{l4RulesByID: map[string][]storage.L4RuleRow{}}
	svc := NewL4RuleService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	_, err := svc.Create(context.Background(), "local", L4RuleInput{
		Protocol:     stringPtrL4("tcp"),
		ListenPort:   intPtrL4(9000),
		UpstreamHost: stringPtrL4("upstream"),
		UpstreamPort: intPtrL4(9001),
	})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Create() error = %v, want ErrInvalidArgument", err)
	}
}

func TestL4RuleServiceUpdateRejectsUpstreamOnlyForTCPMode(t *testing.T) {
	store := &fakeL4Store{
		l4RulesByID: map[string][]storage.L4RuleRow{
			"local": {{
				ID:           1,
				AgentID:      "local",
				Name:         "tcp",
				Protocol:     "tcp",
				ListenHost:   "0.0.0.0",
				ListenPort:   9000,
				BackendsJSON: `[{"host":"upstream","port":9001}]`,
				Enabled:      true,
				Revision:     3,
			}},
		},
	}
	svc := NewL4RuleService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	_, err := svc.Update(context.Background(), "local", 1, L4RuleInput{
		UpstreamHost: stringPtrL4("other-upstream"),
		UpstreamPort: intPtrL4(9002),
	})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Update() error = %v, want ErrInvalidArgument", err)
	}
}

func TestL4RuleFromRowDoesNotSynthesizeLegacyBackendFields(t *testing.T) {
	rule := l4RuleFromRow(storage.L4RuleRow{
		ID:             1,
		AgentID:        "local",
		Protocol:       "tcp",
		ListenHost:     "0.0.0.0",
		ListenPort:     9000,
		UpstreamHost:   "legacy",
		UpstreamPort:   9001,
		RelayChainJSON: `[7]`,
		Enabled:        true,
	})

	if rule.UpstreamHost != "" || rule.UpstreamPort != 0 || len(rule.Backends) != 0 {
		t.Fatalf("legacy upstream fields were synthesized: upstream=%q:%d backends=%+v", rule.UpstreamHost, rule.UpstreamPort, rule.Backends)
	}
	if len(rule.RelayChain) != 0 {
		t.Fatalf("legacy relay_chain was synthesized: %+v", rule.RelayChain)
	}
}

func TestL4RuleJSONOmitsLegacyFields(t *testing.T) {
	raw, err := json.Marshal(L4Rule{
		ID:           1,
		AgentID:      "local",
		Name:         "tcp",
		Protocol:     "tcp",
		ListenHost:   "0.0.0.0",
		ListenPort:   25565,
		UpstreamHost: "legacy",
		UpstreamPort: 25566,
		Backends:     []L4Backend{{Host: "upstream", Port: 25567}},
		RelayChain:   []int{7},
		RelayLayers:  [][]int{{7}},
		Enabled:      true,
	})
	if err != nil {
		t.Fatalf("json.Marshal(L4Rule) error = %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("json.Unmarshal(L4Rule) error = %v", err)
	}
	for _, key := range []string{"upstream_host", "upstream_port", "relay_chain"} {
		if _, ok := payload[key]; ok {
			t.Fatalf("L4Rule JSON exposed legacy field %q: %s", key, raw)
		}
	}
	if _, ok := payload["backends"]; !ok {
		t.Fatalf("L4Rule JSON missing canonical backends: %s", raw)
	}
	if _, ok := payload["relay_layers"]; !ok {
		t.Fatalf("L4Rule JSON missing canonical relay_layers: %s", raw)
	}
}

func TestL4RuleServiceCreateRejectsRelayChainOnly(t *testing.T) {
	store := &fakeL4Store{
		l4RulesByID: map[string][]storage.L4RuleRow{},
		relayByAgent: map[string][]storage.RelayListenerRow{
			"local": {{ID: 7, AgentID: "local", Enabled: true}},
		},
	}
	svc := NewL4RuleService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	_, err := svc.Create(context.Background(), "local", L4RuleInput{
		Protocol:   stringPtrL4("tcp"),
		ListenPort: intPtrL4(9000),
		Backends:   &[]L4Backend{{Host: "upstream", Port: 9001}},
		RelayChain: &[]int{7},
	})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Create() error = %v, want ErrInvalidArgument", err)
	}
}

func TestL4RuleServiceCreatePreservesRelayObfsForRelayLayersOnly(t *testing.T) {
	store := &fakeL4Store{
		l4RulesByID: map[string][]storage.L4RuleRow{},
		relayByAgent: map[string][]storage.RelayListenerRow{
			"local": {{
				ID:      7,
				AgentID: "local",
				Enabled: true,
			}},
		},
	}
	svc := NewL4RuleService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	rule, err := svc.Create(context.Background(), "local", L4RuleInput{
		Protocol:    stringPtrL4("tcp"),
		ListenPort:  intPtrL4(9000),
		Backends:    &[]L4Backend{{Host: "upstream", Port: 9001}},
		RelayLayers: &[][]int{{7}},
		RelayObfs:   boolPtrL4(true),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if !rule.RelayObfs {
		t.Fatalf("expected relay_obfs to be preserved for relay_layers-only rule")
	}
}

func TestL4RuleServiceCreateNormalizesLoadBalancingStrategies(t *testing.T) {
	tests := []struct {
		name     string
		input    *L4LoadBalancing
		expected string
	}{
		{name: "defaults empty input to adaptive", input: nil, expected: "adaptive"},
		{name: "normalizes explicit adaptive", input: &L4LoadBalancing{Strategy: "ADAPTIVE"}, expected: "adaptive"},
		{name: "preserves explicit round robin", input: &L4LoadBalancing{Strategy: "round_robin"}, expected: "round_robin"},
		{name: "preserves explicit random", input: &L4LoadBalancing{Strategy: "RANDOM"}, expected: "random"},
		{name: "normalizes invalid strategy to adaptive", input: &L4LoadBalancing{Strategy: "invalid"}, expected: "adaptive"},
		{name: "normalizes blank strategy to adaptive", input: &L4LoadBalancing{Strategy: "   "}, expected: "adaptive"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &fakeL4Store{l4RulesByID: map[string][]storage.L4RuleRow{}}
			svc := NewL4RuleService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, store)

			rule, err := svc.Create(context.Background(), "local", L4RuleInput{
				Protocol:      stringPtrL4("tcp"),
				ListenPort:    intPtrL4(9000),
				Backends:      &[]L4Backend{{Host: "upstream", Port: 9001}},
				LoadBalancing: tt.input,
			})
			if err != nil {
				t.Fatalf("Create() error = %v", err)
			}
			if rule.LoadBalancing.Strategy != tt.expected {
				t.Fatalf("Create() load_balancing = %+v", rule.LoadBalancing)
			}
			if got := store.l4RulesByID["local"][0].LoadBalancingJSON; got != `{"strategy":"`+tt.expected+`"}` {
				t.Fatalf("persisted load_balancing_json = %q", got)
			}
		})
	}
}

func TestL4RuleFromRowDefaultsLoadBalancingToAdaptive(t *testing.T) {
	rule := l4RuleFromRow(storage.L4RuleRow{
		ID:           1,
		AgentID:      "local",
		Protocol:     "tcp",
		ListenHost:   "0.0.0.0",
		ListenPort:   9000,
		UpstreamHost: "upstream",
		UpstreamPort: 9001,
	})

	if rule.LoadBalancing.Strategy != "adaptive" {
		t.Fatalf("l4RuleFromRow() load_balancing = %+v", rule.LoadBalancing)
	}
}

func TestL4RuleFromRowClearsProxyEntryFieldsForTCPMode(t *testing.T) {
	rule := l4RuleFromRow(storage.L4RuleRow{
		ID:                 1,
		AgentID:            "local",
		Protocol:           "tcp",
		ListenHost:         "0.0.0.0",
		ListenPort:         9000,
		UpstreamHost:       "upstream",
		UpstreamPort:       9001,
		ListenMode:         " TCP ",
		ProxyEntryAuthJSON: `{"enabled":true,"username":"u","password":"p"}`,
		ProxyEgressMode:    "relay",
		ProxyEgressURL:     "socks://user:pass@127.0.0.1:1080",
	})

	if rule.ListenMode != "tcp" {
		t.Fatalf("ListenMode = %q", rule.ListenMode)
	}
	if rule.ProxyEntryAuth != (L4ProxyEntryAuth{}) || rule.ProxyEgressMode != "" || rule.ProxyEgressURL != "" {
		t.Fatalf("proxy entry fields = auth=%+v mode=%q url=%q", rule.ProxyEntryAuth, rule.ProxyEgressMode, rule.ProxyEgressURL)
	}
}

func TestNormalizeL4RuleInputAcceptsProxyEntryRelayEgress(t *testing.T) {
	protocol := "tcp"
	listenMode := "proxy"
	egressMode := "relay"
	relayLayers := [][]int{{101}}
	input := L4RuleInput{
		Protocol:        &protocol,
		ListenHost:      stringPtrL4("127.0.0.1"),
		ListenPort:      intPtrL4(1080),
		ListenMode:      &listenMode,
		ProxyEntryAuth:  &L4ProxyEntryAuth{Enabled: true, Username: "u", Password: "p"},
		ProxyEgressMode: &egressMode,
		RelayLayers:     &relayLayers,
	}
	rule, err := normalizeL4RuleInput(input, L4Rule{}, 1)
	if err != nil {
		t.Fatalf("normalizeL4RuleInput() error = %v", err)
	}
	if rule.ListenMode != "proxy" || rule.ProxyEgressMode != "relay" {
		t.Fatalf("proxy entry fields = %+v", rule)
	}
	if !rule.ProxyEntryAuth.Enabled || rule.ProxyEntryAuth.Username != "u" || rule.ProxyEntryAuth.Password != "p" {
		t.Fatalf("ProxyEntryAuth = %+v", rule.ProxyEntryAuth)
	}
}

func TestNormalizeL4RuleInputRejectsProxyEntryUDP(t *testing.T) {
	protocol := "udp"
	listenMode := "proxy"
	egressMode := "relay"
	relayLayers := [][]int{{101}}
	input := L4RuleInput{
		Protocol:        &protocol,
		ListenHost:      stringPtrL4("127.0.0.1"),
		ListenPort:      intPtrL4(1080),
		ListenMode:      &listenMode,
		ProxyEgressMode: &egressMode,
		RelayLayers:     &relayLayers,
	}
	_, err := normalizeL4RuleInput(input, L4Rule{}, 1)
	if err == nil || !strings.Contains(err.Error(), "listen_mode=proxy requires protocol tcp") {
		t.Fatalf("error = %v, want proxy udp validation", err)
	}
}

func TestNormalizeL4RuleInputRejectsProxyEntryWithoutEgress(t *testing.T) {
	protocol := "tcp"
	listenMode := "proxy"
	input := L4RuleInput{
		Protocol:   &protocol,
		ListenHost: stringPtrL4("127.0.0.1"),
		ListenPort: intPtrL4(1080),
		ListenMode: &listenMode,
	}
	_, err := normalizeL4RuleInput(input, L4Rule{}, 1)
	if err == nil || !strings.Contains(err.Error(), "proxy_egress_mode") {
		t.Fatalf("error = %v, want proxy_egress_mode validation", err)
	}
}

func TestNormalizeL4RuleInputRejectsProxyEntryMissingProxyEgressURL(t *testing.T) {
	protocol := "tcp"
	listenMode := "proxy"
	egressMode := "proxy"
	input := L4RuleInput{
		Protocol:        &protocol,
		ListenHost:      stringPtrL4("127.0.0.1"),
		ListenPort:      intPtrL4(1080),
		ListenMode:      &listenMode,
		ProxyEgressMode: &egressMode,
	}
	_, err := normalizeL4RuleInput(input, L4Rule{}, 1)
	if err == nil || !strings.Contains(err.Error(), "proxy_egress_url") {
		t.Fatalf("error = %v, want proxy_egress_url validation", err)
	}
}

func TestNormalizeL4RuleInputRejectsInvalidProxyEgressURL(t *testing.T) {
	protocol := "tcp"
	listenMode := "proxy"
	egressMode := "proxy"
	tests := []string{
		"127.0.0.1:1080",
		"ftp://127.0.0.1:1080",
		"socks://127.0.0.1",
		"http://127.0.0.1:70000",
	}
	for _, egressURL := range tests {
		t.Run(egressURL, func(t *testing.T) {
			input := L4RuleInput{
				Protocol:        &protocol,
				ListenHost:      stringPtrL4("127.0.0.1"),
				ListenPort:      intPtrL4(1080),
				ListenMode:      &listenMode,
				ProxyEgressMode: &egressMode,
				ProxyEgressURL:  &egressURL,
			}
			_, err := normalizeL4RuleInput(input, L4Rule{}, 1)
			if err == nil || !strings.Contains(err.Error(), "invalid proxy_egress_url") {
				t.Fatalf("error = %v, want invalid proxy_egress_url validation", err)
			}
		})
	}
}

func TestNormalizeL4RuleInputRejectsProxyEntryInvalidEgressMode(t *testing.T) {
	protocol := "tcp"
	listenMode := "proxy"
	egressMode := "direct"
	input := L4RuleInput{
		Protocol:        &protocol,
		ListenHost:      stringPtrL4("127.0.0.1"),
		ListenPort:      intPtrL4(1080),
		ListenMode:      &listenMode,
		ProxyEgressMode: &egressMode,
	}
	_, err := normalizeL4RuleInput(input, L4Rule{}, 1)
	if err == nil || !strings.Contains(err.Error(), "proxy_egress_mode") {
		t.Fatalf("error = %v, want proxy_egress_mode validation", err)
	}
}

func TestL4RuleServiceWireGuardDefaultsToTransparentForTCPAndUDPRejectsUDP(t *testing.T) {
	tests := []struct {
		protocol string
		wantErr  string
	}{
		{protocol: "tcp"},
		{protocol: "udp", wantErr: "transparent inbound does not support udp"},
	}
	for _, tt := range tests {
		t.Run(tt.protocol, func(t *testing.T) {
			store := &fakeL4Store{
				l4RulesByID: map[string][]storage.L4RuleRow{},
				wireGuardByAgent: map[string][]storage.WireGuardProfileRow{
					"local": {{
						ID:            7,
						AgentID:       "local",
						PrivateKey:    testWireGuardPrivateKey,
						AddressesJSON: `["10.8.0.1/24"]`,
						Enabled:       true,
						TagsJSON:      `["system:default-wireguard"]`,
					}},
				},
			}
			svc := NewL4RuleService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, store)

			rule, err := svc.Create(context.Background(), "local", L4RuleInput{
				Protocol:   stringPtrL4(tt.protocol),
				ListenMode: stringPtrL4("wireguard"),
				ListenHost: stringPtrL4("0.0.0.0"),
				ListenPort: intPtrL4(443),
				Backends:   &[]L4Backend{{Host: "backend", Port: 8443}},
			})
			if tt.wantErr != "" {
				if !errors.Is(err, ErrInvalidArgument) {
					t.Fatalf("Create() error = %v, want ErrInvalidArgument", err)
				}
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("Create() error = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Create() error = %v", err)
			}
			if rule.WireGuardProfileID == nil || *rule.WireGuardProfileID != 7 {
				t.Fatalf("WireGuardProfileID = %v, want auto default profile 7", rule.WireGuardProfileID)
			}
			if rule.WireGuardInboundMode != "transparent" {
				t.Fatalf("WireGuardInboundMode = %q, want transparent", rule.WireGuardInboundMode)
			}
			if rule.WireGuardListenHost != "" {
				t.Fatalf("WireGuardListenHost = %q, want empty for transparent", rule.WireGuardListenHost)
			}
		})
	}
}

func TestL4WireGuardProxyEgressRequiresProfile(t *testing.T) {
	store := &fakeL4Store{l4RulesByID: map[string][]storage.L4RuleRow{}}
	svc := NewL4RuleService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, store)

	_, err := svc.Create(context.Background(), "local", L4RuleInput{
		Protocol:        stringPtrL4("tcp"),
		ListenPort:      intPtrL4(1080),
		ListenMode:      stringPtrL4("proxy"),
		ProxyEgressMode: stringPtrL4("wireguard"),
	})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Create() error = %v, want ErrInvalidArgument", err)
	}
	if err == nil || !strings.Contains(err.Error(), "wireguard_profile_id is required") {
		t.Fatalf("Create() error = %v, want clear wireguard_profile_id validation", err)
	}
}

func TestL4RuleServiceCreateMaterializesWireGuardURIEgressProfile(t *testing.T) {
	store := &fakeL4Store{
		l4RulesByID:      map[string][]storage.L4RuleRow{},
		wireGuardByAgent: map[string][]storage.WireGuardProfileRow{},
	}
	svc := NewL4RuleService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, store)
	uri := "wireguard://" + testWireGuardPrivateKey + "@edge.example.com:51820?publickey=" + testWireGuardPublicKey + "&psk=" + testWireGuardPresharedKey + "&address=10.44.0.2/32&allowedips=10.0.0.0/8&dns=1.1.1.1&mtu=1420#Edge%20WG"

	rule, err := svc.Create(context.Background(), "local", L4RuleInput{
		Protocol:           stringPtrL4("tcp"),
		ListenPort:         intPtrL4(1080),
		ListenMode:         stringPtrL4("proxy"),
		ProxyEgressMode:    stringPtrL4("wireguard"),
		WireGuardEgressURI: stringPtrL4(uri),
		WireGuardProfileID: nil,
		ProxyEntryAuth:     &L4ProxyEntryAuth{Enabled: true, Username: "u", Password: "p"},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if rule.WireGuardProfileID == nil {
		t.Fatalf("WireGuardProfileID = nil")
	}
	if got := store.l4RulesByID["local"][0].WireGuardProfileID; got == nil || *got != *rule.WireGuardProfileID {
		t.Fatalf("persisted WireGuardProfileID = %v, want %d", got, *rule.WireGuardProfileID)
	}
	rows := store.wireGuardByAgent["local"]
	if len(rows) != 1 {
		t.Fatalf("wireguard profile rows = %+v, want one", rows)
	}
	profile := wireGuardProfileFromRow(rows[0])
	if profile.ID != *rule.WireGuardProfileID || profile.Name != "Edge WG" || profile.Mode != "generic_wireguard" || !profile.Enabled || profile.ListenPort != 0 {
		t.Fatalf("materialized profile basics = %+v", profile)
	}
	if profile.PrivateKey != testWireGuardPrivateKey || len(profile.Peers) != 1 {
		t.Fatalf("materialized profile secrets/peers = %+v", profile)
	}
	peer := profile.Peers[0]
	if peer.Endpoint != "edge.example.com:51820" || peer.PublicKey != testWireGuardPublicKey || peer.PresharedKey != testWireGuardPresharedKey {
		t.Fatalf("materialized peer = %+v", peer)
	}
	if got := strings.Join(profile.Addresses, ","); got != "10.44.0.2/32" {
		t.Fatalf("profile addresses = %q", got)
	}
	if got := strings.Join(peer.AllowedIPs, ","); got != "10.0.0.0/8" {
		t.Fatalf("peer allowed_ips = %q", got)
	}
	if got := strings.Join(profile.DNS, ","); got != "1.1.1.1" {
		t.Fatalf("profile dns = %q", got)
	}
	if profile.MTU != 1420 {
		t.Fatalf("profile MTU = %d", profile.MTU)
	}
	if rule.WireGuardEgressURI != uri {
		t.Fatalf("WireGuardEgressURI = %q, want original URI", rule.WireGuardEgressURI)
	}
	if got := store.l4RulesByID["local"][0].WireGuardEgressURI; got != uri {
		t.Fatalf("persisted WireGuardEgressURI = %q, want original URI", got)
	}
}

func TestL4RuleServiceUpdateMaterializesWireGuardURIEgressProfile(t *testing.T) {
	store := &fakeL4Store{
		l4RulesByID: map[string][]storage.L4RuleRow{
			"local": {{
				ID:           11,
				AgentID:      "local",
				Name:         "entry",
				Protocol:     "tcp",
				ListenHost:   "0.0.0.0",
				ListenPort:   1080,
				BackendsJSON: `[{"host":"upstream","port":9001}]`,
				Enabled:      true,
				Revision:     3,
			}},
		},
		wireGuardByAgent: map[string][]storage.WireGuardProfileRow{},
	}
	svc := NewL4RuleService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, store)
	uri := "wireguard://" + testWireGuardPrivateKey + "@edge.example.com:51820?publickey=" + testWireGuardPublicKey + "&address=10.44.0.2/32"

	rule, err := svc.Update(context.Background(), "local", 11, L4RuleInput{
		ListenMode:         stringPtrL4("proxy"),
		ProxyEgressMode:    stringPtrL4("wireguard"),
		WireGuardEgressURI: stringPtrL4(uri),
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if rule.WireGuardProfileID == nil {
		t.Fatalf("WireGuardProfileID = nil")
	}
	rows := store.wireGuardByAgent["local"]
	if len(rows) != 1 {
		t.Fatalf("wireguard profile rows = %+v, want one", rows)
	}
	profile := wireGuardProfileFromRow(rows[0])
	if profile.Name != "l4-rule-11-wireguard-egress" {
		t.Fatalf("profile name = %q", profile.Name)
	}
	if len(profile.Peers) != 1 || strings.Join(profile.Peers[0].AllowedIPs, ",") != "0.0.0.0/0,::/0" {
		t.Fatalf("materialized peer = %+v", profile.Peers)
	}
	if got := store.l4RulesByID["local"][0].WireGuardProfileID; got == nil || *got != *rule.WireGuardProfileID {
		t.Fatalf("persisted WireGuardProfileID = %v, want %d", got, *rule.WireGuardProfileID)
	}
}

func TestL4RuleServiceUpdateSwitchesWireGuardProfileEgressToURIEgress(t *testing.T) {
	existingProfileID := 7
	store := &fakeL4Store{
		l4RulesByID: map[string][]storage.L4RuleRow{
			"local": {{
				ID:                 11,
				AgentID:            "local",
				Name:               "entry",
				Protocol:           "tcp",
				ListenHost:         "0.0.0.0",
				ListenPort:         1080,
				BackendsJSON:       `[]`,
				ListenMode:         "proxy",
				ProxyEgressMode:    "wireguard",
				WireGuardProfileID: &existingProfileID,
				Enabled:            true,
				Revision:           3,
			}},
		},
		wireGuardByAgent: map[string][]storage.WireGuardProfileRow{
			"local": {{ID: existingProfileID, AgentID: "local", Enabled: true}},
		},
	}
	svc := NewL4RuleService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, store)
	uri := "wireguard://" + testWireGuardPrivateKey + "@edge.example.com:51820?publickey=" + testWireGuardPublicKey + "&address=10.44.0.2/32#URI%20egress"

	rule, err := svc.Update(context.Background(), "local", 11, L4RuleInput{
		ProxyEgressMode:    stringPtrL4("wireguard"),
		WireGuardEgressURI: stringPtrL4(uri),
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if rule.WireGuardProfileID == nil || *rule.WireGuardProfileID == existingProfileID {
		t.Fatalf("WireGuardProfileID = %v, want generated profile different from %d", rule.WireGuardProfileID, existingProfileID)
	}
	if rule.WireGuardEgressURI != uri {
		t.Fatalf("WireGuardEgressURI = %q, want original URI", rule.WireGuardEgressURI)
	}
	if got := store.l4RulesByID["local"][0].WireGuardEgressURI; got != uri {
		t.Fatalf("persisted WireGuardEgressURI = %q, want original URI", got)
	}
	if rows := store.wireGuardByAgent["local"]; len(rows) != 2 {
		t.Fatalf("wireguard profiles = %+v, want existing plus generated", rows)
	}
}

func TestL4RuleServiceUpdateSwitchesWireGuardURIEgressToProfileEgress(t *testing.T) {
	uriProfileID := 8
	selectedProfileID := 7
	uri := "wireguard://" + testWireGuardPrivateKey + "@edge.example.com:51820?publickey=" + testWireGuardPublicKey + "&address=10.44.0.2/32#URI%20egress"
	store := &fakeL4Store{
		l4RulesByID: map[string][]storage.L4RuleRow{
			"local": {{
				ID:                 11,
				AgentID:            "local",
				Name:               "entry",
				Protocol:           "tcp",
				ListenHost:         "0.0.0.0",
				ListenPort:         1080,
				BackendsJSON:       `[]`,
				ListenMode:         "proxy",
				ProxyEgressMode:    "wireguard",
				WireGuardProfileID: &uriProfileID,
				WireGuardEgressURI: uri,
				Enabled:            true,
				Revision:           3,
			}},
		},
		wireGuardByAgent: map[string][]storage.WireGuardProfileRow{
			"local": {
				{ID: selectedProfileID, AgentID: "local", Enabled: true},
				materializedWireGuardURIProfileRow(t, "local", uriProfileID, uri),
			},
		},
	}
	svc := NewL4RuleService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, store)

	rule, err := svc.Update(context.Background(), "local", 11, L4RuleInput{
		ProxyEgressMode:    stringPtrL4("wireguard"),
		WireGuardProfileID: intPtrL4(selectedProfileID),
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if rule.WireGuardProfileID == nil || *rule.WireGuardProfileID != selectedProfileID {
		t.Fatalf("WireGuardProfileID = %v, want %d", rule.WireGuardProfileID, selectedProfileID)
	}
	if rule.WireGuardEgressURI != "" {
		t.Fatalf("WireGuardEgressURI = %q, want cleared", rule.WireGuardEgressURI)
	}
	if got := store.l4RulesByID["local"][0].WireGuardEgressURI; got != "" {
		t.Fatalf("persisted WireGuardEgressURI = %q, want cleared", got)
	}
	assertWireGuardProfileIDs(t, store.wireGuardByAgent["local"], selectedProfileID)
}

func TestL4RuleServiceUpdateWireGuardURIEgressPayloadProfileIDClearsOriginalURI(t *testing.T) {
	uriProfileID := 8
	selectedProfileID := 7
	uri := "wireguard://" + testWireGuardPrivateKey + "@edge.example.com:51820?publickey=" + testWireGuardPublicKey + "&address=10.44.0.2/32#URI%20egress"
	store := &fakeL4Store{
		l4RulesByID: map[string][]storage.L4RuleRow{
			"local": {{
				ID:                 11,
				AgentID:            "local",
				Name:               "entry",
				Protocol:           "tcp",
				ListenHost:         "0.0.0.0",
				ListenPort:         1080,
				BackendsJSON:       `[]`,
				ListenMode:         "proxy",
				ProxyEgressMode:    "wireguard",
				WireGuardProfileID: &uriProfileID,
				WireGuardEgressURI: uri,
				Enabled:            true,
				Revision:           3,
			}},
		},
		wireGuardByAgent: map[string][]storage.WireGuardProfileRow{
			"local": {
				{ID: selectedProfileID, AgentID: "local", Enabled: true},
				materializedWireGuardURIProfileRow(t, "local", uriProfileID, uri),
			},
		},
	}
	svc := NewL4RuleService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, store)

	rule, err := svc.Update(context.Background(), "local", 11, L4RuleInput{
		ProxyEgressMode:    stringPtrL4("wireguard"),
		WireGuardEgressURI: stringPtrL4(uri),
		WireGuardProfileID: intPtrL4(selectedProfileID),
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if rule.WireGuardProfileID == nil || *rule.WireGuardProfileID != selectedProfileID {
		t.Fatalf("WireGuardProfileID = %v, want %d", rule.WireGuardProfileID, selectedProfileID)
	}
	if rule.WireGuardEgressURI != "" {
		t.Fatalf("WireGuardEgressURI = %q, want cleared", rule.WireGuardEgressURI)
	}
	if got := store.l4RulesByID["local"][0].WireGuardEgressURI; got != "" {
		t.Fatalf("persisted WireGuardEgressURI = %q, want cleared", got)
	}
	assertWireGuardProfileIDs(t, store.wireGuardByAgent["local"], selectedProfileID)

	if _, err := svc.Delete(context.Background(), "local", 11); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	assertWireGuardProfileIDs(t, store.wireGuardByAgent["local"], selectedProfileID)
}

func TestL4RuleServiceUpdateWireGuardURIEgressToNewURIRemovesOldMaterializedProfile(t *testing.T) {
	uriProfileID := 8
	oldURI := "wireguard://" + testWireGuardPrivateKey + "@old.example.com:51820?publickey=" + testWireGuardPublicKey + "&address=10.44.0.2/32#Old%20URI"
	newURI := "wireguard://" + testWireGuardPrivateKey + "@new.example.com:51820?publickey=" + testWireGuardPublicKey + "&address=10.44.0.3/32#New%20URI"
	store := &fakeL4Store{
		l4RulesByID: map[string][]storage.L4RuleRow{
			"local": {{
				ID:                 11,
				AgentID:            "local",
				Name:               "entry",
				Protocol:           "tcp",
				ListenHost:         "0.0.0.0",
				ListenPort:         1080,
				BackendsJSON:       `[]`,
				ListenMode:         "proxy",
				ProxyEgressMode:    "wireguard",
				WireGuardProfileID: &uriProfileID,
				WireGuardEgressURI: oldURI,
				Enabled:            true,
				Revision:           3,
			}},
		},
		wireGuardByAgent: map[string][]storage.WireGuardProfileRow{
			"local": {materializedWireGuardURIProfileRow(t, "local", uriProfileID, oldURI)},
		},
	}
	svc := NewL4RuleService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, store)

	rule, err := svc.Update(context.Background(), "local", 11, L4RuleInput{
		WireGuardEgressURI: stringPtrL4(newURI),
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if rule.WireGuardProfileID == nil || *rule.WireGuardProfileID == uriProfileID {
		t.Fatalf("WireGuardProfileID = %v, want new materialized profile", rule.WireGuardProfileID)
	}
	if rule.WireGuardEgressURI != newURI {
		t.Fatalf("WireGuardEgressURI = %q, want new URI", rule.WireGuardEgressURI)
	}
	assertWireGuardProfileIDs(t, store.wireGuardByAgent["local"], *rule.WireGuardProfileID)
}

func TestL4RuleServiceUpdateWireGuardURIEgressKeepsProfileReferencedByOtherL4Rule(t *testing.T) {
	uriProfileID := 8
	selectedProfileID := 7
	uri := "wireguard://" + testWireGuardPrivateKey + "@edge.example.com:51820?publickey=" + testWireGuardPublicKey + "&address=10.44.0.2/32#URI%20egress"
	store := &fakeL4Store{
		l4RulesByID: map[string][]storage.L4RuleRow{
			"local": {
				{
					ID:                 11,
					AgentID:            "local",
					Name:               "entry",
					Protocol:           "tcp",
					ListenHost:         "0.0.0.0",
					ListenPort:         1080,
					BackendsJSON:       `[]`,
					ListenMode:         "proxy",
					ProxyEgressMode:    "wireguard",
					WireGuardProfileID: &uriProfileID,
					WireGuardEgressURI: uri,
					Enabled:            true,
					Revision:           3,
				},
				{
					ID:                 12,
					AgentID:            "local",
					Name:               "other",
					Protocol:           "tcp",
					ListenHost:         "0.0.0.0",
					ListenPort:         1081,
					BackendsJSON:       `[]`,
					ListenMode:         "proxy",
					ProxyEgressMode:    "wireguard",
					WireGuardProfileID: &uriProfileID,
					Enabled:            true,
					Revision:           3,
				},
			},
		},
		wireGuardByAgent: map[string][]storage.WireGuardProfileRow{
			"local": {
				{ID: selectedProfileID, AgentID: "local", Enabled: true},
				materializedWireGuardURIProfileRow(t, "local", uriProfileID, uri),
			},
		},
	}
	svc := NewL4RuleService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, store)

	rule, err := svc.Update(context.Background(), "local", 11, L4RuleInput{
		ProxyEgressMode:    stringPtrL4("wireguard"),
		WireGuardProfileID: intPtrL4(selectedProfileID),
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if rule.WireGuardProfileID == nil || *rule.WireGuardProfileID != selectedProfileID {
		t.Fatalf("WireGuardProfileID = %v, want %d", rule.WireGuardProfileID, selectedProfileID)
	}
	assertWireGuardProfileIDs(t, store.wireGuardByAgent["local"], selectedProfileID, uriProfileID)
}

func TestL4RuleServiceUpdateWireGuardURIEgressPartialUpdateKeepsMaterializedProfile(t *testing.T) {
	uriProfileID := 8
	uri := "wireguard://" + testWireGuardPrivateKey + "@edge.example.com:51820?publickey=" + testWireGuardPublicKey + "&address=10.44.0.2/32#URI%20egress"
	store := &fakeL4Store{
		l4RulesByID: map[string][]storage.L4RuleRow{
			"local": {{
				ID:                 11,
				AgentID:            "local",
				Name:               "entry",
				Protocol:           "tcp",
				ListenHost:         "0.0.0.0",
				ListenPort:         1080,
				BackendsJSON:       `[]`,
				ListenMode:         "proxy",
				ProxyEgressMode:    "wireguard",
				WireGuardProfileID: &uriProfileID,
				WireGuardEgressURI: uri,
				Enabled:            true,
				Revision:           3,
			}},
		},
		wireGuardByAgent: map[string][]storage.WireGuardProfileRow{
			"local": {{ID: uriProfileID, AgentID: "local", Enabled: true}},
		},
	}
	svc := NewL4RuleService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, store)

	rule, err := svc.Update(context.Background(), "local", 11, L4RuleInput{
		Name: stringPtrL4("renamed entry"),
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if rule.WireGuardProfileID == nil || *rule.WireGuardProfileID != uriProfileID {
		t.Fatalf("WireGuardProfileID = %v, want existing materialized profile", rule.WireGuardProfileID)
	}
	if rule.WireGuardEgressURI != uri {
		t.Fatalf("WireGuardEgressURI = %q, want unchanged URI", rule.WireGuardEgressURI)
	}
	assertWireGuardProfileIDs(t, store.wireGuardByAgent["local"], uriProfileID)
}

func TestL4RuleServiceUpdateWireGuardURIEgressPartialUpdateRollsBackL4RowsWhenLocalApplyFails(t *testing.T) {
	uriProfileID := 8
	uri := "wireguard://" + testWireGuardPrivateKey + "@edge.example.com:51820?publickey=" + testWireGuardPublicKey + "&address=10.44.0.2/32#URI%20egress"
	store := &fakeL4Store{
		l4RulesByID: map[string][]storage.L4RuleRow{
			"local": {{
				ID:                 11,
				AgentID:            "local",
				Name:               "entry",
				Protocol:           "tcp",
				ListenHost:         "0.0.0.0",
				ListenPort:         1080,
				BackendsJSON:       `[]`,
				ListenMode:         "proxy",
				ProxyEgressMode:    "wireguard",
				WireGuardProfileID: &uriProfileID,
				WireGuardEgressURI: uri,
				Enabled:            true,
				Revision:           3,
			}},
		},
		wireGuardByAgent: map[string][]storage.WireGuardProfileRow{
			"local": {{ID: uriProfileID, AgentID: "local", Enabled: true}},
		},
	}
	svc := NewL4RuleService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, store)
	svc.SetLocalApplyTrigger(func(context.Context) error {
		return errors.New("apply failed")
	})

	_, err := svc.Update(context.Background(), "local", 11, L4RuleInput{
		Name: stringPtrL4("renamed entry"),
	})
	if err == nil {
		t.Fatal("Update() error = nil, want apply error")
	}
	rows := store.l4RulesByID["local"]
	if len(rows) != 1 {
		t.Fatalf("l4 rules after failed apply = %+v, want original row", rows)
	}
	if rows[0].Name != "entry" || rows[0].Revision != 3 {
		t.Fatalf("l4 row after failed apply = %+v, want original name and revision", rows[0])
	}
	assertWireGuardProfileIDs(t, store.wireGuardByAgent["local"], uriProfileID)
}

func TestL4RuleServiceDeleteWireGuardURIEgressRemovesMaterializedProfile(t *testing.T) {
	uriProfileID := 8
	manualProfileID := 7
	uri := "wireguard://" + testWireGuardPrivateKey + "@edge.example.com:51820?publickey=" + testWireGuardPublicKey + "&address=10.44.0.2/32#URI%20egress"
	store := &fakeL4Store{
		l4RulesByID: map[string][]storage.L4RuleRow{
			"local": {{
				ID:                 11,
				AgentID:            "local",
				Name:               "entry",
				Protocol:           "tcp",
				ListenHost:         "0.0.0.0",
				ListenPort:         1080,
				BackendsJSON:       `[]`,
				ListenMode:         "proxy",
				ProxyEgressMode:    "wireguard",
				WireGuardProfileID: &uriProfileID,
				WireGuardEgressURI: uri,
				Enabled:            true,
				Revision:           3,
			}},
		},
		wireGuardByAgent: map[string][]storage.WireGuardProfileRow{
			"local": {
				{ID: manualProfileID, AgentID: "local", Enabled: true},
				materializedWireGuardURIProfileRow(t, "local", uriProfileID, uri),
			},
		},
	}
	svc := NewL4RuleService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, store)

	if _, err := svc.Delete(context.Background(), "local", 11); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	assertWireGuardProfileIDs(t, store.wireGuardByAgent["local"], manualProfileID)
}

func TestL4RuleServiceDeleteWireGuardURIEgressSkipsRenamedMaterializedProfile(t *testing.T) {
	uriProfileID := 8
	ruleID := 11
	uri := "wireguard://" + testWireGuardPrivateKey + "@edge.example.com:51820?publickey=" + testWireGuardPublicKey + "&address=10.44.0.2/32"
	profileRow := materializedWireGuardURIProfileRowForRule(t, "local", uriProfileID, ruleID, uri)
	profileRow.Name = "manual rename"
	store := &fakeL4Store{
		l4RulesByID: map[string][]storage.L4RuleRow{
			"local": {{
				ID:                 ruleID,
				AgentID:            "local",
				Name:               "entry",
				Protocol:           "tcp",
				ListenHost:         "0.0.0.0",
				ListenPort:         1080,
				BackendsJSON:       `[]`,
				ListenMode:         "proxy",
				ProxyEgressMode:    "wireguard",
				WireGuardProfileID: &uriProfileID,
				WireGuardEgressURI: uri,
				Enabled:            true,
				Revision:           3,
			}},
		},
		wireGuardByAgent: map[string][]storage.WireGuardProfileRow{
			"local": {profileRow},
		},
	}
	svc := NewL4RuleService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, store)

	if _, err := svc.Delete(context.Background(), "local", ruleID); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	assertWireGuardProfileIDs(t, store.wireGuardByAgent["local"], uriProfileID)
}

func TestL4RuleServiceDeleteWireGuardURIEgressKeepsProfileReferencedByHTTPRule(t *testing.T) {
	uriProfileID := 8
	uri := "wireguard://" + testWireGuardPrivateKey + "@edge.example.com:51820?publickey=" + testWireGuardPublicKey + "&address=10.44.0.2/32#URI%20egress"
	store := &fakeL4Store{
		httpRulesByID: map[string][]storage.HTTPRuleRow{
			"local": {{
				ID:                    21,
				AgentID:               "local",
				WireGuardEntryEnabled: true,
				WireGuardProfileID:    &uriProfileID,
				Enabled:               true,
			}},
		},
		l4RulesByID: map[string][]storage.L4RuleRow{
			"local": {{
				ID:                 11,
				AgentID:            "local",
				Name:               "entry",
				Protocol:           "tcp",
				ListenHost:         "0.0.0.0",
				ListenPort:         1080,
				BackendsJSON:       `[]`,
				ListenMode:         "proxy",
				ProxyEgressMode:    "wireguard",
				WireGuardProfileID: &uriProfileID,
				WireGuardEgressURI: uri,
				Enabled:            true,
				Revision:           3,
			}},
		},
		wireGuardByAgent: map[string][]storage.WireGuardProfileRow{
			"local": {materializedWireGuardURIProfileRow(t, "local", uriProfileID, uri)},
		},
	}
	svc := NewL4RuleService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, store)

	if _, err := svc.Delete(context.Background(), "local", 11); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	assertWireGuardProfileIDs(t, store.wireGuardByAgent["local"], uriProfileID)
}

func TestL4RuleServiceDeleteWireGuardURIEgressKeepsProfileReferencedByRelayListener(t *testing.T) {
	uriProfileID := 8
	uri := "wireguard://" + testWireGuardPrivateKey + "@edge.example.com:51820?publickey=" + testWireGuardPublicKey + "&address=10.44.0.2/32#URI%20egress"
	store := &fakeL4Store{
		l4RulesByID: map[string][]storage.L4RuleRow{
			"local": {{
				ID:                 11,
				AgentID:            "local",
				Name:               "entry",
				Protocol:           "tcp",
				ListenHost:         "0.0.0.0",
				ListenPort:         1080,
				BackendsJSON:       `[]`,
				ListenMode:         "proxy",
				ProxyEgressMode:    "wireguard",
				WireGuardProfileID: &uriProfileID,
				WireGuardEgressURI: uri,
				Enabled:            true,
				Revision:           3,
			}},
		},
		relayByAgent: map[string][]storage.RelayListenerRow{
			"local": {{
				ID:                 31,
				AgentID:            "local",
				TransportMode:      "wireguard",
				WireGuardProfileID: &uriProfileID,
				Enabled:            true,
			}},
		},
		wireGuardByAgent: map[string][]storage.WireGuardProfileRow{
			"local": {materializedWireGuardURIProfileRow(t, "local", uriProfileID, uri)},
		},
	}
	svc := NewL4RuleService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, store)

	if _, err := svc.Delete(context.Background(), "local", 11); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	assertWireGuardProfileIDs(t, store.wireGuardByAgent["local"], uriProfileID)
}

func TestL4RuleServiceDeleteWireGuardURIEgressSkipsManualProfileThatDoesNotMatchURI(t *testing.T) {
	manualProfileID := 7
	uri := "wireguard://" + testWireGuardPrivateKey + "@edge.example.com:51820?publickey=" + testWireGuardPublicKey + "&address=10.44.0.2/32#URI%20egress"
	store := &fakeL4Store{
		l4RulesByID: map[string][]storage.L4RuleRow{
			"local": {{
				ID:                 11,
				AgentID:            "local",
				Name:               "entry",
				Protocol:           "tcp",
				ListenHost:         "0.0.0.0",
				ListenPort:         1080,
				BackendsJSON:       `[]`,
				ListenMode:         "proxy",
				ProxyEgressMode:    "wireguard",
				WireGuardProfileID: &manualProfileID,
				WireGuardEgressURI: uri,
				Enabled:            true,
				Revision:           3,
			}},
		},
		wireGuardByAgent: map[string][]storage.WireGuardProfileRow{
			"local": {{ID: manualProfileID, AgentID: "local", Enabled: true}},
		},
	}
	svc := NewL4RuleService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, store)

	if _, err := svc.Delete(context.Background(), "local", 11); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	assertWireGuardProfileIDs(t, store.wireGuardByAgent["local"], manualProfileID)
}

func TestL4RuleServiceDeleteWireGuardURIEgressSkipsManualProfileWithURICoreFieldsAndManualDefaults(t *testing.T) {
	manualProfileID := 7
	uri := "wireguard://" + testWireGuardPrivateKey + "@edge.example.com:51820?publickey=" + testWireGuardPublicKey + "&psk=" + testWireGuardPresharedKey + "&address=10.44.0.2/32&allowedips=10.0.0.0/8&dns=1.1.1.1&mtu=1420#URI%20egress"
	tests := []struct {
		name   string
		mutate func(*storage.WireGuardProfileRow)
	}{
		{
			name: "listen_port",
			mutate: func(row *storage.WireGuardProfileRow) {
				row.ListenPort = 51820
			},
		},
		{
			name: "public_endpoint",
			mutate: func(row *storage.WireGuardProfileRow) {
				row.PublicEndpoint = "public.example.com:51820"
			},
		},
		{
			name: "tags",
			mutate: func(row *storage.WireGuardProfileRow) {
				profile := wireGuardProfileFromRow(*row)
				profile.Tags = []string{"manual"}
				*row = wireGuardProfileToRow(profile)
			},
		},
		{
			name: "peer_keepalive",
			mutate: func(row *storage.WireGuardProfileRow) {
				profile := wireGuardProfileFromRow(*row)
				profile.Peers[0].PersistentKeepaliveSeconds = 25
				*row = wireGuardProfileToRow(profile)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			profileRow := materializedWireGuardURIProfileRow(t, "local", manualProfileID, uri)
			tt.mutate(&profileRow)
			store := &fakeL4Store{
				l4RulesByID: map[string][]storage.L4RuleRow{
					"local": {{
						ID:                 11,
						AgentID:            "local",
						Name:               "entry",
						Protocol:           "tcp",
						ListenHost:         "0.0.0.0",
						ListenPort:         1080,
						BackendsJSON:       `[]`,
						ListenMode:         "proxy",
						ProxyEgressMode:    "wireguard",
						WireGuardProfileID: &manualProfileID,
						WireGuardEgressURI: uri,
						Enabled:            true,
						Revision:           3,
					}},
				},
				wireGuardByAgent: map[string][]storage.WireGuardProfileRow{
					"local": {profileRow},
				},
			}
			svc := NewL4RuleService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, store)

			if _, err := svc.Delete(context.Background(), "local", 11); err != nil {
				t.Fatalf("Delete() error = %v", err)
			}
			assertWireGuardProfileIDs(t, store.wireGuardByAgent["local"], manualProfileID)
		})
	}
}

func TestL4RuleServiceDeleteWireGuardURIEgressRestoresL4RowsWhenMissingProfileAndLocalApplyFails(t *testing.T) {
	uriProfileID := 8
	uri := "wireguard://" + testWireGuardPrivateKey + "@edge.example.com:51820?publickey=" + testWireGuardPublicKey + "&address=10.44.0.2/32#URI%20egress"
	store := &fakeL4Store{
		l4RulesByID: map[string][]storage.L4RuleRow{
			"local": {{
				ID:                 11,
				AgentID:            "local",
				Name:               "entry",
				Protocol:           "tcp",
				ListenHost:         "0.0.0.0",
				ListenPort:         1080,
				BackendsJSON:       `[]`,
				ListenMode:         "proxy",
				ProxyEgressMode:    "wireguard",
				WireGuardProfileID: &uriProfileID,
				WireGuardEgressURI: uri,
				Enabled:            true,
				Revision:           3,
			}},
		},
		wireGuardByAgent: map[string][]storage.WireGuardProfileRow{
			"local": {},
		},
	}
	svc := NewL4RuleService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, store)
	svc.SetLocalApplyTrigger(func(context.Context) error {
		return errors.New("apply failed")
	})

	_, err := svc.Delete(context.Background(), "local", 11)
	if err == nil {
		t.Fatal("Delete() error = nil, want apply error")
	}
	rows := store.l4RulesByID["local"]
	if len(rows) != 1 {
		t.Fatalf("l4 rules after failed apply = %+v, want restored row", rows)
	}
	if rows[0].ID != 11 || rows[0].Name != "entry" || rows[0].WireGuardEgressURI != uri {
		t.Fatalf("l4 row after failed apply = %+v, want deleted row restored", rows[0])
	}
	if rows := store.wireGuardByAgent["local"]; len(rows) != 0 {
		t.Fatalf("wireguard profiles after failed apply = %+v, want none", rows)
	}
}

func TestL4RuleServiceCreateRejectsWireGuardInboundWithURIEgress(t *testing.T) {
	store := &fakeL4Store{
		l4RulesByID:      map[string][]storage.L4RuleRow{},
		wireGuardByAgent: map[string][]storage.WireGuardProfileRow{},
	}
	svc := NewL4RuleService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, store)
	uri := "wireguard://" + testWireGuardPrivateKey + "@edge.example.com:51820?publickey=" + testWireGuardPublicKey + "&address=10.44.0.2/32"

	_, err := svc.Create(context.Background(), "local", L4RuleInput{
		Protocol:             stringPtrL4("tcp"),
		ListenPort:           intPtrL4(1080),
		ListenMode:           stringPtrL4("wireguard"),
		ProxyEgressMode:      stringPtrL4("wireguard"),
		WireGuardListenHost:  stringPtrL4("10.8.0.1"),
		WireGuardEgressURI:   stringPtrL4(uri),
		WireGuardInboundMode: stringPtrL4("address"),
	})
	if err == nil {
		t.Fatal("Create() error = nil, want unsupported combination error")
	}
	if !strings.Contains(err.Error(), "wireguard URI egress cannot be combined with wireguard listen mode") {
		t.Fatalf("Create() error = %v", err)
	}
	if rows := store.l4RulesByID["local"]; len(rows) != 0 {
		t.Fatalf("l4 rules after rejected create = %+v, want none", rows)
	}
}

func TestL4RuleServiceCreateRollsBackWireGuardURIEgressProfileWhenRuleSaveFails(t *testing.T) {
	store := &fakeL4Store{
		l4RulesByID:      map[string][]storage.L4RuleRow{},
		wireGuardByAgent: map[string][]storage.WireGuardProfileRow{},
		saveL4RulesErr:   errors.New("save l4 failed"),
	}
	svc := NewL4RuleService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, store)
	uri := "wireguard://" + testWireGuardPrivateKey + "@edge.example.com:51820?publickey=" + testWireGuardPublicKey + "&address=10.44.0.2/32"

	_, err := svc.Create(context.Background(), "local", L4RuleInput{
		Protocol:           stringPtrL4("tcp"),
		ListenPort:         intPtrL4(1080),
		ListenMode:         stringPtrL4("proxy"),
		ProxyEgressMode:    stringPtrL4("wireguard"),
		WireGuardEgressURI: stringPtrL4(uri),
	})
	if err == nil {
		t.Fatal("Create() error = nil, want save error")
	}
	if rows := store.wireGuardByAgent["local"]; len(rows) != 0 {
		t.Fatalf("wireguard profiles after failed create = %+v, want none", rows)
	}
}

func TestL4RuleServiceCreateRollsBackWireGuardURIEgressProfileWhenLocalApplyFails(t *testing.T) {
	store := &fakeL4Store{
		l4RulesByID:      map[string][]storage.L4RuleRow{},
		wireGuardByAgent: map[string][]storage.WireGuardProfileRow{},
	}
	svc := NewL4RuleService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, store)
	svc.SetLocalApplyTrigger(func(context.Context) error {
		return errors.New("apply failed")
	})
	uri := "wireguard://" + testWireGuardPrivateKey + "@edge.example.com:51820?publickey=" + testWireGuardPublicKey + "&address=10.44.0.2/32"

	_, err := svc.Create(context.Background(), "local", L4RuleInput{
		Protocol:           stringPtrL4("tcp"),
		ListenPort:         intPtrL4(1080),
		ListenMode:         stringPtrL4("proxy"),
		ProxyEgressMode:    stringPtrL4("wireguard"),
		WireGuardEgressURI: stringPtrL4(uri),
	})
	if err == nil {
		t.Fatal("Create() error = nil, want apply error")
	}
	if rows := store.l4RulesByID["local"]; len(rows) != 0 {
		t.Fatalf("l4 rules after failed apply = %+v, want none", rows)
	}
	if rows := store.wireGuardByAgent["local"]; len(rows) != 0 {
		t.Fatalf("wireguard profiles after failed apply = %+v, want none", rows)
	}
}

func TestL4RuleServiceCreateRollsBackWireGuardURIEgressProfileWhenProfileValidationFails(t *testing.T) {
	store := &fakeL4Store{
		l4RulesByID:      map[string][]storage.L4RuleRow{},
		wireGuardByAgent: map[string][]storage.WireGuardProfileRow{},
	}
	svc := NewL4RuleService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, store)
	uri := "wireguard://" + testWireGuardPrivateKey + "@edge.example.com:51820?publickey=" + testWireGuardPublicKey + "&address=10.44.0.2/32"
	store.listWireGuardHook = func() {
		if store.listWireGuardCalls == 2 {
			store.listWireGuardErr = errors.New("profile validation failed")
		}
	}

	_, err := svc.Create(context.Background(), "local", L4RuleInput{
		Protocol:           stringPtrL4("tcp"),
		ListenPort:         intPtrL4(1080),
		ListenMode:         stringPtrL4("proxy"),
		ProxyEgressMode:    stringPtrL4("wireguard"),
		WireGuardEgressURI: stringPtrL4(uri),
	})
	if err == nil {
		t.Fatal("Create() error = nil, want profile validation error")
	}
	store.listWireGuardErr = nil
	if rows := store.wireGuardByAgent["local"]; len(rows) != 0 {
		t.Fatalf("wireguard profiles after failed validation = %+v, want none", rows)
	}
	if rows := store.l4RulesByID["local"]; len(rows) != 0 {
		t.Fatalf("l4 rules after failed validation = %+v, want none", rows)
	}
}

func TestL4RuleServiceCreateRejectsWireGuardURIReservedUntilProfileModelSupportsIt(t *testing.T) {
	store := &fakeL4Store{
		l4RulesByID:      map[string][]storage.L4RuleRow{},
		wireGuardByAgent: map[string][]storage.WireGuardProfileRow{},
	}
	svc := NewL4RuleService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, store)
	uri := "wireguard://" + testWireGuardPrivateKey + "@edge.example.com:51820?publickey=" + testWireGuardPublicKey + "&address=10.44.0.2/32&reserved=1,2,3"

	_, err := svc.Create(context.Background(), "local", L4RuleInput{
		Protocol:           stringPtrL4("tcp"),
		ListenPort:         intPtrL4(1080),
		ListenMode:         stringPtrL4("proxy"),
		ProxyEgressMode:    stringPtrL4("wireguard"),
		WireGuardEgressURI: stringPtrL4(uri),
	})
	if err == nil {
		t.Fatal("Create() error = nil, want unsupported reserved error")
	}
	if !strings.Contains(err.Error(), "reserved is not supported") {
		t.Fatalf("Create() error = %v", err)
	}
	if rows := store.wireGuardByAgent["local"]; len(rows) != 0 {
		t.Fatalf("wireguard profiles after rejected create = %+v, want none", rows)
	}
}

func TestL4WireGuardListenModeAllowsProxyEntryWithoutBackend(t *testing.T) {
	profileID := 7
	store := &fakeL4Store{
		l4RulesByID: map[string][]storage.L4RuleRow{},
		wireGuardByAgent: map[string][]storage.WireGuardProfileRow{
			"local": {{ID: profileID, AgentID: "local", Enabled: true}},
		},
	}
	svc := NewL4RuleService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, store)

	rule, err := svc.Create(context.Background(), "local", L4RuleInput{
		Protocol:           stringPtrL4("tcp"),
		ListenHost:         stringPtrL4("0.0.0.0"),
		ListenPort:         intPtrL4(1081),
		ListenMode:         stringPtrL4("wireguard"),
		WireGuardProfileID: intPtrL4(profileID),
		ProxyEgressMode:    stringPtrL4("wireguard"),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if rule.ListenMode != "wireguard" || rule.ProxyEgressMode != "wireguard" {
		t.Fatalf("created rule modes = listen %q egress %q", rule.ListenMode, rule.ProxyEgressMode)
	}
	if len(rule.Backends) != 0 || rule.ProxyEgressURL != "" {
		t.Fatalf("proxy-entry targets = backends=%+v url=%q", rule.Backends, rule.ProxyEgressURL)
	}
	if got := store.l4RulesByID["local"][0]; got.ProxyEgressMode != "wireguard" || got.BackendsJSON != "[]" {
		t.Fatalf("persisted row = %+v", got)
	}
}

func TestL4WireGuardListenHostDefaultsToListenHostOnCreate(t *testing.T) {
	store := &fakeL4Store{
		l4RulesByID: map[string][]storage.L4RuleRow{},
		wireGuardByAgent: map[string][]storage.WireGuardProfileRow{
			"local": {{ID: 7, AgentID: "local", Enabled: true}},
		},
	}
	svc := NewL4RuleService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, store)

	rule, err := svc.Create(context.Background(), "local", L4RuleInput{
		Protocol:             stringPtrL4("udp"),
		ListenHost:           stringPtrL4("127.0.0.1"),
		ListenPort:           intPtrL4(51820),
		ListenMode:           stringPtrL4("wireguard"),
		WireGuardProfileID:   intPtrL4(7),
		WireGuardInboundMode: stringPtrL4("address"),
		Backends:             &[]L4Backend{{Host: "upstream", Port: 9001}},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if rule.WireGuardInboundMode != "address" {
		t.Fatalf("WireGuardInboundMode = %q, want address", rule.WireGuardInboundMode)
	}
	if rule.WireGuardListenHost != "127.0.0.1" {
		t.Fatalf("WireGuardListenHost = %q, want listen host default", rule.WireGuardListenHost)
	}
	if got := store.l4RulesByID["local"][0].WireGuardListenHost; got != "127.0.0.1" {
		t.Fatalf("persisted WireGuardListenHost = %q, want listen host default", got)
	}
}

func TestL4WireGuardListenHostPreservesExplicitValueOnCreate(t *testing.T) {
	store := &fakeL4Store{
		l4RulesByID: map[string][]storage.L4RuleRow{},
		wireGuardByAgent: map[string][]storage.WireGuardProfileRow{
			"local": {{ID: 7, AgentID: "local", Enabled: true}},
		},
	}
	svc := NewL4RuleService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, store)

	rule, err := svc.Create(context.Background(), "local", L4RuleInput{
		Protocol:             stringPtrL4("udp"),
		ListenHost:           stringPtrL4("0.0.0.0"),
		ListenPort:           intPtrL4(51820),
		ListenMode:           stringPtrL4("wireguard"),
		WireGuardProfileID:   intPtrL4(7),
		WireGuardInboundMode: stringPtrL4("address"),
		WireGuardListenHost:  stringPtrL4("10.8.0.1"),
		Backends:             &[]L4Backend{{Host: "upstream", Port: 9001}},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if rule.WireGuardListenHost != "10.8.0.1" {
		t.Fatalf("WireGuardListenHost = %q, want explicit value", rule.WireGuardListenHost)
	}
	if got := store.l4RulesByID["local"][0].WireGuardListenHost; got != "10.8.0.1" {
		t.Fatalf("persisted WireGuardListenHost = %q, want explicit value", got)
	}
}

func TestL4RuleServiceWireGuardDefaultsInboundModeTransparent(t *testing.T) {
	store := &fakeL4Store{
		l4RulesByID: map[string][]storage.L4RuleRow{},
		wireGuardByAgent: map[string][]storage.WireGuardProfileRow{
			"local": {{ID: 7, AgentID: "local", Enabled: true}},
		},
	}
	svc := NewL4RuleService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, store)

	rule, err := svc.Create(context.Background(), "local", L4RuleInput{
		Protocol:           stringPtrL4("tcp"),
		ListenHost:         stringPtrL4("0.0.0.0"),
		ListenPort:         intPtrL4(51820),
		ListenMode:         stringPtrL4("wireguard"),
		WireGuardProfileID: intPtrL4(7),
		Backends:           &[]L4Backend{{Host: "upstream", Port: 9001}},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if rule.WireGuardInboundMode != "transparent" {
		t.Fatalf("WireGuardInboundMode = %q, want transparent", rule.WireGuardInboundMode)
	}
	if got := store.l4RulesByID["local"][0].WireGuardInboundMode; got != "transparent" {
		t.Fatalf("persisted WireGuardInboundMode = %q, want transparent", got)
	}
}

func TestL4RuleServiceGetLegacyWireGuardInboundDefaultsToTransparent(t *testing.T) {
	profileID := 7
	store := &fakeL4Store{
		l4RulesByID: map[string][]storage.L4RuleRow{
			"local": {{
				ID:                 1,
				AgentID:            "local",
				Name:               "legacy-wg",
				Protocol:           "udp",
				ListenHost:         "0.0.0.0",
				ListenPort:         51820,
				ListenMode:         "wireguard",
				WireGuardProfileID: &profileID,
				BackendsJSON:       `[]`,
				Enabled:            true,
			}},
		},
	}
	svc := NewL4RuleService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, store)

	rule, err := svc.Get(context.Background(), "local", 1)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if rule.WireGuardInboundMode != "transparent" {
		t.Fatalf("WireGuardInboundMode = %q, want transparent for legacy empty mode", rule.WireGuardInboundMode)
	}
}

func TestL4RuleServiceWireGuardTransparentTCPAllowsEmptyBackends(t *testing.T) {
	store := &fakeL4Store{
		l4RulesByID: map[string][]storage.L4RuleRow{},
		wireGuardByAgent: map[string][]storage.WireGuardProfileRow{
			"local": {{ID: 7, AgentID: "local", Enabled: true}},
		},
	}
	svc := NewL4RuleService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, store)

	rule, err := svc.Create(context.Background(), "local", L4RuleInput{
		Protocol:             stringPtrL4("tcp"),
		ListenHost:           stringPtrL4("0.0.0.0"),
		ListenPort:           intPtrL4(443),
		ListenMode:           stringPtrL4("wireguard"),
		WireGuardProfileID:   intPtrL4(7),
		WireGuardInboundMode: stringPtrL4("transparent"),
		WireGuardListenHost:  stringPtrL4("10.8.0.1"),
		Backends:             &[]L4Backend{},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if rule.WireGuardInboundMode != "transparent" {
		t.Fatalf("WireGuardInboundMode = %q, want transparent", rule.WireGuardInboundMode)
	}
	if rule.WireGuardListenHost != "" {
		t.Fatalf("WireGuardListenHost = %q, want cleared for transparent mode", rule.WireGuardListenHost)
	}
	if len(rule.Backends) != 0 {
		t.Fatalf("Backends = %#v, want empty", rule.Backends)
	}
	row := store.l4RulesByID["local"][0]
	if row.WireGuardInboundMode != "transparent" {
		t.Fatalf("persisted WireGuardInboundMode = %q, want transparent", row.WireGuardInboundMode)
	}
	if row.WireGuardListenHost != "" {
		t.Fatalf("persisted WireGuardListenHost = %q, want cleared for transparent mode", row.WireGuardListenHost)
	}
	if row.BackendsJSON != "[]" {
		t.Fatalf("persisted BackendsJSON = %s, want []", row.BackendsJSON)
	}
}

func TestL4RuleServiceWireGuardTransparentTCPClearsSubmittedBackends(t *testing.T) {
	store := &fakeL4Store{
		l4RulesByID: map[string][]storage.L4RuleRow{},
		wireGuardByAgent: map[string][]storage.WireGuardProfileRow{
			"local": {{ID: 7, AgentID: "local", Enabled: true}},
		},
	}
	svc := NewL4RuleService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, store)

	rule, err := svc.Create(context.Background(), "local", L4RuleInput{
		Protocol:             stringPtrL4("tcp"),
		ListenHost:           stringPtrL4("0.0.0.0"),
		ListenPort:           intPtrL4(443),
		ListenMode:           stringPtrL4("wireguard"),
		WireGuardProfileID:   intPtrL4(7),
		WireGuardInboundMode: stringPtrL4("transparent"),
		Backends:             &[]L4Backend{{Host: "stale.example", Port: 9001}},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if len(rule.Backends) != 0 {
		t.Fatalf("Backends = %#v, want empty for transparent forwarding", rule.Backends)
	}
	if row := store.l4RulesByID["local"][0]; row.BackendsJSON != "[]" {
		t.Fatalf("persisted BackendsJSON = %s, want []", row.BackendsJSON)
	}
}

func TestL4RuleServiceWireGuardTransparentUDPRejected(t *testing.T) {
	store := &fakeL4Store{
		l4RulesByID: map[string][]storage.L4RuleRow{},
		wireGuardByAgent: map[string][]storage.WireGuardProfileRow{
			"local": {{ID: 7, AgentID: "local", Enabled: true}},
		},
	}
	svc := NewL4RuleService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, store)

	_, err := svc.Create(context.Background(), "local", L4RuleInput{
		Protocol:             stringPtrL4("udp"),
		ListenHost:           stringPtrL4("0.0.0.0"),
		ListenPort:           intPtrL4(51820),
		ListenMode:           stringPtrL4("wireguard"),
		WireGuardProfileID:   intPtrL4(7),
		WireGuardInboundMode: stringPtrL4("transparent"),
		Backends:             &[]L4Backend{{Host: "upstream", Port: 9001}},
	})
	if err == nil || !strings.Contains(err.Error(), "transparent") || !strings.Contains(err.Error(), "udp") {
		t.Fatalf("Create() error = %v, want transparent udp validation", err)
	}
}

func TestL4RuleServiceUpdateWireGuardTransparentTCPInboundModeAccepted(t *testing.T) {
	profileID := 7
	existing := L4Rule{
		ID:                 1,
		AgentID:            "local",
		Protocol:           "tcp",
		ListenHost:         "0.0.0.0",
		ListenPort:         51820,
		Backends:           []L4Backend{{Host: "upstream", Port: 9001}},
		ListenMode:         "wireguard",
		WireGuardProfileID: &profileID,
		Enabled:            true,
	}
	store := &fakeL4Store{
		l4RulesByID: map[string][]storage.L4RuleRow{
			"local": {l4RuleToRow(existing)},
		},
		wireGuardByAgent: map[string][]storage.WireGuardProfileRow{
			"local": {{ID: profileID, AgentID: "local", Enabled: true}},
		},
	}
	svc := NewL4RuleService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, store)

	rule, err := svc.Update(context.Background(), "local", existing.ID, L4RuleInput{
		Protocol:             stringPtrL4("tcp"),
		ListenHost:           stringPtrL4("0.0.0.0"),
		ListenPort:           intPtrL4(51820),
		ListenMode:           stringPtrL4("wireguard"),
		WireGuardProfileID:   intPtrL4(profileID),
		WireGuardInboundMode: stringPtrL4("transparent"),
		WireGuardListenHost:  stringPtrL4("10.8.0.1"),
		Backends:             &[]L4Backend{{Host: "upstream", Port: 9001}},
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if rule.WireGuardInboundMode != "transparent" {
		t.Fatalf("WireGuardInboundMode = %q, want transparent", rule.WireGuardInboundMode)
	}
	if rule.WireGuardListenHost != "" {
		t.Fatalf("WireGuardListenHost = %q, want cleared for transparent mode", rule.WireGuardListenHost)
	}
	row := store.l4RulesByID["local"][0]
	if row.WireGuardInboundMode != "transparent" {
		t.Fatalf("persisted WireGuardInboundMode = %q, want transparent", row.WireGuardInboundMode)
	}
	if row.WireGuardListenHost != "" {
		t.Fatalf("persisted WireGuardListenHost = %q, want cleared for transparent mode", row.WireGuardListenHost)
	}
}

func TestL4WireGuardTransparentListenConflictsIgnoreListenHostOnCreate(t *testing.T) {
	profileID := 7
	existing := L4Rule{
		ID:                   1,
		AgentID:              "local",
		Name:                 "wg-a",
		Protocol:             "tcp",
		ListenHost:           "10.8.0.10",
		ListenPort:           51820,
		Backends:             []L4Backend{{Host: "upstream-a", Port: 9001}},
		ListenMode:           "wireguard",
		WireGuardProfileID:   &profileID,
		WireGuardInboundMode: "transparent",
		Enabled:              true,
	}
	store := &fakeL4Store{
		l4RulesByID: map[string][]storage.L4RuleRow{
			"local": {l4RuleToRow(existing)},
		},
		wireGuardByAgent: map[string][]storage.WireGuardProfileRow{
			"local": {{ID: profileID, AgentID: "local", Enabled: true}},
		},
	}
	svc := NewL4RuleService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, store)

	_, err := svc.Create(context.Background(), "local", L4RuleInput{
		Protocol:             stringPtrL4("tcp"),
		ListenHost:           stringPtrL4("10.8.0.11"),
		ListenPort:           intPtrL4(51820),
		ListenMode:           stringPtrL4("wireguard"),
		WireGuardProfileID:   intPtrL4(profileID),
		WireGuardInboundMode: stringPtrL4("transparent"),
		Backends:             &[]L4Backend{{Host: "upstream-b", Port: 9002}},
	})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Create() error = %v, want ErrInvalidArgument", err)
	}
	if err == nil || !strings.Contains(err.Error(), "51820 conflicts") {
		t.Fatalf("Create() error = %v, want transparent WireGuard listen conflict", err)
	}

	rule, err := svc.Create(context.Background(), "local", L4RuleInput{
		Protocol:             stringPtrL4("tcp"),
		ListenHost:           stringPtrL4("10.8.0.11"),
		ListenPort:           intPtrL4(51821),
		ListenMode:           stringPtrL4("wireguard"),
		WireGuardProfileID:   intPtrL4(profileID),
		WireGuardInboundMode: stringPtrL4("transparent"),
		Backends:             &[]L4Backend{{Host: "upstream-b", Port: 9002}},
	})
	if err != nil {
		t.Fatalf("Create() different port error = %v", err)
	}
	if rule.ListenPort != 51821 {
		t.Fatalf("created ListenPort = %d, want 51821", rule.ListenPort)
	}
}

func TestL4WireGuardTransparentListenConflictsWithAddressModeOnCreate(t *testing.T) {
	profileID := 7
	tests := []struct {
		name     string
		existing L4Rule
		input    L4RuleInput
	}{
		{
			name: "address mode conflicts with existing transparent",
			existing: L4Rule{
				ID:                   1,
				AgentID:              "local",
				Name:                 "wg-transparent",
				Protocol:             "tcp",
				ListenHost:           "10.8.0.20",
				ListenPort:           8443,
				Backends:             []L4Backend{{Host: "upstream-a", Port: 9001}},
				ListenMode:           "wireguard",
				WireGuardProfileID:   &profileID,
				WireGuardInboundMode: "transparent",
				Enabled:              true,
			},
			input: L4RuleInput{
				Protocol:             stringPtrL4("tcp"),
				ListenHost:           stringPtrL4("0.0.0.0"),
				ListenPort:           intPtrL4(8443),
				ListenMode:           stringPtrL4("wireguard"),
				WireGuardProfileID:   intPtrL4(profileID),
				WireGuardInboundMode: stringPtrL4("address"),
				WireGuardListenHost:  stringPtrL4("10.8.0.1"),
				Backends:             &[]L4Backend{{Host: "upstream-b", Port: 9002}},
			},
		},
		{
			name: "transparent conflicts with existing address mode",
			existing: L4Rule{
				ID:                   1,
				AgentID:              "local",
				Name:                 "wg-address",
				Protocol:             "tcp",
				ListenHost:           "0.0.0.0",
				ListenPort:           8443,
				Backends:             []L4Backend{{Host: "upstream-a", Port: 9001}},
				ListenMode:           "wireguard",
				WireGuardProfileID:   &profileID,
				WireGuardInboundMode: "address",
				WireGuardListenHost:  "10.8.0.1",
				Enabled:              true,
			},
			input: L4RuleInput{
				Protocol:             stringPtrL4("tcp"),
				ListenHost:           stringPtrL4("10.8.0.20"),
				ListenPort:           intPtrL4(8443),
				ListenMode:           stringPtrL4("wireguard"),
				WireGuardProfileID:   intPtrL4(profileID),
				WireGuardInboundMode: stringPtrL4("transparent"),
				Backends:             &[]L4Backend{{Host: "upstream-b", Port: 9002}},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeL4Store{
				l4RulesByID: map[string][]storage.L4RuleRow{
					"local": {l4RuleToRow(tc.existing)},
				},
				wireGuardByAgent: map[string][]storage.WireGuardProfileRow{
					"local": {{ID: profileID, AgentID: "local", Enabled: true}},
				},
			}
			svc := NewL4RuleService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, store)

			_, err := svc.Create(context.Background(), "local", tc.input)
			if !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("Create() error = %v, want ErrInvalidArgument", err)
			}
			if err == nil || !strings.Contains(err.Error(), "8443 conflicts") {
				t.Fatalf("Create() error = %v, want transparent WireGuard wildcard listen conflict", err)
			}
		})
	}
}

func TestL4WireGuardTransparentProxyEntryListenConflictsIgnoreListenHostOnCreate(t *testing.T) {
	profileID := 7
	tests := []struct {
		name     string
		existing L4Rule
	}{
		{
			name: "backend forwarding",
			existing: L4Rule{
				ID:                   1,
				AgentID:              "local",
				Name:                 "wg-backend",
				Protocol:             "tcp",
				ListenHost:           "10.8.0.10",
				ListenPort:           8443,
				Backends:             []L4Backend{{Host: "upstream-a", Port: 9001}},
				ListenMode:           "wireguard",
				WireGuardProfileID:   &profileID,
				WireGuardInboundMode: "transparent",
				Enabled:              true,
			},
		},
		{
			name: "proxy entry",
			existing: L4Rule{
				ID:                   1,
				AgentID:              "local",
				Name:                 "wg-proxy-a",
				Protocol:             "tcp",
				ListenHost:           "10.8.0.10",
				ListenPort:           8443,
				ListenMode:           "wireguard",
				WireGuardProfileID:   &profileID,
				WireGuardInboundMode: "transparent",
				ProxyEgressMode:      "proxy",
				ProxyEgressURL:       "socks5://127.0.0.1:1080",
				Enabled:              true,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeL4Store{
				l4RulesByID: map[string][]storage.L4RuleRow{
					"local": {l4RuleToRow(tc.existing)},
				},
				wireGuardByAgent: map[string][]storage.WireGuardProfileRow{
					"local": {{ID: profileID, AgentID: "local", Enabled: true}},
				},
			}
			svc := NewL4RuleService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, store)

			_, err := svc.Create(context.Background(), "local", L4RuleInput{
				Protocol:             stringPtrL4("tcp"),
				ListenHost:           stringPtrL4("10.8.0.11"),
				ListenPort:           intPtrL4(8443),
				ListenMode:           stringPtrL4("wireguard"),
				WireGuardProfileID:   intPtrL4(profileID),
				WireGuardInboundMode: stringPtrL4("transparent"),
				ProxyEgressMode:      stringPtrL4("proxy"),
				ProxyEgressURL:       stringPtrL4("socks5://127.0.0.1:1081"),
			})
			if !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("Create() error = %v, want ErrInvalidArgument", err)
			}
			if err == nil || !strings.Contains(err.Error(), "8443 conflicts") {
				t.Fatalf("Create() error = %v, want transparent WireGuard proxy-entry listen conflict", err)
			}
		})
	}
}

func TestL4WireGuardTransparentListenConflictsIgnoreListenHostOnUpdate(t *testing.T) {
	profileID := 7
	existing := L4Rule{
		ID:                   1,
		AgentID:              "local",
		Name:                 "wg-a",
		Protocol:             "tcp",
		ListenHost:           "10.8.0.10",
		ListenPort:           51820,
		Backends:             []L4Backend{{Host: "upstream-a", Port: 9001}},
		ListenMode:           "wireguard",
		WireGuardProfileID:   &profileID,
		WireGuardInboundMode: "transparent",
		Enabled:              true,
	}
	current := L4Rule{
		ID:                   2,
		AgentID:              "local",
		Name:                 "wg-b",
		Protocol:             "tcp",
		ListenHost:           "10.8.0.11",
		ListenPort:           51821,
		Backends:             []L4Backend{{Host: "upstream-b", Port: 9002}},
		ListenMode:           "wireguard",
		WireGuardProfileID:   &profileID,
		WireGuardInboundMode: "transparent",
		Enabled:              true,
	}
	store := &fakeL4Store{
		l4RulesByID: map[string][]storage.L4RuleRow{
			"local": {l4RuleToRow(existing), l4RuleToRow(current)},
		},
		wireGuardByAgent: map[string][]storage.WireGuardProfileRow{
			"local": {{ID: profileID, AgentID: "local", Enabled: true}},
		},
	}
	svc := NewL4RuleService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, store)

	_, err := svc.Update(context.Background(), "local", current.ID, L4RuleInput{
		ListenPort: intPtrL4(51820),
	})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Update() error = %v, want ErrInvalidArgument", err)
	}
	if err == nil || !strings.Contains(err.Error(), "51820 conflicts") {
		t.Fatalf("Update() error = %v, want transparent WireGuard listen conflict", err)
	}
}

func TestL4WireGuardTransparentListenConflictsWithAddressModeOnUpdate(t *testing.T) {
	profileID := 7
	tests := []struct {
		name     string
		existing L4Rule
		current  L4Rule
		input    L4RuleInput
	}{
		{
			name: "address mode conflicts with existing transparent",
			existing: L4Rule{
				ID:                   1,
				AgentID:              "local",
				Name:                 "wg-transparent",
				Protocol:             "tcp",
				ListenHost:           "10.8.0.20",
				ListenPort:           8443,
				Backends:             []L4Backend{{Host: "upstream-a", Port: 9001}},
				ListenMode:           "wireguard",
				WireGuardProfileID:   &profileID,
				WireGuardInboundMode: "transparent",
				Enabled:              true,
			},
			current: L4Rule{
				ID:                   2,
				AgentID:              "local",
				Name:                 "wg-address",
				Protocol:             "tcp",
				ListenHost:           "0.0.0.0",
				ListenPort:           8444,
				Backends:             []L4Backend{{Host: "upstream-b", Port: 9002}},
				ListenMode:           "wireguard",
				WireGuardProfileID:   &profileID,
				WireGuardInboundMode: "address",
				WireGuardListenHost:  "10.8.0.1",
				Enabled:              true,
			},
			input: L4RuleInput{
				ListenPort: intPtrL4(8443),
			},
		},
		{
			name: "transparent conflicts with existing address mode",
			existing: L4Rule{
				ID:                   1,
				AgentID:              "local",
				Name:                 "wg-address",
				Protocol:             "tcp",
				ListenHost:           "0.0.0.0",
				ListenPort:           8443,
				Backends:             []L4Backend{{Host: "upstream-a", Port: 9001}},
				ListenMode:           "wireguard",
				WireGuardProfileID:   &profileID,
				WireGuardInboundMode: "address",
				WireGuardListenHost:  "10.8.0.1",
				Enabled:              true,
			},
			current: L4Rule{
				ID:                   2,
				AgentID:              "local",
				Name:                 "wg-transparent",
				Protocol:             "tcp",
				ListenHost:           "10.8.0.20",
				ListenPort:           8444,
				Backends:             []L4Backend{{Host: "upstream-b", Port: 9002}},
				ListenMode:           "wireguard",
				WireGuardProfileID:   &profileID,
				WireGuardInboundMode: "transparent",
				Enabled:              true,
			},
			input: L4RuleInput{
				ListenPort: intPtrL4(8443),
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeL4Store{
				l4RulesByID: map[string][]storage.L4RuleRow{
					"local": {l4RuleToRow(tc.existing), l4RuleToRow(tc.current)},
				},
				wireGuardByAgent: map[string][]storage.WireGuardProfileRow{
					"local": {{ID: profileID, AgentID: "local", Enabled: true}},
				},
			}
			svc := NewL4RuleService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, store)

			_, err := svc.Update(context.Background(), "local", tc.current.ID, tc.input)
			if !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("Update() error = %v, want ErrInvalidArgument", err)
			}
			if err == nil || !strings.Contains(err.Error(), "8443 conflicts") {
				t.Fatalf("Update() error = %v, want transparent WireGuard wildcard listen conflict", err)
			}
		})
	}
}

func TestL4WireGuardTransparentProxyEntryListenConflictsIgnoreListenHostOnUpdate(t *testing.T) {
	profileID := 7
	existing := L4Rule{
		ID:                   1,
		AgentID:              "local",
		Name:                 "wg-proxy",
		Protocol:             "tcp",
		ListenHost:           "10.8.0.10",
		ListenPort:           8443,
		ListenMode:           "wireguard",
		WireGuardProfileID:   &profileID,
		WireGuardInboundMode: "transparent",
		ProxyEgressMode:      "proxy",
		ProxyEgressURL:       "socks5://127.0.0.1:1080",
		Enabled:              true,
	}
	current := L4Rule{
		ID:                   2,
		AgentID:              "local",
		Name:                 "wg-backend",
		Protocol:             "tcp",
		ListenHost:           "10.8.0.11",
		ListenPort:           8444,
		Backends:             []L4Backend{{Host: "upstream-b", Port: 9002}},
		ListenMode:           "wireguard",
		WireGuardProfileID:   &profileID,
		WireGuardInboundMode: "transparent",
		Enabled:              true,
	}
	store := &fakeL4Store{
		l4RulesByID: map[string][]storage.L4RuleRow{
			"local": {l4RuleToRow(existing), l4RuleToRow(current)},
		},
		wireGuardByAgent: map[string][]storage.WireGuardProfileRow{
			"local": {{ID: profileID, AgentID: "local", Enabled: true}},
		},
	}
	svc := NewL4RuleService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, store)

	_, err := svc.Update(context.Background(), "local", current.ID, L4RuleInput{
		ListenPort: intPtrL4(8443),
	})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Update() error = %v, want ErrInvalidArgument", err)
	}
	if err == nil || !strings.Contains(err.Error(), "8443 conflicts") {
		t.Fatalf("Update() error = %v, want transparent WireGuard proxy-entry listen conflict", err)
	}
}

func TestL4RuleServiceWireGuardInvalidInboundModeReject(t *testing.T) {
	_, err := normalizeL4RuleInput(L4RuleInput{
		Protocol:             stringPtrL4("udp"),
		ListenHost:           stringPtrL4("0.0.0.0"),
		ListenPort:           intPtrL4(51820),
		ListenMode:           stringPtrL4("wireguard"),
		WireGuardProfileID:   intPtrL4(7),
		WireGuardInboundMode: stringPtrL4("capture"),
		Backends:             &[]L4Backend{{Host: "upstream", Port: 9001}},
	}, L4Rule{}, 1)
	if err == nil || !strings.Contains(err.Error(), "wireguard_inbound_mode") {
		t.Fatalf("normalizeL4RuleInput() error = %v, want wireguard_inbound_mode validation", err)
	}
}

func TestL4WireGuardListenHostConflictsUseTunnelHost(t *testing.T) {
	profileID := 7
	existing := L4Rule{
		ID:                   1,
		AgentID:              "local",
		Name:                 "wg-a",
		Protocol:             "udp",
		ListenHost:           "0.0.0.0",
		ListenPort:           51820,
		Backends:             []L4Backend{{Host: "upstream-a", Port: 9001}},
		ListenMode:           "wireguard",
		WireGuardProfileID:   &profileID,
		WireGuardInboundMode: "address",
		WireGuardListenHost:  "10.8.0.1",
		Enabled:              true,
	}
	store := &fakeL4Store{
		l4RulesByID: map[string][]storage.L4RuleRow{
			"local": {l4RuleToRow(existing)},
		},
		wireGuardByAgent: map[string][]storage.WireGuardProfileRow{
			"local": {{ID: profileID, AgentID: "local", Enabled: true}},
		},
	}
	svc := NewL4RuleService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, store)

	_, err := svc.Create(context.Background(), "local", L4RuleInput{
		Protocol:             stringPtrL4("udp"),
		ListenHost:           stringPtrL4("127.0.0.1"),
		ListenPort:           intPtrL4(51820),
		ListenMode:           stringPtrL4("wireguard"),
		WireGuardProfileID:   intPtrL4(profileID),
		WireGuardInboundMode: stringPtrL4("address"),
		WireGuardListenHost:  stringPtrL4("10.8.0.1"),
		Backends:             &[]L4Backend{{Host: "upstream-b", Port: 9002}},
	})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Create() error = %v, want ErrInvalidArgument", err)
	}
	if err == nil || !strings.Contains(err.Error(), "10.8.0.1:51820 conflicts") {
		t.Fatalf("Create() error = %v, want tunnel listen host conflict", err)
	}
}

func TestL4WireGuardListenUniquenessAllowsSameTunnelAddressAcrossProfiles(t *testing.T) {
	existingProfileID := 7
	nextProfileID := 8
	existing := L4Rule{
		ID:                   1,
		AgentID:              "local",
		Name:                 "wg-a",
		Protocol:             "udp",
		ListenHost:           "0.0.0.0",
		ListenPort:           51820,
		Backends:             []L4Backend{{Host: "upstream-a", Port: 9001}},
		ListenMode:           "wireguard",
		WireGuardProfileID:   &existingProfileID,
		WireGuardInboundMode: "address",
		WireGuardListenHost:  "10.8.0.1",
		Enabled:              true,
	}
	store := &fakeL4Store{
		l4RulesByID: map[string][]storage.L4RuleRow{
			"local": {l4RuleToRow(existing)},
		},
		wireGuardByAgent: map[string][]storage.WireGuardProfileRow{
			"local": {
				{ID: existingProfileID, AgentID: "local", Enabled: true},
				{ID: nextProfileID, AgentID: "local", Enabled: true},
			},
		},
	}
	svc := NewL4RuleService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, store)

	rule, err := svc.Create(context.Background(), "local", L4RuleInput{
		Protocol:             stringPtrL4("udp"),
		ListenHost:           stringPtrL4("0.0.0.0"),
		ListenPort:           intPtrL4(51820),
		ListenMode:           stringPtrL4("wireguard"),
		WireGuardProfileID:   intPtrL4(nextProfileID),
		WireGuardInboundMode: stringPtrL4("address"),
		WireGuardListenHost:  stringPtrL4("10.8.0.1"),
		Backends:             &[]L4Backend{{Host: "upstream-b", Port: 9002}},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if rule.WireGuardProfileID == nil || *rule.WireGuardProfileID != nextProfileID {
		t.Fatalf("WireGuardProfileID = %v, want %d", rule.WireGuardProfileID, nextProfileID)
	}
	if got := len(store.l4RulesByID["local"]); got != 2 {
		t.Fatalf("persisted L4 rules len = %d, want 2", got)
	}
}

func TestL4ListenUniquenessAllowsHostAndWireGuardStacksToShareAddress(t *testing.T) {
	profileID := 7
	existing := L4Rule{
		ID:         1,
		AgentID:    "local",
		Name:       "public",
		Protocol:   "tcp",
		ListenHost: "0.0.0.0",
		ListenPort: 8080,
		Backends:   []L4Backend{{Host: "upstream-a", Port: 9001}},
		ListenMode: "tcp",
		Enabled:    true,
	}
	store := &fakeL4Store{
		l4RulesByID: map[string][]storage.L4RuleRow{
			"local": {l4RuleToRow(existing)},
		},
		wireGuardByAgent: map[string][]storage.WireGuardProfileRow{
			"local": {{ID: profileID, AgentID: "local", Enabled: true}},
		},
	}
	svc := NewL4RuleService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, store)

	rule, err := svc.Create(context.Background(), "local", L4RuleInput{
		Protocol:           stringPtrL4("tcp"),
		ListenHost:         stringPtrL4("0.0.0.0"),
		ListenPort:         intPtrL4(8080),
		ListenMode:         stringPtrL4("wireguard"),
		WireGuardProfileID: intPtrL4(profileID),
		Backends:           &[]L4Backend{{Host: "upstream-b", Port: 9002}},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if rule.ListenMode != "wireguard" || rule.WireGuardInboundMode != "transparent" || rule.WireGuardListenHost != "" {
		t.Fatalf("created rule = %+v", rule)
	}
	if got := len(store.l4RulesByID["local"]); got != 2 {
		t.Fatalf("persisted L4 rules len = %d, want 2", got)
	}
}

func TestL4WireGuardListenHostDefaultsToListenHostOnUpdate(t *testing.T) {
	current := L4Rule{
		ID:         1,
		AgentID:    "local",
		Name:       "TCP 9000",
		Protocol:   "tcp",
		ListenHost: "0.0.0.0",
		ListenPort: 9000,
		Backends:   []L4Backend{{Host: "upstream", Port: 9001}},
		ListenMode: "tcp",
		Enabled:    true,
	}
	store := &fakeL4Store{
		l4RulesByID: map[string][]storage.L4RuleRow{
			"local": {l4RuleToRow(current)},
		},
		wireGuardByAgent: map[string][]storage.WireGuardProfileRow{
			"local": {{ID: 7, AgentID: "local", Enabled: true}},
		},
	}
	svc := NewL4RuleService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, store)

	rule, err := svc.Update(context.Background(), "local", 1, L4RuleInput{
		Protocol:             stringPtrL4("udp"),
		ListenHost:           stringPtrL4("127.0.0.1"),
		ListenMode:           stringPtrL4("wireguard"),
		WireGuardProfileID:   intPtrL4(7),
		WireGuardInboundMode: stringPtrL4("address"),
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if rule.WireGuardInboundMode != "address" || rule.WireGuardListenHost != "127.0.0.1" {
		t.Fatalf("rule = %+v, want address mode with listen host default", rule)
	}
	if got := store.l4RulesByID["local"][0].WireGuardListenHost; got != "127.0.0.1" {
		t.Fatalf("persisted WireGuardListenHost = %q, want listen host default", got)
	}
}

func TestL4WireGuardListenHostDefaultsToProfileAddress(t *testing.T) {
	profileID := 7
	store := &fakeL4Store{
		l4RulesByID: map[string][]storage.L4RuleRow{},
		wireGuardByAgent: map[string][]storage.WireGuardProfileRow{
			"local": {{
				ID:            profileID,
				AgentID:       "local",
				AddressesJSON: `["10.8.9.1/24"]`,
				Enabled:       true,
			}},
		},
	}
	svc := NewL4RuleService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, store)

	rule, err := svc.Create(context.Background(), "local", L4RuleInput{
		Protocol:           stringPtrL4("tcp"),
		ListenHost:         stringPtrL4("0.0.0.0"),
		ListenPort:         intPtrL4(8443),
		ListenMode:         stringPtrL4("wireguard"),
		WireGuardProfileID: intPtrL4(profileID),
		Backends:           &[]L4Backend{{Host: "upstream", Port: 9001}},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if rule.WireGuardInboundMode != "transparent" || rule.WireGuardListenHost != "" {
		t.Fatalf("rule = %+v, want transparent mode with empty listen host", rule)
	}
	if got := store.l4RulesByID["local"][0].WireGuardListenHost; got != "" {
		t.Fatalf("persisted WireGuardListenHost = %q, want empty for transparent mode", got)
	}
}

func TestL4RuleServiceUpdateToWireGuardInboundClearsStaleProxyEgress(t *testing.T) {
	profileID := 7
	current := L4Rule{
		ID:              1,
		AgentID:         "local",
		Name:            "proxy entry",
		Protocol:        "tcp",
		ListenHost:      "0.0.0.0",
		ListenPort:      1080,
		ListenMode:      "proxy",
		ProxyEntryAuth:  L4ProxyEntryAuth{Enabled: true, Username: "user", Password: "secret"},
		ProxyEgressMode: "relay",
		RelayLayers:     [][]int{{101}},
		Enabled:         true,
		Revision:        3,
	}
	store := &fakeL4Store{
		l4RulesByID: map[string][]storage.L4RuleRow{
			"local": {l4RuleToRow(current)},
		},
		wireGuardByAgent: map[string][]storage.WireGuardProfileRow{
			"local": {{ID: profileID, AgentID: "local", Enabled: true}},
		},
	}
	svc := NewL4RuleService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, store)

	rule, err := svc.Update(context.Background(), "local", 1, L4RuleInput{
		ListenMode:         stringPtrL4("wireguard"),
		WireGuardProfileID: intPtrL4(profileID),
		Backends:           &[]L4Backend{{Host: "upstream", Port: 9001}},
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if rule.ProxyEgressMode != "" || rule.ProxyEgressURL != "" || rule.ProxyEntryAuth != (L4ProxyEntryAuth{}) {
		t.Fatalf("proxy entry fields = mode=%q url=%q auth=%+v, want cleared", rule.ProxyEgressMode, rule.ProxyEgressURL, rule.ProxyEntryAuth)
	}
	if rule.WireGuardInboundMode != "transparent" || rule.WireGuardListenHost != "" {
		t.Fatalf("rule = %+v, want transparent inbound with cleared listen host", rule)
	}
	if len(rule.Backends) != 0 {
		t.Fatalf("Backends = %+v, want cleared for transparent inbound", rule.Backends)
	}
	row := store.l4RulesByID["local"][0]
	if row.ProxyEgressMode != "" || row.ProxyEgressURL != "" || parseL4ProxyEntryAuth(row.ProxyEntryAuthJSON) != (L4ProxyEntryAuth{}) {
		t.Fatalf("persisted proxy entry fields = mode=%q url=%q auth=%s, want cleared", row.ProxyEgressMode, row.ProxyEgressURL, row.ProxyEntryAuthJSON)
	}
	if row.BackendsJSON != `[]` {
		t.Fatalf("persisted backends = %s, want cleared for transparent inbound", row.BackendsJSON)
	}
	if row.RelayLayersJSON != "[]" {
		t.Fatalf("persisted relay_layers = %s, want cleared", row.RelayLayersJSON)
	}
}

func TestL4WireGuardValidatesProfileReferences(t *testing.T) {
	tests := []struct {
		name     string
		input    L4RuleInput
		profiles map[string][]storage.WireGuardProfileRow
		assert   func(t *testing.T, rule L4Rule, row storage.L4RuleRow)
		wantErr  string
	}{
		{
			name: "accepts enabled profile for tcp listen",
			input: L4RuleInput{
				Protocol:           stringPtrL4("tcp"),
				ListenPort:         intPtrL4(51820),
				ListenMode:         stringPtrL4("wireguard"),
				WireGuardProfileID: intPtrL4(7),
				Backends:           &[]L4Backend{{Host: "upstream", Port: 9001}},
			},
			profiles: map[string][]storage.WireGuardProfileRow{
				"local": {{ID: 7, AgentID: "local", Enabled: true}},
			},
			assert: func(t *testing.T, rule L4Rule, row storage.L4RuleRow) {
				t.Helper()
				if rule.ListenMode != "wireguard" || rule.WireGuardProfileID == nil || *rule.WireGuardProfileID != 7 {
					t.Fatalf("rule = %+v", rule)
				}
				if row.WireGuardProfileID == nil || *row.WireGuardProfileID != 7 {
					t.Fatalf("persisted WireGuardProfileID = %v", row.WireGuardProfileID)
				}
			},
		},
		{
			name: "accepts enabled profile for udp listen",
			input: L4RuleInput{
				Protocol:             stringPtrL4("udp"),
				ListenPort:           intPtrL4(51820),
				ListenMode:           stringPtrL4("wireguard"),
				WireGuardProfileID:   intPtrL4(7),
				WireGuardInboundMode: stringPtrL4("address"),
				WireGuardListenHost:  stringPtrL4("10.8.0.1"),
				Backends:             &[]L4Backend{{Host: "upstream", Port: 9001}},
			},
			profiles: map[string][]storage.WireGuardProfileRow{
				"local": {{ID: 7, AgentID: "local", Enabled: true}},
			},
			assert: func(t *testing.T, rule L4Rule, _ storage.L4RuleRow) {
				t.Helper()
				if rule.Protocol != "udp" || rule.ListenMode != "wireguard" || rule.WireGuardInboundMode != "address" {
					t.Fatalf("rule = %+v", rule)
				}
			},
		},
		{
			name: "accepts enabled profile for proxy egress",
			input: L4RuleInput{
				Protocol:           stringPtrL4("tcp"),
				ListenPort:         intPtrL4(1080),
				ListenMode:         stringPtrL4("proxy"),
				ProxyEgressMode:    stringPtrL4("wireguard"),
				WireGuardProfileID: intPtrL4(7),
			},
			profiles: map[string][]storage.WireGuardProfileRow{
				"local": {{ID: 7, AgentID: "local", Enabled: true}},
			},
			assert: func(t *testing.T, rule L4Rule, row storage.L4RuleRow) {
				t.Helper()
				if rule.ProxyEgressMode != "wireguard" || rule.WireGuardProfileID == nil || *rule.WireGuardProfileID != 7 {
					t.Fatalf("rule = %+v", rule)
				}
				if len(rule.Backends) != 0 || rule.ProxyEgressURL != "" {
					t.Fatalf("proxy-entry wireguard targets = backends=%+v url=%q", rule.Backends, rule.ProxyEgressURL)
				}
				if row.WireGuardProfileID == nil || *row.WireGuardProfileID != 7 || row.ProxyEgressURL != "" {
					t.Fatalf("persisted row = %+v", row)
				}
			},
		},
		{
			name: "accepts enabled profile for wireguard listen with proxy egress",
			input: L4RuleInput{
				Protocol:            stringPtrL4("tcp"),
				ListenPort:          intPtrL4(8443),
				ListenMode:          stringPtrL4("wireguard"),
				WireGuardProfileID:  intPtrL4(7),
				WireGuardListenHost: stringPtrL4("10.8.0.1"),
				ProxyEgressMode:     stringPtrL4("proxy"),
				ProxyEgressURL:      stringPtrL4("socks://127.0.0.1:1080"),
			},
			profiles: map[string][]storage.WireGuardProfileRow{
				"local": {{ID: 7, AgentID: "local", AddressesJSON: `["10.8.0.1/24"]`, Enabled: true}},
			},
			assert: func(t *testing.T, rule L4Rule, row storage.L4RuleRow) {
				t.Helper()
				if rule.ListenMode != "wireguard" || rule.ProxyEgressMode != "proxy" || len(rule.Backends) != 0 {
					t.Fatalf("rule = %+v, want wireguard proxy entry", rule)
				}
				if row.ProxyEgressMode != "proxy" || row.ProxyEgressURL != "socks://127.0.0.1:1080" || row.BackendsJSON != "[]" {
					t.Fatalf("persisted row = %+v", row)
				}
			},
		},
		{
			name: "rejects disabled profile",
			input: L4RuleInput{
				Protocol:           stringPtrL4("tcp"),
				ListenPort:         intPtrL4(51820),
				ListenMode:         stringPtrL4("wireguard"),
				WireGuardProfileID: intPtrL4(7),
				Backends:           &[]L4Backend{{Host: "upstream", Port: 9001}},
			},
			profiles: map[string][]storage.WireGuardProfileRow{
				"local": {{ID: 7, AgentID: "local", Enabled: false}},
			},
			wantErr: "wireguard profile 7 is disabled",
		},
		{
			name: "rejects missing profile",
			input: L4RuleInput{
				Protocol:           stringPtrL4("tcp"),
				ListenPort:         intPtrL4(51820),
				ListenMode:         stringPtrL4("wireguard"),
				WireGuardProfileID: intPtrL4(7),
				Backends:           &[]L4Backend{{Host: "upstream", Port: 9001}},
			},
			profiles: map[string][]storage.WireGuardProfileRow{
				"other": {{ID: 7, AgentID: "other", Enabled: true}},
			},
			wantErr: "wireguard profile 7 not found for agent local",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &fakeL4Store{
				l4RulesByID:      map[string][]storage.L4RuleRow{},
				wireGuardByAgent: tt.profiles,
			}
			svc := NewL4RuleService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, store)

			rule, err := svc.Create(context.Background(), "local", tt.input)
			if tt.wantErr != "" {
				if !errors.Is(err, ErrInvalidArgument) {
					t.Fatalf("Create() error = %v, want ErrInvalidArgument", err)
				}
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("Create() error = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Create() error = %v", err)
			}
			if tt.assert != nil {
				tt.assert(t, rule, store.l4RulesByID["local"][0])
			}
		})
	}
}

func TestL4WireGuardRequiresAgentCapability(t *testing.T) {
	tests := []struct {
		name         string
		capabilities []string
		input        L4RuleInput
		wantErr      bool
	}{
		{
			name:         "rejects wireguard listen without capability",
			capabilities: []string{"l4"},
			input: L4RuleInput{
				Protocol:           stringPtrL4("tcp"),
				ListenPort:         intPtrL4(51820),
				ListenMode:         stringPtrL4("wireguard"),
				WireGuardProfileID: intPtrL4(7),
				Backends:           &[]L4Backend{{Host: "upstream", Port: 9001}},
			},
			wantErr: true,
		},
		{
			name:         "accepts wireguard listen with capability",
			capabilities: []string{"l4", "wireguard"},
			input: L4RuleInput{
				Protocol:           stringPtrL4("tcp"),
				ListenPort:         intPtrL4(51820),
				ListenMode:         stringPtrL4("wireguard"),
				WireGuardProfileID: intPtrL4(7),
				Backends:           &[]L4Backend{{Host: "upstream", Port: 9001}},
			},
		},
		{
			name:         "rejects wireguard egress without capability",
			capabilities: []string{"l4"},
			input: L4RuleInput{
				Protocol:           stringPtrL4("tcp"),
				ListenPort:         intPtrL4(1080),
				ListenMode:         stringPtrL4("proxy"),
				ProxyEgressMode:    stringPtrL4("wireguard"),
				WireGuardProfileID: intPtrL4(7),
			},
			wantErr: true,
		},
		{
			name:         "accepts wireguard egress with capability",
			capabilities: []string{"l4", "wireguard"},
			input: L4RuleInput{
				Protocol:           stringPtrL4("tcp"),
				ListenPort:         intPtrL4(1080),
				ListenMode:         stringPtrL4("proxy"),
				ProxyEgressMode:    stringPtrL4("wireguard"),
				WireGuardProfileID: intPtrL4(7),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &fakeL4Store{
				agents: []storage.AgentRow{{
					ID:               "edge-1",
					Name:             "Edge 1",
					CapabilitiesJSON: marshalStringArray(tt.capabilities),
				}},
				httpRulesByID: map[string][]storage.HTTPRuleRow{},
				l4RulesByID:   map[string][]storage.L4RuleRow{},
				relayByAgent:  map[string][]storage.RelayListenerRow{},
				wireGuardByAgent: map[string][]storage.WireGuardProfileRow{
					"edge-1": {{ID: 7, AgentID: "edge-1", Enabled: true}},
				},
			}
			svc := NewL4RuleService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, store)

			rule, err := svc.Create(context.Background(), "edge-1", tt.input)
			if tt.wantErr {
				if !errors.Is(err, ErrInvalidArgument) {
					t.Fatalf("Create() error = %v, want ErrInvalidArgument", err)
				}
				if err == nil || !strings.Contains(err.Error(), "agent does not support WireGuard") {
					t.Fatalf("Create() error = %v, want WireGuard capability message", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Create() error = %v", err)
			}
			if rule.WireGuardProfileID == nil || *rule.WireGuardProfileID != 7 {
				t.Fatalf("Create() rule = %+v", rule)
			}
		})
	}
}

func TestL4UpdateWireGuardRequiresAgentCapability(t *testing.T) {
	store := &fakeL4Store{
		agents: []storage.AgentRow{{
			ID:               "edge-1",
			Name:             "Edge 1",
			CapabilitiesJSON: marshalStringArray([]string{"l4"}),
		}},
		httpRulesByID: map[string][]storage.HTTPRuleRow{},
		l4RulesByID: map[string][]storage.L4RuleRow{
			"edge-1": {{
				ID:                1,
				AgentID:           "edge-1",
				Name:              "tcp rule",
				Protocol:          "tcp",
				ListenHost:        "0.0.0.0",
				ListenPort:        9000,
				BackendsJSON:      `[{"host":"upstream","port":9001}]`,
				LoadBalancingJSON: `{"strategy":"adaptive"}`,
				TuningJSON:        `{"proxy_protocol":{}}`,
				RelayChainJSON:    "[]",
				RelayLayersJSON:   "[]",
				ListenMode:        "tcp",
				TagsJSON:          "[]",
				Enabled:           true,
				Revision:          1,
			}},
		},
		relayByAgent: map[string][]storage.RelayListenerRow{},
		wireGuardByAgent: map[string][]storage.WireGuardProfileRow{
			"edge-1": {{ID: 7, AgentID: "edge-1", Enabled: true}},
		},
	}
	svc := NewL4RuleService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, store)

	_, err := svc.Update(context.Background(), "edge-1", 1, L4RuleInput{
		ListenMode:         stringPtrL4("wireguard"),
		WireGuardProfileID: intPtrL4(7),
	})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Update() error = %v, want ErrInvalidArgument", err)
	}
	if err == nil || !strings.Contains(err.Error(), "agent does not support WireGuard") {
		t.Fatalf("Update() error = %v, want WireGuard capability message", err)
	}
}

func TestL4UpdateWireGuardProxyEgressRequiresAgentCapability(t *testing.T) {
	store := &fakeL4Store{
		agents: []storage.AgentRow{{
			ID:               "edge-1",
			Name:             "Edge 1",
			CapabilitiesJSON: marshalStringArray([]string{"l4"}),
		}},
		httpRulesByID: map[string][]storage.HTTPRuleRow{},
		l4RulesByID: map[string][]storage.L4RuleRow{
			"edge-1": {{
				ID:                1,
				AgentID:           "edge-1",
				Name:              "tcp rule",
				Protocol:          "tcp",
				ListenHost:        "0.0.0.0",
				ListenPort:        9000,
				BackendsJSON:      `[{"host":"upstream","port":9001}]`,
				LoadBalancingJSON: `{"strategy":"adaptive"}`,
				TuningJSON:        `{"proxy_protocol":{}}`,
				RelayChainJSON:    "[]",
				RelayLayersJSON:   "[]",
				ListenMode:        "tcp",
				TagsJSON:          "[]",
				Enabled:           true,
				Revision:          1,
			}},
		},
		relayByAgent: map[string][]storage.RelayListenerRow{},
		wireGuardByAgent: map[string][]storage.WireGuardProfileRow{
			"edge-1": {{ID: 7, AgentID: "edge-1", Enabled: true}},
		},
	}
	svc := NewL4RuleService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, store)

	_, err := svc.Update(context.Background(), "edge-1", 1, L4RuleInput{
		ListenMode:         stringPtrL4("proxy"),
		ProxyEgressMode:    stringPtrL4("wireguard"),
		WireGuardProfileID: intPtrL4(7),
	})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Update() error = %v, want ErrInvalidArgument", err)
	}
	if err == nil || !strings.Contains(err.Error(), "agent does not support WireGuard") {
		t.Fatalf("Update() error = %v, want WireGuard capability message", err)
	}
}

func TestL4UpdateAllowsSwitchingAwayFromWireGuardWithoutCapability(t *testing.T) {
	profileID := 7
	store := &fakeL4Store{
		agents: []storage.AgentRow{{
			ID:               "edge-1",
			Name:             "Edge 1",
			CapabilitiesJSON: marshalStringArray([]string{"l4"}),
		}},
		httpRulesByID: map[string][]storage.HTTPRuleRow{},
		l4RulesByID: map[string][]storage.L4RuleRow{
			"edge-1": {{
				ID:                   1,
				AgentID:              "edge-1",
				Name:                 "wg rule",
				Protocol:             "tcp",
				ListenHost:           "0.0.0.0",
				ListenPort:           9000,
				BackendsJSON:         `[{"host":"upstream","port":9001}]`,
				LoadBalancingJSON:    `{"strategy":"adaptive"}`,
				TuningJSON:           `{"proxy_protocol":{}}`,
				RelayChainJSON:       "[]",
				RelayLayersJSON:      "[]",
				ListenMode:           "wireguard",
				WireGuardProfileID:   &profileID,
				WireGuardInboundMode: "address",
				WireGuardListenHost:  "10.8.0.1",
				TagsJSON:             "[]",
				Enabled:              true,
				Revision:             1,
			}},
		},
		relayByAgent: map[string][]storage.RelayListenerRow{},
		wireGuardByAgent: map[string][]storage.WireGuardProfileRow{
			"edge-1": {{ID: profileID, AgentID: "edge-1", Enabled: true}},
		},
	}
	svc := NewL4RuleService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, store)

	rule, err := svc.Update(context.Background(), "edge-1", 1, L4RuleInput{
		ListenMode: stringPtrL4("tcp"),
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if rule.ListenMode != "tcp" || rule.WireGuardProfileID != nil || rule.WireGuardListenHost != "" {
		t.Fatalf("Update() rule = %+v, want non-WireGuard listener with WireGuard fields cleared", rule)
	}
}

func TestL4UpdateAllowsSwitchingProxyEgressAwayFromWireGuardWithoutCapability(t *testing.T) {
	profileID := 7
	store := &fakeL4Store{
		agents: []storage.AgentRow{{
			ID:               "edge-1",
			Name:             "Edge 1",
			CapabilitiesJSON: marshalStringArray([]string{"l4"}),
		}},
		httpRulesByID: map[string][]storage.HTTPRuleRow{},
		l4RulesByID: map[string][]storage.L4RuleRow{
			"edge-1": {{
				ID:                   1,
				AgentID:              "edge-1",
				Name:                 "wg egress rule",
				Protocol:             "tcp",
				ListenHost:           "0.0.0.0",
				ListenPort:           1080,
				BackendsJSON:         "[]",
				LoadBalancingJSON:    `{"strategy":"adaptive"}`,
				TuningJSON:           `{"proxy_protocol":{}}`,
				RelayChainJSON:       "[]",
				RelayLayersJSON:      "[]",
				ListenMode:           "proxy",
				ProxyEgressMode:      "wireguard",
				WireGuardProfileID:   &profileID,
				WireGuardInboundMode: "address",
				TagsJSON:             "[]",
				Enabled:              true,
				Revision:             1,
			}},
		},
		relayByAgent: map[string][]storage.RelayListenerRow{},
		wireGuardByAgent: map[string][]storage.WireGuardProfileRow{
			"edge-1": {{ID: profileID, AgentID: "edge-1", Enabled: true}},
		},
	}
	svc := NewL4RuleService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, store)

	rule, err := svc.Update(context.Background(), "edge-1", 1, L4RuleInput{
		ListenMode: stringPtrL4("tcp"),
		Backends:   &[]L4Backend{{Host: "upstream", Port: 9001}},
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if rule.ListenMode != "tcp" || rule.ProxyEgressMode != "" || rule.WireGuardProfileID != nil {
		t.Fatalf("Update() rule = %+v, want normal backend forwarding without WireGuard fields", rule)
	}
	if len(rule.Backends) != 1 || rule.Backends[0].Host != "upstream" || rule.Backends[0].Port != 9001 {
		t.Fatalf("Update() backends = %+v, want normal backend forwarding", rule.Backends)
	}
	row := store.l4RulesByID["edge-1"][0]
	if row.ListenMode != "tcp" || row.ProxyEgressMode != "" || row.WireGuardProfileID != nil {
		t.Fatalf("persisted rule = %+v, want WireGuard egress fields cleared", row)
	}
}

func TestNormalizeL4RuleInputAcceptsProxyEntryProxyEgress(t *testing.T) {
	protocol := "tcp"
	listenMode := "proxy"
	egressMode := "proxy"
	egressURL := "http://user:pass@127.0.0.1:8080"
	input := L4RuleInput{
		Protocol:        &protocol,
		ListenHost:      stringPtrL4("127.0.0.1"),
		ListenPort:      intPtrL4(1080),
		ListenMode:      &listenMode,
		ProxyEgressMode: &egressMode,
		ProxyEgressURL:  &egressURL,
	}
	rule, err := normalizeL4RuleInput(input, L4Rule{}, 1)
	if err != nil {
		t.Fatalf("normalizeL4RuleInput() error = %v", err)
	}
	if rule.ProxyEgressURL != egressURL {
		t.Fatalf("ProxyEgressURL = %q", rule.ProxyEgressURL)
	}
}

func TestL4RuleServiceUpdateProxyEgressClearsStaleRelayFields(t *testing.T) {
	current := L4Rule{
		ID:              1,
		AgentID:         "local",
		Name:            "proxy entry",
		Protocol:        "tcp",
		ListenHost:      "0.0.0.0",
		ListenPort:      1080,
		ListenMode:      "proxy",
		ProxyEgressMode: "relay",
		RelayChain:      []int{101},
		RelayLayers:     [][]int{{101}},
		RelayObfs:       true,
		Enabled:         true,
		Revision:        3,
	}
	store := &fakeL4Store{
		l4RulesByID: map[string][]storage.L4RuleRow{
			"local": {l4RuleToRow(current)},
		},
		relayByAgent: map[string][]storage.RelayListenerRow{},
	}
	svc := NewL4RuleService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, store)

	rule, err := svc.Update(context.Background(), "local", 1, L4RuleInput{
		ProxyEgressMode: stringPtrL4("proxy"),
		ProxyEgressURL:  stringPtrL4("socks://127.0.0.1:1080"),
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if len(rule.RelayChain) != 0 || len(rule.RelayLayers) != 0 || rule.RelayObfs {
		t.Fatalf("relay fields = chain=%+v layers=%+v obfs=%v", rule.RelayChain, rule.RelayLayers, rule.RelayObfs)
	}
	row := store.l4RulesByID["local"][0]
	if row.RelayChainJSON != "[]" || row.RelayLayersJSON != "[]" || row.RelayObfs {
		t.Fatalf("persisted relay fields = chain=%s layers=%s obfs=%v", row.RelayChainJSON, row.RelayLayersJSON, row.RelayObfs)
	}
}

func TestNormalizeL4RuleInputClearsProxyEgressURLWhenSwitchingToRelay(t *testing.T) {
	egressMode := "relay"
	egressURL := ""
	relayLayers := [][]int{{101}}
	fallback := L4Rule{
		ID:              1,
		Protocol:        "tcp",
		ListenHost:      "127.0.0.1",
		ListenPort:      1080,
		ListenMode:      "proxy",
		ProxyEgressMode: "proxy",
		ProxyEgressURL:  "socks5://user:secret@127.0.0.1:1080",
		Enabled:         true,
	}
	rule, err := normalizeL4RuleInput(L4RuleInput{
		ProxyEgressMode: &egressMode,
		ProxyEgressURL:  &egressURL,
		RelayLayers:     &relayLayers,
	}, fallback, fallback.ID)
	if err != nil {
		t.Fatalf("normalizeL4RuleInput() error = %v", err)
	}
	if rule.ProxyEgressMode != "relay" {
		t.Fatalf("ProxyEgressMode = %q, want relay", rule.ProxyEgressMode)
	}
	if rule.ProxyEgressURL != "" {
		t.Fatalf("ProxyEgressURL = %q, want cleared", rule.ProxyEgressURL)
	}
}

func TestL4RuleServiceUpdatePreservesRedactedProxyEntrySecrets(t *testing.T) {
	current := L4Rule{
		ID:         1,
		AgentID:    "local",
		Name:       "proxy entry",
		Protocol:   "tcp",
		ListenHost: "0.0.0.0",
		ListenPort: 1080,
		ListenMode: "proxy",
		ProxyEntryAuth: L4ProxyEntryAuth{
			Enabled:  true,
			Username: "client",
			Password: "entry-secret",
		},
		ProxyEgressMode: "proxy",
		ProxyEgressURL:  "socks://egress:egress-secret@127.0.0.1:1080",
		Enabled:         true,
		Revision:        4,
	}
	store := &fakeL4Store{
		l4RulesByID: map[string][]storage.L4RuleRow{
			"local": {l4RuleToRow(current)},
		},
	}
	svc := NewL4RuleService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, store)

	rule, err := svc.Update(context.Background(), "local", 1, L4RuleInput{
		Protocol:        stringPtrL4("tcp"),
		ListenHost:      stringPtrL4("0.0.0.0"),
		ListenPort:      intPtrL4(1080),
		ListenMode:      stringPtrL4("proxy"),
		ProxyEntryAuth:  &L4ProxyEntryAuth{Enabled: true, Username: "client"},
		ProxyEgressMode: stringPtrL4("proxy"),
		ProxyEgressURL:  stringPtrL4("socks://egress:xxxxx@127.0.0.1:1080"),
		Enabled:         boolPtrL4(true),
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if rule.ProxyEntryAuth.Password != "entry-secret" {
		t.Fatalf("ProxyEntryAuth.Password = %q, want preserved secret", rule.ProxyEntryAuth.Password)
	}
	if rule.ProxyEgressURL != "socks://egress:egress-secret@127.0.0.1:1080" {
		t.Fatalf("ProxyEgressURL = %q, want preserved secret URL", rule.ProxyEgressURL)
	}
}

func TestL4RuleServiceUpdateProxyEntryClearsBackendFields(t *testing.T) {
	store := &fakeL4Store{
		l4RulesByID: map[string][]storage.L4RuleRow{
			"local": {{
				ID:           1,
				AgentID:      "local",
				Name:         "forwarding rule",
				Protocol:     "tcp",
				ListenHost:   "0.0.0.0",
				ListenPort:   9000,
				UpstreamHost: "upstream",
				UpstreamPort: 9001,
				BackendsJSON: `[{"host":"upstream","port":9001}]`,
				Enabled:      true,
				Revision:     3,
			}},
		},
		relayByAgent: map[string][]storage.RelayListenerRow{
			"local": {{
				ID:      7,
				AgentID: "local",
				Enabled: true,
			}},
		},
	}
	svc := NewL4RuleService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, store)

	listenMode := "proxy"
	egressMode := "relay"
	rule, err := svc.Update(context.Background(), "local", 1, L4RuleInput{
		ListenMode:      &listenMode,
		ProxyEgressMode: &egressMode,
		RelayLayers:     &[][]int{{7}},
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if len(rule.Backends) != 0 || rule.UpstreamHost != "" || rule.UpstreamPort != 0 {
		t.Fatalf("rule backend fields = backends=%+v upstream=%q:%d", rule.Backends, rule.UpstreamHost, rule.UpstreamPort)
	}
	row := store.l4RulesByID["local"][0]
	if row.BackendsJSON != "[]" || row.UpstreamHost != "" || row.UpstreamPort != 0 {
		t.Fatalf("persisted backend fields = backends=%s upstream=%q:%d", row.BackendsJSON, row.UpstreamHost, row.UpstreamPort)
	}
}

func TestL4RuleServiceCreateAllocatesGlobalIDsAcrossAgentsInSQLiteStore(t *testing.T) {
	dataRoot := t.TempDir()
	store, err := storage.NewSQLiteStore(dataRoot, "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})

	for _, agentID := range []string{"agent-a", "agent-b"} {
		if err := store.SaveAgent(context.Background(), storage.AgentRow{
			ID:               agentID,
			Name:             agentID,
			AgentToken:       agentID + "-token",
			CapabilitiesJSON: marshalStringArray([]string{"http_rules", "l4"}),
		}); err != nil {
			t.Fatalf("SaveAgent(%q) error = %v", agentID, err)
		}
	}

	svc := NewL4RuleService(config.Config{}, store)

	first, err := svc.Create(context.Background(), "agent-a", L4RuleInput{
		Protocol:   stringPtrL4("tcp"),
		ListenPort: intPtrL4(9000),
		Backends:   &[]L4Backend{{Host: "upstream-a", Port: 9001}},
	})
	if err != nil {
		t.Fatalf("Create(agent-a) error = %v", err)
	}
	second, err := svc.Create(context.Background(), "agent-b", L4RuleInput{
		Protocol:   stringPtrL4("tcp"),
		ListenPort: intPtrL4(9100),
		Backends:   &[]L4Backend{{Host: "upstream-b", Port: 9101}},
	})
	if err != nil {
		t.Fatalf("Create(agent-b) error = %v", err)
	}

	if first.ID != 1 {
		t.Fatalf("first rule id = %d", first.ID)
	}
	if second.ID != 2 {
		t.Fatalf("second rule id = %d", second.ID)
	}
}

func TestL4RuleServiceCreateAllocatesIDsAfterExistingHTTPRulesInSQLiteStore(t *testing.T) {
	dataRoot := t.TempDir()
	store, err := storage.NewSQLiteStore(dataRoot, "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})

	for _, agentID := range []string{"agent-a", "agent-b"} {
		if err := store.SaveAgent(context.Background(), storage.AgentRow{
			ID:               agentID,
			Name:             agentID,
			AgentToken:       agentID + "-token",
			CapabilitiesJSON: marshalStringArray([]string{"http_rules", "l4"}),
		}); err != nil {
			t.Fatalf("SaveAgent(%q) error = %v", agentID, err)
		}
	}

	httpSvc := NewRuleService(config.Config{}, store)
	l4Svc := NewL4RuleService(config.Config{}, store)

	httpRule, err := httpSvc.Create(context.Background(), "agent-a", HTTPRuleInput{
		FrontendURL: stringPtrRule("http://agent-a.example.com"),
		Backends:    &[]HTTPRuleBackend{{URL: "http://backend-a.example.internal:8096"}},
	})
	if err != nil {
		t.Fatalf("Create HTTP rule error = %v", err)
	}

	l4Rule, err := l4Svc.Create(context.Background(), "agent-b", L4RuleInput{
		Protocol:   stringPtrL4("tcp"),
		ListenPort: intPtrL4(9100),
		Backends:   &[]L4Backend{{Host: "backend-b.example.internal", Port: 9101}},
	})
	if err != nil {
		t.Fatalf("Create L4 rule error = %v", err)
	}

	if httpRule.ID != 1 {
		t.Fatalf("httpRule.ID = %d", httpRule.ID)
	}
	if l4Rule.ID != 2 {
		t.Fatalf("l4Rule.ID = %d", l4Rule.ID)
	}
}

func TestL4RuleServiceCreateClearsRelayObfsWithoutRelayChain(t *testing.T) {
	store := &fakeL4Store{l4RulesByID: map[string][]storage.L4RuleRow{}}
	svc := NewL4RuleService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, store)

	rule, err := svc.Create(context.Background(), "local", L4RuleInput{
		Protocol:   stringPtrL4("tcp"),
		ListenPort: intPtrL4(9000),
		Backends:   &[]L4Backend{{Host: "upstream", Port: 9001}},
		RelayObfs:  boolPtrL4(true),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if rule.RelayObfs {
		t.Fatalf("expected relay_obfs to be cleared when relay_chain is empty")
	}
}

func TestL4RuleServiceCreateDetachesCanceledTriggerContext(t *testing.T) {
	store := &fakeL4Store{
		l4RulesByID: map[string][]storage.L4RuleRow{},
		relayByAgent: map[string][]storage.RelayListenerRow{
			"local": {},
		},
	}
	svc := NewL4RuleService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, store)

	type requestContextKey string
	requestCtx := context.WithValue(context.Background(), requestContextKey("trace"), "l4-create")
	requestCtx, cancel := context.WithCancel(requestCtx)
	cancel()

	triggerCalls := 0
	svc.SetLocalApplyTrigger(func(ctx context.Context) error {
		triggerCalls++
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("trigger ctx err = %v", err)
		}
		if got := ctx.Value(requestContextKey("trace")); got != "l4-create" {
			return fmt.Errorf("trigger ctx trace = %v", got)
		}
		return nil
	})

	rule, err := svc.Create(requestCtx, "local", L4RuleInput{
		Protocol:   stringPtrL4("tcp"),
		ListenPort: intPtrL4(9000),
		Backends:   &[]L4Backend{{Host: "upstream", Port: 9001}},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if rule.ID != 1 {
		t.Fatalf("Create() rule = %+v", rule)
	}
	if triggerCalls != 1 {
		t.Fatalf("triggerCalls = %d", triggerCalls)
	}
}

func TestL4RuleServiceCreateClearsRelayObfsForUDP(t *testing.T) {
	store := &fakeL4Store{l4RulesByID: map[string][]storage.L4RuleRow{}}
	svc := NewL4RuleService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, store)

	rule, err := svc.Create(context.Background(), "local", L4RuleInput{
		Protocol:   stringPtrL4("udp"),
		ListenPort: intPtrL4(9000),
		Backends:   &[]L4Backend{{Host: "upstream", Port: 9001}},
		RelayObfs:  boolPtrL4(true),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if rule.RelayObfs {
		t.Fatalf("expected relay_obfs to be cleared for udp protocol")
	}
}

func TestL4RuleServiceUpdateClearsRelayObfsWhenRelayChainRemoved(t *testing.T) {
	store := &fakeL4Store{
		l4RulesByID: map[string][]storage.L4RuleRow{
			"local": {{
				ID:              1,
				AgentID:         "local",
				Name:            "relay rule",
				Protocol:        "tcp",
				ListenHost:      "0.0.0.0",
				ListenPort:      9000,
				BackendsJSON:    `[{"host":"upstream","port":9001}]`,
				RelayLayersJSON: `[[7]]`,
				RelayObfs:       true,
				Enabled:         true,
				Revision:        3,
			}},
		},
		relayByAgent: map[string][]storage.RelayListenerRow{
			"local": {{
				ID:      7,
				AgentID: "local",
				Enabled: true,
			}},
		},
	}
	svc := NewL4RuleService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, store)

	rule, err := svc.Update(context.Background(), "local", 1, L4RuleInput{
		RelayLayers: &[][]int{},
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if len(rule.RelayChain) != 0 {
		t.Fatalf("expected relay_chain to be cleared, got %+v", rule.RelayChain)
	}
	if rule.RelayObfs {
		t.Fatalf("expected relay_obfs to be cleared when relay_chain is removed")
	}
}

func TestL4RuleServiceUpdateRejectsRelayChainOnly(t *testing.T) {
	store := &fakeL4Store{
		l4RulesByID: map[string][]storage.L4RuleRow{
			"local": {{
				ID:              1,
				AgentID:         "local",
				Name:            "relay rule",
				Protocol:        "tcp",
				ListenHost:      "0.0.0.0",
				ListenPort:      9000,
				BackendsJSON:    `[{"host":"upstream","port":9001}]`,
				RelayLayersJSON: `[[7],[8,9]]`,
				Enabled:         true,
				Revision:        3,
			}},
		},
		relayByAgent: map[string][]storage.RelayListenerRow{
			"local": {{
				ID:      5,
				AgentID: "local",
				Enabled: true,
			}},
		},
	}
	svc := NewL4RuleService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, store)

	_, err := svc.Update(context.Background(), "local", 1, L4RuleInput{
		RelayChain: &[]int{5},
	})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Update() error = %v, want ErrInvalidArgument", err)
	}
}

func TestL4RuleServiceUpdateClearsRelayChainWhenRelayLayersSupplied(t *testing.T) {
	store := &fakeL4Store{
		l4RulesByID: map[string][]storage.L4RuleRow{
			"local": {{
				ID:              1,
				AgentID:         "local",
				Name:            "relay rule",
				Protocol:        "tcp",
				ListenHost:      "0.0.0.0",
				ListenPort:      9000,
				BackendsJSON:    `[{"host":"upstream","port":9001}]`,
				RelayLayersJSON: `[[7]]`,
				Enabled:         true,
				Revision:        3,
			}},
		},
		relayByAgent: map[string][]storage.RelayListenerRow{
			"local": {{
				ID:      8,
				AgentID: "local",
				Enabled: true,
			}},
		},
	}
	svc := NewL4RuleService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, store)

	rule, err := svc.Update(context.Background(), "local", 1, L4RuleInput{
		RelayLayers: &[][]int{{8}},
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if len(rule.RelayChain) != 0 {
		t.Fatalf("expected relay_chain to be cleared, got %+v", rule.RelayChain)
	}
	if len(rule.RelayLayers) != 1 || len(rule.RelayLayers[0]) != 1 || rule.RelayLayers[0][0] != 8 {
		t.Fatalf("expected relay_layers to update, got %+v", rule.RelayLayers)
	}
	if got := store.l4RulesByID["local"][0].RelayChainJSON; got != `[]` {
		t.Fatalf("persisted relay_chain = %s", got)
	}
}

func TestL4RuleServiceUpdateClearsRelayWhenRelayLayersCleared(t *testing.T) {
	store := &fakeL4Store{
		l4RulesByID: map[string][]storage.L4RuleRow{
			"local": {{
				ID:              1,
				AgentID:         "local",
				Name:            "relay rule",
				Protocol:        "tcp",
				ListenHost:      "0.0.0.0",
				ListenPort:      9000,
				BackendsJSON:    `[{"host":"upstream","port":9001}]`,
				RelayLayersJSON: `[[7]]`,
				Enabled:         true,
				Revision:        3,
			}},
		},
	}
	svc := NewL4RuleService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, store)

	rule, err := svc.Update(context.Background(), "local", 1, L4RuleInput{
		RelayLayers: &[][]int{},
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if len(rule.RelayChain) != 0 {
		t.Fatalf("expected relay_chain to be cleared, got %+v", rule.RelayChain)
	}
	if len(rule.RelayLayers) != 0 {
		t.Fatalf("expected relay_layers to be cleared, got %+v", rule.RelayLayers)
	}
	if got := store.l4RulesByID["local"][0].RelayChainJSON; got != `[]` {
		t.Fatalf("persisted relay_chain = %s", got)
	}
}

func TestL4RuleServiceUpdateDefaultsInvalidLoadBalancingToAdaptive(t *testing.T) {
	store := &fakeL4Store{
		l4RulesByID: map[string][]storage.L4RuleRow{
			"local": {{
				ID:                1,
				AgentID:           "local",
				Name:              "relay rule",
				Protocol:          "tcp",
				ListenHost:        "0.0.0.0",
				ListenPort:        9000,
				BackendsJSON:      `[{"host":"upstream","port":9001}]`,
				LoadBalancingJSON: `{}`,
				Enabled:           true,
				Revision:          3,
			}},
		},
	}
	svc := NewL4RuleService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, store)

	rule, err := svc.Update(context.Background(), "local", 1, L4RuleInput{
		Protocol: stringPtrL4("tcp"),
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	if rule.LoadBalancing.Strategy != "adaptive" {
		t.Fatalf("Update() load_balancing = %+v", rule.LoadBalancing)
	}
	if got := store.l4RulesByID["local"][0].LoadBalancingJSON; got != `{"strategy":"adaptive"}` {
		t.Fatalf("persisted load_balancing_json = %q", got)
	}
}

func TestL4RuleServiceUpdatePreservesExplicitLoadBalancingStrategies(t *testing.T) {
	for _, strategy := range []string{"round_robin", "random"} {
		t.Run(strategy, func(t *testing.T) {
			lbJSON := `{"strategy":"` + strategy + `"}`
			store := &fakeL4Store{
				l4RulesByID: map[string][]storage.L4RuleRow{
					"local": {{
						ID:                1,
						AgentID:           "local",
						Name:              "relay rule",
						Protocol:          "tcp",
						ListenHost:        "0.0.0.0",
						ListenPort:        9000,
						BackendsJSON:      `[{"host":"upstream","port":9001}]`,
						LoadBalancingJSON: lbJSON,
						Enabled:           true,
						Revision:          3,
					}},
				},
			}
			svc := NewL4RuleService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, store)

			rule, err := svc.Update(context.Background(), "local", 1, L4RuleInput{
				Protocol: stringPtrL4("tcp"),
			})
			if err != nil {
				t.Fatalf("Update() error = %v", err)
			}

			if rule.LoadBalancing.Strategy != strategy {
				t.Fatalf("Update() load_balancing = %+v", rule.LoadBalancing)
			}
			if got := store.l4RulesByID["local"][0].LoadBalancingJSON; got != lbJSON {
				t.Fatalf("persisted load_balancing_json = %q", got)
			}
		})
	}
}

func TestL4RuleServiceUpdatePreservesRelayLayersWhenSwitchingToUDP(t *testing.T) {
	store := &fakeL4Store{
		l4RulesByID: map[string][]storage.L4RuleRow{
			"local": {{
				ID:              1,
				AgentID:         "local",
				Name:            "relay rule",
				Protocol:        "tcp",
				ListenHost:      "0.0.0.0",
				ListenPort:      9000,
				BackendsJSON:    `[{"host":"upstream","port":9001}]`,
				RelayLayersJSON: `[[7]]`,
				RelayObfs:       true,
				Enabled:         true,
				Revision:        3,
			}},
		},
		relayByAgent: map[string][]storage.RelayListenerRow{
			"local": {{
				ID:      7,
				AgentID: "local",
				Enabled: true,
			}},
		},
	}
	svc := NewL4RuleService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, store)

	rule, err := svc.Update(context.Background(), "local", 1, L4RuleInput{
		Protocol: stringPtrL4("udp"),
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if rule.Protocol != "udp" {
		t.Fatalf("expected protocol udp, got %q", rule.Protocol)
	}
	if len(rule.RelayChain) != 0 {
		t.Fatalf("expected relay_chain to be neutral, got %+v", rule.RelayChain)
	}
	if len(rule.RelayLayers) != 1 || len(rule.RelayLayers[0]) != 1 || rule.RelayLayers[0][0] != 7 {
		t.Fatalf("expected relay_layers to be preserved for udp, got %+v", rule.RelayLayers)
	}
	if rule.RelayObfs {
		t.Fatalf("expected relay_obfs to be cleared for udp protocol")
	}
}

func TestL4RuleServiceCreateRejectsDuplicateRelayLayerEntries(t *testing.T) {
	store := &fakeL4Store{
		l4RulesByID: map[string][]storage.L4RuleRow{},
		relayByAgent: map[string][]storage.RelayListenerRow{
			"local": {{
				ID:      7,
				AgentID: "local",
				Enabled: true,
			}},
		},
	}
	svc := NewL4RuleService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	_, err := svc.Create(context.Background(), "local", L4RuleInput{
		ListenPort:  intPtrL4(9000),
		Backends:    &[]L4Backend{{Host: "upstream", Port: 9001}},
		RelayLayers: &[][]int{{7, 7}},
	})
	if err == nil {
		t.Fatal("Create() error = nil")
	}
	if err.Error() != "invalid argument: relay_layers entries must not contain duplicates" {
		t.Fatalf("Create() error = %v", err)
	}
}

func TestL4RuleServiceCreateRejectsDuplicateRelayLayerEntriesAcrossLayers(t *testing.T) {
	store := &fakeL4Store{
		l4RulesByID: map[string][]storage.L4RuleRow{},
		relayByAgent: map[string][]storage.RelayListenerRow{
			"local": {
				{ID: 7, AgentID: "local", Enabled: true},
				{ID: 8, AgentID: "local", Enabled: true},
			},
		},
	}
	svc := NewL4RuleService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	_, err := svc.Create(context.Background(), "local", L4RuleInput{
		ListenPort:  intPtrL4(9000),
		Backends:    &[]L4Backend{{Host: "upstream", Port: 9001}},
		RelayLayers: &[][]int{{7, 8}, {7}},
	})
	if err == nil {
		t.Fatal("Create() error = nil")
	}
	if err.Error() != "invalid argument: relay_layers entries must not repeat listener IDs across layers" {
		t.Fatalf("Create() error = %v", err)
	}
}

func TestL4RuleServiceCreateRejectsUnknownRelayLayerListener(t *testing.T) {
	store := &fakeL4Store{
		l4RulesByID: map[string][]storage.L4RuleRow{},
		relayByAgent: map[string][]storage.RelayListenerRow{
			"local": {
				{ID: 7, AgentID: "local", Enabled: true},
			},
		},
	}
	svc := NewL4RuleService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	_, err := svc.Create(context.Background(), "local", L4RuleInput{
		ListenPort:  intPtrL4(9000),
		Backends:    &[]L4Backend{{Host: "upstream", Port: 9001}},
		RelayLayers: &[][]int{{7, 8}},
	})
	if err == nil {
		t.Fatal("Create() error = nil")
	}
	if err.Error() != "invalid argument: relay listener not found: 8" {
		t.Fatalf("Create() error = %v", err)
	}
}

func TestL4RuleServiceCreateAllowsCrossAgentWireGuardRelayListener(t *testing.T) {
	profileID := 41
	store := &fakeL4Store{
		agents:      []storage.AgentRow{{ID: "remote-relay", Name: "remote-relay", CapabilitiesJSON: `["l4"]`}},
		l4RulesByID: map[string][]storage.L4RuleRow{},
		relayByAgent: map[string][]storage.RelayListenerRow{
			"remote-relay": {{
				ID:                 7,
				AgentID:            "remote-relay",
				Enabled:            true,
				TransportMode:      "wireguard",
				WireGuardProfileID: &profileID,
			}},
		},
	}
	svc := NewL4RuleService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	rule, err := svc.Create(context.Background(), "local", L4RuleInput{
		ListenPort:  intPtrL4(9000),
		Backends:    &[]L4Backend{{Host: "upstream", Port: 9001}},
		RelayLayers: &[][]int{{7}},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if len(rule.RelayLayers) != 1 || len(rule.RelayLayers[0]) != 1 || rule.RelayLayers[0][0] != 7 {
		t.Fatalf("RelayLayers = %+v", rule.RelayLayers)
	}
}

func TestL4RuleServiceCreateAllowsSameAgentWireGuardRelayListener(t *testing.T) {
	profileID := 41
	store := &fakeL4Store{
		l4RulesByID: map[string][]storage.L4RuleRow{},
		relayByAgent: map[string][]storage.RelayListenerRow{
			"local": {{
				ID:                 7,
				AgentID:            "local",
				Enabled:            true,
				TransportMode:      "wireguard",
				WireGuardProfileID: &profileID,
			}},
		},
	}
	svc := NewL4RuleService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	rule, err := svc.Create(context.Background(), "local", L4RuleInput{
		ListenPort:  intPtrL4(9000),
		Backends:    &[]L4Backend{{Host: "upstream", Port: 9001}},
		RelayLayers: &[][]int{{7}},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if len(rule.RelayLayers) != 1 || len(rule.RelayLayers[0]) != 1 || rule.RelayLayers[0][0] != 7 {
		t.Fatalf("RelayLayers = %+v", rule.RelayLayers)
	}
}

func TestL4RuleServiceCreateAllowsCrossAgentTLSRelayListener(t *testing.T) {
	store := &fakeL4Store{
		agents:      []storage.AgentRow{{ID: "remote-relay", Name: "remote-relay", CapabilitiesJSON: `["l4"]`}},
		l4RulesByID: map[string][]storage.L4RuleRow{},
		relayByAgent: map[string][]storage.RelayListenerRow{
			"remote-relay": {{
				ID:            7,
				AgentID:       "remote-relay",
				Enabled:       true,
				TransportMode: "tls_tcp",
			}},
		},
	}
	svc := NewL4RuleService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	rule, err := svc.Create(context.Background(), "local", L4RuleInput{
		ListenPort:  intPtrL4(9000),
		Backends:    &[]L4Backend{{Host: "upstream", Port: 9001}},
		RelayLayers: &[][]int{{7}},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if len(rule.RelayLayers) != 1 || len(rule.RelayLayers[0]) != 1 || rule.RelayLayers[0][0] != 7 {
		t.Fatalf("RelayLayers = %+v", rule.RelayLayers)
	}
}

func TestL4RuleServiceUpdateAllowsCrossAgentWireGuardRelayListener(t *testing.T) {
	profileID := 41
	store := &fakeL4Store{
		agents: []storage.AgentRow{{ID: "remote-relay", Name: "remote-relay", CapabilitiesJSON: `["l4"]`}},
		l4RulesByID: map[string][]storage.L4RuleRow{
			"local": {{
				ID:                1,
				AgentID:           "local",
				Name:              "existing",
				Protocol:          "tcp",
				ListenHost:        "0.0.0.0",
				ListenPort:        9000,
				BackendsJSON:      `[{"host":"upstream","port":9001}]`,
				LoadBalancingJSON: `{"strategy":"adaptive"}`,
				Enabled:           true,
				Revision:          3,
			}},
		},
		relayByAgent: map[string][]storage.RelayListenerRow{
			"remote-relay": {{
				ID:                 7,
				AgentID:            "remote-relay",
				Enabled:            true,
				TransportMode:      "wireguard",
				WireGuardProfileID: &profileID,
			}},
		},
	}
	svc := NewL4RuleService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	rule, err := svc.Update(context.Background(), "local", 1, L4RuleInput{
		RelayLayers: &[][]int{{7}},
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if len(rule.RelayLayers) != 1 || len(rule.RelayLayers[0]) != 1 || rule.RelayLayers[0][0] != 7 {
		t.Fatalf("RelayLayers = %+v", rule.RelayLayers)
	}
}

func TestL4RuleServiceDeleteUpdatesRemoteAgentDesiredRevision(t *testing.T) {
	store := &fakeL4Store{
		agents: []storage.AgentRow{{
			ID:               "edge-1",
			Name:             "Edge 1",
			CapabilitiesJSON: `["l4"]`,
			DesiredRevision:  4,
			CurrentRevision:  4,
		}},
		l4RulesByID: map[string][]storage.L4RuleRow{
			"edge-1": {{
				ID:           1,
				AgentID:      "edge-1",
				Protocol:     "tcp",
				ListenHost:   "0.0.0.0",
				ListenPort:   50381,
				BackendsJSON: `[{"host":"127.0.0.1","port":26966}]`,
				Enabled:      true,
				Revision:     4,
			}},
		},
		relayByAgent: map[string][]storage.RelayListenerRow{},
	}
	svc := NewL4RuleService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	deleted, err := svc.Delete(context.Background(), "edge-1", 1)
	if err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if deleted.ID != 1 {
		t.Fatalf("deleted.ID = %d", deleted.ID)
	}
	if len(store.l4RulesByID["edge-1"]) != 0 {
		t.Fatalf("l4 rules still stored: %+v", store.l4RulesByID["edge-1"])
	}
	if store.agents[0].DesiredRevision != 5 {
		t.Fatalf("remote desired_revision = %d", store.agents[0].DesiredRevision)
	}
	if store.loadSnapshotCalls != 0 {
		t.Fatalf("LoadAgentSnapshot() calls = %d", store.loadSnapshotCalls)
	}
}

func TestL4RuleServiceDeleteCascadesL4RuleTraffic(t *testing.T) {
	store := &fakeL4Store{
		agents: []storage.AgentRow{{
			ID:               "edge-1",
			CapabilitiesJSON: `["l4"]`,
		}},
		httpRulesByID: map[string][]storage.HTTPRuleRow{},
		l4RulesByID: map[string][]storage.L4RuleRow{
			"edge-1": {{
				ID:           12,
				AgentID:      "edge-1",
				Protocol:     "tcp",
				ListenHost:   "0.0.0.0",
				ListenPort:   50381,
				BackendsJSON: `[{"host":"127.0.0.1","port":26966}]`,
				Enabled:      true,
			}},
		},
		relayByAgent: map[string][]storage.RelayListenerRow{},
	}
	svc := NewL4RuleService(config.Config{LocalAgentID: "local"}, store)

	if _, err := svc.Delete(context.Background(), "edge-1", 12); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if len(store.trafficDeletes) != 1 {
		t.Fatalf("traffic deletes = %+v, want one scope delete", store.trafficDeletes)
	}
	if got := store.trafficDeletes[0]; got != (trafficScopeDeleteCall{agentID: "edge-1", scopeType: "l4_rule", scopeID: "12"}) {
		t.Fatalf("traffic delete = %+v", got)
	}
}

func TestL4RuleServiceDeleteTrafficCleanupIsBestEffortAfterApply(t *testing.T) {
	order := []string{}
	store := &fakeL4Store{
		agents: []storage.AgentRow{{
			ID:               "local",
			CapabilitiesJSON: `["l4"]`,
		}},
		httpRulesByID: map[string][]storage.HTTPRuleRow{},
		l4RulesByID: map[string][]storage.L4RuleRow{
			"local": {{
				ID:           13,
				AgentID:      "local",
				Protocol:     "tcp",
				ListenHost:   "0.0.0.0",
				ListenPort:   50381,
				UpstreamHost: "127.0.0.1",
				UpstreamPort: 26966,
				Enabled:      true,
			}},
		},
		relayByAgent:     map[string][]storage.RelayListenerRow{},
		trafficDeleteErr: errors.New("cleanup failed"),
		trafficDeleteHook: func() {
			order = append(order, "cleanup")
		},
	}
	svc := NewL4RuleService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)
	svc.SetLocalApplyTrigger(func(context.Context) error {
		order = append(order, "apply")
		return nil
	})

	if _, err := svc.Delete(context.Background(), "local", 13); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if len(order) != 2 || order[0] != "apply" || order[1] != "cleanup" {
		t.Fatalf("order = %+v, want apply then cleanup", order)
	}
}

func TestL4RuleServiceCreateUsesRevisionAboveRemoteAgentSyncFloor(t *testing.T) {
	store := &fakeL4Store{
		agents: []storage.AgentRow{{
			ID:               "edge-1",
			Name:             "Edge 1",
			CapabilitiesJSON: `["l4"]`,
			DesiredRevision:  9,
			CurrentRevision:  9,
		}},
		httpRulesByID: map[string][]storage.HTTPRuleRow{},
		l4RulesByID: map[string][]storage.L4RuleRow{
			"edge-1": {{
				ID:           1,
				AgentID:      "edge-1",
				Protocol:     "tcp",
				ListenHost:   "0.0.0.0",
				ListenPort:   50381,
				UpstreamHost: "127.0.0.1",
				UpstreamPort: 26966,
				Enabled:      true,
				Revision:     4,
			}},
		},
		relayByAgent: map[string][]storage.RelayListenerRow{},
	}
	svc := NewL4RuleService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	rule, err := svc.Create(context.Background(), "edge-1", L4RuleInput{
		Protocol:   stringPtrL4("tcp"),
		ListenPort: intPtrL4(50382),
		Backends:   &[]L4Backend{{Host: "127.0.0.1", Port: 26967}},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	assertRevisionAboveFloor(t, "Create() revision", rule.Revision, 9)
	assertRevisionAboveFloor(t, "remote desired_revision", store.agents[0].DesiredRevision, 9)
	assertRevisionNotBehind(t, "remote desired_revision", store.agents[0].DesiredRevision, rule.Revision)
}

func TestL4RuleServiceCreateReassignsPreferredIDWhenHTTPRuleAlreadyUsesIt(t *testing.T) {
	store := &fakeL4Store{
		httpRulesByID: map[string][]storage.HTTPRuleRow{
			"local": {{
				ID:          9,
				AgentID:     "local",
				FrontendURL: "http://existing-http.example.com",
				BackendURL:  "http://127.0.0.1:8096",
				Revision:    2,
			}},
		},
		l4RulesByID: map[string][]storage.L4RuleRow{
			"local": {{
				ID:           7,
				AgentID:      "local",
				Protocol:     "tcp",
				ListenHost:   "0.0.0.0",
				ListenPort:   50381,
				UpstreamHost: "127.0.0.1",
				UpstreamPort: 26966,
				Enabled:      true,
				Revision:     3,
			}},
		},
		relayByAgent: map[string][]storage.RelayListenerRow{},
	}
	svc := NewL4RuleService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	rule, err := svc.Create(context.Background(), "local", L4RuleInput{
		ID:         intPtrL4(9),
		Protocol:   stringPtrL4("tcp"),
		ListenPort: intPtrL4(50382),
		Backends:   &[]L4Backend{{Host: "127.0.0.1", Port: 26967}},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if rule.ID != 10 {
		t.Fatalf("Create() id = %d, want 10", rule.ID)
	}
}

func TestL4RuleServiceUpdateUsesRevisionAboveRemoteAgentSyncFloor(t *testing.T) {
	store := &fakeL4Store{
		agents: []storage.AgentRow{{
			ID:               "edge-1",
			Name:             "Edge 1",
			CapabilitiesJSON: `["l4"]`,
			DesiredRevision:  9,
			CurrentRevision:  9,
		}},
		httpRulesByID: map[string][]storage.HTTPRuleRow{},
		l4RulesByID: map[string][]storage.L4RuleRow{
			"edge-1": {{
				ID:           1,
				AgentID:      "edge-1",
				Protocol:     "tcp",
				ListenHost:   "0.0.0.0",
				ListenPort:   50381,
				BackendsJSON: `[{"host":"127.0.0.1","port":26966}]`,
				Enabled:      true,
				Revision:     4,
			}},
		},
		relayByAgent: map[string][]storage.RelayListenerRow{},
	}
	svc := NewL4RuleService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	rule, err := svc.Update(context.Background(), "edge-1", 1, L4RuleInput{
		ListenPort: intPtrL4(50382),
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	assertRevisionAboveFloor(t, "Update() revision", rule.Revision, 9)
	assertRevisionAboveFloor(t, "remote desired_revision", store.agents[0].DesiredRevision, 9)
	assertRevisionNotBehind(t, "remote desired_revision", store.agents[0].DesiredRevision, rule.Revision)
}

func TestL4RuleServiceDeleteUsesRevisionAboveRemoteAgentSyncFloor(t *testing.T) {
	store := &fakeL4Store{
		agents: []storage.AgentRow{{
			ID:               "edge-1",
			Name:             "Edge 1",
			CapabilitiesJSON: `["l4"]`,
			DesiredRevision:  9,
			CurrentRevision:  9,
		}},
		httpRulesByID: map[string][]storage.HTTPRuleRow{},
		l4RulesByID: map[string][]storage.L4RuleRow{
			"edge-1": {{
				ID:           1,
				AgentID:      "edge-1",
				Protocol:     "tcp",
				ListenHost:   "0.0.0.0",
				ListenPort:   50381,
				UpstreamHost: "127.0.0.1",
				UpstreamPort: 26966,
				Enabled:      true,
				Revision:     4,
			}},
		},
		relayByAgent: map[string][]storage.RelayListenerRow{},
	}
	svc := NewL4RuleService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	deleted, err := svc.Delete(context.Background(), "edge-1", 1)
	if err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if deleted.ID != 1 {
		t.Fatalf("deleted.ID = %d", deleted.ID)
	}
	assertRevisionAboveFloor(t, "remote desired_revision", store.agents[0].DesiredRevision, 9)
}

func TestL4RuleServiceGetUsesDirectStoreLookup(t *testing.T) {
	store := &fakeL4Store{
		agents: []storage.AgentRow{{
			ID:               "edge-1",
			Name:             "Edge 1",
			CapabilitiesJSON: `["l4"]`,
		}},
		l4RulesByID: map[string][]storage.L4RuleRow{
			"edge-1": {{
				ID:           9,
				AgentID:      "edge-1",
				Protocol:     "tcp",
				ListenHost:   "0.0.0.0",
				ListenPort:   9000,
				UpstreamHost: "127.0.0.1",
				UpstreamPort: 9001,
				Revision:     3,
			}},
		},
		relayByAgent:   map[string][]storage.RelayListenerRow{},
		listL4RulesErr: context.DeadlineExceeded,
	}
	svc := NewL4RuleService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	rule, err := svc.Get(context.Background(), "edge-1", 9)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if rule.ID != 9 {
		t.Fatalf("Get() rule = %+v", rule)
	}
	if store.getL4RuleCalls != 1 {
		t.Fatalf("GetL4Rule() calls = %d", store.getL4RuleCalls)
	}
}

func stringPtrL4(value string) *string {
	return &value
}

func intPtrL4(value int) *int {
	return &value
}

func boolPtrL4(value bool) *bool {
	return &value
}

func materializedWireGuardURIProfileRow(t *testing.T, agentID string, id int, uri string) storage.WireGuardProfileRow {
	t.Helper()
	return materializedWireGuardURIProfileRowForRule(t, agentID, id, id, uri)
}

func materializedWireGuardURIProfileRowForRule(t *testing.T, agentID string, id int, ruleID int, uri string) storage.WireGuardProfileRow {
	t.Helper()
	parsed, err := ParseWireGuardURI(uri)
	if err != nil {
		t.Fatalf("ParseWireGuardURI() error = %v", err)
	}
	input := wireGuardProfileInputFromURI(parsed, fmt.Sprintf("l4-rule-%d-wireguard-egress", ruleID))
	input.ID = id
	profile, err := normalizeWireGuardProfileInput(input, WireGuardProfile{}, id)
	if err != nil {
		t.Fatalf("normalizeWireGuardProfileInput() error = %v", err)
	}
	profile.AgentID = agentID
	return wireGuardProfileToRow(profile)
}

func assertWireGuardProfileIDs(t *testing.T, rows []storage.WireGuardProfileRow, want ...int) {
	t.Helper()
	if len(rows) != len(want) {
		t.Fatalf("wireguard profile rows = %+v, want IDs %v", rows, want)
	}
	for i, id := range want {
		if rows[i].ID != id {
			t.Fatalf("wireguard profile rows = %+v, want IDs %v", rows, want)
		}
	}
}
