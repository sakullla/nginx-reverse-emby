package relay

import (
	"encoding/binary"
	"fmt"
	"io"
	"sync"
)

const (
	muxProtocolVersion = 1
	maxMuxPayloadBytes = maxRequestSize
	muxFrameHeaderSize = 11
)

var muxFrameWriteBufferPool = sync.Pool{
	New: func() any {
		return make([]byte, muxFrameHeaderSize+tlsTCPBulkFrameSize)
	},
}

type muxFrameType byte

const (
	muxFrameTypeOpen muxFrameType = 1 + iota
	muxFrameTypeOpenResult
	muxFrameTypeData
	muxFrameTypeFin
	muxFrameTypeRst
)

type muxFrameFlags byte

const (
	muxFlagAckRequired muxFrameFlags = 1 << iota
)

type muxFrame struct {
	Version  byte
	Type     muxFrameType
	Flags    muxFrameFlags
	StreamID uint32
	Payload  []byte

	pooledPayload []byte
}

func writeMuxFrame(w io.Writer, frame muxFrame) error {
	if frame.Version == 0 {
		frame.Version = muxProtocolVersion
	}
	if len(frame.Payload) > maxMuxPayloadBytes {
		return fmt.Errorf("mux payload exceeds %d bytes", maxMuxPayloadBytes)
	}

	if len(frame.Payload) <= tlsTCPBulkFrameSize {
		buf := muxFrameWriteBufferPool.Get().([]byte)
		defer muxFrameWriteBufferPool.Put(buf)

		header := buf[:muxFrameHeaderSize]
		header[0] = frame.Version
		header[1] = byte(frame.Type)
		header[2] = byte(frame.Flags)
		binary.BigEndian.PutUint32(header[3:7], frame.StreamID)
		binary.BigEndian.PutUint32(header[7:11], uint32(len(frame.Payload)))
		copy(buf[muxFrameHeaderSize:], frame.Payload)
		return writeAll(w, buf[:muxFrameHeaderSize+len(frame.Payload)])
	}

	var header [muxFrameHeaderSize]byte
	header[0] = frame.Version
	header[1] = byte(frame.Type)
	header[2] = byte(frame.Flags)
	binary.BigEndian.PutUint32(header[3:7], frame.StreamID)
	binary.BigEndian.PutUint32(header[7:11], uint32(len(frame.Payload)))
	if err := writeAll(w, header[:]); err != nil {
		return err
	}
	if len(frame.Payload) == 0 {
		return nil
	}
	return writeAll(w, frame.Payload)
}

func readMuxFrame(r io.Reader) (muxFrame, error) {
	var header [11]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return muxFrame{}, err
	}

	size := binary.BigEndian.Uint32(header[7:11])
	if size > maxMuxPayloadBytes {
		return muxFrame{}, fmt.Errorf("invalid mux payload size %d", size)
	}

	payload, pooledPayload := allocMuxPayload(size)
	if _, err := io.ReadFull(r, payload); err != nil {
		if pooledPayload != nil {
			tlsTCPBulkBufferPool.Put(pooledPayload)
		}
		return muxFrame{}, err
	}

	return muxFrame{
		Version:       header[0],
		Type:          muxFrameType(header[1]),
		Flags:         muxFrameFlags(header[2]),
		StreamID:      binary.BigEndian.Uint32(header[3:7]),
		Payload:       payload,
		pooledPayload: pooledPayload,
	}, nil
}

func allocMuxPayload(size uint32) ([]byte, []byte) {
	if size == 0 {
		return nil, nil
	}
	if size <= tlsTCPBulkFrameSize {
		buf := tlsTCPBulkBufferPool.Get().([]byte)
		return buf[:size], buf
	}
	return make([]byte, size), nil
}

func (f *muxFrame) releasePayload() {
	if f.pooledPayload != nil {
		tlsTCPBulkBufferPool.Put(f.pooledPayload)
		f.pooledPayload = nil
	}
}

func (f *muxFrame) takeReadChunk() tlsTCPReadChunk {
	chunk := tlsTCPReadChunk{
		payload:       f.Payload,
		pooledPayload: f.pooledPayload,
	}
	f.Payload = nil
	f.pooledPayload = nil
	return chunk
}
