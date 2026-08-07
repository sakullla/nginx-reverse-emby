package service

import (
	"context"
	"errors"
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
