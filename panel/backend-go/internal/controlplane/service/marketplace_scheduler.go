package service

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/marketplace"
)

type marketplaceSchedulerService interface {
	ListSources(context.Context) ([]marketplace.Source, error)
	Refresh(context.Context, string) (marketplace.Snapshot, error)
	AuditSourceFailure(context.Context, string, string, string) error
}

type MarketplaceScheduler struct {
	service  marketplaceSchedulerService
	prepare  func(context.Context, marketplace.Source) (context.Context, error)
	now      func() time.Time
	interval time.Duration
	cancel   context.CancelFunc
	done     chan struct{}
	once     sync.Once
}

func NewMarketplaceScheduler(service marketplaceSchedulerService, prepare func(context.Context, marketplace.Source) (context.Context, error), interval time.Duration) (*MarketplaceScheduler, error) {
	if service == nil || prepare == nil {
		return nil, errors.New("marketplace scheduler service and trusted context preparer are required")
	}
	if interval <= 0 {
		interval = 30 * time.Second
	}
	return &MarketplaceScheduler{service: service, prepare: prepare, now: func() time.Time { return time.Now().UTC() }, interval: interval, done: make(chan struct{})}, nil
}

func (s *MarketplaceScheduler) Start(parent context.Context) {
	s.once.Do(func() {
		ctx, cancel := context.WithCancel(parent)
		s.cancel = cancel
		go s.run(ctx)
	})
}

func (s *MarketplaceScheduler) run(ctx context.Context) {
	defer close(s.done)
	_ = s.RunDue(ctx)
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = s.RunDue(ctx)
		}
	}
}

func (s *MarketplaceScheduler) RunDue(ctx context.Context) error {
	sources, err := s.service.ListSources(ctx)
	if err != nil {
		return err
	}
	now := s.now()
	var result error
	for _, source := range sources {
		if source.RefreshInterval <= 0 || (!source.UpdatedAt.IsZero() && source.UpdatedAt.Add(source.RefreshInterval).After(now)) {
			continue
		}
		refreshCtx, prepareErr := s.prepare(ctx, source)
		if prepareErr != nil {
			_ = s.service.AuditSourceFailure(refreshCtx, "refresh", source.ID, "credential_authorization")
			result = errors.Join(result, prepareErr)
			continue
		}
		if _, refreshErr := s.service.Refresh(refreshCtx, source.ID); refreshErr != nil && !errors.Is(refreshErr, marketplace.ErrRefreshLeaseHeld) {
			result = errors.Join(result, refreshErr)
		}
	}
	return result
}

func (s *MarketplaceScheduler) Close() error {
	if s.cancel == nil {
		return nil
	}
	s.cancel()
	<-s.done
	return nil
}
