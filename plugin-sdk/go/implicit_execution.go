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

// RuntimeProjectsControlPlaneRPC is the control-plane management-face
// contract. The host starts the rpc-service process and may reverse-proxy
// ui.route. wasm-policy-only packages never satisfy this, even when they
// list control-plane in host_scopes.
func RuntimeProjectsControlPlaneRPC(runtime Runtime) bool {
	return strings.TrimSpace(runtime.Kind) == RuntimeRPCService && RuntimeDeclaresHostScope(runtime, HostScopeControlPlane)
}

// RuntimeProjectsAgentPolicy is true when Agent PluginPolicies should include
// this package's wasm-policy face. wasm-policy-only packages stay on the agent
// host_scope. Dual-face rpc-service packages project a nested policy face
// without becoming Agent RPC.
func RuntimeProjectsAgentPolicy(runtime Runtime) bool {
	_, ok := RuntimeAgentPolicyFace(runtime)
	return ok
}

// RuntimeProjectsControlPlaneUIAndAgentPolicy is the one-package contract for
// a control-plane rpc-service ui.route face plus an Agent wasm-policy face.
func RuntimeProjectsControlPlaneUIAndAgentPolicy(runtime Runtime) bool {
	return RuntimeProjectsControlPlaneRPC(runtime) && runtime.Policy != nil && RuntimeProjectsAgentPolicy(runtime)
}

// RuntimeAgentPolicyFace returns the Agent wasm-policy identity. Nested dual-face
// policy entries keep their own ABI, entry, and budgets; wasm-policy-only
// packages project the primary runtime.
func RuntimeAgentPolicyFace(runtime Runtime) (RuntimePolicy, bool) {
	if strings.TrimSpace(runtime.Kind) == RuntimeWASMPolicy {
		if !RuntimeDeclaresHostScope(runtime, HostScopeAgent) {
			return RuntimePolicy{}, false
		}
		return RuntimePolicy{
			Kind:      RuntimeWASMPolicy,
			ABI:       strings.TrimSpace(runtime.ABI),
			HostScope: HostScopeAgent,
			Entry:     strings.TrimSpace(runtime.Entry),
		}, true
	}
	if runtime.Policy == nil {
		return RuntimePolicy{}, false
	}
	policy := *runtime.Policy
	if strings.TrimSpace(policy.Kind) != RuntimeWASMPolicy || strings.TrimSpace(policy.ABI) != PolicyABIV1 || strings.TrimSpace(policy.HostScope) != HostScopeAgent || strings.TrimSpace(policy.Entry) == "" {
		return RuntimePolicy{}, false
	}
	return policy, true
}

// ProjectAgentPolicy is the host PluginPolicies projection: identity plus the
// budget, failure policy, and policy-face extensions that Agent evaluation
// must enforce. Dual-face packages use the nested policy budgets and omit
// control-plane management-face extensions such as ui.route; wasm-policy-only
// packages use the primary manifest budgets.
func ProjectAgentPolicy(manifest Manifest) (AgentPolicyProjection, bool) {
	face, ok := RuntimeAgentPolicyFace(manifest.Runtime)
	if !ok {
		return AgentPolicyProjection{}, false
	}
	budget := manifest.ResourceBudget
	failure := manifest.FailurePolicy
	if manifest.Runtime.Policy != nil {
		budget = face.ResourceBudget
		failure = face.FailurePolicy
	}
	policyKind := strings.TrimSpace(manifest.Runtime.PolicyKind)
	return AgentPolicyProjection{
		Kind:            face.Kind,
		ABI:             face.ABI,
		HostScope:       face.HostScope,
		Entry:           face.Entry,
		PolicyKind:      policyKind,
		ExtensionPoints: projectAgentPolicyExtensionPoints(manifest.ExtensionPoints, policyKind),
		ResourceBudget:  budget,
		FailurePolicy:   failure,
	}, true
}

// AgentPolicyProjection is the wasm-policy face consumed by Agent
// PluginPolicies. It is not a second durable plugin_id.
type AgentPolicyProjection struct {
	Kind            string
	ABI             string
	HostScope       string
	Entry           string
	PolicyKind      string
	ExtensionPoints []string
	ResourceBudget  ResourceBudget
	FailurePolicy   FailurePolicy
}

// projectAgentPolicyExtensionPoints keeps only Agent wasm-policy face
// extensions. Dual-face packages list ui.route for the control-plane
// management face; Agent PolicyStage must not publish it. WAF stages admit
// http.request only.
func projectAgentPolicyExtensionPoints(extensionPoints []string, policyKind string) []string {
	projected := make([]string, 0, len(extensionPoints))
	seen := make(map[string]struct{}, len(extensionPoints))
	for _, point := range extensionPoints {
		point = strings.TrimSpace(point)
		switch point {
		case ExtensionHTTPRequest:
		case ExtensionL4Accept:
			if policyKind == "waf" {
				continue
			}
		default:
			continue
		}
		if _, exists := seen[point]; exists {
			continue
		}
		seen[point] = struct{}{}
		projected = append(projected, point)
	}
	return projected
}

// RuntimeImplicitRemoteAgentExecution is the dual-face control-plane contract.
// The host auto-starts the management face. Execution-face Agents are only
// those listed in explicit instance targets. The plugin UI does not configure
// the package onto the Agent as an HTTP backend. The embedded local
// management Agent is never an execution target.
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
// Agent. Explicit targets are an allowlist. Empty explicit targets do not
// allow arbitrary remotes; plugin.call only addresses Agents already in
// explicit targets.
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
	return false
}

// AgentExecutionFace is true when this process has no control-plane host
// runtime endpoint. Dual-face packages skip UI on that face so the Agent
// listener does not race the management page.
func AgentExecutionFace() bool {
	return strings.TrimSpace(os.Getenv(EnvPluginHostEndpoint)) == ""
}
