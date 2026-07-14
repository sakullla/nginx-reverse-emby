//go:build linux

package platform

import (
	"errors"
	"net"
	"os"

	"golang.org/x/sys/unix"
)

const packetHandoffBufferBytes = 256 * 1024

func PacketConnFile(conn net.PacketConn) (*os.File, error) {
	if !SupportsHotRestart() {
		return nil, errors.New("hot restart is unsupported on this linux architecture")
	}
	fileConn, ok := conn.(interface {
		File() (*os.File, error)
	})
	if !ok {
		return nil, errors.New("packet connection does not expose an inheritable file descriptor")
	}
	file, err := fileConn.File()
	if err != nil {
		return nil, err
	}
	if file == nil {
		return nil, errors.New("packet connection returned a nil file descriptor")
	}
	return file, nil
}

func PacketConnFromFile(file *os.File) (net.PacketConn, error) {
	if !SupportsHotRestart() {
		return nil, errors.New("hot restart is unsupported on this linux architecture")
	}
	if file == nil {
		return nil, errors.New("packet connection file is required")
	}
	return net.FilePacketConn(file)
}

func PacketHandoffFiles() (*os.File, *os.File, error) {
	fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_SEQPACKET|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		return nil, nil, err
	}
	closeBoth := func() {
		_ = unix.Close(fds[0])
		_ = unix.Close(fds[1])
	}
	if err := unix.SetsockoptInt(fds[0], unix.SOL_SOCKET, unix.SO_SNDBUF, packetHandoffBufferBytes); err != nil {
		closeBoth()
		return nil, nil, err
	}
	if err := unix.SetNonblock(fds[0], true); err != nil {
		closeBoth()
		return nil, nil, err
	}
	return os.NewFile(uintptr(fds[0]), "hot-restart-packet-forward-parent"),
		os.NewFile(uintptr(fds[1]), "hot-restart-packet-forward-child"), nil
}
