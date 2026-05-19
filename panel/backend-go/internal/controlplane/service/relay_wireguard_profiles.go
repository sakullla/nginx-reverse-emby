package service

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

func ensureDefaultWireGuardProfilesForRelayLayers(ctx context.Context, cfg config.Config, store relayChainLookupStore, ruleAgentID string, layers [][]int) error {
	if len(layers) == 0 {
		return nil
	}
	listeners, err := store.ListRelayListeners(ctx, "")
	if err != nil {
		return err
	}
	listenersByID := make(map[int]storage.RelayListenerRow, len(listeners))
	for _, listener := range listeners {
		if listener.ID > 0 {
			listenersByID[listener.ID] = listener
		}
	}
	callerAgentIDs := wireGuardRelayLayerCallerAgentIDs(ruleAgentID, layers, listenersByID)
	if len(callerAgentIDs) == 0 {
		return nil
	}
	profileStore, ok := any(store).(wireGuardProfileStore)
	if !ok {
		return fmt.Errorf("%w: wireguard profile store is unavailable", ErrInvalidArgument)
	}
	profileSvc := NewWireGuardProfileService(cfg, profileStore)
	for _, callerAgentID := range callerAgentIDs {
		if _, err := profileSvc.EnsureDefault(ctx, callerAgentID); err != nil {
			return err
		}
	}
	return nil
}

func wireGuardRelayLayerCallerAgentIDs(ruleAgentID string, layers [][]int, listenersByID map[int]storage.RelayListenerRow) []string {
	callerSet := make(map[string]struct{})
	addCaller := func(callerAgentID string, downstream storage.RelayListenerRow) {
		callerAgentID = strings.TrimSpace(callerAgentID)
		downstreamAgentID := strings.TrimSpace(downstream.AgentID)
		if callerAgentID == "" || callerAgentID == downstreamAgentID {
			return
		}
		callerSet[callerAgentID] = struct{}{}
	}

	for layerIndex, layer := range layers {
		for _, listenerID := range layer {
			downstream, ok := listenersByID[listenerID]
			if !ok || !downstream.Enabled ||
				!strings.EqualFold(strings.TrimSpace(downstream.TransportMode), "wireguard") ||
				downstream.WireGuardProfileID == nil || *downstream.WireGuardProfileID <= 0 {
				continue
			}
			if layerIndex == 0 {
				addCaller(ruleAgentID, downstream)
				continue
			}
			for _, previousListenerID := range layers[layerIndex-1] {
				previous, ok := listenersByID[previousListenerID]
				if !ok || !previous.Enabled {
					continue
				}
				addCaller(previous.AgentID, downstream)
			}
		}
	}

	callerAgentIDs := make([]string, 0, len(callerSet))
	for callerAgentID := range callerSet {
		callerAgentIDs = append(callerAgentIDs, callerAgentID)
	}
	sort.Strings(callerAgentIDs)
	return callerAgentIDs
}
