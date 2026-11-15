package network

import (
	"io"
	"net"
	"testing"
	"time"

	sdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

func TestRetiredOwnerRejectsLateResourceCreation(t *testing.T) {
	manager := NewManager()
	defer manager.Close()
	owner := testOwner(t, manager, "instance", "old", false)
	owner.Retire()
	if _, err := owner.newResource("stream", "tcp", sdk.PermissionManagedNetworkDial, nil, 0); err == nil {
		t.Fatal("in-flight dial created a resource after retirement")
	}
	if manager.resources.Load() != 0 {
		t.Fatal("rejected resource leaked its budget")
	}
}

func TestIntegrationManagedReplacementReleasesTCPAndUDPWhileTCPDrains(t *testing.T) {
	if testing.Short() {
		t.Skip("real listener lifecycle")
	}
	oldManager, childManager := NewManager(), NewManager()
	defer oldManager.Close()
	defer childManager.Close()
	old := testOwner(t, oldManager, "ss", "old", false)
	address := endpoint(t)
	listen := func(owner *Owner, protocol string) sdk.ManagedNetworkResponse {
		return call(t, owner, sdk.ManagedNetworkRequest{Action: sdk.ManagedNetworkListen, Endpoint: &address, Protocol: protocol, MaxFlows: 4, IdleMS: 30000})
	}
	tcpListen, udpListen := listen(old, "tcp"), listen(old, "udp")
	old.Activate()
	peer, err := net.DialTimeout("tcp", endpointString(address), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	_ = peer.SetDeadline(time.Now().Add(3 * time.Second))
	tcpFlow := call(t, old, sdk.ManagedNetworkRequest{Action: sdk.ManagedNetworkAccept, Handle: tcpListen.Handle, WaitMS: 1000})
	udp, err := net.Dial("udp", endpointString(address))
	if err != nil {
		t.Fatal(err)
	}
	defer udp.Close()
	_ = udp.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := udp.Write([]byte("old-packet")); err != nil {
		t.Fatal(err)
	}
	udpFlow := call(t, old, sdk.ManagedNetworkRequest{Action: sdk.ManagedNetworkAccept, Handle: udpListen.Handle, WaitMS: 1000})
	call(t, old, sdk.ManagedNetworkRequest{Action: sdk.ManagedNetworkReceive, Handle: udpFlow.Handle, MaxBytes: sdk.ManagedNetworkMaxDatagramBytes, WaitMS: 1000})
	oldManager.RetireForReplacement()
	oldManager.RetireForReplacement() // Cleanup retries must remain harmless.
	if _, err := oldManager.Stage(old.authority); err == nil {
		t.Fatal("retired process admitted a new generation")
	}
	if _, err := old.newResource("datagram", "udp", sdk.PermissionManagedNetworkListen, nil, 0); err == nil {
		t.Fatal("late UDP accept resurrected a retired listener")
	}
	child := testOwner(t, childManager, "ss", "child", false)
	childTCP, childUDP := listen(child, "tcp"), listen(child, "udp")
	child.Activate()
	// The old accepted TCP socket is independent of its listening socket.
	if _, err := peer.Write([]byte("draining")); err != nil {
		t.Fatal(err)
	}
	r := call(t, old, sdk.ManagedNetworkRequest{Action: sdk.ManagedNetworkRead, Handle: tcpFlow.Handle, MaxBytes: 100, WaitMS: 1000})
	if string(r.Data) != "draining" {
		t.Fatalf("old TCP read = %q", r.Data)
	}
	call(t, old, sdk.ManagedNetworkRequest{Action: sdk.ManagedNetworkWrite, Handle: tcpFlow.Handle, Data: []byte("reply"), WaitMS: 1000})
	buf := make([]byte, 5)
	if _, err := io.ReadFull(peer, buf); err != nil || string(buf) != "reply" {
		t.Fatalf("old TCP reply = %q, %v", buf, err)
	}
	newPeer, err := net.DialTimeout("tcp", endpointString(address), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer newPeer.Close()
	call(t, child, sdk.ManagedNetworkRequest{Action: sdk.ManagedNetworkAccept, Handle: childTCP.Handle, WaitMS: 1000})
	if _, err := udp.Write([]byte("new-packet")); err != nil {
		t.Fatal(err)
	}
	newUDP := call(t, child, sdk.ManagedNetworkRequest{Action: sdk.ManagedNetworkAccept, Handle: childUDP.Handle, WaitMS: 1000})
	r = call(t, child, sdk.ManagedNetworkRequest{Action: sdk.ManagedNetworkReceive, Handle: newUDP.Handle, MaxBytes: sdk.ManagedNetworkMaxDatagramBytes, WaitMS: 1000})
	if string(r.Data) != "new-packet" {
		t.Fatalf("child UDP read = %q", r.Data)
	}
	if err := oldManager.Close(); err != nil {
		t.Fatal(err)
	}
	// Parent final cleanup must not touch the replacement's UDP listener.
	call(t, child, sdk.ManagedNetworkRequest{Action: sdk.ManagedNetworkSend, Handle: newUDP.Handle, Data: []byte("child"), WaitMS: 1000})
	if n, err := udp.Read(buf); err != nil || string(buf[:n]) != "child" {
		t.Fatalf("child UDP reply = %q, %v", buf[:n], err)
	}
	if oldManager.resources.Load() != 0 || oldManager.buffered.Load() != 0 {
		t.Fatal("retired process leaked managed resources")
	}
}
