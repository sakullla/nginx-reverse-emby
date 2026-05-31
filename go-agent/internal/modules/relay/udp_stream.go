package relay

import (
	"net"
	"strings"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/traffic"
)

func listenerUsesEarlyWindowMask(listener Listener) bool {
	return normalizeListenerTransportModeValue(listener.TransportMode) == ListenerTransportModeTLSTCP &&
		strings.EqualFold(strings.TrimSpace(listener.ObfsMode), RelayObfsModeEarlyWindowV2)
}

func (s *Server) handleUDPRelayStream(clientConn net.Conn, listener Listener, target string, chain []Hop, options DialOptions) {
	upstream, selectedAddress, err := s.openUDPPeerWithResultOptions(target, chain, options)
	if err != nil {
		_ = withFrameDeadline(clientConn, func() error {
			return writeRelayResponse(clientConn, relayResponse{OK: false, Error: err.Error()})
		})
		return
	}
	defer upstream.Close()

	if err := withFrameDeadline(clientConn, func() error {
		return writeRelayResponse(clientConn, relayResponse{OK: true, SelectedAddress: selectedAddress})
	}); err != nil {
		return
	}

	relayClientConn := clientConn
	if listenerUsesEarlyWindowMask(listener) {
		relayClientConn = wrapConnWithEarlyWindowMask(clientConn, defaultEarlyWindowMaskConfig())
	}

	pipeUDPPackets(relayClientConn, upstream, traffic.NewRelayListenerRecorder(listener.ID))
}

func pipeUDPPackets(clientConn net.Conn, upstream udpPacketPeer, recorder *traffic.Recorder) {
	done := make(chan struct{}, 2)
	recorder = relayRecorderOrAggregate(recorder)

	go func() {
		defer upstream.Close()
		buf := make([]byte, maxUOTPacketSize)
		for {
			payload, err := readUOTPacketInto(clientConn, buf)
			if err != nil {
				done <- struct{}{}
				return
			}
			if err := upstream.WritePacket(payload); err != nil {
				done <- struct{}{}
				return
			}
			recorder.Add(int64(len(payload)), 0)
			recorder.FlushIfPendingBelow(32 * 1024)
		}
	}()

	go func() {
		defer clientConn.Close()
		for {
			payload, err := upstream.ReadPacket()
			if err != nil {
				done <- struct{}{}
				return
			}
			if err := writeUOTPacket(clientConn, payload); err != nil {
				done <- struct{}{}
				return
			}
			recorder.Add(0, int64(len(payload)))
			recorder.FlushIfPendingBelow(32 * 1024)
		}
	}()

	<-done
	<-done
	recorder.Flush()
}
