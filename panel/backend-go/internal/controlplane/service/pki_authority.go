package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"
	"time"
)

const (
	PKICARotationPhasePrepare         = "prepare"
	PKICARotationPhaseDistributeTrust = "distribute_trust"
	PKICARotationPhaseReissue         = "reissue"
	PKICARotationPhaseCutover         = "cutover"
	PKICARotationPhaseOverlap         = "overlap"
	PKICARotationPhaseRetire          = "retire"
	PKICARotationPhaseSucceeded       = "succeeded"

	PKICARotationStateRunning   = "running"
	PKICARotationStateBlocked   = "blocked"
	PKICARotationStateSucceeded = "succeeded"

	PKICATrustAckTimeout = time.Hour
	PKIMaxCAOverlap      = 30 * 24 * time.Hour
)

type PKICARotationParticipant struct {
	IdentityID         string
	LastHeartbeatAt    time.Time
	CanReceiveRevision bool
	TrustAcked         bool
	Reissued           bool
	CutoverAcked       bool
	Quarantined        bool
	Revoked            bool
}

type PKICARotationJob struct {
	ID                     string
	Phase                  string
	State                  string
	CurrentGeneration      int64
	CurrentKeyFingerprint  string
	CurrentCertFingerprint string
	NewGeneration          int64
	NewKeyFingerprint      string
	NewCertFingerprint     string
	PhaseStartedAt         time.Time
	AckDeadline            time.Time
	CutoverAt              time.Time
	RetireDeadline         time.Time
	BlockedIdentityIDs     []string
	LastError              string
}

type PKICARotationInput struct {
	Now               time.Time
	HeartbeatInterval time.Duration
	Prepared          bool
	Retired           bool
	Participants      []PKICARotationParticipant
}

type PKICARotationAction struct {
	GenerateAuthority        bool
	DistributeTrust          bool
	RequestReissue           bool
	RequestCutover           bool
	PromoteNewAuthority      bool
	MarkOldAuthorityRetiring bool
	RetireOldAuthority       bool
	RemoveOldTrust           bool
	DestroyOldPrivateKey     bool
	ExpireOldCertificates    bool
	TrustGenerations         []int64
	BlockedIdentityIDs       []string
	Alerts                   []PKIAlertFact
}

// AdvancePKICARotation is a monotonic, restart-safe reducer. Offline agents do
// not block; agents continuously online within two heartbeat intervals do.
func AdvancePKICARotation(job PKICARotationJob, input PKICARotationInput) (PKICARotationJob, PKICARotationAction, error) {
	if strings.TrimSpace(job.ID) == "" || input.Now.IsZero() || input.HeartbeatInterval <= 0 ||
		job.CurrentGeneration <= 0 || strings.TrimSpace(job.CurrentKeyFingerprint) == "" ||
		strings.TrimSpace(job.CurrentCertFingerprint) == "" {
		return job, PKICARotationAction{}, fmt.Errorf("%w: CA rotation fields are incomplete", ErrPKILifecycleInvalid)
	}
	if job.Phase == "" {
		job.Phase = PKICARotationPhasePrepare
		job.State = PKICARotationStateRunning
		job.PhaseStartedAt = input.Now
	}
	if job.State == PKICARotationStateSucceeded || job.Phase == PKICARotationPhaseSucceeded {
		job.State = PKICARotationStateSucceeded
		job.Phase = PKICARotationPhaseSucceeded
		return job, PKICARotationAction{}, nil
	}
	action := PKICARotationAction{}
	switch job.Phase {
	case PKICARotationPhasePrepare:
		if !input.Prepared {
			action.GenerateAuthority = true
			return job, action, nil
		}
		if err := validatePreparedPKIAuthority(job); err != nil {
			return job, action, err
		}
		advancePKICARotationPhase(&job, PKICARotationPhaseDistributeTrust, input.Now, true)
		action.DistributeTrust = true
		action.TrustGenerations = []int64{job.CurrentGeneration, job.NewGeneration}
		return job, action, nil
	case PKICARotationPhaseDistributeTrust:
		action.DistributeTrust = true
		action.TrustGenerations = []int64{job.CurrentGeneration, job.NewGeneration}
		missing := missingOnlinePKIAcks(input, func(participant PKICARotationParticipant) bool { return participant.TrustAcked })
		if len(missing) > 0 {
			return blockOrRetryPKICARotation(job, action, input.Now, missing, "online identity did not acknowledge dual trust")
		}
		advancePKICARotationPhase(&job, PKICARotationPhaseReissue, input.Now, true)
		action.RequestReissue = true
		return job, action, nil
	case PKICARotationPhaseReissue:
		action.RequestReissue = true
		missing := missingOnlinePKIAcks(input, func(participant PKICARotationParticipant) bool { return participant.Reissued })
		if len(missing) > 0 {
			return blockOrRetryPKICARotation(job, action, input.Now, missing, "online identity did not reissue under the new CA")
		}
		advancePKICARotationPhase(&job, PKICARotationPhaseCutover, input.Now, true)
		action.RequestCutover = true
		return job, action, nil
	case PKICARotationPhaseCutover:
		action.RequestCutover = true
		missing := missingOnlinePKIAcks(input, func(participant PKICARotationParticipant) bool { return participant.CutoverAcked })
		if len(missing) > 0 {
			return blockOrRetryPKICARotation(job, action, input.Now, missing, "online identity did not acknowledge CA cutover")
		}
		advancePKICARotationPhase(&job, PKICARotationPhaseOverlap, input.Now, false)
		job.CutoverAt = input.Now
		job.RetireDeadline = input.Now.Add(PKIMaxCAOverlap)
		action.PromoteNewAuthority = true
		action.MarkOldAuthorityRetiring = true
		action.TrustGenerations = []int64{job.CurrentGeneration, job.NewGeneration}
		return job, action, nil
	case PKICARotationPhaseOverlap:
		if job.CutoverAt.IsZero() || job.RetireDeadline.IsZero() ||
			job.RetireDeadline.After(job.CutoverAt.Add(PKIMaxCAOverlap)) {
			return job, action, fmt.Errorf("%w: CA overlap exceeds 30 days", ErrPKILifecycleInvalid)
		}
		if input.Now.Before(job.RetireDeadline) {
			action.RequestReissue = true
			action.RequestCutover = true
			action.TrustGenerations = []int64{job.CurrentGeneration, job.NewGeneration}
			return job, action, nil
		}
		advancePKICARotationPhase(&job, PKICARotationPhaseRetire, input.Now, false)
		setPKICARetireAction(&action, job.NewGeneration)
		return job, action, nil
	case PKICARotationPhaseRetire:
		if !input.Retired {
			setPKICARetireAction(&action, job.NewGeneration)
			return job, action, nil
		}
		job.Phase = PKICARotationPhaseSucceeded
		job.State = PKICARotationStateSucceeded
		job.PhaseStartedAt = input.Now
		job.BlockedIdentityIDs = nil
		job.LastError = ""
		return job, action, nil
	default:
		return job, action, fmt.Errorf("%w: unknown CA rotation phase %q", ErrPKILifecycleInvalid, job.Phase)
	}
}

func setPKICARetireAction(action *PKICARotationAction, newGeneration int64) {
	action.RetireOldAuthority = true
	action.RemoveOldTrust = true
	action.DestroyOldPrivateKey = true
	action.ExpireOldCertificates = true
	action.TrustGenerations = []int64{newGeneration}
}

type PKICARotationTransition struct {
	ExpectedPhase  string
	ExpectedState  string
	IdempotencyKey string
	Lease          PKILeaseGrant
	Job            PKICARotationJob
	Action         PKICARotationAction
	Event          PKIAuditEvent
}

// PKICARotationRepository persists every reducer step with a CAS on the
// phase/state that were loaded and an atomic canonical live-lease comparison
// using Transition.Lease, making restart replay idempotent and fenced.
type PKICARotationRepository interface {
	LoadPKICARotationJob(context.Context, string) (PKICARotationJob, error)
	SavePKICARotationTransition(context.Context, PKICARotationTransition) error
}

type PKICARotationService struct {
	repository        PKICARotationRepository
	lease             PKILeaseGate
	clock             func() time.Time
	heartbeatInterval time.Duration
}

func NewPKICARotationService(
	repository PKICARotationRepository,
	lease PKILeaseGate,
	clock func() time.Time,
	heartbeatInterval time.Duration,
) (*PKICARotationService, error) {
	if repository == nil || lease == nil || heartbeatInterval <= 0 {
		return nil, fmt.Errorf("%w: CA rotation repository, lease, and heartbeat interval are required", ErrPKILifecycleInvalid)
	}
	if clock == nil {
		clock = time.Now
	}
	return &PKICARotationService{repository: repository, lease: lease, clock: clock, heartbeatInterval: heartbeatInterval}, nil
}

func (s *PKICARotationService) Advance(
	ctx context.Context,
	jobID string,
	input PKICARotationInput,
) (PKICARotationJob, PKICARotationAction, error) {
	jobID = strings.TrimSpace(jobID)
	if jobID == "" {
		return PKICARotationJob{}, PKICARotationAction{}, fmt.Errorf("%w: CA rotation job ID is required", ErrPKILifecycleInvalid)
	}
	before, err := s.lease.RequirePKILease(ctx)
	if err != nil {
		return PKICARotationJob{}, PKICARotationAction{}, err
	}
	current, err := s.repository.LoadPKICARotationJob(ctx, jobID)
	if err != nil {
		return PKICARotationJob{}, PKICARotationAction{}, err
	}
	if current.ID != jobID {
		return PKICARotationJob{}, PKICARotationAction{}, fmt.Errorf("%w: repository returned another CA rotation job", ErrPKILifecycleInvalid)
	}
	input.Now = s.clock().UTC()
	input.HeartbeatInterval = s.heartbeatInterval
	next, action, err := AdvancePKICARotation(current, input)
	if err != nil {
		return current, action, err
	}
	if current.Phase == PKICARotationPhaseSucceeded && current.State == PKICARotationStateSucceeded {
		return current, action, nil
	}
	after, err := s.lease.RequirePKILease(ctx)
	if err != nil || !samePKILeaseAuthority(before, after) {
		if err == nil {
			err = ErrPKILeaseNotHeld
		}
		return current, action, err
	}
	if err := validatePKIMutationLeaseFence(after); err != nil {
		return current, action, err
	}
	eventType := "ca_rotation_progressed"
	result := "succeeded"
	if next.State == PKICARotationStateBlocked {
		eventType = "ca_rotation_blocked"
		result = "blocked"
	}
	event := NewPKIAuditEvent(eventType, "scheduler", jobID, result, next.LastError, input.Now)
	event.ObjectType = "pki_lifecycle_job"
	event.CAGeneration = next.NewGeneration
	event.ID = stablePKIAuditEventID(event)
	transition := PKICARotationTransition{
		ExpectedPhase: current.Phase, ExpectedState: current.State,
		IdempotencyKey: pkiCARotationTransitionKey(current, next), Lease: after,
		Job: next, Action: action, Event: event,
	}
	if err := s.repository.SavePKICARotationTransition(ctx, transition); err != nil {
		return current, action, err
	}
	return next, action, nil
}

func pkiCARotationTransitionKey(current, next PKICARotationJob) string {
	digest := sha256.Sum256([]byte(fmt.Sprintf(
		"%s\x00%s\x00%s\x00%s\x00%s\x00%s\x00%s\x00%s",
		current.ID, current.Phase, current.State, next.Phase, next.State,
		next.AckDeadline.UTC().Format(time.RFC3339Nano), next.RetireDeadline.UTC().Format(time.RFC3339Nano),
		strings.Join(next.BlockedIdentityIDs, ","),
	)))
	return "pki-ca-transition-" + hex.EncodeToString(digest[:16])
}

func missingOnlinePKIAcks(input PKICARotationInput, acknowledged func(PKICARotationParticipant) bool) []string {
	missing := make([]string, 0)
	for _, participant := range input.Participants {
		if participant.Quarantined || participant.Revoked || strings.TrimSpace(participant.IdentityID) == "" {
			continue
		}
		online := participant.CanReceiveRevision && !participant.LastHeartbeatAt.IsZero() &&
			!participant.LastHeartbeatAt.After(input.Now) && input.Now.Sub(participant.LastHeartbeatAt) <= 2*input.HeartbeatInterval
		if online && !acknowledged(participant) {
			missing = append(missing, participant.IdentityID)
		}
	}
	slices.Sort(missing)
	return slices.Compact(missing)
}

func blockOrRetryPKICARotation(
	job PKICARotationJob,
	action PKICARotationAction,
	now time.Time,
	missing []string,
	reason string,
) (PKICARotationJob, PKICARotationAction, error) {
	job.BlockedIdentityIDs = slices.Clone(missing)
	if job.AckDeadline.IsZero() {
		job.AckDeadline = job.PhaseStartedAt.Add(PKICATrustAckTimeout)
	}
	if now.Before(job.AckDeadline) {
		job.State = PKICARotationStateRunning
		return job, action, nil
	}
	job.State = PKICARotationStateBlocked
	job.LastError = reason
	action.BlockedIdentityIDs = slices.Clone(missing)
	action.Alerts = append(action.Alerts, PKIAlertFact{
		Kind: PKIAlertKindRotationBlocked, ObjectType: "pki_lifecycle_job", ObjectID: job.ID,
		Level: PKIAlertCritical, FirstSeen: job.AckDeadline, LastSeen: now,
		Reason: fmt.Sprintf("%s: %s", reason, strings.Join(missing, ",")),
	})
	return job, action, nil
}

func advancePKICARotationPhase(job *PKICARotationJob, phase string, now time.Time, withAckDeadline bool) {
	job.Phase = phase
	job.State = PKICARotationStateRunning
	job.PhaseStartedAt = now
	job.BlockedIdentityIDs = nil
	job.LastError = ""
	job.AckDeadline = time.Time{}
	if withAckDeadline {
		job.AckDeadline = now.Add(PKICATrustAckTimeout)
	}
}

func validatePreparedPKIAuthority(job PKICARotationJob) error {
	if job.NewGeneration <= job.CurrentGeneration || strings.TrimSpace(job.NewKeyFingerprint) == "" ||
		strings.TrimSpace(job.NewCertFingerprint) == "" || job.NewKeyFingerprint == job.CurrentKeyFingerprint ||
		job.NewCertFingerprint == job.CurrentCertFingerprint {
		return fmt.Errorf("%w: prepared CA must use a new generation, key, and certificate", ErrPKILifecycleInvalid)
	}
	return nil
}

type PKIAuthorityMaterial struct {
	Generation             int64
	CertificatePEM         string
	KeyReference           string
	KeyFingerprint         string
	CertificateFingerprint string
	NotBefore              time.Time
	NotAfter               time.Time
}

type PKIEmergencyAuthorityState struct {
	PKIDomainID           string
	ActiveGeneration      int64
	ActiveKeyFingerprint  string
	ActiveCertFingerprint string
	SecurityRevision      int64
}

type PKIEmergencyRotationRequest struct {
	Reason     string
	OperatorID string
	Confirmed  bool
}

type PKIEmergencyRotationCommit struct {
	PreviousGeneration   int64
	NewAuthority         PKIAuthorityMaterial
	SecurityRevision     int64
	RevokeAllOldTrust    bool
	DisableControlTokens bool
	RequireReenrollment  bool
	Lease                PKILeaseGrant
	Event                PKIAuditEvent
}

// CommitPKIEmergencyAuthorityRotation must compare commit.Lease with the live
// canonical lease in the same transaction that revokes old trust, increments
// security revision, disables tokens, and activates the new authority.
type PKIEmergencyAuthorityRepository interface {
	LoadPKIEmergencyAuthorityState(context.Context) (PKIEmergencyAuthorityState, error)
	CommitPKIEmergencyAuthorityRotation(context.Context, PKIEmergencyRotationCommit) error
}

type PKIAuthorityGenerator interface {
	GeneratePKIAuthority(context.Context, int64, string) (PKIAuthorityMaterial, error)
}

type PKIEmergencyRelayGate interface {
	DisablePKIRelay(context.Context) error
}

type PKIEmergencyAuthorityService struct {
	repository PKIEmergencyAuthorityRepository
	generator  PKIAuthorityGenerator
	relay      PKIEmergencyRelayGate
	lease      PKILeaseGate
	clock      func() time.Time
}

func NewPKIEmergencyAuthorityService(
	repository PKIEmergencyAuthorityRepository,
	generator PKIAuthorityGenerator,
	relay PKIEmergencyRelayGate,
	lease PKILeaseGate,
	clock func() time.Time,
) (*PKIEmergencyAuthorityService, error) {
	if repository == nil || generator == nil || relay == nil || lease == nil {
		return nil, fmt.Errorf("%w: emergency CA dependencies are required", ErrPKILifecycleInvalid)
	}
	if clock == nil {
		clock = time.Now
	}
	return &PKIEmergencyAuthorityService{repository: repository, generator: generator, relay: relay, lease: lease, clock: clock}, nil
}

// Rotate disables relay before generating the replacement. Every subsequent
// error deliberately leaves relay disabled; there is no old-trust fallback.
func (s *PKIEmergencyAuthorityService) Rotate(ctx context.Context, request PKIEmergencyRotationRequest) (PKIEmergencyRotationCommit, error) {
	request.Reason = strings.TrimSpace(request.Reason)
	if !request.Confirmed || request.Reason == "" {
		return PKIEmergencyRotationCommit{}, fmt.Errorf("%w: emergency CA rotation requires confirmation and reason", ErrPKILifecycleInvalid)
	}
	before, err := s.lease.RequirePKILease(ctx)
	if err != nil {
		return PKIEmergencyRotationCommit{}, err
	}
	state, err := s.repository.LoadPKIEmergencyAuthorityState(ctx)
	if err != nil {
		return PKIEmergencyRotationCommit{}, err
	}
	if state.ActiveGeneration <= 0 || state.SecurityRevision < 0 || strings.TrimSpace(state.PKIDomainID) == "" {
		return PKIEmergencyRotationCommit{}, fmt.Errorf("%w: active CA state is invalid", ErrPKILifecycleInvalid)
	}
	if state.ActiveGeneration == int64(^uint64(0)>>1) || state.SecurityRevision == int64(^uint64(0)>>1) {
		return PKIEmergencyRotationCommit{}, fmt.Errorf("%w: CA generation or security revision cannot be incremented", ErrPKILifecycleInvalid)
	}
	if err := s.relay.DisablePKIRelay(ctx); err != nil {
		return PKIEmergencyRotationCommit{}, err
	}
	material, err := s.generator.GeneratePKIAuthority(ctx, state.ActiveGeneration+1, request.Reason)
	if err != nil {
		return PKIEmergencyRotationCommit{}, err
	}
	if err := validateEmergencyPKIAuthority(state, material, s.clock().UTC()); err != nil {
		return PKIEmergencyRotationCommit{}, err
	}
	after, err := s.lease.RequirePKILease(ctx)
	if err != nil || !samePKILeaseAuthority(before, after) {
		if err == nil {
			err = ErrPKILeaseNotHeld
		}
		return PKIEmergencyRotationCommit{}, err
	}
	if err := validatePKIMutationLeaseFence(after); err != nil {
		return PKIEmergencyRotationCommit{}, err
	}
	commit := PKIEmergencyRotationCommit{
		PreviousGeneration: state.ActiveGeneration, NewAuthority: material,
		SecurityRevision: state.SecurityRevision + 1, RevokeAllOldTrust: true,
		DisableControlTokens: true, RequireReenrollment: true, Lease: after,
		Event: NewPKIAuditEvent("ca_emergency_rotated", "operator", state.PKIDomainID, "succeeded", request.Reason, s.clock().UTC()),
	}
	commit.Event.OperatorID = strings.TrimSpace(request.OperatorID)
	commit.Event.ObjectType = "pki_domain"
	commit.Event.CAGeneration = material.Generation
	commit.Event.SecurityRevision = commit.SecurityRevision
	commit.Event.ID = stablePKIAuditEventID(commit.Event)
	if err := s.repository.CommitPKIEmergencyAuthorityRotation(ctx, commit); err != nil {
		return PKIEmergencyRotationCommit{}, err
	}
	return commit, nil
}

func validateEmergencyPKIAuthority(state PKIEmergencyAuthorityState, material PKIAuthorityMaterial, now time.Time) error {
	if now.IsZero() || material.Generation <= state.ActiveGeneration || strings.TrimSpace(material.CertificatePEM) == "" ||
		strings.TrimSpace(material.KeyReference) == "" || strings.TrimSpace(material.KeyFingerprint) == "" ||
		strings.TrimSpace(material.CertificateFingerprint) == "" || material.KeyFingerprint == state.ActiveKeyFingerprint ||
		material.CertificateFingerprint == state.ActiveCertFingerprint || material.NotBefore.IsZero() ||
		material.NotBefore.After(now) || !material.NotAfter.After(material.NotBefore) || !material.NotAfter.After(now) {
		return fmt.Errorf("%w: emergency CA replacement is invalid", ErrPKILifecycleInvalid)
	}
	return nil
}
