package pluginsdk

import (
	"reflect"
	"testing"
)

func TestPolicySecurityEventAcceptsOnlyFixedEnumCatalog(t *testing.T) {
	event, err := PolicySecurityEventFromWire(int32(PolicySecurityEventCodeWAFRuleMatch), int32(PolicySecurityEventActionDeny))
	if err != nil {
		t.Fatalf("PolicySecurityEventFromWire() error = %v", err)
	}
	if event.Code.String() != "waf.rule_match" || event.Action.String() != "deny" || event.Template() != "WAF rule matched" {
		t.Fatalf("event = %+v", event)
	}

	for name, test := range map[string]struct {
		code   int32
		action int32
	}{
		"unspecified code":   {0, int32(PolicySecurityEventActionDeny)},
		"unknown code":       {200, int32(PolicySecurityEventActionDeny)},
		"unspecified action": {int32(PolicySecurityEventCodeWAFRuleMatch), 0},
		"unknown action":     {int32(PolicySecurityEventCodeWAFRuleMatch), 200},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := PolicySecurityEventFromWire(test.code, test.action); err == nil {
				t.Fatal("PolicySecurityEventFromWire accepted value outside fixed catalog")
			}
		})
	}
}

func TestPolicySecurityEventHasNoFreeTextOrByteCarrier(t *testing.T) {
	typeOf := reflect.TypeOf(PolicySecurityEvent{})
	for index := 0; index < typeOf.NumField(); index++ {
		kind := typeOf.Field(index).Type.Kind()
		if kind == reflect.String || kind == reflect.Slice || kind == reflect.Array || kind == reflect.Map || kind == reflect.Interface {
			t.Fatalf("PolicySecurityEvent field %q can carry guest-controlled data: %s", typeOf.Field(index).Name, kind)
		}
	}
}

func TestPolicyStructuredSecurityEventsRoundtripAndCatalogBinding(t *testing.T) {
	events := []PolicySecurityEvent{
		{Code: PolicySecurityEventCodeWAFRuleMatch, Action: PolicySecurityEventActionDeny},
		{Code: PolicySecurityEventCodeIPRuleMatch, Action: PolicySecurityEventActionObserve, RuleIndex: 2, DatasetIndex: 1, ClassificationIndex: 31},
		{Code: PolicySecurityEventCodeIPCheckFailure, Action: PolicySecurityEventActionObserve, Reason: PolicySecurityEventReasonDatasetUnavailable},
		{Code: PolicySecurityEventCodeRoutingRuleMatch, Action: PolicySecurityEventActionUpstream, RuleIndex: 1, OutboundIndex: 2, DomainSource: PolicyDomainSourceTLSSNI},
		{Code: PolicySecurityEventCodeRoutingFailure, Action: PolicySecurityEventActionDeny, OutboundIndex: 2, Reason: PolicySecurityEventReasonUpstreamAuthentication},
	}
	for _, event := range events {
		if err := event.ValidateForCatalog(2, 1, 31, 2); err != nil {
			t.Fatal(err)
		}
		frame, err := MarshalPolicySecurityEvent(event, 4096)
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := UnmarshalPolicySecurityEvent(frame, 4096)
		if err != nil || decoded != event {
			t.Fatalf("event roundtrip: %+v %v", decoded, err)
		}
		if _, err := UnmarshalPolicySecurityEvent(appendVarintField(frame, 1, uint64(event.Code)), 4096); err == nil {
			t.Fatal("duplicate event code accepted")
		}
	}
	if events[1].ValidateForCatalog(1, 1, 31, 2) == nil {
		t.Fatal("event resolved nonexistent rule")
	}
	for _, event := range []PolicySecurityEvent{
		{Code: PolicySecurityEventCodeIPCheckFailure, Action: PolicySecurityEventActionAllow, Reason: PolicySecurityEventReasonBudgetExceeded},
		{Code: PolicySecurityEventCodeIPCheckFailure, Action: PolicySecurityEventActionObserve},
		{Code: PolicySecurityEventCodeRoutingFailure, Action: PolicySecurityEventActionDirect, Reason: PolicySecurityEventReasonUpstreamUnavailable},
		{Code: PolicySecurityEventCodeRoutingRuleMatch, Action: PolicySecurityEventActionUpstream},
		{Code: PolicySecurityEventCodeIPRuleMatch, Action: PolicySecurityEventActionDeny, ClassificationIndex: 1},
		{Code: PolicySecurityEventCodeWAFRuleMatch, Action: PolicySecurityEventActionDeny, Reason: PolicySecurityEventReasonDataInvalid},
	} {
		if event.Validate() == nil {
			t.Fatalf("ambiguous security event accepted: %+v", event)
		}
	}
}
