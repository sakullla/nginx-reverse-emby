package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

func TestPKIAuthorityRotationBlocksOnlineNonAckAndIgnoresOffline(t *testing.T) {
	now := time.Date(2026, 8, 3, 9, 0, 0, 0, time.UTC)
	job := PKICARotationJob{
		ID: "ca-job-1", CurrentGeneration: 1, CurrentKeyFingerprint: "old-key", CurrentCertFingerprint: "old-cert",
		NewGeneration: 2, NewKeyFingerprint: "new-key", NewCertFingerprint: "new-cert",
	}
	participants := []PKICARotationParticipant{
		{IdentityID: "online", LastHeartbeatAt: now, CanReceiveRevision: true},
		{IdentityID: "offline", LastHeartbeatAt: now.Add(-time.Hour), CanReceiveRevision: true},
	}
	var action PKICARotationAction
	var err error
	job, action, err = AdvancePKICARotation(job, PKICARotationInput{
		Now: now, HeartbeatInterval: 10 * time.Second, Prepared: true, Participants: participants,
	})
	if err != nil || job.Phase != PKICARotationPhaseDistributeTrust || !action.DistributeTrust {
		t.Fatalf("prepare transition = (%+v, %+v, %v)", job, action, err)
	}
	deadline := job.AckDeadline
	job, action, err = AdvancePKICARotation(job, PKICARotationInput{
		Now: deadline, HeartbeatInterval: 10 * time.Second,
		Participants: []PKICARotationParticipant{
			{IdentityID: "online", LastHeartbeatAt: deadline, CanReceiveRevision: true},
			{IdentityID: "offline", LastHeartbeatAt: now.Add(-time.Hour), CanReceiveRevision: true},
		},
	})
	if err != nil || job.State != PKICARotationStateBlocked || len(job.BlockedIdentityIDs) != 1 || job.BlockedIdentityIDs[0] != "online" {
		t.Fatalf("trust deadline result = (%+v, %+v, %v)", job, action, err)
	}
	alerts, err := DerivePKIAlerts(action.Alerts)
	if err != nil || len(alerts) != 1 || alerts[0].Level != PKIAlertCritical {
		t.Fatalf("blocked alerts = (%+v, %v)", alerts, err)
	}

	resumeAt := deadline.Add(time.Second)
	acknowledged := []PKICARotationParticipant{
		{IdentityID: "online", LastHeartbeatAt: resumeAt, CanReceiveRevision: true, TrustAcked: true},
		{IdentityID: "offline", LastHeartbeatAt: now.Add(-time.Hour), CanReceiveRevision: true},
	}
	job, action, err = AdvancePKICARotation(job, PKICARotationInput{Now: resumeAt, HeartbeatInterval: 10 * time.Second, Participants: acknowledged})
	if err != nil || job.Phase != PKICARotationPhaseReissue || !action.RequestReissue {
		t.Fatalf("trust resume = (%+v, %+v, %v)", job, action, err)
	}
	acknowledged[0].Reissued = true
	job, action, err = AdvancePKICARotation(job, PKICARotationInput{Now: resumeAt.Add(time.Second), HeartbeatInterval: 10 * time.Second, Participants: acknowledged})
	if err != nil || job.Phase != PKICARotationPhaseCutover || !action.RequestCutover {
		t.Fatalf("reissue transition = (%+v, %+v, %v)", job, action, err)
	}
	acknowledged[0].CutoverAcked = true
	cutoverAt := resumeAt.Add(2 * time.Second)
	job, action, err = AdvancePKICARotation(job, PKICARotationInput{Now: cutoverAt, HeartbeatInterval: 10 * time.Second, Participants: acknowledged})
	if err != nil || job.Phase != PKICARotationPhaseOverlap || job.RetireDeadline.Sub(job.CutoverAt) != PKIMaxCAOverlap ||
		!action.PromoteNewAuthority || !action.MarkOldAuthorityRetiring {
		t.Fatalf("cutover transition = (%+v, %+v, %v)", job, action, err)
	}
	job, action, err = AdvancePKICARotation(job, PKICARotationInput{Now: job.RetireDeadline, HeartbeatInterval: 10 * time.Second})
	if err != nil || job.Phase != PKICARotationPhaseRetire || !action.RetireOldAuthority || !action.RemoveOldTrust ||
		!action.DestroyOldPrivateKey || !action.ExpireOldCertificates {
		t.Fatalf("overlap retirement = (%+v, %+v, %v)", job, action, err)
	}
	job, _, err = AdvancePKICARotation(job, PKICARotationInput{Now: job.RetireDeadline.Add(time.Second), HeartbeatInterval: 10 * time.Second, Retired: true})
	if err != nil || job.Phase != PKICARotationPhaseSucceeded || job.State != PKICARotationStateSucceeded {
		t.Fatalf("retire completion = (%+v, %v)", job, err)
	}
}

func TestPKIAuthorityServicePersistsMonotonicTransitionWithCAS(t *testing.T) {
	now := time.Date(2026, 8, 3, 11, 0, 0, 0, time.UTC)
	repository := &pkiCARotationTestRepository{job: PKICARotationJob{
		ID: "ca-job", CurrentGeneration: 1, CurrentKeyFingerprint: "old-key", CurrentCertFingerprint: "old-cert",
		NewGeneration: 2, NewKeyFingerprint: "new-key", NewCertFingerprint: "new-cert",
	}}
	service, err := NewPKICARotationService(repository, pkiStaticLeaseGate{}, func() time.Time { return now }, 10*time.Second)
	if err != nil {
		t.Fatalf("NewPKICARotationService() error = %v", err)
	}
	job, action, err := service.Advance(t.Context(), "ca-job", PKICARotationInput{Prepared: true})
	if err != nil || job.Phase != PKICARotationPhaseDistributeTrust || !action.DistributeTrust || len(repository.transitions) != 1 {
		t.Fatalf("Advance() = (%+v, %+v, %v), transitions %d", job, action, err, len(repository.transitions))
	}
	transition := repository.transitions[0]
	if transition.ExpectedPhase != "" || transition.ExpectedState != "" || transition.IdempotencyKey == "" || transition.Job.Phase != PKICARotationPhaseDistributeTrust ||
		transition.Event.ObjectType != "pki_lifecycle_job" {
		t.Fatalf("persisted transition = %+v", transition)
	}
}

func TestPKIAuthorityRepositoryFenceRejectsCheckCommitLeaseLoss(t *testing.T) {
	now := time.Date(2026, 8, 3, 11, 30, 0, 0, time.UTC)
	repository := &pkiCARotationTestRepository{rejectLease: true, job: PKICARotationJob{
		ID: "ca-job", CurrentGeneration: 1, CurrentKeyFingerprint: "old-key", CurrentCertFingerprint: "old-cert",
		NewGeneration: 2, NewKeyFingerprint: "new-key", NewCertFingerprint: "new-cert",
	}}
	service, err := NewPKICARotationService(repository, pkiStaticLeaseGate{}, func() time.Time { return now }, 10*time.Second)
	if err != nil {
		t.Fatalf("NewPKICARotationService() error = %v", err)
	}
	if _, _, err := service.Advance(t.Context(), "ca-job", PKICARotationInput{Prepared: true}); !errors.Is(err, ErrPKILeaseNotHeld) {
		t.Fatalf("Advance(fence loss) error = %v, want ErrPKILeaseNotHeld", err)
	}
	if len(repository.transitions) != 0 || repository.job.Phase != "" {
		t.Fatalf("fenced repository mutated job: %+v, transitions %d", repository.job, len(repository.transitions))
	}
}

func TestPKIAuthorityRotationRejectsReusedKeyOrExcessOverlap(t *testing.T) {
	now := time.Date(2026, 8, 3, 9, 0, 0, 0, time.UTC)
	job := PKICARotationJob{
		ID: "ca-job", CurrentGeneration: 1, CurrentKeyFingerprint: "same", CurrentCertFingerprint: "old-cert",
		NewGeneration: 2, NewKeyFingerprint: "same", NewCertFingerprint: "new-cert",
	}
	if _, _, err := AdvancePKICARotation(job, PKICARotationInput{Now: now, HeartbeatInterval: 10 * time.Second, Prepared: true}); err == nil {
		t.Fatal("prepared CA with reused key was accepted")
	}
	job = PKICARotationJob{
		ID: "ca-job", Phase: PKICARotationPhaseOverlap, State: PKICARotationStateRunning,
		CurrentGeneration: 1, CurrentKeyFingerprint: "old-key", CurrentCertFingerprint: "old-cert",
		NewGeneration: 2, NewKeyFingerprint: "new-key", NewCertFingerprint: "new-cert",
		CutoverAt: now, RetireDeadline: now.Add(PKIMaxCAOverlap + time.Second),
	}
	if _, _, err := AdvancePKICARotation(job, PKICARotationInput{Now: now, HeartbeatInterval: 10 * time.Second}); err == nil {
		t.Fatal("CA overlap beyond 30 days was accepted")
	}
}

func TestPKIAuthorityEmergencyFailureNeverReenablesOldTrust(t *testing.T) {
	now := time.Date(2026, 8, 3, 10, 0, 0, 0, time.UTC)
	repository := &pkiEmergencyAuthorityTestRepository{state: PKIEmergencyAuthorityState{
		PKIDomainID: "domain-1", ActiveGeneration: 3, ActiveKeyFingerprint: "old-key",
		ActiveCertFingerprint: "old-cert", SecurityRevision: 9,
	}}
	relay := &pkiEmergencyRelayTestGate{}
	generator := &pkiAuthorityTestGenerator{err: errors.New("key generation failed")}
	service, err := NewPKIEmergencyAuthorityService(repository, generator, relay, pkiStaticLeaseGate{}, func() time.Time { return now })
	if err != nil {
		t.Fatalf("NewPKIEmergencyAuthorityService() error = %v", err)
	}
	if _, err := service.Rotate(t.Context(), PKIEmergencyRotationRequest{Reason: "suspected compromise", Confirmed: true}); err == nil {
		t.Fatal("emergency generation failure returned nil error")
	}
	if !relay.disabled || repository.committed {
		t.Fatalf("failure state = relay disabled %v, committed %v", relay.disabled, repository.committed)
	}

	relay = &pkiEmergencyRelayTestGate{}
	generator = &pkiAuthorityTestGenerator{material: PKIAuthorityMaterial{
		Generation: 4, CertificatePEM: "certificate", KeyReference: "vault/4", KeyFingerprint: "new-key",
		CertificateFingerprint: "new-cert", NotBefore: now.Add(-time.Minute), NotAfter: now.Add(10 * 365 * 24 * time.Hour),
	}}
	service, err = NewPKIEmergencyAuthorityService(repository, generator, relay, pkiStaticLeaseGate{}, func() time.Time { return now })
	if err != nil {
		t.Fatalf("NewPKIEmergencyAuthorityService(success) error = %v", err)
	}
	commit, err := service.Rotate(t.Context(), PKIEmergencyRotationRequest{
		Reason: "suspected compromise", OperatorID: "admin", Confirmed: true,
	})
	if err != nil || !relay.disabled || !repository.committed || !commit.RevokeAllOldTrust ||
		!commit.DisableControlTokens || !commit.RequireReenrollment || commit.SecurityRevision != 10 {
		t.Fatalf("emergency success = (%+v, %v), relay disabled %v", commit, err, relay.disabled)
	}

	repository.committed = false
	repository.rejectLease = true
	relay = &pkiEmergencyRelayTestGate{}
	service, err = NewPKIEmergencyAuthorityService(repository, generator, relay, pkiStaticLeaseGate{}, func() time.Time { return now })
	if err != nil {
		t.Fatalf("NewPKIEmergencyAuthorityService(fence) error = %v", err)
	}
	if _, err := service.Rotate(t.Context(), PKIEmergencyRotationRequest{Reason: "compromise", Confirmed: true}); !errors.Is(err, ErrPKILeaseNotHeld) {
		t.Fatalf("Rotate(fence loss) error = %v, want ErrPKILeaseNotHeld", err)
	}
	if repository.committed || !relay.disabled {
		t.Fatalf("fenced emergency state = committed %v, relay disabled %v", repository.committed, relay.disabled)
	}
}

type pkiEmergencyAuthorityTestRepository struct {
	state       PKIEmergencyAuthorityState
	commit      PKIEmergencyRotationCommit
	committed   bool
	rejectLease bool
}

type pkiCARotationTestRepository struct {
	job         PKICARotationJob
	transitions []PKICARotationTransition
	rejectLease bool
}

func (r *pkiCARotationTestRepository) LoadPKICARotationJob(context.Context, string) (PKICARotationJob, error) {
	return r.job, nil
}

func (r *pkiCARotationTestRepository) SavePKICARotationTransition(_ context.Context, transition PKICARotationTransition) error {
	if r.rejectLease || validatePKIMutationLeaseFence(transition.Lease) != nil {
		return ErrPKILeaseNotHeld
	}
	if r.job.Phase != transition.ExpectedPhase || r.job.State != transition.ExpectedState {
		return ErrPKILifecycleConflict
	}
	r.transitions = append(r.transitions, transition)
	r.job = transition.Job
	return nil
}

func (r *pkiEmergencyAuthorityTestRepository) LoadPKIEmergencyAuthorityState(context.Context) (PKIEmergencyAuthorityState, error) {
	return r.state, nil
}

func (r *pkiEmergencyAuthorityTestRepository) CommitPKIEmergencyAuthorityRotation(_ context.Context, commit PKIEmergencyRotationCommit) error {
	if r.rejectLease || validatePKIMutationLeaseFence(commit.Lease) != nil {
		return ErrPKILeaseNotHeld
	}
	r.commit = commit
	r.committed = true
	return nil
}

type pkiAuthorityTestGenerator struct {
	material PKIAuthorityMaterial
	err      error
}

func (g *pkiAuthorityTestGenerator) GeneratePKIAuthority(context.Context, int64, string) (PKIAuthorityMaterial, error) {
	return g.material, g.err
}

type pkiEmergencyRelayTestGate struct {
	disabled       bool
	enabled        bool
	disableBarrier PKIRelayRevisionBarrier
	enableBarrier  PKIRelayRevisionBarrier
	disableErr     error
	enableErr      error
}

func (g *pkiEmergencyRelayTestGate) DisablePKIRelay(context.Context, PKIRelayRevisionBarrier) (PKIRelayRevisionBarrier, error) {
	g.disabled = true
	if g.disableErr != nil {
		return g.disableBarrier, g.disableErr
	}
	if g.disableBarrier.OperationID == "" && len(g.disableBarrier.Revisions) == 0 {
		g.disableBarrier.Converged = true
	}
	return g.disableBarrier, nil
}

func (g *pkiEmergencyRelayTestGate) EnablePKIRelay(context.Context, PKIRelayRevisionBarrier) (PKIRelayRevisionBarrier, error) {
	if g.enableErr != nil {
		return g.enableBarrier, g.enableErr
	}
	g.enabled = true
	if g.enableBarrier.OperationID == "" && len(g.enableBarrier.Revisions) == 0 {
		g.enableBarrier.Converged = true
	}
	return g.enableBarrier, nil
}

func (g *pkiEmergencyRelayTestGate) ConfirmPKIRelayBarrier(
	context.Context,
	*storage.PKITransaction,
	PKIRelayRevisionBarrier,
) (bool, error) {
	return true, nil
}
