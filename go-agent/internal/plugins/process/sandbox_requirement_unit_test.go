package process

import (
	"fmt"
	"strings"
	"testing"
)

func TestNewSandboxRequirementUsesProcessFloor(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		concurrency int
		want        int
	}{{1, 50}, {8, 50}, {46, 50}, {47, 51}} {
		t.Run(fmt.Sprintf("concurrency_%d", test.concurrency), func(t *testing.T) {
			requirement, err := NewSandboxRequirement(SandboxRequirementProjection{
				PackageDigest: strings.Repeat("a", 64),
				ResourceBudget: ManifestResourceBudget{
					TimeoutMS: 1000, MemoryBytes: 1 << 20, Concurrency: test.concurrency,
					InputBytes: 4096, OutputBytes: 4096, CPUMillis: 1000, Restarts: 1,
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			if got := requirement.Budget().Processes; got != test.want {
				t.Fatalf("process budget = %d, want %d", got, test.want)
			}
		})
	}
}

func TestNewSandboxRequirementRejectsRemovedDockerComposeIdentifiers(t *testing.T) {
	t.Parallel()
	budget := ManifestResourceBudget{
		TimeoutMS: 1000, MemoryBytes: 1 << 20, Concurrency: 1,
		InputBytes: 4096, OutputBytes: 4096, CPUMillis: 1000, Restarts: 1,
	}
	digest := strings.Repeat("d", 64)
	if _, err := NewSandboxRequirement(SandboxRequirementProjection{
		PackageDigest:   digest,
		Permissions:     []SandboxPermission{"container.compose"},
		ExtensionPoints: []SandboxExtensionPoint{ExtensionHTTPRequest},
		ResourceBudget:  budget,
	}); err == nil {
		t.Fatal("container.compose sandbox permission was accepted")
	}
	if _, err := NewSandboxRequirement(SandboxRequirementProjection{
		PackageDigest:   digest,
		Permissions:     []SandboxPermission{PermissionSecretUse},
		ExtensionPoints: []SandboxExtensionPoint{"container.provider"},
		ResourceBudget:  budget,
	}); err == nil {
		t.Fatal("container.provider sandbox extension was accepted")
	}
}

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

func TestNewSandboxRequirementKeepsL4RulePermissionHostMediated(t *testing.T) {
	t.Parallel()
	requirement, err := NewSandboxRequirement(SandboxRequirementProjection{
		PackageDigest:   strings.Repeat("e", 64),
		Permissions:     []SandboxPermission{PermissionL4Rule},
		ExtensionPoints: []SandboxExtensionPoint{ExtensionHTTPRequest},
		ResourceBudget: ManifestResourceBudget{
			TimeoutMS: 1000, MemoryBytes: 1 << 20, Concurrency: 1,
			InputBytes: 4096, OutputBytes: 4096, CPUMillis: 1000, Restarts: 1,
		},
	})
	if err != nil {
		t.Fatalf("l4.rule sandbox requirement = %v", err)
	}
	if requirement.RequiresPrivilegeBoundary() || requirement.RequiresFilesystemBoundary() || requirement.Budget().Network {
		t.Fatal("l4.rule permission gained ambient privilege, filesystem, or network authority")
	}
}

func TestNewSandboxRequirementKeepsChannelReversePermissionHostMediated(t *testing.T) {
	t.Parallel()
	requirement, err := NewSandboxRequirement(SandboxRequirementProjection{
		PackageDigest:   strings.Repeat("f", 64),
		Permissions:     []SandboxPermission{PermissionChannelReverse},
		ExtensionPoints: []SandboxExtensionPoint{ExtensionHTTPRequest},
		ResourceBudget: ManifestResourceBudget{
			TimeoutMS: 1000, MemoryBytes: 1 << 20, Concurrency: 1,
			InputBytes: 4096, OutputBytes: 4096, CPUMillis: 1000, Restarts: 1,
		},
	})
	if err != nil {
		t.Fatalf("channel.reverse sandbox requirement = %v", err)
	}
	if requirement.RequiresPrivilegeBoundary() || requirement.RequiresFilesystemBoundary() || requirement.Budget().Network {
		t.Fatal("channel.reverse permission gained ambient privilege, filesystem, or network authority")
	}
}

func TestNewSandboxRequirementAllowsDeclaredFullNetwork(t *testing.T) {
	t.Parallel()
	requirement, err := NewSandboxRequirement(SandboxRequirementProjection{
		PackageDigest: strings.Repeat("b", 64),
		Permissions:   []SandboxPermission{PermissionNetworkFull},
		ResourceBudget: ManifestResourceBudget{
			TimeoutMS: 1000, MemoryBytes: 1 << 20, Concurrency: 1,
			InputBytes: 4096, OutputBytes: 4096, CPUMillis: 1000, Restarts: 1,
		},
	})
	if err != nil {
		t.Fatalf("network.full sandbox requirement = %v", err)
	}
	if !requirement.RequiresPrivilegeBoundary() || !requirement.Budget().Network || requirement.RequiresNetworkIsolation() {
		t.Fatalf("network.full requirement = %+v", requirement)
	}
}
