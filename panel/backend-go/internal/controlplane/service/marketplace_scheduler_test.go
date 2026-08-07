package service

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/marketplace"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/plugins"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
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

func TestMarketplaceSchedulerPreservesTrustedPreparationContextOnFailure(t *testing.T) {
	now := time.Now().UTC()
	fake := &marketplaceSchedulerFake{sources: []marketplace.Source{{ID: "private", CredentialRef: "vault-ref", RefreshInterval: time.Second, UpdatedAt: now.Add(-time.Hour)}}}
	actor := storage.QuotaActor{UserID: "system-marketplace", SessionID: "scheduler-session", CorrelationID: "scheduler-correlation"}
	prepare := func(ctx context.Context, _ marketplace.Source) (context.Context, error) {
		return storage.WithQuotaActor(ctx, actor), errors.New("credential authorization failed")
	}
	scheduler, err := NewMarketplaceScheduler(fake, prepare, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	scheduler.now = func() time.Time { return now }
	if err := scheduler.RunDue(context.Background()); err == nil {
		t.Fatal("credential preparation failure was not returned")
	}
	if len(fake.auditActors) != 1 || fake.auditActors[0] != actor {
		t.Fatalf("credential failure audit actor = %+v", fake.auditActors)
	}
}

func TestMarketplaceSchedulerAuditsPreparationTimeoutBeforeLease(t *testing.T) {
	now := time.Now().UTC()
	store, err := storage.NewSQLiteStore(t.TempDir(), "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	source, err := marketplace.NewCustomSource("private-timeout", "Private Timeout", "https://example.com/private.git", "main", "vault-ref", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveMarketplaceSource(t.Context(), source); err != nil {
		t.Fatal(err)
	}
	service := NewMarketplaceService(store, nil, plugins.NewValidator(plugins.ValidatorOptions{}), t.TempDir())
	scheduler, err := NewMarketplaceScheduler(service, func(ctx context.Context, _ marketplace.Source) (context.Context, error) {
		<-ctx.Done()
		return ctx, ctx.Err()
	}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	scheduler.now = func() time.Time { return now.Add(time.Hour) }
	scheduler.sourceTimeout = 20 * time.Millisecond
	if err := scheduler.RunDue(t.Context()); err == nil {
		t.Fatal("pre-lease credential preparation timeout was not returned")
	}
	audits, err := store.ListAuditEvents(t.Context(), 20)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, audit := range audits {
		if audit.Action != "marketplace.source.refresh" || audit.TargetID != source.ID || audit.ErrorClass != "timeout" {
			continue
		}
		found = true
		if audit.ActorID != "system.marketplace.scheduler" || audit.SessionID != "service" || audit.CorrelationID == "" {
			t.Fatalf("pre-lease timeout audit lost trusted provenance: %+v", audit)
		}
	}
	if !found {
		t.Fatal("pre-lease timeout did not persist a marketplace failure audit")
	}
}

func TestMarketplaceSchedulerCloseUsesOneDeadlineForBlockedMainLoop(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	fake := &blockingGCSchedulerFake{entered: entered, release: release}
	scheduler, err := NewMarketplaceScheduler(fake, func(ctx context.Context, _ marketplace.Source) (context.Context, error) { return ctx, nil }, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	scheduler.closeTimeout = 30 * time.Millisecond
	scheduler.Start(context.Background())
	<-entered
	started := time.Now()
	if err := scheduler.Close(); err == nil || !strings.Contains(err.Error(), "main loop") {
		t.Fatalf("blocked main close error = %v", err)
	}
	if elapsed := time.Since(started); elapsed > 250*time.Millisecond {
		t.Fatalf("blocked main close elapsed = %v", elapsed)
	}
	close(release)
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
	sources     []marketplace.Source
	refreshed   []string
	audited     []string
	auditActors []storage.QuotaActor
}

func (f *marketplaceSchedulerFake) ListSources(context.Context) ([]marketplace.Source, error) {
	return append([]marketplace.Source(nil), f.sources...), nil
}
func (f *marketplaceSchedulerFake) Refresh(_ context.Context, sourceID string) (marketplace.Snapshot, error) {
	f.refreshed = append(f.refreshed, sourceID)
	return marketplace.Snapshot{}, nil
}
func (f *marketplaceSchedulerFake) AuditSourceFailure(ctx context.Context, _, sourceID, _ string) error {
	f.audited = append(f.audited, sourceID)
	actor, _ := storage.QuotaActorFromContext(ctx)
	f.auditActors = append(f.auditActors, actor)
	return nil
}
func (f *marketplaceSchedulerFake) RunPendingGC(context.Context) error { return nil }

type blockingGCSchedulerFake struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (f *blockingGCSchedulerFake) RunPendingGC(context.Context) error {
	f.once.Do(func() { close(f.entered) })
	<-f.release
	return nil
}
func (f *blockingGCSchedulerFake) ListSources(context.Context) ([]marketplace.Source, error) {
	return nil, nil
}
func (f *blockingGCSchedulerFake) Refresh(context.Context, string) (marketplace.Snapshot, error) {
	return marketplace.Snapshot{}, nil
}
func (f *blockingGCSchedulerFake) AuditSourceFailure(context.Context, string, string, string) error {
	return nil
}
