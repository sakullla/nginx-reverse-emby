//go:build integration

package cloudflare

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestIntegrationDNSWireCodecCNAMESOANSTXTAndCompressionBounds(t *testing.T) {
	packet := buildDNSFixtureResponse(t, 0x1234, "_acme-challenge.example.com", []wireFixtureRecord{
		{recordType: TypeCNAME, name: "_acme-challenge.example.com", value: "delegate.example.net"},
		{recordType: TypeSOA, name: "example.net", value: "ns1.example.net", secondary: "hostmaster.example.net"},
		{recordType: TypeNS, name: "example.net", value: "ns1.example.net"},
		{recordType: TypeTXT, name: "delegate.example.net", value: "part-onepart-two", textParts: []string{"part-one", "part-two"}},
	})
	message, err := decodeDNSMessage(packet, 0x1234)
	if err != nil {
		t.Fatalf("decodeDNSMessage() error = %v", err)
	}
	if message.ID != 0x1234 || message.Truncated || message.RCode != 0 || len(message.Answers) != 4 {
		t.Fatalf("message = %#v", message)
	}
	if len(message.Questions) != 1 || message.Questions[0].Name != "_acme-challenge.example.com" || message.Questions[0].Type != TypeTXT {
		t.Fatalf("questions = %#v", message.Questions)
	}
	if got := message.Answers[0]; got.Type != TypeCNAME || got.Name != "_acme-challenge.example.com" || got.Value != "delegate.example.net" {
		t.Fatalf("CNAME = %#v", got)
	}
	soa := message.Answers[1]
	if soa.Type != TypeSOA || soa.SOA == nil || soa.SOA.MName != "ns1.example.net" || soa.SOA.RName != "hostmaster.example.net" || soa.SOA.Minimum != 300 {
		t.Fatalf("SOA = %#v", soa)
	}
	if got := message.Answers[2]; got.Type != TypeNS || got.Value != "ns1.example.net" {
		t.Fatalf("NS = %#v", got)
	}
	if got := message.Answers[3]; got.Type != TypeTXT || got.Value != "part-onepart-two" || len(got.Text) != 2 {
		t.Fatalf("TXT = %#v", got)
	}

	if _, _, err := decodeDNSName([]byte{0xc0, 0x00}, 0); err == nil {
		t.Fatal("decodeDNSName(pointer loop) error = nil")
	}
	if _, _, err := decodeDNSName([]byte{0xc0, 0x02, 0x00}, 0); err == nil {
		t.Fatal("decodeDNSName(forward pointer) error = nil")
	}
	if _, _, err := decodeDNSName([]byte{0xc0}, 0); err == nil {
		t.Fatal("decodeDNSName(truncated pointer) error = nil")
	}
	if _, err := decodeDNSMessage(packet[:len(packet)-1], 0x1234); err == nil {
		t.Fatal("decodeDNSMessage(truncated RDATA) error = nil")
	}
	if _, err := decodeDNSMessage(packet, 0xbeef); err == nil {
		t.Fatal("decodeDNSMessage(unexpected ID) error = nil")
	}
	if _, err := encodeDNSQuery(1, "bad/name.example", TypeTXT); err == nil {
		t.Fatal("encodeDNSQuery(invalid name) error = nil")
	}
	if _, err := NewWireResolver(WireResolverConfig{RecursiveServers: []string{"127.0.0.1:not-a-port"}}); err == nil {
		t.Fatal("NewWireResolver(invalid port) error = nil")
	}
}

func TestIntegrationDNSWireUsesSystemResolversBeforeFallback(t *testing.T) {
	path := filepath.Join(t.TempDir(), "resolv.conf")
	data := []byte("# generated\nnameserver 192.0.2.53\nnameserver 2001:db8::53 # local\nsearch example.test\n")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("WriteFile(resolv.conf): %v", err)
	}
	servers := recursiveServersFromResolvConf(path, []string{"8.8.8.8:53"})
	if got := servers; len(got) != 2 || got[0] != "192.0.2.53:53" || got[1] != "[2001:db8::53]:53" {
		t.Fatalf("system recursive servers = %#v", got)
	}
	if got := recursiveServersFromResolvConf(filepath.Join(t.TempDir(), "missing"), []string{"8.8.8.8:53"}); len(got) != 1 || got[0] != "8.8.8.8:53" {
		t.Fatalf("fallback recursive servers = %#v", got)
	}
}

func TestIntegrationDNSWireRejectsMismatchedQuestion(t *testing.T) {
	udpConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("listen UDP: %v", err)
	}
	defer udpConn.Close()
	responsePacket := buildDNSFixtureResponse(t, 9, "attacker.example", []wireFixtureRecord{{recordType: TypeTXT, name: "attacker.example", value: "forged"}})
	served := make(chan error, 1)
	go func() {
		buffer := make([]byte, 512)
		_, remote, readErr := udpConn.ReadFromUDP(buffer)
		if readErr != nil {
			served <- readErr
			return
		}
		_, writeErr := udpConn.WriteToUDP(responsePacket, remote)
		served <- writeErr
	}()
	resolver, err := NewWireResolver(WireResolverConfig{
		RecursiveServers: []string{udpConn.LocalAddr().String()},
		QueryTimeout:     time.Second,
		NextID:           func() uint16 { return 9 },
	})
	if err != nil {
		t.Fatalf("NewWireResolver() error = %v", err)
	}
	if _, err := resolver.Query(context.Background(), udpConn.LocalAddr().String(), "example.com", TypeTXT); err == nil {
		t.Fatal("Query(mismatched question) error = nil")
	}
	if err := <-served; err != nil {
		t.Fatalf("DNS fixture: %v", err)
	}
}

func TestIntegrationDNSWireUDPTruncationFallsBackToTCP(t *testing.T) {
	var tcpListener net.Listener
	var udpConn *net.UDPConn
	var err error
	for attempt := 0; attempt < 64; attempt++ {
		if attempt%2 == 0 {
			tcpListener, err = net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				continue
			}
			udpAddress := &net.UDPAddr{
				IP:   net.ParseIP("127.0.0.1"),
				Port: tcpListener.Addr().(*net.TCPAddr).Port,
			}
			udpConn, err = net.ListenUDP("udp", udpAddress)
			if err == nil {
				break
			}
			_ = tcpListener.Close()
			tcpListener = nil
			continue
		}

		udpConn, err = net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
		if err != nil {
			continue
		}
		tcpListener, err = net.Listen("tcp", udpConn.LocalAddr().String())
		if err == nil {
			break
		}
		_ = udpConn.Close()
		udpConn = nil
	}
	if tcpListener == nil || udpConn == nil {
		t.Fatalf("listen on one TCP/UDP fixture port: %v", err)
	}
	defer tcpListener.Close()
	defer udpConn.Close()

	udpSeen := make(chan error, 1)
	tcpSeen := make(chan error, 1)
	tcpResponse := buildDNSFixtureResponse(t, 0x4321, "example.com", []wireFixtureRecord{{recordType: TypeTXT, name: "example.com", value: "propagated"}})
	go func() {
		buffer := make([]byte, 4096)
		count, remote, readErr := udpConn.ReadFromUDP(buffer)
		if readErr != nil {
			udpSeen <- readErr
			return
		}
		if count < 2 {
			udpSeen <- errors.New("short UDP query")
			return
		}
		response := make([]byte, 12)
		copy(response[:2], buffer[:2])
		binary.BigEndian.PutUint16(response[2:4], 0x8380)
		binary.BigEndian.PutUint16(response[4:6], 1)
		binary.BigEndian.PutUint16(response[6:8], 1)
		response = append(response, buffer[12:count]...)
		response = append(response, 0xc0, 0x0c)
		response = binary.BigEndian.AppendUint16(response, uint16(TypeTXT))
		response = binary.BigEndian.AppendUint16(response, dnsClassIN)
		response = binary.BigEndian.AppendUint32(response, 120)
		response = binary.BigEndian.AppendUint16(response, 10)
		response = append(response, 3, 'c')
		_, writeErr := udpConn.WriteToUDP(response, remote)
		udpSeen <- writeErr
	}()
	go func() {
		connection, acceptErr := tcpListener.Accept()
		if acceptErr != nil {
			tcpSeen <- acceptErr
			return
		}
		defer connection.Close()
		var length [2]byte
		if _, readErr := io.ReadFull(connection, length[:]); readErr != nil {
			tcpSeen <- readErr
			return
		}
		query := make([]byte, int(binary.BigEndian.Uint16(length[:])))
		if _, readErr := io.ReadFull(connection, query); readErr != nil {
			tcpSeen <- readErr
			return
		}
		if len(query) < 2 {
			tcpSeen <- errors.New("short TCP query")
			return
		}
		response := append([]byte(nil), tcpResponse...)
		binary.BigEndian.PutUint16(response[:2], binary.BigEndian.Uint16(query[:2]))
		framed := make([]byte, 2+len(response))
		binary.BigEndian.PutUint16(framed[:2], uint16(len(response)))
		copy(framed[2:], response)
		_, writeErr := connection.Write(framed)
		tcpSeen <- writeErr
	}()

	resolver, err := NewWireResolver(WireResolverConfig{
		RecursiveServers: []string{tcpListener.Addr().String()},
		QueryTimeout:     time.Second,
		NextID:           func() uint16 { return 0x4321 },
	})
	if err != nil {
		t.Fatalf("NewWireResolver() error = %v", err)
	}
	message, err := resolver.Query(context.Background(), tcpListener.Addr().String(), "example.com", TypeTXT)
	if err != nil {
		t.Fatalf("Query() error = %v", err)
	}
	if len(message.Answers) != 1 || message.Answers[0].Type != TypeTXT || message.Answers[0].Value != "propagated" {
		t.Fatalf("Query() message = %#v", message)
	}
	if err := <-udpSeen; err != nil {
		t.Fatalf("UDP fixture: %v", err)
	}
	if err := <-tcpSeen; err != nil {
		t.Fatalf("TCP fixture: %v", err)
	}
}

func TestIntegrationDNSWireCancellationIsBounded(t *testing.T) {
	udpConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("listen UDP: %v", err)
	}
	defer udpConn.Close()
	resolver, err := NewWireResolver(WireResolverConfig{
		RecursiveServers: []string{udpConn.LocalAddr().String()},
		QueryTimeout:     5 * time.Second,
		NextID:           func() uint16 { return 7 },
	})
	if err != nil {
		t.Fatalf("NewWireResolver() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, queryErr := resolver.Query(ctx, udpConn.LocalAddr().String(), "example.com", TypeTXT)
		done <- queryErr
	}()
	buffer := make([]byte, 512)
	if err := udpConn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("SetReadDeadline(): %v", err)
	}
	if _, _, err := udpConn.ReadFromUDP(buffer); err != nil {
		t.Fatalf("read query: %v", err)
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Query() error = nil")
		}
	case <-time.After(time.Second):
		t.Fatal("Query() did not stop after cancellation")
	}
}

type wireFixtureRecord struct {
	recordType RRType
	name       string
	value      string
	secondary  string
	textParts  []string
}

func buildDNSFixtureResponse(t *testing.T, id uint16, questionName string, records []wireFixtureRecord) []byte {
	t.Helper()
	question, err := encodeDNSName(questionName)
	if err != nil {
		t.Fatalf("encode question: %v", err)
	}
	packet := make([]byte, 12)
	binary.BigEndian.PutUint16(packet[0:2], id)
	binary.BigEndian.PutUint16(packet[2:4], 0x8180)
	binary.BigEndian.PutUint16(packet[4:6], 1)
	binary.BigEndian.PutUint16(packet[6:8], uint16(len(records)))
	packet = append(packet, question...)
	packet = binary.BigEndian.AppendUint16(packet, uint16(TypeTXT))
	packet = binary.BigEndian.AppendUint16(packet, 1)
	for _, record := range records {
		if record.name == questionName {
			packet = append(packet, 0xc0, 0x0c)
		} else {
			name, nameErr := encodeDNSName(record.name)
			if nameErr != nil {
				t.Fatalf("encode record name: %v", nameErr)
			}
			packet = append(packet, name...)
		}
		packet = binary.BigEndian.AppendUint16(packet, uint16(record.recordType))
		packet = binary.BigEndian.AppendUint16(packet, 1)
		packet = binary.BigEndian.AppendUint32(packet, 120)
		var data []byte
		switch record.recordType {
		case TypeCNAME, TypeNS:
			data, err = encodeDNSName(record.value)
		case TypeSOA:
			data, err = encodeDNSName(record.value)
			if err == nil {
				var rname []byte
				rname, err = encodeDNSName(record.secondary)
				data = append(data, rname...)
				for _, value := range []uint32{1, 60, 60, 600, 300} {
					data = binary.BigEndian.AppendUint32(data, value)
				}
			}
		case TypeTXT:
			parts := record.textParts
			if len(parts) == 0 {
				parts = []string{record.value}
			}
			for _, part := range parts {
				if len(part) > 255 {
					t.Fatalf("TXT fixture part too large")
				}
				data = append(data, byte(len(part)))
				data = append(data, part...)
			}
		default:
			t.Fatalf("unsupported fixture type %d", record.recordType)
		}
		if err != nil {
			t.Fatalf("encode RDATA: %v", err)
		}
		packet = binary.BigEndian.AppendUint16(packet, uint16(len(data)))
		packet = append(packet, data...)
	}
	return packet
}
