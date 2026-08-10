package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/authz"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/plugins"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

type capabilityTestStore struct {
	quotaCalls int
	audits     []storage.AuditEventRow
	quotaErr   error
}

func (store *capabilityTestStore) ConsumeQuotaForResource(context.Context, string, string, string, string, string, int64) (storage.QuotaDecision, error) {
	store.quotaCalls++
	return storage.QuotaDecision{}, store.quotaErr
}

func (store *capabilityTestStore) AppendAuditEvent(_ context.Context, row storage.AuditEventRow) error {
	store.audits = append(store.audits, row)
	return nil
}

type capabilityTestAuthorizer struct{ err error }

func (authorizer capabilityTestAuthorizer) AuthorizeResource(context.Context, authz.Actor, string, string, string) error {
	return authorizer.err
}

func TestPluginCapabilityPolicyUsesValidatedDeclarationDurableGrantRBACQuotaAndAudit(t *testing.T) {
	capability := pluginsdk.CapabilityServiceRevocableResourceHandle
	packageDigest := strings.Repeat("a", 64)
	packageIdentity := strings.Repeat("b", 64)
	target := pluginsdk.HostTarget{Kind: "agent", ID: "edge-1", ResourceGroupID: "group-a"}
	actor := authz.Actor{ID: "operator", SessionID: "session-1", Permissions: []string{authz.PermissionResourceWrite}, VisibleResourceGroups: []string{"group-a"}}
	pkg := plugins.ValidatedPackage{Manifest: plugins.Manifest{ID: "official.service", Signature: plugins.Signature{Algorithm: "ed25519", KeyID: plugins.OfficialSignatureKeyID}, Permissions: []plugins.Permission{{Name: string(capability), Resource: target.ID}}}, Digest: packageDigest}
	instance := storage.PluginInstanceRow{ID: "instance-1", PluginID: pkg.Manifest.ID, ResourceGroupID: target.ResourceGroupID}
	grant := storage.PluginGrantRow{PluginID: pkg.Manifest.ID, PackageDigest: packageDigest, PackageIdentity: packageIdentity, Permission: string(capability), ResourceSelector: target.ID}
	store := &capabilityTestStore{}
	policy, err := BuildPluginCapabilityPolicy(t.Context(), store, capabilityTestAuthorizer{}, PluginCapabilityPolicyInput{Package: pkg, PackageIdentity: packageIdentity, Instance: instance, Grants: []storage.PluginGrantRow{grant}, Actor: actor, Generation: "generation-1", Target: target})
	if err != nil {
		t.Fatal(err)
	}
	call := pluginsdk.HostCapabilityCall{PluginID: pkg.Manifest.ID, InstanceID: instance.ID, Generation: "generation-1", Capability: capability, Actor: pluginsdk.HostActor{ID: actor.ID, ResourceGroupID: target.ResourceGroupID}, Target: target, QuotaMetric: "host.calls", QuotaUnits: 1}
	if err := policy.Authorize(t.Context(), call); err != nil {
		t.Fatalf("Authorize() error = %v", err)
	}
	if store.quotaCalls != 1 || len(store.audits) != 1 || store.audits[0].Result != "allowed" || strings.Contains(store.audits[0].MetadataJSON, "session-1") {
		t.Fatalf("quota/audit projection = calls %d audits %+v", store.quotaCalls, store.audits)
	}

	for name, mutate := range map[string]func(*PluginCapabilityPolicyInput){
		"unsigned":            func(input *PluginCapabilityPolicyInput) { input.Package.Manifest.Signature = plugins.Signature{} },
		"forged official":     func(input *PluginCapabilityPolicyInput) { input.Package.Manifest.Signature.KeyID = "custom.signer" },
		"wrong group":         func(input *PluginCapabilityPolicyInput) { input.Target.ResourceGroupID = "group-b" },
		"actor outside group": func(input *PluginCapabilityPolicyInput) { input.Actor.VisibleResourceGroups = nil },
		"wrong digest grant":  func(input *PluginCapabilityPolicyInput) { input.Grants[0].PackageDigest = strings.Repeat("c", 64) },
	} {
		t.Run(name, func(t *testing.T) {
			input := PluginCapabilityPolicyInput{Package: pkg, PackageIdentity: packageIdentity, Instance: instance, Grants: []storage.PluginGrantRow{grant}, Actor: actor, Generation: "generation-1", Target: target}
			mutate(&input)
			candidate, buildErr := BuildPluginCapabilityPolicy(t.Context(), &capabilityTestStore{}, capabilityTestAuthorizer{}, input)
			if buildErr == nil {
				buildErr = candidate.Authorize(t.Context(), call)
			}
			if buildErr == nil {
				t.Fatal("invalid capability projection was authorized")
			}
		})
	}

	if _, err := BuildPluginCapabilityPolicy(t.Context(), &capabilityTestStore{}, capabilityTestAuthorizer{err: errors.New("forbidden")}, PluginCapabilityPolicyInput{Package: pkg, PackageIdentity: packageIdentity, Instance: instance, Grants: []storage.PluginGrantRow{grant}, Actor: actor, Generation: "generation-1", Target: target}); err == nil {
		t.Fatal("core resource authorization denial was ignored")
	}
}
