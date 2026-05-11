package relay

import (
	"bytes"
	"io"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/traffic"
)

func BenchmarkTLSTCPLogicalStreamReadFrom1MiB(b *testing.B) {
	payload := bytes.Repeat([]byte("x"), 1<<20)

	b.ReportAllocs()
	b.SetBytes(int64(len(payload)))
	for i := 0; i < b.N; i++ {
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
			streamID:     1,
			readCh:       make(chan struct{}, 1),
			openResultCh: make(chan muxOpenResult, 1),
		}
		if _, err := stream.ReadFrom(bytes.NewReader(payload)); err != nil {
			b.Fatalf("ReadFrom() error = %v", err)
		}
	}
}

func BenchmarkUOTPacketRoundTrip1400B(b *testing.B) {
	payload := bytes.Repeat([]byte("u"), 1400)
	scratch := make([]byte, maxUOTPacketSize)

	b.ReportAllocs()
	b.SetBytes(int64(len(payload)))
	for i := 0; i < b.N; i++ {
		var buf bytes.Buffer
		if err := writeUOTPacket(&buf, payload); err != nil {
			b.Fatalf("writeUOTPacket() error = %v", err)
		}
		if _, err := readUOTPacketInto(&buf, scratch); err != nil {
			b.Fatalf("readUOTPacketInto() error = %v", err)
		}
	}
}

func BenchmarkReadMuxFrame64KiB(b *testing.B) {
	payload := bytes.Repeat([]byte("m"), 64*1024)
	var wire bytes.Buffer
	if err := writeMuxFrame(&wire, muxFrame{
		Type:     muxFrameTypeData,
		StreamID: 1,
		Payload:  payload,
	}); err != nil {
		b.Fatalf("writeMuxFrame() error = %v", err)
	}
	frameBytes := wire.Bytes()

	b.ReportAllocs()
	b.SetBytes(int64(len(payload)))
	for i := 0; i < b.N; i++ {
		frame, err := readMuxFrame(bytes.NewReader(frameBytes))
		if err != nil {
			b.Fatalf("readMuxFrame() error = %v", err)
		}
		if len(frame.Payload) != len(payload) {
			b.Fatalf("payload len = %d, want %d", len(frame.Payload), len(payload))
		}
		frame.releasePayload()
	}
}

func BenchmarkWriteMuxFrame64KiB(b *testing.B) {
	payload := bytes.Repeat([]byte("w"), 64*1024)

	b.ReportAllocs()
	b.SetBytes(int64(len(payload)))
	for i := 0; i < b.N; i++ {
		var wire bytes.Buffer
		if err := writeMuxFrame(&wire, muxFrame{
			Type:     muxFrameTypeData,
			StreamID: 1,
			Payload:  payload,
		}); err != nil {
			b.Fatalf("writeMuxFrame() error = %v", err)
		}
	}
}

func BenchmarkCopyRelayTraffic1MiB(b *testing.B) {
	payload := bytes.Repeat([]byte("r"), 1<<20)
	previousTrafficEnabled := traffic.Enabled()
	traffic.Reset()
	traffic.SetEnabled(true)
	b.Cleanup(func() {
		traffic.SetEnabled(previousTrafficEnabled)
		traffic.Reset()
	})

	b.ReportAllocs()
	b.SetBytes(int64(len(payload)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := copyRelayTraffic(io.Discard, bytes.NewReader(payload), false, traffic.NewRelayRecorder()); err != nil {
			b.Fatalf("copyRelayTraffic() error = %v", err)
		}
	}
}
