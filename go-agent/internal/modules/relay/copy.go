package relay

import (
	"io"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/traffic"
)

const relayTrafficFlushThreshold uint64 = 32 * 1024

func copyPreferReaderFrom(dst io.Writer, src io.Reader) (int64, error) {
	return traffic.CopyPreferReaderFrom(dst, src)
}

func copyGeneric(dst io.Writer, src io.Reader) (int64, error) {
	return traffic.CopyGeneric(dst, src)
}

func copyRelayTraffic(dst io.Writer, src io.Reader, rxDirection bool, recorder *traffic.Recorder) (int64, error) {
	direction := traffic.DirectionTX
	if rxDirection {
		direction = traffic.DirectionRX
	}
	writer := traffic.NewTrafficWriterFlushBelow(dst, direction, recorder, relayTrafficFlushThreshold)
	var n int64
	var err error
	if shouldUseRelayDestinationReadFrom(dst) {
		n, err = writer.ReadFrom(src)
	} else {
		n, err = traffic.CopyPreferReaderFrom(writer, src)
	}
	writer.FlushTraffic()
	return n, err
}

func shouldUseRelayDestinationReadFrom(dst io.Writer) bool {
	switch conn := dst.(type) {
	case *tlsTCPLogicalStream:
		return true
	case *idleDeadlineConn:
		_, ok := conn.Conn.(*tlsTCPLogicalStream)
		return ok
	default:
		return false
	}
}
