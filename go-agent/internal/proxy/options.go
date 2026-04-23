package proxy

import (
	"context"
	"net"
	"net/http"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/upstream"
)

type TransportOptions struct {
	DialTimeout           time.Duration
	TLSHandshakeTimeout   time.Duration
	ResponseHeaderTimeout time.Duration
	IdleConnTimeout       time.Duration
	KeepAlive             time.Duration
	MaxConnsPerHost       int
}

type StreamResilienceOptions struct {
	ResumeEnabled            bool
	ResumeMaxAttempts        int
	SameBackendRetryAttempts int
}

func ApplyTransportOptions(transport *http.Transport, options TransportOptions) {
	if transport == nil {
		return
	}

	if options.TLSHandshakeTimeout > 0 {
		transport.TLSHandshakeTimeout = options.TLSHandshakeTimeout
	}
	if options.ResponseHeaderTimeout > 0 {
		transport.ResponseHeaderTimeout = options.ResponseHeaderTimeout
	}
	if options.IdleConnTimeout > 0 {
		transport.IdleConnTimeout = options.IdleConnTimeout
	}
	if options.MaxConnsPerHost > 0 {
		transport.MaxConnsPerHost = options.MaxConnsPerHost
	}

	if options.DialTimeout <= 0 && options.KeepAlive <= 0 {
		return
	}

	dialer := &net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
	}
	if options.DialTimeout > 0 {
		dialer.Timeout = options.DialTimeout
	}
	if options.KeepAlive > 0 {
		dialer.KeepAlive = options.KeepAlive
	}
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		return dialer.DialContext(ctx, network, dialAddressFromContext(ctx, address))
	}
}

func NewClassedDirectTransports(base *http.Transport) (*http.Transport, *http.Transport) {
	interactive := cloneTransport(base)
	bulk := cloneTransport(base)

	ApplyTransportOptions(interactive, TransportOptions{MaxConnsPerHost: 16})
	ApplyTransportOptions(bulk, TransportOptions{MaxConnsPerHost: 64})
	return interactive, bulk
}

func NewClassedRelayTransports(
	base *http.Transport,
	dial func(context.Context, string, string, upstream.TrafficClass) (net.Conn, error),
) (*http.Transport, *http.Transport) {
	interactive, bulk := NewClassedDirectTransports(base)
	interactive.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		return dial(ctx, network, address, upstream.TrafficClassInteractive)
	}
	bulk.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		return dial(ctx, network, address, upstream.TrafficClassBulk)
	}
	return interactive, bulk
}

func NewRelayTransport(
	base *http.Transport,
	dial func(context.Context, string, string, upstream.TrafficClass) (net.Conn, error),
) *http.Transport {
	transport := cloneTransport(base)
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		return dial(ctx, network, address, upstream.TrafficClassUnknown)
	}
	return transport
}
