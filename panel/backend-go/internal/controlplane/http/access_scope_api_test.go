package http

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/authz"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/service"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
	"gorm.io/gorm"
)

func TestRestrictedSessionCannotReadHiddenTrafficAgent(t *testing.T) {
	manager, token := newScopedAccessSession(t)
	deps := trafficTestDependencies(fakeTrafficService{})
	deps.AccessManager = manager
	deps.AgentService = fakeAgentService{agents: []service.AgentSummary{{ID: "edge-a"}, {ID: "edge-b"}}}
	router, err := NewRouter(deps)
	if err != nil {
		t.Fatalf("NewRouter() error = %v", err)
	}

	request := httptest.NewRequest(http.MethodGet, "/panel-api/traffic-overview?agent_id=edge-b", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusForbidden {
		t.Fatalf("hidden traffic response = %d body=%s, want 403", response.Code, response.Body.String())
	}
}

func TestRestrictedSessionCannotReadOrDismissHiddenOperation(t *testing.T) {
	manager, token := newScopedAccessSession(t)
	revisions := &scopedRevisionService{status: service.OperationStatus{OperationID: "op-hidden", PrimaryAgent: "edge-b"}}
	deps := trafficTestDependencies(fakeTrafficService{})
	deps.AccessManager = manager
	deps.RevisionService = revisions
	router, err := NewRouter(deps)
	if err != nil {
		t.Fatalf("NewRouter() error = %v", err)
	}

	for _, path := range []string{"/panel-api/operations/op-hidden", "/panel-api/operations/op-hidden/dismiss"} {
		method := http.MethodGet
		if path[len(path)-7:] == "dismiss" {
			method = http.MethodPost
		}
		request := httptest.NewRequest(method, path, nil)
		request.Header.Set("Authorization", "Bearer "+token)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if response.Code != http.StatusForbidden {
			t.Fatalf("%s %s = %d body=%s, want 403", method, path, response.Code, response.Body.String())
		}
	}
	if revisions.dismissCalls != 0 {
		t.Fatalf("DismissOperation calls = %d, want 0", revisions.dismissCalls)
	}
}

func TestRestrictedSessionCannotEnumerateHiddenRevisionEvents(t *testing.T) {
	manager, token := newScopedAccessSession(t)
	revisions := &scopedRevisionService{}
	deps := trafficTestDependencies(fakeTrafficService{})
	deps.AccessManager = manager
	deps.RevisionService = revisions
	router, err := NewRouter(deps)
	if err != nil {
		t.Fatalf("NewRouter() error = %v", err)
	}

	request := httptest.NewRequest(http.MethodGet, "/panel-api/revision-events?agent_id=edge-b", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusForbidden {
		t.Fatalf("hidden revision events response = %d body=%s, want 403", response.Code, response.Body.String())
	}
	if revisions.listCalls != 0 {
		t.Fatalf("ListEvents calls = %d, want 0", revisions.listCalls)
	}
}

func TestQuotaErrorPayloadIncludesRecoveryDecision(t *testing.T) {
	decision := storage.QuotaDecision{
		Metric: "rule_count", ResourceGroupID: "group-a", Current: 2, Limit: 1,
		Allowed: false, ExceedAction: "reject", RecoveryCondition: "delete a rule",
	}
	payload := quotaErrorPayload(&storage.QuotaExceededError{Decision: decision})
	quota, ok := payload["quota_context"].(storage.QuotaDecision)
	if !ok {
		t.Fatalf("quota payload = %#v, want typed decision", payload["quota_context"])
	}
	if quota.Metric != decision.Metric || quota.ResourceGroupID != decision.ResourceGroupID ||
		quota.Current != decision.Current || quota.Limit != decision.Limit ||
		quota.RecoveryCondition != decision.RecoveryCondition {
		t.Fatalf("quota payload = %+v, want %+v", quota, decision)
	}
}

func TestRestrictedAccessManagerCannotEnableAdministrator(t *testing.T) {
	store, err := storage.NewStore(storage.StoreConfig{Driver: "sqlite", DataRoot: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	manager := authz.NewManager(store, authz.Options{})
	if err := manager.EnsureDefaults(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, row := range []storage.AgentRow{{ID: "edge-a"}, {ID: "edge-b"}} {
		if err := store.SaveAgent(t.Context(), row); err != nil {
			t.Fatalf("SaveAgent() error = %v", err)
		}
	}
	managerRole, err := manager.CreateRole(t.Context(), "access-manager", "", []string{authz.PermissionAccessManage})
	if err != nil {
		t.Fatal(err)
	}
	managerUser, err := manager.CreateUser(t.Context(), "access-manager", "", "correct-horse-battery", []string{managerRole.ID})
	if err != nil {
		t.Fatal(err)
	}
	administrator, err := manager.CreateUser(t.Context(), "disabled-admin", "", "correct-horse-battery", []string{authz.RoleAdministrator})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.DisableUser(t.Context(), administrator.ID, true); err != nil {
		t.Fatal(err)
	}
	login, err := manager.Login(t.Context(), managerUser.Username, "correct-horse-battery")
	if err != nil {
		t.Fatal(err)
	}
	deps := trafficTestDependencies(fakeTrafficService{})
	deps.AccessManager = manager
	router, err := NewRouter(deps)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPut, "/panel-api/access/users/"+administrator.ID, strings.NewReader(`{"disabled":false}`))
	request.Header.Set("Authorization", "Bearer "+login.Token)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("response = %d body=%s, want 403", response.Code, response.Body.String())
	}
	current, err := manager.GetUser(t.Context(), administrator.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !current.Disabled {
		t.Fatal("administrator was re-enabled by restricted access manager")
	}
	events, err := manager.ListAuditEvents(t.Context(), 50)
	if err != nil {
		t.Fatal(err)
	}
	foundDenied := false
	for _, event := range events {
		if event.Action == "access.user.update" && event.TargetID == administrator.ID && event.Result == "denied" {
			foundDenied = true
			break
		}
	}
	if !foundDenied {
		t.Fatalf("audit events = %+v, want denied access.user.update", events)
	}
}

func TestRestrictedAccessManagerCannotMutateSecurityBoundaries(t *testing.T) {
	store, err := storage.NewStore(storage.StoreConfig{Driver: "sqlite", DataRoot: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	manager := authz.NewManager(store, authz.Options{})
	if err := manager.EnsureDefaults(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveAgent(t.Context(), storage.AgentRow{ID: "edge-a"}); err != nil {
		t.Fatal(err)
	}
	role, err := manager.CreateRole(t.Context(), "boundary-manager", "", []string{authz.PermissionAccessManage, authz.PermissionQuotaManage, authz.PermissionResourceRead, authz.PermissionResourceWrite})
	if err != nil {
		t.Fatal(err)
	}
	user, err := manager.CreateUser(t.Context(), "boundary-manager", "", "correct-horse-battery", []string{role.ID})
	if err != nil {
		t.Fatal(err)
	}
	hidden, err := manager.CreateResourceGroup(t.Context(), "hidden-boundary", "")
	if err != nil {
		t.Fatal(err)
	}
	login, err := manager.Login(t.Context(), user.Username, "correct-horse-battery")
	if err != nil {
		t.Fatal(err)
	}
	deps := trafficTestDependencies(fakeTrafficService{})
	deps.AccessManager = manager
	router, err := NewRouter(deps)
	if err != nil {
		t.Fatal(err)
	}
	requests := []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodPost, "/panel-api/access/resource-group-grants", `{"subject_kind":"user","subject_id":"` + user.ID + `","resource_group_id":"` + hidden.ID + `"}`},
		{http.MethodPost, "/panel-api/access/resource-bindings", `{"resource_kind":"agent","resource_id":"edge-a","resource_group_id":"` + hidden.ID + `"}`},
		{http.MethodPost, "/panel-api/access/quota-policies", `{"subject_kind":"resource_group","subject_id":"` + hidden.ID + `","resource_group_id":"` + hidden.ID + `","metric":"rule_count","limit":1}`},
		{http.MethodPost, "/panel-api/version-policies", `{}`},
		{http.MethodGet, "/panel-api/pki/authorities", ""},
	}
	for _, item := range requests {
		request := httptest.NewRequest(item.method, item.path, strings.NewReader(item.body))
		request.Header.Set("Authorization", "Bearer "+login.Token)
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if response.Code != http.StatusForbidden {
			t.Fatalf("%s %s = %d body=%s, want 403", item.method, item.path, response.Code, response.Body.String())
		}
	}
	grants, err := manager.ListResourceGroupGrants(t.Context(), authz.BootstrapActor())
	if err != nil {
		t.Fatal(err)
	}
	for _, grant := range grants {
		if grant.SubjectKind == "user" && grant.SubjectID == user.ID && grant.ResourceGroupID == hidden.ID {
			t.Fatalf("unexpected grant side effect: %+v", grant)
		}
	}
	if _, err := store.GetResourceBinding(t.Context(), "agent", "edge-a"); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("agent binding error = %v, want no side effect", err)
	}
	policies, err := store.ListQuotaPolicies(t.Context())
	if err != nil || len(policies) != 0 {
		t.Fatalf("quota policies = %+v error=%v, want no side effect", policies, err)
	}
	events, err := manager.ListAuditEvents(t.Context(), 100)
	if err != nil {
		t.Fatal(err)
	}
	denied := 0
	for _, event := range events {
		if (event.Action == "authorization.check" || event.Action == "quota.policy.upsert") && event.Result == "denied" {
			denied++
		}
	}
	if denied < len(requests) {
		t.Fatalf("denied authorization audits = %d, want at least %d", denied, len(requests))
	}
}

func TestSystemAdminCanListAndRevokeGrantThroughAPI(t *testing.T) {
	store, err := storage.NewStore(storage.StoreConfig{Driver: "sqlite", DataRoot: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	manager := authz.NewManager(store, authz.Options{})
	if err := manager.EnsureDefaults(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveAgent(t.Context(), storage.AgentRow{ID: "edge-granted"}); err != nil {
		t.Fatal(err)
	}
	role, err := manager.CreateRole(t.Context(), "api-grant-reader", "", []string{authz.PermissionResourceRead})
	if err != nil {
		t.Fatal(err)
	}
	user, err := manager.CreateUser(t.Context(), "api-grant-user", "", "correct-horse-battery", []string{role.ID})
	if err != nil {
		t.Fatal(err)
	}
	group, err := manager.CreateResourceGroup(t.Context(), "api-granted-group", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.BindResource(t.Context(), authz.BootstrapActor(), "agent", "edge-granted", group.ID); err != nil {
		t.Fatal(err)
	}
	login, err := manager.Login(t.Context(), user.Username, "correct-horse-battery")
	if err != nil {
		t.Fatal(err)
	}
	deps := trafficTestDependencies(fakeTrafficService{})
	deps.AccessManager = manager
	router, err := NewRouter(deps)
	if err != nil {
		t.Fatal(err)
	}
	body := `{"subject_kind":"user","subject_id":"` + user.ID + `","resource_group_id":"` + group.ID + `"}`
	doAdminRequest := func(method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		request.Header.Set("X-Panel-Token", "secret")
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		return response
	}
	if response := doAdminRequest(http.MethodPost, "/panel-api/access/resource-group-grants", body); response.Code != http.StatusCreated {
		t.Fatalf("grant response = %d body=%s", response.Code, response.Body.String())
	}
	if response := doAdminRequest(http.MethodGet, "/panel-api/access/resource-group-grants", ""); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), user.ID) {
		t.Fatalf("list response = %d body=%s", response.Code, response.Body.String())
	}
	refreshed, err := manager.AuthenticateSession(t.Context(), login.Token)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.AuthorizeResource(t.Context(), refreshed, authz.PermissionResourceRead, "agent", "edge-granted"); err != nil {
		t.Fatalf("authorization after grant = %v", err)
	}
	if response := doAdminRequest(http.MethodDelete, "/panel-api/access/resource-group-grants", body); response.Code != http.StatusOK {
		t.Fatalf("revoke response = %d body=%s", response.Code, response.Body.String())
	}
	refreshed, err = manager.AuthenticateSession(t.Context(), login.Token)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.AuthorizeResource(t.Context(), refreshed, authz.PermissionResourceRead, "agent", "edge-granted"); !errors.Is(err, authz.ErrForbidden) {
		t.Fatalf("authorization after revoke = %v, want forbidden", err)
	}
	events, err := manager.ListAuditEvents(t.Context(), 100)
	if err != nil {
		t.Fatal(err)
	}
	actions := map[string]bool{}
	for _, event := range events {
		if event.Result == "success" {
			actions[event.Action] = true
		}
	}
	if !actions["access.resource_group.grant"] || !actions["access.resource_group.revoke"] {
		t.Fatalf("successful audit actions = %+v, want grant and revoke", actions)
	}
}

func TestScopedQuotaManagerAPIUsesVisibleResourceGroups(t *testing.T) {
	store, err := storage.NewStore(storage.StoreConfig{Driver: "sqlite", DataRoot: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	manager := authz.NewManager(store, authz.Options{})
	if err := manager.EnsureDefaults(t.Context()); err != nil {
		t.Fatal(err)
	}
	role, err := manager.CreateRole(t.Context(), "api-scoped-quota", "", []string{authz.PermissionQuotaManage})
	if err != nil {
		t.Fatal(err)
	}
	user, err := manager.CreateUser(t.Context(), "api-scoped-quota", "", "correct-horse-battery", []string{role.ID})
	if err != nil {
		t.Fatal(err)
	}
	visible, err := manager.CreateResourceGroup(t.Context(), "api-quota-visible", "")
	if err != nil {
		t.Fatal(err)
	}
	hidden, err := manager.CreateResourceGroup(t.Context(), "api-quota-hidden", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.GrantResourceGroup(t.Context(), authz.BootstrapActor(), "user", user.ID, visible.ID); err != nil {
		t.Fatal(err)
	}
	login, err := manager.Login(t.Context(), user.Username, "correct-horse-battery")
	if err != nil {
		t.Fatal(err)
	}
	deps := trafficTestDependencies(fakeTrafficService{})
	deps.AccessManager = manager
	router, err := NewRouter(deps)
	if err != nil {
		t.Fatal(err)
	}
	doRequest := func(method, body string) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(method, "/panel-api/access/quota-policies", strings.NewReader(body))
		request.Header.Set("Authorization", "Bearer "+login.Token)
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		return response
	}
	visibleBody := `{"id":"api-visible-policy","subject_kind":"resource_group","subject_id":"` + visible.ID + `","resource_group_id":"` + visible.ID + `","metric":"rule_count","limit":2}`
	if response := doRequest(http.MethodPost, visibleBody); response.Code != http.StatusCreated {
		t.Fatalf("visible policy response = %d body=%s", response.Code, response.Body.String())
	}
	hiddenBody := `{"id":"api-hidden-policy","subject_kind":"resource_group","subject_id":"` + hidden.ID + `","resource_group_id":"` + hidden.ID + `","metric":"rule_count","limit":1}`
	if response := doRequest(http.MethodPost, hiddenBody); response.Code != http.StatusForbidden {
		t.Fatalf("hidden policy response = %d body=%s, want 403", response.Code, response.Body.String())
	}
	response := doRequest(http.MethodGet, "")
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "api-visible-policy") || strings.Contains(response.Body.String(), "api-hidden-policy") {
		t.Fatalf("quota list response = %d body=%s", response.Code, response.Body.String())
	}
	events, err := manager.ListAuditEvents(t.Context(), 100)
	if err != nil {
		t.Fatal(err)
	}
	foundDenied := false
	for _, event := range events {
		if event.Action == "quota.policy.upsert" && event.Result == "denied" {
			foundDenied = true
		}
	}
	if !foundDenied {
		t.Fatal("missing denied quota policy audit")
	}
}

func TestCertificateResourceBindingAPIPreservesDerivedOwnership(t *testing.T) {
	dataRoot := t.TempDir()
	store, err := storage.NewStore(storage.StoreConfig{Driver: "sqlite", DataRoot: dataRoot})
	if err != nil {
		t.Fatal(err)
	}
	manager := authz.NewManager(store, authz.Options{})
	if err := manager.EnsureDefaults(t.Context()); err != nil {
		t.Fatal(err)
	}
	groupA, err := manager.CreateResourceGroup(t.Context(), "certificate-a", "")
	if err != nil {
		t.Fatal(err)
	}
	groupB, err := manager.CreateResourceGroup(t.Context(), "certificate-b", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, agentID := range []string{"cert-edge-a", "cert-edge-b"} {
		if err := store.SaveAgent(t.Context(), storage.AgentRow{ID: agentID}); err != nil {
			t.Fatal(err)
		}
	}
	if err := manager.BindResource(t.Context(), authz.BootstrapActor(), "agent", "cert-edge-a", groupA.ID); err != nil {
		t.Fatal(err)
	}
	if err := manager.BindResource(t.Context(), authz.BootstrapActor(), "agent", "cert-edge-b", groupB.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveManagedCertificates(t.Context(), []storage.ManagedCertificateRow{
		{ID: 21, Domain: "single.example.com", TargetAgentIDs: `["cert-edge-a"]`},
		{ID: 22, Domain: "cross.example.com", TargetAgentIDs: `["cert-edge-a","cert-edge-b"]`},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = storage.NewStore(storage.StoreConfig{Driver: "sqlite", DataRoot: dataRoot})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	manager = authz.NewManager(store, authz.Options{})
	if err := manager.EnsureDefaults(t.Context()); err != nil {
		t.Fatal(err)
	}
	deps := trafficTestDependencies(fakeTrafficService{})
	deps.AccessManager = manager
	router, err := NewRouter(deps)
	if err != nil {
		t.Fatal(err)
	}
	bind := func(certificateID, groupID string) *httptest.ResponseRecorder {
		t.Helper()
		body := `{"resource_kind":"certificate","resource_id":"` + certificateID + `","resource_group_id":"` + groupID + `"}`
		request := httptest.NewRequest(http.MethodPost, "/panel-api/access/resource-bindings", strings.NewReader(body))
		request.Header.Set("X-Panel-Token", "secret")
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		return response
	}
	if response := bind("21", groupA.ID); response.Code != http.StatusCreated {
		t.Fatalf("authoritative bind = %d body=%s", response.Code, response.Body.String())
	}
	if response := bind("21", groupB.ID); response.Code != http.StatusBadRequest {
		t.Fatalf("wrong-group bind = %d body=%s, want 400", response.Code, response.Body.String())
	}
	if response := bind("22", groupA.ID); response.Code != http.StatusBadRequest {
		t.Fatalf("cross-group bind = %d body=%s, want 400", response.Code, response.Body.String())
	}
	for id, wantGroup := range map[string]string{"21": groupA.ID, "22": "system:cross-group-certificate"} {
		binding, err := store.GetResourceBinding(t.Context(), "certificate", id)
		if err != nil || binding.ResourceGroupID != wantGroup {
			t.Fatalf("certificate %s binding = %+v error=%v, want %s", id, binding, err, wantGroup)
		}
	}
}

func TestResourceBindingAPIRejectsWhitespaceWithoutSideEffects(t *testing.T) {
	store, err := storage.NewStore(storage.StoreConfig{Driver: "sqlite", DataRoot: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	manager := authz.NewManager(store, authz.Options{})
	if err := manager.EnsureDefaults(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveAgent(t.Context(), storage.AgentRow{ID: "edge-whitespace"}); err != nil {
		t.Fatal(err)
	}
	groupA, err := manager.CreateResourceGroup(t.Context(), "whitespace-a", "")
	if err != nil {
		t.Fatal(err)
	}
	groupB, err := manager.CreateResourceGroup(t.Context(), "whitespace-b", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.BindResource(t.Context(), authz.BootstrapActor(), "agent", "edge-whitespace", groupA.ID); err != nil {
		t.Fatal(err)
	}
	deps := trafficTestDependencies(fakeTrafficService{})
	deps.AccessManager = manager
	router, err := NewRouter(deps)
	if err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{
		`{"resource_kind":"agent ","resource_id":"edge-whitespace","resource_group_id":"` + groupB.ID + `"}`,
		`{"resource_kind":"agent","resource_id":" edge-whitespace","resource_group_id":"` + groupB.ID + `"}`,
	} {
		request := httptest.NewRequest(http.MethodPost, "/panel-api/access/resource-bindings", strings.NewReader(body))
		request.Header.Set("X-Panel-Token", "secret")
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("whitespace binding = %d body=%s, want 400", response.Code, response.Body.String())
		}
	}
	binding, err := store.GetResourceBinding(t.Context(), "agent", "edge-whitespace")
	if err != nil || binding.ResourceGroupID != groupA.ID {
		t.Fatalf("canonical binding = %+v error=%v, want unchanged group %s", binding, err, groupA.ID)
	}
	for _, input := range [][2]string{{"agent ", "edge-whitespace"}, {"agent", " edge-whitespace"}} {
		if _, err := store.GetResourceBinding(t.Context(), input[0], input[1]); !errors.Is(err, gorm.ErrRecordNotFound) {
			t.Fatalf("non-canonical binding %q/%q error = %v, want no row", input[0], input[1], err)
		}
	}
}

func TestEgressProfileCollectionFiltersBeforeGroupAuthorization(t *testing.T) {
	store, err := storage.NewStore(storage.StoreConfig{Driver: "sqlite", DataRoot: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	manager := authz.NewManager(store, authz.Options{})
	if err := manager.EnsureDefaults(t.Context()); err != nil {
		t.Fatal(err)
	}
	group, err := manager.CreateResourceGroup(t.Context(), "egress-visible", "")
	if err != nil {
		t.Fatal(err)
	}
	role, err := manager.CreateRole(t.Context(), "egress-reader", "", []string{authz.PermissionResourceRead})
	if err != nil {
		t.Fatal(err)
	}
	user, err := manager.CreateUser(t.Context(), "egress-reader", "", "correct-horse-battery", []string{role.ID})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.GrantResourceGroup(t.Context(), authz.BootstrapActor(), "user", user.ID, group.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveEgressProfiles(t.Context(), []storage.EgressProfileRow{{ID: 7, Name: "visible-egress"}, {ID: 8, Name: "hidden-default"}}); err != nil {
		t.Fatal(err)
	}
	if err := manager.BindResource(t.Context(), authz.BootstrapActor(), "egress_profile", "7", group.ID); err != nil {
		t.Fatal(err)
	}
	login, err := manager.Login(t.Context(), user.Username, "correct-horse-battery")
	if err != nil {
		t.Fatal(err)
	}
	deps := trafficTestDependencies(fakeTrafficService{})
	deps.AccessManager = manager
	deps.EgressProfileService = fakeEgressProfileService{profiles: []service.EgressProfile{{ID: 7, Name: "visible-egress"}, {ID: 8, Name: "hidden-default"}}}
	router, err := NewRouter(deps)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/panel-api/egress-profiles", nil)
	request.Header.Set("Authorization", "Bearer "+login.Token)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "visible-egress") || strings.Contains(response.Body.String(), "hidden-default") {
		t.Fatalf("egress collection = %d body=%s", response.Code, response.Body.String())
	}
}

func newScopedAccessSession(t *testing.T) (*authz.Manager, string) {
	t.Helper()
	store, err := storage.NewStore(storage.StoreConfig{Driver: "sqlite", DataRoot: t.TempDir()})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	manager := authz.NewManager(store, authz.Options{})
	if err := manager.EnsureDefaults(t.Context()); err != nil {
		t.Fatalf("EnsureDefaults() error = %v", err)
	}
	for _, row := range []storage.AgentRow{{ID: "edge-a"}, {ID: "edge-b"}} {
		if err := store.SaveAgent(t.Context(), row); err != nil {
			t.Fatalf("SaveAgent(%s) error = %v", row.ID, err)
		}
	}
	role, err := manager.CreateRole(t.Context(), "scoped-operator", "", []string{authz.PermissionResourceRead, authz.PermissionResourceWrite})
	if err != nil {
		t.Fatalf("CreateRole() error = %v", err)
	}
	user, err := manager.CreateUser(t.Context(), "scoped-user", "", "correct-horse-battery", []string{role.ID})
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}
	group, err := manager.CreateResourceGroup(t.Context(), "visible-group", "")
	if err != nil {
		t.Fatalf("CreateResourceGroup() error = %v", err)
	}
	if err := manager.GrantResourceGroup(t.Context(), authz.BootstrapActor(), "user", user.ID, group.ID); err != nil {
		t.Fatalf("GrantResourceGroup() error = %v", err)
	}
	if err := manager.BindResource(t.Context(), authz.BootstrapActor(), "agent", "edge-a", group.ID); err != nil {
		t.Fatalf("BindResource(edge-a) error = %v", err)
	}
	hidden, err := manager.CreateResourceGroup(t.Context(), "hidden-group", "")
	if err != nil {
		t.Fatalf("CreateResourceGroup(hidden) error = %v", err)
	}
	if err := manager.BindResource(t.Context(), authz.BootstrapActor(), "agent", "edge-b", hidden.ID); err != nil {
		t.Fatalf("BindResource(edge-b) error = %v", err)
	}
	login, err := manager.Login(t.Context(), user.Username, "correct-horse-battery")
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}
	return manager, login.Token
}

type scopedRevisionService struct {
	status       service.OperationStatus
	dismissCalls int
	listCalls    int
}

func (s *scopedRevisionService) GetOperationStatus(context.Context, string) (service.OperationStatus, error) {
	return s.status, nil
}
func (s *scopedRevisionService) DismissOperation(context.Context, string) (service.OperationStatus, error) {
	s.dismissCalls++
	return s.status, nil
}
func (*scopedRevisionService) GetAgentRevisionStatus(context.Context, string, int64) (service.AgentRevisionStatus, error) {
	return service.AgentRevisionStatus{}, nil
}
func (*scopedRevisionService) Retry(context.Context, string, int64) (service.AgentRevisionStatus, error) {
	return service.AgentRevisionStatus{}, nil
}
func (*scopedRevisionService) Rollback(context.Context, string) (service.OperationStatus, error) {
	return service.OperationStatus{}, nil
}
func (s *scopedRevisionService) ListEvents(context.Context, service.RevisionEventQuery) (service.RevisionEventPage, error) {
	s.listCalls++
	return service.RevisionEventPage{}, nil
}
func (*scopedRevisionService) PullRemoteRevision(context.Context, string) (service.RemoteRevisionPull, error) {
	return service.RemoteRevisionPull{}, nil
}
func (*scopedRevisionService) StartRemoteRevision(context.Context, string, service.RemoteRevisionStart) (service.AgentRevisionStatus, error) {
	return service.AgentRevisionStatus{}, nil
}
func (*scopedRevisionService) ReportRemoteRevision(context.Context, string, service.RemoteRevisionReport) (service.AgentRevisionStatus, error) {
	return service.AgentRevisionStatus{}, nil
}
func (*scopedRevisionService) SaveMutationResponse(context.Context, string, string, string, []byte) error {
	return nil
}
func (*scopedRevisionService) LoadMutationResponse(context.Context, string, string, string) (map[string]any, bool, error) {
	return nil, false, nil
}
func (*scopedRevisionService) LoadMutationResponseByKey(context.Context, string, string) (map[string]any, bool, error) {
	return nil, false, nil
}
