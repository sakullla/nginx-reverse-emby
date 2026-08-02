package pki

import (
	"context"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

var errInjectedPersistenceLoss = errors.New("injected persistence boundary failure")

func TestPrepareEnrollmentReestablishesPublicationBarrierOnReplay(t *testing.T) {
	now := time.Date(2026, 8, 2, 8, 0, 0, 0, time.UTC)
	failed := false
	dataRoot := t.TempDir()
	store, err := NewStore(dataRoot, WithClock(func() time.Time { return now }), withPersistenceCheckpoint(func(point string) error {
		if point == "enrollment.after_publish" && !failed {
			failed = true
			return errInjectedPersistenceLoss
		}
		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	spec := EnrollmentSpec{StorageIdentity: "agent", Kind: model.PKIIdentityKindAgent, Purpose: model.PKICertificatePurposeClient}
	if _, err := store.PrepareEnrollment(context.Background(), spec); !errors.Is(err, errInjectedPersistenceLoss) {
		t.Fatalf("first PrepareEnrollment() error = %v", err)
	}
	replayed, err := store.PrepareEnrollment(context.Background(), spec)
	if err != nil {
		t.Fatalf("replayed PrepareEnrollment() error = %v", err)
	}
	reopened, err := NewStore(dataRoot, WithClock(func() time.Time { return now.Add(time.Minute) }))
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := reopened.LoadPending("agent")
	if err != nil || loaded.Request.RequestID != replayed.Request.RequestID || loaded.Request.CSRPEM != replayed.Request.CSRPEM {
		t.Fatalf("reopened pending = %+v, error = %v", loaded, err)
	}
}

func TestPendingEnrollmentRejectsJournalAndCSRBindingTampering(t *testing.T) {
	now := time.Date(2026, 8, 2, 8, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name   string
		mutate func(t *testing.T, pending *PendingEnrollment)
	}{
		{
			name: "request fingerprint",
			mutate: func(_ *testing.T, pending *PendingEnrollment) {
				pending.RequestFingerprint = strings.Repeat("0", sha256.Size*2)
			},
		},
		{
			name: "owner metadata with recomputed fingerprint",
			mutate: func(t *testing.T, pending *PendingEnrollment) {
				pending.AgentID = "agent-2"
				spec, err := normalizeEnrollmentSpec(EnrollmentSpec{
					StorageIdentity: pending.StorageIdentity, DomainID: pending.DomainID, AgentID: pending.AgentID,
					Kind: pending.Request.Kind, ListenerID: pending.Request.ListenerID, Purpose: pending.Request.Purpose,
					DNSNames: pending.Request.DNSNames, IPAddresses: pending.Request.IPAddresses,
				})
				if err != nil {
					t.Fatal(err)
				}
				pending.RequestFingerprint, err = enrollmentFingerprint(spec, pending.Request)
				if err != nil {
					t.Fatal(err)
				}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := newTestStore(t, now)
			pending, err := store.PrepareEnrollment(context.Background(), EnrollmentSpec{
				StorageIdentity: "agent", DomainID: "domain-1", AgentID: "agent-1",
				Kind: model.PKIIdentityKindAgent, Purpose: model.PKICertificatePurposeClient,
			})
			if err != nil {
				t.Fatal(err)
			}
			test.mutate(t, &pending)
			journal, err := json.Marshal(pending)
			if err != nil {
				t.Fatal(err)
			}
			journalPath := filepath.Join(store.Root(), identitiesDirName, "agent", pendingDirName, pendingJournalName)
			if err := os.WriteFile(journalPath, journal, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := store.LoadPending("agent"); !errors.Is(err, ErrCredentialInvalid) {
				t.Fatalf("LoadPending() error = %v, want ErrCredentialInvalid", err)
			}
		})
	}
}

func TestPendingEnrollmentsReturnsValidatedStablePublicCopies(t *testing.T) {
	now := time.Date(2026, 8, 2, 8, 0, 0, 0, time.UTC)
	store := newTestStore(t, now)
	for _, spec := range []EnrollmentSpec{
		{StorageIdentity: "listener_2", DomainID: "domain-1", AgentID: "agent-1", Kind: model.PKIIdentityKindListener, ListenerID: "listener-2", Purpose: model.PKICertificatePurposeServer, DNSNames: []string{"relay.example"}},
		{StorageIdentity: "agent", DomainID: "domain-1", AgentID: "agent-1", Kind: model.PKIIdentityKindAgent, Purpose: model.PKICertificatePurposeClient},
	} {
		if _, err := store.PrepareEnrollment(context.Background(), spec); err != nil {
			t.Fatal(err)
		}
	}
	pending, err := store.PendingEnrollments()
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 2 || pending[0].StorageIdentity != "agent" || pending[1].StorageIdentity != "listener_2" {
		t.Fatalf("PendingEnrollments() order = %+v", pending)
	}
	pending[0].Request.CSRPEM = "PRIVATE KEY"
	pending[0].Request.DNSNames = append(pending[0].Request.DNSNames, "mutated.example")
	reloaded, err := store.PendingEnrollments()
	if err != nil || strings.Contains(reloaded[0].Request.CSRPEM, "PRIVATE KEY") || len(reloaded[0].Request.DNSNames) != 0 {
		t.Fatalf("enumeration did not return independent public copies: %+v, error = %v", reloaded, err)
	}

	journalPath := filepath.Join(store.Root(), identitiesDirName, "listener_2", pendingDirName, pendingJournalName)
	journal, err := os.ReadFile(journalPath)
	if err != nil {
		t.Fatal(err)
	}
	journal = []byte(strings.Replace(string(journal), `"request_fingerprint_sha256":"`, `"request_fingerprint_sha256":"00`, 1))
	if err := os.WriteFile(journalPath, journal, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PendingEnrollments(); !errors.Is(err, ErrCredentialInvalid) {
		t.Fatalf("corrupt PendingEnrollments() error = %v", err)
	}
}

func TestStagedRegistrationErrorsDistinguishAbsentResponseFromCorruptPending(t *testing.T) {
	now := time.Date(2026, 8, 2, 8, 0, 0, 0, time.UTC)

	t.Run("no pending", func(t *testing.T) {
		store := newTestStore(t, now)
		if _, _, err := store.LoadStagedRegistration("agent"); !errors.Is(err, ErrPendingNotFound) || errors.Is(err, os.ErrNotExist) {
			t.Fatalf("LoadStagedRegistration() error = %v, want only ErrPendingNotFound", err)
		}
		if _, err := store.ActivateStagedRegistration(context.Background(), "agent"); !errors.Is(err, ErrPendingNotFound) {
			t.Fatalf("ActivateStagedRegistration() error = %v, want ErrPendingNotFound", err)
		}
	})

	t.Run("complete pending without response", func(t *testing.T) {
		store := newTestStore(t, now)
		if _, err := store.PrepareEnrollment(context.Background(), EnrollmentSpec{StorageIdentity: "agent"}); err != nil {
			t.Fatal(err)
		}
		if _, _, err := store.LoadStagedRegistration("agent"); !errors.Is(err, ErrStagedRegistrationNotFound) || errors.Is(err, ErrPendingNotFound) || errors.Is(err, os.ErrNotExist) {
			t.Fatalf("LoadStagedRegistration() error = %v, want only ErrStagedRegistrationNotFound", err)
		}
		if _, err := store.ActivateStagedRegistration(context.Background(), "agent"); !errors.Is(err, ErrStagedRegistrationNotFound) {
			t.Fatalf("ActivateStagedRegistration() error = %v, want ErrStagedRegistrationNotFound", err)
		}
	})

	for _, missing := range []string{pendingJournalName, pendingKeyName, pendingCSRName} {
		t.Run("missing "+missing, func(t *testing.T) {
			store := newTestStore(t, now)
			if _, err := store.PrepareEnrollment(context.Background(), EnrollmentSpec{StorageIdentity: "agent"}); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(store.Root(), identitiesDirName, "agent", pendingDirName, missing)
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if _, _, err := store.LoadStagedRegistration("agent"); !errors.Is(err, ErrCredentialInvalid) || errors.Is(err, os.ErrNotExist) || errors.Is(err, ErrStagedRegistrationNotFound) {
				t.Fatalf("LoadStagedRegistration() error = %v, want fail-closed ErrCredentialInvalid", err)
			}
		})
	}

	t.Run("corrupt response", func(t *testing.T) {
		store := newTestStore(t, now)
		if _, err := store.PrepareEnrollment(context.Background(), EnrollmentSpec{StorageIdentity: "agent"}); err != nil {
			t.Fatal(err)
		}
		responsePath := filepath.Join(store.Root(), identitiesDirName, "agent", pendingDirName, "response.json")
		if err := os.WriteFile(responsePath, []byte(`{"agent_id":`), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, _, err := store.LoadStagedRegistration("agent"); !errors.Is(err, ErrCredentialInvalid) || errors.Is(err, ErrStagedRegistrationNotFound) {
			t.Fatalf("LoadStagedRegistration() corrupt response error = %v, want ErrCredentialInvalid", err)
		}
	})
}

func TestStorageIdentityIsUnambiguousAcrossWindowsPathRules(t *testing.T) {
	store := newTestStore(t, time.Date(2026, 8, 2, 8, 0, 0, 0, time.UTC))
	for _, identity := range []string{"Agent", "agent.", "listener.name", "con", "NUL", "com1", "lpt9"} {
		if _, err := store.PrepareEnrollment(context.Background(), EnrollmentSpec{StorageIdentity: identity}); !errors.Is(err, ErrInvalidIdentity) {
			t.Fatalf("PrepareEnrollment(%q) error = %v, want ErrInvalidIdentity", identity, err)
		}
	}
	if _, err := store.PrepareEnrollment(context.Background(), EnrollmentSpec{StorageIdentity: "listener_1"}); err != nil {
		t.Fatalf("canonical storage identity rejected: %v", err)
	}
}

func TestSecuritySnapshotCrashWindowsAreDeterministicallyRecoverable(t *testing.T) {
	now := time.Date(2026, 8, 2, 8, 0, 0, 0, time.UTC)
	authority := newTestAuthority(t, now, "authority-1", 1)
	snapshot := authority.snapshot(t, "domain-1", 1, 0, true, nil, nil, now)
	for _, point := range []string{"security.after_state_publish", "security.after_pointer_publish"} {
		t.Run(point, func(t *testing.T) {
			failed := false
			dataRoot := t.TempDir()
			store, err := NewStore(dataRoot, WithClock(func() time.Time { return now }), withPersistenceCheckpoint(func(actual string) error {
				if actual == point && !failed {
					failed = true
					return errInjectedPersistenceLoss
				}
				return nil
			}))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := store.ApplySecuritySnapshot(snapshot); !errors.Is(err, errInjectedPersistenceLoss) {
				t.Fatalf("ApplySecuritySnapshot() error = %v", err)
			}
			reopened, err := NewStore(dataRoot, WithClock(func() time.Time { return now.Add(time.Hour) }))
			if err != nil {
				t.Fatal(err)
			}
			state, err := reopened.ApplySecuritySnapshot(snapshot)
			if err != nil {
				t.Fatalf("recovered ApplySecuritySnapshot() error = %v", err)
			}
			if !state.ActivatedAt.Equal(now) {
				t.Fatalf("recovered ActivatedAt = %s, want immutable %s", state.ActivatedAt, now)
			}
			loaded, err := reopened.LoadSecuritySnapshot()
			if err != nil || loaded.Hash != state.Hash {
				t.Fatalf("LoadSecuritySnapshot() = %+v, error = %v", loaded, err)
			}
		})
	}
}

func TestSecurityStoreCorruptionCannotResetTrustOrAdvanceBeforeRecovery(t *testing.T) {
	now := time.Date(2026, 8, 2, 8, 0, 0, 0, time.UTC)
	authority := newTestAuthority(t, now, "authority-1", 1)
	initial := authority.snapshot(t, "domain-1", 1, 0, true, nil, nil, now)

	t.Run("missing pointer", func(t *testing.T) {
		store := newTestStore(t, now)
		if _, err := store.ApplySecuritySnapshot(initial); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(filepath.Join(store.Root(), securityDirName, activePointerName)); err != nil {
			t.Fatal(err)
		}
		higher := authority.snapshot(t, "domain-1", 1, 1, false, nil, nil, now.Add(time.Minute))
		if _, err := store.ApplySecuritySnapshot(higher); !errors.Is(err, ErrSecurityInvalid) {
			t.Fatalf("advance before recovery error = %v, want ErrSecurityInvalid", err)
		}
		other := newTestAuthority(t, now, "other-authority", 2)
		unrelated := other.snapshot(t, "other-domain", 0, 0, true, nil, nil, now)
		if _, err := store.ApplySecuritySnapshot(unrelated); err == nil {
			t.Fatal("unrelated bootstrap reset was accepted")
		}
		if _, err := store.ApplySecuritySnapshot(initial); err != nil {
			t.Fatalf("exact known-state recovery error = %v", err)
		}
		if _, err := store.ApplySecuritySnapshot(higher); err != nil {
			t.Fatalf("post-recovery advance error = %v", err)
		}
	})

	t.Run("missing pointer target", func(t *testing.T) {
		store := newTestStore(t, now)
		state, err := store.ApplySecuritySnapshot(initial)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(filepath.Join(store.Root(), securityDirName, "snapshots", securityStateFileName(state))); err != nil {
			t.Fatal(err)
		}
		if _, err := store.ApplySecuritySnapshot(initial); !errors.Is(err, ErrSecurityInvalid) {
			t.Fatalf("missing target replay error = %v, want ErrSecurityInvalid", err)
		}
	})
}

func TestSecuritySnapshotRejectsMalformedSerialAndRetiringSignerResurrection(t *testing.T) {
	now := time.Date(2026, 8, 2, 8, 0, 0, 0, time.UTC)
	authority1 := newTestAuthority(t, now, "authority-1", 1)
	authority2 := newTestAuthority(t, now, "authority-2", 2)

	malformed := signedSnapshot(t, authority1, []model.PKITrustRoot{authority1.root}, "domain-1", 1, 0, true, nil, []string{"not-hex"}, now)
	if _, err := newTestStore(t, now).ApplySecuritySnapshot(malformed); !errors.Is(err, ErrSecurityInvalid) {
		t.Fatalf("malformed serial error = %v, want ErrSecurityInvalid", err)
	}

	prepared := authority2.root
	prepared.Status = "prepared"
	initial := signedSnapshot(t, authority1, []model.PKITrustRoot{authority1.root, prepared}, "domain-1", 1, 0, true, nil, nil, now)
	retiring := authority1.root
	retiring.Status = "retiring"
	cutover := signedSnapshot(t, authority2, []model.PKITrustRoot{retiring, authority2.root}, "domain-1", 1, 1, false, nil, nil, now.Add(time.Minute))
	resurrected := authority1.root
	retiring2 := authority2.root
	retiring2.Status = "retiring"
	attack := signedSnapshot(t, authority1, []model.PKITrustRoot{resurrected, retiring2}, "domain-1", 1, 2, false, nil, nil, now.Add(2*time.Minute))
	store := newTestStore(t, now.Add(3*time.Minute))
	if _, err := store.ApplySecuritySnapshot(initial); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ApplySecuritySnapshot(cutover); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ApplySecuritySnapshot(attack); !errors.Is(err, ErrSecurityInvalid) {
		t.Fatalf("retiring signer resurrection error = %v, want ErrSecurityInvalid", err)
	}
}

func TestCredentialGenerationRecoversAcrossSecurityRevisionAdvance(t *testing.T) {
	now := time.Date(2026, 8, 2, 8, 0, 0, 0, time.UTC)
	failed := false
	store, err := NewStore(t.TempDir(), WithClock(func() time.Time { return now }), withPersistenceCheckpoint(func(point string) error {
		if point == "credential.after_generation_publish" && !failed {
			failed = true
			return errInjectedPersistenceLoss
		}
		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	authority := newTestAuthority(t, now, "authority-1", 1)
	expectation := testAgentExpectation(now)
	pending := prepareKnownAgent(t, store, expectation)
	credential := authority.issueCredential(t, pending, expectation, "identity-1", "certificate-1", now)
	initial := authority.snapshot(t, "domain-1", 1, 0, true, nil, nil, now)
	if _, err := store.ActivateCredential(context.Background(), ActivateRequest{StorageIdentity: "agent", RequestID: pending.Request.RequestID, Credential: credential, Security: initial, Expectation: expectation}); !errors.Is(err, errInjectedPersistenceLoss) {
		t.Fatalf("first ActivateCredential() error = %v", err)
	}
	higher := authority.snapshot(t, "domain-1", 1, 1, false, nil, nil, now.Add(time.Minute))
	if _, err := store.ApplySecuritySnapshot(higher); err != nil {
		t.Fatal(err)
	}
	metadata, err := store.ActivateCredential(context.Background(), ActivateRequest{StorageIdentity: "agent", RequestID: pending.Request.RequestID, Credential: credential, Security: higher, Expectation: expectation})
	if err != nil {
		t.Fatalf("replayed ActivateCredential() error = %v", err)
	}
	if metadata.Manifest.SecurityRevision != 1 || metadata.Manifest.SecuritySnapshotHash == "" {
		t.Fatalf("replayed credential manifest = %+v", metadata.Manifest)
	}
}

func TestCredentialPostPointerFailuresReturnCommittedResultAndReconcile(t *testing.T) {
	now := time.Date(2026, 8, 2, 8, 0, 0, 0, time.UTC)
	for _, point := range []string{"credential.after_pointer_publish", "credential.after_ack_publish", "credential.after_pending_remove"} {
		t.Run(point, func(t *testing.T) {
			failed := false
			store, err := NewStore(t.TempDir(), WithClock(func() time.Time { return now }), withPersistenceCheckpoint(func(actual string) error {
				if actual == point && !failed {
					failed = true
					return errInjectedPersistenceLoss
				}
				return nil
			}))
			if err != nil {
				t.Fatal(err)
			}
			authority := newTestAuthority(t, now, "authority-1", 1)
			expectation := testAgentExpectation(now)
			pending := prepareKnownAgent(t, store, expectation)
			credential := authority.issueCredential(t, pending, expectation, "identity-1", "certificate-1", now)
			request := ActivateRequest{StorageIdentity: "agent", RequestID: pending.Request.RequestID, Credential: credential, Security: authority.snapshot(t, "domain-1", 1, 0, true, nil, nil, now), Expectation: expectation}
			metadata, err := store.ActivateCredential(context.Background(), request)
			if !errors.Is(err, ErrActivationCommitted) || metadata.Manifest.Credential.CertificateID != "certificate-1" {
				t.Fatalf("ActivateCredential() = %+v, error = %v", metadata, err)
			}
			loaded, loadErr := store.LoadActiveCredential("agent")
			if loadErr != nil || loaded.Manifest.Generation != metadata.Manifest.Generation {
				t.Fatalf("committed LoadActiveCredential() = %+v, error = %v", loaded, loadErr)
			}
			if _, err := store.ActivateCredential(context.Background(), request); err != nil {
				t.Fatalf("reconcile ActivateCredential() error = %v", err)
			}
			ack, err := store.SecurityAcknowledgement("agent")
			if err != nil || ack.CertificateID != "certificate-1" {
				t.Fatalf("reconciled acknowledgement = %+v, error = %v", ack, err)
			}
		})
	}
}

func TestSecurityAcknowledgementIsDerivedAndReadOnlyWhenUnchanged(t *testing.T) {
	now := time.Date(2026, 8, 2, 8, 0, 0, 0, time.UTC)
	ackWrites := 0
	store, err := NewStore(t.TempDir(), WithClock(func() time.Time { return now }), withPersistenceCheckpoint(func(point string) error {
		if point == "credential.after_ack_publish" {
			ackWrites++
		}
		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	authority := newTestAuthority(t, now, "authority-1", 1)
	expectation := testAgentExpectation(now)
	pending := prepareKnownAgent(t, store, expectation)
	credential := authority.issueCredential(t, pending, expectation, "identity-1", "certificate-1", now)
	initial := authority.snapshot(t, "domain-1", 1, 0, true, nil, nil, now)
	if _, err := store.ActivateCredential(context.Background(), ActivateRequest{StorageIdentity: "agent", RequestID: pending.Request.RequestID, Credential: credential, Security: initial, Expectation: expectation}); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		ack, err := store.SecurityAcknowledgement("agent")
		if err != nil || ack.CertificateID != "certificate-1" {
			t.Fatalf("SecurityAcknowledgement() = %+v, error = %v", ack, err)
		}
	}
	if ackWrites != 1 {
		t.Fatalf("unchanged acknowledgement writes = %d, want 1 activation write", ackWrites)
	}
	higher := authority.snapshot(t, "domain-1", 1, 1, false, nil, nil, now.Add(time.Minute))
	if _, err := store.ApplySecuritySnapshot(higher); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SecurityAcknowledgement("agent"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SecurityAcknowledgement("agent"); err != nil {
		t.Fatal(err)
	}
	if ackWrites != 2 {
		t.Fatalf("advanced acknowledgement writes = %d, want one additional durable write", ackWrites)
	}
}

func TestCredentialExpectationIsBoundToDurablePendingOwner(t *testing.T) {
	now := time.Date(2026, 8, 2, 8, 0, 0, 0, time.UTC)
	store := newTestStore(t, now)
	authority := newTestAuthority(t, now, "authority-1", 1)
	durable := testAgentExpectation(now)
	pending := prepareKnownAgent(t, store, durable)
	wrong := durable
	wrong.AgentID = "agent-2"
	credential := authority.issueCredential(t, pending, wrong, "identity-2", "certificate-2", now)
	if _, err := store.ActivateCredential(context.Background(), ActivateRequest{
		StorageIdentity: "agent", RequestID: pending.Request.RequestID, Credential: credential,
		Security: authority.snapshot(t, "domain-1", 1, 0, true, nil, nil, now), Expectation: wrong,
	}); !errors.Is(err, ErrPendingConflict) {
		t.Fatalf("cross-agent activation error = %v, want ErrPendingConflict", err)
	}
}

func TestListenerCredentialValidatesSANsAndInstallsServerCallback(t *testing.T) {
	now := time.Date(2026, 8, 2, 8, 0, 0, 0, time.UTC)
	store := newTestStore(t, now)
	authority := newTestAuthority(t, now, "authority-1", 1)
	expectation := CredentialExpectation{
		DomainID: "domain-1", AgentID: "agent-1", Kind: model.PKIIdentityKindListener,
		ListenerID: "listener-1", Purpose: model.PKICertificatePurposeServer,
		DNSNames: []string{"relay.example"}, IPAddresses: []string{"127.0.0.1"}, Now: now,
	}
	pending, err := store.PrepareEnrollment(context.Background(), EnrollmentSpec{
		StorageIdentity: "listener_1", DomainID: expectation.DomainID, AgentID: expectation.AgentID,
		Kind: expectation.Kind, ListenerID: expectation.ListenerID, Purpose: expectation.Purpose,
		DNSNames: expectation.DNSNames, IPAddresses: expectation.IPAddresses,
	})
	if err != nil {
		t.Fatal(err)
	}
	credential := authority.issueCredential(t, pending, expectation, "identity-listener", "certificate-listener", now)
	if _, err := store.ActivateCredential(context.Background(), ActivateRequest{
		StorageIdentity: "listener_1", RequestID: pending.Request.RequestID, Credential: credential,
		Security: authority.snapshot(t, "domain-1", 1, 0, true, nil, nil, now), Expectation: expectation,
	}); err != nil {
		t.Fatal(err)
	}
	config := &tls.Config{MinVersion: tls.VersionTLS13}
	if _, err := store.InstallTLSCertificate("listener_1", config); err != nil || config.GetCertificate == nil || config.GetClientCertificate != nil {
		t.Fatalf("InstallTLSCertificate(listener) error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(store.Root(), securityDirName, acknowledgementName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("listener activation wrote the agent security acknowledgement: %v", err)
	}
	if _, err := store.SecurityAcknowledgement("listener_1"); !errors.Is(err, ErrActiveCredential) {
		t.Fatalf("listener SecurityAcknowledgement() error = %v, want ErrActiveCredential", err)
	}

	otherStore := newTestStore(t, now)
	otherPending, err := otherStore.PrepareEnrollment(context.Background(), EnrollmentSpec{
		StorageIdentity: "listener_1", DomainID: expectation.DomainID, AgentID: expectation.AgentID,
		Kind: expectation.Kind, ListenerID: expectation.ListenerID, Purpose: expectation.Purpose,
		DNSNames: expectation.DNSNames, IPAddresses: expectation.IPAddresses,
	})
	if err != nil {
		t.Fatal(err)
	}
	wrong := expectation
	wrong.ListenerID = "listener-2"
	wrongCredential := authority.issueCredential(t, otherPending, wrong, "identity-wrong-listener", "certificate-wrong-listener", now)
	if _, err := otherStore.ActivateCredential(context.Background(), ActivateRequest{
		StorageIdentity: "listener_1", RequestID: otherPending.Request.RequestID, Credential: wrongCredential,
		Security: authority.snapshot(t, "domain-1", 1, 0, true, nil, nil, now), Expectation: wrong,
	}); !errors.Is(err, ErrPendingConflict) {
		t.Fatalf("cross-listener activation error = %v, want ErrPendingConflict", err)
	}
}

func TestConcurrentRevocationCannotInterleaveCredentialCutover(t *testing.T) {
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
	expectation := testAgentExpectation(now)
	pending := prepareKnownAgent(t, store, expectation)
	credential := authority.issueCredential(t, pending, expectation, "identity-1", "certificate-1", now)
	initial := authority.snapshot(t, "domain-1", 1, 0, true, nil, nil, now)
	higher := authority.snapshot(t, "domain-1", 1, 1, false, []string{"identity-1"}, nil, now.Add(time.Minute))
	activationDone := make(chan error, 1)
	go func() {
		_, activateErr := store.ActivateCredential(context.Background(), ActivateRequest{StorageIdentity: "agent", RequestID: pending.Request.RequestID, Credential: credential, Security: initial, Expectation: expectation})
		activationDone <- activateErr
	}()
	<-selected
	revocationDone := make(chan error, 1)
	go func() {
		_, applyErr := store.ApplySecuritySnapshot(higher)
		revocationDone <- applyErr
	}()
	select {
	case err := <-revocationDone:
		t.Fatalf("revocation interleaved while cutover lock was held: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	close(release)
	if err := <-activationDone; err != nil {
		t.Fatalf("ActivateCredential() error = %v", err)
	}
	if err := <-revocationDone; err != nil {
		t.Fatalf("ApplySecuritySnapshot(revocation) error = %v", err)
	}
	if _, err := store.LoadActiveCredential("agent"); !errors.Is(err, ErrCredentialInvalid) {
		t.Fatalf("revoked active credential load error = %v, want ErrCredentialInvalid", err)
	}
	if _, err := store.SecurityAcknowledgement("agent"); !errors.Is(err, ErrCredentialInvalid) {
		t.Fatalf("revoked credential acknowledgement error = %v, want ErrCredentialInvalid", err)
	}
}

func TestCredentialValidationMatrixKeepsPreviousGeneration(t *testing.T) {
	now := time.Date(2026, 8, 2, 8, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name   string
		mutate func(t *testing.T, authority testAuthority, pending PendingEnrollment, expectation CredentialExpectation, credential *model.PKITunnelCredential, snapshot *model.PKISecuritySnapshot)
	}{
		{name: "public key fingerprint", mutate: func(_ *testing.T, _ testAuthority, _ PendingEnrollment, _ CredentialExpectation, credential *model.PKITunnelCredential, _ *model.PKISecuritySnapshot) {
			credential.PublicKeyFingerprint = strings.Repeat("0", 64)
		}},
		{name: "private key mismatch", mutate: func(t *testing.T, authority testAuthority, _ PendingEnrollment, expectation CredentialExpectation, credential *model.PKITunnelCredential, _ *model.PKISecuritySnapshot) {
			otherStore := newTestStore(t, now)
			otherPending := prepareKnownAgent(t, otherStore, expectation)
			*credential = authority.issueCredential(t, otherPending, expectation, "identity-new", "certificate-new", now)
		}},
		{name: "untrusted chain", mutate: func(t *testing.T, _ testAuthority, pending PendingEnrollment, expectation CredentialExpectation, credential *model.PKITunnelCredential, _ *model.PKISecuritySnapshot) {
			other := newTestAuthority(t, now, "authority-2", 2)
			*credential = other.issueCredential(t, pending, expectation, "identity-new", "certificate-new", now)
		}},
		{name: "wrong EKU", mutate: func(t *testing.T, authority testAuthority, pending PendingEnrollment, expectation CredentialExpectation, credential *model.PKITunnelCredential, _ *model.PKISecuritySnapshot) {
			*credential = authority.issueCredentialWithMutator(t, pending, expectation, "identity-new", "certificate-new", now, func(template *x509.Certificate) {
				template.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
			})
		}},
		{name: "wrong URI", mutate: func(t *testing.T, authority testAuthority, pending PendingEnrollment, expectation CredentialExpectation, credential *model.PKITunnelCredential, _ *model.PKISecuritySnapshot) {
			*credential = authority.issueCredentialWithMutator(t, pending, expectation, "identity-new", "certificate-new", now, func(template *x509.Certificate) {
				otherURI, err := url.Parse("spiffe://domain-1/agent/agent-2")
				if err != nil {
					t.Fatal(err)
				}
				template.Subject.CommonName = otherURI.String()
				template.URIs = []*url.URL{otherURI}
			})
		}},
		{name: "unexpected SAN", mutate: func(t *testing.T, authority testAuthority, pending PendingEnrollment, expectation CredentialExpectation, credential *model.PKITunnelCredential, _ *model.PKISecuritySnapshot) {
			*credential = authority.issueCredentialWithMutator(t, pending, expectation, "identity-new", "certificate-new", now, func(template *x509.Certificate) {
				template.DNSNames = []string{"unexpected.example"}
			})
		}},
		{name: "CA generation", mutate: func(_ *testing.T, _ testAuthority, _ PendingEnrollment, _ CredentialExpectation, credential *model.PKITunnelCredential, _ *model.PKISecuritySnapshot) {
			credential.CAGeneration++
		}},
		{name: "identity revocation", mutate: func(t *testing.T, authority testAuthority, _ PendingEnrollment, _ CredentialExpectation, credential *model.PKITunnelCredential, snapshot *model.PKISecuritySnapshot) {
			*snapshot = authority.snapshot(t, "domain-1", 1, 1, false, []string{credential.IdentityID}, nil, now.Add(time.Minute))
		}},
		{name: "serial revocation", mutate: func(t *testing.T, authority testAuthority, _ PendingEnrollment, _ CredentialExpectation, credential *model.PKITunnelCredential, snapshot *model.PKISecuritySnapshot) {
			leaf, _ := parseCertificatePEM(credential.CertificatePEM)
			*snapshot = authority.snapshot(t, "domain-1", 1, 1, false, nil, []string{leaf.SerialNumber.Text(16)}, now.Add(time.Minute))
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := newTestStore(t, now)
			authority := newTestAuthority(t, now, "authority-1", 1)
			expectation := testAgentExpectation(now)
			baselinePending := prepareKnownAgent(t, store, expectation)
			baselineCredential := authority.issueCredential(t, baselinePending, expectation, "identity-old", "certificate-old", now)
			initial := authority.snapshot(t, "domain-1", 1, 0, true, nil, nil, now)
			baseline, err := store.ActivateCredential(context.Background(), ActivateRequest{StorageIdentity: "agent", RequestID: baselinePending.Request.RequestID, Credential: baselineCredential, Security: initial, Expectation: expectation})
			if err != nil {
				t.Fatal(err)
			}
			pending := prepareKnownAgent(t, store, expectation)
			credential := authority.issueCredential(t, pending, expectation, "identity-new", "certificate-new", now)
			snapshot := initial
			test.mutate(t, authority, pending, expectation, &credential, &snapshot)
			if _, err := store.ActivateCredential(context.Background(), ActivateRequest{StorageIdentity: "agent", RequestID: pending.Request.RequestID, Credential: credential, Security: snapshot, Expectation: expectation}); !errors.Is(err, ErrCredentialInvalid) {
				t.Fatalf("invalid activation error = %v, want ErrCredentialInvalid", err)
			}
			active, err := store.LoadActiveCredential("agent")
			if err != nil || active.Manifest.Generation != baseline.Manifest.Generation {
				t.Fatalf("previous generation changed: %+v, error = %v", active.Manifest, err)
			}
		})
	}
}

func TestActiveCredentialInternalValueCannotSerializePrivateKey(t *testing.T) {
	now := time.Date(2026, 8, 2, 8, 0, 0, 0, time.UTC)
	store := newTestStore(t, now)
	authority := newTestAuthority(t, now, "authority-1", 1)
	expectation := testAgentExpectation(now)
	pending := prepareKnownAgent(t, store, expectation)
	credential := authority.issueCredential(t, pending, expectation, "identity-1", "certificate-1", now)
	if _, err := store.ActivateCredential(context.Background(), ActivateRequest{StorageIdentity: "agent", RequestID: pending.Request.RequestID, Credential: credential, Security: authority.snapshot(t, "domain-1", 1, 0, true, nil, nil, now), Expectation: expectation}); err != nil {
		t.Fatal(err)
	}
	store.mu.Lock()
	active, err := store.loadActiveCredentialLocked("agent")
	store.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(active)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "PRIVATE KEY") || strings.Contains(string(encoded), `"tlsCertificate"`) || strings.Contains(string(encoded), `"PrivateKey"`) {
		t.Fatalf("serialized active credential leaked key-bearing fields: %s", encoded)
	}
}

func testAgentExpectation(now time.Time) CredentialExpectation {
	return CredentialExpectation{DomainID: "domain-1", AgentID: "agent-1", Kind: model.PKIIdentityKindAgent, Purpose: model.PKICertificatePurposeClient, Now: now}
}

func signedSnapshot(t *testing.T, signer testAuthority, roots []model.PKITrustRoot, domain string, epoch, revision int64, full bool, revokedIdentities, revokedSerials []string, issuedAt time.Time) model.PKISecuritySnapshot {
	t.Helper()
	roots = slices.Clone(roots)
	slices.SortFunc(roots, func(left, right model.PKITrustRoot) int {
		if left.Generation < right.Generation {
			return -1
		}
		if left.Generation > right.Generation {
			return 1
		}
		return 0
	})
	identities := slices.Clone(revokedIdentities)
	serials := slices.Clone(revokedSerials)
	slices.Sort(identities)
	for index := range serials {
		serials[index] = strings.ToLower(strings.TrimSpace(serials[index]))
	}
	slices.Sort(serials)
	descriptors := make([]securityTrustDescriptor, len(roots))
	generations := make([]int64, len(roots))
	for index, root := range roots {
		descriptors[index] = securityTrustDescriptor{AuthorityID: root.AuthorityID, Generation: root.Generation, Status: root.Status, FingerprintSHA256: root.FingerprintSHA256, NotBefore: root.NotBefore.UTC(), NotAfter: root.NotAfter.UTC()}
		generations[index] = root.Generation
	}
	payload, err := json.Marshal(securitySignaturePayload{
		PKIDomainID: domain, Version: securitySnapshotVersion{Version: securityVersion{PKIEpoch: epoch, SecurityRevision: revision}, Full: full}, IssuedAt: issuedAt.UTC(),
		TrustGenerations: generations, TrustRoots: descriptors, RevokedIdentityIDs: identities, RevokedSerials: serials,
	})
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(payload)
	signature, err := ecdsa.SignASN1(rand.Reader, signer.key, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return model.PKISecuritySnapshot{PKIDomainID: domain, PKIEpoch: epoch, SecurityRevision: revision, Full: full, IssuedAt: issuedAt, TrustRoots: roots, RevokedIdentityIDs: identities, RevokedSerials: serials, SignerGeneration: signer.root.Generation, Signature: signature}
}
