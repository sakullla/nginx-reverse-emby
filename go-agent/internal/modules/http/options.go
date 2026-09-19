package http

import (
	"context"
	"net"
	"net/http"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

type TransportOptions struct {
	DialTimeout           time.Duration
	TLSHandshakeTimeout   time.Duration
	ResponseHeaderTimeout time.Duration
	IdleConnTimeout       time.Duration
	KeepAlive             time.Duration
	MaxConnsPerHost       int
	DisableHTTP2          bool
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
	if options.DisableHTTP2 {
		// A custom DialContext conservatively disables HTTP/2 by default, but
		// make the intent explicit and also clear any previously installed
		// alternate protocol handler when a transport is reused in tests or by
		// an embedding caller.
		transport.ForceAttemptHTTP2 = false
		transport.TLSNextProto = nil
		if transport.Protocols != nil {
			protocols := *transport.Protocols
			protocols.SetHTTP1(true)
			protocols.SetHTTP2(false)
			protocols.SetUnencryptedHTTP2(false)
			transport.Protocols = &protocols
		}
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

const (
	interactiveClassMaxConnsPerHost = 16
	bulkClassMaxConnsPerHost        = 64
)

func NewClassedDirectTransports(base *http.Transport) (*http.Transport, *http.Transport) {
	interactive := cloneTransport(base)
	bulk := cloneTransport(base)

	ApplyTransportOptions(interactive, TransportOptions{MaxConnsPerHost: classedMaxConnsPerHost(base, interactiveClassMaxConnsPerHost)})
	ApplyTransportOptions(bulk, TransportOptions{MaxConnsPerHost: classedMaxConnsPerHost(base, bulkClassMaxConnsPerHost)})
	return interactive, bulk
}

// classedMaxConnsPerHost keeps a class's default ceiling unless the shared
// transport was configured with a tighter cap. A configured value above every
// class default is an explicit capacity raise and applies to both classes:
// keeping the class ceilings would silently make the raise a no-op.
func classedMaxConnsPerHost(base *http.Transport, classDefault int) int {
	if base == nil || base.MaxConnsPerHost <= 0 {
		return classDefault
	}
	if base.MaxConnsPerHost > bulkClassMaxConnsPerHost || base.MaxConnsPerHost < classDefault {
		return base.MaxConnsPerHost
	}
	return classDefault
}

func NewClassedRelayTransports(
	base *http.Transport,
	dial func(context.Context, string, string, model.TrafficClass) (net.Conn, error),
) (*http.Transport, *http.Transport) {
	interactive, bulk := NewClassedDirectTransports(base)
	configureRelayTransport(interactive, model.TrafficClassInteractive, dial)
	configureRelayTransport(bulk, model.TrafficClassBulk, dial)
	return interactive, bulk
}

func NewRelayTransport(
	base *http.Transport,
	dial func(context.Context, string, string, model.TrafficClass) (net.Conn, error),
) *http.Transport {
	transport := cloneTransport(base)
	configureRelayTransport(transport, model.TrafficClassUnknown, dial)
	return transport
}

func configureRelayTransport(
	transport *http.Transport,
	class model.TrafficClass,
	dial func(context.Context, string, string, model.TrafficClass) (net.Conn, error),
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
	class model.TrafficClass,
	dial func(context.Context, string, string, model.TrafficClass) (net.Conn, error),
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
