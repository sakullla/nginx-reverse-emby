package l4

import (
	"bytes"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/traffic"
)

func BenchmarkCopyBidirectionalTCP1MiBWithTrafficAccounting(b *testing.B) {
	payload := bytes.Repeat([]byte("t"), 1<<20)
	previousTrafficEnabled := traffic.Enabled()
	traffic.Reset()
	traffic.SetEnabled(true)
	b.Cleanup(func() {
		traffic.SetEnabled(previousTrafficEnabled)
		traffic.Reset()
	})

	b.ReportAllocs()
	b.SetBytes(int64(len(payload) * 2))
	b.ResetTimer()

	const transferTimeout = 5 * time.Second
	for i := 0; i < b.N; i++ {
		downstreamClient, downstreamServer := net.Pipe()
		upstreamServer, upstreamBackend := net.Pipe()
		deadline := time.Now().Add(transferTimeout)
		_ = downstreamClient.SetDeadline(deadline)
		_ = downstreamServer.SetDeadline(deadline)
		_ = upstreamServer.SetDeadline(deadline)
		_ = upstreamBackend.SetDeadline(deadline)

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
		wgDone := make(chan struct{})
		go func() {
			wg.Wait()
			close(wgDone)
		}()

		select {
		case <-wgDone:
		case <-time.After(transferTimeout):
			_ = downstreamClient.Close()
			_ = downstreamServer.Close()
			_ = upstreamServer.Close()
			_ = upstreamBackend.Close()
			<-wgDone
			b.Fatalf("timed out waiting for benchmark traffic after %s", transferTimeout)
		}

		_ = downstreamClient.Close()
		_ = downstreamServer.Close()
		_ = upstreamServer.Close()
		_ = upstreamBackend.Close()
		select {
		case <-done:
		case <-time.After(transferTimeout):
			_ = downstreamClient.Close()
			_ = downstreamServer.Close()
			_ = upstreamServer.Close()
			_ = upstreamBackend.Close()
			b.Fatalf("timed out waiting for copyBidirectionalTCP after %s", transferTimeout)
		}
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
