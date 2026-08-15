//go:build linux

package process

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func TestLinuxLauncherUsesInheritedBindingsAndNoHelperLookup(t *testing.T) {
	environment := linuxChildEnvironment(
		[]string{"PATH=/usr/bin:/bin", "NRE_PLUGIN_ENDPOINT=unix:/host/rpc.sock", "NRE_PLUGIN_COOKIE_FILE=/host/cookie"},
		4,
		5,
		"/run/nre-plugin/rpc.sock",
	)
	joined := strings.Join(environment, " ")
	for _, required := range []string{"NRE_PLUGIN_ENDPOINT=unix:/proc/self/fd/4/rpc.sock", "NRE_PLUGIN_COOKIE_FILE=/proc/self/fd/5/cookie"} {
		if !strings.Contains(joined, required) {
			t.Fatalf("child environment missing %q: %s", required, joined)
		}
	}
	for _, forbidden := range []string{"bwrap", "prlimit", "/host/rpc.sock", "/host/cookie"} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("child environment exposes external launcher dependency %q: %s", forbidden, joined)
		}
	}
	attributes := linuxSandboxSysProcAttr(nil, false, true)
	if !attributes.Setpgid || attributes.Pdeathsig != unix.SIGKILL || attributes.UseCgroupFD || attributes.Cloneflags&unix.CLONE_NEWNET == 0 || attributes.Cloneflags&unix.CLONE_NEWUSER == 0 {
		t.Fatalf("launcher process attributes = %+v", attributes)
	}
}

func TestLinuxLauncherChildRejectsMismatchedInheritedBindings(t *testing.T) {
	artifact, identity, digest := copyLinuxTestArtifact(t)
	endpointPath := filepath.Join(t.TempDir(), "e")
	credentialPath := filepath.Join(t.TempDir(), "c")
	if err := os.Mkdir(endpointPath, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(credentialPath, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(credentialPath, "cookie"), []byte("cookie"), 0o600); err != nil {
		t.Fatal(err)
	}
	endpoint, err := os.Open(endpointPath)
	if err != nil {
		t.Fatal(err)
	}
	defer endpoint.Close()
	credential, err := os.Open(credentialPath)
	if err != nil {
		t.Fatal(err)
	}
	defer credential.Close()
	endpointIdentity, _ := linuxFileIdentity(endpoint)
	credentialIdentity, _ := linuxFileIdentity(credential)
	cookieDigest, _ := digestAt(int(credential.Fd()), "cookie")
	generationDigest, _ := digestGenerationCookieAt(int(credential.Fd()), "generation-1")
	base := linuxLaunchProtocol{
		Version:                linuxLauncherVersion,
		Generation:             "generation-1",
		ArtifactDigest:         digest,
		CookieDigest:           cookieDigest,
		GenerationCookieDigest: generationDigest,
		Artifact:               identity,
		Endpoint:               endpointIdentity,
		Credential:             credentialIdentity,
		ArtifactFD:             3,
		EndpointFD:             4,
		CredentialFD:           5,
		EndpointRequired:       true,
		Environment:            []string{"PATH=/usr/bin:/bin"},
		Budget:                 Budget{CPUMillis: 1000, MemoryBytes: 2 << 30, Processes: 16, Files: 64},
	}
	tests := map[string]struct {
		mutate func(*linuxLaunchProtocol)
		want   string
	}{
		"artifact":   {func(protocol *linuxLaunchProtocol) { protocol.ArtifactDigest = strings.Repeat("0", 64) }, "artifact descriptor digest mismatch"},
		"fd":         {func(protocol *linuxLaunchProtocol) { protocol.Endpoint = credentialIdentity }, "endpoint descriptor: descriptor identity mismatch"},
		"cookie":     {func(protocol *linuxLaunchProtocol) { protocol.CookieDigest = strings.Repeat("0", 64) }, "credential cookie digest mismatch"},
		"generation": {func(protocol *linuxLaunchProtocol) { protocol.GenerationCookieDigest = strings.Repeat("0", 64) }, "credential generation binding mismatch"},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			protocol := base
			test.mutate(&protocol)
			protocolFile, err := createLinuxProtocolFile(protocol)
			if err != nil {
				t.Fatal(err)
			}
			command := exec.Command(os.Args[0], linuxLauncherChildArg, "6")
			command.ExtraFiles = []*os.File{artifact, endpoint, credential, protocolFile}
			var output bytes.Buffer
			command.Stderr = &output
			err = command.Run()
			var exitErr *exec.ExitError
			if !errors.As(err, &exitErr) || exitErr.ExitCode() != 125 {
				t.Fatalf("launcher mismatch exit = %v, output=%s", err, output.String())
			}
			if !strings.Contains(output.String(), test.want) {
				t.Fatalf("launcher mismatch output = %q, want %q", output.String(), test.want)
			}
		})
	}
}

func TestLinuxLauncherCleanPathLiveProcess(t *testing.T) {
	artifact, _, digest := copyLinuxTestArtifact(t)
	t.Setenv("PATH", t.TempDir())
	process, cleanup, err := (ExecRunner{}).Start(context.Background(), InstanceSpec{
		ID:          "launcher-live",
		Executable:  artifact.Name(),
		Args:        []string{"-test.run=^TestLinuxLauncherGuest$"},
		Environment: []string{"NRE_TEST_LINUX_LAUNCHER_GUEST=1"},
		Security: Security{
			Requirement:    testSandboxRequirement(Budget{CPUMillis: 1000, MemoryBytes: 2 << 30, Processes: 16, Files: 64}, false, true),
			ArtifactDigest: digest,
			Generation:     "generation-live",
		},
	}, linuxSandbox{prepareCgroup: func(Budget) (string, *os.File, error) {
		return "", nil, unix.EROFS
	}}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if err := errors.Join(process.Wait(), cleanup()); err != nil {
		t.Fatal(err)
	}
}

func TestLinuxLauncherGuest(t *testing.T) {
	if os.Getenv("NRE_TEST_LINUX_LAUNCHER_GUEST") != "1" {
		t.Skip("launcher guest helper")
	}
	var files unix.Rlimit
	if err := unix.Getrlimit(unix.RLIMIT_NOFILE, &files); err != nil {
		t.Fatal(err)
	}
	if files.Cur != 64 || files.Max != 64 {
		t.Fatalf("RLIMIT_NOFILE = %+v", files)
	}
	var cpu unix.Rlimit
	if err := unix.Getrlimit(unix.RLIMIT_CPU, &cpu); err != nil {
		t.Fatal(err)
	}
	if cpu.Cur <= 1 {
		t.Fatalf("RLIMIT_CPU unexpectedly limits a long-lived plugin: %+v", cpu)
	}
	if err := unix.Mount("none", "/", "", 0, ""); !errors.Is(err, unix.EPERM) {
		t.Fatalf("seccomp mount result = %v, want EPERM", err)
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

func copyLinuxTestArtifact(t *testing.T) (*os.File, linuxFDIdentity, string) {
	t.Helper()
	source, err := os.Open(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	path := filepath.Join(t.TempDir(), "plugin")
	target, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o500)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.New()
	if _, err := io.Copy(io.MultiWriter(target, hash), source); err != nil {
		t.Fatal(err)
	}
	if err := target.Close(); err != nil {
		t.Fatal(err)
	}
	artifact, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = artifact.Close() })
	identity, err := linuxFileIdentity(artifact)
	if err != nil {
		t.Fatal(err)
	}
	return artifact, identity, hex.EncodeToString(hash.Sum(nil))
}
