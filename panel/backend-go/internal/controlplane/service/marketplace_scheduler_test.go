package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/marketplace"
)

func TestMarketplaceSchedulerRunsPersistentlyDueSourcesAndAuditsPrivatePreparationFailure(t *testing.T) {
	now := time.Unix(1_000, 0).UTC()
	fake := &marketplaceSchedulerFake{sources: []marketplace.Source{
		{ID: "due", RefreshInterval: time.Minute, UpdatedAt: now.Add(-2 * time.Minute)},
		{ID: "future", RefreshInterval: time.Minute, UpdatedAt: now},
		{ID: "private", CredentialRef: "revoked", RefreshInterval: time.Minute, UpdatedAt: now.Add(-2 * time.Minute)},
		{ID: "crashed", RefreshInterval: time.Hour, UpdatedAt: now, LastResult: "running", LeaseExpiresAt: now.Add(-time.Second)},
	}}
	prepare := func(ctx context.Context, source marketplace.Source) (context.Context, error) {
		if source.CredentialRef != "" {
			return ctx, errors.New("credential revoked")
		}
		return ctx, nil
	}
	scheduler, err := NewMarketplaceScheduler(fake, prepare, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	scheduler.now = func() time.Time { return now }
	if err := scheduler.RunDue(context.Background()); err == nil {
		t.Fatal("private preparation failure was not reported")
	}
	if len(fake.refreshed) != 2 || fake.refreshed[0] != "due" || fake.refreshed[1] != "crashed" || len(fake.audited) != 1 || fake.audited[0] != "private" {
		t.Fatalf("scheduler results refreshed=%v audited=%v", fake.refreshed, fake.audited)
	}
	// A fresh scheduler over the same durable timestamps recovers due work after restart.
	restarted, _ := NewMarketplaceScheduler(fake, prepare, time.Hour)
	restarted.now = func() time.Time { return now.Add(2 * time.Minute) }
	if err := restarted.RunDue(context.Background()); err == nil {
		t.Fatal("restarted scheduler did not retain private-source failure")
	}
	if len(fake.refreshed) != 5 || fake.refreshed[2] != "due" || fake.refreshed[3] != "future" || fake.refreshed[4] != "crashed" {
		t.Fatalf("restart due-source recovery = %v", fake.refreshed)
	}
}

func TestMarketplaceSchedulerTimeoutIsolatesHungSourceAndPropagatesAuditFailure(t *testing.T) {
	now := time.Now().UTC()
	auditFailure := errors.New("audit persistence failed")
	fake := &isolatingSchedulerFake{sources: []marketplace.Source{{ID: "hung", RefreshInterval: time.Second, UpdatedAt: now.Add(-time.Hour)}, {ID: "healthy", RefreshInterval: time.Second, UpdatedAt: now.Add(-time.Hour)}}, auditErr: auditFailure}
	scheduler, err := NewMarketplaceScheduler(fake, func(ctx context.Context, _ marketplace.Source) (context.Context, error) { return ctx, nil }, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	scheduler.now = func() time.Time { return now }
	scheduler.sourceTimeout = 20 * time.Millisecond
	err = scheduler.RunDue(context.Background())
	if err == nil || !errors.Is(err, auditFailure) {
		t.Fatalf("timeout audit failure = %v", err)
	}
	fake.mu.Lock()
	if len(fake.refreshed) != 2 || fake.refreshed[0] != "hung" || fake.refreshed[1] != "healthy" {
		t.Fatalf("hung source blocked later refresh: %v", fake.refreshed)
	}
	fake.mu.Unlock()
	// The timed-out goroutine remains registered until it actually exits, so
	// repeated ticks cannot accumulate more workers for the same source.
	if err := scheduler.RunDue(context.Background()); err != nil {
		t.Fatalf("second tick should skip the registered hung source: %v", err)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	hungCalls := 0
	for _, sourceID := range fake.refreshed {
		if sourceID == "hung" {
			hungCalls++
		}
	}
	if hungCalls != 1 || len(scheduler.workerSlots) > cap(scheduler.workerSlots) {
		t.Fatalf("hung source calls=%d active=%d capacity=%d", hungCalls, len(scheduler.workerSlots), cap(scheduler.workerSlots))
	}
}

type isolatingSchedulerFake struct {
	mu        sync.Mutex
	sources   []marketplace.Source
	refreshed []string
	auditErr  error
}

func (f *isolatingSchedulerFake) ListSources(context.Context) ([]marketplace.Source, error) {
	return f.sources, nil
}
func (f *isolatingSchedulerFake) Refresh(_ context.Context, sourceID string) (marketplace.Snapshot, error) {
	f.mu.Lock()
	f.refreshed = append(f.refreshed, sourceID)
	f.mu.Unlock()
	if sourceID == "hung" {
		select {}
	}
	return marketplace.Snapshot{}, nil
}
func (f *isolatingSchedulerFake) AuditSourceFailure(context.Context, string, string, string) error {
	return f.auditErr
}
func (f *isolatingSchedulerFake) RunPendingGC(context.Context) error { return nil }

type marketplaceSchedulerFake struct {
	sources   []marketplace.Source
	refreshed []string
	audited   []string
}

func (f *marketplaceSchedulerFake) ListSources(context.Context) ([]marketplace.Source, error) {
	return append([]marketplace.Source(nil), f.sources...), nil
}
func (f *marketplaceSchedulerFake) Refresh(_ context.Context, sourceID string) (marketplace.Snapshot, error) {
	f.refreshed = append(f.refreshed, sourceID)
	return marketplace.Snapshot{}, nil
}
func (f *marketplaceSchedulerFake) AuditSourceFailure(_ context.Context, _, sourceID, _ string) error {
	f.audited = append(f.audited, sourceID)
	return nil
}
func (f *marketplaceSchedulerFake) RunPendingGC(context.Context) error { return nil }
