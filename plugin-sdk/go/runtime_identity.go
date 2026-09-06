package pluginsdk

import (
	"errors"
	"os"
	"strings"
)

const (
	// EnvPluginInstanceID is authored by the Host process launcher. It names
	// the current durable plugin instance for ManagedBinding claims; it does
	// not grant access to that instance or to any Host resource.
	EnvPluginInstanceID = "NRE_PLUGIN_INSTANCE_ID"

	PermissionRuntimeIdentity                = "runtime.identity"
	CapabilityRuntimeIdentity HostCapability = PermissionRuntimeIdentity
)

// ResolvePluginInstanceID strictly validates a Host-authored instance ID.
// Opted-in runtimes fail when the variable is absent instead of guessing an
// identity from user configuration, endpoints, or process arguments.
func ResolvePluginInstanceID(value string, present bool) (string, error) {
	if !present {
		return "", errors.New("plugin instance identity is missing")
	}
	if value == "" || value != strings.TrimSpace(value) || value == "*" || ValidatePolicyIdentity(value) != nil {
		return "", errors.New("plugin instance identity is invalid")
	}
	return value, nil
}

func PluginInstanceIDFromEnvironment() (string, error) {
	value, present := os.LookupEnv(EnvPluginInstanceID)
	return ResolvePluginInstanceID(value, present)
}
