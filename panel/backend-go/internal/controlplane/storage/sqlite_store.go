package storage

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Store interface {
	ListAgents(context.Context) ([]AgentRow, error)
	ListHTTPRules(context.Context, string) ([]HTTPRuleRow, error)
	ListL4Rules(context.Context, string) ([]L4RuleRow, error)
	ListRelayListeners(context.Context, string) ([]RelayListenerRow, error)
	LoadLocalAgentState(context.Context) (LocalAgentStateRow, error)
	LoadAgentSnapshot(context.Context, string, AgentSnapshotInput) (Snapshot, error)
	ListVersionPolicies(context.Context) ([]VersionPolicyRow, error)
	ListManagedCertificates(context.Context) ([]ManagedCertificateRow, error)
	SaveAgent(context.Context, AgentRow) error
	SaveL4Rules(context.Context, string, []L4RuleRow) error
	SaveRelayListeners(context.Context, string, []RelayListenerRow) error
	SaveVersionPolicies(context.Context, []VersionPolicyRow) error
	SaveManagedCertificates(context.Context, []ManagedCertificateRow) error
	LoadManagedCertificateMaterial(context.Context, string) (ManagedCertificateBundle, bool, error)
	SaveManagedCertificateMaterial(context.Context, string, ManagedCertificateBundle) error
	CleanupManagedCertificateMaterial(context.Context, []ManagedCertificateRow, []ManagedCertificateRow) error
}

type SQLiteStore struct {
	db           *gorm.DB
	dataRoot     string
	localAgentID string
}

func NewSQLiteStore(dataRoot string, localAgentID string) (*SQLiteStore, error) {
	if err := os.MkdirAll(dataRoot, 0o755); err != nil {
		return nil, err
	}

	db, err := gorm.Open(sqlite.Open(filepath.Join(dataRoot, "panel.db")), &gorm.Config{})
	if err != nil {
		return nil, err
	}

	store := &SQLiteStore{db: db, dataRoot: dataRoot, localAgentID: localAgentID}
	if err := BootstrapSQLiteSchema(context.Background(), db); err != nil {
		if sqlDB, dbErr := db.DB(); dbErr == nil {
			_ = sqlDB.Close()
		}
		return nil, err
	}
	return store, nil
}

func (s *SQLiteStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	sqlDB, err := s.db.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}

func (s *SQLiteStore) ListAgents(ctx context.Context) ([]AgentRow, error) {
	var agents []AgentRow
	if err := s.db.WithContext(ctx).Order("id").Find(&agents).Error; err != nil {
		return nil, err
	}
	for i := range agents {
		normalizeAgentRow(&agents[i])
	}
	return agents, nil
}

func (s *SQLiteStore) ListHTTPRules(ctx context.Context, agentID string) ([]HTTPRuleRow, error) {
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

func (s *SQLiteStore) LoadLocalAgentState(ctx context.Context) (LocalAgentStateRow, error) {
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

func (s *SQLiteStore) LoadLocalSnapshot(ctx context.Context, agentID string) (Snapshot, error) {
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

func (s *SQLiteStore) LoadAgentSnapshot(ctx context.Context, agentID string, input AgentSnapshotInput) (Snapshot, error) {
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

	certRows, err := s.ListManagedCertificates(ctx)
	if err != nil {
		return Snapshot{}, err
	}

	versionPolicies, err := s.ListVersionPolicies(ctx)
	if err != nil {
		return Snapshot{}, err
	}

	relevantCertRows := filterManagedCertificatesForAgent(certRows, resolvedAgentID, relayRows)
	revisionState := LocalAgentStateRow{
		DesiredRevision: input.DesiredRevision,
		CurrentRevision: input.CurrentRevision,
	}

	return Snapshot{
		DesiredVersion:      strings.TrimSpace(input.DesiredVersion),
		Revision:            int64(computeDesiredRevision(revisionState, httpRows, l4Rows, relayRows, relevantCertRows)),
		VersionPackage:      resolveVersionPackageForPlatform(versionPolicies, input.DesiredVersion, input.Platform),
		Rules:               snapshotHTTPRules(httpRows),
		L4Rules:             snapshotL4Rules(l4Rows),
		RelayListeners:      snapshotRelayListeners(relayRows),
		Certificates:        s.snapshotCertificateBundles(relevantCertRows),
		CertificatePolicies: snapshotCertificatePolicies(relevantCertRows, resolvedAgentID),
	}, nil
}

func (s *SQLiteStore) ListL4Rules(ctx context.Context, agentID string) ([]L4RuleRow, error) {
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

func (s *SQLiteStore) ListVersionPolicies(ctx context.Context) ([]VersionPolicyRow, error) {
	var policies []VersionPolicyRow
	if err := s.db.WithContext(ctx).Order("id").Find(&policies).Error; err != nil {
		return nil, err
	}
	for i := range policies {
		normalizeVersionPolicyRow(&policies[i])
	}
	return policies, nil
}

func (s *SQLiteStore) ListRelayListeners(ctx context.Context, agentID string) ([]RelayListenerRow, error) {
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

func (s *SQLiteStore) ListManagedCertificates(ctx context.Context) ([]ManagedCertificateRow, error) {
	var certs []ManagedCertificateRow
	if err := s.db.WithContext(ctx).Order("id").Find(&certs).Error; err != nil {
		return nil, err
	}
	for i := range certs {
		normalizeManagedCertificateRow(&certs[i])
	}
	return certs, nil
}

func (s *SQLiteStore) SaveLocalRuntimeState(ctx context.Context, agentID string, runtimeState RuntimeState) error {
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

	return s.db.WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "id"}},
			UpdateAll: true,
		}).
		Create(&row).Error
}

func (s *SQLiteStore) SaveAgent(ctx context.Context, row AgentRow) error {
	normalizeAgentRow(&row)
	return s.db.WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "id"}},
			UpdateAll: true,
		}).
		Create(&row).Error
}

func (s *SQLiteStore) DeleteAgent(ctx context.Context, agentID string) error {
	return s.db.WithContext(ctx).Where("id = ?", agentID).Delete(&AgentRow{}).Error
}

func (s *SQLiteStore) SaveHTTPRules(ctx context.Context, agentID string, rules []HTTPRuleRow) error {
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

func (s *SQLiteStore) SaveL4Rules(ctx context.Context, agentID string, rules []L4RuleRow) error {
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

func (s *SQLiteStore) SaveVersionPolicies(ctx context.Context, policies []VersionPolicyRow) error {
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

func (s *SQLiteStore) SaveRelayListeners(ctx context.Context, agentID string, listeners []RelayListenerRow) error {
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

func (s *SQLiteStore) SaveManagedCertificates(ctx context.Context, certs []ManagedCertificateRow) error {
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

func (s *SQLiteStore) CleanupManagedCertificateMaterial(_ context.Context, previous []ManagedCertificateRow, next []ManagedCertificateRow) error {
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

func (s *SQLiteStore) LoadManagedCertificateMaterial(_ context.Context, domain string) (ManagedCertificateBundle, bool, error) {
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

func (s *SQLiteStore) SaveManagedCertificateMaterial(_ context.Context, domain string, bundle ManagedCertificateBundle) error {
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

func (s *SQLiteStore) initializeSchema(ctx context.Context) error {
	return BootstrapSQLiteSchema(ctx, s.db)
}

func normalizeAgentRow(row *AgentRow) {
	row.TagsJSON = defaultJSON(row.TagsJSON, "[]")
	row.CapabilitiesJSON = defaultJSON(row.CapabilitiesJSON, "[]")
	row.Mode = defaultString(row.Mode, "pull")
	row.LastApplyStatus = defaultString(row.LastApplyStatus, "")
	row.LastApplyMessage = defaultString(row.LastApplyMessage, "")
	row.LastReportedStatsJSON = defaultJSON(row.LastReportedStatsJSON, "{}")
	row.LastSeenAt = defaultString(row.LastSeenAt, "")
	row.LastSeenIP = defaultString(row.LastSeenIP, "")
}

func normalizeHTTPRuleRow(row *HTTPRuleRow) {
	row.BackendsJSON = defaultJSON(row.BackendsJSON, "[]")
	row.LoadBalancingJSON = defaultJSON(row.LoadBalancingJSON, "{}")
	row.TagsJSON = defaultJSON(row.TagsJSON, "[]")
	row.RelayChainJSON = defaultJSON(row.RelayChainJSON, "[]")
	row.UserAgent = defaultString(row.UserAgent, "")
	row.CustomHeadersJSON = defaultJSON(row.CustomHeadersJSON, "[]")
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
	row.LoadBalancingJSON = defaultJSON(row.LoadBalancingJSON, "{}")
	row.TuningJSON = defaultJSON(row.TuningJSON, "{}")
	row.RelayChainJSON = defaultJSON(row.RelayChainJSON, "[]")
	row.TagsJSON = defaultJSON(row.TagsJSON, "[]")
}

func normalizeVersionPolicyRow(row *VersionPolicyRow) {
	row.Channel = defaultString(row.Channel, "stable")
	row.DesiredVersion = defaultString(row.DesiredVersion, "")
	row.PackagesJSON = defaultJSON(row.PackagesJSON, "[]")
	row.TagsJSON = defaultJSON(row.TagsJSON, "[]")
}

func normalizeRelayListenerRow(row *RelayListenerRow) {
	row.Name = defaultString(row.Name, "")
	row.BindHostsJSON = defaultJSON(row.BindHostsJSON, "[]")
	row.ListenHost = defaultString(row.ListenHost, "0.0.0.0")
	row.PublicHost = defaultString(row.PublicHost, row.ListenHost)
	row.TLSMode = defaultString(row.TLSMode, "pin_or_ca")
	row.PinSetJSON = defaultJSON(row.PinSetJSON, "[]")
	row.TrustedCACertificateIDs = defaultJSON(row.TrustedCACertificateIDs, "[]")
	row.TagsJSON = defaultJSON(row.TagsJSON, "[]")
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

func (s *SQLiteStore) resolveAgentID(agentID string) string {
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
	certRows []ManagedCertificateRow,
) int {
	desiredRevision := normalizeRevision(localState.DesiredRevision)
	currentRevision := normalizeRevision(localState.CurrentRevision)
	highestConfigRevision := maxInt(
		highestHTTPRuleRevision(httpRows),
		highestL4RuleRevision(l4Rows),
		highestRelayListenerRevision(relayRows),
		highestManagedCertificateRevision(certRows),
	)

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

func (s *SQLiteStore) loadRelayListenersForSync(
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

func referencedRelayListenerIDs(httpRows []HTTPRuleRow, l4Rows []L4RuleRow) []int {
	referenced := make([]int, 0)
	seen := make(map[int]struct{})
	addRelayChain := func(chainJSON string) {
		for _, listenerID := range parseIntSlice(chainJSON) {
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

	for _, row := range httpRows {
		if !row.Enabled {
			continue
		}
		addRelayChain(row.RelayChainJSON)
	}
	for _, row := range l4Rows {
		if !row.Enabled {
			continue
		}
		addRelayChain(row.RelayChainJSON)
	}
	return referenced
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
	if row.ListenPort < 1 || row.ListenPort > 65535 {
		return false
	}

	protocol := strings.ToLower(strings.TrimSpace(row.Protocol))
	if protocol == "" {
		protocol = "tcp"
	}
	if protocol != "tcp" && protocol != "udp" {
		return false
	}

	if len(parseL4Backends(row.BackendsJSON)) > 0 {
		return true
	}

	if strings.TrimSpace(row.UpstreamHost) == "" {
		return false
	}
	return row.UpstreamPort >= 1 && row.UpstreamPort <= 65535
}

func snapshotHTTPRules(rows []HTTPRuleRow) []HTTPRule {
	rules := make([]HTTPRule, 0, len(rows))
	for _, row := range rows {
		if !row.Enabled {
			continue
		}
		backends := parseHTTPBackends(row.BackendsJSON)
		backendURL := strings.TrimSpace(row.BackendURL)
		if len(backends) == 0 && backendURL != "" {
			backends = []HTTPBackend{{URL: backendURL}}
		}
		if backendURL == "" && len(backends) > 0 {
			backendURL = backends[0].URL
		}
		rules = append(rules, HTTPRule{
			ID:               row.ID,
			AgentID:          row.AgentID,
			FrontendURL:      row.FrontendURL,
			BackendURL:       backendURL,
			Backends:         backends,
			LoadBalancing:    parseLoadBalancingStrategy(row.LoadBalancingJSON),
			ProxyRedirect:    row.ProxyRedirect,
			PassProxyHeaders: row.PassProxyHeaders,
			UserAgent:        row.UserAgent,
			CustomHeaders:    parseHTTPHeaders(row.CustomHeadersJSON),
			RelayChain:       parseIntSlice(row.RelayChainJSON),
			Revision:         int64(row.Revision),
		})
	}
	return rules
}

func snapshotL4Rules(rows []L4RuleRow) []L4Rule {
	rules := make([]L4Rule, 0, len(rows))
	for _, row := range rows {
		if !row.Enabled {
			continue
		}
		backends := parseL4Backends(row.BackendsJSON)
		upstreamHost := strings.TrimSpace(row.UpstreamHost)
		upstreamPort := row.UpstreamPort
		if len(backends) == 0 && upstreamHost != "" && upstreamPort > 0 {
			backends = []L4Backend{{Host: upstreamHost, Port: upstreamPort}}
		}
		if len(backends) > 0 {
			upstreamHost = backends[0].Host
			upstreamPort = backends[0].Port
		}
		rules = append(rules, L4Rule{
			ID:            row.ID,
			AgentID:       row.AgentID,
			Name:          row.Name,
			Protocol:      defaultString(row.Protocol, "tcp"),
			ListenHost:    defaultString(row.ListenHost, "0.0.0.0"),
			ListenPort:    row.ListenPort,
			UpstreamHost:  upstreamHost,
			UpstreamPort:  upstreamPort,
			Backends:      backends,
			LoadBalancing: parseLoadBalancingStrategy(row.LoadBalancingJSON),
			Tuning:        parseL4Tuning(row.TuningJSON),
			RelayChain:    parseIntSlice(row.RelayChainJSON),
			Revision:      int64(row.Revision),
		})
	}
	return rules
}

func snapshotRelayListeners(rows []RelayListenerRow) []RelayListener {
	listeners := make([]RelayListener, 0, len(rows))
	for _, row := range rows {
		listeners = append(listeners, RelayListener{
			ID:                      row.ID,
			AgentID:                 row.AgentID,
			Name:                    row.Name,
			ListenHost:              defaultString(row.ListenHost, "0.0.0.0"),
			BindHosts:               parseStringSlice(row.BindHostsJSON),
			ListenPort:              row.ListenPort,
			PublicHost:              defaultString(row.PublicHost, row.ListenHost),
			PublicPort:              row.PublicPort,
			Enabled:                 row.Enabled,
			CertificateID:           copyOptionalInt(row.CertificateID),
			TLSMode:                 defaultString(row.TLSMode, "pin_or_ca"),
			PinSet:                  parseRelayPins(row.PinSetJSON),
			TrustedCACertificateIDs: parseIntSlice(row.TrustedCACertificateIDs),
			AllowSelfSigned:         row.AllowSelfSigned,
			Tags:                    parseStringSlice(row.TagsJSON),
			Revision:                int64(row.Revision),
		})
	}
	return listeners
}

func (s *SQLiteStore) snapshotCertificateBundles(rows []ManagedCertificateRow) []ManagedCertificateBundle {
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

func filterManagedCertificatesForAgent(rows []ManagedCertificateRow, agentID string, relayRows []RelayListenerRow) []ManagedCertificateRow {
	filtered := make([]ManagedCertificateRow, 0, len(rows))
	referencedCertificateIDs := relayTrustedCACertificateIDs(relayRows)
	for _, row := range rows {
		if referencedCertificateIDs[row.ID] || containsString(parseStringSlice(row.TargetAgentIDs), agentID) {
			filtered = append(filtered, row)
		}
	}
	return filtered
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
		return LoadBalancing{Strategy: "round_robin"}
	}
	if strings.TrimSpace(value.Strategy) != "random" {
		value.Strategy = "round_robin"
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

func relayTrustedCACertificateIDs(rows []RelayListenerRow) map[int]bool {
	ids := make(map[int]bool)
	for _, row := range rows {
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

func (s *SQLiteStore) readManagedCertificateMaterial(domain string) (managedCertificateMaterial, bool) {
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

func (s *SQLiteStore) managedCertificateDirectory(domain string) string {
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
