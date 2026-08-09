package pluginsdk

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

const (
	PolicySecurityEventMaxBytes   = 1024
	PolicySecurityRuleMaxBytes    = 96
	PolicySecurityActionMaxBytes  = 32
	PolicySecuritySummaryMaxBytes = 256
)

// PolicySecurityEvent is the bounded, redacted payload accepted by
// PolicyHost.EmitEvent. It deliberately cannot carry request bodies, headers,
// credentials, or arbitrary structured fields.
type PolicySecurityEvent struct {
	RuleID  string `json:"rule_id"`
	Action  string `json:"action"`
	Summary string `json:"summary"`
}

func DecodePolicySecurityEvent(payload []byte) (PolicySecurityEvent, error) {
	if len(payload) == 0 || len(payload) > PolicySecurityEventMaxBytes {
		return PolicySecurityEvent{}, fmt.Errorf("policy security event payload must contain 1..%d bytes", PolicySecurityEventMaxBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var event PolicySecurityEvent
	if err := decoder.Decode(&event); err != nil {
		return PolicySecurityEvent{}, fmt.Errorf("decode policy security event: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return PolicySecurityEvent{}, errors.New("policy security event contains trailing JSON")
	}
	event.RuleID = strings.TrimSpace(event.RuleID)
	event.Action = strings.TrimSpace(event.Action)
	event.Summary = strings.TrimSpace(event.Summary)
	if !canonicalPolicySignal(event.RuleID, PolicySecurityRuleMaxBytes) {
		return PolicySecurityEvent{}, errors.New("policy security event rule_id is not canonical")
	}
	if !canonicalPolicySignal(event.Action, PolicySecurityActionMaxBytes) {
		return PolicySecurityEvent{}, errors.New("policy security event action is not canonical")
	}
	if event.Summary == "" || len(event.Summary) > PolicySecuritySummaryMaxBytes || !utf8.ValidString(event.Summary) || strings.ContainsAny(event.Summary, "\r\n\x00") {
		return PolicySecurityEvent{}, errors.New("policy security event summary is not canonical")
	}
	lowerSummary := strings.ToLower(event.Summary)
	for _, sensitive := range []string{"authorization", "cookie", "password", "secret", "token"} {
		if strings.Contains(lowerSummary, sensitive) {
			return PolicySecurityEvent{}, errors.New("policy security event summary contains sensitive material")
		}
	}
	return event, nil
}

func canonicalPolicySignal(value string, limit int) bool {
	if value == "" || len(value) > limit {
		return false
	}
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') || char == '.' || char == '-' || char == '_' {
			continue
		}
		return false
	}
	return true
}
