package pluginsdk

import "errors"

type PolicySecurityEventCode int32

const (
	PolicySecurityEventCodeUnspecified  PolicySecurityEventCode = 0
	PolicySecurityEventCodeWAFRuleMatch PolicySecurityEventCode = 1
)

type PolicySecurityEventAction int32

const (
	PolicySecurityEventActionUnspecified PolicySecurityEventAction = 0
	PolicySecurityEventActionObserve     PolicySecurityEventAction = 1
	PolicySecurityEventActionDeny        PolicySecurityEventAction = 2
)

// PolicySecurityEvent contains only fixed-catalog enum values. Its printable
// code, action, and template are all host-owned and cannot transport request
// fields, bodies, credentials, or arbitrary guest bytes.
type PolicySecurityEvent struct {
	Code   PolicySecurityEventCode
	Action PolicySecurityEventAction
}

func PolicySecurityEventFromWire(code, action int32) (PolicySecurityEvent, error) {
	event := PolicySecurityEvent{Code: PolicySecurityEventCode(code), Action: PolicySecurityEventAction(action)}
	if event.Code != PolicySecurityEventCodeWAFRuleMatch {
		return PolicySecurityEvent{}, errors.New("policy security event code is unknown")
	}
	if event.Action != PolicySecurityEventActionObserve && event.Action != PolicySecurityEventActionDeny {
		return PolicySecurityEvent{}, errors.New("policy security event action is unknown")
	}
	return event, nil
}

func (code PolicySecurityEventCode) String() string {
	if code == PolicySecurityEventCodeWAFRuleMatch {
		return "waf.rule_match"
	}
	return "unknown"
}

func (action PolicySecurityEventAction) String() string {
	switch action {
	case PolicySecurityEventActionObserve:
		return "observe"
	case PolicySecurityEventActionDeny:
		return "deny"
	default:
		return "unknown"
	}
}

func (event PolicySecurityEvent) Template() string {
	if event.Code == PolicySecurityEventCodeWAFRuleMatch {
		return "WAF rule matched"
	}
	return "Policy security event"
}
