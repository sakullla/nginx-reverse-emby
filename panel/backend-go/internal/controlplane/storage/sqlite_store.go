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
	ListVersionPolicies(context.Context) ([]VersionPolicyRow, error)
	ListManagedCertificates(context.Context) ([]ManagedCertificateRow, error)
	SaveAgent(context.Context, AgentRow) error
	SaveL4Rules(context.Context, string, []L4RuleRow) error
	SaveRelayListeners(context.Context, string, []RelayListenerRow) error
	SaveVersionPolicies(context.Context, []VersionPolicyRow) error
	SaveManagedCertificates(context.Context, []ManagedCertificateRow) error
}

type SQLiteStore struct {
	db           *gorm.DB
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

	store := &SQLiteStore{db: db, localAgentID: localAgentID}
	if err := store.initializeSchema(context.Background()); err != nil {
		if sqlDB, dbErr := db.DB(); dbErr == nil {
			_ = sqlDB.Close()
		}
		return nil, err
	}
	return store, nil
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
	resolvedAgentID := s.resolveAgentID(agentID)

	httpRows, err := s.ListHTTPRules(ctx, resolvedAgentID)
	if err != nil {
		return Snapshot{}, err
	}

	l4Rows, err := s.ListL4Rules(ctx, resolvedAgentID)
	if err != nil {
		return Snapshot{}, err
	}

	relayRows, err := s.ListRelayListeners(ctx, resolvedAgentID)
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

	localState, err := s.LoadLocalAgentState(ctx)
	if err != nil {
		return Snapshot{}, err
	}

	relevantCertRows := filterManagedCertificatesForAgent(certRows, resolvedAgentID)
	return Snapshot{
		DesiredVersion:      localState.DesiredVersion,
		Revision:            int64(computeDesiredRevision(localState, httpRows, l4Rows, relayRows, relevantCertRows)),
		VersionPackage:      resolveVersionPackage(versionPolicies, localState.DesiredVersion),
		Rules:               snapshotHTTPRules(httpRows),
		L4Rules:             snapshotL4Rules(l4Rows),
		RelayListeners:      snapshotRelayListeners(relayRows),
		Certificates:        []ManagedCertificateBundle{},
		CertificatePolicies: snapshotCertificatePolicies(relevantCertRows),
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

	lastApplyStatus := strings.TrimSpace(runtimeState.Status)
	if lastApplyStatus == "" {
		lastApplyStatus = currentState.LastApplyStatus
	}

	lastApplyMessage := ""
	if runtimeState.Metadata != nil {
		lastApplyMessage = strings.TrimSpace(runtimeState.Metadata["last_sync_error"])
		if lastApplyMessage == "" {
			lastApplyMessage = strings.TrimSpace(runtimeState.Metadata["last_apply_message"])
		}
	}

	row := LocalAgentStateRow{
		ID:                1,
		DesiredRevision:   currentState.DesiredRevision,
		CurrentRevision:   int(runtimeState.CurrentRevision),
		LastApplyRevision: int(runtimeState.CurrentRevision),
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

func (s *SQLiteStore) initializeSchema(ctx context.Context) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS agents (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			agent_url TEXT DEFAULT '',
			agent_token TEXT DEFAULT '',
			version TEXT DEFAULT '',
			platform TEXT DEFAULT '',
			desired_version TEXT DEFAULT '',
			tags TEXT DEFAULT '[]',
			capabilities TEXT DEFAULT '[]',
			mode TEXT DEFAULT 'pull',
			desired_revision INTEGER DEFAULT 0,
			current_revision INTEGER DEFAULT 0,
			last_apply_revision INTEGER DEFAULT 0,
			last_apply_status TEXT,
			last_apply_message TEXT DEFAULT '',
			last_reported_stats TEXT,
			last_seen_at TEXT,
			last_seen_ip TEXT,
			created_at TEXT,
			updated_at TEXT,
			error TEXT,
			is_local INTEGER DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS rules (
			id INTEGER NOT NULL,
			agent_id TEXT NOT NULL,
			frontend_url TEXT NOT NULL,
			backend_url TEXT NOT NULL,
			backends TEXT DEFAULT '[]',
			load_balancing TEXT DEFAULT '{}',
			enabled INTEGER DEFAULT 1,
			tags TEXT DEFAULT '[]',
			proxy_redirect INTEGER DEFAULT 1,
			relay_chain TEXT DEFAULT '[]',
			pass_proxy_headers INTEGER DEFAULT 1,
			user_agent TEXT DEFAULT '',
			custom_headers TEXT DEFAULT '[]',
			revision INTEGER DEFAULT 0,
			PRIMARY KEY (agent_id, id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_rules_agent ON rules(agent_id)`,
		`CREATE TABLE IF NOT EXISTS l4_rules (
			id INTEGER NOT NULL,
			agent_id TEXT NOT NULL,
			name TEXT DEFAULT '',
			protocol TEXT DEFAULT 'tcp',
			listen_host TEXT DEFAULT '0.0.0.0',
			listen_port INTEGER NOT NULL,
			upstream_host TEXT DEFAULT '',
			upstream_port INTEGER DEFAULT 0,
			backends TEXT DEFAULT '[]',
			load_balancing TEXT DEFAULT '{}',
			tuning TEXT DEFAULT '{}',
			relay_chain TEXT DEFAULT '[]',
			enabled INTEGER DEFAULT 1,
			tags TEXT DEFAULT '[]',
			revision INTEGER DEFAULT 0,
			PRIMARY KEY (agent_id, id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_l4_rules_agent ON l4_rules(agent_id)`,
		`CREATE TABLE IF NOT EXISTS relay_listeners (
			id INTEGER PRIMARY KEY,
			agent_id TEXT NOT NULL,
			name TEXT DEFAULT '',
			bind_hosts TEXT DEFAULT '[]',
			listen_host TEXT DEFAULT '0.0.0.0',
			listen_port INTEGER NOT NULL,
			public_host TEXT,
			public_port INTEGER,
			enabled INTEGER DEFAULT 1,
			certificate_id INTEGER,
			tls_mode TEXT DEFAULT 'pin_or_ca',
			pin_set TEXT DEFAULT '[]',
			trusted_ca_certificate_ids TEXT DEFAULT '[]',
			allow_self_signed INTEGER DEFAULT 0,
			tags TEXT DEFAULT '[]',
			revision INTEGER DEFAULT 0
		)`,
		`CREATE INDEX IF NOT EXISTS idx_relay_listeners_agent ON relay_listeners(agent_id)`,
		`CREATE TABLE IF NOT EXISTS managed_certificates (
			id INTEGER PRIMARY KEY,
			domain TEXT NOT NULL,
			enabled INTEGER DEFAULT 1,
			scope TEXT DEFAULT 'domain',
			issuer_mode TEXT DEFAULT 'master_cf_dns',
			target_agent_ids TEXT DEFAULT '[]',
			status TEXT DEFAULT 'pending',
			last_issue_at TEXT,
			last_error TEXT DEFAULT '',
			material_hash TEXT DEFAULT '',
			agent_reports TEXT DEFAULT '{}',
			acme_info TEXT DEFAULT '{}',
			usage TEXT DEFAULT 'https',
			certificate_type TEXT DEFAULT 'acme',
			self_signed INTEGER DEFAULT 0,
			tags TEXT DEFAULT '[]',
			revision INTEGER DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS local_agent_state (
			id INTEGER PRIMARY KEY CHECK (id = 1),
			desired_revision INTEGER DEFAULT 0,
			current_revision INTEGER DEFAULT 0,
			last_apply_revision INTEGER DEFAULT 0,
			last_apply_status TEXT DEFAULT 'success',
			last_apply_message TEXT DEFAULT '',
			desired_version TEXT DEFAULT ''
		)`,
		`CREATE TABLE IF NOT EXISTS version_policy (
			id TEXT PRIMARY KEY,
			channel TEXT DEFAULT 'stable',
			desired_version TEXT DEFAULT '',
			packages TEXT DEFAULT '[]',
			tags TEXT DEFAULT '[]'
		)`,
		`CREATE TABLE IF NOT EXISTS meta (
			key TEXT PRIMARY KEY,
			value TEXT
		)`,
		`INSERT OR IGNORE INTO local_agent_state (
			id, desired_revision, current_revision, last_apply_revision, last_apply_status, last_apply_message, desired_version
		) VALUES (1, 0, 0, 0, 'success', '', '')`,
	}

	for _, stmt := range statements {
		if err := s.db.WithContext(ctx).Exec(stmt).Error; err != nil {
			return err
		}
	}
	return nil
}

func normalizeAgentRow(row *AgentRow) {
	row.TagsJSON = defaultJSON(row.TagsJSON, "[]")
	row.CapabilitiesJSON = defaultJSON(row.CapabilitiesJSON, "[]")
	row.Mode = defaultString(row.Mode, "pull")
	row.LastApplyStatus = defaultString(row.LastApplyStatus, "")
	row.LastApplyMessage = defaultString(row.LastApplyMessage, "")
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
	return desiredRevision
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

func snapshotCertificatePolicies(rows []ManagedCertificateRow) []ManagedCertificatePolicy {
	policies := make([]ManagedCertificatePolicy, 0, len(rows))
	for _, row := range rows {
		policies = append(policies, ManagedCertificatePolicy{
			ID:              row.ID,
			Domain:          row.Domain,
			Enabled:         row.Enabled,
			Scope:           defaultString(row.Scope, "domain"),
			IssuerMode:      defaultString(row.IssuerMode, "master_cf_dns"),
			Status:          defaultString(row.Status, "pending"),
			LastIssueAt:     row.LastIssueAt,
			LastError:       row.LastError,
			ACMEInfo:        parseManagedCertificateACMEInfo(row.ACMEInfo),
			Tags:            parseStringSlice(row.TagsJSON),
			Revision:        int64(row.Revision),
			Usage:           defaultString(row.Usage, "https"),
			CertificateType: defaultString(row.CertificateType, "acme"),
			SelfSigned:      row.SelfSigned,
		})
	}
	return policies
}

func filterManagedCertificatesForAgent(rows []ManagedCertificateRow, agentID string) []ManagedCertificateRow {
	filtered := make([]ManagedCertificateRow, 0, len(rows))
	for _, row := range rows {
		if containsString(parseStringSlice(row.TargetAgentIDs), agentID) {
			filtered = append(filtered, row)
		}
	}
	return filtered
}

func resolveVersionPackage(rows []VersionPolicyRow, desiredVersion string) *VersionPackage {
	desiredVersion = strings.TrimSpace(desiredVersion)
	if desiredVersion == "" {
		return nil
	}

	platform := runtime.GOOS + "-" + runtime.GOARCH
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
