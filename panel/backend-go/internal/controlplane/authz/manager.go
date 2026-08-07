package authz

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

const (
	RoleAdministrator = "administrator"
	RoleOperator      = "operator"
	RoleReadonly      = "readonly"

	DefaultResourceGroup = "default"

	PermissionAll                = "*"
	PermissionResourceRead       = "resource.read"
	PermissionResourceWrite      = "resource.write"
	PermissionAccessManage       = "access.manage"
	PermissionQuotaManage        = "quota.manage"
	PermissionAuditRead          = "audit.read"
	PermissionSecretUse          = "secret.use"
	PermissionSecretMetadataRead = "secret.metadata.read"
	PermissionSecretManage       = "secret.manage"
	PermissionSystemAdmin        = "system.admin"

	QuotaRules        = "rule_count"
	QuotaApplications = "application_count"
	QuotaPublicPorts  = "public_port_count"
	QuotaBandwidth    = "bandwidth_bytes_per_second"
	QuotaTraffic      = "traffic_bytes"
)

var (
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrUnauthorized       = errors.New("authentication required")
	ErrForbidden          = errors.New("permission denied")
	ErrInvalidInput       = errors.New("invalid access-control input")
	ErrAuditUnavailable   = errors.New("audit persistence unavailable")
)

type Store interface {
	UpsertPermission(context.Context, storage.PermissionRow) error
	ListPermissions(context.Context) ([]storage.PermissionRow, error)
	UpsertBuiltinRole(context.Context, storage.RoleRow, []string) error
	CreateRole(context.Context, storage.RoleRow, []string) error
	ReplaceRolePermissions(context.Context, string, []string) error
	ListRoles(context.Context) ([]storage.RoleRow, error)
	GetRole(context.Context, string) (storage.RoleRow, []string, error)
	BindRole(context.Context, storage.RoleBindingRow) error
	UnbindRole(context.Context, string, string) error
	UserRoleIDs(context.Context, string) ([]string, error)
	UserPermissions(context.Context, string) ([]string, error)
	CreateUser(context.Context, storage.UserRow) error
	CreateUserWithRoleBindings(context.Context, storage.UserRow, []storage.RoleBindingRow) error
	SaveUser(context.Context, storage.UserRow) error
	GetUser(context.Context, string) (storage.UserRow, error)
	GetUserByUsername(context.Context, string) (storage.UserRow, error)
	ListUsers(context.Context) ([]storage.UserRow, error)
	CreateSession(context.Context, storage.SessionRow) error
	GetSessionByTokenHash(context.Context, string) (storage.SessionRow, error)
	TouchSession(context.Context, string, time.Time) error
	RevokeSession(context.Context, string, time.Time) error
	RevokeUserSessions(context.Context, string, time.Time) error
	UpsertBuiltinResourceGroup(context.Context, storage.ResourceGroupRow) error
	CreateResourceGroup(context.Context, storage.ResourceGroupRow) error
	ListResourceGroups(context.Context) ([]storage.ResourceGroupRow, error)
	GetResourceGroup(context.Context, string) (storage.ResourceGroupRow, error)
	GrantResourceGroup(context.Context, storage.ResourceGroupGrantRow) error
	ListResourceGroupGrants(context.Context) ([]storage.ResourceGroupGrantRow, error)
	RevokeResourceGroupGrant(context.Context, string, string, string) error
	VisibleResourceGroupIDs(context.Context, string) ([]string, error)
	BindResource(context.Context, storage.ResourceBindingRow) error
	GetResourceBinding(context.Context, string, string) (storage.ResourceBindingRow, error)
	ResourceExists(context.Context, string, string) (bool, error)
	UpsertQuotaPolicy(context.Context, storage.QuotaPolicyRow) error
	GetQuotaPolicy(context.Context, string) (storage.QuotaPolicyRow, error)
	ListQuotaPolicies(context.Context) ([]storage.QuotaPolicyRow, error)
	ListQuotaUsage(context.Context) ([]storage.QuotaUsageRow, error)
	ListQuotaPolicyUsage(context.Context) ([]storage.QuotaPolicyUsageRow, error)
	ConsumeQuota(context.Context, string, string, string, int64, time.Time) (storage.QuotaDecision, error)
	AppendAuditEvent(context.Context, storage.AuditEventRow) error
	ListAuditEvents(context.Context, int) ([]storage.AuditEventRow, error)
}

var dummyPasswordHash = func() []byte {
	hash, err := bcrypt.GenerateFromPassword([]byte("fixed-invalid-login-password"), bcrypt.DefaultCost)
	if err != nil {
		panic(err)
	}
	return hash
}()

type Options struct {
	SessionTTL time.Duration
	Now        func() time.Time
}

type Manager struct {
	store      Store
	sessionTTL time.Duration
	now        func() time.Time
}

type transactionalStore interface {
	SecurityTransaction(context.Context, func(*storage.GormStore) error) error
}

type Actor struct {
	ID                    string   `json:"id"`
	Username              string   `json:"username"`
	SessionID             string   `json:"session_id,omitempty"`
	Bootstrap             bool     `json:"bootstrap"`
	Permissions           []string `json:"permissions"`
	VisibleResourceGroups []string `json:"visible_resource_groups"`

	permissionSet map[string]struct{}
	groupSet      map[string]struct{}
}

type User struct {
	ID           string    `json:"id"`
	Username     string    `json:"username"`
	DisplayName  string    `json:"display_name"`
	Disabled     bool      `json:"disabled"`
	AuthRevision uint64    `json:"auth_revision"`
	RoleIDs      []string  `json:"role_ids"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type Role struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Builtin     bool     `json:"builtin"`
	Revision    uint64   `json:"revision"`
	Permissions []string `json:"permissions"`
}

type ResourceGroup struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Builtin     bool   `json:"builtin"`
}

type LoginResult struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
	Actor     Actor     `json:"actor"`
}

type AuditEvent struct {
	ID              string         `json:"id"`
	ActorID         string         `json:"actor_id"`
	SessionID       string         `json:"session_id,omitempty"`
	Action          string         `json:"action"`
	TargetKind      string         `json:"target_kind"`
	TargetID        string         `json:"target_id"`
	ResourceGroupID string         `json:"resource_group_id,omitempty"`
	CorrelationID   string         `json:"correlation_id,omitempty"`
	Result          string         `json:"result"`
	ErrorClass      string         `json:"error_class,omitempty"`
	Metadata        map[string]any `json:"metadata"`
	CreatedAt       time.Time      `json:"created_at"`
}

type QuotaStatus struct {
	PolicyID          string     `json:"policy_id"`
	SubjectKind       string     `json:"subject_kind"`
	SubjectID         string     `json:"subject_id"`
	ResourceGroupID   string     `json:"resource_group_id,omitempty"`
	Metric            string     `json:"metric"`
	Current           int64      `json:"current"`
	Limit             int64      `json:"limit"`
	ExceedAction      string     `json:"exceed_action"`
	RecoveryCondition string     `json:"recovery_condition"`
	ResetAt           *time.Time `json:"reset_at,omitempty"`
}

func NewManager(store Store, options Options) *Manager {
	ttl := options.SessionTTL
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	now := options.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &Manager{store: store, sessionTTL: ttl, now: now}
}

func (m *Manager) EnsureDefaults(ctx context.Context) error {
	if m == nil || m.store == nil {
		return fmt.Errorf("access manager store is required")
	}
	permissions := []storage.PermissionRow{
		{ID: PermissionAll, Description: "all control-plane permissions"},
		{ID: PermissionResourceRead, Description: "read resources in granted resource groups"},
		{ID: PermissionResourceWrite, Description: "change resources in granted resource groups"},
		{ID: PermissionAccessManage, Description: "manage users, roles and resource groups"},
		{ID: PermissionQuotaManage, Description: "manage quota policies"},
		{ID: PermissionAuditRead, Description: "read security audit events"},
		{ID: PermissionSecretUse, Description: "use referenced secrets"},
		{ID: PermissionSecretMetadataRead, Description: "read secret metadata"},
		{ID: PermissionSecretManage, Description: "create and rotate secrets"},
		{ID: PermissionSystemAdmin, Description: "perform privileged control-plane operations"},
	}
	for _, permission := range permissions {
		if err := m.store.UpsertPermission(ctx, permission); err != nil {
			return err
		}
	}
	now := m.now()
	roles := []struct {
		row         storage.RoleRow
		permissions []string
	}{
		{storage.RoleRow{ID: RoleAdministrator, Name: RoleAdministrator, Description: "full control-plane administrator", Builtin: true, Revision: 1, CreatedAt: now, UpdatedAt: now}, []string{PermissionAll}},
		{storage.RoleRow{ID: RoleOperator, Name: RoleOperator, Description: "operate granted resources", Builtin: true, Revision: 1, CreatedAt: now, UpdatedAt: now}, []string{PermissionResourceRead, PermissionResourceWrite, PermissionSecretUse}},
		{storage.RoleRow{ID: RoleReadonly, Name: RoleReadonly, Description: "read granted resources", Builtin: true, Revision: 1, CreatedAt: now, UpdatedAt: now}, []string{PermissionResourceRead}},
	}
	for _, role := range roles {
		if err := m.store.UpsertBuiltinRole(ctx, role.row, role.permissions); err != nil {
			return err
		}
	}
	group := storage.ResourceGroupRow{ID: DefaultResourceGroup, Name: DefaultResourceGroup, Description: "resources not explicitly assigned to another group", Builtin: true, CreatedAt: now, UpdatedAt: now}
	if err := m.store.UpsertBuiltinResourceGroup(ctx, group); err != nil {
		return err
	}
	for _, roleID := range []string{RoleOperator, RoleReadonly} {
		if err := m.store.GrantResourceGroup(ctx, storage.ResourceGroupGrantRow{ID: newID("grant"), SubjectKind: "role", SubjectID: roleID, ResourceGroupID: DefaultResourceGroup, CreatedAt: now}); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) CreateUser(ctx context.Context, username, displayName, password string, roleIDs []string) (User, error) {
	username = strings.ToLower(strings.TrimSpace(username))
	if username == "" || len(password) < 10 {
		return User{}, fmt.Errorf("%w: username and a password of at least 10 characters are required", ErrInvalidInput)
	}
	roleIDs = uniqueStrings(roleIDs)
	for _, roleID := range roleIDs {
		if _, _, err := m.store.GetRole(ctx, roleID); err != nil {
			return User{}, err
		}
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return User{}, err
	}
	now := m.now()
	row := storage.UserRow{ID: newID("usr"), Username: username, DisplayName: strings.TrimSpace(displayName), PasswordHash: string(hash), AuthRevision: 1, CreatedAt: now, UpdatedAt: now}
	bindings := make([]storage.RoleBindingRow, 0, len(roleIDs))
	for _, roleID := range roleIDs {
		bindings = append(bindings, storage.RoleBindingRow{ID: newID("rb"), UserID: row.ID, RoleID: roleID, CreatedAt: now})
	}
	if err := m.store.CreateUserWithRoleBindings(ctx, row, bindings); err != nil {
		return User{}, err
	}
	return m.GetUser(ctx, row.ID)
}

func (m *Manager) transaction(ctx context.Context, fn func(*Manager) error) error {
	if txStore, ok := m.store.(transactionalStore); ok {
		return txStore.SecurityTransaction(ctx, func(store *storage.GormStore) error {
			txManager := *m
			txManager.store = store
			return fn(&txManager)
		})
	}
	return fn(m)
}

// AuditedMutation commits the security mutation and its success audit event in
// one database transaction. Failed mutations are rolled back and receive a
// separate durable failure event.
func (m *Manager) AuditedMutation(ctx context.Context, actor Actor, action, targetKind, targetID, resourceGroupID string, metadata map[string]any, mutate func(*Manager) (string, error)) error {
	canonicalTargetID := targetID
	err := m.transaction(ctx, func(tx *Manager) error {
		resolvedTargetID, err := mutate(tx)
		if err != nil {
			return err
		}
		if strings.TrimSpace(resolvedTargetID) != "" {
			canonicalTargetID = resolvedTargetID
		}
		return tx.Audit(ctx, actor, action, targetKind, canonicalTargetID, resourceGroupID, "success", "", metadata)
	})
	if err == nil {
		return nil
	}
	result := "error"
	if errors.Is(err, ErrForbidden) {
		result = "denied"
	}
	auditErr := m.Audit(ctx, actor, action, targetKind, canonicalTargetID, resourceGroupID, result, errorClass(err), metadata)
	return errors.Join(err, auditErr)
}

func (m *Manager) GetUser(ctx context.Context, id string) (User, error) {
	row, err := m.store.GetUser(ctx, id)
	if err != nil {
		return User{}, err
	}
	roles, err := m.store.UserRoleIDs(ctx, id)
	if err != nil {
		return User{}, err
	}
	return userFromRow(row, roles), nil
}

func (m *Manager) ListUsers(ctx context.Context) ([]User, error) {
	rows, err := m.store.ListUsers(ctx)
	if err != nil {
		return nil, err
	}
	users := make([]User, 0, len(rows))
	for _, row := range rows {
		roles, err := m.store.UserRoleIDs(ctx, row.ID)
		if err != nil {
			return nil, err
		}
		users = append(users, userFromRow(row, roles))
	}
	return users, nil
}

func userFromRow(row storage.UserRow, roles []string) User {
	return User{ID: row.ID, Username: row.Username, DisplayName: row.DisplayName, Disabled: row.Disabled, AuthRevision: row.AuthRevision, RoleIDs: roles, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}
}

func (m *Manager) SetUserRoles(ctx context.Context, userID string, roleIDs []string) (User, error) {
	current, err := m.store.UserRoleIDs(ctx, userID)
	if err != nil {
		return User{}, err
	}
	next := uniqueStrings(roleIDs)
	for _, roleID := range next {
		if _, _, err := m.store.GetRole(ctx, roleID); err != nil {
			return User{}, err
		}
		if !contains(current, roleID) {
			if err := m.store.BindRole(ctx, storage.RoleBindingRow{ID: newID("rb"), UserID: userID, RoleID: roleID, CreatedAt: m.now()}); err != nil {
				return User{}, err
			}
		}
	}
	for _, roleID := range current {
		if !contains(next, roleID) {
			if err := m.store.UnbindRole(ctx, userID, roleID); err != nil {
				return User{}, err
			}
		}
	}
	row, err := m.store.GetUser(ctx, userID)
	if err != nil {
		return User{}, err
	}
	row.AuthRevision++
	if err := m.store.SaveUser(ctx, row); err != nil {
		return User{}, err
	}
	return m.GetUser(ctx, userID)
}

func (m *Manager) DisableUser(ctx context.Context, userID string, disabled bool) (User, error) {
	row, err := m.store.GetUser(ctx, userID)
	if err != nil {
		return User{}, err
	}
	row.Disabled = disabled
	row.AuthRevision++
	if err := m.store.SaveUser(ctx, row); err != nil {
		return User{}, err
	}
	if disabled {
		if err := m.store.RevokeUserSessions(ctx, userID, m.now()); err != nil {
			return User{}, err
		}
	}
	return m.GetUser(ctx, userID)
}

func (m *Manager) Login(ctx context.Context, username, password string) (LoginResult, error) {
	user, err := m.store.GetUserByUsername(ctx, username)
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return LoginResult{}, err
	}
	invalidUser := errors.Is(err, gorm.ErrRecordNotFound) || user.Disabled
	passwordHash := dummyPasswordHash
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		passwordHash = []byte(user.PasswordHash)
	}
	passwordErr := bcrypt.CompareHashAndPassword(passwordHash, []byte(password))
	if invalidUser || passwordErr != nil {
		auditErr := m.Audit(ctx, Actor{ID: "anonymous"}, "auth.login", "user", strings.ToLower(strings.TrimSpace(username)), "", "denied", "invalid_credentials", nil)
		return LoginResult{}, errors.Join(ErrInvalidCredentials, auditErr)
	}
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		return LoginResult{}, err
	}
	token := base64.RawURLEncoding.EncodeToString(tokenBytes)
	now := m.now()
	session := storage.SessionRow{ID: newID("ses"), TokenHash: tokenDigest(token), UserID: user.ID, CreatedAt: now, LastSeen: now, ExpiresAt: now.Add(m.sessionTTL)}
	var actor Actor
	err = m.transaction(ctx, func(tx *Manager) error {
		if err := tx.store.CreateSession(ctx, session); err != nil {
			return err
		}
		permissions, err := tx.store.UserPermissions(ctx, user.ID)
		if err != nil {
			return err
		}
		groups, err := tx.store.VisibleResourceGroupIDs(ctx, user.ID)
		if err != nil {
			return err
		}
		actor = newActor(user.ID, user.Username, session.ID, false, permissions, groups)
		return tx.Audit(ctx, actor, "auth.login", "user", user.ID, "", "success", "", nil)
	})
	if err != nil {
		return LoginResult{}, err
	}
	return LoginResult{Token: token, ExpiresAt: session.ExpiresAt, Actor: actor}, nil
}

func (m *Manager) AuthenticateSession(ctx context.Context, token string) (Actor, error) {
	if strings.TrimSpace(token) == "" {
		return Actor{}, ErrUnauthorized
	}
	session, err := m.store.GetSessionByTokenHash(ctx, tokenDigest(token))
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return Actor{}, err
	}
	if errors.Is(err, gorm.ErrRecordNotFound) || session.RevokedAt != nil || !m.now().Before(session.ExpiresAt) {
		return Actor{}, ErrUnauthorized
	}
	user, err := m.store.GetUser(ctx, session.UserID)
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return Actor{}, err
	}
	if errors.Is(err, gorm.ErrRecordNotFound) || user.Disabled {
		return Actor{}, ErrUnauthorized
	}
	permissions, err := m.store.UserPermissions(ctx, user.ID)
	if err != nil {
		return Actor{}, err
	}
	groups, err := m.store.VisibleResourceGroupIDs(ctx, user.ID)
	if err != nil {
		return Actor{}, err
	}
	actor := newActor(user.ID, user.Username, session.ID, false, permissions, groups)
	if err := m.store.TouchSession(ctx, session.ID, m.now()); err != nil {
		return Actor{}, err
	}
	return actor, nil
}

func BootstrapActor() Actor {
	return newActor("bootstrap-administrator", "bootstrap-administrator", "", true, []string{PermissionAll}, nil)
}

func newActor(id, username, sessionID string, bootstrap bool, permissions, groups []string) Actor {
	actor := Actor{ID: id, Username: username, SessionID: sessionID, Bootstrap: bootstrap, Permissions: uniqueStrings(permissions), VisibleResourceGroups: uniqueStrings(groups), permissionSet: map[string]struct{}{}, groupSet: map[string]struct{}{}}
	for _, permission := range actor.Permissions {
		actor.permissionSet[permission] = struct{}{}
	}
	for _, group := range actor.VisibleResourceGroups {
		actor.groupSet[group] = struct{}{}
	}
	return actor
}

func (m *Manager) Logout(ctx context.Context, actor Actor) error {
	if actor.SessionID == "" {
		return nil
	}
	return m.AuditedMutation(ctx, actor, "auth.logout", "session", actor.SessionID, "", nil, func(tx *Manager) (string, error) {
		return actor.SessionID, tx.store.RevokeSession(ctx, actor.SessionID, m.now())
	})
}

func (a Actor) Has(permission string) bool {
	if a.Bootstrap {
		return true
	}
	if a.permissionSet == nil {
		a = newActor(a.ID, a.Username, a.SessionID, a.Bootstrap, a.Permissions, a.VisibleResourceGroups)
	}
	_, all := a.permissionSet[PermissionAll]
	_, exact := a.permissionSet[permission]
	return all || exact
}

func (a Actor) CanAccessGroup(groupID string) bool {
	if a.Has(PermissionAll) {
		return true
	}
	if a.groupSet == nil {
		a = newActor(a.ID, a.Username, a.SessionID, a.Bootstrap, a.Permissions, a.VisibleResourceGroups)
	}
	_, ok := a.groupSet[groupID]
	return ok
}

func (m *Manager) Authorize(ctx context.Context, actor Actor, permission, targetKind, targetID, resourceGroupID string) error {
	allowed := actor.Has(permission) && (resourceGroupID == "" || actor.CanAccessGroup(resourceGroupID))
	result := "success"
	errorName := ""
	if !allowed {
		result = "denied"
		errorName = "forbidden"
	}
	if err := m.Audit(ctx, actor, "authorization.check", targetKind, targetID, resourceGroupID, result, errorName, map[string]any{"permission": permission}); err != nil {
		return err
	}
	if !allowed {
		return ErrForbidden
	}
	return nil
}

func (m *Manager) AuthorizeResource(ctx context.Context, actor Actor, permission, kind, id string) error {
	binding, err := m.resourceBinding(ctx, kind, id)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return m.Authorize(ctx, actor, permission, kind, id, DefaultResourceGroup)
	}
	if err != nil {
		return err
	}
	return m.Authorize(ctx, actor, permission, kind, id, binding.ResourceGroupID)
}

func (m *Manager) CanAccessResource(ctx context.Context, actor Actor, kind, id string) (bool, error) {
	if actor.Has(PermissionAll) {
		return true, nil
	}
	binding, err := m.resourceBinding(ctx, kind, id)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return actor.CanAccessGroup(DefaultResourceGroup), nil
	}
	if err != nil {
		return false, err
	}
	return actor.CanAccessGroup(binding.ResourceGroupID), nil
}

func (m *Manager) resourceBinding(ctx context.Context, kind, id string) (storage.ResourceBindingRow, error) {
	binding, err := m.store.GetResourceBinding(ctx, kind, id)
	if err == nil || !errors.Is(err, gorm.ErrRecordNotFound) {
		return binding, err
	}
	switch kind {
	case "http_rule", "l4_rule", "relay_listener":
		agentID, _, found := strings.Cut(id, ":")
		if found && strings.TrimSpace(agentID) != "" {
			return m.store.GetResourceBinding(ctx, "agent", strings.TrimSpace(agentID))
		}
	}
	return storage.ResourceBindingRow{}, gorm.ErrRecordNotFound
}

func (m *Manager) CreateRole(ctx context.Context, name, description string, permissionIDs []string) (Role, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Role{}, ErrInvalidInput
	}
	if err := m.validatePermissions(ctx, permissionIDs); err != nil {
		return Role{}, err
	}
	now := m.now()
	row := storage.RoleRow{ID: newID("role"), Name: name, Description: strings.TrimSpace(description), Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := m.store.CreateRole(ctx, row, uniqueStrings(permissionIDs)); err != nil {
		return Role{}, err
	}
	return roleFrom(row, permissionIDs), nil
}

func (m *Manager) SetRolePermissions(ctx context.Context, roleID string, permissionIDs []string) (Role, error) {
	role, _, err := m.store.GetRole(ctx, roleID)
	if err != nil {
		return Role{}, err
	}
	if role.Builtin {
		return Role{}, fmt.Errorf("%w: built-in role permissions are immutable", ErrInvalidInput)
	}
	if err := m.validatePermissions(ctx, permissionIDs); err != nil {
		return Role{}, err
	}
	if err := m.store.ReplaceRolePermissions(ctx, roleID, uniqueStrings(permissionIDs)); err != nil {
		return Role{}, err
	}
	role, ids, err := m.store.GetRole(ctx, roleID)
	return roleFrom(role, ids), err
}

func (m *Manager) ListRoles(ctx context.Context) ([]Role, error) {
	rows, err := m.store.ListRoles(ctx)
	if err != nil {
		return nil, err
	}
	roles := make([]Role, 0, len(rows))
	for _, row := range rows {
		_, permissions, err := m.store.GetRole(ctx, row.ID)
		if err != nil {
			return nil, err
		}
		roles = append(roles, roleFrom(row, permissions))
	}
	return roles, nil
}

func (m *Manager) GetRole(ctx context.Context, id string) (Role, error) {
	row, permissions, err := m.store.GetRole(ctx, id)
	if err != nil {
		return Role{}, err
	}
	return roleFrom(row, permissions), nil
}

func (m *Manager) ListPermissions(ctx context.Context) ([]storage.PermissionRow, error) {
	return m.store.ListPermissions(ctx)
}

func roleFrom(row storage.RoleRow, permissions []string) Role {
	return Role{ID: row.ID, Name: row.Name, Description: row.Description, Builtin: row.Builtin, Revision: row.Revision, Permissions: uniqueStrings(permissions)}
}

func (m *Manager) validatePermissions(ctx context.Context, ids []string) error {
	rows, err := m.store.ListPermissions(ctx)
	if err != nil {
		return err
	}
	known := map[string]struct{}{}
	for _, row := range rows {
		known[row.ID] = struct{}{}
	}
	for _, id := range uniqueStrings(ids) {
		if _, ok := known[id]; !ok {
			return fmt.Errorf("%w: unknown permission %q", ErrInvalidInput, id)
		}
	}
	return nil
}

func (m *Manager) CreateResourceGroup(ctx context.Context, name, description string) (ResourceGroup, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return ResourceGroup{}, ErrInvalidInput
	}
	now := m.now()
	row := storage.ResourceGroupRow{ID: newID("rg"), Name: name, Description: strings.TrimSpace(description), CreatedAt: now, UpdatedAt: now}
	if err := m.store.CreateResourceGroup(ctx, row); err != nil {
		return ResourceGroup{}, err
	}
	return groupFrom(row), nil
}

func (m *Manager) ListResourceGroups(ctx context.Context, actor Actor) ([]ResourceGroup, error) {
	rows, err := m.store.ListResourceGroups(ctx)
	if err != nil {
		return nil, err
	}
	groups := make([]ResourceGroup, 0, len(rows))
	for _, row := range rows {
		if actor.CanAccessGroup(row.ID) {
			groups = append(groups, groupFrom(row))
		}
	}
	return groups, nil
}

func groupFrom(row storage.ResourceGroupRow) ResourceGroup {
	return ResourceGroup{ID: row.ID, Name: row.Name, Description: row.Description, Builtin: row.Builtin}
}

func (m *Manager) GrantResourceGroup(ctx context.Context, actor Actor, subjectKind, subjectID, groupID string) error {
	if !actor.Has(PermissionSystemAdmin) {
		return ErrForbidden
	}
	if subjectKind != "user" && subjectKind != "role" {
		return ErrInvalidInput
	}
	if _, err := m.store.GetResourceGroup(ctx, groupID); err != nil {
		return err
	}
	switch subjectKind {
	case "user":
		if _, err := m.store.GetUser(ctx, subjectID); err != nil {
			return err
		}
	case "role":
		if _, _, err := m.store.GetRole(ctx, subjectID); err != nil {
			return err
		}
	}
	return m.store.GrantResourceGroup(ctx, storage.ResourceGroupGrantRow{ID: newID("grant"), SubjectKind: subjectKind, SubjectID: subjectID, ResourceGroupID: groupID, CreatedAt: m.now()})
}

func (m *Manager) ListResourceGroupGrants(ctx context.Context, actor Actor) ([]storage.ResourceGroupGrantRow, error) {
	if !actor.Has(PermissionSystemAdmin) {
		return nil, ErrForbidden
	}
	return m.store.ListResourceGroupGrants(ctx)
}

func (m *Manager) RevokeResourceGroupGrant(ctx context.Context, actor Actor, subjectKind, subjectID, groupID string) error {
	if !actor.Has(PermissionSystemAdmin) {
		return ErrForbidden
	}
	if subjectKind != "user" && subjectKind != "role" || strings.TrimSpace(subjectID) == "" || strings.TrimSpace(groupID) == "" {
		return ErrInvalidInput
	}
	if subjectKind == "role" && groupID == DefaultResourceGroup && (subjectID == RoleOperator || subjectID == RoleReadonly) {
		return fmt.Errorf("%w: built-in default resource-group grants are immutable", ErrForbidden)
	}
	return m.store.RevokeResourceGroupGrant(ctx, subjectKind, subjectID, groupID)
}

func (m *Manager) BindResource(ctx context.Context, actor Actor, kind, id, groupID string) error {
	if !actor.Has(PermissionSystemAdmin) {
		return ErrForbidden
	}
	if strings.TrimSpace(kind) == "" || strings.TrimSpace(id) == "" {
		return ErrInvalidInput
	}
	if _, err := m.store.GetResourceGroup(ctx, groupID); err != nil {
		return err
	}
	exists, err := m.store.ResourceExists(ctx, kind, id)
	if err != nil {
		return err
	}
	if !exists {
		return gorm.ErrRecordNotFound
	}
	err = m.store.BindResource(ctx, storage.ResourceBindingRow{ID: newID("res"), ResourceKind: kind, ResourceID: id, ResourceGroupID: groupID, UpdatedAt: m.now()})
	if errors.Is(err, storage.ErrCertificateTargetsCrossGroup) || errors.Is(err, storage.ErrCertificateGroupMismatch) {
		return fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	return err
}

func (m *Manager) UpsertQuotaPolicy(ctx context.Context, actor Actor, row storage.QuotaPolicyRow) (storage.QuotaPolicyRow, error) {
	var policy storage.QuotaPolicyRow
	err := m.transaction(ctx, func(tx *Manager) error {
		currentActor, err := tx.reloadActor(ctx, actor)
		if err != nil {
			return err
		}
		policy, err = tx.upsertQuotaPolicy(ctx, currentActor, row)
		return err
	})
	return policy, err
}

func (m *Manager) upsertQuotaPolicy(ctx context.Context, actor Actor, row storage.QuotaPolicyRow) (storage.QuotaPolicyRow, error) {
	if !actor.Has(PermissionSystemAdmin) && !actor.Has(PermissionQuotaManage) {
		return storage.QuotaPolicyRow{}, ErrForbidden
	}
	if row.SubjectKind != "user" && row.SubjectKind != "role" && row.SubjectKind != "resource_group" || strings.TrimSpace(row.SubjectID) == "" || row.Limit < 0 || !validQuotaMetric(row.Metric) {
		return storage.QuotaPolicyRow{}, ErrInvalidInput
	}
	if row.ID == "" {
		row.ID = newID("quota")
	} else {
		current, err := m.store.GetQuotaPolicy(ctx, row.ID)
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return storage.QuotaPolicyRow{}, err
		}
		if err == nil && !canManageQuotaPolicy(actor, current) {
			return storage.QuotaPolicyRow{}, ErrForbidden
		}
	}
	if row.ExceedAction == "" {
		row.ExceedAction = "reject"
	}
	if row.ExceedAction != "reject" && row.ExceedAction != "limit" && row.ExceedAction != "disable" {
		return storage.QuotaPolicyRow{}, ErrInvalidInput
	}
	if (row.Metric == QuotaTraffic || row.Metric == QuotaBandwidth) && row.SubjectKind != "resource_group" {
		return storage.QuotaPolicyRow{}, fmt.Errorf("%w: %s quotas are supported only for resource groups", ErrInvalidInput, row.Metric)
	}
	if isCountQuotaMetric(row.Metric) {
		row.ResetAt = nil
	}
	switch row.SubjectKind {
	case "user":
		if _, err := m.store.GetUser(ctx, row.SubjectID); err != nil {
			return storage.QuotaPolicyRow{}, err
		}
	case "role":
		if _, _, err := m.store.GetRole(ctx, row.SubjectID); err != nil {
			return storage.QuotaPolicyRow{}, err
		}
	case "resource_group":
		if _, err := m.store.GetResourceGroup(ctx, row.SubjectID); err != nil {
			return storage.QuotaPolicyRow{}, err
		}
		if row.ResourceGroupID != "" && row.ResourceGroupID != row.SubjectID {
			return storage.QuotaPolicyRow{}, ErrInvalidInput
		}
		row.ResourceGroupID = row.SubjectID
	}
	if row.ResourceGroupID != "" {
		if _, err := m.store.GetResourceGroup(ctx, row.ResourceGroupID); err != nil {
			return storage.QuotaPolicyRow{}, err
		}
	}
	if !canManageQuotaPolicy(actor, row) {
		return storage.QuotaPolicyRow{}, ErrForbidden
	}
	now := m.now()
	if row.CreatedAt.IsZero() {
		row.CreatedAt = now
	}
	row.UpdatedAt = now
	if err := m.store.UpsertQuotaPolicy(ctx, row); err != nil {
		return storage.QuotaPolicyRow{}, err
	}
	return row, nil
}

func canManageQuotaPolicy(actor Actor, row storage.QuotaPolicyRow) bool {
	if actor.Has(PermissionSystemAdmin) {
		return true
	}
	return actor.Has(PermissionQuotaManage) && strings.TrimSpace(row.ResourceGroupID) != "" && actor.CanAccessGroup(row.ResourceGroupID)
}

func (m *Manager) reloadActor(ctx context.Context, actor Actor) (Actor, error) {
	if actor.Bootstrap {
		return actor, nil
	}
	permissions, err := m.store.UserPermissions(ctx, actor.ID)
	if err != nil {
		return Actor{}, err
	}
	groups, err := m.store.VisibleResourceGroupIDs(ctx, actor.ID)
	if err != nil {
		return Actor{}, err
	}
	return newActor(actor.ID, actor.Username, actor.SessionID, actor.Bootstrap, permissions, groups), nil
}

func isCountQuotaMetric(metric string) bool {
	return metric == QuotaRules || metric == QuotaApplications || metric == QuotaPublicPorts
}

func validQuotaMetric(metric string) bool {
	switch metric {
	case QuotaRules, QuotaApplications, QuotaPublicPorts, QuotaBandwidth, QuotaTraffic:
		return true
	default:
		return false
	}
}

func (m *Manager) ConsumeQuota(ctx context.Context, actor Actor, groupID, metric string, delta int64) (storage.QuotaDecision, error) {
	if !actor.CanAccessGroup(groupID) || !validQuotaMetric(metric) {
		return storage.QuotaDecision{}, ErrForbidden
	}
	ctx = storage.WithQuotaActor(ctx, storage.QuotaActor{UserID: actor.ID, SessionID: actor.SessionID, Bootstrap: actor.Bootstrap})
	var decision storage.QuotaDecision
	err := m.transaction(ctx, func(tx *Manager) error {
		var err error
		decision, err = tx.store.ConsumeQuota(ctx, actor.ID, groupID, metric, delta, m.now())
		if err != nil {
			return err
		}
		return tx.Audit(ctx, actor, "quota.consume", "resource_group", groupID, groupID, "success", "", map[string]any{"metric": metric, "delta": delta, "current": decision.Current, "limit": decision.Limit, "recovery_condition": decision.RecoveryCondition})
	})
	if err != nil {
		auditErr := m.Audit(ctx, actor, "quota.consume", "resource_group", groupID, groupID, "denied", errorClass(err), map[string]any{"metric": metric, "delta": delta, "current": decision.Current, "limit": decision.Limit, "recovery_condition": decision.RecoveryCondition})
		err = errors.Join(err, auditErr)
	}
	return decision, err
}

func (m *Manager) ListQuotaPolicies(ctx context.Context) ([]storage.QuotaPolicyRow, error) {
	return m.store.ListQuotaPolicies(ctx)
}

func (m *Manager) ListQuotaStatus(ctx context.Context, actor Actor) ([]QuotaStatus, error) {
	var result []QuotaStatus
	err := m.transaction(ctx, func(tx *Manager) error {
		currentActor, err := tx.reloadActor(ctx, actor)
		if err != nil {
			return err
		}
		if !currentActor.Has(PermissionSystemAdmin) && !currentActor.Has(PermissionQuotaManage) && !currentActor.Has(PermissionResourceRead) {
			return ErrForbidden
		}
		result, err = tx.listQuotaStatus(ctx, currentActor)
		return err
	})
	return result, err
}

func (m *Manager) listQuotaStatus(ctx context.Context, actor Actor) ([]QuotaStatus, error) {
	policies, err := m.store.ListQuotaPolicies(ctx)
	if err != nil {
		return nil, err
	}
	usages, err := m.store.ListQuotaUsage(ctx)
	if err != nil {
		return nil, err
	}
	policyUsages, err := m.store.ListQuotaPolicyUsage(ctx)
	if err != nil {
		return nil, err
	}
	roleIDs := []string{}
	if !actor.Has(PermissionAll) {
		roleIDs, err = m.store.UserRoleIDs(ctx, actor.ID)
		if err != nil {
			return nil, err
		}
	}
	usageByScope := make(map[string]storage.QuotaUsageRow, len(usages))
	for _, usage := range usages {
		usageByScope[usage.SubjectKind+"\x00"+usage.SubjectID+"\x00"+usage.ResourceGroupID+"\x00"+usage.Metric] = usage
	}
	usageByPolicy := make(map[string]storage.QuotaPolicyUsageRow, len(policyUsages))
	for _, usage := range policyUsages {
		usageByPolicy[usage.PolicyID+"\x00"+usage.ResourceGroupID] = usage
	}
	result := make([]QuotaStatus, 0, len(policies))
	for _, policy := range policies {
		visibleSubject := actor.Has(PermissionSystemAdmin) || policy.ResourceGroupID != "" && actor.Has(PermissionQuotaManage) && actor.CanAccessGroup(policy.ResourceGroupID) || policy.SubjectKind == "user" && policy.SubjectID == actor.ID || policy.SubjectKind == "role" && contains(roleIDs, policy.SubjectID) || policy.SubjectKind == "resource_group" && actor.CanAccessGroup(policy.SubjectID)
		if !visibleSubject || policy.ResourceGroupID != "" && !actor.CanAccessGroup(policy.ResourceGroupID) {
			continue
		}
		current := usageByScope[policy.SubjectKind+"\x00"+policy.SubjectID+"\x00"+policy.ResourceGroupID+"\x00"+policy.Metric].Current
		if !isCountQuotaMetric(policy.Metric) {
			current = usageByPolicy[policy.ID+"\x00"+policy.ResourceGroupID].Current
			if policy.ResetAt != nil && !m.now().Before(*policy.ResetAt) {
				current = 0
			}
		}
		result = append(result, QuotaStatus{PolicyID: policy.ID, SubjectKind: policy.SubjectKind, SubjectID: policy.SubjectID, ResourceGroupID: policy.ResourceGroupID, Metric: policy.Metric, Current: current, Limit: policy.Limit, ExceedAction: policy.ExceedAction, RecoveryCondition: policy.RecoveryCondition, ResetAt: policy.ResetAt})
	}
	return result, nil
}

func (m *Manager) Audit(ctx context.Context, actor Actor, action, targetKind, targetID, resourceGroupID, result, errorName string, metadata map[string]any) error {
	if m == nil || m.store == nil {
		return fmt.Errorf("audit store is unavailable")
	}
	metadata = redactMetadata(metadata)
	encoded, err := json.Marshal(metadata)
	if err != nil {
		encoded = []byte("{}")
	}
	err = m.store.AppendAuditEvent(ctx, storage.AuditEventRow{
		ID: newID("audit"), ActorID: actor.ID, SessionID: actor.SessionID, Action: action,
		TargetKind: targetKind, TargetID: targetID, ResourceGroupID: resourceGroupID,
		CorrelationID: correlationID(ctx), Result: result, ErrorClass: errorName,
		MetadataJSON: string(encoded), CreatedAt: m.now(),
	})
	if err != nil {
		return fmt.Errorf("%w: %v", ErrAuditUnavailable, err)
	}
	return nil
}

func (m *Manager) ListAuditEvents(ctx context.Context, limit int) ([]AuditEvent, error) {
	rows, err := m.store.ListAuditEvents(ctx, limit)
	if err != nil {
		return nil, err
	}
	events := make([]AuditEvent, 0, len(rows))
	for _, row := range rows {
		metadata := map[string]any{}
		_ = json.Unmarshal([]byte(row.MetadataJSON), &metadata)
		events = append(events, AuditEvent{ID: row.ID, ActorID: row.ActorID, SessionID: row.SessionID, Action: row.Action, TargetKind: row.TargetKind, TargetID: row.TargetID, ResourceGroupID: row.ResourceGroupID, CorrelationID: row.CorrelationID, Result: row.Result, ErrorClass: row.ErrorClass, Metadata: redactMetadata(metadata), CreatedAt: row.CreatedAt})
	}
	return events, nil
}

type correlationKey struct{}

func WithCorrelationID(ctx context.Context, value string) context.Context {
	return context.WithValue(ctx, correlationKey{}, strings.TrimSpace(value))
}

func correlationID(ctx context.Context) string {
	value, _ := ctx.Value(correlationKey{}).(string)
	return value
}

func redactMetadata(metadata map[string]any) map[string]any {
	out := map[string]any{}
	for key, value := range metadata {
		lower := strings.ToLower(key)
		if strings.Contains(lower, "token") || strings.Contains(lower, "secret") || strings.Contains(lower, "password") || strings.Contains(lower, "credential") || strings.Contains(lower, "authorization") {
			out[key] = "[REDACTED]"
			continue
		}
		switch typed := value.(type) {
		case map[string]any:
			out[key] = redactMetadata(typed)
		default:
			out[key] = typed
		}
	}
	return out
}

func tokenDigest(token string) string {
	digest := sha256.Sum256([]byte(token))
	return hex.EncodeToString(digest[:])
}

func newID(prefix string) string {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		panic(err)
	}
	return prefix + "_" + hex.EncodeToString(value)
}

func uniqueStrings(values []string) []string {
	seen := map[string]struct{}{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func errorClass(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, storage.ErrQuotaExceeded) {
		return "quota_exceeded"
	}
	if errors.Is(err, ErrForbidden) {
		return "forbidden"
	}
	return "operation_failed"
}
