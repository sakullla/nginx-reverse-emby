package relay

import (
	"net"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/traffic"
)

func pipeBothWays(left, right net.Conn, recorder *traffic.Recorder) {
	pipeBothWaysWithInitialRelayRX(left, right, 0, recorder)
}

func pipeBothWaysWithInitialRelayRX(left, right net.Conn, initialRX int64, recorder *traffic.Recorder) {
	done := make(chan struct{}, 2)
	recorder = relayRecorderOrAggregate(recorder)
	recorder.Add(initialRX, 0)
	recorder.Flush()

	go func() {
		_, _ = copyRelayTraffic(right, left, true, recorder)
		closeWrite(right)
		closeRead(left)
		done <- struct{}{}
	}()

	go func() {
		_, _ = copyRelayTraffic(left, right, false, recorder)
		closeWrite(left)
		closeRead(right)
		done <- struct{}{}
	}()

	<-done
	<-done
	recorder.Flush()
}

func relayRecorderOrAggregate(recorder *traffic.Recorder) *traffic.Recorder {
	if recorder != nil {
		return recorder
	}
	return traffic.NewRelayRecorder()
}

func closeWrite(conn net.Conn) {
	if conn == nil {
		return
	}
	if closer, ok := conn.(interface{ CloseWrite() error }); ok {
		_ = closer.CloseWrite()
		return
	}
	_ = conn.Close()
}

func closeRead(conn net.Conn) {
	if conn == nil {
		return
	}
	if closer, ok := conn.(interface{ CloseRead() error }); ok {
		_ = closer.CloseRead()
	}
}
