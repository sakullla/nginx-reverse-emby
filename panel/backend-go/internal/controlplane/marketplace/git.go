package marketplace

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
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
	options := &git.CloneOptions{URL: source.URL, Depth: 1, SingleBranch: true, NoCheckout: false}
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
	pollInterval := f.PollInterval
	if pollInterval <= 0 {
		pollInterval = 25 * time.Millisecond
	}
	fetchCtx, cancelFetch := context.WithCancelCause(ctx)
	defer cancelFetch(context.Canceled)
	monitorDone := make(chan struct{})
	go func() {
		ticker := time.NewTicker(pollInterval)
		defer ticker.Stop()
		for {
			select {
			case <-monitorDone:
				return
			case <-fetchCtx.Done():
				return
			case <-ticker.C:
				if err := enforceFetchTreeBudget(destination, maxFiles, maxBytes); err != nil {
					cancelFetch(err)
					return
				}
			}
		}
	}()
	repository, err := git.PlainCloneContext(fetchCtx, destination, false, options)
	close(monitorDone)
	if cause := context.Cause(fetchCtx); cause != nil && !errors.Is(cause, context.Canceled) {
		return "", cause
	}
	if err != nil {
		return "", err
	}
	if err := enforceFetchTreeBudget(destination, maxFiles, maxBytes); err != nil {
		return "", err
	}
	head, err := repository.Head()
	if err != nil {
		return "", err
	}
	if err := removeGitMetadata(destination); err != nil {
		return "", fmt.Errorf("remove Git metadata: %w", err)
	}
	return head.Hash().String(), nil
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
