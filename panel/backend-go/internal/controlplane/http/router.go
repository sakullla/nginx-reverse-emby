package http

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/coordinator"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/revision"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/service"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

type SystemService interface {
	Info(context.Context) service.SystemInfo
}

type AgentService interface {
	List(context.Context) ([]service.AgentSummary, error)
	Get(context.Context, string) (service.AgentSummary, error)
	GetByToken(context.Context, string) (service.AgentSummary, error)
	Register(context.Context, service.RegisterRequest, string) (service.AgentSummary, error)
	Update(context.Context, string, service.UpdateAgentRequest) (service.AgentSummary, error)
	Delete(context.Context, string) (service.AgentSummary, error)
	Stats(context.Context, string) (service.AgentStats, error)
	Apply(context.Context, string) (service.ApplyAgentResult, error)
	Heartbeat(context.Context, service.HeartbeatRequest, string) (service.HeartbeatReply, error)
	MonitorSnapshot(context.Context) (service.AgentMonitorSnapshot, error)
	SubscribeMonitorUpdates(context.Context) (<-chan service.AgentMonitorUpdate, func())
}

type TrafficService interface {
	GetPolicy(context.Context, string) (service.TrafficPolicy, error)
	UpdatePolicy(context.Context, string, service.TrafficPolicy) (service.TrafficPolicy, error)
	Summary(context.Context, string) (service.TrafficSummary, error)
	Trend(context.Context, service.TrafficTrendQuery) ([]service.TrafficTrendPoint, error)
	Calibrate(context.Context, string, service.TrafficCalibrationRequest) (service.TrafficSummary, error)
	Cleanup(context.Context, string) (service.TrafficCleanupResult, error)
	Overview(ctx context.Context, agentFilter string, granularity string, agentNames map[string]string) (service.TrafficOverviewResult, error)
	Aggregate(ctx context.Context, agentFilter string, granularity string, agentNames map[string]string) (service.TrafficAggregateResult, error)
}

type RuleService interface {
	List(context.Context, string) ([]service.HTTPRule, error)
	ListPage(context.Context, service.ListQuery) ([]service.HTTPRule, service.PageMeta, error)
	Get(context.Context, string, int) (service.HTTPRule, error)
	Create(context.Context, string, service.HTTPRuleInput) (service.HTTPRule, error)
	Update(context.Context, string, int, service.HTTPRuleInput) (service.HTTPRule, error)
	Delete(context.Context, string, int) (service.HTTPRule, error)
}

type L4RuleService interface {
	List(context.Context, string) ([]service.L4Rule, error)
	ListPage(context.Context, service.ListQuery) ([]service.L4Rule, service.PageMeta, error)
	Get(context.Context, string, int) (service.L4Rule, error)
	Create(context.Context, string, service.L4RuleInput) (service.L4Rule, error)
	Update(context.Context, string, int, service.L4RuleInput) (service.L4Rule, error)
	Delete(context.Context, string, int) (service.L4Rule, error)
}

type TaskService interface {
	CreateAndDispatch(service.TaskCreateRequest) (service.TaskRecord, error)
	Get(context.Context, string, string) (service.TaskRecord, error)
	RegisterSession(service.TaskSessionRegistration) error
	ApplyUpdate(context.Context, service.TaskUpdateInput) error
}

type VersionPolicyService interface {
	List(context.Context) ([]service.VersionPolicy, error)
	Create(context.Context, service.VersionPolicyInput) (service.VersionPolicy, error)
	Update(context.Context, string, service.VersionPolicyInput) (service.VersionPolicy, error)
	Delete(context.Context, string) (service.VersionPolicy, error)
}

type EgressProfileService interface {
	List(context.Context) ([]service.EgressProfile, error)
	Get(context.Context, int) (service.EgressProfile, error)
	Create(context.Context, service.EgressProfileInput) (service.EgressProfile, error)
	Update(context.Context, int, service.EgressProfileInput) (service.EgressProfile, error)
	Delete(context.Context, int) (service.EgressProfile, error)
}

type RelayListenerService interface {
	List(context.Context, string) ([]service.RelayListener, error)
	ListPage(context.Context, service.ListQuery) ([]service.RelayListener, service.PageMeta, error)
	Create(context.Context, string, service.RelayListenerInput) (service.RelayListener, error)
	Update(context.Context, string, int, service.RelayListenerInput) (service.RelayListener, error)
	Delete(context.Context, string, int) (service.RelayListener, error)
}

type CertificateService interface {
	List(context.Context, string) ([]service.ManagedCertificate, error)
	ListPage(context.Context, service.ListQuery) ([]service.ManagedCertificate, service.PageMeta, error)
	Create(context.Context, string, service.ManagedCertificateInput) (service.ManagedCertificate, error)
	Update(context.Context, string, int, service.ManagedCertificateInput) (service.ManagedCertificate, error)
	Delete(context.Context, string, int) (service.ManagedCertificate, error)
	Issue(context.Context, string, int) (service.ManagedCertificate, error)
}

type BackupService interface {
	Export(context.Context) ([]byte, string, error)
	ExportSelective(context.Context, service.BackupExportOptions) ([]byte, string, error)
	Import(context.Context, []byte) (service.BackupImportResult, error)
	ResourceCounts(context.Context) (service.BackupCounts, error)
	Preview(context.Context, []byte) (service.BackupImportResult, error)
}

type RevisionService interface {
	GetOperationStatus(context.Context, string) (service.OperationStatus, error)
	DismissOperation(context.Context, string) (service.OperationStatus, error)
	GetAgentRevisionStatus(context.Context, string, int64) (service.AgentRevisionStatus, error)
	Retry(context.Context, string, int64) (service.AgentRevisionStatus, error)
	Rollback(context.Context, string) (service.OperationStatus, error)
	ListEvents(context.Context, service.RevisionEventQuery) (service.RevisionEventPage, error)
	PullRemoteRevision(context.Context, string) (service.RemoteRevisionPull, error)
	StartRemoteRevision(context.Context, string, service.RemoteRevisionStart) (service.AgentRevisionStatus, error)
	ReportRemoteRevision(context.Context, string, service.RemoteRevisionReport) (service.AgentRevisionStatus, error)
	SaveMutationResponse(context.Context, string, string, string, []byte) error
	LoadMutationResponse(context.Context, string, string, string) (map[string]any, bool, error)
	LoadMutationResponseByKey(context.Context, string, string) (map[string]any, bool, error)
}

type Dependencies struct {
	Config                       config.Config
	SystemService                SystemService
	AgentService                 AgentService
	RuleService                  RuleService
	L4RuleService                L4RuleService
	VersionPolicyService         VersionPolicyService
	EgressProfileService         EgressProfileService
	RelayListenerService         RelayListenerService
	CertificateService           CertificateService
	TaskService                  TaskService
	BackupService                BackupService
	TrafficService               TrafficService
	RevisionService              RevisionService
	MonitorStreamRefreshInterval time.Duration
	MonitorStreamMaxAge          time.Duration
	cleanup                      func() error
}

var openConfiguredStore = storage.NewConfiguredStore

type legacyRuleListService interface {
	ListHTTPRules(context.Context, string) ([]service.HTTPRule, error)
}

type agentRuleServiceAdapter struct {
	agent legacyRuleListService
}

type unavailableBackupService struct{}

type unavailableTrafficService struct{}

type unavailableEgressProfileService struct{}

func (unavailableBackupService) Export(context.Context) ([]byte, string, error) {
	return nil, "", fmt.Errorf("backup service unavailable")
}

func (unavailableBackupService) ExportSelective(context.Context, service.BackupExportOptions) ([]byte, string, error) {
	return nil, "", fmt.Errorf("backup service unavailable")
}

func (unavailableBackupService) Import(context.Context, []byte) (service.BackupImportResult, error) {
	return service.BackupImportResult{}, fmt.Errorf("backup service unavailable")
}

func (unavailableBackupService) ResourceCounts(context.Context) (service.BackupCounts, error) {
	return service.BackupCounts{}, fmt.Errorf("backup service unavailable")
}

func (unavailableBackupService) Preview(context.Context, []byte) (service.BackupImportResult, error) {
	return service.BackupImportResult{}, fmt.Errorf("backup service unavailable")
}

func (unavailableTrafficService) GetPolicy(context.Context, string) (service.TrafficPolicy, error) {
	return service.TrafficPolicy{}, trafficStatsDisabledError()
}

func (unavailableTrafficService) UpdatePolicy(context.Context, string, service.TrafficPolicy) (service.TrafficPolicy, error) {
	return service.TrafficPolicy{}, trafficStatsDisabledError()
}

func (unavailableTrafficService) Summary(context.Context, string) (service.TrafficSummary, error) {
	return service.TrafficSummary{}, trafficStatsDisabledError()
}

func (unavailableTrafficService) Trend(context.Context, service.TrafficTrendQuery) ([]service.TrafficTrendPoint, error) {
	return nil, trafficStatsDisabledError()
}

func (unavailableTrafficService) Calibrate(context.Context, string, service.TrafficCalibrationRequest) (service.TrafficSummary, error) {
	return service.TrafficSummary{}, trafficStatsDisabledError()
}

func (unavailableTrafficService) Cleanup(context.Context, string) (service.TrafficCleanupResult, error) {
	return service.TrafficCleanupResult{}, trafficStatsDisabledError()
}

func (unavailableTrafficService) Overview(context.Context, string, string, map[string]string) (service.TrafficOverviewResult, error) {
	return service.TrafficOverviewResult{}, trafficStatsDisabledError()
}

func (unavailableTrafficService) Aggregate(context.Context, string, string, map[string]string) (service.TrafficAggregateResult, error) {
	return service.TrafficAggregateResult{}, trafficStatsDisabledError()
}

func trafficStatsDisabledError() error {
	return service.TrafficServiceError{Code: service.ErrCodeTrafficStatsDisabled, Err: service.ErrTrafficStatsDisabled}
}

func (unavailableEgressProfileService) List(context.Context) ([]service.EgressProfile, error) {
	return nil, fmt.Errorf("egress profile service unavailable")
}

func (unavailableEgressProfileService) Get(context.Context, int) (service.EgressProfile, error) {
	return service.EgressProfile{}, fmt.Errorf("egress profile service unavailable")
}

func (unavailableEgressProfileService) Create(context.Context, service.EgressProfileInput) (service.EgressProfile, error) {
	return service.EgressProfile{}, fmt.Errorf("egress profile service unavailable")
}

func (unavailableEgressProfileService) Update(context.Context, int, service.EgressProfileInput) (service.EgressProfile, error) {
	return service.EgressProfile{}, fmt.Errorf("egress profile service unavailable")
}

func (unavailableEgressProfileService) Delete(context.Context, int) (service.EgressProfile, error) {
	return service.EgressProfile{}, fmt.Errorf("egress profile service unavailable")
}

func (a agentRuleServiceAdapter) List(ctx context.Context, agentID string) ([]service.HTTPRule, error) {
	return a.agent.ListHTTPRules(ctx, agentID)
}

func (a agentRuleServiceAdapter) ListPage(ctx context.Context, query service.ListQuery) ([]service.HTTPRule, service.PageMeta, error) {
	rules, err := a.List(ctx, query.AgentID)
	if err != nil {
		return nil, service.PageMeta{}, err
	}
	query = service.NormalizeListQuery(query)
	filtered := make([]service.HTTPRule, 0, len(rules))
	for _, rule := range rules {
		if query.Q != "" {
			hay := strings.ToLower(rule.FrontendURL + " " + rule.AgentID + " " + rule.AgentName)
			if !strings.Contains(hay, strings.ToLower(query.Q)) {
				continue
			}
		}
		filtered = append(filtered, rule)
	}
	page, meta := service.ApplyPage(filtered, query)
	return page, meta, nil
}

func (a agentRuleServiceAdapter) Get(ctx context.Context, agentID string, id int) (service.HTTPRule, error) {
	rules, err := a.agent.ListHTTPRules(ctx, agentID)
	if err != nil {
		return service.HTTPRule{}, err
	}
	for _, rule := range rules {
		if rule.ID == id {
			return rule, nil
		}
	}
	return service.HTTPRule{}, service.ErrRuleNotFound
}

func (a agentRuleServiceAdapter) Create(context.Context, string, service.HTTPRuleInput) (service.HTTPRule, error) {
	return service.HTTPRule{}, fmt.Errorf("%w: rule service is read-only", service.ErrInvalidArgument)
}

func (a agentRuleServiceAdapter) Update(context.Context, string, int, service.HTTPRuleInput) (service.HTTPRule, error) {
	return service.HTTPRule{}, fmt.Errorf("%w: rule service is read-only", service.ErrInvalidArgument)
}

func (a agentRuleServiceAdapter) Delete(context.Context, string, int) (service.HTTPRule, error) {
	return service.HTTPRule{}, fmt.Errorf("%w: rule service is read-only", service.ErrInvalidArgument)
}

func NewRouter(deps Dependencies) (http.Handler, error) {
	resolved, err := deps.withDefaults()
	if err != nil {
		return nil, err
	}

	mux := http.NewServeMux()
	for _, prefix := range []string{"/panel-api", "/api"} {
		mux.Handle(prefix+"/health", http.HandlerFunc(resolved.handleHealth))
		mux.Handle(prefix+"/auth/verify", http.HandlerFunc(resolved.handleVerify))
		mux.Handle(prefix+"/info", resolved.requirePanelToken(http.HandlerFunc(resolved.handleInfo)))
		mux.Handle(prefix+"/public/join-agent.sh", http.HandlerFunc(resolved.handleJoinAgentScript))
		mux.Handle(prefix+"/public/agent-assets/", http.HandlerFunc(resolved.handlePublicAgentAsset))
		mux.Handle(prefix+"/agents/register", http.HandlerFunc(resolved.handleRegisterAgent))
		mux.Handle(prefix+"/agents/heartbeat", http.HandlerFunc(resolved.handleHeartbeat))
		mux.Handle(prefix+"/agent-revisions/pull", http.HandlerFunc(resolved.handleRemoteRevisionPull))
		mux.Handle(prefix+"/agent-revisions/{revision}/start", http.HandlerFunc(resolved.handleRemoteRevisionStart))
		mux.Handle(prefix+"/agent-revisions/{revision}/report", http.HandlerFunc(resolved.handleRemoteRevisionReport))
		mux.Handle(prefix+"/agents/task-session", http.HandlerFunc(resolved.handleAgentTaskSession))
		mux.Handle(prefix+"/agents/task-stream", http.HandlerFunc(resolved.handleAgentTaskStream))
		mux.Handle(prefix+"/agent-tasks/{taskID}/updates", http.HandlerFunc(resolved.handleAgentTaskUpdate))
		if resolved.BackupService != nil {
			mux.Handle(prefix+"/system/backup/export", resolved.requirePanelToken(http.HandlerFunc(resolved.handleBackupExport)))
			mux.Handle(prefix+"/system/backup/import", resolved.requirePanelToken(http.HandlerFunc(resolved.handleBackupImport)))
			mux.Handle(prefix+"/system/backup/import/preview", resolved.requirePanelToken(http.HandlerFunc(resolved.handleBackupImportPreview)))
			mux.Handle(prefix+"/system/backup/counts", resolved.requirePanelToken(http.HandlerFunc(resolved.handleBackupResourceCounts)))
		}
		mux.Handle(prefix+"/agents", resolved.requirePanelToken(http.HandlerFunc(resolved.handleAgents)))
		mux.Handle(prefix+"/agents/monitor-stream", resolved.requirePanelToken(http.HandlerFunc(resolved.handleAgentMonitorStream)))
		mux.Handle(prefix+"/metrics", resolved.requirePanelToken(http.HandlerFunc(resolved.handleObservabilityMetrics)))
		mux.Handle(prefix+"/agents/{agentID}", resolved.requirePanelToken(http.HandlerFunc(resolved.handleAgent)))
		mux.Handle(prefix+"/agents/{agentID}/stats", resolved.requirePanelToken(http.HandlerFunc(resolved.handleAgentStats)))
		mux.Handle(prefix+"/agents/{agentID}/apply", resolved.requirePanelToken(http.HandlerFunc(resolved.handleApplyAgent)))
		mux.Handle(prefix+"/agents/{agentID}/revisions/rollback", resolved.requirePanelToken(http.HandlerFunc(resolved.handleRevisionRollback)))
		mux.Handle(prefix+"/agents/{agentID}/revisions/{revision}", resolved.requirePanelToken(http.HandlerFunc(resolved.handleAgentRevisionStatus)))
		mux.Handle(prefix+"/agents/{agentID}/revisions/{revision}/retry", resolved.requirePanelToken(http.HandlerFunc(resolved.handleRevisionRetry)))
		mux.Handle(prefix+"/operations/{operationID}", resolved.requirePanelToken(http.HandlerFunc(resolved.handleOperationStatus)))
		mux.Handle(prefix+"/operations/{operationID}/dismiss", resolved.requirePanelToken(http.HandlerFunc(resolved.handleOperationDismiss)))
		mux.Handle(prefix+"/revision-events", resolved.requirePanelToken(http.HandlerFunc(resolved.handleRevisionEvents)))
		mux.Handle(prefix+"/agents/{agentID}/traffic-policy", resolved.requirePanelToken(http.HandlerFunc(resolved.handleAgentTrafficPolicy)))
		mux.Handle(prefix+"/agents/{agentID}/traffic-summary", resolved.requirePanelToken(http.HandlerFunc(resolved.handleAgentTrafficSummary)))
		mux.Handle(prefix+"/agents/{agentID}/traffic-trend", resolved.requirePanelToken(http.HandlerFunc(resolved.handleAgentTrafficTrend)))
		mux.Handle(prefix+"/agents/{agentID}/traffic-calibration", resolved.requirePanelToken(http.HandlerFunc(resolved.handleAgentTrafficCalibration)))
		mux.Handle(prefix+"/agents/{agentID}/traffic-cleanup", resolved.requirePanelToken(http.HandlerFunc(resolved.handleAgentTrafficCleanup)))
		mux.Handle(prefix+"/traffic-overview", resolved.requirePanelToken(http.HandlerFunc(resolved.handleTrafficOverview)))
		mux.Handle(prefix+"/traffic-aggregate", resolved.requirePanelToken(http.HandlerFunc(resolved.handleTrafficAggregate)))
		mux.Handle(prefix+"/egress-profiles", resolved.requirePanelToken(http.HandlerFunc(resolved.handleEgressProfiles)))
		mux.Handle(prefix+"/egress-profiles/{id}", resolved.requirePanelToken(http.HandlerFunc(resolved.handleEgressProfile)))
		mux.Handle(prefix+"/agents/{agentID}/rules", resolved.requirePanelToken(http.HandlerFunc(resolved.handleAgentRules)))
		mux.Handle(prefix+"/agents/{agentID}/rules/{id}", resolved.requirePanelToken(http.HandlerFunc(resolved.handleAgentRule)))
		mux.Handle(prefix+"/agents/{agentID}/rules/{id}/diagnose", resolved.requirePanelToken(http.HandlerFunc(resolved.handleAgentRuleDiagnose)))
		mux.Handle(prefix+"/agents/{agentID}/l4-rules", resolved.requirePanelToken(http.HandlerFunc(resolved.handleAgentL4Rules)))
		mux.Handle(prefix+"/agents/{agentID}/l4-rules/{id}", resolved.requirePanelToken(http.HandlerFunc(resolved.handleAgentL4Rule)))
		mux.Handle(prefix+"/agents/{agentID}/l4-rules/{id}/diagnose", resolved.requirePanelToken(http.HandlerFunc(resolved.handleAgentL4RuleDiagnose)))
		mux.Handle(prefix+"/agents/{agentID}/tasks/{taskID}", resolved.requirePanelToken(http.HandlerFunc(resolved.handleAgentTask)))
		mux.Handle(prefix+"/agents/{agentID}/relay-listeners", resolved.requirePanelToken(http.HandlerFunc(resolved.handleRelayListeners)))
		mux.Handle(prefix+"/agents/{agentID}/relay-listeners/{id}", resolved.requirePanelToken(http.HandlerFunc(resolved.handleRelayListener)))
		mux.Handle(prefix+"/agents/{agentID}/certificates", resolved.requirePanelToken(http.HandlerFunc(resolved.handleCertificates)))
		mux.Handle(prefix+"/agents/{agentID}/certificates/{id}", resolved.requirePanelToken(http.HandlerFunc(resolved.handleCertificate)))
		mux.Handle(prefix+"/agents/{agentID}/certificates/{id}/issue", resolved.requirePanelToken(http.HandlerFunc(resolved.handleIssueCertificate)))
		mux.Handle(prefix+"/http-rules", resolved.requirePanelToken(http.HandlerFunc(resolved.handleHTTPRulesList)))
		mux.Handle(prefix+"/l4-rules", resolved.requirePanelToken(http.HandlerFunc(resolved.handleL4RulesList)))
		mux.Handle(prefix+"/relay-listeners", resolved.requirePanelToken(http.HandlerFunc(resolved.handleRelayListenersList)))
		mux.Handle(prefix+"/certificates", resolved.requirePanelToken(http.HandlerFunc(resolved.handleGlobalCertificates)))
		mux.Handle(prefix+"/certificates/{id}", resolved.requirePanelToken(http.HandlerFunc(resolved.handleGlobalCertificate)))
		mux.Handle(prefix+"/certificates/{id}/issue", resolved.requirePanelToken(http.HandlerFunc(resolved.handleIssueCertificate)))
		mux.Handle(prefix+"/rules", resolved.requirePanelToken(http.HandlerFunc(resolved.handleLocalRules)))
		mux.Handle(prefix+"/rules/{id}", resolved.requirePanelToken(http.HandlerFunc(resolved.handleLocalRule)))
		mux.Handle(prefix+"/stats", resolved.requirePanelToken(http.HandlerFunc(resolved.handleLocalStats)))
		mux.Handle(prefix+"/apply", resolved.requirePanelToken(http.HandlerFunc(resolved.handleLocalApply)))
		mux.Handle(prefix+"/version-policies", resolved.requirePanelToken(http.HandlerFunc(resolved.handleVersionPolicies)))
		mux.Handle(prefix+"/version-policies/{id}", resolved.requirePanelToken(http.HandlerFunc(resolved.handleVersionPolicy)))
	}
	mux.Handle("/", resolved.staticHandler())
	handler := resolved.withMutationContext(mux)

	if resolved.cleanup != nil {
		return closeableHandler{Handler: handler, close: resolved.cleanup}, nil
	}
	return handler, nil
}

type closeableHandler struct {
	http.Handler
	close func() error
}

func (h closeableHandler) Close() error {
	if h.close == nil {
		return nil
	}
	return h.close()
}

// joinCleanup composes two cleanup functions so both run on Close, joining
// their errors via errors.Join. Either argument may be nil.
func joinCleanup(prev, next func() error) func() error {
	switch {
	case prev == nil:
		return next
	case next == nil:
		return prev
	default:
		return func() error {
			return errors.Join(prev(), next())
		}
	}
}

func (d Dependencies) withDefaults() (Dependencies, error) {
	if d.RuleService == nil {
		if legacy, ok := any(d.AgentService).(legacyRuleListService); ok {
			d.RuleService = agentRuleServiceAdapter{agent: legacy}
		}
	}
	if d.RevisionService == nil {
		if provider, ok := d.AgentService.(interface{ RevisionAPI() *service.RevisionAPI }); ok {
			d.RevisionService = provider.RevisionAPI()
		}
	}

	if d.TaskService == nil && d.hasCoreServices() {
		// withDefaults owns this TaskService, so its background prune goroutine
		// must be stopped on cleanup alongside any store Close wired below.
		taskSvc := service.NewTaskService(service.TaskServiceConfig{})
		d.TaskService = taskSvc
		d.cleanup = joinCleanup(d.cleanup, taskSvc.Close)
	}

	if d.BackupService == nil && d.hasCoreServices() && d.TaskService != nil {
		d.BackupService = unavailableBackupService{}
	}

	canOpenOwnedStore := d.canOpenConfiguredStore()
	if !canOpenOwnedStore {
		if d.EgressProfileService == nil && d.hasCoreServices() {
			d.EgressProfileService = unavailableEgressProfileService{}
		}
	}

	needsOwnedStore := !d.hasCoreServices() || d.TrafficService == nil || (canOpenOwnedStore && d.EgressProfileService == nil)
	if !needsOwnedStore && d.TaskService != nil && d.BackupService != nil && d.EgressProfileService != nil {
		return d, nil
	}
	if d.hasCoreServices() && d.TaskService != nil && d.BackupService != nil && d.EgressProfileService != nil && d.TrafficService == nil && !d.Config.TrafficStatsEnabled {
		d.TrafficService = unavailableTrafficService{}
		return d, nil
	}

	store, err := openConfiguredStore(d.Config)
	if err != nil {
		return Dependencies{}, err
	}
	d.cleanup = joinCleanup(d.cleanup, store.Close)

	if d.SystemService == nil {
		d.SystemService = service.NewSystemService(d.Config)
	}
	if d.AgentService == nil {
		d.AgentService = service.NewAgentService(d.Config, store)
	}
	if d.RuleService == nil {
		d.RuleService = service.NewRuleService(d.Config, store)
	}
	if d.L4RuleService == nil {
		d.L4RuleService = service.NewL4RuleService(d.Config, store)
	}
	if d.VersionPolicyService == nil {
		d.VersionPolicyService = service.NewVersionPolicyService(store)
	}
	if d.EgressProfileService == nil {
		d.EgressProfileService = service.NewEgressProfileServiceWithConfig(d.Config, store)
	}
	if d.RelayListenerService == nil {
		d.RelayListenerService = service.NewRelayListenerService(d.Config, store)
	}
	if d.CertificateService == nil {
		d.CertificateService = service.NewCertificateService(d.Config, store)
	}
	if d.TaskService == nil {
		// withDefaults owns this TaskService; stop its background prune goroutine
		// on cleanup together with the store Close wired above.
		taskSvc := service.NewTaskService(service.TaskServiceConfig{})
		d.TaskService = taskSvc
		d.cleanup = joinCleanup(d.cleanup, taskSvc.Close)
	}
	if d.BackupService == nil {
		d.BackupService = service.NewBackupService(d.Config, store)
	}
	if d.TrafficService == nil {
		trafficCfg, err := service.NewTrafficServiceConfig(d.Config.TrafficStatsEnabled, d.Config.Timezone)
		if err != nil {
			return Dependencies{}, err
		}
		d.TrafficService = service.NewTrafficService(trafficCfg, store)
	}
	if d.RevisionService == nil {
		if provider, ok := d.AgentService.(interface{ RevisionAPI() *service.RevisionAPI }); ok {
			d.RevisionService = provider.RevisionAPI()
		}
	}

	return d, nil
}

func (d Dependencies) hasCoreServices() bool {
	return d.SystemService != nil &&
		d.AgentService != nil &&
		d.RuleService != nil &&
		d.L4RuleService != nil &&
		d.VersionPolicyService != nil &&
		d.RelayListenerService != nil &&
		d.CertificateService != nil
}

func (d Dependencies) canOpenConfiguredStore() bool {
	driver := d.Config.DatabaseDriver
	if driver == "" {
		driver = "sqlite"
	}
	if d.Config.DatabaseDSN != "" {
		return true
	}
	return driver == "sqlite" && d.Config.DataDir != ""
}

func mapServiceError(err error) (int, map[string]any) {
	var trafficErr service.TrafficServiceError
	if status := revision.HTTPStatus(err); status != http.StatusInternalServerError && status != 0 {
		return status, errorPayload(err.Error())
	}
	switch {
	case errors.As(err, &trafficErr) && trafficErr.Code == service.ErrCodeTrafficStatsDisabled:
		return http.StatusNotFound, trafficStatsDisabledPayload()
	case errors.Is(err, service.ErrTrafficStatsDisabled):
		return http.StatusNotFound, trafficStatsDisabledPayload()
	case errors.Is(err, service.ErrAgentUnauthorized):
		return http.StatusUnauthorized, errorPayload("Unauthorized: missing agent token")
	case errors.Is(err, service.ErrRevisionForbidden):
		return http.StatusForbidden, errorPayload(err.Error())
	case errors.Is(err, service.ErrRevisionNotFound), errors.Is(err, coordinator.ErrNotFound):
		return http.StatusNotFound, errorPayload(err.Error())
	case errors.Is(err, coordinator.ErrLeaseConflict):
		return http.StatusConflict, revisionErrorPayload(err.Error(), "revision_lease_conflict")
	case errors.Is(err, coordinator.ErrStateConflict):
		return http.StatusConflict, errorPayload(err.Error())
	case errors.Is(err, service.ErrConflict):
		return http.StatusConflict, errorPayload(err.Error())
	case errors.Is(err, service.ErrAgentNotFound):
		return http.StatusNotFound, errorPayload("agent not found")
	case errors.Is(err, service.ErrRuleNotFound):
		return http.StatusNotFound, errorPayload("rule id not found")
	case errors.Is(err, service.ErrVersionPolicyNotFound):
		return http.StatusNotFound, errorPayload("version policy not found")
	case errors.Is(err, service.ErrEgressProfileNotFound):
		return http.StatusNotFound, errorPayload("egress profile not found")
	case errors.Is(err, service.ErrRelayListenerNotFound):
		return http.StatusNotFound, errorPayload("relay listener not found")
	case errors.Is(err, service.ErrCertificateNotFound):
		return http.StatusNotFound, errorPayload("certificate not found")
	case errors.Is(err, service.ErrTaskNotFound):
		return http.StatusNotFound, errorPayload("task not found")
	case errors.Is(err, service.ErrL4Unsupported):
		return http.StatusBadRequest, errorPayload("agent does not support L4 rules")
	case errors.Is(err, service.ErrInvalidArgument):
		return http.StatusBadRequest, errorPayload(err.Error())
	default:
		return http.StatusInternalServerError, errorPayload("internal server error")
	}
}

func revisionErrorPayload(message, code string) map[string]any {
	payload := errorPayload(message)
	payload["code"] = code
	return payload
}

func trafficStatsDisabledPayload() map[string]any {
	return map[string]any{
		"error": service.ErrTrafficStatsDisabled.Error(),
		"code":  service.ErrCodeTrafficStatsDisabled,
	}
}
