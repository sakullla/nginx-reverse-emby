package service

import (
	"context"
	"encoding/json"
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
// and never appear here. Kind discriminates static plugin providers from
// published-port offers; offers submit as ordinary URL backends.
type HTTPBackendProvider struct {
	Kind         string `json:"kind,omitempty"`
	InstanceID   string `json:"instance_id"`
	PluginID     string `json:"plugin_id"`
	ProviderID   string `json:"provider_id,omitempty"`
	DisplayName  string `json:"display_name"`
	AgentID      string `json:"agent_id"`
	GenerationID string `json:"ready_generation,omitempty"`
	State        string `json:"state"`
	ResourceID   string `json:"resource_id,omitempty"`
	Port         int    `json:"port,omitempty"`
}

type httpBackendProviderProjectionStore interface {
	LoadAgentPluginGenerations(context.Context, string, string) ([]storage.PluginGeneration, error)
	GetPluginInstance(context.Context, string) (storage.PluginInstanceRow, bool, error)
	GetPluginAgentRuntimeStatusFence(context.Context, string, string, string) (storage.PluginAgentRuntimeStatusRow, bool, error)
	ListAgents(context.Context) ([]storage.AgentRow, error)
}

type httpBackendOfferStateStore interface {
	GetPluginRuntimeState(context.Context, string, string) ([]byte, bool, error)
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
			if !found || !instance.DesiredEnabled || (instance.CurrentState != "active" && instance.CurrentState != "degraded") || (!actor.Has(authz.PermissionSystemAdmin) && !actor.Bootstrap && !actor.CanAccessGroup(instance.ResourceGroupID)) {
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
					Kind:       pluginsdk.HTTPBackendCatalogKindPluginProvider,
					InstanceID: generation.InstanceID, PluginID: generation.PluginID,
					ProviderID: descriptor.ID, DisplayName: descriptor.DisplayName,
					AgentID: agentID, GenerationID: status.GenerationID, State: status.State,
				})
			}
		}
		offers, err := listHTTPBackendOffersForActor(ctx, scoped, agentID, actor)
		if err != nil {
			return err
		}
		result = append(result, offers...)
		return nil
	})
	sort.Slice(result, func(i, j int) bool {
		if result[i].Kind != result[j].Kind {
			return result[i].Kind < result[j].Kind
		}
		if result[i].PluginID != result[j].PluginID {
			return result[i].PluginID < result[j].PluginID
		}
		if result[i].InstanceID != result[j].InstanceID {
			return result[i].InstanceID < result[j].InstanceID
		}
		if result[i].ResourceID != result[j].ResourceID {
			return result[i].ResourceID < result[j].ResourceID
		}
		if result[i].Port != result[j].Port {
			return result[i].Port < result[j].Port
		}
		return result[i].ProviderID < result[j].ProviderID
	})
	return result, err
}

func listHTTPBackendOffersForActor(ctx context.Context, scoped *PluginService, agentID string, actor authz.Actor) ([]HTTPBackendProvider, error) {
	stateStore, ok := scoped.store.(httpBackendOfferStateStore)
	if !ok {
		return nil, nil
	}
	installed, err := scoped.store.ListInstalledPlugins(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]HTTPBackendProvider, 0)
	for _, plugin := range installed {
		instances, err := scoped.store.ListPluginInstances(ctx, plugin.PluginID)
		if err != nil {
			return nil, err
		}
		for _, instance := range instances {
			if !instance.DesiredEnabled || (instance.CurrentState != "active" && instance.CurrentState != "degraded") || (!actor.Has(authz.PermissionSystemAdmin) && !actor.Bootstrap && !actor.CanAccessGroup(instance.ResourceGroupID)) {
				continue
			}
			value, found, err := stateStore.GetPluginRuntimeState(ctx, instance.ID, pluginsdk.HTTPBackendOfferCatalogKey)
			if err != nil {
				return nil, err
			}
			if !found || len(value) == 0 {
				continue
			}
			var payload pluginsdk.HTTPBackendOfferReplaceRequest
			if json.Unmarshal(value, &payload) != nil || payload.Validate() != nil {
				continue
			}
			for _, offer := range payload.Offers {
				if offer.AgentID != agentID {
					continue
				}
				state := "unavailable"
				if offer.Available {
					state = "active"
				}
				result = append(result, HTTPBackendProvider{
					Kind:        pluginsdk.HTTPBackendCatalogKindPublishedPort,
					InstanceID:  instance.ID,
					PluginID:    instance.PluginID,
					DisplayName: offer.DisplayName,
					AgentID:     offer.AgentID,
					State:       state,
					ResourceID:  offer.ResourceID,
					Port:        offer.Port,
				})
			}
		}
	}
	return result, nil
}

func (s *PluginService) HTTPBackendProviderForActor(ctx context.Context, agentID, instanceID, providerID string, actor authz.Actor) (HTTPBackendProvider, error) {
	providers, err := s.ListHTTPBackendProvidersForActor(ctx, agentID, actor)
	if err != nil {
		return HTTPBackendProvider{}, err
	}
	for _, provider := range providers {
		if provider.Kind == pluginsdk.HTTPBackendCatalogKindPublishedPort {
			continue
		}
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
