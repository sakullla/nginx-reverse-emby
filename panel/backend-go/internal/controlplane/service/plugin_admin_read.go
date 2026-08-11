package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

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
		result = make([]PluginOperationDetail, 0, len(operations))
		for _, operation := range operations {
			visibleScopes, filteredResults, err := pluginScopedOperationAgentResults(operation.AgentResults, operation.Scopes, actor)
			if err != nil {
				return err
			}
			if len(visibleScopes) == 0 {
				continue
			}
			operation.Scopes = visibleScopes
			operation.InstanceID, operation.ResourceGroupID = "", ""
			operation.ActorID, operation.SessionID, operation.CorrelationID = "", "", ""
			operation.AgentResults = filteredResults
			result = append(result, operation)
		}
		return nil
	})
	if err == nil && len(result) == 0 && !actor.Has(authz.PermissionSystemAdmin) && !actor.Bootstrap {
		return nil, authz.ErrForbidden
	}
	return result, err
}

func pluginScopedOperationAgentResults(raw json.RawMessage, scopes []PluginOperationScopeDetail, actor authz.Actor) ([]PluginOperationScopeDetail, json.RawMessage, error) {
	visibleScopes := make([]PluginOperationScopeDetail, 0, len(scopes))
	allScopes := make(map[string]PluginOperationScopeDetail, len(scopes))
	visibleInstances := make(map[string]struct{}, len(scopes))
	for _, scope := range scopes {
		if scope.InstanceID == "" || scope.ResourceGroupID == "" {
			return nil, nil, ErrPluginReadProjection
		}
		if _, duplicate := allScopes[scope.InstanceID]; duplicate {
			return nil, nil, ErrPluginReadProjection
		}
		allScopes[scope.InstanceID] = scope
		if actor.CanAccessGroup(scope.ResourceGroupID) {
			visibleScopes = append(visibleScopes, scope)
			visibleInstances[scope.InstanceID] = struct{}{}
		}
	}
	var all map[string]json.RawMessage
	if err := json.Unmarshal(raw, &all); err != nil || all == nil {
		if err == nil {
			err = ErrPluginReadProjection
		}
		return nil, nil, errors.Join(ErrPluginReadProjection, err)
	}
	filtered := make(map[string]json.RawMessage)
	for identity, value := range all {
		agentID, instanceID, ok := pluginOperationAgentResultIdentity(identity)
		if !ok {
			return nil, nil, ErrPluginReadProjection
		}
		if _, exists := allScopes[instanceID]; !exists {
			return nil, nil, ErrPluginReadProjection
		}
		if _, visible := visibleInstances[instanceID]; visible {
			filtered[agentID+"/"+instanceID] = value
		}
	}
	encoded, err := json.Marshal(filtered)
	return visibleScopes, encoded, err
}

func pluginOperationAgentResultIdentity(identity string) (string, string, bool) {
	if identity != strings.TrimSpace(identity) || strings.Count(identity, "/") != 1 {
		return "", "", false
	}
	parts := strings.SplitN(identity, "/", 2)
	if parts[0] == "" || parts[1] == "" || strings.ContainsAny(parts[0], "\r\n\x00") || strings.ContainsAny(parts[1], "\r\n\x00") {
		return "", "", false
	}
	return parts[0], parts[1], true
}
