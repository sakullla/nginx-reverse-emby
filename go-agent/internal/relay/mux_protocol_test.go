package relay

import (
	"bytes"
	"testing"
)

func TestMuxFrameRoundTrip(t *testing.T) {
	frame := muxFrame{
		Version:  1,
		Type:     muxFrameTypeOpen,
		Flags:    muxFlagAckRequired,
		StreamID: 7,
		Payload:  []byte(`{"network":"tcp","target":"127.0.0.1:443"}`),
	}

	var buf bytes.Buffer
	if err := writeMuxFrame(&buf, frame); err != nil {
		t.Fatalf("writeMuxFrame() error = %v", err)
	}

	got, err := readMuxFrame(&buf)
	if err != nil {
		t.Fatalf("readMuxFrame() error = %v", err)
	}
	if got.Version != frame.Version || got.Type != frame.Type || got.Flags != frame.Flags || got.StreamID != frame.StreamID || !bytes.Equal(got.Payload, frame.Payload) {
		t.Fatalf("readMuxFrame() = %#v, want %#v", got, frame)
	}
}

func TestMuxFrameRejectsOversizedPayload(t *testing.T) {
	frame := muxFrame{
		Version:  1,
		Type:     muxFrameTypeData,
		StreamID: 9,
		Payload:  bytes.Repeat([]byte("a"), maxRequestSize+1),
	}

	if err := writeMuxFrame(&bytes.Buffer{}, frame); err == nil {
		t.Fatal("expected oversized payload error")
	}
}
