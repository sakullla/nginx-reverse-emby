package http

import (
	"encoding/json"
	"net/http"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/service"
	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

func (d Dependencies) handlePluginDynamicAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, errorPayload("method not allowed"))
		return
	}
	actor, ok := actorFromRequest(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, errorPayload("plugin action actor is unavailable"))
		return
	}
	var input struct {
		OperationID     string `json:"operation_id"`
		TargetKind      string `json:"target_kind"`
		TargetID        string `json:"target_id"`
		ResourceGroupID string `json:"resource_group_id"`
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeJSON(w, http.StatusBadRequest, errorPayload("invalid plugin action request"))
		return
	}
	result, err := d.PluginCapabilityService.InvokeDynamicAction(r.Context(), service.PluginDynamicActionRequest{
		OperationID: input.OperationID,
		PluginID:    r.PathValue("id"), InstanceID: r.PathValue("instance"), ActionID: r.PathValue("action"), Actor: actor,
		Target: pluginsdk.HostTarget{Kind: input.TargetKind, ID: input.TargetID, ResourceGroupID: input.ResourceGroupID},
	})
	if err != nil {
		writePluginError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}
