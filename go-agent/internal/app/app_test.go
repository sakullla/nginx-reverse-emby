//go:build !integration

package app

import (
	"context"

	"errors"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/control"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/core"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/hotrestart"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	agentmodule "github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"

	modulerelay "github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/relay"

	pluginrpc "github.com/sakullla/nginx-reverse-emby/go-agent/internal/plugins/rpc"

	"os"

	"reflect"

	"strings"
	"testing"
)

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

func mustHotRestartSnapshotDigest(t *testing.T, snapshot Snapshot) string {
	t.Helper()
	digest, err := hotRestartSnapshotDigest(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	return digest
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

func mustRegisterAppModule(t *testing.T, registry *agentmodule.Registry, candidate agentmodule.Module) {
	t.Helper()
	if err := registry.Register(candidate); err != nil {
		t.Fatalf("Register(%s) error = %v", candidate.Name(), err)
	}
}

var _ pluginrpc.SecretRedeemer = (*control.SyncClient)(nil)
