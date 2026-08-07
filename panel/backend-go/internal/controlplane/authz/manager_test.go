package authz_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

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

func TestNestedResourceInheritsParentAgentGroupWhenBindingIsMissing(t *testing.T) {
	ctx := context.Background()
	store := newSecurityStore(t)
	if err := store.SaveAgent(ctx, storage.AgentRow{ID: "edge-hidden"}); err != nil {
		t.Fatal(err)
	}
	manager := authz.NewManager(store, authz.Options{})
	if err := manager.EnsureDefaults(ctx); err != nil {
		t.Fatal(err)
	}
	role, err := manager.CreateRole(ctx, "default-writer", "", []string{authz.PermissionResourceWrite})
	if err != nil {
		t.Fatal(err)
	}
	user, err := manager.CreateUser(ctx, "default-user", "", "correct-horse-battery", []string{role.ID})
	if err != nil {
		t.Fatal(err)
	}
	hidden, err := manager.CreateResourceGroup(ctx, "hidden", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.BindResource(ctx, authz.BootstrapActor(), "agent", "edge-hidden", hidden.ID); err != nil {
		t.Fatal(err)
	}
	login, err := manager.Login(ctx, user.Username, "correct-horse-battery")
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.AuthorizeResource(ctx, login.Actor, authz.PermissionResourceWrite, "http_rule", "edge-hidden:7"); !errors.Is(err, authz.ErrForbidden) {
		t.Fatalf("AuthorizeResource(child) error = %v, want forbidden inherited from parent", err)
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

func TestListQuotaStatusUsesIndependentPolicyUsage(t *testing.T) {
	store := newSecurityStore(t)
	now := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)
	shortReset := now.Add(time.Minute)
	longReset := now.Add(time.Hour)
	for _, policy := range []storage.QuotaPolicyRow{
		{ID: "short", SubjectKind: "resource_group", SubjectID: authz.DefaultResourceGroup, ResourceGroupID: authz.DefaultResourceGroup, Metric: authz.QuotaTraffic, Limit: 100, ResetAt: &shortReset, CreatedAt: now, UpdatedAt: now},
		{ID: "long", SubjectKind: "resource_group", SubjectID: authz.DefaultResourceGroup, ResourceGroupID: authz.DefaultResourceGroup, Metric: authz.QuotaTraffic, Limit: 100, ResetAt: &longReset, CreatedAt: now, UpdatedAt: now},
	} {
		if err := store.UpsertQuotaPolicy(t.Context(), policy); err != nil {
			t.Fatal(err)
		}
	}
	ctx := storage.WithQuotaActor(t.Context(), storage.QuotaActor{UserID: "system", Bootstrap: true})
	if _, err := store.ObserveQuota(ctx, "", authz.DefaultResourceGroup, authz.QuotaTraffic, 4, now); err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Minute)
	if _, err := store.ObserveQuota(ctx, "", authz.DefaultResourceGroup, authz.QuotaTraffic, 1, now); err != nil {
		t.Fatal(err)
	}
	manager := authz.NewManager(store, authz.Options{Now: func() time.Time { return now }})
	statuses, err := manager.ListQuotaStatus(t.Context(), authz.Actor{Bootstrap: true})
	if err != nil {
		t.Fatal(err)
	}
	currents := map[string]int64{}
	for _, status := range statuses {
		currents[status.PolicyID] = status.Current
	}
	if currents["short"] != 1 || currents["long"] != 5 {
		t.Fatalf("quota status currents = %+v, want short=1 long=5", currents)
	}
}

func TestUpsertQuotaPolicyRejectsUnsupportedPrincipalTrafficMetrics(t *testing.T) {
	store := newSecurityStore(t)
	manager := authz.NewManager(store, authz.Options{})
	if err := manager.EnsureDefaults(t.Context()); err != nil {
		t.Fatal(err)
	}
	user, err := manager.CreateUser(t.Context(), "quota-user", "", "correct-horse-battery", []string{authz.RoleOperator})
	if err != nil {
		t.Fatal(err)
	}
	for _, policy := range []storage.QuotaPolicyRow{
		{SubjectKind: "user", SubjectID: user.ID, Metric: authz.QuotaTraffic, Limit: 100},
		{SubjectKind: "role", SubjectID: authz.RoleOperator, Metric: authz.QuotaBandwidth, Limit: 100},
	} {
		if _, err := manager.UpsertQuotaPolicy(t.Context(), authz.BootstrapActor(), policy); !errors.Is(err, authz.ErrInvalidInput) {
			t.Fatalf("UpsertQuotaPolicy(%s/%s) error = %v, want invalid input", policy.SubjectKind, policy.Metric, err)
		}
	}
}

func TestLegacyCrossGroupCertificateBackfillFailsClosedAuthorization(t *testing.T) {
	dataRoot := t.TempDir()
	store, err := storage.NewStore(storage.StoreConfig{Driver: "sqlite", DataRoot: dataRoot})
	if err != nil {
		t.Fatal(err)
	}
	manager := authz.NewManager(store, authz.Options{})
	if err := manager.EnsureDefaults(t.Context()); err != nil {
		t.Fatal(err)
	}
	groupA, err := manager.CreateResourceGroup(t.Context(), "group-a", "")
	if err != nil {
		t.Fatal(err)
	}
	groupB, err := manager.CreateResourceGroup(t.Context(), "group-b", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range []storage.AgentRow{{ID: "edge-a"}, {ID: "edge-b"}} {
		if err := store.SaveAgent(t.Context(), row); err != nil {
			t.Fatal(err)
		}
	}
	if err := manager.BindResource(t.Context(), authz.BootstrapActor(), "agent", "edge-a", groupA.ID); err != nil {
		t.Fatal(err)
	}
	if err := manager.BindResource(t.Context(), authz.BootstrapActor(), "agent", "edge-b", groupB.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveManagedCertificates(t.Context(), []storage.ManagedCertificateRow{
		{ID: 7, Domain: "same.example.com", TargetAgentIDs: `["edge-a"]`},
		{ID: 8, Domain: "cross.example.com", TargetAgentIDs: `["edge-a","edge-b"]`},
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
	user, err := manager.CreateUser(t.Context(), "certificate-user", "", "correct-horse-battery", []string{authz.RoleOperator})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.GrantResourceGroup(t.Context(), authz.BootstrapActor(), "user", user.ID, groupA.ID); err != nil {
		t.Fatal(err)
	}
	login, err := manager.Login(t.Context(), user.Username, "correct-horse-battery")
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.AuthorizeResource(t.Context(), login.Actor, authz.PermissionResourceWrite, "certificate", "7"); err != nil {
		t.Fatalf("same-group certificate authorization error = %v", err)
	}
	if err := manager.AuthorizeResource(t.Context(), login.Actor, authz.PermissionResourceWrite, "certificate", "8"); !errors.Is(err, authz.ErrForbidden) {
		t.Fatalf("cross-group certificate authorization error = %v, want forbidden", err)
	}
}

func newSecurityStore(t *testing.T) *storage.GormStore {
	t.Helper()
	store, err := storage.NewStore(storage.StoreConfig{Driver: "sqlite", DataRoot: t.TempDir()})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}
