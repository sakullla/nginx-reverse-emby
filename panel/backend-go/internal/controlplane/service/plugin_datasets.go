package service

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/pluginhost"
	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

func (manager *PluginCapabilityManager) SetDatasetService(service *DatasetService) {
	if manager != nil {
		manager.datasets = service
	}
}

func (manager *PluginCapabilityManager) pluginHostDataset(ctx context.Context, candidate pluginhost.Candidate, call pluginsdk.HostRuntimeCall) (any, error) {
	if manager.datasets == nil {
		return nil, errPluginHostUnavailable
	}
	permission := string(pluginsdk.CapabilityDatasetManage)
	if !pluginCandidateHasGrant(candidate, permission) {
		return nil, errPluginHostDenied
	}
	declared := false
	for _, scope := range candidate.Identity.Scopes {
		if scope == permission {
			declared = true
		}
	}
	if !declared {
		return nil, errPluginHostDenied
	}
	authority := DatasetAuthorization{ActorID: "plugin/" + candidate.Identity.PluginID, ResourceGroupID: candidate.ResourceGroupID, Manage: true}
	var identity struct {
		SourceID string `json:"source_id"`
	}
	if json.Unmarshal(call.Payload, &identity) != nil || pluginsdk.ValidatePolicyIdentity(identity.SourceID) != nil {
		return nil, errPluginHostInvalid
	}
	// Empty selector explicitly means the granted resource group; otherwise the
	// exact source ID must appear in the Host's administrator grant projection.
	selectors := candidate.GrantSelectors[permission]
	allowed := len(selectors) == 0
	for _, selector := range selectors {
		if selector == "" || selector == identity.SourceID {
			allowed = true
		}
	}
	if !allowed {
		return nil, errPluginHostDenied
	}
	switch call.Operation {
	case pluginsdk.HostRuntimeDatasetControl:
		var request pluginsdk.DatasetControlRequest
		if decodePluginHostPayload(call.Payload, &request) != nil {
			return nil, errPluginHostInvalid
		}
		// Match the durable HostRuntime replay scope without exposing the global
		// revision identity as the caller's acknowledgement ID.
		revisionOperationID := "dataset-runtime-" + pluginHostOperationKey(candidate, call.OperationID)
		return manager.datasets.controlWithOperationIDs(ctx, authority, request, call.OperationID, revisionOperationID)
	case pluginsdk.HostRuntimeDatasetCatalog:
		var request pluginsdk.DatasetCatalogRequest
		if decodePluginHostPayload(call.Payload, &request) != nil {
			return nil, errPluginHostInvalid
		}
		return manager.datasets.Catalog(ctx, authority, request)
	case pluginsdk.HostRuntimeDatasetStatus:
		var request pluginsdk.DatasetStatusRequest
		if decodePluginHostPayload(call.Payload, &request) != nil {
			return nil, errPluginHostInvalid
		}
		return manager.datasets.Status(ctx, authority, request)
	}
	return nil, errors.New("unsupported dataset Host operation")
}
