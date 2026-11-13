//go:build !linux

package l4

import "net"

func prefetchTCPAvailable(net.Conn, int) ([]byte, error) { return nil, nil }
