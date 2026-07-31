package service

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

const PKIOnlineRevocationConvergence = 5 * time.Second

type PKIUnsignedSecuritySnapshot struct {
	PKIDomainID        string
	Version            PKISecuritySnapshotVersion
	IssuedAt           time.Time
	TrustGenerations   []int64
	RevokedIdentityIDs []string
	RevokedSerials     []string
}

type PKISignedSecuritySnapshot struct {
	PKIUnsignedSecuritySnapshot
	SignerGeneration int64
	Signature        []byte
}

type PKIRevocationRequest struct {
	IdentityID string
	Reason     string
	Source     string
	OperatorID string
}

type PKIRevocationFacts struct {
	PKIDomainID            string
	PKIEpoch               int64
	PreviousRevision       int64
	SecurityRevision       int64
	IdentityID             string
	RevokedSerials         []string
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
	Event                 PKIAuditEvent
}

// PKIRevocationRepository owns one transaction which locks the identity,
// revokes every active certificate, disables the control token, increments the
// security revision, invokes buildSnapshot with the locked facts, persists the
// signed snapshot/event, and records session-close intents.
type PKIRevocationRepository interface {
	RevokePKIIdentityAtomically(
		context.Context,
		PKIRevocationRequest,
		func(context.Context, PKIRevocationFacts) (PKISignedSecuritySnapshot, error),
	) (PKIRevocationCommit, error)
}

type PKISecuritySnapshotSigner interface {
	SignPKISecuritySnapshot(context.Context, PKIUnsignedSecuritySnapshot) (PKISignedSecuritySnapshot, error)
}

type PKISecuritySnapshotPublisher interface {
	PublishPKISecuritySnapshot(context.Context, PKISignedSecuritySnapshot) error
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
	request.IdentityID = strings.TrimSpace(request.IdentityID)
	request.Reason = strings.TrimSpace(request.Reason)
	request.Source = strings.TrimSpace(request.Source)
	if request.IdentityID == "" || request.Reason == "" || request.Source == "" {
		return PKIRevocationCommit{}, fmt.Errorf("%w: identity, source, and reason are required", ErrPKILifecycleInvalid)
	}
	before, err := s.lease.RequirePKILease(ctx)
	if err != nil {
		return PKIRevocationCommit{}, err
	}
	commit, err := s.repository.RevokePKIIdentityAtomically(ctx, request, func(signCtx context.Context, facts PKIRevocationFacts) (PKISignedSecuritySnapshot, error) {
		if err := validatePKIRevocationFacts(request.IdentityID, facts); err != nil {
			return PKISignedSecuritySnapshot{}, err
		}
		unsigned := PKIUnsignedSecuritySnapshot{
			PKIDomainID: facts.PKIDomainID,
			Version: PKISecuritySnapshotVersion{
				Version: PKISecurityVersion{PKIEpoch: facts.PKIEpoch, SecurityRevision: facts.SecurityRevision},
				Full:    false,
			},
			IssuedAt: s.clock().UTC(), TrustGenerations: slices.Clone(facts.ActiveTrustGenerations),
			RevokedIdentityIDs: []string{facts.IdentityID}, RevokedSerials: slices.Clone(facts.RevokedSerials),
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
	if err := validatePKIRevocationCommit(request, commit); err != nil {
		return commit, err
	}
	after, leaseErr := s.lease.RequirePKILease(ctx)
	if leaseErr != nil || !samePKILeaseAuthority(before, after) {
		if leaseErr == nil {
			leaseErr = ErrPKILeaseNotHeld
		}
		return commit, leaseErr
	}
	convergeCtx, cancel := context.WithTimeout(ctx, s.convergence)
	defer cancel()
	errorsByConsumer := make(chan error, 2)
	go func() {
		errorsByConsumer <- s.publisher.PublishPKISecuritySnapshot(convergeCtx, commit.Snapshot)
	}()
	go func() {
		errorsByConsumer <- s.closer.CloseRevokedPKISessions(convergeCtx, commit)
	}()
	var convergenceErr error
	for completed := 0; completed < 2; completed++ {
		select {
		case consumerErr := <-errorsByConsumer:
			convergenceErr = errors.Join(convergenceErr, consumerErr)
		case <-convergeCtx.Done():
			return commit, errors.Join(convergenceErr, convergeCtx.Err())
		}
	}
	return commit, convergenceErr
}

func validatePKIRevocationFacts(identityID string, facts PKIRevocationFacts) error {
	if strings.TrimSpace(facts.PKIDomainID) == "" || facts.PKIEpoch < 0 || facts.PreviousRevision < 0 ||
		facts.PreviousRevision == int64(^uint64(0)>>1) || facts.SecurityRevision != facts.PreviousRevision+1 ||
		facts.IdentityID != identityID || len(facts.RevokedSerials) == 0 || len(facts.ActiveTrustGenerations) == 0 {
		return fmt.Errorf("%w: atomic revocation facts are invalid", ErrPKILifecycleInvalid)
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
		!slices.Equal(snapshot.TrustGenerations, expected.TrustGenerations) {
		return fmt.Errorf("%w: signed security snapshot does not match atomic facts", ErrPKILifecycleInvalid)
	}
	return nil
}

func validatePKIRevocationCommit(request PKIRevocationRequest, commit PKIRevocationCommit) error {
	if err := validatePKIRevocationFacts(request.IdentityID, commit.Facts); err != nil {
		return err
	}
	if !commit.IdentityRevoked || commit.CertificatesRevoked <= 0 || !commit.ControlTokenDisabled ||
		commit.Event.SecurityRevision != commit.Facts.SecurityRevision || commit.Event.ObjectID != request.IdentityID ||
		commit.Snapshot.PKIDomainID != commit.Facts.PKIDomainID ||
		commit.Snapshot.Version.Version != (PKISecurityVersion{PKIEpoch: commit.Facts.PKIEpoch, SecurityRevision: commit.Facts.SecurityRevision}) ||
		!slices.Contains(commit.Snapshot.RevokedIdentityIDs, request.IdentityID) ||
		!slices.Equal(commit.Snapshot.RevokedSerials, commit.Facts.RevokedSerials) || len(commit.Snapshot.Signature) == 0 {
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
