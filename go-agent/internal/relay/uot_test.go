package relay

import (
	"bytes"
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
