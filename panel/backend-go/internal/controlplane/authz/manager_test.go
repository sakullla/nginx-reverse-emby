//go:build !integration

package authz_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
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
