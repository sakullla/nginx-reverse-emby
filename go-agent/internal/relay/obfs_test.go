package relay

import (
	"bytes"
	"io"
	"testing"
)

func TestObfsFramesRoundTripOriginalBytes(t *testing.T) {
	payload := bytes.Repeat([]byte{0x16, 0x03, 0x01, 0x20}, 256)
	var framed bytes.Buffer

	writer := newObfsFirstSegmentWriter(&framed, obfsConfig{
		MaxDataBytes: 4096,
		MaxPadFrames: 4,
		Seed:         1,
	})
	if _, err := writer.Write(payload); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	reader := newObfsFirstSegmentReader(bytes.NewReader(framed.Bytes()), obfsConfig{MaxDataBytes: 4096})
	got, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("payload mismatch")
	}
}

func TestObfsReaderRejectsFrameAfterEnd(t *testing.T) {
	var stream bytes.Buffer
	if err := writeObfsFrame(&stream, obfsFrameData, []byte("hello")); err != nil {
		t.Fatalf("writeObfsFrame(data) error = %v", err)
	}
	if err := writeObfsFrame(&stream, obfsFrameEnd, nil); err != nil {
		t.Fatalf("writeObfsFrame(end) error = %v", err)
	}
	if err := writeObfsFrame(&stream, obfsFrameData, []byte("again")); err != nil {
		t.Fatalf("writeObfsFrame(data-again) error = %v", err)
	}

	reader := newObfsFirstSegmentReader(bytes.NewReader(stream.Bytes()), obfsConfig{MaxDataBytes: 4096})
	if _, err := io.ReadAll(reader); err == nil {
		t.Fatal("expected invalid relay obfs frame error")
	}
}
