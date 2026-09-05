package pluginsdk

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
)

const (
	PolicyOverlaySchemaV1         = "nre.policy-overlay/v1"
	PolicyOverlayFormatEnvelopeV1 = "envelope-v1"
	PolicyOverlayFormatLegacyWAF  = "legacy-waf"
	PolicyOverlayFormatLegacyIP   = "legacy-ip"
	PolicyOverlayFormatLegacyRate = "legacy-rate"
	PolicyOverlayStageIP          = "ip"
	PolicyOverlayStageRate        = "rate"
	PolicyOverlayStageWAF         = "waf"
	PolicyOverlayMaxBytes         = 64 << 10
	PolicyStageOverlayMaxBytes    = 16 << 10
	PolicyOverlayMaxDepth         = 32
)

// PolicyStageOverlay binds opaque plugin-owned configuration to exactly one
// kind and policy identity. The SDK validates structure, not plugin rule
// semantics. Hosts must select by both fields before invoking a guest and must
// independently bind the envelope to the administrator's effective policy chain.
type PolicyStageOverlay struct {
	Kind     string          `json:"kind"`
	PolicyID string          `json:"policy_id"`
	Payload  json.RawMessage `json:"payload"`
}

// PolicyOverlayEnvelope isolates overlays in canonical IP -> rate -> WAF order,
// with at most one policy of each kind. An independent second policy of the same
// kind is a composition conflict, never implicitly merged or discarded. A stage
// absent from Stages inherits its existing instance defaults; no mode or allow
// decision is synthesized. WAF applicability to HTTP is enforced by the Host.
//
// These JSON bounds do not replace policy/v1's complete frame, execution time,
// or memory limits. Hosts must still validate the final selected payload inside
// the complete EvaluateRequest frame with the existing SDK frame-size helpers.
type PolicyOverlayEnvelope struct {
	Schema string               `json:"schema"`
	Stages []PolicyStageOverlay `json:"stages"`
}

func policyOverlayStageOrder(kind string) int {
	switch kind {
	case PolicyOverlayStageIP:
		return 1
	case PolicyOverlayStageRate:
		return 2
	case PolicyOverlayStageWAF:
		return 3
	default:
		return 0
	}
}

func (stage PolicyStageOverlay) Validate() error {
	if policyOverlayStageOrder(stage.Kind) == 0 {
		return errors.New("policy overlay stage kind is unsupported")
	}
	if ValidatePolicyIdentity(stage.PolicyID) != nil {
		return errors.New("policy overlay stage identity is invalid")
	}
	if err := validatePolicyOverlayJSON(stage.Payload, PolicyStageOverlayMaxBytes); err != nil {
		return err
	}
	if !policyOverlayJSONObject(stage.Payload) {
		return errors.New("policy stage overlay must be an object")
	}
	return nil
}

func (envelope PolicyOverlayEnvelope) Validate() error {
	if envelope.Schema != PolicyOverlaySchemaV1 || envelope.Stages == nil || len(envelope.Stages) > 3 {
		return errors.New("policy overlay envelope schema or stages are invalid")
	}
	previous := 0
	for _, stage := range envelope.Stages {
		if err := stage.Validate(); err != nil {
			return err
		}
		order := policyOverlayStageOrder(stage.Kind)
		if order <= previous {
			return errors.New("policy overlay stages are repeated or out of order")
		}
		previous = order
	}
	encoded, err := json.Marshal(envelope)
	if err != nil || len(encoded) > PolicyOverlayMaxBytes {
		return errors.New("policy overlay envelope exceeds its byte budget")
	}
	return validatePolicyOverlayJSON(encoded, PolicyOverlayMaxBytes)
}

// PolicyOverlayDecodeContext is selected from Host-owned stored-format metadata
// and the existing chain identity, never from untrusted overlay fields. Format
// is mandatory, including for new envelopes. LegacyPolicyID is mandatory only
// for legacy input and identifies its sole owning policy. The SDK never guesses
// a format based on fields such as mode, schema, or stages.
type PolicyOverlayDecodeContext struct {
	Format         string
	LegacyPolicyID string
}

// DecodePolicyOverlay explicitly decodes a new envelope or wraps an old overlay
// for its known owning stage. Empty/whitespace/null legacy overlays mean no
// override and preserve defaults. Legacy WAF accepts only observe/deny mode;
// legacy IP and rate object payloads are copied without rewriting their rules.
// Wrapping legacy IP does not resolve obsolete dataset references or incompatible
// failure policies: the plugin/Host migration must retain the old effective
// snapshot until those business-level issues are explicitly resolved.
func DecodePolicyOverlay(payload json.RawMessage, decodeContext PolicyOverlayDecodeContext) (PolicyOverlayEnvelope, error) {
	if decodeContext.Format == PolicyOverlayFormatEnvelopeV1 {
		if decodeContext.LegacyPolicyID != "" {
			return PolicyOverlayEnvelope{}, errors.New("new overlay envelope does not accept a legacy identity")
		}
		return DecodePolicyOverlayEnvelope(payload)
	}
	kind := ""
	switch decodeContext.Format {
	case PolicyOverlayFormatLegacyWAF:
		kind = PolicyOverlayStageWAF
	case PolicyOverlayFormatLegacyIP:
		kind = PolicyOverlayStageIP
	case PolicyOverlayFormatLegacyRate:
		kind = PolicyOverlayStageRate
	default:
		return PolicyOverlayEnvelope{}, errors.New("policy overlay format context is required and must be supported")
	}
	if ValidatePolicyIdentity(decodeContext.LegacyPolicyID) != nil {
		return PolicyOverlayEnvelope{}, errors.New("legacy overlay owner identity is required")
	}
	if len(payload) > PolicyStageOverlayMaxBytes {
		return PolicyOverlayEnvelope{}, errors.New("legacy policy overlay exceeds its byte budget")
	}
	envelope := PolicyOverlayEnvelope{Schema: PolicyOverlaySchemaV1, Stages: []PolicyStageOverlay{}}
	trimmed := bytes.TrimSpace(payload)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return envelope, nil
	}
	if err := validatePolicyOverlayJSON(payload, PolicyStageOverlayMaxBytes); err != nil {
		return PolicyOverlayEnvelope{}, err
	}
	if kind == PolicyOverlayStageWAF {
		fields, err := policyOverlayFields(payload, "mode")
		if err != nil || len(fields) != 1 {
			return PolicyOverlayEnvelope{}, errors.New("legacy WAF overlay must contain only mode")
		}
		var mode string
		if json.Unmarshal(fields["mode"], &mode) != nil || (mode != "observe" && mode != "deny") {
			return PolicyOverlayEnvelope{}, errors.New("legacy WAF overlay mode is invalid")
		}
	}
	envelope.Stages = append(envelope.Stages, PolicyStageOverlay{Kind: kind, PolicyID: decodeContext.LegacyPolicyID, Payload: append(json.RawMessage(nil), payload...)})
	if err := envelope.Validate(); err != nil {
		return PolicyOverlayEnvelope{}, err
	}
	return envelope, nil
}

// DecodePolicyOverlayEnvelope strictly decodes the versioned JSON contract.
// Duplicate JSON members are rejected even inside opaque payloads, preventing
// differing parser interpretations. Envelope/stage names are case-sensitive;
// unknown fields and trailing JSON are rejected rather than silently ignored.
func DecodePolicyOverlayEnvelope(payload json.RawMessage) (PolicyOverlayEnvelope, error) {
	if err := validatePolicyOverlayJSON(payload, PolicyOverlayMaxBytes); err != nil {
		return PolicyOverlayEnvelope{}, err
	}
	fields, err := policyOverlayFields(payload, "schema", "stages")
	if err != nil || len(fields) != 2 {
		return PolicyOverlayEnvelope{}, errors.New("policy overlay envelope requires schema and stages only")
	}
	var envelope PolicyOverlayEnvelope
	if json.Unmarshal(fields["schema"], &envelope.Schema) != nil {
		return PolicyOverlayEnvelope{}, errors.New("policy overlay schema is invalid")
	}
	var stages []json.RawMessage
	if json.Unmarshal(fields["stages"], &stages) != nil || stages == nil || len(stages) > 3 {
		return PolicyOverlayEnvelope{}, errors.New("policy overlay stages must be a bounded array")
	}
	envelope.Stages = make([]PolicyStageOverlay, 0, len(stages))
	for _, raw := range stages {
		fields, err := policyOverlayFields(raw, "kind", "policy_id", "payload")
		if err != nil || len(fields) != 3 {
			return PolicyOverlayEnvelope{}, errors.New("policy overlay stage requires kind, policy_id and payload only")
		}
		var stage PolicyStageOverlay
		if json.Unmarshal(fields["kind"], &stage.Kind) != nil || json.Unmarshal(fields["policy_id"], &stage.PolicyID) != nil {
			return PolicyOverlayEnvelope{}, errors.New("policy overlay stage identity or kind is invalid")
		}
		stage.Payload = append(json.RawMessage(nil), fields["payload"]...)
		envelope.Stages = append(envelope.Stages, stage)
	}
	if err := envelope.Validate(); err != nil {
		return PolicyOverlayEnvelope{}, err
	}
	return envelope, nil
}

// SelectPolicyStageOverlay returns a private copy of only the selected policy's
// payload. A missing kind returns nil (instance defaults); a kind owned by a
// different policy is an explicit conflict. Validation occurs before selection,
// so an invalid unselected stage cannot be concealed by selecting a valid one.
func SelectPolicyStageOverlay(envelope PolicyOverlayEnvelope, kind, policyID string) (json.RawMessage, error) {
	if policyOverlayStageOrder(kind) == 0 || ValidatePolicyIdentity(policyID) != nil {
		return nil, errors.New("policy overlay selector is invalid")
	}
	if err := envelope.Validate(); err != nil {
		return nil, err
	}
	for _, stage := range envelope.Stages {
		if stage.Kind != kind {
			continue
		}
		if stage.PolicyID != policyID {
			return nil, errors.New("policy overlay stage belongs to another policy")
		}
		return append(json.RawMessage(nil), stage.Payload...), nil
	}
	return nil, nil
}

func policyOverlayJSONObject(raw []byte) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) > 0 && trimmed[0] == '{'
}

func policyOverlayFields(raw []byte, allowed ...string) (map[string]json.RawMessage, error) {
	if !policyOverlayJSONObject(raw) {
		return nil, errors.New("policy overlay must be an object")
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return nil, errors.New("policy overlay object is invalid")
	}
	for key := range fields {
		known := false
		for _, field := range allowed {
			if key == field {
				known = true
				break
			}
		}
		if !known {
			return nil, errors.New("policy overlay contains an unknown field")
		}
	}
	return fields, nil
}

func validatePolicyOverlayJSON(raw []byte, limit int) error {
	if len(raw) == 0 || len(raw) > limit {
		return errors.New("policy overlay JSON is missing or exceeds its byte budget")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := scanPolicyOverlayJSON(decoder, 0); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return errors.New("policy overlay JSON contains trailing data")
	}
	return nil
}

func scanPolicyOverlayJSON(decoder *json.Decoder, depth int) error {
	if depth > PolicyOverlayMaxDepth {
		return errors.New("policy overlay JSON exceeds its depth budget")
	}
	token, err := decoder.Token()
	if err != nil {
		return errors.New("policy overlay JSON is invalid")
	}
	delimiter, nested := token.(json.Delim)
	if !nested {
		return nil
	}
	switch delimiter {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			token, err := decoder.Token()
			if err != nil {
				return errors.New("policy overlay JSON member is invalid")
			}
			key, ok := token.(string)
			if !ok {
				return errors.New("policy overlay JSON member is invalid")
			}
			if _, exists := seen[key]; exists {
				return errors.New("policy overlay JSON member is repeated")
			}
			seen[key] = struct{}{}
			if err := scanPolicyOverlayJSON(decoder, depth+1); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim('}') {
			return errors.New("policy overlay JSON object is invalid")
		}
	case '[':
		for decoder.More() {
			if err := scanPolicyOverlayJSON(decoder, depth+1); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim(']') {
			return errors.New("policy overlay JSON array is invalid")
		}
	default:
		return errors.New("policy overlay JSON delimiter is invalid")
	}
	return nil
}
