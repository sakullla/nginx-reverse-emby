package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Store interface {
	ListAgents(context.Context) ([]AgentRow, error)
	ListHTTPRules(context.Context, string) ([]HTTPRuleRow, error)
	GetHTTPRule(context.Context, string, int) (HTTPRuleRow, bool, error)
	ListL4Rules(context.Context, string) ([]L4RuleRow, error)
	GetL4Rule(context.Context, string, int) (L4RuleRow, bool, error)
	ListRelayListeners(context.Context, string) ([]RelayListenerRow, error)
	ListEgressProfiles(context.Context) ([]EgressProfileRow, error)
	LoadLocalAgentState(context.Context) (LocalAgentStateRow, error)
	LoadAgentSnapshot(context.Context, string, AgentSnapshotInput) (Snapshot, error)
	ListVersionPolicies(context.Context) ([]VersionPolicyRow, error)
	ListManagedCertificates(context.Context) ([]ManagedCertificateRow, error)
	SaveAgent(context.Context, AgentRow) error
	SaveL4Rules(context.Context, string, []L4RuleRow) error
	SaveRelayListeners(context.Context, string, []RelayListenerRow) error
	SaveEgressProfiles(context.Context, []EgressProfileRow) error
	SaveVersionPolicies(context.Context, []VersionPolicyRow) error
	SaveManagedCertificates(context.Context, []ManagedCertificateRow) error
	LoadManagedCertificateMaterial(context.Context, string) (ManagedCertificateBundle, bool, error)
	SaveManagedCertificateMaterial(context.Context, string, ManagedCertificateBundle) error
	CleanupManagedCertificateMaterial(context.Context, []ManagedCertificateRow, []ManagedCertificateRow) error
}

// ManagedCertificateUpdateStore serializes a managed-certificate read/modify/
// write against the current rows. Implementations keep generation pointers
// owned by the generation store while merging caller-owned metadata.
type ManagedCertificateUpdateStore interface {
	UpdateManagedCertificates(context.Context, func([]ManagedCertificateRow) ([]ManagedCertificateRow, bool, error)) error
}

type EgressProfileReference struct {
	Kind    string
	AgentID string
	ID      int
}

type SQLiteStore = GormStore

const localRuntimeStateMetaKey = "local_runtime_state"

func NewSQLiteStore(dataRoot string, localAgentID string) (*SQLiteStore, error) {
	return NewStore(StoreConfig{
		Driver:              "sqlite",
		DataRoot:            dataRoot,
		LocalAgentID:        localAgentID,
		TrafficStatsEnabled: true,
	})
}

func (s *GormStore) ListAgents(ctx context.Context) ([]AgentRow, error) {
	var agents []AgentRow
	if err := s.db.WithContext(ctx).Order("id").Find(&agents).Error; err != nil {
		return nil, err
	}
	for i := range agents {
		normalizeAgentRow(&agents[i])
	}
	return agents, nil
}

func (s *GormStore) GetAgentTrafficState(ctx context.Context, agentID string) (bool, string, bool, error) {
	if agentID == "" || agentID == s.localAgentID {
		return false, "", false, nil
	}
	var row AgentRow
	if err := s.db.WithContext(ctx).
		Select("id", "traffic_blocked", "traffic_block_reason").
		Where("id = ?", agentID).
		Limit(1).
		Find(&row).Error; err != nil {
		return false, "", false, err
	}
	if row.ID == "" {
		return false, "", false, nil
	}
	return row.TrafficBlocked, defaultString(row.TrafficBlockReason, ""), true, nil
}

func (s *GormStore) SaveAgentTrafficState(ctx context.Context, agentID string, blocked bool, reason string) error {
	if agentID == "" || agentID == s.localAgentID {
		return nil
	}
	return s.db.WithContext(ctx).
		Model(&AgentRow{}).
		Where("id = ?", agentID).
		Updates(map[string]any{
			"traffic_blocked":      blocked,
			"traffic_block_reason": defaultString(reason, ""),
		}).Error
}

func (s *GormStore) loadAgentRevisionState(ctx context.Context, agentID string) (LocalAgentStateRow, error) {
	var row AgentRow
	err := s.db.WithContext(ctx).
		Where("id = ?", agentID).
		First(&row).Error
	if err == nil {
		normalizeAgentRow(&row)
		return LocalAgentStateRow{
			Version:         row.Version,
			DesiredRevision: row.DesiredRevision,
			CurrentRevision: row.CurrentRevision,
		}, nil
	}
	if err == gorm.ErrRecordNotFound {
		return LocalAgentStateRow{}, nil
	}
	return LocalAgentStateRow{}, err
}

func (s *GormStore) SetLocalAgentVersion(ctx context.Context, version string) error {
	return s.SetLocalAgentBuild(ctx, version, true)
}

func (s *GormStore) SetLocalAgentBuild(ctx context.Context, version string, present bool) error {
	if !present {
		s.localAgentPresent.Store(false)
		return nil
	}
	if err := s.db.WithContext(ctx).Model(&LocalAgentStateRow{}).Where("id = ?", 1).Update("version", strings.TrimSpace(version)).Error; err != nil {
		return err
	}
	s.localAgentPresent.Store(true)
	return nil
}

func (s *GormStore) LocalAgentBuild(ctx context.Context) (string, string, bool, error) {
	state, err := s.LoadLocalAgentState(ctx)
	return s.localAgentID, state.Version, s.localAgentPresent.Load(), err
}

func (s *GormStore) ListHTTPRules(ctx context.Context, agentID string) ([]HTTPRuleRow, error) {
	if agentID == "" {
		agentID = s.localAgentID
	}

	var rules []HTTPRuleRow
	if err := s.db.WithContext(ctx).
		Where("agent_id = ?", agentID).
		Order("id").
		Find(&rules).Error; err != nil {
		return nil, err
	}
	for i := range rules {
		normalizeHTTPRuleRow(&rules[i])
	}
	return rules, nil
}

func (s *GormStore) GetHTTPRule(ctx context.Context, agentID string, id int) (HTTPRuleRow, bool, error) {
	if agentID == "" {
		agentID = s.localAgentID
	}

	var rule HTTPRuleRow
	err := s.db.WithContext(ctx).
		Where("agent_id = ? AND id = ?", agentID, id).
		First(&rule).Error
	if err == nil {
		normalizeHTTPRuleRow(&rule)
		return rule, true, nil
	}
	if err == gorm.ErrRecordNotFound {
		return HTTPRuleRow{}, false, nil
	}
	return HTTPRuleRow{}, false, err
}

func (s *GormStore) LoadLocalAgentState(ctx context.Context) (LocalAgentStateRow, error) {
	var state LocalAgentStateRow
	err := s.db.WithContext(ctx).
		Where("id = ?", 1).
		Order("id").
		First(&state).Error
	if err == nil {
		normalizeLocalAgentStateRow(&state)
		return state, nil
	}
	if err == gorm.ErrRecordNotFound {
		return LocalAgentStateRow{
			ID:              1,
			LastApplyStatus: "success",
		}, nil
	}
	return LocalAgentStateRow{}, err
}

func (s *GormStore) LoadLocalRuntimeState(ctx context.Context) (RuntimeState, error) {
	var row MetaRow
	err := s.db.WithContext(ctx).
		Where("key = ?", localRuntimeStateMetaKey).
		First(&row).Error
	if err == nil {
		var state RuntimeState
		if unmarshalErr := json.Unmarshal([]byte(strings.TrimSpace(row.Value)), &state); unmarshalErr != nil {
			return RuntimeState{}, unmarshalErr
		}
		if state.Metadata == nil {
			state.Metadata = map[string]string{}
		}
		return state, nil
	}
	if err != gorm.ErrRecordNotFound {
		return RuntimeState{}, err
	}

	localState, err := s.LoadLocalAgentState(ctx)
	if err != nil {
		return RuntimeState{}, err
	}
	return RuntimeState{
		CurrentRevision:   int64(localState.CurrentRevision),
		LastApplyRevision: int64(localState.LastApplyRevision),
		LastApplyStatus:   localState.LastApplyStatus,
		LastApplyMessage:  localState.LastApplyMessage,
		Metadata:          map[string]string{},
	}, nil
}

func (s *GormStore) LoadLocalSnapshot(ctx context.Context, agentID string) (Snapshot, error) {
	return s.loadCompleteSnapshot(ctx, func(scoped *GormStore) (Snapshot, error) {
		return scoped.loadLocalSnapshot(ctx, agentID, true)
	})
}

func (s *GormStore) LoadLocalIntentSnapshot(ctx context.Context, agentID string) (Snapshot, error) {
	return s.loadCompleteSnapshot(ctx, func(scoped *GormStore) (Snapshot, error) {
		return scoped.loadLocalSnapshot(ctx, agentID, false)
	})
}

// LoadRelayListenerCredentialTargets returns configured relay listeners without
// applying the runtime fail-closed projection. PKI credential reconciliation
// must continue while relay publication is fenced during emergency rotation.
func (s *GormStore) LoadRelayListenerCredentialTargets(ctx context.Context, agentID string) ([]RelayListener, error) {
	rows, err := s.ListRelayListeners(ctx, s.resolveAgentID(agentID))
	if err != nil {
		return nil, err
	}
	rows, _ = partitionSnapshotRelayRows(rows)
	return snapshotRelayListeners(rows, nil), nil
}

func (s *GormStore) loadLocalSnapshot(ctx context.Context, agentID string, runtimeFiltered bool) (Snapshot, error) {
	localState, err := s.LoadLocalAgentState(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	return s.loadAgentSnapshot(ctx, agentID, AgentSnapshotInput{
		DesiredVersion:  localState.DesiredVersion,
		DesiredRevision: localState.DesiredRevision,
		CurrentRevision: localState.CurrentRevision,
		Platform:        runtime.GOOS + "-" + runtime.GOARCH,
	}, runtimeFiltered)
}

func (s *GormStore) LoadAgentSnapshot(ctx context.Context, agentID string, input AgentSnapshotInput) (Snapshot, error) {
	return s.loadCompleteSnapshot(ctx, func(scoped *GormStore) (Snapshot, error) {
		return scoped.loadAgentSnapshot(ctx, agentID, input, true)
	})
}

func (s *GormStore) LoadAgentIntentSnapshot(ctx context.Context, agentID string, input AgentSnapshotInput) (Snapshot, error) {
	return s.loadCompleteSnapshot(ctx, func(scoped *GormStore) (Snapshot, error) {
		return scoped.loadAgentSnapshot(ctx, agentID, input, false)
	})
}

func (s *GormStore) loadCompleteSnapshot(ctx context.Context, load func(*GormStore) (Snapshot, error)) (Snapshot, error) {
	var snapshot Snapshot
	err := s.readSnapshotTransaction(ctx, func(scoped *GormStore) error {
		var err error
		snapshot, err = load(scoped)
		return err
	})
	return snapshot, err
}

func (s *GormStore) loadAgentSnapshot(ctx context.Context, agentID string, input AgentSnapshotInput, runtimeFiltered bool) (Snapshot, error) {
	resolvedAgentID := s.resolveAgentID(agentID)

	httpRows, err := s.ListHTTPRules(ctx, resolvedAgentID)
	if err != nil {
		return Snapshot{}, err
	}

	l4Rows, err := s.ListL4Rules(ctx, resolvedAgentID)
	if err != nil {
		return Snapshot{}, err
	}
	l4Rows = filterSyncL4RuleRows(l4Rows)

	relayRows, err := s.loadRelayListenersForSync(ctx, resolvedAgentID, httpRows, l4Rows)
	if err != nil {
		return Snapshot{}, err
	}
	storedEgressRows, err := s.ListEgressProfiles(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	allEgressRows, excludedEgressIDs := partitionSnapshotEgressRows(storedEgressRows)
	allHTTPRows, err := s.loadAllHTTPRulesForSnapshot(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	allL4Rows, err := s.loadAllL4RulesForSnapshot(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	allL4Rows = filterSyncL4RuleRows(allL4Rows)
	storedRelayRows, err := s.ListRelayListeners(ctx, "")
	if err != nil {
		return Snapshot{}, err
	}
	allRelayRows, excludedRelayIDs := partitionSnapshotRelayRows(storedRelayRows)
	relayFailClosed, err := s.pkiRelayFailClosed(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	if relayFailClosed {
		for _, row := range storedRelayRows {
			if row.ID > 0 {
				excludedRelayIDs[row.ID] = struct{}{}
			}
		}
		relayRows = nil
		allRelayRows = nil
	}
	httpRows = filterHTTPRuleRowsForSnapshot(httpRows, excludedRelayIDs, excludedEgressIDs)
	l4Rows = filterL4RuleRowsForSnapshot(l4Rows, excludedRelayIDs, excludedEgressIDs)
	allHTTPRows = filterHTTPRuleRowsForSnapshot(allHTTPRows, excludedRelayIDs, excludedEgressIDs)
	allL4Rows = filterL4RuleRowsForSnapshot(allL4Rows, excludedRelayIDs, excludedEgressIDs)
	egressRows := filterEgressProfilesForSnapshot(resolvedAgentID, allEgressRows, allHTTPRows, allL4Rows, allRelayRows, !runtimeFiltered)
	egressScopeRevision := egressProfileScopeRevision(resolvedAgentID, allEgressRows, allHTTPRows, allL4Rows, allRelayRows)
	certRows, err := s.ListManagedCertificates(ctx)
	if err != nil {
		return Snapshot{}, err
	}

	versionPolicies, err := s.ListVersionPolicies(ctx)
	if err != nil {
		return Snapshot{}, err
	}

	relevantCertRows := filterManagedCertificatesForAgent(certRows, resolvedAgentID, httpRows, relayRows)
	var agentRevisionState LocalAgentStateRow
	agentConfig := AgentConfig{}
	if resolvedAgentID == s.localAgentID {
		agentRevisionState, err = s.LoadLocalAgentState(ctx)
	} else {
		agentRevisionState, err = s.loadAgentRevisionState(ctx, resolvedAgentID)
	}
	if err != nil {
		return Snapshot{}, err
	}
	agentConfig, _ = s.loadAgentConfigForSnapshot(ctx, resolvedAgentID)
	revisionState := LocalAgentStateRow{
		Version:         agentRevisionState.Version,
		DesiredRevision: maxInt(input.DesiredRevision, agentRevisionState.DesiredRevision),
		CurrentRevision: maxInt(input.CurrentRevision, agentRevisionState.CurrentRevision),
	}

	agentNames, err := s.relayListenerAgentNames(ctx, relayRows)
	if err != nil {
		return Snapshot{}, err
	}

	certBundles, err := s.snapshotCertificateBundles(ctx, relevantCertRows)
	if err != nil {
		return Snapshot{}, err
	}
	certMaterialDomains := make(map[string]bool, len(certBundles))
	for _, bundle := range certBundles {
		certMaterialDomains[strings.TrimSpace(bundle.Domain)] = true
	}
	pkiSecurity, err := s.LoadLatestPKISecuritySnapshot(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	pluginPolicies, err := s.loadAgentPluginPolicies(ctx, resolvedAgentID)
	if err != nil {
		return Snapshot{}, err
	}

	return Snapshot{
		DesiredVersion:      strings.TrimSpace(input.DesiredVersion),
		Revision:            int64(computeDesiredRevision(revisionState, httpRows, l4Rows, relayRows, egressRows, relevantCertRows, egressScopeRevision, highestPluginPolicyRevision(pluginPolicies))),
		VersionPackage:      resolveVersionPackageForPlatform(versionPolicies, input.DesiredVersion, input.Platform),
		AgentConfig:         agentConfig,
		DDNSConfig:          s.loadDDNSConfigForSnapshot(ctx, resolvedAgentID),
		Rules:               snapshotHTTPRules(httpRows, !runtimeFiltered),
		L4Rules:             snapshotL4Rules(l4Rows, !runtimeFiltered),
		RelayListeners:      snapshotRelayListeners(relayRows, agentNames),
		EgressProfiles:      snapshotEgressProfiles(egressRows, !runtimeFiltered),
		Certificates:        certBundles,
		CertificatePolicies: snapshotCertificatePolicies(relevantCertRows, resolvedAgentID, certMaterialDomains, !runtimeFiltered),
		PluginPolicies:      pluginPolicies,
		PKISecurity:         pkiSecurity,
	}, nil
}

func highestPluginPolicyRevision(policies []PluginPolicy) int {
	result := 0
	for _, policy := range policies {
		if policy.Revision > int64(result) {
			if policy.Revision > int64(^uint(0)>>1) {
				return int(^uint(0) >> 1)
			}
			result = int(policy.Revision)
		}
	}
	return result
}

func (s *GormStore) pkiRelayFailClosed(ctx context.Context) (bool, error) {
	if enabled, overridden := ctx.Value(emergencyPKIRelayAvailabilityContextKey{}).(bool); overridden {
		return !enabled, nil
	}
	present, err := s.HasPKICanonicalSchema(ctx)
	if err != nil || !present {
		return false, err
	}
	var settings PKISettingsRow
	err = s.db.WithContext(ctx).First(&settings, PKISettingsSingletonID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return settings.RelayFailClosed, nil
}

type emergencyPKIRelayAvailabilityContextKey struct{}

// WithEmergencyPKIRelayAvailability is restricted to the emergency revision
// coordinator. It projects the one relay-enable revision while the canonical
// fail-closed latch remains set until every exact revision is applied and
// drained.
func WithEmergencyPKIRelayAvailability(ctx context.Context, enabled bool) context.Context {
	return context.WithValue(ctx, emergencyPKIRelayAvailabilityContextKey{}, enabled)
}

func (s *GormStore) loadAgentConfigForSnapshot(ctx context.Context, agentID string) (AgentConfig, bool) {
	var row AgentRow
	if err := s.db.WithContext(ctx).
		Select("id", "outbound_proxy_url", "traffic_stats_interval", "traffic_blocked", "traffic_block_reason").
		Where("id = ?", agentID).
		Limit(1).
		Find(&row).Error; err != nil {
		return AgentConfig{}, false
	}
	if row.ID == "" {
		return AgentConfig{}, false
	}
	normalizeAgentRow(&row)
	return AgentConfig{
		OutboundProxyURL:     strings.TrimSpace(row.OutboundProxyURL),
		TrafficStatsInterval: strings.TrimSpace(row.TrafficStatsInterval),
		TrafficBlocked:       row.TrafficBlocked,
		TrafficBlockReason:   strings.TrimSpace(row.TrafficBlockReason),
	}, true
}

// loadDDNSConfigForSnapshot reads the per-agent DDNS extraction config to
// dispatch. Returns nil when the agent has no DDNS configured (empty/malformed
// JSON), which omits ddns_config from the snapshot entirely. The config never
// carries Cloudflare credentials (R7); those are read from the environment by
// the master DDNS service only.
func (s *GormStore) loadDDNSConfigForSnapshot(ctx context.Context, agentID string) *DDNSConfig {
	var row AgentRow
	if err := s.db.WithContext(ctx).
		Select("id", "ddns_config").
		Where("id = ?", agentID).
		Limit(1).
		Find(&row).Error; err != nil {
		return nil
	}
	if row.ID == "" {
		return nil
	}
	normalizeAgentRow(&row)
	raw := strings.TrimSpace(row.DdnsConfigJSON)
	if raw == "" {
		return nil
	}
	var cfg DDNSConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return nil
	}
	if strings.TrimSpace(cfg.Domain) == "" && !cfg.IPv4.Enabled && !cfg.IPv6.Enabled {
		return nil
	}
	cfg.Domain = strings.TrimSpace(cfg.Domain)
	return &cfg
}

func (s *GormStore) ListL4Rules(ctx context.Context, agentID string) ([]L4RuleRow, error) {
	if agentID == "" {
		agentID = s.localAgentID
	}

	var rules []L4RuleRow
	if err := s.db.WithContext(ctx).
		Where("agent_id = ?", agentID).
		Order("id").
		Find(&rules).Error; err != nil {
		return nil, err
	}
	for i := range rules {
		normalizeL4RuleRow(&rules[i])
	}
	return rules, nil
}

func (s *GormStore) GetL4Rule(ctx context.Context, agentID string, id int) (L4RuleRow, bool, error) {
	if agentID == "" {
		agentID = s.localAgentID
	}

	var rule L4RuleRow
	err := s.db.WithContext(ctx).
		Where("agent_id = ? AND id = ?", agentID, id).
		First(&rule).Error
	if err == nil {
		normalizeL4RuleRow(&rule)
		return rule, true, nil
	}
	if err == gorm.ErrRecordNotFound {
		return L4RuleRow{}, false, nil
	}
	return L4RuleRow{}, false, err
}

func (s *GormStore) ListVersionPolicies(ctx context.Context) ([]VersionPolicyRow, error) {
	var policies []VersionPolicyRow
	if err := s.db.WithContext(ctx).Order("id").Find(&policies).Error; err != nil {
		return nil, err
	}
	for i := range policies {
		normalizeVersionPolicyRow(&policies[i])
	}
	return policies, nil
}

func (s *GormStore) ListRelayListeners(ctx context.Context, agentID string) ([]RelayListenerRow, error) {
	query := s.db.WithContext(ctx).Order("id")
	if strings.TrimSpace(agentID) != "" {
		query = query.Where("agent_id = ?", agentID)
	}

	var listeners []RelayListenerRow
	if err := query.Find(&listeners).Error; err != nil {
		return nil, err
	}
	for i := range listeners {
		normalizeRelayListenerRow(&listeners[i])
	}
	return listeners, nil
}

func (s *GormStore) ListEgressProfiles(ctx context.Context) ([]EgressProfileRow, error) {
	var profiles []EgressProfileRow
	if err := s.db.WithContext(ctx).
		Order("id").
		Find(&profiles).Error; err != nil {
		return nil, err
	}
	for i := range profiles {
		normalizeEgressProfileRow(&profiles[i])
	}
	return profiles, nil
}

func (s *GormStore) EgressProfileReferences(ctx context.Context, profileID int) ([]EgressProfileReference, error) {
	if profileID <= 0 {
		return nil, nil
	}
	var httpRows []HTTPRuleRow
	if err := s.db.WithContext(ctx).
		Select("id", "agent_id").
		Where("egress_profile_id = ?", profileID).
		Order("agent_id, id").
		Find(&httpRows).Error; err != nil {
		return nil, err
	}
	var l4Rows []L4RuleRow
	if err := s.db.WithContext(ctx).
		Select("id", "agent_id").
		Where("egress_profile_id = ?", profileID).
		Order("agent_id, id").
		Find(&l4Rows).Error; err != nil {
		return nil, err
	}
	references := make([]EgressProfileReference, 0, len(httpRows)+len(l4Rows))
	for _, row := range httpRows {
		references = append(references, EgressProfileReference{
			Kind:    "http",
			AgentID: row.AgentID,
			ID:      row.ID,
		})
	}
	for _, row := range l4Rows {
		references = append(references, EgressProfileReference{
			Kind:    "l4",
			AgentID: row.AgentID,
			ID:      row.ID,
		})
	}
	return references, nil
}

func (s *GormStore) ListManagedCertificates(ctx context.Context) ([]ManagedCertificateRow, error) {
	var certs []ManagedCertificateRow
	if err := s.db.WithContext(ctx).Order("id").Find(&certs).Error; err != nil {
		return nil, err
	}
	for i := range certs {
		normalizeManagedCertificateRow(&certs[i])
	}
	return certs, nil
}

func (s *GormStore) SaveLocalRuntimeState(ctx context.Context, agentID string, runtimeState RuntimeState) error {
	_ = s.resolveAgentID(agentID)
	stateJSON, err := json.Marshal(runtimeState)
	if err != nil {
		return err
	}
	stateJSONString := string(stateJSON)
	outcome := NormalizeLocalApplyOutcome(runtimeState)

	return s.writeTransaction(ctx, func(tx *gorm.DB) error {
		var currentState LocalAgentStateRow
		err := tx.WithContext(ctx).
			Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ?", 1).
			First(&currentState).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			currentState = LocalAgentStateRow{ID: 1, LastApplyStatus: "success"}
		} else if err != nil {
			return err
		}
		normalizeLocalAgentStateRow(&currentState)

		lastApplyStatus := outcome.Status
		if lastApplyStatus == "" {
			lastApplyStatus = currentState.LastApplyStatus
		}
		lastApplyRevision := outcome.Revision
		if lastApplyRevision <= 0 {
			lastApplyRevision = runtimeState.CurrentRevision
		}
		desiredRevision := currentState.DesiredRevision
		lastApplyRevisionInt := boundedIntFromInt64(lastApplyRevision)
		if lastApplyStatus == "success" {
			desiredRevision = maxInt(desiredRevision, lastApplyRevisionInt)
		}
		row := LocalAgentStateRow{
			ID:                 1,
			Version:            currentState.Version,
			DesiredRevision:    desiredRevision,
			CurrentRevision:    boundedIntFromInt64(runtimeState.CurrentRevision),
			LastApplyRevision:  lastApplyRevisionInt,
			LastApplyStatus:    lastApplyStatus,
			LastApplyMessage:   outcome.Message,
			DesiredVersion:     currentState.DesiredVersion,
			PKISecurityAckJSON: currentState.PKISecurityAckJSON,
			PKISecurityAckAt:   currentState.PKISecurityAckAt,
		}
		normalizeLocalAgentStateRow(&row)

		if localAgentStateRowsEqual(currentState, row) {
			var existingMeta MetaRow
			err := tx.WithContext(ctx).
				Where("key = ?", localRuntimeStateMetaKey).
				Limit(1).
				Find(&existingMeta).Error
			if err != nil {
				return err
			}
			if existingMeta.Key == localRuntimeStateMetaKey && strings.TrimSpace(existingMeta.Value) == stateJSONString {
				return nil
			}
		}
		if err := tx.
			Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "id"}},
				UpdateAll: true,
			}).
			Create(&row).Error; err != nil {
			return err
		}
		return tx.
			Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "key"}},
				UpdateAll: true,
			}).
			Create(&MetaRow{
				Key:   localRuntimeStateMetaKey,
				Value: stateJSONString,
			}).Error
	})
}

func localAgentStateRowsEqual(a, b LocalAgentStateRow) bool {
	normalizeLocalAgentStateRow(&a)
	normalizeLocalAgentStateRow(&b)
	acknowledgementTimesEqual := a.PKISecurityAckAt == nil && b.PKISecurityAckAt == nil ||
		a.PKISecurityAckAt != nil && b.PKISecurityAckAt != nil && a.PKISecurityAckAt.Equal(*b.PKISecurityAckAt)
	return a.ID == b.ID &&
		a.Version == b.Version &&
		a.DesiredRevision == b.DesiredRevision &&
		a.CurrentRevision == b.CurrentRevision &&
		a.LastApplyRevision == b.LastApplyRevision &&
		a.LastApplyStatus == b.LastApplyStatus &&
		a.LastApplyMessage == b.LastApplyMessage &&
		a.DesiredVersion == b.DesiredVersion &&
		a.PKISecurityAckJSON == b.PKISecurityAckJSON &&
		acknowledgementTimesEqual
}

func (s *GormStore) SaveAgent(ctx context.Context, row AgentRow) error {
	normalizeAgentRow(&row)
	return s.db.WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "id"}},
			UpdateAll: true,
		}).
		Create(&row).Error
}

// SaveAuthenticatedAgentRegistration updates only registration metadata and
// only while the credential authenticated by the caller is still current. It
// cannot recreate a token that a concurrent revoke has cleared.
func (s *GormStore) SaveAuthenticatedAgentRegistration(ctx context.Context, expectedToken string, row AgentRow) error {
	normalizeAgentRow(&row)
	expectedToken = strings.TrimSpace(expectedToken)
	if row.ID == "" || expectedToken == "" || row.AgentToken != expectedToken {
		return ErrAgentControlTokenChanged
	}
	result := s.db.WithContext(ctx).
		Model(&AgentRow{}).
		Where("id = ? AND agent_token = ? AND agent_token <> ''", row.ID, expectedToken).
		Updates(map[string]any{
			"name":         row.Name,
			"agent_url":    row.AgentURL,
			"version":      row.Version,
			"platform":     row.Platform,
			"tags":         row.TagsJSON,
			"capabilities": row.CapabilitiesJSON,
			"mode":         row.Mode,
			"is_local":     false,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrAgentControlTokenChanged
	}
	return nil
}

// UpdateDdnsStatusColumn writes only the ddns_status column for agentID. It is a
// targeted update, intentionally NOT a full-row upsert, so the DDNS reconciler
// (which may hold a stale row across a slow Cloudflare call) cannot clobber
// concurrent full-row writes from heartbeats or admin edits. A missing row is a
// no-op. Column-driven Update is used so an empty value still writes.
func (s *GormStore) UpdateDdnsStatusColumn(ctx context.Context, agentID, statusJSON string) error {
	if strings.TrimSpace(agentID) == "" {
		return nil
	}
	return s.db.WithContext(ctx).
		Model(&AgentRow{}).
		Where("id = ?", agentID).
		Update("ddns_status", statusJSON).Error
}

func (s *GormStore) SaveAgentHeartbeat(ctx context.Context, row AgentRow) error {
	normalizeAgentRow(&row)
	var current AgentRow
	if err := s.db.WithContext(ctx).
		Select(
			"id",
			"version",
			"platform",
			"runtime_package_version",
			"runtime_package_platform",
			"runtime_package_arch",
			"runtime_package_sha256",
			"current_revision",
			"last_apply_revision",
			"last_apply_status",
			"last_apply_message",
			"last_reported_stats",
			"last_seen_ip",
			"last_seen_ipv4",
			"last_seen_ipv6",
		).
		Where("id = ?", row.ID).
		Limit(1).
		Find(&current).Error; err != nil {
		return err
	}
	if current.ID == "" {
		return s.SaveAgent(ctx, row)
	}
	normalizeAgentRow(&current)

	updates := map[string]any{
		"last_seen_at": row.LastSeenAt,
	}
	if current.Version != row.Version {
		updates["version"] = row.Version
	}
	if current.Platform != row.Platform {
		updates["platform"] = row.Platform
	}
	if current.RuntimePackageVersion != row.RuntimePackageVersion {
		updates["runtime_package_version"] = row.RuntimePackageVersion
	}
	if current.RuntimePackagePlatform != row.RuntimePackagePlatform {
		updates["runtime_package_platform"] = row.RuntimePackagePlatform
	}
	if current.RuntimePackageArch != row.RuntimePackageArch {
		updates["runtime_package_arch"] = row.RuntimePackageArch
	}
	if current.RuntimePackageSHA256 != row.RuntimePackageSHA256 {
		updates["runtime_package_sha256"] = row.RuntimePackageSHA256
	}
	if current.CurrentRevision != row.CurrentRevision {
		updates["current_revision"] = row.CurrentRevision
	}
	if current.LastApplyRevision != row.LastApplyRevision {
		updates["last_apply_revision"] = row.LastApplyRevision
	}
	if current.LastApplyStatus != row.LastApplyStatus {
		updates["last_apply_status"] = row.LastApplyStatus
	}
	if current.LastApplyMessage != row.LastApplyMessage {
		updates["last_apply_message"] = row.LastApplyMessage
	}
	if current.LastReportedStatsJSON != row.LastReportedStatsJSON {
		updates["last_reported_stats"] = row.LastReportedStatsJSON
	}
	if current.LastSeenIP != row.LastSeenIP {
		updates["last_seen_ip"] = row.LastSeenIP
	}
	// IPv4/IPv6 are reported by the agent (distinct from the server-derived
	// LastSeenIP fallback). Only a non-empty report overwrites the stored value
	// so a transiently-unreported family does not clobber the last known address.
	if row.LastSeenIPv4 != "" {
		updates["last_seen_ipv4"] = row.LastSeenIPv4
	}
	if row.LastSeenIPv6 != "" {
		updates["last_seen_ipv6"] = row.LastSeenIPv6
	}

	return s.db.WithContext(ctx).
		Model(&AgentRow{}).
		Where("id = ?", row.ID).
		Updates(updates).Error
}

func (s *GormStore) DeleteAgent(ctx context.Context, agentID string) error {
	_, _, err := s.DeleteAgentWithAssociations(ctx, agentID)
	return err
}

// DeleteAgentWithAssociations performs the final PKI tombstone guard, all
// database-owned association cleanup, and the AgentRow hard delete in one
// write transaction. Callers may clean certificate material only after this
// method commits successfully.
func (s *GormStore) DeleteAgentWithAssociations(ctx context.Context, agentID string) ([]ManagedCertificateRow, []ManagedCertificateRow, error) {
	agentID = strings.TrimSpace(agentID)
	var originalCertificates []ManagedCertificateRow
	var nextCertificates []ManagedCertificateRow
	err := s.writeTransaction(ctx, func(tx *gorm.DB) error {
		now := time.Now().UTC()
		if err := requireAgentPKIRevokedForDeletion(ctx, tx, agentID); err != nil {
			return err
		}
		if err := requireAgentRelayListenersUnreferenced(tx, agentID); err != nil {
			return err
		}
		if err := tx.Order("id").Find(&originalCertificates).Error; err != nil {
			return err
		}
		resourceKeys := [][2]string{{"agent", agentID}}
		for _, spec := range []struct {
			model any
			kind  string
		}{
			{model: &HTTPRuleRow{}, kind: "http_rule"},
			{model: &L4RuleRow{}, kind: "l4_rule"},
			{model: &RelayListenerRow{}, kind: "relay_listener"},
		} {
			var ids []int
			if err := tx.Model(spec.model).Where("agent_id = ?", agentID).Pluck("id", &ids).Error; err != nil {
				return err
			}
			for _, id := range ids {
				resourceKeys = append(resourceKeys, [2]string{spec.kind, agentID + ":" + strconv.Itoa(id)})
			}
		}
		var childBindings []ResourceBindingRow
		if err := tx.Where("parent_resource_kind = ? AND parent_resource_id = ?", "agent", agentID).Find(&childBindings).Error; err != nil {
			return err
		}
		for _, binding := range childBindings {
			resourceKeys = append(resourceKeys, [2]string{binding.ResourceKind, binding.ResourceID})
		}
		changedCertificates := make([]ManagedCertificateRow, 0)
		deletedCertificateIDs := make([]int, 0)
		nextCertificates = make([]ManagedCertificateRow, 0, len(originalCertificates))
		for _, row := range originalCertificates {
			targets, err := decodeAgentIDList(row.TargetAgentIDs)
			if err != nil {
				return err
			}
			filtered := targets[:0]
			for _, target := range targets {
				if target != agentID {
					filtered = append(filtered, target)
				}
			}
			if len(filtered) == len(targets) {
				nextCertificates = append(nextCertificates, row)
				continue
			}
			reports := make(map[string]json.RawMessage)
			if strings.TrimSpace(row.AgentReports) != "" && row.AgentReports != "{}" {
				if err := json.Unmarshal([]byte(row.AgentReports), &reports); err != nil {
					return fmt.Errorf("decode managed certificate agent reports: %w", err)
				}
			}
			delete(reports, agentID)
			if len(filtered) == 0 {
				if err := tx.Where("id = ?", row.ID).Delete(&ManagedCertificateRow{}).Error; err != nil {
					return err
				}
				deletedCertificateIDs = append(deletedCertificateIDs, row.ID)
				continue
			}
			encodedTargets, err := json.Marshal(filtered)
			if err != nil {
				return err
			}
			encodedReports, err := json.Marshal(reports)
			if err != nil {
				return err
			}
			row.TargetAgentIDs = string(encodedTargets)
			row.AgentReports = string(encodedReports)
			if err := tx.Model(&ManagedCertificateRow{}).Where("id = ?", row.ID).Updates(map[string]any{
				"target_agent_ids": row.TargetAgentIDs,
				"agent_reports":    row.AgentReports,
			}).Error; err != nil {
				return err
			}
			changedCertificates = append(changedCertificates, row)
			nextCertificates = append(nextCertificates, row)
		}
		for _, certificateID := range deletedCertificateIDs {
			resourceKeys = append(resourceKeys, [2]string{"certificate", strconv.Itoa(certificateID)})
		}
		for _, certificate := range changedCertificates {
			resourceID := strconv.Itoa(certificate.ID)
			groupID, err := managedCertificateResourceGroupTx(tx, certificate, "", "")
			if err != nil {
				return err
			}
			binding := ResourceBindingRow{ID: securityID("res"), ResourceKind: "certificate", ResourceID: resourceID, ResourceGroupID: groupID, UpdatedAt: now}
			if err := tx.Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "resource_kind"}, {Name: "resource_id"}},
				DoUpdates: clause.AssignmentColumns([]string{"resource_group_id", "updated_at"}),
			}).Create(&binding).Error; err != nil {
				return err
			}
			if err := tx.Where("resource_kind = ? AND resource_id = ?", "certificate", resourceID).Delete(&QuotaAllocationRow{}).Error; err != nil {
				return err
			}
		}
		if err := tx.Where("parent_resource_kind = ? AND parent_resource_id = ?", "agent", agentID).Delete(&ResourceBindingRow{}).Error; err != nil {
			return err
		}
		for _, key := range resourceKeys {
			if err := tx.Where("resource_kind = ? AND resource_id = ?", key[0], key[1]).Delete(&ResourceBindingRow{}).Error; err != nil {
				return err
			}
			if err := tx.Where("resource_kind = ? AND resource_id = ?", key[0], key[1]).Delete(&QuotaAllocationRow{}).Error; err != nil {
				return err
			}
		}
		if err := recomputeCountQuotaUsageTx(tx, now); err != nil {
			return err
		}
		if err := removeAgentBandwidthTx(tx, agentID, now); err != nil {
			return err
		}
		if err := tx.Where("agent_id = ?", agentID).Delete(&HTTPRuleRow{}).Error; err != nil {
			return err
		}
		if err := tx.Where("agent_id = ?", agentID).Delete(&L4RuleRow{}).Error; err != nil {
			return err
		}
		if err := tx.Where("agent_id = ?", agentID).Delete(&RelayListenerRow{}).Error; err != nil {
			return err
		}
		if err := retireCoordinatorAgentTx(tx, agentID, now); err != nil {
			return err
		}
		if _, err := s.deleteTrafficByAgentTx(tx, agentID); err != nil {
			return err
		}
		return tx.Where("id = ?", agentID).Delete(&AgentRow{}).Error
	})
	if err != nil {
		return nil, nil, err
	}
	return originalCertificates, nextCertificates, nil
}

func requireAgentRelayListenersUnreferenced(tx *gorm.DB, agentID string) error {
	var listenerIDs []int
	if err := tx.Model(&RelayListenerRow{}).
		Where("agent_id = ?", agentID).
		Pluck("id", &listenerIDs).Error; err != nil {
		return err
	}
	if len(listenerIDs) == 0 {
		return nil
	}
	listenerSet := make(map[int]struct{}, len(listenerIDs))
	for _, listenerID := range listenerIDs {
		listenerSet[listenerID] = struct{}{}
	}
	check := func(ruleType string, ruleID int, ruleAgentID, relayChainJSON, relayLayersJSON string) error {
		references := append(parseIntSlice(relayChainJSON), flattenIntLayers(parseIntLayers(relayLayersJSON))...)
		for _, listenerID := range references {
			if _, referenced := listenerSet[listenerID]; referenced {
				return fmt.Errorf("%w: listener %d is referenced by %s rule #%d on agent %s", ErrAgentRelayListenerReferenced, listenerID, ruleType, ruleID, ruleAgentID)
			}
		}
		return nil
	}
	var httpRows []HTTPRuleRow
	if err := tx.Select("id", "agent_id", "relay_chain", "relay_layers").Where("agent_id <> ?", agentID).Find(&httpRows).Error; err != nil {
		return err
	}
	for _, row := range httpRows {
		if err := check("HTTP", row.ID, row.AgentID, row.RelayChainJSON, row.RelayLayersJSON); err != nil {
			return err
		}
	}
	var l4Rows []L4RuleRow
	if err := tx.Select("id", "agent_id", "relay_chain", "relay_layers").Where("agent_id <> ?", agentID).Find(&l4Rows).Error; err != nil {
		return err
	}
	for _, row := range l4Rows {
		if err := check("L4", row.ID, row.AgentID, row.RelayChainJSON, row.RelayLayersJSON); err != nil {
			return err
		}
	}
	return nil
}

func decodeAgentIDList(encoded string) ([]string, error) {
	if strings.TrimSpace(encoded) == "" {
		return []string{}, nil
	}
	var values []string
	if err := json.Unmarshal([]byte(encoded), &values); err != nil {
		return nil, fmt.Errorf("decode managed certificate target agents: %w", err)
	}
	return values, nil
}

func (s *GormStore) RequireAgentPKIRevokedForDeletion(ctx context.Context, agentID string) error {
	return requireAgentPKIRevokedForDeletion(ctx, s.db.WithContext(ctx), strings.TrimSpace(agentID))
}

func requireAgentPKIRevokedForDeletion(ctx context.Context, db *gorm.DB, agentID string) error {
	if !db.Migrator().HasTable(&PKIIdentityRow{}) {
		return nil
	}
	var rows []PKIIdentityRow
	if err := db.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("agent_id = ? AND state <> ?", agentID, PKIIdentityStateRevoked).
		Find(&rows).Error; err != nil {
		return err
	}
	if len(rows) != 0 {
		return ErrPKIAgentIdentityNotRevoked
	}
	return nil
}

func (s *GormStore) SaveHTTPRules(ctx context.Context, agentID string, rules []HTTPRuleRow) error {
	if agentID == "" {
		agentID = s.localAgentID
	}

	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("agent_id = ?", agentID).Delete(&HTTPRuleRow{}).Error; err != nil {
			return err
		}

		if len(rules) == 0 {
			return nil
		}

		rows := make([]HTTPRuleRow, 0, len(rules))
		for _, row := range rules {
			row.AgentID = agentID
			normalizeHTTPRuleRow(&row)
			rows = append(rows, row)
		}
		return tx.Create(&rows).Error
	})
}

func (s *GormStore) SaveL4Rules(ctx context.Context, agentID string, rules []L4RuleRow) error {
	if agentID == "" {
		agentID = s.localAgentID
	}

	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("agent_id = ?", agentID).Delete(&L4RuleRow{}).Error; err != nil {
			return err
		}

		if len(rules) == 0 {
			return nil
		}

		rows := make([]L4RuleRow, 0, len(rules))
		for _, row := range rules {
			row.AgentID = agentID
			normalizeL4RuleRow(&row)
			rows = append(rows, row)
		}
		return tx.Create(&rows).Error
	})
}

func (s *GormStore) SaveVersionPolicies(ctx context.Context, policies []VersionPolicyRow) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(&VersionPolicyRow{}).Error; err != nil {
			return err
		}

		if len(policies) == 0 {
			return nil
		}

		rows := make([]VersionPolicyRow, 0, len(policies))
		for _, row := range policies {
			normalizeVersionPolicyRow(&row)
			rows = append(rows, row)
		}
		return tx.Create(&rows).Error
	})
}

func (s *GormStore) SaveRelayListeners(ctx context.Context, agentID string, listeners []RelayListenerRow) error {
	if agentID == "" {
		agentID = s.localAgentID
	}

	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("agent_id = ?", agentID).Delete(&RelayListenerRow{}).Error; err != nil {
			return err
		}

		if len(listeners) == 0 {
			return nil
		}

		rows := make([]RelayListenerRow, 0, len(listeners))
		for _, row := range listeners {
			row.AgentID = agentID
			normalizeRelayListenerRow(&row)
			rows = append(rows, row)
		}
		return tx.Create(&rows).Error
	})
}

func (s *GormStore) SaveEgressProfiles(ctx context.Context, profiles []EgressProfileRow) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(&EgressProfileRow{}).Error; err != nil {
			return err
		}

		if len(profiles) == 0 {
			return nil
		}

		rows := make([]map[string]any, 0, len(profiles))
		for _, row := range profiles {
			rows = append(rows, egressProfileRowPayload(row))
		}
		return tx.Model(&EgressProfileRow{}).Create(&rows).Error
	})
}

func (s *GormStore) SaveManagedCertificates(ctx context.Context, certs []ManagedCertificateRow) error {
	return s.writeTransaction(ctx, func(tx *gorm.DB) error {
		var existing []ManagedCertificateRow
		if err := managedCertificatePointerSnapshotQuery(tx).
			Find(&existing).Error; err != nil {
			return err
		}
		return replaceManagedCertificatesInTransaction(tx, existing, certs)
	})
}

// UpdateManagedCertificates locks the current rows and applies update inside
// the same transaction that persists them. This keeps concurrent heartbeat
// report merges from overwriting acknowledgements read by another handler.
func (s *GormStore) UpdateManagedCertificates(ctx context.Context, update func([]ManagedCertificateRow) ([]ManagedCertificateRow, bool, error)) error {
	if update == nil {
		return nil
	}
	return s.writeTransaction(ctx, func(tx *gorm.DB) error {
		var existing []ManagedCertificateRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Order("id").Find(&existing).Error; err != nil {
			return err
		}
		for index := range existing {
			normalizeManagedCertificateRow(&existing[index])
		}
		next, changed, err := update(append([]ManagedCertificateRow(nil), existing...))
		if err != nil || !changed {
			return err
		}
		return updateManagedCertificatesInTransaction(tx, existing, next)
	})
}

func updateManagedCertificatesInTransaction(tx *gorm.DB, existing, next []ManagedCertificateRow) error {
	if len(next) != len(existing) {
		return fmt.Errorf("managed certificate update changed row count from %d to %d", len(existing), len(next))
	}
	currentByID := make(map[int]ManagedCertificateRow, len(existing))
	for _, row := range existing {
		currentByID[row.ID] = row
	}
	seen := make(map[int]struct{}, len(next))
	for _, row := range next {
		current, ok := currentByID[row.ID]
		if !ok || strings.TrimSpace(row.Domain) != strings.TrimSpace(current.Domain) {
			return fmt.Errorf("managed certificate update changed identity for id %d", row.ID)
		}
		if _, duplicate := seen[row.ID]; duplicate {
			return fmt.Errorf("managed certificate update duplicated id %d", row.ID)
		}
		seen[row.ID] = struct{}{}
		normalizeManagedCertificateRow(&row)
		row.ActiveGenerationID = current.ActiveGenerationID
		row.PendingGenerationID = ""
		if managedCertificateGenerationOwnershipMatches(current, row) {
			row.PendingGenerationID = current.PendingGenerationID
		} else if pendingID := strings.TrimSpace(current.PendingGenerationID); pendingID != "" {
			if err := tx.Model(&ManagedCertificateGenerationRow{}).
				Where("id = ? AND domain = ? AND state = ?", pendingID, strings.TrimSpace(current.Domain), ManagedCertificateGenerationStatePending).
				Update("state", managedCertificateGenerationStateInvalid).Error; err != nil {
				return err
			}
		}
		if row == current {
			continue
		}
		result := tx.Model(&ManagedCertificateRow{}).
			Where("id = ?", row.ID).
			Select("*").
			Updates(&row)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return fmt.Errorf("managed certificate update affected %d rows for id %d", result.RowsAffected, row.ID)
		}
	}
	return nil
}

func replaceManagedCertificatesInTransaction(tx *gorm.DB, existing, certs []ManagedCertificateRow) error {
	type managedCertificateIdentity struct {
		id     int
		domain string
	}
	internalPointers := make(map[managedCertificateIdentity]ManagedCertificateRow, len(existing))
	for _, row := range existing {
		internalPointers[managedCertificateIdentity{id: row.ID, domain: strings.TrimSpace(row.Domain)}] = row
	}

	if err := tx.Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(&ManagedCertificateRow{}).Error; err != nil {
		return err
	}
	if len(certs) == 0 {
		return nil
	}
	rows := make([]ManagedCertificateRow, 0, len(certs))
	for _, row := range certs {
		normalizeManagedCertificateRow(&row)
		row.ActiveGenerationID = ""
		row.PendingGenerationID = ""
		if current, ok := internalPointers[managedCertificateIdentity{id: row.ID, domain: strings.TrimSpace(row.Domain)}]; ok {
			row.ActiveGenerationID = current.ActiveGenerationID
			if managedCertificateGenerationOwnershipMatches(current, row) {
				row.PendingGenerationID = current.PendingGenerationID
			} else if pendingID := strings.TrimSpace(current.PendingGenerationID); pendingID != "" {
				if err := tx.Model(&ManagedCertificateGenerationRow{}).
					Where("id = ? AND domain = ? AND state = ?", pendingID, strings.TrimSpace(current.Domain), ManagedCertificateGenerationStatePending).
					Update("state", managedCertificateGenerationStateInvalid).Error; err != nil {
					return err
				}
			}
		}
		rows = append(rows, row)
	}
	return tx.Create(&rows).Error
}

func managedCertificatePointerSnapshotQuery(tx *gorm.DB) *gorm.DB {
	return tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Select("id", "domain", "issuer_mode", "certificate_type", "active_generation_id", "pending_generation_id")
}

func managedCertificateGenerationOwnershipMatches(current, next ManagedCertificateRow) bool {
	return defaultString(current.IssuerMode, "master_cf_dns") == defaultString(next.IssuerMode, "master_cf_dns") &&
		defaultString(current.CertificateType, "acme") == defaultString(next.CertificateType, "acme")
}

func (s *GormStore) CleanupManagedCertificateMaterial(ctx context.Context, previous []ManagedCertificateRow, next []ManagedCertificateRow) error {
	nextDomains := managedCertificateDomainSet(next)
	nextLegacyDomains := managedCertificateLegacyDomainSet(next)
	baseDir := filepath.Join(s.dataRoot, "managed_certificates")
	processed := make(map[string]struct{}, len(previous))
	reconcileLegacyKeys := make(map[string]struct{})
	for _, previousRow := range previous {
		domain, err := normalizeManagedCertificateGenerationDomain(previousRow.Domain)
		if err != nil {
			return err
		}
		domainKey := managedCertificateDomainStorageKey(domain)
		if _, ok := processed[domainKey]; ok {
			continue
		}
		processed[domainKey] = struct{}{}
		if _, ok := nextDomains[domainKey]; ok {
			continue
		}
		unlock := s.lockManagedCertificateDomain(domain)
		var certificate ManagedCertificateRow
		err = s.db.WithContext(ctx).
			Select("id", "active_generation_id", "pending_generation_id").
			Where("domain = ?", domain).
			First(&certificate).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			unlock()
			return err
		}
		if err == nil && (strings.TrimSpace(certificate.ActiveGenerationID) != "" || strings.TrimSpace(certificate.PendingGenerationID) != "") {
			unlock()
			continue
		}
		certDir := managedCertificateDirectory(baseDir, domain)
		if err := removeManagedCertificateDirectoryPath(certDir); err != nil {
			unlock()
			return err
		}
		legacyKey := managedCertificateLegacyDomainKey(domain)
		if _, retained := nextLegacyDomains[legacyKey]; !retained {
			legacyDir := legacyManagedCertificateDirectory(baseDir, domain)
			owned, ownershipErr := managedCertificateLegacyDirectoryOwnedBy(legacyDir, domain)
			if ownershipErr != nil {
				unlock()
				return ownershipErr
			}
			if owned {
				if err := removeManagedCertificateDirectoryPath(legacyDir); err != nil {
					unlock()
					return err
				}
			}
		} else {
			reconcileLegacyKeys[legacyKey] = struct{}{}
		}
		if err := syncManagedCertificateDirectoryIfPresent(baseDir); err != nil {
			unlock()
			return err
		}
		if err := s.writeTransaction(ctx, func(tx *gorm.DB) error {
			return tx.Where("domain = ?", domain).Delete(&ManagedCertificateGenerationRow{}).Error
		}); err != nil {
			unlock()
			return err
		}
		unlock()
	}
	return s.reconcileManagedCertificateLegacyProjectionOwners(ctx, next, reconcileLegacyKeys)
}

func (s *GormStore) reconcileManagedCertificateLegacyProjectionOwners(
	ctx context.Context,
	rows []ManagedCertificateRow,
	keys map[string]struct{},
) error {
	domainsByKey := make(map[string][]string, len(keys))
	seenDomains := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		domain, err := normalizeManagedCertificateGenerationDomain(row.Domain)
		if err != nil {
			return err
		}
		key := managedCertificateLegacyDomainKey(domain)
		if _, ok := keys[key]; !ok {
			continue
		}
		domainKey := managedCertificateDomainStorageKey(domain)
		if _, ok := seenDomains[domainKey]; ok {
			continue
		}
		seenDomains[domainKey] = struct{}{}
		domainsByKey[key] = append(domainsByKey[key], domain)
	}
	for _, domains := range domainsByKey {
		domain := domains[0]
		unlock := s.lockManagedCertificateDomain(domain)
		if len(domains) != 1 {
			err := s.retireManagedCertificateLegacyProjection(domain)
			unlock()
			if err != nil {
				return err
			}
			continue
		}
		active, ok, err := s.loadActiveManagedCertificateGenerationLocked(ctx, domain)
		if err == nil && ok {
			err = s.writeManagedCertificateLegacyProjection(domain, active.Material)
		} else if err == nil {
			err = s.retireManagedCertificateLegacyProjection(domain)
		}
		unlock()
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *GormStore) LoadManagedCertificateMaterial(ctx context.Context, domain string) (ManagedCertificateBundle, bool, error) {
	domain, err := normalizeManagedCertificateGenerationDomain(domain)
	if err != nil {
		return ManagedCertificateBundle{}, false, err
	}
	if s.transactionScoped {
		// Revision mutations already hold SQLite's write lock. The helper may
		// inspect or repair projection state, but it must not wait for the domain
		// lock and invert generation maintenance's lock order.
		return s.loadManagedCertificateMaterialLocked(ctx, domain)
	}
	unlock := s.lockManagedCertificateDomain(domain)
	defer unlock()
	return s.loadManagedCertificateMaterialLocked(ctx, domain)
}

func (s *GormStore) loadManagedCertificateMaterialLocked(ctx context.Context, domain string) (ManagedCertificateBundle, bool, error) {
	if err := s.migrateManagedCertificateLegacyDirectoryLocked(domain); err != nil {
		return ManagedCertificateBundle{}, false, err
	}
	var certificate ManagedCertificateRow
	if err := s.db.WithContext(ctx).Where("domain = ?", domain).First(&certificate).Error; err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return ManagedCertificateBundle{}, false, err
		}
		material, ok, err := s.readManagedCertificateMaterialSecure(domain)
		if err != nil {
			return ManagedCertificateBundle{}, false, err
		}
		if !ok {
			return ManagedCertificateBundle{}, false, nil
		}
		return ManagedCertificateBundle{Domain: domain, CertPEM: material.CertPEM, KeyPEM: material.KeyPEM}, true, nil
	}

	active, ok, err := s.loadActiveManagedCertificateGenerationLocked(ctx, domain)
	if err != nil {
		return ManagedCertificateBundle{}, false, err
	}
	if ok {
		return active.Material, true, nil
	}
	material, ok, err := s.readManagedCertificateMaterialSecure(domain)
	if err != nil {
		return ManagedCertificateBundle{}, false, err
	}
	if !ok {
		return ManagedCertificateBundle{}, false, nil
	}
	var nonPendingCount int64
	if err := s.db.WithContext(ctx).Model(&ManagedCertificateGenerationRow{}).
		Where("domain = ? AND state <> ?", domain, ManagedCertificateGenerationStatePending).
		Count(&nonPendingCount).Error; err != nil {
		return ManagedCertificateBundle{}, false, err
	}
	if nonPendingCount != 0 {
		return ManagedCertificateBundle{}, false, nil
	}
	bundle := ManagedCertificateBundle{Domain: domain, CertPEM: material.CertPEM, KeyPEM: material.KeyPEM}
	imported, err := s.importLegacyManagedCertificateGenerationLocked(ctx, domain, bundle)
	if err != nil {
		return ManagedCertificateBundle{}, false, err
	}
	return imported.Material, true, nil
}

func (s *GormStore) SaveManagedCertificateMaterial(ctx context.Context, domain string, bundle ManagedCertificateBundle) error {
	domain, err := normalizeManagedCertificateGenerationDomain(domain)
	if err != nil {
		return err
	}
	if s.transactionScoped {
		// See LoadManagedCertificateMaterial: the surrounding revision mutation
		// owns the database write lock, so acquiring the domain lock here would
		// create the same SQLite/domain AB/BA cycle. Defer collection until that
		// transaction commits so a rollback cannot remove retained material.
		err := s.saveManagedCertificateMaterialLocked(ctx, domain, bundle)
		if err == nil && s.certificateGCDomains != nil {
			s.certificateGCDomains[domain] = struct{}{}
		}
		return err
	}
	var saveErr error
	func() {
		unlock := s.lockManagedCertificateDomain(domain)
		defer unlock()
		saveErr = s.saveManagedCertificateMaterialLocked(ctx, domain, bundle)
	}()
	if saveErr != nil {
		return saveErr
	}
	// Promotion is already durable. Collection remains best effort and is
	// retried by the next successful material save.
	s.garbageCollectManagedCertificateGenerationDomains(ctx, map[string]struct{}{domain: struct{}{}})
	return nil
}

func (s *GormStore) saveManagedCertificateMaterialLocked(ctx context.Context, domain string, bundle ManagedCertificateBundle) error {
	if err := s.migrateManagedCertificateLegacyDirectoryLocked(domain); err != nil {
		return err
	}
	bundle.Domain = domain
	var certificate ManagedCertificateRow
	if err := s.db.WithContext(ctx).Where("domain = ?", domain).First(&certificate).Error; err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		return s.writeManagedCertificateLegacyProjection(domain, bundle)
	}
	active, ok, err := s.loadActiveManagedCertificateGenerationLocked(ctx, domain)
	if err != nil {
		return err
	}
	if ok && active.MaterialHash == managedCertificateGenerationMaterialHash(bundle) {
		return s.writeManagedCertificateLegacyProjection(domain, bundle)
	}
	pending, err := s.stageManagedCertificateGenerationLocked(ctx, domain, bundle)
	if err != nil {
		return err
	}
	return s.promoteManagedCertificateGenerationLocked(ctx, domain, pending.ID, pending.MaterialHash)
}

func (s *GormStore) initializeSchema(ctx context.Context) error {
	options := SchemaOptionsForDriver("sqlite", true)
	options.LocalAgentID = s.LocalAgentID()
	return BootstrapSchema(ctx, s.db, options)
}

func normalizeAgentRow(row *AgentRow) {
	row.TagsJSON = defaultJSON(row.TagsJSON, "[]")
	row.CapabilitiesJSON = defaultJSON(row.CapabilitiesJSON, "[]")
	row.OutboundProxyURL = defaultString(row.OutboundProxyURL, "")
	row.TrafficStatsInterval = defaultString(row.TrafficStatsInterval, "")
	row.Mode = defaultString(row.Mode, "pull")
	row.LastApplyStatus = defaultString(row.LastApplyStatus, "")
	row.LastApplyMessage = defaultString(row.LastApplyMessage, "")
	row.LastReportedStatsJSON = defaultJSON(row.LastReportedStatsJSON, "{}")
	row.TrafficBlockReason = defaultString(row.TrafficBlockReason, "")
	row.LastSeenAt = defaultString(row.LastSeenAt, "")
	row.LastSeenIP = defaultString(row.LastSeenIP, "")
	row.LastSeenIPv4 = defaultString(row.LastSeenIPv4, "")
	row.LastSeenIPv6 = defaultString(row.LastSeenIPv6, "")
	row.DdnsConfigJSON = defaultString(row.DdnsConfigJSON, "")
	row.DdnsStatusJSON = defaultString(row.DdnsStatusJSON, "")
}

func normalizeHTTPRuleRow(row *HTTPRuleRow) {
	row.BackendsJSON = defaultJSON(row.BackendsJSON, "[]")
	row.LoadBalancingJSON = normalizeLoadBalancingJSON(row.LoadBalancingJSON)
	row.TagsJSON = defaultJSON(row.TagsJSON, "[]")
	row.RelayChainJSON = defaultJSON(row.RelayChainJSON, "[]")
	row.RelayLayersJSON = defaultJSON(row.RelayLayersJSON, "[]")
	row.UserAgent = defaultString(row.UserAgent, "")
	row.CustomHeadersJSON = defaultJSON(row.CustomHeadersJSON, "[]")
	row.EgressProfileID = copyOptionalPositiveInt(row.EgressProfileID)
}

func normalizeLocalAgentStateRow(row *LocalAgentStateRow) {
	row.LastApplyStatus = defaultString(row.LastApplyStatus, "success")
	row.LastApplyMessage = defaultString(row.LastApplyMessage, "")
	row.DesiredVersion = defaultString(row.DesiredVersion, "")
	row.PKISecurityAckJSON = defaultString(row.PKISecurityAckJSON, "")
}

func normalizeL4RuleRow(row *L4RuleRow) {
	row.Name = defaultString(row.Name, "")
	row.Protocol = defaultString(row.Protocol, "tcp")
	row.ListenHost = defaultString(row.ListenHost, "0.0.0.0")
	row.UpstreamHost = defaultString(row.UpstreamHost, "")
	row.BackendsJSON = defaultJSON(row.BackendsJSON, "[]")
	row.LoadBalancingJSON = normalizeLoadBalancingJSON(row.LoadBalancingJSON)
	row.TuningJSON = defaultJSON(row.TuningJSON, "{}")
	row.RelayChainJSON = defaultJSON(row.RelayChainJSON, "[]")
	row.RelayLayersJSON = defaultJSON(row.RelayLayersJSON, "[]")
	row.ListenMode = defaultString(row.ListenMode, "tcp")
	row.ProxyEntryAuthJSON = defaultJSON(row.ProxyEntryAuthJSON, "{}")
	row.EgressProfileID = copyOptionalPositiveInt(row.EgressProfileID)
	row.TagsJSON = defaultJSON(row.TagsJSON, "[]")
}

func normalizeVersionPolicyRow(row *VersionPolicyRow) {
	row.Channel = defaultString(row.Channel, "stable")
	row.DesiredVersion = defaultString(row.DesiredVersion, "")
	row.PackagesJSON = defaultJSON(row.PackagesJSON, "[]")
	row.TagsJSON = defaultJSON(row.TagsJSON, "[]")
}

func normalizeRelayListenerRow(row *RelayListenerRow) {
	legacyTransportUnset := strings.TrimSpace(row.TransportMode) == ""
	row.Name = defaultString(row.Name, "")
	row.BindHostsJSON = defaultJSON(row.BindHostsJSON, "[]")
	row.ListenHost = defaultString(row.ListenHost, "0.0.0.0")
	row.PublicHost = defaultString(row.PublicHost, row.ListenHost)
	row.TLSMode = defaultString(row.TLSMode, "pin_or_ca")
	row.TransportMode = defaultString(row.TransportMode, "tls_tcp")
	if legacyTransportUnset {
		row.AllowTransportFallback = true
	}
	row.ObfsMode = defaultString(row.ObfsMode, "off")
	row.PinSetJSON = defaultJSON(row.PinSetJSON, "[]")
	row.TrustedCACertificateIDs = defaultJSON(row.TrustedCACertificateIDs, "[]")
	row.TagsJSON = defaultJSON(row.TagsJSON, "[]")
}

func normalizeEgressProfileRow(row *EgressProfileRow) {
	row.Name = defaultString(row.Name, "")
	row.Type = defaultString(row.Type, "")
	row.ProxyURL = defaultString(row.ProxyURL, "")
	row.Description = defaultString(row.Description, "")
}

func egressProfileRowPayload(row EgressProfileRow) map[string]any {
	normalizeEgressProfileRow(&row)
	return map[string]any{
		"id":          row.ID,
		"name":        row.Name,
		"type":        row.Type,
		"proxy_url":   row.ProxyURL,
		"enabled":     row.Enabled,
		"description": row.Description,
		"revision":    row.Revision,
	}
}

func normalizeManagedCertificateRow(row *ManagedCertificateRow) {
	row.Domain = defaultString(row.Domain, "")
	row.Scope = defaultString(row.Scope, "domain")
	row.IssuerMode = defaultString(row.IssuerMode, "master_cf_dns")
	row.TargetAgentIDs = defaultJSON(row.TargetAgentIDs, "[]")
	row.Status = defaultString(row.Status, "pending")
	row.LastIssueAt = defaultString(row.LastIssueAt, "")
	row.LastError = defaultString(row.LastError, "")
	row.MaterialHash = defaultString(row.MaterialHash, "")
	row.AgentReports = defaultJSON(row.AgentReports, "{}")
	row.ACMEInfo = defaultJSON(row.ACMEInfo, "{}")
	row.Usage = defaultString(row.Usage, "https")
	row.CertificateType = defaultString(row.CertificateType, "acme")
	row.TagsJSON = defaultJSON(row.TagsJSON, "[]")
	row.ActiveGenerationID = defaultString(row.ActiveGenerationID, "")
	row.PendingGenerationID = defaultString(row.PendingGenerationID, "")
}

func defaultJSON(value string, fallback string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return fallback
	}
	return trimmed
}

func defaultString(value string, fallback string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return fallback
	}
	return trimmed
}

func normalizeLoadBalancingJSON(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || trimmed == "{}" {
		return `{"strategy":"adaptive"}`
	}
	return trimmed
}

func (s *GormStore) resolveAgentID(agentID string) string {
	if strings.TrimSpace(agentID) == "" {
		return s.localAgentID
	}
	return strings.TrimSpace(agentID)
}

func (s *GormStore) LocalAgentID() string {
	localAgentID := strings.TrimSpace(s.localAgentID)
	if localAgentID == "" {
		return "local"
	}
	return localAgentID
}

func computeDesiredRevision(
	localState LocalAgentStateRow,
	httpRows []HTTPRuleRow,
	l4Rows []L4RuleRow,
	relayRows []RelayListenerRow,
	egressRows []EgressProfileRow,
	certRows []ManagedCertificateRow,
	extraRevisions ...int,
) int {
	desiredRevision := normalizeRevision(localState.DesiredRevision)
	currentRevision := normalizeRevision(localState.CurrentRevision)
	highestConfigRevision := maxInt(
		highestHTTPRuleRevision(httpRows),
		highestL4RuleRevision(l4Rows),
		highestRelayListenerRevision(relayRows),
		highestEgressProfileRevision(egressRows),
		highestManagedCertificateRevision(certRows),
	)
	for _, revision := range extraRevisions {
		highestConfigRevision = maxInt(highestConfigRevision, normalizeRevision(revision))
	}

	if desiredRevision > currentRevision {
		return maxInt(desiredRevision, highestConfigRevision)
	}
	if highestConfigRevision > currentRevision {
		return highestConfigRevision
	}
	return maxInt(desiredRevision, highestConfigRevision)
}

func normalizeRevision(value int) int {
	if value < 0 {
		return 0
	}
	return value
}

func highestHTTPRuleRevision(rows []HTTPRuleRow) int {
	maxRevision := 0
	for _, row := range rows {
		maxRevision = maxInt(maxRevision, normalizeRevision(row.Revision))
	}
	return maxRevision
}

func highestL4RuleRevision(rows []L4RuleRow) int {
	maxRevision := 0
	for _, row := range rows {
		maxRevision = maxInt(maxRevision, normalizeRevision(row.Revision))
	}
	return maxRevision
}

func highestRelayListenerRevision(rows []RelayListenerRow) int {
	maxRevision := 0
	for _, row := range rows {
		maxRevision = maxInt(maxRevision, normalizeRevision(row.Revision))
	}
	return maxRevision
}

func highestManagedCertificateRevision(rows []ManagedCertificateRow) int {
	maxRevision := 0
	for _, row := range rows {
		maxRevision = maxInt(maxRevision, normalizeRevision(row.Revision))
	}
	return maxRevision
}

func highestEgressProfileRevision(rows []EgressProfileRow) int {
	maxRevision := 0
	for _, row := range rows {
		maxRevision = maxInt(maxRevision, normalizeRevision(int(row.Revision)))
	}
	return maxRevision
}

func egressProfileScopeRevision(
	agentID string,
	egressRows []EgressProfileRow,
	httpRows []HTTPRuleRow,
	l4Rows []L4RuleRow,
	relayRows []RelayListenerRow,
) int {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return 0
	}

	revision := 0
	for _, row := range egressRows {
		if row.ID <= 0 {
			continue
		}
		if egressProfileRowReferencesAgent(row.ID, agentID, httpRows, l4Rows, relayRows) {
			revision = maxInt(revision, normalizeRevision(int(row.Revision)))
		}
	}
	for _, row := range httpRows {
		if !egressScopeRuleAffectsAgent(agentID, row.AgentID, row.RelayLayersJSON, relayRows) {
			continue
		}
		revision = maxInt(revision, normalizeRevision(row.Revision))
	}
	for _, row := range l4Rows {
		if !egressScopeRuleAffectsAgent(agentID, row.AgentID, row.RelayLayersJSON, relayRows) {
			continue
		}
		revision = maxInt(revision, normalizeRevision(row.Revision))
	}
	return revision
}

func egressProfileRowReferencesAgent(profileID int, agentID string, httpRows []HTTPRuleRow, l4Rows []L4RuleRow, relayRows []RelayListenerRow) bool {
	if profileID <= 0 || strings.TrimSpace(agentID) == "" {
		return false
	}
	matchesProfile := func(value *int) bool {
		return value != nil && *value == profileID
	}
	for _, row := range httpRows {
		if !matchesProfile(row.EgressProfileID) {
			continue
		}
		if egressScopeRuleAffectsAgent(agentID, row.AgentID, row.RelayLayersJSON, relayRows) {
			return true
		}
	}
	for _, row := range l4Rows {
		if !matchesProfile(row.EgressProfileID) {
			continue
		}
		if egressScopeRuleAffectsAgent(agentID, row.AgentID, row.RelayLayersJSON, relayRows) {
			return true
		}
	}
	return false
}

func egressScopeRuleAffectsAgent(agentID string, rowAgentID string, relayLayersJSON string, relayRows []RelayListenerRow) bool {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return false
	}
	relayLayers := parseIntLayers(relayLayersJSON)
	if len(relayLayers) == 0 {
		return strings.TrimSpace(rowAgentID) == agentID
	}
	if relayLayersReferenceAgent(relayLayers, relayRows, agentID) {
		return true
	}
	_, isFinalHop := finalHopAgentIDsForRelayLayers(relayLayers, relayRows)[agentID]
	return isFinalHop || agentOwnsRelayListener(agentID, relayRows)
}

func relayLayersReferenceAgent(relayLayers [][]int, relayRows []RelayListenerRow, agentID string) bool {
	relayAgentByID := make(map[int]string, len(relayRows))
	for _, row := range relayRows {
		if row.ID <= 0 {
			continue
		}
		relayAgentByID[row.ID] = strings.TrimSpace(row.AgentID)
	}
	for _, layer := range relayLayers {
		for _, relayID := range layer {
			if strings.TrimSpace(relayAgentByID[relayID]) == agentID {
				return true
			}
		}
	}
	return false
}

func agentOwnsRelayListener(agentID string, relayRows []RelayListenerRow) bool {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return false
	}
	for _, row := range relayRows {
		if strings.TrimSpace(row.AgentID) == agentID {
			return true
		}
	}
	return false
}

func filterEgressProfilesForSnapshot(
	agentID string,
	rows []EgressProfileRow,
	httpRows []HTTPRuleRow,
	l4Rows []L4RuleRow,
	relayRows []RelayListenerRow,
	includeDisabled bool,
) []EgressProfileRow {
	if len(rows) == 0 {
		return rows
	}
	executorIDs := egressProfileExecutorIDs(agentID, httpRows, l4Rows, relayRows)
	if len(executorIDs) == 0 {
		return nil
	}
	filtered := make([]EgressProfileRow, 0, len(executorIDs))
	for _, row := range rows {
		if !includeDisabled && !row.Enabled {
			continue
		}
		if _, ok := executorIDs[row.ID]; ok {
			filtered = append(filtered, row)
		}
	}
	return filtered
}

func egressProfileExecutorIDs(agentID string, httpRows []HTTPRuleRow, l4Rows []L4RuleRow, relayRows []RelayListenerRow) map[int]struct{} {
	agentID = strings.TrimSpace(agentID)
	executorIDs := make(map[int]struct{})
	addProfile := func(profileID *int) {
		if profileID == nil || *profileID <= 0 {
			return
		}
		executorIDs[*profileID] = struct{}{}
	}
	addIfExecutor := func(rowAgentID string, profileID *int, relayLayersJSON string) {
		if profileID == nil || *profileID <= 0 {
			return
		}
		relayLayers := parseIntLayers(relayLayersJSON)
		if len(relayLayers) == 0 {
			if strings.TrimSpace(rowAgentID) == agentID {
				addProfile(profileID)
			}
			return
		}
		if _, ok := finalHopAgentIDsForRelayLayers(relayLayers, relayRows)[agentID]; ok {
			addProfile(profileID)
		}
	}

	for _, row := range httpRows {
		if !row.Enabled {
			continue
		}
		addIfExecutor(row.AgentID, row.EgressProfileID, row.RelayLayersJSON)
	}
	for _, row := range l4Rows {
		if !row.Enabled {
			continue
		}
		addIfExecutor(row.AgentID, row.EgressProfileID, row.RelayLayersJSON)
	}
	return executorIDs
}

func finalHopAgentIDsForRelayLayers(relayLayers [][]int, relayRows []RelayListenerRow) map[string]struct{} {
	relayAgentByID := make(map[int]string, len(relayRows))
	for _, row := range relayRows {
		if row.ID <= 0 || !row.Enabled {
			continue
		}
		relayAgentByID[row.ID] = strings.TrimSpace(row.AgentID)
	}

	agentIDs := make(map[string]struct{})
	for i := len(relayLayers) - 1; i >= 0; i-- {
		if len(relayLayers[i]) == 0 {
			continue
		}
		for _, finalHopID := range relayLayers[i] {
			finalHopAgentID := strings.TrimSpace(relayAgentByID[finalHopID])
			if finalHopAgentID == "" {
				continue
			}
			agentIDs[finalHopAgentID] = struct{}{}
		}
		return agentIDs
	}
	return agentIDs
}

func (s *GormStore) loadRelayListenersForSync(
	ctx context.Context,
	agentID string,
	httpRows []HTTPRuleRow,
	l4Rows []L4RuleRow,
) ([]RelayListenerRow, error) {
	localRows, err := s.ListRelayListeners(ctx, agentID)
	if err != nil {
		return nil, err
	}
	localRows, _ = partitionSnapshotRelayRows(localRows)

	syncRows := append([]RelayListenerRow(nil), localRows...)
	referencedIDs := referencedRelayListenerIDs(httpRows, l4Rows)
	if len(referencedIDs) == 0 {
		return syncRows, nil
	}

	included := make(map[int]struct{}, len(syncRows))
	for _, row := range syncRows {
		if row.ID > 0 {
			included[row.ID] = struct{}{}
		}
	}

	missingIDs := make([]int, 0, len(referencedIDs))
	for _, listenerID := range referencedIDs {
		if listenerID <= 0 {
			continue
		}
		if _, ok := included[listenerID]; ok {
			continue
		}
		included[listenerID] = struct{}{}
		missingIDs = append(missingIDs, listenerID)
	}
	if len(missingIDs) == 0 {
		return syncRows, nil
	}

	allRows, err := s.ListRelayListeners(ctx, "")
	if err != nil {
		return nil, err
	}
	allRows, _ = partitionSnapshotRelayRows(allRows)
	rowsByID := make(map[int]RelayListenerRow, len(allRows))
	for _, row := range allRows {
		if row.ID <= 0 {
			continue
		}
		rowsByID[row.ID] = row
	}
	for _, listenerID := range missingIDs {
		if row, ok := rowsByID[listenerID]; ok {
			syncRows = append(syncRows, row)
		}
	}
	return syncRows, nil
}

func (s *GormStore) loadAllHTTPRulesForSnapshot(ctx context.Context) ([]HTTPRuleRow, error) {
	var rows []HTTPRuleRow
	if err := s.db.WithContext(ctx).Order("agent_id, id").Find(&rows).Error; err != nil {
		return nil, err
	}
	for i := range rows {
		normalizeHTTPRuleRow(&rows[i])
	}
	return rows, nil
}

func (s *GormStore) loadAllL4RulesForSnapshot(ctx context.Context) ([]L4RuleRow, error) {
	var rows []L4RuleRow
	if err := s.db.WithContext(ctx).Order("agent_id, id").Find(&rows).Error; err != nil {
		return nil, err
	}
	for i := range rows {
		normalizeL4RuleRow(&rows[i])
	}
	return rows, nil
}

func referencedRelayListenerIDs(httpRows []HTTPRuleRow, l4Rows []L4RuleRow) []int {
	referenced := make([]int, 0)
	seen := make(map[int]struct{})
	addListenerIDs := func(listenerIDs []int) {
		for _, listenerID := range listenerIDs {
			if listenerID <= 0 {
				continue
			}
			if _, ok := seen[listenerID]; ok {
				continue
			}
			seen[listenerID] = struct{}{}
			referenced = append(referenced, listenerID)
		}
	}
	addRelayLayers := func(layersJSON string) {
		addListenerIDs(flattenIntLayers(parseIntLayers(layersJSON)))
	}

	for _, row := range httpRows {
		if !row.Enabled {
			continue
		}
		addRelayLayers(row.RelayLayersJSON)
	}
	for _, row := range l4Rows {
		if !row.Enabled {
			continue
		}
		addRelayLayers(row.RelayLayersJSON)
	}
	return referenced
}

func flattenIntLayers(layers [][]int) []int {
	flattened := make([]int, 0)
	for _, layer := range layers {
		flattened = append(flattened, layer...)
	}
	return flattened
}

func partitionSnapshotRelayRows(rows []RelayListenerRow) ([]RelayListenerRow, map[int]struct{}) {
	supported := make([]RelayListenerRow, 0, len(rows))
	excludedIDs := make(map[int]struct{})
	for _, row := range rows {
		if snapshotRelayTransportSupported(row.TransportMode) {
			supported = append(supported, row)
			continue
		}
		if row.ID > 0 {
			excludedIDs[row.ID] = struct{}{}
		}
	}
	return supported, excludedIDs
}

func partitionSnapshotEgressRows(rows []EgressProfileRow) ([]EgressProfileRow, map[int]struct{}) {
	supported := make([]EgressProfileRow, 0, len(rows))
	excludedIDs := make(map[int]struct{})
	for _, row := range rows {
		if snapshotEgressProfileTypeSupported(row.Type) {
			supported = append(supported, row)
			continue
		}
		if row.ID > 0 {
			excludedIDs[row.ID] = struct{}{}
		}
	}
	return supported, excludedIDs
}

func filterHTTPRuleRowsForSnapshot(rows []HTTPRuleRow, excludedRelayIDs, excludedEgressIDs map[int]struct{}) []HTTPRuleRow {
	filtered := make([]HTTPRuleRow, 0, len(rows))
	for _, row := range rows {
		if snapshotRuleReferencesExcludedResource(row.RelayLayersJSON, row.EgressProfileID, excludedRelayIDs, excludedEgressIDs) {
			continue
		}
		filtered = append(filtered, row)
	}
	return filtered
}

func filterL4RuleRowsForSnapshot(rows []L4RuleRow, excludedRelayIDs, excludedEgressIDs map[int]struct{}) []L4RuleRow {
	filtered := make([]L4RuleRow, 0, len(rows))
	for _, row := range rows {
		if snapshotRuleReferencesExcludedResource(row.RelayLayersJSON, row.EgressProfileID, excludedRelayIDs, excludedEgressIDs) {
			continue
		}
		filtered = append(filtered, row)
	}
	return filtered
}

func snapshotRuleReferencesExcludedResource(relayLayersJSON string, egressProfileID *int, excludedRelayIDs, excludedEgressIDs map[int]struct{}) bool {
	if egressProfileID != nil {
		if _, excluded := excludedEgressIDs[*egressProfileID]; excluded {
			return true
		}
	}
	for _, layer := range parseIntLayers(relayLayersJSON) {
		for _, listenerID := range layer {
			if _, excluded := excludedRelayIDs[listenerID]; excluded {
				return true
			}
		}
	}
	return false
}

func filterSyncL4RuleRows(rows []L4RuleRow) []L4RuleRow {
	filtered := make([]L4RuleRow, 0, len(rows))
	for _, row := range rows {
		if isSyncL4RuleRowValid(row) {
			filtered = append(filtered, row)
		}
	}
	return filtered
}

func isSyncL4RuleRowValid(row L4RuleRow) bool {
	protocol := strings.ToLower(strings.TrimSpace(row.Protocol))
	if protocol == "" {
		protocol = "tcp"
	}
	if protocol != "tcp" && protocol != "udp" {
		return false
	}

	listenMode := strings.ToLower(strings.TrimSpace(row.ListenMode))
	switch listenMode {
	case "proxy":
		if row.ListenPort < 1 || row.ListenPort > 65535 {
			return false
		}
		return true
	case "", "tcp":
	default:
		return false
	}

	if row.ListenPort < 1 || row.ListenPort > 65535 {
		return false
	}
	return len(parseL4Backends(row.BackendsJSON)) > 0
}

func SnapshotHTTPRules(rows []HTTPRuleRow) []HTTPRule {
	return snapshotHTTPRules(rows, false)
}

func snapshotHTTPRules(rows []HTTPRuleRow, intent bool) []HTTPRule {
	rules := make([]HTTPRule, 0, len(rows))
	for _, row := range rows {
		if !row.Enabled {
			continue
		}
		backends := parseHTTPBackends(row.BackendsJSON)
		if intent {
			backends = parseHTTPBackendsForIntent(row.BackendsJSON)
		}
		rules = append(rules, HTTPRule{
			ID:               row.ID,
			AgentID:          row.AgentID,
			FrontendURL:      row.FrontendURL,
			Backends:         backends,
			LoadBalancing:    parseLoadBalancingStrategy(row.LoadBalancingJSON),
			ProxyRedirect:    row.ProxyRedirect,
			PassProxyHeaders: row.PassProxyHeaders,
			UserAgent:        row.UserAgent,
			CustomHeaders:    parseHTTPHeaders(row.CustomHeadersJSON),

			EgressProfileID:    copyOptionalPositiveInt(row.EgressProfileID),
			TrustedProxyRanges: parseStringSlice(row.TrustedProxyRangesJSON),

			RelayLayers: parseIntLayers(row.RelayLayersJSON),
			RelayObfs:   row.RelayObfs,
			PolicyRef:   parsePolicyRef(row.PolicyRefJSON),
			Revision:    int64(row.Revision),
		})
	}
	return rules
}

func SnapshotL4Rules(rows []L4RuleRow) []L4Rule {
	return snapshotL4Rules(rows, false)
}

func snapshotL4Rules(rows []L4RuleRow, intent bool) []L4Rule {
	rules := make([]L4Rule, 0, len(rows))
	for _, row := range rows {
		if !row.Enabled {
			continue
		}
		backends := parseL4Backends(row.BackendsJSON)
		if intent {
			backends = parseL4BackendsForIntent(row.BackendsJSON)
		}
		rules = append(rules, L4Rule{
			ID:            row.ID,
			AgentID:       row.AgentID,
			Name:          row.Name,
			Protocol:      defaultString(row.Protocol, "tcp"),
			ListenHost:    defaultString(row.ListenHost, "0.0.0.0"),
			ListenPort:    row.ListenPort,
			Backends:      backends,
			LoadBalancing: parseLoadBalancingStrategy(row.LoadBalancingJSON),
			Tuning:        parseL4Tuning(row.TuningJSON),
			RelayLayers:   parseIntLayers(row.RelayLayersJSON),
			RelayObfs:     row.RelayObfs,
			ListenMode:    defaultString(row.ListenMode, "tcp"),

			EgressProfileID: copyOptionalPositiveInt(row.EgressProfileID),

			ProxyEntryAuth: parseL4ProxyEntryAuth(row.ProxyEntryAuthJSON),
			PolicyRef:      parsePolicyRef(row.PolicyRefJSON),
			Revision:       int64(row.Revision),
		})
	}
	return rules
}

func parsePolicyRef(raw string) *PolicyRef {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "null" {
		return nil
	}
	var ref PolicyRef
	if err := json.Unmarshal([]byte(raw), &ref); err != nil || ValidatePluginPolicyIdentity(ref.ID) != nil {
		// Preserve fail-closed behavior: malformed persisted attachment must make
		// the Agent reject the candidate instead of silently dropping protection.
		return &PolicyRef{ID: "\x00invalid-policy-ref"}
	}
	ref.Overlay = append(json.RawMessage(nil), ref.Overlay...)
	return &ref
}

func SnapshotEgressProfiles(rows []EgressProfileRow) []EgressProfile {
	return snapshotEgressProfiles(rows, false)
}

// SnapshotEgressProfilesForIntent preserves disabled rows for pre-commit validation.
func SnapshotEgressProfilesForIntent(rows []EgressProfileRow) []EgressProfile {
	return snapshotEgressProfiles(rows, true)
}

func snapshotEgressProfiles(rows []EgressProfileRow, includeDisabled bool) []EgressProfile {
	profiles := make([]EgressProfile, 0, len(rows))
	for _, row := range rows {
		if !snapshotEgressProfileTypeSupported(row.Type) || (!includeDisabled && !row.Enabled) {
			continue
		}
		profiles = append(profiles, EgressProfile{
			ID:       row.ID,
			Name:     row.Name,
			Type:     row.Type,
			ProxyURL: row.ProxyURL,

			Enabled:     row.Enabled,
			Description: row.Description,
			Revision:    row.Revision,
		})
	}
	return profiles
}

func (s *GormStore) relayListenerAgentNames(ctx context.Context, rows []RelayListenerRow) (map[string]string, error) {
	if len(rows) == 0 {
		return nil, nil
	}
	agents, err := s.ListAgents(ctx)
	if err != nil {
		return nil, err
	}
	names := make(map[string]string, len(agents))
	for _, agent := range agents {
		if name := strings.TrimSpace(agent.Name); agent.ID != "" && name != "" {
			names[agent.ID] = name
		}
	}
	return names, nil
}

func snapshotRelayListeners(rows []RelayListenerRow, agentNames map[string]string) []RelayListener {
	listeners := make([]RelayListener, 0, len(rows))
	for _, row := range rows {
		listeners = append(listeners, RelayListener{
			ID:            row.ID,
			AgentID:       row.AgentID,
			AgentName:     agentNames[row.AgentID],
			Name:          row.Name,
			ListenHost:    defaultString(row.ListenHost, "0.0.0.0"),
			BindHosts:     parseStringSlice(row.BindHostsJSON),
			ListenPort:    row.ListenPort,
			PublicHost:    defaultString(row.PublicHost, row.ListenHost),
			PublicPort:    row.PublicPort,
			Enabled:       row.Enabled,
			CertificateID: copyOptionalInt(row.CertificateID),
			TLSMode:       defaultString(row.TLSMode, "pin_or_ca"),
			TransportMode: defaultString(row.TransportMode, "tls_tcp"),

			AllowTransportFallback:  row.AllowTransportFallback,
			ObfsMode:                defaultString(row.ObfsMode, "off"),
			PinSet:                  parseRelayPins(row.PinSetJSON),
			TrustedCACertificateIDs: parseIntSlice(row.TrustedCACertificateIDs),
			AllowSelfSigned:         row.AllowSelfSigned,
			Tags:                    parseStringSlice(row.TagsJSON),
			Revision:                int64(row.Revision),
		})
		if strings.TrimSpace(row.TransportMode) == "" {
			listeners[len(listeners)-1].AllowTransportFallback = true
		}
	}
	return listeners
}

func (s *GormStore) snapshotCertificateBundles(ctx context.Context, rows []ManagedCertificateRow) ([]ManagedCertificateBundle, error) {
	bundles := make([]ManagedCertificateBundle, 0, len(rows))
	for _, row := range rows {
		if !row.Enabled {
			continue
		}
		domain, err := normalizeManagedCertificateGenerationDomain(row.Domain)
		if err != nil {
			return nil, err
		}
		if s.transactionScoped {
			// Revision mutations already hold database write locks. Read the immutable
			// active generation directly so snapshotting does not reverse the
			// domain-to-database lock order used by generation maintenance.
			active, activeOK, err := s.loadManagedCertificateGenerationByPointer(ctx, domain, "active_generation_id", ManagedCertificateGenerationStateActive)
			if err != nil {
				return nil, err
			}
			if activeOK {
				bundles = append(bundles, ManagedCertificateBundle{
					ID:       row.ID,
					Domain:   domain,
					Revision: int64(row.Revision),
					CertPEM:  active.Material.CertPEM,
					KeyPEM:   active.Material.KeyPEM,
				})
				continue
			}
			projected, projectedOK, err := s.readManagedCertificateMaterialSecure(domain)
			if err != nil {
				return nil, err
			}
			if !projectedOK {
				continue
			}
			bundles = append(bundles, ManagedCertificateBundle{
				ID:       row.ID,
				Domain:   domain,
				Revision: int64(row.Revision),
				CertPEM:  projected.CertPEM,
				KeyPEM:   projected.KeyPEM,
			})
			continue
		}
		unlock := s.lockManagedCertificateDomain(domain)
		active, activeOK, err := s.loadActiveManagedCertificateGenerationLocked(ctx, domain)
		if err != nil {
			unlock()
			return nil, err
		}
		projected, projectedOK, err := s.readManagedCertificateMaterialSecure(domain)
		if err != nil {
			unlock()
			return nil, err
		}
		if activeOK && (!projectedOK || projected.CertPEM != active.Material.CertPEM || projected.KeyPEM != active.Material.KeyPEM) {
			if err := s.reconcileManagedCertificateGenerationsLocked(ctx, domain); err != nil {
				unlock()
				return nil, err
			}
			active, activeOK, err = s.loadActiveManagedCertificateGenerationLocked(ctx, domain)
			if err != nil {
				unlock()
				return nil, err
			}
		}
		if activeOK {
			projected = managedCertificateMaterial{CertPEM: active.Material.CertPEM, KeyPEM: active.Material.KeyPEM}
			projectedOK = true
		}
		unlock()
		if !projectedOK {
			continue
		}
		bundles = append(bundles, ManagedCertificateBundle{
			ID:       row.ID,
			Domain:   domain,
			Revision: int64(row.Revision),
			CertPEM:  projected.CertPEM,
			KeyPEM:   projected.KeyPEM,
		})
	}
	return bundles, nil
}

func snapshotCertificatePolicies(rows []ManagedCertificateRow, agentID string, materialByDomain map[string]bool, includeUnpublished bool) []ManagedCertificatePolicy {
	policies := make([]ManagedCertificatePolicy, 0, len(rows))
	for _, row := range rows {
		// Master-issued (control-plane) certificates are installed, not issued, by agents:
		// withhold the policy until material exists so agents don't attempt local
		// master_cf_dns issuance, which non-master agents reject and fail the heartbeat on.
		if !includeUnpublished && isMasterIssuedCertificateMode(defaultString(row.IssuerMode, "master_cf_dns")) && !materialByDomain[strings.TrimSpace(row.Domain)] {
			continue
		}
		view := buildManagedCertificateViewForAgent(row, agentID)
		policies = append(policies, ManagedCertificatePolicy{
			ID:              view.ID,
			Domain:          view.Domain,
			Enabled:         view.Enabled,
			Scope:           defaultString(view.Scope, "domain"),
			IssuerMode:      defaultString(view.IssuerMode, "master_cf_dns"),
			Status:          defaultString(view.Status, "pending"),
			LastIssueAt:     view.LastIssueAt,
			LastError:       view.LastError,
			ACMEInfo:        parseManagedCertificateACMEInfo(view.ACMEInfo),
			Tags:            parseStringSlice(view.TagsJSON),
			Revision:        int64(view.Revision),
			Usage:           defaultString(view.Usage, "https"),
			CertificateType: defaultString(view.CertificateType, "acme"),
			SelfSigned:      view.SelfSigned,
		})
	}
	return policies
}

// isMasterIssuedCertificateMode reports whether the issuer mode is issued by the control
// plane (DNS-01) rather than the agent, so its policy must wait for material before publishing.
func isMasterIssuedCertificateMode(mode string) bool {
	mode = strings.TrimSpace(strings.ToLower(mode))
	return mode == "" || mode == "master_cf_dns"
}

func filterManagedCertificatesForAgent(rows []ManagedCertificateRow, agentID string, httpRows []HTTPRuleRow, relayRows []RelayListenerRow) []ManagedCertificateRow {
	filtered := make([]ManagedCertificateRow, 0, len(rows))
	referencedCertificateIDs := relayReferencedCertificateIDs(relayRows)
	for _, row := range rows {
		if referencedCertificateIDs[row.ID] || containsString(parseStringSlice(row.TargetAgentIDs), agentID) || doesManagedCertificateMatchAnyHTTPRule(row, httpRows) {
			filtered = append(filtered, row)
		}
	}
	return filtered
}

func doesManagedCertificateMatchAnyHTTPRule(row ManagedCertificateRow, httpRows []HTTPRuleRow) bool {
	if !row.Enabled || !strings.EqualFold(defaultString(row.Usage, "https"), "https") {
		return false
	}
	if defaultString(row.Scope, "domain") == "ip" {
		return false
	}
	for _, httpRow := range httpRows {
		if !httpRow.Enabled {
			continue
		}
		scheme, host, ok := parseSnapshotHTTPRuleFrontendTarget(httpRow.FrontendURL)
		if !ok || scheme != "https" {
			continue
		}
		if doesManagedCertificateRowMatchHost(row, host) {
			return true
		}
	}
	return false
}

func parseSnapshotHTTPRuleFrontendTarget(frontendURL string) (string, string, bool) {
	parsed, err := url.Parse(strings.TrimSpace(frontendURL))
	if err != nil || parsed == nil {
		return "", "", false
	}
	host := strings.ToLower(normalizeSnapshotCertificateHost(parsed.Hostname()))
	scheme := strings.ToLower(strings.TrimSpace(parsed.Scheme))
	if host == "" || scheme == "" {
		return "", "", false
	}
	return scheme, host, true
}

func doesManagedCertificateRowMatchHost(row ManagedCertificateRow, host string) bool {
	if defaultString(row.Scope, "domain") == "ip" {
		return isExactSnapshotManagedCertificateMatch(row.Domain, host)
	}
	return isExactSnapshotManagedCertificateMatch(row.Domain, host) || isWildcardSnapshotManagedCertificateMatch(row.Domain, host)
}

func isExactSnapshotManagedCertificateMatch(certDomain string, host string) bool {
	return strings.EqualFold(normalizeSnapshotCertificateHost(certDomain), normalizeSnapshotCertificateHost(host))
}

func isWildcardSnapshotManagedCertificateMatch(certDomain string, host string) bool {
	pattern := strings.ToLower(normalizeSnapshotCertificateHost(certDomain))
	target := strings.ToLower(normalizeSnapshotCertificateHost(host))
	if !isWildcardSnapshotCertificateDomain(pattern) {
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

func isWildcardSnapshotCertificateDomain(value string) bool {
	normalized := normalizeSnapshotCertificateHost(value)
	return strings.HasPrefix(normalized, "*.") && len(normalized) > 2
}

func normalizeSnapshotCertificateHost(value string) string {
	trimmed := strings.TrimSpace(value)
	if len(trimmed) >= 2 && strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
		return trimmed[1 : len(trimmed)-1]
	}
	return trimmed
}

func buildManagedCertificateViewForAgent(row ManagedCertificateRow, agentID string) ManagedCertificateRow {
	report, ok := parseManagedCertificateAgentReport(row.AgentReports, agentID)
	if !ok {
		return row
	}

	view := row
	if report.Status != "" {
		view.Status = report.Status
	}
	view.LastIssueAt = report.LastIssueAt
	view.LastError = report.LastError
	view.MaterialHash = report.MaterialHash
	view.ACMEInfo = marshalManagedCertificateACMEInfo(report.ACMEInfo)
	return view
}

func resolveVersionPackageForPlatform(rows []VersionPolicyRow, desiredVersion string, platform string) *VersionPackage {
	desiredVersion = strings.TrimSpace(desiredVersion)
	platform = strings.TrimSpace(platform)
	if desiredVersion == "" || platform == "" {
		return nil
	}

	for _, row := range rows {
		if strings.TrimSpace(row.DesiredVersion) != desiredVersion {
			continue
		}
		for _, pkg := range parseVersionPackages(row.PackagesJSON) {
			if strings.TrimSpace(pkg.Platform) == platform {
				copyValue := pkg
				return &copyValue
			}
		}
	}
	return nil
}

func parseHTTPBackends(raw string) []HTTPBackend {
	values := parseHTTPBackendsForIntent(raw)
	normalized := make([]HTTPBackend, 0, len(values))
	for _, value := range values {
		if value.URL == "" {
			continue
		}
		normalized = append(normalized, value)
	}
	return normalized
}

func parseHTTPBackendsForIntent(raw string) []HTTPBackend {
	var values []HTTPBackend
	if err := json.Unmarshal([]byte(defaultString(raw, "[]")), &values); err != nil {
		return []HTTPBackend{}
	}
	for i := range values {
		values[i].URL = strings.TrimSpace(values[i].URL)
	}
	return values
}

func parseHTTPHeaders(raw string) []HTTPHeader {
	var values []HTTPHeader
	if err := json.Unmarshal([]byte(defaultString(raw, "[]")), &values); err != nil {
		return []HTTPHeader{}
	}
	normalized := make([]HTTPHeader, 0, len(values))
	for _, value := range values {
		name := strings.TrimSpace(value.Name)
		if name == "" {
			continue
		}
		normalized = append(normalized, HTTPHeader{Name: name, Value: value.Value})
	}
	return normalized
}

func parseLoadBalancingStrategy(raw string) LoadBalancing {
	var value LoadBalancing
	if err := json.Unmarshal([]byte(defaultString(raw, "{}")), &value); err != nil {
		return LoadBalancing{Strategy: "adaptive"}
	}
	switch strings.ToLower(strings.TrimSpace(value.Strategy)) {
	case "round_robin", "random", "adaptive":
		value.Strategy = strings.ToLower(strings.TrimSpace(value.Strategy))
	default:
		value.Strategy = "adaptive"
	}
	return value
}

func parseL4Backends(raw string) []L4Backend {
	values := parseL4BackendsForIntent(raw)
	normalized := make([]L4Backend, 0, len(values))
	for _, value := range values {
		if value.Host == "" || value.Port < 1 || value.Port > 65535 {
			continue
		}
		normalized = append(normalized, value)
	}
	return normalized
}

func parseL4BackendsForIntent(raw string) []L4Backend {
	var values []L4Backend
	if err := json.Unmarshal([]byte(defaultString(raw, "[]")), &values); err != nil {
		return []L4Backend{}
	}
	for i := range values {
		values[i].Host = strings.TrimSpace(values[i].Host)
	}
	return values
}

func parseL4Tuning(raw string) L4Tuning {
	var tuning L4Tuning
	if err := json.Unmarshal([]byte(defaultString(raw, "{}")), &tuning); err != nil {
		return L4Tuning{}
	}
	return tuning
}

func parseL4ProxyEntryAuth(raw string) L4ProxyEntryAuth {
	var auth L4ProxyEntryAuth
	if err := json.Unmarshal([]byte(defaultString(raw, "{}")), &auth); err != nil {
		return L4ProxyEntryAuth{}
	}
	auth.Username = strings.TrimSpace(auth.Username)
	return auth
}

func parseRelayPins(raw string) []RelayPin {
	var values []RelayPin
	if err := json.Unmarshal([]byte(defaultString(raw, "[]")), &values); err != nil {
		return []RelayPin{}
	}
	normalized := make([]RelayPin, 0, len(values))
	for _, value := range values {
		pinType := strings.TrimSpace(value.Type)
		pinValue := strings.TrimSpace(value.Value)
		if pinType == "" || pinValue == "" {
			continue
		}
		normalized = append(normalized, RelayPin{Type: pinType, Value: pinValue})
	}
	return normalized
}

func parseManagedCertificateACMEInfo(raw string) ManagedCertificateACMEInfo {
	var info ManagedCertificateACMEInfo
	_ = json.Unmarshal([]byte(defaultString(raw, "{}")), &info)
	return info
}

func marshalManagedCertificateACMEInfo(info ManagedCertificateACMEInfo) string {
	data, err := json.Marshal(info)
	if err != nil {
		return "{}"
	}
	return string(data)
}

type managedCertificateAgentReport struct {
	Status       string                     `json:"status"`
	LastIssueAt  string                     `json:"last_issue_at"`
	LastError    string                     `json:"last_error"`
	MaterialHash string                     `json:"material_hash"`
	ACMEInfo     ManagedCertificateACMEInfo `json:"acme_info"`
}

func parseManagedCertificateAgentReport(raw string, agentID string) (managedCertificateAgentReport, bool) {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return managedCertificateAgentReport{}, false
	}
	var reports map[string]managedCertificateAgentReport
	if err := json.Unmarshal([]byte(defaultString(raw, "{}")), &reports); err != nil {
		return managedCertificateAgentReport{}, false
	}
	report, ok := reports[agentID]
	if !ok {
		return managedCertificateAgentReport{}, false
	}
	report.Status = normalizeManagedCertificateReportStatus(report.Status)
	report.LastIssueAt = strings.TrimSpace(report.LastIssueAt)
	report.MaterialHash = strings.TrimSpace(report.MaterialHash)
	return report, true
}

func normalizeManagedCertificateReportStatus(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "pending", "active", "error":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return ""
	}
}

func parseVersionPackages(raw string) []VersionPackage {
	var values []VersionPackage
	if err := json.Unmarshal([]byte(defaultString(raw, "[]")), &values); err != nil {
		return []VersionPackage{}
	}
	normalized := make([]VersionPackage, 0, len(values))
	for _, value := range values {
		platform := strings.TrimSpace(value.Platform)
		url := strings.TrimSpace(value.URL)
		sha256 := strings.TrimSpace(value.SHA256)
		if platform == "" || url == "" || sha256 == "" {
			continue
		}
		normalized = append(normalized, VersionPackage{
			Platform: platform,
			URL:      url,
			SHA256:   sha256,
			Filename: strings.TrimSpace(value.Filename),
			Size:     value.Size,
		})
	}
	return normalized
}

func parseStringSlice(raw string) []string {
	var values []string
	if err := json.Unmarshal([]byte(defaultString(raw, "[]")), &values); err != nil {
		return []string{}
	}
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed != "" {
			normalized = append(normalized, trimmed)
		}
	}
	return normalized
}

func parseIntSlice(raw string) []int {
	var values []int
	if err := json.Unmarshal([]byte(defaultString(raw, "[]")), &values); err != nil {
		return []int{}
	}
	normalized := make([]int, 0, len(values))
	for _, value := range values {
		if value > 0 {
			normalized = append(normalized, value)
		}
	}
	return normalized
}

func parseIntLayers(raw string) [][]int {
	var values [][]int
	if err := json.Unmarshal([]byte(defaultString(raw, "[]")), &values); err != nil {
		return [][]int{}
	}
	normalized := make([][]int, 0, len(values))
	for _, layer := range values {
		normalizedLayer := make([]int, 0, len(layer))
		for _, value := range layer {
			if value > 0 {
				normalizedLayer = append(normalizedLayer, value)
			}
		}
		if len(normalizedLayer) > 0 {
			normalized = append(normalized, normalizedLayer)
		}
	}
	return normalized
}

func relayReferencedCertificateIDs(rows []RelayListenerRow) map[int]bool {
	ids := make(map[int]bool)
	for _, row := range rows {
		if row.CertificateID != nil && *row.CertificateID > 0 {
			ids[*row.CertificateID] = true
		}
		for _, certID := range parseIntSlice(row.TrustedCACertificateIDs) {
			if certID > 0 {
				ids[certID] = true
			}
		}
	}
	return ids
}

func containsString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func copyOptionalInt(value *int) *int {
	if value == nil {
		return nil
	}
	copyValue := *value
	return &copyValue
}

func copyOptionalPositiveInt(value *int) *int {
	if value == nil || *value <= 0 {
		return nil
	}
	copyValue := *value
	return &copyValue
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

func boundedIntFromInt64(value int64) int {
	if value <= 0 {
		return 0
	}
	if value > int64(math.MaxInt) {
		return math.MaxInt
	}
	return int(value)
}

type managedCertificateMaterial struct {
	CertPEM string
	KeyPEM  string
}

func (s *GormStore) readManagedCertificateMaterial(domain string) (managedCertificateMaterial, bool) {
	material, ok, err := s.readManagedCertificateMaterialSecure(domain)
	if err != nil {
		return managedCertificateMaterial{}, false
	}
	return material, ok
}

func (s *GormStore) readManagedCertificateMaterialSecure(domain string) (managedCertificateMaterial, bool, error) {
	directory, err := s.validateManagedCertificateDirectory(domain)
	if errors.Is(err, os.ErrNotExist) {
		directory, err = s.validateManagedCertificateLegacyDirectory(domain)
	}
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return managedCertificateMaterial{}, false, nil
		}
		return managedCertificateMaterial{}, false, err
	}
	certPEM, err := readManagedCertificateRegularFile(filepath.Join(directory, "cert"))
	if errors.Is(err, os.ErrNotExist) {
		return managedCertificateMaterial{}, false, nil
	}
	if err != nil {
		return managedCertificateMaterial{}, false, err
	}
	keyPEM, err := readManagedCertificateRegularFile(filepath.Join(directory, "key"))
	if errors.Is(err, os.ErrNotExist) {
		return managedCertificateMaterial{}, false, nil
	}
	if err != nil {
		return managedCertificateMaterial{}, false, err
	}
	return managedCertificateMaterial{
		CertPEM: string(certPEM),
		KeyPEM:  string(keyPEM),
	}, true, nil
}

func (s *GormStore) managedCertificateDirectory(domain string) string {
	return managedCertificateDirectory(filepath.Join(s.dataRoot, "managed_certificates"), domain)
}

func managedCertificateDirectory(baseDir string, domain string) string {
	domainKey := managedCertificateDomainStorageKey(strings.TrimSpace(domain))
	if !isSafeSinglePathComponent(domainKey) {
		domainKey = "v1-invalid"
	}
	return filepath.Join(baseDir, domainKey)
}

func (s *GormStore) legacyManagedCertificateDirectory(domain string) string {
	return legacyManagedCertificateDirectory(filepath.Join(s.dataRoot, "managed_certificates"), domain)
}

func legacyManagedCertificateDirectory(baseDir string, domain string) string {
	safeHost := normalizeManagedCertificateHost(domain)
	if !isSafeSinglePathComponent(safeHost) {
		safeHost = "_"
	}
	return filepath.Join(baseDir, safeHost)
}

func normalizeManagedCertificateHost(domain string) string {
	normalized := strings.TrimSpace(domain)
	if strings.HasPrefix(normalized, "[") && strings.HasSuffix(normalized, "]") && len(normalized) >= 2 {
		normalized = normalized[1 : len(normalized)-1]
	}
	normalized = strings.ReplaceAll(normalized, "*.", "_wildcard_.")
	replacer := strings.NewReplacer("<", "_", ">", "_", ":", "_", "\"", "_", "/", "_", "\\", "_", "|", "_", "?", "_", "*", "_")
	normalized = replacer.Replace(normalized)
	for strings.Contains(normalized, "..") {
		normalized = strings.ReplaceAll(normalized, "..", "_")
	}
	normalized = strings.Trim(normalized, ". ")
	if normalized == "" {
		return "_"
	}
	return normalized
}

func isSafeSinglePathComponent(component string) bool {
	return component != "" &&
		component != "." &&
		component != ".." &&
		!strings.Contains(component, "..") &&
		!strings.Contains(component, "/") &&
		!strings.Contains(component, "\\")
}

func managedCertificateDomainSet(rows []ManagedCertificateRow) map[string]struct{} {
	domains := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		domain := strings.TrimSpace(row.Domain)
		if domain == "" {
			continue
		}
		domains[managedCertificateDomainStorageKey(domain)] = struct{}{}
	}
	return domains
}

func managedCertificateLegacyDomainSet(rows []ManagedCertificateRow) map[string]struct{} {
	domains := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		domain := strings.TrimSpace(row.Domain)
		if domain == "" {
			continue
		}
		domains[managedCertificateLegacyDomainKey(domain)] = struct{}{}
	}
	return domains
}
