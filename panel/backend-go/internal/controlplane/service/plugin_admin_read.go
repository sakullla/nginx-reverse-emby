package service

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/authz"
)

func (s *PluginService) ListForActor(ctx context.Context, actor authz.Actor) ([]PluginSummary, error) {
	if !actor.Has(authz.PermissionResourceRead) && !actor.Has(authz.PermissionSystemAdmin) {
		return nil, authz.ErrForbidden
	}
	rows, err := s.List(ctx)
	if err != nil || actor.Has(authz.PermissionSystemAdmin) || actor.Bootstrap {
		return rows, err
	}
	result := make([]PluginSummary, 0, len(rows))
	for _, row := range rows {
		instances, err := s.store.ListPluginInstances(ctx, row.PluginID)
		if err != nil {
			return nil, err
		}
		for _, instance := range instances {
			if actor.CanAccessGroup(instance.ResourceGroupID) {
				result = append(result, row)
				break
			}
		}
	}
	return result, nil
}

func (s *PluginService) DetailForActor(ctx context.Context, pluginID string, actor authz.Actor) (PluginDetail, error) {
	if !actor.Has(authz.PermissionResourceRead) && !actor.Has(authz.PermissionSystemAdmin) {
		return PluginDetail{}, authz.ErrForbidden
	}
	if actor.Has(authz.PermissionSystemAdmin) || actor.Bootstrap {
		return s.Detail(ctx, pluginID)
	}
	visible := make(map[string]struct{})
	rows, err := s.store.ListPluginInstances(ctx, pluginID)
	if err != nil {
		return PluginDetail{}, err
	}
	for _, instance := range rows {
		if actor.CanAccessGroup(instance.ResourceGroupID) {
			visible[instance.ID] = struct{}{}
		}
	}
	if len(visible) == 0 {
		return PluginDetail{}, authz.ErrForbidden
	}
	detail, err := s.Detail(ctx, pluginID)
	if err != nil {
		return PluginDetail{}, err
	}
	instances := make([]PluginInstanceDetail, 0, len(visible))
	for _, instance := range detail.Instances {
		if _, ok := visible[instance.ID]; ok {
			instances = append(instances, instance)
		}
	}
	statuses := make([]PluginAgentStatus, 0, len(detail.AgentStatuses))
	for _, status := range detail.AgentStatuses {
		if _, ok := visible[status.InstanceID]; ok {
			statuses = append(statuses, status)
		}
	}
	detail.Instances = instances
	detail.AgentStatuses = statuses
	return detail, nil
}

func (s *PluginService) OperationsForActor(ctx context.Context, pluginID string, actor authz.Actor) ([]PluginOperationDetail, error) {
	if !actor.Has(authz.PermissionResourceRead) && !actor.Has(authz.PermissionSystemAdmin) {
		return nil, authz.ErrForbidden
	}
	if actor.Has(authz.PermissionSystemAdmin) || actor.Bootstrap {
		return s.Operations(ctx, pluginID)
	}
	instances, err := s.store.ListPluginInstances(ctx, pluginID)
	if err != nil {
		return nil, err
	}
	visibleAgents := map[string]struct{}{}
	visibleInstance := false
	defaultTargetID, err := s.defaultPluginTargetID(ctx)
	if err != nil {
		return nil, err
	}
	for _, instance := range instances {
		if !actor.CanAccessGroup(instance.ResourceGroupID) {
			continue
		}
		visibleInstance = true
		targets, err := pluginTargetIDs(json.RawMessage(instance.TargetJSON), defaultTargetID)
		if err != nil {
			return nil, ErrPluginReadProjection
		}
		for _, target := range targets {
			visibleAgents[target] = struct{}{}
		}
	}
	if !visibleInstance {
		return nil, authz.ErrForbidden
	}
	operations, err := s.Operations(ctx, pluginID)
	if err != nil {
		return nil, err
	}
	for index := range operations {
		operations[index].ActorID = ""
		operations[index].SessionID = ""
		operations[index].CorrelationID = ""
		var all map[string]json.RawMessage
		if err := json.Unmarshal(operations[index].AgentResults, &all); err != nil {
			return nil, errors.Join(ErrPluginReadProjection, err)
		}
		filtered := make(map[string]json.RawMessage)
		for agentID, value := range all {
			if _, ok := visibleAgents[agentID]; ok {
				filtered[agentID] = value
			}
		}
		encoded, err := json.Marshal(filtered)
		if err != nil {
			return nil, err
		}
		operations[index].AgentResults = encoded
	}
	return operations, nil
}
