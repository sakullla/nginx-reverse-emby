//go:build !linux && !integration

package pluginhost

import (
	"io"
	"os"
	"strings"
	"testing"
)

func TestPluginHostSandboxFailClosedWhenUnavailable(t *testing.T) {
	t.Parallel()
	digest := strings.Repeat("a", 64)
	requirement, err := SandboxRequirementFromValidatedPackage(validatedSandboxPackage(digest, []string{"secret.use"}, []string{"container.provider"}))
	if err != nil {
		t.Fatal(err)
	}
	candidate := Candidate{Identity: Identity{PackageDigest: digest}, Requirement: requirement}
	if err := validatePlatformSandbox(candidate); err == nil || !strings.Contains(err.Error(), "unavailable") {
		t.Fatalf("validatePlatformSandbox() = %v, want sandbox unavailable", err)
	}
	if _, err := (ExecLauncher{}).Start(t.Context(), os.Args[0], nil, nil, io.Discard, candidate); err == nil || !strings.Contains(err.Error(), "unavailable") {
		t.Fatalf("Start() = %v, want sandbox unavailable", err)
	}
}
