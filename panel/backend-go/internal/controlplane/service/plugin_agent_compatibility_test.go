//go:build exhaustive && !integration

package service

import (
	"encoding/json"
	"runtime"
	"strings"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

func TestValidateAgentTargetsAllowsDevelopmentVersionWithRequiredCapability(t *testing.T) {
	store := newServiceOwnerStore(t)
	if err := store.SaveAgent(t.Context(), storage.AgentRow{
		ID:               "development-agent",
		Name:             "development-agent",
		Version:          "1",
		Platform:         runtime.GOOS + "-" + runtime.GOARCH,
		CapabilitiesJSON: `["package_manifest_v1"]`,
	}); err != nil {
		t.Fatal(err)
	}

	service := NewPluginService(store, t.TempDir())
	if err := service.validateAgentTargets(t.Context(), ">=2.0.0", json.RawMessage(`["development-agent"]`)); err != nil {
		t.Fatalf("validateAgentTargets() rejected a capable development agent: %v", err)
	}
}

func TestValidateAgentTargetsStillEnforcesConcreteReleaseCompatibility(t *testing.T) {
	store := newServiceOwnerStore(t)
	if err := store.SaveAgent(t.Context(), storage.AgentRow{
		ID:               "release-agent",
		Name:             "release-agent",
		Version:          "1.0.0",
		Platform:         runtime.GOOS + "-" + runtime.GOARCH,
		CapabilitiesJSON: `["package_manifest_v1"]`,
	}); err != nil {
		t.Fatal(err)
	}

	service := NewPluginService(store, t.TempDir())
	err := service.validateAgentTargets(t.Context(), ">=2.0.0", json.RawMessage(`["release-agent"]`))
	if err == nil || !strings.Contains(err.Error(), "outside >=2.0.0") {
		t.Fatalf("validateAgentTargets() error = %v, want release compatibility rejection", err)
	}
}

func TestValidateAgentTargetsStillRequiresCapabilityForDevelopmentVersion(t *testing.T) {
	store := newServiceOwnerStore(t)
	if err := store.SaveAgent(t.Context(), storage.AgentRow{
		ID:               "incapable-development-agent",
		Name:             "incapable-development-agent",
		Version:          "dev",
		Platform:         runtime.GOOS + "-" + runtime.GOARCH,
		CapabilitiesJSON: `[]`,
	}); err != nil {
		t.Fatal(err)
	}

	service := NewPluginService(store, t.TempDir())
	err := service.validateAgentTargets(t.Context(), "*", json.RawMessage(`["incapable-development-agent"]`))
	if err == nil || !strings.Contains(err.Error(), "lacks package_manifest_v1 capability") {
		t.Fatalf("validateAgentTargets() error = %v, want capability rejection", err)
	}
}
