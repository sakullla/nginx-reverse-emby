package localagent

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"testing"
	"time"

	goagentembedded "github.com/sakullla/nginx-reverse-emby/go-agent/embedded"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/app"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

type bridgeStoreStub struct {
	snapshot             Snapshot
	loadAgentID          string
	savedAgentID         string
	savedState           RuntimeState
	saveLocalStateCalled bool
	managedCerts         []storage.ManagedCertificateRow
	rulesByAgent         map[string][]storage.HTTPRuleRow
	saveManagedCalled    bool
}

type embeddedRuntimeStub struct {
	start func(context.Context) error
}

func (s embeddedRuntimeStub) Run(ctx context.Context) error {
	if s.start != nil {
		return s.start(ctx)
	}
	return nil
}

func (s embeddedRuntimeStub) SyncNow(context.Context) error {
	return nil
}

func (s embeddedRuntimeStub) DiagnoseSnapshot(context.Context, goagentembedded.Snapshot, goagentembedded.DiagnosticRequest) (map[string]any, error) {
	return map[string]any{}, nil
}

func (s *bridgeStoreStub) LoadLocalSnapshot(_ context.Context, agentID string) (Snapshot, error) {
	s.loadAgentID = agentID
	return s.snapshot, nil
}

func (s *bridgeStoreStub) SaveLocalRuntimeState(_ context.Context, agentID string, state RuntimeState) error {
	s.savedAgentID = agentID
	s.savedState = state
	s.saveLocalStateCalled = true
	return nil
}

func (s *bridgeStoreStub) ListManagedCertificates(context.Context) ([]storage.ManagedCertificateRow, error) {
	return append([]storage.ManagedCertificateRow(nil), s.managedCerts...), nil
}

func (s *bridgeStoreStub) ListHTTPRules(_ context.Context, agentID string) ([]storage.HTTPRuleRow, error) {
	return append([]storage.HTTPRuleRow(nil), s.rulesByAgent[agentID]...), nil
}

func (s *bridgeStoreStub) SaveManagedCertificates(_ context.Context, rows []storage.ManagedCertificateRow) error {
	s.managedCerts = append([]storage.ManagedCertificateRow(nil), rows...)
	s.saveManagedCalled = true
	return nil
}

func TestNewRuntimeStartsEmbeddedRuntimeWithBridgeAdapters(t *testing.T) {
	cfg := config.Default()
	cfg.EnableLocalAgent = true
	cfg.LocalAgentID = "local-test"
	cfg.LocalAgentName = "local-test"
	cfg.LocalAgentHTTP3Enabled = true
	cfg.LocalAgentHTTPTransport.DialTimeout = 7 * time.Second
	cfg.LocalAgentHTTPResilience.ResumeMaxAttempts = 4
	cfg.LocalAgentBackendFailures.BackoffBase = 1 * time.Second
	cfg.LocalAgentBackendFailures.BackoffLimit = 15 * time.Second
	cfg.LocalAgentBackendFailuresExplicit = true
	cfg.LocalAgentRelayTimeouts.IdleTimeout = 12 * time.Second

	store := &bridgeStoreStub{
		snapshot: Snapshot{
			DesiredVersion: "1.2.3",
			Revision:       15,
		},
	}

	started := false
	previousNewEmbeddedRuntime := newEmbeddedRuntime
	t.Cleanup(func() {
		newEmbeddedRuntime = previousNewEmbeddedRuntime
	})

	newEmbeddedRuntime = func(cfg goagentembedded.Config, source goagentembedded.SyncSource, sink goagentembedded.StateSink) (embeddedRuntimeRunner, error) {
		if cfg.AgentID != "local-test" {
			t.Fatalf("embedded runtime AgentID = %q", cfg.AgentID)
		}
		if !cfg.HTTP3Enabled {
			t.Fatal("expected HTTP3Enabled to propagate")
		}
		if cfg.HTTPTransport.DialTimeout != 7*time.Second {
			t.Fatalf("DialTimeout = %v", cfg.HTTPTransport.DialTimeout)
		}
		if cfg.HTTPResilience.ResumeMaxAttempts != 4 {
			t.Fatalf("ResumeMaxAttempts = %d", cfg.HTTPResilience.ResumeMaxAttempts)
		}
		if !cfg.BackendFailuresExplicit {
			t.Fatal("expected BackendFailuresExplicit to propagate")
		}
		if cfg.RelayTimeouts.IdleTimeout != 12*time.Second {
			t.Fatalf("IdleTimeout = %v", cfg.RelayTimeouts.IdleTimeout)
		}
		request := mustDecodeEmbeddedSyncRequest(t, `{
			"CurrentRevision": 14,
			"LastApplyRevision": 13,
			"LastApplyStatus": "error",
			"LastApplyMessage": "apply failed",
			"ManagedCertificateReports": [
				{
					"id": 21,
					"domain": "sync.example.com",
					"status": "active",
					"last_issue_at": "2026-04-11T12:00:00Z",
					"material_hash": "hash-21",
					"acme_info": {"Main_Domain":"sync.example.com"}
				}
			]
		}`)
		snapshot, err := source.Sync(t.Context(), request)
		if err != nil {
			t.Fatalf("source.Sync() error = %v", err)
		}
		if snapshot.Revision != 15 {
			t.Fatalf("source.Sync() revision = %d", snapshot.Revision)
		}
		if err := sink.Save(t.Context(), goagentembedded.RuntimeState{
			CurrentRevision: 27,
			Status:          "active",
			Metadata: map[string]string{
				"last_sync_error": "apply failed",
			},
		}); err != nil {
			t.Fatalf("sink.Save() error = %v", err)
		}
		return embeddedRuntimeStub{
			start: func(context.Context) error {
				started = true
				return nil
			},
		}, nil
	}

	runtime, err := NewRuntime(cfg, store)
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}

	if err := runtime.Start(t.Context()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if !started {
		t.Fatal("embedded runtime was not started")
	}
	if store.loadAgentID != "local-test" {
		t.Fatalf("LoadLocalSnapshot() agentID = %q", store.loadAgentID)
	}
	if !store.saveLocalStateCalled {
		t.Fatal("SaveLocalRuntimeState() was not called")
	}
	if store.savedAgentID != "local-test" {
		t.Fatalf("SaveLocalRuntimeState() agentID = %q", store.savedAgentID)
	}
	if store.savedState.CurrentRevision != 27 || store.savedState.Status != "active" {
		t.Fatalf("SaveLocalRuntimeState() state = %+v", store.savedState)
	}
	if store.savedState.LastApplyRevision != 0 || store.savedState.LastApplyStatus != "" || store.savedState.LastApplyMessage != "" {
		t.Fatalf("SaveLocalRuntimeState() stale request apply metadata should not override runtime metadata = %+v", store.savedState)
	}
	if len(store.savedState.ManagedCertificateReports) != 1 || store.savedState.ManagedCertificateReports[0].ID != 21 {
		t.Fatalf("SaveLocalRuntimeState() managed reports = %+v", store.savedState.ManagedCertificateReports)
	}
}

func TestAppStartsEmbeddedLocalAgentWhenEnabled(t *testing.T) {
	var started bool
	application := app.New(
		config.Config{
			ListenAddr:       "127.0.0.1:0",
			EnableLocalAgent: true,
		},
		http.NewServeMux(),
		nil,
		func(context.Context) error {
			started = true
			return nil
		},
	)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_ = application.Run(ctx)

	if !started {
		t.Fatal("embedded local agent did not start")
	}
}

func TestLocalSyncSourceReturnsSnapshotFromControlPlaneStore(t *testing.T) {
	store := &bridgeStoreStub{
		snapshot: Snapshot{
			DesiredVersion: "1.2.3",
			Revision:       15,
		},
	}

	source := NewSyncSource(store, "local")
	got, err := source.Sync(t.Context(), SyncRequest{CurrentRevision: 14})
	if err != nil {
		t.Fatalf("Sync() error = %v", err)
	}

	if store.loadAgentID != "local" {
		t.Fatalf("LoadLocalSnapshot() agentID = %q", store.loadAgentID)
	}
	if got.Revision != 15 || got.DesiredVersion != "1.2.3" {
		t.Fatalf("Sync() = %+v", got)
	}
}

func TestToEmbeddedSnapshotPreservesRelayTransportFields(t *testing.T) {
	snapshot := Snapshot{
		Revision: 15,
		Rules: []storage.HTTPRule{{
			ID:          7,
			FrontendURL: "https://media.example.test",
			BackendURL:  "http://127.0.0.1:8096",
			RelayLayers: [][]int{{1, 2}, {3}},
		}},
		L4Rules: []storage.L4Rule{{
			ID:              11,
			Name:            "tcp-game",
			Protocol:        "tcp",
			ListenHost:      "0.0.0.0",
			ListenPort:      19000,
			ListenMode:      "proxy",
			ProxyEntryAuth:  storage.L4ProxyEntryAuth{Enabled: true, Username: "client", Password: "secret"},
			ProxyEgressMode: "proxy",
			ProxyEgressURL:  "socks5h://egress:pass@127.0.0.1:1080",
			Backends: []storage.L4Backend{{
				Host: "relay-echo-test",
				Port: 18081,
			}},
			RelayChain:  []int{1},
			RelayLayers: [][]int{{1}, {2, 3}},
			RelayObfs:   true,
			Revision:    3,
		}},
		RelayListeners: []storage.RelayListener{{
			ID:                     1,
			AgentID:                "local",
			AgentName:              "Local Node",
			Name:                   "relay-self",
			ListenHost:             "0.0.0.0",
			BindHosts:              []string{"0.0.0.0"},
			ListenPort:             9443,
			PublicHost:             "127.0.0.1",
			PublicPort:             9443,
			Enabled:                true,
			TLSMode:                "pin_and_ca",
			TransportMode:          "quic",
			AllowTransportFallback: true,
			ObfsMode:               "early_window_v2",
			PinSet: []storage.RelayPin{{
				Type:  "spki_sha256",
				Value: "pin",
			}},
			TrustedCACertificateIDs: []int{1},
			AllowSelfSigned:         true,
			Revision:                2,
		}},
	}

	embedded := toEmbeddedSnapshot(snapshot)

	if len(embedded.Rules) != 1 || embedded.Rules[0].ID != 7 {
		t.Fatalf("embedded HTTP rule IDs = %+v", embedded.Rules)
	}
	if len(embedded.Rules[0].RelayLayers) != 2 || embedded.Rules[0].RelayLayers[1][0] != 3 {
		t.Fatalf("embedded HTTP RelayLayers = %+v", embedded.Rules[0].RelayLayers)
	}

	if len(embedded.L4Rules) != 1 {
		t.Fatalf("embedded L4Rules len = %d, want 1", len(embedded.L4Rules))
	}
	if embedded.L4Rules[0].ID != 11 || embedded.L4Rules[0].Name != "tcp-game" {
		t.Fatalf("embedded L4Rules[0] identity = %+v", embedded.L4Rules[0])
	}
	if !embedded.L4Rules[0].RelayObfs {
		t.Fatalf("embedded L4Rules[0].RelayObfs = false, want true")
	}
	if embedded.L4Rules[0].ListenMode != "proxy" {
		t.Fatalf("embedded L4Rules[0].ListenMode = %q, want proxy", embedded.L4Rules[0].ListenMode)
	}
	if !embedded.L4Rules[0].ProxyEntryAuth.Enabled || embedded.L4Rules[0].ProxyEntryAuth.Username != "client" || embedded.L4Rules[0].ProxyEntryAuth.Password != "secret" {
		t.Fatalf("embedded L4Rules[0].ProxyEntryAuth = %+v", embedded.L4Rules[0].ProxyEntryAuth)
	}
	if embedded.L4Rules[0].ProxyEgressMode != "proxy" || embedded.L4Rules[0].ProxyEgressURL != "socks5h://egress:pass@127.0.0.1:1080" {
		t.Fatalf("embedded L4Rules[0] proxy egress = mode %q url %q", embedded.L4Rules[0].ProxyEgressMode, embedded.L4Rules[0].ProxyEgressURL)
	}
	if len(embedded.L4Rules[0].RelayLayers) != 2 || embedded.L4Rules[0].RelayLayers[1][1] != 3 {
		t.Fatalf("embedded L4Rules[0].RelayLayers = %+v", embedded.L4Rules[0].RelayLayers)
	}
	if len(embedded.RelayListeners) != 1 {
		t.Fatalf("embedded RelayListeners len = %d, want 1", len(embedded.RelayListeners))
	}
	if embedded.RelayListeners[0].AgentName != "Local Node" {
		t.Fatalf("embedded RelayListeners[0].AgentName = %q, want Local Node", embedded.RelayListeners[0].AgentName)
	}
	if embedded.RelayListeners[0].TransportMode != "quic" {
		t.Fatalf("embedded RelayListeners[0].TransportMode = %q, want quic", embedded.RelayListeners[0].TransportMode)
	}
	if !embedded.RelayListeners[0].AllowTransportFallback {
		t.Fatalf("embedded RelayListeners[0].AllowTransportFallback = false, want true")
	}
	if embedded.RelayListeners[0].ObfsMode != "early_window_v2" {
		t.Fatalf("embedded RelayListeners[0].ObfsMode = %q, want early_window_v2", embedded.RelayListeners[0].ObfsMode)
	}
}

func TestLocalStateSinkPersistsRuntimeStateToControlPlaneStore(t *testing.T) {
	store := &bridgeStoreStub{}
	sink := NewStateSink(store, "local")
	state := RuntimeState{
		CurrentRevision: 27,
		Status:          "error",
		Metadata: map[string]string{
			"last_sync_error": "apply failed",
		},
	}

	if err := sink.Save(t.Context(), state); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	if !store.saveLocalStateCalled {
		t.Fatal("SaveLocalRuntimeState() was not called")
	}
	if store.savedAgentID != "local" {
		t.Fatalf("SaveLocalRuntimeState() agentID = %q", store.savedAgentID)
	}
	if !reflect.DeepEqual(store.savedState, state) {
		t.Fatalf("SaveLocalRuntimeState() state = %+v", store.savedState)
	}
}

func TestLocalStateSinkPersistsManagedCertificateReportForLocalAgent(t *testing.T) {
	store := &bridgeStoreStub{
		managedCerts: []storage.ManagedCertificateRow{{
			ID:              21,
			Domain:          "sync.example.com",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "local_http01",
			TargetAgentIDs:  `["local"]`,
			Status:          "pending",
			AgentReports:    `{}`,
			ACMEInfo:        `{"Main_Domain":"sync.example.com"}`,
			Usage:           "https",
			CertificateType: "acme",
			Revision:        4,
		}},
	}
	sink := NewStateSink(store, "local")
	state := RuntimeState{
		CurrentRevision:   4,
		LastApplyRevision: 4,
		LastApplyStatus:   "success",
		ManagedCertificateReports: []storage.ManagedCertificateReport{{
			ID:           21,
			Domain:       "SYNC.EXAMPLE.COM",
			Status:       "active",
			LastIssueAt:  "2026-04-11T12:00:00Z",
			MaterialHash: "hash-21",
			ACMEInfo: storage.ManagedCertificateACMEInfo{
				MainDomain: "sync.example.com",
			},
		}},
	}

	if err := sink.Save(t.Context(), state); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	if !store.saveManagedCalled {
		t.Fatal("SaveManagedCertificates() was not called")
	}
	if len(store.managedCerts) != 1 {
		t.Fatalf("managedCerts = %+v", store.managedCerts)
	}

	cert := store.managedCerts[0]
	if cert.Status != "active" || cert.MaterialHash != "hash-21" {
		t.Fatalf("managed cert overlay fields not updated: %+v", cert)
	}
	report := parseAgentReportForTest(t, cert.AgentReports, "local")
	if report.Status != "active" || report.MaterialHash != "hash-21" {
		t.Fatalf("agent report not updated: %+v", report)
	}
}

func TestLocalStateSinkReconcilesLocalHTTP01FallbackForLocalAgent(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		store := &bridgeStoreStub{
			managedCerts: []storage.ManagedCertificateRow{{
				ID:              22,
				Domain:          "fallback.example.com",
				Enabled:         true,
				Scope:           "domain",
				IssuerMode:      "local_http01",
				TargetAgentIDs:  `["local"]`,
				Status:          "pending",
				MaterialHash:    "hash-22",
				AgentReports:    `{}`,
				ACMEInfo:        `{"Main_Domain":"fallback.example.com"}`,
				Usage:           "https",
				CertificateType: "acme",
				Revision:        4,
			}},
			rulesByAgent: map[string][]storage.HTTPRuleRow{
				"local": {{
					ID:          8,
					AgentID:     "local",
					FrontendURL: "https://fallback.example.com",
					Enabled:     true,
					Revision:    4,
				}},
			},
		}
		sink := NewStateSink(store, "local")

		if err := sink.Save(t.Context(), RuntimeState{
			CurrentRevision:   4,
			LastApplyRevision: 4,
			LastApplyStatus:   "success",
		}); err != nil {
			t.Fatalf("Save() error = %v", err)
		}

		cert := store.managedCerts[0]
		if cert.Status != "active" || cert.LastError != "" {
			t.Fatalf("unexpected success fallback cert: %+v", cert)
		}
		report := parseAgentReportForTest(t, cert.AgentReports, "local")
		if report.Status != "active" || report.LastIssueAt == "" {
			t.Fatalf("unexpected success fallback report: %+v", report)
		}
	})

	t.Run("error", func(t *testing.T) {
		store := &bridgeStoreStub{
			managedCerts: []storage.ManagedCertificateRow{{
				ID:              23,
				Domain:          "error.example.com",
				Enabled:         true,
				Scope:           "domain",
				IssuerMode:      "local_http01",
				TargetAgentIDs:  `["local"]`,
				Status:          "pending",
				MaterialHash:    "hash-23",
				AgentReports:    `{}`,
				ACMEInfo:        `{"Main_Domain":"error.example.com"}`,
				Usage:           "https",
				CertificateType: "acme",
				Revision:        4,
			}},
			rulesByAgent: map[string][]storage.HTTPRuleRow{
				"local": {{
					ID:          9,
					AgentID:     "local",
					FrontendURL: "https://error.example.com",
					Enabled:     true,
					Revision:    4,
				}},
			},
		}
		sink := NewStateSink(store, "local")

		if err := sink.Save(t.Context(), RuntimeState{
			CurrentRevision:   4,
			LastApplyRevision: 4,
			LastApplyStatus:   "error",
			LastApplyMessage:  "apply failed",
		}); err != nil {
			t.Fatalf("Save() error = %v", err)
		}

		cert := store.managedCerts[0]
		if cert.Status != "error" || cert.LastError != "apply failed" {
			t.Fatalf("unexpected error fallback cert: %+v", cert)
		}
		report := parseAgentReportForTest(t, cert.AgentReports, "local")
		if report.Status != "error" || report.LastError != "apply failed" {
			t.Fatalf("unexpected error fallback report: %+v", report)
		}
	})
}

func TestMergeRuntimeStateWithSyncRequestPreservesAuthoritativeMetadataApplyOutcome(t *testing.T) {
	state := RuntimeState{
		Metadata: map[string]string{
			"last_sync_error":     "apply failed",
			"last_apply_revision": "9",
			"last_apply_status":   "error",
			"last_apply_message":  "apply failed",
		},
	}
	request := SyncRequest{
		LastApplyRevision: 2,
		LastApplyStatus:   "success",
		LastApplyMessage:  "",
		ManagedCertificateReports: []storage.ManagedCertificateReport{{
			ID:     21,
			Domain: "sync.example.com",
			Status: "active",
		}},
	}

	merged := mergeRuntimeStateWithSyncRequest(state, request)

	if merged.LastApplyRevision != 0 || merged.LastApplyStatus != "" || merged.LastApplyMessage != "" {
		t.Fatalf("merge unexpectedly overwrote authoritative metadata apply fields: %+v", merged)
	}
	if len(merged.ManagedCertificateReports) != 1 || merged.ManagedCertificateReports[0].ID != 21 {
		t.Fatalf("merge did not preserve bridged managed certificate reports: %+v", merged.ManagedCertificateReports)
	}
}

func TestMergeRuntimeStateWithSyncRequestPersistsStatsMetadata(t *testing.T) {
	state := RuntimeState{}
	request := SyncRequest{
		Stats: map[string]any{
			"traffic": map[string]any{
				"total": map[string]any{
					"rx_bytes": float64(123),
					"tx_bytes": float64(456),
				},
			},
		},
	}

	merged := mergeRuntimeStateWithSyncRequest(state, request)

	if merged.Metadata["stats"] == "" {
		t.Fatalf("merge did not persist stats metadata: %+v", merged.Metadata)
	}
}

func TestFromEmbeddedSyncRequestCopiesStats(t *testing.T) {
	request := goagentembedded.SyncRequest{
		Stats: map[string]any{
			"traffic": map[string]any{
				"total": map[string]any{
					"rx_bytes": float64(123),
					"tx_bytes": float64(456),
				},
			},
		},
	}

	converted := fromEmbeddedSyncRequest(request)

	if converted.Stats["traffic"] == nil {
		t.Fatalf("fromEmbeddedSyncRequest() Stats = %+v", converted.Stats)
	}
}

func TestLocalStateSinkMetadataErrorWinsOverStaleBridgeApplyStatus(t *testing.T) {
	store := &bridgeStoreStub{
		managedCerts: []storage.ManagedCertificateRow{{
			ID:              31,
			Domain:          "stale-bridge.example.com",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "local_http01",
			TargetAgentIDs:  `["local"]`,
			Status:          "pending",
			AgentReports:    `{}`,
			ACMEInfo:        `{"Main_Domain":"stale-bridge.example.com"}`,
			Usage:           "https",
			CertificateType: "acme",
			Revision:        4,
		}},
		rulesByAgent: map[string][]storage.HTTPRuleRow{
			"local": {{
				ID:          31,
				AgentID:     "local",
				FrontendURL: "https://stale-bridge.example.com",
				Enabled:     true,
				Revision:    4,
			}},
		},
	}

	bridge := newSyncRequestBridge()
	bridge.Store(SyncRequest{
		LastApplyRevision: 4,
		LastApplyStatus:   "success",
		LastApplyMessage:  "",
	})
	sink := newStateSinkWithBridge(store, "local", bridge)

	err := sink.Save(t.Context(), RuntimeState{
		CurrentRevision: 4,
		Status:          "active",
		Metadata: map[string]string{
			"last_sync_error":     "apply failed",
			"last_apply_revision": "4",
			"last_apply_status":   "error",
			"last_apply_message":  "apply failed",
		},
	})
	if err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	cert := store.managedCerts[0]
	if cert.Status != "error" || cert.LastError != "apply failed" {
		t.Fatalf("stale bridge apply status incorrectly overrode metadata error: %+v", cert)
	}
	report := parseAgentReportForTest(t, cert.AgentReports, "local")
	if report.Status != "error" || report.LastError != "apply failed" {
		t.Fatalf("unexpected reconciled report from metadata error: %+v", report)
	}
}

type managedCertificateAgentReportForTest struct {
	Status       string `json:"status"`
	LastIssueAt  string `json:"last_issue_at"`
	LastError    string `json:"last_error"`
	MaterialHash string `json:"material_hash"`
}

func parseAgentReportForTest(t *testing.T, raw string, agentID string) managedCertificateAgentReportForTest {
	t.Helper()

	var reports map[string]managedCertificateAgentReportForTest
	if err := json.Unmarshal([]byte(raw), &reports); err != nil {
		t.Fatalf("json.Unmarshal(agent_reports) error = %v", err)
	}
	report, ok := reports[agentID]
	if !ok {
		t.Fatalf("missing report for %q in %s", agentID, raw)
	}
	return report
}

func mustDecodeEmbeddedSyncRequest(t *testing.T, raw string) goagentembedded.SyncRequest {
	t.Helper()

	var request goagentembedded.SyncRequest
	if err := json.Unmarshal([]byte(raw), &request); err != nil {
		t.Fatalf("json.Unmarshal(sync request) error = %v", err)
	}
	return request
}
