//go:build linux

package app

import (
	"bytes"
	"context"
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
	timeout := testCase.timeout
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	ctx, cancel := context.WithTimeout(t.Context(), timeout)
	defer cancel()
	args := []string{"test", "-race", "-v", "-timeout=90s", "-count=1", testCase.args[0], "-run", testCase.args[1]}
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
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		err = <-done
		t.Fatalf("%s child timed out after %s and process group was reaped: %v; output:\n%s", testCase.name, timeout, err, output.String())
	}
	if err != nil {
		t.Fatalf("%s child failed: %v; output:\n%s", testCase.name, err, output.String())
	}
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		t.Fatalf("%s child was not reaped", testCase.name)
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

func hotRestartPacketProcessDescription(testCase hotRestartPacketTestProcess) string {
	return fmt.Sprintf("%s:%s", testCase.args[0], testCase.args[1])
}
