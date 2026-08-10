//go:build windows

package process

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func TestWindowsSandboxStartsSuspendedBeforeJobAssignment(t *testing.T) {
	attributes := windowsSandboxSysProcAttr(windows.Token(123))
	if attributes.CreationFlags&windows.CREATE_SUSPENDED == 0 || !attributes.NoInheritHandles || attributes.Token == 0 {
		t.Fatalf("sandbox process is not suspended with restricted non-inherited handles: %+v", attributes)
	}
}

func TestWindowsSandboxLiveProcess(t *testing.T) {
	if os.Getenv("NRE_TEST_WINDOWS_SANDBOX") != "1" {
		t.Skip("set NRE_TEST_WINDOWS_SANDBOX=1 to exercise the restricted process")
	}
	sandbox := newPlatformSandbox()
	deniedPath := filepath.Join(t.TempDir(), "must-not-write")
	process, cleanup, err := (ExecRunner{}).Start(context.Background(), InstanceSpec{
		ID:          "sandbox-live",
		Executable:  os.Args[0],
		Args:        []string{"-test.run=^TestWindowsSandboxGuest$"},
		Environment: []string{"NRE_TEST_WINDOWS_SANDBOX_GUEST=1", "NRE_TEST_WINDOWS_DENIED_PATH=" + deniedPath},
		Security: Security{Requirement: testSandboxRequirement(Budget{
			CPUMillis:   1000,
			MemoryBytes: 256 << 20,
			Processes:   2,
			Network:     true,
		}, false, false)},
	}, sandbox, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if err := errors.Join(process.Wait(), cleanup()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(deniedPath); !os.IsNotExist(err) {
		t.Fatal("restricted guest wrote host-private file")
	}
}

func TestWindowsSandboxGuest(t *testing.T) {
	if os.Getenv("NRE_TEST_WINDOWS_SANDBOX_GUEST") != "1" {
		t.Skip("sandbox guest helper")
	}
	if err := os.WriteFile(os.Getenv("NRE_TEST_WINDOWS_DENIED_PATH"), []byte("denied"), 0o600); err == nil {
		t.Fatal("restricted token retained host file write access")
	}
}

func TestWindowsSandboxRejectsHighRiskBoundary(t *testing.T) {
	requirement, err := NewSandboxRequirement(SandboxRequirementProjection{
		PackageDigest: strings.Repeat("a", 64), Permissions: []SandboxPermission{PermissionSecretUse}, ExtensionPoints: []SandboxExtensionPoint{ExtensionHTTPRequest},
		ResourceBudget: ManifestResourceBudget{TimeoutMS: 1000, MemoryBytes: 1 << 20, Concurrency: 1, InputBytes: 4096, OutputBytes: 4096, CPUMillis: 100, Restarts: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	security := Security{Requirement: requirement}
	if _, err := DecideSandbox(windowsJobSandbox{}, security); err == nil {
		t.Fatal("high-risk capability was admitted without an enforceable Windows boundary")
	}
	security.Grants = []string{UnsandboxedGrant}
	decision, err := DecideSandbox(windowsJobSandbox{}, security)
	if err != nil || decision.Sandboxed || decision.Provider != "unsandboxed" {
		t.Fatalf("explicit Windows unsandboxed admission = %+v, %v", decision, err)
	}
}
