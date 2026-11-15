package rpc

import (
	"bytes"
	"context"
	"net"
	"slices"
	"testing"
	"time"

	sdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

type sizedTLSReadConn struct {
	net.Conn
	data  *bytes.Reader
	sizes []int
}

func (c *sizedTLSReadConn) Read(p []byte) (int, error) {
	c.sizes = append(c.sizes, len(p))
	return c.data.Read(p)
}
func (*sizedTLSReadConn) SetReadDeadline(time.Time) error { return nil }

func TestTerminatedAdaptiveReadPreservesDataAndOwnership(t *testing.T) {
	payload := bytes.Repeat([]byte("data"), 1<<17)
	conn := &sizedTLSReadConn{data: bytes.NewReader(payload)}
	stream := newTerminatedStream(nil, "", "", conn)
	request := sdk.ManagedNetworkRequest{MaxBytes: sdk.ManagedNetworkMaxChunkBytes, WaitMS: 30000}
	var chunks [][]byte
	for {
		response, err := stream.read(t.Context(), request)
		if err != nil {
			t.Fatal(err)
		}
		chunks = append(chunks, response.Data)
		if response.EOF {
			break
		}
	}
	if !bytes.Equal(bytes.Join(chunks, nil), payload) {
		t.Fatal("short reads lost data or reused a previous response")
	}
	if conn.sizes[0] > 4<<10 || !slices.Contains(conn.sizes, request.MaxBytes) {
		t.Fatalf("read buffer failed to start small and grow: %v", conn.sizes)
	}
	conn.data = bytes.NewReader([]byte("next"))
	request.MaxBytes = 3
	response, err := stream.read(t.Context(), request)
	if err != nil || string(response.Data) != "nex" {
		t.Fatalf("limited read = %q, %v", response.Data, err)
	}
	request.MaxBytes = sdk.ManagedNetworkMaxChunkBytes
	response, err = stream.read(t.Context(), request)
	if err != nil || string(response.Data) != "t" || conn.sizes[len(conn.sizes)-1] > 4<<10 {
		t.Fatalf("read after empty poll = %q, %v", response.Data, err)
	}
}

type smallTLSReadConn struct{ net.Conn }

func (smallTLSReadConn) Read(p []byte) (int, error)      { return copy(p, "small response"), nil }
func (smallTLSReadConn) SetReadDeadline(time.Time) error { return nil }

func BenchmarkTerminatedSmallRead(b *testing.B) {
	stream := newTerminatedStream(nil, "", "", smallTLSReadConn{})
	request := sdk.ManagedNetworkRequest{MaxBytes: sdk.ManagedNetworkMaxChunkBytes, WaitMS: 30000}
	b.ReportAllocs()
	for b.Loop() {
		response, err := stream.read(context.Background(), request)
		if err != nil || string(response.Data) != "small response" {
			b.Fatalf("read = %q, %v", response.Data, err)
		}
	}
}
