//go:build !integration

package http

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/authz"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/service"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
	"gorm.io/gorm"
)

func TestTokenMatchesRequiresExactSecret(t *testing.T) {
	t.Parallel()
	if tokenMatches("secret", "secret-extra") || !tokenMatches("secret", "secret") {
		t.Fatal("token matching is not exact")
	}
}

func TestPanelAuthInfoRulesAndMonitorUseExactResourceScope(t *testing.T) {
	t.Parallel()
	access := newScopedAccessManager(t)
	created := &fakeOwnerRuleService{rules: []service.HTTPRule{
		{ID: 1, AgentID: "visible-edge", FrontendURL: "https://visible.example.com"},
	}}
	updates := make(chan service.AgentMonitorUpdate, 2)
	deps := Dependencies{
		Config: config.Config{
			PanelToken:    "secret",
			RegisterToken: "register-secret",
			LocalAgentID:  "visible-edge",
		},
		SystemService: fakeSystemService{info: service.SystemInfo{
			Role: "master", DataDir: "C:/srv/nre/data", DefaultAgentID: "visible-edge",
		}},
		AgentService: fakeOwnerAgentService{
			agents: []service.AgentSummary{
				{ID: "visible-edge", Name: "visible", Status: "online"},
				{ID: "hidden-edge", Name: "hidden", Status: "online"},
			},
			snapshot: service.AgentMonitorSnapshot{Agents: []service.AgentMonitorAgent{
				{ID: "visible-edge", Name: "visible"},
				{ID: "hidden-edge", Name: "hidden"},
			}},
			updates: updates,
		},
		RuleService:   created,
		AccessManager: access.Manager,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/panel-api/health", deps.handleHealth)
	mux.Handle("/panel-api/info", deps.requirePanelToken(http.HandlerFunc(deps.handleInfo)))
	mux.Handle("/panel-api/agents/{agentID}/rules", deps.requirePanelToken(http.HandlerFunc(deps.handleAgentRules)))
	mux.Handle("/panel-api/agents/{agentID}/rules/{id}", deps.requirePanelToken(http.HandlerFunc(deps.handleAgentRule)))
	mux.Handle("/panel-api/agents/monitor-stream", deps.requirePanelToken(http.HandlerFunc(deps.handleAgentMonitorStream)))

	health := httptest.NewRecorder()
	mux.ServeHTTP(health, httptest.NewRequest(http.MethodGet, "/panel-api/health", nil))
	if health.Code != http.StatusOK {
		t.Fatalf("health status = %d", health.Code)
	}

	unauth := httptest.NewRecorder()
	mux.ServeHTTP(unauth, httptest.NewRequest(http.MethodGet, "/panel-api/info", nil))
	if unauth.Code != http.StatusUnauthorized {
		t.Fatalf("info without token status = %d", unauth.Code)
	}

	infoReq := httptest.NewRequest(http.MethodGet, "/panel-api/info", nil)
	infoReq.Header.Set("X-Panel-Session", access.session)
	info := httptest.NewRecorder()
	mux.ServeHTTP(info, infoReq)
	if info.Code != http.StatusOK {
		t.Fatalf("info status = %d body=%s", info.Code, info.Body.String())
	}
	var infoBody map[string]any
	if err := json.Unmarshal(info.Body.Bytes(), &infoBody); err != nil {
		t.Fatal(err)
	}
	if _, hasDir := infoBody["data_dir"]; hasDir {
		t.Fatalf("scoped actor received data_dir: %v", infoBody)
	}
	if _, hasToken := infoBody["master_register_token"]; hasToken {
		t.Fatalf("scoped actor received register token: %v", infoBody)
	}

	kind, id, ok := deps.requestResource(http.MethodPut, "/panel-api/agents/visible-edge/rules/1")
	if !ok || kind != "http_rule" || id != "visible-edge:1" {
		t.Fatalf("nested rule resource = %s %s ok=%v", kind, id, ok)
	}

	allowBody := `{"frontend_url":"https://visible.example.com"}`
	allowReq := httptest.NewRequest(http.MethodPut, "/panel-api/agents/visible-edge/rules/1", strings.NewReader(allowBody))
	allowReq.Header.Set("X-Panel-Session", access.session)
	allowReq.Header.Set("Content-Type", "application/json")
	allow := httptest.NewRecorder()
	mux.ServeHTTP(allow, allowReq)
	if allow.Code != http.StatusOK {
		t.Fatalf("visible nested mutation status=%d body=%s", allow.Code, allow.Body.String())
	}

	denyReq := httptest.NewRequest(http.MethodPut, "/panel-api/agents/hidden-edge/rules/2", bytes.NewBufferString(allowBody))
	denyReq.Header.Set("X-Panel-Session", access.session)
	denyReq.Header.Set("Content-Type", "application/json")
	deny := httptest.NewRecorder()
	mux.ServeHTTP(deny, denyReq)
	if deny.Code != http.StatusForbidden {
		t.Fatalf("hidden nested mutation status=%d body=%s", deny.Code, deny.Body.String())
	}

	monitorReq := httptest.NewRequest(http.MethodGet, "/panel-api/agents/monitor-stream", nil)
	monitorReq.Header.Set("X-Panel-Session", access.session)
	ctx, cancel := context.WithCancel(monitorReq.Context())
	defer cancel()
	monitorReq = monitorReq.WithContext(ctx)
	monitor := &flushRecorder{ResponseRecorder: httptest.NewRecorder()}
	done := make(chan struct{})
	go func() {
		defer close(done)
		mux.ServeHTTP(monitor, monitorReq)
	}()
	deadline := time.Now().Add(2 * time.Second)
	for monitor.Body.Len() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if monitor.Body.Len() == 0 {
		t.Fatal("monitor snapshot was never written")
	}
	updates <- service.AgentMonitorUpdate{Agent: service.AgentMonitorAgent{ID: "hidden-edge", Name: "hidden-live"}}
	updates <- service.AgentMonitorUpdate{Agent: service.AgentMonitorAgent{ID: "visible-edge", Name: "visible-live"}}
	deadline = time.Now().Add(2 * time.Second)
	for !strings.Contains(monitor.Body.String(), `"visible-live"`) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
	body := monitor.Body.String()
	if !strings.Contains(body, `"id":"visible-edge"`) || strings.Contains(body, `"id":"hidden-edge"`) {
		t.Fatalf("monitor snapshot leaked unauthorized agents: %s", body)
	}
	if !strings.Contains(body, `"type":"update"`) || !strings.Contains(body, `"visible-live"`) {
		t.Fatalf("authorized monitor update missing: %s", body)
	}
	if strings.Contains(body, `"hidden-live"`) {
		t.Fatalf("monitor update leaked unauthorized agent: %s", body)
	}
}

type flushRecorder struct{ *httptest.ResponseRecorder }

func (flushRecorder) Flush() {}

type scopedAccess struct {
	*authz.Manager
	session string
}

func newScopedAccessManager(t *testing.T) scopedAccess {
	t.Helper()
	store := newHTTPAuthzStore(t)
	manager := authz.NewManager(store, authz.Options{})
	if err := manager.EnsureDefaults(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveAgent(t.Context(), storage.AgentRow{ID: "visible-edge"}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveAgent(t.Context(), storage.AgentRow{ID: "hidden-edge"}); err != nil {
		t.Fatal(err)
	}
	visible, err := manager.CreateResourceGroup(t.Context(), "visible-group", "")
	if err != nil {
		t.Fatal(err)
	}
	hidden, err := manager.CreateResourceGroup(t.Context(), "hidden-group", "")
	if err != nil {
		t.Fatal(err)
	}
	role, err := manager.CreateRole(t.Context(), "http-writer", "", []string{authz.PermissionResourceRead, authz.PermissionResourceWrite})
	if err != nil {
		t.Fatal(err)
	}
	user, err := manager.CreateUser(t.Context(), "scoped-http", "", "correct-horse-battery", []string{role.ID})
	if err != nil {
		t.Fatal(err)
	}
	admin := authz.BootstrapActor()
	if err := manager.GrantResourceGroup(t.Context(), admin, "user", user.ID, visible.ID); err != nil {
		t.Fatal(err)
	}
	if err := manager.BindResource(t.Context(), admin, "agent", "visible-edge", visible.ID); err != nil {
		t.Fatal(err)
	}
	if err := manager.BindResource(t.Context(), admin, "agent", "hidden-edge", hidden.ID); err != nil {
		t.Fatal(err)
	}
	login, err := manager.Login(t.Context(), user.Username, "correct-horse-battery")
	if err != nil {
		t.Fatal(err)
	}
	return scopedAccess{Manager: manager, session: login.Token}
}

func newHTTPAuthzStore(t *testing.T) *storage.GormStore {
	t.Helper()
	root := t.TempDir()
	store, err := storage.NewStore(storage.StoreConfig{Driver: "sqlite", DataRoot: root, LocalAgentID: "local"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	clone := t.TempDir()
	if err := os.WriteFile(filepath.Join(clone, "panel.db"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	dsn := filepath.Join(clone, "panel.db") + "?_pragma=journal_mode(MEMORY)&_pragma=synchronous(OFF)&_pragma=busy_timeout(5000)&_pragma=temp_store(MEMORY)"
	opened, err := storage.NewStore(storage.StoreConfig{
		Driver: "sqlite", DSN: dsn, DataRoot: clone, LocalAgentID: "local", SkipBootstrapSchema: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = opened.Close() })
	return opened
}

type fakeSystemService struct {
	info service.SystemInfo
}

func (f fakeSystemService) Info(context.Context) service.SystemInfo { return f.info }

type fakeOwnerAgentService struct {
	agents   []service.AgentSummary
	snapshot service.AgentMonitorSnapshot
	updates  chan service.AgentMonitorUpdate
}

func (f fakeOwnerAgentService) List(context.Context) ([]service.AgentSummary, error) {
	return f.agents, nil
}
func (f fakeOwnerAgentService) Get(context.Context, string) (service.AgentSummary, error) {
	return service.AgentSummary{}, service.ErrAgentNotFound
}
func (f fakeOwnerAgentService) GetByToken(context.Context, string) (service.AgentSummary, error) {
	return service.AgentSummary{}, service.ErrAgentUnauthorized
}
func (f fakeOwnerAgentService) Register(context.Context, service.RegisterRequest, string) (service.AgentSummary, error) {
	return service.AgentSummary{}, service.ErrAgentNotFound
}
func (f fakeOwnerAgentService) Update(context.Context, string, service.UpdateAgentRequest) (service.AgentSummary, error) {
	return service.AgentSummary{}, service.ErrAgentNotFound
}
func (f fakeOwnerAgentService) Delete(context.Context, string) (service.AgentSummary, error) {
	return service.AgentSummary{}, service.ErrAgentNotFound
}
func (f fakeOwnerAgentService) Stats(context.Context, string) (service.AgentStats, error) {
	return service.AgentStats{}, service.ErrAgentNotFound
}
func (f fakeOwnerAgentService) Apply(context.Context, string) (service.ApplyAgentResult, error) {
	return service.ApplyAgentResult{}, service.ErrAgentNotFound
}
func (f fakeOwnerAgentService) Heartbeat(context.Context, service.HeartbeatRequest, string) (service.HeartbeatReply, error) {
	return service.HeartbeatReply{}, service.ErrAgentUnauthorized
}
func (f fakeOwnerAgentService) MonitorSnapshot(context.Context) (service.AgentMonitorSnapshot, error) {
	return f.snapshot, nil
}
func (f fakeOwnerAgentService) SubscribeMonitorUpdates(context.Context) (<-chan service.AgentMonitorUpdate, func()) {
	ch := f.updates
	if ch == nil {
		ch = make(chan service.AgentMonitorUpdate)
	}
	return ch, func() {}
}

type fakeOwnerRuleService struct {
	rules []service.HTTPRule
}

func (f *fakeOwnerRuleService) List(_ context.Context, agentID string) ([]service.HTTPRule, error) {
	out := make([]service.HTTPRule, 0, len(f.rules))
	for _, rule := range f.rules {
		if rule.AgentID == agentID {
			out = append(out, rule)
		}
	}
	return out, nil
}
func (f *fakeOwnerRuleService) ListPage(context.Context, service.ListQuery) ([]service.HTTPRule, service.PageMeta, error) {
	return nil, service.PageMeta{}, nil
}
func (f *fakeOwnerRuleService) Get(context.Context, string, int) (service.HTTPRule, error) {
	return service.HTTPRule{}, io.EOF
}
func (f *fakeOwnerRuleService) Create(context.Context, string, service.HTTPRuleInput) (service.HTTPRule, error) {
	return service.HTTPRule{}, io.EOF
}
func (f *fakeOwnerRuleService) Update(_ context.Context, agentID string, id int, _ service.HTTPRuleInput) (service.HTTPRule, error) {
	return service.HTTPRule{ID: id, AgentID: agentID, FrontendURL: "https://visible.example.com"}, nil
}
func (f *fakeOwnerRuleService) Delete(context.Context, string, int) (service.HTTPRule, error) {
	return service.HTTPRule{}, io.EOF
}

func TestAccessUserAccountOwnerContract(t *testing.T) {
	t.Parallel()
	env := newUserAccountHTTP(t)
	bootstrap := "secret"
	password := "correct-horse-battery"

	created := env.do(t, http.MethodPost, "/api/access/users", bootstrapToken(bootstrap), `{
		"username":" Alice ",
		"display_name":"Alice",
		"password":"`+password+`",
		"role_ids":["administrator"]
	}`)
	if created.Code != http.StatusCreated {
		t.Fatalf("create user status=%d body=%s", created.Code, created.Body.String())
	}
	assertJSONHasNoSecretMaterial(t, created.Body.Bytes())
	alice := decodeUser(t, created)
	if alice.Username != "alice" || alice.DisplayName != "Alice" {
		t.Fatalf("created user = %+v, want normalized alice", alice)
	}
	if alice.ID == "" || !containsString(alice.RoleIDs, authz.RoleAdministrator) {
		t.Fatalf("created user missing id or administrator role: %+v", alice)
	}

	beforeUsers := env.listUsers(t, bootstrap, "")
	for _, item := range []struct {
		name string
		body string
		want []string
	}{
		{"empty", `{"username":"","display_name":"X","password":"` + password + `","role_ids":["administrator"]}`, []string{"username"}},
		{"short-password", `{"username":"bob","display_name":"Bob","password":"short","role_ids":["administrator"]}`, []string{"password"}},
		{"missing-role", `{"username":"bob","display_name":"Bob","password":"` + password + `","role_ids":[]}`, []string{"role_ids"}},
		{"unknown-role", `{"username":"bob","display_name":"Bob","password":"` + password + `","role_ids":["missing-role"]}`, []string{"role_ids"}},
		{"duplicate", `{"username":"ALICE","display_name":"Dup","password":"` + password + `","role_ids":["operator"]}`, []string{"username"}},
	} {
		t.Run("create-"+item.name, func(t *testing.T) {
			rec := env.do(t, http.MethodPost, "/api/access/users", bootstrapToken(bootstrap), item.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
			payload := decodeMap(t, rec)
			if payload["code"] != "invalid_input" {
				t.Fatalf("code=%v body=%s", payload["code"], rec.Body.String())
			}
			fields := fieldMap(payload["fields"])
			for _, key := range item.want {
				if strings.TrimSpace(fields[key]) == "" {
					t.Fatalf("fields=%v missing %s body=%s", fields, key, rec.Body.String())
				}
			}
			assertJSONHasNoSecretMaterial(t, rec.Body.Bytes())
		})
	}
	afterUsers := env.listUsers(t, bootstrap, "")
	if len(afterUsers) != len(beforeUsers) {
		t.Fatalf("failed creates inserted users: before=%d after=%d", len(beforeUsers), len(afterUsers))
	}

	operator := env.do(t, http.MethodPost, "/api/access/users", bootstrapToken(bootstrap), `{
		"username":"Operator",
		"display_name":"Ops",
		"password":"`+password+`",
		"role_ids":["operator"]
	}`)
	if operator.Code != http.StatusCreated {
		t.Fatalf("create operator status=%d body=%s", operator.Code, operator.Body.String())
	}
	ops := decodeUser(t, operator)

	renamed := env.do(t, http.MethodPut, "/api/access/users/"+alice.ID, bootstrapToken(bootstrap), `{"display_name":"Alicia"}`)
	if renamed.Code != http.StatusOK {
		t.Fatalf("update display_name status=%d body=%s", renamed.Code, renamed.Body.String())
	}
	got := decodeUser(t, renamed)
	if got.Username != "alice" || got.DisplayName != "Alicia" {
		t.Fatalf("updated user = %+v, want display Alicia without username change", got)
	}

	usernameWrite := env.do(t, http.MethodPut, "/api/access/users/"+alice.ID, bootstrapToken(bootstrap), `{"username":"renamed"}`)
	if usernameWrite.Code != http.StatusBadRequest {
		t.Fatalf("put username status=%d body=%s", usernameWrite.Code, usernameWrite.Body.String())
	}
	deleted := env.do(t, http.MethodDelete, "/api/access/users/"+alice.ID, bootstrapToken(bootstrap), "")
	if deleted.Code != http.StatusMethodNotAllowed {
		t.Fatalf("delete user status=%d body=%s", deleted.Code, deleted.Body.String())
	}

	roles := env.do(t, http.MethodPut, "/api/access/users/"+ops.ID, bootstrapToken(bootstrap), `{"role_ids":["readonly"]}`)
	if roles.Code != http.StatusOK {
		t.Fatalf("update roles status=%d body=%s", roles.Code, roles.Body.String())
	}
	if got = decodeUser(t, roles); !containsString(got.RoleIDs, authz.RoleReadonly) || containsString(got.RoleIDs, authz.RoleOperator) {
		t.Fatalf("updated roles = %v", got.RoleIDs)
	}

	disabled := env.do(t, http.MethodPut, "/api/access/users/"+ops.ID, bootstrapToken(bootstrap), `{"disabled":true}`)
	if disabled.Code != http.StatusOK {
		t.Fatalf("disable operator status=%d body=%s", disabled.Code, disabled.Body.String())
	}
	if got = decodeUser(t, disabled); !got.Disabled {
		t.Fatalf("operator not disabled: %+v", got)
	}

	lastAdmin := env.do(t, http.MethodPut, "/api/access/users/"+alice.ID, bootstrapToken(bootstrap), `{"disabled":true}`)
	if lastAdmin.Code == http.StatusOK {
		t.Fatalf("disabled last administrator: %s", lastAdmin.Body.String())
	}
	lastAdminBody := decodeMap(t, lastAdmin)
	if code, _ := lastAdminBody["code"].(string); code != "last_admin_protected" && code != "last_administrator_protected" {
		t.Fatalf("last admin code=%q body=%s", code, lastAdmin.Body.String())
	}
	demote := env.do(t, http.MethodPut, "/api/access/users/"+alice.ID, bootstrapToken(bootstrap), `{"role_ids":["operator"]}`)
	if demote.Code == http.StatusOK {
		t.Fatalf("demoted last administrator: %s", demote.Body.String())
	}
	current := env.getUser(t, bootstrap, alice.ID)
	if current.Disabled || current.Username != "alice" || !containsString(current.RoleIDs, authz.RoleAdministrator) {
		t.Fatalf("last admin mutated after rejected writes: %+v", current)
	}

	filtered := env.listUsers(t, bootstrap, " ali ")
	if len(filtered) != 1 || filtered[0].Username != "alice" {
		t.Fatalf("list q=ali users=%v", usernames(filtered))
	}

	first := env.login(t, "alice", password)
	second := env.login(t, "alice", password)
	if err := env.Manager.ChangePassword(t.Context(), alice.ID, password, "new-correct-horse"); err != nil {
		t.Fatalf("ChangePassword() error = %v", err)
	}
	assertHTTPSessionRejected(t, env, first)
	assertHTTPSessionRejected(t, env, second)
	if rec := env.do(t, http.MethodPost, "/api/auth/login", nil, `{"username":"alice","password":"`+password+`"}`); rec.Code != http.StatusUnauthorized {
		t.Fatalf("login old password status=%d body=%s", rec.Code, rec.Body.String())
	}
	changed := env.login(t, "alice", "new-correct-horse")
	if err := env.Manager.ResetPassword(t.Context(), alice.ID, "reset-correct-horse"); err != nil {
		t.Fatalf("ResetPassword() error = %v", err)
	}
	assertHTTPSessionRejected(t, env, changed)
	env.login(t, "alice", "reset-correct-horse")

	selfLogin := env.login(t, "alice", "reset-correct-horse")
	if err := env.Manager.ChangePassword(t.Context(), alice.ID, "wrong-horse-battery", "fresh-correct"); !errors.Is(err, authz.ErrInvalidCredentials) {
		t.Fatalf("ChangePassword(wrong current) error = %v", err)
	}
	if err := env.Manager.ChangePassword(t.Context(), alice.ID, "reset-correct-horse", "short"); !errors.Is(err, authz.ErrInvalidInput) {
		t.Fatalf("ChangePassword(short next) error = %v", err)
	}
	me := env.do(t, http.MethodGet, "/api/auth/me", sessionToken(selfLogin), "")
	if me.Code != http.StatusOK {
		t.Fatalf("session after failed password writes status=%d body=%s", me.Code, me.Body.String())
	}

	wrongCurrent := env.do(t, http.MethodPost, "/api/access/me/password", sessionToken(selfLogin), `{
		"current_password":"wrong-horse-battery",
		"new_password":"fresh-correct-horse"
	}`)
	if wrongCurrent.Code == http.StatusUnauthorized || wrongCurrent.Code < 400 {
		t.Fatalf("POST /access/me/password wrong current status=%d body=%s", wrongCurrent.Code, wrongCurrent.Body.String())
	}
	wrongBody := decodeMap(t, wrongCurrent)
	if wrongBody["code"] != "invalid_credentials" {
		t.Fatalf("wrong current code=%v body=%s", wrongBody["code"], wrongCurrent.Body.String())
	}
	if fields := fieldMap(wrongBody["fields"]); strings.TrimSpace(fields["current_password"]) == "" {
		t.Fatalf("wrong current fields=%v body=%s", wrongBody["fields"], wrongCurrent.Body.String())
	}
	assertJSONHasNoSecretMaterial(t, wrongCurrent.Body.Bytes())
	meAfterWrong := env.do(t, http.MethodGet, "/api/auth/me", sessionToken(selfLogin), "")
	if meAfterWrong.Code != http.StatusOK {
		t.Fatalf("session after wrong current password status=%d body=%s", meAfterWrong.Code, meAfterWrong.Body.String())
	}

	changeRoute := env.do(t, http.MethodPost, "/api/access/me/password", sessionToken(selfLogin), `{
		"current_password":"reset-correct-horse",
		"new_password":"route-correct-horse"
	}`)
	if changeRoute.Code != http.StatusOK {
		t.Fatalf("POST /access/me/password status=%d body=%s", changeRoute.Code, changeRoute.Body.String())
	}
	assertJSONHasNoSecretMaterial(t, changeRoute.Body.Bytes())
	assertHTTPSessionRejected(t, env, selfLogin)
	resetActor := env.login(t, "alice", "route-correct-horse")
	resetRoute := env.do(t, http.MethodPost, "/api/access/users/"+ops.ID+"/password", bootstrapToken(bootstrap), `{"new_password":"ops-reset-horse"}`)
	if resetRoute.Code != http.StatusOK {
		t.Fatalf("POST /access/users/{id}/password status=%d body=%s", resetRoute.Code, resetRoute.Body.String())
	}
	assertJSONHasNoSecretMaterial(t, resetRoute.Body.Bytes())
	_ = resetActor

	events := env.do(t, http.MethodGet, "/api/access/audit-events?limit=100", bootstrapToken(bootstrap), "")
	if events.Code != http.StatusOK {
		t.Fatalf("audit status=%d body=%s", events.Code, events.Body.String())
	}
	assertJSONHasNoSecretMaterial(t, events.Body.Bytes())
}

func TestUserAccountStoreProtectsUsernamePasswordAndLastAdmin(t *testing.T) {
	t.Parallel()
	store := newHTTPAuthzStore(t)
	manager := authz.NewManager(store, authz.Options{})
	if err := manager.EnsureDefaults(t.Context()); err != nil {
		t.Fatal(err)
	}
	admin, err := manager.CreateUser(t.Context(), "root", "Root", "correct-horse-battery", []string{authz.RoleAdministrator})
	if err != nil {
		t.Fatal(err)
	}
	_, err = manager.CreateUser(t.Context(), "ops", "Ops", "correct-horse-battery", []string{authz.RoleOperator})
	if err != nil {
		t.Fatal(err)
	}

	row, err := store.GetUser(t.Context(), admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	originalHash := row.PasswordHash
	row.Username = "renamed"
	if err := store.SaveUser(t.Context(), row); err != nil {
		t.Fatal(err)
	}
	reloaded, err := store.GetUser(t.Context(), admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Username != "root" {
		t.Fatalf("SaveUser changed username to %q", reloaded.Username)
	}

	dup := storage.UserRow{ID: "usr_dup", Username: "ROOT", DisplayName: "Dup", PasswordHash: originalHash, AuthRevision: 1, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	if err := store.CreateUser(t.Context(), dup); !errors.Is(err, storage.ErrUsernameTaken) {
		t.Fatalf("CreateUser(duplicate) error = %v, want username taken", err)
	}

	filtered, err := store.ListUsersFiltered(t.Context(), "Roo")
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered) != 1 || filtered[0].Username != "root" {
		t.Fatalf("ListUsersFiltered(Roo) = %+v", filtered)
	}

	login, err := manager.Login(t.Context(), "root", "correct-horse-battery")
	if err != nil {
		t.Fatal(err)
	}
	other, err := manager.Login(t.Context(), "root", "correct-horse-battery")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateUserPasswordAndRevokeSessions(t.Context(), "missing-user", "hash", time.Now().UTC()); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("UpdateUserPasswordAndRevokeSessions(missing) error = %v", err)
	}
	sessions, err := store.ListUserSessions(t.Context(), admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	active := 0
	for _, session := range sessions {
		if session.RevokedAt == nil {
			active++
		}
	}
	if active != 2 {
		t.Fatalf("active sessions after failed password write = %d, want 2", active)
	}
	if _, err := manager.AuthenticateSession(t.Context(), login.Token); err != nil {
		t.Fatalf("session revoked after failed password write: %v", err)
	}

	if err := store.UpdateUserPasswordAndRevokeSessions(t.Context(), admin.ID, "replacement-hash", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.AuthenticateSession(t.Context(), login.Token); !errors.Is(err, authz.ErrUnauthorized) {
		t.Fatalf("first session after password write error = %v", err)
	}
	if _, err := manager.AuthenticateSession(t.Context(), other.Token); !errors.Is(err, authz.ErrUnauthorized) {
		t.Fatalf("second session after password write error = %v", err)
	}

	ids, err := store.EnabledFullAdministratorIDs(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || ids[0] != admin.ID {
		t.Fatalf("EnabledFullAdministratorIDs() = %v, want [%s]", ids, admin.ID)
	}
	if err := store.RequireEnabledFullAdministrator(t.Context()); err != nil {
		t.Fatalf("RequireEnabledFullAdministrator() error = %v", err)
	}
}

type userAccountHTTP struct {
	*authz.Manager
	mux http.Handler
}

type userAccountUser struct {
	ID          string   `json:"id"`
	Username    string   `json:"username"`
	DisplayName string   `json:"display_name"`
	Disabled    bool     `json:"disabled"`
	RoleIDs     []string `json:"role_ids"`
}

func newUserAccountHTTP(t *testing.T) userAccountHTTP {
	t.Helper()
	store := newHTTPAuthzStore(t)
	manager := authz.NewManager(store, authz.Options{})
	if err := manager.EnsureDefaults(t.Context()); err != nil {
		t.Fatal(err)
	}
	deps := Dependencies{
		Config:        config.Config{PanelToken: "secret"},
		SystemService: fakeSystemService{info: service.SystemInfo{Role: "master"}},
		AgentService:  fakeOwnerAgentService{},
		RuleService:   &fakeOwnerRuleService{},
		AccessManager: manager,
	}
	mux := http.NewServeMux()
	mux.Handle("/api/auth/login", http.HandlerFunc(deps.handleLogin))
	mux.Handle("/api/auth/me", deps.requirePanelToken(http.HandlerFunc(deps.handleMe)))
	mux.Handle("/api/access/users", deps.requirePanelToken(http.HandlerFunc(deps.handleAccessUsers)))
	mux.Handle("/api/access/users/{id}", deps.requirePanelToken(http.HandlerFunc(deps.handleAccessUser)))
	mux.Handle("/api/access/audit-events", deps.requirePanelToken(http.HandlerFunc(deps.handleAuditEvents)))
	if mePassword, ok := any(deps).(interface {
		handleAccessMePassword(http.ResponseWriter, *http.Request)
	}); ok {
		mux.Handle("/api/access/me/password", deps.requirePanelToken(http.HandlerFunc(mePassword.handleAccessMePassword)))
	}
	if userPassword, ok := any(deps).(interface {
		handleAccessUserPassword(http.ResponseWriter, *http.Request)
	}); ok {
		mux.Handle("/api/access/users/{id}/password", deps.requirePanelToken(http.HandlerFunc(userPassword.handleAccessUserPassword)))
	}
	return userAccountHTTP{Manager: manager, mux: mux}
}

func (env userAccountHTTP) do(t *testing.T, method, path string, auth func(*http.Request), body string) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if auth != nil {
		auth(req)
	}
	rec := httptest.NewRecorder()
	env.mux.ServeHTTP(rec, req)
	return rec
}

func (env userAccountHTTP) listUsers(t *testing.T, token, q string) []userAccountUser {
	t.Helper()
	path := "/api/access/users"
	if strings.TrimSpace(q) != "" {
		path += "?q=" + url.QueryEscape(q)
	}
	rec := env.do(t, http.MethodGet, path, bootstrapToken(token), "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list users status=%d body=%s", rec.Code, rec.Body.String())
	}
	var payload struct {
		Users []userAccountUser `json:"users"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	assertJSONHasNoSecretMaterial(t, rec.Body.Bytes())
	return payload.Users
}

func (env userAccountHTTP) getUser(t *testing.T, token, id string) userAccountUser {
	t.Helper()
	rec := env.do(t, http.MethodGet, "/api/access/users/"+id, bootstrapToken(token), "")
	if rec.Code != http.StatusOK {
		t.Fatalf("get user status=%d body=%s", rec.Code, rec.Body.String())
	}
	return decodeUser(t, rec)
}

func (env userAccountHTTP) login(t *testing.T, username, password string) string {
	t.Helper()
	rec := env.do(t, http.MethodPost, "/api/auth/login", nil, `{"username":"`+username+`","password":"`+password+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("login %s status=%d body=%s", username, rec.Code, rec.Body.String())
	}
	assertJSONHasNoSecretMaterial(t, rec.Body.Bytes())
	var payload struct {
		Session struct {
			Token string `json:"token"`
		} `json:"session"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Session.Token == "" {
		t.Fatal("login returned empty session token")
	}
	return payload.Session.Token
}

func bootstrapToken(token string) func(*http.Request) {
	return func(r *http.Request) { r.Header.Set("X-Panel-Token", token) }
}

func sessionToken(token string) func(*http.Request) {
	return func(r *http.Request) { r.Header.Set("X-Panel-Session", token) }
}

func decodeUser(t *testing.T, rec *httptest.ResponseRecorder) userAccountUser {
	t.Helper()
	assertJSONHasNoSecretMaterial(t, rec.Body.Bytes())
	var payload struct {
		User userAccountUser `json:"user"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode user %s: %v", rec.Body.String(), err)
	}
	return payload.User
}

func decodeMap(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode %s: %v", rec.Body.String(), err)
	}
	return payload
}

func fieldMap(value any) map[string]string {
	raw, _ := value.(map[string]any)
	out := map[string]string{}
	for key, item := range raw {
		out[key] = fmt.Sprint(item)
	}
	return out
}

func usernames(users []userAccountUser) []string {
	out := make([]string, 0, len(users))
	for _, user := range users {
		out = append(out, user.Username)
	}
	return out
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func assertHTTPSessionRejected(t *testing.T, env userAccountHTTP, token string) {
	t.Helper()
	rec := env.do(t, http.MethodGet, "/api/auth/me", sessionToken(token), "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("session still authorized status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func assertJSONHasNoSecretMaterial(t *testing.T, raw []byte) {
	t.Helper()
	lower := strings.ToLower(string(raw))
	for _, marker := range []string{"password_hash", "correct-horse-battery", "new-correct-horse", "reset-correct-horse", "route-correct-horse", "ops-reset-horse"} {
		if strings.Contains(lower, marker) {
			t.Fatalf("payload contains secret material %q: %s", marker, raw)
		}
	}
}
