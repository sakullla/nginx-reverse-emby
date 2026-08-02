package control

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

type recordingPKIHeartbeatHandler struct {
	prepared      PKIHeartbeatState
	reply         PKIHeartbeatReply
	applyErr      error
	preparedCount int
	appliedCount  int
}

func (h *recordingPKIHeartbeatHandler) PrepareHeartbeat(context.Context) (PKIHeartbeatState, error) {
	h.preparedCount++
	return h.prepared, nil
}

func (h *recordingPKIHeartbeatHandler) ApplyHeartbeat(_ context.Context, reply PKIHeartbeatReply) error {
	h.appliedCount++
	h.reply = reply
	return h.applyErr
}

func TestHeartbeatProcessesPKIEnvelopeWithoutOrdinaryRevisionUpdate(t *testing.T) {
	handler := &recordingPKIHeartbeatHandler{prepared: PKIHeartbeatState{
		SecurityAcknowledgement: &model.PKISecurityAcknowledgement{
			PKIDomainID: "domain-1", PKIEpoch: 2, SecurityRevision: 7, Full: true,
		},
		EnrollmentRequests: []model.PKIEnrollmentRequest{{
			RequestID: "request-1", Kind: model.PKIIdentityKindAgent,
			Purpose: model.PKICertificatePurposeClient, CSRPEM: "PUBLIC CSR",
		}},
	}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Agent-Token"); got != "control-token" {
			t.Fatalf("X-Agent-Token = %q, want control-token", got)
		}
		var payload struct {
			SecurityAcknowledgement *model.PKISecurityAcknowledgement `json:"pki_security_ack"`
			EnrollmentRequests      []model.PKIEnrollmentRequest      `json:"pki_enrollment_requests"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode heartbeat: %v", err)
		}
		if !reflect.DeepEqual(payload.SecurityAcknowledgement, handler.prepared.SecurityAcknowledgement) {
			t.Fatalf("security acknowledgement = %+v", payload.SecurityAcknowledgement)
		}
		if !reflect.DeepEqual(payload.EnrollmentRequests, handler.prepared.EnrollmentRequests) {
			t.Fatalf("enrollment requests = %+v", payload.EnrollmentRequests)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"sync":{"has_update":false,"desired_revision":7,"pki_security":{"pki_domain_id":"domain-1","pki_epoch":2,"security_revision":8,"full":true},"pki_credentials":[{"request_id":"request-1","credential":{"certificate_id":"certificate-1"}}],"pki_status":{"status":"ready"}}}`)
	}))
	defer server.Close()

	client := NewSyncClient(SyncClientConfig{
		MasterURL: server.URL, AgentToken: "control-token", AgentID: "agent-1",
		PKIHeartbeatHandler: handler,
	}, server.Client())
	snapshot, err := client.Sync(t.Context(), SyncRequest{CurrentRevision: 7})
	if err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	if snapshot.Revision != 7 {
		t.Fatalf("ordinary snapshot revision = %d, want 7", snapshot.Revision)
	}
	if handler.preparedCount != 1 || handler.appliedCount != 1 {
		t.Fatalf("handler calls = prepare %d, apply %d", handler.preparedCount, handler.appliedCount)
	}
	if handler.reply.Security == nil || handler.reply.Security.SecurityRevision != 8 {
		t.Fatalf("PKI security reply = %+v", handler.reply.Security)
	}
	if len(handler.reply.Credentials) != 1 || handler.reply.Credentials[0].RequestID != "request-1" {
		t.Fatalf("PKI credentials reply = %+v", handler.reply.Credentials)
	}
	if handler.reply.Status == nil || handler.reply.Status.Status != "ready" {
		t.Fatalf("PKI status reply = %+v", handler.reply.Status)
	}
}

func TestHeartbeatDoesNotReturnOrdinarySnapshotWhenPKIApplyFails(t *testing.T) {
	handler := &recordingPKIHeartbeatHandler{applyErr: errors.New("credential cutover failed")}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"sync":{"has_update":true,"desired_revision":8,"pki_status":{"status":"ready"}}}`)
	}))
	defer server.Close()

	client := NewSyncClient(SyncClientConfig{
		MasterURL: server.URL, AgentToken: "control-token", AgentID: "agent-1",
		PKIHeartbeatHandler: handler,
	}, server.Client())
	if _, err := client.Sync(t.Context(), SyncRequest{CurrentRevision: 7}); err == nil {
		t.Fatal("Sync() succeeded despite failed PKI cutover")
	}
}

func TestHeartbeatRejectsPKIEnvelopeWithoutExecutionPlaneHandler(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"sync":{"desired_revision":7,"pki_status":{"status":"ready"}}}`)
	}))
	defer server.Close()

	client := NewSyncClient(SyncClientConfig{
		MasterURL: server.URL, AgentToken: "control-token", AgentID: "agent-1",
	}, server.Client())
	if _, err := client.Sync(t.Context(), SyncRequest{CurrentRevision: 7}); err == nil {
		t.Fatal("Sync() accepted PKI state without an execution-plane handler")
	}
}

func TestPKIHeartbeatKeepsTokenOnlyControlTLS(t *testing.T) {
	handler := &recordingPKIHeartbeatHandler{}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/agents/heartbeat" {
			t.Fatalf("heartbeat path = %q", r.URL.Path)
		}
		if got := r.Header.Get("X-Agent-Token"); got != "control-token" {
			t.Fatalf("X-Agent-Token = %q, want control-token", got)
		}
		if r.TLS == nil {
			t.Fatal("heartbeat did not use TLS")
		}
		if len(r.TLS.PeerCertificates) != 0 {
			t.Fatalf("control heartbeat unexpectedly sent %d client certificate(s)", len(r.TLS.PeerCertificates))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"sync":{"has_update":false,"desired_revision":7}}`)
	}))
	server.TLS = &tls.Config{ClientAuth: tls.RequestClientCert, MinVersion: tls.VersionTLS12}
	server.StartTLS()
	defer server.Close()

	client := NewSyncClient(SyncClientConfig{
		MasterURL: server.URL, AgentToken: "control-token", AgentID: "agent-1",
		PKIHeartbeatHandler: handler,
	}, nil)
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	client.transport.TLSClientConfig = &tls.Config{
		MinVersion: tls.VersionTLS12,
		RootCAs:    roots,
	}
	if _, err := client.Sync(t.Context(), SyncRequest{CurrentRevision: 7}); err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
}
