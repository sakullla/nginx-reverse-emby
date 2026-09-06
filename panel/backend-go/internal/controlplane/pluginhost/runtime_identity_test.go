package pluginhost

import (
	"strings"
	"testing"

	sdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

func TestControlPlaneRuntimeIdentityEnvironmentRequiresOptIn(t *testing.T) {
	legacy := Candidate{InstanceID: "legacy-instance"}
	if environment, err := candidateRuntimeIdentityEnvironment(legacy); err != nil || environment != nil {
		t.Fatalf("legacy runtime identity environment = %v, %v", environment, err)
	}
	opted := Candidate{
		InstanceID:       "first-instance",
		RequiredFeatures: []string{sdk.RPCFeatureRuntimeIdentityV1},
		Identity:         Identity{Scopes: []string{string(sdk.CapabilityRuntimeIdentity)}},
		Grants:           []string{string(sdk.CapabilityRuntimeIdentity)},
	}
	first, err := candidateRuntimeIdentityEnvironment(opted)
	if err != nil || len(first) != 1 || first[0] != sdk.EnvPluginInstanceID+"=first-instance" {
		t.Fatalf("opted runtime identity environment = %v, %v", first, err)
	}
	opted.InstanceID = "second-instance"
	second, err := candidateRuntimeIdentityEnvironment(opted)
	if err != nil || second[0] != sdk.EnvPluginInstanceID+"=second-instance" || first[0] == second[0] {
		t.Fatalf("second runtime identity environment = %v, %v", second, err)
	}
	opted.Identity.Scopes = nil
	if _, err := candidateRuntimeIdentityEnvironment(opted); err == nil {
		t.Fatal("unsigned runtime identity feature was accepted")
	}
}

func TestControlPlaneRuntimeIdentityRejectsUserOverride(t *testing.T) {
	generated := []string{sdk.EnvPluginInstanceID + "=host-instance"}
	if _, err := buildPluginEnvironment([]string{sdk.EnvPluginInstanceID + "=user-instance"}, generated); err == nil || !strings.Contains(err.Error(), "reserved") {
		t.Fatalf("user runtime identity override error = %v", err)
	}
	environment, err := buildPluginEnvironment(nil, generated)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, entry := range environment {
		found = found || entry == generated[0]
	}
	if !found {
		t.Fatalf("Host runtime identity missing from process environment: %v", environment)
	}
}
