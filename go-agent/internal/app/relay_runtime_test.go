package app

import (
	"context"
	"crypto/tls"
	"net"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/proxyproto"
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
