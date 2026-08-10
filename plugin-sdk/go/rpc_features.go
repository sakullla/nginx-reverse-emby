package pluginsdk

import (
	"errors"
	"fmt"
	"strings"
)

const RPCFeatureDurableActionsV1 = "rpc.durable-actions.v1"

// RequiredRPCFeatures projects protocol extensions from signed/granted
// scopes. Older v1 guests remain compatible when no extension is required.
func RequiredRPCFeatures(scopes []string) []string {
	for _, scope := range scopes {
		switch HostCapability(strings.TrimSpace(scope)) {
		case CapabilityServiceRevocableResourceHandle, CapabilityUIDynamicActions:
			return []string{RPCFeatureDurableActionsV1}
		}
	}
	return nil
}

// ValidateRPCFeatures requires an exact, canonical acknowledgement of every
// requested extension. Unrequested extensions are rejected so protocol state
// cannot be smuggled through a capability list.
func ValidateRPCFeatures(required, provided []string) error {
	want := make(map[string]struct{}, len(required))
	for _, feature := range required {
		if feature != strings.TrimSpace(feature) || feature != RPCFeatureDurableActionsV1 {
			return fmt.Errorf("unsupported required RPC feature %q", feature)
		}
		if _, exists := want[feature]; exists {
			return fmt.Errorf("duplicate required RPC feature %q", feature)
		}
		want[feature] = struct{}{}
	}
	seen := make(map[string]struct{}, len(provided))
	for _, feature := range provided {
		if feature != strings.TrimSpace(feature) || feature == "" {
			return errors.New("RPC feature acknowledgement is not canonical")
		}
		if _, exists := want[feature]; !exists {
			return fmt.Errorf("unrequested RPC feature %q", feature)
		}
		if _, exists := seen[feature]; exists {
			return fmt.Errorf("duplicate RPC feature acknowledgement %q", feature)
		}
		seen[feature] = struct{}{}
	}
	for feature := range want {
		if _, exists := seen[feature]; !exists {
			return fmt.Errorf("required RPC feature %q was not acknowledged", feature)
		}
	}
	return nil
}
