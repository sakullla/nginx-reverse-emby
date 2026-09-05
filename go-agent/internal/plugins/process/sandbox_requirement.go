package process

import (
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

type SandboxPermission string
type SandboxExtensionPoint string

const (
	PermissionAgentRead                      SandboxPermission = "agent.read"
	PermissionAgentConfigure                 SandboxPermission = "agent.configure"
	PermissionEventEmit                      SandboxPermission = "event.emit"
	PermissionHTTPInspect                    SandboxPermission = "http.inspect"
	PermissionHTTPRespond                    SandboxPermission = "http.respond"
	PermissionHTTPOutbound                   SandboxPermission = SandboxPermission(pluginsdk.PermissionHTTPOutbound)
	PermissionNetworkFull                    SandboxPermission = SandboxPermission(pluginsdk.PermissionNetworkFull)
	PermissionL4Inspect                      SandboxPermission = "l4.inspect"
	PermissionL4Respond                      SandboxPermission = "l4.respond"
	PermissionPolicyRead                     SandboxPermission = "policy.read"
	PermissionPolicyWrite                    SandboxPermission = "policy.write"
	PermissionSecretUse                      SandboxPermission = "secret.use"
	PermissionStorageRead                    SandboxPermission = "storage.read"
	PermissionStorageWrite                   SandboxPermission = "storage.write"
	PermissionHTTPRule                       SandboxPermission = SandboxPermission(pluginsdk.CapabilityHTTPRule)
	PermissionL4Rule                         SandboxPermission = SandboxPermission(pluginsdk.CapabilityL4Rule)
	PermissionChannelReverse                 SandboxPermission = SandboxPermission(pluginsdk.CapabilityChannelReverse)
	PermissionUIDynamic                      SandboxPermission = SandboxPermission(pluginsdk.CapabilityUIDynamic)
	PermissionDNSManage                      SandboxPermission = "dns.manage"
	PermissionPolicyAtomicState              SandboxPermission = SandboxPermission(pluginsdk.CapabilityPolicyAtomicState)
	PermissionPolicyMonotonicClock           SandboxPermission = SandboxPermission(pluginsdk.CapabilityPolicyMonotonicClock)
	PermissionPolicyTrustedSource            SandboxPermission = SandboxPermission(pluginsdk.CapabilityPolicyTrustedSource)
	PermissionServiceRevocableResourceHandle SandboxPermission = SandboxPermission(pluginsdk.CapabilityServiceRevocableResourceHandle)
	PermissionUIDynamicActions               SandboxPermission = SandboxPermission(pluginsdk.CapabilityUIDynamicActions)

	ExtensionHTTPRequest         SandboxExtensionPoint = "http.request"
	ExtensionHTTPResponse        SandboxExtensionPoint = "http.response"
	ExtensionHTTPBackendProvider SandboxExtensionPoint = SandboxExtensionPoint(pluginsdk.ExtensionHTTPBackendProvider)
	ExtensionL4Accept            SandboxExtensionPoint = "l4.accept"
	ExtensionPolicyProvider      SandboxExtensionPoint = "policy.provider"
	ExtensionDNSProvider         SandboxExtensionPoint = "dns.provider"
	ExtensionTunnelProvider      SandboxExtensionPoint = "tunnel.provider"
	ExtensionUIRoute             SandboxExtensionPoint = SandboxExtensionPoint(pluginsdk.ExtensionUIRoute)
	ExtensionResourceGroup       SandboxExtensionPoint = SandboxExtensionPoint(pluginsdk.ExtensionResourceGroup)
)

type ManifestResourceBudget struct {
	TimeoutMS, MemoryBytes, InputBytes, OutputBytes, CPUMillis int64
	Concurrency, Restarts                                      int
}

type SandboxRequirementProjection struct {
	PackageDigest   string
	Permissions     []SandboxPermission
	ExtensionPoints []SandboxExtensionPoint
	ResourceBudget  ManifestResourceBudget
}

type SandboxRequirement struct {
	packageDigest string
	budget        Budget
	privileged    bool
	networkBound  bool
	filesystem    bool
}

// minimumPluginProcessLimit leaves enough task headroom for Go RPC guests and
// their host-authorized helpers. Linux accounts threads against RLIMIT_NPROC,
// so a small process-only floor can terminate an otherwise healthy guest.
const minimumPluginProcessLimit = 50

func NewSandboxRequirement(projection SandboxRequirementProjection) (SandboxRequirement, error) {
	digest := strings.TrimSpace(projection.PackageDigest)
	if !validSandboxPackageDigest(digest) {
		return SandboxRequirement{}, errors.New("sandbox requirement requires a validated package digest")
	}
	budget := projection.ResourceBudget
	if budget.TimeoutMS <= 0 || budget.TimeoutMS > 300000 || budget.MemoryBytes < 65536 || budget.MemoryBytes > 4<<30 || budget.Concurrency <= 0 || budget.Concurrency > 4096 || budget.InputBytes <= 0 || budget.InputBytes > 16<<20 || budget.OutputBytes <= 0 || budget.OutputBytes > 16<<20 || budget.CPUMillis <= 0 || budget.CPUMillis > 100000 || budget.Restarts < 0 || budget.Restarts > 100 {
		return SandboxRequirement{}, errors.New("sandbox requirement requires a bounded canonical rpc-service resource budget")
	}
	requirement := SandboxRequirement{packageDigest: digest}
	seenPermissions := map[SandboxPermission]struct{}{}
	for _, permission := range projection.Permissions {
		if !knownSandboxPermission(permission) {
			return SandboxRequirement{}, fmt.Errorf("sandbox requirement permission %q is not canonical", permission)
		}
		if _, duplicate := seenPermissions[permission]; duplicate {
			return SandboxRequirement{}, fmt.Errorf("sandbox requirement permission %q is duplicated", permission)
		}
		seenPermissions[permission] = struct{}{}
		switch permission {
		case PermissionDNSManage, PermissionSecretUse, PermissionStorageWrite, PermissionNetworkFull:
			requirement.privileged = true
		case PermissionPolicyAtomicState, PermissionPolicyMonotonicClock, PermissionPolicyTrustedSource,
			PermissionServiceRevocableResourceHandle, PermissionUIDynamicActions,
			PermissionHTTPRule, PermissionL4Rule, PermissionChannelReverse, PermissionUIDynamic:
			// These operations remain host-mediated and grant the guest no
			// ambient filesystem, network, or process authority.
		}
		if permission == PermissionStorageRead || permission == PermissionStorageWrite {
			requirement.filesystem = true
		}
	}
	seenExtensions := map[SandboxExtensionPoint]struct{}{}
	network := false
	for _, extension := range projection.ExtensionPoints {
		if !knownSandboxExtension(extension) {
			return SandboxRequirement{}, fmt.Errorf("sandbox requirement extension point %q is not canonical", extension)
		}
		if _, duplicate := seenExtensions[extension]; duplicate {
			return SandboxRequirement{}, fmt.Errorf("sandbox requirement extension point %q is duplicated", extension)
		}
		seenExtensions[extension] = struct{}{}
		switch extension {
		case ExtensionDNSProvider, ExtensionTunnelProvider:
			requirement.privileged = true
			network = true
		}
	}
	if _, ok := seenPermissions[PermissionDNSManage]; ok {
		network = true
	}
	if _, ok := seenPermissions[PermissionHTTPOutbound]; ok {
		network = true
	}
	if _, ok := seenPermissions[PermissionNetworkFull]; ok {
		network = true
	}
	requirement.networkBound = network
	for _, permission := range []SandboxPermission{SandboxPermission(pluginsdk.PermissionManagedNetworkListen), SandboxPermission(pluginsdk.PermissionManagedNetworkDial)} {
		if _, ok := seenPermissions[permission]; ok {
			requirement.networkBound = true
		}
	}
	processes := budget.Concurrency + 4
	if processes < minimumPluginProcessLimit {
		processes = minimumPluginProcessLimit
	}
	files := processes*4 + 16
	if files < 64 {
		files = 64
	}
	requirement.budget = Budget{CPUMillis: budget.CPUMillis, MemoryBytes: budget.MemoryBytes, Processes: processes, Files: files, Network: network}
	return requirement, nil
}

func (r SandboxRequirement) Budget() Budget { return r.budget }

func (r SandboxRequirement) Filesystem() bool { return r.filesystem }
func (r SandboxRequirement) HighRisk() bool {
	return r.privileged || r.networkBound || r.budget.Processes > 0 || r.budget.Files > 0
}
func (r SandboxRequirement) RequiresPrivilegeBoundary() bool  { return r.privileged }
func (r SandboxRequirement) RequiresFilesystemBoundary() bool { return r.filesystem }
func (r SandboxRequirement) RequiresNetworkIsolation() bool {
	return r.networkBound && !r.budget.Network
}
func (r SandboxRequirement) ValidatePackageDigest(digest string) error {
	if r.packageDigest == "" || strings.TrimSpace(digest) != r.packageDigest {
		return errors.New("sandbox requirement is not bound to the hosted package digest")
	}
	return nil
}

func validSandboxPackageDigest(value string) bool {
	if len(value) != 64 || value != strings.ToLower(value) {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32
}

func knownSandboxPermission(value SandboxPermission) bool {
	switch value {
	case PermissionAgentRead, PermissionAgentConfigure, PermissionEventEmit, PermissionHTTPInspect, PermissionHTTPRespond, PermissionHTTPOutbound, PermissionNetworkFull,
		PermissionL4Inspect, PermissionL4Respond, PermissionPolicyRead, PermissionPolicyWrite, PermissionSecretUse,
		PermissionStorageRead, PermissionStorageWrite, PermissionDNSManage,
		PermissionHTTPRule, PermissionL4Rule, PermissionChannelReverse, PermissionUIDynamic,
		PermissionPolicyAtomicState, PermissionPolicyMonotonicClock, PermissionPolicyTrustedSource,
		PermissionServiceRevocableResourceHandle, PermissionUIDynamicActions:
		return true
	case SandboxPermission(pluginsdk.PermissionManagedNetworkListen), SandboxPermission(pluginsdk.PermissionManagedNetworkDial), SandboxPermission(pluginsdk.CapabilityDatasetQuery), SandboxPermission(pluginsdk.CapabilityDatasetResolve), SandboxPermission(pluginsdk.PermissionScopedSecretRead), SandboxPermission(pluginsdk.PermissionScopedSecretWrite):
		return true
	default:
		return false
	}
}

func knownSandboxExtension(value SandboxExtensionPoint) bool {
	switch value {
	case ExtensionHTTPRequest, ExtensionHTTPResponse, ExtensionHTTPBackendProvider, ExtensionL4Accept, ExtensionPolicyProvider,
		ExtensionDNSProvider, ExtensionTunnelProvider, ExtensionUIRoute, ExtensionResourceGroup:
		return true
	default:
		return false
	}
}
