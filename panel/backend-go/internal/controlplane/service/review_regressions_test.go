package service

import (
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/plugins"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

func TestApplyPageRejectsOverflowingPage(t *testing.T) {
	items, meta := ApplyPage([]int{1, 2, 3}, ListQuery{Page: math.MaxInt, PageSize: MaxListPageSize})
	if len(items) != 0 || meta.Page != math.MaxInt {
		t.Fatalf("ApplyPage() = %v, %+v", items, meta)
	}
}

func TestLocalAgentHasNoPublicCredentialAndSummaryIncludesDDNS(t *testing.T) {
	t.Parallel()
	store, err := newServiceTestSQLiteStoreForAllTiers(t, t.TempDir(), "local")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.SaveAgent(t.Context(), storage.AgentRow{
		ID: "local", Name: "Local", AgentToken: "local", Mode: "local", IsLocal: true,
		DdnsConfigJSON: `{"enabled":true,"domain":"media.example.com","ipv4":{"enabled":true,"source":"public_api"}}`,
		DdnsStatusJSON: `{"status":"ok","last_resolved_ipv4":"203.0.113.20"}`,
		LastSeenIPv4:   "203.0.113.20",
	}); err != nil {
		t.Fatal(err)
	}
	svc := NewAgentService(config.Config{
		EnableLocalAgent: true, LocalAgentID: "local", LocalAgentName: "Local", AppVersion: "v1.4.1",
	}, store)
	if err := svc.EnsureLocalAgentBuild(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.GetByToken(t.Context(), "local"); !errors.Is(err, ErrAgentUnauthorized) {
		t.Fatalf("GetByToken(local) error = %v, want unauthorized", err)
	}
	summary, err := svc.Get(t.Context(), "local")
	if err != nil {
		t.Fatal(err)
	}
	if summary.DdnsConfig == nil || summary.DdnsDomain != "media.example.com" ||
		summary.DdnsStatus.Status != "ok" || summary.LastSeenIPv4 != "203.0.113.20" || summary.Version != "1.4.1" {
		t.Fatalf("local DDNS summary = %+v", summary)
	}
	if err := newPluginTestService(store).validateAgentTargets(t.Context(), ">=1.4.0", json.RawMessage(`["local"]`)); err != nil {
		t.Fatalf("stale legacy local agent row overrode embedded build compatibility: %v", err)
	}
	if row := localAgentSettingsRow(config.Config{LocalAgentID: "local"}, storage.LocalAgentStateRow{}); row.AgentToken != "" {
		t.Fatalf("local fallback token = %q, want empty", row.AgentToken)
	}
}

func TestDisabledEmbeddedAgentRejectsStaleLocalPluginTarget(t *testing.T) {
	dataRoot := t.TempDir()
	ctx := WithSystemMutationPrincipal(t.Context(), "test")
	store, err := storage.NewSQLiteStore(dataRoot, "local")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := store.CreateResourceGroup(ctx, storage.ResourceGroupRow{ID: "default", Name: "Default", Builtin: true, CreatedAt: now, UpdatedAt: now}); err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	if err := store.SaveAgent(ctx, storage.AgentRow{ID: "local", Version: "0.1.0", CapabilitiesJSON: `[]`, IsLocal: true, Mode: "local"}); err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	enabled := NewAgentService(config.Config{EnableLocalAgent: true, LocalAgentID: "local", AppVersion: "v1.4.1"}, store)
	if err := enabled.EnsureLocalAgentBuild(ctx); err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	cleanup := plugins.CleanupPolicy{Instances: "retain", Config: "retain", OwnedData: "retain", Grants: "retain", SharedRefs: "retain", AuditEvents: "retain"}
	candidate := pluginCandidateFixture(t, "official.local-presence", "1.0.0", []string{"http.inspect"}, cleanup)
	installed, err := newPluginTestService(store).Install(ctx, PluginInstallRequest{Package: candidate, ActorID: "admin", ConfirmedPermissions: []string{"http.inspect"}})
	if err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := storage.NewSQLiteStore(dataRoot, "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	disabled := NewAgentService(config.Config{EnableLocalAgent: false, LocalAgentID: "local", AppVersion: "v1.4.1"}, reopened)
	if err := disabled.EnsureLocalAgentBuild(ctx); err != nil {
		t.Fatal(err)
	}
	pluginService := newPluginTestService(reopened)
	if _, err := pluginService.Configure(ctx, PluginConfigureRequest{PluginID: installed.PluginID, InstanceID: "local-instance", ResourceGroupID: "default", Targets: []string{"local"}, Config: json.RawMessage(`{"mode":"observe"}`), ActorID: "admin"}); err == nil || !strings.Contains(err.Error(), "unavailable") {
		t.Fatalf("disabled embedded local target error = %v", err)
	}
	current, ok, err := reopened.GetInstalledPlugin(ctx, installed.PluginID)
	if err != nil || !ok {
		t.Fatalf("load plugin after rejected local target = %v, %v", ok, err)
	}
	if current.PendingOperationID != "" || current.PendingRevision != 0 {
		t.Fatalf("rejected disabled local target created pending operation: %+v", current)
	}
	if _, exists, err := reopened.GetPluginInstance(ctx, "local-instance"); err != nil || exists {
		t.Fatalf("rejected disabled local target persisted instance: %v, %v", exists, err)
	}
}

func TestUnchangedDDNSConfigDoesNotRequireRevision(t *testing.T) {
	current := `{"domain":"media.example.com","ipv4":{"enabled":true,"source":"public_api"}}`
	next := &storage.DDNSConfig{
		Enabled: true, Domain: "media.example.com",
		IPv4: storage.DDNSFamily{Enabled: true, Source: "public_api"},
	}
	_, changed := updatedDDNSConfigJSON(current, next)
	if changed {
		t.Fatal("semantically unchanged legacy DDNS config was treated as changed")
	}
}

func TestVersionPolicyPreservesManifestMetadata(t *testing.T) {
	packages := normalizeVersionPackages([]VersionPackage{{
		Platform: " linux-amd64 ", URL: " https://example.test/nre-agent ", SHA256: " digest ",
		Filename: " nre-agent-linux-amd64 ", Size: 123,
	}})
	if len(packages) != 1 || packages[0].Filename != "nre-agent-linux-amd64" || packages[0].Size != 123 {
		t.Fatalf("normalized packages = %+v", packages)
	}
}

func TestBundledPackageBootstrapsLegacyLinuxAgents(t *testing.T) {
	assetRoot := t.TempDir()
	for _, platform := range []string{"linux-amd64", "darwin-arm64"} {
		if err := os.WriteFile(filepath.Join(assetRoot, "nre-agent-"+platform), []byte(platform), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	svc := NewAgentService(config.Config{PublicAgentAssetsDir: assetRoot}, nil)
	if pkg := svc.resolveDesiredPackage(nil, "linux-amd64"); pkg == nil {
		t.Fatal("legacy Linux agent did not receive a bootstrap package")
	}
	explicit := &storage.VersionPackage{URL: "https://example.test/nre-agent", SHA256: "explicit"}
	if pkg := svc.resolveDesiredPackage(explicit, "linux-arm64"); pkg == nil || pkg.SHA256 != explicit.SHA256 {
		t.Fatalf("legacy Linux agent explicit package = %+v", pkg)
	}
	if pkg := svc.resolveDesiredPackage(nil, "darwin-arm64"); pkg != nil {
		t.Fatalf("package dispatched to unsupported Darwin updater: %+v", pkg)
	}
	if pkg := svc.resolveDesiredPackage(&storage.VersionPackage{SHA256: "explicit"}, "darwin-arm64"); pkg != nil {
		t.Fatalf("explicit package dispatched to unsupported Darwin updater: %+v", pkg)
	}
}

func TestHeartbeatBootstrapsAgentWithoutPackageManifestCapability(t *testing.T) {
	t.Parallel()
	store, err := newServiceTestSQLiteStoreForAllTiers(t, t.TempDir(), "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.SaveAgent(t.Context(), storage.AgentRow{
		ID: "edge-legacy", Name: "Legacy Edge", AgentToken: "legacy-token", Mode: "pull", LastApplyStatus: "success",
	}); err != nil {
		t.Fatal(err)
	}
	assetRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(assetRoot, "nre-agent-linux-amd64"), []byte("agent"), 0o755); err != nil {
		t.Fatal(err)
	}
	svc := NewAgentService(config.Config{PublicAgentAssetsDir: assetRoot}, store)
	reply, err := svc.Heartbeat(t.Context(), HeartbeatRequest{
		Platform: "linux-amd64", Capabilities: []string{"http_rules"}, HasCapabilities: true,
	}, "legacy-token")
	if err != nil {
		t.Fatal(err)
	}
	if reply.VersionPackageMeta == nil || strings.TrimSpace(reply.VersionPackage) == "" {
		t.Fatalf("legacy heartbeat package = %q metadata=%+v", reply.VersionPackage, reply.VersionPackageMeta)
	}
}

func TestHeartbeatPreservesPackageManifestCapability(t *testing.T) {
	t.Parallel()
	store, err := newServiceTestSQLiteStoreForAllTiers(t, t.TempDir(), "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.SaveAgent(t.Context(), storage.AgentRow{
		ID: "edge-1", Name: "Edge", AgentToken: "token", Mode: "pull", LastApplyStatus: "success",
	}); err != nil {
		t.Fatal(err)
	}

	assetRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(assetRoot, "nre-agent-linux-amd64"), []byte("agent"), 0o755); err != nil {
		t.Fatal(err)
	}
	svc := NewAgentService(config.Config{PublicAgentAssetsDir: assetRoot}, store)
	reply, err := svc.Heartbeat(t.Context(), HeartbeatRequest{
		Platform: "linux-amd64", Capabilities: []string{packageManifestCapability}, HasCapabilities: true,
	}, "token")
	if err != nil {
		t.Fatal(err)
	}
	if reply.VersionPackageMeta == nil {
		t.Fatal("heartbeat did not receive bundled package metadata")
	}

	rows, err := store.ListAgents(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || !containsString(parseStringArray(rows[0].CapabilitiesJSON), packageManifestCapability) {
		t.Fatalf("stored capabilities = %q", rows[0].CapabilitiesJSON)
	}
}

func TestValidateSnapshotDDNSRejectsIncompleteEnabledFamilies(t *testing.T) {
	tests := []struct {
		name    string
		config  *storage.DDNSConfig
		wantErr bool
	}{
		{
			name: "blank IPv4 interface",
			config: &storage.DDNSConfig{Enabled: true, IPv4: storage.DDNSFamily{
				Enabled: true, Source: "interface", Interface: " ",
			}},
			wantErr: true,
		},
		{
			name: "blank IPv6 interface",
			config: &storage.DDNSConfig{Enabled: true, IPv6: storage.DDNSFamily{
				Enabled: true, Source: "interface",
			}},
			wantErr: true,
		},
		{
			name: "invalid source",
			config: &storage.DDNSConfig{Enabled: true, IPv4: storage.DDNSFamily{
				Enabled: true, Source: "hostname",
			}},
			wantErr: true,
		},
		{
			name: "legacy public source",
			config: &storage.DDNSConfig{Enabled: true, IPv4: storage.DDNSFamily{
				Enabled: true,
			}},
		},
		{
			name: "named interface",
			config: &storage.DDNSConfig{Enabled: true, IPv6: storage.DDNSFamily{
				Enabled: true, Source: "interface", Interface: "eth0",
			}},
		},
		{
			name: "disabled config preserves incomplete family",
			config: &storage.DDNSConfig{IPv4: storage.DDNSFamily{
				Enabled: true, Source: "interface",
			}},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateSnapshotDDNS(test.config)
			if (err != nil) != test.wantErr {
				t.Fatalf("validateSnapshotDDNS() error = %v, wantErr %v", err, test.wantErr)
			}
		})
	}
}
