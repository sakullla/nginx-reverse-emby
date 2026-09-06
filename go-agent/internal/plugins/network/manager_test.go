package network

import (
	"context"
	"errors"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	sdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"
)

func testOwner(t *testing.T, m *Manager, instance, generation string, deny bool) *Owner {
	t.Helper()
	owner, err := m.Stage(Authority{InstanceID: instance, Generation: generation, Grants: []model.PluginGrantProjection{{Name: sdk.PermissionManagedNetworkListen}, {Name: sdk.PermissionManagedNetworkDial}}, Admit: func(ctx context.Context, source sdk.ManagedSourceMetadata) error {
		if source.Validate() != nil || deny {
			return errors.New("denied")
		}
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { owner.Close() })
	return owner
}

var requestSequence atomic.Uint64

func call(t *testing.T, o *Owner, r sdk.ManagedNetworkRequest) sdk.ManagedNetworkResponse {
	t.Helper()
	r.Binding = o.Binding()
	r.RequestID = token()
	response, err := o.Handle(t.Context(), r)
	if err != nil {
		t.Fatalf("%s: %v", r.Action, err)
	}
	return response
}
func endpoint(t *testing.T) sdk.ManagedNetworkEndpoint {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	return sdk.ManagedNetworkEndpoint{Host: "127.0.0.1", Port: port}
}
func TestManagedTCPAdmissionHalfCloseAndGeneration(t *testing.T) {
	m := NewManager()
	defer m.Close()
	old := testOwner(t, m, "instance", "generation-a", false)
	address := endpoint(t)
	request := sdk.ManagedNetworkRequest{Action: sdk.ManagedNetworkListen, Endpoint: &address, Protocol: "tcp", MaxFlows: 4, IdleMS: 30000}
	listening := call(t, old, request)
	old.Activate()
	peer, err := net.Dial("tcp", endpointString(address))
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	peer.SetDeadline(time.Now().Add(2 * time.Second))
	flow := call(t, old, sdk.ManagedNetworkRequest{Action: sdk.ManagedNetworkAccept, Handle: listening.Handle, WaitMS: 1000})
	if flow.Source == nil || flow.Source.Peer.Host != "127.0.0.1" {
		t.Fatal("missing socket authority")
	}
	idle := call(t, old, sdk.ManagedNetworkRequest{Action: sdk.ManagedNetworkRead, Handle: flow.Handle, MaxBytes: 64, WaitMS: 1})
	if !idle.Idle {
		t.Fatal("idle read not safe poll")
	}
	peer.Write([]byte("first"))
	peer.(*net.TCPConn).CloseWrite()
	data := call(t, old, sdk.ManagedNetworkRequest{Action: sdk.ManagedNetworkRead, Handle: flow.Handle, MaxBytes: 64, WaitMS: 1000})
	if string(data.Data) != "first" {
		t.Fatal(data)
	}
	next := testOwner(t, m, "instance", "generation-b", false)
	nextListener := call(t, next, request)
	next.Activate()
	old.Retire()
	old.Activate()
	nextPeer, err := net.Dial("tcp", endpointString(address))
	if err != nil {
		t.Fatal(err)
	}
	defer nextPeer.Close()
	acceptedNext := call(t, next, sdk.ManagedNetworkRequest{Action: sdk.ManagedNetworkAccept, Handle: nextListener.Handle, WaitMS: 1000})
	if acceptedNext.Handle.Binding.Generation != next.Binding().Generation {
		t.Fatal("retired owner regained listener")
	}
	call(t, old, sdk.ManagedNetworkRequest{Action: sdk.ManagedNetworkWrite, Handle: flow.Handle, Data: []byte("reply"), WaitMS: 1000})
	call(t, old, sdk.ManagedNetworkRequest{Action: sdk.ManagedNetworkHalfClose, Handle: flow.Handle, Direction: "write"})
	reply, err := io.ReadAll(peer)
	if err != nil || string(reply) != "reply" {
		t.Fatalf("halfclose: %q %v", reply, err)
	}
	wrong := sdk.ManagedNetworkRequest{Action: sdk.ManagedNetworkRead, Binding: next.Binding(), RequestID: token(), Handle: flow.Handle, MaxBytes: 1, WaitMS: 1}
	if _, err := next.Handle(t.Context(), wrong); err == nil {
		t.Fatal("cross generation read accepted")
	}
	conflict := testOwner(t, m, "foreign", "generation-c", false)
	request.Binding = conflict.Binding()
	request.RequestID = token()
	if _, err := conflict.Handle(t.Context(), request); err == nil {
		t.Fatal("foreign listener stole endpoint")
	}
}
func TestManagedDeniedSourceAndUDPResponses(t *testing.T) {
	m := NewManager()
	defer m.Close()
	denied := testOwner(t, m, "denied", "generation-a", true)
	address := endpoint(t)
	listening := call(t, denied, sdk.ManagedNetworkRequest{Action: sdk.ManagedNetworkListen, Endpoint: &address, Protocol: "tcp", MaxFlows: 2, IdleMS: 1000})
	denied.Activate()
	peer, err := net.Dial("tcp", endpointString(address))
	if err != nil {
		t.Fatal(err)
	}
	peer.Write([]byte("must-not-arrive"))
	peer.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := peer.Read(make([]byte, 1)); err == nil {
		t.Fatal("denied peer stayed open")
	}
	peer.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Millisecond)
	defer cancel()
	if _, err := denied.Handle(ctx, sdk.ManagedNetworkRequest{Action: sdk.ManagedNetworkAccept, Binding: denied.Binding(), RequestID: token(), Handle: listening.Handle, WaitMS: 100}); err == nil {
		t.Fatal("denied source delivered")
	}
	owner := testOwner(t, m, "udp", "generation-u", false)
	packetReservation, reserveErr := net.ListenPacket("udp", "127.0.0.1:0")
	if reserveErr != nil {
		t.Fatal(reserveErr)
	}
	address = sdk.ManagedNetworkEndpoint{Host: "127.0.0.1", Port: packetReservation.LocalAddr().(*net.UDPAddr).Port}
	packetReservation.Close()
	listening = call(t, owner, sdk.ManagedNetworkRequest{Action: sdk.ManagedNetworkListen, Endpoint: &address, Protocol: "udp", MaxFlows: 1, IdleMS: 1000})
	owner.Activate()
	udp, err := net.Dial("udp", endpointString(address))
	if err != nil {
		t.Fatal(err)
	}
	defer udp.Close()
	udp.SetDeadline(time.Now().Add(2 * time.Second))
	udp.Write([]byte("packet"))
	flow := call(t, owner, sdk.ManagedNetworkRequest{Action: sdk.ManagedNetworkAccept, Handle: listening.Handle, WaitMS: 1000})
	received := call(t, owner, sdk.ManagedNetworkRequest{Action: sdk.ManagedNetworkReceive, Handle: flow.Handle, MaxBytes: sdk.ManagedNetworkMaxDatagramBytes, WaitMS: 1000})
	if string(received.Data) != "packet" {
		t.Fatal(received)
	}
	for _, value := range []string{"one", "two", ""} {
		call(t, owner, sdk.ManagedNetworkRequest{Action: sdk.ManagedNetworkSend, Handle: flow.Handle, Data: []byte(value), WaitMS: 1000})
		buffer := make([]byte, 32)
		n, err := udp.Read(buffer)
		if err != nil || string(buffer[:n]) != value {
			t.Fatalf("multiresponse %q %v", buffer[:n], err)
		}
	}
	owner.Close()
	if _, err := owner.Handle(t.Context(), sdk.ManagedNetworkRequest{Action: sdk.ManagedNetworkSend, Binding: owner.Binding(), RequestID: token(), Handle: flow.Handle, Data: []byte("old"), WaitMS: 1}); err == nil {
		t.Fatal("closed generation accepted packet")
	}
}
func TestManagedDatagramBackpressureReclaimsQueuedBytes(t *testing.T) {
	manager := NewManager()
	defer manager.Close()
	owner := testOwner(t, manager, "instance", "generation", false)
	flow, err := owner.newResource("datagram", "udp", sdk.PermissionManagedNetworkListen, nil, 0, func(flow *resource) { flow.datagrams = make(chan []byte, maxPending) })
	if err != nil {
		t.Fatal(err)
	}
	for range maxPending {
		flow.enqueuePacket(make([]byte, 1024))
	}
	if manager.buffered.Load() != maxPending*1024 {
		t.Fatal("queued bytes were not charged")
	}
	flow.enqueuePacket([]byte("overflow"))
	if !flow.closed.Load() || manager.buffered.Load() != 0 || manager.resources.Load() != 0 {
		t.Fatal("overflow did not close and reclaim flow")
	}
}
