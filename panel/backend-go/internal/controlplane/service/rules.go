package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"os"
	"path"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/revision"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

func newConfigMutationExecutor(cfg config.Config, store any) *revision.Executor {
	revisionStore, ok := store.(revision.Store)
	if !ok {
		return nil
	}
	return newMutationExecutor(cfg, revisionStore)
}

var errRevisionMutationStoreRequired = errors.New("revision mutation store is required")

// legacyConfigMutationStore is an in-package opt-in for narrow unit-test stores.
// Runtime stores must support revision mutations instead of silently falling back.
type legacyConfigMutationStore interface {
	allowLegacyConfigMutationFallback()
}

func requireConfigMutationStore(store any, executor *revision.Executor, revisionMutation bool) error {
	if executor != nil || revisionMutation {
		return nil
	}
	if _, ok := store.(legacyConfigMutationStore); ok {
		return nil
	}
	return errRevisionMutationStoreRequired
}

func configMutationTargets(cfg config.Config, agentIDs []string, intentEgressProfileIDs []int) []revision.Target {
	agentIDs = uniqueAgentIDs(agentIDs)
	targets := make([]revision.Target, 0, len(agentIDs))
	for _, agentID := range agentIDs {
		local := cfg.EnableLocalAgent && agentID == cfg.LocalAgentID
		target := revisionTimeoutTarget(cfg, agentID)
		target.Local = local
		if local {
			target.Capabilities = append([]string(nil), defaultLocalCapabilities...)
		}
		if len(intentEgressProfileIDs) > 0 {
			target.IntentResources.EgressProfileIDs = append([]int(nil), intentEgressProfileIDs...)
		}
		targets = append(targets, target)
	}
	return targets
}

func revisionTimeoutTarget(cfg config.Config, agentID string) revision.Target {
	agentID = strings.TrimSpace(agentID)
	applyTimeout := cfg.RevisionCoordinator.ApplyTimeout
	drainTimeout := cfg.RevisionCoordinator.DrainTimeout
	if override, ok := cfg.RevisionCoordinator.AgentTimeoutOverrides[agentID]; ok {
		if override.ApplyTimeout > 0 {
			applyTimeout = override.ApplyTimeout
		}
		if override.DrainTimeout > 0 {
			drainTimeout = override.DrainTimeout
		}
	}
	return revision.Target{
		AgentID:             agentID,
		ApplyTimeoutSeconds: revisionTimeoutSeconds(applyTimeout),
		DrainTimeoutSeconds: revisionTimeoutSeconds(drainTimeout),
	}
}

func revisionTimeoutSeconds(timeout time.Duration) int {
	if timeout <= 0 {
		return 0
	}
	seconds := timeout / time.Second
	if timeout%time.Second != 0 {
		seconds++
	}
	maxInt := int(^uint(0) >> 1)
	if seconds > time.Duration(maxInt) {
		return maxInt
	}
	return int(seconds)
}

func configMutationRevision(revisions map[string]int64, agentID string, fallback int) int {
	if revisionNumber := revisions[strings.TrimSpace(agentID)]; revisionNumber > 0 {
		return int(revisionNumber)
	}
	return fallback
}

func maxConfigMutationRevision(revisions map[string]int64, fallback int) int {
	result := fallback
	for _, revisionNumber := range revisions {
		if int(revisionNumber) > result {
			result = int(revisionNumber)
		}
	}
	return result
}

type configDependencyStore interface {
	ListAgents(context.Context) ([]storage.AgentRow, error)
	ListHTTPRules(context.Context, string) ([]storage.HTTPRuleRow, error)
	ListL4Rules(context.Context, string) ([]storage.L4RuleRow, error)
	ListRelayListeners(context.Context, string) ([]storage.RelayListenerRow, error)
}

func expandConfigDependencyAgentIDs(ctx context.Context, cfg config.Config, store configDependencyStore, seeds []string) ([]string, error) {
	listeners, err := store.ListRelayListeners(ctx, "")
	if err != nil {
		return nil, err
	}
	listenerAgentIDs := make(map[int]string, len(listeners))
	for _, listener := range listeners {
		if listener.ID > 0 {
			listenerAgentIDs[listener.ID] = strings.TrimSpace(listener.AgentID)
		}
	}

	agentIDs, err := allKnownAgentIDs(ctx, cfg, store)
	if err != nil {
		return nil, err
	}
	dependencies := make(map[string]map[string]struct{})
	connect := func(left, right string) {
		left = strings.TrimSpace(left)
		right = strings.TrimSpace(right)
		if left == "" || right == "" || left == right {
			return
		}
		if dependencies[left] == nil {
			dependencies[left] = make(map[string]struct{})
		}
		if dependencies[right] == nil {
			dependencies[right] = make(map[string]struct{})
		}
		dependencies[left][right] = struct{}{}
		dependencies[right][left] = struct{}{}
	}
	addLayers := func(agentID, layersJSON string) {
		for _, listenerID := range flattenRelayLayers(parseIntLayers(layersJSON)) {
			connect(agentID, listenerAgentIDs[listenerID])
		}
	}
	for _, agentID := range agentIDs {
		httpRules, err := store.ListHTTPRules(ctx, agentID)
		if err != nil {
			return nil, err
		}
		for _, row := range httpRules {
			if row.Enabled {
				addLayers(agentID, row.RelayLayersJSON)
			}
		}
		l4Rules, err := store.ListL4Rules(ctx, agentID)
		if err != nil {
			return nil, err
		}
		for _, row := range l4Rules {
			if row.Enabled {
				addLayers(agentID, row.RelayLayersJSON)
			}
		}
	}

	seen := make(map[string]struct{})
	queue := uniqueAgentIDs(seeds)
	for len(queue) > 0 {
		agentID := queue[0]
		queue = queue[1:]
		if _, ok := seen[agentID]; ok {
			continue
		}
		seen[agentID] = struct{}{}
		for dependencyAgentID := range dependencies[agentID] {
			if _, ok := seen[dependencyAgentID]; !ok {
				queue = append(queue, dependencyAgentID)
			}
		}
	}
	result := make([]string, 0, len(seen))
	for agentID := range seen {
		result = append(result, agentID)
	}
	return uniqueAgentIDs(result), nil
}

func runConfigPostCommitActions(actions []func()) {
	for _, action := range actions {
		if action != nil {
			action()
		}
	}
}

type HTTPRuleInput struct {
	ID                 *int                `json:"id,omitempty"`
	FrontendURL        *string             `json:"frontend_url,omitempty"`
	BackendURL         *string             `json:"backend_url,omitempty"`
	Backends           *[]HTTPRuleBackend  `json:"backends,omitempty"`
	LoadBalancing      *HTTPLoadBalancing  `json:"load_balancing,omitempty"`
	Enabled            *bool               `json:"enabled,omitempty"`
	Tags               *[]string           `json:"tags,omitempty"`
	ProxyRedirect      *bool               `json:"proxy_redirect,omitempty"`
	RelayChain         *[]int              `json:"relay_chain,omitempty"`
	RelayLayers        *[][]int            `json:"relay_layers,omitempty"`
	RelayObfs          *bool               `json:"relay_obfs,omitempty"`
	PassProxyHeaders   *bool               `json:"pass_proxy_headers,omitempty"`
	UserAgent          *string             `json:"user_agent,omitempty"`
	CustomHeaders      *[]HTTPCustomHeader `json:"custom_headers,omitempty"`
	EgressProfileID    *int                `json:"egress_profile_id,omitempty"`
	TrustedProxyRanges *[]string           `json:"trusted_proxy_ranges,omitempty"`
	PolicyRef          *storage.PolicyRef  `json:"policy_ref,omitempty"`
}

type ruleStore interface {
	ListAgents(context.Context) ([]storage.AgentRow, error)
	ListHTTPRules(context.Context, string) ([]storage.HTTPRuleRow, error)
	GetHTTPRule(context.Context, string, int) (storage.HTTPRuleRow, bool, error)
	ListL4Rules(context.Context, string) ([]storage.L4RuleRow, error)
	ListEgressProfiles(context.Context) ([]storage.EgressProfileRow, error)
	LoadLocalAgentState(context.Context) (storage.LocalAgentStateRow, error)
	ListManagedCertificates(context.Context) ([]storage.ManagedCertificateRow, error)
	ListRelayListeners(context.Context, string) ([]storage.RelayListenerRow, error)
	SaveAgent(context.Context, storage.AgentRow) error
	SaveHTTPRules(context.Context, string, []storage.HTTPRuleRow) error
	SaveManagedCertificates(context.Context, []storage.ManagedCertificateRow) error
	CleanupManagedCertificateMaterial(context.Context, []storage.ManagedCertificateRow, []storage.ManagedCertificateRow) error
}

type resourceQuotaStore interface {
	ConsumeQuotaForResource(context.Context, string, string, string, string, string, int64) (storage.QuotaDecision, error)
}

func consumeResourceQuota(ctx context.Context, store any, resourceKind, resourceID, ownerKind, ownerID, metric string, delta int64) error {
	quotaStore, ok := store.(resourceQuotaStore)
	if !ok {
		if allowsTestUngovernedMutation(store) {
			return nil
		}
		return ErrMutationPrincipalRequired
	}
	if _, found := storage.QuotaActorFromContext(ctx); !found || !hasResourceAuthorizer(ctx) {
		return ErrMutationPrincipalRequired
	}
	_, err := quotaStore.ConsumeQuotaForResource(ctx, resourceKind, resourceID, ownerKind, ownerID, metric, delta)
	return err
}

type ruleService struct {
	cfg                    config.Config
	store                  ruleStore
	localApplyTrigger      func(context.Context) error
	mutationExecutor       *revision.Executor
	revisionMutation       bool
	revisionNumbers        map[string]int64
	postCommitActions      *[]func()
	pluginPublishAdmission bool
	dnsTokenProviderReady  func() bool
}

func NewRuleService(cfg config.Config, store ruleStore) *ruleService {
	return &ruleService{cfg: cfg, store: store, mutationExecutor: newConfigMutationExecutor(cfg, store)}
}

func (s *ruleService) SetLocalApplyTrigger(trigger func(context.Context) error) {
	s.localApplyTrigger = wrapLocalApplyTrigger(trigger)
}

func (s *ruleService) SetDNSTokenProviderReady(ready func() bool) {
	s.dnsTokenProviderReady = ready
}

func (s *ruleService) triggerLocalApply(ctx context.Context, agentID string) error {
	if s.revisionMutation {
		return nil
	}
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

func (s *ruleService) ListPage(ctx context.Context, query ListQuery) ([]HTTPRule, PageMeta, error) {
	query = NormalizeListQuery(query)
	names, err := agentDisplayNameMap(ctx, s.cfg, s.store)
	if err != nil {
		return nil, PageMeta{}, err
	}

	var rows []storage.HTTPRuleRow
	if query.AgentID != "" {
		resolvedID, err := s.ensureAgentExists(ctx, query.AgentID)
		if err != nil {
			return nil, PageMeta{}, err
		}
		rows, err = s.store.ListHTTPRules(ctx, resolvedID)
		if err != nil {
			return nil, PageMeta{}, err
		}
	} else {
		rows, err = s.listRulesAcrossAllAgents(ctx)
		if err != nil {
			return nil, PageMeta{}, err
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
	certDomain := ""
	certDomainFound := false
	if query.CertificateID != nil {
		domain, found, certErr := certificateDomainByID(ctx, s.store, *query.CertificateID)
		if certErr != nil {
			return nil, PageMeta{}, certErr
		}
		certDomain, certDomainFound = domain, found
	}

	filtered := make([]HTTPRule, 0, len(rows))
	for _, row := range rows {
		rule := httpRuleFromRow(row)
		if strings.TrimSpace(rule.AgentID) == "" {
			rule.AgentID = row.AgentID
		}
		rule.AgentName = resolveAgentDisplayName(names, rule.AgentID)
		searchFields := []string{rule.FrontendURL, rule.AgentID, rule.AgentName, strings.Join(rule.Tags, " ")}
		for _, backend := range rule.Backends {
			searchFields = append(searchFields, strings.TrimSpace(backend.URL))
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
		if query.CertificateID != nil {
			_, host, ok := parseRuleFrontendTarget(rule.FrontendURL)
			if !ok || !certDomainFound || host != certDomain {
				continue
			}
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
	if err := requireConfigMutationStore(s.store, s.mutationExecutor, s.revisionMutation); err != nil {
		return HTTPRule{}, err
	}
	if s.mutationExecutor == nil || s.revisionMutation {
		return s.createLegacy(ctx, agentID, input)
	}
	resolvedID, err := s.ensureAgentExists(ctx, agentID)
	if err != nil {
		return HTTPRule{}, err
	}
	if err := ensureRulePolicyCatalogFence(ctx, s.store, resolvedID); err != nil {
		return HTTPRule{}, err
	}
	targetAgentIDs, err := s.ruleMutationAgentIDs(ctx, resolvedID, nil, &input)
	if err != nil {
		return HTTPRule{}, err
	}
	postCommitActions := make([]func(), 0)
	var created HTTPRule
	_, err = s.mutationExecutor.Execute(ctx, revision.MutationRequest{
		Kind:             "http_rule.create",
		DependencyAction: revision.DependencyActionApply,
		Request:          input,
		Targets:          configMutationTargets(s.cfg, targetAgentIDs, nil),
		ResourceState: func(ctx context.Context, tx *storage.GormStore, _ revision.Target) (any, error) {
			return httpRuleMutationResourceState(ctx, tx, s.cfg)
		},
		ReplayResourceField: "rule",
		ReplayResource:      func() any { return created },
		Mutate: func(ctx context.Context, tx *storage.GormStore, revisions map[string]int64) error {
			txService := &ruleService{
				cfg: s.cfg, store: tx, revisionMutation: true, revisionNumbers: revisions,
				postCommitActions: &postCommitActions,
			}
			var mutateErr error
			created, mutateErr = txService.createLegacy(ctx, resolvedID, input)
			return mutateErr
		},
	})
	if err != nil {
		return HTTPRule{}, err
	}
	runConfigPostCommitActions(postCommitActions)
	return created, nil
}

func (s *ruleService) createLegacy(ctx context.Context, agentID string, input HTTPRuleInput) (HTTPRule, error) {
	resolvedID, err := s.ensureAgentExists(ctx, agentID)
	if err != nil {
		return HTTPRule{}, err
	}
	if err := lockRulePolicyCatalogFence(ctx, s.store, resolvedID, s.revisionMutation); err != nil {
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
	allocator, err := newConfigIdentityAllocatorFromStore(ctx, s.cfg, s.store)
	if err != nil {
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
		return HTTPRule{}, err
	}
	rule.AgentID = resolvedID
	if err := s.validateHTTPBackendProviders(ctx, resolvedID, rule.Backends); err != nil {
		return HTTPRule{}, err
	}
	if err := validateRulePolicyReference(ctx, s.store, resolvedID, rule.PolicyRef, policyExtensionHTTP); err != nil {
		return HTTPRule{}, err
	}
	rule.Revision = configMutationRevision(s.revisionNumbers, resolvedID, allocator.AllocateRevisionForAgent(resolvedID, maxRevision))
	if err := validateUniqueHTTPFrontendBinding(append(rows, httpRuleToRow(rule))); err != nil {
		return HTTPRule{}, err
	}
	egressExecutorAgentIDs, egressExecutorRevision, err := egressProfileScheduleTargets(ctx, s.store, resolvedID, rule.RelayLayers, rule.EgressProfileID, rule.Revision)
	if err != nil {
		return HTTPRule{}, err
	}
	agentRollbackRows, err := snapshotAgentRowsForRollback(ctx, s.store, uniqueAgentIDs(append([]string{resolvedID}, egressExecutorAgentIDs...)))
	if err != nil {
		return HTTPRule{}, err
	}

	nextRows := append(append([]storage.HTTPRuleRow(nil), rows...), httpRuleToRow(rule))
	certRowsChanged := false
	var originalCertRows []storage.ManagedCertificateRow
	var nextCertRows []storage.ManagedCertificateRow
	autoManagedDNSIssueIDs := []int(nil)
	if scheme, _, ok := parseRuleFrontendTarget(rule.FrontendURL); ok && scheme == "https" {
		originalCertRows, nextCertRows, certRowsChanged, err = s.prepareManagedCertificatesForRuleMutation(
			ctx,
			resolvedID,
			&rule,
			httpRulesFromRows(nextRows),
			false,
		)
		if err != nil {
			return HTTPRule{}, err
		}
		if certRowsChanged {
			autoManagedDNSIssueIDs, err = s.prepareAutoManagedDNSCertificateIssues(originalCertRows, nextCertRows)
			if err != nil {
				return HTTPRule{}, err
			}
			if err := s.store.SaveManagedCertificates(ctx, nextCertRows); err != nil {
				return HTTPRule{}, err
			}
		}
	}
	if err := consumeResourceQuota(ctx, s.store, "http_rule", fmt.Sprintf("%s:%d", resolvedID, rule.ID), "agent", resolvedID, "rule_count", 1); err != nil {
		return HTTPRule{}, err
	}
	if err := consumeResourceQuota(ctx, s.store, "http_rule", fmt.Sprintf("%s:%d", resolvedID, rule.ID), "agent", resolvedID, "application_count", 1); err != nil {
		return HTTPRule{}, err
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
	if err := s.bumpRemoteDesiredRevision(ctx, resolvedID, rule.Revision); err != nil {
		return rollbackPostSave(err)
	}
	deferLocalApply := len(autoManagedDNSIssueIDs) > 0
	if err := s.bumpDependentAgentRevisions(ctx, egressExecutorAgentIDs, egressExecutorRevision, deferLocalApply); err != nil {
		return rollbackPostSave(err)
	}
	if !deferLocalApply {
		if err := s.triggerLocalApply(ctx, resolvedID); err != nil {
			return rollbackPostSave(err)
		}
	}
	if certRowsChanged {
		s.runAfterRevisionCommit(func() {
			cleanupManagedCertificateMaterialBestEffort(ctx, s.store, originalCertRows, nextCertRows)
		})
	}
	for _, certID := range autoManagedDNSIssueIDs {
		certID := certID
		s.runAfterRevisionCommit(func() { ManagedCertificateDispatcher().Submit(certID) })
	}
	return rule, nil
}

func (s *ruleService) Update(ctx context.Context, agentID string, id int, input HTTPRuleInput) (HTTPRule, error) {
	if err := requireConfigMutationStore(s.store, s.mutationExecutor, s.revisionMutation); err != nil {
		return HTTPRule{}, err
	}
	if s.mutationExecutor == nil || s.revisionMutation {
		return s.updateLegacy(ctx, agentID, id, input)
	}
	resolvedID, err := s.ensureAgentExists(ctx, agentID)
	if err != nil {
		return HTTPRule{}, err
	}
	if err := ensureRulePolicyCatalogFence(ctx, s.store, resolvedID); err != nil {
		return HTTPRule{}, err
	}
	current, err := s.Get(ctx, resolvedID, id)
	if err != nil {
		return HTTPRule{}, err
	}
	targetAgentIDs, err := s.ruleMutationAgentIDs(ctx, resolvedID, &current, &input)
	if err != nil {
		return HTTPRule{}, err
	}
	postCommitActions := make([]func(), 0)
	var updated HTTPRule
	result, err := s.mutationExecutor.Execute(ctx, revision.MutationRequest{
		Kind:             "http_rule.update",
		DependencyAction: revision.DependencyActionApply,
		Request: struct {
			ID    int           `json:"id"`
			Input HTTPRuleInput `json:"input"`
		}{ID: id, Input: input},
		Targets: configMutationTargets(s.cfg, targetAgentIDs, nil),
		ResourceState: func(ctx context.Context, tx *storage.GormStore, _ revision.Target) (any, error) {
			return httpRuleMutationResourceState(ctx, tx, s.cfg)
		},
		ReplayResourceField: "rule",
		ReplayResource:      func() any { return updated },
		Mutate: func(ctx context.Context, tx *storage.GormStore, revisions map[string]int64) error {
			txService := &ruleService{
				cfg: s.cfg, store: tx, revisionMutation: true, revisionNumbers: revisions,
				postCommitActions: &postCommitActions,
			}
			var mutateErr error
			updated, mutateErr = txService.updateLegacy(ctx, resolvedID, id, input)
			return mutateErr
		},
	})
	if err != nil {
		return HTTPRule{}, err
	}
	if result.NoOp {
		return s.Get(ctx, resolvedID, id)
	}
	runConfigPostCommitActions(postCommitActions)
	return updated, nil
}

func (s *ruleService) updateLegacy(ctx context.Context, agentID string, id int, input HTTPRuleInput) (HTTPRule, error) {
	resolvedID, err := s.ensureAgentExists(ctx, agentID)
	if err != nil {
		return HTTPRule{}, err
	}
	if err := lockRulePolicyCatalogFence(ctx, s.store, resolvedID, s.revisionMutation); err != nil {
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

	allocator, err := newConfigIdentityAllocatorFromStore(ctx, s.cfg, s.store)
	if err != nil {
		return HTTPRule{}, err
	}

	rule, err := s.normalizeHTTPRuleInput(ctx, input, current, id)
	if err != nil {
		return HTTPRule{}, err
	}
	rule.AgentID = resolvedID
	if err := s.validateHTTPBackendProviders(ctx, resolvedID, rule.Backends); err != nil {
		return HTTPRule{}, err
	}
	if err := validateRulePolicyReference(ctx, s.store, resolvedID, rule.PolicyRef, policyExtensionHTTP); err != nil {
		return HTTPRule{}, err
	}
	rule.Revision = configMutationRevision(s.revisionNumbers, resolvedID, allocator.AllocateRevisionForAgent(resolvedID, maxRevision))

	nextRows := append([]storage.HTTPRuleRow(nil), rows...)
	nextRows[targetIndex] = httpRuleToRow(rule)
	if err := validateUniqueHTTPFrontendBinding(nextRows); err != nil {
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
		return HTTPRule{}, err
	}
	autoManagedDNSIssueIDs := []int(nil)
	if certRowsChanged {
		autoManagedDNSIssueIDs, err = s.prepareAutoManagedDNSCertificateIssues(originalCertRows, nextCertRows)
		if err != nil {
			return HTTPRule{}, err
		}
		if err := s.store.SaveManagedCertificates(ctx, nextCertRows); err != nil {
			return HTTPRule{}, err
		}
	}
	egressExecutorAgentIDs, egressExecutorRevision, err := egressProfileScheduleTargets(ctx, s.store, resolvedID, rule.RelayLayers, rule.EgressProfileID, rule.Revision)
	if err != nil {
		if certRowsChanged {
			if rollbackErr := s.store.SaveManagedCertificates(ctx, originalCertRows); rollbackErr != nil {
				return HTTPRule{}, fmt.Errorf("%v (rollback failed: %v)", err, rollbackErr)
			}
		}
		return HTTPRule{}, err
	}
	previousEgressExecutorAgentIDs, err := egressProfileExecutorAgentIDsForMutation(ctx, s.store, resolvedID, current.RelayLayers, current.EgressProfileID)
	if err != nil {
		if certRowsChanged {
			if rollbackErr := s.store.SaveManagedCertificates(ctx, originalCertRows); rollbackErr != nil {
				return HTTPRule{}, fmt.Errorf("%v (rollback failed: %v)", err, rollbackErr)
			}
		}
		return HTTPRule{}, err
	}
	egressExecutorAgentIDs = uniqueAgentIDs(append(egressExecutorAgentIDs, previousEgressExecutorAgentIDs...))
	agentRollbackRows, err := snapshotAgentRowsForRollback(ctx, s.store, uniqueAgentIDs(append([]string{resolvedID}, egressExecutorAgentIDs...)))
	if err != nil {
		if certRowsChanged {
			if rollbackErr := s.store.SaveManagedCertificates(ctx, originalCertRows); rollbackErr != nil {
				return HTTPRule{}, fmt.Errorf("%v (rollback failed: %v)", err, rollbackErr)
			}
		}
		return HTTPRule{}, err
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
	if err := s.bumpRemoteDesiredRevision(ctx, resolvedID, rule.Revision); err != nil {
		return rollbackPostSave(err)
	}
	deferLocalApply := len(autoManagedDNSIssueIDs) > 0
	if err := s.bumpDependentAgentRevisions(ctx, egressExecutorAgentIDs, egressExecutorRevision, deferLocalApply); err != nil {
		return rollbackPostSave(err)
	}
	if !deferLocalApply {
		if err := s.triggerLocalApply(ctx, resolvedID); err != nil {
			return rollbackPostSave(err)
		}
	}
	if certRowsChanged {
		s.runAfterRevisionCommit(func() {
			cleanupManagedCertificateMaterialBestEffort(ctx, s.store, originalCertRows, nextCertRows)
		})
	}
	for _, certID := range autoManagedDNSIssueIDs {
		certID := certID
		s.runAfterRevisionCommit(func() { ManagedCertificateDispatcher().Submit(certID) })
	}
	dropped := make([]string, 0)
	nextIDs := make(map[string]struct{})
	for _, instanceID := range pluginProviderInstanceIDs(rule) {
		nextIDs[instanceID] = struct{}{}
	}
	for _, instanceID := range pluginProviderInstanceIDs(current) {
		if _, keep := nextIDs[instanceID]; !keep {
			dropped = append(dropped, instanceID)
		}
	}
	if err := s.dropHTTPRulePluginBindings(ctx, resolvedID, rule.ID, dropped); err != nil {
		return rollbackPostSave(err)
	}
	return rule, nil
}

func (s *ruleService) Delete(ctx context.Context, agentID string, id int) (HTTPRule, error) {
	if err := requireConfigMutationStore(s.store, s.mutationExecutor, s.revisionMutation); err != nil {
		return HTTPRule{}, err
	}
	if s.mutationExecutor == nil || s.revisionMutation {
		return s.deleteLegacy(ctx, agentID, id)
	}
	resolvedID, err := s.ensureAgentExists(ctx, agentID)
	if err != nil {
		return HTTPRule{}, err
	}
	if err := ensureRulePolicyCatalogFence(ctx, s.store, resolvedID); err != nil {
		return HTTPRule{}, err
	}
	current, err := s.Get(ctx, resolvedID, id)
	if err != nil {
		return HTTPRule{}, err
	}
	targetAgentIDs, err := s.ruleMutationAgentIDs(ctx, resolvedID, &current, nil)
	if err != nil {
		return HTTPRule{}, err
	}
	postCommitActions := make([]func(), 0)
	var deleted HTTPRule
	_, err = s.mutationExecutor.Execute(ctx, revision.MutationRequest{
		Kind:             "http_rule.delete",
		DependencyAction: revision.DependencyActionDelete,
		Request:          map[string]int{"id": id},
		Targets:          configMutationTargets(s.cfg, targetAgentIDs, nil),
		ResourceState: func(ctx context.Context, tx *storage.GormStore, _ revision.Target) (any, error) {
			return httpRuleMutationResourceState(ctx, tx, s.cfg)
		},
		ReplayResourceField: "rule",
		ReplayResource:      func() any { return deleted },
		Mutate: func(ctx context.Context, tx *storage.GormStore, revisions map[string]int64) error {
			txService := &ruleService{
				cfg: s.cfg, store: tx, revisionMutation: true, revisionNumbers: revisions,
				postCommitActions: &postCommitActions,
			}
			var mutateErr error
			deleted, mutateErr = txService.deleteLegacy(ctx, resolvedID, id)
			return mutateErr
		},
	})
	if err != nil {
		return HTTPRule{}, err
	}
	runConfigPostCommitActions(postCommitActions)
	return deleted, nil
}

func (s *ruleService) deleteLegacy(ctx context.Context, agentID string, id int) (HTTPRule, error) {
	resolvedID, err := s.ensureAgentExists(ctx, agentID)
	if err != nil {
		return HTTPRule{}, err
	}
	if err := lockRulePolicyCatalogFence(ctx, s.store, resolvedID, s.revisionMutation); err != nil {
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
	if err := consumeResourceQuota(ctx, s.store, "http_rule", fmt.Sprintf("%s:%d", resolvedID, deleted.ID), "agent", resolvedID, "rule_count", -1); err != nil {
		return HTTPRule{}, err
	}
	if err := consumeResourceQuota(ctx, s.store, "http_rule", fmt.Sprintf("%s:%d", resolvedID, deleted.ID), "agent", resolvedID, "application_count", -1); err != nil {
		return HTTPRule{}, err
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
	if err := s.bumpDependentAgentRevisions(ctx, egressExecutorAgentIDs, nextRevision, false); err != nil {
		return rollbackPostSave(err)
	}
	if err := s.triggerLocalApply(ctx, resolvedID); err != nil {
		return rollbackPostSave(err)
	}
	if certRowsChanged {
		s.runAfterRevisionCommit(func() {
			cleanupManagedCertificateMaterialBestEffort(ctx, s.store, originalCertRows, nextCertRows)
		})
	}
	_ = deleteTrafficByScopeIfSupported(ctx, s.store, resolvedID, "http_rule", deleted.ID)
	if err := s.dropHTTPRulePluginBindings(ctx, resolvedID, deleted.ID, pluginProviderInstanceIDs(deleted)); err != nil {
		return rollbackPostSave(err)
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

func (s *ruleService) bumpDependentAgentRevisions(ctx context.Context, agentIDs []string, revision int, deferLocalApply bool) error {
	if s.revisionMutation {
		return nil
	}
	for _, agentID := range agentIDs {
		if err := s.bumpRemoteDesiredRevision(ctx, agentID, revision); err != nil {
			return err
		}
		if deferLocalApply && s.cfg.EnableLocalAgent && agentID == s.cfg.LocalAgentID {
			continue
		}
		if err := s.triggerLocalApply(ctx, agentID); err != nil {
			return err
		}
	}
	return nil
}

func (s *ruleService) runAfterRevisionCommit(action func()) {
	if action == nil {
		return
	}
	if s.revisionMutation && s.postCommitActions != nil {
		*s.postCommitActions = append(*s.postCommitActions, action)
		return
	}
	action()
}

func (s *ruleService) ruleMutationAgentIDs(
	ctx context.Context,
	ruleAgentID string,
	current *HTTPRule,
	input *HTTPRuleInput,
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

func httpRuleMutationResourceState(ctx context.Context, tx *storage.GormStore, cfg config.Config) (any, error) {
	agentIDs, err := allKnownAgentIDs(ctx, cfg, tx)
	if err != nil {
		return nil, err
	}
	rows := make([]storage.HTTPRuleRow, 0)
	for _, agentID := range agentIDs {
		agentRows, err := tx.ListHTTPRules(ctx, agentID)
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

func (s *ruleService) prepareAutoManagedDNSCertificateIssues(originalRows []storage.ManagedCertificateRow, nextRows []storage.ManagedCertificateRow) ([]int, error) {
	previousByID := make(map[int]ManagedCertificate, len(originalRows))
	for _, row := range originalRows {
		previousByID[row.ID] = managedCertificateFromRow(row)
	}

	issueIDs := make([]int, 0)
	for index, row := range nextRows {
		cert := managedCertificateFromRow(row)
		var previous *ManagedCertificate
		if value, ok := previousByID[cert.ID]; ok {
			previous = &value
		}
		if !managedCertificateMutationNeedsManagedDNSIssue(previous, cert) {
			continue
		}
		if !s.autoManagedDNSIssuerAvailable() {
			return nil, fmt.Errorf("%w: managed certificates require ACME_DNS_PROVIDER=cf and CF_Token", ErrInvalidArgument)
		}
		scheduled := cert
		scheduled.Status = "issuing"
		scheduled.LastError = ""
		scheduled.BackoffClass = ""
		scheduled.RetryCount = 0
		scheduled.NextRetryAtUnix = 0
		nextRows[index] = managedCertificateToRow(scheduled)
		issueIDs = append(issueIDs, scheduled.ID)
	}
	return issueIDs, nil
}

func (s *ruleService) autoManagedDNSIssuerAvailable() bool {
	return (s.dnsTokenProviderReady != nil && s.dnsTokenProviderReady()) || (s.cfg.ManagedCloudflareDNSReady() && newMasterCFDNSManagedCertificateIssuer() != nil)
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
	if (s.dnsTokenProviderReady != nil && s.dnsTokenProviderReady()) || s.cfg.ManagedCloudflareDNSReady() {
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
	trustedProxyRanges := append([]string(nil), fallback.TrustedProxyRanges...)
	if input.TrustedProxyRanges != nil {
		trustedProxyRanges, err = normalizeTrustedPeerRanges(*input.TrustedProxyRanges)
		if err != nil {
			return HTTPRule{}, err
		}
	}
	policyRef, err := normalizeRulePolicyRef(input.PolicyRef, fallback.PolicyRef)
	if err != nil {
		return HTTPRule{}, err
	}

	egressProfileID, err := normalizeEgressProfileIDInput(input.EgressProfileID, fallback.EgressProfileID)
	if err != nil {
		return HTTPRule{}, err
	}
	if egressProfileID != nil {
		if err := authorizeReferencedResource(ctx, s.store, "egress_profile", strconv.Itoa(*egressProfileID)); err != nil {
			return HTTPRule{}, err
		}
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

	return HTTPRule{
		ID:                 id,
		AgentID:            fallback.AgentID,
		FrontendURL:        frontendURL,
		BackendURL:         backendURL,
		Backends:           backends,
		LoadBalancing:      loadBalancing,
		Enabled:            enabled,
		Tags:               tags,
		ProxyRedirect:      proxyRedirect,
		RelayChain:         relayChain,
		RelayLayers:        relayLayers,
		RelayObfs:          relayObfs,
		PassProxyHeaders:   passProxyHeaders,
		UserAgent:          userAgent,
		CustomHeaders:      customHeaders,
		EgressProfileID:    egressProfileID,
		TrustedProxyRanges: trustedProxyRanges,
		PolicyRef:          policyRef,
		Revision:           fallback.Revision,
	}, nil
}

func (s *ruleService) getEnabledEgressProfile(ctx context.Context, id int) (EgressProfile, error) {
	return getEnabledEgressProfile(ctx, s.store, id)
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

func normalizeHTTPBackendsInput(input HTTPRuleInput, fallback HTTPRule) ([]HTTPRuleBackend, error) {
	if input.Backends != nil {
		return normalizeHTTPBackends(*input.Backends)
	}

	if input.BackendURL != nil {
		return nil, fmt.Errorf("%w: backend_url is legacy; use backends[].url", ErrInvalidArgument)
	}

	return normalizeHTTPBackends(fallback.Backends)
}

func normalizeHTTPBackends(backends []HTTPRuleBackend) ([]HTTPRuleBackend, error) {
	normalized := make([]HTTPRuleBackend, 0, len(backends))
	for _, backend := range backends {
		backend.Kind = strings.TrimSpace(backend.Kind)
		backend.URL = strings.TrimSpace(backend.URL)
		if (backend.Kind == "" || backend.Kind == pluginsdk.HTTPBackendKindURL) && backend.PluginProvider == nil {
			if !isValidHTTPURL(backend.URL) {
				continue
			}
			normalized = append(normalized, HTTPRuleBackend{URL: backend.URL})
			continue
		}
		if backend.PluginProvider != nil {
			provider := *backend.PluginProvider
			provider.InstanceID = strings.TrimSpace(provider.InstanceID)
			provider.ProviderID = strings.TrimSpace(provider.ProviderID)
			backend.PluginProvider = &provider
		}
		normalized = append(normalized, backend)
	}
	if len(normalized) == 0 {
		return nil, fmt.Errorf("%w: backends must contain at least one valid http/https URL or plugin provider", ErrInvalidArgument)
	}
	if err := pluginsdk.ValidateHTTPBackends(normalized); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidArgument, err)
	}
	return normalized, nil
}

type httpBackendProviderAdmissionStore interface {
	LoadAgentPluginGenerations(context.Context, string, string) ([]storage.PluginGeneration, error)
	GetPluginInstance(context.Context, string) (storage.PluginInstanceRow, bool, error)
	GetPluginAgentRuntimeStatusFence(context.Context, string, string, string) (storage.PluginAgentRuntimeStatusRow, bool, error)
}

func pluginPublishHTTPRuleInput(frontendURL, instanceID, providerID, pluginID string) HTTPRuleInput {
	enabled := true
	frontend := strings.TrimSpace(frontendURL)
	tags := pluginPublishRuleTags(pluginID)
	return HTTPRuleInput{
		FrontendURL: &frontend,
		Enabled:     &enabled,
		Tags:        &tags,
		Backends: &[]HTTPRuleBackend{{
			Kind: pluginsdk.HTTPBackendKindPluginProvider,
			PluginProvider: &pluginsdk.HTTPPluginProviderRef{
				InstanceID: strings.TrimSpace(instanceID),
				ProviderID: strings.TrimSpace(providerID),
			},
		}},
	}
}

func pluginPublishFrontendURLInput(frontendURL, pluginID string, currentTags []string) HTTPRuleInput {
	frontend := strings.TrimSpace(frontendURL)
	tags := normalizeTagUnion(currentTags, pluginPublishRuleTags(pluginID))
	return HTTPRuleInput{FrontendURL: &frontend, Tags: &tags}
}

func pluginPublishRuleTags(pluginID string) []string {
	tags := []string{"plugin"}
	if pluginID = strings.TrimSpace(pluginID); pluginID != "" {
		tags = append(tags, "plugin:"+pluginID)
	}
	return tags
}

func (s *ruleService) validateHTTPBackendProviders(ctx context.Context, agentID string, backends []HTTPRuleBackend) error {
	hasProvider := false
	for _, backend := range backends {
		if backend.Kind == pluginsdk.HTTPBackendKindPluginProvider {
			hasProvider = true
			break
		}
	}
	if !hasProvider {
		return nil
	}
	if s != nil && s.pluginPublishAdmission {
		return s.validateHTTPBackendProvidersForPluginPublish(ctx, agentID, backends)
	}
	providerStore, ok := s.store.(httpBackendProviderAdmissionStore)
	if !ok {
		return fmt.Errorf("%w: HTTP backend provider admission is unavailable", ErrInvalidArgument)
	}
	platform := runtime.GOOS + "-" + runtime.GOARCH
	agents, err := s.store.ListAgents(ctx)
	if err != nil {
		return err
	}
	for _, agent := range agents {
		if strings.TrimSpace(agent.ID) == agentID && strings.TrimSpace(agent.Platform) != "" {
			platform = strings.TrimSpace(agent.Platform)
			break
		}
	}
	generations, err := providerStore.LoadAgentPluginGenerations(ctx, agentID, platform)
	if err != nil {
		return fmt.Errorf("%w: load HTTP backend providers: %v", ErrInvalidArgument, err)
	}
	byInstance := make(map[string]storage.PluginGeneration, len(generations))
	for _, generation := range generations {
		byInstance[generation.InstanceID] = generation
	}
	for _, backend := range backends {
		if backend.Kind != pluginsdk.HTTPBackendKindPluginProvider || backend.PluginProvider == nil {
			continue
		}
		ref := backend.PluginProvider
		if err := authorizeReferencedResource(ctx, s.store, "plugin_instance", ref.InstanceID); err != nil {
			return err
		}
		instance, found, err := providerStore.GetPluginInstance(ctx, ref.InstanceID)
		if err != nil {
			return err
		}
		if !found || !instance.DesiredEnabled || instance.CurrentState != "active" {
			return fmt.Errorf("%w: plugin provider instance %q is not active", ErrInvalidArgument, ref.InstanceID)
		}
		generation, found := byInstance[ref.InstanceID]
		if !found || generation.Runtime.Kind != pluginsdk.RuntimeRPCService || generation.Runtime.HostScope != pluginsdk.HostScopeAgent || generation.Target.ID != agentID {
			return fmt.Errorf("%w: plugin provider instance %q is unavailable on Agent %q", ErrInvalidArgument, ref.InstanceID, agentID)
		}
		if !slices.Contains(generation.ExtensionPoints, pluginsdk.ExtensionHTTPBackendProvider) || !slices.Contains(generation.RequiredFeatures, pluginsdk.RPCFeatureHTTPBackendProviderV1) {
			return fmt.Errorf("%w: plugin provider instance %q lacks the canonical HTTP provider contract", ErrInvalidArgument, ref.InstanceID)
		}
		declared := false
		for _, descriptor := range generation.HTTPBackendProviders {
			if descriptor.ID == ref.ProviderID {
				declared = true
				break
			}
		}
		if !declared {
			return fmt.Errorf("%w: plugin provider %q is not declared by instance %q", ErrInvalidArgument, ref.ProviderID, ref.InstanceID)
		}
		status, found, err := providerStore.GetPluginAgentRuntimeStatusFence(ctx, generation.OperationID, agentID, generation.InstanceID)
		if err != nil {
			return err
		}
		if !found || status.State != "active" || status.GenerationID != generation.ID {
			return fmt.Errorf("%w: plugin provider instance %q has no ready generation", ErrInvalidArgument, ref.InstanceID)
		}
	}
	return nil
}

type pluginPublishAdmissionStore interface {
	GetPluginInstance(context.Context, string) (storage.PluginInstanceRow, bool, error)
	GetInstalledPlugin(context.Context, string) (storage.InstalledPluginRow, bool, error)
	GetPluginPackage(context.Context, string) (storage.PluginPackageRow, bool, error)
	GetPluginPackageByIdentity(context.Context, string) (storage.PluginPackageRow, bool, error)
}

func (s *ruleService) validateHTTPBackendProvidersForPluginPublish(ctx context.Context, agentID string, backends []HTTPRuleBackend) error {
	providerStore, ok := s.store.(pluginPublishAdmissionStore)
	if !ok {
		return fmt.Errorf("%w: plugin publish admission is unavailable", ErrInvalidArgument)
	}
	defaultTargetID := ""
	if s != nil {
		defaultTargetID = strings.TrimSpace(s.cfg.LocalAgentID)
	}
	for _, backend := range backends {
		if backend.Kind != pluginsdk.HTTPBackendKindPluginProvider || backend.PluginProvider == nil {
			continue
		}
		ref := backend.PluginProvider
		if err := authorizeReferencedResource(ctx, s.store, "plugin_instance", ref.InstanceID); err != nil {
			return err
		}
		instance, found, err := providerStore.GetPluginInstance(ctx, ref.InstanceID)
		if err != nil {
			return err
		}
		if !found || !instance.DesiredEnabled {
			return fmt.Errorf("%w: plugin provider instance %q is not desired-enabled", ErrInvalidArgument, ref.InstanceID)
		}
		targets, err := pluginPublishInstanceTargets(instance, defaultTargetID)
		if err != nil {
			return fmt.Errorf("%w: plugin provider instance %q targets are invalid", ErrInvalidArgument, ref.InstanceID)
		}
		if !slices.Contains(targets, strings.TrimSpace(agentID)) {
			return fmt.Errorf("%w: plugin provider instance %q does not target Agent %q", ErrInvalidArgument, ref.InstanceID, agentID)
		}
		installed, found, err := providerStore.GetInstalledPlugin(ctx, instance.PluginID)
		if err != nil {
			return err
		}
		if !found {
			return fmt.Errorf("%w: plugin provider instance %q is not installed", ErrInvalidArgument, ref.InstanceID)
		}
		var packageRow storage.PluginPackageRow
		if strings.TrimSpace(installed.ActivePackageIdentity) != "" {
			packageRow, found, err = providerStore.GetPluginPackageByIdentity(ctx, installed.ActivePackageIdentity)
		} else {
			packageRow, found, err = providerStore.GetPluginPackage(ctx, installed.ActivePackageDigest)
		}
		if err != nil {
			return err
		}
		if !found {
			return fmt.Errorf("%w: plugin provider package is unavailable", ErrInvalidArgument)
		}
		var manifest pluginsdk.Manifest
		if err := json.Unmarshal([]byte(packageRow.ManifestJSON), &manifest); err != nil {
			return fmt.Errorf("%w: plugin provider manifest is invalid", ErrInvalidArgument)
		}
		if !pluginManifestDeclaresHTTPProvider(manifest, ref.ProviderID) {
			return fmt.Errorf("%w: plugin provider %q is not declared by instance %q", ErrInvalidArgument, ref.ProviderID, ref.InstanceID)
		}
	}
	return nil
}

func pluginPublishInstanceTargets(instance storage.PluginInstanceRow, defaultTargetID string) ([]string, error) {
	raw := json.RawMessage(instance.TargetJSON)
	if strings.TrimSpace(instance.PendingTargetJSON) != "" {
		raw = json.RawMessage(instance.PendingTargetJSON)
	}
	return pluginTargetIDs(raw, defaultTargetID)
}

func pluginManifestDeclaresHTTPProvider(manifest pluginsdk.Manifest, providerID string) bool {
	providerID = strings.TrimSpace(providerID)
	if providerID == "" || len(manifest.HTTPBackendProviders) == 0 {
		return false
	}
	if !slices.Contains(manifest.ExtensionPoints, pluginsdk.ExtensionHTTPBackendProvider) {
		return false
	}
	for _, descriptor := range manifest.HTTPBackendProviders {
		if strings.TrimSpace(descriptor.ID) == providerID {
			return true
		}
	}
	return false
}

func pluginProviderInstanceIDs(rule HTTPRule) []string {
	ids := make([]string, 0)
	seen := make(map[string]struct{})
	for _, backend := range rule.Backends {
		if backend.Kind != pluginsdk.HTTPBackendKindPluginProvider || backend.PluginProvider == nil {
			continue
		}
		instanceID := strings.TrimSpace(backend.PluginProvider.InstanceID)
		if instanceID == "" {
			continue
		}
		if _, exists := seen[instanceID]; exists {
			continue
		}
		seen[instanceID] = struct{}{}
		ids = append(ids, instanceID)
	}
	return ids
}

type pluginBindingSyncStore interface {
	GetPluginInstance(context.Context, string) (storage.PluginInstanceRow, bool, error)
	GetInstalledPlugin(context.Context, string) (storage.InstalledPluginRow, bool, error)
	ApplyPluginMutation(context.Context, storage.PluginMutation) error
}

func (s *ruleService) dropHTTPRulePluginBindings(ctx context.Context, agentID string, ruleID int, instanceIDs []string) error {
	if ruleID <= 0 || len(instanceIDs) == 0 {
		return nil
	}
	store, ok := s.store.(pluginBindingSyncStore)
	if !ok {
		return nil
	}
	consumerID := strconv.Itoa(ruleID)
	agentID = strings.TrimSpace(agentID)
	for _, instanceID := range instanceIDs {
		instance, found, err := store.GetPluginInstance(ctx, instanceID)
		if err != nil {
			return err
		}
		if !found {
			continue
		}
		changed := false
		for _, field := range []*string{&instance.BindingsJSON, &instance.PendingBindingsJSON, &instance.RollbackBindingsJSON} {
			bindings, err := storage.CanonicalPluginInstanceBindings(*field)
			if err != nil {
				return err
			}
			filtered := bindings[:0]
			for _, binding := range bindings {
				if binding.Consumer.Kind == storage.PluginDependencyConsumerHTTPRule && binding.Consumer.ID == consumerID && binding.TargetAgentID == agentID {
					changed = true
					continue
				}
				filtered = append(filtered, binding)
			}
			encoded, err := storage.EncodePluginInstanceBindings(filtered)
			if err != nil {
				return err
			}
			*field = encoded
		}
		if !changed {
			continue
		}
		installed, found, err := store.GetInstalledPlugin(ctx, instance.PluginID)
		if err != nil {
			return err
		}
		if !found {
			continue
		}
		now := time.Now().UTC()
		instance.UpdatedAt = now
		installed.UpdatedAt = now
		operation := storage.PluginOperationRow{
			ID: lifecycleID("pluginop"), PluginID: instance.PluginID, InstanceID: instance.ID,
			Kind: "publish", Status: "succeeded", TargetPackageDigest: installed.ActivePackageDigest,
			AgentResultsJSON: "{}", ActorID: "system", CompletedAt: &now, CreatedAt: now,
		}
		if err := bindInstalledActiveOperation(&operation, installed); err != nil {
			return err
		}
		if err := store.ApplyPluginMutation(ctx, storage.PluginMutation{
			PluginID: instance.PluginID, ExpectedActive: installed.ActivePackageDigest,
			ExpectedStateVersion: installed.StateVersion, Installed: &installed, ReplaceInstance: &instance,
			Operation: operation, Audit: pluginLifecycleAudit(operation, operation.ActorID, "accepted", "", now),
		}); err != nil {
			return err
		}
	}
	return nil
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

func pluginHostRuleFrontendURL(domain string) (string, error) {
	domain = strings.TrimSpace(domain)
	if domain == "" || domain != strings.TrimSpace(domain) || strings.ContainsAny(domain, "\r\n\x00 /") {
		return "", fmt.Errorf("%w: domain is invalid", ErrInvalidArgument)
	}
	frontend := domain
	if !strings.Contains(domain, "://") {
		frontend = "http://" + domain
	}
	if !isValidHTTPURL(frontend) {
		return "", fmt.Errorf("%w: domain is not a valid HTTP frontend", ErrInvalidArgument)
	}
	return frontend, nil
}

func pluginHostRuleBackendURL(port int) (string, error) {
	if port <= 0 || port > 65535 {
		return "", fmt.Errorf("%w: port is invalid", ErrInvalidArgument)
	}
	return fmt.Sprintf("http://127.0.0.1:%d", port), nil
}

func parsePluginHostRuleRef(ruleRef string) (int, error) {
	ruleRef = strings.TrimSpace(ruleRef)
	if ruleRef == "" {
		return 0, fmt.Errorf("%w: rule_ref is required", ErrInvalidArgument)
	}
	id, err := strconv.Atoi(ruleRef)
	if err != nil || id <= 0 || strconv.Itoa(id) != ruleRef {
		return 0, fmt.Errorf("%w: rule_ref is invalid", ErrInvalidArgument)
	}
	return id, nil
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

	return HTTPRule{
		ID:                 row.ID,
		AgentID:            row.AgentID,
		FrontendURL:        row.FrontendURL,
		BackendURL:         "",
		Backends:           backends,
		LoadBalancing:      parseLoadBalancing(row.LoadBalancingJSON),
		Enabled:            row.Enabled,
		Tags:               parseStringArray(row.TagsJSON),
		ProxyRedirect:      row.ProxyRedirect,
		RelayChain:         []int{},
		RelayLayers:        parseIntLayers(row.RelayLayersJSON),
		RelayObfs:          row.RelayObfs,
		PassProxyHeaders:   row.PassProxyHeaders,
		UserAgent:          row.UserAgent,
		CustomHeaders:      parseCustomHeaders(row.CustomHeadersJSON),
		EgressProfileID:    normalizeOptionalPositiveInt(row.EgressProfileID),
		TrustedProxyRanges: parseStringArray(row.TrustedProxyRangesJSON),
		PolicyRef:          parseRulePolicyRef(row.PolicyRefJSON),
		Revision:           row.Revision,
	}
}

func httpRuleToRow(rule HTTPRule) storage.HTTPRuleRow {
	return storage.HTTPRuleRow{
		ID:                     rule.ID,
		AgentID:                rule.AgentID,
		FrontendURL:            rule.FrontendURL,
		BackendURL:             "",
		BackendsJSON:           marshalJSON(rule.Backends, "[]"),
		LoadBalancingJSON:      marshalJSON(rule.LoadBalancing, `{"strategy":"adaptive"}`),
		Enabled:                rule.Enabled,
		TagsJSON:               marshalJSON(rule.Tags, "[]"),
		ProxyRedirect:          rule.ProxyRedirect,
		RelayChainJSON:         "[]",
		RelayLayersJSON:        marshalJSON(rule.RelayLayers, "[]"),
		RelayObfs:              rule.RelayObfs,
		PassProxyHeaders:       rule.PassProxyHeaders,
		UserAgent:              rule.UserAgent,
		CustomHeadersJSON:      marshalJSON(rule.CustomHeaders, "[]"),
		EgressProfileID:        normalizeOptionalPositiveInt(rule.EgressProfileID),
		TrustedProxyRangesJSON: marshalJSON(rule.TrustedProxyRanges, "[]"),
		PolicyRefJSON:          marshalJSON(rule.PolicyRef, ""),
		Revision:               rule.Revision,
	}
}

func normalizeTrustedPeerRanges(values []string) ([]string, error) {
	if values == nil {
		return nil, nil
	}
	normalized := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, raw := range values {
		value := strings.TrimSpace(raw)
		if value == "" {
			return nil, fmt.Errorf("%w: trusted peer range is empty", ErrInvalidArgument)
		}
		prefix, err := netip.ParsePrefix(value)
		if err != nil {
			address, addressErr := netip.ParseAddr(value)
			if addressErr != nil {
				return nil, fmt.Errorf("%w: trusted peer range %q is invalid", ErrInvalidArgument, raw)
			}
			address = address.Unmap()
			prefix = netip.PrefixFrom(address, address.BitLen())
		}
		canonical := prefix.Masked().String()
		if _, duplicate := seen[canonical]; duplicate {
			continue
		}
		seen[canonical] = struct{}{}
		normalized = append(normalized, canonical)
	}
	sort.Strings(normalized)
	return normalized, nil
}

func normalizeRulePolicyRef(input, fallback *storage.PolicyRef) (*storage.PolicyRef, error) {
	if input == nil {
		return cloneRulePolicyRef(fallback), nil
	}
	id := strings.TrimSpace(input.ID)
	if id == "" {
		if len(input.Overlay) != 0 && string(input.Overlay) != "null" {
			return nil, fmt.Errorf("%w: policy_ref id is required with an overlay", ErrInvalidArgument)
		}
		return nil, nil
	}
	if err := pluginsdk.ValidatePolicyIdentity(input.ID); err != nil {
		return nil, fmt.Errorf("%w: policy_ref id is not canonical", ErrInvalidArgument)
	}
	overlay := append(json.RawMessage(nil), input.Overlay...)
	if len(overlay) != 0 && (len(overlay) > 128<<10 || !json.Valid(overlay)) {
		return nil, fmt.Errorf("%w: policy_ref overlay is invalid", ErrInvalidArgument)
	}
	return &storage.PolicyRef{ID: id, Overlay: overlay}, nil
}

type rulePolicyCatalogStore interface {
	LoadAgentPluginPolicies(context.Context, string) ([]storage.PluginPolicy, error)
}

type rulePolicyCatalogFenceStore interface {
	EnsureAgentPluginPolicyCatalog(context.Context, string) error
	LockAgentPluginPolicyCatalog(context.Context, string) error
}

const (
	policyExtensionHTTP = "http.request"
	policyExtensionL4   = "l4.accept"
)

func ensureRulePolicyCatalogFence(ctx context.Context, store any, agentID string) error {
	fenceStore, ok := store.(rulePolicyCatalogFenceStore)
	if !ok {
		return fmt.Errorf("%w: policy catalog fence is unavailable", errRevisionMutationStoreRequired)
	}
	return fenceStore.EnsureAgentPluginPolicyCatalog(ctx, agentID)
}

func lockRulePolicyCatalogFence(ctx context.Context, store any, agentID string, required bool) error {
	if !required {
		return nil
	}
	fenceStore, ok := store.(rulePolicyCatalogFenceStore)
	if !ok {
		return fmt.Errorf("%w: policy catalog fence is unavailable", errRevisionMutationStoreRequired)
	}
	return fenceStore.LockAgentPluginPolicyCatalog(ctx, agentID)
}

func validateRulePolicyReference(ctx context.Context, store any, agentID string, ref *storage.PolicyRef, extensionPoint string) error {
	if ref == nil {
		return nil
	}
	catalogStore, ok := store.(rulePolicyCatalogStore)
	if !ok {
		return fmt.Errorf("%w: policy catalog is unavailable", ErrInvalidArgument)
	}
	policies, err := catalogStore.LoadAgentPluginPolicies(ctx, agentID)
	if err != nil {
		return fmt.Errorf("resolve policy_ref %q: %w", ref.ID, err)
	}
	for _, policy := range policies {
		if policy.ID == ref.ID {
			frameBytes, frameErr := pluginsdk.PolicyV1EvaluateRequestFrameBytes(extensionPoint, strings.Repeat("r", pluginsdk.PolicyRequestIDMaxBytes), ref.Overlay)
			if frameErr != nil {
				return fmt.Errorf("%w: policy_ref %q frame is invalid: %v", ErrInvalidArgument, ref.ID, frameErr)
			}
			for index, stage := range policy.Stages {
				if !slices.Contains(stage.ExtensionPoints, extensionPoint) {
					return fmt.Errorf("%w: policy_ref %q stage %d does not support %q", ErrInvalidArgument, ref.ID, index, extensionPoint)
				}
				if int64(frameBytes) > stage.ResourceBudget.InputBytes {
					return fmt.Errorf("%w: policy_ref %q overlay exceeds stage %d input frame budget", ErrInvalidArgument, ref.ID, index)
				}
			}
			return nil
		}
	}
	return fmt.Errorf("%w: policy_ref %q is not active for agent %q", ErrInvalidArgument, ref.ID, agentID)
}

func cloneRulePolicyRef(ref *storage.PolicyRef) *storage.PolicyRef {
	if ref == nil {
		return nil
	}
	return &storage.PolicyRef{ID: ref.ID, Overlay: append(json.RawMessage(nil), ref.Overlay...)}
}

func parseRulePolicyRef(raw string) *storage.PolicyRef {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "null" {
		return nil
	}
	var ref storage.PolicyRef
	if err := json.Unmarshal([]byte(raw), &ref); err != nil {
		return &storage.PolicyRef{ID: "\x00invalid-policy-ref"}
	}
	return cloneRulePolicyRef(&ref)
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
			return newConflictError("frontend_url conflicts with existing rule: %d", existingID)
		}
		seen[binding] = row.ID
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
