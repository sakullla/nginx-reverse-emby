//go:build linux

package pluginhost

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"unsafe"

	"golang.org/x/sys/unix"
)

const backendLandlockSignalABI = 6

func probeBackendNamespaces(launcher, scratch *os.File, network bool, hostUID int) bool {
	return validateBackendNamespaces(launcher, scratch, network, hostUID) == nil
}

func validateBackendNamespaces(launcher, scratch *os.File, network bool, hostUID int) error {
	regular, err := os.CreateTemp(scratch.Name(), ".fd-mount-regular-")
	if err != nil {
		return fmt.Errorf("create control regular descriptor probe: %w", err)
	}
	regularPath := regular.Name()
	defer func() {
		_ = regular.Close()
		_ = os.Remove(regularPath)
	}()
	if err := regular.Chmod(0o555); err != nil {
		return fmt.Errorf("seal control regular descriptor probe: %w", err)
	}
	command := exec.Command("/proc/self/fd/3", backendNamespaceProbeArg, strconv.Itoa(hostUID))
	command.ExtraFiles = []*os.File{launcher, scratch, regular}
	command.Env = []string{"PATH=/usr/bin:/bin", "LANG=C"}
	command.Stdout = io.Discard
	var stderr strings.Builder
	command.Stderr = &stderr
	command.SysProcAttr = backendLinuxSandboxSysProcAttrForUID(nil, network, true, hostUID)
	if err := command.Run(); err != nil {
		return fmt.Errorf("run control namespace descriptor mount probe: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

func captureBackendNamespaceIDs(network bool) (map[string]string, error) {
	result := make(map[string]string)
	for _, name := range requiredBackendNamespaces(network) {
		identity, err := os.Readlink("/proc/self/ns/" + name)
		if err != nil {
			return nil, fmt.Errorf("read control-plane parent %s namespace: %w", name, err)
		}
		result[name] = identity
	}
	return result, nil
}

func requiredBackendNamespaces(network bool) []string {
	names := []string{"user", "mnt", "pid", "ipc", "uts", "cgroup"}
	if !network {
		names = append(names, "net")
	}
	return names
}

func backendChildEnvironmentForIsolation(environment []string, endpointFD, credentialFD, tempFD int, guestEndpoint string, namespaces bool) []string {
	result := backendChildEnvironment(environment, endpointFD, credentialFD, guestEndpoint)
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

func prepareBackendMinimalRoot(protocol backendLaunchProtocol) error {
	openMount := func(fd int, identity backendFDIdentity, name string) (*os.File, error) {
		source := os.NewFile(uintptr(fd), name)
		mountFile, err := openBackendDetachedMount(source, identity)
		if err != nil {
			return nil, fmt.Errorf("clone %s descriptor mount: %w", name, err)
		}
		return mountFile, nil
	}
	artifactMount, err := openMount(protocol.ArtifactFD, protocol.Artifact, "control plugin artifact")
	if err != nil {
		return err
	}
	defer artifactMount.Close()
	var endpointMount, credentialMount *os.File
	if protocol.EndpointFD != 0 {
		endpointMount, err = openMount(protocol.EndpointFD, protocol.Endpoint, "control plugin endpoint")
		if err != nil {
			return err
		}
		defer endpointMount.Close()
	}
	if protocol.CredentialFD != 0 {
		credentialMount, err = openMount(protocol.CredentialFD, protocol.Credential, "control plugin credential")
		if err != nil {
			return err
		}
		defer credentialMount.Close()
	}
	tempMount, err := openMount(protocol.TempFD, protocol.Temp, "control plugin temporary directory")
	if err != nil {
		return err
	}
	defer tempMount.Close()
	if err := unix.Unshare(unix.CLONE_NEWNS); err != nil {
		return fmt.Errorf("create control plugin mount namespace: %w", err)
	}
	currentMountNamespace, err := os.Readlink("/proc/self/ns/mnt")
	if err != nil {
		return fmt.Errorf("read control plugin mount namespace: %w", err)
	}
	if parent := protocol.ParentNamespaces["mnt"]; parent == "" || currentMountNamespace == parent {
		return errors.New("control plugin mount namespace was not isolated")
	}
	if protocol.SandboxRoot == "" {
		return errors.New("control-plane plugin mount root is missing")
	}
	if err := unix.Mount("", "/", "", unix.MS_REC|unix.MS_PRIVATE, ""); err != nil {
		return fmt.Errorf("make control-plane plugin mount namespace private: %w", err)
	}
	if err := unix.Mount("tmpfs", protocol.SandboxRoot, "tmpfs", unix.MS_NOSUID|unix.MS_NODEV, "mode=0755,size=16m"); err != nil {
		return fmt.Errorf("mount control-plane plugin minimal root: %w", err)
	}
	for _, directory := range []string{"plugin", "proc", "dev", "run/nre-plugin", "run/nre-plugin-credentials", "etc", "etc/ssl", "usr/share"} {
		if err := os.MkdirAll(filepath.Join(protocol.SandboxRoot, directory), 0o755); err != nil {
			return err
		}
	}
	artifactTarget := filepath.Join(protocol.SandboxRoot, "plugin/plugin")
	if err := attachBackendSandboxMount(int(artifactMount.Fd()), artifactTarget, false, true); err != nil {
		return fmt.Errorf("attach control-plane plugin artifact: %w", err)
	}
	mountedIdentity, err := backendPathIdentity(artifactTarget)
	if err != nil || mountedIdentity != protocol.Artifact {
		return errors.New("mounted control-plane plugin artifact identity mismatch")
	}
	if protocol.EndpointFD != 0 {
		target := filepath.Join(protocol.SandboxRoot, "run/nre-plugin")
		if err := attachBackendSandboxMount(int(endpointMount.Fd()), target, true, false); err != nil {
			return fmt.Errorf("attach control-plane plugin endpoint: %w", err)
		}
		mountedIdentity, err := backendPathIdentity(target)
		if err != nil || mountedIdentity != protocol.Endpoint {
			return errors.New("mounted control-plane plugin endpoint identity mismatch")
		}
	}
	if protocol.CredentialFD != 0 {
		target := filepath.Join(protocol.SandboxRoot, "run/nre-plugin-credentials")
		if err := attachBackendSandboxMount(int(credentialMount.Fd()), target, true, true); err != nil {
			return fmt.Errorf("attach control-plane plugin credentials: %w", err)
		}
		mountedIdentity, err := backendPathIdentity(target)
		if err != nil || mountedIdentity != protocol.Credential {
			return errors.New("mounted control-plane plugin credential identity mismatch")
		}
	}
	tempTarget := filepath.Join(protocol.SandboxRoot, "tmp")
	if err := attachBackendSandboxMount(int(tempMount.Fd()), tempTarget, true, false); err != nil {
		return fmt.Errorf("attach control-plane plugin private temporary directory: %w", err)
	}
	mountedTempIdentity, err := backendPathIdentity(tempTarget)
	if err != nil || mountedTempIdentity != protocol.Temp {
		return errors.New("mounted control-plane plugin temporary directory identity mismatch")
	}
	for _, binding := range backendReadOnlySystemBindings(protocol.Budget.Network) {
		target := filepath.Join(protocol.SandboxRoot, strings.TrimPrefix(binding, "/"))
		if err := bindBackendSandboxPath(binding, target, isBackendDirectory(binding), true); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("bind control-plane plugin system path %s: %w", binding, err)
		}
	}
	for _, device := range []string{"/dev/null", "/dev/urandom"} {
		target := filepath.Join(protocol.SandboxRoot, strings.TrimPrefix(device, "/"))
		if err := bindBackendSandboxDevice(device, target, device != "/dev/null"); err != nil {
			return fmt.Errorf("bind control-plane plugin device %s: %w", device, err)
		}
	}
	if err := unix.Mount("proc", filepath.Join(protocol.SandboxRoot, "proc"), "proc", unix.MS_RDONLY|unix.MS_NOSUID|unix.MS_NODEV|unix.MS_NOEXEC, "hidepid=2"); err != nil {
		return fmt.Errorf("mount control-plane plugin proc: %w", err)
	}
	if err := unix.Mount("", protocol.SandboxRoot, "", unix.MS_REMOUNT|unix.MS_RDONLY|unix.MS_NOSUID|unix.MS_NODEV, ""); err != nil {
		return fmt.Errorf("seal control-plane plugin minimal root read-only: %w", err)
	}
	return nil
}

func enterBackendMinimalRoot(protocol backendLaunchProtocol) error {
	for _, name := range requiredBackendNamespaces(protocol.Budget.Network) {
		current, err := os.Readlink("/proc/self/ns/" + name)
		if err != nil {
			return fmt.Errorf("read final control-plane plugin %s namespace: %w", name, err)
		}
		if parent := protocol.ParentNamespaces[name]; parent == "" || current == parent {
			return fmt.Errorf("final control-plane plugin %s namespace was not isolated", name)
		}
	}
	if protocol.SandboxRoot == "" {
		return errors.New("final control-plane plugin mount root is missing")
	}
	if mounted, err := backendPathIdentity(filepath.Join(protocol.SandboxRoot, "plugin/plugin")); err != nil || mounted != protocol.Artifact {
		return errors.New("final mounted control-plane plugin artifact identity mismatch")
	}
	if protocol.EndpointFD != 0 {
		if mounted, err := backendPathIdentity(filepath.Join(protocol.SandboxRoot, "run/nre-plugin")); err != nil || mounted != protocol.Endpoint {
			return errors.New("final mounted control-plane plugin endpoint identity mismatch")
		}
	}
	if protocol.CredentialFD != 0 {
		if mounted, err := backendPathIdentity(filepath.Join(protocol.SandboxRoot, "run/nre-plugin-credentials")); err != nil || mounted != protocol.Credential {
			return errors.New("final mounted control-plane plugin credential identity mismatch")
		}
	}
	if mounted, err := backendPathIdentity(filepath.Join(protocol.SandboxRoot, "tmp")); err != nil || mounted != protocol.Temp {
		return errors.New("final mounted control-plane plugin temporary directory identity mismatch")
	}
	if err := unix.Chroot(protocol.SandboxRoot); err != nil {
		return fmt.Errorf("enter control-plane plugin minimal root: %w", err)
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

func probeBackendFinalUserNamespace(launcherFD, hostUID int, network bool) error {
	launcher := os.NewFile(uintptr(launcherFD), "control-namespace-final-probe-launcher")
	command := exec.Command("/proc/self/fd/3", backendNamespaceFinalArg, strconv.Itoa(hostUID))
	command.ExtraFiles = []*os.File{launcher}
	command.Env = []string{"PATH=/usr/bin:/bin", "LANG=C"}
	command.Stdout = io.Discard
	var stderr strings.Builder
	command.Stderr = &stderr
	command.SysProcAttr = backendFinalUserNamespaceSysProcAttr(hostUID, network)
	if err := command.Run(); err != nil {
		return fmt.Errorf("run control final user namespace probe: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

func validateBackendFinalUserNamespace(hostUID int) error {
	if os.Geteuid() != 0 || os.Getegid() != 0 {
		return errors.New("final control namespace identity is not uid/gid 0")
	}
	body, err := os.ReadFile("/proc/self/uid_map")
	if err != nil {
		return err
	}
	want := fmt.Sprintf("0 %d 1", hostUID)
	if strings.Join(strings.Fields(string(body)), " ") != want {
		return fmt.Errorf("final control namespace uid mapping %q does not equal %q", strings.TrimSpace(string(body)), want)
	}
	return nil
}

func bindBackendSandboxPath(source, target string, directory, readOnly bool) error {
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

func attachBackendSandboxMount(mountFD int, target string, directory, readOnly bool) error {
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
		return fmt.Errorf("attach control descriptor mount: %w", err)
	}
	if readOnly {
		flags := uintptr(unix.MS_BIND | unix.MS_REMOUNT | unix.MS_RDONLY | unix.MS_NOSUID | unix.MS_NODEV)
		if err := unix.Mount("", target, "", flags, ""); err != nil {
			return fmt.Errorf("seal control descriptor mount read-only: %w", err)
		}
	}
	return nil
}

func openBackendDetachedMount(source *os.File, expected backendFDIdentity) (*os.File, error) {
	fd, err := unix.OpenTree(int(source.Fd()), "", uint(unix.AT_EMPTY_PATH|unix.OPEN_TREE_CLONE|unix.OPEN_TREE_CLOEXEC))
	if errors.Is(err, unix.EINVAL) {
		fd, err = unix.OpenTree(unix.AT_FDCWD, "/proc/self/fd/"+strconv.Itoa(int(source.Fd())), uint(unix.OPEN_TREE_CLONE|unix.OPEN_TREE_CLOEXEC))
	}
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), "detached-control-plugin-mount")
	actual, err := backendFileIdentity(file)
	if err != nil || actual != expected {
		_ = file.Close()
		return nil, errors.New("detached control plugin mount identity mismatch")
	}
	return file, nil
}

func probeBackendFDMounts(scratchFD, fileSourceFD, directorySourceFD int) error {
	if err := unix.Fchdir(scratchFD); err != nil {
		return err
	}
	root, err := os.MkdirTemp(".", ".fd-mount-probe-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(root)
	fileSource := os.NewFile(uintptr(fileSourceFD), "control-namespace-probe-file")
	fileIdentity, err := backendFileIdentity(fileSource)
	if err != nil {
		return err
	}
	fileMount, err := openBackendDetachedMount(fileSource, fileIdentity)
	if err != nil {
		return fmt.Errorf("clone control regular descriptor mount: %w", err)
	}
	defer fileMount.Close()
	directorySource := os.NewFile(uintptr(directorySourceFD), "control-namespace-probe-directory")
	directoryIdentity, err := backendFileIdentity(directorySource)
	if err != nil {
		return err
	}
	directoryMount, err := openBackendDetachedMount(directorySource, directoryIdentity)
	if err != nil {
		return fmt.Errorf("clone control directory descriptor mount: %w", err)
	}
	defer directoryMount.Close()
	if err := unix.Unshare(unix.CLONE_NEWNS); err != nil {
		return fmt.Errorf("create control descriptor probe mount namespace: %w", err)
	}
	if err := unix.Mount("", "/", "", unix.MS_REC|unix.MS_PRIVATE, ""); err != nil {
		return fmt.Errorf("make control descriptor probe mounts private: %w", err)
	}
	fileTarget := filepath.Join(root, "file")
	if err := attachBackendSandboxMount(int(fileMount.Fd()), fileTarget, false, true); err != nil {
		return err
	}
	if err := unix.Unmount(fileTarget, unix.MNT_DETACH); err != nil {
		return err
	}
	directoryTarget := filepath.Join(root, "directory")
	if err := attachBackendSandboxMount(int(directoryMount.Fd()), directoryTarget, true, false); err != nil {
		return err
	}
	return unix.Unmount(directoryTarget, unix.MNT_DETACH)
}

func bindBackendSandboxDevice(source, target string, readOnly bool) error {
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
		return fmt.Errorf("initial control device bind mount: %w", err)
	}
	flags := uintptr(unix.MS_BIND | unix.MS_REMOUNT | unix.MS_NOSUID)
	if readOnly {
		flags |= unix.MS_RDONLY
	}
	if err := unix.Mount("", target, "", flags, ""); err != nil {
		return fmt.Errorf("remount control device bind: %w", err)
	}
	return nil
}

func backendPathIdentity(path string) (backendFDIdentity, error) {
	file, err := os.Open(path)
	if err != nil {
		return backendFDIdentity{}, err
	}
	defer file.Close()
	return backendFileIdentity(file)
}

func isBackendDirectory(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func backendReadOnlySystemBindings(network bool) []string {
	paths := []string{"/usr/share/zoneinfo"}
	if network {
		paths = append(paths, "/etc/resolv.conf", "/etc/hosts", "/etc/nsswitch.conf", "/etc/ssl/certs")
	}
	return paths
}

func requireBackendLandlock(sandboxUID int) error {
	abi, err := backendLandlockABI()
	if err != nil || abi < 1 {
		return errors.New("control-plane plugin filesystem isolation requires Landlock when namespaces are unavailable")
	}
	if sandboxUID == 0 && abi < backendLandlockSignalABI {
		return errors.New("control-plane plugin signal isolation requires Landlock ABI 6 or an isolated uid")
	}
	return nil
}

func backendLandlockABI() (int, error) {
	version, _, errno := unix.Syscall(unix.SYS_LANDLOCK_CREATE_RULESET, 0, 0, unix.LANDLOCK_CREATE_RULESET_VERSION)
	if errno != 0 {
		return 0, errno
	}
	return int(version), nil
}

func applyBackendLandlock(protocol backendLaunchProtocol) error {
	abi, err := backendLandlockABI()
	if err != nil {
		return fmt.Errorf("query control-plane Landlock ABI: %w", err)
	}
	handled := uint64(unix.LANDLOCK_ACCESS_FS_EXECUTE | unix.LANDLOCK_ACCESS_FS_WRITE_FILE | unix.LANDLOCK_ACCESS_FS_READ_FILE | unix.LANDLOCK_ACCESS_FS_READ_DIR | unix.LANDLOCK_ACCESS_FS_REMOVE_DIR | unix.LANDLOCK_ACCESS_FS_REMOVE_FILE | unix.LANDLOCK_ACCESS_FS_MAKE_CHAR | unix.LANDLOCK_ACCESS_FS_MAKE_DIR | unix.LANDLOCK_ACCESS_FS_MAKE_REG | unix.LANDLOCK_ACCESS_FS_MAKE_SOCK | unix.LANDLOCK_ACCESS_FS_MAKE_FIFO | unix.LANDLOCK_ACCESS_FS_MAKE_BLOCK | unix.LANDLOCK_ACCESS_FS_MAKE_SYM)
	if abi >= 2 {
		handled |= unix.LANDLOCK_ACCESS_FS_REFER
	}
	if abi >= 3 {
		handled |= unix.LANDLOCK_ACCESS_FS_TRUNCATE
	}
	attr := unix.LandlockRulesetAttr{Access_fs: handled}
	if protocol.SandboxUID == 0 && abi >= backendLandlockSignalABI {
		attr.Scoped = unix.LANDLOCK_SCOPE_SIGNAL
	}
	ruleset, _, errno := unix.Syscall(unix.SYS_LANDLOCK_CREATE_RULESET, uintptr(unsafe.Pointer(&attr)), unsafe.Sizeof(attr), 0)
	if errno != 0 {
		return fmt.Errorf("create control-plane plugin Landlock ruleset: %w", errno)
	}
	defer unix.Close(int(ruleset))
	readFile := uint64(unix.LANDLOCK_ACCESS_FS_READ_FILE)
	readDirectory := readFile | unix.LANDLOCK_ACCESS_FS_READ_DIR
	if err := addBackendLandlockRule(int(ruleset), protocol.ArtifactFD, readFile|unix.LANDLOCK_ACCESS_FS_EXECUTE); err != nil {
		return err
	}
	if protocol.EndpointFD != 0 {
		allowed := uint64(unix.LANDLOCK_ACCESS_FS_READ_DIR | unix.LANDLOCK_ACCESS_FS_REMOVE_FILE | unix.LANDLOCK_ACCESS_FS_MAKE_SOCK)
		if err := addBackendLandlockRule(int(ruleset), protocol.EndpointFD, allowed); err != nil {
			return err
		}
	}
	if protocol.CredentialFD != 0 {
		if err := addBackendLandlockRule(int(ruleset), protocol.CredentialFD, readDirectory); err != nil {
			return err
		}
	}
	tempAllowed := handled &^ uint64(unix.LANDLOCK_ACCESS_FS_EXECUTE|unix.LANDLOCK_ACCESS_FS_MAKE_CHAR|unix.LANDLOCK_ACCESS_FS_MAKE_BLOCK)
	if err := addBackendLandlockRule(int(ruleset), protocol.TempFD, tempAllowed); err != nil {
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
		err = addBackendLandlockRule(int(ruleset), fd, device.allowed)
		_ = unix.Close(fd)
		if err != nil {
			return err
		}
	}
	for _, path := range append(backendReadOnlySystemBindings(protocol.Budget.Network), "/proc/self/fd") {
		fd, openErr := unix.Open(path, unix.O_PATH|unix.O_CLOEXEC, 0)
		if openErr != nil {
			if errors.Is(openErr, unix.ENOENT) {
				continue
			}
			return openErr
		}
		allowed := readFile
		if isBackendDirectory(path) {
			allowed = readDirectory
		}
		err = addBackendLandlockRule(int(ruleset), fd, allowed)
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
		return fmt.Errorf("enforce control-plane plugin Landlock ruleset: %w", errno)
	}
	return nil
}

func addBackendLandlockRule(rulesetFD, parentFD int, allowed uint64) error {
	attr := unix.LandlockPathBeneathAttr{Allowed_access: allowed, Parent_fd: int32(parentFD)}
	_, _, errno := unix.Syscall6(unix.SYS_LANDLOCK_ADD_RULE, uintptr(rulesetFD), unix.LANDLOCK_RULE_PATH_BENEATH, uintptr(unsafe.Pointer(&attr)), 0, 0, 0)
	if errno != 0 {
		return fmt.Errorf("add control-plane plugin Landlock path rule: %w", errno)
	}
	return nil
}

func dropBackendSandboxIdentity(uid int) error {
	if uid == 0 {
		return nil
	}
	if err := unix.Setgroups(nil); err != nil {
		return fmt.Errorf("clear control-plane plugin supplementary groups: %w", err)
	}
	if err := unix.Setresgid(uid, uid, uid); err != nil {
		return fmt.Errorf("drop control-plane plugin group identity: %w", err)
	}
	if err := unix.Setresuid(uid, uid, uid); err != nil {
		return fmt.Errorf("drop control-plane plugin user identity: %w", err)
	}
	if os.Geteuid() != uid {
		return errors.New("control-plane plugin isolated uid did not take effect")
	}
	return nil
}
