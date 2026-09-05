package pluginsdk

import (
	"encoding/json"
	"errors"
)

// PolicyStageModeProjection is part of the trusted immutable Host generation.
// The Host selects its stage/entry from stored authority, not request headers,
// guest claims, opaque Config, or overlay payload fields. The surrounding Host
// snapshot supplies instance/generation identity and settings/config versions.
type PolicyStageModeProjection struct {
	Stage    PolicyStageIdentity `json:"stage"`
	Settings PolicyModeSettings  `json:"settings"`
}

func (projection PolicyStageModeProjection) Validate() error {
	if projection.Stage.Validate() != nil || projection.Settings.Validate() != nil {
		return errors.New("policy mode projection is invalid")
	}
	if projection.Settings.Handling == PolicyModeHandlingLegacyWAF && projection.Stage.Kind != PolicyOverlayStageWAF {
		return errors.New("legacy WAF bridge belongs to WAF only")
	}
	return nil
}

const (
	PolicyCheckAllow = "allow"
	PolicyCheckDeny  = "deny"
	PolicyCheckError = "error"
)

type PolicyCheckResult struct {
	Outcome string `json:"outcome"`
	Failure string `json:"failure,omitempty"`
}

func validPolicyCheckFailure(code string) bool {
	switch code {
	case "source-unavailable", "dataset-unavailable", "classification-missing", "budget-exceeded", "guest-failure", "invalid-result", "revoked", "migration-required", "composition-conflict", "apply-failed":
		return true
	}
	return false
}
func (result PolicyCheckResult) Validate() error {
	switch result.Outcome {
	case PolicyCheckAllow, PolicyCheckDeny:
		if result.Failure != "" {
			return errors.New("completed check contains a failure")
		}
	case PolicyCheckError:
		if !validPolicyCheckFailure(result.Failure) {
			return errors.New("failed check requires a fixed failure code")
		}
	default:
		return errors.New("policy check outcome is invalid")
	}
	return nil
}

type PolicyModeDecision struct {
	Denied      bool   `json:"denied"`
	Checked     bool   `json:"checked"`
	WouldDeny   bool   `json:"would_deny"`
	CheckFailed bool   `json:"check_failed"`
	Failure     string `json:"failure,omitempty"`
}

// ApplyPolicyMode translates only this stage. priorDenied is a Host-owned
// accumulator including global denials and earlier enforce stages. Observe can
// never clear it. A failed observe check remains unchecked/check_failed, emits
// its fixed failure event, and continues the remaining stages when not already
// denied; it is never converted into a successful guest allow. Enforce errors
// deny regardless of the old static fail-open setting. Legacy callers must use
// their unchanged legacy path, so old failure policies are not reinterpreted.
func ApplyPolicyMode(projection PolicyStageModeProjection, result PolicyCheckResult, priorDenied bool) (PolicyModeDecision, error) {
	if projection.Validate() != nil || result.Validate() != nil {
		return PolicyModeDecision{}, errors.New("policy mode decision input is invalid")
	}
	mode, err := ResolvePolicyMode(projection.Settings)
	if err != nil || mode == "" {
		return PolicyModeDecision{}, errors.New("legacy policy mode requires the legacy execution path")
	}
	decision := PolicyModeDecision{Denied: priorDenied, Checked: result.Outcome != PolicyCheckError}
	switch result.Outcome {
	case PolicyCheckDeny:
		if mode == PolicyModeEnforce {
			decision.Denied = true
		} else {
			decision.WouldDeny = true
		}
	case PolicyCheckError:
		decision.CheckFailed, decision.Failure = true, result.Failure
		if mode == PolicyModeEnforce {
			decision.Denied = true
		}
	}
	return decision, nil
}

// PolicyOverlayForMode preserves the existing envelope contract and stage
// isolation. Raw-decision guests receive only their opaque rule overlay and
// MUST NOT turn denies into allows based on private mode fields. The explicit
// legacy-WAF bridge always requests the publicly defined deny-mode overlay,
// including for typed observe. Otherwise the old guest returns Observe rather
// than a raw deny hit and Host could lose would-deny evidence. ApplyPolicyMode
// alone turns that raw deny into observation. Other legacy payloads/configs
// are not guessed or rewritten; absent typed settings keep their old behavior.
func PolicyOverlayForMode(envelope PolicyOverlayEnvelope, projection PolicyStageModeProjection) (json.RawMessage, error) {
	if err := projection.Validate(); err != nil {
		return nil, err
	}
	payload, err := SelectPolicyStageOverlay(envelope, projection.Stage.Kind, projection.Stage.PolicyID)
	if err != nil {
		return nil, err
	}
	if projection.Settings.Handling != PolicyModeHandlingLegacyWAF {
		return payload, nil
	}
	if _, err := DecodePolicyOverlay(payload, PolicyOverlayDecodeContext{Format: PolicyOverlayFormatLegacyWAF, LegacyPolicyID: projection.Stage.PolicyID}); err != nil {
		return nil, err
	}
	return json.Marshal(struct {
		Mode string `json:"mode"`
	}{Mode: "deny"})
}
