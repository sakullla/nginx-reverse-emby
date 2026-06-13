//go:build !linux

package wireguard

import "net"

const hostGSOControlSize = 0

func hostListenConfig() *net.ListenConfig {
	return &net.ListenConfig{}
}

func hostSupportsUDPOffload(_ *net.UDPConn) (txOffload, rxOffload bool) {
	return false, false
}

func hostGetGSOSize(_ []byte) (int, error) {
	return 0, nil
}

func hostSetGSOSize(_ *[]byte, _ uint16) {
}
