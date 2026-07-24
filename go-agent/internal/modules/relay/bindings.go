package relay

import (
	"net"
	"strconv"
	"strings"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

func relayListenerBindingKeys(listeners []model.RelayListener) []string {
	keys := make([]string, 0, len(listeners))
	for _, listener := range listeners {
		if !listener.Enabled {
			continue
		}
		protocol := relayListenerBindingProtocol(listener.TransportMode)
		bindHosts := relayListenerBindHosts(listener)
		for _, bindHost := range bindHosts {
			address := net.JoinHostPort(bindHost, strconv.Itoa(listener.ListenPort))
			keys = append(keys, protocol+":"+address)
		}
	}
	return keys
}

func serverBindingKeys(server *Server) []string {
	if server == nil {
		return nil
	}
	return server.BindingKeys()
}

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

func firstBindingOverlap(bindings []string) (string, string, bool) {
	for index, leftBinding := range bindings {
		leftKey, ok := parseBindingKey(leftBinding)
		if !ok {
			continue
		}
		for _, rightBinding := range bindings[index+1:] {
			rightKey, ok := parseBindingKey(rightBinding)
			if ok && relayBindingKeysOverlap(leftKey, rightKey) {
				return leftBinding, rightBinding, true
			}
		}
	}
	return "", "", false
}

func firstNonReusableBindingOverlap(active, next []string) (string, string, bool) {
	for _, activeBinding := range active {
		activeKey, ok := parseBindingKey(activeBinding)
		if !ok {
			continue
		}
		for _, nextBinding := range next {
			if activeBinding == nextBinding {
				continue
			}
			nextKey, ok := parseBindingKey(nextBinding)
			if ok && relayBindingKeysOverlap(activeKey, nextKey) && !relayBindingCanReuse(activeKey, nextKey) {
				return activeBinding, nextBinding, true
			}
		}
	}
	return "", "", false
}

func relayBindingCanReuse(active, next bindingKey) bool {
	if active.namespace != next.namespace || active.protocol != "tcp" || next.protocol != "tcp" || active.port != next.port ||
		!active.wildcard || next.wildcard {
		return false
	}
	activeFamily := bindingHostIPFamily(active.host)
	nextFamily := bindingHostIPFamily(next.host)
	return activeFamily != 0 && activeFamily == nextFamily
}

func relayBindingKeysOverlap(left, right bindingKey) bool {
	if left.namespace != right.namespace || left.protocol != right.protocol || left.port != right.port {
		return false
	}
	if left.host == right.host || bindingHostsEquivalent(left.host, right.host) {
		return true
	}
	if !left.wildcard && !right.wildcard {
		return false
	}
	leftFamily := bindingHostIPFamily(left.host)
	rightFamily := bindingHostIPFamily(right.host)
	return leftFamily == 0 || rightFamily == 0 || leftFamily == rightFamily
}

func bindingHostIPFamily(host string) int {
	ip := net.ParseIP(strings.TrimSpace(host))
	if ip == nil {
		return 0
	}
	if ip.To4() != nil {
		return 4
	}
	return 6
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

func relayListenerBindingProtocol(transportMode string) string {
	if strings.EqualFold(strings.TrimSpace(transportMode), ListenerTransportModeQUIC) {
		return "udp"
	}
	return "tcp"
}
