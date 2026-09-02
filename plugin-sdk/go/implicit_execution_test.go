package pluginsdk

import (
	"testing"
)

func TestRuntimeImplicitRemoteAgentExecution(t *testing.T) {
	t.Parallel()
	dual := Runtime{
		Kind: RuntimeRPCService, ABI: RPCABIV1,
		HostScope: HostScopeControlPlane, HostScopes: []string{HostScopeAgent},
	}
	if !RuntimeProjectsAgentRPC(dual) || !RuntimeImplicitRemoteAgentExecution(dual) {
		t.Fatalf("dual-face runtime = %+v", dual)
	}
	agentOnly := Runtime{Kind: RuntimeRPCService, HostScope: HostScopeAgent}
	if !RuntimeProjectsAgentRPC(agentOnly) || RuntimeImplicitRemoteAgentExecution(agentOnly) {
		t.Fatalf("agent-only runtime = %+v", agentOnly)
	}
	controlOnly := Runtime{Kind: RuntimeRPCService, HostScope: HostScopeControlPlane}
	if RuntimeProjectsAgentRPC(controlOnly) || RuntimeImplicitRemoteAgentExecution(controlOnly) {
		t.Fatalf("control-plane-only runtime = %+v", controlOnly)
	}
	wasm := Runtime{Kind: RuntimeWASMPolicy, HostScope: HostScopeAgent, HostScopes: []string{HostScopeControlPlane}}
	if RuntimeProjectsAgentRPC(wasm) || RuntimeImplicitRemoteAgentExecution(wasm) {
		t.Fatalf("wasm runtime = %+v", wasm)
	}
	if RuntimeProjectsControlPlaneRPC(wasm) || RuntimeProjectsControlPlaneUIAndAgentPolicy(wasm) {
		t.Fatalf("wasm-policy-only became a control-plane process: %+v", wasm)
	}
	if !RuntimeProjectsAgentPolicy(wasm) {
		t.Fatalf("wasm-policy-only lost the agent policy face: %+v", wasm)
	}
}

func TestRuntimeProjectsControlPlaneUIAndAgentPolicy(t *testing.T) {
	t.Parallel()
	dual := Runtime{
		Kind: RuntimeRPCService, ABI: RPCABIV1,
		HostScope: HostScopeControlPlane, Entry: "plugin", PolicyKind: "waf",
		Policy: &RuntimePolicy{
			Kind: RuntimeWASMPolicy, ABI: PolicyABIV1, HostScope: HostScopeAgent,
			Entry: "artifacts/waf.wasm",
		},
	}
	if RuntimeProjectsAgentRPC(dual) || RuntimeImplicitRemoteAgentExecution(dual) {
		t.Fatalf("waf dual-face must not project Agent RPC: %+v", dual)
	}
	if !RuntimeProjectsControlPlaneRPC(dual) || !RuntimeProjectsAgentPolicy(dual) || !RuntimeProjectsControlPlaneUIAndAgentPolicy(dual) {
		t.Fatalf("waf dual-face runtime = %+v", dual)
	}
	face, ok := RuntimeAgentPolicyFace(dual)
	if !ok || face.Entry != "artifacts/waf.wasm" || face.ABI != PolicyABIV1 || face.HostScope != HostScopeAgent {
		t.Fatalf("waf dual-face policy face = %+v ok=%v", face, ok)
	}
	rpcOnly := Runtime{Kind: RuntimeRPCService, ABI: RPCABIV1, HostScope: HostScopeControlPlane, Entry: "plugin"}
	if RuntimeProjectsAgentPolicy(rpcOnly) || RuntimeProjectsControlPlaneUIAndAgentPolicy(rpcOnly) {
		t.Fatalf("rpc-only runtime projected a policy face: %+v", rpcOnly)
	}
	agentRPC := Runtime{Kind: RuntimeRPCService, HostScope: HostScopeAgent, PolicyKind: "waf"}
	if RuntimeProjectsControlPlaneUIAndAgentPolicy(agentRPC) {
		t.Fatalf("agent rpc-service became a control-plane UI+policy package: %+v", agentRPC)
	}
}

func TestInstanceTargetsRemoteAgent(t *testing.T) {
	t.Parallel()
	dual := Runtime{
		Kind: RuntimeRPCService, HostScope: HostScopeControlPlane, HostScopes: []string{HostScopeAgent},
	}
	controlOnly := Runtime{Kind: RuntimeRPCService, HostScope: HostScopeControlPlane}

	if InstanceTargetsRemoteAgent(dual, nil, "edge-a", "local") {
		t.Fatal("empty dual-face targets must not allow arbitrary remotes")
	}
	if InstanceTargetsRemoteAgent(dual, []string{}, "edge-a", "local") {
		t.Fatal("empty explicit target list must not allow plugin.call")
	}
	if InstanceTargetsRemoteAgent(dual, nil, "local", "local") {
		t.Fatal("empty dual-face targets must skip the embedded local Agent")
	}
	if InstanceTargetsRemoteAgent(dual, nil, "", "local") {
		t.Fatal("empty agent id must not match")
	}
	if InstanceTargetsRemoteAgent(controlOnly, nil, "edge-a", "local") {
		t.Fatal("control-plane-only empty targets must not imply Agent execution")
	}
	if InstanceTargetsRemoteAgent(dual, []string{"edge-b"}, "edge-a", "local") {
		t.Fatal("explicit targets must remain an allowlist")
	}
	if !InstanceTargetsRemoteAgent(dual, []string{"edge-a"}, "edge-a", "local") {
		t.Fatal("explicit target must include that Agent")
	}
	if !InstanceTargetsRemoteAgent(agentOnlyRuntime(), []string{"local"}, "local", "local") {
		t.Fatal("explicit local target on an agent-only plugin must still match")
	}
}

func agentOnlyRuntime() Runtime {
	return Runtime{Kind: RuntimeRPCService, HostScope: HostScopeAgent}
}

func TestAgentExecutionFace(t *testing.T) {
	t.Setenv(EnvPluginHostEndpoint, "")
	if !AgentExecutionFace() {
		t.Fatal("missing host runtime endpoint must be the Agent execution face")
	}
	t.Setenv(EnvPluginHostEndpoint, "unix:/run/nre-plugin/host.sock")
	if AgentExecutionFace() {
		t.Fatal("control-plane host runtime endpoint still classified as Agent face")
	}
}
