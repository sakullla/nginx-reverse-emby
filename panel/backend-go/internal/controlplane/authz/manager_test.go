//go:build !integration

package authz_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/authz"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
	"gorm.io/gorm"
)

func TestSessionReloadsRolePermissionsAndResourceScope(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := newSecurityStore(t)
	if err := store.SaveAgent(ctx, storage.AgentRow{ID: "edge-1"}); err != nil {
		t.Fatal(err)
	}
	manager := authz.NewManager(store, authz.Options{})
	if err := manager.EnsureDefaults(ctx); err != nil {
		t.Fatalf("EnsureDefaults() error = %v", err)
	}
	role, err := manager.CreateRole(ctx, "media-reader", "", []string{authz.PermissionResourceRead})
	if err != nil {
		t.Fatalf("CreateRole() error = %v", err)
	}
	user, err := manager.CreateUser(ctx, "alice", "Alice", "correct-horse-battery", []string{role.ID})
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}
	group, err := manager.CreateResourceGroup(ctx, "media", "")
	if err != nil {
		t.Fatalf("CreateResourceGroup() error = %v", err)
	}
	if err := manager.GrantResourceGroup(ctx, authz.BootstrapActor(), "user", user.ID, group.ID); err != nil {
		t.Fatalf("GrantResourceGroup() error = %v", err)
	}
	if err := manager.BindResource(ctx, authz.BootstrapActor(), "agent", "edge-1", group.ID); err != nil {
		t.Fatalf("BindResource() error = %v", err)
	}
	login, err := manager.Login(ctx, "alice", "correct-horse-battery")
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}
	if err := manager.AuthorizeResource(ctx, login.Actor, authz.PermissionResourceRead, "agent", "edge-1"); err != nil {
		t.Fatalf("AuthorizeResource() error = %v", err)
	}
	if _, err := manager.SetRolePermissions(ctx, role.ID, nil); err != nil {
		t.Fatalf("SetRolePermissions() error = %v", err)
	}
	refreshed, err := manager.AuthenticateSession(ctx, login.Token)
	if err != nil {
		t.Fatalf("AuthenticateSession() error = %v", err)
	}
	if err := manager.AuthorizeResource(ctx, refreshed, authz.PermissionResourceRead, "agent", "edge-1"); !errors.Is(err, authz.ErrForbidden) {
		t.Fatalf("AuthorizeResource() after role update error = %v, want forbidden", err)
	}
}

func TestRevokedResourceGroupGrantInvalidatesExistingSessionScope(t *testing.T) {
	ctx := t.Context()
	store := newSecurityStore(t)
	if err := store.SaveAgent(ctx, storage.AgentRow{ID: "edge-1"}); err != nil {
		t.Fatal(err)
	}
	manager := authz.NewManager(store, authz.Options{})
	if err := manager.EnsureDefaults(ctx); err != nil {
		t.Fatal(err)
	}
	role, err := manager.CreateRole(ctx, "grant-reader", "", []string{authz.PermissionResourceRead})
	if err != nil {
		t.Fatal(err)
	}
	user, err := manager.CreateUser(ctx, "grant-user", "", "correct-horse-battery", []string{role.ID})
	if err != nil {
		t.Fatal(err)
	}
	group, err := manager.CreateResourceGroup(ctx, "granted-group", "")
	if err != nil {
		t.Fatal(err)
	}
	admin := authz.BootstrapActor()
	if err := manager.GrantResourceGroup(ctx, admin, "user", user.ID, group.ID); err != nil {
		t.Fatal(err)
	}
	if err := manager.BindResource(ctx, admin, "agent", "edge-1", group.ID); err != nil {
		t.Fatal(err)
	}
	login, err := manager.Login(ctx, user.Username, "correct-horse-battery")
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.AuthorizeResource(ctx, login.Actor, authz.PermissionResourceRead, "agent", "edge-1"); err != nil {
		t.Fatal(err)
	}
	grants, err := manager.ListResourceGroupGrants(ctx, admin)
	if err != nil {
		t.Fatal(err)
	}
	matchingGrants := 0
	for _, grant := range grants {
		if grant.SubjectKind == "user" && grant.SubjectID == user.ID && grant.ResourceGroupID == group.ID {
			matchingGrants++
		}
	}
	if matchingGrants != 1 {
		t.Fatalf("ListResourceGroupGrants() = %+v, want target grant once", grants)
	}
	if err := manager.RevokeResourceGroupGrant(ctx, admin, "user", user.ID, group.ID); err != nil {
		t.Fatal(err)
	}
	refreshed, err := manager.AuthenticateSession(ctx, login.Token)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.AuthorizeResource(ctx, refreshed, authz.PermissionResourceRead, "agent", "edge-1"); !errors.Is(err, authz.ErrForbidden) {
		t.Fatalf("AuthorizeResource() after revoke error = %v, want forbidden", err)
	}
	grants, err = manager.ListResourceGroupGrants(ctx, admin)
	if err != nil {
		t.Fatal(err)
	}
	for _, grant := range grants {
		if grant.SubjectKind == "user" && grant.SubjectID == user.ID && grant.ResourceGroupID == group.ID {
			t.Fatalf("target grant remains after revoke: %+v", grant)
		}
	}
}

func TestSecurityAdministrationMutationsRequireSystemAdminAndCanonicalResource(t *testing.T) {
	ctx := t.Context()
	store := newSecurityStore(t)
	manager := authz.NewManager(store, authz.Options{})
	if err := manager.EnsureDefaults(ctx); err != nil {
		t.Fatal(err)
	}
	role, err := manager.CreateRole(ctx, "access-manager", "", []string{authz.PermissionAccessManage, authz.PermissionQuotaManage})
	if err != nil {
		t.Fatal(err)
	}
	user, err := manager.CreateUser(ctx, "access-manager", "", "correct-horse-battery", []string{role.ID})
	if err != nil {
		t.Fatal(err)
	}
	group, err := manager.CreateResourceGroup(ctx, "hidden-group", "")
	if err != nil {
		t.Fatal(err)
	}
	login, err := manager.Login(ctx, user.Username, "correct-horse-battery")
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.GrantResourceGroup(ctx, login.Actor, "user", user.ID, group.ID); !errors.Is(err, authz.ErrForbidden) {
		t.Fatalf("GrantResourceGroup() error = %v, want forbidden", err)
	}
	if err := manager.BindResource(ctx, login.Actor, "agent", "missing-agent", group.ID); !errors.Is(err, authz.ErrForbidden) {
		t.Fatalf("BindResource(non-admin) error = %v, want forbidden", err)
	}
	if _, err := manager.UpsertQuotaPolicy(ctx, login.Actor, storage.QuotaPolicyRow{SubjectKind: "resource_group", SubjectID: group.ID, ResourceGroupID: group.ID, Metric: authz.QuotaRules, Limit: 1}); !errors.Is(err, authz.ErrForbidden) {
		t.Fatalf("UpsertQuotaPolicy() error = %v, want forbidden", err)
	}
	if err := manager.BindResource(ctx, authz.BootstrapActor(), "agent", "missing-agent", group.ID); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("BindResource(missing resource) error = %v, want not found", err)
	}
}

func TestConsumeQuotaUsesStrictestPolicyAtomically(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := newSecurityStore(t)
	manager := authz.NewManager(store, authz.Options{})
	if err := manager.EnsureDefaults(ctx); err != nil {
		t.Fatalf("EnsureDefaults() error = %v", err)
	}
	user, err := manager.CreateUser(ctx, "operator", "", "correct-horse-battery", []string{authz.RoleOperator})
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}
	login, err := manager.Login(ctx, user.Username, "correct-horse-battery")
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}
	for _, policy := range []storage.QuotaPolicyRow{
		{SubjectKind: "user", SubjectID: user.ID, ResourceGroupID: authz.DefaultResourceGroup, Metric: authz.QuotaRules, Limit: 2, RecoveryCondition: "delete a rule"},
		{SubjectKind: "role", SubjectID: authz.RoleOperator, ResourceGroupID: authz.DefaultResourceGroup, Metric: authz.QuotaRules, Limit: 1, RecoveryCondition: "delete a rule"},
	} {
		if _, err := manager.UpsertQuotaPolicy(ctx, authz.BootstrapActor(), policy); err != nil {
			t.Fatalf("UpsertQuotaPolicy() error = %v", err)
		}
	}
	var wg sync.WaitGroup
	errorsSeen := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := manager.ConsumeQuota(ctx, login.Actor, authz.DefaultResourceGroup, authz.QuotaRules, 1)
			errorsSeen <- err
		}()
	}
	wg.Wait()
	close(errorsSeen)
	successes := 0
	denied := 0
	for err := range errorsSeen {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, storage.ErrQuotaExceeded):
			denied++
		default:
			t.Fatalf("ConsumeQuota() error = %v", err)
		}
	}
	if successes != 1 || denied != 1 {
		t.Fatalf("quota results successes=%d denied=%d, want 1/1", successes, denied)
	}
}

func TestAuthorizationPersistsAcrossReopenAndNestedGroupsFailClosed(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	open := func() *storage.GormStore {
		t.Helper()
		store, err := storage.NewStore(storage.StoreConfig{Driver: "sqlite", DataRoot: root})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = store.Close() })
		return store
	}
	store := open()
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
	role, err := manager.CreateRole(t.Context(), "scoped-reader", "", []string{authz.PermissionResourceRead})
	if err != nil {
		t.Fatal(err)
	}
	user, err := manager.CreateUser(t.Context(), "scoped-user", "", "correct-horse-battery", []string{role.ID})
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
	if err := manager.AuthorizeResource(t.Context(), login.Actor, authz.PermissionResourceRead, "agent", "visible-edge"); err != nil {
		t.Fatal(err)
	}
	if err := manager.AuthorizeResource(t.Context(), login.Actor, authz.PermissionResourceRead, "agent", "hidden-edge"); !errors.Is(err, authz.ErrForbidden) {
		t.Fatalf("cross-group fail-closed = %v", err)
	}
	if err := manager.AuthorizeResource(t.Context(), login.Actor, authz.PermissionResourceRead, "http_rule", "visible-edge:1"); err != nil {
		t.Fatalf("parent agent http_rule inheritance = %v", err)
	}
	if err := manager.AuthorizeResource(t.Context(), login.Actor, authz.PermissionResourceRead, "http_rule", "hidden-edge:1"); !errors.Is(err, authz.ErrForbidden) {
		t.Fatalf("hidden parent http_rule fail-closed = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	reopened := authz.NewManager(open(), authz.Options{})
	restored, err := reopened.AuthenticateSession(t.Context(), login.Token)
	if err != nil {
		t.Fatal(err)
	}
	if err := reopened.AuthorizeResource(t.Context(), restored, authz.PermissionResourceRead, "agent", "visible-edge"); err != nil {
		t.Fatalf("restart persistence failed: %v", err)
	}
	if err := reopened.AuthorizeResource(t.Context(), restored, authz.PermissionResourceRead, "agent", "hidden-edge"); !errors.Is(err, authz.ErrForbidden) {
		t.Fatalf("restart cross-group fail-closed = %v", err)
	}
	if err := reopened.AuthorizeResource(t.Context(), restored, authz.PermissionResourceRead, "http_rule", "visible-edge:1"); err != nil {
		t.Fatalf("restart parent agent http_rule inheritance = %v", err)
	}
	if err := reopened.AuthorizeResource(t.Context(), restored, authz.PermissionResourceRead, "http_rule", "hidden-edge:1"); !errors.Is(err, authz.ErrForbidden) {
		t.Fatalf("restart hidden parent http_rule fail-closed = %v", err)
	}
}

func TestCreateUserRejectsFieldErrorsWithoutCreating(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	manager := newAccessManager(t)
	before, err := manager.ListUsers(ctx)
	if err != nil {
		t.Fatal(err)
	}

	_, err = manager.CreateUser(ctx, "  ", "Admin", "short", nil)
	fields := fieldMessages(t, err)
	if fields["username"] == "" || fields["password"] == "" || fields["role_ids"] == "" {
		t.Fatalf("CreateUser() fields = %#v, want username/password/role_ids", fields)
	}

	_, err = manager.CreateUser(ctx, "alice", "Alice", "correct-horse-battery", []string{"missing-role"})
	fields = fieldMessages(t, err)
	if fields["role_ids"] == "" {
		t.Fatalf("CreateUser(unknown role) fields = %#v", fields)
	}

	created, err := manager.CreateUser(ctx, " Alice ", "Alice", "correct-horse-battery", []string{authz.RoleAdministrator})
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}
	if created.Username != "alice" {
		t.Fatalf("CreateUser() username = %q, want alice", created.Username)
	}
	assertUserHasNoSecretMaterial(t, created)

	_, err = manager.CreateUser(ctx, "ALICE", "Dup", "correct-horse-battery", []string{authz.RoleOperator})
	fields = fieldMessages(t, err)
	if fields["username"] == "" {
		t.Fatalf("CreateUser(duplicate) fields = %#v", fields)
	}

	after, err := manager.ListUsers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before)+1 {
		t.Fatalf("ListUsers() len = %d, want %d", len(after), len(before)+1)
	}
}

func TestSetUserDisplayNameDoesNotChangeUsername(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	manager := newAccessManager(t)
	user, err := manager.CreateUser(ctx, "bob", "Bob", "correct-horse-battery", []string{authz.RoleOperator})
	if err != nil {
		t.Fatal(err)
	}
	updated, err := manager.SetUserDisplayName(ctx, user.ID, "  Bobby  ")
	if err != nil {
		t.Fatalf("SetUserDisplayName() error = %v", err)
	}
	if updated.Username != "bob" || updated.DisplayName != "Bobby" {
		t.Fatalf("updated user = %+v, want username bob display Bobby", updated)
	}
	reloaded, err := manager.GetUser(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Username != "bob" || reloaded.DisplayName != "Bobby" {
		t.Fatalf("GetUser() = %+v after display name update", reloaded)
	}
}

func TestLastAdministratorProtection(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	manager := newAccessManager(t)
	admin, err := manager.CreateUser(ctx, "root", "Root", "correct-horse-battery", []string{authz.RoleAdministrator})
	if err != nil {
		t.Fatal(err)
	}
	operator, err := manager.CreateUser(ctx, "ops", "Ops", "correct-horse-battery", []string{authz.RoleOperator})
	if err != nil {
		t.Fatal(err)
	}
	adminLogin, err := manager.Login(ctx, admin.Username, "correct-horse-battery")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.DisableUser(ctx, admin.ID, true); !errors.Is(err, authz.ErrLastAdministrator) {
		t.Fatalf("DisableUser(last admin) error = %v, want last administrator", err)
	}
	if _, err := manager.SetUserRoles(ctx, admin.ID, []string{authz.RoleOperator}); !errors.Is(err, authz.ErrLastAdministrator) {
		t.Fatalf("SetUserRoles(last admin) error = %v, want last administrator", err)
	}
	if _, err := manager.SetUserRoles(ctx, admin.ID, nil); !errors.Is(err, authz.ErrInvalidInput) {
		t.Fatalf("SetUserRoles(empty) error = %v, want invalid input", err)
	}
	current, err := manager.GetUser(ctx, admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Disabled || !containsString(current.RoleIDs, authz.RoleAdministrator) {
		t.Fatalf("last admin mutated after rejected protection: %+v", current)
	}
	if _, err := manager.AuthenticateSession(ctx, adminLogin.Token); err != nil {
		t.Fatalf("AuthenticateSession() after rejected last-admin disable error = %v", err)
	}

	second, err := manager.CreateUser(ctx, "root-2", "Root 2", "correct-horse-battery", []string{authz.RoleAdministrator})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.DisableUser(ctx, admin.ID, true); err != nil {
		t.Fatalf("DisableUser(second admin remains) error = %v", err)
	}
	if _, err := manager.DisableUser(ctx, second.ID, true); !errors.Is(err, authz.ErrLastAdministrator) {
		t.Fatalf("DisableUser(remaining admin) error = %v, want last administrator", err)
	}
	if _, err := manager.DisableUser(ctx, operator.ID, true); err != nil {
		t.Fatalf("DisableUser(operator) error = %v", err)
	}

	starRole, err := manager.CreateRole(ctx, "star-admin", "", []string{authz.PermissionAll})
	if err != nil {
		t.Fatal(err)
	}
	starUser, err := manager.CreateUser(ctx, "star", "Star", "correct-horse-battery", []string{starRole.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.DisableUser(ctx, second.ID, true); err != nil {
		t.Fatalf("DisableUser(builtin admin while * remains) error = %v", err)
	}
	if _, err := manager.SetUserRoles(ctx, starUser.ID, []string{authz.RoleReadonly}); !errors.Is(err, authz.ErrLastAdministrator) {
		t.Fatalf("SetUserRoles(* holder) error = %v, want last administrator", err)
	}
}

func TestChangeAndResetPasswordRevokeSessions(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	manager := newAccessManager(t)
	user, err := manager.CreateUser(ctx, "alice", "Alice", "correct-horse-battery", []string{authz.RoleOperator})
	if err != nil {
		t.Fatal(err)
	}
	first, err := manager.Login(ctx, user.Username, "correct-horse-battery")
	if err != nil {
		t.Fatal(err)
	}
	second, err := manager.Login(ctx, user.Username, "correct-horse-battery")
	if err != nil {
		t.Fatal(err)
	}
	actor := authz.BootstrapActor()
	err = manager.AuditedMutation(ctx, actor, "access.user.password.change", "user", user.ID, "", map[string]any{"password": "correct-horse-battery", "new_password": "new-correct-horse"}, func(tx *authz.Manager) (string, error) {
		return user.ID, tx.ChangePassword(ctx, user.ID, "correct-horse-battery", "new-correct-horse")
	})
	if err != nil {
		t.Fatalf("ChangePassword() error = %v", err)
	}
	assertSessionRevoked(t, manager, first.Token)
	assertSessionRevoked(t, manager, second.Token)
	if _, err := manager.Login(ctx, user.Username, "correct-horse-battery"); !errors.Is(err, authz.ErrInvalidCredentials) {
		t.Fatalf("Login(old password) error = %v, want invalid credentials", err)
	}
	afterChange, err := manager.Login(ctx, user.Username, "new-correct-horse")
	if err != nil {
		t.Fatalf("Login(new password) error = %v", err)
	}

	err = manager.AuditedMutation(ctx, actor, "access.user.password.reset", "user", user.ID, "", map[string]any{"new_password": "reset-correct-horse"}, func(tx *authz.Manager) (string, error) {
		return user.ID, tx.ResetPassword(ctx, user.ID, "reset-correct-horse")
	})
	if err != nil {
		t.Fatalf("ResetPassword() error = %v", err)
	}
	assertSessionRevoked(t, manager, afterChange.Token)
	if _, err := manager.Login(ctx, user.Username, "new-correct-horse"); !errors.Is(err, authz.ErrInvalidCredentials) {
		t.Fatalf("Login(pre-reset password) error = %v, want invalid credentials", err)
	}
	if _, err := manager.Login(ctx, user.Username, "reset-correct-horse"); err != nil {
		t.Fatalf("Login(reset password) error = %v", err)
	}
	assertAuditHasNoSecretMaterial(t, manager)
	reloaded, err := manager.GetUser(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertUserHasNoSecretMaterial(t, reloaded)
}

func TestPasswordFailuresLeaveCredentialsAndSessions(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	manager := newAccessManager(t)
	user, err := manager.CreateUser(ctx, "alice", "Alice", "correct-horse-battery", []string{authz.RoleOperator})
	if err != nil {
		t.Fatal(err)
	}
	login, err := manager.Login(ctx, user.Username, "correct-horse-battery")
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.ChangePassword(ctx, user.ID, "wrong-horse-battery", "new-correct-horse"); !errors.Is(err, authz.ErrInvalidCredentials) {
		t.Fatalf("ChangePassword(wrong current) error = %v, want invalid credentials", err)
	}
	shortErr := manager.ChangePassword(ctx, user.ID, "correct-horse-battery", "short")
	if !errors.Is(shortErr, authz.ErrInvalidInput) {
		t.Fatalf("ChangePassword(short next) error = %v, want invalid input", shortErr)
	}
	if fields := fieldMessages(t, shortErr); fields["new_password"] == "" {
		t.Fatalf("ChangePassword(short next) fields = %#v", fields)
	}
	if err := manager.ResetPassword(ctx, user.ID, "tiny"); !errors.Is(err, authz.ErrInvalidInput) {
		t.Fatalf("ResetPassword(short next) error = %v, want invalid input", err)
	}
	if _, err := manager.AuthenticateSession(ctx, login.Token); err != nil {
		t.Fatalf("AuthenticateSession() after failed password writes error = %v", err)
	}
	if _, err := manager.Login(ctx, user.Username, "correct-horse-battery"); err != nil {
		t.Fatalf("Login(original password) after failures error = %v", err)
	}
}

var securityStoreTemplate struct {
	once sync.Once
	data []byte
	err  error
}

func newSecurityStore(t *testing.T) *storage.GormStore {
	t.Helper()
	securityStoreTemplate.once.Do(func() {
		root, err := os.MkdirTemp("", "nre-authz-template-")
		if err != nil {
			securityStoreTemplate.err = err
			return
		}
		defer os.RemoveAll(root)
		store, err := storage.NewStore(storage.StoreConfig{Driver: "sqlite", DataRoot: root, LocalAgentID: "local"})
		if err != nil {
			securityStoreTemplate.err = err
			return
		}
		if err := store.Close(); err != nil {
			securityStoreTemplate.err = err
			return
		}
		securityStoreTemplate.data, securityStoreTemplate.err = os.ReadFile(filepath.Join(root, "panel.db"))
	})
	if securityStoreTemplate.err != nil {
		t.Fatal(securityStoreTemplate.err)
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "panel.db"), securityStoreTemplate.data, 0o600); err != nil {
		t.Fatal(err)
	}
	dsn := filepath.Join(root, "panel.db") + "?_pragma=journal_mode(MEMORY)&_pragma=synchronous(OFF)&_pragma=busy_timeout(5000)&_pragma=temp_store(MEMORY)"
	store, err := storage.NewStore(storage.StoreConfig{Driver: "sqlite", DSN: dsn, DataRoot: root, LocalAgentID: "local", SkipBootstrapSchema: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func newAccessManager(t *testing.T) *authz.Manager {
	t.Helper()
	manager := authz.NewManager(newSecurityStore(t), authz.Options{})
	if err := manager.EnsureDefaults(t.Context()); err != nil {
		t.Fatalf("EnsureDefaults() error = %v", err)
	}
	return manager
}

func fieldMessages(t *testing.T, err error) map[string]string {
	t.Helper()
	var fieldErr *authz.FieldError
	if !errors.As(err, &fieldErr) {
		t.Fatalf("error %v is not FieldError", err)
	}
	return fieldErr.Fields
}

func assertUserHasNoSecretMaterial(t *testing.T, user authz.User) {
	t.Helper()
	encoded, err := json.Marshal(user)
	if err != nil {
		t.Fatal(err)
	}
	lower := strings.ToLower(string(encoded))
	for _, marker := range []string{"password", "password_hash", "correct-horse", "new-correct", "reset-correct"} {
		if strings.Contains(lower, strings.ToLower(marker)) {
			t.Fatalf("user payload contains secret material %q: %s", marker, encoded)
		}
	}
}

func assertSessionRevoked(t *testing.T, manager *authz.Manager, token string) {
	t.Helper()
	if _, err := manager.AuthenticateSession(t.Context(), token); !errors.Is(err, authz.ErrUnauthorized) {
		t.Fatalf("AuthenticateSession() error = %v, want unauthorized", err)
	}
}

func assertAuditHasNoSecretMaterial(t *testing.T, manager *authz.Manager) {
	t.Helper()
	events, err := manager.ListAuditEvents(t.Context(), 100)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(events)
	if err != nil {
		t.Fatal(err)
	}
	lower := strings.ToLower(string(encoded))
	for _, marker := range []string{"correct-horse-battery", "new-correct-horse", "reset-correct-horse"} {
		if strings.Contains(lower, marker) {
			t.Fatalf("audit events contain password material %q: %s", marker, encoded)
		}
	}
	foundRedaction := false
	for _, event := range events {
		if event.Metadata == nil {
			continue
		}
		for key, value := range event.Metadata {
			if strings.Contains(strings.ToLower(key), "password") {
				if value != "[REDACTED]" {
					t.Fatalf("audit metadata %q = %#v, want [REDACTED]", key, value)
				}
				foundRedaction = true
			}
		}
	}
	if !foundRedaction {
		t.Fatal("expected password keys in audit metadata to be redacted")
	}
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
