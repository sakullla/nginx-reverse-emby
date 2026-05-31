package app

import (
	"net"
	"strconv"
	"strings"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/relay"
)

func bindingKeysOverlap(left, right []string) bool {
	if len(left) == 0 || len(right) == 0 {
		return false
	}
	for _, leftBinding := range left {
		leftKey, ok := parseBindingKey(leftBinding)
		if !ok {
			continue
		}
		for _, rightBinding := range right {
			rightKey, ok := parseBindingKey(rightBinding)
			if !ok {
				continue
			}
			if leftKey.overlaps(rightKey) {
				return true
			}
		}
	}
	return false
}

type bindingKey struct {
	namespace string
	protocol  string
	host      string
	port      string
	wildcard  bool
}

func parseBindingKey(raw string) (bindingKey, bool) {
	protocol, address, ok := strings.Cut(raw, ":")
	if !ok {
		return bindingKey{}, false
	}
	protocol = strings.ToLower(strings.TrimSpace(protocol))
	if protocol == "" {
		return bindingKey{}, false
	}
	namespace := "host"
	if protocol == "wireguard" {
		profileID, rest, ok := strings.Cut(address, ":")
		if !ok || strings.TrimSpace(profileID) == "" {
			return bindingKey{}, false
		}
		protocol, address, ok = strings.Cut(rest, ":")
		if !ok {
			return bindingKey{}, false
		}
		protocol = strings.ToLower(strings.TrimSpace(protocol))
		if protocol == "" {
			return bindingKey{}, false
		}
		namespace = "wireguard:" + strings.TrimSpace(profileID)
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil || port == "" {
		return bindingKey{}, false
	}
	host = strings.TrimSpace(strings.Trim(host, "[]"))
	return bindingKey{
		namespace: namespace,
		protocol:  protocol,
		host:      normalizeBindingHost(host),
		port:      port,
		wildcard:  bindingHostIsWildcard(host),
	}, true
}

func (k bindingKey) overlaps(other bindingKey) bool {
	if k.namespace != other.namespace || k.protocol != other.protocol || k.port != other.port {
		return false
	}
	if k.host == other.host || k.wildcard || other.wildcard {
		return true
	}
	return bindingHostsEquivalent(k.host, other.host)
}

func normalizeBindingHost(host string) string {
	if ip := net.ParseIP(host); ip != nil {
		return ip.String()
	}
	return strings.ToLower(host)
}

func bindingHostsEquivalent(left, right string) bool {
	left = strings.ToLower(strings.TrimSpace(left))
	right = strings.ToLower(strings.TrimSpace(right))
	if left == right {
		return true
	}
	if left == "localhost" && isLoopbackBindingHost(right) {
		return true
	}
	if right == "localhost" && isLoopbackBindingHost(left) {
		return true
	}
	return false
}

func isLoopbackBindingHost(host string) bool {
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func bindingHostIsWildcard(host string) bool {
	if strings.TrimSpace(host) == "" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsUnspecified()
}

func isRuntimeBindConflict(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "address already in use") ||
		strings.Contains(message, "port is in use") ||
		strings.Contains(message, "only one usage of each socket address") ||
		strings.Contains(message, "an attempt was made to access a socket") ||
		strings.Contains(message, "eaddrinuse")
}

func relayListenerBindingKeys(listeners []model.RelayListener) []string {
	keys := make([]string, 0, len(listeners))
	for _, listener := range listeners {
		if !listener.Enabled {
			continue
		}
		protocol := relayListenerBindingProtocol(listener.TransportMode)
		if strings.EqualFold(strings.TrimSpace(listener.TransportMode), relay.ListenerTransportModeWireGuard) {
			listenHost := strings.TrimSpace(listener.ListenHost)
			if listenHost == "" {
				continue
			}
			address := net.JoinHostPort(listenHost, strconv.Itoa(listener.ListenPort))
			keys = append(keys, "wireguard:"+strconv.Itoa(valueOrZeroWireGuardProfileID(listener.WireGuardProfileID))+":"+protocol+":"+address)
			continue
		}
		bindHosts := relayListenerBindHosts(listener)
		for _, bindHost := range bindHosts {
			address := net.JoinHostPort(bindHost, strconv.Itoa(listener.ListenPort))
			keys = append(keys, protocol+":"+address)
		}
	}
	return keys
}

func relayListenerBindHosts(listener model.RelayListener) []string {
	bindHosts := make([]string, 0, len(listener.BindHosts))
	seen := make(map[string]struct{}, len(listener.BindHosts))
	for _, rawHost := range listener.BindHosts {
		host := strings.TrimSpace(rawHost)
		if host == "" {
			continue
		}
		if _, ok := seen[host]; ok {
			continue
		}
		seen[host] = struct{}{}
		bindHosts = append(bindHosts, host)
	}
	if len(bindHosts) == 0 && strings.TrimSpace(listener.ListenHost) != "" {
		bindHosts = append(bindHosts, strings.TrimSpace(listener.ListenHost))
	}
	return bindHosts
}

func relayListenerBindingProtocol(transportMode string) string {
	if strings.EqualFold(strings.TrimSpace(transportMode), relay.ListenerTransportModeQUIC) {
		return "udp"
	}
	return "tcp"
}

func valueOrZeroWireGuardProfileID(value *int) int {
	if value == nil {
		return 0
	}
	return *value
}

func cloneL4Rules(rules []model.L4Rule) []model.L4Rule {
	if rules == nil {
		return nil
	}
	cloned := make([]model.L4Rule, len(rules))
	for i, rule := range rules {
		cloned[i] = rule
		cloned[i].Backends = append([]model.L4Backend(nil), rule.Backends...)
		cloned[i].RelayChain = append([]int(nil), rule.RelayChain...)
		cloned[i].RelayLayers = cloneIntLayers(rule.RelayLayers)
		cloned[i].Tags = append([]string(nil), rule.Tags...)
	}
	return cloned
}

func cloneRelayListeners(listeners []model.RelayListener) []model.RelayListener {
	if listeners == nil {
		return nil
	}
	cloned := make([]model.RelayListener, len(listeners))
	for i, listener := range listeners {
		cloned[i] = listener
		cloned[i].BindHosts = append([]string(nil), listener.BindHosts...)
		cloned[i].PinSet = append([]model.RelayPin(nil), listener.PinSet...)
		cloned[i].TrustedCACertificateIDs = append([]int(nil), listener.TrustedCACertificateIDs...)
		cloned[i].Tags = append([]string(nil), listener.Tags...)
	}
	return cloned
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
