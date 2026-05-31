package traffic_test

import (
	"context"
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
