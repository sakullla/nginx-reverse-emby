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
