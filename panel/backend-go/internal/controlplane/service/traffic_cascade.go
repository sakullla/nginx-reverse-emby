package service

import (
	"context"
	"strconv"
)

type trafficScopeDeletionStore interface {
	DeleteTrafficByScope(context.Context, string, string, string) (int64, error)
}

func deleteTrafficByScopeIfSupported(ctx context.Context, store any, agentID, scopeType string, id int) error {
	deleter, ok := store.(trafficScopeDeletionStore)
	if !ok {
		return nil
	}
	_, err := deleter.DeleteTrafficByScope(ctx, agentID, scopeType, strconv.Itoa(id))
	return err
}
