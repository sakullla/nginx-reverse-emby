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
	"time"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/transport"
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
	options := &git.CloneOptions{URL: source.URL, Depth: 1, SingleBranch: true, NoCheckout: true}
	if reference := strings.TrimSpace(source.Reference); reference != "" {
		if strings.HasPrefix(reference, "refs/") {
			options.ReferenceName = plumbing.ReferenceName(reference)
		} else {
			options.ReferenceName = plumbing.NewBranchReferenceName(reference)
		}
	}
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
	repository, err := git.PlainCloneContext(ctx, bareRoot, true, options)
	if err != nil {
		return "", err
	}
	head, err := repository.Head()
	if err != nil {
		return "", err
	}
	commit, err := repository.CommitObject(head.Hash())
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
	return head.Hash().String(), nil
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
