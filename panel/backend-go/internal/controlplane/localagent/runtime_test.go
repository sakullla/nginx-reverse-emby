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
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/service"
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

type localTrafficSummaryStub struct {
	summary service.TrafficSummary
	err     error
}

func (s localTrafficSummaryStub) IngestHeartbeat(context.Context, string, service.AgentStats) error {
	return nil
}

func (s localTrafficSummaryStub) Summary(context.Context, string) (service.TrafficSummary, error) {
	if s.err != nil {
		return service.TrafficSummary{}, s.err
	}
	return s.summary, nil
}

func (s localTrafficSummaryStub) BlockState(context.Context, string) (bool, string, error) {
	if s.err != nil {
		return false, "", s.err
	}
	if !s.summary.Blocked {
		return false, "", nil
	}
	return true, s.summary.BlockReason, nil
}

type localTrafficIngestStub struct {
	ingestAgentID       string
	ingestStats         service.AgentStats
	blockStateSawIngest bool
}

func (s *localTrafficIngestStub) IngestHeartbeat(_ context.Context, agentID string, stats service.AgentStats) error {
	s.ingestAgentID = agentID
	s.ingestStats = stats
	return nil
}

func (s *localTrafficIngestStub) Summary(context.Context, string) (service.TrafficSummary, error) {
	return service.TrafficSummary{}, nil
}

func (s *localTrafficIngestStub) BlockState(context.Context, string) (bool, string, error) {
	s.blockStateSawIngest = s.ingestStats != nil
	return false, "", nil
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
	cfg.LocalAgentTrafficStatsEnabled = false

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
		if cfg.TrafficStatsEnabled {
			t.Fatal("expected TrafficStatsEnabled to propagate")
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

func TestLocalSyncSourceAddsTrafficAgentConfig(t *testing.T) {
	store := &bridgeStoreStub{
		snapshot: Snapshot{
			DesiredVersion: "1.2.3",
			Revision:       15,
		},
	}
	source := NewSyncSource(store, "local")
	source.SetTrafficService(true, localTrafficSummaryStub{summary: service.TrafficSummary{
		Blocked:     true,
		BlockReason: "monthly quota exceeded",
	}})

	got, err := source.Sync(t.Context(), SyncRequest{CurrentRevision: 14})
	if err != nil {
		t.Fatalf("Sync() error = %v", err)
	}

	if got.AgentConfig.TrafficStatsEnabled == nil || !*got.AgentConfig.TrafficStatsEnabled {
		t.Fatalf("TrafficStatsEnabled = %v, want true", got.AgentConfig.TrafficStatsEnabled)
	}
	if !got.AgentConfig.TrafficBlocked || got.AgentConfig.TrafficBlockReason != "monthly quota exceeded" {
		t.Fatalf("AgentConfig traffic block = %+v", got.AgentConfig)
	}
}

func TestLocalSyncSourceIngestsTrafficBeforeBlockState(t *testing.T) {
	store := &bridgeStoreStub{
		snapshot: Snapshot{
			DesiredVersion: "1.2.3",
			Revision:       15,
		},
	}
	trafficSvc := &localTrafficIngestStub{}
	source := NewSyncSource(store, "local")
	source.SetTrafficService(true, trafficSvc)
	stats := map[string]any{
		"traffic": map[string]any{
			"total": map[string]any{
				"rx_bytes": float64(123),
				"tx_bytes": float64(456),
			},
		},
	}

	if _, err := source.Sync(t.Context(), SyncRequest{CurrentRevision: 14, StatsPresent: true, Stats: stats}); err != nil {
		t.Fatalf("Sync() error = %v", err)
	}

	if trafficSvc.ingestAgentID != "local" {
		t.Fatalf("IngestHeartbeat() agentID = %q, want local", trafficSvc.ingestAgentID)
	}
	if trafficSvc.ingestStats == nil || trafficSvc.ingestStats["traffic"] == nil {
		t.Fatalf("IngestHeartbeat() stats = %+v", trafficSvc.ingestStats)
	}
	if !trafficSvc.blockStateSawIngest {
		t.Fatal("BlockState() ran before local traffic stats were ingested")
	}
}

func TestToEmbeddedSnapshotPreservesRelayTransportFields(t *testing.T) {
	trafficStatsEnabled := false
	l4WireGuardProfileID := 17
	relayWireGuardProfileID := 18
	snapshot := Snapshot{
		Revision: 15,
		AgentConfig: storage.AgentConfig{
			TrafficStatsEnabled:  &trafficStatsEnabled,
			TrafficBlocked:       true,
			TrafficBlockReason:   "monthly quota exceeded",
			TrafficStatsInterval: "30s",
		},
		Rules: []storage.HTTPRule{{
			ID:          7,
			FrontendURL: "https://media.example.test",
			Backends:    []storage.HTTPBackend{{URL: "http://127.0.0.1:8096"}},
			RelayLayers: [][]int{{1, 2}, {3}},
		}},
		L4Rules: []storage.L4Rule{{
			ID:                  11,
			Name:                "tcp-game",
			Protocol:            "tcp",
			ListenHost:          "0.0.0.0",
			ListenPort:          19000,
			ListenMode:          "proxy",
			WireGuardProfileID:  &l4WireGuardProfileID,
			WireGuardListenHost: "10.60.0.1",
			ProxyEntryAuth:      storage.L4ProxyEntryAuth{Enabled: true, Username: "client", Password: "secret"},
			ProxyEgressMode:     "wireguard",
			Backends: []storage.L4Backend{{
				Host: "relay-echo-test",
				Port: 18081,
			}},
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
			WireGuardProfileID:     &relayWireGuardProfileID,
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

	if embedded.AgentConfig.TrafficStatsEnabled == nil || *embedded.AgentConfig.TrafficStatsEnabled {
		t.Fatalf("embedded AgentConfig.TrafficStatsEnabled = %v, want false", embedded.AgentConfig.TrafficStatsEnabled)
	}
	if !embedded.AgentConfig.TrafficBlocked || embedded.AgentConfig.TrafficBlockReason != "monthly quota exceeded" || embedded.AgentConfig.TrafficStatsInterval != "30s" {
		t.Fatalf("embedded AgentConfig = %+v", embedded.AgentConfig)
	}

	if len(embedded.Rules) != 1 || embedded.Rules[0].ID != 7 {
		t.Fatalf("embedded HTTP rule IDs = %+v", embedded.Rules)
	}
	if len(embedded.Rules[0].RelayLayers) != 2 || embedded.Rules[0].RelayLayers[1][0] != 3 {
		t.Fatalf("embedded HTTP RelayLayers = %+v", embedded.Rules[0].RelayLayers)
	}
	if embedded.Rules[0].BackendURL != "" || len(embedded.Rules[0].RelayChain) != 0 {
		t.Fatalf("embedded HTTP legacy fields = backend_url=%q relay_chain=%+v", embedded.Rules[0].BackendURL, embedded.Rules[0].RelayChain)
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
	if embedded.L4Rules[0].ProxyEgressMode != "wireguard" || embedded.L4Rules[0].ProxyEgressURL != "" {
		t.Fatalf("embedded L4Rules[0] proxy egress = mode %q url %q", embedded.L4Rules[0].ProxyEgressMode, embedded.L4Rules[0].ProxyEgressURL)
	}
	if embedded.L4Rules[0].WireGuardProfileID == nil || *embedded.L4Rules[0].WireGuardProfileID != l4WireGuardProfileID || embedded.L4Rules[0].WireGuardListenHost != "10.60.0.1" {
		t.Fatalf("embedded L4Rules[0] WireGuard fields = profile %v listen_host %q", embedded.L4Rules[0].WireGuardProfileID, embedded.L4Rules[0].WireGuardListenHost)
	}
	if len(embedded.L4Rules[0].RelayLayers) != 2 || embedded.L4Rules[0].RelayLayers[1][1] != 3 {
		t.Fatalf("embedded L4Rules[0].RelayLayers = %+v", embedded.L4Rules[0].RelayLayers)
	}
	if embedded.L4Rules[0].UpstreamHost != "" || embedded.L4Rules[0].UpstreamPort != 0 || len(embedded.L4Rules[0].RelayChain) != 0 {
		t.Fatalf("embedded L4 legacy fields = upstream=%q:%d relay_chain=%+v", embedded.L4Rules[0].UpstreamHost, embedded.L4Rules[0].UpstreamPort, embedded.L4Rules[0].RelayChain)
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
	if embedded.RelayListeners[0].WireGuardProfileID == nil || *embedded.RelayListeners[0].WireGuardProfileID != relayWireGuardProfileID {
		t.Fatalf("embedded RelayListeners[0].WireGuardProfileID = %v", embedded.RelayListeners[0].WireGuardProfileID)
	}
	if !embedded.RelayListeners[0].AllowTransportFallback {
		t.Fatalf("embedded RelayListeners[0].AllowTransportFallback = false, want true")
	}
	if embedded.RelayListeners[0].ObfsMode != "early_window_v2" {
		t.Fatalf("embedded RelayListeners[0].ObfsMode = %q, want early_window_v2", embedded.RelayListeners[0].ObfsMode)
	}
}

func TestToEmbeddedSnapshotPreservesWireGuardProfilesWithRawSecrets(t *testing.T) {
	snapshot := Snapshot{
		WireGuardProfiles: []storage.WireGuardProfile{{
			ID:         17,
			AgentID:    "local",
			Name:       "wg-egress",
			Mode:       "generic_wireguard",
			PrivateKey: "raw-private-key",
			ListenPort: 51820,
			Addresses:  []string{"10.50.0.2/32", "fd50::2/128"},
			Peers: []storage.WireGuardPeer{{
				Name:                       "hub",
				PublicKey:                  "peer-public-key",
				PresharedKey:               "raw-preshared-key",
				Endpoint:                   "hub.example.com:51820",
				AllowedIPs:                 []string{"0.0.0.0/0", "::/0"},
				PersistentKeepaliveSeconds: 25,
			}},
			DNS:      []string{"1.1.1.1"},
			MTU:      1420,
			Enabled:  true,
			Tags:     []string{"relay"},
			Revision: 44,
		}},
	}

	embedded := toEmbeddedSnapshot(snapshot)

	if len(embedded.WireGuardProfiles) != 1 {
		t.Fatalf("embedded WireGuardProfiles len = %d, want 1", len(embedded.WireGuardProfiles))
	}
	profile := embedded.WireGuardProfiles[0]
	if profile.ID != 17 || profile.AgentID != "local" || profile.Name != "wg-egress" || profile.Mode != "generic_wireguard" {
		t.Fatalf("embedded WireGuard profile identity = %+v", profile)
	}
	if profile.PrivateKey != "raw-private-key" {
		t.Fatalf("embedded WireGuard profile PrivateKey = %q, want raw private key", profile.PrivateKey)
	}
	if profile.ListenPort != 51820 || profile.MTU != 1420 || !profile.Enabled || profile.Revision != 44 {
		t.Fatalf("embedded WireGuard profile scalar fields = %+v", profile)
	}
	if !reflect.DeepEqual(profile.Addresses, []string{"10.50.0.2/32", "fd50::2/128"}) {
		t.Fatalf("embedded WireGuard profile Addresses = %+v", profile.Addresses)
	}
	if !reflect.DeepEqual(profile.DNS, []string{"1.1.1.1"}) || !reflect.DeepEqual(profile.Tags, []string{"relay"}) {
		t.Fatalf("embedded WireGuard profile DNS/Tags = dns %+v tags %+v", profile.DNS, profile.Tags)
	}
	if len(profile.Peers) != 1 {
		t.Fatalf("embedded WireGuard profile Peers len = %d, want 1", len(profile.Peers))
	}
	peer := profile.Peers[0]
	if peer.Name != "hub" || peer.PublicKey != "peer-public-key" || peer.PresharedKey != "raw-preshared-key" {
		t.Fatalf("embedded WireGuard peer secrets = %+v", peer)
	}
	if peer.Endpoint != "hub.example.com:51820" || peer.PersistentKeepaliveSeconds != 25 {
		t.Fatalf("embedded WireGuard peer endpoint/keepalive = %+v", peer)
	}
	if !reflect.DeepEqual(peer.AllowedIPs, []string{"0.0.0.0/0", "::/0"}) {
		t.Fatalf("embedded WireGuard peer AllowedIPs = %+v", peer.AllowedIPs)
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
		StatsPresent: true,
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

func TestMergeRuntimeStateWithSyncRequestPreservesStatsMetadataWhenStatsOmitted(t *testing.T) {
	const existingStats = `{"traffic":{"total":{"rx_bytes":123,"tx_bytes":456}}}`
	state := RuntimeState{
		Metadata: map[string]string{
			"stats":             existingStats,
			"last_apply_status": "success",
		},
	}

	merged := mergeRuntimeStateWithSyncRequest(state, SyncRequest{})

	if merged.Metadata["stats"] != existingStats {
		t.Fatalf("merge did not preserve existing stats metadata: %+v", merged.Metadata)
	}
	if merged.Metadata["last_apply_status"] != "success" {
		t.Fatalf("merge removed unrelated metadata: %+v", merged.Metadata)
	}
}

func TestMergeRuntimeStateWithSyncRequestPreservesStatsMetadataWhenStatsMapEmptyButNotPresent(t *testing.T) {
	const existingStats = `{"traffic":{"total":{"rx_bytes":123,"tx_bytes":456}}}`
	state := RuntimeState{
		Metadata: map[string]string{
			"stats":             existingStats,
			"last_apply_status": "success",
		},
	}

	merged := mergeRuntimeStateWithSyncRequest(state, SyncRequest{
		StatsPresent: false,
		Stats:        map[string]any{},
	})

	if merged.Metadata["stats"] != existingStats {
		t.Fatalf("merge cleared existing stats metadata unexpectedly: %+v", merged.Metadata)
	}
}

func TestMergeRuntimeStateWithSyncRequestClearsStatsMetadataWhenStatsExplicitlyEmpty(t *testing.T) {
	state := RuntimeState{
		Metadata: map[string]string{
			"stats":             `{"traffic":{"total":{"rx_bytes":123,"tx_bytes":456}}}`,
			"last_apply_status": "success",
		},
	}

	merged := mergeRuntimeStateWithSyncRequest(state, SyncRequest{StatsPresent: true, Stats: map[string]any{}})

	if _, ok := merged.Metadata["stats"]; ok {
		t.Fatalf("merge retained stale stats metadata: %+v", merged.Metadata)
	}
	if merged.Metadata["last_apply_status"] != "success" {
		t.Fatalf("merge removed unrelated metadata: %+v", merged.Metadata)
	}
}

func TestFromEmbeddedSyncRequestPreservesExplicitEmptyStats(t *testing.T) {
	request := goagentembedded.SyncRequest{
		Stats:        map[string]any{},
		StatsPresent: true,
	}

	converted := fromEmbeddedSyncRequest(request)

	if converted.Stats == nil {
		t.Fatal("fromEmbeddedSyncRequest() Stats = nil, want explicit empty map")
	}
	if len(converted.Stats) != 0 {
		t.Fatalf("fromEmbeddedSyncRequest() Stats = %+v, want empty map", converted.Stats)
	}
}

func TestSyncRequestBridgePreservesExplicitEmptyStats(t *testing.T) {
	bridge := newSyncRequestBridge()
	bridge.Store(SyncRequest{StatsPresent: true, Stats: map[string]any{}})

	loaded := bridge.Load()

	if !loaded.StatsPresent {
		t.Fatal("bridge.Load() StatsPresent = false, want true")
	}
	if loaded.Stats == nil {
		t.Fatal("bridge.Load() Stats = nil, want explicit empty map")
	}
	if len(loaded.Stats) != 0 {
		t.Fatalf("bridge.Load() Stats = %+v, want empty map", loaded.Stats)
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
	if !converted.StatsPresent {
		t.Fatal("fromEmbeddedSyncRequest() StatsPresent = false, want true for non-empty stats")
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
