package policy

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

func ValidateWAFPolicyOverlay(overlay json.RawMessage) error {
	overlay = bytes.TrimSpace(overlay)
	if len(overlay) == 0 || string(overlay) == "null" {
		return nil
	}
	decoder := json.NewDecoder(bytes.NewReader(overlay))
	opening, err := decoder.Token()
	if err != nil || opening != json.Delim('{') {
		return errors.New("waf policy overlay must be a json object")
	}
	mode := ""
	seenMode := false
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return fmt.Errorf("waf policy overlay is invalid: %w", err)
		}
		if key != "mode" {
			return fmt.Errorf("waf policy overlay field %q is unknown", key)
		}
		if seenMode {
			return errors.New("waf policy overlay mode is duplicated")
		}
		seenMode = true
		if err := decoder.Decode(&mode); err != nil {
			return fmt.Errorf("waf policy overlay mode is invalid: %w", err)
		}
	}
	if _, err := decoder.Token(); err != nil {
		return fmt.Errorf("waf policy overlay is invalid: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("waf policy overlay has trailing data")
		}
		return fmt.Errorf("waf policy overlay has invalid trailing data: %w", err)
	}
	if mode != strings.TrimSpace(mode) {
		return errors.New("waf policy overlay mode is not canonical")
	}
	switch mode {
	case model.PolicyOverlayModeObserve, model.PolicyOverlayModeDeny:
		return nil
	default:
		return fmt.Errorf("waf policy overlay mode %q is invalid", mode)
	}
}
