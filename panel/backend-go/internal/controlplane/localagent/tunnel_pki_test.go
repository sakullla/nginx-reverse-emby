package localagent

import (
	"context"
	"errors"
	"testing"
	"time"

	goagentembedded "github.com/sakullla/nginx-reverse-emby/go-agent/embedded"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/service"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

type tunnelCredentialStoreStub struct {
	ack                goagentembedded.PKISecurityAcknowledgement
	ackErr             error
	applyErr           error
	active             goagentembedded.PKICredentialMetadata
	activeErr          error
	pending            goagentembedded.PKIPendingEnrollment
	prepareErr         error
	activated          bool
	activationRequest  goagentembedded.PKIActivateRequest
	activationErr      error
	rejectedRequestID  string
	rejectedReason     string
	rejectErr          error
	appliedSnapshots   []goagentembedded.PKISecuritySnapshot
	prepareEnrollments []goagentembedded.PKIEnrollmentSpec
}

func (s *tunnelCredentialStoreStub) PrepareEnrollment(_ context.Context, spec goagentembedded.PKIEnrollmentSpec) (goagentembedded.PKIPendingEnrollment, error) {
	s.prepareEnrollments = append(s.prepareEnrollments, spec)
	return s.pending, s.prepareErr
}

func (s *tunnelCredentialStoreStub) RejectPendingEnrollment(_ string, requestID, reason string) error {
	s.rejectedRequestID = requestID
	s.rejectedReason = reason
	return s.rejectErr
}

func (s *tunnelCredentialStoreStub) ActivateRegistrationCredential(_ context.Context, request goagentembedded.PKIActivateRequest) (goagentembedded.PKICredentialMetadata, error) {
	s.activationRequest = request
	if s.activationErr == nil {
		s.activated = true
	}
	return s.active, s.activationErr
}

func (s *tunnelCredentialStoreStub) ActivateCredential(_ context.Context, request goagentembedded.PKIActivateRequest) (goagentembedded.PKICredentialMetadata, error) {
	s.activationRequest = request
	if s.activationErr == nil {
		s.activated = true
	}
	return s.active, s.activationErr
}

func (s *tunnelCredentialStoreStub) LoadActiveCredential(string) (goagentembedded.PKICredentialMetadata, error) {
	return s.active, s.activeErr
}

func (s *tunnelCredentialStoreStub) ApplySecuritySnapshot(snapshot goagentembedded.PKISecuritySnapshot) (goagentembedded.PKISecurityState, error) {
	s.appliedSnapshots = append(s.appliedSnapshots, snapshot)
	return goagentembedded.PKISecurityState{Snapshot: snapshot}, s.applyErr
}

func (s *tunnelCredentialStoreStub) SecurityAcknowledgement(string) (goagentembedded.PKISecurityAcknowledgement, error) {
	if s.ackErr != nil && !s.activated {
		return goagentembedded.PKISecurityAcknowledgement{}, s.ackErr
	}
	return s.ack, nil
}

type tunnelPKIServiceStub struct {
	snapshot          storage.PKISecuritySnapshot
	enrollment        service.PKILocalEnrollmentReply
	enrollmentErr     error
	preparedListeners []storage.RelayListener
	prepareErr        error
	requests          []service.PKILocalEnrollRequest
	acks              []*storage.PKISecurityAcknowledgement
}

func (s *tunnelPKIServiceStub) SecuritySnapshot(_ context.Context, _ string, acknowledgement *storage.PKISecurityAcknowledgement) (storage.PKISecuritySnapshot, error) {
	if acknowledgement == nil {
		s.acks = append(s.acks, nil)
	} else {
		copyValue := *acknowledgement
		copyValue.TrustGenerations = append([]int64(nil), acknowledgement.TrustGenerations...)
		s.acks = append(s.acks, &copyValue)
	}
	return s.snapshot, nil
}

func (s *tunnelPKIServiceStub) EnrollLocal(_ context.Context, request service.PKILocalEnrollRequest) (service.PKILocalEnrollmentReply, error) {
	s.requests = append(s.requests, request)
	return s.enrollment, s.enrollmentErr
}

func (s *tunnelPKIServiceStub) PrepareRelayListeners(_ context.Context, _ string, listeners []storage.RelayListener) ([]storage.RelayListener, error) {
	if s.prepareErr != nil {
		return nil, s.prepareErr
	}
	if s.preparedListeners != nil {
		return append([]storage.RelayListener(nil), s.preparedListeners...), nil
	}
	return append([]storage.RelayListener(nil), listeners...), nil
}

func TestEmbeddedSyncSourceProjectsPKIAndFailsRelayClosed(t *testing.T) {
	store := &bridgeStoreStub{snapshot: Snapshot{RelayListeners: []storage.RelayListener{{
		ID: 1, AgentID: "local-agent", Enabled: true, TLSMode: "pki_mtls",
	}}}}
	source := NewSyncSource(store, "local-agent")
	source.SetTunnelPKI(&tunnelPKIServiceStub{preparedListeners: []storage.RelayListener{{
		ID: 1, AgentID: "local-agent", Enabled: true, TLSMode: "pki_mtls",
		PKIIdentityID: "listener-identity", PKIIdentityState: storage.PKIIdentityStateActive,
	}}})
	snapshot, err := source.Sync(t.Context(), SyncRequest{})
	if err != nil {
		t.Fatalf("Sync(projected) error = %v", err)
	}
	if len(snapshot.RelayListeners) != 1 || snapshot.RelayListeners[0].PKIIdentityID != "listener-identity" {
		t.Fatalf("projected relay listeners = %+v", snapshot.RelayListeners)
	}

	source.SetTunnelPKI(&tunnelPKIServiceStub{prepareErr: service.ErrPKIRuntimeUnavailable})
	snapshot, err = source.Sync(t.Context(), SyncRequest{})
	if err != nil {
		t.Fatalf("Sync(degraded) error = %v", err)
	}
	if len(snapshot.RelayListeners) != 0 {
		t.Fatalf("degraded relay listeners = %+v, want fail-closed empty", snapshot.RelayListeners)
	}
}

func TestReconcileTunnelPKIEnrollsAndAcknowledgesEmbeddedIdentity(t *testing.T) {
	now := time.Date(2026, 8, 2, 8, 0, 0, 0, time.UTC)
	snapshot := tunnelPKITestSnapshot(now)
	pending := tunnelPKITestPending()
	credential := tunnelPKITestCredential(now)
	store := &tunnelCredentialStoreStub{
		ack:       goagentembedded.PKISecurityAcknowledgement{PKIDomainID: "domain-1", PKIEpoch: 1, Full: true, CertificateID: "certificate-1", TrustGenerations: []int64{1}},
		ackErr:    goagentembedded.ErrPKIActiveCredential,
		activeErr: goagentembedded.ErrPKIActiveCredential,
		pending:   pending,
	}
	pki := &tunnelPKIServiceStub{
		snapshot: snapshot,
		enrollment: service.PKILocalEnrollmentReply{
			TunnelCredential: credential,
			SecuritySnapshot: snapshot,
		},
	}
	runtime := &Runtime{
		agentID: "local-agent", credentials: store, tunnelPKI: pki,
		now: func() time.Time { return now },
	}
	if err := runtime.ReconcileTunnelPKI(t.Context()); err != nil {
		t.Fatalf("ReconcileTunnelPKI() error = %v", err)
	}
	if len(pki.requests) != 1 || pki.requests[0].RequestID != pending.Request.RequestID ||
		pki.requests[0].Purpose != storage.PKICertificatePurposeClient ||
		pki.requests[0].CSRPEM != pending.Request.CSRPEM {
		t.Fatalf("local enrollment request = %+v", pki.requests)
	}
	if !store.activated || store.activationRequest.Expectation.AgentID != "local-agent" ||
		store.activationRequest.Expectation.DomainID != "domain-1" ||
		store.activationRequest.Credential.Purpose != storage.PKICertificatePurposeClient {
		t.Fatalf("activation request = %+v", store.activationRequest)
	}
	if len(pki.acks) != 2 || pki.acks[0] != nil || pki.acks[1] == nil || pki.acks[1].CertificateID != "certificate-1" {
		t.Fatalf("local PKI acknowledgement sequence = %+v", pki.acks)
	}
}

func TestReconcileTunnelPKIListenersDoesNotEnrollForeignRelayHop(t *testing.T) {
	now := time.Date(2026, 8, 2, 8, 0, 0, 0, time.UTC)
	listener := storage.RelayListener{
		ID: 2, AgentID: "remote-agent", Enabled: true, TLSMode: "pki_mtls",
		PKIIdentityID: "remote-listener-identity", PKIIdentityState: storage.PKIIdentityStateActive,
	}
	pki := &tunnelPKIServiceStub{preparedListeners: []storage.RelayListener{listener}}
	credentials := &tunnelCredentialStoreStub{activeErr: goagentembedded.ErrPKIActiveCredential}
	runtime := &Runtime{
		agentID: "local-agent", credentials: credentials, tunnelPKI: pki,
		source: NewSyncSource(&bridgeStoreStub{snapshot: Snapshot{RelayListeners: []storage.RelayListener{listener}}}, "local-agent"),
		now:    func() time.Time { return now },
	}
	if err := runtime.reconcileTunnelPKIListeners(t.Context(), pki, credentials, tunnelPKITestSnapshot(now), toEmbeddedPKISnapshot(tunnelPKITestSnapshot(now)), ""); err != nil {
		t.Fatalf("reconcileTunnelPKIListeners() error = %v", err)
	}
	if len(pki.requests) != 0 || credentials.activationRequest.StorageIdentity != "" {
		t.Fatalf("foreign relay hop attempted local enrollment: requests=%+v activation=%+v", pki.requests, credentials.activationRequest)
	}
}

func TestNewRuntimeRetainsEmbeddedTunnelCredentialFacade(t *testing.T) {
	cfg := config.Default()
	cfg.LocalAgentID = "local-agent"
	cfg.LocalAgentName = "embedded local agent"
	cfg.DataDir = t.TempDir()
	runtime, err := NewRuntime(cfg, &bridgeStoreStub{})
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}
	if closer, ok := runtime.runtime.(interface{ Close() error }); ok {
		t.Cleanup(func() { _ = closer.Close() })
	}
	if err := runtime.ConfigureTunnelPKI(&tunnelPKIServiceStub{}); err != nil {
		t.Fatalf("ConfigureTunnelPKI() erased embedded credential facade: %v", err)
	}
}

func TestReconcileTunnelPKIKeepsHealthyCredentialAndUsesEmergencyRegistrationReset(t *testing.T) {
	now := time.Date(2026, 8, 2, 8, 0, 0, 0, time.UTC)
	snapshot := tunnelPKITestSnapshot(now)
	active := goagentembedded.PKICredentialMetadata{Manifest: goagentembedded.PKICredentialManifest{
		PKIDomainID: "domain-1",
		Expectation: goagentembedded.PKICredentialExpectation{
			DomainID: "domain-1", AgentID: "local-agent",
			Kind: storage.PKIIdentityKindAgent, Purpose: storage.PKICertificatePurposeClient,
		},
		Credential: toEmbeddedPKICredential(tunnelPKITestCredential(now)),
	}}

	t.Run("healthy credential", func(t *testing.T) {
		store := &tunnelCredentialStoreStub{active: active, ack: goagentembedded.PKISecurityAcknowledgement{PKIDomainID: "domain-1"}}
		pki := &tunnelPKIServiceStub{snapshot: snapshot}
		runtime := &Runtime{agentID: "local-agent", credentials: store, tunnelPKI: pki, now: func() time.Time { return now }}
		if err := runtime.ReconcileTunnelPKI(t.Context()); err != nil {
			t.Fatalf("ReconcileTunnelPKI() error = %v", err)
		}
		if len(pki.requests) != 0 || store.activated {
			t.Fatalf("healthy credential unexpectedly reenrolled: requests=%+v activation=%+v", pki.requests, store.activationRequest)
		}
	})

	t.Run("emergency trust reset", func(t *testing.T) {
		pending := tunnelPKITestPending()
		store := &tunnelCredentialStoreStub{
			active: active, applyErr: goagentembedded.ErrPKISecurityInvalid,
			pending: pending,
			ack:     goagentembedded.PKISecurityAcknowledgement{PKIDomainID: "domain-1", PKIEpoch: 1, CertificateID: "certificate-old"},
		}
		pki := &tunnelPKIServiceStub{
			snapshot: snapshot,
			enrollment: service.PKILocalEnrollmentReply{
				TunnelCredential: tunnelPKITestCredential(now), SecuritySnapshot: snapshot,
			},
		}
		runtime := &Runtime{agentID: "local-agent", credentials: store, tunnelPKI: pki, now: func() time.Time { return now }}
		if err := runtime.ReconcileTunnelPKI(t.Context()); err != nil {
			t.Fatalf("ReconcileTunnelPKI(emergency) error = %v", err)
		}
		if len(pki.requests) != 1 || !store.activated {
			t.Fatalf("emergency reset did not use local registration: requests=%+v activation=%+v", pki.requests, store.activationRequest)
		}
	})
}

func TestReconcileTunnelPKIQuarantinesTerminalReplayRejection(t *testing.T) {
	now := time.Date(2026, 8, 2, 8, 0, 0, 0, time.UTC)
	pending := tunnelPKITestPending()
	store := &tunnelCredentialStoreStub{
		ackErr: goagentembedded.ErrPKIActiveCredential, activeErr: goagentembedded.ErrPKIActiveCredential,
		pending: pending,
	}
	pki := &tunnelPKIServiceStub{
		snapshot:      tunnelPKITestSnapshot(now),
		enrollmentErr: service.ErrPKIEnrollmentOwnerMismatch,
	}
	runtime := &Runtime{agentID: "local-agent", credentials: store, tunnelPKI: pki, now: func() time.Time { return now }}
	err := runtime.ReconcileTunnelPKI(t.Context())
	if !errors.Is(err, service.ErrPKIEnrollmentOwnerMismatch) {
		t.Fatalf("ReconcileTunnelPKI() error = %v", err)
	}
	if store.rejectedRequestID != pending.Request.RequestID || store.rejectedReason != "owner_mismatch" {
		t.Fatalf("terminal replay rejection = request %q reason %q", store.rejectedRequestID, store.rejectedReason)
	}
}

func tunnelPKITestPending() goagentembedded.PKIPendingEnrollment {
	return goagentembedded.PKIPendingEnrollment{
		StorageIdentity: "agent", DomainID: "domain-1", AgentID: "local-agent",
		Request: goagentembedded.PKIEnrollmentRequest{
			RequestID: "0123456789abcdef0123456789abcdef",
			Kind:      storage.PKIIdentityKindAgent, Purpose: storage.PKICertificatePurposeClient,
			CSRPEM: "PUBLIC CSR",
		},
	}
}

func tunnelPKITestCredential(now time.Time) storage.PKITunnelCredential {
	return storage.PKITunnelCredential{
		IdentityID: "identity-1", CertificateID: "certificate-1",
		Purpose: storage.PKICertificatePurposeClient, CertificatePEM: "PUBLIC CERTIFICATE",
		PublicKeyFingerprint: "fingerprint-1", AuthorityID: "authority-1", CAGeneration: 1,
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(89 * 24 * time.Hour),
	}
}

func tunnelPKITestSnapshot(now time.Time) storage.PKISecuritySnapshot {
	return storage.PKISecuritySnapshot{
		PKIDomainID: "domain-1", PKIEpoch: 1, Full: true, IssuedAt: now,
		TrustRoots: []storage.PKITrustRoot{{
			AuthorityID: "authority-1", Generation: 1, Status: "active",
			CertificatePEM: "PUBLIC CA", FingerprintSHA256: "fingerprint-ca",
			NotBefore: now.Add(-time.Hour), NotAfter: now.Add(365 * 24 * time.Hour),
		}},
		SignerGeneration: 1, Signature: []byte("signature"),
	}
}
