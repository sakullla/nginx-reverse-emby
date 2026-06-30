package wgnetstack

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net/netip"
	"reflect"
	"sync"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
	"gvisor.dev/gvisor/pkg/buffer"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/adapters/gonet"
	"gvisor.dev/gvisor/pkg/tcpip/link/channel"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
	"gvisor.dev/gvisor/pkg/tcpip/transport/tcp"
)

func TestNetTunBatchSizeUsesConfiguredBatchSize(t *testing.T) {
	tun := &netTun{}
	if got := tun.BatchSize(); got != netTunBatchSize {
		t.Fatalf("BatchSize() = %d, want %d", got, netTunBatchSize)
	}
}

func TestNetTunBatchSizeUsesConservativeWireGuardReadBatch(t *testing.T) {
	tun := &netTun{}
	if got, want := tun.BatchSize(), 32; got != want {
		t.Fatalf("BatchSize() = %d, want %d", got, want)
	}
}

func TestNetTunBatchSizeStaysWithinWireGuardBindLimit(t *testing.T) {
	tun := &netTun{}
	if got, wantMax := tun.BatchSize(), 128; got > wantMax {
		t.Fatalf("BatchSize() = %d, want <= %d", got, wantMax)
	}
}

func TestNetTunChannelQueueSizeIsBounded(t *testing.T) {
	if got, wantMax := netTunChannelQueueSize, 256; got > wantMax {
		t.Fatalf("netTunChannelQueueSize = %d, want <= %d", got, wantMax)
	}
}

func TestNetTunOutboundQueueAllowsMobileTrafficBursts(t *testing.T) {
	dev, _, _, err := CreateNetTUN([]netip.Addr{netip.MustParseAddr("10.99.0.1")}, nil, 1280)
	if err != nil {
		t.Fatalf("CreateNetTUN() error = %v", err)
	}
	defer dev.Close()

	tun, ok := dev.(*netTun)
	if !ok {
		t.Fatalf("CreateNetTUN() device type = %T, want *netTun", dev)
	}
	if got, wantMin := cap(tun.incomingPacket), 256; got < wantMin {
		t.Fatalf("incomingPacket capacity = %d, want >= %d", got, wantMin)
	}
}

func TestCreateNetTUNRejectsInvalidMTU(t *testing.T) {
	if _, _, _, err := CreateNetTUN(nil, nil, -1); err == nil {
		t.Fatal("expected negative MTU to be rejected")
	}
}

func TestConfigureTCPBuffersRaisesNetstackWindowDefaults(t *testing.T) {
	s := stack.New(stack.Options{
		NetworkProtocols:   []stack.NetworkProtocolFactory{ipv4.NewProtocol},
		TransportProtocols: []stack.TransportProtocolFactory{tcp.NewProtocol},
	})

	if err := configureTCPBuffers(s); err != nil {
		t.Fatalf("configureTCPBuffers() error = %v", err)
	}

	var recv tcpip.TCPReceiveBufferSizeRangeOption
	if err := s.TransportProtocolOption(tcp.ProtocolNumber, &recv); err != nil {
		t.Fatalf("TransportProtocolOption(recv) error = %v", err)
	}
	if recv.Default < netTunTCPDefaultBufferSize || recv.Max < netTunTCPMaxBufferSize {
		t.Fatalf("receive buffer range = %+v, want default >= %d max >= %d", recv, netTunTCPDefaultBufferSize, netTunTCPMaxBufferSize)
	}

	var send tcpip.TCPSendBufferSizeRangeOption
	if err := s.TransportProtocolOption(tcp.ProtocolNumber, &send); err != nil {
		t.Fatalf("TransportProtocolOption(send) error = %v", err)
	}
	if send.Default < netTunTCPDefaultBufferSize || send.Max < netTunTCPMaxBufferSize {
		t.Fatalf("send buffer range = %+v, want default >= %d max >= %d", send, netTunTCPDefaultBufferSize, netTunTCPMaxBufferSize)
	}
}

func TestConfigureTCPBuffersUsesModerateDefaultWindow(t *testing.T) {
	s := stack.New(stack.Options{
		NetworkProtocols:   []stack.NetworkProtocolFactory{ipv4.NewProtocol},
		TransportProtocols: []stack.TransportProtocolFactory{tcp.NewProtocol},
	})

	if err := configureTCPBuffers(s); err != nil {
		t.Fatalf("configureTCPBuffers() error = %v", err)
	}

	var recv tcpip.TCPReceiveBufferSizeRangeOption
	if err := s.TransportProtocolOption(tcp.ProtocolNumber, &recv); err != nil {
		t.Fatalf("TransportProtocolOption(recv) error = %v", err)
	}
	if recv.Default != 2<<20 {
		t.Fatalf("receive buffer default = %d, want %d", recv.Default, 2<<20)
	}

	var send tcpip.TCPSendBufferSizeRangeOption
	if err := s.TransportProtocolOption(tcp.ProtocolNumber, &send); err != nil {
		t.Fatalf("TransportProtocolOption(send) error = %v", err)
	}
	if send.Default != 2<<20 {
		t.Fatalf("send buffer default = %d, want %d", send.Default, 2<<20)
	}
}

func TestConfigureTCPBuffersBoundsNetstackWindowMax(t *testing.T) {
	s := stack.New(stack.Options{
		NetworkProtocols:   []stack.NetworkProtocolFactory{ipv4.NewProtocol},
		TransportProtocols: []stack.TransportProtocolFactory{tcp.NewProtocol},
	})

	if err := configureTCPBuffers(s); err != nil {
		t.Fatalf("configureTCPBuffers() error = %v", err)
	}

	var recv tcpip.TCPReceiveBufferSizeRangeOption
	if err := s.TransportProtocolOption(tcp.ProtocolNumber, &recv); err != nil {
		t.Fatalf("TransportProtocolOption(recv) error = %v", err)
	}
	if recv.Max > 4<<20 {
		t.Fatalf("receive buffer max = %d, want <= %d", recv.Max, 4<<20)
	}

	var send tcpip.TCPSendBufferSizeRangeOption
	if err := s.TransportProtocolOption(tcp.ProtocolNumber, &send); err != nil {
		t.Fatalf("TransportProtocolOption(send) error = %v", err)
	}
	if send.Max > 4<<20 {
		t.Fatalf("send buffer max = %d, want <= %d", send.Max, 4<<20)
	}
}

func TestNetTunDNSCacheReturnsStoredHostBeforeExpiry(t *testing.T) {
	tun := &netTun{}
	tun.storeDNSCache("Example.COM", []string{"203.0.113.10"}, time.Minute)

	got, ok := tun.lookupDNSCache("example.com")

	if !ok {
		t.Fatal("lookupDNSCache() ok = false, want true")
	}
	if !reflect.DeepEqual(got, []string{"203.0.113.10"}) {
		t.Fatalf("lookupDNSCache() = %#v", got)
	}
	got[0] = "198.51.100.99"
	again, ok := tun.lookupDNSCache("example.com")
	if !ok || !reflect.DeepEqual(again, []string{"203.0.113.10"}) {
		t.Fatalf("cached addrs were not isolated from caller mutation: %#v ok=%t", again, ok)
	}
}

func TestNetTunDNSCacheSkipsExpiredHost(t *testing.T) {
	tun := &netTun{}
	tun.storeDNSCache("example.com", []string{"203.0.113.10"}, -time.Second)

	if got, ok := tun.lookupDNSCache("example.com"); ok {
		t.Fatalf("lookupDNSCache() = %#v, true; want expired miss", got)
	}
}

func TestNetTunDNSCachePrunesExpiredHostsOnInsert(t *testing.T) {
	tun := &netTun{
		dnsCache: map[string]dnsCacheEntry{
			"expired.example.com": {
				addrs:  []string{"203.0.113.10"},
				expiry: time.Now().Add(-time.Second),
			},
			"fresh.example.com": {
				addrs:  []string{"203.0.113.11"},
				expiry: time.Now().Add(time.Minute),
			},
		},
	}

	tun.storeDNSCache("new.example.com", []string{"203.0.113.12"}, time.Minute)

	if _, ok := tun.dnsCache["expired.example.com"]; ok {
		t.Fatal("expired DNS cache entry was retained after insert")
	}
	if _, ok := tun.dnsCache["fresh.example.com"]; !ok {
		t.Fatal("fresh DNS cache entry was pruned")
	}
	if _, ok := tun.dnsCache["new.example.com"]; !ok {
		t.Fatal("new DNS cache entry was not stored")
	}
}

// TestNetTunDNSCacheEnforcesMaxEntries verifies the cache is bounded at
// netTunDNSCacheMaxEntries: inserting a brand-new host beyond capacity evicts
// the earliest-expiring entry, overwriting an existing host never grows or
// evicts, and continued inserts never exceed the cap (R3).
func TestNetTunDNSCacheEnforcesMaxEntries(t *testing.T) {
	tun := &netTun{}
	now := time.Now()

	// Pre-populate to exactly the capacity with deterministic expiries. One
	// entry (the victim) expires earliest so it must be evicted first.
	tun.dnsCache = make(map[string]dnsCacheEntry, netTunDNSCacheMaxEntries)
	for i := 0; i < netTunDNSCacheMaxEntries-1; i++ {
		tun.dnsCache[fmt.Sprintf("host-%04d.example.com", i)] = dnsCacheEntry{
			addrs:  []string{"203.0.113.1"},
			expiry: now.Add(5 * time.Minute),
		}
	}
	const victim = "victim.example.com"
	tun.dnsCache[victim] = dnsCacheEntry{
		addrs:  []string{"203.0.113.2"},
		expiry: now.Add(time.Minute), // earliest expiry
	}
	if got := len(tun.dnsCache); got != netTunDNSCacheMaxEntries {
		t.Fatalf("precondition: cache size = %d, want %d", got, netTunDNSCacheMaxEntries)
	}

	// Inserting a brand-new host beyond capacity must cap the cache and evict
	// the earliest-expiring entry rather than growing unbounded.
	tun.storeDNSCache("overflow.example.com", []string{"203.0.113.9"}, 5*time.Minute)

	if got := len(tun.dnsCache); got != netTunDNSCacheMaxEntries {
		t.Fatalf("cache size after overflow insert = %d, want capped at %d", got, netTunDNSCacheMaxEntries)
	}
	if _, ok := tun.dnsCache[victim]; ok {
		t.Fatalf("expected earliest-expiry entry %q to be evicted", victim)
	}
	if _, ok := tun.dnsCache["overflow.example.com"]; !ok {
		t.Fatal("expected new overflow entry to be present")
	}

	// Overwriting an existing host must not grow the cache or evict anything.
	tun.storeDNSCache("host-0000.example.com", []string{"203.0.113.50"}, 5*time.Minute)
	if got := len(tun.dnsCache); got != netTunDNSCacheMaxEntries {
		t.Fatalf("cache size after overwrite = %d, want %d", got, netTunDNSCacheMaxEntries)
	}
	if got := tun.dnsCache["host-0000.example.com"].addrs[0]; got != "203.0.113.50" {
		t.Fatalf("overwrite did not update addrs, got %q", got)
	}

	// Continued inserts must stay bounded: the cache never exceeds the cap.
	for i := 0; i < 64; i++ {
		tun.storeDNSCache(fmt.Sprintf("extra-%04d.example.com", i), []string{"203.0.113.99"}, 5*time.Minute)
	}
	if got := len(tun.dnsCache); got != netTunDNSCacheMaxEntries {
		t.Fatalf("cache size after extra inserts = %d, want capped at %d", got, netTunDNSCacheMaxEntries)
	}
}

// TestNetTunDNSCacheConcurrentStoresStayBounded exercises the cache under
// concurrent inserts from many goroutines: the size must never exceed the cap
// and no entry may be left with empty addrs (no torn / conflicting writes).
func TestNetTunDNSCacheConcurrentStoresStayBounded(t *testing.T) {
	tun := &netTun{}

	const goroutines = 16
	const insertsPerGoroutine = 200
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func(g int) {
			defer wg.Done()
			for i := 0; i < insertsPerGoroutine; i++ {
				host := fmt.Sprintf("g%d-h%d.example.com", g, i)
				tun.storeDNSCache(host, []string{"203.0.113.10"}, 5*time.Minute)
				if _, ok := tun.lookupDNSCache(host); !ok {
					// A concurrent evictor may have removed this host; that is
					// acceptable as long as the cache stays consistent.
					continue
				}
			}
		}(g)
	}
	wg.Wait()

	tun.dnsCacheMu.Lock()
	defer tun.dnsCacheMu.Unlock()
	if got := len(tun.dnsCache); got > netTunDNSCacheMaxEntries {
		t.Fatalf("concurrent stores grew cache to %d, exceeding cap %d", got, netTunDNSCacheMaxEntries)
	}
	for host, entry := range tun.dnsCache {
		if len(entry.addrs) == 0 {
			t.Fatalf("cache entry %q has empty addrs (torn write)", host)
		}
		if entry.expiry.IsZero() {
			t.Fatalf("cache entry %q has zero expiry", host)
		}
	}
}

func TestNetExchangeFallsBackToTCPWhenUDPQueryTimesOut(t *testing.T) {
	_, runtimeNet, _, err := CreateNetTUN(
		[]netip.Addr{netip.MustParseAddr("10.99.0.1")},
		[]netip.Addr{netip.MustParseAddr("10.99.0.1")},
		1420,
	)
	if err != nil {
		t.Fatalf("CreateNetTUN() error = %v", err)
	}
	tnet := runtimeNet.(*Net)

	udpConn, err := tnet.ListenUDPAddrPort(netip.MustParseAddrPort("10.99.0.1:53"))
	if err != nil {
		t.Fatalf("ListenUDPAddrPort() error = %v", err)
	}
	defer udpConn.Close()
	go func() {
		buf := make([]byte, 512)
		_, _, _ = udpConn.ReadFrom(buf)
	}()

	tcpListener, err := tnet.ListenTCPAddrPort(netip.MustParseAddrPort("10.99.0.1:53"))
	if err != nil {
		t.Fatalf("ListenTCPAddrPort() error = %v", err)
	}
	defer tcpListener.Close()
	servedTCP := make(chan struct{}, 1)
	go serveSingleDNSOverTCP(t, tcpListener, servedTCP)

	name := dnsmessage.MustNewName("example.com.")
	parser, header, err := tnet.exchange(context.Background(), netip.MustParseAddr("10.99.0.1"), dnsmessage.Question{
		Name:  name,
		Type:  dnsmessage.TypeA,
		Class: dnsmessage.ClassINET,
	}, 25*time.Millisecond)
	if err != nil {
		t.Fatalf("exchange() error = %v", err)
	}
	if !header.Response || header.Truncated {
		t.Fatalf("exchange() header = %+v, want non-truncated response", header)
	}
	answerHeader, err := parser.AnswerHeader()
	if err != nil {
		t.Fatalf("AnswerHeader() error = %v", err)
	}
	if answerHeader.Type != dnsmessage.TypeA {
		t.Fatalf("answer type = %v, want A", answerHeader.Type)
	}
	answer, err := parser.AResource()
	if err != nil {
		t.Fatalf("AResource() error = %v", err)
	}
	if got, want := answer.A, [4]byte{203, 0, 113, 55}; got != want {
		t.Fatalf("AResource() = %v, want %v", got, want)
	}
	select {
	case <-servedTCP:
	case <-time.After(time.Second):
		t.Fatal("DNS TCP server was not used after UDP timeout")
	}
}

func serveSingleDNSOverTCP(t *testing.T, listener *gonet.TCPListener, served chan<- struct{}) {
	t.Helper()

	conn, err := listener.Accept()
	if err != nil {
		return
	}
	defer conn.Close()
	defer func() { served <- struct{}{} }()

	var lengthBuf [2]byte
	if _, err := io.ReadFull(conn, lengthBuf[:]); err != nil {
		t.Errorf("read DNS TCP length error = %v", err)
		return
	}
	req := make([]byte, binary.BigEndian.Uint16(lengthBuf[:]))
	if _, err := io.ReadFull(conn, req); err != nil {
		t.Errorf("read DNS TCP request error = %v", err)
		return
	}
	resp, err := dnsTCPAResponse(req, [4]byte{203, 0, 113, 55})
	if err != nil {
		t.Errorf("build DNS TCP response error = %v", err)
		return
	}
	binary.BigEndian.PutUint16(lengthBuf[:], uint16(len(resp)))
	if _, err := conn.Write(append(lengthBuf[:], resp...)); err != nil {
		t.Errorf("write DNS TCP response error = %v", err)
	}
}

func dnsTCPAResponse(req []byte, ip [4]byte) ([]byte, error) {
	var parser dnsmessage.Parser
	header, err := parser.Start(req)
	if err != nil {
		return nil, err
	}
	question, err := parser.Question()
	if err != nil {
		return nil, err
	}
	builder := dnsmessage.NewBuilder(nil, dnsmessage.Header{
		ID:                 header.ID,
		Response:           true,
		RecursionAvailable: true,
	})
	if err := builder.StartQuestions(); err != nil {
		return nil, err
	}
	if err := builder.Question(question); err != nil {
		return nil, err
	}
	if err := builder.StartAnswers(); err != nil {
		return nil, err
	}
	if err := builder.AResource(dnsmessage.ResourceHeader{
		Name:  question.Name,
		Type:  dnsmessage.TypeA,
		Class: dnsmessage.ClassINET,
		TTL:   60,
	}, dnsmessage.AResource{A: ip}); err != nil {
		return nil, err
	}
	return builder.Finish()
}

func TestNetTunReadDrainsQueuedPacketBatch(t *testing.T) {
	tun := &netTun{incomingPacket: make(chan *stack.PacketBuffer, netTunBatchSize)}
	for _, payload := range [][]byte{[]byte("one"), []byte("two"), []byte("three")} {
		tun.incomingPacket <- stack.NewPacketBuffer(stack.PacketBufferOptions{
			Payload: buffer.MakeWithData(payload),
		})
	}

	bufs := [][]byte{make([]byte, 16), make([]byte, 16), make([]byte, 16)}
	sizes := make([]int, len(bufs))
	n, err := tun.Read(bufs, sizes, 0)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if n != 3 {
		t.Fatalf("Read() packet count = %d, want 3", n)
	}
	for i, want := range []string{"one", "two", "three"} {
		if got := string(bufs[i][:sizes[i]]); got != want {
			t.Fatalf("packet %d = %q, want %q", i, got, want)
		}
	}
}

func TestNetTunWriteNotifyDrainsQueuedOutboundBatch(t *testing.T) {
	tun := &netTun{
		ep:             channel.New(netTunChannelQueueSize, 1280, ""),
		incomingPacket: make(chan *stack.PacketBuffer, netTunBatchSize),
		localAddresses: map[netip.Addr]struct{}{},
	}

	var packets stack.PacketBufferList
	for _, payload := range [][]byte{[]byte("one"), []byte("two"), []byte("three")} {
		packets.PushBack(stack.NewPacketBuffer(stack.PacketBufferOptions{
			Payload: buffer.MakeWithData(payload),
		}))
	}
	defer packets.DecRef()

	if written, tcpipErr := tun.ep.WritePackets(packets); tcpipErr != nil || written != 3 {
		t.Fatalf("WritePackets() = %d, %v; want 3, nil", written, tcpipErr)
	}

	tun.WriteNotify()

	bufs := [][]byte{make([]byte, 16), make([]byte, 16), make([]byte, 16)}
	sizes := make([]int, len(bufs))
	n, err := tun.Read(bufs, sizes, 0)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if n != 3 {
		t.Fatalf("Read() packet count = %d, want 3", n)
	}
	for i, want := range []string{"one", "two", "three"} {
		if got := string(bufs[i][:sizes[i]]); got != want {
			t.Fatalf("packet %d = %q, want %q", i, got, want)
		}
	}
}

func TestNetTunWriteNotifyDoesNotBlockWhenOutboundQueueIsFull(t *testing.T) {
	tun := &netTun{
		ep:             channel.New(netTunChannelQueueSize, 1280, ""),
		incomingPacket: make(chan *stack.PacketBuffer, 1),
		localAddresses: map[netip.Addr]struct{}{},
	}
	tun.incomingPacket <- stack.NewPacketBuffer(stack.PacketBufferOptions{
		Payload: buffer.MakeWithData([]byte("queued")),
	})

	var packets stack.PacketBufferList
	packets.PushBack(stack.NewPacketBuffer(stack.PacketBufferOptions{
		Payload: buffer.MakeWithData([]byte("overflow")),
	}))
	defer packets.DecRef()

	if written, tcpipErr := tun.ep.WritePackets(packets); tcpipErr != nil || written != 1 {
		t.Fatalf("WritePackets() = %d, %v; want 1, nil", written, tcpipErr)
	}

	done := make(chan struct{})
	go func() {
		tun.WriteNotify()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("WriteNotify blocked after outbound packet queue filled")
	}
}

func BenchmarkNetTunWriteNotifyRead1400B(b *testing.B) {
	payload := make([]byte, 1400)
	payload[0] = 0x45
	tun := &netTun{
		ep:             channel.New(netTunChannelQueueSize, 1500, ""),
		incomingPacket: make(chan *stack.PacketBuffer, netTunBatchSize),
		localAddresses: map[netip.Addr]struct{}{},
	}
	bufs := [][]byte{make([]byte, 1600)}
	sizes := make([]int, len(bufs))

	b.ReportAllocs()
	b.SetBytes(int64(len(payload)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var packets stack.PacketBufferList
		packets.PushBack(stack.NewPacketBuffer(stack.PacketBufferOptions{
			Payload: buffer.MakeWithData(payload),
		}))
		if written, tcpipErr := tun.ep.WritePackets(packets); tcpipErr != nil || written != 1 {
			b.Fatalf("WritePackets() = %d, %v; want 1, nil", written, tcpipErr)
		}
		packets.DecRef()

		tun.WriteNotify()
		n, err := tun.Read(bufs, sizes, 0)
		if err != nil {
			b.Fatalf("Read() error = %v", err)
		}
		if n != 1 || sizes[0] != len(payload) {
			b.Fatalf("Read() = %d size %d, want 1 size %d", n, sizes[0], len(payload))
		}
	}
}
