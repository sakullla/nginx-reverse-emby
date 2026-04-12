package service

import (
	"context"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

type fakeStore struct {
	agents              []storage.AgentRow
	rulesByID           map[string][]storage.HTTPRuleRow
	l4RulesByID         map[string][]storage.L4RuleRow
	relayByID           map[string][]storage.RelayListenerRow
	managedCerts        []storage.ManagedCertificateRow
	localState          storage.LocalAgentStateRow
	savedAgent          storage.AgentRow
	savedAgentCalls     int
	deletedAgentID      string
	snapshot            storage.Snapshot
	localSnapshot       storage.Snapshot
	loadSnapshotCalls   int
	lastSnapshotAgentID string
	lastSnapshotInput   storage.AgentSnapshotInput
	savedRuntimeState   storage.RuntimeState
	savedRuntimeAgentID string
	saveRuntimeCalls    int
}

func (f *fakeStore) ListAgents(context.Context) ([]storage.AgentRow, error) {
	return append([]storage.AgentRow(nil), f.agents...), nil
}

func (f *fakeStore) ListHTTPRules(_ context.Context, agentID string) ([]storage.HTTPRuleRow, error) {
	return append([]storage.HTTPRuleRow(nil), f.rulesByID[agentID]...), nil
}

func (f *fakeStore) LoadLocalAgentState(context.Context) (storage.LocalAgentStateRow, error) {
	return f.localState, nil
}

func (f *fakeStore) SaveAgent(_ context.Context, row storage.AgentRow) error {
	f.savedAgent = row
	f.savedAgentCalls++
	for i := range f.agents {
		if f.agents[i].ID == row.ID {
			f.agents[i] = row
			return nil
		}
	}
	f.agents = append(f.agents, row)
	return nil
}

func (f *fakeStore) ListL4Rules(_ context.Context, agentID string) ([]storage.L4RuleRow, error) {
	return append([]storage.L4RuleRow(nil), f.l4RulesByID[agentID]...), nil
}

func (f *fakeStore) ListVersionPolicies(context.Context) ([]storage.VersionPolicyRow, error) {
	return nil, nil
}

func (f *fakeStore) SaveL4Rules(_ context.Context, agentID string, rows []storage.L4RuleRow) error {
	if f.l4RulesByID == nil {
		f.l4RulesByID = map[string][]storage.L4RuleRow{}
	}
	f.l4RulesByID[agentID] = append([]storage.L4RuleRow(nil), rows...)
	return nil
}

func (f *fakeStore) SaveVersionPolicies(context.Context, []storage.VersionPolicyRow) error {
	return nil
}

func (f *fakeStore) ListRelayListeners(_ context.Context, agentID string) ([]storage.RelayListenerRow, error) {
	return append([]storage.RelayListenerRow(nil), f.relayByID[agentID]...), nil
}

func (f *fakeStore) ListManagedCertificates(context.Context) ([]storage.ManagedCertificateRow, error) {
	return append([]storage.ManagedCertificateRow(nil), f.managedCerts...), nil
}

func (f *fakeStore) SaveRelayListeners(_ context.Context, agentID string, rows []storage.RelayListenerRow) error {
	if f.relayByID == nil {
		f.relayByID = map[string][]storage.RelayListenerRow{}
	}
	f.relayByID[agentID] = append([]storage.RelayListenerRow(nil), rows...)
	return nil
}

func (f *fakeStore) SaveManagedCertificates(_ context.Context, rows []storage.ManagedCertificateRow) error {
	f.managedCerts = append([]storage.ManagedCertificateRow(nil), rows...)
	return nil
}

func (f *fakeStore) LoadManagedCertificateMaterial(context.Context, string) (storage.ManagedCertificateBundle, bool, error) {
	return storage.ManagedCertificateBundle{}, false, nil
}

func (f *fakeStore) SaveManagedCertificateMaterial(context.Context, string, storage.ManagedCertificateBundle) error {
	return nil
}

func (f *fakeStore) CleanupManagedCertificateMaterial(context.Context, []storage.ManagedCertificateRow, []storage.ManagedCertificateRow) error {
	return nil
}

func (f *fakeStore) LoadAgentSnapshot(_ context.Context, agentID string, input storage.AgentSnapshotInput) (storage.Snapshot, error) {
	f.loadSnapshotCalls++
	f.lastSnapshotAgentID = agentID
	f.lastSnapshotInput = input
	return f.snapshot, nil
}

func (f *fakeStore) LoadLocalSnapshot(context.Context, string) (storage.Snapshot, error) {
	return f.localSnapshot, nil
}

func (f *fakeStore) SaveHTTPRules(_ context.Context, agentID string, rows []storage.HTTPRuleRow) error {
	if f.rulesByID == nil {
		f.rulesByID = map[string][]storage.HTTPRuleRow{}
	}
	f.rulesByID[agentID] = append([]storage.HTTPRuleRow(nil), rows...)
	return nil
}

func (f *fakeStore) SaveLocalRuntimeState(_ context.Context, agentID string, state storage.RuntimeState) error {
	f.savedRuntimeAgentID = agentID
	f.savedRuntimeState = state
	f.saveRuntimeCalls++
	return nil
}

func (f *fakeStore) DeleteAgent(_ context.Context, agentID string) error {
	f.deletedAgentID = agentID
	next := make([]storage.AgentRow, 0, len(f.agents))
	for _, row := range f.agents {
		if row.ID != agentID {
			next = append(next, row)
		}
	}
	f.agents = next
	return nil
}

func TestAgentServiceListSynthesizesLocalAgentAndRemoteStatus(t *testing.T) {
	cfg := config.Config{
		EnableLocalAgent:  true,
		LocalAgentID:      "local",
		LocalAgentName:    "Local Agent",
		HeartbeatInterval: 30 * time.Second,
	}

	now := time.Date(2026, time.April, 10, 22, 0, 0, 0, time.UTC)
	svc := NewAgentService(cfg, &fakeStore{
		agents: []storage.AgentRow{{
			ID:                "edge-1",
			Name:              "Edge 1",
			AgentURL:          "http://edge-1:8080",
			Version:           "1.2.3",
			Platform:          "linux-amd64",
			DesiredVersion:    "1.2.3",
			TagsJSON:          `["edge"]`,
			CapabilitiesJSON:  `["http_rules"]`,
			Mode:              "pull",
			DesiredRevision:   4,
			CurrentRevision:   3,
			LastApplyRevision: 3,
			LastApplyStatus:   "success",
			LastApplyMessage:  "",
			LastSeenAt:        now.Add(-15 * time.Second).Format(time.RFC3339),
			LastSeenIP:        "10.0.0.5",
		}},
		rulesByID: map[string][]storage.HTTPRuleRow{
			"local":  {{ID: 1}},
			"edge-1": {{ID: 1}, {ID: 2}},
		},
		localState: storage.LocalAgentStateRow{
			DesiredRevision:   7,
			CurrentRevision:   7,
			LastApplyRevision: 7,
			LastApplyStatus:   "success",
			LastApplyMessage:  "",
			DesiredVersion:    "1.2.3",
		},
	})
	svc.now = func() time.Time { return now }

	agents, err := svc.List(context.Background())
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(agents) != 2 {
		t.Fatalf("len(agents) = %d", len(agents))
	}

	if agents[0].ID != "local" || !agents[0].IsLocal || agents[0].Status != "online" {
		t.Fatalf("local agent = %+v", agents[0])
	}
	if agents[0].Mode != "local" {
		t.Fatalf("local Mode = %q", agents[0].Mode)
	}
	if agents[0].HTTPRulesCount != 1 {
		t.Fatalf("local HTTPRulesCount = %d", agents[0].HTTPRulesCount)
	}

	if agents[1].ID != "edge-1" || agents[1].Status != "online" {
		t.Fatalf("remote agent = %+v", agents[1])
	}
	if agents[1].HTTPRulesCount != 2 || agents[1].LastSeenIP != "10.0.0.5" {
		t.Fatalf("remote agent counts/ip = %+v", agents[1])
	}
}

func TestAgentServiceRegisterNormalizesURLAndDeduplicatesByURL(t *testing.T) {
	store := &fakeStore{
		agents: []storage.AgentRow{{
			ID:               "edge-existing",
			Name:             "Edge Existing",
			AgentURL:         "https://edge.example.com",
			AgentToken:       "token-old",
			CapabilitiesJSON: `["http_rules"]`,
			TagsJSON:         `["old"]`,
			Mode:             "master",
		}},
	}
	svc := NewAgentService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	agent, err := svc.Register(context.Background(), RegisterRequest{
		Name:         "Edge New",
		AgentURL:     " https://edge.example.com/ ",
		AgentToken:   "token-new",
		Tags:         []string{" edge ", "edge", "", "blue"},
		Capabilities: []string{"http_rules", "l4", "bad", "l4"},
	}, "")
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	if agent.ID != "edge-existing" {
		t.Fatalf("Register() reused wrong row: %+v", agent)
	}
	if store.savedAgent.AgentURL != "https://edge.example.com" {
		t.Fatalf("saved AgentURL = %q", store.savedAgent.AgentURL)
	}
	if store.savedAgent.Mode != "master" {
		t.Fatalf("saved Mode = %q", store.savedAgent.Mode)
	}
	if store.savedAgent.TagsJSON != `["edge","blue"]` {
		t.Fatalf("saved TagsJSON = %q", store.savedAgent.TagsJSON)
	}
	if store.savedAgent.CapabilitiesJSON != `["http_rules","l4"]` {
		t.Fatalf("saved CapabilitiesJSON = %q", store.savedAgent.CapabilitiesJSON)
	}
}

func TestAgentServiceRegisterReusesPullAgentByNameWhenURLIsEmpty(t *testing.T) {
	store := &fakeStore{
		agents: []storage.AgentRow{{
			ID:         "pull-existing",
			Name:       "Pull Node",
			AgentURL:   "",
			AgentToken: "token-old",
		}},
	}
	svc := NewAgentService(config.Config{}, store)

	agent, err := svc.Register(context.Background(), RegisterRequest{
		Name:       "Pull Node",
		AgentURL:   "",
		AgentToken: "token-new",
	}, "")
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	if agent.ID != "pull-existing" {
		t.Fatalf("Register() did not reuse pull agent by name: got %+v", agent)
	}
	if store.savedAgent.AgentToken != "token-new" {
		t.Fatalf("expected token updated to token-new, got %q", store.savedAgent.AgentToken)
	}
}

func TestAgentServiceRegisterReusesPullAgentByNameAndResetsRuntimeState(t *testing.T) {
	store := &fakeStore{
		agents: []storage.AgentRow{{
			ID:                     "pull-existing",
			Name:                   "Pull Node",
			AgentURL:               "",
			AgentToken:             "token-old",
			Version:                "old-version",
			Platform:               "linux-amd64",
			RuntimePackageVersion:  "old-runtime",
			RuntimePackagePlatform: "linux",
			RuntimePackageArch:     "amd64",
			RuntimePackageSHA256:   "old-sha",
			DesiredVersion:         "2.0.0",
			DesiredRevision:        3,
			CurrentRevision:        7,
			LastApplyRevision:      7,
			LastApplyStatus:        "error",
			LastApplyMessage:       "heartbeat failed: 503 Service Unavailable",
			LastReportedStatsJSON:  `{"status":"old"}`,
			LastSeenAt:             "2026-04-12T16:05:46Z",
			LastSeenIP:             "142.248.151.126",
		}},
	}
	svc := NewAgentService(config.Config{}, store)

	agent, err := svc.Register(context.Background(), RegisterRequest{
		Name:       "Pull Node",
		AgentURL:   "",
		AgentToken: "token-new",
		Version:    "1",
		Platform:   "linux-amd64",
	}, "")
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	if agent.ID != "pull-existing" {
		t.Fatalf("Register() did not reuse pull agent by name: got %+v", agent)
	}
	if store.savedAgent.DesiredRevision != 0 || store.savedAgent.CurrentRevision != 0 || store.savedAgent.LastApplyRevision != 0 {
		t.Fatalf("expected revision state reset, got desired=%d current=%d last_apply=%d", store.savedAgent.DesiredRevision, store.savedAgent.CurrentRevision, store.savedAgent.LastApplyRevision)
	}
	if store.savedAgent.LastApplyStatus != "success" || store.savedAgent.LastApplyMessage != "" {
		t.Fatalf("expected apply state reset, got status=%q message=%q", store.savedAgent.LastApplyStatus, store.savedAgent.LastApplyMessage)
	}
	if store.savedAgent.RuntimePackageVersion != "" || store.savedAgent.RuntimePackagePlatform != "" || store.savedAgent.RuntimePackageArch != "" || store.savedAgent.RuntimePackageSHA256 != "" {
		t.Fatalf("expected runtime package state reset, got %+v", store.savedAgent)
	}
	if store.savedAgent.LastReportedStatsJSON != "" || store.savedAgent.LastSeenAt != "" || store.savedAgent.LastSeenIP != "" {
		t.Fatalf("expected liveness state reset, got stats=%q last_seen_at=%q last_seen_ip=%q", store.savedAgent.LastReportedStatsJSON, store.savedAgent.LastSeenAt, store.savedAgent.LastSeenIP)
	}
}

func TestAgentServiceRegisterDoesNotReusePushAgentByNameAlone(t *testing.T) {
	store := &fakeStore{
		agents: []storage.AgentRow{{
			ID:         "push-existing",
			Name:       "Push Node",
			AgentURL:   "https://push.example.com",
			AgentToken: "token-old",
		}},
	}
	svc := NewAgentService(config.Config{}, store)

	agent, err := svc.Register(context.Background(), RegisterRequest{
		Name:       "Push Node",
		AgentURL:   "https://push-new.example.com",
		AgentToken: "token-new",
	}, "")
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	if agent.ID == "push-existing" {
		t.Fatalf("Register() reused push agent by name alone: got %+v", agent)
	}
	if len(store.agents) != 2 {
		t.Fatalf("expected 2 agents, got %d", len(store.agents))
	}
}

func TestAgentServiceRegisterRejectsInvalidURL(t *testing.T) {
	svc := NewAgentService(config.Config{}, &fakeStore{})
	for _, invalidURL := range []string{
		"ftp://bad.example.com",
		"http:example.com",
		"http://",
	} {
		_, err := svc.Register(context.Background(), RegisterRequest{
			Name:       "Bad URL",
			AgentURL:   invalidURL,
			AgentToken: "token-bad",
		}, "")
		if err == nil || err.Error() != "agent_url must be a valid http/https URL" {
			t.Fatalf("Register(%q) error = %v", invalidURL, err)
		}
	}
}

func TestAgentServiceRegisterCapabilitiesDefaultingByPresence(t *testing.T) {
	storeOmitted := &fakeStore{}
	svcOmitted := NewAgentService(config.Config{}, storeOmitted)
	_, err := svcOmitted.Register(context.Background(), RegisterRequest{
		Name:       "edge-omitted",
		AgentURL:   "https://edge-omitted.example.com",
		AgentToken: "token-omitted",
	}, "")
	if err != nil {
		t.Fatalf("Register() omitted capabilities error = %v", err)
	}
	if storeOmitted.savedAgent.CapabilitiesJSON != `["http_rules"]` {
		t.Fatalf("omitted capabilities saved as %q", storeOmitted.savedAgent.CapabilitiesJSON)
	}

	storeExplicitEmpty := &fakeStore{}
	svcExplicitEmpty := NewAgentService(config.Config{}, storeExplicitEmpty)
	_, err = svcExplicitEmpty.Register(context.Background(), RegisterRequest{
		Name:            "edge-empty",
		AgentURL:        "https://edge-empty.example.com",
		AgentToken:      "token-empty",
		Capabilities:    []string{},
		HasCapabilities: true,
	}, "")
	if err != nil {
		t.Fatalf("Register() empty capabilities error = %v", err)
	}
	if storeExplicitEmpty.savedAgent.CapabilitiesJSON != `[]` {
		t.Fatalf("empty capabilities saved as %q", storeExplicitEmpty.savedAgent.CapabilitiesJSON)
	}
}

func TestAgentServiceListHTTPRulesNormalizesStoredFields(t *testing.T) {
	svc := NewAgentService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, &fakeStore{
		rulesByID: map[string][]storage.HTTPRuleRow{
			"local": {{
				ID:                1,
				AgentID:           "local",
				FrontendURL:       "https://emby.example.com",
				BackendURL:        "http://emby:8096",
				BackendsJSON:      `[]`,
				LoadBalancingJSON: `{}`,
				Enabled:           true,
				TagsJSON:          `["media"]`,
				ProxyRedirect:     true,
				RelayChainJSON:    `[1,2]`,
				PassProxyHeaders:  true,
				UserAgent:         "",
				CustomHeadersJSON: `[{"name":"X-Test","value":"1"}]`,
				Revision:          9,
			}},
		},
	})

	rules, err := svc.ListHTTPRules(context.Background(), "local")
	if err != nil {
		t.Fatalf("ListHTTPRules() error = %v", err)
	}
	if len(rules) != 1 {
		t.Fatalf("len(rules) = %d", len(rules))
	}

	rule := rules[0]
	if len(rule.Backends) != 1 || rule.Backends[0].URL != "http://emby:8096" {
		t.Fatalf("Backends = %+v", rule.Backends)
	}
	if rule.LoadBalancing.Strategy != "round_robin" {
		t.Fatalf("LoadBalancing = %+v", rule.LoadBalancing)
	}
	if len(rule.CustomHeaders) != 1 || rule.CustomHeaders[0].Name != "X-Test" {
		t.Fatalf("CustomHeaders = %+v", rule.CustomHeaders)
	}
}

func TestAgentServiceHeartbeatReturnsFullSnapshotSyncPayload(t *testing.T) {
	now := time.Date(2026, time.April, 11, 8, 30, 0, 0, time.UTC)
	store := &fakeStore{
		agents: []storage.AgentRow{{
			ID:              "remote-a",
			Name:            "remote-a",
			AgentToken:      "token-remote-a",
			DesiredVersion:  "2.0.0",
			DesiredRevision: 2,
			CurrentRevision: 1,
			LastApplyStatus: "success",
		}},
		snapshot: storage.Snapshot{
			DesiredVersion: "2.0.0",
			Revision:       8,
			VersionPackage: &storage.VersionPackage{
				Platform: "windows-amd64",
				URL:      "https://example.com/agent-windows.zip",
				SHA256:   "sha-windows",
				Filename: "agent-windows.zip",
				Size:     123,
			},
			Rules: []storage.HTTPRule{{
				ID:          9,
				FrontendURL: "https://edge.example.com",
				BackendURL:  "http://127.0.0.1:8096",
				RelayChain:  []int{11, 22},
				Revision:    6,
			}},
			L4Rules: []storage.L4Rule{{
				ID:           2,
				Protocol:     "tcp",
				ListenHost:   "0.0.0.0",
				ListenPort:   9000,
				UpstreamHost: "127.0.0.1",
				UpstreamPort: 9001,
				Revision:     6,
			}},
			RelayListeners: []storage.RelayListener{{
				ID:         11,
				AgentID:    "remote-a",
				Name:       "relay-a",
				ListenHost: "0.0.0.0",
				ListenPort: 7443,
				Revision:   4,
			}},
			Certificates: []storage.ManagedCertificateBundle{{
				ID:       21,
				Domain:   "__relay-ca.internal",
				Revision: 7,
				CertPEM:  "CERT",
				KeyPEM:   "KEY",
			}},
			CertificatePolicies: []storage.ManagedCertificatePolicy{{
				ID:              21,
				Domain:          "__relay-ca.internal",
				Enabled:         true,
				Scope:           "domain",
				IssuerMode:      "local_http01",
				Status:          "active",
				Revision:        7,
				Usage:           "relay_ca",
				CertificateType: "internal_ca",
			}},
		},
	}

	svc := NewAgentService(config.Config{}, store)
	svc.now = func() time.Time { return now }

	reply, err := svc.Heartbeat(context.Background(), HeartbeatRequest{
		CurrentRevision:  1,
		Version:          "1.4.0",
		Platform:         "windows-amd64",
		AgentURL:         "http://remote-a:8080",
		HasAgentURL:      true,
		Tags:             []string{"edge"},
		HasTags:          true,
		Capabilities:     []string{"http_rules", "l4"},
		HasCapabilities:  true,
		LastApplyStatus:  "success",
		LastApplyMessage: "",
	}, "token-remote-a")
	if err != nil {
		t.Fatalf("Heartbeat() error = %v", err)
	}

	if !reply.HasUpdate {
		t.Fatalf("HasUpdate = false, want true")
	}
	if reply.DesiredRevision != 8 {
		t.Fatalf("DesiredRevision = %d", reply.DesiredRevision)
	}
	if reply.DesiredVersion != "2.0.0" {
		t.Fatalf("DesiredVersion = %q", reply.DesiredVersion)
	}
	if reply.VersionPackage != "https://example.com/agent-windows.zip" || reply.VersionSHA256 != "sha-windows" {
		t.Fatalf("version package fields = %q / %q", reply.VersionPackage, reply.VersionSHA256)
	}
	if reply.VersionPackageMeta == nil || reply.VersionPackageMeta.Platform != "windows-amd64" {
		t.Fatalf("VersionPackageMeta = %+v", reply.VersionPackageMeta)
	}
	if len(reply.Rules) != 1 || len(reply.L4Rules) != 1 || len(reply.RelayListeners) != 1 {
		t.Fatalf("sync arrays = %+v", reply)
	}
	if len(reply.Certificates) != 1 || len(reply.CertificatePolicies) != 1 {
		t.Fatalf("cert sync arrays = %+v", reply)
	}
	if store.loadSnapshotCalls != 1 || store.lastSnapshotAgentID != "remote-a" {
		t.Fatalf("LoadAgentSnapshot() calls = %d, agent_id = %q", store.loadSnapshotCalls, store.lastSnapshotAgentID)
	}
	if store.lastSnapshotInput.Platform != "windows-amd64" || store.lastSnapshotInput.DesiredVersion != "2.0.0" {
		t.Fatalf("snapshot input = %+v", store.lastSnapshotInput)
	}
	if store.savedAgentCalls != 1 {
		t.Fatalf("SaveAgent() calls = %d", store.savedAgentCalls)
	}
	if store.savedAgent.Version != "1.4.0" || store.savedAgent.Platform != "windows-amd64" || store.savedAgent.CurrentRevision != 1 {
		t.Fatalf("saved agent metadata = %+v", store.savedAgent)
	}
	if store.savedAgent.LastSeenAt != now.Format(time.RFC3339) {
		t.Fatalf("LastSeenAt = %q", store.savedAgent.LastSeenAt)
	}
}

func TestAgentServiceHeartbeatOmitsSyncPayloadWhenUpToDateButKeepsRelayListeners(t *testing.T) {
	store := &fakeStore{
		agents: []storage.AgentRow{{
			ID:              "remote-b",
			Name:            "remote-b",
			AgentToken:      "token-remote-b",
			DesiredVersion:  "3.0.0",
			DesiredRevision: 1,
			CurrentRevision: 1,
			LastApplyStatus: "success",
		}},
		snapshot: storage.Snapshot{
			DesiredVersion: "3.0.0",
			Revision:       7,
			VersionPackage: &storage.VersionPackage{
				Platform: "linux-amd64",
				URL:      "https://example.com/agent-linux.tar.gz",
				SHA256:   "sha-linux",
			},
			Rules:          []storage.HTTPRule{{ID: 1, FrontendURL: "https://a.example.com", BackendURL: "http://127.0.0.1:8096"}},
			L4Rules:        []storage.L4Rule{{ID: 2, Protocol: "tcp", ListenHost: "0.0.0.0", ListenPort: 9000, UpstreamHost: "127.0.0.1", UpstreamPort: 9001}},
			RelayListeners: []storage.RelayListener{{ID: 11, AgentID: "remote-b", Name: "relay-b", ListenHost: "0.0.0.0", ListenPort: 7443}},
			Certificates:   []storage.ManagedCertificateBundle{{ID: 31, Domain: "relay.example.com", CertPEM: "CERT", KeyPEM: "KEY"}},
			CertificatePolicies: []storage.ManagedCertificatePolicy{{
				ID:              31,
				Domain:          "relay.example.com",
				Enabled:         true,
				Scope:           "domain",
				IssuerMode:      "local_http01",
				Status:          "active",
				Usage:           "relay_tunnel",
				CertificateType: "uploaded",
			}},
		},
	}
	svc := NewAgentService(config.Config{}, store)

	reply, err := svc.Heartbeat(context.Background(), HeartbeatRequest{
		CurrentRevision: 7,
		Platform:        "linux-amd64",
	}, "token-remote-b")
	if err != nil {
		t.Fatalf("Heartbeat() error = %v", err)
	}

	if reply.HasUpdate {
		t.Fatalf("HasUpdate = true, want false")
	}
	if reply.DesiredRevision != 7 || reply.DesiredVersion != "3.0.0" {
		t.Fatalf("desired sync fields = %+v", reply)
	}
	if reply.Rules != nil || reply.L4Rules != nil || reply.Certificates != nil || reply.CertificatePolicies != nil {
		t.Fatalf("expected non-relay sync payloads omitted when up-to-date: %+v", reply)
	}
	if len(reply.RelayListeners) != 1 || reply.RelayListeners[0].ID != 11 {
		t.Fatalf("expected relay listeners to remain populated when up-to-date: %+v", reply.RelayListeners)
	}
	if reply.VersionPackage != "https://example.com/agent-linux.tar.gz" || reply.VersionSHA256 != "sha-linux" {
		t.Fatalf("version package fields = %q / %q", reply.VersionPackage, reply.VersionSHA256)
	}
	if store.lastSnapshotInput.CurrentRevision != 7 || store.lastSnapshotInput.DesiredRevision != 1 {
		t.Fatalf("snapshot input revision state = %+v", store.lastSnapshotInput)
	}
}

func TestAgentServiceHeartbeatAppliesManagedCertificateReports(t *testing.T) {
	store := &fakeStore{
		agents: []storage.AgentRow{{
			ID:               "remote-cert",
			Name:             "remote-cert",
			AgentToken:       "token-remote-cert",
			CapabilitiesJSON: `["cert_install","local_acme"]`,
			DesiredVersion:   "3.0.0",
			DesiredRevision:  4,
			CurrentRevision:  3,
			LastApplyStatus:  "success",
		}},
		managedCerts: []storage.ManagedCertificateRow{{
			ID:              21,
			Domain:          "sync.example.com",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "local_http01",
			TargetAgentIDs:  `["remote-cert"]`,
			Status:          "pending",
			AgentReports:    `{}`,
			ACMEInfo:        `{"Main_Domain":"sync.example.com"}`,
			Usage:           "https",
			CertificateType: "acme",
			Revision:        4,
		}},
		snapshot: storage.Snapshot{DesiredVersion: "3.0.0", Revision: 4},
	}
	svc := NewAgentService(config.Config{}, store)
	now := time.Date(2026, time.April, 11, 12, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { return now }

	_, err := svc.Heartbeat(context.Background(), HeartbeatRequest{
		CurrentRevision:   3,
		LastApplyRevision: 4,
		LastApplyStatus:   "success",
		ManagedCertificateReports: []ManagedCertificateHeartbeatReport{{
			ID:           21,
			Domain:       "SYNC.EXAMPLE.COM",
			Status:       "active",
			LastIssueAt:  "2026-04-11T12:00:00Z",
			LastError:    "",
			MaterialHash: "hash-21",
			ACMEInfo:     ManagedCertificateACMEInfo{MainDomain: "sync.example.com"},
		}},
	}, "token-remote-cert")
	if err != nil {
		t.Fatalf("Heartbeat() error = %v", err)
	}

	if len(store.managedCerts) != 1 {
		t.Fatalf("managed cert rows = %+v", store.managedCerts)
	}
	cert := managedCertificateFromRow(store.managedCerts[0])
	if cert.Status != "active" || cert.MaterialHash != "hash-21" {
		t.Fatalf("unexpected updated cert = %+v", cert)
	}
	report, ok := cert.AgentReports["remote-cert"]
	if !ok {
		t.Fatalf("missing agent report in %+v", cert.AgentReports)
	}
	if report.Status != "active" || report.MaterialHash != "hash-21" {
		t.Fatalf("unexpected agent report = %+v", report)
	}
}

func TestAgentServiceHeartbeatReconcilesLocalHTTP01FromApplyStatus(t *testing.T) {
	store := &fakeStore{
		agents: []storage.AgentRow{{
			ID:               "remote-cert",
			Name:             "remote-cert",
			AgentToken:       "token-remote-cert",
			CapabilitiesJSON: `["cert_install","local_acme"]`,
			DesiredVersion:   "3.0.0",
			DesiredRevision:  4,
			CurrentRevision:  3,
			LastApplyStatus:  "success",
		}},
		rulesByID: map[string][]storage.HTTPRuleRow{
			"remote-cert": {{
				ID:          9,
				AgentID:     "remote-cert",
				FrontendURL: "https://sync.example.com",
				Enabled:     true,
				Revision:    4,
			}},
		},
		managedCerts: []storage.ManagedCertificateRow{{
			ID:              21,
			Domain:          "sync.example.com",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "local_http01",
			TargetAgentIDs:  `["remote-cert"]`,
			Status:          "pending",
			MaterialHash:    "hash-21",
			AgentReports:    `{}`,
			ACMEInfo:        `{"Main_Domain":"sync.example.com"}`,
			Usage:           "https",
			CertificateType: "acme",
			Revision:        4,
		}},
		snapshot: storage.Snapshot{DesiredVersion: "3.0.0", Revision: 4},
	}
	svc := NewAgentService(config.Config{}, store)
	now := time.Date(2026, time.April, 11, 12, 30, 0, 0, time.UTC)
	svc.now = func() time.Time { return now }

	_, err := svc.Heartbeat(context.Background(), HeartbeatRequest{
		CurrentRevision:   3,
		LastApplyRevision: 4,
		LastApplyStatus:   "success",
	}, "token-remote-cert")
	if err != nil {
		t.Fatalf("Heartbeat() error = %v", err)
	}

	cert := managedCertificateFromRow(store.managedCerts[0])
	if cert.Status != "active" || cert.LastError != "" {
		t.Fatalf("unexpected reconciled cert = %+v", cert)
	}
	report := cert.AgentReports["remote-cert"]
	if report.Status != "active" || report.LastIssueAt != now.Format(time.RFC3339) {
		t.Fatalf("unexpected reconciled report = %+v", report)
	}
}

func TestAgentServiceHeartbeatSkipsLocalHTTP01ReconcileWithoutRequiredCapabilities(t *testing.T) {
	store := &fakeStore{
		agents: []storage.AgentRow{{
			ID:               "remote-cert",
			Name:             "remote-cert",
			AgentToken:       "token-remote-cert",
			CapabilitiesJSON: `["cert_install"]`,
			DesiredVersion:   "3.0.0",
			DesiredRevision:  4,
			CurrentRevision:  3,
			LastApplyStatus:  "success",
		}},
		rulesByID: map[string][]storage.HTTPRuleRow{
			"remote-cert": {{
				ID:          9,
				AgentID:     "remote-cert",
				FrontendURL: "https://sync.example.com",
				Enabled:     true,
				Revision:    4,
			}},
		},
		managedCerts: []storage.ManagedCertificateRow{{
			ID:              21,
			Domain:          "sync.example.com",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "local_http01",
			TargetAgentIDs:  `["remote-cert"]`,
			Status:          "pending",
			MaterialHash:    "hash-21",
			AgentReports:    `{}`,
			ACMEInfo:        `{"Main_Domain":"sync.example.com"}`,
			Usage:           "https",
			CertificateType: "acme",
			Revision:        4,
		}},
		snapshot: storage.Snapshot{DesiredVersion: "3.0.0", Revision: 4},
	}
	svc := NewAgentService(config.Config{}, store)
	now := time.Date(2026, time.April, 11, 12, 30, 0, 0, time.UTC)
	svc.now = func() time.Time { return now }

	_, err := svc.Heartbeat(context.Background(), HeartbeatRequest{
		CurrentRevision:   3,
		LastApplyRevision: 4,
		LastApplyStatus:   "success",
	}, "token-remote-cert")
	if err != nil {
		t.Fatalf("Heartbeat() error = %v", err)
	}

	cert := managedCertificateFromRow(store.managedCerts[0])
	if cert.Status != "pending" || cert.LastIssueAt != "" || cert.LastError != "" {
		t.Fatalf("unexpected reconciled cert = %+v", cert)
	}
	if len(cert.AgentReports) != 0 {
		t.Fatalf("expected no reconciled agent report, got %+v", cert.AgentReports)
	}
}

func TestAgentServiceHeartbeatPersistsReportedStats(t *testing.T) {
	store := &fakeStore{
		agents: []storage.AgentRow{{
			ID:              "remote-stats",
			Name:            "remote-stats",
			AgentToken:      "token-remote-stats",
			DesiredVersion:  "3.0.0",
			DesiredRevision: 2,
			CurrentRevision: 1,
			LastApplyStatus: "success",
		}},
		snapshot: storage.Snapshot{DesiredVersion: "3.0.0", Revision: 2},
	}
	svc := NewAgentService(config.Config{}, store)

	_, err := svc.Heartbeat(context.Background(), HeartbeatRequest{
		CurrentRevision: 1,
		Stats: AgentStats{
			"totalRequests": "42",
			"status":        "运行中",
		},
	}, "token-remote-stats")
	if err != nil {
		t.Fatalf("Heartbeat() error = %v", err)
	}

	if store.savedAgent.LastReportedStatsJSON == "" {
		t.Fatalf("LastReportedStatsJSON was not persisted")
	}
	stats, err := svc.Stats(context.Background(), "remote-stats")
	if err != nil {
		t.Fatalf("Stats() error = %v", err)
	}
	if stats["totalRequests"] != "42" || stats["status"] != "运行中" {
		t.Fatalf("Stats() = %+v", stats)
	}
}

func TestAgentServiceHeartbeatPersistsRuntimePackageMetadata(t *testing.T) {
	store := &fakeStore{
		agents: []storage.AgentRow{{
			ID:              "edge-1",
			Name:            "edge-1",
			AgentToken:      "agent-token",
			Platform:        "linux-amd64",
			DesiredVersion:  "",
			DesiredRevision: 2,
			CurrentRevision: 1,
			LastApplyStatus: "success",
		}},
		snapshot: storage.Snapshot{
			DesiredVersion: "",
			Revision:       2,
			VersionPackage: &storage.VersionPackage{
				Platform: "linux-amd64",
				URL:      "https://example.com/nre-agent",
				SHA256:   "desired-sha",
			},
		},
	}
	svc := NewAgentService(config.Config{}, store)

	reply, err := svc.Heartbeat(context.Background(), HeartbeatRequest{
		CurrentRevision: 1,
		Version:         "1.2.3",
		Platform:        "linux-amd64",
		RuntimePackage: RuntimePackageInfo{
			Version:  "1.2.3",
			Platform: "linux",
			Arch:     "amd64",
			SHA256:   "runtime-sha",
		},
	}, "agent-token")
	if err != nil {
		t.Fatalf("Heartbeat() error = %v", err)
	}
	if store.savedAgent.RuntimePackageSHA256 != "runtime-sha" {
		t.Fatalf("saved runtime sha = %q", store.savedAgent.RuntimePackageSHA256)
	}
	if store.savedAgent.RuntimePackageArch != "amd64" {
		t.Fatalf("saved runtime arch = %q", store.savedAgent.RuntimePackageArch)
	}
	summary, err := svc.Get(context.Background(), "edge-1")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if summary.RuntimePackageSHA256 != "runtime-sha" {
		t.Fatalf("summary runtime sha = %q", summary.RuntimePackageSHA256)
	}
	if summary.DesiredPackageSHA256 != "desired-sha" {
		t.Fatalf("summary desired sha = %q", summary.DesiredPackageSHA256)
	}
	if summary.PackageSyncStatus != "pending" {
		t.Fatalf("summary package status = %q", summary.PackageSyncStatus)
	}
	if reply.VersionPackageMeta == nil || reply.VersionPackageMeta.SHA256 != "desired-sha" {
		t.Fatalf("reply VersionPackageMeta = %+v", reply.VersionPackageMeta)
	}
}

func TestAgentServiceHeartbeatNormalizesURLAndIP(t *testing.T) {
	store := &fakeStore{
		agents: []storage.AgentRow{{
			ID:               "remote-heartbeat",
			Name:             "remote-heartbeat",
			AgentToken:       "token-remote-heartbeat",
			DesiredVersion:   "3.0.0",
			DesiredRevision:  2,
			CurrentRevision:  1,
			LastApplyStatus:  "success",
			CapabilitiesJSON: `["http_rules"]`,
			TagsJSON:         `["old"]`,
			LastSeenIP:       "",
			Mode:             "pull",
		}},
		snapshot: storage.Snapshot{DesiredVersion: "3.0.0", Revision: 2},
	}
	svc := NewAgentService(config.Config{}, store)

	_, err := svc.Heartbeat(context.Background(), HeartbeatRequest{
		CurrentRevision: 1,
		AgentURL:        " https://edge-heartbeat.example.com/ ",
		HasAgentURL:     true,
		Tags:            []string{" edge ", "", "edge"},
		HasTags:         true,
		Capabilities:    []string{"http_rules", "l4", "bad"},
		HasCapabilities: true,
		LastSeenIP:      "203.0.113.9",
	}, "token-remote-heartbeat")
	if err != nil {
		t.Fatalf("Heartbeat() error = %v", err)
	}

	if store.savedAgent.AgentURL != "https://edge-heartbeat.example.com" {
		t.Fatalf("saved AgentURL = %q", store.savedAgent.AgentURL)
	}
	if store.savedAgent.Mode != "master" {
		t.Fatalf("saved Mode = %q", store.savedAgent.Mode)
	}
	if store.savedAgent.LastSeenIP != "203.0.113.9" {
		t.Fatalf("saved LastSeenIP = %q", store.savedAgent.LastSeenIP)
	}
	if store.savedAgent.TagsJSON != `["edge"]` {
		t.Fatalf("saved TagsJSON = %q", store.savedAgent.TagsJSON)
	}
	if store.savedAgent.CapabilitiesJSON != `["http_rules","l4"]` {
		t.Fatalf("saved CapabilitiesJSON = %q", store.savedAgent.CapabilitiesJSON)
	}
}

func TestAgentServiceHeartbeatRejectsInvalidURL(t *testing.T) {
	store := &fakeStore{
		agents: []storage.AgentRow{{
			ID:              "remote-invalid-url",
			Name:            "remote-invalid-url",
			AgentToken:      "token-remote-invalid-url",
			DesiredVersion:  "3.0.0",
			DesiredRevision: 2,
			CurrentRevision: 1,
			LastApplyStatus: "success",
		}},
	}
	svc := NewAgentService(config.Config{}, store)

	for _, invalidURL := range []string{
		"ftp://bad.example.com",
		"http:example.com",
		"http://",
	} {
		_, err := svc.Heartbeat(context.Background(), HeartbeatRequest{
			CurrentRevision: 1,
			AgentURL:        invalidURL,
			HasAgentURL:     true,
		}, "token-remote-invalid-url")
		if err == nil || err.Error() != "invalid argument: agent_url must be a valid http/https URL" {
			t.Fatalf("Heartbeat(%q) error = %v", invalidURL, err)
		}
	}
}

func TestAgentServiceHeartbeatClearsAgentURLAndListFieldsWhenPresentEmpty(t *testing.T) {
	store := &fakeStore{
		agents: []storage.AgentRow{{
			ID:               "remote-clear",
			Name:             "remote-clear",
			AgentToken:       "token-remote-clear",
			DesiredVersion:   "3.0.0",
			DesiredRevision:  2,
			CurrentRevision:  1,
			LastApplyStatus:  "success",
			AgentURL:         "https://edge-clear.example.com",
			Mode:             "master",
			TagsJSON:         `["edge"]`,
			CapabilitiesJSON: `["http_rules"]`,
		}},
		snapshot: storage.Snapshot{DesiredVersion: "3.0.0", Revision: 2},
	}
	svc := NewAgentService(config.Config{}, store)

	_, err := svc.Heartbeat(context.Background(), HeartbeatRequest{
		CurrentRevision: 1,
		AgentURL:        "   ",
		HasAgentURL:     true,
		Tags:            []string{},
		HasTags:         true,
		Capabilities:    []string{},
		HasCapabilities: true,
	}, "token-remote-clear")
	if err != nil {
		t.Fatalf("Heartbeat() error = %v", err)
	}

	if store.savedAgent.AgentURL != "" {
		t.Fatalf("saved AgentURL = %q", store.savedAgent.AgentURL)
	}
	if store.savedAgent.Mode != "pull" {
		t.Fatalf("saved Mode = %q", store.savedAgent.Mode)
	}
	if store.savedAgent.TagsJSON != `[]` {
		t.Fatalf("saved TagsJSON = %q", store.savedAgent.TagsJSON)
	}
	if store.savedAgent.CapabilitiesJSON != `[]` {
		t.Fatalf("saved CapabilitiesJSON = %q", store.savedAgent.CapabilitiesJSON)
	}
}

func TestAgentServiceUpdateRemoteAgentNormalizesFields(t *testing.T) {
	store := &fakeStore{
		agents: []storage.AgentRow{{
			ID:               "edge-1",
			Name:             "Edge 1",
			AgentToken:       "token-old",
			Mode:             "pull",
			CapabilitiesJSON: `["http_rules"]`,
			TagsJSON:         `["old"]`,
		}},
	}
	svc := NewAgentService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	name := "  Edge Renamed  "
	agentURL := " https://edge.example.com/ "
	agentToken := " token-new "
	version := " 1.2.3 "
	tags := []string{" edge ", "edge", "", "blue"}
	capabilities := []string{"http_rules", "l4", "nope", "l4"}
	agent, err := svc.Update(context.Background(), "edge-1", UpdateAgentRequest{
		Name:         &name,
		AgentURL:     &agentURL,
		AgentToken:   &agentToken,
		Version:      &version,
		Tags:         &tags,
		Capabilities: &capabilities,
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	if agent.Name != "Edge Renamed" {
		t.Fatalf("agent.Name = %q", agent.Name)
	}
	if store.savedAgent.AgentURL != "https://edge.example.com" {
		t.Fatalf("saved AgentURL = %q", store.savedAgent.AgentURL)
	}
	if store.savedAgent.AgentToken != "token-new" {
		t.Fatalf("saved AgentToken = %q", store.savedAgent.AgentToken)
	}
	if store.savedAgent.Mode != "master" {
		t.Fatalf("saved Mode = %q", store.savedAgent.Mode)
	}
	if store.savedAgent.TagsJSON != `["edge","blue"]` {
		t.Fatalf("saved TagsJSON = %q", store.savedAgent.TagsJSON)
	}
	if store.savedAgent.CapabilitiesJSON != `["http_rules","l4"]` {
		t.Fatalf("saved CapabilitiesJSON = %q", store.savedAgent.CapabilitiesJSON)
	}
}

func TestAgentServiceDeleteRejectsReferencedRelayListenerAndCleansUpRemoteAgent(t *testing.T) {
	cfg := config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}
	store := &fakeStore{
		agents: []storage.AgentRow{
			{ID: "edge-a", Name: "edge-a", AgentToken: "token-a"},
			{ID: "edge-b", Name: "edge-b", AgentToken: "token-b"},
		},
		relayByID: map[string][]storage.RelayListenerRow{
			"edge-a": {{
				ID:      7,
				AgentID: "edge-a",
				Name:    "relay-a",
			}},
		},
		rulesByID: map[string][]storage.HTTPRuleRow{
			"edge-a": {{ID: 1, AgentID: "edge-a"}},
			"edge-b": {{
				ID:             9,
				AgentID:        "edge-b",
				FrontendURL:    "https://relay.example.com",
				RelayChainJSON: `[7]`,
			}},
		},
		l4RulesByID: map[string][]storage.L4RuleRow{
			"edge-a": {{ID: 2, AgentID: "edge-a"}},
		},
	}
	svc := NewAgentService(cfg, store)

	_, err := svc.Delete(context.Background(), "edge-a")
	if err == nil || err.Error() != "invalid argument: cannot delete agent edge-a: relay listener 7 is referenced by HTTP rule #9 on agent edge-b" {
		t.Fatalf("Delete() error = %v", err)
	}

	delete(store.rulesByID, "edge-b")
	deleted, err := svc.Delete(context.Background(), "edge-a")
	if err != nil {
		t.Fatalf("Delete() second call error = %v", err)
	}

	if deleted.ID != "edge-a" {
		t.Fatalf("deleted agent = %+v", deleted)
	}
	if store.deletedAgentID != "edge-a" {
		t.Fatalf("DeleteAgent() called with %q", store.deletedAgentID)
	}
	if len(store.rulesByID["edge-a"]) != 0 || len(store.l4RulesByID["edge-a"]) != 0 || len(store.relayByID["edge-a"]) != 0 {
		t.Fatalf("agent resources not cleaned up: rules=%+v l4=%+v relay=%+v", store.rulesByID["edge-a"], store.l4RulesByID["edge-a"], store.relayByID["edge-a"])
	}
}

func TestAgentServiceStatsFallbackAndApplyBehavior(t *testing.T) {
	cfg := config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
		LocalAgentName:   "Local",
	}
	store := &fakeStore{
		agents: []storage.AgentRow{{
			ID:                    "edge-1",
			Name:                  "edge-1",
			AgentToken:            "token-edge-1",
			Platform:              "linux-amd64",
			DesiredVersion:        "1.2.3",
			DesiredRevision:       2,
			CurrentRevision:       1,
			LastApplyStatus:       "success",
			LastSeenAt:            time.Now().UTC().Format(time.RFC3339),
			LastReportedStatsJSON: "",
		}},
		localState: storage.LocalAgentStateRow{
			DesiredRevision: 1,
			CurrentRevision: 1,
		},
		snapshot:      storage.Snapshot{DesiredVersion: "1.2.3", Revision: 5},
		localSnapshot: storage.Snapshot{DesiredVersion: "1.2.3", Revision: 4},
	}
	svc := NewAgentService(cfg, store)

	remoteStats, err := svc.Stats(context.Background(), "edge-1")
	if err != nil {
		t.Fatalf("Stats(remote) error = %v", err)
	}
	if remoteStats["totalRequests"] != "0" {
		t.Fatalf("Stats(remote) = %+v", remoteStats)
	}

	localStats, err := svc.Stats(context.Background(), "local")
	if err != nil {
		t.Fatalf("Stats(local) error = %v", err)
	}
	if localStats["status"] != "运行中" {
		t.Fatalf("Stats(local) = %+v", localStats)
	}

	remoteApply, err := svc.Apply(context.Background(), "edge-1")
	if err != nil {
		t.Fatalf("Apply(remote) error = %v", err)
	}
	if remoteApply.Message != "waiting for agent heartbeat to apply" || store.savedAgent.DesiredRevision != 5 {
		t.Fatalf("Apply(remote) = %+v, savedAgent = %+v", remoteApply, store.savedAgent)
	}

	localApply, err := svc.Apply(context.Background(), "local")
	if err != nil {
		t.Fatalf("Apply(local) error = %v", err)
	}
	if localApply.Message != "waiting for embedded local agent to apply" || !localApply.Pending {
		t.Fatalf("Apply(local) = %+v", localApply)
	}
	if store.saveRuntimeCalls != 0 {
		t.Fatalf("Apply(local) should not persist fake runtime state, saveRuntimeCalls = %d", store.saveRuntimeCalls)
	}
}

func TestAgentServiceApplyLocalUsesTriggerForSynchronousEmbeddedApply(t *testing.T) {
	cfg := config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
		LocalAgentName:   "Local",
	}
	store := &fakeStore{
		localState: storage.LocalAgentStateRow{
			DesiredRevision:   1,
			CurrentRevision:   1,
			LastApplyRevision: 1,
			LastApplyStatus:   "success",
		},
		localSnapshot: storage.Snapshot{DesiredVersion: "1.2.3", Revision: 4},
	}
	svc := NewAgentService(cfg, store)

	triggerCalls := 0
	svc.SetLocalApplyTrigger(func(context.Context) error {
		triggerCalls++
		store.localState = storage.LocalAgentStateRow{
			DesiredRevision:   4,
			CurrentRevision:   4,
			LastApplyRevision: 4,
			LastApplyStatus:   "success",
		}
		return nil
	})

	localApply, err := svc.Apply(context.Background(), "local")
	if err != nil {
		t.Fatalf("Apply(local) error = %v", err)
	}
	if localApply.Message != "applied" || localApply.Pending || localApply.DesiredRevision != 4 {
		t.Fatalf("Apply(local) = %+v", localApply)
	}
	if triggerCalls != 1 {
		t.Fatalf("triggerCalls = %d", triggerCalls)
	}
	if store.saveRuntimeCalls != 0 {
		t.Fatalf("Apply(local) should rely on runtime callback, saveRuntimeCalls = %d", store.saveRuntimeCalls)
	}
}

func TestAgentServiceRegisterDoesNotReuseByNameAlone(t *testing.T) {
	store := &fakeStore{
		agents: []storage.AgentRow{{
			ID:         "edge-existing",
			Name:       "Edge Node",
			AgentURL:   "https://edge1.example.com",
			AgentToken: "token-old",
		}},
	}
	svc := NewAgentService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	agent, err := svc.Register(context.Background(), RegisterRequest{
		Name:       "Edge Node",
		AgentURL:   "https://edge2.example.com",
		AgentToken: "token-new",
	}, "")
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	if agent.ID == "edge-existing" {
		t.Fatalf("Register() reused existing agent by name: %+v", agent)
	}
	if len(store.agents) != 2 {
		t.Fatalf("expected 2 agents, got %d", len(store.agents))
	}
}

func TestAgentServiceDeleteCleansUpManagedCertificates(t *testing.T) {
	cfg := config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}
	store := &fakeStore{
		agents: []storage.AgentRow{
			{ID: "edge-a", Name: "edge-a", AgentToken: "token-a"},
			{ID: "edge-b", Name: "edge-b", AgentToken: "token-b"},
		},
		managedCerts: []storage.ManagedCertificateRow{
			{ID: 1, Domain: "shared.example.com", TargetAgentIDs: `["edge-a","edge-b"]`},
			{ID: 2, Domain: "orphan.example.com", TargetAgentIDs: `["edge-a"]`},
		},
	}
	svc := NewAgentService(cfg, store)

	deleted, err := svc.Delete(context.Background(), "edge-a")
	if err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if deleted.ID != "edge-a" {
		t.Fatalf("deleted agent = %+v", deleted)
	}
	if store.deletedAgentID != "edge-a" {
		t.Fatalf("DeleteAgent() called with %q", store.deletedAgentID)
	}

	if len(store.managedCerts) != 1 {
		t.Fatalf("expected 1 remaining cert, got %d", len(store.managedCerts))
	}
	remaining := store.managedCerts[0]
	if remaining.ID != 1 {
		t.Fatalf("expected remaining cert ID 1, got %d", remaining.ID)
	}
	if remaining.TargetAgentIDs != `["edge-b"]` {
		t.Fatalf("expected shared cert to drop edge-a, got %q", remaining.TargetAgentIDs)
	}
}
