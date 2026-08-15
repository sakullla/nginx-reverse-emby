//go:build linux

package process

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

const linuxLandlockSignalABI = 6

func probeLinuxNamespaces(launcher *os.File, network bool, hostUID int) bool {
	command := exec.Command("/proc/self/fd/3", linuxNamespaceProbeArg)
	command.ExtraFiles = []*os.File{launcher}
	command.Env = []string{"PATH=/usr/bin:/bin", "LANG=C"}
	command.Stdout = io.Discard
	command.Stderr = io.Discard
	command.SysProcAttr = linuxSandboxSysProcAttrForUID(nil, network, true, hostUID)
	return command.Run() == nil
}

func captureLinuxNamespaceIDs(network bool) (map[string]string, error) {
	result := make(map[string]string)
	for _, name := range requiredLinuxNamespaces(network) {
		identity, err := os.Readlink("/proc/self/ns/" + name)
		if err != nil {
			return nil, fmt.Errorf("read parent %s namespace: %w", name, err)
		}
		result[name] = identity
	}
	return result, nil
}

func requiredLinuxNamespaces(network bool) []string {
	names := []string{"user", "mnt", "pid", "ipc", "uts", "cgroup"}
	if !network {
		names = append(names, "net")
	}
	return names
}

func linuxChildEnvironmentForIsolation(environment []string, endpointFD, credentialFD int, guestEndpoint string, namespaces bool) []string {
	result := linuxChildEnvironment(environment, endpointFD, credentialFD, guestEndpoint)
	if !namespaces {
		return result
	}
	for index, entry := range result {
		key, _, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		switch key {
		case "NRE_PLUGIN_ENDPOINT":
			result[index] = key + "=unix:/run/nre-plugin/" + filepath.Base(guestEndpoint)
		case "NRE_PLUGIN_COOKIE_FILE":
			result[index] = key + "=/run/nre-plugin-credentials/cookie"
		case "NRE_PLUGIN_TLS_CA_FILE":
			result[index] = key + "=/run/nre-plugin-credentials/ca.crt"
		case "NRE_PLUGIN_TLS_CERT_FILE":
			result[index] = key + "=/run/nre-plugin-credentials/server.crt"
		case "NRE_PLUGIN_TLS_KEY_FILE":
			result[index] = key + "=/run/nre-plugin-credentials/server.key"
		}
	}
	return result
}

func applyLinuxMinimalRoot(protocol linuxLaunchProtocol) error {
	for _, name := range requiredLinuxNamespaces(protocol.Budget.Network) {
		current, err := os.Readlink("/proc/self/ns/" + name)
		if err != nil {
			return fmt.Errorf("read plugin %s namespace: %w", name, err)
		}
		if parent := protocol.ParentNamespaces[name]; parent == "" || current == parent {
			return fmt.Errorf("plugin %s namespace was not isolated", name)
		}
	}
	if protocol.SandboxRoot == "" {
		return errors.New("plugin mount root is missing")
	}
	if err := unix.Mount("", "/", "", unix.MS_REC|unix.MS_PRIVATE, ""); err != nil {
		return fmt.Errorf("make plugin mount namespace private: %w", err)
	}
	if err := unix.Mount("tmpfs", protocol.SandboxRoot, "tmpfs", unix.MS_NOSUID|unix.MS_NODEV, "mode=0755,size=16m"); err != nil {
		return fmt.Errorf("mount plugin minimal root: %w", err)
	}
	for _, directory := range []string{"plugin", "proc", "dev", "run/nre-plugin", "run/nre-plugin-credentials", "etc", "etc/ssl", "usr/share"} {
		if err := os.MkdirAll(filepath.Join(protocol.SandboxRoot, directory), 0o755); err != nil {
			return err
		}
	}
	if protocol.ArtifactPath == "" {
		return errors.New("private plugin artifact path is missing")
	}
	pathIdentity, err := linuxPathIdentity(protocol.ArtifactPath)
	if err != nil || pathIdentity != protocol.Artifact {
		return errors.New("private plugin artifact path identity mismatch")
	}
	artifactTarget := filepath.Join(protocol.SandboxRoot, "plugin/plugin")
	if err := bindLinuxSandboxPath(protocol.ArtifactPath, artifactTarget, false, true); err != nil {
		return fmt.Errorf("bind plugin artifact: %w", err)
	}
	mountedIdentity, err := linuxPathIdentity(artifactTarget)
	if err != nil || mountedIdentity != protocol.Artifact {
		return errors.New("mounted plugin artifact identity mismatch")
	}
	if protocol.EndpointFD != 0 {
		if err := bindLinuxSandboxPath("/proc/self/fd/"+strconv.Itoa(protocol.EndpointFD), filepath.Join(protocol.SandboxRoot, "run/nre-plugin"), true, false); err != nil {
			return fmt.Errorf("bind plugin endpoint: %w", err)
		}
	}
	if protocol.CredentialFD != 0 {
		if err := bindLinuxSandboxPath("/proc/self/fd/"+strconv.Itoa(protocol.CredentialFD), filepath.Join(protocol.SandboxRoot, "run/nre-plugin-credentials"), true, true); err != nil {
			return fmt.Errorf("bind plugin credentials: %w", err)
		}
	}
	for _, binding := range linuxReadOnlySystemBindings(protocol.Budget.Network) {
		target := filepath.Join(protocol.SandboxRoot, strings.TrimPrefix(binding, "/"))
		if err := bindLinuxSandboxPath(binding, target, isLinuxDirectory(binding), true); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("bind plugin system path %s: %w", binding, err)
		}
	}
	for _, device := range []string{"/dev/null", "/dev/urandom"} {
		target := filepath.Join(protocol.SandboxRoot, strings.TrimPrefix(device, "/"))
		if err := bindLinuxSandboxPath(device, target, false, device != "/dev/null"); err != nil {
			return fmt.Errorf("bind plugin device %s: %w", device, err)
		}
	}
	if err := unix.Mount("proc", filepath.Join(protocol.SandboxRoot, "proc"), "proc", unix.MS_RDONLY|unix.MS_NOSUID|unix.MS_NODEV|unix.MS_NOEXEC, "hidepid=2"); err != nil {
		return fmt.Errorf("mount plugin proc: %w", err)
	}
	if err := unix.Mount("", protocol.SandboxRoot, "", unix.MS_REMOUNT|unix.MS_RDONLY|unix.MS_NOSUID|unix.MS_NODEV, ""); err != nil {
		return fmt.Errorf("seal plugin minimal root read-only: %w", err)
	}
	if err := unix.Chroot(protocol.SandboxRoot); err != nil {
		return fmt.Errorf("enter plugin minimal root: %w", err)
	}
	if err := os.Chdir("/plugin"); err != nil {
		return err
	}
	for _, fd := range []int{protocol.ArtifactFD, protocol.EndpointFD, protocol.CredentialFD, protocol.LauncherFD} {
		if fd != 0 {
			unix.CloseOnExec(fd)
		}
	}
	return nil
}

func bindLinuxSandboxPath(source, target string, directory, readOnly bool) error {
	if directory {
		if err := os.MkdirAll(target, 0o755); err != nil {
			return err
		}
	} else {
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		file, err := os.OpenFile(target, os.O_CREATE, 0o644)
		if err != nil {
			return err
		}
		_ = file.Close()
	}
	flags := uintptr(unix.MS_BIND)
	if directory {
		flags |= unix.MS_REC
	}
	if err := unix.Mount(source, target, "", flags, ""); err != nil {
		return fmt.Errorf("initial bind mount: %w", err)
	}
	remount := uintptr(unix.MS_BIND | unix.MS_REMOUNT | unix.MS_NOSUID | unix.MS_NODEV)
	if readOnly {
		remount |= unix.MS_RDONLY
	}
	if err := unix.Mount("", target, "", remount, ""); err != nil {
		return fmt.Errorf("bind remount: %w", err)
	}
	return nil
}

func linuxPathIdentity(path string) (linuxFDIdentity, error) {
	file, err := os.Open(path)
	if err != nil {
		return linuxFDIdentity{}, err
	}
	defer file.Close()
	return linuxFileIdentity(file)
}

func isLinuxDirectory(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func linuxReadOnlySystemBindings(network bool) []string {
	paths := []string{"/usr/share/zoneinfo"}
	if network {
		paths = append(paths, "/etc/resolv.conf", "/etc/hosts", "/etc/nsswitch.conf", "/etc/ssl/certs")
	}
	return paths
}

func requireLinuxLandlock(sandboxUID int) error {
	abi, err := linuxLandlockABI()
	if err != nil || abi < 1 {
		return errors.New("plugin filesystem isolation requires Landlock when namespaces are unavailable")
	}
	if sandboxUID == 0 && abi < linuxLandlockSignalABI {
		return errors.New("plugin signal isolation requires Landlock ABI 6 or an isolated uid")
	}
	return nil
}

func linuxLandlockABI() (int, error) {
	version, _, errno := unix.Syscall(unix.SYS_LANDLOCK_CREATE_RULESET, 0, 0, unix.LANDLOCK_CREATE_RULESET_VERSION)
	if errno != 0 {
		return 0, errno
	}
	return int(version), nil
}

func applyLinuxLandlock(protocol linuxLaunchProtocol) error {
	abi, err := linuxLandlockABI()
	if err != nil {
		return fmt.Errorf("query Landlock ABI: %w", err)
	}
	handled := uint64(unix.LANDLOCK_ACCESS_FS_EXECUTE | unix.LANDLOCK_ACCESS_FS_WRITE_FILE | unix.LANDLOCK_ACCESS_FS_READ_FILE | unix.LANDLOCK_ACCESS_FS_READ_DIR | unix.LANDLOCK_ACCESS_FS_REMOVE_DIR | unix.LANDLOCK_ACCESS_FS_REMOVE_FILE | unix.LANDLOCK_ACCESS_FS_MAKE_CHAR | unix.LANDLOCK_ACCESS_FS_MAKE_DIR | unix.LANDLOCK_ACCESS_FS_MAKE_REG | unix.LANDLOCK_ACCESS_FS_MAKE_SOCK | unix.LANDLOCK_ACCESS_FS_MAKE_FIFO | unix.LANDLOCK_ACCESS_FS_MAKE_BLOCK | unix.LANDLOCK_ACCESS_FS_MAKE_SYM)
	if abi >= 2 {
		handled |= unix.LANDLOCK_ACCESS_FS_REFER
	}
	if abi >= 3 {
		handled |= unix.LANDLOCK_ACCESS_FS_TRUNCATE
	}
	attr := unix.LandlockRulesetAttr{Access_fs: handled}
	if protocol.SandboxUID == 0 && abi >= linuxLandlockSignalABI {
		attr.Scoped = unix.LANDLOCK_SCOPE_SIGNAL
	}
	ruleset, _, errno := unix.Syscall(unix.SYS_LANDLOCK_CREATE_RULESET, uintptr(unsafe.Pointer(&attr)), unsafe.Sizeof(attr), 0)
	if errno != 0 {
		return fmt.Errorf("create plugin Landlock ruleset: %w", errno)
	}
	defer unix.Close(int(ruleset))
	readFile := uint64(unix.LANDLOCK_ACCESS_FS_READ_FILE)
	readDirectory := readFile | unix.LANDLOCK_ACCESS_FS_READ_DIR
	if err := addLinuxLandlockRule(int(ruleset), protocol.ArtifactFD, readFile|unix.LANDLOCK_ACCESS_FS_EXECUTE); err != nil {
		return err
	}
	if protocol.EndpointFD != 0 {
		allowed := uint64(unix.LANDLOCK_ACCESS_FS_READ_DIR | unix.LANDLOCK_ACCESS_FS_REMOVE_FILE | unix.LANDLOCK_ACCESS_FS_MAKE_SOCK)
		if err := addLinuxLandlockRule(int(ruleset), protocol.EndpointFD, allowed); err != nil {
			return err
		}
	}
	if protocol.CredentialFD != 0 {
		if err := addLinuxLandlockRule(int(ruleset), protocol.CredentialFD, readDirectory); err != nil {
			return err
		}
	}
	for _, path := range append(linuxReadOnlySystemBindings(protocol.Budget.Network), "/proc/self/fd") {
		fd, openErr := unix.Open(path, unix.O_PATH|unix.O_CLOEXEC, 0)
		if openErr != nil {
			if errors.Is(openErr, unix.ENOENT) {
				continue
			}
			return openErr
		}
		allowed := readFile
		if isLinuxDirectory(path) {
			allowed = readDirectory
		}
		err = addLinuxLandlockRule(int(ruleset), fd, allowed)
		_ = unix.Close(fd)
		if err != nil {
			return err
		}
	}
	if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		return err
	}
	_, _, errno = unix.Syscall(unix.SYS_LANDLOCK_RESTRICT_SELF, ruleset, 0, 0)
	if errno != 0 {
		return fmt.Errorf("enforce plugin Landlock ruleset: %w", errno)
	}
	return nil
}

func addLinuxLandlockRule(rulesetFD, parentFD int, allowed uint64) error {
	attr := unix.LandlockPathBeneathAttr{Allowed_access: allowed, Parent_fd: int32(parentFD)}
	_, _, errno := unix.Syscall6(unix.SYS_LANDLOCK_ADD_RULE, uintptr(rulesetFD), unix.LANDLOCK_RULE_PATH_BENEATH, uintptr(unsafe.Pointer(&attr)), 0, 0, 0)
	if errno != 0 {
		return fmt.Errorf("add plugin Landlock path rule: %w", errno)
	}
	return nil
}

func dropLinuxSandboxIdentity(uid int) error {
	if uid == 0 {
		return nil
	}
	if err := unix.Setgroups(nil); err != nil {
		return fmt.Errorf("clear plugin supplementary groups: %w", err)
	}
	if err := unix.Setresgid(uid, uid, uid); err != nil {
		return fmt.Errorf("drop plugin group identity: %w", err)
	}
	if err := unix.Setresuid(uid, uid, uid); err != nil {
		return fmt.Errorf("drop plugin user identity: %w", err)
	}
	if os.Geteuid() != uid {
		return errors.New("plugin isolated uid did not take effect")
	}
	return nil
}

func linuxSandboxSysProcAttrForUID(cgroup *os.File, network, namespaces bool, hostUID int) *syscall.SysProcAttr {
	attributes := &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL, Setpgid: true}
	if namespaces {
		attributes.Cloneflags = uintptr(unix.CLONE_NEWUSER | unix.CLONE_NEWPID | unix.CLONE_NEWIPC | unix.CLONE_NEWUTS | unix.CLONE_NEWNS | unix.CLONE_NEWCGROUP)
		if !network {
			attributes.Cloneflags |= unix.CLONE_NEWNET
		}
		attributes.UidMappings = []syscall.SysProcIDMap{{ContainerID: 0, HostID: hostUID, Size: 1}}
		attributes.GidMappings = []syscall.SysProcIDMap{{ContainerID: 0, HostID: hostUID, Size: 1}}
		attributes.GidMappingsEnableSetgroups = false
	}
	if cgroup != nil {
		attributes.UseCgroupFD = true
		attributes.CgroupFD = int(cgroup.Fd())
	}
	return attributes
}
