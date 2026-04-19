package relay

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"time"
)

const maxUOTPacketSize = 64 * 1024

func WriteUOTPacket(w io.Writer, payload []byte) error {
	return writeUOTPacket(w, payload)
}

func ReadUOTPacket(r io.Reader) ([]byte, error) {
	return readUOTPacket(r)
}

func writeUOTPacket(w io.Writer, payload []byte) error {
	if len(payload) > maxUOTPacketSize {
		return fmt.Errorf("uot packet exceeds %d bytes", maxUOTPacketSize)
	}

	var header [2]byte
	binary.BigEndian.PutUint16(header[:], uint16(len(payload)))
	if err := writeAll(w, header[:]); err != nil {
		return err
	}
	return writeAll(w, payload)
}

func readUOTPacket(r io.Reader) ([]byte, error) {
	var header [2]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return nil, err
	}

	size := int(binary.BigEndian.Uint16(header[:]))
	payload := make([]byte, size)
	if _, err := io.ReadFull(r, payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func readUOTPacketInto(r io.Reader, buf []byte) ([]byte, error) {
	var header [2]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return nil, err
	}

	size := int(binary.BigEndian.Uint16(header[:]))
	if size > len(buf) {
		return nil, fmt.Errorf("uot packet exceeds buffer size %d", len(buf))
	}
	payload := buf[:size]
	if _, err := io.ReadFull(r, payload); err != nil {
		return nil, err
	}
	return payload, nil
}

type udpPacketPeer interface {
	Close() error
	SetReadDeadline(time.Time) error
	SetWriteDeadline(time.Time) error
	ReadPacket() ([]byte, error)
	WritePacket([]byte) error
}

type udpStreamPeer struct {
	conn net.Conn
}

func newUDPStreamPeer(conn net.Conn) udpPacketPeer {
	return &udpStreamPeer{conn: conn}
}

func (p *udpStreamPeer) Close() error {
	return p.conn.Close()
}

func (p *udpStreamPeer) SetReadDeadline(deadline time.Time) error {
	return p.conn.SetReadDeadline(deadline)
}

func (p *udpStreamPeer) SetWriteDeadline(deadline time.Time) error {
	return p.conn.SetWriteDeadline(deadline)
}

func (p *udpStreamPeer) ReadPacket() ([]byte, error) {
	return readUOTPacket(p.conn)
}

func (p *udpStreamPeer) WritePacket(payload []byte) error {
	return writeUOTPacket(p.conn, payload)
}

type udpSocketPeer struct {
	conn *net.UDPConn
}

func newUDPSocketPeer(conn *net.UDPConn) udpPacketPeer {
	return &udpSocketPeer{conn: conn}
}

func (p *udpSocketPeer) Close() error {
	return p.conn.Close()
}

func (p *udpSocketPeer) SetReadDeadline(deadline time.Time) error {
	return p.conn.SetReadDeadline(deadline)
}

func (p *udpSocketPeer) SetWriteDeadline(deadline time.Time) error {
	return p.conn.SetWriteDeadline(deadline)
}

func (p *udpSocketPeer) ReadPacket() ([]byte, error) {
	buf := make([]byte, maxUOTPacketSize)
	n, err := p.conn.Read(buf)
	if err != nil {
		return nil, err
	}
	return append([]byte(nil), buf[:n]...), nil
}

func (p *udpSocketPeer) WritePacket(payload []byte) error {
	_, err := p.conn.Write(payload)
	return err
}
