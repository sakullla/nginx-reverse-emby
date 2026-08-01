package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/coordinator"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/revision"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

var ErrAgentNotFound = errors.New("agent not found")
var ErrAgentUnauthorized = errors.New("agent unauthorized")

type agentRegistrationError string

func (e agentRegistrationError) Error() string { return string(e) }
func (e agentRegistrationError) Is(target error) bool {
	return target == ErrInvalidArgument
}

var defaultLocalCapabilities = []string{"http_rules", "local_acme", "cert_install", managedCertificateReportsCapability, "l4", "relay_quic", "egress_profiles", packageManifestCapability}

const (
	packageManifestCapability           = "package_manifest_v1"
	managedCertificateReportsCapability = "managed_certificate_reports_v1"
)

type agentStore interface {
	ListAgents(context.Context) ([]storage.AgentRow, error)
	ListHTTPRules(context.Context, string) ([]storage.HTTPRuleRow, error)
	ListL4Rules(context.Context, string) ([]storage.L4RuleRow, error)
	ListRelayListeners(context.Context, string) ([]storage.RelayListenerRow, error)
	LoadLocalAgentState(context.Context) (storage.LocalAgentStateRow, error)
	LoadLocalRuntimeState(context.Context) (storage.RuntimeState, error)
	LoadAgentSnapshot(context.Context, string, storage.AgentSnapshotInput) (storage.Snapshot, error)
	LoadLocalSnapshot(context.Context, string) (storage.Snapshot, error)
	ListManagedCertificates(context.Context) ([]storage.ManagedCertificateRow, error)
	SaveAgent(context.Context, storage.AgentRow) error
	SaveHTTPRules(context.Context, string, []storage.HTTPRuleRow) error
	SaveL4Rules(context.Context, string, []storage.L4RuleRow) error
	SaveRelayListeners(context.Context, string, []storage.RelayListenerRow) error
	SaveLocalRuntimeState(context.Context, string, storage.RuntimeState) error
	SaveManagedCertificates(context.Context, []storage.ManagedCertificateRow) error
	LoadManagedCertificateMaterial(context.Context, string) (storage.ManagedCertificateBundle, bool, error)
	SaveManagedCertificateMaterial(context.Context, string, storage.ManagedCertificateBundle) error
	CleanupManagedCertificateMaterial(context.Context, []storage.ManagedCertificateRow, []storage.ManagedCertificateRow) error
	DeleteAgent(context.Context, string) error
}

type agentHeartbeatStore interface {
	SaveAgentHeartbeat(context.Context, storage.AgentRow) error
}

type agentRevisionActionStore interface {
	GetAgentRevisionPointer(context.Context, string) (storage.AgentRevisionPointerRow, bool, error)
	GetCoordinatorRevision(context.Context, string, int64) (storage.AgentRevisionRow, bool, error)
	RetryCoordinatorRevision(context.Context, string, int64, time.Time) (storage.AgentRevisionRow, error)
}

type agentRevisionRepository interface {
	RevisionRepository
	coordinator.Repository
}

// AgentPKIController extends the existing token-authenticated control paths
// with tunnel PKI payloads. It never owns a listener or transport of its own.
type AgentPKIController interface {
	RegisterAgent(context.Context, RegisterRequest, storage.AgentRow) (PKIRegistrationReply, error)
	ControlSync(context.Context, string, *storage.PKISecurityAcknowledgement, []PKIControlEnrollmentRequest) (storage.PKISecuritySnapshot, []PKIControlCredential, error)
	PrepareRelayListeners(context.Context, string, []storage.RelayListener) ([]storage.RelayListener, error)
}

type AgentSummary struct {
	ID                       string                `json:"id"`
	Name                     string                `json:"name"`
	AgentURL                 string                `json:"agent_url"`
	Version                  string                `json:"version"`
	Platform                 string                `json:"platform"`
	RuntimePackageVersion    string                `json:"runtime_package_version"`
	RuntimePackagePlatform   string                `json:"runtime_package_platform"`
	RuntimePackageArch       string                `json:"runtime_package_arch"`
	RuntimePackageSHA256     string                `json:"runtime_package_sha256"`
	DesiredPackageSHA256     string                `json:"desired_package_sha256"`
	PackageSyncStatus        string                `json:"package_sync_status"`
	DesiredVersion           string                `json:"desired_version"`
	Tags                     []string              `json:"tags"`
	OutboundProxyURL         string                `json:"outbound_proxy_url"`
	TrafficStatsInterval     string                `json:"traffic_stats_interval"`
	Mode                     string                `json:"mode"`
	DesiredRevision          int                   `json:"desired_revision"`
	CurrentRevision          int                   `json:"current_revision"`
	LastApplyRevision        int                   `json:"last_apply_revision"`
	LastApplyStatus          string                `json:"last_apply_status"`
	LastApplyMessage         string                `json:"last_apply_message"`
	LastSeenAt               string                `json:"last_seen_at"`
	Status                   string                `json:"status"`
	Error                    string                `json:"error"`
	IsLocal                  bool                  `json:"is_local"`
	LastSeenIP               string                `json:"last_seen_ip"`
	LastSeenIPv4             string                `json:"last_seen_ipv4"`
	LastSeenIPv6             string                `json:"last_seen_ipv6"`
	DdnsDomain               string                `json:"ddns_domain"`
	DdnsStatus               storage.DdnsStatus    `json:"ddns_status,omitempty"`
	DdnsConfig               *storage.DDNSConfig   `json:"ddns_config,omitempty"`
	Capabilities             []string              `json:"capabilities"`
	HTTPRulesCount           int                   `json:"http_rules_count"`
	L4RulesCount             int                   `json:"l4_rules_count"`
	RegistrationControlToken string                `json:"-"`
	PKIRegistration          *PKIRegistrationReply `json:"-"`
}

// PKIRegistrationReply is returned only by the existing agent registration
// route. It contains public certificate/trust material and the existing
// per-agent control token, never an endpoint private key.
type PKIRegistrationReply struct {
	AgentID          string                      `json:"agent_id"`
	AgentToken       string                      `json:"agent_token"`
	TunnelCredential storage.PKITunnelCredential `json:"tunnel_credential"`
	SecuritySnapshot storage.PKISecuritySnapshot `json:"security_snapshot"`
}

type HTTPRuleBackend struct {
	URL string `json:"url"`
}

type HTTPLoadBalancing struct {
	Strategy string `json:"strategy"`
}

type HTTPCustomHeader struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type HTTPRule struct {
	ID               int                `json:"id"`
	AgentID          string             `json:"agent_id"`
	AgentName        string             `json:"agent_name,omitempty"`
	FrontendURL      string             `json:"frontend_url"`
	BackendURL       string             `json:"-"`
	Backends         []HTTPRuleBackend  `json:"backends"`
	LoadBalancing    HTTPLoadBalancing  `json:"load_balancing"`
	Enabled          bool               `json:"enabled"`
	Tags             []string           `json:"tags"`
	ProxyRedirect    bool               `json:"proxy_redirect"`
	RelayChain       []int              `json:"-"`
	RelayLayers      [][]int            `json:"relay_layers"`
	RelayObfs        bool               `json:"relay_obfs"`
	PassProxyHeaders bool               `json:"pass_proxy_headers"`
	UserAgent        string             `json:"user_agent"`
	CustomHeaders    []HTTPCustomHeader `json:"custom_headers"`
	EgressProfileID  *int               `json:"egress_profile_id,omitempty"`
	Revision         int                `json:"revision"`
}

type HeartbeatRequest struct {
	Name                      string                              `json:"name"`
	AgentID                   string                              `json:"agent_id"`
	CurrentRevision           int64                               `json:"current_revision"`
	LastApplyRevision         int64                               `json:"last_apply_revision"`
	Version                   string                              `json:"version"`
	Platform                  string                              `json:"platform"`
	RuntimePackage            RuntimePackageInfo                  `json:"runtime_package"`
	AgentURL                  string                              `json:"agent_url"`
	Tags                      []string                            `json:"tags"`
	Capabilities              []string                            `json:"capabilities"`
	Stats                     AgentStats                          `json:"stats"`
	LastSeenIP                string                              `json:"last_seen_ip"`
	LastSeenIPv4              string                              `json:"last_seen_ipv4"`
	LastSeenIPv6              string                              `json:"last_seen_ipv6"`
	LastApplyStatus           string                              `json:"last_apply_status"`
	LastApplyMessage          string                              `json:"last_apply_message"`
	ManagedCertificateReports []ManagedCertificateHeartbeatReport `json:"managed_certificate_reports"`
	PKISecurityAck            *storage.PKISecurityAcknowledgement `json:"pki_security_ack,omitempty"`
	PKIEnrollmentRequests     []PKIControlEnrollmentRequest       `json:"pki_enrollment_requests,omitempty"`
	HasAgentURL               bool                                `json:"-"`
	HasTags                   bool                                `json:"-"`
	HasCapabilities           bool                                `json:"-"`
}

type HeartbeatReply struct {
	HasUpdate            bool                               `json:"has_update"`
	DesiredVersion       string                             `json:"desired_version"`
	DesiredRevision      int64                              `json:"desired_revision"`
	CurrentRevision      int64                              `json:"current_revision"`
	VersionPackage       string                             `json:"version_package,omitempty"`
	VersionPackageMeta   *storage.VersionPackage            `json:"version_package_meta,omitempty"`
	VersionSHA256        string                             `json:"version_sha256,omitempty"`
	Rules                []storage.HTTPRule                 `json:"rules"`
	L4Rules              []storage.L4Rule                   `json:"l4_rules"`
	RelayListeners       []storage.RelayListener            `json:"relay_listeners"`
	EgressProfiles       []storage.EgressProfile            `json:"egress_profiles"`
	Certificates         []storage.ManagedCertificateBundle `json:"certificates"`
	CertificatePolicies  []storage.ManagedCertificatePolicy `json:"certificate_policies"`
	PKISecurity          *storage.PKISecuritySnapshot       `json:"pki_security,omitempty"`
	PKICredentials       []PKIControlCredential             `json:"pki_credentials,omitempty"`
	PKIStatus            *PKIControlStatus                  `json:"pki_status,omitempty"`
	DDNSConfig           *storage.DDNSConfig                `json:"ddns_config,omitempty"`
	OutboundProxyURL     string                             `json:"-"`
	TrafficStatsInterval string                             `json:"-"`
	TrafficStatsEnabled  *bool                              `json:"-"`
	TrafficBlocked       bool                               `json:"-"`
	TrafficBlockReason   string                             `json:"-"`
}

type PKIControlStatus struct {
	Status       string `json:"status"`
	Code         string `json:"code,omitempty"`
	RecoveryHint string `json:"recovery_hint,omitempty"`
}

type AgentRuntimeConfig struct {
	OutboundProxyURL     string `json:"outbound_proxy_url"`
	TrafficStatsInterval string `json:"traffic_stats_interval,omitempty"`
	TrafficStatsEnabled  *bool  `json:"traffic_stats_enabled,omitempty"`
	TrafficBlocked       bool   `json:"traffic_blocked,omitempty"`
	TrafficBlockReason   string `json:"traffic_block_reason,omitempty"`
}

type RuntimePackageInfo struct {
	Version  string `json:"version"`
	Platform string `json:"platform"`
	Arch     string `json:"arch"`
	SHA256   string `json:"sha256"`
}

type RegisterRequest struct {
	AgentID                string                              `json:"agent_id,omitempty"`
	Name                   string                              `json:"name"`
	AgentURL               string                              `json:"agent_url"`
	AgentToken             string                              `json:"agent_token"`
	Version                string                              `json:"version"`
	Platform               string                              `json:"platform"`
	Tags                   []string                            `json:"tags"`
	Capabilities           []string                            `json:"capabilities"`
	Mode                   string                              `json:"mode"`
	RegisterToken          string                              `json:"register_token"`
	PKIEnrollmentRequestID string                              `json:"pki_enrollment_request_id,omitempty"`
	TunnelCSRPEM           string                              `json:"tunnel_csr_pem,omitempty"`
	PKISecurityAck         *storage.PKISecurityAcknowledgement `json:"pki_security_ack,omitempty"`
	HasCapabilities        bool                                `json:"-"`
	// RegisterTokenAuthorized is set only by the HTTP boundary after it has
	// validated the configured static registration token. It preserves the
	// legacy registration path when canonical tunnel PKI exists without
	// weakening CSR enrollment, which continues to use a one-time PKI token.
	RegisterTokenAuthorized bool `json:"-"`
}

// PKIControlEnrollmentRequest is carried by the existing authenticated
// heartbeat flow for tunnel-client renewal and listener CSR enrollment.
type PKIControlEnrollmentRequest struct {
	RequestID    string   `json:"request_id"`
	Kind         string   `json:"kind"`
	ListenerID   string   `json:"listener_id,omitempty"`
	Purpose      string   `json:"purpose"`
	CSRPEM       string   `json:"csr_pem"`
	DNSNames     []string `json:"dns_names,omitempty"`
	IPAddresses  []string `json:"ip_addresses,omitempty"`
	controlToken string
}

type PKIControlCredential struct {
	RequestID  string                      `json:"request_id"`
	Credential storage.PKITunnelCredential `json:"credential,omitempty"`
	Error      string                      `json:"error,omitempty"`
}

type UpdateAgentRequest struct {
	Name                 *string             `json:"name,omitempty"`
	AgentURL             *string             `json:"agent_url,omitempty"`
	AgentToken           *string             `json:"agent_token,omitempty"`
	Version              *string             `json:"version,omitempty"`
	DesiredVersion       *string             `json:"desired_version,omitempty"`
	Tags                 *[]string           `json:"tags,omitempty"`
	Capabilities         *[]string           `json:"capabilities,omitempty"`
	OutboundProxyURL     *string             `json:"outbound_proxy_url,omitempty"`
	TrafficStatsInterval *string             `json:"traffic_stats_interval,omitempty"`
	DdnsConfig           *storage.DDNSConfig `json:"ddns_config,omitempty"`
}

type AgentStats map[string]any

type ApplyAgentResult struct {
	Message         string `json:"message"`
	DesiredRevision int64  `json:"desired_revision,omitempty"`
	Pending         bool   `json:"pending,omitempty"`
}

type agentService struct {
	cfg                        config.Config
	store                      agentStore
	trafficService             heartbeatTrafficService
	settingsMutation           *revision.Executor
	revisionActions            agentRevisionActionStore
	revisionAPI                *RevisionAPI
	pki                        AgentPKIController
	now                        func() time.Time
	localMonitorRefreshTrigger func(context.Context) error
	ddnsReconciler             DDNSReconciler
	bundledCacheMu             sync.Mutex
	bundledCache               map[string]bundledPackageCacheEntry
	managedCertificateUpdateMu sync.Mutex
	monitorMu                  sync.Mutex
	monitorSubscribers         map[chan AgentMonitorUpdate]struct{}
}

type heartbeatTrafficService interface {
	IngestHeartbeat(context.Context, string, AgentStats) error
	Summary(context.Context, string) (TrafficSummary, error)
	BlockState(context.Context, string) (bool, string, error)
}

// DDNSReconciler triggers master-side A/AAAA reconciliation after an agent
// heartbeat reports fresh IPs. It is implemented by the DDNS service (see
// service/ddns*.go) and injected via SetDDNSReconciler. The Heartbeat path
// invokes it fire-and-forget: the caller swallows any panic and ignores the
// outcome, so DNS resolution issues can never break the heartbeat main path.
// The method carries no Cloudflare credential — the implementer reads CF tokens
// from the master environment exclusively (R7).
type DDNSReconciler interface {
	ReconcileAfterHeartbeat(ctx context.Context, agentID string)
}

type bundledPackageCacheEntry struct {
	modTimeUnixNano int64
	size            int64
	pkg             storage.VersionPackage
}

func NewAgentService(cfg config.Config, store agentStore) *agentService {
	cfg.TrafficStatsEnabled = agentTrafficStatsEnabled(cfg)
	svc := &agentService{
		cfg:                cfg,
		store:              store,
		now:                time.Now,
		bundledCache:       make(map[string]bundledPackageCacheEntry),
		monitorSubscribers: make(map[chan AgentMonitorUpdate]struct{}),
	}
	if mutationStore, ok := store.(revision.Store); ok {
		svc.settingsMutation = newMutationExecutor(
			cfg,
			mutationStore,
			revision.WithSnapshotBuilder(revision.SnapshotBuilderFunc(buildAgentSettingsSnapshot)),
		)
	}
	if actionStore, ok := store.(agentRevisionActionStore); ok {
		svc.revisionActions = actionStore
	}
	if repository, ok := store.(agentRevisionRepository); ok {
		revisionCoordinator, err := coordinator.New(repository, coordinator.OptionsFromConfig(cfg.RevisionCoordinator))
		if err == nil {
			svc.revisionAPI = newRevisionAPI(cfg, repository, revisionCoordinator)
		}
	}
	if trafficStore, ok := store.(trafficStore); ok {
		trafficCfg, err := NewTrafficServiceConfig(cfg.TrafficStatsEnabled, cfg.Timezone)
		if err == nil {
			svc.trafficService = NewTrafficService(trafficCfg, trafficStore)
		}
	}
	return svc
}

func (s *agentService) RevisionAPI() *RevisionAPI {
	if s == nil {
		return nil
	}
	return s.revisionAPI
}

func agentTrafficStatsEnabled(cfg config.Config) bool {
	if cfg.TrafficStatsEnabled {
		return true
	}
	return strings.TrimSpace(cfg.ListenAddr) == "" &&
		strings.TrimSpace(cfg.DataDir) == "" &&
		strings.TrimSpace(cfg.DatabaseDriver) == "" &&
		strings.TrimSpace(cfg.FrontendDistDir) == "" &&
		strings.TrimSpace(cfg.PublicAgentAssetsDir) == ""
}

func (s *agentService) SetTrafficService(trafficService heartbeatTrafficService) {
	s.trafficService = trafficService
}

func (s *agentService) SetPKIController(controller AgentPKIController) {
	s.pki = controller
}

func (s *agentService) SetLocalApplyTrigger(trigger func(context.Context) error) {
	_ = trigger
}

func (s *agentService) SetLocalMonitorRefreshTrigger(trigger func(context.Context) error) {
	s.localMonitorRefreshTrigger = wrapLocalApplyTrigger(trigger)
}

// SetDDNSReconciler injects the master DDNS reconciler (T5). nil leaves the
// heartbeat path with a no-op trigger, which is the default until wired.
func (s *agentService) SetDDNSReconciler(reconciler DDNSReconciler) {
	s.ddnsReconciler = reconciler
}

// triggerDDNSReconcile fires the master DDNS reconciler after a heartbeat,
// swallowing any panic so DNS issues never break the heartbeat main path.
func (s *agentService) triggerDDNSReconcile(ctx context.Context, agentID string) {
	if s.ddnsReconciler == nil {
		return
	}
	defer func() { _ = recover() }()
	s.ddnsReconciler.ReconcileAfterHeartbeat(ctx, agentID)
}

func (s *agentService) List(ctx context.Context) ([]AgentSummary, error) {
	rows, err := s.store.ListAgents(ctx)
	if err != nil {
		return nil, err
	}

	agents := make([]AgentSummary, 0, len(rows)+1)
	if s.cfg.EnableLocalAgent {
		summary, err := s.localSummary(ctx)
		if err != nil {
			return nil, err
		}
		agents = append(agents, summary)
	}

	for _, row := range rows {
		if row.IsLocal || (s.cfg.EnableLocalAgent && row.ID == s.cfg.LocalAgentID) {
			continue
		}

		summary, err := s.summaryForRow(ctx, row)
		if err != nil {
			return nil, err
		}
		agents = append(agents, summary)
	}

	return agents, nil
}

func (s *agentService) Get(ctx context.Context, agentID string) (AgentSummary, error) {
	if s.cfg.EnableLocalAgent && agentID == s.cfg.LocalAgentID {
		return s.localSummary(ctx)
	}
	row, err := s.findAgentByID(ctx, agentID)
	if err != nil {
		return AgentSummary{}, err
	}
	return s.summaryForRow(ctx, row)
}

func (s *agentService) GetByToken(ctx context.Context, agentToken string) (AgentSummary, error) {
	token := strings.TrimSpace(agentToken)
	if token == "" {
		return AgentSummary{}, ErrAgentUnauthorized
	}
	row, err := s.findAgentByToken(ctx, token)
	if err != nil {
		if errors.Is(err, ErrAgentNotFound) {
			return AgentSummary{}, ErrAgentUnauthorized
		}
		return AgentSummary{}, err
	}
	return s.summaryForRow(ctx, row)
}

func (s *agentService) Register(ctx context.Context, request RegisterRequest, headerAgentToken string) (AgentSummary, error) {
	name := strings.TrimSpace(request.Name)
	if name == "" {
		return AgentSummary{}, agentRegistrationError("name is required")
	}
	agentURL := trimTrailingSlash(request.AgentURL)
	if agentURL != "" && !validateAgentURL(agentURL) {
		return AgentSummary{}, agentRegistrationError("agent_url must be a valid http/https URL")
	}

	agentToken := strings.TrimSpace(request.AgentToken)
	if agentToken == "" {
		agentToken = strings.TrimSpace(headerAgentToken)
	}
	if agentToken == "" && strings.TrimSpace(request.TunnelCSRPEM) != "" {
		generatedToken, tokenErr := randomAgentControlToken()
		if tokenErr != nil {
			return AgentSummary{}, fmt.Errorf("generate agent control token: %w", tokenErr)
		}
		agentToken = generatedToken
	}
	if agentToken == "" {
		return AgentSummary{}, agentRegistrationError("agent_token is required")
	}

	rows, err := s.store.ListAgents(ctx)
	if err != nil {
		return AgentSummary{}, err
	}
	pkiSettingsPresent := false
	if source, ok := s.store.(interface {
		LoadPKICanonicalState(context.Context) (storage.PKICanonicalState, error)
	}); ok && strings.TrimSpace(request.TunnelCSRPEM) == "" {
		state, stateErr := source.LoadPKICanonicalState(ctx)
		if stateErr != nil {
			return AgentSummary{}, stateErr
		}
		pkiSettingsPresent = state.Settings != nil
	}
	var authenticatedRow *storage.AgentRow
	if pkiSettingsPresent && !request.RegisterTokenAuthorized {
		presentedToken := strings.TrimSpace(headerAgentToken)
		if presentedToken == "" {
			return AgentSummary{}, ErrAgentUnauthorized
		}
		for index := range rows {
			existing := &rows[index]
			if !existing.IsLocal && existing.ID != s.cfg.LocalAgentID && existing.AgentToken == presentedToken {
				authenticatedRow = existing
				break
			}
		}
		if authenticatedRow == nil || strings.TrimSpace(authenticatedRow.AgentToken) == "" {
			return AgentSummary{}, ErrAgentUnauthorized
		}
		if requestedID := strings.TrimSpace(request.AgentID); requestedID != "" && requestedID != authenticatedRow.ID {
			return AgentSummary{}, ErrAgentUnauthorized
		}
		if bodyToken := strings.TrimSpace(request.AgentToken); bodyToken != "" && bodyToken != authenticatedRow.AgentToken {
			return AgentSummary{}, ErrAgentUnauthorized
		}
		agentToken = authenticatedRow.AgentToken
	}

	hasCapabilities := request.HasCapabilities || len(request.Capabilities) > 0
	capabilities := []string{"http_rules"}
	if hasCapabilities {
		capabilities = request.Capabilities
	}

	row := storage.AgentRow{
		ID:               randomAgentID(),
		DesiredVersion:   "",
		TagsJSON:         marshalStringArray(normalizeAgentTags(request.Tags)),
		CapabilitiesJSON: marshalStringArray(normalizeCapabilities(capabilities)),
		Mode:             resolveRemoteAgentMode(agentURL),
		LastApplyStatus:  "success",
	}
	if authenticatedRow != nil {
		row = *authenticatedRow
	}
	reusedPullByName := false
	for _, existing := range rows {
		if authenticatedRow != nil {
			break
		}
		// The embedded local agent has no public credential. Never let the
		// registration endpoint reuse or convert its persisted settings row.
		if existing.IsLocal || existing.ID == s.cfg.LocalAgentID {
			continue
		}
		existingAgentURL := trimTrailingSlash(existing.AgentURL)
		if (strings.TrimSpace(request.AgentID) != "" && existing.ID == strings.TrimSpace(request.AgentID)) ||
			existing.AgentToken == agentToken ||
			(existingAgentURL != "" && existingAgentURL == agentURL) {
			row = existing
			break
		}
		if existingAgentURL == "" && existing.Name == name {
			row = existing
			reusedPullByName = true
			break
		}
	}

	row.Name = name
	row.AgentURL = agentURL
	row.AgentToken = agentToken
	row.Version = strings.TrimSpace(request.Version)
	row.Platform = strings.TrimSpace(request.Platform)
	row.TagsJSON = marshalStringArray(normalizeAgentTags(request.Tags))
	row.CapabilitiesJSON = marshalStringArray(normalizeCapabilities(capabilities))
	row.Mode = resolveRemoteAgentMode(agentURL)
	row.IsLocal = false
	if reusedPullByName {
		row.DesiredRevision = 0
		row.CurrentRevision = 0
		row.LastApplyRevision = 0
		row.LastApplyStatus = "success"
		row.LastApplyMessage = ""
		row.RuntimePackageVersion = ""
		row.RuntimePackagePlatform = ""
		row.RuntimePackageArch = ""
		row.RuntimePackageSHA256 = ""
		row.LastReportedStatsJSON = ""
		row.LastSeenAt = ""
		row.LastSeenIP = ""
	}

	if strings.TrimSpace(request.TunnelCSRPEM) != "" {
		if s.pki == nil {
			return AgentSummary{}, fmt.Errorf("%w: internal PKI service is unavailable", ErrPKIEnrollmentAuthorityUnavailable)
		}
		registration, err := s.pki.RegisterAgent(ctx, request, row)
		if err != nil {
			return AgentSummary{}, err
		}
		persisted, err := s.findAgentByID(ctx, registration.AgentID)
		if err != nil {
			return AgentSummary{}, err
		}
		summary, err := s.summaryForRow(ctx, persisted)
		if err != nil {
			return AgentSummary{}, err
		}
		summary.RegistrationControlToken = registration.AgentToken
		summary.PKIRegistration = &registration
		return summary, nil
	}

	if authenticatedRow != nil {
		if authenticatedStore, ok := s.store.(interface {
			SaveAuthenticatedAgentRegistration(context.Context, string, storage.AgentRow) error
		}); ok {
			if err := authenticatedStore.SaveAuthenticatedAgentRegistration(ctx, agentToken, row); err != nil {
				if errors.Is(err, storage.ErrAgentControlTokenChanged) {
					return AgentSummary{}, ErrAgentUnauthorized
				}
				return AgentSummary{}, err
			}
		} else if err := s.store.SaveAgent(ctx, row); err != nil {
			return AgentSummary{}, err
		}
	} else if err := s.store.SaveAgent(ctx, row); err != nil {
		return AgentSummary{}, err
	}

	return s.summaryForRow(ctx, row)
}

func (s *agentService) ListHTTPRules(ctx context.Context, agentID string) ([]HTTPRule, error) {
	if agentID == "" {
		agentID = s.cfg.LocalAgentID
	}
	if err := s.ensureAgentExists(ctx, agentID); err != nil {
		return nil, err
	}

	rows, err := s.store.ListHTTPRules(ctx, agentID)
	if err != nil {
		return nil, err
	}

	rules := make([]HTTPRule, 0, len(rows))
	for _, row := range rows {
		rules = append(rules, httpRuleFromRow(row))
	}

	return rules, nil
}

func (s *agentService) Update(ctx context.Context, agentID string, input UpdateAgentRequest) (AgentSummary, error) {
	isLocal := s.cfg.EnableLocalAgent && agentID == s.cfg.LocalAgentID
	var row storage.AgentRow
	var err error
	if isLocal {
		if s.settingsMutation == nil {
			return AgentSummary{}, fmt.Errorf("%w: local agent cannot be modified", ErrInvalidArgument)
		}
		row, err = s.localSettingsRow(ctx)
	} else {
		row, err = s.findAgentByID(ctx, agentID)
	}
	if err != nil {
		return AgentSummary{}, err
	}

	name := strings.TrimSpace(row.Name)
	if input.Name != nil {
		name = strings.TrimSpace(*input.Name)
	}
	if name == "" {
		return AgentSummary{}, fmt.Errorf("%w: name is required", ErrInvalidArgument)
	}

	agentURL := strings.TrimSpace(row.AgentURL)
	if input.AgentURL != nil {
		agentURL = trimTrailingSlash(*input.AgentURL)
	}
	if agentURL != "" && !validateAgentURL(agentURL) {
		return AgentSummary{}, fmt.Errorf("%w: agent_url must be a valid http/https URL", ErrInvalidArgument)
	}

	agentToken := ""
	if !isLocal {
		agentToken = strings.TrimSpace(row.AgentToken)
		if input.AgentToken != nil {
			agentToken = strings.TrimSpace(*input.AgentToken)
		}
		if agentToken == "" {
			return AgentSummary{}, fmt.Errorf("%w: agent_token is required", ErrInvalidArgument)
		}
	}

	row.Name = name
	row.AgentURL = agentURL
	row.AgentToken = agentToken
	if isLocal {
		row.Mode = "local"
		row.IsLocal = true
	} else {
		row.Mode = resolveRemoteAgentMode(agentURL)
	}
	if input.Version != nil {
		row.Version = strings.TrimSpace(*input.Version)
	}
	configChanged := false
	if input.DesiredVersion != nil {
		desiredVersion := strings.TrimSpace(*input.DesiredVersion)
		if desiredVersion != strings.TrimSpace(row.DesiredVersion) {
			configChanged = true
		}
		row.DesiredVersion = desiredVersion
	}
	if input.Tags != nil {
		row.TagsJSON = marshalStringArray(normalizeAgentTags(*input.Tags))
	}
	if input.Capabilities != nil {
		previousCapabilities := canonicalCapabilities(parseStringArray(row.CapabilitiesJSON))
		nextCapabilities := canonicalCapabilities(*input.Capabilities)
		if isLocal {
			nextCapabilities = canonicalCapabilities(defaultLocalCapabilities)
		}
		if !slices.Equal(previousCapabilities, nextCapabilities) {
			configChanged = true
		}
		row.CapabilitiesJSON = marshalStringArray(nextCapabilities)
	}
	if input.OutboundProxyURL != nil {
		previousOutboundProxyURL := strings.TrimSpace(row.OutboundProxyURL)
		outboundProxyURL, err := normalizeOutboundProxyURLUpdate(*input.OutboundProxyURL, row.OutboundProxyURL)
		if err != nil {
			return AgentSummary{}, err
		}
		if outboundProxyURL != "" {
			if err := validateProxyURL(outboundProxyURL); err != nil {
				return AgentSummary{}, fmt.Errorf("%w: invalid outbound_proxy_url: %v", ErrInvalidArgument, err)
			}
		}
		row.OutboundProxyURL = outboundProxyURL
		if outboundProxyURL != previousOutboundProxyURL {
			configChanged = true
		}
	}
	if input.TrafficStatsInterval != nil {
		previousTrafficStatsInterval := normalizeStoredTrafficStatsInterval(row.TrafficStatsInterval)
		trafficStatsInterval, err := normalizeTrafficStatsInterval(*input.TrafficStatsInterval)
		if err != nil {
			return AgentSummary{}, err
		}
		row.TrafficStatsInterval = trafficStatsInterval
		if trafficStatsInterval != previousTrafficStatsInterval {
			configChanged = true
		}
	}
	// DDNS config is an optional, pointer-guarded update (nil = leave untouched,
	// matching Tags/Capabilities). Only a semantic change bumps the desired
	// revision; routing an unchanged DDNS value through the revision executor can
	// turn a metadata-only PATCH into a no-op rollback.
	if input.DdnsConfig != nil {
		nextDDNSConfigJSON, ddnsConfigChanged := updatedDDNSConfigJSON(row.DdnsConfigJSON, input.DdnsConfig)
		row.DdnsConfigJSON = nextDDNSConfigJSON
		if ddnsConfigChanged {
			configChanged = true
		}
	}
	if configChanged {
		if s.settingsMutation != nil {
			var replaySummary AgentSummary
			target := revisionTimeoutTarget(s.cfg, row.ID)
			validationTarget := revision.Target{
				AgentID:      row.ID,
				Capabilities: canonicalCapabilities(parseStringArray(row.CapabilitiesJSON)),
			}
			if isLocal {
				target.Local = true
				target.Platform = runtime.GOOS + "-" + runtime.GOARCH
				target.Capabilities = append([]string(nil), defaultLocalCapabilities...)
				validationTarget.Local = true
				validationTarget.Platform = target.Platform
				validationTarget.Capabilities = append([]string(nil), defaultLocalCapabilities...)
			}
			_, err := s.settingsMutation.Execute(ctx, revision.MutationRequest{
				Kind: "agent.settings.update",
				Request: map[string]any{
					"agent_id":               row.ID,
					"capabilities":           validationTarget.Capabilities,
					"desired_version":        row.DesiredVersion,
					"outbound_proxy_url":     row.OutboundProxyURL,
					"traffic_stats_interval": row.TrafficStatsInterval,
					"ddns_config":            row.DdnsConfigJSON,
				},
				Targets:             []revision.Target{target},
				ResourceState:       agentSettingsCapabilityState,
				ReplayResourceField: "agent",
				ReplayResource:      func() any { return replaySummary },
				Mutate: func(mutateCtx context.Context, tx *storage.GormStore, revisions map[string]int64) error {
					row.DesiredRevision = int(revisions[row.ID])
					if err := tx.SaveAgent(mutateCtx, row); err != nil {
						return err
					}
					snapshot, err := buildAgentSettingsSnapshot(mutateCtx, tx, validationTarget)
					if err != nil {
						return err
					}
					intentSnapshot, err := buildAgentSettingsIntentSnapshot(mutateCtx, tx, validationTarget)
					if err != nil {
						return err
					}
					if err := (FullSnapshotValidator{}).Validate(mutateCtx, revision.SnapshotValidation{
						Target: validationTarget, Snapshot: snapshot, IntentSnapshot: &intentSnapshot,
					}); err != nil {
						return err
					}
					replaySummary, err = s.summaryForRowWithStore(mutateCtx, tx, row)
					replaySummary = RedactAgentSummary(replaySummary)
					return err
				},
			})
			if err != nil {
				return AgentSummary{}, err
			}
			if isLocal {
				return s.localSummary(ctx)
			}
			return s.summaryForRow(ctx, row)
		}
		allocator, err := newConfigIdentityAllocatorFromStore(ctx, s.cfg, s.store)
		if err != nil {
			return AgentSummary{}, err
		}
		row.DesiredRevision = allocator.AllocateRevisionForAgent(row.ID, row.DesiredRevision)
	}

	if err := s.store.SaveAgent(ctx, row); err != nil {
		return AgentSummary{}, err
	}
	if isLocal {
		return s.localSummary(ctx)
	}
	return s.summaryForRow(ctx, row)
}

func updatedDDNSConfigJSON(current string, next *storage.DDNSConfig) (string, bool) {
	current = marshalDDNSConfigJSON(parseDDNSConfig(current))
	normalizedNext := marshalDDNSConfigJSON(next)
	return normalizedNext, normalizedNext != current
}

func normalizeOutboundProxyURLUpdate(raw string, fallback string) (string, error) {
	normalized, err := normalizeProxyURLUpdate(raw, fallback)
	if err != nil {
		return "", fmt.Errorf("%w: outbound_proxy_url password is redacted; re-enter the password before saving changes", ErrInvalidArgument)
	}
	return normalized, nil
}

func normalizeTrafficStatsInterval(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", nil
	}
	dur, err := time.ParseDuration(trimmed)
	if err != nil || dur <= 0 {
		return "", fmt.Errorf("%w: traffic_stats_interval must be a positive duration", ErrInvalidArgument)
	}
	return dur.String(), nil
}

func normalizeStoredTrafficStatsInterval(raw string) string {
	normalized, err := normalizeTrafficStatsInterval(raw)
	if err != nil {
		return strings.TrimSpace(raw)
	}
	return normalized
}

func (s *agentService) Delete(ctx context.Context, agentID string) (AgentSummary, error) {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return AgentSummary{}, fmt.Errorf("%w: agent id is required", ErrInvalidArgument)
	}
	if s.cfg.EnableLocalAgent && agentID == s.cfg.LocalAgentID {
		return AgentSummary{}, fmt.Errorf("%w: local agent cannot be deleted", ErrInvalidArgument)
	}

	row, err := s.findAgentByID(ctx, agentID)
	if errors.Is(err, ErrAgentNotFound) {
		if atomicStore, ok := s.store.(interface {
			DeleteAgentWithAssociations(context.Context, string) ([]storage.ManagedCertificateRow, []storage.ManagedCertificateRow, error)
		}); ok {
			originalCertificates, nextCertificates, cleanupErr := atomicStore.DeleteAgentWithAssociations(ctx, agentID)
			if cleanupErr != nil {
				if errors.Is(cleanupErr, storage.ErrPKIAgentIdentityNotRevoked) {
					return AgentSummary{}, fmt.Errorf("%w: revoke the agent PKI identity before deletion", ErrInvalidArgument)
				}
				if errors.Is(cleanupErr, storage.ErrAgentRelayListenerReferenced) {
					return AgentSummary{}, fmt.Errorf("%w: %v", ErrInvalidArgument, cleanupErr)
				}
				return AgentSummary{}, cleanupErr
			}
			cleanupManagedCertificateMaterialBestEffort(ctx, s.store, originalCertificates, nextCertificates)
			return AgentSummary{ID: agentID}, nil
		}
		if cleanupErr := s.store.DeleteAgent(ctx, agentID); cleanupErr != nil {
			return AgentSummary{}, cleanupErr
		}
		return AgentSummary{ID: agentID}, nil
	}
	if err != nil {
		return AgentSummary{}, err
	}
	if guard, ok := s.store.(interface {
		RequireAgentPKIRevokedForDeletion(context.Context, string) error
	}); ok {
		if err := guard.RequireAgentPKIRevokedForDeletion(ctx, agentID); err != nil {
			if errors.Is(err, storage.ErrPKIAgentIdentityNotRevoked) {
				return AgentSummary{}, fmt.Errorf("%w: revoke the agent PKI identity before deletion", ErrInvalidArgument)
			}
			return AgentSummary{}, err
		}
	}
	deleted, err := s.summaryForRow(ctx, row)
	if err != nil {
		return AgentSummary{}, err
	}

	listeners, err := s.store.ListRelayListeners(ctx, agentID)
	if err != nil {
		return AgentSummary{}, err
	}
	for _, listener := range listeners {
		if ref, err := s.findRelayListenerReference(ctx, agentID, listener.ID); err != nil {
			return AgentSummary{}, err
		} else if ref != nil {
			return AgentSummary{}, fmt.Errorf("%w: cannot delete agent %s: relay listener %d is referenced by %s rule #%d on agent %s", ErrInvalidArgument, agentID, listener.ID, ref.RuleType, ref.RuleID, ref.AgentID)
		}
	}
	if atomicStore, ok := s.store.(interface {
		DeleteAgentWithAssociations(context.Context, string) ([]storage.ManagedCertificateRow, []storage.ManagedCertificateRow, error)
	}); ok {
		originalCertificates, nextCertificates, err := atomicStore.DeleteAgentWithAssociations(ctx, agentID)
		if err != nil {
			if errors.Is(err, storage.ErrPKIAgentIdentityNotRevoked) {
				return AgentSummary{}, fmt.Errorf("%w: revoke the agent PKI identity before deletion", ErrInvalidArgument)
			}
			if errors.Is(err, storage.ErrAgentRelayListenerReferenced) {
				return AgentSummary{}, fmt.Errorf("%w: %v", ErrInvalidArgument, err)
			}
			return AgentSummary{}, err
		}
		cleanupManagedCertificateMaterialBestEffort(ctx, s.store, originalCertificates, nextCertificates)
		return deleted, nil
	}

	if err := s.store.SaveHTTPRules(ctx, agentID, nil); err != nil {
		return AgentSummary{}, err
	}
	if err := s.store.SaveL4Rules(ctx, agentID, nil); err != nil {
		return AgentSummary{}, err
	}
	if err := s.store.SaveRelayListeners(ctx, agentID, nil); err != nil {
		return AgentSummary{}, err
	}

	certRows, err := s.store.ListManagedCertificates(ctx)
	if err != nil {
		return AgentSummary{}, err
	}
	originalCertRows := append([]storage.ManagedCertificateRow(nil), certRows...)
	nextCertRows := make([]storage.ManagedCertificateRow, 0, len(certRows))
	certsChanged := false
	for _, row := range certRows {
		cert := managedCertificateFromRow(row)
		if !containsString(cert.TargetAgentIDs, agentID) {
			nextCertRows = append(nextCertRows, row)
			continue
		}
		certsChanged = true
		cert.TargetAgentIDs = removeString(cert.TargetAgentIDs, agentID)
		delete(cert.AgentReports, agentID)
		if len(cert.TargetAgentIDs) == 0 {
			continue
		}
		nextCertRows = append(nextCertRows, managedCertificateToRow(cert))
	}
	if certsChanged {
		if err := s.store.SaveManagedCertificates(ctx, nextCertRows); err != nil {
			return AgentSummary{}, err
		}
		cleanupManagedCertificateMaterialBestEffort(ctx, s.store, originalCertRows, nextCertRows)
	}

	if err := s.store.DeleteAgent(ctx, agentID); err != nil {
		if errors.Is(err, storage.ErrPKIAgentIdentityNotRevoked) {
			return AgentSummary{}, fmt.Errorf("%w: revoke the agent PKI identity before deletion", ErrInvalidArgument)
		}
		return AgentSummary{}, err
	}
	return deleted, nil
}

func (s *agentService) Stats(ctx context.Context, agentID string) (AgentStats, error) {
	if s.cfg.EnableLocalAgent && agentID == s.cfg.LocalAgentID {
		runtimeState, err := s.store.LoadLocalRuntimeState(ctx)
		if err != nil {
			return nil, err
		}
		if runtimeState.Metadata != nil {
			if stats := parseAgentStats(runtimeState.Metadata["stats"]); len(stats) > 0 {
				if _, ok := stats["status"]; !ok {
					stats["status"] = "运行中"
				}
				return stats, nil
			}
		}
		return localFallbackStats(), nil
	}
	row, err := s.findAgentByID(ctx, agentID)
	if err != nil {
		return nil, err
	}
	if stats := parseAgentStats(row.LastReportedStatsJSON); len(stats) > 0 {
		return stats, nil
	}
	status := "离线"
	if s.agentStatus(row) == "online" {
		status = "运行中"
	}
	return AgentStats{
		"totalRequests": "0",
		"status":        status,
	}, nil
}

func localFallbackStats() AgentStats {
	return AgentStats{
		"activeConnections": "0",
		"totalRequests":     "0",
		"status":            "运行中",
	}
}

func (s *agentService) Apply(ctx context.Context, agentID string) (ApplyAgentResult, error) {
	isLocal := s.cfg.EnableLocalAgent && agentID == s.cfg.LocalAgentID
	var row storage.AgentRow
	if !isLocal {
		var err error
		row, err = s.findAgentByID(ctx, agentID)
		if err != nil {
			return ApplyAgentResult{}, err
		}
	}
	if s.revisionActions != nil {
		pointer, found, err := s.revisionActions.GetAgentRevisionPointer(ctx, agentID)
		if err != nil {
			return ApplyAgentResult{}, err
		}
		if found {
			desired := pointer.DesiredRevision
			revisionRow, revisionFound, err := s.revisionActions.GetCoordinatorRevision(ctx, agentID, desired)
			if err != nil {
				return ApplyAgentResult{}, err
			}
			message := "current desired revision is already scheduled"
			if revisionFound && revisionRow.State == storage.AgentRevisionStateFailed {
				if _, err := s.revisionActions.RetryCoordinatorRevision(ctx, agentID, desired, s.now().UTC()); err != nil {
					return ApplyAgentResult{}, err
				}
				message = "retry scheduled for current desired revision"
			}
			return ApplyAgentResult{Message: message, DesiredRevision: desired, Pending: true}, nil
		}
	}

	var snapshot storage.Snapshot
	var err error
	if isLocal {
		snapshot, err = s.store.LoadLocalSnapshot(ctx, s.cfg.LocalAgentID)
	} else {
		snapshot, err = s.store.LoadAgentSnapshot(ctx, row.ID, storage.AgentSnapshotInput{
			DesiredVersion: row.DesiredVersion, DesiredRevision: row.DesiredRevision,
			CurrentRevision: row.CurrentRevision, Platform: row.Platform,
		})
	}
	if err != nil {
		return ApplyAgentResult{}, err
	}
	return ApplyAgentResult{
		Message:         "current desired revision is already scheduled",
		DesiredRevision: snapshot.Revision,
		Pending:         true,
	}, nil
}

func buildAgentSettingsSnapshot(ctx context.Context, tx *storage.GormStore, target revision.Target) (storage.Snapshot, error) {
	return buildAgentSettingsSnapshotMode(ctx, tx, target, false)
}

func buildAgentSettingsIntentSnapshot(ctx context.Context, tx *storage.GormStore, target revision.Target) (storage.Snapshot, error) {
	return buildAgentSettingsSnapshotMode(ctx, tx, target, true)
}

func buildAgentSettingsSnapshotMode(ctx context.Context, tx *storage.GormStore, target revision.Target, intent bool) (storage.Snapshot, error) {
	if target.Local {
		state, err := tx.LoadLocalAgentState(ctx)
		if err != nil {
			return storage.Snapshot{}, err
		}
		desiredVersion := state.DesiredVersion
		desiredRevision := state.DesiredRevision
		rows, err := tx.ListAgents(ctx)
		if err != nil {
			return storage.Snapshot{}, err
		}
		for _, row := range rows {
			if row.ID != target.AgentID {
				continue
			}
			desiredVersion = row.DesiredVersion
			if row.DesiredRevision > desiredRevision {
				desiredRevision = row.DesiredRevision
			}
			break
		}
		input := storage.AgentSnapshotInput{
			DesiredVersion: desiredVersion, DesiredRevision: desiredRevision,
			CurrentRevision: state.CurrentRevision, Platform: runtime.GOOS + "-" + runtime.GOARCH,
		}
		if intent {
			return tx.LoadAgentIntentSnapshot(ctx, target.AgentID, input)
		}
		return tx.LoadAgentSnapshot(ctx, target.AgentID, input)
	}
	rows, err := tx.ListAgents(ctx)
	if err != nil {
		return storage.Snapshot{}, err
	}
	for _, row := range rows {
		if row.ID != target.AgentID {
			continue
		}
		input := storage.AgentSnapshotInput{
			DesiredVersion: row.DesiredVersion, DesiredRevision: row.DesiredRevision,
			CurrentRevision: row.CurrentRevision, Platform: row.Platform,
		}
		if intent {
			return tx.LoadAgentIntentSnapshot(ctx, row.ID, input)
		}
		return tx.LoadAgentSnapshot(ctx, row.ID, input)
	}
	return storage.Snapshot{}, ErrAgentNotFound
}

func agentSettingsCapabilityState(ctx context.Context, tx *storage.GormStore, target revision.Target) (any, error) {
	if target.Local {
		return canonicalCapabilities(defaultLocalCapabilities), nil
	}
	rows, err := tx.ListAgents(ctx)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		if row.ID == target.AgentID {
			return canonicalCapabilities(parseStringArray(row.CapabilitiesJSON)), nil
		}
	}
	return nil, ErrAgentNotFound
}

func (s *agentService) localSettingsRow(ctx context.Context) (storage.AgentRow, error) {
	rows, err := s.store.ListAgents(ctx)
	if err != nil {
		return storage.AgentRow{}, err
	}
	for _, row := range rows {
		if row.ID == s.cfg.LocalAgentID {
			return row, nil
		}
	}
	state, err := s.store.LoadLocalAgentState(ctx)
	if err != nil {
		return storage.AgentRow{}, err
	}
	return localAgentSettingsRow(s.cfg, state), nil
}

func localAgentSettingsRow(cfg config.Config, state storage.LocalAgentStateRow) storage.AgentRow {
	return storage.AgentRow{
		ID: cfg.LocalAgentID, Name: cfg.LocalAgentName,
		DesiredVersion: state.DesiredVersion, DesiredRevision: state.DesiredRevision,
		CurrentRevision: state.CurrentRevision, LastApplyRevision: state.LastApplyRevision,
		LastApplyStatus: state.LastApplyStatus, LastApplyMessage: state.LastApplyMessage,
		Mode: "local", IsLocal: true, CapabilitiesJSON: marshalStringArray(defaultLocalCapabilities),
	}
}

func (s *agentService) Heartbeat(ctx context.Context, request HeartbeatRequest, agentToken string) (HeartbeatReply, error) {
	if strings.TrimSpace(agentToken) == "" {
		return HeartbeatReply{}, ErrAgentUnauthorized
	}

	row, err := s.findAgentByToken(ctx, agentToken)
	if err != nil {
		return HeartbeatReply{}, err
	}

	previousRow := row
	row.Version = defaultString(request.Version, row.Version)
	row.Platform = defaultString(request.Platform, row.Platform)
	row.RuntimePackageVersion = defaultString(request.RuntimePackage.Version, row.RuntimePackageVersion)
	row.RuntimePackagePlatform = defaultString(request.RuntimePackage.Platform, row.RuntimePackagePlatform)
	row.RuntimePackageArch = defaultString(request.RuntimePackage.Arch, row.RuntimePackageArch)
	row.RuntimePackageSHA256 = defaultString(request.RuntimePackage.SHA256, row.RuntimePackageSHA256)
	hasAgentURL := request.HasAgentURL || strings.TrimSpace(request.AgentURL) != ""
	if hasAgentURL {
		agentURL := trimTrailingSlash(request.AgentURL)
		if agentURL != "" && !validateAgentURL(agentURL) {
			return HeartbeatReply{}, fmt.Errorf("%w: agent_url must be a valid http/https URL", ErrInvalidArgument)
		}
		row.AgentURL = agentURL
		row.Mode = resolveRemoteAgentMode(agentURL)
	}
	hasTags := request.HasTags || len(request.Tags) > 0
	if hasTags {
		row.TagsJSON = marshalStringArray(normalizeAgentTags(request.Tags))
	}
	hasCapabilities := request.HasCapabilities || len(request.Capabilities) > 0
	if hasCapabilities {
		nextCapabilities := normalizeCapabilities(request.Capabilities)
		row.CapabilitiesJSON = marshalStringArray(nextCapabilities)
	}
	trafficStatsEnabled := s.cfg.TrafficStatsEnabled
	currentSeenAt := s.now().UTC().Format(time.RFC3339)
	if request.Stats != nil {
		persistedStats := monitorPersistentAgentStats(request.Stats)
		persistedStats = statsWithMonitorRates(persistedStats, parseAgentStats(previousRow.LastReportedStatsJSON), previousRow.LastSeenAt, currentSeenAt)
		if len(persistedStats) == 0 {
			row.LastReportedStatsJSON = ""
		} else {
			row.LastReportedStatsJSON = marshalAgentStats(persistedStats)
		}
		if trafficStatsEnabled && s.trafficService != nil {
			if err := s.trafficService.IngestHeartbeat(ctx, row.ID, request.Stats); err != nil {
				return HeartbeatReply{}, err
			}
		}
	}
	if strings.TrimSpace(request.LastSeenIP) != "" {
		row.LastSeenIP = strings.TrimSpace(request.LastSeenIP)
	}
	// Agent-reported address families overwrite only when non-empty, mirroring the
	// storage layer: a family transiently absent this cycle keeps its last value.
	if v4 := strings.TrimSpace(request.LastSeenIPv4); v4 != "" {
		row.LastSeenIPv4 = v4
	}
	if v6 := strings.TrimSpace(request.LastSeenIPv6); v6 != "" {
		row.LastSeenIPv6 = v6
	}
	row.CurrentRevision = int(request.CurrentRevision)
	if request.LastApplyRevision > 0 {
		row.LastApplyRevision = int(request.LastApplyRevision)
	} else if row.LastApplyRevision <= 0 {
		row.LastApplyRevision = int(request.CurrentRevision)
	}
	row.LastApplyStatus = defaultString(request.LastApplyStatus, row.LastApplyStatus)
	row.LastApplyMessage = request.LastApplyMessage
	row.LastSeenAt = currentSeenAt

	if heartbeatStore, ok := s.store.(agentHeartbeatStore); ok && !agentHeartbeatRequiresFullSave(previousRow, row) {
		if err := heartbeatStore.SaveAgentHeartbeat(ctx, row); err != nil {
			return HeartbeatReply{}, err
		}
	} else {
		if err := s.store.SaveAgent(ctx, row); err != nil {
			return HeartbeatReply{}, err
		}
	}
	// Fire-and-forget: fresh reported IPs may warrant a master-side A/AAAA
	// refresh. This MUST NOT affect the heartbeat return — triggerDDNSReconcile
	// swallows panics and the reconciler handles its own errors (R-主链路阻塞).
	s.triggerDDNSReconcile(ctx, row.ID)
	if err := s.reconcileManagedCertificatesFromHeartbeat(ctx, row, request); err != nil {
		return HeartbeatReply{}, err
	}

	snapshot, err := s.loadHeartbeatSnapshot(ctx, row)
	if err != nil {
		return HeartbeatReply{}, err
	}
	pkiDegraded := false
	if s.pki != nil {
		snapshot.RelayListeners, err = s.pki.PrepareRelayListeners(ctx, row.ID, snapshot.RelayListeners)
		if err != nil {
			pkiDegraded = true
			snapshot.RelayListeners = nil
		}
	}

	trafficBlocked, trafficBlockReason, err := s.heartbeatTrafficBlockState(ctx, row.ID, trafficStatsEnabled)
	if err != nil {
		return HeartbeatReply{}, err
	}
	if err := s.persistHeartbeatTrafficBlockState(ctx, &row, trafficBlocked, trafficBlockReason); err != nil {
		return HeartbeatReply{}, err
	}
	s.broadcastMonitorUpdate(ctx, row)
	reply := HeartbeatReply{
		HasUpdate:            request.CurrentRevision < snapshot.Revision || !strings.EqualFold(strings.TrimSpace(row.LastApplyStatus), "success"),
		DesiredVersion:       snapshot.DesiredVersion,
		DesiredRevision:      snapshot.Revision,
		CurrentRevision:      int64(row.CurrentRevision),
		Rules:                snapshot.Rules,
		L4Rules:              snapshot.L4Rules,
		RelayListeners:       snapshot.RelayListeners,
		EgressProfiles:       snapshot.EgressProfiles,
		Certificates:         snapshot.Certificates,
		CertificatePolicies:  snapshot.CertificatePolicies,
		DDNSConfig:           snapshot.DDNSConfig,
		OutboundProxyURL:     strings.TrimSpace(row.OutboundProxyURL),
		TrafficStatsInterval: strings.TrimSpace(row.TrafficStatsInterval),
		TrafficStatsEnabled:  heartbeatBoolPtr(trafficStatsEnabled),
		TrafficBlocked:       trafficBlocked,
		TrafficBlockReason:   trafficBlockReason,
	}
	if s.pki != nil {
		for index := range request.PKIEnrollmentRequests {
			request.PKIEnrollmentRequests[index].controlToken = agentToken
		}
		pkiSnapshot, credentials, err := s.pki.ControlSync(ctx, row.ID, request.PKISecurityAck, request.PKIEnrollmentRequests)
		if err != nil {
			if isPKIControlClientError(err) {
				return HeartbeatReply{}, err
			}
			pkiDegraded = true
			reply.RelayListeners = nil
		} else {
			if pkiSnapshot.PKIDomainID != "" {
				reply.PKISecurity = &pkiSnapshot
			}
			reply.PKICredentials = credentials
		}
		if pkiDegraded {
			reply.PKIStatus = &PKIControlStatus{Status: "degraded", Code: "runtime_unavailable", RecoveryHint: "retry ordinary control sync; relay credentials remain disabled"}
		} else {
			reply.PKIStatus = &PKIControlStatus{Status: "ready"}
		}
	}
	if snapshot.VersionPackage != nil {
		pkgCopy := *snapshot.VersionPackage
		reply.VersionPackage = pkgCopy.URL
		reply.VersionSHA256 = pkgCopy.SHA256
		reply.VersionPackageMeta = &pkgCopy
	}
	if !reply.HasUpdate {
		reply.Rules = nil
		reply.L4Rules = nil
		reply.EgressProfiles = nil
		reply.Certificates = nil
		reply.CertificatePolicies = nil
		reply.DDNSConfig = nil
	}
	return reply, nil
}

func isPKIControlClientError(err error) bool {
	return errors.Is(err, ErrInvalidArgument) || errors.Is(err, ErrPKIEpochStale) ||
		errors.Is(err, ErrPKIEnrollmentRequest) || errors.Is(err, ErrPKIEnrollmentCSR) ||
		errors.Is(err, ErrPKIEnrollmentOwnerMismatch) || errors.Is(err, ErrPKIEnrollmentTokenRejected) ||
		errors.Is(err, ErrPKIEnrollmentPublicKeyReuse)
}

func (s *agentService) persistHeartbeatTrafficBlockState(ctx context.Context, row *storage.AgentRow, blocked bool, reason string) error {
	if row == nil {
		return nil
	}
	reason = strings.TrimSpace(reason)
	if row.TrafficBlocked == blocked && row.TrafficBlockReason == reason {
		return nil
	}
	previousBlocked := row.TrafficBlocked
	previousReason := row.TrafficBlockReason
	row.TrafficBlocked = blocked
	row.TrafficBlockReason = reason
	if err := s.store.SaveAgent(ctx, *row); err != nil {
		return err
	}
	if previousBlocked != blocked || previousReason != reason {
		if err := s.recordTrafficEvent(ctx, row.ID, "traffic_block_state_changed", "traffic block state changed", map[string]any{
			"previous_blocked": previousBlocked,
			"previous_reason":  previousReason,
			"blocked":          blocked,
			"reason":           reason,
		}); err != nil {
			return err
		}
	}
	return nil
}

func (s *agentService) recordTrafficEvent(ctx context.Context, agentID, eventType, message string, payload map[string]any) error {
	eventStore, ok := s.store.(trafficEventStore)
	if !ok {
		return nil
	}
	payloadJSON, _ := json.Marshal(payload)
	return eventStore.SaveTrafficEvent(ctx, storage.AgentTrafficEventRow{
		AgentID:   agentID,
		EventType: eventType,
		Message:   message,
		Payload:   string(payloadJSON),
		CreatedAt: s.now().UTC().Format(time.RFC3339),
	})
}

func (s *agentService) heartbeatTrafficBlockState(ctx context.Context, agentID string, enabled bool) (bool, string, error) {
	if !enabled || s.trafficService == nil {
		return false, "", nil
	}
	blocked, reason, err := s.trafficService.BlockState(ctx, agentID)
	if err != nil {
		if errors.Is(err, ErrTrafficStatsDisabled) {
			return false, "", nil
		}
		return false, "", nil
	}
	if !blocked {
		return false, "", nil
	}
	return true, reason, nil
}

func heartbeatBoolPtr(value bool) *bool {
	return &value
}

func (s *agentService) reconcileManagedCertificatesFromHeartbeat(ctx context.Context, row storage.AgentRow, request HeartbeatRequest) error {
	rules, err := s.store.ListHTTPRules(ctx, row.ID)
	if err != nil {
		return err
	}

	capabilities := parseStringArray(row.CapabilitiesJSON)
	now := s.now()
	update := func(rows []storage.ManagedCertificateRow) ([]storage.ManagedCertificateRow, bool, error) {
		nextRows, reportedCertIDs, changed := applyManagedCertificateHeartbeatReports(rows, row.ID, request.ManagedCertificateReports, now)
		nextRows, reconciled := reconcileLocalHTTP01CertificatesForAgent(nextRows, row.ID, capabilities, rules, row.LastApplyRevision, row.LastApplyStatus, row.LastApplyMessage, reportedCertIDs, now)
		return nextRows, changed || reconciled, nil
	}
	if atomicStore, ok := s.store.(storage.ManagedCertificateUpdateStore); ok {
		if err := atomicStore.UpdateManagedCertificates(ctx, update); err != nil {
			return err
		}
	} else {
		if err := func() error {
			s.managedCertificateUpdateMu.Lock()
			defer s.managedCertificateUpdateMu.Unlock()
			rows, err := s.store.ListManagedCertificates(ctx)
			if err != nil {
				return err
			}
			nextRows, changed, err := update(rows)
			if err != nil {
				return err
			}
			if !changed {
				return nil
			}
			return s.store.SaveManagedCertificates(ctx, nextRows)
		}(); err != nil {
			return err
		}
	}
	fullStore, ok := s.store.(storage.Store)
	if !ok {
		return nil
	}
	if _, ok := s.store.(storage.ManagedCertificateGenerationStore); !ok {
		return nil
	}
	_, err = NewCertificateService(s.cfg, fullStore).reconcileManagedCertificateGenerationPromotions(ctx)
	return err
}

func (s *agentService) loadHeartbeatSnapshot(ctx context.Context, row storage.AgentRow) (storage.Snapshot, error) {
	snapshot, err := s.store.LoadAgentSnapshot(ctx, row.ID, storage.AgentSnapshotInput{
		DesiredVersion:  row.DesiredVersion,
		DesiredRevision: row.DesiredRevision,
		CurrentRevision: row.CurrentRevision,
		Platform:        row.Platform,
	})
	if err != nil {
		return storage.Snapshot{}, err
	}
	snapshot, err = overlayPendingManagedCertificateGenerationsForConfig(ctx, s.cfg, s.store, row.ID, snapshot)
	if err != nil {
		return storage.Snapshot{}, err
	}
	snapshot.VersionPackage = s.resolveDesiredPackage(snapshot.VersionPackage, row.Platform)
	return snapshot, nil
}

func (s *agentService) ensureAgentExists(ctx context.Context, agentID string) error {
	if s.cfg.EnableLocalAgent && agentID == s.cfg.LocalAgentID {
		return nil
	}

	rows, err := s.store.ListAgents(ctx)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if row.ID == agentID {
			return nil
		}
	}
	return ErrAgentNotFound
}

func (s *agentService) findAgentByToken(ctx context.Context, agentToken string) (storage.AgentRow, error) {
	rows, err := s.store.ListAgents(ctx)
	if err != nil {
		return storage.AgentRow{}, err
	}
	for _, row := range rows {
		// Local snapshots contain control-plane-owned secrets and are consumed
		// exclusively through the in-process bridge. Local rows must therefore
		// never participate in any public token-authenticated endpoint, including
		// when upgrading from a database that still contains the legacy "local"
		// token.
		if row.IsLocal || row.ID == s.cfg.LocalAgentID || strings.EqualFold(strings.TrimSpace(row.Mode), "local") {
			continue
		}
		if row.AgentToken == agentToken {
			return row, nil
		}
	}
	return storage.AgentRow{}, ErrAgentNotFound
}

func (s *agentService) findAgentByID(ctx context.Context, agentID string) (storage.AgentRow, error) {
	rows, err := s.store.ListAgents(ctx)
	if err != nil {
		return storage.AgentRow{}, err
	}
	for _, row := range rows {
		if row.ID == agentID {
			return row, nil
		}
	}
	return storage.AgentRow{}, ErrAgentNotFound
}

func (s *agentService) localSummary(ctx context.Context) (AgentSummary, error) {
	localState, err := s.store.LoadLocalAgentState(ctx)
	if err != nil {
		return AgentSummary{}, err
	}
	localRules, err := s.store.ListHTTPRules(ctx, s.cfg.LocalAgentID)
	if err != nil {
		return AgentSummary{}, err
	}
	localL4Rules, err := s.store.ListL4Rules(ctx, s.cfg.LocalAgentID)
	if err != nil {
		return AgentSummary{}, err
	}
	agentRows, err := s.store.ListAgents(ctx)
	if err != nil {
		return AgentSummary{}, err
	}
	var settings storage.AgentRow
	for _, row := range agentRows {
		if row.ID == s.cfg.LocalAgentID {
			settings = row
			break
		}
	}
	desiredVersion := localState.DesiredVersion
	desiredRevision := localState.DesiredRevision
	if settings.ID != "" {
		desiredVersion = settings.DesiredVersion
		if settings.DesiredRevision > desiredRevision {
			desiredRevision = settings.DesiredRevision
		}
	}
	ddnsConfig := parseDDNSConfig(settings.DdnsConfigJSON)
	ddnsDomain := ""
	if ddnsConfig != nil {
		ddnsDomain = strings.TrimSpace(ddnsConfig.Domain)
	}
	return AgentSummary{
		ID:                   s.cfg.LocalAgentID,
		Name:                 s.cfg.LocalAgentName,
		Version:              settings.Version,
		Platform:             settings.Platform,
		DesiredVersion:       desiredVersion,
		OutboundProxyURL:     strings.TrimSpace(settings.OutboundProxyURL),
		TrafficStatsInterval: strings.TrimSpace(settings.TrafficStatsInterval),
		Mode:                 "local",
		DesiredRevision:      desiredRevision,
		CurrentRevision:      localState.CurrentRevision,
		LastApplyRevision:    localState.LastApplyRevision,
		LastApplyStatus:      localState.LastApplyStatus,
		LastApplyMessage:     localState.LastApplyMessage,
		Status:               "online",
		IsLocal:              true,
		LastSeenIP:           settings.LastSeenIP,
		LastSeenIPv4:         settings.LastSeenIPv4,
		LastSeenIPv6:         settings.LastSeenIPv6,
		DdnsDomain:           ddnsDomain,
		DdnsStatus:           parseDdnsStatus(settings.DdnsStatusJSON),
		DdnsConfig:           ddnsConfig,
		Capabilities:         append([]string(nil), defaultLocalCapabilities...),
		HTTPRulesCount:       len(localRules),
		L4RulesCount:         len(localL4Rules),
	}, nil
}

func (s *agentService) summaryForRow(ctx context.Context, row storage.AgentRow) (AgentSummary, error) {
	return s.summaryForRowWithStore(ctx, s.store, row)
}

func (s *agentService) summaryForRowWithStore(ctx context.Context, store agentStore, row storage.AgentRow) (AgentSummary, error) {
	rules, err := store.ListHTTPRules(ctx, row.ID)
	if err != nil {
		return AgentSummary{}, err
	}
	l4Rules, err := store.ListL4Rules(ctx, row.ID)
	if err != nil {
		return AgentSummary{}, err
	}
	snapshot, err := store.LoadAgentSnapshot(ctx, row.ID, storage.AgentSnapshotInput{
		DesiredVersion:  row.DesiredVersion,
		DesiredRevision: row.DesiredRevision,
		CurrentRevision: row.CurrentRevision,
		Platform:        row.Platform,
	})
	if err != nil {
		return AgentSummary{}, err
	}
	snapshot.VersionPackage = s.resolveDesiredPackage(snapshot.VersionPackage, row.Platform)
	desiredPackageSHA256 := ""
	packageSyncStatus := ""
	if snapshot.VersionPackage != nil {
		desiredPackageSHA256 = strings.TrimSpace(snapshot.VersionPackage.SHA256)
		packageSyncStatus = derivePackageSyncStatus(row, snapshot.VersionPackage)
	}
	// DDNS fields: domain is flattened for display; the full dispatched config is
	// also exposed so the edit form can seed and round-trip family state without
	// clobbering it; resolution status comes from the master-written runtime
	// column. None carries a Cloudflare credential — DDNSConfig holds only domain
	// + per-family {enabled,source,interface} (R7).
	ddnsDomain := ""
	if snapshot.DDNSConfig != nil {
		ddnsDomain = strings.TrimSpace(snapshot.DDNSConfig.Domain)
	}

	return AgentSummary{
		ID:                     row.ID,
		Name:                   row.Name,
		AgentURL:               row.AgentURL,
		Version:                row.Version,
		Platform:               row.Platform,
		RuntimePackageVersion:  row.RuntimePackageVersion,
		RuntimePackagePlatform: row.RuntimePackagePlatform,
		RuntimePackageArch:     row.RuntimePackageArch,
		RuntimePackageSHA256:   row.RuntimePackageSHA256,
		DesiredPackageSHA256:   desiredPackageSHA256,
		PackageSyncStatus:      packageSyncStatus,
		DesiredVersion:         row.DesiredVersion,
		Tags:                   parseStringArray(row.TagsJSON),
		OutboundProxyURL:       strings.TrimSpace(row.OutboundProxyURL),
		TrafficStatsInterval:   strings.TrimSpace(row.TrafficStatsInterval),
		Mode:                   defaultString(row.Mode, "pull"),
		DesiredRevision:        row.DesiredRevision,
		CurrentRevision:        row.CurrentRevision,
		LastApplyRevision:      row.LastApplyRevision,
		LastApplyStatus:        row.LastApplyStatus,
		LastApplyMessage:       row.LastApplyMessage,
		LastSeenAt:             row.LastSeenAt,
		Status:                 s.agentStatus(row),
		Error:                  "",
		IsLocal:                false,
		LastSeenIP:             row.LastSeenIP,
		LastSeenIPv4:           row.LastSeenIPv4,
		LastSeenIPv6:           row.LastSeenIPv6,
		DdnsDomain:             ddnsDomain,
		DdnsStatus:             parseDdnsStatus(row.DdnsStatusJSON),
		DdnsConfig:             snapshot.DDNSConfig,
		Capabilities:           normalizeCapabilities(parseStringArray(row.CapabilitiesJSON)),
		HTTPRulesCount:         len(rules),
		L4RulesCount:           len(l4Rules),
	}, nil
}

func RedactAgentSummary(agent AgentSummary) AgentSummary {
	agent.RegistrationControlToken = ""
	agent.PKIRegistration = nil
	parsed, err := url.Parse(agent.OutboundProxyURL)
	if err != nil || parsed.User == nil {
		return agent
	}
	password, ok := parsed.User.Password()
	if !ok || password == "" {
		return agent
	}
	parsed.User = url.UserPassword(parsed.User.Username(), "xxxxx")
	agent.OutboundProxyURL = parsed.String()
	return agent
}

// parseDdnsStatus decodes the master-written runtime DDNS status column for
// display. Empty/malformed values yield a zero status (omitted from JSON). The
// status never contains credentials — it is runtime resolution state only (R7).
func parseDdnsStatus(raw string) storage.DdnsStatus {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return storage.DdnsStatus{}
	}
	var status storage.DdnsStatus
	if err := json.Unmarshal([]byte(raw), &status); err != nil {
		return storage.DdnsStatus{}
	}
	return status
}

func derivePackageSyncStatus(row storage.AgentRow, pkg *storage.VersionPackage) string {
	if pkg == nil || strings.TrimSpace(pkg.SHA256) == "" {
		return ""
	}
	if strings.EqualFold(strings.TrimSpace(row.RuntimePackageSHA256), strings.TrimSpace(pkg.SHA256)) {
		return "aligned"
	}
	return "pending"
}

func (s *agentService) resolveDesiredPackage(pkg *storage.VersionPackage, platform string) *storage.VersionPackage {
	if !supportsBundledAgentPackage(platform) {
		return nil
	}
	if pkg != nil && strings.TrimSpace(pkg.SHA256) != "" {
		copyValue := *pkg
		return &copyValue
	}
	return s.bundledAgentPackageInfo(platform)
}

func supportsBundledAgentPackage(platform string) bool {
	switch strings.ToLower(strings.TrimSpace(platform)) {
	case "linux-amd64", "linux-arm64":
		// Heartbeat delivery is the compatibility channel that upgrades legacy
		// Linux agents before they can advertise package_manifest_v1.
		return true
	default:
		return false
	}
}

var fileSHA256Func = fileSHA256

func (s *agentService) bundledAgentPackageInfo(platform string) *storage.VersionPackage {
	return bundledAgentPackageInfoCached(s.cfg.PublicAgentAssetsDir, platform, &s.bundledCacheMu, s.bundledCache)
}

func bundledAgentPackageInfoCached(assetRoot string, platform string, mu *sync.Mutex, cache map[string]bundledPackageCacheEntry) *storage.VersionPackage {
	normalizedPlatform := strings.TrimSpace(platform)
	normalizedRoot := strings.TrimSpace(assetRoot)
	if normalizedPlatform == "" || normalizedRoot == "" {
		return nil
	}
	if !isSafeBundledAgentPlatform(normalizedPlatform) {
		return nil
	}
	filename := "nre-agent-" + normalizedPlatform
	assetPath := filepath.Join(normalizedRoot, filename)
	info, err := os.Stat(assetPath)
	if err != nil || info.IsDir() {
		return nil
	}
	cacheKey := filepath.Clean(assetPath)
	if mu != nil && cache != nil {
		mu.Lock()
		if entry, ok := cache[cacheKey]; ok && entry.size == info.Size() && entry.modTimeUnixNano == info.ModTime().UnixNano() {
			copyValue := entry.pkg
			mu.Unlock()
			return &copyValue
		}
		mu.Unlock()
	}

	shaValue, err := fileSHA256Func(assetPath)
	if err != nil || shaValue == "" {
		return nil
	}
	pkg := storage.VersionPackage{
		Platform: normalizedPlatform,
		URL:      "/panel-api/public/agent-assets/" + filename,
		SHA256:   shaValue,
		Filename: filename,
		Size:     info.Size(),
	}
	if mu != nil && cache != nil {
		mu.Lock()
		cache[cacheKey] = bundledPackageCacheEntry{
			modTimeUnixNano: info.ModTime().UnixNano(),
			size:            info.Size(),
			pkg:             pkg,
		}
		mu.Unlock()
	}
	return &pkg
}

func isSafeBundledAgentPlatform(platform string) bool {
	if platform == "." || platform == ".." || strings.Contains(platform, "..") {
		return false
	}
	for _, r := range platform {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '-' || r == '_' || r == '.':
		default:
			return false
		}
	}
	return true
}

func fileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()

	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

type agentRelayRuleReference struct {
	AgentID  string
	RuleID   int
	RuleType string
}

func (s *agentService) findRelayListenerReference(ctx context.Context, excludedAgentID string, listenerID int) (*agentRelayRuleReference, error) {
	agentIDs, err := s.allKnownAgentIDs(ctx)
	if err != nil {
		return nil, err
	}
	for _, agentID := range agentIDs {
		if agentID == excludedAgentID {
			continue
		}
		httpRules, err := s.store.ListHTTPRules(ctx, agentID)
		if err != nil {
			return nil, err
		}
		for _, row := range httpRules {
			if relayLayersReferenceListener(row.RelayLayersJSON, listenerID) {
				return &agentRelayRuleReference{AgentID: agentID, RuleID: row.ID, RuleType: "HTTP"}, nil
			}
		}
		l4Rules, err := s.store.ListL4Rules(ctx, agentID)
		if err != nil {
			return nil, err
		}
		for _, row := range l4Rules {
			if relayLayersReferenceListener(row.RelayLayersJSON, listenerID) {
				return &agentRelayRuleReference{AgentID: agentID, RuleID: row.ID, RuleType: "L4"}, nil
			}
		}
	}
	return nil, nil
}

func (s *agentService) allKnownAgentIDs(ctx context.Context) ([]string, error) {
	return allKnownAgentIDs(ctx, s.cfg, s.store)
}

func (s *agentService) agentStatus(row storage.AgentRow) string {
	lastSeenAt := strings.TrimSpace(row.LastSeenAt)
	if lastSeenAt == "" {
		return "offline"
	}
	lastSeen, err := time.Parse(time.RFC3339, lastSeenAt)
	if err != nil {
		return "offline"
	}

	timeout := s.cfg.HeartbeatInterval * 3
	if timeout <= 0 {
		timeout = 90 * time.Second
	}
	if s.now().Sub(lastSeen) <= timeout {
		return "online"
	}
	return "offline"
}

func defaultString(value string, fallback string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return fallback
	}
	return trimmed
}

func parseStringArray(raw string) []string {
	var values []string
	if err := json.Unmarshal([]byte(defaultString(raw, "[]")), &values); err != nil {
		return []string{}
	}
	if values == nil {
		return []string{}
	}
	return values
}

func parseIntArray(raw string) []int {
	var values []int
	if err := json.Unmarshal([]byte(defaultString(raw, "[]")), &values); err != nil {
		return []int{}
	}
	if values == nil {
		return []int{}
	}
	return values
}

func parseIntLayers(raw string) [][]int {
	var values [][]int
	if err := json.Unmarshal([]byte(defaultString(raw, "[]")), &values); err != nil {
		return [][]int{}
	}
	if values == nil {
		return [][]int{}
	}
	return values
}

func parseBackends(raw string) []HTTPRuleBackend {
	type backend struct {
		URL string `json:"url"`
	}

	var values []backend
	if err := json.Unmarshal([]byte(defaultString(raw, "[]")), &values); err != nil {
		return []HTTPRuleBackend{}
	}

	normalized := make([]HTTPRuleBackend, 0, len(values))
	for _, item := range values {
		url := strings.TrimSpace(item.URL)
		if url == "" {
			continue
		}
		normalized = append(normalized, HTTPRuleBackend{URL: url})
	}
	return normalized
}

func parseLoadBalancing(raw string) HTTPLoadBalancing {
	value := struct {
		Strategy string `json:"strategy"`
	}{Strategy: "adaptive"}
	if err := json.Unmarshal([]byte(defaultString(raw, "{}")), &value); err != nil {
		return HTTPLoadBalancing{Strategy: "adaptive"}
	}
	switch strings.ToLower(strings.TrimSpace(value.Strategy)) {
	case "round_robin", "random", "adaptive":
		value.Strategy = strings.ToLower(strings.TrimSpace(value.Strategy))
	default:
		value.Strategy = "adaptive"
	}
	return HTTPLoadBalancing{Strategy: value.Strategy}
}

func parseCustomHeaders(raw string) []HTTPCustomHeader {
	type header struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	}

	var values []header
	if err := json.Unmarshal([]byte(defaultString(raw, "[]")), &values); err != nil {
		return []HTTPCustomHeader{}
	}

	normalized := make([]HTTPCustomHeader, 0, len(values))
	for _, item := range values {
		name := strings.TrimSpace(item.Name)
		if name == "" {
			continue
		}
		normalized = append(normalized, HTTPCustomHeader{
			Name:  name,
			Value: item.Value,
		})
	}
	return normalized
}

func marshalStringArray(values []string) string {
	data, err := json.Marshal(values)
	if err != nil {
		return "[]"
	}
	return string(data)
}

func normalizeAgentTags(values []string) []string {
	seen := map[string]struct{}{}
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		item := strings.TrimSpace(value)
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		normalized = append(normalized, item)
	}
	return normalized
}

func normalizeCapabilities(values []string) []string {
	allowed := map[string]struct{}{
		"http_rules":                        {},
		"local_acme":                        {},
		"cert_install":                      {},
		managedCertificateReportsCapability: {},
		"l4":                                {},
		"relay_quic":                        {},
		"egress_profiles":                   {},
		"http3_ingress":                     {},
		packageManifestCapability:           {},
	}
	seen := map[string]struct{}{}
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		item := strings.TrimSpace(value)
		if _, ok := allowed[item]; !ok {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		normalized = append(normalized, item)
	}
	return normalized
}

func canonicalCapabilities(values []string) []string {
	normalized := normalizeCapabilities(values)
	slices.Sort(normalized)
	return normalized
}

func parseAgentStats(raw string) AgentStats {
	var stats AgentStats
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &stats); err != nil {
		return nil
	}
	if len(stats) == 0 {
		return nil
	}
	return stats
}

func marshalAgentStats(stats AgentStats) string {
	data, err := json.Marshal(stats)
	if err != nil {
		return "{}"
	}
	return string(data)
}

func monitorPersistentAgentStats(stats AgentStats) AgentStats {
	if _, ok := stats["traffic"]; !ok {
		return stats
	}
	persisted := make(AgentStats, len(stats)-1)
	for key, value := range stats {
		if key == "traffic" {
			continue
		}
		persisted[key] = value
	}
	if len(persisted) == 0 {
		return nil
	}
	return persisted
}

func agentHeartbeatRequiresFullSave(previous, next storage.AgentRow) bool {
	trim := func(value string) string {
		return strings.TrimSpace(value)
	}
	return trim(previous.AgentURL) != trim(next.AgentURL) ||
		trim(previous.TagsJSON) != trim(next.TagsJSON) ||
		trim(previous.CapabilitiesJSON) != trim(next.CapabilitiesJSON) ||
		trim(previous.Mode) != trim(next.Mode)
}

func trimTrailingSlash(value string) string {
	return strings.TrimRight(strings.TrimSpace(value), "/")
}

func validateAgentURL(value string) bool {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil {
		return false
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return false
	}
	return strings.TrimSpace(parsed.Host) != ""
}

func resolveRemoteAgentMode(agentURL string) string {
	if trimTrailingSlash(agentURL) != "" {
		return "master"
	}
	return "pull"
}

func randomAgentID() string {
	var buffer [16]byte
	if _, err := rand.Read(buffer[:]); err != nil {
		return "agent-" + time.Now().UTC().Format("20060102150405")
	}
	return hex.EncodeToString(buffer[:])
}

var agentControlTokenRandom io.Reader = rand.Reader

func randomAgentControlToken() (string, error) {
	var buffer [32]byte
	if _, err := io.ReadFull(agentControlTokenRandom, buffer[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(buffer[:]), nil
}
