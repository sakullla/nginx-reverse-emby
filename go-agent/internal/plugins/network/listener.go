package network

import (
	"context"
	"net"
	"sync"
	"time"

	sdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

type listener struct {
	manager         *Manager
	key, instanceID string
	tcp             net.Listener
	udp             *net.UDPConn
	mu              sync.Mutex
	owners          map[*Owner]*resource
	active          *resource
	flows           map[string]*resource
	writeGate       chan struct{}
	closed          bool
}

func (owner *Owner) listen(request sdk.ManagedNetworkRequest) (sdk.ManagedNetworkResponse, error) {
	manager := owner.manager
	key := request.Protocol + "://" + endpointString(*request.Endpoint)
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.closed {
		return sdk.ManagedNetworkResponse{}, net.ErrClosed
	}
	shared := manager.listeners[key]
	if shared != nil {
		shared.mu.Lock()
		defer shared.mu.Unlock()
		if shared.closed || shared.instanceID != owner.authority.InstanceID {
			return sdk.ManagedNetworkResponse{}, fail(sdk.ErrorPermissionDenied, "listener belongs to another instance")
		}
		if existing := shared.owners[owner]; existing != nil && !existing.closed.Load() {
			if existing.maxFlows != request.MaxFlows || existing.idle != time.Duration(request.IdleMS)*time.Millisecond {
				return sdk.ManagedNetworkResponse{}, fail(sdk.ErrorInvalidArgument, "listener budget changed within generation")
			}
			return sdk.ManagedNetworkResponse{Handle: &existing.handle}, nil
		}
	} else {
		shared = &listener{manager: manager, key: key, instanceID: owner.authority.InstanceID, owners: map[*Owner]*resource{}, flows: map[string]*resource{}, writeGate: make(chan struct{}, 1)}
		var err error
		if request.Protocol == "tcp" {
			shared.tcp, err = net.Listen("tcp", endpointString(*request.Endpoint))
		} else {
			var address *net.UDPAddr
			address, err = net.ResolveUDPAddr("udp", endpointString(*request.Endpoint))
			if err == nil {
				shared.udp, err = net.ListenUDP("udp", address)
			}
		}
		if err != nil {
			return sdk.ManagedNetworkResponse{}, fail(sdk.ErrorUnavailable, "managed listen endpoint unavailable")
		}
		manager.listeners[key] = shared
		shared.mu.Lock()
		defer shared.mu.Unlock()
		if shared.tcp != nil {
			go shared.acceptTCP()
		} else {
			go shared.acceptUDP()
		}
	}
	record, err := owner.newResource("listener", request.Protocol, sdk.PermissionManagedNetworkListen, nil, 0, func(record *resource) {
		record.listener = shared
		record.accepted = make(chan *resource, maxPending)
		record.maxFlows = request.MaxFlows
		record.idle = time.Duration(request.IdleMS) * time.Millisecond
	})
	if err != nil {
		go shared.collect()
		return sdk.ManagedNetworkResponse{}, err
	}
	shared.owners[owner] = record
	owner.mu.Lock()
	active := owner.active && !owner.retired && !owner.closed
	owner.mu.Unlock()
	if active {
		shared.active = record
	}
	return sdk.ManagedNetworkResponse{Handle: &record.handle}, nil
}

func (shared *listener) remove(record *resource) {
	shared.mu.Lock()
	if shared.owners[record.owner] == record {
		delete(shared.owners, record.owner)
	}
	if shared.active == record {
		shared.active = nil
	}
	shared.mu.Unlock()
	// Undelivered TCP accepts cannot pin a retired generation indefinitely.
	for {
		select {
		case flow := <-record.accepted:
			flow.close()
		default:
			shared.collect()
			return
		}
	}
}
func (shared *listener) collect() {
	shared.manager.mu.Lock()
	defer shared.manager.mu.Unlock()
	shared.mu.Lock()
	defer shared.mu.Unlock()
	if shared.closed || len(shared.owners) > 0 || len(shared.flows) > 0 {
		return
	}
	shared.closed = true
	if shared.manager.listeners[shared.key] == shared {
		delete(shared.manager.listeners, shared.key)
	}
	if shared.tcp != nil {
		_ = shared.tcp.Close()
	}
	if shared.udp != nil {
		_ = shared.udp.Close()
	}
}
func (shared *listener) current() *resource {
	shared.mu.Lock()
	defer shared.mu.Unlock()
	if shared.active == nil || shared.active.closed.Load() {
		return nil
	}
	return shared.active
}

func (shared *listener) acceptTCP() {
	for {
		connection, err := shared.tcp.Accept()
		if err != nil {
			return
		}
		parent := shared.current()
		if parent == nil {
			_ = connection.Close()
			continue
		}
		source, err := sourceMetadata(connection.RemoteAddr())
		if err == nil {
			ctx, cancel := context.WithTimeout(parent.owner.ctx, 10*time.Millisecond)
			err = parent.owner.authority.Admit(ctx, source)
			if err == nil {
				err = ctx.Err()
			}
			cancel()
		}
		if err != nil {
			_ = connection.Close()
			continue
		}
		flow, err := parent.owner.newResource("stream", "tcp", sdk.PermissionManagedNetworkListen, parent, parent.idle, func(flow *resource) { flow.source = &source })
		if err != nil {
			_ = connection.Close()
			continue
		}
		if !flow.attach(connection) {
			continue
		}
		if err := flow.track(); err != nil {
			flow.close()
			continue
		}
		flow.touch()
		if parent.closed.Load() {
			flow.close()
			continue
		}
		select {
		case parent.accepted <- flow:
		case <-parent.done:
			flow.close()
		default:
			flow.close()
		}
	}
}

func (shared *listener) acceptUDP() {
	buffer := make([]byte, 65536)
	for {
		n, peer, err := shared.udp.ReadFromUDP(buffer)
		if err != nil {
			return
		}
		if n > sdk.ManagedNetworkMaxDatagramBytes {
			continue
		}
		shared.mu.Lock()
		flow := shared.flows[peer.String()]
		shared.mu.Unlock()
		if flow != nil && flow.closed.Load() {
			flow = nil
		}
		if flow == nil {
			parent := shared.current()
			if parent == nil {
				continue
			}
			source, err := sourceMetadata(peer)
			if err == nil {
				ctx, cancel := context.WithTimeout(parent.owner.ctx, 10*time.Millisecond)
				err = parent.owner.authority.Admit(ctx, source)
				if err == nil {
					err = ctx.Err()
				}
				cancel()
			}
			if err != nil {
				continue
			}
			flow, err = parent.owner.newResource("datagram", "udp", sdk.PermissionManagedNetworkListen, parent, parent.idle, func(flow *resource) {
				flow.peer = peer
				flow.listener = shared
				flow.source = &source
				flow.datagrams = make(chan []byte, maxPending)
			})
			if err != nil {
				continue
			}
			if err := flow.track(); err != nil {
				flow.close()
				continue
			}
			shared.mu.Lock()
			if flow.closed.Load() {
				shared.mu.Unlock()
				continue
			}
			shared.flows[peer.String()] = flow
			shared.mu.Unlock()
			if parent.closed.Load() {
				flow.close()
				continue
			}
			select {
			case parent.accepted <- flow:
			case <-parent.done:
				flow.close()
				continue
			default:
				flow.close()
				continue
			}
		}
		flow.touch()
		flow.enqueuePacket(buffer[:n])
	}
}

func (owner *Owner) dial(ctx context.Context, request sdk.ManagedNetworkRequest) (sdk.ManagedNetworkResponse, error) {
	kind := "stream"
	if request.Protocol == "udp" {
		kind = "datagram"
	}
	flow, err := owner.newResource(kind, request.Protocol, sdk.PermissionManagedNetworkDial, nil, time.Duration(request.IdleMS)*time.Millisecond)
	if err != nil {
		return sdk.ManagedNetworkResponse{}, err
	}
	dialCtx, cancel := context.WithTimeout(ctx, time.Duration(request.WaitMS)*time.Millisecond)
	defer cancel()
	connection, err := (&net.Dialer{}).DialContext(dialCtx, request.Protocol, endpointString(*request.Endpoint))
	if err != nil {
		flow.close()
		return sdk.ManagedNetworkResponse{}, fail(sdk.ErrorUnavailable, "managed outbound connection failed")
	}
	if !flow.attach(connection) {
		return sdk.ManagedNetworkResponse{}, net.ErrClosed
	}
	if err := flow.track(); err != nil {
		flow.close()
		return sdk.ManagedNetworkResponse{}, err
	}
	flow.touch()
	return sdk.ManagedNetworkResponse{Handle: &flow.handle}, nil
}
