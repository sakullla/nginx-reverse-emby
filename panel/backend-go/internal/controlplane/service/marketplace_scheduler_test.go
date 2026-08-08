package service

import (
	"context"
	"errors"
	"path/filepath"
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

func TestMarketplaceSchedulerRefreshesLegacyOfficialSourceInitiallyAndWhenDue(t *testing.T) {
	now := time.Unix(2_000, 0).UTC()
	official := marketplace.OfficialSource()
	if official.RefreshInterval != marketplace.OfficialRefreshInterval || official.RefreshInterval <= 0 {
		t.Fatalf("new official refresh interval = %v", official.RefreshInterval)
	}
	official.RefreshInterval = 0 // Simulate an official source persisted before the default existed.
	official.UpdatedAt = now     // Lazy creation must not delay the initial catalog population.
	fake := &marketplaceSchedulerFake{sources: []marketplace.Source{
		official,
		{ID: "custom-disabled", Kind: marketplace.SourceKindCustom, RefreshInterval: 0},
		{ID: "custom-disabled-negative", Kind: marketplace.SourceKindCustom, RefreshInterval: -time.Second},
	}}
	scheduler, err := NewMarketplaceScheduler(fake, func(ctx context.Context, _ marketplace.Source) (context.Context, error) { return ctx, nil }, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	current := now
	scheduler.now = func() time.Time { return current }
	if err := scheduler.RunDue(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(fake.refreshed) != 1 || fake.refreshed[0] != marketplace.OfficialSourceID {
		t.Fatalf("initial official scheduler refreshes = %v", fake.refreshed)
	}
	fake.sources[0].LastCompletedAt = now
	fake.sources[0].LastResult = "succeeded"
	fake.sources[0].CurrentSnapshot = "snapshot-1"
	current = now.Add(marketplace.OfficialRefreshInterval - time.Nanosecond)
	if err := scheduler.RunDue(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(fake.refreshed) != 1 {
		t.Fatalf("official source refreshed before due: %v", fake.refreshed)
	}
	current = now.Add(marketplace.OfficialRefreshInterval)
	if err := scheduler.RunDue(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(fake.refreshed) != 2 || fake.refreshed[1] != marketplace.OfficialSourceID {
		t.Fatalf("subsequent due official scheduler refreshes = %v", fake.refreshed)
	}
}

func TestMarketplaceSchedulerFailureStartsNextRefreshCycle(t *testing.T) {
	refreshInterval := time.Minute
	fetcher := &schedulerLifecycleFetcher{
		failure: errors.New("upstream unavailable"),
	}
	scheduler, store, sourceID := newMarketplaceSchedulerLifecycleHarness(t, fetcher, refreshInterval)
	succeeded := marketplaceSchedulerPersistedSource(t, store, sourceID)
	if succeeded.LastResult != "succeeded" || succeeded.LastCompletedAt.IsZero() {
		t.Fatalf("seed refresh source = %+v", succeeded)
	}
	current := succeeded.LastCompletedAt.Add(refreshInterval)
	scheduler.now = func() time.Time { return current }
	if err := scheduler.RunDue(t.Context()); err == nil {
		t.Fatal("due refresh failure was not returned")
	}
	failed := marketplaceSchedulerPersistedSource(t, store, sourceID)
	if failed.LastResult != "failed" || !failed.LastCompletedAt.After(succeeded.LastCompletedAt) || fetcher.callCount() != 1 {
		t.Fatalf("failed refresh source=%+v calls=%d", failed, fetcher.callCount())
	}

	current = failed.LastCompletedAt.Add(refreshInterval - time.Nanosecond)
	if err := scheduler.RunDue(t.Context()); err != nil {
		t.Fatal(err)
	}
	if fetcher.callCount() != 1 {
		t.Fatalf("failed refresh retried before next cycle: %d", fetcher.callCount())
	}
	current = failed.LastCompletedAt.Add(refreshInterval)
	if err := scheduler.RunDue(t.Context()); err == nil {
		t.Fatal("next-cycle refresh failure was not returned")
	}
	if fetcher.callCount() != 2 {
		t.Fatalf("failed refresh was not retried in next cycle: %d", fetcher.callCount())
	}
}

func TestMarketplaceSchedulerRejectedInvalidCompletionDoesNotRetryNextTick(t *testing.T) {
	refreshInterval := time.Minute
	fetcher := &schedulerLifecycleFetcher{failure: errors.New("upstream unavailable")}
	scheduler, store, sourceID := newMarketplaceSchedulerLifecycleHarness(t, fetcher, refreshInterval)
	seeded := marketplaceSchedulerPersistedSource(t, store, sourceID)
	startedAt := seeded.LastCompletedAt.Add(refreshInterval)
	operation := marketplace.RefreshOperation{ID: "scheduler-invalid-completion", SourceID: sourceID, Status: "running", StartedAt: startedAt, LeaseToken: "scheduler-invalid-completion-lease", LeaseExpiresAt: startedAt.Add(refreshInterval)}
	if err := store.AcquireRefreshLease(t.Context(), operation); err != nil {
		t.Fatal(err)
	}
	failure := operation
	failure.Status, failure.ErrorClass, failure.Error = "failed", "fetch", "offline"
	if err := store.SaveRefreshOperation(t.Context(), failure); err == nil {
		t.Fatal("nil completion time was accepted")
	}
	backdated := startedAt.Add(-time.Nanosecond)
	failure.FinishedAt = &backdated
	if err := store.SaveRefreshOperation(t.Context(), failure); err == nil {
		t.Fatal("backdated completion time was accepted")
	}
	scheduler.now = func() time.Time { return startedAt.Add(time.Second) }
	if err := scheduler.RunDue(t.Context()); err != nil {
		t.Fatal(err)
	}
	if fetcher.callCount() != 0 {
		t.Fatalf("scheduler retried rejected invalid completion on next tick: %d", fetcher.callCount())
	}
	persisted := marketplaceSchedulerPersistedSource(t, store, sourceID)
	if persisted.LastResult != "running" || !persisted.LeaseExpiresAt.Equal(operation.LeaseExpiresAt) || !persisted.LastCompletedAt.Equal(seeded.LastCompletedAt) {
		t.Fatalf("invalid completion changed scheduler baseline: before=%+v after=%+v", seeded, persisted)
	}
}

func TestMarketplaceSchedulerTimeoutStartsNextRefreshCycle(t *testing.T) {
	refreshInterval := time.Minute
	started := make(chan struct{})
	release := make(chan struct{})
	fetcher := &schedulerLifecycleFetcher{
		failure:   errors.New("upstream unavailable"),
		blockCall: 1,
		started:   started,
		release:   release,
	}
	scheduler, store, sourceID := newMarketplaceSchedulerLifecycleHarness(t, fetcher, refreshInterval)
	succeeded := marketplaceSchedulerPersistedSource(t, store, sourceID)
	if succeeded.LastResult != "succeeded" || succeeded.LastCompletedAt.IsZero() {
		t.Fatalf("seed refresh source = %+v", succeeded)
	}
	current := succeeded.LastCompletedAt.Add(refreshInterval)
	scheduler.now = func() time.Time { return current }
	scheduler.sourceTimeout = 20 * time.Millisecond
	if err := scheduler.RunDue(t.Context()); err == nil {
		t.Fatal("due refresh timeout was not returned")
	}
	select {
	case <-started:
	default:
		t.Fatal("timed refresh never reached the fetcher")
	}
	timedOut := marketplaceSchedulerPersistedSource(t, store, sourceID)
	if timedOut.LastResult != "failed" || !timedOut.LastCompletedAt.After(succeeded.LastCompletedAt) || fetcher.callCount() != 1 {
		t.Fatalf("timed refresh source=%+v calls=%d", timedOut, fetcher.callCount())
	}
	close(release)
	workersDone := make(chan struct{})
	go func() {
		scheduler.workers.Wait()
		close(workersDone)
	}()
	select {
	case <-workersDone:
	case <-time.After(2 * time.Second):
		t.Fatal("timed-out refresh worker did not stop")
	}

	current = timedOut.LastCompletedAt.Add(refreshInterval - time.Nanosecond)
	if err := scheduler.RunDue(t.Context()); err != nil {
		t.Fatal(err)
	}
	if fetcher.callCount() != 1 {
		t.Fatalf("timed refresh retried before next cycle: %d", fetcher.callCount())
	}
	scheduler.sourceTimeout = 2 * time.Second
	current = timedOut.LastCompletedAt.Add(refreshInterval)
	if err := scheduler.RunDue(t.Context()); err == nil {
		t.Fatal("next-cycle refresh failure was not returned")
	}
	if fetcher.callCount() != 2 {
		t.Fatalf("timed refresh was not retried in next cycle: %d", fetcher.callCount())
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
	select {
	case <-scheduler.done:
	case <-time.After(time.Second):
		t.Fatal("scheduler main loop did not quiesce after blocked GC was released")
	}
	if err := scheduler.Close(); err != nil {
		t.Fatalf("retry Close() after quiescence = %v", err)
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

type schedulerLifecycleService struct {
	*MarketplaceService
	store    *storage.SQLiteStore
	sourceID string
}

func (s *schedulerLifecycleService) ListSources(ctx context.Context) ([]marketplace.Source, error) {
	source, ok, err := s.store.GetMarketplaceSource(ctx, s.sourceID)
	if err != nil || !ok {
		return nil, err
	}
	return []marketplace.Source{source}, nil
}

type schedulerLifecycleFetcher struct {
	mu          sync.Mutex
	calls       int
	failure     error
	blockCall   int
	started     chan struct{}
	release     chan struct{}
	startedOnce sync.Once
}

func (f *schedulerLifecycleFetcher) Fetch(_ context.Context, _ marketplace.Source, destination string) (string, error) {
	f.mu.Lock()
	f.calls++
	call := f.calls
	block := call == f.blockCall
	f.mu.Unlock()
	if block {
		f.startedOnce.Do(func() { close(f.started) })
		<-f.release
	}
	return "", f.failure
}

func (f *schedulerLifecycleFetcher) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func newMarketplaceSchedulerLifecycleHarness(t *testing.T, fetcher marketplace.Fetcher, refreshInterval time.Duration) (*MarketplaceScheduler, *storage.SQLiteStore, string) {
	t.Helper()
	dataRoot := t.TempDir()
	store, err := storage.NewSQLiteStore(dataRoot, "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	source, err := marketplace.NewCustomSource("scheduler-cycle", "Scheduler Cycle", "https://example.com/plugins.git", "main", "", refreshInterval)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveMarketplaceSource(t.Context(), source); err != nil {
		t.Fatal(err)
	}
	seededAt := time.Now().UTC()
	seed := marketplace.Snapshot{ID: "seed", SourceID: source.ID, Commit: "seed", Path: filepath.Join(dataRoot, "marketplace", "snapshots", source.ID, "seed"), ValidatedAt: seededAt}
	if err := store.PromoteSnapshot(t.Context(), source, seed); err != nil {
		t.Fatal(err)
	}
	validator := plugins.NewValidator(plugins.ValidatorOptions{})
	cacheRoot := filepath.Join(dataRoot, "plugins", "packages")
	cleanupMarketplaceCache(t, cacheRoot)
	cache, err := marketplace.NewVerifiedCache(cacheRoot, validator, store)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := marketplace.NewManager(filepath.Join(dataRoot, "marketplace"), fetcher, validator, cache, store)
	if err != nil {
		t.Fatal(err)
	}
	service := &schedulerLifecycleService{MarketplaceService: NewMarketplaceService(store, manager, validator, cacheRoot), store: store, sourceID: source.ID}
	scheduler, err := NewMarketplaceScheduler(service, func(ctx context.Context, _ marketplace.Source) (context.Context, error) { return ctx, nil }, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return scheduler, store, source.ID
}

func marketplaceSchedulerPersistedSource(t *testing.T, store *storage.SQLiteStore, sourceID string) marketplace.Source {
	t.Helper()
	source, ok, err := store.GetMarketplaceSource(t.Context(), sourceID)
	if err != nil || !ok {
		t.Fatalf("marketplace source %q = %+v, %v, %v", sourceID, source, ok, err)
	}
	return source
}
