package service

import (
	"path/filepath"
	"strings"

	"github.com/sakullla/nginx-reverse-emby/go-agent/pkg/acmeflow"
	"github.com/sakullla/nginx-reverse-emby/go-agent/pkg/acmeflow/cloudflare"
)

type masterACMEStateStore interface {
	acmeflow.AccountStore
	cloudflare.ChallengeIntentStore
	Close() error
}

type masterACMEAccountStore struct {
	*acmeflow.StateStore
	root string
}

var _ masterACMEStateStore = (*masterACMEAccountStore)(nil)

func openMasterACMEAccountStore(dataDir string, options ...acmeflow.StateStoreOption) (*masterACMEAccountStore, error) {
	dataDir = strings.TrimSpace(dataDir)
	if dataDir == "" {
		return nil, normalizeManagedCertificateACMEError("master_account_open", acmeflow.CategoryAccount, acmeflow.ErrAccountNotFound)
	}

	root := filepath.Join(dataDir, "acme", "master")
	store, err := acmeflow.OpenStateStore(root, options...)
	if err != nil {
		return nil, normalizeManagedCertificateACMEError("master_account_open", acmeflow.CategoryAccount, err)
	}
	return &masterACMEAccountStore{StateStore: store, root: root}, nil
}
