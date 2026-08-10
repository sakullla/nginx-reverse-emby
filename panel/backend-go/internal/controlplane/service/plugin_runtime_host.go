package service

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/pluginhost"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

type PluginRuntimeRepository interface {
	StagePluginRuntime(context.Context, storage.PluginRuntimeInstanceRow) error
	PromotePluginRuntime(context.Context, string, string, int, string) error
	FailPluginRuntimeCandidate(context.Context, string, string, error) error
	GetPluginRuntime(context.Context, string) (storage.PluginRuntimeInstanceRow, bool, error)
	UpdatePluginRuntimeHealth(context.Context, string, string, string, int, int, bool, string) error
	StopPluginRuntime(context.Context, string, string) error
}

const pluginRuntimeHealthWriteTimeout = 5 * time.Second

type PluginRuntimeHost struct {
	host          *pluginhost.Host
	repository    PluginRuntimeRepository
	locks         sync.Map
	stateMu       sync.Mutex
	closeMu       sync.Mutex
	ctx           context.Context
	cancel        context.CancelFunc
	operations    sync.WaitGroup
	closed        bool
	closeTargets  map[string]string
	closeFailures map[string]error
	closeResults  map[string]pluginhost.TerminalResult
	pendingMu     sync.Mutex
	pending       map[*pluginhost.Instance]pendingRuntimeCleanup
}

type pendingRuntimeCleanup struct {
	candidate pluginhost.Candidate
	promoted  bool
}

func NewPluginRuntimeHost(host *pluginhost.Host, repository PluginRuntimeRepository) (*PluginRuntimeHost, error) {
	if host == nil || repository == nil {
		return nil, errors.New("plugin runtime host and repository are required")
	}
	ctx, cancel := context.WithCancel(context.Background())
	service := &PluginRuntimeHost{host: host, repository: repository, ctx: ctx, cancel: cancel, closeTargets: make(map[string]string), closeFailures: make(map[string]error), closeResults: make(map[string]pluginhost.TerminalResult), pending: make(map[*pluginhost.Instance]pendingRuntimeCleanup)}
	host.SetStatusObserver(func(status pluginhost.RuntimeStatus) error {
		observerCtx, cancel := context.WithTimeout(service.ctx, pluginRuntimeHealthWriteTimeout)
		defer cancel()
		return repository.UpdatePluginRuntimeHealth(observerCtx, status.InstanceID, status.Generation, status.State, status.PID, status.RestartCount, status.CircuitOpen, status.LastError)
	})
	return service, nil
}

func (s *PluginRuntimeHost) Activate(ctx context.Context, candidate pluginhost.Candidate) (*pluginhost.Instance, error) {
	operationCtx, done, err := s.beginOperation(ctx)
	if err != nil {
		return nil, err
	}
	defer done()
	lockValue, _ := s.locks.LoadOrStore(candidate.InstanceID, &sync.Mutex{})
	lock := lockValue.(*sync.Mutex)
	lock.Lock()
	defer lock.Unlock()
	if err := operationCtx.Err(); err != nil {
		return nil, errors.Join(errors.New("plugin runtime activation canceled"), err)
	}
	row := storage.PluginRuntimeInstanceRow{InstanceID: candidate.InstanceID, PluginID: candidate.Identity.PluginID, HostScope: "control-plane", CandidateGeneration: candidate.Identity.Generation, CandidatePackageDigest: candidate.Identity.PackageDigest, CandidateArtifactDigest: candidate.Artifact.SHA256}
	if err := s.repository.StagePluginRuntime(operationCtx, row); err != nil {
		return nil, fmt.Errorf("stage plugin runtime state: %w", err)
	}
	instance, err := s.host.PrepareCandidate(operationCtx, candidate)
	if err != nil {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if reportErr := s.repository.FailPluginRuntimeCandidate(cleanupCtx, candidate.InstanceID, candidate.Identity.Generation, err); reportErr != nil {
			return nil, errors.Join(err, reportErr)
		}
		return nil, err
	}
	state, _ := instance.Status()
	sandbox := instance.SandboxProvider
	if err := s.repository.PromotePluginRuntime(operationCtx, candidate.InstanceID, candidate.Identity.Generation, instance.PID, sandbox); err != nil {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		stopErr := s.stopPreparedCandidate(cleanupCtx, instance, candidate, false)
		reportErr := s.repository.FailPluginRuntimeCandidate(cleanupCtx, candidate.InstanceID, candidate.Identity.Generation, err)
		return nil, errors.Join(fmt.Errorf("promote plugin runtime state after %s: %w", state, err), stopErr, reportErr)
	}
	if err := operationCtx.Err(); err != nil {
		return nil, s.rollbackPromotedCandidate(instance, candidate, fmt.Errorf("publish canceled promoted plugin runtime: %w", err))
	}
	if err := s.host.Publish(instance); err != nil {
		return nil, s.rollbackPromotedCandidate(instance, candidate, fmt.Errorf("publish promoted plugin runtime: %w", err))
	}
	return instance, nil
}

func (s *PluginRuntimeHost) beginOperation(ctx context.Context) (context.Context, func(), error) {
	if ctx == nil {
		return nil, nil, errors.New("plugin runtime operation context is required")
	}
	s.stateMu.Lock()
	if s.closed {
		s.stateMu.Unlock()
		return nil, nil, errors.New("plugin runtime service is closed")
	}
	s.operations.Add(1)
	serviceCtx := s.ctx
	s.stateMu.Unlock()
	operationCtx, cancel := context.WithCancel(ctx)
	stopServiceCancellation := context.AfterFunc(serviceCtx, cancel)
	done := func() {
		stopServiceCancellation()
		cancel()
		s.operations.Done()
	}
	return operationCtx, done, nil
}

func (s *PluginRuntimeHost) rollbackPromotedCandidate(instance *pluginhost.Instance, candidate pluginhost.Candidate, publishErr error) error {
	cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stopErr := s.stopPreparedCandidate(cleanupCtx, instance, candidate, true)
	if !instance.Terminated() {
		healthErr := s.repository.UpdatePluginRuntimeHealth(cleanupCtx, candidate.InstanceID, candidate.Identity.Generation, "failed", instance.ProcessID(), 0, false, safeRuntimeError(errors.Join(publishErr, stopErr)))
		return errors.Join(publishErr, stopErr, healthErr)
	}
	terminalErr := retryRuntimeStopPersistence(cleanupCtx, s.repository, candidate.InstanceID, candidate.Identity.Generation)
	if terminalErr != nil {
		fallbackErr := s.repository.UpdatePluginRuntimeHealth(cleanupCtx, candidate.InstanceID, candidate.Identity.Generation, "failed", 0, 0, false, safeRuntimeError(errors.Join(publishErr, terminalErr)))
		terminalErr = errors.Join(fmt.Errorf("terminalize promoted plugin runtime: %w", terminalErr), fallbackErr)
	}
	return errors.Join(publishErr, stopErr, terminalErr)
}

func (s *PluginRuntimeHost) stopPreparedCandidate(ctx context.Context, instance *pluginhost.Instance, candidate pluginhost.Candidate, promoted bool) error {
	stopErr := s.host.StopCandidate(ctx, instance)
	s.pendingMu.Lock()
	defer s.pendingMu.Unlock()
	if !instance.Terminated() {
		s.pending[instance] = pendingRuntimeCleanup{candidate: candidate, promoted: promoted}
	} else {
		delete(s.pending, instance)
	}
	return stopErr
}

func safeRuntimeError(err error) string {
	if err == nil {
		return ""
	}
	value := err.Error()
	if len(value) > 256 {
		return value[:256]
	}
	return value
}

func (s *PluginRuntimeHost) Stop(ctx context.Context, instanceID string) error {
	operationCtx, done, err := s.beginOperation(ctx)
	if err != nil {
		return err
	}
	defer done()
	lockValue, _ := s.locks.LoadOrStore(instanceID, &sync.Mutex{})
	lock := lockValue.(*sync.Mutex)
	lock.Lock()
	defer lock.Unlock()
	if err := operationCtx.Err(); err != nil {
		return errors.Join(errors.New("plugin runtime stop canceled"), err)
	}
	row, found, readErr := s.repository.GetPluginRuntime(operationCtx, instanceID)
	if readErr != nil {
		return fmt.Errorf("read plugin runtime before stop: %w", readErr)
	}
	results, stopErr := s.host.StopWithResults(operationCtx, instanceID)
	if !found || row.ActiveGeneration == "" {
		return stopErr
	}
	result, resultFound := publishedTerminalResult(results, instanceID, row.ActiveGeneration)
	if !resultFound {
		return errors.Join(stopErr, fmt.Errorf("plugin runtime %s generation %s has no ownership-fenced terminal result", instanceID, row.ActiveGeneration))
	}
	if !result.Terminated {
		return errors.Join(stopErr, result.CleanupError, fmt.Errorf("plugin runtime %s generation %s remains owned after failed termination", instanceID, row.ActiveGeneration))
	}
	persistCtx := operationCtx
	cancelPersist := func() {}
	if operationCtx.Err() != nil {
		persistCtx, cancelPersist = context.WithTimeout(context.Background(), 5*time.Second)
	}
	defer cancelPersist()
	if result.PendingExitError != nil {
		failure := errors.Join(result.PendingExitError, s.host.StatusPersistenceError(instanceID))
		persistErr := retryRuntimeFailurePersistence(persistCtx, s.repository, instanceID, row.ActiveGeneration, failure)
		return errors.Join(stopErr, fmt.Errorf("plugin runtime %s generation %s stopped with unacknowledged exit: %w", instanceID, row.ActiveGeneration, failure), persistErr)
	}
	persistErr := retryRuntimeStopPersistence(persistCtx, s.repository, instanceID, row.ActiveGeneration)
	return errors.Join(stopErr, persistErr, s.host.StatusPersistenceError(instanceID))
}

func publishedTerminalResult(results []pluginhost.TerminalResult, instanceID, generation string) (pluginhost.TerminalResult, bool) {
	for _, result := range results {
		if result.Published && result.InstanceID == instanceID && result.Generation == generation {
			return result, true
		}
	}
	return pluginhost.TerminalResult{}, false
}

func (s *PluginRuntimeHost) Close(ctx context.Context) error {
	s.closeMu.Lock()
	defer s.closeMu.Unlock()
	s.stateMu.Lock()
	if !s.closed {
		s.closed = true
		s.cancel()
	}
	s.stateMu.Unlock()
	s.operations.Wait()
	results, hostErr := s.host.CloseWithResults(ctx)
	for _, result := range results {
		if !result.Published || result.InstanceID == "" || result.Generation == "" {
			continue
		}
		s.closeTargets[result.InstanceID] = result.Generation
		s.closeResults[result.InstanceID] = result
		if result.PendingExitError != nil {
			s.closeFailures[result.InstanceID] = errors.Join(result.PendingExitError, s.host.StatusPersistenceError(result.InstanceID))
		}
	}
	errs := []error{hostErr}
	s.pendingMu.Lock()
	for instance, pending := range s.pending {
		if !instance.Terminated() {
			errs = append(errs, fmt.Errorf("plugin runtime candidate %s generation %s remains owned after failed termination", pending.candidate.InstanceID, pending.candidate.Identity.Generation))
			continue
		}
		if pending.promoted {
			if err := retryRuntimeStopPersistence(ctx, s.repository, pending.candidate.InstanceID, pending.candidate.Identity.Generation); err != nil {
				errs = append(errs, fmt.Errorf("persist stopped rollback candidate %s generation %s: %w", pending.candidate.InstanceID, pending.candidate.Identity.Generation, err))
				continue
			}
		}
		delete(s.pending, instance)
	}
	s.pendingMu.Unlock()
	for instanceID, generation := range s.closeTargets {
		result, resultFound := s.closeResults[instanceID]
		if resultFound && !result.Terminated {
			errs = append(errs, fmt.Errorf("plugin runtime %s generation %s remains owned after failed termination", instanceID, generation))
			continue
		}
		failure := s.closeFailures[instanceID]
		if failure != nil {
			if err := retryRuntimeFailurePersistence(ctx, s.repository, instanceID, generation, failure); err != nil {
				errs = append(errs, fmt.Errorf("persist failed close for plugin runtime %s generation %s: %w", instanceID, generation, err))
			} else {
				delete(s.closeTargets, instanceID)
				delete(s.closeFailures, instanceID)
				delete(s.closeResults, instanceID)
			}
			errs = append(errs, fmt.Errorf("plugin runtime %s generation %s closed with unacknowledged exit: %w", instanceID, generation, failure))
			continue
		}
		if !resultFound {
			errs = append(errs, fmt.Errorf("plugin runtime %s generation %s has no ownership-fenced terminal result", instanceID, generation))
			continue
		}
		if err := retryRuntimeStopPersistence(ctx, s.repository, instanceID, generation); err != nil {
			errs = append(errs, fmt.Errorf("persist closed plugin runtime %s generation %s: %w", instanceID, generation, err))
			continue
		}
		delete(s.closeTargets, instanceID)
		delete(s.closeResults, instanceID)
		if observerErr := s.host.StatusPersistenceError(instanceID); observerErr != nil {
			if !errors.Is(observerErr, context.Canceled) || s.ctx.Err() == nil {
				errs = append(errs, observerErr)
			}
		}
	}
	return errors.Join(errs...)
}

func retryRuntimeFailurePersistence(ctx context.Context, repository PluginRuntimeRepository, instanceID, generation string, failure error) error {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		lastErr = repository.UpdatePluginRuntimeHealth(ctx, instanceID, generation, "failed", 0, 0, false, safeRuntimeError(failure))
		if lastErr == nil {
			return nil
		}
		if attempt < 2 {
			timer := time.NewTimer(time.Duration(attempt+1) * 10 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return errors.Join(lastErr, ctx.Err())
			case <-timer.C:
			}
		}
	}
	return lastErr
}

func retryRuntimeStopPersistence(ctx context.Context, repository PluginRuntimeRepository, instanceID, generation string) error {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if lastErr = repository.StopPluginRuntime(ctx, instanceID, generation); lastErr == nil {
			return nil
		}
		if attempt < 2 {
			timer := time.NewTimer(time.Duration(attempt+1) * 10 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return errors.Join(lastErr, ctx.Err())
			case <-timer.C:
			}
		}
	}
	return lastErr
}
