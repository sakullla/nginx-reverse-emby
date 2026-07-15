//go:build linux

package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)

type hotRestartPacketTestProcess struct {
	name    string
	args    []string
	timeout time.Duration
}

func TestHotRestartPacketProtocolMatrix(t *testing.T) {
	if testing.Short() {
		t.Skip("cross-protocol packet hot-restart matrix")
	}
	cases := []hotRestartPacketTestProcess{
		{
			name: "http3",
			args: []string{"./internal/modules/http", "^TestHTTP3ProcessPacketHandoffRoutesOldNewAndAbort$|^TestHTTPIngressConsumesProcessPacketDescriptor$"},
		},
		{
			name: "l4_udp",
			args: []string{"./internal/modules/l4", "^TestL4UDPProcessPacketHandoffRoutesOldNewAndAbort$|^TestL4UDPIngressConsumesProcessPacketDescriptor$"},
		},
		{
			name: "relay_quic_uot",
			args: []string{"./internal/modules/relay", "^TestRelayQUICProcessPacketHandoffRoutesOldNewAndAbort$|^TestRelayUOTUsesExistingTLSTCPStreamHandoff$|^TestRelayQUICIngressConsumesProcessPacketDescriptor$"},
		},
		{
			name: "wireguard",
			args: []string{"./internal/modules/wireguard", "^TestProcessWireGuardBindHandoffPinsOldAndForwardsNew$|^TestProcessWireGuardBindConsumesImportedDescriptor$|^TestProcessWireGuardClassifierCleanupLinearizesRealBrokerReassociation$|^TestWireGuardEndpointReleaseLinearizesSuccessorReceiverClaim$"},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			runHotRestartPacketTestProcess(t, testCase)
		})
	}
}

func runHotRestartPacketTestProcess(t *testing.T, testCase hotRestartPacketTestProcess) {
	t.Helper()
	if err := enableHotRestartPacketChildSubreaper(); err != nil {
		t.Fatalf("%s enable child subreaper: %v", testCase.name, err)
	}
	timeout := testCase.timeout
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	ctx, cancel := context.WithTimeout(t.Context(), timeout)
	defer cancel()
	innerTimeout := timeout + 30*time.Second
	args := []string{"test", "-race", "-v", "-timeout=" + innerTimeout.String(), "-count=1", testCase.args[0], "-run", testCase.args[1]}
	cmd := exec.Command("go", args...)
	cmd.Dir = hotRestartPacketModuleRoot(t)
	cmd.Env = os.Environ()
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Start(); err != nil {
		t.Fatalf("%s child start failed: %v", testCase.name, err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	var err error
	select {
	case err = <-done:
	case <-ctx.Done():
		processGroupID := cmd.Process.Pid
		killErr := syscall.Kill(-processGroupID, syscall.SIGKILL)
		select {
		case err = <-done:
		case <-time.After(5 * time.Second):
			t.Fatalf("%s direct child did not exit within 5s after process-group kill (%v); output:\n%s", testCase.name, killErr, output.String())
		}
		groupErr := waitHotRestartPacketProcessGroupGone(processGroupID, 5*time.Second)
		if killErr != nil && !errors.Is(killErr, syscall.ESRCH) {
			t.Fatalf("%s process-group kill failed: %v; direct wait: %v; group check: %v; output:\n%s", testCase.name, killErr, err, groupErr, output.String())
		}
		if groupErr != nil {
			t.Fatalf("%s descendants remained after process-group kill: %v; direct wait: %v; output:\n%s", testCase.name, groupErr, err, output.String())
		}
		t.Fatalf("%s child timed out after %s; process group terminated and direct child reaped: %v; output:\n%s", testCase.name, timeout, err, output.String())
	}
	if err != nil {
		cleanupErr := terminateHotRestartPacketProcessGroup(cmd.Process.Pid)
		t.Fatalf("%s child failed: %v; process-group cleanup: %v; output:\n%s", testCase.name, err, cleanupErr, output.String())
	}
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		t.Fatalf("%s child was not reaped", testCase.name)
	}
	if groupErr := waitHotRestartPacketProcessGroupGone(cmd.Process.Pid, 5*time.Second); groupErr != nil {
		cleanupErr := terminateHotRestartPacketProcessGroup(cmd.Process.Pid)
		t.Fatalf("%s descendants remained after successful child exit: %v; process-group cleanup: %v; output:\n%s", testCase.name, groupErr, cleanupErr, output.String())
	}
	outputText := output.String()
	if !strings.Contains(outputText, "ok  ") && !strings.Contains(outputText, "ok\t") {
		t.Fatalf("%s child did not report a passing package: %s", testCase.name, outputText)
	}
	for _, testName := range hotRestartPacketExpectedTests(testCase.args[1]) {
		if !strings.Contains(outputText, "=== RUN   "+testName) || !strings.Contains(outputText, "--- PASS: "+testName) {
			t.Fatalf("%s child did not execute and pass %s; output:\n%s", testCase.name, testName, outputText)
		}
	}
}

func terminateHotRestartPacketProcessGroup(processGroupID int) error {
	killErr := syscall.Kill(-processGroupID, syscall.SIGKILL)
	if killErr != nil && !errors.Is(killErr, syscall.ESRCH) {
		return fmt.Errorf("kill process group %d: %w", processGroupID, killErr)
	}
	if groupErr := waitHotRestartPacketProcessGroupGone(processGroupID, 5*time.Second); groupErr != nil {
		return groupErr
	}
	return nil
}

func TestHotRestartPacketFailedChildCleansProcessGroup(t *testing.T) {
	if err := enableHotRestartPacketChildSubreaper(); err != nil {
		t.Fatalf("enable child subreaper: %v", err)
	}
	cmd := exec.Command("sh", "-c", "trap '' HUP; sleep 30 & exit 7")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start failed child fixture: %v", err)
	}
	processGroupID := cmd.Process.Pid
	if err := cmd.Wait(); err == nil {
		t.Fatal("failed child fixture unexpectedly succeeded")
	}
	if err := terminateHotRestartPacketProcessGroup(processGroupID); err != nil {
		t.Fatalf("clean failed child process group: %v", err)
	}
}

func enableHotRestartPacketChildSubreaper() error {
	const prSetChildSubreaper = 36
	_, _, errno := syscall.Syscall6(syscall.SYS_PRCTL, prSetChildSubreaper, 1, 0, 0, 0, 0)
	if errno != 0 {
		return errno
	}
	return nil
}

func waitHotRestartPacketProcessGroupGone(processGroupID int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		if err := reapHotRestartPacketProcessGroup(processGroupID); err != nil {
			return err
		}
		err := syscall.Kill(-processGroupID, 0)
		if errors.Is(err, syscall.ESRCH) {
			return nil
		}
		if err != nil && !errors.Is(err, syscall.EPERM) {
			return err
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("process group %d still exists after %s", processGroupID, timeout)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func reapHotRestartPacketProcessGroup(processGroupID int) error {
	for {
		var status syscall.WaitStatus
		pid, err := syscall.Wait4(-processGroupID, &status, syscall.WNOHANG, nil)
		if pid > 0 {
			continue
		}
		if err != nil && !errors.Is(err, syscall.ECHILD) {
			return fmt.Errorf("reap process group %d: %w", processGroupID, err)
		}
		return nil
	}
}

func hotRestartPacketExpectedTests(pattern string) []string {
	parts := strings.Split(pattern, "|")
	tests := make([]string, 0, len(parts))
	for _, part := range parts {
		name := strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(part), "^"), "$")
		if name != "" {
			tests = append(tests, name)
		}
	}
	return tests
}

func hotRestartPacketModuleRoot(t *testing.T) string {
	t.Helper()
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve packet matrix source path")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(currentFile), "..", ".."))
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("resolve go-agent module root %q: %v", root, err)
	}
	return root
}
