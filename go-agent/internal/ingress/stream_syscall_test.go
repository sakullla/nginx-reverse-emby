package ingress

import (
	"net"
	"sync"
	"syscall"
	"testing"
)

type syscallTestConn struct{ net.Conn }

func (syscallTestConn) SyscallConn() (syscall.RawConn, error) { return nil, nil }

func TestStreamSocketAccessPreservesDispatchAcknowledgement(t *testing.T) {
	for _, automatic := range []bool{true, false} {
		endpoint := &StreamEndpoint{}
		endpoint.dispatchIdle = sync.NewCond(&endpoint.dispatchMu)
		endpoint.acknowledgeOnIO.Store(automatic)
		conn := endpoint.trackDispatch(syscallTestConn{})
		for range 2 {
			if _, err := conn.SyscallConn(); err != nil {
				t.Fatal(err)
			}
		}
		want := 1
		if automatic {
			want = 0
		}
		if endpoint.pendingDispatch != want {
			t.Fatalf("automatic=%v pending=%d, want %d", automatic, endpoint.pendingDispatch, want)
		}
		conn.AcknowledgeStreamDispatch()
		if endpoint.pendingDispatch != 0 {
			t.Fatal("explicit acknowledgement did not release dispatch")
		}
	}
}
