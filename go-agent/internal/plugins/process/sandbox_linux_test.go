//go:build linux

package process

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLinuxSandboxBindsCgroupBeforeExecAndUsesMinimalFilesystem(t *testing.T) {
	security := Security{Requirement: testSandboxRequirement(Budget{Files: 32, Network: false}, false, true), EndpointDirectory: "/managed/attempt/endpoint", CredentialDirectory: "/managed/attempt/credentials", GuestEndpoint: "/run/nre-plugin/rpc.sock"}
	args := linuxSandboxArguments("/usr/bin/bwrap", "/usr/bin/prlimit", "/managed/instance/plugin", []string{"--guest"}, []string{"PATH=/usr/bin:/bin"}, security, 3)
	joined := strings.Join(args, " ")
	if strings.Contains(joined, "--ro-bind / /") || strings.Contains(joined, "/home") || strings.Contains(joined, "/root") || strings.Contains(joined, "/panel") {
		t.Fatalf("sandbox exposed host filesystem: %s", joined)
	}
	for _, required := range []string{"--ro-bind /managed/instance/plugin /plugin/plugin", "--bind /managed/attempt/endpoint /run/nre-plugin", "--ro-bind /managed/attempt/credentials /run/nre-plugin-credentials", "NRE_PLUGIN_ENDPOINT unix:/run/nre-plugin/rpc.sock", "--unshare-net", "--seccomp 3", "/runtime/prlimit --nofile=32:32 -- /plugin/plugin"} {
		if !strings.Contains(joined, required) {
			t.Fatalf("sandbox argument %q missing from %s", required, joined)
		}
	}
	attributes := linuxSandboxSysProcAttr(19)
	if !attributes.UseCgroupFD || attributes.CgroupFD != 19 {
		t.Fatalf("cgroup is not bound before guest exec: %+v", attributes)
	}
}

func TestLinuxSandboxLiveProcess(t *testing.T) {
	if os.Getenv("NRE_TEST_LINUX_SANDBOX") != "1" {
		t.Skip("set NRE_TEST_LINUX_SANDBOX=1 on a Linux cgroup v2 host")
	}
	sandbox := newPlatformSandbox()
	if !sandbox.Available() {
		t.Fatal("Linux sandbox prerequisites are unavailable")
	}
	process, cleanup, err := (ExecRunner{}).Start(context.Background(), InstanceSpec{
		ID:          "sandbox-live",
		Executable:  os.Args[0],
		Args:        []string{"-test.run=^TestLinuxSandboxGuest$"},
		Environment: []string{"NRE_TEST_LINUX_SANDBOX_GUEST=1"},
		Security: Security{Requirement: testSandboxRequirement(Budget{
			CPUMillis:   1000,
			MemoryBytes: 256 << 20,
			Processes:   8,
			Files:       64,
		}, false, true)},
	}, sandbox, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	waitErr := process.Wait()
	cleanupErr := cleanup()
	if err := errors.Join(waitErr, cleanupErr); err != nil {
		t.Fatal(err)
	}
}

func TestLinuxSandboxGuest(t *testing.T) {
	if os.Getenv("NRE_TEST_LINUX_SANDBOX_GUEST") != "1" {
		t.Skip("sandbox guest helper")
	}
	if _, err := os.Stat("/panel"); !os.IsNotExist(err) {
		t.Fatalf("host panel path is visible: %v", err)
	}
}

func TestLinuxSandboxEnablesControllersBeforeCreatingInstance(t *testing.T) {
	root := filepath.Join(t.TempDir(), "nre-plugins")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "cgroup.controllers"), []byte("cpuset cpu io memory pids"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "cgroup.subtree_control"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	directory, file, err := prepareLinuxCgroupAt(root, Budget{CPUMillis: 100, MemoryBytes: 1 << 20, Processes: 2, Files: 16})
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
