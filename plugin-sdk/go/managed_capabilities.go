package pluginsdk

import "fmt"

// ValidateManifestManagedCapabilities is the additive package gate for managed
// resources. Call it alongside structural/artifact validation, before Prepare.
// A hybrid RPC/control-plane + Agent policy package may declare permissions for
// both faces; the runtime grants only the effects usable on the current face.
func ValidateManifestManagedCapabilities(manifest Manifest, supported []HostCapability) error {
	available := make(map[HostCapability]bool, len(supported))
	for _, capability := range supported {
		if err := capability.Validate(); err != nil {
			return err
		}
		available[capability] = true
	}
	for _, permission := range manifest.Permissions {
		capability := HostCapability(permission.Name)
		if capability == CapabilityDatasetResolve {
			queryDeclared := false
			for _, declared := range manifest.Permissions {
				if declared.Name == string(CapabilityDatasetQuery) {
					queryDeclared = true
				}
			}
			if !queryDeclared {
				return fmt.Errorf("dataset.resolve requires a dataset.query declaration")
			}
		}
		switch capability {
		case CapabilityDatasetQuery, CapabilityDatasetResolve, CapabilityDatasetManage, CapabilityManagedNetworkListen, CapabilityManagedNetworkDial, CapabilityScopedSecretRead, CapabilityScopedSecretWrite:
			if !available[capability] {
				return fmt.Errorf("Host does not support required managed capability %q", capability)
			}
			if manifest.Runtime.Kind == RuntimeWASMPolicy && capability != CapabilityDatasetQuery && capability != CapabilityDatasetResolve {
				return fmt.Errorf("managed capability %q is not available to a WASM policy", capability)
			}
			if manifest.Runtime.Kind != RuntimeWASMPolicy && manifest.Runtime.Kind != RuntimeRPCService {
				return fmt.Errorf("managed capability %q requires a known runtime", capability)
			}
		}
	}
	return nil
}

// DatasetRuntimeCapability maps public operations to their explicit grant.
func DatasetRuntimeCapability(operation string) (HostCapability, error) {
	switch operation {
	case HostRuntimeDatasetResolve:
		return CapabilityDatasetResolve, nil
	case HostRuntimeDatasetOpen, HostRuntimeDatasetQuery:
		return CapabilityDatasetQuery, nil
	case HostRuntimeDatasetControl, HostRuntimeDatasetStatus, HostRuntimeDatasetCatalog:
		return CapabilityDatasetManage, nil
	default:
		return "", fmt.Errorf("unknown dataset operation %q", operation)
	}
}
