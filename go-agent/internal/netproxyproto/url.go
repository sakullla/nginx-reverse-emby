package proxyproto

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
)

type ProxyURL struct {
	Scheme       string
	Address      string
	Username     string
	Password     string
	RemoteDNS    bool
	SOCKSVersion int
	HTTPConnect  bool
	Original     string
}

func ParseProxyURL(raw string) (ProxyURL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return ProxyURL{}, fmt.Errorf("parse proxy URL: %w", err)
	}
	if u.Scheme == "" {
		return ProxyURL{}, fmt.Errorf("proxy URL missing scheme")
	}
	if u.Host == "" {
		return ProxyURL{}, fmt.Errorf("proxy URL missing host")
	}

	proxy := ProxyURL{
		Scheme:   strings.ToLower(u.Scheme),
		Address:  u.Host,
		Original: raw,
	}

	switch proxy.Scheme {
	case "socks":
		proxy.SOCKSVersion = 5
	case "socks4":
		proxy.SOCKSVersion = 4
	case "socks4a":
		proxy.SOCKSVersion = 4
		proxy.RemoteDNS = true
	case "socks5":
		proxy.SOCKSVersion = 5
	case "socks5h":
		proxy.SOCKSVersion = 5
		proxy.RemoteDNS = true
	case "http":
		proxy.HTTPConnect = true
	default:
		return ProxyURL{}, fmt.Errorf("unsupported proxy URL scheme %q", u.Scheme)
	}

	host, port, err := net.SplitHostPort(u.Host)
	if err != nil {
		return ProxyURL{}, fmt.Errorf("proxy URL must include host and port: %w", err)
	}
	if host == "" {
		return ProxyURL{}, fmt.Errorf("proxy URL missing host")
	}
	if err := validatePort(port); err != nil {
		return ProxyURL{}, err
	}

	if u.User != nil {
		proxy.Username = u.User.Username()
		proxy.Password, _ = u.User.Password()
	}

	return proxy, nil
}

func RedactProxyURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.User == nil {
		return raw
	}

	password, hasPassword := u.User.Password()
	if !hasPassword || password == "" {
		return raw
	}

	u.User = url.UserPassword(u.User.Username(), "xxxxx")
	return u.String()
}

func validatePort(port string) error {
	if port == "" {
		return fmt.Errorf("proxy URL missing port")
	}
	n, err := strconv.Atoi(port)
	if err != nil {
		return fmt.Errorf("proxy URL port must be numeric: %w", err)
	}
	if n < 1 || n > 65535 {
		return fmt.Errorf("proxy URL port out of range")
	}
	return nil
}
