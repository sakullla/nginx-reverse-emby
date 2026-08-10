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
	lifecycle    sync.RWMutex
	closed       bool
	closeTargets map[string]string
}

func NewPluginRuntimeHost(host *pluginhost.Host, repository PluginRuntimeRepository) (*PluginRuntimeHost, error) {
	if host == nil || repository == nil {
		return nil, errors.New("plugin runtime host and repository are required")
	}
	service := &PluginRuntimeHost{host: host, repository: repository, closeTargets: make(map[string]string)}
	host.SetStatusObserver(func(status pluginhost.RuntimeStatus) error {
		return repository.UpdatePluginRuntimeHealth(context.Background(), status.InstanceID, status.Generation, status.State, status.PID, status.RestartCount, status.CircuitOpen, status.LastError)
	})
	return service, nil
}

func (s *PluginRuntimeHost) Activate(ctx context.Context, candidate pluginhost.Candidate) (*pluginhost.Instance, error) {
	s.lifecycle.RLock()
	defer s.lifecycle.RUnlock()
	if s.closed {
		return nil, errors.New("plugin runtime service is closed")
	}
	lockValue, _ := s.locks.LoadOrStore(candidate.InstanceID, &sync.Mutex{})
	lock := lockValue.(*sync.Mutex)
	lock.Lock()
	defer lock.Unlock()
	row := storage.PluginRuntimeInstanceRow{InstanceID: candidate.InstanceID, PluginID: candidate.Identity.PluginID, HostScope: "control-plane", CandidateGeneration: candidate.Identity.Generation, CandidatePackageDigest: candidate.Identity.PackageDigest, CandidateArtifactDigest: candidate.Artifact.SHA256}
	if err := s.repository.StagePluginRuntime(ctx, row); err != nil {
		return nil, fmt.Errorf("stage plugin runtime state: %w", err)
	}
	instance, err := s.host.PrepareCandidate(ctx, candidate)
	if err != nil {
		if reportErr := s.repository.FailPluginRuntimeCandidate(ctx, candidate.InstanceID, candidate.Identity.Generation, err); reportErr != nil {
			return nil, errors.Join(err, reportErr)
		}
		return nil, err
	}
	state, _ := instance.Status()
	sandbox := instance.SandboxProvider
	if err := s.repository.PromotePluginRuntime(ctx, candidate.InstanceID, candidate.Identity.Generation, instance.PID, sandbox); err != nil {
		_ = instance.Stop(context.Background())
		_ = s.repository.FailPluginRuntimeCandidate(context.Background(), candidate.InstanceID, candidate.Identity.Generation, err)
		return nil, fmt.Errorf("promote plugin runtime state after %s: %w", state, err)
	}
	if err := s.host.Publish(instance); err != nil {
		publishErr := fmt.Errorf("publish promoted plugin runtime: %w", err)
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		stopErr := instance.Stop(cleanupCtx)
		terminalErr := retryRuntimeStopPersistence(cleanupCtx, s.repository, candidate.InstanceID, candidate.Identity.Generation)
		if terminalErr != nil {
			fallbackErr := s.repository.UpdatePluginRuntimeHealth(cleanupCtx, candidate.InstanceID, candidate.Identity.Generation, "failed", 0, 0, false, safeRuntimeError(errors.Join(publishErr, terminalErr)))
			terminalErr = errors.Join(fmt.Errorf("terminalize promoted plugin runtime: %w", terminalErr), fallbackErr)
		}
		return nil, errors.Join(publishErr, stopErr, terminalErr)
	}
	return instance, nil
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
	s.lifecycle.RLock()
	defer s.lifecycle.RUnlock()
	if s.closed {
		return errors.New("plugin runtime service is closed")
	}
	lockValue, _ := s.locks.LoadOrStore(instanceID, &sync.Mutex{})
	lock := lockValue.(*sync.Mutex)
	lock.Lock()
	defer lock.Unlock()
	row, found, readErr := s.repository.GetPluginRuntime(ctx, instanceID)
	if readErr != nil {
		return fmt.Errorf("read plugin runtime before stop: %w", readErr)
	}
	stopErr := s.host.Stop(ctx, instanceID)
	if !found || row.ActiveGeneration == "" {
		return stopErr
	}
	persistErr := retryRuntimeStopPersistence(ctx, s.repository, instanceID, row.ActiveGeneration)
	return errors.Join(stopErr, persistErr, s.host.StatusPersistenceError(instanceID))
}
func (s *PluginRuntimeHost) Close(ctx context.Context) error {
	s.lifecycle.Lock()
	defer s.lifecycle.Unlock()
	s.closed = true
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
