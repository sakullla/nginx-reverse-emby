package l4

import (
	"net"
	"sync/atomic"
	"testing"
	"time"
)

type releaseOrderListener struct {
	udpListener
	released atomic.Bool
}

func (l *releaseOrderListener) ReleaseAssociation(*net.UDPAddr, string) { l.released.Store(true) }

type releaseOrderUpstream struct {
	udpUpstream
	entered chan struct{}
	unblock chan struct{}
}

func (u *releaseOrderUpstream) Close() error {
	close(u.entered)
	<-u.unblock
	return nil
}

func TestUDPAssociationRetiresBeforeBlockingUpstreamClose(t *testing.T) {
	for _, all := range []bool{false, true} {
		name := "single"
		if all {
			name = "all"
		}
		t.Run(name, func(t *testing.T) {
			listener := &releaseOrderListener{}
			upstream := &releaseOrderUpstream{entered: make(chan struct{}), unblock: make(chan struct{})}
			server := &Server{udpSessions: map[string]*udpSession{"flow": {listener: listener, upstream: upstream}}}
			done := make(chan struct{})
			go func() {
				defer close(done)
				if all {
					server.closeUDPSessions()
				} else {
					server.closeUDPSession("flow")
				}
			}()
			defer func() { close(upstream.unblock); <-done }()
			select {
			case <-upstream.entered:
			case <-time.After(time.Second):
				t.Fatal("upstream close not reached")
			}
			if count := server.udpSessionCount(); count != 0 {
				t.Fatalf("retired session still visible: %d", count)
			}
			if !listener.released.Load() {
				t.Fatal("absent session still pins packets to the old generation during upstream close")
			}
		})
	}
}
