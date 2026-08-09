//go:build windows

package process

import (
	"context"
	"errors"
	"io"
	"os"
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
	process, cleanup, err := (ExecRunner{}).Start(context.Background(), InstanceSpec{
		ID:          "sandbox-live",
		Executable:  os.Args[0],
		Args:        []string{"-test.run=^TestWindowsSandboxGuest$"},
		Environment: []string{"NRE_TEST_WINDOWS_SANDBOX_GUEST=1"},
		Security: Security{Budget: Budget{
			CPUMillis:   1000,
			MemoryBytes: 256 << 20,
			Processes:   2,
			Network:     true,
		}},
	}, sandbox, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if err := errors.Join(process.Wait(), cleanup()); err != nil {
		t.Fatal(err)
	}
}

func TestWindowsSandboxGuest(t *testing.T) {
	if os.Getenv("NRE_TEST_WINDOWS_SANDBOX_GUEST") != "1" {
		t.Skip("sandbox guest helper")
	}
}
