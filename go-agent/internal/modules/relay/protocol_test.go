package relay

import (
	"bytes"
	"context"
	"io"
	"net"
	"strings"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
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
		Chain: []Hop{{
			Address: "relay.example.test:9443",
			Listener: Listener{
				ID:            7,
				TransportMode: ListenerTransportModeQUIC,
			},
		}},
	}

	var sink bytes.Buffer
	if err := writeRelayRequest(&sink, request); err != nil {
		t.Fatalf("writeRelayRequest() error = %v", err)
	}

	got, err := readRelayRequest(bytes.NewReader(sink.Bytes()))
	if err != nil {
		t.Fatalf("readRelayRequest() error = %v", err)
	}
	if len(got.Chain) != 1 || got.Chain[0].Listener.TransportMode != ListenerTransportModeQUIC {
		t.Fatalf("request chain = %+v", got.Chain)
	}
}

func TestRelayOpenFrameRoundTripsInitialData(t *testing.T) {
	payload, err := marshalMuxOpenPayload(relayOpenFrame{
		Kind:        "tcp",
		Target:      "127.0.0.1:9000",
		InitialData: []byte("hello"),
	})
	if err != nil {
		t.Fatalf("marshalMuxOpenPayload() error = %v", err)
	}

	frame, err := readMuxOpenPayload(payload)
	if err != nil {
		t.Fatalf("readMuxOpenPayload() error = %v", err)
	}

	if got := string(frame.InitialData); got != "hello" {
		t.Fatalf("InitialData = %q, want %q", got, "hello")
	}
}

func TestRelayOpenFrameRoundTripsTrafficClassMetadata(t *testing.T) {
	payload, err := marshalMuxOpenPayload(relayOpenFrame{
		Kind:   "tcp",
		Target: "127.0.0.1:9000",
		Metadata: map[string]any{
			relayMetadataTrafficClass: "bulk",
		},
	})
	if err != nil {
		t.Fatalf("marshalMuxOpenPayload() error = %v", err)
	}

	frame, err := readMuxOpenPayload(payload)
	if err != nil {
		t.Fatalf("readMuxOpenPayload() error = %v", err)
	}
	if got := relayTrafficClassFromMetadata(frame.Metadata); got != model.TrafficClassBulk {
		t.Fatalf("relayTrafficClassFromMetadata() = %q, want %q", got, model.TrafficClassBulk)
	}
}

func TestExchangeRelayOpenFrameWritesRequestAndReadsResponse(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	done := make(chan relayOpenFrame, 1)
	go func() {
		request, err := readRelayOpenFrame(server)
		if err != nil {
			t.Errorf("readRelayOpenFrame() error = %v", err)
			return
		}
		if err := writeRelayResponse(server, relayResponse{OK: true, SelectedAddress: "127.0.0.1:9000"}); err != nil {
			t.Errorf("writeRelayResponse() error = %v", err)
			return
		}
		done <- request
	}()

	response, err := exchangeRelayOpenFrame(client, relayOpenFrame{
		Kind:   "tcp",
		Target: "example.test:443",
	})
	if err != nil {
		t.Fatalf("exchangeRelayOpenFrame() error = %v", err)
	}
	if !response.OK || response.SelectedAddress != "127.0.0.1:9000" {
		t.Fatalf("response = %+v", response)
	}

	request := <-done
	if request.Kind != "tcp" || request.Target != "example.test:443" {
		t.Fatalf("request = %+v", request)
	}
}

func TestExchangeRelayOpenFrameApplicationErrorKeepsResponse(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	go func() {
		_, _ = readRelayOpenFrame(server)
		_ = writeRelayResponse(server, relayResponse{OK: false, Error: "blocked", SelectedAddress: "127.0.0.1:9001"})
	}()

	response, err := exchangeRelayOpenFrame(client, relayOpenFrame{
		Kind:   "tcp",
		Target: "example.test:443",
	})
	if err == nil {
		t.Fatal("exchangeRelayOpenFrame() error = nil")
	}
	if got, want := err.Error(), "relay request failed: blocked"; got != want {
		t.Fatalf("error = %q, want %q", got, want)
	}
	if response.SelectedAddress != "127.0.0.1:9001" {
		t.Fatalf("response = %+v", response)
	}
}

func TestDialOptionsCloneInitialPayload(t *testing.T) {
	opts := DialOptions{InitialPayload: []byte("abc"), TrafficClass: model.TrafficClassInteractive}
	clone := opts.clone()
	opts.InitialPayload[0] = 'z'

	if got := string(clone.InitialPayload); got != "abc" {
		t.Fatalf("clone.InitialPayload = %q, want %q", got, "abc")
	}
	if clone.TrafficClass != model.TrafficClassInteractive {
		t.Fatalf("clone.TrafficClass = %q, want %q", clone.TrafficClass, model.TrafficClassInteractive)
	}
}

func TestDialRejectsMultipleDialOptions(t *testing.T) {
	_, err := Dial(
		context.Background(),
		"tcp",
		"127.0.0.1:9000",
		[]Hop{{Address: "127.0.0.1:9443"}},
		nil,
		DialOptions{},
		DialOptions{},
	)
	if err == nil {
		t.Fatal("Dial() error = nil")
	}
	if got, want := err.Error(), "multiple relay dial options"; !strings.Contains(got, want) {
		t.Fatalf("Dial() error = %q, want containing %q", got, want)
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
