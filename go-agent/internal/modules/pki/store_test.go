package pki

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

func TestPrepareEnrollmentPersistsReplaySafeKeyAndCSR(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 2, 8, 0, 0, 0, time.UTC)
	store := newTestStore(t, now)
	spec := EnrollmentSpec{StorageIdentity: "agent", Kind: model.PKIIdentityKindAgent, Purpose: model.PKICertificatePurposeClient}

	first, err := store.PrepareEnrollment(context.Background(), spec)
	if err != nil {
		t.Fatalf("PrepareEnrollment() error = %v", err)
	}
	second, err := store.PrepareEnrollment(context.Background(), spec)
	if err != nil {
		t.Fatalf("replayed PrepareEnrollment() error = %v", err)
	}
	if first.Request.RequestID != second.Request.RequestID || first.Request.CSRPEM != second.Request.CSRPEM || first.RequestFingerprint != second.RequestFingerprint {
		t.Fatal("pending enrollment did not replay the identical request")
	}
	request, err := parseCSRPEM([]byte(first.Request.CSRPEM))
	if err != nil {
		t.Fatalf("parse generated CSR: %v", err)
	}
	if request.Subject.String() != "" || len(request.Extensions) != 0 || len(request.URIs) != 0 || len(request.DNSNames) != 0 || len(request.IPAddresses) != 0 {
		t.Fatalf("anonymous CSR is not server-bound and empty: %+v", request)
	}
	for _, name := range []string{pendingKeyName, pendingCSRName, pendingJournalName} {
		path := filepath.Join(store.Root(), identitiesDirName, "agent", pendingDirName, name)
		info, statErr := os.Stat(path)
		if statErr != nil {
			t.Fatalf("stat %s: %v", name, statErr)
		}
		if got := info.Mode().Perm(); runtime.GOOS != "windows" && got&0o077 != 0 {
			t.Fatalf("%s permissions = %o, want no group/other access", name, got)
		}
		if err := verifyPrivatePath(path, false); err != nil {
			t.Fatalf("%s platform restriction: %v", name, err)
		}
	}
	pendingRoot := filepath.Join(store.Root(), identitiesDirName, "agent", pendingDirName)
	if err := verifyPrivatePath(pendingRoot, true); err != nil {
		t.Fatalf("pending directory platform restriction: %v", err)
	}

	_, err = store.PrepareEnrollment(context.Background(), EnrollmentSpec{
		StorageIdentity: "agent", DomainID: "other-domain", AgentID: "agent-1",
		Kind: model.PKIIdentityKindAgent, Purpose: model.PKICertificatePurposeClient,
	})
	if !errors.Is(err, ErrPendingConflict) {
		t.Fatalf("conflicting PrepareEnrollment() error = %v, want ErrPendingConflict", err)
	}
}

func TestSecuritySnapshotRejectsDowngradeAndRequiresEpochZeroFullRecovery(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 2, 8, 0, 0, 0, time.UTC)
	store := newTestStore(t, now)
	authority := newTestAuthority(t, now, "authority-1", 1)

	initial := authority.snapshot(t, "domain-1", 7, 3, true, nil, nil, now)
	state, err := store.ApplySecuritySnapshot(initial)
	if err != nil {
		t.Fatalf("ApplySecuritySnapshot() error = %v", err)
	}
	if state.Snapshot.PKIEpoch != 7 || state.Snapshot.SecurityRevision != 3 {
		t.Fatalf("active security version = (%d,%d)", state.Snapshot.PKIEpoch, state.Snapshot.SecurityRevision)
	}
	if _, err := store.ApplySecuritySnapshot(initial); err != nil {
		t.Fatalf("identical snapshot replay error = %v", err)
	}
	delta := authority.snapshot(t, "domain-1", 7, 4, false, nil, nil, now.Add(time.Second))
	if _, err := store.ApplySecuritySnapshot(delta); err != nil {
		t.Fatalf("same-epoch delta activation error = %v", err)
	}
	deltaReopened, err := NewStore(store.dataRoot, WithClock(func() time.Time { return now.Add(2 * time.Second) }))
	if err != nil {
		t.Fatalf("reopen delta store: %v", err)
	}
	if loadedDelta, err := deltaReopened.LoadSecuritySnapshot(); err != nil || loadedDelta.Snapshot.Full {
		t.Fatalf("reloaded delta = %+v, error = %v", loadedDelta.Snapshot, err)
	}
	downgrade := authority.snapshot(t, "domain-1", 7, 2, true, nil, nil, now.Add(time.Second))
	if _, err := store.ApplySecuritySnapshot(downgrade); !errors.Is(err, ErrSecurityDowngrade) {
		t.Fatalf("downgrade error = %v, want ErrSecurityDowngrade", err)
	}
	invalidEpoch := authority.snapshot(t, "domain-1", 8, 1, true, nil, nil, now.Add(2*time.Second))
	if _, err := store.ApplySecuritySnapshot(invalidEpoch); !errors.Is(err, ErrSecurityDowngrade) {
		t.Fatalf("higher nonzero epoch revision error = %v, want ErrSecurityDowngrade", err)
	}
	invalidEpochDelta := authority.snapshot(t, "domain-1", 8, 0, false, nil, nil, now.Add(2*time.Second))
	if _, err := store.ApplySecuritySnapshot(invalidEpochDelta); !errors.Is(err, ErrSecurityDowngrade) {
		t.Fatalf("higher epoch delta error = %v, want ErrSecurityDowngrade", err)
	}
	if unchanged, err := store.LoadSecuritySnapshot(); err != nil || unchanged.Snapshot.PKIEpoch != 7 || unchanged.Snapshot.SecurityRevision != 4 {
		t.Fatalf("higher epoch delta changed active state: %+v, error = %v", unchanged, err)
	}
	revokedSerial := "abcdefabcdefabcdefabcdefabcdefab"
	nextEpoch := authority.snapshot(t, "domain-1", 8, 0, true, []string{"identity-revoked"}, []string{revokedSerial}, now.Add(3*time.Second))
	state, err = store.ApplySecuritySnapshot(nextEpoch)
	if err != nil {
		t.Fatalf("higher epoch revision zero error = %v", err)
	}
	if !slices.Equal(state.Snapshot.RevokedSerials, []string{revokedSerial}) {
		t.Fatalf("normalized revoked serials = %v", state.Snapshot.RevokedSerials)
	}

	reopened, err := NewStore(store.dataRoot, WithClock(func() time.Time { return now.Add(4 * time.Second) }))
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	loaded, err := reopened.LoadSecuritySnapshot()
	if err != nil {
		t.Fatalf("LoadSecuritySnapshot() error = %v", err)
	}
	if loaded.Hash != state.Hash || loaded.Snapshot.PKIEpoch != 8 {
		t.Fatalf("reopened security state = %+v, want hash %s epoch 8", loaded, state.Hash)
	}
}

func TestStoreStartupRecoversHighestDurableSecurityState(t *testing.T) {
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
	initialSecurity, err := store.LoadSecuritySnapshot()
	if err != nil {
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

	securityRoot := filepath.Join(store.Root(), securityDirName)
	rollback := securityPointer{
		Version: 1, File: securityStateFileName(initialSecurity), Hash: initialSecurity.Hash, ActivatedAt: initialSecurity.ActivatedAt,
	}
	if _, err := writeAtomicPrivateJSON(securityRoot, activePointerName, rollback, store.random); err != nil {
		t.Fatalf("publish downgraded active pointer: %v", err)
	}

	reopened, err := NewStore(store.dataRoot, WithClock(func() time.Time { return now.Add(3 * time.Minute) }))
	if err != nil {
		t.Fatalf("reopen downgraded store: %v", err)
	}
	recovered, err := reopened.LoadSecuritySnapshot()
	if err != nil || recovered.Hash != higherState.Hash || recovered.Snapshot.SecurityRevision != 1 {
		t.Fatalf("recovered security state = %+v, error = %v", recovered, err)
	}
	if acknowledgement, err := reopened.SecurityAcknowledgement("agent"); err != nil || acknowledgement.SecurityRevision != 1 {
		t.Fatalf("recovered acknowledgement = %+v, error = %v", acknowledgement, err)
	}
}

func TestActivateCredentialPublishesCompleteGenerationAndKeepsOldOnFailure(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 2, 8, 0, 0, 0, time.UTC)
	store := newTestStore(t, now)
	authority := newTestAuthority(t, now, "authority-1", 1)
	snapshot := authority.snapshot(t, "domain-1", 1, 0, true, nil, nil, now)
	expectation := CredentialExpectation{
		DomainID: "domain-1", AgentID: "agent-1", Kind: model.PKIIdentityKindAgent,
		Purpose: model.PKICertificatePurposeClient,
	}

	pending := prepareKnownAgent(t, store, expectation)
	credential := authority.issueCredential(t, pending, expectation, "identity-1", "certificate-1", now)
	active, err := store.ActivateCredential(context.Background(), ActivateRequest{
		StorageIdentity: "agent", RequestID: pending.Request.RequestID,
		Credential: credential, Security: snapshot, Expectation: expectation,
	})
	if err != nil {
		t.Fatalf("ActivateCredential() error = %v", err)
	}
	if active.Manifest.Credential.CertificateID != "certificate-1" {
		t.Fatalf("active credential = %+v", active.Manifest)
	}
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS13}
	if _, err := store.InstallTLSCertificate("agent", tlsConfig); err != nil || tlsConfig.GetClientCertificate == nil {
		t.Fatalf("InstallTLSCertificate() error = %v", err)
	}
	keyPair, err := tlsConfig.GetClientCertificate(nil)
	if err != nil || keyPair == nil || keyPair.PrivateKey == nil {
		t.Fatalf("installed client credential is unavailable: %v", err)
	}
	if _, err := os.Stat(filepath.Join(store.Root(), identitiesDirName, "agent", pendingDirName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("completed pending directory still exists: %v", err)
	}
	ackData, err := os.ReadFile(filepath.Join(store.Root(), securityDirName, "ack.json"))
	if err != nil || !strings.Contains(string(ackData), `"certificate_id":"certificate-1"`) {
		t.Fatalf("durable security acknowledgement = %q, error = %v", ackData, err)
	}
	loaded, err := store.LoadActiveCredential("agent")
	if err != nil {
		t.Fatalf("LoadActiveCredential() error = %v", err)
	}
	if loaded.Manifest.Generation != active.Manifest.Generation {
		t.Fatalf("loaded active generation = %+v", loaded.Manifest)
	}
	ack, err := store.SecurityAcknowledgement("agent")
	if err != nil || ack.CertificateID != "certificate-1" || !slices.Equal(ack.TrustGenerations, []int64{1}) {
		t.Fatalf("SecurityAcknowledgement() = %+v, error = %v", ack, err)
	}

	badPending := prepareKnownAgent(t, store, expectation)
	badCredential := authority.issueCredential(t, badPending, expectation, "identity-1", "certificate-2", now)
	badCredential.PublicKeyFingerprint = strings.Repeat("0", 64)
	if _, err := store.ActivateCredential(context.Background(), ActivateRequest{
		StorageIdentity: "agent", RequestID: badPending.Request.RequestID,
		Credential: badCredential, Security: snapshot, Expectation: expectation,
	}); !errors.Is(err, ErrCredentialInvalid) {
		t.Fatalf("invalid credential activation error = %v, want ErrCredentialInvalid", err)
	}
	stillActive, err := store.LoadActiveCredential("agent")
	if err != nil {
		t.Fatalf("LoadActiveCredential() after failed candidate error = %v", err)
	}
	if stillActive.Manifest.Generation != active.Manifest.Generation {
		t.Fatalf("failed candidate changed active generation from %s to %s", active.Manifest.Generation, stillActive.Manifest.Generation)
	}
	replayed, err := store.LoadPending("agent")
	if err != nil || replayed.Request.RequestID != badPending.Request.RequestID || replayed.Request.CSRPEM != badPending.Request.CSRPEM || replayed.RequestFingerprint != badPending.RequestFingerprint {
		t.Fatalf("failed candidate damaged replayable pending state: %+v, error = %v", replayed, err)
	}
	fixedCredential := authority.issueCredential(t, replayed, expectation, "identity-1", "certificate-2", now)
	fixed, err := store.ActivateCredential(context.Background(), ActivateRequest{
		StorageIdentity: "agent", RequestID: replayed.Request.RequestID,
		Credential: fixedCredential, Security: snapshot, Expectation: expectation,
	})
	if err != nil || fixed.Manifest.Credential.CertificateID != "certificate-2" {
		t.Fatalf("corrected credential replay = %+v, error = %v", fixed, err)
	}
}

func TestPrivateFilePermissionDriftFailsClosed(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 2, 8, 0, 0, 0, time.UTC)
	store := newTestStore(t, now)
	expectation := CredentialExpectation{DomainID: "domain-1", AgentID: "agent-1", Kind: model.PKIIdentityKindAgent, Purpose: model.PKICertificatePurposeClient}
	prepareKnownAgent(t, store, expectation)
	keyPath := filepath.Join(store.Root(), identitiesDirName, "agent", pendingDirName, pendingKeyName)
	if runtime.GOOS == "windows" {
		output, err := exec.Command("icacls", keyPath, "/grant", "*S-1-1-0:(R)").CombinedOutput()
		if err != nil {
			t.Fatalf("broaden Windows private-key DACL: %v: %s", err, output)
		}
	} else if err := os.Chmod(keyPath, 0o644); err != nil {
		t.Fatalf("broaden Unix private-key mode: %v", err)
	}
	if _, err := store.LoadPending("agent"); !errors.Is(err, ErrCredentialInvalid) {
		t.Fatalf("LoadPending() with exposed private key error = %v, want ErrCredentialInvalid", err)
	}
}

func testActivateStagedRegistrationConsumesSanitizedJoinResponse(t *testing.T) {
	now := time.Date(2026, 8, 2, 8, 0, 0, 0, time.UTC)
	store := newTestStore(t, now)
	authority := newTestAuthority(t, now, "authority-1", 1)
	pending, err := store.PrepareEnrollment(context.Background(), EnrollmentSpec{
		StorageIdentity: "agent", Kind: model.PKIIdentityKindAgent, Purpose: model.PKICertificatePurposeClient,
	})
	if err != nil {
		t.Fatalf("PrepareEnrollment() error = %v", err)
	}
	expectation := CredentialExpectation{
		DomainID: "domain-1", AgentID: "agent-1", Kind: model.PKIIdentityKindAgent,
		Purpose: model.PKICertificatePurposeClient,
	}
	staged := StagedRegistration{
		AgentID:          "agent-1",
		TunnelCredential: authority.issueCredential(t, pending, expectation, "identity-1", "certificate-staged", now),
		SecuritySnapshot: authority.snapshot(t, "domain-1", 0, 0, true, nil, nil, now),
	}
	rawResponse := map[string]any{
		"ok":             true,
		"register_token": "register-secret",
		"pki": map[string]any{
			"agent_id": "agent-1", "agent_token": "control-secret",
			"tunnel_credential": staged.TunnelCredential,
			"security_snapshot": staged.SecuritySnapshot,
		},
	}
	responsePath := stageRegistrationWithJoinScript(t, store.dataRoot, rawResponse)
	encoded, err := os.ReadFile(responsePath)
	if err != nil {
		t.Fatalf("read script-staged response: %v", err)
	}
	if strings.Contains(string(encoded), "control-secret") || strings.Contains(string(encoded), "register-secret") || !strings.Contains(string(encoded), `"certificate_id":"certificate-staged"`) {
		t.Fatalf("script-staged response is not the sanitized PKI projection: %s", encoded)
	}
	environment, err := os.ReadFile(filepath.Join(store.dataRoot, "agent.env"))
	if err != nil {
		t.Fatalf("read script-persisted agent environment: %v", err)
	}
	if !strings.Contains(string(environment), "control-secret") || strings.Contains(string(environment), "register-secret") {
		t.Fatalf("script-persisted agent environment does not contain only the durable control token: %s", environment)
	}
	// Production starts (or restarts) the native agent after the shell helper.
	// Reopening the Store must apply the fixed-root Windows DACL migration to
	// the raw Git-Bash artifacts without a test-only ACL pre-pass.
	store, err = NewStore(store.dataRoot, WithClock(func() time.Time { return now }))
	if err != nil {
		t.Fatalf("reopen Store over raw join artifacts: %v", err)
	}
	active, err := store.ActivateStagedRegistration(context.Background(), "agent")
	if err != nil {
		t.Fatalf("ActivateStagedRegistration() error = %v", err)
	}
	if active.Manifest.Credential.CertificateID != "certificate-staged" {
		t.Fatalf("active staged certificate = %s", active.Manifest.Credential.CertificateID)
	}
}

func TestCredentialValidationUsesStoreClockAndTypedReasons(t *testing.T) {
	t.Parallel()
	issuedAt := time.Date(2026, 8, 2, 8, 0, 0, 0, time.UTC)

	t.Run("expired", func(t *testing.T) {
		store := newTestStore(t, issuedAt.Add(25*time.Hour))
		authority := newTestAuthority(t, issuedAt, "authority-1", 1)
		expectation := testAgentExpectation(issuedAt)
		pending := prepareKnownAgent(t, store, expectation)
		credential := authority.issueCredential(t, pending, expectation, "identity-1", "certificate-expired", issuedAt)
		_, err := store.ActivateCredential(context.Background(), ActivateRequest{
			StorageIdentity: "agent", RequestID: pending.Request.RequestID, Credential: credential,
			Security: authority.snapshot(t, "domain-1", 1, 0, true, nil, nil, issuedAt), Expectation: expectation,
		})
		assertCredentialInvalidReason(t, err, CredentialInvalidExpired)
	})

	t.Run("not yet valid", func(t *testing.T) {
		store := newTestStore(t, issuedAt)
		authority := newTestAuthority(t, issuedAt, "authority-1", 1)
		expectation := testAgentExpectation(issuedAt)
		pending := prepareKnownAgent(t, store, expectation)
		credential := authority.issueCredentialWithMutator(t, pending, expectation, "identity-1", "certificate-future", issuedAt, func(certificate *x509.Certificate) {
			certificate.NotBefore = issuedAt.Add(time.Hour)
			certificate.NotAfter = issuedAt.Add(2 * time.Hour)
		})
		_, err := store.ActivateCredential(context.Background(), ActivateRequest{
			StorageIdentity: "agent", RequestID: pending.Request.RequestID, Credential: credential,
			Security: authority.snapshot(t, "domain-1", 1, 0, true, nil, nil, issuedAt), Expectation: expectation,
		})
		assertCredentialInvalidReason(t, err, CredentialInvalidNotYetValid)
	})

	for _, test := range []struct {
		name   string
		revoke func(model.PKITunnelCredential) ([]string, []string)
		reason CredentialInvalidReason
	}{
		{name: "identity revocation", reason: CredentialInvalidRevokedIdentity, revoke: func(credential model.PKITunnelCredential) ([]string, []string) {
			return []string{credential.IdentityID}, nil
		}},
		{name: "serial revocation", reason: CredentialInvalidRevokedSerial, revoke: func(credential model.PKITunnelCredential) ([]string, []string) {
			leaf, err := parseCertificatePEM(credential.CertificatePEM)
			if err != nil {
				t.Fatal(err)
			}
			return nil, []string{leaf.SerialNumber.Text(16)}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := newTestStore(t, issuedAt.Add(2*time.Minute))
			authority := newTestAuthority(t, issuedAt, "authority-1", 1)
			expectation := testAgentExpectation(issuedAt)
			pending := prepareKnownAgent(t, store, expectation)
			credential := authority.issueCredential(t, pending, expectation, "identity-1", "certificate-1", issuedAt)
			initial := authority.snapshot(t, "domain-1", 1, 0, true, nil, nil, issuedAt)
			if _, err := store.ActivateCredential(context.Background(), ActivateRequest{StorageIdentity: "agent", RequestID: pending.Request.RequestID, Credential: credential, Security: initial, Expectation: expectation}); err != nil {
				t.Fatal(err)
			}
			identities, serials := test.revoke(credential)
			if _, err := store.ApplySecuritySnapshot(authority.snapshot(t, "domain-1", 1, 1, false, identities, serials, issuedAt.Add(time.Minute))); err != nil {
				t.Fatal(err)
			}
			_, err := store.LoadActiveCredential("agent")
			assertCredentialInvalidReason(t, err, test.reason)
		})
	}
}

func TestRenewalStateIsPrivateAtomicAndUsesStoreClock(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 2, 8, 0, 0, 0, time.UTC)
	current := now
	dataRoot := t.TempDir()
	store, err := NewStore(dataRoot, WithClock(func() time.Time { return current }))
	if err != nil {
		t.Fatal(err)
	}
	input := RenewalState{
		Version: 99, CredentialIdentity: " identity-1 ", CredentialFingerprint: strings.Repeat("a", 64),
		DueAt: now.Add(8 * time.Hour), FailureCount: 2, NextAttemptAt: now.Add(time.Minute),
		ReenrollmentRequired: true, Reason: " revoked_identity ", UpdatedAt: now.Add(-24 * time.Hour),
		PendingRejectionRequestID: " " + strings.Repeat("b", 32) + " ", PendingRejectionCode: " owner_mismatch ",
	}
	saved, err := store.SaveRenewalState("agent", input)
	if err != nil {
		t.Fatalf("SaveRenewalState() error = %v", err)
	}
	if saved.Version != 1 || saved.CredentialIdentity != "identity-1" || saved.Reason != "revoked_identity" ||
		saved.PendingRejectionRequestID != strings.Repeat("b", 32) || saved.PendingRejectionCode != "owner_mismatch" || !saved.UpdatedAt.Equal(now) {
		t.Fatalf("normalized renewal state = %+v", saved)
	}
	invalidIntent := saved
	invalidIntent.PendingRejectionCode = ""
	if _, err := store.SaveRenewalState("agent", invalidIntent); !errors.Is(err, ErrCredentialInvalid) {
		t.Fatalf("partial rejection intent error = %v, want ErrCredentialInvalid", err)
	}
	path := filepath.Join(store.Root(), identitiesDirName, "agent", renewalStateName)
	if err := verifyPrivatePath(path, false); err != nil {
		t.Fatalf("renewal state permissions: %v", err)
	}
	current = now.Add(time.Hour)
	unchanged, err := store.SaveRenewalState("agent", saved)
	if err != nil || !unchanged.UpdatedAt.Equal(now) {
		t.Fatalf("unchanged SaveRenewalState() = %+v, error = %v", unchanged, err)
	}
	saved.FailureCount++
	changed, err := store.SaveRenewalState("agent", saved)
	if err != nil || !changed.UpdatedAt.Equal(current) {
		t.Fatalf("changed SaveRenewalState() = %+v, error = %v", changed, err)
	}
	reopened, err := NewStore(dataRoot, WithClock(func() time.Time { return current.Add(time.Hour) }))
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := reopened.LoadRenewalState("agent")
	if err != nil || !reflect.DeepEqual(loaded, changed) {
		t.Fatalf("LoadRenewalState() = %+v, error = %v, want %+v", loaded, err, changed)
	}
	if _, err := reopened.LoadRenewalState("missing"); !errors.Is(err, ErrRenewalStateNotFound) {
		t.Fatalf("missing LoadRenewalState() error = %v", err)
	}
}

func assertCredentialInvalidReason(t *testing.T, err error, reason CredentialInvalidReason) {
	t.Helper()
	if !errors.Is(err, ErrCredentialInvalid) {
		t.Fatalf("credential error = %v, want ErrCredentialInvalid", err)
	}
	var classified *CredentialInvalidError
	if !errors.As(err, &classified) || classified.Reason != reason {
		t.Fatalf("credential error classification = %#v, want %q (error %v)", classified, reason, err)
	}
}

func TestSecuritySnapshotRejectsForgedSignature(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 2, 8, 0, 0, 0, time.UTC)
	store := newTestStore(t, now)
	authority := newTestAuthority(t, now, "authority-1", 1)
	snapshot := authority.snapshot(t, "domain-1", 0, 0, true, nil, nil, now)
	snapshot.Signature[0] ^= 0xff
	if _, err := store.ApplySecuritySnapshot(snapshot); !errors.Is(err, ErrSecurityInvalid) {
		t.Fatalf("forged signature error = %v, want ErrSecurityInvalid", err)
	}
}

func TestNewStoreDurablyCreatesMissingDataRoot(t *testing.T) {
	t.Parallel()
	parent := t.TempDir()
	dataRoot := filepath.Join(parent, "new", "agent-data")
	injected := errors.New("injected process loss after data-root publication")
	if _, err := NewStore(dataRoot, withPersistenceCheckpoint(func(point string) error {
		if point == "store.after_data_root_publish" {
			return injected
		}
		return nil
	})); !errors.Is(err, injected) {
		t.Fatalf("NewStore() publication error = %v, want injected failure", err)
	}
	if info, err := os.Lstat(dataRoot); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("published data root = %+v, error = %v", info, err)
	}
	reopened, err := NewStore(dataRoot)
	if err != nil {
		t.Fatalf("reopen durably published data root: %v", err)
	}
	if reopened.Root() != filepath.Join(dataRoot, storeDirName) {
		t.Fatalf("reopened root = %q", reopened.Root())
	}
}

func newTestStore(t *testing.T, now time.Time) *Store {
	t.Helper()
	store, err := NewStore(t.TempDir(), WithClock(func() time.Time { return now }))
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	return store
}

func stageRegistrationWithJoinScript(t *testing.T, dataRoot string, response any) string {
	t.Helper()
	shell := findJoinTestShell(t)
	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	joinScript := filepath.Clean(filepath.Join(workingDirectory, "..", "..", "..", "..", "scripts", "join-agent.sh"))
	script, err := os.ReadFile(joinScript)
	if err != nil {
		t.Fatalf("read join-agent.sh: %v", err)
	}
	marker := []byte("\nCOMMAND=\"join\"\n")
	index := strings.Index(string(script), string(marker))
	if index < 0 {
		t.Fatal("join-agent.sh function boundary is missing")
	}
	temporary := t.TempDir()
	functionsPath := filepath.Join(temporary, "join-functions.sh")
	if err := os.WriteFile(functionsPath, script[:index+1], 0o600); err != nil {
		t.Fatal(err)
	}
	responseData, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	responsePath := filepath.Join(temporary, "register-response.json")
	if err := os.WriteFile(responsePath, responseData, 0o600); err != nil {
		t.Fatal(err)
	}
	command := `. "$1"
DATA_DIR="$2"
ENV_FILE="$DATA_DIR/agent.env"
REGISTER_RESPONSE="$(cat "$3")"
MASTER_URL="https://control.example.test"
AGENT_NAME="agent-1"
AGENT_ID=""
AGENT_TOKEN=""
AGENT_URL=""
AGENT_VERSION="1"
AGENT_TAGS=""
AGENT_CAPABILITIES=""
PKI_DOMAIN_ID=""
stage_pki_registration_response >/dev/null`
	output, err := exec.Command(shell, "-c", command, "join-stage-test", shellTestPath(functionsPath), shellTestPath(dataRoot), shellTestPath(responsePath)).CombinedOutput()
	if err != nil {
		t.Fatalf("stage_pki_registration_response: %v: %s", err, output)
	}
	stagedPath := filepath.Join(dataRoot, storeDirName, identitiesDirName, "agent", pendingDirName, "response.json")
	return stagedPath
}

func findJoinTestShell(t *testing.T) string {
	t.Helper()
	if shell, err := exec.LookPath("sh"); err == nil {
		return shell
	}
	if runtime.GOOS == "windows" {
		for _, candidate := range []string{
			`C:\Program Files\Git\bin\sh.exe`,
			`C:\Program Files\Git\usr\bin\sh.exe`,
			filepath.Join(os.Getenv("LOCALAPPDATA"), "Programs", "Git", "bin", "sh.exe"),
		} {
			if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
				return candidate
			}
		}
	}
	t.Skip("POSIX shell unavailable for join-agent projection contract")
	return ""
}

func shellTestPath(path string) string {
	return filepath.ToSlash(path)
}

func prepareKnownAgent(t *testing.T, store *Store, expectation CredentialExpectation) PendingEnrollment {
	t.Helper()
	pending, err := store.PrepareEnrollment(context.Background(), EnrollmentSpec{
		StorageIdentity: "agent", DomainID: expectation.DomainID, AgentID: expectation.AgentID,
		Kind: expectation.Kind, Purpose: expectation.Purpose,
	})
	if err != nil {
		t.Fatalf("PrepareEnrollment() error = %v", err)
	}
	return pending
}

type testAuthority struct {
	key         *ecdsa.PrivateKey
	certificate *x509.Certificate
	root        model.PKITrustRoot
}

// These fixture types mirror the backend canonical signer contract without
// reusing the agent verifier's private payload types. A schema drift on either
// side therefore invalidates signatures instead of silently updating tests.
type backendSecurityVersionFixture struct {
	PKIEpoch         int64 `json:"pki_epoch"`
	SecurityRevision int64 `json:"security_revision"`
}

type backendSecuritySnapshotVersionFixture struct {
	Version backendSecurityVersionFixture `json:"version"`
	Full    bool                          `json:"full"`
}

type backendSecurityTrustDescriptorFixture struct {
	AuthorityID       string    `json:"authority_id"`
	Generation        int64     `json:"generation"`
	Status            string    `json:"status"`
	FingerprintSHA256 string    `json:"fingerprint_sha256"`
	NotBefore         time.Time `json:"not_before"`
	NotAfter          time.Time `json:"not_after"`
}

type backendSecurityPayloadFixture struct {
	PKIDomainID        string                                  `json:"pki_domain_id"`
	Version            backendSecuritySnapshotVersionFixture   `json:"version"`
	IssuedAt           time.Time                               `json:"issued_at"`
	TrustGenerations   []int64                                 `json:"trust_generations"`
	TrustRoots         []backendSecurityTrustDescriptorFixture `json:"trust_roots"`
	RevokedIdentityIDs []string                                `json:"revoked_identity_ids"`
	RevokedSerials     []string                                `json:"revoked_serials"`
}

func newTestAuthority(t *testing.T, now time.Time, authorityID string, generation int64) testAuthority {
	return newTestAuthorityWithProfile(t, now, authorityID, generation, elliptic.P256(), nil)
}

func newTestAuthorityWithProfile(t *testing.T, now time.Time, authorityID string, generation int64, curve elliptic.Curve, mutate func(*x509.Certificate)) testAuthority {
	t.Helper()
	key, err := ecdsa.GenerateKey(curve, rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serialDigest := sha256.Sum256([]byte(authorityID + "\x00" + big.NewInt(generation).String()))
	serialNumber := new(big.Int).SetBytes(serialDigest[:16])
	serialNumber.SetBit(serialNumber, 127, 1)
	template := &x509.Certificate{
		SerialNumber: serialNumber, Subject: pkix.Name{CommonName: authorityID},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(365 * 24 * time.Hour),
		IsCA: true, BasicConstraintsValid: true,
		KeyUsage:           x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		SignatureAlgorithm: x509.ECDSAWithSHA256,
	}
	if mutate != nil {
		mutate(template)
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	fingerprint := sha256.Sum256(der)
	return testAuthority{
		key: key, certificate: certificate,
		root: model.PKITrustRoot{
			AuthorityID: authorityID, Generation: generation, Status: "active",
			CertificatePEM:    string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})),
			FingerprintSHA256: hex.EncodeToString(fingerprint[:]), NotBefore: certificate.NotBefore, NotAfter: certificate.NotAfter,
		},
	}
}

func (authority testAuthority) snapshot(t *testing.T, domain string, epoch, revision int64, full bool, revokedIdentities, revokedSerials []string, issuedAt time.Time) model.PKISecuritySnapshot {
	t.Helper()
	identities := slices.Clone(revokedIdentities)
	serials := slices.Clone(revokedSerials)
	slices.Sort(identities)
	for index := range serials {
		serials[index] = strings.ToLower(serials[index])
	}
	slices.Sort(serials)
	descriptor := backendSecurityTrustDescriptorFixture{
		AuthorityID: authority.root.AuthorityID, Generation: authority.root.Generation, Status: authority.root.Status,
		FingerprintSHA256: authority.root.FingerprintSHA256, NotBefore: authority.root.NotBefore.UTC(), NotAfter: authority.root.NotAfter.UTC(),
	}
	payload, err := json.Marshal(backendSecurityPayloadFixture{
		PKIDomainID: domain,
		Version:     backendSecuritySnapshotVersionFixture{Version: backendSecurityVersionFixture{PKIEpoch: epoch, SecurityRevision: revision}, Full: full},
		IssuedAt:    issuedAt.UTC(), TrustGenerations: []int64{authority.root.Generation}, TrustRoots: []backendSecurityTrustDescriptorFixture{descriptor},
		RevokedIdentityIDs: identities, RevokedSerials: serials,
	})
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(payload)
	signature, err := ecdsa.SignASN1(rand.Reader, authority.key, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return model.PKISecuritySnapshot{
		PKIDomainID: domain, PKIEpoch: epoch, SecurityRevision: revision, Full: full, IssuedAt: issuedAt,
		TrustRoots: []model.PKITrustRoot{authority.root}, RevokedIdentityIDs: identities, RevokedSerials: serials,
		SignerGeneration: authority.root.Generation, Signature: signature,
	}
}

func (authority testAuthority) issueCredential(t *testing.T, pending PendingEnrollment, expectation CredentialExpectation, identityID, certificateID string, now time.Time) model.PKITunnelCredential {
	return authority.issueCredentialWithMutator(t, pending, expectation, identityID, certificateID, now, nil)
}

func (authority testAuthority) issueCredentialWithMutator(t *testing.T, pending PendingEnrollment, expectation CredentialExpectation, identityID, certificateID string, now time.Time, mutate func(*x509.Certificate)) model.PKITunnelCredential {
	t.Helper()
	csrBlock, _ := pem.Decode([]byte(pending.Request.CSRPEM))
	csr, err := x509.ParseCertificateRequest(csrBlock.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	identityURI, err := url.Parse("spiffe://" + expectation.DomainID + "/agent/" + expectation.AgentID)
	if err != nil {
		t.Fatal(err)
	}
	if expectation.Kind == model.PKIIdentityKindListener {
		identityURI.Path += "/listener/" + expectation.ListenerID
	}
	usage := x509.ExtKeyUsageClientAuth
	if expectation.Purpose == model.PKICertificatePurposeServer {
		usage = x509.ExtKeyUsageServerAuth
	}
	serialDigest := sha256.Sum256([]byte(certificateID))
	serialNumber := new(big.Int).SetBytes(serialDigest[:16])
	serialNumber.SetBit(serialNumber, 127, 1)
	template := &x509.Certificate{
		SerialNumber: serialNumber, Subject: pkix.Name{CommonName: identityURI.String()},
		NotBefore: now.Add(-time.Minute), NotAfter: now.Add(24 * time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{usage}, BasicConstraintsValid: true,
		SignatureAlgorithm: x509.ECDSAWithSHA256,
		URIs:               []*url.URL{identityURI}, DNSNames: slices.Clone(expectation.DNSNames),
	}
	for _, value := range expectation.IPAddresses {
		template.IPAddresses = append(template.IPAddresses, net.ParseIP(value))
	}
	if mutate != nil {
		mutate(template)
	}
	der, err := x509.CreateCertificate(rand.Reader, template, authority.certificate, csr.PublicKey, authority.key)
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	fingerprint := sha256.Sum256(certificate.RawSubjectPublicKeyInfo)
	return model.PKITunnelCredential{
		IdentityID: identityID, CertificateID: certificateID, Purpose: expectation.Purpose,
		CertificatePEM:       string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})),
		PublicKeyFingerprint: hex.EncodeToString(fingerprint[:]), AuthorityID: authority.root.AuthorityID,
		CAGeneration: authority.root.Generation, NotBefore: certificate.NotBefore, NotAfter: certificate.NotAfter,
	}
}
