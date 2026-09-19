package service

import (
	"context"
	"fmt"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

// PKIInstanceLeaseStore is the storage-owned, cycle-free boundary used by the
// service adapter. GormStore implements these operations with transactional
// compare-and-swap semantics for SQLite, PostgreSQL, and MySQL.
type PKIInstanceLeaseStore interface {
	ReadPKIInstanceLease(context.Context) (storage.PKIInstanceLeaseSnapshot, error)
	TryAcquirePKIInstanceLease(context.Context, string, string, time.Time, time.Time) (storage.PKIInstanceLeaseSnapshot, bool, error)
	RenewPKIInstanceLease(context.Context, string, string, int64, time.Time, time.Time) (storage.PKIInstanceLeaseSnapshot, bool, error)
	RelinquishPKIInstanceLease(context.Context, string, string, int64, time.Time) (bool, error)
}

type GormPKILeaseRepository struct {
	store PKIInstanceLeaseStore
}

func NewGormPKILeaseRepository(store PKIInstanceLeaseStore) (*GormPKILeaseRepository, error) {
	if store == nil {
		return nil, fmt.Errorf("%w: PKI lease store is required", ErrPKILeaseInvalid)
	}
	return &GormPKILeaseRepository{store: store}, nil
}

func (r *GormPKILeaseRepository) ReadPKILease(ctx context.Context) (PKILeaseSnapshot, error) {
	snapshot, err := r.store.ReadPKIInstanceLease(ctx)
	return pkiLeaseSnapshotFromStorage(snapshot), err
}

func (r *GormPKILeaseRepository) TryAcquirePKILease(
	ctx context.Context,
	instanceID string,
	leaseTerm string,
	now time.Time,
	deadline time.Time,
) (PKILeaseSnapshot, bool, error) {
	snapshot, acquired, err := r.store.TryAcquirePKIInstanceLease(ctx, instanceID, leaseTerm, now, deadline)
	return pkiLeaseSnapshotFromStorage(snapshot), acquired, err
}

func (r *GormPKILeaseRepository) RenewPKILease(
	ctx context.Context,
	instanceID string,
	leaseTerm string,
	epoch int64,
	now time.Time,
	deadline time.Time,
) (PKILeaseSnapshot, bool, error) {
	snapshot, renewed, err := r.store.RenewPKIInstanceLease(ctx, instanceID, leaseTerm, epoch, now, deadline)
	return pkiLeaseSnapshotFromStorage(snapshot), renewed, err
}

func (r *GormPKILeaseRepository) RelinquishPKILease(
	ctx context.Context,
	instanceID string,
	leaseTerm string,
	epoch int64,
	now time.Time,
) (bool, error) {
	return r.store.RelinquishPKIInstanceLease(ctx, instanceID, leaseTerm, epoch, now)
}

func pkiLeaseSnapshotFromStorage(snapshot storage.PKIInstanceLeaseSnapshot) PKILeaseSnapshot {
	return PKILeaseSnapshot{
		Exists: snapshot.Exists, PKIDomainID: snapshot.PKIDomainID, PKIEpoch: snapshot.PKIEpoch,
		InstanceID: snapshot.InstanceID, LeaseTerm: snapshot.LeaseTerm,
		LeaseDeadline: snapshot.LeaseDeadline, State: snapshot.State,
	}
}

var _ PKILeaseRepository = (*GormPKILeaseRepository)(nil)
