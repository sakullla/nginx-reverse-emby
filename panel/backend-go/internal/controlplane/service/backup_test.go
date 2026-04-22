package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

func TestBackupManifestRoundTripShape(t *testing.T) {
	manifest := BackupManifest{
		PackageVersion:       BackupPackageVersion,
		SourceArchitecture:   BackupSourceArchitectureGo,
		SourceAppVersion:     "v1.2.3",
		ExportedAt:           time.Date(2026, 4, 18, 9, 30, 0, 0, time.UTC),
		IncludesCertificates: true,
		Counts: BackupCounts{
			Agents:          2,
			HTTPRules:       3,
			L4Rules:         4,
			RelayListeners:  5,
			Certificates:    6,
			VersionPolicies: 7,
		},
	}

	rawJSON, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshal backup manifest: %v", err)
	}

	var decoded BackupManifest
	if err := json.Unmarshal(rawJSON, &decoded); err != nil {
		t.Fatalf("unmarshal backup manifest: %v", err)
	}
	if decoded != manifest {
		t.Fatalf("manifest round-trip mismatch: got %+v want %+v", decoded, manifest)
	}

	var payload map[string]any
	if err := json.Unmarshal(rawJSON, &payload); err != nil {
		t.Fatalf("unmarshal json payload: %v", err)
	}
	if got, want := payload["package_version"], float64(BackupPackageVersion); got != want {
		t.Fatalf("manifest.package_version = %#v, want %#v", got, want)
	}
	if got, want := payload["source_architecture"], BackupSourceArchitectureGo; got != want {
		t.Fatalf("manifest.source_architecture = %#v, want %#v", got, want)
	}
	if got, want := payload["source_app_version"], "v1.2.3"; got != want {
		t.Fatalf("manifest.source_app_version = %#v, want %#v", got, want)
	}
	if got, want := payload["exported_at"], "2026-04-18T09:30:00Z"; got != want {
		t.Fatalf("manifest.exported_at = %#v, want %#v", got, want)
	}
	if got, want := payload["includes_certificates"], true; got != want {
		t.Fatalf("manifest.includes_certificates = %#v, want %#v", got, want)
	}
	countsRaw, ok := payload["counts"].(map[string]any)
	if !ok {
		t.Fatalf("manifest.counts missing or wrong type: %#v", payload["counts"])
	}
	if got, want := countsRaw["agents"], float64(2); got != want {
		t.Fatalf("manifest.counts.agents = %#v, want %#v", got, want)
	}
	if got, want := countsRaw["http_rules"], float64(3); got != want {
		t.Fatalf("manifest.counts.http_rules = %#v, want %#v", got, want)
	}
	if got, want := countsRaw["l4_rules"], float64(4); got != want {
		t.Fatalf("manifest.counts.l4_rules = %#v, want %#v", got, want)
	}
	if got, want := countsRaw["relay_listeners"], float64(5); got != want {
		t.Fatalf("manifest.counts.relay_listeners = %#v, want %#v", got, want)
	}
	if got, want := countsRaw["certificates"], float64(6); got != want {
		t.Fatalf("manifest.counts.certificates = %#v, want %#v", got, want)
	}
	if got, want := countsRaw["version_policies"], float64(7); got != want {
		t.Fatalf("manifest.counts.version_policies = %#v, want %#v", got, want)
	}
}

func TestBackupServiceExportImportRoundTripAndConflictReport(t *testing.T) {
	sourceStore, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "source"), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore(source) error = %v", err)
	}
	defer sourceStore.Close()

	targetStore, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "target"), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore(target) error = %v", err)
	}
	defer targetStore.Close()

	ctx := t.Context()
	if err := sourceStore.SaveAgent(ctx, storage.AgentRow{
		ID:               "edge-a",
		Name:             "edge-a",
		AgentToken:       "token-edge-a",
		AgentURL:         "http://edge-a:8080",
		Version:          "1.2.3",
		Platform:         "linux-amd64",
		DesiredVersion:   "1.2.3",
		DesiredRevision:  3,
		TagsJSON:         `["edge","media"]`,
		CapabilitiesJSON: `["http_rules","l4","cert_install"]`,
		Mode:             "pull",
	}); err != nil {
		t.Fatalf("SaveAgent() error = %v", err)
	}
	if err := sourceStore.SaveHTTPRules(ctx, "edge-a", []storage.HTTPRuleRow{{
		ID:                11,
		AgentID:           "edge-a",
		FrontendURL:       "https://media.example.com",
		BackendURL:        "http://127.0.0.1:8096",
		BackendsJSON:      `[{"url":"http://127.0.0.1:8096"}]`,
		LoadBalancingJSON: `{"strategy":"adaptive"}`,
		Enabled:           true,
		TagsJSON:          `["media"]`,
		ProxyRedirect:     true,
		RelayChainJSON:    `[]`,
		PassProxyHeaders:  true,
		CustomHeadersJSON: `[]`,
		Revision:          2,
	}}); err != nil {
		t.Fatalf("SaveHTTPRules() error = %v", err)
	}
	if err := sourceStore.SaveL4Rules(ctx, "edge-a", []storage.L4RuleRow{{
		ID:                12,
		AgentID:           "edge-a",
		Name:              "TCP 25565",
		Protocol:          "tcp",
		ListenHost:        "0.0.0.0",
		ListenPort:        25565,
		UpstreamHost:      "127.0.0.1",
		UpstreamPort:      25565,
		BackendsJSON:      `[{"host":"127.0.0.1","port":25565}]`,
		LoadBalancingJSON: `{"strategy":"adaptive"}`,
		TuningJSON:        `{"proxy_protocol":{"decode":false,"send":false}}`,
		RelayChainJSON:    `[]`,
		Enabled:           true,
		TagsJSON:          `["game"]`,
		Revision:          2,
	}}); err != nil {
		t.Fatalf("SaveL4Rules() error = %v", err)
	}
	if err := sourceStore.SaveManagedCertificates(ctx, []storage.ManagedCertificateRow{{
		ID:              21,
		Domain:          "media.example.com",
		Enabled:         true,
		Scope:           "domain",
		IssuerMode:      "local_http01",
		TargetAgentIDs:  `["edge-a"]`,
		Status:          "active",
		LastIssueAt:     "2026-04-18T12:00:00Z",
		MaterialHash:    "old-hash",
		AgentReports:    `{}`,
		ACMEInfo:        `{}`,
		Usage:           "https",
		CertificateType: "uploaded",
		SelfSigned:      false,
		TagsJSON:        `["media"]`,
		Revision:        2,
	}}); err != nil {
		t.Fatalf("SaveManagedCertificates() error = %v", err)
	}
	if err := sourceStore.SaveManagedCertificateMaterial(ctx, "media.example.com", storage.ManagedCertificateBundle{
		Domain:  "media.example.com",
		CertPEM: "cert-pem",
		KeyPEM:  "key-pem",
	}); err != nil {
		t.Fatalf("SaveManagedCertificateMaterial() error = %v", err)
	}
	if err := sourceStore.SaveRelayListeners(ctx, "edge-a", []storage.RelayListenerRow{{
		ID:                      31,
		AgentID:                 "edge-a",
		Name:                    "relay-edge-a",
		BindHostsJSON:           `["0.0.0.0"]`,
		ListenHost:              "0.0.0.0",
		ListenPort:              7443,
		PublicHost:              "relay.example.com",
		PublicPort:              7443,
		Enabled:                 true,
		CertificateID:           backupIntPtr(21),
		TLSMode:                 "pin_only",
		TransportMode:           "tls_tcp",
		AllowTransportFallback:  true,
		ObfsMode:                "off",
		PinSetJSON:              `[{"type":"spki","value":"pin-edge-a"}]`,
		TrustedCACertificateIDs: `[]`,
		AllowSelfSigned:         false,
		TagsJSON:                `["relay"]`,
		Revision:                2,
	}}); err != nil {
		t.Fatalf("SaveRelayListeners() error = %v", err)
	}
	if err := sourceStore.SaveVersionPolicies(ctx, []storage.VersionPolicyRow{{
		ID:             "stable",
		Channel:        "stable",
		DesiredVersion: "1.2.3",
		PackagesJSON:   `[{"platform":"linux-amd64","url":"https://example.com/nre-agent","sha256":"abc123"}]`,
		TagsJSON:       `["edge"]`,
	}}); err != nil {
		t.Fatalf("SaveVersionPolicies() error = %v", err)
	}

	cfg := config.Config{EnableLocalAgent: true, LocalAgentID: "local", LocalAgentName: "local"}
	sourceSvc := NewBackupService(cfg, sourceStore)
	archive, _, err := sourceSvc.Export(ctx)
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}

	targetSvc := NewBackupService(cfg, targetStore)
	firstImport, err := targetSvc.Import(ctx, archive)
	if err != nil {
		t.Fatalf("Import(first) error = %v", err)
	}
	if firstImport.Summary.Imported.Agents != 1 || firstImport.Summary.Imported.HTTPRules != 1 || firstImport.Summary.Imported.L4Rules != 1 || firstImport.Summary.Imported.RelayListeners != 1 || firstImport.Summary.Imported.Certificates != 1 || firstImport.Summary.Imported.VersionPolicies != 1 {
		t.Fatalf("first import summary = %+v", firstImport.Summary)
	}

	agents, err := targetStore.ListAgents(ctx)
	if err != nil {
		t.Fatalf("ListAgents() error = %v", err)
	}
	if len(agents) != 1 || agents[0].Name != "edge-a" || agents[0].AgentToken != "token-edge-a" {
		t.Fatalf("imported agents = %+v", agents)
	}
	rules, err := targetStore.ListHTTPRules(ctx, "edge-a")
	if err != nil {
		t.Fatalf("ListHTTPRules() error = %v", err)
	}
	if len(rules) != 1 || rules[0].FrontendURL != "https://media.example.com" {
		t.Fatalf("imported http rules = %+v", rules)
	}
	certs, err := targetStore.ListManagedCertificates(ctx)
	if err != nil {
		t.Fatalf("ListManagedCertificates() error = %v", err)
	}
	if len(certs) != 1 || certs[0].Domain != "media.example.com" {
		t.Fatalf("imported certs = %+v", certs)
	}
	material, ok, err := targetStore.LoadManagedCertificateMaterial(ctx, "media.example.com")
	if err != nil {
		t.Fatalf("LoadManagedCertificateMaterial() error = %v", err)
	}
	if !ok || material.CertPEM != "cert-pem" || material.KeyPEM != "key-pem" {
		t.Fatalf("imported material = %+v ok=%v", material, ok)
	}

	secondImport, err := targetSvc.Import(ctx, archive)
	if err != nil {
		t.Fatalf("Import(second) error = %v", err)
	}
	if secondImport.Summary.SkippedConflict.Agents != 1 || secondImport.Summary.SkippedConflict.HTTPRules != 1 || secondImport.Summary.SkippedConflict.L4Rules != 1 || secondImport.Summary.SkippedConflict.RelayListeners != 1 || secondImport.Summary.SkippedConflict.Certificates != 1 || secondImport.Summary.SkippedConflict.VersionPolicies != 1 {
		t.Fatalf("second import conflict summary = %+v", secondImport.Summary)
	}

	legacyBundle, err := decodeBackupBundle(archive)
	if err != nil {
		t.Fatalf("decodeBackupBundle() error = %v", err)
	}
	legacyBundle.Manifest.SourceArchitecture = BackupSourceArchitectureMain
	legacyArchive, err := encodeBackupBundle(legacyBundle)
	if err != nil {
		t.Fatalf("encodeBackupBundle(legacy) error = %v", err)
	}
	legacyStore, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "legacy-target"), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore(legacy-target) error = %v", err)
	}
	defer legacyStore.Close()
	legacySvc := NewBackupService(cfg, legacyStore)
	legacyImport, err := legacySvc.Import(ctx, legacyArchive)
	if err != nil {
		t.Fatalf("Import(legacy) error = %v", err)
	}
	if legacyImport.Summary.Imported.Agents != 1 || legacyImport.Manifest.SourceArchitecture != BackupSourceArchitectureMain {
		t.Fatalf("legacy import result = %+v", legacyImport)
	}
}

type failingBackupStore struct {
	backupStore
	remainingVersionPolicyFailures int
}

func (s *failingBackupStore) SaveVersionPolicies(ctx context.Context, rows []storage.VersionPolicyRow) error {
	if s.remainingVersionPolicyFailures > 0 {
		s.remainingVersionPolicyFailures--
		return errors.New("forced version policy failure")
	}
	return s.backupStore.SaveVersionPolicies(ctx, rows)
}

type countingBackupStore struct {
	backupStore
	listAgentsCalls int
}

func (s *countingBackupStore) ListAgents(ctx context.Context) ([]storage.AgentRow, error) {
	s.listAgentsCalls++
	return s.backupStore.ListAgents(ctx)
}

func TestBackupServiceRollbackOnImportFailure(t *testing.T) {
	sourceStore, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "rollback-source"), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore(source) error = %v", err)
	}
	defer sourceStore.Close()

	ctx := t.Context()
	if err := sourceStore.SaveAgent(ctx, storage.AgentRow{
		ID:         "edge-b",
		Name:       "edge-b",
		AgentToken: "token-edge-b",
	}); err != nil {
		t.Fatalf("SaveAgent() error = %v", err)
	}
	if err := sourceStore.SaveVersionPolicies(ctx, []storage.VersionPolicyRow{{
		ID:             "beta",
		Channel:        "beta",
		DesiredVersion: "2.0.0",
		PackagesJSON:   `[{"platform":"linux-amd64","url":"https://example.com/nre-agent-beta","sha256":"beta123"}]`,
		TagsJSON:       `[]`,
	}}); err != nil {
		t.Fatalf("SaveVersionPolicies() error = %v", err)
	}

	sourceSvc := NewBackupService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, sourceStore)
	archive, _, err := sourceSvc.Export(ctx)
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}

	targetStore, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "rollback-target"), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore(target) error = %v", err)
	}
	defer targetStore.Close()

	failingStore := &failingBackupStore{
		backupStore:                    targetStore,
		remainingVersionPolicyFailures: 1,
	}
	targetSvc := NewBackupService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, failingStore)
	if _, err := targetSvc.Import(ctx, archive); err == nil {
		t.Fatal("Import() error = nil, want rollback failure path")
	}

	agents, err := targetStore.ListAgents(ctx)
	if err != nil {
		t.Fatalf("ListAgents() error = %v", err)
	}
	if len(agents) != 0 {
		t.Fatalf("agents after rollback = %+v", agents)
	}
	policies, err := targetStore.ListVersionPolicies(ctx)
	if err != nil {
		t.Fatalf("ListVersionPolicies() error = %v", err)
	}
	if len(policies) != 0 {
		t.Fatalf("version policies after rollback = %+v", policies)
	}
}

func TestBackupServiceImportBumpsLocalSnapshotRevisionForRestoredLocalRules(t *testing.T) {
	sourceStore, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "local-source"), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore(source) error = %v", err)
	}
	defer sourceStore.Close()

	ctx := t.Context()
	if err := sourceStore.SaveHTTPRules(ctx, "local", []storage.HTTPRuleRow{{
		ID:                1,
		AgentID:           "local",
		FrontendURL:       "https://restored.example.com",
		BackendURL:        "http://127.0.0.1:8096",
		BackendsJSON:      `[{"url":"http://127.0.0.1:8096"}]`,
		LoadBalancingJSON: `{"strategy":"adaptive"}`,
		Enabled:           true,
		RelayChainJSON:    `[]`,
		TagsJSON:          `[]`,
		CustomHeadersJSON: `[]`,
		Revision:          4,
	}}); err != nil {
		t.Fatalf("SaveHTTPRules() error = %v", err)
	}

	sourceSvc := NewBackupService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, sourceStore)
	archive, _, err := sourceSvc.Export(ctx)
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}

	targetStore, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "local-target"), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore(target) error = %v", err)
	}
	defer targetStore.Close()
	if err := targetStore.SaveLocalRuntimeState(ctx, "local", storage.RuntimeState{
		CurrentRevision:   10,
		LastApplyRevision: 10,
		LastApplyStatus:   "success",
	}); err != nil {
		t.Fatalf("SaveLocalRuntimeState() error = %v", err)
	}

	targetSvc := NewBackupService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, targetStore)
	if _, err := targetSvc.Import(ctx, archive); err != nil {
		t.Fatalf("Import() error = %v", err)
	}

	snapshot, err := targetStore.LoadLocalSnapshot(ctx, "local")
	if err != nil {
		t.Fatalf("LoadLocalSnapshot() error = %v", err)
	}
	if len(snapshot.Rules) != 1 {
		t.Fatalf("local snapshot rules = %+v", snapshot.Rules)
	}
	if snapshot.Revision <= 10 {
		t.Fatalf("local snapshot revision = %d, want > 10 after import", snapshot.Revision)
	}
}

func TestBackupServiceImportBumpsDesiredRevisionForCertificateOnlyRestore(t *testing.T) {
	sourceStore, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "cert-source"), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore(source) error = %v", err)
	}
	defer sourceStore.Close()

	ctx := t.Context()
	if err := sourceStore.SaveAgent(ctx, storage.AgentRow{
		ID:               "edge-a",
		Name:             "edge-a",
		AgentToken:       "token-edge-a",
		Platform:         "linux-amd64",
		CapabilitiesJSON: `["cert_install"]`,
		DesiredRevision:  2,
		CurrentRevision:  2,
	}); err != nil {
		t.Fatalf("SaveAgent(source) error = %v", err)
	}
	if err := sourceStore.SaveManagedCertificates(ctx, []storage.ManagedCertificateRow{{
		ID:              1,
		Domain:          "cert-only.example.com",
		Enabled:         true,
		Scope:           "domain",
		IssuerMode:      "local_http01",
		TargetAgentIDs:  `["edge-a"]`,
		Status:          "active",
		MaterialHash:    "hash-a",
		AgentReports:    `{}`,
		ACMEInfo:        `{}`,
		Usage:           "https",
		CertificateType: "uploaded",
		TagsJSON:        `[]`,
		Revision:        3,
	}}); err != nil {
		t.Fatalf("SaveManagedCertificates(source) error = %v", err)
	}
	if err := sourceStore.SaveManagedCertificateMaterial(ctx, "cert-only.example.com", storage.ManagedCertificateBundle{
		Domain:  "cert-only.example.com",
		CertPEM: "cert-pem",
		KeyPEM:  "key-pem",
	}); err != nil {
		t.Fatalf("SaveManagedCertificateMaterial() error = %v", err)
	}

	sourceSvc := NewBackupService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, sourceStore)
	archive, _, err := sourceSvc.Export(ctx)
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}

	targetStore, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "cert-target"), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore(target) error = %v", err)
	}
	defer targetStore.Close()
	if err := targetStore.SaveAgent(ctx, storage.AgentRow{
		ID:               "edge-a",
		Name:             "edge-a",
		AgentToken:       "token-edge-a",
		Platform:         "linux-amd64",
		CapabilitiesJSON: `["cert_install"]`,
		DesiredRevision:  50,
		CurrentRevision:  50,
	}); err != nil {
		t.Fatalf("SaveAgent(target) error = %v", err)
	}

	targetSvc := NewBackupService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, targetStore)
	if _, err := targetSvc.Import(ctx, archive); err != nil {
		t.Fatalf("Import() error = %v", err)
	}

	agents, err := targetStore.ListAgents(ctx)
	if err != nil {
		t.Fatalf("ListAgents() error = %v", err)
	}
	if len(agents) != 1 {
		t.Fatalf("agents = %+v", agents)
	}
	if agents[0].DesiredRevision <= 50 {
		t.Fatalf("desired revision = %d, want > 50 after cert-only restore", agents[0].DesiredRevision)
	}

	snapshot, err := targetStore.LoadAgentSnapshot(ctx, "edge-a", storage.AgentSnapshotInput{
		DesiredRevision: agents[0].DesiredRevision,
		CurrentRevision: agents[0].CurrentRevision,
		Platform:        agents[0].Platform,
	})
	if err != nil {
		t.Fatalf("LoadAgentSnapshot() error = %v", err)
	}
	if snapshot.Revision <= 50 {
		t.Fatalf("snapshot revision = %d, want > 50 after cert-only restore", snapshot.Revision)
	}
	if len(snapshot.Certificates) != 1 {
		t.Fatalf("snapshot certificates = %+v", snapshot.Certificates)
	}
}

func TestBackupServiceBumpModifiedAgentsListsAgentsOnce(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "counting-target"), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore(target) error = %v", err)
	}
	defer store.Close()

	ctx := t.Context()
	for _, row := range []storage.AgentRow{
		{ID: "edge-a", Name: "edge-a", AgentToken: "token-a", CurrentRevision: 3, DesiredRevision: 3},
		{ID: "edge-b", Name: "edge-b", AgentToken: "token-b", CurrentRevision: 8, DesiredRevision: 8},
	} {
		if err := store.SaveAgent(ctx, row); err != nil {
			t.Fatalf("SaveAgent(%s) error = %v", row.ID, err)
		}
	}

	countingStore := &countingBackupStore{backupStore: store}
	svc := NewBackupService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, countingStore)

	if err := svc.bumpModifiedAgents(ctx, modifiedAgentRevisions{"edge-a": 4, "edge-b": 9}); err != nil {
		t.Fatalf("bumpModifiedAgents() error = %v", err)
	}
	if countingStore.listAgentsCalls != 1 {
		t.Fatalf("ListAgents() calls = %d, want 1", countingStore.listAgentsCalls)
	}

	agents, err := store.ListAgents(ctx)
	if err != nil {
		t.Fatalf("ListAgents() after bump error = %v", err)
	}
	byID := map[string]storage.AgentRow{}
	for _, row := range agents {
		byID[row.ID] = row
	}
	if byID["edge-a"].DesiredRevision != 4 {
		t.Fatalf("edge-a DesiredRevision = %d, want 4", byID["edge-a"].DesiredRevision)
	}
	if byID["edge-b"].DesiredRevision != 9 {
		t.Fatalf("edge-b DesiredRevision = %d, want 9", byID["edge-b"].DesiredRevision)
	}
}

func TestBackupServiceAllowsSameL4ListenAcrossDifferentAgents(t *testing.T) {
	sourceStore, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "l4-source"), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore(source) error = %v", err)
	}
	defer sourceStore.Close()

	ctx := t.Context()
	for _, agent := range []storage.AgentRow{
		{ID: "edge-a", Name: "edge-a", AgentToken: "token-a"},
		{ID: "edge-b", Name: "edge-b", AgentToken: "token-b"},
	} {
		if err := sourceStore.SaveAgent(ctx, agent); err != nil {
			t.Fatalf("SaveAgent(%s) error = %v", agent.ID, err)
		}
	}
	if err := sourceStore.SaveL4Rules(ctx, "edge-a", []storage.L4RuleRow{{
		ID:                1,
		AgentID:           "edge-a",
		Name:              "edge-a tcp",
		Protocol:          "tcp",
		ListenHost:        "0.0.0.0",
		ListenPort:        25565,
		UpstreamHost:      "127.0.0.1",
		UpstreamPort:      25565,
		BackendsJSON:      `[{"host":"127.0.0.1","port":25565}]`,
		LoadBalancingJSON: `{"strategy":"adaptive"}`,
		TuningJSON:        `{"proxy_protocol":{"decode":false,"send":false}}`,
		RelayChainJSON:    `[]`,
		Enabled:           true,
		TagsJSON:          `[]`,
	}}); err != nil {
		t.Fatalf("SaveL4Rules(edge-a) error = %v", err)
	}
	if err := sourceStore.SaveL4Rules(ctx, "edge-b", []storage.L4RuleRow{{
		ID:                2,
		AgentID:           "edge-b",
		Name:              "edge-b tcp",
		Protocol:          "tcp",
		ListenHost:        "0.0.0.0",
		ListenPort:        25565,
		UpstreamHost:      "127.0.0.1",
		UpstreamPort:      25566,
		BackendsJSON:      `[{"host":"127.0.0.1","port":25566}]`,
		LoadBalancingJSON: `{"strategy":"adaptive"}`,
		TuningJSON:        `{"proxy_protocol":{"decode":false,"send":false}}`,
		RelayChainJSON:    `[]`,
		Enabled:           true,
		TagsJSON:          `[]`,
	}}); err != nil {
		t.Fatalf("SaveL4Rules(edge-b) error = %v", err)
	}

	sourceSvc := NewBackupService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, sourceStore)
	archive, _, err := sourceSvc.Export(ctx)
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}

	targetStore, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "l4-target"), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore(target) error = %v", err)
	}
	defer targetStore.Close()

	targetSvc := NewBackupService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, targetStore)
	result, err := targetSvc.Import(ctx, archive)
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	if result.Summary.Imported.L4Rules != 2 || result.Summary.SkippedConflict.L4Rules != 0 {
		t.Fatalf("L4 import summary = %+v", result.Summary)
	}
}

func TestBackupServiceAllowsSameHTTPFrontendAcrossDifferentAgents(t *testing.T) {
	sourceStore, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "http-source"), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore(source) error = %v", err)
	}
	defer sourceStore.Close()

	ctx := t.Context()
	for _, agent := range []storage.AgentRow{
		{ID: "edge-a", Name: "edge-a", AgentToken: "token-a"},
		{ID: "edge-b", Name: "edge-b", AgentToken: "token-b"},
	} {
		if err := sourceStore.SaveAgent(ctx, agent); err != nil {
			t.Fatalf("SaveAgent(%s) error = %v", agent.ID, err)
		}
	}
	for _, item := range []struct {
		agentID string
		id      int
		backend string
	}{
		{agentID: "edge-a", id: 1, backend: "http://127.0.0.1:8096"},
		{agentID: "edge-b", id: 2, backend: "http://127.0.0.1:8097"},
	} {
		if err := sourceStore.SaveHTTPRules(ctx, item.agentID, []storage.HTTPRuleRow{{
			ID:                item.id,
			AgentID:           item.agentID,
			FrontendURL:       "https://media.example.com",
			BackendURL:        item.backend,
			BackendsJSON:      fmt.Sprintf(`[{"url":"%s"}]`, item.backend),
			LoadBalancingJSON: `{"strategy":"adaptive"}`,
			Enabled:           true,
			TagsJSON:          `[]`,
			RelayChainJSON:    `[]`,
			CustomHeadersJSON: `[]`,
			Revision:          item.id,
		}}); err != nil {
			t.Fatalf("SaveHTTPRules(%s) error = %v", item.agentID, err)
		}
	}

	archive, _, err := NewBackupService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, sourceStore).Export(ctx)
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}

	targetStore, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "http-target"), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore(target) error = %v", err)
	}
	defer targetStore.Close()

	result, err := NewBackupService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, targetStore).Import(ctx, archive)
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	if result.Summary.Imported.HTTPRules != 2 || result.Summary.SkippedConflict.HTTPRules != 0 {
		t.Fatalf("HTTP import summary = %+v", result.Summary)
	}

	for _, agentID := range []string{"edge-a", "edge-b"} {
		rows, err := targetStore.ListHTTPRules(ctx, agentID)
		if err != nil {
			t.Fatalf("ListHTTPRules(%s) error = %v", agentID, err)
		}
		if len(rows) != 1 || rows[0].FrontendURL != "https://media.example.com" {
			t.Fatalf("imported http rules for %s = %+v", agentID, rows)
		}
	}
}

func TestBackupServiceImportReassignsHTTPRuleIDAndRevisionWhenExistingL4RuleUsesThatFloor(t *testing.T) {
	sourceStore, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "http-cross-source"), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore(source) error = %v", err)
	}
	defer sourceStore.Close()

	targetStore, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "http-cross-target"), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore(target) error = %v", err)
	}
	defer targetStore.Close()

	ctx := t.Context()
	agent := storage.AgentRow{
		ID:               "edge-a",
		Name:             "edge-a",
		AgentToken:       "token-edge-a",
		CapabilitiesJSON: `["http_rules","l4"]`,
	}
	if err := sourceStore.SaveAgent(ctx, agent); err != nil {
		t.Fatalf("SaveAgent(source) error = %v", err)
	}
	if err := targetStore.SaveAgent(ctx, storage.AgentRow{
		ID:               "edge-a",
		Name:             "edge-a",
		AgentToken:       "token-edge-a",
		CapabilitiesJSON: `["http_rules","l4"]`,
		DesiredRevision:  0,
		CurrentRevision:  0,
	}); err != nil {
		t.Fatalf("SaveAgent(target) error = %v", err)
	}
	if err := sourceStore.SaveHTTPRules(ctx, "edge-a", []storage.HTTPRuleRow{{
		ID:                9,
		AgentID:           "edge-a",
		FrontendURL:       "https://import-http.example.com",
		BackendURL:        "http://127.0.0.1:8096",
		BackendsJSON:      `[{"url":"http://127.0.0.1:8096"}]`,
		LoadBalancingJSON: `{"strategy":"adaptive"}`,
		Enabled:           true,
		RelayChainJSON:    `[]`,
		TagsJSON:          `[]`,
		CustomHeadersJSON: `[]`,
		Revision:          4,
	}}); err != nil {
		t.Fatalf("SaveHTTPRules(source) error = %v", err)
	}
	if err := targetStore.SaveL4Rules(ctx, "edge-a", []storage.L4RuleRow{{
		ID:                9,
		AgentID:           "edge-a",
		Name:              "existing l4",
		Protocol:          "tcp",
		ListenHost:        "0.0.0.0",
		ListenPort:        25565,
		UpstreamHost:      "127.0.0.1",
		UpstreamPort:      25565,
		BackendsJSON:      `[{"host":"127.0.0.1","port":25565}]`,
		LoadBalancingJSON: `{"strategy":"adaptive"}`,
		TuningJSON:        `{"proxy_protocol":{"decode":false,"send":false}}`,
		RelayChainJSON:    `[]`,
		Enabled:           true,
		TagsJSON:          `[]`,
		Revision:          9,
	}}); err != nil {
		t.Fatalf("SaveL4Rules(target) error = %v", err)
	}

	archive, _, err := NewBackupService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, sourceStore).Export(ctx)
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}

	result, err := NewBackupService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, targetStore).Import(ctx, archive)
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	if result.Summary.Imported.HTTPRules != 1 {
		t.Fatalf("HTTP import summary = %+v", result.Summary)
	}

	rows, err := targetStore.ListHTTPRules(ctx, "edge-a")
	if err != nil {
		t.Fatalf("ListHTTPRules() error = %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("imported http rows = %+v", rows)
	}
	if rows[0].ID != 10 {
		t.Fatalf("imported http id = %d", rows[0].ID)
	}
	assertRevisionAboveFloor(t, "imported http revision", rows[0].Revision, 9)
	agents, err := targetStore.ListAgents(ctx)
	if err != nil {
		t.Fatalf("ListAgents() error = %v", err)
	}
	if len(agents) != 1 {
		t.Fatalf("agents after import = %+v", agents)
	}
	assertRevisionAboveFloor(t, "imported agent desired revision", agents[0].DesiredRevision, 9)
	assertRevisionNotBehind(t, "imported agent desired revision", agents[0].DesiredRevision, rows[0].Revision)
}

func TestBackupServiceImportReassignsL4RuleIDAndRevisionWhenExistingHTTPRuleUsesThatFloor(t *testing.T) {
	sourceStore, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "l4-cross-source"), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore(source) error = %v", err)
	}
	defer sourceStore.Close()

	targetStore, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "l4-cross-target"), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore(target) error = %v", err)
	}
	defer targetStore.Close()

	ctx := t.Context()
	if err := sourceStore.SaveAgent(ctx, storage.AgentRow{
		ID:               "edge-a",
		Name:             "edge-a",
		AgentToken:       "token-edge-a",
		CapabilitiesJSON: `["http_rules","l4"]`,
	}); err != nil {
		t.Fatalf("SaveAgent(source) error = %v", err)
	}
	if err := targetStore.SaveAgent(ctx, storage.AgentRow{
		ID:               "edge-a",
		Name:             "edge-a",
		AgentToken:       "token-edge-a",
		CapabilitiesJSON: `["http_rules","l4"]`,
		DesiredRevision:  0,
		CurrentRevision:  0,
	}); err != nil {
		t.Fatalf("SaveAgent(target) error = %v", err)
	}
	if err := sourceStore.SaveL4Rules(ctx, "edge-a", []storage.L4RuleRow{{
		ID:                11,
		AgentID:           "edge-a",
		Name:              "import l4",
		Protocol:          "tcp",
		ListenHost:        "0.0.0.0",
		ListenPort:        25566,
		UpstreamHost:      "127.0.0.1",
		UpstreamPort:      25566,
		BackendsJSON:      `[{"host":"127.0.0.1","port":25566}]`,
		LoadBalancingJSON: `{"strategy":"adaptive"}`,
		TuningJSON:        `{"proxy_protocol":{"decode":false,"send":false}}`,
		RelayChainJSON:    `[]`,
		Enabled:           true,
		TagsJSON:          `[]`,
		Revision:          4,
	}}); err != nil {
		t.Fatalf("SaveL4Rules(source) error = %v", err)
	}
	if err := targetStore.SaveHTTPRules(ctx, "edge-a", []storage.HTTPRuleRow{{
		ID:                11,
		AgentID:           "edge-a",
		FrontendURL:       "https://existing-http.example.com",
		BackendURL:        "http://127.0.0.1:8096",
		BackendsJSON:      `[{"url":"http://127.0.0.1:8096"}]`,
		LoadBalancingJSON: `{"strategy":"adaptive"}`,
		Enabled:           true,
		RelayChainJSON:    `[]`,
		TagsJSON:          `[]`,
		CustomHeadersJSON: `[]`,
		Revision:          9,
	}}); err != nil {
		t.Fatalf("SaveHTTPRules(target) error = %v", err)
	}

	archive, _, err := NewBackupService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, sourceStore).Export(ctx)
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}

	result, err := NewBackupService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, targetStore).Import(ctx, archive)
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	if result.Summary.Imported.L4Rules != 1 {
		t.Fatalf("L4 import summary = %+v", result.Summary)
	}

	rows, err := targetStore.ListL4Rules(ctx, "edge-a")
	if err != nil {
		t.Fatalf("ListL4Rules() error = %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("imported l4 rows = %+v", rows)
	}
	if rows[0].ID != 12 {
		t.Fatalf("imported l4 id = %d", rows[0].ID)
	}
	assertRevisionAboveFloor(t, "imported l4 revision", rows[0].Revision, 9)
	agents, err := targetStore.ListAgents(ctx)
	if err != nil {
		t.Fatalf("ListAgents() error = %v", err)
	}
	if len(agents) != 1 {
		t.Fatalf("agents after import = %+v", agents)
	}
	assertRevisionAboveFloor(t, "imported agent desired revision", agents[0].DesiredRevision, 9)
	assertRevisionNotBehind(t, "imported agent desired revision", agents[0].DesiredRevision, rows[0].Revision)
}

func TestBackupServicePreviewAccountsForAgentRemapBeforeConflictChecks(t *testing.T) {
	sourceStore, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "preview-source"), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore(source) error = %v", err)
	}
	defer sourceStore.Close()

	targetStore, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "preview-target"), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore(target) error = %v", err)
	}
	defer targetStore.Close()

	ctx := t.Context()
	if err := sourceStore.SaveAgent(ctx, storage.AgentRow{
		ID:         "edge-from-backup",
		Name:       "edge-a",
		AgentToken: "token-source",
	}); err != nil {
		t.Fatalf("SaveAgent(source) error = %v", err)
	}
	if err := sourceStore.SaveHTTPRules(ctx, "edge-from-backup", []storage.HTTPRuleRow{{
		ID:                1,
		AgentID:           "edge-from-backup",
		FrontendURL:       "https://shared.example.com",
		BackendURL:        "http://127.0.0.1:8096",
		BackendsJSON:      `[{"url":"http://127.0.0.1:8096"}]`,
		LoadBalancingJSON: `{"strategy":"adaptive"}`,
		Enabled:           true,
		RelayChainJSON:    `[]`,
		TagsJSON:          `[]`,
		CustomHeadersJSON: `[]`,
		Revision:          1,
	}}); err != nil {
		t.Fatalf("SaveHTTPRules(source) error = %v", err)
	}
	if err := sourceStore.SaveL4Rules(ctx, "edge-from-backup", []storage.L4RuleRow{{
		ID:                2,
		AgentID:           "edge-from-backup",
		Name:              "backup-l4",
		Protocol:          "tcp",
		ListenHost:        "0.0.0.0",
		ListenPort:        25565,
		UpstreamHost:      "127.0.0.1",
		UpstreamPort:      25565,
		BackendsJSON:      `[{"host":"127.0.0.1","port":25565}]`,
		LoadBalancingJSON: `{"strategy":"adaptive"}`,
		TuningJSON:        `{"proxy_protocol":{"decode":false,"send":false}}`,
		RelayChainJSON:    `[]`,
		Enabled:           true,
		TagsJSON:          `[]`,
		Revision:          1,
	}}); err != nil {
		t.Fatalf("SaveL4Rules(source) error = %v", err)
	}
	if err := sourceStore.SaveRelayListeners(ctx, "edge-from-backup", []storage.RelayListenerRow{{
		ID:                      3,
		AgentID:                 "edge-from-backup",
		Name:                    "shared-relay",
		ListenHost:              "127.0.0.1",
		BindHostsJSON:           `["127.0.0.1"]`,
		ListenPort:              7443,
		PublicHost:              "relay.example.com",
		PublicPort:              7443,
		Enabled:                 true,
		TLSMode:                 "pin_only",
		TransportMode:           "tls_tcp",
		ObfsMode:                "off",
		PinSetJSON:              `[{"type":"spki_sha256","value":"fixture-pin"}]`,
		TrustedCACertificateIDs: `[]`,
		TagsJSON:                `[]`,
		Revision:                1,
	}}); err != nil {
		t.Fatalf("SaveRelayListeners(source) error = %v", err)
	}

	if err := targetStore.SaveAgent(ctx, storage.AgentRow{
		ID:         "edge-live",
		Name:       "edge-a",
		AgentToken: "token-target",
	}); err != nil {
		t.Fatalf("SaveAgent(target) error = %v", err)
	}
	if err := targetStore.SaveHTTPRules(ctx, "edge-live", []storage.HTTPRuleRow{{
		ID:                10,
		AgentID:           "edge-live",
		FrontendURL:       "https://shared.example.com",
		BackendURL:        "http://127.0.0.1:9096",
		BackendsJSON:      `[{"url":"http://127.0.0.1:9096"}]`,
		LoadBalancingJSON: `{"strategy":"adaptive"}`,
		Enabled:           true,
		RelayChainJSON:    `[]`,
		TagsJSON:          `[]`,
		CustomHeadersJSON: `[]`,
		Revision:          10,
	}}); err != nil {
		t.Fatalf("SaveHTTPRules(target) error = %v", err)
	}
	if err := targetStore.SaveL4Rules(ctx, "edge-live", []storage.L4RuleRow{{
		ID:                11,
		AgentID:           "edge-live",
		Name:              "live-l4",
		Protocol:          "tcp",
		ListenHost:        "0.0.0.0",
		ListenPort:        25565,
		UpstreamHost:      "127.0.0.1",
		UpstreamPort:      25566,
		BackendsJSON:      `[{"host":"127.0.0.1","port":25566}]`,
		LoadBalancingJSON: `{"strategy":"adaptive"}`,
		TuningJSON:        `{"proxy_protocol":{"decode":false,"send":false}}`,
		RelayChainJSON:    `[]`,
		Enabled:           true,
		TagsJSON:          `[]`,
		Revision:          10,
	}}); err != nil {
		t.Fatalf("SaveL4Rules(target) error = %v", err)
	}
	if err := targetStore.SaveRelayListeners(ctx, "edge-live", []storage.RelayListenerRow{{
		ID:                      12,
		AgentID:                 "edge-live",
		Name:                    "shared-relay",
		ListenHost:              "127.0.0.1",
		BindHostsJSON:           `["127.0.0.1"]`,
		ListenPort:              8443,
		PublicHost:              "relay.example.com",
		PublicPort:              8443,
		Enabled:                 true,
		TLSMode:                 "pin_only",
		TransportMode:           "tls_tcp",
		ObfsMode:                "off",
		PinSetJSON:              `[{"type":"spki_sha256","value":"fixture-pin"}]`,
		TrustedCACertificateIDs: `[]`,
		TagsJSON:                `[]`,
		Revision:                10,
	}}); err != nil {
		t.Fatalf("SaveRelayListeners(target) error = %v", err)
	}

	archive, _, err := NewBackupService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, sourceStore).Export(ctx)
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}

	result, err := NewBackupService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, targetStore).Preview(ctx, archive)
	if err != nil {
		t.Fatalf("Preview() error = %v", err)
	}

	if result.Summary.SkippedConflict.Agents != 1 {
		t.Fatalf("agent preview conflicts = %+v", result.Summary.SkippedConflict)
	}
	if result.Summary.SkippedConflict.HTTPRules != 1 {
		t.Fatalf("http preview conflicts = %+v", result.Summary.SkippedConflict)
	}
	if result.Summary.SkippedConflict.L4Rules != 1 {
		t.Fatalf("l4 preview conflicts = %+v", result.Summary.SkippedConflict)
	}
	if result.Summary.SkippedConflict.RelayListeners != 1 {
		t.Fatalf("relay preview conflicts = %+v", result.Summary.SkippedConflict)
	}
	if result.Summary.Imported.HTTPRules != 0 || result.Summary.Imported.L4Rules != 0 || result.Summary.Imported.RelayListeners != 0 {
		t.Fatalf("preview imported summary = %+v", result.Summary.Imported)
	}
}

func TestBackupServicePreviewTreatsIncomingLocalAgentAsRemappedConflict(t *testing.T) {
	sourceStore, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "preview-local-source"), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore(source) error = %v", err)
	}
	defer sourceStore.Close()

	targetStore, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "preview-local-target"), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore(target) error = %v", err)
	}
	defer targetStore.Close()

	ctx := t.Context()
	if err := sourceStore.SaveAgent(ctx, storage.AgentRow{
		ID:         "source-local",
		Name:       "embedded-source",
		AgentToken: "token-source-local",
		Mode:       "local",
	}); err != nil {
		t.Fatalf("SaveAgent(source local) error = %v", err)
	}

	archive, _, err := NewBackupService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "source-local",
		LocalAgentName:   "embedded-source",
	}, sourceStore).Export(ctx)
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}

	result, err := NewBackupService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "target-local",
		LocalAgentName:   "embedded-target",
	}, targetStore).Preview(ctx, archive)
	if err != nil {
		t.Fatalf("Preview() error = %v", err)
	}

	if result.Summary.SkippedConflict.Agents != 1 {
		t.Fatalf("agent preview conflicts = %+v", result.Summary.SkippedConflict)
	}
	if result.Summary.Imported.Agents != 0 {
		t.Fatalf("agent preview imported = %+v", result.Summary.Imported)
	}

	found := false
	for _, item := range result.Report.SkippedConflict {
		if item.Kind == "agent" && item.Key == "embedded-source" && item.Reason == "local agent remapped to target" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("skipped conflict report = %+v", result.Report.SkippedConflict)
	}
}

func TestBackupServicePreviewRejectsRulesWithMissingRelayChainDependencies(t *testing.T) {
	sourceStore, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "preview-relay-source"), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore(source) error = %v", err)
	}
	defer sourceStore.Close()

	targetStore, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "preview-relay-target"), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore(target) error = %v", err)
	}
	defer targetStore.Close()

	ctx := t.Context()
	if err := sourceStore.SaveAgent(ctx, storage.AgentRow{
		ID:         "edge-a",
		Name:       "edge-a",
		AgentToken: "token-edge-a",
	}); err != nil {
		t.Fatalf("SaveAgent(source) error = %v", err)
	}
	if err := sourceStore.SaveRelayListeners(ctx, "edge-a", []storage.RelayListenerRow{{
		ID:                      31,
		AgentID:                 "edge-a",
		Name:                    "relay-edge",
		ListenHost:              "127.0.0.1",
		BindHostsJSON:           `["127.0.0.1"]`,
		ListenPort:              7443,
		PublicHost:              "relay.example.com",
		PublicPort:              7443,
		Enabled:                 true,
		TLSMode:                 "pin_only",
		TransportMode:           "tls_tcp",
		ObfsMode:                "off",
		PinSetJSON:              `[{"type":"spki_sha256","value":"fixture-pin"}]`,
		TrustedCACertificateIDs: `[]`,
		TagsJSON:                `[]`,
		Revision:                2,
	}}); err != nil {
		t.Fatalf("SaveRelayListeners(source) error = %v", err)
	}
	if err := sourceStore.SaveHTTPRules(ctx, "edge-a", []storage.HTTPRuleRow{{
		ID:                11,
		AgentID:           "edge-a",
		FrontendURL:       "https://relay-http.example.com",
		BackendURL:        "http://127.0.0.1:8096",
		BackendsJSON:      `[{"url":"http://127.0.0.1:8096"}]`,
		LoadBalancingJSON: `{"strategy":"adaptive"}`,
		Enabled:           true,
		RelayChainJSON:    `[31]`,
		TagsJSON:          `[]`,
		CustomHeadersJSON: `[]`,
		Revision:          2,
	}}); err != nil {
		t.Fatalf("SaveHTTPRules(source) error = %v", err)
	}
	if err := sourceStore.SaveL4Rules(ctx, "edge-a", []storage.L4RuleRow{{
		ID:                12,
		AgentID:           "edge-a",
		Name:              "relay-l4",
		Protocol:          "tcp",
		ListenHost:        "0.0.0.0",
		ListenPort:        25565,
		UpstreamHost:      "127.0.0.1",
		UpstreamPort:      25565,
		BackendsJSON:      `[{"host":"127.0.0.1","port":25565}]`,
		LoadBalancingJSON: `{"strategy":"adaptive"}`,
		TuningJSON:        `{"proxy_protocol":{"decode":false,"send":false}}`,
		RelayChainJSON:    `[31]`,
		Enabled:           true,
		TagsJSON:          `[]`,
		Revision:          2,
	}}); err != nil {
		t.Fatalf("SaveL4Rules(source) error = %v", err)
	}

	archive, _, err := NewBackupService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, sourceStore).Export(ctx)
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}

	bundle, err := decodeBackupBundle(archive)
	if err != nil {
		t.Fatalf("decodeBackupBundle() error = %v", err)
	}
	bundle.RelayListeners = nil
	bundle.Manifest.Counts.RelayListeners = 0

	archive, err = encodeBackupBundle(bundle)
	if err != nil {
		t.Fatalf("encodeBackupBundle() error = %v", err)
	}

	result, err := NewBackupService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, targetStore).Preview(ctx, archive)
	if err != nil {
		t.Fatalf("Preview() error = %v", err)
	}

	if result.Summary.SkippedInvalid.HTTPRules != 1 || result.Summary.SkippedInvalid.L4Rules != 1 {
		t.Fatalf("preview invalid summary = %+v", result.Summary.SkippedInvalid)
	}
	if result.Summary.Imported.HTTPRules != 0 || result.Summary.Imported.L4Rules != 0 {
		t.Fatalf("preview imported summary = %+v", result.Summary.Imported)
	}
}

func TestBackupServicePreviewRejectsRelayListenersWithMissingCertificateDependencies(t *testing.T) {
	sourceStore, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "preview-cert-source"), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore(source) error = %v", err)
	}
	defer sourceStore.Close()

	targetStore, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "preview-cert-target"), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore(target) error = %v", err)
	}
	defer targetStore.Close()

	ctx := t.Context()
	if err := sourceStore.SaveAgent(ctx, storage.AgentRow{
		ID:               "edge-a",
		Name:             "edge-a",
		AgentToken:       "token-edge-a",
		CapabilitiesJSON: `["cert_install"]`,
	}); err != nil {
		t.Fatalf("SaveAgent(source) error = %v", err)
	}
	if err := sourceStore.SaveManagedCertificates(ctx, []storage.ManagedCertificateRow{
		{
			ID:              21,
			Domain:          "leaf.example.com",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "local_http01",
			TargetAgentIDs:  `["edge-a"]`,
			Status:          "active",
			LastIssueAt:     "2026-04-18T12:00:00Z",
			MaterialHash:    "leaf-hash",
			AgentReports:    `{}`,
			ACMEInfo:        `{}`,
			Usage:           "https",
			CertificateType: "uploaded",
			TagsJSON:        `[]`,
			Revision:        2,
		},
		{
			ID:              22,
			Domain:          "ca.example.com",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "local_http01",
			TargetAgentIDs:  `["edge-a"]`,
			Status:          "active",
			LastIssueAt:     "2026-04-18T12:00:00Z",
			MaterialHash:    "ca-hash",
			AgentReports:    `{}`,
			ACMEInfo:        `{}`,
			Usage:           "https",
			CertificateType: "uploaded",
			TagsJSON:        `[]`,
			Revision:        3,
		},
	}); err != nil {
		t.Fatalf("SaveManagedCertificates(source) error = %v", err)
	}
	if err := sourceStore.SaveManagedCertificateMaterial(ctx, "leaf.example.com", storage.ManagedCertificateBundle{
		Domain:  "leaf.example.com",
		CertPEM: "leaf-cert",
		KeyPEM:  "leaf-key",
	}); err != nil {
		t.Fatalf("SaveManagedCertificateMaterial(leaf) error = %v", err)
	}
	if err := sourceStore.SaveManagedCertificateMaterial(ctx, "ca.example.com", storage.ManagedCertificateBundle{
		Domain:  "ca.example.com",
		CertPEM: "ca-cert",
		KeyPEM:  "ca-key",
	}); err != nil {
		t.Fatalf("SaveManagedCertificateMaterial(ca) error = %v", err)
	}
	if err := sourceStore.SaveRelayListeners(ctx, "edge-a", []storage.RelayListenerRow{
		{
			ID:                      31,
			AgentID:                 "edge-a",
			Name:                    "relay-missing-cert",
			ListenHost:              "127.0.0.1",
			BindHostsJSON:           `["127.0.0.1"]`,
			ListenPort:              7443,
			PublicHost:              "relay-cert.example.com",
			PublicPort:              7443,
			Enabled:                 true,
			CertificateID:           backupIntPtr(21),
			TLSMode:                 "pin_only",
			TransportMode:           "tls_tcp",
			ObfsMode:                "off",
			PinSetJSON:              `[{"type":"spki_sha256","value":"fixture-pin"}]`,
			TrustedCACertificateIDs: `[]`,
			TagsJSON:                `[]`,
			Revision:                2,
		},
		{
			ID:                      32,
			AgentID:                 "edge-a",
			Name:                    "relay-missing-trusted-ca",
			ListenHost:              "127.0.0.1",
			BindHostsJSON:           `["127.0.0.1"]`,
			ListenPort:              7444,
			PublicHost:              "relay-ca.example.com",
			PublicPort:              7444,
			Enabled:                 true,
			TLSMode:                 "pin_only",
			TransportMode:           "tls_tcp",
			ObfsMode:                "off",
			PinSetJSON:              `[{"type":"spki_sha256","value":"fixture-pin"}]`,
			TrustedCACertificateIDs: `[22]`,
			TagsJSON:                `[]`,
			Revision:                3,
		},
	}); err != nil {
		t.Fatalf("SaveRelayListeners(source) error = %v", err)
	}

	archive, _, err := NewBackupService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, sourceStore).Export(ctx)
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}

	bundle, err := decodeBackupBundle(archive)
	if err != nil {
		t.Fatalf("decodeBackupBundle() error = %v", err)
	}
	bundle.Certificates = nil
	bundle.Materials = nil
	bundle.Manifest.Counts.Certificates = 0
	bundle.Manifest.IncludesCertificates = false

	archive, err = encodeBackupBundle(bundle)
	if err != nil {
		t.Fatalf("encodeBackupBundle() error = %v", err)
	}

	result, err := NewBackupService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, targetStore).Preview(ctx, archive)
	if err != nil {
		t.Fatalf("Preview() error = %v", err)
	}

	if result.Summary.SkippedInvalid.RelayListeners != 2 {
		t.Fatalf("relay preview invalid summary = %+v", result.Summary.SkippedInvalid)
	}
	if result.Summary.Imported.RelayListeners != 0 {
		t.Fatalf("relay preview imported = %+v", result.Summary.Imported)
	}

	foundMissingCert := false
	foundMissingCA := false
	for _, item := range result.Report.SkippedInvalid {
		if item.Kind == "relay_listener" && item.Key == "edge-a|relay-missing-cert" && item.Reason == "referenced certificate was not imported" {
			foundMissingCert = true
		}
		if item.Kind == "relay_listener" && item.Key == "edge-a|relay-missing-trusted-ca" && item.Reason == "referenced trusted CA certificate was not imported" {
			foundMissingCA = true
		}
	}
	if !foundMissingCert || !foundMissingCA {
		t.Fatalf("relay preview invalid report = %+v", result.Report.SkippedInvalid)
	}
}
