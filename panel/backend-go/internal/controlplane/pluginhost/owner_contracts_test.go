//go:build !integration

package pluginhost

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

func TestPluginHostSandboxRequirement(t *testing.T) {
	t.Parallel()
	digest := strings.Repeat("a", 64)
	pkg := validatedSandboxPackage(digest, []string{"secret.use"}, []string{"dns.provider"})
	requirement, err := SandboxRequirementFromValidatedPackage(pkg)
	if err != nil || !requirement.RequiresPrivilegeBoundary() {
		t.Fatalf("requirement=%+v err=%v", requirement, err)
	}
	if !requirement.RequiresNetworkIsolation() && !requirement.HighRisk() {
		t.Fatal("privileged dns package lost sandbox identity")
	}
	if err := requirement.validatePackageDigest(digest); err != nil {
		t.Fatalf("sandbox package digest binding = %v", err)
	}
}

func TestPluginHostSandboxProcessBudgetUsesSafeFloor(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		concurrency int
		want        int
	}{{1, 50}, {8, 50}, {46, 50}, {47, 51}} {
		t.Run(fmt.Sprintf("concurrency_%d", test.concurrency), func(t *testing.T) {
			pkg := validatedSandboxPackage(strings.Repeat("e", 64), nil, []string{pluginsdk.ExtensionUIRoute})
			pkg.Manifest.ResourceBudget.Concurrency = test.concurrency
			requirement, err := SandboxRequirementFromValidatedPackage(pkg)
			if err != nil {
				t.Fatal(err)
			}
			if got := requirement.Budget().Processes; got != test.want {
				t.Fatalf("process budget = %d, want %d", got, test.want)
			}
		})
	}
}

func TestPluginHostSandboxFileBudgetScalesFromSafeFloor(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		concurrency int
		wantFiles   int
	}{{2, 512}, {8, 512}, {16, 512}, {32, 512}, {64, 1024}} {
		t.Run(fmt.Sprintf("concurrency_%d", test.concurrency), func(t *testing.T) {
			pkg := validatedSandboxPackage(strings.Repeat("f", 64), nil, []string{pluginsdk.ExtensionUIRoute})
			pkg.Manifest.ResourceBudget.Concurrency = test.concurrency
			requirement, err := SandboxRequirementFromValidatedPackage(pkg)
			if err != nil {
				t.Fatal(err)
			}
			if got := requirement.Budget().Files; got != test.wantFiles {
				t.Fatalf("file budget = %d, want %d", got, test.wantFiles)
			}
		})
	}
}

func TestPluginHostSandboxRequirementRejectsRemovedDockerComposeIdentifiers(t *testing.T) {
	t.Parallel()
	digest := strings.Repeat("a", 64)
	if _, err := SandboxRequirementFromValidatedPackage(validatedSandboxPackage(digest, []string{"container.compose"}, []string{"http.request"})); err == nil {
		t.Fatal("container.compose sandbox permission was accepted")
	}
	if _, err := SandboxRequirementFromValidatedPackage(validatedSandboxPackage(digest, []string{"container.read"}, []string{"http.request"})); err == nil {
		t.Fatal("container.read sandbox permission was accepted")
	}
	if _, err := SandboxRequirementFromValidatedPackage(validatedSandboxPackage(digest, []string{"container.manage"}, []string{"http.request"})); err == nil {
		t.Fatal("container.manage sandbox permission was accepted")
	}
	if _, err := SandboxRequirementFromValidatedPackage(validatedSandboxPackage(digest, []string{"secret.use"}, []string{"container.provider"})); err == nil {
		t.Fatal("container.provider sandbox extension was accepted")
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

func TestPluginHostSandboxRequirementAllowsDeclaredFullNetwork(t *testing.T) {
	t.Parallel()
	digest := strings.Repeat("c", 64)
	pkg := validatedSandboxPackage(digest, []string{pluginsdk.PermissionNetworkFull}, []string{pluginsdk.ExtensionResourceGroup})
	requirement, err := SandboxRequirementFromValidatedPackage(pkg)
	if err != nil {
		t.Fatalf("network.full sandbox requirement = %v", err)
	}
	if !requirement.RequiresPrivilegeBoundary() || !requirement.Budget().Network || requirement.RequiresNetworkIsolation() {
		t.Fatalf("network.full requirement = %+v", requirement)
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

type stopEligibilityRPCClient struct{ stops int }

func (*stopEligibilityRPCClient) Handshake(context.Context, pluginsdk.RPCHandshakeRequest) (pluginsdk.RPCHandshakeResponse, error) {
	return pluginsdk.RPCHandshakeResponse{}, nil
}
func (*stopEligibilityRPCClient) Prepare(context.Context, pluginsdk.LifecycleRequest) (pluginsdk.LifecycleResponse, error) {
	return pluginsdk.LifecycleResponse{}, nil
}
func (*stopEligibilityRPCClient) Activate(context.Context, pluginsdk.LifecycleRequest) (pluginsdk.LifecycleResponse, error) {
	return pluginsdk.LifecycleResponse{}, nil
}
func (client *stopEligibilityRPCClient) Stop(context.Context, pluginsdk.LifecycleRequest) (pluginsdk.LifecycleResponse, error) {
	client.stops++
	return pluginsdk.LifecycleResponse{}, nil
}

func TestPluginHostSkipsRPCStopBeforePreparedLifecycle(t *testing.T) {
	client := &stopEligibilityRPCClient{}
	setupDone := make(chan struct{})
	close(setupDone)
	instance := &Instance{ID: "instance", Generation: "generation", State: "starting", client: client, setupDone: setupDone}
	if err := instance.Stop(t.Context()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if client.stops != 0 || instance.State != "stopped" {
		t.Fatalf("handshaken cleanup stops=%d state=%q, want process-only stopped cleanup", client.stops, instance.State)
	}
}
