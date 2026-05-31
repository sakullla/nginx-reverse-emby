package l4_test

import (
	"context"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
	moduleegress "github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/egress"
	l4module "github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/l4"
)

func TestModuleAppliesL4RuleAndProvidesDiagnosticsSource(t *testing.T) {
	profileID := 42
	mod := l4module.NewModule(l4module.Config{})
	registry := module.NewRegistry()
	mustRegister(t, registry, moduleegress.NewModule(nil))
	mustRegister(t, registry, mod)

	next := model.Snapshot{
		EgressProfiles: []model.EgressProfile{{
			ID:      profileID,
			Name:    "direct-final-hop",
			Type:    "direct",
			Enabled: true,
		}},
		L4Rules: []model.L4Rule{{
			ID:              1,
			Protocol:        "tcp",
			ListenHost:      "127.0.0.1",
			ListenPort:      0,
			Backends:        []model.L4Backend{{Host: "127.0.0.1", Port: 1}},
			EgressProfileID: &profileID,
			Enabled:         true,
		}},
	}
	if err := registry.Apply(context.Background(), model.Snapshot{}, next); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if _, ok := registry.Resolve(module.ProviderDiagnosticsL4Source); !ok {
		t.Fatal("diagnostics.l4.source provider missing")
	}
}

func mustRegister(t *testing.T, registry *module.Registry, mod any) {
	t.Helper()
	if err := registry.Register(mod); err != nil {
		t.Fatalf("Register(%T) error = %v", mod, err)
	}
}
