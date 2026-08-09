//go:build windows

package pluginhost

import (
	"context"
	"io"
	"os"
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
	candidate := Candidate{Budget: ProcessBudget{CPUMillis: 1000, MemoryBytes: 256 << 20, Processes: 2, Network: true}}
	process, err := (ExecLauncher{}).Start(context.Background(), os.Args[0], []string{"-test.run=^TestPluginHostWindowsSandboxGuest$"}, []string{"NRE_TEST_WINDOWS_SANDBOX_GUEST=1"}, io.Discard, candidate)
	if err != nil {
		t.Fatal(err)
	}
	if err := process.Wait(); err != nil {
		t.Fatal(err)
	}
}

func TestPluginHostWindowsSandboxGuest(t *testing.T) {
	if os.Getenv("NRE_TEST_WINDOWS_SANDBOX_GUEST") != "1" {
		t.Skip("sandbox guest helper")
	}
}
