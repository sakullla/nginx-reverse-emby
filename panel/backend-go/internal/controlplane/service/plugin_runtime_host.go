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

type PluginRuntimeHost struct {
	host         *pluginhost.Host
	repository   PluginRuntimeRepository
	locks        sync.Map
	stateMu      sync.Mutex
	closeMu      sync.Mutex
	ctx          context.Context
	cancel       context.CancelFunc
	operations   sync.WaitGroup
	closed       bool
	closeTargets map[string]string
}

func NewPluginRuntimeHost(host *pluginhost.Host, repository PluginRuntimeRepository) (*PluginRuntimeHost, error) {
	if host == nil || repository == nil {
		return nil, errors.New("plugin runtime host and repository are required")
	}
	ctx, cancel := context.WithCancel(context.Background())
	service := &PluginRuntimeHost{host: host, repository: repository, ctx: ctx, cancel: cancel, closeTargets: make(map[string]string)}
	host.SetStatusObserver(func(status pluginhost.RuntimeStatus) error {
		return repository.UpdatePluginRuntimeHealth(context.Background(), status.InstanceID, status.Generation, status.State, status.PID, status.RestartCount, status.CircuitOpen, status.LastError)
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
		stopErr := instance.Stop(cleanupCtx)
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
	stopErr := instance.Stop(cleanupCtx)
	terminalErr := retryRuntimeStopPersistence(cleanupCtx, s.repository, candidate.InstanceID, candidate.Identity.Generation)
	if terminalErr != nil {
		fallbackErr := s.repository.UpdatePluginRuntimeHealth(cleanupCtx, candidate.InstanceID, candidate.Identity.Generation, "failed", 0, 0, false, safeRuntimeError(errors.Join(publishErr, terminalErr)))
		terminalErr = errors.Join(fmt.Errorf("terminalize promoted plugin runtime: %w", terminalErr), fallbackErr)
	}
	return errors.Join(publishErr, stopErr, terminalErr)
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
	stopErr := s.host.Stop(operationCtx, instanceID)
	if !found || row.ActiveGeneration == "" {
		return stopErr
	}
	persistCtx := operationCtx
	cancelPersist := func() {}
	if operationCtx.Err() != nil {
		persistCtx, cancelPersist = context.WithTimeout(context.Background(), 5*time.Second)
	}
	defer cancelPersist()
	persistErr := retryRuntimeStopPersistence(persistCtx, s.repository, instanceID, row.ActiveGeneration)
	return errors.Join(stopErr, persistErr, s.host.StatusPersistenceError(instanceID))
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
	for instanceID, generation := range s.host.ActiveGenerations() {
		s.closeTargets[instanceID] = generation
	}
	hostErr := s.host.Close(ctx)
	errs := []error{hostErr}
	for instanceID, generation := range s.closeTargets {
		if err := retryRuntimeStopPersistence(ctx, s.repository, instanceID, generation); err != nil {
			errs = append(errs, fmt.Errorf("persist closed plugin runtime %s generation %s: %w", instanceID, generation, err))
			continue
		}
		delete(s.closeTargets, instanceID)
		if observerErr := s.host.StatusPersistenceError(instanceID); observerErr != nil {
			errs = append(errs, observerErr)
		}
	}
	return errors.Join(errs...)
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
