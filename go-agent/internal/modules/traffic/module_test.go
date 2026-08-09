package traffic_test

import (
	"context"
	"errors"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
	trafficmodule "github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/traffic"
)

func TestModuleReportsRuntimeMetadataAfterAppliedTrafficStatsInterval(t *testing.T) {
	mod := trafficmodule.NewModule(trafficmodule.Config{Interfaces: []string{"lo"}})
	trafficmodule.Reset()
	trafficmodule.SetEnabled(true)
	t.Cleanup(func() {
		trafficmodule.SetEnabled(true)
		trafficmodule.Reset()
	})
	trafficmodule.AddHTTP(1, 2)

	if err := mod.Apply(context.Background(), module.ApplyRequest{
		Next: model.Snapshot{AgentConfig: model.AgentConfig{TrafficStatsInterval: "5s"}},
	}); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	report, err := mod.TrafficReport(context.Background(), map[string]string{})
	if err != nil {
		t.Fatalf("TrafficReport() error = %v", err)
	}
	if report.RuntimeMetadata == nil {
		t.Fatal("RuntimeMetadata = nil, want last traffic stats report metadata")
	}
}

func TestModuleTrafficReportMergesModuleMetadataWithNilInput(t *testing.T) {
	mod := trafficmodule.NewModule(trafficmodule.Config{Interfaces: []string{"lo"}})
	trafficmodule.Reset()
	trafficmodule.SetEnabled(true)
	t.Cleanup(func() {
		trafficmodule.SetEnabled(true)
		trafficmodule.Reset()
	})
	trafficmodule.AddHTTP(1, 2)

	if err := mod.Apply(context.Background(), module.ApplyRequest{
		Next: model.Snapshot{AgentConfig: model.AgentConfig{TrafficStatsInterval: "5s"}},
	}); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	report, err := mod.TrafficReport(context.Background(), nil)
	if err != nil {
		t.Fatalf("TrafficReport() error = %v", err)
	}
	if !report.StatsPresent {
		t.Fatal("StatsPresent = false, want report produced with module metadata")
	}
	if report.RuntimeMetadata == nil {
		t.Fatal("RuntimeMetadata = nil, want last traffic stats report metadata")
	}
}

func TestModuleDescriptorProvidesTrafficSink(t *testing.T) {
	mod := trafficmodule.NewModule()

	descriptor := mod.Descriptor()
	if descriptor.Name != "traffic" {
		t.Fatalf("Descriptor().Name = %q, want traffic", descriptor.Name)
	}
	if len(descriptor.Provides) != 1 || descriptor.Provides[0] != module.ProviderTrafficSink {
		t.Fatalf("Descriptor().Provides = %+v, want traffic sink provider", descriptor.Provides)
	}
}

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

func TestGenerationModuleReportsOnlyActiveViewConfiguration(t *testing.T) {
	trafficmodule.Reset()
	trafficmodule.SetEnabled(false)
	t.Cleanup(func() {
		trafficmodule.SetEnabled(true)
		trafficmodule.Reset()
	})

	registry := module.NewRegistry()
	mod := trafficmodule.NewModule(trafficmodule.Config{EnabledSet: true, Enabled: false, GenerationSelector: registry})
	trafficmodule.AddHTTP(7, 11)
	mustRegisterTrafficTestModule(t, registry, mod)
	disabled := false
	first := model.Snapshot{Revision: 1, AgentConfig: model.AgentConfig{TrafficStatsEnabled: &disabled}}
	firstContext, err := module.NewGenerationContext(model.Snapshot{}, first)
	if err != nil {
		t.Fatalf("NewGenerationContext(first) error = %v", err)
	}
	firstCandidate, err := registry.PrepareGeneration(context.Background(), firstContext)
	if err != nil {
		t.Fatalf("PrepareGeneration(first) error = %v", err)
	}
	if err := firstCandidate.Ready(context.Background()); err != nil {
		t.Fatalf("Ready(first) error = %v", err)
	}
	firstCandidate.Publish()
	report, err := mod.TrafficReport(context.Background(), nil)
	if err != nil {
		t.Fatalf("TrafficReport(first) error = %v", err)
	}
	if !report.StatsPresent || len(report.Stats) != 0 {
		t.Fatalf("TrafficReport(first) = %+v, want active disabled view", report)
	}

	enabled := true
	second := model.Snapshot{Revision: 2, AgentConfig: model.AgentConfig{TrafficStatsEnabled: &enabled}}
	secondContext, err := module.NewGenerationContext(first, second)
	if err != nil {
		t.Fatalf("NewGenerationContext(second) error = %v", err)
	}
	secondCandidate, err := registry.PrepareGeneration(context.Background(), secondContext)
	if err != nil {
		t.Fatalf("PrepareGeneration(second) error = %v", err)
	}
	if err := secondCandidate.Ready(context.Background()); err != nil {
		t.Fatalf("Ready(second) error = %v", err)
	}
	report, err = mod.TrafficReport(context.Background(), nil)
	if err != nil || !report.StatsPresent || len(report.Stats) != 0 {
		t.Fatalf("TrafficReport(before second publish) = %+v, %v, want first disabled view", report, err)
	}
	secondCandidate.Publish()
	if leaked := trafficmodule.SnapshotNonZero(); leaked != nil {
		t.Fatalf("disabled generation traffic leaked after enable: %+v", leaked)
	}
	trafficmodule.AddHTTP(7, 11)
	report, err = mod.TrafficReport(context.Background(), nil)
	if err != nil {
		t.Fatalf("TrafficReport(second) error = %v", err)
	}
	if !report.StatsPresent || report.Stats["traffic"] == nil {
		t.Fatalf("TrafficReport(second) = %+v, want active enabled view", report)
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

func TestModuleRollbackAfterDisableKeepsLiveScopedRecordersAttached(t *testing.T) {
	tests := []struct {
		name        string
		newRecorder func(int) interface {
			Add(int64, int64)
			Flush()
		}
		bucket string
	}{
		{
			name: "http rule",
			newRecorder: func(id int) interface {
				Add(int64, int64)
				Flush()
			} {
				return trafficmodule.NewHTTPRuleRecorder(id)
			},
			bucket: "http_rules",
		},
		{
			name: "l4 rule",
			newRecorder: func(id int) interface {
				Add(int64, int64)
				Flush()
			} {
				return trafficmodule.NewL4RuleRecorder(id)
			},
			bucket: "l4_rules",
		},
		{
			name: "relay listener",
			newRecorder: func(id int) interface {
				Add(int64, int64)
				Flush()
			} {
				return trafficmodule.NewRelayListenerRecorder(id)
			},
			bucket: "relay_listeners",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			trafficmodule.Reset()
			trafficmodule.SetEnabled(true)
			t.Cleanup(func() {
				trafficmodule.SetEnabled(true)
				trafficmodule.Reset()
			})
			disabled := false
			recorder := tc.newRecorder(41)
			recorder.Add(3, 4)
			recorder.Flush()

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

			recorder.Add(5, 6)
			recorder.Flush()

			stats := trafficmodule.Snapshot()["traffic"].(map[string]any)
			scoped := stats[tc.bucket].(map[string]map[string]uint64)["41"]
			if scoped["rx_bytes"] != 8 || scoped["tx_bytes"] != 10 {
				t.Fatalf("%s[41] after rollback = %+v, want live recorder to stay attached", tc.bucket, scoped)
			}
		})
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
