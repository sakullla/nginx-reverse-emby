package proxy

import (
	"context"
	"errors"
	"log"
	"net"
	"net/http"

	"github.com/quic-go/quic-go/http3"
)

type http3ServerHandle struct {
	server  *http3.Server
	packet  net.PacketConn
	binding string
}

var http3ListenPacket = func(network, address string) (net.PacketConn, error) {
	return net.ListenPacket(network, address)
}

func startHTTP3Server(ctx context.Context, handler http.Handler, spec runtimeListenerSpec, provider TLSMaterialProvider) (*http3ServerHandle, error) {
	tlsConfig, err := newInboundTLSConfig(ctx, spec, provider)
	if err != nil {
		return nil, err
	}

	packetConn, err := http3ListenPacket("udp", spec.address)
	if err != nil {
		return nil, err
	}

	server := &http3.Server{
		Addr:      spec.address,
		Handler:   handler,
		TLSConfig: http3.ConfigureTLSConfig(tlsConfig),
	}
	handle := &http3ServerHandle{
		server:  server,
		packet:  packetConn,
		binding: spec.bindingKey,
	}

	go func() {
		if err := server.Serve(packetConn); err != nil && !errors.Is(err, net.ErrClosed) {
			log.Printf("[proxy] http3 serve error on %s: %v", spec.bindingKey, err)
		}
	}()

	return handle, nil
}

func (h *http3ServerHandle) Close() error {
	if h == nil {
		return nil
	}

	var closeErr error
	if h.server != nil {
		if err := h.server.Close(); err != nil && !errors.Is(err, net.ErrClosed) && closeErr == nil {
			closeErr = err
		}
	}
	if h.packet != nil {
		if err := h.packet.Close(); err != nil && !errors.Is(err, net.ErrClosed) && closeErr == nil {
			closeErr = err
		}
	}
	return closeErr
}
