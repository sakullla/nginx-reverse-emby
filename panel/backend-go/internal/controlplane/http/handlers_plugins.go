package http

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/authz"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/marketplace"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/service"
)

func (d Dependencies) handleMarketplaceSources(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		sources, err := d.MarketplaceService.ListSources(r.Context())
		if err != nil {
			writePluginError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"sources": sources})
	case http.MethodPost:
		var input struct {
			ID              string `json:"id"`
			Name            string `json:"name"`
			URL             string `json:"url"`
			Reference       string `json:"reference"`
			CredentialRef   string `json:"credential_ref,omitempty"`
			RefreshInterval string `json:"refresh_interval,omitempty"`
		}
		if err := decodeStrictPluginJSON(r, &input); err != nil {
			d.auditMarketplaceFailure(r, "add", "unknown", "invalid_json")
			writeJSON(w, http.StatusBadRequest, errorPayload(err.Error()))
			return
		}
		var interval time.Duration
		var err error
		if input.RefreshInterval != "" {
			interval, err = time.ParseDuration(input.RefreshInterval)
			if err != nil || interval < 0 {
				d.auditMarketplaceFailure(r, "add", input.ID, "invalid_interval")
				writeJSON(w, http.StatusBadRequest, errorPayload("invalid refresh_interval"))
				return
			}
		}
		if err := d.authorizeMarketplaceCredential(r, input.CredentialRef); err != nil {
			d.auditMarketplaceFailure(r, "add", input.ID, "credential_authorization")
			writePluginError(w, err)
			return
		}
		source, err := d.MarketplaceService.AddCustomSource(r.Context(), input.ID, input.Name, input.URL, input.Reference, input.CredentialRef, interval)
		if err != nil {
			writePluginError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"source": source})
	default:
		writeJSON(w, http.StatusMethodNotAllowed, errorPayload("method not allowed"))
	}
}

func (d Dependencies) handleMarketplaceSource(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		writeJSON(w, http.StatusMethodNotAllowed, errorPayload("method not allowed"))
		return
	}
	if err := d.MarketplaceService.DeleteSource(r.Context(), r.PathValue("id")); err != nil {
		writePluginError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (d Dependencies) handleMarketplaceRefresh(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, errorPayload("method not allowed"))
		return
	}
	source, err := d.MarketplaceService.Source(r.Context(), r.PathValue("id"))
	if err != nil {
		d.auditMarketplaceFailure(r, "refresh", r.PathValue("id"), "source_lookup")
		writePluginError(w, err)
		return
	}
	if err := d.authorizeMarketplaceCredential(r, source.CredentialRef); err != nil {
		d.auditMarketplaceFailure(r, "refresh", source.ID, "credential_authorization")
		writePluginError(w, err)
		return
	}
	snapshot, err := d.MarketplaceService.Refresh(r.Context(), r.PathValue("id"))
	if err != nil {
		writePluginError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"snapshot": snapshot})
}

func (d Dependencies) authorizeMarketplaceCredential(r *http.Request, secretID string) error {
	secretID = strings.TrimSpace(secretID)
	if secretID == "" {
		return nil
	}
	actor, ok := actorFromRequest(r)
	if !ok || d.SecretVault == nil || d.AccessManager == nil {
		return errors.New("marketplace credential authorization is unavailable")
	}
	metadata, err := d.SecretVault.Get(r.Context(), secretID)
	if err != nil {
		return err
	}
	if metadata.Purpose != marketplace.CredentialPurpose {
		return errors.New("marketplace credential must use purpose git.marketplace")
	}
	if err := d.AccessManager.Authorize(r.Context(), actor, authz.PermissionSecretUse, "secret", secretID, metadata.ResourceGroupID); err != nil {
		return err
	}
	authorization := marketplace.CredentialAuthorization{SecretID: secretID, ResourceGroupID: metadata.ResourceGroupID, Actor: marketplace.OperationActor{ActorID: actor.ID, SessionID: actor.SessionID, CorrelationID: strings.TrimSpace(r.Header.Get("X-Request-ID"))}}
	*r = *r.WithContext(marketplace.WithCredentialAuthorization(r.Context(), authorization))
	return nil
}

func (d Dependencies) auditMarketplaceFailure(r *http.Request, action, sourceID, errorClass string) {
	if d.MarketplaceService == nil {
		return
	}
	if strings.TrimSpace(sourceID) == "" {
		sourceID = "unknown"
	}
	_ = d.MarketplaceService.AuditSourceFailure(r.Context(), action, sourceID, errorClass)
}

func (d Dependencies) handleMarketplaceEntries(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, errorPayload("method not allowed"))
		return
	}
	catalog, err := d.MarketplaceService.CurrentCatalog(r.Context(), r.PathValue("id"))
	if err != nil {
		writePluginError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, catalog)
}

type pluginPackageSelection struct {
	SourceID             string   `json:"source_id"`
	PluginID             string   `json:"plugin_id,omitempty"`
	Version              string   `json:"version"`
	Digest               string   `json:"digest"`
	ConfirmedPermissions []string `json:"confirmed_permissions"`
	RiskAccepted         bool     `json:"risk_accepted"`
}

func (d Dependencies) handlePluginInstall(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, errorPayload("method not allowed"))
		return
	}
	var input pluginPackageSelection
	if err := decodeStrictPluginJSON(r, &input); err != nil {
		writeJSON(w, http.StatusBadRequest, errorPayload(err.Error()))
		return
	}
	candidate, _, err := d.resolveHTTPPluginPackage(r, input)
	if err != nil {
		writePluginError(w, err)
		return
	}
	installed, err := d.PluginService.Install(r.Context(), service.PluginInstallRequest{Package: candidate, ActorID: pluginActorID(r), ConfirmedPermissions: input.ConfirmedPermissions, RiskAccepted: input.RiskAccepted})
	if err != nil {
		writePluginError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"plugin": installed})
}

func (d Dependencies) handlePlugin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, errorPayload("method not allowed"))
		return
	}
	installed, err := d.PluginService.Status(r.Context(), r.PathValue("id"))
	if err != nil {
		writePluginError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"plugin": installed})
}

func (d Dependencies) handlePluginOperations(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, errorPayload("method not allowed"))
		return
	}
	operations, err := d.PluginService.Operations(r.Context(), r.PathValue("id"))
	if err != nil {
		writePluginError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"operations": operations})
}

func (d Dependencies) handlePluginAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, errorPayload("method not allowed"))
		return
	}
	pluginID, action, actorID := r.PathValue("id"), r.PathValue("action"), pluginActorID(r)
	var result any
	var err error
	status := http.StatusAccepted
	switch action {
	case "enable":
		result, err = d.PluginService.Enable(r.Context(), pluginID, actorID)
	case "disable":
		result, err = d.PluginService.Disable(r.Context(), pluginID, actorID)
	case "rollback":
		var input struct {
			ConfirmedPermissions []string `json:"confirmed_permissions"`
		}
		if err = decodeStrictPluginJSON(r, &input); err == nil {
			result, err = d.PluginService.Rollback(r.Context(), service.PluginRollbackRequest{PluginID: pluginID, ActorID: actorID, ConfirmedPermissions: input.ConfirmedPermissions})
		}
	case "configure":
		var input struct {
			InstanceID      string          `json:"instance_id"`
			ResourceGroupID string          `json:"resource_group_id"`
			Targets         any             `json:"targets"`
			Config          json.RawMessage `json:"config"`
		}
		if err = decodeStrictPluginJSON(r, &input); err == nil {
			result, err = d.PluginService.Configure(r.Context(), service.PluginConfigureRequest{PluginID: pluginID, InstanceID: input.InstanceID, ResourceGroupID: input.ResourceGroupID, Targets: input.Targets, Config: input.Config, ActorID: actorID})
		}
	case "upgrade":
		var input pluginPackageSelection
		if err = decodeStrictPluginJSON(r, &input); err == nil {
			input.PluginID = pluginID
			var candidate service.PluginPackageCandidate
			candidate, _, err = d.resolveHTTPPluginPackage(r, input)
			if err == nil {
				result, err = d.PluginService.Upgrade(r.Context(), service.PluginUpgradeRequest{PluginID: pluginID, Package: candidate, ActorID: actorID, ConfirmedPermissions: input.ConfirmedPermissions, RiskAccepted: input.RiskAccepted})
			}
		}
	case "uninstall":
		var input struct {
			Drained bool `json:"drained"`
		}
		if err = decodeStrictPluginJSON(r, &input); err == nil {
			err = d.PluginService.Uninstall(r.Context(), service.PluginUninstallRequest{PluginID: pluginID, ActorID: actorID, Drained: input.Drained})
			result = map[string]any{"uninstalled": err == nil}
			status = http.StatusOK
		}
	default:
		writeJSON(w, http.StatusNotFound, errorPayload("plugin action not found"))
		return
	}
	if err != nil {
		if strings.Contains(err.Error(), "json") || strings.Contains(err.Error(), "unknown field") {
			writeJSON(w, http.StatusBadRequest, errorPayload(err.Error()))
		} else {
			writePluginError(w, err)
		}
		return
	}
	writeJSON(w, status, map[string]any{"result": result})
}

func (d Dependencies) resolveHTTPPluginPackage(r *http.Request, input pluginPackageSelection) (service.PluginPackageCandidate, string, error) {
	source, err := d.MarketplaceService.Source(r.Context(), input.SourceID)
	if err != nil {
		return service.PluginPackageCandidate{}, "", err
	}
	candidate, err := d.MarketplaceService.ResolvePackage(r.Context(), input.SourceID, input.PluginID, input.Version, input.Digest)
	return candidate, source.Kind, err
}

func decodeStrictPluginJSON(r *http.Request, target any) error {
	decoder := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return errors.New("multiple JSON values are forbidden")
		}
		return err
	}
	return nil
}

func pluginActorID(r *http.Request) string {
	actor, ok := actorFromRequest(r)
	if !ok {
		return "system.admin"
	}
	return actor.ID
}

func writePluginError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	switch {
	case errors.Is(err, service.ErrPluginNotInstalled), errors.Is(err, service.ErrMarketplaceSourceNotFound), errors.Is(err, service.ErrMarketplaceEntryNotFound):
		status = http.StatusNotFound
	case errors.Is(err, service.ErrPluginPermissionConfirmation), errors.Is(err, service.ErrPluginRiskConfirmation):
		status = http.StatusForbidden
	case errors.Is(err, service.ErrPluginUninstallBlocked):
		status = http.StatusConflict
	case errors.Is(err, service.ErrMarketplaceSourceExists):
		status = http.StatusConflict
	case errors.Is(err, marketplace.ErrRefreshLeaseHeld):
		status = http.StatusConflict
	}
	message := err.Error()
	if status == http.StatusInternalServerError {
		message = "internal marketplace or plugin service error"
	}
	writeJSON(w, status, errorPayload(message))
}
