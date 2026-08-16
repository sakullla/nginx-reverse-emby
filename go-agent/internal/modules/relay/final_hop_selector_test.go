//go:build !integration

package relay

import (
	"errors"
	"io"

	"sync"

	"time"
)

type noopUDPPacketPeer struct{}

func (noopUDPPacketPeer) ReadPacket() ([]byte, error)      { return nil, io.EOF }
func (noopUDPPacketPeer) WritePacket([]byte) error         { return nil }
func (noopUDPPacketPeer) SetReadDeadline(time.Time) error  { return nil }
func (noopUDPPacketPeer) SetWriteDeadline(time.Time) error { return nil }
func (noopUDPPacketPeer) Close() error                     { return nil }

type closeUnblocksUDPPeer struct {
	closeOnce sync.Once
	closed    chan struct{}
}

func (p *closeUnblocksUDPPeer) Close() error {
	p.closeOnce.Do(func() {
		close(p.closed)
	})
	return nil
}

func (p *closeUnblocksUDPPeer) SetReadDeadline(time.Time) error  { return nil }
func (p *closeUnblocksUDPPeer) SetWriteDeadline(time.Time) error { return nil }

func (p *closeUnblocksUDPPeer) ReadPacket() ([]byte, error) {
	<-p.closed
	return nil, errors.New("local close")
}

func (p *closeUnblocksUDPPeer) WritePacket([]byte) error {
	return nil
}
