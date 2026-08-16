//go:build integration

package storage

import (
	"context"
	"testing"
	"time"
)

func TestIntegrationIngestTrafficCursorDeltaFirstReplayAndBootReset(t *testing.T) {
	t.Parallel()
	store := newTrafficTestStore(t, true)
	ctx := context.Background()
	bucket := time.Date(2026, 5, 3, 8, 0, 0, 0, time.UTC)
	seed := AgentTrafficRawCursorRow{
		AgentID: "edge-1", ScopeType: "host_total", RXBytes: 100, TXBytes: 200,
		BootID: "boot-a", ObservedAt: "2026-05-03T08:00:00Z",
	}
	first, err := store.IngestTrafficCursorDelta(ctx, seed, bucket)
	if err != nil || first.FoundPrevious || first.DeltaRXBytes != 0 || first.CounterReset {
		t.Fatalf("host seed ingest = %+v err=%v", first, err)
	}
	next := seed
	next.RXBytes = 150
	next.TXBytes = 275
	next.ObservedAt = "2026-05-03T08:01:00Z"
	delta, err := store.IngestTrafficCursorDelta(ctx, next, bucket)
	if err != nil || !delta.FoundPrevious || delta.CounterReset || delta.DeltaRXBytes != 50 || delta.DeltaTXBytes != 75 {
		t.Fatalf("incremental ingest = %+v err=%v", delta, err)
	}
	replay, err := store.IngestTrafficCursorDelta(ctx, next, bucket)
	if err != nil || !replay.FoundPrevious || replay.DeltaRXBytes != 0 || replay.DeltaTXBytes != 0 || replay.CounterReset {
		t.Fatalf("replay ingest = %+v err=%v", replay, err)
	}
	reset := next
	reset.RXBytes = 10
	reset.TXBytes = 20
	reset.BootID = "boot-b"
	reset.ObservedAt = "2026-05-03T08:02:00Z"
	booted, err := store.IngestTrafficCursorDelta(ctx, reset, bucket)
	if err != nil || !booted.FoundPrevious || !booted.CounterReset || booted.DeltaRXBytes != 10 || booted.DeltaTXBytes != 20 {
		t.Fatalf("boot reset ingest = %+v err=%v", booted, err)
	}
	event := &AgentTrafficEventRow{EventType: "counter_reset", Message: "boot"}
	rollback := reset
	rollback.RXBytes = 1
	rollback.TXBytes = 2
	rollback.ObservedAt = "2026-05-03T08:03:00Z"
	rolled, err := store.IngestTrafficCursorDeltaWithEvent(ctx, rollback, bucket, event)
	if err != nil || !rolled.CounterReset {
		t.Fatalf("event ingest = %+v err=%v", rolled, err)
	}
}
