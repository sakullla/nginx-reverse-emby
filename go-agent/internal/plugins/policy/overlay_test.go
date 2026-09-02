//go:build !integration

package policy

import (
	"encoding/json"
	"testing"
)

func TestValidateWAFPolicyOverlayAcceptsEmptyObserveAndDeny(t *testing.T) {
	t.Parallel()
	for _, overlay := range []json.RawMessage{nil, json.RawMessage(""), json.RawMessage("null"), json.RawMessage(`{"mode":"observe"}`), json.RawMessage(`{"mode":"deny"}`)} {
		if err := ValidateWAFPolicyOverlay(overlay); err != nil {
			t.Fatalf("overlay %s error = %v", overlay, err)
		}
	}
}

func TestValidateWAFPolicyOverlayRejectsUnknownModeAndExtraFields(t *testing.T) {
	t.Parallel()
	for _, overlay := range []json.RawMessage{
		json.RawMessage(`{}`),
		json.RawMessage(`{"mode":"block"}`),
		json.RawMessage(`{"mode":"deny","extra":true}`),
		json.RawMessage(`[]`),
		json.RawMessage(`not-json`),
	} {
		if err := ValidateWAFPolicyOverlay(overlay); err == nil {
			t.Fatalf("overlay %s was accepted", overlay)
		}
	}
}
