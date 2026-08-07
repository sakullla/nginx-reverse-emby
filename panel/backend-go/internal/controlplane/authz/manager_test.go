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

func TestLoginNormalizesUsernameLikeCreateUser(t *testing.T) {
	store := newSecurityStore(t)
	manager := authz.NewManager(store, authz.Options{})
	if err := manager.EnsureDefaults(t.Context()); err != nil {
		t.Fatal(err)
	}
	user, err := manager.CreateUser(t.Context(), "MixedCaseUser", "", "correct-horse-battery", []string{authz.RoleReadonly})
	if err != nil {
		t.Fatal(err)
	}
	if user.Username != "mixedcaseuser" {
		t.Fatalf("created username = %q, want normalized lowercase", user.Username)
	}
	login, err := manager.Login(t.Context(), "  MIXEDCASEUSER  ", "correct-horse-battery")
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}
	if login.Actor.ID != user.ID || login.Actor.Username != user.Username {
		t.Fatalf("login = %+v, want user %s", login, user.ID)
	}
}

func TestBindResourceRejectsNonCanonicalKindAndIDWithoutSideEffects(t *testing.T) {
	store := newSecurityStore(t)
	manager := authz.NewManager(store, authz.Options{})
	if err := manager.EnsureDefaults(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveAgent(t.Context(), storage.AgentRow{ID: "edge-canonical"}); err != nil {
		t.Fatal(err)
	}
	groupA, err := manager.CreateResourceGroup(t.Context(), "canonical-a", "")
	if err != nil {
		t.Fatal(err)
	}
	groupB, err := manager.CreateResourceGroup(t.Context(), "canonical-b", "")
	if err != nil {
		t.Fatal(err)
	}
	admin := authz.BootstrapActor()
	if err := manager.BindResource(t.Context(), admin, "agent", "edge-canonical", groupA.ID); err != nil {
		t.Fatal(err)
	}
	for _, input := range []struct{ kind, id string }{{"agent ", "edge-canonical"}, {"agent", " edge-canonical"}} {
		if err := manager.BindResource(t.Context(), admin, input.kind, input.id, groupB.ID); !errors.Is(err, authz.ErrInvalidInput) {
			t.Fatalf("BindResource(%q, %q) error = %v, want invalid input", input.kind, input.id, err)
		}
	}
	binding, err := store.GetResourceBinding(t.Context(), "agent", "edge-canonical")
	if err != nil || binding.ResourceGroupID != groupA.ID {
		t.Fatalf("canonical binding = %+v error=%v, want unchanged group %s", binding, err, groupA.ID)
	}
	for _, input := range []struct{ kind, id string }{{"agent ", "edge-canonical"}, {"agent", " edge-canonical"}} {
		if _, err := store.GetResourceBinding(t.Context(), input.kind, input.id); !errors.Is(err, gorm.ErrRecordNotFound) {
			t.Fatalf("non-canonical binding %q/%q error = %v, want no row", input.kind, input.id, err)
		}
	}
}

func TestCustomSystemAdminListsAllQuotaPoliciesWithoutGroupGrants(t *testing.T) {
	store := newSecurityStore(t)
	manager := authz.NewManager(store, authz.Options{})
	if err := manager.EnsureDefaults(t.Context()); err != nil {
		t.Fatal(err)
	}
	role, err := manager.CreateRole(t.Context(), "custom-system-admin", "", []string{authz.PermissionSystemAdmin})
	if err != nil {
		t.Fatal(err)
	}
	user, err := manager.CreateUser(t.Context(), "custom-system-admin", "", "correct-horse-battery", []string{role.ID})
	if err != nil {
		t.Fatal(err)
	}
	login, err := manager.Login(t.Context(), user.Username, "correct-horse-battery")
	if err != nil {
		t.Fatal(err)
	}
	groups := make([]authz.ResourceGroup, 0, 2)
	for _, name := range []string{"admin-group-a", "admin-group-b"} {
		group, err := manager.CreateResourceGroup(t.Context(), name, "")
		if err != nil {
			t.Fatal(err)
		}
		groups = append(groups, group)
		if _, err := manager.UpsertQuotaPolicy(t.Context(), login.Actor, storage.QuotaPolicyRow{
			ID: "policy-" + group.ID, SubjectKind: "resource_group", SubjectID: group.ID,
			ResourceGroupID: group.ID, Metric: authz.QuotaRules, Limit: 3,
		}); err != nil {
			t.Fatalf("UpsertQuotaPolicy(%s) error = %v", group.ID, err)
		}
	}
	statuses, err := manager.ListQuotaStatus(t.Context(), login.Actor)
	if err != nil {
		t.Fatal(err)
	}
	seen := make(map[string]bool, len(statuses))
	for _, status := range statuses {
		seen[status.ResourceGroupID] = true
	}
	for _, group := range groups {
		if !seen[group.ID] {
			t.Fatalf("quota statuses = %+v, missing ungranted group %s", statuses, group.ID)
		}
	}
}

func TestScopedQuotaManagerCanManageOnlyVisibleGroupPolicies(t *testing.T) {
	store := newSecurityStore(t)
	manager := authz.NewManager(store, authz.Options{})
	if err := manager.EnsureDefaults(t.Context()); err != nil {
		t.Fatal(err)
	}
	role, err := manager.CreateRole(t.Context(), "scoped-quota-manager", "", []string{authz.PermissionQuotaManage})
	if err != nil {
		t.Fatal(err)
	}
	user, err := manager.CreateUser(t.Context(), "scoped-quota-manager", "", "correct-horse-battery", []string{role.ID})
	if err != nil {
		t.Fatal(err)
	}
	targetUser, err := manager.CreateUser(t.Context(), "quota-target", "", "correct-horse-battery", []string{authz.RoleReadonly})
	if err != nil {
		t.Fatal(err)
	}
	visible, err := manager.CreateResourceGroup(t.Context(), "quota-visible", "")
	if err != nil {
		t.Fatal(err)
	}
	hidden, err := manager.CreateResourceGroup(t.Context(), "quota-hidden", "")
	if err != nil {
		t.Fatal(err)
	}
	admin := authz.BootstrapActor()
	if err := manager.GrantResourceGroup(t.Context(), admin, "user", user.ID, visible.ID); err != nil {
		t.Fatal(err)
	}
	login, err := manager.Login(t.Context(), user.Username, "correct-horse-battery")
	if err != nil {
		t.Fatal(err)
	}
	visiblePolicy, err := manager.UpsertQuotaPolicy(t.Context(), login.Actor, storage.QuotaPolicyRow{
		ID: "visible-user-policy", SubjectKind: "user", SubjectID: targetUser.ID, ResourceGroupID: visible.ID, Metric: authz.QuotaRules, Limit: 2,
	})
	if err != nil {
		t.Fatalf("visible scoped policy error = %v", err)
	}
	if _, err := manager.UpsertQuotaPolicy(t.Context(), login.Actor, storage.QuotaPolicyRow{
		ID: "hidden-policy", SubjectKind: "resource_group", SubjectID: hidden.ID, ResourceGroupID: hidden.ID, Metric: authz.QuotaRules, Limit: 1,
	}); !errors.Is(err, authz.ErrForbidden) {
		t.Fatalf("hidden scoped policy error = %v, want forbidden", err)
	}
	if _, err := manager.UpsertQuotaPolicy(t.Context(), login.Actor, storage.QuotaPolicyRow{
		ID: "global-policy", SubjectKind: "user", SubjectID: targetUser.ID, Metric: authz.QuotaRules, Limit: 1,
	}); !errors.Is(err, authz.ErrForbidden) {
		t.Fatalf("global policy error = %v, want forbidden", err)
	}
	if _, err := manager.UpsertQuotaPolicy(t.Context(), admin, storage.QuotaPolicyRow{
		ID: "hidden-policy", SubjectKind: "resource_group", SubjectID: hidden.ID, ResourceGroupID: hidden.ID, Metric: authz.QuotaRules, Limit: 4,
	}); err != nil {
		t.Fatal(err)
	}
	statuses, err := manager.ListQuotaStatus(t.Context(), login.Actor)
	if err != nil {
		t.Fatalf("ListQuotaStatus() error = %v", err)
	}
	if len(statuses) != 1 || statuses[0].PolicyID != visiblePolicy.ID {
		t.Fatalf("visible quota statuses = %+v, want only %s", statuses, visiblePolicy.ID)
	}
	if err := manager.RevokeResourceGroupGrant(t.Context(), admin, "user", user.ID, visible.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.UpsertQuotaPolicy(t.Context(), login.Actor, storage.QuotaPolicyRow{
		ID: visiblePolicy.ID, SubjectKind: "user", SubjectID: targetUser.ID, ResourceGroupID: visible.ID, Metric: authz.QuotaRules, Limit: 3,
	}); !errors.Is(err, authz.ErrForbidden) {
		t.Fatalf("stale actor policy update error = %v, want forbidden", err)
	}
}

func TestBuiltinDefaultResourceGroupGrantsAreImmutableAcrossRestart(t *testing.T) {
	dataRoot := t.TempDir()
	store, err := storage.NewStore(storage.StoreConfig{Driver: "sqlite", DataRoot: dataRoot})
	if err != nil {
		t.Fatal(err)
	}
	manager := authz.NewManager(store, authz.Options{})
	if err := manager.EnsureDefaults(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := manager.RevokeResourceGroupGrant(t.Context(), authz.BootstrapActor(), "role", authz.RoleReadonly, authz.DefaultResourceGroup); !errors.Is(err, authz.ErrForbidden) {
		t.Fatalf("built-in revoke error = %v, want forbidden", err)
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
	grants, err := manager.ListResourceGroupGrants(t.Context(), authz.BootstrapActor())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, grant := range grants {
		if grant.SubjectKind == "role" && grant.SubjectID == authz.RoleReadonly && grant.ResourceGroupID == authz.DefaultResourceGroup {
			found = true
		}
	}
	if !found {
		t.Fatal("readonly built-in default grant missing after restart")
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
