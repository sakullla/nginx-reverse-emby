package localagent

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/service"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

type Snapshot = storage.Snapshot

type SyncRequest struct {
	CurrentRevision           int
	LastApplyRevision         int
	LastApplyStatus           string
	LastApplyMessage          string
	Stats                     map[string]any
	StatsPresent              bool
	ManagedCertificateReports []storage.ManagedCertificateReport
}

type SnapshotStore interface {
	LoadLocalSnapshot(context.Context, string) (storage.Snapshot, error)
}

type trafficSummaryService interface {
	IngestHeartbeat(context.Context, string, service.AgentStats) error
	Summary(context.Context, string) (service.TrafficSummary, error)
	BlockState(context.Context, string) (bool, string, error)
}

type SyncSource struct {
	store               SnapshotStore
	agentID             string
	bridge              *syncRequestBridge
	trafficService      trafficSummaryService
	trafficStatsEnabled bool
}

func NewSyncSource(store SnapshotStore, agentID string) *SyncSource {
	return newSyncSourceWithBridge(store, agentID, nil)
}

func newSyncSourceWithBridge(store SnapshotStore, agentID string, bridge *syncRequestBridge) *SyncSource {
	return &SyncSource{
		store:               store,
		agentID:             agentID,
		bridge:              bridge,
		trafficStatsEnabled: true,
	}
}

func (s *SyncSource) SetTrafficService(enabled bool, trafficService trafficSummaryService) {
	s.trafficStatsEnabled = enabled
	s.trafficService = trafficService
}

func (s *SyncSource) Sync(ctx context.Context, request SyncRequest) (Snapshot, error) {
	if s.bridge != nil {
		s.bridge.Store(request)
	}
	snapshot, err := s.store.LoadLocalSnapshot(ctx, s.agentID)
	if err != nil {
		return Snapshot{}, err
	}
	snapshot.AgentConfig.TrafficStatsEnabled = boolPtr(s.trafficStatsEnabled)
	if !s.trafficStatsEnabled || s.trafficService == nil {
		snapshot.AgentConfig.TrafficBlocked = false
		snapshot.AgentConfig.TrafficBlockReason = ""
		return snapshot, nil
	}
	if len(request.Stats) > 0 {
		_ = s.trafficService.IngestHeartbeat(ctx, s.agentID, service.AgentStats(request.Stats))
	}
	blocked, reason, err := s.trafficService.BlockState(ctx, s.agentID)
	if err != nil {
		snapshot.AgentConfig.TrafficBlocked = false
		snapshot.AgentConfig.TrafficBlockReason = ""
		return snapshot, nil
	}
	snapshot.AgentConfig.TrafficBlocked = blocked
	snapshot.AgentConfig.TrafficBlockReason = reason
	return snapshot, nil
}

func boolPtr(value bool) *bool {
	return &value
}

type syncRequestBridge struct {
	mu      sync.RWMutex
	request SyncRequest
}

func newSyncRequestBridge() *syncRequestBridge {
	return &syncRequestBridge{}
}

func (b *syncRequestBridge) Store(request SyncRequest) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.request = cloneSyncRequest(request)
}

func (b *syncRequestBridge) Load() SyncRequest {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return cloneSyncRequest(b.request)
}

func cloneSyncRequest(request SyncRequest) SyncRequest {
	copyValue := request
	if len(request.ManagedCertificateReports) > 0 {
		copyValue.ManagedCertificateReports = append([]storage.ManagedCertificateReport(nil), request.ManagedCertificateReports...)
	}
	if request.Stats != nil {
		data, err := json.Marshal(request.Stats)
		if err == nil {
			var stats map[string]any
			if json.Unmarshal(data, &stats) == nil {
				copyValue.Stats = stats
			}
		}
	}
	return copyValue
}
