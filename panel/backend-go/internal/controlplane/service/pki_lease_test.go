package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestPKILeaseConcurrentAcquireAndTakeoverFailClosed(t *testing.T) {
	clock := &pkiLeaseTestClock{now: time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)}
	repository := &pkiLeaseTestRepository{domainID: "domain-1", epoch: 7}
	first := newPKILeaseTestService(t, repository, clock, "instance-a")
	second := newPKILeaseTestService(t, repository, clock, "instance-b")

	start := make(chan struct{})
	results := make(chan string, 2)
	var workers sync.WaitGroup
	for _, candidate := range []struct {
		id      string
		service *PKILeaseService
	}{{"instance-a", first}, {"instance-b", second}} {
		candidate := candidate
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			if _, err := candidate.service.Acquire(context.Background()); err == nil {
				results <- candidate.id
			} else if !errors.Is(err, ErrPKILeaseNotHeld) {
				t.Errorf("Acquire(%s) error = %v", candidate.id, err)
			}
		}()
	}
	close(start)
	workers.Wait()
	close(results)
	var winner string
	for instanceID := range results {
		if winner != "" {
			t.Fatalf("multiple lease winners: %q and %q", winner, instanceID)
		}
		winner = instanceID
	}
	if winner == "" {
		t.Fatal("concurrent lease acquisition had no winner")
	}
	holder := first
	standby := second
	if winner == "instance-b" {
		holder, standby = second, first
	}
	if grant, err := holder.RequirePKILease(t.Context()); err != nil || grant.PKIEpoch != 7 {
		t.Fatalf("RequirePKILease(holder) = %+v, %v", grant, err)
	}
	if _, err := standby.RequirePKILease(t.Context()); !errors.Is(err, ErrPKILeaseNotHeld) {
		t.Fatalf("RequirePKILease(standby) error = %v, want ErrPKILeaseNotHeld", err)
	}

	clock.Advance(31 * time.Second)
	if _, err := standby.Acquire(t.Context()); err != nil {
		t.Fatalf("Acquire(expired takeover) error = %v", err)
	}
	if _, err := holder.RequirePKILease(t.Context()); !errors.Is(err, ErrPKILeaseNotHeld) {
		t.Fatalf("old holder after takeover error = %v, want ErrPKILeaseNotHeld", err)
	}
	if _, err := holder.Renew(t.Context()); !errors.Is(err, ErrPKILeaseNotHeld) {
		t.Fatalf("old holder Renew() error = %v, want ErrPKILeaseNotHeld", err)
	}
}

func TestPKILeaseRenewRelinquishAndRepositoryFailure(t *testing.T) {
	clock := &pkiLeaseTestClock{now: time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)}
	repository := &pkiLeaseTestRepository{domainID: "domain-1", epoch: 3}
	service := newPKILeaseTestService(t, repository, clock, "instance-a")
	if _, err := service.Acquire(t.Context()); err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	clock.Advance(9 * time.Second)
	renewed, err := service.Renew(t.Context())
	if err != nil {
		t.Fatalf("Renew() error = %v", err)
	}
	if renewed.LeaseDeadline.Sub(clock.Now()) != defaultPKILeaseTTL {
		t.Fatalf("renewed TTL = %v, want %v", renewed.LeaseDeadline.Sub(clock.Now()), defaultPKILeaseTTL)
	}

	injected := errors.New("shared state unavailable")
	repository.SetReadError(injected)
	if _, err := service.RequirePKILease(t.Context()); !errors.Is(err, injected) {
		t.Fatalf("RequirePKILease(repository failure) error = %v, want injected", err)
	}
	repository.SetReadError(nil)
	if _, err := service.RequirePKILease(t.Context()); !errors.Is(err, ErrPKILeaseNotHeld) {
		t.Fatalf("RequirePKILease(after failure) error = %v, want fail-closed state", err)
	}

	if _, err := service.Acquire(t.Context()); err != nil {
		t.Fatalf("Acquire(recovery) error = %v", err)
	}
	if err := service.Relinquish(t.Context()); err != nil {
		t.Fatalf("Relinquish() error = %v", err)
	}
	if _, err := service.RequirePKILease(t.Context()); !errors.Is(err, ErrPKILeaseNotHeld) {
		t.Fatalf("RequirePKILease(relinquished) error = %v", err)
	}
}

func TestPKILeaseReadCrossingDeadlineFailsClosed(t *testing.T) {
	clock := &pkiLeaseTestClock{now: time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)}
	base := &pkiLeaseTestRepository{domainID: "domain-1", epoch: 4}
	repository := &pkiLeaseBlockingReadRepository{
		PKILeaseRepository: base, started: make(chan struct{}), release: make(chan struct{}),
	}
	service := newPKILeaseTestService(t, repository, clock, "instance-a")
	if _, err := service.Acquire(t.Context()); err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	result := make(chan error, 1)
	go func() {
		_, err := service.RequirePKILease(context.Background())
		result <- err
	}()
	<-repository.started
	clock.Advance(defaultPKILeaseTTL + time.Second)
	close(repository.release)
	if err := <-result; !errors.Is(err, ErrPKILeaseNotHeld) {
		t.Fatalf("RequirePKILease(read crossed deadline) error = %v, want ErrPKILeaseNotHeld", err)
	}
}

func TestPKILeaseAcquireAndRenewCrossingDeadlineFailClosed(t *testing.T) {
	t.Run("acquire", func(t *testing.T) {
		clock := &pkiLeaseTestClock{now: time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)}
		base := &pkiLeaseTestRepository{domainID: "domain-1", epoch: 4}
		repository := &pkiLeaseBlockingMutationRepository{
			PKILeaseRepository: base, acquireStarted: make(chan struct{}), acquireRelease: make(chan struct{}),
		}
		service := newPKILeaseTestService(t, repository, clock, "instance-a")
		result := make(chan error, 1)
		go func() {
			_, err := service.Acquire(context.Background())
			result <- err
		}()
		<-repository.acquireStarted
		clock.Advance(defaultPKILeaseTTL + time.Second)
		close(repository.acquireRelease)
		if err := <-result; !errors.Is(err, ErrPKILeaseNotHeld) {
			t.Fatalf("Acquire(response crossed deadline) error = %v, want ErrPKILeaseNotHeld", err)
		}
	})

	t.Run("renew", func(t *testing.T) {
		clock := &pkiLeaseTestClock{now: time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)}
		base := &pkiLeaseTestRepository{domainID: "domain-1", epoch: 4}
		repository := &pkiLeaseBlockingMutationRepository{
			PKILeaseRepository: base, renewStarted: make(chan struct{}), renewRelease: make(chan struct{}),
		}
		service := newPKILeaseTestService(t, repository, clock, "instance-a")
		if _, err := service.Acquire(t.Context()); err != nil {
			t.Fatalf("Acquire() error = %v", err)
		}
		result := make(chan error, 1)
		go func() {
			_, err := service.Renew(context.Background())
			result <- err
		}()
		<-repository.renewStarted
		clock.Advance(defaultPKILeaseTTL + time.Second)
		close(repository.renewRelease)
		if err := <-result; !errors.Is(err, ErrPKILeaseNotHeld) {
			t.Fatalf("Renew(response crossed deadline) error = %v, want ErrPKILeaseNotHeld", err)
		}
	})
}

func TestPKILeaseSameInstanceReacquireInvalidatesOldTerm(t *testing.T) {
	clock := &pkiLeaseTestClock{now: time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)}
	repository := &pkiLeaseTestRepository{domainID: "domain-1", epoch: 9}
	service := newPKILeaseTestService(t, repository, clock, "instance-a")
	oldGrant, err := service.Acquire(t.Context())
	if err != nil {
		t.Fatalf("Acquire(first) error = %v", err)
	}
	newGrant, err := service.Acquire(t.Context())
	if err != nil {
		t.Fatalf("Acquire(reacquire) error = %v", err)
	}
	if !validPKILeaseTerm(oldGrant.LeaseTerm) || !validPKILeaseTerm(newGrant.LeaseTerm) || oldGrant.LeaseTerm == newGrant.LeaseTerm {
		t.Fatalf("lease terms = %q and %q, want distinct 32-byte terms", oldGrant.LeaseTerm, newGrant.LeaseTerm)
	}
	now := clock.Now()
	if _, renewed, err := repository.RenewPKILease(t.Context(), oldGrant.InstanceID, oldGrant.LeaseTerm, oldGrant.PKIEpoch, now, now.Add(defaultPKILeaseTTL)); err != nil || renewed {
		t.Fatalf("RenewPKILease(old term) = renewed %v, error %v", renewed, err)
	}
	if relinquished, err := repository.RelinquishPKILease(t.Context(), oldGrant.InstanceID, oldGrant.LeaseTerm, oldGrant.PKIEpoch, now); err != nil || relinquished {
		t.Fatalf("RelinquishPKILease(old term) = relinquished %v, error %v", relinquished, err)
	}
	if _, err := service.RequirePKILease(t.Context()); err != nil {
		t.Fatalf("RequirePKILease(new term) error = %v", err)
	}
}

func TestPKIEpochLexicographicFencingAndForceActivation(t *testing.T) {
	current := PKISecurityVersion{PKIEpoch: 4, SecurityRevision: 100}
	tests := []struct {
		name     string
		incoming PKISecuritySnapshotVersion
		wantErr  bool
	}{
		{"same epoch newer revision", PKISecuritySnapshotVersion{Version: PKISecurityVersion{PKIEpoch: 4, SecurityRevision: 101}}, false},
		{"same version idempotent", PKISecuritySnapshotVersion{Version: current}, false},
		{"same epoch downgrade", PKISecuritySnapshotVersion{Version: PKISecurityVersion{PKIEpoch: 4, SecurityRevision: 99}}, true},
		{"older epoch higher revision", PKISecuritySnapshotVersion{Version: PKISecurityVersion{PKIEpoch: 3, SecurityRevision: 1000}, Full: true}, true},
		{"higher epoch delta", PKISecuritySnapshotVersion{Version: PKISecurityVersion{PKIEpoch: 5, SecurityRevision: 0}}, true},
		{"higher epoch full revision reset", PKISecuritySnapshotVersion{Version: PKISecurityVersion{PKIEpoch: 5, SecurityRevision: 0}, Full: true}, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidatePKISecuritySnapshot(current, test.incoming)
			if (err != nil) != test.wantErr {
				t.Fatalf("ValidatePKISecuritySnapshot() error = %v, wantErr=%v", err, test.wantErr)
			}
		})
	}
	forced, err := NextForcedPKISecurityVersion(
		PKISecurityVersion{PKIEpoch: 8, SecurityRevision: 2},
		PKISecurityVersion{PKIEpoch: 7, SecurityRevision: 900},
	)
	if err != nil {
		t.Fatalf("NextForcedPKISecurityVersion() error = %v", err)
	}
	if forced != (PKISecurityVersion{PKIEpoch: 9, SecurityRevision: 0}) {
		t.Fatalf("forced version = %+v, want epoch 9/revision 0", forced)
	}
	for _, versions := range [][2]PKISecurityVersion{
		{{PKIEpoch: 1, SecurityRevision: -1}, {PKIEpoch: 2, SecurityRevision: 0}},
		{{PKIEpoch: 1, SecurityRevision: 0}, {PKIEpoch: 2, SecurityRevision: -1}},
	} {
		if _, err := NextForcedPKISecurityVersion(versions[0], versions[1]); !errors.Is(err, ErrPKIEpochStale) {
			t.Fatalf("NextForcedPKISecurityVersion(%+v, %+v) error = %v, want ErrPKIEpochStale", versions[0], versions[1], err)
		}
	}
}

func newPKILeaseTestService(t *testing.T, repository PKILeaseRepository, clock *pkiLeaseTestClock, instanceID string) *PKILeaseService {
	t.Helper()
	service, err := NewPKILeaseService(PKILeaseServiceOptions{
		Repository: repository, InstanceID: instanceID, Clock: clock.Now,
	})
	if err != nil {
		t.Fatalf("NewPKILeaseService() error = %v", err)
	}
	return service
}

type pkiLeaseTestClock struct {
	mutex sync.Mutex
	now   time.Time
}

func (c *pkiLeaseTestClock) Now() time.Time {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	return c.now
}

func (c *pkiLeaseTestClock) Advance(duration time.Duration) {
	c.mutex.Lock()
	c.now = c.now.Add(duration)
	c.mutex.Unlock()
}

type pkiLeaseTestRepository struct {
	mutex    sync.Mutex
	domainID string
	epoch    int64
	lease    PKILeaseSnapshot
	readErr  error
}

func (r *pkiLeaseTestRepository) ReadPKILease(context.Context) (PKILeaseSnapshot, error) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	if r.readErr != nil {
		return PKILeaseSnapshot{}, r.readErr
	}
	return r.snapshotLocked(), nil
}

func (r *pkiLeaseTestRepository) TryAcquirePKILease(_ context.Context, instanceID, leaseTerm string, now, deadline time.Time) (PKILeaseSnapshot, bool, error) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	if r.readErr != nil {
		return PKILeaseSnapshot{}, false, r.readErr
	}
	available := !r.lease.Exists || r.lease.State == PKILeaseStateRelinquished || !r.lease.LeaseDeadline.After(now) || r.lease.InstanceID == instanceID
	if !available {
		return r.snapshotLocked(), false, nil
	}
	r.lease = PKILeaseSnapshot{
		Exists: true, PKIDomainID: r.domainID, PKIEpoch: r.epoch, InstanceID: instanceID,
		LeaseTerm: leaseTerm, LeaseDeadline: deadline, State: PKILeaseStateHeld,
	}
	return r.snapshotLocked(), true, nil
}

func (r *pkiLeaseTestRepository) RenewPKILease(_ context.Context, instanceID, leaseTerm string, epoch int64, now, deadline time.Time) (PKILeaseSnapshot, bool, error) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	if r.readErr != nil {
		return PKILeaseSnapshot{}, false, r.readErr
	}
	if !r.lease.Exists || r.lease.State != PKILeaseStateHeld || r.lease.InstanceID != instanceID || r.lease.LeaseTerm != leaseTerm || r.epoch != epoch || !r.lease.LeaseDeadline.After(now) {
		return r.snapshotLocked(), false, nil
	}
	r.lease.LeaseDeadline = deadline
	return r.snapshotLocked(), true, nil
}

func (r *pkiLeaseTestRepository) RelinquishPKILease(_ context.Context, instanceID, leaseTerm string, epoch int64, now time.Time) (bool, error) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	if r.readErr != nil {
		return false, r.readErr
	}
	if !r.lease.Exists || r.lease.InstanceID != instanceID || r.lease.LeaseTerm != leaseTerm || r.epoch != epoch || r.lease.State != PKILeaseStateHeld {
		return false, nil
	}
	r.lease.State = PKILeaseStateRelinquished
	r.lease.LeaseDeadline = now
	return true, nil
}

type pkiLeaseBlockingReadRepository struct {
	PKILeaseRepository
	started chan struct{}
	release chan struct{}
}

func (r *pkiLeaseBlockingReadRepository) ReadPKILease(ctx context.Context) (PKILeaseSnapshot, error) {
	snapshot, err := r.PKILeaseRepository.ReadPKILease(ctx)
	close(r.started)
	select {
	case <-r.release:
		return snapshot, err
	case <-ctx.Done():
		return PKILeaseSnapshot{}, ctx.Err()
	}
}

type pkiLeaseBlockingMutationRepository struct {
	PKILeaseRepository
	acquireStarted chan struct{}
	acquireRelease chan struct{}
	renewStarted   chan struct{}
	renewRelease   chan struct{}
}

func (r *pkiLeaseBlockingMutationRepository) TryAcquirePKILease(ctx context.Context, instanceID, leaseTerm string, now, deadline time.Time) (PKILeaseSnapshot, bool, error) {
	snapshot, acquired, err := r.PKILeaseRepository.TryAcquirePKILease(ctx, instanceID, leaseTerm, now, deadline)
	if r.acquireStarted == nil {
		return snapshot, acquired, err
	}
	close(r.acquireStarted)
	select {
	case <-r.acquireRelease:
		return snapshot, acquired, err
	case <-ctx.Done():
		return PKILeaseSnapshot{}, false, ctx.Err()
	}
}

func (r *pkiLeaseBlockingMutationRepository) RenewPKILease(ctx context.Context, instanceID, leaseTerm string, epoch int64, now, deadline time.Time) (PKILeaseSnapshot, bool, error) {
	snapshot, renewed, err := r.PKILeaseRepository.RenewPKILease(ctx, instanceID, leaseTerm, epoch, now, deadline)
	if r.renewStarted == nil {
		return snapshot, renewed, err
	}
	close(r.renewStarted)
	select {
	case <-r.renewRelease:
		return snapshot, renewed, err
	case <-ctx.Done():
		return PKILeaseSnapshot{}, false, ctx.Err()
	}
}

func (r *pkiLeaseTestRepository) SetReadError(err error) {
	r.mutex.Lock()
	r.readErr = err
	r.mutex.Unlock()
}

func (r *pkiLeaseTestRepository) snapshotLocked() PKILeaseSnapshot {
	snapshot := r.lease
	snapshot.PKIDomainID = r.domainID
	snapshot.PKIEpoch = r.epoch
	return snapshot
}
