package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

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
	security           modulepki.SecurityState
	securityErr        error
	active             map[string]modulepki.CredentialMetadata
	activeErr          error
	preparedSpecs      []modulepki.EnrollmentSpec
	prepareErr         error
	rejected           []fakeRejectedEnrollment
	rejectErr          error
	renewal            map[string]modulepki.RenewalState
	renewalErr         error
	savedRenewal       []modulepki.RenewalState
}

type fakeRejectedEnrollment struct {
	storageIdentity string
	requestID       string
	code            string
}

func (s *fakeRemotePKIStore) PendingEnrollments() ([]modulepki.PendingEnrollment, error) {
	result := make([]modulepki.PendingEnrollment, len(s.pending))
	for i := range s.pending {
		result[i] = clonePendingEnrollment(s.pending[i])
	}
	return result, nil
}

func (s *fakeRemotePKIStore) PrepareEnrollment(_ context.Context, spec modulepki.EnrollmentSpec) (modulepki.PendingEnrollment, error) {
	s.preparedSpecs = append(s.preparedSpecs, spec)
	if s.prepareErr != nil {
		return modulepki.PendingEnrollment{}, s.prepareErr
	}
	pending := testPendingEnrollment(spec.StorageIdentity, fmt.Sprintf("renewal-%d", len(s.preparedSpecs)), spec.Kind)
	pending.DomainID = spec.DomainID
	pending.AgentID = spec.AgentID
	pending.Request.ListenerID = spec.ListenerID
	pending.Request.Purpose = spec.Purpose
	pending.Request.DNSNames = append([]string(nil), spec.DNSNames...)
	pending.Request.IPAddresses = append([]string(nil), spec.IPAddresses...)
	s.pending = append(s.pending, pending)
	return clonePendingEnrollment(pending), nil
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
	staged := s.staged[storageIdentity]
	pending := s.stagedPending[storageIdentity]
	credential := staged.TunnelCredential
	if credential.IdentityID == "" {
		credential.IdentityID = "identity-" + staged.AgentID
	}
	if credential.CertificateID == "" {
		credential.CertificateID = "certificate-" + staged.AgentID
	}
	if credential.NotBefore.IsZero() {
		credential.NotBefore = time.Now().UTC().Add(-time.Hour)
	}
	if credential.NotAfter.IsZero() {
		credential.NotAfter = time.Now().UTC().Add(365 * 24 * time.Hour)
	}
	active := modulepki.CredentialMetadata{
		Manifest: modulepki.CredentialManifest{
			Credential: credential, PKIDomainID: staged.SecuritySnapshot.PKIDomainID,
			Expectation: modulepki.CredentialExpectation{
				DomainID: staged.SecuritySnapshot.PKIDomainID, AgentID: staged.AgentID,
				Kind: pending.Request.Kind, Purpose: pending.Request.Purpose,
			},
		},
		Security: staged.SecuritySnapshot,
	}
	if s.active == nil {
		s.active = make(map[string]modulepki.CredentialMetadata)
	}
	s.active[storageIdentity] = active
	s.security = modulepki.SecurityState{Snapshot: staged.SecuritySnapshot}
	s.activeErr = nil
	remaining := s.pending[:0]
	for _, pending := range s.pending {
		if pending.StorageIdentity != storageIdentity {
			remaining = append(remaining, pending)
		}
	}
	s.pending = remaining
	return active, nil
}

func (s *fakeRemotePKIStore) LoadActiveCredential(storageIdentity string) (modulepki.CredentialMetadata, error) {
	if s.activeErr != nil {
		return modulepki.CredentialMetadata{}, s.activeErr
	}
	active, ok := s.active[storageIdentity]
	if !ok || active.Manifest.Credential.CertificateID == "" {
		return modulepki.CredentialMetadata{}, modulepki.ErrActiveCredential
	}
	return active, nil
}

func (s *fakeRemotePKIStore) LoadSecuritySnapshot() (modulepki.SecurityState, error) {
	if s.securityErr != nil {
		return modulepki.SecurityState{}, s.securityErr
	}
	if s.security.Snapshot.PKIDomainID == "" {
		return modulepki.SecurityState{}, modulepki.ErrSecurityInvalid
	}
	return s.security, nil
}

func (s *fakeRemotePKIStore) SecurityAcknowledgement(string) (model.PKISecurityAcknowledgement, error) {
	return s.acknowledgement, s.acknowledgementErr
}

func (s *fakeRemotePKIStore) ApplySecuritySnapshot(snapshot model.PKISecuritySnapshot) (modulepki.SecurityState, error) {
	s.appliedSecurity = append(s.appliedSecurity, snapshot)
	s.security = modulepki.SecurityState{Snapshot: snapshot}
	return s.security, nil
}

func (s *fakeRemotePKIStore) ActivateCredential(_ context.Context, request modulepki.ActivateRequest) (modulepki.CredentialMetadata, error) {
	s.credentialRequests = append(s.credentialRequests, request)
	if s.credentialErr != nil {
		return modulepki.CredentialMetadata{}, s.credentialErr
	}
	credential := request.Credential
	if credential.NotBefore.IsZero() {
		credential.NotBefore = time.Now().UTC().Add(-time.Hour)
	}
	if credential.NotAfter.IsZero() {
		credential.NotAfter = time.Now().UTC().Add(365 * 24 * time.Hour)
	}
	if s.active == nil {
		s.active = make(map[string]modulepki.CredentialMetadata)
	}
	active := modulepki.CredentialMetadata{
		Manifest: modulepki.CredentialManifest{
			Credential: credential, PKIDomainID: request.Security.PKIDomainID, Expectation: request.Expectation,
		},
		Security: request.Security,
	}
	s.active[request.StorageIdentity] = active
	remaining := s.pending[:0]
	for _, pending := range s.pending {
		if pending.StorageIdentity != request.StorageIdentity || pending.Request.RequestID != request.RequestID {
			remaining = append(remaining, pending)
		}
	}
	s.pending = remaining
	return active, nil
}

func (s *fakeRemotePKIStore) RejectPendingEnrollment(storageIdentity, requestID, code string) error {
	if s.rejectErr != nil {
		return s.rejectErr
	}
	s.rejected = append(s.rejected, fakeRejectedEnrollment{storageIdentity: storageIdentity, requestID: requestID, code: code})
	remaining := s.pending[:0]
	found := false
	for _, pending := range s.pending {
		if pending.StorageIdentity == storageIdentity && pending.Request.RequestID == requestID {
			found = true
			continue
		}
		remaining = append(remaining, pending)
	}
	if !found {
		return modulepki.ErrPendingNotFound
	}
	s.pending = remaining
	return nil
}

func (s *fakeRemotePKIStore) LoadRenewalState(storageIdentity string) (modulepki.RenewalState, error) {
	if s.renewalErr != nil {
		return modulepki.RenewalState{}, s.renewalErr
	}
	state, ok := s.renewal[storageIdentity]
	if !ok {
		return modulepki.RenewalState{}, modulepki.ErrRenewalStateNotFound
	}
	return state, nil
}

func (s *fakeRemotePKIStore) SaveRenewalState(storageIdentity string, state modulepki.RenewalState) (modulepki.RenewalState, error) {
	if s.renewalErr != nil {
		return modulepki.RenewalState{}, s.renewalErr
	}
	if s.renewal == nil {
		s.renewal = make(map[string]modulepki.RenewalState)
	}
	state.Version = 1
	state.UpdatedAt = time.Now().UTC()
	s.renewal[storageIdentity] = state
	s.savedRenewal = append(s.savedRenewal, state)
	return state, nil
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
			"agent": {
				AgentID: "agent-1",
				TunnelCredential: model.PKITunnelCredential{
					IdentityID: "identity-1", CertificateID: "certificate-1",
					NotBefore: time.Now().UTC().Add(-time.Hour), NotAfter: time.Now().UTC().Add(24 * time.Hour),
				},
				SecuritySnapshot: model.PKISecuritySnapshot{
					PKIDomainID: "domain-1", PKIEpoch: 2, SecurityRevision: 7, Full: true,
				},
			},
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

func TestRemotePKIHeartbeatContinuesWithoutAckForInvalidActiveCredential(t *testing.T) {
	pending := testPendingEnrollment("agent", "request-agent", model.PKIIdentityKindAgent)
	store := &fakeRemotePKIStore{
		pending:            []modulepki.PendingEnrollment{pending},
		acknowledgementErr: modulepki.ErrCredentialInvalid,
	}
	handler := newRemotePKIHeartbeatHandler(store, "agent-1")
	state, err := handler.PrepareHeartbeat(t.Context())
	if err != nil {
		t.Fatalf("PrepareHeartbeat() error = %v", err)
	}
	if state.SecurityAcknowledgement != nil {
		t.Fatalf("invalid credential produced acknowledgement: %+v", state.SecurityAcknowledgement)
	}
	if len(state.EnrollmentRequests) != 1 || state.EnrollmentRequests[0].RequestID != "request-agent" {
		t.Fatalf("pending recovery enrollment = %+v", state.EnrollmentRequests)
	}
}

func TestRemotePKIHeartbeatCreatesDurableRenewalAfterStableDueTime(t *testing.T) {
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	security := model.PKISecuritySnapshot{
		PKIDomainID: "domain-1",
		TrustRoots:  []model.PKITrustRoot{{AuthorityID: "authority-1", Generation: 2, Status: "active"}},
	}
	store := &fakeRemotePKIStore{
		security: modulepki.SecurityState{Snapshot: security},
		active: map[string]modulepki.CredentialMetadata{
			"agent": {Manifest: modulepki.CredentialManifest{
				PKIDomainID: "domain-1",
				Expectation: modulepki.CredentialExpectation{
					DomainID: "domain-1", AgentID: "agent-1", Kind: model.PKIIdentityKindAgent,
					Purpose: model.PKICertificatePurposeClient,
				},
				Credential: model.PKITunnelCredential{
					CertificateID: "certificate-1", AuthorityID: "authority-1", CAGeneration: 2,
					NotBefore: now.Add(-71 * time.Hour), NotAfter: now.Add(time.Hour),
				},
			}},
		},
		acknowledgementErr: modulepki.ErrCredentialInvalid,
	}
	handler := newRemotePKIHeartbeatHandler(store, "agent-1")
	handler.now = func() time.Time { return now }
	state, err := handler.PrepareHeartbeat(t.Context())
	if err != nil {
		t.Fatalf("PrepareHeartbeat() error = %v", err)
	}
	if len(store.preparedSpecs) != 1 {
		t.Fatalf("prepared renewal specs = %+v", store.preparedSpecs)
	}
	spec := store.preparedSpecs[0]
	if spec.StorageIdentity != "agent" || spec.DomainID != "domain-1" || spec.AgentID != "agent-1" ||
		spec.Kind != model.PKIIdentityKindAgent || spec.Purpose != model.PKICertificatePurposeClient {
		t.Fatalf("renewal spec = %+v", spec)
	}
	if len(state.EnrollmentRequests) != 1 || state.EnrollmentRequests[0].RequestID != "renewal-1" {
		t.Fatalf("renewal heartbeat state = %+v", state.EnrollmentRequests)
	}
}

func TestRemotePKIHeartbeatReplaysMissingCredentialRenewalAfterRestart(t *testing.T) {
	store := &fakeRemotePKIStore{
		security: modulepki.SecurityState{Snapshot: model.PKISecuritySnapshot{
			PKIDomainID: "domain-1",
			TrustRoots:  []model.PKITrustRoot{{AuthorityID: "authority-1", Generation: 2, Status: "active"}},
		}},
		acknowledgementErr: modulepki.ErrActiveCredential,
	}
	first := newRemotePKIHeartbeatHandler(store, "agent-1")
	firstState, err := first.PrepareHeartbeat(t.Context())
	if err != nil {
		t.Fatalf("first PrepareHeartbeat() error = %v", err)
	}
	if len(store.preparedSpecs) != 1 || len(firstState.EnrollmentRequests) != 1 {
		t.Fatalf("first durable renewal = specs %+v, state %+v", store.preparedSpecs, firstState.EnrollmentRequests)
	}

	restarted := newRemotePKIHeartbeatHandler(store, "agent-1")
	replayed, err := restarted.PrepareHeartbeat(t.Context())
	if err != nil {
		t.Fatalf("restarted PrepareHeartbeat() error = %v", err)
	}
	if len(store.preparedSpecs) != 1 {
		t.Fatalf("restart generated a second renewal: %+v", store.preparedSpecs)
	}
	if len(replayed.EnrollmentRequests) != 1 || replayed.EnrollmentRequests[0].RequestID != firstState.EnrollmentRequests[0].RequestID {
		t.Fatalf("restarted renewal = %+v, want %+v", replayed.EnrollmentRequests, firstState.EnrollmentRequests)
	}
}

func TestAgentCredentialNeedsRenewalPolicy(t *testing.T) {
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	credential := model.PKITunnelCredential{
		CertificateID: "certificate-1", AuthorityID: "authority-1", CAGeneration: 2,
		NotBefore: now.Add(-60 * 24 * time.Hour), NotAfter: now.Add(31 * 24 * time.Hour),
	}
	activeRoot := model.PKITrustRoot{AuthorityID: "authority-1", Generation: 2, Status: "active"}
	tests := []struct {
		name       string
		credential model.PKITunnelCredential
		roots      []model.PKITrustRoot
		want       bool
	}{
		{name: "healthy", credential: credential, roots: []model.PKITrustRoot{activeRoot}},
		{name: "one third remaining", credential: func() model.PKITunnelCredential {
			candidate := credential
			candidate.NotBefore = now.Add(-60 * 24 * time.Hour)
			candidate.NotAfter = now.Add(30 * 24 * time.Hour)
			return candidate
		}(), roots: []model.PKITrustRoot{activeRoot}, want: true},
		{name: "expired", credential: func() model.PKITunnelCredential {
			candidate := credential
			candidate.NotAfter = now
			return candidate
		}(), roots: []model.PKITrustRoot{activeRoot}, want: true},
		{name: "not yet valid", credential: func() model.PKITunnelCredential {
			candidate := credential
			candidate.NotBefore = now.Add(time.Hour)
			return candidate
		}(), roots: []model.PKITrustRoot{activeRoot}, want: true},
		{name: "signer retiring", credential: credential, roots: []model.PKITrustRoot{{AuthorityID: "authority-1", Generation: 2, Status: "retiring"}}, want: true},
		{name: "signer missing", credential: credential, roots: []model.PKITrustRoot{{AuthorityID: "authority-2", Generation: 3, Status: "active"}}, want: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			active := modulepki.CredentialMetadata{Manifest: modulepki.CredentialManifest{Credential: test.credential}}
			security := modulepki.SecurityState{Snapshot: model.PKISecuritySnapshot{TrustRoots: test.roots}}
			if got := agentCredentialNeedsRenewal(active, security, now); got != test.want {
				t.Fatalf("agentCredentialNeedsRenewal() = %t, want %t", got, test.want)
			}
		})
	}
}

func TestRemotePKIHeartbeatIsolatesTerminalRejectionFromSuccessfulCredential(t *testing.T) {
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	agentPending := testPendingEnrollment("agent", "request-agent", model.PKIIdentityKindAgent)
	agentPending.DomainID = "domain-1"
	agentPending.AgentID = "agent-1"
	listenerPending := testPendingEnrollment("listener-1", "request-listener", model.PKIIdentityKindListener)
	listenerPending.DomainID = "domain-1"
	listenerPending.AgentID = "agent-1"
	listenerPending.Request.ListenerID = "listener-1"
	listenerPending.Request.Purpose = model.PKICertificatePurposeServer
	store := &fakeRemotePKIStore{
		pending:            []modulepki.PendingEnrollment{agentPending, listenerPending},
		acknowledgementErr: modulepki.ErrActiveCredential,
	}
	handler := newRemotePKIHeartbeatHandler(store, "agent-1")
	handler.now = func() time.Time { return now }
	if _, err := handler.PrepareHeartbeat(t.Context()); err != nil {
		t.Fatalf("PrepareHeartbeat() error = %v", err)
	}
	security := model.PKISecuritySnapshot{
		PKIDomainID: "domain-1", PKIEpoch: 2, SecurityRevision: 8, Full: true,
		TrustRoots: []model.PKITrustRoot{{AuthorityID: "authority-1", Generation: 2, Status: "active"}},
	}
	err := handler.ApplyHeartbeat(t.Context(), control.PKIHeartbeatReply{
		Security: &security,
		Credentials: []model.PKIControlCredential{
			{RequestID: "request-agent", Error: "invalid_csr"},
			{RequestID: "request-listener", Credential: model.PKITunnelCredential{
				CertificateID: "listener-certificate", AuthorityID: "authority-1", CAGeneration: 2,
			}},
		},
		Status: &model.PKIControlStatus{Status: "ready"},
	})
	if err != nil {
		t.Fatalf("ApplyHeartbeat() error = %v", err)
	}
	if len(store.credentialRequests) != 1 || store.credentialRequests[0].RequestID != "request-listener" {
		t.Fatalf("successful activations = %+v", store.credentialRequests)
	}
	if !reflect.DeepEqual(store.rejected, []fakeRejectedEnrollment{{storageIdentity: "agent", requestID: "request-agent", code: "invalid_csr"}}) {
		t.Fatalf("durable rejections = %+v", store.rejected)
	}
	if len(store.preparedSpecs) != 0 {
		t.Fatalf("terminal rejection immediately created a new key: %+v", store.preparedSpecs)
	}
	state, err := handler.PrepareHeartbeat(t.Context())
	if err != nil {
		t.Fatalf("subsequent PrepareHeartbeat() error = %v", err)
	}
	if len(state.EnrollmentRequests) != 0 {
		t.Fatalf("backoff heartbeat emitted enrollment requests = %+v", state.EnrollmentRequests)
	}
	renewal := store.renewal[remoteAgentPKIStorageIdentity]
	if renewal.FailureCount != 1 || !renewal.NextAttemptAt.Equal(now.Add(remoteRenewalBackoffBase)) {
		t.Fatalf("durable renewal backoff = %+v", renewal)
	}
	now = renewal.NextAttemptAt.Add(time.Second)
	state, err = handler.PrepareHeartbeat(t.Context())
	if err != nil {
		t.Fatalf("PrepareHeartbeat(after backoff) error = %v", err)
	}
	if len(store.preparedSpecs) != 1 || len(state.EnrollmentRequests) != 1 || state.EnrollmentRequests[0].RequestID != "renewal-1" {
		t.Fatalf("post-backoff renewal = specs %+v, requests %+v", store.preparedSpecs, state.EnrollmentRequests)
	}
	if err := handler.ApplyHeartbeat(t.Context(), control.PKIHeartbeatReply{
		Credentials: []model.PKIControlCredential{{RequestID: "renewal-1", Error: "public_key_reuse"}},
		Status:      &model.PKIControlStatus{Status: "ready"},
	}); err != nil {
		t.Fatalf("ApplyHeartbeat(second rejection) error = %v", err)
	}
	renewal = store.renewal[remoteAgentPKIStorageIdentity]
	if renewal.FailureCount != 2 || !renewal.NextAttemptAt.Equal(now.Add(2*remoteRenewalBackoffBase)) {
		t.Fatalf("second durable renewal backoff = %+v", renewal)
	}
	if got := remoteRenewalBackoff(remoteRenewalFailureCountLimit); got != remoteRenewalBackoffMaximum {
		t.Fatalf("capped renewal backoff = %s, want %s", got, remoteRenewalBackoffMaximum)
	}
}

func TestRemotePKIHeartbeatKeepsOrdinarySyncAvailableWhenDegradedAndRejectsUnrequestedResponses(t *testing.T) {
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
	if err != nil {
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

func TestDegradedPKIHeartbeatStillReturnsFailClosedOrdinarySnapshot(t *testing.T) {
	store := &fakeRemotePKIStore{acknowledgementErr: modulepki.ErrActiveCredential}
	handler := newRemotePKIHeartbeatHandler(store, "agent-1")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Agent-Token"); got != "control-token" {
			t.Fatalf("X-Agent-Token = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"sync":{"has_update":true,"desired_revision":8,"relay_listeners":[],"pki_security":{"pki_domain_id":"domain-1","pki_epoch":2,"security_revision":8,"full":true},"pki_status":{"status":"degraded","code":"runtime_unavailable","recovery_hint":"retry ordinary control sync"}}}`)
	}))
	defer server.Close()

	client := control.NewSyncClient(control.SyncClientConfig{
		MasterURL: server.URL, AgentToken: "control-token", AgentID: "agent-1",
		PKIHeartbeatHandler: handler,
	}, server.Client())
	snapshot, err := client.Sync(t.Context(), control.SyncRequest{CurrentRevision: 7})
	if err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	if snapshot.Revision != 8 || snapshot.RelayListeners == nil || len(snapshot.RelayListeners) != 0 {
		t.Fatalf("degraded ordinary snapshot = %+v", snapshot)
	}
	if len(store.appliedSecurity) != 1 || store.appliedSecurity[0].SecurityRevision != 8 {
		t.Fatalf("applied security = %+v", store.appliedSecurity)
	}
}

func TestRemotePKIHeartbeatRequiresReenrollmentForAnotherAgentDataRoot(t *testing.T) {
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	store := &fakeRemotePKIStore{
		security: modulepki.SecurityState{Snapshot: model.PKISecuritySnapshot{
			PKIDomainID: "domain-1",
			TrustRoots:  []model.PKITrustRoot{{AuthorityID: "authority-1", Generation: 1, Status: "active"}},
		}},
		active: map[string]modulepki.CredentialMetadata{
			"agent": {Manifest: modulepki.CredentialManifest{
				PKIDomainID: "domain-1",
				Expectation: modulepki.CredentialExpectation{
					DomainID: "domain-1", AgentID: "agent-2", Kind: model.PKIIdentityKindAgent,
					Purpose: model.PKICertificatePurposeClient,
				},
				Credential: model.PKITunnelCredential{
					CertificateID: "certificate-2", AuthorityID: "authority-1", CAGeneration: 1,
					NotBefore: now.Add(-time.Hour), NotAfter: now.Add(24 * time.Hour),
				},
			}},
		},
		acknowledgement: model.PKISecurityAcknowledgement{
			PKIDomainID: "domain-1", CertificateID: "certificate-2",
		},
	}
	handler := newRemotePKIHeartbeatHandler(store, "agent-1")
	handler.now = func() time.Time { return now }
	state, err := handler.PrepareHeartbeat(t.Context())
	if err != nil {
		t.Fatalf("PrepareHeartbeat() error = %v", err)
	}
	if state.SecurityAcknowledgement != nil || len(store.preparedSpecs) != 0 {
		t.Fatalf("mismatched data root emitted state=%+v renewal=%+v", state, store.preparedSpecs)
	}
	renewal := store.renewal[remoteAgentPKIStorageIdentity]
	if !renewal.ReenrollmentRequired || renewal.Reason != "owner_mismatch" {
		t.Fatalf("mismatched data root recovery state = %+v", renewal)
	}
}

func TestRemotePKIRenewalJitterDiffersAcrossAgentsAndSurvivesRestart(t *testing.T) {
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	notBefore := now.Add(-24 * time.Hour)
	notAfter := notBefore.Add(90 * 24 * time.Hour)
	security := modulepki.SecurityState{Snapshot: model.PKISecuritySnapshot{
		PKIDomainID: "domain-1",
		TrustRoots:  []model.PKITrustRoot{{AuthorityID: "authority-1", Generation: 3, Status: "active"}},
	}}

	stores := make([]*fakeRemotePKIStore, 2)
	dueTimes := make([]time.Time, 2)
	for index, agentID := range []string{"agent-1", "agent-2"} {
		active := testAgentCredentialMetadata(agentID, "certificate-shared", notBefore, notAfter)
		store := &fakeRemotePKIStore{
			security: security,
			active:   map[string]modulepki.CredentialMetadata{remoteAgentPKIStorageIdentity: active},
		}
		handler := newRemotePKIHeartbeatHandler(store, agentID)
		handler.now = func() time.Time { return now }
		state, err := handler.PrepareHeartbeat(t.Context())
		if err != nil {
			t.Fatalf("PrepareHeartbeat(%s) error = %v", agentID, err)
		}
		if len(state.EnrollmentRequests) != 0 || len(store.preparedSpecs) != 0 {
			t.Fatalf("%s renewed before its due time: %+v", agentID, state.EnrollmentRequests)
		}
		stores[index] = store
		dueTimes[index] = store.renewal[remoteAgentPKIStorageIdentity].DueAt
	}
	if dueTimes[0].Equal(dueTimes[1]) {
		t.Fatalf("agent renewal jitter did not stagger due times: %v", dueTimes)
	}
	base := notAfter.Add(-notAfter.Sub(notBefore) / 3)
	halfWindow := notAfter.Sub(notBefore) / 24
	for index, due := range dueTimes {
		if due.Before(base.Add(-halfWindow)) || due.After(base.Add(halfWindow)) {
			t.Fatalf("agent %d due time %s is outside safe jitter window around %s", index+1, due, base)
		}
	}

	restarted := newRemotePKIHeartbeatHandler(stores[0], "agent-1")
	restarted.now = func() time.Time { return now.Add(time.Hour) }
	if _, err := restarted.PrepareHeartbeat(t.Context()); err != nil {
		t.Fatalf("restarted PrepareHeartbeat() error = %v", err)
	}
	if got := stores[0].renewal[remoteAgentPKIStorageIdentity].DueAt; !got.Equal(dueTimes[0]) {
		t.Fatalf("restart changed durable due time from %s to %s", dueTimes[0], got)
	}
}

func TestRemotePKILifecycleRenewalUsesPersistedStableJitter(t *testing.T) {
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	active := testAgentCredentialMetadata("agent-1", "certificate-1", now.Add(-24*time.Hour), now.Add(89*24*time.Hour))
	security := modulepki.SecurityState{Snapshot: model.PKISecuritySnapshot{
		PKIDomainID: "domain-1", PKIEpoch: 3, SecurityRevision: 4, Full: true, IssuedAt: now.Add(-time.Hour),
		TrustRoots: []model.PKITrustRoot{{AuthorityID: "authority-1", Generation: 3, Status: "active"}},
	}}
	store := &fakeRemotePKIStore{
		security: security,
		active:   map[string]modulepki.CredentialMetadata{remoteAgentPKIStorageIdentity: active},
	}
	handler := newRemotePKIHeartbeatHandler(store, "agent-1")
	handler.now = func() time.Time { return now }
	if _, err := handler.PrepareHeartbeat(t.Context()); err != nil {
		t.Fatalf("seed PrepareHeartbeat() error = %v", err)
	}
	originalDue := store.renewal[remoteAgentPKIStorageIdentity].DueAt

	retiring := security.Snapshot
	retiring.SecurityRevision++
	retiring.IssuedAt = now
	retiring.TrustRoots[0].Status = "retiring"
	if err := handler.ApplyHeartbeat(t.Context(), control.PKIHeartbeatReply{
		Security: &retiring,
		Status:   &model.PKIControlStatus{Status: "ready"},
	}); err != nil {
		t.Fatalf("ApplyHeartbeat(retiring signer) error = %v", err)
	}
	lifecycleDue := store.renewal[remoteAgentPKIStorageIdentity].DueAt
	if !lifecycleDue.Before(originalDue) || lifecycleDue.Before(now) || lifecycleDue.After(now.Add(remoteLifecycleJitterWindow)) {
		t.Fatalf("lifecycle due time = %s, original %s, window [%s,%s]", lifecycleDue, originalDue, now, now.Add(remoteLifecycleJitterWindow))
	}
	if len(store.preparedSpecs) != 0 {
		t.Fatalf("lifecycle transition renewed immediately: %+v", store.preparedSpecs)
	}

	restarted := newRemotePKIHeartbeatHandler(store, "agent-1")
	beforeDue := lifecycleDue.Add(-time.Nanosecond)
	restarted.now = func() time.Time { return beforeDue }
	if _, err := restarted.PrepareHeartbeat(t.Context()); err != nil {
		t.Fatalf("restarted PrepareHeartbeat(before lifecycle due) error = %v", err)
	}
	if got := store.renewal[remoteAgentPKIStorageIdentity].DueAt; !got.Equal(lifecycleDue) {
		t.Fatalf("restart changed lifecycle due time from %s to %s", lifecycleDue, got)
	}
	restarted.now = func() time.Time { return lifecycleDue }
	state, err := restarted.PrepareHeartbeat(t.Context())
	if err != nil {
		t.Fatalf("PrepareHeartbeat(at lifecycle due) error = %v", err)
	}
	if len(store.preparedSpecs) != 1 || len(state.EnrollmentRequests) != 1 {
		t.Fatalf("lifecycle renewal at due = specs %+v, requests %+v", store.preparedSpecs, state.EnrollmentRequests)
	}
}

func TestRemotePKIRevocationWaitsForStagedRegistration(t *testing.T) {
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	active := testAgentCredentialMetadata("agent-1", "certificate-old", now.Add(-24*time.Hour), now.Add(60*24*time.Hour))
	security := modulepki.SecurityState{Snapshot: model.PKISecuritySnapshot{
		PKIDomainID: "domain-1", PKIEpoch: 3, SecurityRevision: 4, Full: true,
		TrustRoots: []model.PKITrustRoot{{AuthorityID: "authority-1", Generation: 3, Status: "active"}},
	}}
	store := &fakeRemotePKIStore{
		security: security,
		active:   map[string]modulepki.CredentialMetadata{remoteAgentPKIStorageIdentity: active},
		acknowledgement: model.PKISecurityAcknowledgement{
			PKIDomainID: "domain-1", CertificateID: "certificate-old",
		},
	}
	handler := newRemotePKIHeartbeatHandler(store, "agent-1")
	handler.now = func() time.Time { return now }
	if _, err := handler.PrepareHeartbeat(t.Context()); err != nil {
		t.Fatalf("seed PrepareHeartbeat() error = %v", err)
	}

	revoked := security.Snapshot
	revoked.SecurityRevision++
	revoked.RevokedIdentityIDs = []string{"identity-agent-1"}
	store.activeErr = &modulepki.CredentialInvalidError{
		Reason: modulepki.CredentialInvalidRevokedIdentity,
		Detail: "identity is revoked",
	}
	if err := handler.ApplyHeartbeat(t.Context(), control.PKIHeartbeatReply{
		Security: &revoked,
		Status:   &model.PKIControlStatus{Status: "ready"},
	}); err != nil {
		t.Fatalf("ApplyHeartbeat(revoked) error = %v", err)
	}
	for heartbeat := 0; heartbeat < 3; heartbeat++ {
		state, err := handler.PrepareHeartbeat(t.Context())
		if err != nil {
			t.Fatalf("PrepareHeartbeat(revoked %d) error = %v", heartbeat, err)
		}
		if len(state.EnrollmentRequests) != 0 || state.SecurityAcknowledgement != nil || len(store.preparedSpecs) != 0 {
			t.Fatalf("revoked heartbeat %d emitted authenticated PKI state: %+v", heartbeat, state)
		}
	}
	renewal := store.renewal[remoteAgentPKIStorageIdentity]
	if !renewal.ReenrollmentRequired || renewal.Reason != string(modulepki.CredentialInvalidRevokedIdentity) {
		t.Fatalf("revocation recovery state = %+v", renewal)
	}

	pending := testPendingEnrollment(remoteAgentPKIStorageIdentity, "registration-recovery", model.PKIIdentityKindAgent)
	pending.AgentID = "agent-1"
	pending.DomainID = "domain-1"
	newCredential := testAgentCredentialMetadata("agent-1", "certificate-new", now.Add(-time.Hour), now.Add(90*24*time.Hour)).Manifest.Credential
	newSecurity := revoked
	newSecurity.PKIEpoch++
	newSecurity.SecurityRevision = 0
	newSecurity.RevokedIdentityIDs = nil
	store.pending = append(store.pending, pending)
	store.staged = map[string]modulepki.StagedRegistration{
		remoteAgentPKIStorageIdentity: {
			AgentID: "agent-1", TunnelCredential: newCredential, SecuritySnapshot: newSecurity,
		},
	}
	store.stagedPending = map[string]modulepki.PendingEnrollment{remoteAgentPKIStorageIdentity: pending}
	store.acknowledgement = model.PKISecurityAcknowledgement{PKIDomainID: "domain-1", CertificateID: "certificate-new"}
	state, err := handler.PrepareHeartbeat(t.Context())
	if err != nil {
		t.Fatalf("PrepareHeartbeat(staged recovery) error = %v", err)
	}
	if len(store.stagedActivations) != 1 || len(state.EnrollmentRequests) != 0 {
		t.Fatalf("staged revocation recovery = activations %+v, state %+v", store.stagedActivations, state)
	}
	renewal = store.renewal[remoteAgentPKIStorageIdentity]
	if renewal.ReenrollmentRequired || renewal.Reason != "" || renewal.FailureCount != 0 || !renewal.NextAttemptAt.IsZero() {
		t.Fatalf("staged registration did not reset recovery state: %+v", renewal)
	}
}

func testAgentCredentialMetadata(agentID, certificateID string, notBefore, notAfter time.Time) modulepki.CredentialMetadata {
	credential := model.PKITunnelCredential{
		IdentityID: "identity-" + agentID, CertificateID: certificateID,
		PublicKeyFingerprint: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		AuthorityID:          "authority-1", CAGeneration: 3,
		Purpose: model.PKICertificatePurposeClient, NotBefore: notBefore, NotAfter: notAfter,
	}
	return modulepki.CredentialMetadata{
		Manifest: modulepki.CredentialManifest{
			Credential: credential, PKIDomainID: "domain-1",
			Expectation: modulepki.CredentialExpectation{
				DomainID: "domain-1", AgentID: agentID, Kind: model.PKIIdentityKindAgent,
				Purpose: model.PKICertificatePurposeClient,
			},
		},
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
