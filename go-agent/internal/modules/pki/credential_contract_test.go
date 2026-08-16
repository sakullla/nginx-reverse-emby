//go:build !integration

package pki

import (
	"context"

	"errors"
	"os"
	"path/filepath"

	"sync"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

var errCredentialContractInjected = errors.New("injected credential contract persistence failure")

func testCredentialCrashBoundariesRecoverAfterStoreReopen(t *testing.T) {
	now := time.Date(2026, 8, 2, 8, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		point     string
		committed bool
	}{
		{point: "credential.after_generation_publish"},
		{point: "credential.after_pointer_publish", committed: true},
		{point: "credential.after_ack_publish", committed: true},
		{point: "credential.after_pending_remove", committed: true},
	} {
		t.Run(test.point, func(t *testing.T) {
			dataRoot := t.TempDir()
			failed := false
			store, err := NewStore(dataRoot, WithClock(func() time.Time { return now }), withPersistenceCheckpoint(func(point string) error {
				if point == test.point && !failed {
					failed = true
					return errCredentialContractInjected
				}
				return nil
			}))
			if err != nil {
				t.Fatal(err)
			}
			authority := newTestAuthority(t, now, "authority-1", 1)
			expectation := credentialContractAgentExpectation(now)
			pending := prepareKnownAgent(t, store, expectation)
			credential := authority.issueCredential(t, pending, expectation, "identity-1", "certificate-1", now)
			request := ActivateRequest{
				StorageIdentity: "agent", RequestID: pending.Request.RequestID, Credential: credential,
				Security: authority.snapshot(t, "domain-1", 1, 0, true, nil, nil, now), Expectation: expectation,
			}
			metadata, activateErr := store.ActivateCredential(context.Background(), request)
			if test.committed {
				if !errors.Is(activateErr, ErrActivationCommitted) || metadata.Manifest.Credential.CertificateID != "certificate-1" {
					t.Fatalf("committed ActivateCredential() = %+v, error = %v", metadata, activateErr)
				}
			} else if !errors.Is(activateErr, errCredentialContractInjected) {
				t.Fatalf("uncommitted ActivateCredential() error = %v", activateErr)
			}

			reopened, err := NewStore(dataRoot, WithClock(func() time.Time { return now.Add(2 * time.Minute) }))
			if err != nil {
				t.Fatal(err)
			}
			if !test.committed {
				higher := authority.snapshot(t, "domain-1", 1, 1, false, nil, nil, now.Add(time.Minute))
				if _, err := reopened.ApplySecuritySnapshot(higher); err != nil {
					t.Fatalf("ApplySecuritySnapshot(higher) error = %v", err)
				}
				request.Security = higher
			} else if active, err := reopened.LoadActiveCredential("agent"); err != nil || active.Manifest.Credential.CertificateID != "certificate-1" {
				t.Fatalf("reopened active credential = %+v, error = %v", active, err)
			}
			if _, err := reopened.ActivateCredential(context.Background(), request); err != nil {
				t.Fatalf("reopened reconciliation error = %v", err)
			}
			if _, err := reopened.LoadPending("agent"); !errors.Is(err, ErrPendingNotFound) {
				t.Fatalf("pending after reconciliation error = %v, want ErrPendingNotFound", err)
			}
			acknowledgement, err := reopened.SecurityAcknowledgement("agent")
			if err != nil || acknowledgement.CertificateID != "certificate-1" {
				t.Fatalf("reopened acknowledgement = %+v, error = %v", acknowledgement, err)
			}
		})
	}
}

func TestCredentialCrashBoundariesRecoverAfterStoreReopen(t *testing.T) {
	testCredentialCrashBoundariesRecoverAfterStoreReopen(t)
}

func TestCommittedPendingTombstoneSurvivesPartialCleanupAndReconciles(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 2, 8, 0, 0, 0, time.UTC)
	dataRoot := t.TempDir()
	failed := false
	store, err := NewStore(dataRoot, WithClock(func() time.Time { return now }), withPersistenceCheckpoint(func(point string) error {
		if point == "credential.after_pending_tombstone_publish" && !failed {
			failed = true
			return errCredentialContractInjected
		}
		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	authority := newTestAuthority(t, now, "authority-1", 1)
	expectation := credentialContractAgentExpectation(now)
	pending := prepareKnownAgent(t, store, expectation)
	credential := authority.issueCredential(t, pending, expectation, "identity-1", "certificate-1", now)
	request := ActivateRequest{
		StorageIdentity: "agent", RequestID: pending.Request.RequestID, Credential: credential,
		Security: authority.snapshot(t, "domain-1", 1, 0, true, nil, nil, now), Expectation: expectation,
	}
	metadata, err := store.ActivateCredential(context.Background(), request)
	if !errors.Is(err, ErrActivationCommitted) || !errors.Is(err, errCredentialContractInjected) || metadata.Manifest.Generation == "" {
		t.Fatalf("ActivateCredential() = %+v, error = %v", metadata, err)
	}
	identityRoot := filepath.Join(store.Root(), identitiesDirName, "agent")
	tombstones, err := filepath.Glob(filepath.Join(identityRoot, ".pending-tombstone-*"))
	if err != nil || len(tombstones) != 1 {
		t.Fatalf("committed tombstones = %v, error = %v", tombstones, err)
	}
	if err := os.Remove(filepath.Join(tombstones[0], pendingJournalName)); err != nil {
		t.Fatalf("simulate partial tombstone deletion: %v", err)
	}
	if pendingValues, err := store.PendingEnrollments(); err != nil || len(pendingValues) != 0 {
		t.Fatalf("tombstone leaked into replay set: %+v, error = %v", pendingValues, err)
	}

	reopened, err := NewStore(dataRoot, WithClock(func() time.Time { return now.Add(time.Minute) }))
	if err != nil {
		t.Fatalf("reopen after partial tombstone cleanup: %v", err)
	}
	if _, err := os.Lstat(tombstones[0]); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("partial tombstone survived startup recovery: %v", err)
	}
	if _, err := reopened.LoadPending("agent"); !errors.Is(err, ErrPendingNotFound) {
		t.Fatalf("LoadPending() after committed cleanup error = %v", err)
	}
	if active, err := reopened.LoadActiveCredential("agent"); err != nil || active.Manifest.Generation != metadata.Manifest.Generation {
		t.Fatalf("reopened active credential = %+v, error = %v", active, err)
	}
	if _, err := reopened.ActivateCredential(context.Background(), request); err != nil {
		t.Fatalf("committed response reconciliation error = %v", err)
	}
}

func TestConcurrentRevocationWaitsForCredentialCutoverBarrier(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 2, 8, 0, 0, 0, time.UTC)
	selected := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	store, err := NewStore(t.TempDir(), WithClock(func() time.Time { return now }), withPersistenceCheckpoint(func(point string) error {
		if point == "credential.security_selected" {
			once.Do(func() { close(selected) })
			<-release
		}
		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	authority := newTestAuthority(t, now, "authority-1", 1)
	expectation := credentialContractAgentExpectation(now)
	pending := prepareKnownAgent(t, store, expectation)
	credential := authority.issueCredential(t, pending, expectation, "identity-1", "certificate-1", now)
	initial := authority.snapshot(t, "domain-1", 1, 0, true, nil, nil, now)
	revoked := authority.snapshot(t, "domain-1", 1, 1, false, []string{"identity-1"}, nil, now.Add(time.Minute))

	activationDone := make(chan error, 1)
	go func() {
		_, err := store.ActivateCredential(context.Background(), ActivateRequest{
			StorageIdentity: "agent", RequestID: pending.Request.RequestID,
			Credential: credential, Security: initial, Expectation: expectation,
		})
		activationDone <- err
	}()
	<-selected
	revocationAttempted := make(chan struct{})
	revocationDone := make(chan error, 1)
	go func() {
		close(revocationAttempted)
		_, err := store.ApplySecuritySnapshot(revoked)
		revocationDone <- err
	}()
	<-revocationAttempted
	if store.mu.TryLock() {
		store.mu.Unlock()
		close(release)
		<-activationDone
		<-revocationDone
		t.Fatal("credential cutover did not hold the authoritative Store lock at its security barrier")
	}
	close(release)
	if err := <-activationDone; err != nil {
		t.Fatalf("ActivateCredential() error = %v", err)
	}
	if err := <-revocationDone; err != nil {
		t.Fatalf("ApplySecuritySnapshot(revocation) error = %v", err)
	}
	if _, err := store.LoadActiveCredential("agent"); !errors.Is(err, ErrCredentialInvalid) {
		t.Fatalf("revoked active credential error = %v, want ErrCredentialInvalid", err)
	}
}

func credentialContractAgentExpectation(_ time.Time) CredentialExpectation {
	return CredentialExpectation{
		DomainID: "domain-1", AgentID: "agent-1", Kind: "agent",
		Purpose: model.PKICertificatePurposeClient,
	}
}
