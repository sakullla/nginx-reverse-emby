//go:build !integration

package service

import (
	"context"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

type pkiTaskSessionProbe struct{ closed bool }

func (*pkiTaskSessionProbe) SendTask(TaskEnvelope) error { return nil }
func (session *pkiTaskSessionProbe) Close() error {
	session.closed = true
	return nil
}

func TestPKITaskSessionCloserIgnoresRelayOnlyTargets(t *testing.T) {
	tasks := NewTaskService(TaskServiceConfig{})
	t.Cleanup(func() { _ = tasks.Close() })
	session := &pkiTaskSessionProbe{}
	if err := tasks.RegisterSession(TaskSessionRegistration{AgentID: "edge-a", Session: session}); err != nil {
		t.Fatal(err)
	}
	closer, err := NewPKITaskSessionCloser(tasks)
	if err != nil {
		t.Fatal(err)
	}
	if err := closer.CloseRevokedPKISessions(t.Context(), PKIRevocationCommit{RelaySessionTargets: []string{"edge-a"}}); err != nil {
		t.Fatal(err)
	}
	if !tasks.HasSession("edge-a") || session.closed {
		t.Fatal("relay-only revocation fenced the Agent control task session")
	}
	replacement := &pkiTaskSessionProbe{}
	if err := tasks.RegisterSession(TaskSessionRegistration{AgentID: "edge-a", Session: replacement}); err != nil {
		t.Fatalf("relay-only revocation rejected replacement control session: %v", err)
	}
	if !tasks.HasSession("edge-a") || replacement.closed {
		t.Fatal("replacement control task session was not retained")
	}
}

func TestPKITaskSessionCloserFencesControlTargets(t *testing.T) {
	tasks := NewTaskService(TaskServiceConfig{})
	t.Cleanup(func() { _ = tasks.Close() })
	session := &pkiTaskSessionProbe{}
	if err := tasks.RegisterSession(TaskSessionRegistration{AgentID: "edge-a", Session: session}); err != nil {
		t.Fatal(err)
	}
	closer, err := NewPKITaskSessionCloser(tasks)
	if err != nil {
		t.Fatal(err)
	}
	if err := closer.CloseRevokedPKISessions(context.Background(), PKIRevocationCommit{ControlSessionTargets: []string{"edge-a"}}); err != nil {
		t.Fatal(err)
	}
	if tasks.HasSession("edge-a") || !session.closed {
		t.Fatal("control revocation did not close and fence the task session")
	}
	rejected := &pkiTaskSessionProbe{}
	if err := tasks.RegisterSession(TaskSessionRegistration{AgentID: "edge-a", Session: rejected}); err == nil {
		t.Fatal("control revocation allowed a replacement task session")
	}
	if !rejected.closed {
		t.Fatal("rejected replacement task session was not closed")
	}
}

func TestRecoveredPKIRevocationTargetsPreserveValidControlSessions(t *testing.T) {
	listener := storage.PKIIdentityRow{Kind: storage.PKIIdentityKindListener, AgentID: "edge-a"}
	control, relay := recoveredPKIRevocationSessionTargets(listener)
	if len(control) != 0 || len(relay) != 1 || relay[0] != "edge-a" {
		t.Fatalf("listener recovery targets = control %v relay %v", control, relay)
	}
	agent := storage.PKIIdentityRow{Kind: storage.PKIIdentityKindAgent, AgentID: "edge-a"}
	control, relay = recoveredPKIRevocationSessionTargets(agent)
	if len(control) != 1 || control[0] != "edge-a" || len(relay) != 1 || relay[0] != "edge-a" {
		t.Fatalf("agent recovery targets = control %v relay %v", control, relay)
	}
}
