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

func TestCloudflareProxiedRecordPreservesAutomaticTTL(t *testing.T) {
	var patches atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/zones":
			_, _ = w.Write([]byte(`{"success":true,"result":[{"id":"zone-1","name":"example.com"}],"result_info":{"total_pages":1}}`))
		case request.Method == http.MethodGet && request.URL.Path == "/zones/zone-1/dns_records":
			content := "203.0.113.10"
			if patches.Load() > 0 {
				content = "203.0.113.20"
			}
			_, _ = w.Write([]byte(`{"success":true,"result":[{"id":"record-1","type":"A","name":"media.example.com","content":"` + content + `","ttl":1,"proxied":true}],"result_info":{"total_pages":1}}`))
		case request.Method == http.MethodPatch && request.URL.Path == "/zones/zone-1/dns_records/record-1":
			patches.Add(1)
			var body struct {
				Content string `json:"content"`
				TTL     int    `json:"ttl"`
			}
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Errorf("decode PATCH body: %v", err)
			}
			if body.Content != "203.0.113.20" || body.TTL != 1 {
				t.Errorf("PATCH body = %+v", body)
			}
			_, _ = w.Write([]byte(`{"success":true,"result":{"id":"record-1"}}`))
		default:
			http.Error(w, "unexpected request", http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)

	client := newHTTPCloudflareClient(server.URL, time.Second)
	outcome, err := client.EnsureRecord(t.Context(), "token", "media.example.com", "A", "203.0.113.20", 120)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Action != "updated" || patches.Load() != 1 {
		t.Fatalf("proxied reconcile outcome = %+v, patches = %d", outcome, patches.Load())
	}
	outcome, err = client.EnsureRecord(t.Context(), "token", "media.example.com", "A", "203.0.113.20", 120)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Action != "unchanged" || patches.Load() != 1 {
		t.Fatalf("proxied repeat outcome = %+v, patches = %d", outcome, patches.Load())
	}
}

func TestExpiredDrainReportIsForcedAndIdempotent(t *testing.T) {
	store, err := storage.NewSQLiteStore(t.TempDir(), "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	appliedAt := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	seedAppliedDrain(t, store, appliedAt, 5)
	now := appliedAt.Add(2 * time.Hour)
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
		Status: storage.AgentRevisionDrainStateDrained,
	})
	if err != nil {
		t.Fatalf("ReportRemoteRevision(expired drain) error = %v", err)
	}
	_, err = api.ReportRemoteRevision(t.Context(), "edge-1", RemoteRevisionReport{
		AgentID: "edge-1", Revision: 2, RetryCycle: 0, Attempt: 1,
		LeaseID: "lease-2", GenerationID: "generation-1", Status: storage.AgentRevisionDrainStateDrained,
	})
	if err != nil {
		t.Fatalf("ReportRemoteRevision(replayed drain) error = %v", err)
	}
	generation, found, err := store.GetCoordinatorGeneration(t.Context(), "edge-1", "generation-1")
	if err != nil || !found || generation.State != storage.AgentRevisionDrainStateForced || !generation.Forced || generation.ForceReason != "timeout" {
		t.Fatalf("forced generation = %+v, found=%v, error=%v", generation, found, err)
	}
	events, err := store.ListRevisionEvents(t.Context(), storage.RevisionEventQuery{OperationID: "operation-drain", Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	terminalEvents := 0
	for _, event := range events {
		if event.EventType == "generation_forced" {
			terminalEvents++
		}
	}
	if terminalEvents != 1 {
		t.Fatalf("generation_forced event count = %d, want 1; events=%+v", terminalEvents, events)
	}
}

func TestForcedDrainDominatesLaterNaturalPredecessorCompletion(t *testing.T) {
	store, err := storage.NewSQLiteStore(t.TempDir(), "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	appliedAt := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	seedAppliedDrain(t, store, appliedAt, 300, storage.AgentGenerationRow{
		AgentID: "edge-1", GenerationID: "generation-0", Revision: 0,
		State: storage.GenerationStateDraining, CreatedAt: appliedAt.Add(-2 * time.Minute), UpdatedAt: appliedAt,
	})
	clock := lifecycleCoordinatorClock{now: appliedAt.Add(time.Second)}
	coord, err := coordinator.New(store, coordinator.Options{Clock: clock})
	if err != nil {
		t.Fatal(err)
	}
	api := NewRevisionAPI(store, coord)
	api.now = clock.Now
	report := RemoteRevisionReport{
		AgentID: "edge-1", Revision: 2, RetryCycle: 0, Attempt: 1, LeaseID: "lease-2",
	}
	report.GenerationID = "generation-1"
	report.Status = storage.AgentRevisionDrainStateForced
	report.Forced = true
	report.ForceReason = "generation_limit"
	if _, err := api.ReportRemoteRevision(t.Context(), "edge-1", report); err != nil {
		t.Fatal(err)
	}
	report.GenerationID = "generation-0"
	report.Status = storage.AgentRevisionDrainStateDrained
	report.Forced = false
	report.ForceReason = ""
	if _, err := api.ReportRemoteRevision(t.Context(), "edge-1", report); err != nil {
		t.Fatal(err)
	}
	revision, found, err := store.GetCoordinatorRevision(t.Context(), "edge-1", 2)
	if err != nil || !found || revision.DrainState != storage.AgentRevisionDrainStateForced {
		t.Fatalf("aggregate drain state = %+v, found=%t, error=%v", revision, found, err)
	}
}

func TestAppliedReportIsIdempotentAfterCommittedResponseLoss(t *testing.T) {
	store, err := storage.NewSQLiteStore(t.TempDir(), "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	startedAt := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	seedStartedApply(t, store, startedAt)
	clock := lifecycleCoordinatorClock{now: startedAt.Add(10 * time.Second)}
	coord, err := coordinator.New(store, coordinator.Options{Clock: clock})
	if err != nil {
		t.Fatal(err)
	}
	api := NewRevisionAPI(store, coord)
	api.now = clock.Now
	report := RemoteRevisionReport{
		AgentID: "edge-1", Revision: 2, RetryCycle: 0, Attempt: 1,
		LeaseID: "lease-2", GenerationID: "generation-2", Status: storage.AgentRevisionStateApplied,
	}
	if _, err := api.ReportRemoteRevision(t.Context(), "edge-1", report); err != nil {
		t.Fatalf("first applied report error = %v", err)
	}
	if _, err := api.ReportRemoteRevision(t.Context(), "edge-1", report); err != nil {
		t.Fatalf("replayed applied report error = %v", err)
	}
	events, err := store.ListRevisionEvents(t.Context(), storage.RevisionEventQuery{OperationID: "operation-apply", Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	appliedEvents := 0
	for _, event := range events {
		if event.EventType == "revision_applied" {
			appliedEvents++
		}
	}
	if appliedEvents != 1 {
		t.Fatalf("revision_applied event count = %d, want 1; events=%+v", appliedEvents, events)
	}
}

func TestRevisionReconcilerExpiresAttemptWithoutAgentPull(t *testing.T) {
	store, err := storage.NewSQLiteStore(t.TempDir(), "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	startedAt := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	seedStartedApply(t, store, startedAt)
	clock := lifecycleCoordinatorClock{now: startedAt.Add(2 * time.Minute)}
	coord, err := coordinator.New(store, coordinator.Options{Clock: clock})
	if err != nil {
		t.Fatal(err)
	}
	api := NewRevisionAPI(store, coord)
	api.now = clock.Now
	reconciler := NewRevisionReconciler(api, nil)
	reconciler.reconcileOnce(t.Context())

	row, found, err := store.GetCoordinatorRevision(t.Context(), "edge-1", 2)
	if err != nil {
		t.Fatal(err)
	}
	if !found || row.State == storage.AgentRevisionStateApplying {
		t.Fatalf("orphaned applying revision was not reconciled: %+v", row)
	}
}

func TestRevisionReconcilerForcesExpiredDrainWithoutAgentPull(t *testing.T) {
	store, err := storage.NewSQLiteStore(t.TempDir(), "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	appliedAt := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	seedAppliedDrain(t, store, appliedAt, 5)
	clock := lifecycleCoordinatorClock{now: appliedAt.Add(time.Hour)}
	coord, err := coordinator.New(store, coordinator.Options{Clock: clock})
	if err != nil {
		t.Fatal(err)
	}
	api := NewRevisionAPI(store, coord)
	api.now = clock.Now
	reconciler := NewRevisionReconciler(api, nil)
	reconciler.reconcileOnce(t.Context())

	generation, found, err := store.GetCoordinatorGeneration(t.Context(), "edge-1", "generation-1")
	if err != nil {
		t.Fatal(err)
	}
	if !found || generation.State != storage.AgentRevisionDrainStateForced || generation.ForceReason != "timeout" {
		t.Fatalf("expired drain was not reconciled: %+v", generation)
	}
	if _, err := api.ReportRemoteRevision(t.Context(), "edge-1", RemoteRevisionReport{
		AgentID: "edge-1", Revision: 2, RetryCycle: 0, Attempt: 1,
		LeaseID: "lease-2", GenerationID: "generation-1", Status: storage.AgentRevisionDrainStateDrained,
	}); err != nil {
		t.Fatalf("late terminal replay after server reconciliation error = %v", err)
	}
}

func seedAppliedDrain(t *testing.T, store *storage.GormStore, appliedAt time.Time, drainTimeoutSeconds int, extraPredecessors ...storage.AgentGenerationRow) {
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
	ledger.Generations = append(ledger.Generations, extraPredecessors...)
	if err := store.CreateRevisionLedger(context.Background(), ledger); err != nil {
		t.Fatalf("CreateRevisionLedger() error = %v", err)
	}
}

func seedStartedApply(t *testing.T, store *storage.GormStore, startedAt time.Time) {
	t.Helper()
	snapshotPayload, err := json.Marshal(storage.Snapshot{Revision: 2})
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(snapshotPayload)
	digestText := hex.EncodeToString(digest[:])
	ledger := storage.RevisionLedgerWrite{
		Operation: storage.OperationRow{
			ID: "operation-apply", Kind: "test", Status: storage.OperationStatusApplying,
			PrimaryAgentID: "edge-1", CreatedAt: startedAt, UpdatedAt: startedAt,
		},
		Artifacts: []storage.GenerationArtifactRow{{
			ID: "snapshot-apply", Kind: "agent_snapshot", SHA256: digestText,
			Payload: snapshotPayload, SizeBytes: int64(len(snapshotPayload)), CreatedAt: startedAt,
		}},
		Revisions: []storage.AgentRevisionRow{{
			AgentID: "edge-1", Revision: 2, OperationID: "operation-apply", State: storage.AgentRevisionStateApplying,
			SnapshotArtifactID: "snapshot-apply", SnapshotDigest: digestText,
			RetryCycle: 0, AttemptCount: 1, GenerationID: "generation-2",
			ApplyTimeoutSeconds: 60, DrainTimeoutSeconds: 30,
			CreatedAt: startedAt, UpdatedAt: startedAt,
		}},
		Pointers: []storage.AgentRevisionPointerRow{{
			AgentID: "edge-1", DesiredRevision: 2, AppliedRevision: 1, LastKnownGoodRevision: 1, UpdatedAt: startedAt,
		}},
		Attempts: []storage.AgentRevisionAttemptRow{{
			AgentID: "edge-1", Revision: 2, RetryCycle: 0, Attempt: 1, LeaseID: "lease-2",
			State: storage.AgentRevisionAttemptStateStarted, StartedAt: startedAt, DeadlineAt: startedAt.Add(time.Minute),
		}},
		Generations: []storage.AgentGenerationRow{{
			AgentID: "edge-1", GenerationID: "generation-1", Revision: 1,
			State: storage.GenerationStateActive, CreatedAt: startedAt.Add(-time.Minute), UpdatedAt: startedAt,
		}},
	}
	if err := store.CreateRevisionLedger(context.Background(), ledger); err != nil {
		t.Fatalf("CreateRevisionLedger() error = %v", err)
	}
}

type lifecycleCoordinatorClock struct {
	now time.Time
}

func (c lifecycleCoordinatorClock) Now() time.Time { return c.now }
