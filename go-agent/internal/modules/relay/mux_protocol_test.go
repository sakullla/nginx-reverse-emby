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
	defer got.releasePayload()
	if got.Version != frame.Version || got.Type != frame.Type || got.Flags != frame.Flags || got.StreamID != frame.StreamID || !bytes.Equal(got.Payload, frame.Payload) {
		t.Fatalf("readMuxFrame() = %#v, want %#v", got, frame)
	}
}

func TestReadMuxFrameAllocations(t *testing.T) {
	payload := bytes.Repeat([]byte("m"), 64*1024)
	var wire bytes.Buffer
	if err := writeMuxFrame(&wire, muxFrame{
		Type:     muxFrameTypeData,
		StreamID: 1,
		Payload:  payload,
	}); err != nil {
		t.Fatalf("writeMuxFrame() error = %v", err)
	}
	frameBytes := append([]byte(nil), wire.Bytes()...)
	frame, err := readMuxFrame(bytes.NewReader(frameBytes))
	if err != nil {
		t.Fatalf("warm readMuxFrame() error = %v", err)
	}
	frame.releasePayload()

	allocs := testing.AllocsPerRun(1000, func() {
		frame, err := readMuxFrame(bytes.NewReader(frameBytes))
		if err != nil {
			t.Fatalf("readMuxFrame() error = %v", err)
		}
		if len(frame.Payload) != len(payload) {
			t.Fatalf("payload len = %d, want %d", len(frame.Payload), len(payload))
		}
		frame.releasePayload()
	})
	if allocs > 3 {
		t.Fatalf("readMuxFrame() allocations = %.2f, want <= 3", allocs)
	}
}

func TestMuxFrameTakeReadChunkTransfersPayloadOwnership(t *testing.T) {
	payload := bytes.Repeat([]byte("m"), 1024)
	var wire bytes.Buffer
	if err := writeMuxFrame(&wire, muxFrame{
		Type:     muxFrameTypeData,
		StreamID: 1,
		Payload:  payload,
	}); err != nil {
		t.Fatalf("writeMuxFrame() error = %v", err)
	}
	frame, err := readMuxFrame(&wire)
	if err != nil {
		t.Fatalf("readMuxFrame() error = %v", err)
	}
	chunk := frame.takeReadChunk()
	if len(frame.Payload) != 0 {
		t.Fatalf("frame retained payload after takeReadChunk: %d", len(frame.Payload))
	}
	frame.releasePayload()
	frame.releasePayload()
	chunk.releaseNow()
	chunk.releaseNow()
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

func TestWriteMuxFrameUsesSingleWriteForBulkDataFrame(t *testing.T) {
	writer := &countingMuxWriter{}
	frame := muxFrame{
		Type:     muxFrameTypeData,
		StreamID: 11,
		Payload:  bytes.Repeat([]byte("z"), 64*1024),
	}

	if err := writeMuxFrame(writer, frame); err != nil {
		t.Fatalf("writeMuxFrame() error = %v", err)
	}
	if writer.writeCalls != 1 {
		t.Fatalf("write calls = %d, want 1", writer.writeCalls)
	}
}

type countingMuxWriter struct {
	writeCalls int
	buf        bytes.Buffer
}

func (w *countingMuxWriter) Write(p []byte) (int, error) {
	w.writeCalls++
	return w.buf.Write(p)
}
