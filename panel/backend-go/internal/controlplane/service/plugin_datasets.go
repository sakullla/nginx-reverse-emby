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

// pluginDatasetScopeGranted requires the dataset capability to be both signed
// into the manifest declaration and projected into an administrator grant.
func pluginDatasetScopeGranted(candidate pluginhost.Candidate, permission string) bool {
	if !pluginCandidateHasGrant(candidate, permission) {
		return false
	}
	for _, scope := range candidate.Identity.Scopes {
		if scope == permission {
			return true
		}
	}
	return false
}

// pluginDatasetSelectorGranted applies the administrator source-ID projection:
// an empty selector list means the whole granted resource group, otherwise the
// exact source ID must appear.
func pluginDatasetSelectorGranted(candidate pluginhost.Candidate, permission, sourceID string) bool {
	selectors := candidate.GrantSelectors[permission]
	if len(selectors) == 0 {
		return true
	}
	for _, selector := range selectors {
		if selector == "" || selector == sourceID {
			return true
		}
	}
	return false
}

func (manager *PluginCapabilityManager) pluginHostDataset(ctx context.Context, candidate pluginhost.Candidate, call pluginsdk.HostRuntimeCall) (any, error) {
	if manager.datasets == nil {
		return nil, errPluginHostUnavailable
	}
	permission := string(pluginsdk.CapabilityDatasetManage)
	if !pluginDatasetScopeGranted(candidate, permission) {
		return nil, errPluginHostDenied
	}
	authority := DatasetAuthorization{ActorID: "plugin/" + candidate.Identity.PluginID, ResourceGroupID: candidate.ResourceGroupID, Manage: true}
	var identity struct {
		SourceID string `json:"source_id"`
	}
	if json.Unmarshal(call.Payload, &identity) != nil || pluginsdk.ValidatePolicyIdentity(identity.SourceID) != nil {
		return nil, errPluginHostInvalid
	}
	if !pluginDatasetSelectorGranted(candidate, permission, identity.SourceID) {
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

// pluginHostDatasetSourceRegister implements dataset.source-register. The
// caller selects the remote URL and update policy; the Host re-verifies
// restricted network authority and performs the initial prepare/activate.
func (manager *PluginCapabilityManager) pluginHostDatasetSourceRegister(ctx context.Context, candidate pluginhost.Candidate, call pluginsdk.HostRuntimeCall) (any, error) {
	if manager.datasets == nil {
		return nil, errPluginHostUnavailable
	}
	permission := string(pluginsdk.CapabilityDatasetSourceRegister)
	if !pluginDatasetScopeGranted(candidate, permission) {
		return nil, errPluginHostDenied
	}
	var request pluginsdk.DatasetSourceRegisterRequest
	if decodePluginHostPayload(call.Payload, &request) != nil || request.Validate() != nil {
		return nil, errPluginHostInvalid
	}
	if !pluginDatasetSelectorGranted(candidate, permission, request.Source.ID) {
		return nil, errPluginHostDenied
	}
	authority := DatasetAuthorization{ActorID: "plugin/" + candidate.Identity.PluginID, ResourceGroupID: candidate.ResourceGroupID, Manage: true}
	// Match the durable HostRuntime replay scope without exposing the global
	// revision identity as the caller's acknowledgement ID.
	revisionOperationID := "dataset-runtime-" + pluginHostOperationKey(candidate, call.OperationID)
	response, err := manager.datasets.RegisterSource(ctx, authority, request, revisionOperationID)
	if errors.Is(err, ErrInvalidArgument) {
		return nil, errPluginHostInvalid
	}
	if err != nil {
		return nil, err
	}
	return response, nil
}
