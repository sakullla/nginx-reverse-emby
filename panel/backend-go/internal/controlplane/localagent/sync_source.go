package localagent

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

type Snapshot = storage.Snapshot

type SyncRequest struct {
	CurrentRevision           int
	LastApplyRevision         int
	LastApplyStatus           string
	LastApplyMessage          string
	Stats                     map[string]any
	ManagedCertificateReports []storage.ManagedCertificateReport
}

type SnapshotStore interface {
	LoadLocalSnapshot(context.Context, string) (storage.Snapshot, error)
}

type SyncSource struct {
	store   SnapshotStore
	agentID string
	bridge  *syncRequestBridge
}

func NewSyncSource(store SnapshotStore, agentID string) *SyncSource {
	return &SyncSource{store: store, agentID: agentID}
}

func newSyncSourceWithBridge(store SnapshotStore, agentID string, bridge *syncRequestBridge) *SyncSource {
	return &SyncSource{
		store:   store,
		agentID: agentID,
		bridge:  bridge,
	}
}

func (s *SyncSource) Sync(ctx context.Context, request SyncRequest) (Snapshot, error) {
	if s.bridge != nil {
		s.bridge.Store(request)
	}
	return s.store.LoadLocalSnapshot(ctx, s.agentID)
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
	if len(request.Stats) > 0 {
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
