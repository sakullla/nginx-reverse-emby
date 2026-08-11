package service

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/authz"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

func (s *PluginService) scopedRead(ctx context.Context, read func(*PluginService) error) error {
	transactions, ok := s.store.(interface {
		PluginReadTransaction(context.Context, func(*storage.GormStore) error) error
	})
	if !ok {
		return errors.New("stable scoped plugin read is unavailable")
	}
	return transactions.PluginReadTransaction(ctx, func(store *storage.GormStore) error {
		scoped := *s
		scoped.store = store
		return read(&scoped)
	})
}

func (s *PluginService) ListForActor(ctx context.Context, actor authz.Actor) ([]PluginSummary, error) {
	if !actor.Has(authz.PermissionResourceRead) && !actor.Has(authz.PermissionSystemAdmin) {
		return nil, authz.ErrForbidden
	}
	var result []PluginSummary
	err := s.scopedRead(ctx, func(scoped *PluginService) error {
		rows, err := scoped.List(ctx)
		if err != nil {
			return err
		}
		if actor.Has(authz.PermissionSystemAdmin) || actor.Bootstrap {
			result = rows
			return nil
		}
		result = make([]PluginSummary, 0, len(rows))
		for _, row := range rows {
			instances, err := scoped.store.ListPluginInstances(ctx, row.PluginID)
			if err != nil {
				return err
			}
			for _, instance := range instances {
				if actor.CanAccessGroup(instance.ResourceGroupID) {
					result = append(result, row)
					break
				}
			}
		}
		return nil
	})
	return result, err
}

func (s *PluginService) DetailForActor(ctx context.Context, pluginID string, actor authz.Actor) (PluginDetail, error) {
	if !actor.Has(authz.PermissionResourceRead) && !actor.Has(authz.PermissionSystemAdmin) {
		return PluginDetail{}, authz.ErrForbidden
	}
	var result PluginDetail
	err := s.scopedRead(ctx, func(scoped *PluginService) error {
		if actor.Has(authz.PermissionSystemAdmin) || actor.Bootstrap {
			var err error
			result, err = scoped.Detail(ctx, pluginID)
			return err
		}
		visible := make(map[string]struct{})
		rows, err := scoped.store.ListPluginInstances(ctx, pluginID)
		if err != nil {
			return err
		}
		for _, instance := range rows {
			if actor.CanAccessGroup(instance.ResourceGroupID) {
				visible[instance.ID] = struct{}{}
			}
		}
		if len(visible) == 0 {
			return authz.ErrForbidden
		}
		detail, err := scoped.Detail(ctx, pluginID)
		if err != nil {
			return err
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
		detail.Instances, detail.AgentStatuses = instances, statuses
		result = detail
		return nil
	})
	return result, err
}

func (s *PluginService) OperationsForActor(ctx context.Context, pluginID string, actor authz.Actor) ([]PluginOperationDetail, error) {
	if !actor.Has(authz.PermissionResourceRead) && !actor.Has(authz.PermissionSystemAdmin) {
		return nil, authz.ErrForbidden
	}
	var result []PluginOperationDetail
	err := s.scopedRead(ctx, func(scoped *PluginService) error {
		operations, err := scoped.Operations(ctx, pluginID)
		if err != nil {
			return err
		}
		if actor.Has(authz.PermissionSystemAdmin) || actor.Bootstrap {
			result = operations
			return nil
		}
		instances, err := scoped.store.ListPluginInstances(ctx, pluginID)
		if err != nil {
			return err
		}
		visibleAgents, visibleGroups := map[string]struct{}{}, map[string]struct{}{}
		defaultTargetID, err := scoped.defaultPluginTargetID(ctx)
		if err != nil {
			return err
		}
		for _, instance := range instances {
			if !actor.CanAccessGroup(instance.ResourceGroupID) {
				continue
			}
			visibleGroups[instance.ResourceGroupID] = struct{}{}
			targets, err := pluginTargetIDs(json.RawMessage(instance.TargetJSON), defaultTargetID)
			if err != nil {
				return ErrPluginReadProjection
			}
			for _, target := range targets {
				visibleAgents[target] = struct{}{}
			}
		}
		if len(visibleGroups) == 0 {
			return authz.ErrForbidden
		}
		result = make([]PluginOperationDetail, 0, len(operations))
		for _, operation := range operations {
			if _, ok := visibleGroups[operation.ResourceGroupID]; !ok || operation.InstanceID == "" {
				continue
			}
			operation.ActorID, operation.SessionID, operation.CorrelationID = "", "", ""
			var all map[string]json.RawMessage
			if err := json.Unmarshal(operation.AgentResults, &all); err != nil {
				return errors.Join(ErrPluginReadProjection, err)
			}
			filtered := make(map[string]json.RawMessage)
			for agentID, value := range all {
				if _, ok := visibleAgents[agentID]; ok {
					filtered[agentID] = value
				}
			}
			operation.AgentResults, err = json.Marshal(filtered)
			if err != nil {
				return err
			}
			result = append(result, operation)
		}
		return nil
	})
	return result, err
}
