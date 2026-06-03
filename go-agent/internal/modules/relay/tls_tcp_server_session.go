package relay

import (
	"context"
	"fmt"
	"io"
	"net"
	"strings"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/traffic"
)

type serverTLSTCPSession struct {
	tunnel *tlsTCPTunnel
	server *Server
}

func newServerTLSTCPSession(conn net.Conn) *serverTLSTCPSession {
	tunnel := &tlsTCPTunnel{
		key:        "server",
		rawConn:    conn,
		reader:     conn,
		writer:     conn,
		closeOuter: conn.Close,
		streams:    make(map[uint32]*tlsTCPLogicalStream),
		closed:     make(chan struct{}),
	}
	return &serverTLSTCPSession{tunnel: tunnel}
}

func (s *Server) handleMuxTLSTCPConn(clientConn net.Conn, listener Listener) {
	session := newServerTLSTCPSession(clientConn)
	session.server = s
	session.run(listener)
}

func (s *serverTLSTCPSession) run(listener Listener) {
	for {
		frame, err := readMuxFrame(s.tunnel.reader)
		if err != nil {
			_ = s.tunnel.close()
			return
		}

		switch frame.Type {
		case muxFrameTypeOpen:
			request, err := readMuxOpenPayload(frame.Payload)
			frame.releasePayload()
			if err != nil {
				_ = s.writeOpenResult(frame.StreamID, muxOpenResult{OK: false, Error: err.Error()})
				continue
			}
			if !strings.EqualFold(request.Kind, "tcp") && !strings.EqualFold(request.Kind, "udp") && !strings.EqualFold(request.Kind, "resolve") && !strings.EqualFold(request.Kind, relayOpenKindProbe) {
				_ = s.writeOpenResult(frame.StreamID, muxOpenResult{OK: false, Error: fmt.Sprintf("unsupported network %q", request.Kind)})
				continue
			}
			if state := s.server.currentTrafficBlockState(); state.Blocked && (strings.EqualFold(request.Kind, "tcp") || strings.EqualFold(request.Kind, "udp")) {
				_ = s.writeOpenResult(frame.StreamID, muxOpenResult{OK: false, Error: trafficBlockErrorMessage(state)})
				continue
			}

			stream := &tlsTCPLogicalStream{
				tunnel:       s.tunnel,
				streamID:     frame.StreamID,
				readCh:       make(chan struct{}, 1),
				openResultCh: make(chan muxOpenResult, 1),
			}
			s.tunnel.registerStream(stream)
			go s.handleStream(listener, stream, request)
		case muxFrameTypeData:
			if stream := s.tunnel.getStream(frame.StreamID); stream != nil {
				stream.appendDataChunk(frame.takeReadChunk())
			} else {
				frame.releasePayload()
			}
		case muxFrameTypeFin:
			frame.releasePayload()
			if stream := s.tunnel.getStream(frame.StreamID); stream != nil {
				stream.setReadError(io.EOF)
				s.tunnel.removeStream(frame.StreamID)
			}
		case muxFrameTypeRst:
			frame.releasePayload()
			if stream := s.tunnel.getStream(frame.StreamID); stream != nil {
				stream.setReadError(io.ErrClosedPipe)
				s.tunnel.removeStream(frame.StreamID)
			}
		default:
			frame.releasePayload()
		}
	}
}

func (s *serverTLSTCPSession) handleStream(listener Listener, stream *tlsTCPLogicalStream, request relayOpenFrame) {
	options := relayDialOptionsFromMetadata(request.Kind, request.Metadata)
	if strings.EqualFold(request.Kind, relayOpenKindProbe) {
		timings, err := s.server.probeRelayPath(s.server.ctx, relayProbeNetworkFromMetadata(request.Metadata), request.Target, request.Chain, options)
		if err != nil {
			_ = s.writeOpenResult(stream.streamID, muxOpenResult{OK: false, Error: err.Error()})
			s.tunnel.removeStream(stream.streamID)
			return
		}
		_ = s.writeOpenResult(stream.streamID, muxOpenResult{OK: true, ProbeTimings: timings})
		s.tunnel.removeStream(stream.streamID)
		return
	}
	if strings.EqualFold(request.Kind, "resolve") {
		resolvedCandidates, err := s.server.resolveTargetCandidates(request.Target, request.Chain)
		if err != nil {
			_ = s.writeOpenResult(stream.streamID, muxOpenResult{OK: false, Error: err.Error()})
			s.tunnel.removeStream(stream.streamID)
			return
		}
		_ = s.writeOpenResult(stream.streamID, muxOpenResult{OK: true, ResolvedCandidates: resolvedCandidates})
		s.tunnel.removeStream(stream.streamID)
		return
	}
	if strings.EqualFold(request.Kind, "udp") {
		s.handleUDPStream(listener, stream, request)
		return
	}

	upstream, upstreamResult, err := s.server.openUpstreamWithResult(request.Kind, request.Target, request.Chain, options)
	if err != nil {
		_ = s.writeOpenResult(stream.streamID, muxOpenResult{OK: false, Error: err.Error(), SelectedAddress: upstreamResult.SelectedAddress})
		s.tunnel.removeStream(stream.streamID)
		return
	}
	s.server.trackConn(upstream)
	defer s.server.untrackConn(upstream)
	defer upstream.Close()

	if len(request.InitialData) > 0 {
		n, err := upstream.Write(request.InitialData)
		if err != nil {
			_ = s.writeOpenResult(stream.streamID, muxOpenResult{OK: false, Error: err.Error()})
			s.tunnel.removeStream(stream.streamID)
			return
		}
		if n != len(request.InitialData) {
			_ = s.writeOpenResult(stream.streamID, muxOpenResult{OK: false, Error: io.ErrShortWrite.Error()})
			s.tunnel.removeStream(stream.streamID)
			return
		}
	}
	if err := s.writeOpenResult(stream.streamID, muxOpenResult{
		OK:              true,
		SelectedAddress: upstreamResult.SelectedAddress,
	}); err != nil {
		s.tunnel.removeStream(stream.streamID)
		return
	}

	recorder := traffic.NewRelayListenerRecorder(listener.ID)
	pipeBothWaysWithInitialRelayRX(wrapIdleConn(stream), wrapIdleConn(upstream), int64(len(request.InitialData)), recorder)
	s.tunnel.removeStream(stream.streamID)
}

func (s *serverTLSTCPSession) handleUDPStream(listener Listener, stream *tlsTCPLogicalStream, request relayOpenFrame) {
	upstream, selectedAddress, err := s.server.openUDPPeerWithResultOptions(
		request.Target,
		request.Chain,
		relayDialOptionsFromMetadata(request.Kind, request.Metadata),
	)
	if err != nil {
		_ = s.writeOpenResult(stream.streamID, muxOpenResult{OK: false, Error: err.Error()})
		s.tunnel.removeStream(stream.streamID)
		return
	}
	defer upstream.Close()

	if err := s.writeOpenResult(stream.streamID, muxOpenResult{OK: true, SelectedAddress: selectedAddress}); err != nil {
		s.tunnel.removeStream(stream.streamID)
		return
	}

	pipeUDPPackets(stream, upstream, traffic.NewRelayListenerRecorder(listener.ID))
	s.tunnel.removeStream(stream.streamID)
}

func (s *serverTLSTCPSession) writeOpenResult(streamID uint32, result muxOpenResult) error {
	payload, err := marshalMuxOpenResultPayload(result)
	if err != nil {
		return err
	}
	return s.tunnel.writeFrame(context.Background(), muxFrame{
		Type:     muxFrameTypeOpenResult,
		StreamID: streamID,
		Payload:  payload,
	})
}
