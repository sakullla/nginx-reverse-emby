package http

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/authz"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/secrets"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
	"gorm.io/gorm"
)

func (d Dependencies) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, errorPayloadCode("method_not_allowed", "method not allowed"))
		return
	}
	if d.AccessManager == nil {
		writeJSON(w, http.StatusServiceUnavailable, errorPayloadCode("access_control_unavailable", "multi-user authentication is unavailable"))
		return
	}
	var input struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decodeAccessJSON(r, &input); err != nil {
		writeAccessError(w, err)
		return
	}
	result, err := d.AccessManager.Login(r.Context(), input.Username, input.Password)
	if err != nil {
		writeAccessError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "session": result})
}

func (d Dependencies) handleMe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, errorPayloadCode("method_not_allowed", "method not allowed"))
		return
	}
	actor, _ := actorFromRequest(r)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "actor": actor})
}

func (d Dependencies) handleLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, errorPayloadCode("method_not_allowed", "method not allowed"))
		return
	}
	actor, _ := actorFromRequest(r)
	if d.AccessManager != nil {
		if err := d.AccessManager.Logout(r.Context(), actor); err != nil {
			writeAccessError(w, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (d Dependencies) handleAccessUsers(w http.ResponseWriter, r *http.Request) {
	actor, ok := d.requireAccessPermission(w, r, authz.PermissionAccessManage)
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		users, err := d.AccessManager.ListUsers(r.Context())
		if err != nil {
			writeAccessError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "users": users})
	case http.MethodPost:
		var input struct {
			Username    string   `json:"username"`
			DisplayName string   `json:"display_name"`
			Password    string   `json:"password"`
			RoleIDs     []string `json:"role_ids"`
		}
		if err := decodeAccessJSON(r, &input); err != nil {
			writeAccessError(w, err)
			return
		}
		var user authz.User
		err := d.AccessManager.AuditedMutation(r.Context(), actor, "access.user.create", "user", input.Username, "", nil, func(tx *authz.Manager) (string, error) {
			if err := ensureDelegableRoles(r.Context(), tx, actor, input.RoleIDs); err != nil {
				return input.Username, err
			}
			var err error
			user, err = tx.CreateUser(r.Context(), input.Username, input.DisplayName, input.Password, input.RoleIDs)
			return user.ID, err
		})
		if err != nil {
			writeAccessError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"ok": true, "user": user})
	default:
		writeJSON(w, http.StatusMethodNotAllowed, errorPayloadCode("method_not_allowed", "method not allowed"))
	}
}

func (d Dependencies) handleAccessUser(w http.ResponseWriter, r *http.Request) {
	actor, ok := d.requireAccessPermission(w, r, authz.PermissionAccessManage)
	if !ok {
		return
	}
	id := r.PathValue("id")
	switch r.Method {
	case http.MethodGet:
		user, err := d.AccessManager.GetUser(r.Context(), id)
		if err != nil {
			writeAccessError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "user": user})
	case http.MethodPut:
		var input struct {
			RoleIDs  *[]string `json:"role_ids"`
			Disabled *bool     `json:"disabled"`
		}
		if err := decodeAccessJSON(r, &input); err != nil {
			writeAccessError(w, err)
			return
		}
		var user authz.User
		err := d.AccessManager.AuditedMutation(r.Context(), actor, "access.user.update", "user", id, "", nil, func(tx *authz.Manager) (string, error) {
			current, err := tx.GetUser(r.Context(), id)
			if err != nil {
				return id, err
			}
			if (input.RoleIDs != nil || input.Disabled != nil) && !actor.Has(authz.PermissionSystemAdmin) {
				if err := ensureDelegableRoles(r.Context(), tx, actor, current.RoleIDs); err != nil {
					return id, err
				}
			}
			if input.RoleIDs != nil {
				if err := ensureDelegableRoles(r.Context(), tx, actor, *input.RoleIDs); err != nil {
					return id, err
				}
			}
			err = nil
			if input.RoleIDs != nil {
				user, err = tx.SetUserRoles(r.Context(), id, *input.RoleIDs)
			}
			if err == nil && input.Disabled != nil {
				user, err = tx.DisableUser(r.Context(), id, *input.Disabled)
			}
			if input.RoleIDs == nil && input.Disabled == nil {
				err = authz.ErrInvalidInput
			}
			return id, err
		})
		if err != nil {
			writeAccessError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "user": user})
	default:
		writeJSON(w, http.StatusMethodNotAllowed, errorPayloadCode("method_not_allowed", "method not allowed"))
	}
}

func (d Dependencies) handleAccessPermissions(w http.ResponseWriter, r *http.Request) {
	if _, ok := d.requireAccessPermission(w, r, authz.PermissionAccessManage); !ok {
		return
	}
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, errorPayloadCode("method_not_allowed", "method not allowed"))
		return
	}
	permissions, err := d.AccessManager.ListPermissions(r.Context())
	if err != nil {
		writeAccessError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "permissions": permissions})
}

func (d Dependencies) handleAccessRoles(w http.ResponseWriter, r *http.Request) {
	actor, ok := d.requireAccessPermission(w, r, authz.PermissionAccessManage)
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		roles, err := d.AccessManager.ListRoles(r.Context())
		if err != nil {
			writeAccessError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "roles": roles})
	case http.MethodPost:
		var input struct {
			Name        string   `json:"name"`
			Description string   `json:"description"`
			Permissions []string `json:"permissions"`
		}
		if err := decodeAccessJSON(r, &input); err != nil {
			writeAccessError(w, err)
			return
		}
		var role authz.Role
		err := d.AccessManager.AuditedMutation(r.Context(), actor, "access.role.create", "role", input.Name, "", nil, func(tx *authz.Manager) (string, error) {
			if err := ensureDelegablePermissions(actor, input.Permissions); err != nil {
				return input.Name, err
			}
			var err error
			role, err = tx.CreateRole(r.Context(), input.Name, input.Description, input.Permissions)
			return role.ID, err
		})
		if err != nil {
			writeAccessError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"ok": true, "role": role})
	default:
		writeJSON(w, http.StatusMethodNotAllowed, errorPayloadCode("method_not_allowed", "method not allowed"))
	}
}

func (d Dependencies) handleAccessRole(w http.ResponseWriter, r *http.Request) {
	actor, ok := d.requireAccessPermission(w, r, authz.PermissionAccessManage)
	if !ok {
		return
	}
	id := r.PathValue("id")
	switch r.Method {
	case http.MethodGet:
		role, err := d.AccessManager.GetRole(r.Context(), id)
		if err != nil {
			writeAccessError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "role": role})
	case http.MethodPut:
		var input struct {
			Permissions []string `json:"permissions"`
		}
		if err := decodeAccessJSON(r, &input); err != nil {
			writeAccessError(w, err)
			return
		}
		var role authz.Role
		err := d.AccessManager.AuditedMutation(r.Context(), actor, "access.role.permissions.update", "role", id, "", map[string]any{"permission_count": len(input.Permissions)}, func(tx *authz.Manager) (string, error) {
			current, err := tx.GetRole(r.Context(), id)
			if err != nil {
				return id, err
			}
			if id == authz.RoleAdministrator && !actor.Has(authz.PermissionSystemAdmin) {
				return id, authz.ErrForbidden
			}
			if !actor.Has(authz.PermissionSystemAdmin) {
				if err := ensureDelegablePermissions(actor, current.Permissions); err != nil {
					return id, err
				}
			}
			if err := ensureDelegablePermissions(actor, input.Permissions); err != nil {
				return id, err
			}
			err = nil
			role, err = tx.SetRolePermissions(r.Context(), id, input.Permissions)
			return id, err
		})
		if err != nil {
			writeAccessError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "role": role})
	default:
		writeJSON(w, http.StatusMethodNotAllowed, errorPayloadCode("method_not_allowed", "method not allowed"))
	}
}

func (d Dependencies) handleResourceGroups(w http.ResponseWriter, r *http.Request) {
	actor, ok := actorFromRequest(r)
	if !ok || d.AccessManager == nil {
		writeAccessError(w, authz.ErrUnauthorized)
		return
	}
	switch r.Method {
	case http.MethodGet:
		if err := d.AccessManager.Authorize(r.Context(), actor, authz.PermissionResourceRead, "resource_group", "list", ""); err != nil {
			writeAccessError(w, err)
			return
		}
		groups, err := d.AccessManager.ListResourceGroups(r.Context(), actor)
		if err != nil {
			writeAccessError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "resource_groups": groups})
	case http.MethodPost:
		if err := d.AccessManager.Authorize(r.Context(), actor, authz.PermissionAccessManage, "resource_group", "create", ""); err != nil {
			writeAccessError(w, err)
			return
		}
		var input struct {
			Name        string `json:"name"`
			Description string `json:"description"`
		}
		if err := decodeAccessJSON(r, &input); err != nil {
			writeAccessError(w, err)
			return
		}
		var group authz.ResourceGroup
		err := d.AccessManager.AuditedMutation(r.Context(), actor, "access.resource_group.create", "resource_group", input.Name, "", nil, func(tx *authz.Manager) (string, error) {
			var err error
			group, err = tx.CreateResourceGroup(r.Context(), input.Name, input.Description)
			return group.ID, err
		})
		if err != nil {
			writeAccessError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"ok": true, "resource_group": group})
	default:
		writeJSON(w, http.StatusMethodNotAllowed, errorPayloadCode("method_not_allowed", "method not allowed"))
	}
}

func (d Dependencies) handleResourceGroupGrants(w http.ResponseWriter, r *http.Request) {
	actor, ok := d.requireAccessPermission(w, r, authz.PermissionSystemAdmin)
	if !ok {
		return
	}
	if r.Method == http.MethodGet {
		grants, err := d.AccessManager.ListResourceGroupGrants(r.Context(), actor)
		if err != nil {
			writeAccessError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "resource_group_grants": grants})
		return
	}
	if r.Method != http.MethodPost && r.Method != http.MethodDelete {
		writeJSON(w, http.StatusMethodNotAllowed, errorPayloadCode("method_not_allowed", "method not allowed"))
		return
	}
	var input struct {
		SubjectKind     string `json:"subject_kind"`
		SubjectID       string `json:"subject_id"`
		ResourceGroupID string `json:"resource_group_id"`
	}
	if err := decodeAccessJSON(r, &input); err != nil {
		writeAccessError(w, err)
		return
	}
	action := "access.resource_group.grant"
	if r.Method == http.MethodDelete {
		action = "access.resource_group.revoke"
	}
	err := d.AccessManager.AuditedMutation(r.Context(), actor, action, input.SubjectKind, input.SubjectID, input.ResourceGroupID, nil, func(tx *authz.Manager) (string, error) {
		if r.Method == http.MethodDelete {
			return input.SubjectID, tx.RevokeResourceGroupGrant(r.Context(), actor, input.SubjectKind, input.SubjectID, input.ResourceGroupID)
		}
		return input.SubjectID, tx.GrantResourceGroup(r.Context(), actor, input.SubjectKind, input.SubjectID, input.ResourceGroupID)
	})
	if err != nil {
		writeAccessError(w, err)
		return
	}
	status := http.StatusCreated
	if r.Method == http.MethodDelete {
		status = http.StatusOK
	}
	writeJSON(w, status, map[string]any{"ok": true})
}

func (d Dependencies) handleResourceBindings(w http.ResponseWriter, r *http.Request) {
	actor, ok := d.requireAccessPermission(w, r, authz.PermissionSystemAdmin)
	if !ok {
		return
	}
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, errorPayloadCode("method_not_allowed", "method not allowed"))
		return
	}
	var input struct {
		ResourceKind    string `json:"resource_kind"`
		ResourceID      string `json:"resource_id"`
		ResourceGroupID string `json:"resource_group_id"`
	}
	if err := decodeAccessJSON(r, &input); err != nil {
		writeAccessError(w, err)
		return
	}
	err := d.AccessManager.AuditedMutation(r.Context(), actor, "access.resource.move", input.ResourceKind, input.ResourceID, input.ResourceGroupID, nil, func(tx *authz.Manager) (string, error) {
		return input.ResourceID, tx.BindResource(r.Context(), actor, input.ResourceKind, input.ResourceID, input.ResourceGroupID)
	})
	if err != nil {
		writeAccessError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"ok": true})
}

func (d Dependencies) handleQuotaPolicies(w http.ResponseWriter, r *http.Request) {
	actor, found := actorFromRequest(r)
	if !found || d.AccessManager == nil {
		writeAccessError(w, authz.ErrUnauthorized)
		return
	}
	switch r.Method {
	case http.MethodGet:
		if err := d.AccessManager.Authorize(r.Context(), actor, authz.PermissionResourceRead, "quota_policy", "list", ""); err != nil {
			writeAccessError(w, err)
			return
		}
		usage, err := d.AccessManager.ListQuotaStatus(r.Context(), actor)
		if err != nil {
			writeAccessError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "quota_policies": usage, "quota_usage": usage})
	case http.MethodPost, http.MethodPut:
		if err := d.AccessManager.Authorize(r.Context(), actor, authz.PermissionSystemAdmin, "quota_policy", "upsert", ""); err != nil {
			writeAccessError(w, err)
			return
		}
		var input storage.QuotaPolicyRow
		if err := decodeAccessJSON(r, &input); err != nil {
			writeAccessError(w, err)
			return
		}
		var policy storage.QuotaPolicyRow
		err := d.AccessManager.AuditedMutation(r.Context(), actor, "quota.policy.upsert", "quota_policy", input.ID, input.ResourceGroupID, map[string]any{"metric": input.Metric, "limit": input.Limit}, func(tx *authz.Manager) (string, error) {
			var err error
			policy, err = tx.UpsertQuotaPolicy(r.Context(), actor, input)
			return policy.ID, err
		})
		if err != nil {
			writeAccessError(w, err)
			return
		}
		status := http.StatusCreated
		if r.Method == http.MethodPut {
			status = http.StatusOK
		}
		writeJSON(w, status, map[string]any{"ok": true, "quota_policy": policy})
	default:
		writeJSON(w, http.StatusMethodNotAllowed, errorPayloadCode("method_not_allowed", "method not allowed"))
	}
}

func (d Dependencies) handleAuditEvents(w http.ResponseWriter, r *http.Request) {
	if _, ok := d.requireAccessPermission(w, r, authz.PermissionAuditRead); !ok {
		return
	}
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, errorPayloadCode("method_not_allowed", "method not allowed"))
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	events, err := d.AccessManager.ListAuditEvents(r.Context(), limit)
	if err != nil {
		writeAccessError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "audit_events": events})
}

func (d Dependencies) handleSecrets(w http.ResponseWriter, r *http.Request) {
	actor, ok := actorFromRequest(r)
	if !ok || d.AccessManager == nil {
		writeAccessError(w, authz.ErrUnauthorized)
		return
	}
	if d.SecretVault == nil {
		writeJSON(w, http.StatusServiceUnavailable, errorPayloadCode("vault_key_unavailable", "secret vault master key is unavailable"))
		return
	}
	switch r.Method {
	case http.MethodGet:
		if err := d.AccessManager.Authorize(r.Context(), actor, authz.PermissionSecretMetadataRead, "secret", "list", ""); err != nil {
			writeAccessError(w, err)
			return
		}
		groupIDs := actor.VisibleResourceGroups
		if actor.Has(authz.PermissionAll) {
			groupIDs = nil
		}
		items, err := d.SecretVault.List(r.Context(), groupIDs)
		if err != nil {
			writeAccessError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "secrets": items})
	case http.MethodPost:
		if err := d.AccessManager.Authorize(r.Context(), actor, authz.PermissionSecretManage, "secret", "create", ""); err != nil {
			writeAccessError(w, err)
			return
		}
		var input struct {
			Name            string `json:"name"`
			Purpose         string `json:"purpose"`
			ResourceGroupID string `json:"resource_group_id"`
			Value           string `json:"value"`
			Generate        bool   `json:"generate"`
			GeneratedBytes  int    `json:"generated_bytes"`
		}
		if err := decodeAccessJSON(r, &input); err != nil {
			writeAccessError(w, err)
			return
		}
		if !actor.CanAccessGroup(input.ResourceGroupID) {
			writeAccessError(w, authz.ErrForbidden)
			return
		}
		op := secretOperation(r, actor, input.ResourceGroupID)
		if input.Generate {
			metadata, _, err := d.SecretVault.Generate(r.Context(), op, input.Name, input.Purpose, input.GeneratedBytes)
			if err != nil {
				writeAccessError(w, err)
				return
			}
			writeJSON(w, http.StatusCreated, map[string]any{"ok": true, "secret": metadata})
			return
		}
		metadata, err := d.SecretVault.Create(r.Context(), op, input.Name, input.Purpose, input.Value)
		if err != nil {
			writeAccessError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"ok": true, "secret": metadata})
	default:
		writeJSON(w, http.StatusMethodNotAllowed, errorPayloadCode("method_not_allowed", "method not allowed"))
	}
}

func (d Dependencies) handleSecret(w http.ResponseWriter, r *http.Request) {
	actor, ok := d.requireAccessPermission(w, r, authz.PermissionSecretMetadataRead)
	if !ok {
		return
	}
	if d.SecretVault == nil {
		writeJSON(w, http.StatusServiceUnavailable, errorPayloadCode("vault_key_unavailable", "secret vault master key is unavailable"))
		return
	}
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, errorPayloadCode("method_not_allowed", "method not allowed"))
		return
	}
	metadata, err := d.SecretVault.Get(r.Context(), r.PathValue("id"))
	if err == nil && !actor.CanAccessGroup(metadata.ResourceGroupID) {
		err = authz.ErrForbidden
	}
	if err != nil {
		writeAccessError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "secret": metadata})
}

func (d Dependencies) handleSecretRotate(w http.ResponseWriter, r *http.Request) {
	actor, ok := d.requireAccessPermission(w, r, authz.PermissionSecretManage)
	if !ok {
		return
	}
	if d.SecretVault == nil {
		writeJSON(w, http.StatusServiceUnavailable, errorPayloadCode("vault_key_unavailable", "secret vault master key is unavailable"))
		return
	}
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, errorPayloadCode("method_not_allowed", "method not allowed"))
		return
	}
	id := r.PathValue("id")
	metadata, err := d.SecretVault.Get(r.Context(), id)
	if err != nil {
		writeAccessError(w, err)
		return
	}
	if !actor.CanAccessGroup(metadata.ResourceGroupID) {
		writeAccessError(w, authz.ErrForbidden)
		return
	}
	var input struct {
		Value string `json:"value"`
	}
	if err := decodeAccessJSON(r, &input); err != nil {
		writeAccessError(w, err)
		return
	}
	metadata, err = d.SecretVault.Rotate(r.Context(), secretOperation(r, actor, metadata.ResourceGroupID), id, input.Value)
	if err != nil {
		writeAccessError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "secret": metadata})
}

func (d Dependencies) requireAccessPermission(w http.ResponseWriter, r *http.Request, permission string) (authz.Actor, bool) {
	actor, ok := actorFromRequest(r)
	if !ok || d.AccessManager == nil {
		writeAccessError(w, authz.ErrUnauthorized)
		return authz.Actor{}, false
	}
	if err := d.AccessManager.Authorize(r.Context(), actor, permission, "api", r.URL.Path, ""); err != nil {
		writeAccessError(w, err)
		return authz.Actor{}, false
	}
	return actor, true
}

func ensureDelegablePermissions(actor authz.Actor, permissions []string) error {
	for _, permission := range permissions {
		permission = strings.TrimSpace(permission)
		if permission == "" {
			return authz.ErrForbidden
		}
		if permission == authz.PermissionAll || permission == authz.PermissionSystemAdmin {
			if !actor.Has(authz.PermissionSystemAdmin) {
				return authz.ErrForbidden
			}
			continue
		}
		if !actor.Has(permission) {
			return authz.ErrForbidden
		}
	}
	return nil
}

func ensureDelegableRoles(ctx context.Context, manager *authz.Manager, actor authz.Actor, roleIDs []string) error {
	for _, roleID := range roleIDs {
		roleID = strings.TrimSpace(roleID)
		if roleID == authz.RoleAdministrator && !actor.Has(authz.PermissionSystemAdmin) {
			return authz.ErrForbidden
		}
		role, err := manager.GetRole(ctx, roleID)
		if err != nil {
			return err
		}
		if err := ensureDelegablePermissions(actor, role.Permissions); err != nil {
			return err
		}
	}
	return nil
}

func secretOperation(r *http.Request, actor authz.Actor, groupID string) secrets.OperationContext {
	return secrets.OperationContext{ActorID: actor.ID, SessionID: actor.SessionID, CorrelationID: strings.TrimSpace(r.Header.Get("X-Request-ID")), ResourceGroupID: groupID}
}

func decodeAccessJSON(r *http.Request, target any) error {
	decoder := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmtAccessInput(err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return authz.ErrInvalidInput
	}
	return nil
}

func fmtAccessInput(err error) error {
	return errors.Join(authz.ErrInvalidInput, err)
}

func writeAccessError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	code := "internal_error"
	message := "access-control operation failed"
	switch {
	case errors.Is(err, authz.ErrAuditUnavailable), errors.Is(err, secrets.ErrAuditUnavailable):
		status, code, message = http.StatusServiceUnavailable, "audit_unavailable", "security audit persistence is unavailable"
	case errors.Is(err, authz.ErrInvalidCredentials):
		status, code, message = http.StatusUnauthorized, "invalid_credentials", "invalid username or password"
	case errors.Is(err, authz.ErrUnauthorized):
		status, code, message = http.StatusUnauthorized, "authentication_required", "authentication required"
	case errors.Is(err, authz.ErrForbidden):
		status, code, message = http.StatusForbidden, "permission_denied", "permission denied"
	case errors.Is(err, authz.ErrInvalidInput), errors.Is(err, secrets.ErrInvalidSecret):
		status, code, message = http.StatusBadRequest, "invalid_input", "invalid request"
	case errors.Is(err, storage.ErrQuotaExceeded):
		status, code, message = http.StatusTooManyRequests, "quota_exceeded", "quota exceeded"
	case errors.Is(err, gorm.ErrRecordNotFound):
		status, code, message = http.StatusNotFound, "not_found", "resource not found"
	}
	if errors.Is(err, storage.ErrQuotaExceeded) {
		writeJSON(w, status, quotaErrorPayload(err))
		return
	}
	writeJSON(w, status, errorPayloadCode(code, message))
}
