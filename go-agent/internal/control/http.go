package control

import (
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/config"
)

type HTTPTransportConfig = config.HTTPTransportConfig

func normalizeMasterBaseURL(raw string) string {
	trimmed := strings.TrimRight(strings.TrimSpace(raw), "/")
	switch {
	case strings.HasSuffix(trimmed, "/panel-api"):
		return strings.TrimSuffix(trimmed, "/panel-api")
	case strings.HasSuffix(trimmed, "/api"):
		return strings.TrimSuffix(trimmed, "/api")
	default:
		return trimmed
	}
}

func resolvedHTTPTransportConfig(cfg config.HTTPTransportConfig) config.HTTPTransportConfig {
	transportCfg := config.Default().HTTPTransport
	if cfg.DialTimeout > 0 {
		transportCfg.DialTimeout = cfg.DialTimeout
	}
	if cfg.TLSHandshakeTimeout > 0 {
		transportCfg.TLSHandshakeTimeout = cfg.TLSHandshakeTimeout
	}
	if cfg.ResponseHeaderTimeout > 0 {
		transportCfg.ResponseHeaderTimeout = cfg.ResponseHeaderTimeout
	}
	if cfg.IdleConnTimeout > 0 {
		transportCfg.IdleConnTimeout = cfg.IdleConnTimeout
	}
	if cfg.KeepAlive > 0 {
		transportCfg.KeepAlive = cfg.KeepAlive
	}
	return transportCfg
}

func newHTTPTransport(cfg config.HTTPTransportConfig) *http.Transport {
	transportCfg := resolvedHTTPTransportConfig(cfg)
	return &http.Transport{
		DialContext: (&net.Dialer{
			Timeout:   transportCfg.DialTimeout,
			KeepAlive: transportCfg.KeepAlive,
		}).DialContext,
		TLSHandshakeTimeout:   transportCfg.TLSHandshakeTimeout,
		ResponseHeaderTimeout: transportCfg.ResponseHeaderTimeout,
		IdleConnTimeout:       transportCfg.IdleConnTimeout,
		ExpectContinueTimeout: 1 * time.Second,
		ForceAttemptHTTP2:     true,
	}
}
