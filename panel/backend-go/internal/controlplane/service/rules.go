package service

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

type HTTPRuleInput struct {
	ID               *int                `json:"id,omitempty"`
	FrontendURL      *string             `json:"frontend_url,omitempty"`
	BackendURL       *string             `json:"backend_url,omitempty"`
	Backends         *[]HTTPRuleBackend  `json:"backends,omitempty"`
	LoadBalancing    *HTTPLoadBalancing  `json:"load_balancing,omitempty"`
	Enabled          *bool               `json:"enabled,omitempty"`
	Tags             *[]string           `json:"tags,omitempty"`
	ProxyRedirect    *bool               `json:"proxy_redirect,omitempty"`
	RelayChain       *[]int              `json:"relay_chain,omitempty"`
	PassProxyHeaders *bool               `json:"pass_proxy_headers,omitempty"`
	UserAgent        *string             `json:"user_agent,omitempty"`
	CustomHeaders    *[]HTTPCustomHeader `json:"custom_headers,omitempty"`
}

type ruleStore interface {
	ListAgents(context.Context) ([]storage.AgentRow, error)
	ListHTTPRules(context.Context, string) ([]storage.HTTPRuleRow, error)
	ListRelayListeners(context.Context, string) ([]storage.RelayListenerRow, error)
	SaveAgent(context.Context, storage.AgentRow) error
	SaveHTTPRules(context.Context, string, []storage.HTTPRuleRow) error
}

type ruleService struct {
	cfg   config.Config
	store ruleStore
}

func NewRuleService(cfg config.Config, store ruleStore) *ruleService {
	return &ruleService{cfg: cfg, store: store}
}

func (s *ruleService) List(ctx context.Context, agentID string) ([]HTTPRule, error) {
	resolvedID, err := s.ensureAgentExists(ctx, agentID)
	if err != nil {
		return nil, err
	}

	rows, err := s.store.ListHTTPRules(ctx, resolvedID)
	if err != nil {
		return nil, err
	}

	rules := make([]HTTPRule, 0, len(rows))
	for _, row := range rows {
		rules = append(rules, httpRuleFromRow(row))
	}
	return rules, nil
}

func (s *ruleService) Create(ctx context.Context, agentID string, input HTTPRuleInput) (HTTPRule, error) {
	resolvedID, err := s.ensureAgentExists(ctx, agentID)
	if err != nil {
		return HTTPRule{}, err
	}

	rows, err := s.store.ListHTTPRules(ctx, resolvedID)
	if err != nil {
		return HTTPRule{}, err
	}
	allRows, err := s.listRulesAcrossAllAgents(ctx)
	if err != nil {
		return HTTPRule{}, err
	}

	maxID := 0
	for _, row := range rows {
		if row.ID > maxID {
			maxID = row.ID
		}
	}
	maxRevision := 0
	for _, row := range allRows {
		if row.Revision > maxRevision {
			maxRevision = row.Revision
		}
	}

	rule, err := s.normalizeHTTPRuleInput(ctx, input, HTTPRule{}, maxID+1)
	if err != nil {
		return HTTPRule{}, err
	}
	rule.AgentID = resolvedID
	rule.Revision = maxRevision + 1

	rows = append(rows, httpRuleToRow(rule))
	if err := s.store.SaveHTTPRules(ctx, resolvedID, rows); err != nil {
		return HTTPRule{}, err
	}
	if err := s.bumpRemoteDesiredRevision(ctx, resolvedID, rule.Revision); err != nil {
		return HTTPRule{}, err
	}
	return rule, nil
}

func (s *ruleService) Update(ctx context.Context, agentID string, id int, input HTTPRuleInput) (HTTPRule, error) {
	resolvedID, err := s.ensureAgentExists(ctx, agentID)
	if err != nil {
		return HTTPRule{}, err
	}

	rows, err := s.store.ListHTTPRules(ctx, resolvedID)
	if err != nil {
		return HTTPRule{}, err
	}
	allRows, err := s.listRulesAcrossAllAgents(ctx)
	if err != nil {
		return HTTPRule{}, err
	}

	maxRevision := 0
	targetIndex := -1
	var current HTTPRule
	for i, row := range rows {
		if row.Revision > maxRevision {
			maxRevision = row.Revision
		}
		rule := httpRuleFromRow(row)
		if rule.ID == id {
			targetIndex = i
			current = rule
		}
	}
	for _, row := range allRows {
		if row.Revision > maxRevision {
			maxRevision = row.Revision
		}
	}
	if targetIndex < 0 {
		return HTTPRule{}, ErrRuleNotFound
	}

	rule, err := s.normalizeHTTPRuleInput(ctx, input, current, id)
	if err != nil {
		return HTTPRule{}, err
	}
	rule.AgentID = resolvedID
	rule.Revision = maxRevision + 1

	rows[targetIndex] = httpRuleToRow(rule)
	if err := s.store.SaveHTTPRules(ctx, resolvedID, rows); err != nil {
		return HTTPRule{}, err
	}
	if err := s.bumpRemoteDesiredRevision(ctx, resolvedID, rule.Revision); err != nil {
		return HTTPRule{}, err
	}
	return rule, nil
}

func (s *ruleService) Delete(ctx context.Context, agentID string, id int) (HTTPRule, error) {
	resolvedID, err := s.ensureAgentExists(ctx, agentID)
	if err != nil {
		return HTTPRule{}, err
	}

	rows, err := s.store.ListHTTPRules(ctx, resolvedID)
	if err != nil {
		return HTTPRule{}, err
	}

	targetIndex := -1
	var deleted HTTPRule
	for i, row := range rows {
		rule := httpRuleFromRow(row)
		if rule.ID == id {
			targetIndex = i
			deleted = rule
			break
		}
	}
	if targetIndex < 0 {
		return HTTPRule{}, ErrRuleNotFound
	}

	nextRows := append([]storage.HTTPRuleRow(nil), rows[:targetIndex]...)
	nextRows = append(nextRows, rows[targetIndex+1:]...)
	if err := s.store.SaveHTTPRules(ctx, resolvedID, nextRows); err != nil {
		return HTTPRule{}, err
	}
	if err := s.bumpRemoteDesiredRevision(ctx, resolvedID, deleted.Revision+1); err != nil {
		return HTTPRule{}, err
	}
	return deleted, nil
}

func (s *ruleService) ensureAgentExists(ctx context.Context, agentID string) (string, error) {
	resolvedID := strings.TrimSpace(agentID)
	if resolvedID == "" {
		resolvedID = s.cfg.LocalAgentID
	}
	if s.cfg.EnableLocalAgent && resolvedID == s.cfg.LocalAgentID {
		return resolvedID, nil
	}

	rows, err := s.store.ListAgents(ctx)
	if err != nil {
		return "", err
	}
	for _, row := range rows {
		if row.ID == resolvedID {
			return resolvedID, nil
		}
	}
	return "", ErrAgentNotFound
}

func (s *ruleService) bumpRemoteDesiredRevision(ctx context.Context, agentID string, revision int) error {
	if s.cfg.EnableLocalAgent && agentID == s.cfg.LocalAgentID {
		return nil
	}

	rows, err := s.store.ListAgents(ctx)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if row.ID != agentID {
			continue
		}
		if row.DesiredRevision < revision {
			row.DesiredRevision = revision
		}
		return s.store.SaveAgent(ctx, row)
	}
	return ErrAgentNotFound
}

func (s *ruleService) listRulesAcrossAllAgents(ctx context.Context) ([]storage.HTTPRuleRow, error) {
	agentIDs, err := s.allKnownAgentIDs(ctx)
	if err != nil {
		return nil, err
	}

	rows := make([]storage.HTTPRuleRow, 0)
	for _, agentID := range agentIDs {
		agentRows, err := s.store.ListHTTPRules(ctx, agentID)
		if err != nil {
			return nil, err
		}
		rows = append(rows, agentRows...)
	}
	return rows, nil
}

func (s *ruleService) allKnownAgentIDs(ctx context.Context) ([]string, error) {
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

func (s *ruleService) normalizeHTTPRuleInput(ctx context.Context, input HTTPRuleInput, fallback HTTPRule, suggestedID int) (HTTPRule, error) {
	id := fallback.ID
	if input.ID != nil && *input.ID > 0 {
		id = *input.ID
	}
	if id <= 0 {
		id = suggestedID
	}

	frontendURL := strings.TrimSpace(pointerString(input.FrontendURL))
	if frontendURL == "" {
		frontendURL = strings.TrimSpace(fallback.FrontendURL)
	}
	if !isValidHTTPURL(frontendURL) {
		return HTTPRule{}, fmt.Errorf("%w: frontend_url and backend_url/backends[].url must be valid http/https URLs", ErrInvalidArgument)
	}

	backends, err := normalizeHTTPBackendsInput(input, fallback)
	if err != nil {
		return HTTPRule{}, err
	}
	backendURL := backends[0].URL

	loadBalancing := fallback.LoadBalancing
	if loadBalancing.Strategy == "" {
		loadBalancing = HTTPLoadBalancing{Strategy: "round_robin"}
	}
	if input.LoadBalancing != nil {
		loadBalancing = *input.LoadBalancing
	}
	loadBalancing = normalizeHTTPLoadBalancing(loadBalancing)

	enabled := true
	if fallback.ID > 0 {
		enabled = fallback.Enabled
	}
	if input.Enabled != nil {
		enabled = *input.Enabled
	}

	tags := append([]string(nil), fallback.Tags...)
	if input.Tags != nil {
		tags = normalizeTags(*input.Tags)
	}

	proxyRedirect := true
	if fallback.ID > 0 {
		proxyRedirect = fallback.ProxyRedirect
	}
	if input.ProxyRedirect != nil {
		proxyRedirect = *input.ProxyRedirect
	}

	relayChain := append([]int(nil), fallback.RelayChain...)
	if input.RelayChain != nil {
		relayChain = normalizeRelayChain(*input.RelayChain)
	}
	if err := s.validateRelayChain(ctx, relayChain); err != nil {
		return HTTPRule{}, err
	}

	passProxyHeaders := true
	if fallback.ID > 0 {
		passProxyHeaders = fallback.PassProxyHeaders
	}
	if input.PassProxyHeaders != nil {
		passProxyHeaders = *input.PassProxyHeaders
	}

	userAgent := strings.TrimSpace(fallback.UserAgent)
	if input.UserAgent != nil {
		userAgent = strings.TrimSpace(*input.UserAgent)
	}

	customHeaders := append([]HTTPCustomHeader(nil), fallback.CustomHeaders...)
	if input.CustomHeaders != nil {
		customHeaders = normalizeHTTPCustomHeaders(*input.CustomHeaders)
	}

	return HTTPRule{
		ID:               id,
		AgentID:          fallback.AgentID,
		FrontendURL:      frontendURL,
		BackendURL:       backendURL,
		Backends:         backends,
		LoadBalancing:    loadBalancing,
		Enabled:          enabled,
		Tags:             tags,
		ProxyRedirect:    proxyRedirect,
		RelayChain:       relayChain,
		PassProxyHeaders: passProxyHeaders,
		UserAgent:        userAgent,
		CustomHeaders:    customHeaders,
		Revision:         fallback.Revision,
	}, nil
}

func (s *ruleService) validateRelayChain(ctx context.Context, relayChain []int) error {
	if len(relayChain) == 0 {
		return nil
	}

	listeners, err := s.store.ListRelayListeners(ctx, "")
	if err != nil {
		return err
	}

	listenersByID := make(map[int]storage.RelayListenerRow, len(listeners))
	for _, listener := range listeners {
		listenersByID[listener.ID] = listener
	}

	knownAgentIDs, err := s.allKnownAgentIDs(ctx)
	if err != nil {
		return err
	}
	knownAgents := make(map[string]struct{}, len(knownAgentIDs))
	for _, agentID := range knownAgentIDs {
		knownAgents[agentID] = struct{}{}
	}

	for _, listenerID := range relayChain {
		listener, ok := listenersByID[listenerID]
		if !ok {
			return fmt.Errorf("%w: relay listener not found: %d", ErrInvalidArgument, listenerID)
		}
		if !listener.Enabled {
			return fmt.Errorf("%w: relay listener is disabled: %d", ErrInvalidArgument, listenerID)
		}
		if _, ok := knownAgents[strings.TrimSpace(listener.AgentID)]; !ok {
			return fmt.Errorf("%w: relay listener belongs to unknown agent: %d", ErrInvalidArgument, listenerID)
		}
	}

	return nil
}

func normalizeHTTPBackendsInput(input HTTPRuleInput, fallback HTTPRule) ([]HTTPRuleBackend, error) {
	if input.Backends != nil {
		backends := normalizeHTTPBackends(*input.Backends)
		if len(backends) > 0 {
			return backends, nil
		}
		if input.BackendURL != nil {
			backends = normalizeHTTPBackends([]HTTPRuleBackend{{URL: strings.TrimSpace(*input.BackendURL)}})
			if len(backends) > 0 {
				return backends, nil
			}
		}
		backends = normalizeHTTPBackends(fallback.Backends)
		if len(backends) > 0 {
			return backends, nil
		}
	}

	if input.BackendURL != nil {
		backends := normalizeHTTPBackends([]HTTPRuleBackend{{URL: strings.TrimSpace(*input.BackendURL)}})
		if len(backends) > 0 {
			return backends, nil
		}
	}

	backends := normalizeHTTPBackends(fallback.Backends)
	if len(backends) > 0 {
		return backends, nil
	}
	if strings.TrimSpace(fallback.BackendURL) != "" {
		backends = normalizeHTTPBackends([]HTTPRuleBackend{{URL: strings.TrimSpace(fallback.BackendURL)}})
		if len(backends) > 0 {
			return backends, nil
		}
	}
	return nil, fmt.Errorf("%w: frontend_url and backend_url/backends[].url must be valid http/https URLs", ErrInvalidArgument)
}

func normalizeHTTPBackends(backends []HTTPRuleBackend) []HTTPRuleBackend {
	normalized := make([]HTTPRuleBackend, 0, len(backends))
	for _, backend := range backends {
		urlValue := strings.TrimSpace(backend.URL)
		if !isValidHTTPURL(urlValue) {
			continue
		}
		normalized = append(normalized, HTTPRuleBackend{URL: urlValue})
	}
	return normalized
}

func normalizeHTTPCustomHeaders(values []HTTPCustomHeader) []HTTPCustomHeader {
	normalized := make([]HTTPCustomHeader, 0, len(values))
	for _, value := range values {
		name := strings.TrimSpace(value.Name)
		if name == "" {
			continue
		}
		normalized = append(normalized, HTTPCustomHeader{
			Name:  name,
			Value: value.Value,
		})
	}
	return normalized
}

func normalizeHTTPLoadBalancing(value HTTPLoadBalancing) HTTPLoadBalancing {
	if strings.EqualFold(strings.TrimSpace(value.Strategy), "random") {
		return HTTPLoadBalancing{Strategy: "random"}
	}
	return HTTPLoadBalancing{Strategy: "round_robin"}
}

func isValidHTTPURL(raw string) bool {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return false
	}
	if parsed == nil || parsed.Host == "" {
		return false
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https":
		return true
	default:
		return false
	}
}

func httpRuleFromRow(row storage.HTTPRuleRow) HTTPRule {
	backends := parseBackends(row.BackendsJSON)
	if len(backends) == 0 && strings.TrimSpace(row.BackendURL) != "" {
		backends = []HTTPRuleBackend{{URL: strings.TrimSpace(row.BackendURL)}}
	}
	backendURL := strings.TrimSpace(row.BackendURL)
	if backendURL == "" && len(backends) > 0 {
		backendURL = backends[0].URL
	}

	return HTTPRule{
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
	}
}

func httpRuleToRow(rule HTTPRule) storage.HTTPRuleRow {
	return storage.HTTPRuleRow{
		ID:                rule.ID,
		AgentID:           rule.AgentID,
		FrontendURL:       rule.FrontendURL,
		BackendURL:        rule.BackendURL,
		BackendsJSON:      marshalJSON(rule.Backends, "[]"),
		LoadBalancingJSON: marshalJSON(rule.LoadBalancing, `{"strategy":"round_robin"}`),
		Enabled:           rule.Enabled,
		TagsJSON:          marshalJSON(rule.Tags, "[]"),
		ProxyRedirect:     rule.ProxyRedirect,
		RelayChainJSON:    marshalJSON(rule.RelayChain, "[]"),
		PassProxyHeaders:  rule.PassProxyHeaders,
		UserAgent:         rule.UserAgent,
		CustomHeadersJSON: marshalJSON(rule.CustomHeaders, "[]"),
		Revision:          rule.Revision,
	}
}
