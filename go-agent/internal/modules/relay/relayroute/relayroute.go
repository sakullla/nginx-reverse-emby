package relayroute

import (
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/relay"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/relay/relayplan"
)

const DefaultMaxPaths = 32

func UsesRelay(chain []int, layers [][]int) bool {
	return len(relayplan.NormalizeLayers(chain, layers)) > 0
}

func ListenerMap(listeners []model.RelayListener) map[int]model.RelayListener {
	out := make(map[int]model.RelayListener, len(listeners))
	for _, listener := range listeners {
		out[listener.ID] = listener
	}
	return out
}

func ResolvePaths(label string, chain []int, layers [][]int, listeners []model.RelayListener, target string) ([]relayplan.Path, error) {
	paths, err := ResolvePathsFromMap(chain, layers, ListenerMap(listeners), target)
	if err != nil {
		if strings.TrimSpace(label) != "" {
			return nil, fmt.Errorf("%s: %w", label, err)
		}
		return nil, err
	}
	return paths, nil
}

func ResolvePathsFromMapWithLabel(label string, chain []int, layers [][]int, listenersByID map[int]model.RelayListener, target string) ([]relayplan.Path, error) {
	paths, err := ResolvePathsFromMap(chain, layers, listenersByID, target)
	if err != nil {
		if strings.TrimSpace(label) != "" {
			return nil, fmt.Errorf("%s: %w", label, err)
		}
		return nil, err
	}
	return paths, nil
}

func ResolvePathsFromMap(chain []int, layers [][]int, listenersByID map[int]model.RelayListener, target string) ([]relayplan.Path, error) {
	normalizedLayers := relayplan.NormalizeLayers(chain, layers)
	pathIDs, err := relayplan.ExpandPaths(normalizedLayers, DefaultMaxPaths)
	if err != nil {
		return nil, err
	}
	if len(pathIDs) == 0 {
		return nil, nil
	}
	paths := make([]relayplan.Path, 0, len(pathIDs))
	for _, ids := range pathIDs {
		hops := make([]relay.Hop, 0, len(ids))
		for _, listenerID := range ids {
			listener, ok := listenersByID[listenerID]
			if !ok {
				return nil, fmt.Errorf("relay listener %d not found", listenerID)
			}
			if !listener.Enabled {
				return nil, fmt.Errorf("relay listener %d is disabled", listenerID)
			}
			if err := relay.ValidateListener(listener); err != nil {
				return nil, fmt.Errorf("relay listener %d: %w", listenerID, err)
			}
			host, port := relayHopDialEndpoint(listener)
			hops = append(hops, relay.Hop{
				Address:    net.JoinHostPort(host, strconv.Itoa(port)),
				ServerName: relayHopServerName(listener, host),
				Listener:   listener,
			})
		}
		paths = append(paths, relayplan.Path{
			IDs:  ids,
			Hops: hops,
			Key:  relayplan.PathKey("relay_path", ids, target),
		})
	}
	return paths, nil
}

func relayHopDialEndpoint(listener model.RelayListener) (string, int) {
	if strings.EqualFold(strings.TrimSpace(listener.TransportMode), relay.ListenerTransportModeWireGuard) {
		host := strings.TrimSpace(listener.ListenHost)
		if host == "" {
			for _, bindHost := range listener.BindHosts {
				if trimmed := strings.TrimSpace(bindHost); trimmed != "" {
					host = trimmed
					break
				}
			}
		}
		return host, listener.ListenPort
	}
	return model.RelayListenerDialEndpoint(listener)
}

func relayHopServerName(listener model.RelayListener, fallback string) string {
	if publicHost := strings.TrimSpace(listener.PublicHost); publicHost != "" {
		return publicHost
	}
	return fallback
}

func ClonePaths(paths []relayplan.Path) []relayplan.Path {
	cloned := make([]relayplan.Path, len(paths))
	for i, path := range paths {
		cloned[i] = relayplan.Path{
			IDs:  append([]int(nil), path.IDs...),
			Hops: append([]relay.Hop(nil), path.Hops...),
			Key:  path.Key,
		}
	}
	return cloned
}

func ClonePathsWithTarget(paths []relayplan.Path, target string) []relayplan.Path {
	cloned := ClonePaths(paths)
	for i := range cloned {
		cloned[i].Key = relayplan.PathKey("relay_path", cloned[i].IDs, target)
	}
	return cloned
}
