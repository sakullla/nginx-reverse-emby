package wireguard

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/ingress"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"golang.zx2c4.com/wireguard/conn"
)

type processWireGuardBind struct {
	mu sync.Mutex

	registry   *ingress.ProcessPacketRegistry
	identity   string
	addresses  []string
	classifier *processWireGuardClassifier
	sockets    []*processWireGuardSocket
	opened     bool
	closed     bool
}

type processWireGuardSocket struct {
	network  string
	broker   *ingress.PacketBroker
	endpoint *ingress.PacketEndpoint
}

type processWireGuardSocketSpec struct {
	network string
	host    string
}

func newProcessWireGuardBind(registry *ingress.ProcessPacketRegistry, identity string, addresses []string) conn.Bind {
	return &processWireGuardBind{
		registry:   registry,
		identity:   strings.TrimSpace(identity),
		addresses:  append([]string(nil), addresses...),
		classifier: newProcessWireGuardClassifier(),
	}
}

func (b *processWireGuardBind) Open(port uint16) ([]conn.ReceiveFunc, uint16, error) {
	if b == nil || b.registry == nil || b.identity == "" {
		return nil, 0, errors.New("wireguard process packet bind is not configured")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil, 0, net.ErrClosed
	}
	if b.opened {
		return nil, 0, conn.ErrBindAlreadyOpen
	}

	specs, err := processWireGuardSocketSpecs(b.addresses)
	if err != nil {
		return nil, 0, err
	}
	var selected uint16
	opened := make([]*processWireGuardSocket, 0, len(specs))
	receivers := make([]conn.ReceiveFunc, 0, len(specs))
	for _, spec := range specs {
		listenPort := port
		if selected != 0 {
			listenPort = selected
		}
		processID := fmt.Sprintf("wireguard:%s:%s:%s", b.identity, spec.network, spec.host)
		broker, openErr := b.registry.NewBroker(
			context.Background(),
			processID,
			"udp",
			func(ctx context.Context) (net.PacketConn, error) {
				packet, listenErr := hostListenConfig().ListenPacket(ctx, spec.network, net.JoinHostPort(spec.host, strconv.Itoa(int(listenPort))))
				if listenErr != nil {
					return nil, listenErr
				}
				if tuner, ok := packet.(model.UDPBufferTuner); ok {
					model.TuneUDPBuffers(tuner)
				}
				return packet, nil
			},
			b.classifier,
		)
		if openErr != nil {
			closeProcessWireGuardSockets(opened)
			return nil, 0, openErr
		}
		endpoint := broker.NewEndpoint(processID, wireGuardBindBacklog)
		if endpoint == nil {
			_ = broker.Close()
			closeProcessWireGuardSockets(opened)
			return nil, 0, net.ErrClosed
		}
		if _, activateErr := broker.Activate(endpoint); activateErr != nil {
			_ = endpoint.Close()
			_ = broker.Close()
			closeProcessWireGuardSockets(opened)
			return nil, 0, activateErr
		}
		actual, portErr := packetConnPort(broker.LocalAddr())
		if portErr != nil || selected != 0 && actual != selected {
			_ = endpoint.Close()
			_ = broker.Close()
			closeProcessWireGuardSockets(opened)
			if portErr != nil {
				return nil, 0, portErr
			}
			return nil, 0, fmt.Errorf("wireguard process packet bind port mismatch: have %d, want %d", actual, selected)
		}
		if selected == 0 {
			selected = actual
		}
		socket := &processWireGuardSocket{network: spec.network, broker: broker, endpoint: endpoint}
		opened = append(opened, socket)
		receivers = append(receivers, processWireGuardReceiveFunc(socket))
	}
	b.classifier.setReleaser(func(key ingress.AssociationKey) {
		for _, socket := range opened {
			socket.broker.Release(key)
		}
	})
	b.sockets = opened
	b.opened = true
	return receivers, selected, nil
}

func processWireGuardSocketSpecs(addresses []string) ([]processWireGuardSocketSpec, error) {
	if len(addresses) == 0 {
		return []processWireGuardSocketSpec{{network: "udp4", host: "0.0.0.0"}, {network: "udp6", host: "::"}}, nil
	}
	ordered := append([]string(nil), addresses...)
	sort.Strings(ordered)
	seen := make(map[string]struct{}, len(ordered))
	specs := make([]processWireGuardSocketSpec, 0, len(ordered))
	for _, raw := range ordered {
		addr, err := netip.ParseAddr(strings.TrimSpace(raw))
		if err != nil {
			return nil, err
		}
		network := "udp4"
		if addr.Is6() {
			network = "udp6"
		}
		key := network + "/" + addr.String()
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		specs = append(specs, processWireGuardSocketSpec{network: network, host: addr.String()})
	}
	if len(specs) == 0 {
		return nil, errors.New("wireguard process packet bind has no addresses")
	}
	return specs, nil
}

func processWireGuardReceiveFunc(socket *processWireGuardSocket) conn.ReceiveFunc {
	return func(packets [][]byte, sizes []int, endpoints []conn.Endpoint) (int, error) {
		if socket == nil || socket.endpoint == nil || len(packets) == 0 || len(sizes) == 0 || len(endpoints) == 0 {
			return 0, net.ErrClosed
		}
		n, remote, err := socket.endpoint.ReadFrom(packets[0])
		if err != nil {
			return 0, err
		}
		addrPort, err := netip.ParseAddrPort(remote.String())
		if err != nil {
			return 0, err
		}
		sizes[0] = n
		endpoints[0] = &conn.StdNetEndpoint{AddrPort: addrPort}
		return 1, nil
	}
}

func (b *processWireGuardBind) Close() error {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil
	}
	b.closed = true
	sockets := b.sockets
	b.sockets = nil
	b.mu.Unlock()
	return closeProcessWireGuardSockets(sockets)
}

func closeProcessWireGuardSockets(sockets []*processWireGuardSocket) error {
	var closeErr error
	for _, socket := range sockets {
		if socket == nil {
			continue
		}
		if socket.endpoint != nil {
			closeErr = errors.Join(closeErr, socket.endpoint.Close())
		}
		if socket.broker != nil {
			closeErr = errors.Join(closeErr, socket.broker.Close())
		}
	}
	return closeErr
}

func (*processWireGuardBind) SetMark(uint32) error { return nil }

func (b *processWireGuardBind) Send(bufs [][]byte, endpoint conn.Endpoint) error {
	if b == nil || endpoint == nil {
		return net.ErrClosed
	}
	destination, err := endpointAddrPort(endpoint)
	if err != nil {
		return err
	}
	b.mu.Lock()
	if b.closed || !b.opened {
		b.mu.Unlock()
		return net.ErrClosed
	}
	var socket *processWireGuardSocket
	for _, candidate := range b.sockets {
		if candidate != nil && (candidate.network == "udp6") == destination.Addr().Is6() {
			socket = candidate
			break
		}
	}
	b.mu.Unlock()
	if socket == nil || socket.endpoint == nil {
		return fmt.Errorf("wireguard process packet bind has no socket for %s", destination)
	}
	if err := b.classifier.observeSend(bufs, destination); err != nil {
		return err
	}
	remote := net.UDPAddrFromAddrPort(destination)
	for _, payload := range bufs {
		if _, err := socket.endpoint.WriteTo(payload, remote); err != nil {
			return err
		}
	}
	return nil
}

func (*processWireGuardBind) ParseEndpoint(raw string) (conn.Endpoint, error) {
	addrPort, err := netip.ParseAddrPort(raw)
	if err != nil {
		return nil, err
	}
	return &conn.StdNetEndpoint{AddrPort: addrPort}, nil
}

func (*processWireGuardBind) BatchSize() int { return 1 }

func packetConnPort(addr net.Addr) (uint16, error) {
	udpAddr, ok := addr.(*net.UDPAddr)
	if !ok || udpAddr.Port < 0 || udpAddr.Port > 65535 {
		return 0, fmt.Errorf("unexpected wireguard packet address %T", addr)
	}
	return uint16(udpAddr.Port), nil
}

type processWireGuardClassifier struct {
	mu sync.Mutex

	receivers  map[uint32]ingress.AssociationKey
	remotes    map[string]ingress.AssociationKey
	remoteFIFO []string
	release    func(ingress.AssociationKey)
}

func newProcessWireGuardClassifier() *processWireGuardClassifier {
	return &processWireGuardClassifier{
		receivers: make(map[uint32]ingress.AssociationKey),
		remotes:   make(map[string]ingress.AssociationKey),
	}
}

func (c *processWireGuardClassifier) setReleaser(release func(ingress.AssociationKey)) {
	c.mu.Lock()
	c.release = release
	c.mu.Unlock()
}

func (c *processWireGuardClassifier) Classify(payload []byte, metadata ingress.PacketMetadata) (ingress.AssociationKey, bool) {
	if c == nil || metadata.RemoteAddr == nil {
		return "", false
	}
	remote := metadata.RemoteAddr.String()
	c.mu.Lock()
	if _, receiver, ok := wireGuardPacketReceiver(payload); ok {
		if key := c.receivers[receiver]; key != "" {
			c.mu.Unlock()
			return key, true
		}
	}
	key, evicted, _ := c.rememberRemoteLocked(remote)
	release := c.release
	c.mu.Unlock()
	if evicted != "" && release != nil {
		release(evicted)
	}
	return key, key != ""
}

func (c *processWireGuardClassifier) observeSend(payloads [][]byte, destination netip.AddrPort) error {
	if c == nil {
		return nil
	}
	remote := destination.String()
	c.mu.Lock()
	key, evicted, remembered := c.rememberRemoteLocked(remote)
	newReceivers := make([]uint32, 0, len(payloads))
	for _, payload := range payloads {
		sender, ok := wireGuardPacketSender(payload)
		if !ok {
			continue
		}
		if existing := c.receivers[sender]; existing != "" {
			if existing != key {
				release := c.release
				c.mu.Unlock()
				if !remembered && release != nil {
					release(key)
				}
				return errors.New("wireguard process receiver index is owned by another association")
			}
			continue
		}
		newReceivers = append(newReceivers, sender)
	}
	if !remembered || len(c.receivers)+len(newReceivers) > wireGuardAssociationLimit {
		release := c.release
		c.mu.Unlock()
		if release != nil {
			release(key)
		}
		return errWireGuardAssociationLimit
	}
	for _, sender := range newReceivers {
		c.receivers[sender] = key
	}
	release := c.release
	c.mu.Unlock()
	if evicted != "" && release != nil {
		release(evicted)
	}
	return nil
}

func (c *processWireGuardClassifier) rememberRemoteLocked(remote string) (ingress.AssociationKey, ingress.AssociationKey, bool) {
	remote = strings.TrimSpace(remote)
	if remote == "" {
		return "", "", false
	}
	if key := c.remotes[remote]; key != "" {
		return key, "", true
	}
	var evicted ingress.AssociationKey
	if len(c.remotes) >= wireGuardAssociationLimit && len(c.remoteFIFO) > 0 {
		evictAt := -1
		for index, candidate := range c.remoteFIFO {
			candidateKey := c.remotes[candidate]
			if !c.receiverKeyPinnedLocked(candidateKey) {
				evictAt = index
				evicted = candidateKey
				delete(c.remotes, candidate)
				break
			}
		}
		if evictAt < 0 {
			return ingress.AssociationKey("wireguard|overflow"), "", false
		}
		c.remoteFIFO = append(c.remoteFIFO[:evictAt], c.remoteFIFO[evictAt+1:]...)
	}
	key := ingress.AssociationKey("wireguard|" + remote)
	c.remotes[remote] = key
	c.remoteFIFO = append(c.remoteFIFO, remote)
	return key, evicted, true
}

func (c *processWireGuardClassifier) receiverKeyPinnedLocked(key ingress.AssociationKey) bool {
	for _, receiverKey := range c.receivers {
		if receiverKey == key {
			return true
		}
	}
	return false
}

var _ conn.Bind = (*processWireGuardBind)(nil)
var _ ingress.PacketClassifier = (*processWireGuardClassifier)(nil)
