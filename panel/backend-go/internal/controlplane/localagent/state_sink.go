package localagent

import (
	"context"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/service"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

type RuntimeState = storage.RuntimeState

type RuntimeStateStore interface {
	SaveLocalRuntimeState(context.Context, string, storage.RuntimeState) error
	service.LocalRuntimeManagedCertificateStore
}

type StateSink struct {
	store   RuntimeStateStore
	agentID string
	bridge  *syncRequestBridge
	now     func() time.Time
}

func NewStateSink(store RuntimeStateStore, agentID string) *StateSink {
	return newStateSinkWithBridge(store, agentID, nil)
}

func newStateSinkWithBridge(store RuntimeStateStore, agentID string, bridge *syncRequestBridge) *StateSink {
	return &StateSink{
		store:   store,
		agentID: agentID,
		bridge:  bridge,
		now:     time.Now,
	}
}

func (s *StateSink) Save(ctx context.Context, state RuntimeState) error {
	nextState := state
	if s.bridge != nil {
		nextState = mergeRuntimeStateWithSyncRequest(nextState, s.bridge.Load())
	}

	if err := s.store.SaveLocalRuntimeState(ctx, s.agentID, nextState); err != nil {
		return err
	}
	return service.ReconcileManagedCertificatesFromLocalRuntimeState(ctx, s.store, s.agentID, nextState, s.now())
}

func mergeRuntimeStateWithSyncRequest(state RuntimeState, request SyncRequest) RuntimeState {
	if request.LastApplyRevision > 0 {
		state.LastApplyRevision = int64(request.LastApplyRevision)
	}
	if request.LastApplyStatus != "" {
		state.LastApplyStatus = request.LastApplyStatus
	}
	if request.LastApplyMessage != "" {
		state.LastApplyMessage = request.LastApplyMessage
	}
	if len(request.ManagedCertificateReports) > 0 {
		state.ManagedCertificateReports = append([]storage.ManagedCertificateReport(nil), request.ManagedCertificateReports...)
	}
	return state
}
