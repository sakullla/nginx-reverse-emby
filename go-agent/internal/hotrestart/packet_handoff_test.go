//go:build integration

package hotrestart

import (
	"errors"
	"net"
	"os"
	"runtime"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/platform"
)

func TestIntegrationPacketHandoffGatesForwardingThenTakesPhysicalAuthority(t *testing.T) {
	requirePacketHandoff(t)
	physical, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer physical.Close()
	bundle, err := ExportPacketConns(map[string]net.PacketConn{"l4:udp:test": physical})
	if err != nil {
		t.Fatal(err)
	}
	forwarders := bundle.TakeForwarders()
	defer func() { _ = forwarders["l4:udp:test"].Close() }()
	set, err := ImportPacketConns(bundle.Descriptors, bundle.Files)
	if err != nil {
		t.Fatal(err)
	}
	defer set.Close()
	child := set.Conns["l4:udp:test"]

	type packetResult struct {
		payload string
		remote  net.Addr
		err     error
	}
	read := func() <-chan packetResult {
		result := make(chan packetResult, 1)
		go func() {
			buffer := make([]byte, 64)
			n, remote, err := child.ReadFrom(buffer)
			result <- packetResult{payload: string(buffer[:n]), remote: remote, err: err}
		}()
		return result
	}

	preActivation := read()
	if err := forwarders["l4:udp:test"].Send([]byte("queued"), &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 4567}); err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-preActivation:
		t.Fatalf("ReadFrom() completed before activation: %+v", result)
	case <-time.After(50 * time.Millisecond):
	}
	if err := child.Activate(); err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-preActivation:
		if result.err != nil || result.payload != "queued" || result.remote.String() != "127.0.0.1:4567" {
			t.Fatalf("forwarded result = %+v", result)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("forwarded packet was not delivered after activation")
	}

	physicalRead := read()
	if err := forwarders["l4:udp:test"].Barrier(); err != nil {
		t.Fatal(err)
	}
	if err := child.TakeAuthority(); err != nil {
		t.Fatal(err)
	}
	sender, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer sender.Close()
	if _, err := sender.WriteTo([]byte("physical"), physical.LocalAddr()); err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-physicalRead:
		if result.err != nil || result.payload != "physical" || result.remote.String() != sender.LocalAddr().String() {
			t.Fatalf("physical result = %+v", result)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("packet connection did not take physical authority")
	}
}

func TestIntegrationPacketHandoffDrainsQueuedForwardingBeforeParentCrashTakeover(t *testing.T) {
	requirePacketHandoff(t)
	physical, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer physical.Close()
	bundle, err := ExportPacketConns(map[string]net.PacketConn{"packet": physical})
	if err != nil {
		t.Fatal(err)
	}
	forwarder := bundle.TakeForwarders()["packet"]
	set, err := ImportPacketConns(bundle.Descriptors, bundle.Files)
	if err != nil {
		t.Fatal(err)
	}
	defer set.Close()
	child := set.Conns["packet"]
	if err := child.Activate(); err != nil {
		t.Fatal(err)
	}
	remote := &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 4567}
	if err := forwarder.Send([]byte("queued-before-crash"), remote); err != nil {
		t.Fatal(err)
	}
	if err := forwarder.Close(); err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, 64)
	n, _, err := child.ReadFrom(buffer)
	if err != nil || string(buffer[:n]) != "queued-before-crash" {
		t.Fatalf("queued forward read = %q, %v", buffer[:n], err)
	}
	result := make(chan string, 1)
	go func() {
		n, _, err := child.ReadFrom(buffer)
		if err != nil {
			result <- "error: " + err.Error()
			return
		}
		result <- string(buffer[:n])
	}()
	if err := child.TakeAuthority(); err != nil {
		t.Fatal(err)
	}
	sender, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer sender.Close()
	if _, err := sender.WriteTo([]byte("physical-after-crash"), physical.LocalAddr()); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-result:
		if got != "physical-after-crash" {
			t.Fatalf("post-crash authority payload = %q", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("child did not take physical authority after parent channel closed")
	}
}

func TestIntegrationPacketAuthorityReservationBlocksCloseUntilCommit(t *testing.T) {
	requirePacketHandoff(t)
	physical, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := ExportPacketConns(map[string]net.PacketConn{"packet": physical})
	if err != nil {
		_ = physical.Close()
		t.Fatal(err)
	}
	forwarder := bundle.TakeForwarders()["packet"]
	set, err := ImportPacketConns(bundle.Descriptors, bundle.Files)
	if err != nil {
		_ = forwarder.Close()
		_ = physical.Close()
		t.Fatal(err)
	}
	child := set.Conns["packet"]
	if err := child.Activate(); err != nil {
		t.Fatal(err)
	}
	readDone := make(chan error, 1)
	go func() {
		buffer := make([]byte, 64)
		_, _, err := child.ReadFrom(buffer)
		readDone <- err
	}()
	if err := forwarder.Barrier(); err != nil {
		t.Fatal(err)
	}
	reservation, err := child.ReserveAuthority()
	if err != nil {
		t.Fatal(err)
	}
	closeDone := make(chan error, 1)
	go func() { closeDone <- child.Close() }()
	select {
	case err := <-closeDone:
		t.Fatalf("Close() crossed an active authority reservation: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	reservation.Commit()
	select {
	case err := <-closeDone:
		t.Fatalf("Close() crossed a committed but unfinished authority reservation: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	reservation.Finish()
	if err := <-closeDone; err != nil {
		t.Fatal(err)
	}
	if err := <-readDone; !errors.Is(err, net.ErrClosed) {
		t.Fatalf("ReadFrom() error = %v, want closed after committed reservation", err)
	}
	if err := errors.Join(forwarder.Close(), bundle.Close(), physical.Close()); err != nil {
		t.Fatal(err)
	}
}

func TestIntegrationPacketHandoffRejectsDescriptorFileIndexAndIdentityTampering(t *testing.T) {
	requirePacketHandoff(t)
	for _, tc := range []struct {
		name   string
		mutate func([]PacketDescriptor)
	}{
		{name: "duplicate index", mutate: func(descriptors []PacketDescriptor) { descriptors[0].ForwardFileIndex = descriptors[0].FileIndex }},
		{name: "wrong address", mutate: func(descriptors []PacketDescriptor) { descriptors[0].Address = "127.0.0.1:1" }},
		{name: "missing identity", mutate: func(descriptors []PacketDescriptor) { descriptors[0].ID = "" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			physical, err := net.ListenPacket("udp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer physical.Close()
			bundle, err := ExportPacketConns(map[string]net.PacketConn{"packet": physical})
			if err != nil {
				t.Fatal(err)
			}
			defer bundle.Close()
			descriptors := append([]PacketDescriptor(nil), bundle.Descriptors...)
			tc.mutate(descriptors)
			if _, err := ImportPacketConns(descriptors, bundle.Files); err == nil {
				t.Fatal("ImportPacketConns() accepted tampered descriptor")
			}
		})
	}
}

func TestIntegrationPacketHandoffRepeatedCloseReturnsFileDescriptorsToBaseline(t *testing.T) {
	requirePacketHandoff(t)
	baseline, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Fatal(err)
	}
	for iteration := 0; iteration < 10; iteration++ {
		physical, err := net.ListenPacket("udp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		bundle, err := ExportPacketConns(map[string]net.PacketConn{"packet": physical})
		if err != nil {
			_ = physical.Close()
			t.Fatal(err)
		}
		forwarder := bundle.TakeForwarders()["packet"]
		set, err := ImportPacketConns(bundle.Descriptors, bundle.Files)
		if err != nil {
			_ = forwarder.Close()
			_ = bundle.Close()
			_ = physical.Close()
			t.Fatal(err)
		}
		if err := errors.Join(set.Close(), forwarder.Close(), bundle.Close(), physical.Close()); err != nil {
			t.Fatal(err)
		}
	}
	runtime.GC()
	deadline := time.Now().Add(5 * time.Second)
	for {
		current, err := os.ReadDir("/proc/self/fd")
		if err != nil {
			t.Fatal(err)
		}
		if len(current) <= len(baseline) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("open file descriptors after repeated handoff = %d, baseline %d", len(current), len(baseline))
		}
		time.Sleep(25 * time.Millisecond)
		runtime.GC()
	}
}

func requirePacketHandoff(t *testing.T) {
	t.Helper()
	if !platform.SupportsHotRestart() {
		t.Skipf("packet FD handoff is unsupported on %s/%s", runtime.GOOS, runtime.GOARCH)
	}
}
