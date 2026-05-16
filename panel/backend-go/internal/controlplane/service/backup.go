package service

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"path"
	"runtime/debug"
	"slices"
	"strings"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

const (
	backupManifestFile          = "manifest.json"
	backupAgentsFile            = "agents.json"
	backupHTTPRulesFile         = "http_rules.json"
	backupL4RulesFile           = "l4_rules.json"
	backupWireGuardProfilesFile = "wireguard_profiles.json"
	backupWireGuardClientsFile  = "wireguard_clients.json"
	backupRelayListenersFile    = "relay_listeners.json"
	backupCertificatesFile      = "certificates.json"
	backupVersionPoliciesFile   = "version_policies.json"
	backupTrafficPoliciesFile   = "traffic_policies.json"
	backupTrafficBaselinesFile  = "traffic_baselines.json"
	backupMaterialPrefix        = "certificate_material"
)

type backupService struct {
	cfg   config.Config
	store backupStore
	now   func() time.Time
}

type modifiedAgentRevisions map[string]int

type backupStore interface {
	storage.Store
	DeleteAgent(context.Context, string) error
	SaveHTTPRules(context.Context, string, []storage.HTTPRuleRow) error
	ListWireGuardClients(context.Context, string, int) ([]storage.WireGuardClientRow, error)
	SaveWireGuardClients(context.Context, string, int, []storage.WireGuardClientRow) error
	ListTrafficPolicies(context.Context) ([]storage.AgentTrafficPolicyRow, error)
	ListTrafficBaselines(context.Context) ([]storage.AgentTrafficBaselineRow, error)
	SaveTrafficPolicy(context.Context, storage.AgentTrafficPolicyRow) error
	SaveTrafficBaseline(context.Context, storage.AgentTrafficBaselineRow) error
	ReplaceTrafficPolicies(context.Context, []storage.AgentTrafficPolicyRow) error
	ReplaceTrafficBaselines(context.Context, []storage.AgentTrafficBaselineRow) error
}

type BackupExportOptions struct {
	Agents            bool `json:"agents"`
	HTTPRules         bool `json:"http_rules"`
	L4Rules           bool `json:"l4_rules"`
	WireGuardProfiles bool `json:"wireguard_profiles"`
	WireGuardClients  bool `json:"wireguard_clients"`
	RelayListeners    bool `json:"relay_listeners"`
	Certificates      bool `json:"certificates"`
	VersionPolicies   bool `json:"version_policies"`
	TrafficPolicies   bool `json:"traffic_policies"`
	TrafficBaselines  bool `json:"traffic_baselines"`
}

func AllExportOptions() BackupExportOptions {
	return BackupExportOptions{
		Agents:            true,
		HTTPRules:         true,
		L4Rules:           true,
		WireGuardProfiles: true,
		WireGuardClients:  true,
		RelayListeners:    true,
		Certificates:      true,
		VersionPolicies:   true,
		TrafficPolicies:   true,
		TrafficBaselines:  true,
	}
}

func NewBackupService(cfg config.Config, store backupStore) *backupService {
	return &backupService{
		cfg:   cfg,
		store: store,
		now:   time.Now,
	}
}

func (s *backupService) Export(ctx context.Context) ([]byte, string, error) {
	bundle, err := s.exportBundle(ctx)
	if err != nil {
		return nil, "", err
	}
	archive, err := encodeBackupBundle(bundle)
	if err != nil {
		return nil, "", err
	}
	filename := fmt.Sprintf("nre-backup-%s.tar.gz", bundle.Manifest.ExportedAt.UTC().Format("20060102T150405Z"))
	return archive, filename, nil
}

func (s *backupService) ExportSelective(ctx context.Context, opts BackupExportOptions) ([]byte, string, error) {
	bundle, err := s.exportBundle(ctx)
	if err != nil {
		return nil, "", err
	}
	if !opts.Agents {
		bundle.Agents = nil
	}
	if !opts.HTTPRules {
		bundle.HTTPRules = nil
	}
	if !opts.L4Rules {
		bundle.L4Rules = nil
	}
	if !opts.WireGuardProfiles {
		bundle.WireGuardProfiles = nil
		bundle.WireGuardClients = nil
	} else if !opts.WireGuardClients {
		bundle.WireGuardClients = nil
	}
	if !opts.RelayListeners {
		bundle.RelayListeners = nil
	}
	if !opts.Certificates {
		bundle.Certificates = nil
		bundle.Materials = nil
	}
	if !opts.VersionPolicies {
		bundle.VersionPolicies = nil
	}
	if !opts.TrafficPolicies {
		bundle.TrafficPolicies = nil
	}
	if !opts.TrafficBaselines {
		bundle.TrafficBaselines = nil
	}
	bundle.Manifest.Counts = BackupCounts{
		Agents:            len(bundle.Agents),
		HTTPRules:         len(bundle.HTTPRules),
		L4Rules:           len(bundle.L4Rules),
		WireGuardProfiles: len(bundle.WireGuardProfiles),
		WireGuardClients:  len(bundle.WireGuardClients),
		RelayListeners:    len(bundle.RelayListeners),
		Certificates:      len(bundle.Certificates),
		VersionPolicies:   len(bundle.VersionPolicies),
		TrafficPolicies:   len(bundle.TrafficPolicies),
		TrafficBaselines:  len(bundle.TrafficBaselines),
	}
	bundle.Manifest.IncludesCertificates = len(bundle.Materials) > 0
	archive, err := encodeBackupBundle(bundle)
	if err != nil {
		return nil, "", err
	}
	filename := fmt.Sprintf("nre-backup-%s.tar.gz", bundle.Manifest.ExportedAt.UTC().Format("20060102T150405Z"))
	return archive, filename, nil
}

func (s *backupService) ResourceCounts(ctx context.Context) (BackupCounts, error) {
	bundle, err := s.exportBundle(ctx)
	if err != nil {
		return BackupCounts{}, err
	}
	return bundle.Manifest.Counts, nil
}

func (s *backupService) Preview(ctx context.Context, archive []byte) (BackupImportResult, error) {
	bundle, err := decodeBackupBundle(archive)
	if err != nil {
		return BackupImportResult{}, err
	}
	if bundle.Manifest.PackageVersion != BackupPackageVersion {
		return BackupImportResult{}, fmt.Errorf("%w: unsupported backup package version %d", ErrInvalidArgument, bundle.Manifest.PackageVersion)
	}
	result := newBackupImportResult(bundle.Manifest)
	existingAgents, err := s.store.ListAgents(ctx)
	if err != nil {
		return BackupImportResult{}, err
	}
	existingByName := make(map[string]storage.AgentRow, len(existingAgents))
	existingByID := make(map[string]storage.AgentRow, len(existingAgents))
	for _, row := range existingAgents {
		existingByName[row.Name] = row
		existingByID[row.ID] = row
	}
	for _, item := range bundle.Agents {
		key := strings.TrimSpace(item.Name)
		if key == "" {
			key = strings.TrimSpace(item.ID)
		}
		if strings.TrimSpace(item.ID) == "" || strings.TrimSpace(item.Name) == "" || strings.TrimSpace(item.AgentToken) == "" {
			result.addSkippedInvalid("agent", key, "agent id, name, and agent_token are required")
			continue
		}
		if strings.EqualFold(strings.TrimSpace(item.Mode), "local") && s.cfg.EnableLocalAgent {
			result.addSkippedConflict("agent", item.Name, "local agent remapped to target")
			continue
		}
		if _, ok := existingByName[item.Name]; ok {
			result.addSkippedConflict("agent", item.Name, "agent name already exists")
			continue
		}
		if _, ok := existingByID[item.ID]; ok {
			result.addSkippedConflict("agent", item.Name, "agent id already exists")
			continue
		}
		result.addImported("agent", item.Name)
	}
	agentIDMap := previewAgentIDMap(bundle.Manifest, bundle.Agents, existingByName, existingByID, s.cfg)
	existingCertRows, err := s.store.ListManagedCertificates(ctx)
	if err != nil {
		return BackupImportResult{}, err
	}
	previewAgentRowsByID := previewAgentRows(bundle.Agents, agentIDMap, existingByName, existingByID, s.cfg)
	certIDMap := previewCertificateIDMap(bundle.Certificates, bundle.Agents, existingCertRows, agentIDMap, existingByName, existingByID, s.cfg)
	existingCertDomains := map[string]struct{}{}
	for _, row := range existingCertRows {
		existingCertDomains[strings.TrimSpace(row.Domain)] = struct{}{}
	}
	for _, item := range bundle.Certificates {
		key := strings.TrimSpace(item.Domain)
		if _, exists := existingCertDomains[key]; exists {
			result.addSkippedConflict("certificate", key, "certificate domain already exists")
			continue
		}
		result.addImported("certificate", key)
	}
	wireGuardProfileIDMap, enabledWireGuardProfileIDs, importedWireGuardProfileIDs, skippedConflictWireGuardProfileIDs, err := previewWireGuardProfiles(ctx, bundle.WireGuardProfiles, agentIDMap, s.cfg, s.store, &result)
	if err != nil {
		return BackupImportResult{}, err
	}
	if err := previewWireGuardClients(bundle.WireGuardClients, agentIDMap, wireGuardProfileIDMap, importedWireGuardProfileIDs, skippedConflictWireGuardProfileIDs, &result, s.cfg); err != nil {
		return BackupImportResult{}, err
	}
	existingRelayRows, err := s.store.ListRelayListeners(ctx, "")
	if err != nil {
		return BackupImportResult{}, err
	}
	listenerAllocator := newConfigIdentityAllocator(configIdentityAllocatorState{
		LocalAgentID:   s.cfg.LocalAgentID,
		RelayListeners: existingRelayRows,
	})
	previewCapabilityStore := previewAgentCapabilityStore{rows: previewAgentRowsByID}
	listenerIDMap := previewListenerIDMap(ctx, bundle.RelayListeners, existingRelayRows, agentIDMap, certIDMap, wireGuardProfileIDMap, enabledWireGuardProfileIDs, listenerAllocator, previewCapabilityStore, s.cfg)
	previewRelayListeners := previewRelayListenersByID(existingRelayRows, bundle.RelayListeners, listenerIDMap, agentIDMap, s.cfg)
	existingRelayKeys := map[string]struct{}{}
	for _, row := range existingRelayRows {
		existingRelayKeys[relayConflictKey(row.AgentID, row.Name)] = struct{}{}
	}
	previewRelayKeys := map[string]struct{}{}
	for _, item := range bundle.RelayListeners {
		resolvedAgentID, ok := resolveAgentID(item.AgentID, agentIDMap, s.cfg)
		key := relayConflictKey(item.AgentID, item.Name)
		if !ok {
			result.addSkippedInvalid("relay_listener", key, "relay listener references unknown agent")
			continue
		}
		conflictKey := relayConflictKey(resolvedAgentID, item.Name)
		if _, exists := existingRelayKeys[conflictKey]; exists {
			result.addSkippedConflict("relay_listener", conflictKey, "relay listener already exists")
			continue
		}
		if _, exists := previewRelayKeys[conflictKey]; exists {
			result.addSkippedConflict("relay_listener", conflictKey, "relay listener already exists")
			continue
		}
		if strings.EqualFold(strings.TrimSpace(item.TransportMode), "wireguard") {
			if err := ensureAgentSupportsWireGuardCapability(ctx, s.cfg, previewCapabilityStore, resolvedAgentID); err != nil {
				result.addSkippedInvalid("relay_listener", conflictKey, err.Error())
				continue
			}
		}
		wireGuardProfileID, ok := remapBackupWireGuardProfileID(item.AgentID, item.WireGuardProfileID, wireGuardProfileIDMap, enabledWireGuardProfileIDs)
		if strings.EqualFold(strings.TrimSpace(item.TransportMode), "wireguard") && !ok {
			result.addSkippedInvalid("relay_listener", conflictKey, "wireguard profile was not imported")
			continue
		}
		input := relayListenerInputFromBackup(item, certIDMap, wireGuardProfileID)
		if item.CertificateID != nil && input.CertificateID == nil {
			result.addSkippedInvalid("relay_listener", conflictKey, "referenced certificate was not imported")
			continue
		}
		if len(item.TrustedCACertificateIDs) > 0 && len(pointerIntSlice(input.TrustedCACertificateIDs)) != len(item.TrustedCACertificateIDs) {
			result.addSkippedInvalid("relay_listener", conflictKey, "referenced trusted CA certificate was not imported")
			continue
		}
		result.addImported("relay_listener", conflictKey)
		previewRelayKeys[conflictKey] = struct{}{}
	}
	knownAgentIDs, err := allKnownAgentIDs(ctx, s.cfg, s.store)
	if err != nil {
		return BackupImportResult{}, err
	}
	knownAgentIDs = appendKnownAgentIDs(knownAgentIDs, agentIDMap)
	existingHTTPRules, err := s.listAllHTTPRules(ctx, knownAgentIDs)
	if err != nil {
		return BackupImportResult{}, err
	}
	existingHTTPKeys := map[string]struct{}{}
	for _, row := range existingHTTPRules {
		existingHTTPKeys[httpRuleConflictKey(row.AgentID, row.FrontendURL)] = struct{}{}
	}
	for _, item := range bundle.HTTPRules {
		key := strings.TrimSpace(item.FrontendURL)
		resolvedAgentID, ok := resolveAgentID(item.AgentID, agentIDMap, s.cfg)
		if !ok {
			result.addSkippedInvalid("http_rule", key, "http rule references unknown agent")
			continue
		}
		conflictKey := httpRuleConflictKey(resolvedAgentID, item.FrontendURL)
		if _, exists := existingHTTPKeys[conflictKey]; exists {
			result.addSkippedConflict("http_rule", key, "frontend_url already exists")
			continue
		}
		if item.WireGuardEntryEnabled {
			if err := ensureAgentSupportsWireGuardCapability(ctx, s.cfg, previewCapabilityStore, resolvedAgentID); err != nil {
				result.addSkippedInvalid("http_rule", key, err.Error())
				continue
			}
		}
		input := httpRuleInputFromBackup(item, listenerIDMap, nil)
		if !remappedBackupRelayLayersComplete(item.RelayChain, item.RelayLayers, input.RelayLayers) {
			result.addSkippedInvalid("http_rule", key, "relay listener reference not available")
			continue
		}
		if err := validateRelayChainReferencesFromRows(knownAgentIDs, previewRelayListeners, flattenRelayLayers(pointerRelayLayers(input.RelayLayers)), relayChainValidationOptions{RuleAgentID: resolvedAgentID}); err != nil {
			result.addSkippedInvalid("http_rule", key, err.Error())
			continue
		}
		if item.WireGuardEntryEnabled {
			if _, ok := remapBackupWireGuardProfileID(item.AgentID, item.WireGuardProfileID, wireGuardProfileIDMap, enabledWireGuardProfileIDs); !ok {
				result.addSkippedInvalid("http_rule", key, "wireguard profile was not imported")
				continue
			}
		}
		result.addImported("http_rule", key)
		existingHTTPKeys[conflictKey] = struct{}{}
	}
	existingL4Rules, err := s.listAllL4Rules(ctx, knownAgentIDs)
	if err != nil {
		return BackupImportResult{}, err
	}
	existingL4Keys := map[string]struct{}{}
	for _, row := range existingL4Rules {
		existingL4Keys[l4BackupConflictKey(row.AgentID, row.Protocol, row.ListenHost, row.ListenPort, row.ListenMode, row.WireGuardListenHost, row.WireGuardProfileID)] = struct{}{}
	}
	for _, item := range bundle.L4Rules {
		resolvedAgentID, ok := resolveAgentID(item.AgentID, agentIDMap, s.cfg)
		key := l4BackupConflictKey(item.AgentID, item.Protocol, item.ListenHost, item.ListenPort, item.ListenMode, item.WireGuardListenHost, item.WireGuardProfileID)
		if !ok {
			result.addSkippedInvalid("l4_rule", key, "l4 rule references unknown agent")
			continue
		}
		wireGuardProfileID, profileOK := remapBackupWireGuardProfileID(item.AgentID, item.WireGuardProfileID, wireGuardProfileIDMap, enabledWireGuardProfileIDs)
		key = l4BackupConflictKey(resolvedAgentID, item.Protocol, item.ListenHost, item.ListenPort, item.ListenMode, item.WireGuardListenHost, wireGuardProfileID)
		if _, exists := existingL4Keys[key]; exists {
			result.addSkippedConflict("l4_rule", key, "protocol/listen_host/listen_port already exists")
			continue
		}
		if strings.EqualFold(strings.TrimSpace(item.ListenMode), "wireguard") || strings.EqualFold(strings.TrimSpace(item.ProxyEgressMode), "wireguard") {
			if err := ensureAgentSupportsWireGuardCapability(ctx, s.cfg, previewCapabilityStore, resolvedAgentID); err != nil {
				result.addSkippedInvalid("l4_rule", key, err.Error())
				continue
			}
		}
		if (strings.EqualFold(strings.TrimSpace(item.ListenMode), "wireguard") || strings.EqualFold(strings.TrimSpace(item.ProxyEgressMode), "wireguard")) && !profileOK {
			result.addSkippedInvalid("l4_rule", key, "wireguard profile was not imported")
			continue
		}
		input := l4RuleInputFromBackup(item, listenerIDMap, wireGuardProfileID)
		if !remappedBackupRelayLayersComplete(item.RelayChain, item.RelayLayers, input.RelayLayers) {
			result.addSkippedInvalid("l4_rule", key, "relay listener reference not available")
			continue
		}
		if err := validateRelayChainReferencesFromRows(knownAgentIDs, previewRelayListeners, flattenRelayLayers(pointerRelayLayers(input.RelayLayers)), relayChainValidationOptions{RuleAgentID: resolvedAgentID}); err != nil {
			result.addSkippedInvalid("l4_rule", key, err.Error())
			continue
		}
		result.addImported("l4_rule", key)
		existingL4Keys[key] = struct{}{}
	}
	existingPolicyRows, err := s.store.ListVersionPolicies(ctx)
	if err != nil {
		return BackupImportResult{}, err
	}
	existingPolicyIDs := map[string]struct{}{}
	for _, row := range existingPolicyRows {
		existingPolicyIDs[strings.TrimSpace(row.ID)] = struct{}{}
	}
	for _, item := range bundle.VersionPolicies {
		key := strings.TrimSpace(item.ID)
		if _, exists := existingPolicyIDs[key]; exists {
			result.addSkippedConflict("version_policy", key, "version policy already exists")
			continue
		}
		result.addImported("version_policy", key)
	}
	for _, item := range bundle.TrafficPolicies {
		key := strings.TrimSpace(item.AgentID)
		if _, ok := resolveAgentID(item.AgentID, agentIDMap, s.cfg); !ok {
			result.addSkippedInvalid("traffic_policy", key, "traffic policy references unknown agent")
			continue
		}
		result.addImported("traffic_policy", key)
	}
	for _, item := range bundle.TrafficBaselines {
		key := trafficBaselineKey(item.AgentID, item.CycleStart)
		if _, ok := resolveAgentID(item.AgentID, agentIDMap, s.cfg); !ok {
			result.addSkippedInvalid("traffic_baseline", key, "traffic baseline references unknown agent")
			continue
		}
		result.addImported("traffic_baseline", key)
	}
	return result, nil
}

func previewCertificateIDMap(certs []BackupCertificate, agents []BackupAgent, existing []storage.ManagedCertificateRow, agentIDMap map[string]string, existingAgentsByName map[string]storage.AgentRow, existingAgentsByID map[string]storage.AgentRow, cfg config.Config) map[int]int {
	certIDMap := map[int]int{}
	existingByDomain := make(map[string]ManagedCertificate, len(existing))
	for _, row := range existing {
		cert := managedCertificateFromRow(row)
		existingByDomain[cert.Domain] = cert
		certIDMap[cert.ID] = cert.ID
	}
	previewAgentCaps := previewAgentCapabilities(agents, agentIDMap, existingAgentsByName, existingAgentsByID, cfg)

	for _, item := range certs {
		if existingCert, ok := existingByDomain[item.Domain]; ok {
			certIDMap[item.ID] = existingCert.ID
			continue
		}
		if !previewCertificateTargetsResolvable(item.TargetAgentIDs, agentIDMap, previewAgentCaps, cfg) {
			continue
		}
		if item.ID > 0 {
			certIDMap[item.ID] = item.ID
		}
	}
	return certIDMap
}

func previewCertificateTargetsResolvable(targetAgentIDs []string, agentIDMap map[string]string, capabilitiesByAgentID map[string][]string, cfg config.Config) bool {
	targetIDs, ok := remapAgentIDs(targetAgentIDs, agentIDMap)
	if !ok {
		return false
	}
	for _, targetID := range targetIDs {
		if cfg.EnableLocalAgent && strings.TrimSpace(targetID) == strings.TrimSpace(cfg.LocalAgentID) {
			if !agentHasCapability(defaultLocalCapabilities, "cert_install") {
				return false
			}
			continue
		}
		capabilities, ok := capabilitiesByAgentID[targetID]
		if !ok || !agentHasCapability(capabilities, "cert_install") {
			return false
		}
	}
	return true
}

type previewAgentCapabilityStore struct {
	rows map[string]storage.AgentRow
}

func (s previewAgentCapabilityStore) ListAgents(context.Context) ([]storage.AgentRow, error) {
	rows := make([]storage.AgentRow, 0, len(s.rows))
	for _, row := range s.rows {
		rows = append(rows, row)
	}
	return rows, nil
}

func previewAgentRows(agents []BackupAgent, agentIDMap map[string]string, existingAgentsByName map[string]storage.AgentRow, existingAgentsByID map[string]storage.AgentRow, cfg config.Config) map[string]storage.AgentRow {
	rows := make(map[string]storage.AgentRow, len(existingAgentsByID)+len(agents)+1)
	for id, row := range existingAgentsByID {
		rows[id] = row
	}
	for _, item := range agents {
		if strings.TrimSpace(item.ID) == "" {
			continue
		}
		resolvedID := strings.TrimSpace(agentIDMap[item.ID])
		if resolvedID == "" {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(item.Mode), "local") && cfg.EnableLocalAgent {
			continue
		}
		if existingRow, ok := existingAgentsByName[item.Name]; ok {
			rows[resolvedID] = existingRow
			continue
		}
		if existingRow, ok := existingAgentsByID[item.ID]; ok {
			rows[resolvedID] = existingRow
			continue
		}
		rows[resolvedID] = storage.AgentRow{
			ID:               resolvedID,
			Name:             strings.TrimSpace(item.Name),
			CapabilitiesJSON: marshalJSON(normalizeTags(item.Capabilities), "[]"),
		}
	}
	if cfg.EnableLocalAgent && strings.TrimSpace(cfg.LocalAgentID) != "" {
		rows[cfg.LocalAgentID] = storage.AgentRow{
			ID:               cfg.LocalAgentID,
			Name:             cfg.LocalAgentID,
			CapabilitiesJSON: marshalJSON(defaultLocalCapabilities, "[]"),
		}
	}
	return rows
}

func previewAgentCapabilities(agents []BackupAgent, agentIDMap map[string]string, existingAgentsByName map[string]storage.AgentRow, existingAgentsByID map[string]storage.AgentRow, cfg config.Config) map[string][]string {
	capabilitiesByAgentID := make(map[string][]string, len(existingAgentsByID)+len(agents)+1)
	for id, row := range existingAgentsByID {
		capabilitiesByAgentID[id] = parseStringArray(row.CapabilitiesJSON)
	}
	if cfg.EnableLocalAgent && strings.TrimSpace(cfg.LocalAgentID) != "" {
		capabilitiesByAgentID[cfg.LocalAgentID] = append([]string(nil), defaultLocalCapabilities...)
	}
	for _, item := range agents {
		if strings.TrimSpace(item.ID) == "" {
			continue
		}
		resolvedID := strings.TrimSpace(agentIDMap[item.ID])
		if resolvedID == "" {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(item.Mode), "local") && cfg.EnableLocalAgent {
			capabilitiesByAgentID[resolvedID] = append([]string(nil), defaultLocalCapabilities...)
			continue
		}
		if existingRow, ok := existingAgentsByName[item.Name]; ok {
			capabilitiesByAgentID[resolvedID] = parseStringArray(existingRow.CapabilitiesJSON)
			continue
		}
		if existingRow, ok := existingAgentsByID[item.ID]; ok {
			capabilitiesByAgentID[resolvedID] = parseStringArray(existingRow.CapabilitiesJSON)
			continue
		}
		capabilitiesByAgentID[resolvedID] = append([]string(nil), item.Capabilities...)
	}
	return capabilitiesByAgentID
}

func previewListenerIDMap(ctx context.Context, listeners []BackupRelayListener, existing []storage.RelayListenerRow, agentIDMap map[string]string, certIDMap map[int]int, wireGuardProfileIDMap map[string]int, enabledWireGuardProfileIDs map[string]struct{}, allocator *configIdentityAllocator, capabilityStore agentCapabilityStore, cfg config.Config) map[int]int {
	listenerIDMap := map[int]int{}
	conflictIndex := map[string]int{}
	for _, row := range existing {
		listener := relayListenerFromRow(row)
		conflictIndex[relayConflictKey(listener.AgentID, listener.Name)] = listener.ID
		if listener.ID > 0 {
			listenerIDMap[listener.ID] = listener.ID
		}
	}

	for _, item := range listeners {
		resolvedAgentID, ok := resolveAgentID(item.AgentID, agentIDMap, cfg)
		if !ok {
			continue
		}
		conflictKey := relayConflictKey(resolvedAgentID, item.Name)
		if mappedID, ok := conflictIndex[conflictKey]; ok {
			if item.ID > 0 {
				listenerIDMap[item.ID] = mappedID
			}
			continue
		}
		if strings.EqualFold(strings.TrimSpace(item.TransportMode), "wireguard") && capabilityStore != nil {
			if err := ensureAgentSupportsWireGuardCapability(ctx, cfg, capabilityStore, resolvedAgentID); err != nil {
				continue
			}
		}
		wireGuardProfileID, ok := remapBackupWireGuardProfileID(item.AgentID, item.WireGuardProfileID, wireGuardProfileIDMap, enabledWireGuardProfileIDs)
		if strings.EqualFold(strings.TrimSpace(item.TransportMode), "wireguard") && !ok {
			continue
		}
		input := relayListenerInputFromBackup(item, certIDMap, wireGuardProfileID)
		if item.CertificateID != nil && input.CertificateID == nil {
			continue
		}
		if len(item.TrustedCACertificateIDs) > 0 && len(pointerIntSlice(input.TrustedCACertificateIDs)) != len(item.TrustedCACertificateIDs) {
			continue
		}
		assignedID := item.ID
		if allocator != nil {
			assignedID = allocator.AllocateListenerID(item.ID)
		}
		listenerIDMap[item.ID] = assignedID
		conflictIndex[conflictKey] = assignedID
	}
	return listenerIDMap
}

func previewRelayListenersByID(existing []storage.RelayListenerRow, incoming []BackupRelayListener, listenerIDMap map[int]int, agentIDMap map[string]string, cfg config.Config) map[int]storage.RelayListenerRow {
	listenersByID := map[int]storage.RelayListenerRow{}
	existingConflictIDs := map[string]int{}
	for _, row := range existing {
		if row.ID > 0 {
			listenersByID[row.ID] = row
			existingConflictIDs[relayConflictKey(row.AgentID, row.Name)] = row.ID
		}
	}
	for _, item := range incoming {
		mappedID, ok := listenerIDMap[item.ID]
		if !ok || mappedID <= 0 {
			continue
		}
		resolvedAgentID, ok := resolveAgentID(item.AgentID, agentIDMap, cfg)
		if !ok {
			continue
		}
		conflictKey := relayConflictKey(resolvedAgentID, item.Name)
		if existingID, exists := existingConflictIDs[conflictKey]; exists && existingID == mappedID {
			continue
		}
		if _, exists := listenersByID[mappedID]; exists {
			continue
		}
		row := relayListenerToRow(RelayListener{
			ID:                 mappedID,
			AgentID:            resolvedAgentID,
			Name:               item.Name,
			Enabled:            item.Enabled,
			TransportMode:      defaultString(item.TransportMode, "tls_tcp"),
			WireGuardProfileID: copyOptionalInt(item.WireGuardProfileID),
		})
		listenersByID[mappedID] = row
	}
	return listenersByID
}

func appendKnownAgentIDs(known []string, agentIDMap map[string]string) []string {
	seen := make(map[string]struct{}, len(known)+len(agentIDMap))
	out := make([]string, 0, len(known)+len(agentIDMap))
	for _, agentID := range known {
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
	for _, agentID := range agentIDMap {
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
	return out
}

func previewAgentIDMap(manifest BackupManifest, agents []BackupAgent, existingByName map[string]storage.AgentRow, existingByID map[string]storage.AgentRow, cfg config.Config) map[string]string {
	agentIDMap := map[string]string{}
	for id := range existingByID {
		agentIDMap[id] = id
	}
	if cfg.EnableLocalAgent && strings.TrimSpace(cfg.LocalAgentID) != "" {
		agentIDMap[cfg.LocalAgentID] = cfg.LocalAgentID
	}
	for _, item := range agents {
		if strings.TrimSpace(item.ID) == "" || strings.TrimSpace(item.Name) == "" || strings.TrimSpace(item.AgentToken) == "" {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(item.Mode), "local") && cfg.EnableLocalAgent {
			agentIDMap[item.ID] = cfg.LocalAgentID
			continue
		}
		if existingRow, ok := existingByName[item.Name]; ok {
			agentIDMap[item.ID] = existingRow.ID
			continue
		}
		if existingRow, ok := existingByID[item.ID]; ok {
			agentIDMap[item.ID] = existingRow.ID
			continue
		}
		if trimmed := strings.TrimSpace(item.ID); trimmed != "" {
			agentIDMap[item.ID] = trimmed
		}
	}
	if srcID := strings.TrimSpace(manifest.SourceLocalAgentID); srcID != "" && cfg.EnableLocalAgent {
		agentIDMap[srcID] = cfg.LocalAgentID
	}
	return agentIDMap
}

func (s *backupService) Import(ctx context.Context, archive []byte) (BackupImportResult, error) {
	bundle, err := decodeBackupBundle(archive)
	if err != nil {
		return BackupImportResult{}, err
	}
	if bundle.Manifest.PackageVersion != BackupPackageVersion {
		return BackupImportResult{}, fmt.Errorf("%w: unsupported backup package version %d", ErrInvalidArgument, bundle.Manifest.PackageVersion)
	}
	snapshot, err := s.captureState(ctx)
	if err != nil {
		return BackupImportResult{}, err
	}
	result, err := s.importBundle(ctx, bundle)
	if err != nil {
		if rollbackErr := s.restoreState(ctx, snapshot); rollbackErr != nil {
			return BackupImportResult{}, fmt.Errorf("%v (rollback failed: %v)", err, rollbackErr)
		}
		return BackupImportResult{}, err
	}
	return result, nil
}

func (s *backupService) exportBundle(ctx context.Context) (BackupBundle, error) {
	agentRows, err := s.store.ListAgents(ctx)
	if err != nil {
		return BackupBundle{}, err
	}
	knownAgentIDs, err := allKnownAgentIDs(ctx, s.cfg, s.store)
	if err != nil {
		return BackupBundle{}, err
	}

	bundle := BackupBundle{
		Agents:            make([]BackupAgent, 0, len(agentRows)),
		HTTPRules:         []BackupHTTPRule{},
		L4Rules:           []BackupL4Rule{},
		WireGuardProfiles: []BackupWireGuardProfile{},
		WireGuardClients:  []BackupWireGuardClient{},
		RelayListeners:    []BackupRelayListener{},
		Certificates:      []BackupCertificate{},
		VersionPolicies:   []BackupVersionPolicy{},
		TrafficPolicies:   []BackupTrafficPolicy{},
		TrafficBaselines:  []BackupTrafficBaseline{},
		Materials:         []BackupCertificateFile{},
	}

	for _, row := range agentRows {
		bundle.Agents = append(bundle.Agents, backupAgentFromRow(row))
	}

	for _, agentID := range knownAgentIDs {
		ruleRows, err := s.store.ListHTTPRules(ctx, agentID)
		if err != nil {
			return BackupBundle{}, err
		}
		for _, row := range ruleRows {
			bundle.HTTPRules = append(bundle.HTTPRules, backupHTTPRuleFromRule(httpRuleFromRow(row)))
		}

		l4Rows, err := s.store.ListL4Rules(ctx, agentID)
		if err != nil {
			return BackupBundle{}, err
		}
		for _, row := range l4Rows {
			bundle.L4Rules = append(bundle.L4Rules, backupL4RuleFromRule(l4RuleFromRow(row)))
		}

		wireGuardRows, err := s.store.ListWireGuardProfiles(ctx, agentID)
		if err != nil {
			return BackupBundle{}, err
		}
		for _, row := range wireGuardRows {
			bundle.WireGuardProfiles = append(bundle.WireGuardProfiles, backupWireGuardProfileFromRow(row))
			clientRows, err := s.store.ListWireGuardClients(ctx, agentID, row.ID)
			if err != nil {
				return BackupBundle{}, err
			}
			for _, clientRow := range clientRows {
				bundle.WireGuardClients = append(bundle.WireGuardClients, backupWireGuardClientFromRow(clientRow))
			}
		}
	}

	listenerRows, err := s.store.ListRelayListeners(ctx, "")
	if err != nil {
		return BackupBundle{}, err
	}
	for _, row := range listenerRows {
		bundle.RelayListeners = append(bundle.RelayListeners, relayListenerFromRow(row))
	}

	certRows, err := s.store.ListManagedCertificates(ctx)
	if err != nil {
		return BackupBundle{}, err
	}
	for _, row := range certRows {
		cert := managedCertificateFromRow(row)
		bundle.Certificates = append(bundle.Certificates, cert)
		material, ok, loadErr := s.store.LoadManagedCertificateMaterial(ctx, cert.Domain)
		if loadErr != nil {
			return BackupBundle{}, loadErr
		}
		if ok && strings.TrimSpace(material.CertPEM) != "" && strings.TrimSpace(material.KeyPEM) != "" {
			bundle.Materials = append(bundle.Materials, BackupCertificateFile{
				Domain:  cert.Domain,
				CertPEM: material.CertPEM,
				KeyPEM:  material.KeyPEM,
			})
		}
	}

	policyRows, err := s.store.ListVersionPolicies(ctx)
	if err != nil {
		return BackupBundle{}, err
	}
	for _, row := range policyRows {
		bundle.VersionPolicies = append(bundle.VersionPolicies, versionPolicyFromRow(row))
	}

	trafficPolicies, err := s.store.ListTrafficPolicies(ctx)
	if err != nil {
		return BackupBundle{}, err
	}
	for _, row := range trafficPolicies {
		bundle.TrafficPolicies = append(bundle.TrafficPolicies, backupTrafficPolicyFromRow(row))
	}

	trafficBaselines, err := s.store.ListTrafficBaselines(ctx)
	if err != nil {
		return BackupBundle{}, err
	}
	for _, row := range trafficBaselines {
		bundle.TrafficBaselines = append(bundle.TrafficBaselines, backupTrafficBaselineFromRow(row))
	}

	bundle.Manifest = BackupManifest{
		PackageVersion:       BackupPackageVersion,
		SourceArchitecture:   BackupSourceArchitectureGo,
		SourceAppVersion:     backupAppVersion(),
		SourceLocalAgentID:   s.cfg.LocalAgentID,
		ExportedAt:           s.now().UTC(),
		IncludesCertificates: len(bundle.Materials) > 0,
		Counts: BackupCounts{
			Agents:            len(bundle.Agents),
			HTTPRules:         len(bundle.HTTPRules),
			L4Rules:           len(bundle.L4Rules),
			WireGuardProfiles: len(bundle.WireGuardProfiles),
			WireGuardClients:  len(bundle.WireGuardClients),
			RelayListeners:    len(bundle.RelayListeners),
			Certificates:      len(bundle.Certificates),
			VersionPolicies:   len(bundle.VersionPolicies),
			TrafficPolicies:   len(bundle.TrafficPolicies),
			TrafficBaselines:  len(bundle.TrafficBaselines),
		},
	}
	return bundle, nil
}

func (s *backupService) importBundle(ctx context.Context, bundle BackupBundle) (BackupImportResult, error) {
	result := newBackupImportResult(bundle.Manifest)

	agentRows, err := s.store.ListAgents(ctx)
	if err != nil {
		return BackupImportResult{}, err
	}
	agentIDMap, err := s.importAgents(ctx, agentRows, bundle.Agents, &result)
	if err != nil {
		return BackupImportResult{}, err
	}

	if srcID := strings.TrimSpace(bundle.Manifest.SourceLocalAgentID); srcID != "" && s.cfg.EnableLocalAgent {
		if _, mapped := agentIDMap[srcID]; !mapped {
			agentIDMap[srcID] = s.cfg.LocalAgentID
		}
	}

	allocator, err := newConfigIdentityAllocatorFromStore(ctx, s.cfg, s.store)
	if err != nil {
		return BackupImportResult{}, err
	}
	modifiedAgents := modifiedAgentRevisions{}

	certRows, err := s.store.ListManagedCertificates(ctx)
	if err != nil {
		return BackupImportResult{}, err
	}
	certIDMap, err := s.importCertificates(ctx, certRows, bundle.Certificates, bundle.Materials, agentIDMap, &result, modifiedAgents, allocator)
	if err != nil {
		return BackupImportResult{}, err
	}

	wireGuardProfileIDMap, enabledWireGuardProfileIDs, importedWireGuardProfileIDs, skippedConflictWireGuardProfileIDs, err := s.importWireGuardProfiles(ctx, bundle.WireGuardProfiles, agentIDMap, &result, modifiedAgents, allocator)
	if err != nil {
		return BackupImportResult{}, err
	}
	if err := s.importWireGuardClients(ctx, bundle.WireGuardClients, agentIDMap, wireGuardProfileIDMap, importedWireGuardProfileIDs, skippedConflictWireGuardProfileIDs, &result, modifiedAgents, allocator); err != nil {
		return BackupImportResult{}, err
	}

	listenerRows, err := s.store.ListRelayListeners(ctx, "")
	if err != nil {
		return BackupImportResult{}, err
	}
	listenerIDMap, err := s.importRelayListeners(ctx, listenerRows, bundle.RelayListeners, agentIDMap, certIDMap, wireGuardProfileIDMap, enabledWireGuardProfileIDs, &result, modifiedAgents, allocator)
	if err != nil {
		return BackupImportResult{}, err
	}

	policyRows, err := s.store.ListVersionPolicies(ctx)
	if err != nil {
		return BackupImportResult{}, err
	}
	if err := s.importVersionPolicies(ctx, policyRows, bundle.VersionPolicies, &result); err != nil {
		return BackupImportResult{}, err
	}

	if err := s.importTrafficPolicies(ctx, bundle.TrafficPolicies, agentIDMap, &result); err != nil {
		return BackupImportResult{}, err
	}
	if err := s.importTrafficBaselines(ctx, bundle.TrafficBaselines, agentIDMap, &result); err != nil {
		return BackupImportResult{}, err
	}

	if err := s.importHTTPRules(ctx, bundle.HTTPRules, agentIDMap, listenerIDMap, wireGuardProfileIDMap, enabledWireGuardProfileIDs, &result, modifiedAgents, allocator); err != nil {
		return BackupImportResult{}, err
	}
	if err := s.importL4Rules(ctx, bundle.L4Rules, agentIDMap, listenerIDMap, wireGuardProfileIDMap, enabledWireGuardProfileIDs, importedWireGuardProfileIDs, &result, modifiedAgents, allocator); err != nil {
		return BackupImportResult{}, err
	}
	if err := s.bumpModifiedAgents(ctx, modifiedAgents); err != nil {
		return BackupImportResult{}, err
	}

	return result, nil
}

func (s *backupService) bumpModifiedAgents(ctx context.Context, modifiedAgents modifiedAgentRevisions) error {
	rows, err := s.store.ListAgents(ctx)
	if err != nil {
		return err
	}
	rowsByID := make(map[string]storage.AgentRow, len(rows))
	for _, row := range rows {
		rowsByID[row.ID] = row
	}

	for agentID, importedRevision := range modifiedAgents {
		if s.cfg.EnableLocalAgent && agentID == s.cfg.LocalAgentID {
			continue
		}
		row, ok := rowsByID[agentID]
		if !ok {
			continue
		}
		nextDesired := row.CurrentRevision + 1
		if importedRevision > nextDesired {
			nextDesired = importedRevision
		}
		if row.DesiredRevision < nextDesired {
			row.DesiredRevision = nextDesired
			if err := s.store.SaveAgent(ctx, row); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *backupService) importAgents(ctx context.Context, existing []storage.AgentRow, incoming []BackupAgent, result *BackupImportResult) (map[string]string, error) {
	agentIDMap := map[string]string{}
	existingByID := make(map[string]storage.AgentRow, len(existing))
	existingByName := make(map[string]storage.AgentRow, len(existing))
	for _, row := range existing {
		existingByID[row.ID] = row
		existingByName[row.Name] = row
		agentIDMap[row.ID] = row.ID
	}
	if s.cfg.EnableLocalAgent {
		agentIDMap[s.cfg.LocalAgentID] = s.cfg.LocalAgentID
		if strings.TrimSpace(s.cfg.LocalAgentName) != "" {
			existingByName[s.cfg.LocalAgentName] = storage.AgentRow{ID: s.cfg.LocalAgentID, Name: s.cfg.LocalAgentName}
		}
	}

	for _, item := range incoming {
		key := strings.TrimSpace(item.Name)
		if key == "" {
			key = strings.TrimSpace(item.ID)
		}
		if strings.TrimSpace(item.ID) == "" || strings.TrimSpace(item.Name) == "" || strings.TrimSpace(item.AgentToken) == "" {
			result.addSkippedInvalid("agent", key, "agent id, name, and agent_token are required")
			continue
		}
		if strings.EqualFold(strings.TrimSpace(item.Mode), "local") && s.cfg.EnableLocalAgent {
			if item.ID != s.cfg.LocalAgentID {
				agentIDMap[item.ID] = s.cfg.LocalAgentID
			}
			result.addSkippedConflict("agent", item.Name, "local agent remapped to target")
			continue
		}
		if existingRow, ok := existingByName[item.Name]; ok {
			agentIDMap[item.ID] = existingRow.ID
			result.addSkippedConflict("agent", item.Name, "agent name already exists")
			continue
		}
		if existingRow, ok := existingByID[item.ID]; ok {
			agentIDMap[item.ID] = existingRow.ID
			result.addSkippedConflict("agent", item.Name, "agent id already exists")
			continue
		}
		row := storage.AgentRow{
			ID:                     strings.TrimSpace(item.ID),
			Name:                   strings.TrimSpace(item.Name),
			AgentURL:               strings.TrimSpace(item.AgentURL),
			AgentToken:             strings.TrimSpace(item.AgentToken),
			Version:                strings.TrimSpace(item.Version),
			Platform:               strings.TrimSpace(item.Platform),
			RuntimePackageVersion:  strings.TrimSpace(item.RuntimePackageVersion),
			RuntimePackagePlatform: strings.TrimSpace(item.RuntimePackagePlatform),
			RuntimePackageArch:     strings.TrimSpace(item.RuntimePackageArch),
			RuntimePackageSHA256:   strings.TrimSpace(item.RuntimePackageSHA256),
			DesiredVersion:         strings.TrimSpace(item.DesiredVersion),
			DesiredRevision:        item.DesiredRevision,
			OutboundProxyURL:       strings.TrimSpace(item.OutboundProxyURL),
			TrafficStatsInterval:   strings.TrimSpace(item.TrafficStatsInterval),
			TagsJSON:               marshalJSON(normalizeTags(item.Tags), "[]"),
			CapabilitiesJSON:       marshalJSON(normalizeTags(item.Capabilities), "[]"),
			Mode:                   strings.TrimSpace(item.Mode),
		}
		if err := s.store.SaveAgent(ctx, row); err != nil {
			return nil, err
		}
		existingByID[row.ID] = row
		existingByName[row.Name] = row
		agentIDMap[item.ID] = row.ID
		result.addImported("agent", row.Name)
	}
	return agentIDMap, nil
}

func (s *backupService) importCertificates(ctx context.Context, existing []storage.ManagedCertificateRow, incoming []BackupCertificate, materials []BackupCertificateFile, agentIDMap map[string]string, result *BackupImportResult, modifiedAgents modifiedAgentRevisions, allocator *configIdentityAllocator) (map[int]int, error) {
	certSvc := newCertificateServiceWithRenewal(s.cfg, s.store, nil)
	certIDMap := map[int]int{}
	existingByDomain := make(map[string]ManagedCertificate, len(existing))
	maxRevision := 0
	for _, row := range existing {
		cert := managedCertificateFromRow(row)
		existingByDomain[cert.Domain] = cert
		if row.Revision > maxRevision {
			maxRevision = row.Revision
		}
		certIDMap[cert.ID] = cert.ID
	}

	materialByDomain := make(map[string]BackupCertificateFile, len(materials))
	for _, material := range materials {
		materialByDomain[strings.TrimSpace(material.Domain)] = material
	}

	nextRows := append([]storage.ManagedCertificateRow(nil), existing...)
	pendingMaterials := []BackupCertificateFile{}
	for _, item := range incoming {
		key := strings.TrimSpace(item.Domain)
		if key == "" {
			key = fmt.Sprintf("#%d", item.ID)
		}
		if existingCert, ok := existingByDomain[item.Domain]; ok {
			certIDMap[item.ID] = existingCert.ID
			result.addSkippedConflict("certificate", key, "certificate domain already exists")
			continue
		}

		targetIDs, ok := remapAgentIDs(item.TargetAgentIDs, agentIDMap)
		if !ok {
			result.addSkippedInvalid("certificate", key, "certificate references unknown agent")
			continue
		}

		input := ManagedCertificateInput{
			Domain:          backupStringPtr(item.Domain),
			Enabled:         backupBoolPtr(item.Enabled),
			Scope:           backupStringPtr(item.Scope),
			IssuerMode:      backupStringPtr(item.IssuerMode),
			TargetAgentIDs:  &targetIDs,
			Status:          backupStringPtr(item.Status),
			LastIssueAt:     backupStringPtr(item.LastIssueAt),
			LastError:       backupStringPtr(item.LastError),
			MaterialHash:    backupStringPtr(item.MaterialHash),
			AgentReports:    &item.AgentReports,
			ACMEInfo:        &item.ACMEInfo,
			Tags:            &item.Tags,
			Usage:           backupStringPtr(item.Usage),
			CertificateType: backupStringPtr(item.CertificateType),
			SelfSigned:      backupBoolPtr(item.SelfSigned),
		}
		normalized, err := normalizeManagedCertificateInput(input, ManagedCertificate{}, 0, s.cfg.LocalAgentID, true)
		if err != nil {
			result.addSkippedInvalid("certificate", key, err.Error())
			continue
		}
		normalized.TargetAgentIDs = targetIDs
		if err := assertManagedCertificateMutationAllowed(nil, normalized); err != nil {
			result.addSkippedInvalid("certificate", key, err.Error())
			continue
		}
		if err := assertManagedCertificateTargetingAllowed(s.cfg, normalized); err != nil {
			result.addSkippedInvalid("certificate", key, err.Error())
			continue
		}
		if err := certSvc.assertCertificateDistributionTargetsAllowed(ctx, normalized); err != nil {
			result.addSkippedInvalid("certificate", key, err.Error())
			continue
		}

		material, hasMaterial := materialByDomain[item.Domain]
		if certificateRequiresMaterial(normalized) && (!hasMaterial || strings.TrimSpace(material.CertPEM) == "" || strings.TrimSpace(material.KeyPEM) == "") {
			result.addSkippedMissingMaterial("certificate", key, "certificate material missing from backup")
			continue
		}

		assignedID := allocator.AllocateCertificateID(item.ID)
		certIDMap[item.ID] = assignedID
		normalized.ID = assignedID
		normalized.Revision = allocator.AllocateRevisionForTargets(targetIDs, maxRevision)
		if normalized.Revision > maxRevision {
			maxRevision = normalized.Revision
		}
		for _, targetID := range targetIDs {
			recordModifiedAgentRevision(modifiedAgents, targetID, normalized.Revision)
		}
		if hasMaterial {
			normalized.MaterialHash = hashManagedCertificateMaterial(strings.TrimSpace(material.CertPEM), strings.TrimSpace(material.KeyPEM))
			pendingMaterials = append(pendingMaterials, BackupCertificateFile{
				Domain:  normalized.Domain,
				CertPEM: material.CertPEM,
				KeyPEM:  material.KeyPEM,
			})
		}
		nextRows = append(nextRows, managedCertificateToRow(normalized))
		existingByDomain[normalized.Domain] = normalized
		result.addImported("certificate", key)
	}

	if !managedCertificateRowsEqual(existing, nextRows) {
		if err := s.store.SaveManagedCertificates(ctx, nextRows); err != nil {
			return nil, err
		}
	}
	for _, material := range pendingMaterials {
		if err := s.store.SaveManagedCertificateMaterial(ctx, material.Domain, storage.ManagedCertificateBundle{
			Domain:  material.Domain,
			CertPEM: material.CertPEM,
			KeyPEM:  material.KeyPEM,
		}); err != nil {
			return nil, err
		}
	}
	return certIDMap, nil
}

func (s *backupService) importRelayListeners(ctx context.Context, existing []storage.RelayListenerRow, incoming []BackupRelayListener, agentIDMap map[string]string, certIDMap map[int]int, wireGuardProfileIDMap map[string]int, enabledWireGuardProfileIDs map[string]struct{}, result *BackupImportResult, modifiedAgents modifiedAgentRevisions, allocator *configIdentityAllocator) (map[int]int, error) {
	listenerIDMap := map[int]int{}
	maxRevisionByAgent := map[string]int{}
	grouped := map[string][]storage.RelayListenerRow{}
	conflictIndex := map[string]RelayListener{}

	for _, row := range existing {
		grouped[row.AgentID] = append(grouped[row.AgentID], row)
		listener := relayListenerFromRow(row)
		conflictIndex[relayConflictKey(listener.AgentID, listener.Name)] = listener
		listenerIDMap[listener.ID] = listener.ID
		if row.Revision > maxRevisionByAgent[row.AgentID] {
			maxRevisionByAgent[row.AgentID] = row.Revision
		}
	}

	for _, item := range incoming {
		resolvedAgentID, ok := resolveAgentID(item.AgentID, agentIDMap, s.cfg)
		key := relayConflictKey(item.AgentID, item.Name)
		if !ok {
			result.addSkippedInvalid("relay_listener", key, "relay listener references unknown agent")
			continue
		}
		conflictKey := relayConflictKey(resolvedAgentID, item.Name)
		if existingListener, ok := conflictIndex[conflictKey]; ok {
			listenerIDMap[item.ID] = existingListener.ID
			result.addSkippedConflict("relay_listener", conflictKey, "relay listener already exists")
			continue
		}
		if strings.EqualFold(strings.TrimSpace(item.TransportMode), "wireguard") {
			if err := ensureAgentSupportsWireGuardCapability(ctx, s.cfg, s.store, resolvedAgentID); err != nil {
				result.addSkippedInvalid("relay_listener", conflictKey, err.Error())
				continue
			}
		}

		wireGuardProfileID, ok := remapBackupWireGuardProfileID(item.AgentID, item.WireGuardProfileID, wireGuardProfileIDMap, enabledWireGuardProfileIDs)
		if strings.EqualFold(strings.TrimSpace(item.TransportMode), "wireguard") && !ok {
			result.addSkippedInvalid("relay_listener", conflictKey, "wireguard profile was not imported")
			continue
		}
		input := relayListenerInputFromBackup(item, certIDMap, wireGuardProfileID)
		if item.CertificateID != nil && input.CertificateID == nil {
			result.addSkippedInvalid("relay_listener", conflictKey, "referenced certificate was not imported")
			continue
		}
		if len(item.TrustedCACertificateIDs) > 0 && len(pointerIntSlice(input.TrustedCACertificateIDs)) != len(item.TrustedCACertificateIDs) {
			result.addSkippedInvalid("relay_listener", conflictKey, "referenced trusted CA certificate was not imported")
			continue
		}

		assignedID := allocator.AllocateListenerID(item.ID)
		normalized, err := normalizeRelayListenerInput(input, RelayListener{}, assignedID, relayNormalizeOptions{})
		if err != nil {
			result.addSkippedInvalid("relay_listener", conflictKey, err.Error())
			continue
		}
		normalized.AgentID = resolvedAgentID

		listenerIDMap[item.ID] = assignedID
		normalized.ID = assignedID
		normalized.Revision = allocator.AllocateRevisionForAgent(resolvedAgentID, maxRevisionByAgent[resolvedAgentID])
		if normalized.Revision > maxRevisionByAgent[resolvedAgentID] {
			maxRevisionByAgent[resolvedAgentID] = normalized.Revision
		}
		conflictIndex[conflictKey] = normalized
		grouped[resolvedAgentID] = append(grouped[resolvedAgentID], relayListenerToRow(normalized))
		recordModifiedAgentRevision(modifiedAgents, resolvedAgentID, normalized.Revision)
		result.addImported("relay_listener", conflictKey)
	}

	for agentID, rows := range grouped {
		existingRows, err := s.store.ListRelayListeners(ctx, agentID)
		if err != nil {
			return nil, err
		}
		if relayListenerRowsEqual(existingRows, rows) {
			continue
		}
		if err := s.store.SaveRelayListeners(ctx, agentID, rows); err != nil {
			return nil, err
		}
	}
	return listenerIDMap, nil
}

func (s *backupService) importVersionPolicies(ctx context.Context, existing []storage.VersionPolicyRow, incoming []BackupVersionPolicy, result *BackupImportResult) error {
	existingByID := make(map[string]VersionPolicy, len(existing))
	for _, row := range existing {
		policy := versionPolicyFromRow(row)
		existingByID[policy.ID] = policy
	}
	next := append([]storage.VersionPolicyRow(nil), existing...)
	for _, item := range incoming {
		key := strings.TrimSpace(item.ID)
		if key == "" {
			result.addSkippedInvalid("version_policy", "unknown", "version policy id is required")
			continue
		}
		if _, ok := existingByID[item.ID]; ok {
			result.addSkippedConflict("version_policy", key, "version policy already exists")
			continue
		}
		normalized, err := normalizeVersionPolicyInput(VersionPolicyInput{
			ID:             backupStringPtr(item.ID),
			Channel:        backupStringPtr(item.Channel),
			DesiredVersion: backupStringPtr(item.DesiredVersion),
			Packages:       &item.Packages,
			Tags:           &item.Tags,
		}, VersionPolicy{}, item.ID)
		if err != nil {
			result.addSkippedInvalid("version_policy", key, err.Error())
			continue
		}
		next = append(next, versionPolicyToRow(normalized))
		existingByID[normalized.ID] = normalized
		result.addImported("version_policy", key)
	}
	if len(next) != len(existing) {
		if err := s.store.SaveVersionPolicies(ctx, next); err != nil {
			return err
		}
	}
	return nil
}

func (s *backupService) importTrafficPolicies(ctx context.Context, incoming []BackupTrafficPolicy, agentIDMap map[string]string, result *BackupImportResult) error {
	for _, item := range incoming {
		key := strings.TrimSpace(item.AgentID)
		resolvedAgentID, ok := resolveAgentID(item.AgentID, agentIDMap, s.cfg)
		if !ok {
			result.addSkippedInvalid("traffic_policy", key, "traffic policy references unknown agent")
			continue
		}
		row := trafficPolicyRowFromBackup(item)
		row.AgentID = resolvedAgentID
		if err := s.store.SaveTrafficPolicy(ctx, row); err != nil {
			return err
		}
		result.addImported("traffic_policy", key)
	}
	return nil
}

func (s *backupService) importTrafficBaselines(ctx context.Context, incoming []BackupTrafficBaseline, agentIDMap map[string]string, result *BackupImportResult) error {
	for _, item := range incoming {
		key := trafficBaselineKey(item.AgentID, item.CycleStart)
		resolvedAgentID, ok := resolveAgentID(item.AgentID, agentIDMap, s.cfg)
		if !ok {
			result.addSkippedInvalid("traffic_baseline", key, "traffic baseline references unknown agent")
			continue
		}
		row := trafficBaselineRowFromBackup(item)
		row.AgentID = resolvedAgentID
		if err := s.store.SaveTrafficBaseline(ctx, row); err != nil {
			return err
		}
		result.addImported("traffic_baseline", key)
	}
	return nil
}

func (s *backupService) importWireGuardClients(ctx context.Context, incoming []BackupWireGuardClient, agentIDMap map[string]string, wireGuardProfileIDMap map[string]int, acceptedProfileIDs map[string]struct{}, skippedConflictProfileIDs map[string]struct{}, result *BackupImportResult, modifiedAgents modifiedAgentRevisions, allocator *configIdentityAllocator) error {
	grouped := map[string]map[int][]storage.WireGuardClientRow{}
	skippedGeneratedPeers := map[string]map[int][]storage.WireGuardClientRow{}
	touchedProfiles := map[string]map[int]struct{}{}
	markTouchedProfile := func(agentID string, profileID int) {
		if touchedProfiles[agentID] == nil {
			touchedProfiles[agentID] = map[int]struct{}{}
		}
		touchedProfiles[agentID][profileID] = struct{}{}
	}
	for _, item := range incoming {
		key := wireGuardClientBackupKey(item.AgentID, item.ProfileID, item.Name, item.ID)
		resolvedAgentID, ok := resolveAgentID(item.AgentID, agentIDMap, s.cfg)
		if !ok {
			result.addSkippedInvalid("wireguard_client", key, "wireguard client references unknown agent")
			continue
		}
		mappedProfileID, ok := remapBackupWireGuardClientProfileID(item.AgentID, resolvedAgentID, item.ProfileID, wireGuardProfileIDMap, acceptedProfileIDs)
		if !ok {
			if _, conflict := skippedConflictProfileIDs[wireGuardProfileKey(item.AgentID, item.ProfileID)]; conflict {
				result.addSkippedConflict("wireguard_client", key, "wireguard profile was skipped due to conflict")
			} else {
				result.addSkippedInvalid("wireguard_client", key, "wireguard profile was not imported")
			}
			continue
		}
		row := wireGuardClientRowFromBackup(item, resolvedAgentID, mappedProfileID)
		if err := validateBackupWireGuardClientRow(row); err != nil {
			result.addSkippedInvalid("wireguard_client", key, err.Error())
			if strings.TrimSpace(row.PublicKey) != "" {
				if skippedGeneratedPeers[resolvedAgentID] == nil {
					skippedGeneratedPeers[resolvedAgentID] = map[int][]storage.WireGuardClientRow{}
				}
				skippedGeneratedPeers[resolvedAgentID][mappedProfileID] = append(skippedGeneratedPeers[resolvedAgentID][mappedProfileID], row)
				markTouchedProfile(resolvedAgentID, mappedProfileID)
			}
			continue
		}
		if grouped[resolvedAgentID] == nil {
			grouped[resolvedAgentID] = map[int][]storage.WireGuardClientRow{}
		}
		grouped[resolvedAgentID][mappedProfileID] = append(grouped[resolvedAgentID][mappedProfileID], row)
		markTouchedProfile(resolvedAgentID, mappedProfileID)
		result.addImported("wireguard_client", key)
	}

	for agentID, profileIDs := range touchedProfiles {
		profiles, err := s.store.ListWireGuardProfiles(ctx, agentID)
		if err != nil {
			return err
		}
		profileIndexByID := map[int]int{}
		for i := range profiles {
			profileIndexByID[profiles[i].ID] = i
		}
		profilesChanged := false
		for profileID := range profileIDs {
			rows, hasValidRows := grouped[agentID][profileID]
			index, ok := profileIndexByID[profileID]
			var existingClients []storage.WireGuardClientRow
			if ok {
				var err error
				existingClients, err = s.store.ListWireGuardClients(ctx, agentID, profileID)
				if err != nil {
					return err
				}
			}
			if hasValidRows {
				if err := s.store.SaveWireGuardClients(ctx, agentID, profileID, rows); err != nil {
					return err
				}
			}
			if !ok {
				continue
			}
			profile := wireGuardProfileFromRow(profiles[index])
			profile.Peers = removeWireGuardGeneratedClientPeers(profile.Peers, existingClients)
			profile.Peers = removeWireGuardGeneratedClientPeers(profile.Peers, skippedGeneratedPeers[agentID][profileID])
			profile.Peers = reconcileWireGuardGeneratedClientPeers(profile.Peers, rows)
			nextRow := wireGuardProfileToRow(profile)
			if nextRow != profiles[index] {
				profile.Revision = allocator.AllocateRevisionForAgent(agentID, maxWireGuardProfileRevision(profiles))
				nextRow = wireGuardProfileToRow(profile)
				profiles[index] = nextRow
				profilesChanged = true
				recordModifiedAgentRevision(modifiedAgents, agentID, profile.Revision)
			}
		}
		if profilesChanged {
			if err := s.store.SaveWireGuardProfiles(ctx, agentID, profiles); err != nil {
				return err
			}
		}
	}
	return nil
}

func previewWireGuardClients(incoming []BackupWireGuardClient, agentIDMap map[string]string, wireGuardProfileIDMap map[string]int, acceptedProfileIDs map[string]struct{}, skippedConflictProfileIDs map[string]struct{}, result *BackupImportResult, cfg config.Config) error {
	for _, item := range incoming {
		key := wireGuardClientBackupKey(item.AgentID, item.ProfileID, item.Name, item.ID)
		resolvedAgentID, ok := resolveAgentID(item.AgentID, agentIDMap, cfg)
		if !ok {
			result.addSkippedInvalid("wireguard_client", key, "wireguard client references unknown agent")
			continue
		}
		mappedProfileID, ok := remapBackupWireGuardClientProfileID(item.AgentID, resolvedAgentID, item.ProfileID, wireGuardProfileIDMap, acceptedProfileIDs)
		if !ok {
			if _, conflict := skippedConflictProfileIDs[wireGuardProfileKey(item.AgentID, item.ProfileID)]; conflict {
				result.addSkippedConflict("wireguard_client", key, "wireguard profile was skipped due to conflict")
			} else {
				result.addSkippedInvalid("wireguard_client", key, "wireguard profile was not imported")
			}
			continue
		}
		row := wireGuardClientRowFromBackup(item, resolvedAgentID, mappedProfileID)
		if err := validateBackupWireGuardClientRow(row); err != nil {
			result.addSkippedInvalid("wireguard_client", key, err.Error())
			continue
		}
		result.addImported("wireguard_client", key)
	}
	return nil
}

func (s *backupService) importHTTPRules(ctx context.Context, incoming []BackupHTTPRule, agentIDMap map[string]string, listenerIDMap map[int]int, wireGuardProfileIDMap map[string]int, enabledWireGuardProfileIDs map[string]struct{}, result *BackupImportResult, modifiedAgents modifiedAgentRevisions, allocator *configIdentityAllocator) error {
	ruleSvc := &ruleService{cfg: s.cfg, store: s.store}
	knownAgentIDs, err := allKnownAgentIDs(ctx, s.cfg, s.store)
	if err != nil {
		return err
	}
	existingRules, err := s.listAllHTTPRules(ctx, knownAgentIDs)
	if err != nil {
		return err
	}
	conflictSet := map[string]struct{}{}
	grouped := map[string][]storage.HTTPRuleRow{}
	maxRevisionByAgent := map[string]int{}

	for _, row := range existingRules {
		key := httpRuleConflictKey(row.AgentID, row.FrontendURL)
		conflictSet[key] = struct{}{}
		grouped[row.AgentID] = append(grouped[row.AgentID], row)
		if row.Revision > maxRevisionByAgent[row.AgentID] {
			maxRevisionByAgent[row.AgentID] = row.Revision
		}
	}

	for _, item := range incoming {
		resolvedAgentID, ok := resolveAgentID(item.AgentID, agentIDMap, s.cfg)
		key := strings.TrimSpace(item.FrontendURL)
		if !ok {
			result.addSkippedInvalid("http_rule", key, "http rule references unknown agent")
			continue
		}
		conflictKey := httpRuleConflictKey(resolvedAgentID, item.FrontendURL)
		if _, exists := conflictSet[conflictKey]; exists {
			result.addSkippedConflict("http_rule", key, "frontend_url already exists")
			continue
		}
		if item.WireGuardEntryEnabled {
			if err := ensureAgentSupportsWireGuardCapability(ctx, s.cfg, s.store, resolvedAgentID); err != nil {
				result.addSkippedInvalid("http_rule", key, err.Error())
				continue
			}
		}

		wireGuardProfileID, profileOK := remapBackupWireGuardProfileID(item.AgentID, item.WireGuardProfileID, wireGuardProfileIDMap, enabledWireGuardProfileIDs)
		if item.WireGuardEntryEnabled && !profileOK {
			result.addSkippedInvalid("http_rule", key, "wireguard profile was not imported")
			continue
		}
		input := httpRuleInputFromBackup(item, listenerIDMap, wireGuardProfileID)
		if !remappedBackupRelayLayersComplete(item.RelayChain, item.RelayLayers, input.RelayLayers) {
			result.addSkippedInvalid("http_rule", key, "relay listener reference not available")
			continue
		}

		normalized, err := ruleSvc.normalizeHTTPRuleInput(ctx, input, HTTPRule{AgentID: resolvedAgentID}, 0)
		if err != nil {
			result.addSkippedInvalid("http_rule", key, err.Error())
			continue
		}
		normalized.AgentID = resolvedAgentID
		assignedID := allocator.AllocateRuleID(item.ID)
		normalized.ID = assignedID
		normalized.Revision = allocator.AllocateRevisionForAgent(resolvedAgentID, maxRevisionByAgent[resolvedAgentID])
		if normalized.Revision > maxRevisionByAgent[resolvedAgentID] {
			maxRevisionByAgent[resolvedAgentID] = normalized.Revision
		}
		grouped[resolvedAgentID] = append(grouped[resolvedAgentID], httpRuleToRow(normalized))
		conflictSet[conflictKey] = struct{}{}
		recordModifiedAgentRevision(modifiedAgents, resolvedAgentID, normalized.Revision)
		result.addImported("http_rule", key)
	}

	for agentID, rows := range grouped {
		existingRows, err := s.store.ListHTTPRules(ctx, agentID)
		if err != nil {
			return err
		}
		if httpRuleRowsEqual(existingRows, rows) {
			continue
		}
		if err := s.store.SaveHTTPRules(ctx, agentID, rows); err != nil {
			return err
		}
	}
	return nil
}

func (s *backupService) importL4Rules(ctx context.Context, incoming []BackupL4Rule, agentIDMap map[string]string, listenerIDMap map[int]int, wireGuardProfileIDMap map[string]int, enabledWireGuardProfileIDs map[string]struct{}, importedWireGuardProfileIDs map[string]struct{}, result *BackupImportResult, modifiedAgents modifiedAgentRevisions, allocator *configIdentityAllocator) error {
	l4Svc := &l4Service{cfg: s.cfg, store: s.store}
	knownAgentIDs, err := allKnownAgentIDs(ctx, s.cfg, s.store)
	if err != nil {
		return err
	}
	existingRules, err := s.listAllL4Rules(ctx, knownAgentIDs)
	if err != nil {
		return err
	}
	conflictSet := map[string]struct{}{}
	grouped := map[string][]storage.L4RuleRow{}
	maxRevisionByAgent := map[string]int{}

	for _, row := range existingRules {
		key := l4BackupConflictKey(row.AgentID, row.Protocol, row.ListenHost, row.ListenPort, row.ListenMode, row.WireGuardListenHost, row.WireGuardProfileID)
		conflictSet[key] = struct{}{}
		grouped[row.AgentID] = append(grouped[row.AgentID], row)
		if row.Revision > maxRevisionByAgent[row.AgentID] {
			maxRevisionByAgent[row.AgentID] = row.Revision
		}
	}

	for _, item := range incoming {
		resolvedAgentID, ok := resolveAgentID(item.AgentID, agentIDMap, s.cfg)
		wireGuardProfileID, profileOK := remapBackupWireGuardProfileID(item.AgentID, item.WireGuardProfileID, wireGuardProfileIDMap, enabledWireGuardProfileIDs)
		key := l4BackupConflictKey(resolvedAgentID, item.Protocol, item.ListenHost, item.ListenPort, item.ListenMode, item.WireGuardListenHost, wireGuardProfileID)
		if !ok {
			result.addSkippedInvalid("l4_rule", key, "l4 rule references unknown agent")
			continue
		}
		if _, exists := conflictSet[key]; exists {
			result.addSkippedConflict("l4_rule", key, "protocol/listen_host/listen_port already exists")
			continue
		}
		if strings.EqualFold(strings.TrimSpace(item.ListenMode), "wireguard") || strings.EqualFold(strings.TrimSpace(item.ProxyEgressMode), "wireguard") {
			if err := ensureAgentSupportsWireGuardCapability(ctx, s.cfg, s.store, resolvedAgentID); err != nil {
				result.addSkippedInvalid("l4_rule", key, err.Error())
				continue
			}
		}

		if (strings.EqualFold(strings.TrimSpace(item.ListenMode), "wireguard") || strings.EqualFold(strings.TrimSpace(item.ProxyEgressMode), "wireguard")) && !profileOK {
			result.addSkippedInvalid("l4_rule", key, "wireguard profile was not imported")
			continue
		}
		input := l4RuleInputFromBackup(item, listenerIDMap, wireGuardProfileID)
		if !remappedBackupRelayLayersComplete(item.RelayChain, item.RelayLayers, input.RelayLayers) {
			result.addSkippedInvalid("l4_rule", key, "relay listener reference not available")
			continue
		}

		normalized, err := normalizeL4RuleInput(input, L4Rule{AgentID: resolvedAgentID}, 0)
		if err != nil {
			result.addSkippedInvalid("l4_rule", key, err.Error())
			continue
		}
		if strings.EqualFold(strings.TrimSpace(item.ProxyEgressMode), "wireguard") {
			if rawURI := strings.TrimSpace(item.WireGuardEgressURI); rawURI != "" {
				normalized.WireGuardEgressURI = rawURI
			}
		}
		if err := l4Svc.validateRelayChain(ctx, resolvedAgentID, normalized.RelayChain); err != nil {
			result.addSkippedInvalid("l4_rule", key, err.Error())
			continue
		}
		if err := l4Svc.validateRelayChain(ctx, resolvedAgentID, flattenRelayLayers(normalized.RelayLayers)); err != nil {
			result.addSkippedInvalid("l4_rule", key, err.Error())
			continue
		}
		normalized.AgentID = resolvedAgentID
		assignedID := allocator.AllocateRuleID(item.ID)
		normalized.ID = assignedID
		if err := s.remapImportedL4WireGuardURIEgressProfileOwnership(ctx, item, resolvedAgentID, assignedID, wireGuardProfileID, importedWireGuardProfileIDs); err != nil {
			return err
		}
		normalized.Revision = allocator.AllocateRevisionForAgent(resolvedAgentID, maxRevisionByAgent[resolvedAgentID])
		if normalized.Revision > maxRevisionByAgent[resolvedAgentID] {
			maxRevisionByAgent[resolvedAgentID] = normalized.Revision
		}
		grouped[resolvedAgentID] = append(grouped[resolvedAgentID], l4RuleToRow(normalized))
		conflictSet[key] = struct{}{}
		recordModifiedAgentRevision(modifiedAgents, resolvedAgentID, normalized.Revision)
		result.addImported("l4_rule", key)
	}

	for agentID, rows := range grouped {
		existingRows, err := s.store.ListL4Rules(ctx, agentID)
		if err != nil {
			return err
		}
		if l4RuleRowsEqual(existingRows, rows) {
			continue
		}
		if err := s.store.SaveL4Rules(ctx, agentID, rows); err != nil {
			return err
		}
	}
	return nil
}

func (s *backupService) remapImportedL4WireGuardURIEgressProfileOwnership(ctx context.Context, item BackupL4Rule, resolvedAgentID string, assignedRuleID int, mappedProfileID *int, importedWireGuardProfileIDs map[string]struct{}) error {
	if assignedRuleID <= 0 || assignedRuleID == item.ID || mappedProfileID == nil || *mappedProfileID <= 0 || item.WireGuardProfileID == nil || *item.WireGuardProfileID <= 0 {
		return nil
	}
	if !strings.EqualFold(strings.TrimSpace(item.ProxyEgressMode), "wireguard") {
		return nil
	}
	rawURI := strings.TrimSpace(item.WireGuardEgressURI)
	if rawURI == "" {
		return nil
	}
	if !backupWireGuardProfileWasImported(item.AgentID, resolvedAgentID, *item.WireGuardProfileID, importedWireGuardProfileIDs) {
		return nil
	}
	parsed, err := ParseWireGuardURI(rawURI)
	if err != nil {
		return nil
	}

	sourceProfileName := fmt.Sprintf("l4-rule-%d-wireguard-egress", item.ID)
	targetProfileInput := wireGuardProfileInputFromURI(parsed, fmt.Sprintf("l4-rule-%d-wireguard-egress", assignedRuleID))
	targetProfileName := strings.TrimSpace(targetProfileInput.Name)
	if targetProfileName == "" {
		return nil
	}
	sourceProfileInput := wireGuardProfileInputFromURI(parsed, sourceProfileName)
	if strings.TrimSpace(sourceProfileInput.Name) == targetProfileName {
		return nil
	}

	rows, err := s.store.ListWireGuardProfiles(ctx, resolvedAgentID)
	if err != nil {
		return err
	}
	for i := range rows {
		if rows[i].ID != *mappedProfileID {
			continue
		}
		if !wireGuardProfileRowMatchesURI(rows[i], parsed, sourceProfileName) {
			return nil
		}
		nextRows := append([]storage.WireGuardProfileRow(nil), rows...)
		nextRows[i].Name = targetProfileName
		return s.store.SaveWireGuardProfiles(ctx, resolvedAgentID, nextRows)
	}
	return nil
}

func backupWireGuardProfileWasImported(sourceAgentID string, resolvedAgentID string, sourceProfileID int, importedWireGuardProfileIDs map[string]struct{}) bool {
	if sourceProfileID <= 0 {
		return false
	}
	for _, key := range []string{
		wireGuardProfileKey(sourceAgentID, sourceProfileID),
		wireGuardProfileKey(resolvedAgentID, sourceProfileID),
	} {
		if _, ok := importedWireGuardProfileIDs[key]; ok {
			return true
		}
	}
	return false
}

func recordModifiedAgentRevision(modifiedAgents modifiedAgentRevisions, agentID string, revision int) {
	if revision <= 0 {
		return
	}
	if modifiedAgents[agentID] < revision {
		modifiedAgents[agentID] = revision
	}
}

func (s *backupService) listAllHTTPRules(ctx context.Context, agentIDs []string) ([]storage.HTTPRuleRow, error) {
	rows := []storage.HTTPRuleRow{}
	for _, agentID := range agentIDs {
		agentRows, err := s.store.ListHTTPRules(ctx, agentID)
		if err != nil {
			return nil, err
		}
		rows = append(rows, agentRows...)
	}
	return rows, nil
}

func (s *backupService) listAllL4Rules(ctx context.Context, agentIDs []string) ([]storage.L4RuleRow, error) {
	rows := []storage.L4RuleRow{}
	for _, agentID := range agentIDs {
		agentRows, err := s.store.ListL4Rules(ctx, agentID)
		if err != nil {
			return nil, err
		}
		rows = append(rows, agentRows...)
	}
	return rows, nil
}

func backupAgentFromRow(row storage.AgentRow) BackupAgent {
	return BackupAgent{
		ID:                     row.ID,
		Name:                   row.Name,
		AgentURL:               row.AgentURL,
		AgentToken:             row.AgentToken,
		Version:                row.Version,
		Platform:               row.Platform,
		RuntimePackageVersion:  row.RuntimePackageVersion,
		RuntimePackagePlatform: row.RuntimePackagePlatform,
		RuntimePackageArch:     row.RuntimePackageArch,
		RuntimePackageSHA256:   row.RuntimePackageSHA256,
		DesiredVersion:         row.DesiredVersion,
		DesiredRevision:        row.DesiredRevision,
		OutboundProxyURL:       row.OutboundProxyURL,
		TrafficStatsInterval:   row.TrafficStatsInterval,
		Tags:                   parseStringArray(row.TagsJSON),
		Capabilities:           parseStringArray(row.CapabilitiesJSON),
		Mode:                   row.Mode,
	}
}

func backupTrafficPolicyFromRow(row storage.AgentTrafficPolicyRow) BackupTrafficPolicy {
	return BackupTrafficPolicy{
		AgentID:                row.AgentID,
		Direction:              row.Direction,
		CycleStartDay:          row.CycleStartDay,
		MonthlyQuotaBytes:      row.MonthlyQuotaBytes,
		BlockWhenExceeded:      row.BlockWhenExceeded,
		HourlyRetentionDays:    row.HourlyRetentionDays,
		DailyRetentionMonths:   row.DailyRetentionMonths,
		MonthlyRetentionMonths: row.MonthlyRetentionMonths,
		UpdatedAt:              row.UpdatedAt,
		CreatedAt:              row.CreatedAt,
	}
}

func trafficPolicyRowFromBackup(item BackupTrafficPolicy) storage.AgentTrafficPolicyRow {
	return storage.AgentTrafficPolicyRow{
		AgentID:                item.AgentID,
		Direction:              item.Direction,
		CycleStartDay:          item.CycleStartDay,
		MonthlyQuotaBytes:      item.MonthlyQuotaBytes,
		BlockWhenExceeded:      item.BlockWhenExceeded,
		HourlyRetentionDays:    item.HourlyRetentionDays,
		DailyRetentionMonths:   item.DailyRetentionMonths,
		MonthlyRetentionMonths: item.MonthlyRetentionMonths,
		UpdatedAt:              item.UpdatedAt,
		CreatedAt:              item.CreatedAt,
	}
}

func backupTrafficBaselineFromRow(row storage.AgentTrafficBaselineRow) BackupTrafficBaseline {
	return BackupTrafficBaseline{
		AgentID:           row.AgentID,
		CycleStart:        row.CycleStart,
		RawRXBytes:        row.RawRXBytes,
		RawTXBytes:        row.RawTXBytes,
		RawAccountedBytes: row.RawAccountedBytes,
		AdjustUsedBytes:   row.AdjustUsedBytes,
		UpdatedAt:         row.UpdatedAt,
		CreatedAt:         row.CreatedAt,
	}
}

func trafficBaselineRowFromBackup(item BackupTrafficBaseline) storage.AgentTrafficBaselineRow {
	return storage.AgentTrafficBaselineRow{
		AgentID:           item.AgentID,
		CycleStart:        item.CycleStart,
		RawRXBytes:        item.RawRXBytes,
		RawTXBytes:        item.RawTXBytes,
		RawAccountedBytes: item.RawAccountedBytes,
		AdjustUsedBytes:   item.AdjustUsedBytes,
		UpdatedAt:         item.UpdatedAt,
		CreatedAt:         item.CreatedAt,
	}
}

func encodeBackupBundle(bundle BackupBundle) ([]byte, error) {
	var buffer bytes.Buffer
	gz := gzip.NewWriter(&buffer)
	tw := tar.NewWriter(gz)
	if err := writeBackupJSONFile(tw, backupManifestFile, bundle.Manifest); err != nil {
		return nil, err
	}
	if err := writeBackupJSONFile(tw, backupAgentsFile, bundle.Agents); err != nil {
		return nil, err
	}
	if err := writeBackupJSONFile(tw, backupHTTPRulesFile, bundle.HTTPRules); err != nil {
		return nil, err
	}
	if err := writeBackupJSONFile(tw, backupL4RulesFile, bundle.L4Rules); err != nil {
		return nil, err
	}
	if err := writeBackupJSONFile(tw, backupWireGuardProfilesFile, bundle.WireGuardProfiles); err != nil {
		return nil, err
	}
	if err := writeBackupJSONFile(tw, backupWireGuardClientsFile, bundle.WireGuardClients); err != nil {
		return nil, err
	}
	if err := writeBackupJSONFile(tw, backupRelayListenersFile, bundle.RelayListeners); err != nil {
		return nil, err
	}
	if err := writeBackupJSONFile(tw, backupCertificatesFile, bundle.Certificates); err != nil {
		return nil, err
	}
	if err := writeBackupJSONFile(tw, backupVersionPoliciesFile, bundle.VersionPolicies); err != nil {
		return nil, err
	}
	if err := writeBackupJSONFile(tw, backupTrafficPoliciesFile, bundle.TrafficPolicies); err != nil {
		return nil, err
	}
	if err := writeBackupJSONFile(tw, backupTrafficBaselinesFile, bundle.TrafficBaselines); err != nil {
		return nil, err
	}
	for _, material := range bundle.Materials {
		if strings.TrimSpace(material.CertPEM) != "" {
			if err := writeBackupFile(tw, backupMaterialPath(material.Domain, "cert.pem"), []byte(material.CertPEM)); err != nil {
				return nil, err
			}
		}
		if strings.TrimSpace(material.KeyPEM) != "" {
			if err := writeBackupFile(tw, backupMaterialPath(material.Domain, "key.pem"), []byte(material.KeyPEM)); err != nil {
				return nil, err
			}
		}
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

func decodeBackupBundle(archive []byte) (BackupBundle, error) {
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return BackupBundle{}, fmt.Errorf("%w: invalid backup archive", ErrInvalidArgument)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	var bundle BackupBundle
	materialMap := map[string]BackupCertificateFile{}
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return BackupBundle{}, fmt.Errorf("%w: invalid tar stream", ErrInvalidArgument)
		}
		name := path.Clean(strings.TrimPrefix(header.Name, "./"))
		content, err := io.ReadAll(tr)
		if err != nil {
			return BackupBundle{}, err
		}
		switch name {
		case backupManifestFile:
			if err := json.Unmarshal(content, &bundle.Manifest); err != nil {
				return BackupBundle{}, fmt.Errorf("%w: invalid manifest.json", ErrInvalidArgument)
			}
		case backupAgentsFile:
			if err := json.Unmarshal(content, &bundle.Agents); err != nil {
				return BackupBundle{}, fmt.Errorf("%w: invalid agents.json", ErrInvalidArgument)
			}
		case backupHTTPRulesFile:
			if err := json.Unmarshal(content, &bundle.HTTPRules); err != nil {
				return BackupBundle{}, fmt.Errorf("%w: invalid http_rules.json", ErrInvalidArgument)
			}
		case backupL4RulesFile:
			if err := json.Unmarshal(content, &bundle.L4Rules); err != nil {
				return BackupBundle{}, fmt.Errorf("%w: invalid l4_rules.json", ErrInvalidArgument)
			}
		case backupWireGuardProfilesFile:
			if err := json.Unmarshal(content, &bundle.WireGuardProfiles); err != nil {
				return BackupBundle{}, fmt.Errorf("%w: invalid wireguard_profiles.json", ErrInvalidArgument)
			}
		case backupWireGuardClientsFile:
			if err := json.Unmarshal(content, &bundle.WireGuardClients); err != nil {
				return BackupBundle{}, fmt.Errorf("%w: invalid wireguard_clients.json", ErrInvalidArgument)
			}
		case backupRelayListenersFile:
			if err := json.Unmarshal(content, &bundle.RelayListeners); err != nil {
				return BackupBundle{}, fmt.Errorf("%w: invalid relay_listeners.json", ErrInvalidArgument)
			}
		case backupCertificatesFile:
			if err := json.Unmarshal(content, &bundle.Certificates); err != nil {
				return BackupBundle{}, fmt.Errorf("%w: invalid certificates.json", ErrInvalidArgument)
			}
		case backupVersionPoliciesFile:
			if err := json.Unmarshal(content, &bundle.VersionPolicies); err != nil {
				return BackupBundle{}, fmt.Errorf("%w: invalid version_policies.json", ErrInvalidArgument)
			}
		case backupTrafficPoliciesFile:
			if err := json.Unmarshal(content, &bundle.TrafficPolicies); err != nil {
				return BackupBundle{}, fmt.Errorf("%w: invalid traffic_policies.json", ErrInvalidArgument)
			}
		case backupTrafficBaselinesFile:
			if err := json.Unmarshal(content, &bundle.TrafficBaselines); err != nil {
				return BackupBundle{}, fmt.Errorf("%w: invalid traffic_baselines.json", ErrInvalidArgument)
			}
		default:
			if !strings.HasPrefix(name, backupMaterialPrefix+"/") {
				continue
			}
			domain, fileName, ok := parseBackupMaterialPath(name)
			if !ok {
				continue
			}
			item := materialMap[domain]
			item.Domain = domain
			switch fileName {
			case "cert.pem":
				item.CertPEM = string(content)
			case "key.pem":
				item.KeyPEM = string(content)
			}
			materialMap[domain] = item
		}
	}
	if bundle.Manifest.PackageVersion == 0 {
		return BackupBundle{}, fmt.Errorf("%w: manifest.json is required", ErrInvalidArgument)
	}
	domains := make([]string, 0, len(materialMap))
	for domain := range materialMap {
		domains = append(domains, domain)
	}
	slices.Sort(domains)
	for _, domain := range domains {
		bundle.Materials = append(bundle.Materials, materialMap[domain])
	}
	return bundle, nil
}

func writeBackupJSONFile(tw *tar.Writer, name string, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return writeBackupFile(tw, name, data)
}

func writeBackupFile(tw *tar.Writer, name string, content []byte) error {
	header := &tar.Header{
		Name:    name,
		Mode:    0o644,
		Size:    int64(len(content)),
		ModTime: time.Now().UTC(),
	}
	if err := tw.WriteHeader(header); err != nil {
		return err
	}
	_, err := tw.Write(content)
	return err
}

func backupMaterialPath(domain string, fileName string) string {
	return path.Join(backupMaterialPrefix, pathEscapeDomain(domain), fileName)
}

func parseBackupMaterialPath(name string) (string, string, bool) {
	parts := strings.Split(path.Clean(name), "/")
	if len(parts) != 3 || parts[0] != backupMaterialPrefix {
		return "", "", false
	}
	domain := pathUnescapeDomain(parts[1])
	if domain == "" {
		return "", "", false
	}
	return domain, parts[2], true
}

func pathEscapeDomain(domain string) string {
	return url.QueryEscape(strings.TrimSpace(domain))
}

func pathUnescapeDomain(value string) string {
	decoded, err := url.QueryUnescape(strings.TrimSpace(value))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(decoded)
}

func relayConflictKey(agentID string, name string) string {
	return strings.TrimSpace(agentID) + "|" + strings.TrimSpace(name)
}

func httpRuleConflictKey(agentID string, frontendURL string) string {
	return strings.TrimSpace(agentID) + "|" + strings.TrimSpace(frontendURL)
}

func l4ConflictKey(agentID string, protocol string, listenHost string, listenPort int, listenStack string) string {
	return strings.TrimSpace(agentID) + "|" + strings.ToLower(strings.TrimSpace(protocol)) + "|" + strings.TrimSpace(listenHost) + "|" + fmt.Sprintf("%d", listenPort) + "|" + strings.TrimSpace(listenStack)
}

func l4BackupConflictKey(agentID string, protocol string, listenHost string, listenPort int, listenMode string, wireGuardListenHost string, wireGuardProfileID *int) string {
	effectiveHost := strings.TrimSpace(listenHost)
	listenStack := "host"
	if strings.EqualFold(strings.TrimSpace(listenMode), "wireguard") {
		listenStack = "wireguard"
		if wireGuardProfileID != nil && *wireGuardProfileID > 0 {
			listenStack = fmt.Sprintf("wireguard:%d", *wireGuardProfileID)
		}
		if host := strings.TrimSpace(wireGuardListenHost); host != "" {
			effectiveHost = host
		}
	}
	return l4ConflictKey(agentID, protocol, effectiveHost, listenPort, listenStack)
}

func trafficBaselineKey(agentID string, cycleStart string) string {
	return strings.TrimSpace(agentID) + "|" + strings.TrimSpace(cycleStart)
}

func resolveAgentID(agentID string, agentIDMap map[string]string, cfg config.Config) (string, bool) {
	trimmed := strings.TrimSpace(agentID)
	if trimmed == "" {
		if cfg.EnableLocalAgent && strings.TrimSpace(cfg.LocalAgentID) != "" {
			return cfg.LocalAgentID, true
		}
		return "", false
	}
	if mapped, ok := agentIDMap[trimmed]; ok && strings.TrimSpace(mapped) != "" {
		return mapped, true
	}
	if cfg.EnableLocalAgent && trimmed == cfg.LocalAgentID {
		return trimmed, true
	}
	return "", false
}

func remapAgentIDs(values []string, agentIDMap map[string]string) ([]string, bool) {
	mapped := make([]string, 0, len(values))
	for _, value := range values {
		resolved, ok := agentIDMap[strings.TrimSpace(value)]
		if !ok || strings.TrimSpace(resolved) == "" {
			return nil, false
		}
		mapped = append(mapped, resolved)
	}
	return normalizeTags(mapped), true
}

func certificateRequiresMaterial(cert ManagedCertificate) bool {
	if cert.CertificateType == "uploaded" || cert.CertificateType == "internal_ca" {
		return true
	}
	if strings.TrimSpace(cert.LastIssueAt) != "" || strings.TrimSpace(cert.MaterialHash) != "" {
		return true
	}
	return cert.Status == "active"
}

func backupHTTPRuleFromRule(rule HTTPRule) BackupHTTPRule {
	return BackupHTTPRule{
		ID:                       rule.ID,
		AgentID:                  rule.AgentID,
		FrontendURL:              rule.FrontendURL,
		BackendURL:               rule.BackendURL,
		Backends:                 append([]HTTPRuleBackend(nil), rule.Backends...),
		LoadBalancing:            rule.LoadBalancing,
		Enabled:                  rule.Enabled,
		Tags:                     append([]string(nil), rule.Tags...),
		ProxyRedirect:            rule.ProxyRedirect,
		RelayChain:               append([]int(nil), rule.RelayChain...),
		RelayLayers:              cloneIntLayers(rule.RelayLayers),
		RelayObfs:                rule.RelayObfs,
		PassProxyHeaders:         rule.PassProxyHeaders,
		UserAgent:                rule.UserAgent,
		CustomHeaders:            append([]HTTPCustomHeader(nil), rule.CustomHeaders...),
		WireGuardEntryEnabled:    rule.WireGuardEntryEnabled,
		WireGuardProfileID:       copyOptionalInt(rule.WireGuardProfileID),
		WireGuardEntryListenHost: rule.WireGuardEntryListenHost,
		WireGuardEntryListenPort: rule.WireGuardEntryListenPort,
		Revision:                 rule.Revision,
	}
}

func backupL4RuleFromRule(rule L4Rule) BackupL4Rule {
	return BackupL4Rule{
		ID:                   rule.ID,
		AgentID:              rule.AgentID,
		Name:                 rule.Name,
		Protocol:             rule.Protocol,
		ListenHost:           rule.ListenHost,
		ListenPort:           rule.ListenPort,
		UpstreamHost:         rule.UpstreamHost,
		UpstreamPort:         rule.UpstreamPort,
		Backends:             append([]L4Backend(nil), rule.Backends...),
		LoadBalancing:        rule.LoadBalancing,
		Tuning:               rule.Tuning,
		RelayChain:           append([]int(nil), rule.RelayChain...),
		RelayLayers:          cloneIntLayers(rule.RelayLayers),
		RelayObfs:            rule.RelayObfs,
		ListenMode:           rule.ListenMode,
		WireGuardProfileID:   copyOptionalInt(rule.WireGuardProfileID),
		WireGuardInboundMode: rule.WireGuardInboundMode,
		WireGuardListenHost:  rule.WireGuardListenHost,
		ProxyEntryAuth:       rule.ProxyEntryAuth,
		ProxyEgressMode:      rule.ProxyEgressMode,
		ProxyEgressURL:       rule.ProxyEgressURL,
		WireGuardEgressURI:   rule.WireGuardEgressURI,
		Enabled:              rule.Enabled,
		Tags:                 append([]string(nil), rule.Tags...),
		Revision:             rule.Revision,
	}
}

func backupWireGuardProfileFromRow(row storage.WireGuardProfileRow) BackupWireGuardProfile {
	return BackupWireGuardProfile{
		ID:             row.ID,
		AgentID:        row.AgentID,
		Name:           row.Name,
		Mode:           row.Mode,
		PrivateKey:     row.PrivateKey,
		ListenPort:     row.ListenPort,
		PublicEndpoint: row.PublicEndpoint,
		Addresses:      parseStringArray(row.AddressesJSON),
		Peers:          parseWireGuardPeers(row.PeersJSON),
		DNS:            parseStringArray(row.DNSJSON),
		MTU:            row.MTU,
		Enabled:        row.Enabled,
		Tags:           parseStringArray(row.TagsJSON),
		Revision:       row.Revision,
	}
}

func backupWireGuardClientFromRow(row storage.WireGuardClientRow) BackupWireGuardClient {
	return BackupWireGuardClient{
		ID:           row.ID,
		AgentID:      row.AgentID,
		ProfileID:    row.ProfileID,
		Name:         row.Name,
		PrivateKey:   row.PrivateKey,
		PublicKey:    row.PublicKey,
		PresharedKey: row.PresharedKey,
		Address:      row.Address,
		AllowedIPs:   parseStringArray(row.AllowedIPsJSON),
		DNS:          parseStringArray(row.DNSJSON),
		Enabled:      row.Enabled,
		CreatedAt:    row.CreatedAt,
		UpdatedAt:    row.UpdatedAt,
	}
}

func wireGuardClientRowFromBackup(item BackupWireGuardClient, agentID string, profileID int) storage.WireGuardClientRow {
	return storage.WireGuardClientRow{
		ID:             item.ID,
		AgentID:        agentID,
		ProfileID:      profileID,
		Name:           strings.TrimSpace(item.Name),
		PrivateKey:     strings.TrimSpace(item.PrivateKey),
		PublicKey:      strings.TrimSpace(item.PublicKey),
		PresharedKey:   strings.TrimSpace(item.PresharedKey),
		Address:        strings.TrimSpace(item.Address),
		AllowedIPsJSON: marshalJSON(normalizeStringList(item.AllowedIPs), "[]"),
		DNSJSON:        marshalJSON(normalizeStringList(item.DNS), "[]"),
		Enabled:        item.Enabled,
		CreatedAt:      strings.TrimSpace(item.CreatedAt),
		UpdatedAt:      strings.TrimSpace(item.UpdatedAt),
	}
}

func httpRuleInputFromBackup(rule BackupHTTPRule, listenerIDMap map[int]int, wireGuardProfileID *int) HTTPRuleInput {
	backends := backupHTTPBackends(rule.Backends, rule.BackendURL)
	relayLayers := backupRelayLayers(rule.RelayChain, rule.RelayLayers, listenerIDMap)
	return HTTPRuleInput{
		FrontendURL:              backupStringPtr(rule.FrontendURL),
		Backends:                 &backends,
		LoadBalancing:            &rule.LoadBalancing,
		Enabled:                  backupBoolPtr(rule.Enabled),
		Tags:                     &rule.Tags,
		ProxyRedirect:            backupBoolPtr(rule.ProxyRedirect),
		RelayLayers:              relayLayers,
		RelayObfs:                backupBoolPtr(rule.RelayObfs),
		PassProxyHeaders:         backupBoolPtr(rule.PassProxyHeaders),
		UserAgent:                backupStringPtr(rule.UserAgent),
		CustomHeaders:            &rule.CustomHeaders,
		WireGuardEntryEnabled:    backupBoolPtr(rule.WireGuardEntryEnabled),
		WireGuardProfileID:       copyOptionalInt(wireGuardProfileID),
		WireGuardEntryListenHost: backupStringPtr(rule.WireGuardEntryListenHost),
		WireGuardEntryListenPort: backupIntPtr(rule.WireGuardEntryListenPort),
	}
}

func l4RuleInputFromBackup(rule BackupL4Rule, listenerIDMap map[int]int, wireGuardProfileID *int) L4RuleInput {
	backends := backupL4Backends(rule.Backends, rule.UpstreamHost, rule.UpstreamPort)
	relayLayers := backupRelayLayers(rule.RelayChain, rule.RelayLayers, listenerIDMap)
	return L4RuleInput{
		Name:                 backupStringPtr(rule.Name),
		Protocol:             backupStringPtr(rule.Protocol),
		ListenHost:           backupStringPtr(rule.ListenHost),
		ListenPort:           backupIntPtr(rule.ListenPort),
		Backends:             &backends,
		LoadBalancing:        &rule.LoadBalancing,
		Tuning:               &rule.Tuning,
		RelayLayers:          relayLayers,
		RelayObfs:            backupBoolPtr(rule.RelayObfs),
		ListenMode:           backupStringPtr(rule.ListenMode),
		WireGuardProfileID:   copyOptionalInt(wireGuardProfileID),
		WireGuardInboundMode: backupStringPtr(rule.WireGuardInboundMode),
		WireGuardListenHost:  backupStringPtr(rule.WireGuardListenHost),
		ProxyEntryAuth: &L4ProxyEntryAuth{
			Enabled:  rule.ProxyEntryAuth.Enabled,
			Username: rule.ProxyEntryAuth.Username,
			Password: rule.ProxyEntryAuth.Password,
		},
		ProxyEgressMode:    backupStringPtr(rule.ProxyEgressMode),
		ProxyEgressURL:     backupStringPtr(rule.ProxyEgressURL),
		WireGuardEgressURI: backupStringPtr(rule.WireGuardEgressURI),
		Enabled:            backupBoolPtr(rule.Enabled),
		Tags:               &rule.Tags,
	}
}

func wireGuardProfileKey(agentID string, profileID int) string {
	return strings.TrimSpace(agentID) + "|" + fmt.Sprintf("%d", profileID)
}

func wireGuardProfileConflictKey(agentID string, name string) string {
	return strings.TrimSpace(agentID) + "|" + strings.TrimSpace(name)
}

func wireGuardClientBackupKey(agentID string, profileID int, name string, id int) string {
	trimmedName := strings.TrimSpace(name)
	if trimmedName == "" {
		trimmedName = fmt.Sprintf("#%d", id)
	}
	return wireGuardProfileKey(agentID, profileID) + "|" + trimmedName
}

func remapBackupWireGuardProfileID(agentID string, profileID *int, profileIDMap map[string]int, enabledProfileIDs map[string]struct{}) (*int, bool) {
	if profileID == nil || *profileID <= 0 {
		return nil, true
	}
	profileKey := wireGuardProfileKey(agentID, *profileID)
	mapped, ok := profileIDMap[profileKey]
	if !ok || mapped <= 0 {
		return nil, false
	}
	if _, ok := enabledProfileIDs[profileKey]; !ok {
		return nil, false
	}
	return backupIntPtr(mapped), true
}

func remapBackupWireGuardClientProfileID(agentID string, resolvedAgentID string, profileID int, profileIDMap map[string]int, acceptedProfileIDs map[string]struct{}) (int, bool) {
	if profileID <= 0 {
		return 0, false
	}
	for _, profileKey := range []string{
		wireGuardProfileKey(agentID, profileID),
		wireGuardProfileKey(resolvedAgentID, profileID),
	} {
		if _, ok := acceptedProfileIDs[profileKey]; !ok {
			continue
		}
		mapped, ok := profileIDMap[profileKey]
		if ok && mapped > 0 {
			return mapped, true
		}
	}
	return 0, false
}

func validateBackupWireGuardClientRow(row storage.WireGuardClientRow) error {
	if row.ID <= 0 {
		return fmt.Errorf("%w: wireguard client id is required", ErrInvalidArgument)
	}
	if strings.TrimSpace(row.Name) == "" {
		return fmt.Errorf("%w: wireguard client name is required", ErrInvalidArgument)
	}
	if strings.TrimSpace(row.PrivateKey) == "" || strings.TrimSpace(row.PublicKey) == "" {
		return fmt.Errorf("%w: wireguard client private_key and public_key are required", ErrInvalidArgument)
	}
	if err := validateWireGuardKey(row.PrivateKey, true); err != nil {
		return fmt.Errorf("%w: wireguard client private_key must be a WireGuard key", ErrInvalidArgument)
	}
	if err := validateWireGuardKey(row.PublicKey, true); err != nil {
		return fmt.Errorf("%w: wireguard client public_key must be a WireGuard key", ErrInvalidArgument)
	}
	if err := validateWireGuardKey(row.PresharedKey, false); err != nil {
		return fmt.Errorf("%w: wireguard client preshared_key must be a WireGuard key", ErrInvalidArgument)
	}
	if strings.TrimSpace(row.Address) == "" {
		return fmt.Errorf("%w: wireguard client address is required", ErrInvalidArgument)
	}
	if err := validateWireGuardPrefixes([]string{row.Address}, "address"); err != nil {
		return err
	}
	if err := validateWireGuardPrefixes(parseStringArray(row.AllowedIPsJSON), "allowed_ips"); err != nil {
		return err
	}
	if err := validateWireGuardDNSAddrs(parseStringArray(row.DNSJSON)); err != nil {
		return err
	}
	return nil
}

func previewWireGuardProfiles(ctx context.Context, incoming []BackupWireGuardProfile, agentIDMap map[string]string, cfg config.Config, store backupStore, result *BackupImportResult) (map[string]int, map[string]struct{}, map[string]struct{}, map[string]struct{}, error) {
	knownAgentIDs, err := allKnownAgentIDs(ctx, cfg, store)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	existingRows, err := listAllWireGuardProfileRows(ctx, store, knownAgentIDs)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	allocator, err := newConfigIdentityAllocatorFromStore(ctx, cfg, store)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	profileIDMap, enabledProfileIDs, importedProfileIDs, skippedConflictProfileIDs, _, _, err := planWireGuardProfilesWithRows(incoming, existingRows, agentIDMap, cfg, allocator, result)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	return profileIDMap, enabledProfileIDs, importedProfileIDs, skippedConflictProfileIDs, nil
}

func (s *backupService) importWireGuardProfiles(ctx context.Context, incoming []BackupWireGuardProfile, agentIDMap map[string]string, result *BackupImportResult, modifiedAgents modifiedAgentRevisions, allocator *configIdentityAllocator) (map[string]int, map[string]struct{}, map[string]struct{}, map[string]struct{}, error) {
	knownAgentIDs, err := allKnownAgentIDs(ctx, s.cfg, s.store)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	existingRows, err := listAllWireGuardProfileRows(ctx, s.store, knownAgentIDs)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	profileIDMap, enabledProfileIDs, importedProfileIDs, skippedConflictProfileIDs, groupedRows, revisionsByAgent, err := planWireGuardProfilesWithRows(incoming, existingRows, agentIDMap, s.cfg, allocator, result)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	for agentID, rows := range groupedRows {
		existingAgentRows, err := s.store.ListWireGuardProfiles(ctx, agentID)
		if err != nil {
			return nil, nil, nil, nil, err
		}
		if wireGuardProfileRowsEqual(existingAgentRows, rows) {
			continue
		}
		if err := s.store.SaveWireGuardProfiles(ctx, agentID, rows); err != nil {
			return nil, nil, nil, nil, err
		}
	}
	for agentID, revision := range revisionsByAgent {
		recordModifiedAgentRevision(modifiedAgents, agentID, revision)
	}
	return profileIDMap, enabledProfileIDs, importedProfileIDs, skippedConflictProfileIDs, nil
}

func planWireGuardProfilesWithRows(incoming []BackupWireGuardProfile, existing []storage.WireGuardProfileRow, agentIDMap map[string]string, cfg config.Config, allocator *configIdentityAllocator, result *BackupImportResult) (map[string]int, map[string]struct{}, map[string]struct{}, map[string]struct{}, map[string][]storage.WireGuardProfileRow, map[string]int, error) {
	profileIDMap := map[string]int{}
	enabledProfileIDs := map[string]struct{}{}
	importedProfileIDs := map[string]struct{}{}
	skippedConflictProfileIDs := map[string]struct{}{}
	grouped := map[string][]storage.WireGuardProfileRow{}
	maxRevisionByAgent := map[string]int{}
	revisionsByAgent := map[string]int{}
	conflictIndex := map[string]storage.WireGuardProfileRow{}

	for _, row := range existing {
		grouped[row.AgentID] = append(grouped[row.AgentID], row)
		conflictIndex[wireGuardProfileConflictKey(row.AgentID, row.Name)] = row
		if row.ID > 0 {
			profileIDMap[wireGuardProfileKey(row.AgentID, row.ID)] = row.ID
			if row.Enabled {
				enabledProfileIDs[wireGuardProfileKey(row.AgentID, row.ID)] = struct{}{}
			}
		}
		if row.Revision > maxRevisionByAgent[row.AgentID] {
			maxRevisionByAgent[row.AgentID] = row.Revision
		}
	}

	for _, item := range incoming {
		resolvedAgentID, ok := resolveAgentID(item.AgentID, agentIDMap, cfg)
		key := wireGuardProfileConflictKey(item.AgentID, item.Name)
		if !ok {
			if result != nil {
				result.addSkippedInvalid("wireguard_profile", key, "wireguard profile references unknown agent")
			}
			continue
		}
		conflictKey := wireGuardProfileConflictKey(resolvedAgentID, item.Name)
		if existingRow, ok := conflictIndex[conflictKey]; ok {
			profileIDMap[wireGuardProfileKey(item.AgentID, item.ID)] = existingRow.ID
			profileIDMap[wireGuardProfileKey(resolvedAgentID, item.ID)] = existingRow.ID
			if existingRow.Enabled {
				enabledProfileIDs[wireGuardProfileKey(item.AgentID, item.ID)] = struct{}{}
				enabledProfileIDs[wireGuardProfileKey(resolvedAgentID, item.ID)] = struct{}{}
			}
			skippedConflictProfileIDs[wireGuardProfileKey(item.AgentID, item.ID)] = struct{}{}
			skippedConflictProfileIDs[wireGuardProfileKey(resolvedAgentID, item.ID)] = struct{}{}
			if result != nil {
				result.addSkippedConflict("wireguard_profile", conflictKey, "wireguard profile already exists")
			}
			continue
		}
		assignedID := allocator.AllocateRuleID(item.ID)
		input := WireGuardProfileInput{
			Name:           item.Name,
			Mode:           item.Mode,
			PrivateKey:     item.PrivateKey,
			ListenPort:     item.ListenPort,
			PublicEndpoint: item.PublicEndpoint,
			Addresses:      append([]string(nil), item.Addresses...),
			Peers:          append([]WireGuardPeer(nil), item.Peers...),
			DNS:            append([]string(nil), item.DNS...),
			MTU:            item.MTU,
			Enabled:        backupBoolPtr(item.Enabled),
			Tags:           append([]string(nil), item.Tags...),
		}
		normalized, err := normalizeWireGuardProfileInput(input, WireGuardProfile{}, assignedID)
		if err != nil {
			if result != nil {
				result.addSkippedInvalid("wireguard_profile", conflictKey, err.Error())
			}
			continue
		}
		if err := validateRequiredWireGuardProfileEssentials(normalized); err != nil {
			if result != nil {
				result.addSkippedInvalid("wireguard_profile", conflictKey, err.Error())
			}
			continue
		}
		normalized.AgentID = resolvedAgentID
		normalized.ID = assignedID
		candidateRow := wireGuardProfileToRow(normalized)
		candidateRows := append(append([]storage.WireGuardProfileRow(nil), grouped[resolvedAgentID]...), candidateRow)
		if err := validateUniqueEnabledWireGuardListenPorts(candidateRows); err != nil {
			if result != nil {
				result.addSkippedConflict("wireguard_profile", conflictKey, err.Error())
			}
			skippedConflictProfileIDs[wireGuardProfileKey(item.AgentID, item.ID)] = struct{}{}
			skippedConflictProfileIDs[wireGuardProfileKey(resolvedAgentID, item.ID)] = struct{}{}
			continue
		}
		profileIDMap[wireGuardProfileKey(item.AgentID, item.ID)] = assignedID
		profileIDMap[wireGuardProfileKey(resolvedAgentID, item.ID)] = assignedID
		importedProfileIDs[wireGuardProfileKey(item.AgentID, item.ID)] = struct{}{}
		importedProfileIDs[wireGuardProfileKey(resolvedAgentID, item.ID)] = struct{}{}
		if normalized.Enabled {
			enabledProfileIDs[wireGuardProfileKey(item.AgentID, item.ID)] = struct{}{}
			enabledProfileIDs[wireGuardProfileKey(resolvedAgentID, item.ID)] = struct{}{}
		}
		normalized.Revision = allocator.AllocateRevisionForAgent(resolvedAgentID, maxRevisionByAgent[resolvedAgentID])
		if normalized.Revision > maxRevisionByAgent[resolvedAgentID] {
			maxRevisionByAgent[resolvedAgentID] = normalized.Revision
		}
		if normalized.Revision > revisionsByAgent[resolvedAgentID] {
			revisionsByAgent[resolvedAgentID] = normalized.Revision
		}
		candidateRow = wireGuardProfileToRow(normalized)
		grouped[resolvedAgentID] = append(grouped[resolvedAgentID], candidateRow)
		conflictIndex[conflictKey] = candidateRow
		if result != nil {
			result.addImported("wireguard_profile", conflictKey)
		}
	}

	return profileIDMap, enabledProfileIDs, importedProfileIDs, skippedConflictProfileIDs, grouped, revisionsByAgent, nil
}

func backupHTTPBackends(backends []HTTPRuleBackend, backendURL string) []HTTPRuleBackend {
	canonical := append([]HTTPRuleBackend(nil), backends...)
	if len(canonical) == 0 && strings.TrimSpace(backendURL) != "" {
		canonical = []HTTPRuleBackend{{URL: backendURL}}
	}
	return canonical
}

func backupL4Backends(backends []L4Backend, upstreamHost string, upstreamPort int) []L4Backend {
	canonical := append([]L4Backend(nil), backends...)
	if len(canonical) == 0 && strings.TrimSpace(upstreamHost) != "" && upstreamPort > 0 {
		canonical = []L4Backend{{Host: upstreamHost, Port: upstreamPort}}
	}
	return canonical
}

func backupRelayLayers(relayChain []int, relayLayers [][]int, listenerIDMap map[int]int) *[][]int {
	if len(relayLayers) == 0 && len(relayChain) > 0 {
		return remapRelayChainAsLayers(relayChain, listenerIDMap)
	}
	return remapIntLayers(relayLayers, listenerIDMap)
}

func remapRelayChainAsLayers(values []int, mapping map[int]int) *[][]int {
	if values == nil {
		return nil
	}
	mapped := make([][]int, 0, len(values))
	for _, value := range values {
		next, ok := mapping[value]
		if !ok || next <= 0 {
			empty := [][]int{}
			return &empty
		}
		mapped = append(mapped, []int{next})
	}
	return &mapped
}

func relayListenerInputFromBackup(listener BackupRelayListener, certIDMap map[int]int, wireGuardProfileID *int) RelayListenerInput {
	var certificateID *int
	if listener.CertificateID != nil {
		if mapped, ok := certIDMap[*listener.CertificateID]; ok && mapped > 0 {
			certificateID = backupIntPtr(mapped)
		}
	}
	trustedIDs := remapIntSlice(listener.TrustedCACertificateIDs, certIDMap)
	return RelayListenerInput{
		Name:                       backupStringPtr(listener.Name),
		ListenHost:                 backupStringPtr(listener.ListenHost),
		BindHosts:                  &listener.BindHosts,
		ListenPort:                 backupIntPtr(listener.ListenPort),
		PublicHost:                 backupStringPtr(listener.PublicHost),
		PublicPort:                 backupIntPtr(listener.PublicPort),
		Enabled:                    backupBoolPtr(listener.Enabled),
		CertificateID:              certificateID,
		TLSMode:                    backupStringPtr(listener.TLSMode),
		TransportMode:              backupStringPtr(listener.TransportMode),
		WireGuardProfileID:         copyOptionalInt(wireGuardProfileID),
		AllowTransportFallback:     backupBoolPtr(listener.AllowTransportFallback),
		ObfsMode:                   backupStringPtr(listener.ObfsMode),
		PinSet:                     &listener.PinSet,
		TrustedCACertificateIDs:    trustedIDs,
		AllowSelfSigned:            backupBoolPtr(listener.AllowSelfSigned),
		Tags:                       &listener.Tags,
		HasCertificateID:           true,
		HasTLSMode:                 true,
		HasTrustedCACertificateIDs: trustedIDs != nil,
		HasAllowSelfSigned:         true,
		HasPinSet:                  true,
	}
}

func remapIntSlice(values []int, mapping map[int]int) *[]int {
	if values == nil {
		return nil
	}
	mapped := make([]int, 0, len(values))
	for _, value := range values {
		next, ok := mapping[value]
		if !ok || next <= 0 {
			return &[]int{}
		}
		mapped = append(mapped, next)
	}
	return &mapped
}

func remapIntLayers(values [][]int, mapping map[int]int) *[][]int {
	if values == nil {
		return nil
	}
	mapped := make([][]int, 0, len(values))
	for _, layer := range values {
		mappedLayer := make([]int, 0, len(layer))
		for _, value := range layer {
			next, ok := mapping[value]
			if !ok || next <= 0 {
				empty := [][]int{}
				return &empty
			}
			mappedLayer = append(mappedLayer, next)
		}
		mapped = append(mapped, mappedLayer)
	}
	return &mapped
}

func remappedIntLayersComplete(original [][]int, mapped *[][]int) bool {
	if len(original) == 0 {
		return true
	}
	if mapped == nil || len(*mapped) != len(original) {
		return false
	}
	for i := range original {
		if len((*mapped)[i]) != len(original[i]) {
			return false
		}
	}
	return true
}

func remappedBackupRelayLayersComplete(relayChain []int, relayLayers [][]int, mapped *[][]int) bool {
	if len(relayLayers) == 0 && len(relayChain) > 0 {
		if mapped == nil || len(*mapped) != len(relayChain) {
			return false
		}
		for _, layer := range *mapped {
			if len(layer) != 1 {
				return false
			}
		}
		return true
	}
	return remappedIntLayersComplete(relayLayers, mapped)
}

func pointerRelayLayers(values *[][]int) [][]int {
	if values == nil {
		return nil
	}
	return *values
}

func pointerIntSlice(values *[]int) []int {
	if values == nil {
		return nil
	}
	return *values
}

func httpRuleRowsEqual(a []storage.HTTPRuleRow, b []storage.HTTPRuleRow) bool {
	return equalSortedRows(a, b, func(x storage.HTTPRuleRow, y storage.HTTPRuleRow) int {
		return compareAgentScopedRows(x.AgentID, x.ID, y.AgentID, y.ID)
	})
}

func l4RuleRowsEqual(a []storage.L4RuleRow, b []storage.L4RuleRow) bool {
	return equalSortedRows(a, b, func(x storage.L4RuleRow, y storage.L4RuleRow) int {
		return compareAgentScopedRows(x.AgentID, x.ID, y.AgentID, y.ID)
	})
}

func relayListenerRowsEqual(a []storage.RelayListenerRow, b []storage.RelayListenerRow) bool {
	return equalSortedRows(a, b, func(x storage.RelayListenerRow, y storage.RelayListenerRow) int {
		return compareAgentScopedRows(x.AgentID, x.ID, y.AgentID, y.ID)
	})
}

func wireGuardProfileRowsEqual(a []storage.WireGuardProfileRow, b []storage.WireGuardProfileRow) bool {
	return equalSortedRows(a, b, func(x storage.WireGuardProfileRow, y storage.WireGuardProfileRow) int {
		return compareAgentScopedRows(x.AgentID, x.ID, y.AgentID, y.ID)
	})
}

func equalSortedRows[T comparable](a []T, b []T, compare func(T, T) int) bool {
	left := append([]T(nil), a...)
	right := append([]T(nil), b...)
	slices.SortFunc(left, compare)
	slices.SortFunc(right, compare)
	return slices.Equal(left, right)
}

func compareAgentScopedRows(agentIDA string, idA int, agentIDB string, idB int) int {
	if agentIDA != agentIDB {
		return strings.Compare(agentIDA, agentIDB)
	}
	if idA < idB {
		return -1
	}
	if idA > idB {
		return 1
	}
	return 0
}

func backupAppVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok || info == nil {
		return ""
	}
	version := strings.TrimSpace(info.Main.Version)
	if version == "" || version == "(devel)" {
		return ""
	}
	return version
}

func backupStringPtr(value string) *string {
	return &value
}

func backupBoolPtr(value bool) *bool {
	return &value
}

func backupIntPtr(value int) *int {
	return &value
}

type backupStateSnapshot struct {
	agents                    []storage.AgentRow
	httpRulesByAgentID        map[string][]storage.HTTPRuleRow
	l4RulesByAgentID          map[string][]storage.L4RuleRow
	wireGuardByAgentID        map[string][]storage.WireGuardProfileRow
	wireGuardClientsByAgentID map[string]map[int][]storage.WireGuardClientRow
	relayByAgentID            map[string][]storage.RelayListenerRow
	certificates              []storage.ManagedCertificateRow
	versionPolicies           []storage.VersionPolicyRow
	trafficPolicies           []storage.AgentTrafficPolicyRow
	trafficBaselines          []storage.AgentTrafficBaselineRow
	certificateMaterials      map[string]storage.ManagedCertificateBundle
}

func (s *backupService) captureState(ctx context.Context) (backupStateSnapshot, error) {
	agents, err := s.store.ListAgents(ctx)
	if err != nil {
		return backupStateSnapshot{}, err
	}
	knownAgentIDs, err := allKnownAgentIDs(ctx, s.cfg, s.store)
	if err != nil {
		return backupStateSnapshot{}, err
	}
	httpRulesByAgentID := map[string][]storage.HTTPRuleRow{}
	l4RulesByAgentID := map[string][]storage.L4RuleRow{}
	wireGuardByAgentID := map[string][]storage.WireGuardProfileRow{}
	wireGuardClientsByAgentID := map[string]map[int][]storage.WireGuardClientRow{}
	for _, agentID := range knownAgentIDs {
		rules, err := s.store.ListHTTPRules(ctx, agentID)
		if err != nil {
			return backupStateSnapshot{}, err
		}
		httpRulesByAgentID[agentID] = append([]storage.HTTPRuleRow(nil), rules...)
		l4Rules, err := s.store.ListL4Rules(ctx, agentID)
		if err != nil {
			return backupStateSnapshot{}, err
		}
		l4RulesByAgentID[agentID] = append([]storage.L4RuleRow(nil), l4Rules...)
		wireGuardProfiles, err := s.store.ListWireGuardProfiles(ctx, agentID)
		if err != nil {
			return backupStateSnapshot{}, err
		}
		wireGuardByAgentID[agentID] = append([]storage.WireGuardProfileRow(nil), wireGuardProfiles...)
		wireGuardClientsByAgentID[agentID] = map[int][]storage.WireGuardClientRow{}
		for _, profile := range wireGuardProfiles {
			clients, err := s.store.ListWireGuardClients(ctx, agentID, profile.ID)
			if err != nil {
				return backupStateSnapshot{}, err
			}
			wireGuardClientsByAgentID[agentID][profile.ID] = append([]storage.WireGuardClientRow(nil), clients...)
		}
	}

	relayRows, err := s.store.ListRelayListeners(ctx, "")
	if err != nil {
		return backupStateSnapshot{}, err
	}
	relayByAgentID := map[string][]storage.RelayListenerRow{}
	for _, row := range relayRows {
		relayByAgentID[row.AgentID] = append(relayByAgentID[row.AgentID], row)
	}

	certs, err := s.store.ListManagedCertificates(ctx)
	if err != nil {
		return backupStateSnapshot{}, err
	}
	certificateMaterials := map[string]storage.ManagedCertificateBundle{}
	for _, row := range certs {
		material, ok, err := s.store.LoadManagedCertificateMaterial(ctx, row.Domain)
		if err != nil {
			return backupStateSnapshot{}, err
		}
		if ok {
			certificateMaterials[row.Domain] = material
		}
	}

	policies, err := s.store.ListVersionPolicies(ctx)
	if err != nil {
		return backupStateSnapshot{}, err
	}
	trafficPolicies, err := s.store.ListTrafficPolicies(ctx)
	if err != nil {
		return backupStateSnapshot{}, err
	}
	trafficBaselines, err := s.store.ListTrafficBaselines(ctx)
	if err != nil {
		return backupStateSnapshot{}, err
	}

	return backupStateSnapshot{
		agents:                    append([]storage.AgentRow(nil), agents...),
		httpRulesByAgentID:        httpRulesByAgentID,
		l4RulesByAgentID:          l4RulesByAgentID,
		wireGuardByAgentID:        wireGuardByAgentID,
		wireGuardClientsByAgentID: wireGuardClientsByAgentID,
		relayByAgentID:            relayByAgentID,
		certificates:              append([]storage.ManagedCertificateRow(nil), certs...),
		versionPolicies:           append([]storage.VersionPolicyRow(nil), policies...),
		trafficPolicies:           append([]storage.AgentTrafficPolicyRow(nil), trafficPolicies...),
		trafficBaselines:          append([]storage.AgentTrafficBaselineRow(nil), trafficBaselines...),
		certificateMaterials:      certificateMaterials,
	}, nil
}

func (s *backupService) restoreState(ctx context.Context, snapshot backupStateSnapshot) error {
	currentAgents, err := s.store.ListAgents(ctx)
	if err != nil {
		return err
	}
	currentKnownAgentIDs, err := allKnownAgentIDs(ctx, s.cfg, s.store)
	if err != nil {
		return err
	}
	agentIDs := map[string]struct{}{}
	for _, agentID := range currentKnownAgentIDs {
		agentIDs[agentID] = struct{}{}
	}
	for agentID := range snapshot.httpRulesByAgentID {
		agentIDs[agentID] = struct{}{}
	}
	for agentID := range snapshot.l4RulesByAgentID {
		agentIDs[agentID] = struct{}{}
	}
	for agentID := range snapshot.wireGuardByAgentID {
		agentIDs[agentID] = struct{}{}
	}
	for agentID := range snapshot.wireGuardClientsByAgentID {
		agentIDs[agentID] = struct{}{}
	}
	for agentID := range snapshot.relayByAgentID {
		agentIDs[agentID] = struct{}{}
	}
	for agentID := range agentIDs {
		if err := s.store.SaveHTTPRules(ctx, agentID, snapshot.httpRulesByAgentID[agentID]); err != nil {
			return err
		}
		if err := s.store.SaveL4Rules(ctx, agentID, snapshot.l4RulesByAgentID[agentID]); err != nil {
			return err
		}
		if err := s.store.SaveWireGuardProfiles(ctx, agentID, snapshot.wireGuardByAgentID[agentID]); err != nil {
			return err
		}
		currentClients, err := s.store.ListWireGuardClients(ctx, agentID, 0)
		if err != nil {
			return err
		}
		profileIDs := map[int]struct{}{}
		for _, row := range currentClients {
			if row.ProfileID > 0 {
				profileIDs[row.ProfileID] = struct{}{}
			}
		}
		for profileID := range snapshot.wireGuardClientsByAgentID[agentID] {
			profileIDs[profileID] = struct{}{}
		}
		for profileID := range profileIDs {
			if err := s.store.SaveWireGuardClients(ctx, agentID, profileID, snapshot.wireGuardClientsByAgentID[agentID][profileID]); err != nil {
				return err
			}
		}
		if err := s.store.SaveRelayListeners(ctx, agentID, snapshot.relayByAgentID[agentID]); err != nil {
			return err
		}
	}

	currentCerts, err := s.store.ListManagedCertificates(ctx)
	if err != nil {
		return err
	}
	if err := s.store.SaveManagedCertificates(ctx, snapshot.certificates); err != nil {
		return err
	}
	for domain, material := range snapshot.certificateMaterials {
		if err := s.store.SaveManagedCertificateMaterial(ctx, domain, material); err != nil {
			return err
		}
	}
	if err := s.store.CleanupManagedCertificateMaterial(ctx, currentCerts, snapshot.certificates); err != nil {
		return err
	}

	if err := s.store.SaveVersionPolicies(ctx, snapshot.versionPolicies); err != nil {
		return err
	}
	if err := s.store.ReplaceTrafficPolicies(ctx, snapshot.trafficPolicies); err != nil {
		return err
	}
	if err := s.store.ReplaceTrafficBaselines(ctx, snapshot.trafficBaselines); err != nil {
		return err
	}

	originalAgents := map[string]storage.AgentRow{}
	for _, row := range snapshot.agents {
		originalAgents[row.ID] = row
	}
	for _, row := range currentAgents {
		if _, ok := originalAgents[row.ID]; !ok {
			if err := s.store.DeleteAgent(ctx, row.ID); err != nil {
				return err
			}
		}
	}
	for _, row := range snapshot.agents {
		if err := s.store.SaveAgent(ctx, row); err != nil {
			return err
		}
	}
	return nil
}
