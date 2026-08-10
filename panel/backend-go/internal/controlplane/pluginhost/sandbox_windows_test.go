//go:build windows

package pluginhost

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
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
	candidate := Candidate{Requirement: testControlRequirement(ProcessBudget{CPUMillis: 1000, MemoryBytes: 256 << 20, Processes: 2, Network: true}, false, false), attemptEnvironment: []string{"NRE_PLUGIN_TEST=1"}}
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

func TestPluginHostWindowsSandboxRejectsHighRiskBoundary(t *testing.T) {
	digest := strings.Repeat("a", 64)
	requirement, err := SandboxRequirementFromValidatedPackage(validatedSandboxPackage(digest, []string{"secret.use"}, []string{"http.request"}))
	if err != nil {
		t.Fatal(err)
	}
	candidate := Candidate{Identity: Identity{PackageDigest: digest}, Requirement: requirement}
	if err := authorizeSandbox(candidate); err == nil {
		t.Fatal("high-risk capability was admitted without an enforceable Windows boundary")
	}
	candidate.Grants = []string{UnsandboxedGrant}
	if err := authorizeSandbox(candidate); err != nil {
		t.Fatalf("explicit Windows unsandboxed admission failed: %v", err)
	}
}
