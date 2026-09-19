//go:build !integration

package app

import (
	"context"
	"errors"
	"fmt"

	"reflect"
	"slices"
	"strings"
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
	rejectAfterCommit  bool
	renewal            map[string]modulepki.RenewalState
	renewalErr         error
	saveRenewalErrors  []error
	savedRenewal       []modulepki.RenewalState
	events             []string
}

type fakeRejectedEnrollment struct {
	storageIdentity string
	requestID       string
	code            string
}

type fakeAppRevisionSyncClient struct {
	pull model.RevisionPull
}

func (c *fakeAppRevisionSyncClient) PullRevision(context.Context) (model.RevisionPull, error) {
	return c.pull, nil
}

func (c *fakeAppRevisionSyncClient) StartRevision(context.Context, model.RevisionStart) error {
	return nil
}

func (c *fakeAppRevisionSyncClient) ReportRevision(context.Context, model.RevisionReport) error {
	return nil
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

func (s *fakeRemotePKIStore) SecurityAcknowledgement(storageIdentity string) (model.PKISecurityAcknowledgement, error) {
	if s.acknowledgementErr != nil {
		return model.PKISecurityAcknowledgement{}, s.acknowledgementErr
	}
	active, err := s.LoadActiveCredential(storageIdentity)
	if err != nil {
		return model.PKISecurityAcknowledgement{}, err
	}
	security, err := s.LoadSecuritySnapshot()
	if err != nil {
		return model.PKISecurityAcknowledgement{}, err
	}
	certificateID := active.Manifest.Credential.CertificateID
	if s.acknowledgement.CertificateID != "" && s.acknowledgement.CertificateID != certificateID {
		return model.PKISecurityAcknowledgement{}, modulepki.ErrCredentialInvalid
	}
	trustGenerations := make([]int64, 0, len(security.Snapshot.TrustRoots))
	for _, root := range security.Snapshot.TrustRoots {
		trustGenerations = append(trustGenerations, root.Generation)
	}
	slices.Sort(trustGenerations)
	return model.PKISecurityAcknowledgement{
		PKIDomainID: security.Snapshot.PKIDomainID, PKIEpoch: security.Snapshot.PKIEpoch,
		SecurityRevision: security.Snapshot.SecurityRevision, Full: security.Snapshot.Full,
		CertificateID: certificateID, TrustGenerations: trustGenerations,
	}, nil
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
	s.events = append(s.events, "reject:"+requestID+":"+code)
	if s.rejectErr != nil && !s.rejectAfterCommit {
		return s.rejectErr
	}
	for _, rejected := range s.rejected {
		if rejected.storageIdentity == storageIdentity && rejected.requestID == requestID {
			if rejected.code == code {
				return nil
			}
			return modulepki.ErrPendingConflict
		}
	}
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
	s.rejected = append(s.rejected, fakeRejectedEnrollment{storageIdentity: storageIdentity, requestID: requestID, code: code})
	return s.rejectErr
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
	s.events = append(s.events, "save:"+state.PendingRejectionRequestID+":"+state.PendingRejectionCode)
	if s.renewalErr != nil {
		return modulepki.RenewalState{}, s.renewalErr
	}
	if len(s.saveRenewalErrors) != 0 {
		err := s.saveRenewalErrors[0]
		s.saveRenewalErrors = s.saveRenewalErrors[1:]
		if err != nil {
			return modulepki.RenewalState{}, err
		}
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

func TestRemotePKIHeartbeatAutomaticallyEnrollsProjectedRelayListener(t *testing.T) {
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	store := &fakeRemotePKIStore{
		active: map[string]modulepki.CredentialMetadata{
			remoteAgentPKIStorageIdentity: testAgentCredentialMetadata("agent-1", "agent-certificate", now.Add(-time.Hour), now.Add(90*24*time.Hour)),
		},
		security: modulepki.SecurityState{Snapshot: model.PKISecuritySnapshot{
			PKIDomainID: "domain-1", PKIEpoch: 1, SecurityRevision: 4, Full: true, IssuedAt: now,
			TrustRoots: []model.PKITrustRoot{{AuthorityID: "authority-1", Generation: 3, Status: "active"}},
		}},
	}
	handler := newRemotePKIHeartbeatHandler(store, "agent-1")
	handler.now = func() time.Time { return now }
	handler.observeRelayListeners([]model.RelayListener{{
		ID: 71, AgentID: "agent-1", ListenHost: "0.0.0.0", BindHosts: []string{"0.0.0.0", "192.0.2.71"},
		PublicHost: "Relay.Example.Test", PKIIdentityID: "listener-identity-71", PKIIdentityState: "enrollment_required",
	}})

	heartbeat, err := handler.PrepareHeartbeat(t.Context())
	if err != nil {
		t.Fatalf("PrepareHeartbeat() error = %v", err)
	}
	var request *model.PKIEnrollmentRequest
	for index := range heartbeat.EnrollmentRequests {
		if heartbeat.EnrollmentRequests[index].Kind == model.PKIIdentityKindListener {
			request = &heartbeat.EnrollmentRequests[index]
		}
	}
	if request == nil || request.ListenerID != "71" || request.Purpose != model.PKICertificatePurposeServer ||
		!reflect.DeepEqual(request.DNSNames, []string{"relay.example.test"}) || !reflect.DeepEqual(request.IPAddresses, []string{"192.0.2.71"}) {
		t.Fatalf("automatic listener enrollment request = %+v", request)
	}
	if len(store.preparedSpecs) != 1 || store.preparedSpecs[0].StorageIdentity != "listener-71" {
		t.Fatalf("prepared listener specs = %+v", store.preparedSpecs)
	}
}

func TestRelaySecuritySyncPrefetchesNewListenerCredentialBeforeRevisionPull(t *testing.T) {
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	security := model.PKISecuritySnapshot{
		PKIDomainID: "domain-1", PKIEpoch: 1, SecurityRevision: 4, Full: true, IssuedAt: now,
		TrustRoots: []model.PKITrustRoot{{AuthorityID: "authority-1", Generation: 3, Status: "active"}},
	}
	store := &fakeRemotePKIStore{
		active: map[string]modulepki.CredentialMetadata{
			remoteAgentPKIStorageIdentity: testAgentCredentialMetadata("agent-1", "agent-certificate", now.Add(-time.Hour), now.Add(90*24*time.Hour)),
		},
		security: modulepki.SecurityState{Snapshot: security},
	}
	handler := newRemotePKIHeartbeatHandler(store, "agent-1")
	handler.now = func() time.Time { return now }
	listener := model.RelayListener{
		ID: 71, AgentID: "agent-1", ListenHost: "0.0.0.0", BindHosts: []string{"0.0.0.0", "192.0.2.71"},
		PublicHost: "relay.example.test", Enabled: true, TLSMode: "pki_mtls",
		PKIIdentityID: "listener-identity-71", PKIIdentityState: "enrollment_required",
	}
	calls := 0
	delegate := syncClientFunc(func(ctx context.Context, _ SyncRequest) (Snapshot, error) {
		calls++
		heartbeat, err := handler.PrepareHeartbeat(ctx)
		if err != nil {
			return Snapshot{}, err
		}
		reply := control.PKIHeartbeatReply{Security: &security}
		for _, request := range heartbeat.EnrollmentRequests {
			if request.Kind != model.PKIIdentityKindListener {
				continue
			}
			reply.Credentials = append(reply.Credentials, model.PKIControlCredential{
				RequestID: request.RequestID,
				Credential: model.PKITunnelCredential{
					IdentityID: listener.PKIIdentityID, CertificateID: "listener-certificate-71",
					AuthorityID: "authority-1", CAGeneration: 3, Purpose: model.PKICertificatePurposeServer,
					NotBefore: now.Add(-time.Hour), NotAfter: now.Add(90 * 24 * time.Hour),
				},
			})
		}
		if err := handler.ApplyHeartbeat(ctx, reply); err != nil {
			return Snapshot{}, err
		}
		if calls < 3 {
			return Snapshot{RelayListeners: []model.RelayListener{}}, nil
		}
		visible := listener
		visible.PKIIdentityState = "active"
		return Snapshot{RelayListeners: []model.RelayListener{visible}}, nil
	})
	base := &relaySecuritySyncClient{delegate: delegate, pki: handler}
	revisionSnapshot := model.Snapshot{RelayListeners: []model.RelayListener{listener}}
	client := &relaySecurityRevisionSyncClient{
		relaySecuritySyncClient: base,
		revision:                &fakeAppRevisionSyncClient{pull: model.RevisionPull{HasUpdate: true, Snapshot: &revisionSnapshot}},
	}

	if _, err := client.Sync(t.Context(), SyncRequest{}); err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	if calls != 1 {
		t.Fatalf("fail-closed heartbeat calls before revision pull = %d, want 1", calls)
	}
	pull, err := client.PullRevision(t.Context())
	if err != nil || pull.HasUpdate {
		t.Fatalf("PullRevision(fail closed) = %+v, error = %v", pull, err)
	}
	if calls != 2 || len(store.preparedSpecs) != 1 || len(store.credentialRequests) != 1 {
		t.Fatalf("listener prefetch calls=%d specs=%+v activations=%+v", calls, store.preparedSpecs, store.credentialRequests)
	}
	active, err := store.LoadActiveCredential("listener-71")
	if err != nil || active.Manifest.Credential.IdentityID != listener.PKIIdentityID {
		t.Fatalf("prefetched listener credential = %+v, error = %v", active, err)
	}
	if _, err := client.Sync(t.Context(), SyncRequest{}); err != nil {
		t.Fatalf("Sync(restored heartbeat) error = %v", err)
	}
	pull, err = client.PullRevision(t.Context())
	if err != nil || !pull.HasUpdate || pull.Snapshot == nil {
		t.Fatalf("PullRevision(restored) = %+v, error = %v", pull, err)
	}
	if calls != 3 {
		t.Fatalf("restored heartbeat calls = %d, want 3", calls)
	}
}

func TestRemotePKIHeartbeatRenewsAndForceRotatesProjectedRelayListener(t *testing.T) {
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	listener := model.RelayListener{
		ID: 71, AgentID: "agent-1", ListenHost: "127.0.0.1", BindHosts: []string{"127.0.0.1"},
		PublicHost: "relay.example.test", PKIIdentityID: "listener-identity-71", PKIIdentityState: "active",
	}
	store := &fakeRemotePKIStore{
		active: map[string]modulepki.CredentialMetadata{
			remoteAgentPKIStorageIdentity: testAgentCredentialMetadata("agent-1", "agent-certificate", now.Add(-time.Hour), now.Add(90*24*time.Hour)),
			"listener-71":                 remoteListenerCredentialMetadata(listener, "listener-certificate", now.Add(-80*24*time.Hour), now.Add(10*24*time.Hour)),
		},
		security: modulepki.SecurityState{Snapshot: model.PKISecuritySnapshot{
			PKIDomainID: "domain-1", PKIEpoch: 1, SecurityRevision: 4, Full: true, IssuedAt: now,
			TrustRoots: []model.PKITrustRoot{{AuthorityID: "authority-1", Generation: 3, Status: "active"}},
		}},
	}
	handler := newRemotePKIHeartbeatHandler(store, "agent-1")
	handler.now = func() time.Time { return now }
	handler.observeRelayListeners([]model.RelayListener{listener})
	heartbeat, err := handler.PrepareHeartbeat(t.Context())
	if err != nil {
		t.Fatalf("PrepareHeartbeat(renewal) error = %v", err)
	}
	if len(heartbeat.EnrollmentRequests) != 1 || heartbeat.EnrollmentRequests[0].ListenerID != "71" {
		t.Fatalf("listener renewal requests = %+v", heartbeat.EnrollmentRequests)
	}

	store.pending = nil
	store.preparedSpecs = nil
	store.renewal = nil
	store.active["listener-71"] = remoteListenerCredentialMetadata(listener, "listener-certificate-fresh", now.Add(-time.Hour), now.Add(90*24*time.Hour))
	handler.inflight = make(map[string]modulepki.PendingEnrollment)
	taskHandler := newRemoteAgentTaskHandler(nil, handler)
	result, err := taskHandler.HandleTask(t.Context(), control.TaskMessage{
		TaskType: control.TaskTypePKIForceRotation,
		RawPayload: map[string]any{
			"identity_id": "listener-identity-71", "identity_kind": model.PKIIdentityKindListener, "listener_id": "71",
		},
	})
	if err != nil || result["identity_id"] != "listener-identity-71" || len(store.preparedSpecs) != 1 ||
		store.preparedSpecs[0].StorageIdentity != "listener-71" {
		t.Fatalf("forced listener rotation result=%+v specs=%+v error=%v", result, store.preparedSpecs, err)
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
		Credentials: []model.PKIControlCredential{{
			RequestID: "request-agent", Credential: model.PKITunnelCredential{CertificateID: "certificate-degraded"},
		}},
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
	replayed, err := handler.PrepareHeartbeat(t.Context())
	if err != nil || len(replayed.EnrollmentRequests) != 1 || replayed.EnrollmentRequests[0].RequestID != "request-agent" {
		t.Fatalf("degraded heartbeat did not preserve replayable enrollment: state=%+v error=%v", replayed, err)
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

func TestRemotePKIHeartbeatRepairsHealthyActivationAfterRenewalStateFailure(t *testing.T) {
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	injected := errors.New("injected renewal state failure")
	oldActive := testAgentCredentialMetadata("agent-1", "certificate-old", now.Add(-24*time.Hour), now.Add(30*24*time.Hour))
	oldState := renewalStateForCredential(oldActive, "agent-1")
	oldState.ReenrollmentRequired = true
	oldState.Reason = "owner_mismatch"
	pending := testPendingEnrollment(remoteAgentPKIStorageIdentity, "registration-recovery", model.PKIIdentityKindAgent)
	pending.AgentID = "agent-1"
	pending.DomainID = "domain-1"
	newActive := testAgentCredentialMetadata("agent-1", "certificate-new", now.Add(-time.Hour), now.Add(90*24*time.Hour))
	newSecurity := model.PKISecuritySnapshot{
		PKIDomainID: "domain-1", PKIEpoch: 4, SecurityRevision: 0, Full: true,
		TrustRoots: []model.PKITrustRoot{{AuthorityID: "authority-1", Generation: 3, Status: "active"}},
	}
	store := &fakeRemotePKIStore{
		pending: []modulepki.PendingEnrollment{pending},
		staged: map[string]modulepki.StagedRegistration{
			remoteAgentPKIStorageIdentity: {
				AgentID: "agent-1", TunnelCredential: newActive.Manifest.Credential, SecuritySnapshot: newSecurity,
			},
		},
		stagedPending:     map[string]modulepki.PendingEnrollment{remoteAgentPKIStorageIdentity: pending},
		renewal:           map[string]modulepki.RenewalState{remoteAgentPKIStorageIdentity: oldState},
		saveRenewalErrors: []error{injected},
		acknowledgement: model.PKISecurityAcknowledgement{
			PKIDomainID: "domain-1", CertificateID: "certificate-new",
		},
	}
	first := newRemotePKIHeartbeatHandler(store, "agent-1")
	first.now = func() time.Time { return now }
	if _, err := first.PrepareHeartbeat(t.Context()); !errors.Is(err, injected) {
		t.Fatalf("PrepareHeartbeat() error = %v, want injected state failure", err)
	}
	if len(store.pending) != 0 || store.active[remoteAgentPKIStorageIdentity].Manifest.Credential.CertificateID != "certificate-new" {
		t.Fatalf("staged activation did not commit before injected failure: pending=%+v active=%+v", store.pending, store.active)
	}

	restarted := newRemotePKIHeartbeatHandler(store, "agent-1")
	restarted.now = func() time.Time { return now }
	state, err := restarted.PrepareHeartbeat(t.Context())
	if err != nil {
		t.Fatalf("restarted PrepareHeartbeat() error = %v", err)
	}
	if state.SecurityAcknowledgement == nil || state.SecurityAcknowledgement.CertificateID != "certificate-new" || len(state.EnrollmentRequests) != 0 {
		t.Fatalf("healthy activation recovery heartbeat = %+v", state)
	}
	resolved := store.renewal[remoteAgentPKIStorageIdentity]
	if resolved.ReenrollmentRequired || resolved.Reason != "" || resolved.CredentialFingerprint != renewalStateForCredential(store.active[remoteAgentPKIStorageIdentity], "agent-1").CredentialFingerprint {
		t.Fatalf("healthy activation renewal state = %+v", resolved)
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
	pending.PublicKeyFingerprint = strings.Repeat("f", 64)
	newCredential := testAgentCredentialMetadata("agent-1", "certificate-new", now.Add(-time.Hour), now.Add(90*24*time.Hour)).Manifest.Credential
	newCredential.PublicKeyFingerprint = pending.PublicKeyFingerprint
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

func remoteListenerCredentialMetadata(listener model.RelayListener, certificateID string, notBefore, notAfter time.Time) modulepki.CredentialMetadata {
	dnsNames, ipAddresses, err := canonicalRemoteListenerSANs(listener)
	if err != nil {
		panic(err)
	}
	return modulepki.CredentialMetadata{Manifest: modulepki.CredentialManifest{
		Credential: model.PKITunnelCredential{
			IdentityID: listener.PKIIdentityID, CertificateID: certificateID,
			PublicKeyFingerprint: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			AuthorityID:          "authority-1", CAGeneration: 3, Purpose: model.PKICertificatePurposeServer,
			NotBefore: notBefore, NotAfter: notAfter,
		},
		PKIDomainID: "domain-1",
		Expectation: modulepki.CredentialExpectation{
			DomainID: "domain-1", AgentID: listener.AgentID, Kind: model.PKIIdentityKindListener,
			ListenerID: fmt.Sprint(listener.ID), Purpose: model.PKICertificatePurposeServer,
			DNSNames: dnsNames, IPAddresses: ipAddresses,
		},
	}}
}

func testPendingEnrollment(storageIdentity, requestID, kind string) modulepki.PendingEnrollment {
	return modulepki.PendingEnrollment{
		StorageIdentity: storageIdentity,
		Request: model.PKIEnrollmentRequest{
			RequestID: requestID, Kind: kind, Purpose: model.PKICertificatePurposeClient, CSRPEM: "PUBLIC CSR",
		},
	}
}
