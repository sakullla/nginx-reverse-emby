//go:build exhaustive && !integration

package service

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/marketplace"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/pluginhost"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

func TestPendingUpgradeRecoveryActivatesControlPlaneBeforePromotion(t *testing.T) {
	for _, fail := range []bool{false, true} {
		name := "success"
		if fail {
			name = "activation_failure"
		}
		t.Run(name, func(t *testing.T) {
			fixture := newPluginTargetAuthorityFixture(t, "official.upgrade-recovery", true)
			ctx := WithSystemMutationPrincipal(t.Context(), "system:test")
			request := fixture.configureRequest("edge-a", "upgrade-instance")
			if _, err := fixture.service.ConfigureMutation(ctx, request); err != nil {
				t.Fatal(err)
			}
			completePluginTargetConfigure(t, fixture, request.InstanceID)
			before, _, _ := fixture.store.GetInstalledPlugin(ctx, fixture.pluginID)
			key := publishFixtureSigningKey()
			publicKey := base64.StdEncoding.EncodeToString(key.Public().(ed25519.PublicKey))
			fingerprint, err := marketplace.SourceSignerFingerprint(publicKey)
			if err != nil {
				t.Fatal(err)
			}
			trust := marketplace.SignatureTrust{SourceID: "target-authority-fixture", SourceKind: marketplace.SourceKindCustom, KeyID: "test-fixture", PublicKey: publicKey, Fingerprint: fingerprint}
			candidate := importPackageCandidate(t, fixture.cacheRoot, writePluginTargetAuthorityPackageVersion(t, fixture.pluginID, "1.0.1", true, key, false), fixture.service.validator, trust)
			if _, err := fixture.service.Upgrade(ctx, PluginUpgradeRequest{PluginID: fixture.pluginID, Package: candidate, ActorID: "admin", RiskAccepted: true}); err != nil {
				t.Fatal(err)
			}
			pending, _, _ := fixture.store.GetInstalledPlugin(ctx, fixture.pluginID)
			instance := mustPluginInstanceByID(t, fixture.store, request.InstanceID)
			fixture.service.store = &upgradeRecoveryStore{GormStore: fixture.store, status: storage.PluginAgentRuntimeStatusRow{
				OperationID: pending.PendingOperationID, PluginID: fixture.pluginID, InstanceID: instance.ID,
				AgentID: "edge-a", Revision: pending.PendingRevision, ConfigVersion: instance.PendingVersion,
				PackageDigest: candidate.Package.Digest, State: "active", DetailsJSON: `{}`,
			}}
			runtime := &upgradeRecoveryRuntime{activate: func(candidates []pluginhost.Candidate) error {
				current, _, _ := fixture.store.GetInstalledPlugin(ctx, fixture.pluginID)
				if current.ActivePackageDigest != before.ActivePackageDigest || current.PendingOperationID != pending.PendingOperationID {
					t.Fatal("upgrade was promoted before control-plane activation")
				}
				if len(candidates) != 1 || candidates[0].Identity.PackageDigest != candidate.Package.Digest || candidates[0].Identity.Version != "1.0.1" {
					t.Fatalf("activation did not select the staged package: %+v", candidates)
				}
				if fail {
					return errors.New("candidate handshake failed")
				}
				return nil
			}}
			fixture.service.controlPlaneRuntime = runtime
			if err := fixture.service.RecoverPendingPluginOperations(ctx); err != nil {
				t.Fatal(err)
			}
			if runtime.calls != 1 {
				t.Fatalf("control-plane activations = %d, want 1", runtime.calls)
			}
			after, _, _ := fixture.store.GetInstalledPlugin(ctx, fixture.pluginID)
			op, _, _ := fixture.store.GetPluginOperation(ctx, pending.PendingOperationID)
			wantDigest, wantStatus := candidate.Package.Digest, "succeeded"
			if fail {
				wantDigest, wantStatus = before.ActivePackageDigest, "failed"
			}
			if after.ActivePackageDigest != wantDigest || op.Status != wantStatus || after.PendingOperationID != "" {
				t.Fatalf("recovery result: digest=%s status=%s pending=%s", after.ActivePackageDigest, op.Status, after.PendingOperationID)
			}
		})
	}
}

type upgradeRecoveryStore struct {
	*storage.GormStore
	status storage.PluginAgentRuntimeStatusRow
}

func (s *upgradeRecoveryStore) ListPluginAgentRuntimeStatuses(context.Context, string) ([]storage.PluginAgentRuntimeStatusRow, error) {
	return []storage.PluginAgentRuntimeStatusRow{s.status}, nil
}

func (s *upgradeRecoveryStore) GetCoordinatorRevision(context.Context, string, int64) (storage.AgentRevisionRow, bool, error) {
	return storage.AgentRevisionRow{State: storage.AgentRevisionStateApplied}, true, nil
}

type upgradeRecoveryRuntime struct {
	calls    int
	activate func([]pluginhost.Candidate) error
}

func (r *upgradeRecoveryRuntime) ActivateBatch(_ context.Context, candidates []pluginhost.Candidate) ([]*pluginhost.Instance, error) {
	r.calls++
	return nil, r.activate(candidates)
}

func (*upgradeRecoveryRuntime) ActiveGeneration(string) (string, bool) { return "old-generation", true }
func (*upgradeRecoveryRuntime) Stop(context.Context, string) error     { return nil }
