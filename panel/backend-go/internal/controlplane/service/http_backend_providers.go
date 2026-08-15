package service

import (
	"context"
	"errors"
	"runtime"
	"sort"
	"strings"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/authz"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

// HTTPBackendProvider is the safe control-plane projection used by the rule
// editor. Private endpoints and credentials are generation-owned Agent state
// and never appear here.
type HTTPBackendProvider struct {
	InstanceID   string `json:"instance_id"`
	PluginID     string `json:"plugin_id"`
	ProviderID   string `json:"provider_id"`
	DisplayName  string `json:"display_name"`
	AgentID      string `json:"agent_id"`
	GenerationID string `json:"ready_generation"`
	State        string `json:"state"`
}

type httpBackendProviderProjectionStore interface {
	LoadAgentPluginGenerations(context.Context, string, string) ([]storage.PluginGeneration, error)
	GetPluginInstance(context.Context, string) (storage.PluginInstanceRow, bool, error)
	GetPluginAgentRuntimeStatusFence(context.Context, string, string, string) (storage.PluginAgentRuntimeStatusRow, bool, error)
	ListAgents(context.Context) ([]storage.AgentRow, error)
}

func (s *PluginService) ListHTTPBackendProvidersForActor(ctx context.Context, agentID string, actor authz.Actor) ([]HTTPBackendProvider, error) {
	if !actor.Has(authz.PermissionResourceRead) && !actor.Has(authz.PermissionSystemAdmin) {
		return nil, authz.ErrForbidden
	}
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return nil, ErrInvalidArgument
	}
	var result []HTTPBackendProvider
	err := s.scopedRead(ctx, func(scoped *PluginService) error {
		store, ok := scoped.store.(httpBackendProviderProjectionStore)
		if !ok {
			return errors.New("HTTP backend provider projection is unavailable")
		}
		platform := runtime.GOOS + "-" + runtime.GOARCH
		agents, err := store.ListAgents(ctx)
		if err != nil {
			return err
		}
		for _, agent := range agents {
			if strings.TrimSpace(agent.ID) == agentID && strings.TrimSpace(agent.Platform) != "" {
				platform = strings.TrimSpace(agent.Platform)
				break
			}
		}
		generations, err := store.LoadAgentPluginGenerations(ctx, agentID, platform)
		if err != nil {
			return err
		}
		for _, generation := range generations {
			if generation.Runtime.Kind != pluginsdk.RuntimeRPCService || generation.Runtime.HostScope != pluginsdk.HostScopeAgent ||
				!containsExact(generation.ExtensionPoints, pluginsdk.ExtensionHTTPBackendProvider) ||
				!containsExact(generation.RequiredFeatures, pluginsdk.RPCFeatureHTTPBackendProviderV1) {
				continue
			}
			instance, found, err := store.GetPluginInstance(ctx, generation.InstanceID)
			if err != nil {
				return err
			}
			if !found || !instance.DesiredEnabled || instance.CurrentState != "active" || (!actor.Has(authz.PermissionSystemAdmin) && !actor.Bootstrap && !actor.CanAccessGroup(instance.ResourceGroupID)) {
				continue
			}
			status, found, err := store.GetPluginAgentRuntimeStatusFence(ctx, generation.OperationID, agentID, generation.InstanceID)
			if err != nil {
				return err
			}
			if !found || status.State != "active" || status.GenerationID != generation.ID {
				continue
			}
			for _, descriptor := range generation.HTTPBackendProviders {
				result = append(result, HTTPBackendProvider{
					InstanceID: generation.InstanceID, PluginID: generation.PluginID,
					ProviderID: descriptor.ID, DisplayName: descriptor.DisplayName,
					AgentID: agentID, GenerationID: status.GenerationID, State: status.State,
				})
			}
		}
		return nil
	})
	sort.Slice(result, func(i, j int) bool {
		if result[i].PluginID != result[j].PluginID {
			return result[i].PluginID < result[j].PluginID
		}
		if result[i].InstanceID != result[j].InstanceID {
			return result[i].InstanceID < result[j].InstanceID
		}
		return result[i].ProviderID < result[j].ProviderID
	})
	return result, err
}

func (s *PluginService) HTTPBackendProviderForActor(ctx context.Context, agentID, instanceID, providerID string, actor authz.Actor) (HTTPBackendProvider, error) {
	providers, err := s.ListHTTPBackendProvidersForActor(ctx, agentID, actor)
	if err != nil {
		return HTTPBackendProvider{}, err
	}
	for _, provider := range providers {
		if provider.InstanceID == strings.TrimSpace(instanceID) && provider.ProviderID == strings.TrimSpace(providerID) {
			return provider, nil
		}
	}
	return HTTPBackendProvider{}, ErrRuleNotFound
}

func containsExact(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
