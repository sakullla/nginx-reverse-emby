package hostapi

import (
	"context"
	"errors"
	"net/netip"
	"testing"

	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

type auditRecorder struct{ events []AuditEvent }

func (recorder *auditRecorder) Audit(_ context.Context, event AuditEvent) error {
	recorder.events = append(recorder.events, event)
	return nil
}

func TestHostAPIAuthorizationChecksDeclarationGrantActorGroupTargetQuotaAndAudit(t *testing.T) {
	capability := pluginsdk.CapabilityPolicyTrustedSource
	target := pluginsdk.HostTarget{Kind: "http.rule", ID: "rule-1", ResourceGroupID: "group-1"}
	call := pluginsdk.HostCapabilityCall{PluginID: "official.ip-policy", InstanceID: "instance-1", Generation: "generation-1", Capability: capability, Actor: pluginsdk.HostActor{ID: "official.ip-policy", ResourceGroupID: "group-1"}, Target: target, QuotaMetric: "host.call", QuotaUnits: 1}
	quota, _ := NewCallQuota(1)
	audit := &auditRecorder{}
	authorizer := Authorizer{PluginID: call.PluginID, InstanceID: call.InstanceID, Generation: call.Generation, Declared: []pluginsdk.HostCapability{capability}, Granted: []pluginsdk.HostCapability{capability}, Actor: call.Actor, ActorCapabilities: []pluginsdk.HostCapability{capability}, Targets: []pluginsdk.HostTarget{target}, Quota: quota, Auditor: audit}
	if err := authorizer.Authorize(t.Context(), call); err != nil {
		t.Fatal(err)
	}
	if err := authorizer.Authorize(t.Context(), call); !errors.Is(err, ErrDenied) {
		t.Fatalf("quota overflow error = %v", err)
	}
	if len(audit.events) != 2 || audit.events[0].Outcome != "allowed" || audit.events[1].Reason != "quota_denied" {
		t.Fatalf("audit events = %+v", audit.events)
	}
	for _, mutate := range []func(*Authorizer, *pluginsdk.HostCapabilityCall){
		func(authorizer *Authorizer, _ *pluginsdk.HostCapabilityCall) { authorizer.Declared = nil },
		func(authorizer *Authorizer, _ *pluginsdk.HostCapabilityCall) { authorizer.Granted = nil },
		func(_ *Authorizer, call *pluginsdk.HostCapabilityCall) { call.Generation = "generation-2" },
		func(_ *Authorizer, call *pluginsdk.HostCapabilityCall) { call.Actor.ID = "other-actor" },
		func(authorizer *Authorizer, _ *pluginsdk.HostCapabilityCall) { authorizer.ActorCapabilities = nil },
		func(_ *Authorizer, call *pluginsdk.HostCapabilityCall) { call.Actor.ResourceGroupID = "group-2" },
		func(_ *Authorizer, call *pluginsdk.HostCapabilityCall) { call.Target.ID = "rule-2" },
	} {
		candidateCall := call
		candidateQuota, _ := NewCallQuota(1)
		candidate := Authorizer{PluginID: call.PluginID, InstanceID: call.InstanceID, Generation: call.Generation, Declared: []pluginsdk.HostCapability{capability}, Granted: []pluginsdk.HostCapability{capability}, Actor: call.Actor, ActorCapabilities: []pluginsdk.HostCapability{capability}, Targets: []pluginsdk.HostTarget{target}, Quota: candidateQuota, Auditor: &auditRecorder{}}
		mutate(&candidate, &candidateCall)
		if err := candidate.Authorize(t.Context(), candidateCall); !errors.Is(err, ErrDenied) {
			t.Fatalf("negative authorization error = %v", err)
		}
	}
}

func TestPolicyAtomicStateIsBoundedAndLinearizable(t *testing.T) {
	state, err := NewAtomicState(AtomicStateLimits{TotalEntries: 2, NamespaceEntries: 2, TotalBytes: 16, NamespaceBytes: 16, KeyBytes: 8, ValueBytes: 8})
	if err != nil {
		t.Fatal(err)
	}
	if swapped, err := state.CompareAndSwap("instance", "count", nil, []byte("1")); err != nil || !swapped {
		t.Fatalf("initial CAS = (%v, %v)", swapped, err)
	}
	if swapped, err := state.CompareAndSwap("instance", "count", []byte("0"), []byte("2")); err != nil || swapped {
		t.Fatalf("stale CAS = (%v, %v)", swapped, err)
	}
	if value, ok := state.Get("instance", "count"); !ok || string(value) != "1" {
		t.Fatalf("state = (%q, %v)", value, ok)
	}
	if err := state.Put("instance", "oversize", []byte("123456789")); err == nil {
		t.Fatal("oversize atomic state value was accepted")
	}
}

func TestPolicyMonotonicClockAndTrustedSource(t *testing.T) {
	clock := NewMonotonicClock()
	previous := clock.NowNanoseconds()
	for index := 0; index < 100; index++ {
		current := clock.NowNanoseconds()
		if current < previous {
			t.Fatalf("clock moved backward: %d -> %d", previous, current)
		}
		previous = current
	}
	source := netip.MustParseAddrPort("198.51.100.1:443")
	peer := netip.MustParseAddrPort("10.0.0.1:1234")
	if _, err := NewTrustedSource(source, peer, "trusted-proxy", true); err != nil {
		t.Fatal(err)
	}
	if _, err := NewTrustedSource(source, peer, "trusted-proxy", false); err == nil {
		t.Fatal("unauthenticated source metadata was accepted")
	}
	if _, err := NewTrustedSource(source, peer, "forwarded-header", true); err == nil {
		t.Fatal("unowned source kind was accepted")
	}
}

func TestHostAPIRevocableHandleBindsOwnerAndFencesDrainAndTargetRotation(t *testing.T) {
	capability := pluginsdk.CapabilityServiceRevocableResourceHandle
	target := pluginsdk.HostTarget{Kind: "relay", ID: "relay-1", ResourceGroupID: "group-1"}
	call := pluginsdk.HostCapabilityCall{PluginID: "official.reverse", InstanceID: "instance-1", Generation: "generation-1", Capability: capability, Actor: pluginsdk.HostActor{ID: "official.reverse", ResourceGroupID: "group-1"}, Target: target, QuotaMetric: "host.calls", QuotaUnits: 1}
	quota, _ := NewCallQuota(16)
	authorizer := Authorizer{PluginID: call.PluginID, InstanceID: call.InstanceID, Generation: call.Generation, Declared: []pluginsdk.HostCapability{capability}, Granted: []pluginsdk.HostCapability{capability}, Actor: call.Actor, ActorCapabilities: []pluginsdk.HostCapability{capability}, Targets: []pluginsdk.HostTarget{target}, Quota: quota, Auditor: &auditRecorder{}}
	handles := NewResourceHandles()
	resource := &struct{ ID string }{ID: "core-relay"}
	token, err := handles.Issue(t.Context(), authorizer, call, resource)
	if err != nil {
		t.Fatal(err)
	}
	if resolved, err := handles.Resolve(t.Context(), token, call); err != nil || resolved != resource {
		t.Fatalf("Resolve() = (%v, %v)", resolved, err)
	}
	wrong := call
	wrong.Generation = "generation-2"
	if _, err := handles.Resolve(t.Context(), token, wrong); !errors.Is(err, ErrDenied) {
		t.Fatalf("cross-generation Resolve() error = %v", err)
	}
	handles.RevokeGeneration(call.InstanceID, call.Generation)
	if _, err := handles.Resolve(t.Context(), token, call); !errors.Is(err, ErrDenied) {
		t.Fatalf("Resolve(after drain) error = %v", err)
	}
	if _, err := handles.Issue(t.Context(), authorizer, call, resource); !errors.Is(err, ErrDenied) {
		t.Fatalf("Issue(after drain) error = %v", err)
	}

	call.Generation = "generation-2"
	authorizer.Generation = call.Generation
	handles = NewResourceHandles()
	token, err = handles.Issue(t.Context(), authorizer, call, resource)
	if err != nil {
		t.Fatal(err)
	}
	handles.RevokeTarget(target)
	if _, err := handles.Resolve(t.Context(), token, call); !errors.Is(err, ErrDenied) {
		t.Fatalf("Resolve(after target rotation) error = %v", err)
	}
}
