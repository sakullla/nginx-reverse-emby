package hostapi

import (
	"context"
	"errors"
	"testing"

	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

type contractAuditRecorder struct{ events []AuditEvent }

func (recorder *contractAuditRecorder) Audit(_ context.Context, event AuditEvent) error {
	recorder.events = append(recorder.events, event)
	return nil
}

func TestHostAPIAuthorizesDeclaredCapabilityAndAuditsQuotaFence(t *testing.T) {
	capability := pluginsdk.CapabilityPolicyTrustedSource
	target := pluginsdk.HostTarget{Kind: "http.rule", ID: "rule-1", ResourceGroupID: "group-1"}
	actor := pluginsdk.HostActor{ID: "official.ip-policy", ResourceGroupID: "group-1"}
	call := pluginsdk.HostCapabilityCall{
		PluginID: "official.ip-policy", InstanceID: "instance-1", Generation: "generation-1",
		Capability: capability, Actor: actor, Target: target, QuotaMetric: "host.call", QuotaUnits: 1,
	}
	quota, err := NewCallQuota(1)
	if err != nil {
		t.Fatal(err)
	}
	audit := &contractAuditRecorder{}
	authorizer := Authorizer{
		PluginID: call.PluginID, InstanceID: call.InstanceID, Generation: call.Generation,
		Declared: []pluginsdk.HostCapability{capability}, Granted: []pluginsdk.HostCapability{capability},
		Actor: actor, ActorCapabilities: []pluginsdk.HostCapability{capability},
		Targets: []pluginsdk.HostTarget{target}, Quota: quota, Auditor: audit,
	}
	if err := authorizer.Authorize(t.Context(), call); err != nil {
		t.Fatalf("Authorize() error = %v", err)
	}
	if err := authorizer.Authorize(t.Context(), call); !errors.Is(err, ErrDenied) {
		t.Fatalf("quota fence error = %v", err)
	}
	if len(audit.events) != 2 || audit.events[0].Outcome != "allowed" || audit.events[1].Reason != "quota_denied" {
		t.Fatalf("audit events = %+v", audit.events)
	}
}
