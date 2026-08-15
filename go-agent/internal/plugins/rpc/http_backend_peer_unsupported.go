//go:build !linux

package rpc

import (
	"errors"
	"net"
)

func validateProviderPeer(net.Conn, int, int) (providerPeerIdentity, error) {
	return providerPeerIdentity{}, errors.New("HTTP backend provider peer credentials are unavailable on this platform")
}
