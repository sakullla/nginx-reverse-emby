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
