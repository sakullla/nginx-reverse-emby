package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

const PKIOnlineRevocationConvergence = 5 * time.Second

type PKIUnsignedSecuritySnapshot struct {
	PKIDomainID        string
	Version            PKISecuritySnapshotVersion
	IssuedAt           time.Time
	TrustGenerations   []int64
	TrustRoots         []PKISecurityTrustRootDescriptor
	RevokedIdentityIDs []string
	RevokedSerials     []string
}

// PKISecurityTrustRootDescriptor binds every public trust-root field carried
// outside PKISignedSecuritySnapshot to its canonical signature. The PEM bytes
// are represented by FingerprintSHA256 so a same-generation substitution
// cannot be accepted without invalidating the snapshot signature.
type PKISecurityTrustRootDescriptor struct {
	AuthorityID       string    `json:"authority_id"`
	Generation        int64     `json:"generation"`
	Status            string    `json:"status"`
	FingerprintSHA256 string    `json:"fingerprint_sha256"`
	NotBefore         time.Time `json:"not_before"`
	NotAfter          time.Time `json:"not_after"`
}

type PKISignedSecuritySnapshot struct {
	PKIUnsignedSecuritySnapshot
	SignerGeneration int64
	Signature        []byte
}

type PKIRevocationRequest struct {
	IdentityID           string
	Reason               string
	Source               string
	OperatorID           string
	ConfirmationDigest   string
	ConfirmationAction   string
	ConfirmationTargetID string
}

type PKIRevocationFacts struct {
	PKIDomainID            string
	PKIEpoch               int64
	PreviousRevision       int64
	SecurityRevision       int64
	IdentityID             string
	IdentityKind           string
	RevokedSerials         []string
	RevokedIdentityIDs     []string
	AllRevokedSerials      []string
	ActiveTrustGenerations []int64
}

type PKIRevocationCommit struct {
	Facts                 PKIRevocationFacts
	Snapshot              PKISignedSecuritySnapshot
	IdentityRevoked       bool
	CertificatesRevoked   int
	ControlTokenDisabled  bool
	ControlSessionTargets []string
	RelaySessionTargets   []string
	Lease                 PKILeaseGrant
	Event                 PKIAuditEvent
	ConvergenceJobID      string
}

type PKIRevocationMutation struct {
	Request PKIRevocationRequest
	Lease   PKILeaseGrant
}

// PKIRevocationRepository owns one transaction which locks the identity,
// revokes every active certificate, disables the control token, increments the
// security revision, invokes buildSnapshot with the locked facts, persists the
// signed snapshot/event, and records session-close intents. It must compare
// mutation.Lease against the canonical live domain/epoch/instance/term and
// deadline at commit in that same transaction.
type PKIRevocationRepository interface {
	RevokePKIIdentityAtomically(
		context.Context,
		PKIRevocationMutation,
		func(context.Context, PKIRevocationFacts) (PKISignedSecuritySnapshot, error),
	) (PKIRevocationCommit, error)
	RecordPKIRevocationConvergence(context.Context, PKIRevocationCommit, error) error
}

type PKISecuritySnapshotSigner interface {
	SignPKISecuritySnapshot(context.Context, PKIUnsignedSecuritySnapshot) (PKISignedSecuritySnapshot, error)
}

type PKISecuritySnapshotPublisher interface {
	PublishPKISecuritySnapshot(context.Context, PKISignedSecuritySnapshot, []string) error
}

type PKIRevokedSessionCloser interface {
	CloseRevokedPKISessions(context.Context, PKIRevocationCommit) error
}

type PKIRevocationService struct {
	repository  PKIRevocationRepository
	signer      PKISecuritySnapshotSigner
	publisher   PKISecuritySnapshotPublisher
	closer      PKIRevokedSessionCloser
	lease       PKILeaseGate
	clock       func() time.Time
	convergence time.Duration
}

type PKIRevocationServiceOptions struct {
	Repository  PKIRevocationRepository
	Signer      PKISecuritySnapshotSigner
	Publisher   PKISecuritySnapshotPublisher
	Closer      PKIRevokedSessionCloser
	Lease       PKILeaseGate
	Clock       func() time.Time
	Convergence time.Duration
}

func NewPKIRevocationService(options PKIRevocationServiceOptions) (*PKIRevocationService, error) {
	if options.Repository == nil || options.Signer == nil || options.Publisher == nil || options.Closer == nil || options.Lease == nil {
		return nil, fmt.Errorf("%w: revocation dependencies are required", ErrPKILifecycleInvalid)
	}
	if options.Clock == nil {
		options.Clock = time.Now
	}
	if options.Convergence == 0 {
		options.Convergence = PKIOnlineRevocationConvergence
	}
	if options.Convergence <= 0 || options.Convergence > PKIOnlineRevocationConvergence {
		return nil, fmt.Errorf("%w: online revocation convergence must be at most five seconds", ErrPKILifecycleInvalid)
	}
	return &PKIRevocationService{
		repository: options.Repository, signer: options.Signer, publisher: options.Publisher,
		closer: options.Closer, lease: options.Lease, clock: options.Clock, convergence: options.Convergence,
	}, nil
}

func (s *PKIRevocationService) Revoke(ctx context.Context, request PKIRevocationRequest) (PKIRevocationCommit, error) {
	commit, err := s.commitWithRepository(ctx, request, s.repository)
	if err != nil {
		return PKIRevocationCommit{}, err
	}
	return commit, s.CompleteCommittedRevocation(commit)
}

// CommitWithRepository is used when a surrounding configuration transaction
// must include the canonical revocation. Convergence remains a post-commit
// operation and is completed through CompleteCommittedRevocation.
func (s *PKIRevocationService) CommitWithRepository(
	ctx context.Context,
	request PKIRevocationRequest,
	repository PKIRevocationRepository,
) (PKIRevocationCommit, error) {
	if repository == nil {
		return PKIRevocationCommit{}, fmt.Errorf("%w: revocation repository is required", ErrPKILifecycleInvalid)
	}
	return s.commitWithRepository(ctx, request, repository)
}

func (s *PKIRevocationService) commitWithRepository(
	ctx context.Context,
	request PKIRevocationRequest,
	repository PKIRevocationRepository,
) (PKIRevocationCommit, error) {
	request.IdentityID = strings.TrimSpace(request.IdentityID)
	request.Reason = strings.TrimSpace(request.Reason)
	request.Source = strings.TrimSpace(request.Source)
	request.ConfirmationDigest = strings.TrimSpace(request.ConfirmationDigest)
	request.ConfirmationAction = strings.TrimSpace(request.ConfirmationAction)
	request.ConfirmationTargetID = strings.TrimSpace(request.ConfirmationTargetID)
	if request.IdentityID == "" || request.Reason == "" || request.Source == "" {
		return PKIRevocationCommit{}, fmt.Errorf("%w: identity, source, and reason are required", ErrPKILifecycleInvalid)
	}
	if request.Source == "panel" && (request.OperatorID != "panel" || request.ConfirmationDigest == "" ||
		request.ConfirmationAction != "revoke" || request.ConfirmationTargetID != request.IdentityID) {
		return PKIRevocationCommit{}, fmt.Errorf("%w: panel revocation confirmation is incomplete", ErrInvalidArgument)
	}
	before, err := s.lease.RequirePKILease(ctx)
	if err != nil {
		return PKIRevocationCommit{}, err
	}
	if err := validatePKIMutationLeaseFence(before); err != nil {
		return PKIRevocationCommit{}, err
	}
	mutation := PKIRevocationMutation{Request: request, Lease: before}
	commit, err := repository.RevokePKIIdentityAtomically(ctx, mutation, func(signCtx context.Context, facts PKIRevocationFacts) (PKISignedSecuritySnapshot, error) {
		if err := validatePKIRevocationFacts(request.IdentityID, facts); err != nil {
			return PKISignedSecuritySnapshot{}, err
		}
		unsigned := PKIUnsignedSecuritySnapshot{
			PKIDomainID: facts.PKIDomainID,
			Version: PKISecuritySnapshotVersion{
				Version: PKISecurityVersion{PKIEpoch: facts.PKIEpoch, SecurityRevision: facts.SecurityRevision},
				Full:    true,
			},
			IssuedAt: s.clock().UTC(), TrustGenerations: slices.Clone(facts.ActiveTrustGenerations),
			RevokedIdentityIDs: slices.Clone(facts.RevokedIdentityIDs), RevokedSerials: slices.Clone(facts.AllRevokedSerials),
		}
		snapshot, signErr := s.signer.SignPKISecuritySnapshot(signCtx, unsigned)
		if signErr != nil {
			return PKISignedSecuritySnapshot{}, signErr
		}
		if err := validateSignedPKISecuritySnapshot(snapshot, unsigned); err != nil {
			return PKISignedSecuritySnapshot{}, err
		}
		return snapshot, nil
	})
	if err != nil {
		return PKIRevocationCommit{}, err
	}
	if err := validatePKIRevocationCommit(request, before, commit); err != nil {
		return commit, err
	}
	return commit, nil
}

func (s *PKIRevocationService) CompleteCommittedRevocation(commit PKIRevocationCommit) error {
	convergeCtx, cancel := context.WithTimeout(context.Background(), s.convergence)
	convergenceErr := s.converge(convergeCtx, commit)
	cancel()
	recordCtx, recordCancel := context.WithTimeout(context.Background(), s.convergence)
	recordErr := s.repository.RecordPKIRevocationConvergence(recordCtx, commit, convergenceErr)
	recordCancel()
	return errors.Join(convergenceErr, recordErr)
}

func (s *PKIRevocationService) converge(ctx context.Context, commit PKIRevocationCommit) error {
	errorsByConsumer := make(chan error, 2)
	go func() {
		errorsByConsumer <- s.publisher.PublishPKISecuritySnapshot(ctx, commit.Snapshot, commit.ControlSessionTargets)
	}()
	go func() {
		errorsByConsumer <- s.closer.CloseRevokedPKISessions(ctx, commit)
	}()
	var convergenceErr error
	for completed := 0; completed < 2; completed++ {
		select {
		case consumerErr := <-errorsByConsumer:
			convergenceErr = errors.Join(convergenceErr, consumerErr)
		case <-ctx.Done():
			return errors.Join(convergenceErr, ctx.Err())
		}
	}
	return convergenceErr
}

// ReconcilePendingConvergence retries durable revoke jobs using the latest
// full canonical snapshot. A newer full snapshot safely subsumes every older
// revocation and avoids depending on the request that originally committed it.
func (s *PKIRevocationService) ReconcilePendingConvergence(ctx context.Context, state storage.PKICanonicalState) error {
	if state.Settings == nil || state.SecuritySnapshot == nil {
		return nil
	}
	var persisted storage.PKISecuritySnapshot
	if err := json.Unmarshal([]byte(state.SecuritySnapshot.SnapshotJSON), &persisted); err != nil || !persisted.Full {
		return fmt.Errorf("%w: durable convergence requires a full security snapshot", ErrPKILifecycleInvalid)
	}
	signed := signedPKISecuritySnapshotFromStorage(persisted)
	identities := make(map[string]storage.PKIIdentityRow, len(state.Identities))
	for _, identity := range state.Identities {
		identities[identity.ID] = identity
	}
	now := s.clock().UTC()
	var result error
	for _, job := range state.LifecycleJobs {
		if job.Kind != "revoke" || (job.State != storage.PKILifecycleJobStatePending && job.State != storage.PKILifecycleJobStateRunning) ||
			(job.NextAttemptAt != nil && job.NextAttemptAt.After(now)) {
			continue
		}
		identity, found := identities[job.TargetID]
		if !found || identity.State != storage.PKIIdentityStateRevoked {
			result = errors.Join(result, fmt.Errorf("%w: revoke convergence identity is missing", ErrPKILifecycleInvalid))
			continue
		}
		serials := make([]string, 0)
		for _, certificate := range state.Certificates {
			if certificate.IdentityID == identity.ID && certificate.Status == storage.PKICertificateStatusRevoked {
				serials = append(serials, certificate.SerialHex)
			}
		}
		controlTargets, relayTargets := recoveredPKIRevocationSessionTargets(identity)
		commit := PKIRevocationCommit{
			Facts: PKIRevocationFacts{
				PKIDomainID: state.Settings.PKIDomainID, PKIEpoch: state.Settings.PKIEpoch,
				SecurityRevision: persisted.SecurityRevision, IdentityID: identity.ID, IdentityKind: identity.Kind, RevokedSerials: serials,
			},
			Snapshot: signed, IdentityRevoked: true, CertificatesRevoked: len(serials),
			ControlSessionTargets: controlTargets, RelaySessionTargets: relayTargets,
			ConvergenceJobID: job.ID,
		}
		attemptCtx, cancel := context.WithTimeout(ctx, s.convergence)
		convergenceErr := s.converge(attemptCtx, commit)
		cancel()
		recordErr := s.repository.RecordPKIRevocationConvergence(ctx, commit, convergenceErr)
		result = errors.Join(result, convergenceErr, recordErr)
	}
	return result
}

func recoveredPKIRevocationSessionTargets(identity storage.PKIIdentityRow) (control, relay []string) {
	agentID := strings.TrimSpace(identity.AgentID)
	if agentID == "" {
		return nil, nil
	}
	relay = []string{agentID}
	if identity.Kind == storage.PKIIdentityKindAgent {
		control = []string{agentID}
	}
	return control, relay
}

func signedPKISecuritySnapshotFromStorage(snapshot storage.PKISecuritySnapshot) PKISignedSecuritySnapshot {
	trustGenerations := make([]int64, 0, len(snapshot.TrustRoots))
	trustRoots := make([]PKISecurityTrustRootDescriptor, 0, len(snapshot.TrustRoots))
	for _, root := range snapshot.TrustRoots {
		trustGenerations = append(trustGenerations, root.Generation)
		trustRoots = append(trustRoots, PKISecurityTrustRootDescriptor{
			AuthorityID: root.AuthorityID, Generation: root.Generation, Status: root.Status,
			FingerprintSHA256: root.FingerprintSHA256, NotBefore: root.NotBefore, NotAfter: root.NotAfter,
		})
	}
	return PKISignedSecuritySnapshot{
		PKIUnsignedSecuritySnapshot: PKIUnsignedSecuritySnapshot{
			PKIDomainID: snapshot.PKIDomainID,
			Version:     PKISecuritySnapshotVersion{Version: PKISecurityVersion{PKIEpoch: snapshot.PKIEpoch, SecurityRevision: snapshot.SecurityRevision}, Full: snapshot.Full},
			IssuedAt:    snapshot.IssuedAt, TrustGenerations: trustGenerations, TrustRoots: trustRoots,
			RevokedIdentityIDs: slices.Clone(snapshot.RevokedIdentityIDs), RevokedSerials: slices.Clone(snapshot.RevokedSerials),
		},
		SignerGeneration: snapshot.SignerGeneration, Signature: slices.Clone(snapshot.Signature),
	}
}

func validatePKIRevocationFacts(identityID string, facts PKIRevocationFacts) error {
	if strings.TrimSpace(facts.PKIDomainID) == "" || facts.PKIEpoch < 0 || facts.PreviousRevision < 0 ||
		facts.PreviousRevision == int64(^uint64(0)>>1) || facts.SecurityRevision != facts.PreviousRevision+1 ||
		facts.IdentityID != identityID || (facts.IdentityKind != storage.PKIIdentityKindAgent && facts.IdentityKind != storage.PKIIdentityKindListener) ||
		len(facts.RevokedIdentityIDs) == 0 ||
		len(facts.ActiveTrustGenerations) == 0 {
		return fmt.Errorf("%w: atomic revocation facts are invalid", ErrPKILifecycleInvalid)
	}
	if !slices.Contains(facts.RevokedIdentityIDs, identityID) {
		return fmt.Errorf("%w: full revocation facts omit the target identity", ErrPKILifecycleInvalid)
	}
	for _, serial := range facts.RevokedSerials {
		if strings.TrimSpace(serial) == "" {
			return fmt.Errorf("%w: revoked certificate serial is empty", ErrPKILifecycleInvalid)
		}
	}
	for _, generation := range facts.ActiveTrustGenerations {
		if generation <= 0 {
			return fmt.Errorf("%w: trust generation is invalid", ErrPKILifecycleInvalid)
		}
	}
	return nil
}

func validateSignedPKISecuritySnapshot(snapshot PKISignedSecuritySnapshot, expected PKIUnsignedSecuritySnapshot) error {
	if expected.IssuedAt.IsZero() || snapshot.PKIDomainID != expected.PKIDomainID || snapshot.Version != expected.Version ||
		!snapshot.IssuedAt.Equal(expected.IssuedAt) || snapshot.SignerGeneration <= 0 || len(snapshot.Signature) == 0 ||
		!slices.Equal(snapshot.RevokedIdentityIDs, expected.RevokedIdentityIDs) ||
		!slices.Equal(snapshot.RevokedSerials, expected.RevokedSerials) ||
		!slices.Equal(snapshot.TrustGenerations, expected.TrustGenerations) ||
		!validPKISecurityTrustRootDescriptors(snapshot.TrustGenerations, snapshot.TrustRoots) {
		return fmt.Errorf("%w: signed security snapshot does not match atomic facts", ErrPKILifecycleInvalid)
	}
	return nil
}

func validPKISecurityTrustRootDescriptors(generations []int64, roots []PKISecurityTrustRootDescriptor) bool {
	if len(generations) == 0 || len(roots) != len(generations) {
		return false
	}
	for index, generation := range generations {
		root := roots[index]
		if generation <= 0 || root.Generation != generation || strings.TrimSpace(root.AuthorityID) == "" ||
			strings.TrimSpace(root.Status) == "" || len(strings.TrimSpace(root.FingerprintSHA256)) != 64 ||
			root.NotBefore.IsZero() || !root.NotAfter.After(root.NotBefore) {
			return false
		}
	}
	return true
}

func validatePKIRevocationCommit(request PKIRevocationRequest, lease PKILeaseGrant, commit PKIRevocationCommit) error {
	if err := validatePKIRevocationFacts(request.IdentityID, commit.Facts); err != nil {
		return err
	}
	controlTokenStateValid := commit.Facts.IdentityKind == storage.PKIIdentityKindAgent && commit.ControlTokenDisabled ||
		commit.Facts.IdentityKind == storage.PKIIdentityKindListener && !commit.ControlTokenDisabled
	if !commit.IdentityRevoked || commit.CertificatesRevoked != len(commit.Facts.RevokedSerials) || !controlTokenStateValid ||
		!samePKILeaseAuthority(commit.Lease, lease) || !commit.Lease.LeaseDeadline.Equal(lease.LeaseDeadline) ||
		commit.Event.SecurityRevision != commit.Facts.SecurityRevision || commit.Event.ObjectID != request.IdentityID ||
		commit.Snapshot.PKIDomainID != commit.Facts.PKIDomainID ||
		commit.Snapshot.Version.Version != (PKISecurityVersion{PKIEpoch: commit.Facts.PKIEpoch, SecurityRevision: commit.Facts.SecurityRevision}) ||
		!slices.Contains(commit.Snapshot.RevokedIdentityIDs, request.IdentityID) ||
		!slices.Equal(commit.Snapshot.RevokedSerials, commit.Facts.AllRevokedSerials) || len(commit.Snapshot.Signature) == 0 {
		return fmt.Errorf("%w: atomic revocation commit is incomplete", ErrPKILifecycleInvalid)
	}
	if err := ValidatePKIAuditEvent(commit.Event); err != nil {
		return err
	}
	return nil
}

type PKIControlSyncTarget interface {
	CurrentPKISecurityVersion(context.Context) (PKISecurityVersion, error)
	VerifyPKISecuritySnapshot(context.Context, PKISignedSecuritySnapshot) error
	PersistPKISecuritySnapshot(context.Context, PKISignedSecuritySnapshot) error
	CloseRevokedRelaySessions(context.Context, PKISignedSecuritySnapshot) error
	ApplyOrdinaryControlRevision(context.Context, []byte) error
}

// ApplyRecoveredPKIControlSync always commits and enforces security state
// before ordinary revision data. Snapshot age is deliberately not a liveness
// gate; cryptographic trust and monotonic version are the gates.
func ApplyRecoveredPKIControlSync(
	ctx context.Context,
	target PKIControlSyncTarget,
	snapshot PKISignedSecuritySnapshot,
	ordinaryRevision []byte,
) error {
	if target == nil {
		return fmt.Errorf("%w: control sync target is required", ErrPKILifecycleInvalid)
	}
	current, err := target.CurrentPKISecurityVersion(ctx)
	if err != nil {
		return err
	}
	if err := ValidatePKISecuritySnapshot(current, snapshot.Version); err != nil {
		return err
	}
	if err := target.VerifyPKISecuritySnapshot(ctx, snapshot); err != nil {
		return err
	}
	if err := target.PersistPKISecuritySnapshot(ctx, snapshot); err != nil {
		return err
	}
	if err := target.CloseRevokedRelaySessions(ctx, snapshot); err != nil {
		return err
	}
	return target.ApplyOrdinaryControlRevision(ctx, ordinaryRevision)
}

func ValidateLastTrustedPKISnapshotForOfflineRelay(snapshot PKISignedSecuritySnapshot) error {
	if strings.TrimSpace(snapshot.PKIDomainID) == "" || snapshot.Version.Version.PKIEpoch < 0 ||
		snapshot.Version.Version.SecurityRevision < 0 || snapshot.IssuedAt.IsZero() || snapshot.SignerGeneration <= 0 ||
		len(snapshot.Signature) == 0 {
		return fmt.Errorf("%w: last trusted security snapshot is invalid", ErrPKILifecycleInvalid)
	}
	return nil
}
