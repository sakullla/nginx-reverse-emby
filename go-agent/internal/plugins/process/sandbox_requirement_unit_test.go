package process

import (
	"strings"
	"testing"
)

func TestNewSandboxRequirementAllowsResourceGroupExtension(t *testing.T) {
	t.Parallel()
	requirement, err := NewSandboxRequirement(SandboxRequirementProjection{
		PackageDigest:   strings.Repeat("c", 64),
		ExtensionPoints: []SandboxExtensionPoint{ExtensionResourceGroup},
		ResourceBudget: ManifestResourceBudget{
			TimeoutMS: 1000, MemoryBytes: 1 << 20, Concurrency: 1,
			InputBytes: 4096, OutputBytes: 4096, CPUMillis: 1000, Restarts: 1,
		},
	})
	if err != nil {
		t.Fatalf("resource.group sandbox requirement = %v", err)
	}
	if err := requirement.ValidatePackageDigest(strings.Repeat("c", 64)); err != nil {
		t.Fatalf("resource.group package digest binding = %v", err)
	}
}
