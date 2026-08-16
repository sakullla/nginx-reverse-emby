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
	if !attributes.Setpgid || attributes.Pdeathsig != unix.SIGKILL || attributes.UseCgroupFD || attributes.Cloneflags != 0 || attributes.Credential != nil {
		t.Fatalf("launcher process attributes = %+v", attributes)
	}
	final := linuxFinalUserNamespaceSysProcAttr(2100000000, false)
	if final.Cloneflags&unix.CLONE_NEWNET == 0 || final.Cloneflags&unix.CLONE_NEWUSER == 0 || final.Cloneflags&unix.CLONE_NEWPID == 0 || final.Cloneflags&unix.CLONE_NEWNS == 0 {
		t.Fatalf("final launcher process attributes = %+v", final)
	}
	if final.Credential == nil || final.Credential.Uid != 0 || final.Credential.Gid != 0 || !final.Credential.NoSetGroups {
		t.Fatalf("final launcher namespace credential = %+v", final.Credential)
	}
	if connected := linuxFinalUserNamespaceSysProcAttr(2100000000, true); connected.Cloneflags&unix.CLONE_NEWNET != 0 {
		t.Fatalf("network-enabled final launcher unexpectedly requested NEWNET: %+v", connected)
	}
}

func TestLinuxSeccompFilterValidatesArchitectureAndNetworkEscapeSyscalls(t *testing.T) {
	filters := linuxSeccompFilters(false)
	deny := uint32(unix.SECCOMP_RET_ERRNO) | uint32(unix.EPERM)
	if got := evaluateLinuxSeccomp(filters, linuxSeccompAuditArch^1, uint32(unix.SYS_GETPID), 0); got != unix.SECCOMP_RET_KILL_PROCESS {
		t.Fatalf("wrong-architecture decision = %#x", got)
	}
	if linuxSeccompX32Bit != 0 {
		if got := evaluateLinuxSeccomp(filters, linuxSeccompAuditArch, uint32(unix.SYS_GETPID)|linuxSeccompX32Bit, 0); got != deny {
			t.Fatalf("x32 syscall decision = %#x", got)
		}
	}
	for _, number := range []uint32{unix.SYS_IO_URING_SETUP, unix.SYS_IO_URING_ENTER, unix.SYS_IO_URING_REGISTER} {
		if got := evaluateLinuxSeccomp(filters, linuxSeccompAuditArch, number, 0); got != deny {
			t.Fatalf("io_uring syscall %d decision = %#x", number, got)
		}
		if got := evaluateLinuxSeccomp(linuxSeccompFilters(true), linuxSeccompAuditArch, number, 0); got != unix.SECCOMP_RET_ALLOW {
			t.Fatalf("network-enabled io_uring syscall %d decision = %#x", number, got)
		}
	}
	for _, number := range linuxSeccompProcessCreationSyscalls {
		if got := evaluateLinuxSeccomp(filters, linuxSeccompAuditArch, number, 0); got != deny {
			t.Fatalf("subprocess syscall %d decision = %#x", number, got)
		}
	}
	if got := evaluateLinuxSeccomp(filters, linuxSeccompAuditArch, uint32(unix.SYS_CLONE), uint32(unix.SIGCHLD)); got != deny {
		t.Fatalf("process clone decision = %#x", got)
	}
	if got := evaluateLinuxSeccomp(filters, linuxSeccompAuditArch, uint32(unix.SYS_CLONE), unix.CLONE_THREAD); got != unix.SECCOMP_RET_ALLOW {
		t.Fatalf("thread clone decision = %#x", got)
	}
	for _, number := range []uint32{unix.SYS_OPEN_TREE, unix.SYS_MOVE_MOUNT, unix.SYS_FSOPEN, unix.SYS_FSCONFIG, unix.SYS_FSMOUNT, unix.SYS_FSPICK, unix.SYS_MOUNT_SETATTR} {
		if got := evaluateLinuxSeccomp(filters, linuxSeccompAuditArch, number, 0); got != deny {
			t.Fatalf("mount-family syscall %d decision = %#x", number, got)
		}
	}
	if got := evaluateLinuxSeccomp(filters, linuxSeccompAuditArch, uint32(unix.SYS_SOCKET), unix.AF_INET); got != deny {
		t.Fatalf("AF_INET socket decision = %#x", got)
	}
	if got := evaluateLinuxSeccomp(filters, linuxSeccompAuditArch, uint32(unix.SYS_SOCKET), unix.AF_UNIX); got != unix.SECCOMP_RET_ALLOW {
		t.Fatalf("AF_UNIX socket decision = %#x", got)
	}
}

func TestLinuxRuntimeBudgetSeparatesGuestAndLauncherTasks(t *testing.T) {
	budget := Budget{CPUMillis: 500, Processes: 8}
	if got := linuxGuestGOMAXPROCS(budget); got != 1 {
		t.Fatalf("guest GOMAXPROCS = %d, want 1", got)
	}
	if got := linuxProcessTreeLimit(budget); got != 16 {
		t.Fatalf("launcher process tree limit = %d, want 16", got)
	}
	environment := setLinuxEnvironment([]string{"GOMAXPROCS=64"}, "GOMAXPROCS", strconv.Itoa(linuxGuestGOMAXPROCS(budget)))
	if len(environment) != 1 || environment[0] != "GOMAXPROCS=1" {
		t.Fatalf("canonical guest environment = %v", environment)
	}
}

func evaluateLinuxSeccomp(filters []unix.SockFilter, arch, number, argument0 uint32) uint32 {
	var accumulator uint32
	for pc := 0; pc < len(filters); {
		instruction := filters[pc]
		switch instruction.Code {
		case unix.BPF_LD | unix.BPF_W | unix.BPF_ABS:
			switch instruction.K {
			case 0:
				accumulator = number
			case 4:
				accumulator = arch
			case 16:
				accumulator = argument0
			default:
				return 0
			}
			pc++
		case unix.BPF_JMP | unix.BPF_JEQ | unix.BPF_K:
			if accumulator == instruction.K {
				pc += int(instruction.Jt) + 1
			} else {
				pc += int(instruction.Jf) + 1
			}
		case unix.BPF_JMP | unix.BPF_JSET | unix.BPF_K:
			if accumulator&instruction.K != 0 {
				pc += int(instruction.Jt) + 1
			} else {
				pc += int(instruction.Jf) + 1
			}
		case unix.BPF_RET | unix.BPF_K:
			return instruction.K
		default:
			return 0
		}
	}
	return 0
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
	temp, err := os.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer temp.Close()
	endpointIdentity, _ := linuxFileIdentity(endpoint)
	credentialIdentity, _ := linuxFileIdentity(credential)
	tempIdentity, _ := linuxFileIdentity(temp)
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
		ParentPID:              os.Getpid(),
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
		Temp:                   tempIdentity,
		TempFD:                 6,
		LauncherFD:             7,
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
		"parent":     {func(protocol *linuxLaunchProtocol) { protocol.ParentPID++ }, "launcher parent identity changed"},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			protocol := base
			test.mutate(&protocol)
			protocolFile, err := createLinuxProtocolFile(protocol)
			if err != nil {
				t.Fatal(err)
			}
			command := exec.Command(os.Args[0], linuxLauncherChildArg, "8")
			command.ExtraFiles = []*os.File{artifact, endpoint, credential, temp, launcher, protocolFile}
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
		probeNamespaces: func(*os.File, *os.File, bool, int) bool { return false },
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
	scratch, err := os.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer scratch.Close()
	sandboxUID := testLinuxSandboxUID(t)
	if err := validateLinuxNamespaces(launcher, scratch, false, sandboxUID); err != nil {
		t.Skipf("kernel blocks the complete user/PID/mount/network namespace profile: %v", err)
	}
	if err := validateLinuxNamespaces(launcher, scratch, true, sandboxUID); err != nil {
		t.Fatalf("network-enabled namespace profile incorrectly required NEWNET: %v", err)
	}
	source, _, digest := copyLinuxTestArtifact(t)
	artifact, artifactPath, err := createLinuxArtifactImage(source)
	if err != nil {
		t.Fatal(err)
	}
	defer artifact.Close()
	defer os.Remove(artifactPath)
	root, err := os.MkdirTemp("", ".nre-plugin-root-test-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(root)
	if err := os.Chown(root, sandboxUID, sandboxUID); err != nil {
		t.Fatal(err)
	}
	tempDirectory := t.TempDir()
	if err := os.Chown(tempDirectory, sandboxUID, sandboxUID); err != nil {
		t.Fatal(err)
	}
	temp, err := os.Open(tempDirectory)
	if err != nil {
		t.Fatal(err)
	}
	defer temp.Close()
	tempIdentity, err := linuxFileIdentity(temp)
	if err != nil {
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
		Version: linuxLauncherVersion, ParentPID: os.Getpid(), Generation: "namespace-live", ArtifactDigest: digest, LauncherDigest: launcherDigest,
		Artifact: artifactIdentity, Launcher: launcherIdentity, Temp: tempIdentity, ArtifactFD: 3, LauncherFD: 4, TempFD: 5,
		Arguments:   []string{"-test.run=^TestLinuxLauncherGuest$"},
		Environment: []string{"NRE_TEST_LINUX_LAUNCHER_GUEST=1", "NRE_TEST_NAMESPACE_ROOT=1"}, Budget: Budget{CPUMillis: 1000, MemoryBytes: 2 << 30, Processes: 16, Files: 64},
		Namespaces: true, SandboxRoot: root, SandboxUID: sandboxUID, ParentNamespaces: parents,
	}
	protocolFile, err := createLinuxProtocolFile(protocol)
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(os.Args[0], linuxLauncherChildArg, "6")
	command.ExtraFiles = []*os.File{artifact, launcher, temp, protocolFile}
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
	attributes := linuxFinalUserNamespaceSysProcAttr(os.Geteuid(), false)
	if len(attributes.UidMappings) != 1 || attributes.UidMappings[0].HostID != os.Geteuid() {
		t.Fatalf("final namespace uid mapping = %+v, want host euid %d", attributes.UidMappings, os.Geteuid())
	}
	builder := linuxSandboxSysProcAttrForUID(nil, false, true, os.Geteuid())
	if builder.Cloneflags&(unix.CLONE_NEWUSER|unix.CLONE_NEWNS) != unix.CLONE_NEWUSER|unix.CLONE_NEWNS || len(builder.UidMappings) != 1 || builder.UidMappings[0].HostID != os.Geteuid() {
		t.Fatalf("non-root builder namespace mapping = %+v", builder)
	}
	launcher, err := os.Open("/proc/self/exe")
	if err != nil {
		t.Fatal(err)
	}
	defer launcher.Close()
	scratch, err := os.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer scratch.Close()
	if err := validateLinuxNamespaces(launcher, scratch, false, os.Geteuid()); err != nil {
		if probeLinuxNamespaces(launcher, scratch, false, os.Geteuid()) {
			t.Fatalf("production probe admitted an incomplete current-euid namespace profile: %v", err)
		}
		return
	}
}

func TestLinuxNonRootUnavailableKernelBoundariesFailClosed(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("non-root admission fixture")
	}
	artifact, _, digest := copyLinuxTestArtifact(t)
	defer artifact.Close()
	command := exec.Command(artifact.Name())
	_, _, _, err := (linuxSandbox{
		prepareCgroup:   func(Budget) (string, *os.File, error) { return "", nil, unix.EROFS },
		probeNamespaces: func(*os.File, *os.File, bool, int) bool { return false },
	}).Configure(command, Security{
		Requirement:    testSandboxRequirement(Budget{CPUMillis: 500, MemoryBytes: 256 << 20, Processes: 8, Files: 64}, false, true),
		ArtifactDigest: digest,
		Generation:     "non-root-fail-closed",
	})
	if err == nil {
		t.Fatal("non-root launcher admitted without a complete namespace or delegated cgroup boundary")
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
	if linuxSeccompX32Bit != 0 {
		_, _, errno := unix.RawSyscall(uintptr(uint32(unix.SYS_GETPID)|linuxSeccompX32Bit), 0, 0, 0)
		if !errors.Is(errno, unix.EPERM) {
			t.Fatalf("x32 syscall result = %v, want EPERM", errno)
		}
	}
	if _, _, errno := unix.RawSyscall(uintptr(unix.SYS_IO_URING_SETUP), 0, 0, 0); !errors.Is(errno, unix.EPERM) {
		t.Fatalf("io_uring setup result = %v, want EPERM", errno)
	}
	if _, _, errno := unix.RawSyscall(uintptr(unix.SYS_OPEN_TREE), 0, 0, 0); !errors.Is(errno, unix.EPERM) {
		t.Fatalf("open_tree after sandbox setup = %v, want EPERM", errno)
	}
	if err := exec.Command("/proc/self/exe", "-test.run=^$").Run(); !errors.Is(err, unix.EPERM) {
		t.Fatalf("single-process RPC contract exec.Command result = %v, want EPERM", err)
	}
	threadReady := make(chan struct{})
	go func() {
		runtime.LockOSThread()
		close(threadReady)
	}()
	<-threadReady
	if fd, err := unix.Socket(unix.AF_INET, unix.SOCK_STREAM, 0); !errors.Is(err, unix.EPERM) {
		if err == nil {
			_ = unix.Close(fd)
		}
		t.Fatalf("AF_INET socket result = %v, want EPERM", err)
	}
	temporary, err := os.CreateTemp("", "plugin-guest-")
	if err != nil {
		t.Fatalf("create private temporary file: %v", err)
	}
	temporaryPath := temporary.Name()
	if _, err := temporary.Write([]byte("temporary")); err != nil {
		t.Fatal(err)
	}
	if err := temporary.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(temporaryPath); err != nil {
		t.Fatalf("unlink private temporary file: %v", err)
	}
	null, err := os.OpenFile("/dev/null", os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("open exact /dev/null device: %v", err)
	}
	if _, err := null.Write([]byte("discard")); err != nil {
		t.Fatal(err)
	}
	_ = null.Close()
	random := make([]byte, 1)
	if source, err := os.Open("/dev/urandom"); err != nil {
		t.Fatalf("open exact /dev/urandom device: %v", err)
	} else {
		_, err = source.Read(random)
		_ = source.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.ReadFile("/etc/passwd"); err == nil {
		t.Fatal("sandbox exposed unrelated host file contents")
	}
	if os.Getenv("NRE_TEST_NAMESPACE_ROOT") == "1" {
		if os.Getpid() != 1 {
			t.Fatalf("namespace plugin pid = %d, want 1", os.Getpid())
		}
		stat, err := os.ReadFile("/proc/self/stat")
		if err != nil {
			t.Fatal(err)
		}
		if fields := strings.Fields(string(stat)); len(fields) == 0 || fields[0] != "1" {
			t.Fatalf("namespace proc self stat = %q, want pid 1", stat)
		}
		entries, err := os.ReadDir("/proc")
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if pid, err := strconv.Atoi(entry.Name()); err == nil && pid != 1 {
				t.Fatalf("namespace proc exposed ancestor pid %d", pid)
			}
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

func TestLinuxProcessCPUDeltaIsMonotonicAcrossExitAndPIDReuse(t *testing.T) {
	old := linuxProcessIdentity{pid: 10, startTime: 100}
	reused := linuxProcessIdentity{pid: 11, startTime: 200}
	previous := map[linuxProcessIdentity]uint64{old: 900, reused: 400}
	current := map[linuxProcessIdentity]uint64{old: 910, {pid: 11, startTime: 300}: 7}
	if got := linuxProcessCPUDelta(previous, current); got != 17 {
		t.Fatalf("CPU delta across exit/PID reuse = %d, want 17", got)
	}
	if got := linuxCPUThrottleDuration(17, uint64(100*runtime.NumCPU()), 1000); got != 0 {
		t.Fatalf("PID churn produced a false throttle = %s", got)
	}
}

func TestLinuxFallbackRejectsShortCPUChildrenAndThrottlesGuest(t *testing.T) {
	started := time.Now()
	process, cleanup, output := startLinuxResourceGuest(t, "churn", Budget{CPUMillis: 500, MemoryBytes: 1 << 30, Processes: 32, Files: 128})
	if err := process.Wait(); err != nil {
		_ = cleanup()
		t.Fatalf("single-process CPU guest: %v; output=%s", err, output.String())
	}
	if elapsed := time.Since(started); elapsed < 1800*time.Millisecond {
		_ = cleanup()
		t.Fatalf("denied child churn ran 1.2 CPU seconds without observable 500 milli-CPU throttling in %s; output=%s", elapsed, output.String())
	}
	if err := cleanup(); err != nil {
		t.Fatal(err)
	}
}

func TestLinuxFallbackRejectsDescendantsAndCleanupIsIdempotent(t *testing.T) {
	process, cleanup, output := startLinuxResourceGuest(t, "tree", Budget{CPUMillis: 1000, MemoryBytes: 1 << 30, Processes: 32, Files: 64})
	if err := process.Wait(); err != nil {
		_ = cleanup()
		t.Fatalf("tree leader: %v; output=%s", err, output.String())
	}
	if !strings.Contains(output.String(), "child_denied") {
		_ = cleanup()
		t.Fatalf("fallback guest did not prove descendant rejection; output=%s", output.String())
	}
	if err := cleanup(); err != nil {
		t.Fatal(err)
	}
	if err := cleanup(); err != nil {
		t.Fatalf("repeated cleanup: %v", err)
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
		probeNamespaces: func(*os.File, *os.File, bool, int) bool { return false },
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
	case "churn":
		var initial unix.Rusage
		if err := unix.Getrusage(unix.RUSAGE_SELF, &initial); err != nil {
			t.Fatal(err)
		}
		initialCPU := time.Duration(initial.Utime.Sec+initial.Stime.Sec)*time.Second + time.Duration(initial.Utime.Usec+initial.Stime.Usec)*time.Microsecond
		for index := 0; index < 8; index++ {
			child := exec.Command("/proc/self/exe", "-test.run=^$")
			child.Env = os.Environ()
			child.Stdin, child.Stdout, child.Stderr = os.Stdin, os.Stdout, os.Stderr
			if err := child.Run(); !errors.Is(err, unix.EPERM) {
				t.Fatalf("short CPU child %d result = %v, want EPERM", index, err)
			}
		}
		var usage unix.Rusage
		for {
			if err := unix.Getrusage(unix.RUSAGE_SELF, &usage); err != nil {
				t.Fatal(err)
			}
			cpu := time.Duration(usage.Utime.Sec+usage.Stime.Sec)*time.Second + time.Duration(usage.Utime.Usec+usage.Stime.Usec)*time.Microsecond
			if cpu-initialCPU >= 1200*time.Millisecond {
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
		if err := child.Start(); !errors.Is(err, unix.EPERM) {
			t.Fatalf("descendant start result = %v, want EPERM", err)
		}
		_, _ = fmt.Println("child_denied")
	case "sleeper":
		time.Sleep(time.Minute)
	default:
		t.Fatalf("unknown resource fixture %q", mode)
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
	pidsMax, err := os.ReadFile(filepath.Join(directory, "pids.max"))
	if err != nil {
		t.Fatal(err)
	}
	if string(pidsMax) != "10" {
		t.Fatalf("pids.max = %q, want guest budget 2 + launcher allowance 8", pidsMax)
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
