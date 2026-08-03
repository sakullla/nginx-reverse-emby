//go:build integration

package service

import (
	"context"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/revision"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

func TestRevisionSnapshotProjectsListenerIdentityCreatedInMutation(t *testing.T) {
	root := t.TempDir()
	store := newControlPKIStore(t, root)
	vault, err := OpenPKIVault(PKIVaultConfig{DataRoot: root})
	if err != nil {
		t.Fatalf("OpenPKIVault() error = %v", err)
	}
	bootstrap := bootstrapInternalPKIForControlTest(t, store, vault)

	const (
		agentID    = "local"
		identityID = "listener-identity-1"
	)
	executor := newMutationExecutor(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     agentID,
	}, store)
	result, err := executor.Execute(t.Context(), revision.MutationRequest{
		Kind:          "relay_listener.create",
		Request:       map[string]any{"listener_id": 1},
		Targets:       []revision.Target{{AgentID: agentID, Local: true}},
		ResourceState: relayListenerMutationResourceState,
		Mutate: func(ctx context.Context, tx *storage.GormStore, revisions map[string]int64) error {
			if err := tx.SaveRelayListeners(ctx, agentID, []storage.RelayListenerRow{{
				ID: 1, AgentID: agentID, Name: "relay", BindHostsJSON: `["127.0.0.1"]`,
				ListenHost: "127.0.0.1", ListenPort: 18443, PublicHost: "127.0.0.1", PublicPort: 18443,
				Enabled: true, TransportMode: "tls_tcp", TLSMode: "pki_mtls", Revision: int(revisions[agentID]),
			}}); err != nil {
				return err
			}
			return tx.WithPKITransaction(ctx, func(pkiTx *storage.PKITransaction) error {
				return pkiTx.CreatePKIIdentity(ctx, storage.PKIIdentityRow{
					ID: identityID, PKIDomainID: bootstrap.result.PKIDomainID,
					Kind: storage.PKIIdentityKindListener, AgentID: agentID, ListenerID: "1",
					State: storage.PKIIdentityStateEnrollmentRequired, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
				})
			})
		},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(result.Agents) != 1 {
		t.Fatalf("result agents = %+v, want one local revision", result.Agents)
	}
	artifact, found, err := store.LoadCoordinatorRuntimeSnapshot(t.Context(), agentID, result.Agents[0].DesiredRevision)
	if err != nil || !found {
		t.Fatalf("LoadCoordinatorRuntimeSnapshot() found=%t error=%v", found, err)
	}
	if len(artifact.Snapshot.RelayListeners) != 1 {
		t.Fatalf("revision relay listeners = %+v, want one", artifact.Snapshot.RelayListeners)
	}
	listener := artifact.Snapshot.RelayListeners[0]
	if listener.PKIIdentityID != identityID || listener.PKIIdentityState != storage.PKIIdentityStateEnrollmentRequired {
		t.Fatalf("revision listener PKI projection = %+v", listener)
	}
}
