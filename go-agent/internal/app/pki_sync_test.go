package app

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/control"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	modulepki "github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/pki"
)

type fakeRemotePKIStore struct {
	pending            []modulepki.PendingEnrollment
	staged             map[string]modulepki.StagedRegistration
	stagedPending      map[string]modulepki.PendingEnrollment
	loadStagedErr      error
	stagedActivations  []string
	acknowledgement    model.PKISecurityAcknowledgement
	acknowledgementErr error
	appliedSecurity    []model.PKISecuritySnapshot
	credentialRequests []modulepki.ActivateRequest
	credentialErr      error
}

func (s *fakeRemotePKIStore) PendingEnrollments() ([]modulepki.PendingEnrollment, error) {
	result := make([]modulepki.PendingEnrollment, len(s.pending))
	for i := range s.pending {
		result[i] = clonePendingEnrollment(s.pending[i])
	}
	return result, nil
}

func (s *fakeRemotePKIStore) LoadStagedRegistration(storageIdentity string) (modulepki.StagedRegistration, modulepki.PendingEnrollment, error) {
	if s.loadStagedErr != nil {
		return modulepki.StagedRegistration{}, modulepki.PendingEnrollment{}, s.loadStagedErr
	}
	staged, ok := s.staged[storageIdentity]
	if !ok {
		return modulepki.StagedRegistration{}, modulepki.PendingEnrollment{}, modulepki.ErrStagedRegistrationNotFound
	}
	return staged, clonePendingEnrollment(s.stagedPending[storageIdentity]), nil
}

func (s *fakeRemotePKIStore) ActivateStagedRegistration(_ context.Context, storageIdentity string) (modulepki.CredentialMetadata, error) {
	s.stagedActivations = append(s.stagedActivations, storageIdentity)
	remaining := s.pending[:0]
	for _, pending := range s.pending {
		if pending.StorageIdentity != storageIdentity {
			remaining = append(remaining, pending)
		}
	}
	s.pending = remaining
	return modulepki.CredentialMetadata{}, nil
}

func (s *fakeRemotePKIStore) SecurityAcknowledgement(string) (model.PKISecurityAcknowledgement, error) {
	return s.acknowledgement, s.acknowledgementErr
}

func (s *fakeRemotePKIStore) ApplySecuritySnapshot(snapshot model.PKISecuritySnapshot) (modulepki.SecurityState, error) {
	s.appliedSecurity = append(s.appliedSecurity, snapshot)
	return modulepki.SecurityState{Snapshot: snapshot}, nil
}

func (s *fakeRemotePKIStore) ActivateCredential(_ context.Context, request modulepki.ActivateRequest) (modulepki.CredentialMetadata, error) {
	s.credentialRequests = append(s.credentialRequests, request)
	return modulepki.CredentialMetadata{}, s.credentialErr
}

func TestRemotePKIHeartbeatPrepareRestoresStagedRegistration(t *testing.T) {
	agentPending := testPendingEnrollment("agent", "request-agent", model.PKIIdentityKindAgent)
	listenerPending := testPendingEnrollment("listener-1", "request-listener", model.PKIIdentityKindListener)
	listenerPending.Request.ListenerID = "listener-1"
	listenerPending.Request.Purpose = model.PKICertificatePurposeServer
	listenerPending.Request.DNSNames = []string{"relay.example.com"}
	store := &fakeRemotePKIStore{
		pending: []modulepki.PendingEnrollment{agentPending, listenerPending},
		staged: map[string]modulepki.StagedRegistration{
			"agent": {AgentID: "agent-1"},
		},
		stagedPending: map[string]modulepki.PendingEnrollment{"agent": agentPending},
		acknowledgement: model.PKISecurityAcknowledgement{
			PKIDomainID: "domain-1", PKIEpoch: 2, SecurityRevision: 7, Full: true,
		},
	}
	handler := newRemotePKIHeartbeatHandler(store, "agent-1")

	state, err := handler.PrepareHeartbeat(t.Context())
	if err != nil {
		t.Fatalf("PrepareHeartbeat() error = %v", err)
	}
	if !reflect.DeepEqual(store.stagedActivations, []string{"agent"}) {
		t.Fatalf("staged activations = %+v", store.stagedActivations)
	}
	if state.SecurityAcknowledgement == nil || state.SecurityAcknowledgement.SecurityRevision != 7 {
		t.Fatalf("security acknowledgement = %+v", state.SecurityAcknowledgement)
	}
	if len(state.EnrollmentRequests) != 1 || state.EnrollmentRequests[0].RequestID != "request-listener" {
		t.Fatalf("outgoing enrollment requests = %+v", state.EnrollmentRequests)
	}
	state.EnrollmentRequests[0].DNSNames[0] = "mutated.example.com"
	if store.pending[0].Request.DNSNames[0] != "relay.example.com" {
		t.Fatal("outgoing enrollment request shares mutable state with the durable journal")
	}
}

func TestRemotePKIHeartbeatPrepareRejectsStagedOwnerMismatchAndCorruption(t *testing.T) {
	pending := testPendingEnrollment("agent", "request-agent", model.PKIIdentityKindAgent)
	tests := []struct {
		name   string
		store  *fakeRemotePKIStore
		isWant error
	}{
		{
			name: "owner mismatch",
			store: &fakeRemotePKIStore{
				pending:       []modulepki.PendingEnrollment{pending},
				staged:        map[string]modulepki.StagedRegistration{"agent": {AgentID: "agent-2"}},
				stagedPending: map[string]modulepki.PendingEnrollment{"agent": pending},
			},
			isWant: modulepki.ErrPendingConflict,
		},
		{
			name: "corrupt pending",
			store: &fakeRemotePKIStore{
				pending:       []modulepki.PendingEnrollment{pending},
				loadStagedErr: modulepki.ErrCredentialInvalid,
			},
			isWant: modulepki.ErrCredentialInvalid,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handler := newRemotePKIHeartbeatHandler(test.store, "agent-1")
			_, err := handler.PrepareHeartbeat(t.Context())
			if !errors.Is(err, test.isWant) {
				t.Fatalf("PrepareHeartbeat() error = %v, want %v", err, test.isWant)
			}
		})
	}
}

func TestRemotePKIHeartbeatAppliesPreparedCredentialWithDurableOwner(t *testing.T) {
	pending := testPendingEnrollment("listener-1", "request-listener", model.PKIIdentityKindListener)
	pending.DomainID = "domain-1"
	pending.AgentID = "agent-1"
	pending.Request.ListenerID = "listener-1"
	pending.Request.Purpose = model.PKICertificatePurposeServer
	pending.Request.DNSNames = []string{"relay.example.com"}
	store := &fakeRemotePKIStore{
		pending:            []modulepki.PendingEnrollment{pending},
		acknowledgementErr: modulepki.ErrActiveCredential,
	}
	handler := newRemotePKIHeartbeatHandler(store, "agent-1")
	if _, err := handler.PrepareHeartbeat(t.Context()); err != nil {
		t.Fatalf("PrepareHeartbeat() error = %v", err)
	}
	security := model.PKISecuritySnapshot{PKIDomainID: "domain-1", PKIEpoch: 2, SecurityRevision: 8, Full: true}
	err := handler.ApplyHeartbeat(t.Context(), control.PKIHeartbeatReply{
		Security: &security,
		Credentials: []model.PKIControlCredential{{
			RequestID:  "request-listener",
			Credential: model.PKITunnelCredential{CertificateID: "certificate-1"},
		}},
		Status: &model.PKIControlStatus{Status: "ready"},
	})
	if err != nil {
		t.Fatalf("ApplyHeartbeat() error = %v", err)
	}
	if len(store.appliedSecurity) != 1 || store.appliedSecurity[0].SecurityRevision != 8 {
		t.Fatalf("applied security = %+v", store.appliedSecurity)
	}
	if len(store.credentialRequests) != 1 {
		t.Fatalf("credential activations = %+v", store.credentialRequests)
	}
	request := store.credentialRequests[0]
	if request.StorageIdentity != "listener-1" || request.Expectation.DomainID != "domain-1" ||
		request.Expectation.AgentID != "agent-1" || request.Expectation.ListenerID != "listener-1" ||
		!reflect.DeepEqual(request.Expectation.DNSNames, []string{"relay.example.com"}) {
		t.Fatalf("credential activation was not bound to the durable pending owner: %+v", request)
	}
}

func TestRemotePKIHeartbeatFailsClosedForDegradedAndUnrequestedResponses(t *testing.T) {
	pending := testPendingEnrollment("agent", "request-agent", model.PKIIdentityKindAgent)
	store := &fakeRemotePKIStore{
		pending:            []modulepki.PendingEnrollment{pending},
		acknowledgementErr: modulepki.ErrActiveCredential,
	}
	handler := newRemotePKIHeartbeatHandler(store, "agent-1")
	if _, err := handler.PrepareHeartbeat(t.Context()); err != nil {
		t.Fatalf("PrepareHeartbeat() error = %v", err)
	}
	security := model.PKISecuritySnapshot{PKIDomainID: "domain-1", PKIEpoch: 2, SecurityRevision: 8, Full: true}
	err := handler.ApplyHeartbeat(t.Context(), control.PKIHeartbeatReply{
		Security: &security,
		Status: &model.PKIControlStatus{
			Status: "degraded", Code: "runtime_unavailable", RecoveryHint: "retry ordinary control sync",
		},
	})
	var degraded *pkiControlDegradedError
	if !errors.As(err, &degraded) {
		t.Fatalf("ApplyHeartbeat(degraded) error = %v", err)
	}
	if len(store.appliedSecurity) != 1 {
		t.Fatal("degraded heartbeat did not retain its valid security update")
	}
	if len(store.credentialRequests) != 0 {
		t.Fatal("degraded heartbeat activated a credential")
	}

	err = handler.ApplyHeartbeat(t.Context(), control.PKIHeartbeatReply{
		Security: &security,
		Credentials: []model.PKIControlCredential{{
			RequestID: "unsolicited", Credential: model.PKITunnelCredential{CertificateID: "certificate-2"},
		}},
	})
	if !errors.Is(err, modulepki.ErrPendingConflict) {
		t.Fatalf("ApplyHeartbeat(unsolicited) error = %v", err)
	}
}

func testPendingEnrollment(storageIdentity, requestID, kind string) modulepki.PendingEnrollment {
	return modulepki.PendingEnrollment{
		StorageIdentity: storageIdentity,
		Request: model.PKIEnrollmentRequest{
			RequestID: requestID, Kind: kind, Purpose: model.PKICertificatePurposeClient, CSRPEM: "PUBLIC CSR",
		},
	}
}
