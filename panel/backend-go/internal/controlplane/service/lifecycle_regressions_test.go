package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/coordinator"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

func TestCloudflareRecordTTLChangeTriggersPatch(t *testing.T) {
	var patches atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/zones":
			_, _ = w.Write([]byte(`{"success":true,"result":[{"id":"zone-1","name":"example.com"}],"result_info":{"total_pages":1}}`))
		case request.Method == http.MethodGet && request.URL.Path == "/zones/zone-1/dns_records":
			_, _ = w.Write([]byte(`{"success":true,"result":[{"id":"record-1","type":"A","name":"media.example.com","content":"203.0.113.10","ttl":60}],"result_info":{"total_pages":1}}`))
		case request.Method == http.MethodPatch && request.URL.Path == "/zones/zone-1/dns_records/record-1":
			patches.Add(1)
			var body struct {
				Content string `json:"content"`
				TTL     int    `json:"ttl"`
			}
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Errorf("decode PATCH body: %v", err)
			}
			if body.Content != "203.0.113.10" || body.TTL != 120 {
				t.Errorf("PATCH body = %+v", body)
			}
			_, _ = w.Write([]byte(`{"success":true,"result":{"id":"record-1"}}`))
		default:
			http.Error(w, "unexpected request", http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)

	client := newHTTPCloudflareClient(server.URL, time.Second)
	outcome, err := client.EnsureRecord(t.Context(), "token", "media.example.com", "A", "203.0.113.10", 120)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Action != "updated" || patches.Load() != 1 {
		t.Fatalf("TTL reconcile outcome = %+v, patches = %d", outcome, patches.Load())
	}
}

func TestTimeoutForcedDrainReportAcceptedDuringGraceWindow(t *testing.T) {
	store, err := storage.NewSQLiteStore(t.TempDir(), "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	appliedAt := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	seedAppliedDrain(t, store, appliedAt, 5)
	now := appliedAt.Add(15 * time.Second)
	clock := lifecycleCoordinatorClock{now: now}
	coord, err := coordinator.New(store, coordinator.Options{Clock: clock})
	if err != nil {
		t.Fatal(err)
	}
	api := NewRevisionAPI(store, coord)
	api.now = clock.Now

	_, err = api.ReportRemoteRevision(t.Context(), "edge-1", RemoteRevisionReport{
		AgentID: "edge-1", Revision: 2, RetryCycle: 0, Attempt: 1,
		LeaseID: "lease-2", GenerationID: "generation-1",
		Status: storage.AgentRevisionDrainStateForced, Forced: true, ForceReason: "timeout",
	})
	if err != nil {
		t.Fatalf("ReportRemoteRevision(timeout forced) error = %v", err)
	}
	generation, found, err := store.GetCoordinatorGeneration(t.Context(), "edge-1", "generation-1")
	if err != nil || !found || generation.State != storage.AgentRevisionDrainStateForced {
		t.Fatalf("forced generation = %+v, found=%v, error=%v", generation, found, err)
	}
}

func seedAppliedDrain(t *testing.T, store *storage.GormStore, appliedAt time.Time, drainTimeoutSeconds int) {
	t.Helper()
	snapshotPayload, err := json.Marshal(storage.Snapshot{Revision: 2})
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(snapshotPayload)
	digestText := hex.EncodeToString(digest[:])
	finishedAt := appliedAt
	ledger := storage.RevisionLedgerWrite{
		Operation: storage.OperationRow{
			ID: "operation-drain", Kind: "test", Status: storage.OperationStatusApplied,
			PrimaryAgentID: "edge-1", CreatedAt: appliedAt, UpdatedAt: appliedAt,
		},
		Artifacts: []storage.GenerationArtifactRow{{
			ID: "snapshot-drain", Kind: "agent_snapshot", SHA256: digestText,
			Payload: snapshotPayload, SizeBytes: int64(len(snapshotPayload)), CreatedAt: appliedAt,
		}},
		Revisions: []storage.AgentRevisionRow{{
			AgentID: "edge-1", Revision: 2, OperationID: "operation-drain", State: storage.AgentRevisionStateApplied,
			SnapshotArtifactID: "snapshot-drain", SnapshotDigest: digestText,
			RetryCycle: 0, AttemptCount: 1, GenerationID: "generation-2",
			ApplyTimeoutSeconds: 60, DrainTimeoutSeconds: drainTimeoutSeconds,
			CreatedAt: appliedAt, UpdatedAt: appliedAt, AppliedAt: &appliedAt,
		}},
		Pointers: []storage.AgentRevisionPointerRow{{
			AgentID: "edge-1", DesiredRevision: 2, AppliedRevision: 2, LastKnownGoodRevision: 2, UpdatedAt: appliedAt,
		}},
		Attempts: []storage.AgentRevisionAttemptRow{{
			AgentID: "edge-1", Revision: 2, RetryCycle: 0, Attempt: 1, LeaseID: "lease-2",
			State: storage.AgentRevisionAttemptStateApplied, StartedAt: appliedAt.Add(-time.Second),
			DeadlineAt: appliedAt.Add(time.Minute), FinishedAt: &finishedAt,
		}},
		Generations: []storage.AgentGenerationRow{
			{
				AgentID: "edge-1", GenerationID: "generation-1", Revision: 1,
				State: storage.GenerationStateDraining, CreatedAt: appliedAt.Add(-time.Minute), UpdatedAt: appliedAt,
			},
			{
				AgentID: "edge-1", GenerationID: "generation-2", Revision: 2,
				State: storage.GenerationStateActive, CreatedAt: appliedAt, UpdatedAt: appliedAt,
			},
		},
	}
	if err := store.CreateRevisionLedger(context.Background(), ledger); err != nil {
		t.Fatalf("CreateRevisionLedger() error = %v", err)
	}
}

type lifecycleCoordinatorClock struct {
	now time.Time
}

func (c lifecycleCoordinatorClock) Now() time.Time { return c.now }
