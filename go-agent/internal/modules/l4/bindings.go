package l4

import (
	"context"
	"net"
	"strings"
	"time"
)

const runtimeBindRetryTimeout = 2 * time.Second
const runtimeBindRetryInterval = 25 * time.Millisecond

func retryRuntimeBindConflict[T any](ctx context.Context, start func() (T, error)) (T, error) {
	deadline := time.Now().Add(runtimeBindRetryTimeout)
	for {
		value, err := start()
		if err == nil || !isRuntimeBindConflict(err) || time.Now().After(deadline) {
			return value, err
		}
		timer := time.NewTimer(runtimeBindRetryInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			var zero T
			return zero, ctx.Err()
		case <-timer.C:
		}
	}
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
	if _, _, err := net.SplitHostPort(address); err != nil && strings.TrimSpace(address) != "" && !strings.Contains(address, ":") {
		address = net.JoinHostPort("", strings.TrimSpace(address))
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
