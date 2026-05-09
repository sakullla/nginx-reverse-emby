package netutil

import (
	"net"
	"net/url"
	"strconv"
	"strings"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

func NormalizeHost(value string) string {
	host := strings.TrimSpace(value)
	if host == "" {
		return ""
	}
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	return strings.ToLower(strings.Trim(host, "[]"))
}

func DefaultPort(scheme string) int {
	switch strings.ToLower(strings.TrimSpace(scheme)) {
	case "https":
		return 443
	default:
		return 80
	}
}

func DefaultPortString(scheme string) string {
	port := DefaultPort(scheme)
	if port <= 0 {
		return ""
	}
	return strconv.Itoa(port)
}

func URLAuthority(target *url.URL) string {
	if target == nil {
		return ""
	}
	host := NormalizeHost(target.Hostname())
	if host == "" {
		return ""
	}
	port := target.Port()
	if port == "" {
		port = DefaultPortString(target.Scheme)
	}
	if port == "" {
		return host
	}
	return net.JoinHostPort(host, port)
}

func PortWithDefault(target *url.URL) int {
	if target == nil {
		return 0
	}
	if target.Port() != "" {
		port, _ := strconv.Atoi(target.Port())
		return port
	}
	return DefaultPort(target.Scheme)
}

func AddressWithDefaultPort(target *url.URL) string {
	if target == nil {
		return ""
	}
	if target.Port() != "" {
		return target.Host
	}
	return net.JoinHostPort(target.Hostname(), strconv.Itoa(DefaultPort(target.Scheme)))
}

func ClientIP(remoteAddr string) string {
	if remoteAddr == "" {
		return ""
	}
	if host, _, err := net.SplitHostPort(remoteAddr); err == nil {
		return host
	}
	return remoteAddr
}

func RelayListenerDialEndpoint(listener model.RelayListener) (string, int) {
	host := strings.TrimSpace(listener.PublicHost)
	if host == "" {
		for _, bindHost := range listener.BindHosts {
			if trimmed := strings.TrimSpace(bindHost); trimmed != "" {
				host = trimmed
				break
			}
		}
	}
	if host == "" {
		host = strings.TrimSpace(listener.ListenHost)
	}

	port := listener.PublicPort
	if port <= 0 {
		port = listener.ListenPort
	}
	return host, port
}
