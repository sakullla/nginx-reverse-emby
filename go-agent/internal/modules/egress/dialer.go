package egress

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/proxyproto"
)

type Dialer struct {
	Resolver       ProfileResolver
	OverlayRuntime module.OverlayRuntime
}

type ProfileResolver interface {
	Resolve(id *int, network string) (model.EgressProfile, bool, error)
}

func (d Dialer) DialTCP(ctx context.Context, target string, id *int) (net.Conn, error) {
	profile, _, err := d.Resolver.Resolve(id, "tcp")
	if err != nil {
		return nil, err
	}

	switch strings.ToLower(strings.TrimSpace(profile.Type)) {
	case "direct":
		var dialer net.Dialer
		return dialer.DialContext(ctx, "tcp", target)
	case "socks", "http":
		return proxyproto.Dial(ctx, profile.ProxyURL, target)
	case "wireguard":
		runtime, err := d.overlayRuntime(profile.ID)
		if err != nil {
			return nil, err
		}
		return runtime.DialContext(ctx, "", profile.ID, "tcp", target)
	default:
		return nil, fmt.Errorf("unsupported egress profile type %q", profile.Type)
	}
}

func (d Dialer) DialUDP(ctx context.Context, target string, id *int) (proxyproto.UDPPacketConn, error) {
	profile, _, err := d.Resolver.Resolve(id, "udp")
	if err != nil {
		return nil, err
	}

	switch strings.ToLower(strings.TrimSpace(profile.Type)) {
	case "direct":
		var dialer net.Dialer
		conn, err := dialer.DialContext(ctx, "udp", target)
		if err != nil {
			return nil, err
		}
		return &netUDPPacketConn{conn: conn}, nil
	case "socks":
		return proxyproto.DialUDP(ctx, profile.ProxyURL)
	case "http":
		return nil, fmt.Errorf("UDP egress profile %d type http is unsupported", profile.ID)
	case "wireguard":
		runtime, err := d.overlayRuntime(profile.ID)
		if err != nil {
			return nil, err
		}
		conn, err := runtime.DialContext(ctx, "", profile.ID, "udp", target)
		if err != nil {
			return nil, err
		}
		return &netUDPPacketConn{conn: conn}, nil
	default:
		return nil, fmt.Errorf("unsupported egress profile type %q", profile.Type)
	}
}

func (d Dialer) overlayRuntime(profileID int) (module.OverlayRuntime, error) {
	if d.OverlayRuntime == nil {
		return nil, fmt.Errorf("wireguard runtime provider is required for egress profile %d", profileID)
	}
	return d.OverlayRuntime, nil
}

type netUDPPacketConn struct {
	conn    net.Conn
	readBuf []byte
}

func (c *netUDPPacketConn) Close() error {
	return c.conn.Close()
}

func (c *netUDPPacketConn) SetReadDeadline(t time.Time) error {
	return c.conn.SetReadDeadline(t)
}

func (c *netUDPPacketConn) SetWriteDeadline(t time.Time) error {
	return c.conn.SetWriteDeadline(t)
}

func (c *netUDPPacketConn) ReadPacket() (string, []byte, error) {
	if c.readBuf == nil {
		c.readBuf = make([]byte, 64*1024)
	}
	n, err := c.conn.Read(c.readBuf)
	if err != nil {
		return "", nil, err
	}
	return "", append([]byte(nil), c.readBuf[:n]...), nil
}

func (c *netUDPPacketConn) WritePacket(_ string, payload []byte) error {
	_, err := c.conn.Write(payload)
	return err
}
