package service

import (
	"errors"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

func TestApplyPageRejectsOverflowingPage(t *testing.T) {
	items, meta := ApplyPage([]int{1, 2, 3}, ListQuery{Page: math.MaxInt, PageSize: MaxListPageSize})
	if len(items) != 0 || meta.Page != math.MaxInt {
		t.Fatalf("ApplyPage() = %v, %+v", items, meta)
	}
}

func TestLocalAgentHasNoPublicCredentialAndSummaryIncludesDDNS(t *testing.T) {
	store, err := storage.NewSQLiteStore(t.TempDir(), "local")
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
		EnableLocalAgent: true, LocalAgentID: "local", LocalAgentName: "Local",
	}, store)
	if _, err := svc.GetByToken(t.Context(), "local"); !errors.Is(err, ErrAgentUnauthorized) {
		t.Fatalf("GetByToken(local) error = %v, want unauthorized", err)
	}
	summary, err := svc.Get(t.Context(), "local")
	if err != nil {
		t.Fatal(err)
	}
	if summary.DdnsConfig == nil || summary.DdnsDomain != "media.example.com" ||
		summary.DdnsStatus.Status != "ok" || summary.LastSeenIPv4 != "203.0.113.20" {
		t.Fatalf("local DDNS summary = %+v", summary)
	}
	if row := localAgentSettingsRow(config.Config{LocalAgentID: "local"}, storage.LocalAgentStateRow{}); row.AgentToken != "" {
		t.Fatalf("local fallback token = %q, want empty", row.AgentToken)
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

func TestBundledPackageRequiresSupportedAgentCapability(t *testing.T) {
	assetRoot := t.TempDir()
	for _, platform := range []string{"linux-amd64", "darwin-arm64"} {
		if err := os.WriteFile(filepath.Join(assetRoot, "nre-agent-"+platform), []byte(platform), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	svc := NewAgentService(config.Config{PublicAgentAssetsDir: assetRoot}, nil)
	if pkg := svc.resolveDesiredPackage(nil, "linux-amd64", nil); pkg != nil {
		t.Fatalf("package dispatched without manifest capability: %+v", pkg)
	}
	if pkg := svc.resolveDesiredPackage(nil, "darwin-arm64", []string{"package_manifest_v1"}); pkg != nil {
		t.Fatalf("package dispatched to unsupported Darwin updater: %+v", pkg)
	}
	if pkg := svc.resolveDesiredPackage(&storage.VersionPackage{SHA256: "explicit"}, "darwin-arm64", []string{"package_manifest_v1"}); pkg != nil {
		t.Fatalf("explicit package dispatched to unsupported Darwin updater: %+v", pkg)
	}
	if pkg := svc.resolveDesiredPackage(nil, "linux-amd64", []string{"package_manifest_v1"}); pkg == nil {
		t.Fatal("supported package-capable Linux agent did not receive bundled package")
	}
}
