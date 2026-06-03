package egress

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

func TestDialerTCPDirectUsesNetDialer(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen tcp: %v", err)
	}
	defer ln.Close()

	accepted := make(chan struct{}, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		accepted <- struct{}{}
	}()

	conn, err := Dialer{Resolver: NewResolver(nil)}.DialTCP(context.Background(), ln.Addr().String(), nil)
	if err != nil {
		t.Fatalf("DialTCP() error = %v", err)
	}
	defer conn.Close()

	select {
	case <-accepted:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for direct TCP accept")
	}
}

func TestDialerUDPHTTPProfileReturnsUnsupportedError(t *testing.T) {
	profileID := 23
	dialer := Dialer{Resolver: NewResolver([]model.EgressProfile{{
		ID:       profileID,
		Type:     "http",
		ProxyURL: "http://127.0.0.1:8080",
		Enabled:  true,
	}})}

	_, err := dialer.DialUDP(context.Background(), "127.0.0.1:5353", &profileID)
	if err == nil || !strings.Contains(err.Error(), "UDP egress profile 23 type http is unsupported") {
		t.Fatalf("DialUDP() error = %v, want UDP/http unsupported profile error", err)
	}
}
