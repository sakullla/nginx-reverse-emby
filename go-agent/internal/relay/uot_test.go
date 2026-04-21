package relay

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"
)

func TestUOTPacketRoundTrip(t *testing.T) {
	payload := []byte("uot-payload")
	var framed bytes.Buffer

	if err := writeUOTPacket(&framed, payload); err != nil {
		t.Fatalf("writeUOTPacket() error = %v", err)
	}

	got, err := readUOTPacket(&framed)
	if err != nil {
		t.Fatalf("readUOTPacket() error = %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("payload = %q", got)
	}
}

func TestReadUOTPacketIntoReusesCallerBuffer(t *testing.T) {
	payload := []byte("uot-reused-buffer")
	var framed bytes.Buffer

	if err := writeUOTPacket(&framed, payload); err != nil {
		t.Fatalf("writeUOTPacket() error = %v", err)
	}

	buf := make([]byte, maxUOTPacketSize)
	got, err := readUOTPacketInto(&framed, buf)
	if err != nil {
		t.Fatalf("readUOTPacketInto() error = %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("payload = %q", got)
	}
	if len(got) > 0 && &got[0] != &buf[0] {
		t.Fatalf("readUOTPacketInto() did not return caller buffer")
	}
}

func TestReadUOTPacketIntoReportsPacketAndBufferSizes(t *testing.T) {
	var framed bytes.Buffer
	var header [2]byte
	binary.BigEndian.PutUint16(header[:], 4)
	framed.Write(header[:])
	framed.Write([]byte("abcd"))

	_, err := readUOTPacketInto(&framed, make([]byte, 2))
	if err == nil {
		t.Fatal("readUOTPacketInto() error = nil")
	}
	if got, want := err.Error(), "uot packet size 4 exceeds buffer 2"; !strings.Contains(got, want) {
		t.Fatalf("readUOTPacketInto() error = %q, want containing %q", got, want)
	}
}
