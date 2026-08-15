//go:build linux

package process

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
	sourceArtifact, _, digest := copyLinuxTestArtifact(t)
	artifact, artifactPath, err := createLinuxArtifactImage(sourceArtifact)
	if err != nil {
		t.Fatal(err)
	}
	defer artifact.Close()
	defer os.Remove(artifactPath)
	identity, err := linuxFileIdentity(artifact)
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
	endpointIdentity, _ := linuxFileIdentity(endpoint)
	credentialIdentity, _ := linuxFileIdentity(credential)
	cookieDigest, _ := digestAt(int(credential.Fd()), "cookie")
	generationDigest, _ := digestGenerationCookieAt(int(credential.Fd()), "generation-1")
	launcher, err := os.Open(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	defer launcher.Close()
	launcherIdentity, _ := linuxFileIdentity(launcher)
	launcherDigest, _ := digestOpenFile(launcher)
	base := linuxLaunchProtocol{
		Version:                linuxLauncherVersion,
		Generation:             "generation-1",
		ArtifactDigest:         digest,
		LauncherDigest:         launcherDigest,
		CookieDigest:           cookieDigest,
		GenerationCookieDigest: generationDigest,
		Artifact:               identity,
		Launcher:               launcherIdentity,
		Endpoint:               endpointIdentity,
		Credential:             credentialIdentity,
		ArtifactFD:             3,
		EndpointFD:             4,
		CredentialFD:           5,
		LauncherFD:             6,
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
			command := exec.Command(os.Args[0], linuxLauncherChildArg, "7")
			command.ExtraFiles = []*os.File{artifact, endpoint, credential, launcher, protocolFile}
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
	var output bytes.Buffer
	process, cleanup, err := (ExecRunner{}).Start(context.Background(), InstanceSpec{
		ID:          "launcher-live",
		Executable:  artifact.Name(),
		Args:        []string{"-test.run=^TestLinuxLauncherGuest$"},
		Environment: []string{"NRE_TEST_LINUX_LAUNCHER_GUEST=1"},
		Security: Security{
			Requirement:    testSandboxRequirement(Budget{CPUMillis: 1000, MemoryBytes: 2 << 30, Processes: 16, Files: 64}, false, true),
			ArtifactDigest: digest,
			Generation:     "generation-live",
			SandboxUID:     testLinuxSandboxUID(t),
		},
	}, linuxSandbox{prepareCgroup: func(Budget) (string, *os.File, error) {
		return "", nil, unix.EROFS
	}}, &output)
	if err != nil {
		t.Fatal(err)
	}
	if err := errors.Join(process.Wait(), cleanup()); err != nil {
		t.Fatalf("launcher live process: %v; output=%s", err, output.String())
	}
}

func TestLinuxLauncherInheritedImageSurvivesAtomicPathReplacement(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("isolated uid fallback requires root")
	}
	artifact, _, digest := copyLinuxTestArtifact(t)
	launcherPath := filepath.Join(t.TempDir(), "host")
	copyLinuxExecutable(t, os.Args[0], launcherPath, 0o755)
	replacement := filepath.Join(t.TempDir(), "replacement")
	if err := os.WriteFile(replacement, []byte("replaced"), 0o755); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(artifact.Name(), "-test.run=^TestLinuxLauncherGuest$")
	command.Env = []string{"NRE_TEST_LINUX_LAUNCHER_GUEST=1"}
	sandbox := linuxSandbox{
		prepareCgroup:   func(Budget) (string, *os.File, error) { return "", nil, unix.EROFS },
		openLauncher:    func() (*os.File, error) { return os.Open(launcherPath) },
		probeNamespaces: func(*os.File, bool, int) bool { return false },
	}
	startCleanup, processCleanup, afterStart, err := sandbox.Configure(command, Security{
		Requirement:    testSandboxRequirement(Budget{CPUMillis: 1000, MemoryBytes: 2 << 30, Processes: 16, Files: 64}, false, true),
		ArtifactDigest: digest,
		Generation:     "generation-atomic-replacement",
		SandboxUID:     testLinuxSandboxUID(t),
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
		t.Fatalf("FD-bound launcher after path replacement: %v; output=%s", err, output.String())
	}
}

func TestLinuxNamespaceMinimalRootLive(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("root mapping fixture")
	}
	launcher, err := os.Open("/proc/self/exe")
	if err != nil {
		t.Fatal(err)
	}
	defer launcher.Close()
	if !probeLinuxNamespaces(launcher, false, 0) {
		t.Skip("kernel blocks the complete user/PID/mount/network namespace profile")
	}
	source, _, digest := copyLinuxTestArtifact(t)
	artifact, artifactPath, err := createLinuxArtifactImage(source)
	if err != nil {
		t.Fatal(err)
	}
	defer artifact.Close()
	defer os.Remove(artifactPath)
	root := filepath.Join(t.TempDir(), "root")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	artifactIdentity, _ := linuxFileIdentity(artifact)
	launcherIdentity, _ := linuxFileIdentity(launcher)
	launcherDigest, _ := digestOpenFile(launcher)
	parents, err := captureLinuxNamespaceIDs(false)
	if err != nil {
		t.Fatal(err)
	}
	protocol := linuxLaunchProtocol{
		Version: linuxLauncherVersion, Generation: "namespace-live", ArtifactDigest: digest, LauncherDigest: launcherDigest,
		Artifact: artifactIdentity, Launcher: launcherIdentity, ArtifactFD: 3, ArtifactPath: artifactPath, LauncherFD: 4,
		Arguments:   []string{"-test.run=^TestLinuxLauncherGuest$"},
		Environment: []string{"NRE_TEST_LINUX_LAUNCHER_GUEST=1", "NRE_TEST_NAMESPACE_ROOT=1"}, Budget: Budget{CPUMillis: 1000, MemoryBytes: 2 << 30, Processes: 16, Files: 64},
		Namespaces: true, SandboxRoot: root, ParentNamespaces: parents,
	}
	protocolFile, err := createLinuxProtocolFile(protocol)
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(os.Args[0], linuxLauncherChildArg, "5")
	command.ExtraFiles = []*os.File{artifact, launcher, protocolFile}
	command.SysProcAttr = linuxSandboxSysProcAttrForUID(nil, false, true, 0)
	var output bytes.Buffer
	command.Stdout, command.Stderr = &output, &output
	if err := command.Run(); err != nil {
		t.Fatalf("namespace minimal root: %v; output=%s", err, output.String())
	}
}

func TestLinuxNamespaceProbeMapsCurrentNonRootUID(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("non-root mapping fixture")
	}
	launcher, err := os.Open("/proc/self/exe")
	if err != nil {
		t.Fatal(err)
	}
	defer launcher.Close()
	if !probeLinuxNamespaces(launcher, false, os.Geteuid()) {
		t.Skip("kernel blocks unprivileged user namespaces; production fails closed without cgroup and Landlock signal scope")
	}
}

func testLinuxSandboxUID(t *testing.T) int {
	t.Helper()
	if os.Geteuid() != 0 {
		t.Skip("isolated-uid fallback requires root when delegated cgroup v2 is unavailable")
	}
	return 2100000000
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
	if os.Getenv("NRE_TEST_NAMESPACE_ROOT") == "1" {
		if os.Getpid() != 1 {
			t.Fatalf("namespace plugin pid = %d, want 1", os.Getpid())
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

func TestLinuxFallbackHardMemoryLimit(t *testing.T) {
	process, cleanup, output := startLinuxResourceGuest(t, "memory", Budget{CPUMillis: 1000, MemoryBytes: 256 << 20, Processes: 32, Files: 64})
	if err := process.Wait(); err == nil {
		t.Fatalf("memory guest exceeded RLIMIT_DATA without termination; output=%s", output.String())
	}
	if err := cleanup(); err != nil {
		t.Fatal(err)
	}
}

func TestLinuxFallbackHardTaskLimit(t *testing.T) {
	process, cleanup, output := startLinuxResourceGuest(t, "tasks", Budget{CPUMillis: 1000, MemoryBytes: 1 << 30, Processes: 32, Files: 128})
	if err := process.Wait(); err == nil {
		t.Fatalf("task guest exceeded RLIMIT_NPROC without termination; output=%s", output.String())
	}
	if err := cleanup(); err != nil {
		t.Fatal(err)
	}
}

func TestLinuxFallbackCPUThrottleIsNotCumulativeKill(t *testing.T) {
	started := time.Now()
	process, cleanup, output := startLinuxResourceGuest(t, "cpu", Budget{CPUMillis: 500, MemoryBytes: 1 << 30, Processes: 32, Files: 64})
	if err := process.Wait(); err != nil {
		_ = cleanup()
		t.Fatalf("long-lived CPU guest was killed: %v; output=%s", err, output.String())
	}
	if elapsed := time.Since(started); elapsed < 1800*time.Millisecond {
		_ = cleanup()
		t.Fatalf("500 milli-CPU guest completed 1.2 CPU seconds without bounded throttling in %s", elapsed)
	}
	if err := cleanup(); err != nil {
		t.Fatal(err)
	}
}

func TestLinuxCleanupTerminatesDescendantsAfterLeaderExit(t *testing.T) {
	process, cleanup, output := startLinuxResourceGuest(t, "tree", Budget{CPUMillis: 1000, MemoryBytes: 1 << 30, Processes: 32, Files: 64})
	if err := process.Wait(); err != nil {
		_ = cleanup()
		t.Fatalf("tree leader: %v; output=%s", err, output.String())
	}
	marker := "child_pid="
	index := strings.Index(output.String(), marker)
	if index < 0 {
		_ = cleanup()
		t.Fatalf("tree leader did not report child pid; output=%s", output.String())
	}
	pidText := strings.Fields(output.String()[index+len(marker):])[0]
	pid, err := strconv.Atoi(pidText)
	if err != nil {
		_ = cleanup()
		t.Fatal(err)
	}
	if err := cleanup(); err != nil {
		t.Fatal(err)
	}
	if err := cleanup(); err != nil {
		t.Fatalf("repeated cleanup: %v", err)
	}
	if liveLinuxProcess(pid) {
		t.Fatalf("descendant pid %d survived process-group cleanup", pid)
	}
}

type linuxResourceOutput struct{ bytes.Buffer }

func startLinuxResourceGuest(t *testing.T, mode string, budget Budget) (ManagedProcess, func() error, *linuxResourceOutput) {
	t.Helper()
	if os.Geteuid() != 0 {
		t.Skip("hard RLIMIT_NPROC fallback fixture requires an isolated host uid")
	}
	artifact, _, digest := copyLinuxTestArtifact(t)
	endpoint := t.TempDir()
	uid := testLinuxSandboxUID(t)
	if err := os.Chown(endpoint, uid, uid); err != nil {
		t.Fatal(err)
	}
	output := &linuxResourceOutput{}
	process, cleanup, err := (ExecRunner{}).Start(context.Background(), InstanceSpec{
		ID:          "resource-" + mode,
		Executable:  artifact.Name(),
		Args:        []string{"-test.run=^TestLinuxResourceGuest$"},
		Environment: []string{"NRE_TEST_LINUX_RESOURCE=" + mode, "NRE_TEST_ENDPOINT_DIR=/proc/self/fd/4", "GOMAXPROCS=2"},
		Security: Security{
			Requirement:       testSandboxRequirement(budget, false, true),
			ArtifactDigest:    digest,
			Generation:        "resource-" + mode,
			SandboxUID:        uid,
			EndpointDirectory: endpoint,
		},
	}, linuxSandbox{
		prepareCgroup:   func(Budget) (string, *os.File, error) { return "", nil, unix.EROFS },
		probeNamespaces: func(*os.File, bool, int) bool { return false },
	}, output)
	if err != nil {
		t.Fatalf("start resource guest: %v; output=%s", err, output.String())
	}
	return process, cleanup, output
}

func TestLinuxResourceGuest(t *testing.T) {
	mode := os.Getenv("NRE_TEST_LINUX_RESOURCE")
	if mode == "" {
		t.Skip("resource guest helper")
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
		child := exec.Command("/proc/self/fd/3", "-test.run=^TestLinuxResourceGuest$")
		child.Env = append(os.Environ(), "NRE_TEST_LINUX_RESOURCE=sleeper")
		child.ExtraFiles = []*os.File{artifact}
		child.Stdin, child.Stdout, child.Stderr = os.Stdin, os.Stdin, os.Stdin
		if err := child.Start(); err != nil {
			t.Fatal(err)
		}
		_, _ = fmt.Printf("child_pid=%d\n", child.Process.Pid)
	case "sleeper":
		time.Sleep(time.Minute)
	default:
		t.Fatalf("unknown resource fixture %q", mode)
	}
}

func liveLinuxProcess(pid int) bool {
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

func copyLinuxExecutable(t *testing.T, sourcePath, targetPath string, mode os.FileMode) {
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
