package relay

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"testing"
	"time"
)

type openSignalWriter struct {
	once   sync.Once
	opened chan struct{}
}

func (w *openSignalWriter) Write(p []byte) (int, error) {
	w.once.Do(func() { close(w.opened) })
	return len(p), nil
}

func TestMultiHopOpenWaitsForDownstreamAndHonorsCancellation(t *testing.T) {
	defer ConfigureTimeouts(TimeoutConfig{FrameTimeout: 20 * time.Millisecond, DialTimeout: time.Second, HandshakeTimeout: time.Second})()
	for _, cancelCaller := range []bool{false, true} {
		t.Run(map[bool]string{false: "delayed_ack", true: "caller_canceled"}[cancelCaller], func(t *testing.T) {
			writer := &openSignalWriter{opened: make(chan struct{})}
			tunnel := &tlsTCPTunnel{writer: writer, streams: make(map[uint32]*tlsTCPLogicalStream), closed: make(chan struct{}), closeOuter: func() error { return nil }}
			defer tunnel.close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			ackDone := make(chan struct{})
			go func() {
				defer close(ackDone)
				<-writer.opened
				if cancelCaller {
					cancel()
					return
				}
				// A real timer is intentional: this is a regression for an elapsed
				// frame deadline while the next hop is still establishing TLS.
				timer := time.NewTimer(60 * time.Millisecond)
				defer timer.Stop()
				<-timer.C
				if stream := tunnel.getStream(1); stream != nil {
					stream.deliverOpenResult(muxOpenResult{OK: true})
				}
			}()
			conn, _, err := tunnel.openStream(ctx, relayOpenFrame{Kind: "tcp", Target: "example.test:80", Chain: []Hop{{Address: "next.test:443"}}})
			<-ackDone
			if cancelCaller {
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("error=%v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("downstream acknowledgement lost: %v", err)
			}
			_ = conn.Close()
		})
	}
}

// Verify the non-multiplexed exchange (also used by QUIC) installs the path
// budget for the acknowledgement rather than only the frame-write budget.
type openDeadlineConn struct {
	net.Conn
	response  io.Reader
	deadlines []time.Time
}

func (c *openDeadlineConn) Write(p []byte) (int, error) { return len(p), nil }
func (c *openDeadlineConn) Read(p []byte) (int, error)  { return c.response.Read(p) }
func (c *openDeadlineConn) SetDeadline(d time.Time) error {
	c.deadlines = append(c.deadlines, d)
	return nil
}

func TestExchangeRelayOpenUsesDownstreamBudgetAndClearsDeadline(t *testing.T) {
	defer ConfigureTimeouts(TimeoutConfig{FrameTimeout: 20 * time.Millisecond, DialTimeout: time.Second, HandshakeTimeout: time.Second})()
	var wire bytes.Buffer
	if err := writeRelayResponse(&wire, relayResponse{OK: true}); err != nil {
		t.Fatal(err)
	}
	conn := &openDeadlineConn{response: &wire}
	start := time.Now()
	if _, err := exchangeRelayOpenFrame(conn, relayOpenFrame{Kind: "udp", Chain: []Hop{{Address: "next:443"}}}); err != nil {
		t.Fatal(err)
	}
	if len(conn.deadlines) != 4 || conn.deadlines[0].Sub(start) > time.Second || conn.deadlines[2].Sub(start) < 2*time.Second || !conn.deadlines[3].IsZero() {
		t.Fatalf("exchange deadlines = %v", conn.deadlines)
	}
}
