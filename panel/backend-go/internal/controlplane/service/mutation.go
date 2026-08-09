package service

import (
	"context"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/revision"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

func NewMutationExecutor(store revision.Store, options ...revision.Option) *revision.Executor {
	return newMutationExecutor(config.Config{}, store, options...)
}

func newMutationExecutor(cfg config.Config, store revision.Store, options ...revision.Option) *revision.Executor {
	base := []revision.Option{
		revision.WithSnapshotValidator(FullSnapshotValidator{}),
	}
	return newRevisionExecutor(cfg, store, append(base, options...)...)
}

func newRevisionExecutor(cfg config.Config, store revision.Store, options ...revision.Option) *revision.Executor {
	options = append(options,
		revision.WithSnapshotDecorator(revision.SnapshotDecoratorFunc(
			func(ctx context.Context, tx *storage.GormStore, target revision.Target, snapshot storage.Snapshot) (storage.Snapshot, error) {
				return overlayPendingManagedCertificateGenerationsForConfig(ctx, cfg, tx, target.AgentID, snapshot)
			},
		)),
		revision.WithSnapshotDecorator(revision.SnapshotDecoratorFunc(
			func(ctx context.Context, tx *storage.GormStore, target revision.Target, snapshot storage.Snapshot) (storage.Snapshot, error) {
				present, err := tx.HasPKICanonicalSchema(ctx)
				if err != nil || !present {
					return snapshot, err
				}
				state, err := tx.LoadPKICanonicalState(ctx)
				if err != nil {
					return storage.Snapshot{}, err
				}
				if state.Settings == nil {
					return snapshot, nil
				}
				snapshot.RelayListeners, err = prepareRelayListenersWithPKIState(state, target.AgentID, snapshot.RelayListeners)
				if err != nil {
					return storage.Snapshot{}, err
				}
				return snapshot, nil
			},
		)),
	)
	return revision.NewExecutor(store, options...)
}
