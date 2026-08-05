package pki

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/asn1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

var errInjectedPersistenceLoss = errors.New("injected persistence boundary failure")

func TestPrepareEnrollmentReestablishesPublicationBarrierOnReplay(t *testing.T) {
	t.Parallel()
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
	reopened, err := NewStore(dataRoot, WithClock(func() time.Time { return now.Add(time.Minute) }))
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := reopened.PrepareEnrollment(context.Background(), spec)
	if err != nil {
		t.Fatalf("replayed PrepareEnrollment() error = %v", err)
	}
	loaded, err := reopened.LoadPending("agent")
	if err != nil || loaded.Request.RequestID != replayed.Request.RequestID || loaded.Request.CSRPEM != replayed.Request.CSRPEM {
		t.Fatalf("reopened pending = %+v, error = %v", loaded, err)
	}
}

func TestPendingEnrollmentRejectsJournalAndCSRBindingTampering(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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

func TestUnixStoreRejectsPrivatePathsOwnedByAnotherUID(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("Unix UID ownership contract")
	}
	uid, err := exec.Command("id", "-u").Output()
	if err != nil || strings.TrimSpace(string(uid)) != "0" {
		t.Skip("changing a fixture UID requires a root test process")
	}
	for _, test := range []struct {
		name string
		path func(*Store) string
	}{
		{name: "opened private file", path: func(store *Store) string {
			return filepath.Join(store.Root(), identitiesDirName, "agent", pendingDirName, pendingKeyName)
		}},
		{name: "pending directory", path: func(store *Store) string {
			return filepath.Join(store.Root(), identitiesDirName, "agent", pendingDirName)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := newTestStore(t, time.Date(2026, 8, 2, 8, 0, 0, 0, time.UTC))
			if _, err := store.PrepareEnrollment(context.Background(), EnrollmentSpec{StorageIdentity: "agent"}); err != nil {
				t.Fatal(err)
			}
			path := test.path(store)
			if output, err := exec.Command("chown", "1", path).CombinedOutput(); err != nil {
				t.Skipf("cannot change fixture owner: %v: %s", err, output)
			}
			if _, err := store.LoadPending("agent"); !errors.Is(err, ErrCredentialInvalid) {
				t.Fatalf("LoadPending() with foreign-owned path error = %v, want ErrCredentialInvalid", err)
			}
			if _, err := NewStore(store.dataRoot); err == nil {
				t.Fatal("NewStore() accepted a foreign-owned private path")
			}
		})
	}
}

func TestStoreRecoveryCleansKnownCrashStagingAndRejectsUnknownEntries(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 2, 8, 0, 0, 0, time.UTC)
	dataRoot := t.TempDir()
	store, err := NewStore(dataRoot, WithClock(func() time.Time { return now }))
	if err != nil {
		t.Fatal(err)
	}
	identityStaging := filepath.Join(store.Root(), identitiesDirName, ".agent-new-0123456789abcdef")
	snapshotStaging := filepath.Join(store.Root(), securityDirName, ".snapshots-new-fedcba9876543210")
	for _, path := range []string{identityStaging, snapshotStaging} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	activeTemporary := filepath.Join(store.Root(), securityDirName, ".active.json.tmp-0123456789abcdef")
	if err := writePrivateFile(activeTemporary, []byte("incomplete")); err != nil {
		t.Fatal(err)
	}
	store, err = NewStore(dataRoot, WithClock(func() time.Time { return now }))
	if err != nil {
		t.Fatalf("reopen with known directory staging: %v", err)
	}
	for _, path := range []string{identityStaging, snapshotStaging} {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("known staging path survived recovery %s: %v", path, err)
		}
	}
	if pending, err := store.PendingEnrollments(); err != nil || len(pending) != 0 {
		t.Fatalf("PendingEnrollments() after staging recovery = %+v, error = %v", pending, err)
	}
	authority := newTestAuthority(t, now, "authority-1", 1)
	initial := authority.snapshot(t, "domain-1", 1, 0, true, nil, nil, now)
	state, err := store.ApplySecuritySnapshot(initial)
	if err != nil {
		t.Fatalf("bootstrap after known active temporary: %v", err)
	}
	if _, err := os.Lstat(activeTemporary); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("known active temporary survived recovery: %v", err)
	}
	if err := os.Remove(filepath.Join(store.Root(), securityDirName, activePointerName)); err != nil {
		t.Fatal(err)
	}
	snapshotTemporary := filepath.Join(store.Root(), securityDirName, "snapshots", ".1-99-0123456789abcdef.json.tmp-fedcba9876543210")
	if err := writePrivateFile(snapshotTemporary, []byte("incomplete")); err != nil {
		t.Fatal(err)
	}
	higher := authority.snapshot(t, "domain-1", 1, 1, false, nil, nil, now.Add(time.Minute))
	if advanced, err := store.ApplySecuritySnapshot(higher); err != nil || advanced.Snapshot.SecurityRevision != 1 {
		t.Fatalf("recover immutable temporary and advance = %+v, error = %v", advanced, err)
	}
	if _, err := os.Lstat(snapshotTemporary); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("known immutable temporary survived recovery: %v", err)
	}
	unknown := filepath.Join(store.Root(), securityDirName, "snapshots", ".unknown.tmp-0123456789abcdef")
	if err := writePrivateFile(unknown, []byte("unknown")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(store.Root(), securityDirName, activePointerName)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ApplySecuritySnapshot(authority.snapshot(t, "domain-1", 1, 2, false, nil, nil, now.Add(2*time.Minute))); !errors.Is(err, ErrSecurityInvalid) {
		t.Fatalf("unknown recovery entry error = %v, want ErrSecurityInvalid", err)
	}
	_ = state
}

func TestStoreRecoveryRemovesSecretBearingEnrollmentAndGenerationCandidates(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 2, 8, 0, 0, 0, time.UTC)
	dataRoot := t.TempDir()
	store, err := NewStore(dataRoot, WithClock(func() time.Time { return now }))
	if err != nil {
		t.Fatal(err)
	}
	pending, err := store.PrepareEnrollment(context.Background(), EnrollmentSpec{StorageIdentity: "agent"})
	if err != nil {
		t.Fatal(err)
	}
	identityRoot := filepath.Join(store.Root(), identitiesDirName, "agent")
	pendingCandidate := filepath.Join(identityRoot, ".pending-0123456789abcdef")
	generationCandidate := filepath.Join(identityRoot, generationsDirName, ".candidate-fedcba9876543210")
	for _, candidate := range []struct {
		root string
		name string
	}{{pendingCandidate, pendingKeyName}, {generationCandidate, privateKeyName}} {
		if err := os.Mkdir(candidate.root, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := restrictPath(candidate.root, true); err != nil {
			t.Fatal(err)
		}
		if err := writePrivateFile(filepath.Join(candidate.root, candidate.name), []byte("abandoned-secret")); err != nil {
			t.Fatal(err)
		}
	}
	reopened, err := NewStore(dataRoot, WithClock(func() time.Time { return now.Add(time.Minute) }))
	if err != nil {
		t.Fatalf("reopen with crash candidates: %v", err)
	}
	for _, path := range []string{pendingCandidate, generationCandidate} {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("secret-bearing candidate survived recovery %s: %v", path, err)
		}
	}
	if replayed, err := reopened.LoadPending("agent"); err != nil || replayed.Request.RequestID != pending.Request.RequestID {
		t.Fatalf("candidate cleanup damaged published pending request: %+v, error = %v", replayed, err)
	}
}

func TestImmutablePublicationNeverReplacesExistingTarget(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := ensurePrivateDir(dir); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "snapshot.json")
	temporary := filepath.Join(dir, ".snapshot.json.tmp-0123456789abcdef")
	if err := writePrivateFile(target, []byte("first")); err != nil {
		t.Fatal(err)
	}
	if err := writePrivateFile(temporary, []byte("second")); err != nil {
		t.Fatal(err)
	}
	if err := publishImmutableFile(temporary, target); err == nil {
		t.Fatal("immutable publication replaced an existing target")
	}
	stored, err := readPrivateFile(target)
	if err != nil || string(stored) != "first" {
		t.Fatalf("immutable target = %q, error = %v", stored, err)
	}
}

func TestSecuritySnapshotCrashWindowsAreDeterministicallyRecoverable(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 2, 8, 0, 0, 0, time.UTC)
	authority := newTestAuthority(t, now, "authority-1", 1)
	snapshot := authority.snapshot(t, "domain-1", 1, 0, true, nil, nil, now)
	for _, point := range []string{"security.after_snapshots_publish", "security.after_state_publish", "security.after_pointer_publish"} {
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
			committed, err := store.ApplySecuritySnapshot(snapshot)
			if !errors.Is(err, errInjectedPersistenceLoss) {
				t.Fatalf("ApplySecuritySnapshot() error = %v", err)
			}
			if point == "security.after_pointer_publish" && (!errors.Is(err, ErrActivationCommitted) || committed.Hash == "") {
				t.Fatalf("post-pointer result = %+v, error = %v, want committed state", committed, err)
			}
			reopened, err := NewStore(dataRoot, WithClock(func() time.Time { return now.Add(time.Hour) }))
			if err != nil {
				t.Fatal(err)
			}
			state, err := reopened.ApplySecuritySnapshot(snapshot)
			if err != nil {
				t.Fatalf("recovered ApplySecuritySnapshot() error = %v", err)
			}
			wantActivatedAt := now
			if point == "security.after_snapshots_publish" {
				wantActivatedAt = now.Add(time.Hour)
			}
			if !state.ActivatedAt.Equal(wantActivatedAt) {
				t.Fatalf("recovered ActivatedAt = %s, want %s", state.ActivatedAt, wantActivatedAt)
			}
			loaded, err := reopened.LoadSecuritySnapshot()
			if err != nil || loaded.Hash != state.Hash {
				t.Fatalf("LoadSecuritySnapshot() = %+v, error = %v", loaded, err)
			}
		})
	}
}

func TestImmutableSnapshotDirectoryBarrierBlocksPointerAdvance(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 2, 8, 0, 0, 0, time.UTC)
	dataRoot := t.TempDir()
	authority := newTestAuthority(t, now, "authority-1", 1)
	initial := authority.snapshot(t, "domain-1", 1, 0, true, nil, nil, now)
	store, err := NewStore(dataRoot, WithClock(func() time.Time { return now }))
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := store.ApplySecuritySnapshot(initial)
	if err != nil {
		t.Fatal(err)
	}
	snapshotsRoot := filepath.Join(store.Root(), securityDirName, "snapshots")
	failed := false
	store, err = NewStore(dataRoot,
		WithClock(func() time.Time { return now.Add(time.Minute) }),
		withDirectorySync(func(path string) error {
			if filepath.Clean(path) == filepath.Clean(snapshotsRoot) && !failed {
				failed = true
				return errInjectedPersistenceLoss
			}
			return syncDirectory(path)
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	higher := authority.snapshot(t, "domain-1", 1, 1, false, nil, nil, now.Add(time.Minute))
	if _, err := store.ApplySecuritySnapshot(higher); !errors.Is(err, errInjectedPersistenceLoss) {
		t.Fatalf("ApplySecuritySnapshot() directory barrier error = %v", err)
	}
	if active, err := store.LoadSecuritySnapshot(); err != nil || active.Hash != baseline.Hash {
		t.Fatalf("failed immutable directory sync advanced pointer: %+v, error = %v", active, err)
	}
	reopened, err := NewStore(dataRoot, WithClock(func() time.Time { return now.Add(2 * time.Minute) }))
	if err != nil {
		t.Fatal(err)
	}
	advanced, err := reopened.ApplySecuritySnapshot(higher)
	if err != nil || advanced.Snapshot.SecurityRevision != 1 {
		t.Fatalf("retry did not rebuild immutable directory barrier: %+v, error = %v", advanced, err)
	}
}

func TestSecurityRecoveryCannotFallBelowDurableAcknowledgement(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 2, 8, 0, 0, 0, time.UTC)
	store := newTestStore(t, now.Add(2*time.Minute))
	authority := newTestAuthority(t, now, "authority-1", 1)
	expectation := testAgentExpectation(now)
	pending := prepareKnownAgent(t, store, expectation)
	credential := authority.issueCredential(t, pending, expectation, "identity-1", "certificate-1", now)
	initial := authority.snapshot(t, "domain-1", 1, 0, true, nil, nil, now)
	if _, err := store.ActivateCredential(context.Background(), ActivateRequest{
		StorageIdentity: "agent", RequestID: pending.Request.RequestID, Credential: credential,
		Security: initial, Expectation: expectation,
	}); err != nil {
		t.Fatal(err)
	}
	higher := authority.snapshot(t, "domain-1", 1, 1, false, nil, nil, now.Add(time.Minute))
	higherState, err := store.ApplySecuritySnapshot(higher)
	if err != nil {
		t.Fatal(err)
	}
	if acknowledgement, err := store.SecurityAcknowledgement("agent"); err != nil || acknowledgement.SecurityRevision != 1 {
		t.Fatalf("advanced acknowledgement = %+v, error = %v", acknowledgement, err)
	}
	ackPath := filepath.Join(store.Root(), securityDirName, acknowledgementName)
	ackBefore, err := os.ReadFile(ackPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(store.Root(), securityDirName, activePointerName)); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(store.Root(), securityDirName, "snapshots", securityStateFileName(higherState))); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ApplySecuritySnapshot(initial); !errors.Is(err, ErrSecurityDowngrade) {
		t.Fatalf("old snapshot recovery error = %v, want ErrSecurityDowngrade", err)
	}
	ackAfter, err := os.ReadFile(ackPath)
	if err != nil || !slices.Equal(ackAfter, ackBefore) {
		t.Fatalf("durable acknowledgement changed during failed recovery: before=%s after=%s error=%v", ackBefore, ackAfter, err)
	}
	if _, err := store.LoadSecuritySnapshot(); !errors.Is(err, ErrSecurityInvalid) {
		t.Fatalf("failed recovery published an old pointer: %v", err)
	}
}

func TestSecurityStoreCorruptionCannotResetTrustOrAdvanceBeforeRecovery(t *testing.T) {
	t.Parallel()
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
		advanced, err := store.ApplySecuritySnapshot(higher)
		if err != nil || advanced.Snapshot.SecurityRevision != 1 {
			t.Fatalf("newer-only recovery = %+v, error = %v", advanced, err)
		}
		other := newTestAuthority(t, now, "other-authority", 2)
		unrelated := other.snapshot(t, "other-domain", 0, 0, true, nil, nil, now)
		if _, err := store.ApplySecuritySnapshot(unrelated); err == nil {
			t.Fatal("unrelated bootstrap reset was accepted")
		}
		if loaded, err := store.LoadSecuritySnapshot(); err != nil || loaded.Hash != advanced.Hash {
			t.Fatalf("recovered active pointer = %+v, error = %v", loaded, err)
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
	t.Parallel()
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

func TestSecuritySnapshotEnforcesProducerCryptoProfiles(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 2, 8, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name      string
		authority func(t *testing.T) testAuthority
	}{
		{
			name: "short root serial",
			authority: func(t *testing.T) testAuthority {
				return newTestAuthorityWithProfile(t, now, "authority-short", 1, elliptic.P256(), func(template *x509.Certificate) {
					template.SerialNumber = big.NewInt(1)
				})
			},
		},
		{
			name: "non-P256 root",
			authority: func(t *testing.T) testAuthority {
				return newTestAuthorityWithProfile(t, now, "authority-p384", 1, elliptic.P384(), func(template *x509.Certificate) {
					template.SignatureAlgorithm = x509.ECDSAWithSHA384
				})
			},
		},
		{
			name: "non-SHA256 root signature",
			authority: func(t *testing.T) testAuthority {
				return newTestAuthorityWithProfile(t, now, "authority-sha384", 1, elliptic.P256(), func(template *x509.Certificate) {
					template.SignatureAlgorithm = x509.ECDSAWithSHA384
				})
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			authority := test.authority(t)
			snapshot := authority.snapshot(t, "domain-1", 1, 0, true, nil, nil, now)
			if _, err := newTestStore(t, now).ApplySecuritySnapshot(snapshot); !errors.Is(err, ErrSecurityInvalid) {
				t.Fatalf("ApplySecuritySnapshot() error = %v, want ErrSecurityInvalid", err)
			}
		})
	}
	valid := newTestAuthority(t, now, "authority-valid", 1)
	for _, serial := range []string{strings.Repeat("1", 32), strings.Repeat("8", 40)} {
		snapshot := signedSnapshot(t, valid, []model.PKITrustRoot{valid.root}, "domain-1", 1, 0, true, nil, []string{serial}, now)
		if _, err := newTestStore(t, now).ApplySecuritySnapshot(snapshot); !errors.Is(err, ErrSecurityInvalid) {
			t.Fatalf("out-of-profile revoked serial %q error = %v", serial, err)
		}
	}
}

func TestSecuritySnapshotKeepsRevocationsAndTrustLifecycleMonotonic(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 2, 8, 0, 0, 0, time.UTC)
	authority1 := newTestAuthority(t, now, "authority-1", 1)
	authority2 := newTestAuthority(t, now, "authority-2", 2)
	serial := "8" + strings.Repeat("0", 31)

	t.Run("revocations cannot disappear", func(t *testing.T) {
		store := newTestStore(t, now.Add(time.Hour))
		initial := authority1.snapshot(t, "domain-1", 1, 0, true, []string{"identity-1"}, []string{serial}, now)
		if _, err := store.ApplySecuritySnapshot(initial); err != nil {
			t.Fatal(err)
		}
		for _, test := range []struct {
			name       string
			identities []string
			serials    []string
		}{
			{name: "identity only", serials: []string{serial}},
			{name: "serial only", identities: []string{"identity-1"}},
		} {
			candidate := authority1.snapshot(t, "domain-1", 1, 1, false, test.identities, test.serials, now.Add(time.Minute))
			if _, err := store.ApplySecuritySnapshot(candidate); !errors.Is(err, ErrSecurityInvalid) {
				t.Fatalf("%s revocation removal error = %v, want ErrSecurityInvalid", test.name, err)
			}
			loaded, err := store.LoadSecuritySnapshot()
			if err != nil || !slices.Equal(loaded.Snapshot.RevokedIdentityIDs, []string{"identity-1"}) || !slices.Equal(loaded.Snapshot.RevokedSerials, []string{serial}) {
				t.Fatalf("durable revocations changed after %s removal: %+v, error = %v", test.name, loaded.Snapshot, err)
			}
		}
	})

	t.Run("new roots enter prepared and active roots cannot disappear", func(t *testing.T) {
		store := newTestStore(t, now.Add(time.Hour))
		if _, err := store.ApplySecuritySnapshot(authority1.snapshot(t, "domain-1", 1, 0, true, nil, nil, now)); err != nil {
			t.Fatal(err)
		}
		newRetiring := authority2.root
		newRetiring.Status = "retiring"
		candidate := signedSnapshot(t, authority1, []model.PKITrustRoot{authority1.root, newRetiring}, "domain-1", 1, 1, false, nil, nil, now.Add(time.Minute))
		if _, err := store.ApplySecuritySnapshot(candidate); !errors.Is(err, ErrSecurityInvalid) {
			t.Fatalf("new retiring root error = %v, want ErrSecurityInvalid", err)
		}
		newActive := authority2.root
		candidate = signedSnapshot(t, authority1, []model.PKITrustRoot{authority1.root, newActive}, "domain-1", 1, 1, false, nil, nil, now.Add(time.Minute))
		if _, err := store.ApplySecuritySnapshot(candidate); !errors.Is(err, ErrSecurityInvalid) {
			t.Fatalf("new active root error = %v, want ErrSecurityInvalid", err)
		}

		prepared := authority2.root
		prepared.Status = "prepared"
		preparedState := signedSnapshot(t, authority1, []model.PKITrustRoot{authority1.root, prepared}, "domain-1", 1, 1, false, nil, nil, now.Add(time.Minute))
		if _, err := store.ApplySecuritySnapshot(preparedState); err != nil {
			t.Fatalf("prepared root activation: %v", err)
		}
		preparedHash, err := store.LoadSecuritySnapshot()
		if err != nil {
			t.Fatal(err)
		}
		dualActive := signedSnapshot(t, authority2, []model.PKITrustRoot{authority1.root, authority2.root}, "domain-1", 1, 2, false, nil, nil, now.Add(2*time.Minute))
		if _, err := store.ApplySecuritySnapshot(dualActive); !errors.Is(err, ErrSecurityInvalid) {
			t.Fatalf("dual-active cutover error = %v, want ErrSecurityInvalid", err)
		}
		missingOldActive := signedSnapshot(t, authority2, []model.PKITrustRoot{authority2.root}, "domain-1", 1, 2, false, nil, nil, now.Add(2*time.Minute))
		if _, err := store.ApplySecuritySnapshot(missingOldActive); !errors.Is(err, ErrSecurityInvalid) {
			t.Fatalf("prepared promotion without old active error = %v, want ErrSecurityInvalid", err)
		}
		if unchanged, err := store.LoadSecuritySnapshot(); err != nil || unchanged.Hash != preparedHash.Hash {
			t.Fatalf("invalid lifecycle candidates changed durable state: %+v, error = %v", unchanged, err)
		}
		missingPrepared := authority1.snapshot(t, "domain-1", 1, 2, false, nil, nil, now.Add(2*time.Minute))
		if _, err := store.ApplySecuritySnapshot(missingPrepared); !errors.Is(err, ErrSecurityInvalid) {
			t.Fatalf("prepared root disappearance error = %v, want ErrSecurityInvalid", err)
		}
		active2 := authority2.root
		retiring1 := authority1.root
		retiring1.Status = "retiring"
		cutover := signedSnapshot(t, authority2, []model.PKITrustRoot{retiring1, active2}, "domain-1", 1, 2, false, nil, nil, now.Add(2*time.Minute))
		if _, err := store.ApplySecuritySnapshot(cutover); err != nil {
			t.Fatalf("prepared-to-active cutover: %v", err)
		}
		retiredRemoved := signedSnapshot(t, authority2, []model.PKITrustRoot{active2}, "domain-1", 1, 3, false, nil, nil, now.Add(3*time.Minute))
		if _, err := store.ApplySecuritySnapshot(retiredRemoved); err != nil {
			t.Fatalf("retiring root removal: %v", err)
		}
	})

	t.Run("bootstrap requires exactly one active root", func(t *testing.T) {
		candidate := signedSnapshot(t, authority1, []model.PKITrustRoot{authority1.root, authority2.root}, "domain-1", 1, 0, true, nil, nil, now)
		if _, err := newTestStore(t, now).ApplySecuritySnapshot(candidate); !errors.Is(err, ErrSecurityInvalid) {
			t.Fatalf("multi-active bootstrap error = %v, want ErrSecurityInvalid", err)
		}
	})
}

func TestSecuritySnapshotCanonicalizesEquivalentECDSASignatures(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 2, 8, 0, 0, 0, time.UTC)
	authority := newTestAuthority(t, now, "authority-1", 1)
	snapshot := authority.snapshot(t, "domain-1", 1, 0, true, nil, nil, now)
	alternate := cloneSecuritySnapshot(snapshot)
	alternate.Signature = equivalentECDSASignature(t, authority.key, snapshot.Signature)
	store := newTestStore(t, now)
	first, err := store.ApplySecuritySnapshot(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := store.ApplySecuritySnapshot(alternate)
	if err != nil || replayed.Hash != first.Hash {
		t.Fatalf("equivalent signature replay = %+v, error = %v, want hash %s", replayed, err, first.Hash)
	}
}

func TestBackendCanonicalSecurityPayloadGolden(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 2, 8, 0, 0, 0, time.UTC)
	payload, err := json.Marshal(backendSecurityPayloadFixture{
		PKIDomainID: "domain-1",
		Version: backendSecuritySnapshotVersionFixture{
			Version: backendSecurityVersionFixture{PKIEpoch: 7, SecurityRevision: 3}, Full: true,
		},
		IssuedAt:         now,
		TrustGenerations: []int64{1},
		TrustRoots: []backendSecurityTrustDescriptorFixture{{
			AuthorityID: "authority-1", Generation: 1, Status: "active",
			FingerprintSHA256: strings.Repeat("a", 64), NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour),
		}},
		RevokedIdentityIDs: []string{"identity-1"},
		RevokedSerials:     []string{"8" + strings.Repeat("0", 31)},
	})
	if err != nil {
		t.Fatal(err)
	}
	const golden = `{"pki_domain_id":"domain-1","version":{"version":{"pki_epoch":7,"security_revision":3},"full":true},"issued_at":"2026-08-02T08:00:00Z","trust_generations":[1],"trust_roots":[{"authority_id":"authority-1","generation":1,"status":"active","fingerprint_sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","not_before":"2026-08-02T07:00:00Z","not_after":"2026-08-02T09:00:00Z"}],"revoked_identity_ids":["identity-1"],"revoked_serials":["80000000000000000000000000000000"]}`
	if string(payload) != golden {
		t.Fatalf("backend canonical security payload = %s", payload)
	}
}

func testBackendProductionSignerEnvelopeIsConsumedByAgentVerifier(t *testing.T) {
	encoded := runBackendProductionSnapshotProbe(t)
	var snapshot model.PKISecuritySnapshot
	if err := decodeStrictJSON(encoded, &snapshot); err != nil {
		t.Fatalf("decode production backend envelope: %v\nenvelope: %s", err, encoded)
	}
	store := newTestStore(t, snapshot.IssuedAt)
	state, err := store.ApplySecuritySnapshot(snapshot)
	if err != nil {
		t.Fatalf("agent rejected production backend envelope: %v\nenvelope: %s", err, encoded)
	}
	if state.Snapshot.PKIDomainID != "domain-1" || state.Snapshot.PKIEpoch != 7 || state.Snapshot.SecurityRevision != 3 ||
		!slices.Equal(state.Snapshot.RevokedIdentityIDs, []string{"identity-1"}) {
		t.Fatalf("consumed production backend state = %+v", state.Snapshot)
	}
}

func testBackendProductionIssuerResponseActivatesInAgentStore(t *testing.T) {
	now := time.Date(2026, 8, 2, 8, 0, 0, 0, time.UTC)
	store := newTestStore(t, now)
	expectation := testAgentExpectation(now)
	pending := prepareKnownAgent(t, store, expectation)
	encoded := runBackendProductionCredentialProbe(t, pending.Request.CSRPEM)
	var response struct {
		Credential model.PKITunnelCredential `json:"credential"`
		Security   model.PKISecuritySnapshot `json:"security"`
	}
	if err := decodeStrictJSON(encoded, &response); err != nil {
		t.Fatalf("decode production backend enrollment response: %v\nresponse: %s", err, encoded)
	}
	active, err := store.ActivateCredential(context.Background(), ActivateRequest{
		StorageIdentity: "agent", RequestID: pending.Request.RequestID,
		Credential: response.Credential, Security: response.Security, Expectation: expectation,
	})
	if err != nil {
		t.Fatalf("agent rejected production-issued credential: %v\nresponse: %s", err, encoded)
	}
	if active.Manifest.Credential.CertificateID != "certificate-production" ||
		active.Manifest.Credential.IdentityID != "identity-production" {
		t.Fatalf("activated production credential = %+v", active.Manifest.Credential)
	}
}

// runBackendProductionSnapshotProbe executes the real backend projected
// signer/canonical marshaler from a temporary internal-package probe. This is
// deliberately cross-module: copying the producer payload shape into this
// package would let producer and consumer drift while both unit suites pass.
func runBackendProductionSnapshotProbe(t *testing.T) []byte {
	t.Helper()
	backendRoot, err := filepath.Abs(filepath.Join("..", "..", "..", "..", "panel", "backend-go"))
	if err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(filepath.Join(backendRoot, "go.mod")); err != nil || info.IsDir() {
		t.Fatalf("backend module is unavailable at %s: %v", backendRoot, err)
	}
	agentRoot := filepath.Clean(filepath.Join(backendRoot, "..", "..", "go-agent"))
	probeRoot := t.TempDir()
	goMod := fmt.Sprintf(`module github.com/sakullla/nginx-reverse-emby/panel/backend-go/contractprobe

go 1.26.5

require github.com/sakullla/nginx-reverse-emby/panel/backend-go v0.0.0

replace github.com/sakullla/nginx-reverse-emby/panel/backend-go => %q

replace github.com/sakullla/nginx-reverse-emby/go-agent => %q
`, filepath.ToSlash(backendRoot), filepath.ToSlash(agentRoot))
	if err := os.WriteFile(filepath.Join(probeRoot, "go.mod"), []byte(goMod), 0o600); err != nil {
		t.Fatal(err)
	}
	const source = `package main

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"os"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/service"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

type canonicalSource struct{}

func (canonicalSource) LoadPKICanonicalState(context.Context) (storage.PKICanonicalState, error) {
	return storage.PKICanonicalState{}, nil
}


type authoritySigner struct{ key *ecdsa.PrivateKey }

func (s authoritySigner) LoadSigner(context.Context, storage.PKIAuthorityRow) (crypto.Signer, error) {
	return s.key, nil
}

func main() {
	now := time.Date(2026, 8, 2, 8, 0, 0, 0, time.UTC)
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	must(err)
	serial := new(big.Int).Lsh(big.NewInt(1), 127)
	serial.Add(serial, big.NewInt(7))
	template := &x509.Certificate{
		SerialNumber: serial, Subject: pkix.Name{CommonName: "authority-1"},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(365 * 24 * time.Hour),
		IsCA: true, BasicConstraintsValid: true,
		KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		SignatureAlgorithm: x509.ECDSAWithSHA256,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	must(err)
	certificate, err := x509.ParseCertificate(der)
	must(err)
	fingerprint := sha256.Sum256(certificate.Raw)
	fingerprintHex := hex.EncodeToString(fingerprint[:])
	certificatePEM := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	authority := storage.PKIAuthorityRow{
		ID: "authority-1", PKIDomainID: "domain-1", Generation: 1, Status: "active",
		CertificatePEM: certificatePEM, FingerprintSHA256: fingerprintHex,
		NotBefore: certificate.NotBefore, NotAfter: certificate.NotAfter,
	}
	signer, err := service.NewPKIVaultSecuritySnapshotSigner(service.PKIVaultSecuritySnapshotSignerOptions{
		StateSource: canonicalSource{}, Signer: authoritySigner{key: key}, Random: rand.Reader,
	})
	must(err)
	descriptor := service.PKISecurityTrustRootDescriptor{
		AuthorityID: authority.ID, Generation: authority.Generation, Status: authority.Status,
		FingerprintSHA256: authority.FingerprintSHA256, NotBefore: authority.NotBefore, NotAfter: authority.NotAfter,
	}
	signed, err := signer.SignPKIProjectedSecuritySnapshot(context.Background(), service.PKIUnsignedSecuritySnapshot{
		PKIDomainID: "domain-1",
		Version: service.PKISecuritySnapshotVersion{
			Version: service.PKISecurityVersion{PKIEpoch: 7, SecurityRevision: 3}, Full: true,
		},
		IssuedAt: now, TrustGenerations: []int64{1}, TrustRoots: []service.PKISecurityTrustRootDescriptor{descriptor},
		RevokedIdentityIDs: []string{"identity-1"}, RevokedSerials: []string{"80000000000000000000000000000000"},
	}, authority)
	must(err)
	wire := storage.PKISecuritySnapshot{
		PKIDomainID: signed.PKIDomainID,
		PKIEpoch: signed.Version.Version.PKIEpoch, SecurityRevision: signed.Version.Version.SecurityRevision,
		Full: signed.Version.Full, IssuedAt: signed.IssuedAt,
		TrustRoots: []storage.PKITrustRoot{{
			AuthorityID: authority.ID, Generation: authority.Generation, Status: authority.Status,
			CertificatePEM: certificatePEM, FingerprintSHA256: authority.FingerprintSHA256,
			NotBefore: authority.NotBefore, NotAfter: authority.NotAfter,
		}},
		RevokedIdentityIDs: signed.RevokedIdentityIDs, RevokedSerials: signed.RevokedSerials,
		SignerGeneration: signed.SignerGeneration, Signature: signed.Signature,
	}
	must(json.NewEncoder(os.Stdout).Encode(wire))
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
`
	if err := os.WriteFile(filepath.Join(probeRoot, "main.go"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("go", "run", "-mod=mod", ".")
	command.Dir = probeRoot
	command.Env = append(os.Environ(), "GOWORK=off")
	output, err := command.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			t.Fatalf("run backend production snapshot probe: %v\n%s", err, exitErr.Stderr)
		}
		t.Fatalf("run backend production snapshot probe: %v", err)
	}
	return []byte(strings.TrimSpace(string(output)))
}

func runBackendProductionCredentialProbe(t *testing.T, csrPEM string) []byte {
	t.Helper()
	backendRoot, err := filepath.Abs(filepath.Join("..", "..", "..", "..", "panel", "backend-go"))
	if err != nil {
		t.Fatal(err)
	}
	probeRoot := t.TempDir()
	csrPath := filepath.Join(probeRoot, "request.csr.pem")
	if err := os.WriteFile(csrPath, []byte(csrPEM), 0o600); err != nil {
		t.Fatal(err)
	}
	const source = `package service

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

type nreAgentContractSource struct{}

func (nreAgentContractSource) LoadPKICanonicalState(context.Context) (storage.PKICanonicalState, error) {
	return storage.PKICanonicalState{}, nil
}

type nreAgentContractSigner struct{ key *ecdsa.PrivateKey }

func (s nreAgentContractSigner) LoadSigner(context.Context, storage.PKIAuthorityRow) (crypto.Signer, error) {
	return s.key, nil
}

func TestNREAgentCredentialContractProbe(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 2, 8, 0, 0, 0, time.UTC)
	csrEncoded, err := os.ReadFile(os.Getenv("NRE_AGENT_CONTRACT_CSR"))
	if err != nil {
		t.Fatal(err)
	}
	csr, err := parsePKIEnrollmentCSR(string(csrEncoded))
	if err != nil {
		t.Fatal(err)
	}
	binding, err := newPKIIdentityBinding(
		"domain-1", storage.PKIIdentityKindAgent, "agent-1", "", storage.PKICertificatePurposeClient, nil, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := validatePKIEnrollmentCSRBinding(csr, binding, false); err != nil {
		t.Fatal(err)
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serial := new(big.Int).Lsh(big.NewInt(1), 127)
	serial.Add(serial, big.NewInt(11))
	template := &x509.Certificate{
		SerialNumber: serial, Subject: pkix.Name{CommonName: "authority-1"},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(365 * 24 * time.Hour),
		IsCA: true, BasicConstraintsValid: true,
		KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		SignatureAlgorithm: x509.ECDSAWithSHA256,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	fingerprint := sha256.Sum256(certificate.Raw)
	fingerprintHex := hex.EncodeToString(fingerprint[:])
	certificatePEM := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	authority := storage.PKIAuthorityRow{
		ID: "authority-1", PKIDomainID: "domain-1", Generation: 1, Status: "active",
		CertificatePEM: certificatePEM, FingerprintSHA256: fingerprintHex,
		NotBefore: certificate.NotBefore, NotAfter: certificate.NotAfter,
	}
	issued, err := issuePKIIdentityCertificate(rand.Reader, now, 24*time.Hour, authority, false, key, csr, binding)
	if err != nil {
		t.Fatal(err)
	}
	credential := storage.PKITunnelCredential{
		IdentityID: "identity-production", CertificateID: "certificate-production", Purpose: issued.Purpose,
		CertificatePEM: issued.CertificatePEM, PublicKeyFingerprint: issued.PublicKeyFingerprint,
		AuthorityID: issued.AuthorityID, CAGeneration: issued.CAGeneration,
		NotBefore: issued.NotBefore, NotAfter: issued.NotAfter,
	}
	descriptor := PKISecurityTrustRootDescriptor{
		AuthorityID: authority.ID, Generation: authority.Generation, Status: authority.Status,
		FingerprintSHA256: authority.FingerprintSHA256, NotBefore: authority.NotBefore, NotAfter: authority.NotAfter,
	}
	snapshotSigner, err := NewPKIVaultSecuritySnapshotSigner(PKIVaultSecuritySnapshotSignerOptions{
		StateSource: nreAgentContractSource{}, Signer: nreAgentContractSigner{key: key}, Random: rand.Reader,
	})
	if err != nil {
		t.Fatal(err)
	}
	signed, err := snapshotSigner.SignPKIProjectedSecuritySnapshot(context.Background(), PKIUnsignedSecuritySnapshot{
		PKIDomainID: "domain-1",
		Version: PKISecuritySnapshotVersion{Version: PKISecurityVersion{PKIEpoch: 1, SecurityRevision: 0}, Full: true},
		IssuedAt: now, TrustGenerations: []int64{1}, TrustRoots: []PKISecurityTrustRootDescriptor{descriptor},
	}, authority)
	if err != nil {
		t.Fatal(err)
	}
	security := storage.PKISecuritySnapshot{
		PKIDomainID: signed.PKIDomainID,
		PKIEpoch: signed.Version.Version.PKIEpoch, SecurityRevision: signed.Version.Version.SecurityRevision,
		Full: signed.Version.Full, IssuedAt: signed.IssuedAt,
		TrustRoots: []storage.PKITrustRoot{{
			AuthorityID: authority.ID, Generation: authority.Generation, Status: authority.Status,
			CertificatePEM: certificatePEM, FingerprintSHA256: authority.FingerprintSHA256,
			NotBefore: authority.NotBefore, NotAfter: authority.NotAfter,
		}},
		SignerGeneration: signed.SignerGeneration, Signature: signed.Signature,
	}
	encoded, err := json.Marshal(map[string]any{"credential": credential, "security": security})
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("NRE_AGENT_CONTRACT_JSON=%s\n", encoded)
}
`
	backingPath := filepath.Join(probeRoot, "credential_contract_probe_test.go")
	if err := os.WriteFile(backingPath, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	virtualPath := filepath.Join(backendRoot, "internal", "controlplane", "service", "zz_nre_agent_contract_probe_test.go")
	overlayPath := filepath.Join(probeRoot, "overlay.json")
	overlay, err := json.Marshal(map[string]any{"Replace": map[string]string{virtualPath: backingPath}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(overlayPath, overlay, 0o600); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("go", "test", "-count=1", "-run", "^TestNREAgentCredentialContractProbe$", "-v", "-overlay", overlayPath, "./internal/controlplane/service")
	command.Dir = backendRoot
	command.Env = append(os.Environ(), "NRE_AGENT_CONTRACT_CSR="+csrPath)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("run backend production credential probe: %v\n%s", err, output)
	}
	const marker = "NRE_AGENT_CONTRACT_JSON="
	index := strings.Index(string(output), marker)
	if index < 0 {
		t.Fatalf("backend credential probe produced no contract payload:\n%s", output)
	}
	line := string(output[index+len(marker):])
	if end := strings.IndexByte(line, '\n'); end >= 0 {
		line = line[:end]
	}
	return []byte(strings.TrimSpace(line))
}

func TestSecurityHistorySurvivesOfflineSignerExpiryForPreparedCutover(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 2, 8, 0, 0, 0, time.UTC)
	authority1 := newTestAuthorityWithProfile(t, now, "authority-1", 1, elliptic.P256(), func(template *x509.Certificate) {
		template.NotAfter = now.Add(time.Hour)
	})
	authority2 := newTestAuthority(t, now, "authority-2", 2)
	prepared2 := authority2.root
	prepared2.Status = "prepared"
	initial := signedSnapshot(t, authority1, []model.PKITrustRoot{authority1.root, prepared2}, "domain-1", 1, 0, true, nil, nil, now)
	dataRoot := t.TempDir()
	store, err := NewStore(dataRoot, WithClock(func() time.Time { return now }))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ApplySecuritySnapshot(initial); err != nil {
		t.Fatal(err)
	}
	offlineNow := now.Add(2 * time.Hour)
	store, err = NewStore(dataRoot, WithClock(func() time.Time { return offlineNow }))
	if err != nil {
		t.Fatal(err)
	}
	if loaded, err := store.LoadSecuritySnapshot(); err != nil || loaded.Hash == "" {
		t.Fatalf("load last-known-good after signer expiry = %+v, error = %v", loaded, err)
	}
	retiring1 := authority1.root
	retiring1.Status = "retiring"
	cutover := signedSnapshot(t, authority2, []model.PKITrustRoot{retiring1, authority2.root}, "domain-1", 1, 1, false, nil, nil, offlineNow)
	advanced, err := store.ApplySecuritySnapshot(cutover)
	if err != nil || advanced.Snapshot.SignerGeneration != 2 {
		t.Fatalf("prepared cutover after offline expiry = %+v, error = %v", advanced, err)
	}
}

func TestSecuritySnapshotSignerValidityIsBoundToIssuedAt(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 2, 8, 0, 0, 0, time.UTC)
	authority := newTestAuthorityWithProfile(t, now, "authority-1", 1, elliptic.P256(), func(template *x509.Certificate) {
		template.NotBefore = now.Add(time.Hour)
		template.NotAfter = now.Add(3 * time.Hour)
	})
	snapshot := authority.snapshot(t, "domain-1", 1, 0, true, nil, nil, now)
	store, err := NewStore(t.TempDir(), WithClock(func() time.Time { return now.Add(2 * time.Hour) }))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ApplySecuritySnapshot(snapshot); !errors.Is(err, ErrSecurityInvalid) {
		t.Fatalf("ApplySecuritySnapshot() error = %v, want signer invalid at signed issued_at", err)
	}
}

func TestRegistrationTrustResetPreflightsRecoverableHistoryBeforeActivation(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 2, 8, 0, 0, 0, time.UTC)
	dataRoot := t.TempDir()
	store, err := NewStore(dataRoot, WithClock(func() time.Time { return now.Add(2 * time.Minute) }))
	if err != nil {
		t.Fatal(err)
	}
	authority1 := newTestAuthority(t, now, "authority-1", 1)
	authority2 := newTestAuthority(t, now, "authority-2", 2)
	expectation := testAgentExpectation(now)
	initialPending := prepareKnownAgent(t, store, expectation)
	initialCredential := authority1.issueCredential(t, initialPending, expectation, "identity-1", "certificate-1", now)
	initial := authority1.snapshot(t, "domain-1", 1, 0, true, nil, nil, now)
	if _, err := store.ActivateCredential(context.Background(), ActivateRequest{
		StorageIdentity: "agent", RequestID: initialPending.Request.RequestID, Credential: initialCredential,
		Security: initial, Expectation: expectation,
	}); err != nil {
		t.Fatal(err)
	}
	registrationPending, err := store.PrepareEnrollment(context.Background(), EnrollmentSpec{StorageIdentity: "agent"})
	if err != nil {
		t.Fatal(err)
	}
	emergencyCredential := authority2.issueCredential(t, registrationPending, expectation, "identity-2", "certificate-2", now.Add(time.Minute))
	emergencyCredential.PublicKeyFingerprint = strings.Repeat("0", 64)
	emergency := authority2.snapshot(t, "domain-1", 1, 1, true, []string{"identity-1"}, nil, now.Add(time.Minute))
	if err := os.Remove(filepath.Join(store.Root(), securityDirName, activePointerName)); err != nil {
		t.Fatal(err)
	}
	_, err = store.ActivateRegistrationCredential(context.Background(), ActivateRequest{
		StorageIdentity: "agent", RequestID: registrationPending.Request.RequestID,
		Credential: emergencyCredential, Security: emergency, Expectation: expectation,
	})
	if !errors.Is(err, ErrCredentialInvalid) {
		t.Fatalf("ActivateRegistrationCredential() error = %v, want credential preflight failure", err)
	}
	recovered, err := store.ApplySecuritySnapshot(initial)
	if err != nil || recovered.Snapshot.SignerGeneration != 1 {
		t.Fatalf("recover prior security after rejected reset = %+v, error = %v", recovered, err)
	}
}

func TestRegistrationTrustResetRejectsRelabeledHistoricalSigner(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 2, 8, 0, 0, 0, time.UTC)
	store, err := NewStore(t.TempDir(), WithClock(func() time.Time { return now.Add(2 * time.Minute) }))
	if err != nil {
		t.Fatal(err)
	}
	authority := newTestAuthority(t, now, "authority-1", 1)
	expectation := testAgentExpectation(now)
	initialPending := prepareKnownAgent(t, store, expectation)
	initialCredential := authority.issueCredential(t, initialPending, expectation, "identity-1", "certificate-1", now)
	initial := authority.snapshot(t, "domain-1", 1, 0, true, nil, nil, now)
	if _, err := store.ActivateCredential(context.Background(), ActivateRequest{
		StorageIdentity: "agent", RequestID: initialPending.Request.RequestID, Credential: initialCredential,
		Security: initial, Expectation: expectation,
	}); err != nil {
		t.Fatal(err)
	}
	registrationPending, err := store.PrepareEnrollment(context.Background(), EnrollmentSpec{StorageIdentity: "agent"})
	if err != nil {
		t.Fatal(err)
	}
	relabeled := authority
	relabeled.root.AuthorityID = "authority-relabelled"
	relabeled.root.Generation = 2
	credential := relabeled.issueCredential(t, registrationPending, expectation, "identity-2", "certificate-2", now.Add(time.Minute))
	reset := relabeled.snapshot(t, "domain-1", 1, 1, true, []string{"identity-1"}, nil, now.Add(time.Minute))
	_, err = store.ActivateRegistrationCredential(context.Background(), ActivateRequest{
		StorageIdentity: "agent", RequestID: registrationPending.Request.RequestID,
		Credential: credential, Security: reset, Expectation: expectation,
	})
	if !errors.Is(err, ErrSecurityInvalid) {
		t.Fatalf("ActivateRegistrationCredential(relabeled signer) error = %v, want ErrSecurityInvalid", err)
	}
	if current, err := store.LoadSecuritySnapshot(); err != nil || current.Snapshot.SignerGeneration != 1 {
		t.Fatalf("relabeled reset advanced security = %+v, error = %v", current, err)
	}
}

func TestRegistrationActivationConsumesConstrainedEmergencyTrustReset(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 2, 8, 0, 0, 0, time.UTC)
	dataRoot := t.TempDir()
	store, err := NewStore(dataRoot, WithClock(func() time.Time { return now.Add(2 * time.Minute) }))
	if err != nil {
		t.Fatal(err)
	}
	authority1 := newTestAuthority(t, now, "authority-1", 1)
	authority2 := newTestAuthority(t, now, "authority-2", 2)
	expectation := testAgentExpectation(now)
	initialPending := prepareKnownAgent(t, store, expectation)
	initialCredential := authority1.issueCredential(t, initialPending, expectation, "identity-1", "certificate-1", now)
	initial := authority1.snapshot(t, "domain-1", 1, 0, true, nil, nil, now)
	if _, err := store.ActivateCredential(context.Background(), ActivateRequest{
		StorageIdentity: "agent", RequestID: initialPending.Request.RequestID, Credential: initialCredential,
		Security: initial, Expectation: expectation,
	}); err != nil {
		t.Fatal(err)
	}

	registrationPending, err := store.PrepareEnrollment(context.Background(), EnrollmentSpec{StorageIdentity: "agent"})
	if err != nil {
		t.Fatal(err)
	}
	emergencyCredential := authority2.issueCredential(t, registrationPending, expectation, "identity-2", "certificate-2", now.Add(time.Minute))
	emergency := authority2.snapshot(t, "domain-1", 1, 1, true, []string{"identity-1"}, nil, now.Add(time.Minute))
	request := ActivateRequest{
		StorageIdentity: "agent", RequestID: registrationPending.Request.RequestID,
		Credential: emergencyCredential, Security: emergency, Expectation: expectation,
	}
	if _, err := store.ActivateCredential(context.Background(), request); !errors.Is(err, ErrSecurityInvalid) {
		t.Fatalf("ordinary activation accepted self-signed emergency reset: %v", err)
	}
	badRequest := request
	badRequest.Credential.PublicKeyFingerprint = strings.Repeat("0", 64)
	if _, err := store.ActivateRegistrationCredential(context.Background(), badRequest); !errors.Is(err, ErrCredentialInvalid) {
		t.Fatalf("invalid registration credential reset error = %v, want ErrCredentialInvalid", err)
	}
	if unchanged, err := store.LoadSecuritySnapshot(); err != nil || unchanged.Snapshot.SignerGeneration != 1 {
		t.Fatalf("invalid registration credential advanced emergency trust: %+v, error = %v", unchanged, err)
	}
	active, err := store.ActivateRegistrationCredential(context.Background(), request)
	if err != nil || active.Manifest.Credential.CertificateID != "certificate-2" {
		t.Fatalf("registration emergency activation = %+v, error = %v", active, err)
	}
	security, err := store.LoadSecuritySnapshot()
	if err != nil || security.Snapshot.SignerGeneration != 2 || security.Transition != securityTransitionRegistrationReset {
		t.Fatalf("emergency security state = %+v, error = %v", security, err)
	}
	acknowledgement, err := store.SecurityAcknowledgement("agent")
	if err != nil || acknowledgement.SecurityRevision != 1 || acknowledgement.CertificateID != "certificate-2" {
		t.Fatalf("emergency acknowledgement = %+v, error = %v", acknowledgement, err)
	}

	if err := os.Remove(filepath.Join(store.Root(), securityDirName, activePointerName)); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewStore(dataRoot, WithClock(func() time.Time { return now.Add(3 * time.Minute) }))
	if err != nil {
		t.Fatal(err)
	}
	recovered, err := reopened.ApplySecuritySnapshot(emergency)
	if err != nil || recovered.Hash != security.Hash {
		t.Fatalf("recover emergency transition history = %+v, error = %v", recovered, err)
	}
}

func TestLostEnrollmentResponseCanActivateFromRetiringIssuerOnlyWhenPredatingCutover(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 2, 8, 0, 0, 0, time.UTC)
	current := now
	store, err := NewStore(t.TempDir(), WithClock(func() time.Time { return current }))
	if err != nil {
		t.Fatal(err)
	}
	authority1 := newTestAuthority(t, now, "authority-1", 1)
	authority2 := newTestAuthority(t, now, "authority-2", 2)
	expectation := testAgentExpectation(now)
	pending := prepareKnownAgent(t, store, expectation)
	lostResponse := authority1.issueCredential(t, pending, expectation, "identity-1", "certificate-before-cutover", now)
	initial := authority1.snapshot(t, "domain-1", 1, 0, true, nil, nil, now)
	if _, err := store.ApplySecuritySnapshot(initial); err != nil {
		t.Fatal(err)
	}
	prepared2 := authority2.root
	prepared2.Status = "prepared"
	prepared := signedSnapshot(t, authority1, []model.PKITrustRoot{authority1.root, prepared2}, "domain-1", 1, 1, false, nil, nil, now.Add(time.Minute))
	current = now.Add(time.Minute)
	if _, err := store.ApplySecuritySnapshot(prepared); err != nil {
		t.Fatal(err)
	}
	retiring1 := authority1.root
	retiring1.Status = "retiring"
	cutover := signedSnapshot(t, authority2, []model.PKITrustRoot{retiring1, authority2.root}, "domain-1", 1, 2, false, nil, nil, now.Add(2*time.Minute))
	current = now.Add(2 * time.Minute)
	if _, err := store.ApplySecuritySnapshot(cutover); err != nil {
		t.Fatal(err)
	}
	if active, err := store.ActivateCredential(context.Background(), ActivateRequest{
		StorageIdentity: "agent", RequestID: pending.Request.RequestID, Credential: lostResponse,
		Security: cutover, Expectation: expectation,
	}); err != nil || active.Manifest.Credential.CertificateID != "certificate-before-cutover" {
		t.Fatalf("lost pre-cutover response activation = %+v, error = %v", active, err)
	}

	current = now.Add(5 * time.Minute)
	latePending := prepareKnownAgent(t, store, expectation)
	lateResponse := authority1.issueCredentialWithMutator(t, latePending, expectation, "identity-late", "certificate-after-cutover", current, func(certificate *x509.Certificate) {
		certificate.NotBefore = now
	})
	later := signedSnapshot(t, authority2, []model.PKITrustRoot{retiring1, authority2.root}, "domain-1", 1, 3, false, nil, nil, now.Add(10*time.Minute))
	current = now.Add(10 * time.Minute)
	if _, err := store.ApplySecuritySnapshot(later); err != nil {
		t.Fatal(err)
	}
	_, err = store.ActivateCredential(context.Background(), ActivateRequest{
		StorageIdentity: "agent", RequestID: latePending.Request.RequestID, Credential: lateResponse,
		Security: later, Expectation: expectation,
	})
	assertCredentialInvalidReason(t, err, CredentialInvalidSignerLifecycle)
}

func TestCredentialGenerationRecoversAcrossSecurityRevisionAdvance(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 2, 8, 0, 0, 0, time.UTC)
	failed := false
	dataRoot := t.TempDir()
	store, err := NewStore(dataRoot, WithClock(func() time.Time { return now }), withPersistenceCheckpoint(func(point string) error {
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
	store, err = NewStore(dataRoot, WithClock(func() time.Time { return now.Add(time.Minute) }))
	if err != nil {
		t.Fatalf("reopen after generation publication: %v", err)
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
	t.Parallel()
	now := time.Date(2026, 8, 2, 8, 0, 0, 0, time.UTC)
	for _, point := range []string{"credential.after_pointer_publish", "credential.after_ack_publish", "credential.after_pending_remove"} {
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
			authority := newTestAuthority(t, now, "authority-1", 1)
			expectation := testAgentExpectation(now)
			pending := prepareKnownAgent(t, store, expectation)
			credential := authority.issueCredential(t, pending, expectation, "identity-1", "certificate-1", now)
			request := ActivateRequest{StorageIdentity: "agent", RequestID: pending.Request.RequestID, Credential: credential, Security: authority.snapshot(t, "domain-1", 1, 0, true, nil, nil, now), Expectation: expectation}
			metadata, err := store.ActivateCredential(context.Background(), request)
			if !errors.Is(err, ErrActivationCommitted) || metadata.Manifest.Credential.CertificateID != "certificate-1" {
				t.Fatalf("ActivateCredential() = %+v, error = %v", metadata, err)
			}
			store, err = NewStore(dataRoot, WithClock(func() time.Time { return now.Add(time.Minute) }))
			if err != nil {
				t.Fatalf("reopen committed activation: %v", err)
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

func TestActivationCommittedErrorPreservesSentinelAndCauseChain(t *testing.T) {
	t.Parallel()
	cause := &os.PathError{Op: "fsync", Path: "active.json", Err: errInjectedPersistenceLoss}
	err := &ActivationCommittedError{Stage: "active pointer directory sync", Cause: cause}
	if !errors.Is(err, ErrActivationCommitted) || !errors.Is(err, errInjectedPersistenceLoss) {
		t.Fatalf("committed error chain = %v", err)
	}
	var pathError *os.PathError
	if !errors.As(err, &pathError) || pathError != cause {
		t.Fatalf("errors.As(PathError) = %#v, want original cause", pathError)
	}
}

func TestSecurityAcknowledgementIsDerivedAndReadOnlyWhenUnchanged(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
	now := time.Date(2026, 8, 2, 8, 0, 0, 0, time.UTC)
	store := newTestStore(t, now)
	authority := newTestAuthority(t, now, "authority-1", 1)
	expectation := CredentialExpectation{
		DomainID: "domain-1", AgentID: "agent-1", Kind: model.PKIIdentityKindListener,
		ListenerID: "listener-1", Purpose: model.PKICertificatePurposeServer,
		DNSNames: []string{"relay.example"}, IPAddresses: []string{"127.0.0.1"},
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
	t.Parallel()
	now := time.Date(2026, 8, 2, 8, 0, 0, 0, time.UTC)
	selected := make(chan struct{})
	release := make(chan struct{})
	revocationAttempted := make(chan struct{})
	events := make(chan string, 2)
	var once sync.Once
	store, err := NewStore(t.TempDir(), WithClock(func() time.Time { return now }), withPersistenceCheckpoint(func(point string) error {
		switch point {
		case "credential.security_selected":
			once.Do(func() { close(selected) })
			<-release
		case "credential.after_pending_remove":
			events <- "credential_complete"
		case "security.before_lock":
			close(revocationAttempted)
		case "security.lock_acquired":
			events <- "security_lock_acquired"
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
	<-revocationAttempted
	close(release)
	if err := <-activationDone; err != nil {
		t.Fatalf("ActivateCredential() error = %v", err)
	}
	if err := <-revocationDone; err != nil {
		t.Fatalf("ApplySecuritySnapshot(revocation) error = %v", err)
	}
	if first, second := <-events, <-events; first != "credential_complete" || second != "security_lock_acquired" {
		t.Fatalf("critical-section event order = [%s %s], want credential completion before revocation lock", first, second)
	}
	if _, err := store.LoadActiveCredential("agent"); !errors.Is(err, ErrCredentialInvalid) {
		t.Fatalf("revoked active credential load error = %v, want ErrCredentialInvalid", err)
	}
	if _, err := store.SecurityAcknowledgement("agent"); !errors.Is(err, ErrCredentialInvalid) {
		t.Fatalf("revoked credential acknowledgement error = %v, want ErrCredentialInvalid", err)
	}
}

func testCredentialValidationMatrixKeepsPreviousGeneration(t *testing.T) {
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
			credential.AuthorityID = "authority-1"
			credential.CAGeneration = 1
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
			replayed, err := store.LoadPending("agent")
			if err != nil || replayed.Request.RequestID != pending.Request.RequestID || replayed.Request.CSRPEM != pending.Request.CSRPEM || replayed.RequestFingerprint != pending.RequestFingerprint {
				t.Fatalf("invalid candidate damaged pending enrollment: %+v, error = %v", replayed, err)
			}
			corrected := authority.issueCredential(t, replayed, expectation, "identity-corrected", "certificate-corrected", now)
			if _, err := store.ActivateCredential(context.Background(), ActivateRequest{
				StorageIdentity: "agent", RequestID: replayed.Request.RequestID,
				Credential: corrected, Security: snapshot, Expectation: expectation,
			}); err != nil {
				t.Fatalf("corrected candidate replay error = %v", err)
			}
		})
	}
}

func TestActiveCredentialInternalValueCannotSerializePrivateKey(t *testing.T) {
	t.Parallel()
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
	if string(encoded) != "{}" {
		t.Fatalf("serialized active credential = %s, want exactly {}", encoded)
	}
	privateKey, ok := active.tlsCertificate.PrivateKey.(*ecdsa.PrivateKey)
	if !ok {
		t.Fatalf("active private key type = %T", active.tlsCertificate.PrivateKey)
	}
	privateDER, err := x509.MarshalECPrivateKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	for _, secretEncoding := range []string{
		string(privateDER),
		base64.StdEncoding.EncodeToString(privateDER),
		hex.EncodeToString(privateKey.D.Bytes()),
		base64.StdEncoding.EncodeToString(privateKey.D.Bytes()),
	} {
		if secretEncoding != "" && strings.Contains(string(encoded), secretEncoding) {
			t.Fatalf("serialized active credential contains a private-key encoding: %s", encoded)
		}
	}
}

func testAgentExpectation(_ time.Time) CredentialExpectation {
	return CredentialExpectation{DomainID: "domain-1", AgentID: "agent-1", Kind: model.PKIIdentityKindAgent, Purpose: model.PKICertificatePurposeClient}
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
	descriptors := make([]backendSecurityTrustDescriptorFixture, len(roots))
	generations := make([]int64, len(roots))
	for index, root := range roots {
		descriptors[index] = backendSecurityTrustDescriptorFixture{AuthorityID: root.AuthorityID, Generation: root.Generation, Status: root.Status, FingerprintSHA256: root.FingerprintSHA256, NotBefore: root.NotBefore.UTC(), NotAfter: root.NotAfter.UTC()}
		generations[index] = root.Generation
	}
	payload, err := json.Marshal(backendSecurityPayloadFixture{
		PKIDomainID: domain, Version: backendSecuritySnapshotVersionFixture{Version: backendSecurityVersionFixture{PKIEpoch: epoch, SecurityRevision: revision}, Full: full}, IssuedAt: issuedAt.UTC(),
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

func equivalentECDSASignature(t *testing.T, key *ecdsa.PrivateKey, signature []byte) []byte {
	t.Helper()
	parsed := struct {
		R *big.Int
		S *big.Int
	}{}
	rest, err := asn1.Unmarshal(signature, &parsed)
	if err != nil || len(rest) != 0 || parsed.R == nil || parsed.S == nil {
		t.Fatalf("parse ECDSA signature: %v", err)
	}
	parsed.S = new(big.Int).Sub(key.Curve.Params().N, parsed.S)
	alternate, err := asn1.Marshal(parsed)
	if err != nil {
		t.Fatalf("marshal equivalent ECDSA signature: %v", err)
	}
	return alternate
}
