package proxy

import (
	"context"
	"net"
	"net/http"
	"time"
)

type TransportOptions struct {
	DialTimeout           time.Duration
	TLSHandshakeTimeout   time.Duration
	ResponseHeaderTimeout time.Duration
	IdleConnTimeout       time.Duration
	KeepAlive             time.Duration
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
