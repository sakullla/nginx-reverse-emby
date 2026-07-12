package service

import "github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/revision"

func NewMutationExecutor(store revision.Store, options ...revision.Option) *revision.Executor {
	base := []revision.Option{revision.WithSnapshotValidator(FullSnapshotValidator{})}
	return revision.NewExecutor(store, append(base, options...)...)
}
