package localagent

import (
	"context"
	"encoding/json"
	"strings"
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
	hasAuthoritativeApplyMetadata := hasAuthoritativeApplyMetadata(state.Metadata)

	if !hasAuthoritativeApplyMetadata && state.LastApplyRevision <= 0 && request.LastApplyRevision > 0 {
		state.LastApplyRevision = int64(request.LastApplyRevision)
	}
	if !hasAuthoritativeApplyMetadata && strings.TrimSpace(state.LastApplyStatus) == "" && strings.TrimSpace(request.LastApplyStatus) != "" {
		state.LastApplyStatus = request.LastApplyStatus
	}
	if !hasAuthoritativeApplyMetadata && strings.TrimSpace(state.LastApplyMessage) == "" && strings.TrimSpace(request.LastApplyMessage) != "" {
		state.LastApplyMessage = request.LastApplyMessage
	}
	if len(state.ManagedCertificateReports) == 0 && len(request.ManagedCertificateReports) > 0 {
		state.ManagedCertificateReports = append([]storage.ManagedCertificateReport(nil), request.ManagedCertificateReports...)
	}
	if len(request.Stats) > 0 {
		statsJSON, err := json.Marshal(request.Stats)
		if err == nil {
			if state.Metadata == nil {
				state.Metadata = map[string]string{}
			}
			state.Metadata["stats"] = string(statsJSON)
		}
	}
	return state
}

func hasAuthoritativeApplyMetadata(metadata map[string]string) bool {
	if metadata == nil {
		return false
	}
	if strings.TrimSpace(metadata["last_sync_error"]) != "" {
		return true
	}
	if strings.TrimSpace(metadata["last_apply_status"]) != "" {
		return true
	}
	if strings.TrimSpace(metadata["last_apply_revision"]) != "" {
		return true
	}
	if strings.TrimSpace(metadata["last_apply_message"]) != "" {
		return true
	}
	return false
}
