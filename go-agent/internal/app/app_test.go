package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/control"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/core"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/generation"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/hotrestart"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	agentmodule "github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
	modulediagnostics "github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/diagnostics"
	modulerelay "github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/relay"
	"io"
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
	if app.runtime == nil {
		t.Fatal("runtime = nil")
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

func TestNewRegistersConfiguredModules(t *testing.T) {
	tests := []struct {
		name              string
		wireGuardEnabled  bool
		wireGuardExplicit bool
		want              []string
	}{
		{
			name:              "implicit default",
			wireGuardEnabled:  false,
			wireGuardExplicit: false,
			want:              []string{"certs", "diagnostics", "egress", "http", "wireguard", "relay", "l4", "traffic"},
		},
		{
			name:              "explicit disabled",
			wireGuardEnabled:  false,
			wireGuardExplicit: true,
			want:              []string{"certs", "diagnostics", "egress", "http", "relay", "l4", "traffic"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			app, err := New(Config{
				AgentID:           "agent",
				AgentName:         "agent",
				MasterURL:         "https://master.example.com",
				AgentToken:        "token",
				CurrentVersion:    "0.1.0",
				DataDir:           t.TempDir(),
				WireGuardEnabled:  tc.wireGuardEnabled,
				WireGuardExplicit: tc.wireGuardExplicit,
			})
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}
			t.Cleanup(func() { _ = app.Close() })

			if got := app.ModuleNames(); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("ModuleNames() = %v, want %v", got, tc.want)
			}
		})
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

func TestAdvertisedCapabilitiesUsePanelContract(t *testing.T) {
	got := advertisedCapabilities(Config{WireGuardEnabled: false, WireGuardExplicit: true})
	want := []string{"http_rules", "cert_install", "local_acme", "l4", "relay_quic", "egress_profiles"}
	if core.SupportsPackageManifest(stdruntime.GOOS, stdruntime.GOARCH) {
		want = append(want, core.PackageManifestCapability)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("advertisedCapabilities() = %v, want %v", got, want)
	}
}

func TestAdvertisedCapabilitiesIncludeConfiguredOptionalPanelCapabilities(t *testing.T) {
	got := advertisedCapabilities(Config{HTTP3Enabled: true})
	want := []string{"http_rules", "cert_install", "local_acme", "l4", "relay_quic", "wireguard", "egress_profiles", "http3_ingress"}
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
		Revision: 17, SnapshotDigest: desiredDigest, Phase: model.GenerationPhaseStarted,
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
		GenerationID: "generation-17", Revision: 17, SnapshotDigest: mustHotRestartSnapshotDigest(t, desired),
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
	if _, err := app.hotRestartLaunchIdentity(); err == nil || !strings.Contains(err.Error(), "not ready") {
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
		Revision: 18, SnapshotDigest: desiredDigest, Phase: model.GenerationPhaseStarted,
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

	err = app.hotRestartReplacement("/staged/nre-agent", []string{"/staged/nre-agent", "serve"}, []string{"NRE_AGENT_VERSION=2.0.0"})
	if !errors.Is(err, core.ErrRestartRequested) {
		t.Fatalf("hotRestartReplacement() error = %v, want restart requested", err)
	}
	if want := []string{"start", "activate", "drain", "authority", "wait"}; !reflect.DeepEqual(order, want) {
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
		GenerationID: "generation-18", Revision: 18, SnapshotDigest: mustHotRestartSnapshotDigest(t, desired),
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
	if err := app.updater.Activate(stagedPath, "2.0.0"); !errors.Is(err, core.ErrRestartRequested) {
		t.Fatalf("production updater Activate() error = %v", err)
	}
	if want := []string{"start", "activate", "drain", "authority", "wait"}; !reflect.DeepEqual(order, want) {
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
		GenerationID: "generation-18", Revision: 18, SnapshotDigest: mustHotRestartSnapshotDigest(t, desired),
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
	err = app.hotRestartReplacement("/staged/nre-agent", nil, nil)
	if err == nil || errors.Is(err, core.ErrRestartRequested) || !strings.Contains(err.Error(), "child activation failed") {
		t.Fatalf("hotRestartReplacement() error = %v", err)
	}
	if want := []string{"start", "activate", "abort"}; !reflect.DeepEqual(order, want) {
		t.Fatalf("failure order = %v, want %v", order, want)
	}
}

func TestHotRestartDrainWaitsForSameGenerationParentSessions(t *testing.T) {
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
	timeoutHandle, err := app.generations.RegisterSession(active.ID, generation.EntityKey{Module: "http", ID: "rule-1"}, "session-timeout", appDrainTestSession{})
	if err != nil {
		t.Fatal(err)
	}
	defer timeoutHandle.Finish()
	app.hotRestartDrainTimeout = 100 * time.Millisecond
	if err := app.drainHotRestartParent(t.Context(), hotrestart.Identity{Revision: 17, GenerationID: active.ID}); err == nil || !strings.Contains(err.Error(), "did not drain") {
		t.Fatalf("timed drain error = %v", err)
	}
}

func TestHotRestartSupervisorKeepsManagerAliveAndForwardsStop(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	process := &supervisedHotRestartProcess{waitStarted: make(chan struct{}), release: make(chan struct{}), signaled: make(chan os.Signal, 1)}
	result := make(chan error, 1)
	go func() { result <- superviseHotRestartChild(ctx, process) }()
	<-process.waitStarted
	select {
	case err := <-result:
		t.Fatalf("manager returned while authoritative child was running: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
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
		if !errors.Is(err, core.ErrRestartRequested) {
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
	close(p.release)
	return nil
}
func (p *supervisedHotRestartProcess) Abort() error { return nil }

type recordingHotRestartProcess struct {
	order        *[]string
	activateErr  error
	authorityErr error
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
	return nil
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
