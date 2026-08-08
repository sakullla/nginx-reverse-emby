package plugins

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

type packageSnapshot struct {
	temporaryRoot string
	packageRoot   string
	sourceRoot    string
	rootInfo      os.FileInfo
	entries       map[string]snapshotEntry
	options       ValidatorOptions
}

type snapshotEntry struct {
	info   os.FileInfo
	isDir  bool
	size   int64
	digest [sha256.Size]byte
}

func createPackageSnapshot(root string, options ValidatorOptions) (result packageSnapshot, resultErr error) {
	sourceRoot, err := resolvePackageRoot(root)
	if err != nil {
		return packageSnapshot{}, err
	}
	rootInfo, err := os.Lstat(sourceRoot)
	if err != nil {
		return packageSnapshot{}, validationError("package_root", ".", err)
	}
	temporaryRoot, err := os.MkdirTemp("", "nre-plugin-snapshot-")
	if err != nil {
		return packageSnapshot{}, validationError("snapshot", ".", err)
	}
	snapshot := packageSnapshot{
		temporaryRoot: temporaryRoot,
		packageRoot:   filepath.Join(temporaryRoot, "package"),
		sourceRoot:    sourceRoot,
		rootInfo:      rootInfo,
		entries:       make(map[string]snapshotEntry),
		options:       options,
	}
	cleanup := true
	defer func() {
		if cleanup {
			if err := os.RemoveAll(temporaryRoot); err != nil {
				result = packageSnapshot{}
				resultErr = errors.Join(resultErr, validationError("snapshot_cleanup", ".", err))
			}
		}
	}()
	if err := os.Mkdir(snapshot.packageRoot, 0o700); err != nil {
		return packageSnapshot{}, validationError("snapshot", ".", err)
	}

	paths, files, totalBytes := 0, 0, int64(0)
	regularFiles := newStableFileSet(options.MaxFiles)
	err = filepath.WalkDir(sourceRoot, func(current string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(sourceRoot, current)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		canonical := filepath.ToSlash(rel)
		if !fs.ValidPath(canonical) || len([]byte(canonical)) > MaxPackagePathBytes {
			return validationError("path", canonical, errors.New("package path is non-canonical or exceeds limit"))
		}
		paths++
		if paths > options.MaxFiles {
			return validationError("size_limit", canonical, errors.New("package path count exceeds limit"))
		}
		if err := ensureNoSymlinkComponents(sourceRoot, canonical); err != nil {
			return validationError("symlink", canonical, err)
		}
		info, err := os.Lstat(current)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return validationError("symlink", canonical, errors.New("symbolic links are forbidden"))
		}
		destination := filepath.Join(snapshot.packageRoot, filepath.FromSlash(canonical))
		if info.IsDir() {
			if err := os.Mkdir(destination, 0o700); err != nil {
				return validationError("snapshot", canonical, err)
			}
			snapshot.entries[canonical] = snapshotEntry{info: info, isDir: true}
			return nil
		}
		if !info.Mode().IsRegular() {
			return validationError("file_type", canonical, errors.New("only regular files are allowed"))
		}
		files++
		totalBytes += info.Size()
		if files > options.MaxFiles || totalBytes > options.MaxPackageBytes || info.Size() > options.MaxFileBytes {
			return validationError("size_limit", canonical, errors.New("package size or file count limit exceeded"))
		}
		identity, err := stableRegularFileKey(current, info)
		if err != nil {
			return validationError("file_identity", canonical, err)
		}
		if !regularFiles.add(identity) {
			return validationError("hardlink", canonical, errors.New("hard-linked package files are forbidden"))
		}
		digest, err := copyStableRegularFile(current, destination, info)
		if err != nil {
			return validationError("snapshot", canonical, err)
		}
		snapshot.entries[canonical] = snapshotEntry{info: info, size: info.Size(), digest: digest}
		return nil
	})
	if err != nil {
		return packageSnapshot{}, err
	}
	if err := snapshot.verifySource(); err != nil {
		return packageSnapshot{}, err
	}
	cleanup = false
	return snapshot, nil
}

// createMarketSnapshot captures the complete market tree with the market-level
// budgets before market.yaml or any package is interpreted. Reusing the same
// no-follow copier and identity revalidation as package snapshots ensures the
// catalog, package projections, and any caller-supplied manifest digest all
// describe one private filesystem image.
func createMarketSnapshot(root string, options ValidatorOptions) (packageSnapshot, error) {
	marketOptions := options
	marketOptions.MaxFiles = options.MaxMarketFiles
	marketOptions.MaxPackageBytes = options.MaxMarketBytes
	marketOptions.MaxFileBytes = options.MaxFileBytes
	if marketOptions.MaxFileBytes > marketOptions.MaxMarketBytes {
		marketOptions.MaxFileBytes = marketOptions.MaxMarketBytes
	}
	return createPackageSnapshot(root, marketOptions)
}

func copyStableRegularFile(source, destination string, expected os.FileInfo) ([sha256.Size]byte, error) {
	var digest [sha256.Size]byte
	file, err := os.Open(source)
	if err != nil {
		return digest, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil {
		return digest, err
	}
	current, err := os.Lstat(source)
	if err != nil {
		return digest, err
	}
	if current.Mode()&os.ModeSymlink != 0 || !opened.Mode().IsRegular() || !os.SameFile(expected, opened) || !os.SameFile(opened, current) {
		return digest, errors.New("source file identity changed before snapshot copy")
	}
	destinationFile, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, expected.Mode().Perm())
	if err != nil {
		return digest, err
	}
	hash := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(destinationFile, hash), io.LimitReader(file, expected.Size()+1))
	closeErr := destinationFile.Close()
	if copyErr != nil || closeErr != nil {
		return digest, errors.Join(copyErr, closeErr)
	}
	if written != expected.Size() {
		return digest, fmt.Errorf("source size changed during snapshot copy: copied %d, expected %d", written, expected.Size())
	}
	afterOpen, err := file.Stat()
	if err != nil {
		return digest, err
	}
	afterPath, err := os.Lstat(source)
	if err != nil {
		return digest, err
	}
	if afterPath.Mode()&os.ModeSymlink != 0 || !os.SameFile(expected, afterOpen) || !os.SameFile(afterOpen, afterPath) || afterOpen.Size() != expected.Size() || !afterOpen.ModTime().Equal(expected.ModTime()) {
		return digest, errors.New("source file changed during snapshot copy")
	}
	copy(digest[:], hash.Sum(nil))
	return digest, nil
}

func (snapshot packageSnapshot) verifySource() error {
	rootInfo, err := os.Lstat(snapshot.sourceRoot)
	if err != nil || rootInfo.Mode()&os.ModeSymlink != 0 || !rootInfo.IsDir() || !os.SameFile(snapshot.rootInfo, rootInfo) {
		if err == nil {
			err = errors.New("package root identity changed while validation was in progress")
		}
		return validationError("snapshot_changed", ".", err)
	}
	seen := make(map[string]struct{}, len(snapshot.entries))
	err = filepath.WalkDir(snapshot.sourceRoot, func(current string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(snapshot.sourceRoot, current)
		if err != nil || rel == "." {
			return err
		}
		canonical := filepath.ToSlash(rel)
		expected, ok := snapshot.entries[canonical]
		if !ok {
			return validationError("snapshot_changed", canonical, errors.New("package entry was added during validation"))
		}
		seen[canonical] = struct{}{}
		if err := ensureNoSymlinkComponents(snapshot.sourceRoot, canonical); err != nil {
			return validationError("snapshot_changed", canonical, err)
		}
		info, err := os.Lstat(current)
		if err != nil || info.Mode()&os.ModeSymlink != 0 || info.IsDir() != expected.isDir || !os.SameFile(expected.info, info) {
			if err == nil {
				err = errors.New("package entry identity changed during validation")
			}
			return validationError("snapshot_changed", canonical, err)
		}
		if expected.isDir {
			return nil
		}
		digest, err := hashStableRegularFile(current, expected.info, snapshot.options.MaxFileBytes)
		if err != nil || info.Size() != expected.size || digest != expected.digest {
			if err == nil {
				err = errors.New("package file content changed during validation")
			}
			return validationError("snapshot_changed", canonical, err)
		}
		return nil
	})
	if err != nil {
		return err
	}
	if len(seen) != len(snapshot.entries) {
		return validationError("snapshot_changed", ".", errors.New("package entry was removed during validation"))
	}
	finalRoot, err := os.Lstat(snapshot.sourceRoot)
	if err != nil || !os.SameFile(snapshot.rootInfo, finalRoot) {
		if err == nil {
			err = errors.New("package root identity changed during source verification")
		}
		return validationError("snapshot_changed", ".", err)
	}
	return nil
}

func hashStableRegularFile(name string, expected os.FileInfo, limit int64) ([sha256.Size]byte, error) {
	var digest [sha256.Size]byte
	file, err := os.Open(name)
	if err != nil {
		return digest, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil {
		return digest, err
	}
	current, err := os.Lstat(name)
	if err != nil {
		return digest, err
	}
	if current.Mode()&os.ModeSymlink != 0 || !opened.Mode().IsRegular() || !os.SameFile(expected, opened) || !os.SameFile(opened, current) {
		return digest, errors.New("source file identity changed during verification")
	}
	hash := sha256.New()
	written, err := io.Copy(hash, io.LimitReader(file, limit+1))
	if err != nil {
		return digest, err
	}
	after, err := file.Stat()
	if err != nil || written != expected.Size() || !os.SameFile(expected, after) || !after.ModTime().Equal(expected.ModTime()) {
		if err == nil {
			err = errors.New("source file changed during verification")
		}
		return digest, err
	}
	copy(digest[:], hash.Sum(nil))
	return digest, nil
}

func ensureNoSymlinkComponents(root, canonical string) error {
	current := root
	for _, component := range strings.Split(canonical, "/") {
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errors.New("symbolic links are forbidden")
		}
	}
	return nil
}
