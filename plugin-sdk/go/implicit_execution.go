package pluginsdk

import (
	"os"
	"strings"
)

// RuntimeProjectsAgentRPC is the Agent execution-face RPC contract. WASM
// policy stays on the separate PluginPolicies projection.
func RuntimeProjectsAgentRPC(runtime Runtime) bool {
	return strings.TrimSpace(runtime.Kind) == RuntimeRPCService && RuntimeDeclaresHostScope(runtime, HostScopeAgent)
}

// RuntimeImplicitRemoteAgentExecution is the dual-face control-plane contract:
// empty instance targets deliver the Agent execution face to every remote
// Agent. The embedded local management Agent is never an implicit target.
// Plugins declare this by host_scope: control-plane plus host_scopes: [agent];
// they do not list Agents in instance TargetJSON.
func RuntimeImplicitRemoteAgentExecution(runtime Runtime) bool {
	return RuntimeProjectsAgentRPC(runtime) && RuntimeDeclaresHostScope(runtime, HostScopeControlPlane)
}

// ImplicitRemoteAgentExecutionSkipsAgent excludes the empty id and the
// control-plane's embedded local Agent from implicit execution delivery.
func ImplicitRemoteAgentExecutionSkipsAgent(localAgentID, agentID string) bool {
	agentID = strings.TrimSpace(agentID)
	localAgentID = strings.TrimSpace(localAgentID)
	return agentID == "" || (localAgentID != "" && agentID == localAgentID)
}

// InstanceTargetsRemoteAgent reports whether plugin.call and Agent generation
// projection include this Agent. Explicit targets remain an allowlist. Empty
// targets on an implicit-execution runtime mean every remote Agent.
func InstanceTargetsRemoteAgent(runtime Runtime, explicitTargets []string, agentID, localAgentID string) bool {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return false
	}
	for _, target := range explicitTargets {
		if strings.TrimSpace(target) == agentID {
			return true
		}
	}
	if len(explicitTargets) > 0 {
		return false
	}
	if !RuntimeImplicitRemoteAgentExecution(runtime) {
		return false
	}
	return !ImplicitRemoteAgentExecutionSkipsAgent(localAgentID, agentID)
}

// AgentExecutionFace is true when this process has no control-plane host
// runtime endpoint. Dual-face packages skip UI on that face so the Agent
// listener does not race the management page.
func AgentExecutionFace() bool {
	return strings.TrimSpace(os.Getenv(EnvPluginHostEndpoint)) == ""
}
