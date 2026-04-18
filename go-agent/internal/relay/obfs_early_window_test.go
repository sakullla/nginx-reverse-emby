package relay

import (
	"bytes"
	"io"
	"testing"
)

func TestEarlyWindowMaskerRoundTrip(t *testing.T) {
	payload := bytes.Repeat([]byte{0x17, 0x03, 0x03, 0x00, 0x20}, 256)
	var masked bytes.Buffer

	writer := newEarlyWindowMaskWriter(&masked, earlyWindowMaskConfig{
		MaxBytes:  32 * 1024,
		MaxWrites: 8,
		Seed:      1,
	})
	if _, err := writer.Write(payload); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	reader := newEarlyWindowMaskReader(bytes.NewReader(masked.Bytes()), earlyWindowMaskConfig{
		MaxBytes: 32 * 1024,
	})
	got, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatal("payload mismatch")
	}
}

func TestEarlyWindowMaskReaderRejectsFrameAfterWindowEnd(t *testing.T) {
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

	reader := newEarlyWindowMaskReader(bytes.NewReader(stream.Bytes()), earlyWindowMaskConfig{
		MaxBytes: 32 * 1024,
	})
	if _, err := io.ReadAll(reader); err == nil {
		t.Fatal("expected invalid relay obfs frame error")
	}
}

func TestEarlyWindowMaskerKeepsWindowOpenAcrossMultipleWrites(t *testing.T) {
	part1 := bytes.Repeat([]byte{0x16, 0x03}, 128)
	part2 := bytes.Repeat([]byte{0x01, 0x20}, 128)
	var masked bytes.Buffer

	writer := newEarlyWindowMaskWriter(&masked, earlyWindowMaskConfig{
		MaxBytes:  32 * 1024,
		MaxWrites: 8,
		Seed:      1,
	})
	if _, err := writer.Write(part1); err != nil {
		t.Fatalf("Write(part1) error = %v", err)
	}
	if _, err := writer.Write(part2); err != nil {
		t.Fatalf("Write(part2) error = %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	reader := newEarlyWindowMaskReader(bytes.NewReader(masked.Bytes()), earlyWindowMaskConfig{
		MaxBytes: 32 * 1024,
	})
	got, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	want := append(append([]byte(nil), part1...), part2...)
	if !bytes.Equal(got, want) {
		t.Fatal("payload mismatch")
	}
}
