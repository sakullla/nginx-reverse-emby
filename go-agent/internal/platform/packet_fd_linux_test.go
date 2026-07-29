//go:build linux && integration

package platform

import (
	"net"
	"os"
	"testing"

	"golang.org/x/sys/unix"
)

func TestIntegrationPacketConnFileRoundTripAndHandoffFilesUseCloseOnExec(t *testing.T) {
	if !SupportsHotRestart() {
		t.Skip("packet FD handoff is unsupported on this architecture")
	}
	packet, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer packet.Close()
	file, err := PacketConnFile(packet)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	flags, err := unix.FcntlInt(file.Fd(), unix.F_GETFD, 0)
	if err != nil {
		t.Fatal(err)
	}
	if flags&unix.FD_CLOEXEC == 0 {
		t.Fatal("packet file is missing FD_CLOEXEC")
	}
	inherited, err := PacketConnFromFile(file)
	if err != nil {
		t.Fatal(err)
	}
	defer inherited.Close()
	if inherited.LocalAddr().String() != packet.LocalAddr().String() {
		t.Fatalf("inherited address = %s, want %s", inherited.LocalAddr(), packet.LocalAddr())
	}

	parent, child, err := PacketHandoffFiles()
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	defer child.Close()
	for _, handoff := range []*os.File{parent, child} {
		flags, err := unix.FcntlInt(handoff.Fd(), unix.F_GETFD, 0)
		if err != nil {
			t.Fatal(err)
		}
		if flags&unix.FD_CLOEXEC == 0 {
			t.Fatal("handoff file is missing FD_CLOEXEC")
		}
	}
	handoffConn, err := PacketHandoffConnFromFile(child)
	if err != nil {
		t.Fatal(err)
	}
	if err := handoffConn.Close(); err != nil {
		t.Fatal(err)
	}
}
