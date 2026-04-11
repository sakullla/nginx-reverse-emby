package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

var ErrAgentNotFound = errors.New("agent not found")
var ErrAgentUnauthorized = errors.New("agent unauthorized")

var defaultLocalCapabilities = []string{"http_rules", "local_acme", "cert_install", "l4"}

type agentStore interface {
	ListAgents(context.Context) ([]storage.AgentRow, error)
	ListHTTPRules(context.Context, string) ([]storage.HTTPRuleRow, error)
	ListL4Rules(context.Context, string) ([]storage.L4RuleRow, error)
	ListRelayListeners(context.Context, string) ([]storage.RelayListenerRow, error)
	LoadLocalAgentState(context.Context) (storage.LocalAgentStateRow, error)
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

type AgentSummary struct {
	ID                string   `json:"id"`
	Name              string   `json:"name"`
	AgentURL          string   `json:"agent_url"`
	Version           string   `json:"version"`
	Platform          string   `json:"platform"`
	DesiredVersion    string   `json:"desired_version"`
	Tags              []string `json:"tags"`
	Mode              string   `json:"mode"`
	DesiredRevision   int      `json:"desired_revision"`
	CurrentRevision   int      `json:"current_revision"`
	LastApplyRevision int      `json:"last_apply_revision"`
	LastApplyStatus   string   `json:"last_apply_status"`
	LastApplyMessage  string   `json:"last_apply_message"`
	LastSeenAt        string   `json:"last_seen_at"`
	Status            string   `json:"status"`
	Error             string   `json:"error"`
	IsLocal           bool     `json:"is_local"`
	LastSeenIP        string   `json:"last_seen_ip"`
	Capabilities      []string `json:"capabilities"`
	HTTPRulesCount    int      `json:"http_rules_count"`
	L4RulesCount      int      `json:"l4_rules_count"`
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
	FrontendURL      string             `json:"frontend_url"`
	BackendURL       string             `json:"backend_url"`
	Backends         []HTTPRuleBackend  `json:"backends"`
	LoadBalancing    HTTPLoadBalancing  `json:"load_balancing"`
	Enabled          bool               `json:"enabled"`
	Tags             []string           `json:"tags"`
	ProxyRedirect    bool               `json:"proxy_redirect"`
	RelayChain       []int              `json:"relay_chain"`
	PassProxyHeaders bool               `json:"pass_proxy_headers"`
	UserAgent        string             `json:"user_agent"`
	CustomHeaders    []HTTPCustomHeader `json:"custom_headers"`
	Revision         int                `json:"revision"`
}

type HeartbeatRequest struct {
	Name                      string                              `json:"name"`
	AgentID                   string                              `json:"agent_id"`
	CurrentRevision           int64                               `json:"current_revision"`
	LastApplyRevision         int64                               `json:"last_apply_revision"`
	Version                   string                              `json:"version"`
	Platform                  string                              `json:"platform"`
	AgentURL                  string                              `json:"agent_url"`
	Tags                      []string                            `json:"tags"`
	Capabilities              []string                            `json:"capabilities"`
	Stats                     AgentStats                          `json:"stats"`
	LastSeenIP                string                              `json:"last_seen_ip"`
	LastApplyStatus           string                              `json:"last_apply_status"`
	LastApplyMessage          string                              `json:"last_apply_message"`
	ManagedCertificateReports []ManagedCertificateHeartbeatReport `json:"managed_certificate_reports"`
}

type HeartbeatReply struct {
	HasUpdate           bool                               `json:"has_update"`
	DesiredVersion      string                             `json:"desired_version"`
	DesiredRevision     int64                              `json:"desired_revision"`
	CurrentRevision     int64                              `json:"current_revision"`
	VersionPackage      string                             `json:"version_package,omitempty"`
	VersionPackageMeta  *storage.VersionPackage            `json:"version_package_meta,omitempty"`
	VersionSHA256       string                             `json:"version_sha256,omitempty"`
	Rules               []storage.HTTPRule                 `json:"rules"`
	L4Rules             []storage.L4Rule                   `json:"l4_rules"`
	RelayListeners      []storage.RelayListener            `json:"relay_listeners"`
	Certificates        []storage.ManagedCertificateBundle `json:"certificates"`
	CertificatePolicies []storage.ManagedCertificatePolicy `json:"certificate_policies"`
}

type RegisterRequest struct {
	Name          string   `json:"name"`
	AgentURL      string   `json:"agent_url"`
	AgentToken    string   `json:"agent_token"`
	Version       string   `json:"version"`
	Platform      string   `json:"platform"`
	Tags          []string `json:"tags"`
	Capabilities  []string `json:"capabilities"`
	Mode          string   `json:"mode"`
	RegisterToken string   `json:"register_token"`
}

type UpdateAgentRequest struct {
	Name         *string   `json:"name,omitempty"`
	AgentURL     *string   `json:"agent_url,omitempty"`
	AgentToken   *string   `json:"agent_token,omitempty"`
	Version      *string   `json:"version,omitempty"`
	Tags         *[]string `json:"tags,omitempty"`
	Capabilities *[]string `json:"capabilities,omitempty"`
}

type AgentStats map[string]any

type ApplyAgentResult struct {
	Message         string `json:"message"`
	DesiredRevision int64  `json:"desired_revision,omitempty"`
	Pending         bool   `json:"pending,omitempty"`
}

type agentService struct {
	cfg   config.Config
	store agentStore
	now   func() time.Time
}

func NewAgentService(cfg config.Config, store agentStore) *agentService {
	return &agentService{
		cfg:   cfg,
		store: store,
		now:   time.Now,
	}
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

func (s *agentService) Register(ctx context.Context, request RegisterRequest, headerAgentToken string) (AgentSummary, error) {
	name := strings.TrimSpace(request.Name)
	if name == "" {
		return AgentSummary{}, errors.New("name is required")
	}
	agentURL := trimTrailingSlash(request.AgentURL)
	if agentURL != "" && !validateAgentURL(agentURL) {
		return AgentSummary{}, errors.New("agent_url must be a valid http/https URL")
	}

	agentToken := strings.TrimSpace(request.AgentToken)
	if agentToken == "" {
		agentToken = strings.TrimSpace(headerAgentToken)
	}
	if agentToken == "" {
		return AgentSummary{}, errors.New("agent_token is required")
	}

	rows, err := s.store.ListAgents(ctx)
	if err != nil {
		return AgentSummary{}, err
	}

	row := storage.AgentRow{
		ID:               randomAgentID(),
		DesiredVersion:   "",
		TagsJSON:         marshalStringArray(normalizeAgentTags(request.Tags)),
		CapabilitiesJSON: marshalStringArray(normalizeCapabilities(defaultCapabilities(request.Capabilities))),
		Mode:             resolveRemoteAgentMode(agentURL),
		LastApplyStatus:  "success",
	}
	for _, existing := range rows {
		existingAgentURL := trimTrailingSlash(existing.AgentURL)
		if existing.AgentToken == agentToken ||
			(existingAgentURL != "" && existingAgentURL == agentURL) ||
			existing.Name == name {
			row = existing
			break
		}
	}

	row.Name = name
	row.AgentURL = agentURL
	row.AgentToken = agentToken
	row.Version = strings.TrimSpace(request.Version)
	row.Platform = strings.TrimSpace(request.Platform)
	row.TagsJSON = marshalStringArray(normalizeAgentTags(request.Tags))
	row.CapabilitiesJSON = marshalStringArray(normalizeCapabilities(defaultCapabilities(request.Capabilities)))
	row.Mode = resolveRemoteAgentMode(agentURL)
	row.IsLocal = false

	if err := s.store.SaveAgent(ctx, row); err != nil {
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
		backends := parseBackends(row.BackendsJSON)
		if len(backends) == 0 && strings.TrimSpace(row.BackendURL) != "" {
			backends = []HTTPRuleBackend{{URL: strings.TrimSpace(row.BackendURL)}}
		}
		backendURL := strings.TrimSpace(row.BackendURL)
		if backendURL == "" && len(backends) > 0 {
			backendURL = backends[0].URL
		}

		rules = append(rules, HTTPRule{
			ID:               row.ID,
			AgentID:          row.AgentID,
			FrontendURL:      row.FrontendURL,
			BackendURL:       backendURL,
			Backends:         backends,
			LoadBalancing:    parseLoadBalancing(row.LoadBalancingJSON),
			Enabled:          row.Enabled,
			Tags:             parseStringArray(row.TagsJSON),
			ProxyRedirect:    row.ProxyRedirect,
			RelayChain:       parseIntArray(row.RelayChainJSON),
			PassProxyHeaders: row.PassProxyHeaders,
			UserAgent:        row.UserAgent,
			CustomHeaders:    parseCustomHeaders(row.CustomHeadersJSON),
			Revision:         row.Revision,
		})
	}

	return rules, nil
}

func (s *agentService) Update(ctx context.Context, agentID string, input UpdateAgentRequest) (AgentSummary, error) {
	if s.cfg.EnableLocalAgent && agentID == s.cfg.LocalAgentID {
		return AgentSummary{}, fmt.Errorf("%w: 本地 Agent 不允许修改", ErrInvalidArgument)
	}

	row, err := s.findAgentByID(ctx, agentID)
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

	agentToken := strings.TrimSpace(row.AgentToken)
	if input.AgentToken != nil {
		agentToken = strings.TrimSpace(*input.AgentToken)
	}
	if agentToken == "" {
		return AgentSummary{}, fmt.Errorf("%w: agent_token is required", ErrInvalidArgument)
	}

	row.Name = name
	row.AgentURL = agentURL
	row.AgentToken = agentToken
	row.Mode = resolveRemoteAgentMode(agentURL)
	if input.Version != nil {
		row.Version = strings.TrimSpace(*input.Version)
	}
	if input.Tags != nil {
		row.TagsJSON = marshalStringArray(normalizeAgentTags(*input.Tags))
	}
	if input.Capabilities != nil {
		row.CapabilitiesJSON = marshalStringArray(normalizeCapabilities(*input.Capabilities))
	}

	if err := s.store.SaveAgent(ctx, row); err != nil {
		return AgentSummary{}, err
	}
	return s.summaryForRow(ctx, row)
}

func (s *agentService) Delete(ctx context.Context, agentID string) (AgentSummary, error) {
	if s.cfg.EnableLocalAgent && agentID == s.cfg.LocalAgentID {
		return AgentSummary{}, fmt.Errorf("%w: 本地 Agent 不允许删除", ErrInvalidArgument)
	}

	row, err := s.findAgentByID(ctx, agentID)
	if err != nil {
		return AgentSummary{}, err
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

	if err := s.store.SaveHTTPRules(ctx, agentID, nil); err != nil {
		return AgentSummary{}, err
	}
	if err := s.store.SaveL4Rules(ctx, agentID, nil); err != nil {
		return AgentSummary{}, err
	}
	if err := s.store.SaveRelayListeners(ctx, agentID, nil); err != nil {
		return AgentSummary{}, err
	}
	if err := s.store.DeleteAgent(ctx, agentID); err != nil {
		return AgentSummary{}, err
	}
	return deleted, nil
}

func (s *agentService) Stats(ctx context.Context, agentID string) (AgentStats, error) {
	if s.cfg.EnableLocalAgent && agentID == s.cfg.LocalAgentID {
		return AgentStats{
			"activeConnections": "0",
			"totalRequests":     "0",
			"status":            "运行中",
		}, nil
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

func (s *agentService) Apply(ctx context.Context, agentID string) (ApplyAgentResult, error) {
	if s.cfg.EnableLocalAgent && agentID == s.cfg.LocalAgentID {
		snapshot, err := s.store.LoadLocalSnapshot(ctx, s.cfg.LocalAgentID)
		if err != nil {
			return ApplyAgentResult{}, err
		}
		if err := s.store.SaveLocalRuntimeState(ctx, s.cfg.LocalAgentID, storage.RuntimeState{
			CurrentRevision:   snapshot.Revision,
			Status:            "success",
			LastApplyRevision: snapshot.Revision,
			LastApplyStatus:   "success",
		}); err != nil {
			return ApplyAgentResult{}, err
		}
		return ApplyAgentResult{
			Message:         "applied",
			DesiredRevision: snapshot.Revision,
		}, nil
	}

	row, err := s.findAgentByID(ctx, agentID)
	if err != nil {
		return ApplyAgentResult{}, err
	}
	snapshot, err := s.store.LoadAgentSnapshot(ctx, row.ID, storage.AgentSnapshotInput{
		DesiredVersion:  row.DesiredVersion,
		DesiredRevision: row.DesiredRevision,
		CurrentRevision: row.CurrentRevision,
		Platform:        row.Platform,
	})
	if err != nil {
		return ApplyAgentResult{}, err
	}
	if row.DesiredRevision < int(snapshot.Revision) {
		row.DesiredRevision = int(snapshot.Revision)
		if err := s.store.SaveAgent(ctx, row); err != nil {
			return ApplyAgentResult{}, err
		}
	}
	return ApplyAgentResult{
		Message:         "waiting for agent heartbeat to apply",
		DesiredRevision: snapshot.Revision,
		Pending:         true,
	}, nil
}

func (s *agentService) Heartbeat(ctx context.Context, request HeartbeatRequest, agentToken string) (HeartbeatReply, error) {
	if strings.TrimSpace(agentToken) == "" {
		return HeartbeatReply{}, ErrAgentUnauthorized
	}

	row, err := s.findAgentByToken(ctx, agentToken)
	if err != nil {
		return HeartbeatReply{}, err
	}

	row.Version = defaultString(request.Version, row.Version)
	row.Platform = defaultString(request.Platform, row.Platform)
	if request.AgentURL != "" {
		agentURL := trimTrailingSlash(request.AgentURL)
		if !validateAgentURL(agentURL) {
			return HeartbeatReply{}, fmt.Errorf("%w: agent_url must be a valid http/https URL", ErrInvalidArgument)
		}
		row.AgentURL = agentURL
		row.Mode = resolveRemoteAgentMode(agentURL)
	}
	if request.Tags != nil {
		row.TagsJSON = marshalStringArray(normalizeAgentTags(request.Tags))
	}
	if request.Capabilities != nil {
		row.CapabilitiesJSON = marshalStringArray(normalizeCapabilities(request.Capabilities))
	}
	if request.Stats != nil {
		row.LastReportedStatsJSON = marshalAgentStats(request.Stats)
	}
	if strings.TrimSpace(request.LastSeenIP) != "" {
		row.LastSeenIP = strings.TrimSpace(request.LastSeenIP)
	}
	row.CurrentRevision = int(request.CurrentRevision)
	if request.LastApplyRevision > 0 {
		row.LastApplyRevision = int(request.LastApplyRevision)
	} else if row.LastApplyRevision <= 0 {
		row.LastApplyRevision = int(request.CurrentRevision)
	}
	row.LastApplyStatus = defaultString(request.LastApplyStatus, row.LastApplyStatus)
	row.LastApplyMessage = request.LastApplyMessage
	row.LastSeenAt = s.now().UTC().Format(time.RFC3339)

	if err := s.store.SaveAgent(ctx, row); err != nil {
		return HeartbeatReply{}, err
	}
	if err := s.reconcileManagedCertificatesFromHeartbeat(ctx, row, request); err != nil {
		return HeartbeatReply{}, err
	}

	snapshot, err := s.loadHeartbeatSnapshot(ctx, row)
	if err != nil {
		return HeartbeatReply{}, err
	}

	reply := HeartbeatReply{
		HasUpdate:           request.CurrentRevision < snapshot.Revision,
		DesiredVersion:      snapshot.DesiredVersion,
		DesiredRevision:     snapshot.Revision,
		CurrentRevision:     int64(row.CurrentRevision),
		Rules:               snapshot.Rules,
		L4Rules:             snapshot.L4Rules,
		RelayListeners:      snapshot.RelayListeners,
		Certificates:        snapshot.Certificates,
		CertificatePolicies: snapshot.CertificatePolicies,
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
		reply.Certificates = nil
		reply.CertificatePolicies = nil
	}
	return reply, nil
}

func (s *agentService) reconcileManagedCertificatesFromHeartbeat(ctx context.Context, row storage.AgentRow, request HeartbeatRequest) error {
	rows, err := s.store.ListManagedCertificates(ctx)
	if err != nil {
		return err
	}
	rules, err := s.store.ListHTTPRules(ctx, row.ID)
	if err != nil {
		return err
	}

	nextRows, reportedCertIDs, changed := applyManagedCertificateHeartbeatReports(rows, row.ID, request.ManagedCertificateReports, s.now())
	nextRows, reconciled := reconcileLocalHTTP01CertificatesForAgent(nextRows, row.ID, rules, row.LastApplyRevision, row.LastApplyStatus, row.LastApplyMessage, reportedCertIDs, s.now())
	if !changed && !reconciled {
		return nil
	}
	return s.store.SaveManagedCertificates(ctx, nextRows)
}

func (s *agentService) loadHeartbeatSnapshot(ctx context.Context, row storage.AgentRow) (storage.Snapshot, error) {
	return s.store.LoadAgentSnapshot(ctx, row.ID, storage.AgentSnapshotInput{
		DesiredVersion:  row.DesiredVersion,
		DesiredRevision: row.DesiredRevision,
		CurrentRevision: row.CurrentRevision,
		Platform:        row.Platform,
	})
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
	return AgentSummary{
		ID:                s.cfg.LocalAgentID,
		Name:              s.cfg.LocalAgentName,
		DesiredVersion:    localState.DesiredVersion,
		Mode:              "local",
		DesiredRevision:   localState.DesiredRevision,
		CurrentRevision:   localState.CurrentRevision,
		LastApplyRevision: localState.LastApplyRevision,
		LastApplyStatus:   localState.LastApplyStatus,
		LastApplyMessage:  localState.LastApplyMessage,
		Status:            "online",
		IsLocal:           true,
		Capabilities:      append([]string(nil), defaultLocalCapabilities...),
		HTTPRulesCount:    len(localRules),
		L4RulesCount:      len(localL4Rules),
	}, nil
}

func (s *agentService) summaryForRow(ctx context.Context, row storage.AgentRow) (AgentSummary, error) {
	rules, err := s.store.ListHTTPRules(ctx, row.ID)
	if err != nil {
		return AgentSummary{}, err
	}
	l4Rules, err := s.store.ListL4Rules(ctx, row.ID)
	if err != nil {
		return AgentSummary{}, err
	}

	return AgentSummary{
		ID:                row.ID,
		Name:              row.Name,
		AgentURL:          row.AgentURL,
		Version:           row.Version,
		Platform:          row.Platform,
		DesiredVersion:    row.DesiredVersion,
		Tags:              parseStringArray(row.TagsJSON),
		Mode:              defaultString(row.Mode, "pull"),
		DesiredRevision:   row.DesiredRevision,
		CurrentRevision:   row.CurrentRevision,
		LastApplyRevision: row.LastApplyRevision,
		LastApplyStatus:   row.LastApplyStatus,
		LastApplyMessage:  row.LastApplyMessage,
		LastSeenAt:        row.LastSeenAt,
		Status:            s.agentStatus(row),
		Error:             "",
		IsLocal:           false,
		LastSeenIP:        row.LastSeenIP,
		Capabilities:      parseStringArray(row.CapabilitiesJSON),
		HTTPRulesCount:    len(rules),
		L4RulesCount:      len(l4Rules),
	}, nil
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
			if containsInt(parseIntArray(row.RelayChainJSON), listenerID) {
				return &agentRelayRuleReference{AgentID: agentID, RuleID: row.ID, RuleType: "HTTP"}, nil
			}
		}
		l4Rules, err := s.store.ListL4Rules(ctx, agentID)
		if err != nil {
			return nil, err
		}
		for _, row := range l4Rules {
			if containsInt(parseIntArray(row.RelayChainJSON), listenerID) {
				return &agentRelayRuleReference{AgentID: agentID, RuleID: row.ID, RuleType: "L4"}, nil
			}
		}
	}
	return nil, nil
}

func (s *agentService) allKnownAgentIDs(ctx context.Context) ([]string, error) {
	seen := map[string]struct{}{}
	agentIDs := make([]string, 0)
	if s.cfg.EnableLocalAgent && strings.TrimSpace(s.cfg.LocalAgentID) != "" {
		seen[s.cfg.LocalAgentID] = struct{}{}
		agentIDs = append(agentIDs, s.cfg.LocalAgentID)
	}
	rows, err := s.store.ListAgents(ctx)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		if strings.TrimSpace(row.ID) == "" {
			continue
		}
		if _, ok := seen[row.ID]; ok {
			continue
		}
		seen[row.ID] = struct{}{}
		agentIDs = append(agentIDs, row.ID)
	}
	return agentIDs, nil
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
	}{Strategy: "round_robin"}
	if err := json.Unmarshal([]byte(defaultString(raw, "{}")), &value); err != nil {
		return HTTPLoadBalancing{Strategy: "round_robin"}
	}
	if value.Strategy != "random" {
		value.Strategy = "round_robin"
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

func defaultCapabilities(values []string) []string {
	if len(values) == 0 {
		return []string{"http_rules"}
	}
	return values
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
		"http_rules":   {},
		"local_acme":   {},
		"cert_install": {},
		"l4":           {},
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

func trimTrailingSlash(value string) string {
	return strings.TrimRight(strings.TrimSpace(value), "/")
}

func validateAgentURL(value string) bool {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil {
		return false
	}
	return parsed.Scheme == "http" || parsed.Scheme == "https"
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
