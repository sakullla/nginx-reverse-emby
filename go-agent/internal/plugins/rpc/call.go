package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

// PluginCallClient is the equivalent RPC used to deliver an opaque plugin.call
// envelope to a local execution instance. Hosts must not switch on name.
type PluginCallClient interface {
	Call(ctx context.Context, generation, name string, payload json.RawMessage) (json.RawMessage, error)
}

func (h *Host) Call(ctx context.Context, pluginID, name string, payload json.RawMessage) (json.RawMessage, error) {
	if h == nil || ctx == nil {
		return nil, errors.New("Agent RPC plugin call host and context are required")
	}
	pluginID = strings.TrimSpace(pluginID)
	if pluginsdk.ValidatePolicyIdentity(pluginID) != nil || pluginsdk.ValidatePolicyIdentity(name) != nil {
		return nil, errors.New("plugin call identity is invalid")
	}
	if len(payload) > pluginsdk.PluginHostPayloadMaxBytes || (len(payload) > 0 && !json.Valid(payload)) {
		return nil, errors.New("plugin call payload is invalid or exceeds the canonical bound")
	}
	instance, generation, err := h.activeByPluginID(pluginID)
	if err != nil {
		return nil, err
	}
	instance.mu.RLock()
	attempt := instance.attempt
	var caller PluginCallClient
	if attempt != nil {
		caller, _ = attempt.client.(PluginCallClient)
	}
	instance.mu.RUnlock()
	if caller == nil {
		return nil, errors.New("plugin execution instance is unavailable")
	}
	response, err := caller.Call(ctx, generation, name, payload)
	if err != nil {
		return nil, err
	}
	if len(response) > pluginsdk.PluginHostPayloadMaxBytes || (len(response) > 0 && !json.Valid(response)) {
		return nil, errors.New("plugin call response is invalid or exceeds the canonical bound")
	}
	h.mu.RLock()
	stillActive := h.active[instance.candidate.InstanceID] == instance && instance.candidate.Generation == generation
	h.mu.RUnlock()
	if !stillActive {
		return nil, errors.New("Agent RPC plugin call generation drained during dispatch")
	}
	return response, nil
}

func (h *Host) activeByPluginID(pluginID string) (*HostedInstance, string, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	var match *HostedInstance
	for _, instance := range h.active {
		if instance == nil || instance.candidate.PluginID != pluginID {
			continue
		}
		if match != nil {
			return nil, "", errors.New("plugin execution instance is ambiguous")
		}
		match = instance
	}
	if match == nil || strings.TrimSpace(match.candidate.Generation) == "" {
		return nil, "", errors.New("plugin execution instance is unavailable")
	}
	return match, match.candidate.Generation, nil
}

func (c *Client) Call(ctx context.Context, generation, name string, payload json.RawMessage) (json.RawMessage, error) {
	if c == nil || c.conn == nil {
		return nil, errors.New("RPC plugin client is unavailable")
	}
	if pluginsdk.ValidatePolicyIdentity(generation) != nil || pluginsdk.ValidatePolicyIdentity(name) != nil {
		return nil, errors.New("plugin call identity is invalid")
	}
	if len(payload) > pluginsdk.PluginHostPayloadMaxBytes || (len(payload) > 0 && !json.Valid(payload)) {
		return nil, errors.New("plugin call payload is invalid or exceeds the canonical bound")
	}
	envelope, err := json.Marshal(struct {
		Generation string          `json:"generation"`
		Name       string          `json:"name"`
		Payload    json.RawMessage `json:"payload,omitempty"`
	}{Generation: generation, Name: name, Payload: payload})
	if err != nil {
		return nil, err
	}
	callCtx, cancel := context.WithTimeout(ctx, c.deadline)
	defer cancel()
	callCtx = metadata.AppendToOutgoingContext(callCtx, CookieMetadataKey, c.cookie)
	out := &wrapperspb.BytesValue{}
	if err := c.conn.Invoke(callCtx, "/"+rpcServiceName+"/Call", wrapperspb.Bytes(envelope), out); err != nil {
		return nil, fmt.Errorf("RPC plugin call: %w", err)
	}
	result := append([]byte(nil), out.GetValue()...)
	if len(result) > pluginsdk.PluginHostPayloadMaxBytes || (len(result) > 0 && !json.Valid(result)) {
		return nil, errors.New("plugin call response is invalid or exceeds the canonical bound")
	}
	return json.RawMessage(result), nil
}
