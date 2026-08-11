package marketplace

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/go-git/go-billy/v5"
	billyingos "github.com/go-git/go-billy/v5/osfs"
	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/cache"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/transport"
	gitfilesystem "github.com/go-git/go-git/v5/storage/filesystem"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/plugins"
)

type CredentialResolver func(context.Context, string) (transport.AuthMethod, error)

// GoGitFetcher deliberately uses a Go library and never depends on a Git CLI
// being present in the control-plane image.
type GoGitFetcher struct {
	ResolveCredential CredentialResolver
	MaxFiles          int
	MaxBytes          int64
	PollInterval      time.Duration
}

func (f GoGitFetcher) Fetch(ctx context.Context, source Source, destination string) (string, error) {
	if err := ValidateSource(source); err != nil {
		return "", err
	}
	referenceName, err := sourceReferenceName(source)
	if err != nil {
		return "", err
	}
	options := &git.CloneOptions{URL: source.URL, Depth: 1, SingleBranch: true, NoCheckout: true, ReferenceName: referenceName}
	if source.CredentialRef != "" {
		if f.ResolveCredential == nil {
			return "", fmt.Errorf("credential resolver is required for source %s", source.ID)
		}
		auth, err := f.ResolveCredential(ctx, source.CredentialRef)
		if err != nil {
			return "", err
		}
		options.Auth = auth
	}
	maxFiles, maxBytes := f.MaxFiles, f.MaxBytes
	if maxFiles <= 0 {
		maxFiles = plugins.DefaultMaxMarketFiles * 2
	}
	if maxBytes <= 0 {
		maxBytes = plugins.DefaultMaxMarketBytes
	}
	parent := filepath.Dir(destination)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return "", err
	}
	bareRoot, err := os.MkdirTemp(parent, ".market-bare-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(bareRoot)
	repository, closeStorage, err := cloneBareBudgeted(ctx, bareRoot, options, maxBytes)
	if err != nil {
		return "", err
	}
	defer closeStorage()
	resolved, err := repository.Reference(referenceName, false)
	if err != nil {
		return "", fmt.Errorf("configured %s ref %q was not fetched: %w", source.RefKind, source.RefName, err)
	}
	commit, err := peelCommit(repository, resolved.Hash(), 8)
	if err != nil {
		return "", err
	}
	tree, err := commit.Tree()
	if err != nil {
		return "", err
	}
	if err := checkoutBudgetedTree(ctx, tree, destination, maxFiles, maxBytes); err != nil {
		_ = os.RemoveAll(destination)
		return "", err
	}
	remote, err := repository.Remote("origin")
	if err != nil {
		_ = os.RemoveAll(destination)
		return "", err
	}
	advertised, err := remote.ListContext(ctx, &git.ListOptions{Auth: options.Auth})
	if err != nil {
		_ = os.RemoveAll(destination)
		return "", fmt.Errorf("revalidate configured Git ref: %w", err)
	}
	matched := 0
	for _, candidate := range advertised {
		if candidate.Name() == referenceName {
			matched++
			if candidate.Hash() != resolved.Hash() {
				_ = os.RemoveAll(destination)
				return "", errors.New("configured Git ref moved during refresh")
			}
		}
	}
	if matched != 1 {
		_ = os.RemoveAll(destination)
		return "", errors.New("configured Git ref is missing or ambiguous")
	}
	return commit.Hash.String(), nil
}

func sourceReferenceName(source Source) (plumbing.ReferenceName, error) {
	if strings.HasPrefix(source.RefName, "refs/") {
		return "", errors.New("configured Git ref_name must be an unqualified branch or tag name")
	}
	var name plumbing.ReferenceName
	switch source.RefKind {
	case GitRefKindBranch:
		name = plumbing.NewBranchReferenceName(source.RefName)
	case GitRefKindTag:
		name = plumbing.NewTagReferenceName(source.RefName)
	default:
		return "", fmt.Errorf("unsupported Git ref kind %q", source.RefKind)
	}
	if err := name.Validate(); err != nil {
		return "", fmt.Errorf("invalid configured Git ref: %w", err)
	}
	return name, nil
}

func peelCommit(repository *git.Repository, hash plumbing.Hash, maxDepth int) (*object.Commit, error) {
	seen := make(map[plumbing.Hash]struct{}, maxDepth+1)
	for depth := 0; depth <= maxDepth; depth++ {
		if _, duplicate := seen[hash]; duplicate {
			return nil, errors.New("Git tag peel cycle detected")
		}
		seen[hash] = struct{}{}
		if commit, err := repository.CommitObject(hash); err == nil {
			return commit, nil
		}
		tag, err := repository.TagObject(hash)
		if err != nil {
			return nil, errors.New("configured Git ref does not resolve to a commit")
		}
		hash = tag.Target
	}
	return nil, errors.New("configured Git tag peel depth exceeds limit")
}

func cloneBareBudgeted(ctx context.Context, bareRoot string, options *git.CloneOptions, maxBytes int64) (*git.Repository, func() error, error) {
	if maxBytes <= 0 {
		return nil, nil, errors.New("marketplace Git transfer byte budget must be positive")
	}
	quota := &gitWriteQuota{remaining: maxBytes}
	filesystem := &quotaFilesystem{Filesystem: billyingos.New(bareRoot), quota: quota, files: newQuotaFileTracker()}
	storage := gitfilesystem.NewStorage(filesystem, cache.NewObjectLRUDefault())
	repository, err := git.CloneContext(ctx, storage, nil, options)
	if err != nil {
		filesystem.files.closeAll()
		_ = storage.Close()
		if quota.wasExceeded() {
			return nil, nil, errors.New("marketplace Git transfer exceeds byte budget")
		}
		return nil, nil, err
	}
	return repository, func() error {
		filesystem.files.closeAll()
		return storage.Close()
	}, nil
}

type gitWriteQuota struct {
	mu        sync.Mutex
	remaining int64
	exceeded  bool
}

func (q *gitWriteQuota) reserve(size int64) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if size < 0 || size > q.remaining {
		q.exceeded = true
		return errors.New("marketplace Git transfer exceeds byte budget")
	}
	q.remaining -= size
	return nil
}

func (q *gitWriteQuota) wasExceeded() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.exceeded
}

type quotaFilesystem struct {
	billy.Filesystem
	quota *gitWriteQuota
	files *quotaFileTracker
}

func (f *quotaFilesystem) Create(name string) (billy.File, error) {
	file, err := f.Filesystem.Create(name)
	return f.wrap(file, err)
}

func (f *quotaFilesystem) Open(name string) (billy.File, error) {
	file, err := f.Filesystem.Open(name)
	return f.wrap(file, err)
}

func (f *quotaFilesystem) OpenFile(name string, flag int, permission os.FileMode) (billy.File, error) {
	file, err := f.Filesystem.OpenFile(name, flag, permission)
	return f.wrap(file, err)
}

func (f *quotaFilesystem) TempFile(directory, prefix string) (billy.File, error) {
	file, err := f.Filesystem.TempFile(directory, prefix)
	return f.wrap(file, err)
}

func (f *quotaFilesystem) Chroot(path string) (billy.Filesystem, error) {
	filesystem, err := f.Filesystem.Chroot(path)
	if err != nil {
		return nil, err
	}
	return &quotaFilesystem{Filesystem: filesystem, quota: f.quota, files: f.files}, nil
}

func (f *quotaFilesystem) wrap(file billy.File, err error) (billy.File, error) {
	if err != nil {
		return nil, err
	}
	wrapped := &quotaFile{File: file, quota: f.quota, files: f.files}
	f.files.add(wrapped)
	return wrapped, nil
}

type quotaFile struct {
	billy.File
	quota *gitWriteQuota
	files *quotaFileTracker
}

func (f *quotaFile) Write(data []byte) (int, error) {
	if err := f.quota.reserve(int64(len(data))); err != nil {
		_ = f.File.Close()
		return 0, err
	}
	return f.File.Write(data)
}

func (f *quotaFile) Truncate(size int64) error {
	position, err := f.File.Seek(0, io.SeekCurrent)
	if err != nil {
		return err
	}
	currentSize, err := f.File.Seek(0, io.SeekEnd)
	if err != nil {
		return err
	}
	if _, err := f.File.Seek(position, io.SeekStart); err != nil {
		return err
	}
	if size > currentSize {
		if err := f.quota.reserve(size - currentSize); err != nil {
			_ = f.File.Close()
			return err
		}
	}
	return f.File.Truncate(size)
}

func (f *quotaFile) Close() error {
	f.files.remove(f)
	return f.File.Close()
}

type quotaFileTracker struct {
	mu    sync.Mutex
	files map[*quotaFile]struct{}
}

func newQuotaFileTracker() *quotaFileTracker {
	return &quotaFileTracker{files: make(map[*quotaFile]struct{})}
}

func (t *quotaFileTracker) add(file *quotaFile) {
	t.mu.Lock()
	t.files[file] = struct{}{}
	t.mu.Unlock()
}

func (t *quotaFileTracker) remove(file *quotaFile) {
	t.mu.Lock()
	delete(t.files, file)
	t.mu.Unlock()
}

func (t *quotaFileTracker) closeAll() {
	t.mu.Lock()
	files := make([]*quotaFile, 0, len(t.files))
	for file := range t.files {
		files = append(files, file)
	}
	t.files = make(map[*quotaFile]struct{})
	t.mu.Unlock()
	for _, file := range files {
		_ = file.File.Close()
	}
}

func checkoutBudgetedTree(ctx context.Context, tree *object.Tree, destination string, maxFiles int, maxBytes int64) error {
	var files []*object.File
	total := int64(0)
	iter := tree.Files()
	err := iter.ForEach(func(file *object.File) error {
		if file.Mode != filemode.Regular && file.Mode != filemode.Deprecated && file.Mode != filemode.Executable {
			return fmt.Errorf("marketplace Git tree contains unsupported mode %s", file.Mode)
		}
		if len(files)+1 > maxFiles || file.Blob.Size < 0 || total+file.Blob.Size > maxBytes {
			return errors.New("marketplace fetch exceeds total file or byte budget")
		}
		total += file.Blob.Size
		files = append(files, file)
		return nil
	})
	if err != nil {
		return err
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Name < files[j].Name })
	if err := os.MkdirAll(destination, 0o755); err != nil {
		return err
	}
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return err
		}
		target, err := secureCheckoutPath(destination, file.Name)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		reader, err := file.Reader()
		if err != nil {
			return err
		}
		// Checkout content is always non-executable. The signed package manifest
		// retains the canonical 0755 artifact role, and the runtime installer only
		// restores execution permission after platform and digest verification.
		writer, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err != nil {
			_ = reader.Close()
			return err
		}
		_, copyErr := io.CopyN(writer, reader, file.Blob.Size)
		closeErr := errors.Join(writer.Close(), reader.Close())
		if copyErr != nil || closeErr != nil {
			return errors.Join(copyErr, closeErr)
		}
	}
	return nil
}

func secureCheckoutPath(root, name string) (string, error) {
	name = filepath.FromSlash(name)
	if filepath.IsAbs(name) {
		return "", errors.New("marketplace Git path is absolute")
	}
	target := filepath.Clean(filepath.Join(root, name))
	relative, err := filepath.Rel(root, target)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", errors.New("marketplace Git path escapes checkout root")
	}
	return target, nil
}

func removeGitMetadata(root string) error {
	return os.RemoveAll(filepath.Join(root, ".git"))
}

func enforceFetchTreeBudget(root string, maxFiles int, maxBytes int64) error {
	files, bytes := 0, int64(0)
	err := filepath.WalkDir(root, func(_ string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if errors.Is(walkErr, os.ErrNotExist) {
				return nil
			}
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		files++
		bytes += info.Size()
		if files > maxFiles || bytes > maxBytes {
			return errors.New("marketplace fetch exceeds total file or byte budget")
		}
		return nil
	})
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
