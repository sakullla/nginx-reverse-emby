package relay

import (
	"errors"
	"io"
	"testing"
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
