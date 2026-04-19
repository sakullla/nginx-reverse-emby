package relay

import (
	"bytes"
	"errors"
	"io"
	"net"
	"sync"
	"testing"
	"time"
)

func TestTLSTCPLogicalStreamReadConsumesQueuedChunksInOrder(t *testing.T) {
	stream := &tlsTCPLogicalStream{readCh: make(chan struct{}, 1)}
	stream.appendData([]byte("hello"))
	stream.appendData([]byte("world"))
	if got := len(stream.readChunks); got != 2 {
		t.Fatalf("len(readChunks) = %d, want 2", got)
	}
	if stream.readOffset != 0 {
		t.Fatalf("readOffset = %d, want 0", stream.readOffset)
	}

	buf := make([]byte, 7)
	n, err := stream.Read(buf)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if got := string(buf[:n]); got != "hellowo" {
		t.Fatalf("Read() = %q, want %q", got, "hellowo")
	}
	if got := len(stream.readChunks); got != 1 {
		t.Fatalf("len(readChunks) after first read = %d, want 1", got)
	}
	if stream.readOffset != 2 {
		t.Fatalf("readOffset after first read = %d, want 2", stream.readOffset)
	}

	buf = make([]byte, 3)
	n, err = stream.Read(buf)
	if err != nil {
		t.Fatalf("Read() second error = %v", err)
	}
	if got := string(buf[:n]); got != "rld" {
		t.Fatalf("Read() second = %q, want %q", got, "rld")
	}
	if got := len(stream.readChunks); got != 0 {
		t.Fatalf("len(readChunks) after second read = %d, want 0", got)
	}
	if stream.readOffset != 0 {
		t.Fatalf("readOffset after second read = %d, want 0", stream.readOffset)
	}
}

func TestTLSTCPLogicalStreamReadReturnsQueuedDataBeforeEOF(t *testing.T) {
	stream := &tlsTCPLogicalStream{readCh: make(chan struct{}, 1)}
	stream.appendData([]byte("payload"))
	stream.setReadError(io.EOF)

	buf := make([]byte, 7)
	n, err := stream.Read(buf)
	if err != nil {
		t.Fatalf("Read() first error = %v", err)
	}
	if got := string(buf[:n]); got != "payload" {
		t.Fatalf("Read() first = %q, want %q", got, "payload")
	}

	n, err = stream.Read(buf)
	if !errors.Is(err, io.EOF) {
		t.Fatalf("Read() second error = %v, want EOF", err)
	}
	if n != 0 {
		t.Fatalf("Read() second n = %d, want 0", n)
	}
}

func TestTLSTCPLogicalStreamReadDoesNotReturnZeroNilForEmptyDataFrame(t *testing.T) {
	stream := &tlsTCPLogicalStream{readCh: make(chan struct{}, 1)}
	stream.appendData(nil)
	stream.setReadError(io.EOF)

	buf := make([]byte, 1)
	n, err := stream.Read(buf)
	if !errors.Is(err, io.EOF) {
		t.Fatalf("Read() error = %v, want EOF", err)
	}
	if n != 0 {
		t.Fatalf("Read() n = %d, want 0", n)
	}
}

func TestTLSTCPLogicalStreamReadFromSplitsLargePayloadIntoMuxFrames(t *testing.T) {
	var wire bytes.Buffer
	tunnel := &tlsTCPTunnel{
		rawConn:    noopDeadlineConn{},
		writer:     &wire,
		closeOuter: func() error { return nil },
		streams:    make(map[uint32]*tlsTCPLogicalStream),
		closed:     make(chan struct{}),
	}
	stream := &tlsTCPLogicalStream{
		tunnel:       tunnel,
		streamID:     7,
		readCh:       make(chan struct{}, 1),
		openResultCh: make(chan error, 1),
	}
	src := bytes.NewReader(bytes.Repeat([]byte("a"), 150000))

	n, err := stream.ReadFrom(src)
	if err != nil {
		t.Fatalf("ReadFrom() error = %v", err)
	}
	if n != 150000 {
		t.Fatalf("ReadFrom() = %d, want %d", n, 150000)
	}

	frameReader := bytes.NewReader(wire.Bytes())
	frames := 0
	var payload bytes.Buffer
	for frameReader.Len() > 0 {
		frame, err := readMuxFrame(frameReader)
		if err != nil {
			t.Fatalf("readMuxFrame() error = %v", err)
		}
		if frame.Type != muxFrameTypeData {
			t.Fatalf("frame.Type = %v, want %v", frame.Type, muxFrameTypeData)
		}
		frames++
		payload.Write(frame.Payload)
	}
	if frames < 2 {
		t.Fatalf("data frame count = %d, want at least 2", frames)
	}
	if got := payload.Len(); got != 150000 {
		t.Fatalf("payload len = %d, want %d", got, 150000)
	}
}

func TestTLSTCPLogicalStreamWriteToDrainsQueuedChunks(t *testing.T) {
	stream := &tlsTCPLogicalStream{readCh: make(chan struct{}, 1)}
	stream.appendData([]byte("hello"))
	stream.appendData([]byte("world"))
	stream.setReadError(io.EOF)

	var dst bytes.Buffer
	n, err := stream.WriteTo(&dst)
	if err != nil {
		t.Fatalf("WriteTo() error = %v", err)
	}
	if n != int64(len("helloworld")) {
		t.Fatalf("WriteTo() = %d, want %d", n, len("helloworld"))
	}
	if got := dst.String(); got != "helloworld" {
		t.Fatalf("WriteTo() payload = %q, want %q", got, "helloworld")
	}
}

func TestTLSTCPLogicalStreamWriteToDoesNotHoldReadMuWhileWriting(t *testing.T) {
	stream := &tlsTCPLogicalStream{
		tunnel: &tlsTCPTunnel{
			closed: make(chan struct{}),
		},
		readCh: make(chan struct{}, 1),
	}
	stream.appendData([]byte("blocked"))
	writer := newBlockingFirstWrite()
	done := make(chan error, 1)

	go func() {
		_, err := stream.WriteTo(writer)
		done <- err
	}()

	<-writer.started
	appendDone := make(chan struct{})
	go func() {
		stream.appendData([]byte("next"))
		close(appendDone)
	}()

	select {
	case <-appendDone:
	case <-time.After(100 * time.Millisecond):
		close(writer.release)
		<-appendDone
		stream.setReadError(io.EOF)
		<-done
		t.Fatal("appendData blocked while WriteTo was writing to a slow destination")
	}

	stream.setReadError(io.EOF)
	close(writer.release)
	if err := <-done; err != nil {
		t.Fatalf("WriteTo() error = %v", err)
	}
	if got := writer.String(); got != "blockednext" {
		t.Fatalf("WriteTo() payload = %q, want %q", got, "blockednext")
	}
}

func TestWrapIdleConnPreservesTLSTCPBulkInterfaces(t *testing.T) {
	stream := &tlsTCPLogicalStream{readCh: make(chan struct{}, 1)}
	wrapped := wrapIdleConn(stream)

	if _, ok := wrapped.(io.ReaderFrom); !ok {
		t.Fatalf("wrapped tls tcp stream does not implement io.ReaderFrom")
	}
	if _, ok := wrapped.(io.WriterTo); !ok {
		t.Fatalf("wrapped tls tcp stream does not implement io.WriterTo")
	}
}

type noopDeadlineConn struct{ net.Conn }

func (noopDeadlineConn) SetDeadline(time.Time) error      { return nil }
func (noopDeadlineConn) SetReadDeadline(time.Time) error  { return nil }
func (noopDeadlineConn) SetWriteDeadline(time.Time) error { return nil }

type blockingFirstWrite struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
	mu      sync.Mutex
	buf     bytes.Buffer
}

func newBlockingFirstWrite() *blockingFirstWrite {
	return &blockingFirstWrite{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (w *blockingFirstWrite) Write(p []byte) (int, error) {
	blocked := false
	w.once.Do(func() {
		close(w.started)
		<-w.release
		blocked = true
	})
	if !blocked {
		select {
		case <-w.release:
		default:
		}
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.Write(p)
}

func (w *blockingFirstWrite) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}
