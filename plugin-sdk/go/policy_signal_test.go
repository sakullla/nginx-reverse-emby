package pluginsdk

import "testing"

func TestDecodePolicySecurityEventAcceptsOnlyBoundedRedactedShape(t *testing.T) {
	event, err := DecodePolicySecurityEvent([]byte(`{"rule_id":"waf.sql-1","action":"deny","summary":"sql injection signature matched"}`))
	if err != nil {
		t.Fatalf("DecodePolicySecurityEvent() error = %v", err)
	}
	if event.RuleID != "waf.sql-1" || event.Action != "deny" || event.Summary != "sql injection signature matched" {
		t.Fatalf("event = %+v", event)
	}

	for name, payload := range map[string]string{
		"unknown field":       `{"rule_id":"waf.sql-1","action":"deny","summary":"matched","body":"secret"}`,
		"trailing json":       `{"rule_id":"waf.sql-1","action":"deny","summary":"matched"}{}`,
		"sensitive summary":   `{"rule_id":"waf.sql-1","action":"deny","summary":"authorization token matched"}`,
		"noncanonical action": `{"rule_id":"waf.sql-1","action":"DENY","summary":"matched"}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodePolicySecurityEvent([]byte(payload)); err == nil {
				t.Fatal("DecodePolicySecurityEvent() accepted invalid payload")
			}
		})
	}
}
