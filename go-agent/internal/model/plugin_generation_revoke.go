package model

import (
	"errors"
	sdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

// PluginGenerationRevokeRequest is delivered only over the authenticated Agent task channel.
type PluginGenerationRevokeRequest struct {
	InstanceID           string `json:"instance_id"`
	PluginID             string `json:"plugin_id"`
	GenerationID         string `json:"generation_id"`
	ProviderGenerationID string `json:"provider_generation_id"`
	Revision             int64  `json:"revision"`
	FenceID              string `json:"fence_id"`
}

func (r PluginGenerationRevokeRequest) Validate() error {
	for _, v := range []string{r.InstanceID, r.PluginID, r.GenerationID, r.ProviderGenerationID, r.FenceID} {
		if sdk.ValidatePolicyIdentity(v) != nil {
			return errors.New("invalid generation revocation identity")
		}
	}
	if r.Revision <= 0 {
		return errors.New("invalid generation revocation revision")
	}
	return nil
}
