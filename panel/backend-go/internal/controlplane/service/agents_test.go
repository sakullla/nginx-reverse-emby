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
	managedCerts        []storage.ManagedCertificateRow
	localState          storage.LocalAgentStateRow
	savedAgent          storage.AgentRow
	savedAgentCalls     int
	snapshot            storage.Snapshot
	loadSnapshotCalls   int
	lastSnapshotAgentID string
	lastSnapshotInput   storage.AgentSnapshotInput
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

func (f *fakeStore) ListL4Rules(context.Context, string) ([]storage.L4RuleRow, error) {
	return nil, nil
}

func (f *fakeStore) ListVersionPolicies(context.Context) ([]storage.VersionPolicyRow, error) {
	return nil, nil
}

func (f *fakeStore) SaveL4Rules(context.Context, string, []storage.L4RuleRow) error {
	return nil
}

func (f *fakeStore) SaveVersionPolicies(context.Context, []storage.VersionPolicyRow) error {
	return nil
}

func (f *fakeStore) ListRelayListeners(context.Context, string) ([]storage.RelayListenerRow, error) {
	return nil, nil
}

func (f *fakeStore) ListManagedCertificates(context.Context) ([]storage.ManagedCertificateRow, error) {
	return append([]storage.ManagedCertificateRow(nil), f.managedCerts...), nil
}

func (f *fakeStore) SaveRelayListeners(context.Context, string, []storage.RelayListenerRow) error {
	return nil
}

func (f *fakeStore) SaveManagedCertificates(_ context.Context, rows []storage.ManagedCertificateRow) error {
	f.managedCerts = append([]storage.ManagedCertificateRow(nil), rows...)
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
		Tags:             []string{"edge"},
		Capabilities:     []string{"http_rules", "l4"},
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
			ID:              "remote-cert",
			Name:            "remote-cert",
			AgentToken:      "token-remote-cert",
			DesiredVersion:  "3.0.0",
			DesiredRevision: 4,
			CurrentRevision: 3,
			LastApplyStatus: "success",
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
			ID:              "remote-cert",
			Name:            "remote-cert",
			AgentToken:      "token-remote-cert",
			DesiredVersion:  "3.0.0",
			DesiredRevision: 4,
			CurrentRevision: 3,
			LastApplyStatus: "success",
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
