package core

import (
	"context"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	agentruntime "github.com/sakullla/nginx-reverse-emby/go-agent/internal/runtime"
)

type ModuleApplier interface {
	Apply(context.Context, model.Snapshot, model.Snapshot) error
}

func NewSnapshotActivator(modules ModuleApplier) agentruntime.Activator {
	return func(ctx context.Context, previous, next model.Snapshot) error {
		if modules == nil {
			return nil
		}
		return modules.Apply(ctx, previous, next)
	}
}
