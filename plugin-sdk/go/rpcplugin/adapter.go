package rpcplugin

import (
	"context"
	"sync"

	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

// Adapter exposes Lifecycle as the canonical pluginsdk.RPCLifecycle surface.
// Embed it in a plugin Controller to avoid forwarding Handshake, Prepare,
// Activate, and Stop in every plugin package.
type Adapter struct {
	lifecycle *Lifecycle
	mu        sync.RWMutex
	request   pluginsdk.RPCHandshakeRequest
	accepted  bool
}

func NewAdapter(config Config, hooks Hooks) (*Adapter, error) {
	lifecycle, err := New(config, hooks)
	if err != nil {
		return nil, err
	}
	return &Adapter{lifecycle: lifecycle}, nil
}

func (adapter *Adapter) Handshake(ctx context.Context, request pluginsdk.RPCHandshakeRequest) (pluginsdk.RPCHandshakeResponse, error) {
	response, err := adapter.lifecycle.Handshake(ctx, request)
	if err == nil {
		adapter.mu.Lock()
		adapter.request = cloneHandshakeRequest(request)
		adapter.accepted = true
		adapter.mu.Unlock()
	}
	return response, err
}

// Request returns the immutable Host binding accepted by Handshake.
func (adapter *Adapter) Request() (pluginsdk.RPCHandshakeRequest, bool) {
	adapter.mu.RLock()
	defer adapter.mu.RUnlock()
	if !adapter.accepted {
		return pluginsdk.RPCHandshakeRequest{}, false
	}
	return cloneHandshakeRequest(adapter.request), true
}

func cloneHandshakeRequest(request pluginsdk.RPCHandshakeRequest) pluginsdk.RPCHandshakeRequest {
	request.GrantedScopes = append([]string(nil), request.GrantedScopes...)
	request.RequiredFeatures = append([]string(nil), request.RequiredFeatures...)
	return request
}

func (adapter *Adapter) Prepare(ctx context.Context, request pluginsdk.LifecycleRequest) pluginsdk.LifecycleResponse {
	return adapter.lifecycle.Prepare(ctx, request)
}

func (adapter *Adapter) Activate(ctx context.Context, request pluginsdk.LifecycleRequest) pluginsdk.LifecycleResponse {
	return adapter.lifecycle.Activate(ctx, request)
}

func (adapter *Adapter) Stop(ctx context.Context, request pluginsdk.LifecycleRequest) pluginsdk.LifecycleResponse {
	return adapter.lifecycle.Stop(ctx, request)
}

func (adapter *Adapter) Status(message string, fields map[string]string) Record {
	return adapter.lifecycle.Status(message, fields)
}
