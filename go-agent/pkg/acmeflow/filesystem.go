package acmeflow

import (
	"errors"
	"io"
	iofs "io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

const (
	stateDirectoryMode = 0o700
	stateFileMode      = 0o600
	maxStateFileSize   = 8 << 20
	temporaryPrefix    = ".tmp-acmeflow-"
)

// PersistenceFaultPoint names a completed filesystem boundary. Tests and
// owner adapters may inject a process-like failure after any such boundary.
type PersistenceFaultPoint string

// PersistenceFaultInjector returns an error after a persistence boundary.
// Implementations must not include credentials or file contents in errors.
type PersistenceFaultInjector func(PersistenceFaultPoint) error

type durableFilesystem struct {
	root     *os.Root
	rootPath string
	inject   PersistenceFaultInjector
	tempID   atomic.Uint64

	directoryMu       sync.Mutex
	verifiedDirectory map[string]struct{}
}

func openDurableFilesystem(rootPath string, inject PersistenceFaultInjector) (*durableFilesystem, error) {
	rootPath = strings.TrimSpace(rootPath)
	if rootPath == "" {
		return nil, errors.New("state root is empty")
	}
	absRoot, err := filepath.Abs(rootPath)
	if err != nil {
		return nil, errors.New("state root is invalid")
	}
	if err := ensureStateRootDurable(absRoot, inject); err != nil {
		return nil, err
	}
	info, err := os.Lstat(absRoot)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return nil, errors.New("state root changed during initialization")
	}
	root, err := os.OpenRoot(absRoot)
	if err != nil {
		return nil, errors.New("state root could not be opened")
	}
	return &durableFilesystem{
		root:              root,
		rootPath:          absRoot,
		inject:            inject,
		verifiedDirectory: make(map[string]struct{}),
	}, nil
}

func (filesystem *durableFilesystem) close() error {
	if filesystem == nil || filesystem.root == nil {
		return nil
	}
	return filesystem.root.Close()
}

func (filesystem *durableFilesystem) checkpoint(operation, boundary string) error {
	if filesystem.inject == nil {
		return nil
	}
	return filesystem.inject(PersistenceFaultPoint(operation + "." + boundary))
}

func (filesystem *durableFilesystem) ensureDirectory(name string) error {
	if err := validateRelativePath(name, true); err != nil {
		return err
	}
	if name == "." {
		return nil
	}
	current := ""
	for _, component := range strings.Split(name, "/") {
		parent := "."
		if current == "" {
			current = component
		} else {
			parent = current
			current = path.Join(current, component)
		}
		info, err := filesystem.root.Lstat(current)
		created := false
		if os.IsNotExist(err) {
			if err := filesystem.root.Mkdir(current, stateDirectoryMode); err != nil {
				if !os.IsExist(err) {
					return errors.New("state directory could not be created")
				}
			} else {
				created = true
				if err := filesystem.checkpoint(directoryOperation(current), "created"); err != nil {
					return err
				}
			}
			info, err = filesystem.root.Lstat(current)
		}
		if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return errors.New("state directory is not safe")
		}
		modeNeedsRestriction := info.Mode().Perm() != stateDirectoryMode
		if !created && !modeNeedsRestriction && filesystem.isDirectoryVerified(current) {
			continue
		}
		if err := filesystem.root.Chmod(current, stateDirectoryMode); err != nil {
			return errors.New("state directory permissions could not be restricted")
		}
		if created || modeNeedsRestriction {
			if err := filesystem.checkpoint(directoryOperation(current), "permissions_restricted"); err != nil {
				return err
			}
		}
		if err := filesystem.syncDirectory(current); err != nil {
			return err
		}
		if err := filesystem.checkpoint(directoryOperation(current), "directory_synced"); err != nil {
			return err
		}
		if err := filesystem.syncDirectory(parent); err != nil {
			return err
		}
		if err := filesystem.checkpoint(directoryOperation(current), "parent_synced"); err != nil {
			return err
		}
		filesystem.markDirectoryVerified(current)
	}
	return nil
}

func (filesystem *durableFilesystem) makeDirectory(name, operation string) error {
	if err := validateRelativePath(name, false); err != nil {
		return err
	}
	parent := path.Dir(name)
	if err := filesystem.ensureDirectory(parent); err != nil {
		return err
	}
	if _, err := filesystem.root.Lstat(name); err == nil {
		return errors.New("state directory already exists")
	} else if !os.IsNotExist(err) {
		return errors.New("state directory could not be inspected")
	}
	if err := filesystem.root.Mkdir(name, stateDirectoryMode); err != nil {
		return errors.New("state directory could not be created")
	}
	if err := filesystem.checkpoint(operation, "created"); err != nil {
		return err
	}
	if err := filesystem.syncDirectory(parent); err != nil {
		return err
	}
	return filesystem.checkpoint(operation, "parent_synced")
}

func (filesystem *durableFilesystem) writeFileAtomic(name string, data []byte, operation string) error {
	if err := validateRelativePath(name, false); err != nil {
		return err
	}
	if len(data) > maxStateFileSize {
		return errors.New("state file is too large")
	}
	parent := path.Dir(name)
	if err := filesystem.ensureDirectory(parent); err != nil {
		return err
	}
	if info, err := filesystem.root.Lstat(name); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return errors.New("state file target is not regular")
		}
	} else if !os.IsNotExist(err) {
		return errors.New("state file target could not be inspected")
	}

	tempName := path.Join(parent, temporaryPrefix+strconv.Itoa(os.Getpid())+"-"+strconv.FormatUint(filesystem.tempID.Add(1), 10))
	tempFile, err := filesystem.root.OpenFile(tempName, os.O_WRONLY|os.O_CREATE|os.O_EXCL, stateFileMode)
	if err != nil {
		return errors.New("temporary state file could not be created")
	}
	closed := false
	defer func() {
		if !closed {
			_ = tempFile.Close()
		}
		_ = filesystem.root.Remove(tempName)
	}()
	if err := filesystem.root.Chmod(tempName, stateFileMode); err != nil {
		return errors.New("temporary state file permissions could not be restricted")
	}
	if err := filesystem.checkpoint(operation, "temp_created"); err != nil {
		return err
	}
	if err := writeAll(tempFile, data); err != nil {
		return errors.New("state file could not be written")
	}
	if err := filesystem.checkpoint(operation, "data_written"); err != nil {
		return err
	}
	if err := tempFile.Sync(); err != nil {
		return errors.New("state file could not be synchronized")
	}
	if err := filesystem.checkpoint(operation, "file_synced"); err != nil {
		return err
	}
	if err := tempFile.Close(); err != nil {
		closed = true
		return errors.New("state file could not be closed")
	}
	closed = true
	if err := filesystem.root.Rename(tempName, name); err != nil {
		return errors.New("state file could not be atomically replaced")
	}
	if err := filesystem.checkpoint(operation, "renamed"); err != nil {
		return err
	}
	if err := filesystem.syncDirectory(parent); err != nil {
		return err
	}
	return filesystem.checkpoint(operation, "parent_synced")
}

func (filesystem *durableFilesystem) renameDirectory(oldName, newName, operation string) error {
	if err := validateRelativePath(oldName, false); err != nil {
		return err
	}
	if err := validateRelativePath(newName, false); err != nil {
		return err
	}
	oldInfo, err := filesystem.inspectPath(oldName)
	if err != nil || oldInfo.Mode()&os.ModeSymlink != 0 || !oldInfo.IsDir() {
		return errors.New("staged state directory is not safe")
	}
	if _, err := filesystem.root.Lstat(newName); err == nil {
		return errors.New("immutable state directory already exists")
	} else if !os.IsNotExist(err) {
		return errors.New("immutable state directory could not be inspected")
	}
	oldParent := path.Dir(oldName)
	newParent := path.Dir(newName)
	if err := filesystem.ensureDirectory(newParent); err != nil {
		return err
	}
	if err := filesystem.root.Rename(oldName, newName); err != nil {
		return errors.New("state directory could not be atomically committed")
	}
	if err := filesystem.checkpoint(operation, "renamed"); err != nil {
		return err
	}
	if err := filesystem.syncDirectory(oldParent); err != nil {
		return err
	}
	if err := filesystem.checkpoint(operation, "source_parent_synced"); err != nil {
		return err
	}
	if err := filesystem.syncDirectory(newParent); err != nil {
		return err
	}
	return filesystem.checkpoint(operation, "destination_parent_synced")
}

func (filesystem *durableFilesystem) readRegularFile(name string, maximum int64) ([]byte, error) {
	if err := validateRelativePath(name, false); err != nil {
		return nil, err
	}
	info, err := filesystem.inspectPath(name)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, os.ErrNotExist
		}
		return nil, errors.New("state file could not be inspected")
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, errors.New("state file is not regular")
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return nil, errors.New("state file permissions are too broad")
	}
	if info.Size() < 0 || info.Size() > maximum {
		return nil, errors.New("state file has an invalid size")
	}
	file, err := filesystem.root.Open(name)
	if err != nil {
		return nil, errors.New("state file could not be opened")
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maximum+1))
	if err != nil || int64(len(data)) > maximum {
		return nil, errors.New("state file could not be read")
	}
	return data, nil
}

func (filesystem *durableFilesystem) readOptionalRegularFile(name string, maximum int64) ([]byte, bool, error) {
	data, err := filesystem.readRegularFile(name, maximum)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	return data, err == nil, err
}

func (filesystem *durableFilesystem) readDirectory(name string) ([]iofs.DirEntry, error) {
	if err := validateRelativePath(name, true); err != nil {
		return nil, err
	}
	info, err := filesystem.inspectPath(name)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return nil, errors.New("state directory is not safe")
	}
	entries, err := iofs.ReadDir(filesystem.root.FS(), name)
	if err != nil {
		return nil, errors.New("state directory could not be read")
	}
	return entries, nil
}

func (filesystem *durableFilesystem) removeAll(name, operation string) error {
	if err := validateRelativePath(name, false); err != nil {
		return err
	}
	parent := path.Dir(name)
	if _, err := filesystem.inspectPath(parent); err != nil {
		return err
	}
	if err := filesystem.root.RemoveAll(name); err != nil {
		return errors.New("state path could not be removed")
	}
	if err := filesystem.checkpoint(operation, "removed"); err != nil {
		return err
	}
	if err := filesystem.syncDirectory(parent); err != nil {
		return err
	}
	return filesystem.checkpoint(operation, "parent_synced")
}

func (filesystem *durableFilesystem) removeFile(name, operation string) error {
	if err := validateRelativePath(name, false); err != nil {
		return err
	}
	parent := path.Dir(name)
	if _, err := filesystem.inspectPath(parent); err != nil {
		return err
	}
	if err := filesystem.root.Remove(name); err != nil && !os.IsNotExist(err) {
		return errors.New("state file could not be removed")
	}
	if err := filesystem.checkpoint(operation, "removed"); err != nil {
		return err
	}
	if err := filesystem.syncDirectory(parent); err != nil {
		return err
	}
	return filesystem.checkpoint(operation, "parent_synced")
}

func (filesystem *durableFilesystem) syncDirectory(name string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	if err := validateRelativePath(name, true); err != nil {
		return err
	}
	info, err := filesystem.inspectPath(name)
	if err != nil || !info.IsDir() {
		return errors.New("state directory is not safe for synchronization")
	}
	directory, err := filesystem.root.Open(name)
	if err != nil {
		return errors.New("state directory could not be opened for synchronization")
	}
	defer directory.Close()
	if err := directory.Sync(); err != nil {
		return errors.New("state directory could not be synchronized")
	}
	return nil
}

func (filesystem *durableFilesystem) inspectPath(name string) (os.FileInfo, error) {
	if err := validateRelativePath(name, true); err != nil {
		return nil, err
	}
	if name == "." {
		info, err := filesystem.root.Lstat(name)
		if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return nil, errors.New("state root is not safe")
		}
		return info, nil
	}
	current := ""
	components := strings.Split(name, "/")
	for index, component := range components {
		if current == "" {
			current = component
		} else {
			current = path.Join(current, component)
		}
		info, err := filesystem.root.Lstat(current)
		if err != nil {
			if os.IsNotExist(err) {
				return nil, os.ErrNotExist
			}
			return nil, errors.New("state path could not be inspected")
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, errors.New("state path contains a symbolic link")
		}
		if info.IsDir() && runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
			return nil, errors.New("state directory permissions are too broad")
		}
		if index+1 < len(components) && !info.IsDir() {
			return nil, errors.New("state path contains a non-directory component")
		}
		if index+1 == len(components) {
			return info, nil
		}
	}
	return nil, errors.New("state path is invalid")
}

func (filesystem *durableFilesystem) isDirectoryVerified(name string) bool {
	filesystem.directoryMu.Lock()
	defer filesystem.directoryMu.Unlock()
	_, exists := filesystem.verifiedDirectory[name]
	return exists
}

func (filesystem *durableFilesystem) markDirectoryVerified(name string) {
	filesystem.directoryMu.Lock()
	filesystem.verifiedDirectory[name] = struct{}{}
	filesystem.directoryMu.Unlock()
}

func ensureStateRootDurable(rootPath string, inject PersistenceFaultInjector) error {
	missing := make([]string, 0, 2)
	current := rootPath
	for {
		info, err := os.Lstat(current)
		if err == nil {
			if current == rootPath && info.Mode()&os.ModeSymlink != 0 {
				return errors.New("state root is not a regular directory")
			}
			if info.Mode()&os.ModeSymlink != 0 {
				followed, statErr := os.Stat(current)
				if statErr != nil || !followed.IsDir() {
					return errors.New("state root parent is not a directory")
				}
			} else if !info.IsDir() {
				return errors.New("state root parent is not a directory")
			}
			break
		}
		if !os.IsNotExist(err) {
			return errors.New("state root could not be inspected")
		}
		missing = append(missing, current)
		parent := filepath.Dir(current)
		if parent == current {
			return errors.New("state root has no existing parent")
		}
		current = parent
	}

	for index := len(missing) - 1; index >= 0; index-- {
		directory := missing[index]
		operation := "state.root_parent"
		if directory == rootPath {
			operation = "state.root"
		}
		if err := os.Mkdir(directory, stateDirectoryMode); err != nil && !os.IsExist(err) {
			return errors.New("state root could not be created")
		}
		if err := persistenceCheckpoint(inject, operation, "created"); err != nil {
			return err
		}
		if err := os.Chmod(directory, stateDirectoryMode); err != nil {
			return errors.New("state root permissions could not be restricted")
		}
		if err := persistenceCheckpoint(inject, operation, "permissions_restricted"); err != nil {
			return err
		}
		if err := syncAbsoluteDirectory(directory); err != nil {
			return err
		}
		if err := persistenceCheckpoint(inject, operation, "directory_synced"); err != nil {
			return err
		}
		if err := syncAbsoluteDirectory(filepath.Dir(directory)); err != nil {
			return err
		}
		if err := persistenceCheckpoint(inject, operation, "parent_synced"); err != nil {
			return err
		}
	}

	if err := os.Chmod(rootPath, stateDirectoryMode); err != nil {
		return errors.New("state root permissions could not be restricted")
	}
	if len(missing) == 0 {
		if err := syncAbsoluteDirectory(rootPath); err != nil {
			return err
		}
		if err := persistenceCheckpoint(inject, "state.root", "directory_synced"); err != nil {
			return err
		}
		if err := syncAbsoluteDirectory(filepath.Dir(rootPath)); err != nil {
			return err
		}
		if err := persistenceCheckpoint(inject, "state.root", "parent_synced"); err != nil {
			return err
		}
	}
	return nil
}

func syncAbsoluteDirectory(name string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	directory, err := os.Open(name)
	if err != nil {
		return errors.New("state directory could not be opened for synchronization")
	}
	defer directory.Close()
	if err := directory.Sync(); err != nil {
		return errors.New("state directory could not be synchronized")
	}
	return nil
}

func persistenceCheckpoint(inject PersistenceFaultInjector, operation, boundary string) error {
	if inject == nil {
		return nil
	}
	return inject(PersistenceFaultPoint(operation + "." + boundary))
}

func directoryOperation(name string) string {
	return "directory." + strings.ReplaceAll(name, "/", "_")
}

func validateRelativePath(name string, allowRoot bool) error {
	if name == "." && allowRoot {
		return nil
	}
	if name == "" || name == "." || !iofs.ValidPath(name) || strings.Contains(name, "\\") {
		return errors.New("state path is invalid")
	}
	return nil
}

func writeAll(writer io.Writer, data []byte) error {
	for len(data) > 0 {
		written, err := writer.Write(data)
		if err != nil {
			return err
		}
		if written <= 0 {
			return io.ErrShortWrite
		}
		data = data[written:]
	}
	return nil
}

func statePath(parts ...string) string {
	return path.Join(parts...)
}
