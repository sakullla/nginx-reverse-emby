// Package network owns sockets granted to isolated RPC plugin generations.
// It transports opaque bytes and never interprets application protocols.
package network

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net"
	"net/netip"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/generation"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	sdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

const maxResources = 4096
const maxPending = 32
const maxBufferedDatagramBytes = 64 << 20

type Authority struct {
	InstanceID, Generation string
	Grants                 []model.PluginGrantProjection
	Admit                  func(context.Context, sdk.ManagedSourceMetadata) error
	Track                  func(generation.Session) (func(), error)
}
type Manager struct {
	mu        sync.Mutex
	listeners map[string]*listener
	owners    map[*Owner]bool
	resources atomic.Int64
	buffered  atomic.Int64
	closed    bool
}

func NewManager() *Manager {
	return &Manager{listeners: map[string]*listener{}, owners: map[*Owner]bool{}}
}

type Owner struct {
	manager    *Manager
	authority  Authority
	ctx        context.Context
	cancel     context.CancelFunc
	mu         sync.Mutex
	active     bool
	retired    bool
	closed     bool
	handles    map[string]*resource
	operations map[string]context.CancelFunc
}

func (manager *Manager) Stage(authority Authority) (*Owner, error) {
	if sdk.ValidatePolicyIdentity(authority.InstanceID) != nil || sdk.ValidatePolicyIdentity(authority.Generation) != nil || authority.Admit == nil {
		return nil, fail(sdk.ErrorInvalidArgument, "managed network authority is incomplete")
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.closed {
		return nil, net.ErrClosed
	}
	authority.Grants = append([]model.PluginGrantProjection(nil), authority.Grants...)
	ctx, cancel := context.WithCancel(context.Background())
	owner := &Owner{manager: manager, authority: authority, ctx: ctx, cancel: cancel, handles: map[string]*resource{}, operations: map[string]context.CancelFunc{}}
	manager.owners[owner] = true
	return owner, nil
}
func (owner *Owner) Binding() sdk.ManagedBinding {
	return sdk.ManagedBinding{InstanceID: owner.authority.InstanceID, Generation: owner.authority.Generation, EntryID: owner.authority.InstanceID}
}

func (owner *Owner) Context() context.Context { return owner.ctx }
func (owner *Owner) Activate() {
	owner.mu.Lock()
	if owner.closed || owner.retired {
		owner.mu.Unlock()
		return
	}
	owner.active = true
	handles := owner.copyHandles()
	owner.mu.Unlock()
	for _, handle := range handles {
		if handle.handle.Kind == "listener" && handle.listener != nil {
			handle.listener.mu.Lock()
			owner.mu.Lock()
			if owner.active && !owner.retired && !owner.closed && !handle.closed.Load() {
				handle.listener.active = handle
			}
			owner.mu.Unlock()
			handle.listener.mu.Unlock()
		}
	}
}
func (owner *Owner) Retire() {
	owner.mu.Lock()
	owner.active = false
	owner.retired = true
	handles := owner.copyHandles()
	owner.mu.Unlock()
	for _, handle := range handles {
		if handle.handle.Kind == "listener" {
			handle.close()
		}
	}
}
func (owner *Owner) Close() error {
	if owner == nil {
		return nil
	}
	owner.mu.Lock()
	if owner.closed {
		owner.mu.Unlock()
		return nil
	}
	owner.closed = true
	owner.active = false
	owner.cancel()
	handles := owner.copyHandles()
	owner.mu.Unlock()
	for _, handle := range handles {
		handle.close()
	}
	owner.manager.mu.Lock()
	delete(owner.manager.owners, owner)
	owner.manager.mu.Unlock()
	return nil
}
func (owner *Owner) copyHandles() []*resource {
	result := make([]*resource, 0, len(owner.handles))
	for _, handle := range owner.handles {
		result = append(result, handle)
	}
	return result
}
func (manager *Manager) Close() error {
	manager.mu.Lock()
	manager.closed = true
	owners := make([]*Owner, 0, len(manager.owners))
	for owner := range manager.owners {
		owners = append(owners, owner)
	}
	manager.mu.Unlock()
	for _, owner := range owners {
		_ = owner.Close()
	}
	return nil
}

func fail(code sdk.ErrorCode, message string) error {
	return &sdk.RuntimeError{Code: code, Message: message}
}
func token() string {
	value := make([]byte, 32)
	_, _ = rand.Read(value)
	return hex.EncodeToString(value)
}
func endpointString(endpoint sdk.ManagedNetworkEndpoint) string {
	return net.JoinHostPort(endpoint.Host, strconv.Itoa(endpoint.Port))
}
func sourceMetadata(address net.Addr) (sdk.ManagedSourceMetadata, error) {
	host, port, err := net.SplitHostPort(address.String())
	if err != nil {
		return sdk.ManagedSourceMetadata{}, err
	}
	ip, err := netip.ParseAddr(host)
	if err != nil || ip.Zone() != "" {
		return sdk.ManagedSourceMetadata{}, errors.New("source address unavailable")
	}
	number, err := strconv.Atoi(port)
	if err != nil {
		return sdk.ManagedSourceMetadata{}, err
	}
	endpoint := sdk.ManagedNetworkEndpoint{Host: ip.Unmap().String(), Port: number}
	source := sdk.ManagedSourceMetadata{Peer: endpoint, Source: endpoint, Authority: "socket"}
	return source, source.Validate()
}
func (owner *Owner) allowed(permission string, endpoint *sdk.ManagedNetworkEndpoint, protocol string) bool {
	for _, grant := range owner.authority.Grants {
		if grant.Name != permission {
			continue
		}
		if grant.ResourceID == "" {
			return true
		}
		if endpoint != nil && (grant.ResourceKind == "" || grant.ResourceKind == "network-endpoint") && grant.ResourceID == protocol+"://"+endpointString(*endpoint) {
			return true
		}
	}
	return false
}
func (owner *Owner) scopes() []string {
	scopes := make([]string, 0, len(owner.authority.Grants))
	for _, grant := range owner.authority.Grants {
		scopes = append(scopes, grant.Name)
	}
	return scopes
}

type resource struct {
	packetMu                sync.Mutex
	owner                   *Owner
	handle                  sdk.ManagedNetworkHandle
	permission              string
	listener                *listener
	parent                  *resource
	conn                    net.Conn
	peer                    *net.UDPAddr
	source                  *sdk.ManagedSourceMetadata
	accepted                chan *resource
	datagrams               chan []byte
	readGate, writeGate     chan struct{}
	closed                  atomic.Bool
	readClosed, writeClosed atomic.Bool
	done                    chan struct{}
	closeOnce               sync.Once
	children                atomic.Int64
	maxFlows                int
	idle                    time.Duration
	mu                      sync.Mutex
	timer                   *time.Timer
	lastActivity            time.Time
	finish                  func()
}

func (owner *Owner) newResource(kind, protocol, permission string, parent *resource, idle time.Duration, configure ...func(*resource)) (*resource, error) {
	if owner.manager.resources.Add(1) > maxResources {
		owner.manager.resources.Add(-1)
		return nil, fail(sdk.ErrorResourceExhausted, "managed socket budget exceeded")
	}
	if parent != nil && parent.children.Add(1) > int64(parent.maxFlows) {
		parent.children.Add(-1)
		owner.manager.resources.Add(-1)
		return nil, fail(sdk.ErrorResourceExhausted, "managed listener flow budget exceeded")
	}
	resource := &resource{owner: owner, handle: sdk.ManagedNetworkHandle{Binding: owner.Binding(), Token: token(), Kind: kind, Protocol: protocol}, permission: permission, parent: parent, done: make(chan struct{}), readGate: make(chan struct{}, 1), writeGate: make(chan struct{}, 1), idle: idle}
	for _, initialize := range configure {
		initialize(resource)
	}
	owner.mu.Lock()
	if owner.closed {
		owner.mu.Unlock()
		if parent != nil {
			parent.children.Add(-1)
		}
		owner.manager.resources.Add(-1)
		return nil, net.ErrClosed
	}
	owner.handles[resource.handle.Token] = resource
	owner.mu.Unlock()
	return resource, nil
}
func (resource *resource) touch() {
	if resource.idle <= 0 {
		return
	}
	resource.mu.Lock()
	defer resource.mu.Unlock()
	if resource.closed.Load() {
		return
	}
	if resource.timer == nil {
		resource.timer = time.AfterFunc(resource.idle, resource.expire)
	} else {
		resource.timer.Reset(resource.idle)
	}
	resource.lastActivity = time.Now()
}

func (resource *resource) expire() {
	resource.mu.Lock()
	if resource.closed.Load() {
		resource.mu.Unlock()
		return
	}
	remaining := resource.idle - time.Since(resource.lastActivity)
	if remaining > 0 {
		resource.timer.Reset(remaining)
		resource.mu.Unlock()
		return
	}
	resource.mu.Unlock()
	resource.close()
}
func (resource *resource) ForceClose(context.Context, string) error { resource.close(); return nil }
func (resource *resource) attach(conn net.Conn) bool {
	resource.mu.Lock()
	defer resource.mu.Unlock()
	if resource.closed.Load() {
		_ = conn.Close()
		return false
	}
	resource.conn = conn
	return true
}
func (resource *resource) track() error {
	if resource.owner.authority.Track == nil {
		return nil
	}
	finish, err := resource.owner.authority.Track(resource)
	if err != nil {
		return err
	}
	resource.mu.Lock()
	if resource.closed.Load() {
		resource.mu.Unlock()
		if finish != nil {
			finish()
		}
		return net.ErrClosed
	}
	resource.finish = finish
	resource.mu.Unlock()
	return nil
}
func (resource *resource) close() {
	resource.closeOnce.Do(func() {
		resource.closed.Store(true)
		close(resource.done)
		resource.mu.Lock()
		conn, finish := resource.conn, resource.finish
		if resource.timer != nil {
			resource.timer.Stop()
		}
		resource.mu.Unlock()
		if conn != nil {
			_ = conn.Close()
		}
		if resource.handle.Kind == "listener" && resource.listener != nil {
			resource.listener.remove(resource)
		}
		resource.packetMu.Lock()
		if resource.datagrams != nil {
			for {
				select {
				case packet := <-resource.datagrams:
					resource.owner.manager.buffered.Add(-int64(len(packet)))
				default:
					goto packetsDrained
				}
			}
		}
	packetsDrained:
		resource.packetMu.Unlock()
		if resource.parent != nil {
			resource.parent.children.Add(-1)
			if resource.listener != nil && resource.peer != nil {
				resource.listener.mu.Lock()
				if resource.listener.flows[resource.peer.String()] == resource {
					delete(resource.listener.flows, resource.peer.String())
				}
				resource.listener.mu.Unlock()
				resource.listener.collect()
			}
		}
		resource.owner.mu.Lock()
		delete(resource.owner.handles, resource.handle.Token)
		resource.owner.mu.Unlock()
		resource.owner.manager.resources.Add(-1)
		if finish != nil {
			finish()
		}
	})
}

func (owner *Owner) Handle(ctx context.Context, request sdk.ManagedNetworkRequest) (sdk.ManagedNetworkResponse, error) {
	if request.Validate() != nil || request.Binding != owner.Binding() {
		return sdk.ManagedNetworkResponse{}, fail(sdk.ErrorPermissionDenied, "managed request binding is invalid")
	}
	owner.mu.Lock()
	if owner.closed {
		owner.mu.Unlock()
		return sdk.ManagedNetworkResponse{}, fail(sdk.ErrorUnavailable, "managed generation revoked")
	}
	if !owner.active && request.Action == sdk.ManagedNetworkDial {
		owner.mu.Unlock()
		return sdk.ManagedNetworkResponse{}, fail(sdk.ErrorUnavailable, "managed generation not active")
	}
	if owner.retired && (request.Action == sdk.ManagedNetworkListen || request.Action == sdk.ManagedNetworkDial || request.Action == sdk.ManagedNetworkAccept) {
		owner.mu.Unlock()
		return sdk.ManagedNetworkResponse{}, fail(sdk.ErrorUnavailable, "managed generation is draining")
	}
	if request.Action == sdk.ManagedNetworkCancel {
		cancel := owner.operations[request.TargetRequestID]
		owner.mu.Unlock()
		if cancel != nil {
			cancel()
		}
		return sdk.ManagedNetworkResponse{Done: true}, nil
	}
	var record *resource
	if request.Handle != nil {
		record = owner.handles[request.Handle.Token]
	}
	if _, exists := owner.operations[request.RequestID]; exists || len(owner.operations) >= 128 {
		owner.mu.Unlock()
		return sdk.ManagedNetworkResponse{}, fail(sdk.ErrorResourceExhausted, "managed operation identity or concurrency exceeded")
	}
	operation, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(owner.ctx, cancel)
	owner.operations[request.RequestID] = cancel
	owner.mu.Unlock()
	defer func() {
		stop()
		cancel()
		owner.mu.Lock()
		delete(owner.operations, request.RequestID)
		owner.mu.Unlock()
	}()
	var known *sdk.ManagedNetworkRecord
	if record != nil {
		known = &sdk.ManagedNetworkRecord{Handle: record.handle, Active: !record.closed.Load(), OriginPermission: record.permission, ReadClosed: record.readClosed.Load(), WriteClosed: record.writeClosed.Load()}
	}
	if err := sdk.ValidateManagedNetworkBinding(request, owner.Binding(), known, owner.scopes()); err != nil {
		return sdk.ManagedNetworkResponse{}, fail(sdk.ErrorPermissionDenied, "managed handle or permission denied")
	}
	var response sdk.ManagedNetworkResponse
	var err error
	switch request.Action {
	case sdk.ManagedNetworkListen:
		if !owner.allowed(sdk.PermissionManagedNetworkListen, request.Endpoint, request.Protocol) {
			return response, fail(sdk.ErrorPermissionDenied, "listen endpoint denied")
		}
		response, err = owner.listen(request)
	case sdk.ManagedNetworkDial:
		if !owner.allowed(sdk.PermissionManagedNetworkDial, request.Endpoint, request.Protocol) {
			return response, fail(sdk.ErrorPermissionDenied, "dial endpoint denied")
		}
		response, err = owner.dial(operation, request)
	case sdk.ManagedNetworkAccept:
		var flow *resource
		flow, err = awaitResource(operation, record, request.WaitMS)
		if err == nil {
			response.Handle = &flow.handle
			response.Source = flow.source
		}
	case sdk.ManagedNetworkRead:
		response, err = record.read(operation, request)
	case sdk.ManagedNetworkWrite:
		response, err = record.write(operation, request)
	case sdk.ManagedNetworkReceive:
		response, err = record.receive(operation, request)
	case sdk.ManagedNetworkSend:
		response, err = record.send(operation, request)
	case sdk.ManagedNetworkHalfClose:
		err = record.halfClose(request.Direction)
		response.Done = err == nil
	case sdk.ManagedNetworkClose:
		record.close()
		response.Done = true
	default:
		err = fail(sdk.ErrorInvalidArgument, "managed action unsupported")
	}
	if err != nil {
		return sdk.ManagedNetworkResponse{}, err
	}
	if err := response.ValidateFor(request); err != nil {
		return sdk.ManagedNetworkResponse{}, fail(sdk.ErrorInternal, "managed response invalid")
	}
	return response, nil
}

func awaitResource(ctx context.Context, parent *resource, wait int) (*resource, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Duration(wait)*time.Millisecond)
	defer cancel()
	for {
		select {
		case flow := <-parent.accepted:
			if !flow.closed.Load() {
				return flow, nil
			}
		case <-parent.done:
			return nil, net.ErrClosed
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}
func acquire(ctx context.Context, resource *resource, gate chan struct{}) (func(), error) {
	select {
	case gate <- struct{}{}:
		if resource.closed.Load() {
			<-gate
			return nil, net.ErrClosed
		}
		return func() { <-gate }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-resource.done:
		return nil, net.ErrClosed
	}
}

// Endpoint grant selectors use exactly protocol://host:port. Empty selectors
// are an explicit instance-wide managed grant, never ambient network access.
func ValidEndpointSelector(value string) bool {
	return model.ValidManagedNetworkEndpointSelector(sdk.PermissionManagedNetworkDial, value)
}

// enqueuePacket charges shared Host memory before copying; overflow closes the flow.
func (resource *resource) enqueuePacket(value []byte) {
	resource.packetMu.Lock()
	if resource.closed.Load() {
		resource.packetMu.Unlock()
		return
	}
	size := int64(len(value))
	if resource.owner.manager.buffered.Add(size) > maxBufferedDatagramBytes {
		resource.owner.manager.buffered.Add(-size)
		resource.packetMu.Unlock()
		resource.close()
		return
	}
	packet := append([]byte(nil), value...)
	select {
	case resource.datagrams <- packet:
		resource.packetMu.Unlock()
		return
	default:
		resource.owner.manager.buffered.Add(-size)
		resource.packetMu.Unlock()
		resource.close()
	}
}
