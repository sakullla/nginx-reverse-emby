package pluginhost

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

type blockingCapabilityQuota struct {
	started chan struct{}
	release chan struct{}
}

func (quota *blockingCapabilityQuota) ConsumeHostCapability(ctx context.Context, _ pluginsdk.HostCapabilityCall) error {
	close(quota.started)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-quota.release:
		return nil
	}
}

type capabilityQuotaStub struct {
	err   error
	calls int
}

func (quota *capabilityQuotaStub) ConsumeHostCapability(context.Context, pluginsdk.HostCapabilityCall) error {
	quota.calls++
	return quota.err
}

type capabilityAuditStub struct {
	err    error
	events []CapabilityAudit
}

func (audit *capabilityAuditStub) AuditHostCapability(_ context.Context, event CapabilityAudit) error {
	audit.events = append(audit.events, event)
	return audit.err
}

func TestPluginCapabilityAuthorizationMatrix(t *testing.T) {
	call, policy, quota, audit := testCapabilityPolicy()
	if err := policy.Authorize(t.Context(), call); err != nil {
		t.Fatal(err)
	}
	if quota.calls != 1 || len(audit.events) != 1 || audit.events[0].Outcome != "allowed" {
		t.Fatalf("allowed call side effects = quota:%d audit:%+v", quota.calls, audit.events)
	}
	for _, test := range []struct {
		name   string
		mutate func(*pluginsdk.HostCapabilityCall, *CapabilityPolicy)
		reason string
	}{
		{name: "signed declaration", mutate: func(_ *pluginsdk.HostCapabilityCall, policy *CapabilityPolicy) { policy.Declared = nil }, reason: "not_declared"},
		{name: "admin grant", mutate: func(_ *pluginsdk.HostCapabilityCall, policy *CapabilityPolicy) { policy.Granted = nil }, reason: "not_granted"},
		{name: "generation owner", mutate: func(call *pluginsdk.HostCapabilityCall, _ *CapabilityPolicy) { call.Generation = "generation-2" }, reason: "owner_denied"},
		{name: "actor", mutate: func(_ *pluginsdk.HostCapabilityCall, policy *CapabilityPolicy) { policy.ActorCapabilities = nil }, reason: "actor_denied"},
		{name: "actor identity", mutate: func(call *pluginsdk.HostCapabilityCall, _ *CapabilityPolicy) { call.Actor.ID = "operator-2" }, reason: "actor_denied"},
		{name: "resource group", mutate: func(call *pluginsdk.HostCapabilityCall, _ *CapabilityPolicy) {
			call.Actor.ResourceGroupID = "other-group"
		}, reason: "invalid_call"},
		{name: "target", mutate: func(call *pluginsdk.HostCapabilityCall, _ *CapabilityPolicy) { call.Target.ID = "other-target" }, reason: "target_denied"},
		{name: "quota", mutate: func(_ *pluginsdk.HostCapabilityCall, policy *CapabilityPolicy) {
			policy.Quota = &capabilityQuotaStub{err: errors.New("quota exceeded token=must-not-leak")}
		}, reason: "quota_denied"},
	} {
		t.Run(test.name, func(t *testing.T) {
			candidateCall, candidatePolicy, _, candidateAudit := testCapabilityPolicy()
			test.mutate(&candidateCall, &candidatePolicy)
			err := candidatePolicy.Authorize(t.Context(), candidateCall)
			if !errors.Is(err, ErrCapabilityDenied) || !strings.Contains(err.Error(), test.reason) {
				t.Fatalf("Authorize() error = %v", err)
			}
			if len(candidateAudit.events) != 1 || candidateAudit.events[0].Outcome != "denied" || candidateAudit.events[0].Reason != test.reason {
				t.Fatalf("denial audit = %+v", candidateAudit.events)
			}
			if strings.Contains(candidateAudit.events[0].Reason, "token") {
				t.Fatalf("denial audit leaked provider error: %+v", candidateAudit.events[0])
			}
		})
	}
}

func TestPluginCapabilityRevocableResourceHandleLifecycle(t *testing.T) {
	call, policy, _, _ := testCapabilityPolicy()
	broker := NewResourceHandleBroker()
	resource := &struct{ name string }{name: "secret broker"}
	token, err := broker.Issue(t.Context(), policy, call, resource)
	if err != nil || token == "" || strings.Contains(token, resource.name) {
		t.Fatalf("Issue() = (%q, %v)", token, err)
	}
	resolved, err := broker.Resolve(t.Context(), token, call)
	if err != nil || resolved != resource {
		t.Fatalf("Resolve() = (%v, %v)", resolved, err)
	}
	wrong := call
	wrong.Generation = "generation-2"
	if _, err := broker.Resolve(t.Context(), token, wrong); !errors.Is(err, ErrCapabilityDenied) {
		t.Fatalf("cross-generation Resolve() error = %v", err)
	}
	broker.RevokeGeneration(call.InstanceID, call.Generation)
	if _, err := broker.Resolve(t.Context(), token, call); !errors.Is(err, ErrCapabilityDenied) {
		t.Fatalf("Resolve(after generation drain) error = %v", err)
	}
	if _, err = broker.Issue(t.Context(), policy, call, resource); !errors.Is(err, ErrCapabilityDenied) {
		t.Fatalf("Issue(after generation drain) error = %v", err)
	}
	broker = NewResourceHandleBroker()
	token, err = broker.Issue(t.Context(), policy, call, resource)
	if err != nil {
		t.Fatal(err)
	}
	broker.RevokeTarget(call.Target)
	if _, err := broker.Resolve(t.Context(), token, call); !errors.Is(err, ErrCapabilityDenied) {
		t.Fatalf("Resolve(after target delete/rotation) error = %v", err)
	}
	if _, err := broker.Issue(t.Context(), policy, call, resource); !errors.Is(err, ErrCapabilityDenied) {
		t.Fatalf("Issue(after target delete/rotation) error = %v", err)
	}
}

func TestPluginCapabilityHandleRevokeCancelsLeaseWithoutWaitingForQuota(t *testing.T) {
	call, policy, _, _ := testCapabilityPolicy()
	quota := &blockingCapabilityQuota{started: make(chan struct{}), release: make(chan struct{})}
	broker := NewResourceHandleBroker()
	token, err := broker.Issue(t.Context(), policy, call, "resource")
	if err != nil {
		t.Fatal(err)
	}
	broker.handles[token].policy.Quota = quota
	done := make(chan error, 1)
	go func() {
		_, resolveErr := broker.Resolve(context.Background(), token, call)
		done <- resolveErr
	}()
	<-quota.started
	revoked := make(chan struct{})
	go func() { broker.RevokeGeneration(call.InstanceID, call.Generation); close(revoked) }()
	select {
	case <-revoked:
	case <-time.After(time.Second):
		t.Fatal("RevokeGeneration waited for external quota owner")
	}
	select {
	case err := <-done:
		if !errors.Is(err, ErrCapabilityDenied) {
			t.Fatalf("Resolve() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled handle lease did not return")
	}
}

func TestPluginCapabilityDynamicActionsAreHostDeclaredAndAuthorized(t *testing.T) {
	call, policy, _, _ := testCapabilityPolicy()
	policy.Declared = append(policy.Declared, pluginsdk.CapabilityUIDynamicActions)
	policy.Granted = append(policy.Granted, pluginsdk.CapabilityUIDynamicActions)
	policy.ActorCapabilities = append(policy.ActorCapabilities, pluginsdk.CapabilityUIDynamicActions)
	action := pluginsdk.DynamicAction{ID: "rotate", Label: "Rotate", Capability: pluginsdk.CapabilityServiceRevocableResourceHandle, TargetKind: call.Target.Kind, Confirm: "Rotate this resource?"}
	policy.DynamicActions = []pluginsdk.DynamicAction{action}
	registry := NewDynamicActionRegistry()
	called := false
	if err := registry.Register(action, call, policy, func(context.Context, pluginsdk.HostCapabilityCall) error { called = true; return nil }); err != nil {
		t.Fatal(err)
	}
	forged := action
	forged.Label = "Guest supplied label"
	if err := registry.Register(forged, call, policy, func(context.Context, pluginsdk.HostCapabilityCall) error { return nil }); err == nil {
		t.Fatal("runtime action differing from signed UI projection was accepted")
	}
	if err := registry.Invoke(t.Context(), call.InstanceID, call.Generation, action.ID, call.Actor); err != nil || !called {
		t.Fatalf("Invoke() called=%v error=%v", called, err)
	}
	registry.RevokeGeneration(call.InstanceID, call.Generation)
	if err := registry.Invoke(t.Context(), call.InstanceID, call.Generation, action.ID, call.Actor); !errors.Is(err, ErrCapabilityDenied) {
		t.Fatalf("Invoke(after drain) error = %v", err)
	}
	if err := registry.Register(action, call, policy, func(context.Context, pluginsdk.HostCapabilityCall) error { return nil }); err == nil {
		t.Fatal("Register(after drain) unexpectedly succeeded")
	}
	policy.Granted = []pluginsdk.HostCapability{pluginsdk.CapabilityServiceRevocableResourceHandle}
	registry = NewDynamicActionRegistry()
	if err := registry.Register(action, call, policy, func(context.Context, pluginsdk.HostCapabilityCall) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if err := registry.Invoke(t.Context(), call.InstanceID, call.Generation, action.ID, call.Actor); !errors.Is(err, ErrCapabilityDenied) {
		t.Fatalf("Invoke(without ui grant) error = %v", err)
	}
}

func TestPluginCapabilityDynamicActionCanRevokeItselfWithoutDeadlock(t *testing.T) {
	call, policy, _, _ := testCapabilityPolicy()
	policy.Declared = append(policy.Declared, pluginsdk.CapabilityUIDynamicActions)
	policy.Granted = append(policy.Granted, pluginsdk.CapabilityUIDynamicActions)
	policy.ActorCapabilities = append(policy.ActorCapabilities, pluginsdk.CapabilityUIDynamicActions)
	action := pluginsdk.DynamicAction{ID: "rotate", Label: "Rotate", Capability: pluginsdk.CapabilityServiceRevocableResourceHandle, TargetKind: call.Target.Kind}
	policy.DynamicActions = []pluginsdk.DynamicAction{action}
	registry := NewDynamicActionRegistry()
	if err := registry.Register(action, call, policy, func(context.Context, pluginsdk.HostCapabilityCall) error {
		registry.RevokeGeneration(call.InstanceID, call.Generation)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		done <- registry.Invoke(context.Background(), call.InstanceID, call.Generation, action.ID, call.Actor)
	}()
	select {
	case err := <-done:
		if !errors.Is(err, ErrCapabilityDenied) {
			t.Fatalf("Invoke() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("self-revoking action deadlocked")
	}
	if err := registry.Invoke(t.Context(), call.InstanceID, call.Generation, action.ID, call.Actor); !errors.Is(err, ErrCapabilityDenied) {
		t.Fatalf("Invoke(after self revoke) error = %v", err)
	}
}

func testCapabilityPolicy() (pluginsdk.HostCapabilityCall, CapabilityPolicy, *capabilityQuotaStub, *capabilityAuditStub) {
	capability := pluginsdk.CapabilityServiceRevocableResourceHandle
	target := pluginsdk.HostTarget{Kind: "secret", ID: "secret-1", ResourceGroupID: "group-1"}
	call := pluginsdk.HostCapabilityCall{
		PluginID: "official.cloudflare", InstanceID: "instance-1", Generation: "generation-1", Capability: capability,
		Actor: pluginsdk.HostActor{ID: "operator-1", ResourceGroupID: "group-1"}, Target: target,
		QuotaMetric: "host.call", QuotaUnits: 1,
	}
	quota := &capabilityQuotaStub{}
	audit := &capabilityAuditStub{}
	policy := CapabilityPolicy{PluginID: call.PluginID, InstanceID: call.InstanceID, Generation: call.Generation, Declared: []pluginsdk.HostCapability{capability}, Granted: []pluginsdk.HostCapability{capability}, Actor: call.Actor, ActorCapabilities: []pluginsdk.HostCapability{capability}, Targets: []pluginsdk.HostTarget{target}, Quota: quota, Auditor: audit}
	return call, policy, quota, audit
}
