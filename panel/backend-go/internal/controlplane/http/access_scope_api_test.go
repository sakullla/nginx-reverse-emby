package http

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/authz"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/service"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
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
	if err := manager.GrantResourceGroup(t.Context(), "user", user.ID, group.ID); err != nil {
		t.Fatalf("GrantResourceGroup() error = %v", err)
	}
	if err := manager.BindResource(t.Context(), "agent", "edge-a", group.ID); err != nil {
		t.Fatalf("BindResource(edge-a) error = %v", err)
	}
	hidden, err := manager.CreateResourceGroup(t.Context(), "hidden-group", "")
	if err != nil {
		t.Fatalf("CreateResourceGroup(hidden) error = %v", err)
	}
	if err := manager.BindResource(t.Context(), "agent", "edge-b", hidden.ID); err != nil {
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
