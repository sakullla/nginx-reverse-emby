package policy

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

func ValidateWAFPolicyOverlay(overlay json.RawMessage) error {
	overlay = bytes.TrimSpace(overlay)
	if len(overlay) == 0 || string(overlay) == "null" {
		return nil
	}
	if !json.Valid(overlay) {
		return errors.New("waf policy overlay is not valid json")
	}
	decoder := json.NewDecoder(bytes.NewReader(overlay))
	decoder.DisallowUnknownFields()
	var parsed struct {
		Mode string `json:"mode"`
	}
	if err := decoder.Decode(&parsed); err != nil {
		return fmt.Errorf("waf policy overlay is invalid: %w", err)
	}
	if decoder.More() {
		return errors.New("waf policy overlay has trailing data")
	}
	switch strings.TrimSpace(parsed.Mode) {
	case model.PolicyOverlayModeObserve, model.PolicyOverlayModeDeny:
		return nil
	default:
		return fmt.Errorf("waf policy overlay mode %q is invalid", parsed.Mode)
	}
}
