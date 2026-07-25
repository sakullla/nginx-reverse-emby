package service

import (
	"context"
	"path/filepath"
	"strings"
	"sync"

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

type masterACMEAccountLifecycleGate struct {
	token      chan struct{}
	references int
}

var masterACMEAccountLifecycleGates = struct {
	sync.Mutex
	entries map[string]*masterACMEAccountLifecycleGate
}{entries: make(map[string]*masterACMEAccountLifecycleGate)}

func acquireMasterACMEAccountLifecycle(ctx context.Context, dataDir, directoryURL, email string) (func(), error) {
	key := strings.Join([]string{
		filepath.Clean(strings.TrimSpace(dataDir)),
		strings.TrimSpace(directoryURL),
		strings.TrimSpace(email),
	}, "\x00")

	masterACMEAccountLifecycleGates.Lock()
	gate := masterACMEAccountLifecycleGates.entries[key]
	if gate == nil {
		gate = &masterACMEAccountLifecycleGate{token: make(chan struct{}, 1)}
		gate.token <- struct{}{}
		masterACMEAccountLifecycleGates.entries[key] = gate
	}
	gate.references++
	masterACMEAccountLifecycleGates.Unlock()

	releaseReference := func() {
		masterACMEAccountLifecycleGates.Lock()
		defer masterACMEAccountLifecycleGates.Unlock()
		gate.references--
		if gate.references == 0 && masterACMEAccountLifecycleGates.entries[key] == gate {
			delete(masterACMEAccountLifecycleGates.entries, key)
		}
	}

	select {
	case <-ctx.Done():
		releaseReference()
		return nil, ctx.Err()
	case <-gate.token:
	}
	if err := ctx.Err(); err != nil {
		gate.token <- struct{}{}
		releaseReference()
		return nil, err
	}

	var once sync.Once
	return func() {
		once.Do(func() {
			gate.token <- struct{}{}
			releaseReference()
		})
	}, nil
}

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
