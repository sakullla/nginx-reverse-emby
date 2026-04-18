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
	backupManifestFile        = "manifest.json"
	backupAgentsFile          = "agents.json"
	backupHTTPRulesFile       = "http_rules.json"
	backupL4RulesFile         = "l4_rules.json"
	backupRelayListenersFile  = "relay_listeners.json"
	backupCertificatesFile    = "certificates.json"
	backupVersionPoliciesFile = "version_policies.json"
	backupMaterialPrefix      = "certificate_material"
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
		Agents:          make([]BackupAgent, 0, len(agentRows)),
		HTTPRules:       []BackupHTTPRule{},
		L4Rules:         []BackupL4Rule{},
		RelayListeners:  []BackupRelayListener{},
		Certificates:    []BackupCertificate{},
		VersionPolicies: []BackupVersionPolicy{},
		Materials:       []BackupCertificateFile{},
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
			bundle.HTTPRules = append(bundle.HTTPRules, httpRuleFromRow(row))
		}

		l4Rows, err := s.store.ListL4Rules(ctx, agentID)
		if err != nil {
			return BackupBundle{}, err
		}
		for _, row := range l4Rows {
			bundle.L4Rules = append(bundle.L4Rules, l4RuleFromRow(row))
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

	bundle.Manifest = BackupManifest{
		PackageVersion:       BackupPackageVersion,
		SourceArchitecture:   BackupSourceArchitectureGo,
		SourceAppVersion:     backupAppVersion(),
		SourceLocalAgentID:   s.cfg.LocalAgentID,
		ExportedAt:           s.now().UTC(),
		IncludesCertificates: len(bundle.Materials) > 0,
		Counts: BackupCounts{
			Agents:          len(bundle.Agents),
			HTTPRules:       len(bundle.HTTPRules),
			L4Rules:         len(bundle.L4Rules),
			RelayListeners:  len(bundle.RelayListeners),
			Certificates:    len(bundle.Certificates),
			VersionPolicies: len(bundle.VersionPolicies),
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

	listenerRows, err := s.store.ListRelayListeners(ctx, "")
	if err != nil {
		return BackupImportResult{}, err
	}
	listenerIDMap, err := s.importRelayListeners(ctx, listenerRows, bundle.RelayListeners, agentIDMap, certIDMap, &result, modifiedAgents, allocator)
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

	if err := s.importHTTPRules(ctx, bundle.HTTPRules, agentIDMap, listenerIDMap, &result, modifiedAgents, allocator); err != nil {
		return BackupImportResult{}, err
	}
	if err := s.importL4Rules(ctx, bundle.L4Rules, agentIDMap, listenerIDMap, &result, modifiedAgents, allocator); err != nil {
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

func (s *backupService) importRelayListeners(ctx context.Context, existing []storage.RelayListenerRow, incoming []BackupRelayListener, agentIDMap map[string]string, certIDMap map[int]int, result *BackupImportResult, modifiedAgents modifiedAgentRevisions, allocator *configIdentityAllocator) (map[int]int, error) {
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

		input := relayListenerInputFromBackup(item, certIDMap)
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

func (s *backupService) importHTTPRules(ctx context.Context, incoming []BackupHTTPRule, agentIDMap map[string]string, listenerIDMap map[int]int, result *BackupImportResult, modifiedAgents modifiedAgentRevisions, allocator *configIdentityAllocator) error {
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

		input := httpRuleInputFromBackup(item, listenerIDMap)
		if len(item.RelayChain) > 0 && len(pointerIntSlice(input.RelayChain)) != len(item.RelayChain) {
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

func (s *backupService) importL4Rules(ctx context.Context, incoming []BackupL4Rule, agentIDMap map[string]string, listenerIDMap map[int]int, result *BackupImportResult, modifiedAgents modifiedAgentRevisions, allocator *configIdentityAllocator) error {
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
		key := l4ConflictKey(row.AgentID, row.Protocol, row.ListenHost, row.ListenPort)
		conflictSet[key] = struct{}{}
		grouped[row.AgentID] = append(grouped[row.AgentID], row)
		if row.Revision > maxRevisionByAgent[row.AgentID] {
			maxRevisionByAgent[row.AgentID] = row.Revision
		}
	}

	for _, item := range incoming {
		resolvedAgentID, ok := resolveAgentID(item.AgentID, agentIDMap, s.cfg)
		key := l4ConflictKey(resolvedAgentID, item.Protocol, item.ListenHost, item.ListenPort)
		if !ok {
			result.addSkippedInvalid("l4_rule", key, "l4 rule references unknown agent")
			continue
		}
		if _, exists := conflictSet[key]; exists {
			result.addSkippedConflict("l4_rule", key, "protocol/listen_host/listen_port already exists")
			continue
		}

		input := l4RuleInputFromBackup(item, listenerIDMap)
		if len(item.RelayChain) > 0 && len(pointerIntSlice(input.RelayChain)) != len(item.RelayChain) {
			result.addSkippedInvalid("l4_rule", key, "relay listener reference not available")
			continue
		}

		normalized, err := normalizeL4RuleInput(input, L4Rule{AgentID: resolvedAgentID}, 0)
		if err != nil {
			result.addSkippedInvalid("l4_rule", key, err.Error())
			continue
		}
		if err := l4Svc.validateRelayChain(ctx, normalized.RelayChain); err != nil {
			result.addSkippedInvalid("l4_rule", key, err.Error())
			continue
		}
		normalized.AgentID = resolvedAgentID
		assignedID := allocator.AllocateRuleID(item.ID)
		normalized.ID = assignedID
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
		Tags:                   parseStringArray(row.TagsJSON),
		Capabilities:           parseStringArray(row.CapabilitiesJSON),
		Mode:                   row.Mode,
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
	if err := writeBackupJSONFile(tw, backupRelayListenersFile, bundle.RelayListeners); err != nil {
		return nil, err
	}
	if err := writeBackupJSONFile(tw, backupCertificatesFile, bundle.Certificates); err != nil {
		return nil, err
	}
	if err := writeBackupJSONFile(tw, backupVersionPoliciesFile, bundle.VersionPolicies); err != nil {
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

func l4ConflictKey(agentID string, protocol string, listenHost string, listenPort int) string {
	return strings.TrimSpace(agentID) + "|" + strings.ToLower(strings.TrimSpace(protocol)) + "|" + strings.TrimSpace(listenHost) + "|" + fmt.Sprintf("%d", listenPort)
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

func httpRuleInputFromBackup(rule BackupHTTPRule, listenerIDMap map[int]int) HTTPRuleInput {
	relayChain := remapIntSlice(rule.RelayChain, listenerIDMap)
	return HTTPRuleInput{
		FrontendURL:      backupStringPtr(rule.FrontendURL),
		BackendURL:       backupStringPtr(rule.BackendURL),
		Backends:         &rule.Backends,
		LoadBalancing:    &rule.LoadBalancing,
		Enabled:          backupBoolPtr(rule.Enabled),
		Tags:             &rule.Tags,
		ProxyRedirect:    backupBoolPtr(rule.ProxyRedirect),
		RelayChain:       relayChain,
		RelayObfs:        backupBoolPtr(rule.RelayObfs),
		PassProxyHeaders: backupBoolPtr(rule.PassProxyHeaders),
		UserAgent:        backupStringPtr(rule.UserAgent),
		CustomHeaders:    &rule.CustomHeaders,
	}
}

func l4RuleInputFromBackup(rule BackupL4Rule, listenerIDMap map[int]int) L4RuleInput {
	relayChain := remapIntSlice(rule.RelayChain, listenerIDMap)
	return L4RuleInput{
		Name:          backupStringPtr(rule.Name),
		Protocol:      backupStringPtr(rule.Protocol),
		ListenHost:    backupStringPtr(rule.ListenHost),
		ListenPort:    backupIntPtr(rule.ListenPort),
		UpstreamHost:  backupStringPtr(rule.UpstreamHost),
		UpstreamPort:  backupIntPtr(rule.UpstreamPort),
		Backends:      &rule.Backends,
		LoadBalancing: &rule.LoadBalancing,
		Tuning:        &rule.Tuning,
		RelayChain:    relayChain,
		RelayObfs:     backupBoolPtr(rule.RelayObfs),
		Enabled:       backupBoolPtr(rule.Enabled),
		Tags:          &rule.Tags,
	}
}

func relayListenerInputFromBackup(listener BackupRelayListener, certIDMap map[int]int) RelayListenerInput {
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
	agents               []storage.AgentRow
	httpRulesByAgentID   map[string][]storage.HTTPRuleRow
	l4RulesByAgentID     map[string][]storage.L4RuleRow
	relayByAgentID       map[string][]storage.RelayListenerRow
	certificates         []storage.ManagedCertificateRow
	versionPolicies      []storage.VersionPolicyRow
	certificateMaterials map[string]storage.ManagedCertificateBundle
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

	return backupStateSnapshot{
		agents:               append([]storage.AgentRow(nil), agents...),
		httpRulesByAgentID:   httpRulesByAgentID,
		l4RulesByAgentID:     l4RulesByAgentID,
		relayByAgentID:       relayByAgentID,
		certificates:         append([]storage.ManagedCertificateRow(nil), certs...),
		versionPolicies:      append([]storage.VersionPolicyRow(nil), policies...),
		certificateMaterials: certificateMaterials,
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
