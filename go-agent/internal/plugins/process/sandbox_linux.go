//go:build linux

package process

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

const linuxCgroupRoot = "/sys/fs/cgroup"

type linuxSandbox struct {
	bwrap     string
	available bool
}

func newPlatformSandbox() Sandbox {
	bwrap, bwrapErr := exec.LookPath("bwrap")
	_, prlimitErr := exec.LookPath("prlimit")
	info, cgroupErr := os.Stat(filepath.Join(linuxCgroupRoot, "cgroup.controllers"))
	available := bwrapErr == nil && prlimitErr == nil && cgroupErr == nil && info.Mode().IsRegular() && unix.Access(linuxCgroupRoot, unix.W_OK) == nil
	return linuxSandbox{bwrap: bwrap, available: available}
}
func (s linuxSandbox) Available() bool { return s.available }
func (linuxSandbox) Provider() string  { return "linux-cgroupv2-bwrap-seccomp" }
func (linuxSandbox) Validate(security Security) error {
	budget := security.Requirement.Budget()
	if budget.CPUMillis <= 0 || budget.CPUMillis > 1000 {
		return errors.New("linux plugin sandbox requires cpu_millis within 1..1000")
	}
	if budget.MemoryBytes <= 0 {
		return errors.New("linux plugin sandbox requires memory_bytes")
	}
	if budget.Processes <= 0 {
		return errors.New("linux plugin sandbox requires a positive process limit")
	}
	if budget.Files <= 0 {
		return errors.New("linux plugin sandbox requires a positive file limit")
	}
	return nil
}
func (s linuxSandbox) Configure(cmd *exec.Cmd, security Security) (func() error, func() error, func(int) error, error) {
	if cmd == nil {
		return nil, nil, nil, errors.New("plugin process command is required")
	}
	if err := s.Validate(security); err != nil {
		return nil, nil, nil, err
	}
	budget := security.Requirement.Budget()
	filter, err := createSeccompFilter(budget.Network)
	if err != nil {
		return nil, nil, nil, err
	}
	cgroupDir, cgroupFile, err := prepareLinuxCgroup(budget)
	if err != nil {
		filter.Close()
		return nil, nil, nil, err
	}
	prlimit, err := exec.LookPath("prlimit")
	if err != nil {
		filter.Close()
		cgroupFile.Close()
		os.Remove(cgroupDir)
		return nil, nil, nil, err
	}
	originalArgs := append([]string(nil), cmd.Args[1:]...)
	fd := 3 + len(cmd.ExtraFiles)
	args := linuxSandboxArguments(s.bwrap, prlimit, cmd.Path, originalArgs, cmd.Env, security, fd)
	cmd.Path = s.bwrap
	cmd.Args = args
	cmd.ExtraFiles = append(cmd.ExtraFiles, filter)
	cmd.SysProcAttr = linuxSandboxSysProcAttr(int(cgroupFile.Fd()))
	startCleanup := func() error { return errors.Join(filter.Close(), cgroupFile.Close()) }
	processCleanup := func() error { return os.Remove(cgroupDir) }
	return startCleanup, processCleanup, func(int) error { return nil }, nil
}

func linuxSandboxArguments(bwrap, prlimit, executable string, originalArgs, environment []string, security Security, filterFD int) []string {
	args := []string{bwrap, "--die-with-parent", "--new-session", "--unshare-user", "--unshare-pid", "--unshare-ipc", "--unshare-uts", "--unshare-cgroup", "--clearenv", "--dir", "/plugin", "--ro-bind", executable, "/plugin/plugin", "--dir", "/runtime", "--ro-bind", prlimit, "/runtime/prlimit", "--dev", "/dev", "--proc", "/proc", "--tmpfs", "/tmp"}
	if security.EndpointDirectory != "" {
		args = append(args, "--dir", "/run", "--bind", security.EndpointDirectory, "/run/nre-plugin")
	}
	if security.CredentialDirectory != "" {
		args = append(args, "--ro-bind", security.CredentialDirectory, "/run/nre-plugin-credentials")
	}
	for _, directory := range []string{"/lib", "/lib64", "/usr/lib", "/usr/lib64"} {
		if info, statErr := os.Stat(directory); statErr == nil && info.IsDir() {
			args = append(args, "--ro-bind", directory, directory)
		}
	}
	budget := security.Requirement.Budget()
	if !budget.Network {
		args = append(args, "--unshare-net")
	} else {
		args = append(args, "--dir", "/etc")
		for _, path := range []string{"/etc/resolv.conf", "/etc/hosts", "/etc/nsswitch.conf"} {
			if info, statErr := os.Stat(path); statErr == nil && info.Mode().IsRegular() {
				args = append(args, "--ro-bind", path, path)
			}
		}
		if info, statErr := os.Stat("/etc/ssl/certs"); statErr == nil && info.IsDir() {
			args = append(args, "--dir", "/etc/ssl", "--ro-bind", "/etc/ssl/certs", "/etc/ssl/certs")
		}
	}
	for _, entry := range environment {
		key, value, ok := strings.Cut(entry, "=")
		if ok {
			args = append(args, "--setenv", key, value)
		}
	}
	if security.GuestEndpoint != "" {
		args = append(args, "--setenv", "NRE_PLUGIN_ENDPOINT", "unix:"+security.GuestEndpoint)
	}
	if security.CredentialDirectory != "" {
		args = append(args,
			"--setenv", "NRE_PLUGIN_COOKIE_FILE", "/run/nre-plugin-credentials/cookie",
			"--setenv", "NRE_PLUGIN_TLS_CA_FILE", "/run/nre-plugin-credentials/ca.crt",
			"--setenv", "NRE_PLUGIN_TLS_CERT_FILE", "/run/nre-plugin-credentials/server.crt",
			"--setenv", "NRE_PLUGIN_TLS_KEY_FILE", "/run/nre-plugin-credentials/server.key",
		)
	}
	args = append(args, "--chdir", "/plugin")
	args = append(args, "--seccomp", strconv.Itoa(filterFD), "--")
	args = append(args, "/runtime/prlimit", "--nofile="+strconv.Itoa(budget.Files)+":"+strconv.Itoa(budget.Files), "--", "/plugin/plugin")
	args = append(args, originalArgs...)
	return args
}

func linuxSandboxSysProcAttr(cgroupFD int) *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL, Setpgid: true, UseCgroupFD: true, CgroupFD: cgroupFD}
}

func prepareLinuxCgroup(budget Budget) (string, *os.File, error) {
	root := filepath.Join(linuxCgroupRoot, "nre-plugins")
	return prepareLinuxCgroupAt(root, budget)
}

func prepareLinuxCgroupAt(root string, budget Budget) (string, *os.File, error) {
	if err := os.Mkdir(root, 0o755); err != nil && !errors.Is(err, os.ErrExist) {
		return "", nil, err
	}
	if err := enableLinuxCgroupControllers(root); err != nil {
		return "", nil, err
	}
	dir, err := os.MkdirTemp(root, "instance-")
	if err != nil {
		return "", nil, err
	}
	failed := true
	defer func() {
		if failed {
			_ = os.Remove(dir)
		}
	}()
	values := map[string]string{"memory.max": strconv.FormatInt(budget.MemoryBytes, 10), "memory.swap.max": "0", "pids.max": strconv.Itoa(budget.Processes), "cpu.max": fmt.Sprintf("%d 100000", budget.CPUMillis*100)}
	for name, value := range values {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(value), 0o600); err != nil {
			return "", nil, err
		}
	}
	fd, err := unix.Open(dir, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return "", nil, err
	}
	failed = false
	return dir, os.NewFile(uintptr(fd), dir), nil
}

func enableLinuxCgroupControllers(root string) error {
	controllers, err := os.ReadFile(filepath.Join(root, "cgroup.controllers"))
	if err != nil {
		return fmt.Errorf("read plugin cgroup controllers: %w", err)
	}
	available := make(map[string]struct{})
	for _, controller := range strings.Fields(string(controllers)) {
		available[controller] = struct{}{}
	}
	for _, required := range []string{"cpu", "memory", "pids"} {
		if _, ok := available[required]; !ok {
			return fmt.Errorf("plugin cgroup controller %s is unavailable", required)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "cgroup.subtree_control"), []byte("+cpu +memory +pids"), 0o600); err != nil {
		return fmt.Errorf("enable plugin cgroup controllers: %w", err)
	}
	return nil
}

func createSeccompFilter(_ bool) (*os.File, error) {
	denied := []uint32{unix.SYS_MOUNT, unix.SYS_UMOUNT2, unix.SYS_PTRACE, unix.SYS_BPF, unix.SYS_KEXEC_LOAD, unix.SYS_OPEN_BY_HANDLE_AT, unix.SYS_INIT_MODULE, unix.SYS_FINIT_MODULE, unix.SYS_DELETE_MODULE, unix.SYS_REBOOT, unix.SYS_SWAPON, unix.SYS_SWAPOFF}
	filters := []unix.SockFilter{{Code: unix.BPF_LD | unix.BPF_W | unix.BPF_ABS, K: 0}}
	for _, number := range denied {
		filters = append(filters, unix.SockFilter{Code: unix.BPF_JMP | unix.BPF_JEQ | unix.BPF_K, Jt: 0, Jf: 1, K: number}, unix.SockFilter{Code: unix.BPF_RET | unix.BPF_K, K: unix.SECCOMP_RET_ERRNO | uint32(unix.EPERM)})
	}
	filters = append(filters, unix.SockFilter{Code: unix.BPF_RET | unix.BPF_K, K: unix.SECCOMP_RET_ALLOW})
	file, err := os.CreateTemp("", "nre-seccomp-*")
	if err != nil {
		return nil, err
	}
	_ = os.Remove(file.Name())
	if err := binary.Write(file, binary.LittleEndian, filters); err != nil {
		file.Close()
		return nil, err
	}
	if _, err := file.Seek(0, 0); err != nil {
		file.Close()
		return nil, err
	}
	return file, nil
}
