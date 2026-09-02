package pluginhost

import (
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/plugins"
	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

type SandboxRequirement struct {
	packageDigest string
	budget        ProcessBudget
	privileged    bool
	networkBound  bool
	filesystem    bool
}

// minimumPluginProcessLimit leaves enough task headroom for Go RPC guests and
// their host-authorized helpers. Linux accounts threads against RLIMIT_NPROC,
// so a small process-only floor can terminate an otherwise healthy guest.
const minimumPluginProcessLimit = 50

func SandboxRequirementFromValidatedPackage(pkg plugins.ValidatedPackage) (SandboxRequirement, error) {
	manifest := pkg.Manifest
	digest := strings.TrimSpace(pkg.Digest)
	if !validControlSandboxDigest(digest) || manifest.Runtime.Kind != pluginsdk.RuntimeRPCService {
		return SandboxRequirement{}, errors.New("sandbox requirement requires a validated rpc-service package")
	}
	budget := manifest.ResourceBudget
	if budget.TimeoutMS <= 0 || budget.TimeoutMS > 300000 || budget.MemoryBytes < 65536 || budget.MemoryBytes > plugins.MaxRuntimeMemoryBytes || budget.Concurrency <= 0 || budget.Concurrency > 4096 || budget.InputBytes <= 0 || budget.InputBytes > plugins.MaxRuntimeIOBytes || budget.OutputBytes <= 0 || budget.OutputBytes > plugins.MaxRuntimeIOBytes || budget.CPUMillis <= 0 || budget.CPUMillis > 100000 || budget.Restarts < 0 || budget.Restarts > 100 {
		return SandboxRequirement{}, errors.New("sandbox requirement requires a bounded canonical rpc-service resource budget")
	}
	requirement := SandboxRequirement{packageDigest: digest}
	seenPermissions := map[string]struct{}{}
	for _, permission := range manifest.Permissions {
		if permission.Name != strings.TrimSpace(permission.Name) || !knownControlSandboxPermission(permission.Name) {
			return SandboxRequirement{}, fmt.Errorf("sandbox requirement permission %q is not canonical", permission.Name)
		}
		if _, duplicate := seenPermissions[permission.Name]; duplicate {
			continue
		}
		seenPermissions[permission.Name] = struct{}{}
		switch permission.Name {
		case "dns.manage", "secret.use", "storage.write":
			requirement.privileged = true
		case string(pluginsdk.CapabilityPolicyAtomicState), string(pluginsdk.CapabilityPolicyMonotonicClock),
			string(pluginsdk.CapabilityPolicyTrustedSource), string(pluginsdk.CapabilityServiceRevocableResourceHandle),
			string(pluginsdk.CapabilityUIDynamicActions),
			string(pluginsdk.CapabilityHTTPRule), string(pluginsdk.CapabilityL4Rule),
			string(pluginsdk.CapabilityUIDynamic):
			// These operations remain host-mediated and grant the guest no
			// ambient filesystem, network, or process authority.
		}
		if permission.Name == "storage.read" || permission.Name == "storage.write" {
			requirement.filesystem = true
		}
	}
	seenExtensions := map[string]struct{}{}
	network := false
	for _, extension := range manifest.ExtensionPoints {
		if extension != strings.TrimSpace(extension) || !knownControlSandboxExtension(extension) {
			return SandboxRequirement{}, fmt.Errorf("sandbox requirement extension point %q is not canonical", extension)
		}
		if _, duplicate := seenExtensions[extension]; duplicate {
			return SandboxRequirement{}, fmt.Errorf("sandbox requirement extension point %q is duplicated", extension)
		}
		seenExtensions[extension] = struct{}{}
		switch extension {
		case "dns.provider", "tunnel.provider":
			requirement.privileged = true
			network = true
		}
	}
	if _, ok := seenPermissions["dns.manage"]; ok {
		network = true
	}
	requirement.networkBound = network
	processes := budget.Concurrency + 4
	if processes < minimumPluginProcessLimit {
		processes = minimumPluginProcessLimit
	}
	files := pluginFileLimit(processes)
	requirement.budget = ProcessBudget{CPUMillis: budget.CPUMillis, MemoryBytes: budget.MemoryBytes, Processes: processes, Files: files, Network: network}
	return requirement, nil
}

func pluginFileLimit(processes int) int {
	files := 64 + processes*8
	if files < 256 {
		files = 256
	}
	result := 1
	for result < files {
		result <<= 1
	}
	return result
}

func (r SandboxRequirement) Budget() ProcessBudget { return r.budget }
func (r SandboxRequirement) HighRisk() bool {
	return r.privileged || r.networkBound || r.budget.Processes > 0 || r.budget.Files > 0
}
func (r SandboxRequirement) RequiresPrivilegeBoundary() bool  { return r.privileged }
func (r SandboxRequirement) RequiresFilesystemBoundary() bool { return r.filesystem }
func (r SandboxRequirement) RequiresNetworkIsolation() bool {
	return r.networkBound && !r.budget.Network
}
func (r SandboxRequirement) validatePackageDigest(digest string) error {
	if r.packageDigest == "" || strings.TrimSpace(digest) != r.packageDigest {
		return errors.New("sandbox requirement is not bound to the hosted package digest")
	}
	return nil
}

func validControlSandboxDigest(value string) bool {
	if len(value) != 64 || value != strings.ToLower(value) {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32
}

func knownControlSandboxPermission(value string) bool {
	switch value {
	case "agent.read", "agent.configure", "event.emit", "http.inspect", "http.respond", "l4.inspect", "l4.respond",
		"policy.read", "policy.write", "secret.use", "storage.read", "storage.write",
		string(pluginsdk.CapabilityHTTPRule), string(pluginsdk.CapabilityUIDynamic),
		"dns.manage":
		return true
	default:
		// Canonical Host capabilities are mediated by the host broker. They do
		// not grant the guest direct filesystem, network, or process authority,
		// so their sandbox projection adds no ambient privilege.
		return pluginsdk.HostCapability(value).Validate() == nil
	}
}

func knownControlSandboxExtension(value string) bool {
	switch value {
	case "http.request", "http.response", "l4.accept", "policy.provider", "dns.provider", "tunnel.provider",
		pluginsdk.ExtensionUIRoute, pluginsdk.ExtensionResourceGroup:
		return true
	default:
		return false
	}
}
