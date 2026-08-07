package marketplace

import (
	"context"
	"fmt"
	"strings"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/transport"
)

type CredentialResolver func(context.Context, string) (transport.AuthMethod, error)

// GoGitFetcher deliberately uses a Go library and never depends on a Git CLI
// being present in the control-plane image.
type GoGitFetcher struct {
	ResolveCredential CredentialResolver
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
	repository, err := git.PlainCloneContext(ctx, destination, false, options)
	if err != nil {
		return "", err
	}
	head, err := repository.Head()
	if err != nil {
		return "", err
	}
	return head.Hash().String(), nil
}
