package http

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/service"
	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

var pluginActionIdempotencyKeyPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{16,128}$`)

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
		TargetID  string `json:"target_id"`
		Confirmed bool   `json:"confirmed"`
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeJSON(w, http.StatusBadRequest, errorPayload("invalid plugin action request"))
		return
	}
	idempotencyKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if !pluginActionIdempotencyKeyPattern.MatchString(idempotencyKey) {
		writeJSON(w, http.StatusBadRequest, errorPayload("Idempotency-Key must be 16 to 128 canonical opaque characters"))
		return
	}
	operationID := pluginActionOperationID(actor.ID, idempotencyKey)
	result, err := d.PluginCapabilityService.InvokeDynamicAction(r.Context(), service.PluginDynamicActionRequest{
		OperationID: operationID,
		PluginID:    r.PathValue("id"), InstanceID: r.PathValue("instance"), ActionID: r.PathValue("action"), Actor: actor,
		Target: pluginsdk.HostTarget{ID: input.TargetID}, Confirmed: input.Confirmed,
	})
	if err != nil {
		writePluginError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func pluginActionOperationID(actorID, key string) string {
	digest := sha256.Sum256([]byte(fmt.Sprintf("plugin.action.operation.v1\x00%s\x00%s", actorID, key)))
	return fmt.Sprintf("%x", digest[:])
}
