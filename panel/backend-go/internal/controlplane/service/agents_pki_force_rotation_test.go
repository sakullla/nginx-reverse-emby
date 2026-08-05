package service

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

func TestInternalPKIForceRotateWaitsForDurableCredentialAcknowledgement(t *testing.T) {
	t.Run("task completion without enrollment fails", func(t *testing.T) {
		fixture := newForceRotationServiceFixture(t)
		fixture.pki.forceRotationConvergence = 40 * time.Millisecond
		fixture.session.send = func(task TaskEnvelope) error {
			return fixture.tasks.ApplyUpdate(context.Background(), TaskUpdateInput{
				AgentID: fixture.agentID, TaskID: task.ID, State: "completed",
				Result: map[string]any{"identity_id": fixture.identityID, "request_id": "force-no-enrollment"},
			})
		}

		operation := invokeForceRotationForTest(t, fixture)
		if operation.State != storage.PKILifecycleJobStateFailed || operation.Phase != "failed" ||
			!strings.Contains(operation.LastError, "credential activation did not converge") {
			t.Fatalf("ForceRotate(task-only completion) operation = %+v", operation)
		}
		state := loadPKIEnrollmentState(t, fixture.store)
		identity := findForceRotationIdentity(t, state, fixture.identityID)
		if identity.CurrentCertificateID == nil || *identity.CurrentCertificateID != fixture.initialCertificateID {
			t.Fatalf("task-only force rotation changed current certificate: %+v", identity)
		}
	})

	t.Run("signed credential without acknowledgement fails", func(t *testing.T) {
		fixture := newForceRotationServiceFixture(t)
		fixture.pki.forceRotationConvergence = 40 * time.Millisecond
		requestID := "force-without-ack"
		binding, err := newPKIIdentityBinding(
			"domain-1", storage.PKIIdentityKindAgent, fixture.agentID, "",
			storage.PKICertificatePurposeClient, nil, nil,
		)
		if err != nil {
			t.Fatal(err)
		}
		csr := mustPKIEnrollmentCSR(t, mustPKIEnrollmentKey(t), binding, false)
		fixture.session.send = func(task TaskEnvelope) error {
			if err := fixture.tasks.ApplyUpdate(context.Background(), TaskUpdateInput{
				AgentID: fixture.agentID, TaskID: task.ID, State: "completed",
				Result: map[string]any{"identity_id": fixture.identityID, "request_id": requestID},
			}); err != nil {
				return err
			}
			_, err := fixture.enrollment.EnrollAuthenticated(
				context.Background(), fixture.agentID, "agent-a-token", PKIEnrollRequest{
					RequestID: requestID, Kind: storage.PKIIdentityKindAgent,
					Purpose: storage.PKICertificatePurposeClient, CSRPEM: csr,
				},
			)
			return err
		}

		operation := invokeForceRotationForTest(t, fixture)
		if operation.State != storage.PKILifecycleJobStateFailed || operation.Phase != "failed" ||
			!strings.Contains(operation.LastError, "credential activation did not converge") {
			t.Fatalf("ForceRotate(without acknowledgement) operation = %+v", operation)
		}
		state := loadPKIEnrollmentState(t, fixture.store)
		identity := findForceRotationIdentity(t, state, fixture.identityID)
		if identity.CurrentCertificateID == nil || *identity.CurrentCertificateID == fixture.initialCertificateID {
			t.Fatalf("force rotation did not sign a replacement before the missing ACK: %+v", identity)
		}
	})

	t.Run("matching enrollment and acknowledgement succeeds", func(t *testing.T) {
		fixture := newForceRotationServiceFixture(t)
		fixture.pki.forceRotationConvergence = time.Second
		requestID := "force-converged"
		binding, err := newPKIIdentityBinding(
			"domain-1", storage.PKIIdentityKindAgent, fixture.agentID, "",
			storage.PKICertificatePurposeClient, nil, nil,
		)
		if err != nil {
			t.Fatal(err)
		}
		csr := mustPKIEnrollmentCSR(t, mustPKIEnrollmentKey(t), binding, false)
		fixture.session.send = func(task TaskEnvelope) error {
			if err := fixture.tasks.ApplyUpdate(context.Background(), TaskUpdateInput{
				AgentID: fixture.agentID, TaskID: task.ID, State: "completed",
				Result: map[string]any{"identity_id": fixture.identityID, "request_id": requestID},
			}); err != nil {
				return err
			}
			result, err := fixture.enrollment.EnrollAuthenticated(
				context.Background(), fixture.agentID, "agent-a-token", PKIEnrollRequest{
					RequestID: requestID, Kind: storage.PKIIdentityKindAgent,
					Purpose: storage.PKICertificatePurposeClient, CSRPEM: csr,
				},
			)
			if err != nil {
				return err
			}
			acknowledgement, err := json.Marshal(storage.PKISecurityAcknowledgement{
				PKIDomainID: "domain-1", PKIEpoch: 1, SecurityRevision: 0, Full: true,
				CertificateID: result.CertificateID, TrustGenerations: []int64{1},
			})
			if err != nil {
				return err
			}
			return fixture.store.WithPKITransaction(context.Background(), func(tx *storage.PKITransaction) error {
				return tx.SavePKISecurityAcknowledgement(
					context.Background(), fixture.agentID, string(acknowledgement), fixture.now,
				)
			})
		}

		operation := invokeForceRotationForTest(t, fixture)
		if operation.State != storage.PKILifecycleJobStateSucceeded || operation.Phase != "completed" || operation.LastError != "" {
			t.Fatalf("ForceRotate(converged) operation = %+v", operation)
		}
		state := loadPKIEnrollmentState(t, fixture.store)
		identity := findForceRotationIdentity(t, state, fixture.identityID)
		if identity.CurrentCertificateID == nil || *identity.CurrentCertificateID == fixture.initialCertificateID {
			t.Fatalf("converged force rotation retained old certificate: %+v", identity)
		}
	})
}

type forceRotationServiceFixture struct {
	store                *storage.GormStore
	now                  time.Time
	agentID              string
	identityID           string
	initialCertificateID string
	enrollment           *PKIEnrollmentService
	tasks                *TaskService
	pki                  *InternalPKIService
	session              *forceRotationTaskSession
}

func newForceRotationServiceFixture(t *testing.T) forceRotationServiceFixture {
	t.Helper()
	fixture := newPKIEnrollmentFixture(t)
	operationNow := time.Now().UTC()
	agentID := "agent-a"
	identityID := "identity-force-agent-a"
	if err := fixture.store.WithPKITransaction(t.Context(), func(tx *storage.PKITransaction) error {
		return tx.CreatePKIIdentity(t.Context(), storage.PKIIdentityRow{
			ID: identityID, PKIDomainID: "domain-1", Kind: storage.PKIIdentityKindAgent,
			AgentID: agentID, State: storage.PKIIdentityStateEnrollmentRequired,
			CreatedAt: fixture.now, UpdatedAt: fixture.now,
		})
	}); err != nil {
		t.Fatal(err)
	}
	binding, err := newPKIIdentityBinding(
		"domain-1", storage.PKIIdentityKindAgent, agentID, "",
		storage.PKICertificatePurposeClient, nil, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	enrollment := newPKIEnrollmentServiceForTest(
		t, fixture, &pkiEnrollmentTestAuthoritySigner{key: fixture.authorityKey},
		sequencePKIID("force-initial-certificate", "force-initial-event", "force-next-certificate", "force-next-event"),
	)
	initial, err := enrollment.EnrollAuthenticated(t.Context(), agentID, "agent-a-token", PKIEnrollRequest{
		RequestID: "force-initial", Kind: storage.PKIIdentityKindAgent,
		Purpose: storage.PKICertificatePurposeClient,
		CSRPEM:  mustPKIEnrollmentCSR(t, mustPKIEnrollmentKey(t), binding, false),
	})
	if err != nil {
		t.Fatal(err)
	}
	tasks := NewTaskService(TaskServiceConfig{Now: func() time.Time { return operationNow }})
	t.Cleanup(func() { _ = tasks.Close() })
	session := &forceRotationTaskSession{}
	if err := tasks.RegisterSession(TaskSessionRegistration{AgentID: agentID, SessionID: "force-rotation", Session: session}); err != nil {
		t.Fatal(err)
	}
	pki := &InternalPKIService{
		store: fixture.store, lease: pkiStaticLeaseGate{}, enrollment: enrollment, tasks: tasks,
		clock: func() time.Time { return operationNow }, random: rand.Reader,
	}
	return forceRotationServiceFixture{
		store: fixture.store, now: operationNow, agentID: agentID, identityID: identityID,
		initialCertificateID: initial.CertificateID, enrollment: enrollment, tasks: tasks, pki: pki, session: session,
	}
}

func invokeForceRotationForTest(t *testing.T, fixture forceRotationServiceFixture) PKIOperation {
	t.Helper()
	confirmation, err := fixture.pki.IssueConfirmationNonce(t.Context(), PKIConfirmationRequest{
		Action: "force_rotate", TargetID: fixture.identityID,
	})
	if err != nil {
		t.Fatal(err)
	}
	operation, err := fixture.pki.ForceRotate(t.Context(), PKIActionRequest{
		TargetID: fixture.identityID, Reason: "test forced rotation", ConfirmationNonce: confirmation.Nonce,
	})
	if err != nil {
		t.Fatalf("ForceRotate() error = %v", err)
	}
	return operation
}

func findForceRotationIdentity(t *testing.T, state storage.PKICanonicalState, identityID string) storage.PKIIdentityRow {
	t.Helper()
	for _, identity := range state.Identities {
		if identity.ID == identityID {
			return identity
		}
	}
	t.Fatalf("identity %q not found", identityID)
	return storage.PKIIdentityRow{}
}

type forceRotationTaskSession struct {
	send func(TaskEnvelope) error
}

func (s *forceRotationTaskSession) SendTask(task TaskEnvelope) error {
	if s.send == nil {
		return nil
	}
	return s.send(task)
}

func (*forceRotationTaskSession) Close() error { return nil }
