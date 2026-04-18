package http

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
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
}

type RuleService interface {
	List(context.Context, string) ([]service.HTTPRule, error)
	Get(context.Context, string, int) (service.HTTPRule, error)
	Create(context.Context, string, service.HTTPRuleInput) (service.HTTPRule, error)
	Update(context.Context, string, int, service.HTTPRuleInput) (service.HTTPRule, error)
	Delete(context.Context, string, int) (service.HTTPRule, error)
}

type L4RuleService interface {
	List(context.Context, string) ([]service.L4Rule, error)
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

type RelayListenerService interface {
	List(context.Context, string) ([]service.RelayListener, error)
	Create(context.Context, string, service.RelayListenerInput) (service.RelayListener, error)
	Update(context.Context, string, int, service.RelayListenerInput) (service.RelayListener, error)
	Delete(context.Context, string, int) (service.RelayListener, error)
}

type CertificateService interface {
	List(context.Context, string) ([]service.ManagedCertificate, error)
	Create(context.Context, string, service.ManagedCertificateInput) (service.ManagedCertificate, error)
	Update(context.Context, string, int, service.ManagedCertificateInput) (service.ManagedCertificate, error)
	Delete(context.Context, string, int) (service.ManagedCertificate, error)
	Issue(context.Context, string, int) (service.ManagedCertificate, error)
}

type BackupService interface {
	Export(context.Context) ([]byte, string, error)
	Import(context.Context, []byte) (service.BackupImportResult, error)
}

type Dependencies struct {
	Config               config.Config
	SystemService        SystemService
	AgentService         AgentService
	RuleService          RuleService
	L4RuleService        L4RuleService
	VersionPolicyService VersionPolicyService
	RelayListenerService RelayListenerService
	CertificateService   CertificateService
	TaskService          TaskService
	BackupService        BackupService
}

type legacyRuleListService interface {
	ListHTTPRules(context.Context, string) ([]service.HTTPRule, error)
}

type agentRuleServiceAdapter struct {
	agent legacyRuleListService
}

type unavailableBackupService struct{}

func (unavailableBackupService) Export(context.Context) ([]byte, string, error) {
	return nil, "", fmt.Errorf("backup service unavailable")
}

func (unavailableBackupService) Import(context.Context, []byte) (service.BackupImportResult, error) {
	return service.BackupImportResult{}, fmt.Errorf("backup service unavailable")
}

func (a agentRuleServiceAdapter) List(ctx context.Context, agentID string) ([]service.HTTPRule, error) {
	return a.agent.ListHTTPRules(ctx, agentID)
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
		mux.Handle(prefix+"/info", http.HandlerFunc(resolved.handleInfo))
		mux.Handle(prefix+"/public/join-agent.sh", http.HandlerFunc(resolved.handleJoinAgentScript))
		mux.Handle(prefix+"/public/agent-assets/", http.HandlerFunc(resolved.handlePublicAgentAsset))
		mux.Handle(prefix+"/agents/register", http.HandlerFunc(resolved.handleRegisterAgent))
		mux.Handle(prefix+"/agents/heartbeat", http.HandlerFunc(resolved.handleHeartbeat))
		mux.Handle(prefix+"/agents/task-session", http.HandlerFunc(resolved.handleAgentTaskSession))
		mux.Handle(prefix+"/agent-tasks/{taskID}/updates", http.HandlerFunc(resolved.handleAgentTaskUpdate))
		if resolved.BackupService != nil {
			mux.Handle(prefix+"/system/backup/export", resolved.requirePanelToken(http.HandlerFunc(resolved.handleBackupExport)))
			mux.Handle(prefix+"/system/backup/import", resolved.requirePanelToken(http.HandlerFunc(resolved.handleBackupImport)))
		}
		mux.Handle(prefix+"/agents", resolved.requirePanelToken(http.HandlerFunc(resolved.handleAgents)))
		mux.Handle(prefix+"/agents/{agentID}", resolved.requirePanelToken(http.HandlerFunc(resolved.handleAgent)))
		mux.Handle(prefix+"/agents/{agentID}/stats", resolved.requirePanelToken(http.HandlerFunc(resolved.handleAgentStats)))
		mux.Handle(prefix+"/agents/{agentID}/apply", resolved.requirePanelToken(http.HandlerFunc(resolved.handleApplyAgent)))
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

	return mux, nil
}

func (d Dependencies) withDefaults() (Dependencies, error) {
	if d.RuleService == nil {
		if legacy, ok := any(d.AgentService).(legacyRuleListService); ok {
			d.RuleService = agentRuleServiceAdapter{agent: legacy}
		}
	}

	if d.TaskService == nil && d.hasCoreServices() {
		d.TaskService = service.NewTaskService(service.TaskServiceConfig{})
	}

	if d.BackupService == nil && d.hasCoreServices() && d.TaskService != nil {
		d.BackupService = unavailableBackupService{}
	}

	if d.hasCoreServices() && d.TaskService != nil && d.BackupService != nil {
		return d, nil
	}

	store, err := storage.NewSQLiteStore(d.Config.DataDir, d.Config.LocalAgentID)
	if err != nil {
		return Dependencies{}, err
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
	if d.RelayListenerService == nil {
		d.RelayListenerService = service.NewRelayListenerService(d.Config, store)
	}
	if d.CertificateService == nil {
		d.CertificateService = service.NewCertificateService(d.Config, store)
	}
	if d.TaskService == nil {
		d.TaskService = service.NewTaskService(service.TaskServiceConfig{})
	}
	if d.BackupService == nil {
		d.BackupService = service.NewBackupService(d.Config, store)
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

func mapServiceError(err error) (int, map[string]any) {
	switch {
	case errors.Is(err, service.ErrAgentUnauthorized):
		return http.StatusUnauthorized, errorPayload("Unauthorized: missing agent token")
	case errors.Is(err, service.ErrAgentNotFound):
		return http.StatusNotFound, errorPayload("agent not found")
	case errors.Is(err, service.ErrRuleNotFound):
		return http.StatusNotFound, errorPayload("rule id not found")
	case errors.Is(err, service.ErrVersionPolicyNotFound):
		return http.StatusNotFound, errorPayload("version policy not found")
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
