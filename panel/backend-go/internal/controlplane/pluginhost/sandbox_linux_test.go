//go:build linux

package pluginhost

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPluginHostLinuxSandboxBindsBeforeExecAndUsesMinimalFilesystem(t *testing.T) {
	candidate := Candidate{Budget: ProcessBudget{Files: 32, Network: false}, endpointDirectory: "/managed/attempt/endpoint", credentialDirectory: "/managed/attempt/credentials", guestEndpoint: "/run/nre-plugin/rpc.sock"}
	args := backendLinuxSandboxArguments("/usr/bin/bwrap", "/usr/bin/prlimit", "/managed/instance/plugin", []string{"--guest"}, []string{"PATH=/usr/bin:/bin"}, candidate, 3)
	joined := strings.Join(args, " ")
	if strings.Contains(joined, "--ro-bind / /") || strings.Contains(joined, "/home") || strings.Contains(joined, "/root") || strings.Contains(joined, "/panel") {
		t.Fatalf("sandbox exposed host filesystem: %s", joined)
	}
	for _, required := range []string{"--ro-bind /managed/instance/plugin /plugin/plugin", "--bind /managed/attempt/endpoint /run/nre-plugin", "--ro-bind /managed/attempt/credentials /run/nre-plugin-credentials", "NRE_PLUGIN_ENDPOINT unix:/run/nre-plugin/rpc.sock", "--unshare-net", "--seccomp 3", "/runtime/prlimit --nofile=32:32 -- /plugin/plugin"} {
		if !strings.Contains(joined, required) {
			t.Fatalf("sandbox argument %q missing from %s", required, joined)
		}
	}
	attributes := backendLinuxSandboxSysProcAttr(19)
	if !attributes.UseCgroupFD || attributes.CgroupFD != 19 {
		t.Fatalf("cgroup is not bound before guest exec: %+v", attributes)
	}
}

func TestPluginHostLinuxSandboxLiveProcess(t *testing.T) {
	if os.Getenv("NRE_TEST_LINUX_SANDBOX") != "1" {
		t.Skip("set NRE_TEST_LINUX_SANDBOX=1 on a Linux cgroup v2 host")
	}
	candidate := Candidate{Budget: ProcessBudget{CPUMillis: 1000, MemoryBytes: 256 << 20, Processes: 8, Files: 64}}
	process, err := (ExecLauncher{}).Start(context.Background(), os.Args[0], []string{"-test.run=^TestPluginHostLinuxSandboxGuest$"}, []string{"NRE_TEST_LINUX_SANDBOX_GUEST=1"}, io.Discard, candidate)
	if err != nil {
		t.Fatal(err)
	}
	if err := process.Wait(); err != nil {
		t.Fatal(err)
	}
}

func TestPluginHostLinuxSandboxGuest(t *testing.T) {
	if os.Getenv("NRE_TEST_LINUX_SANDBOX_GUEST") != "1" {
		t.Skip("sandbox guest helper")
	}
	if _, err := os.Stat("/panel"); !os.IsNotExist(err) {
		t.Fatalf("host panel path is visible: %v", err)
	}
}

func TestBackendLinuxSandboxEnablesControllersBeforeCreatingInstance(t *testing.T) {
	root := filepath.Join(t.TempDir(), "nre-control-plugins")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "cgroup.controllers"), []byte("cpu memory pids"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "cgroup.subtree_control"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	directory, file, err := prepareBackendCgroupAt(root, ProcessBudget{CPUMillis: 100, MemoryBytes: 1 << 20, Processes: 2, Files: 16})
	if err != nil {
		t.Fatal(err)
	}
	file.Close()
	defer os.RemoveAll(directory)
	control, err := os.ReadFile(filepath.Join(root, "cgroup.subtree_control"))
	if err != nil {
		t.Fatal(err)
	}
	if string(control) != "+cpu +memory +pids" {
		t.Fatalf("subtree controllers = %q", control)
	}
}
