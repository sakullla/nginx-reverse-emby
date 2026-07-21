package core

import (
	"context"
	"reflect"
	"strconv"
	"strings"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/generation"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

type ModuleApplier interface {
	Apply(context.Context, model.Snapshot, model.Snapshot) error
}

func generationEntityChanges(previous, next model.Snapshot) []generation.EntityChange {
	var changes []generation.EntityChange
	changes = append(changes, changedEntities("http", previous.Rules, next.Rules,
		func(rule model.HTTPRule) string {
			if rule.ID > 0 {
				return strconv.Itoa(rule.ID)
			}
			return strings.ToLower(strings.TrimSpace(rule.FrontendURL))
		}, func(rule model.HTTPRule) bool { return rule.Enabled })...)
	changes = append(changes, changedEntities("l4", previous.L4Rules, next.L4Rules,
		func(rule model.L4Rule) string { return strconv.Itoa(rule.ID) }, func(rule model.L4Rule) bool { return rule.Enabled })...)
	changes = append(changes, changedEntities("relay", previous.RelayListeners, next.RelayListeners,
		func(listener model.RelayListener) string { return strconv.Itoa(listener.ID) }, func(listener model.RelayListener) bool { return listener.Enabled })...)
	return changes
}

func changedEntities[T any](moduleName string, previous, next []T, id func(T) string, enabled func(T) bool) []generation.EntityChange {
	nextByID := make(map[string]T, len(next))
	for _, value := range next {
		nextByID[id(value)] = value
	}
	var changes []generation.EntityChange
	for _, value := range previous {
		entityID := id(value)
		nextValue, exists := nextByID[entityID]
		action := generation.EntityAction("")
		switch {
		case !exists:
			action = generation.EntityDeleted
		case enabled(value) && !enabled(nextValue):
			action = generation.EntityDisabled
		case !reflect.DeepEqual(value, nextValue):
			action = generation.EntityModified
		}
		if action != "" {
			changes = append(changes, generation.EntityChange{Entity: generation.EntityKey{Module: moduleName, ID: entityID}, Action: action})
		}
	}
	return changes
}

func NewSnapshotActivator(modules ModuleApplier) Activator {
	return func(ctx context.Context, previous, next model.Snapshot) error {
		if modules == nil {
			return nil
		}
		return modules.Apply(ctx, previous, next)
	}
}
