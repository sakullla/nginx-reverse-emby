package traffic

import (
	"io"
	"net"
	"sync"
)

type Direction int

const (
	DirectionRX Direction = iota
	DirectionTX
)

type FlushPolicy int

const (
	FlushAtOrAboveThreshold FlushPolicy = iota
	FlushAtOrBelowThreshold
)

const copyBufferSize = 32 * 1024

var copyBufferPool = sync.Pool{
	New: func() any {
		buf := make([]byte, copyBufferSize)
		return &buf
	},
}

func CopyPreferReaderFrom(dst io.Writer, src io.Reader) (int64, error) {
	if _, trafficWriter := dst.(*TrafficWriter); trafficWriter {
		if wt, ok := src.(io.WriterTo); ok {
			return wt.WriteTo(dst)
		}
	}
	if rf, ok := dst.(io.ReaderFrom); ok {
		return rf.ReadFrom(readerWithoutWriterTo{Reader: src})
	}
	return io.Copy(dst, src)
}

func CopyGeneric(dst io.Writer, src io.Reader) (int64, error) {
	bufPtr := copyBufferPool.Get().(*[]byte)
	defer copyBufferPool.Put(bufPtr)
	return io.CopyBuffer(writerWithoutReaderFrom{Writer: dst}, readerWithoutWriterTo{Reader: src}, *bufPtr)
}

type readerWithoutWriterTo struct {
	io.Reader
}

type writerWithoutReaderFrom struct {
	io.Writer
}

type readerFromWithProgress interface {
	ReadFromWithProgress(io.Reader, func(int64)) (int64, error)
}

type TrafficWriter struct {
	dst       io.Writer
	direction Direction
	recorder  *Recorder
	threshold uint64
	policy    FlushPolicy
	pending   uint64
}

func NewTrafficWriter(dst io.Writer, direction Direction, recorder *Recorder, threshold uint64) *TrafficWriter {
	return &TrafficWriter{dst: dst, direction: direction, recorder: recorder, threshold: threshold, policy: FlushAtOrAboveThreshold}
}

func NewTrafficWriterFlushBelow(dst io.Writer, direction Direction, recorder *Recorder, threshold uint64) *TrafficWriter {
	return &TrafficWriter{dst: dst, direction: direction, recorder: recorder, threshold: threshold, policy: FlushAtOrBelowThreshold}
}

func (w *TrafficWriter) Write(p []byte) (int, error) {
	n, err := w.dst.Write(p)
	if n > 0 && w.recorder != nil {
		w.recordCopied(int64(n))
	}
	return n, err
}

func (w *TrafficWriter) ReadFrom(r io.Reader) (int64, error) {
	if _, ok := w.dst.(*net.TCPConn); ok {
		return CopyGeneric(w, r)
	}
	if rf, ok := w.dst.(readerFromWithProgress); ok {
		return rf.ReadFromWithProgress(r, w.recordCopied)
	}
	if rf, ok := w.dst.(io.ReaderFrom); ok {
		n, err := rf.ReadFrom(r)
		w.recordCopied(n)
		return n, err
	}
	return CopyGeneric(w, r)
}

func (w *TrafficWriter) FlushTraffic() {
	if w == nil || w.recorder == nil {
		return
	}
	if w.policy == FlushAtOrBelowThreshold {
		w.recorder.Flush()
		return
	}
	if w.pending == 0 {
		return
	}
	w.record(w.pending)
	w.recorder.Flush()
	w.pending = 0
}

func (w *TrafficWriter) recordCopied(n int64) {
	if n <= 0 || w.recorder == nil {
		return
	}
	if w.policy == FlushAtOrBelowThreshold {
		w.record(uint64(n))
		w.recorder.FlushIfPendingBelow(w.threshold)
		return
	}
	w.add(uint64(n))
}

func (w *TrafficWriter) add(bytes uint64) {
	w.pending += bytes
	if w.shouldFlush() {
		w.FlushTraffic()
	}
}

func (w *TrafficWriter) record(bytes uint64) {
	if w.direction == DirectionRX {
		w.recorder.Add(int64(bytes), 0)
	} else {
		w.recorder.Add(0, int64(bytes))
	}
}

func (w *TrafficWriter) shouldFlush() bool {
	if w.threshold == 0 {
		return true
	}
	switch w.policy {
	case FlushAtOrBelowThreshold:
		return w.pending <= w.threshold
	default:
		return w.pending >= w.threshold
	}
}

type TrafficReadCloser struct {
	io.ReadCloser
	direction Direction
	recorder  *Recorder
}

func NewTrafficReadCloser(delegate io.ReadCloser, direction Direction, recorder *Recorder) *TrafficReadCloser {
	return &TrafficReadCloser{ReadCloser: delegate, direction: direction, recorder: recorder}
}

func (c *TrafficReadCloser) Read(p []byte) (int, error) {
	n, err := c.ReadCloser.Read(p)
	if n > 0 && c.recorder != nil {
		if c.direction == DirectionRX {
			c.recorder.Add(int64(n), 0)
		} else {
			c.recorder.Add(0, int64(n))
		}
	}
	if err != nil && c.recorder != nil {
		c.recorder.Flush()
	}
	return n, err
}

func (c *TrafficReadCloser) Close() error {
	if c.recorder != nil {
		c.recorder.Flush()
	}
	return c.ReadCloser.Close()
}
