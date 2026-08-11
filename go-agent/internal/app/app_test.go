package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/control"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/core"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/generation"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/hotrestart"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/ingress"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	agentmodule "github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
	modulediagnostics "github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/diagnostics"
	modulerelay "github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/relay"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/observability"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/plugins/policy"
	pluginrpc "github.com/sakullla/nginx-reverse-emby/go-agent/internal/plugins/rpc"
	pluginwasm "github.com/sakullla/nginx-reverse-emby/go-agent/internal/plugins/wasm"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	stdruntime "runtime"
	"strings"
	"testing"
	"time"
	"unsafe"
)

func TestPolicyWASMObserverRecordsStateBudgetDimension(t *testing.T) {
	var logOutput bytes.Buffer
	recorder := observability.NewRecorder(slog.New(slog.NewJSONHandler(&logOutput, nil)))
	observer := newPolicyWASMObserverWith(recorder)
	observer.ObserveWASM(pluginwasm.Event{
		Generation: "generation-7",
		Operation:  "host.nre_host_state_put",
		Code:       pluginwasm.ErrorHost,
		Dimension:  "state",
	})

	logged := logOutput.String()
	for _, want := range []string{`"event":"nre_agent_policy_budget"`, `"outcome":"exhausted"`, `"generation_id":"generation-7"`, `"reason":"dimension=state:host_failure:host.nre_host_state_put"`} {
		if !strings.Contains(logged, want) {
			t.Fatalf("observer log %q does not contain %q", logged, want)
		}
	}
	var metrics bytes.Buffer
	if err := recorder.WritePrometheus(&metrics); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(metrics.String(), `nre_agent_policy_budget_total{outcome="exhausted"} 1`) {
		t.Fatalf("observer metrics = %q", metrics.String())
	}
}

func TestNewBuildsControlPlaneWiring(t *testing.T) {
	cfg := Config{
		AgentID:        "agent",
		AgentName:      "agent",
		MasterURL:      "https://master.example.com",
		AgentToken:     "token",
		CurrentVersion: "0.1.0",
		DataDir:        t.TempDir(),
		HTTPTransport: model.HTTPTransportConfig{
			TLSHandshakeTimeout:   22 * time.Second,
			ResponseHeaderTimeout: 23 * time.Second,
		},
	}

	app, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() { _ = app.Close() })

	if _, ok := app.store.(*core.Filesystem); !ok {
		t.Fatalf("store = %T, want filesystem store", app.store)
	}
	if app.syncClient == nil {
		t.Fatal("syncClient = nil")
	}
	if app.rpcHost == nil || !app.rpcHost.SecretRedemptionReady() {
		t.Fatal("remote RPC host plugin secret redeemer is not wired")
	}
	if app.rpcGeneration == nil || !app.rpcGeneration.RuntimeLogFenceRetirementReady() {
		t.Fatal("remote RPC generation durable log fence retirement is not wired")
	}
	if app.pkiStore == nil || app.pkiStore.Root() != filepath.Join(cfg.DataDir, "pki") {
		t.Fatalf("pkiStore root = %q, want %q", app.pkiStore.Root(), filepath.Join(cfg.DataDir, "pki"))
	}
	if app.runtime == nil {
		t.Fatal("runtime = nil")
	}
	if app.capabilityAudit == nil {
		t.Fatal("durable plugin capability audit journal is not wired")
	}
	if _, err := os.Stat(filepath.Join(cfg.DataDir, "audit", "plugin-capabilities.jsonl")); err != nil {
		t.Fatalf("capability audit journal: %v", err)
	}
	if app.taskClient == nil {
		t.Fatal("taskClient = nil")
	}
	transport := extractPrivateTransport(t, app.taskClient)
	if transport.ResponseHeaderTimeout != 23*time.Second {
		t.Fatalf("task ResponseHeaderTimeout = %v", transport.ResponseHeaderTimeout)
	}
	if transport.TLSHandshakeTimeout != 22*time.Second {
		t.Fatalf("task TLSHandshakeTimeout = %v", transport.TLSHandshakeTimeout)
	}
}

func TestNewAppWiresGenerationFencedPluginSecretRedeemer(t *testing.T) {
	client := appSecretRedeemingSyncClient{}
	application := newAppWithAllDeps(Config{DataDir: t.TempDir()}, core.NewInMemory(), client, nil, nil)
	t.Cleanup(func() { _ = application.Close() })
	if application.rpcHost == nil || !application.rpcHost.SecretRedemptionReady() {
		t.Fatal("RPC host did not receive the sync client's plugin secret redeemer")
	}
	if application.rpcProcesses == nil || !application.rpcProcesses.RuntimeLogSinkReady() {
		t.Fatal("RPC process supervisor did not receive the durable plugin log sink")
	}
}

func TestNewEmbeddedWiresGenerationFencedPluginSecretRedeemer(t *testing.T) {
	application, err := NewEmbedded(Config{AgentID: "local", AgentName: "local", DataDir: t.TempDir()}, core.NewInMemory(), appSecretRedeemingSyncClient{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = application.Close() })
	if application.PluginRPCHost() == nil || !application.PluginRPCHost().SecretRedemptionReady() {
		t.Fatal("embedded RPC host did not receive the local plugin secret redeemer")
	}
	if application.rpcProcesses == nil || !application.rpcProcesses.RuntimeLogSinkReady() {
		t.Fatal("embedded RPC process supervisor did not receive the durable plugin log sink")
	}
	if application.rpcGeneration == nil || !application.rpcGeneration.RuntimeLogFenceRetirementReady() {
		t.Fatal("embedded RPC generation module did not receive durable plugin log fence retirement")
	}
}

type appSecretRedeemingSyncClient struct{}

func (appSecretRedeemingSyncClient) Sync(context.Context, SyncRequest) (Snapshot, error) {
	return Snapshot{}, nil
}

func (appSecretRedeemingSyncClient) RedeemPluginSecrets(context.Context, model.PluginSecretRedemptionRequest) ([]model.PluginRedeemedSecret, error) {
	return nil, nil
}

func TestNewRegistersConfiguredModules(t *testing.T) {
	app, err := New(Config{
		AgentID:        "agent",
		AgentName:      "agent",
		MasterURL:      "https://master.example.com",
		AgentToken:     "token",
		CurrentVersion: "0.1.0",
		DataDir:        t.TempDir(),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() { _ = app.Close() })

	want := []string{"certs", "diagnostics", "egress", "plugin-rpc", "plugin-policy", "http", "relay", "l4", "traffic", "ddns"}
	if got := app.ModuleNames(); !reflect.DeepEqual(got, want) {
		t.Fatalf("ModuleNames() = %v, want %v", got, want)
	}
	if app.policyWASM == nil {
		t.Fatal("configured app did not retain the process-scoped policy WASM runtime")
	}
}

func TestDDNSModuleConfigFromAppConfig(t *testing.T) {
	got := ddnsModuleConfigFromAppConfig(Config{DDNS: model.DDNSRuntimeConfig{
		IPv4PublicAPIURL: "https://v4.example.test/ip",
		IPv6PublicAPIURL: "https://v6.example.test/ip",
		IPProbeInterval:  30 * time.Second,
	}})

	if got.IPv4PublicAPIURL != "https://v4.example.test/ip" || got.IPv6PublicAPIURL != "https://v6.example.test/ip" {
		t.Fatalf("DDNS public API URLs = %q / %q", got.IPv4PublicAPIURL, got.IPv6PublicAPIURL)
	}
	if got.MinExtractInterval != 30*time.Second {
		t.Fatalf("DDNS minimum extract interval = %v, want 30s", got.MinExtractInterval)
	}
}

func TestConfiguredRuntimeUsesCompatibleSoleViewGenerationPath(t *testing.T) {
	configured, err := newConfiguredModules(Config{
		AgentID:   "agent",
		AgentName: "agent",
		DataDir:   t.TempDir(),
	})
	if err != nil {
		t.Fatalf("newConfiguredModules() error = %v", err)
	}
	if err := configured.registry.ValidateGenerationCompatibility(); err != nil {
		t.Fatalf("ValidateGenerationCompatibility() error = %v", err)
	}
	app := &App{}
	app.setConfiguredModules(configured)
	t.Cleanup(func() { _ = app.Close() })
	t.Cleanup(func() { _ = app.Close() })
	if app.runtime == nil {
		t.Fatal("configured runtime is nil")
	}

	next := model.Snapshot{Revision: 1, DesiredVersion: "v1"}
	if err := app.runtime.Apply(context.Background(), model.Snapshot{}, next); err != nil {
		t.Fatalf("runtime.Apply() error = %v", err)
	}
	active := configured.registry.ActiveGeneration()
	if active == nil || active.Revision() != 1 {
		t.Fatalf("active generation = %+v, want revision 1", active)
	}
	if app.diagnosticModule.Handler() == nil {
		t.Fatal("diagnostics direct consumer did not resolve the active generation")
	}
	if provider, ok := active.Resolve(agentmodule.ProviderTrafficSink); !ok || provider == nil {
		t.Fatal("traffic provider is missing from active generation")
	}
	if provider, ok := active.Resolve(agentmodule.ProviderRef("certificates.reporter")); !ok || provider == nil {
		t.Fatal("certificate reporter is missing from active generation")
	}
}

func TestConfiguredRuntimeContinuesWithoutUnavailablePolicyCompiler(t *testing.T) {
	configured, err := newConfiguredModulesWithPolicyRuntime(
		Config{AgentID: "agent", AgentName: "agent", DataDir: t.TempDir()},
		func(context.Context, pluginwasm.RuntimeOptions) (*pluginwasm.Runtime, error) {
			return nil, &pluginwasm.RuntimeError{Code: pluginwasm.ErrorUnavailable, Operation: "compiler_config"}
		},
	)
	if err != nil {
		t.Fatalf("newConfiguredModulesWithPolicyRuntime() error = %v", err)
	}
	t.Cleanup(func() {
		if configured.generations != nil {
			_ = configured.generations.Close(context.Background())
		}
		if configured.registry != nil {
			_ = configured.registry.StopAll(context.Background())
		}
		if configured.capabilityAudit != nil {
			_ = configured.capabilityAudit.Close()
		}
	})
	if configured.policyWASM != nil {
		t.Fatal("unavailable compiler retained a policy runtime")
	}
	if !containsString(configured.registry.Names(), "plugin-policy") {
		t.Fatalf("unavailable compiler omitted policy validation module: %v", configured.registry.Names())
	}
	for _, capability := range configured.registry.Capabilities(model.Snapshot{}) {
		if capability.Name == policy.ExtensionHTTP || capability.Name == policy.ExtensionL4 {
			t.Fatalf("unavailable policy execution capability was advertised: %+v", capability)
		}
	}
	if len(configured.registry.Names()) == 0 {
		t.Fatal("unrelated core modules were not registered")
	}

	app := &App{}
	app.setConfiguredModules(configured)
	base := model.Snapshot{Revision: 1, DesiredVersion: "v1", PluginPolicies: []model.PluginPolicy{}}
	if err := app.runtime.Apply(t.Context(), model.Snapshot{}, base); err != nil {
		t.Fatalf("policy-free revision failed with unavailable compiler: %v", err)
	}
	policySnapshot := base
	policySnapshot.Revision = 2
	policySnapshot.PluginPolicies = []model.PluginPolicy{appTestPolicy("required-policy")}
	policySnapshot.Rules = []model.HTTPRule{{ID: 1, Enabled: true, PolicyRef: &model.PolicyRef{ID: "required-policy"}}}
	if err := app.runtime.Apply(t.Context(), base, policySnapshot); err == nil || !strings.Contains(err.Error(), "policy execution runtime is unavailable") {
		t.Fatalf("policy-bearing revision error=%v, want unavailable prepare failure", err)
	}
	active := configured.registry.ActiveGeneration()
	if active == nil || active.Revision() != base.Revision {
		t.Fatalf("active generation=%+v, want last-known-good revision %d", active, base.Revision)
	}
}

func appTestPolicy(id string) model.PluginPolicy {
	return model.PluginPolicy{ID: id, Revision: 1, Stages: []model.PolicyStage{{
		Kind: model.PolicyKindIP, PolicyID: id + "-ip", PluginID: "official.ip", PluginVersion: "1.0.0",
		InstanceID: id + "-instance", PackageDigest: "package-digest", ArtifactPath: "verified/policy.wasm",
		ArtifactDigest: "artifact-digest", SignatureVerified: true, SignerKeyID: "official-release", SignerFingerprint: "signer-fingerprint",
		ABI: model.PolicyABIV1, ExtensionPoints: []string{policy.ExtensionHTTP, policy.ExtensionL4},
		DeclaredScopes: []string{"policy.read"}, GrantedScopes: []string{"policy.read"}, ResourceGroupID: "group-a",
		ResourceBudget: model.PolicyResourceBudget{TimeoutMS: 2, MemoryBytes: 1 << 20, Concurrency: 2, InputBytes: 4096, OutputBytes: 1024},
		FailurePolicy:  model.PolicyFailurePolicy{OnError: "fail-closed", OnBudget: "fail-closed", Restart: "never", CoreFallback: "preserve"},
	}}}
}

func TestConfiguredRuntimeInjectsProcessPacketRegistry(t *testing.T) {
	configured, err := newConfiguredModules(Config{
		AgentID:   "agent",
		AgentName: "agent",
		DataDir:   t.TempDir(),
	})
	if err != nil {
		t.Fatalf("newConfiguredModules() error = %v", err)
	}
	set, err := configured.processPackets.Import(nil, nil)
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	defer set.Close()
	app := &App{}
	app.setConfiguredModules(configured)
	t.Cleanup(func() { _ = app.Close() })

	next := Snapshot{Revision: 1, DesiredVersion: "v1", L4Rules: []model.L4Rule{{
		ID: 91, AgentID: "agent", Protocol: "udp", ListenHost: "127.0.0.1", ListenPort: 39001,
		Backends: []model.L4Backend{{Host: "127.0.0.1", Port: 9}}, Enabled: true,
	}}}
	err = app.runtime.Apply(t.Context(), Snapshot{}, next)
	if err == nil || !strings.Contains(err.Error(), "inherited packet descriptor") || !strings.Contains(err.Error(), "l4:") {
		t.Fatalf("runtime.Apply() error = %v, want missing L4 process packet descriptor", err)
	}
}

func TestConfigureProcessPacketRegistryInjectsSameRegistryIntoAllProductionConsumers(t *testing.T) {
	registry := ingress.NewProcessPacketRegistry()
	consumers := []*recordingPacketRegistryConsumer{{}, {}, {}, {}}
	configureProcessPacketRegistry(registry, consumers[0], consumers[1], consumers[2], consumers[3])
	for index, consumer := range consumers {
		if consumer.registry != registry || consumer.calls != 1 {
			t.Fatalf("consumer %d registry=%p calls=%d, want registry=%p calls=1", index, consumer.registry, consumer.calls, registry)
		}
	}
}

type recordingPacketRegistryConsumer struct {
	registry *ingress.ProcessPacketRegistry
	calls    int
}

func (c *recordingPacketRegistryConsumer) SetProcessPacketRegistry(registry *ingress.ProcessPacketRegistry) {
	c.registry = registry
	c.calls++
}

func TestDiagnoseUsesDiagnosticModuleHandler(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer backend.Close()

	diagnosticModule := modulediagnostics.NewModule()
	if err := diagnosticModule.Apply(context.Background(), agentmodule.ApplyRequest{
		Next: Snapshot{
			Rules: []model.HTTPRule{{
				ID:          77,
				FrontendURL: "http://frontend.example.test",
				Backends:    []model.HTTPBackend{{URL: backend.URL}},
			}},
		},
	}); err != nil {
		t.Fatalf("diagnostic module Apply() error = %v", err)
	}
	app := &App{diagnosticModule: diagnosticModule}

	got, err := app.Diagnose(context.Background(), control.TaskTypeDiagnoseHTTPRule, 77)
	if err != nil {
		t.Fatalf("Diagnose() error = %v", err)
	}
	if got["kind"] != "http" || got["rule_id"] != 77 {
		t.Fatalf("Diagnose() = %+v, want http report for rule 77", got)
	}
}

func TestDiagnoseSnapshotUsesRegistryDiagnosticSources(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	defer backend.Close()

	cache := model.NewCache(model.BackendCacheConfig{})
	cache.MarkFailure(backend.Listener.Addr().String())

	registry := agentmodule.NewRegistry()
	mustRegisterAppModule(t, registry, appProviderModule{
		name:     "http-diagnostics-source",
		provides: agentmodule.ProviderDiagnosticsHTTPSource,
		provider: appDiagnosticSource{cache: cache},
	})
	diagnosticModule := modulediagnostics.NewModule()
	mustRegisterAppModule(t, registry, diagnosticModule)
	app := &App{diagnosticModule: diagnosticModule, moduleRegistry: registry}
	snapshot := Snapshot{
		Rules: []model.HTTPRule{{
			ID:          89,
			FrontendURL: "http://frontend.example.test",
			Backends:    []model.HTTPBackend{{URL: backend.URL}},
		}},
	}

	_, err := app.DiagnoseSnapshot(context.Background(), snapshot, control.TaskTypeDiagnoseHTTPRule, 89)
	if err == nil || !strings.Contains(err.Error(), "no healthy backend candidates") {
		t.Fatalf("DiagnoseSnapshot() error = %v, want registry cache source backoff", err)
	}
}

func TestRunReturnsInitialSyncErrorWhenNoAppliedSnapshot(t *testing.T) {
	errSync := errors.New("sync failed")
	app := newAppWithAllDeps(
		Config{},
		core.NewInMemory(),
		syncClientFunc(func(context.Context, SyncRequest) (Snapshot, error) {
			return Snapshot{}, errSync
		}),
		nil,
		nil,
	)

	if err := app.Run(context.Background()); !errors.Is(err, errSync) {
		t.Fatalf("Run() error = %v, want %v", err, errSync)
	}
}

func TestRunTreatsFreshStartupCancellationAsGraceful(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	app := newAppWithAllDeps(
		Config{},
		core.NewInMemory(),
		syncClientFunc(func(context.Context, SyncRequest) (Snapshot, error) {
			cancel()
			return Snapshot{}, context.Canceled
		}),
		nil,
		nil,
	)

	if err := app.Run(ctx); err != nil {
		t.Fatalf("Run() cancellation error = %v, want graceful shutdown", err)
	}
}

func TestAdvertisedCapabilitiesUsePanelContract(t *testing.T) {
	got := advertisedCapabilities(Config{})
	want := []string{"http_rules", "cert_install", "managed_certificate_reports_v1", "local_acme", "l4", "relay_quic", "plugin_generation_v1", "egress_profiles"}
	if core.SupportsPackageManifest(stdruntime.GOOS, stdruntime.GOARCH) {
		want = append(want, core.PackageManifestCapability)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("advertisedCapabilities() = %v, want %v", got, want)
	}
}

func TestAdvertisedCapabilitiesIncludeConfiguredOptionalPanelCapabilities(t *testing.T) {
	got := advertisedCapabilities(Config{HTTP3Enabled: true})
	want := []string{"http_rules", "cert_install", "managed_certificate_reports_v1", "local_acme", "l4", "relay_quic", "plugin_generation_v1", "egress_profiles", "http3_ingress"}
	if core.SupportsPackageManifest(stdruntime.GOOS, stdruntime.GOARCH) {
		want = append(want, core.PackageManifestCapability)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("advertisedCapabilities() = %v, want %v", got, want)
	}
}
func TestAdvertisedHotUpgradeCapabilityRequiresSelfCheck(t *testing.T) {
	base := appCapabilitySource{cfg: Config{}, platform: "linux", arch: "amd64"}
	withoutSelfCheck := core.CapabilityNames(base)
	if !containsString(withoutSelfCheck, core.PackageManifestCapability) || containsString(withoutSelfCheck, core.HotUpgradeCapabilityV1) {
		t.Fatalf("capabilities without self-check = %v", withoutSelfCheck)
	}
	base.hotUpgradeReady = true
	ready := core.CapabilityNames(base)
	for _, capability := range []string{core.PackageManifestCapability, core.GenerationCapabilityV1, core.HotUpgradeCapabilityV1} {
		if !containsString(ready, capability) {
			t.Fatalf("ready capabilities = %v, missing %q", ready, capability)
		}
	}
}

func TestHotRestartChildIdentityMustMatchDesiredSnapshotAndJournal(t *testing.T) {
	store, err := core.NewFilesystem(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	desired := Snapshot{Revision: 17}
	desiredDigest := mustHotRestartSnapshotDigest(t, desired)
	if err := store.SaveDesiredSnapshot(desired); err != nil {
		t.Fatal(err)
	}
	journal := model.GenerationJournal{Version: 1, Candidate: &model.GenerationRecord{
		GenerationID: "attempt-generation-17", RuntimeGenerationID: "runtime-generation-17",
		RuntimeSnapshotHash: desiredDigest, Revision: 17, SnapshotDigest: desiredDigest, Phase: model.GenerationPhaseStarted,
		Lease: model.RevisionLease{Revision: 17, LeaseID: "lease-17"},
	}}
	if err := store.SaveGenerationJournal(journal); err != nil {
		t.Fatal(err)
	}
	app := &App{store: store}
	valid := hotrestart.Identity{
		Revision: 17, SnapshotDigest: desiredDigest, GenerationID: "runtime-generation-17", LeaseID: "lease-17", LaunchEpoch: "launch-17",
	}
	if err := app.validateHotRestartIdentity(valid, desired); err != nil {
		t.Fatalf("validateHotRestartIdentity() error = %v", err)
	}
	for _, tc := range []struct {
		name     string
		identity hotrestart.Identity
		desired  Snapshot
	}{
		{name: "revision", identity: func() hotrestart.Identity { value := valid; value.Revision++; return value }(), desired: desired},
		{name: "digest", identity: func() hotrestart.Identity {
			value := valid
			value.SnapshotDigest = strings.Repeat("b", 64)
			return value
		}(), desired: desired},
		{name: "generation", identity: func() hotrestart.Identity { value := valid; value.GenerationID = "other"; return value }(), desired: desired},
		{name: "lease", identity: func() hotrestart.Identity { value := valid; value.LeaseID = "other"; return value }(), desired: desired},
		{name: "desired snapshot", identity: valid, desired: Snapshot{Revision: 18}},
		{name: "same revision changed payload", identity: valid, desired: Snapshot{Revision: 17, DesiredVersion: "corrupt"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := app.validateHotRestartIdentity(tc.identity, tc.desired); err == nil {
				t.Fatal("validateHotRestartIdentity() succeeded, want durable identity rejection")
			}
		})
	}
}

func TestHotRestartLaunchRejectsCorruptedDurableDesiredPayload(t *testing.T) {
	dataDir := t.TempDir()
	store, err := core.NewFilesystem(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	desired := Snapshot{Revision: 17, DesiredVersion: "1.0.0"}
	if err := store.SaveDesiredSnapshot(desired); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveGenerationJournal(model.GenerationJournal{Version: 1, Candidate: &model.GenerationRecord{
		GenerationID: "generation-17", RuntimeGenerationID: "generation-17",
		RuntimeSnapshotHash: mustHotRestartSnapshotDigest(t, desired), Revision: 17, SnapshotDigest: mustHotRestartSnapshotDigest(t, desired),
		Phase: model.GenerationPhaseStarted, Lease: model.RevisionLease{Revision: 17, LeaseID: "lease-17"},
	}}); err != nil {
		t.Fatal(err)
	}
	corrupted, err := json.Marshal(Snapshot{Revision: 17, DesiredVersion: "corrupt"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "desired-snapshot.json"), corrupted, 0o600); err != nil {
		t.Fatal(err)
	}
	app := &App{store: store}
	if _, err := app.hotRestartLaunchIdentity(); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("hotRestartLaunchIdentity() error = %v, want durable digest rejection", err)
	}
}

func TestHotRestartReplacementRunsSupervisorActivationDrainAndAuthority(t *testing.T) {
	store, err := core.NewFilesystem(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	desired := Snapshot{Revision: 18, DesiredVersion: "2.0.0"}
	desiredDigest := mustHotRestartSnapshotDigest(t, desired)
	if err := store.SaveDesiredSnapshot(desired); err != nil {
		t.Fatal(err)
	}
	journal := model.GenerationJournal{Version: 1, Candidate: &model.GenerationRecord{
		GenerationID: "attempt-generation-18", RuntimeGenerationID: "runtime-generation-18",
		RuntimeSnapshotHash: desiredDigest, Revision: 18, SnapshotDigest: desiredDigest, Phase: model.GenerationPhaseStarted,
		Lease: model.RevisionLease{Revision: 18, LeaseID: "lease-18"},
	}}
	if err := store.SaveGenerationJournal(journal); err != nil {
		t.Fatal(err)
	}

	var order []string
	process := &recordingHotRestartProcess{order: &order}
	app := &App{cfg: Config{DataDir: t.TempDir()}, store: store, runCtx: t.Context()}
	app.hotRestartStart = func(_ context.Context, launch hotrestart.Launch) (hotRestartProcess, error) {
		order = append(order, "start")
		if launch.Binary != "/staged/nre-agent" || launch.Identity.Revision != 18 || launch.Identity.GenerationID != "runtime-generation-18" ||
			launch.Identity.LeaseID != "lease-18" || launch.Identity.SnapshotDigest != desiredDigest {
			t.Fatalf("launch = %+v", launch)
		}
		if launch.AuthorityJournal == "" {
			t.Fatal("authority journal path is empty")
		}
		return process, nil
	}
	app.hotRestartDrain = func(context.Context, hotrestart.Identity) error {
		order = append(order, "drain")
		return nil
	}

	err = app.hotRestartReplacement(t.Context(), "/staged/nre-agent", []string{"/staged/nre-agent", "serve"}, []string{"NRE_AGENT_VERSION=2.0.0"})
	if !errors.Is(err, core.ErrRestartRequested) {
		t.Fatalf("hotRestartReplacement() error = %v, want restart requested", err)
	}
	if want := []string{"start", "activate", "authority", "drain", "wait"}; !reflect.DeepEqual(order, want) {
		t.Fatalf("replacement order = %v, want %v", order, want)
	}
}

func TestNewUpdateManagerUsesHotRestartReplacement(t *testing.T) {
	if stdruntime.GOOS != "linux" || (stdruntime.GOARCH != "amd64" && stdruntime.GOARCH != "arm64") {
		t.Skip("hot upgrade packages are supported on linux amd64/arm64")
	}
	dataDir := t.TempDir()
	app, err := New(Config{
		AgentID: "agent", AgentName: "agent", MasterURL: "https://master.example.com",
		AgentToken: "token", CurrentVersion: "1.0.0", DataDir: dataDir,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = app.Close() })
	store := app.store.(*core.Filesystem)
	desired := Snapshot{Revision: 18, DesiredVersion: "2.0.0"}
	if err := store.SaveDesiredSnapshot(desired); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveGenerationJournal(model.GenerationJournal{Version: 1, Candidate: &model.GenerationRecord{
		GenerationID: "generation-18", RuntimeGenerationID: "generation-18",
		RuntimeSnapshotHash: mustHotRestartSnapshotDigest(t, desired), Revision: 18, SnapshotDigest: mustHotRestartSnapshotDigest(t, desired),
		Phase: model.GenerationPhaseStarted, Lease: model.RevisionLease{Revision: 18, LeaseID: "lease-18"},
	}}); err != nil {
		t.Fatal(err)
	}
	var order []string
	app.hotRestartStart = func(context.Context, hotrestart.Launch) (hotRestartProcess, error) {
		order = append(order, "start")
		return &recordingHotRestartProcess{order: &order}, nil
	}
	app.hotRestartDrain = func(context.Context, hotrestart.Identity) error {
		order = append(order, "drain")
		return nil
	}
	payload := []byte("replacement-agent")
	sourcePath := filepath.Join(t.TempDir(), "nre-agent")
	if err := os.WriteFile(sourcePath, payload, 0o555); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(payload)
	stagedPath, err := app.updater.Stage(t.Context(), model.VersionPackage{
		URL: "file://" + filepath.ToSlash(sourcePath), SHA256: hex.EncodeToString(digest[:]),
		Platform: "linux-" + stdruntime.GOARCH, Filename: "nre-agent-linux-" + stdruntime.GOARCH, Size: int64(len(payload)),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := app.updater.Activate(t.Context(), stagedPath, "2.0.0"); !errors.Is(err, core.ErrRestartRequested) {
		t.Fatalf("production updater Activate() error = %v", err)
	}
	if want := []string{"start", "activate", "authority", "drain", "wait"}; !reflect.DeepEqual(order, want) {
		t.Fatalf("production updater order = %v, want %v", order, want)
	}
}

func TestHotRestartReplacementAbortsAndRetainsParentOnFailure(t *testing.T) {
	store, err := core.NewFilesystem(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	desired := Snapshot{Revision: 18}
	if err := store.SaveDesiredSnapshot(desired); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveGenerationJournal(model.GenerationJournal{Version: 1, Candidate: &model.GenerationRecord{
		GenerationID: "generation-18", RuntimeGenerationID: "generation-18",
		RuntimeSnapshotHash: mustHotRestartSnapshotDigest(t, desired), Revision: 18, SnapshotDigest: mustHotRestartSnapshotDigest(t, desired),
		Phase: model.GenerationPhaseStarted, Lease: model.RevisionLease{Revision: 18, LeaseID: "lease-18"},
	}}); err != nil {
		t.Fatal(err)
	}

	var order []string
	process := &recordingHotRestartProcess{order: &order, activateErr: errors.New("child activation failed")}
	app := &App{cfg: Config{DataDir: t.TempDir()}, store: store, runCtx: t.Context()}
	app.hotRestartStart = func(context.Context, hotrestart.Launch) (hotRestartProcess, error) {
		order = append(order, "start")
		return process, nil
	}
	err = app.hotRestartReplacement(t.Context(), "/staged/nre-agent", nil, nil)
	if err == nil || errors.Is(err, core.ErrRestartRequested) || !strings.Contains(err.Error(), "child activation failed") {
		t.Fatalf("hotRestartReplacement() error = %v", err)
	}
	if want := []string{"start", "activate", "abort"}; !reflect.DeepEqual(order, want) {
		t.Fatalf("failure order = %v, want %v", order, want)
	}
}

func TestIntegrationHotRestartDrainWaitsForSameGenerationParentSessions(t *testing.T) {
	if testing.Short() {
		t.Skip("real generation drain timeout runs in the integration tier")
	}
	configured, err := newConfiguredModules(Config{AgentID: "agent", AgentName: "agent", DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	app := &App{}
	app.setConfiguredModules(configured)
	t.Cleanup(func() { _ = app.Close() })
	if err := app.runtime.Apply(t.Context(), Snapshot{}, Snapshot{Revision: 17, DesiredVersion: "1.0.0"}); err != nil {
		t.Fatal(err)
	}
	active := app.generations.ActiveIdentity()
	handle, err := app.generations.RegisterSession(active.ID, generation.EntityKey{Module: "http", ID: "rule-1"}, "session-1", appDrainTestSession{})
	if err != nil {
		t.Fatal(err)
	}
	drained := make(chan error, 1)
	go func() {
		drained <- app.drainHotRestartParent(t.Context(), hotrestart.Identity{Revision: 17, GenerationID: active.ID})
	}()
	select {
	case err := <-drained:
		t.Fatalf("drain returned with parent session still active: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	handle.Finish()
	select {
	case err := <-drained:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("drain did not finish after parent session completion")
	}
	forcedModules, err := newConfiguredModules(Config{AgentID: "agent-forced", AgentName: "agent-forced", DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	forcedApp := &App{}
	forcedApp.setConfiguredModules(forcedModules)
	t.Cleanup(func() { _ = forcedApp.Close() })
	if err := forcedApp.runtime.Apply(t.Context(), Snapshot{}, Snapshot{Revision: 18, DesiredVersion: "2.0.0"}); err != nil {
		t.Fatal(err)
	}
	forcedGeneration := forcedApp.generations.ActiveIdentity()
	timeoutHandle, err := forcedApp.generations.RegisterSession(forcedGeneration.ID, generation.EntityKey{Module: "http", ID: "rule-1"}, "session-timeout", appDrainTestSession{})
	if err != nil {
		t.Fatal(err)
	}
	defer timeoutHandle.Finish()
	forcedApp.hotRestartDrainTimeout = 100 * time.Millisecond
	if err := forcedApp.drainHotRestartParent(t.Context(), hotrestart.Identity{Revision: 18, GenerationID: forcedGeneration.ID}); err != nil {
		t.Fatalf("forced drain error = %v", err)
	}
	forced := false
	for _, status := range forcedApp.generations.DrainController().Snapshot().Generations {
		if status.GenerationID == forcedGeneration.ID && status.State == model.GenerationDrainStateForced {
			forced = true
		}
	}
	if !forced {
		t.Fatalf("generation %q was not recorded as forced", forcedGeneration.ID)
	}
}

func TestHotRestartSupervisorKeepsManagerAliveAndForwardsStop(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	process := &supervisedHotRestartProcess{waitStarted: make(chan struct{}), release: make(chan struct{}), signaled: make(chan os.Signal, 1)}
	result := make(chan error, 1)
	app := &App{}
	journalPath := filepath.Join(t.TempDir(), "authority.json")
	go func() {
		result <- app.superviseHotRestartLineage(
			ctx,
			process,
			journalPath,
			hotrestart.Identity{},
		)
	}()
	<-process.waitStarted
	released := false
	defer func() {
		if !released {
			close(process.release)
		}
	}()
	cancel()
	select {
	case signal := <-process.signaled:
		if signal != os.Interrupt {
			t.Fatalf("forwarded signal = %v, want interrupt", signal)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("manager did not forward shutdown to child")
	}
	select {
	case err := <-result:
		t.Fatalf("manager returned before the authoritative child exited: %v", err)
	default:
	}
	close(process.release)
	released = true
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("supervisor result = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("manager did not reap child after forwarded shutdown")
	}
}

type appDrainTestSession struct{}

func (appDrainTestSession) ForceClose(context.Context, string) error { return nil }

type supervisedHotRestartProcess struct {
	waitStarted chan struct{}
	release     chan struct{}
	signaled    chan os.Signal
}

func (*supervisedHotRestartProcess) Activate(context.Context) error          { return nil }
func (*supervisedHotRestartProcess) TransferAuthority(context.Context) error { return nil }
func (p *supervisedHotRestartProcess) Wait() error {
	close(p.waitStarted)
	<-p.release
	return nil
}
func (p *supervisedHotRestartProcess) Signal(signal os.Signal) error {
	p.signaled <- signal
	return nil
}
func (p *supervisedHotRestartProcess) Abort() error { return nil }

type recordingHotRestartProcess struct {
	order        *[]string
	activateErr  error
	authorityErr error
	abortErr     error
}

func (p *recordingHotRestartProcess) Activate(context.Context) error {
	*p.order = append(*p.order, "activate")
	return p.activateErr
}

func (p *recordingHotRestartProcess) TransferAuthority(context.Context) error {
	*p.order = append(*p.order, "authority")
	return p.authorityErr
}

func (p *recordingHotRestartProcess) Wait() error {
	*p.order = append(*p.order, "wait")
	return nil
}

func (p *recordingHotRestartProcess) Signal(os.Signal) error {
	*p.order = append(*p.order, "signal")
	return nil
}

func (p *recordingHotRestartProcess) Abort() error {
	*p.order = append(*p.order, "abort")
	return p.abortErr
}

type recordingProcessStreamAuthority struct{ order *[]string }

func (a recordingProcessStreamAuthority) Pause() error {
	*a.order = append(*a.order, "pause")
	return nil
}

func (a recordingProcessStreamAuthority) Resume() error {
	*a.order = append(*a.order, "resume")
	return nil
}

func TestHotRestartStreamActivationAbortsChildBeforeParentResume(t *testing.T) {
	var order []string
	process := &hotRestartStreamProcess{
		hotRestartProcess: &recordingHotRestartProcess{order: &order, activateErr: errors.New("activation failed")},
		parent:            recordingProcessStreamAuthority{order: &order},
	}
	if err := process.Activate(t.Context()); err == nil {
		t.Fatal("Activate() succeeded")
	}
	if want := []string{"pause", "activate", "abort", "resume"}; !reflect.DeepEqual(order, want) {
		t.Fatalf("activation failure order = %v, want %v", order, want)
	}
}

func TestHotRestartStreamActivationResumesParentWhenAbortFails(t *testing.T) {
	var order []string
	abortErr := errors.New("child termination unconfirmed")
	process := &hotRestartStreamProcess{
		hotRestartProcess: &recordingHotRestartProcess{
			order: &order, activateErr: errors.New("activation failed"), abortErr: abortErr,
		},
		parent: recordingProcessStreamAuthority{order: &order},
	}
	if err := process.Activate(t.Context()); !errors.Is(err, abortErr) {
		t.Fatalf("Activate() error = %v, want abort failure", err)
	}
	if want := []string{"pause", "activate", "abort", "resume"}; !reflect.DeepEqual(order, want) {
		t.Fatalf("unconfirmed termination order = %v, want %v", order, want)
	}
}

func TestHotRestartStreamAbortResumesParentWhenTerminationUnconfirmed(t *testing.T) {
	var order []string
	abortErr := errors.New("child termination unconfirmed")
	process := &hotRestartStreamProcess{
		hotRestartProcess: &recordingHotRestartProcess{order: &order, abortErr: abortErr},
		parent:            recordingProcessStreamAuthority{order: &order},
	}
	if err := process.Abort(); !errors.Is(err, abortErr) {
		t.Fatalf("Abort() error = %v, want termination failure", err)
	}
	if want := []string{"abort", "resume"}; !reflect.DeepEqual(order, want) {
		t.Fatalf("unconfirmed abort order = %v, want %v", order, want)
	}
}

func mustHotRestartSnapshotDigest(t *testing.T, snapshot Snapshot) string {
	t.Helper()
	digest, err := hotRestartSnapshotDigest(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	return digest
}

func containsString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func TestSnapshotActivatorAppliesOutboundProxyBeforeRegistryModules(t *testing.T) {
	previousProxy := modulerelay.OutboundProxyURL()
	t.Cleanup(func() { modulerelay.SetOutboundProxyURL(previousProxy) })
	modulerelay.SetOutboundProxyURL("socks://127.0.0.1:1080")

	registry := agentmodule.NewRegistry()
	mustRegisterAppModule(t, registry, appApplyFuncModule{
		name: "http",
		apply: func(context.Context, agentmodule.ApplyRequest) error {
			if got := modulerelay.OutboundProxyURL(); got != "socks://127.0.0.1:2080" {
				t.Fatalf("OutboundProxyURL() during registry apply = %q, want next snapshot proxy", got)
			}
			return nil
		},
	})
	activator := appSnapshotActivator(registry)

	if err := activator(context.Background(),
		Snapshot{AgentConfig: model.AgentConfig{OutboundProxyURL: "socks://127.0.0.1:1080"}},
		Snapshot{AgentConfig: model.AgentConfig{OutboundProxyURL: "socks://127.0.0.1:2080"}},
	); err != nil {
		t.Fatalf("activator() error = %v", err)
	}
}

func TestSnapshotActivatorRestoresOutboundProxyOnRegistryFailure(t *testing.T) {
	previousProxy := modulerelay.OutboundProxyURL()
	t.Cleanup(func() { modulerelay.SetOutboundProxyURL(previousProxy) })
	modulerelay.SetOutboundProxyURL("socks://127.0.0.1:1080")

	failErr := errors.New("module activation failed")
	registry := agentmodule.NewRegistry()
	mustRegisterAppModule(t, registry, appApplyFuncModule{
		name:  "later",
		apply: func(context.Context, agentmodule.ApplyRequest) error { return failErr },
	})
	activator := appSnapshotActivator(registry)

	err := activator(context.Background(),
		Snapshot{AgentConfig: model.AgentConfig{OutboundProxyURL: "socks://127.0.0.1:1080"}},
		Snapshot{AgentConfig: model.AgentConfig{OutboundProxyURL: "socks://127.0.0.1:2080"}},
	)
	if !errors.Is(err, failErr) {
		t.Fatalf("activator() error = %v, want %v", err, failErr)
	}
	if got := modulerelay.OutboundProxyURL(); got != "socks://127.0.0.1:1080" {
		t.Fatalf("OutboundProxyURL() after failed activation = %q, want previous proxy", got)
	}
}

type syncClientFunc func(context.Context, SyncRequest) (Snapshot, error)

func TestRuntimePayloadCompleteRequiresPluginGenerationsAndDependencies(t *testing.T) {
	complete := Snapshot{
		Rules: []model.HTTPRule{}, L4Rules: []model.L4Rule{}, RelayListeners: []model.RelayListener{},
		EgressProfiles: []model.EgressProfile{}, Certificates: []model.ManagedCertificateBundle{},
		CertificatePolicies: []model.ManagedCertificatePolicy{}, PluginPolicies: []model.PluginPolicy{},
		PluginGenerations: []model.PluginGeneration{}, PluginDependencies: []model.PluginDependencyEdge{},
	}
	if !runtimePayloadComplete(complete) {
		t.Fatal("explicit full plugin generation payload was incomplete")
	}
	complete.PluginGenerations = nil
	if runtimePayloadComplete(complete) {
		t.Fatal("nil plugin generation payload was accepted as complete")
	}
	complete.PluginGenerations = []model.PluginGeneration{}
	complete.PluginDependencies = nil
	if runtimePayloadComplete(complete) {
		t.Fatal("nil plugin dependency payload was accepted as complete")
	}
}

func (f syncClientFunc) Sync(ctx context.Context, req SyncRequest) (Snapshot, error) {
	return f(ctx, req)
}

type appProviderModule struct {
	name     string
	provides agentmodule.ProviderRef
	provider any
}

func (m appProviderModule) Name() string { return m.name }

func (m appProviderModule) Descriptor() agentmodule.ModuleDescriptor {
	return agentmodule.ModuleDescriptor{Name: m.name, Provides: []agentmodule.ProviderRef{m.provides}}
}

func (m appProviderModule) RegisterProviders(reg agentmodule.ProviderRegistry) error {
	return reg.Provide(m.provides, m.provider)
}

func (m appProviderModule) Capabilities(agentmodule.SnapshotView) []agentmodule.Capability {
	return nil
}

func (m appProviderModule) Apply(context.Context, agentmodule.ApplyRequest) error { return nil }

func (m appProviderModule) Stop(context.Context) error { return nil }

type appApplyFuncModule struct {
	name  string
	apply func(context.Context, agentmodule.ApplyRequest) error
}

func (m appApplyFuncModule) Name() string { return m.name }

func (m appApplyFuncModule) Descriptor() agentmodule.ModuleDescriptor {
	return agentmodule.ModuleDescriptor{Name: m.name}
}

func (m appApplyFuncModule) RegisterProviders(agentmodule.ProviderRegistry) error {
	return nil
}

func (m appApplyFuncModule) Capabilities(agentmodule.SnapshotView) []agentmodule.Capability {
	return nil
}

func (m appApplyFuncModule) Apply(ctx context.Context, req agentmodule.ApplyRequest) error {
	if m.apply == nil {
		return nil
	}
	return m.apply(ctx, req)
}

func (m appApplyFuncModule) Stop(context.Context) error { return nil }

type appDiagnosticSource struct {
	cache *model.Cache
}

func (s appDiagnosticSource) Cache() *model.Cache {
	return s.cache
}

func mustRegisterAppModule(t *testing.T, registry *agentmodule.Registry, candidate agentmodule.Module) {
	t.Helper()
	if err := registry.Register(candidate); err != nil {
		t.Fatalf("Register(%s) error = %v", candidate.Name(), err)
	}
}

func extractPrivateTransport(t *testing.T, client any) *http.Transport {
	t.Helper()

	value := reflect.ValueOf(client)
	if value.Kind() != reflect.Pointer || value.IsNil() {
		t.Fatalf("client = %T", client)
	}
	field := value.Elem().FieldByName("transport")
	if !field.IsValid() {
		t.Fatalf("transport field not found on %T", client)
	}
	return reflect.NewAt(field.Type(), unsafe.Pointer(field.UnsafeAddr())).Elem().Interface().(*http.Transport)
}

var _ SyncClient = (*control.SyncClient)(nil)
var _ pluginrpc.SecretRedeemer = (*control.SyncClient)(nil)
