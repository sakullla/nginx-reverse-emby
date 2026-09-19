package control

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

const TaskTypePluginGenerationRevoke = "plugin.generation.revoke"

type PluginGenerationRevoker interface {
	RevokeGeneration(context.Context, model.PluginGenerationRevokeRequest) error
}

func HandlePluginGenerationRevokeTask(ctx context.Context, revoker PluginGenerationRevoker, task TaskMessage) (map[string]any, error) {
	if revoker == nil {
		return nil, errors.New("plugin generation revoker unavailable")
	}
	raw, err := json.Marshal(task.RawPayload)
	if err != nil {
		return nil, err
	}
	if len(raw) > 4096 {
		return nil, errors.New("generation revoke payload oversized")
	}
	var request model.PluginGenerationRevokeRequest
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		return nil, err
	}
	if err := request.Validate(); err != nil {
		return nil, err
	}
	if err := revoker.RevokeGeneration(ctx, request); err != nil {
		return nil, err
	}
	return map[string]any{"fence_id": request.FenceID, "generation_id": request.GenerationID, "revoked": true}, nil
}
