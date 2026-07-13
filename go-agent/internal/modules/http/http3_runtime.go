package http

import (
	"context"
	"crypto/tls"
	"errors"
	"log"
	"net"
	"net/http"

	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

type http3ServerHandle struct {
	server    *http3.Server
	packet    net.PacketConn
	transport *quic.Transport
	listener  *quic.Listener
	binding   string
}

var http3ListenPacket = func(network, address string) (net.PacketConn, error) {
	return net.ListenPacket(network, address)
}

var http3ListenQUIC = func(transport *quic.Transport, tlsConfig *tls.Config, config *quic.Config) (*quic.Listener, error) {
	return transport.Listen(tlsConfig, config)
}

func startHTTP3Server(ctx context.Context, handler http.Handler, spec runtimeListenerSpec, provider TLSMaterialProvider) (*http3ServerHandle, error) {
	if _, err := newInboundTLSConfig(ctx, spec, provider); err != nil {
		return nil, err
	}
	packetConn, err := http3ListenPacket("udp", spec.address)
	if err != nil {
		return nil, err
	}
	if tuner, ok := packetConn.(model.UDPBufferTuner); ok {
		model.TuneUDPBuffers(tuner)
	}

	handle, err := startHTTP3ServerOnPacket(ctx, handler, spec, provider, packetConn)
	if err != nil {
		_ = packetConn.Close()
		return nil, err
	}
	return handle, nil
}

func startHTTP3ServerOnPacket(ctx context.Context, handler http.Handler, spec runtimeListenerSpec, provider TLSMaterialProvider, packetConn net.PacketConn) (*http3ServerHandle, error) {
	if packetConn == nil {
		return nil, net.ErrClosed
	}
	tlsConfig, err := newInboundTLSConfig(ctx, spec, provider)
	if err != nil {
		return nil, err
	}
	server := &http3.Server{
		Addr:      spec.address,
		Handler:   handler,
		TLSConfig: http3.ConfigureTLSConfig(tlsConfig),
	}
	transport := &quic.Transport{Conn: packetConn}
	listener, err := http3ListenQUIC(transport, server.TLSConfig, server.QUICConfig)
	if err != nil {
		_ = transport.Close()
		return nil, err
	}
	handle := &http3ServerHandle{
		server:    server,
		packet:    packetConn,
		transport: transport,
		listener:  listener,
		binding:   spec.bindingKey,
	}

	go func() {
		if err := server.ServeListener(listener); err != nil && !errors.Is(err, net.ErrClosed) && !errors.Is(err, http.ErrServerClosed) {
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
		if err := h.server.Close(); err != nil && !errors.Is(err, net.ErrClosed) && !errors.Is(err, http.ErrServerClosed) {
			closeErr = errors.Join(closeErr, err)
		}
	}
	if h.listener != nil {
		if err := h.listener.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			closeErr = errors.Join(closeErr, err)
		}
	}
	if h.transport != nil {
		if err := h.transport.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			closeErr = errors.Join(closeErr, err)
		}
	}
	if h.packet != nil {
		if err := h.packet.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			closeErr = errors.Join(closeErr, err)
		}
	}
	return closeErr
}
