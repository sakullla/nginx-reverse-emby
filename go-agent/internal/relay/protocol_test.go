package relay

import (
	"bytes"
	"io"
	"testing"
)

func TestWriteRelayRequestHandlesShortWrites(t *testing.T) {
	var sink bytes.Buffer
	writer := &shortWriter{target: &sink, limit: 3}

	request := relayRequest{
		Network: "tcp",
		Target:  "127.0.0.1:1234",
	}
	if err := writeRelayRequest(writer, request); err != nil {
		t.Fatalf("writeRelayRequest returned error: %v", err)
	}

	decoded, err := readRelayRequest(bytes.NewReader(sink.Bytes()))
	if err != nil {
		t.Fatalf("readRelayRequest returned error: %v", err)
	}
	if decoded.Target != request.Target || decoded.Network != request.Network {
		t.Fatalf("decoded request mismatch: got %+v want %+v", decoded, request)
	}
}

func TestRelayRequestRoundTripsTransportMode(t *testing.T) {
	request := relayRequest{
		Network: "tcp",
		Target:  "127.0.0.1:443",
		Transport: relayTransport{
			Mode: relayTransportModeFirstSegmentV1,
		},
	}

	var sink bytes.Buffer
	if err := writeRelayRequest(&sink, request); err != nil {
		t.Fatalf("writeRelayRequest() error = %v", err)
	}

	got, err := readRelayRequest(bytes.NewReader(sink.Bytes()))
	if err != nil {
		t.Fatalf("readRelayRequest() error = %v", err)
	}
	if got.Transport.Mode != relayTransportModeFirstSegmentV1 {
		t.Fatalf("transport mode = %q", got.Transport.Mode)
	}
}

type shortWriter struct {
	target io.Writer
	limit  int
}

func (w *shortWriter) Write(p []byte) (int, error) {
	if len(p) > w.limit {
		p = p[:w.limit]
	}
	return w.target.Write(p)
}
