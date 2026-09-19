package localagent

import (
	"context"
	"errors"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/service"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

type trafficSnapshotStore struct {
	snapshot storage.Snapshot
}

func (s trafficSnapshotStore) LoadLocalSnapshot(context.Context, string) (storage.Snapshot, error) {
	return s.snapshot, nil
}

type failingIngestTrafficService struct {
	blockStateCalled bool
}

func (*failingIngestTrafficService) IngestHeartbeat(context.Context, string, service.AgentStats) error {
	return errors.New("traffic storage unavailable")
}

func (*failingIngestTrafficService) Summary(context.Context, string) (service.TrafficSummary, error) {
	return service.TrafficSummary{}, nil
}

func (s *failingIngestTrafficService) BlockState(context.Context, string) (bool, string, error) {
	s.blockStateCalled = true
	return false, "", nil
}

func TestSyncPreservesDurableTrafficBlockWhenIngestFails(t *testing.T) {
	traffic := &failingIngestTrafficService{}
	source := NewSyncSource(trafficSnapshotStore{snapshot: storage.Snapshot{
		AgentConfig: storage.AgentConfig{
			TrafficBlocked:     true,
			TrafficBlockReason: "durable quota block",
		},
	}}, "local")
	source.SetTrafficService(true, traffic)

	snapshot, err := source.Sync(t.Context(), SyncRequest{
		Stats: map[string]any{"host_total": map[string]any{"rx_bytes": 2}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.AgentConfig.TrafficBlocked || snapshot.AgentConfig.TrafficBlockReason != "durable quota block" {
		t.Fatalf("traffic block = %v %q, want durable state preserved", snapshot.AgentConfig.TrafficBlocked, snapshot.AgentConfig.TrafficBlockReason)
	}
	if traffic.blockStateCalled {
		t.Fatal("BlockState called after failed traffic ingestion")
	}
}
