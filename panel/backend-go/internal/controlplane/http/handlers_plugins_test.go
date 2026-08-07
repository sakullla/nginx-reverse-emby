package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/authz"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/marketplace"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/plugins"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/secrets"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/service"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

func TestPluginInstallAPIRejectsClientSuppliedCachePathAndManifest(t *testing.T) {
	for name, body := range map[string]string{
		"cache_path": `{"source_id":"official","plugin_id":"official.waf","version":"1.0.0","digest":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","confirmed_permissions":[],"risk_accepted":false,"cache_path":"C:/attacker"}`,
		"manifest":   `{"source_id":"official","plugin_id":"official.waf","version":"1.0.0","digest":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","confirmed_permissions":[],"risk_accepted":false,"manifest":{"permissions":[]}}`,
	} {
		t.Run(name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/panel-api/plugins/install", strings.NewReader(body))
			response := httptest.NewRecorder()
			Dependencies{}.handlePluginInstall(response, request)
			if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "unknown field") {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
}

func TestMarketplaceAndPluginAPIDTOsHideInternalPathsAndUseStableFields(t *testing.T) {
	catalog := service.MarketplaceCatalog{
		Source:   marketplace.Source{ID: "community", Kind: marketplace.SourceKindCustom, RiskLabel: marketplace.UntrustedRiskLabel, CredentialRef: "secret-ref"},
		Snapshot: marketplace.Snapshot{ID: "snapshot-1", SourceID: "community", Commit: "commit-1", Path: `C:\panel\data\marketplace\snapshots\secret`, ValidatedAt: time.Unix(1, 0).UTC(), Entries: []plugins.MarketEntry{{ID: "example.plugin", Version: "1.0.0", PackageSHA256: strings.Repeat("a", 64)}}},
	}
	encoded, err := json.Marshal(catalog)
	if err != nil {
		t.Fatal(err)
	}
	body := string(encoded)
	for _, expected := range []string{`"source_id":"community"`, `"risk_label":"` + marketplace.UntrustedRiskLabel + `"`, `"commit":"commit-1"`, `"id":"example.plugin"`, `"sha256":"` + strings.Repeat("a", 64) + `"`} {
		if !strings.Contains(body, expected) {
			t.Fatalf("catalog response %s lacks %s", body, expected)
		}
	}
	for _, forbidden := range []string{"C:\\panel", "CachePath", "ManifestJSON", "ConfigSchemaJSON", `"Path"`} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("catalog response leaked %q: %s", forbidden, body)
		}
	}
	pluginBody, err := json.Marshal(storage.InstalledPluginRow{PluginID: "example.plugin", CleanupPolicyJSON: `{"secret":"internal"}`})
	if err != nil || !strings.Contains(string(pluginBody), `"plugin_id":"example.plugin"`) || strings.Contains(string(pluginBody), "CleanupPolicyJSON") || strings.Contains(string(pluginBody), "internal") {
		t.Fatalf("plugin status response = %s, %v", pluginBody, err)
	}
}

func TestPublicPluginAPIRejectsManualCompletion(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/panel-api/plugins/example.plugin/lifecycle-complete", strings.NewReader(`{"applied":true}`))
	request.SetPathValue("id", "example.plugin")
	request.SetPathValue("action", "lifecycle-complete")
	response := httptest.NewRecorder()
	Dependencies{}.handlePluginAction(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("manual completion status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestTrustedMarketplaceCredentialResolverUsesVaultWithoutSourcePlaintext(t *testing.T) {
	ctx := context.Background()
	store, err := storage.NewSQLiteStore(t.TempDir(), "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	vault, err := secrets.NewVault(store, secrets.Keyring{CurrentKeyID: "key", Keys: map[string][]byte{"key": []byte("0123456789abcdef0123456789abcdef")}})
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := vault.Create(ctx, secrets.OperationContext{ActorID: "admin", ResourceGroupID: "default"}, "market-token", "git.marketplace", "private-token")
	if err != nil {
		t.Fatal(err)
	}
	auth, err := trustedMarketplaceCredentialResolver(vault)(ctx, metadata.ID)
	if err != nil {
		t.Fatal(err)
	}
	basic, ok := auth.(*githttp.BasicAuth)
	if !ok || basic.Password != "private-token" {
		t.Fatalf("resolved auth = %T", auth)
	}
	encoded, err := json.Marshal(marketplace.Source{ID: "private", Kind: marketplace.SourceKindCustom, CredentialRef: metadata.ID})
	if err != nil || strings.Contains(string(encoded), "private-token") || !strings.Contains(string(encoded), metadata.ID) {
		t.Fatalf("source JSON = %s, %v", encoded, err)
	}
}

func TestPluginAPIRejectsTrailingJSONValue(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/panel-api/plugins/install", strings.NewReader(`{"source_id":"official"} {"cache_path":"C:/attacker"}`))
	response := httptest.NewRecorder()
	Dependencies{}.handlePluginInstall(response, request)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "multiple JSON values") {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestPluginAndMarketplaceRoutesRequireSystemAdminPermission(t *testing.T) {
	for _, path := range []string{"/panel-api/plugins/official.waf", "/panel-api/plugins/install", "/panel-api/marketplace/sources", "/panel-api/marketplace/sources/official/refresh", "/panel-api/marketplace/sources/official/entries"} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		if permission := requestPermission(request); permission != authz.PermissionSystemAdmin {
			t.Fatalf("%s permission = %q, want %q", path, permission, authz.PermissionSystemAdmin)
		}
	}
}
