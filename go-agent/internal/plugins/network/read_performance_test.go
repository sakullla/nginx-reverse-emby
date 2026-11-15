package network

import (
	"bytes"
	"context"
	"net"
	"slices"
	"testing"
	"time"

	sdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

type sizedReadConn struct {
	net.Conn
	data  *bytes.Reader
	sizes []int
}

func (c *sizedReadConn) Read(p []byte) (int, error) {
	c.sizes = append(c.sizes, len(p))
	return c.data.Read(p)
}
func (*sizedReadConn) SetReadDeadline(time.Time) error { return nil }

func TestManagedAdaptiveReadPreservesDataAndOwnership(t *testing.T) {
	payload := bytes.Repeat([]byte("data"), 1<<17)
	conn := &sizedReadConn{data: bytes.NewReader(payload)}
	r := &resource{conn: conn, readGate: make(chan struct{}, 1), done: make(chan struct{})}
	request := sdk.ManagedNetworkRequest{MaxBytes: sdk.ManagedNetworkMaxChunkBytes, WaitMS: 30000}
	var chunks [][]byte
	for {
		response, err := r.read(t.Context(), request)
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
	if conn.sizes[0] > 4<<10 {
		t.Fatal("first read reserves a bulk-transfer buffer")
	}
	if !slices.Contains(conn.sizes, request.MaxBytes) {
		t.Fatal("bulk transfer never reached requested read size")
	}
	// The EOF poll resets sizing; a smaller next request remains an upper bound.
	request.MaxBytes = 3
	conn.data = bytes.NewReader([]byte("next"))
	response, err := r.read(t.Context(), request)
	if err != nil || string(response.Data) != "nex" {
		t.Fatalf("limited read = %q, %v", response.Data, err)
	}
	request.MaxBytes = sdk.ManagedNetworkMaxChunkBytes
	response, err = r.read(t.Context(), request)
	if err != nil || string(response.Data) != "t" || conn.sizes[len(conn.sizes)-1] > 4<<10 {
		t.Fatalf("read after empty poll = %q, %v", response.Data, err)
	}
}

type smallReadConn struct{ net.Conn }

func (smallReadConn) Read(p []byte) (int, error)      { return copy(p, "small response"), nil }
func (smallReadConn) SetReadDeadline(time.Time) error { return nil }

func BenchmarkManagedSmallRead(b *testing.B) {
	r := &resource{conn: smallReadConn{}, readGate: make(chan struct{}, 1), done: make(chan struct{})}
	request := sdk.ManagedNetworkRequest{MaxBytes: sdk.ManagedNetworkMaxChunkBytes, WaitMS: 30000}
	b.ReportAllocs()
	for b.Loop() {
		response, err := r.read(context.Background(), request)
		if err != nil || string(response.Data) != "small response" {
			b.Fatalf("read = %q, %v", response.Data, err)
		}
	}
}
