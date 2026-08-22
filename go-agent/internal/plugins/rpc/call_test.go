//go:build !integration

package rpc

import (
	"context"
	"encoding/json"
	"testing"

	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

type recordingPluginCallClient struct {
	generation string
	name       string
	payload    json.RawMessage
	response   json.RawMessage
}

func (c *recordingPluginCallClient) Handshake(context.Context, pluginsdk.RPCHandshakeRequest) (pluginsdk.RPCHandshakeResponse, error) {
	return pluginsdk.RPCHandshakeResponse{}, nil
}
func (c *recordingPluginCallClient) Prepare(context.Context, pluginsdk.LifecycleRequest) (pluginsdk.LifecycleResponse, error) {
	return pluginsdk.LifecycleResponse{Success: &pluginsdk.LifecycleSuccess{Ready: true}}, nil
}
func (c *recordingPluginCallClient) Activate(context.Context, pluginsdk.LifecycleRequest) (pluginsdk.LifecycleResponse, error) {
	return pluginsdk.LifecycleResponse{Success: &pluginsdk.LifecycleSuccess{Ready: true}}, nil
}
func (c *recordingPluginCallClient) Stop(context.Context, pluginsdk.LifecycleRequest) (pluginsdk.LifecycleResponse, error) {
	return pluginsdk.LifecycleResponse{Success: &pluginsdk.LifecycleSuccess{Ready: true}}, nil
}
func (c *recordingPluginCallClient) Call(_ context.Context, generation, name string, payload json.RawMessage) (json.RawMessage, error) {
	c.generation = generation
	c.name = name
	c.payload = append(json.RawMessage(nil), payload...)
	if len(c.response) == 0 {
		return payload, nil
	}
	return c.response, nil
}

func TestHostCallForwardsOpaquePayloadWithoutSwitchingOnName(t *testing.T) {
	t.Parallel()
	client := &recordingPluginCallClient{response: json.RawMessage(`{"engine":"installed","version":"27.0"}`)}
	host := &Host{active: map[string]*HostedInstance{
		"exec-1": {
			candidate: HostCandidate{InstanceID: "exec-1", PluginID: "example.plugin", Generation: "generation-1"},
			attempt:   &hostAttempt{client: client},
		},
	}}
	got, err := host.Call(context.Background(), "example.plugin", "compose.apply", json.RawMessage(`{"yaml":"services: {}"}`))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"engine":"installed","version":"27.0"}` {
		t.Fatalf("call payload = %s", got)
	}
	if client.name != "compose.apply" || string(client.payload) != `{"yaml":"services: {}"}` {
		t.Fatalf("forwarded call = name=%q payload=%s", client.name, client.payload)
	}
}

func TestHostCallFailClosedWithoutExecutionInstance(t *testing.T) {
	t.Parallel()
	host := &Host{active: map[string]*HostedInstance{}}
	if _, err := host.Call(context.Background(), "example.plugin", "engine.report", json.RawMessage(`{}`)); err == nil {
		t.Fatal("missing execution instance was accepted")
	}
	host.active["exec-other"] = &HostedInstance{
		candidate: HostCandidate{InstanceID: "exec-other", PluginID: "other.plugin", Generation: "generation-1"},
		attempt:   &hostAttempt{client: &recordingPluginCallClient{}},
	}
	if _, err := host.Call(context.Background(), "example.plugin", "engine.report", json.RawMessage(`{}`)); err == nil {
		t.Fatal("mismatched plugin_id execution instance was accepted")
	}
}
