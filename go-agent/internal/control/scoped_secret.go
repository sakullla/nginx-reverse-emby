package control

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	sdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

func (c *SyncClient) RedeemScopedPluginSecret(ctx context.Context, input model.PluginSecretRedemptionRequest) (json.RawMessage, error) {
	if c == nil || c.client == nil || ctx == nil || input.Validate() != nil || len(input.Scoped) == 0 {
		return nil, errors.New("scoped secret redemption identity unavailable")
	}
	encoded, err := json.Marshal(input)
	if err != nil {
		return nil, errors.New("scoped secret redemption encoding failed")
	}
	defer clear(encoded)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.MasterURL+"/api/agent-plugin-secrets/redeem", bytes.NewReader(encoded))
	if err != nil {
		return nil, errors.New("scoped secret request unavailable")
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("x-agent-token", c.cfg.AgentToken)
	response, err := c.client.Do(request)
	if err != nil {
		return nil, errors.New("scoped secret transport failed")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, errors.New("scoped secret redemption rejected")
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, sdk.PluginHostPayloadMaxBytes+1))
	if err != nil || len(data) > sdk.PluginHostPayloadMaxBytes {
		return nil, errors.New("scoped secret response exceeded bound")
	}
	defer clear(data)
	var result model.PluginSecretRedemptionResponse
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&result) != nil || len(result.Scoped) == 0 || len(result.Secrets) > 0 {
		return nil, errors.New("scoped secret response is invalid")
	}
	var trailing any
	if decoder.Decode(&trailing) != io.EOF {
		return nil, errors.New("scoped secret trailing data")
	}
	return result.Scoped, nil
}
