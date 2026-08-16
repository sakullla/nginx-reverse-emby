//go:build !integration

package traffic_test

import (
	"context"
	"errors"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
	trafficmodule "github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/traffic"
)

func TestModuleApplyOwnsTrafficEnabledAndBlockState(t *testing.T) {
	trafficmodule.SetEnabled(true)
	t.Cleanup(func() {
		trafficmodule.SetEnabled(true)
		trafficmodule.Reset()
	})
	disabled := false
	mod := trafficmodule.NewModule()

	if err := mod.Apply(context.Background(), module.ApplyRequest{
		Next: model.Snapshot{AgentConfig: model.AgentConfig{
			TrafficStatsEnabled: &disabled,
			TrafficBlocked:      true,
			TrafficBlockReason:  " monthly quota exceeded ",
		}},
	}); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	if trafficmodule.Enabled() {
		t.Fatal("traffic enabled = true, want traffic module to disable stats")
	}
	if got := mod.TrafficBlockState(); !got.Blocked || got.Reason != "monthly quota exceeded" {
		t.Fatalf("TrafficBlockState() = %+v, want normalized blocked state", got)
	}
}

func TestModuleKeepsPreparedTrafficStateInvisibleUntilPublish(t *testing.T) {
	trafficmodule.SetEnabled(true)
	t.Cleanup(func() {
		trafficmodule.SetEnabled(true)
		trafficmodule.Reset()
	})
	mod := trafficmodule.NewModule()
	registry := module.NewRegistry()
	mustRegisterTrafficTestModule(t, registry, mod)
	first := model.Snapshot{Revision: 1}
	if err := registry.Apply(context.Background(), model.Snapshot{}, first); err != nil {
		t.Fatalf("Apply(first) error = %v", err)
	}
	firstView := registry.ActiveGeneration()

	disabled := false
	second := model.Snapshot{Revision: 2, AgentConfig: model.AgentConfig{
		TrafficStatsEnabled: &disabled,
		TrafficBlocked:      true,
		TrafficBlockReason:  "quota",
	}}
	generationContext, err := module.NewGenerationContext(first, second)
	if err != nil {
		t.Fatalf("NewGenerationContext() error = %v", err)
	}
	candidate, err := registry.PrepareGeneration(context.Background(), generationContext)
	if err != nil {
		t.Fatalf("PrepareGeneration() error = %v", err)
	}
	if err := candidate.Ready(context.Background()); err != nil {
		t.Fatalf("Ready() error = %v", err)
	}

	if registry.ActiveGeneration() != firstView || !trafficmodule.Enabled() {
		t.Fatal("traffic candidate became active before publish")
	}
	if got := mod.TrafficBlockState(); got.Blocked {
		t.Fatalf("TrafficBlockState() before publish = %+v, want unblocked", got)
	}

	candidate.Publish()
	if trafficmodule.Enabled() {
		t.Fatal("active generation did not disable global traffic collectors")
	}
	if got := mod.TrafficBlockState(); !got.Blocked || got.Reason != "quota" {
		t.Fatalf("legacy TrafficBlockState() after publish = %+v, want active generation state", got)
	}
	if got, ok := trafficmodule.BlockStateFromProvider(registry); !ok || !got.Blocked || got.Reason != "quota" {
		t.Fatalf("candidate TrafficBlockState() after publish = %+v/%v", got, ok)
	}
}

func TestGenerationTrafficDisableDropsGlobalAndRecorderWindow(t *testing.T) {
	trafficmodule.Reset()
	trafficmodule.SetEnabled(true)
	t.Cleanup(func() {
		trafficmodule.SetEnabled(true)
		trafficmodule.Reset()
	})

	registry := module.NewRegistry()
	mod := trafficmodule.NewModule(trafficmodule.Config{GenerationSelector: registry})
	mustRegisterTrafficTestModule(t, registry, mod)
	enabled := true
	if err := registry.Apply(context.Background(), model.Snapshot{}, model.Snapshot{
		Revision: 1, AgentConfig: model.AgentConfig{TrafficStatsEnabled: &enabled},
	}); err != nil {
		t.Fatal(err)
	}
	trafficmodule.AddHTTP(10, 20)
	preDisableRecorder := trafficmodule.NewL4Recorder()
	preDisableRecorder.Add(30, 40)

	disabled := false
	if err := registry.Apply(context.Background(), model.Snapshot{Revision: 1}, model.Snapshot{
		Revision: 2, AgentConfig: model.AgentConfig{TrafficStatsEnabled: &disabled},
	}); err != nil {
		t.Fatal(err)
	}
	if trafficmodule.Enabled() {
		t.Fatal("global collector remained enabled after active generation disabled it")
	}
	trafficmodule.AddHTTP(100, 200)
	preDisableRecorder.Add(300, 400)
	preDisableRecorder.Flush()
	disabledRecorder := trafficmodule.NewRelayRecorder()
	disabledRecorder.Add(500, 600)
	disabledRecorder.Flush()

	if err := registry.Apply(context.Background(), model.Snapshot{Revision: 2}, model.Snapshot{
		Revision: 3, AgentConfig: model.AgentConfig{TrafficStatsEnabled: &enabled},
	}); err != nil {
		t.Fatal(err)
	}
	if !trafficmodule.Enabled() {
		t.Fatal("global collector remained disabled after active generation enabled it")
	}
	if leaked := trafficmodule.SnapshotNonZero(); leaked != nil {
		t.Fatalf("disabled-window traffic leaked after re-enable: %+v", leaked)
	}

	trafficmodule.AddHTTP(7, 11)
	preDisableRecorder.Add(13, 17)
	preDisableRecorder.Flush()
	stats := trafficmodule.SnapshotNonZero()
	if stats == nil {
		t.Fatal("post-enable traffic was not collected")
	}
	traffic := stats["traffic"].(map[string]any)
	assertModuleTrafficCounters(t, traffic["http"], 7, 11)
	assertModuleTrafficCounters(t, traffic["l4"], 13, 17)
	assertModuleTrafficCounters(t, traffic["relay"], 0, 0)
}

func assertModuleTrafficCounters(t *testing.T, value any, rx, tx uint64) {
	t.Helper()
	counters, ok := value.(map[string]uint64)
	if !ok || counters["rx_bytes"] != rx || counters["tx_bytes"] != tx {
		t.Fatalf("traffic counters = %#v, want rx=%d tx=%d", value, rx, tx)
	}
}

func TestModuleRollsBackTrafficStateWhenLaterModuleFails(t *testing.T) {
	trafficmodule.SetEnabled(true)
	t.Cleanup(func() {
		trafficmodule.SetEnabled(true)
		trafficmodule.Reset()
	})
	disabled := false
	mod := trafficmodule.NewModule()
	registry := module.NewRegistry()
	mustRegisterTrafficTestModule(t, registry, mod)
	mustRegisterTrafficTestModule(t, registry, trafficCommitFailingModule{name: "after-traffic", err: errors.New("later commit failed")})

	err := registry.Apply(context.Background(), model.Snapshot{}, model.Snapshot{AgentConfig: model.AgentConfig{
		TrafficStatsEnabled: &disabled,
		TrafficBlocked:      true,
		TrafficBlockReason:  "monthly quota exceeded",
	}})
	if err == nil {
		t.Fatal("Apply() error = nil, want later commit failure")
	}

	if !trafficmodule.Enabled() {
		t.Fatal("traffic enabled = false after rollback, want previous true")
	}
	if got := mod.TrafficBlockState(); got.Blocked || got.Reason != "" {
		t.Fatalf("TrafficBlockState() after rollback = %+v, want previous unblocked state", got)
	}
}

func TestModuleRollbackAfterDisablePreservesCommittedCounters(t *testing.T) {
	trafficmodule.Reset()
	trafficmodule.SetEnabled(true)
	trafficmodule.AddHTTP(11, 22)
	t.Cleanup(func() {
		trafficmodule.SetEnabled(true)
		trafficmodule.Reset()
	})
	disabled := false
	mod := trafficmodule.NewModule()
	registry := module.NewRegistry()
	mustRegisterTrafficTestModule(t, registry, mod)
	mustRegisterTrafficTestModule(t, registry, trafficCommitFailingModule{name: "after-traffic", err: errors.New("later commit failed")})

	err := registry.Apply(context.Background(), model.Snapshot{}, model.Snapshot{AgentConfig: model.AgentConfig{
		TrafficStatsEnabled: &disabled,
	}})
	if err == nil {
		t.Fatal("Apply() error = nil, want later commit failure")
	}

	if !trafficmodule.Enabled() {
		t.Fatal("traffic enabled = false after rollback, want previous true")
	}
	stats := trafficmodule.Snapshot()["traffic"].(map[string]any)
	total := stats["total"].(map[string]uint64)
	if total["rx_bytes"] != 11 || total["tx_bytes"] != 22 {
		t.Fatalf("traffic total after rollback = %+v, want committed counters preserved", total)
	}
}

func mustRegisterTrafficTestModule(t *testing.T, registry *module.Registry, candidate module.Module) {
	t.Helper()
	if err := registry.Register(candidate); err != nil {
		t.Fatalf("Register(%s) error = %v", candidate.Name(), err)
	}
}

type trafficCommitFailingModule struct {
	name string
	err  error
}

func (m trafficCommitFailingModule) Name() string { return m.name }

func (m trafficCommitFailingModule) Descriptor() module.ModuleDescriptor {
	return module.ModuleDescriptor{Name: m.name}
}

func (trafficCommitFailingModule) RegisterProviders(module.ProviderRegistry) error { return nil }

func (trafficCommitFailingModule) Capabilities(module.SnapshotView) []module.Capability { return nil }

func (m trafficCommitFailingModule) Apply(context.Context, module.ApplyRequest) error { return nil }

func (m trafficCommitFailingModule) Prepare(context.Context, module.ApplyRequest) (module.ModuleTransaction, error) {
	return module.TransactionFuncs{CommitFunc: func() error { return m.err }}, nil
}

func (trafficCommitFailingModule) Stop(context.Context) error { return nil }
