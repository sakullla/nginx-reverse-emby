package relay

import (
	"io"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/stream"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/traffic"
)

const relayTrafficFlushThreshold uint64 = 32 * 1024

func copyPreferReaderFrom(dst io.Writer, src io.Reader) (int64, error) {
	return stream.CopyPreferReaderFrom(dst, src)
}

func copyGeneric(dst io.Writer, src io.Reader) (int64, error) {
	return stream.CopyGeneric(dst, src)
}

func copyRelayTraffic(dst io.Writer, src io.Reader, rxDirection bool, recorder *traffic.Recorder) (int64, error) {
	direction := stream.DirectionTX
	if rxDirection {
		direction = stream.DirectionRX
	}
	writer := stream.NewTrafficWriterFlushBelow(dst, direction, recorder, relayTrafficFlushThreshold)
	var n int64
	var err error
	if shouldUseRelayDestinationReadFrom(dst) {
		n, err = writer.ReadFrom(src)
	} else {
		n, err = stream.CopyPreferReaderFrom(writer, src)
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
