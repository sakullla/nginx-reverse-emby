package storage

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/crypto/curve25519"
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
	ListWireGuardProfiles(context.Context, string) ([]WireGuardProfileRow, error)
	ListEgressProfiles(context.Context) ([]EgressProfileRow, error)
	LoadLocalAgentState(context.Context) (LocalAgentStateRow, error)
	LoadAgentSnapshot(context.Context, string, AgentSnapshotInput) (Snapshot, error)
	ListVersionPolicies(context.Context) ([]VersionPolicyRow, error)
	ListManagedCertificates(context.Context) ([]ManagedCertificateRow, error)
	SaveAgent(context.Context, AgentRow) error
	SaveL4Rules(context.Context, string, []L4RuleRow) error
	SaveRelayListeners(context.Context, string, []RelayListenerRow) error
	SaveWireGuardProfiles(context.Context, string, []WireGuardProfileRow) error
	SaveEgressProfiles(context.Context, []EgressProfileRow) error
	SaveVersionPolicies(context.Context, []VersionPolicyRow) error
	SaveManagedCertificates(context.Context, []ManagedCertificateRow) error
	LoadManagedCertificateMaterial(context.Context, string) (ManagedCertificateBundle, bool, error)
	SaveManagedCertificateMaterial(context.Context, string, ManagedCertificateBundle) error
	CleanupManagedCertificateMaterial(context.Context, []ManagedCertificateRow, []ManagedCertificateRow) error
}

type EgressProfileReference struct {
	Kind    string
	AgentID string
	ID      int
}

type SQLiteStore = GormStore

const localRuntimeStateMetaKey = "local_runtime_state"

type WireGuardClientProfileMutation struct {
	Profiles     []WireGuardProfileRow
	ProfileIndex int
	Clients      []WireGuardClientRow
	NextClientID int
}

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
	err := s.db.WithContext(ctx).
		Select("id", "traffic_blocked", "traffic_block_reason").
		Where("id = ?", agentID).
		First(&row).Error
	if err == nil {
		return row.TrafficBlocked, defaultString(row.TrafficBlockReason, ""), true, nil
	}
	if err == gorm.ErrRecordNotFound {
		return false, "", false, nil
	}
	return false, "", false, err
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
			DesiredRevision: row.DesiredRevision,
			CurrentRevision: row.CurrentRevision,
		}, nil
	}
	if err == gorm.ErrRecordNotFound {
		return LocalAgentStateRow{}, nil
	}
	return LocalAgentStateRow{}, err
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
	localState, err := s.LoadLocalAgentState(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	return s.LoadAgentSnapshot(ctx, agentID, AgentSnapshotInput{
		DesiredVersion:  localState.DesiredVersion,
		DesiredRevision: localState.DesiredRevision,
		CurrentRevision: localState.CurrentRevision,
		Platform:        runtime.GOOS + "-" + runtime.GOARCH,
	})
}

func (s *GormStore) LoadAgentSnapshot(ctx context.Context, agentID string, input AgentSnapshotInput) (Snapshot, error) {
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
	wireGuardRows, err := s.loadWireGuardProfilesForSync(ctx, resolvedAgentID)
	if err != nil {
		return Snapshot{}, err
	}
	allEgressRows, err := s.ListEgressProfiles(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	allHTTPRows, err := s.loadAllHTTPRulesForSnapshot(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	allL4Rows, err := s.loadAllL4RulesForSnapshot(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	allL4Rows = filterSyncL4RuleRows(allL4Rows)
	allRelayRows, err := s.ListRelayListeners(ctx, "")
	if err != nil {
		return Snapshot{}, err
	}
	egressRows := filterEgressProfilesForSnapshot(resolvedAgentID, allEgressRows, allHTTPRows, allL4Rows, allRelayRows)
	egressScopeRevision := egressProfileScopeRevision(resolvedAgentID, allEgressRows, allHTTPRows, allL4Rows, allRelayRows)
	wireGuardClientRows, err := s.ListWireGuardClients(ctx, resolvedAgentID, 0)
	if err != nil {
		return Snapshot{}, err
	}
	wireGuardRows, err = s.attachWireGuardRelayPeersForSnapshot(ctx, resolvedAgentID, wireGuardRows, httpRows, l4Rows, relayRows)
	if err != nil {
		return Snapshot{}, err
	}
	wireGuardRows = filterWireGuardProfilesForSnapshotGraph(resolvedAgentID, wireGuardRows, httpRows, l4Rows, relayRows, wireGuardClientRows)
	supportsWireGuard, err := s.agentSupportsWireGuardSnapshots(ctx, resolvedAgentID)
	if err != nil {
		return Snapshot{}, err
	}
	if !supportsWireGuard {
		relayRows = filterRelayListenerRowsWithoutWireGuard(relayRows)
		httpRows = filterHTTPRuleRowsWithoutWireGuard(httpRows, relayRows)
		l4Rows = filterL4RuleRowsWithoutWireGuard(l4Rows, relayRows)
		wireGuardRows = nil
	}

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
		DesiredRevision: maxInt(input.DesiredRevision, agentRevisionState.DesiredRevision),
		CurrentRevision: maxInt(input.CurrentRevision, agentRevisionState.CurrentRevision),
	}

	agentNames, err := s.relayListenerAgentNames(ctx, relayRows)
	if err != nil {
		return Snapshot{}, err
	}

	return Snapshot{
		DesiredVersion:      strings.TrimSpace(input.DesiredVersion),
		Revision:            int64(computeDesiredRevision(revisionState, httpRows, l4Rows, relayRows, wireGuardRows, egressRows, relevantCertRows, egressScopeRevision)),
		VersionPackage:      resolveVersionPackageForPlatform(versionPolicies, input.DesiredVersion, input.Platform),
		AgentConfig:         agentConfig,
		Rules:               SnapshotHTTPRules(httpRows),
		L4Rules:             SnapshotL4Rules(l4Rows),
		RelayListeners:      snapshotRelayListeners(relayRows, agentNames),
		WireGuardProfiles:   SnapshotWireGuardProfiles(wireGuardRows),
		EgressProfiles:      SnapshotEgressProfiles(egressRows),
		Certificates:        s.snapshotCertificateBundles(relevantCertRows),
		CertificatePolicies: snapshotCertificatePolicies(relevantCertRows, resolvedAgentID),
	}, nil
}

func (s *GormStore) loadAgentConfigForSnapshot(ctx context.Context, agentID string) (AgentConfig, bool) {
	var row AgentRow
	err := s.db.WithContext(ctx).
		Select("id", "outbound_proxy_url", "traffic_stats_interval", "traffic_blocked", "traffic_block_reason").
		Where("id = ?", agentID).
		First(&row).Error
	if err != nil {
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

func (s *GormStore) ListWireGuardProfiles(ctx context.Context, agentID string) ([]WireGuardProfileRow, error) {
	if !s.wireGuard {
		return nil, nil
	}
	if agentID == "" {
		agentID = s.localAgentID
	}

	var profiles []WireGuardProfileRow
	if err := s.db.WithContext(ctx).
		Where("agent_id = ?", agentID).
		Order("id").
		Find(&profiles).Error; err != nil {
		return nil, err
	}
	for i := range profiles {
		normalizeWireGuardProfileRow(&profiles[i])
	}
	return profiles, nil
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

func (s *GormStore) ListWireGuardClients(ctx context.Context, agentID string, profileID int) ([]WireGuardClientRow, error) {
	if !s.wireGuard {
		return nil, nil
	}
	if agentID == "" {
		agentID = s.localAgentID
	}

	var clients []WireGuardClientRow
	query := s.db.WithContext(ctx).
		Where("agent_id = ?", agentID).
		Order("id")
	if profileID > 0 {
		query = query.Where("profile_id = ?", profileID)
	}
	if err := query.Find(&clients).Error; err != nil {
		return nil, err
	}
	for i := range clients {
		normalizeWireGuardClientRow(&clients[i])
	}
	return clients, nil
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

	currentState, err := s.LoadLocalAgentState(ctx)
	if err != nil {
		return err
	}

	outcome := NormalizeLocalApplyOutcome(runtimeState)
	lastApplyStatus := outcome.Status
	if lastApplyStatus == "" {
		lastApplyStatus = currentState.LastApplyStatus
	}

	lastApplyMessage := outcome.Message
	lastApplyRevision := outcome.Revision
	if lastApplyRevision <= 0 {
		lastApplyRevision = runtimeState.CurrentRevision
	}

	desiredRevision := currentState.DesiredRevision
	if lastApplyStatus == "success" {
		desiredRevision = maxInt(desiredRevision, int(lastApplyRevision))
	}

	row := LocalAgentStateRow{
		ID:                1,
		DesiredRevision:   desiredRevision,
		CurrentRevision:   int(runtimeState.CurrentRevision),
		LastApplyRevision: int(lastApplyRevision),
		LastApplyStatus:   lastApplyStatus,
		LastApplyMessage:  lastApplyMessage,
		DesiredVersion:    currentState.DesiredVersion,
	}
	normalizeLocalAgentStateRow(&row)

	stateJSON, err := json.Marshal(runtimeState)
	if err != nil {
		return err
	}

	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
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
				Value: string(stateJSON),
			}).Error
	})
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

func (s *GormStore) DeleteAgent(ctx context.Context, agentID string) error {
	agentID = strings.TrimSpace(agentID)
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, err := s.deleteTrafficByAgentTx(tx, agentID); err != nil {
			return err
		}
		if err := tx.Where("agent_id = ?", agentID).Delete(&WireGuardClientRow{}).Error; err != nil {
			return err
		}
		if err := tx.Where("agent_id = ?", agentID).Delete(&WireGuardProfileRow{}).Error; err != nil {
			return err
		}
		return tx.Where("id = ?", agentID).Delete(&AgentRow{}).Error
	})
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

func (s *GormStore) SaveWireGuardProfiles(ctx context.Context, agentID string, profiles []WireGuardProfileRow) error {
	if !s.wireGuard {
		return fmt.Errorf("wireguard disabled")
	}
	if agentID == "" {
		agentID = s.localAgentID
	}

	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return s.saveWireGuardProfilesTx(tx, agentID, profiles)
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

func (s *GormStore) SaveWireGuardClients(ctx context.Context, agentID string, profileID int, clients []WireGuardClientRow) error {
	if !s.wireGuard {
		return fmt.Errorf("wireguard disabled")
	}
	if agentID == "" {
		agentID = s.localAgentID
	}
	if profileID <= 0 {
		return fmt.Errorf("wireguard profile_id is required")
	}

	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return s.saveWireGuardClientsTx(tx, agentID, profileID, clients)
	})
}

func (s *GormStore) SaveWireGuardClientProfileMutation(ctx context.Context, agentID string, profileID int, clients []WireGuardClientRow, profiles []WireGuardProfileRow) error {
	if !s.wireGuard {
		return fmt.Errorf("wireguard disabled")
	}
	if agentID == "" {
		agentID = s.localAgentID
	}
	if profileID <= 0 {
		return fmt.Errorf("wireguard profile_id is required")
	}

	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := s.saveWireGuardClientsTx(tx, agentID, profileID, clients); err != nil {
			return err
		}
		return s.saveWireGuardProfilesTx(tx, agentID, profiles)
	})
}

func (s *GormStore) MutateWireGuardClientProfile(ctx context.Context, agentID string, profileID int, mutate func(WireGuardClientProfileMutation) (WireGuardClientProfileMutation, error)) error {
	if !s.wireGuard {
		return fmt.Errorf("wireguard disabled")
	}
	if agentID == "" {
		agentID = s.localAgentID
	}
	if profileID <= 0 {
		return fmt.Errorf("wireguard profile_id is required")
	}
	if mutate == nil {
		return fmt.Errorf("wireguard mutation callback is required")
	}

	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("UPDATE local_agent_state SET desired_revision = desired_revision WHERE id = ?", 1).Error; err != nil {
			return err
		}

		var profiles []WireGuardProfileRow
		if err := tx.Where("agent_id = ?", agentID).Order("id").Find(&profiles).Error; err != nil {
			return err
		}
		profileIndex := -1
		for i := range profiles {
			normalizeWireGuardProfileRow(&profiles[i])
			if profiles[i].ID == profileID {
				profileIndex = i
			}
		}

		var clients []WireGuardClientRow
		if err := tx.Where("agent_id = ? AND profile_id = ?", agentID, profileID).Order("id").Find(&clients).Error; err != nil {
			return err
		}
		for i := range clients {
			normalizeWireGuardClientRow(&clients[i])
		}
		var maxClientID int
		if err := tx.Model(&WireGuardClientRow{}).Select("COALESCE(MAX(id), 0)").Scan(&maxClientID).Error; err != nil {
			return err
		}

		next, err := mutate(WireGuardClientProfileMutation{
			Profiles:     profiles,
			ProfileIndex: profileIndex,
			Clients:      clients,
			NextClientID: maxClientID + 1,
		})
		if err != nil {
			return err
		}
		if err := s.saveWireGuardClientsTx(tx, agentID, profileID, next.Clients); err != nil {
			return err
		}
		return s.saveWireGuardProfilesTx(tx, agentID, next.Profiles)
	})
}

func (s *GormStore) saveWireGuardProfilesTx(tx *gorm.DB, agentID string, profiles []WireGuardProfileRow) error {
	if err := tx.Where("agent_id = ?", agentID).Delete(&WireGuardProfileRow{}).Error; err != nil {
		return err
	}

	nextProfileIDs := make([]int, 0, len(profiles))
	if len(profiles) > 0 {
		rows := make([]WireGuardProfileRow, 0, len(profiles))
		for _, row := range profiles {
			row.AgentID = agentID
			normalizeWireGuardProfileRow(&row)
			rows = append(rows, row)
			if row.ID > 0 {
				nextProfileIDs = append(nextProfileIDs, row.ID)
			}
		}
		if err := tx.Create(&rows).Error; err != nil {
			return err
		}
	}

	clientCleanup := tx.Where("agent_id = ?", agentID)
	if len(nextProfileIDs) > 0 {
		clientCleanup = clientCleanup.Where("profile_id NOT IN ?", nextProfileIDs)
	}
	return clientCleanup.Delete(&WireGuardClientRow{}).Error
}

func (s *GormStore) saveWireGuardClientsTx(tx *gorm.DB, agentID string, profileID int, clients []WireGuardClientRow) error {
	if profileID <= 0 {
		return fmt.Errorf("wireguard profile_id is required")
	}
	if err := tx.Where("agent_id = ? AND profile_id = ?", agentID, profileID).Delete(&WireGuardClientRow{}).Error; err != nil {
		return err
	}

	if len(clients) == 0 {
		return nil
	}

	rows := make([]WireGuardClientRow, 0, len(clients))
	for _, row := range clients {
		row.AgentID = agentID
		row.ProfileID = profileID
		normalizeWireGuardClientRow(&row)
		rows = append(rows, row)
	}
	return tx.Create(&rows).Error
}

func (s *GormStore) SaveManagedCertificates(ctx context.Context, certs []ManagedCertificateRow) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(&ManagedCertificateRow{}).Error; err != nil {
			return err
		}

		if len(certs) == 0 {
			return nil
		}
		rows := make([]ManagedCertificateRow, 0, len(certs))
		for _, row := range certs {
			normalizeManagedCertificateRow(&row)
			rows = append(rows, row)
		}
		return tx.Create(&rows).Error
	})
}

func (s *GormStore) CleanupManagedCertificateMaterial(_ context.Context, previous []ManagedCertificateRow, next []ManagedCertificateRow) error {
	previousDomains := managedCertificateDomainSet(previous)
	nextDomains := managedCertificateDomainSet(next)
	baseDir := filepath.Join(s.dataRoot, "managed_certificates")
	for domain := range previousDomains {
		if _, ok := nextDomains[domain]; ok {
			continue
		}
		if err := os.RemoveAll(filepath.Join(baseDir, domain)); err != nil {
			return err
		}
	}
	return nil
}

func (s *GormStore) LoadManagedCertificateMaterial(_ context.Context, domain string) (ManagedCertificateBundle, bool, error) {
	material, ok := s.readManagedCertificateMaterial(domain)
	if !ok {
		return ManagedCertificateBundle{}, false, nil
	}
	return ManagedCertificateBundle{
		Domain:  strings.TrimSpace(domain),
		CertPEM: material.CertPEM,
		KeyPEM:  material.KeyPEM,
	}, true, nil
}

func (s *GormStore) SaveManagedCertificateMaterial(_ context.Context, domain string, bundle ManagedCertificateBundle) error {
	certDir := s.managedCertificateDirectory(domain)
	if err := os.MkdirAll(certDir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(certDir, "cert"), []byte(bundle.CertPEM), 0o600); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(certDir, "key"), []byte(bundle.KeyPEM), 0o600); err != nil {
		return err
	}
	return nil
}

func (s *GormStore) initializeSchema(ctx context.Context) error {
	return BootstrapSQLiteSchema(ctx, s.db)
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
	row.TrafficBlocked = row.TrafficBlocked
	row.TrafficBlockReason = defaultString(row.TrafficBlockReason, "")
	row.LastSeenAt = defaultString(row.LastSeenAt, "")
	row.LastSeenIP = defaultString(row.LastSeenIP, "")
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
	if !row.WireGuardEntryEnabled {
		row.WireGuardProfileID = nil
		row.WireGuardEntryListenHost = ""
		row.WireGuardEntryListenPort = 0
	}
	row.WireGuardEntryListenHost = defaultString(row.WireGuardEntryListenHost, "")
}

func normalizeLocalAgentStateRow(row *LocalAgentStateRow) {
	row.LastApplyStatus = defaultString(row.LastApplyStatus, "success")
	row.LastApplyMessage = defaultString(row.LastApplyMessage, "")
	row.DesiredVersion = defaultString(row.DesiredVersion, "")
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
	row.WireGuardInboundMode = normalizeWireGuardInboundMode(row.ListenMode, row.WireGuardInboundMode)
	row.WireGuardListenHost = defaultString(row.WireGuardListenHost, "")
	row.ProxyEntryAuthJSON = defaultJSON(row.ProxyEntryAuthJSON, "{}")
	row.EgressProfileID = copyOptionalPositiveInt(row.EgressProfileID)
	row.TagsJSON = defaultJSON(row.TagsJSON, "[]")
}

func normalizeWireGuardInboundMode(listenMode string, inboundMode string) string {
	if !strings.EqualFold(strings.TrimSpace(listenMode), "wireguard") {
		return ""
	}
	switch strings.ToLower(strings.TrimSpace(inboundMode)) {
	case "transparent":
		return "transparent"
	default:
		return "address"
	}
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

func normalizeWireGuardProfileRow(row *WireGuardProfileRow) {
	row.Name = defaultString(row.Name, "")
	row.Mode = defaultString(row.Mode, "generic_wireguard")
	row.PrivateKey = defaultString(row.PrivateKey, "")
	row.PublicEndpoint = defaultString(row.PublicEndpoint, "")
	row.AddressesJSON = defaultJSON(row.AddressesJSON, "[]")
	row.BindAddressesJSON = defaultJSON(row.BindAddressesJSON, "[]")
	row.PeersJSON = defaultJSON(row.PeersJSON, "[]")
	row.DNSJSON = defaultJSON(row.DNSJSON, "[]")
	row.TagsJSON = defaultJSON(row.TagsJSON, "[]")
}

func normalizeEgressProfileRow(row *EgressProfileRow) {
	row.Name = defaultString(row.Name, "")
	row.Type = defaultString(row.Type, "")
	row.ProxyURL = defaultString(row.ProxyURL, "")
	row.WireGuardConfigJSON = defaultString(row.WireGuardConfigJSON, "")
	row.Description = defaultString(row.Description, "")
}

func egressProfileRowPayload(row EgressProfileRow) map[string]any {
	normalizeEgressProfileRow(&row)
	return map[string]any{
		"id":                    row.ID,
		"name":                  row.Name,
		"type":                  row.Type,
		"proxy_url":             row.ProxyURL,
		"wireguard_config_json": row.WireGuardConfigJSON,
		"enabled":               row.Enabled,
		"description":           row.Description,
		"revision":              row.Revision,
	}
}

func normalizeWireGuardClientRow(row *WireGuardClientRow) {
	row.Name = defaultString(row.Name, "")
	row.PrivateKey = defaultString(row.PrivateKey, "")
	row.PublicKey = defaultString(row.PublicKey, "")
	row.PresharedKey = defaultString(row.PresharedKey, "")
	row.Address = defaultString(row.Address, "")
	row.AllowedIPsJSON = defaultJSON(row.AllowedIPsJSON, "[]")
	row.DNSJSON = defaultJSON(row.DNSJSON, "[]")
	row.CreatedAt = defaultString(row.CreatedAt, "")
	row.UpdatedAt = defaultString(row.UpdatedAt, "")
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

func computeDesiredRevision(
	localState LocalAgentStateRow,
	httpRows []HTTPRuleRow,
	l4Rows []L4RuleRow,
	relayRows []RelayListenerRow,
	wireGuardRows []WireGuardProfileRow,
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
		highestWireGuardProfileRevision(wireGuardRows),
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

func highestWireGuardProfileRevision(rows []WireGuardProfileRow) int {
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
		if !row.Enabled {
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

	syncRows := append([]RelayListenerRow(nil), localRows...)
	referencedIDs := referencedRelayListenerIDs(httpRows, l4Rows)
	transitIDs, err := s.transitDownstreamWireGuardRelayListenerIDs(ctx, agentID, localRows)
	if err != nil {
		return nil, err
	}
	referencedIDs = append(referencedIDs, transitIDs...)
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

func (s *GormStore) transitDownstreamWireGuardRelayListenerIDs(ctx context.Context, agentID string, localRows []RelayListenerRow) ([]int, error) {
	localRelayIDs := make(map[int]struct{})
	for _, row := range localRows {
		if row.ID <= 0 || !row.Enabled {
			continue
		}
		localRelayIDs[row.ID] = struct{}{}
	}
	if len(localRelayIDs) == 0 {
		return nil, nil
	}

	allRelayRows, err := s.ListRelayListeners(ctx, "")
	if err != nil {
		return nil, err
	}
	relayRowsByID := make(map[int]RelayListenerRow, len(allRelayRows))
	for _, row := range allRelayRows {
		if row.ID > 0 {
			relayRowsByID[row.ID] = row
		}
	}

	downstreamSet := make(map[int]struct{})
	addFromLayers := func(layersJSON string) {
		layers := parseIntLayers(layersJSON)
		for i := 0; i+1 < len(layers); i++ {
			if !intLayerIntersects(layers[i], localRelayIDs) {
				continue
			}
			for _, listenerID := range layers[i+1] {
				row, ok := relayRowsByID[listenerID]
				if !ok || row.AgentID == agentID || !row.Enabled ||
					!strings.EqualFold(strings.TrimSpace(row.TransportMode), "wireguard") {
					continue
				}
				downstreamSet[listenerID] = struct{}{}
			}
		}
	}

	allHTTPRows, err := s.loadAllHTTPRulesForSnapshot(ctx)
	if err != nil {
		return nil, err
	}
	for _, row := range allHTTPRows {
		if row.Enabled {
			addFromLayers(row.RelayLayersJSON)
		}
	}
	allL4Rows, err := s.loadAllL4RulesForSnapshot(ctx)
	if err != nil {
		return nil, err
	}
	for _, row := range filterSyncL4RuleRows(allL4Rows) {
		if row.Enabled {
			addFromLayers(row.RelayLayersJSON)
		}
	}

	ids := make([]int, 0, len(downstreamSet))
	for listenerID := range downstreamSet {
		ids = append(ids, listenerID)
	}
	sort.Ints(ids)
	return ids, nil
}

func (s *GormStore) loadWireGuardProfilesForSync(ctx context.Context, agentID string) ([]WireGuardProfileRow, error) {
	return s.ListWireGuardProfiles(ctx, agentID)
}

func (s *GormStore) attachWireGuardRelayPeersForSnapshot(
	ctx context.Context,
	agentID string,
	profiles []WireGuardProfileRow,
	httpRows []HTTPRuleRow,
	l4Rows []L4RuleRow,
	relayRows []RelayListenerRow,
) ([]WireGuardProfileRow, error) {
	if len(profiles) == 0 || len(relayRows) == 0 {
		return profiles, nil
	}
	localIndex := defaultWireGuardProfileIndex(profiles)
	if localIndex < 0 {
		localIndex = firstEnabledWireGuardProfileIndex(profiles)
	}
	if localIndex < 0 {
		return profiles, nil
	}

	remoteProfileIDsByAgent := make(map[string]map[int]struct{})
	for _, relayRow := range relayRows {
		if relayRow.AgentID == agentID ||
			!relayRow.Enabled ||
			!strings.EqualFold(strings.TrimSpace(relayRow.TransportMode), "wireguard") ||
			relayRow.WireGuardProfileID == nil ||
			*relayRow.WireGuardProfileID <= 0 {
			continue
		}
		ownerAgentID := strings.TrimSpace(relayRow.AgentID)
		if ownerAgentID == "" {
			continue
		}
		if _, ok := remoteProfileIDsByAgent[ownerAgentID]; !ok {
			remoteProfileIDsByAgent[ownerAgentID] = make(map[int]struct{})
		}
		remoteProfileIDsByAgent[ownerAgentID][*relayRow.WireGuardProfileID] = struct{}{}
	}
	if len(remoteProfileIDsByAgent) == 0 {
		return s.attachWireGuardRelayOwnerPeersForSnapshot(ctx, agentID, profiles, httpRows, l4Rows, relayRows)
	}

	remoteProfiles := make(map[string]WireGuardProfileRow)
	for ownerAgentID, profileIDs := range remoteProfileIDsByAgent {
		rows, err := s.ListWireGuardProfiles(ctx, ownerAgentID)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			if _, ok := profileIDs[row.ID]; !ok || !row.Enabled {
				continue
			}
			remoteProfiles[wireGuardProfileGraphKey(row.AgentID, row.ID)] = row
		}
	}
	if len(remoteProfiles) == 0 {
		return s.attachWireGuardRelayOwnerPeersForSnapshot(ctx, agentID, profiles, httpRows, l4Rows, relayRows)
	}

	next := append([]WireGuardProfileRow(nil), profiles...)
	localProfile := next[localIndex]
	peers := parseWireGuardPeers(localProfile.PeersJSON)
	changed := false
	syntheticPeerRevision := highestRelayListenerRevision(relayRows)
	for _, relayRow := range relayRows {
		if relayRow.AgentID == agentID ||
			!relayRow.Enabled ||
			!strings.EqualFold(strings.TrimSpace(relayRow.TransportMode), "wireguard") ||
			relayRow.WireGuardProfileID == nil ||
			*relayRow.WireGuardProfileID <= 0 {
			continue
		}
		remoteProfile, ok := remoteProfiles[wireGuardProfileGraphKey(relayRow.AgentID, *relayRow.WireGuardProfileID)]
		if !ok {
			continue
		}
		peer, ok := snapshotWireGuardRelayPeer(relayRow, remoteProfile)
		if !ok {
			continue
		}
		if wireGuardPeersContainPublicKey(peers, peer.PublicKey) {
			continue
		}
		peers = append(peers, peer)
		changed = true
		syntheticPeerRevision = maxInt(syntheticPeerRevision, remoteProfile.Revision)
	}
	if !changed {
		return s.attachWireGuardRelayOwnerPeersForSnapshot(ctx, agentID, profiles, httpRows, l4Rows, relayRows)
	}
	next[localIndex].PeersJSON = marshalSnapshotWireGuardPeers(peers)
	next[localIndex].Revision = maxInt(next[localIndex].Revision, syntheticPeerRevision)
	return s.attachWireGuardRelayOwnerPeersForSnapshot(ctx, agentID, next, httpRows, l4Rows, relayRows)
}

func (s *GormStore) attachWireGuardRelayOwnerPeersForSnapshot(
	ctx context.Context,
	agentID string,
	profiles []WireGuardProfileRow,
	httpRows []HTTPRuleRow,
	l4Rows []L4RuleRow,
	relayRows []RelayListenerRow,
) ([]WireGuardProfileRow, error) {
	localWireGuardRelayProfileIDs := make(map[int]struct{})
	localRelayIDs := make(map[int]struct{})
	for _, relayRow := range relayRows {
		if relayRow.AgentID != agentID ||
			!relayRow.Enabled ||
			!strings.EqualFold(strings.TrimSpace(relayRow.TransportMode), "wireguard") ||
			relayRow.WireGuardProfileID == nil ||
			*relayRow.WireGuardProfileID <= 0 {
			continue
		}
		localRelayIDs[relayRow.ID] = struct{}{}
		localWireGuardRelayProfileIDs[*relayRow.WireGuardProfileID] = struct{}{}
	}
	if len(localRelayIDs) == 0 {
		return profiles, nil
	}

	callerAgentIDs, err := s.wireGuardRelayCallerAgentIDs(ctx, agentID, localRelayIDs, httpRows, l4Rows)
	if err != nil {
		return nil, err
	}
	if len(callerAgentIDs) == 0 {
		return profiles, nil
	}

	callerProfiles := make([]WireGuardProfileRow, 0, len(callerAgentIDs))
	for _, callerAgentID := range callerAgentIDs {
		rows, err := s.ListWireGuardProfiles(ctx, callerAgentID)
		if err != nil {
			return nil, err
		}
		index := defaultWireGuardProfileIndex(rows)
		if index < 0 {
			index = firstEnabledWireGuardProfileIndex(rows)
		}
		if index >= 0 {
			callerProfiles = append(callerProfiles, rows[index])
		}
	}
	if len(callerProfiles) == 0 {
		return profiles, nil
	}

	next := append([]WireGuardProfileRow(nil), profiles...)
	changed := false
	syntheticPeerRevision := highestRelayListenerRevision(relayRows)
	for i := range next {
		if _, ok := localWireGuardRelayProfileIDs[next[i].ID]; !ok || !next[i].Enabled {
			continue
		}
		peers := parseWireGuardPeers(next[i].PeersJSON)
		profileChanged := false
		for _, callerProfile := range callerProfiles {
			peer, ok := snapshotWireGuardProfilePeerAllowEmptyEndpoint("system:relay-caller:"+strings.TrimSpace(callerProfile.AgentID), callerProfile)
			if !ok || wireGuardPeersContainPublicKey(peers, peer.PublicKey) {
				continue
			}
			peers = append(peers, peer)
			profileChanged = true
			syntheticPeerRevision = maxInt(syntheticPeerRevision, callerProfile.Revision)
		}
		if profileChanged {
			next[i].PeersJSON = marshalSnapshotWireGuardPeers(peers)
			next[i].Revision = maxInt(next[i].Revision, syntheticPeerRevision)
			changed = true
		}
	}
	if !changed {
		return profiles, nil
	}
	return next, nil
}

func (s *GormStore) wireGuardRelayCallerAgentIDs(
	ctx context.Context,
	agentID string,
	localRelayIDs map[int]struct{},
	httpRows []HTTPRuleRow,
	l4Rows []L4RuleRow,
) ([]string, error) {
	callerSet := make(map[string]struct{})
	addCaller := func(rowAgentID string) {
		rowAgentID = strings.TrimSpace(rowAgentID)
		if rowAgentID == "" || rowAgentID == agentID {
			return
		}
		callerSet[rowAgentID] = struct{}{}
	}
	allRelayRows, err := s.ListRelayListeners(ctx, "")
	if err != nil {
		return nil, err
	}
	relayAgentByID := make(map[int]string, len(allRelayRows))
	for _, row := range allRelayRows {
		if row.ID > 0 {
			relayAgentByID[row.ID] = strings.TrimSpace(row.AgentID)
		}
	}
	addIfReferences := func(rowAgentID string, relayLayersJSON string) {
		layers := parseIntLayers(relayLayersJSON)
		for i, layer := range layers {
			if !intLayerIntersects(layer, localRelayIDs) {
				continue
			}
			if i == 0 {
				addCaller(rowAgentID)
				continue
			}
			for _, previousListenerID := range layers[i-1] {
				addCaller(relayAgentByID[previousListenerID])
			}
		}
	}

	for _, row := range httpRows {
		if row.Enabled {
			addIfReferences(row.AgentID, row.RelayLayersJSON)
		}
	}
	for _, row := range l4Rows {
		if row.Enabled {
			addIfReferences(row.AgentID, row.RelayLayersJSON)
		}
	}

	allHTTPRows, err := s.loadAllHTTPRulesForSnapshot(ctx)
	if err != nil {
		return nil, err
	}
	for _, row := range allHTTPRows {
		if row.Enabled {
			addIfReferences(row.AgentID, row.RelayLayersJSON)
		}
	}
	allL4Rows, err := s.loadAllL4RulesForSnapshot(ctx)
	if err != nil {
		return nil, err
	}
	for _, row := range filterSyncL4RuleRows(allL4Rows) {
		if row.Enabled {
			addIfReferences(row.AgentID, row.RelayLayersJSON)
		}
	}

	agentIDs := make([]string, 0, len(callerSet))
	for callerAgentID := range callerSet {
		agentIDs = append(agentIDs, callerAgentID)
	}
	sort.Strings(agentIDs)
	return agentIDs, nil
}

func intLayerIntersects(layer []int, ids map[int]struct{}) bool {
	if len(layer) == 0 || len(ids) == 0 {
		return false
	}
	for _, value := range layer {
		if _, ok := ids[value]; ok {
			return true
		}
	}
	return false
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

func defaultWireGuardProfileIndex(profiles []WireGuardProfileRow) int {
	for i, row := range profiles {
		if row.Enabled && hasStringValue(parseStringSlice(row.TagsJSON), "system:default-wireguard") {
			return i
		}
	}
	return -1
}

func firstEnabledWireGuardProfileIndex(profiles []WireGuardProfileRow) int {
	for i, row := range profiles {
		if row.Enabled {
			return i
		}
	}
	return -1
}

func snapshotWireGuardRelayPeer(relayRow RelayListenerRow, remoteProfile WireGuardProfileRow) (WireGuardPeer, bool) {
	endpoint := defaultString(remoteProfile.PublicEndpoint, relayPublicEndpoint(relayRow))
	peer, ok := snapshotWireGuardProfilePeer("system:relay-listener:"+strconv.Itoa(relayRow.ID), withWireGuardProfileEndpoint(remoteProfile, endpoint))
	if !ok {
		return WireGuardPeer{}, false
	}
	peer.AllowedIPs = appendUniqueStrings(peer.AllowedIPs, relayListenerTunnelAllowedIPs(relayRow)...)
	return peer, len(peer.AllowedIPs) > 0
}

func snapshotWireGuardProfilePeer(name string, profile WireGuardProfileRow) (WireGuardPeer, bool) {
	return snapshotWireGuardProfilePeerWithEndpointPolicy(name, profile, true)
}

func snapshotWireGuardProfilePeerAllowEmptyEndpoint(name string, profile WireGuardProfileRow) (WireGuardPeer, bool) {
	return snapshotWireGuardProfilePeerWithEndpointPolicy(name, profile, false)
}

func snapshotWireGuardProfilePeerWithEndpointPolicy(name string, profile WireGuardProfileRow, requireEndpoint bool) (WireGuardPeer, bool) {
	publicKey, err := wireGuardPublicKeyFromPrivateKey(profile.PrivateKey)
	if err != nil {
		return WireGuardPeer{}, false
	}
	endpoint := strings.TrimSpace(profile.PublicEndpoint)
	allowedIPs := wireGuardProfileHostAllowedIPs(parseStringSlice(profile.AddressesJSON))
	if len(allowedIPs) == 0 || (requireEndpoint && endpoint == "") {
		return WireGuardPeer{}, false
	}
	return WireGuardPeer{
		Name:                       name,
		PublicKey:                  publicKey,
		Endpoint:                   endpoint,
		AllowedIPs:                 allowedIPs,
		PersistentKeepaliveSeconds: 25,
	}, true
}

func withWireGuardProfileEndpoint(profile WireGuardProfileRow, endpoint string) WireGuardProfileRow {
	profile.PublicEndpoint = endpoint
	return profile
}

func relayPublicEndpoint(row RelayListenerRow) string {
	host := strings.TrimSpace(row.PublicHost)
	if host == "" {
		host = strings.TrimSpace(row.ListenHost)
	}
	port := row.PublicPort
	if port <= 0 {
		port = row.ListenPort
	}
	if host == "" || port <= 0 {
		return ""
	}
	return net.JoinHostPort(host, strconv.Itoa(port))
}

func wireGuardProfileHostAllowedIPs(addresses []string) []string {
	allowed := make([]string, 0, len(addresses))
	for _, address := range addresses {
		addr, ok := wireGuardAddressHostPrefix(address)
		if ok {
			allowed = append(allowed, addr)
		}
	}
	return allowed
}

func relayListenerTunnelAllowedIPs(row RelayListenerRow) []string {
	values := make([]string, 0, 1+len(parseStringSlice(row.BindHostsJSON)))
	if host := strings.TrimSpace(row.ListenHost); host != "" {
		values = append(values, host)
	}
	values = append(values, parseStringSlice(row.BindHostsJSON)...)
	allowed := make([]string, 0, len(values))
	for _, value := range values {
		if allowedIP, ok := wireGuardAddressHostPrefix(value); ok {
			allowed = append(allowed, allowedIP)
		}
	}
	return allowed
}

func appendUniqueStrings(values []string, additions ...string) []string {
	seen := make(map[string]struct{}, len(values)+len(additions))
	next := make([]string, 0, len(values)+len(additions))
	for _, value := range append(values, additions...) {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		next = append(next, trimmed)
	}
	return next
}

func wireGuardAddressHostPrefix(address string) (string, bool) {
	prefix, err := netip.ParsePrefix(strings.TrimSpace(address))
	if err != nil {
		addr, addrErr := netip.ParseAddr(strings.TrimSpace(address))
		if addrErr != nil {
			return "", false
		}
		if addr.Is4() {
			return netip.PrefixFrom(addr, 32).String(), true
		}
		return netip.PrefixFrom(addr, 128).String(), true
	}
	addr := prefix.Addr()
	if addr.Is4() {
		return netip.PrefixFrom(addr, 32).String(), true
	}
	return netip.PrefixFrom(addr, 128).String(), true
}

func wireGuardPeersContainPublicKey(peers []WireGuardPeer, publicKey string) bool {
	publicKey = strings.TrimSpace(publicKey)
	if publicKey == "" {
		return false
	}
	for _, peer := range peers {
		if strings.TrimSpace(peer.PublicKey) == publicKey {
			return true
		}
	}
	return false
}

func marshalSnapshotWireGuardPeers(peers []WireGuardPeer) string {
	data, err := json.Marshal(peers)
	if err != nil {
		return "[]"
	}
	return string(data)
}

func wireGuardPublicKeyFromPrivateKey(privateKey string) (string, error) {
	privateBytes, err := base64.StdEncoding.DecodeString(strings.TrimSpace(privateKey))
	if err != nil || len(privateBytes) != 32 {
		return "", fmt.Errorf("invalid key")
	}
	privateBytes[0] &= 248
	privateBytes[31] &= 127
	privateBytes[31] |= 64
	publicBytes, err := curve25519.X25519(privateBytes, curve25519.Basepoint)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(publicBytes), nil
}

func wireGuardProfileGraphKey(agentID string, profileID int) string {
	return strings.TrimSpace(agentID) + ":" + strconv.Itoa(profileID)
}

func hasStringValue(values []string, want string) bool {
	want = strings.TrimSpace(want)
	for _, value := range values {
		if strings.TrimSpace(value) == want {
			return true
		}
	}
	return false
}

func filterWireGuardProfilesForSnapshotGraph(
	agentID string,
	profiles []WireGuardProfileRow,
	httpRows []HTTPRuleRow,
	l4Rows []L4RuleRow,
	relayRows []RelayListenerRow,
	wireGuardClientRows []WireGuardClientRow,
) []WireGuardProfileRow {
	if len(profiles) == 0 {
		return profiles
	}

	referenced := referencedWireGuardProfileIDs(agentID, httpRows, l4Rows, relayRows, wireGuardClientRows)
	filtered := make([]WireGuardProfileRow, 0, len(profiles))
	for _, row := range profiles {
		if _, ok := referenced[row.ID]; ok || wireGuardProfileHasRuntimePeer(row) {
			filtered = append(filtered, row)
		}
	}
	return filtered
}

func wireGuardProfileHasRuntimePeer(row WireGuardProfileRow) bool {
	if row.AgentID == "" || !row.Enabled {
		return false
	}
	for _, peer := range parseWireGuardPeers(row.PeersJSON) {
		if !wireGuardPeerHasRuntimeConfig(peer) {
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(peer.Name), "system:") {
			continue
		}
		return true
	}
	for _, peer := range parseWireGuardPeers(row.PeersJSON) {
		if strings.HasPrefix(strings.TrimSpace(peer.Name), "system:relay-listener:") {
			return true
		}
	}
	return false
}

func wireGuardPeerHasRuntimeConfig(peer WireGuardPeer) bool {
	return strings.TrimSpace(peer.PublicKey) != "" ||
		strings.TrimSpace(peer.Endpoint) != "" ||
		len(peer.AllowedIPs) > 0 ||
		strings.TrimSpace(peer.PresharedKey) != "" ||
		len(peer.Reserved) > 0 ||
		peer.PersistentKeepaliveSeconds > 0
}

func referencedWireGuardProfileIDs(
	agentID string,
	httpRows []HTTPRuleRow,
	l4Rows []L4RuleRow,
	relayRows []RelayListenerRow,
	wireGuardClientRows []WireGuardClientRow,
) map[int]struct{} {
	referenced := make(map[int]struct{})
	add := func(profileID *int) {
		if profileID == nil || *profileID <= 0 {
			return
		}
		referenced[*profileID] = struct{}{}
	}

	for _, row := range httpRows {
		if !row.Enabled || !row.WireGuardEntryEnabled {
			continue
		}
		add(row.WireGuardProfileID)
	}
	for _, row := range l4Rows {
		if !row.Enabled {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(row.ListenMode), "wireguard") {
			add(row.WireGuardProfileID)
		}
	}
	for _, row := range relayRows {
		if row.AgentID != agentID ||
			!row.Enabled ||
			!strings.EqualFold(strings.TrimSpace(row.TransportMode), "wireguard") {
			continue
		}
		add(row.WireGuardProfileID)
	}
	for _, row := range wireGuardClientRows {
		if row.AgentID != agentID || !row.Enabled || row.ProfileID <= 0 {
			continue
		}
		profileID := row.ProfileID
		add(&profileID)
	}
	return referenced
}

func (s *GormStore) agentSupportsWireGuardSnapshots(ctx context.Context, agentID string) (bool, error) {
	if strings.TrimSpace(agentID) == s.localAgentID {
		return true, nil
	}
	var row AgentRow
	err := s.db.WithContext(ctx).
		Select("id", "capabilities").
		Where("id = ?", strings.TrimSpace(agentID)).
		First(&row).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		}
		return false, err
	}
	for _, capability := range parseStringSlice(row.CapabilitiesJSON) {
		if capability == "wireguard" {
			return true, nil
		}
	}
	return false, nil
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
	if listenMode == "proxy" {
		if row.ListenPort < 1 || row.ListenPort > 65535 {
			return false
		}
		return true
	}
	if listenMode == "wireguard" {
		if row.WireGuardProfileID == nil {
			return false
		}
		inboundMode := normalizeWireGuardInboundMode(row.ListenMode, row.WireGuardInboundMode)
		if inboundMode == "transparent" {
			if row.ListenPort < 0 || row.ListenPort > 65535 {
				return false
			}
			return true
		}
		if row.ListenPort < 1 || row.ListenPort > 65535 {
			return false
		}
	}

	if row.ListenPort < 1 || row.ListenPort > 65535 {
		return false
	}
	return len(parseL4Backends(row.BackendsJSON)) > 0
}

func filterHTTPRuleRowsWithoutWireGuard(rows []HTTPRuleRow, relayRows []RelayListenerRow) []HTTPRuleRow {
	relayIDs := relayListenerIDSet(relayRows)
	filtered := make([]HTTPRuleRow, 0, len(rows))
	for _, row := range rows {
		if row.WireGuardEntryEnabled ||
			positiveOptionalInt(row.WireGuardProfileID) ||
			relayLayersReferenceMissingListener(row.RelayLayersJSON, relayIDs) {
			continue
		}
		filtered = append(filtered, row)
	}
	return filtered
}

func filterL4RuleRowsWithoutWireGuard(rows []L4RuleRow, relayRows []RelayListenerRow) []L4RuleRow {
	relayIDs := relayListenerIDSet(relayRows)
	filtered := make([]L4RuleRow, 0, len(rows))
	for _, row := range rows {
		if strings.EqualFold(strings.TrimSpace(row.ListenMode), "wireguard") ||
			positiveOptionalInt(row.WireGuardProfileID) ||
			relayLayersReferenceMissingListener(row.RelayLayersJSON, relayIDs) {
			continue
		}
		filtered = append(filtered, row)
	}
	return filtered
}

func filterRelayListenerRowsWithoutWireGuard(rows []RelayListenerRow) []RelayListenerRow {
	filtered := make([]RelayListenerRow, 0, len(rows))
	for _, row := range rows {
		if strings.EqualFold(strings.TrimSpace(row.TransportMode), "wireguard") ||
			positiveOptionalInt(row.WireGuardProfileID) {
			continue
		}
		filtered = append(filtered, row)
	}
	return filtered
}

func positiveOptionalInt(value *int) bool {
	return value != nil && *value > 0
}

func relayListenerIDSet(rows []RelayListenerRow) map[int]struct{} {
	ids := make(map[int]struct{}, len(rows))
	for _, row := range rows {
		if row.ID > 0 {
			ids[row.ID] = struct{}{}
		}
	}
	return ids
}

func relayLayersReferenceMissingListener(layersJSON string, available map[int]struct{}) bool {
	for _, layer := range parseIntLayers(layersJSON) {
		for _, listenerID := range layer {
			if listenerID <= 0 {
				continue
			}
			if _, ok := available[listenerID]; !ok {
				return true
			}
		}
	}
	return false
}

func SnapshotHTTPRules(rows []HTTPRuleRow) []HTTPRule {
	rules := make([]HTTPRule, 0, len(rows))
	for _, row := range rows {
		if !row.Enabled {
			continue
		}
		wireGuardEntryListenPort := row.WireGuardEntryListenPort
		if row.WireGuardEntryEnabled {
			if port, ok := snapshotHTTPFrontendListenPort(row.FrontendURL); ok {
				wireGuardEntryListenPort = port
			}
		}
		rules = append(rules, HTTPRule{
			ID:                       row.ID,
			AgentID:                  row.AgentID,
			FrontendURL:              row.FrontendURL,
			Backends:                 parseHTTPBackends(row.BackendsJSON),
			LoadBalancing:            parseLoadBalancingStrategy(row.LoadBalancingJSON),
			ProxyRedirect:            row.ProxyRedirect,
			PassProxyHeaders:         row.PassProxyHeaders,
			UserAgent:                row.UserAgent,
			CustomHeaders:            parseHTTPHeaders(row.CustomHeadersJSON),
			WireGuardEntryEnabled:    row.WireGuardEntryEnabled,
			WireGuardProfileID:       copyOptionalInt(row.WireGuardProfileID),
			EgressProfileID:          copyOptionalPositiveInt(row.EgressProfileID),
			WireGuardEntryListenHost: row.WireGuardEntryListenHost,
			WireGuardEntryListenPort: wireGuardEntryListenPort,
			RelayLayers:              parseIntLayers(row.RelayLayersJSON),
			RelayObfs:                row.RelayObfs,
			Revision:                 int64(row.Revision),
		})
	}
	return rules
}

func snapshotHTTPFrontendListenPort(raw string) (int, bool) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed == nil {
		return 0, false
	}
	if portText := parsed.Port(); portText != "" {
		port, err := strconv.Atoi(portText)
		return port, err == nil && port >= 1 && port <= 65535
	}
	switch strings.ToLower(parsed.Scheme) {
	case "https":
		return 443, true
	case "http":
		return 80, true
	default:
		return 0, false
	}
}

func SnapshotL4Rules(rows []L4RuleRow) []L4Rule {
	rules := make([]L4Rule, 0, len(rows))
	for _, row := range rows {
		if !row.Enabled {
			continue
		}
		rules = append(rules, L4Rule{
			ID:                   row.ID,
			AgentID:              row.AgentID,
			Name:                 row.Name,
			Protocol:             defaultString(row.Protocol, "tcp"),
			ListenHost:           defaultString(row.ListenHost, "0.0.0.0"),
			ListenPort:           row.ListenPort,
			Backends:             parseL4Backends(row.BackendsJSON),
			LoadBalancing:        parseLoadBalancingStrategy(row.LoadBalancingJSON),
			Tuning:               parseL4Tuning(row.TuningJSON),
			RelayLayers:          parseIntLayers(row.RelayLayersJSON),
			RelayObfs:            row.RelayObfs,
			ListenMode:           defaultString(row.ListenMode, "tcp"),
			WireGuardProfileID:   copyOptionalInt(row.WireGuardProfileID),
			EgressProfileID:      copyOptionalPositiveInt(row.EgressProfileID),
			WireGuardInboundMode: normalizeWireGuardInboundMode(row.ListenMode, row.WireGuardInboundMode),
			WireGuardListenHost:  row.WireGuardListenHost,
			ProxyEntryAuth:       parseL4ProxyEntryAuth(row.ProxyEntryAuthJSON),
			Revision:             int64(row.Revision),
		})
	}
	return rules
}

func SnapshotWireGuardProfiles(rows []WireGuardProfileRow) []WireGuardProfile {
	profiles := make([]WireGuardProfile, 0, len(rows))
	for _, row := range rows {
		if !row.Enabled {
			continue
		}
		profiles = append(profiles, WireGuardProfile{
			ID:             row.ID,
			AgentID:        row.AgentID,
			Name:           row.Name,
			Mode:           defaultString(row.Mode, "generic_wireguard"),
			PrivateKey:     row.PrivateKey,
			ListenPort:     row.ListenPort,
			PublicEndpoint: row.PublicEndpoint,
			BindAddresses:  parseStringSlice(row.BindAddressesJSON),
			Addresses:      parseStringSlice(row.AddressesJSON),
			Peers:          parseWireGuardPeers(row.PeersJSON),
			DNS:            parseStringSlice(row.DNSJSON),
			MTU:            row.MTU,
			Enabled:        row.Enabled,
			Tags:           parseStringSlice(row.TagsJSON),
			Revision:       int64(row.Revision),
		})
	}
	return profiles
}

func SnapshotEgressProfiles(rows []EgressProfileRow) []EgressProfile {
	profiles := make([]EgressProfile, 0, len(rows))
	for _, row := range rows {
		if !row.Enabled {
			continue
		}
		profiles = append(profiles, EgressProfile{
			ID:              row.ID,
			Name:            row.Name,
			Type:            row.Type,
			ProxyURL:        row.ProxyURL,
			WireGuardConfig: parseEgressWireGuardConfig(row.WireGuardConfigJSON),
			Enabled:         row.Enabled,
			Description:     row.Description,
			Revision:        row.Revision,
		})
	}
	return profiles
}

func parseEgressWireGuardConfig(raw string) *EgressWireGuardConfig {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var config EgressWireGuardConfig
	if err := json.Unmarshal([]byte(raw), &config); err != nil {
		return nil
	}
	return &config
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
			ID:                      row.ID,
			AgentID:                 row.AgentID,
			AgentName:               agentNames[row.AgentID],
			Name:                    row.Name,
			ListenHost:              defaultString(row.ListenHost, "0.0.0.0"),
			BindHosts:               parseStringSlice(row.BindHostsJSON),
			ListenPort:              row.ListenPort,
			PublicHost:              defaultString(row.PublicHost, row.ListenHost),
			PublicPort:              row.PublicPort,
			Enabled:                 row.Enabled,
			CertificateID:           copyOptionalInt(row.CertificateID),
			TLSMode:                 defaultString(row.TLSMode, "pin_or_ca"),
			TransportMode:           defaultString(row.TransportMode, "tls_tcp"),
			WireGuardProfileID:      copyOptionalInt(row.WireGuardProfileID),
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

func (s *GormStore) snapshotCertificateBundles(rows []ManagedCertificateRow) []ManagedCertificateBundle {
	bundles := make([]ManagedCertificateBundle, 0, len(rows))
	for _, row := range rows {
		if !row.Enabled {
			continue
		}
		material, ok := s.readManagedCertificateMaterial(row.Domain)
		if !ok {
			continue
		}
		bundles = append(bundles, ManagedCertificateBundle{
			ID:       row.ID,
			Domain:   row.Domain,
			Revision: int64(row.Revision),
			CertPEM:  material.CertPEM,
			KeyPEM:   material.KeyPEM,
		})
	}
	return bundles
}

func snapshotCertificatePolicies(rows []ManagedCertificateRow, agentID string) []ManagedCertificatePolicy {
	policies := make([]ManagedCertificatePolicy, 0, len(rows))
	for _, row := range rows {
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
	var values []HTTPBackend
	if err := json.Unmarshal([]byte(defaultString(raw, "[]")), &values); err != nil {
		return []HTTPBackend{}
	}
	normalized := make([]HTTPBackend, 0, len(values))
	for _, value := range values {
		url := strings.TrimSpace(value.URL)
		if url == "" {
			continue
		}
		normalized = append(normalized, HTTPBackend{URL: url})
	}
	return normalized
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
	var values []L4Backend
	if err := json.Unmarshal([]byte(defaultString(raw, "[]")), &values); err != nil {
		return []L4Backend{}
	}
	normalized := make([]L4Backend, 0, len(values))
	for _, value := range values {
		host := strings.TrimSpace(value.Host)
		if host == "" || value.Port < 1 || value.Port > 65535 {
			continue
		}
		normalized = append(normalized, L4Backend{Host: host, Port: value.Port})
	}
	return normalized
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
	report.LastError = report.LastError
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

func parseWireGuardPeers(raw string) []WireGuardPeer {
	var peers []WireGuardPeer
	if err := json.Unmarshal([]byte(defaultString(raw, "[]")), &peers); err != nil {
		return []WireGuardPeer{}
	}
	if peers == nil {
		return []WireGuardPeer{}
	}
	return peers
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

type managedCertificateMaterial struct {
	CertPEM string
	KeyPEM  string
}

func (s *GormStore) readManagedCertificateMaterial(domain string) (managedCertificateMaterial, bool) {
	certDir := s.managedCertificateDirectory(domain)
	certPEM, certErr := os.ReadFile(filepath.Join(certDir, "cert"))
	keyPEM, keyErr := os.ReadFile(filepath.Join(certDir, "key"))
	if certErr != nil || keyErr != nil {
		return managedCertificateMaterial{}, false
	}
	return managedCertificateMaterial{
		CertPEM: string(certPEM),
		KeyPEM:  string(keyPEM),
	}, true
}

func (s *GormStore) managedCertificateDirectory(domain string) string {
	return filepath.Join(s.dataRoot, "managed_certificates", normalizeManagedCertificateHost(domain))
}

func normalizeManagedCertificateHost(domain string) string {
	normalized := strings.TrimSpace(domain)
	if strings.HasPrefix(normalized, "[") && strings.HasSuffix(normalized, "]") && len(normalized) >= 2 {
		normalized = normalized[1 : len(normalized)-1]
	}
	normalized = strings.ReplaceAll(normalized, "*.", "_wildcard_.")
	replacer := strings.NewReplacer("<", "_", ">", "_", ":", "_", "\"", "_", "/", "_", "\\", "_", "|", "_", "?", "_", "*", "_")
	return replacer.Replace(normalized)
}

func managedCertificateDomainSet(rows []ManagedCertificateRow) map[string]struct{} {
	domains := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		domain := strings.TrimSpace(row.Domain)
		if domain == "" {
			continue
		}
		domains[normalizeManagedCertificateHost(domain)] = struct{}{}
	}
	return domains
}
