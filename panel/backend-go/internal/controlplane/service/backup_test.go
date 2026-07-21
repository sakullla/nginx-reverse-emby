//go:build integration

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
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

func assertBackupSkippedInvalidReason(t *testing.T, result BackupImportResult, kind string, key string, reason string) {
	t.Helper()
	for _, item := range result.Report.SkippedInvalid {
		if item.Kind == kind && item.Key == key && item.Reason == reason {
			return
		}
	}
	t.Fatalf("missing skipped invalid item kind=%q key=%q reason=%q in %+v", kind, key, reason, result.Report.SkippedInvalid)
}

func assertBackupSkippedInvalidReasonCount(t *testing.T, result BackupImportResult, kind string, reason string, want int) {
	t.Helper()
	got := 0
	for _, item := range result.Report.SkippedInvalid {
		if item.Kind == kind && item.Reason == reason {
			got++
		}
	}
	if got != want {
		t.Fatalf("skipped invalid %s reason %q count = %d, want %d in %+v", kind, reason, got, want, result.Report.SkippedInvalid)
	}
}

func assertBackupSkippedConflictReason(t *testing.T, result BackupImportResult, kind string, key string, reason string) {
	t.Helper()
	for _, item := range result.Report.SkippedConflict {
		if item.Kind == kind && item.Key == key && item.Reason == reason {
			return
		}
	}
	t.Fatalf("missing skipped conflict item kind=%q key=%q reason=%q in %+v", kind, key, reason, result.Report.SkippedConflict)
}

func assertBackupSkippedMissingMaterialReason(t *testing.T, result BackupImportResult, kind string, key string, reason string) {
	t.Helper()
	for _, item := range result.Report.SkippedMissingMaterial {
		if item.Kind == kind && item.Key == key && item.Reason == reason {
			return
		}
	}
	t.Fatalf("missing skipped missing material item kind=%q key=%q reason=%q in %+v", kind, key, reason, result.Report.SkippedMissingMaterial)
}

func TestBackupManifestRoundTripShape(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
	sourceStore, err := newServiceTestSQLiteStore(t, filepath.Join(t.TempDir(), "source"), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore(source) error = %v", err)
	}
	defer sourceStore.Close()

	targetStore, err := newServiceTestSQLiteStore(t, filepath.Join(t.TempDir(), "target"), "local")
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
		RelayLayersJSON:   `[[31]]`,
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
		RelayLayersJSON:   `[[31]]`,
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
	if got := rules[0].RelayLayersJSON; got != `[[31]]` {
		t.Fatalf("imported http relay_layers = %s", got)
	}
	l4Rules, err := targetStore.ListL4Rules(ctx, "edge-a")
	if err != nil {
		t.Fatalf("ListL4Rules() error = %v", err)
	}
	if len(l4Rules) != 1 || l4Rules[0].Name != "TCP 25565" {
		t.Fatalf("imported l4 rules = %+v", l4Rules)
	}
	if got := l4Rules[0].RelayLayersJSON; got != `[[31]]` {
		t.Fatalf("imported l4 relay_layers = %s", got)
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
	legacyStore, err := newServiceTestSQLiteStore(t, filepath.Join(t.TempDir(), "legacy-target"), "local")
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

func TestBackupServicePreservesAgentRuntimeConfiguration(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	cfg := config.Config{EnableLocalAgent: true, LocalAgentID: "local"}
	sourceStore, err := newServiceTestSQLiteStore(t, filepath.Join(t.TempDir(), "proxy-source"), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore(source) error = %v", err)
	}
	defer sourceStore.Close()
	if err := sourceStore.SaveAgent(ctx, storage.AgentRow{
		ID:                   "edge-proxy",
		Name:                 "Edge Proxy",
		AgentToken:           "token-proxy",
		CapabilitiesJSON:     `["http_rules","l4","relay_quic"]`,
		OutboundProxyURL:     "socks://user:pass@127.0.0.1:1080",
		TrafficStatsInterval: "30s",
	}); err != nil {
		t.Fatalf("SaveAgent() error = %v", err)
	}

	sourceSvc := NewBackupService(cfg, sourceStore)
	archive, _, err := sourceSvc.Export(ctx)
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}

	targetStore, err := newServiceTestSQLiteStore(t, filepath.Join(t.TempDir(), "proxy-target"), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore(target) error = %v", err)
	}
	defer targetStore.Close()
	targetSvc := NewBackupService(cfg, targetStore)
	if _, err := targetSvc.Import(ctx, archive); err != nil {
		t.Fatalf("Import() error = %v", err)
	}

	agents, err := targetStore.ListAgents(ctx)
	if err != nil {
		t.Fatalf("ListAgents() error = %v", err)
	}
	if len(agents) != 1 {
		t.Fatalf("agents len = %d, want 1", len(agents))
	}
	if agents[0].OutboundProxyURL != "socks://user:pass@127.0.0.1:1080" {
		t.Fatalf("OutboundProxyURL = %q", agents[0].OutboundProxyURL)
	}
	if agents[0].TrafficStatsInterval != "30s" {
		t.Fatalf("TrafficStatsInterval = %q", agents[0].TrafficStatsInterval)
	}
}

func TestBackupServiceExportIncludesEgressProfiles(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	sourceStore, err := newServiceTestSQLiteStore(t, filepath.Join(t.TempDir(), "egress-export-source"), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore(source) error = %v", err)
	}
	defer sourceStore.Close()

	if err := sourceStore.SaveEgressProfiles(ctx, []storage.EgressProfileRow{{
		ID:          41,
		Name:        "office socks",
		Type:        "socks",
		ProxyURL:    "socks5://user:secret@127.0.0.1:1080",
		Enabled:     true,
		Description: "lab",
		Revision:    7,
	}}); err != nil {
		t.Fatalf("SaveEgressProfiles() error = %v", err)
	}

	archive, _, err := NewBackupService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, sourceStore).Export(ctx)
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}
	files := backupArchiveFileNames(t, archive)
	if !files["egress_profiles.json"] {
		t.Fatalf("backup files missing egress_profiles.json: %#v", files)
	}
	bundle, err := decodeBackupBundle(archive)
	if err != nil {
		t.Fatalf("decodeBackupBundle() error = %v", err)
	}
	if bundle.Manifest.Counts.EgressProfiles != 1 {
		t.Fatalf("manifest counts = %+v", bundle.Manifest.Counts)
	}
	if len(bundle.EgressProfiles) != 1 || bundle.EgressProfiles[0].ProxyURL != "socks5://user:secret@127.0.0.1:1080" {
		t.Fatalf("egress profiles = %+v, want raw proxy secret", bundle.EgressProfiles)
	}
}

func TestBackupServiceImportRemapsEgressProfileReferences(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	sourceProfileID := 41
	targetStore, err := newServiceTestSQLiteStore(t, filepath.Join(t.TempDir(), "egress-import-target"), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore(target) error = %v", err)
	}
	defer targetStore.Close()
	if err := targetStore.SaveEgressProfiles(ctx, []storage.EgressProfileRow{{
		ID:       sourceProfileID,
		Name:     "existing direct",
		Type:     "direct",
		Enabled:  true,
		Revision: 2,
	}}); err != nil {
		t.Fatalf("SaveEgressProfiles(target) error = %v", err)
	}

	bundle := BackupBundle{
		Manifest: BackupManifest{
			PackageVersion:     BackupPackageVersion,
			SourceArchitecture: BackupSourceArchitectureGo,
			SourceLocalAgentID: "source-local",
			Counts: BackupCounts{
				Agents:         1,
				HTTPRules:      1,
				L4Rules:        1,
				EgressProfiles: 1,
			},
		},
		Agents: []BackupAgent{{
			ID:           "edge-egress",
			Name:         "edge-egress",
			AgentToken:   "token-edge-egress",
			Capabilities: []string{"http_rules", "l4", "egress_profiles"},
		}},
		EgressProfiles: []BackupEgressProfile{{
			ID:          sourceProfileID,
			Name:        "office socks",
			Type:        "socks",
			ProxyURL:    "socks5://user:secret@127.0.0.1:1080",
			Enabled:     true,
			Description: "lab",
			Revision:    3,
		}},
		HTTPRules: []BackupHTTPRule{{
			ID:               51,
			AgentID:          "edge-egress",
			FrontendURL:      "https://media.example.test",
			Backends:         []HTTPRuleBackend{{URL: "http://127.0.0.1:8096"}},
			LoadBalancing:    HTTPLoadBalancing{Strategy: "adaptive"},
			Enabled:          true,
			ProxyRedirect:    true,
			PassProxyHeaders: defaultPassProxyHeaders(),
			CustomHeaders:    []HTTPCustomHeader{},
			EgressProfileID:  &sourceProfileID,
		}},
		L4Rules: []BackupL4Rule{{
			ID:              52,
			AgentID:         "edge-egress",
			Name:            "tcp egress",
			Protocol:        "tcp",
			ListenHost:      "0.0.0.0",
			ListenPort:      25565,
			Backends:        []L4Backend{{Host: "127.0.0.1", Port: 25565}},
			LoadBalancing:   L4LoadBalancing{Strategy: "adaptive"},
			Tuning:          L4Tuning{ProxyProtocol: L4ProxyProtocolTuning{}},
			ListenMode:      "tcp",
			Enabled:         true,
			EgressProfileID: &sourceProfileID,
		}},
		RelayListeners:   []BackupRelayListener{},
		Certificates:     []BackupCertificate{},
		VersionPolicies:  []BackupVersionPolicy{},
		TrafficPolicies:  []BackupTrafficPolicy{},
		TrafficBaselines: []BackupTrafficBaseline{},
	}
	archive, err := encodeBackupBundle(bundle)
	if err != nil {
		t.Fatalf("encodeBackupBundle() error = %v", err)
	}

	result, err := NewBackupService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, targetStore).Import(ctx, archive)
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	if result.Summary.Imported.EgressProfiles != 1 || result.Summary.Imported.HTTPRules != 1 || result.Summary.Imported.L4Rules != 1 {
		t.Fatalf("import summary = %+v", result.Summary)
	}

	profiles, err := targetStore.ListEgressProfiles(ctx)
	if err != nil {
		t.Fatalf("ListEgressProfiles() error = %v", err)
	}
	if len(profiles) != 2 {
		t.Fatalf("egress profiles = %+v, want existing plus imported", profiles)
	}
	importedID := 0
	for _, profile := range profiles {
		if profile.Name == "office socks" {
			importedID = profile.ID
		}
	}
	if importedID == 0 || importedID == sourceProfileID {
		t.Fatalf("imported egress profile id = %d, want remapped away from %d in %+v", importedID, sourceProfileID, profiles)
	}
	httpRows, err := targetStore.ListHTTPRules(ctx, "edge-egress")
	if err != nil {
		t.Fatalf("ListHTTPRules() error = %v", err)
	}
	l4Rows, err := targetStore.ListL4Rules(ctx, "edge-egress")
	if err != nil {
		t.Fatalf("ListL4Rules() error = %v", err)
	}
	if len(httpRows) != 1 || httpRows[0].EgressProfileID == nil || *httpRows[0].EgressProfileID != importedID {
		t.Fatalf("http rows = %+v, want egress profile id %d", httpRows, importedID)
	}
	if len(l4Rows) != 1 || l4Rows[0].EgressProfileID == nil || *l4Rows[0].EgressProfileID != importedID {
		t.Fatalf("l4 rows = %+v, want egress profile id %d", l4Rows, importedID)
	}
}

func TestBackupServiceImportBumpsRelayedEgressFinalHopAgent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	targetStore, err := newServiceTestSQLiteStore(t, filepath.Join(t.TempDir(), "relayed-egress-import-target"), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore(target) error = %v", err)
	}
	defer targetStore.Close()
	if err := targetStore.SaveAgent(ctx, storage.AgentRow{
		ID:               "relay-a",
		Name:             "relay-a",
		CapabilitiesJSON: marshalStringArray([]string{"relay_quic", "egress_profiles"}),
		DesiredRevision:  10,
		CurrentRevision:  10,
	}); err != nil {
		t.Fatalf("SaveAgent(relay-a) error = %v", err)
	}
	if err := targetStore.SaveRelayListeners(ctx, "relay-a", []storage.RelayListenerRow{{
		ID:            7,
		AgentID:       "relay-a",
		Name:          "relay-a",
		ListenHost:    "127.0.0.1",
		BindHostsJSON: `["127.0.0.1"]`,
		ListenPort:    9443,
		PublicHost:    "relay-a.example.test",
		PublicPort:    9443,
		Enabled:       true,
		TransportMode: "tls_tcp",
	}}); err != nil {
		t.Fatalf("SaveRelayListeners(relay-a) error = %v", err)
	}

	profileID := 41
	bundle := BackupBundle{
		Manifest: BackupManifest{
			PackageVersion:     BackupPackageVersion,
			SourceArchitecture: BackupSourceArchitectureGo,
			SourceLocalAgentID: "source-local",
			Counts: BackupCounts{
				Agents:         1,
				HTTPRules:      1,
				EgressProfiles: 1,
			},
		},
		Agents: []BackupAgent{{
			ID:           "edge-a",
			Name:         "edge-a",
			AgentToken:   "token-edge-a",
			Capabilities: []string{"http_rules", "egress_profiles"},
		}},
		EgressProfiles: []BackupEgressProfile{{
			ID:       profileID,
			Name:     "office socks",
			Type:     "socks",
			ProxyURL: "socks5://127.0.0.1:1080",
			Enabled:  true,
			Revision: 20,
		}},
		RelayListeners: []BackupRelayListener{},
		HTTPRules: []BackupHTTPRule{{
			ID:               51,
			AgentID:          "edge-a",
			FrontendURL:      "http://media.example.test",
			Backends:         []HTTPRuleBackend{{URL: "http://127.0.0.1:8096"}},
			LoadBalancing:    HTTPLoadBalancing{Strategy: "adaptive"},
			RelayLayers:      [][]int{{7}},
			Enabled:          true,
			ProxyRedirect:    true,
			PassProxyHeaders: defaultPassProxyHeaders(),
			CustomHeaders:    []HTTPCustomHeader{},
			EgressProfileID:  &profileID,
		}},
	}
	archive, err := encodeBackupBundle(bundle)
	if err != nil {
		t.Fatalf("encodeBackupBundle() error = %v", err)
	}

	result, err := NewBackupService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, targetStore).Import(ctx, archive)
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	if result.Summary.Imported.HTTPRules != 1 {
		t.Fatalf("import summary = %+v", result.Summary)
	}
	agents, err := targetStore.ListAgents(ctx)
	if err != nil {
		t.Fatalf("ListAgents() error = %v", err)
	}
	desired := map[string]int{}
	for _, row := range agents {
		desired[row.ID] = row.DesiredRevision
	}
	if desired["relay-a"] <= 10 {
		t.Fatalf("agent desired revisions = %+v, want relay-a bumped above current revision 10", desired)
	}
}

func TestBackupServiceImportMigratesLegacyL4ProxyEgress(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	targetStore, err := newServiceTestSQLiteStore(t, filepath.Join(t.TempDir(), "legacy-egress-import-target"), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore(target) error = %v", err)
	}
	defer targetStore.Close()

	bundle := BackupBundle{
		Manifest: BackupManifest{
			PackageVersion:     BackupPackageVersion,
			SourceArchitecture: BackupSourceArchitectureGo,
			SourceLocalAgentID: "source-local",
			Counts: BackupCounts{
				Agents:  1,
				L4Rules: 1,
			},
		},
		Agents: []BackupAgent{{
			ID:           "legacy-egress-agent",
			Name:         "legacy-egress-agent",
			AgentToken:   "token-legacy-egress",
			Capabilities: []string{"l4", "egress_profiles"},
		}},
		L4Rules: []BackupL4Rule{},
	}
	legacyL4Rules := []map[string]any{{
		"id":                52,
		"agent_id":          "legacy-egress-agent",
		"name":              "legacy proxy egress",
		"protocol":          "tcp",
		"listen_host":       "0.0.0.0",
		"listen_port":       25565,
		"backends":          []map[string]any{{"host": "127.0.0.1", "port": 25565}},
		"load_balancing":    map[string]any{"strategy": "adaptive"},
		"tuning":            map[string]any{},
		"listen_mode":       "tcp",
		"proxy_egress_mode": "proxy",
		"proxy_egress_url":  "socks5://user:secret@127.0.0.1:1080",
		"enabled":           true,
		"revision":          4,
	}}
	archive, err := encodeBackupBundleWithOverrides(t, bundle, map[string]any{
		backupL4RulesFile: legacyL4Rules,
	})
	if err != nil {
		t.Fatalf("encodeBackupBundleWithOverrides() error = %v", err)
	}

	result, err := NewBackupService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, targetStore).Import(ctx, archive)
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	if result.Summary.Imported.EgressProfiles != 1 || result.Summary.Imported.L4Rules != 1 {
		t.Fatalf("import summary = %+v", result.Summary)
	}

	profiles, err := targetStore.ListEgressProfiles(ctx)
	if err != nil {
		t.Fatalf("ListEgressProfiles() error = %v", err)
	}
	if len(profiles) != 1 {
		t.Fatalf("egress profiles = %+v, want one migrated profile", profiles)
	}
	if profiles[0].Name != "legacy proxy egress" || profiles[0].Type != "socks" || profiles[0].ProxyURL != "socks5://user:secret@127.0.0.1:1080" || !profiles[0].Enabled {
		t.Fatalf("migrated egress profile = %+v", profiles[0])
	}
	l4Rows, err := targetStore.ListL4Rules(ctx, "legacy-egress-agent")
	if err != nil {
		t.Fatalf("ListL4Rules() error = %v", err)
	}
	if len(l4Rows) != 1 || l4Rows[0].EgressProfileID == nil || *l4Rows[0].EgressProfileID != profiles[0].ID {
		t.Fatalf("l4 rows = %+v, profiles=%+v", l4Rows, profiles)
	}
}

func TestBackupServiceTrafficPolicyAndBaselineRoundTripExcludesHistory(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	cfg := config.Config{EnableLocalAgent: true, LocalAgentID: "local"}
	sourceStore, err := newServiceTestSQLiteStore(t, filepath.Join(t.TempDir(), "traffic-source"), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore(source) error = %v", err)
	}
	defer sourceStore.Close()
	if err := sourceStore.SaveAgent(ctx, storage.AgentRow{
		ID:         "edge-traffic",
		Name:       "edge-traffic",
		AgentToken: "token-traffic",
	}); err != nil {
		t.Fatalf("SaveAgent() error = %v", err)
	}
	quota := int64(1099511627776)
	retentionMonths := 36
	if err := sourceStore.SaveTrafficPolicy(ctx, storage.AgentTrafficPolicyRow{
		AgentID:                "edge-traffic",
		Direction:              "rx",
		CycleStartDay:          15,
		MonthlyQuotaBytes:      &quota,
		BlockWhenExceeded:      true,
		HourlyRetentionDays:    30,
		DailyRetentionMonths:   12,
		MonthlyRetentionMonths: &retentionMonths,
		CreatedAt:              "2026-05-01T00:00:00Z",
		UpdatedAt:              "2026-05-02T00:00:00Z",
	}); err != nil {
		t.Fatalf("SaveTrafficPolicy() error = %v", err)
	}
	if err := sourceStore.SaveTrafficBaseline(ctx, storage.AgentTrafficBaselineRow{
		AgentID:           "edge-traffic",
		CycleStart:        "2026-05-15T00:00:00Z",
		RawRXBytes:        1000,
		RawTXBytes:        2000,
		RawAccountedBytes: 1000,
		AdjustUsedBytes:   -250,
		CreatedAt:         "2026-05-15T01:00:00Z",
		UpdatedAt:         "2026-05-15T02:00:00Z",
	}); err != nil {
		t.Fatalf("SaveTrafficBaseline() error = %v", err)
	}

	archive, _, err := NewBackupService(cfg, sourceStore).Export(ctx)
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}
	files := backupArchiveFileNames(t, archive)
	if !files["traffic_policies.json"] {
		t.Fatalf("backup files missing traffic_policies.json: %#v", files)
	}
	if !files["traffic_baselines.json"] {
		t.Fatalf("backup files missing traffic_baselines.json: %#v", files)
	}
	for _, name := range []string{
		"traffic_raw_cursors.json",
		"traffic_hourly_buckets.json",
		"traffic_daily_summaries.json",
		"traffic_monthly_summaries.json",
		"traffic_events.json",
	} {
		if files[name] {
			t.Fatalf("backup unexpectedly included traffic history file %s", name)
		}
	}

	bundle, err := decodeBackupBundle(archive)
	if err != nil {
		t.Fatalf("decodeBackupBundle() error = %v", err)
	}
	if len(bundle.TrafficPolicies) != 1 || bundle.TrafficPolicies[0].AgentID != "edge-traffic" || bundle.TrafficPolicies[0].MonthlyQuotaBytes == nil || *bundle.TrafficPolicies[0].MonthlyQuotaBytes != quota {
		t.Fatalf("traffic policies = %+v", bundle.TrafficPolicies)
	}
	if len(bundle.TrafficBaselines) != 1 || bundle.TrafficBaselines[0].AgentID != "edge-traffic" || bundle.TrafficBaselines[0].AdjustUsedBytes != -250 {
		t.Fatalf("traffic baselines = %+v", bundle.TrafficBaselines)
	}
	policyPayload, err := json.Marshal(bundle.TrafficPolicies[0])
	if err != nil {
		t.Fatalf("marshal traffic policy: %v", err)
	}
	if !bytes.Contains(policyPayload, []byte(`"agent_id"`)) || bytes.Contains(policyPayload, []byte(`"AgentID"`)) {
		t.Fatalf("traffic policy JSON uses unstable field names: %s", policyPayload)
	}
	baselinePayload, err := json.Marshal(bundle.TrafficBaselines[0])
	if err != nil {
		t.Fatalf("marshal traffic baseline: %v", err)
	}
	if !bytes.Contains(baselinePayload, []byte(`"cycle_start"`)) || bytes.Contains(baselinePayload, []byte(`"CycleStart"`)) {
		t.Fatalf("traffic baseline JSON uses unstable field names: %s", baselinePayload)
	}
	if bundle.Manifest.Counts.TrafficPolicies != 1 || bundle.Manifest.Counts.TrafficBaselines != 1 {
		t.Fatalf("manifest counts = %+v", bundle.Manifest.Counts)
	}

	targetStore, err := newServiceTestSQLiteStore(t, filepath.Join(t.TempDir(), "traffic-target"), "target-local")
	if err != nil {
		t.Fatalf("NewSQLiteStore(target) error = %v", err)
	}
	defer targetStore.Close()
	if err := targetStore.SaveAgent(ctx, storage.AgentRow{
		ID:         "target-edge",
		Name:       "edge-traffic",
		AgentToken: "target-token",
	}); err != nil {
		t.Fatalf("SaveAgent(target existing) error = %v", err)
	}
	result, err := NewBackupService(config.Config{EnableLocalAgent: true, LocalAgentID: "target-local"}, targetStore).Import(ctx, archive)
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	if result.Summary.Imported.TrafficPolicies != 1 || result.Summary.Imported.TrafficBaselines != 1 {
		t.Fatalf("import summary = %+v", result.Summary)
	}
	policies, err := targetStore.ListTrafficPolicies(ctx)
	if err != nil {
		t.Fatalf("ListTrafficPolicies() error = %v", err)
	}
	if len(policies) != 1 || policies[0].AgentID != "target-edge" || policies[0].Direction != "rx" {
		t.Fatalf("imported policies = %+v", policies)
	}
	baselines, err := targetStore.ListTrafficBaselines(ctx)
	if err != nil {
		t.Fatalf("ListTrafficBaselines() error = %v", err)
	}
	if len(baselines) != 1 || baselines[0].AgentID != "target-edge" || baselines[0].CycleStart != "2026-05-15T00:00:00Z" {
		t.Fatalf("imported baselines = %+v", baselines)
	}
}

func TestBackupServiceImportsLegacyArchiveWithoutTrafficFiles(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	targetStore, err := newServiceTestSQLiteStore(t, filepath.Join(t.TempDir(), "legacy-no-traffic"), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore(target) error = %v", err)
	}
	defer targetStore.Close()

	bundle := BackupBundle{
		Manifest: BackupManifest{
			PackageVersion:     BackupPackageVersion,
			SourceArchitecture: BackupSourceArchitectureGo,
			ExportedAt:         time.Date(2026, 4, 18, 9, 30, 0, 0, time.UTC),
			Counts:             BackupCounts{Agents: 1},
		},
		Agents: []BackupAgent{{
			ID:         "legacy-edge",
			Name:       "legacy-edge",
			AgentToken: "token-legacy-edge",
		}},
	}
	archive, err := encodeBackupBundleWithoutTrafficFiles(bundle)
	if err != nil {
		t.Fatalf("encodeBackupBundleWithoutTrafficFiles() error = %v", err)
	}
	decoded, err := decodeBackupBundle(archive)
	if err != nil {
		t.Fatalf("decodeBackupBundle() error = %v", err)
	}
	if len(decoded.TrafficPolicies) != 0 || len(decoded.TrafficBaselines) != 0 {
		t.Fatalf("decoded traffic payloads = policies %+v baselines %+v", decoded.TrafficPolicies, decoded.TrafficBaselines)
	}
	if decoded.Manifest.Counts.TrafficPolicies != 0 || decoded.Manifest.Counts.TrafficBaselines != 0 {
		t.Fatalf("decoded manifest counts = %+v", decoded.Manifest.Counts)
	}

	result, err := NewBackupService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, targetStore).Import(ctx, archive)
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	if result.Summary.Imported.Agents != 1 || result.Summary.Imported.TrafficPolicies != 0 || result.Summary.Imported.TrafficBaselines != 0 {
		t.Fatalf("import summary = %+v", result.Summary)
	}
}

func TestBackupServicePreviewAndImportSkipUnsupportedLegacyResources(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	retiredMode := "wire" + "guard"
	directProfileID := 10
	bundle := BackupBundle{
		Manifest: BackupManifest{
			PackageVersion:     BackupPackageVersion,
			SourceArchitecture: BackupSourceArchitectureGo,
			ExportedAt:         time.Date(2026, 4, 18, 9, 30, 0, 0, time.UTC),
		},
		Agents: []BackupAgent{{
			ID:           "legacy-edge",
			Name:         "legacy-edge",
			AgentToken:   "token-legacy-edge",
			Capabilities: []string{"http_rules", "l4", "egress_profiles", retiredMode},
		}},
		EgressProfiles: []BackupEgressProfile{
			{ID: directProfileID, Name: "direct", Type: "direct", Enabled: true},
			{ID: 11, Name: "retired", Type: retiredMode, Enabled: true},
		},
		RelayListeners: []BackupRelayListener{{
			ID:            20,
			AgentID:       "legacy-edge",
			Name:          "retired",
			ListenHost:    "0.0.0.0",
			BindHosts:     []string{"0.0.0.0"},
			ListenPort:    7443,
			PublicHost:    "relay.example.com",
			PublicPort:    7443,
			TransportMode: retiredMode,
		}},
		L4Rules: []BackupL4Rule{
			{
				ID:              30,
				AgentID:         "legacy-edge",
				Name:            "current",
				Protocol:        "tcp",
				ListenHost:      "0.0.0.0",
				ListenPort:      9000,
				Backends:        []L4Backend{{Host: "127.0.0.1", Port: 9001}},
				ListenMode:      "tcp",
				EgressProfileID: &directProfileID,
				Enabled:         true,
			},
			{
				ID:         31,
				AgentID:    "legacy-edge",
				Name:       "retired",
				Protocol:   "tcp",
				ListenHost: "0.0.0.0",
				ListenPort: 9002,
				Backends:   []L4Backend{{Host: "127.0.0.1", Port: 9003}},
				ListenMode: retiredMode,
				Enabled:    true,
			},
		},
	}
	httpRules := []map[string]any{{
		"id":                             40,
		"agent_id":                       "legacy-edge",
		"frontend_url":                   "https://media.example.com",
		"backends":                       []map[string]any{{"url": "http://127.0.0.1:8096"}},
		"enabled":                        true,
		"pass_proxy_headers":             true,
		retiredMode + "_entry_enabled":   true,
		retiredMode + "_profile_id":      41,
		retiredMode + "_entry_listen_ip": "10.44.0.1",
	}}
	archive, err := encodeBackupBundleWithOverrides(t, bundle, map[string]any{
		backupHTTPRulesFile:            httpRules,
		retiredMode + "_profiles.json": []map[string]any{{"private_key": "retired-secret"}},
	})
	if err != nil {
		t.Fatalf("encodeBackupBundleWithOverrides() error = %v", err)
	}

	targetStore, err := newServiceTestSQLiteStore(t, filepath.Join(t.TempDir(), "target"), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore(target) error = %v", err)
	}
	defer targetStore.Close()

	svc := NewBackupService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, targetStore)
	preview, err := svc.Preview(ctx, archive)
	if err != nil {
		t.Fatalf("Preview() error = %v", err)
	}
	assertBackupUnsupportedLegacyResourceResult(t, preview)

	result, err := svc.Import(ctx, archive)
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	assertBackupUnsupportedLegacyResourceResult(t, result)

	egressRows, err := targetStore.ListEgressProfiles(ctx)
	if err != nil {
		t.Fatalf("ListEgressProfiles() error = %v", err)
	}
	if len(egressRows) != 1 || egressRows[0].Type != "direct" {
		t.Fatalf("egress profiles = %+v, want only direct profile", egressRows)
	}
	relayRows, err := targetStore.ListRelayListeners(ctx, "legacy-edge")
	if err != nil {
		t.Fatalf("ListRelayListeners() error = %v", err)
	}
	if len(relayRows) != 0 {
		t.Fatalf("relay listeners = %+v, want unsupported listener ignored", relayRows)
	}
	l4Rows, err := targetStore.ListL4Rules(ctx, "legacy-edge")
	if err != nil {
		t.Fatalf("ListL4Rules() error = %v", err)
	}
	if len(l4Rows) != 1 || l4Rows[0].ListenMode != "tcp" {
		t.Fatalf("L4 rules = %+v, want only current rule", l4Rows)
	}
	httpRows, err := targetStore.ListHTTPRules(ctx, "legacy-edge")
	if err != nil {
		t.Fatalf("ListHTTPRules() error = %v", err)
	}
	if len(httpRows) != 1 || httpRows[0].FrontendURL != "https://media.example.com" {
		t.Fatalf("HTTP rules = %+v, want ordinary fields imported", httpRows)
	}
	agents, err := targetStore.ListAgents(ctx)
	if err != nil {
		t.Fatalf("ListAgents() error = %v", err)
	}
	if len(agents) != 1 || strings.Contains(strings.ToLower(agents[0].CapabilitiesJSON), retiredMode) {
		t.Fatalf("agents = %+v, want retired capability ignored", agents)
	}
}

func assertBackupUnsupportedLegacyResourceResult(t *testing.T, result BackupImportResult) {
	t.Helper()
	if result.Summary.Imported.Agents != 1 || result.Summary.Imported.HTTPRules != 1 || result.Summary.Imported.L4Rules != 1 || result.Summary.Imported.EgressProfiles != 1 {
		t.Fatalf("imported summary = %+v", result.Summary.Imported)
	}
	if result.Summary.SkippedInvalid.L4Rules != 1 || result.Summary.SkippedInvalid.EgressProfiles != 1 || result.Summary.SkippedInvalid.RelayListeners != 1 {
		t.Fatalf("skipped invalid summary = %+v", result.Summary.SkippedInvalid)
	}
}

func TestBackupServiceImportPreservesExistingUnsupportedRelayRows(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	retiredMode := "wire" + "guard"
	targetStore, err := newServiceTestSQLiteStore(t, filepath.Join(t.TempDir(), "target"), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore(target) error = %v", err)
	}
	defer targetStore.Close()

	if err := targetStore.SaveAgent(ctx, storage.AgentRow{
		ID:         "edge-existing",
		Name:       "edge-existing",
		AgentToken: "token-edge-existing",
	}); err != nil {
		t.Fatalf("SaveAgent() error = %v", err)
	}
	retiredRow := storage.RelayListenerRow{
		ID:            71,
		AgentID:       "edge-existing",
		Name:          "retired-existing",
		BindHostsJSON: `["0.0.0.0"]`,
		ListenHost:    "0.0.0.0",
		ListenPort:    7443,
		PublicHost:    "retired.example.com",
		PublicPort:    7443,
		TransportMode: retiredMode,
		TLSMode:       "passthrough",
		ObfsMode:      "off",
		PinSetJSON:    `[]`,
		TagsJSON:      `[]`,
		Revision:      5,
	}
	if err := targetStore.SaveRelayListeners(ctx, "edge-existing", []storage.RelayListenerRow{retiredRow}); err != nil {
		t.Fatalf("SaveRelayListeners(seed) error = %v", err)
	}

	bundle := BackupBundle{
		Manifest: BackupManifest{
			PackageVersion:     BackupPackageVersion,
			SourceArchitecture: BackupSourceArchitectureGo,
			ExportedAt:         time.Date(2026, 4, 18, 9, 30, 0, 0, time.UTC),
		},
		RelayListeners: []BackupRelayListener{{
			ID:            72,
			AgentID:       "edge-existing",
			Name:          "current-import",
			ListenHost:    "0.0.0.0",
			BindHosts:     []string{"0.0.0.0"},
			ListenPort:    8443,
			PublicHost:    "current.example.com",
			PublicPort:    8443,
			TransportMode: "tls_tcp",
			TLSMode:       "pin_only",
			ObfsMode:      "off",
			PinSet:        []RelayPin{{Type: "spki_sha256", Value: "fixture-pin"}},
		}},
	}
	archive, err := encodeBackupBundle(bundle)
	if err != nil {
		t.Fatalf("encodeBackupBundle() error = %v", err)
	}

	result, err := NewBackupService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, targetStore).Import(ctx, archive)
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	if result.Summary.Imported.RelayListeners != 1 {
		t.Fatalf("imported relay listeners = %d, want 1; report = %+v", result.Summary.Imported.RelayListeners, result.Report)
	}
	rows, err := targetStore.ListRelayListeners(ctx, "edge-existing")
	if err != nil {
		t.Fatalf("ListRelayListeners() error = %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("relay listeners = %+v, want preserved existing row plus imported row", rows)
	}
	foundRetired := false
	for _, row := range rows {
		if row.ID == retiredRow.ID {
			foundRetired = row.TransportMode == retiredMode && row.Name == retiredRow.Name
		}
	}
	if !foundRetired {
		t.Fatalf("existing unsupported relay row was not preserved: %+v", rows)
	}
}

func TestBackupServiceCanonicalizesLegacyRuleFieldsOnPreviewAndImport(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	cfg := config.Config{EnableLocalAgent: true, LocalAgentID: "local"}
	bundle := BackupBundle{
		Manifest: BackupManifest{
			PackageVersion:     BackupPackageVersion,
			SourceArchitecture: BackupSourceArchitectureMain,
			ExportedAt:         time.Date(2026, 4, 18, 9, 30, 0, 0, time.UTC),
			Counts: BackupCounts{
				Agents:         1,
				HTTPRules:      2,
				L4Rules:        2,
				RelayListeners: 1,
				Certificates:   1,
			},
		},
		Agents: []BackupAgent{{
			ID:           "edge-legacy",
			Name:         "edge-legacy",
			AgentToken:   "token-edge-legacy",
			Capabilities: []string{"http_rules", "l4", "cert_install"},
		}},
		Certificates: []BackupCertificate{{
			ID:              21,
			Domain:          "relay.example.com",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "local_http01",
			TargetAgentIDs:  []string{"edge-legacy"},
			Status:          "pending",
			AgentReports:    map[string]ManagedCertificateAgentReport{},
			ACMEInfo:        ManagedCertificateACMEInfo{},
			Usage:           "relay_tunnel",
			CertificateType: "acme",
		}},
		RelayListeners: []BackupRelayListener{{
			ID:                      31,
			AgentID:                 "edge-legacy",
			Name:                    "relay-legacy",
			ListenHost:              "127.0.0.1",
			BindHosts:               []string{"127.0.0.1"},
			ListenPort:              7443,
			PublicHost:              "relay.example.com",
			PublicPort:              7443,
			Enabled:                 true,
			CertificateID:           backupIntPtr(21),
			TLSMode:                 "pin_only",
			TransportMode:           "tls_tcp",
			ObfsMode:                "off",
			PinSet:                  []RelayPin{{Type: "spki_sha256", Value: "fixture-pin"}},
			TrustedCACertificateIDs: []int{},
		}},
		HTTPRules: []BackupHTTPRule{{
			ID:               41,
			AgentID:          "edge-legacy",
			FrontendURL:      "https://legacy-backend.example.com",
			BackendURL:       "http://127.0.0.1:8096",
			Enabled:          true,
			ProxyRedirect:    true,
			PassProxyHeaders: defaultPassProxyHeaders(),
		}, {
			ID:               42,
			AgentID:          "edge-legacy",
			FrontendURL:      "https://legacy-relay.example.com",
			Backends:         []HTTPRuleBackend{{URL: "http://127.0.0.1:8097"}},
			Enabled:          true,
			ProxyRedirect:    true,
			RelayChain:       []int{31},
			PassProxyHeaders: defaultPassProxyHeaders(),
		}},
		L4Rules: []BackupL4Rule{{
			ID:           51,
			AgentID:      "edge-legacy",
			Name:         "legacy upstream",
			Protocol:     "tcp",
			ListenHost:   "0.0.0.0",
			ListenPort:   9000,
			UpstreamHost: "127.0.0.1",
			UpstreamPort: 9001,
			Enabled:      true,
		}, {
			ID:         52,
			AgentID:    "edge-legacy",
			Name:       "legacy relay",
			Protocol:   "tcp",
			ListenHost: "0.0.0.0",
			ListenPort: 9002,
			Backends:   []L4Backend{{Host: "127.0.0.1", Port: 9003}},
			RelayChain: []int{31},
			Enabled:    true,
		}},
	}
	archive, err := encodeBackupBundle(bundle)
	if err != nil {
		t.Fatalf("encodeBackupBundle() error = %v", err)
	}

	previewStore, err := newServiceTestSQLiteStore(t, filepath.Join(t.TempDir(), "preview"), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore(preview) error = %v", err)
	}
	defer previewStore.Close()
	preview, err := NewBackupService(cfg, previewStore).Preview(ctx, archive)
	if err != nil {
		t.Fatalf("Preview() error = %v", err)
	}
	if preview.Summary.Imported.HTTPRules != 2 || preview.Summary.Imported.L4Rules != 2 || preview.Summary.Imported.RelayListeners != 1 {
		t.Fatalf("preview imported summary = %+v", preview.Summary.Imported)
	}
	if preview.Summary.SkippedInvalid.HTTPRules != 0 || preview.Summary.SkippedInvalid.L4Rules != 0 {
		t.Fatalf("preview invalid summary = %+v", preview.Summary.SkippedInvalid)
	}

	importStore, err := newServiceTestSQLiteStore(t, filepath.Join(t.TempDir(), "import"), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore(import) error = %v", err)
	}
	defer importStore.Close()
	result, err := NewBackupService(cfg, importStore).Import(ctx, archive)
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	if result.Summary.Imported.HTTPRules != 2 || result.Summary.Imported.L4Rules != 2 || result.Summary.SkippedInvalid.HTTPRules != 0 || result.Summary.SkippedInvalid.L4Rules != 0 {
		t.Fatalf("import summary = %+v", result.Summary)
	}

	httpRows, err := importStore.ListHTTPRules(ctx, "edge-legacy")
	if err != nil {
		t.Fatalf("ListHTTPRules() error = %v", err)
	}
	if len(httpRows) != 2 {
		t.Fatalf("http rules len = %d, want 2: %+v", len(httpRows), httpRows)
	}
	httpByFrontend := map[string]storage.HTTPRuleRow{}
	for _, row := range httpRows {
		httpByFrontend[row.FrontendURL] = row
	}
	if got := httpByFrontend["https://legacy-backend.example.com"].BackendsJSON; got != `[{"url":"http://127.0.0.1:8096"}]` {
		t.Fatalf("legacy http backends = %s", got)
	}
	if got := httpByFrontend["https://legacy-backend.example.com"].RelayChainJSON; got != `[]` {
		t.Fatalf("legacy http relay_chain = %s", got)
	}
	if got := httpByFrontend["https://legacy-relay.example.com"].RelayLayersJSON; got != `[[31]]` {
		t.Fatalf("legacy http relay_layers = %s", got)
	}
	if got := httpByFrontend["https://legacy-relay.example.com"].RelayChainJSON; got != `[]` {
		t.Fatalf("legacy relay http relay_chain = %s", got)
	}

	l4Rows, err := importStore.ListL4Rules(ctx, "edge-legacy")
	if err != nil {
		t.Fatalf("ListL4Rules() error = %v", err)
	}
	if len(l4Rows) != 2 {
		t.Fatalf("l4 rules len = %d, want 2: %+v", len(l4Rows), l4Rows)
	}
	l4ByPort := map[int]storage.L4RuleRow{}
	for _, row := range l4Rows {
		l4ByPort[row.ListenPort] = row
	}
	if got := l4ByPort[9000].BackendsJSON; got != `[{"host":"127.0.0.1","port":9001}]` {
		t.Fatalf("legacy l4 backends = %s", got)
	}
	if got := l4ByPort[9000].RelayChainJSON; got != `[]` {
		t.Fatalf("legacy l4 relay_chain = %s", got)
	}
	if got := l4ByPort[9002].RelayLayersJSON; got != `[[31]]` {
		t.Fatalf("legacy relay l4 relay_layers = %s", got)
	}
	if got := l4ByPort[9002].RelayChainJSON; got != `[]` {
		t.Fatalf("legacy relay l4 relay_chain = %s", got)
	}
}

func TestBackupServiceExportSkipsTrafficTablesWhenDisabled(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := storage.NewStore(storage.StoreConfig{
		Driver:              "sqlite",
		DataRoot:            filepath.Join(t.TempDir(), "disabled-traffic"),
		LocalAgentID:        "local",
		TrafficStatsEnabled: false,
	})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})
	if err := store.SaveAgent(ctx, storage.AgentRow{
		ID:         "edge-no-traffic",
		Name:       "edge-no-traffic",
		AgentToken: "token-no-traffic",
	}); err != nil {
		t.Fatalf("SaveAgent() error = %v", err)
	}

	archive, _, err := NewBackupService(config.Config{TrafficStatsEnabled: false}, store).Export(ctx)
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}
	bundle, err := decodeBackupBundle(archive)
	if err != nil {
		t.Fatalf("decodeBackupBundle() error = %v", err)
	}
	if len(bundle.TrafficPolicies) != 0 || len(bundle.TrafficBaselines) != 0 {
		t.Fatalf("traffic payloads = policies %+v baselines %+v, want empty", bundle.TrafficPolicies, bundle.TrafficBaselines)
	}
	if bundle.Manifest.Counts.TrafficPolicies != 0 || bundle.Manifest.Counts.TrafficBaselines != 0 {
		t.Fatalf("traffic counts = %+v, want zero traffic counts", bundle.Manifest.Counts)
	}
}

func TestBackupServiceImportPreservesL4ProxyEntryFields(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	cfg := config.Config{EnableLocalAgent: true, LocalAgentID: "local"}
	targetStore, err := newServiceTestSQLiteStore(t, filepath.Join(t.TempDir(), "target"), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore(target) error = %v", err)
	}
	defer targetStore.Close()

	bundle := BackupBundle{
		Manifest: BackupManifest{
			PackageVersion:     BackupPackageVersion,
			SourceArchitecture: BackupSourceArchitectureGo,
			ExportedAt:         time.Date(2026, 4, 18, 9, 30, 0, 0, time.UTC),
			Counts: BackupCounts{
				Agents:  1,
				L4Rules: 1,
			},
		},
		Agents: []BackupAgent{{
			ID:           "edge-proxy-entry",
			Name:         "edge-proxy-entry",
			AgentToken:   "token-proxy-entry",
			Capabilities: []string{"l4"},
		}},
		L4Rules: []BackupL4Rule{{
			ID:             45,
			AgentID:        "edge-proxy-entry",
			Name:           "proxy entry",
			Protocol:       "tcp",
			ListenHost:     "0.0.0.0",
			ListenPort:     1080,
			ListenMode:     "proxy",
			ProxyEntryAuth: L4ProxyEntryAuth{Enabled: true, Username: "client", Password: "secret"},
			Enabled:        true,
			Tags:           []string{"proxy-entry"},
		}},
	}
	archive, err := encodeBackupBundle(bundle)
	if err != nil {
		t.Fatalf("encodeBackupBundle() error = %v", err)
	}

	result, err := NewBackupService(cfg, targetStore).Import(ctx, archive)
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	if result.Summary.Imported.L4Rules != 1 || result.Summary.SkippedInvalid.L4Rules != 0 {
		t.Fatalf("import summary = %+v", result.Summary)
	}

	rows, err := targetStore.ListL4Rules(ctx, "edge-proxy-entry")
	if err != nil {
		t.Fatalf("ListL4Rules() error = %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("l4 rules len = %d, want 1: %+v", len(rows), rows)
	}
	row := rows[0]
	if row.ListenMode != "proxy" {
		t.Fatalf("ListenMode = %q", row.ListenMode)
	}
	var auth L4ProxyEntryAuth
	if err := json.Unmarshal([]byte(row.ProxyEntryAuthJSON), &auth); err != nil {
		t.Fatalf("unmarshal ProxyEntryAuthJSON: %v", err)
	}
	if !auth.Enabled || auth.Username != "client" || auth.Password != "secret" {
		t.Fatalf("ProxyEntryAuth = %+v", auth)
	}
}

func TestBackupServicePreviewAndImportUseNormalizedL4ListenHostConflictKeys(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	targetStore, err := newServiceTestSQLiteStore(t, filepath.Join(t.TempDir(), "l4-normalized-listen-host-conflict-target"), "target-local")
	if err != nil {
		t.Fatalf("NewSQLiteStore(target) error = %v", err)
	}
	defer targetStore.Close()

	bundle := BackupBundle{
		Manifest: BackupManifest{
			PackageVersion:     BackupPackageVersion,
			SourceArchitecture: BackupSourceArchitectureGo,
			ExportedAt:         time.Date(2026, 5, 14, 0, 0, 0, 0, time.UTC),
		},
		Agents: []BackupAgent{{
			ID:           "edge-l4",
			Name:         "edge-l4",
			AgentToken:   "token-edge-l4",
			Capabilities: []string{"l4"},
		}},
		L4Rules: []BackupL4Rule{
			{
				ID:            91,
				AgentID:       "edge-l4",
				Name:          "legacy empty listen host",
				Protocol:      "tcp",
				ListenHost:    "",
				ListenPort:    8443,
				Backends:      []L4Backend{{Host: "127.0.0.1", Port: 9443}},
				LoadBalancing: L4LoadBalancing{Strategy: "round_robin"},
				Enabled:       true,
			},
			{
				ID:            92,
				AgentID:       "edge-l4",
				Name:          "explicit default listen host",
				Protocol:      "tcp",
				ListenHost:    "0.0.0.0",
				ListenPort:    8443,
				Backends:      []L4Backend{{Host: "127.0.0.1", Port: 9444}},
				LoadBalancing: L4LoadBalancing{Strategy: "round_robin"},
				Enabled:       true,
			},
		},
	}
	archive, err := encodeBackupBundle(bundle)
	if err != nil {
		t.Fatalf("encodeBackupBundle() error = %v", err)
	}

	svc := NewBackupService(config.Config{EnableLocalAgent: true, LocalAgentID: "target-local"}, targetStore)
	preview, err := svc.Preview(ctx, archive)
	if err != nil {
		t.Fatalf("Preview() error = %v", err)
	}
	if preview.Summary.Imported.L4Rules != 1 || preview.Summary.SkippedConflict.L4Rules != 1 {
		t.Fatalf("preview summary = %+v", preview.Summary)
	}

	result, err := svc.Import(ctx, archive)
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	if result.Summary.Imported.L4Rules != preview.Summary.Imported.L4Rules || result.Summary.SkippedConflict.L4Rules != preview.Summary.SkippedConflict.L4Rules {
		t.Fatalf("import summary = %+v, want preview counts %+v", result.Summary, preview.Summary)
	}
	l4Rules, err := targetStore.ListL4Rules(ctx, "edge-l4")
	if err != nil {
		t.Fatalf("ListL4Rules(edge-l4) error = %v", err)
	}
	if len(l4Rules) != 1 {
		t.Fatalf("imported l4 rules = %+v, want exactly one", l4Rules)
	}
	if l4Rules[0].ListenHost != "0.0.0.0" {
		t.Fatalf("imported ListenHost = %q, want normalized 0.0.0.0", l4Rules[0].ListenHost)
	}
}

func TestBackupServiceImportSkipsRulesWithMissingRelayLayerDependencies(t *testing.T) {
	t.Parallel()
	targetStore, err := newServiceTestSQLiteStore(t, filepath.Join(t.TempDir(), "target"), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore(target) error = %v", err)
	}
	defer targetStore.Close()

	ctx := t.Context()
	bundle := BackupBundle{
		Manifest: BackupManifest{
			PackageVersion:     BackupPackageVersion,
			SourceArchitecture: BackupSourceArchitectureGo,
			ExportedAt:         time.Date(2026, 4, 18, 9, 30, 0, 0, time.UTC),
			Counts: BackupCounts{
				Agents:    1,
				HTTPRules: 1,
				L4Rules:   1,
			},
		},
		Agents: []BackupAgent{{
			ID:         "edge-a",
			Name:       "edge-a",
			AgentToken: "token-edge-a",
		}},
		HTTPRules: []BackupHTTPRule{{
			ID:               11,
			AgentID:          "edge-a",
			FrontendURL:      "https://missing-layer.example.com",
			BackendURL:       "http://127.0.0.1:8096",
			Backends:         []HTTPRuleBackend{{URL: "http://127.0.0.1:8096"}},
			Enabled:          true,
			RelayLayers:      [][]int{{999}},
			ProxyRedirect:    true,
			PassProxyHeaders: defaultPassProxyHeaders(),
		}},
		L4Rules: []BackupL4Rule{{
			ID:           12,
			AgentID:      "edge-a",
			Name:         "missing layer",
			Protocol:     "tcp",
			ListenHost:   "0.0.0.0",
			ListenPort:   9000,
			UpstreamHost: "127.0.0.1",
			UpstreamPort: 9001,
			Backends:     []L4Backend{{Host: "127.0.0.1", Port: 9001}},
			Enabled:      true,
			RelayLayers:  [][]int{{999}},
		}},
	}
	archive, err := encodeBackupBundle(bundle)
	if err != nil {
		t.Fatalf("encodeBackupBundle() error = %v", err)
	}

	result, err := NewBackupService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, targetStore).Import(ctx, archive)
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	if result.Summary.SkippedInvalid.HTTPRules != 1 || result.Summary.SkippedInvalid.L4Rules != 1 {
		t.Fatalf("import invalid summary = %+v", result.Summary.SkippedInvalid)
	}
	if result.Summary.Imported.HTTPRules != 0 || result.Summary.Imported.L4Rules != 0 {
		t.Fatalf("imported summary = %+v", result.Summary.Imported)
	}

	httpRules, err := targetStore.ListHTTPRules(ctx, "edge-a")
	if err != nil {
		t.Fatalf("ListHTTPRules() error = %v", err)
	}
	if len(httpRules) != 0 {
		t.Fatalf("expected no imported http rules, got %+v", httpRules)
	}
	l4Rules, err := targetStore.ListL4Rules(ctx, "edge-a")
	if err != nil {
		t.Fatalf("ListL4Rules() error = %v", err)
	}
	if len(l4Rules) != 0 {
		t.Fatalf("expected no imported l4 rules, got %+v", l4Rules)
	}
}

func TestBackupServicePreviewUsesExistingRelayListenerForConflictValidation(t *testing.T) {
	t.Parallel()
	const (
		ruleAgentID     = "edge-a"
		relayAgentID    = "relay-b"
		existingRelayID = 500
		conflictRelayID = 77
		freshRelayID    = 78
	)

	tests := []struct {
		name                      string
		existingTransport         string
		incomingConflictTransport string
		wantImportedRules         int
		wantSkippedInvalidRules   int
		wantInvalidReason         string
	}{
		{
			name:                      "stored cross-agent quic conflict allows imported rules",
			existingTransport:         "quic",
			incomingConflictTransport: "tls_tcp",
			wantImportedRules:         2,
			wantSkippedInvalidRules:   0,
		},
		{
			name:                      "stored tls conflict allows even when incoming conflict is cross-agent quic",
			existingTransport:         "tls_tcp",
			incomingConflictTransport: "quic",
			wantImportedRules:         2,
			wantSkippedInvalidRules:   0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			targetStore, err := newServiceTestSQLiteStore(t, filepath.Join(t.TempDir(), "target"), "local")
			if err != nil {
				t.Fatalf("NewSQLiteStore(target) error = %v", err)
			}
			defer targetStore.Close()

			ctx := t.Context()
			for _, agent := range []storage.AgentRow{
				{ID: ruleAgentID, Name: ruleAgentID, AgentToken: "token-edge"},
				{ID: relayAgentID, Name: relayAgentID, AgentToken: "token-relay"},
			} {
				if err := targetStore.SaveAgent(ctx, agent); err != nil {
					t.Fatalf("SaveAgent(%s) error = %v", agent.ID, err)
				}
			}
			if err := targetStore.SaveManagedCertificates(ctx, []storage.ManagedCertificateRow{{
				ID:              21,
				Domain:          "relay.example.com",
				Enabled:         true,
				Scope:           "domain",
				IssuerMode:      "local_http01",
				TargetAgentIDs:  `["relay-b"]`,
				Status:          "active",
				AgentReports:    `{}`,
				ACMEInfo:        `{}`,
				Usage:           "relay_tunnel",
				CertificateType: "uploaded",
				Revision:        2,
			}}); err != nil {
				t.Fatalf("SaveManagedCertificates() error = %v", err)
			}
			if err := targetStore.SaveRelayListeners(ctx, relayAgentID, []storage.RelayListenerRow{{
				ID:            existingRelayID,
				AgentID:       relayAgentID,
				Name:          "relay-conflict",
				ListenHost:    "0.0.0.0",
				BindHostsJSON: `["0.0.0.0"]`,
				ListenPort:    7443,
				PublicHost:    "relay.example.com",
				PublicPort:    7443,
				Enabled:       true,
				TransportMode: tt.existingTransport,
				Revision:      3,
			}}); err != nil {
				t.Fatalf("SaveRelayListeners(existing) error = %v", err)
			}

			bundle := BackupBundle{
				Manifest: BackupManifest{
					PackageVersion:     BackupPackageVersion,
					SourceArchitecture: BackupSourceArchitectureGo,
					ExportedAt:         time.Date(2026, 4, 18, 9, 30, 0, 0, time.UTC),
					Counts: BackupCounts{
						HTTPRules:      2,
						L4Rules:        2,
						RelayListeners: 2,
					},
				},
				RelayListeners: []BackupRelayListener{
					{
						ID:            conflictRelayID,
						AgentID:       relayAgentID,
						Name:          "relay-conflict",
						BindHosts:     []string{"0.0.0.0"},
						ListenHost:    "0.0.0.0",
						ListenPort:    7443,
						PublicHost:    "relay.example.com",
						PublicPort:    7443,
						Enabled:       true,
						TransportMode: tt.incomingConflictTransport,
						Revision:      9,
					},
					{
						ID:            freshRelayID,
						AgentID:       relayAgentID,
						Name:          "relay-fresh",
						BindHosts:     []string{"0.0.0.0"},
						ListenHost:    "0.0.0.0",
						ListenPort:    7444,
						PublicHost:    "fresh-relay.example.com",
						PublicPort:    7444,
						Enabled:       true,
						CertificateID: backupIntPtr(21),
						TLSMode:       "pin_only",
						TransportMode: "tls_tcp",
						PinSet:        []RelayPin{{Type: "spki_sha256", Value: "fixture-pin"}},
						Revision:      10,
					},
				},
				HTTPRules: []BackupHTTPRule{
					{
						ID:               11,
						AgentID:          ruleAgentID,
						FrontendURL:      "https://conflict-relay.example.com",
						BackendURL:       "http://127.0.0.1:8096",
						Backends:         []HTTPRuleBackend{{URL: "http://127.0.0.1:8096"}},
						Enabled:          true,
						RelayLayers:      [][]int{{conflictRelayID}},
						ProxyRedirect:    true,
						PassProxyHeaders: defaultPassProxyHeaders(),
					},
					{
						ID:               12,
						AgentID:          ruleAgentID,
						FrontendURL:      "https://fresh-relay.example.com",
						BackendURL:       "http://127.0.0.1:8097",
						Backends:         []HTTPRuleBackend{{URL: "http://127.0.0.1:8097"}},
						Enabled:          true,
						RelayLayers:      [][]int{{freshRelayID}},
						ProxyRedirect:    true,
						PassProxyHeaders: defaultPassProxyHeaders(),
					},
				},
				L4Rules: []BackupL4Rule{
					{
						ID:           21,
						AgentID:      ruleAgentID,
						Name:         "conflict relay",
						Protocol:     "tcp",
						ListenHost:   "0.0.0.0",
						ListenPort:   9000,
						UpstreamHost: "127.0.0.1",
						UpstreamPort: 9001,
						Backends:     []L4Backend{{Host: "127.0.0.1", Port: 9001}},
						Enabled:      true,
						RelayLayers:  [][]int{{conflictRelayID}},
					},
					{
						ID:           22,
						AgentID:      ruleAgentID,
						Name:         "fresh relay",
						Protocol:     "tcp",
						ListenHost:   "0.0.0.0",
						ListenPort:   9002,
						UpstreamHost: "127.0.0.1",
						UpstreamPort: 9003,
						Backends:     []L4Backend{{Host: "127.0.0.1", Port: 9003}},
						Enabled:      true,
						RelayLayers:  [][]int{{freshRelayID}},
					},
				},
			}
			archive, err := encodeBackupBundle(bundle)
			if err != nil {
				t.Fatalf("encodeBackupBundle() error = %v", err)
			}

			svc := NewBackupService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, targetStore)
			preview, err := svc.Preview(ctx, archive)
			if err != nil {
				t.Fatalf("Preview() error = %v", err)
			}
			assertBackupConflictRelayPreview(t, preview, tt.wantImportedRules, tt.wantSkippedInvalidRules, tt.wantInvalidReason)

			result, err := svc.Import(ctx, archive)
			if err != nil {
				t.Fatalf("Import() error = %v", err)
			}
			assertBackupConflictRelayPreview(t, result, tt.wantImportedRules, tt.wantSkippedInvalidRules, tt.wantInvalidReason)
		})
	}
}

func assertBackupConflictRelayPreview(t *testing.T, result BackupImportResult, wantImportedRules int, wantSkippedInvalidRules int, wantInvalidReason string) {
	t.Helper()
	if result.Summary.SkippedConflict.RelayListeners != 1 || result.Summary.Imported.RelayListeners != 1 {
		t.Fatalf("relay summary = imported %+v skipped conflict %+v", result.Summary.Imported, result.Summary.SkippedConflict)
	}
	if result.Summary.Imported.HTTPRules != wantImportedRules || result.Summary.Imported.L4Rules != wantImportedRules {
		t.Fatalf("imported rules = %+v, want HTTP/L4 %d", result.Summary.Imported, wantImportedRules)
	}
	if result.Summary.SkippedInvalid.HTTPRules != wantSkippedInvalidRules || result.Summary.SkippedInvalid.L4Rules != wantSkippedInvalidRules {
		t.Fatalf("invalid rules = %+v, want HTTP/L4 %d", result.Summary.SkippedInvalid, wantSkippedInvalidRules)
	}
	if wantInvalidReason == "" {
		return
	}
	assertBackupSkippedInvalidReason(t, result, "http_rule", "https://conflict-relay.example.com", wantInvalidReason)
	assertBackupSkippedInvalidReason(t, result, "l4_rule", "edge-a|tcp|0.0.0.0|9000|host", wantInvalidReason)
}

func TestBackupServicePreviewAllocatesRelayListenerIDWhenSourceIDCollidesWithExistingListener(t *testing.T) {
	t.Parallel()
	targetStore, err := newServiceTestSQLiteStore(t, filepath.Join(t.TempDir(), "target"), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore(target) error = %v", err)
	}
	defer targetStore.Close()

	ctx := t.Context()
	for _, agent := range []storage.AgentRow{
		{ID: "edge-a", Name: "edge-a", AgentToken: "token-edge"},
		{ID: "relay-a", Name: "relay-a", AgentToken: "token-relay-a"},
		{ID: "relay-existing", Name: "relay-existing", AgentToken: "token-relay-existing"},
	} {
		if err := targetStore.SaveAgent(ctx, agent); err != nil {
			t.Fatalf("SaveAgent(%s) error = %v", agent.ID, err)
		}
	}
	if err := targetStore.SaveManagedCertificates(ctx, []storage.ManagedCertificateRow{{
		ID:              21,
		Domain:          "incoming-relay.example.com",
		Enabled:         true,
		Scope:           "domain",
		IssuerMode:      "local_http01",
		TargetAgentIDs:  `["relay-a"]`,
		Status:          "active",
		AgentReports:    `{}`,
		ACMEInfo:        `{}`,
		Usage:           "relay_tunnel",
		CertificateType: "uploaded",
		Revision:        2,
	}}); err != nil {
		t.Fatalf("SaveManagedCertificates() error = %v", err)
	}
	if err := targetStore.SaveRelayListeners(ctx, "relay-existing", []storage.RelayListenerRow{{
		ID:            77,
		AgentID:       "relay-existing",
		Name:          "stored-id-collision",
		ListenHost:    "0.0.0.0",
		BindHostsJSON: `["0.0.0.0"]`,
		ListenPort:    7443,
		PublicHost:    "stored-relay.example.com",
		PublicPort:    7443,
		Enabled:       false,
		TransportMode: "tls_tcp",
		Revision:      3,
	}}); err != nil {
		t.Fatalf("SaveRelayListeners(existing) error = %v", err)
	}

	bundle := BackupBundle{
		Manifest: BackupManifest{
			PackageVersion:     BackupPackageVersion,
			SourceArchitecture: BackupSourceArchitectureGo,
			ExportedAt:         time.Date(2026, 4, 18, 9, 30, 0, 0, time.UTC),
			Counts: BackupCounts{
				Agents:         2,
				HTTPRules:      1,
				L4Rules:        1,
				RelayListeners: 1,
			},
		},
		Agents: []BackupAgent{
			{ID: "edge-a", Name: "edge-a", AgentToken: "token-edge"},
			{ID: "relay-a", Name: "relay-a", AgentToken: "token-relay-a"},
		},
		RelayListeners: []BackupRelayListener{{
			ID:            77,
			AgentID:       "relay-a",
			Name:          "incoming-relay",
			BindHosts:     []string{"0.0.0.0"},
			ListenHost:    "0.0.0.0",
			ListenPort:    7444,
			PublicHost:    "incoming-relay.example.com",
			PublicPort:    7444,
			Enabled:       true,
			CertificateID: backupIntPtr(21),
			TLSMode:       "pin_only",
			TransportMode: "tls_tcp",
			PinSet:        []RelayPin{{Type: "spki_sha256", Value: "fixture-pin"}},
			Revision:      5,
		}},
		HTTPRules: []BackupHTTPRule{{
			ID:               11,
			AgentID:          "edge-a",
			FrontendURL:      "https://id-collision.example.com",
			BackendURL:       "http://127.0.0.1:8096",
			Backends:         []HTTPRuleBackend{{URL: "http://127.0.0.1:8096"}},
			Enabled:          true,
			RelayLayers:      [][]int{{77}},
			ProxyRedirect:    true,
			PassProxyHeaders: defaultPassProxyHeaders(),
		}},
		L4Rules: []BackupL4Rule{{
			ID:           12,
			AgentID:      "edge-a",
			Name:         "id collision",
			Protocol:     "tcp",
			ListenHost:   "0.0.0.0",
			ListenPort:   9000,
			UpstreamHost: "127.0.0.1",
			UpstreamPort: 9001,
			Backends:     []L4Backend{{Host: "127.0.0.1", Port: 9001}},
			Enabled:      true,
			RelayLayers:  [][]int{{77}},
		}},
	}
	archive, err := encodeBackupBundle(bundle)
	if err != nil {
		t.Fatalf("encodeBackupBundle() error = %v", err)
	}

	svc := NewBackupService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, targetStore)
	preview, err := svc.Preview(ctx, archive)
	if err != nil {
		t.Fatalf("Preview() error = %v", err)
	}
	assertBackupRelayIDCollisionResult(t, preview)

	result, err := svc.Import(ctx, archive)
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	assertBackupRelayIDCollisionResult(t, result)

	listeners, err := targetStore.ListRelayListeners(ctx, "relay-a")
	if err != nil {
		t.Fatalf("ListRelayListeners(relay-a) error = %v", err)
	}
	if len(listeners) != 1 {
		t.Fatalf("imported relay listeners = %+v, want one", listeners)
	}
	if listeners[0].ID == 77 || listeners[0].ID <= 0 {
		t.Fatalf("imported relay listener id = %d, want remapped away from existing id 77", listeners[0].ID)
	}
	httpRules, err := targetStore.ListHTTPRules(ctx, "edge-a")
	if err != nil {
		t.Fatalf("ListHTTPRules(edge-a) error = %v", err)
	}
	if len(httpRules) != 1 || httpRules[0].RelayLayersJSON != fmt.Sprintf("[[%d]]", listeners[0].ID) {
		t.Fatalf("imported HTTP rules = %+v, want relay layer remapped to %d", httpRules, listeners[0].ID)
	}
}

func assertBackupRelayIDCollisionResult(t *testing.T, result BackupImportResult) {
	t.Helper()
	if result.Summary.Imported.RelayListeners != 1 || result.Summary.SkippedConflict.RelayListeners != 0 {
		t.Fatalf("relay summary = imported %+v skipped conflict %+v skipped invalid %+v report %+v", result.Summary.Imported, result.Summary.SkippedConflict, result.Summary.SkippedInvalid, result.Report)
	}
	if result.Summary.Imported.HTTPRules != 1 || result.Summary.Imported.L4Rules != 1 {
		t.Fatalf("imported rules = %+v, want one HTTP and one L4", result.Summary.Imported)
	}
	if result.Summary.SkippedInvalid.HTTPRules != 0 || result.Summary.SkippedInvalid.L4Rules != 0 {
		t.Fatalf("invalid rules = %+v, want none", result.Summary.SkippedInvalid)
	}
}

func TestBackupServicePreviewMapsDuplicateIncomingRelayListenerToFirstImportable(t *testing.T) {
	t.Parallel()
	targetStore, err := newServiceTestSQLiteStore(t, filepath.Join(t.TempDir(), "target"), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore(target) error = %v", err)
	}
	defer targetStore.Close()

	ctx := t.Context()
	for _, agent := range []storage.AgentRow{
		{ID: "edge-a", Name: "edge-a", AgentToken: "token-edge"},
		{ID: "relay-live", Name: "relay-live", AgentToken: "token-relay"},
	} {
		if err := targetStore.SaveAgent(ctx, agent); err != nil {
			t.Fatalf("SaveAgent(%s) error = %v", agent.ID, err)
		}
	}

	const (
		firstRelayID = 77
		laterRelayID = 78
	)
	bundle := BackupBundle{
		Manifest: BackupManifest{
			PackageVersion:     BackupPackageVersion,
			SourceArchitecture: BackupSourceArchitectureGo,
			ExportedAt:         time.Date(2026, 4, 18, 9, 30, 0, 0, time.UTC),
			Counts: BackupCounts{
				Agents:         2,
				HTTPRules:      1,
				L4Rules:        1,
				RelayListeners: 2,
			},
		},
		Agents: []BackupAgent{
			{ID: "relay-first", Name: "relay-live", AgentToken: "token-first"},
			{ID: "relay-later", Name: "relay-live", AgentToken: "token-later"},
		},
		RelayListeners: []BackupRelayListener{
			{
				ID:            firstRelayID,
				AgentID:       "relay-first",
				Name:          "shared-relay",
				BindHosts:     []string{"0.0.0.0"},
				ListenHost:    "0.0.0.0",
				ListenPort:    7443,
				PublicHost:    "relay.example.com",
				PublicPort:    7443,
				Enabled:       false,
				TransportMode: "tls_tcp",
				Revision:      5,
			},
			{
				ID:            laterRelayID,
				AgentID:       "relay-later",
				Name:          "shared-relay",
				BindHosts:     []string{"0.0.0.0"},
				ListenHost:    "0.0.0.0",
				ListenPort:    7444,
				PublicHost:    "relay-later.example.com",
				PublicPort:    7444,
				Enabled:       true,
				TransportMode: "tls_tcp",
				Revision:      6,
			},
		},
		HTTPRules: []BackupHTTPRule{{
			ID:               11,
			AgentID:          "edge-a",
			FrontendURL:      "https://duplicate-relay.example.com",
			BackendURL:       "http://127.0.0.1:8096",
			Backends:         []HTTPRuleBackend{{URL: "http://127.0.0.1:8096"}},
			Enabled:          true,
			RelayLayers:      [][]int{{laterRelayID}},
			ProxyRedirect:    true,
			PassProxyHeaders: defaultPassProxyHeaders(),
		}},
		L4Rules: []BackupL4Rule{{
			ID:           12,
			AgentID:      "edge-a",
			Name:         "duplicate relay",
			Protocol:     "tcp",
			ListenHost:   "0.0.0.0",
			ListenPort:   9000,
			UpstreamHost: "127.0.0.1",
			UpstreamPort: 9001,
			Backends:     []L4Backend{{Host: "127.0.0.1", Port: 9001}},
			Enabled:      true,
			RelayLayers:  [][]int{{laterRelayID}},
		}},
	}
	archive, err := encodeBackupBundle(bundle)
	if err != nil {
		t.Fatalf("encodeBackupBundle() error = %v", err)
	}

	svc := NewBackupService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, targetStore)
	preview, err := svc.Preview(ctx, archive)
	if err != nil {
		t.Fatalf("Preview() error = %v", err)
	}
	assertBackupDuplicateIncomingRelayResult(t, preview)

	result, err := svc.Import(ctx, archive)
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	assertBackupDuplicateIncomingRelayResult(t, result)

	listeners, err := targetStore.ListRelayListeners(ctx, "relay-live")
	if err != nil {
		t.Fatalf("ListRelayListeners(relay-live) error = %v", err)
	}
	if len(listeners) != 1 {
		t.Fatalf("imported relay listeners = %+v, want one first listener", listeners)
	}
	if listeners[0].ID != firstRelayID || listeners[0].Enabled || listeners[0].TransportMode != "tls_tcp" || listeners[0].AgentID != "relay-live" {
		t.Fatalf("imported relay listener = %+v, want first disabled TLS listener on resolved agent", listeners[0])
	}
}

func assertBackupDuplicateIncomingRelayResult(t *testing.T, result BackupImportResult) {
	t.Helper()
	if result.Summary.Imported.RelayListeners != 1 || result.Summary.SkippedConflict.RelayListeners != 1 {
		t.Fatalf("relay summary = imported %+v skipped conflict %+v", result.Summary.Imported, result.Summary.SkippedConflict)
	}
	if result.Summary.Imported.HTTPRules != 0 || result.Summary.Imported.L4Rules != 0 {
		t.Fatalf("imported rules = %+v, want none", result.Summary.Imported)
	}
	if result.Summary.SkippedInvalid.HTTPRules != 1 || result.Summary.SkippedInvalid.L4Rules != 1 {
		t.Fatalf("invalid rules = %+v, want one HTTP and one L4", result.Summary.SkippedInvalid)
	}
	assertBackupSkippedInvalidReason(t, result, "http_rule", "https://duplicate-relay.example.com", "invalid argument: relay listener is disabled: 77")
	assertBackupSkippedInvalidReason(t, result, "l4_rule", "edge-a|tcp|0.0.0.0|9000|host", "invalid argument: relay listener is disabled: 77")
}

func TestBackupServicePreviewAndImportRejectRelayListenerBindDuplicateAfterNormalization(t *testing.T) {
	t.Parallel()
	targetStore, err := newServiceTestSQLiteStore(t, filepath.Join(t.TempDir(), "target"), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore(target) error = %v", err)
	}
	defer targetStore.Close()

	ctx := t.Context()
	if err := targetStore.SaveAgent(ctx, storage.AgentRow{ID: "relay-a", Name: "relay-a", AgentToken: "token-relay", CapabilitiesJSON: `["cert_install"]`}); err != nil {
		t.Fatalf("SaveAgent(relay-a) error = %v", err)
	}

	bundle := BackupBundle{
		Manifest: BackupManifest{
			PackageVersion:     BackupPackageVersion,
			SourceArchitecture: BackupSourceArchitectureGo,
			ExportedAt:         time.Date(2026, 4, 18, 9, 30, 0, 0, time.UTC),
			Counts: BackupCounts{
				RelayListeners: 2,
				Certificates:   1,
			},
		},
		Certificates: []BackupCertificate{{
			ID:              21,
			Domain:          "relay.example.com",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "local_http01",
			TargetAgentIDs:  []string{"relay-a"},
			Status:          "pending",
			AgentReports:    map[string]ManagedCertificateAgentReport{},
			ACMEInfo:        ManagedCertificateACMEInfo{},
			Usage:           "relay_tunnel",
			CertificateType: "acme",
		}},
		RelayListeners: []BackupRelayListener{
			{
				ID:            77,
				AgentID:       "relay-a",
				Name:          "relay-explicit-host",
				BindHosts:     []string{"0.0.0.0"},
				ListenHost:    "0.0.0.0",
				ListenPort:    7443,
				PublicHost:    "relay.example.com",
				PublicPort:    7443,
				Enabled:       true,
				CertificateID: backupIntPtr(21),
				TLSMode:       "pin_only",
				TransportMode: "tls_tcp",
				PinSet:        []RelayPin{{Type: "spki_sha256", Value: "fixture-pin"}},
				Revision:      5,
			},
			{
				ID:            78,
				AgentID:       "relay-a",
				Name:          "relay-default-host",
				ListenPort:    7443,
				PublicHost:    "relay-default.example.com",
				PublicPort:    7443,
				Enabled:       true,
				CertificateID: backupIntPtr(21),
				TLSMode:       "pin_only",
				PinSet:        []RelayPin{{Type: "spki_sha256", Value: "fixture-pin"}},
				Revision:      6,
			},
		},
	}
	archive, err := encodeBackupBundle(bundle)
	if err != nil {
		t.Fatalf("encodeBackupBundle() error = %v", err)
	}

	svc := NewBackupService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, targetStore)
	preview, err := svc.Preview(ctx, archive)
	if err != nil {
		t.Fatalf("Preview() error = %v", err)
	}
	assertBackupRelayBindDuplicateResult(t, preview, "relay-a|relay-default-host", 1)

	result, err := svc.Import(ctx, archive)
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	assertBackupRelayBindDuplicateResult(t, result, "relay-a|relay-default-host", 1)

	listeners, err := targetStore.ListRelayListeners(ctx, "relay-a")
	if err != nil {
		t.Fatalf("ListRelayListeners(relay-a) error = %v", err)
	}
	if len(listeners) != 1 {
		t.Fatalf("imported relay listeners = %+v, want only first listener", listeners)
	}
	if listeners[0].Name != "relay-explicit-host" || listeners[0].ListenHost != "0.0.0.0" || listeners[0].TransportMode != "tls_tcp" {
		t.Fatalf("imported relay listener = %+v, want normalized first listener", listeners[0])
	}
}

func TestBackupServicePreviewAndImportRejectRelayListenerBindConflictWithExisting(t *testing.T) {
	t.Parallel()
	targetStore, err := newServiceTestSQLiteStore(t, filepath.Join(t.TempDir(), "target"), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore(target) error = %v", err)
	}
	defer targetStore.Close()

	ctx := t.Context()
	if err := targetStore.SaveAgent(ctx, storage.AgentRow{ID: "relay-a", Name: "relay-a", AgentToken: "token-relay", CapabilitiesJSON: `["cert_install"]`}); err != nil {
		t.Fatalf("SaveAgent(relay-a) error = %v", err)
	}
	if err := targetStore.SaveRelayListeners(ctx, "relay-a", []storage.RelayListenerRow{{
		ID:            50,
		AgentID:       "relay-a",
		Name:          "existing-relay",
		ListenHost:    "0.0.0.0",
		BindHostsJSON: `["0.0.0.0"]`,
		ListenPort:    7443,
		PublicHost:    "existing.example.com",
		PublicPort:    7443,
		Enabled:       true,
		TransportMode: "tls_tcp",
		Revision:      2,
	}}); err != nil {
		t.Fatalf("SaveRelayListeners(existing) error = %v", err)
	}

	bundle := BackupBundle{
		Manifest: BackupManifest{
			PackageVersion:     BackupPackageVersion,
			SourceArchitecture: BackupSourceArchitectureGo,
			ExportedAt:         time.Date(2026, 4, 18, 9, 30, 0, 0, time.UTC),
			Counts: BackupCounts{
				RelayListeners: 1,
				Certificates:   1,
			},
		},
		Certificates: []BackupCertificate{{
			ID:              21,
			Domain:          "incoming.example.com",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "local_http01",
			TargetAgentIDs:  []string{"relay-a"},
			Status:          "pending",
			AgentReports:    map[string]ManagedCertificateAgentReport{},
			ACMEInfo:        ManagedCertificateACMEInfo{},
			Usage:           "relay_tunnel",
			CertificateType: "acme",
		}},
		RelayListeners: []BackupRelayListener{{
			ID:            77,
			AgentID:       "relay-a",
			Name:          "incoming-relay",
			BindHosts:     []string{"127.0.0.1"},
			ListenHost:    "127.0.0.1",
			ListenPort:    7443,
			PublicHost:    "incoming.example.com",
			PublicPort:    7443,
			Enabled:       true,
			CertificateID: backupIntPtr(21),
			TLSMode:       "pin_only",
			TransportMode: "tls_tcp",
			PinSet:        []RelayPin{{Type: "spki_sha256", Value: "fixture-pin"}},
			Revision:      5,
		}},
	}
	archive, err := encodeBackupBundle(bundle)
	if err != nil {
		t.Fatalf("encodeBackupBundle() error = %v", err)
	}

	svc := NewBackupService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, targetStore)
	preview, err := svc.Preview(ctx, archive)
	if err != nil {
		t.Fatalf("Preview() error = %v", err)
	}
	assertBackupRelayBindDuplicateResult(t, preview, "relay-a|incoming-relay", 0)

	result, err := svc.Import(ctx, archive)
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	assertBackupRelayBindDuplicateResult(t, result, "relay-a|incoming-relay", 0)

	listeners, err := targetStore.ListRelayListeners(ctx, "relay-a")
	if err != nil {
		t.Fatalf("ListRelayListeners(relay-a) error = %v", err)
	}
	if len(listeners) != 1 || listeners[0].Name != "existing-relay" {
		t.Fatalf("relay listeners after import = %+v, want only existing listener", listeners)
	}
}

func assertBackupRelayBindDuplicateResult(t *testing.T, result BackupImportResult, conflictKey string, wantImported int) {
	t.Helper()
	if result.Summary.Imported.RelayListeners != wantImported {
		t.Fatalf("relay imported summary = %+v, want %d; report = %+v", result.Summary.Imported, wantImported, result.Report)
	}
	if result.Summary.SkippedConflict.RelayListeners != 1 {
		t.Fatalf("relay skipped conflict summary = %+v, want one; report = %+v", result.Summary.SkippedConflict, result.Report)
	}
	for _, item := range result.Report.SkippedConflict {
		if item.Kind == "relay_listener" && item.Key == conflictKey {
			return
		}
	}
	t.Fatalf("missing relay_listener conflict key %q in %+v", conflictKey, result.Report.SkippedConflict)
}

type failingBackupStore struct {
	backupStore
	remainingVersionPolicyFailures int
	remainingHTTPRuleFailures      int
}

func (s *failingBackupStore) SaveVersionPolicies(ctx context.Context, rows []storage.VersionPolicyRow) error {
	if s.remainingVersionPolicyFailures > 0 {
		s.remainingVersionPolicyFailures--
		return errors.New("forced version policy failure")
	}
	return s.backupStore.SaveVersionPolicies(ctx, rows)
}

func (s *failingBackupStore) SaveHTTPRules(ctx context.Context, agentID string, rows []storage.HTTPRuleRow) error {
	if s.remainingHTTPRuleFailures > 0 {
		s.remainingHTTPRuleFailures--
		return errors.New("forced http rule failure")
	}
	return s.backupStore.SaveHTTPRules(ctx, agentID, rows)
}

type countingBackupStore struct {
	backupStore
	listAgentsCalls int
}

func (s *countingBackupStore) ListAgents(ctx context.Context) ([]storage.AgentRow, error) {
	s.listAgentsCalls++
	return s.backupStore.ListAgents(ctx)
}

func backupArchiveFileNames(t *testing.T, archive []byte) map[string]bool {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		t.Fatalf("gzip.NewReader() error = %v", err)
	}
	defer gz.Close()

	files := map[string]bool{}
	tr := tar.NewReader(gz)
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("tar.Next() error = %v", err)
		}
		files[header.Name] = true
	}
	return files
}

func backupArchiveJSONFile(t *testing.T, archive []byte, name string) []byte {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		t.Fatalf("gzip.NewReader() error = %v", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("tar.Next() error = %v", err)
		}
		content, err := io.ReadAll(tr)
		if err != nil {
			t.Fatalf("ReadAll(%s) error = %v", header.Name, err)
		}
		if header.Name == name {
			return content
		}
	}
	t.Fatalf("backup file %s not found", name)
	return nil
}

func encodeBackupBundleWithHTTPRules(t *testing.T, bundle BackupBundle, rules []map[string]any) ([]byte, error) {
	t.Helper()
	return encodeBackupBundleWithOverrides(t, bundle, map[string]any{
		backupHTTPRulesFile: rules,
	})
}

func encodeBackupBundleWithOverrides(t *testing.T, bundle BackupBundle, overrides map[string]any) ([]byte, error) {
	t.Helper()
	var buffer bytes.Buffer
	gz := gzip.NewWriter(&buffer)
	tw := tar.NewWriter(gz)
	files := []struct {
		name    string
		payload any
	}{
		{backupManifestFile, bundle.Manifest},
		{backupAgentsFile, bundle.Agents},
		{backupHTTPRulesFile, bundle.HTTPRules},
		{backupL4RulesFile, bundle.L4Rules},
		{backupEgressProfilesFile, bundle.EgressProfiles},
		{backupRelayListenersFile, bundle.RelayListeners},
		{backupCertificatesFile, bundle.Certificates},
		{backupVersionPoliciesFile, bundle.VersionPolicies},
		{backupTrafficPoliciesFile, bundle.TrafficPolicies},
		{backupTrafficBaselinesFile, bundle.TrafficBaselines},
	}
	written := map[string]struct{}{}
	for _, item := range files {
		payload := item.payload
		if override, ok := overrides[item.name]; ok {
			payload = override
		}
		if err := writeBackupJSONFile(tw, item.name, payload); err != nil {
			return nil, err
		}
		written[item.name] = struct{}{}
	}
	for name, payload := range overrides {
		if _, ok := written[name]; ok {
			continue
		}
		if err := writeBackupJSONFile(tw, name, payload); err != nil {
			return nil, err
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

func encodeBackupBundleWithoutTrafficFiles(bundle BackupBundle) ([]byte, error) {
	var buffer bytes.Buffer
	gz := gzip.NewWriter(&buffer)
	tw := tar.NewWriter(gz)
	for _, item := range []struct {
		name    string
		payload any
	}{
		{backupManifestFile, bundle.Manifest},
		{backupAgentsFile, bundle.Agents},
		{backupHTTPRulesFile, bundle.HTTPRules},
		{backupL4RulesFile, bundle.L4Rules},
		{backupRelayListenersFile, bundle.RelayListeners},
		{backupCertificatesFile, bundle.Certificates},
		{backupVersionPoliciesFile, bundle.VersionPolicies},
	} {
		if err := writeBackupJSONFile(tw, item.name, item.payload); err != nil {
			return nil, err
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

func TestBackupServiceRollbackOnImportFailure(t *testing.T) {
	t.Parallel()
	sourceStore, err := newServiceTestSQLiteStore(t, filepath.Join(t.TempDir(), "rollback-source"), "local")
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
	if err := sourceStore.SaveTrafficPolicy(ctx, storage.AgentTrafficPolicyRow{
		AgentID:       "edge-b",
		Direction:     "rx",
		CycleStartDay: 15,
	}); err != nil {
		t.Fatalf("SaveTrafficPolicy(source) error = %v", err)
	}
	if err := sourceStore.SaveTrafficBaseline(ctx, storage.AgentTrafficBaselineRow{
		AgentID:           "edge-b",
		CycleStart:        "2026-05-15T00:00:00Z",
		RawRXBytes:        1000,
		RawTXBytes:        2000,
		RawAccountedBytes: 1000,
	}); err != nil {
		t.Fatalf("SaveTrafficBaseline(source) error = %v", err)
	}
	if err := sourceStore.SaveHTTPRules(ctx, "edge-b", []storage.HTTPRuleRow{{
		AgentID:           "edge-b",
		FrontendURL:       "https://edge-b.example.com",
		BackendURL:        "http://127.0.0.1:8096",
		BackendsJSON:      `[{"url":"http://127.0.0.1:8096"}]`,
		LoadBalancingJSON: `{"strategy":"round_robin"}`,
		Enabled:           true,
		RelayChainJSON:    `[]`,
		TagsJSON:          `[]`,
		CustomHeadersJSON: `[]`,
	}}); err != nil {
		t.Fatalf("SaveHTTPRules(source) error = %v", err)
	}

	sourceSvc := NewBackupService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, sourceStore)
	archive, _, err := sourceSvc.Export(ctx)
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}

	targetStore, err := newServiceTestSQLiteStore(t, filepath.Join(t.TempDir(), "rollback-target"), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore(target) error = %v", err)
	}
	defer targetStore.Close()
	if err := targetStore.SaveTrafficPolicy(ctx, storage.AgentTrafficPolicyRow{
		AgentID:       "edge-original",
		Direction:     "tx",
		CycleStartDay: 3,
	}); err != nil {
		t.Fatalf("SaveTrafficPolicy(target original) error = %v", err)
	}
	if err := targetStore.SaveTrafficBaseline(ctx, storage.AgentTrafficBaselineRow{
		AgentID:           "edge-original",
		CycleStart:        "2026-05-03T00:00:00Z",
		RawRXBytes:        10,
		RawTXBytes:        20,
		RawAccountedBytes: 20,
		AdjustUsedBytes:   5,
	}); err != nil {
		t.Fatalf("SaveTrafficBaseline(target original) error = %v", err)
	}

	failingStore := &failingBackupStore{
		backupStore:               targetStore,
		remainingHTTPRuleFailures: 1,
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
	trafficPolicies, err := targetStore.ListTrafficPolicies(ctx)
	if err != nil {
		t.Fatalf("ListTrafficPolicies() error = %v", err)
	}
	if len(trafficPolicies) != 1 || trafficPolicies[0].AgentID != "edge-original" || trafficPolicies[0].Direction != "tx" {
		t.Fatalf("traffic policies after rollback = %+v", trafficPolicies)
	}
	trafficBaselines, err := targetStore.ListTrafficBaselines(ctx)
	if err != nil {
		t.Fatalf("ListTrafficBaselines() error = %v", err)
	}
	if len(trafficBaselines) != 1 || trafficBaselines[0].AgentID != "edge-original" || trafficBaselines[0].CycleStart != "2026-05-03T00:00:00Z" {
		t.Fatalf("traffic baselines after rollback = %+v", trafficBaselines)
	}
}

func TestBackupServiceImportBumpsLocalSnapshotRevisionForRestoredLocalRules(t *testing.T) {
	t.Parallel()
	sourceStore, err := newServiceTestSQLiteStore(t, filepath.Join(t.TempDir(), "local-source"), "local")
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

	targetStore, err := newServiceTestSQLiteStore(t, filepath.Join(t.TempDir(), "local-target"), "local")
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
	t.Parallel()
	sourceStore, err := newServiceTestSQLiteStore(t, filepath.Join(t.TempDir(), "cert-source"), "local")
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

	targetStore, err := newServiceTestSQLiteStore(t, filepath.Join(t.TempDir(), "cert-target"), "local")
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
	t.Parallel()
	store, err := newServiceTestSQLiteStore(t, filepath.Join(t.TempDir(), "counting-target"), "local")
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
	t.Parallel()
	sourceStore, err := newServiceTestSQLiteStore(t, filepath.Join(t.TempDir(), "l4-source"), "local")
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

	targetStore, err := newServiceTestSQLiteStore(t, filepath.Join(t.TempDir(), "l4-target"), "local")
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
	t.Parallel()
	sourceStore, err := newServiceTestSQLiteStore(t, filepath.Join(t.TempDir(), "http-source"), "local")
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

	targetStore, err := newServiceTestSQLiteStore(t, filepath.Join(t.TempDir(), "http-target"), "local")
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
	t.Parallel()
	sourceStore, err := newServiceTestSQLiteStore(t, filepath.Join(t.TempDir(), "http-cross-source"), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore(source) error = %v", err)
	}
	defer sourceStore.Close()

	targetStore, err := newServiceTestSQLiteStore(t, filepath.Join(t.TempDir(), "http-cross-target"), "local")
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
	t.Parallel()
	sourceStore, err := newServiceTestSQLiteStore(t, filepath.Join(t.TempDir(), "l4-cross-source"), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore(source) error = %v", err)
	}
	defer sourceStore.Close()

	targetStore, err := newServiceTestSQLiteStore(t, filepath.Join(t.TempDir(), "l4-cross-target"), "local")
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
	t.Parallel()
	sourceStore, err := newServiceTestSQLiteStore(t, filepath.Join(t.TempDir(), "preview-source"), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore(source) error = %v", err)
	}
	defer sourceStore.Close()

	targetStore, err := newServiceTestSQLiteStore(t, filepath.Join(t.TempDir(), "preview-target"), "local")
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

func TestBackupServicePreviewSkipsDuplicateIncomingHTTPRulesAfterFirstImport(t *testing.T) {
	t.Parallel()
	targetStore, err := newServiceTestSQLiteStore(t, filepath.Join(t.TempDir(), "preview-target"), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore(target) error = %v", err)
	}
	defer targetStore.Close()

	archive, err := encodeBackupBundle(BackupBundle{
		Manifest: BackupManifest{
			PackageVersion:     BackupPackageVersion,
			SourceArchitecture: BackupSourceArchitectureGo,
			SourceLocalAgentID: "source-local",
			Counts: BackupCounts{
				Agents:    1,
				HTTPRules: 2,
			},
		},
		Agents: []BackupAgent{{
			ID:         "backup-local",
			Name:       "backup-local",
			AgentToken: "token-backup-local",
			Mode:       "local",
		}},
		HTTPRules: []BackupHTTPRule{
			{
				ID:               1,
				AgentID:          "source-local",
				FrontendURL:      "https://shared.example.com",
				Backends:         []HTTPRuleBackend{{URL: "http://127.0.0.1:8096"}},
				LoadBalancing:    HTTPLoadBalancing{Strategy: "adaptive"},
				Enabled:          true,
				PassProxyHeaders: defaultPassProxyHeaders(),
				CustomHeaders:    []HTTPCustomHeader{},
			},
			{
				ID:               2,
				AgentID:          "backup-local",
				FrontendURL:      "https://shared.example.com",
				Backends:         []HTTPRuleBackend{{URL: "http://127.0.0.1:9096"}},
				LoadBalancing:    HTTPLoadBalancing{Strategy: "adaptive"},
				Enabled:          true,
				PassProxyHeaders: defaultPassProxyHeaders(),
				CustomHeaders:    []HTTPCustomHeader{},
			},
		},
		L4Rules:          []BackupL4Rule{},
		RelayListeners:   []BackupRelayListener{},
		Certificates:     []BackupCertificate{},
		VersionPolicies:  []BackupVersionPolicy{},
		TrafficPolicies:  []BackupTrafficPolicy{},
		TrafficBaselines: []BackupTrafficBaseline{},
	})
	if err != nil {
		t.Fatalf("encodeBackupBundle() error = %v", err)
	}

	result, err := NewBackupService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, targetStore).Preview(t.Context(), archive)
	if err != nil {
		t.Fatalf("Preview() error = %v", err)
	}

	if result.Summary.Imported.HTTPRules != 1 || result.Summary.SkippedConflict.HTTPRules != 1 {
		t.Fatalf("http preview summary = imported %+v skipped conflict %+v report %+v", result.Summary.Imported, result.Summary.SkippedConflict, result.Report)
	}
	importedHTTPRules := 0
	for _, item := range result.Report.Imported {
		if item.Kind == "http_rule" && item.Key == "https://shared.example.com" {
			importedHTTPRules++
		}
	}
	if importedHTTPRules != 1 {
		t.Fatalf("preview imported http rules = %d in report %+v, want first remapped rule imported once", importedHTTPRules, result.Report.Imported)
	}
	var skippedHTTPRules []BackupImportItem
	for _, item := range result.Report.SkippedConflict {
		if item.Kind == "http_rule" {
			skippedHTTPRules = append(skippedHTTPRules, item)
		}
	}
	if len(skippedHTTPRules) != 1 || skippedHTTPRules[0].Key != "https://shared.example.com" || skippedHTTPRules[0].Reason != "frontend_url already exists" {
		t.Fatalf("preview skipped http conflicts = %+v in report %+v", skippedHTTPRules, result.Report.SkippedConflict)
	}
}

func TestBackupServicePreviewTreatsIncomingLocalAgentAsRemappedConflict(t *testing.T) {
	t.Parallel()
	sourceStore, err := newServiceTestSQLiteStore(t, filepath.Join(t.TempDir(), "preview-local-source"), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore(source) error = %v", err)
	}
	defer sourceStore.Close()

	targetStore, err := newServiceTestSQLiteStore(t, filepath.Join(t.TempDir(), "preview-local-target"), "local")
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
	t.Parallel()
	sourceStore, err := newServiceTestSQLiteStore(t, filepath.Join(t.TempDir(), "preview-relay-source"), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore(source) error = %v", err)
	}
	defer sourceStore.Close()

	targetStore, err := newServiceTestSQLiteStore(t, filepath.Join(t.TempDir(), "preview-relay-target"), "local")
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
		RelayLayersJSON:   `[[31]]`,
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
		RelayLayersJSON:   `[[31]]`,
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
	t.Parallel()
	sourceStore, err := newServiceTestSQLiteStore(t, filepath.Join(t.TempDir(), "preview-cert-source"), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore(source) error = %v", err)
	}
	defer sourceStore.Close()

	targetStore, err := newServiceTestSQLiteStore(t, filepath.Join(t.TempDir(), "preview-cert-target"), "local")
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

func TestBackupServiceImportReplacesExistingSystemRelayCAMaterial(t *testing.T) {
	t.Parallel()
	sourceStore, err := newServiceTestSQLiteStore(t, filepath.Join(t.TempDir(), "source"), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore(source) error = %v", err)
	}
	defer sourceStore.Close()

	targetStore, err := newServiceTestSQLiteStore(t, filepath.Join(t.TempDir(), "target"), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore(target) error = %v", err)
	}
	defer targetStore.Close()

	ctx := t.Context()
	if err := sourceStore.SaveAgent(ctx, storage.AgentRow{
		ID:         "edge-a",
		Name:       "edge-a",
		AgentToken: "source-token",
	}); err != nil {
		t.Fatalf("SaveAgent(source) error = %v", err)
	}

	sourceCA, err := generateInternalCAMaterial(relayCADomainIdentity)
	if err != nil {
		t.Fatalf("generate source relay CA: %v", err)
	}
	sourceCA.ID = 10
	sourceLeaf, err := generateRelayLeafMaterial("listener.edge-a.relay.internal", sourceCA, "relay.example.com")
	if err != nil {
		t.Fatalf("generate source relay leaf: %v", err)
	}
	sourceLeaf.ID = 11
	sourcePins, err := deriveRelayPinSetFromCertificate(sourceLeaf.CertPEM)
	if err != nil {
		t.Fatalf("derive source relay pin: %v", err)
	}

	if err := sourceStore.SaveManagedCertificates(ctx, []storage.ManagedCertificateRow{
		managedCertificateToRow(ManagedCertificate{
			ID:              10,
			Domain:          relayCADomainIdentity,
			Enabled:         false,
			Scope:           "domain",
			IssuerMode:      "local_http01",
			TargetAgentIDs:  []string{"edge-a"},
			Status:          "active",
			Usage:           "https",
			CertificateType: "internal_ca",
			SelfSigned:      false,
			Tags:            []string{"legacy"},
			Revision:        1,
		}),
		managedCertificateToRow(ManagedCertificate{
			ID:              11,
			Domain:          sourceLeaf.Domain,
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "local_http01",
			TargetAgentIDs:  []string{"edge-a"},
			Status:          "active",
			Usage:           "relay_tunnel",
			CertificateType: "internal_ca",
			SelfSigned:      false,
			Tags:            []string{"system:relay-listener", systemTag},
			Revision:        2,
		}),
	}); err != nil {
		t.Fatalf("SaveManagedCertificates(source) error = %v", err)
	}
	if err := sourceStore.SaveManagedCertificateMaterial(ctx, relayCADomainIdentity, sourceCA); err != nil {
		t.Fatalf("SaveManagedCertificateMaterial(source CA) error = %v", err)
	}
	if err := sourceStore.SaveManagedCertificateMaterial(ctx, sourceLeaf.Domain, sourceLeaf); err != nil {
		t.Fatalf("SaveManagedCertificateMaterial(source leaf) error = %v", err)
	}
	if err := sourceStore.SaveRelayListeners(ctx, "edge-a", []storage.RelayListenerRow{{
		ID:                      31,
		AgentID:                 "edge-a",
		Name:                    "relay-auto",
		ListenHost:              "127.0.0.1",
		BindHostsJSON:           `["127.0.0.1"]`,
		ListenPort:              7443,
		PublicHost:              "relay.example.com",
		PublicPort:              7443,
		Enabled:                 true,
		CertificateID:           backupIntPtr(11),
		TLSMode:                 "pin_and_ca",
		TransportMode:           "tls_tcp",
		ObfsMode:                "off",
		PinSetJSON:              marshalJSON(sourcePins, "[]"),
		TrustedCACertificateIDs: `[10]`,
		AllowSelfSigned:         true,
		TagsJSON:                `[]`,
		Revision:                3,
	}}); err != nil {
		t.Fatalf("SaveRelayListeners(source) error = %v", err)
	}

	targetCA, err := generateInternalCAMaterial(relayCADomainIdentity)
	if err != nil {
		t.Fatalf("generate target relay CA: %v", err)
	}
	if err := targetStore.SaveManagedCertificates(ctx, []storage.ManagedCertificateRow{
		managedCertificateToRow(ManagedCertificate{
			ID:              1,
			Domain:          relayCADomainIdentity,
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "local_http01",
			Status:          "active",
			Usage:           "relay_ca",
			CertificateType: "internal_ca",
			SelfSigned:      true,
			Tags:            []string{systemRelayCATag, systemTag},
			Revision:        1,
		}),
	}); err != nil {
		t.Fatalf("SaveManagedCertificates(target) error = %v", err)
	}
	if err := targetStore.SaveManagedCertificateMaterial(ctx, relayCADomainIdentity, targetCA); err != nil {
		t.Fatalf("SaveManagedCertificateMaterial(target CA) error = %v", err)
	}

	archive, _, err := NewBackupService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, sourceStore).Export(ctx)
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}
	targetSvc := NewBackupService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, targetStore)
	preview, err := targetSvc.Preview(ctx, archive)
	if err != nil {
		t.Fatalf("Preview() error = %v", err)
	}
	if preview.Summary.Imported.Certificates != 2 || preview.Summary.SkippedConflict.Certificates != 0 {
		t.Fatalf("preview certificate summary = %+v", preview.Summary)
	}
	if _, err := targetSvc.Import(ctx, archive); err != nil {
		t.Fatalf("Import() error = %v", err)
	}

	certs, err := targetStore.ListManagedCertificates(ctx)
	if err != nil {
		t.Fatalf("ListManagedCertificates(target) error = %v", err)
	}
	importedCARow, ok := findRelayCACertificate(certs)
	if !ok {
		t.Fatal("imported relay CA not found")
	}
	if importedCARow.Domain != relayCADomainIdentity || importedCARow.Usage != "relay_ca" || importedCARow.CertificateType != "internal_ca" {
		t.Fatalf("importedCA = %+v", importedCARow)
	}
	if !importedCARow.Enabled || !importedCARow.SelfSigned {
		t.Fatalf("importedCA flags = %+v", importedCARow)
	}
	for _, expectedTag := range []string{systemRelayCATag, systemTag} {
		if !containsString(importedCARow.Tags, expectedTag) {
			t.Fatalf("importedCA.Tags = %+v", importedCARow.Tags)
		}
	}

	importedCAMaterial, ok, err := targetStore.LoadManagedCertificateMaterial(ctx, relayCADomainIdentity)
	if err != nil {
		t.Fatalf("LoadManagedCertificateMaterial(imported CA) error = %v", err)
	}
	if !ok {
		t.Fatal("imported relay CA material missing")
	}
	if importedCAMaterial.CertPEM != sourceCA.CertPEM || importedCAMaterial.KeyPEM != sourceCA.KeyPEM {
		t.Fatal("target relay CA material was not replaced with backup relay CA material")
	}
	importedLeaf, ok, err := targetStore.LoadManagedCertificateMaterial(ctx, sourceLeaf.Domain)
	if err != nil {
		t.Fatalf("LoadManagedCertificateMaterial(imported leaf) error = %v", err)
	}
	if !ok {
		t.Fatal("imported relay leaf material missing")
	}
	if !certificateChainUsesRelayCA(importedLeaf, importedCAMaterial) {
		t.Fatal("imported relay leaf is not verifiable with imported relay CA")
	}
}

func TestBackupServiceImportSkipsSystemRelayCAReplacementWhenExistingRelayCertDependsOnCurrentCA(t *testing.T) {
	t.Parallel()
	sourceStore, err := newServiceTestSQLiteStore(t, filepath.Join(t.TempDir(), "source"), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore(source) error = %v", err)
	}
	defer sourceStore.Close()

	targetStore, err := newServiceTestSQLiteStore(t, filepath.Join(t.TempDir(), "target"), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore(target) error = %v", err)
	}
	defer targetStore.Close()

	ctx := t.Context()
	cfg := config.Config{EnableLocalAgent: true, LocalAgentID: "local"}

	sourceCA, err := generateInternalCAMaterial(relayCADomainIdentity)
	if err != nil {
		t.Fatalf("generate source relay CA: %v", err)
	}
	sourceCA.ID = 1
	sourceLeaf, err := generateRelayLeafMaterial("listener.source.relay.internal", sourceCA, "source-relay.example.com")
	if err != nil {
		t.Fatalf("generate source relay leaf: %v", err)
	}
	sourceLeaf.ID = 11
	sourcePins, err := deriveRelayPinSetFromCertificate(sourceLeaf.CertPEM)
	if err != nil {
		t.Fatalf("derive source relay pin: %v", err)
	}

	if err := sourceStore.SaveManagedCertificates(ctx, []storage.ManagedCertificateRow{
		managedCertificateToRow(ManagedCertificate{
			ID:              1,
			Domain:          relayCADomainIdentity,
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "local_http01",
			TargetAgentIDs:  []string{"local"},
			Status:          "active",
			Usage:           "relay_ca",
			CertificateType: "internal_ca",
			SelfSigned:      true,
			Tags:            []string{systemRelayCATag, systemTag},
			Revision:        1,
		}),
		managedCertificateToRow(ManagedCertificate{
			ID:              11,
			Domain:          sourceLeaf.Domain,
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "local_http01",
			TargetAgentIDs:  []string{"local"},
			Status:          "active",
			Usage:           "relay_tunnel",
			CertificateType: "internal_ca",
			SelfSigned:      false,
			Tags:            autoRelayListenerCertificateTags(31, "local"),
			Revision:        2,
		}),
	}); err != nil {
		t.Fatalf("SaveManagedCertificates(source) error = %v", err)
	}
	if err := sourceStore.SaveManagedCertificateMaterial(ctx, relayCADomainIdentity, sourceCA); err != nil {
		t.Fatalf("SaveManagedCertificateMaterial(source CA) error = %v", err)
	}
	if err := sourceStore.SaveManagedCertificateMaterial(ctx, sourceLeaf.Domain, sourceLeaf); err != nil {
		t.Fatalf("SaveManagedCertificateMaterial(source leaf) error = %v", err)
	}
	if err := sourceStore.SaveRelayListeners(ctx, "local", []storage.RelayListenerRow{{
		ID:                      31,
		AgentID:                 "local",
		Name:                    "source-relay",
		ListenHost:              "127.0.0.1",
		BindHostsJSON:           `["127.0.0.1"]`,
		ListenPort:              7443,
		PublicHost:              "source-relay.example.com",
		PublicPort:              7443,
		Enabled:                 true,
		CertificateID:           backupIntPtr(11),
		TLSMode:                 "pin_and_ca",
		TransportMode:           "tls_tcp",
		ObfsMode:                "off",
		PinSetJSON:              marshalJSON(sourcePins, "[]"),
		TrustedCACertificateIDs: `[1]`,
		AllowSelfSigned:         true,
		TagsJSON:                `[]`,
		Revision:                3,
	}}); err != nil {
		t.Fatalf("SaveRelayListeners(source) error = %v", err)
	}

	targetCA, err := generateInternalCAMaterial(relayCADomainIdentity)
	if err != nil {
		t.Fatalf("generate target relay CA: %v", err)
	}
	targetLeaf, err := generateRelayLeafMaterial("listener.target.relay.internal", targetCA, "target-relay.example.com")
	if err != nil {
		t.Fatalf("generate target relay leaf: %v", err)
	}
	targetLeaf.ID = 2
	targetPins, err := deriveRelayPinSetFromCertificate(targetLeaf.CertPEM)
	if err != nil {
		t.Fatalf("derive target relay pin: %v", err)
	}

	if err := targetStore.SaveManagedCertificates(ctx, []storage.ManagedCertificateRow{
		managedCertificateToRow(ManagedCertificate{
			ID:              1,
			Domain:          relayCADomainIdentity,
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "local_http01",
			TargetAgentIDs:  []string{"local"},
			Status:          "active",
			Usage:           "relay_ca",
			CertificateType: "internal_ca",
			SelfSigned:      true,
			Tags:            []string{systemRelayCATag, systemTag},
			Revision:        1,
		}),
		managedCertificateToRow(ManagedCertificate{
			ID:              2,
			Domain:          targetLeaf.Domain,
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "local_http01",
			TargetAgentIDs:  []string{"local"},
			Status:          "active",
			Usage:           "mixed",
			CertificateType: "uploaded",
			SelfSigned:      false,
			Tags:            []string{"manual-relay-cert"},
			Revision:        2,
		}),
	}); err != nil {
		t.Fatalf("SaveManagedCertificates(target) error = %v", err)
	}
	if err := targetStore.SaveManagedCertificateMaterial(ctx, relayCADomainIdentity, targetCA); err != nil {
		t.Fatalf("SaveManagedCertificateMaterial(target CA) error = %v", err)
	}
	if err := targetStore.SaveManagedCertificateMaterial(ctx, targetLeaf.Domain, targetLeaf); err != nil {
		t.Fatalf("SaveManagedCertificateMaterial(target leaf) error = %v", err)
	}
	if err := targetStore.SaveRelayListeners(ctx, "local", []storage.RelayListenerRow{{
		ID:                      41,
		AgentID:                 "local",
		Name:                    "target-relay",
		ListenHost:              "127.0.0.1",
		BindHostsJSON:           `["127.0.0.1"]`,
		ListenPort:              8443,
		PublicHost:              "target-relay.example.com",
		PublicPort:              8443,
		Enabled:                 true,
		CertificateID:           backupIntPtr(2),
		TLSMode:                 "pin_and_ca",
		TransportMode:           "tls_tcp",
		ObfsMode:                "off",
		PinSetJSON:              marshalJSON(targetPins, "[]"),
		TrustedCACertificateIDs: `[1]`,
		AllowSelfSigned:         true,
		TagsJSON:                `[]`,
		Revision:                3,
	}}); err != nil {
		t.Fatalf("SaveRelayListeners(target) error = %v", err)
	}

	archive, _, err := NewBackupService(cfg, sourceStore).Export(ctx)
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}
	targetSvc := NewBackupService(cfg, targetStore)
	preview, err := targetSvc.Preview(ctx, archive)
	if err != nil {
		t.Fatalf("Preview() error = %v", err)
	}
	if preview.Summary.SkippedConflict.Certificates != 1 || preview.Summary.Imported.Certificates != 1 || preview.Summary.SkippedInvalid.RelayListeners != 1 {
		t.Fatalf("preview summary = %+v", preview.Summary)
	}
	assertBackupSkippedConflictReason(t, preview, "certificate", relayCADomainIdentity, backupSystemRelayCAReplacementConflictReason)
	assertBackupSkippedInvalidReason(t, preview, "relay_listener", "local|source-relay", "referenced trusted CA certificate was not imported")

	result, err := targetSvc.Import(ctx, archive)
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	if result.Summary.SkippedConflict.Certificates != 1 || result.Summary.Imported.Certificates != 1 || result.Summary.SkippedInvalid.RelayListeners != 1 {
		t.Fatalf("import summary = %+v", result.Summary)
	}
	assertBackupSkippedConflictReason(t, result, "certificate", relayCADomainIdentity, backupSystemRelayCAReplacementConflictReason)
	assertBackupSkippedInvalidReason(t, result, "relay_listener", "local|source-relay", "referenced trusted CA certificate was not imported")

	currentCA, ok, err := targetStore.LoadManagedCertificateMaterial(ctx, relayCADomainIdentity)
	if err != nil {
		t.Fatalf("LoadManagedCertificateMaterial(target CA) error = %v", err)
	}
	if !ok {
		t.Fatal("target relay CA material missing")
	}
	if currentCA.CertPEM != targetCA.CertPEM || currentCA.KeyPEM != targetCA.KeyPEM {
		t.Fatal("target relay CA material was replaced")
	}
	currentLeaf, ok, err := targetStore.LoadManagedCertificateMaterial(ctx, targetLeaf.Domain)
	if err != nil {
		t.Fatalf("LoadManagedCertificateMaterial(target leaf) error = %v", err)
	}
	if !ok {
		t.Fatal("target relay leaf material missing")
	}
	if !certificateChainUsesRelayCA(currentLeaf, currentCA) {
		t.Fatal("target relay leaf is no longer verifiable with target relay CA")
	}
}

func TestBackupServiceImportSkipsSystemRelayCAReplacementWhenMaterialMissing(t *testing.T) {
	t.Parallel()
	sourceStore, err := newServiceTestSQLiteStore(t, filepath.Join(t.TempDir(), "source"), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore(source) error = %v", err)
	}
	defer sourceStore.Close()

	targetStore, err := newServiceTestSQLiteStore(t, filepath.Join(t.TempDir(), "target"), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore(target) error = %v", err)
	}
	defer targetStore.Close()

	ctx := t.Context()
	cfg := config.Config{EnableLocalAgent: true, LocalAgentID: "local"}

	sourceCA, err := generateInternalCAMaterial(relayCADomainIdentity)
	if err != nil {
		t.Fatalf("generate source relay CA: %v", err)
	}
	sourceCA.ID = 1
	sourceLeaf, err := generateRelayLeafMaterial("listener.source-missing-ca.relay.internal", sourceCA, "source-missing-ca.example.com")
	if err != nil {
		t.Fatalf("generate source relay leaf: %v", err)
	}
	sourceLeaf.ID = 11
	sourcePins, err := deriveRelayPinSetFromCertificate(sourceLeaf.CertPEM)
	if err != nil {
		t.Fatalf("derive source relay pin: %v", err)
	}

	if err := sourceStore.SaveManagedCertificates(ctx, []storage.ManagedCertificateRow{
		managedCertificateToRow(ManagedCertificate{
			ID:              1,
			Domain:          relayCADomainIdentity,
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "local_http01",
			TargetAgentIDs:  []string{"local"},
			Status:          "active",
			Usage:           "relay_ca",
			CertificateType: "internal_ca",
			SelfSigned:      true,
			Tags:            []string{systemRelayCATag, systemTag},
			Revision:        1,
		}),
		managedCertificateToRow(ManagedCertificate{
			ID:              11,
			Domain:          sourceLeaf.Domain,
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "local_http01",
			TargetAgentIDs:  []string{"local"},
			Status:          "active",
			Usage:           "relay_tunnel",
			CertificateType: "internal_ca",
			SelfSigned:      false,
			Tags:            autoRelayListenerCertificateTags(31, "local"),
			Revision:        2,
		}),
	}); err != nil {
		t.Fatalf("SaveManagedCertificates(source) error = %v", err)
	}
	if err := sourceStore.SaveManagedCertificateMaterial(ctx, relayCADomainIdentity, sourceCA); err != nil {
		t.Fatalf("SaveManagedCertificateMaterial(source CA) error = %v", err)
	}
	if err := sourceStore.SaveManagedCertificateMaterial(ctx, sourceLeaf.Domain, sourceLeaf); err != nil {
		t.Fatalf("SaveManagedCertificateMaterial(source leaf) error = %v", err)
	}
	if err := sourceStore.SaveRelayListeners(ctx, "local", []storage.RelayListenerRow{{
		ID:                      31,
		AgentID:                 "local",
		Name:                    "source-missing-ca",
		ListenHost:              "127.0.0.1",
		BindHostsJSON:           `["127.0.0.1"]`,
		ListenPort:              7443,
		PublicHost:              "source-missing-ca.example.com",
		PublicPort:              7443,
		Enabled:                 true,
		CertificateID:           backupIntPtr(11),
		TLSMode:                 "pin_and_ca",
		TransportMode:           "tls_tcp",
		ObfsMode:                "off",
		PinSetJSON:              marshalJSON(sourcePins, "[]"),
		TrustedCACertificateIDs: `[1]`,
		AllowSelfSigned:         true,
		TagsJSON:                `[]`,
		Revision:                3,
	}}); err != nil {
		t.Fatalf("SaveRelayListeners(source) error = %v", err)
	}

	targetCA, err := generateInternalCAMaterial(relayCADomainIdentity)
	if err != nil {
		t.Fatalf("generate target relay CA: %v", err)
	}
	if err := targetStore.SaveManagedCertificates(ctx, []storage.ManagedCertificateRow{
		managedCertificateToRow(ManagedCertificate{
			ID:              1,
			Domain:          relayCADomainIdentity,
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "local_http01",
			TargetAgentIDs:  []string{"local"},
			Status:          "active",
			Usage:           "relay_ca",
			CertificateType: "internal_ca",
			SelfSigned:      true,
			Tags:            []string{systemRelayCATag, systemTag},
			Revision:        1,
		}),
	}); err != nil {
		t.Fatalf("SaveManagedCertificates(target) error = %v", err)
	}
	if err := targetStore.SaveManagedCertificateMaterial(ctx, relayCADomainIdentity, targetCA); err != nil {
		t.Fatalf("SaveManagedCertificateMaterial(target CA) error = %v", err)
	}

	archive, _, err := NewBackupService(cfg, sourceStore).Export(ctx)
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}
	bundle, err := decodeBackupBundle(archive)
	if err != nil {
		t.Fatalf("decodeBackupBundle() error = %v", err)
	}
	materials := make([]BackupCertificateFile, 0, len(bundle.Materials))
	for _, material := range bundle.Materials {
		if strings.TrimSpace(material.Domain) == relayCADomainIdentity {
			continue
		}
		materials = append(materials, material)
	}
	bundle.Materials = materials
	archive, err = encodeBackupBundle(bundle)
	if err != nil {
		t.Fatalf("encodeBackupBundle() error = %v", err)
	}

	targetSvc := NewBackupService(cfg, targetStore)
	preview, err := targetSvc.Preview(ctx, archive)
	if err != nil {
		t.Fatalf("Preview() error = %v", err)
	}
	if preview.Summary.SkippedMissingMaterial.Certificates != 1 || preview.Summary.Imported.Certificates != 1 || preview.Summary.SkippedInvalid.RelayListeners != 1 {
		t.Fatalf("preview summary = %+v", preview.Summary)
	}
	assertBackupSkippedMissingMaterialReason(t, preview, "certificate", relayCADomainIdentity, "certificate material missing from backup")
	assertBackupSkippedInvalidReason(t, preview, "relay_listener", "local|source-missing-ca", "referenced trusted CA certificate was not imported")

	result, err := targetSvc.Import(ctx, archive)
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	if result.Summary.SkippedMissingMaterial.Certificates != 1 || result.Summary.Imported.Certificates != 1 || result.Summary.SkippedInvalid.RelayListeners != 1 {
		t.Fatalf("import summary = %+v", result.Summary)
	}
	assertBackupSkippedMissingMaterialReason(t, result, "certificate", relayCADomainIdentity, "certificate material missing from backup")
	assertBackupSkippedInvalidReason(t, result, "relay_listener", "local|source-missing-ca", "referenced trusted CA certificate was not imported")

	currentCA, ok, err := targetStore.LoadManagedCertificateMaterial(ctx, relayCADomainIdentity)
	if err != nil {
		t.Fatalf("LoadManagedCertificateMaterial(target CA) error = %v", err)
	}
	if !ok {
		t.Fatal("target relay CA material missing")
	}
	if currentCA.CertPEM != targetCA.CertPEM || currentCA.KeyPEM != targetCA.KeyPEM {
		t.Fatal("target relay CA material was replaced")
	}
}

func TestBackupServiceImportCommitsLocalAndRemoteRevisionsWithOneDependencyPlan(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	store, err := newServiceTestSQLiteStore(t, t.TempDir(), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.SaveAgent(ctx, storage.AgentRow{
		ID: "edge-target", Name: "Edge Target", AgentToken: "target-token",
		Platform: "linux-amd64", CapabilitiesJSON: `["http_rules"]`,
		DesiredRevision: 1, CurrentRevision: 1, LastApplyRevision: 1, LastApplyStatus: "success",
	}); err != nil {
		t.Fatalf("SaveAgent() error = %v", err)
	}
	localBefore, err := store.ListAgentRevisions(ctx, "local")
	if err != nil {
		t.Fatalf("ListAgentRevisions(local before) error = %v", err)
	}
	remoteBefore, err := store.ListAgentRevisions(ctx, "edge-target")
	if err != nil {
		t.Fatalf("ListAgentRevisions(remote before) error = %v", err)
	}

	bundle := BackupBundle{
		Manifest: BackupManifest{
			PackageVersion: BackupPackageVersion, SourceArchitecture: BackupSourceArchitectureGo,
			SourceLocalAgentID: "source-local", ExportedAt: time.Date(2026, 7, 12, 22, 30, 0, 0, time.UTC),
		},
		Agents: []BackupAgent{
			{ID: "source-local", Name: "Source Local", AgentToken: "source-local-token", Mode: "local"},
			{ID: "source-edge", Name: "Edge Target", AgentToken: "source-edge-token", Platform: "linux-amd64", Capabilities: []string{"http_rules"}},
		},
		HTTPRules: []BackupHTTPRule{
			{ID: 11, AgentID: "source-local", FrontendURL: "https://local-import.example.com", Backends: []HTTPRuleBackend{{URL: "http://127.0.0.1:8096"}}, Enabled: true},
			{ID: 12, AgentID: "source-edge", FrontendURL: "https://remote-import.example.com", Backends: []HTTPRuleBackend{{URL: "http://127.0.0.1:8097"}}, Enabled: true},
		},
	}
	archive, err := encodeBackupBundle(bundle)
	if err != nil {
		t.Fatalf("encodeBackupBundle() error = %v", err)
	}
	result, err := NewBackupService(config.Config{
		EnableLocalAgent: true, LocalAgentID: "local", LocalAgentName: "Local",
	}, store).Import(ctx, archive)
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	if result.Summary.Imported.HTTPRules != 2 {
		t.Fatalf("import result = %+v", result)
	}
	assertBackupSkippedConflictReason(t, result, "agent", "Source Local", "local agent remapped to target")
	assertBackupSkippedConflictReason(t, result, "agent", "Edge Target", "agent name already exists")

	localAfter, err := store.ListAgentRevisions(ctx, "local")
	if err != nil {
		t.Fatalf("ListAgentRevisions(local after) error = %v", err)
	}
	remoteAfter, err := store.ListAgentRevisions(ctx, "edge-target")
	if err != nil {
		t.Fatalf("ListAgentRevisions(remote after) error = %v", err)
	}
	if len(localAfter) != len(localBefore)+1 || len(remoteAfter) != len(remoteBefore)+1 {
		t.Fatalf("revision counts local %d->%d remote %d->%d", len(localBefore), len(localAfter), len(remoteBefore), len(remoteAfter))
	}
	localRevision := localAfter[len(localAfter)-1]
	remoteRevision := remoteAfter[len(remoteAfter)-1]
	if localRevision.OperationID == "" || localRevision.OperationID != remoteRevision.OperationID {
		t.Fatalf("local revision = %+v, remote revision = %+v", localRevision, remoteRevision)
	}
	if localRevision.State != storage.AgentRevisionStatePending || remoteRevision.State != storage.AgentRevisionStatePending {
		t.Fatalf("local/remote states = %q/%q", localRevision.State, remoteRevision.State)
	}
	dependencyArtifact, found, err := store.GetOperationDependencyArtifact(ctx, localRevision.OperationID)
	if err != nil || !found {
		t.Fatalf("GetOperationDependencyArtifact() found=%v error=%v", found, err)
	}
	if dependencyArtifact.Kind != storage.GenerationArtifactKindDependencyPlan || len(dependencyArtifact.Payload) == 0 {
		t.Fatalf("dependency artifact = %+v", dependencyArtifact)
	}
	agentSvc := NewAgentService(config.Config{
		EnableLocalAgent: true, LocalAgentID: "local", LocalAgentName: "Local",
	}, store)
	localSummary, err := agentSvc.Get(ctx, "local")
	if err != nil {
		t.Fatalf("AgentService.Get(local) error = %v", err)
	}
	if int64(localSummary.DesiredRevision) != localRevision.Revision {
		t.Fatalf("local summary desired revision = %d, ledger revision = %d", localSummary.DesiredRevision, localRevision.Revision)
	}
	summaries, err := agentSvc.List(ctx)
	if err != nil {
		t.Fatalf("AgentService.List() error = %v", err)
	}
	if len(summaries) == 0 || summaries[0].ID != "local" || int64(summaries[0].DesiredRevision) != localRevision.Revision {
		t.Fatalf("agent summaries after backup import = %+v, local ledger revision = %d", summaries, localRevision.Revision)
	}
}

func TestBackupServiceImportValidationFailureRollsBackResourcesAndRevisionLedger(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	store, err := newServiceTestSQLiteStore(t, t.TempDir(), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	for _, agentID := range []string{"edge-a", "edge-b"} {
		if err := store.SaveAgent(ctx, storage.AgentRow{
			ID: agentID, Name: agentID, AgentToken: "token-" + agentID,
			Platform: "linux-amd64", CapabilitiesJSON: `["http_rules"]`,
			DesiredRevision: 1, CurrentRevision: 1, LastApplyStatus: "success",
		}); err != nil {
			t.Fatalf("SaveAgent(%s) error = %v", agentID, err)
		}
	}
	if err := store.SaveRelayListeners(ctx, "edge-a", []storage.RelayListenerRow{{
		ID: 101, AgentID: "edge-a", Name: "relay-a", ListenHost: "127.0.0.1", ListenPort: 7101,
		Enabled: true, TransportMode: "tls_tcp", BindHostsJSON: `[]`, TrustedCACertificateIDs: `[]`, TagsJSON: `[]`, Revision: 1,
	}}); err != nil {
		t.Fatalf("SaveRelayListeners(edge-a) error = %v", err)
	}
	if err := store.SaveRelayListeners(ctx, "edge-b", []storage.RelayListenerRow{{
		ID: 102, AgentID: "edge-b", Name: "relay-b", ListenHost: "127.0.0.1", ListenPort: 7102,
		Enabled: true, TransportMode: "tls_tcp", BindHostsJSON: `[]`, TrustedCACertificateIDs: `[]`, TagsJSON: `[]`, Revision: 1,
	}}); err != nil {
		t.Fatalf("SaveRelayListeners(edge-b) error = %v", err)
	}
	revisionsBefore := map[string][]storage.AgentRevisionRow{}
	for _, agentID := range []string{"edge-a", "edge-b"} {
		revisionsBefore[agentID], err = store.ListAgentRevisions(ctx, agentID)
		if err != nil {
			t.Fatalf("ListAgentRevisions(%s before) error = %v", agentID, err)
		}
	}

	bundle := BackupBundle{
		Manifest: BackupManifest{PackageVersion: BackupPackageVersion, SourceArchitecture: BackupSourceArchitectureGo, ExportedAt: time.Now().UTC()},
		Agents: []BackupAgent{
			{ID: "source-a", Name: "edge-a", AgentToken: "source-a", Platform: "linux-amd64", Capabilities: []string{"http_rules"}},
			{ID: "source-b", Name: "edge-b", AgentToken: "source-b", Platform: "linux-amd64", Capabilities: []string{"http_rules"}},
		},
		HTTPRules: []BackupHTTPRule{
			{ID: 11, AgentID: "source-a", FrontendURL: "https://cycle-a.example.com", Backends: []HTTPRuleBackend{{URL: "http://127.0.0.1:8096"}}, RelayLayers: [][]int{{102}}, Enabled: true},
			{ID: 12, AgentID: "source-b", FrontendURL: "https://cycle-b.example.com", Backends: []HTTPRuleBackend{{URL: "http://127.0.0.1:8097"}}, RelayLayers: [][]int{{101}}, Enabled: true},
		},
	}
	archive, err := encodeBackupBundle(bundle)
	if err != nil {
		t.Fatalf("encodeBackupBundle() error = %v", err)
	}
	_, err = NewBackupService(config.Config{LocalAgentID: "local"}, store).Import(ctx, archive)
	if err == nil {
		t.Fatal("Import() error = nil, want dependency cycle validation failure")
	}
	for _, agentID := range []string{"edge-a", "edge-b"} {
		rules, listErr := store.ListHTTPRules(ctx, agentID)
		if listErr != nil {
			t.Fatalf("ListHTTPRules(%s) error = %v", agentID, listErr)
		}
		if len(rules) != 0 {
			t.Fatalf("rules for %s after failed import = %+v", agentID, rules)
		}
		revisionsAfter, listErr := store.ListAgentRevisions(ctx, agentID)
		if listErr != nil {
			t.Fatalf("ListAgentRevisions(%s after) error = %v", agentID, listErr)
		}
		if len(revisionsAfter) != len(revisionsBefore[agentID]) {
			t.Fatalf("%s revision count changed from %d to %d after failed import", agentID, len(revisionsBefore[agentID]), len(revisionsAfter))
		}
	}
}

// TestBackupServiceRoundTripsAgentDDNSConfigWithoutCredentials locks in the
// backup contract for the DDNS config: the per-agent domain + extraction
// strategy survives export/import, and the serialized ddns_config carries no
// Cloudflare credential surface (R7 — CF tokens never enter backups).
func TestBackupServiceRoundTripsAgentDDNSConfigWithoutCredentials(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	cfg := config.Config{EnableLocalAgent: true, LocalAgentID: "local"}
	sourceStore, err := newServiceTestSQLiteStore(t, filepath.Join(t.TempDir(), "ddns-source"), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore(source) error = %v", err)
	}
	defer sourceStore.Close()
	original := storage.DDNSConfig{
		Domain: "edge.example.com",
		IPv4:   storage.DDNSFamily{Enabled: true, Source: "public_api"},
		IPv6:   storage.DDNSFamily{Enabled: true, Source: "interface", Interface: "eth0"},
	}
	originalJSON, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal original ddns config: %v", err)
	}
	if err := sourceStore.SaveAgent(ctx, storage.AgentRow{
		ID:             "edge-ddns",
		Name:           "Edge DDNS",
		AgentToken:     "token-ddns",
		DdnsConfigJSON: string(originalJSON),
	}); err != nil {
		t.Fatalf("SaveAgent() error = %v", err)
	}
	// An unconfigured agent exercises the pointer omitempty path: its ddns_config
	// must be dropped from the backup and stay empty after import.
	if err := sourceStore.SaveAgent(ctx, storage.AgentRow{
		ID:         "edge-plain",
		Name:       "Edge Plain",
		AgentToken: "token-plain",
	}); err != nil {
		t.Fatalf("SaveAgent(plain) error = %v", err)
	}

	sourceSvc := NewBackupService(cfg, sourceStore)
	archive, _, err := sourceSvc.Export(ctx)
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}

	targetStore, err := newServiceTestSQLiteStore(t, filepath.Join(t.TempDir(), "ddns-target"), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore(target) error = %v", err)
	}
	defer targetStore.Close()
	targetSvc := NewBackupService(cfg, targetStore)
	if _, err := targetSvc.Import(ctx, archive); err != nil {
		t.Fatalf("Import() error = %v", err)
	}

	agents, err := targetStore.ListAgents(ctx)
	if err != nil {
		t.Fatalf("ListAgents() error = %v", err)
	}
	if len(agents) != 2 {
		t.Fatalf("agents len = %d, want 2", len(agents))
	}
	byID := map[string]storage.AgentRow{}
	for _, row := range agents {
		byID[row.ID] = row
	}
	var restored storage.DDNSConfig
	if err := json.Unmarshal([]byte(byID["edge-ddns"].DdnsConfigJSON), &restored); err != nil {
		t.Fatalf("unmarshal restored ddns_config %q: %v", byID["edge-ddns"].DdnsConfigJSON, err)
	}
	if restored != original {
		t.Fatalf("restored ddns_config = %+v, want %+v", restored, original)
	}
	if byID["edge-plain"].DdnsConfigJSON != "" {
		t.Fatalf("unconfigured agent ddns_config = %q, want empty (pointer omitempty)", byID["edge-plain"].DdnsConfigJSON)
	}

	// R7: the backed-up ddns_config payload must expose only domain + ipv4 + ipv6.
	lower := strings.ToLower(byID["edge-ddns"].DdnsConfigJSON)
	for _, forbidden := range []string{"token", "secret", "api_key", "apikey", "password", "credential"} {
		if strings.Contains(lower, forbidden) {
			t.Fatalf("backed-up ddns_config leaked credential-ish key %q: %s", forbidden, byID["edge-ddns"].DdnsConfigJSON)
		}
	}
}

// TestBackupDDNSConfigHelpersNilEmpty covers the pointer helpers directly: nil
// and all-disabled configs round-trip to "" so unconfigured agents stay clean,
// and a populated config survives a marshal/parse cycle without credentials.
func TestBackupDDNSConfigHelpersNilEmpty(t *testing.T) {
	t.Parallel()
	if got := parseBackupDDNSConfig(""); got != nil {
		t.Fatalf("parseBackupDDNSConfig(empty) = %+v, want nil", got)
	}
	if got := parseBackupDDNSConfig(`{"domain":"","ipv4":{"enabled":false},"ipv6":{"enabled":false}}`); got != nil {
		t.Fatalf("parseBackupDDNSConfig(all-disabled) = %+v, want nil", got)
	}
	if got := parseBackupDDNSConfig("{not-json"); got != nil {
		t.Fatalf("parseBackupDDNSConfig(malformed) = %+v, want nil", got)
	}
	if got := marshalDDNSConfigJSON(nil); got != "" {
		t.Fatalf("marshalDDNSConfigJSON(nil) = %q, want empty", got)
	}
	populated := &storage.DDNSConfig{
		Domain: "edge.example.com",
		IPv4:   storage.DDNSFamily{Enabled: true, Source: "public_api"},
	}
	marshaled := marshalDDNSConfigJSON(populated)
	parsed := parseBackupDDNSConfig(marshaled)
	if parsed == nil {
		t.Fatalf("parseBackupDDNSConfig(marshaled) = nil, want populated")
	}
	if *parsed != *populated {
		t.Fatalf("round-trip = %+v, want %+v", *parsed, *populated)
	}
}
