package main

import (
	"context"
	"io"
	"net"
	"testing"
	"time"
)

func TestLoadConfigParsesThroughputDurations(t *testing.T) {
	t.Setenv("HARNESS_C1_DURATION_SECONDS", "30")
	t.Setenv("HARNESS_C8_DURATION_SECONDS", "45")

	cfg := loadConfig()

	if cfg.c1Duration != 30*time.Second {
		t.Fatalf("c1Duration = %v, want %v", cfg.c1Duration, 30*time.Second)
	}
	if cfg.c8Duration != 45*time.Second {
		t.Fatalf("c8Duration = %v, want %v", cfg.c8Duration, 45*time.Second)
	}
}

func TestHandleBackendConnStreamsUnlimitedDownloadUntilClientClose(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer clientConn.Close()

	done := make(chan struct{})
	go func() {
		handleBackendConn(serverConn)
		close(done)
	}()

	if _, err := clientConn.Write([]byte{protocolModeDownloadUnlimited}); err != nil {
		t.Fatalf("client write mode: %v", err)
	}

	buf := make([]byte, 128*1024)
	n, err := io.ReadFull(clientConn, buf)
	if err != nil {
		t.Fatalf("client read download payload: %v", err)
	}
	if n != len(buf) {
		t.Fatalf("client read bytes = %d, want %d", n, len(buf))
	}

	_ = clientConn.Close()

	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("backend unlimited download did not stop after client close")
	}
}

func TestTransferForDurationDownloadsUntilDeadline(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()

	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		handleBackendConn(conn)
	}()

	deadline := time.Now().Add(150 * time.Millisecond)
	n, err := transferForDuration(ln.Addr().String(), deadline)
	if err != nil {
		t.Fatalf("transferForDuration: %v", err)
	}
	if n <= 0 {
		t.Fatalf("transferForDuration bytes = %d, want > 0", n)
	}

	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("backend connection did not exit after timed transfer")
	}
}
