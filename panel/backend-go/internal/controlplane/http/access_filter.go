package http

import (
	"context"
	"strconv"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/authz"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/service"
)

func (d Dependencies) visibleResource(ctx context.Context, kind, id string) (bool, error) {
	requestActor, found := ctx.Value(actorContextKey{}).(authz.Actor)
	if !found || d.AccessManager == nil || requestActor.Bootstrap {
		return true, nil
	}
	return d.AccessManager.CanAccessResource(ctx, requestActor, kind, id)
}

func (d Dependencies) accessFilteringActive(ctx context.Context) bool {
	actor, found := ctx.Value(actorContextKey{}).(authz.Actor)
	return found && d.AccessManager != nil && !actor.Bootstrap
}

func resourceKey(agentID string, id int) string {
	if agentID == "" {
		return strconv.Itoa(id)
	}
	return agentID + ":" + strconv.Itoa(id)
}

func (d Dependencies) filterAgents(ctx context.Context, items []service.AgentSummary) ([]service.AgentSummary, error) {
	result := make([]service.AgentSummary, 0, len(items))
	for _, item := range items {
		visible, err := d.visibleResource(ctx, "agent", item.ID)
		if err != nil {
			return nil, err
		}
		if visible {
			result = append(result, item)
		}
	}
	return result, nil
}

func (d Dependencies) filterHTTPRules(ctx context.Context, items []service.HTTPRule) ([]service.HTTPRule, error) {
	result := make([]service.HTTPRule, 0, len(items))
	for _, item := range items {
		visible, err := d.visibleResource(ctx, "http_rule", resourceKey(item.AgentID, item.ID))
		if err != nil {
			return nil, err
		}
		if visible {
			result = append(result, item)
		}
	}
	return result, nil
}

func (d Dependencies) filterL4Rules(ctx context.Context, items []service.L4Rule) ([]service.L4Rule, error) {
	result := make([]service.L4Rule, 0, len(items))
	for _, item := range items {
		visible, err := d.visibleResource(ctx, "l4_rule", resourceKey(item.AgentID, item.ID))
		if err != nil {
			return nil, err
		}
		if visible {
			result = append(result, item)
		}
	}
	return result, nil
}

func (d Dependencies) filterRelayListeners(ctx context.Context, items []service.RelayListener) ([]service.RelayListener, error) {
	result := make([]service.RelayListener, 0, len(items))
	for _, item := range items {
		visible, err := d.visibleResource(ctx, "relay_listener", resourceKey(item.AgentID, item.ID))
		if err != nil {
			return nil, err
		}
		if visible {
			result = append(result, item)
		}
	}
	return result, nil
}

func (d Dependencies) filterCertificates(ctx context.Context, agentID string, items []service.ManagedCertificate) ([]service.ManagedCertificate, error) {
	result := make([]service.ManagedCertificate, 0, len(items))
	for _, item := range items {
		itemAgentID := agentID
		if item.AgentID != "" {
			itemAgentID = item.AgentID
		}
		visible, err := d.visibleResource(ctx, "certificate", resourceKey(itemAgentID, item.ID))
		if err != nil {
			return nil, err
		}
		if visible {
			result = append(result, item)
		}
	}
	return result, nil
}
