//go:build !integration

package http

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/service"
)

type fakeSystemService struct {
	info service.SystemInfo
}

func (f fakeSystemService) Info(context.Context) service.SystemInfo {
	return f.info
}

type fakeAgentService struct {
	agents           []service.AgentSummary
	agentsByID       map[string]service.AgentSummary
	agentsByToken    map[string]service.AgentSummary
	heartbeatReply   service.HeartbeatReply
	registerErr      error
	heartbeatErr     error
	updateAgent      service.AgentSummary
	deleteAgent      service.AgentSummary
	applyResult      service.ApplyAgentResult
	statsByID        map[string]service.AgentStats
	getErr           error
	updateErr        error
	deleteErr        error
	statsErr         error
	applyErr         error
	monitorSnapshot  service.AgentMonitorSnapshot
	monitorSnapshots []service.AgentMonitorSnapshot
	monitorErr       error
	monitorUpdates   chan service.AgentMonitorUpdate
	state            *fakeAgentServiceState
}

type fakeAgentServiceState struct {
	register             service.RegisterRequest
	registerHeaderToken  string
	updateAgentID        string
	updateInput          service.UpdateAgentRequest
	deleteAgentID        string
	statsAgentID         string
	applyAgentID         string
	heartbeat            service.HeartbeatRequest
	heartbeatToken       string
	resolveTokens        []string
	monitorSnapshotCalls int
	monitorSubscribed    bool
	monitorUnsubscribed  bool
}

func float64Ptr(value float64) *float64 {
	return &value
}

func uint64Ptr(value uint64) *uint64 {
	return &value
}

func (f fakeAgentService) List(context.Context) ([]service.AgentSummary, error) {
	return f.agents, nil
}

func (f fakeAgentService) Register(_ context.Context, request service.RegisterRequest, headerToken string) (service.AgentSummary, error) {
	if f.state != nil {
		f.state.register = request
		f.state.registerHeaderToken = headerToken
	}
	if f.registerErr != nil {
		return service.AgentSummary{}, f.registerErr
	}
	if len(f.agents) == 0 {
		return service.AgentSummary{}, service.ErrAgentNotFound
	}
	return f.agents[0], nil
}

func (f fakeAgentService) Heartbeat(_ context.Context, req service.HeartbeatRequest, token string) (service.HeartbeatReply, error) {
	if f.state != nil {
		f.state.heartbeat = req
		f.state.heartbeatToken = token
	}
	if f.heartbeatErr != nil {
		return service.HeartbeatReply{}, f.heartbeatErr
	}
	return f.heartbeatReply, nil
}

func (f fakeAgentService) Get(_ context.Context, agentID string) (service.AgentSummary, error) {
	if f.getErr != nil {
		return service.AgentSummary{}, f.getErr
	}
	if agent, ok := f.agentsByID[agentID]; ok {
		return agent, nil
	}
	return service.AgentSummary{}, service.ErrAgentNotFound
}

func (f fakeAgentService) Update(_ context.Context, agentID string, input service.UpdateAgentRequest) (service.AgentSummary, error) {
	if f.state != nil {
		f.state.updateAgentID = agentID
		f.state.updateInput = input
	}
	if f.updateErr != nil {
		return service.AgentSummary{}, f.updateErr
	}
	return f.updateAgent, nil
}

func (f fakeAgentService) Delete(_ context.Context, agentID string) (service.AgentSummary, error) {
	if f.state != nil {
		f.state.deleteAgentID = agentID
	}
	if f.deleteErr != nil {
		return service.AgentSummary{}, f.deleteErr
	}
	return f.deleteAgent, nil
}

func (f fakeAgentService) Stats(_ context.Context, agentID string) (service.AgentStats, error) {
	if f.state != nil {
		f.state.statsAgentID = agentID
	}
	if f.statsErr != nil {
		return service.AgentStats{}, f.statsErr
	}
	if stats, ok := f.statsByID[agentID]; ok {
		return stats, nil
	}
	return service.AgentStats{}, service.ErrAgentNotFound
}

func (f fakeAgentService) Apply(_ context.Context, agentID string) (service.ApplyAgentResult, error) {
	if f.state != nil {
		f.state.applyAgentID = agentID
	}
	if f.applyErr != nil {
		return service.ApplyAgentResult{}, f.applyErr
	}
	return f.applyResult, nil
}

func (f fakeAgentService) GetByToken(_ context.Context, agentToken string) (service.AgentSummary, error) {
	if f.state != nil {
		f.state.resolveTokens = append(f.state.resolveTokens, agentToken)
	}
	if agent, ok := f.agentsByToken[agentToken]; ok {
		return agent, nil
	}
	return service.AgentSummary{}, service.ErrAgentUnauthorized
}

func (f fakeAgentService) MonitorSnapshot(context.Context) (service.AgentMonitorSnapshot, error) {
	if f.state != nil {
		f.state.monitorSnapshotCalls++
	}
	if f.monitorErr != nil {
		return service.AgentMonitorSnapshot{}, f.monitorErr
	}
	if len(f.monitorSnapshots) > 0 {
		idx := 0
		if f.state != nil && f.state.monitorSnapshotCalls > 0 {
			idx = f.state.monitorSnapshotCalls - 1
		}
		if idx >= len(f.monitorSnapshots) {
			idx = len(f.monitorSnapshots) - 1
		}
		return f.monitorSnapshots[idx], nil
	}
	return f.monitorSnapshot, nil
}

func (f fakeAgentService) SubscribeMonitorUpdates(context.Context) (<-chan service.AgentMonitorUpdate, func()) {
	if f.state != nil {
		f.state.monitorSubscribed = true
	}
	ch := f.monitorUpdates
	if ch == nil {
		ch = make(chan service.AgentMonitorUpdate)
	}
	return ch, func() {
		if f.state != nil {
			f.state.monitorUnsubscribed = true
		}
	}
}

type fakeL4RuleService struct {
	rules       map[string][]service.L4Rule
	createdRule service.L4Rule
	updatedRule service.L4Rule
	deletedRule service.L4Rule
	state       *fakeL4RuleServiceState
}

type fakeL4RuleServiceState struct {
	getAgentIDs   []string
	getIDs        []int
	createInputs  []service.L4RuleInput
	updateInputs  []service.L4RuleInput
	updateRuleIDs []int
}

func (f fakeL4RuleService) List(_ context.Context, agentID string) ([]service.L4Rule, error) {
	rules, ok := f.rules[agentID]
	if !ok {
		return nil, service.ErrAgentNotFound
	}
	return rules, nil
}

func (f fakeL4RuleService) ListPage(_ context.Context, query service.ListQuery) ([]service.L4Rule, service.PageMeta, error) {
	query = service.NormalizeListQuery(query)
	var items []service.L4Rule
	if query.AgentID != "" {
		rules, err := f.List(context.Background(), query.AgentID)
		if err != nil {
			return nil, service.PageMeta{}, err
		}
		items = rules
	} else {
		for _, rules := range f.rules {
			items = append(items, rules...)
		}
	}
	page, meta := service.ApplyPage(items, query)
	return page, meta, nil
}

func (f fakeL4RuleService) Get(_ context.Context, agentID string, id int) (service.L4Rule, error) {
	if f.state != nil {
		f.state.getAgentIDs = append(f.state.getAgentIDs, agentID)
		f.state.getIDs = append(f.state.getIDs, id)
	}
	rules, ok := f.rules[agentID]
	if !ok {
		return service.L4Rule{}, service.ErrAgentNotFound
	}
	for _, rule := range rules {
		if rule.ID == id {
			return rule, nil
		}
	}
	return service.L4Rule{}, service.ErrRuleNotFound
}

func (f fakeL4RuleService) Create(_ context.Context, _ string, input service.L4RuleInput) (service.L4Rule, error) {
	if f.state != nil {
		f.state.createInputs = append(f.state.createInputs, input)
	}
	return f.createdRule, nil
}

func (f fakeL4RuleService) Update(_ context.Context, _ string, id int, input service.L4RuleInput) (service.L4Rule, error) {
	if f.state != nil {
		f.state.updateRuleIDs = append(f.state.updateRuleIDs, id)
		f.state.updateInputs = append(f.state.updateInputs, input)
	}
	return f.updatedRule, nil
}

func (f fakeL4RuleService) Delete(context.Context, string, int) (service.L4Rule, error) {
	return f.deletedRule, nil
}

type fakeRuleService struct {
	rules       map[string][]service.HTTPRule
	createdRule service.HTTPRule
	updatedRule service.HTTPRule
	deletedRule service.HTTPRule
	state       *fakeRuleServiceState
}

type fakeRuleServiceState struct {
	listAgentIDs   []string
	createAgentIDs []string
	createInputs   []service.HTTPRuleInput
	updateAgentIDs []string
	updateIDs      []int
	updateInputs   []service.HTTPRuleInput
	deleteAgentIDs []string
	deleteIDs      []int
}

func (f fakeRuleService) List(_ context.Context, agentID string) ([]service.HTTPRule, error) {
	if f.state != nil {
		f.state.listAgentIDs = append(f.state.listAgentIDs, agentID)
	}
	rules, ok := f.rules[agentID]
	if !ok {
		return nil, service.ErrAgentNotFound
	}
	return rules, nil
}

func (f fakeRuleService) ListPage(_ context.Context, query service.ListQuery) ([]service.HTTPRule, service.PageMeta, error) {
	query = service.NormalizeListQuery(query)
	var items []service.HTTPRule
	if query.AgentID != "" {
		rules, err := f.List(context.Background(), query.AgentID)
		if err != nil {
			return nil, service.PageMeta{}, err
		}
		items = rules
	} else {
		for agentID, rules := range f.rules {
			_ = agentID
			items = append(items, rules...)
		}
	}
	page, meta := service.ApplyPage(items, query)
	return page, meta, nil
}

func (f fakeRuleService) Get(_ context.Context, agentID string, id int) (service.HTTPRule, error) {
	if f.state != nil {
		f.state.listAgentIDs = append(f.state.listAgentIDs, agentID)
	}
	rules, ok := f.rules[agentID]
	if !ok {
		return service.HTTPRule{}, service.ErrAgentNotFound
	}
	for _, rule := range rules {
		if rule.ID == id {
			return rule, nil
		}
	}
	return service.HTTPRule{}, service.ErrRuleNotFound
}

func (f fakeRuleService) Create(_ context.Context, agentID string, input service.HTTPRuleInput) (service.HTTPRule, error) {
	if f.state != nil {
		f.state.createAgentIDs = append(f.state.createAgentIDs, agentID)
		f.state.createInputs = append(f.state.createInputs, input)
	}
	return f.createdRule, nil
}

func (f fakeRuleService) Update(_ context.Context, agentID string, id int, input service.HTTPRuleInput) (service.HTTPRule, error) {
	if f.state != nil {
		f.state.updateAgentIDs = append(f.state.updateAgentIDs, agentID)
		f.state.updateIDs = append(f.state.updateIDs, id)
		f.state.updateInputs = append(f.state.updateInputs, input)
	}
	return f.updatedRule, nil
}

func (f fakeRuleService) Delete(_ context.Context, agentID string, id int) (service.HTTPRule, error) {
	if f.state != nil {
		f.state.deleteAgentIDs = append(f.state.deleteAgentIDs, agentID)
		f.state.deleteIDs = append(f.state.deleteIDs, id)
	}
	return f.deletedRule, nil
}

type fakeTaskService struct {
	taskByID           map[string]service.TaskRecord
	createResult       service.TaskRecord
	createErr          error
	getErr             error
	registerSessionErr error
	registerDispatch   *service.TaskEnvelope
	state              *fakeTaskServiceState
}

type fakeTaskServiceState struct {
	createRequests       []service.TaskCreateRequest
	getAgentIDs          []string
	getTaskIDs           []string
	sessionRegistrations []service.TaskSessionRegistration
	sessionRegistered    chan<- struct{}
	updates              []service.TaskUpdateInput
}

type fullDuplexRecorder struct {
	*httptest.ResponseRecorder
	flushed chan<- struct{}
}

func (r *fullDuplexRecorder) EnableFullDuplex() error {
	return nil
}

func (r *fullDuplexRecorder) Flush() {
	r.ResponseRecorder.Flush()
	if r.flushed != nil {
		r.flushed <- struct{}{}
	}
}

func (f fakeTaskService) CreateAndDispatch(req service.TaskCreateRequest) (service.TaskRecord, error) {
	if f.state != nil {
		f.state.createRequests = append(f.state.createRequests, req)
	}
	if f.createErr != nil {
		return service.TaskRecord{}, f.createErr
	}
	if f.createResult.ID != "" {
		return f.createResult, nil
	}
	return service.TaskRecord{ID: "task-1", AgentID: req.AgentID, Type: req.Type, State: "dispatched"}, nil
}

func (f fakeTaskService) Get(_ context.Context, agentID string, taskID string) (service.TaskRecord, error) {
	if f.state != nil {
		f.state.getAgentIDs = append(f.state.getAgentIDs, agentID)
		f.state.getTaskIDs = append(f.state.getTaskIDs, taskID)
	}
	if f.getErr != nil {
		return service.TaskRecord{}, f.getErr
	}
	record, ok := f.taskByID[taskID]
	if !ok {
		return service.TaskRecord{}, service.ErrTaskNotFound
	}
	return record, nil
}

func (f fakeTaskService) RegisterSession(reg service.TaskSessionRegistration) error {
	if f.state != nil {
		f.state.sessionRegistrations = append(f.state.sessionRegistrations, reg)
	}
	if f.registerDispatch != nil && reg.Session != nil {
		if err := reg.Session.SendTask(*f.registerDispatch); err != nil {
			return err
		}
	}
	if f.state != nil && f.state.sessionRegistered != nil {
		f.state.sessionRegistered <- struct{}{}
	}
	return f.registerSessionErr
}

func (f fakeTaskService) ApplyUpdate(_ context.Context, input service.TaskUpdateInput) error {
	if f.state != nil {
		f.state.updates = append(f.state.updates, input)
	}
	return nil
}

type fakeVersionPolicyService struct {
	policies      []service.VersionPolicy
	createdPolicy service.VersionPolicy
	updatedPolicy service.VersionPolicy
	deletedPolicy service.VersionPolicy
}

func (f fakeVersionPolicyService) List(context.Context) ([]service.VersionPolicy, error) {
	return f.policies, nil
}

func (f fakeVersionPolicyService) Create(context.Context, service.VersionPolicyInput) (service.VersionPolicy, error) {
	return f.createdPolicy, nil
}

func (f fakeVersionPolicyService) Update(context.Context, string, service.VersionPolicyInput) (service.VersionPolicy, error) {
	return f.updatedPolicy, nil
}

func (f fakeVersionPolicyService) Delete(context.Context, string) (service.VersionPolicy, error) {
	return f.deletedPolicy, nil
}

type fakeEgressProfileService struct {
	profiles       []service.EgressProfile
	createdProfile service.EgressProfile
	updatedProfile service.EgressProfile
	deletedProfile service.EgressProfile
	getErr         error
	createErr      error
	updateErr      error
	deleteErr      error
	state          *fakeEgressProfileServiceState
}

type fakeEgressProfileServiceState struct {
	createInputs []service.EgressProfileInput
	updateIDs    []int
	updateInputs []service.EgressProfileInput
	deleteIDs    []int
}

func (f fakeEgressProfileService) List(context.Context) ([]service.EgressProfile, error) {
	return f.profiles, nil
}

func (f fakeEgressProfileService) Get(_ context.Context, id int) (service.EgressProfile, error) {
	if f.getErr != nil {
		return service.EgressProfile{}, f.getErr
	}
	for _, profile := range f.profiles {
		if profile.ID == id {
			return profile, nil
		}
	}
	return service.EgressProfile{}, service.ErrEgressProfileNotFound
}

func (f fakeEgressProfileService) Create(_ context.Context, input service.EgressProfileInput) (service.EgressProfile, error) {
	if f.state != nil {
		f.state.createInputs = append(f.state.createInputs, input)
	}
	if f.createErr != nil {
		return service.EgressProfile{}, f.createErr
	}
	return f.createdProfile, nil
}

func (f fakeEgressProfileService) Update(_ context.Context, id int, input service.EgressProfileInput) (service.EgressProfile, error) {
	if f.state != nil {
		f.state.updateIDs = append(f.state.updateIDs, id)
		f.state.updateInputs = append(f.state.updateInputs, input)
	}
	if f.updateErr != nil {
		return service.EgressProfile{}, f.updateErr
	}
	return f.updatedProfile, nil
}

func (f fakeEgressProfileService) Delete(_ context.Context, id int) (service.EgressProfile, error) {
	if f.state != nil {
		f.state.deleteIDs = append(f.state.deleteIDs, id)
	}
	if f.deleteErr != nil {
		return service.EgressProfile{}, f.deleteErr
	}
	return f.deletedProfile, nil
}

type fakeRelayListenerService struct {
	listeners       map[string][]service.RelayListener
	createdListener service.RelayListener
	updatedListener service.RelayListener
	deletedListener service.RelayListener
	state           *fakeRelayListenerServiceState
}

type fakeRelayListenerServiceState struct {
	createdInputs []service.RelayListenerInput
	updatedInputs []service.RelayListenerInput
}

func (f fakeRelayListenerService) List(_ context.Context, agentID string) ([]service.RelayListener, error) {
	listeners, ok := f.listeners[agentID]
	if !ok {
		return nil, service.ErrAgentNotFound
	}
	return listeners, nil
}

func (f fakeRelayListenerService) ListPage(_ context.Context, query service.ListQuery) ([]service.RelayListener, service.PageMeta, error) {
	query = service.NormalizeListQuery(query)
	var items []service.RelayListener
	if query.AgentID != "" {
		listeners, err := f.List(context.Background(), query.AgentID)
		if err != nil {
			return nil, service.PageMeta{}, err
		}
		items = listeners
	} else {
		for _, listeners := range f.listeners {
			items = append(items, listeners...)
		}
	}
	page, meta := service.ApplyPage(items, query)
	return page, meta, nil
}

func (f fakeRelayListenerService) Create(_ context.Context, _ string, input service.RelayListenerInput) (service.RelayListener, error) {
	if f.state != nil {
		f.state.createdInputs = append(f.state.createdInputs, input)
	}
	return f.createdListener, nil
}

func (f fakeRelayListenerService) Update(_ context.Context, _ string, _ int, input service.RelayListenerInput) (service.RelayListener, error) {
	if f.state != nil {
		f.state.updatedInputs = append(f.state.updatedInputs, input)
	}
	return f.updatedListener, nil
}

func (f fakeRelayListenerService) Delete(context.Context, string, int) (service.RelayListener, error) {
	return f.deletedListener, nil
}

type fakeCertificateService struct {
	certificates       map[string][]service.ManagedCertificate
	createdCertificate service.ManagedCertificate
	updatedCertificate service.ManagedCertificate
	deletedCertificate service.ManagedCertificate
	issuedCertificate  service.ManagedCertificate
	state              *fakeCertificateServiceState
}

type fakeCertificateServiceState struct {
	createInputs   []service.ManagedCertificateInput
	createAgentIDs []string
	updateInputs   []service.ManagedCertificateInput
	updateAgentIDs []string
	updateIDs      []int
	deleteAgentIDs []string
	deleteIDs      []int
	listAgentIDs   []string
	issueAgentIDs  []string
	issueIDs       []int
}

func (f fakeCertificateService) List(_ context.Context, agentID string) ([]service.ManagedCertificate, error) {
	if f.state != nil {
		f.state.listAgentIDs = append(f.state.listAgentIDs, agentID)
	}
	certs, ok := f.certificates[agentID]
	if !ok {
		return nil, service.ErrAgentNotFound
	}
	return certs, nil
}

func (f fakeCertificateService) ListPage(_ context.Context, query service.ListQuery) ([]service.ManagedCertificate, service.PageMeta, error) {
	query = service.NormalizeListQuery(query)
	var items []service.ManagedCertificate
	if query.AgentID != "" {
		certs, err := f.List(context.Background(), query.AgentID)
		if err != nil {
			return nil, service.PageMeta{}, err
		}
		items = certs
	} else if certs, ok := f.certificates[""]; ok {
		items = append(items, certs...)
	} else {
		for _, certs := range f.certificates {
			items = append(items, certs...)
		}
	}
	page, meta := service.ApplyPage(items, query)
	return page, meta, nil
}

func (f fakeCertificateService) Create(_ context.Context, agentID string, input service.ManagedCertificateInput) (service.ManagedCertificate, error) {
	if f.state != nil {
		f.state.createAgentIDs = append(f.state.createAgentIDs, agentID)
		f.state.createInputs = append(f.state.createInputs, input)
	}
	return f.createdCertificate, nil
}

func (f fakeCertificateService) Update(_ context.Context, agentID string, id int, input service.ManagedCertificateInput) (service.ManagedCertificate, error) {
	if f.state != nil {
		f.state.updateAgentIDs = append(f.state.updateAgentIDs, agentID)
		f.state.updateIDs = append(f.state.updateIDs, id)
		f.state.updateInputs = append(f.state.updateInputs, input)
	}
	return f.updatedCertificate, nil
}

func (f fakeCertificateService) Delete(_ context.Context, agentID string, id int) (service.ManagedCertificate, error) {
	if f.state != nil {
		f.state.deleteAgentIDs = append(f.state.deleteAgentIDs, agentID)
		f.state.deleteIDs = append(f.state.deleteIDs, id)
	}
	return f.deletedCertificate, nil
}

func (f fakeCertificateService) Issue(_ context.Context, agentID string, id int) (service.ManagedCertificate, error) {
	if f.state != nil {
		f.state.issueAgentIDs = append(f.state.issueAgentIDs, agentID)
		f.state.issueIDs = append(f.state.issueIDs, id)
	}
	return f.issuedCertificate, nil
}

type fakeBackupService struct {
	exportBody     []byte
	exportFilename string
	importResult   service.BackupImportResult
	importErr      error
	state          *fakeBackupServiceState
}

type fakeBackupServiceState struct {
	importBodies [][]byte
}

func (f fakeBackupService) Export(context.Context) ([]byte, string, error) {
	return f.exportBody, f.exportFilename, nil
}

func (f fakeBackupService) ExportSelective(_ context.Context, opts service.BackupExportOptions) ([]byte, string, error) {
	if opts.Agents && opts.HTTPRules && opts.L4Rules && opts.RelayListeners && opts.Certificates && opts.VersionPolicies {
		return f.exportBody, f.exportFilename, nil
	}
	return f.exportBody, f.exportFilename, nil
}

func (f fakeBackupService) Import(_ context.Context, body []byte) (service.BackupImportResult, error) {
	if f.state != nil {
		copyBody := append([]byte(nil), body...)
		f.state.importBodies = append(f.state.importBodies, copyBody)
	}
	if f.importErr != nil {
		return service.BackupImportResult{}, f.importErr
	}
	return f.importResult, nil
}

func (f fakeBackupService) ResourceCounts(context.Context) (service.BackupCounts, error) {
	return service.BackupCounts{}, nil
}

func (f fakeBackupService) Preview(_ context.Context, _ []byte) (service.BackupImportResult, error) {
	return service.BackupImportResult{}, nil
}

func TestRouterServesPanelAuthAndInfoEndpoints(t *testing.T) {
	t.Parallel()
	router, err := NewRouter(Dependencies{
		Config: config.Config{
			PanelToken:    "secret",
			RegisterToken: "register-secret",
		},
		SystemService: fakeSystemService{
			info: service.SystemInfo{
				Role:              "master",
				LocalApplyRuntime: "go-agent",
				DefaultAgentID:    "local",
				LocalAgentEnabled: true,
			},
		},
		AgentService:         fakeAgentService{},
		RuleService:          fakeRuleService{},
		L4RuleService:        fakeL4RuleService{},
		VersionPolicyService: fakeVersionPolicyService{},
		RelayListenerService: fakeRelayListenerService{},
		CertificateService:   fakeCertificateService{},
	})
	if err != nil {
		t.Fatalf("NewRouter() error = %v", err)
	}

	healthReq := httptest.NewRequest(http.MethodHead, "/panel-api/health", nil)
	healthResp := httptest.NewRecorder()
	router.ServeHTTP(healthResp, healthReq)
	if healthResp.Code != http.StatusOK {
		t.Fatalf("HEAD /panel-api/health = %d", healthResp.Code)
	}

	verifyReq := httptest.NewRequest(http.MethodGet, "/panel-api/auth/verify", nil)
	verifyReq.Header.Set("X-Panel-Token", "secret")
	verifyResp := httptest.NewRecorder()
	router.ServeHTTP(verifyResp, verifyReq)
	if verifyResp.Code != http.StatusOK {
		t.Fatalf("GET /panel-api/auth/verify = %d", verifyResp.Code)
	}

	unauthorizedReq := httptest.NewRequest(http.MethodGet, "/panel-api/auth/verify", nil)
	unauthorizedResp := httptest.NewRecorder()
	router.ServeHTTP(unauthorizedResp, unauthorizedReq)
	if unauthorizedResp.Code != http.StatusUnauthorized {
		t.Fatalf("GET /panel-api/auth/verify without token = %d", unauthorizedResp.Code)
	}

	infoReq := httptest.NewRequest(http.MethodGet, "/panel-api/info", nil)
	infoReq.Header.Set("X-Panel-Token", "secret")
	infoResp := httptest.NewRecorder()
	router.ServeHTTP(infoResp, infoReq)
	if infoResp.Code != http.StatusOK {
		t.Fatalf("GET /panel-api/info = %d", infoResp.Code)
	}

	var payload map[string]any
	if err := json.Unmarshal(infoResp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if payload["role"] != "master" || payload["local_apply_runtime"] != "go-agent" {
		t.Fatalf("unexpected info payload: %+v", payload)
	}
	if payload["default_agent_id"] != "local" {
		t.Fatalf("default_agent_id = %v", payload["default_agent_id"])
	}
	localAgentEnabled, ok := payload["local_agent_enabled"].(bool)
	if !ok || !localAgentEnabled {
		t.Fatalf("local_agent_enabled = %v", payload["local_agent_enabled"])
	}
	if payload["master_register_token"] != "register-secret" {
		t.Fatalf("master_register_token = %v", payload["master_register_token"])
	}
}

func TestTokenMatchesRequiresExactSecret(t *testing.T) {
	t.Parallel()
	if !tokenMatches("secret", "secret") {
		t.Fatal("expected matching tokens to authorize")
	}
	if tokenMatches("secret", "Secret") {
		t.Fatal("expected mismatched tokens to be rejected")
	}
	if tokenMatches("secret", "") {
		t.Fatal("expected empty presented token to be rejected")
	}
	if tokenMatches("", "secret") {
		t.Fatal("expected empty expected token to be rejected")
	}
}

func TestRouterServesAgentsAndRulesEndpoints(t *testing.T) {
	t.Parallel()
	router, err := NewRouter(Dependencies{
		Config: config.Config{PanelToken: "secret"},
		SystemService: fakeSystemService{
			info: service.SystemInfo{
				Role:              "master",
				LocalApplyRuntime: "go-agent",
				DefaultAgentID:    "local",
				LocalAgentEnabled: true,
			},
		},
		AgentService: fakeAgentService{
			agents: []service.AgentSummary{{
				ID:             "local",
				Name:           "Local Agent",
				Mode:           "local",
				Status:         "online",
				IsLocal:        true,
				HTTPRulesCount: 1,
			}},
		},
		RuleService: fakeRuleService{
			rules: map[string][]service.HTTPRule{
				"local": {{
					ID:               1,
					AgentID:          "local",
					FrontendURL:      "https://emby.example.com",
					Backends:         []service.HTTPRuleBackend{{URL: "http://emby:8096"}},
					LoadBalancing:    service.HTTPLoadBalancing{Strategy: "round_robin"},
					Enabled:          true,
					Tags:             []string{},
					ProxyRedirect:    true,
					PassProxyHeaders: true,
					UserAgent:        "",
					CustomHeaders:    []service.HTTPCustomHeader{},
					Revision:         3,
				}},
			},
		},
		L4RuleService:        fakeL4RuleService{},
		VersionPolicyService: fakeVersionPolicyService{},
		RelayListenerService: fakeRelayListenerService{},
		CertificateService:   fakeCertificateService{},
	})
	if err != nil {
		t.Fatalf("NewRouter() error = %v", err)
	}

	agentsReq := httptest.NewRequest(http.MethodGet, "/panel-api/agents", nil)
	agentsReq.Header.Set("X-Panel-Token", "secret")
	agentsResp := httptest.NewRecorder()
	router.ServeHTTP(agentsResp, agentsReq)
	if agentsResp.Code != http.StatusOK {
		t.Fatalf("GET /panel-api/agents = %d", agentsResp.Code)
	}
	var agentsPayload map[string]any
	if err := json.Unmarshal(agentsResp.Body.Bytes(), &agentsPayload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	agentsValue, ok := agentsPayload["agents"].([]any)
	if !ok || len(agentsValue) != 1 {
		t.Fatalf("unexpected agents payload: %+v", agentsPayload)
	}
	agentValue, ok := agentsValue[0].(map[string]any)
	if !ok {
		t.Fatalf("agents[0] type = %T", agentsValue[0])
	}
	isLocal, ok := agentValue["is_local"].(bool)
	if !ok || !isLocal {
		t.Fatalf("agents[0].is_local = %v", agentValue["is_local"])
	}
	if agentValue["mode"] != "local" {
		t.Fatalf("agents[0].mode = %v", agentValue["mode"])
	}

	rulesReq := httptest.NewRequest(http.MethodGet, "/panel-api/agents/local/rules", nil)
	rulesReq.Header.Set("X-Panel-Token", "secret")
	rulesResp := httptest.NewRecorder()
	router.ServeHTTP(rulesResp, rulesReq)
	if rulesResp.Code != http.StatusOK {
		t.Fatalf("GET /panel-api/agents/local/rules = %d", rulesResp.Code)
	}

	missingReq := httptest.NewRequest(http.MethodGet, "/panel-api/agents/missing/rules", nil)
	missingReq.Header.Set("X-Panel-Token", "secret")
	missingResp := httptest.NewRecorder()
	router.ServeHTTP(missingResp, missingReq)
	if missingResp.Code != http.StatusNotFound {
		t.Fatalf("GET /panel-api/agents/missing/rules = %d", missingResp.Code)
	}
}

func TestRouterUpdatesAgentOutboundProxyAndRedactsResponse(t *testing.T) {
	t.Parallel()
	state := &fakeAgentServiceState{}
	router, err := NewRouter(Dependencies{
		Config: config.Config{PanelToken: "secret"},
		SystemService: fakeSystemService{
			info: service.SystemInfo{
				Role:              "master",
				LocalApplyRuntime: "go-agent",
				DefaultAgentID:    "local",
				LocalAgentEnabled: true,
			},
		},
		AgentService: fakeAgentService{
			updateAgent: service.AgentSummary{
				ID:               "edge-1",
				Name:             "Edge 1",
				OutboundProxyURL: "socks://user:pass@127.0.0.1:1080",
			},
			state: state,
		},
		RuleService:          fakeRuleService{},
		L4RuleService:        fakeL4RuleService{},
		VersionPolicyService: fakeVersionPolicyService{},
		RelayListenerService: fakeRelayListenerService{},
		CertificateService:   fakeCertificateService{},
	})
	if err != nil {
		t.Fatalf("NewRouter() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodPatch, "/panel-api/agents/edge-1", bytes.NewBufferString(`{"outbound_proxy_url":"socks://user:pass@127.0.0.1:1080"}`))
	req.Header.Set("X-Panel-Token", "secret")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("PATCH /panel-api/agents/edge-1 = %d body=%s", resp.Code, resp.Body.String())
	}
	if state.updateAgentID != "edge-1" || state.updateInput.OutboundProxyURL == nil || *state.updateInput.OutboundProxyURL != "socks://user:pass@127.0.0.1:1080" {
		t.Fatalf("update state = %+v", state)
	}

	var payload struct {
		Agent service.AgentSummary `json:"agent"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if payload.Agent.OutboundProxyURL != "socks://user:xxxxx@127.0.0.1:1080" {
		t.Fatalf("outbound_proxy_url = %q", payload.Agent.OutboundProxyURL)
	}
}

func TestRouterServesHTTPRuleCRUDAndValidation(t *testing.T) {
	t.Parallel()
	ruleState := &fakeRuleServiceState{}
	router, err := NewRouter(Dependencies{
		Config: config.Config{PanelToken: "secret"},
		SystemService: fakeSystemService{
			info: service.SystemInfo{
				Role:              "master",
				LocalApplyRuntime: "go-agent",
				DefaultAgentID:    "local",
				LocalAgentEnabled: true,
			},
		},
		AgentService: fakeAgentService{},
		RuleService: fakeRuleService{
			rules: map[string][]service.HTTPRule{
				"local": {{
					ID:               1,
					AgentID:          "local",
					FrontendURL:      "https://emby.example.com",
					Backends:         []service.HTTPRuleBackend{{URL: "http://emby:8096"}},
					LoadBalancing:    service.HTTPLoadBalancing{Strategy: "round_robin"},
					Enabled:          true,
					Tags:             []string{"media"},
					ProxyRedirect:    true,
					PassProxyHeaders: true,
					UserAgent:        "",
					CustomHeaders:    []service.HTTPCustomHeader{},
					Revision:         3,
				}},
			},
			createdRule: service.HTTPRule{ID: 2, AgentID: "local", FrontendURL: "https://new.example.com", Backends: []service.HTTPRuleBackend{{URL: "http://emby:8096"}}, RelayLayers: [][]int{{1, 2}, {3}}, EgressProfileID: intPtr(17)},
			updatedRule: service.HTTPRule{ID: 2, AgentID: "local", FrontendURL: "https://updated.example.com", Backends: []service.HTTPRuleBackend{{URL: "http://emby:8096"}}, RelayLayers: [][]int{{4}, {5, 6}}},
			deletedRule: service.HTTPRule{ID: 2, AgentID: "local", FrontendURL: "https://updated.example.com", Backends: []service.HTTPRuleBackend{{URL: "http://emby:8096"}}},
			state:       ruleState,
		},
		L4RuleService:        fakeL4RuleService{},
		VersionPolicyService: fakeVersionPolicyService{},
		RelayListenerService: fakeRelayListenerService{},
		CertificateService:   fakeCertificateService{},
	})
	if err != nil {
		t.Fatalf("NewRouter() error = %v", err)
	}

	getReq := httptest.NewRequest(http.MethodGet, "/panel-api/agents/local/rules", nil)
	getReq.Header.Set("X-Panel-Token", "secret")
	getResp := httptest.NewRecorder()
	router.ServeHTTP(getResp, getReq)
	if getResp.Code != http.StatusOK {
		t.Fatalf("GET /panel-api/agents/local/rules = %d", getResp.Code)
	}
	var getPayload map[string]any
	if err := json.Unmarshal(getResp.Body.Bytes(), &getPayload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if ok, cast := getPayload["ok"].(bool); !cast || !ok {
		t.Fatalf("GET ok = %v", getPayload["ok"])
	}
	if _, found := getPayload["rules"]; !found {
		t.Fatalf("GET payload missing rules: %+v", getPayload)
	}

	getAliasReq := httptest.NewRequest(http.MethodGet, "/api/agents/local/rules", nil)
	getAliasReq.Header.Set("X-Panel-Token", "secret")
	getAliasResp := httptest.NewRecorder()
	router.ServeHTTP(getAliasResp, getAliasReq)
	if getAliasResp.Code != http.StatusOK {
		t.Fatalf("GET /api/agents/local/rules = %d", getAliasResp.Code)
	}

	createReq := httptest.NewRequest(http.MethodPost, "/panel-api/agents/local/rules", bytes.NewBufferString(`{"frontend_url":"https://new.example.com","backends":[{"url":"http://emby:8096"}],"relay_layers":[[1,2],[3]],"egress_profile_id":17}`))
	createReq.Header.Set("X-Panel-Token", "secret")
	createReq.Header.Set("Content-Type", "application/json")
	createResp := httptest.NewRecorder()
	router.ServeHTTP(createResp, createReq)
	if createResp.Code != http.StatusCreated {
		t.Fatalf("POST /panel-api/agents/local/rules = %d", createResp.Code)
	}
	var createPayload map[string]any
	if err := json.Unmarshal(createResp.Body.Bytes(), &createPayload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if ok, cast := createPayload["ok"].(bool); !cast || !ok {
		t.Fatalf("POST ok = %v", createPayload["ok"])
	}
	if _, found := createPayload["rule"]; !found {
		t.Fatalf("POST payload missing rule: %+v", createPayload)
	}
	if len(ruleState.createInputs) != 1 || ruleState.createInputs[0].RelayLayers == nil || len(*ruleState.createInputs[0].RelayLayers) != 2 {
		t.Fatalf("POST relay_layers input = %+v", ruleState.createInputs)
	}
	if ruleState.createInputs[0].EgressProfileID == nil || *ruleState.createInputs[0].EgressProfileID != 17 {
		t.Fatalf("POST egress_profile_id input = %+v", ruleState.createInputs[0].EgressProfileID)
	}
	if !strings.Contains(createResp.Body.String(), `"relay_layers":[[1,2],[3]]`) {
		t.Fatalf("POST response missing relay_layers: %s", createResp.Body.String())
	}
	if !strings.Contains(createResp.Body.String(), `"egress_profile_id":17`) {
		t.Fatalf("POST response missing egress_profile_id: %s", createResp.Body.String())
	}

	updateReq := httptest.NewRequest(http.MethodPut, "/panel-api/agents/local/rules/2", bytes.NewBufferString(`{"frontend_url":"https://updated.example.com","relay_layers":[[4],[5,6]],"egress_profile_id":0}`))
	updateReq.Header.Set("X-Panel-Token", "secret")
	updateReq.Header.Set("Content-Type", "application/json")
	updateResp := httptest.NewRecorder()
	router.ServeHTTP(updateResp, updateReq)
	if updateResp.Code != http.StatusOK {
		t.Fatalf("PUT /panel-api/agents/local/rules/2 = %d", updateResp.Code)
	}
	if len(ruleState.updateInputs) != 1 || ruleState.updateInputs[0].RelayLayers == nil || len(*ruleState.updateInputs[0].RelayLayers) != 2 {
		t.Fatalf("PUT relay_layers input = %+v", ruleState.updateInputs)
	}
	if ruleState.updateInputs[0].EgressProfileID == nil || *ruleState.updateInputs[0].EgressProfileID != 0 {
		t.Fatalf("PUT egress_profile_id input = %+v", ruleState.updateInputs[0].EgressProfileID)
	}
	if !strings.Contains(updateResp.Body.String(), `"relay_layers":[[4],[5,6]]`) {
		t.Fatalf("PUT response missing relay_layers: %s", updateResp.Body.String())
	}
	if strings.Contains(updateResp.Body.String(), `"egress_profile_id"`) {
		t.Fatalf("PUT response included cleared egress_profile_id: %s", updateResp.Body.String())
	}
	var updatePayload map[string]any
	if err := json.Unmarshal(updateResp.Body.Bytes(), &updatePayload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if ok, cast := updatePayload["ok"].(bool); !cast || !ok {
		t.Fatalf("PUT ok = %v", updatePayload["ok"])
	}
	if _, found := updatePayload["rule"]; !found {
		t.Fatalf("PUT payload missing rule: %+v", updatePayload)
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, "/panel-api/agents/local/rules/2", nil)
	deleteReq.Header.Set("X-Panel-Token", "secret")
	deleteResp := httptest.NewRecorder()
	router.ServeHTTP(deleteResp, deleteReq)
	if deleteResp.Code != http.StatusOK {
		t.Fatalf("DELETE /panel-api/agents/local/rules/2 = %d", deleteResp.Code)
	}
	var deletePayload map[string]any
	if err := json.Unmarshal(deleteResp.Body.Bytes(), &deletePayload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if ok, cast := deletePayload["ok"].(bool); !cast || !ok {
		t.Fatalf("DELETE ok = %v", deletePayload["ok"])
	}
	if _, found := deletePayload["rule"]; !found {
		t.Fatalf("DELETE payload missing rule: %+v", deletePayload)
	}

	invalidIDReq := httptest.NewRequest(http.MethodPut, "/panel-api/agents/local/rules/not-an-int", bytes.NewBufferString(`{}`))
	invalidIDReq.Header.Set("X-Panel-Token", "secret")
	invalidIDResp := httptest.NewRecorder()
	router.ServeHTTP(invalidIDResp, invalidIDReq)
	if invalidIDResp.Code != http.StatusBadRequest {
		t.Fatalf("PUT /panel-api/agents/local/rules/not-an-int = %d", invalidIDResp.Code)
	}
}

func intPtr(value int) *int {
	return &value
}
