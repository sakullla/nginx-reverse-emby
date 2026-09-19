//go:build integration

package hotrestart

import (
	"errors"
	"net"
	"runtime"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/platform"
)

func TestIntegrationPacketHandoffMatrix(t *testing.T) {
	requirePacketHandoff(t)

	t.Run("activation gate drains queued delivery before physical authority", func(t *testing.T) {
		fixture := newPacketHandoffFixture(t)

		preActivation := fixture.read()
		if err := fixture.forwarder.Send([]byte("queued"), &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 4567}); err != nil {
			t.Fatal(err)
		}
		assertPacketReadBlocked(t, preActivation, "activation")
		if err := fixture.child.Activate(); err != nil {
			t.Fatal(err)
		}
		assertPacketRead(t, preActivation, "queued", "127.0.0.1:4567")

		physicalRead := fixture.read()
		if err := fixture.forwarder.Barrier(); err != nil {
			t.Fatal(err)
		}
		if err := fixture.child.TakeAuthority(); err != nil {
			t.Fatal(err)
		}
		sender, err := net.ListenPacket("udp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer sender.Close()
		if _, err := sender.WriteTo([]byte("physical"), fixture.physical.LocalAddr()); err != nil {
			t.Fatal(err)
		}
		assertPacketRead(t, physicalRead, "physical", sender.LocalAddr().String())
	})

	t.Run("descriptor identity and index tampering fail closed", func(t *testing.T) {
		for _, tc := range []struct {
			name   string
			mutate func(*PacketDescriptor)
		}{
			{name: "missing identity", mutate: func(descriptor *PacketDescriptor) { descriptor.ID = "" }},
			{name: "duplicate file index", mutate: func(descriptor *PacketDescriptor) {
				descriptor.ForwardFileIndex = descriptor.FileIndex
			}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				export := newPacketExportFixture(t)
				descriptors := append([]PacketDescriptor(nil), export.bundle.Descriptors...)
				tc.mutate(&descriptors[0])
				if set, err := ImportPacketConns(descriptors, export.bundle.Files); err == nil {
					_ = set.Close()
					t.Fatal("ImportPacketConns() accepted a tampered descriptor")
				}
			})
		}
	})

	t.Run("authority reservation blocks close until ownership completes", func(t *testing.T) {
		fixture := newPacketHandoffFixture(t)
		if err := fixture.child.Activate(); err != nil {
			t.Fatal(err)
		}
		readDone := make(chan error, 1)
		go func() {
			buffer := make([]byte, 64)
			_, _, err := fixture.child.ReadFrom(buffer)
			readDone <- err
		}()
		if err := fixture.forwarder.Barrier(); err != nil {
			t.Fatal(err)
		}
		reservation, err := fixture.child.ReserveAuthority()
		if err != nil {
			t.Fatal(err)
		}
		closeDone := make(chan error, 1)
		go func() { closeDone <- fixture.child.Close() }()
		assertCloseBlocked(t, closeDone, "active authority reservation")
		reservation.Commit()
		assertCloseBlocked(t, closeDone, "committed authority reservation")
		reservation.Finish()
		if err := <-closeDone; err != nil {
			t.Fatal(err)
		}
		if err := <-readDone; !errors.Is(err, net.ErrClosed) {
			t.Fatalf("ReadFrom() error = %v, want net.ErrClosed", err)
		}
	})
}

type packetHandoffExportFixture struct {
	physical net.PacketConn
	bundle   *PacketBundle
}

func newPacketExportFixture(t *testing.T) *packetHandoffExportFixture {
	t.Helper()
	physical, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := ExportPacketConns(map[string]net.PacketConn{"packet": physical})
	if err != nil {
		_ = physical.Close()
		t.Fatal(err)
	}
	fixture := &packetHandoffExportFixture{physical: physical, bundle: bundle}
	t.Cleanup(func() {
		_ = errors.Join(fixture.bundle.Close(), fixture.physical.Close())
	})
	return fixture
}

type packetHandoffFixture struct {
	*packetHandoffExportFixture
	forwarder *PacketForwarder
	set       *PacketSet
	child     *GatedPacketConn
}

func newPacketHandoffFixture(t *testing.T) *packetHandoffFixture {
	t.Helper()
	export := newPacketExportFixture(t)
	forwarder := export.bundle.TakeForwarders()["packet"]
	set, err := ImportPacketConns(export.bundle.Descriptors, export.bundle.Files)
	if err != nil {
		_ = forwarder.Close()
		t.Fatal(err)
	}
	fixture := &packetHandoffFixture{
		packetHandoffExportFixture: export,
		forwarder:                  forwarder,
		set:                        set,
		child:                      set.Conns["packet"],
	}
	t.Cleanup(func() {
		_ = errors.Join(fixture.set.Close(), fixture.forwarder.Close())
	})
	return fixture
}

type packetReadResult struct {
	payload string
	remote  net.Addr
	err     error
}

func (f *packetHandoffFixture) read() <-chan packetReadResult {
	result := make(chan packetReadResult, 1)
	go func() {
		buffer := make([]byte, 64)
		n, remote, err := f.child.ReadFrom(buffer)
		result <- packetReadResult{payload: string(buffer[:n]), remote: remote, err: err}
	}()
	return result
}

func assertPacketReadBlocked(t *testing.T, result <-chan packetReadResult, phase string) {
	t.Helper()
	select {
	case got := <-result:
		t.Fatalf("ReadFrom() completed before %s: %+v", phase, got)
	case <-time.After(50 * time.Millisecond):
	}
}

func assertPacketRead(t *testing.T, result <-chan packetReadResult, payload, remote string) {
	t.Helper()
	select {
	case got := <-result:
		if got.err != nil || got.payload != payload || got.remote == nil || got.remote.String() != remote {
			t.Fatalf("packet read = %+v, want payload %q from %q", got, payload, remote)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for packet %q", payload)
	}
}

func assertCloseBlocked(t *testing.T, result <-chan error, phase string) {
	t.Helper()
	select {
	case err := <-result:
		t.Fatalf("Close() crossed %s: %v", phase, err)
	case <-time.After(50 * time.Millisecond):
	}
}

func requirePacketHandoff(t *testing.T) {
	t.Helper()
	if !platform.SupportsHotRestart() {
		t.Skipf("packet FD handoff is unsupported on %s/%s", runtime.GOOS, runtime.GOARCH)
	}
}
