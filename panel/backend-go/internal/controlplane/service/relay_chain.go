package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

type relayChainLookupStore interface {
	ListRelayListeners(context.Context, string) ([]storage.RelayListenerRow, error)
}

func normalizeRelayChainInput(values []int, protocol string) ([]int, error) {
	normalized := make([]int, 0, len(values))
	seen := make(map[int]struct{}, len(values))
	for _, value := range values {
		if value <= 0 {
			return nil, fmt.Errorf("%w: relay_chain entries must be positive integer listener IDs", ErrInvalidArgument)
		}
		if _, ok := seen[value]; ok {
			return nil, fmt.Errorf("%w: relay_chain entries must not contain duplicates", ErrInvalidArgument)
		}
		seen[value] = struct{}{}
		normalized = append(normalized, value)
	}
	return normalized, nil
}

func normalizeRelayLayersInput(layers [][]int, protocol string) ([][]int, error) {
	normalized := make([][]int, 0, len(layers))
	seenAcrossLayers := make(map[int]struct{})
	for _, layer := range layers {
		normalizedLayer, err := normalizeRelayChainInput(layer, protocol)
		if err != nil {
			return nil, err
		}
		if len(normalizedLayer) > 0 {
			for _, listenerID := range normalizedLayer {
				if _, ok := seenAcrossLayers[listenerID]; ok {
					return nil, fmt.Errorf("%w: relay_layers entries must not repeat listener IDs across layers", ErrInvalidArgument)
				}
				seenAcrossLayers[listenerID] = struct{}{}
			}
			normalized = append(normalized, normalizedLayer)
		}
	}
	return normalized, nil
}

func flattenRelayLayers(layers [][]int) []int {
	flattened := make([]int, 0)
	for _, layer := range layers {
		flattened = append(flattened, layer...)
	}
	return flattened
}

func relayConfigReferencesListener(chainJSON string, layersJSON string, listenerID int) bool {
	return containsInt(parseIntArray(chainJSON), listenerID) || containsInt(flattenRelayLayers(parseIntLayers(layersJSON)), listenerID)
}

func cloneIntLayers(layers [][]int) [][]int {
	if layers == nil {
		return nil
	}
	cloned := make([][]int, len(layers))
	for i, layer := range layers {
		cloned[i] = append([]int(nil), layer...)
	}
	return cloned
}

func validateRelayChainReferences(ctx context.Context, store relayChainLookupStore, knownAgentIDs []string, relayChain []int) error {
	if len(relayChain) == 0 {
		return nil
	}

	listeners, err := store.ListRelayListeners(ctx, "")
	if err != nil {
		return err
	}
	listenersByID := make(map[int]storage.RelayListenerRow, len(listeners))
	for _, listener := range listeners {
		listenersByID[listener.ID] = listener
	}

	knownAgents := make(map[string]struct{}, len(knownAgentIDs))
	for _, agentID := range knownAgentIDs {
		knownAgents[strings.TrimSpace(agentID)] = struct{}{}
	}

	for _, listenerID := range relayChain {
		listener, ok := listenersByID[listenerID]
		if !ok {
			return fmt.Errorf("%w: relay listener not found: %d", ErrInvalidArgument, listenerID)
		}
		if !listener.Enabled {
			return fmt.Errorf("%w: relay listener is disabled: %d", ErrInvalidArgument, listenerID)
		}
		if _, ok := knownAgents[strings.TrimSpace(listener.AgentID)]; !ok {
			return fmt.Errorf("%w: relay listener belongs to unknown agent: %d", ErrInvalidArgument, listenerID)
		}
	}
	return nil
}
