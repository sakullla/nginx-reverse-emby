package service

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

type HTTPRuleInput struct {
	ID                       *int                `json:"id,omitempty"`
	FrontendURL              *string             `json:"frontend_url,omitempty"`
	BackendURL               *string             `json:"backend_url,omitempty"`
	Backends                 *[]HTTPRuleBackend  `json:"backends,omitempty"`
	LoadBalancing            *HTTPLoadBalancing  `json:"load_balancing,omitempty"`
	Enabled                  *bool               `json:"enabled,omitempty"`
	Tags                     *[]string           `json:"tags,omitempty"`
	ProxyRedirect            *bool               `json:"proxy_redirect,omitempty"`
	RelayChain               *[]int              `json:"relay_chain,omitempty"`
	RelayLayers              *[][]int            `json:"relay_layers,omitempty"`
	RelayObfs                *bool               `json:"relay_obfs,omitempty"`
	PassProxyHeaders         *bool               `json:"pass_proxy_headers,omitempty"`
	UserAgent                *string             `json:"user_agent,omitempty"`
	CustomHeaders            *[]HTTPCustomHeader `json:"custom_headers,omitempty"`
	EgressProfileID          *int                `json:"egress_profile_id,omitempty"`
	WireGuardEntryEnabled    *bool               `json:"wireguard_entry_enabled,omitempty"`
	WireGuardProfileID       *int                `json:"wireguard_profile_id,omitempty"`
	WireGuardEntryListenHost *string             `json:"wireguard_entry_listen_host,omitempty"`
	WireGuardEntryListenPort *int                `json:"wireguard_entry_listen_port,omitempty"`
}

type ruleStore interface {
	ListAgents(context.Context) ([]storage.AgentRow, error)
	ListHTTPRules(context.Context, string) ([]storage.HTTPRuleRow, error)
	GetHTTPRule(context.Context, string, int) (storage.HTTPRuleRow, bool, error)
	ListL4Rules(context.Context, string) ([]storage.L4RuleRow, error)
	ListWireGuardProfiles(context.Context, string) ([]storage.WireGuardProfileRow, error)
	ListEgressProfiles(context.Context) ([]storage.EgressProfileRow, error)
	LoadLocalAgentState(context.Context) (storage.LocalAgentStateRow, error)
	ListManagedCertificates(context.Context) ([]storage.ManagedCertificateRow, error)
	ListRelayListeners(context.Context, string) ([]storage.RelayListenerRow, error)
	SaveAgent(context.Context, storage.AgentRow) error
	SaveHTTPRules(context.Context, string, []storage.HTTPRuleRow) error
	SaveManagedCertificates(context.Context, []storage.ManagedCertificateRow) error
	SaveWireGuardProfiles(context.Context, string, []storage.WireGuardProfileRow) error
	CleanupManagedCertificateMaterial(context.Context, []storage.ManagedCertificateRow, []storage.ManagedCertificateRow) error
}

type ruleService struct {
	cfg               config.Config
	store             ruleStore
	localApplyTrigger func(context.Context) error
}

func NewRuleService(cfg config.Config, store ruleStore) *ruleService {
	return &ruleService{cfg: cfg, store: store}
}

func (s *ruleService) SetLocalApplyTrigger(trigger func(context.Context) error) {
	s.localApplyTrigger = wrapLocalApplyTrigger(trigger)
}

func (s *ruleService) triggerLocalApply(ctx context.Context, agentID string) error {
	if !s.cfg.EnableLocalAgent || agentID != s.cfg.LocalAgentID || s.localApplyTrigger == nil {
		return nil
	}
	return s.localApplyTrigger(ctx)
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

func (s *ruleService) Get(ctx context.Context, agentID string, id int) (HTTPRule, error) {
	resolvedID, err := s.ensureAgentExists(ctx, agentID)
	if err != nil {
		return HTTPRule{}, err
	}

	row, ok, err := s.store.GetHTTPRule(ctx, resolvedID, id)
	if err != nil {
		return HTTPRule{}, err
	}
	if !ok {
		return HTTPRule{}, ErrRuleNotFound
	}
	return httpRuleFromRow(row), nil
}

func (s *ruleService) Create(ctx context.Context, agentID string, input HTTPRuleInput) (HTTPRule, error) {
	resolvedID, err := s.ensureAgentExists(ctx, agentID)
	if err != nil {
		return HTTPRule{}, err
	}
	var defaultWireGuardRollback *wireGuardProfileRollback
	if httpRuleInputEnablesWireGuard(input, HTTPRule{}) {
		if err := ensureAgentSupportsWireGuardCapability(ctx, s.cfg, s.store, resolvedID); err != nil {
			return HTTPRule{}, err
		}
		if input.WireGuardProfileID == nil {
			profile, rollback, err := s.ensureDefaultHTTPWireGuardProfileWithRollback(ctx, resolvedID)
			if err != nil {
				return HTTPRule{}, err
			}
			defaultWireGuardRollback = rollback
			input.WireGuardProfileID = &profile.ID
		}
	}
	var relayLayerWireGuardEnsure relayLayerWireGuardProfileEnsureResult
	rollbackDefaultWireGuard := func() {
		restoreWireGuardProfileRollbacks(ctx, s.store, relayLayerWireGuardEnsure.Rollbacks)
		restoreWireGuardProfileRollback(ctx, s.store, resolvedID, defaultWireGuardRollback)
	}

	rows, err := s.store.ListHTTPRules(ctx, resolvedID)
	if err != nil {
		rollbackDefaultWireGuard()
		return HTTPRule{}, err
	}
	allRows, err := s.listRulesAcrossAllAgents(ctx)
	if err != nil {
		rollbackDefaultWireGuard()
		return HTTPRule{}, err
	}
	allocator, err := newConfigIdentityAllocatorFromStore(ctx, s.cfg, s.store)
	if err != nil {
		rollbackDefaultWireGuard()
		return HTTPRule{}, err
	}

	maxRevision := 0
	for _, row := range allRows {
		if row.Revision > maxRevision {
			maxRevision = row.Revision
		}
	}

	allocatedID := allocator.AllocateRuleID(preferredInt(input.ID))
	normalizedInput := input
	// Keep the caller's preferred ID only for allocator conflict resolution.
	// Normalization should see the assigned ID, not re-read the raw preference.
	normalizedInput.ID = nil
	rule, err := s.normalizeHTTPRuleInput(ctx, normalizedInput, HTTPRule{AgentID: resolvedID}, allocatedID)
	if err != nil {
		rollbackDefaultWireGuard()
		return HTTPRule{}, err
	}
	rule.AgentID = resolvedID
	relayLayerWireGuardEnsure, err = ensureDefaultWireGuardProfilesForRelayLayers(ctx, s.cfg, s.store, resolvedID, rule.RelayLayers)
	if err != nil {
		rollbackDefaultWireGuard()
		return HTTPRule{}, err
	}
	rule.Revision = allocator.AllocateRevisionForAgent(resolvedID, maxRevision)
	if err := validateUniqueHTTPFrontendBinding(append(rows, httpRuleToRow(rule))); err != nil {
		rollbackDefaultWireGuard()
		return HTTPRule{}, err
	}
	if err := validateUniqueHTTPWireGuardEntryRoutes(append(rows, httpRuleToRow(rule))); err != nil {
		rollbackDefaultWireGuard()
		return HTTPRule{}, err
	}
	egressExecutorAgentIDs, egressExecutorRevision, err := egressProfileScheduleTargets(ctx, s.store, resolvedID, rule.RelayLayers, rule.EgressProfileID, rule.Revision)
	if err != nil {
		rollbackDefaultWireGuard()
		return HTTPRule{}, err
	}
	agentRollbackRows, err := snapshotAgentRowsForRollback(ctx, s.store, uniqueAgentIDs(append(append([]string{resolvedID}, relayLayerWireGuardEnsure.CallerAgentIDs...), egressExecutorAgentIDs...)))
	if err != nil {
		rollbackDefaultWireGuard()
		return HTTPRule{}, err
	}

	nextRows := append(append([]storage.HTTPRuleRow(nil), rows...), httpRuleToRow(rule))
	certRowsChanged := false
	var originalCertRows []storage.ManagedCertificateRow
	var nextCertRows []storage.ManagedCertificateRow
	if scheme, _, ok := parseRuleFrontendTarget(rule.FrontendURL); ok && scheme == "https" {
		originalCertRows, nextCertRows, certRowsChanged, err = s.prepareManagedCertificatesForRuleMutation(
			ctx,
			resolvedID,
			&rule,
			httpRulesFromRows(nextRows),
			false,
		)
		if err != nil {
			rollbackDefaultWireGuard()
			return HTTPRule{}, err
		}
		if certRowsChanged {
			if err := s.store.SaveManagedCertificates(ctx, nextCertRows); err != nil {
				rollbackDefaultWireGuard()
				return HTTPRule{}, err
			}
		}
	}
	if err := s.store.SaveHTTPRules(ctx, resolvedID, nextRows); err != nil {
		if certRowsChanged {
			if rollbackErr := s.store.SaveManagedCertificates(ctx, originalCertRows); rollbackErr != nil {
				rollbackDefaultWireGuard()
				return HTTPRule{}, fmt.Errorf("%v (rollback failed: %v)", err, rollbackErr)
			}
		}
		rollbackDefaultWireGuard()
		return HTTPRule{}, err
	}
	rollbackPostSave := func(err error) (HTTPRule, error) {
		restoreAgentRowsBestEffort(ctx, s.store, agentRollbackRows)
		if rollbackErr := s.store.SaveHTTPRules(ctx, resolvedID, rows); rollbackErr != nil {
			rollbackDefaultWireGuard()
			return HTTPRule{}, fmt.Errorf("%v (rollback failed: %v)", err, rollbackErr)
		}
		if certRowsChanged {
			if rollbackErr := s.store.SaveManagedCertificates(ctx, originalCertRows); rollbackErr != nil {
				rollbackDefaultWireGuard()
				return HTTPRule{}, fmt.Errorf("%v (rollback failed: %v)", err, rollbackErr)
			}
		}
		rollbackDefaultWireGuard()
		return HTTPRule{}, err
	}
	if err := s.bumpRemoteDesiredRevision(ctx, resolvedID, rule.Revision); err != nil {
		return rollbackPostSave(err)
	}
	if err := s.bumpRelayLayerWireGuardCallers(ctx, relayLayerWireGuardEnsure.CallerAgentIDs, rule.Revision); err != nil {
		return rollbackPostSave(err)
	}
	if err := s.bumpRelayLayerWireGuardCallers(ctx, egressExecutorAgentIDs, egressExecutorRevision); err != nil {
		return rollbackPostSave(err)
	}
	if err := s.triggerLocalApply(ctx, resolvedID); err != nil {
		return rollbackPostSave(err)
	}
	if certRowsChanged {
		cleanupManagedCertificateMaterialBestEffort(ctx, s.store, originalCertRows, nextCertRows)
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
	var defaultWireGuardRollback *wireGuardProfileRollback
	if httpRuleInputEnablesWireGuard(input, current) {
		if err := ensureAgentSupportsWireGuardCapability(ctx, s.cfg, s.store, resolvedID); err != nil {
			return HTTPRule{}, err
		}
		if input.WireGuardProfileID == nil && current.WireGuardProfileID == nil {
			profile, rollback, err := s.ensureDefaultHTTPWireGuardProfileWithRollback(ctx, resolvedID)
			if err != nil {
				return HTTPRule{}, err
			}
			defaultWireGuardRollback = rollback
			input.WireGuardProfileID = &profile.ID
		}
	}
	var relayLayerWireGuardEnsure relayLayerWireGuardProfileEnsureResult
	rollbackDefaultWireGuard := func() {
		restoreWireGuardProfileRollbacks(ctx, s.store, relayLayerWireGuardEnsure.Rollbacks)
		restoreWireGuardProfileRollback(ctx, s.store, resolvedID, defaultWireGuardRollback)
	}

	allocator, err := newConfigIdentityAllocatorFromStore(ctx, s.cfg, s.store)
	if err != nil {
		rollbackDefaultWireGuard()
		return HTTPRule{}, err
	}

	rule, err := s.normalizeHTTPRuleInput(ctx, input, current, id)
	if err != nil {
		rollbackDefaultWireGuard()
		return HTTPRule{}, err
	}
	rule.AgentID = resolvedID
	relayLayerWireGuardEnsure, err = ensureDefaultWireGuardProfilesForRelayLayers(ctx, s.cfg, s.store, resolvedID, rule.RelayLayers)
	if err != nil {
		rollbackDefaultWireGuard()
		return HTTPRule{}, err
	}
	rule.Revision = allocator.AllocateRevisionForAgent(resolvedID, maxRevision)

	nextRows := append([]storage.HTTPRuleRow(nil), rows...)
	nextRows[targetIndex] = httpRuleToRow(rule)
	if err := validateUniqueHTTPFrontendBinding(nextRows); err != nil {
		rollbackDefaultWireGuard()
		return HTTPRule{}, err
	}
	if err := validateUniqueHTTPWireGuardEntryRoutes(nextRows); err != nil {
		rollbackDefaultWireGuard()
		return HTTPRule{}, err
	}
	originalCertRows, nextCertRows, certRowsChanged, err := s.prepareManagedCertificatesForRuleMutation(
		ctx,
		resolvedID,
		&rule,
		httpRulesFromRows(nextRows),
		true,
	)
	if err != nil {
		rollbackDefaultWireGuard()
		return HTTPRule{}, err
	}
	if certRowsChanged {
		if err := s.store.SaveManagedCertificates(ctx, nextCertRows); err != nil {
			rollbackDefaultWireGuard()
			return HTTPRule{}, err
		}
	}
	egressExecutorAgentIDs, egressExecutorRevision, err := egressProfileScheduleTargets(ctx, s.store, resolvedID, rule.RelayLayers, rule.EgressProfileID, rule.Revision)
	if err != nil {
		if certRowsChanged {
			if rollbackErr := s.store.SaveManagedCertificates(ctx, originalCertRows); rollbackErr != nil {
				rollbackDefaultWireGuard()
				return HTTPRule{}, fmt.Errorf("%v (rollback failed: %v)", err, rollbackErr)
			}
		}
		rollbackDefaultWireGuard()
		return HTTPRule{}, err
	}
	previousEgressExecutorAgentIDs, err := egressProfileExecutorAgentIDsForMutation(ctx, s.store, resolvedID, current.RelayLayers, current.EgressProfileID)
	if err != nil {
		if certRowsChanged {
			if rollbackErr := s.store.SaveManagedCertificates(ctx, originalCertRows); rollbackErr != nil {
				rollbackDefaultWireGuard()
				return HTTPRule{}, fmt.Errorf("%v (rollback failed: %v)", err, rollbackErr)
			}
		}
		rollbackDefaultWireGuard()
		return HTTPRule{}, err
	}
	egressExecutorAgentIDs = uniqueAgentIDs(append(egressExecutorAgentIDs, previousEgressExecutorAgentIDs...))
	agentRollbackRows, err := snapshotAgentRowsForRollback(ctx, s.store, uniqueAgentIDs(append(append([]string{resolvedID}, relayLayerWireGuardEnsure.CallerAgentIDs...), egressExecutorAgentIDs...)))
	if err != nil {
		if certRowsChanged {
			if rollbackErr := s.store.SaveManagedCertificates(ctx, originalCertRows); rollbackErr != nil {
				rollbackDefaultWireGuard()
				return HTTPRule{}, fmt.Errorf("%v (rollback failed: %v)", err, rollbackErr)
			}
		}
		rollbackDefaultWireGuard()
		return HTTPRule{}, err
	}
	if err := s.store.SaveHTTPRules(ctx, resolvedID, nextRows); err != nil {
		if certRowsChanged {
			if rollbackErr := s.store.SaveManagedCertificates(ctx, originalCertRows); rollbackErr != nil {
				rollbackDefaultWireGuard()
				return HTTPRule{}, fmt.Errorf("%v (rollback failed: %v)", err, rollbackErr)
			}
		}
		rollbackDefaultWireGuard()
		return HTTPRule{}, err
	}
	rollbackPostSave := func(err error) (HTTPRule, error) {
		restoreAgentRowsBestEffort(ctx, s.store, agentRollbackRows)
		if rollbackErr := s.store.SaveHTTPRules(ctx, resolvedID, rows); rollbackErr != nil {
			rollbackDefaultWireGuard()
			return HTTPRule{}, fmt.Errorf("%v (rollback failed: %v)", err, rollbackErr)
		}
		if certRowsChanged {
			if rollbackErr := s.store.SaveManagedCertificates(ctx, originalCertRows); rollbackErr != nil {
				rollbackDefaultWireGuard()
				return HTTPRule{}, fmt.Errorf("%v (rollback failed: %v)", err, rollbackErr)
			}
		}
		rollbackDefaultWireGuard()
		return HTTPRule{}, err
	}
	if err := s.bumpRemoteDesiredRevision(ctx, resolvedID, rule.Revision); err != nil {
		return rollbackPostSave(err)
	}
	if err := s.bumpRelayLayerWireGuardCallers(ctx, relayLayerWireGuardEnsure.CallerAgentIDs, rule.Revision); err != nil {
		return rollbackPostSave(err)
	}
	if err := s.bumpRelayLayerWireGuardCallers(ctx, egressExecutorAgentIDs, egressExecutorRevision); err != nil {
		return rollbackPostSave(err)
	}
	if err := s.triggerLocalApply(ctx, resolvedID); err != nil {
		return rollbackPostSave(err)
	}
	if certRowsChanged {
		cleanupManagedCertificateMaterialBestEffort(ctx, s.store, originalCertRows, nextCertRows)
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
	originalCertRows, nextCertRows, certRowsChanged, err := s.prepareManagedCertificatesForRuleMutation(
		ctx,
		resolvedID,
		nil,
		httpRulesFromRows(nextRows),
		true,
	)
	if err != nil {
		return HTTPRule{}, err
	}
	egressExecutorAgentIDs, err := egressProfileExecutorAgentIDsForMutation(ctx, s.store, resolvedID, deleted.RelayLayers, deleted.EgressProfileID)
	if err != nil {
		return HTTPRule{}, err
	}
	agentRollbackRows, err := snapshotAgentRowsForRollback(ctx, s.store, uniqueAgentIDs(append([]string{resolvedID}, egressExecutorAgentIDs...)))
	if err != nil {
		return HTTPRule{}, err
	}
	if certRowsChanged {
		if err := s.store.SaveManagedCertificates(ctx, nextCertRows); err != nil {
			return HTTPRule{}, err
		}
	}
	if err := s.store.SaveHTTPRules(ctx, resolvedID, nextRows); err != nil {
		if certRowsChanged {
			if rollbackErr := s.store.SaveManagedCertificates(ctx, originalCertRows); rollbackErr != nil {
				return HTTPRule{}, fmt.Errorf("%v (rollback failed: %v)", err, rollbackErr)
			}
		}
		return HTTPRule{}, err
	}
	rollbackPostSave := func(err error) (HTTPRule, error) {
		restoreAgentRowsBestEffort(ctx, s.store, agentRollbackRows)
		if rollbackErr := s.store.SaveHTTPRules(ctx, resolvedID, rows); rollbackErr != nil {
			return HTTPRule{}, fmt.Errorf("%v (rollback failed: %v)", err, rollbackErr)
		}
		if certRowsChanged {
			if rollbackErr := s.store.SaveManagedCertificates(ctx, originalCertRows); rollbackErr != nil {
				return HTTPRule{}, fmt.Errorf("%v (rollback failed: %v)", err, rollbackErr)
			}
		}
		return HTTPRule{}, err
	}
	allocator, err := newConfigIdentityAllocatorFromStore(ctx, s.cfg, s.store)
	if err != nil {
		return rollbackPostSave(err)
	}
	nextRevision := allocator.AllocateRevisionForAgent(resolvedID, deleted.Revision)
	if err := s.bumpRemoteDesiredRevision(ctx, resolvedID, nextRevision); err != nil {
		return rollbackPostSave(err)
	}
	if err := s.bumpRelayLayerWireGuardCallers(ctx, egressExecutorAgentIDs, nextRevision); err != nil {
		return rollbackPostSave(err)
	}
	if err := s.triggerLocalApply(ctx, resolvedID); err != nil {
		return rollbackPostSave(err)
	}
	if certRowsChanged {
		cleanupManagedCertificateMaterialBestEffort(ctx, s.store, originalCertRows, nextCertRows)
	}
	_ = deleteTrafficByScopeIfSupported(ctx, s.store, resolvedID, "http_rule", deleted.ID)
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
		nextRevision := maxInt(revision, row.DesiredRevision, row.CurrentRevision+1)
		if row.DesiredRevision < nextRevision {
			row.DesiredRevision = nextRevision
		}
		return s.store.SaveAgent(ctx, row)
	}
	return ErrAgentNotFound
}

func (s *ruleService) bumpRelayLayerWireGuardCallers(ctx context.Context, agentIDs []string, revision int) error {
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

func (s *ruleService) listL4RulesAcrossAllAgents(ctx context.Context) ([]storage.L4RuleRow, error) {
	agentIDs, err := s.allKnownAgentIDs(ctx)
	if err != nil {
		return nil, err
	}

	rows := make([]storage.L4RuleRow, 0)
	for _, agentID := range agentIDs {
		agentRows, err := s.store.ListL4Rules(ctx, agentID)
		if err != nil {
			return nil, err
		}
		rows = append(rows, agentRows...)
	}
	return rows, nil
}

func (s *ruleService) allKnownAgentIDs(ctx context.Context) ([]string, error) {
	return allKnownAgentIDs(ctx, s.cfg, s.store)
}

func (s *ruleService) prepareManagedCertificatesForRuleMutation(
	ctx context.Context,
	agentID string,
	rule *HTTPRule,
	nextRules []HTTPRule,
	cleanupUnused bool,
) ([]storage.ManagedCertificateRow, []storage.ManagedCertificateRow, bool, error) {
	originalRows, err := s.store.ListManagedCertificates(ctx)
	if err != nil {
		return nil, nil, false, err
	}
	nextRows := append([]storage.ManagedCertificateRow(nil), originalRows...)
	nextRevision := nextManagedCertificateRevision(nextRows)
	if rule != nil {
		if err := s.ensureManagedCertificateForRule(ctx, agentID, *rule, &nextRows, &nextRevision); err != nil {
			return nil, nil, false, err
		}
	}
	if cleanupUnused {
		if err := s.cleanupUnusedManagedCertificatesForAgent(agentID, nextRules, &nextRows, &nextRevision); err != nil {
			return nil, nil, false, err
		}
	}
	return originalRows, nextRows, !managedCertificateRowsEqual(originalRows, nextRows), nil
}

func (s *ruleService) ensureManagedCertificateForRule(
	ctx context.Context,
	agentID string,
	rule HTTPRule,
	rows *[]storage.ManagedCertificateRow,
	nextRevision *int,
) error {
	scheme, host, ok := parseRuleFrontendTarget(rule.FrontendURL)
	if !ok || scheme != "https" {
		return nil
	}

	scope := "domain"
	if isIPAddress(host) {
		scope = "ip"
	}
	cert, certIndex, found := findBestManagedCertificateForHost(*rows, agentID, host, scope)
	if found {
		if containsString(cert.TargetAgentIDs, agentID) {
			return nil
		}
		if cert.IssuerMode == "master_cf_dns" {
			return nil
		}
		next := cert
		next.Enabled = true
		next.TargetAgentIDs = appendUniqueNormalized(next.TargetAgentIDs, agentID)
		next.Tags = normalizeTagUnion(next.Tags, []string{managedCertificateAutoTargetTag(agentID)})
		if err := assertManagedCertificateTargetingAllowed(s.cfg, next); err != nil {
			return err
		}
		if err := assertManagedCertificateMutationAllowed(&cert, next); err != nil {
			return err
		}
		next.Revision = allocateManagedCertificateRevision(nextRevision)
		(*rows)[certIndex] = managedCertificateToRow(next)
		return nil
	}

	issuerMode, err := s.chooseAutoManagedCertificateIssuerMode(ctx, agentID, host, scope)
	if err != nil {
		return err
	}
	next := ManagedCertificate{
		ID:              nextManagedCertificateID(*rows),
		Domain:          host,
		Enabled:         true,
		Scope:           scope,
		IssuerMode:      issuerMode,
		TargetAgentIDs:  []string{agentID},
		Status:          "pending",
		Tags:            normalizeTagUnion(rule.Tags, []string{"auto", managedCertificateAutoTargetTag(agentID)}),
		Usage:           "https",
		CertificateType: "acme",
	}
	if err := assertManagedCertificateTargetingAllowed(s.cfg, next); err != nil {
		return err
	}
	if err := assertManagedCertificateMutationAllowed(nil, next); err != nil {
		return err
	}
	next.Revision = allocateManagedCertificateRevision(nextRevision)
	*rows = append(*rows, managedCertificateToRow(next))
	return nil
}

func (s *ruleService) cleanupUnusedManagedCertificatesForAgent(
	agentID string,
	rules []HTTPRule,
	rows *[]storage.ManagedCertificateRow,
	nextRevision *int,
) error {
	for index := 0; index < len(*rows); {
		cert := managedCertificateFromRow((*rows)[index])
		if !containsString(cert.TargetAgentIDs, agentID) || isSystemRelayCACertificate(cert) || isAutoRelayListenerCertificate(cert, 0) {
			index++
			continue
		}
		if hasMatchingHTTPSRuleForCertificate(rules, cert) || !shouldRecycleManagedCertificateForAgent(cert, agentID) {
			index++
			continue
		}

		next := cert
		next.TargetAgentIDs = removeString(next.TargetAgentIDs, agentID)
		next.Tags = removeString(next.Tags, managedCertificateAutoTargetTag(agentID))
		if len(next.TargetAgentIDs) == 0 && isAutoManagedCertificate(next) {
			*rows = append(append([]storage.ManagedCertificateRow(nil), (*rows)[:index]...), (*rows)[index+1:]...)
			continue
		}
		if err := assertManagedCertificateMutationAllowed(&cert, next); err != nil {
			return err
		}
		next.Revision = allocateManagedCertificateRevision(nextRevision)
		(*rows)[index] = managedCertificateToRow(next)
		index++
	}
	return nil
}

func (s *ruleService) chooseAutoManagedCertificateIssuerMode(
	ctx context.Context,
	agentID string,
	host string,
	scope string,
) (string, error) {
	agentName, capabilities, err := s.resolveAgentCapabilities(ctx, agentID)
	if err != nil {
		return "", err
	}
	if !agentHasCapability(capabilities, "cert_install") {
		return "", fmt.Errorf("%w: agent does not support unified certificate install: %s", ErrInvalidArgument, agentName)
	}
	if scope == "ip" {
		if !agentHasCapability(capabilities, "local_acme") {
			return "", fmt.Errorf("%w: agent does not support local ACME issuance for IP HTTPS: %s", ErrInvalidArgument, agentName)
		}
		return "local_http01", nil
	}
	if s.cfg.ManagedDNSCertificatesEnabled {
		return "master_cf_dns", nil
	}
	if agentHasCapability(capabilities, "local_acme") {
		return "local_http01", nil
	}
	return "", fmt.Errorf("%w: no available unified certificate issuer for %s", ErrInvalidArgument, host)
}

func (s *ruleService) resolveAgentCapabilities(ctx context.Context, agentID string) (string, []string, error) {
	_, name, capabilities, err := resolveAgentCapabilitiesForStore(ctx, s.cfg, s.store, agentID)
	return name, capabilities, err
}

func httpRulesFromRows(rows []storage.HTTPRuleRow) []HTTPRule {
	rules := make([]HTTPRule, 0, len(rows))
	for _, row := range rows {
		rules = append(rules, httpRuleFromRow(row))
	}
	return rules
}

func parseRuleFrontendTarget(frontendURL string) (string, string, bool) {
	parsed, err := url.Parse(strings.TrimSpace(frontendURL))
	if err != nil || parsed == nil {
		return "", "", false
	}
	host := strings.ToLower(normalizeCertificateHost(parsed.Hostname()))
	scheme := strings.ToLower(strings.TrimSpace(parsed.Scheme))
	if host == "" || scheme == "" {
		return "", "", false
	}
	return scheme, host, true
}

func normalizeCertificateHost(value string) string {
	trimmed := strings.TrimSpace(value)
	if len(trimmed) >= 2 && strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
		return trimmed[1 : len(trimmed)-1]
	}
	return trimmed
}

func isIPAddress(host string) bool {
	return net.ParseIP(normalizeCertificateHost(host)) != nil
}

func findBestManagedCertificateForHost(rows []storage.ManagedCertificateRow, agentID string, host string, scope string) (ManagedCertificate, int, bool) {
	bestIndex := -1
	var best ManagedCertificate
	for index, row := range rows {
		cert := managedCertificateFromRow(row)
		if !cert.Enabled || cert.Scope != scope {
			continue
		}
		if !doesManagedCertificateMatchHost(cert, host) {
			continue
		}
		if bestIndex < 0 || compareManagedCertificateMatchPriority(cert, best, agentID) < 0 {
			best = cert
			bestIndex = index
		}
	}
	if bestIndex < 0 {
		return ManagedCertificate{}, -1, false
	}
	return best, bestIndex, true
}

func compareManagedCertificateMatchPriority(left ManagedCertificate, right ManagedCertificate, agentID string) int {
	leftWildcard := isWildcardCertificateDomain(left.Domain)
	rightWildcard := isWildcardCertificateDomain(right.Domain)
	if leftWildcard != rightWildcard {
		if leftWildcard {
			return 1
		}
		return -1
	}

	leftTargetsAgent := containsString(left.TargetAgentIDs, agentID)
	rightTargetsAgent := containsString(right.TargetAgentIDs, agentID)
	if leftTargetsAgent != rightTargetsAgent {
		if leftTargetsAgent {
			return -1
		}
		return 1
	}

	return right.Revision - left.Revision
}

func doesManagedCertificateMatchHost(cert ManagedCertificate, host string) bool {
	if cert.Scope == "ip" {
		return isExactManagedCertificateMatch(cert.Domain, host)
	}
	return isExactManagedCertificateMatch(cert.Domain, host) || isWildcardManagedCertificateMatch(cert.Domain, host)
}

func isExactManagedCertificateMatch(certDomain string, host string) bool {
	return strings.EqualFold(normalizeCertificateHost(certDomain), normalizeCertificateHost(host))
}

func isWildcardManagedCertificateMatch(certDomain string, host string) bool {
	pattern := strings.ToLower(normalizeCertificateHost(certDomain))
	target := strings.ToLower(normalizeCertificateHost(host))
	if !isWildcardCertificateDomain(pattern) {
		return false
	}
	suffix := strings.TrimPrefix(pattern, "*.")
	if !strings.HasSuffix(target, "."+suffix) {
		return false
	}
	targetParts := strings.Split(target, ".")
	suffixParts := strings.Split(suffix, ".")
	return len(targetParts) == len(suffixParts)+1
}

func isWildcardCertificateDomain(value string) bool {
	normalized := normalizeCertificateHost(value)
	if !strings.HasPrefix(normalized, "*.") {
		return false
	}
	return len(normalized) > 2
}

func shouldRecycleManagedCertificateForAgent(cert ManagedCertificate, agentID string) bool {
	return isAutoManagedCertificate(cert) || hasManagedCertificateAutoTarget(cert, agentID)
}

func isAutoManagedCertificate(cert ManagedCertificate) bool {
	return containsString(cert.Tags, "auto")
}

func hasManagedCertificateAutoTarget(cert ManagedCertificate, agentID string) bool {
	return containsString(cert.Tags, managedCertificateAutoTargetTag(agentID))
}

func managedCertificateAutoTargetTag(agentID string) string {
	return fmt.Sprintf("auto_target:%s", strings.TrimSpace(agentID))
}

func hasMatchingHTTPSRuleForCertificate(rules []HTTPRule, cert ManagedCertificate) bool {
	for _, rule := range rules {
		if !rule.Enabled {
			continue
		}
		scheme, host, ok := parseRuleFrontendTarget(rule.FrontendURL)
		if !ok || scheme != "https" {
			continue
		}
		if doesManagedCertificateMatchHost(cert, host) {
			return true
		}
	}
	return false
}

func nextManagedCertificateRevision(rows []storage.ManagedCertificateRow) int {
	maxRevision := 0
	for _, row := range rows {
		if row.Revision > maxRevision {
			maxRevision = row.Revision
		}
	}
	return maxRevision + 1
}

func allocateManagedCertificateRevision(nextRevision *int) int {
	revision := *nextRevision
	*nextRevision = *nextRevision + 1
	return revision
}

func nextManagedCertificateID(rows []storage.ManagedCertificateRow) int {
	maxID := 0
	for _, row := range rows {
		if row.ID > maxID {
			maxID = row.ID
		}
	}
	return maxID + 1
}

func managedCertificateRowsEqual(left []storage.ManagedCertificateRow, right []storage.ManagedCertificateRow) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func appendUniqueNormalized(values []string, extra ...string) []string {
	return normalizeTagUnion(values, extra)
}

func normalizeTagUnion(groups ...[]string) []string {
	normalized := make([]string, 0)
	seen := map[string]struct{}{}
	for _, group := range groups {
		for _, raw := range group {
			value := strings.TrimSpace(raw)
			if value == "" {
				continue
			}
			if _, ok := seen[value]; ok {
				continue
			}
			seen[value] = struct{}{}
			normalized = append(normalized, value)
		}
	}
	return normalized
}

func agentHasCapability(capabilities []string, capability string) bool {
	for _, existing := range capabilities {
		if strings.TrimSpace(existing) == capability {
			return true
		}
	}
	return false
}

type agentCapabilityStore interface {
	ListAgents(context.Context) ([]storage.AgentRow, error)
}

type egressProfileCapabilityStore interface {
	agentCapabilityStore
	relayChainLookupStore
}

func resolveAgentCapabilitiesForStore(ctx context.Context, cfg config.Config, store agentCapabilityStore, agentID string) (string, string, []string, error) {
	resolvedID := strings.TrimSpace(agentID)
	if resolvedID == "" {
		resolvedID = cfg.LocalAgentID
	}
	if cfg.EnableLocalAgent && resolvedID == cfg.LocalAgentID {
		return resolvedID, cfg.LocalAgentID, append([]string(nil), defaultLocalCapabilities...), nil
	}
	rows, err := store.ListAgents(ctx)
	if err != nil {
		return "", "", nil, err
	}
	for _, row := range rows {
		if row.ID != resolvedID {
			continue
		}
		name := strings.TrimSpace(row.Name)
		if name == "" {
			name = row.ID
		}
		return resolvedID, name, parseStringArray(row.CapabilitiesJSON), nil
	}
	return "", "", nil, ErrAgentNotFound
}

func ensureAgentSupportsWireGuardCapability(ctx context.Context, cfg config.Config, store agentCapabilityStore, agentID string) error {
	_, name, capabilities, err := resolveAgentCapabilitiesForStore(ctx, cfg, store, agentID)
	if err != nil {
		return err
	}
	if !agentHasCapability(capabilities, "wireguard") {
		return fmt.Errorf("%w: agent does not support WireGuard: %s", ErrInvalidArgument, name)
	}
	return nil
}

func ensureAgentSupportsEgressProfilesCapability(ctx context.Context, cfg config.Config, store agentCapabilityStore, agentID string) error {
	_, name, capabilities, err := resolveAgentCapabilitiesForStore(ctx, cfg, store, agentID)
	if err != nil {
		return err
	}
	if !agentHasCapability(capabilities, "egress_profiles") {
		return fmt.Errorf("%w: agent does not support egress profiles: %s", ErrInvalidArgument, name)
	}
	return nil
}

func ensureEgressProfileExecutorsSupportCapability(ctx context.Context, cfg config.Config, store egressProfileCapabilityStore, ruleAgentID string, relayLayers [][]int) error {
	executors, err := egressProfileExecutorAgentIDsForRule(ctx, store, ruleAgentID, relayLayers)
	if err != nil {
		return err
	}
	for _, agentID := range executors {
		if err := ensureAgentSupportsEgressProfilesCapability(ctx, cfg, store, agentID); err != nil {
			return err
		}
	}
	return nil
}

type egressProfileScheduleStore interface {
	egressProfileLookupStore
	relayChainLookupStore
}

func egressProfileScheduleTargets(ctx context.Context, store egressProfileScheduleStore, ruleAgentID string, relayLayers [][]int, egressProfileID *int, ruleRevision int) ([]string, int, error) {
	executors, err := egressProfileExecutorAgentIDsForMutation(ctx, store, ruleAgentID, relayLayers, egressProfileID)
	if err != nil {
		return nil, 0, err
	}
	if len(executors) == 0 {
		return nil, ruleRevision, nil
	}
	profile, err := getEnabledEgressProfile(ctx, store, *egressProfileID)
	if err != nil {
		return nil, 0, err
	}
	return executors, maxInt(ruleRevision, profile.Revision), nil
}

func egressProfileExecutorAgentIDsForMutation(ctx context.Context, store relayChainLookupStore, ruleAgentID string, relayLayers [][]int, egressProfileID *int) ([]string, error) {
	if egressProfileID == nil || *egressProfileID <= 0 {
		return nil, nil
	}
	executors, err := egressProfileExecutorAgentIDsForRule(ctx, store, ruleAgentID, relayLayers)
	if err != nil {
		return nil, err
	}
	return agentIDsExcept(executors, ruleAgentID), nil
}

func agentIDsExcept(agentIDs []string, excluded string) []string {
	excluded = strings.TrimSpace(excluded)
	out := make([]string, 0, len(agentIDs))
	seen := map[string]struct{}{}
	for _, agentID := range agentIDs {
		agentID = strings.TrimSpace(agentID)
		if agentID == "" || agentID == excluded {
			continue
		}
		if _, ok := seen[agentID]; ok {
			continue
		}
		seen[agentID] = struct{}{}
		out = append(out, agentID)
	}
	sort.Strings(out)
	return out
}

func uniqueAgentIDs(agentIDs []string) []string {
	out := make([]string, 0, len(agentIDs))
	seen := map[string]struct{}{}
	for _, agentID := range agentIDs {
		agentID = strings.TrimSpace(agentID)
		if agentID == "" {
			continue
		}
		if _, ok := seen[agentID]; ok {
			continue
		}
		seen[agentID] = struct{}{}
		out = append(out, agentID)
	}
	sort.Strings(out)
	return out
}

func egressProfileExecutorAgentIDsForRule(ctx context.Context, store relayChainLookupStore, ruleAgentID string, relayLayers [][]int) ([]string, error) {
	ruleAgentID = strings.TrimSpace(ruleAgentID)
	if len(relayLayers) == 0 {
		if ruleAgentID == "" {
			return nil, nil
		}
		return []string{ruleAgentID}, nil
	}
	listeners, err := store.ListRelayListeners(ctx, "")
	if err != nil {
		return nil, err
	}
	finalHops := egressProfileFinalHopAgentIDs(relayLayers, listeners)
	agentIDs := make([]string, 0, len(finalHops))
	for agentID := range finalHops {
		agentIDs = append(agentIDs, agentID)
	}
	sort.Strings(agentIDs)
	return agentIDs, nil
}

func httpRuleInputEnablesWireGuard(input HTTPRuleInput, fallback HTTPRule) bool {
	enabled := false
	if fallback.ID > 0 {
		enabled = fallback.WireGuardEntryEnabled
	}
	if input.WireGuardEntryEnabled != nil {
		enabled = *input.WireGuardEntryEnabled
	}
	return enabled
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
	backendURL := ""

	loadBalancing := fallback.LoadBalancing
	if strings.TrimSpace(loadBalancing.Strategy) == "" {
		loadBalancing = HTTPLoadBalancing{Strategy: "adaptive"}
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

	relayChain := []int{}
	relayLayers := cloneIntLayers(fallback.RelayLayers)
	if input.RelayLayers != nil {
		relayLayers, err = normalizeRelayLayersInput(*input.RelayLayers, "tcp")
		if err != nil {
			return HTTPRule{}, err
		}
	} else if input.RelayChain != nil {
		return HTTPRule{}, fmt.Errorf("%w: relay_chain is legacy; use relay_layers", ErrInvalidArgument)
	}
	if err := s.validateRelayChain(ctx, fallback.AgentID, flattenRelayLayers(relayLayers)); err != nil {
		return HTTPRule{}, err
	}

	relayObfs := false
	if fallback.ID > 0 {
		relayObfs = fallback.RelayObfs
	}
	if input.RelayObfs != nil {
		relayObfs = *input.RelayObfs
	}
	if relayObfs && len(relayChain) == 0 && len(relayLayers) == 0 {
		relayObfs = false
	}

	passProxyHeaders := defaultPassProxyHeaders()
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

	egressProfileID, err := normalizeEgressProfileIDInput(input.EgressProfileID, fallback.EgressProfileID)
	if err != nil {
		return HTTPRule{}, err
	}
	if egressProfileID != nil {
		profile, err := s.getEnabledEgressProfile(ctx, *egressProfileID)
		if err != nil {
			return HTTPRule{}, err
		}
		if !egressProfileSupportsHTTP(profile) {
			return HTTPRule{}, fmt.Errorf("%w: egress profile %d does not support HTTP rules", ErrInvalidArgument, profile.ID)
		}
		if err := ensureEgressProfileExecutorsSupportCapability(ctx, s.cfg, s.store, fallback.AgentID, relayLayers); err != nil {
			return HTTPRule{}, err
		}
	}

	wireGuardEntryEnabled := false
	if fallback.ID > 0 {
		wireGuardEntryEnabled = fallback.WireGuardEntryEnabled
	}
	if input.WireGuardEntryEnabled != nil {
		wireGuardEntryEnabled = *input.WireGuardEntryEnabled
	}
	var wireGuardProfileID *int
	wireGuardEntryListenHost := ""
	wireGuardEntryListenPort := 0
	if wireGuardEntryEnabled {
		wireGuardProfileID = copyOptionalInt(fallback.WireGuardProfileID)
		if input.WireGuardProfileID != nil {
			wireGuardProfileID = copyOptionalInt(input.WireGuardProfileID)
		}
		if input.WireGuardProfileID != nil && *input.WireGuardProfileID <= 0 {
			return HTTPRule{}, fmt.Errorf("%w: wireguard_profile_id is required when wireguard entry is enabled", ErrInvalidArgument)
		}
		if wireGuardProfileID == nil {
			profile, err := s.ensureDefaultHTTPWireGuardProfile(ctx, fallback.AgentID)
			if err != nil {
				return HTTPRule{}, err
			}
			wireGuardProfileID = &profile.ID
		}
		if wireGuardProfileID == nil || *wireGuardProfileID <= 0 {
			return HTTPRule{}, fmt.Errorf("%w: wireguard_profile_id is required when wireguard entry is enabled", ErrInvalidArgument)
		}
		if err := s.validateHTTPWireGuardProfileReference(ctx, fallback.AgentID, wireGuardProfileID); err != nil {
			return HTTPRule{}, err
		}
		wireGuardEntryListenHost = strings.TrimSpace(fallback.WireGuardEntryListenHost)
		if input.WireGuardEntryListenHost != nil {
			wireGuardEntryListenHost = strings.TrimSpace(*input.WireGuardEntryListenHost)
		}
		if wireGuardEntryListenHost == "" {
			host, err := s.defaultHTTPWireGuardEntryListenHost(ctx, fallback.AgentID, wireGuardProfileID)
			if err != nil {
				return HTTPRule{}, err
			}
			wireGuardEntryListenHost = host
		}
		var err error
		wireGuardEntryListenPort, err = httpRuleFrontendListenPort(frontendURL)
		if err != nil {
			return HTTPRule{}, fmt.Errorf("%w: frontend_url must contain a valid http/https port", ErrInvalidArgument)
		}
	}

	return HTTPRule{
		ID:                       id,
		AgentID:                  fallback.AgentID,
		FrontendURL:              frontendURL,
		BackendURL:               backendURL,
		Backends:                 backends,
		LoadBalancing:            loadBalancing,
		Enabled:                  enabled,
		Tags:                     tags,
		ProxyRedirect:            proxyRedirect,
		RelayChain:               relayChain,
		RelayLayers:              relayLayers,
		RelayObfs:                relayObfs,
		PassProxyHeaders:         passProxyHeaders,
		UserAgent:                userAgent,
		CustomHeaders:            customHeaders,
		EgressProfileID:          egressProfileID,
		WireGuardEntryEnabled:    wireGuardEntryEnabled,
		WireGuardProfileID:       wireGuardProfileID,
		WireGuardEntryListenHost: wireGuardEntryListenHost,
		WireGuardEntryListenPort: wireGuardEntryListenPort,
		Revision:                 fallback.Revision,
	}, nil
}

func (s *ruleService) getEnabledEgressProfile(ctx context.Context, id int) (EgressProfile, error) {
	return getEnabledEgressProfile(ctx, s.store, id)
}

func (s *ruleService) ensureDefaultHTTPWireGuardProfile(ctx context.Context, agentID string) (WireGuardProfile, error) {
	profile, _, err := s.ensureDefaultHTTPWireGuardProfileWithRollback(ctx, agentID)
	return profile, err
}

func (s *ruleService) ensureDefaultHTTPWireGuardProfileWithRollback(ctx context.Context, agentID string) (WireGuardProfile, *wireGuardProfileRollback, error) {
	store, ok := s.store.(wireGuardProfileStore)
	if !ok {
		return WireGuardProfile{}, nil, fmt.Errorf("%w: wireguard default profile store is unavailable", ErrInvalidArgument)
	}
	return ensureDefaultWireGuardProfileWithRollback(ctx, s.cfg, store, agentID)
}

func (s *ruleService) validateRelayChain(ctx context.Context, agentID string, relayChain []int) error {
	knownAgentIDs, err := s.allKnownAgentIDs(ctx)
	if err != nil {
		return err
	}
	return validateRelayChainReferences(ctx, s.store, knownAgentIDs, relayChain, relayChainValidationOptions{
		RuleAgentID: agentID,
	})
}

func (s *ruleService) validateHTTPWireGuardProfileReference(ctx context.Context, agentID string, profileID *int) error {
	if profileID == nil || *profileID <= 0 {
		return nil
	}
	rows, err := s.store.ListWireGuardProfiles(ctx, agentID)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if row.ID != *profileID {
			continue
		}
		if !row.Enabled {
			return fmt.Errorf("%w: wireguard profile %d is disabled", ErrInvalidArgument, *profileID)
		}
		return nil
	}
	return fmt.Errorf("%w: wireguard profile %d not found for agent %s", ErrInvalidArgument, *profileID, agentID)
}

func (s *ruleService) defaultHTTPWireGuardEntryListenHost(ctx context.Context, agentID string, profileID *int) (string, error) {
	if profileID == nil || *profileID <= 0 {
		return "", nil
	}
	rows, err := s.store.ListWireGuardProfiles(ctx, agentID)
	if err != nil {
		return "", err
	}
	for _, row := range rows {
		if row.ID != *profileID {
			continue
		}
		return firstWireGuardProfileAddressHost(row.AddressesJSON), nil
	}
	return "", nil
}

func normalizeHTTPBackendsInput(input HTTPRuleInput, fallback HTTPRule) ([]HTTPRuleBackend, error) {
	if input.Backends != nil {
		backends := normalizeHTTPBackends(*input.Backends)
		if len(backends) > 0 {
			return backends, nil
		}
		return nil, fmt.Errorf("%w: backends must contain at least one valid http/https URL", ErrInvalidArgument)
	}

	if input.BackendURL != nil {
		return nil, fmt.Errorf("%w: backend_url is legacy; use backends[].url", ErrInvalidArgument)
	}

	backends := normalizeHTTPBackends(fallback.Backends)
	if len(backends) > 0 {
		return backends, nil
	}
	return nil, fmt.Errorf("%w: backends must contain at least one valid http/https URL", ErrInvalidArgument)
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
	switch strings.ToLower(strings.TrimSpace(value.Strategy)) {
	case "round_robin":
		return HTTPLoadBalancing{Strategy: "round_robin"}
	case "random":
		return HTTPLoadBalancing{Strategy: "random"}
	case "adaptive":
		return HTTPLoadBalancing{Strategy: "adaptive"}
	default:
		return HTTPLoadBalancing{Strategy: "adaptive"}
	}
}

func defaultPassProxyHeaders() bool {
	v := strings.TrimSpace(os.Getenv("PROXY_PASS_PROXY_HEADERS"))
	if v == "" {
		return true
	}
	switch strings.ToLower(v) {
	case "0", "false", "no", "off":
		return false
	default:
		return true
	}
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

func httpRuleFrontendListenPort(raw string) (int, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed == nil {
		return 0, err
	}
	if portText := parsed.Port(); portText != "" {
		port, err := strconv.Atoi(portText)
		if err != nil || port < 1 || port > 65535 {
			return 0, err
		}
		return port, nil
	}
	switch strings.ToLower(parsed.Scheme) {
	case "https":
		return 443, nil
	case "http":
		return 80, nil
	default:
		return 0, fmt.Errorf("unsupported scheme %q", parsed.Scheme)
	}
}

func httpRuleFromRow(row storage.HTTPRuleRow) HTTPRule {
	backends := parseBackends(row.BackendsJSON)
	wireGuardEntryListenPort := row.WireGuardEntryListenPort
	if row.WireGuardEntryEnabled {
		if port, err := httpRuleFrontendListenPort(row.FrontendURL); err == nil {
			wireGuardEntryListenPort = port
		}
	}

	return HTTPRule{
		ID:                       row.ID,
		AgentID:                  row.AgentID,
		FrontendURL:              row.FrontendURL,
		BackendURL:               "",
		Backends:                 backends,
		LoadBalancing:            parseLoadBalancing(row.LoadBalancingJSON),
		Enabled:                  row.Enabled,
		Tags:                     parseStringArray(row.TagsJSON),
		ProxyRedirect:            row.ProxyRedirect,
		RelayChain:               []int{},
		RelayLayers:              parseIntLayers(row.RelayLayersJSON),
		RelayObfs:                row.RelayObfs,
		PassProxyHeaders:         row.PassProxyHeaders,
		UserAgent:                row.UserAgent,
		CustomHeaders:            parseCustomHeaders(row.CustomHeadersJSON),
		EgressProfileID:          normalizeOptionalPositiveInt(row.EgressProfileID),
		WireGuardEntryEnabled:    row.WireGuardEntryEnabled,
		WireGuardProfileID:       copyOptionalInt(row.WireGuardProfileID),
		WireGuardEntryListenHost: row.WireGuardEntryListenHost,
		WireGuardEntryListenPort: wireGuardEntryListenPort,
		Revision:                 row.Revision,
	}
}

func httpRuleToRow(rule HTTPRule) storage.HTTPRuleRow {
	return storage.HTTPRuleRow{
		ID:                       rule.ID,
		AgentID:                  rule.AgentID,
		FrontendURL:              rule.FrontendURL,
		BackendURL:               "",
		BackendsJSON:             marshalJSON(rule.Backends, "[]"),
		LoadBalancingJSON:        marshalJSON(rule.LoadBalancing, `{"strategy":"adaptive"}`),
		Enabled:                  rule.Enabled,
		TagsJSON:                 marshalJSON(rule.Tags, "[]"),
		ProxyRedirect:            rule.ProxyRedirect,
		RelayChainJSON:           "[]",
		RelayLayersJSON:          marshalJSON(rule.RelayLayers, "[]"),
		RelayObfs:                rule.RelayObfs,
		PassProxyHeaders:         rule.PassProxyHeaders,
		UserAgent:                rule.UserAgent,
		CustomHeadersJSON:        marshalJSON(rule.CustomHeaders, "[]"),
		EgressProfileID:          normalizeOptionalPositiveInt(rule.EgressProfileID),
		WireGuardEntryEnabled:    rule.WireGuardEntryEnabled,
		WireGuardProfileID:       copyOptionalInt(rule.WireGuardProfileID),
		WireGuardEntryListenHost: rule.WireGuardEntryListenHost,
		WireGuardEntryListenPort: rule.WireGuardEntryListenPort,
		Revision:                 rule.Revision,
	}
}

func maxInt(values ...int) int {
	maxValue := 0
	for _, value := range values {
		if value > maxValue {
			maxValue = value
		}
	}
	return maxValue
}

func validateUniqueHTTPFrontendBinding(rows []storage.HTTPRuleRow) error {
	seen := make(map[string]int, len(rows))
	for _, row := range rows {
		binding, ok := frontendBindingIdentity(httpRuleFromRow(row))
		if !ok {
			continue
		}
		if existingID, exists := seen[binding]; exists && existingID != row.ID {
			return fmt.Errorf("%w: frontend_url conflicts with existing rule: %d", ErrInvalidArgument, existingID)
		}
		seen[binding] = row.ID
	}
	return nil
}

func validateUniqueHTTPWireGuardEntryRoutes(rows []storage.HTTPRuleRow) error {
	seen := make(map[string]int, len(rows))
	for _, row := range rows {
		key, ok := httpWireGuardEntryRouteKey(httpRuleFromRow(row))
		if !ok {
			continue
		}
		if existingID, exists := seen[key]; exists && existingID != row.ID {
			return fmt.Errorf("%w: wireguard entry route conflicts with existing rule: %d", ErrInvalidArgument, existingID)
		}
		seen[key] = row.ID
	}
	return nil
}

func frontendBindingIdentity(rule HTTPRule) (string, bool) {
	parsed, err := url.Parse(strings.TrimSpace(rule.FrontendURL))
	if err != nil || parsed == nil {
		return "", false
	}
	scheme := strings.ToLower(strings.TrimSpace(parsed.Scheme))
	host := strings.ToLower(strings.TrimSpace(parsed.Hostname()))
	if scheme == "" || host == "" {
		return "", false
	}
	port := parsed.Port()
	if port == "" {
		switch scheme {
		case "https":
			port = "443"
		case "http":
			port = "80"
		default:
			return "", false
		}
	}
	return scheme + "://" + host + ":" + port + normalizeRuleFrontendPath(parsed.Path), true
}

func httpWireGuardEntryRouteKey(rule HTTPRule) (string, bool) {
	if !rule.Enabled || !rule.WireGuardEntryEnabled || rule.WireGuardProfileID == nil || *rule.WireGuardProfileID <= 0 {
		return "", false
	}
	listenHost := strings.TrimSpace(rule.WireGuardEntryListenHost)
	listenPort, err := httpRuleFrontendListenPort(rule.FrontendURL)
	if err != nil && rule.WireGuardEntryListenPort >= 1 && rule.WireGuardEntryListenPort <= 65535 {
		listenPort = rule.WireGuardEntryListenPort
	}
	if listenHost == "" || listenPort < 1 || listenPort > 65535 {
		return "", false
	}
	parsed, err := url.Parse(strings.TrimSpace(rule.FrontendURL))
	if err != nil || parsed == nil {
		return "", false
	}
	return fmt.Sprintf(
		"%s|%d|%s|%d|%s",
		strings.TrimSpace(rule.AgentID),
		*rule.WireGuardProfileID,
		strings.ToLower(listenHost),
		listenPort,
		normalizeRuleFrontendPath(parsed.Path),
	), true
}

func normalizeRuleFrontendPath(raw string) string {
	if strings.TrimSpace(raw) == "" {
		return "/"
	}
	cleaned := path.Clean(raw)
	if !strings.HasPrefix(cleaned, "/") {
		cleaned = "/" + cleaned
	}
	return cleaned
}
