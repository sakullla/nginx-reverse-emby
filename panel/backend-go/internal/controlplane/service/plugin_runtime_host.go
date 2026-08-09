package service

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/pluginhost"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

type PluginRuntimeRepository interface {
	StagePluginRuntime(context.Context, storage.PluginRuntimeInstanceRow) error
	PromotePluginRuntime(context.Context, string, string, int, string) error
	FailPluginRuntimeCandidate(context.Context, string, string, error) error
	GetPluginRuntime(context.Context, string) (storage.PluginRuntimeInstanceRow, bool, error)
	UpdatePluginRuntimeHealth(context.Context, string, string, string, int, int, bool, string) error
}

type PluginRuntimeHost struct {
	host       *pluginhost.Host
	repository PluginRuntimeRepository
	locks      sync.Map
}

func NewPluginRuntimeHost(host *pluginhost.Host, repository PluginRuntimeRepository) (*PluginRuntimeHost, error) {
	if host == nil || repository == nil {
		return nil, errors.New("plugin runtime host and repository are required")
	}
	service := &PluginRuntimeHost{host: host, repository: repository}
	host.SetStatusObserver(func(status pluginhost.RuntimeStatus) {
		_ = repository.UpdatePluginRuntimeHealth(context.Background(), status.InstanceID, status.Generation, status.State, status.PID, status.RestartCount, status.CircuitOpen, status.LastError)
	})
	return service, nil
}

func (s *PluginRuntimeHost) Activate(ctx context.Context, candidate pluginhost.Candidate) (*pluginhost.Instance, error) {
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
		return nil, fmt.Errorf("publish promoted plugin runtime: %w", err)
	}
	return instance, nil
}

func (s *PluginRuntimeHost) Stop(ctx context.Context, instanceID string) error {
	return s.host.Stop(ctx, instanceID)
}
func (s *PluginRuntimeHost) Close(ctx context.Context) error { return s.host.Close(ctx) }
