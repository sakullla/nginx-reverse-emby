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
	info, err := os.Lstat(absRoot)
	switch {
	case err == nil:
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return nil, errors.New("state root is not a regular directory")
		}
	case os.IsNotExist(err):
		if err := os.MkdirAll(absRoot, stateDirectoryMode); err != nil {
			return nil, errors.New("state root could not be created")
		}
	default:
		return nil, errors.New("state root could not be inspected")
	}
	if err := os.Chmod(absRoot, stateDirectoryMode); err != nil {
		return nil, errors.New("state root permissions could not be restricted")
	}
	info, err = os.Lstat(absRoot)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return nil, errors.New("state root changed during initialization")
	}
	root, err := os.OpenRoot(absRoot)
	if err != nil {
		return nil, errors.New("state root could not be opened")
	}
	return &durableFilesystem{root: root, rootPath: absRoot, inject: inject}, nil
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
	if err := filesystem.root.MkdirAll(name, stateDirectoryMode); err != nil {
		return errors.New("state directory could not be created")
	}
	current := ""
	for _, component := range strings.Split(name, "/") {
		if current == "" {
			current = component
		} else {
			current = path.Join(current, component)
		}
		info, err := filesystem.root.Lstat(current)
		if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return errors.New("state directory is not safe")
		}
		if err := filesystem.root.Chmod(current, stateDirectoryMode); err != nil {
			return errors.New("state directory permissions could not be restricted")
		}
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
	oldInfo, err := filesystem.root.Lstat(oldName)
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
	info, err := filesystem.root.Lstat(name)
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
	info, err := filesystem.root.Lstat(name)
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
