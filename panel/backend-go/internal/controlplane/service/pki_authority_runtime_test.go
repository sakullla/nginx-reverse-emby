package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	revisionpkg "github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/revision"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

func TestPKIAuthorityCoordinatorPersistsCutoverAndRestartSafeRetirement(t *testing.T) {
	root := t.TempDir()
	store := newPKIAuthorityRuntimeTestStore(t, root)
	vault, err := OpenPKIVault(PKIVaultConfig{DataRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(vault.Close)
	bootstrap := bootstrapPKIAuthorityRuntimeTest(t, store, vault)
	now := time.Now().UTC().Truncate(time.Second)
	clock := func() time.Time { return now }
	runtime := newPKIAuthorityRuntimeForTest(t, store, vault, bootstrap, clock, nil)
	operationID := queuePKIAuthorityRuntimeTestJob(t, store, bootstrap.lease, "ca-runtime-normal", "ca_rotate", now)

	if err := runtime.StartNormal(t.Context(), operationID, "scheduled test rotation"); err != nil {
		t.Fatalf("StartNormal() error = %v", err)
	}
	state, err := store.LoadPKICanonicalState(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	row, found := findPKILifecycleRow(state, operationID)
	if !found || row.State != storage.PKILifecycleJobStateRunning || row.Phase != PKICARotationPhaseOverlap {
		t.Fatalf("rotation after immediate phases = %+v", row)
	}
	active, activeFound := activePKIAuthority(state.Authorities)
	if !activeFound || active.Generation != 2 || state.Settings == nil || state.Settings.SecurityRevision != 2 {
		t.Fatalf("cutover canonical state = settings %+v authorities %+v", state.Settings, state.Authorities)
	}
	old, oldFound := authorityByGeneration(state.Authorities, 1)
	if !oldFound || old.Status != "retiring" || old.RetireDeadline == nil || old.EncryptedKeyRef == nil {
		t.Fatalf("old authority after cutover = %+v", old)
	}

	now = old.RetireDeadline.Add(time.Second)
	if err := runtime.ReconcilePending(t.Context()); err != nil {
		t.Fatalf("ReconcilePending(retire) error = %v", err)
	}
	state, err = store.LoadPKICanonicalState(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	row, _ = findPKILifecycleRow(state, operationID)
	old, _ = authorityByGeneration(state.Authorities, 1)
	if row.State != storage.PKILifecycleJobStateSucceeded || row.Phase != PKICARotationPhaseSucceeded ||
		old.Status != "retired" || old.EncryptedKeyRef != nil || old.PrivateKeyDestroyPendingAt == nil || old.PrivateKeyDestroyedAt == nil ||
		state.Settings.SecurityRevision != 3 {
		t.Fatalf("retired canonical state = row %+v settings %+v old %+v", row, state.Settings, old)
	}
	persisted, err := storage.ValidateCanonicalPKISecuritySnapshot(state)
	if err != nil || len(persisted.TrustRoots) != 1 || persisted.TrustRoots[0].Generation != 2 {
		t.Fatalf("retired trust snapshot = %+v, error = %v", persisted, err)
	}
	if _, err := vault.OpenCAKey(pkiVaultReference(state.Settings.PKIDomainID, 1, pkiBackupCAPurpose),
		state.Settings.PKIDomainID, 1, pkiBackupCAPurpose); err == nil {
		t.Fatal("retired authority key remained readable")
	}
}

func TestPKIAuthorityTransitionAuditSkipsSamePhaseRunningRetry(t *testing.T) {
	previous := PKICARotationJob{Phase: PKICARotationPhaseDistributeTrust, State: PKICARotationStateRunning}
	retry := previous
	retry.AckDeadline = time.Now().UTC().Add(time.Hour)
	retry.BlockedIdentityIDs = []string{"identity-offline"}
	if shouldAppendPKIAuthorityTransitionEvent(previous, retry) {
		t.Fatal("same-phase running ACK bookkeeping was treated as an audit transition")
	}
	retry.State = PKICARotationStateBlocked
	if !shouldAppendPKIAuthorityTransitionEvent(previous, retry) {
		t.Fatal("running-to-blocked transition did not require an audit event")
	}
	retry.State = PKICARotationStateRunning
	retry.Phase = PKICARotationPhaseReissue
	if !shouldAppendPKIAuthorityTransitionEvent(previous, retry) {
		t.Fatal("phase transition did not require an audit event")
	}
}

func TestPKIAuthorityRuntimeRetriesReissueDispatchAfterInitialFailure(t *testing.T) {
	root := t.TempDir()
	store := newPKIAuthorityRuntimeTestStore(t, root)
	vault, err := OpenPKIVault(PKIVaultConfig{DataRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(vault.Close)
	bootstrap := bootstrapPKIAuthorityRuntimeTest(t, store, vault)
	ctx := t.Context()
	now := time.Now().UTC().Truncate(time.Second)
	clock := func() time.Time { return now }
	const (
		agentID    = "edge-reissue-retry"
		identityID = "identity-edge-reissue-retry"
	)
	if err := store.SaveAgent(ctx, storage.AgentRow{
		ID: agentID, Name: agentID, AgentToken: "token-edge-reissue-retry", Mode: "pull",
		LastSeenAt: now.Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatal(err)
	}
	state, err := store.LoadPKICanonicalState(ctx)
	if err != nil || state.Settings == nil {
		t.Fatalf("load bootstrap state: settings=%+v error=%v", state.Settings, err)
	}
	if err := store.WithPKITransaction(ctx, func(tx *storage.PKITransaction) error {
		return tx.CreatePKIIdentity(ctx, storage.PKIIdentityRow{
			ID: identityID, PKIDomainID: state.Settings.PKIDomainID,
			Kind: storage.PKIIdentityKindAgent, AgentID: agentID, State: storage.PKIIdentityStateEnrollmentRequired,
			CreatedAt: now, UpdatedAt: now,
		})
	}); err != nil {
		t.Fatal(err)
	}
	vaultSigner, err := NewPKIVaultAuthoritySigner(vault)
	if err != nil {
		t.Fatal(err)
	}
	leaseSigner, err := NewPKILeaseAuthoritySigner(bootstrap.lease, vaultSigner)
	if err != nil {
		t.Fatal(err)
	}
	enrollment, err := NewPKIEnrollmentService(PKIEnrollmentServiceOptions{
		Store: store, Lease: bootstrap.lease, AuthoritySigner: leaseSigner, LocalAgentID: "local",
		Clock: clock, NewID: incrementingPKIID("rotation-dispatch-retry"),
	})
	if err != nil {
		t.Fatal(err)
	}
	binding, err := newPKIIdentityBinding(
		state.Settings.PKIDomainID, storage.PKIIdentityKindAgent, agentID, "",
		storage.PKICertificatePurposeClient, nil, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	credential, err := enrollment.EnrollAuthenticated(ctx, agentID, "token-edge-reissue-retry", PKIEnrollRequest{
		RequestID: "rotation-dispatch-retry-enrollment", Kind: storage.PKIIdentityKindAgent,
		Purpose: storage.PKICertificatePurposeClient,
		CSRPEM:  mustPKIEnrollmentCSR(t, mustPKIEnrollmentKey(t), binding, false),
	})
	if err != nil || credential.CAGeneration != 1 || credential.IdentityID != identityID {
		t.Fatalf("initial enrollment = %+v, error=%v", credential, err)
	}
	runtime := newPKIAuthorityRuntimeForTest(t, store, vault, bootstrap, clock, nil)
	failingSession := &pkiFailFirstRotationTaskSession{
		tasks: runtime.tasks, store: store, agentID: agentID, clock: clock,
	}
	if err := runtime.tasks.RegisterSession(TaskSessionRegistration{
		AgentID: agentID, SessionID: "initial", Session: failingSession,
	}); err != nil {
		t.Fatal(err)
	}
	operationID := queuePKIAuthorityRuntimeTestJob(t, store, bootstrap.lease,
		"ca-runtime-reissue-dispatch-retry", "ca_rotate", now)

	if err := runtime.StartNormal(ctx, operationID, "retry failed reissue dispatch"); err != nil {
		t.Fatalf("StartNormal() error = %v", err)
	}
	state, err = store.LoadPKICanonicalState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	job, found := findPKILifecycleRow(state, operationID)
	if !found || job.Phase != PKICARotationPhaseReissue || job.State != storage.PKILifecycleJobStateRunning ||
		job.NextAttemptAt == nil || !strings.HasPrefix(job.LastError, pkiRotationDispatchErrorPrefix) ||
		failingSession.rotationAttempts != 1 {
		t.Fatalf("initial failed dispatch = job %+v attempts %d", job, failingSession.rotationAttempts)
	}

	retrySession := newStubTaskSession(agentID)
	if err := runtime.tasks.RegisterSession(TaskSessionRegistration{
		AgentID: agentID, SessionID: "retry", Session: retrySession,
	}); err != nil {
		t.Fatal(err)
	}
	now = job.NextAttemptAt.Add(time.Second)
	if err := store.SaveAgentHeartbeat(ctx, storage.AgentRow{
		ID: agentID, LastSeenAt: now.Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatal(err)
	}
	if err := runtime.ReconcilePending(ctx); err != nil {
		t.Fatalf("ReconcilePending(retry) error = %v", err)
	}
	first := retrySession.WaitForTask(t)
	assertPKIRotationTask(t, first, operationID, identityID, 2, PKICARotationPhaseReissue)
	state, err = store.LoadPKICanonicalState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	job, _ = findPKILifecycleRow(state, operationID)
	if job.NextAttemptAt != nil || strings.HasPrefix(job.LastError, pkiRotationDispatchErrorPrefix) {
		t.Fatalf("successful retry did not clear dispatch backoff: %+v", job)
	}

	if err := runtime.tasks.ApplyUpdate(ctx, TaskUpdateInput{
		AgentID: agentID, TaskID: first.ID, State: "failed", Error: "injected rotation failure",
	}); err != nil {
		t.Fatal(err)
	}
	now = now.Add(runtime.heartbeatInterval + time.Second)
	if err := store.SaveAgentHeartbeat(ctx, storage.AgentRow{
		ID: agentID, LastSeenAt: now.Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatal(err)
	}
	if err := runtime.ReconcilePending(ctx); err != nil {
		t.Fatalf("ReconcilePending(after terminal failure) error = %v", err)
	}
	second := retrySession.WaitForTask(t)
	assertPKIRotationTask(t, second, operationID, identityID, 2, PKICARotationPhaseReissue)
	if second.ID == first.ID {
		t.Fatalf("terminal task %q was reused instead of retried", first.ID)
	}
	if err := runtime.ReconcilePending(ctx); err != nil {
		t.Fatalf("ReconcilePending(with in-flight retry) error = %v", err)
	}
	select {
	case duplicate := <-retrySession.tasks:
		t.Fatalf("in-flight rotation task was duplicated: %+v", duplicate)
	default:
	}
}

func assertPKIRotationTask(
	t *testing.T,
	task TaskEnvelope,
	operationID string,
	identityID string,
	generation int64,
	phase string,
) {
	t.Helper()
	payloadGeneration, validGeneration := taskPayloadInt64(task.Payload["ca_generation"])
	if task.Type != TaskTypePKIForceRotation || taskPayloadString(task.Payload["operation_id"]) != operationID ||
		taskPayloadString(task.Payload["identity_id"]) != identityID || taskPayloadString(task.Payload["phase"]) != phase ||
		!validGeneration || payloadGeneration != generation {
		t.Fatalf("rotation task = %+v", task)
	}
}

func TestPKIAuthorityRotationParticipantsIncludeDurableEmbeddedAcknowledgement(t *testing.T) {
	root := t.TempDir()
	store := newPKIAuthorityRuntimeTestStore(t, root)
	vault, err := OpenPKIVault(PKIVaultConfig{DataRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(vault.Close)
	bootstrap := bootstrapPKIAuthorityRuntimeTest(t, store, vault)
	now := time.Now().UTC().Truncate(time.Second)
	runtime := newPKIAuthorityRuntimeForTest(t, store, vault, bootstrap, func() time.Time { return now }, nil)
	if err := store.SaveRelayListeners(t.Context(), "local", []storage.RelayListenerRow{{
		ID: 81, AgentID: "local", Name: "local relay", ListenHost: "0.0.0.0", BindHostsJSON: `["0.0.0.0"]`,
		ListenPort: 8443, PublicHost: "relay.example.test", PublicPort: 8443, Enabled: true,
		TLSMode: "pki_mtls", TransportMode: "tls_tcp",
	}}); err != nil {
		t.Fatal(err)
	}
	state, err := store.LoadPKICanonicalState(t.Context())
	if err != nil || state.Settings == nil {
		t.Fatalf("LoadPKICanonicalState() = %+v, %v", state.Settings, err)
	}
	agentCertificateID := "local-agent-certificate"
	listenerCertificateID := "local-listener-certificate"
	state.Identities = append(state.Identities,
		storage.PKIIdentityRow{ID: "local-agent-identity", AgentID: "local", Kind: storage.PKIIdentityKindAgent, State: storage.PKIIdentityStateActive, CurrentCertificateID: &agentCertificateID},
		storage.PKIIdentityRow{ID: "local-listener-identity", AgentID: "local", ListenerID: "81", Kind: storage.PKIIdentityKindListener, State: storage.PKIIdentityStateActive, CurrentCertificateID: &listenerCertificateID},
	)
	state.Certificates = append(state.Certificates,
		storage.PKICertificateRow{ID: agentCertificateID, IdentityID: "local-agent-identity", Status: storage.PKICertificateStatusActive, CAGeneration: 2},
		storage.PKICertificateRow{ID: listenerCertificateID, IdentityID: "local-listener-identity", Status: storage.PKICertificateStatusActive, CAGeneration: 2},
	)
	job := PKICARotationJob{CurrentGeneration: 1, NewGeneration: 2}
	participants, err := runtime.rotationParticipants(t.Context(), state, job)
	if err != nil || len(participants) != 1 || participants[0].TrustAcked || participants[0].CutoverAcked {
		t.Fatalf("participants without local ACK = %+v, %v", participants, err)
	}
	acknowledgement, err := json.Marshal(storage.PKISecurityAcknowledgement{
		PKIDomainID: state.Settings.PKIDomainID, PKIEpoch: state.Settings.PKIEpoch,
		SecurityRevision: state.Settings.SecurityRevision, Full: true,
		CertificateID: agentCertificateID, TrustGenerations: []int64{1, 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.WithPKITransaction(t.Context(), func(tx *storage.PKITransaction) error {
		return tx.SavePKISecurityAcknowledgement(t.Context(), "local", string(acknowledgement), now)
	}); err != nil {
		t.Fatal(err)
	}
	participants, err = runtime.rotationParticipants(t.Context(), state, job)
	if err != nil || len(participants) != 1 || !participants[0].TrustAcked || !participants[0].Reissued || participants[0].CutoverAcked {
		t.Fatalf("participants without active listener ACK = %+v, %v", participants, err)
	}
	acknowledgement, err = json.Marshal(storage.PKISecurityAcknowledgement{
		PKIDomainID: state.Settings.PKIDomainID, PKIEpoch: state.Settings.PKIEpoch,
		SecurityRevision: state.Settings.SecurityRevision, Full: true,
		CertificateID: agentCertificateID, TrustGenerations: []int64{1, 2},
		ListenerCredentials: []storage.PKIListenerCredentialAcknowledgement{{
			ListenerID: "81", IdentityID: "local-listener-identity",
			CertificateID: listenerCertificateID, CAGeneration: 2,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.WithPKITransaction(t.Context(), func(tx *storage.PKITransaction) error {
		return tx.SavePKISecurityAcknowledgement(t.Context(), "local", string(acknowledgement), now)
	}); err != nil {
		t.Fatal(err)
	}
	participants, err = runtime.rotationParticipants(t.Context(), state, job)
	if err != nil || len(participants) != 1 || !participants[0].CutoverAcked {
		t.Fatalf("participants with active listener ACK = %+v, %v", participants, err)
	}
}

func TestPKIEmergencyAuthorityRuntimeLeavesDurableFailClosedStateOnGenerationFailure(t *testing.T) {
	root := t.TempDir()
	store := newPKIAuthorityRuntimeTestStore(t, root)
	vault, err := OpenPKIVault(PKIVaultConfig{DataRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(vault.Close)
	bootstrap := bootstrapPKIAuthorityRuntimeTest(t, store, vault)
	now := time.Now().UTC().Truncate(time.Second)
	injected := errors.New("injected emergency key generation failure")
	runtime := newPKIAuthorityRuntimeForTest(t, store, vault, bootstrap, func() time.Time { return now }, injected)
	operationID := queuePKIAuthorityRuntimeTestJob(t, store, bootstrap.lease, "ca-runtime-emergency-fail", "emergency_ca_rotate", now)

	if err := runtime.StartEmergency(t.Context(), operationID, "suspected compromise", "panel"); err != nil {
		t.Fatalf("StartEmergency(failure) error = %v", err)
	}
	state, err := store.LoadPKICanonicalState(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	row, _ := findPKILifecycleRow(state, operationID)
	active, found := activePKIAuthority(state.Authorities)
	if row.State != storage.PKILifecycleJobStatePending || row.Phase != "relay_disabled" || row.NextAttemptAt == nil ||
		state.Settings == nil || !state.Settings.RelayFailClosed || state.Settings.SecurityRevision != 1 ||
		!found || active.Generation != 1 {
		t.Fatalf("failed emergency canonical state = row %+v settings %+v authorities %+v", row, state.Settings, state.Authorities)
	}
	snapshot, err := storage.ValidateCanonicalPKISecuritySnapshot(state)
	if err != nil || snapshot.SignerGeneration != 1 {
		t.Fatalf("failed emergency snapshot = %+v, error = %v", snapshot, err)
	}
}

func TestPKIEmergencyAuthorityRuntimeResumesDurableFailClosedStateAfterRestart(t *testing.T) {
	root := t.TempDir()
	store := newPKIAuthorityRuntimeTestStore(t, root)
	vault, err := OpenPKIVault(PKIVaultConfig{DataRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(vault.Close)
	bootstrap := bootstrapPKIAuthorityRuntimeTest(t, store, vault)
	now := time.Now().UTC().Truncate(time.Second)
	operationID := queuePKIAuthorityRuntimeTestJob(t, store, bootstrap.lease,
		"ca-runtime-emergency-restart", "emergency_ca_rotate", now)
	injected := errors.New("injected emergency key generation failure")
	failedRuntime := newPKIAuthorityRuntimeForTest(t, store, vault, bootstrap, func() time.Time { return now }, injected)
	if err := failedRuntime.StartEmergency(t.Context(), operationID, "restart recovery", "panel"); err != nil {
		t.Fatalf("StartEmergency(failure) error = %v", err)
	}

	now = now.Add(time.Minute + time.Second)
	restartedRuntime := newPKIAuthorityRuntimeForTest(t, store, vault, bootstrap, func() time.Time { return now }, nil)
	if err := restartedRuntime.ReconcilePending(t.Context()); err != nil {
		t.Fatalf("ReconcilePending(restart) error = %v", err)
	}
	state, err := store.LoadPKICanonicalState(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	row, _ := findPKILifecycleRow(state, operationID)
	active, activeFound := activePKIAuthority(state.Authorities)
	old, oldFound := authorityByGeneration(state.Authorities, 1)
	if row.State != storage.PKILifecycleJobStateSucceeded || row.Phase != "completed" || row.NextAttemptAt != nil ||
		state.Settings == nil || state.Settings.RelayFailClosed || state.Settings.SecurityRevision != 2 ||
		!activeFound || active.Generation != 2 || !oldFound || old.Status != "revoked" || old.PrivateKeyDestroyedAt == nil {
		t.Fatalf("restarted emergency state = row %+v settings %+v authorities %+v", row, state.Settings, state.Authorities)
	}
}

func TestPKIEmergencyAuthorityRuntimeRetriesRelayEnableAfterReplacement(t *testing.T) {
	root := t.TempDir()
	store := newPKIAuthorityRuntimeTestStore(t, root)
	vault, err := OpenPKIVault(PKIVaultConfig{DataRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(vault.Close)
	bootstrap := bootstrapPKIAuthorityRuntimeTest(t, store, vault)
	now := time.Now().UTC().Truncate(time.Second)
	operationID := queuePKIAuthorityRuntimeTestJob(t, store, bootstrap.lease,
		"ca-runtime-emergency-enable-retry", "emergency_ca_rotate", now)
	runtime := newPKIAuthorityRuntimeForTest(t, store, vault, bootstrap, func() time.Time { return now }, nil)
	runtime.relayGate = &pkiEmergencyRelayTestGate{enableErr: errors.New("injected relay enable failure")}
	if err := runtime.StartEmergency(t.Context(), operationID, "relay enable retry", "panel"); err != nil {
		t.Fatalf("StartEmergency(enable failure) error = %v", err)
	}
	state, err := store.LoadPKICanonicalState(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	row, _ := findPKILifecycleRow(state, operationID)
	active, activeFound := activePKIAuthority(state.Authorities)
	if row.State != storage.PKILifecycleJobStatePending || row.Phase != "relay_enable_pending" || row.NextAttemptAt == nil ||
		state.Settings == nil || !state.Settings.RelayFailClosed || state.Settings.SecurityRevision != 2 ||
		!activeFound || active.Generation != 2 {
		t.Fatalf("replacement awaiting relay enable = row %+v settings %+v authorities %+v", row, state.Settings, state.Authorities)
	}

	now = now.Add(time.Minute + time.Second)
	restartedRuntime := newPKIAuthorityRuntimeForTest(t, store, vault, bootstrap, func() time.Time { return now }, nil)
	if err := restartedRuntime.ReconcilePending(t.Context()); err != nil {
		t.Fatalf("ReconcilePending(relay enable) error = %v", err)
	}
	state, err = store.LoadPKICanonicalState(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	row, _ = findPKILifecycleRow(state, operationID)
	active, activeFound = activePKIAuthority(state.Authorities)
	if row.State != storage.PKILifecycleJobStateSucceeded || row.Phase != "completed" || row.NextAttemptAt != nil {
		t.Fatalf("relay enable retry state = %+v", row)
	}
	if !activeFound || active.Generation != 2 || active.EncryptedKeyRef == nil || active.PrivateKeyDestroyPendingAt != nil || active.PrivateKeyDestroyedAt != nil {
		t.Fatalf("replacement authority key was not preserved across relay enable retry: %+v", active)
	}
}

func TestPKIAuthorityRuntimeReconcilesPendingKeyDestructionAfterRestart(t *testing.T) {
	root := t.TempDir()
	store := newPKIAuthorityRuntimeTestStore(t, root)
	vault, err := OpenPKIVault(PKIVaultConfig{DataRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(vault.Close)
	bootstrap := bootstrapPKIAuthorityRuntimeTest(t, store, vault)
	now := time.Now().UTC().Truncate(time.Second)
	operationID := queuePKIAuthorityRuntimeTestJob(t, store, bootstrap.lease,
		"ca-runtime-destroy-restart", "emergency_ca_rotate", now)
	runtime := newPKIAuthorityRuntimeForTest(t, store, vault, bootstrap, func() time.Time { return now }, nil)
	injected := errors.New("injected key destruction failure")
	runtime.keyDestroyer = &pkiFailOnceAuthorityKeyDestroyer{delegate: vault, err: injected}
	if err := runtime.StartEmergency(t.Context(), operationID, "destroy recovery", "panel"); err != nil {
		t.Fatalf("StartEmergency() error = %v", err)
	}
	state, err := store.LoadPKICanonicalState(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	old, found := authorityByGeneration(state.Authorities, 1)
	if !found || old.Status != "revoked" || old.PrivateKeyDestroyPendingAt == nil || old.PrivateKeyDestroyedAt != nil || old.EncryptedKeyRef == nil {
		t.Fatalf("authority after interrupted destruction = %+v", old)
	}

	restartedRuntime := newPKIAuthorityRuntimeForTest(t, store, vault, bootstrap, func() time.Time { return now.Add(time.Second) }, nil)
	if err := restartedRuntime.ReconcilePending(t.Context()); err != nil {
		t.Fatalf("ReconcilePending(key destruction) error = %v", err)
	}
	state, err = store.LoadPKICanonicalState(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	old, _ = authorityByGeneration(state.Authorities, 1)
	if old.PrivateKeyDestroyPendingAt == nil || old.PrivateKeyDestroyedAt == nil || old.EncryptedKeyRef != nil {
		t.Fatalf("authority after destruction recovery = %+v", old)
	}
}

func TestPKIEmergencyAuthorityRuntimeAtomicallyInvalidatesOldTrust(t *testing.T) {
	root := t.TempDir()
	store := newPKIAuthorityRuntimeTestStore(t, root)
	vault, err := OpenPKIVault(PKIVaultConfig{DataRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(vault.Close)
	bootstrap := bootstrapPKIAuthorityRuntimeTest(t, store, vault)
	now := time.Now().UTC().Truncate(time.Second)
	runtime := newPKIAuthorityRuntimeForTest(t, store, vault, bootstrap, func() time.Time { return now }, nil)
	operationID := queuePKIAuthorityRuntimeTestJob(t, store, bootstrap.lease, "ca-runtime-emergency", "emergency_ca_rotate", now)

	if err := runtime.StartEmergency(t.Context(), operationID, "confirmed compromise", "panel"); err != nil {
		t.Fatalf("StartEmergency() error = %v", err)
	}
	state, err := store.LoadPKICanonicalState(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	row, _ := findPKILifecycleRow(state, operationID)
	active, found := activePKIAuthority(state.Authorities)
	old, oldFound := authorityByGeneration(state.Authorities, 1)
	if row.State != storage.PKILifecycleJobStateSucceeded || row.Phase != "completed" ||
		state.Settings == nil || state.Settings.RelayFailClosed || state.Settings.SecurityRevision != 2 ||
		!found || active.Generation != 2 || !oldFound || old.Status != "revoked" || old.PrivateKeyDestroyPendingAt == nil || old.PrivateKeyDestroyedAt == nil {
		t.Fatalf("successful emergency canonical state = row %+v settings %+v authorities %+v", row, state.Settings, state.Authorities)
	}
	snapshot, err := storage.ValidateCanonicalPKISecuritySnapshot(state)
	if err != nil || len(snapshot.TrustRoots) != 1 || snapshot.TrustRoots[0].Generation != 2 {
		t.Fatalf("successful emergency snapshot = %+v, error = %v", snapshot, err)
	}
}

func TestPKIEmergencyAuthorityRuntimeWaitsForDisableApplyAndDrainBeforeReplacement(t *testing.T) {
	root := t.TempDir()
	store := newPKIAuthorityRuntimeTestStore(t, root)
	vault, err := OpenPKIVault(PKIVaultConfig{DataRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(vault.Close)
	bootstrap := bootstrapPKIAuthorityRuntimeTest(t, store, vault)
	ctx := t.Context()
	if err := store.SaveAgent(ctx, storage.AgentRow{
		ID: "edge-delayed", Name: "edge-delayed", AgentToken: "token-edge-delayed", Mode: "pull",
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveRelayListeners(ctx, "edge-delayed", []storage.RelayListenerRow{{
		ID: 71, AgentID: "edge-delayed", Name: "delayed-relay", ListenHost: "0.0.0.0",
		BindHostsJSON: `["0.0.0.0"]`, ListenPort: 7443, PublicHost: "relay.example.test",
		PublicPort: 7443, Enabled: true, TLSMode: "pki_mtls", TransportMode: "tls_tcp",
	}}); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.DataDir = root
	cfg.EnableLocalAgent = false
	cfg.LocalAgentID = "local"
	relay := NewRelayListenerService(cfg, store)
	relayGate, err := NewPKIEmergencyRevisionRelayGate(relay)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	runtime := newPKIAuthorityRuntimeForTest(t, store, vault, bootstrap, func() time.Time { return now }, nil)
	runtime.relayGate = relayGate
	operationID := queuePKIAuthorityRuntimeTestJob(t, store, bootstrap.lease,
		"ca-runtime-emergency-delayed-agent", "emergency_ca_rotate", now)

	if err := runtime.StartEmergency(ctx, operationID, "delayed agent barrier", "panel"); err != nil {
		t.Fatalf("StartEmergency() error = %v", err)
	}
	state, err := store.LoadPKICanonicalState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	job, _ := findPKILifecycleRow(state, operationID)
	active, _ := activePKIAuthority(state.Authorities)
	payload, err := decodePKIEmergencyRuntime(job)
	if err != nil {
		t.Fatal(err)
	}
	if job.Phase != "relay_disable_pending" || job.State != storage.PKILifecycleJobStatePending ||
		active.Generation != 1 || payload.RelayDisableBarrier.Converged ||
		payload.RelayDisableBarrier.Revisions["edge-delayed"] <= 0 {
		t.Fatalf("pre-apply emergency barrier = job %+v active %+v payload %+v", job, active, payload)
	}
	beforeApply, err := store.ListAgentRevisions(ctx, "edge-delayed")
	if err != nil || len(beforeApply) != 1 {
		t.Fatalf("disable revisions before delayed pull = %+v error=%v", beforeApply, err)
	}

	api := newRevisionAPITestService(t, store)
	applyEmergencyRelayRevision(t, api, "edge-delayed", false)
	now = now.Add(runtime.heartbeatInterval + time.Second)
	if err := runtime.ReconcilePending(ctx); err != nil {
		t.Fatalf("ReconcilePending(disable applied) error = %v", err)
	}
	state, err = store.LoadPKICanonicalState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	job, _ = findPKILifecycleRow(state, operationID)
	active, _ = activePKIAuthority(state.Authorities)
	payload, err = decodePKIEmergencyRuntime(job)
	if err != nil {
		t.Fatal(err)
	}
	if active.Generation != 2 || job.Phase != "relay_enable_pending" || job.State != storage.PKILifecycleJobStatePending ||
		state.Settings == nil || !state.Settings.RelayFailClosed ||
		len(payload.RequiredReenrollmentAgentIDs) != 1 || payload.RequiredReenrollmentAgentIDs[0] != "edge-delayed" {
		t.Fatalf("replacement after disable convergence = job %+v active %+v", job, active)
	}
	revisions, err := store.ListAgentRevisions(ctx, "edge-delayed")
	if err != nil || len(revisions) != 1 || revisions[0].State != storage.AgentRevisionStateApplied ||
		revisions[0].DrainState != storage.AgentRevisionDrainStateDrained {
		t.Fatalf("disabled remote revision sequence = %+v error=%v", revisions, err)
	}
	agents, err := store.ListAgents(ctx)
	if err != nil || len(agents) != 1 || agents[0].AgentToken != "" {
		t.Fatalf("replacement did not fence the old remote control credential: agents=%+v error=%v", agents, err)
	}

	agentCredential := reenrollEmergencyRemoteAgent(t, store, vault, bootstrap, "edge-delayed", func() time.Time { return now })
	vaultSigner, err := NewPKIVaultAuthoritySigner(vault)
	if err != nil {
		t.Fatal(err)
	}
	leaseSigner, err := NewPKILeaseAuthoritySigner(bootstrap.lease, vaultSigner)
	if err != nil {
		t.Fatal(err)
	}
	enrollment, err := NewPKIEnrollmentService(PKIEnrollmentServiceOptions{
		Store: store, Lease: bootstrap.lease, AuthoritySigner: leaseSigner, LocalAgentID: "local",
		Clock: func() time.Time { return now }, NewID: incrementingPKIID("emergency-listener-enrollment"),
	})
	if err != nil {
		t.Fatal(err)
	}
	listenerBinding, err := newPKIIdentityBinding(
		state.Settings.PKIDomainID, storage.PKIIdentityKindListener, "edge-delayed", "71",
		storage.PKICertificatePurposeServer, []string{"relay.example.test"}, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	listenerRequest := PKIEnrollRequest{
		RequestID: "emergency-listener-71", Kind: storage.PKIIdentityKindListener, ListenerID: "71",
		Purpose: storage.PKICertificatePurposeServer, DNSNames: []string{"relay.example.test"},
		CSRPEM: mustPKIEnrollmentCSR(t, mustPKIEnrollmentKey(t), listenerBinding, false),
	}
	listenerCredential, err := enrollment.EnrollAuthenticated(
		ctx, "edge-delayed", agentCredential.AgentControlToken, listenerRequest,
	)
	if err != nil || listenerCredential.CAGeneration != 2 {
		t.Fatalf("emergency listener enrollment = %+v, error=%v", listenerCredential, err)
	}
	replayed, err := enrollment.EnrollAuthenticated(ctx, "edge-delayed", agentCredential.AgentControlToken, listenerRequest)
	if err != nil || replayed.CertificateID != listenerCredential.CertificateID {
		t.Fatalf("emergency listener replay = %+v, error=%v", replayed, err)
	}
	listenerRequest.RequestID = "emergency-listener-71-renewal"
	listenerRequest.CSRPEM = mustPKIEnrollmentCSR(t, mustPKIEnrollmentKey(t), listenerBinding, false)
	if _, err := enrollment.EnrollAuthenticated(
		ctx, "edge-delayed", agentCredential.AgentControlToken, listenerRequest,
	); !errors.Is(err, ErrPKIEnrollmentAuthorityUnavailable) {
		t.Fatalf("emergency listener renewal error = %v", err)
	}
	agentBinding, err := newPKIIdentityBinding(
		state.Settings.PKIDomainID, storage.PKIIdentityKindAgent, "edge-delayed", "",
		storage.PKICertificatePurposeClient, nil, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := enrollment.EnrollAuthenticated(ctx, "edge-delayed", agentCredential.AgentControlToken, PKIEnrollRequest{
		RequestID: "emergency-agent-renewal", Kind: storage.PKIIdentityKindAgent,
		Purpose: storage.PKICertificatePurposeClient,
		CSRPEM:  mustPKIEnrollmentCSR(t, mustPKIEnrollmentKey(t), agentBinding, false),
	}); !errors.Is(err, ErrPKIEnrollmentAuthorityUnavailable) {
		t.Fatalf("emergency agent renewal error = %v", err)
	}
	now = now.Add(runtime.heartbeatInterval + time.Second)
	if err := runtime.ReconcilePending(ctx); err != nil {
		t.Fatalf("ReconcilePending(after re-enrollment) error = %v", err)
	}
	state, err = store.LoadPKICanonicalState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	job, _ = findPKILifecycleRow(state, operationID)
	payload, err = decodePKIEmergencyRuntime(job)
	if err != nil {
		t.Fatal(err)
	}
	if job.Phase != "relay_enable_pending" || payload.RelayEnableBarrier.Converged ||
		payload.RelayEnableBarrier.Revisions["edge-delayed"] <= 0 || !payload.RelayRestoreOpened ||
		state.Settings == nil || state.Settings.RelayFailClosed {
		t.Fatalf("post-re-enrollment enable barrier = job %+v payload %+v settings %+v", job, payload, state.Settings)
	}
	applyEmergencyRelayRevision(t, api, "edge-delayed", true)
	firstEnableRevision := payload.RelayEnableBarrier.Revisions["edge-delayed"]
	superseding, err := relay.mutationExecutor.Execute(ctx, revisionpkg.MutationRequest{
		OperationID:   "supersede-emergency-enable-revision",
		Kind:          "test.supersede_emergency_enable_revision",
		ForceRevision: true,
		Request:       map[string]any{"reason": "prove final pointer fencing"},
		Targets:       configMutationTargets(cfg, []string{"edge-delayed"}, nil),
		ResourceState: relayListenerMutationResourceState,
		Mutate: func(context.Context, *storage.GormStore, map[string]int64) error {
			return nil
		},
	})
	if err != nil || len(superseding.Agents) != 1 || superseding.Agents[0].DesiredRevision <= firstEnableRevision {
		t.Fatalf("supersede applied enable revision = %+v, error = %v", superseding, err)
	}
	applyEmergencyRelayRevision(t, api, "edge-delayed", true)
	now = now.Add(runtime.heartbeatInterval + time.Second)
	if err := runtime.ReconcilePending(ctx); err != nil {
		t.Fatalf("ReconcilePending(enable superseded) error = %v", err)
	}
	state, err = store.LoadPKICanonicalState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	job, _ = findPKILifecycleRow(state, operationID)
	payload, err = decodePKIEmergencyRuntime(job)
	if err != nil {
		t.Fatal(err)
	}
	reissuedEnableRevision := payload.RelayEnableBarrier.Revisions["edge-delayed"]
	if job.Phase != "relay_enable_pending" || job.State != storage.PKILifecycleJobStatePending ||
		payload.RelayEnableBarrier.Converged || payload.RelayEnableBarrier.Attempt != 2 ||
		reissuedEnableRevision <= superseding.Agents[0].DesiredRevision || !payload.RelayRestoreOpened ||
		state.Settings == nil || state.Settings.RelayFailClosed {
		t.Fatalf("reissued current enable barrier = job %+v payload %+v settings %+v", job, payload, state.Settings)
	}
	applyEmergencyRelayRevision(t, api, "edge-delayed", true)
	now = now.Add(runtime.heartbeatInterval + time.Second)
	if err := runtime.ReconcilePending(ctx); err != nil {
		t.Fatalf("ReconcilePending(reissued enable applied) error = %v", err)
	}
	state, err = store.LoadPKICanonicalState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	job, _ = findPKILifecycleRow(state, operationID)
	if job.Phase != "completed" || job.State != storage.PKILifecycleJobStateSucceeded ||
		state.Settings == nil || state.Settings.RelayFailClosed {
		t.Fatalf("remote re-enrollment convergence = job %+v settings %+v", job, state.Settings)
	}
}

func TestEmergencyPKIRelayTargetRequiresExplicitRevocationConvergence(t *testing.T) {
	cfg := config.Default()
	cfg.EnableLocalAgent = false
	agents := []storage.AgentRow{{ID: "edge-revoked", Name: "edge-revoked", AgentToken: "", Mode: "pull"}}
	state := storage.PKICanonicalState{
		Identities: []storage.PKIIdentityRow{{
			ID: "identity-edge-revoked", Kind: storage.PKIIdentityKindAgent,
			AgentID: "edge-revoked", State: storage.PKIIdentityStateRevoked,
		}},
		LifecycleJobs: []storage.PKILifecycleJobRow{{
			ID: "revoke-edge-revoked", TargetID: "identity-edge-revoked", Kind: "revoke",
			Phase: "retry_pending", State: storage.PKILifecycleJobStatePending,
		}},
	}
	if got := emergencyPKIRelayAgentIDsFromState(cfg, agents, state, false); len(got) != 1 || got[0] != "edge-revoked" {
		t.Fatalf("disable targets before revocation convergence = %v", got)
	}
	if got := emergencyPKIRelayAgentIDsFromState(cfg, agents, state, true); len(got) != 0 {
		t.Fatalf("enable targets must keep replacement re-enrollment semantics separate = %v", got)
	}
	state.LifecycleJobs[0].Phase = "completed"
	state.LifecycleJobs[0].State = storage.PKILifecycleJobStateSucceeded
	if got := emergencyPKIRelayAgentIDsFromState(cfg, agents, state, false); len(got) != 0 {
		t.Fatalf("disable targets after explicit revocation convergence = %v", got)
	}
}

func TestEmergencyPKIRelayTargetKeepsRevocationConvergenceAfterCleanup(t *testing.T) {
	fixture := newPKIEnrollmentFixture(t)
	now := fixture.now.Add(31 * 24 * time.Hour)
	agent := storage.AgentRow{ID: "edge-revoked", Name: "edge-revoked", Mode: "pull"}
	if err := fixture.store.SaveAgent(t.Context(), agent); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.WithPKITransaction(t.Context(), func(tx *storage.PKITransaction) error {
		if err := tx.CreatePKIIdentity(t.Context(), storage.PKIIdentityRow{
			ID: "identity-edge-revoked", PKIDomainID: "domain-1", Kind: storage.PKIIdentityKindAgent,
			AgentID: agent.ID, State: storage.PKIIdentityStateRevoked,
			CreatedAt: fixture.now.Add(-time.Hour), UpdatedAt: fixture.now.Add(-time.Hour),
		}); err != nil {
			return err
		}
		if err := tx.CreatePKILifecycleJob(t.Context(), storage.PKILifecycleJobRow{
			ID: "revoke-edge-revoked", PKIDomainID: "domain-1", TargetType: "identity",
			TargetID: "identity-edge-revoked", Kind: "revoke", Phase: "completed",
			State: storage.PKILifecycleJobStateSucceeded, IdempotencyKey: "revoke-edge-revoked",
			CreatedAt: fixture.now.Add(-time.Hour), UpdatedAt: fixture.now.Add(-time.Hour),
		}); err != nil {
			return err
		}
		_, err := tx.PrunePKIInvalidData(t.Context(), now, storage.PKIInvalidDataRetention{
			ConsumedNonce: 24 * time.Hour, TerminalJob: 30 * 24 * time.Hour, AuditEvent: 365 * 24 * time.Hour,
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	state, err := fixture.store.LoadPKICanonicalState(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.EnableLocalAgent = false
	if got := emergencyPKIRelayAgentIDsFromState(cfg, []storage.AgentRow{agent}, state, false); len(got) != 0 {
		t.Fatalf("disable targets after cleanup = %v", got)
	}
}

func TestPKIEmergencyRelayRevisionIncludesFreshLocalAgentAndReplaysIdempotently(t *testing.T) {
	root := t.TempDir()
	store := newPKIAuthorityRuntimeTestStore(t, root)
	vault, err := OpenPKIVault(PKIVaultConfig{DataRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(vault.Close)
	bootstrap := bootstrapPKIAuthorityRuntimeTest(t, store, vault)
	ctx := t.Context()
	agents, err := store.ListAgents(ctx)
	if err != nil || len(agents) != 0 {
		t.Fatalf("fresh agents table = %+v error=%v", agents, err)
	}
	if err := store.SaveRelayListeners(ctx, "local", []storage.RelayListenerRow{{
		ID: 81, AgentID: "local", Name: "local-relay", ListenHost: "0.0.0.0",
		BindHostsJSON: `["0.0.0.0"]`, ListenPort: 8443, PublicHost: "local.example.test",
		PublicPort: 8443, Enabled: true, TLSMode: "pki_mtls", TransportMode: "tls_tcp",
	}}); err != nil {
		t.Fatal(err)
	}
	initialState, err := store.LoadPKICanonicalState(ctx)
	if err != nil || initialState.Settings == nil {
		t.Fatalf("load initial PKI state: %+v, %v", initialState.Settings, err)
	}
	if err := store.WithPKITransaction(ctx, func(tx *storage.PKITransaction) error {
		return tx.CreatePKIIdentity(ctx, storage.PKIIdentityRow{
			ID: "local-listener-before-emergency", PKIDomainID: initialState.Settings.PKIDomainID,
			Kind: storage.PKIIdentityKindListener, AgentID: "local", ListenerID: "81",
			State: storage.PKIIdentityStateEnrollmentRequired, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
		})
	}); err != nil {
		t.Fatalf("create pre-emergency listener identity: %v", err)
	}
	cfg := config.Default()
	cfg.DataDir = root
	cfg.EnableLocalAgent = true
	cfg.LocalAgentID = "local"
	relay := NewRelayListenerService(cfg, store)
	relayGate, err := NewPKIEmergencyRevisionRelayGate(relay)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	runtime := newPKIAuthorityRuntimeForTest(t, store, vault, bootstrap, func() time.Time { return now }, nil)
	runtime.relayGate = relayGate
	operationID := queuePKIAuthorityRuntimeTestJob(t, store, bootstrap.lease,
		"ca-runtime-emergency-local-only", "emergency_ca_rotate", now)

	for attempt := 0; attempt < 2; attempt++ {
		if err := runtime.StartEmergency(ctx, operationID, "fresh local barrier", "panel"); err != nil {
			t.Fatalf("StartEmergency(attempt %d) error = %v", attempt+1, err)
		}
	}
	state, err := store.LoadPKICanonicalState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	job, _ := findPKILifecycleRow(state, operationID)
	payload, err := decodePKIEmergencyRuntime(job)
	if err != nil {
		t.Fatal(err)
	}
	localRevision := payload.RelayDisableBarrier.Revisions["local"]
	if localRevision <= 0 || payload.RelayDisableBarrier.Converged || job.Phase != "relay_disable_pending" {
		t.Fatalf("fresh local emergency barrier = job %+v payload %+v", job, payload)
	}
	revisions, err := store.ListAgentRevisions(ctx, "local")
	emergencyRevisions := make([]storage.AgentRevisionRow, 0, len(revisions))
	for _, revision := range revisions {
		if revision.OperationID == payload.RelayDisableBarrier.OperationID {
			emergencyRevisions = append(emergencyRevisions, revision)
		}
	}
	if err != nil || len(emergencyRevisions) != 1 || emergencyRevisions[0].Revision != localRevision {
		t.Fatalf("idempotent local disable revisions = %+v error=%v", revisions, err)
	}
	pull, err := newRevisionAPITestService(t, store).PullRemoteRevision(ctx, "local")
	if err != nil || !pull.HasUpdate || pull.Snapshot == nil || len(pull.Snapshot.RelayListeners) != 0 {
		t.Fatalf("fresh local fail-closed pull = %+v error=%v", pull, err)
	}

	api := newRevisionAPITestService(t, store)
	applyEmergencyRelayRevision(t, api, "local", false)
	now = now.Add(runtime.heartbeatInterval + time.Second)
	if err := runtime.ReconcilePending(ctx); err != nil {
		t.Fatalf("ReconcilePending(local disable applied) error = %v", err)
	}
	state, err = store.LoadPKICanonicalState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	job, _ = findPKILifecycleRow(state, operationID)
	active, _ := activePKIAuthority(state.Authorities)
	if active.Generation != 2 || job.Phase != "relay_enable_pending" || state.Settings == nil || !state.Settings.RelayFailClosed {
		t.Fatalf("local enable barrier state = job %+v settings %+v active %+v", job, state.Settings, active)
	}
	listenerIdentity, found, err := storage.FindActivePKIIdentity(state, storage.PKIIdentityKindListener, "local", "81")
	if err != nil || !found || listenerIdentity.State != storage.PKIIdentityStateEnrollmentRequired || listenerIdentity.CurrentCertificateID != nil {
		t.Fatalf("emergency listener replacement identity = %+v, found=%t, error=%v", listenerIdentity, found, err)
	}
	var historicalListener storage.PKIIdentityRow
	for _, identity := range state.Identities {
		if identity.ID == "local-listener-before-emergency" {
			historicalListener = identity
		}
	}
	if historicalListener.State != storage.PKIIdentityStateRevoked || historicalListener.ActiveOwnerKey != nil {
		t.Fatalf("pre-emergency listener identity was not released: %+v", historicalListener)
	}
	vaultSigner, err := NewPKIVaultAuthoritySigner(vault)
	if err != nil {
		t.Fatal(err)
	}
	leaseSigner, err := NewPKILeaseAuthoritySigner(bootstrap.lease, vaultSigner)
	if err != nil {
		t.Fatal(err)
	}
	enrollment, err := NewPKIEnrollmentService(PKIEnrollmentServiceOptions{
		Store: store, Lease: bootstrap.lease, AuthoritySigner: leaseSigner, LocalAgentID: "local",
		Clock: func() time.Time { return now }, NewID: incrementingPKIID("emergency-local-listener"),
	})
	if err != nil {
		t.Fatal(err)
	}
	listenerBinding, err := newPKIIdentityBinding(
		state.Settings.PKIDomainID, storage.PKIIdentityKindListener, "local", "81",
		storage.PKICertificatePurposeServer, []string{"local.example.test"}, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	listenerCredential, err := enrollment.EnrollLocal(ctx, PKILocalEnrollRequest{
		RequestID: strings.Repeat("a", 32), Kind: storage.PKIIdentityKindListener, ListenerID: "81",
		Purpose: storage.PKICertificatePurposeServer, DNSNames: []string{"local.example.test"},
		CSRPEM: mustPKIEnrollmentCSR(t, mustPKIEnrollmentKey(t), listenerBinding, false),
	})
	if err != nil || listenerCredential.CAGeneration != 2 {
		t.Fatalf("emergency local listener enrollment = %+v, error=%v", listenerCredential, err)
	}
	applyEmergencyRelayRevision(t, api, "local", true)
	now = now.Add(runtime.heartbeatInterval + time.Second)
	if err := runtime.ReconcilePending(ctx); err != nil {
		t.Fatalf("ReconcilePending(local enable applied) error = %v", err)
	}
	state, err = store.LoadPKICanonicalState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	job, _ = findPKILifecycleRow(state, operationID)
	if job.Phase != "completed" || job.State != storage.PKILifecycleJobStateSucceeded ||
		state.Settings == nil || state.Settings.RelayFailClosed {
		t.Fatalf("completed local emergency barrier = job %+v settings %+v", job, state.Settings)
	}
}

func TestPKIEmergencyRelayBarrierReissuesAfterExactRevisionIsSuperseded(t *testing.T) {
	root := t.TempDir()
	store := newPKIAuthorityRuntimeTestStore(t, root)
	vault, err := OpenPKIVault(PKIVaultConfig{DataRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(vault.Close)
	bootstrap := bootstrapPKIAuthorityRuntimeTest(t, store, vault)
	ctx := t.Context()
	if err := store.SaveAgent(ctx, storage.AgentRow{
		ID: "edge-superseded", Name: "edge-superseded", AgentToken: "token-edge-superseded", Mode: "pull",
	}); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.DataDir = root
	cfg.EnableLocalAgent = false
	relay := NewRelayListenerService(cfg, store)
	relayGate, err := NewPKIEmergencyRevisionRelayGate(relay)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	runtime := newPKIAuthorityRuntimeForTest(t, store, vault, bootstrap, func() time.Time { return now }, nil)
	runtime.relayGate = relayGate
	operationID := queuePKIAuthorityRuntimeTestJob(t, store, bootstrap.lease,
		"ca-runtime-emergency-superseded-barrier", "emergency_ca_rotate", now)

	if err := runtime.StartEmergency(ctx, operationID, "superseded exact barrier", "panel"); err != nil {
		t.Fatal(err)
	}
	state, err := store.LoadPKICanonicalState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	job, _ := findPKILifecycleRow(state, operationID)
	payload, err := decodePKIEmergencyRuntime(job)
	if err != nil {
		t.Fatal(err)
	}
	firstRevision := payload.RelayDisableBarrier.Revisions["edge-superseded"]
	if firstRevision <= 0 || payload.RelayDisableBarrier.Attempt != 1 {
		t.Fatalf("first disable barrier = %+v", payload.RelayDisableBarrier)
	}
	superseding, err := relay.mutationExecutor.Execute(ctx, revisionpkg.MutationRequest{
		OperationID:   "supersede-emergency-disable-revision",
		Kind:          "test.supersede_emergency_disable_revision",
		ForceRevision: true,
		Request:       map[string]any{"reason": "prove exact barrier fencing"},
		Targets:       configMutationTargets(cfg, []string{"edge-superseded"}, nil),
		ResourceState: relayListenerMutationResourceState,
		Mutate: func(context.Context, *storage.GormStore, map[string]int64) error {
			return nil
		},
	})
	if err != nil {
		t.Fatalf("allocate superseding revision: %v", err)
	}
	if len(superseding.Agents) != 1 || superseding.Agents[0].DesiredRevision <= firstRevision {
		t.Fatalf("superseding mutation = %+v, first revision = %d", superseding, firstRevision)
	}
	applyEmergencyRelayRevision(t, newRevisionAPITestService(t, store), "edge-superseded", false)

	now = now.Add(runtime.heartbeatInterval + time.Second)
	if err := runtime.ReconcilePending(ctx); err != nil {
		t.Fatalf("ReconcilePending(superseded barrier) error = %v", err)
	}
	state, err = store.LoadPKICanonicalState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	job, _ = findPKILifecycleRow(state, operationID)
	payload, err = decodePKIEmergencyRuntime(job)
	if err != nil {
		t.Fatal(err)
	}
	active, _ := activePKIAuthority(state.Authorities)
	reissuedRevision := payload.RelayDisableBarrier.Revisions["edge-superseded"]
	if active.Generation != 1 || job.Phase != "relay_disable_pending" || payload.RelayDisableBarrier.Converged ||
		payload.RelayDisableBarrier.Attempt != 2 || reissuedRevision <= superseding.Agents[0].DesiredRevision {
		t.Fatalf("reissued exact barrier = job %+v active %+v barrier %+v", job, active, payload.RelayDisableBarrier)
	}
}

func TestEmergencyPKIListenerReenrollmentReadyTracksDeletionAndRecreation(t *testing.T) {
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	domainID := "domain-1"
	certificateID := "listener-certificate-replacement"
	target := pkiEmergencyListenerReenrollment{
		AgentID: "local", ListenerID: "81", IdentityID: "listener-identity-emergency",
	}
	payload := pkiEmergencyRuntimePayload{
		ReplacementGeneration:         2,
		RequiredReenrollmentListeners: []pkiEmergencyListenerReenrollment{target},
	}
	revokedTarget := storage.PKIIdentityRow{
		ID: target.IdentityID, PKIDomainID: domainID, Kind: storage.PKIIdentityKindListener, AgentID: target.AgentID,
		ListenerID: target.ListenerID, State: storage.PKIIdentityStateRevoked,
	}
	recreatedListenerID := "82"
	ownerDigest := sha256.Sum256([]byte(strings.Join([]string{
		"pki-identity-owner-v1", domainID, storage.PKIIdentityKindListener, target.AgentID, recreatedListenerID,
	}, "\x00")))
	ownerKey := hex.EncodeToString(ownerDigest[:])
	replacement := storage.PKIIdentityRow{
		ID: "listener-identity-recreated", PKIDomainID: domainID, Kind: storage.PKIIdentityKindListener, AgentID: target.AgentID,
		ListenerID: recreatedListenerID, ActiveOwnerKey: &ownerKey, State: storage.PKIIdentityStateActive, CurrentCertificateID: &certificateID,
	}
	state := storage.PKICanonicalState{
		Settings:   &storage.PKISettingsRow{PKIDomainID: domainID},
		Identities: []storage.PKIIdentityRow{revokedTarget, replacement},
		Certificates: []storage.PKICertificateRow{{
			ID: certificateID, IdentityID: replacement.ID, Status: storage.PKICertificateStatusActive,
			CAGeneration: 2, Purpose: storage.PKICertificatePurposeServer,
			NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour),
		}},
	}
	configured := []storage.RelayListenerRow{{ID: 82, AgentID: "local"}}
	if !emergencyPKIListenerReenrollmentReady(state, configured, payload, now) {
		t.Fatal("recreated listener with a replacement-generation credential did not converge")
	}

	pending := state
	pending.Identities = append([]storage.PKIIdentityRow(nil), state.Identities...)
	pending.Identities[1].State = storage.PKIIdentityStateEnrollmentRequired
	pending.Identities[1].CurrentCertificateID = nil
	if emergencyPKIListenerReenrollmentReady(pending, configured, payload, now) {
		t.Fatal("recreated listener converged before its replacement credential was active")
	}

	deleted := state
	deleted.Identities = []storage.PKIIdentityRow{revokedTarget}
	deleted.Certificates = nil
	if !emergencyPKIListenerReenrollmentReady(deleted, nil, payload, now) {
		t.Fatal("deleted listener with a converged revocation blocked emergency recovery")
	}
	deleted.Identities[0].State = storage.PKIIdentityStateEnrollmentRequired
	if emergencyPKIListenerReenrollmentReady(deleted, nil, payload, now) {
		t.Fatal("deleted listener was released before its emergency identity was revoked")
	}
}

func TestPKIEmergencyReplacementRechecksNewDisableTargetInFinalTransaction(t *testing.T) {
	root := t.TempDir()
	store := newPKIAuthorityRuntimeTestStore(t, root)
	vault, err := OpenPKIVault(PKIVaultConfig{DataRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(vault.Close)
	bootstrap := bootstrapPKIAuthorityRuntimeTest(t, store, vault)
	cfg := config.Default()
	cfg.DataDir = root
	cfg.EnableLocalAgent = false
	relay := NewRelayListenerService(cfg, store)
	relayGate, err := NewPKIEmergencyRevisionRelayGate(relay)
	if err != nil {
		t.Fatal(err)
	}
	insertGate := &pkiInsertAgentOnConfirmRelayGate{
		delegate: relayGate, insertWhenEnabled: false,
		agent: storage.AgentRow{ID: "edge-late-disable", Name: "edge-late-disable", AgentToken: "token-late-disable", Mode: "pull"},
	}
	now := time.Now().UTC().Truncate(time.Second)
	runtime := newPKIAuthorityRuntimeForTest(t, store, vault, bootstrap, func() time.Time { return now }, nil)
	runtime.relayGate = insertGate
	operationID := queuePKIAuthorityRuntimeTestJob(t, store, bootstrap.lease,
		"ca-runtime-emergency-late-disable-target", "emergency_ca_rotate", now)

	if err := runtime.StartEmergency(t.Context(), operationID, "late disable target", "panel"); err != nil {
		t.Fatal(err)
	}
	state, err := store.LoadPKICanonicalState(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	job, _ := findPKILifecycleRow(state, operationID)
	active, _ := activePKIAuthority(state.Authorities)
	if !insertGate.inserted || active.Generation != 1 || job.Phase != "relay_disable_pending" ||
		job.State != storage.PKILifecycleJobStatePending || state.Settings == nil || !state.Settings.RelayFailClosed {
		t.Fatalf("late disable target transaction = inserted %v job %+v active %+v settings %+v", insertGate.inserted, job, active, state.Settings)
	}
	now = now.Add(runtime.heartbeatInterval + time.Second)
	if err := runtime.ReconcilePending(t.Context()); err != nil {
		t.Fatal(err)
	}
	state, err = store.LoadPKICanonicalState(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	job, _ = findPKILifecycleRow(state, operationID)
	payload, err := decodePKIEmergencyRuntime(job)
	if err != nil {
		t.Fatal(err)
	}
	if payload.RelayDisableBarrier.Revisions["edge-late-disable"] <= 0 || payload.RelayDisableBarrier.Converged {
		t.Fatalf("late disable target barrier = %+v", payload.RelayDisableBarrier)
	}
}

func TestPKIEmergencyCompletionRechecksNewEnableTargetDuringRestoreWindow(t *testing.T) {
	root := t.TempDir()
	store := newPKIAuthorityRuntimeTestStore(t, root)
	vault, err := OpenPKIVault(PKIVaultConfig{DataRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(vault.Close)
	bootstrap := bootstrapPKIAuthorityRuntimeTest(t, store, vault)
	ctx := t.Context()
	if err := store.SaveAgent(ctx, storage.AgentRow{ID: "edge-existing", Name: "edge-existing", AgentToken: "token-existing", Mode: "pull"}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveRelayListeners(ctx, "edge-existing", []storage.RelayListenerRow{{
		ID: 91, AgentID: "edge-existing", Name: "existing-relay", ListenHost: "0.0.0.0",
		BindHostsJSON: `["0.0.0.0"]`, ListenPort: 9443, PublicHost: "existing.example.test",
		PublicPort: 9443, Enabled: true, TLSMode: "pki_mtls", TransportMode: "tls_tcp",
	}}); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.DataDir = root
	cfg.EnableLocalAgent = false
	relay := NewRelayListenerService(cfg, store)
	relayGate, err := NewPKIEmergencyRevisionRelayGate(relay)
	if err != nil {
		t.Fatal(err)
	}
	insertGate := &pkiInsertAgentOnConfirmRelayGate{
		delegate: relayGate, insertWhenEnabled: true,
		agent: storage.AgentRow{ID: "edge-late-enable", Name: "edge-late-enable", AgentToken: "token-late-enable", Mode: "pull"},
	}
	now := time.Now().UTC().Truncate(time.Second)
	runtime := newPKIAuthorityRuntimeForTest(t, store, vault, bootstrap, func() time.Time { return now }, nil)
	runtime.relayGate = insertGate
	operationID := queuePKIAuthorityRuntimeTestJob(t, store, bootstrap.lease,
		"ca-runtime-emergency-late-enable-target", "emergency_ca_rotate", now)

	if err := runtime.StartEmergency(ctx, operationID, "late enable target", "panel"); err != nil {
		t.Fatal(err)
	}
	applyEmergencyRelayRevision(t, newRevisionAPITestService(t, store), "edge-existing", false)
	now = now.Add(runtime.heartbeatInterval + time.Second)
	if err := runtime.ReconcilePending(ctx); err != nil {
		t.Fatal(err)
	}
	state, err := store.LoadPKICanonicalState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	job, _ := findPKILifecycleRow(state, operationID)
	active, _ := activePKIAuthority(state.Authorities)
	if insertGate.inserted || active.Generation != 2 || job.Phase != "relay_enable_pending" ||
		job.State != storage.PKILifecycleJobStatePending || state.Settings == nil || !state.Settings.RelayFailClosed {
		t.Fatalf("replacement re-enrollment wait = inserted %v job %+v active %+v settings %+v", insertGate.inserted, job, active, state.Settings)
	}
	agentCredential := reenrollEmergencyRemoteAgent(t, store, vault, bootstrap, "edge-existing", func() time.Time { return now })
	vaultSigner, err := NewPKIVaultAuthoritySigner(vault)
	if err != nil {
		t.Fatal(err)
	}
	leaseSigner, err := NewPKILeaseAuthoritySigner(bootstrap.lease, vaultSigner)
	if err != nil {
		t.Fatal(err)
	}
	enrollment, err := NewPKIEnrollmentService(PKIEnrollmentServiceOptions{
		Store: store, Lease: bootstrap.lease, AuthoritySigner: leaseSigner, LocalAgentID: "local",
		Clock: func() time.Time { return now }, NewID: incrementingPKIID("emergency-late-enable-listener"),
	})
	if err != nil {
		t.Fatal(err)
	}
	listenerBinding, err := newPKIIdentityBinding(
		state.Settings.PKIDomainID, storage.PKIIdentityKindListener, "edge-existing", "91",
		storage.PKICertificatePurposeServer, []string{"existing.example.test"}, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	listenerCredential, err := enrollment.EnrollAuthenticated(ctx, "edge-existing", agentCredential.AgentControlToken, PKIEnrollRequest{
		RequestID: "emergency-late-enable-listener-91", Kind: storage.PKIIdentityKindListener, ListenerID: "91",
		Purpose: storage.PKICertificatePurposeServer, DNSNames: []string{"existing.example.test"},
		CSRPEM: mustPKIEnrollmentCSR(t, mustPKIEnrollmentKey(t), listenerBinding, false),
	})
	if err != nil || listenerCredential.CAGeneration != 2 {
		t.Fatalf("emergency late-enable listener enrollment = %+v, error=%v", listenerCredential, err)
	}
	now = now.Add(runtime.heartbeatInterval + time.Second)
	if err := runtime.ReconcilePending(ctx); err != nil {
		t.Fatal(err)
	}
	applyEmergencyRelayRevision(t, newRevisionAPITestService(t, store), "edge-existing", true)
	now = now.Add(runtime.heartbeatInterval + time.Second)
	if err := runtime.ReconcilePending(ctx); err != nil {
		t.Fatal(err)
	}
	if !insertGate.inserted {
		t.Fatal("late enable target was not inserted in the final confirmation transaction")
	}
	now = now.Add(runtime.heartbeatInterval + time.Second)
	if err := runtime.ReconcilePending(ctx); err != nil {
		t.Fatal(err)
	}
	state, err = store.LoadPKICanonicalState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	job, _ = findPKILifecycleRow(state, operationID)
	payload, err := decodePKIEmergencyRuntime(job)
	if err != nil {
		t.Fatal(err)
	}
	if payload.RelayEnableBarrier.Revisions["edge-late-enable"] <= 0 || payload.RelayEnableBarrier.Converged ||
		!payload.RelayRestoreOpened || state.Settings == nil || state.Settings.RelayFailClosed {
		t.Fatalf("late enable target barrier = job %+v barrier %+v settings %+v", job, payload.RelayEnableBarrier, state.Settings)
	}
}

func reenrollEmergencyRemoteAgent(
	t *testing.T,
	store *storage.GormStore,
	vault *PKIVault,
	bootstrap pkiAuthorityRuntimeTestBootstrap,
	agentID string,
	clock func() time.Time,
) PKIEnrollmentResult {
	t.Helper()
	state, err := store.LoadPKICanonicalState(t.Context())
	if err != nil || state.Settings == nil {
		t.Fatalf("load emergency re-enrollment state: %+v, %v", state.Settings, err)
	}
	tokens, err := NewPKITokenService(PKITokenServiceOptions{
		Store: store, LocalAgentID: "local", Clock: clock, NewID: incrementingPKIID("emergency-reenrollment-token"),
	})
	if err != nil {
		t.Fatal(err)
	}
	issued, err := tokens.Create(t.Context(), PKIEnrollmentTokenRequest{
		Scope: PKIEnrollmentTokenScopeBoundReenrollment, BoundAgentID: agentID, CreatedBy: "panel",
	})
	if err != nil {
		t.Fatal(err)
	}
	vaultSigner, err := NewPKIVaultAuthoritySigner(vault)
	if err != nil {
		t.Fatal(err)
	}
	leaseSigner, err := NewPKILeaseAuthoritySigner(bootstrap.lease, vaultSigner)
	if err != nil {
		t.Fatal(err)
	}
	enrollment, err := NewPKIEnrollmentService(PKIEnrollmentServiceOptions{
		Store: store, Lease: bootstrap.lease, AuthoritySigner: leaseSigner, LocalAgentID: "local",
		Clock: clock, NewID: incrementingPKIID("emergency-reenrollment"),
	})
	if err != nil {
		t.Fatal(err)
	}
	binding, err := newPKIIdentityBinding(
		state.Settings.PKIDomainID, storage.PKIIdentityKindAgent, agentID, "", storage.PKICertificatePurposeClient, nil, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	result, err := enrollment.EnrollAndBindAgent(t.Context(), PKIEnrollRequest{
		RequestID: "emergency-reenrollment-" + agentID, Token: issued.Token, AgentID: agentID,
		Kind: storage.PKIIdentityKindAgent, Purpose: storage.PKICertificatePurposeClient,
		CSRPEM: mustPKIEnrollmentCSR(t, mustPKIEnrollmentKey(t), binding, false),
	}, storage.AgentRow{
		ID: agentID, Name: agentID, AgentToken: "replacement-control-token-" + agentID,
		Mode: "pull", TagsJSON: "[]", CapabilitiesJSON: "[]",
	})
	if err != nil {
		t.Fatalf("emergency bound re-enrollment for %q: %v", agentID, err)
	}
	if result.AgentID != agentID || result.CAGeneration <= 1 || result.AgentControlToken == "" {
		t.Fatalf("emergency bound re-enrollment result = %+v", result)
	}
	return result
}

type pkiInsertAgentOnConfirmRelayGate struct {
	delegate          PKIEmergencyRuntimeRelayGate
	agent             storage.AgentRow
	insertWhenEnabled bool
	currentEnabled    bool
	inserted          bool
}

func (g *pkiInsertAgentOnConfirmRelayGate) DisablePKIRelay(
	ctx context.Context,
	previous PKIRelayRevisionBarrier,
) (PKIRelayRevisionBarrier, error) {
	g.currentEnabled = false
	return g.delegate.DisablePKIRelay(ctx, previous)
}

func (g *pkiInsertAgentOnConfirmRelayGate) EnablePKIRelay(
	ctx context.Context,
	previous PKIRelayRevisionBarrier,
) (PKIRelayRevisionBarrier, error) {
	g.currentEnabled = true
	return g.delegate.EnablePKIRelay(ctx, previous)
}

func (g *pkiInsertAgentOnConfirmRelayGate) ConfirmPKIRelayBarrier(
	ctx context.Context,
	tx *storage.PKITransaction,
	barrier PKIRelayRevisionBarrier,
) (bool, error) {
	if !g.inserted && g.currentEnabled == g.insertWhenEnabled {
		if _, err := tx.UpsertPKIStableAgent(ctx, g.agent, true); err != nil {
			return false, err
		}
		g.inserted = true
	}
	return g.delegate.ConfirmPKIRelayBarrier(ctx, tx, barrier)
}

func applyEmergencyRelayRevision(t *testing.T, api *RevisionAPI, agentID string, relayEnabled bool) {
	t.Helper()
	pull, err := api.PullRemoteRevision(t.Context(), agentID)
	if err != nil || !pull.HasUpdate || pull.Lease == nil || pull.Snapshot == nil {
		t.Fatalf("PullRemoteRevision(%s) = %+v error=%v", agentID, pull, err)
	}
	if gotEnabled := len(pull.Snapshot.RelayListeners) > 0; gotEnabled != relayEnabled {
		t.Fatalf("revision %d relay enabled=%v, want %v", pull.Lease.Revision, gotEnabled, relayEnabled)
	}
	generationID := fmt.Sprintf("emergency-test-%s-%d", agentID, pull.Lease.Revision)
	start := RemoteRevisionStart{
		AgentID: agentID, Revision: pull.Lease.Revision, RetryCycle: pull.Lease.RetryCycle,
		Attempt: pull.Lease.Attempt, LeaseID: pull.Lease.LeaseID, GenerationID: generationID,
	}
	if _, err := api.StartRemoteRevision(t.Context(), agentID, start); err != nil {
		t.Fatalf("StartRemoteRevision(%s/%d) error = %v", agentID, pull.Lease.Revision, err)
	}
	report := RemoteRevisionReport{
		AgentID: agentID, Revision: pull.Lease.Revision, RetryCycle: pull.Lease.RetryCycle,
		Attempt: pull.Lease.Attempt, LeaseID: pull.Lease.LeaseID, GenerationID: generationID,
		Status: storage.AgentRevisionStateApplied,
	}
	status, err := api.ReportRemoteRevision(t.Context(), agentID, report)
	if err != nil {
		t.Fatalf("ReportRemoteRevision(%s/%d applied) error = %v", agentID, pull.Lease.Revision, err)
	}
	for _, generation := range status.Generations {
		if generation.State != storage.GenerationStateDraining || generation.Revision >= pull.Lease.Revision {
			continue
		}
		report.GenerationID = generation.GenerationID
		report.Status = storage.AgentRevisionDrainStateDrained
		if _, err := api.ReportRemoteRevision(t.Context(), agentID, report); err != nil {
			t.Fatalf("ReportRemoteRevision(%s/%d drain) error = %v", agentID, pull.Lease.Revision, err)
		}
	}
}

type pkiFailFirstRotationTaskSession struct {
	tasks            *TaskService
	store            *storage.GormStore
	agentID          string
	clock            func() time.Time
	rotationAttempts int
}

func (s *pkiFailFirstRotationTaskSession) SendTask(task TaskEnvelope) error {
	switch task.Type {
	case TaskTypePKISecurityUpdate:
		state, err := s.store.LoadPKICanonicalState(context.Background())
		if err != nil {
			return err
		}
		if state.Settings == nil {
			return errors.New("PKI settings are unavailable")
		}
		trustGenerations := make([]int64, 0, len(state.Authorities))
		for _, authority := range state.Authorities {
			if authority.Status != "retired" && authority.Status != "revoked" {
				trustGenerations = append(trustGenerations, authority.Generation)
			}
		}
		acknowledgement, err := json.Marshal(storage.PKISecurityAcknowledgement{
			PKIDomainID: state.Settings.PKIDomainID, PKIEpoch: state.Settings.PKIEpoch,
			SecurityRevision: state.Settings.SecurityRevision, Full: true,
			TrustGenerations: trustGenerations,
		})
		if err != nil {
			return err
		}
		if err := s.store.WithPKITransaction(context.Background(), func(tx *storage.PKITransaction) error {
			return tx.SavePKISecurityAcknowledgement(context.Background(), s.agentID, string(acknowledgement), s.clock().UTC())
		}); err != nil {
			return err
		}
		return s.tasks.ApplyUpdate(context.Background(), TaskUpdateInput{
			AgentID: s.agentID, TaskID: task.ID, State: "completed", Result: map[string]any{"ok": true},
		})
	case TaskTypePKIForceRotation:
		s.rotationAttempts++
		if s.rotationAttempts == 1 {
			return errors.New("injected initial rotation dispatch failure")
		}
	}
	return nil
}

func (s *pkiFailFirstRotationTaskSession) Close() error { return nil }

func newPKIAuthorityRuntimeForTest(
	t *testing.T,
	store *storage.GormStore,
	vault *PKIVault,
	bootstrap pkiAuthorityRuntimeTestBootstrap,
	clock func() time.Time,
	generationErr error,
) *PKIAuthorityRuntime {
	t.Helper()
	projectedSigner, ok := bootstrap.snapshotSigner.(PKIProjectedSecuritySnapshotSigner)
	if !ok {
		t.Fatal("bootstrap snapshot signer does not support projected snapshots")
	}
	state, err := store.LoadPKICanonicalState(t.Context())
	if err != nil || state.Settings == nil {
		t.Fatalf("load bootstrap settings: %+v, %v", state.Settings, err)
	}
	var generator PKIAuthorityGenerator
	if generationErr != nil {
		generator = &pkiAuthorityTestGenerator{err: generationErr}
	} else {
		generator, err = NewPKIVaultAuthorityGenerator(PKIVaultAuthorityGeneratorOptions{
			Vault: vault, PKIDomainID: state.Settings.PKIDomainID, Clock: clock,
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	tasks := NewTaskService(TaskServiceConfig{Now: clock})
	t.Cleanup(func() { _ = tasks.Close() })
	publisher, err := NewPKISecurityTaskPublisher(store, tasks)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := NewPKIAuthorityRuntime(PKIAuthorityRuntimeOptions{
		Store: store, Lease: bootstrap.lease, Generator: generator,
		SnapshotSigner: projectedSigner, SnapshotPublisher: publisher,
		Tasks: tasks, KeyDestroyer: vault, RelayGate: &pkiEmergencyRelayTestGate{},
		Clock: clock, HeartbeatInterval: 10 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	return runtime
}

type pkiAuthorityRuntimeTestBootstrap struct {
	lease          *PKILeaseService
	snapshotSigner PKISecuritySnapshotSigner
}

type pkiFailOnceAuthorityKeyDestroyer struct {
	delegate PKIAuthorityKeyDestroyer
	err      error
	failed   bool
}

func (d *pkiFailOnceAuthorityKeyDestroyer) DestroyCAKey(reference, domainID string, generation int64, purpose string) error {
	if !d.failed {
		d.failed = true
		return d.err
	}
	return d.delegate.DestroyCAKey(reference, domainID, generation, purpose)
}

func newPKIAuthorityRuntimeTestStore(t *testing.T, root string) *storage.GormStore {
	t.Helper()
	store, err := storage.NewStore(storage.StoreConfig{
		Driver: "sqlite", DataRoot: root, DSN: filepath.Join(root, "panel.db"), LocalAgentID: "local",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func bootstrapPKIAuthorityRuntimeTest(t *testing.T, store *storage.GormStore, vault *PKIVault) pkiAuthorityRuntimeTestBootstrap {
	t.Helper()
	repository, err := NewGormPKILeaseRepository(store)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := NewPKILeaseService(PKILeaseServiceOptions{
		Repository: repository, InstanceID: "authority-runtime-" + strings.ReplaceAll(t.Name(), "/", "-"),
	})
	if err != nil {
		t.Fatal(err)
	}
	vaultSigner, err := NewPKIVaultAuthoritySigner(vault)
	if err != nil {
		t.Fatal(err)
	}
	leaseSigner, err := NewPKILeaseAuthoritySigner(lease, vaultSigner)
	if err != nil {
		t.Fatal(err)
	}
	snapshotSigner, err := NewPKIVaultSecuritySnapshotSigner(PKIVaultSecuritySnapshotSignerOptions{
		StateSource: store, Signer: leaseSigner,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := BootstrapInternalPKI(t.Context(), InternalPKIBootstrapOptions{
		Store: store, Vault: vault, Lease: lease, SnapshotSigner: snapshotSigner,
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = lease.Relinquish(ctx)
	})
	return pkiAuthorityRuntimeTestBootstrap{lease: lease, snapshotSigner: snapshotSigner}
}

func queuePKIAuthorityRuntimeTestJob(
	t *testing.T,
	store *storage.GormStore,
	lease *PKILeaseService,
	operationID, kind string,
	now time.Time,
) string {
	t.Helper()
	grant, err := lease.RequirePKILease(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	deadline := grant.LeaseDeadline
	err = store.WithPKITransaction(t.Context(), func(tx *storage.PKITransaction) error {
		if err := requirePKIAuthorityLeaseFence(t.Context(), tx, grant); err != nil {
			return err
		}
		return tx.CreatePKILifecycleJob(t.Context(), storage.PKILifecycleJobRow{
			ID: operationID, PKIDomainID: grant.PKIDomainID, TargetType: "pki_domain", TargetID: grant.PKIDomainID,
			Kind: kind, Phase: "queued", State: storage.PKILifecycleJobStatePending,
			OperationID: operationID, IdempotencyKey: kind + ":" + operationID,
			RuntimeJSON: "{}", LeaseOwner: grant.InstanceID, LeaseDeadline: &deadline,
			CreatedAt: now, UpdatedAt: now,
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	return operationID
}
