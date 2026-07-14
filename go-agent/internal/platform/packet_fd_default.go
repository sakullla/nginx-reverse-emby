//go:build !linux

package platform

import (
	"errors"
	"net"
	"os"
)

func PacketConnFile(net.PacketConn) (*os.File, error) {
	return nil, errors.New("hot restart packet inheritance is only supported on linux")
}

func PacketConnFromFile(*os.File) (net.PacketConn, error) {
	return nil, errors.New("hot restart packet inheritance is only supported on linux")
}

func PacketHandoffFiles() (*os.File, *os.File, error) {
	return nil, nil, errors.New("hot restart packet forwarding is only supported on linux")
}

func PacketHandoffConnFromFile(*os.File) (net.Conn, error) {
	return nil, errors.New("hot restart packet forwarding is only supported on linux")
}
