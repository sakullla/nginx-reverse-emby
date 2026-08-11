package http

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/authz"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/marketplace"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/plugins"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/secrets"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/service"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
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
			Purpose         string `json:"purpose"`
			RefKind         string `json:"ref_kind"`
			RefName         string `json:"ref_name"`
			CredentialRef   string `json:"credential_ref,omitempty"`
			SignerKeyID     string `json:"signer_key_id"`
			SignerSecretRef string `json:"signer_secret_ref"`
			RefreshInterval string `json:"refresh_interval,omitempty"`
		}
		if err := decodeStrictPluginJSON(r, &input); err != nil {
			if auditErr := d.auditMarketplaceFailure(r, "add", "unknown", "invalid_json"); auditErr != nil {
				writePluginError(w, fmt.Errorf("marketplace failure audit persistence: %w", auditErr))
				return
			}
			writeJSON(w, http.StatusBadRequest, errorPayload(err.Error()))
			return
		}
		var interval time.Duration
		var err error
		if input.RefreshInterval != "" {
			interval, err = time.ParseDuration(input.RefreshInterval)
			if err != nil || interval < 0 {
				if auditErr := d.auditMarketplaceFailure(r, "add", input.ID, "invalid_interval"); auditErr != nil {
					writePluginError(w, auditErr)
					return
				}
				writeJSON(w, http.StatusBadRequest, errorPayload("invalid refresh_interval"))
				return
			}
		}
		if err := d.authorizeMarketplaceCredential(r, input.CredentialRef); err != nil {
			if auditErr := d.auditMarketplaceFailure(r, "add", input.ID, "credential_authorization"); auditErr != nil {
				writePluginError(w, fmt.Errorf("marketplace failure audit persistence: %w", auditErr))
				return
			}
			writePluginError(w, err)
			return
		}
		signer, err := d.resolveMarketplaceSigner(r, input.SignerKeyID, input.SignerSecretRef)
		if err != nil {
			if auditErr := d.auditMarketplaceFailure(r, "add", input.ID, "signer_authorization"); auditErr != nil {
				writePluginError(w, fmt.Errorf("marketplace failure audit persistence: %w", auditErr))
				return
			}
			writePluginError(w, err)
			return
		}
		source, err := d.MarketplaceService.AddGitRepositorySource(r.Context(), input.ID, input.Name, input.URL, input.Purpose, input.RefKind, input.RefName, input.CredentialRef, interval, signer)
		if err != nil {
			writePluginError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"source": source})
	default:
		writeJSON(w, http.StatusMethodNotAllowed, errorPayload("method not allowed"))
	}
}

func (d Dependencies) resolveMarketplaceSigner(r *http.Request, keyID, secretID string) (marketplace.SourceSigner, error) {
	if keyID == "" || secretID == "" || keyID != strings.TrimSpace(keyID) || secretID != strings.TrimSpace(secretID) {
		return marketplace.SourceSigner{}, fmt.Errorf("%w: marketplace signer_key_id and signer_secret_ref are required", service.ErrInvalidArgument)
	}
	actor, ok := actorFromRequest(r)
	if !ok || d.SecretVault == nil || d.AccessManager == nil {
		return marketplace.SourceSigner{}, fmt.Errorf("%w: marketplace signer authorization is unavailable", authz.ErrForbidden)
	}
	metadata, err := d.SecretVault.Get(r.Context(), secretID)
	if err != nil || metadata.Purpose != marketplace.SignerSecretPurpose {
		return marketplace.SourceSigner{}, fmt.Errorf("%w: marketplace signer key is unavailable or has an invalid purpose", authz.ErrForbidden)
	}
	if err := d.AccessManager.Authorize(r.Context(), actor, authz.PermissionSecretUse, "secret", secretID, metadata.ResourceGroupID); err != nil {
		return marketplace.SourceSigner{}, err
	}
	correlationID := strings.TrimSpace(r.Header.Get("X-Request-ID"))
	plaintext, err := d.SecretVault.Resolve(r.Context(), secrets.OperationContext{ActorID: actor.ID, SessionID: actor.SessionID, CorrelationID: correlationID, ResourceGroupID: metadata.ResourceGroupID}, secretID)
	if err != nil {
		return marketplace.SourceSigner{}, fmt.Errorf("%w: marketplace signer key could not be resolved", authz.ErrForbidden)
	}
	publicKey := string(plaintext)
	clear(plaintext)
	if publicKey == "" || publicKey != strings.TrimSpace(publicKey) {
		return marketplace.SourceSigner{}, fmt.Errorf("%w: marketplace signer key is unavailable or invalid", authz.ErrForbidden)
	}
	return marketplace.SourceSigner{KeyID: keyID, SecretRef: secretID, PublicKey: publicKey}, nil
}

func (d Dependencies) handleMarketplaceSource(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		source, err := d.MarketplaceService.Source(r.Context(), r.PathValue("id"))
		if err != nil {
			writePluginError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"source": source})
	case http.MethodPatch:
		d.handleMarketplaceSourcePatch(w, r)
	case http.MethodDelete:
		if err := d.MarketplaceService.DeleteSource(r.Context(), r.PathValue("id")); err != nil {
			writePluginError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	default:
		writeJSON(w, http.StatusMethodNotAllowed, errorPayload("method not allowed"))
	}
}

func (d Dependencies) handleMarketplaceSourcePatch(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Name            *string `json:"name"`
		URL             *string `json:"url"`
		Purpose         *string `json:"purpose"`
		RefKind         *string `json:"ref_kind"`
		RefName         *string `json:"ref_name"`
		CredentialRef   *string `json:"credential_ref"`
		SignerKeyID     *string `json:"signer_key_id"`
		SignerSecretRef *string `json:"signer_secret_ref"`
		RefreshInterval *string `json:"refresh_interval"`
		ConfigRevision  *uint64 `json:"config_revision"`
	}
	if err := decodeStrictPluginJSON(r, &input); err != nil {
		writeJSON(w, http.StatusBadRequest, errorPayload(err.Error()))
		return
	}
	current, err := d.MarketplaceService.Source(r.Context(), r.PathValue("id"))
	if err != nil {
		writePluginError(w, err)
		return
	}
	if current.Kind == marketplace.SourceKindOfficial {
		writePluginError(w, fmt.Errorf("%w: official source is immutable", service.ErrInvalidArgument))
		return
	}
	if input.ConfigRevision == nil || *input.ConfigRevision == 0 {
		writeJSON(w, http.StatusBadRequest, errorPayload("config_revision is required"))
		return
	}
	next := current
	if input.Name != nil {
		next.Name = *input.Name
	}
	if input.URL != nil {
		next.URL = *input.URL
	}
	if input.Purpose != nil {
		next.Purpose = *input.Purpose
	}
	if input.RefKind != nil {
		next.RefKind = *input.RefKind
	}
	if input.RefName != nil {
		next.RefName = *input.RefName
	}
	if input.CredentialRef != nil {
		next.CredentialRef = *input.CredentialRef
	}
	if input.RefreshInterval != nil {
		next.RefreshInterval, err = time.ParseDuration(*input.RefreshInterval)
		if err != nil || next.RefreshInterval < 0 {
			writeJSON(w, http.StatusBadRequest, errorPayload("invalid refresh_interval"))
			return
		}
	}
	clearSigner := input.SignerSecretRef != nil && *input.SignerSecretRef == "" && (input.SignerKeyID == nil || *input.SignerKeyID == "") || input.SignerKeyID != nil && *input.SignerKeyID == "" && input.SignerSecretRef == nil
	keepSigner := input.SignerKeyID != nil && input.SignerSecretRef == nil && *input.SignerKeyID == current.SignerKeyID
	if clearSigner {
		next.SignerKeyID, next.SignerSecretRef, next.SignerPublicKey, next.SignerFingerprint = "", "", "", ""
	} else if keepSigner {
		// The public key ID is safe to echo. The write-only secret reference is
		// omitted by clients and remains bound when the ID is unchanged.
	} else if input.SignerKeyID != nil && input.SignerSecretRef != nil {
		signer, signerErr := d.resolveMarketplaceSigner(r, *input.SignerKeyID, *input.SignerSecretRef)
		if signerErr != nil {
			writePluginError(w, signerErr)
			return
		}
		next.SignerKeyID, next.SignerSecretRef, next.SignerPublicKey = signer.KeyID, signer.SecretRef, signer.PublicKey
		next.SignerFingerprint, err = marketplace.SourceSignerFingerprint(signer.PublicKey)
		if err != nil {
			writePluginError(w, err)
			return
		}
	} else if input.SignerKeyID != nil || input.SignerSecretRef != nil {
		writeJSON(w, http.StatusBadRequest, errorPayload("a changed signer_key_id requires signer_secret_ref"))
		return
	}
	if err := d.authorizeMarketplaceCredential(r, next.CredentialRef); err != nil {
		writePluginError(w, err)
		return
	}
	expectedRevision := *input.ConfigRevision
	next.ConfigRevision = expectedRevision + 1
	next.CredentialConfigured = next.CredentialRef != ""
	updated, err := d.MarketplaceService.UpdateGitRepositorySource(r.Context(), next, expectedRevision)
	if err != nil {
		writePluginError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"source": updated})
}

func (d Dependencies) handleMarketplaceRefresh(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, errorPayload("method not allowed"))
		return
	}
	source, err := d.MarketplaceService.Source(r.Context(), r.PathValue("id"))
	if err != nil {
		if auditErr := d.auditMarketplaceFailure(r, "refresh", r.PathValue("id"), "source_lookup"); auditErr != nil {
			writePluginError(w, fmt.Errorf("marketplace failure audit persistence: %w", auditErr))
			return
		}
		writePluginError(w, err)
		return
	}
	if err := d.authorizeMarketplaceCredential(r, source.CredentialRef); err != nil {
		if auditErr := d.auditMarketplaceFailure(r, "refresh", source.ID, "credential_authorization"); auditErr != nil {
			writePluginError(w, fmt.Errorf("marketplace failure audit persistence: %w", auditErr))
			return
		}
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
		return fmt.Errorf("%w: marketplace credential authorization is unavailable", authz.ErrForbidden)
	}
	metadata, err := d.SecretVault.Get(r.Context(), secretID)
	if err != nil {
		return fmt.Errorf("%w: marketplace credential is unavailable", authz.ErrForbidden)
	}
	if metadata.Purpose != marketplace.CredentialPurpose {
		return fmt.Errorf("%w: marketplace credential must use purpose git.marketplace", authz.ErrForbidden)
	}
	if err := d.AccessManager.Authorize(r.Context(), actor, authz.PermissionSecretUse, "secret", secretID, metadata.ResourceGroupID); err != nil {
		return err
	}
	authorization := marketplace.CredentialAuthorization{SecretID: secretID, ResourceGroupID: metadata.ResourceGroupID, Actor: marketplace.OperationActor{ActorID: actor.ID, SessionID: actor.SessionID, CorrelationID: strings.TrimSpace(r.Header.Get("X-Request-ID"))}}
	*r = *r.WithContext(marketplace.WithCredentialAuthorization(r.Context(), authorization))
	return nil
}

func (d Dependencies) auditMarketplaceFailure(r *http.Request, action, sourceID, errorClass string) error {
	if d.MarketplaceService == nil {
		return errors.New("marketplace audit service is unavailable")
	}
	if strings.TrimSpace(sourceID) == "" {
		sourceID = "unknown"
	}
	return d.MarketplaceService.AuditSourceFailure(r.Context(), action, sourceID, errorClass)
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
	writeJSON(w, http.StatusOK, map[string]any{"source": catalog.Source, "snapshot": catalog.Snapshot, "entries": catalog.Snapshot.Entries, "direct_plugin": catalog.Snapshot.DirectPlugin})
}

type pluginPackageSelection struct {
	SourceID             string   `json:"source_id"`
	PluginID             string   `json:"plugin_id,omitempty"`
	Version              string   `json:"version"`
	Digest               string   `json:"digest"`
	ConfirmedPermissions []string `json:"confirmed_permissions"`
	RiskAccepted         bool     `json:"risk_accepted"`
}

func (d Dependencies) handlePlugins(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, errorPayload("method not allowed"))
		return
	}
	installed, err := d.PluginService.List(r.Context())
	if err != nil {
		writePluginError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"plugins": installed})
}

func (d Dependencies) handlePluginPackageDetail(w http.ResponseWriter, r *http.Request) {
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
	detail, err := d.PluginService.PackageDetail(r.Context(), candidate, input.PluginID)
	if err != nil {
		writePluginError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"package": detail})
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
	installed, err := d.PluginService.InstallMutation(r.Context(), service.PluginInstallRequest{Package: candidate, ActorID: pluginActorID(r), ConfirmedPermissions: input.ConfirmedPermissions, RiskAccepted: input.RiskAccepted})
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
	detail, err := d.PluginService.Detail(r.Context(), r.PathValue("id"))
	if err != nil {
		writePluginError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, detail)
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
		result, err = d.PluginService.EnableMutation(r.Context(), pluginID, actorID)
	case "disable":
		result, err = d.PluginService.DisableMutation(r.Context(), pluginID, actorID)
	case "rollback":
		var input struct {
			ConfirmedPermissions []string `json:"confirmed_permissions"`
		}
		if err = decodeStrictPluginJSON(r, &input); err == nil {
			result, err = d.PluginService.RollbackMutation(r.Context(), service.PluginRollbackRequest{PluginID: pluginID, ActorID: actorID, ConfirmedPermissions: input.ConfirmedPermissions})
		}
	case "configure":
		var input struct {
			InstanceID      string                                  `json:"instance_id"`
			ResourceGroupID string                                  `json:"resource_group_id"`
			Targets         any                                     `json:"targets"`
			PolicyChains    *[]string                               `json:"policy_chains"`
			Bindings        *[]storage.PluginInstanceBindingRequest `json:"bindings"`
			Config          json.RawMessage                         `json:"config"`
		}
		if err = decodeStrictPluginJSON(r, &input); err == nil {
			if input.PolicyChains == nil {
				err = fmt.Errorf("%w: policy_chains is required", service.ErrInvalidArgument)
				break
			}
			result, err = d.PluginService.ConfigureMutation(r.Context(), service.PluginConfigureRequest{PluginID: pluginID, InstanceID: input.InstanceID, ResourceGroupID: input.ResourceGroupID, Targets: input.Targets, PolicyChains: input.PolicyChains, Bindings: input.Bindings, Config: input.Config, ActorID: actorID})
		}
	case "upgrade":
		var input pluginPackageSelection
		if err = decodeStrictPluginJSON(r, &input); err == nil {
			input.PluginID = pluginID
			var candidate service.PluginPackageCandidate
			candidate, _, err = d.resolveHTTPPluginPackage(r, input)
			if err == nil {
				result, err = d.PluginService.UpgradeMutation(r.Context(), service.PluginUpgradeRequest{PluginID: pluginID, Package: candidate, ActorID: actorID, ConfirmedPermissions: input.ConfirmedPermissions, RiskAccepted: input.RiskAccepted})
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
		if errors.Is(err, service.ErrInvalidArgument) {
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
	data, err := io.ReadAll(io.LimitReader(r.Body, (1<<20)+1))
	if err != nil {
		return fmt.Errorf("%w: read JSON body: %v", service.ErrInvalidArgument, err)
	}
	if len(data) > 1<<20 {
		return fmt.Errorf("%w: JSON body exceeds 1 MiB", service.ErrInvalidArgument)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("%w: invalid JSON body: %v", service.ErrInvalidArgument, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("%w: multiple JSON values are forbidden", service.ErrInvalidArgument)
		}
		return fmt.Errorf("%w: invalid JSON body: %v", service.ErrInvalidArgument, err)
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
	case errors.Is(err, service.ErrPluginPermissionConfirmation), errors.Is(err, service.ErrPluginRiskConfirmation), errors.Is(err, service.ErrPluginResourceAuthorization), errors.Is(err, service.ErrMutationPrincipalRequired):
		status = http.StatusForbidden
	case errors.Is(err, service.ErrPluginUninstallBlocked), errors.Is(err, storage.ErrQuotaExceeded):
		status = http.StatusConflict
	case errors.Is(err, service.ErrMarketplaceSourceExists), errors.Is(err, storage.ErrPluginAlreadyInstalled), errors.Is(err, storage.ErrPluginConflict):
		status = http.StatusConflict
	case errors.Is(err, marketplace.ErrRefreshLeaseHeld):
		status = http.StatusConflict
	case errors.Is(err, marketplace.ErrSourceGenerationChanged):
		status = http.StatusConflict
	case errors.Is(err, authz.ErrForbidden), errors.Is(err, service.ErrPluginResourceAuthorization):
		status = http.StatusForbidden
	case errors.Is(err, marketplace.ErrInvalidSource), errors.Is(err, service.ErrInvalidArgument), errors.Is(err, authz.ErrInvalidInput):
		status = http.StatusBadRequest
	case errors.Is(err, storage.ErrPluginInstanceScope):
		status = http.StatusUnprocessableEntity
	default:
		var validation *plugins.ValidationError
		if errors.As(err, &validation) {
			status = http.StatusUnprocessableEntity
		}
	}
	message := err.Error()
	if status == http.StatusInternalServerError {
		message = "internal marketplace or plugin service error"
	}
	writeJSON(w, status, errorPayload(message))
}
