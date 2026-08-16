//go:build !integration

package pluginhost

import (
	"io"
	"strings"
	"testing"
)

func TestPluginHostSandboxRequirementAndFailClosedLaunch(t *testing.T) {
	t.Parallel()
	digest := strings.Repeat("a", 64)
	pkg := validatedSandboxPackage(digest, []string{"secret.use"}, []string{"container.provider"})
	requirement, err := SandboxRequirementFromValidatedPackage(pkg)
	if err != nil || !requirement.RequiresPrivilegeBoundary() {
		t.Fatalf("requirement=%+v err=%v", requirement, err)
	}
	if _, err := (ExecLauncher{}).Start(t.Context(), t.TempDir(), nil, nil, io.Discard, Candidate{
		Identity: Identity{PackageDigest: digest}, Requirement: requirement,
	}); err == nil {
		t.Fatal("privileged candidate launched without sandbox identity")
	}
}
