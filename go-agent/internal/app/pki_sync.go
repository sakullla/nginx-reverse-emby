package app

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/control"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	modulepki "github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/pki"
)

const remoteAgentPKIStorageIdentity = "agent"

const (
	remoteRenewalBackoffBase       = time.Minute
	remoteRenewalBackoffMaximum    = 6 * time.Hour
	remoteLifecycleJitterWindow    = 15 * time.Minute
	remoteRenewalFailureCountLimit = 32
)

type remotePKIStore interface {
	PendingEnrollments() ([]modulepki.PendingEnrollment, error)
	PrepareEnrollment(context.Context, modulepki.EnrollmentSpec) (modulepki.PendingEnrollment, error)
	LoadStagedRegistration(string) (modulepki.StagedRegistration, modulepki.PendingEnrollment, error)
	ActivateStagedRegistration(context.Context, string) (modulepki.CredentialMetadata, error)
	LoadActiveCredential(string) (modulepki.CredentialMetadata, error)
	LoadSecuritySnapshot() (modulepki.SecurityState, error)
	SecurityAcknowledgement(string) (model.PKISecurityAcknowledgement, error)
	ApplySecuritySnapshot(model.PKISecuritySnapshot) (modulepki.SecurityState, error)
	ActivateCredential(context.Context, modulepki.ActivateRequest) (modulepki.CredentialMetadata, error)
	RejectPendingEnrollment(string, string, string) error
	LoadRenewalState(string) (modulepki.RenewalState, error)
	SaveRenewalState(string, modulepki.RenewalState) (modulepki.RenewalState, error)
}

type remotePKIHeartbeatHandler struct {
	store   remotePKIStore
	agentID string

	mu       sync.Mutex
	inflight map[string]modulepki.PendingEnrollment
	now      func() time.Time
}

type pkiHeartbeatActivation struct {
	response   model.PKIControlCredential
	enrollment modulepki.PendingEnrollment
}

func newRemotePKIHeartbeatHandler(store remotePKIStore, agentID string) *remotePKIHeartbeatHandler {
	return &remotePKIHeartbeatHandler{
		store: store, agentID: strings.TrimSpace(agentID),
		inflight: make(map[string]modulepki.PendingEnrollment),
		now:      modulepki.RuntimeClock,
	}
}

func (h *remotePKIHeartbeatHandler) PrepareHeartbeat(ctx context.Context) (control.PKIHeartbeatState, error) {
	if h == nil || h.store == nil {
		return control.PKIHeartbeatState{}, errors.New("remote PKI store is unavailable")
	}
	if h.agentID == "" {
		return control.PKIHeartbeatState{}, errors.New("remote PKI agent identity is unavailable")
	}
	if err := ctx.Err(); err != nil {
		return control.PKIHeartbeatState{}, err
	}

	pending, err := h.store.PendingEnrollments()
	if err != nil {
		return control.PKIHeartbeatState{}, fmt.Errorf("enumerate pending PKI enrollments: %w", err)
	}
	pending, err = h.reconcilePendingRejection(pending)
	if err != nil {
		return control.PKIHeartbeatState{}, err
	}
	activatedStaged := false
	for _, enrollment := range pending {
		if enrollment.StorageIdentity != remoteAgentPKIStorageIdentity || enrollment.Request.Kind != model.PKIIdentityKindAgent {
			continue
		}
		staged, stagedPending, err := h.store.LoadStagedRegistration(enrollment.StorageIdentity)
		if errors.Is(err, modulepki.ErrStagedRegistrationNotFound) {
			continue
		}
		if err != nil {
			return control.PKIHeartbeatState{}, fmt.Errorf("load staged PKI registration: %w", err)
		}
		if strings.TrimSpace(staged.AgentID) != h.agentID {
			return control.PKIHeartbeatState{}, fmt.Errorf("%w: staged registration belongs to a different agent", modulepki.ErrPendingConflict)
		}
		if stagedPending.StorageIdentity != enrollment.StorageIdentity || stagedPending.Request.RequestID != enrollment.Request.RequestID {
			return control.PKIHeartbeatState{}, fmt.Errorf("%w: staged registration changed during recovery", modulepki.ErrPendingConflict)
		}
		active, err := h.store.ActivateStagedRegistration(ctx, enrollment.StorageIdentity)
		if err != nil {
			return control.PKIHeartbeatState{}, fmt.Errorf("activate staged PKI registration: %w", err)
		}
		if _, err := h.persistHealthyRenewalState(active); err != nil {
			return control.PKIHeartbeatState{}, fmt.Errorf("reset PKI renewal state after staged registration: %w", err)
		}
		activatedStaged = true
	}
	if activatedStaged {
		pending, err = h.store.PendingEnrollments()
		if err != nil {
			return control.PKIHeartbeatState{}, fmt.Errorf("reload pending PKI enrollments: %w", err)
		}
	}
	pending, err = h.ensureAgentRenewalPending(ctx, pending)
	if err != nil {
		return control.PKIHeartbeatState{}, err
	}

	state := control.PKIHeartbeatState{
		EnrollmentRequests: make([]model.PKIEnrollmentRequest, 0, len(pending)),
	}
	inflight := make(map[string]modulepki.PendingEnrollment, len(pending))
	for _, enrollment := range pending {
		requestID := strings.TrimSpace(enrollment.Request.RequestID)
		if requestID == "" {
			return control.PKIHeartbeatState{}, fmt.Errorf("%w: pending request ID is empty", modulepki.ErrCredentialInvalid)
		}
		if _, duplicate := inflight[requestID]; duplicate {
			return control.PKIHeartbeatState{}, fmt.Errorf("%w: duplicate pending request ID %q", modulepki.ErrCredentialInvalid, requestID)
		}
		inflight[requestID] = clonePendingEnrollment(enrollment)
		state.EnrollmentRequests = append(state.EnrollmentRequests, clonePKIEnrollmentRequest(enrollment.Request))
	}
	h.mu.Lock()
	h.inflight = inflight
	h.mu.Unlock()

	suppressAcknowledgement, err := h.reenrollmentRequired()
	if err != nil {
		return control.PKIHeartbeatState{}, err
	}
	if !suppressAcknowledgement {
		acknowledgement, err := h.store.SecurityAcknowledgement(remoteAgentPKIStorageIdentity)
		if err == nil {
			state.SecurityAcknowledgement = &acknowledgement
		} else if !errors.Is(err, modulepki.ErrActiveCredential) &&
			!errors.Is(err, modulepki.ErrCredentialInvalid) &&
			!errors.Is(err, modulepki.ErrSecurityInvalid) {
			return control.PKIHeartbeatState{}, fmt.Errorf("load durable PKI acknowledgement: %w", err)
		}
	}
	return state, nil
}

func (h *remotePKIHeartbeatHandler) ApplyHeartbeat(ctx context.Context, reply control.PKIHeartbeatReply) error {
	if h == nil || h.store == nil {
		return errors.New("remote PKI store is unavailable")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	degraded, err := validatePKIControlStatus(reply.Status)
	if err != nil {
		return err
	}

	h.mu.Lock()
	inflight := make(map[string]modulepki.PendingEnrollment, len(h.inflight))
	for requestID, enrollment := range h.inflight {
		inflight[requestID] = clonePendingEnrollment(enrollment)
	}
	h.mu.Unlock()

	activations := make([]pkiHeartbeatActivation, 0, len(reply.Credentials))
	rejections := make([]pkiHeartbeatActivation, 0, len(reply.Credentials))
	seen := make(map[string]struct{}, len(reply.Credentials))
	for _, response := range reply.Credentials {
		requestID := strings.TrimSpace(response.RequestID)
		if requestID == "" {
			return fmt.Errorf("%w: PKI credential response request ID is empty", modulepki.ErrCredentialInvalid)
		}
		if _, duplicate := seen[requestID]; duplicate {
			return fmt.Errorf("%w: duplicate PKI credential response %q", modulepki.ErrCredentialInvalid, requestID)
		}
		seen[requestID] = struct{}{}
		enrollment, ok := inflight[requestID]
		if !ok {
			return fmt.Errorf("%w: PKI credential response %q was not requested by this heartbeat", modulepki.ErrPendingConflict, requestID)
		}
		if strings.TrimSpace(response.Error) != "" {
			if strings.TrimSpace(response.Credential.CertificateID) != "" {
				return fmt.Errorf("%w: rejected PKI credential response also contains a certificate", modulepki.ErrCredentialInvalid)
			}
			rejections = append(rejections, pkiHeartbeatActivation{response: response, enrollment: enrollment})
			continue
		}
		if strings.TrimSpace(response.Credential.CertificateID) == "" {
			return fmt.Errorf("%w: successful PKI credential response is empty", modulepki.ErrCredentialInvalid)
		}
		activations = append(activations, pkiHeartbeatActivation{response: response, enrollment: enrollment})
	}

	if reply.Security != nil {
		if _, err := h.store.ApplySecuritySnapshot(*reply.Security); err != nil {
			return fmt.Errorf("apply PKI security snapshot: %w", err)
		}
	}
	if degraded != nil {
		if err := h.recordEnrollmentRejections(rejections); err != nil {
			return err
		}
		if err := h.ensureAgentRenewalAfterReply(ctx); err != nil {
			return err
		}
		// Degraded means the control plane deliberately stripped relay PKI
		// material from the ordinary snapshot. Treat it as observable status,
		// not a transport failure, so SyncClient can still apply that fail-closed
		// snapshot (including removal of stale relay listeners).
		return nil
	}
	if len(activations) != 0 && reply.Security == nil {
		return fmt.Errorf("%w: PKI credential response is missing its security snapshot", modulepki.ErrSecurityInvalid)
	}

	var activationErr error
	for _, candidate := range activations {
		pending := candidate.enrollment
		domainID := strings.TrimSpace(pending.DomainID)
		if domainID == "" {
			domainID = strings.TrimSpace(reply.Security.PKIDomainID)
		}
		agentID := strings.TrimSpace(pending.AgentID)
		if agentID == "" {
			agentID = h.agentID
		}
		active, err := h.store.ActivateCredential(ctx, modulepki.ActivateRequest{
			StorageIdentity: pending.StorageIdentity,
			RequestID:       pending.Request.RequestID,
			Credential:      candidate.response.Credential,
			Security:        *reply.Security,
			Expectation: modulepki.CredentialExpectation{
				DomainID: domainID, AgentID: agentID,
				Kind: pending.Request.Kind, ListenerID: pending.Request.ListenerID,
				Purpose: pending.Request.Purpose, DNSNames: append([]string(nil), pending.Request.DNSNames...),
				IPAddresses: append([]string(nil), pending.Request.IPAddresses...),
			},
		})
		if err != nil {
			activationErr = errors.Join(activationErr, fmt.Errorf("activate PKI credential %q: %w", pending.Request.RequestID, err))
			continue
		}
		if pending.StorageIdentity == remoteAgentPKIStorageIdentity && pending.Request.Kind == model.PKIIdentityKindAgent {
			if _, err := h.persistHealthyRenewalState(active); err != nil {
				activationErr = errors.Join(activationErr, fmt.Errorf("reset PKI renewal state after credential activation: %w", err))
				continue
			}
		}
		h.mu.Lock()
		delete(h.inflight, pending.Request.RequestID)
		h.mu.Unlock()
	}
	rejectionErr := h.recordEnrollmentRejections(rejections)
	if rejectionErr != nil {
		activationErr = errors.Join(activationErr, rejectionErr)
	} else if err := h.ensureAgentRenewalAfterReply(ctx); err != nil {
		activationErr = errors.Join(activationErr, err)
	}
	return activationErr
}

func (h *remotePKIHeartbeatHandler) recordEnrollmentRejections(rejections []pkiHeartbeatActivation) error {
	var result error
	now := h.currentTime()
	for _, rejected := range rejections {
		requestID := strings.TrimSpace(rejected.response.RequestID)
		code := strings.TrimSpace(rejected.response.Error)
		if rejected.enrollment.StorageIdentity == remoteAgentPKIStorageIdentity && rejected.enrollment.Request.Kind == model.PKIIdentityKindAgent {
			state, err := h.currentOrFallbackRenewalState(now)
			if err != nil {
				result = errors.Join(result, fmt.Errorf("load PKI renewal state for rejected enrollment %q: %w", requestID, err))
				continue
			}
			if state.FailureCount < remoteRenewalFailureCountLimit {
				state.FailureCount++
			}
			if code == "owner_mismatch" {
				state.ReenrollmentRequired = true
				state.Reason = code
				state.NextAttemptAt = time.Time{}
			} else if !state.ReenrollmentRequired {
				state.Reason = ""
				state.NextAttemptAt = now.Add(remoteRenewalBackoff(state.FailureCount))
			}
			if _, err := h.commitAgentEnrollmentRejection(rejected.enrollment, code, state); err != nil {
				result = errors.Join(result, fmt.Errorf("commit rejected agent PKI enrollment %q: %w", requestID, err))
				continue
			}
		} else if err := h.store.RejectPendingEnrollment(rejected.enrollment.StorageIdentity, requestID, code); err != nil {
			result = errors.Join(result, fmt.Errorf("record rejected PKI enrollment %q: %w", requestID, err))
			continue
		}
		h.mu.Lock()
		delete(h.inflight, requestID)
		h.mu.Unlock()
	}
	return result
}

func (h *remotePKIHeartbeatHandler) commitAgentEnrollmentRejection(enrollment modulepki.PendingEnrollment, code string, state modulepki.RenewalState) (modulepki.RenewalState, error) {
	requestID := strings.TrimSpace(enrollment.Request.RequestID)
	code = strings.TrimSpace(code)
	state.PendingRejectionRequestID = requestID
	state.PendingRejectionCode = code
	intent, err := h.store.SaveRenewalState(remoteAgentPKIStorageIdentity, state)
	if err != nil {
		return modulepki.RenewalState{}, fmt.Errorf("persist PKI rejection intent: %w", err)
	}
	if err := h.store.RejectPendingEnrollment(remoteAgentPKIStorageIdentity, requestID, code); err != nil {
		return modulepki.RenewalState{}, fmt.Errorf("quarantine pending PKI enrollment: %w", err)
	}
	h.mu.Lock()
	delete(h.inflight, requestID)
	h.mu.Unlock()
	intent.PendingRejectionRequestID = ""
	intent.PendingRejectionCode = ""
	committed, err := h.store.SaveRenewalState(remoteAgentPKIStorageIdentity, intent)
	if err != nil {
		return modulepki.RenewalState{}, fmt.Errorf("finalize PKI rejection intent: %w", err)
	}
	return committed, nil
}

func (h *remotePKIHeartbeatHandler) reconcilePendingRejection(pending []modulepki.PendingEnrollment) ([]modulepki.PendingEnrollment, error) {
	state, ok, err := h.loadRenewalState()
	if err != nil || !ok || state.PendingRejectionRequestID == "" {
		return pending, err
	}
	requestID := state.PendingRejectionRequestID
	if err := h.store.RejectPendingEnrollment(remoteAgentPKIStorageIdentity, requestID, state.PendingRejectionCode); err != nil {
		return nil, fmt.Errorf("reconcile pending PKI rejection %q: %w", requestID, err)
	}
	state.PendingRejectionRequestID = ""
	state.PendingRejectionCode = ""
	if _, err := h.store.SaveRenewalState(remoteAgentPKIStorageIdentity, state); err != nil {
		return nil, fmt.Errorf("finalize reconciled PKI rejection %q: %w", requestID, err)
	}
	h.mu.Lock()
	delete(h.inflight, requestID)
	h.mu.Unlock()
	return withoutEnrollmentRequest(pending, remoteAgentPKIStorageIdentity, requestID), nil
}

func (h *remotePKIHeartbeatHandler) ensureAgentRenewalAfterReply(ctx context.Context) error {
	pending, err := h.store.PendingEnrollments()
	if err != nil {
		return fmt.Errorf("reload pending PKI enrollments after heartbeat: %w", err)
	}
	_, err = h.ensureAgentRenewalPending(ctx, pending)
	return err
}

func (h *remotePKIHeartbeatHandler) ensureAgentRenewalPending(ctx context.Context, pending []modulepki.PendingEnrollment) ([]modulepki.PendingEnrollment, error) {
	var err error
	pending, err = h.reconcilePendingRejection(pending)
	if err != nil {
		return nil, err
	}
	now := h.currentTime()
	state, hasState, err := h.loadRenewalState()
	if err != nil {
		return nil, err
	}
	agentPending, err := findRemoteAgentEnrollment(pending)
	if err != nil {
		return nil, err
	}
	security, err := h.store.LoadSecuritySnapshot()
	if err != nil {
		if !errors.Is(err, modulepki.ErrSecurityInvalid) {
			return nil, fmt.Errorf("load PKI security state for renewal: %w", err)
		}
	}
	securityAvailable := err == nil
	active, activeErr := h.store.LoadActiveCredential(remoteAgentPKIStorageIdentity)
	if activeErr != nil && !errors.Is(activeErr, modulepki.ErrActiveCredential) && !errors.Is(activeErr, modulepki.ErrCredentialInvalid) {
		return nil, fmt.Errorf("load active PKI credential for renewal: %w", activeErr)
	}
	if activeErr == nil && securityAvailable {
		if err := validateRemoteAgentCredentialOwner(active, security, h.agentID); err != nil {
			state, err = h.reenrollmentStateForCredential(active, "owner_mismatch", now)
			if err != nil {
				return nil, err
			}
			return h.enterAgentReenrollment(pending, agentPending, state, "owner_mismatch")
		}
		candidate := renewalStateForCredential(active, h.agentID)
		if !hasState || !renewalStateMatchesCredential(state, candidate) {
			state = candidate
			state, err = h.store.SaveRenewalState(remoteAgentPKIStorageIdentity, state)
			if err != nil {
				return nil, fmt.Errorf("persist stable PKI renewal schedule: %w", err)
			}
			hasState = true
		}
	}
	if agentPending != nil {
		pendingAgentID := strings.TrimSpace(agentPending.AgentID)
		if pendingAgentID != "" && pendingAgentID != h.agentID {
			if activeErr == nil {
				state, err = h.reenrollmentStateForCredential(active, "owner_mismatch", now)
				if err != nil {
					return nil, err
				}
			} else {
				if !hasState {
					state = fallbackRenewalState(h.agentID, strings.TrimSpace(agentPending.DomainID), now)
				}
				state.ReenrollmentRequired = true
				state.Reason = "owner_mismatch"
				state.NextAttemptAt = time.Time{}
			}
			return h.enterAgentReenrollment(pending, agentPending, state, "owner_mismatch")
		}
	}
	if agentPending != nil && securityAvailable && remoteAgentEnrollmentOwnerMismatch(*agentPending, security, h.agentID) {
		if activeErr == nil {
			state, err = h.reenrollmentStateForCredential(active, "owner_mismatch", now)
			if err != nil {
				return nil, err
			}
		} else {
			if !hasState {
				state = fallbackRenewalState(h.agentID, security.Snapshot.PKIDomainID, now)
			}
			state.ReenrollmentRequired = true
			state.Reason = "owner_mismatch"
			state.NextAttemptAt = time.Time{}
		}
		return h.enterAgentReenrollment(pending, agentPending, state, "owner_mismatch")
	}
	if hasState && state.ReenrollmentRequired {
		if agentPending != nil {
			if _, err := h.commitAgentEnrollmentRejection(*agentPending, "reenrollment_required", state); err != nil {
				return nil, fmt.Errorf("reconcile PKI re-enrollment state: %w", err)
			}
		}
		return withoutRemoteAgentEnrollment(pending), nil
	}
	if hasState && !state.NextAttemptAt.IsZero() && now.Before(state.NextAttemptAt) {
		if agentPending != nil {
			if _, err := h.commitAgentEnrollmentRejection(*agentPending, "renewal_backoff", state); err != nil {
				return nil, fmt.Errorf("reconcile PKI renewal backoff: %w", err)
			}
		}
		return withoutRemoteAgentEnrollment(pending), nil
	}
	if agentPending != nil {
		if !securityAvailable && strings.TrimSpace(agentPending.DomainID) != "" {
			return withoutRemoteAgentEnrollment(pending), nil
		}
		return pending, nil
	}
	if !securityAvailable {
		return pending, nil
	}
	needsRenewal := errors.Is(activeErr, modulepki.ErrActiveCredential)
	if activeErr == nil {
		if agentCredentialSignerNeedsRenewal(active, security) {
			lifecycleDue := stableLifecycleRenewalDue(active, security, h.agentID, now)
			if state.DueAt.After(lifecycleDue) {
				state.DueAt = lifecycleDue
				state, err = h.store.SaveRenewalState(remoteAgentPKIStorageIdentity, state)
				if err != nil {
					return nil, fmt.Errorf("persist PKI lifecycle renewal schedule: %w", err)
				}
			}
		}
		needsRenewal = !now.Before(state.DueAt) || !now.Before(active.Manifest.Credential.NotAfter) || now.Before(active.Manifest.Credential.NotBefore)
	} else if errors.Is(activeErr, modulepki.ErrCredentialInvalid) {
		var invalid *modulepki.CredentialInvalidError
		if errors.As(activeErr, &invalid) &&
			(invalid.Reason == modulepki.CredentialInvalidExpired || invalid.Reason == modulepki.CredentialInvalidNotYetValid || invalid.Reason == modulepki.CredentialInvalidSignerLifecycle) {
			if hasState && (!state.NextAttemptAt.IsZero() && now.Before(state.NextAttemptAt)) {
				return pending, nil
			}
			needsRenewal = true
		} else {
			reason := "invalid_credential"
			if invalid != nil && invalid.Reason != "" {
				reason = string(invalid.Reason)
			}
			if !hasState {
				state = fallbackRenewalState(h.agentID, security.Snapshot.PKIDomainID, now)
			}
			state.ReenrollmentRequired = true
			state.Reason = reason
			state.NextAttemptAt = time.Time{}
			if _, err := h.store.SaveRenewalState(remoteAgentPKIStorageIdentity, state); err != nil {
				return nil, fmt.Errorf("persist PKI re-enrollment requirement: %w", err)
			}
			return pending, nil
		}
	} else if hasState && !state.NextAttemptAt.IsZero() && now.Before(state.NextAttemptAt) {
		return pending, nil
	}
	if !needsRenewal {
		return pending, nil
	}
	if _, err := h.store.PrepareEnrollment(ctx, modulepki.EnrollmentSpec{
		StorageIdentity: remoteAgentPKIStorageIdentity,
		DomainID:        strings.TrimSpace(security.Snapshot.PKIDomainID),
		AgentID:         h.agentID,
		Kind:            model.PKIIdentityKindAgent,
		Purpose:         model.PKICertificatePurposeClient,
	}); err != nil {
		if !hasState {
			state = fallbackRenewalState(h.agentID, security.Snapshot.PKIDomainID, now)
		}
		if state.FailureCount < remoteRenewalFailureCountLimit {
			state.FailureCount++
		}
		state.NextAttemptAt = now.Add(remoteRenewalBackoff(state.FailureCount))
		if _, stateErr := h.store.SaveRenewalState(remoteAgentPKIStorageIdentity, state); stateErr != nil {
			return nil, errors.Join(fmt.Errorf("prepare durable agent PKI renewal: %w", err), fmt.Errorf("persist PKI renewal backoff: %w", stateErr))
		}
		return nil, fmt.Errorf("prepare durable agent PKI renewal: %w", err)
	}
	reloaded, err := h.store.PendingEnrollments()
	if err != nil {
		return nil, fmt.Errorf("reload prepared agent PKI renewal: %w", err)
	}
	return reloaded, nil
}

func (h *remotePKIHeartbeatHandler) enterAgentReenrollment(pending []modulepki.PendingEnrollment, enrollment *modulepki.PendingEnrollment, state modulepki.RenewalState, code string) ([]modulepki.PendingEnrollment, error) {
	if enrollment != nil {
		if _, err := h.commitAgentEnrollmentRejection(*enrollment, code, state); err != nil {
			return nil, fmt.Errorf("persist PKI owner mismatch recovery: %w", err)
		}
	} else if _, err := h.store.SaveRenewalState(remoteAgentPKIStorageIdentity, state); err != nil {
		return nil, fmt.Errorf("persist PKI owner mismatch recovery: %w", err)
	}
	return withoutRemoteAgentEnrollment(pending), nil
}

func findRemoteAgentEnrollment(pending []modulepki.PendingEnrollment) (*modulepki.PendingEnrollment, error) {
	var found *modulepki.PendingEnrollment
	for index := range pending {
		if pending[index].StorageIdentity != remoteAgentPKIStorageIdentity {
			continue
		}
		if pending[index].Request.Kind != model.PKIIdentityKindAgent {
			return nil, fmt.Errorf("%w: remote agent storage contains a non-agent enrollment", modulepki.ErrCredentialInvalid)
		}
		if found != nil {
			return nil, fmt.Errorf("%w: remote agent storage contains duplicate enrollments", modulepki.ErrCredentialInvalid)
		}
		found = &pending[index]
	}
	return found, nil
}

func remoteAgentEnrollmentOwnerMismatch(enrollment modulepki.PendingEnrollment, security modulepki.SecurityState, agentID string) bool {
	pendingAgentID := strings.TrimSpace(enrollment.AgentID)
	pendingDomainID := strings.TrimSpace(enrollment.DomainID)
	return (pendingAgentID != "" && pendingAgentID != strings.TrimSpace(agentID)) ||
		(pendingDomainID != "" && pendingDomainID != strings.TrimSpace(security.Snapshot.PKIDomainID))
}

func (h *remotePKIHeartbeatHandler) currentTime() time.Time {
	if h != nil && h.now != nil {
		return h.now().UTC()
	}
	return time.Now().UTC()
}

func (h *remotePKIHeartbeatHandler) loadRenewalState() (modulepki.RenewalState, bool, error) {
	state, err := h.store.LoadRenewalState(remoteAgentPKIStorageIdentity)
	if errors.Is(err, modulepki.ErrRenewalStateNotFound) {
		return modulepki.RenewalState{}, false, nil
	}
	if err != nil {
		return modulepki.RenewalState{}, false, fmt.Errorf("load durable PKI renewal state: %w", err)
	}
	return state, true, nil
}

func (h *remotePKIHeartbeatHandler) reenrollmentRequired() (bool, error) {
	state, ok, err := h.loadRenewalState()
	if err != nil {
		return false, err
	}
	return ok && state.ReenrollmentRequired, nil
}

func (h *remotePKIHeartbeatHandler) persistHealthyRenewalState(active modulepki.CredentialMetadata) (modulepki.RenewalState, error) {
	state := renewalStateForCredential(active, h.agentID)
	return h.store.SaveRenewalState(remoteAgentPKIStorageIdentity, state)
}

func (h *remotePKIHeartbeatHandler) currentOrFallbackRenewalState(now time.Time) (modulepki.RenewalState, error) {
	state, ok, err := h.loadRenewalState()
	if err != nil || ok {
		return state, err
	}
	security, securityErr := h.store.LoadSecuritySnapshot()
	if securityErr != nil && !errors.Is(securityErr, modulepki.ErrSecurityInvalid) {
		return modulepki.RenewalState{}, securityErr
	}
	active, activeErr := h.store.LoadActiveCredential(remoteAgentPKIStorageIdentity)
	if activeErr == nil {
		return renewalStateForCredential(active, h.agentID), nil
	}
	if !errors.Is(activeErr, modulepki.ErrActiveCredential) && !errors.Is(activeErr, modulepki.ErrCredentialInvalid) {
		return modulepki.RenewalState{}, activeErr
	}
	return fallbackRenewalState(h.agentID, security.Snapshot.PKIDomainID, now), nil
}

func (h *remotePKIHeartbeatHandler) reenrollmentStateForCredential(active modulepki.CredentialMetadata, reason string, now time.Time) (modulepki.RenewalState, error) {
	state, ok, err := h.loadRenewalState()
	if err != nil {
		return modulepki.RenewalState{}, err
	}
	candidate := renewalStateForCredential(active, h.agentID)
	if !ok || !renewalStateMatchesCredential(state, candidate) {
		state = candidate
	}
	state.ReenrollmentRequired = true
	state.Reason = reason
	state.NextAttemptAt = time.Time{}
	if state.DueAt.IsZero() {
		state.DueAt = now
	}
	return state, nil
}

func renewalStateForCredential(active modulepki.CredentialMetadata, agentID string) modulepki.RenewalState {
	credential := active.Manifest.Credential
	identity := strings.TrimSpace(credential.IdentityID)
	if identity == "" {
		identity = strings.TrimSpace(active.Manifest.Expectation.AgentID)
	}
	if identity == "" {
		identity = strings.TrimSpace(agentID)
	}
	fingerprint := credentialRenewalFingerprint(credential)
	return modulepki.RenewalState{
		CredentialIdentity:    identity,
		CredentialFingerprint: fingerprint,
		DueAt:                 stableCredentialRenewalDue(credential, agentID, fingerprint),
	}
}

func fallbackRenewalState(agentID, domainID string, now time.Time) modulepki.RenewalState {
	identity := strings.TrimSpace(agentID)
	digest := sha256.Sum256([]byte("unavailable\x00" + identity + "\x00" + strings.TrimSpace(domainID)))
	return modulepki.RenewalState{
		CredentialIdentity:    identity,
		CredentialFingerprint: fmt.Sprintf("%x", digest[:]),
		DueAt:                 now,
	}
}

func renewalStateMatchesCredential(state, candidate modulepki.RenewalState) bool {
	return state.CredentialIdentity == candidate.CredentialIdentity && state.CredentialFingerprint == candidate.CredentialFingerprint
}

func credentialRenewalFingerprint(credential model.PKITunnelCredential) string {
	payload := strings.Join([]string{
		strings.TrimSpace(credential.IdentityID),
		strings.TrimSpace(credential.CertificateID),
		strings.TrimSpace(credential.PublicKeyFingerprint),
		strings.TrimSpace(credential.AuthorityID),
		fmt.Sprintf("%d", credential.CAGeneration),
	}, "\x00")
	digest := sha256.Sum256([]byte(payload))
	return fmt.Sprintf("%x", digest[:])
}

func stableCredentialRenewalDue(credential model.PKITunnelCredential, agentID, fingerprint string) time.Time {
	lifetime := credential.NotAfter.Sub(credential.NotBefore)
	if lifetime <= 0 {
		return credential.NotAfter.UTC()
	}
	base := credential.NotAfter.Add(-lifetime / 3).UTC()
	window := lifetime / 12
	if window <= 0 {
		return base
	}
	digest := sha256.Sum256([]byte(strings.TrimSpace(agentID) + "\x00" + fingerprint))
	offset := time.Duration(binary.BigEndian.Uint64(digest[:8]) % uint64(window+1))
	return base.Add(offset - window/2)
}

func stableLifecycleRenewalDue(active modulepki.CredentialMetadata, security modulepki.SecurityState, agentID string, now time.Time) time.Time {
	fingerprint := credentialRenewalFingerprint(active.Manifest.Credential)
	digest := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s\x00%d\x00%d\x00%s", strings.TrimSpace(agentID), fingerprint,
		security.Snapshot.PKIEpoch, security.Snapshot.SecurityRevision, security.Hash)))
	delay := time.Duration(binary.BigEndian.Uint64(digest[:8]) % uint64(remoteLifecycleJitterWindow+1))
	base := security.Snapshot.IssuedAt.UTC()
	if base.IsZero() || base.After(now) {
		base = now
	}
	return base.Add(delay)
}

func remoteRenewalBackoff(failureCount int) time.Duration {
	if failureCount <= 1 {
		return remoteRenewalBackoffBase
	}
	backoff := remoteRenewalBackoffBase
	for attempt := 1; attempt < failureCount && backoff < remoteRenewalBackoffMaximum; attempt++ {
		if backoff > remoteRenewalBackoffMaximum/2 {
			return remoteRenewalBackoffMaximum
		}
		backoff *= 2
	}
	if backoff > remoteRenewalBackoffMaximum {
		return remoteRenewalBackoffMaximum
	}
	return backoff
}

func withoutRemoteAgentEnrollment(pending []modulepki.PendingEnrollment) []modulepki.PendingEnrollment {
	filtered := make([]modulepki.PendingEnrollment, 0, len(pending))
	for _, enrollment := range pending {
		if enrollment.StorageIdentity == remoteAgentPKIStorageIdentity && enrollment.Request.Kind == model.PKIIdentityKindAgent {
			continue
		}
		filtered = append(filtered, enrollment)
	}
	return filtered
}

func withoutEnrollmentRequest(pending []modulepki.PendingEnrollment, storageIdentity, requestID string) []modulepki.PendingEnrollment {
	filtered := make([]modulepki.PendingEnrollment, 0, len(pending))
	for _, enrollment := range pending {
		if enrollment.StorageIdentity == storageIdentity && enrollment.Request.RequestID == requestID {
			continue
		}
		filtered = append(filtered, enrollment)
	}
	return filtered
}

func validateRemoteAgentCredentialOwner(active modulepki.CredentialMetadata, security modulepki.SecurityState, agentID string) error {
	expectation := active.Manifest.Expectation
	domainID := strings.TrimSpace(security.Snapshot.PKIDomainID)
	if strings.TrimSpace(agentID) == "" || domainID == "" ||
		expectation.Kind != model.PKIIdentityKindAgent ||
		expectation.Purpose != model.PKICertificatePurposeClient ||
		strings.TrimSpace(expectation.ListenerID) != "" ||
		strings.TrimSpace(expectation.AgentID) != strings.TrimSpace(agentID) ||
		strings.TrimSpace(expectation.DomainID) != domainID ||
		strings.TrimSpace(active.Manifest.PKIDomainID) != domainID {
		return fmt.Errorf("%w: active tunnel credential belongs to a different agent or PKI domain", modulepki.ErrCredentialInvalid)
	}
	return nil
}

func agentCredentialSignerNeedsRenewal(active modulepki.CredentialMetadata, security modulepki.SecurityState) bool {
	credential := active.Manifest.Credential
	for _, root := range security.Snapshot.TrustRoots {
		if root.AuthorityID == credential.AuthorityID && root.Generation == credential.CAGeneration {
			status := strings.TrimSpace(root.Status)
			return status != "active" && status != "prepared"
		}
	}
	return true
}

type pkiControlDegradedError struct {
	code         string
	recoveryHint string
}

func (e *pkiControlDegradedError) Error() string {
	if e == nil {
		return "PKI control runtime is degraded"
	}
	message := "PKI control runtime is degraded"
	if e.code != "" {
		message += ": " + e.code
	}
	if e.recoveryHint != "" {
		message += " (" + e.recoveryHint + ")"
	}
	return message
}

func validatePKIControlStatus(status *model.PKIControlStatus) (*pkiControlDegradedError, error) {
	if status == nil {
		return nil, nil
	}
	switch strings.TrimSpace(status.Status) {
	case "ready":
		return nil, nil
	case "degraded":
		return &pkiControlDegradedError{
			code: strings.TrimSpace(status.Code), recoveryHint: strings.TrimSpace(status.RecoveryHint),
		}, nil
	default:
		return nil, fmt.Errorf("%w: unknown PKI control status %q", modulepki.ErrSecurityInvalid, status.Status)
	}
}

func clonePKIEnrollmentRequest(request model.PKIEnrollmentRequest) model.PKIEnrollmentRequest {
	cloned := request
	cloned.DNSNames = append([]string(nil), request.DNSNames...)
	cloned.IPAddresses = append([]string(nil), request.IPAddresses...)
	return cloned
}

func clonePendingEnrollment(enrollment modulepki.PendingEnrollment) modulepki.PendingEnrollment {
	cloned := enrollment
	cloned.Request = clonePKIEnrollmentRequest(enrollment.Request)
	return cloned
}
