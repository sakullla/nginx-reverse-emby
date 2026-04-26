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
	configureRelayTransport(interactive, upstream.TrafficClassInteractive, dial)
	configureRelayTransport(bulk, upstream.TrafficClassBulk, dial)
	return interactive, bulk
}

func NewRelayTransport(
	base *http.Transport,
	dial func(context.Context, string, string, upstream.TrafficClass) (net.Conn, error),
) *http.Transport {
	transport := cloneTransport(base)
	configureRelayTransport(transport, upstream.TrafficClassUnknown, dial)
	return transport
}

func configureRelayTransport(
	transport *http.Transport,
	class upstream.TrafficClass,
	dial func(context.Context, string, string, upstream.TrafficClass) (net.Conn, error),
) {
	if transport == nil {
		return
	}
	transport.DialTLS = nil
	transport.DialTLSContext = nil
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		return dialRelayTransportConn(ctx, network, address, class, dial)
	}
}

func dialRelayTransportConn(
	ctx context.Context,
	network string,
	address string,
	class upstream.TrafficClass,
	dial func(context.Context, string, string, upstream.TrafficClass) (net.Conn, error),
) (net.Conn, error) {
	conn, err := dial(ctx, network, address, class)
	if err != nil {
		return nil, err
	}
	if selectedAddress, selectedPath := selectedRelaySelectionFromContext(ctx); selectedAddress != "" {
		return newSelectedRelayConn(conn, selectedAddress, selectedPath), nil
	}
	return conn, nil
}
