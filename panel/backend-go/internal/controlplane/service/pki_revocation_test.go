package service

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

func TestPKIRevocationCommitsRevisionSnapshotTokenAndDisconnects(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 4, 8, 0, 0, 0, time.UTC)
	repository := &pkiRevocationTestRepository{now: now}
	publisher := &pkiRevocationTestPublisher{}
	closer := &pkiRevocationTestCloser{}
	service, err := NewPKIRevocationService(PKIRevocationServiceOptions{
		Repository: repository, Signer: pkiRevocationTestSigner{}, Publisher: publisher,
		Closer: closer, Lease: pkiStaticLeaseGate{}, Clock: func() time.Time { return now },
		Convergence: 100 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewPKIRevocationService() error = %v", err)
	}
	request := PKIRevocationRequest{
		IdentityID: "agent-1", Reason: "operator revoked compromised node", Source: "operator", OperatorID: "admin",
	}
	commit, err := service.Revoke(t.Context(), request)
	if err != nil {
		t.Fatalf("Revoke() error = %v", err)
	}
	if !repository.committed || !commit.IdentityRevoked || commit.CertificatesRevoked != 2 ||
		!commit.ControlTokenDisabled || commit.Facts.SecurityRevision != 5 ||
		commit.Snapshot.Version.Version != (PKISecurityVersion{PKIEpoch: 1, SecurityRevision: 5}) ||
		len(commit.Snapshot.Signature) == 0 || !publisher.called || !closer.called {
		t.Fatalf("revocation commit = %+v, publisher %v, closer %v", commit, publisher.called, closer.called)
	}
	incomplete := commit
	incomplete.CertificatesRevoked = 0
	if err := validatePKIRevocationCommit(request, commit.Lease, incomplete); !errors.Is(err, ErrPKILifecycleInvalid) {
		t.Fatalf("validatePKIRevocationCommit(mismatched certificate count) error = %v", err)
	}
}

func TestPKIRevocationSigningFailureRollsBackAtomicMutation(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 4, 8, 0, 0, 0, time.UTC)
	repository := &pkiRevocationTestRepository{now: now}
	service, err := NewPKIRevocationService(PKIRevocationServiceOptions{
		Repository: repository, Signer: pkiRevocationTestSigner{err: errors.New("signing failed")},
		Publisher: &pkiRevocationTestPublisher{}, Closer: &pkiRevocationTestCloser{},
		Lease: pkiStaticLeaseGate{}, Clock: func() time.Time { return now }, Convergence: 100 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewPKIRevocationService() error = %v", err)
	}
	if _, err := service.Revoke(t.Context(), PKIRevocationRequest{IdentityID: "agent-1", Reason: "reason", Source: "operator"}); err == nil {
		t.Fatal("Revoke(signing failure) returned nil error")
	}
	if repository.committed {
		t.Fatal("repository committed a revocation whose snapshot could not be signed")
	}
}

func TestPKIRevocationOnlineConvergenceHasHardDeadline(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 4, 8, 0, 0, 0, time.UTC)
	service, err := NewPKIRevocationService(PKIRevocationServiceOptions{
		Repository: &pkiRevocationTestRepository{now: now}, Signer: pkiRevocationTestSigner{},
		Publisher: pkiRevocationBlockingConsumer{}, Closer: pkiRevocationBlockingConsumer{},
		Lease: pkiStaticLeaseGate{}, Clock: func() time.Time { return now }, Convergence: 20 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewPKIRevocationService() error = %v", err)
	}
	started := time.Now()
	commit, err := service.Revoke(t.Context(), PKIRevocationRequest{IdentityID: "agent-1", Reason: "reason", Source: "operator"})
	if !errors.Is(err, context.DeadlineExceeded) || !commit.IdentityRevoked || time.Since(started) > time.Second {
		t.Fatalf("deadline revoke = (%+v, %v), elapsed %v", commit, err, time.Since(started))
	}
}

func TestPKIRevocationRepositoryFenceRejectsCheckCommitLeaseLoss(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 4, 8, 30, 0, 0, time.UTC)
	repository := &pkiRevocationTestRepository{now: now, rejectLease: true}
	publisher := &pkiRevocationTestPublisher{}
	closer := &pkiRevocationTestCloser{}
	service, err := NewPKIRevocationService(PKIRevocationServiceOptions{
		Repository: repository, Signer: pkiRevocationTestSigner{}, Publisher: publisher, Closer: closer,
		Lease: pkiStaticLeaseGate{}, Clock: func() time.Time { return now }, Convergence: 100 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewPKIRevocationService() error = %v", err)
	}
	if _, err := service.Revoke(t.Context(), PKIRevocationRequest{IdentityID: "agent-1", Reason: "reason", Source: "operator"}); !errors.Is(err, ErrPKILeaseNotHeld) {
		t.Fatalf("Revoke(fence loss) error = %v, want ErrPKILeaseNotHeld", err)
	}
	if repository.committed || publisher.called || closer.called {
		t.Fatalf("fenced revoke side effects = committed %v, published %v, closed %v", repository.committed, publisher.called, closer.called)
	}
}

func TestPKIProductionRevocationHandlesUnissuedAndSupersededCertificates(t *testing.T) {
	t.Parallel()
	newRevocation := func(t *testing.T, fixture pkiEnrollmentFixture) *PKIRevocationService {
		t.Helper()
		repository, err := NewGormPKIRevocationRepository(GormPKIRevocationRepositoryOptions{
			Store: fixture.store, Clock: func() time.Time { return fixture.now },
		})
		if err != nil {
			t.Fatal(err)
		}
		signer, err := NewPKIVaultSecuritySnapshotSigner(PKIVaultSecuritySnapshotSignerOptions{
			StateSource: fixture.store, Signer: &pkiEnrollmentTestAuthoritySigner{key: fixture.authorityKey},
		})
		if err != nil {
			t.Fatal(err)
		}
		result, err := NewPKIRevocationService(PKIRevocationServiceOptions{
			Repository: repository, Signer: signer, Publisher: &pkiRevocationTestPublisher{}, Closer: &pkiRevocationTestCloser{},
			Lease: pkiStaticLeaseGate{}, Clock: func() time.Time { return fixture.now }, Convergence: time.Second,
		})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}

	t.Run("enrollment-required identity without certificate", func(t *testing.T) {
		fixture := newPKIEnrollmentFixture(t)
		if err := fixture.store.WithPKITransaction(t.Context(), func(tx *storage.PKITransaction) error {
			return tx.CreatePKIIdentity(t.Context(), storage.PKIIdentityRow{
				ID: "identity-unissued", PKIDomainID: "domain-1", Kind: storage.PKIIdentityKindAgent, AgentID: "agent-a",
				State: storage.PKIIdentityStateEnrollmentRequired, CreatedAt: fixture.now, UpdatedAt: fixture.now,
			})
		}); err != nil {
			t.Fatal(err)
		}
		commit, err := newRevocation(t, fixture).Revoke(t.Context(), PKIRevocationRequest{
			IdentityID: "identity-unissued", Reason: "cancel incomplete enrollment", Source: "test",
		})
		if err != nil || !commit.IdentityRevoked || commit.CertificatesRevoked != 0 {
			t.Fatalf("Revoke(unissued identity) = (%+v, %v)", commit, err)
		}
		state := loadPKIEnrollmentState(t, fixture.store)
		if len(state.Identities) != 1 || state.Identities[0].State != storage.PKIIdentityStateRevoked || state.Settings.SecurityRevision != 1 {
			t.Fatalf("revoked unissued state = %+v", state)
		}
	})

	t.Run("superseded lineage", func(t *testing.T) {
		fixture := newPKIEnrollmentFixture(t)
		if err := fixture.store.WithPKITransaction(t.Context(), func(tx *storage.PKITransaction) error {
			return tx.CreatePKIIdentity(t.Context(), storage.PKIIdentityRow{
				ID: "identity-agent-a", PKIDomainID: "domain-1", Kind: storage.PKIIdentityKindAgent, AgentID: "agent-a",
				State: storage.PKIIdentityStateEnrollmentRequired, CreatedAt: fixture.now, UpdatedAt: fixture.now,
			})
		}); err != nil {
			t.Fatal(err)
		}
		binding, err := newPKIIdentityBinding("domain-1", storage.PKIIdentityKindAgent, "agent-a", "", storage.PKICertificatePurposeClient, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		enrollment := newPKIEnrollmentServiceForTest(
			t, fixture, &pkiEnrollmentTestAuthoritySigner{key: fixture.authorityKey}, incrementingPKIID("revoke-lineage"),
		)
		for _, requestID := range []string{"generation-1", "generation-2"} {
			if _, err := enrollment.EnrollAuthenticated(t.Context(), "agent-a", "agent-a-token", PKIEnrollRequest{
				RequestID: requestID, Kind: storage.PKIIdentityKindAgent, Purpose: storage.PKICertificatePurposeClient,
				CSRPEM: mustPKIEnrollmentCSR(t, mustPKIEnrollmentKey(t), binding, false),
			}); err != nil {
				t.Fatalf("EnrollAuthenticated(%s) error = %v", requestID, err)
			}
		}
		before := loadPKIEnrollmentState(t, fixture.store)
		if len(before.Certificates) != 2 || before.Certificates[0].Status != storage.PKICertificateStatusSuperseded || before.Certificates[0].SupersededByID == nil {
			t.Fatalf("pre-revocation certificate lineage = %+v", before.Certificates)
		}
		commit, err := newRevocation(t, fixture).Revoke(t.Context(), PKIRevocationRequest{
			IdentityID: "identity-agent-a", Reason: "compromised", Source: "test",
		})
		if err != nil || commit.CertificatesRevoked != 2 {
			t.Fatalf("Revoke(superseded lineage) = (%+v, %v)", commit, err)
		}
		after := loadPKIEnrollmentState(t, fixture.store)
		for _, certificate := range after.Certificates {
			if certificate.Status != storage.PKICertificateStatusRevoked || certificate.SupersededByID != nil {
				t.Fatalf("revoked certificate retained invalid lineage: %+v", certificate)
			}
		}
	})
}

func TestPKIRevocationRecoveryAppliesSafetyBeforeOrdinaryRevision(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 4, 8, 0, 0, 0, time.UTC)
	snapshot := PKISignedSecuritySnapshot{
		PKIUnsignedSecuritySnapshot: PKIUnsignedSecuritySnapshot{
			PKIDomainID: "domain-1", Version: PKISecuritySnapshotVersion{
				Version: PKISecurityVersion{PKIEpoch: 3, SecurityRevision: 0}, Full: true,
			},
			IssuedAt: now.Add(-365 * 24 * time.Hour), RevokedIdentityIDs: []string{"agent-1"},
		},
		SignerGeneration: 4, Signature: []byte("signature"),
	}
	if err := ValidateLastTrustedPKISnapshotForOfflineRelay(snapshot); err != nil {
		t.Fatalf("one-year-old trusted snapshot was rejected by age: %v", err)
	}
	target := &pkiControlSyncTestTarget{current: PKISecurityVersion{PKIEpoch: 2, SecurityRevision: 999}}
	if err := ApplyRecoveredPKIControlSync(t.Context(), target, snapshot, []byte("ordinary")); err != nil {
		t.Fatalf("ApplyRecoveredPKIControlSync() error = %v", err)
	}
	wantOrder := []string{"current", "verify", "persist", "close", "ordinary"}
	if !reflect.DeepEqual(target.order, wantOrder) {
		t.Fatalf("application order = %v, want %v", target.order, wantOrder)
	}

	stale := snapshot
	stale.Version = PKISecuritySnapshotVersion{Version: PKISecurityVersion{PKIEpoch: 1, SecurityRevision: 1000}, Full: true}
	target = &pkiControlSyncTestTarget{current: PKISecurityVersion{PKIEpoch: 2, SecurityRevision: 1}}
	if err := ApplyRecoveredPKIControlSync(t.Context(), target, stale, nil); !errors.Is(err, ErrPKIEpochStale) {
		t.Fatalf("stale snapshot error = %v, want ErrPKIEpochStale", err)
	}
	if !reflect.DeepEqual(target.order, []string{"current"}) {
		t.Fatalf("stale application order = %v, want no mutation", target.order)
	}
}

type pkiRevocationTestRepository struct {
	now         time.Time
	committed   bool
	rejectLease bool
	convergence error
}

func (r *pkiRevocationTestRepository) RevokePKIIdentityAtomically(
	ctx context.Context,
	mutation PKIRevocationMutation,
	build func(context.Context, PKIRevocationFacts) (PKISignedSecuritySnapshot, error),
) (PKIRevocationCommit, error) {
	if r.rejectLease || validatePKIMutationLeaseFence(mutation.Lease) != nil ||
		mutation.Lease.PKIDomainID != "domain-1" || mutation.Lease.PKIEpoch != 1 ||
		mutation.Lease.InstanceID != "instance-1" || mutation.Lease.LeaseTerm != strings.Repeat("a", 64) {
		return PKIRevocationCommit{}, ErrPKILeaseNotHeld
	}
	request := mutation.Request
	facts := PKIRevocationFacts{
		PKIDomainID: "domain-1", PKIEpoch: 1, PreviousRevision: 4, SecurityRevision: 5,
		IdentityID: request.IdentityID, IdentityKind: storage.PKIIdentityKindAgent, RevokedSerials: []string{"serial-a", "serial-b"},
		RevokedIdentityIDs: []string{request.IdentityID}, AllRevokedSerials: []string{"serial-a", "serial-b"},
		ActiveTrustGenerations: []int64{1, 2},
	}
	snapshot, err := build(ctx, facts)
	if err != nil {
		return PKIRevocationCommit{}, err
	}
	event := NewPKIAuditEvent("identity_revoked", request.Source, request.IdentityID, "succeeded", request.Reason, r.now)
	event.OperatorID = request.OperatorID
	event.SecurityRevision = facts.SecurityRevision
	event.ID = stablePKIAuditEventID(event)
	commit := PKIRevocationCommit{
		Facts: facts, Snapshot: snapshot, IdentityRevoked: true, CertificatesRevoked: 2,
		ControlTokenDisabled: true, ControlSessionTargets: []string{"control-agent-1"},
		RelaySessionTargets: []string{"relay-agent-1"}, Lease: mutation.Lease, Event: event,
		ConvergenceJobID: "revoke-agent-1-r5",
	}
	r.committed = true
	return commit, nil
}

func (r *pkiRevocationTestRepository) RecordPKIRevocationConvergence(_ context.Context, _ PKIRevocationCommit, convergenceErr error) error {
	r.convergence = convergenceErr
	return nil
}

type pkiRevocationTestSigner struct{ err error }

func (s pkiRevocationTestSigner) SignPKISecuritySnapshot(_ context.Context, unsigned PKIUnsignedSecuritySnapshot) (PKISignedSecuritySnapshot, error) {
	for _, generation := range unsigned.TrustGenerations {
		unsigned.TrustRoots = append(unsigned.TrustRoots, PKISecurityTrustRootDescriptor{
			AuthorityID: fmt.Sprintf("authority-%d", generation), Generation: generation, Status: "active",
			FingerprintSHA256: strings.Repeat("a", 64), NotBefore: unsigned.IssuedAt.Add(-time.Hour), NotAfter: unsigned.IssuedAt.Add(time.Hour),
		})
	}
	signerGeneration := int64(1)
	if len(unsigned.TrustGenerations) > 0 {
		signerGeneration = unsigned.TrustGenerations[len(unsigned.TrustGenerations)-1]
	}
	return PKISignedSecuritySnapshot{PKIUnsignedSecuritySnapshot: unsigned, SignerGeneration: signerGeneration, Signature: []byte("signature")}, s.err
}

type pkiRevocationTestPublisher struct{ called bool }

func (p *pkiRevocationTestPublisher) PublishPKISecuritySnapshot(context.Context, PKISignedSecuritySnapshot, []string) error {
	p.called = true
	return nil
}

type pkiRevocationTestCloser struct{ called bool }

func (c *pkiRevocationTestCloser) CloseRevokedPKISessions(context.Context, PKIRevocationCommit) error {
	c.called = true
	return nil
}

type pkiRevocationBlockingConsumer struct{}

func (pkiRevocationBlockingConsumer) PublishPKISecuritySnapshot(ctx context.Context, _ PKISignedSecuritySnapshot, _ []string) error {
	<-ctx.Done()
	return ctx.Err()
}

func (pkiRevocationBlockingConsumer) CloseRevokedPKISessions(ctx context.Context, _ PKIRevocationCommit) error {
	<-ctx.Done()
	return ctx.Err()
}

type pkiControlSyncTestTarget struct {
	current PKISecurityVersion
	order   []string
}

func (t *pkiControlSyncTestTarget) CurrentPKISecurityVersion(context.Context) (PKISecurityVersion, error) {
	t.order = append(t.order, "current")
	return t.current, nil
}

func (t *pkiControlSyncTestTarget) VerifyPKISecuritySnapshot(context.Context, PKISignedSecuritySnapshot) error {
	t.order = append(t.order, "verify")
	return nil
}

func (t *pkiControlSyncTestTarget) PersistPKISecuritySnapshot(context.Context, PKISignedSecuritySnapshot) error {
	t.order = append(t.order, "persist")
	return nil
}

func (t *pkiControlSyncTestTarget) CloseRevokedRelaySessions(context.Context, PKISignedSecuritySnapshot) error {
	t.order = append(t.order, "close")
	return nil
}

func (t *pkiControlSyncTestTarget) ApplyOrdinaryControlRevision(context.Context, []byte) error {
	t.order = append(t.order, "ordinary")
	return nil
}
