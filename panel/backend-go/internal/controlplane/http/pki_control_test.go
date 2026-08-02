package http

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/service"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

func TestAgentRegistrationRejectsOversizedLegacyAndCSRPayloadsBeforeAuthentication(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "legacy", body: `{"name":"` + strings.Repeat("a", int(maxAgentRegistrationBodyBytes)) + `","register_token":"wrong"}`},
		{name: "csr", body: `{"name":"node","tunnel_csr_pem":"` + strings.Repeat("a", int(maxAgentRegistrationBodyBytes)) + `"}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state := &fakeAgentServiceState{}
			router, err := NewRouter(Dependencies{
				Config: config.Config{RegisterToken: "register-secret"}, SystemService: fakeSystemService{},
				AgentService: fakeAgentService{state: state}, RuleService: fakeRuleService{}, L4RuleService: fakeL4RuleService{},
				VersionPolicyService: fakeVersionPolicyService{}, RelayListenerService: fakeRelayListenerService{},
				CertificateService: fakeCertificateService{},
			})
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(http.MethodPost, "/panel-api/agents/register", strings.NewReader(test.body))
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != http.StatusRequestEntityTooLarge {
				t.Fatalf("oversized registration = %d, body=%s", response.Code, response.Body.String())
			}
			if state.register.Name != "" {
				t.Fatalf("oversized registration reached service: %+v", state.register)
			}
		})
	}
}

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
			heartbeatReply: service.HeartbeatReply{
				DesiredRevision: 12,
				PKISecurity:     &snapshot,
				PKIStatus: &service.PKIControlStatus{
					Status: "degraded", Code: "runtime_unavailable", RecoveryHint: "retry ordinary control sync",
				},
			},
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
	if !bytes.Contains(heartbeatResp.Body.Bytes(), []byte(`"pki_status":{"status":"degraded","code":"runtime_unavailable","recovery_hint":"retry ordinary control sync"}`)) {
		t.Fatalf("heartbeat response missing PKI status and recovery hint: %s", heartbeatResp.Body.String())
	}
}

func TestPKIInvalidSecurityAcknowledgementsAreClientErrors(t *testing.T) {
	base := Dependencies{
		Config: config.Config{PanelToken: "panel-secret"}, SystemService: fakeSystemService{},
		RuleService: fakeRuleService{}, L4RuleService: fakeL4RuleService{}, VersionPolicyService: fakeVersionPolicyService{},
		RelayListenerService: fakeRelayListenerService{}, CertificateService: fakeCertificateService{},
	}

	registrationDeps := base
	registrationDeps.AgentService = fakeAgentService{
		registerErr: fmt.Errorf("%w: PKI security acknowledgement domain/version is invalid", service.ErrInvalidArgument),
	}
	registrationRouter, err := NewRouter(registrationDeps)
	if err != nil {
		t.Fatal(err)
	}
	registration := httptest.NewRequest(http.MethodPost, "/panel-api/agents/register", bytes.NewBufferString(
		`{"name":"node-1","agent_token":"control-token","pki_security_ack":{"pki_domain_id":"wrong-domain","pki_epoch":1,"security_revision":0,"full":true}}`,
	))
	registration.Header.Set("Content-Type", "application/json")
	registrationResponse := httptest.NewRecorder()
	registrationRouter.ServeHTTP(registrationResponse, registration)
	if registrationResponse.Code != http.StatusBadRequest {
		t.Fatalf("invalid registration acknowledgement = %d, body=%s", registrationResponse.Code, registrationResponse.Body.String())
	}

	heartbeatDeps := base
	heartbeatDeps.AgentService = fakeAgentService{
		heartbeatErr: fmt.Errorf("%w: acknowledgement is ahead of canonical state", service.ErrPKIEpochStale),
	}
	heartbeatRouter, err := NewRouter(heartbeatDeps)
	if err != nil {
		t.Fatal(err)
	}
	heartbeat := httptest.NewRequest(http.MethodPost, "/panel-api/agents/heartbeat", bytes.NewBufferString(
		`{"pki_security_ack":{"pki_domain_id":"domain-1","pki_epoch":99,"security_revision":0,"full":true}}`,
	))
	heartbeat.Header.Set("Content-Type", "application/json")
	heartbeat.Header.Set("X-Agent-Token", "control-token")
	heartbeatResponse := httptest.NewRecorder()
	heartbeatRouter.ServeHTTP(heartbeatResponse, heartbeat)
	if heartbeatResponse.Code != http.StatusConflict || !bytes.Contains(heartbeatResponse.Body.Bytes(), []byte(`"code":"pki_security_version_conflict"`)) {
		t.Fatalf("ahead heartbeat acknowledgement = %d, body=%s", heartbeatResponse.Code, heartbeatResponse.Body.String())
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
	confirmationReq := httptest.NewRequest(http.MethodPost, "/panel-api/pki/confirmations", bytes.NewBufferString(`{"action":"revoke","target_id":"identity-1"}`))
	confirmationReq.Header.Set("X-Panel-Token", "panel-secret")
	confirmationResp := httptest.NewRecorder()
	router.ServeHTTP(confirmationResp, confirmationReq)
	if confirmationResp.Code != http.StatusCreated || !bytes.Contains(confirmationResp.Body.Bytes(), []byte(`"nonce":"server-issued-nonce"`)) {
		t.Fatalf("confirmation = %d, body=%s", confirmationResp.Code, confirmationResp.Body.String())
	}
	if got := confirmationResp.Header().Get("Cache-Control"); got != "no-store, private" {
		t.Fatalf("confirmation Cache-Control = %q", got)
	}

	revoke := httptest.NewRequest(http.MethodPost, "/panel-api/pki/identities/identity-1/revoke", bytes.NewBufferString(`{"reason":"compromised","confirmation_nonce":"server-issued-nonce"}`))
	revoke.Header.Set("X-Panel-Token", "panel-secret")
	revokeResp := httptest.NewRecorder()
	router.ServeHTTP(revokeResp, revoke)
	if revokeResp.Code != http.StatusAccepted {
		t.Fatalf("revoke = %d, body=%s", revokeResp.Code, revokeResp.Body.String())
	}
	if pki.lastAction.TargetID != "identity-1" || pki.lastAction.Reason != "compromised" || pki.lastAction.ConfirmationNonce != "server-issued-nonce" {
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
	if got := overviewResp.Header().Get("Cache-Control"); got != "no-store, private" {
		t.Fatalf("overview Cache-Control = %q", got)
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

func TestPKIMissingOperationReturnsNotFound(t *testing.T) {
	pki := &fakePKIAPIService{operationErr: service.ErrPKIOperationNotFound}
	router, err := NewRouter(Dependencies{
		Config: config.Config{PanelToken: "panel-secret"}, PKIService: pki,
		SystemService: fakeSystemService{}, AgentService: fakeAgentService{}, RuleService: fakeRuleService{}, L4RuleService: fakeL4RuleService{},
		VersionPolicyService: fakeVersionPolicyService{}, RelayListenerService: fakeRelayListenerService{}, CertificateService: fakeCertificateService{},
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/pki/operations/missing", nil)
	request.Header.Set("X-Panel-Token", "panel-secret")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("missing PKI operation = %d, body=%s", response.Code, response.Body.String())
	}
}

func TestPKIProtectedImportUsesBoundedMultipartArchive(t *testing.T) {
	pki := &fakePKIAPIService{}
	router, err := NewRouter(Dependencies{
		Config: config.Config{PanelToken: "panel-secret"}, PKIService: pki,
		SystemService: fakeSystemService{}, AgentService: fakeAgentService{}, RuleService: fakeRuleService{}, L4RuleService: fakeL4RuleService{},
		VersionPolicyService: fakeVersionPolicyService{}, RelayListenerService: fakeRelayListenerService{}, CertificateService: fakeCertificateService{},
	})
	if err != nil {
		t.Fatal(err)
	}

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("passphrase", "correct horse battery staple"); err != nil {
		t.Fatal(err)
	}
	if err := writer.WriteField("reason", "restore after loss"); err != nil {
		t.Fatal(err)
	}
	if err := writer.WriteField("force", "true"); err != nil {
		t.Fatal(err)
	}
	part, err := writer.CreateFormFile("archive", "pki-backup.bin")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte("protected-archive")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/panel-api/pki/backups/import", &body)
	request.Header.Set("X-Panel-Token", "panel-secret")
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted || pki.importCalls != 1 {
		t.Fatalf("protected import = %d calls=%d body=%s", response.Code, pki.importCalls, response.Body.String())
	}
	if string(pki.lastAction.Archive) != "protected-archive" || pki.lastAction.Passphrase != "correct horse battery staple" ||
		pki.lastAction.Reason != "restore after loss" || !pki.lastAction.Force {
		t.Fatalf("protected import action = %+v", pki.lastAction)
	}
	if got := response.Header().Get("Cache-Control"); got != "no-store, private" {
		t.Fatalf("protected import Cache-Control = %q", got)
	}

	var oversized bytes.Buffer
	oversizedWriter := multipart.NewWriter(&oversized)
	oversizedPart, err := oversizedWriter.CreateFormFile("archive", "oversized.bin")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := oversizedPart.Write(bytes.Repeat([]byte("x"), int(pkiProtectedImportMaxBytes+1))); err != nil {
		t.Fatal(err)
	}
	if err := oversizedWriter.Close(); err != nil {
		t.Fatal(err)
	}
	oversizedRequest := httptest.NewRequest(http.MethodPost, "/panel-api/pki/backups/import", &oversized)
	oversizedRequest.Header.Set("X-Panel-Token", "panel-secret")
	oversizedRequest.Header.Set("Content-Type", oversizedWriter.FormDataContentType())
	oversizedResponse := httptest.NewRecorder()
	router.ServeHTTP(oversizedResponse, oversizedRequest)
	if oversizedResponse.Code != http.StatusRequestEntityTooLarge || pki.importCalls != 1 {
		t.Fatalf("oversized protected import = %d calls=%d body=%s", oversizedResponse.Code, pki.importCalls, oversizedResponse.Body.String())
	}
}

func TestPKIEventQueryParsesGenerationAndTimeBounds(t *testing.T) {
	pki := &fakePKIAPIService{events: []service.PKIAuditEvent{{
		ID: "event-1", Type: "identity_revoked", OccurredAt: time.Date(2026, 8, 1, 8, 30, 0, 0, time.UTC),
		Source: "panel", ObjectType: "identity", ObjectID: "identity-1", CAGeneration: 2,
		Result: "success", SecurityRevision: 3, Details: map[string]any{"reason_code": "operator_request"},
	}}}
	router, err := NewRouter(Dependencies{
		Config: config.Config{PanelToken: "panel-secret"}, PKIService: pki,
		SystemService: fakeSystemService{}, AgentService: fakeAgentService{}, RuleService: fakeRuleService{}, L4RuleService: fakeL4RuleService{},
		VersionPolicyService: fakeVersionPolicyService{}, RelayListenerService: fakeRelayListenerService{}, CertificateService: fakeCertificateService{},
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet,
		"/panel-api/pki/events?ca_generation=2&from=2026-08-01T08%3A00%3A00Z&to=2026-08-01T09%3A00%3A00Z", nil)
	request.Header.Set("X-Panel-Token", "panel-secret")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK || pki.lastEventQuery.CAGeneration == nil || *pki.lastEventQuery.CAGeneration != 2 ||
		pki.lastEventQuery.From == nil || pki.lastEventQuery.To == nil || pki.lastEventQuery.From.After(*pki.lastEventQuery.To) {
		t.Fatalf("event query response=%d query=%+v body=%s", response.Code, pki.lastEventQuery, response.Body.String())
	}
	if !bytes.Contains(response.Body.Bytes(), []byte(`"occurred_at":"2026-08-01T08:30:00Z"`)) ||
		!bytes.Contains(response.Body.Bytes(), []byte(`"ca_generation":2`)) ||
		!bytes.Contains(response.Body.Bytes(), []byte(`"details":{"reason_code":"operator_request"}`)) ||
		bytes.Contains(response.Body.Bytes(), []byte(`"OccurredAt"`)) || bytes.Contains(response.Body.Bytes(), []byte(`"raw_json"`)) {
		t.Fatalf("event response schema = %s", response.Body.String())
	}

	for _, rawQuery := range []string{
		"ca_generation=zero",
		"from=not-a-time",
		"from=2026-08-01T10%3A00%3A00Z&to=2026-08-01T09%3A00%3A00Z",
	} {
		invalid := httptest.NewRequest(http.MethodGet, "/panel-api/pki/events?"+rawQuery, nil)
		invalid.Header.Set("X-Panel-Token", "panel-secret")
		invalidResponse := httptest.NewRecorder()
		router.ServeHTTP(invalidResponse, invalid)
		if invalidResponse.Code != http.StatusBadRequest {
			t.Fatalf("invalid event query %q = %d, body=%s", rawQuery, invalidResponse.Code, invalidResponse.Body.String())
		}
	}
}

type fakePKIAPIService struct {
	lastAction     service.PKIActionRequest
	lastToken      service.PKIEnrollmentTokenRequest
	lastEventQuery service.PKIEventQuery
	events         []service.PKIAuditEvent
	importCalls    int
	operationErr   error
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

func (f *fakePKIAPIService) Events(_ context.Context, query service.PKIEventQuery) ([]service.PKIAuditEvent, error) {
	f.lastEventQuery = query
	return f.events, nil
}

func (f *fakePKIAPIService) Alerts(context.Context) ([]service.PKIDerivedAlert, error) {
	return []service.PKIDerivedAlert{}, nil
}

func (f *fakePKIAPIService) CreateEnrollmentToken(_ context.Context, request service.PKIEnrollmentTokenRequest) (service.PKIEnrollmentToken, error) {
	f.lastToken = request
	return service.PKIEnrollmentToken{Token: "one-time-secret", Scope: request.Scope, BoundAgentID: request.BoundAgentID}, nil
}

func (f *fakePKIAPIService) IssueConfirmationNonce(_ context.Context, request service.PKIConfirmationRequest) (service.PKIConfirmation, error) {
	return service.PKIConfirmation{
		Nonce: "server-issued-nonce", Action: request.Action, TargetID: request.TargetID,
		ExpiresAt: time.Now().UTC().Add(time.Minute),
	}, nil
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
	request.Archive = bytes.Clone(request.Archive)
	f.lastAction = request
	f.importCalls++
	return pkiTestOperation("protected_import", "backup"), nil
}

func (f *fakePKIAPIService) Activate(_ context.Context, request service.PKIActionRequest) (service.PKIOperation, error) {
	f.lastAction = request
	return pkiTestOperation("activate", "pki"), nil
}

func (f *fakePKIAPIService) Operation(context.Context, string) (service.PKIOperation, error) {
	if f.operationErr != nil {
		return service.PKIOperation{}, f.operationErr
	}
	return pkiTestOperation("revoke", "identity-1"), nil
}

func (f *fakePKIAPIService) SecuritySnapshot(context.Context, string, *storage.PKISecurityAcknowledgement) (storage.PKISecuritySnapshot, error) {
	return storage.PKISecuritySnapshot{PKIDomainID: "domain-1", PKIEpoch: 1, SecurityRevision: 2}, nil
}

func pkiTestOperation(kind, target string) service.PKIOperation {
	now := time.Date(2026, 8, 1, 8, 0, 0, 0, time.UTC)
	return service.PKIOperation{ID: "pki-op-1", Kind: kind, TargetType: "identity", TargetID: target, State: "accepted", CreatedAt: now, UpdatedAt: now}
}
