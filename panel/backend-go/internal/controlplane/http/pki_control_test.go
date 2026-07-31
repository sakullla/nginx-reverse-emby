package http

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/service"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

func TestPKIRegistrationAndHeartbeatUseExistingTokenControlRoutes(t *testing.T) {
	now := time.Date(2026, 8, 1, 8, 0, 0, 0, time.UTC)
	snapshot := storage.PKISecuritySnapshot{
		PKIDomainID: "domain-1", PKIEpoch: 2, SecurityRevision: 7, Full: true, IssuedAt: now,
		TrustRoots: []storage.PKITrustRoot{{
			AuthorityID: "ca-2", Generation: 2, Status: "active", CertificatePEM: "PUBLIC CA", FingerprintSHA256: "abc", NotBefore: now, NotAfter: now.Add(time.Hour),
		}},
		RevokedIdentityIDs: []string{}, RevokedSerials: []string{}, SignerGeneration: 2, Signature: []byte("signed"),
	}
	state := &fakeAgentServiceState{}
	router, err := NewRouter(Dependencies{
		Config:        config.Config{PanelToken: "panel-secret", RegisterToken: "register-secret"},
		SystemService: fakeSystemService{},
		AgentService: fakeAgentService{
			state: state,
			agents: []service.AgentSummary{{
				ID: "agent-1",
				PKIRegistration: &service.PKIRegistrationReply{
					AgentID: "agent-1", AgentToken: "control-token",
					TunnelCredential: storage.PKITunnelCredential{IdentityID: "identity-1", CertificateID: "leaf-1", CertificatePEM: "PUBLIC LEAF"},
					SecuritySnapshot: snapshot,
				},
			}},
			heartbeatReply: service.HeartbeatReply{DesiredRevision: 12, PKISecurity: &snapshot},
		},
		RuleService: fakeRuleService{}, L4RuleService: fakeL4RuleService{}, VersionPolicyService: fakeVersionPolicyService{},
		RelayListenerService: fakeRelayListenerService{}, CertificateService: fakeCertificateService{},
	})
	if err != nil {
		t.Fatalf("NewRouter() error = %v", err)
	}

	registerBody := bytes.NewBufferString(`{"name":"node-1","agent_token":"control-token","register_token":"one-time-pki-token","tunnel_csr_pem":"PUBLIC CSR","pki_security_ack":{"pki_domain_id":"domain-1","pki_epoch":1,"security_revision":4,"full":true}}`)
	registerReq := httptest.NewRequest(http.MethodPost, "/panel-api/agents/register", registerBody)
	registerReq.Header.Set("Content-Type", "application/json")
	registerReq.Header.Set("X-Agent-Token", "control-token")
	registerResp := httptest.NewRecorder()
	router.ServeHTTP(registerResp, registerReq)
	if registerResp.Code != http.StatusOK {
		t.Fatalf("POST registration = %d, body=%s", registerResp.Code, registerResp.Body.String())
	}
	if state.register.TunnelCSRPEM != "PUBLIC CSR" || state.register.PKISecurityAck == nil || state.register.PKISecurityAck.SecurityRevision != 4 {
		t.Fatalf("registration PKI DTO was not forwarded: %+v", state.register)
	}
	if state.register.RegisterToken != "one-time-pki-token" {
		t.Fatalf("registration one-time PKI token was not forwarded: %+v", state.register)
	}
	if state.registerHeaderToken != "control-token" {
		t.Fatalf("registration X-Agent-Token = %q", state.registerHeaderToken)
	}
	var registration map[string]any
	if err := json.Unmarshal(registerResp.Body.Bytes(), &registration); err != nil {
		t.Fatalf("decode registration response: %v", err)
	}
	if registration["pki"] == nil {
		t.Fatalf("registration response missing PKI result: %s", registerResp.Body.String())
	}
	if bytes.Contains(registerResp.Body.Bytes(), []byte("PRIVATE KEY")) {
		t.Fatalf("registration response leaked a private key: %s", registerResp.Body.String())
	}

	heartbeatBody := bytes.NewBufferString(`{"current_revision":1,"pki_security_ack":{"pki_domain_id":"domain-1","pki_epoch":2,"security_revision":7,"full":true,"trust_generations":[2]}}`)
	heartbeatReq := httptest.NewRequest(http.MethodPost, "/panel-api/agents/heartbeat", heartbeatBody)
	heartbeatReq.Header.Set("Content-Type", "application/json")
	heartbeatReq.Header.Set("X-Agent-Token", "control-token")
	heartbeatResp := httptest.NewRecorder()
	router.ServeHTTP(heartbeatResp, heartbeatReq)
	if heartbeatResp.Code != http.StatusOK {
		t.Fatalf("POST heartbeat = %d, body=%s", heartbeatResp.Code, heartbeatResp.Body.String())
	}
	if state.heartbeatToken != "control-token" || state.heartbeat.PKISecurityAck == nil || state.heartbeat.PKISecurityAck.SecurityRevision != 7 {
		t.Fatalf("heartbeat did not retain token auth and PKI ack: token=%q request=%+v", state.heartbeatToken, state.heartbeat)
	}
	if !bytes.Contains(heartbeatResp.Body.Bytes(), []byte(`"pki_security"`)) {
		t.Fatalf("heartbeat response missing PKI security snapshot: %s", heartbeatResp.Body.String())
	}
}

func TestPKIOperationEnvelopeUsesExistingPanelAPI(t *testing.T) {
	pki := &fakePKIAPIService{}
	router, err := NewRouter(Dependencies{
		Config: config.Config{PanelToken: "panel-secret"}, PKIService: pki,
		SystemService: fakeSystemService{}, AgentService: fakeAgentService{}, RuleService: fakeRuleService{}, L4RuleService: fakeL4RuleService{},
		VersionPolicyService: fakeVersionPolicyService{}, RelayListenerService: fakeRelayListenerService{}, CertificateService: fakeCertificateService{},
	})
	if err != nil {
		t.Fatalf("NewRouter() error = %v", err)
	}

	unauthorized := httptest.NewRequest(http.MethodPost, "/panel-api/pki/identities/identity-1/revoke", bytes.NewBufferString(`{"reason":"compromised","confirmation_nonce":"nonce"}`))
	unauthorizedResp := httptest.NewRecorder()
	router.ServeHTTP(unauthorizedResp, unauthorized)
	if unauthorizedResp.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized revoke = %d", unauthorizedResp.Code)
	}

	revoke := httptest.NewRequest(http.MethodPost, "/panel-api/pki/identities/identity-1/revoke", bytes.NewBufferString(`{"reason":"compromised","confirmation_nonce":"nonce"}`))
	revoke.Header.Set("X-Panel-Token", "panel-secret")
	revokeResp := httptest.NewRecorder()
	router.ServeHTTP(revokeResp, revoke)
	if revokeResp.Code != http.StatusAccepted {
		t.Fatalf("revoke = %d, body=%s", revokeResp.Code, revokeResp.Body.String())
	}
	if pki.lastAction.TargetID != "identity-1" || pki.lastAction.Reason != "compromised" || pki.lastAction.ConfirmationNonce != "nonce" {
		t.Fatalf("revoke action = %+v", pki.lastAction)
	}
	var envelope map[string]any
	if err := json.Unmarshal(revokeResp.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode operation envelope: %v", err)
	}
	if envelope["operation_id"] != "pki-op-1" || envelope["status_url"] != "/panel-api/pki/operations/pki-op-1" {
		t.Fatalf("operation envelope = %+v", envelope)
	}

	overview := httptest.NewRequest(http.MethodGet, "/api/pki/overview", nil)
	overview.Header.Set("X-Panel-Token", "panel-secret")
	overviewResp := httptest.NewRecorder()
	router.ServeHTTP(overviewResp, overview)
	if overviewResp.Code != http.StatusOK || !bytes.Contains(overviewResp.Body.Bytes(), []byte(`"pki_domain_id":"domain-1"`)) {
		t.Fatalf("overview = %d, body=%s", overviewResp.Code, overviewResp.Body.String())
	}

	token := httptest.NewRequest(http.MethodPost, "/api/pki/enrollment-tokens", bytes.NewBufferString(`{"scope":"bound_reenrollment","bound_agent_id":"agent-1"}`))
	token.Header.Set("X-Panel-Token", "panel-secret")
	tokenResp := httptest.NewRecorder()
	router.ServeHTTP(tokenResp, token)
	if tokenResp.Code != http.StatusCreated || !bytes.Contains(tokenResp.Body.Bytes(), []byte(`"token":"one-time-secret"`)) {
		t.Fatalf("enrollment token = %d, body=%s", tokenResp.Code, tokenResp.Body.String())
	}
	if pki.lastToken.CreatedBy != "panel" {
		t.Fatalf("enrollment token creator = %q", pki.lastToken.CreatedBy)
	}
}

type fakePKIAPIService struct {
	lastAction service.PKIActionRequest
	lastToken  service.PKIEnrollmentTokenRequest
}

func (f *fakePKIAPIService) Overview(context.Context) (service.PKIOverview, error) {
	return service.PKIOverview{PKIDomainID: "domain-1", PKIEpoch: 1, SecurityRevision: 2}, nil
}

func (f *fakePKIAPIService) Authorities(context.Context) ([]service.PKIAuthorityView, error) {
	return []service.PKIAuthorityView{}, nil
}

func (f *fakePKIAPIService) Identities(context.Context) ([]service.PKIIdentityView, error) {
	return []service.PKIIdentityView{}, nil
}

func (f *fakePKIAPIService) Certificates(context.Context) ([]service.PKICertificateView, error) {
	return []service.PKICertificateView{}, nil
}

func (f *fakePKIAPIService) Events(context.Context, service.PKIEventQuery) ([]service.PKIAuditEvent, error) {
	return []service.PKIAuditEvent{}, nil
}

func (f *fakePKIAPIService) Alerts(context.Context) ([]service.PKIDerivedAlert, error) {
	return []service.PKIDerivedAlert{}, nil
}

func (f *fakePKIAPIService) CreateEnrollmentToken(_ context.Context, request service.PKIEnrollmentTokenRequest) (service.PKIEnrollmentToken, error) {
	f.lastToken = request
	return service.PKIEnrollmentToken{Token: "one-time-secret", Scope: request.Scope, BoundAgentID: request.BoundAgentID}, nil
}

func (f *fakePKIAPIService) Revoke(_ context.Context, request service.PKIActionRequest) (service.PKIOperation, error) {
	f.lastAction = request
	return pkiTestOperation("revoke", request.TargetID), nil
}

func (f *fakePKIAPIService) ForceRotate(_ context.Context, request service.PKIActionRequest) (service.PKIOperation, error) {
	f.lastAction = request
	return pkiTestOperation("force_rotate", request.TargetID), nil
}

func (f *fakePKIAPIService) RotateCA(_ context.Context, request service.PKIActionRequest) (service.PKIOperation, error) {
	f.lastAction = request
	return pkiTestOperation("ca_rotate", "authority"), nil
}

func (f *fakePKIAPIService) EmergencyRotateCA(_ context.Context, request service.PKIActionRequest) (service.PKIOperation, error) {
	f.lastAction = request
	return pkiTestOperation("emergency_ca_rotate", "authority"), nil
}

func (f *fakePKIAPIService) ExportProtected(_ context.Context, request service.PKIActionRequest) (service.PKIOperation, error) {
	f.lastAction = request
	return pkiTestOperation("protected_export", "backup"), nil
}

func (f *fakePKIAPIService) ImportProtected(_ context.Context, request service.PKIActionRequest) (service.PKIOperation, error) {
	f.lastAction = request
	return pkiTestOperation("protected_import", "backup"), nil
}

func (f *fakePKIAPIService) Activate(_ context.Context, request service.PKIActionRequest) (service.PKIOperation, error) {
	f.lastAction = request
	return pkiTestOperation("activate", "pki"), nil
}

func (f *fakePKIAPIService) Operation(context.Context, string) (service.PKIOperation, error) {
	return pkiTestOperation("revoke", "identity-1"), nil
}

func (f *fakePKIAPIService) SecuritySnapshot(context.Context, string, *storage.PKISecurityAcknowledgement) (storage.PKISecuritySnapshot, error) {
	return storage.PKISecuritySnapshot{PKIDomainID: "domain-1", PKIEpoch: 1, SecurityRevision: 2}, nil
}

func pkiTestOperation(kind, target string) service.PKIOperation {
	now := time.Date(2026, 8, 1, 8, 0, 0, 0, time.UTC)
	return service.PKIOperation{ID: "pki-op-1", Kind: kind, TargetType: "identity", TargetID: target, State: "accepted", CreatedAt: now, UpdatedAt: now}
}
