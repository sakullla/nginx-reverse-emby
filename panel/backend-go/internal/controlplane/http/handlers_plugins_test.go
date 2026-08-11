package http

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
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

func TestStrictPluginJSONRejectsPayloadBeyondOneMiB(t *testing.T) {
	body := `{"source_id":"official"}` + strings.Repeat(" ", 1<<20) + `{"cache_path":"C:/attacker"}`
	request := httptest.NewRequest(http.MethodPost, "/panel-api/plugins/install", strings.NewReader(body))
	response := httptest.NewRecorder()
	Dependencies{}.handlePluginInstall(response, request)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "exceeds 1 MiB") {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
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

type pluginReadAPIFake struct {
	PluginAPI
	installed      []service.PluginSummary
	detail         service.PluginDetail
	detailErr      error
	preview        service.PluginPackageDetail
	operations     []service.PluginOperationDetail
	mutation       service.PluginSummary
	configured     service.PluginInstanceDetail
	configureErr   error
	configureIn    service.PluginConfigureRequest
	configureCalls int
}

type scopedPluginReadFake struct {
	*pluginReadAPIFake
	actor authz.Actor
}

func (f *scopedPluginReadFake) ListForActor(_ context.Context, actor authz.Actor) ([]service.PluginSummary, error) {
	f.actor = actor
	return []service.PluginSummary{{PluginID: "visible.plugin"}}, nil
}

func (f *scopedPluginReadFake) DetailForActor(_ context.Context, _ string, actor authz.Actor) (service.PluginDetail, error) {
	f.actor = actor
	return service.PluginDetail{Plugin: service.PluginSummary{PluginID: "visible.plugin"}, Instances: []service.PluginInstanceDetail{{ID: "visible", ResourceGroupID: "group-a"}}}, nil
}

func (f *scopedPluginReadFake) OperationsForActor(_ context.Context, _ string, actor authz.Actor) ([]service.PluginOperationDetail, error) {
	f.actor = actor
	return []service.PluginOperationDetail{{ID: "visible-operation", AgentResults: json.RawMessage(`{}`)}}, nil
}

func (f *scopedPluginReadFake) LogsForActor(_ context.Context, _, _, _, _ string, _ int, actor authz.Actor) (service.PluginRuntimeLogPage, error) {
	f.actor = actor
	return service.PluginRuntimeLogPage{Entries: []service.PluginRuntimeLogEntry{{InstanceID: "visible", AgentID: "edge-a", Level: "info", Message: "token=[REDACTED]"}}, NextCursor: "opaque-next"}, nil
}

func TestPluginReadHandlersUseAuthenticatedResourceScopedProjection(t *testing.T) {
	actor := authz.Actor{ID: "member", Permissions: []string{authz.PermissionResourceRead}, VisibleResourceGroups: []string{"group-a"}}
	api := &scopedPluginReadFake{pluginReadAPIFake: &pluginReadAPIFake{installed: []service.PluginSummary{{PluginID: "hidden.plugin"}}}}
	for _, test := range []struct {
		path    string
		handler func(Dependencies, http.ResponseWriter, *http.Request)
		marker  string
	}{
		{"/panel-api/plugins", func(d Dependencies, w http.ResponseWriter, r *http.Request) { d.handlePlugins(w, r) }, "visible.plugin"},
		{"/panel-api/plugins/visible.plugin", func(d Dependencies, w http.ResponseWriter, r *http.Request) {
			r.SetPathValue("id", "visible.plugin")
			d.handlePlugin(w, r)
		}, `"resource_group_id":"group-a"`},
		{"/panel-api/plugins/visible.plugin/operations", func(d Dependencies, w http.ResponseWriter, r *http.Request) {
			r.SetPathValue("id", "visible.plugin")
			d.handlePluginOperations(w, r)
		}, "visible-operation"},
	} {
		request := httptest.NewRequest(http.MethodGet, test.path, nil)
		request = request.WithContext(context.WithValue(request.Context(), actorContextKey{}, actor))
		response := httptest.NewRecorder()
		test.handler(Dependencies{PluginService: api}, response, request)
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), test.marker) || strings.Contains(response.Body.String(), "hidden.plugin") || api.actor.ID != actor.ID {
			t.Fatalf("path=%s status=%d actor=%+v body=%s", test.path, response.Code, api.actor, response.Body.String())
		}
	}
	for _, path := range []string{"/panel-api/plugins", "/panel-api/plugins/p/operations", "/panel-api/plugins/p/instances/i/logs"} {
		if permission := requestPermission(httptest.NewRequest(http.MethodGet, path, nil)); permission != authz.PermissionResourceRead {
			t.Fatalf("GET %s permission=%q", path, permission)
		}
	}
	if permission := requestPermission(httptest.NewRequest(http.MethodPost, "/panel-api/plugins/p/instances/i/actions/a", nil)); permission != authz.PermissionResourceWrite {
		t.Fatalf("dynamic action permission=%q", permission)
	}
	logRequest := httptest.NewRequest(http.MethodGet, "/panel-api/plugins/visible.plugin/instances/visible/logs?agent_id=edge-a&limit=25", nil)
	logRequest.SetPathValue("id", "visible.plugin")
	logRequest.SetPathValue("instance", "visible")
	logRequest = logRequest.WithContext(context.WithValue(logRequest.Context(), actorContextKey{}, actor))
	logResponse := httptest.NewRecorder()
	Dependencies{PluginService: api}.handlePluginLogs(logResponse, logRequest)
	if logResponse.Code != http.StatusOK || strings.Contains(logResponse.Body.String(), "plaintext") || !strings.Contains(logResponse.Body.String(), "[REDACTED]") || !strings.Contains(logResponse.Body.String(), "opaque-next") {
		t.Fatalf("log status=%d body=%s", logResponse.Code, logResponse.Body.String())
	}
}

func (f *pluginReadAPIFake) List(context.Context) ([]service.PluginSummary, error) {
	return f.installed, nil
}

func (f *pluginReadAPIFake) Detail(context.Context, string) (service.PluginDetail, error) {
	return f.detail, f.detailErr
}

func (f *pluginReadAPIFake) PackageDetail(context.Context, service.PluginPackageCandidate, string) (service.PluginPackageDetail, error) {
	return f.preview, nil
}

func (f *pluginReadAPIFake) Operations(context.Context, string) ([]service.PluginOperationDetail, error) {
	return f.operations, nil
}

func (f *pluginReadAPIFake) InstallMutation(context.Context, service.PluginInstallRequest) (service.PluginSummary, error) {
	return f.mutation, nil
}

func (f *pluginReadAPIFake) EnableMutation(context.Context, string, string) (service.PluginSummary, error) {
	return f.mutation, nil
}

func (f *pluginReadAPIFake) DisableMutation(context.Context, string, string) (service.PluginSummary, error) {
	return f.mutation, nil
}

func (f *pluginReadAPIFake) ConfigureMutation(_ context.Context, input service.PluginConfigureRequest) (service.PluginInstanceDetail, error) {
	f.configureCalls++
	f.configureIn = input
	return f.configured, f.configureErr
}

func (f *pluginReadAPIFake) UpgradeMutation(context.Context, service.PluginUpgradeRequest) (service.PluginSummary, error) {
	return f.mutation, nil
}

func (f *pluginReadAPIFake) RollbackMutation(context.Context, service.PluginRollbackRequest) (service.PluginSummary, error) {
	return f.mutation, nil
}

type marketplacePackageReadFake struct {
	MarketplaceAPI
	candidate service.PluginPackageCandidate
}

func (f *marketplacePackageReadFake) Source(context.Context, string) (marketplace.Source, error) {
	return marketplace.Source{ID: "official", Kind: marketplace.SourceKindOfficial}, nil
}

func (f *marketplacePackageReadFake) ResolvePackage(context.Context, string, string, string, string) (service.PluginPackageCandidate, error) {
	return f.candidate, nil
}

func TestPluginReadHandlersExposeListVerifiedDetailAndPermissionDiff(t *testing.T) {
	installed := storage.InstalledPluginRow{PluginID: "official.read", ActivePackageDigest: strings.Repeat("a", 64)}
	packageDetail := service.PluginPackageDetail{Digest: installed.ActivePackageDigest, Version: "1.0.0", Manifest: plugins.Manifest{ID: installed.PluginID}, ConfigSchema: map[string]any{"type": "object"}, Permissions: []string{"http.inspect"}, PermissionDiff: service.PluginPermissionDiff{Added: []string{"http.inspect"}, Removed: []string{}}}
	pluginSummary := service.PluginSummary{PluginID: installed.PluginID, ActivePackageDigest: installed.ActivePackageDigest}
	instanceDetail := service.PluginInstanceDetail{ID: "instance", PluginID: installed.PluginID, Targets: []string{"local"}, Config: json.RawMessage(`{"mode":"observe"}`), PendingConfig: json.RawMessage(`{"mode":"enforce"}`), PendingTargets: []string{"edge"}, StatusSummary: json.RawMessage(`{"state":"applying"}`)}
	grantDetail := service.PluginGrantDetail{PackageDigest: installed.ActivePackageDigest, Permission: "http.inspect", GrantedBy: "admin"}
	operationDetail := service.PluginOperationDetail{ID: "operation", PluginID: installed.PluginID, Kind: "configure", Status: "applying", AgentResults: json.RawMessage(`{"edge":"pending"}`)}
	pluginAPI := &pluginReadAPIFake{installed: []service.PluginSummary{pluginSummary}, detail: service.PluginDetail{Plugin: pluginSummary, Package: packageDetail, Instances: []service.PluginInstanceDetail{instanceDetail}, Grants: []service.PluginGrantDetail{grantDetail}, AgentStatuses: []service.PluginAgentStatus{}}, preview: packageDetail, operations: []service.PluginOperationDetail{operationDetail}, mutation: pluginSummary, configured: instanceDetail}

	listResponse := httptest.NewRecorder()
	Dependencies{PluginService: pluginAPI}.handlePlugins(listResponse, httptest.NewRequest(http.MethodGet, "/panel-api/plugins", nil))
	if listResponse.Code != http.StatusOK || !strings.Contains(listResponse.Body.String(), `"plugins"`) || !strings.Contains(listResponse.Body.String(), installed.PluginID) {
		t.Fatalf("plugin list status=%d body=%s", listResponse.Code, listResponse.Body.String())
	}

	detailRequest := httptest.NewRequest(http.MethodGet, "/panel-api/plugins/official.read", nil)
	detailRequest.SetPathValue("id", installed.PluginID)
	detailResponse := httptest.NewRecorder()
	Dependencies{PluginService: pluginAPI}.handlePlugin(detailResponse, detailRequest)
	for _, field := range []string{`"plugin"`, `"package"`, `"manifest"`, `"config_schema"`, `"permissions"`, `"permission_diff"`, `"instances"`, `"grants"`, `"agent_statuses"`} {
		if detailResponse.Code != http.StatusOK || !strings.Contains(detailResponse.Body.String(), field) {
			t.Fatalf("plugin detail status=%d body=%s lacks %s", detailResponse.Code, detailResponse.Body.String(), field)
		}
	}
	var detailPayload map[string]any
	if err := json.Unmarshal(detailResponse.Body.Bytes(), &detailPayload); err != nil {
		t.Fatal(err)
	}
	instances := detailPayload["instances"].([]any)
	instance := instances[0].(map[string]any)
	if _, ok := instance["targets"].([]any); !ok {
		t.Fatalf("targets JSON type = %T", instance["targets"])
	}
	for _, field := range []string{"config", "pending_config", "status_summary"} {
		if _, ok := instance[field].(map[string]any); !ok {
			t.Fatalf("%s JSON type = %T", field, instance[field])
		}
	}
	if _, ok := instance["pending_targets"].([]any); !ok {
		t.Fatalf("pending_targets JSON type = %T", instance["pending_targets"])
	}
	if strings.Contains(detailResponse.Body.String(), "grant_key") {
		t.Fatalf("plugin detail leaked storage grant key: %s", detailResponse.Body.String())
	}

	operationsRequest := httptest.NewRequest(http.MethodGet, "/panel-api/plugins/official.read/operations", nil)
	operationsRequest.SetPathValue("id", installed.PluginID)
	operationsResponse := httptest.NewRecorder()
	Dependencies{PluginService: pluginAPI}.handlePluginOperations(operationsResponse, operationsRequest)
	var operationsPayload map[string][]map[string]any
	if err := json.Unmarshal(operationsResponse.Body.Bytes(), &operationsPayload); err != nil {
		t.Fatal(err)
	}
	if _, ok := operationsPayload["operations"][0]["agent_results"].(map[string]any); !ok {
		t.Fatalf("agent_results JSON type = %T", operationsPayload["operations"][0]["agent_results"])
	}

	previewRequest := httptest.NewRequest(http.MethodPost, "/panel-api/plugins/package-detail", strings.NewReader(`{"source_id":"official","plugin_id":"official.read","version":"1.0.0","digest":"`+installed.ActivePackageDigest+`","confirmed_permissions":[],"risk_accepted":false}`))
	previewResponse := httptest.NewRecorder()
	Dependencies{PluginService: pluginAPI, MarketplaceService: &marketplacePackageReadFake{}}.handlePluginPackageDetail(previewResponse, previewRequest)
	if previewResponse.Code != http.StatusOK || !strings.Contains(previewResponse.Body.String(), `"permission_diff"`) {
		t.Fatalf("package detail status=%d body=%s", previewResponse.Code, previewResponse.Body.String())
	}

	configureRequest := httptest.NewRequest(http.MethodPost, "/panel-api/plugins/official.read/configure", strings.NewReader(`{"instance_id":"instance","resource_group_id":"default","targets":["local"],"policy_chains":["shared"],"bindings":[{"consumer":{"kind":"http_rule","id":"1"},"target_agent_id":"local"}],"config":{"mode":"observe"},"secret_replacements":{"/credentials/token":"replacement-secret"}}`))
	configureRequest.SetPathValue("id", installed.PluginID)
	configureRequest.SetPathValue("action", "configure")
	configureResponse := httptest.NewRecorder()
	Dependencies{PluginService: pluginAPI}.handlePluginAction(configureResponse, configureRequest)
	var configurePayload map[string]any
	if err := json.Unmarshal(configureResponse.Body.Bytes(), &configurePayload); err != nil {
		t.Fatal(err)
	}
	configureResult := configurePayload["result"].(map[string]any)
	if _, ok := configureResult["targets"].([]any); !ok {
		t.Fatalf("configure targets JSON type = %T", configureResult["targets"])
	}
	for _, field := range []string{"config", "pending_config", "status_summary"} {
		if _, ok := configureResult[field].(map[string]any); !ok {
			t.Fatalf("configure %s JSON type = %T", field, configureResult[field])
		}
	}
	if _, ok := configureResult["pending_targets"].([]any); !ok || strings.Contains(configureResponse.Body.String(), "TargetJSON") {
		t.Fatalf("configure mutation leaked persistence shape: %s", configureResponse.Body.String())
	}
	if pluginAPI.configureIn.PolicyChains == nil || len(*pluginAPI.configureIn.PolicyChains) != 1 || (*pluginAPI.configureIn.PolicyChains)[0] != "shared" {
		t.Fatalf("configure policy chains = %v", pluginAPI.configureIn.PolicyChains)
	}
	if pluginAPI.configureIn.Bindings == nil || len(*pluginAPI.configureIn.Bindings) != 1 || (*pluginAPI.configureIn.Bindings)[0].Consumer.Kind != "http_rule" {
		t.Fatalf("configure bindings = %v", pluginAPI.configureIn.Bindings)
	}
	if string(pluginAPI.configureIn.SecretReplacements["/credentials/token"]) != `"replacement-secret"` || strings.Contains(configureResponse.Body.String(), "replacement-secret") {
		t.Fatalf("configure secret replacement was not write-only: input=%s body=%s", pluginAPI.configureIn.SecretReplacements["/credentials/token"], configureResponse.Body.String())
	}
	omittedChains := httptest.NewRequest(http.MethodPost, "/panel-api/plugins/official.read/configure", strings.NewReader(`{"instance_id":"instance","resource_group_id":"default","targets":["local"],"config":{"mode":"observe"}}`))
	omittedChains.SetPathValue("id", installed.PluginID)
	omittedChains.SetPathValue("action", "configure")
	omittedResponse := httptest.NewRecorder()
	Dependencies{PluginService: pluginAPI}.handlePluginAction(omittedResponse, omittedChains)
	if omittedResponse.Code != http.StatusBadRequest || pluginAPI.configureCalls != 1 {
		t.Fatalf("omitted policy_chains status=%d configure calls=%d", omittedResponse.Code, pluginAPI.configureCalls)
	}
	callerFence := httptest.NewRequest(http.MethodPost, "/panel-api/plugins/official.read/configure", strings.NewReader(`{"instance_id":"instance","resource_group_id":"default","targets":["local"],"policy_chains":[],"bindings":[{"consumer":{"kind":"http_rule","id":"1","resource_group_id":"forged","version":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},"target_agent_id":"local"}],"config":{}}`))
	callerFence.SetPathValue("id", installed.PluginID)
	callerFence.SetPathValue("action", "configure")
	callerFenceResponse := httptest.NewRecorder()
	Dependencies{PluginService: pluginAPI}.handlePluginAction(callerFenceResponse, callerFence)
	if callerFenceResponse.Code != http.StatusBadRequest || pluginAPI.configureCalls != 1 {
		t.Fatalf("caller-supplied ownership fence status=%d configure calls=%d", callerFenceResponse.Code, pluginAPI.configureCalls)
	}

	enableRequest := httptest.NewRequest(http.MethodPost, "/panel-api/plugins/official.read/enable", nil)
	enableRequest.SetPathValue("id", installed.PluginID)
	enableRequest.SetPathValue("action", "enable")
	enableResponse := httptest.NewRecorder()
	Dependencies{PluginService: pluginAPI}.handlePluginAction(enableResponse, enableRequest)
	if enableResponse.Code != http.StatusAccepted || strings.Contains(enableResponse.Body.String(), "CleanupPolicyJSON") || !strings.Contains(enableResponse.Body.String(), `"plugin_id":"official.read"`) {
		t.Fatalf("enable mutation response status=%d body=%s", enableResponse.Code, enableResponse.Body.String())
	}
}

func TestPluginActionDoesNotClassifyInternalJSONTextAsBadRequest(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/panel-api/plugins/official.read/configure", strings.NewReader(`{"instance_id":"instance","resource_group_id":"default","targets":["local"],"policy_chains":[],"config":{}}`))
	request.SetPathValue("id", "official.read")
	request.SetPathValue("action", "configure")
	response := httptest.NewRecorder()
	Dependencies{PluginService: &pluginReadAPIFake{configureErr: errors.New("storage json projection failed")}}.handlePluginAction(response, request)
	if response.Code != http.StatusInternalServerError || strings.Contains(response.Body.String(), "storage json") || !strings.Contains(response.Body.String(), "internal marketplace or plugin service error") {
		t.Fatalf("internal JSON-text error status=%d body=%s", response.Code, response.Body.String())
	}

	badRequest := httptest.NewRequest(http.MethodPost, "/panel-api/plugins/official.read/configure", strings.NewReader(`{"unknown":true}`))
	badRequest.SetPathValue("id", "official.read")
	badRequest.SetPathValue("action", "configure")
	badResponse := httptest.NewRecorder()
	Dependencies{PluginService: &pluginReadAPIFake{}}.handlePluginAction(badResponse, badRequest)
	if badResponse.Code != http.StatusBadRequest || !strings.Contains(badResponse.Body.String(), "unknown field") {
		t.Fatalf("typed decode error status=%d body=%s", badResponse.Code, badResponse.Body.String())
	}
}

func TestPluginDetailProjectionFailureIsFailClosedAndRedacted(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/panel-api/plugins/official.read", nil)
	request.SetPathValue("id", "official.read")
	response := httptest.NewRecorder()
	Dependencies{PluginService: &pluginReadAPIFake{detailErr: fmt.Errorf("%w: secret persisted json", service.ErrPluginReadProjection)}}.handlePlugin(response, request)
	if response.Code != http.StatusInternalServerError || strings.Contains(response.Body.String(), "secret persisted") || !strings.Contains(response.Body.String(), "internal marketplace or plugin service error") {
		t.Fatalf("projection error status=%d body=%s", response.Code, response.Body.String())
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
	if err != nil || strings.Contains(string(encoded), "private-token") || strings.Contains(string(encoded), metadata.ID) || !strings.Contains(string(encoded), `"credential_configured":true`) {
		t.Fatalf("source JSON = %s, %v", encoded, err)
	}
}

func TestMarketplaceSignerIsResolvedFromAuthorizedPurposeBoundVaultSecret(t *testing.T) {
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
	publicKey := base64.StdEncoding.EncodeToString(make([]byte, 32))
	metadata, err := vault.Create(ctx, secrets.OperationContext{ActorID: "admin", ResourceGroupID: "default"}, "market-signer", marketplace.SignerSecretPurpose, publicKey)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/panel-api/marketplace/sources", nil)
	actor := authz.Actor{ID: "admin", SessionID: "session", Bootstrap: true}
	request = request.WithContext(context.WithValue(request.Context(), actorContextKey{}, actor))
	request.Header.Set("X-Request-ID", "signer-request")
	manager := authz.NewManager(store, authz.Options{})
	signer, err := (Dependencies{SecretVault: vault, AccessManager: manager}).resolveMarketplaceSigner(request, "community-release", metadata.ID)
	if err != nil {
		t.Fatal(err)
	}
	if signer.KeyID != "community-release" || signer.SecretRef != metadata.ID || signer.PublicKey != publicKey {
		t.Fatalf("resolved signer = %+v", signer)
	}
	wrong, err := vault.Create(ctx, secrets.OperationContext{ActorID: "admin", ResourceGroupID: "default"}, "wrong-purpose", "generic", publicKey)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (Dependencies{SecretVault: vault, AccessManager: manager}).resolveMarketplaceSigner(request, "community-release", wrong.ID); err == nil {
		t.Fatal("wrong-purpose signer secret was accepted")
	}
	for _, input := range [][2]string{{" community-release", metadata.ID}, {"community-release", " " + metadata.ID}} {
		if _, err := (Dependencies{SecretVault: vault, AccessManager: manager}).resolveMarketplaceSigner(request, input[0], input[1]); err == nil {
			t.Fatalf("non-canonical signer identity %q/%q was accepted", input[0], input[1])
		}
	}
	whitespace, err := vault.Create(ctx, secrets.OperationContext{ActorID: "admin", ResourceGroupID: "default"}, "whitespace-signer", marketplace.SignerSecretPurpose, " "+publicKey)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (Dependencies{SecretVault: vault, AccessManager: manager}).resolveMarketplaceSigner(request, "community-release", whitespace.ID); err == nil {
		t.Fatal("non-canonical signer public key was accepted")
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

func TestPluginReadsAreResourceScopedAndMarketplaceMutationsRemainAdmin(t *testing.T) {
	for _, path := range []string{"/panel-api/plugins", "/panel-api/plugins/official.waf", "/panel-api/plugins/official.waf/operations"} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		if permission := requestPermission(request); permission != authz.PermissionResourceRead {
			t.Fatalf("%s permission = %q, want %q", path, permission, authz.PermissionResourceRead)
		}
	}
	for _, path := range []string{"/panel-api/plugins/package-detail", "/panel-api/plugins/install", "/panel-api/marketplace/sources", "/panel-api/marketplace/sources/official/refresh"} {
		request := httptest.NewRequest(http.MethodPost, path, nil)
		if permission := requestPermission(request); permission != authz.PermissionSystemAdmin {
			t.Fatalf("%s permission = %q, want %q", path, permission, authz.PermissionSystemAdmin)
		}
	}
}

func TestMarketplaceEntriesKeepsSourceAndSnapshotGenerationCoherentAcrossPromotion(t *testing.T) {
	ctx := context.Background()
	store, err := storage.NewSQLiteStore(t.TempDir(), "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	source, err := marketplace.NewSignedCustomSource("entries-coherent", "Entries Coherent", "https://example.com/plugins.git", "main", "", 0, marketplace.SourceSigner{KeyID: "entries-release", SecretRef: "vault-entries", PublicKey: base64.StdEncoding.EncodeToString(make([]byte, 32))})
	if err != nil {
		t.Fatal(err)
	}
	oldOID := strings.Repeat("1", 40)
	oldSnapshot := marketplace.Snapshot{ID: "entries-old", SourceID: source.ID, Commit: oldOID, Path: "entries-old", ValidatedAt: time.Now().UTC()}
	if err := store.PromoteSnapshot(ctx, source, oldSnapshot); err != nil {
		t.Fatal(err)
	}
	base := service.NewMarketplaceService(store, nil, plugins.NewValidator(plugins.ValidatorOptions{}), t.TempDir())
	barrier := &promotionBarrierMarketplaceAPI{MarketplaceService: base, ready: make(chan struct{}), release: make(chan struct{})}
	request := httptest.NewRequest(http.MethodGet, "/panel-api/marketplace/sources/"+source.ID+"/entries", nil)
	request.SetPathValue("id", source.ID)
	response := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		Dependencies{MarketplaceService: barrier}.handleMarketplaceEntries(response, request)
		close(done)
	}()
	<-barrier.ready
	newOID := strings.Repeat("2", 40)
	newSnapshot := marketplace.Snapshot{ID: "entries-new", SourceID: source.ID, Commit: newOID, Path: "entries-new", ValidatedAt: time.Now().UTC()}
	if err := store.PromoteSnapshot(ctx, source, newSnapshot); err != nil {
		t.Fatal(err)
	}
	close(barrier.release)
	<-done
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var payload struct {
		Source   marketplace.Source   `json:"source"`
		Snapshot marketplace.Snapshot `json:"snapshot"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Source.CurrentSnapshot != oldSnapshot.ID || payload.Source.CurrentResolvedOID != oldOID || payload.Snapshot.ID != oldSnapshot.ID || payload.Snapshot.Commit != oldOID {
		t.Fatalf("entries response mixed generations: %+v", payload)
	}
	current, err := base.CurrentCatalog(ctx, source.ID)
	if err != nil || current.Source.CurrentSnapshot != newSnapshot.ID || current.Source.CurrentResolvedOID != newOID || current.Snapshot.ID != newSnapshot.ID || current.Snapshot.Commit != newOID {
		t.Fatalf("post-promotion catalog = %+v err=%v", current, err)
	}
}

type promotionBarrierMarketplaceAPI struct {
	*service.MarketplaceService
	ready   chan struct{}
	release chan struct{}
}

func (s *promotionBarrierMarketplaceAPI) CurrentCatalog(ctx context.Context, sourceID string) (service.MarketplaceCatalog, error) {
	catalog, err := s.MarketplaceService.CurrentCatalog(ctx, sourceID)
	close(s.ready)
	<-s.release
	return catalog, err
}

func TestMarketplaceAuthenticatedPrevalidationFailuresInvokeRedactedAudit(t *testing.T) {
	fake := &marketplaceAuditFake{}
	dependencies := Dependencies{MarketplaceService: fake}
	for name, body := range map[string]string{
		"json":       `{"id":"bad"} {"url":"https://example.com"}`,
		"interval":   `{"id":"bad","name":"Bad","url":"https://example.com/plugins.git","purpose":"market","ref_kind":"branch","ref_name":"main","refresh_interval":"never"}`,
		"negative":   `{"id":"bad-negative","name":"Bad","url":"https://example.com/plugins.git","purpose":"market","ref_kind":"branch","ref_name":"main","refresh_interval":"-1h"}`,
		"credential": `{"id":"private","name":"Private","url":"https://example.com/plugins.git","purpose":"market","ref_kind":"branch","ref_name":"main","credential_ref":"secret-ref"}`,
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
		"source":        {marketplace.ErrInvalidSource, http.StatusBadRequest},
		"authz":         {authz.ErrForbidden, http.StatusForbidden},
		"input":         {service.ErrInvalidArgument, http.StatusBadRequest},
		"scope":         {storage.ErrPluginInstanceScope, http.StatusUnprocessableEntity},
		"source exists": {service.ErrMarketplaceSourceExists, http.StatusConflict},
		"installed":     {storage.ErrPluginAlreadyInstalled, http.StatusConflict},
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

func TestPluginAndMarketplaceHandlersMapRealDecodeAndSourceValidation(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/panel-api/plugins/example/configure", strings.NewReader(`{"instance_id":`))
	request.SetPathValue("id", "example")
	request.SetPathValue("action", "configure")
	response := httptest.NewRecorder()
	Dependencies{}.handlePluginAction(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("unexpected EOF status=%d body=%s", response.Code, response.Body.String())
	}
	store, err := storage.NewSQLiteStore(t.TempDir(), "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	marketService := service.NewMarketplaceService(store, nil, plugins.NewValidator(plugins.ValidatorOptions{}), filepath.Join(t.TempDir(), "packages"))
	request = httptest.NewRequest(http.MethodPost, "/panel-api/marketplace/sources", strings.NewReader(`{"id":"bad","name":"Bad","url":"https://example.com/repo.git?token=secret","reference":"main"}`))
	response = httptest.NewRecorder()
	Dependencies{MarketplaceService: marketService}.handleMarketplaceSources(response, request)
	if response.Code != http.StatusBadRequest || strings.Contains(response.Body.String(), "token=secret") {
		t.Fatalf("invalid source status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestMarketplacePrevalidationAuditPersistenceFailureReturnsRedactedServerError(t *testing.T) {
	fake := &marketplaceAuditFake{auditErr: errors.New("database audit secret")}
	request := httptest.NewRequest(http.MethodPost, "/panel-api/marketplace/sources", strings.NewReader(`{"id":`))
	response := httptest.NewRecorder()
	Dependencies{MarketplaceService: fake}.handleMarketplaceSources(response, request)
	if response.Code != http.StatusInternalServerError || strings.Contains(response.Body.String(), "secret") {
		t.Fatalf("audit persistence response=%d %s", response.Code, response.Body.String())
	}
}

type marketplaceAuditFake struct {
	errorClasses []string
	auditErr     error
}

type repositorySourceAPIFake struct {
	marketplaceAuditFake
	source  marketplace.Source
	updated marketplace.Source
}

func (f *repositorySourceAPIFake) Source(context.Context, string) (marketplace.Source, error) {
	return f.source, nil
}
func (f *repositorySourceAPIFake) UpdateGitRepositorySource(_ context.Context, source marketplace.Source, expected uint64) (marketplace.Source, error) {
	if expected != f.source.ConfigRevision {
		return marketplace.Source{}, storage.ErrPluginConflict
	}
	f.updated = source
	return source, nil
}

func TestRepositorySourcePatchPreservesWriteOnlySignerAndRejectsDerivedFields(t *testing.T) {
	key := base64.StdEncoding.EncodeToString(make([]byte, ed25519.PublicKeySize))
	source, err := marketplace.NewSignedGitRepositorySource("community", "Community", "https://example.com/community.git", marketplace.SourcePurposeMarket, marketplace.GitRefKindBranch, "main", "", 0, marketplace.SourceSigner{KeyID: "community-release", SecretRef: "vault-signer", PublicKey: key})
	if err != nil {
		t.Fatal(err)
	}
	fake := &repositorySourceAPIFake{source: source}
	request := httptest.NewRequest(http.MethodPatch, "/marketplace/sources/community", strings.NewReader(`{"name":"Updated","signer_key_id":"community-release","config_revision":1}`))
	request.SetPathValue("id", source.ID)
	response := httptest.NewRecorder()
	Dependencies{MarketplaceService: fake}.handleMarketplaceSource(response, request)
	if response.Code != http.StatusOK || fake.updated.SignerSecretRef != source.SignerSecretRef || fake.updated.ConfigRevision != source.ConfigRevision+1 {
		t.Fatalf("patch response=%d %s updated=%+v", response.Code, response.Body.String(), fake.updated)
	}
	if strings.Contains(response.Body.String(), "credential_ref") || strings.Contains(response.Body.String(), "signer_secret_ref") || strings.Contains(response.Body.String(), "vault-signer") {
		t.Fatalf("write-only source secret leaked: %s", response.Body.String())
	}
	request = httptest.NewRequest(http.MethodPatch, "/marketplace/sources/community", strings.NewReader(`{"current_resolved_oid":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`))
	request.SetPathValue("id", source.ID)
	response = httptest.NewRecorder()
	Dependencies{MarketplaceService: fake}.handleMarketplaceSource(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("derived field response=%d %s", response.Code, response.Body.String())
	}
}

func TestRepositorySourcePatchRejectsStaleBrowserRevisionWithoutOverwrite(t *testing.T) {
	key := base64.StdEncoding.EncodeToString(make([]byte, ed25519.PublicKeySize))
	source, err := marketplace.NewSignedGitRepositorySource("stale-form", "Current Name", "https://example.com/community.git", marketplace.SourcePurposeMarket, marketplace.GitRefKindBranch, "release", "", 0, marketplace.SourceSigner{KeyID: "community-release", SecretRef: "vault-signer", PublicKey: key})
	if err != nil {
		t.Fatal(err)
	}
	source.ConfigRevision = 2
	fake := &repositorySourceAPIFake{source: source}
	request := httptest.NewRequest(http.MethodPatch, "/marketplace/sources/stale-form", strings.NewReader(`{"name":"Stale Browser Name","ref_name":"main","config_revision":1}`))
	request.SetPathValue("id", source.ID)
	response := httptest.NewRecorder()
	Dependencies{MarketplaceService: fake}.handleMarketplaceSource(response, request)
	if response.Code != http.StatusConflict {
		t.Fatalf("stale patch response=%d %s", response.Code, response.Body.String())
	}
	if fake.updated.ID != "" {
		t.Fatalf("stale patch overwrote source: %+v", fake.updated)
	}
}

func TestRepositorySourceCreateRejectsClientSuppliedConfigRevision(t *testing.T) {
	fake := &marketplaceAuditFake{}
	request := httptest.NewRequest(http.MethodPost, "/marketplace/sources", strings.NewReader(`{"id":"derived","name":"Derived","url":"https://example.com/derived.git","purpose":"market","ref_kind":"branch","ref_name":"main","signer_key_id":"release","signer_secret_ref":"vault-release","config_revision":1}`))
	response := httptest.NewRecorder()
	Dependencies{MarketplaceService: fake}.handleMarketplaceSources(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("derived create field response=%d %s", response.Code, response.Body.String())
	}
}

func (f *marketplaceAuditFake) ListSources(context.Context) ([]marketplace.Source, error) {
	return nil, nil
}
func (f *marketplaceAuditFake) Source(context.Context, string) (marketplace.Source, error) {
	return marketplace.Source{}, service.ErrMarketplaceSourceNotFound
}
func (f *marketplaceAuditFake) CurrentCatalog(context.Context, string) (service.MarketplaceCatalog, error) {
	return service.MarketplaceCatalog{}, nil
}
func (f *marketplaceAuditFake) AddCustomSource(context.Context, string, string, string, string, string, time.Duration, marketplace.SourceSigner) (marketplace.Source, error) {
	return marketplace.Source{}, nil
}
func (f *marketplaceAuditFake) AddGitRepositorySource(context.Context, string, string, string, string, string, string, string, time.Duration, marketplace.SourceSigner) (marketplace.Source, error) {
	return marketplace.Source{}, nil
}
func (f *marketplaceAuditFake) UpdateGitRepositorySource(context.Context, marketplace.Source, uint64) (marketplace.Source, error) {
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
	return f.auditErr
}
