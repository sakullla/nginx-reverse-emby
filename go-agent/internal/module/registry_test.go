package module

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

type testModule struct {
	name         string
	capabilities []Capability
	health       Health
	startErr     error
	stopErr      error
	starts       int
	stops        int
	events       *[]string
}

func (m *testModule) Name() string { return m.name }

func (m *testModule) Capabilities() []Capability {
	return append([]Capability(nil), m.capabilities...)
}

func (m *testModule) Health(context.Context) Health { return m.health }

func (m *testModule) Start(context.Context, model.Snapshot) error {
	m.starts++
	if m.events != nil {
		*m.events = append(*m.events, "start:"+strings.TrimSpace(m.name))
	}
	return m.startErr
}

func (m *testModule) Stop(context.Context) error {
	m.stops++
	if m.events != nil {
		*m.events = append(*m.events, "stop:"+strings.TrimSpace(m.name))
	}
	return m.stopErr
}

func TestRegistryOrdersModulesAndAggregatesCapabilities(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register(&testModule{name: "traffic", capabilities: []Capability{{Name: "traffic_stats"}}}); err != nil {
		t.Fatalf("Register traffic: %v", err)
	}
	if err := registry.Register(&testModule{name: "wireguard", capabilities: []Capability{{Name: "wireguard"}}}); err != nil {
		t.Fatalf("Register wireguard: %v", err)
	}

	if got, want := registry.Names(), []string{"traffic", "wireguard"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Names() = %+v, want %+v", got, want)
	}
	if got, want := capabilityNames(registry.Capabilities()), []string{"traffic_stats", "wireguard"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Capabilities() = %+v, want %+v", got, want)
	}
}

func TestRegistryRejectsNilBlankAndDuplicateNames(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register(nil); !errors.Is(err, ErrInvalidModule) {
		t.Fatalf("nil Register() error = %v, want ErrInvalidModule", err)
	}
	if err := registry.Register(&testModule{name: " \t\n "}); !errors.Is(err, ErrInvalidModule) {
		t.Fatalf("blank Register() error = %v, want ErrInvalidModule", err)
	}
	if err := registry.Register(&testModule{name: "wireguard"}); err != nil {
		t.Fatalf("Register wireguard: %v", err)
	}
	if err := registry.Register(&testModule{name: " WireGuard "}); !errors.Is(err, ErrDuplicateModule) {
		t.Fatalf("duplicate Register() error = %v, want ErrDuplicateModule", err)
	}
}

func TestRegistryReturnsDefensiveSlices(t *testing.T) {
	registry := NewRegistry()
	first := &testModule{name: "certs", capabilities: []Capability{{Name: "cert_install", Metadata: map[string]string{"scope": "local"}}}}
	second := &testModule{name: "traffic", capabilities: []Capability{{Name: "traffic_stats"}}}
	if err := registry.Register(first); err != nil {
		t.Fatalf("Register certs: %v", err)
	}
	if err := registry.Register(second); err != nil {
		t.Fatalf("Register traffic: %v", err)
	}

	modules := registry.Modules()
	modules[0] = second
	if got, want := registry.Names(), []string{"certs", "traffic"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Names() after mutating Modules() = %+v, want %+v", got, want)
	}

	names := registry.Names()
	names[0] = "changed"
	if got, want := registry.Names(), []string{"certs", "traffic"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Names() after mutating Names() = %+v, want %+v", got, want)
	}

	capabilities := registry.Capabilities()
	capabilities[0].Name = "changed"
	if got, want := capabilityNames(registry.Capabilities()), []string{"cert_install", "traffic_stats"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Capabilities() after mutating result = %+v, want %+v", got, want)
	}

	capabilities = registry.Capabilities()
	capabilities[0].Metadata["scope"] = "changed"
	got := registry.Capabilities()
	if got[0].Metadata["scope"] != "local" {
		t.Fatalf("Capabilities()[0].Metadata after mutating result = %+v, want original metadata", got[0].Metadata)
	}
}

func TestRegistryStartAllStartsInRegistrationOrderRollsBackAndWrapsErrors(t *testing.T) {
	var events []string
	startErr := errors.New("cannot start")
	first := &testModule{name: "certs", events: &events}
	second := &testModule{name: " traffic ", startErr: startErr, events: &events}
	third := &testModule{name: "wireguard", events: &events}
	registry := NewRegistry()
	_ = registry.Register(first)
	_ = registry.Register(second)
	_ = registry.Register(third)

	err := registry.StartAll(context.Background(), model.Snapshot{Revision: 7})
	if !errors.Is(err, startErr) {
		t.Fatalf("StartAll() error = %v, want wrapped startErr", err)
	}
	if err == nil || !strings.Contains(err.Error(), "module traffic start") {
		t.Fatalf("StartAll() error = %v, want module name context", err)
	}
	if got, want := events, []string{"start:certs", "start:traffic", "stop:certs"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("start events = %+v, want %+v", got, want)
	}
	if third.starts != 0 {
		t.Fatalf("third starts = %d, want 0 after earlier error", third.starts)
	}
}

func TestRegistryStartAllIncludesRollbackErrorWithoutHidingStartError(t *testing.T) {
	var events []string
	startErr := errors.New("cannot start")
	rollbackErr := errors.New("cannot roll back")
	first := &testModule{name: "certs", stopErr: rollbackErr, events: &events}
	second := &testModule{name: "traffic", startErr: startErr, events: &events}
	registry := NewRegistry()
	_ = registry.Register(first)
	_ = registry.Register(second)

	err := registry.StartAll(context.Background(), model.Snapshot{})
	if !errors.Is(err, startErr) {
		t.Fatalf("StartAll() error = %v, want wrapped startErr", err)
	}
	if !errors.Is(err, rollbackErr) {
		t.Fatalf("StartAll() error = %v, want wrapped rollbackErr", err)
	}
	if err == nil || !strings.Contains(err.Error(), "module traffic start") || !strings.Contains(err.Error(), "rollback module certs stop") {
		t.Fatalf("StartAll() error = %v, want start and rollback context", err)
	}
	if got, want := events, []string{"start:certs", "start:traffic", "stop:certs"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("events = %+v, want %+v", got, want)
	}
}

func TestRegistryStopAllStopsInReverseOrderAndWrapsErrors(t *testing.T) {
	var events []string
	stopErr := errors.New("cannot stop")
	first := &testModule{name: "certs", events: &events}
	second := &testModule{name: " traffic ", stopErr: stopErr, events: &events}
	third := &testModule{name: "wireguard", events: &events}
	registry := NewRegistry()
	_ = registry.Register(first)
	_ = registry.Register(second)
	_ = registry.Register(third)

	err := registry.StopAll(context.Background())
	if !errors.Is(err, stopErr) {
		t.Fatalf("StopAll() error = %v, want wrapped stopErr", err)
	}
	if err == nil || !strings.Contains(err.Error(), "module traffic stop") {
		t.Fatalf("StopAll() error = %v, want module name context", err)
	}
	if got, want := events, []string{"stop:wireguard", "stop:traffic", "stop:certs"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("stop events = %+v, want %+v", got, want)
	}
}

func capabilityNames(capabilities []Capability) []string {
	names := make([]string, 0, len(capabilities))
	for _, capability := range capabilities {
		names = append(names, capability.Name)
	}
	return names
}
