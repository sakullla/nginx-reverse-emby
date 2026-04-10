package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

var ErrAgentNotFound = errors.New("agent not found")
var ErrAgentUnauthorized = errors.New("agent unauthorized")

var defaultLocalCapabilities = []string{"http_rules", "local_acme", "cert_install", "l4"}

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
	Name             string   `json:"name"`
	AgentID          string   `json:"agent_id"`
	CurrentRevision  int64    `json:"current_revision"`
	Version          string   `json:"version"`
	Platform         string   `json:"platform"`
	AgentURL         string   `json:"agent_url"`
	Tags             []string `json:"tags"`
	Capabilities     []string `json:"capabilities"`
	LastApplyStatus  string   `json:"last_apply_status"`
	LastApplyMessage string   `json:"last_apply_message"`
}

type HeartbeatReply struct {
	DesiredVersion      string     `json:"desired_version,omitempty"`
	DesiredRevision     int64      `json:"desired_revision"`
	CurrentRevision     int64      `json:"current_revision"`
	Rules               []HTTPRule `json:"rules"`
	L4Rules             []any      `json:"l4_rules"`
	RelayListeners      []any      `json:"relay_listeners"`
	Certificates        []any      `json:"certificates"`
	CertificatePolicies []any      `json:"certificate_policies"`
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

type agentService struct {
	cfg   config.Config
	store storage.Store
	now   func() time.Time
}

func NewAgentService(cfg config.Config, store storage.Store) *agentService {
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
		localState, err := s.store.LoadLocalAgentState(ctx)
		if err != nil {
			return nil, err
		}
		localRules, err := s.store.ListHTTPRules(ctx, s.cfg.LocalAgentID)
		if err != nil {
			return nil, err
		}
		localL4Rules, err := s.store.ListL4Rules(ctx, s.cfg.LocalAgentID)
		if err != nil {
			return nil, err
		}
		agents = append(agents, AgentSummary{
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
		})
	}

	for _, row := range rows {
		if row.IsLocal || (s.cfg.EnableLocalAgent && row.ID == s.cfg.LocalAgentID) {
			continue
		}

		rules, err := s.store.ListHTTPRules(ctx, row.ID)
		if err != nil {
			return nil, err
		}
		l4Rules, err := s.store.ListL4Rules(ctx, row.ID)
		if err != nil {
			return nil, err
		}

		agents = append(agents, AgentSummary{
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
		})
	}

	return agents, nil
}

func (s *agentService) Register(ctx context.Context, request RegisterRequest, headerAgentToken string) (AgentSummary, error) {
	name := strings.TrimSpace(request.Name)
	if name == "" {
		return AgentSummary{}, errors.New("name is required")
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
		TagsJSON:         marshalStringArray(request.Tags),
		CapabilitiesJSON: marshalStringArray(defaultCapabilities(request.Capabilities)),
		Mode:             defaultString(request.Mode, "pull"),
		LastApplyStatus:  "success",
	}
	for _, existing := range rows {
		if existing.AgentToken == agentToken || existing.Name == name {
			row = existing
			break
		}
	}

	row.Name = name
	row.AgentURL = strings.TrimSpace(request.AgentURL)
	row.AgentToken = agentToken
	row.Version = strings.TrimSpace(request.Version)
	row.Platform = strings.TrimSpace(request.Platform)
	row.TagsJSON = marshalStringArray(request.Tags)
	row.CapabilitiesJSON = marshalStringArray(defaultCapabilities(request.Capabilities))
	row.Mode = defaultString(request.Mode, "pull")
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
		row.AgentURL = strings.TrimSpace(request.AgentURL)
	}
	if len(request.Tags) > 0 {
		row.TagsJSON = marshalStringArray(request.Tags)
	}
	if len(request.Capabilities) > 0 {
		row.CapabilitiesJSON = marshalStringArray(defaultCapabilities(request.Capabilities))
	}
	row.CurrentRevision = int(request.CurrentRevision)
	row.LastApplyStatus = defaultString(request.LastApplyStatus, row.LastApplyStatus)
	row.LastApplyMessage = request.LastApplyMessage
	row.LastSeenAt = s.now().UTC().Format(time.RFC3339)

	if err := s.store.SaveAgent(ctx, row); err != nil {
		return HeartbeatReply{}, err
	}

	rules, err := s.ListHTTPRules(ctx, row.ID)
	if err != nil {
		return HeartbeatReply{}, err
	}

	hasUpdate := request.CurrentRevision < int64(row.DesiredRevision)
	if !hasUpdate {
		rules = []HTTPRule{}
	}

	return HeartbeatReply{
		DesiredVersion:      row.DesiredVersion,
		DesiredRevision:     int64(row.DesiredRevision),
		CurrentRevision:     request.CurrentRevision,
		Rules:               rules,
		L4Rules:             []any{},
		RelayListeners:      []any{},
		Certificates:        []any{},
		CertificatePolicies: []any{},
	}, nil
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

func randomAgentID() string {
	var buffer [16]byte
	if _, err := rand.Read(buffer[:]); err != nil {
		return "agent-" + time.Now().UTC().Format("20060102150405")
	}
	return hex.EncodeToString(buffer[:])
}
