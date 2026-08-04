package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/control"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	modulepki "github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/pki"
)

type remoteAgentTaskHandler struct {
	diagnostics control.TaskHandler
	pki         *remotePKIHeartbeatHandler

	reconcileMu sync.RWMutex
	reconcile   func(context.Context) error
}

func newRemoteAgentTaskHandler(diagnostics control.TaskHandler, pki *remotePKIHeartbeatHandler) *remoteAgentTaskHandler {
	return &remoteAgentTaskHandler{diagnostics: diagnostics, pki: pki}
}

func (h *remoteAgentTaskHandler) setTunnelSecurityReconciler(reconcile func(context.Context) error) {
	if h == nil {
		return
	}
	h.reconcileMu.Lock()
	h.reconcile = reconcile
	h.reconcileMu.Unlock()
}

func (h *remoteAgentTaskHandler) HandleTask(ctx context.Context, task control.TaskMessage) (map[string]any, error) {
	if h == nil {
		return nil, errors.New("agent task handler is unavailable")
	}
	switch strings.TrimSpace(task.TaskType) {
	case control.TaskTypePKISecurityUpdate:
		return h.handlePKISecurityUpdate(ctx, task.RawPayload)
	case control.TaskTypePKIForceRotation:
		return h.handlePKIForceRotation(ctx, task.RawPayload)
	default:
		if h.diagnostics == nil {
			return nil, fmt.Errorf("unsupported task type %q", task.TaskType)
		}
		return h.diagnostics.HandleTask(ctx, task)
	}
}

func (h *remoteAgentTaskHandler) handlePKISecurityUpdate(ctx context.Context, payload map[string]any) (map[string]any, error) {
	if h.pki == nil {
		return nil, errors.New("remote PKI task handler is unavailable")
	}
	snapshot, err := taskPKISecuritySnapshot(payload)
	if err != nil {
		return nil, err
	}
	if err := h.pki.ApplyHeartbeat(ctx, control.PKIHeartbeatReply{
		Security: &snapshot,
		Status:   &model.PKIControlStatus{Status: "ready"},
	}); err != nil {
		return nil, fmt.Errorf("apply PKI security task: %w", err)
	}
	h.reconcileMu.RLock()
	reconcile := h.reconcile
	h.reconcileMu.RUnlock()
	if reconcile != nil {
		if err := reconcile(ctx); err != nil {
			return nil, fmt.Errorf("fence relay runtime after PKI security task: %w", err)
		}
	}
	return map[string]any{
		"pki_domain_id":     snapshot.PKIDomainID,
		"pki_epoch":         snapshot.PKIEpoch,
		"security_revision": snapshot.SecurityRevision,
	}, nil
}

func (h *remoteAgentTaskHandler) handlePKIForceRotation(ctx context.Context, payload map[string]any) (map[string]any, error) {
	if h.pki == nil {
		return nil, errors.New("remote PKI task handler is unavailable")
	}
	identityID, _ := payload["identity_id"].(string)
	identityID = strings.TrimSpace(identityID)
	if identityID == "" {
		return nil, errors.New("identity_id is required")
	}
	identityKind, _ := payload["identity_kind"].(string)
	listenerID, _ := payload["listener_id"].(string)
	pending, err := h.pki.forceRotation(ctx, identityID, identityKind, listenerID)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"identity_id": identityID,
		"request_id":  pending.Request.RequestID,
	}, nil
}

func (h *remotePKIHeartbeatHandler) forceRotation(ctx context.Context, identityID, identityKind, listenerID string) (modulepki.PendingEnrollment, error) {
	identityKind = strings.TrimSpace(identityKind)
	listenerID = strings.TrimSpace(listenerID)
	switch identityKind {
	case model.PKIIdentityKindAgent:
		return h.forceAgentRotation(ctx, identityID)
	case model.PKIIdentityKindListener:
		return h.forceListenerRotation(ctx, identityID, listenerID)
	case "":
		if _, err := h.store.LoadActiveCredential(remoteAgentPKIStorageIdentity); err == nil {
			return h.forceAgentRotation(ctx, identityID)
		}
		for _, listener := range h.relayListeners() {
			if strings.TrimSpace(listener.PKIIdentityID) == strings.TrimSpace(identityID) {
				return h.forceListenerRotation(ctx, identityID, fmt.Sprint(listener.ID))
			}
		}
		return modulepki.PendingEnrollment{}, fmt.Errorf("forced PKI rotation identity %q is not projected by this agent", identityID)
	default:
		return modulepki.PendingEnrollment{}, fmt.Errorf("unsupported PKI identity kind %q", identityKind)
	}
}

func taskPKISecuritySnapshot(payload map[string]any) (model.PKISecuritySnapshot, error) {
	raw, ok := payload["pki_security"]
	if !ok || raw == nil {
		return model.PKISecuritySnapshot{}, errors.New("pki_security is required")
	}
	if snapshot, ok := raw.(model.PKISecuritySnapshot); ok {
		return snapshot, nil
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return model.PKISecuritySnapshot{}, fmt.Errorf("encode pki_security task payload: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	var snapshot model.PKISecuritySnapshot
	if err := decoder.Decode(&snapshot); err != nil {
		return model.PKISecuritySnapshot{}, fmt.Errorf("decode pki_security task payload: %w", err)
	}
	return snapshot, nil
}

func (h *remotePKIHeartbeatHandler) forceAgentRotation(ctx context.Context, identityID string) (modulepki.PendingEnrollment, error) {
	if h == nil || h.store == nil {
		return modulepki.PendingEnrollment{}, errors.New("remote PKI store is unavailable")
	}
	if err := ctx.Err(); err != nil {
		return modulepki.PendingEnrollment{}, err
	}
	active, err := h.store.LoadActiveCredential(remoteAgentPKIStorageIdentity)
	if err != nil {
		return modulepki.PendingEnrollment{}, fmt.Errorf("load active PKI credential for forced rotation: %w", err)
	}
	credentialIdentity := strings.TrimSpace(active.Manifest.Credential.IdentityID)
	if credentialIdentity == "" || credentialIdentity != strings.TrimSpace(identityID) {
		return modulepki.PendingEnrollment{}, fmt.Errorf("forced PKI rotation identity %q does not own the active agent credential", identityID)
	}
	security, err := h.store.LoadSecuritySnapshot()
	if err != nil {
		return modulepki.PendingEnrollment{}, fmt.Errorf("load PKI security state for forced rotation: %w", err)
	}
	if err := validateRemoteAgentCredentialOwner(active, security, h.agentID); err != nil {
		return modulepki.PendingEnrollment{}, err
	}
	pending, err := h.store.PendingEnrollments()
	if err != nil {
		return modulepki.PendingEnrollment{}, fmt.Errorf("enumerate PKI enrollments for forced rotation: %w", err)
	}
	pending, err = h.reconcilePendingRejection(pending)
	if err != nil {
		return modulepki.PendingEnrollment{}, err
	}
	state := renewalStateForCredential(active, h.agentID)
	state.DueAt = h.currentTime()
	if _, err := h.store.SaveRenewalState(remoteAgentPKIStorageIdentity, state); err != nil {
		return modulepki.PendingEnrollment{}, fmt.Errorf("schedule forced PKI rotation: %w", err)
	}
	pending, err = h.ensureAgentRenewalPending(ctx, pending)
	if err != nil {
		return modulepki.PendingEnrollment{}, err
	}
	enrollment, err := findRemoteAgentEnrollment(pending)
	if err != nil {
		return modulepki.PendingEnrollment{}, err
	}
	if enrollment == nil {
		return modulepki.PendingEnrollment{}, errors.New("forced PKI rotation did not create a replayable enrollment")
	}
	return clonePendingEnrollment(*enrollment), nil
}

func (h *remotePKIHeartbeatHandler) forceListenerRotation(ctx context.Context, identityID, listenerID string) (modulepki.PendingEnrollment, error) {
	if h == nil || h.store == nil {
		return modulepki.PendingEnrollment{}, errors.New("remote PKI store is unavailable")
	}
	if err := ctx.Err(); err != nil {
		return modulepki.PendingEnrollment{}, err
	}
	listenerID = strings.TrimSpace(listenerID)
	var listener *model.RelayListener
	for _, candidate := range h.relayListeners() {
		if fmt.Sprint(candidate.ID) == listenerID && strings.TrimSpace(candidate.PKIIdentityID) == strings.TrimSpace(identityID) {
			copy := candidate
			listener = &copy
			break
		}
	}
	if listener == nil {
		return modulepki.PendingEnrollment{}, fmt.Errorf("forced PKI rotation identity %q does not own projected listener %q", identityID, listenerID)
	}
	storageIdentity := remoteListenerPKIStorageIdentity(listener.ID)
	active, err := h.store.LoadActiveCredential(storageIdentity)
	if err != nil {
		return modulepki.PendingEnrollment{}, fmt.Errorf("load active listener PKI credential for forced rotation: %w", err)
	}
	security, err := h.store.LoadSecuritySnapshot()
	if err != nil {
		return modulepki.PendingEnrollment{}, fmt.Errorf("load PKI security state for forced listener rotation: %w", err)
	}
	spec, err := remoteListenerEnrollmentSpec(*listener, security.Snapshot.PKIDomainID, h.agentID)
	if err != nil {
		return modulepki.PendingEnrollment{}, err
	}
	if err := validateRemoteListenerCredentialOwner(active, security, spec, identityID); err != nil {
		return modulepki.PendingEnrollment{}, err
	}
	pending, err := h.store.PendingEnrollments()
	if err != nil {
		return modulepki.PendingEnrollment{}, fmt.Errorf("enumerate PKI enrollments for forced listener rotation: %w", err)
	}
	state := renewalStateForCredential(active, h.agentID+"\x00"+listenerID)
	state.DueAt = h.currentTime()
	if _, err := h.store.SaveRenewalState(storageIdentity, state); err != nil {
		return modulepki.PendingEnrollment{}, fmt.Errorf("schedule forced listener PKI rotation: %w", err)
	}
	pending, err = h.ensureRelayListenerRenewalsPending(ctx, pending)
	if err != nil {
		return modulepki.PendingEnrollment{}, err
	}
	enrollment, err := findEnrollmentForStorageIdentity(pending, storageIdentity)
	if err != nil {
		return modulepki.PendingEnrollment{}, err
	}
	if enrollment == nil {
		return modulepki.PendingEnrollment{}, errors.New("forced listener PKI rotation did not create a replayable enrollment")
	}
	return clonePendingEnrollment(*enrollment), nil
}

func (a *App) reconcileTunnelSecurityAfterTask(ctx context.Context) error {
	if a == nil {
		return nil
	}
	relayModule := a.bindRelayTunnelCredentialProvider()
	if relayModule == nil {
		return nil
	}
	if err := relayModule.ReconcileTunnelSecurity(ctx); err != nil {
		// An invalidated local credential is an expected result of applying a
		// revocation. Re-fence explicitly so task success means the runtime is
		// closed even though no usable credential remains.
		if fenceErr := relayModule.FenceTunnelListeners(ctx, nil, err.Error()); fenceErr != nil {
			return errors.Join(err, fenceErr)
		}
	}
	return nil
}
