//go:build linux

package rpc

import (
	"errors"
	"fmt"
	"net"

	"golang.org/x/sys/unix"
)

func validateProviderPeer(connection net.Conn, processGroup, sandboxUID int) (providerPeerIdentity, error) {
	unixConnection, ok := connection.(*net.UnixConn)
	if !ok {
		return providerPeerIdentity{}, errors.New("HTTP backend provider connection is not Unix")
	}
	raw, err := unixConnection.SyscallConn()
	if err != nil {
		return providerPeerIdentity{}, err
	}
	var credential *unix.Ucred
	var controlErr error
	if err := raw.Control(func(fd uintptr) {
		credential, controlErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	}); err != nil {
		return providerPeerIdentity{}, err
	}
	if controlErr != nil || credential == nil {
		return providerPeerIdentity{}, errors.Join(errors.New("read HTTP backend provider peer credential"), controlErr)
	}
	peer := providerPeerIdentity{PID: int(credential.Pid), UID: int(credential.Uid), GID: int(credential.Gid)}
	if peer.PID <= 0 || peer.UID < 0 {
		return providerPeerIdentity{}, errors.New("HTTP backend provider peer credential is invalid")
	}
	group, err := unix.Getpgid(peer.PID)
	if err != nil || group != processGroup {
		return providerPeerIdentity{}, fmt.Errorf("HTTP backend provider peer is outside the launched process group: %w", err)
	}
	if sandboxUID > 0 && peer.UID != sandboxUID {
		return providerPeerIdentity{}, errors.New("HTTP backend provider peer UID differs from the leased sandbox UID")
	}
	return peer, nil
}
