package relay

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"time"
)

const maxUOTPacketSize = 1<<16 - 1 // The wire length is an unsigned 16-bit integer.

func WriteUOTPacket(w io.Writer, payload []byte) error {
	return writeUOTPacket(w, payload)
}

func WriteUOTPacketInto(w io.Writer, buf []byte, payload []byte) ([]byte, error) {
	return writeUOTPacketInto(w, buf, payload)
}

func ReadUOTPacket(r io.Reader) ([]byte, error) {
	return readUOTPacket(r)
}

func ReadUOTPacketInto(r io.Reader, buf []byte) ([]byte, error) {
	return readUOTPacketInto(r, buf)
}

// UOTReader retains a partially received datagram across read deadlines. Its
// zero value is ready to use; one reader must own each stream. Returned payloads
// remain valid until the next ReadPacket call. Storage grows with actual packet
// sizes instead of reserving 64 KiB for every idle relay association.
type UOTReader struct {
	header     [2]byte
	headerRead int
	payload    []byte
	read       int
}

func (r *UOTReader) ReadPacket(src io.Reader) ([]byte, error) {
	if r.headerRead < len(r.header) {
		n, err := io.ReadFull(src, r.header[r.headerRead:])
		r.headerRead += n
		if err != nil {
			return nil, err
		}
		size := int(binary.BigEndian.Uint16(r.header[:]))
		if cap(r.payload) < size {
			r.payload = make([]byte, size)
		} else {
			r.payload = r.payload[:size]
		}
	}
	n, err := io.ReadFull(src, r.payload[r.read:])
	r.read += n
	if err != nil {
		return nil, err
	}
	r.headerRead = 0
	r.read = 0
	return r.payload, nil
}

func writeUOTPacket(w io.Writer, payload []byte) error {
	_, err := writeUOTPacketInto(w, nil, payload)
	return err
}

func writeUOTPacketInto(w io.Writer, buf []byte, payload []byte) ([]byte, error) {
	if len(payload) > maxUOTPacketSize {
		return buf, fmt.Errorf("uot packet exceeds %d bytes", maxUOTPacketSize)
	}

	size := len(payload) + 2
	if cap(buf) < size {
		buf = make([]byte, size)
	} else {
		buf = buf[:size]
	}
	binary.BigEndian.PutUint16(buf[:2], uint16(len(payload)))
	copy(buf[2:], payload)
	return buf, writeAll(w, buf)
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
		return nil, fmt.Errorf("uot packet size %d exceeds buffer %d", size, len(buf))
	}
	payload := buf[:size]
	if _, err := io.ReadFull(r, payload); err != nil {
		return nil, err
	}
	return payload, nil
}

type UDPPacketPeer interface {
	Close() error
	SetReadDeadline(time.Time) error
	SetWriteDeadline(time.Time) error
	ReadPacket() ([]byte, error)
	WritePacket([]byte) error
}

type udpPacketPeer = UDPPacketPeer

type udpStreamPeer struct {
	conn   net.Conn
	reader UOTReader
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
	return p.reader.ReadPacket(p.conn)
}

func (p *udpStreamPeer) WritePacket(payload []byte) error {
	return writeUOTPacket(p.conn, payload)
}

type udpSocketPeer struct {
	conn    *net.UDPConn
	readBuf []byte
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
	if p.readBuf == nil {
		p.readBuf = make([]byte, maxUOTPacketSize)
	}
	n, err := p.conn.Read(p.readBuf)
	if err != nil {
		return nil, err
	}
	return p.readBuf[:n], nil
}

func (p *udpSocketPeer) WritePacket(payload []byte) error {
	_, err := p.conn.Write(payload)
	return err
}
