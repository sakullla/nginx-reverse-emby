package service

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/marketplace"
)

type marketplaceSchedulerService interface {
	ListSources(context.Context) ([]marketplace.Source, error)
	Refresh(context.Context, string) (marketplace.Snapshot, error)
	AuditSourceFailure(context.Context, string, string, string) error
	RunPendingGC(context.Context) error
}

type MarketplaceScheduler struct {
	service       marketplaceSchedulerService
	prepare       func(context.Context, marketplace.Source) (context.Context, error)
	now           func() time.Time
	interval      time.Duration
	sourceTimeout time.Duration
	cancel        context.CancelFunc
	done          chan struct{}
	once          sync.Once
}

func NewMarketplaceScheduler(service marketplaceSchedulerService, prepare func(context.Context, marketplace.Source) (context.Context, error), interval time.Duration) (*MarketplaceScheduler, error) {
	if service == nil || prepare == nil {
		return nil, errors.New("marketplace scheduler service and trusted context preparer are required")
	}
	if interval <= 0 {
		interval = 30 * time.Second
	}
	return &MarketplaceScheduler{service: service, prepare: prepare, now: func() time.Time { return time.Now().UTC() }, interval: interval, sourceTimeout: 2 * time.Minute, done: make(chan struct{})}, nil
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
	result := s.service.RunPendingGC(ctx)
	sources, err := s.service.ListSources(ctx)
	if err != nil {
		return errors.Join(result, err)
	}
	now := s.now()
	for _, source := range sources {
		if source.Deleting || source.RefreshInterval <= 0 {
			continue
		}
		if source.LastResult == "running" {
			if source.LeaseExpiresAt.After(now) {
				continue
			}
		} else {
			baseline := source.LastCompletedAt
			if baseline.IsZero() {
				baseline = source.UpdatedAt
			}
			if !baseline.IsZero() && baseline.Add(source.RefreshInterval).After(now) {
				continue
			}
		}
		sourceCtx, cancelSource := context.WithTimeout(ctx, s.sourceTimeout)
		type sourceResult struct {
			err        error
			errorClass string
		}
		refreshResult := make(chan sourceResult, 1)
		go func(source marketplace.Source) {
			refreshCtx, prepareErr := s.prepare(sourceCtx, source)
			if prepareErr != nil {
				refreshResult <- sourceResult{err: prepareErr, errorClass: "credential_authorization"}
				return
			}
			_, refreshErr := s.service.Refresh(refreshCtx, source.ID)
			refreshResult <- sourceResult{err: refreshErr}
		}(source)
		select {
		case completed := <-refreshResult:
			cancelSource()
			if completed.errorClass != "" {
				auditCtx, auditCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
				auditErr := s.service.AuditSourceFailure(auditCtx, "refresh", source.ID, completed.errorClass)
				auditCancel()
				result = errors.Join(result, completed.err, auditErr)
			} else if completed.err != nil && !errors.Is(completed.err, marketplace.ErrRefreshLeaseHeld) {
				result = errors.Join(result, completed.err)
			}
		case <-sourceCtx.Done():
			cancelSource()
			timeoutErr := fmt.Errorf("marketplace source %s refresh timed out: %w", source.ID, sourceCtx.Err())
			auditCtx, auditCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			auditErr := s.service.AuditSourceFailure(auditCtx, "refresh", source.ID, "timeout")
			auditCancel()
			result = errors.Join(result, timeoutErr, auditErr)
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
