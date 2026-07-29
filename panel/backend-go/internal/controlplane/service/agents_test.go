package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/revision"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

type fakeStore struct {
	agents              []storage.AgentRow
	rulesByID           map[string][]storage.HTTPRuleRow
	l4RulesByID         map[string][]storage.L4RuleRow
	relayByID           map[string][]storage.RelayListenerRow
	managedCerts        []storage.ManagedCertificateRow
	events              []storage.AgentTrafficEventRow
	localState          storage.LocalAgentStateRow
	savedAgent          storage.AgentRow
	savedAgentCalls     int
	savedHeartbeatCalls int
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

type fakeHeartbeatTrafficService struct {
	ingestCalls []fakeHeartbeatTrafficIngest
	ingestErr   error
	summary     TrafficSummary
	summaryErr  error
}

type fakeHeartbeatTrafficIngest struct {
	agentID string
	stats   AgentStats
}

func (f *fakeHeartbeatTrafficService) IngestHeartbeat(_ context.Context, agentID string, stats AgentStats) error {
	f.ingestCalls = append(f.ingestCalls, fakeHeartbeatTrafficIngest{
		agentID: agentID,
		stats:   stats,
	})
	if f.ingestErr != nil {
		return f.ingestErr
	}
	return nil
}

func (f *fakeHeartbeatTrafficService) Summary(context.Context, string) (TrafficSummary, error) {
	if f.summaryErr != nil {
		return TrafficSummary{}, f.summaryErr
	}
	return f.summary, nil
}

func (f *fakeHeartbeatTrafficService) BlockState(context.Context, string) (bool, string, error) {
	if f.summaryErr != nil {
		return false, "", f.summaryErr
	}
	if !f.summary.Blocked {
		return false, "", nil
	}
	return true, f.summary.BlockReason, nil
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

func (f *fakeStore) LoadLocalRuntimeState(context.Context) (storage.RuntimeState, error) {
	return f.savedRuntimeState, nil
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

func (f *fakeStore) SaveAgentHeartbeat(_ context.Context, row storage.AgentRow) error {
	f.savedAgent = row
	f.savedHeartbeatCalls++
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

func (f *fakeStore) SaveTrafficEvent(_ context.Context, row storage.AgentTrafficEventRow) error {
	f.events = append(f.events, row)
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
	t.Parallel()
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
	if len(agents[0].Capabilities) != 8 {
		t.Fatalf("local Capabilities = %+v", agents[0].Capabilities)
	}
	if agents[0].Capabilities[3] != managedCertificateReportsCapability || agents[0].Capabilities[5] != "relay_quic" ||
		agents[0].Capabilities[6] != "egress_profiles" || agents[0].Capabilities[7] != packageManifestCapability {
		t.Fatalf("local Capabilities = %+v", agents[0].Capabilities)
	}

	if agents[1].ID != "edge-1" || agents[1].Status != "online" {
		t.Fatalf("remote agent = %+v", agents[1])
	}
	if agents[1].HTTPRulesCount != 2 || agents[1].LastSeenIP != "10.0.0.5" {
		t.Fatalf("remote agent counts/ip = %+v", agents[1])
	}
}

func TestAgentServiceRegisterNormalizesURLAndDeduplicatesByURL(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
			LastSeenIP:             "203.0.113.126",
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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

func TestAgentServiceUpdatePersistsOutboundProxyURL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := &fakeStore{}
	if err := store.SaveAgent(ctx, storage.AgentRow{
		ID:               "edge-a",
		Name:             "Edge A",
		AgentToken:       "token-a",
		CapabilitiesJSON: `["http_rules","l4","relay"]`,
		LastApplyStatus:  "success",
	}); err != nil {
		t.Fatalf("SaveAgent() error = %v", err)
	}
	svc := NewAgentService(config.Config{}, store)
	input := UpdateAgentRequest{
		OutboundProxyURL: stringPtr("socks://user:pass@127.0.0.1:1080"),
	}
	agent, err := svc.Update(ctx, "edge-a", input)
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if agent.OutboundProxyURL != "socks://user:pass@127.0.0.1:1080" {
		t.Fatalf("OutboundProxyURL = %q", agent.OutboundProxyURL)
	}
}

func TestAgentServiceUpdatePersistsTrafficStatsInterval(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := &fakeStore{}
	if err := store.SaveAgent(ctx, storage.AgentRow{
		ID:                   "edge-a",
		Name:                 "Edge A",
		AgentToken:           "token-a",
		CapabilitiesJSON:     `["http_rules"]`,
		DesiredRevision:      7,
		CurrentRevision:      7,
		LastApplyStatus:      "success",
		TrafficStatsInterval: "30s",
	}); err != nil {
		t.Fatalf("SaveAgent() error = %v", err)
	}
	svc := NewAgentService(config.Config{}, store)

	agent, err := svc.Update(ctx, "edge-a", UpdateAgentRequest{
		TrafficStatsInterval: stringPtr("  1m  "),
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if agent.TrafficStatsInterval != "1m0s" || store.savedAgent.TrafficStatsInterval != "1m0s" {
		t.Fatalf("TrafficStatsInterval agent=%q saved=%q", agent.TrafficStatsInterval, store.savedAgent.TrafficStatsInterval)
	}
	assertRevisionAboveFloor(t, "saved DesiredRevision", store.savedAgent.DesiredRevision, 7)

	_, err = svc.Update(ctx, "edge-a", UpdateAgentRequest{
		TrafficStatsInterval: stringPtr("60s"),
	})
	if err != nil {
		t.Fatalf("Update() unchanged canonical error = %v", err)
	}
	if store.savedAgent.TrafficStatsInterval != "1m0s" {
		t.Fatalf("TrafficStatsInterval after unchanged update = %q", store.savedAgent.TrafficStatsInterval)
	}
	unchangedRevision := store.savedAgent.DesiredRevision

	_, err = svc.Update(ctx, "edge-a", UpdateAgentRequest{
		TrafficStatsInterval: stringPtr(" "),
	})
	if err != nil {
		t.Fatalf("Update() clear error = %v", err)
	}
	if store.savedAgent.TrafficStatsInterval != "" {
		t.Fatalf("TrafficStatsInterval after clear = %q", store.savedAgent.TrafficStatsInterval)
	}
	assertRevisionAboveFloor(t, "clear DesiredRevision", store.savedAgent.DesiredRevision, unchangedRevision)
}

func TestAgentServiceUpdateCanonicalizesEquivalentTrafficStatsIntervalWithoutRevisionBump(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := &fakeStore{}
	if err := store.SaveAgent(ctx, storage.AgentRow{
		ID:                   "edge-a",
		Name:                 "Edge A",
		AgentToken:           "token-a",
		CapabilitiesJSON:     `["http_rules"]`,
		DesiredRevision:      7,
		CurrentRevision:      7,
		LastApplyStatus:      "success",
		TrafficStatsInterval: "60s",
	}); err != nil {
		t.Fatalf("SaveAgent() error = %v", err)
	}
	svc := NewAgentService(config.Config{}, store)

	agent, err := svc.Update(ctx, "edge-a", UpdateAgentRequest{
		TrafficStatsInterval: stringPtr("1m"),
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if agent.TrafficStatsInterval != "1m0s" || store.savedAgent.TrafficStatsInterval != "1m0s" {
		t.Fatalf("TrafficStatsInterval agent=%q saved=%q", agent.TrafficStatsInterval, store.savedAgent.TrafficStatsInterval)
	}
	if store.savedAgent.DesiredRevision != 7 {
		t.Fatalf("DesiredRevision = %d, want 7", store.savedAgent.DesiredRevision)
	}
}

func TestAgentServiceUpdateRejectsInvalidTrafficStatsInterval(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	for _, raw := range []string{"bad", "0s", "-1s"} {
		store := &fakeStore{}
		if err := store.SaveAgent(ctx, storage.AgentRow{
			ID:               "edge-a",
			Name:             "Edge A",
			AgentToken:       "token-a",
			CapabilitiesJSON: `["http_rules"]`,
			LastApplyStatus:  "success",
		}); err != nil {
			t.Fatalf("SaveAgent() error = %v", err)
		}
		svc := NewAgentService(config.Config{}, store)

		_, err := svc.Update(ctx, "edge-a", UpdateAgentRequest{
			TrafficStatsInterval: stringPtr(raw),
		})
		if !errors.Is(err, ErrInvalidArgument) || !strings.Contains(err.Error(), "traffic_stats_interval") {
			t.Fatalf("Update(%q) error = %v, want traffic_stats_interval validation", raw, err)
		}
	}
}

func TestAgentServiceUpdateBumpsDesiredRevisionWhenOutboundProxyURLChanges(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := &fakeStore{
		agents: []storage.AgentRow{{
			ID:               "edge-a",
			Name:             "Edge A",
			AgentToken:       "token-a",
			CapabilitiesJSON: `["http_rules","l4","relay"]`,
			DesiredRevision:  7,
			CurrentRevision:  7,
			LastApplyStatus:  "success",
		}},
	}
	svc := NewAgentService(config.Config{}, store)

	_, err := svc.Update(ctx, "edge-a", UpdateAgentRequest{
		OutboundProxyURL: stringPtr("socks://127.0.0.1:1080"),
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	assertRevisionAboveFloor(t, "saved DesiredRevision", store.savedAgent.DesiredRevision, 7)
}

func TestAgentServiceUpdateBumpsDesiredRevisionOnceWhenRuntimeConfigFieldsChange(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := &fakeStore{
		agents: []storage.AgentRow{{
			ID:                   "edge-a",
			Name:                 "Edge A",
			AgentToken:           "token-a",
			CapabilitiesJSON:     `["http_rules","l4","relay"]`,
			OutboundProxyURL:     "socks://127.0.0.1:1080",
			TrafficStatsInterval: "30s",
			DesiredRevision:      7,
			CurrentRevision:      7,
			LastApplyStatus:      "success",
		}, {
			ID:              "edge-b",
			Name:            "Edge B",
			AgentToken:      "token-b",
			DesiredRevision: 9,
			CurrentRevision: 9,
		}},
	}
	svc := NewAgentService(config.Config{}, store)

	_, err := svc.Update(ctx, "edge-a", UpdateAgentRequest{
		OutboundProxyURL:     stringPtr("socks://127.0.0.1:1081"),
		TrafficStatsInterval: stringPtr("1m"),
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	if store.savedAgent.DesiredRevision != 8 {
		t.Fatalf("DesiredRevision = %d, want 8", store.savedAgent.DesiredRevision)
	}
}

func TestAgentServiceUpdateCommitsDesiredVersionAndRuntimeConfigInOneRevision(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	store, err := newServiceTestSQLiteStore(t, t.TempDir(), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	if err := store.SaveAgent(ctx, storage.AgentRow{
		ID: "edge-version", Name: "Edge Version", AgentToken: "token-version",
		Platform: "linux-amd64", CapabilitiesJSON: `["http_rules","package_manifest_v1"]`,
		DesiredVersion: "1.0.0", DesiredRevision: 1, CurrentRevision: 1,
		LastApplyRevision: 1, LastApplyStatus: "success",
	}); err != nil {
		t.Fatalf("SaveAgent() error = %v", err)
	}
	if err := store.SaveVersionPolicies(ctx, []storage.VersionPolicyRow{{
		ID: "stable", Channel: "stable", DesiredVersion: "2.0.0",
		PackagesJSON: `[{"platform":"linux-amd64","url":"https://example.com/nre-agent","sha256":"package-sha"}]`,
		TagsJSON:     `[]`,
	}}); err != nil {
		t.Fatalf("SaveVersionPolicies() error = %v", err)
	}

	svc := NewAgentService(config.Config{}, store)
	updated, err := svc.Update(ctx, "edge-version", UpdateAgentRequest{
		DesiredVersion:       stringPtr("2.0.0"),
		OutboundProxyURL:     stringPtr("socks://127.0.0.1:1080"),
		TrafficStatsInterval: stringPtr("1m"),
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if updated.DesiredVersion != "2.0.0" || updated.OutboundProxyURL != "socks://127.0.0.1:1080" || updated.TrafficStatsInterval != "1m0s" {
		t.Fatalf("Update() = %+v", updated)
	}

	revisions, err := store.ListAgentRevisions(ctx, "edge-version")
	if err != nil {
		t.Fatalf("ListAgentRevisions() error = %v", err)
	}
	if len(revisions) != 1 || revisions[0].State != storage.AgentRevisionStatePending || revisions[0].DesiredVersion != "2.0.0" {
		t.Fatalf("revisions = %+v, want one pending 2.0.0 revision", revisions)
	}
	if int64(updated.DesiredRevision) != revisions[0].Revision {
		t.Fatalf("summary desired revision = %d, ledger revision = %d", updated.DesiredRevision, revisions[0].Revision)
	}
	artifact, found, err := store.GetGenerationArtifact(ctx, revisions[0].SnapshotArtifactID)
	if err != nil || !found {
		t.Fatalf("GetGenerationArtifact() found=%v error=%v", found, err)
	}
	var snapshot storage.Snapshot
	if err := json.Unmarshal(artifact.Payload, &snapshot); err != nil {
		t.Fatalf("decode snapshot: %v", err)
	}
	if snapshot.DesiredVersion != "2.0.0" || snapshot.VersionPackage == nil || snapshot.VersionPackage.SHA256 != "package-sha" {
		t.Fatalf("snapshot version assignment = %+v", snapshot)
	}
	if snapshot.AgentConfig.OutboundProxyURL != "socks://127.0.0.1:1080" || snapshot.AgentConfig.TrafficStatsInterval != "1m0s" {
		t.Fatalf("snapshot agent config = %+v", snapshot.AgentConfig)
	}
}

func TestAgentServiceUpdateLocalCommitsDesiredVersionAndRuntimeConfigInOneRevision(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	store, err := newServiceTestSQLiteStore(t, t.TempDir(), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	platform := runtime.GOOS + "-" + runtime.GOARCH
	if err := store.SaveVersionPolicies(ctx, []storage.VersionPolicyRow{{
		ID: "local-stable", Channel: "stable", DesiredVersion: "2.0.0",
		PackagesJSON: fmt.Sprintf(`[{"platform":%q,"url":"https://example.com/local-agent","sha256":"local-package-sha"}]`, platform),
		TagsJSON:     `[]`,
	}}); err != nil {
		t.Fatalf("SaveVersionPolicies() error = %v", err)
	}
	before, err := store.ListAgentRevisions(ctx, "local")
	if err != nil {
		t.Fatalf("ListAgentRevisions(before) error = %v", err)
	}

	svc := NewAgentService(config.Config{EnableLocalAgent: true, LocalAgentID: "local", LocalAgentName: "Local"}, store)
	updated, err := svc.Update(ctx, "local", UpdateAgentRequest{
		DesiredVersion:       stringPtr("2.0.0"),
		OutboundProxyURL:     stringPtr("http://127.0.0.1:8080"),
		TrafficStatsInterval: stringPtr("45s"),
	})
	if err != nil {
		t.Fatalf("Update(local) error = %v", err)
	}
	if !updated.IsLocal || updated.DesiredVersion != "2.0.0" || updated.OutboundProxyURL != "http://127.0.0.1:8080" || updated.TrafficStatsInterval != "45s" {
		t.Fatalf("Update(local) = %+v", updated)
	}
	after, err := store.ListAgentRevisions(ctx, "local")
	if err != nil {
		t.Fatalf("ListAgentRevisions(after) error = %v", err)
	}
	if len(after) != len(before)+1 {
		t.Fatalf("local revision count = %d -> %d", len(before), len(after))
	}
	revisionRow := after[len(after)-1]
	artifact, found, err := store.GetGenerationArtifact(ctx, revisionRow.SnapshotArtifactID)
	if err != nil || !found {
		t.Fatalf("GetGenerationArtifact() found=%v error=%v", found, err)
	}
	var snapshot storage.Snapshot
	if err := json.Unmarshal(artifact.Payload, &snapshot); err != nil {
		t.Fatalf("decode snapshot: %v", err)
	}
	if snapshot.DesiredVersion != "2.0.0" {
		t.Fatalf("local snapshot version assignment = %+v", snapshot)
	}
	if supportsBundledAgentPackage(platform) {
		if snapshot.VersionPackage == nil || snapshot.VersionPackage.SHA256 != "local-package-sha" {
			t.Fatalf("local snapshot package assignment = %+v", snapshot.VersionPackage)
		}
	} else if snapshot.VersionPackage != nil {
		t.Fatalf("unsupported local platform received package = %+v", snapshot.VersionPackage)
	}
	if snapshot.AgentConfig.OutboundProxyURL != "http://127.0.0.1:8080" || snapshot.AgentConfig.TrafficStatsInterval != "45s" {
		t.Fatalf("local snapshot config = %+v", snapshot.AgentConfig)
	}
}

func TestAgentServiceUpdateRejectsInvalidOutboundProxyURL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := &fakeStore{}
	if err := store.SaveAgent(ctx, storage.AgentRow{
		ID:               "edge-a",
		Name:             "Edge A",
		AgentToken:       "token-a",
		CapabilitiesJSON: `["http_rules","l4","relay"]`,
		LastApplyStatus:  "success",
	}); err != nil {
		t.Fatalf("SaveAgent() error = %v", err)
	}
	svc := NewAgentService(config.Config{}, store)
	_, err := svc.Update(ctx, "edge-a", UpdateAgentRequest{
		OutboundProxyURL: stringPtr("127.0.0.1:1080"),
	})
	if err == nil || !strings.Contains(err.Error(), "outbound_proxy_url") {
		t.Fatalf("Update() error = %v, want outbound_proxy_url validation", err)
	}
}

func TestAgentServiceUpdateRejectsUnsupportedOutboundProxyURL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := &fakeStore{}
	if err := store.SaveAgent(ctx, storage.AgentRow{
		ID:               "edge-a",
		Name:             "Edge A",
		AgentToken:       "token-a",
		CapabilitiesJSON: `["http_rules","l4","relay"]`,
		LastApplyStatus:  "success",
	}); err != nil {
		t.Fatalf("SaveAgent() error = %v", err)
	}
	svc := NewAgentService(config.Config{}, store)

	for _, raw := range []string{
		"ftp://proxy.local:21",
		"http://127.0.0.1",
		"socks5://proxy.local:notaport",
	} {
		_, err := svc.Update(ctx, "edge-a", UpdateAgentRequest{
			OutboundProxyURL: stringPtr(raw),
		})
		if err == nil || !strings.Contains(err.Error(), "invalid outbound_proxy_url") {
			t.Fatalf("Update(%q) error = %v, want invalid outbound_proxy_url validation", raw, err)
		}
	}
}

func TestAgentServiceUpdateTrimsOutboundProxyURL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := &fakeStore{}
	if err := store.SaveAgent(ctx, storage.AgentRow{
		ID:               "edge-a",
		Name:             "Edge A",
		AgentToken:       "token-a",
		CapabilitiesJSON: `["http_rules","l4","relay"]`,
		LastApplyStatus:  "success",
	}); err != nil {
		t.Fatalf("SaveAgent() error = %v", err)
	}
	svc := NewAgentService(config.Config{}, store)
	agent, err := svc.Update(ctx, "edge-a", UpdateAgentRequest{
		OutboundProxyURL: stringPtr("  socks://user:pass@127.0.0.1:1080  "),
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if agent.OutboundProxyURL != "socks://user:pass@127.0.0.1:1080" || store.savedAgent.OutboundProxyURL != "socks://user:pass@127.0.0.1:1080" {
		t.Fatalf("OutboundProxyURL agent=%q saved=%q", agent.OutboundProxyURL, store.savedAgent.OutboundProxyURL)
	}
}

func TestAgentServiceUpdateClearsOutboundProxyURL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := &fakeStore{}
	if err := store.SaveAgent(ctx, storage.AgentRow{
		ID:               "edge-a",
		Name:             "Edge A",
		AgentToken:       "token-a",
		CapabilitiesJSON: `["http_rules","l4","relay"]`,
		OutboundProxyURL: "socks://user:pass@127.0.0.1:1080",
		LastApplyStatus:  "success",
	}); err != nil {
		t.Fatalf("SaveAgent() error = %v", err)
	}
	svc := NewAgentService(config.Config{}, store)
	agent, err := svc.Update(ctx, "edge-a", UpdateAgentRequest{
		OutboundProxyURL: stringPtr(" "),
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if agent.OutboundProxyURL != "" || store.savedAgent.OutboundProxyURL != "" {
		t.Fatalf("OutboundProxyURL agent=%q saved=%q", agent.OutboundProxyURL, store.savedAgent.OutboundProxyURL)
	}
}

func TestAgentServiceUpdatePreservesOmittedOutboundProxyURL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := &fakeStore{}
	if err := store.SaveAgent(ctx, storage.AgentRow{
		ID:               "edge-a",
		Name:             "Edge A",
		AgentToken:       "token-a",
		CapabilitiesJSON: `["http_rules","l4","relay"]`,
		OutboundProxyURL: "socks://user:pass@127.0.0.1:1080",
		LastApplyStatus:  "success",
	}); err != nil {
		t.Fatalf("SaveAgent() error = %v", err)
	}
	svc := NewAgentService(config.Config{}, store)
	agent, err := svc.Update(ctx, "edge-a", UpdateAgentRequest{
		Name: stringPtr("Edge A Updated"),
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if agent.OutboundProxyURL != "socks://user:pass@127.0.0.1:1080" || store.savedAgent.OutboundProxyURL != "socks://user:pass@127.0.0.1:1080" {
		t.Fatalf("OutboundProxyURL agent=%q saved=%q", agent.OutboundProxyURL, store.savedAgent.OutboundProxyURL)
	}
}

func TestAgentServiceUpdatePreservesMatchingRedactedOutboundProxyURL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := &fakeStore{}
	if err := store.SaveAgent(ctx, storage.AgentRow{
		ID:               "edge-a",
		Name:             "Edge A",
		AgentToken:       "token-a",
		CapabilitiesJSON: `["http_rules","l4","relay"]`,
		OutboundProxyURL: "socks://user:pass@127.0.0.1:1080",
		DesiredRevision:  7,
		CurrentRevision:  7,
		LastApplyStatus:  "success",
	}); err != nil {
		t.Fatalf("SaveAgent() error = %v", err)
	}
	svc := NewAgentService(config.Config{}, store)
	agent, err := svc.Update(ctx, "edge-a", UpdateAgentRequest{
		OutboundProxyURL: stringPtr("socks://user:xxxxx@127.0.0.1:1080"),
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if agent.OutboundProxyURL != "socks://user:pass@127.0.0.1:1080" || store.savedAgent.OutboundProxyURL != "socks://user:pass@127.0.0.1:1080" {
		t.Fatalf("OutboundProxyURL agent=%q saved=%q", agent.OutboundProxyURL, store.savedAgent.OutboundProxyURL)
	}
	if store.savedAgent.DesiredRevision != 7 {
		t.Fatalf("DesiredRevision = %d, want 7", store.savedAgent.DesiredRevision)
	}
}

func TestAgentServiceUpdateRejectsMismatchedRedactedOutboundProxyURL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := &fakeStore{}
	if err := store.SaveAgent(ctx, storage.AgentRow{
		ID:               "edge-a",
		Name:             "Edge A",
		AgentToken:       "token-a",
		CapabilitiesJSON: `["http_rules","l4","relay"]`,
		OutboundProxyURL: "socks://user:pass@127.0.0.1:1080",
		LastApplyStatus:  "success",
	}); err != nil {
		t.Fatalf("SaveAgent() error = %v", err)
	}
	svc := NewAgentService(config.Config{}, store)
	_, err := svc.Update(ctx, "edge-a", UpdateAgentRequest{
		OutboundProxyURL: stringPtr("socks://other:xxxxx@127.0.0.1:1080"),
	})
	if err == nil || !strings.Contains(err.Error(), "outbound_proxy_url password is redacted") {
		t.Fatalf("Update() error = %v, want redacted outbound_proxy_url validation", err)
	}
	if store.savedAgent.OutboundProxyURL != "socks://user:pass@127.0.0.1:1080" {
		t.Fatalf("saved OutboundProxyURL = %q", store.savedAgent.OutboundProxyURL)
	}
}

func TestAgentServiceListHTTPRulesNormalizesStoredFields(t *testing.T) {
	t.Parallel()
	svc := NewAgentService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, &fakeStore{
		rulesByID: map[string][]storage.HTTPRuleRow{
			"local": {{
				ID:                1,
				AgentID:           "local",
				FrontendURL:       "https://emby.example.com",
				BackendURL:        "http://legacy:8096",
				BackendsJSON:      `[{"url":"http://emby:8096"}]`,
				LoadBalancingJSON: `{}`,
				Enabled:           true,
				TagsJSON:          `["media"]`,
				ProxyRedirect:     true,
				RelayChainJSON:    `[1,2]`,
				RelayLayersJSON:   `[[1,3],[2]]`,
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
	if rule.BackendURL != "" || len(rule.RelayChain) != 0 {
		t.Fatalf("legacy fields = backend_url=%q relay_chain=%+v", rule.BackendURL, rule.RelayChain)
	}
	if rule.LoadBalancing.Strategy != "adaptive" {
		t.Fatalf("LoadBalancing = %+v", rule.LoadBalancing)
	}
	if len(rule.RelayLayers) != 2 || len(rule.RelayLayers[0]) != 2 || rule.RelayLayers[0][1] != 3 {
		t.Fatalf("RelayLayers = %+v", rule.RelayLayers)
	}
	if len(rule.CustomHeaders) != 1 || rule.CustomHeaders[0].Name != "X-Test" {
		t.Fatalf("CustomHeaders = %+v", rule.CustomHeaders)
	}
}

func TestAgentServiceHeartbeatReturnsFullSnapshotSyncPayload(t *testing.T) {
	t.Parallel()
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
				Platform: "linux-amd64",
				URL:      "https://example.com/agent-linux.tar.gz",
				SHA256:   "sha-linux",
				Filename: "agent-linux.tar.gz",
				Size:     123,
			},
			Rules: []storage.HTTPRule{{
				ID:          9,
				FrontendURL: "https://edge.example.com",
				Backends:    []storage.HTTPBackend{{URL: "http://127.0.0.1:8096"}},
				RelayLayers: [][]int{{11, 22}},
				Revision:    6,
			}},
			L4Rules: []storage.L4Rule{{
				ID:         2,
				Protocol:   "tcp",
				ListenHost: "0.0.0.0",
				ListenPort: 9000,
				Backends:   []storage.L4Backend{{Host: "127.0.0.1", Port: 9001}},
				Revision:   6,
			}},
			RelayListeners: []storage.RelayListener{{
				ID:            11,
				AgentID:       "remote-a",
				Name:          "relay-a",
				ListenHost:    "0.0.0.0",
				ListenPort:    7443,
				TransportMode: "quic",
				Revision:      4,
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
		Platform:         "linux-amd64",
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
	if reply.VersionPackage != "https://example.com/agent-linux.tar.gz" || reply.VersionSHA256 != "sha-linux" {
		t.Fatalf("version package fields = %q / %q", reply.VersionPackage, reply.VersionSHA256)
	}
	if reply.VersionPackageMeta == nil || reply.VersionPackageMeta.Platform != "linux-amd64" {
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
	if store.lastSnapshotInput.Platform != "linux-amd64" || store.lastSnapshotInput.DesiredVersion != "2.0.0" {
		t.Fatalf("snapshot input = %+v", store.lastSnapshotInput)
	}
	if store.savedAgentCalls != 1 {
		t.Fatalf("SaveAgent() calls = %d", store.savedAgentCalls)
	}
	if store.savedAgent.Version != "1.4.0" || store.savedAgent.Platform != "linux-amd64" || store.savedAgent.CurrentRevision != 1 {
		t.Fatalf("saved agent metadata = %+v", store.savedAgent)
	}
	if store.savedAgent.LastSeenAt != now.Format(time.RFC3339) {
		t.Fatalf("LastSeenAt = %q", store.savedAgent.LastSeenAt)
	}
}

func TestAgentServiceHeartbeatOmitsSyncPayloadWhenUpToDateButKeepsRelayListeners(t *testing.T) {
	t.Parallel()
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
			Rules:          []storage.HTTPRule{{ID: 1, FrontendURL: "https://a.example.com", Backends: []storage.HTTPBackend{{URL: "http://127.0.0.1:8096"}}}},
			L4Rules:        []storage.L4Rule{{ID: 2, Protocol: "tcp", ListenHost: "0.0.0.0", ListenPort: 9000, Backends: []storage.L4Backend{{Host: "127.0.0.1", Port: 9001}}}},
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

func TestAgentServiceHeartbeatForcesFullSyncWhenLastApplyFailedAtCurrentRevision(t *testing.T) {
	t.Parallel()
	store := &fakeStore{
		agents: []storage.AgentRow{{
			ID:               "remote-c",
			Name:             "remote-c",
			AgentToken:       "token-remote-c",
			DesiredVersion:   "3.1.0",
			DesiredRevision:  7,
			CurrentRevision:  7,
			LastApplyStatus:  "success",
			LastApplyMessage: "",
		}},
		snapshot: storage.Snapshot{
			DesiredVersion: "3.1.0",
			Revision:       7,
			Rules:          []storage.HTTPRule{{ID: 1, FrontendURL: "https://edge.example.com", Backends: []storage.HTTPBackend{{URL: "http://127.0.0.1:8096"}}}},
			L4Rules:        []storage.L4Rule{{ID: 2, Protocol: "tcp", ListenHost: "0.0.0.0", ListenPort: 50381, Backends: []storage.L4Backend{{Host: "127.0.0.1", Port: 9001}}}},
			RelayListeners: []storage.RelayListener{{ID: 4, AgentID: "remote-c", Name: "relay-local", ListenHost: "0.0.0.0", ListenPort: 443}},
			Certificates:   []storage.ManagedCertificateBundle{{ID: 8, Domain: "relay.example.com", CertPEM: "CERT", KeyPEM: "KEY"}},
			CertificatePolicies: []storage.ManagedCertificatePolicy{{
				ID:              8,
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
		CurrentRevision:   7,
		LastApplyRevision: 7,
		LastApplyStatus:   "error",
		LastApplyMessage:  "relay listener 4: certificate 8 not found",
		Platform:          "linux-amd64",
	}, "token-remote-c")
	if err != nil {
		t.Fatalf("Heartbeat() error = %v", err)
	}

	if !reply.HasUpdate {
		t.Fatalf("HasUpdate = false, want true")
	}
	if len(reply.Rules) != 1 || len(reply.L4Rules) != 1 || len(reply.RelayListeners) != 1 {
		t.Fatalf("expected full rule payload on failed apply retry: %+v", reply)
	}
	if len(reply.Certificates) != 1 || len(reply.CertificatePolicies) != 1 {
		t.Fatalf("expected full certificate payload on failed apply retry: %+v", reply)
	}
	if store.savedAgent.LastApplyStatus != "error" || store.savedAgent.LastApplyMessage == "" {
		t.Fatalf("expected failed apply state to persist, got %+v", store.savedAgent)
	}
}

func TestAgentServiceHeartbeatAppliesManagedCertificateReports(t *testing.T) {
	t.Parallel()
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

func TestAgentServiceConcurrentHeartbeatsMergeMasterCertificateReports(t *testing.T) {
	t.Parallel()

	store := newConcurrentManagedCertificateReportStore(storage.ManagedCertificateRow{
		ID:              81,
		Domain:          "merge.example.com",
		Enabled:         true,
		Scope:           "domain",
		IssuerMode:      "master_cf_dns",
		TargetAgentIDs:  `["edge-a","edge-b"]`,
		Status:          "pending",
		AgentReports:    `{}`,
		Usage:           "https",
		CertificateType: "acme",
		Revision:        9,
	})
	svc := NewAgentService(config.Config{}, store)
	svc.now = func() time.Time { return time.Date(2026, 7, 26, 9, 0, 0, 0, time.UTC) }

	start := make(chan struct{})
	ready := sync.WaitGroup{}
	ready.Add(2)
	errorsByAgent := make(chan error, 2)
	for _, agentID := range []string{"edge-a", "edge-b"} {
		agentID := agentID
		go func() {
			ready.Done()
			<-start
			errorsByAgent <- svc.reconcileManagedCertificatesFromHeartbeat(context.Background(), storage.AgentRow{ID: agentID}, HeartbeatRequest{
				ManagedCertificateReports: []ManagedCertificateHeartbeatReport{{
					ID:           81,
					Domain:       "merge.example.com",
					Status:       "active",
					LastIssueAt:  "2026-07-26T09:00:00Z",
					MaterialHash: "pending-material-hash",
				}},
			})
		}()
	}
	ready.Wait()
	close(start)
	for range 2 {
		if err := <-errorsByAgent; err != nil {
			t.Fatalf("reconcileManagedCertificatesFromHeartbeat() error = %v", err)
		}
	}

	rows := store.snapshot()
	if len(rows) != 1 {
		t.Fatalf("managed certificate rows = %+v", rows)
	}
	reports := managedCertificateFromRow(rows[0]).AgentReports
	if _, ok := reports["edge-a"]; !ok {
		t.Fatalf("edge-a report was lost: %+v", reports)
	}
	if _, ok := reports["edge-b"]; !ok {
		t.Fatalf("edge-b report was lost: %+v", reports)
	}
}

type concurrentManagedCertificateReportStore struct {
	*fakeStore
	mu         sync.Mutex
	rows       []storage.ManagedCertificateRow
	listCalls  int
	bothListed chan struct{}
}

func newConcurrentManagedCertificateReportStore(row storage.ManagedCertificateRow) *concurrentManagedCertificateReportStore {
	return &concurrentManagedCertificateReportStore{
		fakeStore:  &fakeStore{},
		rows:       []storage.ManagedCertificateRow{row},
		bothListed: make(chan struct{}),
	}
}

func (s *concurrentManagedCertificateReportStore) ListManagedCertificates(ctx context.Context) ([]storage.ManagedCertificateRow, error) {
	s.mu.Lock()
	rows := append([]storage.ManagedCertificateRow(nil), s.rows...)
	s.listCalls++
	if s.listCalls == 2 {
		close(s.bothListed)
	}
	bothListed := s.bothListed
	s.mu.Unlock()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-bothListed:
		return rows, nil
	}
}

func (s *concurrentManagedCertificateReportStore) SaveManagedCertificates(_ context.Context, rows []storage.ManagedCertificateRow) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rows = append([]storage.ManagedCertificateRow(nil), rows...)
	return nil
}

func (s *concurrentManagedCertificateReportStore) UpdateManagedCertificates(_ context.Context, update func([]storage.ManagedCertificateRow) ([]storage.ManagedCertificateRow, bool, error)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next, changed, err := update(append([]storage.ManagedCertificateRow(nil), s.rows...))
	if err != nil {
		return err
	}
	if changed {
		s.rows = append([]storage.ManagedCertificateRow(nil), next...)
	}
	return nil
}

func (s *concurrentManagedCertificateReportStore) snapshot() []storage.ManagedCertificateRow {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]storage.ManagedCertificateRow(nil), s.rows...)
}

func TestAgentServiceHeartbeatReconcilesLocalHTTP01FromApplyStatus(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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

func TestHeartbeatIngestsTrafficWhenModuleEnabled(t *testing.T) {
	t.Parallel()
	store := &fakeStore{
		agents: []storage.AgentRow{{
			ID:              "remote-traffic",
			Name:            "remote-traffic",
			AgentToken:      "token-remote-traffic",
			DesiredRevision: 2,
			CurrentRevision: 1,
			LastApplyStatus: "success",
		}},
		snapshot: storage.Snapshot{DesiredVersion: "3.0.0", Revision: 2},
	}
	trafficSvc := &fakeHeartbeatTrafficService{}
	svc := NewAgentService(config.Config{TrafficStatsEnabled: true}, store)
	svc.SetTrafficService(trafficSvc)

	reply, err := svc.Heartbeat(context.Background(), HeartbeatRequest{
		CurrentRevision: 1,
		Stats: AgentStats{
			"status": "运行中",
			"traffic": map[string]any{
				"total": map[string]any{"rx_bytes": float64(123), "tx_bytes": float64(456)},
			},
		},
	}, "token-remote-traffic")
	if err != nil {
		t.Fatalf("Heartbeat() error = %v", err)
	}

	if got := len(trafficSvc.ingestCalls); got != 1 {
		t.Fatalf("traffic ingest calls = %d, want 1", got)
	}
	if trafficSvc.ingestCalls[0].agentID != "remote-traffic" {
		t.Fatalf("traffic ingest agentID = %q", trafficSvc.ingestCalls[0].agentID)
	}
	if _, ok := trafficSvc.ingestCalls[0].stats["traffic"]; !ok {
		t.Fatalf("traffic ingest stats = %+v, want original traffic payload", trafficSvc.ingestCalls[0].stats)
	}
	stats := parseAgentStats(store.savedAgent.LastReportedStatsJSON)
	if _, ok := stats["traffic"]; ok {
		t.Fatalf("LastReportedStatsJSON = %q, want traffic omitted", store.savedAgent.LastReportedStatsJSON)
	}
	if stats["status"] != "运行中" {
		t.Fatalf("LastReportedStatsJSON = %q, want non-traffic stats persisted", store.savedAgent.LastReportedStatsJSON)
	}
	if store.savedHeartbeatCalls != 1 {
		t.Fatalf("SaveAgentHeartbeat calls = %d, want 1", store.savedHeartbeatCalls)
	}
	if store.savedAgentCalls != 0 {
		t.Fatalf("SaveAgent calls = %d, want 0", store.savedAgentCalls)
	}
	if reply.TrafficStatsEnabled == nil || !*reply.TrafficStatsEnabled {
		t.Fatalf("TrafficStatsEnabled = %v, want true", reply.TrafficStatsEnabled)
	}
}

func TestHeartbeatIgnoresTrafficAndDisablesAgentReportingWhenModuleDisabled(t *testing.T) {
	t.Parallel()
	store := &fakeStore{
		agents: []storage.AgentRow{{
			ID:                    "remote-traffic",
			Name:                  "remote-traffic",
			AgentToken:            "token-remote-traffic",
			DesiredRevision:       2,
			CurrentRevision:       1,
			LastApplyStatus:       "success",
			LastReportedStatsJSON: `{"traffic":{"total":{"rx_bytes":1,"tx_bytes":2}}}`,
		}},
		snapshot: storage.Snapshot{DesiredVersion: "3.0.0", Revision: 2},
	}
	trafficSvc := &fakeHeartbeatTrafficService{}
	cfg := config.Default()
	cfg.TrafficStatsEnabled = false
	svc := NewAgentService(cfg, store)
	svc.SetTrafficService(trafficSvc)

	reply, err := svc.Heartbeat(context.Background(), HeartbeatRequest{
		CurrentRevision: 1,
		Stats: AgentStats{
			"traffic": map[string]any{
				"total": map[string]any{"rx_bytes": float64(123), "tx_bytes": float64(456)},
			},
		},
	}, "token-remote-traffic")
	if err != nil {
		t.Fatalf("Heartbeat() error = %v", err)
	}

	if got := len(trafficSvc.ingestCalls); got != 0 {
		t.Fatalf("traffic ingest calls = %d, want 0", got)
	}
	if store.savedAgent.LastReportedStatsJSON != "" {
		t.Fatalf("LastReportedStatsJSON = %q, want cleared", store.savedAgent.LastReportedStatsJSON)
	}
	if reply.TrafficStatsEnabled == nil || *reply.TrafficStatsEnabled {
		t.Fatalf("TrafficStatsEnabled = %v, want false", reply.TrafficStatsEnabled)
	}
	if reply.TrafficBlocked {
		t.Fatal("TrafficBlocked = true, want false when module disabled")
	}
}

func TestHeartbeatUsesFullSaveWhenConfigFieldsChange(t *testing.T) {
	t.Parallel()
	store := &fakeStore{
		agents: []storage.AgentRow{{
			ID:              "remote-config",
			Name:            "remote-config",
			AgentToken:      "token-remote-config",
			DesiredRevision: 2,
			CurrentRevision: 1,
			LastApplyStatus: "success",
		}},
		snapshot: storage.Snapshot{DesiredVersion: "3.0.0", Revision: 2},
	}
	svc := NewAgentService(config.Config{TrafficStatsEnabled: true}, store)

	_, err := svc.Heartbeat(context.Background(), HeartbeatRequest{
		CurrentRevision: 1,
		Capabilities:    []string{"http_rules"},
		Stats: AgentStats{
			"status": "运行中",
		},
	}, "token-remote-config")
	if err != nil {
		t.Fatalf("Heartbeat() error = %v", err)
	}
	if store.savedAgentCalls != 1 {
		t.Fatalf("SaveAgent calls = %d, want 1", store.savedAgentCalls)
	}
	if store.savedHeartbeatCalls != 0 {
		t.Fatalf("SaveAgentHeartbeat calls = %d, want 0", store.savedHeartbeatCalls)
	}
	if store.savedAgent.CapabilitiesJSON != `["http_rules"]` {
		t.Fatalf("CapabilitiesJSON = %q", store.savedAgent.CapabilitiesJSON)
	}
}

func TestHeartbeatTrafficIngestErrorFailsAgentSync(t *testing.T) {
	t.Parallel()
	store := &fakeStore{
		agents: []storage.AgentRow{{
			ID:              "remote-traffic",
			Name:            "remote-traffic",
			AgentToken:      "token-remote-traffic",
			DesiredRevision: 2,
			CurrentRevision: 1,
			LastApplyStatus: "success",
		}},
		snapshot: storage.Snapshot{DesiredVersion: "3.0.0", Revision: 2},
	}
	trafficSvc := &fakeHeartbeatTrafficService{
		ingestErr: errors.New("traffic ingest unavailable"),
	}
	svc := NewAgentService(config.Config{TrafficStatsEnabled: true}, store)
	svc.SetTrafficService(trafficSvc)

	_, err := svc.Heartbeat(context.Background(), HeartbeatRequest{
		CurrentRevision: 1,
		Stats: AgentStats{
			"traffic": map[string]any{
				"total": map[string]any{"rx_bytes": float64(123), "tx_bytes": float64(456)},
			},
		},
	}, "token-remote-traffic")
	if !errors.Is(err, trafficSvc.ingestErr) {
		t.Fatalf("Heartbeat() error = %v, want traffic ingest error", err)
	}
}

func TestHeartbeatTrafficBlockStateErrorsDoNotFailAgentSync(t *testing.T) {
	t.Parallel()
	store := &fakeStore{
		agents: []storage.AgentRow{{
			ID:              "remote-traffic",
			Name:            "remote-traffic",
			AgentToken:      "token-remote-traffic",
			DesiredRevision: 2,
			CurrentRevision: 1,
			LastApplyStatus: "success",
		}},
		snapshot: storage.Snapshot{DesiredVersion: "3.0.0", Revision: 2},
	}
	trafficSvc := &fakeHeartbeatTrafficService{
		summaryErr: errors.New("traffic summary unavailable"),
	}
	svc := NewAgentService(config.Config{TrafficStatsEnabled: true}, store)
	svc.SetTrafficService(trafficSvc)

	reply, err := svc.Heartbeat(context.Background(), HeartbeatRequest{CurrentRevision: 1}, "token-remote-traffic")
	if err != nil {
		t.Fatalf("Heartbeat() error = %v", err)
	}
	if reply.TrafficBlocked {
		t.Fatal("TrafficBlocked = true, want false on summary failure")
	}
}

func TestHeartbeatReplyIncludesTrafficBlockedState(t *testing.T) {
	t.Parallel()
	store := &fakeStore{
		agents: []storage.AgentRow{{
			ID:              "remote-traffic",
			Name:            "remote-traffic",
			AgentToken:      "token-remote-traffic",
			DesiredRevision: 2,
			CurrentRevision: 1,
			LastApplyStatus: "success",
		}},
		snapshot: storage.Snapshot{DesiredVersion: "3.0.0", Revision: 2},
	}
	trafficSvc := &fakeHeartbeatTrafficService{
		summary: TrafficSummary{
			Blocked:     true,
			BlockReason: "monthly quota exceeded",
		},
	}
	svc := NewAgentService(config.Config{TrafficStatsEnabled: true}, store)
	svc.SetTrafficService(trafficSvc)

	reply, err := svc.Heartbeat(context.Background(), HeartbeatRequest{CurrentRevision: 1}, "token-remote-traffic")
	if err != nil {
		t.Fatalf("Heartbeat() error = %v", err)
	}

	if !reply.TrafficBlocked {
		t.Fatal("TrafficBlocked = false, want true")
	}
	if reply.TrafficBlockReason != "monthly quota exceeded" {
		t.Fatalf("TrafficBlockReason = %q", reply.TrafficBlockReason)
	}
	if !store.savedAgent.TrafficBlocked || store.savedAgent.TrafficBlockReason != "monthly quota exceeded" {
		t.Fatalf("saved traffic block state = blocked=%v reason=%q", store.savedAgent.TrafficBlocked, store.savedAgent.TrafficBlockReason)
	}
	if len(store.events) != 1 || store.events[0].EventType != "traffic_block_state_changed" {
		t.Fatalf("events = %+v, want traffic_block_state_changed", store.events)
	}
}

func TestAgentServiceHeartbeatPersistsRuntimePackageMetadata(t *testing.T) {
	t.Parallel()
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

func TestAgentServiceGetUsesSnapshotPackageSHAWhenDesiredVersionEmpty(t *testing.T) {
	t.Parallel()
	store := &fakeStore{
		agents: []storage.AgentRow{{
			ID:                   "edge-1",
			Name:                 "edge-1",
			AgentToken:           "agent-token",
			Platform:             "linux-amd64",
			DesiredVersion:       "",
			RuntimePackageSHA256: "runtime-sha",
			LastApplyStatus:      "success",
		}},
		snapshot: storage.Snapshot{
			DesiredVersion: "",
			VersionPackage: &storage.VersionPackage{
				Platform: "linux-amd64",
				URL:      "/panel-api/public/agent-assets/nre-agent-linux-amd64",
				SHA256:   "desired-sha",
				Filename: "nre-agent-linux-amd64",
			},
		},
	}
	svc := NewAgentService(config.Config{}, store)

	summary, err := svc.Get(context.Background(), "edge-1")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if summary.DesiredVersion != "" {
		t.Fatalf("summary desired version = %q", summary.DesiredVersion)
	}
	if summary.DesiredPackageSHA256 != "desired-sha" {
		t.Fatalf("summary desired sha = %q", summary.DesiredPackageSHA256)
	}
	if summary.PackageSyncStatus != "pending" {
		t.Fatalf("summary package status = %q", summary.PackageSyncStatus)
	}
}

func TestAgentServiceGetUsesBundledAssetSHAWhenDesiredVersionEmpty(t *testing.T) {
	t.Parallel()
	assetDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(assetDir, "nre-agent-linux-amd64"), []byte("bundled-agent"), 0o755); err != nil {
		t.Fatalf("WriteFile(agent asset) error = %v", err)
	}

	store := &fakeStore{
		agents: []storage.AgentRow{{
			ID:                   "edge-1",
			Name:                 "edge-1",
			AgentToken:           "agent-token",
			Platform:             "linux-amd64",
			DesiredVersion:       "",
			RuntimePackageSHA256: "runtime-sha",
			LastApplyStatus:      "success",
		}},
		snapshot: storage.Snapshot{
			DesiredVersion: "",
		},
	}
	svc := NewAgentService(config.Config{
		PublicAgentAssetsDir: assetDir,
	}, store)

	summary, err := svc.Get(context.Background(), "edge-1")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if summary.DesiredPackageSHA256 == "" {
		t.Fatalf("summary desired sha is empty")
	}
	if summary.PackageSyncStatus != "pending" {
		t.Fatalf("summary package status = %q", summary.PackageSyncStatus)
	}
}

func TestAgentServiceHeartbeatUsesBundledAssetPackageWhenDesiredVersionEmpty(t *testing.T) {
	t.Parallel()
	assetDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(assetDir, "nre-agent-linux-amd64"), []byte("bundled-agent"), 0o755); err != nil {
		t.Fatalf("WriteFile(agent asset) error = %v", err)
	}

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
		},
	}
	svc := NewAgentService(config.Config{
		PublicAgentAssetsDir: assetDir,
	}, store)

	reply, err := svc.Heartbeat(context.Background(), HeartbeatRequest{
		CurrentRevision: 1,
		Platform:        "linux-amd64",
		RuntimePackage: RuntimePackageInfo{
			SHA256: "runtime-sha",
		},
	}, "agent-token")
	if err != nil {
		t.Fatalf("Heartbeat() error = %v", err)
	}
	if reply.VersionPackageMeta == nil {
		t.Fatalf("reply VersionPackageMeta = nil")
	}
	if reply.VersionPackageMeta.SHA256 == "" {
		t.Fatalf("reply VersionPackageMeta.SHA256 is empty")
	}
	if reply.VersionPackageMeta.Filename != "nre-agent-linux-amd64" {
		t.Fatalf("reply VersionPackageMeta.Filename = %q", reply.VersionPackageMeta.Filename)
	}
}

func TestBundledAgentPackageInfoCachesSHAUntilFileChanges(t *testing.T) {
	assetDir := t.TempDir()
	assetPath := filepath.Join(assetDir, "nre-agent-linux-amd64")
	if err := os.WriteFile(assetPath, []byte("bundled-agent-v1"), 0o755); err != nil {
		t.Fatalf("WriteFile(agent asset) error = %v", err)
	}

	originalHasher := fileSHA256Func
	t.Cleanup(func() {
		fileSHA256Func = originalHasher
	})

	callCount := 0
	fileSHA256Func = func(path string) (string, error) {
		callCount++
		return originalHasher(path)
	}

	var mu sync.Mutex
	cache := make(map[string]bundledPackageCacheEntry)
	first := bundledAgentPackageInfoCached(assetDir, "linux-amd64", &mu, cache)
	second := bundledAgentPackageInfoCached(assetDir, "linux-amd64", &mu, cache)
	if first == nil || second == nil {
		t.Fatalf("expected bundled package metadata")
	}
	if callCount != 1 {
		t.Fatalf("expected single sha call before file change, got %d", callCount)
	}
	if first.SHA256 != second.SHA256 {
		t.Fatalf("expected cached sha to match, got %q vs %q", first.SHA256, second.SHA256)
	}

	time.Sleep(2 * time.Millisecond)
	if err := os.WriteFile(assetPath, []byte("bundled-agent-v2"), 0o755); err != nil {
		t.Fatalf("WriteFile(agent asset v2) error = %v", err)
	}
	third := bundledAgentPackageInfoCached(assetDir, "linux-amd64", &mu, cache)
	if third == nil {
		t.Fatalf("expected bundled package metadata after change")
	}
	if callCount != 2 {
		t.Fatalf("expected sha to be recomputed after file change, got %d", callCount)
	}
	if third.SHA256 == first.SHA256 {
		t.Fatalf("expected sha to change after file update, got %q", third.SHA256)
	}
}

func TestBundledAgentPackageInfoRejectsUnsafePlatform(t *testing.T) {
	t.Parallel()
	assetDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(assetDir, "nre-agent-linux-amd64"), []byte("bundled-agent"), 0o755); err != nil {
		t.Fatalf("WriteFile(agent asset) error = %v", err)
	}

	if pkg := bundledAgentPackageInfoCached(assetDir, "../linux-amd64", nil, nil); pkg != nil {
		t.Fatalf("expected unsafe platform to be rejected, got %+v", pkg)
	}
	if pkg := bundledAgentPackageInfoCached(assetDir, `linux\amd64`, nil, nil); pkg != nil {
		t.Fatalf("expected platform with path separator to be rejected, got %+v", pkg)
	}
}

func TestAgentServiceHeartbeatNormalizesURLAndIP(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
				ID:              9,
				AgentID:         "edge-b",
				FrontendURL:     "https://relay.example.com",
				RelayChainJSON:  `[8]`,
				RelayLayersJSON: `[[7]]`,
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

func TestAgentServiceDeleteIgnoresLegacyRelayChainOnlyReference(t *testing.T) {
	t.Parallel()
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
			"edge-b": {{
				ID:              9,
				AgentID:         "edge-b",
				FrontendURL:     "https://relay.example.com",
				RelayChainJSON:  `[7]`,
				RelayLayersJSON: `[[8]]`,
			}},
		},
		l4RulesByID: map[string][]storage.L4RuleRow{},
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
}

func TestAgentServiceDeleteRejectsRelayLayerOnlyReference(t *testing.T) {
	t.Parallel()
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
				ID:      8,
				AgentID: "edge-a",
				Name:    "relay-b",
			}},
		},
		rulesByID: map[string][]storage.HTTPRuleRow{
			"edge-b": {{
				ID:              10,
				AgentID:         "edge-b",
				FrontendURL:     "https://relay.example.com",
				RelayChainJSON:  `[7]`,
				RelayLayersJSON: `[[7,8]]`,
			}},
		},
		l4RulesByID: map[string][]storage.L4RuleRow{},
	}
	svc := NewAgentService(cfg, store)

	_, err := svc.Delete(context.Background(), "edge-a")
	if err == nil || err.Error() != "invalid argument: cannot delete agent edge-a: relay listener 8 is referenced by HTTP rule #10 on agent edge-b" {
		t.Fatalf("Delete() error = %v", err)
	}
}

func TestAgentServiceStatsFallbackAndApplyBehavior(t *testing.T) {
	t.Parallel()
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
		savedRuntimeState: storage.RuntimeState{
			Metadata: map[string]string{
				"stats": `{"traffic":{"total":{"rx_bytes":123,"tx_bytes":456}}}`,
			},
		},
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
	traffic, ok := localStats["traffic"].(map[string]any)
	if !ok {
		t.Fatalf("Stats(local) missing traffic = %+v", localStats)
	}
	total, ok := traffic["total"].(map[string]any)
	if !ok {
		t.Fatalf("Stats(local) missing total traffic = %+v", localStats)
	}
	if total["rx_bytes"] != float64(123) || total["tx_bytes"] != float64(456) {
		t.Fatalf("Stats(local) = %+v", localStats)
	}

	remoteApply, err := svc.Apply(context.Background(), "edge-1")
	if err != nil {
		t.Fatalf("Apply(remote) error = %v", err)
	}
	if remoteApply.Message != "current desired revision is already scheduled" || !remoteApply.Pending || remoteApply.DesiredRevision != 5 {
		t.Fatalf("Apply(remote) = %+v, savedAgent = %+v", remoteApply, store.savedAgent)
	}
	if store.savedAgentCalls != 0 {
		t.Fatalf("Apply(remote) savedAgentCalls = %d, want retry intent without desired bump", store.savedAgentCalls)
	}

	localApply, err := svc.Apply(context.Background(), "local")
	if err != nil {
		t.Fatalf("Apply(local) error = %v", err)
	}
	if localApply.Message != "current desired revision is already scheduled" || !localApply.Pending {
		t.Fatalf("Apply(local) = %+v", localApply)
	}
	if store.saveRuntimeCalls != 0 {
		t.Fatalf("Apply(local) should not persist fake runtime state, saveRuntimeCalls = %d", store.saveRuntimeCalls)
	}
}

func TestAgentServiceRegisterDoesNotReuseByNameAlone(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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

func TestAgentServiceUpdateRejectsLocalAgentWithEnglishError(t *testing.T) {
	t.Parallel()
	svc := NewAgentService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, &fakeStore{})

	_, err := svc.Update(context.Background(), "local", UpdateAgentRequest{})
	if err == nil {
		t.Fatal("expected error")
	}
	if err.Error() != "invalid argument: local agent cannot be modified" {
		t.Fatalf("Update() error = %v", err)
	}
}

func TestAgentServiceDeleteRejectsLocalAgentWithEnglishError(t *testing.T) {
	t.Parallel()
	svc := NewAgentService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, &fakeStore{})

	_, err := svc.Delete(context.Background(), "local")
	if err == nil {
		t.Fatal("expected error")
	}
	if err.Error() != "invalid argument: local agent cannot be deleted" {
		t.Fatalf("Delete() error = %v", err)
	}
}

func TestAgentServiceDeleteCleansOrphanStateIdempotently(t *testing.T) {
	t.Parallel()
	store := &fakeStore{}
	svc := NewAgentService(config.Config{}, store)

	deleted, err := svc.Delete(t.Context(), "edge-orphan")
	if err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if deleted.ID != "edge-orphan" {
		t.Fatalf("deleted agent = %+v, want orphan identity", deleted)
	}
	if store.deletedAgentID != "edge-orphan" {
		t.Fatalf("DeleteAgent() called with %q, want orphan cleanup", store.deletedAgentID)
	}
}

// fakeDDNSReconciler records ReconcileAfterHeartbeat invocations and can be
// configured to panic, exercising the fire-and-forget contract on the heartbeat
// main path.
type fakeDDNSReconciler struct {
	calledIDs []string
	panicOn   bool
}

func (f *fakeDDNSReconciler) ReconcileAfterHeartbeat(_ context.Context, agentID string) {
	f.calledIDs = append(f.calledIDs, agentID)
	if f.panicOn {
		panic("simulated ddns reconciler failure")
	}
}

func TestAgentServiceHeartbeatWritesReportedIPv4IPv6OnlyWhenNonEmpty(t *testing.T) {
	t.Parallel()
	store := &fakeStore{
		agents: []storage.AgentRow{{
			ID:              "remote-ddns",
			Name:            "remote-ddns",
			AgentToken:      "token-remote-ddns",
			DesiredRevision: 2,
			CurrentRevision: 1,
			LastApplyStatus: "success",
			LastSeenIPv4:    "198.51.100.7",
			LastSeenIPv6:    "2001:db8::dead",
		}},
		snapshot: storage.Snapshot{DesiredVersion: "3.0.0", Revision: 2},
	}
	svc := NewAgentService(config.Config{}, store)

	if _, err := svc.Heartbeat(context.Background(), HeartbeatRequest{
		CurrentRevision: 1,
		LastSeenIPv4:    "203.0.113.42",
		// LastSeenIPv6 omitted this cycle -> previous value must be retained.
	}, "token-remote-ddns"); err != nil {
		t.Fatalf("Heartbeat() error = %v", err)
	}
	if store.savedAgent.LastSeenIPv4 != "203.0.113.42" {
		t.Fatalf("saved LastSeenIPv4 = %q, want non-empty overwrite", store.savedAgent.LastSeenIPv4)
	}
	if store.savedAgent.LastSeenIPv6 != "2001:db8::dead" {
		t.Fatalf("saved LastSeenIPv6 = %q, want previous retained", store.savedAgent.LastSeenIPv6)
	}

	// Second cycle: only IPv6 reported this time; IPv4 must persist.
	if _, err := svc.Heartbeat(context.Background(), HeartbeatRequest{
		CurrentRevision: 1,
		LastSeenIPv6:    "2001:db8::1",
	}, "token-remote-ddns"); err != nil {
		t.Fatalf("Heartbeat() second error = %v", err)
	}
	if store.savedAgent.LastSeenIPv4 != "203.0.113.42" {
		t.Fatalf("saved LastSeenIPv4 = %q, want previous retained", store.savedAgent.LastSeenIPv4)
	}
	if store.savedAgent.LastSeenIPv6 != "2001:db8::1" {
		t.Fatalf("saved LastSeenIPv6 = %q, want non-empty overwrite", store.savedAgent.LastSeenIPv6)
	}
}

func TestAgentServiceHeartbeatWithNilDDNSReconcilerReturnsNormally(t *testing.T) {
	t.Parallel()
	store := &fakeStore{
		agents: []storage.AgentRow{{
			ID:              "remote-ddns",
			Name:            "remote-ddns",
			AgentToken:      "token-remote-ddns",
			DesiredRevision: 2,
			CurrentRevision: 1,
			LastApplyStatus: "success",
		}},
		snapshot: storage.Snapshot{DesiredVersion: "3.0.0", Revision: 2},
	}
	svc := NewAgentService(config.Config{}, store)
	if svc.ddnsReconciler != nil {
		t.Fatalf("default ddnsReconciler = %v, want nil until injected", svc.ddnsReconciler)
	}

	reply, err := svc.Heartbeat(context.Background(), HeartbeatRequest{CurrentRevision: 1}, "token-remote-ddns")
	if err != nil {
		t.Fatalf("Heartbeat() error = %v", err)
	}
	if reply.DesiredRevision != 2 {
		t.Fatalf("reply DesiredRevision = %d, want 2", reply.DesiredRevision)
	}
}

func TestAgentServiceHeartbeatInvokesAndSurvivesPanickingDDNSReconciler(t *testing.T) {
	t.Parallel()
	store := &fakeStore{
		agents: []storage.AgentRow{{
			ID:              "remote-ddns",
			Name:            "remote-ddns",
			AgentToken:      "token-remote-ddns",
			DesiredRevision: 2,
			CurrentRevision: 1,
			LastApplyStatus: "success",
		}},
		snapshot: storage.Snapshot{DesiredVersion: "3.0.0", Revision: 2},
	}
	reconciler := &fakeDDNSReconciler{panicOn: true}
	svc := NewAgentService(config.Config{}, store)
	svc.SetDDNSReconciler(reconciler)

	reply, err := svc.Heartbeat(context.Background(), HeartbeatRequest{
		CurrentRevision: 1,
		LastSeenIPv4:    "203.0.113.99",
	}, "token-remote-ddns")
	if err != nil {
		t.Fatalf("Heartbeat() error = %v after reconciler panic (must not break main path)", err)
	}
	if reply.DesiredRevision != 2 {
		t.Fatalf("reply DesiredRevision = %d, want 2", reply.DesiredRevision)
	}
	if len(reconciler.calledIDs) != 1 || reconciler.calledIDs[0] != "remote-ddns" {
		t.Fatalf("reconciler calledIDs = %+v, want [remote-ddns]", reconciler.calledIDs)
	}
}

func TestAgentServiceUpdateAppliesDDNSConfigAndBumpsRevision(t *testing.T) {
	t.Parallel()
	store := &fakeStore{
		agents: []storage.AgentRow{{
			ID:               "edge-ddns",
			Name:             "Edge DDNS",
			AgentToken:       "token-ddns",
			CapabilitiesJSON: `["http_rules"]`,
			DesiredRevision:  7,
			CurrentRevision:  7,
			LastApplyStatus:  "success",
		}},
		// summaryForRow derives DdnsDomain from the dispatched snapshot config.
		snapshot: storage.Snapshot{DDNSConfig: &storage.DDNSConfig{Domain: "edge.example.com"}},
	}
	svc := NewAgentService(config.Config{}, store)

	ddns := &storage.DDNSConfig{
		Domain: "edge.example.com",
		IPv4:   storage.DDNSFamily{Enabled: true, Source: "public_api"},
		IPv6:   storage.DDNSFamily{Enabled: true, Source: "interface", Interface: "eth0"},
	}
	agent, err := svc.Update(context.Background(), "edge-ddns", UpdateAgentRequest{DdnsConfig: ddns})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if !strings.Contains(store.savedAgent.DdnsConfigJSON, "edge.example.com") {
		t.Fatalf("saved DdnsConfigJSON = %q, want domain persisted", store.savedAgent.DdnsConfigJSON)
	}
	// R7: no credential may ever live in the DDNS config column.
	if strings.Contains(strings.ToLower(store.savedAgent.DdnsConfigJSON), "token") {
		t.Fatalf("DdnsConfigJSON leaked credential-like key: %q", store.savedAgent.DdnsConfigJSON)
	}
	assertRevisionAboveFloor(t, "saved DesiredRevision", store.savedAgent.DesiredRevision, 7)
	if agent.DdnsDomain != "edge.example.com" {
		t.Fatalf("summary DdnsDomain = %q, want edge.example.com", agent.DdnsDomain)
	}
	// The full dispatched config is exposed on the read path so the edit form can
	// seed family state instead of opening empty and clobbering the config (R7:
	// DDNSConfig carries no credential).
	if agent.DdnsConfig == nil || agent.DdnsConfig.Domain != "edge.example.com" {
		t.Fatalf("summary DdnsConfig = %+v, want domain edge.example.com", agent.DdnsConfig)
	}
}

func TestAgentServiceUpdateDDNSConfigParticipatesInRevisionIdempotency(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	store, err := newServiceTestSQLiteStore(t, t.TempDir(), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.SaveAgent(ctx, storage.AgentRow{
		ID: "edge-ddns-revision", Name: "Edge DDNS Revision", AgentToken: "token-ddns-revision",
		CapabilitiesJSON: `[]`, DesiredRevision: 1, CurrentRevision: 1,
		LastApplyRevision: 1, LastApplyStatus: "success",
	}); err != nil {
		t.Fatalf("SaveAgent() error = %v", err)
	}

	svc := NewAgentService(config.Config{}, store)
	firstCtx, _ := revision.WithMutationContext(ctx, revision.MutationContextOptions{IdempotencyKey: "ddns-config-update"})
	firstConfig := &storage.DDNSConfig{
		Domain: "first.example.com",
		IPv4:   storage.DDNSFamily{Enabled: true, Source: "public_api"},
	}
	updated, err := svc.Update(firstCtx, "edge-ddns-revision", UpdateAgentRequest{DdnsConfig: firstConfig})
	if err != nil {
		t.Fatalf("Update(first) error = %v", err)
	}
	if updated.DdnsConfig == nil || updated.DdnsConfig.Domain != firstConfig.Domain {
		t.Fatalf("Update(first) DDNS config = %+v", updated.DdnsConfig)
	}

	revisions, err := store.ListAgentRevisions(ctx, "edge-ddns-revision")
	if err != nil {
		t.Fatalf("ListAgentRevisions() error = %v", err)
	}
	if len(revisions) != 1 {
		t.Fatalf("revisions = %+v, want one DDNS revision", revisions)
	}
	artifact, found, err := store.GetGenerationArtifact(ctx, revisions[0].SnapshotArtifactID)
	if err != nil || !found {
		t.Fatalf("GetGenerationArtifact() found=%v error=%v", found, err)
	}
	var snapshot storage.Snapshot
	if err := json.Unmarshal(artifact.Payload, &snapshot); err != nil {
		t.Fatalf("decode snapshot: %v", err)
	}
	if snapshot.DDNSConfig == nil || snapshot.DDNSConfig.Domain != firstConfig.Domain {
		t.Fatalf("revision snapshot DDNS config = %+v", snapshot.DDNSConfig)
	}

	secondCtx, _ := revision.WithMutationContext(ctx, revision.MutationContextOptions{IdempotencyKey: "ddns-config-update"})
	secondConfig := &storage.DDNSConfig{
		Domain: "second.example.com",
		IPv4:   storage.DDNSFamily{Enabled: true, Source: "public_api"},
	}
	_, err = svc.Update(secondCtx, "edge-ddns-revision", UpdateAgentRequest{DdnsConfig: secondConfig})
	if revision.ErrorCodeOf(err) != revision.ErrorCodeConflict {
		t.Fatalf("Update(second) error = %v, code = %q, want conflict", err, revision.ErrorCodeOf(err))
	}
}

func TestAgentServiceUpdateLeavesDDNSConfigUntouchedWhenOmitted(t *testing.T) {
	t.Parallel()
	storedConfig := `{"domain":"keep.example.com","ipv4":{"enabled":true,"source":"public_api"}}`
	store := &fakeStore{
		agents: []storage.AgentRow{{
			ID:               "edge-ddns",
			Name:             "Edge DDNS",
			AgentToken:       "token-ddns",
			CapabilitiesJSON: `["http_rules"]`,
			DdnsConfigJSON:   storedConfig,
			DesiredRevision:  9,
			CurrentRevision:  9,
			LastApplyStatus:  "success",
		}},
	}
	svc := NewAgentService(config.Config{}, store)

	renamed := "Edge Renamed"
	if _, err := svc.Update(context.Background(), "edge-ddns", UpdateAgentRequest{Name: &renamed}); err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if store.savedAgent.DdnsConfigJSON != storedConfig {
		t.Fatalf("saved DdnsConfigJSON = %q, want untouched when DdnsConfig is nil", store.savedAgent.DdnsConfigJSON)
	}
	if store.savedAgent.DesiredRevision != 9 {
		t.Fatalf("saved DesiredRevision = %d, want 9 (no bump when ddns config omitted)", store.savedAgent.DesiredRevision)
	}
}

// TestAgentSummaryJSONCarriesNoCredential verifies the AgentSummary wire shape
// that redactAgentSummary operates on never exposes a token/secret key — the
// precondition that lets the handler redact only the proxy password (R7). The
// full dispatched ddns_config is exposed so the edit form can round-trip family
// state; it must carry only domain + per-family {enabled,source,interface}.
func TestAgentSummaryJSONCarriesNoCredential(t *testing.T) {
	t.Parallel()
	summary := AgentSummary{
		ID:           "edge-ddns",
		Name:         "Edge DDNS",
		LastSeenIPv4: "203.0.113.9",
		LastSeenIPv6: "2001:db8::1",
		DdnsDomain:   "edge.example.com",
		DdnsStatus:   storage.DdnsStatus{Status: "ok", LastResolvedIPv4: "203.0.113.9"},
		DdnsConfig: &storage.DDNSConfig{
			Domain: "edge.example.com",
			IPv4:   storage.DDNSFamily{Enabled: true, Source: "public_api"},
			IPv6:   storage.DDNSFamily{Enabled: true, Source: "interface", Interface: "eth0"},
		},
		OutboundProxyURL: "socks://user:secret@127.0.0.1:1080",
	}
	raw, err := json.Marshal(summary)
	if err != nil {
		t.Fatalf("json.Marshal(summary) error = %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("json.Unmarshal(summary) error = %v", err)
	}
	for _, key := range []string{"agent_token", "token", "register_token", "ddns_token", "cf_token", "api_key", "secret"} {
		if _, ok := payload[key]; ok {
			t.Fatalf("AgentSummary JSON leaked credential key %q: %s", key, raw)
		}
	}
	for _, key := range []string{"last_seen_ipv4", "last_seen_ipv6", "ddns_domain", "ddns_status", "ddns_config"} {
		if _, ok := payload[key]; !ok {
			t.Fatalf("AgentSummary JSON missing DDNS field %q: %s", key, raw)
		}
	}
	// R7: the exposed ddns_config object may carry only domain + family state,
	// never a Cloudflare credential. This is the read path the edit form seeds
	// from, so a leaked credential here would reach the browser.
	configMap, ok := payload["ddns_config"].(map[string]any)
	if !ok {
		t.Fatalf("ddns_config is %T, want map: %s", payload["ddns_config"], raw)
	}
	for key := range configMap {
		switch key {
		case "domain", "ipv4", "ipv6":
		default:
			if matched, _ := regexp.MatchString(`token|secret|api[_-]?key|password|credential`, key); matched {
				t.Fatalf("ddns_config leaked credential-like key %q: %s", key, raw)
			}
		}
	}
}

func TestHTTPRuleJSONOmitsLegacyFields(t *testing.T) {
	t.Parallel()
	raw, err := json.Marshal(HTTPRule{
		ID:          1,
		AgentID:     "local",
		FrontendURL: "https://emby.example.com",
		BackendURL:  "http://legacy:8096",
		Backends:    []HTTPRuleBackend{{URL: "http://emby:8096"}},
		RelayChain:  []int{7},
		RelayLayers: [][]int{{7}},
		Enabled:     true,
	})
	if err != nil {
		t.Fatalf("json.Marshal(HTTPRule) error = %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("json.Unmarshal(HTTPRule) error = %v", err)
	}
	retiredPrefix := "wire" + "guard"
	for _, key := range []string{
		"backend_url",
		"relay_chain",
		retiredPrefix + "_entry_enabled",
		retiredPrefix + "_profile_id",
		retiredPrefix + "_entry_listen_host",
		retiredPrefix + "_entry_listen_port",
	} {
		if _, ok := payload[key]; ok {
			t.Fatalf("HTTPRule JSON exposed legacy field %q: %s", key, raw)
		}
	}
	if _, ok := payload["backends"]; !ok {
		t.Fatalf("HTTPRule JSON missing canonical backends: %s", raw)
	}
	if _, ok := payload["relay_layers"]; !ok {
		t.Fatalf("HTTPRule JSON missing canonical relay_layers: %s", raw)
	}
}

func TestAgentServiceListAndGetNormalizeStoredCapabilities(t *testing.T) {
	t.Parallel()
	retiredCapability := "wire" + "guard"
	store := &fakeStore{
		agents: []storage.AgentRow{{
			ID: "edge-legacy", Name: "Edge Legacy", Platform: "linux-amd64",
			CapabilitiesJSON: fmt.Sprintf(`["http_rules",%q,"l4"]`, retiredCapability),
		}},
		rulesByID:   map[string][]storage.HTTPRuleRow{},
		l4RulesByID: map[string][]storage.L4RuleRow{},
	}
	svc := NewAgentService(config.Config{}, store)

	agents, err := svc.List(t.Context())
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(agents) != 1 || strings.Join(agents[0].Capabilities, ",") != "http_rules,l4" {
		t.Fatalf("List() capabilities = %+v, want [http_rules l4]", agents)
	}

	agent, err := svc.Get(t.Context(), "edge-legacy")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if strings.Join(agent.Capabilities, ",") != "http_rules,l4" {
		t.Fatalf("Get() capabilities = %+v, want [http_rules l4]", agent.Capabilities)
	}
}

func TestAgentServiceApplyRetriesCurrentDesiredForLocalAndRemoteWithoutSynchronousTrigger(t *testing.T) {
	t.Parallel()
	for _, testCase := range []struct {
		name    string
		agentID string
		local   bool
	}{
		{name: "local", agentID: "local", local: true},
		{name: "remote", agentID: "edge-retry"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			ctx := t.Context()
			store, err := newServiceTestSQLiteStore(t, t.TempDir(), "local")
			if err != nil {
				t.Fatalf("NewSQLiteStore() error = %v", err)
			}
			t.Cleanup(func() { _ = store.Close() })
			if !testCase.local {
				if err := store.SaveAgent(ctx, storage.AgentRow{
					ID: testCase.agentID, Name: "Edge Retry", AgentToken: "token-retry",
					CapabilitiesJSON: `["http_rules"]`, DesiredRevision: 4, CurrentRevision: 3,
					LastApplyRevision: 3, LastApplyStatus: "error",
				}); err != nil {
					t.Fatalf("SaveAgent() error = %v", err)
				}
			}
			snapshot := storage.Snapshot{
				Revision: 4, Rules: []storage.HTTPRule{}, L4Rules: []storage.L4Rule{},
				RelayListeners: []storage.RelayListener{},
				EgressProfiles: []storage.EgressProfile{}, Certificates: []storage.ManagedCertificateBundle{},
				CertificatePolicies: []storage.ManagedCertificatePolicy{},
			}
			payload, digest, err := revision.CanonicalSnapshotPayload(snapshot)
			if err != nil {
				t.Fatalf("CanonicalSnapshotPayload() error = %v", err)
			}
			now := time.Date(2026, 7, 12, 22, 0, 0, 0, time.UTC)
			operationID := "apply-retry-" + testCase.name
			artifactID := "snapshot-" + digest
			if err := store.CreateRevisionLedger(ctx, storage.RevisionLedgerWrite{
				Operation: storage.OperationRow{ID: operationID, Kind: "test.seed", Status: storage.OperationStatusPending, PrimaryAgentID: testCase.agentID, CreatedAt: now, UpdatedAt: now},
				Revisions: []storage.AgentRevisionRow{{AgentID: testCase.agentID, Revision: 4, OperationID: operationID, State: storage.AgentRevisionStateFailed, SnapshotArtifactID: artifactID, SnapshotDigest: digest, AttemptCount: 5, CreatedAt: now, UpdatedAt: now}},
				Pointers:  []storage.AgentRevisionPointerRow{{AgentID: testCase.agentID, DesiredRevision: 4, AppliedRevision: 3, LastKnownGoodRevision: 3, UpdatedAt: now}},
				Artifacts: []storage.GenerationArtifactRow{{ID: artifactID, Kind: "agent_snapshot", SHA256: digest, Payload: payload, SizeBytes: int64(len(payload)), CreatedAt: now}},
			}); err != nil {
				t.Fatalf("CreateRevisionLedger() error = %v", err)
			}

			svc := NewAgentService(config.Config{EnableLocalAgent: true, LocalAgentID: "local", LocalAgentName: "Local"}, store)
			triggerCalls := 0
			svc.SetLocalApplyTrigger(func(context.Context) error {
				triggerCalls++
				return errors.New("synchronous trigger must not run")
			})
			result, err := svc.Apply(ctx, testCase.agentID)
			if err != nil {
				t.Fatalf("Apply() error = %v", err)
			}
			if !result.Pending || result.DesiredRevision != 4 {
				t.Fatalf("Apply() = %+v, want asynchronous pending retry", result)
			}
			if triggerCalls != 0 {
				t.Fatalf("triggerCalls = %d, want 0", triggerCalls)
			}
			retried, found, err := store.GetCoordinatorRevision(ctx, testCase.agentID, 4)
			if err != nil || !found {
				t.Fatalf("GetCoordinatorRevision() found=%v error=%v", found, err)
			}
			if retried.State != storage.AgentRevisionStatePending || retried.RetryCycle != 1 || retried.AttemptCount != 0 {
				t.Fatalf("retried revision = %+v", retried)
			}
		})
	}
}
