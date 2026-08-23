//go:build !integration

package hostapi

import (
	"context"
	"errors"

	"testing"
	"time"

	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

type blockingQuota struct {
	started chan struct{}
	release chan struct{}
}

func (quota *blockingQuota) Consume(ctx context.Context, _ pluginsdk.HostCapabilityCall) error {
	close(quota.started)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-quota.release:
		return nil
	}
}

type auditRecorder struct {
	events []AuditEvent
	err    error
}

func (recorder *auditRecorder) Audit(_ context.Context, event AuditEvent) error {
	recorder.events = append(recorder.events, event)
	return recorder.err
}

func TestHostAPIAuditFailureNeverIssuesProtectedHandle(t *testing.T) {
	capability := pluginsdk.CapabilityServiceRevocableResourceHandle
	target := pluginsdk.HostTarget{Kind: "relay", ID: "relay-1", ResourceGroupID: "group-1"}
	call := pluginsdk.HostCapabilityCall{PluginID: "official.reverse", InstanceID: "instance-1", Generation: "generation-1", Capability: capability, Actor: pluginsdk.HostActor{ID: "official.reverse", ResourceGroupID: "group-1"}, Target: target, QuotaMetric: "host.calls", QuotaUnits: 1}
	for _, test := range []struct {
		name string
		deny bool
	}{
		{name: "otherwise allowed"},
		{name: "already denied", deny: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			quota, _ := NewCallQuota(1)
			auditErr := errors.New("audit store unavailable")
			audit := &auditRecorder{err: auditErr}
			authorizer := Authorizer{PluginID: call.PluginID, InstanceID: call.InstanceID, Generation: call.Generation, Declared: []pluginsdk.HostCapability{capability}, Granted: []pluginsdk.HostCapability{capability}, Actor: call.Actor, ActorCapabilities: []pluginsdk.HostCapability{capability}, Targets: []pluginsdk.HostTarget{target}, Quota: quota, Auditor: audit}
			if test.deny {
				authorizer.Granted = nil
			}
			handles := NewResourceHandles()
			token, err := handles.Issue(t.Context(), authorizer, call, "protected-effect")
			if token != "" || err == nil || !errors.Is(err, ErrDenied) {
				t.Fatalf("Issue() = token %q error %v", token, err)
			}
			if len(handles.handles) != 0 {
				t.Fatalf("audit failure published handles: %+v", handles.handles)
			}
			if len(audit.events) != 1 || audit.events[0].Outcome != map[bool]string{false: "allowed", true: "denied"}[test.deny] {
				t.Fatalf("audit events = %+v", audit.events)
			}
		})
	}
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

func TestHostAPIAuthorizationProjectsL4RuleCapability(t *testing.T) {
	capability := pluginsdk.CapabilityL4Rule
	target := pluginsdk.HostTarget{Kind: "l4_rule", ID: "edge-a:7", ResourceGroupID: "group-1"}
	call := pluginsdk.HostCapabilityCall{PluginID: "official.l4-manager", InstanceID: "instance-1", Generation: "generation-1", Capability: capability, Actor: pluginsdk.HostActor{ID: "official.l4-manager", ResourceGroupID: "group-1"}, Target: target, QuotaMetric: "host.calls", QuotaUnits: 1}
	for _, test := range []struct {
		name     string
		declared []pluginsdk.HostCapability
		granted  []pluginsdk.HostCapability
		wantDeny bool
	}{
		{name: "declared and granted", declared: []pluginsdk.HostCapability{capability}, granted: []pluginsdk.HostCapability{capability}},
		{name: "not declared", granted: []pluginsdk.HostCapability{capability}, wantDeny: true},
		{name: "not granted", declared: []pluginsdk.HostCapability{capability}, wantDeny: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			quota, _ := NewCallQuota(4)
			audit := &auditRecorder{}
			authorizer := Authorizer{PluginID: call.PluginID, InstanceID: call.InstanceID, Generation: call.Generation, Declared: test.declared, Granted: test.granted, Actor: call.Actor, ActorCapabilities: []pluginsdk.HostCapability{capability}, Targets: []pluginsdk.HostTarget{target}, Quota: quota, Auditor: audit}
			err := authorizer.Authorize(t.Context(), call)
			if test.wantDeny {
				if !errors.Is(err, ErrDenied) {
					t.Fatalf("Authorize() error = %v, want denial", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Authorize() error = %v", err)
			}
			if len(audit.events) != 1 || audit.events[0].Outcome != "allowed" || audit.events[0].Call.Capability != capability {
				t.Fatalf("audit events = %+v", audit.events)
			}
		})
	}
}

func TestHostAPIAuthorizationProjectsChannelReverseCapability(t *testing.T) {
	capability := pluginsdk.CapabilityChannelReverse
	target := pluginsdk.HostTarget{Kind: "channel_session", ID: "edge-a:session-1", ResourceGroupID: "group-1"}
	call := pluginsdk.HostCapabilityCall{PluginID: "official.channel-manager", InstanceID: "instance-1", Generation: "generation-1", Capability: capability, Actor: pluginsdk.HostActor{ID: "official.channel-manager", ResourceGroupID: "group-1"}, Target: target, QuotaMetric: "host.calls", QuotaUnits: 1}
	for _, test := range []struct {
		name     string
		declared []pluginsdk.HostCapability
		granted  []pluginsdk.HostCapability
		wantDeny bool
	}{
		{name: "declared and granted", declared: []pluginsdk.HostCapability{capability}, granted: []pluginsdk.HostCapability{capability}},
		{name: "not declared", granted: []pluginsdk.HostCapability{capability}, wantDeny: true},
		{name: "not granted", declared: []pluginsdk.HostCapability{capability}, wantDeny: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			quota, _ := NewCallQuota(4)
			audit := &auditRecorder{}
			authorizer := Authorizer{PluginID: call.PluginID, InstanceID: call.InstanceID, Generation: call.Generation, Declared: test.declared, Granted: test.granted, Actor: call.Actor, ActorCapabilities: []pluginsdk.HostCapability{capability}, Targets: []pluginsdk.HostTarget{target}, Quota: quota, Auditor: audit}
			err := authorizer.Authorize(t.Context(), call)
			if test.wantDeny {
				if !errors.Is(err, ErrDenied) {
					t.Fatalf("Authorize() error = %v, want denial", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Authorize() error = %v", err)
			}
			if len(audit.events) != 1 || audit.events[0].Outcome != "allowed" || audit.events[0].Call.Capability != capability {
				t.Fatalf("audit events = %+v", audit.events)
			}
		})
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

func TestHostAPIRevokeCancelsLeaseWithoutWaitingForQuota(t *testing.T) {
	capability := pluginsdk.CapabilityServiceRevocableResourceHandle
	target := pluginsdk.HostTarget{Kind: "relay", ID: "relay-1", ResourceGroupID: "group-1"}
	call := pluginsdk.HostCapabilityCall{PluginID: "official.reverse", InstanceID: "instance-1", Generation: "generation-1", Capability: capability, Actor: pluginsdk.HostActor{ID: "official.reverse", ResourceGroupID: "group-1"}, Target: target, QuotaMetric: "host.calls", QuotaUnits: 1}
	quota, _ := NewCallQuota(16)
	authorizer := Authorizer{PluginID: call.PluginID, InstanceID: call.InstanceID, Generation: call.Generation, Declared: []pluginsdk.HostCapability{capability}, Granted: []pluginsdk.HostCapability{capability}, Actor: call.Actor, ActorCapabilities: []pluginsdk.HostCapability{capability}, Targets: []pluginsdk.HostTarget{target}, Quota: quota, Auditor: &auditRecorder{}}
	handles := NewResourceHandles()
	token, err := handles.Issue(t.Context(), authorizer, call, "resource")
	if err != nil {
		t.Fatal(err)
	}
	handles.handles[token].authorizer.Quota = &blockingQuota{started: make(chan struct{}), release: make(chan struct{})}
	blocking := handles.handles[token].authorizer.Quota.(*blockingQuota)
	done := make(chan error, 1)
	go func() {
		_, resolveErr := handles.Resolve(context.Background(), token, call)
		done <- resolveErr
	}()
	<-blocking.started
	revoked := make(chan struct{})
	go func() { handles.RevokeGeneration(call.InstanceID, call.Generation); close(revoked) }()
	select {
	case <-revoked:
	case <-time.After(time.Second):
		t.Fatal("RevokeGeneration waited for external quota owner")
	}
	select {
	case err := <-done:
		if !errors.Is(err, ErrDenied) {
			t.Fatalf("Resolve() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled handle lease did not return")
	}
}
