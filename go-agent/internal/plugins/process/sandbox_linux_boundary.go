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

func probeLinuxNamespaces(launcher, scratch *os.File, network bool, hostUID int) bool {
	return validateLinuxNamespaces(launcher, scratch, network, hostUID) == nil
}

func validateLinuxNamespaces(launcher, scratch *os.File, network bool, hostUID int) error {
	regular, err := os.CreateTemp(scratch.Name(), ".fd-mount-regular-")
	if err != nil {
		return fmt.Errorf("create regular descriptor probe: %w", err)
	}
	regularPath := regular.Name()
	defer func() {
		_ = regular.Close()
		_ = os.Remove(regularPath)
	}()
	if err := regular.Chmod(0o555); err != nil {
		return fmt.Errorf("seal regular descriptor probe: %w", err)
	}
	command := exec.Command("/proc/self/fd/3", linuxNamespaceProbeArg, strconv.Itoa(hostUID))
	command.ExtraFiles = []*os.File{launcher, scratch, regular}
	command.Env = []string{"PATH=/usr/bin:/bin", "LANG=C"}
	command.Stdout = io.Discard
	var stderr strings.Builder
	command.Stderr = &stderr
	command.SysProcAttr = linuxSandboxSysProcAttrForUID(nil, network, true, hostUID)
	if err := command.Run(); err != nil {
		return fmt.Errorf("run namespace descriptor mount probe: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
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

func linuxChildEnvironmentForIsolation(environment []string, endpointFD, credentialFD, tempFD int, guestEndpoint string, namespaces bool) []string {
	result := linuxChildEnvironment(environment, endpointFD, credentialFD, guestEndpoint)
	tempPath := "/proc/self/fd/" + strconv.Itoa(tempFD)
	if namespaces {
		tempPath = "/tmp"
	}
	tempSet := false
	for index, entry := range result {
		key, _, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		switch key {
		case "TMPDIR":
			result[index] = "TMPDIR=" + tempPath
			tempSet = true
		case "NRE_PLUGIN_ENDPOINT":
			if namespaces {
				result[index] = key + "=unix:/run/nre-plugin/" + filepath.Base(guestEndpoint)
			}
		case "NRE_PLUGIN_COOKIE_FILE":
			if namespaces {
				result[index] = key + "=/run/nre-plugin-credentials/cookie"
			}
		case "NRE_PLUGIN_TLS_CA_FILE":
			if namespaces {
				result[index] = key + "=/run/nre-plugin-credentials/ca.crt"
			}
		case "NRE_PLUGIN_TLS_CERT_FILE":
			if namespaces {
				result[index] = key + "=/run/nre-plugin-credentials/server.crt"
			}
		case "NRE_PLUGIN_TLS_KEY_FILE":
			if namespaces {
				result[index] = key + "=/run/nre-plugin-credentials/server.key"
			}
		}
	}
	if !tempSet {
		result = append(result, "TMPDIR="+tempPath)
	}
	return result
}

func prepareLinuxMinimalRoot(protocol linuxLaunchProtocol) error {
	openMount := func(fd int, identity linuxFDIdentity, name string) (*os.File, error) {
		source := os.NewFile(uintptr(fd), name)
		mountFile, err := openLinuxDetachedMount(source, identity)
		if err != nil {
			return nil, fmt.Errorf("clone %s descriptor mount: %w", name, err)
		}
		return mountFile, nil
	}
	artifactMount, err := openMount(protocol.ArtifactFD, protocol.Artifact, "plugin artifact")
	if err != nil {
		return err
	}
	defer artifactMount.Close()
	var endpointMount, credentialMount *os.File
	if protocol.EndpointFD != 0 {
		endpointMount, err = openMount(protocol.EndpointFD, protocol.Endpoint, "plugin endpoint")
		if err != nil {
			return err
		}
		defer endpointMount.Close()
	}
	if protocol.CredentialFD != 0 {
		credentialMount, err = openMount(protocol.CredentialFD, protocol.Credential, "plugin credential")
		if err != nil {
			return err
		}
		defer credentialMount.Close()
	}
	tempMount, err := openMount(protocol.TempFD, protocol.Temp, "plugin temporary directory")
	if err != nil {
		return err
	}
	defer tempMount.Close()
	if err := unix.Unshare(unix.CLONE_NEWNS); err != nil {
		return fmt.Errorf("create plugin mount namespace: %w", err)
	}
	currentMountNamespace, err := os.Readlink("/proc/self/ns/mnt")
	if err != nil {
		return fmt.Errorf("read plugin mount namespace: %w", err)
	}
	if parent := protocol.ParentNamespaces["mnt"]; parent == "" || currentMountNamespace == parent {
		return errors.New("plugin mount namespace was not isolated")
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
	artifactTarget := filepath.Join(protocol.SandboxRoot, "plugin/plugin")
	if err := attachLinuxSandboxMount(int(artifactMount.Fd()), artifactTarget, false, true); err != nil {
		return fmt.Errorf("attach plugin artifact: %w", err)
	}
	mountedIdentity, err := linuxPathIdentity(artifactTarget)
	if err != nil || mountedIdentity != protocol.Artifact {
		return errors.New("mounted plugin artifact identity mismatch")
	}
	if protocol.EndpointFD != 0 {
		target := filepath.Join(protocol.SandboxRoot, "run/nre-plugin")
		if err := attachLinuxSandboxMount(int(endpointMount.Fd()), target, true, false); err != nil {
			return fmt.Errorf("attach plugin endpoint: %w", err)
		}
		mountedIdentity, err := linuxPathIdentity(target)
		if err != nil || mountedIdentity != protocol.Endpoint {
			return errors.New("mounted plugin endpoint identity mismatch")
		}
	}
	if protocol.CredentialFD != 0 {
		target := filepath.Join(protocol.SandboxRoot, "run/nre-plugin-credentials")
		if err := attachLinuxSandboxMount(int(credentialMount.Fd()), target, true, true); err != nil {
			return fmt.Errorf("attach plugin credentials: %w", err)
		}
		mountedIdentity, err := linuxPathIdentity(target)
		if err != nil || mountedIdentity != protocol.Credential {
			return errors.New("mounted plugin credential identity mismatch")
		}
	}
	tempTarget := filepath.Join(protocol.SandboxRoot, "tmp")
	if err := attachLinuxSandboxMount(int(tempMount.Fd()), tempTarget, true, false); err != nil {
		return fmt.Errorf("attach plugin private temporary directory: %w", err)
	}
	mountedTempIdentity, err := linuxPathIdentity(tempTarget)
	if err != nil || mountedTempIdentity != protocol.Temp {
		return errors.New("mounted plugin temporary directory identity mismatch")
	}
	for _, binding := range linuxReadOnlySystemBindings(protocol.Budget.Network) {
		target := filepath.Join(protocol.SandboxRoot, strings.TrimPrefix(binding, "/"))
		if err := bindLinuxSandboxPath(binding, target, isLinuxDirectory(binding), true); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("bind plugin system path %s: %w", binding, err)
		}
	}
	for _, device := range []string{"/dev/null", "/dev/urandom"} {
		target := filepath.Join(protocol.SandboxRoot, strings.TrimPrefix(device, "/"))
		if err := bindLinuxSandboxDevice(device, target, device != "/dev/null"); err != nil {
			return fmt.Errorf("bind plugin device %s: %w", device, err)
		}
	}
	if err := unix.Mount("proc", filepath.Join(protocol.SandboxRoot, "proc"), "proc", unix.MS_RDONLY|unix.MS_NOSUID|unix.MS_NODEV|unix.MS_NOEXEC, "hidepid=2"); err != nil {
		return fmt.Errorf("mount plugin proc: %w", err)
	}
	if err := unix.Mount("", protocol.SandboxRoot, "", unix.MS_REMOUNT|unix.MS_RDONLY|unix.MS_NOSUID|unix.MS_NODEV, ""); err != nil {
		return fmt.Errorf("seal plugin minimal root read-only: %w", err)
	}
	return nil
}

func enterLinuxMinimalRoot(protocol linuxLaunchProtocol) error {
	for _, name := range requiredLinuxNamespaces(protocol.Budget.Network) {
		current, err := os.Readlink("/proc/self/ns/" + name)
		if err != nil {
			return fmt.Errorf("read final plugin %s namespace: %w", name, err)
		}
		if parent := protocol.ParentNamespaces[name]; parent == "" || current == parent {
			return fmt.Errorf("final plugin %s namespace was not isolated", name)
		}
	}
	if protocol.SandboxRoot == "" {
		return errors.New("final plugin mount root is missing")
	}
	if mounted, err := linuxPathIdentity(filepath.Join(protocol.SandboxRoot, "plugin/plugin")); err != nil || mounted != protocol.Artifact {
		return errors.New("final mounted plugin artifact identity mismatch")
	}
	if protocol.EndpointFD != 0 {
		if mounted, err := linuxPathIdentity(filepath.Join(protocol.SandboxRoot, "run/nre-plugin")); err != nil || mounted != protocol.Endpoint {
			return errors.New("final mounted plugin endpoint identity mismatch")
		}
	}
	if protocol.CredentialFD != 0 {
		if mounted, err := linuxPathIdentity(filepath.Join(protocol.SandboxRoot, "run/nre-plugin-credentials")); err != nil || mounted != protocol.Credential {
			return errors.New("final mounted plugin credential identity mismatch")
		}
	}
	if mounted, err := linuxPathIdentity(filepath.Join(protocol.SandboxRoot, "tmp")); err != nil || mounted != protocol.Temp {
		return errors.New("final mounted plugin temporary directory identity mismatch")
	}
	if err := unix.Chroot(protocol.SandboxRoot); err != nil {
		return fmt.Errorf("enter plugin minimal root: %w", err)
	}
	if err := os.Chdir("/plugin"); err != nil {
		return err
	}
	for _, fd := range []int{protocol.ArtifactFD, protocol.EndpointFD, protocol.CredentialFD, protocol.TempFD, protocol.LauncherFD} {
		if fd != 0 {
			unix.CloseOnExec(fd)
		}
	}
	return nil
}

func probeLinuxFinalUserNamespace(launcherFD, hostUID int, network bool) error {
	launcher := os.NewFile(uintptr(launcherFD), "namespace-final-probe-launcher")
	command := exec.Command("/proc/self/fd/3", linuxNamespaceFinalArg, strconv.Itoa(hostUID))
	command.ExtraFiles = []*os.File{launcher}
	command.Env = []string{"PATH=/usr/bin:/bin", "LANG=C"}
	command.Stdout = io.Discard
	var stderr strings.Builder
	command.Stderr = &stderr
	command.SysProcAttr = linuxFinalUserNamespaceSysProcAttr(hostUID, network)
	if err := command.Run(); err != nil {
		return fmt.Errorf("run final user namespace probe: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

func validateLinuxFinalUserNamespace(hostUID int) error {
	if os.Geteuid() != 0 || os.Getegid() != 0 {
		return errors.New("final namespace identity is not uid/gid 0")
	}
	body, err := os.ReadFile("/proc/self/uid_map")
	if err != nil {
		return err
	}
	want := fmt.Sprintf("0 %d 1", hostUID)
	if strings.Join(strings.Fields(string(body)), " ") != want {
		return fmt.Errorf("final namespace uid mapping %q does not equal %q", strings.TrimSpace(string(body)), want)
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

func attachLinuxSandboxMount(mountFD int, target string, directory, readOnly bool) error {
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
		if err := file.Close(); err != nil {
			return err
		}
	}
	if err := unix.MoveMount(mountFD, "", unix.AT_FDCWD, target, unix.MOVE_MOUNT_F_EMPTY_PATH); err != nil {
		return fmt.Errorf("attach descriptor mount: %w", err)
	}
	if readOnly {
		flags := uintptr(unix.MS_BIND | unix.MS_REMOUNT | unix.MS_RDONLY | unix.MS_NOSUID | unix.MS_NODEV)
		if err := unix.Mount("", target, "", flags, ""); err != nil {
			return fmt.Errorf("seal descriptor mount read-only: %w", err)
		}
	}
	return nil
}

func openLinuxDetachedMount(source *os.File, expected linuxFDIdentity) (*os.File, error) {
	fd, err := unix.OpenTree(int(source.Fd()), "", uint(unix.AT_EMPTY_PATH|unix.OPEN_TREE_CLONE|unix.OPEN_TREE_CLOEXEC))
	if errors.Is(err, unix.EINVAL) {
		fd, err = unix.OpenTree(unix.AT_FDCWD, "/proc/self/fd/"+strconv.Itoa(int(source.Fd())), uint(unix.OPEN_TREE_CLONE|unix.OPEN_TREE_CLOEXEC))
	}
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), "detached-plugin-mount")
	actual, err := linuxFileIdentity(file)
	if err != nil || actual != expected {
		_ = file.Close()
		return nil, errors.New("detached plugin mount identity mismatch")
	}
	return file, nil
}

func probeLinuxFDMounts(scratchFD, fileSourceFD, directorySourceFD int) error {
	if err := unix.Fchdir(scratchFD); err != nil {
		return err
	}
	fileSource := os.NewFile(uintptr(fileSourceFD), "namespace-probe-file")
	fileIdentity, err := linuxFileIdentity(fileSource)
	if err != nil {
		return err
	}
	fileMount, err := openLinuxDetachedMount(fileSource, fileIdentity)
	if err != nil {
		return fmt.Errorf("clone regular descriptor mount: %w", err)
	}
	defer fileMount.Close()
	directorySource := os.NewFile(uintptr(directorySourceFD), "namespace-probe-directory")
	directoryIdentity, err := linuxFileIdentity(directorySource)
	if err != nil {
		return err
	}
	directoryMount, err := openLinuxDetachedMount(directorySource, directoryIdentity)
	if err != nil {
		return fmt.Errorf("clone directory descriptor mount: %w", err)
	}
	defer directoryMount.Close()
	if err := unix.Unshare(unix.CLONE_NEWNS); err != nil {
		return fmt.Errorf("create descriptor probe mount namespace: %w", err)
	}
	if err := unix.Mount("", "/", "", unix.MS_REC|unix.MS_PRIVATE, ""); err != nil {
		return fmt.Errorf("make descriptor probe mounts private: %w", err)
	}
	root, err := os.MkdirTemp(".", ".fd-mount-probe-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(root)
	fileTarget := filepath.Join(root, "file")
	if err := attachLinuxSandboxMount(int(fileMount.Fd()), fileTarget, false, true); err != nil {
		return err
	}
	if err := unix.Unmount(fileTarget, unix.MNT_DETACH); err != nil {
		return err
	}
	directoryTarget := filepath.Join(root, "directory")
	if err := attachLinuxSandboxMount(int(directoryMount.Fd()), directoryTarget, true, false); err != nil {
		return err
	}
	return unix.Unmount(directoryTarget, unix.MNT_DETACH)
}

func bindLinuxSandboxDevice(source, target string, readOnly bool) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	file, err := os.OpenFile(target, os.O_CREATE, 0o644)
	if err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := unix.Mount(source, target, "", unix.MS_BIND, ""); err != nil {
		return fmt.Errorf("initial device bind mount: %w", err)
	}
	flags := uintptr(unix.MS_BIND | unix.MS_REMOUNT | unix.MS_NOSUID)
	if readOnly {
		flags |= unix.MS_RDONLY
	}
	if err := unix.Mount("", target, "", flags, ""); err != nil {
		return fmt.Errorf("remount device bind: %w", err)
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
	tempAllowed := handled &^ uint64(unix.LANDLOCK_ACCESS_FS_EXECUTE|unix.LANDLOCK_ACCESS_FS_MAKE_CHAR|unix.LANDLOCK_ACCESS_FS_MAKE_BLOCK)
	if err := addLinuxLandlockRule(int(ruleset), protocol.TempFD, tempAllowed); err != nil {
		return err
	}
	for _, device := range []struct {
		path    string
		allowed uint64
	}{
		{path: "/dev/null", allowed: readFile | unix.LANDLOCK_ACCESS_FS_WRITE_FILE},
		{path: "/dev/urandom", allowed: readFile},
	} {
		fd, openErr := unix.Open(device.path, unix.O_PATH|unix.O_CLOEXEC, 0)
		if openErr != nil {
			return openErr
		}
		err = addLinuxLandlockRule(int(ruleset), fd, device.allowed)
		_ = unix.Close(fd)
		if err != nil {
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
	if cgroup != nil {
		attributes.UseCgroupFD = true
		attributes.CgroupFD = int(cgroup.Fd())
	}
	return attributes
}

func linuxFinalUserNamespaceSysProcAttr(hostUID int, network bool) *syscall.SysProcAttr {
	attributes := &syscall.SysProcAttr{
		Pdeathsig:                  syscall.SIGKILL,
		Cloneflags:                 unix.CLONE_NEWUSER | unix.CLONE_NEWPID | unix.CLONE_NEWIPC | unix.CLONE_NEWUTS | unix.CLONE_NEWNS | unix.CLONE_NEWCGROUP,
		UidMappings:                []syscall.SysProcIDMap{{ContainerID: 0, HostID: hostUID, Size: 1}},
		GidMappings:                []syscall.SysProcIDMap{{ContainerID: 0, HostID: hostUID, Size: 1}},
		GidMappingsEnableSetgroups: false,
		Credential:                 &syscall.Credential{Uid: 0, Gid: 0, NoSetGroups: true},
	}
	if !network {
		attributes.Cloneflags |= unix.CLONE_NEWNET
	}
	return attributes
}
