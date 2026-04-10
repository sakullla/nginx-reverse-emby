package localagent

import (
	"context"
	"net/http"
	"reflect"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/app"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
)

type bridgeStoreStub struct {
	snapshot             Snapshot
	loadAgentID          string
	savedAgentID         string
	savedState           RuntimeState
	saveLocalStateCalled bool
}

func (s *bridgeStoreStub) LoadLocalSnapshot(_ context.Context, agentID string) (Snapshot, error) {
	s.loadAgentID = agentID
	return s.snapshot, nil
}

func (s *bridgeStoreStub) SaveLocalRuntimeState(_ context.Context, agentID string, state RuntimeState) error {
	s.savedAgentID = agentID
	s.savedState = state
	s.saveLocalStateCalled = true
	return nil
}

func TestAppStartsEmbeddedLocalAgentWhenEnabled(t *testing.T) {
	var started bool
	application := app.New(
		config.Config{
			ListenAddr:       "127.0.0.1:0",
			EnableLocalAgent: true,
		},
		http.NewServeMux(),
		nil,
		func(context.Context) error {
			started = true
			return nil
		},
	)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_ = application.Run(ctx)

	if !started {
		t.Fatal("embedded local agent did not start")
	}
}

func TestLocalSyncSourceReturnsSnapshotFromControlPlaneStore(t *testing.T) {
	store := &bridgeStoreStub{
		snapshot: Snapshot{
			DesiredVersion: "1.2.3",
			Revision:       15,
		},
	}

	source := NewSyncSource(store, "local")
	got, err := source.Sync(t.Context(), SyncRequest{CurrentRevision: 14})
	if err != nil {
		t.Fatalf("Sync() error = %v", err)
	}

	if store.loadAgentID != "local" {
		t.Fatalf("LoadLocalSnapshot() agentID = %q", store.loadAgentID)
	}
	if got.Revision != 15 || got.DesiredVersion != "1.2.3" {
		t.Fatalf("Sync() = %+v", got)
	}
}

func TestLocalStateSinkPersistsRuntimeStateToControlPlaneStore(t *testing.T) {
	store := &bridgeStoreStub{}
	sink := NewStateSink(store, "local")
	state := RuntimeState{
		CurrentRevision: 27,
		Status:          "error",
		Metadata: map[string]string{
			"last_sync_error": "apply failed",
		},
	}

	if err := sink.Save(t.Context(), state); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	if !store.saveLocalStateCalled {
		t.Fatal("SaveLocalRuntimeState() was not called")
	}
	if store.savedAgentID != "local" {
		t.Fatalf("SaveLocalRuntimeState() agentID = %q", store.savedAgentID)
	}
	if !reflect.DeepEqual(store.savedState, state) {
		t.Fatalf("SaveLocalRuntimeState() state = %+v", store.savedState)
	}
}
