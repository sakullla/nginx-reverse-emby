//go:build integration

package service

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

var backupRelayCAFixturesOnce sync.Once
var backupRelaySourceCAFixture relayMaterial
var backupRelayTargetCAFixture relayMaterial
var backupRelayArchiveOnce sync.Once
var backupRelayArchiveFixture []byte
var backupRelayArchiveSourceCA storage.ManagedCertificateBundle
var backupRelayArchiveSourceLeaf storage.ManagedCertificateBundle
var backupRelayArchiveErr error

func backupRelayCAFixture(t *testing.T, target bool) storage.ManagedCertificateBundle {
	t.Helper()
	backupRelayCAFixturesOnce.Do(func() {
		backupRelaySourceCAFixture = createSelfSignedCA(t, "source."+relayCADomainIdentity)
		backupRelayTargetCAFixture = createSelfSignedCA(t, "target."+relayCADomainIdentity)
	})
	material := backupRelaySourceCAFixture
	if target {
		material = backupRelayTargetCAFixture
	}
	return storage.ManagedCertificateBundle{
		Domain:  relayCADomainIdentity,
		CertPEM: material.CertPEM,
		KeyPEM:  material.KeyPEM,
	}
}

func backupRelayLeafFixture(t *testing.T, domain, host string, ca storage.ManagedCertificateBundle) storage.ManagedCertificateBundle {
	t.Helper()
	material := mustCreateLeafSignedByCA(t, host, relayMaterial{CertPEM: ca.CertPEM, KeyPEM: ca.KeyPEM})
	return storage.ManagedCertificateBundle{Domain: domain, CertPEM: material.CertPEM, KeyPEM: material.KeyPEM}
}

func backupRelayCAArchive(t *testing.T) ([]byte, storage.ManagedCertificateBundle, storage.ManagedCertificateBundle) {
	t.Helper()
	backupRelayArchiveOnce.Do(func() {
		sourceStore, err := newServiceTestSQLiteStore(t, filepath.Join(t.TempDir(), "relay-ca-source"), "local")
		if err != nil {
			backupRelayArchiveErr = err
			return
		}
		defer func() {
			if err := sourceStore.Close(); err != nil && backupRelayArchiveErr == nil {
				backupRelayArchiveErr = err
			}
		}()

		ctx := context.Background()
		if err := sourceStore.SaveAgent(ctx, storage.AgentRow{
			ID: "edge-a", Name: "edge-a", AgentToken: "source-token",
		}); err != nil {
			backupRelayArchiveErr = err
			return
		}

		sourceCA := backupRelayCAFixture(t, false)
		sourceCA.ID = 10
		sourceLeaf := backupRelayLeafFixture(t, "listener.edge-a.relay.internal", "relay.example.com", sourceCA)
		sourceLeaf.ID = 11
		sourcePins, err := deriveRelayPinSetFromCertificate(sourceLeaf.CertPEM)
		if err != nil {
			backupRelayArchiveErr = err
			return
		}
		if err := sourceStore.SaveManagedCertificates(ctx, []storage.ManagedCertificateRow{
			managedCertificateToRow(ManagedCertificate{
				ID: 10, Domain: relayCADomainIdentity, Enabled: false, Scope: "domain", IssuerMode: "local_http01",
				TargetAgentIDs: []string{"edge-a"}, Status: "active", Usage: "https", CertificateType: "internal_ca",
				SelfSigned: false, Tags: []string{"legacy"}, Revision: 1,
			}),
			managedCertificateToRow(ManagedCertificate{
				ID: 11, Domain: sourceLeaf.Domain, Enabled: true, Scope: "domain", IssuerMode: "local_http01",
				TargetAgentIDs: []string{"edge-a"}, Status: "active", Usage: "relay_tunnel", CertificateType: "internal_ca",
				SelfSigned: false, Tags: autoRelayListenerCertificateTags(31, "edge-a"), Revision: 2,
			}),
		}); err != nil {
			backupRelayArchiveErr = err
			return
		}
		if err := sourceStore.SaveManagedCertificateMaterial(ctx, relayCADomainIdentity, sourceCA); err != nil {
			backupRelayArchiveErr = err
			return
		}
		if err := sourceStore.SaveManagedCertificateMaterial(ctx, sourceLeaf.Domain, sourceLeaf); err != nil {
			backupRelayArchiveErr = err
			return
		}
		if err := sourceStore.SaveRelayListeners(ctx, "edge-a", []storage.RelayListenerRow{{
			ID: 31, AgentID: "edge-a", Name: "relay-auto", ListenHost: "127.0.0.1", BindHostsJSON: `["127.0.0.1"]`,
			ListenPort: 7443, PublicHost: "relay.example.com", PublicPort: 7443, Enabled: true,
			CertificateID: backupIntPtr(11), TLSMode: "pin_and_ca", TransportMode: "tls_tcp", ObfsMode: "off",
			PinSetJSON: marshalJSON(sourcePins, "[]"), TrustedCACertificateIDs: `[10]`, AllowSelfSigned: true,
			TagsJSON: `[]`, Revision: 3,
		}}); err != nil {
			backupRelayArchiveErr = err
			return
		}

		backupRelayArchiveFixture, _, backupRelayArchiveErr = NewBackupService(
			config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, sourceStore,
		).Export(ctx)
		backupRelayArchiveSourceCA = sourceCA
		backupRelayArchiveSourceLeaf = sourceLeaf
	})
	if backupRelayArchiveErr != nil {
		t.Fatalf("build relay CA backup fixture: %v", backupRelayArchiveErr)
	}
	return append([]byte(nil), backupRelayArchiveFixture...), backupRelayArchiveSourceCA, backupRelayArchiveSourceLeaf
}

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

func TestIntegrationBackupServiceExportImportRoundTripAndConflictReport(t *testing.T) {
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

func TestIntegrationBackupServiceImportRemapsEgressProfileReferences(t *testing.T) {
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

func TestIntegrationBackupServiceImportBumpsRelayedEgressFinalHopAgent(t *testing.T) {
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

func TestIntegrationBackupServiceImportMigratesLegacyL4ProxyEgress(t *testing.T) {
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

func TestIntegrationBackupServiceTrafficPolicyAndBaselineRoundTripExcludesHistory(t *testing.T) {
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

func TestIntegrationBackupServicePreviewAndImportSkipUnsupportedLegacyResources(t *testing.T) {
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

func TestIntegrationBackupServicePreviewAndImportUseNormalizedL4ListenHostConflictKeys(t *testing.T) {
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

func TestIntegrationBackupServiceImportSkipsRulesWithMissingRelayLayerDependencies(t *testing.T) {
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

func TestIntegrationBackupServicePreviewAndImportRejectRelayListenerBindConflictWithExisting(t *testing.T) {
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

type backupProviderAdmissionStore struct {
	backupStore
	loadAgentID   string
	loadPlatform  string
	generations   []storage.PluginGeneration
	instance      storage.PluginInstanceRow
	instanceFound bool
	status        storage.PluginAgentRuntimeStatusRow
	statusFound   bool
}

func (s *backupProviderAdmissionStore) LoadAgentPluginGenerations(_ context.Context, agentID, platform string) ([]storage.PluginGeneration, error) {
	if agentID != s.loadAgentID || platform != s.loadPlatform {
		return nil, nil
	}
	return append([]storage.PluginGeneration(nil), s.generations...), nil
}

func (s *backupProviderAdmissionStore) GetPluginInstance(_ context.Context, instanceID string) (storage.PluginInstanceRow, bool, error) {
	if !s.instanceFound || instanceID != s.instance.ID {
		return storage.PluginInstanceRow{}, false, nil
	}
	return s.instance, true, nil
}

func (s *backupProviderAdmissionStore) GetPluginAgentRuntimeStatusFence(_ context.Context, operationID, agentID, instanceID string) (storage.PluginAgentRuntimeStatusRow, bool, error) {
	if !s.statusFound || operationID != s.status.OperationID || agentID != s.status.AgentID || instanceID != s.status.InstanceID {
		return storage.PluginAgentRuntimeStatusRow{}, false, nil
	}
	return s.status, true, nil
}

func TestIntegrationBackupHTTPProviderAdmissionMatchesPreviewAndImport(t *testing.T) {
	const (
		targetAgentID = "edge-target"
		sourceAgentID = "edge-source"
		instanceID    = "provider-1"
		providerID    = "default"
		frontendURL   = "https://provider.example.test"
	)

	for _, test := range []struct {
		name         string
		platform     string
		mutate       func(*backupProviderAdmissionStore, *storage.PluginGeneration)
		unauthorized bool
		wantImported bool
	}{
		{name: "missing", platform: "linux-amd64", mutate: func(store *backupProviderAdmissionStore, _ *storage.PluginGeneration) {
			store.instanceFound = false
		}},
		{name: "inactive", platform: "linux-amd64", mutate: func(store *backupProviderAdmissionStore, _ *storage.PluginGeneration) {
			store.instance.CurrentState = "disabled"
		}},
		{name: "recovered degraded", platform: "linux-amd64", wantImported: true, mutate: func(store *backupProviderAdmissionStore, _ *storage.PluginGeneration) {
			store.instance.CurrentState = "degraded"
		}},
		{name: "wrong agent", platform: "linux-amd64", mutate: func(_ *backupProviderAdmissionStore, generation *storage.PluginGeneration) {
			generation.Target.ID = "edge-other"
		}},
		{name: "platform", platform: "windows-amd64", mutate: func(_ *backupProviderAdmissionStore, _ *storage.PluginGeneration) {}},
		{name: "unauthorized", platform: "linux-amd64", unauthorized: true, mutate: func(_ *backupProviderAdmissionStore, _ *storage.PluginGeneration) {}},
		{name: "success", platform: "linux-amd64", wantImported: true, mutate: func(_ *backupProviderAdmissionStore, _ *storage.PluginGeneration) {}},
	} {
		t.Run(test.name, func(t *testing.T) {
			sqliteStore, err := newServiceTestSQLiteStore(t, filepath.Join(t.TempDir(), "provider-admission"), "local")
			if err != nil {
				t.Fatal(err)
			}
			defer sqliteStore.Close()
			if err := sqliteStore.SaveAgent(t.Context(), storage.AgentRow{
				ID: targetAgentID, Name: "Edge Target", AgentToken: "target-token", Platform: test.platform,
			}); err != nil {
				t.Fatal(err)
			}

			generation := storage.PluginGeneration{
				ID: "generation-1", InstanceID: instanceID, OperationID: "operation-1",
				Runtime:              storage.PluginGenerationRuntime{Kind: pluginsdk.RuntimeRPCService, ABI: pluginsdk.RPCABIV1, HostScope: pluginsdk.HostScopeAgent},
				ExtensionPoints:      []string{pluginsdk.ExtensionHTTPBackendProvider},
				RequiredFeatures:     []string{pluginsdk.RPCFeatureHTTPBackendProviderV1},
				HTTPBackendProviders: []pluginsdk.HTTPBackendProviderDescriptor{{ID: providerID, DisplayName: "Default"}},
				Target:               storage.PluginGenerationTarget{ID: targetAgentID, ResourceGroupID: "group-a", Version: 1},
			}
			store := &backupProviderAdmissionStore{
				backupStore:   sqliteStore,
				loadAgentID:   targetAgentID,
				loadPlatform:  "linux-amd64",
				instance:      storage.PluginInstanceRow{ID: instanceID, ResourceGroupID: "group-a", DesiredEnabled: true, CurrentState: "active"},
				instanceFound: true,
				status:        storage.PluginAgentRuntimeStatusRow{OperationID: generation.OperationID, AgentID: targetAgentID, InstanceID: instanceID, GenerationID: generation.ID, State: "active"},
				statusFound:   true,
			}
			test.mutate(store, &generation)
			store.generations = []storage.PluginGeneration{generation}

			bundle := BackupBundle{
				Manifest: BackupManifest{PackageVersion: BackupPackageVersion, SourceArchitecture: BackupSourceArchitectureGo, ExportedAt: time.Now().UTC()},
				Agents:   []BackupAgent{{ID: sourceAgentID, Name: "Edge Target", AgentToken: "source-token", Platform: test.platform}},
				HTTPRules: []BackupHTTPRule{{
					ID: 41, AgentID: sourceAgentID, FrontendURL: frontendURL, Enabled: true,
					Backends: []HTTPRuleBackend{{Kind: pluginsdk.HTTPBackendKindPluginProvider, PluginProvider: &pluginsdk.HTTPPluginProviderRef{InstanceID: instanceID, ProviderID: providerID}}},
				}},
			}
			archive, err := encodeBackupBundle(bundle)
			if err != nil {
				t.Fatal(err)
			}
			ctx := WithSystemMutationPrincipal(t.Context(), "backup-test")
			if test.unauthorized {
				ctx = WithResourceAuthorizer(ctx, func(_ context.Context, kind, id string) error {
					if kind == "plugin_instance" && id == instanceID {
						return errors.New("provider access denied")
					}
					return nil
				})
			}
			svc := NewBackupService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, store)
			svc.mutationStore = nil

			baselineRevisions, err := sqliteStore.ListAgentRevisions(ctx, targetAgentID)
			if err != nil {
				t.Fatal(err)
			}
			preview, err := svc.Preview(ctx, archive)
			if err != nil {
				t.Fatalf("Preview() error = %v", err)
			}
			previewRules, err := sqliteStore.ListHTTPRules(ctx, targetAgentID)
			if err != nil || len(previewRules) != 0 {
				t.Fatalf("Preview() wrote rules: rows=%+v err=%v", previewRules, err)
			}
			previewRevisions, err := sqliteStore.ListAgentRevisions(ctx, targetAgentID)
			if err != nil || len(previewRevisions) != len(baselineRevisions) {
				t.Fatalf("Preview() changed revisions from %d to %d: %v", len(baselineRevisions), len(previewRevisions), err)
			}

			imported, err := svc.Import(ctx, archive)
			if err != nil {
				t.Fatalf("Import() error = %v", err)
			}
			rules, err := sqliteStore.ListHTTPRules(ctx, targetAgentID)
			if err != nil {
				t.Fatal(err)
			}
			revisions, err := sqliteStore.ListAgentRevisions(ctx, targetAgentID)
			if err != nil {
				t.Fatal(err)
			}

			if test.wantImported {
				if preview.Summary.Imported.HTTPRules != 1 || imported.Summary.Imported.HTTPRules != 1 || len(rules) != 1 {
					t.Fatalf("successful provider import mismatch: preview=%+v import=%+v rules=%+v", preview.Summary, imported.Summary, rules)
				}
				return
			}
			if preview.Summary.SkippedInvalid.HTTPRules != 1 || imported.Summary.SkippedInvalid.HTTPRules != 1 || len(rules) != 0 {
				t.Fatalf("invalid provider result mismatch: preview=%+v import=%+v rules=%+v", preview.Summary, imported.Summary, rules)
			}
			if len(revisions) != len(baselineRevisions) {
				t.Fatalf("invalid provider import changed revisions from %d to %d", len(baselineRevisions), len(revisions))
			}
			previewReason := backupSkippedInvalidReason(preview, "http_rule", frontendURL)
			importReason := backupSkippedInvalidReason(imported, "http_rule", frontendURL)
			if previewReason == "" || previewReason != importReason {
				t.Fatalf("unstable skipped_invalid reason: preview=%q import=%q", previewReason, importReason)
			}
		})
	}
}

func backupSkippedInvalidReason(result BackupImportResult, kind, key string) string {
	for _, item := range result.Report.SkippedInvalid {
		if item.Kind == kind && item.Key == key {
			return item.Reason
		}
	}
	return ""
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

func TestIntegrationBackupServiceRollbackOnImportFailure(t *testing.T) {
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

func TestIntegrationBackupServiceImportBumpsDesiredRevisionForCertificateOnlyRestore(t *testing.T) {
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

func TestIntegrationBackupServicePreviewAccountsForAgentRemapBeforeConflictChecks(t *testing.T) {
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

func TestIntegrationBackupServicePreviewRejectsRelayListenersWithMissingCertificateDependencies(t *testing.T) {
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

func TestIntegrationBackupServiceImportReplacesExistingSystemRelayCAMaterial(t *testing.T) {
	t.Parallel()
	archive, sourceCA, sourceLeaf := backupRelayCAArchive(t)
	targetStore, err := newServiceTestSQLiteStore(t, filepath.Join(t.TempDir(), "target"), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore(target) error = %v", err)
	}
	defer targetStore.Close()

	ctx := t.Context()
	targetCA := backupRelayCAFixture(t, true)
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

func TestIntegrationBackupServiceImportSkipsSystemRelayCAReplacementWhenExistingRelayCertDependsOnCurrentCA(t *testing.T) {
	t.Parallel()
	archive, _, _ := backupRelayCAArchive(t)
	targetStore, err := newServiceTestSQLiteStore(t, filepath.Join(t.TempDir(), "target"), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore(target) error = %v", err)
	}
	defer targetStore.Close()

	ctx := t.Context()
	cfg := config.Config{EnableLocalAgent: true, LocalAgentID: "local"}

	targetCA := backupRelayCAFixture(t, true)
	targetLeaf := backupRelayLeafFixture(t, "listener.target.relay.internal", "target-relay.example.com", targetCA)
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

	targetSvc := NewBackupService(cfg, targetStore)
	preview, err := targetSvc.Preview(ctx, archive)
	if err != nil {
		t.Fatalf("Preview() error = %v", err)
	}
	if preview.Summary.SkippedConflict.Certificates != 1 || preview.Summary.Imported.Certificates != 1 || preview.Summary.SkippedInvalid.RelayListeners != 1 {
		t.Fatalf("preview summary = %+v", preview.Summary)
	}
	assertBackupSkippedConflictReason(t, preview, "certificate", relayCADomainIdentity, backupSystemRelayCAReplacementConflictReason)
	assertBackupSkippedInvalidReason(t, preview, "relay_listener", "edge-a|relay-auto", "referenced certificate was not imported")

	result, err := targetSvc.Import(ctx, archive)
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	if result.Summary.SkippedConflict.Certificates != 1 || result.Summary.Imported.Certificates != 1 || result.Summary.SkippedInvalid.RelayListeners != 1 {
		t.Fatalf("import summary = %+v", result.Summary)
	}
	assertBackupSkippedConflictReason(t, result, "certificate", relayCADomainIdentity, backupSystemRelayCAReplacementConflictReason)
	assertBackupSkippedInvalidReason(t, result, "relay_listener", "edge-a|relay-auto", "referenced trusted CA certificate was not imported")

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

func TestIntegrationBackupServiceImportValidationFailureRollsBackResourcesAndRevisionLedger(t *testing.T) {
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
func TestIntegrationBackupServiceRoundTripsAgentDDNSConfigWithoutCredentials(t *testing.T) {
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
