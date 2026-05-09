package stream

import (
	"bytes"
	"io"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/traffic"
)

type readerFromBuffer struct {
	bytes.Buffer
	usedReaderFrom bool
}

func (b *readerFromBuffer) ReadFrom(r io.Reader) (int64, error) {
	b.usedReaderFrom = true
	return b.Buffer.ReadFrom(r)
}

type writerToReader struct {
	payload []byte
	used    bool
}

func (r *writerToReader) Read(p []byte) (int, error) {
	if len(r.payload) == 0 {
		return 0, io.EOF
	}
	n := copy(p, r.payload)
	r.payload = r.payload[n:]
	return n, nil
}

func (r *writerToReader) WriteTo(w io.Writer) (int64, error) {
	r.used = true
	n, err := w.Write(r.payload)
	r.payload = r.payload[n:]
	return int64(n), err
}

func TestCopyPreferReaderFromUsesDestinationFastPath(t *testing.T) {
	dst := &readerFromBuffer{}
	n, err := CopyPreferReaderFrom(dst, bytes.NewBufferString("payload"))
	if err != nil {
		t.Fatalf("CopyPreferReaderFrom() error = %v", err)
	}
	if n != int64(len("payload")) || dst.String() != "payload" || !dst.usedReaderFrom {
		t.Fatalf("copy result n=%d body=%q usedReaderFrom=%v", n, dst.String(), dst.usedReaderFrom)
	}
}

func TestCopyGenericSuppressesWriterTo(t *testing.T) {
	src := &writerToReader{payload: []byte("payload")}
	var dst bytes.Buffer
	n, err := CopyGeneric(&dst, src)
	if err != nil {
		t.Fatalf("CopyGeneric() error = %v", err)
	}
	if n != int64(len("payload")) || dst.String() != "payload" {
		t.Fatalf("copy result n=%d body=%q", n, dst.String())
	}
	if src.used {
		t.Fatal("CopyGeneric used source WriteTo fast path")
	}
}

func TestTrafficWriterCountsTXAndFlushesAtThreshold(t *testing.T) {
	traffic.Reset()
	t.Cleanup(traffic.Reset)
	recorder := traffic.NewHTTPRecorder()
	var dst bytes.Buffer
	writer := NewTrafficWriter(&dst, DirectionTX, recorder, 4)
	if _, err := writer.Write([]byte("abcd")); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	stats := traffic.Snapshot()
	total := stats["traffic"].(map[string]any)["http"].(map[string]uint64)
	if total["tx_bytes"] != 4 || total["rx_bytes"] != 0 {
		t.Fatalf("http counters = %+v, want tx=4 rx=0", total)
	}
}

func TestTrafficWriterFlushesSmallWritesWithBelowThresholdPolicy(t *testing.T) {
	traffic.Reset()
	t.Cleanup(traffic.Reset)
	recorder := traffic.NewRelayRecorder()
	var dst bytes.Buffer
	writer := NewTrafficWriterFlushBelow(&dst, DirectionRX, recorder, 32*1024)
	if _, err := writer.Write([]byte("abc")); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	stats := traffic.Snapshot()
	total := stats["traffic"].(map[string]any)["relay"].(map[string]uint64)
	if total["rx_bytes"] != 3 || total["tx_bytes"] != 0 {
		t.Fatalf("relay counters = %+v, want rx=3 tx=0", total)
	}
}

func TestTrafficWriterFlushBelowKeepsLargeWritesVisibleToSnapshotNonZero(t *testing.T) {
	traffic.Reset()
	t.Cleanup(traffic.Reset)
	recorder := traffic.NewRelayRecorder()
	var dst bytes.Buffer
	writer := NewTrafficWriterFlushBelow(&dst, DirectionRX, recorder, 4)
	if _, err := writer.Write([]byte("payload")); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	stats := traffic.SnapshotNonZero()
	if stats == nil {
		t.Fatal("SnapshotNonZero() = nil, want pending relay traffic")
	}
	total := stats["traffic"].(map[string]any)["relay"].(map[string]uint64)
	if total["rx_bytes"] != uint64(len("payload")) || total["tx_bytes"] != 0 {
		t.Fatalf("relay counters = %+v, want rx=%d tx=0", total, len("payload"))
	}
}
func TestTrafficReadCloserFlushesOnEOF(t *testing.T) {
	traffic.Reset()
	t.Cleanup(traffic.Reset)
	recorder := traffic.NewHTTPRecorder()
	reader := NewTrafficReadCloser(io.NopCloser(bytes.NewBufferString("abc")), DirectionRX, recorder)
	if _, err := io.ReadAll(reader); err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	if err := reader.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	stats := traffic.Snapshot()
	total := stats["traffic"].(map[string]any)["http"].(map[string]uint64)
	if total["rx_bytes"] != 3 || total["tx_bytes"] != 0 {
		t.Fatalf("http counters = %+v, want rx=3 tx=0", total)
	}
}
