//go:build !fast

package revision

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

func TestProjectRevisionPKIRelayListenersBindsCanonicalIdentity(t *testing.T) {
	certificateID := "certificate-listener-1"
	ownerDigest := sha256.Sum256([]byte(strings.Join([]string{
		"pki-identity-owner-v1", "domain-1", storage.PKIIdentityKindListener, "edge-1", "1",
	}, "\x00")))
	ownerKey := hex.EncodeToString(ownerDigest[:])
	state := storage.PKICanonicalState{
		Settings: &storage.PKISettingsRow{
			PKIDomainID: "domain-1", UpgradeState: storage.PKIUpgradeStateTunnelMTLSOnly,
		},
		Identities: []storage.PKIIdentityRow{{
			ID: "identity-listener-1", PKIDomainID: "domain-1", Kind: storage.PKIIdentityKindListener,
			AgentID: "edge-1", ListenerID: "1", State: storage.PKIIdentityStateActive,
			ActiveOwnerKey: &ownerKey, CurrentCertificateID: &certificateID,
		}},
	}
	snapshot := storage.Snapshot{RelayListeners: []storage.RelayListener{{
		ID: 1, AgentID: "edge-1", TLSMode: "off", AllowSelfSigned: true,
	}}}

	projected, err := projectRevisionPKIRelayListenersWithState(state, "edge-1", snapshot)
	if err != nil {
		t.Fatal(err)
	}
	listener := projected.RelayListeners[0]
	if listener.PKIIdentityID != "identity-listener-1" || listener.PKIIdentityState != storage.PKIIdentityStateActive ||
		listener.PKICertificateID != certificateID || listener.TLSMode != "pki_mtls" || listener.AllowSelfSigned {
		t.Fatalf("projected relay listener = %+v", listener)
	}
}

func TestExecutorPreservesPostMutationSnapshotRevisionFloor(t *testing.T) {
	store := newRevisionTestStore(t)
	if err := store.SaveAgent(t.Context(), storage.AgentRow{ID: "edge-1", Name: "edge"}); err != nil {
		t.Fatal(err)
	}
	mutated := false
	executor := NewExecutor(
		store,
		WithClock(func() time.Time { return time.Date(2026, 9, 4, 0, 0, 0, 0, time.UTC) }),
		WithOperationIDGenerator(func() (string, error) { return "op-post-mutation-floor", nil }),
		WithSnapshotBuilder(SnapshotBuilderFunc(func(context.Context, *storage.GormStore, Target) (storage.Snapshot, error) {
			revision := int64(0)
			if mutated {
				revision = 2
			}
			return storage.Snapshot{Revision: revision}, nil
		})),
	)
	result, err := executor.Execute(t.Context(), MutationRequest{
		Kind:    "relay_listener.create",
		Request: map[string]any{"listener": 1},
		Targets: []Target{{AgentID: "edge-1"}},
		ResourceState: func(context.Context, *storage.GormStore, Target) (any, error) {
			return mutated, nil
		},
		Mutate: func(context.Context, *storage.GormStore, map[string]int64) error {
			mutated = true
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Agents) != 1 || result.Agents[0].DesiredRevision != 2 {
		t.Fatalf("mutation result = %+v, want revision 2", result)
	}
	if _, found, err := store.GetCoordinatorRevision(t.Context(), "edge-1", 2); err != nil || !found {
		t.Fatalf("revision 2 = found %v, err %v", found, err)
	}
	if _, found, err := store.GetCoordinatorRevision(t.Context(), "edge-1", 1); err != nil || found {
		t.Fatalf("revision 1 = found %v, err %v", found, err)
	}
}
