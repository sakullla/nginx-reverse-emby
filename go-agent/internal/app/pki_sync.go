package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/control"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	modulepki "github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/pki"
)

const remoteAgentPKIStorageIdentity = "agent"

type remotePKIStore interface {
	PendingEnrollments() ([]modulepki.PendingEnrollment, error)
	LoadStagedRegistration(string) (modulepki.StagedRegistration, modulepki.PendingEnrollment, error)
	ActivateStagedRegistration(context.Context, string) (modulepki.CredentialMetadata, error)
	SecurityAcknowledgement(string) (model.PKISecurityAcknowledgement, error)
	ApplySecuritySnapshot(model.PKISecuritySnapshot) (modulepki.SecurityState, error)
	ActivateCredential(context.Context, modulepki.ActivateRequest) (modulepki.CredentialMetadata, error)
}

type remotePKIHeartbeatHandler struct {
	store   remotePKIStore
	agentID string

	mu       sync.Mutex
	inflight map[string]modulepki.PendingEnrollment
}

func newRemotePKIHeartbeatHandler(store remotePKIStore, agentID string) *remotePKIHeartbeatHandler {
	return &remotePKIHeartbeatHandler{
		store: store, agentID: strings.TrimSpace(agentID),
		inflight: make(map[string]modulepki.PendingEnrollment),
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
		if _, err := h.store.ActivateStagedRegistration(ctx, enrollment.StorageIdentity); err != nil {
			return control.PKIHeartbeatState{}, fmt.Errorf("activate staged PKI registration: %w", err)
		}
		activatedStaged = true
	}
	if activatedStaged {
		pending, err = h.store.PendingEnrollments()
		if err != nil {
			return control.PKIHeartbeatState{}, fmt.Errorf("reload pending PKI enrollments: %w", err)
		}
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

	acknowledgement, err := h.store.SecurityAcknowledgement(remoteAgentPKIStorageIdentity)
	if err == nil {
		state.SecurityAcknowledgement = &acknowledgement
	} else if !errors.Is(err, modulepki.ErrActiveCredential) {
		return control.PKIHeartbeatState{}, fmt.Errorf("load durable PKI acknowledgement: %w", err)
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

	type activation struct {
		response   model.PKIControlCredential
		enrollment modulepki.PendingEnrollment
	}
	activations := make([]activation, 0, len(reply.Credentials))
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
			activations = append(activations, activation{response: response, enrollment: enrollment})
			continue
		}
		if strings.TrimSpace(response.Credential.CertificateID) == "" {
			return fmt.Errorf("%w: successful PKI credential response is empty", modulepki.ErrCredentialInvalid)
		}
		activations = append(activations, activation{response: response, enrollment: enrollment})
	}

	if reply.Security != nil {
		if _, err := h.store.ApplySecuritySnapshot(*reply.Security); err != nil {
			return fmt.Errorf("apply PKI security snapshot: %w", err)
		}
	}
	if degraded != nil {
		return degraded
	}
	for _, candidate := range activations {
		if code := strings.TrimSpace(candidate.response.Error); code != "" {
			return fmt.Errorf("PKI enrollment %q was rejected: %s", candidate.response.RequestID, code)
		}
	}
	if len(activations) != 0 && reply.Security == nil {
		return fmt.Errorf("%w: PKI credential response is missing its security snapshot", modulepki.ErrSecurityInvalid)
	}

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
		_, err := h.store.ActivateCredential(ctx, modulepki.ActivateRequest{
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
			return fmt.Errorf("activate PKI credential %q: %w", pending.Request.RequestID, err)
		}
		h.mu.Lock()
		delete(h.inflight, pending.Request.RequestID)
		h.mu.Unlock()
	}
	return nil
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
