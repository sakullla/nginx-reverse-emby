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
	nextWireGuard := make(map[string]model.WireGuardProfile, len(next.WireGuardProfiles))
	for _, profile := range next.WireGuardProfiles {
		nextWireGuard[wireGuardEntityID(profile)] = profile
	}
	for _, profile := range previous.WireGuardProfiles {
		id := wireGuardEntityID(profile)
		nextProfile, exists := nextWireGuard[id]
		action := generation.EntityAction("")
		switch {
		case !exists:
			action = generation.EntityDeleted
		case profile.Enabled && !nextProfile.Enabled:
			action = generation.EntityDisabled
		case removedWireGuardPeer(profile, nextProfile):
			action = generation.EntityDeleted
		case !reflect.DeepEqual(profile, nextProfile):
			action = generation.EntityModified
		}
		if action != "" {
			changes = append(changes, generation.EntityChange{Entity: generation.EntityKey{Module: "wireguard", ID: id}, Action: action})
		}
	}
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

func wireGuardEntityID(profile model.WireGuardProfile) string {
	return strings.TrimSpace(profile.AgentID) + "/" + strconv.Itoa(profile.ID)
}

func removedWireGuardPeer(previous, next model.WireGuardProfile) bool {
	nextKeys := make(map[string]struct{}, len(next.Peers))
	for _, peer := range next.Peers {
		nextKeys[strings.TrimSpace(peer.PublicKey)] = struct{}{}
	}
	for _, peer := range previous.Peers {
		if _, ok := nextKeys[strings.TrimSpace(peer.PublicKey)]; !ok {
			return true
		}
	}
	return false
}

func NewSnapshotActivator(modules ModuleApplier) Activator {
	return func(ctx context.Context, previous, next model.Snapshot) error {
		if modules == nil {
			return nil
		}
		return modules.Apply(ctx, previous, next)
	}
}
