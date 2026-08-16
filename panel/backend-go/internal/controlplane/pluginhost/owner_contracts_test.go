//go:build !integration

package pluginhost

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

func TestPluginHostSandboxRequirementAndFailClosedLaunch(t *testing.T) {
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

	err = validatePlatformSandbox(Candidate{Identity: Identity{PackageDigest: digest}, Requirement: requirement})
	if err == nil || !strings.Contains(err.Error(), "isolation is unavailable") {
		t.Fatalf("validatePlatformSandbox() = %v, want isolation unavailable", err)
	}

	_, startErr := (ExecLauncher{}).Start(t.Context(), os.Args[0], nil, nil, io.Discard, Candidate{
		Identity: Identity{PackageDigest: digest}, Requirement: requirement,
	})
	if startErr == nil || !strings.Contains(startErr.Error(), "isolation is unavailable") {
		t.Fatalf("Start() = %v, want isolation unavailable", startErr)
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
}

type capabilityQuotaStub struct{ calls int }

func (q *capabilityQuotaStub) ConsumeHostCapability(context.Context, pluginsdk.HostCapabilityCall) error {
	q.calls++
	return nil
}

type capabilityAuditStub struct{ outcome string }

func (a *capabilityAuditStub) AuditHostCapability(_ context.Context, event CapabilityAudit) error {
	a.outcome = event.Outcome
	return nil
}
