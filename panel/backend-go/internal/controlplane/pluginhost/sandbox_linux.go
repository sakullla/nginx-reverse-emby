//go:build linux

package pluginhost

import (
	"encoding/binary"
	"errors"
	"fmt"
	"golang.org/x/sys/unix"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

const backendCgroupRoot = "/sys/fs/cgroup"

func validatePlatformSandbox(c Candidate) error {
	if _, err := exec.LookPath("bwrap"); err != nil {
		return errors.New("linux control-plane plugin sandbox requires bwrap")
	}
	if _, err := exec.LookPath("prlimit"); err != nil {
		return errors.New("linux control-plane plugin sandbox requires prlimit")
	}
	if unix.Access(backendCgroupRoot, unix.W_OK) != nil {
		return errors.New("linux control-plane plugin sandbox requires writable cgroup v2")
	}
	if c.Budget.CPUMillis <= 0 || c.Budget.CPUMillis > 1000 || c.Budget.MemoryBytes <= 0 || c.Budget.Processes <= 0 || c.Budget.Files <= 0 {
		return errors.New("linux control-plane plugin sandbox requires bounded CPU/memory/process/file budgets")
	}
	return nil
}
func configurePlatformSandbox(cmd *exec.Cmd, c Candidate) (func() error, func(int) (func() error, error), error) {
	if hasUnsandboxedGrant(c.Grants) {
		return func() error { return nil }, func(int) (func() error, error) { return func() error { return nil }, nil }, nil
	}
	if err := validatePlatformSandbox(c); err != nil {
		return nil, nil, err
	}
	bwrap, _ := exec.LookPath("bwrap")
	prlimit, _ := exec.LookPath("prlimit")
	filter, err := backendSeccomp(c.Budget.Network)
	if err != nil {
		return nil, nil, err
	}
	dir, cgroup, err := prepareBackendCgroup(c.Budget)
	if err != nil {
		filter.Close()
		return nil, nil, err
	}
	original := append([]string(nil), cmd.Args[1:]...)
	fd := 3 + len(cmd.ExtraFiles)
	args := backendLinuxSandboxArguments(bwrap, prlimit, cmd.Path, original, cmd.Env, c, fd)
	cmd.Path = bwrap
	cmd.Args = args
	cmd.ExtraFiles = append(cmd.ExtraFiles, filter)
	cmd.SysProcAttr = backendLinuxSandboxSysProcAttr(int(cgroup.Fd()))
	startCleanup := func() error { return errors.Join(filter.Close(), cgroup.Close()) }
	attach := func(int) (func() error, error) { return func() error { return os.Remove(dir) }, nil }
	return startCleanup, attach, nil
}

func backendLinuxSandboxArguments(bwrap, prlimit, executable string, original, environment []string, c Candidate, filterFD int) []string {
	args := []string{bwrap, "--die-with-parent", "--new-session", "--unshare-user", "--unshare-pid", "--unshare-ipc", "--unshare-uts", "--unshare-cgroup", "--clearenv", "--dir", "/plugin", "--ro-bind", executable, "/plugin/plugin", "--dir", "/runtime", "--ro-bind", prlimit, "/runtime/prlimit", "--dev", "/dev", "--proc", "/proc", "--tmpfs", "/tmp"}
	if c.endpointDirectory != "" {
		args = append(args, "--dir", "/run", "--bind", c.endpointDirectory, controlGuestEndpointDirectory)
	}
	if c.credentialDirectory != "" {
		args = append(args, "--ro-bind", c.credentialDirectory, controlGuestCredentialDirectory)
	}
	for _, library := range []string{"/lib", "/lib64", "/usr/lib", "/usr/lib64"} {
		if info, e := os.Stat(library); e == nil && info.IsDir() {
			args = append(args, "--ro-bind", library, library)
		}
	}
	if !c.Budget.Network {
		args = append(args, "--unshare-net")
	} else {
		args = append(args, "--dir", "/etc")
		for _, path := range []string{"/etc/resolv.conf", "/etc/hosts", "/etc/nsswitch.conf"} {
			if info, e := os.Stat(path); e == nil && info.Mode().IsRegular() {
				args = append(args, "--ro-bind", path, path)
			}
		}
		if info, e := os.Stat("/etc/ssl/certs"); e == nil && info.IsDir() {
			args = append(args, "--dir", "/etc/ssl", "--ro-bind", "/etc/ssl/certs", "/etc/ssl/certs")
		}
	}
	for _, entry := range environment {
		if key, value, ok := strings.Cut(entry, "="); ok {
			args = append(args, "--setenv", key, value)
		}
	}
	if c.guestEndpoint != "" {
		args = append(args, "--setenv", "NRE_PLUGIN_ENDPOINT", "unix:"+c.guestEndpoint)
	}
	if c.credentialDirectory != "" {
		args = append(args,
			"--setenv", "NRE_PLUGIN_COOKIE_FILE", controlGuestCredentialDirectory+"/cookie",
			"--setenv", "NRE_PLUGIN_TLS_CA_FILE", controlGuestCredentialDirectory+"/ca.crt",
			"--setenv", "NRE_PLUGIN_TLS_CERT_FILE", controlGuestCredentialDirectory+"/server.crt",
			"--setenv", "NRE_PLUGIN_TLS_KEY_FILE", controlGuestCredentialDirectory+"/server.key",
		)
	}
	args = append(args, "--chdir", "/plugin")
	args = append(args, "--seccomp", strconv.Itoa(filterFD), "--", "/runtime/prlimit", "--nofile="+strconv.Itoa(c.Budget.Files)+":"+strconv.Itoa(c.Budget.Files), "--", "/plugin/plugin")
	args = append(args, original...)
	return args
}

func backendLinuxSandboxSysProcAttr(cgroupFD int) *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL, Setpgid: true, UseCgroupFD: true, CgroupFD: cgroupFD}
}
func prepareBackendCgroup(b ProcessBudget) (string, *os.File, error) {
	root := filepath.Join(backendCgroupRoot, "nre-control-plugins")
	return prepareBackendCgroupAt(root, b)
}

func prepareBackendCgroupAt(root string, b ProcessBudget) (string, *os.File, error) {
	if err := os.Mkdir(root, 0o755); err != nil && !errors.Is(err, os.ErrExist) {
		return "", nil, err
	}
	if err := enableBackendCgroupControllers(root); err != nil {
		return "", nil, err
	}
	dir, err := os.MkdirTemp(root, "instance-")
	if err != nil {
		return "", nil, err
	}
	values := map[string]string{"memory.max": strconv.FormatInt(b.MemoryBytes, 10), "memory.swap.max": "0", "pids.max": strconv.Itoa(b.Processes), "cpu.max": fmt.Sprintf("%d 100000", b.CPUMillis*100)}
	for name, value := range values {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(value), 0o600); err != nil {
			_ = os.Remove(dir)
			return "", nil, err
		}
	}
	fd, err := unix.Open(dir, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		_ = os.Remove(dir)
		return "", nil, err
	}
	return dir, os.NewFile(uintptr(fd), dir), nil
}

func enableBackendCgroupControllers(root string) error {
	controllers, err := os.ReadFile(filepath.Join(root, "cgroup.controllers"))
	if err != nil {
		return fmt.Errorf("read control-plane plugin cgroup controllers: %w", err)
	}
	available := make(map[string]struct{})
	for _, controller := range strings.Fields(string(controllers)) {
		available[controller] = struct{}{}
	}
	for _, required := range []string{"cpu", "memory", "pids"} {
		if _, ok := available[required]; !ok {
			return fmt.Errorf("control-plane plugin cgroup controller %s is unavailable", required)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "cgroup.subtree_control"), []byte("+cpu +memory +pids"), 0o600); err != nil {
		return fmt.Errorf("enable control-plane plugin cgroup controllers: %w", err)
	}
	return nil
}
func backendSeccomp(_ bool) (*os.File, error) {
	denied := []uint32{unix.SYS_MOUNT, unix.SYS_UMOUNT2, unix.SYS_PTRACE, unix.SYS_BPF, unix.SYS_KEXEC_LOAD, unix.SYS_OPEN_BY_HANDLE_AT, unix.SYS_INIT_MODULE, unix.SYS_FINIT_MODULE, unix.SYS_DELETE_MODULE, unix.SYS_REBOOT}
	filters := []unix.SockFilter{{Code: unix.BPF_LD | unix.BPF_W | unix.BPF_ABS, K: 0}}
	for _, n := range denied {
		filters = append(filters, unix.SockFilter{Code: unix.BPF_JMP | unix.BPF_JEQ | unix.BPF_K, Jf: 1, K: n}, unix.SockFilter{Code: unix.BPF_RET | unix.BPF_K, K: unix.SECCOMP_RET_ERRNO | uint32(unix.EPERM)})
	}
	filters = append(filters, unix.SockFilter{Code: unix.BPF_RET | unix.BPF_K, K: unix.SECCOMP_RET_ALLOW})
	f, err := os.CreateTemp("", "nre-backend-seccomp-*")
	if err != nil {
		return nil, err
	}
	_ = os.Remove(f.Name())
	if err := binary.Write(f, binary.LittleEndian, filters); err != nil {
		f.Close()
		return nil, err
	}
	_, err = f.Seek(0, 0)
	if err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}
