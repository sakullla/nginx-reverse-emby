package app

import "context"

type syncClientFunc func(context.Context, SyncRequest) (Snapshot, error)

func (f syncClientFunc) Sync(ctx context.Context, req SyncRequest) (Snapshot, error) {
	return f(ctx, req)
}
