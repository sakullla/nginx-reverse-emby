package pluginsdk

import (
	"errors"
	"fmt"
	"strings"
)

const (
	RPCFeatureDurableActionsV1 = "rpc.durable-actions.v1"
	RPCFeatureDatasetsV1       = "rpc.datasets.v1"
	RPCFeatureDatasetResolveV1 = "rpc.dataset-resolve.v1"
	RPCFeatureManagedNetworkV1 = "rpc.managed-network.v1"
	RPCFeatureScopedSecretsV1  = "rpc.scoped-secrets.v1"
	RPCFeaturePolicyControlsV1 = "rpc.policy-controls.v1"
	RPCFeatureExecutionScopeV1 = "rpc.execution-scope.v1"
)

// RequiredRPCFeatures projects protocol extensions from signed/granted
// scopes. Older v1 guests remain compatible when no extension is required.
func RequiredRPCFeatures(scopes []string) []string {
	features := make([]string, 0, 2)
	for _, scope := range scopes {
		switch HostCapability(strings.TrimSpace(scope)) {
		case CapabilityServiceRevocableResourceHandle, CapabilityUIDynamicActions, CapabilityUIDynamic:
			features = appendRPCFeature(features, RPCFeatureDurableActionsV1)
		case CapabilityDatasetBind:
			features = appendRPCFeature(features, RPCFeatureDatasetBindingsV1)
		case CapabilityPolicyControl:
			features = appendRPCFeature(features, RPCFeaturePolicyControlsV1)
		case CapabilityDatasetQuery, CapabilityDatasetManage:
			features = appendRPCFeature(features, RPCFeatureDatasetsV1)
		case CapabilityDatasetResolve:
			features = appendRPCFeature(features, RPCFeatureDatasetsV1)
			features = appendRPCFeature(features, RPCFeatureDatasetResolveV1)
		case CapabilityManagedNetworkListen, CapabilityManagedNetworkDial:
			features = appendRPCFeature(features, RPCFeatureManagedNetworkV1)
		case CapabilityScopedSecretRead, CapabilityScopedSecretWrite:
			features = appendRPCFeature(features, RPCFeatureScopedSecretsV1)
		}
	}
	return features
}

// RequiredRPCFeaturesForExtensions adds protocol features owned by extension
// points. Permissions authorize effects; they do not classify a plugin as a
// provider of an unrelated RPC surface.
func RequiredRPCFeaturesForExtensions(scopes, extensionPoints []string) []string {
	features := RequiredRPCFeatures(scopes)
	for _, extensionPoint := range extensionPoints {
		if strings.TrimSpace(extensionPoint) == ExtensionHTTPBackendProvider {
			features = appendRPCFeature(features, RPCFeatureHTTPBackendProviderV1)
		}
	}
	return features
}

func appendRPCFeature(features []string, feature string) []string {
	for _, existing := range features {
		if existing == feature {
			return features
		}
	}
	return append(features, feature)
}

// ValidateRPCFeatures requires an exact, canonical acknowledgement of every
// requested extension. Unrequested extensions are rejected so protocol state
// cannot be smuggled through a capability list.
func ValidateRPCFeatures(required, provided []string) error {
	want := make(map[string]struct{}, len(required))
	for _, feature := range required {
		if feature != strings.TrimSpace(feature) || !knownRPCFeature(feature) {
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

func knownRPCFeature(feature string) bool {
	switch feature {
	case RPCFeatureDurableActionsV1, RPCFeatureHTTPBackendProviderV1, RPCFeatureDatasetsV1, RPCFeatureDatasetResolveV1, RPCFeatureManagedNetworkV1, RPCFeatureScopedSecretsV1, RPCFeatureDatasetBindingsV1, RPCFeaturePolicyControlsV1, RPCFeatureExecutionScopeV1:
		return true
	default:
		return false
	}
}

// RequiredRPCFeaturesForExecutionScope is used when a Host injects an explicit
// scope. A guest not supporting the scope protocol must fail admission rather
// than silently select the wrong execution face. Legacy projection is unchanged.
func RequiredRPCFeaturesForExecutionScope(scopes, extensions []string, scope string) ([]string, error) {
	if _, err := ResolveExecutionScope(scope, true, ""); err != nil {
		return nil, err
	}
	return appendRPCFeature(RequiredRPCFeaturesForExtensions(scopes, extensions), RPCFeatureExecutionScopeV1), nil
}

// RPCFeaturesWithExecutionScope advertises support without demanding it from a
// legacy Host. The actual handshake still acknowledges only requested features.
func RPCFeaturesWithExecutionScope(features []string) []string {
	return appendRPCFeature(append([]string(nil), features...), RPCFeatureExecutionScopeV1)
}
