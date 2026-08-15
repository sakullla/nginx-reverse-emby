package process

import (
	"strings"
	"testing"

	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

func testSandboxRequirement(budget Budget, privileged, networkBound bool) SandboxRequirement {
	return SandboxRequirement{packageDigest: "test-package", budget: budget, privileged: privileged, networkBound: networkBound}
}

func TestHTTPBackendProviderRequirementEnablesBoundedOutboundNetwork(t *testing.T) {
	requirement, err := NewSandboxRequirement(SandboxRequirementProjection{
		PackageDigest:   strings.Repeat("a", 64),
		Permissions:     []SandboxPermission{PermissionHTTPOutbound},
		ExtensionPoints: []SandboxExtensionPoint{ExtensionHTTPBackendProvider},
		ResourceBudget:  ManifestResourceBudget{TimeoutMS: 1000, MemoryBytes: 64 << 20, Concurrency: 8, InputBytes: 4096, OutputBytes: 4096, CPUMillis: 1000},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !requirement.Budget().Network {
		t.Fatal("HTTP backend provider outbound permission did not enable the signed network budget")
	}
}

func canonicalNonprivilegedRequirement(t *testing.T, digest string) SandboxRequirement {
	t.Helper()
	requirement, err := NewSandboxRequirement(SandboxRequirementProjection{
		PackageDigest:   digest,
		Permissions:     []SandboxPermission{PermissionAgentRead},
		ExtensionPoints: []SandboxExtensionPoint{ExtensionHTTPRequest},
		ResourceBudget:  ManifestResourceBudget{TimeoutMS: 1000, MemoryBytes: 256 << 20, Concurrency: 2, InputBytes: 4096, OutputBytes: 4096, CPUMillis: 1000, Restarts: 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	return requirement
}

func TestSandboxRequirementCanonicalAdmission(t *testing.T) {
	base := SandboxRequirementProjection{
		PackageDigest:   strings.Repeat("a", 64),
		ExtensionPoints: []SandboxExtensionPoint{ExtensionHTTPRequest},
		Permissions:     []SandboxPermission{PermissionAgentRead},
		ResourceBudget:  ManifestResourceBudget{TimeoutMS: 1000, MemoryBytes: 1 << 20, Concurrency: 2, InputBytes: 4096, OutputBytes: 4096, CPUMillis: 100, Restarts: 2},
	}
	requirement, err := NewSandboxRequirement(base)
	if err != nil || requirement.Budget().Processes != 8 || requirement.Budget().Files != 64 || !requirement.HighRisk() {
		t.Fatalf("canonical requirement = %+v, %v", requirement, err)
	}
	for _, permission := range []SandboxPermission{PermissionContainerManage, PermissionDNSManage, PermissionSecretUse, PermissionStorageWrite} {
		projection := base
		projection.Permissions = []SandboxPermission{permission}
		got, err := NewSandboxRequirement(projection)
		if err != nil || !got.RequiresPrivilegeBoundary() {
			t.Fatalf("permission %q requirement = %+v, %v", permission, got, err)
		}
	}
	for _, capability := range []pluginsdk.HostCapability{
		pluginsdk.CapabilityPolicyAtomicState,
		pluginsdk.CapabilityPolicyMonotonicClock,
		pluginsdk.CapabilityPolicyTrustedSource,
		pluginsdk.CapabilityServiceRevocableResourceHandle,
		pluginsdk.CapabilityUIDynamicActions,
	} {
		projection := base
		projection.Permissions = []SandboxPermission{SandboxPermission(capability)}
		got, err := NewSandboxRequirement(projection)
		if err != nil || got.RequiresPrivilegeBoundary() || got.RequiresFilesystemBoundary() || got.Budget().Network {
			t.Fatalf("host capability %q requirement = %+v, %v", capability, got, err)
		}
	}
	for _, extension := range []SandboxExtensionPoint{ExtensionContainerProvider, ExtensionDNSProvider, ExtensionTunnelProvider} {
		projection := base
		projection.ExtensionPoints = []SandboxExtensionPoint{extension}
		got, err := NewSandboxRequirement(projection)
		if err != nil || !got.RequiresPrivilegeBoundary() || !got.Budget().Network {
			t.Fatalf("extension %q requirement = %+v, %v", extension, got, err)
		}
	}
	invalid := base
	invalid.ExtensionPoints = []SandboxExtensionPoint{"invalid.extension"}
	if _, err := NewSandboxRequirement(invalid); err == nil {
		t.Fatal("non-canonical extension authority was accepted")
	}
	if err := requirement.ValidatePackageDigest("other"); err == nil {
		t.Fatal("requirement was rebound to another package digest")
	}
	overflow := base
	overflow.ResourceBudget.Concurrency = 4097
	if _, err := NewSandboxRequirement(overflow); err == nil {
		t.Fatal("out-of-validator resource projection was accepted")
	}
}
