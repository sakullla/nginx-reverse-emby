package http

import (
	"context"
	"encoding/json"
	"errors"
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
	ctx = marketplace.WithCredentialAuthorization(ctx, marketplace.CredentialAuthorization{SecretID: metadata.ID, ResourceGroupID: metadata.ResourceGroupID, Actor: marketplace.OperationActor{ActorID: "admin", SessionID: "session", CorrelationID: "request"}})
	auth, err := trustedMarketplaceCredentialResolver(vault)(ctx, metadata.ID)
	if err != nil {
		t.Fatal(err)
	}
	basic, ok := auth.(*githttp.BasicAuth)
	if !ok || basic.Password != "private-token" {
		t.Fatalf("resolved auth = %T", auth)
	}
	audits, err := store.ListAuditEvents(ctx, 20)
	if err != nil {
		t.Fatal(err)
	}
	foundUse := false
	for _, audit := range audits {
		if audit.Action == "secret.use" && audit.TargetID == metadata.ID {
			foundUse = true
			if audit.ActorID != "admin" || audit.SessionID != "session" || audit.CorrelationID != "request" || audit.ResourceGroupID != "default" {
				t.Fatalf("market credential use lost provenance: %+v", audit)
			}
		}
	}
	if !foundUse {
		t.Fatal("market credential resolution did not audit secret use")
	}
	schedulerCtx, err := trustedMarketplaceSchedulerContext(vault)(context.Background(), marketplace.Source{ID: "private", CredentialRef: metadata.ID})
	if err != nil {
		t.Fatal(err)
	}
	schedulerAuth, ok := marketplace.CredentialAuthorizationFromContext(schedulerCtx, metadata.ID)
	if !ok || schedulerAuth.ResourceGroupID != metadata.ResourceGroupID || schedulerAuth.Actor.ActorID != "system.marketplace.scheduler" || schedulerAuth.Actor.SessionID != "service" || schedulerAuth.Actor.CorrelationID == "" {
		t.Fatalf("scheduler credential authorization lost trusted provenance: %+v", schedulerAuth)
	}
	if _, err := trustedMarketplaceCredentialResolver(vault)(schedulerCtx, metadata.ID); err != nil {
		t.Fatalf("scheduler could not resolve a private-source credential: %v", err)
	}
	wrongPurpose, err := vault.Create(ctx, secrets.OperationContext{ActorID: "admin", ResourceGroupID: "default"}, "wrong-token", "generic", "do-not-use")
	if err != nil {
		t.Fatal(err)
	}
	wrongContext := marketplace.WithCredentialAuthorization(ctx, marketplace.CredentialAuthorization{SecretID: wrongPurpose.ID, ResourceGroupID: wrongPurpose.ResourceGroupID, Actor: marketplace.OperationActor{ActorID: "admin"}})
	if _, err := trustedMarketplaceCredentialResolver(vault)(wrongContext, wrongPurpose.ID); err == nil {
		t.Fatal("wrong-purpose credential was accepted")
	}
	crossGroup := marketplace.WithCredentialAuthorization(ctx, marketplace.CredentialAuthorization{SecretID: metadata.ID, ResourceGroupID: "other", Actor: marketplace.OperationActor{ActorID: "admin"}})
	if _, err := trustedMarketplaceCredentialResolver(vault)(crossGroup, metadata.ID); err == nil {
		t.Fatal("cross-resource-group credential authorization was accepted")
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

func TestMarketplaceAuthenticatedPrevalidationFailuresInvokeRedactedAudit(t *testing.T) {
	fake := &marketplaceAuditFake{}
	dependencies := Dependencies{MarketplaceService: fake}
	for name, body := range map[string]string{
		"json":       `{"id":"bad"} {"url":"https://example.com"}`,
		"interval":   `{"id":"bad","name":"Bad","url":"https://example.com/plugins.git","reference":"main","refresh_interval":"never"}`,
		"negative":   `{"id":"bad-negative","name":"Bad","url":"https://example.com/plugins.git","reference":"main","refresh_interval":"-1h"}`,
		"credential": `{"id":"private","name":"Private","url":"https://example.com/plugins.git","reference":"main","credential_ref":"secret-ref"}`,
	} {
		t.Run(name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/panel-api/marketplace/sources", strings.NewReader(body))
			response := httptest.NewRecorder()
			dependencies.handleMarketplaceSources(response, request)
			if response.Code == http.StatusOK {
				t.Fatalf("prevalidation failure returned success: %s", response.Body.String())
			}
		})
	}
	want := map[string]bool{"invalid_json": false, "invalid_interval": false, "credential_authorization": false}
	for _, class := range fake.errorClasses {
		if _, ok := want[class]; ok {
			want[class] = true
		}
	}
	for class, found := range want {
		if !found {
			t.Fatalf("prevalidation failure %s was not audited: %+v", class, fake.errorClasses)
		}
	}
}

func TestPluginErrorMapsLeaseContentionAndRedactsInternalFailure(t *testing.T) {
	for name, test := range map[string]struct {
		err    error
		status int
	}{
		"authorization": {service.ErrPluginResourceAuthorization, http.StatusForbidden},
		"validation":    {&plugins.ValidationError{Code: "schema", Err: errors.New("invalid schema")}, http.StatusUnprocessableEntity},
		"quota":         {storage.ErrQuotaExceeded, http.StatusConflict},
	} {
		t.Run(name, func(t *testing.T) {
			response := httptest.NewRecorder()
			writePluginError(response, test.err)
			if response.Code != test.status {
				t.Fatalf("status = %d, want %d", response.Code, test.status)
			}
		})
	}
	response := httptest.NewRecorder()
	writePluginError(response, marketplace.ErrRefreshLeaseHeld)
	if response.Code != http.StatusConflict {
		t.Fatalf("lease contention status = %d", response.Code)
	}
	response = httptest.NewRecorder()
	writePluginError(response, errors.New("database password leaked"))
	if response.Code != http.StatusInternalServerError || strings.Contains(response.Body.String(), "password") {
		t.Fatalf("internal error response = %d %s", response.Code, response.Body.String())
	}
}

type marketplaceAuditFake struct{ errorClasses []string }

func (f *marketplaceAuditFake) ListSources(context.Context) ([]marketplace.Source, error) {
	return nil, nil
}
func (f *marketplaceAuditFake) Source(context.Context, string) (marketplace.Source, error) {
	return marketplace.Source{}, service.ErrMarketplaceSourceNotFound
}
func (f *marketplaceAuditFake) CurrentCatalog(context.Context, string) (service.MarketplaceCatalog, error) {
	return service.MarketplaceCatalog{}, nil
}
func (f *marketplaceAuditFake) AddCustomSource(context.Context, string, string, string, string, string, time.Duration) (marketplace.Source, error) {
	return marketplace.Source{}, nil
}
func (f *marketplaceAuditFake) DeleteSource(context.Context, string) error { return nil }
func (f *marketplaceAuditFake) Refresh(context.Context, string) (marketplace.Snapshot, error) {
	return marketplace.Snapshot{}, nil
}
func (f *marketplaceAuditFake) ResolvePackage(context.Context, string, string, string, string) (service.PluginPackageCandidate, error) {
	return service.PluginPackageCandidate{}, nil
}
func (f *marketplaceAuditFake) AuditSourceFailure(_ context.Context, _, _, errorClass string) error {
	f.errorClasses = append(f.errorClasses, errorClass)
	return nil
}
