package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/revision"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

var ErrInvalidArgument = errors.New("invalid argument")
var ErrRuleNotFound = errors.New("rule not found")
var ErrL4Unsupported = errors.New("agent does not support L4 rules")

const redactedProxyPassword = "xxxxx"

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

type L4ProxyEntryAuth struct {
	Enabled  bool   `json:"enabled"`
	Username string `json:"username"`
	Password string `json:"password,omitempty"`
}

type L4Tuning struct {
	ProxyProtocol L4ProxyProtocolTuning `json:"proxy_protocol"`
}

type L4Rule struct {
	ID            int             `json:"id"`
	AgentID       string          `json:"agent_id"`
	AgentName     string          `json:"agent_name,omitempty"`
	Name          string          `json:"name"`
	Protocol      string          `json:"protocol"`
	ListenHost    string          `json:"listen_host"`
	ListenPort    int             `json:"listen_port"`
	UpstreamHost  string          `json:"-"`
	UpstreamPort  int             `json:"-"`
	Backends      []L4Backend     `json:"backends"`
	LoadBalancing L4LoadBalancing `json:"load_balancing"`
	Tuning        L4Tuning        `json:"tuning"`
	RelayChain    []int           `json:"-"`
	RelayLayers   [][]int         `json:"relay_layers"`
	RelayObfs     bool            `json:"relay_obfs"`
	ListenMode    string          `json:"listen_mode"`

	EgressProfileID *int `json:"egress_profile_id,omitempty"`

	ProxyEntryAuth L4ProxyEntryAuth `json:"proxy_entry_auth"`
	Enabled        bool             `json:"enabled"`
	Tags           []string         `json:"tags"`
	Revision       int              `json:"revision"`
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
	ListenMode    *string          `json:"listen_mode,omitempty"`

	EgressProfileID *int `json:"egress_profile_id,omitempty"`

	ProxyEntryAuth *L4ProxyEntryAuth `json:"proxy_entry_auth,omitempty"`
	Enabled        *bool             `json:"enabled,omitempty"`
	Tags           *[]string         `json:"tags,omitempty"`
}

type l4Service struct {
	cfg               config.Config
	store             storage.Store
	localApplyTrigger func(context.Context) error
	mutationExecutor  *revision.Executor
	revisionMutation  bool
	revisionNumbers   map[string]int64
}

func NewL4RuleService(cfg config.Config, store storage.Store) *l4Service {
	return &l4Service{cfg: cfg, store: store, mutationExecutor: newConfigMutationExecutor(cfg, store)}
}

func (s *l4Service) SetLocalApplyTrigger(trigger func(context.Context) error) {
	s.localApplyTrigger = wrapLocalApplyTrigger(trigger)
}

func (s *l4Service) triggerLocalApply(ctx context.Context, agentID string) error {
	if s.revisionMutation {
		return nil
	}
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
		if !l4RuleRowSupported(row) {
			continue
		}
		rules = append(rules, l4RuleFromRow(row))
	}
	return rules, nil
}

func (s *l4Service) ListPage(ctx context.Context, query ListQuery) ([]L4Rule, PageMeta, error) {
	query = NormalizeListQuery(query)
	names, err := agentDisplayNameMap(ctx, s.cfg, s.store)
	if err != nil {
		return nil, PageMeta{}, err
	}

	var rows []storage.L4RuleRow
	if query.AgentID != "" {
		resolvedID, err := s.ensureAgentSupportsL4(ctx, query.AgentID)
		if err != nil {
			return nil, PageMeta{}, err
		}
		rows, err = s.store.ListL4Rules(ctx, resolvedID)
		if err != nil {
			return nil, PageMeta{}, err
		}
	} else {
		agentIDs, err := s.allKnownAgentIDs(ctx)
		if err != nil {
			return nil, PageMeta{}, err
		}
		rows = make([]storage.L4RuleRow, 0)
		for _, agentID := range agentIDs {
			agentRows, listErr := s.store.ListL4Rules(ctx, agentID)
			if listErr != nil {
				return nil, PageMeta{}, listErr
			}
			rows = append(rows, agentRows...)
		}
	}

	syncRevisions := map[string]int{}
	if query.Sync != "" {
		revisions, syncErr := agentLastApplyRevisionMap(ctx, s.cfg, s.store)
		if syncErr != nil {
			return nil, PageMeta{}, syncErr
		}
		syncRevisions = revisions
	}

	filtered := make([]L4Rule, 0, len(rows))
	for _, row := range rows {
		if !l4RuleRowSupported(row) {
			continue
		}
		rule := l4RuleFromRow(row)
		if strings.TrimSpace(rule.AgentID) == "" {
			rule.AgentID = row.AgentID
		}
		rule.AgentName = resolveAgentDisplayName(names, rule.AgentID)
		identity := rule.Name
		if identity == "" {
			identity = rule.ListenHost + ":" + strconv.Itoa(rule.ListenPort)
		}
		searchFields := []string{identity, rule.Name, rule.ListenHost, strconv.Itoa(rule.ListenPort), rule.Protocol, rule.AgentID, rule.AgentName, strings.Join(rule.Tags, " ")}
		for _, backend := range rule.Backends {
			host := strings.TrimSpace(backend.Host)
			port := strconv.Itoa(backend.Port)
			searchFields = append(searchFields, host, port, host+":"+port, net.JoinHostPort(host, port))
		}
		if !matchesListQuery(query.Q, searchFields...) {
			continue
		}
		if !matchesEnabledFilter(query.Enabled, rule.Enabled) {
			continue
		}
		if !matchesTagsFilter(query.Tags, rule.Tags) {
			continue
		}
		if !matchesOptionalIntFilter(query.EgressProfileID, rule.EgressProfileID) {
			continue
		}
		if query.RelayListenerID != nil && !containsInt(flattenRelayLayers(rule.RelayLayers), *query.RelayListenerID) {
			continue
		}
		if query.Sync != "" {
			lastApplyRevision, agentKnown := syncRevisions[rule.AgentID]
			if !matchesSyncFilter(query.Sync, rule.Revision, lastApplyRevision, agentKnown) {
				continue
			}
		}
		filtered = append(filtered, rule)
	}
	page, meta := ApplyPage(filtered, query)
	return page, meta, nil
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
	if !ok || !l4RuleRowSupported(row) {
		return L4Rule{}, ErrRuleNotFound
	}
	return l4RuleFromRow(row), nil
}

func (s *l4Service) Create(ctx context.Context, agentID string, input L4RuleInput) (L4Rule, error) {
	if err := requireConfigMutationStore(s.store, s.mutationExecutor, s.revisionMutation); err != nil {
		return L4Rule{}, err
	}
	if s.mutationExecutor == nil || s.revisionMutation {
		return s.createLegacy(ctx, agentID, input)
	}
	resolvedID, err := s.ensureAgentSupportsL4(ctx, agentID)
	if err != nil {
		return L4Rule{}, err
	}
	targetAgentIDs, err := s.l4MutationAgentIDs(ctx, resolvedID, nil, &input)
	if err != nil {
		return L4Rule{}, err
	}
	var created L4Rule
	_, err = s.mutationExecutor.Execute(ctx, revision.MutationRequest{
		Kind:             "l4_rule.create",
		DependencyAction: revision.DependencyActionApply,
		Request:          input,
		Targets:          configMutationTargets(s.cfg, targetAgentIDs, nil),
		ResourceState: func(ctx context.Context, tx *storage.GormStore, _ revision.Target) (any, error) {
			return l4RuleMutationResourceState(ctx, tx, s.cfg)
		},
		ReplayResourceField: "rule",
		ReplayResource:      func() any { return redactL4RuleForPanelReplay(created) },
		Mutate: func(ctx context.Context, tx *storage.GormStore, revisions map[string]int64) error {
			txService := &l4Service{
				cfg: s.cfg, store: tx, revisionMutation: true, revisionNumbers: revisions,
			}
			var mutateErr error
			created, mutateErr = txService.createLegacy(ctx, resolvedID, input)
			return mutateErr
		},
	})
	return created, err
}

func (s *l4Service) createLegacy(ctx context.Context, agentID string, input L4RuleInput) (L4Rule, error) {
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
		if row.Revision > maxRevision {
			maxRevision = row.Revision
		}
		if !l4RuleRowSupported(row) {
			continue
		}
		existing = append(existing, l4RuleFromRow(row))
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
	if err := s.validateL4EgressProfileReference(ctx, rule); err != nil {
		return L4Rule{}, err
	}
	if err := s.validateRelayChain(ctx, resolvedID, rule.RelayChain); err != nil {
		return L4Rule{}, err
	}
	if err := s.validateRelayChain(ctx, resolvedID, flattenRelayLayers(rule.RelayLayers)); err != nil {
		return L4Rule{}, err
	}

	if err := ensureUniqueL4Listen(existing, rule, 0); err != nil {
		return L4Rule{}, err
	}
	if err := validateL4RuleSet(l4RulesFromRows(rows)); err != nil {
		return L4Rule{}, err
	}
	rule.Revision = configMutationRevision(s.revisionNumbers, resolvedID, allocator.AllocateRevisionForAgent(resolvedID, maxRevision))
	egressExecutorAgentIDs, egressExecutorRevision, err := egressProfileScheduleTargets(ctx, s.store, resolvedID, rule.RelayLayers, rule.EgressProfileID, rule.Revision)
	if err != nil {
		return L4Rule{}, err
	}
	agentRollbackRows, err := snapshotAgentRowsForRollback(ctx, s.store, uniqueAgentIDs(append([]string{resolvedID}, egressExecutorAgentIDs...)))
	if err != nil {
		return L4Rule{}, err
	}

	rollbackL4Rows := append([]storage.L4RuleRow(nil), rows...)
	rows = append(rows, l4RuleToRow(rule))
	if err := consumeResourceQuota(ctx, s.store, "agent", resolvedID, "public_port_count", 1); err != nil {
		return L4Rule{}, err
	}
	if err := s.store.SaveL4Rules(ctx, resolvedID, rows); err != nil {
		return L4Rule{}, err
	}
	if err := s.bumpRemoteDesiredRevision(ctx, resolvedID, rule.Revision); err != nil {
		s.rollbackL4RowsAndAgents(ctx, resolvedID, rollbackL4Rows, agentRollbackRows)
		return L4Rule{}, err
	}
	if err := s.bumpDependentAgentRevisions(ctx, egressExecutorAgentIDs, egressExecutorRevision); err != nil {
		s.rollbackL4RowsAndAgents(ctx, resolvedID, rollbackL4Rows, agentRollbackRows)
		return L4Rule{}, err
	}
	if err := s.triggerLocalApply(ctx, resolvedID); err != nil {
		s.rollbackL4RowsAndAgents(ctx, resolvedID, rollbackL4Rows, agentRollbackRows)
		return L4Rule{}, err
	}
	return rule, nil
}

func (s *l4Service) Update(ctx context.Context, agentID string, id int, input L4RuleInput) (L4Rule, error) {
	if err := requireConfigMutationStore(s.store, s.mutationExecutor, s.revisionMutation); err != nil {
		return L4Rule{}, err
	}
	if s.mutationExecutor == nil || s.revisionMutation {
		return s.updateLegacy(ctx, agentID, id, input)
	}
	resolvedID, err := s.ensureAgentSupportsL4(ctx, agentID)
	if err != nil {
		return L4Rule{}, err
	}
	current, err := s.Get(ctx, resolvedID, id)
	if err != nil {
		return L4Rule{}, err
	}
	targetAgentIDs, err := s.l4MutationAgentIDs(ctx, resolvedID, &current, &input)
	if err != nil {
		return L4Rule{}, err
	}
	var updated L4Rule
	result, err := s.mutationExecutor.Execute(ctx, revision.MutationRequest{
		Kind:             "l4_rule.update",
		DependencyAction: revision.DependencyActionApply,
		Request: struct {
			ID    int         `json:"id"`
			Input L4RuleInput `json:"input"`
		}{ID: id, Input: input},
		Targets: configMutationTargets(s.cfg, targetAgentIDs, nil),
		ResourceState: func(ctx context.Context, tx *storage.GormStore, _ revision.Target) (any, error) {
			return l4RuleMutationResourceState(ctx, tx, s.cfg)
		},
		ReplayResourceField: "rule",
		ReplayResource:      func() any { return redactL4RuleForPanelReplay(updated) },
		Mutate: func(ctx context.Context, tx *storage.GormStore, revisions map[string]int64) error {
			txService := &l4Service{
				cfg: s.cfg, store: tx, revisionMutation: true, revisionNumbers: revisions,
			}
			var mutateErr error
			updated, mutateErr = txService.updateLegacy(ctx, resolvedID, id, input)
			return mutateErr
		},
	})
	if err != nil {
		return L4Rule{}, err
	}
	if result.NoOp {
		return s.Get(ctx, resolvedID, id)
	}
	return updated, nil
}

func (s *l4Service) updateLegacy(ctx context.Context, agentID string, id int, input L4RuleInput) (L4Rule, error) {
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
		if row.Revision > maxRevision {
			maxRevision = row.Revision
		}
		if !l4RuleRowSupported(row) {
			continue
		}
		rule := l4RuleFromRow(row)
		existing = append(existing, rule)
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
	if err := s.validateL4EgressProfileReference(ctx, rule); err != nil {
		return L4Rule{}, err
	}
	if err := s.validateRelayChain(ctx, resolvedID, rule.RelayChain); err != nil {
		return L4Rule{}, err
	}
	if err := s.validateRelayChain(ctx, resolvedID, flattenRelayLayers(rule.RelayLayers)); err != nil {
		return L4Rule{}, err
	}

	if err := ensureUniqueL4Listen(existing, rule, id); err != nil {
		return L4Rule{}, err
	}
	nextRows := append([]storage.L4RuleRow(nil), rows...)
	nextRows[targetIndex] = l4RuleToRow(rule)
	if err := validateL4RuleSet(l4RulesFromRows(nextRows)); err != nil {
		return L4Rule{}, err
	}
	rule.Revision = configMutationRevision(s.revisionNumbers, resolvedID, allocator.AllocateRevisionForAgent(resolvedID, maxRevision))
	egressExecutorAgentIDs, egressExecutorRevision, err := egressProfileScheduleTargets(ctx, s.store, resolvedID, rule.RelayLayers, rule.EgressProfileID, rule.Revision)
	if err != nil {
		return L4Rule{}, err
	}
	previousEgressExecutorAgentIDs, err := egressProfileExecutorAgentIDsForMutation(ctx, s.store, resolvedID, current.RelayLayers, current.EgressProfileID)
	if err != nil {
		return L4Rule{}, err
	}
	egressExecutorAgentIDs = uniqueAgentIDs(append(egressExecutorAgentIDs, previousEgressExecutorAgentIDs...))
	agentRollbackRows, err := snapshotAgentRowsForRollback(ctx, s.store, uniqueAgentIDs(append([]string{resolvedID}, egressExecutorAgentIDs...)))
	if err != nil {
		return L4Rule{}, err
	}

	rollbackL4Rows := append([]storage.L4RuleRow(nil), rows...)
	rows[targetIndex] = l4RuleToRow(rule)
	if err := s.store.SaveL4Rules(ctx, resolvedID, rows); err != nil {
		return L4Rule{}, err
	}
	if err := s.bumpRemoteDesiredRevision(ctx, resolvedID, rule.Revision); err != nil {
		s.rollbackL4RowsAndAgents(ctx, resolvedID, rollbackL4Rows, agentRollbackRows)
		return L4Rule{}, err
	}
	if err := s.bumpDependentAgentRevisions(ctx, egressExecutorAgentIDs, egressExecutorRevision); err != nil {
		s.rollbackL4RowsAndAgents(ctx, resolvedID, rollbackL4Rows, agentRollbackRows)
		return L4Rule{}, err
	}
	if err := s.triggerLocalApply(ctx, resolvedID); err != nil {
		s.rollbackL4RowsAndAgents(ctx, resolvedID, rollbackL4Rows, agentRollbackRows)
		return L4Rule{}, err
	}
	return rule, nil
}

func (s *l4Service) Delete(ctx context.Context, agentID string, id int) (L4Rule, error) {
	if err := requireConfigMutationStore(s.store, s.mutationExecutor, s.revisionMutation); err != nil {
		return L4Rule{}, err
	}
	if s.mutationExecutor == nil || s.revisionMutation {
		return s.deleteLegacy(ctx, agentID, id)
	}
	resolvedID, err := s.ensureAgentSupportsL4(ctx, agentID)
	if err != nil {
		return L4Rule{}, err
	}
	current, err := s.Get(ctx, resolvedID, id)
	if err != nil {
		return L4Rule{}, err
	}
	targetAgentIDs, err := s.l4MutationAgentIDs(ctx, resolvedID, &current, nil)
	if err != nil {
		return L4Rule{}, err
	}
	var deleted L4Rule
	_, err = s.mutationExecutor.Execute(ctx, revision.MutationRequest{
		Kind:             "l4_rule.delete",
		DependencyAction: revision.DependencyActionDelete,
		Request:          map[string]int{"id": id},
		Targets:          configMutationTargets(s.cfg, targetAgentIDs, nil),
		ResourceState: func(ctx context.Context, tx *storage.GormStore, _ revision.Target) (any, error) {
			return l4RuleMutationResourceState(ctx, tx, s.cfg)
		},
		ReplayResourceField: "rule",
		ReplayResource:      func() any { return redactL4RuleForPanelReplay(deleted) },
		Mutate: func(ctx context.Context, tx *storage.GormStore, revisions map[string]int64) error {
			txService := &l4Service{
				cfg: s.cfg, store: tx, revisionMutation: true, revisionNumbers: revisions,
			}
			var mutateErr error
			deleted, mutateErr = txService.deleteLegacy(ctx, resolvedID, id)
			return mutateErr
		},
	})
	return deleted, err
}

func redactL4RuleForPanelReplay(rule L4Rule) L4Rule {
	rule.ProxyEntryAuth.Password = ""
	return rule
}

func (s *l4Service) deleteLegacy(ctx context.Context, agentID string, id int) (L4Rule, error) {
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
		if !l4RuleRowSupported(row) {
			continue
		}
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
	if err := validateL4RuleSet(l4RulesFromRows(nextRows)); err != nil {
		return L4Rule{}, err
	}
	allocator, err := newConfigIdentityAllocatorFromStore(ctx, s.cfg, s.store)
	if err != nil {
		return L4Rule{}, err
	}
	nextRevision := allocator.AllocateRevisionForAgent(resolvedID, deleted.Revision)
	egressExecutorAgentIDs, err := egressProfileExecutorAgentIDsForMutation(ctx, s.store, resolvedID, deleted.RelayLayers, deleted.EgressProfileID)
	if err != nil {
		return L4Rule{}, err
	}
	agentRollbackRows, err := snapshotAgentRowsForRollback(ctx, s.store, uniqueAgentIDs(append([]string{resolvedID}, egressExecutorAgentIDs...)))
	if err != nil {
		return L4Rule{}, err
	}
	if err := consumeResourceQuota(ctx, s.store, "agent", resolvedID, "public_port_count", -1); err != nil {
		return L4Rule{}, err
	}
	if err := s.store.SaveL4Rules(ctx, resolvedID, nextRows); err != nil {
		return L4Rule{}, err
	}
	if err := s.bumpRemoteDesiredRevision(ctx, resolvedID, nextRevision); err != nil {
		s.rollbackL4RowsAndAgents(ctx, resolvedID, rows, agentRollbackRows)
		return L4Rule{}, err
	}
	if err := s.bumpDependentAgentRevisions(ctx, egressExecutorAgentIDs, nextRevision); err != nil {
		s.rollbackL4RowsAndAgents(ctx, resolvedID, rows, agentRollbackRows)
		return L4Rule{}, err
	}
	if err := s.triggerLocalApply(ctx, resolvedID); err != nil {
		s.rollbackL4RowsAndAgents(ctx, resolvedID, rows, agentRollbackRows)
		return L4Rule{}, err
	}
	_ = deleteTrafficByScopeIfSupported(ctx, s.store, resolvedID, "l4_rule", deleted.ID)
	return deleted, nil
}

func (s *l4Service) bumpRemoteDesiredRevision(ctx context.Context, agentID string, revision int) error {
	if s.revisionMutation {
		return nil
	}
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
		nextRevision := maxInt(revision, row.DesiredRevision, row.CurrentRevision+1)
		if row.DesiredRevision < nextRevision {
			row.DesiredRevision = nextRevision
		}
		return s.store.SaveAgent(ctx, row)
	}
	return ErrAgentNotFound
}

func (s *l4Service) bumpDependentAgentRevisions(ctx context.Context, agentIDs []string, revision int) error {
	if s.revisionMutation {
		return nil
	}
	for _, agentID := range agentIDs {
		if err := s.bumpRemoteDesiredRevision(ctx, agentID, revision); err != nil {
			return err
		}
		if err := s.triggerLocalApply(ctx, agentID); err != nil {
			return err
		}
	}
	return nil
}

func (s *l4Service) l4MutationAgentIDs(
	ctx context.Context,
	ruleAgentID string,
	current *L4Rule,
	input *L4RuleInput,
) ([]string, error) {
	agentIDs := []string{ruleAgentID}
	listeners, err := s.store.ListRelayListeners(ctx, "")
	if err != nil {
		return nil, err
	}
	listenersByID := make(map[int]storage.RelayListenerRow, len(listeners))
	for _, listener := range listeners {
		if listener.ID > 0 {
			listenersByID[listener.ID] = listener
		}
	}

	addGraphTargets := func(layers [][]int, egressProfileID *int) error {
		for _, listenerID := range flattenRelayLayers(layers) {
			if listener, ok := listenersByID[listenerID]; ok {
				agentIDs = append(agentIDs, listener.AgentID)
			}
		}
		executors, err := egressProfileExecutorAgentIDsForMutation(ctx, s.store, ruleAgentID, layers, egressProfileID)
		if err != nil {
			return err
		}
		agentIDs = append(agentIDs, executors...)
		return nil
	}

	var currentLayers [][]int
	var currentEgressProfileID *int
	currentEnabled := false
	if current != nil {
		currentLayers = cloneIntLayers(current.RelayLayers)
		currentEgressProfileID = copyOptionalInt(current.EgressProfileID)
		currentEnabled = current.Enabled
		if currentEnabled {
			if err := addGraphTargets(currentLayers, currentEgressProfileID); err != nil {
				return nil, err
			}
		}
	}
	if input == nil {
		return expandConfigDependencyAgentIDs(ctx, s.cfg, s.store, agentIDs)
	}

	nextLayers := currentLayers
	if input.RelayLayers != nil {
		nextLayers = cloneIntLayers(*input.RelayLayers)
	}
	nextEgressProfileID := currentEgressProfileID
	if input.EgressProfileID != nil {
		nextEgressProfileID = nil
		if *input.EgressProfileID > 0 {
			value := *input.EgressProfileID
			nextEgressProfileID = &value
		}
	}
	nextEnabled := true
	if current != nil {
		nextEnabled = currentEnabled
	}
	if input.Enabled != nil {
		nextEnabled = *input.Enabled
	}
	if nextEnabled {
		if err := addGraphTargets(nextLayers, nextEgressProfileID); err != nil {
			return nil, err
		}
	}
	return expandConfigDependencyAgentIDs(ctx, s.cfg, s.store, agentIDs)
}

func l4RuleMutationResourceState(ctx context.Context, tx *storage.GormStore, cfg config.Config) (any, error) {
	agentIDs, err := allKnownAgentIDs(ctx, cfg, tx)
	if err != nil {
		return nil, err
	}
	rows := make([]storage.L4RuleRow, 0)
	for _, agentID := range agentIDs {
		agentRows, err := tx.ListL4Rules(ctx, agentID)
		if err != nil {
			return nil, err
		}
		for i := range agentRows {
			agentRows[i].Revision = 0
		}
		rows = append(rows, agentRows...)
	}
	return rows, nil
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

	listenMode := strings.ToLower(strings.TrimSpace(defaultString(pointerString(input.ListenMode), fallback.ListenMode)))
	if listenMode == "" {
		listenMode = "tcp"
	}
	if listenMode != "tcp" && listenMode != "proxy" {
		return L4Rule{}, fmt.Errorf("%w: listen_mode must be tcp or proxy", ErrInvalidArgument)
	}
	if listenMode == "proxy" && protocol != "tcp" && protocol != "udp" {
		return L4Rule{}, fmt.Errorf("%w: listen_mode=proxy requires protocol tcp or udp", ErrInvalidArgument)
	}

	listenHost := defaultString(pointerString(input.ListenHost), fallback.ListenHost)
	if listenHost == "" {
		listenHost = "0.0.0.0"
	}

	listenPort := fallback.ListenPort
	if input.ListenPort != nil {
		listenPort = *input.ListenPort
	}
	if listenPort < 0 || listenPort > 65535 {
		return L4Rule{}, fmt.Errorf("%w: listen_port must be a valid port", ErrInvalidArgument)
	}
	egressProfileID, egressProfileErr := normalizeEgressProfileIDInput(input.EgressProfileID, fallback.EgressProfileID)
	if egressProfileErr != nil {
		return L4Rule{}, egressProfileErr
	}

	id := fallback.ID
	if input.ID != nil && *input.ID > 0 {
		id = *input.ID
	}
	if id <= 0 {
		id = suggestedID
	}

	var backends []L4Backend
	var upstreamHost string
	var upstreamPort int

	loadBalancing := normalizeL4LoadBalancingInput(input.LoadBalancing, fallback.LoadBalancing)
	tuning := normalizeL4TuningInput(protocol, input.Tuning, fallback.Tuning)

	var err error
	relayChain := []int{}
	relayLayers := cloneIntLayers(fallback.RelayLayers)
	if input.RelayLayers != nil {
		relayLayers, err = normalizeRelayLayersInput(*input.RelayLayers, protocol)
		if err != nil {
			return L4Rule{}, err
		}
	} else if input.RelayChain != nil {
		return L4Rule{}, fmt.Errorf("%w: relay_chain is legacy; use relay_layers", ErrInvalidArgument)
	}
	proxyEntryAuth := fallback.ProxyEntryAuth
	if input.ProxyEntryAuth != nil {
		proxyEntryAuth = normalizeL4ProxyEntryAuthUpdate(*input.ProxyEntryAuth, fallback.ProxyEntryAuth)
	}
	if listenMode != "proxy" {
		proxyEntryAuth = L4ProxyEntryAuth{}
	}
	proxyEntryMode := listenMode == "proxy"
	if listenPort == 0 {
		return L4Rule{}, fmt.Errorf("%w: listen_port must be a valid port", ErrInvalidArgument)
	}
	backends, upstreamHost, upstreamPort, err = normalizeL4BackendsInput(input, fallback, proxyEntryMode)
	if err != nil {
		if !proxyEntryMode {
			return L4Rule{}, err
		}
		backends = []L4Backend{}
		upstreamHost = ""
		upstreamPort = 0
	}
	if proxyEntryMode {
		backends = []L4Backend{}
		upstreamHost = ""
		upstreamPort = 0
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
	if relayObfs && len(relayChain) == 0 && len(relayLayers) == 0 {
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
		ID:              id,
		AgentID:         fallback.AgentID,
		Name:            name,
		Protocol:        protocol,
		ListenHost:      listenHost,
		ListenPort:      listenPort,
		UpstreamHost:    upstreamHost,
		UpstreamPort:    upstreamPort,
		Backends:        backends,
		LoadBalancing:   loadBalancing,
		Tuning:          tuning,
		RelayChain:      relayChain,
		RelayLayers:     relayLayers,
		RelayObfs:       relayObfs,
		ListenMode:      listenMode,
		EgressProfileID: egressProfileID,
		ProxyEntryAuth:  proxyEntryAuth,
		Enabled:         enabled,
		Tags:            tags,
		Revision:        fallback.Revision,
	}, nil
}

func (s *l4Service) validateRelayChain(ctx context.Context, agentID string, relayChain []int) error {
	knownAgentIDs, err := s.allKnownAgentIDs(ctx)
	if err != nil {
		return err
	}
	return validateRelayChainReferences(ctx, s.store, knownAgentIDs, relayChain, relayChainValidationOptions{
		RuleAgentID: agentID,
	})
}

func (s *l4Service) validateL4EgressProfileReference(ctx context.Context, rule L4Rule) error {
	return validateL4EgressProfileReferenceForStore(ctx, s.cfg, s.store, rule)
}

type l4EgressProfileValidationStore interface {
	egressProfileCapabilityStore
	egressProfileLookupStore
}

func validateL4EgressProfileReferenceForStore(ctx context.Context, cfg config.Config, store l4EgressProfileValidationStore, rule L4Rule) error {
	if rule.EgressProfileID == nil {
		return nil
	}
	profile, err := getEnabledEgressProfile(ctx, store, *rule.EgressProfileID)
	if err != nil {
		return err
	}
	if !egressProfileSupportsL4(profile) {
		return fmt.Errorf("%w: egress profile %d does not support L4 rules", ErrInvalidArgument, profile.ID)
	}
	if strings.EqualFold(rule.Protocol, "udp") && strings.EqualFold(profile.Type, "http") {
		return fmt.Errorf("%w: UDP rules cannot use HTTP egress profiles", ErrInvalidArgument)
	}
	if err := ensureEgressProfileExecutorsSupportCapability(ctx, cfg, store, rule.AgentID, rule.RelayLayers); err != nil {
		return err
	}
	return nil
}

func (s *l4Service) rollbackL4RowsAndAgents(ctx context.Context, agentID string, l4Rows []storage.L4RuleRow, agentRows []storage.AgentRow) {
	restoreAgentRowsBestEffort(ctx, s.store, agentRows)
	_ = s.store.SaveL4Rules(ctx, agentID, append([]storage.L4RuleRow(nil), l4Rows...))
}

func stringSlicesEqual(a []string, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func (s *l4Service) allKnownAgentIDs(ctx context.Context) ([]string, error) {
	return allKnownAgentIDs(ctx, s.cfg, s.store)
}

func normalizeL4BackendsInput(input L4RuleInput, fallback L4Rule, allowEmpty bool) ([]L4Backend, string, int, error) {
	if input.Backends != nil {
		backends := normalizeL4BackendList(*input.Backends)
		if len(backends) == 0 {
			if allowEmpty {
				return []L4Backend{}, "", 0, nil
			}
			return nil, "", 0, fmt.Errorf("%w: at least one valid backend is required", ErrInvalidArgument)
		}
		return backends, "", 0, nil
	}

	if input.UpstreamHost != nil || input.UpstreamPort != nil {
		return nil, "", 0, fmt.Errorf("%w: upstream_host/upstream_port are legacy; use backends", ErrInvalidArgument)
	}

	if len(fallback.Backends) > 0 {
		backends := normalizeL4BackendList(fallback.Backends)
		if len(backends) == 0 {
			if allowEmpty {
				return []L4Backend{}, "", 0, nil
			}
			return nil, "", 0, fmt.Errorf("%w: at least one valid backend is required", ErrInvalidArgument)
		}
		return backends, "", 0, nil
	}

	if allowEmpty {
		return []L4Backend{}, "", 0, nil
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

func normalizeL4ProxyEntryAuth(auth L4ProxyEntryAuth) L4ProxyEntryAuth {
	return L4ProxyEntryAuth{
		Enabled:  auth.Enabled,
		Username: strings.TrimSpace(auth.Username),
		Password: auth.Password,
	}
}

func normalizeL4ProxyEntryAuthUpdate(auth L4ProxyEntryAuth, fallback L4ProxyEntryAuth) L4ProxyEntryAuth {
	normalized := normalizeL4ProxyEntryAuth(auth)
	if normalized.Enabled && normalized.Password == "" && fallback.Password != "" {
		normalized.Password = fallback.Password
	}
	return normalized
}

func normalizeProxyURLUpdate(raw string, fallback string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if !hasRedactedProxyPassword(trimmed) {
		return trimmed, nil
	}
	if trimmed == redactProxyURLPassword(fallback) {
		return strings.TrimSpace(fallback), nil
	}
	return "", fmt.Errorf("%w: proxy URL password is redacted; re-enter the password before saving changes", ErrInvalidArgument)
}

func hasRedactedProxyPassword(raw string) bool {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.User == nil {
		return false
	}
	password, ok := parsed.User.Password()
	return ok && password == redactedProxyPassword
}

func redactProxyURLPassword(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.User == nil {
		return strings.TrimSpace(raw)
	}
	password, ok := parsed.User.Password()
	if !ok || password == "" {
		return strings.TrimSpace(raw)
	}
	parsed.User = url.UserPassword(parsed.User.Username(), redactedProxyPassword)
	return parsed.String()
}

func validateProxyURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("parse proxy URL: %w", err)
	}
	if parsed.Scheme == "" {
		return fmt.Errorf("proxy URL missing scheme")
	}
	if parsed.Host == "" {
		return fmt.Errorf("proxy URL missing host")
	}
	switch strings.ToLower(parsed.Scheme) {
	case "socks", "socks4", "socks4a", "socks5", "socks5h", "http":
	default:
		return fmt.Errorf("unsupported proxy URL scheme %q", parsed.Scheme)
	}
	host, port, err := net.SplitHostPort(parsed.Host)
	if err != nil {
		return fmt.Errorf("proxy URL must include host and port: %w", err)
	}
	if host == "" {
		return fmt.Errorf("proxy URL missing host")
	}
	if port == "" {
		return fmt.Errorf("proxy URL missing port")
	}
	n, err := strconv.Atoi(port)
	if err != nil {
		return fmt.Errorf("proxy URL port must be numeric: %w", err)
	}
	if n < 1 || n > 65535 {
		return fmt.Errorf("proxy URL port out of range")
	}
	return nil
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
	if next.Enabled && isUDPProxyEntryRule(next) && !hasSamePortTCPProxyEntry(rules, next, excludeID) {
		return fmt.Errorf("%w: udp proxy entry requires a same-port TCP SOCKS5 proxy entry on the same agent", ErrInvalidArgument)
	}

	nextListenHost := effectiveL4ListenHost(next)
	for _, rule := range rules {
		if rule.ID == excludeID {
			continue
		}
		if l4ListenConflicts(rule, next) {
			return newConflictError(
				"listen %s:%s:%d conflicts with rule #%d",
				next.Protocol,
				nextListenHost,
				next.ListenPort,
				rule.ID,
			)
		}
	}
	return nil
}

func validateL4RuleSet(rules []L4Rule) error {
	for _, rule := range rules {
		if !rule.Enabled {
			continue
		}
		if !isUDPProxyEntryRule(rule) {
			continue
		}
		if !hasSamePortTCPProxyEntry(rules, rule, rule.ID) {
			return fmt.Errorf("%w: udp proxy entry requires a same-port TCP SOCKS5 proxy entry on the same agent", ErrInvalidArgument)
		}
	}
	return nil
}

func isUDPProxyEntryRule(rule L4Rule) bool {
	return strings.EqualFold(strings.TrimSpace(rule.ListenMode), "proxy") &&
		strings.EqualFold(strings.TrimSpace(rule.Protocol), "udp")
}

func isTCPProxyControlRule(rule L4Rule) bool {
	return strings.EqualFold(strings.TrimSpace(rule.ListenMode), "proxy") &&
		strings.EqualFold(strings.TrimSpace(rule.Protocol), "tcp")
}

func hasSamePortTCPProxyEntry(rules []L4Rule, next L4Rule, excludeID int) bool {
	for _, rule := range rules {
		if rule.ID == excludeID {
			continue
		}
		if !rule.Enabled || !isTCPProxyControlRule(rule) {
			continue
		}
		if effectiveL4ListenHost(rule) == effectiveL4ListenHost(next) && rule.ListenPort == next.ListenPort {
			return true
		}
	}
	return false
}

func l4ListenConflicts(rule L4Rule, next L4Rule) bool {
	if !strings.EqualFold(strings.TrimSpace(rule.Protocol), strings.TrimSpace(next.Protocol)) ||
		rule.ListenPort != next.ListenPort {
		return false
	}
	return effectiveL4ListenHost(rule) == effectiveL4ListenHost(next)
}

func effectiveL4ListenHost(rule L4Rule) string {
	return strings.TrimSpace(rule.ListenHost)
}

func effectiveL4ListenStack(L4Rule) string {
	return "host"
}

func l4RuleFromRow(row storage.L4RuleRow) L4Rule {
	listenMode := strings.ToLower(strings.TrimSpace(defaultString(row.ListenMode, "tcp")))
	if listenMode == "" {
		listenMode = "tcp"
	}
	proxyEntryAuth := parseL4ProxyEntryAuth(row.ProxyEntryAuthJSON)
	if listenMode != "proxy" {
		proxyEntryAuth = L4ProxyEntryAuth{}
	}

	rule := L4Rule{
		ID:            row.ID,
		AgentID:       row.AgentID,
		Name:          row.Name,
		Protocol:      defaultString(row.Protocol, "tcp"),
		ListenHost:    defaultString(row.ListenHost, "0.0.0.0"),
		ListenPort:    row.ListenPort,
		UpstreamHost:  "",
		UpstreamPort:  0,
		LoadBalancing: L4LoadBalancing{Strategy: "adaptive"},
		Tuning:        L4Tuning{ProxyProtocol: L4ProxyProtocolTuning{}},
		RelayChain:    []int{},
		RelayLayers:   [][]int{},
		RelayObfs:     row.RelayObfs,
		ListenMode:    listenMode,

		EgressProfileID: normalizeOptionalPositiveInt(row.EgressProfileID),

		ProxyEntryAuth: proxyEntryAuth,
		Enabled:        row.Enabled,
		Tags:           parseStringArray(row.TagsJSON),
		Revision:       row.Revision,
	}

	if backends := parseL4Backends(row.BackendsJSON); len(backends) > 0 {
		rule.Backends = backends
	}

	if lb := parseL4LoadBalancing(row.LoadBalancingJSON); lb.Strategy != "" {
		rule.LoadBalancing = lb
	}
	if tuning := parseL4Tuning(row.TuningJSON); tuning != (L4Tuning{}) {
		rule.Tuning = tuning
	}
	rule.RelayChain = []int{}
	rule.RelayLayers = parseIntLayers(row.RelayLayersJSON)
	return rule
}

func l4RulesFromRows(rows []storage.L4RuleRow) []L4Rule {
	rules := make([]L4Rule, 0, len(rows))
	for _, row := range rows {
		if !l4RuleRowSupported(row) {
			continue
		}
		rules = append(rules, l4RuleFromRow(row))
	}
	return rules
}

func l4RuleRowSupported(row storage.L4RuleRow) bool {
	switch strings.ToLower(strings.TrimSpace(defaultString(row.ListenMode, "tcp"))) {
	case "tcp", "proxy":
		return true
	default:
		return false
	}
}

func l4RuleToRow(rule L4Rule) storage.L4RuleRow {
	return storage.L4RuleRow{
		ID:                rule.ID,
		AgentID:           rule.AgentID,
		Name:              rule.Name,
		Protocol:          rule.Protocol,
		ListenHost:        rule.ListenHost,
		ListenPort:        rule.ListenPort,
		UpstreamHost:      "",
		UpstreamPort:      0,
		BackendsJSON:      marshalJSON(rule.Backends, "[]"),
		LoadBalancingJSON: marshalJSON(rule.LoadBalancing, `{"strategy":"adaptive"}`),
		TuningJSON:        marshalJSON(rule.Tuning, `{"proxy_protocol":{"decode":false,"send":false}}`),
		RelayChainJSON:    "[]",
		RelayLayersJSON:   marshalJSON(rule.RelayLayers, "[]"),
		RelayObfs:         rule.RelayObfs,
		ListenMode:        defaultString(rule.ListenMode, "tcp"),

		EgressProfileID: normalizeOptionalPositiveInt(rule.EgressProfileID),

		ProxyEntryAuthJSON: marshalJSON(rule.ProxyEntryAuth, "{}"),
		Enabled:            rule.Enabled,
		TagsJSON:           marshalJSON(rule.Tags, "[]"),
		Revision:           rule.Revision,
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

func parseL4ProxyEntryAuth(raw string) L4ProxyEntryAuth {
	var auth L4ProxyEntryAuth
	if strings.TrimSpace(raw) == "" {
		return auth
	}
	if err := json.Unmarshal([]byte(raw), &auth); err != nil {
		return L4ProxyEntryAuth{}
	}
	return normalizeL4ProxyEntryAuth(auth)
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
