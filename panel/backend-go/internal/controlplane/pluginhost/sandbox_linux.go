//go:build linux

package pluginhost

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

const (
	backendCgroupRoot        = "/sys/fs/cgroup"
	backendLauncherChildArg  = "--nre-control-plugin-launcher-child-v1"
	backendNamespaceProbeArg = "--nre-control-plugin-namespace-probe-v1"
	backendLauncherVersion   = 2
)

type backendSandboxOps struct {
	prepareCgroup   func(ProcessBudget) (string, *os.File, error)
	openLauncher    func() (*os.File, error)
	probeNamespaces func(*os.File, bool, int) bool
}

type backendFDIdentity struct {
	Device uint64 `json:"device"`
	Inode  uint64 `json:"inode"`
	Mode   uint32 `json:"mode"`
	Size   int64  `json:"size"`
}

var errBackendCgroupUnavailable = errors.New("control-plane plugin cgroup v2 controllers are unavailable")

type backendLaunchProtocol struct {
	Version                int               `json:"version"`
	Generation             string            `json:"generation"`
	ArtifactDigest         string            `json:"artifact_digest"`
	LauncherDigest         string            `json:"launcher_digest"`
	CookieDigest           string            `json:"cookie_digest,omitempty"`
	GenerationCookieDigest string            `json:"generation_cookie_digest,omitempty"`
	Artifact               backendFDIdentity `json:"artifact"`
	Launcher               backendFDIdentity `json:"launcher"`
	Endpoint               backendFDIdentity `json:"endpoint,omitempty"`
	Credential             backendFDIdentity `json:"credential,omitempty"`
	ArtifactFD             int               `json:"artifact_fd"`
	ArtifactPath           string            `json:"artifact_path,omitempty"`
	LauncherFD             int               `json:"launcher_fd"`
	EndpointFD             int               `json:"endpoint_fd,omitempty"`
	CredentialFD           int               `json:"credential_fd,omitempty"`
	Arguments              []string          `json:"arguments,omitempty"`
	Environment            []string          `json:"environment"`
	Budget                 ProcessBudget     `json:"budget"`
	EndpointRequired       bool              `json:"endpoint_required,omitempty"`
	Namespaces             bool              `json:"namespaces"`
	SandboxRoot            string            `json:"sandbox_root,omitempty"`
	SandboxUID             int               `json:"sandbox_uid,omitempty"`
	ParentNamespaces       map[string]string `json:"parent_namespaces,omitempty"`
	HardRlimits            bool              `json:"hard_rlimits,omitempty"`
}

func init() {
	if len(os.Args) == 2 && os.Args[1] == backendNamespaceProbeArg {
		if err := unix.Mount("", "/", "", unix.MS_REC|unix.MS_PRIVATE, ""); err != nil {
			os.Exit(126)
		}
		os.Exit(0)
	}
	if len(os.Args) == 3 && os.Args[1] == backendLauncherChildArg {
		protocolFD, err := strconv.Atoi(os.Args[2])
		if err == nil {
			err = runBackendLauncherChild(protocolFD)
		}
		if err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "control-plane plugin launcher child rejected request: %v\n", err)
		}
		os.Exit(125)
	}
}

func validatePlatformSandbox(candidate Candidate) error {
	budget := candidate.Requirement.Budget()
	if budget.CPUMillis <= 0 || budget.CPUMillis > 1000 || budget.MemoryBytes <= 0 || budget.Processes <= 0 || budget.Files <= 0 {
		return errors.New("linux control-plane plugin isolation requires bounded CPU/memory/process/file budgets")
	}
	if !backendValidSHA256(candidate.Artifact.SHA256) {
		return errors.New("linux control-plane plugin isolation requires a canonical artifact digest")
	}
	if strings.TrimSpace(candidate.Identity.Generation) == "" {
		return errors.New("linux control-plane plugin isolation requires a generation binding")
	}
	return nil
}

func configurePlatformSandbox(cmd *exec.Cmd, candidate Candidate) (func() error, func() error, func(int) error, error) {
	return configurePlatformSandboxWithOps(cmd, candidate, backendSandboxOps{prepareCgroup: prepareBackendCgroup})
}

func configurePlatformSandboxWithCgroup(cmd *exec.Cmd, candidate Candidate, prepareCgroup func(ProcessBudget) (string, *os.File, error)) (func() error, func() error, func(int) error, error) {
	return configurePlatformSandboxWithOps(cmd, candidate, backendSandboxOps{prepareCgroup: prepareCgroup})
}

func configurePlatformSandboxWithOps(cmd *exec.Cmd, candidate Candidate, ops backendSandboxOps) (func() error, func() error, func(int) error, error) {
	if cmd == nil {
		return nil, nil, nil, errors.New("control-plane plugin command is required")
	}
	if err := validatePlatformSandbox(candidate); err != nil {
		return nil, nil, nil, err
	}
	if len(cmd.ExtraFiles) != 0 {
		return nil, nil, nil, errors.New("control-plane plugin inherited descriptors are reserved by the launcher")
	}
	sourceArtifact, err := os.Open(cmd.Path)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("open verified control-plane plugin artifact: %w", err)
	}
	sourceIdentity, err := backendFileIdentity(sourceArtifact)
	if err != nil || sourceIdentity.Mode&unix.S_IFMT != unix.S_IFREG || sourceIdentity.Mode&0o222 != 0 {
		_ = sourceArtifact.Close()
		return nil, nil, nil, errors.New("verified control-plane plugin artifact must be a read-only regular file")
	}
	artifactDigest, err := backendDigestOpenFile(sourceArtifact)
	if err != nil || artifactDigest != candidate.Artifact.SHA256 {
		_ = sourceArtifact.Close()
		return nil, nil, nil, errors.New("verified control-plane plugin artifact digest changed before launch")
	}
	artifact, artifactPath, err := createBackendArtifactImage(sourceArtifact)
	_ = sourceArtifact.Close()
	if err != nil {
		return nil, nil, nil, err
	}
	files := []*os.File{artifact}
	closeFiles := func() error {
		var result error
		for _, file := range files {
			result = errors.Join(result, file.Close())
		}
		return result
	}
	fail := func(err error) (func() error, func() error, func(int) error, error) {
		return nil, nil, nil, errors.Join(err, closeFiles(), os.Remove(artifactPath))
	}

	artifactIdentity, err := backendFileIdentity(artifact)
	if err != nil || artifactIdentity.Mode&unix.S_IFMT != unix.S_IFREG || artifactIdentity.Mode&0o222 != 0 {
		return fail(errors.New("verified control-plane plugin artifact must be a read-only regular file"))
	}
	cookieHash := sha256.Sum256([]byte(candidate.Endpoint.Cookie))
	protocol := backendLaunchProtocol{
		Version:        backendLauncherVersion,
		Generation:     candidate.Identity.Generation,
		ArtifactDigest: candidate.Artifact.SHA256,
		CookieDigest:   hex.EncodeToString(cookieHash[:]),
		Artifact:       artifactIdentity,
		ArtifactFD:     3,
		ArtifactPath:   artifactPath,
		Arguments:      append([]string(nil), cmd.Args[1:]...),
		Environment:    append([]string(nil), cmd.Env...),
		Budget:         candidate.Requirement.Budget(),
		SandboxUID:     candidate.sandboxUID,
	}
	nextFD := 4
	if candidate.endpointDirectory != "" {
		endpoint, openErr := os.Open(candidate.endpointDirectory)
		if openErr != nil {
			return fail(fmt.Errorf("open control-plane plugin endpoint directory: %w", openErr))
		}
		files = append(files, endpoint)
		protocol.EndpointFD = nextFD
		nextFD++
		protocol.EndpointRequired = true
		protocol.Endpoint, err = backendFileIdentity(endpoint)
		if err != nil || protocol.Endpoint.Mode&unix.S_IFMT != unix.S_IFDIR {
			return fail(errors.New("control-plane plugin endpoint descriptor is not a directory"))
		}
	}
	if candidate.credentialDirectory != "" {
		credential, openErr := os.Open(candidate.credentialDirectory)
		if openErr != nil {
			return fail(fmt.Errorf("open control-plane plugin credential directory: %w", openErr))
		}
		files = append(files, credential)
		protocol.CredentialFD = nextFD
		nextFD++
		protocol.Credential, err = backendFileIdentity(credential)
		if err != nil || protocol.Credential.Mode&unix.S_IFMT != unix.S_IFDIR {
			return fail(errors.New("control-plane plugin credential descriptor is not a directory"))
		}
		cookieDigest, digestErr := backendDigestAt(int(credential.Fd()), "cookie")
		if digestErr != nil || cookieDigest != protocol.CookieDigest {
			return fail(errors.New("control-plane plugin cookie digest changed before launch"))
		}
		protocol.GenerationCookieDigest, err = backendDigestGenerationCookieAt(int(credential.Fd()), protocol.Generation)
		if err != nil {
			return fail(err)
		}
	}
	openLauncher := ops.openLauncher
	if openLauncher == nil {
		openLauncher = openBackendLauncher
	}
	launcher, err := openLauncher()
	if err != nil {
		return fail(fmt.Errorf("open control-plane launcher image: %w", err))
	}
	files = append(files, launcher)
	protocol.LauncherFD = nextFD
	nextFD++
	protocol.Launcher, err = backendFileIdentity(launcher)
	if err != nil || protocol.Launcher.Mode&unix.S_IFMT != unix.S_IFREG {
		return fail(errors.New("control-plane launcher image must be a regular file"))
	}
	protocol.LauncherDigest, err = backendDigestOpenFile(launcher)
	if err != nil {
		return fail(fmt.Errorf("digest control-plane launcher image: %w", err))
	}
	prepareCgroup := ops.prepareCgroup
	if prepareCgroup == nil {
		prepareCgroup = prepareBackendCgroup
	}
	dir, cgroup, cgroupErr := prepareCgroup(protocol.Budget)
	if cgroupErr != nil && !backendCgroupUnavailable(cgroupErr) {
		return fail(fmt.Errorf("prepare control-plane plugin cgroup: %w", cgroupErr))
	}
	probeNamespaces := ops.probeNamespaces
	if probeNamespaces == nil {
		probeNamespaces = probeBackendNamespaces
	}
	mappingUID := candidate.sandboxUID
	if mappingUID == 0 {
		mappingUID = os.Geteuid()
	}
	protocol.Namespaces = probeNamespaces(launcher, protocol.Budget.Network, mappingUID)
	if protocol.Namespaces {
		protocol.ParentNamespaces, err = captureBackendNamespaceIDs(protocol.Budget.Network)
		if err != nil {
			return fail(err)
		}
		protocol.SandboxRoot, err = os.MkdirTemp("", ".nre-control-plugin-root-")
		if err != nil {
			return fail(fmt.Errorf("create control-plane plugin mount root: %w", err))
		}
		if candidate.sandboxUID != 0 {
			if err := os.Chown(protocol.SandboxRoot, candidate.sandboxUID, candidate.sandboxUID); err != nil {
				_ = os.Remove(protocol.SandboxRoot)
				return fail(fmt.Errorf("own control-plane plugin mount root: %w", err))
			}
		}
	} else if err := requireBackendLandlock(candidate.sandboxUID); err != nil {
		return fail(err)
	}
	if cgroup == nil && candidate.sandboxUID == 0 {
		return fail(errors.New("control-plane plugin hard process limits require delegated cgroup v2 or an isolated uid"))
	}
	protocol.HardRlimits = cgroup == nil
	protocol.Environment = backendChildEnvironmentForIsolation(protocol.Environment, protocol.EndpointFD, protocol.CredentialFD, candidate.guestEndpoint, protocol.Namespaces)
	protocolFile, err := createBackendProtocolFile(protocol)
	if err != nil {
		return fail(err)
	}
	files = append(files, protocolFile)
	protocolFD := nextFD
	launcherPath := "/proc/self/fd/" + strconv.Itoa(protocol.LauncherFD)
	cmd.Path = launcherPath
	cmd.Args = []string{launcherPath, backendLauncherChildArg, strconv.Itoa(protocolFD)}
	cmd.ExtraFiles = files
	cmd.Dir = "/"
	cmd.Env = []string{"PATH=/usr/bin:/bin", "LANG=C", "HOME=/nonexistent", "TMPDIR=/tmp"}
	cmd.SysProcAttr = backendLinuxSandboxSysProcAttrForUID(cgroup, protocol.Budget.Network, protocol.Namespaces, mappingUID)
	governor := newBackendResourceGovernor(protocol.Budget)
	startCleanup := func() error {
		if cgroup != nil {
			return errors.Join(closeFiles(), cgroup.Close())
		}
		return closeFiles()
	}
	processCleanup := func() error {
		governorErr := governor.TerminateAndStop()
		artifactErr := os.Remove(artifactPath)
		if errors.Is(artifactErr, os.ErrNotExist) {
			artifactErr = nil
		}
		rootErr := error(nil)
		if protocol.SandboxRoot != "" {
			rootErr = os.Remove(protocol.SandboxRoot)
			if errors.Is(rootErr, os.ErrNotExist) {
				rootErr = nil
			}
		}
		if dir == "" {
			return errors.Join(governorErr, artifactErr, rootErr)
		}
		return errors.Join(governorErr, artifactErr, rootErr, removeBackendCgroup(dir))
	}
	afterStart := func(pid int) error {
		if err := governor.Start(pid); err != nil {
			return fmt.Errorf("start control-plane plugin resource governor: %w", err)
		}
		if dir == "" {
			return nil
		}
		body, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "cgroup"))
		if err != nil {
			return fmt.Errorf("verify control-plane plugin cgroup membership: %w", err)
		}
		if !strings.Contains(string(body), filepath.Base(dir)) {
			return errors.New("control-plane plugin child did not enter its assigned cgroup")
		}
		return nil
	}
	return startCleanup, processCleanup, afterStart, nil
}

func runBackendLauncherChild(protocolFD int) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if protocolFD < 4 {
		return errors.New("protocol descriptor is outside the inherited range")
	}
	protocolFile := os.NewFile(uintptr(protocolFD), "control-launch-protocol")
	if err := verifySealedBackendProtocol(protocolFD); err != nil {
		return err
	}
	decoder := json.NewDecoder(io.LimitReader(protocolFile, 1<<20))
	decoder.DisallowUnknownFields()
	var protocol backendLaunchProtocol
	if err := decoder.Decode(&protocol); err != nil {
		return fmt.Errorf("decode control launcher protocol: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("control launcher protocol has trailing data")
	}
	if protocol.Version != backendLauncherVersion || strings.TrimSpace(protocol.Generation) == "" || !backendValidSHA256(protocol.ArtifactDigest) || !backendValidSHA256(protocol.LauncherDigest) {
		return errors.New("control launcher protocol identity is invalid")
	}
	if protocol.ArtifactFD != 3 || protocol.LauncherFD < 4 || !distinctBackendFDs(protocol.ArtifactFD, protocol.EndpointFD, protocol.CredentialFD, protocol.LauncherFD, protocolFD) {
		return errors.New("control launcher descriptor binding is invalid")
	}
	launcher := os.NewFile(uintptr(protocol.LauncherFD), "control-host-launcher")
	if launcher == nil {
		return errors.New("control launcher image descriptor is invalid")
	}
	launcherIdentity, err := backendFileIdentity(launcher)
	if err != nil || launcherIdentity != protocol.Launcher || launcherIdentity.Mode&unix.S_IFMT != unix.S_IFREG {
		return errors.New("control launcher image descriptor identity mismatch")
	}
	launcherDigest, err := backendDigestOpenFile(launcher)
	if err != nil || launcherDigest != protocol.LauncherDigest {
		return errors.New("control launcher image descriptor digest mismatch")
	}
	artifact := os.NewFile(uintptr(protocol.ArtifactFD), "control-plugin-artifact")
	identity, err := backendFileIdentity(artifact)
	if err != nil || identity != protocol.Artifact || identity.Mode&unix.S_IFMT != unix.S_IFREG || identity.Mode&0o222 != 0 {
		return errors.New("control launcher artifact descriptor identity mismatch")
	}
	digest, err := backendDigestOpenFile(artifact)
	if err != nil || digest != protocol.ArtifactDigest {
		return errors.New("control launcher artifact descriptor digest mismatch")
	}
	if protocol.EndpointRequired {
		if err := verifyBackendDirectoryFD(protocol.EndpointFD, protocol.Endpoint); err != nil {
			return fmt.Errorf("control launcher endpoint descriptor: %w", err)
		}
	}
	if protocol.CredentialFD != 0 {
		if err := verifyBackendDirectoryFD(protocol.CredentialFD, protocol.Credential); err != nil {
			return fmt.Errorf("control launcher credential descriptor: %w", err)
		}
		cookieDigest, err := backendDigestAt(protocol.CredentialFD, "cookie")
		if err != nil || cookieDigest != protocol.CookieDigest {
			return errors.New("control launcher credential cookie digest mismatch")
		}
		generationDigest, err := backendDigestGenerationCookieAt(protocol.CredentialFD, protocol.Generation)
		if err != nil || generationDigest != protocol.GenerationCookieDigest {
			return errors.New("control launcher credential generation binding mismatch")
		}
	}
	if err := backendValidateBudget(protocol.Budget); err != nil {
		return err
	}
	if os.Getppid() == 1 {
		return errors.New("control launcher parent is no longer alive")
	}
	if err := unix.Prctl(unix.PR_SET_PDEATHSIG, uintptr(unix.SIGKILL), 0, 0, 0); err != nil {
		return fmt.Errorf("bind control launcher child lifetime: %w", err)
	}
	if err := applyBackendRlimits(protocol.Budget, protocol.HardRlimits); err != nil {
		return err
	}
	execPath := "/proc/self/fd/3"
	if protocol.Namespaces {
		if err := applyBackendMinimalRoot(protocol); err != nil {
			return err
		}
		execPath = "/plugin/plugin"
	} else {
		if err := applyBackendLandlock(protocol); err != nil {
			return err
		}
		if err := dropBackendSandboxIdentity(protocol.SandboxUID); err != nil {
			return err
		}
	}
	if err := installBackendSeccomp(protocol.Budget.Network); err != nil {
		return err
	}
	if protocol.EndpointFD != 0 && !protocol.Namespaces {
		if err := unix.Fchdir(protocol.EndpointFD); err != nil {
			return fmt.Errorf("enter control-plane plugin runtime directory: %w", err)
		}
	}
	argv := append([]string{execPath}, protocol.Arguments...)
	if err := protocolFile.Close(); err != nil {
		return fmt.Errorf("close control launcher protocol before plugin exec: %w", err)
	}
	return syscall.Exec(argv[0], argv, protocol.Environment)
}

func openBackendLauncher() (*os.File, error) {
	return os.Open("/proc/self/exe")
}

func distinctBackendFDs(values ...int) bool {
	seen := make(map[int]struct{}, len(values))
	for _, value := range values {
		if value == 0 {
			continue
		}
		if _, exists := seen[value]; exists {
			return false
		}
		seen[value] = struct{}{}
	}
	return true
}

func createBackendProtocolFile(protocol backendLaunchProtocol) (*os.File, error) {
	fd, err := unix.MemfdCreate("nre-control-plugin-launch", unix.MFD_CLOEXEC|unix.MFD_ALLOW_SEALING)
	if err != nil {
		return nil, fmt.Errorf("create immutable control launcher protocol: %w", err)
	}
	file := os.NewFile(uintptr(fd), "nre-control-plugin-launch")
	failed := true
	defer func() {
		if failed {
			_ = file.Close()
		}
	}()
	if err := json.NewEncoder(file).Encode(protocol); err != nil {
		return nil, err
	}
	if _, err := file.Seek(0, 0); err != nil {
		return nil, err
	}
	seals := unix.F_SEAL_WRITE | unix.F_SEAL_GROW | unix.F_SEAL_SHRINK | unix.F_SEAL_SEAL
	if _, err := unix.FcntlInt(file.Fd(), unix.F_ADD_SEALS, seals); err != nil {
		return nil, fmt.Errorf("seal immutable control launcher protocol: %w", err)
	}
	failed = false
	return file, nil
}

func createBackendArtifactImage(source *os.File) (*os.File, string, error) {
	file, err := os.CreateTemp("", ".nre-control-plugin-artifact-")
	if err != nil {
		return nil, "", fmt.Errorf("create private control-plane plugin artifact image: %w", err)
	}
	failed := true
	defer func() {
		if failed {
			_ = file.Close()
			_ = os.Remove(file.Name())
		}
	}()
	if _, err := source.Seek(0, 0); err != nil {
		return nil, "", err
	}
	if _, err := io.Copy(file, source); err != nil {
		return nil, "", err
	}
	if err := file.Chmod(0o555); err != nil {
		return nil, "", err
	}
	path := file.Name()
	if err := file.Close(); err != nil {
		return nil, "", err
	}
	artifact, err := os.Open(path)
	if err != nil {
		return nil, "", err
	}
	failed = false
	return artifact, path, nil
}

func verifySealedBackendProtocol(fd int) error {
	seals, err := unix.FcntlInt(uintptr(fd), unix.F_GET_SEALS, 0)
	if err != nil {
		return fmt.Errorf("inspect immutable control launcher protocol: %w", err)
	}
	required := unix.F_SEAL_WRITE | unix.F_SEAL_GROW | unix.F_SEAL_SHRINK | unix.F_SEAL_SEAL
	if seals&required != required {
		return errors.New("control launcher protocol is not immutable")
	}
	return nil
}

func backendChildEnvironment(environment []string, endpointFD, credentialFD int, guestEndpoint string) []string {
	values := make(map[string]string, len(environment))
	for _, entry := range environment {
		if key, value, ok := strings.Cut(entry, "="); ok {
			values[key] = value
		}
	}
	if endpointFD != 0 && guestEndpoint != "" {
		values["NRE_PLUGIN_ENDPOINT"] = "unix:/proc/self/fd/" + strconv.Itoa(endpointFD) + "/" + filepath.Base(guestEndpoint)
	}
	if credentialFD != 0 {
		root := "/proc/self/fd/" + strconv.Itoa(credentialFD)
		values["NRE_PLUGIN_COOKIE_FILE"] = root + "/cookie"
		values["NRE_PLUGIN_TLS_CA_FILE"] = root + "/ca.crt"
		values["NRE_PLUGIN_TLS_CERT_FILE"] = root + "/server.crt"
		values["NRE_PLUGIN_TLS_KEY_FILE"] = root + "/server.key"
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]string, 0, len(keys))
	for _, key := range keys {
		result = append(result, key+"="+values[key])
	}
	return result
}

func backendValidSHA256(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size && value == strings.ToLower(value)
}

func backendFileIdentity(file *os.File) (backendFDIdentity, error) {
	if file == nil {
		return backendFDIdentity{}, errors.New("descriptor is invalid")
	}
	var stat unix.Stat_t
	if err := unix.Fstat(int(file.Fd()), &stat); err != nil {
		return backendFDIdentity{}, err
	}
	return backendFDIdentity{Device: uint64(stat.Dev), Inode: stat.Ino, Mode: stat.Mode, Size: stat.Size}, nil
}

func verifyBackendDirectoryFD(fd int, expected backendFDIdentity) error {
	if fd < 3 {
		return errors.New("descriptor is outside the inherited range")
	}
	actual, err := backendFileIdentity(os.NewFile(uintptr(fd), "control-plugin-directory"))
	if err != nil || actual != expected || actual.Mode&unix.S_IFMT != unix.S_IFDIR {
		return errors.New("descriptor identity mismatch")
	}
	return nil
}

func backendDigestOpenFile(file *os.File) (string, error) {
	if _, err := file.Seek(0, 0); err != nil {
		return "", err
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	if _, err := file.Seek(0, 0); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func backendDigestAt(directoryFD int, name string) (string, error) {
	fd, err := unix.Openat(directoryFD, name, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return "", err
	}
	file := os.NewFile(uintptr(fd), name)
	defer file.Close()
	return backendDigestOpenFile(file)
}

func backendDigestGenerationCookieAt(directoryFD int, generation string) (string, error) {
	fd, err := unix.Openat(directoryFD, "cookie", unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return "", err
	}
	file := os.NewFile(uintptr(fd), "cookie")
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write([]byte(generation))
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func backendValidateBudget(budget ProcessBudget) error {
	if budget.CPUMillis <= 0 || budget.CPUMillis > 1000 || budget.MemoryBytes <= 0 || budget.Processes <= 0 || budget.Files <= 0 {
		return errors.New("control launcher resource profile is invalid")
	}
	return nil
}

func applyBackendRlimits(budget ProcessBudget, hardResources bool) error {
	limits := []struct {
		resource int
		value    uint64
	}{{unix.RLIMIT_NOFILE, uint64(budget.Files)}, {unix.RLIMIT_CORE, 0}}
	if hardResources {
		limits = append(limits,
			struct {
				resource int
				value    uint64
			}{unix.RLIMIT_DATA, uint64(budget.MemoryBytes)},
			struct {
				resource int
				value    uint64
			}{unix.RLIMIT_NPROC, uint64(budget.Processes)},
		)
	}
	for _, limit := range limits {
		if err := unix.Setrlimit(limit.resource, &unix.Rlimit{Cur: limit.value, Max: limit.value}); err != nil {
			return fmt.Errorf("apply control-plane plugin rlimit %d: %w", limit.resource, err)
		}
	}
	return nil
}

func installBackendSeccomp(network bool) error {
	denied := []uint32{unix.SYS_MOUNT, unix.SYS_UMOUNT2, unix.SYS_PTRACE, unix.SYS_BPF, unix.SYS_KEXEC_LOAD, unix.SYS_OPEN_BY_HANDLE_AT, unix.SYS_INIT_MODULE, unix.SYS_FINIT_MODULE, unix.SYS_DELETE_MODULE, unix.SYS_REBOOT, unix.SYS_SWAPON, unix.SYS_SWAPOFF, unix.SYS_SETSID, unix.SYS_SETPGID, unix.SYS_UNSHARE, unix.SYS_SETNS, unix.SYS_KILL, unix.SYS_PIDFD_SEND_SIGNAL, unix.SYS_PROCESS_VM_READV, unix.SYS_PROCESS_VM_WRITEV, unix.SYS_KCMP, unix.SYS_SETUID, unix.SYS_SETGID, unix.SYS_SETRESUID, unix.SYS_SETRESGID}
	filters := []unix.SockFilter{{Code: unix.BPF_LD | unix.BPF_W | unix.BPF_ABS, K: 0}}
	for _, number := range denied {
		filters = append(filters, unix.SockFilter{Code: unix.BPF_JMP | unix.BPF_JEQ | unix.BPF_K, Jf: 1, K: number}, unix.SockFilter{Code: unix.BPF_RET | unix.BPF_K, K: unix.SECCOMP_RET_ERRNO | uint32(unix.EPERM)})
	}
	if !network {
		for _, syscallNumber := range []uint32{unix.SYS_SOCKET, unix.SYS_SOCKETPAIR} {
			filters = append(filters,
				unix.SockFilter{Code: unix.BPF_JMP | unix.BPF_JEQ | unix.BPF_K, Jt: 0, Jf: 4, K: syscallNumber},
				unix.SockFilter{Code: unix.BPF_LD | unix.BPF_W | unix.BPF_ABS, K: 16},
				unix.SockFilter{Code: unix.BPF_JMP | unix.BPF_JEQ | unix.BPF_K, Jt: 1, Jf: 0, K: unix.AF_UNIX},
				unix.SockFilter{Code: unix.BPF_RET | unix.BPF_K, K: unix.SECCOMP_RET_ERRNO | uint32(unix.EPERM)},
				unix.SockFilter{Code: unix.BPF_RET | unix.BPF_K, K: unix.SECCOMP_RET_ALLOW},
				unix.SockFilter{Code: unix.BPF_LD | unix.BPF_W | unix.BPF_ABS, K: 0},
			)
		}
	}
	filters = append(filters, unix.SockFilter{Code: unix.BPF_RET | unix.BPF_K, K: unix.SECCOMP_RET_ALLOW})
	program := unix.SockFprog{Len: uint16(len(filters)), Filter: &filters[0]}
	if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		return fmt.Errorf("enable control-plane plugin no-new-privileges: %w", err)
	}
	if err := unix.Prctl(unix.PR_SET_SECCOMP, unix.SECCOMP_MODE_FILTER, uintptr(unsafe.Pointer(&program)), 0, 0); err != nil {
		return fmt.Errorf("install control-plane plugin seccomp filter: %w", err)
	}
	return nil
}

type backendResourceGovernor struct {
	budget ProcessBudget
	mu     sync.Mutex
	pid    int
	stop   chan struct{}
	done   chan struct{}
}

type backendResourceSample struct {
	processes int
	rssBytes  int64
	cpuTicks  uint64
	totalCPU  uint64
}

func newBackendResourceGovernor(budget ProcessBudget) *backendResourceGovernor {
	return &backendResourceGovernor{budget: budget, stop: make(chan struct{}), done: make(chan struct{})}
}

func (g *backendResourceGovernor) Start(pid int) error {
	initial, err := sampleBackendProcessGroup(pid)
	if err != nil {
		return fmt.Errorf("sample control-plane plugin process group: %w", err)
	}
	if initial.processes == 0 {
		return errors.New("control-plane plugin process group is not observable")
	}
	if initial.processes > g.budget.Processes || initial.rssBytes > g.budget.MemoryBytes {
		_ = unix.Kill(-pid, unix.SIGKILL)
		return errors.New("control-plane plugin process group exceeds its resource budget at startup")
	}
	g.mu.Lock()
	if g.pid != 0 {
		g.mu.Unlock()
		return errors.New("control-plane plugin resource governor is already active")
	}
	g.pid = pid
	g.mu.Unlock()
	go g.run(pid, initial)
	return nil
}

func (g *backendResourceGovernor) Stop() {
	g.mu.Lock()
	pid := g.pid
	if pid == 0 {
		g.mu.Unlock()
		return
	}
	select {
	case <-g.stop:
	default:
		close(g.stop)
	}
	g.mu.Unlock()
	_ = unix.Kill(-pid, unix.SIGCONT)
	<-g.done
}

func (g *backendResourceGovernor) TerminateAndStop() error {
	g.mu.Lock()
	pid := g.pid
	g.mu.Unlock()
	if pid == 0 {
		return nil
	}
	if err := unix.Kill(-pid, unix.SIGKILL); err != nil && !errors.Is(err, unix.ESRCH) {
		g.Stop()
		return fmt.Errorf("kill control-plane plugin process group: %w", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	var remaining int
	for {
		sample, err := sampleBackendProcessGroup(pid)
		if err == nil {
			remaining = sample.processes
			if remaining == 0 {
				break
			}
		}
		if time.Now().After(deadline) {
			g.Stop()
			return fmt.Errorf("control-plane plugin process group still has %d tasks after termination", remaining)
		}
		time.Sleep(10 * time.Millisecond)
	}
	g.Stop()
	return nil
}

func (g *backendResourceGovernor) run(pid int, previous backendResourceSample) {
	defer close(g.done)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-g.stop:
			return
		case <-ticker.C:
		}
		current, err := sampleBackendProcessGroup(pid)
		if err != nil || current.processes == 0 {
			_ = unix.Kill(-pid, unix.SIGKILL)
			return
		}
		if current.processes > g.budget.Processes || current.rssBytes > g.budget.MemoryBytes {
			_ = unix.Kill(-pid, unix.SIGKILL)
			return
		}
		cpuDelta := current.cpuTicks - previous.cpuTicks
		totalDelta := current.totalCPU - previous.totalCPU
		allowed := totalDelta * uint64(g.budget.CPUMillis) / uint64(1000*runtime.NumCPU())
		if totalDelta > 0 && cpuDelta > allowed && cpuDelta > 0 {
			stopFor := time.Duration((cpuDelta - allowed) * uint64(100*time.Millisecond) / cpuDelta)
			if stopFor > 90*time.Millisecond {
				stopFor = 90 * time.Millisecond
			}
			if stopFor > 0 && unix.Kill(-pid, unix.SIGSTOP) == nil {
				timer := time.NewTimer(stopFor)
				select {
				case <-g.stop:
					timer.Stop()
					_ = unix.Kill(-pid, unix.SIGCONT)
					return
				case <-timer.C:
					_ = unix.Kill(-pid, unix.SIGCONT)
				}
			}
		}
		previous = current
	}
}

func sampleBackendProcessGroup(group int) (backendResourceSample, error) {
	totalCPU, err := readBackendTotalCPU()
	if err != nil {
		return backendResourceSample{}, err
	}
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return backendResourceSample{}, err
	}
	sample := backendResourceSample{totalCPU: totalCPU}
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		body, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
		if err != nil {
			continue
		}
		end := strings.LastIndexByte(string(body), ')')
		if end < 0 {
			continue
		}
		fields := strings.Fields(string(body[end+1:]))
		if len(fields) <= 21 {
			continue
		}
		if fields[0] == "Z" {
			continue
		}
		processGroup, err := strconv.Atoi(fields[2])
		if err != nil || processGroup != group {
			continue
		}
		userTicks, userErr := strconv.ParseUint(fields[11], 10, 64)
		systemTicks, systemErr := strconv.ParseUint(fields[12], 10, 64)
		rssPages, rssErr := strconv.ParseInt(fields[21], 10, 64)
		if userErr != nil || systemErr != nil || rssErr != nil {
			continue
		}
		tasks, taskErr := os.ReadDir(filepath.Join("/proc", strconv.Itoa(pid), "task"))
		if taskErr != nil || len(tasks) == 0 {
			sample.processes++
		} else {
			sample.processes += len(tasks)
		}
		sample.cpuTicks += userTicks + systemTicks
		sample.rssBytes += rssPages * int64(os.Getpagesize())
	}
	return sample, nil
}

func readBackendTotalCPU() (uint64, error) {
	body, err := os.ReadFile("/proc/stat")
	if err != nil {
		return 0, err
	}
	line, _, _ := strings.Cut(string(body), "\n")
	fields := strings.Fields(line)
	if len(fields) < 2 || fields[0] != "cpu" {
		return 0, errors.New("Linux aggregate CPU statistics are unavailable")
	}
	var total uint64
	for _, field := range fields[1:] {
		value, err := strconv.ParseUint(field, 10, 64)
		if err != nil {
			return 0, err
		}
		total += value
	}
	return total, nil
}

func removeBackendCgroup(dir string) error {
	if err := os.WriteFile(filepath.Join(dir, "cgroup.kill"), []byte("1"), 0o600); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("kill control-plane plugin cgroup: %w", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		err := os.Remove(dir)
		if err == nil || errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if (!errors.Is(err, syscall.EBUSY) && !errors.Is(err, syscall.ENOTEMPTY)) || time.Now().After(deadline) {
			return fmt.Errorf("remove control-plane plugin cgroup: %w", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func backendLinuxSandboxSysProcAttr(cgroup *os.File, network, namespaces bool) *syscall.SysProcAttr {
	return backendLinuxSandboxSysProcAttrForUID(cgroup, network, namespaces, os.Geteuid())
}

func backendLinuxSandboxSysProcAttrForUID(cgroup *os.File, network, namespaces bool, hostUID int) *syscall.SysProcAttr {
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

func backendCgroupUnavailable(err error) bool {
	return errors.Is(err, errBackendCgroupUnavailable) || errors.Is(err, os.ErrNotExist) || errors.Is(err, os.ErrPermission) || errors.Is(err, syscall.EROFS) || errors.Is(err, syscall.EPERM) || errors.Is(err, syscall.EACCES) || errors.Is(err, syscall.ENODEV)
}

func prepareBackendCgroup(budget ProcessBudget) (string, *os.File, error) {
	root, err := currentBackendCgroupRoot("nre-control-plugins")
	if err != nil {
		return "", nil, err
	}
	return prepareBackendCgroupAt(root, budget)
}

func currentBackendCgroupRoot(child string) (string, error) {
	body, err := os.ReadFile("/proc/self/cgroup")
	if err != nil {
		return "", fmt.Errorf("read current control-plane cgroup: %w", err)
	}
	for _, line := range strings.Split(string(body), "\n") {
		if !strings.HasPrefix(line, "0::") {
			continue
		}
		relative := strings.TrimPrefix(filepath.Clean(strings.TrimPrefix(line, "0::")), string(filepath.Separator))
		if relative == "." {
			relative = ""
		}
		return filepath.Join(backendCgroupRoot, relative, child), nil
	}
	return "", errors.New("unified cgroup v2 membership is unavailable")
}

func prepareBackendCgroupAt(root string, budget ProcessBudget) (string, *os.File, error) {
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
			return fmt.Errorf("%w: %s", errBackendCgroupUnavailable, required)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "cgroup.subtree_control"), []byte("+cpu +memory +pids"), 0o600); err != nil {
		return fmt.Errorf("enable control-plane plugin cgroup controllers: %w", err)
	}
	return nil
}
