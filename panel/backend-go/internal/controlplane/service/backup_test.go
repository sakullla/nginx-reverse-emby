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

func assertBackupSkippedConflictReason(t *testing.T, result BackupImportResult, kind string, key string, reason string) {
	t.Helper()
	for _, item := range result.Report.SkippedConflict {
		if item.Kind == kind && item.Key == key && item.Reason == reason {
			return
		}
	}
	t.Fatalf("missing skipped conflict item kind=%q key=%q reason=%q in %+v", kind, key, reason, result.Report.SkippedConflict)
}

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

func TestBackupServicePreservesAgentOutboundProxyURL(t *testing.T) {
	ctx := context.Background()
	cfg := config.Config{EnableLocalAgent: true, LocalAgentID: "local"}
	sourceStore, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "proxy-source"), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore(source) error = %v", err)
	}
	defer sourceStore.Close()
	if err := sourceStore.SaveAgent(ctx, storage.AgentRow{
		ID:               "edge-proxy",
		Name:             "Edge Proxy",
		AgentToken:       "token-proxy",
		CapabilitiesJSON: `["http_rules","l4","relay_quic"]`,
		OutboundProxyURL: "socks://user:pass@127.0.0.1:1080",
	}); err != nil {
		t.Fatalf("SaveAgent() error = %v", err)
	}

	sourceSvc := NewBackupService(cfg, sourceStore)
	archive, _, err := sourceSvc.Export(ctx)
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}

	targetStore, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "proxy-target"), "local")
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
}

func TestBackupServicePreservesAgentTrafficStatsInterval(t *testing.T) {
	ctx := context.Background()
	cfg := config.Config{EnableLocalAgent: true, LocalAgentID: "local"}
	sourceStore, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "traffic-source"), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore(source) error = %v", err)
	}
	defer sourceStore.Close()
	if err := sourceStore.SaveAgent(ctx, storage.AgentRow{
		ID:                   "edge-traffic",
		Name:                 "Edge Traffic",
		AgentToken:           "token-traffic",
		CapabilitiesJSON:     `["http_rules"]`,
		TrafficStatsInterval: "30s",
	}); err != nil {
		t.Fatalf("SaveAgent() error = %v", err)
	}

	sourceSvc := NewBackupService(cfg, sourceStore)
	archive, _, err := sourceSvc.Export(ctx)
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}

	targetStore, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "traffic-target"), "local")
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
	if agents[0].TrafficStatsInterval != "30s" {
		t.Fatalf("TrafficStatsInterval = %q", agents[0].TrafficStatsInterval)
	}
}

func TestBackupServiceTrafficPolicyAndBaselineRoundTripExcludesHistory(t *testing.T) {
	ctx := context.Background()
	cfg := config.Config{EnableLocalAgent: true, LocalAgentID: "local"}
	sourceStore, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "traffic-source"), "local")
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

	targetStore, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "traffic-target"), "target-local")
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
	ctx := context.Background()
	targetStore, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "legacy-no-traffic"), "local")
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

func TestBackupServiceCanonicalizesLegacyRuleFieldsOnPreviewAndImport(t *testing.T) {
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

	previewStore, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "preview"), "local")
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

	importStore, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "import"), "local")
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
	ctx := context.Background()
	cfg := config.Config{EnableLocalAgent: true, LocalAgentID: "local"}
	targetStore, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "target"), "local")
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
			ID:              45,
			AgentID:         "edge-proxy-entry",
			Name:            "proxy entry",
			Protocol:        "tcp",
			ListenHost:      "0.0.0.0",
			ListenPort:      1080,
			ListenMode:      "proxy",
			ProxyEntryAuth:  L4ProxyEntryAuth{Enabled: true, Username: "client", Password: "secret"},
			ProxyEgressMode: "proxy",
			ProxyEgressURL:  "socks5h://egress:pass@127.0.0.1:1081",
			Enabled:         true,
			Tags:            []string{"proxy-entry"},
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
	if row.ProxyEgressMode != "proxy" || row.ProxyEgressURL != "socks5h://egress:pass@127.0.0.1:1081" {
		t.Fatalf("proxy egress = mode %q url %q", row.ProxyEgressMode, row.ProxyEgressURL)
	}
	var auth L4ProxyEntryAuth
	if err := json.Unmarshal([]byte(row.ProxyEntryAuthJSON), &auth); err != nil {
		t.Fatalf("unmarshal ProxyEntryAuthJSON: %v", err)
	}
	if !auth.Enabled || auth.Username != "client" || auth.Password != "secret" {
		t.Fatalf("ProxyEntryAuth = %+v", auth)
	}
}

func TestBackupServiceExportIncludesL4WireGuardEgressURI(t *testing.T) {
	ctx := t.Context()
	sourceStore, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "l4-wg-export-source"), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore(source) error = %v", err)
	}
	defer sourceStore.Close()

	if err := sourceStore.SaveAgent(ctx, storage.AgentRow{
		ID:         "edge-l4-wg",
		Name:       "edge-l4-wg",
		AgentToken: "token-edge-l4-wg",
	}); err != nil {
		t.Fatalf("SaveAgent() error = %v", err)
	}
	profileID := 41
	uri := "wireguard://" + testWireGuardPrivateKey + "@edge.example.com:51820?publickey=" + testWireGuardPublicKey + "&address=10.44.0.2/32#URI%20egress"
	if err := sourceStore.SaveWireGuardProfiles(ctx, "edge-l4-wg", []storage.WireGuardProfileRow{
		materializedWireGuardURIProfileRowForRule(t, "edge-l4-wg", profileID, 71, uri),
	}); err != nil {
		t.Fatalf("SaveWireGuardProfiles() error = %v", err)
	}
	if err := sourceStore.SaveL4Rules(ctx, "edge-l4-wg", []storage.L4RuleRow{{
		ID:                 71,
		AgentID:            "edge-l4-wg",
		Name:               "wg-egress",
		Protocol:           "tcp",
		ListenHost:         "0.0.0.0",
		ListenPort:         9443,
		BackendsJSON:       `[{"host":"127.0.0.1","port":9443}]`,
		LoadBalancingJSON:  `{"strategy":"adaptive"}`,
		TuningJSON:         `{"proxy_protocol":{"decode":false,"send":false}}`,
		RelayChainJSON:     `[]`,
		RelayLayersJSON:    `[]`,
		ProxyEntryAuthJSON: `{}`,
		ListenMode:         "proxy",
		WireGuardProfileID: &profileID,
		ProxyEgressMode:    "wireguard",
		WireGuardEgressURI: uri,
		Enabled:            true,
		TagsJSON:           `["wg"]`,
		Revision:           5,
	}}); err != nil {
		t.Fatalf("SaveL4Rules() error = %v", err)
	}

	archive, _, err := NewBackupService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, sourceStore).Export(ctx)
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}
	var rules []map[string]any
	if err := json.Unmarshal(backupArchiveJSONFile(t, archive, "l4_rules.json"), &rules); err != nil {
		t.Fatalf("unmarshal l4_rules.json: %v", err)
	}
	if len(rules) != 1 {
		t.Fatalf("l4 rules len = %d, want 1", len(rules))
	}
	rule := rules[0]
	if rule["proxy_egress_mode"] != "wireguard" {
		t.Fatalf("proxy_egress_mode = %#v, want wireguard", rule["proxy_egress_mode"])
	}
	if rule["wireguard_profile_id"] != float64(profileID) {
		t.Fatalf("wireguard_profile_id = %#v, want %d", rule["wireguard_profile_id"], profileID)
	}
	if rule["wireguard_egress_uri"] != uri {
		t.Fatalf("wireguard_egress_uri = %#v, want %q", rule["wireguard_egress_uri"], uri)
	}
}

func TestBackupServiceImportPreservesL4WireGuardEgressURIAndRemapsProfileID(t *testing.T) {
	ctx := t.Context()
	targetStore, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "l4-wg-import-target"), "target-local")
	if err != nil {
		t.Fatalf("NewSQLiteStore(target) error = %v", err)
	}
	defer targetStore.Close()

	if err := targetStore.SaveAgent(ctx, storage.AgentRow{
		ID:         "existing-agent",
		Name:       "existing-agent",
		AgentToken: "token-existing",
	}); err != nil {
		t.Fatalf("SaveAgent(existing) error = %v", err)
	}
	if err := targetStore.SaveWireGuardProfiles(ctx, "existing-agent", []storage.WireGuardProfileRow{{
		ID:            41,
		AgentID:       "existing-agent",
		Name:          "existing-wg",
		Mode:          "generic_wireguard",
		PrivateKey:    testWireGuardPrivateKey,
		ListenPort:    51821,
		AddressesJSON: `["10.60.0.1/24"]`,
		PeersJSON:     `[]`,
		DNSJSON:       `[]`,
		Enabled:       true,
		Revision:      9,
	}}); err != nil {
		t.Fatalf("SaveWireGuardProfiles(existing) error = %v", err)
	}

	sourceProfileID := 41
	ruleID := 71
	uri := "wireguard://" + testWireGuardPrivateKey + "@edge.example.com:51820?publickey=" + testWireGuardPublicKey + "&address=10.44.0.2/32#URI%20egress"
	profileRow := materializedWireGuardURIProfileRowForRule(t, "edge-l4-wg", sourceProfileID, ruleID, uri)
	bundle := BackupBundle{
		Manifest: BackupManifest{
			PackageVersion:     BackupPackageVersion,
			SourceArchitecture: BackupSourceArchitectureGo,
			ExportedAt:         time.Date(2026, 5, 16, 0, 0, 0, 0, time.UTC),
		},
		Agents: []BackupAgent{{
			ID:           "edge-l4-wg",
			Name:         "edge-l4-wg",
			AgentToken:   "token-edge-l4-wg",
			Capabilities: []string{"l4", "wireguard"},
		}},
		WireGuardProfiles: []BackupWireGuardProfile{backupWireGuardProfileFromRow(profileRow)},
		L4Rules: []BackupL4Rule{{
			ID:                 ruleID,
			AgentID:            "edge-l4-wg",
			Name:               "wg-egress",
			Protocol:           "tcp",
			ListenHost:         "0.0.0.0",
			ListenPort:         9443,
			Backends:           []L4Backend{{Host: "127.0.0.1", Port: 9443}},
			LoadBalancing:      L4LoadBalancing{Strategy: "adaptive"},
			Tuning:             L4Tuning{ProxyProtocol: L4ProxyProtocolTuning{}},
			ListenMode:         "proxy",
			WireGuardProfileID: &sourceProfileID,
			ProxyEgressMode:    "wireguard",
			WireGuardEgressURI: uri,
			Enabled:            true,
			Tags:               []string{"wg"},
			Revision:           5,
		}},
	}
	archive, err := encodeBackupBundle(bundle)
	if err != nil {
		t.Fatalf("encodeBackupBundle() error = %v", err)
	}

	result, err := NewBackupService(config.Config{EnableLocalAgent: true, LocalAgentID: "target-local"}, targetStore).Import(ctx, archive)
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	if result.Summary.Imported.L4Rules != 1 || result.Summary.Imported.WireGuardProfiles != 1 {
		t.Fatalf("import summary = %+v", result.Summary)
	}

	rows, err := targetStore.ListL4Rules(ctx, "edge-l4-wg")
	if err != nil {
		t.Fatalf("ListL4Rules() error = %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("l4 rules len = %d, want 1: %+v", len(rows), rows)
	}
	row := rows[0]
	if row.ProxyEgressMode != "wireguard" {
		t.Fatalf("ProxyEgressMode = %q, want wireguard", row.ProxyEgressMode)
	}
	if row.WireGuardProfileID == nil {
		t.Fatalf("WireGuardProfileID = nil, want remapped profile ID")
	}
	if row.WireGuardEgressURI != uri {
		t.Fatalf("WireGuardEgressURI = %q, want %q", row.WireGuardEgressURI, uri)
	}

	profiles, err := targetStore.ListWireGuardProfiles(ctx, "edge-l4-wg")
	if err != nil {
		t.Fatalf("ListWireGuardProfiles() error = %v", err)
	}
	if len(profiles) != 1 {
		t.Fatalf("wireguard profiles len = %d, want 1: %+v", len(profiles), profiles)
	}
	if row.WireGuardProfileID == nil || *row.WireGuardProfileID != profiles[0].ID {
		t.Fatalf("L4 wireguard profile ID = %v, want imported profile ID %d", row.WireGuardProfileID, profiles[0].ID)
	}
	parsed, err := ParseWireGuardURI(uri)
	if err != nil {
		t.Fatalf("ParseWireGuardURI() error = %v", err)
	}
	if !wireGuardProfileRowMatchesURI(profiles[0], parsed, "l4-rule-71-wireguard-egress") {
		t.Fatalf("imported wireguard profile = %+v, want materialized URI shape", profiles[0])
	}
}

func TestBackupServiceImportRemapsL4WireGuardURIEgressProfileOwnershipWhenRuleIDChanges(t *testing.T) {
	ctx := t.Context()
	targetStore, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "l4-wg-import-remap-target"), "target-local")
	if err != nil {
		t.Fatalf("NewSQLiteStore(target) error = %v", err)
	}
	defer targetStore.Close()

	if err := targetStore.SaveAgent(ctx, storage.AgentRow{
		ID:               "existing-agent",
		Name:             "existing-agent",
		AgentToken:       "token-existing",
		CapabilitiesJSON: `["http_rules","l4","wireguard"]`,
	}); err != nil {
		t.Fatalf("SaveAgent(existing) error = %v", err)
	}
	sourceRuleID := 71
	if err := targetStore.SaveHTTPRules(ctx, "existing-agent", []storage.HTTPRuleRow{{
		ID:                sourceRuleID,
		AgentID:           "existing-agent",
		FrontendURL:       "https://existing.example.com",
		BackendURL:        "http://127.0.0.1:8096",
		BackendsJSON:      `[{"url":"http://127.0.0.1:8096"}]`,
		LoadBalancingJSON: `{"strategy":"adaptive"}`,
		Enabled:           true,
		RelayChainJSON:    `[]`,
		TagsJSON:          `[]`,
		CustomHeadersJSON: `[]`,
		Revision:          3,
	}}); err != nil {
		t.Fatalf("SaveHTTPRules(existing) error = %v", err)
	}

	sourceProfileID := 41
	uri := "wireguard://" + testWireGuardPrivateKey + "@edge.example.com:51820?publickey=" + testWireGuardPublicKey + "&address=10.44.0.2/32"
	profileRow := materializedWireGuardURIProfileRowForRule(t, "edge-l4-wg", sourceProfileID, sourceRuleID, uri)
	bundle := BackupBundle{
		Manifest: BackupManifest{
			PackageVersion:     BackupPackageVersion,
			SourceArchitecture: BackupSourceArchitectureGo,
			ExportedAt:         time.Date(2026, 5, 16, 0, 0, 0, 0, time.UTC),
		},
		Agents: []BackupAgent{{
			ID:           "edge-l4-wg",
			Name:         "edge-l4-wg",
			AgentToken:   "token-edge-l4-wg",
			Capabilities: []string{"l4", "wireguard"},
		}},
		WireGuardProfiles: []BackupWireGuardProfile{backupWireGuardProfileFromRow(profileRow)},
		L4Rules: []BackupL4Rule{{
			ID:                 sourceRuleID,
			AgentID:            "edge-l4-wg",
			Name:               "wg-egress",
			Protocol:           "tcp",
			ListenHost:         "0.0.0.0",
			ListenPort:         9443,
			Backends:           []L4Backend{{Host: "127.0.0.1", Port: 9443}},
			LoadBalancing:      L4LoadBalancing{Strategy: "adaptive"},
			Tuning:             L4Tuning{ProxyProtocol: L4ProxyProtocolTuning{}},
			ListenMode:         "proxy",
			WireGuardProfileID: &sourceProfileID,
			ProxyEgressMode:    "wireguard",
			WireGuardEgressURI: uri,
			Enabled:            true,
			Tags:               []string{"wg"},
			Revision:           5,
		}},
	}
	archive, err := encodeBackupBundle(bundle)
	if err != nil {
		t.Fatalf("encodeBackupBundle() error = %v", err)
	}

	result, err := NewBackupService(config.Config{EnableLocalAgent: true, LocalAgentID: "target-local"}, targetStore).Import(ctx, archive)
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	if result.Summary.Imported.L4Rules != 1 || result.Summary.Imported.WireGuardProfiles != 1 {
		t.Fatalf("import summary = %+v", result.Summary)
	}

	rows, err := targetStore.ListL4Rules(ctx, "edge-l4-wg")
	if err != nil {
		t.Fatalf("ListL4Rules() error = %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("l4 rules len = %d, want 1: %+v", len(rows), rows)
	}
	importedRule := rows[0]
	if importedRule.ID == sourceRuleID {
		t.Fatalf("imported L4 rule ID = %d, want remapped ID", importedRule.ID)
	}
	if importedRule.WireGuardEgressURI != uri {
		t.Fatalf("WireGuardEgressURI = %q, want %q", importedRule.WireGuardEgressURI, uri)
	}
	if importedRule.WireGuardProfileID == nil {
		t.Fatalf("WireGuardProfileID = nil, want imported profile")
	}

	profiles, err := targetStore.ListWireGuardProfiles(ctx, "edge-l4-wg")
	if err != nil {
		t.Fatalf("ListWireGuardProfiles() error = %v", err)
	}
	if len(profiles) != 1 {
		t.Fatalf("wireguard profiles len = %d, want 1: %+v", len(profiles), profiles)
	}
	if *importedRule.WireGuardProfileID != profiles[0].ID {
		t.Fatalf("L4 wireguard profile ID = %v, want imported profile ID %d", importedRule.WireGuardProfileID, profiles[0].ID)
	}
	parsed, err := ParseWireGuardURI(uri)
	if err != nil {
		t.Fatalf("ParseWireGuardURI() error = %v", err)
	}
	expectedProfileName := fmt.Sprintf("l4-rule-%d-wireguard-egress", importedRule.ID)
	if !wireGuardProfileRowMatchesURI(profiles[0], parsed, expectedProfileName) {
		t.Errorf("imported wireguard profile = %+v, want materialized URI ownership for %q", profiles[0], expectedProfileName)
	}

	l4Svc := NewL4RuleService(config.Config{EnableLocalAgent: true, LocalAgentID: "target-local"}, targetStore)
	if _, err := l4Svc.Delete(ctx, "edge-l4-wg", importedRule.ID); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	profiles, err = targetStore.ListWireGuardProfiles(ctx, "edge-l4-wg")
	if err != nil {
		t.Fatalf("ListWireGuardProfiles(after delete) error = %v", err)
	}
	if len(profiles) != 0 {
		t.Fatalf("wireguard profiles after L4 delete = %+v, want materialized profile removed", profiles)
	}
}

func TestBackupServiceImportClearsL4WireGuardEgressURIWhenProfileImportConflicts(t *testing.T) {
	ctx := t.Context()
	targetStore, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "l4-wg-import-conflict-target"), "target-local")
	if err != nil {
		t.Fatalf("NewSQLiteStore(target) error = %v", err)
	}
	defer targetStore.Close()

	if err := targetStore.SaveAgent(ctx, storage.AgentRow{
		ID:               "edge-l4-wg",
		Name:             "edge-l4-wg",
		AgentToken:       "token-existing",
		CapabilitiesJSON: `["l4","wireguard"]`,
	}); err != nil {
		t.Fatalf("SaveAgent(existing) error = %v", err)
	}

	sourceProfileID := 41
	existingProfileID := 88
	ruleID := 71
	uri := "wireguard://" + testWireGuardPrivateKey + "@edge.example.com:51820?publickey=" + testWireGuardPublicKey + "&address=10.44.0.2/32#URI%20egress"
	existingProfile := materializedWireGuardURIProfileRowForRule(t, "edge-l4-wg", existingProfileID, ruleID, uri)
	if err := targetStore.SaveWireGuardProfiles(ctx, "edge-l4-wg", []storage.WireGuardProfileRow{existingProfile}); err != nil {
		t.Fatalf("SaveWireGuardProfiles(existing) error = %v", err)
	}

	sourceProfile := materializedWireGuardURIProfileRowForRule(t, "edge-l4-wg", sourceProfileID, ruleID, uri)
	bundle := BackupBundle{
		Manifest: BackupManifest{
			PackageVersion:     BackupPackageVersion,
			SourceArchitecture: BackupSourceArchitectureGo,
			ExportedAt:         time.Date(2026, 5, 16, 0, 0, 0, 0, time.UTC),
		},
		Agents: []BackupAgent{{
			ID:           "edge-l4-wg",
			Name:         "edge-l4-wg",
			AgentToken:   "token-edge-l4-wg",
			Capabilities: []string{"l4", "wireguard"},
		}},
		WireGuardProfiles: []BackupWireGuardProfile{backupWireGuardProfileFromRow(sourceProfile)},
		L4Rules: []BackupL4Rule{{
			ID:                 ruleID,
			AgentID:            "edge-l4-wg",
			Name:               "wg-egress",
			Protocol:           "tcp",
			ListenHost:         "0.0.0.0",
			ListenPort:         9443,
			Backends:           []L4Backend{{Host: "127.0.0.1", Port: 9443}},
			LoadBalancing:      L4LoadBalancing{Strategy: "adaptive"},
			Tuning:             L4Tuning{ProxyProtocol: L4ProxyProtocolTuning{}},
			ListenMode:         "proxy",
			WireGuardProfileID: &sourceProfileID,
			ProxyEgressMode:    "wireguard",
			WireGuardEgressURI: uri,
			Enabled:            true,
			Tags:               []string{"wg"},
			Revision:           5,
		}},
	}
	archive, err := encodeBackupBundle(bundle)
	if err != nil {
		t.Fatalf("encodeBackupBundle() error = %v", err)
	}

	result, err := NewBackupService(config.Config{EnableLocalAgent: true, LocalAgentID: "target-local"}, targetStore).Import(ctx, archive)
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	if result.Summary.Imported.L4Rules != 1 || result.Summary.Imported.WireGuardProfiles != 0 || result.Summary.SkippedConflict.WireGuardProfiles != 1 {
		t.Fatalf("import summary = %+v", result.Summary)
	}

	rows, err := targetStore.ListL4Rules(ctx, "edge-l4-wg")
	if err != nil {
		t.Fatalf("ListL4Rules() error = %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("l4 rules len = %d, want 1: %+v", len(rows), rows)
	}
	importedRule := rows[0]
	if importedRule.WireGuardProfileID == nil || *importedRule.WireGuardProfileID != existingProfileID {
		t.Fatalf("L4 wireguard profile ID = %v, want existing profile ID %d", importedRule.WireGuardProfileID, existingProfileID)
	}
	if importedRule.WireGuardEgressURI != "" {
		t.Fatalf("WireGuardEgressURI = %q, want empty for conflicted profile import", importedRule.WireGuardEgressURI)
	}

	l4Svc := NewL4RuleService(config.Config{EnableLocalAgent: true, LocalAgentID: "target-local"}, targetStore)
	if _, err := l4Svc.Delete(ctx, "edge-l4-wg", importedRule.ID); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	profiles, err := targetStore.ListWireGuardProfiles(ctx, "edge-l4-wg")
	if err != nil {
		t.Fatalf("ListWireGuardProfiles(after delete) error = %v", err)
	}
	if len(profiles) != 1 || profiles[0].ID != existingProfileID {
		t.Fatalf("wireguard profiles after L4 delete = %+v, want existing profile ID %d preserved", profiles, existingProfileID)
	}
}

func TestBackupServiceImportAcceptsLegacyL4WireGuardEgressBackupWithoutURI(t *testing.T) {
	ctx := t.Context()
	targetStore, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "l4-wg-legacy-target"), "target-local")
	if err != nil {
		t.Fatalf("NewSQLiteStore(target) error = %v", err)
	}
	defer targetStore.Close()

	if err := targetStore.SaveAgent(ctx, storage.AgentRow{
		ID:         "existing-agent",
		Name:       "existing-agent",
		AgentToken: "token-existing",
	}); err != nil {
		t.Fatalf("SaveAgent(existing) error = %v", err)
	}
	if err := targetStore.SaveWireGuardProfiles(ctx, "existing-agent", []storage.WireGuardProfileRow{{
		ID:            41,
		AgentID:       "existing-agent",
		Name:          "existing-wg",
		Mode:          "generic_wireguard",
		PrivateKey:    testWireGuardPrivateKey,
		ListenPort:    51821,
		AddressesJSON: `["10.60.0.1/24"]`,
		PeersJSON:     `[]`,
		DNSJSON:       `[]`,
		Enabled:       true,
		Revision:      9,
	}}); err != nil {
		t.Fatalf("SaveWireGuardProfiles(existing) error = %v", err)
	}

	sourceProfileID := 41
	ruleID := 71
	profileRow := materializedWireGuardURIProfileRowForRule(t, "edge-l4-wg", sourceProfileID, ruleID, "wireguard://"+testWireGuardPrivateKey+"@edge.example.com:51820?publickey="+testWireGuardPublicKey+"&address=10.44.0.2/32#URI%20egress")
	bundle := BackupBundle{
		Manifest: BackupManifest{
			PackageVersion:     BackupPackageVersion,
			SourceArchitecture: BackupSourceArchitectureGo,
			ExportedAt:         time.Date(2026, 5, 16, 0, 0, 0, 0, time.UTC),
		},
		Agents: []BackupAgent{{
			ID:           "edge-l4-wg",
			Name:         "edge-l4-wg",
			AgentToken:   "token-edge-l4-wg",
			Capabilities: []string{"l4", "wireguard"},
		}},
		WireGuardProfiles: []BackupWireGuardProfile{backupWireGuardProfileFromRow(profileRow)},
		L4Rules: []BackupL4Rule{{
			ID:                 ruleID,
			AgentID:            "edge-l4-wg",
			Name:               "wg-egress",
			Protocol:           "tcp",
			ListenHost:         "0.0.0.0",
			ListenPort:         9443,
			Backends:           []L4Backend{{Host: "127.0.0.1", Port: 9443}},
			LoadBalancing:      L4LoadBalancing{Strategy: "adaptive"},
			Tuning:             L4Tuning{ProxyProtocol: L4ProxyProtocolTuning{}},
			ListenMode:         "proxy",
			WireGuardProfileID: &sourceProfileID,
			ProxyEgressMode:    "wireguard",
			Enabled:            true,
			Tags:               []string{"wg"},
			Revision:           5,
		}},
	}
	archive, err := encodeBackupBundle(bundle)
	if err != nil {
		t.Fatalf("encodeBackupBundle() error = %v", err)
	}

	result, err := NewBackupService(config.Config{EnableLocalAgent: true, LocalAgentID: "target-local"}, targetStore).Import(ctx, archive)
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	if result.Summary.Imported.L4Rules != 1 || result.Summary.Imported.WireGuardProfiles != 1 {
		t.Fatalf("import summary = %+v", result.Summary)
	}

	rows, err := targetStore.ListL4Rules(ctx, "edge-l4-wg")
	if err != nil {
		t.Fatalf("ListL4Rules() error = %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("l4 rules len = %d, want 1: %+v", len(rows), rows)
	}
	row := rows[0]
	if row.ProxyEgressMode != "wireguard" {
		t.Fatalf("ProxyEgressMode = %q, want wireguard", row.ProxyEgressMode)
	}
	if row.WireGuardProfileID == nil {
		t.Fatalf("WireGuardProfileID = nil, want remapped profile ID")
	}
	if row.WireGuardEgressURI != "" {
		t.Fatalf("WireGuardEgressURI = %q, want empty for legacy backup", row.WireGuardEgressURI)
	}
	profiles, err := targetStore.ListWireGuardProfiles(ctx, "edge-l4-wg")
	if err != nil {
		t.Fatalf("ListWireGuardProfiles() error = %v", err)
	}
	if len(profiles) != 1 {
		t.Fatalf("wireguard profiles len = %d, want 1: %+v", len(profiles), profiles)
	}
	if row.WireGuardProfileID == nil || *row.WireGuardProfileID != profiles[0].ID {
		t.Fatalf("L4 wireguard profile ID = %v, want imported profile ID %d", row.WireGuardProfileID, profiles[0].ID)
	}
}

func TestBackupServiceExportIncludesHTTPWireGuardEntryFields(t *testing.T) {
	ctx := t.Context()
	sourceStore, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "http-wg-export-source"), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore(source) error = %v", err)
	}
	defer sourceStore.Close()

	if err := sourceStore.SaveAgent(ctx, storage.AgentRow{
		ID:         "edge-http-wg",
		Name:       "edge-http-wg",
		AgentToken: "token-edge-http-wg",
	}); err != nil {
		t.Fatalf("SaveAgent() error = %v", err)
	}
	profileID := 41
	if err := sourceStore.SaveWireGuardProfiles(ctx, "edge-http-wg", []storage.WireGuardProfileRow{{
		ID:            profileID,
		AgentID:       "edge-http-wg",
		Name:          "wg-http",
		Mode:          "generic_wireguard",
		PrivateKey:    testWireGuardPrivateKey,
		ListenPort:    51820,
		AddressesJSON: `["10.44.0.1/24"]`,
		PeersJSON:     `[]`,
		DNSJSON:       `[]`,
		Enabled:       true,
		Revision:      3,
	}}); err != nil {
		t.Fatalf("SaveWireGuardProfiles() error = %v", err)
	}
	if err := sourceStore.SaveHTTPRules(ctx, "edge-http-wg", []storage.HTTPRuleRow{{
		ID:                       11,
		AgentID:                  "edge-http-wg",
		FrontendURL:              "https://media.example.com",
		BackendsJSON:             `[{"url":"http://127.0.0.1:8096"}]`,
		LoadBalancingJSON:        `{"strategy":"adaptive"}`,
		Enabled:                  true,
		TagsJSON:                 `[]`,
		RelayLayersJSON:          `[]`,
		CustomHeadersJSON:        `[]`,
		WireGuardEntryEnabled:    true,
		WireGuardProfileID:       &profileID,
		WireGuardEntryListenHost: "10.44.0.1",
		WireGuardEntryListenPort: 18096,
		Revision:                 4,
	}}); err != nil {
		t.Fatalf("SaveHTTPRules() error = %v", err)
	}

	archive, _, err := NewBackupService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, sourceStore).Export(ctx)
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}
	var rules []map[string]any
	if err := json.Unmarshal(backupArchiveJSONFile(t, archive, "http_rules.json"), &rules); err != nil {
		t.Fatalf("unmarshal http_rules.json: %v", err)
	}
	if len(rules) != 1 {
		t.Fatalf("http rules len = %d, want 1", len(rules))
	}
	rule := rules[0]
	if rule["wireguard_entry_enabled"] != true {
		t.Fatalf("wireguard_entry_enabled = %#v, want true", rule["wireguard_entry_enabled"])
	}
	if rule["wireguard_profile_id"] != float64(profileID) {
		t.Fatalf("wireguard_profile_id = %#v, want %d", rule["wireguard_profile_id"], profileID)
	}
	if rule["wireguard_entry_listen_host"] != "10.44.0.1" {
		t.Fatalf("wireguard_entry_listen_host = %#v", rule["wireguard_entry_listen_host"])
	}
	if rule["wireguard_entry_listen_port"] != float64(18096) {
		t.Fatalf("wireguard_entry_listen_port = %#v, want 18096", rule["wireguard_entry_listen_port"])
	}
}

func TestBackupServiceImportPreservesHTTPWireGuardEntryFieldsAndRemapsProfileID(t *testing.T) {
	ctx := t.Context()
	targetStore, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "http-wg-import-target"), "target-local")
	if err != nil {
		t.Fatalf("NewSQLiteStore(target) error = %v", err)
	}
	defer targetStore.Close()

	sourceProfileID := 7
	bundle := BackupBundle{
		Manifest: BackupManifest{
			PackageVersion:     BackupPackageVersion,
			SourceArchitecture: BackupSourceArchitectureGo,
			ExportedAt:         time.Date(2026, 5, 16, 0, 0, 0, 0, time.UTC),
		},
		Agents: []BackupAgent{{
			ID:         "edge-http-wg",
			Name:       "edge-http-wg",
			AgentToken: "token-edge-http-wg",
			Capabilities: []string{
				"http_rules",
				"l4",
				"relay_quic",
				"wireguard",
			},
		}},
		WireGuardProfiles: []BackupWireGuardProfile{{
			ID:         sourceProfileID,
			AgentID:    "edge-http-wg",
			Name:       "wg-http",
			Mode:       "generic_wireguard",
			PrivateKey: testWireGuardPrivateKey,
			ListenPort: 51820,
			Addresses:  []string{"10.44.0.1/24"},
			Peers: []WireGuardPeer{{
				Name:       "peer-a",
				PublicKey:  testWireGuardPublicKey,
				AllowedIPs: []string{"10.44.0.2/32"},
			}},
			Enabled: true,
		}},
	}
	httpRules := []map[string]any{{
		"id":                          11,
		"agent_id":                    "edge-http-wg",
		"frontend_url":                "https://media.example.com",
		"backends":                    []map[string]any{{"url": "http://127.0.0.1:8096"}},
		"load_balancing":              map[string]any{"strategy": "adaptive"},
		"enabled":                     true,
		"proxy_redirect":              false,
		"relay_layers":                [][]int{},
		"pass_proxy_headers":          true,
		"custom_headers":              []map[string]any{},
		"wireguard_entry_enabled":     true,
		"wireguard_profile_id":        sourceProfileID,
		"wireguard_entry_listen_host": "10.44.0.1",
		"wireguard_entry_listen_port": 18096,
	}}
	archive, err := encodeBackupBundleWithHTTPRules(t, bundle, httpRules)
	if err != nil {
		t.Fatalf("encodeBackupBundleWithHTTPRules() error = %v", err)
	}

	if err := targetStore.SaveAgent(ctx, storage.AgentRow{
		ID:         "existing-agent",
		Name:       "existing-agent",
		AgentToken: "token-existing",
	}); err != nil {
		t.Fatalf("SaveAgent(target existing) error = %v", err)
	}
	if err := targetStore.SaveWireGuardProfiles(ctx, "existing-agent", []storage.WireGuardProfileRow{{
		ID:            sourceProfileID,
		AgentID:       "existing-agent",
		Name:          "existing-wg",
		Mode:          "generic_wireguard",
		PrivateKey:    testWireGuardPrivateKey,
		ListenPort:    51821,
		AddressesJSON: `["10.60.0.1/24"]`,
		PeersJSON:     `[]`,
		DNSJSON:       `[]`,
		Enabled:       true,
		Revision:      9,
	}}); err != nil {
		t.Fatalf("SaveWireGuardProfiles(target existing) error = %v", err)
	}

	result, err := NewBackupService(config.Config{EnableLocalAgent: true, LocalAgentID: "target-local"}, targetStore).Import(ctx, archive)
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	if result.Summary.Imported.HTTPRules != 1 || result.Summary.SkippedInvalid.HTTPRules != 0 {
		t.Fatalf("import summary = %+v", result.Summary)
	}
	profiles, err := targetStore.ListWireGuardProfiles(ctx, "edge-http-wg")
	if err != nil {
		t.Fatalf("ListWireGuardProfiles(edge-http-wg) error = %v", err)
	}
	if len(profiles) != 1 {
		t.Fatalf("profiles = %+v, want one imported profile", profiles)
	}
	importedProfileID := profiles[0].ID
	if importedProfileID == sourceProfileID {
		t.Fatalf("imported profile id = %d, want remapped away from existing id", importedProfileID)
	}
	rows, err := targetStore.ListHTTPRules(ctx, "edge-http-wg")
	if err != nil {
		t.Fatalf("ListHTTPRules(edge-http-wg) error = %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("http rules = %+v, want one imported rule", rows)
	}
	row := rows[0]
	if !row.WireGuardEntryEnabled {
		t.Fatalf("WireGuardEntryEnabled = false, want true")
	}
	if row.WireGuardProfileID == nil || *row.WireGuardProfileID != importedProfileID {
		t.Fatalf("WireGuardProfileID = %v, want %d", row.WireGuardProfileID, importedProfileID)
	}
	if row.WireGuardEntryListenHost != "10.44.0.1" || row.WireGuardEntryListenPort != 18096 {
		t.Fatalf("wireguard entry listen = %q:%d, want 10.44.0.1:18096", row.WireGuardEntryListenHost, row.WireGuardEntryListenPort)
	}
}

func TestBackupServiceImportSkipsHTTPWireGuardEntryWithUnmappedProfile(t *testing.T) {
	ctx := t.Context()
	targetStore, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "http-wg-missing-target"), "target-local")
	if err != nil {
		t.Fatalf("NewSQLiteStore(target) error = %v", err)
	}
	defer targetStore.Close()

	missingProfileID := 99
	bundle := BackupBundle{
		Manifest: BackupManifest{
			PackageVersion:     BackupPackageVersion,
			SourceArchitecture: BackupSourceArchitectureGo,
			ExportedAt:         time.Date(2026, 5, 16, 0, 0, 0, 0, time.UTC),
		},
		Agents: []BackupAgent{{
			ID:         "edge-http-wg",
			Name:       "edge-http-wg",
			AgentToken: "token-edge-http-wg",
		}},
	}
	httpRules := []map[string]any{{
		"id":                          11,
		"agent_id":                    "edge-http-wg",
		"frontend_url":                "https://missing-wg.example.com",
		"backends":                    []map[string]any{{"url": "http://127.0.0.1:8096"}},
		"load_balancing":              map[string]any{"strategy": "adaptive"},
		"enabled":                     true,
		"proxy_redirect":              false,
		"relay_layers":                [][]int{},
		"pass_proxy_headers":          true,
		"custom_headers":              []map[string]any{},
		"wireguard_entry_enabled":     true,
		"wireguard_profile_id":        missingProfileID,
		"wireguard_entry_listen_host": "10.70.0.1",
		"wireguard_entry_listen_port": 18096,
	}}
	archive, err := encodeBackupBundleWithHTTPRules(t, bundle, httpRules)
	if err != nil {
		t.Fatalf("encodeBackupBundleWithHTTPRules() error = %v", err)
	}

	result, err := NewBackupService(config.Config{EnableLocalAgent: true, LocalAgentID: "target-local"}, targetStore).Import(ctx, archive)
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	if result.Summary.SkippedInvalid.HTTPRules != 1 || result.Summary.Imported.HTTPRules != 0 {
		t.Fatalf("import summary = %+v", result.Summary)
	}
	rows, err := targetStore.ListHTTPRules(ctx, "edge-http-wg")
	if err != nil {
		t.Fatalf("ListHTTPRules(edge-http-wg) error = %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("http rules = %+v, want missing-profile rule skipped", rows)
	}
}

func TestBackupServiceImportSkipsHTTPWireGuardEntryWithDisabledProfile(t *testing.T) {
	ctx := t.Context()
	targetStore, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "http-wg-disabled-target"), "target-local")
	if err != nil {
		t.Fatalf("NewSQLiteStore(target) error = %v", err)
	}
	defer targetStore.Close()

	disabledProfileID := 7
	bundle := BackupBundle{
		Manifest: BackupManifest{
			PackageVersion:     BackupPackageVersion,
			SourceArchitecture: BackupSourceArchitectureGo,
			ExportedAt:         time.Date(2026, 5, 16, 0, 0, 0, 0, time.UTC),
		},
		Agents: []BackupAgent{{
			ID:         "edge-http-wg",
			Name:       "edge-http-wg",
			AgentToken: "token-edge-http-wg",
		}},
		WireGuardProfiles: []BackupWireGuardProfile{{
			ID:         disabledProfileID,
			AgentID:    "edge-http-wg",
			Name:       "wg-http-disabled",
			Mode:       "generic_wireguard",
			PrivateKey: testWireGuardPrivateKey,
			ListenPort: 51820,
			Addresses:  []string{"10.71.0.1/24"},
			Peers: []WireGuardPeer{{
				Name:       "peer-a",
				PublicKey:  testWireGuardPublicKey,
				AllowedIPs: []string{"10.71.0.2/32"},
			}},
			Enabled: false,
		}},
	}
	httpRules := []map[string]any{{
		"id":                          11,
		"agent_id":                    "edge-http-wg",
		"frontend_url":                "https://disabled-wg.example.com",
		"backends":                    []map[string]any{{"url": "http://127.0.0.1:8096"}},
		"load_balancing":              map[string]any{"strategy": "adaptive"},
		"enabled":                     true,
		"proxy_redirect":              false,
		"relay_layers":                [][]int{},
		"pass_proxy_headers":          true,
		"custom_headers":              []map[string]any{},
		"wireguard_entry_enabled":     true,
		"wireguard_profile_id":        disabledProfileID,
		"wireguard_entry_listen_host": "10.71.0.1",
		"wireguard_entry_listen_port": 18096,
	}}
	archive, err := encodeBackupBundleWithHTTPRules(t, bundle, httpRules)
	if err != nil {
		t.Fatalf("encodeBackupBundleWithHTTPRules() error = %v", err)
	}

	result, err := NewBackupService(config.Config{EnableLocalAgent: true, LocalAgentID: "target-local"}, targetStore).Import(ctx, archive)
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	if result.Summary.Imported.WireGuardProfiles != 1 || result.Summary.SkippedInvalid.WireGuardProfiles != 0 {
		t.Fatalf("wireguard profile summary = %+v", result.Summary)
	}
	if result.Summary.SkippedInvalid.HTTPRules != 1 || result.Summary.Imported.HTTPRules != 0 {
		t.Fatalf("http import summary = %+v", result.Summary)
	}
	profiles, err := targetStore.ListWireGuardProfiles(ctx, "edge-http-wg")
	if err != nil {
		t.Fatalf("ListWireGuardProfiles(edge-http-wg) error = %v", err)
	}
	if len(profiles) != 1 || profiles[0].Enabled {
		t.Fatalf("profiles = %+v, want one disabled imported profile", profiles)
	}
	rows, err := targetStore.ListHTTPRules(ctx, "edge-http-wg")
	if err != nil {
		t.Fatalf("ListHTTPRules(edge-http-wg) error = %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("http rules = %+v, want disabled-profile rule skipped", rows)
	}
}

func TestBackupL4RuleConversionPreservesWireGuardFields(t *testing.T) {
	profileID := 77
	uri := "wireguard://" + testWireGuardPrivateKey + "@edge.example.com:51820?publickey=" + testWireGuardPublicKey + "&address=10.44.0.2/32#URI%20egress"
	rule := L4Rule{
		ID:                   45,
		AgentID:              "edge-wg",
		Name:                 "wireguard l4",
		Protocol:             "tcp",
		ListenHost:           "0.0.0.0",
		ListenPort:           9443,
		Backends:             []L4Backend{{Host: "127.0.0.1", Port: 9443}},
		LoadBalancing:        L4LoadBalancing{Strategy: "adaptive"},
		Tuning:               L4Tuning{ProxyProtocol: L4ProxyProtocolTuning{}},
		ListenMode:           "wireguard",
		WireGuardProfileID:   &profileID,
		WireGuardInboundMode: "address",
		WireGuardListenHost:  "10.44.0.1",
		WireGuardEgressURI:   uri,
		Enabled:              true,
		Tags:                 []string{"wg"},
		Revision:             9,
	}

	backupRule := backupL4RuleFromRule(rule)
	if backupRule.WireGuardProfileID == nil || *backupRule.WireGuardProfileID != profileID {
		t.Fatalf("backup WireGuardProfileID = %v, want %d", backupRule.WireGuardProfileID, profileID)
	}
	if backupRule.WireGuardListenHost != "10.44.0.1" {
		t.Fatalf("backup WireGuardListenHost = %q, want 10.44.0.1", backupRule.WireGuardListenHost)
	}
	if backupRule.WireGuardInboundMode != "address" {
		t.Fatalf("backup WireGuardInboundMode = %q, want address", backupRule.WireGuardInboundMode)
	}
	if backupRule.WireGuardEgressURI != uri {
		t.Fatalf("backup WireGuardEgressURI = %q, want %q", backupRule.WireGuardEgressURI, uri)
	}

	input := l4RuleInputFromBackup(backupRule, nil, backupRule.WireGuardProfileID)
	if input.WireGuardProfileID == nil || *input.WireGuardProfileID != profileID {
		t.Fatalf("input WireGuardProfileID = %v, want %d", input.WireGuardProfileID, profileID)
	}
	if input.WireGuardListenHost == nil || *input.WireGuardListenHost != "10.44.0.1" {
		t.Fatalf("input WireGuardListenHost = %v, want 10.44.0.1", input.WireGuardListenHost)
	}
	if input.WireGuardInboundMode == nil || *input.WireGuardInboundMode != "address" {
		t.Fatalf("input WireGuardInboundMode = %v, want address", input.WireGuardInboundMode)
	}
	if input.WireGuardEgressURI == nil || *input.WireGuardEgressURI != uri {
		t.Fatalf("input WireGuardEgressURI = %v, want %q", input.WireGuardEgressURI, uri)
	}
}

func TestBackupServiceExportIncludesWireGuardProfilesWithRawSecrets(t *testing.T) {
	ctx := t.Context()
	sourceStore, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "wg-export-source"), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore(source) error = %v", err)
	}
	defer sourceStore.Close()

	if err := sourceStore.SaveAgent(ctx, storage.AgentRow{
		ID:         "edge-wg",
		Name:       "edge-wg",
		AgentToken: "token-edge-wg",
	}); err != nil {
		t.Fatalf("SaveAgent() error = %v", err)
	}
	if err := sourceStore.SaveWireGuardProfiles(ctx, "edge-wg", []storage.WireGuardProfileRow{{
		ID:             41,
		AgentID:        "edge-wg",
		Name:           "wg-egress",
		Mode:           "generic_wireguard",
		PrivateKey:     testWireGuardPrivateKey,
		ListenPort:     51820,
		PublicEndpoint: "wg.example.com:51820",
		AddressesJSON:  `["10.44.0.2/32"]`,
		PeersJSON: marshalJSON([]WireGuardPeer{{
			Name:         "relay",
			PublicKey:    testWireGuardPublicKey,
			PresharedKey: testWireGuardPresharedKey,
			Endpoint:     "relay.example.com:51820",
			AllowedIPs:   []string{"0.0.0.0/0"},
		}}, "[]"),
		DNSJSON:  `["1.1.1.1"]`,
		MTU:      1420,
		Enabled:  true,
		TagsJSON: `["wg"]`,
		Revision: 3,
	}}); err != nil {
		t.Fatalf("SaveWireGuardProfiles() error = %v", err)
	}

	archive, _, err := NewBackupService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, sourceStore).Export(ctx)
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}

	files := backupArchiveFileNames(t, archive)
	if !files["wireguard_profiles.json"] {
		t.Fatalf("backup files missing wireguard_profiles.json: %#v", files)
	}

	bundle, err := decodeBackupBundle(archive)
	if err != nil {
		t.Fatalf("decodeBackupBundle() error = %v", err)
	}
	if bundle.Manifest.Counts.WireGuardProfiles != 1 {
		t.Fatalf("wireguard profile count = %d, want 1", bundle.Manifest.Counts.WireGuardProfiles)
	}
	if len(bundle.WireGuardProfiles) != 1 {
		t.Fatalf("wireguard profiles len = %d, want 1", len(bundle.WireGuardProfiles))
	}
	profile := bundle.WireGuardProfiles[0]
	if profile.PrivateKey != testWireGuardPrivateKey {
		t.Fatalf("private_key = %q, want raw key", profile.PrivateKey)
	}
	if len(profile.Peers) != 1 || profile.Peers[0].PresharedKey != testWireGuardPresharedKey {
		t.Fatalf("peers = %+v, want raw preshared key", profile.Peers)
	}
	if profile.PublicEndpoint != "wg.example.com:51820" {
		t.Fatalf("public_endpoint = %q, want wg.example.com:51820", profile.PublicEndpoint)
	}
}

func TestBackupServiceExportIncludesWireGuardClientsWithRawSecretsAndDisabledRows(t *testing.T) {
	ctx := t.Context()
	sourceStore, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "wg-client-export-source"), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore(source) error = %v", err)
	}
	defer sourceStore.Close()

	if err := sourceStore.SaveAgent(ctx, storage.AgentRow{
		ID:         "edge-wg",
		Name:       "edge-wg",
		AgentToken: "token-edge-wg",
	}); err != nil {
		t.Fatalf("SaveAgent() error = %v", err)
	}
	if err := sourceStore.SaveWireGuardProfiles(ctx, "edge-wg", []storage.WireGuardProfileRow{{
		ID:            41,
		AgentID:       "edge-wg",
		Name:          "wg-clients",
		Mode:          "generic_wireguard",
		PrivateKey:    testWireGuardPrivateKey,
		ListenPort:    51820,
		AddressesJSON: `["10.44.0.1/24"]`,
		PeersJSON:     `[]`,
		DNSJSON:       `["1.1.1.1"]`,
		Enabled:       true,
		Revision:      3,
	}}); err != nil {
		t.Fatalf("SaveWireGuardProfiles() error = %v", err)
	}
	if err := sourceStore.SaveWireGuardClients(ctx, "edge-wg", 41, []storage.WireGuardClientRow{
		{
			ID:             1,
			AgentID:        "edge-wg",
			ProfileID:      41,
			Name:           "phone",
			PrivateKey:     testWireGuardPrivateKey,
			PublicKey:      testWireGuardPublicKey,
			PresharedKey:   testWireGuardPresharedKey,
			Address:        "10.44.0.2/32",
			AllowedIPsJSON: `["0.0.0.0/0"]`,
			DNSJSON:        `["1.1.1.1"]`,
			Enabled:        true,
			CreatedAt:      "2026-05-16T10:00:00Z",
			UpdatedAt:      "2026-05-16T10:01:00Z",
		},
		{
			ID:             2,
			AgentID:        "edge-wg",
			ProfileID:      41,
			Name:           "tablet",
			PrivateKey:     testWireGuardPresharedKey,
			PublicKey:      testWireGuardPublicKeyB,
			PresharedKey:   testWireGuardPresharedKeyB,
			Address:        "10.44.0.3/32",
			AllowedIPsJSON: `["10.44.0.0/24"]`,
			DNSJSON:        `["9.9.9.9"]`,
			Enabled:        false,
			CreatedAt:      "2026-05-16T11:00:00Z",
			UpdatedAt:      "2026-05-16T11:01:00Z",
		},
	}); err != nil {
		t.Fatalf("SaveWireGuardClients() error = %v", err)
	}

	archive, _, err := NewBackupService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, sourceStore).Export(ctx)
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}

	files := backupArchiveFileNames(t, archive)
	if !files["wireguard_clients.json"] {
		t.Fatalf("backup files missing wireguard_clients.json: %#v", files)
	}
	bundle, err := decodeBackupBundle(archive)
	if err != nil {
		t.Fatalf("decodeBackupBundle() error = %v", err)
	}
	var counts map[string]int
	countsRaw, err := json.Marshal(bundle.Manifest.Counts)
	if err != nil {
		t.Fatalf("marshal manifest counts: %v", err)
	}
	if err := json.Unmarshal(countsRaw, &counts); err != nil {
		t.Fatalf("unmarshal manifest counts: %v", err)
	}
	if counts["wireguard_clients"] != 2 {
		t.Fatalf("wireguard client count = %d, want 2", counts["wireguard_clients"])
	}
	var clients []testBackupWireGuardClient
	if err := json.Unmarshal(backupArchiveJSONFile(t, archive, "wireguard_clients.json"), &clients); err != nil {
		t.Fatalf("unmarshal wireguard_clients.json: %v", err)
	}
	if len(clients) != 2 {
		t.Fatalf("wireguard clients len = %d, want 2", len(clients))
	}
	clientsByName := map[string]testBackupWireGuardClient{}
	for _, client := range clients {
		clientsByName[client.Name] = client
	}
	phone := clientsByName["phone"]
	if phone.PrivateKey != testWireGuardPrivateKey || phone.PublicKey != testWireGuardPublicKey || phone.PresharedKey != testWireGuardPresharedKey {
		t.Fatalf("phone secrets = private %q public %q psk %q, want raw secrets", phone.PrivateKey, phone.PublicKey, phone.PresharedKey)
	}
	if phone.AgentID != "edge-wg" || phone.ProfileID != 41 || phone.Address != "10.44.0.2/32" || !phone.Enabled {
		t.Fatalf("phone client = %+v, want source identifiers/address/enabled", phone)
	}
	if len(phone.AllowedIPs) != 1 || phone.AllowedIPs[0] != "0.0.0.0/0" || len(phone.DNS) != 1 || phone.DNS[0] != "1.1.1.1" {
		t.Fatalf("phone client routes/dns = allowed %+v dns %+v", phone.AllowedIPs, phone.DNS)
	}
	tablet := clientsByName["tablet"]
	if tablet.Enabled {
		t.Fatalf("tablet Enabled = true, want disabled row preserved")
	}
	if tablet.PrivateKey != testWireGuardPresharedKey || tablet.PresharedKey != testWireGuardPresharedKeyB {
		t.Fatalf("tablet secrets = private %q psk %q, want raw disabled secrets", tablet.PrivateKey, tablet.PresharedKey)
	}
}

func TestBackupServiceImportRestoresWireGuardProfileAndRemapsRelayAndL4References(t *testing.T) {
	ctx := t.Context()
	sourceStore, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "wg-import-source"), "source-local")
	if err != nil {
		t.Fatalf("NewSQLiteStore(source) error = %v", err)
	}
	defer sourceStore.Close()
	targetStore, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "wg-import-target"), "target-local")
	if err != nil {
		t.Fatalf("NewSQLiteStore(target) error = %v", err)
	}
	defer targetStore.Close()

	if err := sourceStore.SaveAgent(ctx, storage.AgentRow{
		ID:               "edge-wg",
		Name:             "edge-wg",
		AgentToken:       "token-edge-wg",
		CapabilitiesJSON: `["cert_install","wireguard"]`,
	}); err != nil {
		t.Fatalf("SaveAgent(source) error = %v", err)
	}
	sourceProfileID := 7
	if err := sourceStore.SaveWireGuardProfiles(ctx, "edge-wg", []storage.WireGuardProfileRow{{
		ID:             sourceProfileID,
		AgentID:        "edge-wg",
		Name:           "wg-relay",
		Mode:           "generic_wireguard",
		PrivateKey:     testWireGuardPrivateKey,
		ListenPort:     51820,
		PublicEndpoint: "wg.example.com:51820",
		AddressesJSON:  `["10.50.0.2/32"]`,
		PeersJSON: marshalJSON([]WireGuardPeer{{
			Name:         "peer-a",
			PublicKey:    testWireGuardPublicKey,
			PresharedKey: testWireGuardPresharedKey,
			Endpoint:     "relay.example.com:51820",
			AllowedIPs:   []string{"10.50.0.1/32"},
		}}, "[]"),
		DNSJSON:  `[]`,
		MTU:      1420,
		Enabled:  true,
		TagsJSON: `["wg"]`,
		Revision: 2,
	}}); err != nil {
		t.Fatalf("SaveWireGuardProfiles(source) error = %v", err)
	}
	if err := sourceStore.SaveManagedCertificates(ctx, []storage.ManagedCertificateRow{{
		ID:              21,
		Domain:          "relay.example.com",
		Enabled:         true,
		Scope:           "domain",
		IssuerMode:      "local_http01",
		TargetAgentIDs:  `["edge-wg"]`,
		Status:          "active",
		MaterialHash:    "relay-cert-hash",
		AgentReports:    `{}`,
		ACMEInfo:        `{}`,
		Usage:           "relay_tunnel",
		CertificateType: "uploaded",
		TagsJSON:        `["relay"]`,
		Revision:        2,
	}}); err != nil {
		t.Fatalf("SaveManagedCertificates(source) error = %v", err)
	}
	if err := sourceStore.SaveManagedCertificateMaterial(ctx, "relay.example.com", storage.ManagedCertificateBundle{
		Domain:  "relay.example.com",
		CertPEM: "relay-cert-pem",
		KeyPEM:  "relay-key-pem",
	}); err != nil {
		t.Fatalf("SaveManagedCertificateMaterial(source) error = %v", err)
	}
	if err := sourceStore.SaveRelayListeners(ctx, "edge-wg", []storage.RelayListenerRow{{
		ID:                      70,
		AgentID:                 "edge-wg",
		Name:                    "relay-wg",
		ListenHost:              "0.0.0.0",
		BindHostsJSON:           `["0.0.0.0"]`,
		ListenPort:              7443,
		PublicHost:              "relay.example.com",
		PublicPort:              7443,
		Enabled:                 true,
		CertificateID:           backupIntPtr(21),
		TLSMode:                 "pin_only",
		TransportMode:           "wireguard",
		WireGuardProfileID:      &sourceProfileID,
		ObfsMode:                "off",
		PinSetJSON:              `[{"type":"spki_sha256","value":"fixture-pin"}]`,
		TrustedCACertificateIDs: `[]`,
		TagsJSON:                `["relay"]`,
		Revision:                2,
	}}); err != nil {
		t.Fatalf("SaveRelayListeners(source) error = %v", err)
	}
	if err := sourceStore.SaveL4Rules(ctx, "edge-wg", []storage.L4RuleRow{{
		ID:                  71,
		AgentID:             "edge-wg",
		Name:                "wg-l4",
		Protocol:            "udp",
		ListenHost:          "0.0.0.0",
		ListenPort:          51820,
		UpstreamHost:        "10.50.0.1",
		UpstreamPort:        51820,
		BackendsJSON:        `[{"host":"10.50.0.1","port":51820}]`,
		LoadBalancingJSON:   `{"strategy":"round_robin"}`,
		TuningJSON:          `{}`,
		RelayChainJSON:      `[]`,
		RelayLayersJSON:     `[]`,
		ListenMode:          "wireguard",
		WireGuardProfileID:  &sourceProfileID,
		WireGuardListenHost: "10.50.0.2",
		Enabled:             true,
		TagsJSON:            `["l4"]`,
		Revision:            2,
	}}); err != nil {
		t.Fatalf("SaveL4Rules(source) error = %v", err)
	}

	if err := targetStore.SaveAgent(ctx, storage.AgentRow{
		ID:         "existing-agent",
		Name:       "existing-agent",
		AgentToken: "token-existing",
	}); err != nil {
		t.Fatalf("SaveAgent(target existing) error = %v", err)
	}
	if err := targetStore.SaveWireGuardProfiles(ctx, "existing-agent", []storage.WireGuardProfileRow{{
		ID:            sourceProfileID,
		AgentID:       "existing-agent",
		Name:          "existing-wg",
		Mode:          "generic_wireguard",
		PrivateKey:    testWireGuardPrivateKey,
		ListenPort:    51821,
		AddressesJSON: `["10.60.0.2/32"]`,
		PeersJSON:     `[]`,
		DNSJSON:       `[]`,
		Enabled:       true,
		Revision:      9,
	}}); err != nil {
		t.Fatalf("SaveWireGuardProfiles(target existing) error = %v", err)
	}

	archive, _, err := NewBackupService(config.Config{EnableLocalAgent: true, LocalAgentID: "source-local"}, sourceStore).Export(ctx)
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}

	result, err := NewBackupService(config.Config{EnableLocalAgent: true, LocalAgentID: "target-local"}, targetStore).Import(ctx, archive)
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	if result.Summary.Imported.RelayListeners != 1 || result.Summary.Imported.L4Rules != 1 {
		t.Fatalf("import summary = %+v", result.Summary)
	}

	importedProfiles, err := targetStore.ListWireGuardProfiles(ctx, "edge-wg")
	if err != nil {
		t.Fatalf("ListWireGuardProfiles(edge-wg) error = %v", err)
	}
	if len(importedProfiles) != 1 {
		t.Fatalf("imported profiles = %+v", importedProfiles)
	}
	importedProfileID := importedProfiles[0].ID
	if importedProfileID == sourceProfileID {
		t.Fatalf("imported profile id = %d, want remapped away from existing id", importedProfileID)
	}
	if importedProfiles[0].PrivateKey != testWireGuardPrivateKey {
		t.Fatalf("imported private_key = %q, want raw key", importedProfiles[0].PrivateKey)
	}
	if importedProfiles[0].PublicEndpoint != "wg.example.com:51820" {
		t.Fatalf("imported public_endpoint = %q, want wg.example.com:51820", importedProfiles[0].PublicEndpoint)
	}
	if importedProfiles[0].Revision == 0 {
		t.Fatalf("imported profile revision = 0, want allocated revision")
	}

	listeners, err := targetStore.ListRelayListeners(ctx, "edge-wg")
	if err != nil {
		t.Fatalf("ListRelayListeners(edge-wg) error = %v", err)
	}
	if len(listeners) != 1 || listeners[0].WireGuardProfileID == nil || *listeners[0].WireGuardProfileID != importedProfileID {
		t.Fatalf("imported relay listeners = %+v, want profile id %d", listeners, importedProfileID)
	}

	l4Rules, err := targetStore.ListL4Rules(ctx, "edge-wg")
	if err != nil {
		t.Fatalf("ListL4Rules(edge-wg) error = %v", err)
	}
	if len(l4Rules) != 1 || l4Rules[0].WireGuardProfileID == nil || *l4Rules[0].WireGuardProfileID != importedProfileID {
		t.Fatalf("imported l4 rules = %+v, want profile id %d", l4Rules, importedProfileID)
	}
	if l4Rules[0].WireGuardListenHost != "10.50.0.2" {
		t.Fatalf("WireGuardListenHost = %q, want 10.50.0.2", l4Rules[0].WireGuardListenHost)
	}
}

func TestBackupServiceImportRestoresWireGuardClientsAndReconcilesProfilePeers(t *testing.T) {
	ctx := t.Context()
	targetStore, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "wg-client-import-target"), "target-local")
	if err != nil {
		t.Fatalf("NewSQLiteStore(target) error = %v", err)
	}
	defer targetStore.Close()

	sourceProfileID := 7
	bundle := BackupBundle{
		Manifest: BackupManifest{
			PackageVersion:     BackupPackageVersion,
			SourceArchitecture: BackupSourceArchitectureGo,
			ExportedAt:         time.Date(2026, 5, 16, 0, 0, 0, 0, time.UTC),
		},
		Agents: []BackupAgent{{
			ID:         "edge-wg",
			Name:       "edge-wg",
			AgentToken: "token-edge-wg",
			Capabilities: []string{
				"l4",
				"wireguard",
			},
		}},
		WireGuardProfiles: []BackupWireGuardProfile{{
			ID:             sourceProfileID,
			AgentID:        "edge-wg",
			Name:           "wg-clients",
			Mode:           "generic_wireguard",
			PrivateKey:     testWireGuardPrivateKey,
			ListenPort:     51820,
			PublicEndpoint: "wg.example.com:51820",
			Addresses:      []string{"10.44.0.1/24"},
			Peers: []WireGuardPeer{{
				Name:         "phone",
				PublicKey:    testWireGuardPublicKey,
				PresharedKey: testWireGuardPresharedKey,
				AllowedIPs:   []string{"10.44.0.2/32"},
			}},
			DNS:      []string{"1.1.1.1"},
			MTU:      1420,
			Enabled:  true,
			Revision: 3,
		}},
	}
	clients := []testBackupWireGuardClient{
		{
			ID:           1,
			AgentID:      "edge-wg",
			ProfileID:    sourceProfileID,
			Name:         "phone",
			PrivateKey:   testWireGuardPrivateKey,
			PublicKey:    testWireGuardPublicKey,
			PresharedKey: testWireGuardPresharedKey,
			Address:      "10.44.0.2/32",
			AllowedIPs:   []string{"0.0.0.0/0"},
			DNS:          []string{"1.1.1.1"},
			Enabled:      true,
			CreatedAt:    "2026-05-16T10:00:00Z",
			UpdatedAt:    "2026-05-16T10:01:00Z",
		},
		{
			ID:           2,
			AgentID:      "edge-wg",
			ProfileID:    sourceProfileID,
			Name:         "laptop",
			PrivateKey:   testWireGuardPresharedKey,
			PublicKey:    testWireGuardPublicKeyB,
			PresharedKey: testWireGuardPresharedKeyB,
			Address:      "10.44.0.3/32",
			AllowedIPs:   []string{"10.44.0.0/24"},
			DNS:          []string{"9.9.9.9"},
			Enabled:      true,
			CreatedAt:    "2026-05-16T11:00:00Z",
			UpdatedAt:    "2026-05-16T11:01:00Z",
		},
		{
			ID:           3,
			AgentID:      "edge-wg",
			ProfileID:    sourceProfileID,
			Name:         "disabled",
			PrivateKey:   "FFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFF=",
			PublicKey:    "GGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGG=",
			PresharedKey: "HHHHHHHHHHHHHHHHHHHHHHHHHHHHHHHHHHHHHHHHHHH=",
			Address:      "10.44.0.4/32",
			AllowedIPs:   []string{"10.44.0.0/24"},
			DNS:          []string{"8.8.8.8"},
			Enabled:      false,
			CreatedAt:    "2026-05-16T12:00:00Z",
			UpdatedAt:    "2026-05-16T12:01:00Z",
		},
	}
	archive, err := encodeBackupBundleWithWireGuardClients(t, bundle, clients)
	if err != nil {
		t.Fatalf("encodeBackupBundleWithWireGuardClients() error = %v", err)
	}

	if err := targetStore.SaveAgent(ctx, storage.AgentRow{
		ID:         "existing-agent",
		Name:       "existing-agent",
		AgentToken: "token-existing",
	}); err != nil {
		t.Fatalf("SaveAgent(target existing) error = %v", err)
	}
	if err := targetStore.SaveWireGuardProfiles(ctx, "existing-agent", []storage.WireGuardProfileRow{{
		ID:            sourceProfileID,
		AgentID:       "existing-agent",
		Name:          "existing-wg",
		Mode:          "generic_wireguard",
		PrivateKey:    testWireGuardPrivateKey,
		ListenPort:    51821,
		AddressesJSON: `["10.60.0.1/24"]`,
		PeersJSON:     `[]`,
		DNSJSON:       `[]`,
		Enabled:       true,
		Revision:      9,
	}}); err != nil {
		t.Fatalf("SaveWireGuardProfiles(target existing) error = %v", err)
	}

	result, err := NewBackupService(config.Config{EnableLocalAgent: true, LocalAgentID: "target-local"}, targetStore).Import(ctx, archive)
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	var importedCounts map[string]int
	importedRaw, err := json.Marshal(result.Summary.Imported)
	if err != nil {
		t.Fatalf("marshal imported summary: %v", err)
	}
	if err := json.Unmarshal(importedRaw, &importedCounts); err != nil {
		t.Fatalf("unmarshal imported summary: %v", err)
	}
	if result.Summary.Imported.WireGuardProfiles != 1 || importedCounts["wireguard_clients"] != 3 {
		t.Fatalf("import summary = %+v", result.Summary)
	}

	profiles, err := targetStore.ListWireGuardProfiles(ctx, "edge-wg")
	if err != nil {
		t.Fatalf("ListWireGuardProfiles(edge-wg) error = %v", err)
	}
	if len(profiles) != 1 {
		t.Fatalf("profiles = %+v, want one imported profile", profiles)
	}
	importedProfileID := profiles[0].ID
	if importedProfileID == sourceProfileID {
		t.Fatalf("imported profile id = %d, want remapped away from existing id", importedProfileID)
	}
	rows, err := targetStore.ListWireGuardClients(ctx, "edge-wg", importedProfileID)
	if err != nil {
		t.Fatalf("ListWireGuardClients(edge-wg) error = %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("wireguard clients = %+v, want three restored rows", rows)
	}
	clientsByName := map[string]storage.WireGuardClientRow{}
	for _, row := range rows {
		clientsByName[row.Name] = row
		if row.AgentID != "edge-wg" || row.ProfileID != importedProfileID {
			t.Fatalf("client row = %+v, want remapped agent/profile", row)
		}
	}
	phone := clientsByName["phone"]
	if phone.PrivateKey != testWireGuardPrivateKey || phone.PublicKey != testWireGuardPublicKey || phone.PresharedKey != testWireGuardPresharedKey {
		t.Fatalf("phone secrets = %+v, want source raw secrets", phone)
	}
	if phone.Address != "10.44.0.2/32" || phone.AllowedIPsJSON != `["0.0.0.0/0"]` || phone.DNSJSON != `["1.1.1.1"]` || !phone.Enabled {
		t.Fatalf("phone row = %+v, want restored address/routes/dns/enabled", phone)
	}
	disabled := clientsByName["disabled"]
	if disabled.Enabled {
		t.Fatalf("disabled client row = %+v, want disabled preserved", disabled)
	}

	profile := wireGuardProfileFromRow(profiles[0])
	peerCounts := map[string]int{}
	for _, peer := range profile.Peers {
		peerCounts[peer.PublicKey]++
	}
	if peerCounts[testWireGuardPublicKey] != 1 || peerCounts[testWireGuardPublicKeyB] != 1 {
		t.Fatalf("profile peers = %+v, want one peer for each enabled client", profile.Peers)
	}
	if peerCounts["GGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGG="] != 0 {
		t.Fatalf("profile peers = %+v, want disabled client omitted", profile.Peers)
	}
}

func TestBackupServiceImportRestoresWireGuardClientsForDisabledProfile(t *testing.T) {
	ctx := t.Context()
	targetStore, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "wg-client-disabled-profile-target"), "target-local")
	if err != nil {
		t.Fatalf("NewSQLiteStore(target) error = %v", err)
	}
	defer targetStore.Close()

	profileID := 7
	bundle := BackupBundle{
		Manifest: BackupManifest{
			PackageVersion:     BackupPackageVersion,
			SourceArchitecture: BackupSourceArchitectureGo,
			ExportedAt:         time.Date(2026, 5, 16, 0, 0, 0, 0, time.UTC),
		},
		Agents: []BackupAgent{{
			ID:         "edge-wg-disabled",
			Name:       "edge-wg-disabled",
			AgentToken: "token-edge-wg-disabled",
		}},
		WireGuardProfiles: []BackupWireGuardProfile{{
			ID:         profileID,
			AgentID:    "edge-wg-disabled",
			Name:       "wg-disabled",
			Mode:       "generic_wireguard",
			PrivateKey: testWireGuardPrivateKey,
			Addresses:  []string{"10.45.0.1/24"},
			Peers:      []WireGuardPeer{},
			Enabled:    false,
		}},
	}
	archive, err := encodeBackupBundleWithWireGuardClients(t, bundle, []testBackupWireGuardClient{{
		ID:           1,
		AgentID:      "edge-wg-disabled",
		ProfileID:    profileID,
		Name:         "disabled-profile-client",
		PrivateKey:   testWireGuardPrivateKey,
		PublicKey:    testWireGuardPublicKey,
		PresharedKey: testWireGuardPresharedKey,
		Address:      "10.45.0.2/32",
		AllowedIPs:   []string{"10.45.0.0/24"},
		DNS:          []string{"1.1.1.1"},
		Enabled:      false,
		CreatedAt:    "2026-05-16T10:00:00Z",
		UpdatedAt:    "2026-05-16T10:01:00Z",
	}})
	if err != nil {
		t.Fatalf("encodeBackupBundleWithWireGuardClients() error = %v", err)
	}

	result, err := NewBackupService(config.Config{EnableLocalAgent: true, LocalAgentID: "target-local"}, targetStore).Import(ctx, archive)
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	if result.Summary.Imported.WireGuardClients != 1 || result.Summary.SkippedInvalid.WireGuardClients != 0 {
		t.Fatalf("import summary = %+v", result.Summary)
	}
	rows, err := targetStore.ListWireGuardClients(ctx, "edge-wg-disabled", profileID)
	if err != nil {
		t.Fatalf("ListWireGuardClients(edge-wg-disabled) error = %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("wireguard clients = %+v, want one restored row", rows)
	}
	if rows[0].Enabled || rows[0].PrivateKey != testWireGuardPrivateKey || rows[0].Address != "10.45.0.2/32" {
		t.Fatalf("restored disabled-profile client = %+v", rows[0])
	}
}

func TestBackupServiceImportSkipsWireGuardClientsForConflictingProfileWithoutTouchingExistingRowsOrPeers(t *testing.T) {
	ctx := t.Context()
	targetStore, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "wg-client-conflict-target"), "target-local")
	if err != nil {
		t.Fatalf("NewSQLiteStore(target) error = %v", err)
	}
	defer targetStore.Close()

	if err := targetStore.SaveAgent(ctx, storage.AgentRow{
		ID:         "edge-wg",
		Name:       "edge-wg",
		AgentToken: "token-edge-wg",
	}); err != nil {
		t.Fatalf("SaveAgent(target) error = %v", err)
	}
	profileID := 7
	existingPeers := []WireGuardPeer{
		{
			Name:         "existing-phone",
			PublicKey:    testWireGuardPublicKey,
			PresharedKey: testWireGuardPresharedKey,
			AllowedIPs:   []string{"10.90.0.2/32"},
		},
		{
			Name:       "manual-peer",
			PublicKey:  testWireGuardPublicKeyB,
			AllowedIPs: []string{"10.90.0.99/32"},
		},
	}
	if err := targetStore.SaveWireGuardProfiles(ctx, "edge-wg", []storage.WireGuardProfileRow{{
		ID:            profileID,
		AgentID:       "edge-wg",
		Name:          "wg-conflict",
		Mode:          "generic_wireguard",
		PrivateKey:    testWireGuardPrivateKey,
		AddressesJSON: `["10.90.0.1/24"]`,
		PeersJSON:     marshalJSON(existingPeers, "[]"),
		DNSJSON:       `[]`,
		Enabled:       true,
		Revision:      12,
	}}); err != nil {
		t.Fatalf("SaveWireGuardProfiles(target) error = %v", err)
	}
	existingClients := []storage.WireGuardClientRow{{
		ID:             1,
		AgentID:        "edge-wg",
		ProfileID:      profileID,
		Name:           "existing-phone",
		PrivateKey:     testWireGuardPrivateKey,
		PublicKey:      testWireGuardPublicKey,
		PresharedKey:   testWireGuardPresharedKey,
		Address:        "10.90.0.2/32",
		AllowedIPsJSON: `["0.0.0.0/0"]`,
		DNSJSON:        `["1.1.1.1"]`,
		Enabled:        true,
		CreatedAt:      "2026-05-16T08:00:00Z",
		UpdatedAt:      "2026-05-16T08:01:00Z",
	}}
	if err := targetStore.SaveWireGuardClients(ctx, "edge-wg", profileID, existingClients); err != nil {
		t.Fatalf("SaveWireGuardClients(target) error = %v", err)
	}

	bundle := BackupBundle{
		Manifest: BackupManifest{
			PackageVersion:     BackupPackageVersion,
			SourceArchitecture: BackupSourceArchitectureGo,
			ExportedAt:         time.Date(2026, 5, 16, 0, 0, 0, 0, time.UTC),
		},
		Agents: []BackupAgent{{
			ID:         "edge-wg",
			Name:       "edge-wg",
			AgentToken: "token-edge-wg",
			Capabilities: []string{
				"l4",
				"wireguard",
			},
		}},
		WireGuardProfiles: []BackupWireGuardProfile{{
			ID:         profileID,
			AgentID:    "edge-wg",
			Name:       "wg-conflict",
			Mode:       "generic_wireguard",
			PrivateKey: testWireGuardPrivateKey,
			Addresses:  []string{"10.91.0.1/24"},
			Peers:      []WireGuardPeer{},
			Enabled:    true,
		}},
	}
	archive, err := encodeBackupBundleWithWireGuardClients(t, bundle, []testBackupWireGuardClient{{
		ID:           99,
		AgentID:      "edge-wg",
		ProfileID:    profileID,
		Name:         "incoming-phone",
		PrivateKey:   testWireGuardPresharedKey,
		PublicKey:    testWireGuardPublicKeyB,
		PresharedKey: testWireGuardPresharedKeyB,
		Address:      "10.91.0.2/32",
		AllowedIPs:   []string{"10.91.0.0/24"},
		DNS:          []string{"9.9.9.9"},
		Enabled:      true,
		CreatedAt:    "2026-05-16T09:00:00Z",
		UpdatedAt:    "2026-05-16T09:01:00Z",
	}})
	if err != nil {
		t.Fatalf("encodeBackupBundleWithWireGuardClients() error = %v", err)
	}

	result, err := NewBackupService(config.Config{EnableLocalAgent: true, LocalAgentID: "target-local"}, targetStore).Import(ctx, archive)
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	if result.Summary.SkippedConflict.WireGuardProfiles != 1 || result.Summary.Imported.WireGuardProfiles != 0 {
		t.Fatalf("wireguard profile summary = imported %+v skipped conflict %+v", result.Summary.Imported, result.Summary.SkippedConflict)
	}
	if result.Summary.SkippedConflict.WireGuardClients != 1 || result.Summary.Imported.WireGuardClients != 0 {
		t.Fatalf("wireguard client summary = imported %+v skipped conflict %+v", result.Summary.Imported, result.Summary.SkippedConflict)
	}
	rows, err := targetStore.ListWireGuardClients(ctx, "edge-wg", profileID)
	if err != nil {
		t.Fatalf("ListWireGuardClients(edge-wg) error = %v", err)
	}
	if len(rows) != 1 || rows[0] != existingClients[0] {
		t.Fatalf("wireguard clients after skipped conflict import = %+v, want existing %+v", rows, existingClients)
	}
	profiles, err := targetStore.ListWireGuardProfiles(ctx, "edge-wg")
	if err != nil {
		t.Fatalf("ListWireGuardProfiles(edge-wg) error = %v", err)
	}
	if len(profiles) != 1 || profiles[0].Revision != 12 {
		t.Fatalf("wireguard profiles after skipped conflict import = %+v", profiles)
	}
	if profiles[0].PeersJSON != marshalJSON(existingPeers, "[]") {
		t.Fatalf("profile peers after skipped conflict import = %s, want %s", profiles[0].PeersJSON, marshalJSON(existingPeers, "[]"))
	}
}

func TestBackupServiceImportReplacingWireGuardClientsRemovesStaleGeneratedPeersAndKeepsManualPeers(t *testing.T) {
	ctx := t.Context()
	targetStore, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "wg-client-replace-target"), "target-local")
	if err != nil {
		t.Fatalf("NewSQLiteStore(target) error = %v", err)
	}
	defer targetStore.Close()

	if err := targetStore.SaveAgent(ctx, storage.AgentRow{
		ID:         "edge-wg",
		Name:       "edge-wg",
		AgentToken: "token-edge-wg",
	}); err != nil {
		t.Fatalf("SaveAgent(target) error = %v", err)
	}
	profileID := 7
	staleGenerated := WireGuardPeer{
		Name:         "old-generated",
		PublicKey:    testWireGuardPrivateKey,
		PresharedKey: testWireGuardPresharedKey,
		AllowedIPs:   []string{"10.92.0.4/32"},
	}
	if err := targetStore.SaveWireGuardProfiles(ctx, "edge-wg", []storage.WireGuardProfileRow{{
		ID:            profileID,
		AgentID:       "edge-wg",
		Name:          "wg-replace",
		Mode:          "generic_wireguard",
		PrivateKey:    testWireGuardPrivateKey,
		AddressesJSON: `["10.92.0.1/24"]`,
		PeersJSON: marshalJSON([]WireGuardPeer{
			staleGenerated,
			{
				Name:       "manual-peer",
				PublicKey:  "FFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFF=",
				AllowedIPs: []string{"10.92.0.99/32"},
			},
		}, "[]"),
		DNSJSON:  `[]`,
		Enabled:  true,
		Revision: 6,
	}}); err != nil {
		t.Fatalf("SaveWireGuardProfiles(target stale peer) error = %v", err)
	}
	if err := targetStore.SaveWireGuardClients(ctx, "edge-wg", profileID, []storage.WireGuardClientRow{{
		ID:             50,
		AgentID:        "edge-wg",
		ProfileID:      profileID,
		Name:           "old-generated",
		PrivateKey:     testWireGuardPresharedKey,
		PublicKey:      testWireGuardPrivateKey,
		PresharedKey:   testWireGuardPresharedKey,
		Address:        "10.92.0.4/32",
		AllowedIPsJSON: `["0.0.0.0/0"]`,
		DNSJSON:        `[]`,
		Enabled:        true,
		CreatedAt:      "2026-05-16T07:00:00Z",
		UpdatedAt:      "2026-05-16T07:01:00Z",
	}}); err != nil {
		t.Fatalf("SaveWireGuardClients(target stale row) error = %v", err)
	}

	backupSvc := NewBackupService(config.Config{EnableLocalAgent: true, LocalAgentID: "target-local"}, targetStore)
	allocator, err := newConfigIdentityAllocatorFromStore(ctx, backupSvc.cfg, targetStore)
	if err != nil {
		t.Fatalf("newConfigIdentityAllocatorFromStore() error = %v", err)
	}
	result := newBackupImportResult(BackupManifest{PackageVersion: BackupPackageVersion})
	err = backupSvc.importWireGuardClients(ctx, []BackupWireGuardClient{
		{
			ID:           1,
			AgentID:      "edge-wg",
			ProfileID:    profileID,
			Name:         "new-phone",
			PrivateKey:   testWireGuardPresharedKey,
			PublicKey:    testWireGuardPublicKeyB,
			PresharedKey: testWireGuardPresharedKeyB,
			Address:      "10.92.0.2/32",
			AllowedIPs:   []string{"0.0.0.0/0"},
			DNS:          []string{"9.9.9.9"},
			Enabled:      true,
			CreatedAt:    "2026-05-16T09:00:00Z",
			UpdatedAt:    "2026-05-16T09:01:00Z",
		},
		{
			ID:           2,
			AgentID:      "edge-wg",
			ProfileID:    profileID,
			Name:         "disabled-tablet",
			PrivateKey:   testWireGuardPrivateKey,
			PublicKey:    testWireGuardPublicKey,
			PresharedKey: testWireGuardPresharedKey,
			Address:      "10.92.0.3/32",
			AllowedIPs:   []string{"10.92.0.0/24"},
			DNS:          []string{"1.1.1.1"},
			Enabled:      false,
			CreatedAt:    "2026-05-16T10:00:00Z",
			UpdatedAt:    "2026-05-16T10:01:00Z",
		},
	}, map[string]string{"edge-wg": "edge-wg"}, map[string]int{wireGuardProfileKey("edge-wg", profileID): profileID}, map[string]struct{}{wireGuardProfileKey("edge-wg", profileID): struct{}{}}, map[string]struct{}{}, &result, modifiedAgentRevisions{}, allocator)
	if err != nil {
		t.Fatalf("importWireGuardClients() error = %v", err)
	}
	if result.Summary.Imported.WireGuardClients != 2 || result.Summary.SkippedInvalid.WireGuardClients != 0 {
		t.Fatalf("wireguard client import summary = %+v", result.Summary)
	}
	rows, err := targetStore.ListWireGuardClients(ctx, "edge-wg", profileID)
	if err != nil {
		t.Fatalf("ListWireGuardClients(edge-wg) error = %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("wireguard clients after replacement = %+v, want imported rows", rows)
	}
	refreshedProfiles, err := targetStore.ListWireGuardProfiles(ctx, "edge-wg")
	if err != nil {
		t.Fatalf("ListWireGuardProfiles(edge-wg) after replacement error = %v", err)
	}
	profile := wireGuardProfileFromRow(refreshedProfiles[0])
	peerByName := map[string]WireGuardPeer{}
	for _, peer := range profile.Peers {
		peerByName[peer.Name] = peer
		if peer.PublicKey == staleGenerated.PublicKey {
			t.Fatalf("profile peers = %+v, want stale generated peer removed", profile.Peers)
		}
	}
	if _, ok := peerByName["manual-peer"]; !ok {
		t.Fatalf("profile peers = %+v, want manual peer preserved", profile.Peers)
	}
	if peer, ok := peerByName["new-phone"]; !ok || peer.PublicKey != testWireGuardPublicKeyB || len(peer.AllowedIPs) != 1 || peer.AllowedIPs[0] != "10.92.0.2/32" {
		t.Fatalf("profile peers = %+v, want enabled imported client peer", profile.Peers)
	}
	if _, ok := peerByName["disabled-tablet"]; ok {
		t.Fatalf("profile peers = %+v, want disabled imported client omitted", profile.Peers)
	}
}

func TestBackupServicePreviewReportsWireGuardClientCountsAndRejectsInvalidKeyMaterial(t *testing.T) {
	ctx := t.Context()
	targetStore, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "wg-client-preview-target"), "target-local")
	if err != nil {
		t.Fatalf("NewSQLiteStore(target) error = %v", err)
	}
	defer targetStore.Close()

	if err := targetStore.SaveAgent(ctx, storage.AgentRow{
		ID:         "edge-wg",
		Name:       "edge-wg",
		AgentToken: "token-edge-wg",
	}); err != nil {
		t.Fatalf("SaveAgent(target) error = %v", err)
	}
	conflictProfileID := 20
	if err := targetStore.SaveWireGuardProfiles(ctx, "edge-wg", []storage.WireGuardProfileRow{{
		ID:            conflictProfileID,
		AgentID:       "edge-wg",
		Name:          "wg-conflict",
		Mode:          "generic_wireguard",
		PrivateKey:    testWireGuardPrivateKey,
		AddressesJSON: `["10.94.0.1/24"]`,
		PeersJSON:     `[]`,
		DNSJSON:       `[]`,
		Enabled:       true,
		Revision:      4,
	}}); err != nil {
		t.Fatalf("SaveWireGuardProfiles(target) error = %v", err)
	}

	bundle := BackupBundle{
		Manifest: BackupManifest{
			PackageVersion:     BackupPackageVersion,
			SourceArchitecture: BackupSourceArchitectureGo,
			ExportedAt:         time.Date(2026, 5, 16, 0, 0, 0, 0, time.UTC),
		},
		WireGuardProfiles: []BackupWireGuardProfile{
			{
				ID:         10,
				AgentID:    "edge-wg",
				Name:       "wg-preview",
				Mode:       "generic_wireguard",
				PrivateKey: testWireGuardPrivateKey,
				Addresses:  []string{"10.93.0.1/24"},
				Peers:      []WireGuardPeer{},
				Enabled:    true,
			},
			{
				ID:         conflictProfileID,
				AgentID:    "edge-wg",
				Name:       "wg-conflict",
				Mode:       "generic_wireguard",
				PrivateKey: testWireGuardPrivateKey,
				Addresses:  []string{"10.94.0.1/24"},
				Peers:      []WireGuardPeer{},
				Enabled:    true,
			},
		},
	}
	archive, err := encodeBackupBundleWithWireGuardClients(t, bundle, []testBackupWireGuardClient{
		{
			ID:           1,
			AgentID:      "edge-wg",
			ProfileID:    10,
			Name:         "valid-phone",
			PrivateKey:   testWireGuardPrivateKey,
			PublicKey:    testWireGuardPublicKey,
			PresharedKey: testWireGuardPresharedKey,
			Address:      "10.93.0.2/32",
			AllowedIPs:   []string{"0.0.0.0/0"},
			DNS:          []string{"1.1.1.1"},
			Enabled:      true,
		},
		{
			ID:           2,
			AgentID:      "edge-wg",
			ProfileID:    10,
			Name:         "invalid-public-key",
			PrivateKey:   testWireGuardPrivateKey,
			PublicKey:    "not-a-wireguard-key",
			PresharedKey: testWireGuardPresharedKey,
			Address:      "10.93.0.3/32",
			AllowedIPs:   []string{"10.93.0.0/24"},
			DNS:          []string{"9.9.9.9"},
			Enabled:      true,
		},
		{
			ID:           3,
			AgentID:      "edge-wg",
			ProfileID:    10,
			Name:         "invalid-psk",
			PrivateKey:   testWireGuardPresharedKey,
			PublicKey:    testWireGuardPublicKeyB,
			PresharedKey: "not-a-wireguard-key",
			Address:      "10.93.0.4/32",
			AllowedIPs:   []string{"10.93.0.0/24"},
			DNS:          []string{"8.8.8.8"},
			Enabled:      true,
		},
		{
			ID:           4,
			AgentID:      "edge-wg",
			ProfileID:    conflictProfileID,
			Name:         "conflict-profile-client",
			PrivateKey:   testWireGuardPrivateKey,
			PublicKey:    testWireGuardPublicKey,
			PresharedKey: testWireGuardPresharedKey,
			Address:      "10.94.0.2/32",
			AllowedIPs:   []string{"10.94.0.0/24"},
			DNS:          []string{"1.1.1.1"},
			Enabled:      true,
		},
	})
	if err != nil {
		t.Fatalf("encodeBackupBundleWithWireGuardClients() error = %v", err)
	}

	result, err := NewBackupService(config.Config{EnableLocalAgent: true, LocalAgentID: "target-local"}, targetStore).Preview(ctx, archive)
	if err != nil {
		t.Fatalf("Preview() error = %v", err)
	}
	if result.Summary.Imported.WireGuardClients != 1 || result.Summary.SkippedInvalid.WireGuardClients != 2 || result.Summary.SkippedConflict.WireGuardClients != 1 {
		t.Fatalf("wireguard client preview summary = imported %+v skipped invalid %+v skipped conflict %+v", result.Summary.Imported, result.Summary.SkippedInvalid, result.Summary.SkippedConflict)
	}
	rows, err := targetStore.ListWireGuardClients(ctx, "edge-wg", 10)
	if err != nil {
		t.Fatalf("ListWireGuardClients(edge-wg) error = %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("preview mutated wireguard clients = %+v, want none", rows)
	}
}

func TestBackupServiceImportSkipsWireGuardClientsWithInvalidKeyMaterial(t *testing.T) {
	ctx := t.Context()
	targetStore, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "wg-client-invalid-import-target"), "target-local")
	if err != nil {
		t.Fatalf("NewSQLiteStore(target) error = %v", err)
	}
	defer targetStore.Close()

	bundle := BackupBundle{
		Manifest: BackupManifest{
			PackageVersion:     BackupPackageVersion,
			SourceArchitecture: BackupSourceArchitectureGo,
			ExportedAt:         time.Date(2026, 5, 16, 0, 0, 0, 0, time.UTC),
		},
		Agents: []BackupAgent{{
			ID:         "edge-wg",
			Name:       "edge-wg",
			AgentToken: "token-edge-wg",
			Capabilities: []string{
				"l4",
				"wireguard",
			},
		}},
		WireGuardProfiles: []BackupWireGuardProfile{{
			ID:         10,
			AgentID:    "edge-wg",
			Name:       "wg-invalid-import",
			Mode:       "generic_wireguard",
			PrivateKey: testWireGuardPrivateKey,
			Addresses:  []string{"10.95.0.1/24"},
			Peers:      []WireGuardPeer{},
			Enabled:    true,
		}},
	}
	archive, err := encodeBackupBundleWithWireGuardClients(t, bundle, []testBackupWireGuardClient{
		{
			ID:           1,
			AgentID:      "edge-wg",
			ProfileID:    10,
			Name:         "valid-phone",
			PrivateKey:   testWireGuardPrivateKey,
			PublicKey:    testWireGuardPublicKey,
			PresharedKey: testWireGuardPresharedKey,
			Address:      "10.95.0.2/32",
			AllowedIPs:   []string{"0.0.0.0/0"},
			DNS:          []string{"1.1.1.1"},
			Enabled:      true,
		},
		{
			ID:           2,
			AgentID:      "edge-wg",
			ProfileID:    10,
			Name:         "invalid-private-key",
			PrivateKey:   "not-a-wireguard-key",
			PublicKey:    testWireGuardPublicKeyB,
			PresharedKey: testWireGuardPresharedKeyB,
			Address:      "10.95.0.3/32",
			AllowedIPs:   []string{"10.95.0.0/24"},
			DNS:          []string{"9.9.9.9"},
			Enabled:      true,
		},
		{
			ID:           3,
			AgentID:      "edge-wg",
			ProfileID:    10,
			Name:         "invalid-psk",
			PrivateKey:   testWireGuardPresharedKey,
			PublicKey:    testWireGuardPublicKeyB,
			PresharedKey: "not-a-wireguard-key",
			Address:      "10.95.0.4/32",
			AllowedIPs:   []string{"10.95.0.0/24"},
			DNS:          []string{"8.8.8.8"},
			Enabled:      true,
		},
	})
	if err != nil {
		t.Fatalf("encodeBackupBundleWithWireGuardClients() error = %v", err)
	}

	result, err := NewBackupService(config.Config{EnableLocalAgent: true, LocalAgentID: "target-local"}, targetStore).Import(ctx, archive)
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	if result.Summary.Imported.WireGuardClients != 1 || result.Summary.SkippedInvalid.WireGuardClients != 2 {
		t.Fatalf("wireguard client import summary = imported %+v skipped invalid %+v", result.Summary.Imported, result.Summary.SkippedInvalid)
	}
	profiles, err := targetStore.ListWireGuardProfiles(ctx, "edge-wg")
	if err != nil {
		t.Fatalf("ListWireGuardProfiles(edge-wg) error = %v", err)
	}
	if len(profiles) != 1 {
		t.Fatalf("profiles = %+v, want one imported profile", profiles)
	}
	rows, err := targetStore.ListWireGuardClients(ctx, "edge-wg", profiles[0].ID)
	if err != nil {
		t.Fatalf("ListWireGuardClients(edge-wg) error = %v", err)
	}
	if len(rows) != 1 || rows[0].Name != "valid-phone" {
		t.Fatalf("wireguard clients after invalid import = %+v, want only valid-phone", rows)
	}
	profile := wireGuardProfileFromRow(profiles[0])
	if len(profile.Peers) != 1 || profile.Peers[0].Name != "valid-phone" || profile.Peers[0].PublicKey != testWireGuardPublicKey {
		t.Fatalf("profile peers after invalid import = %+v, want only valid-phone peer", profile.Peers)
	}
}

func TestBackupServiceImportRemovesInvalidWireGuardClientPeerFromImportedProfile(t *testing.T) {
	ctx := t.Context()
	targetStore, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "wg-client-invalid-peer-import-target"), "target-local")
	if err != nil {
		t.Fatalf("NewSQLiteStore(target) error = %v", err)
	}
	defer targetStore.Close()

	bundle := BackupBundle{
		Manifest: BackupManifest{
			PackageVersion:     BackupPackageVersion,
			SourceArchitecture: BackupSourceArchitectureGo,
			ExportedAt:         time.Date(2026, 5, 16, 0, 0, 0, 0, time.UTC),
		},
		Agents: []BackupAgent{{
			ID:         "edge-wg",
			Name:       "edge-wg",
			AgentToken: "token-edge-wg",
			Capabilities: []string{
				"l4",
				"wireguard",
			},
		}},
		WireGuardProfiles: []BackupWireGuardProfile{{
			ID:         10,
			AgentID:    "edge-wg",
			Name:       "wg-invalid-peer-import",
			Mode:       "generic_wireguard",
			PrivateKey: testWireGuardPrivateKey,
			Addresses:  []string{"10.96.0.1/24"},
			Peers: []WireGuardPeer{
				{
					Name:         "valid-phone",
					PublicKey:    testWireGuardPublicKey,
					PresharedKey: testWireGuardPresharedKey,
					AllowedIPs:   []string{"10.96.0.2/32"},
				},
				{
					Name:         "invalid-phone",
					PublicKey:    testWireGuardPublicKeyB,
					PresharedKey: testWireGuardPresharedKeyB,
					AllowedIPs:   []string{"10.96.0.3/32"},
				},
				{
					Name:       "manual-peer",
					PublicKey:  "FFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFF=",
					AllowedIPs: []string{"10.96.0.99/32"},
				},
			},
			Enabled: true,
		}},
	}
	archive, err := encodeBackupBundleWithWireGuardClients(t, bundle, []testBackupWireGuardClient{
		{
			ID:           1,
			AgentID:      "edge-wg",
			ProfileID:    10,
			Name:         "valid-phone",
			PrivateKey:   testWireGuardPrivateKey,
			PublicKey:    testWireGuardPublicKey,
			PresharedKey: testWireGuardPresharedKey,
			Address:      "10.96.0.2/32",
			AllowedIPs:   []string{"0.0.0.0/0"},
			DNS:          []string{"1.1.1.1"},
			Enabled:      true,
		},
		{
			ID:           2,
			AgentID:      "edge-wg",
			ProfileID:    10,
			Name:         "invalid-phone",
			PrivateKey:   "not-a-wireguard-key",
			PublicKey:    testWireGuardPublicKeyB,
			PresharedKey: testWireGuardPresharedKeyB,
			Address:      "10.96.0.3/32",
			AllowedIPs:   []string{"10.96.0.0/24"},
			DNS:          []string{"9.9.9.9"},
			Enabled:      true,
		},
		{
			ID:           3,
			AgentID:      "edge-wg",
			ProfileID:    10,
			Name:         "disabled-tablet",
			PrivateKey:   testWireGuardPresharedKey,
			PublicKey:    testWireGuardPrivateKey,
			PresharedKey: testWireGuardPresharedKey,
			Address:      "10.96.0.4/32",
			AllowedIPs:   []string{"10.96.0.0/24"},
			DNS:          []string{"8.8.8.8"},
			Enabled:      false,
		},
	})
	if err != nil {
		t.Fatalf("encodeBackupBundleWithWireGuardClients() error = %v", err)
	}

	result, err := NewBackupService(config.Config{EnableLocalAgent: true, LocalAgentID: "target-local"}, targetStore).Import(ctx, archive)
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	if result.Summary.Imported.WireGuardClients != 2 || result.Summary.SkippedInvalid.WireGuardClients != 1 {
		t.Fatalf("wireguard client import summary = imported %+v skipped invalid %+v", result.Summary.Imported, result.Summary.SkippedInvalid)
	}
	profiles, err := targetStore.ListWireGuardProfiles(ctx, "edge-wg")
	if err != nil {
		t.Fatalf("ListWireGuardProfiles(edge-wg) error = %v", err)
	}
	if len(profiles) != 1 {
		t.Fatalf("profiles = %+v, want one imported profile", profiles)
	}
	profile := wireGuardProfileFromRow(profiles[0])
	peerByName := map[string]WireGuardPeer{}
	for _, peer := range profile.Peers {
		peerByName[peer.Name] = peer
		if peer.PublicKey == testWireGuardPublicKeyB {
			t.Fatalf("profile peers = %+v, want invalid generated client peer removed", profile.Peers)
		}
	}
	if peer, ok := peerByName["valid-phone"]; !ok || peer.PublicKey != testWireGuardPublicKey || len(peer.AllowedIPs) != 1 || peer.AllowedIPs[0] != "10.96.0.2/32" {
		t.Fatalf("profile peers = %+v, want valid enabled client peer", profile.Peers)
	}
	if _, ok := peerByName["disabled-tablet"]; ok {
		t.Fatalf("profile peers = %+v, want disabled imported client omitted", profile.Peers)
	}
	if _, ok := peerByName["manual-peer"]; !ok {
		t.Fatalf("profile peers = %+v, want unrelated manual peer preserved", profile.Peers)
	}
}

func TestBackupServiceImportReportsWireGuardProfileResults(t *testing.T) {
	ctx := t.Context()
	targetStore, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "wg-report-target"), "target-local")
	if err != nil {
		t.Fatalf("NewSQLiteStore(target) error = %v", err)
	}
	defer targetStore.Close()

	bundle := BackupBundle{
		Manifest: BackupManifest{
			PackageVersion:     BackupPackageVersion,
			SourceArchitecture: BackupSourceArchitectureGo,
			ExportedAt:         time.Date(2026, 5, 16, 0, 0, 0, 0, time.UTC),
		},
		Agents: []BackupAgent{{
			ID:         "edge-wg",
			Name:       "edge-wg",
			AgentToken: "token-edge-wg",
			Capabilities: []string{
				"l4",
				"wireguard",
			},
		}},
		WireGuardProfiles: []BackupWireGuardProfile{
			{
				ID:         1,
				AgentID:    "edge-wg",
				Name:       "wg-valid",
				Mode:       "generic_wireguard",
				PrivateKey: testWireGuardPrivateKey,
				ListenPort: 51820,
				Addresses:  []string{"10.80.0.2/32"},
				Peers: []WireGuardPeer{{
					Name:       "peer-a",
					PublicKey:  testWireGuardPublicKey,
					AllowedIPs: []string{"10.80.0.1/32"},
				}},
				Enabled: true,
			},
			{
				ID:         2,
				AgentID:    "edge-wg",
				Name:       "wg-invalid",
				Mode:       "generic_wireguard",
				PrivateKey: testWireGuardPrivateKey,
				ListenPort: 51821,
				Peers: []WireGuardPeer{{
					Name:       "peer-b",
					PublicKey:  testWireGuardPublicKeyB,
					AllowedIPs: []string{"10.81.0.1/32"},
				}},
				Enabled: true,
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
	if preview.Summary.Imported.WireGuardProfiles != 1 || preview.Summary.SkippedInvalid.WireGuardProfiles != 1 {
		t.Fatalf("preview WireGuard summary = imported %+v skipped invalid %+v", preview.Summary.Imported, preview.Summary.SkippedInvalid)
	}

	result, err := svc.Import(ctx, archive)
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	if result.Summary.Imported.WireGuardProfiles != 1 || result.Summary.SkippedInvalid.WireGuardProfiles != 1 {
		t.Fatalf("import WireGuard summary = imported %+v skipped invalid %+v", result.Summary.Imported, result.Summary.SkippedInvalid)
	}

	profiles, err := targetStore.ListWireGuardProfiles(ctx, "edge-wg")
	if err != nil {
		t.Fatalf("ListWireGuardProfiles(edge-wg) error = %v", err)
	}
	if len(profiles) != 1 || profiles[0].Name != "wg-valid" {
		t.Fatalf("imported WireGuard profiles = %+v, want only wg-valid", profiles)
	}
}

func TestBackupServiceImportSkipsWireGuardProfileListenPortConflicts(t *testing.T) {
	t.Run("existing profile", func(t *testing.T) {
		ctx := t.Context()
		targetStore, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "wg-existing-port-conflict-target"), "target-local")
		if err != nil {
			t.Fatalf("NewSQLiteStore(target) error = %v", err)
		}
		defer targetStore.Close()

		if err := targetStore.SaveAgent(ctx, storage.AgentRow{
			ID:         "edge-wg",
			Name:       "edge-wg",
			AgentToken: "token-edge-wg",
		}); err != nil {
			t.Fatalf("SaveAgent(edge-wg) error = %v", err)
		}
		if err := targetStore.SaveWireGuardProfiles(ctx, "edge-wg", []storage.WireGuardProfileRow{{
			ID:            10,
			AgentID:       "edge-wg",
			Name:          "wg-existing",
			Mode:          "generic_wireguard",
			PrivateKey:    testWireGuardPrivateKey,
			ListenPort:    51820,
			AddressesJSON: `["10.90.0.2/32"]`,
			PeersJSON:     `[]`,
			DNSJSON:       `[]`,
			Enabled:       true,
			Revision:      4,
		}}); err != nil {
			t.Fatalf("SaveWireGuardProfiles(edge-wg) error = %v", err)
		}

		bundle := BackupBundle{
			Manifest: BackupManifest{
				PackageVersion:     BackupPackageVersion,
				SourceArchitecture: BackupSourceArchitectureGo,
				ExportedAt:         time.Date(2026, 5, 16, 0, 0, 0, 0, time.UTC),
			},
			Agents: []BackupAgent{{
				ID:         "edge-wg",
				Name:       "edge-wg",
				AgentToken: "token-edge-wg",
			}},
			WireGuardProfiles: []BackupWireGuardProfile{{
				ID:         1,
				AgentID:    "edge-wg",
				Name:       "wg-imported",
				Mode:       "generic_wireguard",
				PrivateKey: testWireGuardPrivateKey,
				ListenPort: 51820,
				Addresses:  []string{"10.90.0.3/32"},
				Peers: []WireGuardPeer{{
					Name:       "peer-a",
					PublicKey:  testWireGuardPublicKey,
					AllowedIPs: []string{"10.90.0.1/32"},
				}},
				Enabled: true,
			}},
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
		if preview.Summary.Imported.WireGuardProfiles != 0 || preview.Summary.SkippedConflict.WireGuardProfiles != 1 {
			t.Fatalf("preview WireGuard summary = imported %+v skipped conflict %+v", preview.Summary.Imported, preview.Summary.SkippedConflict)
		}

		result, err := svc.Import(ctx, archive)
		if err != nil {
			t.Fatalf("Import() error = %v", err)
		}
		if result.Summary.Imported.WireGuardProfiles != 0 || result.Summary.SkippedConflict.WireGuardProfiles != 1 {
			t.Fatalf("import WireGuard summary = imported %+v skipped conflict %+v", result.Summary.Imported, result.Summary.SkippedConflict)
		}
		profiles, err := targetStore.ListWireGuardProfiles(ctx, "edge-wg")
		if err != nil {
			t.Fatalf("ListWireGuardProfiles(edge-wg) error = %v", err)
		}
		if len(profiles) != 1 || profiles[0].Name != "wg-existing" {
			t.Fatalf("profiles after import = %+v, want only existing profile", profiles)
		}
	})

	t.Run("incoming profiles", func(t *testing.T) {
		ctx := t.Context()
		targetStore, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "wg-incoming-port-conflict-target"), "target-local")
		if err != nil {
			t.Fatalf("NewSQLiteStore(target) error = %v", err)
		}
		defer targetStore.Close()

		bundle := BackupBundle{
			Manifest: BackupManifest{
				PackageVersion:     BackupPackageVersion,
				SourceArchitecture: BackupSourceArchitectureGo,
				ExportedAt:         time.Date(2026, 5, 16, 0, 0, 0, 0, time.UTC),
			},
			Agents: []BackupAgent{{
				ID:         "edge-wg",
				Name:       "edge-wg",
				AgentToken: "token-edge-wg",
			}},
			WireGuardProfiles: []BackupWireGuardProfile{
				{
					ID:         1,
					AgentID:    "edge-wg",
					Name:       "wg-a",
					Mode:       "generic_wireguard",
					PrivateKey: testWireGuardPrivateKey,
					ListenPort: 51820,
					Addresses:  []string{"10.91.0.2/32"},
					Peers: []WireGuardPeer{{
						Name:       "peer-a",
						PublicKey:  testWireGuardPublicKey,
						AllowedIPs: []string{"10.91.0.1/32"},
					}},
					Enabled: true,
				},
				{
					ID:         2,
					AgentID:    "edge-wg",
					Name:       "wg-b",
					Mode:       "generic_wireguard",
					PrivateKey: testWireGuardPresharedKey,
					ListenPort: 51820,
					Addresses:  []string{"10.92.0.2/32"},
					Peers: []WireGuardPeer{{
						Name:       "peer-b",
						PublicKey:  testWireGuardPublicKeyB,
						AllowedIPs: []string{"10.92.0.1/32"},
					}},
					Enabled: true,
				},
			},
		}
		archive, err := encodeBackupBundle(bundle)
		if err != nil {
			t.Fatalf("encodeBackupBundle() error = %v", err)
		}

		svc := NewBackupService(config.Config{EnableLocalAgent: true, LocalAgentID: "target-local"}, targetStore)
		result, err := svc.Import(ctx, archive)
		if err != nil {
			t.Fatalf("Import() error = %v", err)
		}
		if result.Summary.Imported.WireGuardProfiles != 1 || result.Summary.SkippedConflict.WireGuardProfiles != 1 {
			t.Fatalf("import WireGuard summary = imported %+v skipped conflict %+v", result.Summary.Imported, result.Summary.SkippedConflict)
		}
		profiles, err := targetStore.ListWireGuardProfiles(ctx, "edge-wg")
		if err != nil {
			t.Fatalf("ListWireGuardProfiles(edge-wg) error = %v", err)
		}
		if len(profiles) != 1 || profiles[0].Name != "wg-a" {
			t.Fatalf("profiles after import = %+v, want only wg-a", profiles)
		}
	})
}

func TestBackupServiceImportSkipsWireGuardRelayAndL4EntriesWithUnmappedProfiles(t *testing.T) {
	ctx := t.Context()
	targetStore, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "wg-missing-target"), "target-local")
	if err != nil {
		t.Fatalf("NewSQLiteStore(target) error = %v", err)
	}
	defer targetStore.Close()

	missingProfileID := 99
	bundle := BackupBundle{
		Manifest: BackupManifest{
			PackageVersion:     BackupPackageVersion,
			SourceArchitecture: BackupSourceArchitectureGo,
			ExportedAt:         time.Date(2026, 5, 13, 0, 0, 0, 0, time.UTC),
		},
		Agents: []BackupAgent{{
			ID:         "edge-wg",
			Name:       "edge-wg",
			AgentToken: "token-edge-wg",
			Capabilities: []string{
				"l4",
				"wireguard",
			},
		}},
		RelayListeners: []BackupRelayListener{{
			ID:                 80,
			AgentID:            "edge-wg",
			Name:               "relay-missing-wg",
			ListenHost:         "0.0.0.0",
			BindHosts:          []string{"0.0.0.0"},
			ListenPort:         7443,
			PublicHost:         "relay.example.com",
			PublicPort:         7443,
			Enabled:            true,
			TransportMode:      "wireguard",
			WireGuardProfileID: &missingProfileID,
			ObfsMode:           "off",
		}},
		L4Rules: []BackupL4Rule{{
			ID:                  81,
			AgentID:             "edge-wg",
			Name:                "l4-missing-wg",
			Protocol:            "udp",
			ListenHost:          "0.0.0.0",
			ListenPort:          51820,
			Backends:            []L4Backend{{Host: "10.70.0.1", Port: 51820}},
			LoadBalancing:       L4LoadBalancing{Strategy: "round_robin"},
			ListenMode:          "wireguard",
			WireGuardProfileID:  &missingProfileID,
			WireGuardListenHost: "10.70.0.2",
			Enabled:             true,
		}},
	}
	archive, err := encodeBackupBundle(bundle)
	if err != nil {
		t.Fatalf("encodeBackupBundle() error = %v", err)
	}

	result, err := NewBackupService(config.Config{EnableLocalAgent: true, LocalAgentID: "target-local"}, targetStore).Import(ctx, archive)
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	if result.Summary.SkippedInvalid.RelayListeners != 1 || result.Summary.SkippedInvalid.L4Rules != 1 {
		t.Fatalf("skipped invalid summary = %+v", result.Summary.SkippedInvalid)
	}

	listeners, err := targetStore.ListRelayListeners(ctx, "edge-wg")
	if err != nil {
		t.Fatalf("ListRelayListeners(edge-wg) error = %v", err)
	}
	if len(listeners) != 0 {
		t.Fatalf("imported relay listeners = %+v, want none", listeners)
	}
	l4Rules, err := targetStore.ListL4Rules(ctx, "edge-wg")
	if err != nil {
		t.Fatalf("ListL4Rules(edge-wg) error = %v", err)
	}
	if len(l4Rules) != 0 {
		t.Fatalf("imported l4 rules = %+v, want none", l4Rules)
	}
}

func TestBackupServiceImportSkipsWireGuardL4TunnelListenConflicts(t *testing.T) {
	ctx := t.Context()
	targetStore, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "wg-l4-conflict-target"), "target-local")
	if err != nil {
		t.Fatalf("NewSQLiteStore(target) error = %v", err)
	}
	defer targetStore.Close()

	profileID := 1
	bundle := BackupBundle{
		Manifest: BackupManifest{
			PackageVersion:     BackupPackageVersion,
			SourceArchitecture: BackupSourceArchitectureGo,
			ExportedAt:         time.Date(2026, 5, 14, 0, 0, 0, 0, time.UTC),
		},
		Agents: []BackupAgent{{
			ID:         "edge-wg",
			Name:       "edge-wg",
			AgentToken: "token-edge-wg",
			Capabilities: []string{
				"l4",
				"wireguard",
			},
		}},
		WireGuardProfiles: []BackupWireGuardProfile{{
			ID:         profileID,
			AgentID:    "edge-wg",
			Name:       "wg-edge",
			Mode:       "generic_wireguard",
			PrivateKey: testWireGuardPrivateKey,
			Addresses:  []string{"10.95.0.2/32"},
			Peers: []WireGuardPeer{{
				Name:       "peer-a",
				PublicKey:  testWireGuardPublicKey,
				AllowedIPs: []string{"10.95.0.1/32"},
			}},
			Enabled: true,
		}},
		L4Rules: []BackupL4Rule{
			{
				ID:                  91,
				AgentID:             "edge-wg",
				Name:                "wg-l4-a",
				Protocol:            "udp",
				ListenHost:          "0.0.0.0",
				ListenPort:          51820,
				Backends:            []L4Backend{{Host: "10.95.0.1", Port: 51820}},
				LoadBalancing:       L4LoadBalancing{Strategy: "round_robin"},
				ListenMode:          "wireguard",
				WireGuardProfileID:  &profileID,
				WireGuardListenHost: "10.95.0.2",
				Enabled:             true,
			},
			{
				ID:                  92,
				AgentID:             "edge-wg",
				Name:                "wg-l4-b",
				Protocol:            "udp",
				ListenHost:          "127.0.0.1",
				ListenPort:          51820,
				Backends:            []L4Backend{{Host: "10.95.0.3", Port: 51820}},
				LoadBalancing:       L4LoadBalancing{Strategy: "round_robin"},
				ListenMode:          "wireguard",
				WireGuardProfileID:  &profileID,
				WireGuardListenHost: "10.95.0.2",
				Enabled:             true,
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
	if result.Summary.Imported.L4Rules != 1 || result.Summary.SkippedConflict.L4Rules != 1 {
		t.Fatalf("import summary = %+v", result.Summary)
	}
	l4Rules, err := targetStore.ListL4Rules(ctx, "edge-wg")
	if err != nil {
		t.Fatalf("ListL4Rules(edge-wg) error = %v", err)
	}
	if len(l4Rules) != 1 {
		t.Fatalf("imported l4 rules = %+v, want exactly one", l4Rules)
	}
}

func TestBackupServicePreviewAndImportUseNormalizedL4ListenHostConflictKeys(t *testing.T) {
	ctx := t.Context()
	targetStore, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "l4-normalized-listen-host-conflict-target"), "target-local")
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

func TestBackupServiceImportSkipsWireGuardTransparentL4RuntimeListenConflicts(t *testing.T) {
	ctx := t.Context()
	targetStore, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "wg-l4-transparent-conflict-target"), "target-local")
	if err != nil {
		t.Fatalf("NewSQLiteStore(target) error = %v", err)
	}
	defer targetStore.Close()

	profileID := 1
	bundle := BackupBundle{
		Manifest: BackupManifest{
			PackageVersion:     BackupPackageVersion,
			SourceArchitecture: BackupSourceArchitectureGo,
			ExportedAt:         time.Date(2026, 5, 14, 0, 0, 0, 0, time.UTC),
		},
		Agents: []BackupAgent{{
			ID:         "edge-wg",
			Name:       "edge-wg",
			AgentToken: "token-edge-wg",
			Capabilities: []string{
				"l4",
				"wireguard",
			},
		}},
		WireGuardProfiles: []BackupWireGuardProfile{{
			ID:         profileID,
			AgentID:    "edge-wg",
			Name:       "wg-edge",
			Mode:       "generic_wireguard",
			PrivateKey: testWireGuardPrivateKey,
			Addresses:  []string{"10.98.0.2/32"},
			Peers: []WireGuardPeer{{
				Name:       "peer-a",
				PublicKey:  testWireGuardPublicKey,
				AllowedIPs: []string{"10.98.0.1/32"},
			}},
			Enabled: true,
		}},
		L4Rules: []BackupL4Rule{
			{
				ID:                   91,
				AgentID:              "edge-wg",
				Name:                 "wg-l4-a",
				Protocol:             "udp",
				ListenHost:           "10.98.0.10",
				ListenPort:           51820,
				Backends:             []L4Backend{{Host: "10.98.0.10", Port: 51820}},
				LoadBalancing:        L4LoadBalancing{Strategy: "round_robin"},
				ListenMode:           "wireguard",
				WireGuardProfileID:   &profileID,
				WireGuardInboundMode: "transparent",
				Enabled:              true,
			},
			{
				ID:                   92,
				AgentID:              "edge-wg",
				Name:                 "wg-l4-b",
				Protocol:             "udp",
				ListenHost:           "10.98.0.11",
				ListenPort:           51820,
				Backends:             []L4Backend{{Host: "10.98.0.11", Port: 51820}},
				LoadBalancing:        L4LoadBalancing{Strategy: "round_robin"},
				ListenMode:           "wireguard",
				WireGuardProfileID:   &profileID,
				WireGuardInboundMode: "transparent",
				Enabled:              true,
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
	if result.Summary.Imported.L4Rules != 1 || result.Summary.SkippedConflict.L4Rules != 1 {
		t.Fatalf("import summary = %+v", result.Summary)
	}
	l4Rules, err := targetStore.ListL4Rules(ctx, "edge-wg")
	if err != nil {
		t.Fatalf("ListL4Rules(edge-wg) error = %v", err)
	}
	if len(l4Rules) != 1 {
		t.Fatalf("imported l4 rules = %+v, want exactly one", l4Rules)
	}
}

func TestBackupServiceImportSkipsWireGuardTransparentProxyEntryL4RuntimeListenConflicts(t *testing.T) {
	ctx := t.Context()
	targetStore, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "wg-l4-transparent-proxy-entry-conflict-target"), "target-local")
	if err != nil {
		t.Fatalf("NewSQLiteStore(target) error = %v", err)
	}
	defer targetStore.Close()

	profileID := 1
	bundle := BackupBundle{
		Manifest: BackupManifest{
			PackageVersion:     BackupPackageVersion,
			SourceArchitecture: BackupSourceArchitectureGo,
			ExportedAt:         time.Date(2026, 5, 14, 0, 0, 0, 0, time.UTC),
		},
		Agents: []BackupAgent{{
			ID:         "edge-wg",
			Name:       "edge-wg",
			AgentToken: "token-edge-wg",
			Capabilities: []string{
				"l4",
				"wireguard",
			},
		}},
		WireGuardProfiles: []BackupWireGuardProfile{{
			ID:         profileID,
			AgentID:    "edge-wg",
			Name:       "wg-edge",
			Mode:       "generic_wireguard",
			PrivateKey: testWireGuardPrivateKey,
			Addresses:  []string{"10.100.0.2/32"},
			Peers: []WireGuardPeer{{
				Name:       "peer-a",
				PublicKey:  testWireGuardPublicKey,
				AllowedIPs: []string{"10.100.0.1/32"},
			}},
			Enabled: true,
		}},
		L4Rules: []BackupL4Rule{
			{
				ID:                   91,
				AgentID:              "edge-wg",
				Name:                 "wg-l4-backend",
				Protocol:             "tcp",
				ListenHost:           "10.100.0.10",
				ListenPort:           8443,
				Backends:             []L4Backend{{Host: "10.100.0.10", Port: 8443}},
				LoadBalancing:        L4LoadBalancing{Strategy: "round_robin"},
				ListenMode:           "wireguard",
				WireGuardProfileID:   &profileID,
				WireGuardInboundMode: "transparent",
				Enabled:              true,
			},
			{
				ID:                   92,
				AgentID:              "edge-wg",
				Name:                 "wg-l4-proxy-entry",
				Protocol:             "tcp",
				ListenHost:           "10.100.0.11",
				ListenPort:           8443,
				LoadBalancing:        L4LoadBalancing{Strategy: "round_robin"},
				ListenMode:           "wireguard",
				WireGuardProfileID:   &profileID,
				WireGuardInboundMode: "transparent",
				ProxyEgressMode:      "proxy",
				ProxyEgressURL:       "socks5://127.0.0.1:1080",
				Enabled:              true,
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
	if result.Summary.Imported.L4Rules != 1 || result.Summary.SkippedConflict.L4Rules != 1 {
		t.Fatalf("import summary = %+v", result.Summary)
	}
	l4Rules, err := targetStore.ListL4Rules(ctx, "edge-wg")
	if err != nil {
		t.Fatalf("ListL4Rules(edge-wg) error = %v", err)
	}
	if len(l4Rules) != 1 {
		t.Fatalf("imported l4 rules = %+v, want exactly one", l4Rules)
	}
}

func TestBackupServicePreviewSkipsWireGuardTransparentL4ConflictWithExistingRule(t *testing.T) {
	ctx := t.Context()
	targetStore, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "wg-l4-transparent-existing-conflict-target"), "target-local")
	if err != nil {
		t.Fatalf("NewSQLiteStore(target) error = %v", err)
	}
	defer targetStore.Close()

	profileID := 1
	if err := targetStore.SaveAgent(ctx, storage.AgentRow{
		ID:               "edge-wg",
		Name:             "edge-wg",
		AgentToken:       "token-edge-wg",
		CapabilitiesJSON: `["l4","wireguard"]`,
	}); err != nil {
		t.Fatalf("SaveAgent() error = %v", err)
	}
	if err := targetStore.SaveWireGuardProfiles(ctx, "edge-wg", []storage.WireGuardProfileRow{{
		ID:            profileID,
		AgentID:       "edge-wg",
		Name:          "wg-edge",
		Mode:          "generic_wireguard",
		PrivateKey:    testWireGuardPrivateKey,
		AddressesJSON: `["10.99.0.2/32"]`,
		PeersJSON:     `[{"name":"peer-a","public_key":"` + testWireGuardPublicKey + `","allowed_ips":["10.99.0.1/32"]}]`,
		Enabled:       true,
	}}); err != nil {
		t.Fatalf("SaveWireGuardProfiles() error = %v", err)
	}
	if err := targetStore.SaveL4Rules(ctx, "edge-wg", []storage.L4RuleRow{{
		ID:                   91,
		AgentID:              "edge-wg",
		Name:                 "wg-l4-existing",
		Protocol:             "udp",
		ListenHost:           "10.99.0.10",
		ListenPort:           51820,
		BackendsJSON:         `[{"host":"10.99.0.10","port":51820}]`,
		LoadBalancingJSON:    `{"strategy":"round_robin"}`,
		TuningJSON:           `{"proxy_protocol":{"decode":false,"send":false}}`,
		ListenMode:           "wireguard",
		WireGuardProfileID:   &profileID,
		WireGuardInboundMode: "transparent",
		Enabled:              true,
	}}); err != nil {
		t.Fatalf("SaveL4Rules() error = %v", err)
	}

	bundle := BackupBundle{
		Manifest: BackupManifest{
			PackageVersion:     BackupPackageVersion,
			SourceArchitecture: BackupSourceArchitectureGo,
			ExportedAt:         time.Date(2026, 5, 14, 0, 0, 0, 0, time.UTC),
		},
		Agents: []BackupAgent{{
			ID:         "edge-wg",
			Name:       "edge-wg",
			AgentToken: "token-edge-wg",
			Capabilities: []string{
				"l4",
				"wireguard",
			},
		}},
		L4Rules: []BackupL4Rule{{
			ID:                   92,
			AgentID:              "edge-wg",
			Name:                 "wg-l4-incoming",
			Protocol:             "udp",
			ListenHost:           "10.99.0.11",
			ListenPort:           51820,
			Backends:             []L4Backend{{Host: "10.99.0.11", Port: 51820}},
			LoadBalancing:        L4LoadBalancing{Strategy: "round_robin"},
			ListenMode:           "wireguard",
			WireGuardProfileID:   &profileID,
			WireGuardInboundMode: "transparent",
			Enabled:              true,
		}},
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
	if preview.Summary.Imported.L4Rules != 0 || preview.Summary.SkippedConflict.L4Rules != 1 {
		t.Fatalf("preview summary = %+v", preview.Summary)
	}
	result, err := svc.Import(ctx, archive)
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	if result.Summary.Imported.L4Rules != 0 || result.Summary.SkippedConflict.L4Rules != 1 {
		t.Fatalf("import summary = %+v", result.Summary)
	}
}

func TestBackupServiceImportAllowsWireGuardL4TunnelListenReuseAcrossProfiles(t *testing.T) {
	ctx := t.Context()
	targetStore, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "wg-l4-profile-reuse-target"), "target-local")
	if err != nil {
		t.Fatalf("NewSQLiteStore(target) error = %v", err)
	}
	defer targetStore.Close()

	profileAID := 1
	profileBID := 2
	bundle := BackupBundle{
		Manifest: BackupManifest{
			PackageVersion:     BackupPackageVersion,
			SourceArchitecture: BackupSourceArchitectureGo,
			ExportedAt:         time.Date(2026, 5, 14, 0, 0, 0, 0, time.UTC),
		},
		Agents: []BackupAgent{{
			ID:         "edge-wg",
			Name:       "edge-wg",
			AgentToken: "token-edge-wg",
			Capabilities: []string{
				"l4",
				"wireguard",
			},
		}},
		WireGuardProfiles: []BackupWireGuardProfile{
			{
				ID:         profileAID,
				AgentID:    "edge-wg",
				Name:       "wg-edge-a",
				Mode:       "generic_wireguard",
				PrivateKey: testWireGuardPrivateKey,
				Addresses:  []string{"10.96.0.2/32"},
				Peers: []WireGuardPeer{{
					Name:       "peer-a",
					PublicKey:  testWireGuardPublicKey,
					AllowedIPs: []string{"10.96.0.1/32"},
				}},
				Enabled: true,
			},
			{
				ID:         profileBID,
				AgentID:    "edge-wg",
				Name:       "wg-edge-b",
				Mode:       "generic_wireguard",
				PrivateKey: testWireGuardPrivateKey,
				Addresses:  []string{"10.97.0.2/32"},
				Peers: []WireGuardPeer{{
					Name:       "peer-b",
					PublicKey:  testWireGuardPublicKeyB,
					AllowedIPs: []string{"10.97.0.1/32"},
				}},
				Enabled: true,
			},
		},
		L4Rules: []BackupL4Rule{
			{
				ID:                  91,
				AgentID:             "edge-wg",
				Name:                "wg-l4-a",
				Protocol:            "udp",
				ListenHost:          "0.0.0.0",
				ListenPort:          51820,
				Backends:            []L4Backend{{Host: "10.96.0.1", Port: 51820}},
				LoadBalancing:       L4LoadBalancing{Strategy: "round_robin"},
				ListenMode:          "wireguard",
				WireGuardProfileID:  &profileAID,
				WireGuardListenHost: "10.96.0.2",
				Enabled:             true,
			},
			{
				ID:                  92,
				AgentID:             "edge-wg",
				Name:                "wg-l4-b",
				Protocol:            "udp",
				ListenHost:          "0.0.0.0",
				ListenPort:          51820,
				Backends:            []L4Backend{{Host: "10.97.0.1", Port: 51820}},
				LoadBalancing:       L4LoadBalancing{Strategy: "round_robin"},
				ListenMode:          "wireguard",
				WireGuardProfileID:  &profileBID,
				WireGuardListenHost: "10.96.0.2",
				Enabled:             true,
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
	if preview.Summary.Imported.L4Rules != 2 || preview.Summary.SkippedConflict.L4Rules != 0 {
		t.Fatalf("preview summary = %+v", preview.Summary)
	}

	result, err := svc.Import(ctx, archive)
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	if result.Summary.Imported.L4Rules != 2 || result.Summary.SkippedConflict.L4Rules != 0 {
		t.Fatalf("import summary = %+v", result.Summary)
	}
	l4Rules, err := targetStore.ListL4Rules(ctx, "edge-wg")
	if err != nil {
		t.Fatalf("ListL4Rules(edge-wg) error = %v", err)
	}
	if len(l4Rules) != 2 {
		t.Fatalf("imported l4 rules = %+v, want two", l4Rules)
	}
}

func TestRemapBackupWireGuardProfileIDQualifiesEnabledStateByAgent(t *testing.T) {
	profileID := 1
	profileIDMap := map[string]int{
		wireGuardProfileKey("edge-enabled", profileID):  profileID,
		wireGuardProfileKey("edge-disabled", profileID): profileID,
	}
	enabledProfileIDs := map[string]struct{}{
		wireGuardProfileKey("edge-enabled", profileID): {},
	}

	if mapped, ok := remapBackupWireGuardProfileID("edge-disabled", &profileID, profileIDMap, enabledProfileIDs); ok {
		t.Fatalf("remapBackupWireGuardProfileID() = %v, true; want disabled same numeric id on another agent rejected", mapped)
	}
}

func TestBackupServiceImportSkipsRulesWithMissingRelayLayerDependencies(t *testing.T) {
	targetStore, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "target"), "local")
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
			name:                      "stored cross-agent wireguard conflict rejects even when incoming conflict is tls",
			existingTransport:         "wireguard",
			incomingConflictTransport: "tls_tcp",
			wantImportedRules:         1,
			wantSkippedInvalidRules:   1,
			wantInvalidReason:         "invalid argument: wireguard relay listener 500 belongs to relay-b and cannot be used by agent edge-a",
		},
		{
			name:                      "stored tls conflict allows even when incoming conflict is cross-agent wireguard",
			existingTransport:         "tls_tcp",
			incomingConflictTransport: "wireguard",
			wantImportedRules:         2,
			wantSkippedInvalidRules:   0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			targetStore, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "target"), "local")
			if err != nil {
				t.Fatalf("NewSQLiteStore(target) error = %v", err)
			}
			defer targetStore.Close()

			ctx := t.Context()
			for _, agent := range []storage.AgentRow{
				{ID: ruleAgentID, Name: ruleAgentID, AgentToken: "token-edge"},
				{ID: relayAgentID, Name: relayAgentID, AgentToken: "token-relay", CapabilitiesJSON: `["wireguard"]`},
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
				ID:                 existingRelayID,
				AgentID:            relayAgentID,
				Name:               "relay-conflict",
				ListenHost:         "0.0.0.0",
				BindHostsJSON:      `["0.0.0.0"]`,
				ListenPort:         7443,
				PublicHost:         "relay.example.com",
				PublicPort:         7443,
				Enabled:            true,
				TransportMode:      tt.existingTransport,
				WireGuardProfileID: backupIntPtr(41),
				Revision:           3,
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
						ID:                 conflictRelayID,
						AgentID:            relayAgentID,
						Name:               "relay-conflict",
						BindHosts:          []string{"0.0.0.0"},
						ListenHost:         "0.0.0.0",
						ListenPort:         7443,
						PublicHost:         "relay.example.com",
						PublicPort:         7443,
						Enabled:            true,
						TransportMode:      tt.incomingConflictTransport,
						WireGuardProfileID: backupIntPtr(41),
						Revision:           9,
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
	targetStore, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "target"), "local")
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
	targetStore, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "target"), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore(target) error = %v", err)
	}
	defer targetStore.Close()

	ctx := t.Context()
	for _, agent := range []storage.AgentRow{
		{ID: "edge-a", Name: "edge-a", AgentToken: "token-edge"},
		{ID: "relay-live", Name: "relay-live", AgentToken: "token-relay", CapabilitiesJSON: `["wireguard"]`},
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
				Agents:            2,
				HTTPRules:         1,
				L4Rules:           1,
				RelayListeners:    2,
				WireGuardProfiles: 1,
			},
		},
		Agents: []BackupAgent{
			{ID: "relay-first", Name: "relay-live", AgentToken: "token-first", Capabilities: []string{"wireguard"}},
			{ID: "relay-later", Name: "relay-live", AgentToken: "token-later", Capabilities: []string{"wireguard"}},
		},
		WireGuardProfiles: []BackupWireGuardProfile{{
			ID:         41,
			AgentID:    "relay-first",
			Name:       "first wg",
			Mode:       "generic_wireguard",
			PrivateKey: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=",
			ListenPort: 51820,
			Addresses:  []string{"10.44.0.1/24"},
			Peers:      []WireGuardPeer{},
			DNS:        []string{},
			MTU:        1420,
			Enabled:    true,
			Tags:       []string{},
			Revision:   4,
		}},
		RelayListeners: []BackupRelayListener{
			{
				ID:                 firstRelayID,
				AgentID:            "relay-first",
				Name:               "shared-relay",
				BindHosts:          []string{"0.0.0.0"},
				ListenHost:         "0.0.0.0",
				ListenPort:         7443,
				PublicHost:         "relay.example.com",
				PublicPort:         7443,
				Enabled:            false,
				TransportMode:      "wireguard",
				WireGuardProfileID: backupIntPtr(41),
				Revision:           5,
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
	if listeners[0].ID != firstRelayID || listeners[0].Enabled || listeners[0].TransportMode != "wireguard" || listeners[0].AgentID != "relay-live" {
		t.Fatalf("imported relay listener = %+v, want first disabled wireguard listener on resolved agent", listeners[0])
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
	targetStore, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "target"), "local")
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
	targetStore, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "target"), "local")
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

func TestBackupServicePreviewAndImportSkipCrossAgentWireGuardRelayReferences(t *testing.T) {
	targetStore, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "target"), "local")
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
				Agents:            2,
				HTTPRules:         1,
				L4Rules:           1,
				RelayListeners:    1,
				WireGuardProfiles: 1,
				Certificates:      1,
			},
		},
		Agents: []BackupAgent{
			{ID: "edge-a", Name: "edge-a", AgentToken: "token-edge-a", Capabilities: []string{"l4"}},
			{ID: "relay-b", Name: "relay-b", AgentToken: "token-relay-b", Capabilities: []string{"l4", "cert_install", "wireguard"}},
		},
		WireGuardProfiles: []BackupWireGuardProfile{{
			ID:         41,
			AgentID:    "relay-b",
			Name:       "relay wg",
			Mode:       "generic_wireguard",
			PrivateKey: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=",
			ListenPort: 51820,
			Addresses:  []string{"10.44.0.1/24"},
			Peers:      []WireGuardPeer{},
			DNS:        []string{},
			MTU:        1420,
			Enabled:    true,
			Tags:       []string{},
			Revision:   4,
		}},
		Certificates: []BackupCertificate{{
			ID:              21,
			Domain:          "relay.example.com",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "local_http01",
			TargetAgentIDs:  []string{"relay-b"},
			Status:          "pending",
			Usage:           "relay_tunnel",
			CertificateType: "acme",
			Revision:        4,
		}},
		RelayListeners: []BackupRelayListener{{
			ID:                     77,
			AgentID:                "relay-b",
			Name:                   "relay wg",
			BindHosts:              []string{"0.0.0.0"},
			ListenHost:             "0.0.0.0",
			ListenPort:             7443,
			PublicHost:             "relay.example.com",
			PublicPort:             7443,
			Enabled:                true,
			CertificateID:          intPtrService(21),
			TLSMode:                "pin_only",
			TransportMode:          "wireguard",
			WireGuardProfileID:     intPtrService(41),
			AllowTransportFallback: false,
			ObfsMode:               "off",
			PinSet:                 []RelayPin{{Type: "spki_sha256", Value: "fixture-pin"}},
			Tags:                   []string{},
			Revision:               5,
		}},
		HTTPRules: []BackupHTTPRule{{
			ID:               11,
			AgentID:          "edge-a",
			FrontendURL:      "https://cross-wg.example.com",
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
			Name:         "cross wg",
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
	preview, err := svc.Preview(t.Context(), archive)
	if err != nil {
		t.Fatalf("Preview() error = %v", err)
	}
	if preview.Summary.SkippedInvalid.HTTPRules != 1 || preview.Summary.SkippedInvalid.L4Rules != 1 {
		t.Fatalf("preview invalid summary = %+v", preview.Summary.SkippedInvalid)
	}
	assertBackupSkippedInvalidReason(t, preview, "http_rule", "https://cross-wg.example.com", "invalid argument: wireguard relay listener 77 belongs to relay-b and cannot be used by agent edge-a")
	assertBackupSkippedInvalidReason(t, preview, "l4_rule", "edge-a|tcp|0.0.0.0|9000|host", "invalid argument: wireguard relay listener 77 belongs to relay-b and cannot be used by agent edge-a")

	result, err := svc.Import(t.Context(), archive)
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	if result.Summary.SkippedInvalid.HTTPRules != 1 || result.Summary.SkippedInvalid.L4Rules != 1 {
		t.Fatalf("import invalid summary = %+v", result.Summary.SkippedInvalid)
	}
	if result.Summary.Imported.HTTPRules != 0 || result.Summary.Imported.L4Rules != 0 {
		t.Fatalf("imported rule summary = %+v", result.Summary.Imported)
	}
	assertBackupSkippedInvalidReason(t, result, "http_rule", "https://cross-wg.example.com", "invalid argument: wireguard relay listener 77 belongs to relay-b and cannot be used by agent edge-a")
	assertBackupSkippedInvalidReason(t, result, "l4_rule", "edge-a|tcp|0.0.0.0|9000|host", "invalid argument: wireguard relay listener 77 belongs to relay-b and cannot be used by agent edge-a")
}

func TestBackupServicePreviewAndImportSkipWireGuardRulesWhenAgentLacksCapability(t *testing.T) {
	ctx := t.Context()
	targetStore, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "wg-capability-target"), "target-local")
	if err != nil {
		t.Fatalf("NewSQLiteStore(target) error = %v", err)
	}
	defer targetStore.Close()

	wgProfileID := 41
	bundle := BackupBundle{
		Manifest: BackupManifest{
			PackageVersion:     BackupPackageVersion,
			SourceArchitecture: BackupSourceArchitectureGo,
			ExportedAt:         time.Date(2026, 5, 17, 0, 0, 0, 0, time.UTC),
			Counts: BackupCounts{
				Agents:            1,
				HTTPRules:         1,
				L4Rules:           2,
				RelayListeners:    1,
				WireGuardProfiles: 1,
			},
		},
		Agents: []BackupAgent{
			{
				ID:         "edge-a",
				Name:       "edge-a",
				AgentToken: "token-edge-a",
				Capabilities: []string{
					"http_rules",
					"l4",
					"relay_quic",
				},
			},
		},
		WireGuardProfiles: []BackupWireGuardProfile{
			{
				ID:         wgProfileID,
				AgentID:    "edge-a",
				Name:       "edge-a-wireguard",
				Mode:       "generic_wireguard",
				PrivateKey: testWireGuardPrivateKey,
				ListenPort: 51820,
				Addresses:  []string{"10.44.0.1/24"},
				Peers:      []WireGuardPeer{},
				DNS:        []string{},
				MTU:        1420,
				Enabled:    true,
				Tags:       []string{},
				Revision:   4,
			},
		},
		RelayListeners: []BackupRelayListener{
			{
				ID:                 77,
				AgentID:            "edge-a",
				Name:               "wg-relay",
				BindHosts:          []string{"0.0.0.0"},
				ListenHost:         "0.0.0.0",
				ListenPort:         7443,
				PublicHost:         "relay.example.com",
				PublicPort:         7443,
				Enabled:            false,
				TransportMode:      "wireguard",
				WireGuardProfileID: intPtrService(wgProfileID),
				ObfsMode:           "off",
				Tags:               []string{},
				Revision:           5,
			},
		},
		HTTPRules: []BackupHTTPRule{
			{
				ID:                       11,
				AgentID:                  "edge-a",
				FrontendURL:              "https://wg-http.example.com",
				Backends:                 []HTTPRuleBackend{{URL: "http://127.0.0.1:8096"}},
				LoadBalancing:            HTTPLoadBalancing{Strategy: "adaptive"},
				Enabled:                  true,
				PassProxyHeaders:         defaultPassProxyHeaders(),
				CustomHeaders:            []HTTPCustomHeader{},
				WireGuardEntryEnabled:    true,
				WireGuardProfileID:       intPtrService(wgProfileID),
				WireGuardEntryListenHost: "10.44.0.1",
				WireGuardEntryListenPort: 7443,
			},
		},
		L4Rules: []BackupL4Rule{
			{
				ID:                   12,
				AgentID:              "edge-a",
				Name:                 "wg-listen",
				Protocol:             "tcp",
				ListenHost:           "0.0.0.0",
				ListenPort:           9000,
				Backends:             []L4Backend{{Host: "127.0.0.1", Port: 9001}},
				LoadBalancing:        L4LoadBalancing{Strategy: "round_robin"},
				ListenMode:           "wireguard",
				WireGuardProfileID:   intPtrService(wgProfileID),
				WireGuardInboundMode: "address",
				WireGuardListenHost:  "10.44.0.1",
				Enabled:              true,
			},
			{
				ID:                 13,
				AgentID:            "edge-a",
				Name:               "wg-egress",
				Protocol:           "tcp",
				ListenHost:         "0.0.0.0",
				ListenPort:         9001,
				Backends:           []L4Backend{{Host: "127.0.0.1", Port: 9002}},
				LoadBalancing:      L4LoadBalancing{Strategy: "adaptive"},
				ListenMode:         "proxy",
				ProxyEgressMode:    "wireguard",
				WireGuardProfileID: intPtrService(wgProfileID),
				Enabled:            true,
			},
		},
	}
	archive, err := encodeBackupBundle(bundle)
	if err != nil {
		t.Fatalf("encodeBackupBundle() error = %v", err)
	}

	svc := NewBackupService(config.Config{EnableLocalAgent: true, LocalAgentID: "target-local"}, targetStore)
	expectedInvalid := []struct {
		kind string
		key  string
	}{
		{kind: "relay_listener", key: relayConflictKey("edge-a", "wg-relay")},
		{kind: "http_rule", key: "https://wg-http.example.com"},
		{kind: "l4_rule", key: l4BackupConflictKey("edge-a", "tcp", "0.0.0.0", 9000, "wireguard", "", "10.44.0.1", intPtrService(wgProfileID), "")},
		{kind: "l4_rule", key: l4BackupConflictKey("edge-a", "tcp", "0.0.0.0", 9001, "proxy", "", "", intPtrService(wgProfileID), "wireguard")},
	}
	for _, tc := range []struct {
		name string
		run  func(context.Context, []byte) (BackupImportResult, error)
	}{
		{
			name: "preview",
			run: func(ctx context.Context, archive []byte) (BackupImportResult, error) {
				return svc.Preview(ctx, archive)
			},
		},
		{
			name: "import",
			run: func(ctx context.Context, archive []byte) (BackupImportResult, error) {
				return svc.Import(ctx, archive)
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := tc.run(ctx, archive)
			if err != nil {
				t.Fatalf("%s() error = %v", tc.name, err)
			}
			if result.Summary.Imported.WireGuardProfiles != 1 {
				t.Fatalf("%s summary imported wireguard profiles = %+v, want 1", tc.name, result.Summary.Imported)
			}
			if result.Summary.SkippedInvalid.RelayListeners != 1 || result.Summary.SkippedInvalid.HTTPRules != 1 || result.Summary.SkippedInvalid.L4Rules != 2 {
				t.Fatalf("%s summary skipped invalid = %+v, want relay=1 http=1 l4=2", tc.name, result.Summary.SkippedInvalid)
			}
			wantReason := "invalid argument: agent does not support WireGuard: edge-a"
			for _, item := range expectedInvalid {
				assertBackupSkippedInvalidReason(t, result, item.kind, item.key, wantReason)
			}
		})
	}

	profiles, err := targetStore.ListWireGuardProfiles(ctx, "edge-a")
	if err != nil {
		t.Fatalf("ListWireGuardProfiles(edge-a) error = %v", err)
	}
	if len(profiles) != 1 || profiles[0].Name != "edge-a-wireguard" {
		t.Fatalf("imported wireguard profiles = %+v, want only edge-a-wireguard", profiles)
	}
	if listeners, err := targetStore.ListRelayListeners(ctx, "edge-a"); err != nil {
		t.Fatalf("ListRelayListeners(edge-a) error = %v", err)
	} else if len(listeners) != 0 {
		t.Fatalf("imported relay listeners = %+v, want none", listeners)
	}
	if rules, err := targetStore.ListHTTPRules(ctx, "edge-a"); err != nil {
		t.Fatalf("ListHTTPRules(edge-a) error = %v", err)
	} else if len(rules) != 0 {
		t.Fatalf("imported http rules = %+v, want none", rules)
	}
	if rules, err := targetStore.ListL4Rules(ctx, "edge-a"); err != nil {
		t.Fatalf("ListL4Rules(edge-a) error = %v", err)
	} else if len(rules) != 0 {
		t.Fatalf("imported l4 rules = %+v, want none", rules)
	}
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

type testBackupWireGuardClient struct {
	ID           int      `json:"id"`
	AgentID      string   `json:"agent_id"`
	ProfileID    int      `json:"profile_id"`
	Name         string   `json:"name"`
	PrivateKey   string   `json:"private_key,omitempty"`
	PublicKey    string   `json:"public_key"`
	PresharedKey string   `json:"preshared_key,omitempty"`
	Address      string   `json:"address"`
	AllowedIPs   []string `json:"allowed_ips"`
	DNS          []string `json:"dns"`
	Enabled      bool     `json:"enabled"`
	CreatedAt    string   `json:"created_at,omitempty"`
	UpdatedAt    string   `json:"updated_at,omitempty"`
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

func encodeBackupBundleWithWireGuardClients(t *testing.T, bundle BackupBundle, clients []testBackupWireGuardClient) ([]byte, error) {
	t.Helper()
	return encodeBackupBundleWithOverrides(t, bundle, map[string]any{
		"wireguard_clients.json": clients,
	})
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
		{backupWireGuardProfilesFile, bundle.WireGuardProfiles},
		{backupWireGuardClientsFile, nil},
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

	targetStore, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "rollback-target"), "local")
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

func TestBackupServiceRollbackRestoresWireGuardProfilesOnImportFailure(t *testing.T) {
	sourceStore, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "rollback-wg-source"), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore(source) error = %v", err)
	}
	defer sourceStore.Close()

	ctx := t.Context()
	if err := sourceStore.SaveAgent(ctx, storage.AgentRow{
		ID:         "source-edge-wg",
		Name:       "existing-agent",
		AgentToken: "token-source-edge-wg",
	}); err != nil {
		t.Fatalf("SaveAgent(source) error = %v", err)
	}
	if err := sourceStore.SaveWireGuardProfiles(ctx, "source-edge-wg", []storage.WireGuardProfileRow{{
		ID:            7,
		AgentID:       "source-edge-wg",
		Name:          "imported-wg",
		Mode:          "generic_wireguard",
		PrivateKey:    testWireGuardPrivateKey,
		ListenPort:    51820,
		AddressesJSON: `["10.80.0.2/32"]`,
		PeersJSON: marshalJSON([]WireGuardPeer{{
			Name:         "peer-a",
			PublicKey:    testWireGuardPublicKey,
			PresharedKey: testWireGuardPresharedKey,
			Endpoint:     "relay.example.com:51820",
			AllowedIPs:   []string{"10.80.0.1/32"},
		}}, "[]"),
		DNSJSON:  `[]`,
		Enabled:  true,
		Revision: 2,
	}}); err != nil {
		t.Fatalf("SaveWireGuardProfiles(source) error = %v", err)
	}
	if err := sourceStore.SaveHTTPRules(ctx, "source-edge-wg", []storage.HTTPRuleRow{{
		ID:                11,
		AgentID:           "source-edge-wg",
		FrontendURL:       "https://edge-wg.example.com",
		BackendsJSON:      `[{"url":"http://127.0.0.1:8096"}]`,
		LoadBalancingJSON: `{"strategy":"round_robin"}`,
		Enabled:           true,
		RelayChainJSON:    `[]`,
		RelayLayersJSON:   `[]`,
		TagsJSON:          `[]`,
		CustomHeadersJSON: `[]`,
		Revision:          2,
	}}); err != nil {
		t.Fatalf("SaveHTTPRules(source) error = %v", err)
	}

	archive, _, err := NewBackupService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, sourceStore).Export(ctx)
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}

	targetStore, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "rollback-wg-target"), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore(target) error = %v", err)
	}
	defer targetStore.Close()
	if err := targetStore.SaveAgent(ctx, storage.AgentRow{
		ID:         "existing-agent",
		Name:       "existing-agent",
		AgentToken: "token-existing",
	}); err != nil {
		t.Fatalf("SaveAgent(target) error = %v", err)
	}
	if err := targetStore.SaveWireGuardProfiles(ctx, "existing-agent", []storage.WireGuardProfileRow{{
		ID:            3,
		AgentID:       "existing-agent",
		Name:          "existing-wg",
		Mode:          "generic_wireguard",
		PrivateKey:    testWireGuardPrivateKey,
		ListenPort:    51821,
		AddressesJSON: `["10.81.0.2/32"]`,
		PeersJSON:     `[]`,
		DNSJSON:       `[]`,
		Enabled:       true,
		Revision:      9,
	}}); err != nil {
		t.Fatalf("SaveWireGuardProfiles(target) error = %v", err)
	}

	failingStore := &failingBackupStore{
		backupStore:               targetStore,
		remainingHTTPRuleFailures: 1,
	}
	if _, err := NewBackupService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, failingStore).Import(ctx, archive); err == nil {
		t.Fatal("Import() error = nil, want forced import failure")
	}

	existingProfiles, err := targetStore.ListWireGuardProfiles(ctx, "existing-agent")
	if err != nil {
		t.Fatalf("ListWireGuardProfiles(existing-agent) error = %v", err)
	}
	if len(existingProfiles) != 1 || existingProfiles[0].Name != "existing-wg" || existingProfiles[0].Revision != 9 {
		t.Fatalf("existing wireguard profiles after rollback = %+v", existingProfiles)
	}
}

func TestBackupServiceRollbackRestoresWireGuardClientsOnImportFailure(t *testing.T) {
	ctx := t.Context()
	targetStore, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "rollback-wg-clients-target"), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore(target) error = %v", err)
	}
	defer targetStore.Close()

	if err := targetStore.SaveAgent(ctx, storage.AgentRow{
		ID:         "existing-agent",
		Name:       "existing-agent",
		AgentToken: "token-existing",
	}); err != nil {
		t.Fatalf("SaveAgent(target) error = %v", err)
	}
	if err := targetStore.SaveWireGuardProfiles(ctx, "existing-agent", []storage.WireGuardProfileRow{{
		ID:            3,
		AgentID:       "existing-agent",
		Name:          "existing-wg",
		Mode:          "generic_wireguard",
		PrivateKey:    testWireGuardPrivateKey,
		AddressesJSON: `["10.81.0.1/24"]`,
		PeersJSON: marshalJSON([]WireGuardPeer{{
			Name:         "existing-phone",
			PublicKey:    testWireGuardPublicKey,
			PresharedKey: testWireGuardPresharedKey,
			AllowedIPs:   []string{"10.81.0.2/32"},
		}}, "[]"),
		DNSJSON:  `[]`,
		Enabled:  true,
		Revision: 9,
	}}); err != nil {
		t.Fatalf("SaveWireGuardProfiles(target) error = %v", err)
	}
	if err := targetStore.SaveWireGuardClients(ctx, "existing-agent", 3, []storage.WireGuardClientRow{{
		ID:             1,
		AgentID:        "existing-agent",
		ProfileID:      3,
		Name:           "existing-phone",
		PrivateKey:     testWireGuardPrivateKey,
		PublicKey:      testWireGuardPublicKey,
		PresharedKey:   testWireGuardPresharedKey,
		Address:        "10.81.0.2/32",
		AllowedIPsJSON: `["0.0.0.0/0"]`,
		DNSJSON:        `["1.1.1.1"]`,
		Enabled:        false,
		CreatedAt:      "2026-05-16T08:00:00Z",
		UpdatedAt:      "2026-05-16T08:01:00Z",
	}}); err != nil {
		t.Fatalf("SaveWireGuardClients(target) error = %v", err)
	}

	sourceProfileID := 7
	bundle := BackupBundle{
		Manifest: BackupManifest{
			PackageVersion:     BackupPackageVersion,
			SourceArchitecture: BackupSourceArchitectureGo,
			ExportedAt:         time.Date(2026, 5, 16, 0, 0, 0, 0, time.UTC),
		},
		Agents: []BackupAgent{{
			ID:         "source-edge-wg",
			Name:       "source-edge-wg",
			AgentToken: "token-source-edge-wg",
		}},
		WireGuardProfiles: []BackupWireGuardProfile{{
			ID:         sourceProfileID,
			AgentID:    "source-edge-wg",
			Name:       "imported-wg",
			Mode:       "generic_wireguard",
			PrivateKey: testWireGuardPrivateKey,
			Addresses:  []string{"10.82.0.1/24"},
			Peers: []WireGuardPeer{{
				Name:       "imported-phone",
				PublicKey:  testWireGuardPublicKeyB,
				AllowedIPs: []string{"10.82.0.2/32"},
			}},
			Enabled: true,
		}},
		HTTPRules: []BackupHTTPRule{{
			ID:               11,
			AgentID:          "source-edge-wg",
			FrontendURL:      "https://force-failure.example.com",
			Backends:         []HTTPRuleBackend{{URL: "http://127.0.0.1:8096"}},
			LoadBalancing:    HTTPLoadBalancing{Strategy: "adaptive"},
			Enabled:          true,
			RelayLayers:      [][]int{},
			PassProxyHeaders: true,
		}},
	}
	archive, err := encodeBackupBundleWithWireGuardClients(t, bundle, []testBackupWireGuardClient{{
		ID:           1,
		AgentID:      "source-edge-wg",
		ProfileID:    sourceProfileID,
		Name:         "imported-phone",
		PrivateKey:   testWireGuardPresharedKey,
		PublicKey:    testWireGuardPublicKeyB,
		PresharedKey: testWireGuardPresharedKeyB,
		Address:      "10.82.0.2/32",
		AllowedIPs:   []string{"0.0.0.0/0"},
		DNS:          []string{"9.9.9.9"},
		Enabled:      true,
		CreatedAt:    "2026-05-16T09:00:00Z",
		UpdatedAt:    "2026-05-16T09:01:00Z",
	}})
	if err != nil {
		t.Fatalf("encodeBackupBundleWithWireGuardClients() error = %v", err)
	}

	failingStore := &failingBackupStore{
		backupStore:               targetStore,
		remainingHTTPRuleFailures: 1,
	}
	if _, err := NewBackupService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, failingStore).Import(ctx, archive); err == nil {
		t.Fatal("Import() error = nil, want forced import failure")
	}

	rows, err := targetStore.ListWireGuardClients(ctx, "existing-agent", 3)
	if err != nil {
		t.Fatalf("ListWireGuardClients(existing-agent) error = %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("existing wireguard clients after rollback = %+v", rows)
	}
	row := rows[0]
	if row.Name != "existing-phone" || row.PrivateKey != testWireGuardPrivateKey || row.PublicKey != testWireGuardPublicKey || row.PresharedKey != testWireGuardPresharedKey {
		t.Fatalf("existing wireguard client after rollback = %+v", row)
	}
	if row.Enabled || row.Address != "10.81.0.2/32" || row.AllowedIPsJSON != `["0.0.0.0/0"]` || row.DNSJSON != `["1.1.1.1"]` {
		t.Fatalf("existing wireguard client state after rollback = %+v", row)
	}
	importedRows, err := targetStore.ListWireGuardClients(ctx, "source-edge-wg", 0)
	if err != nil {
		t.Fatalf("ListWireGuardClients(source-edge-wg) error = %v", err)
	}
	if len(importedRows) != 0 {
		t.Fatalf("imported wireguard clients after rollback = %+v, want none", importedRows)
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

func TestBackupServicePreviewSkipsDuplicateIncomingHTTPRulesAfterFirstImport(t *testing.T) {
	targetStore, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "preview-target"), "local")
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
				ID:                    1,
				AgentID:               "source-local",
				FrontendURL:           "https://shared.example.com",
				Backends:              []HTTPRuleBackend{{URL: "http://127.0.0.1:8096"}},
				LoadBalancing:         HTTPLoadBalancing{Strategy: "adaptive"},
				Enabled:               true,
				PassProxyHeaders:      defaultPassProxyHeaders(),
				CustomHeaders:         []HTTPCustomHeader{},
				WireGuardEntryEnabled: false,
			},
			{
				ID:                    2,
				AgentID:               "backup-local",
				FrontendURL:           "https://shared.example.com",
				Backends:              []HTTPRuleBackend{{URL: "http://127.0.0.1:9096"}},
				LoadBalancing:         HTTPLoadBalancing{Strategy: "adaptive"},
				Enabled:               true,
				PassProxyHeaders:      defaultPassProxyHeaders(),
				CustomHeaders:         []HTTPCustomHeader{},
				WireGuardEntryEnabled: false,
			},
		},
		L4Rules:           []BackupL4Rule{},
		WireGuardProfiles: []BackupWireGuardProfile{},
		WireGuardClients:  []BackupWireGuardClient{},
		RelayListeners:    []BackupRelayListener{},
		Certificates:      []BackupCertificate{},
		VersionPolicies:   []BackupVersionPolicy{},
		TrafficPolicies:   []BackupTrafficPolicy{},
		TrafficBaselines:  []BackupTrafficBaseline{},
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

func TestBackupServicePreviewAndImportSkipDuplicateHTTPWireGuardInternalRoutes(t *testing.T) {
	sourceProfileID := 7
	bundle := BackupBundle{
		Manifest: BackupManifest{
			PackageVersion:     BackupPackageVersion,
			SourceArchitecture: BackupSourceArchitectureGo,
			SourceLocalAgentID: "source-local",
			Counts: BackupCounts{
				Agents:            1,
				HTTPRules:         2,
				WireGuardProfiles: 1,
			},
		},
		Agents: []BackupAgent{{
			ID:         "edge-http-wg",
			Name:       "edge-http-wg",
			AgentToken: "token-edge-http-wg",
			Capabilities: []string{
				"http_rules",
				"wireguard",
			},
		}},
		WireGuardProfiles: []BackupWireGuardProfile{{
			ID:         sourceProfileID,
			AgentID:    "edge-http-wg",
			Name:       "wg-http",
			Mode:       "generic_wireguard",
			PrivateKey: testWireGuardPrivateKey,
			ListenPort: 51820,
			Addresses:  []string{"10.44.0.1/24"},
			Peers: []WireGuardPeer{{
				Name:       "peer-a",
				PublicKey:  testWireGuardPublicKey,
				AllowedIPs: []string{"10.44.0.2/32"},
			}},
			DNS:     []string{},
			Enabled: true,
		}},
		HTTPRules: []BackupHTTPRule{
			{
				ID:                       1,
				AgentID:                  "edge-http-wg",
				FrontendURL:              "https://public-a.example.com/app/",
				Backends:                 []HTTPRuleBackend{{URL: "http://127.0.0.1:8096"}},
				LoadBalancing:            HTTPLoadBalancing{Strategy: "adaptive"},
				Enabled:                  true,
				PassProxyHeaders:         defaultPassProxyHeaders(),
				CustomHeaders:            []HTTPCustomHeader{},
				WireGuardEntryEnabled:    true,
				WireGuardProfileID:       &sourceProfileID,
				WireGuardEntryListenHost: "10.44.0.1",
				WireGuardEntryListenPort: 18096,
			},
			{
				ID:                       2,
				AgentID:                  "edge-http-wg",
				FrontendURL:              "https://public-b.example.com/app",
				Backends:                 []HTTPRuleBackend{{URL: "http://127.0.0.1:9096"}},
				LoadBalancing:            HTTPLoadBalancing{Strategy: "adaptive"},
				Enabled:                  true,
				PassProxyHeaders:         defaultPassProxyHeaders(),
				CustomHeaders:            []HTTPCustomHeader{},
				WireGuardEntryEnabled:    true,
				WireGuardProfileID:       &sourceProfileID,
				WireGuardEntryListenHost: "10.44.0.1",
				WireGuardEntryListenPort: 18096,
			},
		},
		L4Rules:          []BackupL4Rule{},
		WireGuardClients: []BackupWireGuardClient{},
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

	previewStore, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "preview-http-wg-duplicate-target"), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore(preview target) error = %v", err)
	}
	defer previewStore.Close()

	preview, err := NewBackupService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, previewStore).Preview(t.Context(), archive)
	if err != nil {
		t.Fatalf("Preview() error = %v", err)
	}
	if preview.Summary.Imported.HTTPRules != 1 || preview.Summary.SkippedConflict.HTTPRules != 1 {
		t.Fatalf("preview http summary = imported %+v skipped conflict %+v report %+v", preview.Summary.Imported, preview.Summary.SkippedConflict, preview.Report)
	}
	assertBackupSkippedConflictReason(t, preview, "http_rule", "https://public-b.example.com/app", "wireguard entry route already exists")

	importStore, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "import-http-wg-duplicate-target"), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore(import target) error = %v", err)
	}
	defer importStore.Close()

	importResult, err := NewBackupService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, importStore).Import(t.Context(), archive)
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	if importResult.Summary.Imported.HTTPRules != 1 || importResult.Summary.SkippedConflict.HTTPRules != 1 {
		t.Fatalf("import http summary = imported %+v skipped conflict %+v report %+v", importResult.Summary.Imported, importResult.Summary.SkippedConflict, importResult.Report)
	}
	assertBackupSkippedConflictReason(t, importResult, "http_rule", "https://public-b.example.com/app", "wireguard entry route already exists")
	rows, err := importStore.ListHTTPRules(t.Context(), "edge-http-wg")
	if err != nil {
		t.Fatalf("ListHTTPRules(edge-http-wg) error = %v", err)
	}
	if len(rows) != 1 || rows[0].FrontendURL != "https://public-a.example.com/app/" {
		t.Fatalf("imported http rows = %+v, want only first WireGuard route", rows)
	}
}

func TestBackupServicePreviewAndImportSkipDuplicateHTTPWireGuardInternalRoutesWithDefaultListenHost(t *testing.T) {
	sourceProfileID := 7
	bundle := BackupBundle{
		Manifest: BackupManifest{
			PackageVersion:     BackupPackageVersion,
			SourceArchitecture: BackupSourceArchitectureGo,
			SourceLocalAgentID: "source-local",
			Counts: BackupCounts{
				Agents:            1,
				HTTPRules:         2,
				WireGuardProfiles: 1,
			},
		},
		Agents: []BackupAgent{{
			ID:         "edge-http-wg-default",
			Name:       "edge-http-wg-default",
			AgentToken: "token-edge-http-wg-default",
			Capabilities: []string{
				"http_rules",
				"wireguard",
			},
		}},
		WireGuardProfiles: []BackupWireGuardProfile{{
			ID:         sourceProfileID,
			AgentID:    "edge-http-wg-default",
			Name:       "wg-http-default",
			Mode:       "generic_wireguard",
			PrivateKey: testWireGuardPrivateKey,
			ListenPort: 51820,
			Addresses:  []string{"10.45.0.1/24"},
			Peers: []WireGuardPeer{{
				Name:       "peer-a",
				PublicKey:  testWireGuardPublicKey,
				AllowedIPs: []string{"10.45.0.2/32"},
			}},
			DNS:     []string{},
			Enabled: true,
		}},
		L4Rules:          []BackupL4Rule{},
		WireGuardClients: []BackupWireGuardClient{},
		RelayListeners:   []BackupRelayListener{},
		Certificates:     []BackupCertificate{},
		VersionPolicies:  []BackupVersionPolicy{},
		TrafficPolicies:  []BackupTrafficPolicy{},
		TrafficBaselines: []BackupTrafficBaseline{},
	}
	httpRules := []map[string]any{
		{
			"id":                          1,
			"agent_id":                    "edge-http-wg-default",
			"frontend_url":                "https://public-a.example.com/app/",
			"backends":                    []map[string]any{{"url": "http://127.0.0.1:8096"}},
			"load_balancing":              map[string]any{"strategy": "adaptive"},
			"enabled":                     true,
			"proxy_redirect":              false,
			"pass_proxy_headers":          defaultPassProxyHeaders(),
			"custom_headers":              []map[string]any{},
			"wireguard_entry_enabled":     true,
			"wireguard_profile_id":        sourceProfileID,
			"wireguard_entry_listen_host": "   ",
			"wireguard_entry_listen_port": 18096,
		},
		{
			"id":                          2,
			"agent_id":                    "edge-http-wg-default",
			"frontend_url":                "https://public-b.example.com/app",
			"backends":                    []map[string]any{{"url": "http://127.0.0.1:9096"}},
			"load_balancing":              map[string]any{"strategy": "adaptive"},
			"enabled":                     true,
			"proxy_redirect":              false,
			"pass_proxy_headers":          defaultPassProxyHeaders(),
			"custom_headers":              []map[string]any{},
			"wireguard_entry_enabled":     true,
			"wireguard_profile_id":        sourceProfileID,
			"wireguard_entry_listen_port": 18096,
		},
	}
	archive, err := encodeBackupBundleWithHTTPRules(t, bundle, httpRules)
	if err != nil {
		t.Fatalf("encodeBackupBundleWithHTTPRules() error = %v", err)
	}

	previewStore, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "preview-http-wg-default-host-duplicate-target"), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore(preview target) error = %v", err)
	}
	defer previewStore.Close()

	preview, err := NewBackupService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, previewStore).Preview(t.Context(), archive)
	if err != nil {
		t.Fatalf("Preview() error = %v", err)
	}
	if preview.Summary.Imported.HTTPRules != 1 || preview.Summary.SkippedConflict.HTTPRules != 1 {
		t.Fatalf("preview http summary = imported %+v skipped conflict %+v report %+v", preview.Summary.Imported, preview.Summary.SkippedConflict, preview.Report)
	}
	assertBackupSkippedConflictReason(t, preview, "http_rule", "https://public-b.example.com/app", "wireguard entry route already exists")

	importStore, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "import-http-wg-default-host-duplicate-target"), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore(import target) error = %v", err)
	}
	defer importStore.Close()

	importResult, err := NewBackupService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, importStore).Import(t.Context(), archive)
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	if importResult.Summary.Imported.HTTPRules != 1 || importResult.Summary.SkippedConflict.HTTPRules != 1 {
		t.Fatalf("import http summary = imported %+v skipped conflict %+v report %+v", importResult.Summary.Imported, importResult.Summary.SkippedConflict, importResult.Report)
	}
	assertBackupSkippedConflictReason(t, importResult, "http_rule", "https://public-b.example.com/app", "wireguard entry route already exists")
	rows, err := importStore.ListHTTPRules(t.Context(), "edge-http-wg-default")
	if err != nil {
		t.Fatalf("ListHTTPRules(edge-http-wg-default) error = %v", err)
	}
	if len(rows) != 1 || rows[0].FrontendURL != "https://public-a.example.com/app/" || rows[0].WireGuardEntryListenHost != "10.45.0.1" {
		t.Fatalf("imported http rows = %+v, want first WireGuard route with default listen host", rows)
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
