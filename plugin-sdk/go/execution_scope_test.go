package pluginsdk

import (
	"context"
	"io"
	"os"
	"reflect"
	"testing"
)

func TestExecutionScopeOverridesEndpointAndRejectsInvalidValues(t *testing.T) {
	for _, test := range []struct {
		scope          string
		present        bool
		endpoint, want string
		invalid        bool
	}{
		{HostScopeAgent, true, "unix:/host.sock", HostScopeAgent, false}, {HostScopeControlPlane, true, "", HostScopeControlPlane, false}, {"", false, "", HostScopeAgent, false}, {"", false, "unix:/host.sock", HostScopeControlPlane, false}, {"", true, "", "", true}, {" agent", true, "", "", true}, {"node", true, "unix:/host.sock", "", true},
	} {
		got, err := ResolveExecutionScope(test.scope, test.present, test.endpoint)
		if (err != nil) != test.invalid || got != test.want {
			t.Fatalf("scope parsing %+v => %q %v", test, got, err)
		}
	}
	t.Setenv(EnvPluginHostEndpoint, "unix:/agent-owned/host.sock")
	t.Setenv(EnvPluginExecutionScope, HostScopeAgent)
	if !AgentExecutionFace() {
		t.Fatal("managed Agent misclassified as management face")
	}
	t.Setenv(EnvPluginExecutionScope, "invalid")
	if AgentExecutionFace() {
		t.Fatal("invalid explicit scope became Agent")
	}
	if RunRPCEntrypoint(context.Background(), nil, io.Discard, RPCEntrypointConfig{}) == nil {
		t.Fatal("invalid execution scope reached entrypoint")
	}
	if err := os.Unsetenv(EnvPluginExecutionScope); err != nil {
		t.Fatal(err)
	}
	if AgentExecutionFace() {
		t.Fatal("legacy endpoint fallback changed")
	}
}
func TestExecutionTargetsFollowActualProjectionIncludingExplicitLocal(t *testing.T) {
	effective := ExecutionTargetSelection{Mode: ExecutionTargetsEffective}
	if got, err := ResolveExecutionTargets(effective, nil, nil); err != nil || len(got) != 0 {
		t.Fatal("empty RPC targets invented Agent", got, err)
	}
	got, err := ResolveExecutionTargets(effective, []string{"remote", "local"}, []string{"local", "remote"})
	if err != nil || !reflect.DeepEqual(got, []string{"local", "remote"}) {
		t.Fatal("policy all-Agent projection omitted local", got, err)
	}
	subset := ExecutionTargetSelection{Mode: ExecutionTargetsSubset, AgentIDs: []string{"local"}}
	if got, err := ResolveExecutionTargets(subset, []string{"local", "remote"}, []string{"local"}); err != nil || len(got) != 1 {
		t.Fatal("explicit local rejected", err)
	}
	if _, err := ResolveExecutionTargets(effective, []string{"local", "remote"}, []string{"local"}); err == nil {
		t.Fatal("effective silently narrowed missing target grant")
	}
	for _, selection := range []ExecutionTargetSelection{{Mode: ExecutionTargetsSubset}, {Mode: ExecutionTargetsSubset, AgentIDs: []string{"*"}}, {Mode: ExecutionTargetsSubset, AgentIDs: []string{"local", "local"}}, {Mode: ExecutionTargetsSubset, AgentIDs: []string{"foreign"}}} {
		if _, err := ResolveExecutionTargets(selection, []string{"local"}, []string{"local"}); err == nil {
			t.Fatalf("invalid target selection accepted: %+v", selection)
		}
	}
}
func TestExecutionScopeFeatureNegotiationIsAdditive(t *testing.T) {
	if len(RequiredRPCFeatures(nil)) != 0 {
		t.Fatal("legacy empty grants require new protocol")
	}
	required, err := RequiredRPCFeaturesForExecutionScope(nil, nil, HostScopeAgent)
	if err != nil || !reflect.DeepEqual(required, []string{RPCFeatureExecutionScopeV1}) {
		t.Fatal(required, err)
	}
	if ValidateRPCFeatures(required, nil) == nil {
		t.Fatal("old guest accepted explicit scope it cannot interpret")
	}
	if ValidateRPCFeatures(required, RPCFeaturesWithExecutionScope(nil)) != nil {
		t.Fatal("updated guest cannot acknowledge scope")
	}
}
