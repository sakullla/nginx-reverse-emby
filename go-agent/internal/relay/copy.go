package relay

import (
	"io"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/traffic"
)

func copyPreferReaderFrom(dst io.Writer, src io.Reader) (int64, error) {
	if rf, ok := dst.(io.ReaderFrom); ok {
		return rf.ReadFrom(readerWithoutWriterTo{Reader: src})
	}
	return io.Copy(dst, src)
}

type readerWithoutWriterTo struct {
	io.Reader
}

func copyGeneric(dst io.Writer, src io.Reader) (int64, error) {
	return io.Copy(writerWithoutReaderFrom{Writer: dst}, readerWithoutWriterTo{Reader: src})
}

type writerWithoutReaderFrom struct {
	io.Writer
}

func copyRelayTraffic(dst io.Writer, src io.Reader, rxDirection bool, recorder *traffic.Recorder) (int64, error) {
	wrapped := relayTrafficWriter{
		dst:         dst,
		rxDirection: rxDirection,
		recorder:    recorder,
	}
	return copyGeneric(wrapped, src)
}

type relayTrafficWriter struct {
	dst         io.Writer
	rxDirection bool
	recorder    *traffic.Recorder
}

func (w relayTrafficWriter) Write(p []byte) (int, error) {
	n, err := w.dst.Write(p)
	if n > 0 && w.recorder != nil {
		if w.rxDirection {
			w.recorder.Add(int64(n), 0)
		} else {
			w.recorder.Add(0, int64(n))
		}
		w.recorder.Flush()
	}
	return n, err
}
