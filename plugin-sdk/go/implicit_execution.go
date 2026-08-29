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

// RuntimeImplicitRemoteAgentExecution is the dual-face control-plane contract.
// The host auto-starts the management face. Saving configuration for an Agent
// on that page installs the execution face on that Agent only. The plugin UI
// does not configure the package onto the Agent as an HTTP backend. The
// embedded local management Agent is never an execution target.
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

// InstanceTargetsRemoteAgent reports whether plugin.call may address this
// Agent. Explicit targets remain an allowlist. Empty targets on an
// implicit-execution runtime allow the Agent that a management-page save
// addresses; they do not mean every Agent.
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
