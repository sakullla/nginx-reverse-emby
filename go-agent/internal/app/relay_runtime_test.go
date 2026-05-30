package app

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/proxyproto"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/relay"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/wireguard"
)

func TestRelayFinalHopUDPEgressPreservesTargetForSOCKS5(t *testing.T) {
	proxyAddr, packetCh := startAppObservingSOCKS5UDPProxy(t)
	profileID := 17
	dialer := relayFinalHopDialer([]model.EgressProfile{{
		ID:       profileID,
		Name:     "socks-udp",
		Type:     "socks",
		ProxyURL: "socks5h://" + proxyAddr,
		Enabled:  true,
	}}, nil)

	peer, err := dialer.OpenUDP(context.Background(), "backend.example:5300", &profileID)
	if err != nil {
		t.Fatalf("OpenUDP() error = %v", err)
	}
	defer peer.Close()

	if err := peer.WritePacket([]byte("ping")); err != nil {
		t.Fatalf("WritePacket() error = %v", err)
	}

	packet := waitForAppSOCKS5UDPPacket(t, packetCh)
	if packet.Target != "backend.example:5300" {
		t.Fatalf("SOCKS5 UDP target = %q, want backend.example:5300", packet.Target)
	}
	if string(packet.Payload) != "ping" {
		t.Fatalf("SOCKS5 UDP payload = %q, want ping", string(packet.Payload))
	}
}

func TestRelayRuntimeManagerAppliesWireGuardEgressProfilesFromInlineConfig(t *testing.T) {
	var created []wireguard.Config
	provider := &testRelayTLSProvider{
		certificates: map[int]tls.Certificate{
			1: mustIssueTestTLSCertificate(t),
		},
	}
	manager := newRelayRuntimeManager(provider)
	manager.egressWireGuard = newEgressWireGuardRuntime(func(_ context.Context, cfg wireguard.Config) (wireguard.Runtime, error) {
		created = append(created, cfg)
		return &testAppWireGuardRuntime{}, nil
	})
	defer manager.Close()

	profileID := 91
	if err := manager.ApplyWithWireGuardAndEgressProfiles(
		context.Background(),
		[]model.RelayListener{runtimeTestRelayListener(pickFreeTCPPort(t), 1)},
		nil,
		[]model.EgressProfile{validAppWireGuardEgressProfile(profileID)},
	); err != nil {
		t.Fatalf("ApplyWithWireGuardAndEgressProfiles() error = %v", err)
	}

	if len(created) != 1 {
		t.Fatalf("wireguard egress runtime creations = %d, want 1", len(created))
	}
	if got := created[0].ID; got != profileID {
		t.Fatalf("wireguard egress runtime profile ID = %d, want %d", got, profileID)
	}
	if got := created[0].PrivateKey; got != "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=" {
		t.Fatalf("wireguard egress runtime private key = %q, want inline egress config key", got)
	}
}

func TestRelayRuntimeManagerRollsBackEgressWireGuardBeforeRestoringPreviousServer(t *testing.T) {
	provider := &testRelayTLSProvider{
		certificates: map[int]tls.Certificate{
			1: mustIssueTestTLSCertificate(t),
		},
	}
	ctx := context.Background()
	listenErr := fmt.Errorf("wireguard listen failed after replacement")
	var candidateEgressRuntime *testAppWireGuardRuntime
	restoreObserved := false
	listenCalls := 0
	wireGuardRuntime := &testAppWireGuardRuntime{}
	wireGuardRuntime.onListenTCP = func(_ context.Context, address string) (net.Listener, error) {
		listenCalls++
		switch listenCalls {
		case 1:
			return net.Listen("tcp", address)
		case 2:
			return nil, fmt.Errorf("bind tcp %s: address already in use", address)
		case 3:
			return nil, listenErr
		default:
			restoreObserved = true
			if candidateEgressRuntime == nil || !candidateEgressRuntime.closed {
				return nil, fmt.Errorf("egress wireguard transaction was not rolled back before restore")
			}
			return net.Listen("tcp", address)
		}
	}

	manager := newRelayRuntimeManagerWithWireGuard(provider, newSharedWireGuardRuntimeWithFactory(func(context.Context, wireguard.Config) (wireguard.Runtime, error) {
		return wireGuardRuntime, nil
	}))
	egressRuntimeCreations := 0
	manager.egressWireGuard = newEgressWireGuardRuntime(func(context.Context, wireguard.Config) (wireguard.Runtime, error) {
		runtime := &testAppWireGuardRuntime{}
		egressRuntimeCreations++
		if egressRuntimeCreations == 2 {
			candidateEgressRuntime = runtime
		}
		return runtime, nil
	})
	defer manager.Close()

	profileID := 92
	listenPort := pickFreeTCPPort(t)
	listener := runtimeTestRelayListener(listenPort, 1)
	listener.TransportMode = relay.ListenerTransportModeWireGuard
	listener.WireGuardProfileID = &profileID
	wireGuardProfile := validAppWireGuardProfile(profileID)
	egressProfile := validAppWireGuardEgressProfile(193)
	if err := manager.ApplyWithWireGuardAndEgressProfiles(ctx, []model.RelayListener{listener}, []model.WireGuardProfile{wireGuardProfile}, []model.EgressProfile{egressProfile}); err != nil {
		t.Fatalf("failed to apply initial relay runtime: %v", err)
	}
	waitForPortState(t, listenPort, true)

	nextEgressProfile := egressProfile
	nextEgressProfile.Revision++
	nextEgressProfile.WireGuardConfig.Peers[0].Endpoint = "127.0.0.1:51821"
	reconfigured := listener
	reconfigured.Revision++
	err := manager.ApplyWithWireGuardAndEgressProfiles(ctx, []model.RelayListener{reconfigured}, []model.WireGuardProfile{wireGuardProfile}, []model.EgressProfile{nextEgressProfile})
	if err == nil || !strings.Contains(err.Error(), listenErr.Error()) {
		t.Fatalf("expected replacement listen error, got %v", err)
	}
	if strings.Contains(err.Error(), "restore failed") {
		t.Fatalf("restore failed because egress rollback happened too late: %v", err)
	}
	if !restoreObserved {
		t.Fatal("expected previous relay server to be restored")
	}
	if candidateEgressRuntime == nil {
		t.Fatal("expected replacement egress wireguard runtime to be prepared")
	}
	if !candidateEgressRuntime.closed {
		t.Fatal("expected replacement egress wireguard runtime to be rolled back")
	}
	waitForPortState(t, listenPort, true)
}

func startAppObservingSOCKS5UDPProxy(t *testing.T) (string, <-chan proxyproto.SOCKS5UDPPacket) {
	t.Helper()

	tcpLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen tcp proxy: %v", err)
	}
	udpLn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		_ = tcpLn.Close()
		t.Fatalf("listen udp proxy: %v", err)
	}

	packetCh := make(chan proxyproto.SOCKS5UDPPacket, 1)
	done := make(chan struct{})

	go func() {
		defer close(done)
		defer close(packetCh)

		client, err := tcpLn.Accept()
		if err != nil {
			return
		}
		defer client.Close()
		if err := client.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
			t.Errorf("set tcp deadline: %v", err)
			return
		}
		req, err := proxyproto.ReadClientRequest(context.Background(), client, proxyproto.EntryAuth{})
		if err != nil {
			t.Errorf("ReadClientRequest() error = %v", err)
			return
		}
		if req.Protocol != "socks5-udp" {
			t.Errorf("req.Protocol = %q, want socks5-udp", req.Protocol)
			return
		}
		if err := proxyproto.WriteClientRequestSuccessWithBind(client, req, udpLn.LocalAddr()); err != nil {
			t.Errorf("WriteClientRequestSuccessWithBind() error = %v", err)
			return
		}

		buf := make([]byte, 64*1024)
		n, _, err := udpLn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		packet, err := proxyproto.ParseSOCKS5UDPPacket(buf[:n])
		if err != nil {
			t.Errorf("ParseSOCKS5UDPPacket() error = %v", err)
			return
		}
		packetCh <- packet
	}()

	t.Cleanup(func() {
		_ = tcpLn.Close()
		_ = udpLn.Close()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for SOCKS5 UDP proxy helper")
		}
	})

	return tcpLn.Addr().String(), packetCh
}

func waitForAppSOCKS5UDPPacket(t *testing.T, packetCh <-chan proxyproto.SOCKS5UDPPacket) proxyproto.SOCKS5UDPPacket {
	t.Helper()

	select {
	case packet, ok := <-packetCh:
		if !ok {
			t.Fatal("SOCKS5 UDP packet channel closed without observation")
		}
		return packet
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for SOCKS5 UDP packet")
		return proxyproto.SOCKS5UDPPacket{}
	}
}
