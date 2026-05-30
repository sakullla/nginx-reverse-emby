package relay

import (
	"context"
	"net"
	"sync/atomic"
	"testing"
	"time"
)

func TestAcceptLoopReturnsOnClosedListenerError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ln := &permanentAcceptErrorListener{err: net.ErrClosed}
	server := &Server{ctx: ctx}
	done := make(chan struct{})
	server.wg.Add(1)
	go func() {
		server.acceptLoop(ln, Listener{})
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
		cancel()
		<-done
		t.Fatal("acceptLoop did not return after listener closed")
	}

	if calls := ln.calls.Load(); calls != 1 {
		t.Fatalf("Accept() calls = %d, want 1", calls)
	}
}

type permanentAcceptErrorListener struct {
	calls atomic.Int64
	err   error
}

func (l *permanentAcceptErrorListener) Accept() (net.Conn, error) {
	l.calls.Add(1)
	return nil, l.err
}

func (l *permanentAcceptErrorListener) Close() error {
	return nil
}

func (l *permanentAcceptErrorListener) Addr() net.Addr {
	return permanentAcceptErrorAddr("accept-loop-test")
}

type permanentAcceptErrorAddr string

func (a permanentAcceptErrorAddr) Network() string { return string(a) }
func (a permanentAcceptErrorAddr) String() string  { return string(a) }
