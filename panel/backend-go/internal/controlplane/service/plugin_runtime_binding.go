package service

import (
	"context"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

// BindLocalPluginRuntimeGeneration is an in-process Host boundary, not a
// plugin RPC. The local revision worker supplies its approved started lease.
func (s *PluginService) BindLocalPluginRuntimeGeneration(ctx context.Context, agentID string, input RemoteRevisionStart) error {
	if err := requireRemoteAgentIdentity(agentID, input.AgentID); err != nil {
		return err
	}
	store, ok := s.store.(interface {
		BindAgentRevisionRuntime(context.Context, storage.CoordinatorRuntimeBindingRequest) error
	})
	if !ok {
		return errPluginHostUnavailable
	}
	return store.BindAgentRevisionRuntime(ctx, storage.CoordinatorRuntimeBindingRequest{
		Lease:        storage.CoordinatorLease{AgentID: agentID, Revision: input.Revision, RetryCycle: input.RetryCycle, Attempt: input.Attempt, LeaseID: input.LeaseID},
		GenerationID: input.GenerationID, RuntimeGenerationID: input.RuntimeGenerationID, RuntimeSnapshotHash: input.RuntimeSnapshotHash,
	})
}
