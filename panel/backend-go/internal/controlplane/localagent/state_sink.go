package localagent

import (
	"context"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

type RuntimeState = storage.RuntimeState

type RuntimeStateStore interface {
	SaveLocalRuntimeState(context.Context, string, storage.RuntimeState) error
}

type StateSink struct {
	store   RuntimeStateStore
	agentID string
}

func NewStateSink(store RuntimeStateStore, agentID string) *StateSink {
	return &StateSink{store: store, agentID: agentID}
}

func (s *StateSink) Save(ctx context.Context, state RuntimeState) error {
	return s.store.SaveLocalRuntimeState(ctx, s.agentID, state)
}
