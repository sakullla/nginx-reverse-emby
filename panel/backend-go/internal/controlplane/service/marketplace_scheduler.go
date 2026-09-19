package service

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/marketplace"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
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
	mu            sync.Mutex
	inflight      map[string]struct{}
	workerSlots   chan struct{}
	workers       sync.WaitGroup
	closeTimeout  time.Duration
}

// DefaultMarketplaceRefreshTimeout bounds one marketplace refresh operation.
// Slow Git transfers (e.g. official market clones behind constrained links)
// legitimately take minutes, so the cap is deliberately generous.
const DefaultMarketplaceRefreshTimeout = 30 * time.Minute

// DefaultPluginPackageResolutionTimeout bounds one interactive package
// resolve/download (inspect or install). Refresh can take half an hour; a
// single blob must fail faster so the marketplace download button is not stuck.
const DefaultPluginPackageResolutionTimeout = 5 * time.Minute

func NewMarketplaceScheduler(service marketplaceSchedulerService, prepare func(context.Context, marketplace.Source) (context.Context, error), interval time.Duration) (*MarketplaceScheduler, error) {
	return NewMarketplaceSchedulerWithSourceTimeout(service, prepare, interval, DefaultMarketplaceRefreshTimeout)
}

func NewMarketplaceSchedulerWithSourceTimeout(service marketplaceSchedulerService, prepare func(context.Context, marketplace.Source) (context.Context, error), interval, sourceTimeout time.Duration) (*MarketplaceScheduler, error) {
	if service == nil || prepare == nil {
		return nil, errors.New("marketplace scheduler service and trusted context preparer are required")
	}
	if interval <= 0 {
		interval = 30 * time.Second
	}
	if sourceTimeout <= 0 {
		sourceTimeout = DefaultMarketplaceRefreshTimeout
	}
	return &MarketplaceScheduler{service: service, prepare: prepare, now: func() time.Time { return time.Now().UTC() }, interval: interval, sourceTimeout: sourceTimeout, done: make(chan struct{}), inflight: make(map[string]struct{}), workerSlots: make(chan struct{}, 4), closeTimeout: 5 * time.Second}, nil
}

func (s *MarketplaceScheduler) beginSource(sourceID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.inflight[sourceID]; exists {
		return false
	}
	select {
	case s.workerSlots <- struct{}{}:
		s.inflight[sourceID] = struct{}{}
		s.workers.Add(1)
		return true
	default:
		return false
	}
}

func (s *MarketplaceScheduler) finishSource(sourceID string) {
	s.mu.Lock()
	delete(s.inflight, sourceID)
	s.mu.Unlock()
	<-s.workerSlots
	s.workers.Done()
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
		refreshInterval := marketplace.EffectiveRefreshInterval(source)
		if source.Deleting || refreshInterval <= 0 {
			continue
		}
		if source.LastResult == "running" {
			if source.LeaseExpiresAt.After(now) {
				continue
			}
		} else {
			initialOfficialRefresh := source.Kind == marketplace.SourceKindOfficial && source.CurrentSnapshot == "" && source.LastResult == ""
			if !initialOfficialRefresh {
				baseline := source.LastCompletedAt
				if baseline.IsZero() {
					baseline = source.UpdatedAt
				}
				if !baseline.IsZero() && baseline.Add(refreshInterval).After(now) {
					continue
				}
			}
		}
		if !s.beginSource(source.ID) {
			continue
		}
		sourceCtx, cancelSource := context.WithTimeout(ctx, s.sourceTimeout)
		schedulerActor := storage.QuotaActor{
			UserID:        "system.marketplace.scheduler",
			SessionID:     "service",
			CorrelationID: fmt.Sprintf("marketplace-scheduler:%s:%d", source.ID, s.now().UnixNano()),
			Bootstrap:     true,
		}
		auditBase := storage.WithQuotaActor(sourceCtx, schedulerActor)
		type sourceResult struct {
			err        error
			errorClass string
			auditCtx   context.Context
		}
		refreshResult := make(chan sourceResult, 1)
		workerCtx, identity := marketplace.WithRefreshIdentityCapture(auditBase)
		go func(source marketplace.Source) {
			defer s.finishSource(source.ID)
			refreshCtx, prepareErr := s.prepare(workerCtx, source)
			if prepareErr != nil {
				refreshResult <- sourceResult{err: prepareErr, errorClass: "credential_authorization", auditCtx: refreshCtx}
				return
			}
			_, refreshErr := s.service.Refresh(refreshCtx, source.ID)
			refreshResult <- sourceResult{err: refreshErr, auditCtx: refreshCtx}
		}(source)
		select {
		case completed := <-refreshResult:
			cancelSource()
			if completed.errorClass != "" {
				auditBase := completed.auditCtx
				if auditBase == nil {
					auditBase = ctx
				}
				auditCtx, auditCancel := context.WithTimeout(context.WithoutCancel(auditBase), 5*time.Second)
				auditErr := s.service.AuditSourceFailure(auditCtx, "refresh", source.ID, completed.errorClass)
				auditCancel()
				result = errors.Join(result, completed.err, auditErr)
			} else if completed.err != nil && !errors.Is(completed.err, marketplace.ErrRefreshLeaseHeld) {
				result = errors.Join(result, completed.err)
			}
		case <-sourceCtx.Done():
			cancelSource()
			timeoutErr := fmt.Errorf("marketplace source %s refresh timed out: %w", source.ID, sourceCtx.Err())
			auditCtx, auditCancel := context.WithTimeout(context.WithoutCancel(auditBase), 5*time.Second)
			var auditErr error
			refreshIdentity := identity.Load()
			if refreshIdentity.OperationID == "" || refreshIdentity.LeaseToken == "" {
				auditErr = s.service.AuditSourceFailure(auditCtx, "refresh", source.ID, "timeout")
			} else if abandoner, ok := s.service.(interface {
				AbandonRefresh(context.Context, string, marketplace.RefreshIdentity, string) error
			}); ok {
				auditErr = abandoner.AbandonRefresh(auditCtx, source.ID, refreshIdentity, "timeout")
			} else {
				auditErr = s.service.AuditSourceFailure(auditCtx, "refresh", source.ID, "timeout")
			}
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
	deadline := time.NewTimer(s.closeTimeout)
	defer deadline.Stop()
	select {
	case <-s.done:
	case <-deadline.C:
		return errors.New("marketplace scheduler main loop did not stop before close deadline")
	}
	workersClosed := make(chan struct{})
	go func() {
		s.workers.Wait()
		close(workersClosed)
	}()
	select {
	case <-workersClosed:
		return nil
	case <-deadline.C:
		return errors.New("marketplace scheduler workers did not stop before close deadline")
	}
}
