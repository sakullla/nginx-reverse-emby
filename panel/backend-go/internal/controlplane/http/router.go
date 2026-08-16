package http

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-git/go-git/v5/plumbing/transport"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/authz"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/coordinator"
	marketplacepkg "github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/marketplace"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/plugins"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/revision"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/secrets"
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

type PKIService interface {
	service.PKIAPIService
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

type MarketplaceAPI interface {
	ListSources(context.Context) ([]marketplacepkg.Source, error)
	Source(context.Context, string) (marketplacepkg.Source, error)
	CurrentCatalog(context.Context, string) (service.MarketplaceCatalog, error)
	AddCustomSource(context.Context, string, string, string, string, string, time.Duration, marketplacepkg.SourceSigner) (marketplacepkg.Source, error)
	AddGitRepositorySource(context.Context, string, string, string, string, string, string, string, time.Duration, marketplacepkg.SourceSigner) (marketplacepkg.Source, error)
	UpdateGitRepositorySource(context.Context, marketplacepkg.Source, uint64) (marketplacepkg.Source, error)
	DeleteSource(context.Context, string) error
	Refresh(context.Context, string) (marketplacepkg.Snapshot, error)
	ResolvePackage(context.Context, string, string, string, string) (service.PluginPackageCandidate, error)
	AuditSourceFailure(context.Context, string, string, string) error
}

type PluginAPI interface {
	List(context.Context) ([]service.PluginSummary, error)
	Detail(context.Context, string) (service.PluginDetail, error)
	PackageDetail(context.Context, service.PluginPackageCandidate, string) (service.PluginPackageDetail, error)
	InstallMutation(context.Context, service.PluginInstallRequest) (service.PluginSummary, error)
	EnableMutation(context.Context, string, string) (service.PluginSummary, error)
	DisableMutation(context.Context, string, string) (service.PluginSummary, error)
	ConfigureMutation(context.Context, service.PluginConfigureRequest) (service.PluginInstanceDetail, error)
	DeleteInstanceMutation(context.Context, service.PluginDeleteInstanceRequest) error
	UpgradeMutation(context.Context, service.PluginUpgradeRequest) (service.PluginSummary, error)
	RollbackMutation(context.Context, service.PluginRollbackRequest) (service.PluginSummary, error)
	Uninstall(context.Context, service.PluginUninstallRequest) error
	Operations(context.Context, string) ([]service.PluginOperationDetail, error)
}

type AgentPluginArtifactService interface {
	ResolveAgentPluginArtifact(context.Context, string, int64, string, string) (service.AgentPluginArtifact, error)
}

type AgentPluginSecretService interface {
	RedeemAgentPluginSecrets(context.Context, string, service.PluginSecretRedemptionRequest) (service.PluginSecretRedemptionResponse, error)
}

type PluginCapabilityAPI interface {
	InvokeDynamicAction(context.Context, service.PluginDynamicActionRequest) (service.PluginDynamicActionResult, error)
}

type HTTPBackendProviderAPI interface {
	ListHTTPBackendProvidersForActor(context.Context, string, authz.Actor) ([]service.HTTPBackendProvider, error)
	HTTPBackendProviderForActor(context.Context, string, string, string, authz.Actor) (service.HTTPBackendProvider, error)
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
	PKIService                   PKIService
	TrafficService               TrafficService
	RevisionService              RevisionService
	MarketplaceService           MarketplaceAPI
	PluginService                PluginAPI
	PluginArtifactService        AgentPluginArtifactService
	PluginSecretService          AgentPluginSecretService
	PluginCapabilityService      PluginCapabilityAPI
	PluginRuntimeHost            *service.PluginRuntimeHost
	AccessManager                *authz.Manager
	SecretVault                  *secrets.Vault
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
		mux.Handle(prefix+"/auth/login", http.HandlerFunc(resolved.handleLogin))
		mux.Handle(prefix+"/auth/verify", http.HandlerFunc(resolved.handleVerify))
		mux.Handle(prefix+"/auth/me", resolved.requirePanelToken(http.HandlerFunc(resolved.handleMe)))
		mux.Handle(prefix+"/auth/logout", resolved.requirePanelToken(http.HandlerFunc(resolved.handleLogout)))
		mux.Handle(prefix+"/access/users", resolved.requirePanelToken(http.HandlerFunc(resolved.handleAccessUsers)))
		mux.Handle(prefix+"/access/users/{id}", resolved.requirePanelToken(http.HandlerFunc(resolved.handleAccessUser)))
		mux.Handle(prefix+"/access/permissions", resolved.requirePanelToken(http.HandlerFunc(resolved.handleAccessPermissions)))
		mux.Handle(prefix+"/access/roles", resolved.requirePanelToken(http.HandlerFunc(resolved.handleAccessRoles)))
		mux.Handle(prefix+"/access/roles/{id}", resolved.requirePanelToken(http.HandlerFunc(resolved.handleAccessRole)))
		mux.Handle(prefix+"/access/resource-groups", resolved.requirePanelToken(http.HandlerFunc(resolved.handleResourceGroups)))
		mux.Handle(prefix+"/access/resource-group-grants", resolved.requirePanelToken(http.HandlerFunc(resolved.handleResourceGroupGrants)))
		mux.Handle(prefix+"/access/resource-bindings", resolved.requirePanelToken(http.HandlerFunc(resolved.handleResourceBindings)))
		mux.Handle(prefix+"/access/quota-policies", resolved.requirePanelToken(http.HandlerFunc(resolved.handleQuotaPolicies)))
		mux.Handle(prefix+"/access/audit-events", resolved.requirePanelToken(http.HandlerFunc(resolved.handleAuditEvents)))
		mux.Handle(prefix+"/access/secrets", resolved.requirePanelToken(http.HandlerFunc(resolved.handleSecrets)))
		mux.Handle(prefix+"/access/secrets/{id}", resolved.requirePanelToken(http.HandlerFunc(resolved.handleSecret)))
		mux.Handle(prefix+"/access/secrets/{id}/rotate", resolved.requirePanelToken(http.HandlerFunc(resolved.handleSecretRotate)))
		mux.Handle(prefix+"/info", resolved.requirePanelToken(http.HandlerFunc(resolved.handleInfo)))
		mux.Handle(prefix+"/public/join-agent.sh", http.HandlerFunc(resolved.handleJoinAgentScript))
		mux.Handle(prefix+"/public/agent-assets/", http.HandlerFunc(resolved.handlePublicAgentAsset))
		mux.Handle(prefix+"/agents/register", http.HandlerFunc(resolved.handleRegisterAgent))
		mux.Handle(prefix+"/agents/heartbeat", http.HandlerFunc(resolved.handleHeartbeat))
		mux.Handle(prefix+"/agent-revisions/pull", http.HandlerFunc(resolved.handleRemoteRevisionPull))
		mux.Handle(prefix+"/agent-revisions/{revision}/start", http.HandlerFunc(resolved.handleRemoteRevisionStart))
		mux.Handle(prefix+"/agent-revisions/{revision}/report", http.HandlerFunc(resolved.handleRemoteRevisionReport))
		mux.Handle(prefix+"/agent-plugin-artifacts/{artifactID}", http.HandlerFunc(resolved.handleAgentPluginArtifact))
		mux.Handle(prefix+"/agent-plugin-secrets/redeem", http.HandlerFunc(resolved.handleAgentPluginSecretRedemption))
		mux.Handle(prefix+"/agents/task-session", http.HandlerFunc(resolved.handleAgentTaskSession))
		mux.Handle(prefix+"/agents/task-stream", http.HandlerFunc(resolved.handleAgentTaskStream))
		mux.Handle(prefix+"/agent-tasks/{taskID}/updates", http.HandlerFunc(resolved.handleAgentTaskUpdate))
		if resolved.BackupService != nil {
			mux.Handle(prefix+"/system/backup/export", resolved.requirePanelToken(http.HandlerFunc(resolved.handleBackupExport)))
			mux.Handle(prefix+"/system/backup/import", resolved.requirePanelToken(http.HandlerFunc(resolved.handleBackupImport)))
			mux.Handle(prefix+"/system/backup/import/preview", resolved.requirePanelToken(http.HandlerFunc(resolved.handleBackupImportPreview)))
			mux.Handle(prefix+"/system/backup/counts", resolved.requirePanelToken(http.HandlerFunc(resolved.handleBackupResourceCounts)))
		}
		mux.Handle(prefix+"/pki/overview", resolved.requirePanelToken(http.HandlerFunc(resolved.handlePKIOverview)))
		mux.Handle(prefix+"/pki/authorities", resolved.requirePanelToken(http.HandlerFunc(resolved.handlePKIAuthorities)))
		mux.Handle(prefix+"/pki/identities", resolved.requirePanelToken(http.HandlerFunc(resolved.handlePKIIdentities)))
		mux.Handle(prefix+"/pki/certificates", resolved.requirePanelToken(http.HandlerFunc(resolved.handlePKICertificates)))
		mux.Handle(prefix+"/pki/events", resolved.requirePanelToken(http.HandlerFunc(resolved.handlePKIEvents)))
		mux.Handle(prefix+"/pki/alerts", resolved.requirePanelToken(http.HandlerFunc(resolved.handlePKIAlerts)))
		mux.Handle(prefix+"/pki/enrollment-tokens", resolved.requirePanelToken(http.HandlerFunc(resolved.handlePKIEnrollmentTokens)))
		mux.Handle(prefix+"/pki/confirmations", resolved.requirePanelToken(http.HandlerFunc(resolved.handlePKIConfirmations)))
		mux.Handle(prefix+"/pki/identities/{identityID}/revoke", resolved.requirePanelToken(http.HandlerFunc(resolved.handlePKIRevoke)))
		mux.Handle(prefix+"/pki/identities/{identityID}/force-rotate", resolved.requirePanelToken(http.HandlerFunc(resolved.handlePKIForceRotate)))
		mux.Handle(prefix+"/pki/authorities/rotate", resolved.requirePanelToken(http.HandlerFunc(resolved.handlePKIRotateCA)))
		mux.Handle(prefix+"/pki/authorities/emergency-rotate", resolved.requirePanelToken(http.HandlerFunc(resolved.handlePKIEmergencyRotateCA)))
		mux.Handle(prefix+"/pki/backups/export", resolved.requirePanelToken(http.HandlerFunc(resolved.handlePKIProtectedExport)))
		mux.Handle(prefix+"/pki/backups/import", resolved.requirePanelToken(http.HandlerFunc(resolved.handlePKIProtectedImport)))
		mux.Handle(prefix+"/pki/activation", resolved.requirePanelToken(http.HandlerFunc(resolved.handlePKIActivation)))
		mux.Handle(prefix+"/pki/operations/{operationID}", resolved.requirePanelToken(http.HandlerFunc(resolved.handlePKIOperation)))
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
		mux.Handle(prefix+"/dashboard/attention", resolved.requirePanelToken(http.HandlerFunc(resolved.handleDashboardAttention)))
		mux.Handle(prefix+"/traffic-overview", resolved.requirePanelToken(http.HandlerFunc(resolved.handleTrafficOverview)))
		mux.Handle(prefix+"/traffic-aggregate", resolved.requirePanelToken(http.HandlerFunc(resolved.handleTrafficAggregate)))
		mux.Handle(prefix+"/egress-profiles", resolved.requirePanelToken(http.HandlerFunc(resolved.handleEgressProfiles)))
		mux.Handle(prefix+"/egress-profiles/{id}", resolved.requirePanelToken(http.HandlerFunc(resolved.handleEgressProfile)))
		mux.Handle(prefix+"/agents/{agentID}/rules", resolved.requirePanelToken(http.HandlerFunc(resolved.handleAgentRules)))
		mux.Handle(prefix+"/agents/{agentID}/rules/{id}", resolved.requirePanelToken(http.HandlerFunc(resolved.handleAgentRule)))
		mux.Handle(prefix+"/agents/{agentID}/http-backend-providers", resolved.requirePanelToken(http.HandlerFunc(resolved.handleHTTPBackendProviders)))
		mux.Handle(prefix+"/agents/{agentID}/http-backend-providers/{instanceID}/{providerID}", resolved.requirePanelToken(http.HandlerFunc(resolved.handleHTTPBackendProvider)))
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
		if resolved.MarketplaceService != nil {
			mux.Handle(prefix+"/marketplace/sources", resolved.requirePanelToken(http.HandlerFunc(resolved.handleMarketplaceSources)))
			mux.Handle(prefix+"/marketplace/sources/{id}", resolved.requirePanelToken(http.HandlerFunc(resolved.handleMarketplaceSource)))
			mux.Handle(prefix+"/marketplace/sources/{id}/entries", resolved.requirePanelToken(http.HandlerFunc(resolved.handleMarketplaceEntries)))
			mux.Handle(prefix+"/marketplace/sources/{id}/refresh", resolved.requirePanelToken(http.HandlerFunc(resolved.handleMarketplaceRefresh)))
		}
		if resolved.PluginService != nil && resolved.MarketplaceService != nil {
			mux.Handle(prefix+"/plugins", resolved.requirePanelToken(http.HandlerFunc(resolved.handlePlugins)))
			mux.Handle(prefix+"/plugins/package-detail", resolved.requirePanelToken(http.HandlerFunc(resolved.handlePluginPackageDetail)))
			mux.Handle(prefix+"/plugins/install", resolved.requirePanelToken(http.HandlerFunc(resolved.handlePluginInstall)))
			mux.Handle(prefix+"/plugins/{id}", resolved.requirePanelToken(http.HandlerFunc(resolved.handlePlugin)))
			mux.Handle(prefix+"/plugins/{id}/operations", resolved.requirePanelToken(http.HandlerFunc(resolved.handlePluginOperations)))
			mux.Handle(prefix+"/plugins/{id}/instances/{instance}", resolved.requirePanelToken(http.HandlerFunc(resolved.handlePluginInstance)))
			mux.Handle(prefix+"/plugins/{id}/instances/{instance}/logs", resolved.requirePanelToken(http.HandlerFunc(resolved.handlePluginLogs)))
			if resolved.PluginCapabilityService != nil {
				mux.Handle(prefix+"/plugins/{id}/instances/{instance}/actions/{action}", resolved.requirePanelToken(http.HandlerFunc(resolved.handlePluginDynamicAction)))
			}
			mux.Handle(prefix+"/plugins/{id}/{action}", resolved.requirePanelToken(http.HandlerFunc(resolved.handlePluginAction)))
		}
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

// joinDependentCleanup runs next only after prev confirms that its dependent
// background work has quiesced. A failed prev can be retried without closing
// resources that the still-running work may access.
func joinDependentCleanup(prev, next func() error) func() error {
	if prev == nil {
		return next
	}
	if next == nil {
		return prev
	}
	return func() error {
		if err := prev(); err != nil {
			return err
		}
		return next()
	}
}

func (d Dependencies) withDefaults() (Dependencies, error) {
	if (d.PluginService == nil) != (d.MarketplaceService == nil) {
		return Dependencies{}, errors.New("plugin and marketplace services must be provided together")
	}
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

	needsOwnedStore := !d.hasCoreServices() || d.TrafficService == nil || (canOpenOwnedStore && (d.EgressProfileService == nil || d.PluginService == nil || d.MarketplaceService == nil))
	if !needsOwnedStore && d.TaskService != nil && d.BackupService != nil && d.EgressProfileService != nil && (!canOpenOwnedStore || (d.PluginService != nil && d.MarketplaceService != nil)) {
		return d, nil
	}
	if d.hasCoreServices() && d.TaskService != nil && d.BackupService != nil && d.EgressProfileService != nil && d.TrafficService == nil && !d.Config.TrafficStatsEnabled && (!canOpenOwnedStore || (d.PluginService != nil && d.MarketplaceService != nil)) {
		d.TrafficService = unavailableTrafficService{}
		return d, nil
	}

	store, err := openConfiguredStore(d.Config)
	if err != nil {
		return Dependencies{}, err
	}
	d.cleanup = joinCleanup(d.cleanup, store.Close)
	initializing := true
	defer func() {
		if initializing && d.cleanup != nil {
			_ = d.cleanup()
		}
	}()
	if d.AccessManager == nil && store.SecurityStoreAvailable() {
		d.AccessManager = authz.NewManager(store, authz.Options{})
		if err := d.AccessManager.EnsureDefaults(context.Background()); err != nil {
			return Dependencies{}, fmt.Errorf("bootstrap access control: %w", err)
		}
	}
	if d.SecretVault == nil && store.SecurityStoreAvailable() {
		if keyring, keyErr := secrets.KeyringFromEnvironment(); keyErr == nil {
			vault, vaultErr := secrets.NewVault(store, keyring)
			if vaultErr != nil {
				return Dependencies{}, fmt.Errorf("initialize secret vault: %w", vaultErr)
			}
			if _, migrateErr := vault.MigrateToCurrentKey(context.Background()); migrateErr != nil {
				return Dependencies{}, fmt.Errorf("migrate secret vault key: %w", migrateErr)
			}
			d.SecretVault = vault
		} else if !errors.Is(keyErr, secrets.ErrKeyNotConfigured) {
			return Dependencies{}, fmt.Errorf("load secret vault key: %w", keyErr)
		}
	}

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
	runtimeVersion, versionErr := plugins.NormalizeBuildVersion(d.Config.AppVersion)
	if versionErr != nil {
		return Dependencies{}, fmt.Errorf("initialize plugin compatibility: %w", versionErr)
	}
	if localBuild, ok := d.AgentService.(interface{ EnsureLocalAgentBuild(context.Context) error }); ok {
		if err := localBuild.EnsureLocalAgentBuild(context.Background()); err != nil {
			return Dependencies{}, fmt.Errorf("persist local agent build identity: %w", err)
		}
	}
	// Package validation enforces host compatibility here. Concrete Agent
	// compatibility is checked per target from durable Agent reports.
	validator := plugins.NewValidator(plugins.ValidatorOptions{HostVersion: runtimeVersion})
	sourceValidators := marketplacepkg.NewSourceValidatorFactory(plugins.ValidatorOptions{HostVersion: runtimeVersion})
	cacheRoot := filepath.Join(d.Config.DataDir, "plugins", "packages")
	if d.PluginService == nil {
		pluginService := service.NewPluginServiceWithValidator(store, validator, cacheRoot)
		pluginService.SetSecretVault(d.SecretVault)
		if err := pluginService.MigrateLegacyWriteOnlySecrets(context.Background()); err != nil {
			return Dependencies{}, fmt.Errorf("migrate legacy plugin writeOnly secrets: %w", err)
		}
		pluginService.ConfigureRevisionMutations(d.Config, store)
		if revisionAPI, ok := d.RevisionService.(*service.RevisionAPI); ok {
			reconciler, reconcileErr := service.NewPluginLifecycleReconciler(store, pluginService)
			if reconcileErr != nil {
				return Dependencies{}, fmt.Errorf("initialize plugin lifecycle reconciler: %w", reconcileErr)
			}
			if d.PluginRuntimeHost != nil {
				reconciler.SetControlPlaneRuntime(d.PluginRuntimeHost)
			}
			revisionAPI.SetPluginLifecycleReconciler(reconciler)
		}
		d.PluginService = pluginService
		d.PluginArtifactService = pluginService
		d.PluginSecretService = pluginService
	} else if d.PluginArtifactService == nil {
		if artifactService, ok := d.PluginService.(AgentPluginArtifactService); ok {
			d.PluginArtifactService = artifactService
		}
	}
	if d.PluginSecretService == nil {
		if secretService, ok := d.PluginService.(AgentPluginSecretService); ok {
			d.PluginSecretService = secretService
		}
	}
	if d.PluginCapabilityService == nil && d.PluginRuntimeHost != nil && d.AccessManager != nil {
		pluginService, ok := d.PluginService.(*service.PluginService)
		if !ok {
			return Dependencies{}, errors.New("plugin capability manager requires the production plugin service")
		}
		if err := store.EnsurePluginCapabilityDockerSocketBinding(context.Background(), authz.DefaultResourceGroup, storage.PluginCapabilityDockerSocketPath); err != nil {
			return Dependencies{}, fmt.Errorf("register local Docker capability endpoint: %w", err)
		}
		manager, managerErr := service.NewPluginCapabilityManager(store, d.AccessManager, d.PluginRuntimeHost, pluginService)
		if managerErr != nil {
			return Dependencies{}, fmt.Errorf("initialize plugin capability manager: %w", managerErr)
		}
		d.PluginCapabilityService = manager
		manager.SetTrafficSummaryProvider(d.TrafficService)
		d.PluginRuntimeHost.SetCapabilityRevoker(manager)
		if d.SecretVault != nil {
			manager.SetCoreResourceVault(d.SecretVault)
			d.SecretVault.SetPluginCapabilityTargetRevoker(manager)
		}
	}
	if d.MarketplaceService == nil {
		cache, cacheErr := marketplacepkg.NewVerifiedCache(cacheRoot, validator, store)
		if cacheErr != nil {
			return Dependencies{}, fmt.Errorf("initialize plugin package cache: %w", cacheErr)
		}
		fetcher := marketplacepkg.GoGitFetcher{}
		if d.SecretVault != nil {
			fetcher.ResolveCredential = trustedMarketplaceCredentialResolver(d.SecretVault)
		}
		officialLockPath, lockPathErr := marketplacepkg.ResolveOfficialMarketLockPath(os.Getenv(marketplacepkg.OfficialMarketLockPathEnv))
		if lockPathErr != nil {
			return Dependencies{}, fmt.Errorf("resolve official marketplace lock: %w", lockPathErr)
		}
		manager, managerErr := marketplacepkg.NewManagerWithOfficialLock(filepath.Join(d.Config.DataDir, "marketplace"), fetcher, validator, cache, store, sourceValidators, officialLockPath)
		if managerErr != nil {
			return Dependencies{}, fmt.Errorf("initialize marketplace manager: %w", managerErr)
		}
		marketplaceService := service.NewMarketplaceServiceWithSourceValidators(store, manager, validator, cacheRoot, sourceValidators)
		d.MarketplaceService = marketplaceService
		scheduler, schedulerErr := service.NewMarketplaceSchedulerWithSourceTimeout(marketplaceService, trustedMarketplaceSchedulerContext(d.SecretVault), 30*time.Second, d.Config.MarketplaceRefreshTimeout)
		if schedulerErr != nil {
			return Dependencies{}, fmt.Errorf("initialize marketplace scheduler: %w", schedulerErr)
		}
		scheduler.Start(context.Background())
		// Stop the scheduler before closing the store it uses, and retain the
		// owned store when scheduler shutdown must be retried.
		d.cleanup = joinDependentCleanup(scheduler.Close, d.cleanup)
	}

	initializing = false
	return d, nil
}

func trustedMarketplaceSchedulerContext(vault *secrets.Vault) func(context.Context, marketplacepkg.Source) (context.Context, error) {
	return func(ctx context.Context, source marketplacepkg.Source) (context.Context, error) {
		correlationID := fmt.Sprintf("marketplace-scheduler:%s:%d", source.ID, time.Now().UTC().UnixNano())
		actor := marketplacepkg.OperationActor{ActorID: "system.marketplace.scheduler", SessionID: "service", CorrelationID: correlationID}
		ctx = storage.WithQuotaActor(ctx, storage.QuotaActor{UserID: actor.ActorID, SessionID: actor.SessionID, CorrelationID: actor.CorrelationID, Bootstrap: true})
		if source.CredentialRef == "" {
			return ctx, nil
		}
		if vault == nil {
			return ctx, errors.New("marketplace scheduler credential vault is unavailable")
		}
		metadata, err := vault.Get(ctx, source.CredentialRef)
		if err != nil {
			return ctx, err
		}
		if metadata.Purpose != marketplacepkg.CredentialPurpose {
			return ctx, errors.New("marketplace scheduler credential has an invalid purpose")
		}
		return marketplacepkg.WithCredentialAuthorization(ctx, marketplacepkg.CredentialAuthorization{SecretID: source.CredentialRef, ResourceGroupID: metadata.ResourceGroupID, Actor: actor}), nil
	}
}

func trustedMarketplaceCredentialResolver(vault *secrets.Vault) marketplacepkg.CredentialResolver {
	return func(ctx context.Context, secretID string) (transport.AuthMethod, error) {
		metadata, err := vault.Get(ctx, secretID)
		if err != nil {
			return nil, err
		}
		if metadata.Purpose != marketplacepkg.CredentialPurpose {
			return nil, errors.New("marketplace credential has an invalid purpose")
		}
		authorization, ok := marketplacepkg.CredentialAuthorizationFromContext(ctx, secretID)
		if !ok || authorization.ResourceGroupID != metadata.ResourceGroupID {
			return nil, errors.New("marketplace credential authorization is missing or stale")
		}
		plaintext, err := vault.Resolve(ctx, secrets.OperationContext{ActorID: authorization.Actor.ActorID, SessionID: authorization.Actor.SessionID, CorrelationID: authorization.Actor.CorrelationID, ResourceGroupID: authorization.ResourceGroupID}, secretID)
		if err != nil {
			return nil, err
		}
		defer func() {
			for index := range plaintext {
				plaintext[index] = 0
			}
		}()
		return &githttp.BasicAuth{Username: "git", Password: string(plaintext)}, nil
	}
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
	case errors.Is(err, authz.ErrAuditUnavailable), errors.Is(err, secrets.ErrAuditUnavailable):
		return http.StatusServiceUnavailable, errorPayloadCode("audit_unavailable", "security audit persistence is unavailable")
	case errors.Is(err, storage.ErrQuotaExceeded):
		return http.StatusTooManyRequests, quotaErrorPayload(err)
	case errors.As(err, &trafficErr) && trafficErr.Code == service.ErrCodeTrafficStatsDisabled:
		return http.StatusNotFound, trafficStatsDisabledPayload()
	case errors.Is(err, service.ErrTrafficStatsDisabled):
		return http.StatusNotFound, trafficStatsDisabledPayload()
	case errors.Is(err, service.ErrAgentUnauthorized):
		return http.StatusUnauthorized, errorPayload("Unauthorized: missing agent token")
	case errors.Is(err, service.ErrPKIEnrollmentTokenRejected):
		return http.StatusUnauthorized, errorPayload("Unauthorized: invalid or expired enrollment token")
	case errors.Is(err, service.ErrPKILeaseNotHeld), errors.Is(err, service.ErrPKIEnrollmentAuthorityUnavailable),
		errors.Is(err, service.ErrPKIRuntimeUnavailable):
		return http.StatusServiceUnavailable, errorPayload("internal PKI signing is temporarily unavailable")
	case errors.Is(err, service.ErrPKIEpochStale):
		return http.StatusConflict, revisionErrorPayload(err.Error(), "pki_security_version_conflict")
	case errors.Is(err, service.ErrPKIEnrollmentTokenRequest), errors.Is(err, service.ErrPKIEnrollmentRequest),
		errors.Is(err, service.ErrPKIEnrollmentCSR), errors.Is(err, service.ErrPKIEnrollmentOwnerMismatch),
		errors.Is(err, service.ErrPKIEnrollmentPublicKeyReuse):
		return http.StatusBadRequest, errorPayload(err.Error())
	case errors.Is(err, service.ErrRevisionForbidden):
		return http.StatusForbidden, errorPayload(err.Error())
	case errors.Is(err, service.ErrRevisionNotFound), errors.Is(err, coordinator.ErrNotFound):
		return http.StatusNotFound, errorPayload(err.Error())
	case errors.Is(err, coordinator.ErrLeaseConflict):
		return http.StatusConflict, revisionErrorPayload(err.Error(), "revision_lease_conflict")
	case errors.Is(err, coordinator.ErrStateConflict):
		return http.StatusConflict, errorPayload(err.Error())
	case errors.Is(err, service.ErrPKIOperationNotFound):
		return http.StatusNotFound, errorPayload("PKI operation not found")
	case errors.Is(err, service.ErrPKILifecycleConflict):
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
