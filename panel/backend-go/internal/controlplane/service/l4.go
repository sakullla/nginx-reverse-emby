package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

var ErrInvalidArgument = errors.New("invalid argument")
var ErrRuleNotFound = errors.New("rule not found")
var ErrL4Unsupported = errors.New("agent does not support L4 rules")

type L4Backend struct {
	Host string `json:"host"`
	Port int    `json:"port"`
}

type L4LoadBalancing struct {
	Strategy string `json:"strategy"`
}

type L4ProxyProtocolTuning struct {
	Decode bool `json:"decode"`
	Send   bool `json:"send"`
}

type L4Tuning struct {
	ProxyProtocol L4ProxyProtocolTuning `json:"proxy_protocol"`
}

type L4Rule struct {
	ID            int             `json:"id"`
	AgentID       string          `json:"agent_id"`
	Name          string          `json:"name"`
	Protocol      string          `json:"protocol"`
	ListenHost    string          `json:"listen_host"`
	ListenPort    int             `json:"listen_port"`
	UpstreamHost  string          `json:"upstream_host"`
	UpstreamPort  int             `json:"upstream_port"`
	Backends      []L4Backend     `json:"backends"`
	LoadBalancing L4LoadBalancing `json:"load_balancing"`
	Tuning        L4Tuning        `json:"tuning"`
	RelayChain    []int           `json:"relay_chain"`
	RelayLayers   [][]int         `json:"relay_layers"`
	RelayObfs     bool            `json:"relay_obfs"`
	Enabled       bool            `json:"enabled"`
	Tags          []string        `json:"tags"`
	Revision      int             `json:"revision"`
}

type L4RuleInput struct {
	ID            *int             `json:"id,omitempty"`
	Name          *string          `json:"name,omitempty"`
	Protocol      *string          `json:"protocol,omitempty"`
	ListenHost    *string          `json:"listen_host,omitempty"`
	ListenPort    *int             `json:"listen_port,omitempty"`
	UpstreamHost  *string          `json:"upstream_host,omitempty"`
	UpstreamPort  *int             `json:"upstream_port,omitempty"`
	Backends      *[]L4Backend     `json:"backends,omitempty"`
	LoadBalancing *L4LoadBalancing `json:"load_balancing,omitempty"`
	Tuning        *L4Tuning        `json:"tuning,omitempty"`
	RelayChain    *[]int           `json:"relay_chain,omitempty"`
	RelayLayers   *[][]int         `json:"relay_layers,omitempty"`
	RelayObfs     *bool            `json:"relay_obfs,omitempty"`
	Enabled       *bool            `json:"enabled,omitempty"`
	Tags          *[]string        `json:"tags,omitempty"`
}

type l4Service struct {
	cfg               config.Config
	store             storage.Store
	localApplyTrigger func(context.Context) error
}

func NewL4RuleService(cfg config.Config, store storage.Store) *l4Service {
	return &l4Service{cfg: cfg, store: store}
}

func (s *l4Service) SetLocalApplyTrigger(trigger func(context.Context) error) {
	s.localApplyTrigger = wrapLocalApplyTrigger(trigger)
}

func (s *l4Service) triggerLocalApply(ctx context.Context, agentID string) error {
	if !s.cfg.EnableLocalAgent || agentID != s.cfg.LocalAgentID || s.localApplyTrigger == nil {
		return nil
	}
	return s.localApplyTrigger(ctx)
}

func (s *l4Service) List(ctx context.Context, agentID string) ([]L4Rule, error) {
	resolvedID, err := s.ensureAgentSupportsL4(ctx, agentID)
	if err != nil {
		return nil, err
	}

	rows, err := s.store.ListL4Rules(ctx, resolvedID)
	if err != nil {
		return nil, err
	}

	rules := make([]L4Rule, 0, len(rows))
	for _, row := range rows {
		rules = append(rules, l4RuleFromRow(row))
	}
	return rules, nil
}

func (s *l4Service) Get(ctx context.Context, agentID string, id int) (L4Rule, error) {
	resolvedID, err := s.ensureAgentSupportsL4(ctx, agentID)
	if err != nil {
		return L4Rule{}, err
	}

	row, ok, err := s.store.GetL4Rule(ctx, resolvedID, id)
	if err != nil {
		return L4Rule{}, err
	}
	if !ok {
		return L4Rule{}, ErrRuleNotFound
	}
	return l4RuleFromRow(row), nil
}

func (s *l4Service) Create(ctx context.Context, agentID string, input L4RuleInput) (L4Rule, error) {
	resolvedID, err := s.ensureAgentSupportsL4(ctx, agentID)
	if err != nil {
		return L4Rule{}, err
	}

	rows, err := s.store.ListL4Rules(ctx, resolvedID)
	if err != nil {
		return L4Rule{}, err
	}
	allocator, err := newConfigIdentityAllocatorFromStore(ctx, s.cfg, s.store)
	if err != nil {
		return L4Rule{}, err
	}

	existing := make([]L4Rule, 0, len(rows))
	maxRevision := 0
	for _, row := range rows {
		rule := l4RuleFromRow(row)
		existing = append(existing, rule)
		if rule.Revision > maxRevision {
			maxRevision = rule.Revision
		}
	}

	allocatedID := allocator.AllocateRuleID(preferredInt(input.ID))
	normalizedInput := input
	// Keep the caller's preferred ID only for allocator conflict resolution.
	// Normalization should see the assigned ID, not re-read the raw preference.
	normalizedInput.ID = nil
	rule, err := normalizeL4RuleInput(normalizedInput, L4Rule{}, allocatedID)
	if err != nil {
		return L4Rule{}, err
	}
	rule.AgentID = resolvedID
	rule.Revision = allocator.AllocateRevisionForAgent(resolvedID, maxRevision)
	if err := s.validateRelayChain(ctx, rule.RelayChain); err != nil {
		return L4Rule{}, err
	}

	if err := ensureUniqueL4Listen(existing, rule, 0); err != nil {
		return L4Rule{}, err
	}

	rows = append(rows, l4RuleToRow(rule))
	if err := s.store.SaveL4Rules(ctx, resolvedID, rows); err != nil {
		return L4Rule{}, err
	}
	if err := s.bumpRemoteDesiredRevision(ctx, resolvedID, rule.Revision); err != nil {
		return L4Rule{}, err
	}
	if err := s.triggerLocalApply(ctx, resolvedID); err != nil {
		return L4Rule{}, err
	}
	return rule, nil
}

func (s *l4Service) Update(ctx context.Context, agentID string, id int, input L4RuleInput) (L4Rule, error) {
	resolvedID, err := s.ensureAgentSupportsL4(ctx, agentID)
	if err != nil {
		return L4Rule{}, err
	}

	rows, err := s.store.ListL4Rules(ctx, resolvedID)
	if err != nil {
		return L4Rule{}, err
	}
	allocator, err := newConfigIdentityAllocatorFromStore(ctx, s.cfg, s.store)
	if err != nil {
		return L4Rule{}, err
	}

	existing := make([]L4Rule, 0, len(rows))
	maxRevision := 0
	targetIndex := -1
	var current L4Rule
	for i, row := range rows {
		rule := l4RuleFromRow(row)
		existing = append(existing, rule)
		if rule.Revision > maxRevision {
			maxRevision = rule.Revision
		}
		if rule.ID == id {
			targetIndex = i
			current = rule
		}
	}
	if targetIndex < 0 {
		return L4Rule{}, ErrRuleNotFound
	}

	rule, err := normalizeL4RuleInput(input, current, id)
	if err != nil {
		return L4Rule{}, err
	}
	rule.AgentID = resolvedID
	rule.Revision = allocator.AllocateRevisionForAgent(resolvedID, maxRevision)
	if err := s.validateRelayChain(ctx, rule.RelayChain); err != nil {
		return L4Rule{}, err
	}

	if err := ensureUniqueL4Listen(existing, rule, id); err != nil {
		return L4Rule{}, err
	}

	rows[targetIndex] = l4RuleToRow(rule)
	if err := s.store.SaveL4Rules(ctx, resolvedID, rows); err != nil {
		return L4Rule{}, err
	}
	if err := s.bumpRemoteDesiredRevision(ctx, resolvedID, rule.Revision); err != nil {
		return L4Rule{}, err
	}
	if err := s.triggerLocalApply(ctx, resolvedID); err != nil {
		return L4Rule{}, err
	}
	return rule, nil
}

func (s *l4Service) Delete(ctx context.Context, agentID string, id int) (L4Rule, error) {
	resolvedID, err := s.ensureAgentSupportsL4(ctx, agentID)
	if err != nil {
		return L4Rule{}, err
	}

	rows, err := s.store.ListL4Rules(ctx, resolvedID)
	if err != nil {
		return L4Rule{}, err
	}

	targetIndex := -1
	var deleted L4Rule
	for i, row := range rows {
		rule := l4RuleFromRow(row)
		if rule.ID == id {
			targetIndex = i
			deleted = rule
			break
		}
	}
	if targetIndex < 0 {
		return L4Rule{}, ErrRuleNotFound
	}

	nextRows := append([]storage.L4RuleRow(nil), rows[:targetIndex]...)
	nextRows = append(nextRows, rows[targetIndex+1:]...)
	if err := s.store.SaveL4Rules(ctx, resolvedID, nextRows); err != nil {
		return L4Rule{}, err
	}
	allocator, err := newConfigIdentityAllocatorFromStore(ctx, s.cfg, s.store)
	if err != nil {
		return L4Rule{}, err
	}
	nextRevision := allocator.AllocateRevisionForAgent(resolvedID, deleted.Revision)
	if err := s.bumpRemoteDesiredRevision(ctx, resolvedID, nextRevision); err != nil {
		return L4Rule{}, err
	}
	if err := s.triggerLocalApply(ctx, resolvedID); err != nil {
		return L4Rule{}, err
	}
	return deleted, nil
}

func (s *l4Service) bumpRemoteDesiredRevision(ctx context.Context, agentID string, revision int) error {
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
		nextRevision := revision
		if row.DesiredRevision > nextRevision {
			nextRevision = row.DesiredRevision
		}
		if row.CurrentRevision > nextRevision {
			nextRevision = row.CurrentRevision
		}
		if row.DesiredRevision < nextRevision {
			row.DesiredRevision = nextRevision
		}
		return s.store.SaveAgent(ctx, row)
	}
	return ErrAgentNotFound
}

func (s *l4Service) ensureAgentSupportsL4(ctx context.Context, agentID string) (string, error) {
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
		if row.ID != resolvedID {
			continue
		}
		capabilities := parseStringArray(row.CapabilitiesJSON)
		for _, capability := range capabilities {
			if strings.EqualFold(capability, "l4") {
				return resolvedID, nil
			}
		}
		return "", ErrL4Unsupported
	}
	return "", ErrAgentNotFound
}

func normalizeL4RuleInput(input L4RuleInput, fallback L4Rule, suggestedID int) (L4Rule, error) {
	protocol := strings.ToLower(defaultString(pointerString(input.Protocol), fallback.Protocol))
	if protocol == "" {
		protocol = "tcp"
	}
	if protocol != "tcp" && protocol != "udp" {
		return L4Rule{}, fmt.Errorf("%w: protocol must be tcp or udp", ErrInvalidArgument)
	}

	listenHost := defaultString(pointerString(input.ListenHost), fallback.ListenHost)
	if listenHost == "" {
		listenHost = "0.0.0.0"
	}

	listenPort := fallback.ListenPort
	if input.ListenPort != nil {
		listenPort = *input.ListenPort
	}
	if listenPort < 1 || listenPort > 65535 {
		return L4Rule{}, fmt.Errorf("%w: listen_port must be a valid port", ErrInvalidArgument)
	}

	id := fallback.ID
	if input.ID != nil && *input.ID > 0 {
		id = *input.ID
	}
	if id <= 0 {
		id = suggestedID
	}

	backends, upstreamHost, upstreamPort, err := normalizeL4BackendsInput(input, fallback)
	if err != nil {
		return L4Rule{}, err
	}

	loadBalancing := normalizeL4LoadBalancingInput(input.LoadBalancing, fallback.LoadBalancing)
	tuning := normalizeL4TuningInput(protocol, input.Tuning, fallback.Tuning)

	relayChain := append([]int(nil), fallback.RelayChain...)
	if input.RelayChain != nil {
		relayChain, err = normalizeRelayChainInput(*input.RelayChain, protocol)
		if err != nil {
			return L4Rule{}, err
		}
	}
	relayLayers := cloneIntLayers(fallback.RelayLayers)
	if input.RelayLayers != nil {
		relayLayers, err = normalizeRelayLayersInput(*input.RelayLayers, protocol)
		if err != nil {
			return L4Rule{}, err
		}
	}

	relayObfs := false
	if fallback.ID > 0 {
		relayObfs = fallback.RelayObfs
	}
	if input.RelayObfs != nil {
		relayObfs = *input.RelayObfs
	}
	if relayObfs && protocol != "tcp" {
		relayObfs = false
	}
	if relayObfs && len(relayChain) == 0 {
		relayObfs = false
	}

	tags := append([]string(nil), fallback.Tags...)
	if input.Tags != nil {
		tags = normalizeTags(*input.Tags)
	}

	enabled := true
	if fallback.ID > 0 {
		enabled = fallback.Enabled
	}
	if input.Enabled != nil {
		enabled = *input.Enabled
	}

	name := strings.TrimSpace(pointerString(input.Name))
	if name == "" {
		name = strings.TrimSpace(fallback.Name)
	}
	if name == "" {
		name = fmt.Sprintf("%s %d", strings.ToUpper(protocol), listenPort)
	}

	return L4Rule{
		ID:            id,
		AgentID:       fallback.AgentID,
		Name:          name,
		Protocol:      protocol,
		ListenHost:    listenHost,
		ListenPort:    listenPort,
		UpstreamHost:  upstreamHost,
		UpstreamPort:  upstreamPort,
		Backends:      backends,
		LoadBalancing: loadBalancing,
		Tuning:        tuning,
		RelayChain:    relayChain,
		RelayLayers:   relayLayers,
		RelayObfs:     relayObfs,
		Enabled:       enabled,
		Tags:          tags,
		Revision:      fallback.Revision,
	}, nil
}

func (s *l4Service) validateRelayChain(ctx context.Context, relayChain []int) error {
	knownAgentIDs, err := s.allKnownAgentIDs(ctx)
	if err != nil {
		return err
	}
	return validateRelayChainReferences(ctx, s.store, knownAgentIDs, relayChain)
}

func (s *l4Service) allKnownAgentIDs(ctx context.Context) ([]string, error) {
	return allKnownAgentIDs(ctx, s.cfg, s.store)
}

func normalizeL4BackendsInput(input L4RuleInput, fallback L4Rule) ([]L4Backend, string, int, error) {
	if input.Backends != nil {
		backends := normalizeL4BackendList(*input.Backends)
		if len(backends) == 0 {
			return nil, "", 0, fmt.Errorf("%w: at least one valid backend is required", ErrInvalidArgument)
		}
		return backends, backends[0].Host, backends[0].Port, nil
	}

	upstreamHost := defaultString(pointerString(input.UpstreamHost), fallback.UpstreamHost)
	upstreamPort := fallback.UpstreamPort
	if input.UpstreamPort != nil {
		upstreamPort = *input.UpstreamPort
	}
	if upstreamHost != "" && upstreamPort >= 1 && upstreamPort <= 65535 {
		backends := []L4Backend{{Host: upstreamHost, Port: upstreamPort}}
		return backends, upstreamHost, upstreamPort, nil
	}

	if len(fallback.Backends) > 0 {
		backends := normalizeL4BackendList(fallback.Backends)
		if len(backends) == 0 {
			return nil, "", 0, fmt.Errorf("%w: at least one valid backend is required", ErrInvalidArgument)
		}
		return backends, backends[0].Host, backends[0].Port, nil
	}

	return nil, "", 0, fmt.Errorf("%w: at least one valid backend is required", ErrInvalidArgument)
}

func normalizeL4BackendList(backends []L4Backend) []L4Backend {
	normalized := make([]L4Backend, 0, len(backends))
	for _, backend := range backends {
		host := strings.TrimSpace(backend.Host)
		if host == "" || backend.Port < 1 || backend.Port > 65535 {
			continue
		}
		normalized = append(normalized, L4Backend{Host: host, Port: backend.Port})
	}
	return normalized
}

func normalizeL4LoadBalancingInput(input *L4LoadBalancing, fallback L4LoadBalancing) L4LoadBalancing {
	strategy := fallback.Strategy
	if input != nil {
		strategy = input.Strategy
	}
	strategy = strings.ToLower(strings.TrimSpace(strategy))
	switch strategy {
	case "round_robin", "random", "adaptive":
		// keep explicit strategies
	default:
		strategy = "adaptive"
	}
	return L4LoadBalancing{Strategy: strategy}
}

func normalizeL4TuningInput(protocol string, input *L4Tuning, fallback L4Tuning) L4Tuning {
	tuning := fallback
	if input != nil {
		tuning = *input
	}
	if protocol == "udp" {
		tuning.ProxyProtocol = L4ProxyProtocolTuning{}
	}
	return tuning
}

func normalizeTags(values []string) []string {
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed != "" {
			normalized = append(normalized, trimmed)
		}
	}
	return normalized
}

func ensureUniqueL4Listen(rules []L4Rule, next L4Rule, excludeID int) error {
	for _, rule := range rules {
		if rule.ID == excludeID {
			continue
		}
		if rule.Protocol == next.Protocol && rule.ListenHost == next.ListenHost && rule.ListenPort == next.ListenPort {
			return fmt.Errorf(
				"%w: listen %s:%s:%d conflicts with rule #%d",
				ErrInvalidArgument,
				next.Protocol,
				next.ListenHost,
				next.ListenPort,
				rule.ID,
			)
		}
	}
	return nil
}

func l4RuleFromRow(row storage.L4RuleRow) L4Rule {
	rule := L4Rule{
		ID:            row.ID,
		AgentID:       row.AgentID,
		Name:          row.Name,
		Protocol:      defaultString(row.Protocol, "tcp"),
		ListenHost:    defaultString(row.ListenHost, "0.0.0.0"),
		ListenPort:    row.ListenPort,
		UpstreamHost:  row.UpstreamHost,
		UpstreamPort:  row.UpstreamPort,
		LoadBalancing: L4LoadBalancing{Strategy: "adaptive"},
		Tuning:        L4Tuning{ProxyProtocol: L4ProxyProtocolTuning{}},
		RelayChain:    []int{},
		RelayLayers:   [][]int{},
		RelayObfs:     row.RelayObfs,
		Enabled:       row.Enabled,
		Tags:          parseStringArray(row.TagsJSON),
		Revision:      row.Revision,
	}

	if backends := parseL4Backends(row.BackendsJSON); len(backends) > 0 {
		rule.Backends = backends
		rule.UpstreamHost = backends[0].Host
		rule.UpstreamPort = backends[0].Port
	} else if rule.UpstreamHost != "" && rule.UpstreamPort > 0 {
		rule.Backends = []L4Backend{{Host: rule.UpstreamHost, Port: rule.UpstreamPort}}
	}

	if lb := parseL4LoadBalancing(row.LoadBalancingJSON); lb.Strategy != "" {
		rule.LoadBalancing = lb
	}
	if tuning := parseL4Tuning(row.TuningJSON); tuning != (L4Tuning{}) {
		rule.Tuning = tuning
	}
	rule.RelayChain = parseIntArray(row.RelayChainJSON)
	rule.RelayLayers = parseIntLayers(row.RelayLayersJSON)
	return rule
}

func l4RuleToRow(rule L4Rule) storage.L4RuleRow {
	return storage.L4RuleRow{
		ID:                rule.ID,
		AgentID:           rule.AgentID,
		Name:              rule.Name,
		Protocol:          rule.Protocol,
		ListenHost:        rule.ListenHost,
		ListenPort:        rule.ListenPort,
		UpstreamHost:      rule.UpstreamHost,
		UpstreamPort:      rule.UpstreamPort,
		BackendsJSON:      marshalJSON(rule.Backends, "[]"),
		LoadBalancingJSON: marshalJSON(rule.LoadBalancing, `{"strategy":"adaptive"}`),
		TuningJSON:        marshalJSON(rule.Tuning, `{"proxy_protocol":{"decode":false,"send":false}}`),
		RelayChainJSON:    marshalJSON(rule.RelayChain, "[]"),
		RelayLayersJSON:   marshalJSON(rule.RelayLayers, "[]"),
		RelayObfs:         rule.RelayObfs,
		Enabled:           rule.Enabled,
		TagsJSON:          marshalJSON(rule.Tags, "[]"),
		Revision:          rule.Revision,
	}
}

func parseL4Backends(raw string) []L4Backend {
	var backends []L4Backend
	if err := json.Unmarshal([]byte(defaultString(raw, "[]")), &backends); err != nil {
		return []L4Backend{}
	}
	return normalizeL4BackendList(backends)
}

func parseL4LoadBalancing(raw string) L4LoadBalancing {
	var lb L4LoadBalancing
	if err := json.Unmarshal([]byte(defaultString(raw, "{}")), &lb); err != nil {
		return L4LoadBalancing{Strategy: "adaptive"}
	}
	return normalizeL4LoadBalancingInput(&lb, L4LoadBalancing{Strategy: "adaptive"})
}

func parseL4Tuning(raw string) L4Tuning {
	var tuning L4Tuning
	if err := json.Unmarshal([]byte(defaultString(raw, "{}")), &tuning); err != nil {
		return L4Tuning{ProxyProtocol: L4ProxyProtocolTuning{}}
	}
	return tuning
}

func marshalJSON(value any, fallback string) string {
	data, err := json.Marshal(value)
	if err != nil {
		return fallback
	}
	return string(data)
}

func pointerString(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}
