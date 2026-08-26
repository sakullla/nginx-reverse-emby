//go:build exhaustive && !integration

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
	if infoBody["timezone"] != "UTC" {
		t.Fatalf("info timezone = %#v", infoBody["timezone"])
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
	if testing.Short() {
		t.Skip("SQLite-backed HTTP authorization scenarios run in the full test tier")
	}
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
	agents        []service.AgentSummary
	authenticated service.AgentSummary
	snapshot      service.AgentMonitorSnapshot
	updates       chan service.AgentMonitorUpdate
}

func (f fakeOwnerAgentService) List(context.Context) ([]service.AgentSummary, error) {
	return f.agents, nil
}
func (f fakeOwnerAgentService) Get(context.Context, string) (service.AgentSummary, error) {
	return service.AgentSummary{}, service.ErrAgentNotFound
}
func (f fakeOwnerAgentService) GetByToken(context.Context, string) (service.AgentSummary, error) {
	if f.authenticated.ID != "" {
		return f.authenticated, nil
	}
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
	temp := env.do(t, http.MethodPost, "/api/access/users", bootstrapToken(bootstrap), `{
		"username":"TempUser",
		"display_name":"Temp",
		"password":"`+password+`",
		"role_ids":["readonly"]
	}`)
	if temp.Code != http.StatusCreated {
		t.Fatalf("create temp user status=%d body=%s", temp.Code, temp.Body.String())
	}
	tempUser := decodeUser(t, temp)
	deleted := env.do(t, http.MethodDelete, "/api/access/users/"+tempUser.ID, bootstrapToken(bootstrap), "")
	if deleted.Code != http.StatusOK {
		t.Fatalf("delete user status=%d body=%s", deleted.Code, deleted.Body.String())
	}
	if rec := env.do(t, http.MethodGet, "/api/access/users/"+tempUser.ID, bootstrapToken(bootstrap), ""); rec.Code != http.StatusNotFound {
		t.Fatalf("get deleted user status=%d body=%s", rec.Code, rec.Body.String())
	}
	lastAdminDelete := env.do(t, http.MethodDelete, "/api/access/users/"+alice.ID, bootstrapToken(bootstrap), "")
	if lastAdminDelete.Code != http.StatusConflict {
		t.Fatalf("delete last admin status=%d body=%s", lastAdminDelete.Code, lastAdminDelete.Body.String())
	}
	lastAdminDeleteBody := decodeMap(t, lastAdminDelete)
	if code, _ := lastAdminDeleteBody["code"].(string); code != "last_admin_protected" && code != "last_administrator_protected" {
		t.Fatalf("delete last admin code=%q body=%s", code, lastAdminDelete.Body.String())
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

func TestResourceGroupStoreProtectsDefaultCatalogAndUnbindFallback(t *testing.T) {
	t.Parallel()
	store := newHTTPAuthzStore(t)
	manager := authz.NewManager(store, authz.Options{})
	if err := manager.EnsureDefaults(t.Context()); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	media := storage.ResourceGroupRow{ID: "rg_media", Name: "media", Description: "old", CreatedAt: now, UpdatedAt: now}
	if err := store.CreateResourceGroup(t.Context(), media); err != nil {
		t.Fatal(err)
	}
	other := storage.ResourceGroupRow{ID: "rg_other", Name: "other", Description: "other", CreatedAt: now, UpdatedAt: now}
	if err := store.CreateResourceGroup(t.Context(), other); err != nil {
		t.Fatal(err)
	}

	updated, err := store.UpdateResourceGroup(t.Context(), media.ID, "media-library", "films")
	if err != nil {
		t.Fatal(err)
	}
	if updated.Name != "media-library" || updated.Description != "films" || updated.Builtin || updated.ID != media.ID {
		t.Fatalf("updated custom group = %+v", updated)
	}
	reloaded, err := store.GetResourceGroup(t.Context(), media.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Name != "media-library" || reloaded.Description != "films" {
		t.Fatalf("persisted custom group = %+v", reloaded)
	}
	if _, err := store.UpdateResourceGroup(t.Context(), media.ID, "other", "clash"); !errors.Is(err, storage.ErrResourceGroupNameTaken) {
		t.Fatalf("UpdateResourceGroup(duplicate name) error = %v", err)
	}

	defaultGroup, err := store.UpdateResourceGroup(t.Context(), authz.DefaultResourceGroup, authz.DefaultResourceGroup, "fallback display")
	if err != nil {
		t.Fatal(err)
	}
	if !defaultGroup.Builtin || defaultGroup.ID != authz.DefaultResourceGroup || defaultGroup.Name != authz.DefaultResourceGroup {
		t.Fatalf("default identity changed: %+v", defaultGroup)
	}
	if defaultGroup.Description != "fallback display" {
		t.Fatalf("default description = %q", defaultGroup.Description)
	}
	if _, err := store.UpdateResourceGroup(t.Context(), authz.DefaultResourceGroup, "renamed-default", "x"); !errors.Is(err, storage.ErrBuiltinResourceGroup) {
		t.Fatalf("UpdateResourceGroup(default name) error = %v", err)
	}
	if err := store.DeleteResourceGroup(t.Context(), authz.DefaultResourceGroup); !errors.Is(err, storage.ErrBuiltinResourceGroup) {
		t.Fatalf("DeleteResourceGroup(default) error = %v", err)
	}
	stillDefault, err := store.GetResourceGroup(t.Context(), authz.DefaultResourceGroup)
	if err != nil || !stillDefault.Builtin {
		t.Fatalf("default group after rejected delete = %+v err=%v", stillDefault, err)
	}

	filtered, err := store.ListResourceGroupsFiltered(t.Context(), "Media")
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered) != 1 || filtered[0].ID != media.ID {
		t.Fatalf("ListResourceGroupsFiltered(Media) = %+v", filtered)
	}

	user, err := manager.CreateUser(t.Context(), "viewer", "Viewer", "correct-horse-battery", []string{authz.RoleReadonly})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.GrantResourceGroup(t.Context(), storage.ResourceGroupGrantRow{
		ID: "grant_viewer_media", SubjectKind: "user", SubjectID: user.ID, ResourceGroupID: media.ID, CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.GrantResourceGroup(t.Context(), storage.ResourceGroupGrantRow{
		ID: "grant_viewer_media_dup", SubjectKind: "user", SubjectID: user.ID, ResourceGroupID: media.ID, CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	grants, err := store.ListResourceGroupGrantsForGroup(t.Context(), media.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(grants) != 1 || grants[0].SubjectID != user.ID {
		t.Fatalf("duplicate grant was not kept as one row: %+v", grants)
	}
	err = store.DeleteResourceGroup(t.Context(), media.ID)
	if !errors.Is(err, storage.ErrResourceGroupHasDependencies) {
		t.Fatalf("DeleteResourceGroup(with grant) error = %v", err)
	}
	var grantDeps *storage.ResourceGroupHasDependenciesError
	if !errors.As(err, &grantDeps) || grantDeps == nil || len(grantDeps.Grants) != 1 || len(grantDeps.Bindings) != 0 {
		t.Fatalf("grant dependency details = %+v", grantDeps)
	}
	if details := grantDeps.Details(); len(details["grants"].([]map[string]string)) != 1 {
		t.Fatalf("grant dependency details = %#v", details)
	}
	if _, err := store.GetResourceGroup(t.Context(), media.ID); err != nil {
		t.Fatalf("group deleted while grant dependency exists: %v", err)
	}

	if err := store.SaveAgent(t.Context(), storage.AgentRow{ID: "edge-media", Name: "Media Edge"}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveHTTPRules(t.Context(), "edge-media", []storage.HTTPRuleRow{{
		ID: 1, FrontendURL: "https://media.example.com", BackendURL: "http://127.0.0.1:8096", Enabled: true,
	}}); err != nil {
		t.Fatal(err)
	}
	catalog, err := store.ListResourceCatalog(t.Context(), "agent", "media")
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog) != 1 || catalog[0].ID != "edge-media" || catalog[0].ResourceGroupID != authz.DefaultResourceGroup {
		t.Fatalf("unbound catalog = %+v", catalog)
	}
	if groupID, err := store.ResolveResourceGroupID(t.Context(), "agent", "edge-media"); err != nil || groupID != authz.DefaultResourceGroup {
		t.Fatalf("ResolveResourceGroupID(unbound) = %q err=%v", groupID, err)
	}

	if err := store.BindResource(t.Context(), storage.ResourceBindingRow{
		ID: "bind_media", ResourceKind: "agent", ResourceID: "edge-media", ResourceGroupID: media.ID, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if groupID, err := store.ResolveResourceGroupID(t.Context(), "agent", "edge-media"); err != nil || groupID != media.ID {
		t.Fatalf("ResolveResourceGroupID(bound) = %q err=%v", groupID, err)
	}
	if groupID, err := store.ResolveResourceGroupID(t.Context(), "http_rule", "edge-media:1"); err != nil || groupID != media.ID {
		t.Fatalf("inherited http_rule group = %q err=%v", groupID, err)
	}
	members, err := store.ListResourceGroupMembers(t.Context(), media.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !catalogContains(members, "agent", "edge-media") || !catalogContains(members, "http_rule", "edge-media:1") {
		t.Fatalf("media members = %+v", members)
	}
	counts, err := store.ResourceGroupCounts(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if counts[media.ID].GrantCount != 1 || counts[media.ID].ResourceCount < 2 {
		t.Fatalf("media counts = %+v", counts[media.ID])
	}

	err = store.DeleteResourceGroup(t.Context(), media.ID)
	if !errors.Is(err, storage.ErrResourceGroupHasDependencies) {
		t.Fatalf("DeleteResourceGroup(with binding) error = %v", err)
	}
	var bindingDeps *storage.ResourceGroupHasDependenciesError
	if !errors.As(err, &bindingDeps) || bindingDeps == nil || len(bindingDeps.Bindings) == 0 {
		t.Fatalf("binding dependency details = %+v", bindingDeps)
	}

	if err := store.UnbindResource(t.Context(), "agent", "edge-media"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetResourceBinding(t.Context(), "agent", "edge-media"); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("explicit binding remained after unbind: %v", err)
	}
	if groupID, err := store.ResolveResourceGroupID(t.Context(), "agent", "edge-media"); err != nil || groupID != authz.DefaultResourceGroup {
		t.Fatalf("ResolveResourceGroupID(unbound after delete) = %q err=%v", groupID, err)
	}
	defaultMembers, err := store.ListResourceGroupMembers(t.Context(), authz.DefaultResourceGroup)
	if err != nil {
		t.Fatal(err)
	}
	if !catalogContains(defaultMembers, "agent", "edge-media") {
		t.Fatalf("unbound agent missing from default members: %+v", defaultMembers)
	}
	agents, err := store.ListAgents(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	foundAgent := false
	for _, agent := range agents {
		if agent.ID == "edge-media" {
			foundAgent = true
			if agent.Name != "Media Edge" {
				t.Fatalf("unbind changed agent business config: %+v", agent)
			}
		}
	}
	if !foundAgent {
		t.Fatal("agent row missing after unbind")
	}

	if err := store.BindResource(t.Context(), storage.ResourceBindingRow{
		ID: "bind_default", ResourceKind: "agent", ResourceID: "edge-media", ResourceGroupID: authz.DefaultResourceGroup, UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	explicitDefault, err := store.GetResourceBinding(t.Context(), "agent", "edge-media")
	if err != nil || explicitDefault.ResourceGroupID != authz.DefaultResourceGroup {
		t.Fatalf("bind-to-default row = %+v err=%v", explicitDefault, err)
	}
	if err := store.UnbindResource(t.Context(), "agent", "edge-media"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetResourceBinding(t.Context(), "agent", "edge-media"); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("unbind left a default binding row: %v", err)
	}

	if err := store.RevokeResourceGroupGrant(t.Context(), "user", user.ID, media.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteResourceGroup(t.Context(), media.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetResourceGroup(t.Context(), media.ID); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("deleted group still present: %v", err)
	}
}

func TestAccessResourceGroupOwnerContract(t *testing.T) {
	t.Parallel()
	env := newResourceGroupHTTP(t)
	bootstrap := "secret"
	password := "correct-horse-battery"

	created := env.do(t, http.MethodPost, "/api/access/resource-groups", bootstrapToken(bootstrap), `{"name":"studio","description":"edit me"}`)
	if created.Code != http.StatusCreated {
		t.Fatalf("create group status=%d body=%s", created.Code, created.Body.String())
	}
	group := decodeResourceGroup(t, created)
	if group.ID == "" || group.Name != "studio" || group.Builtin {
		t.Fatalf("created group = %+v", group)
	}

	listed := env.listGroups(t, bootstrap, "")
	if !resourceGroupNamed(listed, "studio") {
		t.Fatalf("created group missing from list: %+v", listed)
	}
	if filtered := env.listGroups(t, bootstrap, "Stu"); !resourceGroupNamed(filtered, "studio") {
		t.Fatalf("q=Stu missed created group: %+v", filtered)
	}

	reader := env.do(t, http.MethodPost, "/api/access/users", bootstrapToken(bootstrap), `{
		"username":"reader",
		"display_name":"Reader",
		"password":"`+password+`",
		"role_ids":["readonly"]
	}`)
	if reader.Code != http.StatusCreated {
		t.Fatalf("create reader status=%d body=%s", reader.Code, reader.Body.String())
	}
	readerUser := decodeUser(t, reader)

	firstGrant := env.do(t, http.MethodPost, "/api/access/resource-group-grants", bootstrapToken(bootstrap), `{
		"subject_kind":"user","subject_id":"`+readerUser.ID+`","resource_group_id":"`+group.ID+`"
	}`)
	if firstGrant.Code != http.StatusCreated {
		t.Fatalf("grant status=%d body=%s", firstGrant.Code, firstGrant.Body.String())
	}
	dupGrant := env.do(t, http.MethodPost, "/api/access/resource-group-grants", bootstrapToken(bootstrap), `{
		"subject_kind":"user","subject_id":"`+readerUser.ID+`","resource_group_id":"`+group.ID+`"
	}`)
	if dupGrant.Code != http.StatusCreated && dupGrant.Code != http.StatusOK {
		t.Fatalf("duplicate grant status=%d body=%s", dupGrant.Code, dupGrant.Body.String())
	}
	grants := env.listGrants(t, bootstrap)
	if countMatchingGrants(grants, "user", readerUser.ID, group.ID) != 1 {
		t.Fatalf("duplicate grant rows = %+v", grants)
	}

	if err := env.store.SaveAgent(t.Context(), storage.AgentRow{ID: "studio-edge", Name: "Studio"}); err != nil {
		t.Fatal(err)
	}
	bind := env.do(t, http.MethodPost, "/api/access/resource-bindings", bootstrapToken(bootstrap), `{
		"resource_kind":"agent","resource_id":"studio-edge","resource_group_id":"`+group.ID+`"
	}`)
	if bind.Code != http.StatusCreated {
		t.Fatalf("bind status=%d body=%s", bind.Code, bind.Body.String())
	}

	detail := env.getGroup(t, bootstrap, group.ID)
	if detail.Name != "studio" || detail.GrantCount != 1 || detail.ResourceCount < 1 {
		t.Fatalf("detail counts = %+v", detail.resourceGroupDTO)
	}
	if countMatchingGrants(detail.Grants, "user", readerUser.ID, group.ID) != 1 {
		t.Fatalf("detail grants = %+v", detail.Grants)
	}
	if !catalogContains(detail.Members["agent"], "agent", "studio-edge") {
		t.Fatalf("detail members = %+v", detail.Members)
	}
	catalog := env.listCatalog(t, bootstrap, "agent", "studio")
	if item, ok := catalogItem(catalog, "agent", "studio-edge"); !ok || item.ResourceGroupID != group.ID {
		t.Fatalf("catalog after bind = %+v", catalog)
	}

	updated := env.do(t, http.MethodPut, env.groupPath(group.ID), bootstrapToken(bootstrap), `{"name":"studio-west","description":"edited"}`)
	if updated.Code != http.StatusOK {
		t.Fatalf("update group status=%d body=%s", updated.Code, updated.Body.String())
	}
	got := decodeResourceGroup(t, updated)
	if got.ID != group.ID || got.Name != "studio-west" || got.Description != "edited" || got.Builtin {
		t.Fatalf("updated group = %+v", got)
	}
	if filtered := env.listGroups(t, bootstrap, "west"); !resourceGroupNamed(filtered, "studio-west") {
		t.Fatalf("q=west missed updated group: %+v", filtered)
	}

	readerToken := env.login(t, "reader", password)
	visible := env.do(t, http.MethodGet, "/api/access/resource-groups", sessionToken(readerToken), "")
	if visible.Code != http.StatusOK {
		t.Fatalf("reader list groups status=%d body=%s", visible.Code, visible.Body.String())
	}
	if !resourceGroupNamed(decodeResourceGroups(t, visible), "studio-west") {
		t.Fatalf("reader did not see granted group: %s", visible.Body.String())
	}

	deniedGrant := env.do(t, http.MethodPost, "/api/access/resource-group-grants", sessionToken(readerToken), `{
		"subject_kind":"user","subject_id":"`+readerUser.ID+`","resource_group_id":"`+group.ID+`"
	}`)
	if deniedGrant.Code != http.StatusForbidden {
		t.Fatalf("reader grant status=%d body=%s", deniedGrant.Code, deniedGrant.Body.String())
	}
	if decodeMap(t, deniedGrant)["code"] != "permission_denied" {
		t.Fatalf("reader grant body=%s", deniedGrant.Body.String())
	}
	deniedBind := env.do(t, http.MethodPost, "/api/access/resource-bindings", sessionToken(readerToken), `{
		"resource_kind":"agent","resource_id":"studio-edge","resource_group_id":"`+group.ID+`"
	}`)
	if deniedBind.Code != http.StatusForbidden || decodeMap(t, deniedBind)["code"] != "permission_denied" {
		t.Fatalf("reader bind status=%d body=%s", deniedBind.Code, deniedBind.Body.String())
	}
	deniedUpdate := env.do(t, http.MethodPut, env.groupPath(group.ID), sessionToken(readerToken), `{"description":"nope"}`)
	if deniedUpdate.Code != http.StatusForbidden || decodeMap(t, deniedUpdate)["code"] != "permission_denied" {
		t.Fatalf("reader update status=%d body=%s", deniedUpdate.Code, deniedUpdate.Body.String())
	}
	deniedDelete := env.do(t, http.MethodDelete, env.groupPath(group.ID), sessionToken(readerToken), "")
	if deniedDelete.Code != http.StatusForbidden || decodeMap(t, deniedDelete)["code"] != "permission_denied" {
		t.Fatalf("reader delete status=%d body=%s", deniedDelete.Code, deniedDelete.Body.String())
	}
	deniedUnbind := env.do(t, http.MethodDelete, "/api/access/resource-bindings", sessionToken(readerToken), `{
		"resource_kind":"agent","resource_id":"studio-edge"
	}`)
	if deniedUnbind.Code != http.StatusForbidden || decodeMap(t, deniedUnbind)["code"] != "permission_denied" {
		t.Fatalf("reader unbind status=%d body=%s", deniedUnbind.Code, deniedUnbind.Body.String())
	}

	blocked := env.do(t, http.MethodDelete, env.groupPath(group.ID), bootstrapToken(bootstrap), "")
	details := assertResourceGroupInUse(t, blocked, 1, 1)
	assertDependencyGrant(t, details, "user", readerUser.ID, group.ID)
	assertDependencyBinding(t, details, "agent", "studio-edge", group.ID)
	if env.getGroup(t, bootstrap, group.ID).Name != "studio-west" {
		t.Fatal("in-use delete removed or mutated group")
	}

	revoked := env.do(t, http.MethodDelete, "/api/access/resource-group-grants", bootstrapToken(bootstrap), `{
		"subject_kind":"user","subject_id":"`+readerUser.ID+`","resource_group_id":"`+group.ID+`"
	}`)
	if revoked.Code != http.StatusOK {
		t.Fatalf("revoke status=%d body=%s", revoked.Code, revoked.Body.String())
	}
	if countMatchingGrants(env.listGrants(t, bootstrap), "user", readerUser.ID, group.ID) != 0 {
		t.Fatal("grant remained after revoke")
	}
	hidden := env.do(t, http.MethodGet, "/api/access/resource-groups", sessionToken(readerToken), "")
	if hidden.Code != http.StatusOK {
		t.Fatalf("reader list after revoke status=%d body=%s", hidden.Code, hidden.Body.String())
	}
	if resourceGroupNamed(decodeResourceGroups(t, hidden), "studio-west") {
		t.Fatalf("reader still saw revoked group: %s", hidden.Body.String())
	}

	blockedBinding := env.do(t, http.MethodDelete, env.groupPath(group.ID), bootstrapToken(bootstrap), "")
	assertResourceGroupInUse(t, blockedBinding, 0, 1)

	unbound := env.do(t, http.MethodDelete, "/api/access/resource-bindings", bootstrapToken(bootstrap), `{
		"resource_kind":"agent","resource_id":"studio-edge"
	}`)
	if unbound.Code != http.StatusOK {
		t.Fatalf("unbind status=%d body=%s", unbound.Code, unbound.Body.String())
	}
	fallback := env.listCatalog(t, bootstrap, "agent", "studio")
	if item, ok := catalogItem(fallback, "agent", "studio-edge"); !ok || item.ResourceGroupID != authz.DefaultResourceGroup {
		t.Fatalf("unbind fallback catalog = %+v", fallback)
	}
	afterUnbind := env.getGroup(t, bootstrap, group.ID)
	if afterUnbind.ResourceCount != 0 || catalogContains(afterUnbind.Members["agent"], "agent", "studio-edge") {
		t.Fatalf("group still listed unbound member: %+v", afterUnbind)
	}
	readerCatalog := env.do(t, http.MethodGet, "/api/access/resources?kind=agent", sessionToken(readerToken), "")
	if readerCatalog.Code != http.StatusOK {
		t.Fatalf("reader catalog after unbind status=%d body=%s", readerCatalog.Code, readerCatalog.Body.String())
	}
	if item, ok := catalogItem(decodeResources(t, readerCatalog), "agent", "studio-edge"); !ok || item.ResourceGroupID != authz.DefaultResourceGroup {
		t.Fatalf("reader catalog after unbind = %s", readerCatalog.Body.String())
	}

	deleted := env.do(t, http.MethodDelete, env.groupPath(group.ID), bootstrapToken(bootstrap), "")
	if deleted.Code != http.StatusOK {
		t.Fatalf("delete unused group status=%d body=%s", deleted.Code, deleted.Body.String())
	}
	missing := env.do(t, http.MethodGet, env.groupPath(group.ID), bootstrapToken(bootstrap), "")
	if missing.Code != http.StatusNotFound || decodeMap(t, missing)["code"] != "not_found" {
		t.Fatalf("deleted group get status=%d body=%s", missing.Code, missing.Body.String())
	}
	if resourceGroupNamed(env.listGroups(t, bootstrap, ""), "studio-west") {
		t.Fatal("deleted group still listed")
	}

	protected := env.do(t, http.MethodDelete, env.groupPath(authz.DefaultResourceGroup), bootstrapToken(bootstrap), "")
	if protected.Code != http.StatusConflict {
		t.Fatalf("delete default status=%d body=%s", protected.Code, protected.Body.String())
	}
	protectedBody := decodeMap(t, protected)
	if protectedBody["code"] != "resource_group_protected" {
		t.Fatalf("delete default code=%v body=%s", protectedBody["code"], protected.Body.String())
	}
	if details, _ := protectedBody["details"].(map[string]any); details["reason"] != "builtin" || details["id"] != authz.DefaultResourceGroup {
		t.Fatalf("delete default details=%v body=%s", protectedBody["details"], protected.Body.String())
	}
	renameDefault := env.do(t, http.MethodPut, env.groupPath(authz.DefaultResourceGroup), bootstrapToken(bootstrap), `{"name":"renamed-default"}`)
	if renameDefault.Code != http.StatusConflict || decodeMap(t, renameDefault)["code"] != "resource_group_protected" {
		t.Fatalf("rename default status=%d body=%s", renameDefault.Code, renameDefault.Body.String())
	}
	describeDefault := env.do(t, http.MethodPut, env.groupPath(authz.DefaultResourceGroup), bootstrapToken(bootstrap), `{"description":"fallback display"}`)
	if describeDefault.Code != http.StatusOK {
		t.Fatalf("describe default status=%d body=%s", describeDefault.Code, describeDefault.Body.String())
	}
	described := decodeResourceGroup(t, describeDefault)
	if described.ID != authz.DefaultResourceGroup || described.Name != authz.DefaultResourceGroup || !described.Builtin || described.Description != "fallback display" {
		t.Fatalf("default after description update = %+v", described)
	}

	events := env.do(t, http.MethodGet, "/api/access/audit-events?limit=100", bootstrapToken(bootstrap), "")
	if events.Code != http.StatusOK {
		t.Fatalf("audit status=%d body=%s", events.Code, events.Body.String())
	}
	body := events.Body.String()
	for _, action := range []string{
		"access.resource_group.create",
		"access.resource_group.grant",
		"access.resource_group.revoke",
		"access.resource.move",
		"access.resource_group.update",
		"access.resource_group.delete",
		"access.resource.unbind",
	} {
		if !strings.Contains(body, action) {
			t.Fatalf("audit missing %s: %s", action, body)
		}
	}
}

type resourceGroupHTTP struct {
	*authz.Manager
	store *storage.GormStore
	mux   http.Handler
}

type resourceGroupDTO struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Description   string `json:"description"`
	Builtin       bool   `json:"builtin"`
	GrantCount    int64  `json:"grant_count"`
	ResourceCount int64  `json:"resource_count"`
}

type resourceGroupDetailDTO struct {
	resourceGroupDTO
	Grants  []storage.ResourceGroupGrantRow          `json:"grants"`
	Members map[string][]storage.ResourceCatalogItem `json:"members"`
}

func newResourceGroupHTTP(t *testing.T) resourceGroupHTTP {
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
	mux.Handle("/api/access/resource-groups", deps.requirePanelToken(http.HandlerFunc(deps.handleResourceGroups)))
	mux.Handle("/api/access/resource-groups/{id}", deps.requirePanelToken(http.HandlerFunc(deps.handleResourceGroup)))
	mux.Handle("/api/access/resources", deps.requirePanelToken(http.HandlerFunc(deps.handleResources)))
	mux.Handle("/api/access/resource-group-grants", deps.requirePanelToken(http.HandlerFunc(deps.handleResourceGroupGrants)))
	mux.Handle("/api/access/resource-bindings", deps.requirePanelToken(http.HandlerFunc(deps.handleResourceBindings)))
	mux.Handle("/api/access/audit-events", deps.requirePanelToken(http.HandlerFunc(deps.handleAuditEvents)))
	return resourceGroupHTTP{Manager: manager, store: store, mux: mux}
}

func (env resourceGroupHTTP) do(t *testing.T, method, path string, auth func(*http.Request), body string) *httptest.ResponseRecorder {
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

func (env resourceGroupHTTP) login(t *testing.T, username, password string) string {
	t.Helper()
	rec := env.do(t, http.MethodPost, "/api/auth/login", nil, `{"username":"`+username+`","password":"`+password+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("login %s status=%d body=%s", username, rec.Code, rec.Body.String())
	}
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

func (env resourceGroupHTTP) groupPath(id string) string {
	return "/api/access/resource-groups/" + url.PathEscape(id)
}

func (env resourceGroupHTTP) getGroup(t *testing.T, token, id string) resourceGroupDetailDTO {
	t.Helper()
	rec := env.do(t, http.MethodGet, env.groupPath(id), bootstrapToken(token), "")
	if rec.Code != http.StatusOK {
		t.Fatalf("get group %s status=%d body=%s", id, rec.Code, rec.Body.String())
	}
	return decodeResourceGroupDetail(t, rec)
}

func (env resourceGroupHTTP) listCatalog(t *testing.T, token, kind, q string) []storage.ResourceCatalogItem {
	t.Helper()
	path := "/api/access/resources"
	query := url.Values{}
	if strings.TrimSpace(kind) != "" {
		query.Set("kind", kind)
	}
	if strings.TrimSpace(q) != "" {
		query.Set("q", q)
	}
	if encoded := query.Encode(); encoded != "" {
		path += "?" + encoded
	}
	rec := env.do(t, http.MethodGet, path, bootstrapToken(token), "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list resources status=%d body=%s", rec.Code, rec.Body.String())
	}
	return decodeResources(t, rec)
}

func (env resourceGroupHTTP) listGroups(t *testing.T, token, q string) []resourceGroupDTO {
	t.Helper()
	path := "/api/access/resource-groups"
	if strings.TrimSpace(q) != "" {
		path += "?q=" + url.QueryEscape(q)
	}
	rec := env.do(t, http.MethodGet, path, bootstrapToken(token), "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list groups status=%d body=%s", rec.Code, rec.Body.String())
	}
	return decodeResourceGroups(t, rec)
}

func (env resourceGroupHTTP) listGrants(t *testing.T, token string) []storage.ResourceGroupGrantRow {
	t.Helper()
	rec := env.do(t, http.MethodGet, "/api/access/resource-group-grants", bootstrapToken(token), "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list grants status=%d body=%s", rec.Code, rec.Body.String())
	}
	var payload struct {
		Grants []storage.ResourceGroupGrantRow `json:"resource_group_grants"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	return payload.Grants
}

func decodeResourceGroup(t *testing.T, rec *httptest.ResponseRecorder) resourceGroupDTO {
	t.Helper()
	var payload struct {
		ResourceGroup resourceGroupDTO `json:"resource_group"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode group %s: %v", rec.Body.String(), err)
	}
	return payload.ResourceGroup
}

func decodeResourceGroupDetail(t *testing.T, rec *httptest.ResponseRecorder) resourceGroupDetailDTO {
	t.Helper()
	var payload struct {
		ResourceGroup resourceGroupDetailDTO `json:"resource_group"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode group detail %s: %v", rec.Body.String(), err)
	}
	return payload.ResourceGroup
}

func decodeResources(t *testing.T, rec *httptest.ResponseRecorder) []storage.ResourceCatalogItem {
	t.Helper()
	var payload struct {
		Resources []storage.ResourceCatalogItem `json:"resources"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode resources %s: %v", rec.Body.String(), err)
	}
	return payload.Resources
}

func decodeResourceGroups(t *testing.T, rec *httptest.ResponseRecorder) []resourceGroupDTO {
	t.Helper()
	var payload struct {
		ResourceGroups []resourceGroupDTO `json:"resource_groups"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode groups %s: %v", rec.Body.String(), err)
	}
	return payload.ResourceGroups
}

func resourceGroupNamed(groups []resourceGroupDTO, name string) bool {
	for _, group := range groups {
		if group.Name == name {
			return true
		}
	}
	return false
}

func countMatchingGrants(grants []storage.ResourceGroupGrantRow, subjectKind, subjectID, groupID string) int {
	count := 0
	for _, grant := range grants {
		if grant.SubjectKind == subjectKind && grant.SubjectID == subjectID && grant.ResourceGroupID == groupID {
			count++
		}
	}
	return count
}

func catalogContains(items []storage.ResourceCatalogItem, kind, id string) bool {
	_, ok := catalogItem(items, kind, id)
	return ok
}

func catalogItem(items []storage.ResourceCatalogItem, kind, id string) (storage.ResourceCatalogItem, bool) {
	for _, item := range items {
		if item.Kind == kind && item.ID == id {
			return item, true
		}
	}
	return storage.ResourceCatalogItem{}, false
}

func assertResourceGroupInUse(t *testing.T, rec *httptest.ResponseRecorder, wantGrants, wantBindings int) map[string]any {
	t.Helper()
	if rec.Code != http.StatusConflict {
		t.Fatalf("delete in-use status=%d body=%s", rec.Code, rec.Body.String())
	}
	body := decodeMap(t, rec)
	if body["code"] != "resource_group_in_use" {
		t.Fatalf("delete in-use code=%v body=%s", body["code"], rec.Body.String())
	}
	details, _ := body["details"].(map[string]any)
	if details == nil {
		t.Fatalf("delete in-use missing details: %s", rec.Body.String())
	}
	grants, _ := details["grants"].([]any)
	bindings, _ := details["bindings"].([]any)
	if len(grants) != wantGrants || len(bindings) != wantBindings {
		t.Fatalf("dependency classes grants=%d bindings=%d body=%s", len(grants), len(bindings), rec.Body.String())
	}
	return details
}

func assertDependencyGrant(t *testing.T, details map[string]any, subjectKind, subjectID, groupID string) {
	t.Helper()
	for _, raw := range anyMaps(details["grants"]) {
		if raw["subject_kind"] == subjectKind && raw["subject_id"] == subjectID && raw["resource_group_id"] == groupID {
			return
		}
	}
	t.Fatalf("missing grant dependency %s/%s/%s in %#v", subjectKind, subjectID, groupID, details["grants"])
}

func assertDependencyBinding(t *testing.T, details map[string]any, kind, id, groupID string) {
	t.Helper()
	for _, raw := range anyMaps(details["bindings"]) {
		if raw["resource_kind"] == kind && raw["resource_id"] == id && raw["resource_group_id"] == groupID {
			return
		}
	}
	t.Fatalf("missing binding dependency %s/%s/%s in %#v", kind, id, groupID, details["bindings"])
}

func anyMaps(value any) []map[string]any {
	items, _ := value.([]any)
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		if raw, ok := item.(map[string]any); ok {
			out = append(out, raw)
		}
	}
	return out
}
