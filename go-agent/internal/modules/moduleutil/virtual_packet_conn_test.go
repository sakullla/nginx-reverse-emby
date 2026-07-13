package moduleutil

import (
	"errors"
	"net"
	"os"
	"sync"
	"testing"
	"time"
)

func TestVirtualPacketConnGatesClonesAndWritesBack(t *testing.T) {
	var written []byte
	var writtenTo net.Addr
	writer := &testPacketWriter{write: func(payload []byte, addr net.Addr) (int, error) {
		written = append([]byte(nil), payload...)
		writtenTo = addr
		return len(payload), nil
	}}
	conn := NewVirtualPacketConn(testAddr("local"), 1, writer)
	remote := testAddr("remote")
	payload := []byte("candidate")
	if err := conn.Deliver(payload, remote); !errors.Is(err, ErrVirtualEndpointInactive) {
		t.Fatalf("Deliver before Activate error = %v, want ErrVirtualEndpointInactive", err)
	}
	if _, err := conn.WriteTo([]byte("candidate-reply"), remote); !errors.Is(err, ErrVirtualEndpointInactive) {
		t.Fatalf("WriteTo before Activate error = %v, want ErrVirtualEndpointInactive", err)
	}

	conn.Activate()
	if err := conn.Deliver(payload, remote); err != nil {
		t.Fatalf("Deliver after Activate error = %v", err)
	}
	payload[0] = 'X'
	buf := make([]byte, 32)
	n, addr, err := conn.ReadFrom(buf)
	if err != nil {
		t.Fatalf("ReadFrom() error = %v", err)
	}
	if got := string(buf[:n]); got != "candidate" {
		t.Fatalf("ReadFrom payload = %q, want candidate", got)
	}
	if addr.String() != remote.String() {
		t.Fatalf("ReadFrom addr = %v, want %v", addr, remote)
	}
	if _, err := conn.WriteTo([]byte("reply"), remote); err != nil {
		t.Fatalf("WriteTo() error = %v", err)
	}
	if string(written) != "reply" || writtenTo.String() != remote.String() {
		t.Fatalf("writeback = %q/%v, want reply/%v", written, writtenTo, remote)
	}
	if err := conn.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := conn.Deliver([]byte("late"), remote); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("Deliver after Close error = %v, want net.ErrClosed", err)
	}
}

type testPacketWriter struct {
	write    func([]byte, net.Addr) (int, error)
	deadline func(time.Time) error
}

func (w *testPacketWriter) WriteTo(p []byte, a net.Addr) (int, error) { return w.write(p, a) }
func (w *testPacketWriter) SetWriteDeadline(d time.Time) error {
	if w.deadline != nil {
		return w.deadline(d)
	}
	return nil
}

func TestVirtualPacketConnReadDeadlineWakesBlockedRead(t *testing.T) {
	conn := NewVirtualPacketConn(testAddr("local"), 1, nil)
	conn.Activate()
	readResult := make(chan error, 1)
	go func() {
		_, _, err := conn.ReadFrom(make([]byte, 1))
		readResult <- err
	}()
	if err := conn.SetReadDeadline(time.Now().Add(10 * time.Millisecond)); err != nil {
		t.Fatalf("SetReadDeadline() error = %v", err)
	}
	select {
	case err := <-readResult:
		if !errors.Is(err, os.ErrDeadlineExceeded) {
			t.Fatalf("ReadFrom() error = %v, want os.ErrDeadlineExceeded", err)
		}
	case <-time.After(time.Second):
		t.Fatal("blocked ReadFrom did not observe updated deadline")
	}
}

func TestVirtualPacketConnWriteDeadlineWakesBlockedWrite(t *testing.T) {
	writer := newBlockingPacketWriter()
	conn := NewVirtualPacketConn(testAddr("local"), 1, writer)
	conn.Activate()
	result := make(chan error, 1)
	go func() { _, err := conn.WriteTo([]byte("blocked"), testAddr("remote")); result <- err }()
	<-writer.started
	if err := conn.SetWriteDeadline(time.Now().Add(10 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if !errors.Is(err, os.ErrDeadlineExceeded) {
			t.Fatalf("WriteTo error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("blocked write ignored changed deadline")
	}
}

func TestVirtualPacketConnPreconfiguredWriteDeadlineInterruptsWrite(t *testing.T) {
	writer := newBlockingPacketWriter()
	conn := NewVirtualPacketConn(testAddr("local"), 1, writer)
	conn.Activate()
	if err := conn.SetWriteDeadline(time.Now().Add(10 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() { _, err := conn.WriteTo([]byte("blocked"), testAddr("remote")); result <- err }()
	select {
	case err := <-result:
		if !errors.Is(err, os.ErrDeadlineExceeded) {
			t.Fatalf("WriteTo error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("blocked write ignored configured deadline")
	}
}

type blockingPacketWriter struct {
	started chan struct{}
	unblock chan struct{}
	once    sync.Once
}

func newBlockingPacketWriter() *blockingPacketWriter {
	return &blockingPacketWriter{started: make(chan struct{}), unblock: make(chan struct{})}
}
func (w *blockingPacketWriter) WriteTo([]byte, net.Addr) (int, error) {
	w.once.Do(func() { close(w.started) })
	<-w.unblock
	return 0, os.ErrDeadlineExceeded
}
func (w *blockingPacketWriter) SetWriteDeadline(d time.Time) error {
	go func() {
		delay := time.Until(d)
		if delay > 0 {
			time.Sleep(delay)
		}
		select {
		case <-w.unblock:
		default:
			close(w.unblock)
		}
	}()
	return nil
}
