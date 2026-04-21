package l4

import (
	"bytes"
	"io"
	"testing"
)

func TestCopyPreferReaderFromUsesDestinationFastPath(t *testing.T) {
	src := &writerToOnlySource{payload: []byte("payload")}
	dst := &readerFromRecordingWriter{}

	n, err := copyPreferReaderFrom(dst, src)
	if err != nil {
		t.Fatalf("copyPreferReaderFrom() error = %v", err)
	}
	if n != int64(len("payload")) {
		t.Fatalf("copyPreferReaderFrom() = %d, want %d", n, len("payload"))
	}
	if !dst.usedReaderFrom {
		t.Fatal("destination ReaderFrom fast path was not used")
	}
	if src.usedWriterTo {
		t.Fatal("source WriterTo fast path should have been bypassed")
	}
	if got := dst.String(); got != "payload" {
		t.Fatalf("copied payload = %q, want %q", got, "payload")
	}
}

type writerToOnlySource struct {
	payload      []byte
	offset       int
	usedWriterTo bool
}

func (s *writerToOnlySource) Read(p []byte) (int, error) {
	if s.offset >= len(s.payload) {
		return 0, io.EOF
	}
	n := copy(p, s.payload[s.offset:])
	s.offset += n
	return n, nil
}

func (s *writerToOnlySource) WriteTo(w io.Writer) (int64, error) {
	s.usedWriterTo = true
	n, err := w.Write(s.payload)
	return int64(n), err
}

type readerFromRecordingWriter struct {
	bytes.Buffer
	usedReaderFrom bool
}

func (w *readerFromRecordingWriter) ReadFrom(r io.Reader) (int64, error) {
	w.usedReaderFrom = true
	buf := make([]byte, 32*1024)
	var total int64
	for {
		n, err := r.Read(buf)
		if n > 0 {
			written, writeErr := w.Buffer.Write(buf[:n])
			total += int64(written)
			if writeErr != nil {
				return total, writeErr
			}
		}
		if err == io.EOF {
			return total, nil
		}
		if err != nil {
			return total, err
		}
	}
}
