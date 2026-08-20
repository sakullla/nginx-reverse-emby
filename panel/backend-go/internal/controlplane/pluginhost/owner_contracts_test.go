//go:build !integration

package pluginhost

import (
	"context"
	"errors"
	"strings"
	"testing"

	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

func TestPluginHostSandboxRequirement(t *testing.T) {
	t.Parallel()
	digest := strings.Repeat("a", 64)
	pkg := validatedSandboxPackage(digest, []string{"secret.use"}, []string{"container.provider"})
	requirement, err := SandboxRequirementFromValidatedPackage(pkg)
	if err != nil || !requirement.RequiresPrivilegeBoundary() {
		t.Fatalf("requirement=%+v err=%v", requirement, err)
	}
	if !requirement.RequiresNetworkIsolation() && !requirement.HighRisk() {
		t.Fatal("privileged container/dns package lost sandbox identity")
	}
	if err := requirement.validatePackageDigest(digest); err != nil {
		t.Fatalf("sandbox package digest binding = %v", err)
	}
}

func TestPluginHostSandboxRequirementAllowsResourceGroupExtension(t *testing.T) {
	t.Parallel()
	digest := strings.Repeat("b", 64)
	pkg := validatedSandboxPackage(digest, nil, []string{pluginsdk.ExtensionResourceGroup})
	requirement, err := SandboxRequirementFromValidatedPackage(pkg)
	if err != nil {
		t.Fatalf("resource.group sandbox requirement = %v", err)
	}
	if err := requirement.validatePackageDigest(digest); err != nil {
		t.Fatalf("resource.group package digest binding = %v", err)
	}
}

func TestPluginCapabilityAuthorizationMatrix(t *testing.T) {
	t.Parallel()
	quota := &capabilityQuotaStub{}
	audit := &capabilityAuditStub{}
	call := pluginsdk.HostCapabilityCall{
		PluginID: "plugin-1", InstanceID: "instance-1", Generation: "generation-1",
		Capability: pluginsdk.CapabilityPolicyAtomicState,
		Actor:      pluginsdk.HostActor{ID: "admin", ResourceGroupID: "default"},
		Target:     pluginsdk.HostTarget{Kind: "agent", ID: "edge-1", ResourceGroupID: "default"},
	}
	policy := CapabilityPolicy{
		PluginID: "plugin-1", InstanceID: "instance-1", Generation: "generation-1",
		Declared: []pluginsdk.HostCapability{pluginsdk.CapabilityPolicyAtomicState},
		Granted:  []pluginsdk.HostCapability{pluginsdk.CapabilityPolicyAtomicState},
		Actor:    call.Actor, ActorCapabilities: []pluginsdk.HostCapability{pluginsdk.CapabilityPolicyAtomicState},
		Targets: []pluginsdk.HostTarget{call.Target}, Quota: quota, Auditor: audit,
	}
	if err := policy.Authorize(t.Context(), call); err != nil || quota.calls != 1 || audit.outcome != "allowed" {
		t.Fatalf("Authorize() = %v quota=%d audit=%+v", err, quota.calls, audit)
	}

	denied := call
	denied.Capability = pluginsdk.CapabilityUIDynamicActions
	if err := policy.Authorize(t.Context(), denied); !errors.Is(err, ErrCapabilityDenied) {
		t.Fatalf("undeclared capability error = %v", err)
	}

	ungranted := policy
	ungranted.Granted = nil
	if err := ungranted.Authorize(t.Context(), call); !errors.Is(err, ErrCapabilityDenied) {
		t.Fatalf("not-granted error = %v", err)
	}
	wrongActor := call
	wrongActor.Actor.ID = "other"
	if err := policy.Authorize(t.Context(), wrongActor); !errors.Is(err, ErrCapabilityDenied) {
		t.Fatalf("actor deny error = %v", err)
	}
	wrongTarget := call
	wrongTarget.Target.ID = "edge-hidden"
	if err := policy.Authorize(t.Context(), wrongTarget); !errors.Is(err, ErrCapabilityDenied) {
		t.Fatalf("target deny error = %v", err)
	}
	deniedQuota := policy
	deniedQuota.Quota = &capabilityQuotaStub{err: errors.New("quota exceeded")}
	if err := deniedQuota.Authorize(t.Context(), call); !errors.Is(err, ErrCapabilityDenied) {
		t.Fatalf("quota deny error = %v", err)
	}

	handlePolicy := policy
	handlePolicy.Declared = append(handlePolicy.Declared, pluginsdk.CapabilityServiceRevocableResourceHandle)
	handlePolicy.Granted = append(handlePolicy.Granted, pluginsdk.CapabilityServiceRevocableResourceHandle)
	handlePolicy.ActorCapabilities = append(handlePolicy.ActorCapabilities, pluginsdk.CapabilityServiceRevocableResourceHandle)
	handleCall := call
	handleCall.Capability = pluginsdk.CapabilityServiceRevocableResourceHandle
	broker := NewResourceHandleBroker()
	token, err := broker.Issue(t.Context(), handlePolicy, handleCall, "resource")
	if err != nil || token == "" {
		t.Fatalf("Issue() = %q err=%v", token, err)
	}
	broker.RevokeGeneration(handleCall.InstanceID, handleCall.Generation)
	if _, err := broker.Resolve(t.Context(), token, handleCall); !errors.Is(err, ErrCapabilityDenied) {
		t.Fatalf("revoked handle resolve = %v", err)
	}

	action := pluginsdk.DynamicAction{
		ID: "refresh", Label: "Refresh", Capability: pluginsdk.CapabilityPolicyAtomicState, TargetKind: "agent",
	}
	actionPolicy := policy
	actionPolicy.DynamicActions = []pluginsdk.DynamicAction{action}
	registry := NewDynamicActionRegistry()
	if err := registry.Register(action, call, actionPolicy, func(context.Context, pluginsdk.HostCapabilityCall) error { return nil }); err != nil {
		t.Fatal(err)
	}
	forged := action
	forged.ID = "forged"
	if err := registry.Register(forged, call, actionPolicy, func(context.Context, pluginsdk.HostCapabilityCall) error { return nil }); err == nil {
		t.Fatal("unsigned dynamic action registered")
	}
	registry.RevokeGeneration(call.InstanceID, call.Generation)
	if err := registry.Invoke(t.Context(), call.InstanceID, call.Generation, action.ID, call.Actor); err == nil {
		t.Fatal("revoked dynamic action invoked")
	}
}

type capabilityQuotaStub struct {
	calls int
	err   error
}

func (q *capabilityQuotaStub) ConsumeHostCapability(context.Context, pluginsdk.HostCapabilityCall) error {
	if q.err != nil {
		return q.err
	}
	q.calls++
	return nil
}

type capabilityAuditStub struct{ outcome string }

func (a *capabilityAuditStub) AuditHostCapability(_ context.Context, event CapabilityAudit) error {
	a.outcome = event.Outcome
	return nil
}
