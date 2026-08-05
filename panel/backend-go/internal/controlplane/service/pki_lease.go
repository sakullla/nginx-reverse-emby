package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
)

const (
	PKILeaseStateHeld         = "held"
	PKILeaseStateRelinquished = "relinquished"

	defaultPKILeaseTTL           = 30 * time.Second
	defaultPKILeaseRenewInterval = 10 * time.Second
	pkiLeaseTermBytes            = 32
)

var (
	ErrPKILeaseInvalid = errors.New("invalid PKI lease configuration")
	ErrPKILeaseNotHeld = errors.New("PKI lease is not held by this instance")
	ErrPKIEpochStale   = errors.New("stale PKI security snapshot epoch or revision")
)

// PKILeaseSnapshot is the shared, canonical lease view returned by the
// repository. PKIEpoch and PKIDomainID come from the settings singleton in the
// same read transaction as the lease row.
type PKILeaseSnapshot struct {
	Exists        bool
	PKIDomainID   string
	PKIEpoch      int64
	InstanceID    string
	LeaseTerm     string
	LeaseDeadline time.Time
	State         string
}

// PKILeaseRepository owns the database compare-and-swap operations. Each
// mutation must read pki_settings and mutate the singleton lease in one write
// transaction. TryAcquire may succeed only for an absent, relinquished,
// expired, or same-instance lease and must persist the supplied random term.
// Renew and relinquish may succeed only for the same live instance, exact
// acquisition term, and epoch; an older term must never affect a reacquired
// lease, even when instance ID and epoch are unchanged.
type PKILeaseRepository interface {
	ReadPKILease(context.Context) (PKILeaseSnapshot, error)
	TryAcquirePKILease(context.Context, string, string, time.Time, time.Time) (PKILeaseSnapshot, bool, error)
	RenewPKILease(context.Context, string, string, int64, time.Time, time.Time) (PKILeaseSnapshot, bool, error)
	RelinquishPKILease(context.Context, string, string, int64, time.Time) (bool, error)
}

type PKILeaseServiceOptions struct {
	Repository    PKILeaseRepository
	InstanceID    string
	Clock         func() time.Time
	Random        io.Reader
	TTL           time.Duration
	RenewInterval time.Duration
}

type PKILeaseService struct {
	repository    PKILeaseRepository
	instanceID    string
	clock         func() time.Time
	random        io.Reader
	ttl           time.Duration
	renewInterval time.Duration

	transitionMutex sync.Mutex
	mutex           sync.Mutex
	grant           PKILeaseGrant
	held            bool

	randomMutex sync.Mutex
}

// PKILeaseGrant is returned only after a fresh canonical read confirms that
// this process is still the live holder. Consumers use it to fence sensitive
// operations to a domain and epoch.
type PKILeaseGrant struct {
	PKIDomainID   string
	PKIEpoch      int64
	InstanceID    string
	LeaseTerm     string
	LeaseDeadline time.Time
}

type PKILeaseGate interface {
	RequirePKILease(context.Context) (PKILeaseGrant, error)
}

func NewPKILeaseService(options PKILeaseServiceOptions) (*PKILeaseService, error) {
	if options.Repository == nil {
		return nil, fmt.Errorf("%w: repository is required", ErrPKILeaseInvalid)
	}
	options.InstanceID = strings.TrimSpace(options.InstanceID)
	if options.InstanceID == "" {
		return nil, fmt.Errorf("%w: instance ID is required", ErrPKILeaseInvalid)
	}
	if options.Clock == nil {
		options.Clock = time.Now
	}
	if options.Random == nil {
		options.Random = rand.Reader
	}
	if options.TTL == 0 {
		options.TTL = defaultPKILeaseTTL
	}
	if options.RenewInterval == 0 {
		options.RenewInterval = defaultPKILeaseRenewInterval
	}
	if options.TTL <= 0 || options.RenewInterval <= 0 || options.RenewInterval >= options.TTL {
		return nil, fmt.Errorf("%w: renewal interval must be positive and shorter than TTL", ErrPKILeaseInvalid)
	}
	return &PKILeaseService{
		repository: options.Repository, instanceID: options.InstanceID, clock: options.Clock,
		random: options.Random, ttl: options.TTL, renewInterval: options.RenewInterval,
	}, nil
}

func (s *PKILeaseService) Acquire(ctx context.Context) (PKILeaseGrant, error) {
	s.transitionMutex.Lock()
	defer s.transitionMutex.Unlock()

	leaseTerm, err := s.newLeaseTerm()
	if err != nil {
		s.clearGrant()
		return PKILeaseGrant{}, err
	}
	now, err := s.now()
	if err != nil {
		s.clearGrant()
		return PKILeaseGrant{}, err
	}
	snapshot, acquired, err := s.repository.TryAcquirePKILease(ctx, s.instanceID, leaseTerm, now, now.Add(s.ttl))
	if err != nil {
		s.clearGrant()
		return PKILeaseGrant{}, err
	}
	if !acquired {
		s.clearGrant()
		return PKILeaseGrant{}, ErrPKILeaseNotHeld
	}
	checkedAt, err := s.now()
	if err != nil {
		s.clearGrant()
		return PKILeaseGrant{}, err
	}
	grant, err := s.validateOwnedSnapshot(snapshot, leaseTerm, checkedAt)
	if err != nil {
		s.clearGrant()
		return PKILeaseGrant{}, err
	}
	s.setGrant(grant)
	return grant, nil
}

// Bootstrap atomically establishes initial canonical settings and the first
// held lease through initialize. The callback must persist the supplied grant
// in the same transaction as the settings singleton. No CA material may be
// generated or opened until this method returns successfully.
func (s *PKILeaseService) Bootstrap(
	ctx context.Context,
	pkiDomainID string,
	pkiEpoch int64,
	initialize func(PKILeaseGrant) error,
) (PKILeaseGrant, error) {
	s.transitionMutex.Lock()
	defer s.transitionMutex.Unlock()
	pkiDomainID = strings.TrimSpace(pkiDomainID)
	if pkiDomainID == "" || pkiEpoch < 0 || initialize == nil {
		return PKILeaseGrant{}, fmt.Errorf("%w: bootstrap lease fields are incomplete", ErrPKILeaseInvalid)
	}
	leaseTerm, err := s.newLeaseTerm()
	if err != nil {
		s.clearGrant()
		return PKILeaseGrant{}, err
	}
	now, err := s.now()
	if err != nil {
		s.clearGrant()
		return PKILeaseGrant{}, err
	}
	grant := PKILeaseGrant{
		PKIDomainID: pkiDomainID, PKIEpoch: pkiEpoch, InstanceID: s.instanceID,
		LeaseTerm: leaseTerm, LeaseDeadline: now.Add(s.ttl),
	}
	if err := initialize(grant); err != nil {
		s.clearGrant()
		return PKILeaseGrant{}, err
	}
	snapshot, err := s.repository.ReadPKILease(ctx)
	if err != nil {
		s.clearGrant()
		return PKILeaseGrant{}, err
	}
	checkedAt, err := s.now()
	if err != nil {
		s.clearGrant()
		return PKILeaseGrant{}, err
	}
	validated, err := s.validateOwnedSnapshot(snapshot, leaseTerm, checkedAt)
	if err != nil || !samePKILeaseAuthority(validated, grant) {
		s.clearGrant()
		return PKILeaseGrant{}, ErrPKILeaseNotHeld
	}
	s.setGrant(validated)
	return validated, nil
}

func (s *PKILeaseService) Renew(ctx context.Context) (PKILeaseGrant, error) {
	s.transitionMutex.Lock()
	defer s.transitionMutex.Unlock()

	current, ok := s.localGrant()
	if !ok {
		return PKILeaseGrant{}, ErrPKILeaseNotHeld
	}
	now, err := s.now()
	if err != nil {
		s.clearGrant()
		return PKILeaseGrant{}, err
	}
	if !current.LeaseDeadline.After(now) {
		s.clearGrant()
		return PKILeaseGrant{}, ErrPKILeaseNotHeld
	}
	snapshot, renewed, err := s.repository.RenewPKILease(ctx, s.instanceID, current.LeaseTerm, current.PKIEpoch, now, now.Add(s.ttl))
	if err != nil {
		s.clearGrant()
		return PKILeaseGrant{}, err
	}
	if !renewed {
		s.clearGrant()
		return PKILeaseGrant{}, ErrPKILeaseNotHeld
	}
	checkedAt, err := s.now()
	if err != nil {
		s.clearGrant()
		return PKILeaseGrant{}, err
	}
	grant, err := s.validateOwnedSnapshot(snapshot, current.LeaseTerm, checkedAt)
	if err != nil {
		s.clearGrant()
		return PKILeaseGrant{}, err
	}
	s.setGrant(grant)
	return grant, nil
}

func (s *PKILeaseService) RequirePKILease(ctx context.Context) (PKILeaseGrant, error) {
	s.transitionMutex.Lock()
	defer s.transitionMutex.Unlock()

	local, ok := s.localGrant()
	if !ok {
		return PKILeaseGrant{}, ErrPKILeaseNotHeld
	}
	now, err := s.now()
	if err != nil {
		s.clearGrant()
		return PKILeaseGrant{}, err
	}
	if !local.LeaseDeadline.After(now) {
		s.clearGrant()
		return PKILeaseGrant{}, ErrPKILeaseNotHeld
	}
	snapshot, err := s.repository.ReadPKILease(ctx)
	if err != nil {
		s.clearGrant()
		return PKILeaseGrant{}, err
	}
	checkedAt, err := s.now()
	if err != nil {
		s.clearGrant()
		return PKILeaseGrant{}, err
	}
	grant, err := s.validateOwnedSnapshot(snapshot, local.LeaseTerm, checkedAt)
	if err != nil || !samePKILeaseAuthority(grant, local) {
		s.clearGrant()
		return PKILeaseGrant{}, ErrPKILeaseNotHeld
	}
	s.setGrant(grant)
	return grant, nil
}

func (s *PKILeaseService) Relinquish(ctx context.Context) error {
	s.transitionMutex.Lock()
	defer s.transitionMutex.Unlock()

	current, ok := s.localGrant()
	if !ok {
		return ErrPKILeaseNotHeld
	}
	now, err := s.now()
	if err != nil {
		s.clearGrant()
		return err
	}
	relinquished, err := s.repository.RelinquishPKILease(ctx, s.instanceID, current.LeaseTerm, current.PKIEpoch, now)
	s.clearGrant()
	if err != nil {
		return err
	}
	if !relinquished {
		return ErrPKILeaseNotHeld
	}
	return nil
}

// Maintain acquires immediately and renews on the configured cadence. Any
// failed renewal terminates the loop with capabilities already failed closed.
func (s *PKILeaseService) Maintain(ctx context.Context) error {
	if _, held := s.localGrant(); held {
		if _, err := s.RequirePKILease(ctx); err != nil {
			return err
		}
	} else if _, err := s.Acquire(ctx); err != nil {
		return err
	}
	ticker := time.NewTicker(s.renewInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			releaseCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = s.Relinquish(releaseCtx)
			cancel()
			return ctx.Err()
		case <-ticker.C:
			if _, err := s.Renew(ctx); err != nil {
				return err
			}
		}
	}
}

func (s *PKILeaseService) validateOwnedSnapshot(snapshot PKILeaseSnapshot, expectedTerm string, now time.Time) (PKILeaseGrant, error) {
	if !snapshot.Exists || strings.TrimSpace(snapshot.PKIDomainID) == "" || snapshot.PKIEpoch < 0 ||
		strings.TrimSpace(snapshot.InstanceID) != s.instanceID || snapshot.State != PKILeaseStateHeld ||
		!validPKILeaseTerm(snapshot.LeaseTerm) || snapshot.LeaseTerm != expectedTerm || !snapshot.LeaseDeadline.After(now) {
		return PKILeaseGrant{}, ErrPKILeaseNotHeld
	}
	return PKILeaseGrant{
		PKIDomainID: snapshot.PKIDomainID, PKIEpoch: snapshot.PKIEpoch,
		InstanceID: snapshot.InstanceID, LeaseTerm: snapshot.LeaseTerm, LeaseDeadline: snapshot.LeaseDeadline,
	}, nil
}

func (s *PKILeaseService) now() (time.Time, error) {
	now := s.clock().UTC()
	if now.IsZero() {
		return time.Time{}, fmt.Errorf("%w: clock returned zero", ErrPKILeaseInvalid)
	}
	return now, nil
}

func (s *PKILeaseService) newLeaseTerm() (string, error) {
	termBytes := make([]byte, pkiLeaseTermBytes)
	s.randomMutex.Lock()
	_, err := io.ReadFull(s.random, termBytes)
	s.randomMutex.Unlock()
	if err != nil {
		clear(termBytes)
		return "", fmt.Errorf("generate PKI lease term: %w", err)
	}
	leaseTerm := hex.EncodeToString(termBytes)
	clear(termBytes)
	return leaseTerm, nil
}

func validPKILeaseTerm(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == pkiLeaseTermBytes
}

func samePKILeaseAuthority(left, right PKILeaseGrant) bool {
	return left.PKIDomainID == right.PKIDomainID && left.PKIEpoch == right.PKIEpoch && left.InstanceID == right.InstanceID &&
		validPKILeaseTerm(left.LeaseTerm) && left.LeaseTerm == right.LeaseTerm
}

func (s *PKILeaseService) localGrant() (PKILeaseGrant, bool) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return s.grant, s.held
}

func (s *PKILeaseService) setGrant(grant PKILeaseGrant) {
	s.mutex.Lock()
	s.grant = grant
	s.held = true
	s.mutex.Unlock()
}

func (s *PKILeaseService) clearGrant() {
	s.mutex.Lock()
	s.grant = PKILeaseGrant{}
	s.held = false
	s.mutex.Unlock()
}

type PKISecurityVersion struct {
	PKIEpoch         int64 `json:"pki_epoch"`
	SecurityRevision int64 `json:"security_revision"`
}

type PKISecuritySnapshotVersion struct {
	Version PKISecurityVersion `json:"version"`
	Full    bool               `json:"full"`
}

func ComparePKISecurityVersion(left, right PKISecurityVersion) int {
	if left.PKIEpoch < right.PKIEpoch {
		return -1
	}
	if left.PKIEpoch > right.PKIEpoch {
		return 1
	}
	if left.SecurityRevision < right.SecurityRevision {
		return -1
	}
	if left.SecurityRevision > right.SecurityRevision {
		return 1
	}
	return 0
}

// ValidatePKISecuritySnapshot enforces lexicographic fencing. A higher epoch
// always wins over every older revision, but its first accepted message must be
// a complete snapshot. Same-epoch equal revisions are accepted idempotently.
func ValidatePKISecuritySnapshot(current PKISecurityVersion, incoming PKISecuritySnapshotVersion) error {
	if current.PKIEpoch < 0 || current.SecurityRevision < 0 || incoming.Version.PKIEpoch < 0 || incoming.Version.SecurityRevision < 0 {
		return fmt.Errorf("%w: version components must be non-negative", ErrPKIEpochStale)
	}
	comparison := ComparePKISecurityVersion(incoming.Version, current)
	if comparison < 0 {
		return ErrPKIEpochStale
	}
	if incoming.Version.PKIEpoch > current.PKIEpoch && !incoming.Full {
		return fmt.Errorf("%w: a higher epoch requires a full snapshot", ErrPKIEpochStale)
	}
	return nil
}

func NextForcedPKISecurityVersion(current, backup PKISecurityVersion) (PKISecurityVersion, error) {
	if current.PKIEpoch < 0 || current.SecurityRevision < 0 || backup.PKIEpoch < 0 || backup.SecurityRevision < 0 {
		return PKISecurityVersion{}, fmt.Errorf("%w: version components must be non-negative", ErrPKIEpochStale)
	}
	highest := current.PKIEpoch
	if backup.PKIEpoch > highest {
		highest = backup.PKIEpoch
	}
	if highest == int64(^uint64(0)>>1) {
		return PKISecurityVersion{}, fmt.Errorf("%w: epoch cannot be incremented", ErrPKIEpochStale)
	}
	return PKISecurityVersion{PKIEpoch: highest + 1, SecurityRevision: 0}, nil
}
