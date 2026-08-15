//go:build linux

package pluginhost

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestPluginHostLinuxLauncherUsesInheritedBindingsAndNoHelpers(t *testing.T) {
	environment := backendChildEnvironment(
		[]string{"PATH=/usr/bin:/bin", "NRE_PLUGIN_ENDPOINT=unix:/host/rpc.sock", "NRE_PLUGIN_COOKIE_FILE=/host/cookie"},
		4,
		5,
		"/run/nre-plugin/rpc.sock",
	)
	joined := strings.Join(environment, " ")
	for _, required := range []string{"NRE_PLUGIN_ENDPOINT=unix:/proc/self/fd/4/rpc.sock", "NRE_PLUGIN_COOKIE_FILE=/proc/self/fd/5/cookie"} {
		if !strings.Contains(joined, required) {
			t.Fatalf("control launcher environment missing %q: %s", required, joined)
		}
	}
	for _, forbidden := range []string{"bwrap", "prlimit", "/host/rpc.sock", "/host/cookie"} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("control launcher environment contains %q: %s", forbidden, joined)
		}
	}
}

func TestPluginHostLinuxLauncherChildRejectsMismatchedInheritedBindings(t *testing.T) {
	sourceArtifact, _, digest := copyBackendLinuxTestArtifact(t)
	artifact, artifactPath, err := createBackendArtifactImage(sourceArtifact)
	if err != nil {
		t.Fatal(err)
	}
	defer artifact.Close()
	defer os.Remove(artifactPath)
	identity, err := backendFileIdentity(artifact)
	if err != nil {
		t.Fatal(err)
	}
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
	endpointIdentity, _ := backendFileIdentity(endpoint)
	credentialIdentity, _ := backendFileIdentity(credential)
	cookieDigest, _ := backendDigestAt(int(credential.Fd()), "cookie")
	generationDigest, _ := backendDigestGenerationCookieAt(int(credential.Fd()), "generation-1")
	launcher, err := os.Open(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	defer launcher.Close()
	launcherIdentity, _ := backendFileIdentity(launcher)
	launcherDigest, _ := backendDigestOpenFile(launcher)
	base := backendLaunchProtocol{
		Version:                backendLauncherVersion,
		Generation:             "generation-1",
		ArtifactDigest:         digest,
		LauncherDigest:         launcherDigest,
		CookieDigest:           cookieDigest,
		GenerationCookieDigest: generationDigest,
		Artifact:               identity,
		ArtifactPath:           artifactPath,
		Launcher:               launcherIdentity,
		Endpoint:               endpointIdentity,
		Credential:             credentialIdentity,
		ArtifactFD:             3,
		EndpointFD:             4,
		CredentialFD:           5,
		LauncherFD:             6,
		EndpointRequired:       true,
		Environment:            []string{"PATH=/usr/bin:/bin"},
		Budget:                 ProcessBudget{CPUMillis: 1000, MemoryBytes: 2 << 30, Processes: 16, Files: 64},
	}
	tests := map[string]struct {
		mutate func(*backendLaunchProtocol)
		want   string
	}{
		"artifact":   {func(protocol *backendLaunchProtocol) { protocol.ArtifactDigest = strings.Repeat("0", 64) }, "artifact descriptor digest mismatch"},
		"fd":         {func(protocol *backendLaunchProtocol) { protocol.Endpoint = credentialIdentity }, "control launcher endpoint descriptor: descriptor identity mismatch"},
		"cookie":     {func(protocol *backendLaunchProtocol) { protocol.CookieDigest = strings.Repeat("0", 64) }, "credential cookie digest mismatch"},
		"generation": {func(protocol *backendLaunchProtocol) { protocol.GenerationCookieDigest = strings.Repeat("0", 64) }, "credential generation binding mismatch"},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			protocol := base
			test.mutate(&protocol)
			protocolFile, err := createBackendProtocolFile(protocol)
			if err != nil {
				t.Fatal(err)
			}
			command := exec.Command(os.Args[0], backendLauncherChildArg, "7")
			command.ExtraFiles = []*os.File{artifact, endpoint, credential, launcher, protocolFile}
			var output bytes.Buffer
			command.Stderr = &output
			err = command.Run()
			var exitErr *exec.ExitError
			if !errors.As(err, &exitErr) || exitErr.ExitCode() != 125 {
				t.Fatalf("control launcher mismatch exit = %v, output=%s", err, output.String())
			}
			if !strings.Contains(output.String(), test.want) {
				t.Fatalf("control launcher mismatch output = %q, want %q", output.String(), test.want)
			}
		})
	}
}

func TestPluginHostLinuxLauncherCleanPathLiveProcess(t *testing.T) {
	artifact, _, digest := copyBackendLinuxTestArtifact(t)
	t.Setenv("PATH", t.TempDir())
	candidate := Candidate{
		Artifact:    Artifact{SHA256: digest},
		Identity:    Identity{Generation: "generation-live"},
		Requirement: testControlRequirement(ProcessBudget{CPUMillis: 1000, MemoryBytes: 2 << 30, Processes: 16, Files: 64}, false, true),
		sandboxUID:  testBackendLinuxSandboxUID(t),
	}
	launcher := ExecLauncher{configure: func(cmd *exec.Cmd, candidate Candidate) (func() error, func() error, func(int) error, error) {
		return configurePlatformSandboxWithOps(cmd, candidate, backendSandboxOps{
			prepareCgroup:   func(ProcessBudget) (string, *os.File, error) { return "", nil, unix.EROFS },
			probeNamespaces: func(*os.File, bool, int) bool { return false },
		})
	}}
	process, err := launcher.Start(context.Background(), artifact.Name(), []string{"-test.run=^TestPluginHostLinuxLauncherGuest$"}, []string{"NRE_TEST_CONTROL_LINUX_LAUNCHER_GUEST=1"}, io.Discard, candidate)
	if err != nil {
		t.Fatal(err)
	}
	if err := errors.Join(process.Wait(), process.(interface{ Cleanup() error }).Cleanup()); err != nil {
		t.Fatal(err)
	}
}

func TestPluginHostLinuxLauncherInheritedImageSurvivesAtomicPathReplacement(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("isolated uid fallback requires root")
	}
	artifact, _, digest := copyBackendLinuxTestArtifact(t)
	launcherPath := filepath.Join(t.TempDir(), "host")
	copyBackendLinuxExecutable(t, os.Args[0], launcherPath, 0o755)
	replacement := filepath.Join(t.TempDir(), "replacement")
	if err := os.WriteFile(replacement, []byte("replaced"), 0o755); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(artifact.Name(), "-test.run=^TestPluginHostLinuxLauncherGuest$")
	command.Env = []string{"NRE_TEST_CONTROL_LINUX_LAUNCHER_GUEST=1"}
	candidate := Candidate{
		Artifact:    Artifact{SHA256: digest},
		Identity:    Identity{Generation: "generation-atomic-replacement"},
		Requirement: testControlRequirement(ProcessBudget{CPUMillis: 1000, MemoryBytes: 2 << 30, Processes: 16, Files: 64}, false, true),
		sandboxUID:  testBackendLinuxSandboxUID(t),
	}
	startCleanup, processCleanup, afterStart, err := configurePlatformSandboxWithOps(command, candidate, backendSandboxOps{
		prepareCgroup:   func(ProcessBudget) (string, *os.File, error) { return "", nil, unix.EROFS },
		openLauncher:    func() (*os.File, error) { return os.Open(launcherPath) },
		probeNamespaces: func(*os.File, bool, int) bool { return false },
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacement, launcherPath); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	command.Stdout, command.Stderr = &output, &output
	if err := command.Start(); err != nil {
		_ = startCleanup()
		_ = processCleanup()
		t.Fatal(err)
	}
	if err := startCleanup(); err != nil {
		t.Fatal(err)
	}
	if err := afterStart(command.Process.Pid); err != nil {
		_ = command.Process.Kill()
		_ = processCleanup()
		t.Fatal(err)
	}
	if err := errors.Join(command.Wait(), processCleanup()); err != nil {
		t.Fatalf("FD-bound control launcher after path replacement: %v; output=%s", err, output.String())
	}
}

func TestPluginHostLinuxNamespaceMinimalRootLive(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("root mapping fixture")
	}
	launcher, err := os.Open("/proc/self/exe")
	if err != nil {
		t.Fatal(err)
	}
	defer launcher.Close()
	if !probeBackendNamespaces(launcher, false, 0) {
		t.Skip("kernel blocks the complete control-plane namespace profile")
	}
	source, _, digest := copyBackendLinuxTestArtifact(t)
	artifact, artifactPath, err := createBackendArtifactImage(source)
	if err != nil {
		t.Fatal(err)
	}
	defer artifact.Close()
	defer os.Remove(artifactPath)
	root := filepath.Join(t.TempDir(), "root")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	artifactIdentity, _ := backendFileIdentity(artifact)
	launcherIdentity, _ := backendFileIdentity(launcher)
	launcherDigest, _ := backendDigestOpenFile(launcher)
	parents, err := captureBackendNamespaceIDs(false)
	if err != nil {
		t.Fatal(err)
	}
	protocol := backendLaunchProtocol{
		Version: backendLauncherVersion, Generation: "namespace-live", ArtifactDigest: digest, LauncherDigest: launcherDigest,
		Artifact: artifactIdentity, Launcher: launcherIdentity, ArtifactFD: 3, ArtifactPath: artifactPath, LauncherFD: 4,
		Arguments:   []string{"-test.run=^TestPluginHostLinuxLauncherGuest$"},
		Environment: []string{"NRE_TEST_CONTROL_LINUX_LAUNCHER_GUEST=1", "NRE_TEST_CONTROL_NAMESPACE_ROOT=1"},
		Budget:      ProcessBudget{CPUMillis: 1000, MemoryBytes: 2 << 30, Processes: 16, Files: 64},
		Namespaces:  true, SandboxRoot: root, ParentNamespaces: parents,
	}
	protocolFile, err := createBackendProtocolFile(protocol)
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(os.Args[0], backendLauncherChildArg, "5")
	command.ExtraFiles = []*os.File{artifact, launcher, protocolFile}
	command.SysProcAttr = backendLinuxSandboxSysProcAttrForUID(nil, false, true, 0)
	var output bytes.Buffer
	command.Stdout, command.Stderr = &output, &output
	if err := command.Run(); err != nil {
		t.Fatalf("control namespace minimal root: %v; output=%s", err, output.String())
	}
}

func TestPluginHostLinuxNamespaceProbeMapsCurrentNonRootUID(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("non-root mapping fixture")
	}
	launcher, err := os.Open("/proc/self/exe")
	if err != nil {
		t.Fatal(err)
	}
	defer launcher.Close()
	if !probeBackendNamespaces(launcher, false, os.Geteuid()) {
		t.Skip("kernel blocks unprivileged user namespaces")
	}
}

func TestPluginHostLinuxLauncherGuest(t *testing.T) {
	if os.Getenv("NRE_TEST_CONTROL_LINUX_LAUNCHER_GUEST") != "1" {
		t.Skip("control launcher guest helper")
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
	if os.Getenv("NRE_TEST_CONTROL_NAMESPACE_ROOT") == "1" {
		if os.Getpid() != 1 {
			t.Fatalf("namespace control plugin pid = %d, want 1", os.Getpid())
		}
		if _, err := os.Stat("/etc/passwd"); err == nil {
			t.Fatal("minimal root exposed /etc/passwd")
		}
		if err := os.WriteFile("/plugin/write", []byte("x"), 0o600); err == nil {
			t.Fatal("minimal root allowed a persistent write outside the endpoint")
		}
		if fd, err := unix.Socket(unix.AF_INET, unix.SOCK_STREAM, 0); !errors.Is(err, unix.EPERM) {
			if err == nil {
				_ = unix.Close(fd)
			}
			t.Fatalf("isolated network socket result = %v, want EPERM", err)
		}
	}
}

func TestPluginHostLinuxFallbackHardMemoryLimit(t *testing.T) {
	process, output := startBackendLinuxResourceGuest(t, "memory", ProcessBudget{CPUMillis: 1000, MemoryBytes: 256 << 20, Processes: 32, Files: 64})
	if err := process.Wait(); err == nil {
		t.Fatalf("control memory guest exceeded RLIMIT_DATA without termination; output=%s", output.String())
	}
	if err := process.(interface{ Cleanup() error }).Cleanup(); err != nil {
		t.Fatal(err)
	}
}

func TestPluginHostLinuxFallbackHardTaskLimit(t *testing.T) {
	process, output := startBackendLinuxResourceGuest(t, "tasks", ProcessBudget{CPUMillis: 1000, MemoryBytes: 1 << 30, Processes: 32, Files: 128})
	if err := process.Wait(); err == nil {
		t.Fatalf("control task guest exceeded RLIMIT_NPROC without termination; output=%s", output.String())
	}
	if err := process.(interface{ Cleanup() error }).Cleanup(); err != nil {
		t.Fatal(err)
	}
}

func TestPluginHostLinuxFallbackCPUThrottleIsNotCumulativeKill(t *testing.T) {
	started := time.Now()
	process, output := startBackendLinuxResourceGuest(t, "cpu", ProcessBudget{CPUMillis: 500, MemoryBytes: 1 << 30, Processes: 32, Files: 64})
	if err := process.Wait(); err != nil {
		_ = process.(interface{ Cleanup() error }).Cleanup()
		t.Fatalf("long-lived control CPU guest was killed: %v; output=%s", err, output.String())
	}
	if elapsed := time.Since(started); elapsed < 1800*time.Millisecond {
		_ = process.(interface{ Cleanup() error }).Cleanup()
		t.Fatalf("500 milli-CPU control guest completed 1.2 CPU seconds without bounded throttling in %s", elapsed)
	}
	if err := process.(interface{ Cleanup() error }).Cleanup(); err != nil {
		t.Fatal(err)
	}
}

func TestPluginHostLinuxCleanupTerminatesDescendantsAfterLeaderExit(t *testing.T) {
	process, output := startBackendLinuxResourceGuest(t, "tree", ProcessBudget{CPUMillis: 1000, MemoryBytes: 1 << 30, Processes: 32, Files: 64})
	if err := process.Wait(); err != nil {
		_ = process.(interface{ Cleanup() error }).Cleanup()
		t.Fatalf("control tree leader: %v; output=%s", err, output.String())
	}
	marker := "child_pid="
	index := strings.Index(output.String(), marker)
	if index < 0 {
		_ = process.(interface{ Cleanup() error }).Cleanup()
		t.Fatalf("control tree leader did not report child pid; output=%s", output.String())
	}
	pid, err := strconv.Atoi(strings.Fields(output.String()[index+len(marker):])[0])
	if err != nil {
		_ = process.(interface{ Cleanup() error }).Cleanup()
		t.Fatal(err)
	}
	cleanup := process.(interface{ Cleanup() error }).Cleanup
	if err := cleanup(); err != nil {
		t.Fatal(err)
	}
	if err := cleanup(); err != nil {
		t.Fatalf("repeated control cleanup: %v", err)
	}
	if liveBackendLinuxProcess(pid) {
		t.Fatalf("control descendant pid %d survived process-group cleanup", pid)
	}
}

func startBackendLinuxResourceGuest(t *testing.T, mode string, budget ProcessBudget) (Process, *bytes.Buffer) {
	t.Helper()
	if os.Geteuid() != 0 {
		t.Skip("hard RLIMIT_NPROC fallback fixture requires an isolated host uid")
	}
	artifact, _, digest := copyBackendLinuxTestArtifact(t)
	candidate := Candidate{
		Artifact:    Artifact{SHA256: digest},
		Identity:    Identity{Generation: "resource-" + mode},
		Requirement: testControlRequirement(budget, false, true),
		sandboxUID:  testBackendLinuxSandboxUID(t),
	}
	launcher := ExecLauncher{configure: func(cmd *exec.Cmd, candidate Candidate) (func() error, func() error, func(int) error, error) {
		return configurePlatformSandboxWithOps(cmd, candidate, backendSandboxOps{
			prepareCgroup:   func(ProcessBudget) (string, *os.File, error) { return "", nil, unix.EROFS },
			probeNamespaces: func(*os.File, bool, int) bool { return false },
		})
	}}
	output := &bytes.Buffer{}
	process, err := launcher.Start(context.Background(), artifact.Name(), []string{"-test.run=^TestPluginHostLinuxResourceGuest$"}, []string{"NRE_TEST_CONTROL_LINUX_RESOURCE=" + mode, "GOMAXPROCS=2"}, output, candidate)
	if err != nil {
		t.Fatalf("start control resource guest: %v; output=%s", err, output.String())
	}
	return process, output
}

func TestPluginHostLinuxResourceGuest(t *testing.T) {
	mode := os.Getenv("NRE_TEST_CONTROL_LINUX_RESOURCE")
	if mode == "" {
		t.Skip("control resource guest helper")
	}
	switch mode {
	case "memory":
		memory := make([]byte, 512<<20)
		for offset := 0; offset < len(memory); offset += os.Getpagesize() {
			memory[offset] = 1
		}
		t.Fatalf("allocated %d bytes beyond hard memory limit", len(memory))
	case "tasks":
		for index := 0; index < 128; index++ {
			started := make(chan struct{})
			go func() {
				runtime.LockOSThread()
				close(started)
				select {}
			}()
			<-started
		}
		t.Fatal("created threads beyond hard task limit")
	case "cpu":
		var usage unix.Rusage
		for {
			if err := unix.Getrusage(unix.RUSAGE_SELF, &usage); err != nil {
				t.Fatal(err)
			}
			cpu := time.Duration(usage.Utime.Sec+usage.Stime.Sec)*time.Second + time.Duration(usage.Utime.Usec+usage.Stime.Usec)*time.Microsecond
			if cpu >= 1200*time.Millisecond {
				return
			}
		}
	case "tree":
		artifact, err := os.Open("/proc/self/fd/3")
		if err != nil {
			t.Fatal(err)
		}
		defer artifact.Close()
		child := exec.Command("/proc/self/fd/3", "-test.run=^TestPluginHostLinuxResourceGuest$")
		child.Env = append(os.Environ(), "NRE_TEST_CONTROL_LINUX_RESOURCE=sleeper")
		child.ExtraFiles = []*os.File{artifact}
		child.Stdin, child.Stdout, child.Stderr = os.Stdin, os.Stdin, os.Stdin
		if err := child.Start(); err != nil {
			t.Fatal(err)
		}
		_, _ = fmt.Printf("child_pid=%d\n", child.Process.Pid)
	case "sleeper":
		time.Sleep(time.Minute)
	default:
		t.Fatalf("unknown control resource fixture %q", mode)
	}
}

func liveBackendLinuxProcess(pid int) bool {
	body, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if err != nil {
		return false
	}
	end := strings.LastIndexByte(string(body), ')')
	if end < 0 {
		return true
	}
	fields := strings.Fields(string(body[end+1:]))
	return len(fields) == 0 || fields[0] != "Z"
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

func copyBackendLinuxTestArtifact(t *testing.T) (*os.File, backendFDIdentity, string) {
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
	identity, err := backendFileIdentity(artifact)
	if err != nil {
		t.Fatal(err)
	}
	return artifact, identity, hex.EncodeToString(hash.Sum(nil))
}

func testBackendLinuxSandboxUID(t *testing.T) int {
	t.Helper()
	if os.Geteuid() != 0 {
		t.Skip("isolated-uid fallback requires root when delegated cgroup v2 is unavailable")
	}
	return 2100000001
}

func copyBackendLinuxExecutable(t *testing.T, sourcePath, targetPath string, mode os.FileMode) {
	t.Helper()
	source, err := os.Open(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	target, err := os.OpenFile(targetPath, os.O_CREATE|os.O_WRONLY|os.O_EXCL, mode)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(target, source); err != nil {
		_ = target.Close()
		t.Fatal(err)
	}
	if err := target.Close(); err != nil {
		t.Fatal(err)
	}
}
