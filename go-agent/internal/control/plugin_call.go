package control

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

// PluginCaller locates the local execution instance for one plugin_id and
// forwards an opaque action name and payload. Implementations must not switch
// on the action name.
type PluginCaller interface {
	Call(ctx context.Context, pluginID, name string, payload json.RawMessage) (json.RawMessage, error)
}

func HandlePluginCallTask(ctx context.Context, caller PluginCaller, task TaskMessage) (map[string]any, error) {
	if caller == nil {
		return nil, errors.New("plugin execution instance is unavailable")
	}
	pluginID, _ := task.RawPayload["plugin_id"].(string)
	name, _ := task.RawPayload["name"].(string)
	pluginID = strings.TrimSpace(pluginID)
	name = strings.TrimSpace(name)
	if pluginsdk.ValidatePolicyIdentity(pluginID) != nil || pluginsdk.ValidatePolicyIdentity(name) != nil {
		return nil, errors.New("plugin.call payload is invalid")
	}
	var payload json.RawMessage
	if raw, ok := task.RawPayload["payload"]; ok && raw != nil {
		encoded, err := json.Marshal(raw)
		if err != nil {
			return nil, err
		}
		if len(encoded) > pluginsdk.PluginHostPayloadMaxBytes {
			return nil, errors.New("plugin.call payload exceeds the canonical bound")
		}
		payload = encoded
	}
	response, err := caller.Call(ctx, pluginID, name, payload)
	if err != nil {
		return nil, err
	}
	if len(response) > pluginsdk.PluginHostPayloadMaxBytes || (len(response) > 0 && !json.Valid(response)) {
		return nil, errors.New("plugin.call response is invalid or exceeds the canonical bound")
	}
	if len(response) == 0 {
		response = json.RawMessage("null")
	}
	return map[string]any{"payload": json.RawMessage(response)}, nil
}
