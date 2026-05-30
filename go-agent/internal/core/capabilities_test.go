package core

import (
	"context"
	"reflect"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/config"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
)

func TestCapabilitiesPreserveExistingAdvertisedValues(t *testing.T) {
	cfg := config.Default()
	cfg.WireGuardEnabled = true
	cfg.WireGuardExplicit = true
	cfg.HTTP3Enabled = true

	got := CapabilityNames(cfg, nil)
	want := []string{"http_rules", "cert_install", "local_acme", "l4", "relay_quic", "wireguard", "egress_profiles", "http3_ingress"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("CapabilityNames() = %+v, want %+v", got, want)
	}
}

func TestCapabilitiesAppendModuleCapabilitiesInRegistryOrder(t *testing.T) {
	cfg := config.Default()
	cfg.WireGuardEnabled = false
	cfg.WireGuardExplicit = true
	registry := module.NewRegistry()
	_ = registry.Register(staticModule{name: "traffic", capabilities: []module.Capability{
		{Name: "traffic_stats", Enabled: true},
		{Name: " ", Enabled: true},
		{Name: "disabled_capability", Enabled: false},
	}})
	_ = registry.Register(staticModule{name: "certs", capabilities: []module.Capability{
		{Name: "managed_certs", Enabled: true},
	}})

	got := CapabilityNames(cfg, registry)
	want := []string{"http_rules", "cert_install", "local_acme", "l4", "relay_quic", "egress_profiles", "traffic_stats", "managed_certs"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("CapabilityNames() = %+v, want %+v", got, want)
	}
}

type staticModule struct {
	name         string
	capabilities []module.Capability
}

func (m staticModule) Name() string { return m.name }

func (m staticModule) Capabilities() []module.Capability {
	return append([]module.Capability(nil), m.capabilities...)
}

func (m staticModule) Health(context.Context) module.Health { return module.Health{} }

func (m staticModule) Start(context.Context, model.Snapshot) error { return nil }

func (m staticModule) Stop(context.Context) error { return nil }
