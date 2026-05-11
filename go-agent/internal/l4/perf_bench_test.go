package l4

import (
	"bytes"
	"io"
	"net"
	"sync"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/traffic"
)

func BenchmarkCopyBidirectionalTCP1MiBWithTrafficAccounting(b *testing.B) {
	payload := bytes.Repeat([]byte("t"), 1<<20)
	traffic.Reset()
	traffic.SetEnabled(true)
	b.Cleanup(func() {
		traffic.SetEnabled(true)
		traffic.Reset()
	})

	b.ReportAllocs()
	b.SetBytes(int64(len(payload) * 2))
	for i := 0; i < b.N; i++ {
		downstreamClient, downstreamServer := net.Pipe()
		upstreamServer, upstreamBackend := net.Pipe()
		done := make(chan struct{})
		go func() {
			copyBidirectionalTCP(downstreamServer, upstreamServer, traffic.NewL4Recorder())
			close(done)
		}()

		var wg sync.WaitGroup
		wg.Add(4)
		go benchmarkWriteAll(b, &wg, downstreamClient, payload)
		go benchmarkDiscardN(b, &wg, upstreamBackend, len(payload))
		go benchmarkWriteAll(b, &wg, upstreamBackend, payload)
		go benchmarkDiscardN(b, &wg, downstreamClient, len(payload))
		wg.Wait()

		_ = downstreamClient.Close()
		_ = downstreamServer.Close()
		_ = upstreamServer.Close()
		_ = upstreamBackend.Close()
		<-done
	}
}

func benchmarkWriteAll(b *testing.B, wg *sync.WaitGroup, conn net.Conn, payload []byte) {
	b.Helper()
	defer wg.Done()
	if _, err := conn.Write(payload); err != nil {
		b.Errorf("Write() error = %v", err)
	}
}

func benchmarkDiscardN(b *testing.B, wg *sync.WaitGroup, r io.Reader, size int) {
	b.Helper()
	defer wg.Done()
	if _, err := io.CopyN(io.Discard, r, int64(size)); err != nil {
		b.Errorf("CopyN() error = %v", err)
	}
}
