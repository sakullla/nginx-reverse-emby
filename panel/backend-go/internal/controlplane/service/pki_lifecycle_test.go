package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestPKILifecycleEndpointRotationRetainsActiveAndRetries(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	clock := now
	active := testPKIEndpointState("identity-a", "cert-a1", 1, now.Add(-89*24*time.Hour), now.Add(time.Hour), "a")
	repository := newPKIEndpointRotationTestRepository(active)
	rotator := &pkiEndpointRotationTestRotator{errByIdentity: map[string]error{"identity-a": errors.New("candidate write failed")}}
	service := newPKILifecycleTestService(t, repository, rotator, func() time.Time { return clock })

	result, err := service.RunEndpointRotation(t.Context(), "identity-a", false)
	if err == nil || !result.Started || result.Activated || result.ActiveCertificate != "cert-a1" {
		t.Fatalf("RunEndpointRotation(failure) = (%+v, %v)", result, err)
	}
	stored := repository.state("identity-a")
	if stored.CertificateID != "cert-a1" || stored.FailureCount != 1 || !stored.NextAttemptAt.After(now) {
		t.Fatalf("state after failed rotation = %+v", stored)
	}
	if len(repository.failures) != 1 || repository.activations != 0 {
		t.Fatalf("failure/activation calls = %d/%d", len(repository.failures), repository.activations)
	}

	clock = stored.NextAttemptAt
	delete(rotator.errByIdentity, "identity-a")
	rotator.candidateByIdentity = map[string]PKIEndpointRotationCandidate{
		"identity-a": testPKIEndpointCandidate("cert-a2", 2, clock, "b"),
	}
	result, err = service.RunEndpointRotation(t.Context(), "identity-a", false)
	if err != nil || !result.Activated || result.ActiveCertificate != "cert-a2" || result.FailureCount != 0 {
		t.Fatalf("RunEndpointRotation(retry) = (%+v, %v)", result, err)
	}
	stored = repository.state("identity-a")
	if stored.CertificateID != "cert-a2" || stored.Generation != 2 || stored.FailureCount != 0 {
		t.Fatalf("state after successful retry = %+v", stored)
	}
}

func TestPKILifecycleExpiredFailureClosesAndForceRotationIsTargeted(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	clock := now
	expired := testPKIEndpointState("identity-expired", "cert-expired-1", 1, now.Add(-25*time.Hour), now.Add(-time.Hour), "c")
	notDue := testPKIEndpointState("identity-target", "cert-target-1", 1, now.Add(-time.Hour), now.Add(89*24*time.Hour), "d")
	untouched := testPKIEndpointState("identity-other", "cert-other-1", 1, now.Add(-time.Hour), now.Add(89*24*time.Hour), "e")
	repository := newPKIEndpointRotationTestRepository(expired, notDue, untouched)
	rotator := &pkiEndpointRotationTestRotator{
		errByIdentity: map[string]error{"identity-expired": errors.New("issuer unavailable")},
		candidateByIdentity: map[string]PKIEndpointRotationCandidate{
			"identity-target": testPKIEndpointCandidate("cert-target-2", 2, now, "f"),
		},
	}
	service := newPKILifecycleTestService(t, repository, rotator, func() time.Time { return clock })

	failed, err := service.RunEndpointRotation(t.Context(), "identity-expired", false)
	if !errors.Is(err, ErrPKIEndpointFailedClosed) || !failed.FailedClosed || failed.ActiveCertificate != "cert-expired-1" {
		t.Fatalf("expired rotation = (%+v, %v)", failed, err)
	}
	forced, err := service.ForceRotateEndpoint(t.Context(), "identity-target")
	if err != nil || !forced.Forced || !forced.Activated || forced.ActiveCertificate != "cert-target-2" {
		t.Fatalf("ForceRotateEndpoint(target) = (%+v, %v)", forced, err)
	}
	if repository.state("identity-other").CertificateID != "cert-other-1" || repository.state("identity-expired").CertificateID != "cert-expired-1" {
		t.Fatal("force rotation changed a non-target identity")
	}
}

func TestPKILifecycleSchedulerUsesStableStaggerAndBackoff(t *testing.T) {
	t.Parallel()
	policy := DefaultInternalPKIPolicy()
	notBefore := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	state := testPKIEndpointState("identity-a", "cert-a", 1, notBefore, notBefore.Add(policy.EndpointLifetime), "1")
	first, err := EvaluatePKIEndpointSchedule(policy, state, notBefore, false)
	if err != nil {
		t.Fatalf("EvaluatePKIEndpointSchedule() error = %v", err)
	}
	second, err := EvaluatePKIEndpointSchedule(policy, state, notBefore, false)
	if err != nil || !first.RenewalDueAt.Equal(second.RenewalDueAt) {
		t.Fatalf("stable due times = %v and %v, error %v", first.RenewalDueAt, second.RenewalDueAt, err)
	}
	baseDue := state.NotAfter.Add(-policy.EndpointLifetime / 3)
	if first.RenewalDueAt.Before(baseDue) || first.RenewalDueAt.After(baseDue.Add(24*time.Hour)) {
		t.Fatalf("renewal due = %v, want lifetime/3 plus bounded stagger", first.RenewalDueAt)
	}
	failed, err := NextPKIEndpointRetry(policy, state, first.RenewalDueAt)
	if err != nil || failed.FailureCount != 1 || !failed.NextAttemptAt.After(first.RenewalDueAt) {
		t.Fatalf("NextPKIEndpointRetry() = (%+v, %v)", failed, err)
	}
	decision, err := EvaluatePKIEndpointSchedule(policy, failed, first.RenewalDueAt, false)
	if err != nil || decision.Due {
		t.Fatalf("backoff decision = (%+v, %v), want not due", decision, err)
	}
	forced, err := EvaluatePKIEndpointSchedule(policy, failed, first.RenewalDueAt, true)
	if err != nil || !forced.Due || !forced.Forced {
		t.Fatalf("forced decision = (%+v, %v)", forced, err)
	}
}

func TestPKILifecycleRepositoryFenceRejectsCheckCommitLeaseLoss(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 2, 14, 0, 0, 0, time.UTC)
	active := testPKIEndpointState("identity-a", "cert-a1", 1, now.Add(-89*24*time.Hour), now.Add(time.Hour), "a")
	repository := newPKIEndpointRotationTestRepository(active)
	repository.rejectLease = true
	rotator := &pkiEndpointRotationTestRotator{candidateByIdentity: map[string]PKIEndpointRotationCandidate{
		"identity-a": testPKIEndpointCandidate("cert-a2", 2, now, "b"),
	}}
	service := newPKILifecycleTestService(t, repository, rotator, func() time.Time { return now })
	result, err := service.RunEndpointRotation(t.Context(), "identity-a", false)
	if !errors.Is(err, ErrPKILeaseNotHeld) || result.Activated || repository.state("identity-a").CertificateID != "cert-a1" ||
		repository.activations != 0 || len(repository.failures) != 0 {
		t.Fatalf("fenced rotation = (%+v, %v), state %+v", result, err, repository.state("identity-a"))
	}
}

func TestPKILifecycleRejectsFutureDatedCandidateBeforeActivation(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 2, 15, 0, 0, 0, time.UTC)
	active := testPKIEndpointState("identity-a", "cert-a1", 1, now.Add(-89*24*time.Hour), now.Add(time.Hour), "a")
	repository := newPKIEndpointRotationTestRepository(active)
	candidate := testPKIEndpointCandidate("cert-a2", 2, now, "b")
	candidate.NotBefore = now.Add(time.Minute)
	rotator := &pkiEndpointRotationTestRotator{candidateByIdentity: map[string]PKIEndpointRotationCandidate{"identity-a": candidate}}
	service := newPKILifecycleTestService(t, repository, rotator, func() time.Time { return now })
	result, err := service.RunEndpointRotation(t.Context(), "identity-a", false)
	if err == nil || result.Activated || repository.state("identity-a").CertificateID != "cert-a1" || repository.activations != 0 {
		t.Fatalf("future candidate rotation = (%+v, %v), state %+v", result, err, repository.state("identity-a"))
	}
}

func TestPKILifecycleTransitionTableNeverReopensTerminalOperations(t *testing.T) {
	t.Parallel()
	terminal := []string{
		"succeeded",
		"failed",
		"cancelled",
	}
	for _, previous := range terminal {
		if !pkiLifecycleTerminal(previous) {
			t.Fatalf("%q is not recognized as terminal", previous)
		}
		for _, next := range []string{"pending", "running", "succeeded", "failed", "cancelled"} {
			if pkiLifecycleTransitionAllowed(previous, next) {
				t.Fatalf("terminal operation transition %s -> %s was allowed", previous, next)
			}
		}
	}
	allowed := [][2]string{
		{"pending", "running"}, {"pending", "failed"}, {"pending", "cancelled"},
		{"running", "running"}, {"running", "succeeded"}, {"running", "failed"}, {"running", "cancelled"},
	}
	for _, transition := range allowed {
		if !pkiLifecycleTransitionAllowed(transition[0], transition[1]) {
			t.Fatalf("required operation transition %s -> %s was rejected", transition[0], transition[1])
		}
	}
}

func testPKIEndpointState(identityID, certificateID string, generation int64, notBefore, notAfter time.Time, marker string) PKIEndpointCertificateState {
	return PKIEndpointCertificateState{
		IdentityID: identityID, CertificateID: certificateID, Generation: generation,
		CertificateFingerprintSHA256: strings.Repeat(marker, 64),
		PublicKeyFingerprintSHA256:   strings.Repeat(marker+"0", 32),
		NotBefore:                    notBefore, NotAfter: notAfter,
	}
}

func testPKIEndpointCandidate(certificateID string, generation int64, now time.Time, marker string) PKIEndpointRotationCandidate {
	return PKIEndpointRotationCandidate{
		CertificateID: certificateID, Generation: generation,
		CertificateFingerprintSHA256: strings.Repeat(marker, 64),
		PublicKeyFingerprintSHA256:   strings.Repeat(marker+"0", 32),
		NotBefore:                    now.Add(-time.Minute), NotAfter: now.Add(90 * 24 * time.Hour), Verified: true,
	}
}

func newPKILifecycleTestService(
	t *testing.T,
	repository PKIEndpointRotationRepository,
	rotator PKIEndpointRotator,
	clock func() time.Time,
) *PKILifecycleService {
	t.Helper()
	service, err := NewPKILifecycleService(PKILifecycleServiceOptions{
		Policy: DefaultInternalPKIPolicy(), Repository: repository, Rotator: rotator,
		Lease: pkiStaticLeaseGate{}, Clock: clock,
	})
	if err != nil {
		t.Fatalf("NewPKILifecycleService() error = %v", err)
	}
	return service
}

type pkiEndpointRotationTestRepository struct {
	mutex       sync.Mutex
	states      map[string]PKIEndpointCertificateState
	failures    []PKIEndpointRotationFailure
	activations int
	rejectLease bool
}

func newPKIEndpointRotationTestRepository(states ...PKIEndpointCertificateState) *pkiEndpointRotationTestRepository {
	repository := &pkiEndpointRotationTestRepository{states: make(map[string]PKIEndpointCertificateState)}
	for _, state := range states {
		repository.states[state.IdentityID] = state
	}
	return repository
}

func (r *pkiEndpointRotationTestRepository) LoadPKIEndpointCertificate(_ context.Context, identityID string) (PKIEndpointCertificateState, error) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	state, ok := r.states[identityID]
	if !ok {
		return PKIEndpointCertificateState{}, fmt.Errorf("identity not found")
	}
	return state, nil
}

func (r *pkiEndpointRotationTestRepository) RecordPKIEndpointRotationFailure(_ context.Context, failure PKIEndpointRotationFailure) error {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	if r.rejectLease || validatePKIMutationLeaseFence(failure.Lease) != nil {
		return ErrPKILeaseNotHeld
	}
	state := r.states[failure.IdentityID]
	if state.CertificateID != failure.ExpectedActiveCertificateID {
		return ErrPKILifecycleConflict
	}
	state.FailureCount = failure.FailureCount
	state.NextAttemptAt = failure.NextAttemptAt
	r.states[failure.IdentityID] = state
	r.failures = append(r.failures, failure)
	return nil
}

func (r *pkiEndpointRotationTestRepository) ActivatePKIEndpointCandidate(_ context.Context, activation PKIEndpointActivation) error {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	if r.rejectLease || validatePKIMutationLeaseFence(activation.Lease) != nil {
		return ErrPKILeaseNotHeld
	}
	state := r.states[activation.IdentityID]
	if state.CertificateID != activation.ExpectedActiveCertificateID {
		return ErrPKILifecycleConflict
	}
	state.CertificateID = activation.Candidate.CertificateID
	state.CertificateFingerprintSHA256 = activation.Candidate.CertificateFingerprintSHA256
	state.PublicKeyFingerprintSHA256 = activation.Candidate.PublicKeyFingerprintSHA256
	state.Generation = activation.Candidate.Generation
	state.NotBefore = activation.Candidate.NotBefore
	state.NotAfter = activation.Candidate.NotAfter
	state.FailureCount = 0
	state.NextAttemptAt = time.Time{}
	r.states[activation.IdentityID] = state
	r.activations++
	return nil
}

func (r *pkiEndpointRotationTestRepository) state(identityID string) PKIEndpointCertificateState {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	return r.states[identityID]
}

type pkiEndpointRotationTestRotator struct {
	errByIdentity       map[string]error
	candidateByIdentity map[string]PKIEndpointRotationCandidate
}

func (r *pkiEndpointRotationTestRotator) StageAndVerifyPKIEndpoint(_ context.Context, active PKIEndpointCertificateState, _ bool) (PKIEndpointRotationCandidate, error) {
	if err := r.errByIdentity[active.IdentityID]; err != nil {
		return PKIEndpointRotationCandidate{}, err
	}
	candidate, ok := r.candidateByIdentity[active.IdentityID]
	if !ok {
		return PKIEndpointRotationCandidate{}, fmt.Errorf("candidate not configured")
	}
	return candidate, nil
}

type pkiStaticLeaseGate struct{}

func (pkiStaticLeaseGate) RequirePKILease(context.Context) (PKILeaseGrant, error) {
	return PKILeaseGrant{
		PKIDomainID: "domain-1", PKIEpoch: 1, InstanceID: "instance-1",
		LeaseTerm: strings.Repeat("a", 64), LeaseDeadline: time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC),
	}, nil
}
