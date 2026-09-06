package pluginsdk

import (
	"errors"
	"os"
	"sort"
	"strings"
)

// EnvPluginExecutionScope is authored by the Host process launcher. User
// configuration must not override it; it selects a client face, not authority.
const EnvPluginExecutionScope = "NRE_PLUGIN_EXECUTION_SCOPE"

// ResolveExecutionScope strictly interprets a Host-authored scope. The legacy
// endpoint heuristic applies only when the environment key is absent, not empty.
func ResolveExecutionScope(value string, present bool, legacyEndpoint string) (string, error) {
	if present {
		if value != HostScopeAgent && value != HostScopeControlPlane {
			return "", errors.New("invalid explicit plugin execution scope")
		}
		return value, nil
	}
	if legacyEndpoint == "" {
		return HostScopeAgent, nil
	}
	return HostScopeControlPlane, nil
}
func ExecutionScopeFromEnvironment() (string, error) {
	value, present := os.LookupEnv(EnvPluginExecutionScope)
	return ResolveExecutionScope(value, present, stringsTrimLegacyEndpoint())
}
func stringsTrimLegacyEndpoint() string { return strings.TrimSpace(os.Getenv(EnvPluginHostEndpoint)) }

// ValidateExecutionScopeEnvironment is available to entrypoints which surface
// startup errors; AgentExecutionFace fails closed when explicit scope is invalid.
func ValidateExecutionScopeEnvironment() error { _, err := ExecutionScopeFromEnvironment(); return err }

// ExecutionTargetSelection addresses an execution instance's real Host-resolved
// targets. Effective does not invent remotes for an empty RPC target list, and
// a policy's Host-projected all-agent set may include the embedded local Agent.
type ExecutionTargetSelection struct {
	Mode     string   `json:"mode"`
	AgentIDs []string `json:"agent_ids,omitempty"`
}

const (
	ExecutionTargetsEffective = "effective"
	ExecutionTargetsSubset    = "subset"
	ExecutionTargetMaxAgents  = 256
)

func (selection ExecutionTargetSelection) Validate() error {
	switch selection.Mode {
	case ExecutionTargetsEffective:
		if len(selection.AgentIDs) != 0 {
			return errors.New("effective targets must not contain an explicit subset")
		}
	case ExecutionTargetsSubset:
		if len(selection.AgentIDs) == 0 {
			return errors.New("target subset is empty")
		}
	default:
		return errors.New("target selection mode is invalid")
	}
	if len(selection.AgentIDs) > ExecutionTargetMaxAgents {
		return errors.New("target selection exceeds bound")
	}
	previous := ""
	for _, id := range selection.AgentIDs {
		if ValidatePolicyIdentity(id) != nil || id == "*" || id <= previous {
			return errors.New("target IDs must be explicit unique sorted identities")
		}
		previous = id
	}
	return nil
}
func ResolveExecutionTargets(selection ExecutionTargetSelection, effective, granted []string) ([]string, error) {
	if err := selection.Validate(); err != nil {
		return nil, err
	}
	if len(effective) > ExecutionTargetMaxAgents || len(granted) > ExecutionTargetMaxAgents {
		return nil, errors.New("Host target authority exceeds bound")
	}
	actual, allowed := map[string]bool{}, map[string]bool{}
	for _, id := range effective {
		if ValidatePolicyIdentity(id) != nil || id == "*" || actual[id] {
			return nil, errors.New("invalid effective target identity")
		}
		actual[id] = true
	}
	for _, id := range granted {
		if ValidatePolicyIdentity(id) != nil || id == "*" || allowed[id] {
			return nil, errors.New("invalid granted target identity")
		}
		allowed[id] = true
	}
	result := append([]string(nil), selection.AgentIDs...)
	if selection.Mode == ExecutionTargetsEffective {
		result = append([]string(nil), effective...)
		sort.Strings(result)
	}
	for _, id := range result {
		if !actual[id] || !allowed[id] {
			return nil, &RuntimeError{Code: ErrorPermissionDenied, Message: "execution target is not authorized for this instance"}
		}
	}
	return result, nil
}
