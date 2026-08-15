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
	digest, err := fileDigest(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	requirement := canonicalNonprivilegedRequirement(t, digest)
	process, cleanup, err := (ExecRunner{}).Start(context.Background(), InstanceSpec{
		ID:          "sandbox-live",
		Executable:  os.Args[0],
		Args:        []string{"-test.run=^TestWindowsSandboxGuest$"},
		Environment: []string{"NRE_TEST_WINDOWS_SANDBOX_GUEST=1", "NRE_TEST_WINDOWS_DENIED_PATH=" + deniedPath},
		Security:    Security{Requirement: requirement},
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

func TestWindowsSandboxAdmitsCanonicalRequirementsWithoutGrant(t *testing.T) {
	ordinary := canonicalNonprivilegedRequirement(t, strings.Repeat("a", 64))
	privileged, err := NewSandboxRequirement(SandboxRequirementProjection{
		PackageDigest: strings.Repeat("a", 64), Permissions: []SandboxPermission{PermissionSecretUse}, ExtensionPoints: []SandboxExtensionPoint{ExtensionHTTPRequest},
		ResourceBudget: ManifestResourceBudget{TimeoutMS: 1000, MemoryBytes: 1 << 20, Concurrency: 1, InputBytes: 4096, OutputBytes: 4096, CPUMillis: 100, Restarts: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	sandbox := windowsJobSandbox{}
	if !sandbox.Available() {
		t.Fatal("Windows restricted-token launcher is unavailable")
	}
	for name, requirement := range map[string]SandboxRequirement{"ordinary": ordinary, "privileged": privileged} {
		t.Run(name, func(t *testing.T) {
			security := Security{Requirement: requirement}
			decision, err := DecideSandbox(sandbox, security)
			if err != nil || !decision.Sandboxed || decision.Provider != sandbox.Provider() {
				t.Fatalf("Windows sandbox decision = %+v, %v", decision, err)
			}
			security.Grants = []string{UnsandboxedGrant}
			decision, err = DecideSandbox(sandbox, security)
			if err != nil || !decision.Sandboxed || decision.Provider != sandbox.Provider() {
				t.Fatalf("legacy grant changed Windows sandbox decision = %+v, %v", decision, err)
			}
		})
	}
}
