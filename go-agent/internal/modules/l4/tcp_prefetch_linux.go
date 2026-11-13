package l4

import (
	"net"
	"syscall"

	"golang.org/x/sys/unix"
)

// prefetchTCPAvailable runs under net's read lock but deliberately never asks
// the poller to wait. Empty sockets allocate nothing and retain their deadlines.
func prefetchTCPAvailable(conn net.Conn, limit int) ([]byte, error) {
	socket, ok := conn.(syscall.Conn)
	if !ok {
		return nil, nil
	}
	raw, err := socket.SyscallConn()
	if err != nil {
		return nil, nil
	}
	var payload []byte
	var readErr error
	err = raw.Read(func(fd uintptr) bool {
		available, ioctlErr := unix.IoctlGetInt(int(fd), unix.TIOCINQ)
		if ioctlErr != nil || available <= 0 {
			return true // Unsupported query simply uses the normal copy path.
		}
		payload = make([]byte, min(available, limit))
		n, _, recvErr := unix.Recvfrom(int(fd), payload, unix.MSG_DONTWAIT)
		if recvErr != nil {
			payload = nil
			if recvErr != unix.EAGAIN && recvErr != unix.EWOULDBLOCK && recvErr != unix.EINTR {
				readErr = recvErr
			}
			return true
		}
		payload = payload[:n]
		return true
	})
	if err != nil {
		return nil, err
	}
	return payload, readErr
}
