package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

// ExecutePluginCapabilityResourceCall is the core-owned, redacted resource
// adapter. It revalidates the immutable binding on every use and never returns
// credentials, raw configuration, sockets, or database rows.
func (s *GormStore) ExecutePluginCapabilityResourceCall(ctx context.Context, binding PluginCapabilityTargetBinding, call pluginsdk.RPCResourceCall) ([]byte, error) {
	current, ok, err := s.PluginCapabilityTargetBinding(ctx, binding.Kind, binding.ID)
	if err != nil || !ok || current.Version != binding.Version || current.ResourceGroupID != binding.ResourceGroupID {
		return nil, errors.Join(errors.New("plugin capability resource changed or was deleted"), err)
	}
	switch call.Operation {
	case pluginsdk.RPCResourceInspect:
		return boundedPluginResourceJSON(map[string]any{"kind": current.Kind, "resource_group_id": current.ResourceGroupID, "version": current.Version})
	case pluginsdk.RPCResourceProbe:
		address, err := s.pluginCapabilityProbeAddress(ctx, current.Kind, current.ID)
		if err != nil {
			return nil, err
		}
		started := time.Now()
		connection, err := (&net.Dialer{Timeout: 2 * time.Second}).DialContext(ctx, "tcp", address)
		if err != nil {
			return boundedPluginResourceJSON(map[string]any{"available": false, "error": "unavailable"})
		}
		_ = connection.Close()
		return boundedPluginResourceJSON(map[string]any{"available": true, "latency_ms": time.Since(started).Milliseconds()})
	case pluginsdk.RPCResourceTrafficSummary:
		return nil, errors.New("traffic summary requires the canonical traffic service adapter")
	case pluginsdk.RPCResourceDNSApply:
		return nil, errors.New("privileged resource operation has no configured core adapter")
	default:
		return nil, errors.New("plugin capability resource operation is unsupported")
	}
}

func boundedPluginResourceJSON(value any) ([]byte, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	if len(encoded) > pluginsdk.RPCResourcePayloadMaxBytes {
		return nil, errors.New("plugin capability resource result exceeds the canonical bound")
	}
	return encoded, nil
}

func (s *GormStore) pluginCapabilityProbeAddress(ctx context.Context, kind, id string) (string, error) {
	switch kind {
	case "agent":
		var row AgentRow
		if result := s.db.WithContext(ctx).Where("id = ?", id).Limit(1).Find(&row); result.Error != nil || result.RowsAffected != 1 {
			return "", errors.Join(errors.New("agent probe target is unavailable"), result.Error)
		}
		return pluginCapabilityURLAddress(row.AgentURL)
	case "http_rule", "http.rule":
		agentID, numericID, ok := splitBoundResourceID(id)
		if !ok {
			return "", errors.New("HTTP probe target is invalid")
		}
		var row HTTPRuleRow
		if result := s.db.WithContext(ctx).Where("agent_id = ? AND id = ?", agentID, numericID).Limit(1).Find(&row); result.Error != nil || result.RowsAffected != 1 {
			return "", errors.Join(errors.New("HTTP probe target is unavailable"), result.Error)
		}
		return pluginCapabilityURLAddress(row.BackendURL)
	case "l4", "l4_rule", "l4.rule":
		agentID, numericID, ok := splitBoundResourceID(id)
		if !ok {
			return "", errors.New("L4 probe target is invalid")
		}
		var row L4RuleRow
		if result := s.db.WithContext(ctx).Where("agent_id = ? AND id = ?", agentID, numericID).Limit(1).Find(&row); result.Error != nil || result.RowsAffected != 1 {
			return "", errors.Join(errors.New("L4 probe target is unavailable"), result.Error)
		}
		return net.JoinHostPort(row.UpstreamHost, strconv.Itoa(row.UpstreamPort)), nil
	case "relay", "relay_listener", "relay.listener":
		agentID, numericID, ok := splitBoundResourceID(id)
		if !ok {
			return "", errors.New("relay probe target is invalid")
		}
		var row RelayListenerRow
		if result := s.db.WithContext(ctx).Where("agent_id = ? AND id = ?", agentID, numericID).Limit(1).Find(&row); result.Error != nil || result.RowsAffected != 1 {
			return "", errors.Join(errors.New("relay probe target is unavailable"), result.Error)
		}
		host, port := row.PublicHost, row.PublicPort
		if strings.TrimSpace(host) == "" || port <= 0 {
			host, port = row.ListenHost, row.ListenPort
		}
		return net.JoinHostPort(host, strconv.Itoa(port)), nil
	case "egress_profile":
		numericID, err := strconv.Atoi(id)
		if err != nil {
			return "", errors.New("egress probe target is invalid")
		}
		var row EgressProfileRow
		if result := s.db.WithContext(ctx).Where("id = ?", numericID).Limit(1).Find(&row); result.Error != nil || result.RowsAffected != 1 {
			return "", errors.Join(errors.New("egress probe target is unavailable"), result.Error)
		}
		return pluginCapabilityURLAddress(row.ProxyURL)
	default:
		return "", fmt.Errorf("resource kind %q does not support an external probe", kind)
	}
}

func pluginCapabilityURLAddress(value string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Hostname() == "" {
		return "", errors.New("resource endpoint is invalid")
	}
	port := parsed.Port()
	if port == "" {
		switch parsed.Scheme {
		case "https":
			port = "443"
		case "http":
			port = "80"
		default:
			return "", errors.New("resource endpoint port is missing")
		}
	}
	return net.JoinHostPort(parsed.Hostname(), port), nil
}
