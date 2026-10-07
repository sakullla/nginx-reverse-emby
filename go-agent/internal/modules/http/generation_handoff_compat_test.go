//go:build integration && !windows

package http

import (
	"fmt"
	"net"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/hotrestart"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/ingress"
)

func TestIntegrationHTTPInheritedListenerCompatibility(t *testing.T) {
	for _, legacy := range []bool{true, false} {
		for _, scheme := range []string{"http", "https"} {
			for _, http3 := range []bool{false, true} {
				if http3 && scheme == "http" {
					continue
				}
				t.Run(fmt.Sprintf("legacy=%t/%s/http3=%t", legacy, scheme, http3), func(t *testing.T) {
					currentScheme := scheme
					listener, err := net.Listen("tcp", "127.0.0.1:0")
					if err != nil {
						t.Fatal(err)
					}
					defer listener.Close()
					address := listener.Addr().String()
					_, port, _ := net.SplitHostPort(address)
					id := "http:" + address
					if legacy {
						id = "http:" + scheme + ":" + port
					}
					streams, err := hotrestart.ExportStreamListeners(map[string]net.Listener{id: listener})
					if err != nil {
						t.Fatal(err)
					}
					defer streams.Close()
					var packets *hotrestart.PacketBundle
					if http3 {
						conn, err := net.ListenPacket("udp", address)
						if err != nil {
							t.Fatal(err)
						}
						defer conn.Close()
						packets, err = hotrestart.ExportPacketConns(map[string]net.PacketConn{id: conn})
						if err != nil {
							t.Fatal(err)
						}
						defer packets.Close()
					}
					// Import and re-export twice. Keeping the inherited wire identity
					// is necessary for packet forwarding and the next hot upgrade.
					for hop := 0; hop < 2; hop++ {
						manager := newHTTPIngressManager()
						defer manager.close()
						manager.processStreams = ingress.NewProcessStreamRegistry()
						set, err := manager.processStreams.Import(streams.Descriptors, streams.Files)
						if err != nil {
							t.Fatal(err)
						}
						defer set.Close()
						defer manager.processStreams.Close()
						if http3 {
							manager.processPackets = ingress.NewProcessPacketRegistry()
							set, err := manager.processPackets.Import(packets.Descriptors, packets.Files)
							if err != nil {
								t.Fatal(err)
							}
							defer set.Close()
							defer manager.processPackets.Close()
						}
						lease, err := manager.acquire(t.Context(), fmt.Sprintf("hop-%d", hop), runtimeListenerSpec{
							address: address, bindingKey: currentScheme + ":" + port, scheme: currentScheme,
						}, Providers{}, http3)
						if err != nil {
							t.Fatalf("inherit %s at hop %d: %v", id, hop, err)
						}
						defer lease.release()
						if err := manager.processStreams.ValidateImported(); err != nil {
							t.Fatal(err)
						}
						streams, err = manager.processStreams.Export()
						if err != nil {
							t.Fatal(err)
						}
						defer streams.Close()
						if len(streams.Descriptors) != 1 || streams.Descriptors[0].ID != id {
							t.Fatalf("stream handoff identity changed: %+v", streams.Descriptors)
						}
						if http3 {
							if err := manager.processPackets.ValidateImported(); err != nil {
								t.Fatal(err)
							}
							packets, err = manager.processPackets.Export()
							if err != nil {
								t.Fatal(err)
							}
							defer packets.Close()
							if len(packets.Descriptors) != 1 || packets.Descriptors[0].ID != id {
								t.Fatalf("packet handoff identity changed: %+v", packets.Descriptors)
							}
						} else if currentScheme == "http" {
							currentScheme = "https"
						} else {
							currentScheme = "http"
						}
					}
				})
			}
		}
	}
}
