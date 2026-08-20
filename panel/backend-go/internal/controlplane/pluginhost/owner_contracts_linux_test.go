//go:build linux && !integration

package pluginhost

import (
	"strings"
	"testing"
)

func TestPluginHostSandboxValidationAcceptsCanonicalCandidate(t *testing.T) {
	t.Parallel()
	digest := strings.Repeat("a", 64)
	requirement, err := SandboxRequirementFromValidatedPackage(validatedSandboxPackage(digest, []string{"secret.use"}, []string{"container.provider"}))
	if err != nil {
		t.Fatal(err)
	}
	candidate := Candidate{
		Artifact:    Artifact{SHA256: digest},
		Identity:    Identity{PackageDigest: digest, Generation: "generation-1"},
		Requirement: requirement,
	}
	if err := validatePlatformSandbox(candidate); err != nil {
		t.Fatalf("validatePlatformSandbox() = %v", err)
	}
}
