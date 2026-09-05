package pluginsdk

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestPolicyOverlayEnvelopeRoundTripAndStageIsolation(t *testing.T) {
	raw := json.RawMessage(`{"schema":"nre.policy-overlay/v1","stages":[{"kind":"ip","policy_id":"ip-instance","payload":{"deny":["192.0.2.1"],"plugin_option":{"opaque":true}}},{"kind":"rate","policy_id":"rate-instance","payload":{"limit":100}},{"kind":"waf","policy_id":"waf-instance","payload":{"mode":"observe"}}]}`)
	envelope, err := DecodePolicyOverlay(raw, PolicyOverlayDecodeContext{Format: PolicyOverlayFormatEnvelopeV1})
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range []struct{ kind, id, expected string }{
		{"ip", "ip-instance", `{"deny":["192.0.2.1"],"plugin_option":{"opaque":true}}`},
		{"rate", "rate-instance", `{"limit":100}`},
		{"waf", "waf-instance", `{"mode":"observe"}`},
	} {
		selected, err := SelectPolicyStageOverlay(envelope, entry.kind, entry.id)
		if err != nil || string(selected) != entry.expected {
			t.Fatalf("selected %s = %s, %v", entry.kind, selected, err)
		}
		selected[0] = '['
		again, err := SelectPolicyStageOverlay(envelope, entry.kind, entry.id)
		if err != nil || string(again) != entry.expected {
			t.Fatal("selected payload aliases envelope storage")
		}
	}
	encoded, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodePolicyOverlayEnvelope(encoded); err != nil {
		t.Fatal(err)
	}
	if _, err := SelectPolicyStageOverlay(envelope, "ip", "different-instance"); err == nil {
		t.Fatal("same-kind wrong policy selected")
	}
	for _, kind := range []string{"", "IP", "unknown"} {
		if _, err := SelectPolicyStageOverlay(envelope, kind, "ip-instance"); err == nil {
			t.Fatal("unknown stage selected")
		}
	}
}

func TestPolicyOverlayMissingStagePreservesDefaults(t *testing.T) {
	for _, raw := range []string{
		`{"schema":"nre.policy-overlay/v1","stages":[]}`,
		`{"schema":"nre.policy-overlay/v1","stages":[{"kind":"waf","policy_id":"waf-instance","payload":{"mode":"observe"}}]}`,
	} {
		envelope, err := DecodePolicyOverlayEnvelope(json.RawMessage(raw))
		if err != nil {
			t.Fatal(err)
		}
		selected, err := SelectPolicyStageOverlay(envelope, "ip", "ip-instance")
		if err != nil || selected != nil {
			t.Fatalf("missing IP stage must inherit defaults: %s %v", selected, err)
		}
	}
}

func TestPolicyOverlayStrictEnvelopeRejection(t *testing.T) {
	for name, raw := range map[string]string{
		"empty":                         ``,
		"null":                          `null`,
		"array":                         `[]`,
		"unversioned":                   `{"mode":"observe"}`,
		"wrong schema":                  `{"schema":"unknown","stages":[]}`,
		"missing stages":                `{"schema":"nre.policy-overlay/v1"}`,
		"null stages":                   `{"schema":"nre.policy-overlay/v1","stages":null}`,
		"wrong stages":                  `{"schema":"nre.policy-overlay/v1","stages":{}}`,
		"unknown field":                 `{"schema":"nre.policy-overlay/v1","stages":[],"mode":"observe"}`,
		"noncanonical casing":           `{"Schema":"nre.policy-overlay/v1","stages":[]}`,
		"repeated root":                 `{"schema":"nre.policy-overlay/v1","stages":[],"stages":[]}`,
		"repeated escaped root":         `{"schema":"nre.policy-overlay/v1","stages":[],"\u0073tages":[]}`,
		"trailing":                      `{"schema":"nre.policy-overlay/v1","stages":[]} {}`,
		"trailing garbage":              `{"schema":"nre.policy-overlay/v1","stages":[]} x`,
		"unknown stage":                 `{"schema":"nre.policy-overlay/v1","stages":[{"kind":"dns","policy_id":"p","payload":{}}]}`,
		"unknown stage field":           `{"schema":"nre.policy-overlay/v1","stages":[{"kind":"ip","policy_id":"p","payload":{},"mode":"observe"}]}`,
		"stage casing":                  `{"schema":"nre.policy-overlay/v1","stages":[{"Kind":"ip","policy_id":"p","payload":{}}]}`,
		"stage null":                    `{"schema":"nre.policy-overlay/v1","stages":[null]}`,
		"identity missing":              `{"schema":"nre.policy-overlay/v1","stages":[{"kind":"ip","payload":{}}]}`,
		"identity empty":                `{"schema":"nre.policy-overlay/v1","stages":[{"kind":"ip","policy_id":"","payload":{}}]}`,
		"identity wrong type":           `{"schema":"nre.policy-overlay/v1","stages":[{"kind":"ip","policy_id":12,"payload":{}}]}`,
		"payload missing":               `{"schema":"nre.policy-overlay/v1","stages":[{"kind":"ip","policy_id":"p"}]}`,
		"payload null":                  `{"schema":"nre.policy-overlay/v1","stages":[{"kind":"ip","policy_id":"p","payload":null}]}`,
		"payload array":                 `{"schema":"nre.policy-overlay/v1","stages":[{"kind":"ip","policy_id":"p","payload":[]}]}`,
		"payload scalar":                `{"schema":"nre.policy-overlay/v1","stages":[{"kind":"ip","policy_id":"p","payload":true}]}`,
		"repeated stage field":          `{"schema":"nre.policy-overlay/v1","stages":[{"kind":"waf","kind":"ip","policy_id":"p","payload":{}}]}`,
		"repeated payload field":        `{"schema":"nre.policy-overlay/v1","stages":[{"kind":"waf","policy_id":"p","payload":{"mode":"deny","mode":"observe"}}]}`,
		"repeated nested payload field": `{"schema":"nre.policy-overlay/v1","stages":[{"kind":"ip","policy_id":"p","payload":{"rules":[{"deny":true,"deny":false}]}}]}`,
		"same stage twice":              `{"schema":"nre.policy-overlay/v1","stages":[{"kind":"ip","policy_id":"p","payload":{}},{"kind":"ip","policy_id":"p","payload":{}}]}`,
		"independent IP conflict":       `{"schema":"nre.policy-overlay/v1","stages":[{"kind":"ip","policy_id":"p","payload":{}},{"kind":"ip","policy_id":"q","payload":{}}]}`,
		"wrong order":                   `{"schema":"nre.policy-overlay/v1","stages":[{"kind":"waf","policy_id":"w","payload":{}},{"kind":"ip","policy_id":"i","payload":{}}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodePolicyOverlayEnvelope(json.RawMessage(raw)); err == nil {
				t.Fatal("invalid envelope accepted")
			}
		})
	}
}

func TestPolicyOverlayBudgetsApplyBeforeSelection(t *testing.T) {
	exactPayload := json.RawMessage(`{"value":"` + strings.Repeat("x", PolicyStageOverlayMaxBytes-len(`{"value":""}`)) + `"}`)
	envelope := PolicyOverlayEnvelope{Schema: PolicyOverlaySchemaV1, Stages: []PolicyStageOverlay{{Kind: "ip", PolicyID: "i", Payload: exactPayload}}}
	if err := envelope.Validate(); err != nil {
		t.Fatal("exact stage budget rejected", err)
	}
	envelope.Stages[0].Payload = append(exactPayload, ' ')
	if _, err := SelectPolicyStageOverlay(envelope, "waf", "w"); err == nil {
		t.Fatal("oversized unselected stage concealed")
	}
	raw := []byte(`{"schema":"nre.policy-overlay/v1","stages":[]}`)
	raw = append(raw, bytes.Repeat([]byte(" "), PolicyOverlayMaxBytes-len(raw))...)
	if _, err := DecodePolicyOverlayEnvelope(raw); err != nil {
		t.Fatal("exact complete byte budget rejected", err)
	}
	if _, err := DecodePolicyOverlayEnvelope(append(raw, ' ')); err == nil {
		t.Fatal("oversized envelope accepted")
	}
	deep := json.RawMessage(`{"x":` + strings.Repeat(`[`, PolicyOverlayMaxDepth+1) + `0` + strings.Repeat(`]`, PolicyOverlayMaxDepth+1) + `}`)
	envelope.Stages[0].Payload = deep
	if envelope.Validate() == nil {
		t.Fatal("unbounded depth accepted")
	}
	if _, err := DecodePolicyOverlay(append(exactPayload, ' '), PolicyOverlayDecodeContext{Format: PolicyOverlayFormatLegacyIP, LegacyPolicyID: "i"}); err == nil {
		t.Fatal("oversized legacy payload accepted")
	}
}

func TestPolicyOverlayLegacyWAFMigrationIsExplicitAndIsolated(t *testing.T) {
	for _, mode := range []string{"observe", "deny"} {
		raw := json.RawMessage(`{"mode":"` + mode + `"}`)
		if _, err := DecodePolicyOverlay(raw, PolicyOverlayDecodeContext{}); err == nil {
			t.Fatal("legacy format was guessed")
		}
		if _, err := DecodePolicyOverlay(raw, PolicyOverlayDecodeContext{Format: PolicyOverlayFormatEnvelopeV1}); err == nil {
			t.Fatal("legacy WAF guessed as envelope")
		}
		envelope, err := DecodePolicyOverlay(raw, PolicyOverlayDecodeContext{Format: PolicyOverlayFormatLegacyWAF, LegacyPolicyID: "waf-instance"})
		if err != nil {
			t.Fatal(err)
		}
		for _, kind := range []string{"ip", "rate"} {
			selected, err := SelectPolicyStageOverlay(envelope, kind, kind+"-instance")
			if err != nil || selected != nil {
				t.Fatal("legacy WAF mode leaked into another stage")
			}
		}
		selected, err := SelectPolicyStageOverlay(envelope, "waf", "waf-instance")
		if err != nil || !bytes.Equal(selected, raw) {
			t.Fatal("legacy WAF mode was lost or rewritten")
		}
	}
	for _, raw := range []string{`{}`, `{"mode":"block"}`, `{"mode":"observe","extra":1}`, `{"mode":"deny","mode":"observe"}`, `{"mode":null}`, `{"Mode":"observe"}`} {
		if _, err := DecodePolicyOverlay(json.RawMessage(raw), PolicyOverlayDecodeContext{Format: PolicyOverlayFormatLegacyWAF, LegacyPolicyID: "w"}); err == nil {
			t.Fatal("invalid legacy WAF accepted")
		}
	}
}

func TestPolicyOverlayLegacyIPAndRatePreserveOpaqueBusinessConfig(t *testing.T) {
	for _, entry := range []struct{ format, kind, raw string }{
		{PolicyOverlayFormatLegacyIP, "ip", ` {"schema":"plugin-config-v3","mmdb_handle":"old-source","failure_policy":"allow","rules":[{"deny":"192.0.2.0/24"}]} `},
		{PolicyOverlayFormatLegacyRate, "rate", `{"mode":"custom-plugin-mode","rate":7,"stages":{"business_value":true}}`},
	} {
		raw := json.RawMessage(entry.raw)
		envelope, err := DecodePolicyOverlay(raw, PolicyOverlayDecodeContext{Format: entry.format, LegacyPolicyID: "legacy-instance"})
		if err != nil {
			t.Fatal(err)
		}
		selected, err := SelectPolicyStageOverlay(envelope, entry.kind, "legacy-instance")
		if err != nil || !bytes.Equal(selected, raw) {
			t.Fatal("opaque legacy business config was rewritten")
		}
		raw[0] = '!'
		if !bytes.Equal(envelope.Stages[0].Payload, []byte(entry.raw)) {
			t.Fatal("migration retained caller backing storage")
		}
		if selected, err := SelectPolicyStageOverlay(envelope, "waf", "waf-instance"); err != nil || selected != nil {
			t.Fatal("legacy business config leaked into WAF")
		}
	}
}

func TestPolicyOverlayLegacyAbsenceAndContextErrors(t *testing.T) {
	for _, format := range []string{PolicyOverlayFormatLegacyIP, PolicyOverlayFormatLegacyRate, PolicyOverlayFormatLegacyWAF} {
		for _, raw := range []string{"", "  ", "null", " \nnull "} {
			envelope, err := DecodePolicyOverlay(json.RawMessage(raw), PolicyOverlayDecodeContext{Format: format, LegacyPolicyID: "old"})
			if err != nil || len(envelope.Stages) != 0 {
				t.Fatalf("legacy absence did not preserve defaults: %+v %v", envelope, err)
			}
		}
		if _, err := DecodePolicyOverlay(json.RawMessage(`{}`), PolicyOverlayDecodeContext{Format: format}); err == nil {
			t.Fatal("legacy owner was guessed")
		}
		for _, raw := range []string{`null {}`, `{} {}`, `{"x":1,"x":2}`, `[]`, `false`} {
			if _, err := DecodePolicyOverlay(json.RawMessage(raw), PolicyOverlayDecodeContext{Format: format, LegacyPolicyID: "old"}); err == nil {
				t.Fatal("ambiguous legacy input accepted")
			}
		}
	}
	if _, err := DecodePolicyOverlay(json.RawMessage(`{"schema":"nre.policy-overlay/v1","stages":[]}`), PolicyOverlayDecodeContext{Format: PolicyOverlayFormatEnvelopeV1, LegacyPolicyID: "old"}); err == nil {
		t.Fatal("mixed context accepted")
	}
}
