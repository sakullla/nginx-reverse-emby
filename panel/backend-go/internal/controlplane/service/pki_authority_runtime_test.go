package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

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
	store, err := newServiceTestSQLiteStoreForAllTiers(t, root, "local")
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
