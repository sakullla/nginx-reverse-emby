package service

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

func TestConfigMutationRevisionTimeoutsPersistGlobalAndPerAgent(t *testing.T) {
	t.Parallel()
	ctx := authenticatedServiceMutationContext(t)
	store := newMutationTimeoutStore(t, "config-mutation")
	if err := store.SaveAgent(ctx, storage.AgentRow{
		ID: "edge-a", Name: "Edge A", AgentToken: "token-edge-a", CapabilitiesJSON: `["http_rules"]`,
	}); err != nil {
		t.Fatalf("SaveAgent() error = %v", err)
	}
	cfg := mutationTimeoutConfig()
	svc := NewRuleService(cfg, store)

	if _, err := svc.Create(ctx, "local", HTTPRuleInput{
		FrontendURL: mutationTimeoutString("http://local.example.test"),
		Backends:    &[]HTTPRuleBackend{{URL: "http://127.0.0.1:8096"}},
	}); err != nil {
		t.Fatalf("Create(local) error = %v", err)
	}
	if _, err := svc.Create(ctx, "edge-a", HTTPRuleInput{
		FrontendURL: mutationTimeoutString("http://edge-a.example.test"),
		Backends:    &[]HTTPRuleBackend{{URL: "http://127.0.0.1:8097"}},
	}); err != nil {
		t.Fatalf("Create(edge-a) error = %v", err)
	}

	assertLatestMutationTimeouts(t, store, "local", 120, 900)
	assertLatestMutationTimeouts(t, store, "edge-a", 45, 720)

	// Configuration is process state, but timeout values are immutable revision data.
	cfg.RevisionCoordinator.ApplyTimeout = time.Second
	cfg.RevisionCoordinator.AgentTimeoutOverrides["local"] = config.RevisionAgentTimeoutOverride{
		ApplyTimeout: time.Second,
		DrainTimeout: time.Second,
	}
	assertLatestMutationTimeouts(t, store, "local", 120, 900)
}

func TestAgentSettingsMutationRevisionTimeoutsPersistOverrides(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := newMutationTimeoutStore(t, "agent-settings")
	if err := store.SaveAgent(ctx, storage.AgentRow{
		ID: "edge-settings", Name: "Edge Settings", AgentToken: "token-edge-settings",
		CapabilitiesJSON: `["http_rules"]`, CurrentRevision: 1, DesiredRevision: 1,
	}); err != nil {
		t.Fatalf("SaveAgent() error = %v", err)
	}
	cfg := mutationTimeoutConfig()
	cfg.RevisionCoordinator.AgentTimeoutOverrides["edge-settings"] = config.RevisionAgentTimeoutOverride{
		ApplyTimeout: 75 * time.Second,
	}
	svc := NewAgentService(cfg, store)

	if _, err := svc.Update(ctx, "edge-settings", UpdateAgentRequest{
		OutboundProxyURL: mutationTimeoutString("socks://127.0.0.1:1080"),
	}); err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	assertLatestMutationTimeouts(t, store, "edge-settings", 75, 420)
}

func TestBackupMutationRevisionTimeoutsPersistOverrides(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	source := newMutationTimeoutStore(t, "backup-source")
	if err := source.SaveAgent(ctx, storage.AgentRow{
		ID: "edge-backup", Name: "Edge Backup", AgentToken: "token-edge-backup",
		CapabilitiesJSON: `["http_rules"]`,
	}); err != nil {
		t.Fatalf("SaveAgent(source) error = %v", err)
	}
	if err := source.SaveHTTPRules(ctx, "edge-backup", []storage.HTTPRuleRow{{
		ID: 1, AgentID: "edge-backup", FrontendURL: "http://backup.example.test",
		BackendsJSON: `[{"url":"http://127.0.0.1:8096"}]`, LoadBalancingJSON: `{}`,
		Enabled: true, TagsJSON: `[]`, RelayChainJSON: `[]`, RelayLayersJSON: `[]`, CustomHeadersJSON: `[]`,
	}}); err != nil {
		t.Fatalf("SaveHTTPRules(source) error = %v", err)
	}
	archive, _, err := NewBackupService(config.Config{}, source).Export(ctx)
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}

	target := newMutationTimeoutStore(t, "backup-target")
	cfg := mutationTimeoutConfig()
	cfg.RevisionCoordinator.AgentTimeoutOverrides["edge-backup"] = config.RevisionAgentTimeoutOverride{
		DrainTimeout: 11 * time.Minute,
	}
	if _, err := NewBackupService(cfg, target).Import(ctx, archive); err != nil {
		t.Fatalf("Import() error = %v", err)
	}

	assertLatestMutationTimeouts(t, target, "edge-backup", 45, 660)
}

func mutationTimeoutConfig() config.Config {
	return config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
		LocalAgentName:   "Local",
		RevisionCoordinator: config.RevisionCoordinatorConfig{
			ApplyTimeout: 45 * time.Second,
			DrainTimeout: 7 * time.Minute,
			AgentTimeoutOverrides: map[string]config.RevisionAgentTimeoutOverride{
				"local": {
					ApplyTimeout: 2 * time.Minute,
					DrainTimeout: 15 * time.Minute,
				},
				"edge-a": {
					DrainTimeout: 12 * time.Minute,
				},
			},
		},
	}
}

func newMutationTimeoutStore(t *testing.T, name string) *storage.GormStore {
	t.Helper()
	store, err := newServiceTestSQLiteStore(t, filepath.Join(t.TempDir(), name), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func assertLatestMutationTimeouts(t *testing.T, store *storage.GormStore, agentID string, apply, drain int) {
	t.Helper()
	revisions, err := store.ListAgentRevisions(context.Background(), agentID)
	if err != nil {
		t.Fatalf("ListAgentRevisions(%q) error = %v", agentID, err)
	}
	if len(revisions) == 0 {
		t.Fatalf("ListAgentRevisions(%q) = empty", agentID)
	}
	latest := revisions[len(revisions)-1]
	if latest.ApplyTimeoutSeconds != apply || latest.DrainTimeoutSeconds != drain {
		t.Fatalf("revision %s/%d timeouts = (%d,%d), want (%d,%d)", agentID, latest.Revision, latest.ApplyTimeoutSeconds, latest.DrainTimeoutSeconds, apply, drain)
	}
}

func mutationTimeoutString(value string) *string { return &value }
