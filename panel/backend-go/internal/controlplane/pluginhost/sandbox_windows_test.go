//go:build windows

package pluginhost

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func TestPluginHostWindowsSandboxStartsSuspended(t *testing.T) {
	attributes := backendSandboxSysProcAttr(windows.Token(123))
	if attributes.CreationFlags&windows.CREATE_SUSPENDED == 0 || !attributes.NoInheritHandles || attributes.Token == 0 {
		t.Fatalf("sandbox process is not suspended with restricted non-inherited handles: %+v", attributes)
	}
}

func TestPluginHostWindowsSandboxLiveProcess(t *testing.T) {
	if os.Getenv("NRE_TEST_WINDOWS_SANDBOX") != "1" {
		t.Skip("set NRE_TEST_WINDOWS_SANDBOX=1 to exercise the restricted process")
	}
	deniedPath := filepath.Join(t.TempDir(), "must-not-write")
	validated := validatedSignedSandboxPackage(t, "agent.read", "http.request")
	requirement, err := SandboxRequirementFromValidatedPackage(validated)
	if err != nil {
		t.Fatal(err)
	}
	candidate := Candidate{Identity: Identity{PackageDigest: validated.Digest}, Requirement: requirement, attemptEnvironment: []string{"NRE_PLUGIN_TEST=1"}}
	process, err := (ExecLauncher{}).Start(context.Background(), os.Args[0], []string{"-test.run=^TestPluginHostWindowsSandboxGuest$"}, []string{"NRE_TEST_WINDOWS_SANDBOX_GUEST=1", "NRE_TEST_WINDOWS_DENIED_PATH=" + deniedPath}, io.Discard, candidate)
	if err != nil {
		t.Fatal(err)
	}
	if err := process.Wait(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(deniedPath); !os.IsNotExist(err) {
		t.Fatal("restricted guest wrote host-private file")
	}
}

func TestPluginHostWindowsSandboxGuest(t *testing.T) {
	if os.Getenv("NRE_TEST_WINDOWS_SANDBOX_GUEST") != "1" {
		t.Skip("sandbox guest helper")
	}
	if err := os.WriteFile(os.Getenv("NRE_TEST_WINDOWS_DENIED_PATH"), []byte("denied"), 0o600); err == nil {
		t.Fatal("restricted token retained host file write access")
	}
}

func TestPluginHostWindowsSandboxAdmitsSignedRequirementsWithoutGrant(t *testing.T) {
	for name, permission := range map[string]string{"ordinary": "agent.read", "privileged": "secret.use"} {
		t.Run(name, func(t *testing.T) {
			validated := validatedSignedSandboxPackage(t, permission, "http.request")
			requirement, err := SandboxRequirementFromValidatedPackage(validated)
			if err != nil {
				t.Fatal(err)
			}
			candidate := Candidate{Identity: Identity{PackageDigest: validated.Digest}, Requirement: requirement}
			if err := authorizeSandbox(candidate); err != nil {
				t.Fatalf("signed requirement was rejected by Windows isolation: %v", err)
			}
			candidate.Grants = []string{UnsandboxedGrant}
			if err := authorizeSandbox(candidate); err != nil {
				t.Fatalf("legacy grant changed Windows isolation admission: %v", err)
			}
		})
	}
}
