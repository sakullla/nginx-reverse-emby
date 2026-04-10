package localagent

import (
	"context"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

type Snapshot = storage.Snapshot

type SyncRequest struct {
	CurrentRevision int
}

type SnapshotStore interface {
	LoadLocalSnapshot(context.Context, string) (storage.Snapshot, error)
}

type SyncSource struct {
	store   SnapshotStore
	agentID string
}

func NewSyncSource(store SnapshotStore, agentID string) *SyncSource {
	return &SyncSource{store: store, agentID: agentID}
}

func (s *SyncSource) Sync(ctx context.Context, _ SyncRequest) (Snapshot, error) {
	return s.store.LoadLocalSnapshot(ctx, s.agentID)
}
