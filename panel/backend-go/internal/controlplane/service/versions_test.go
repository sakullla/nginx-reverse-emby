package service

import (
	"testing"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

func TestVersionPolicyCRUDRemainsMetadataOnly(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	store, err := newServiceTestSQLiteStore(t, t.TempDir(), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.SaveAgent(ctx, storage.AgentRow{
		ID: "edge-policy", Name: "Edge Policy", AgentToken: "token-policy",
		Platform: "linux-amd64", DesiredVersion: "1.0.0", DesiredRevision: 1,
		CurrentRevision: 1, LastApplyRevision: 1, LastApplyStatus: "success",
	}); err != nil {
		t.Fatalf("SaveAgent() error = %v", err)
	}

	localBefore, err := store.ListAgentRevisions(ctx, "local")
	if err != nil {
		t.Fatalf("ListAgentRevisions(local before) error = %v", err)
	}
	svc := NewVersionPolicyService(store)
	created, err := svc.Create(ctx, VersionPolicyInput{
		ID:             stringPtr("stable"),
		Channel:        stringPtr(" stable "),
		DesiredVersion: stringPtr(" 2.0.0 "),
		Packages: &[]VersionPackage{{
			Platform: " linux-amd64 ", URL: " https://example.com/nre-agent ", SHA256: " package-sha ",
		}},
		Tags: &[]string{" release ", "release"},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if created.DesiredVersion != "2.0.0" || len(created.Packages) != 1 || created.Packages[0].Platform != "linux-amd64" {
		t.Fatalf("Create() = %+v", created)
	}
	if _, err := svc.Update(ctx, "stable", VersionPolicyInput{DesiredVersion: stringPtr("2.0.1")}); err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if _, err := svc.Delete(ctx, "stable"); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}

	if _, found, err := store.GetAgentRevisionPointer(ctx, "edge-policy"); err != nil || found {
		t.Fatalf("remote revision pointer found=%v error=%v, want policy metadata only", found, err)
	}
	remoteRevisions, err := store.ListAgentRevisions(ctx, "edge-policy")
	if err != nil {
		t.Fatalf("ListAgentRevisions(remote) error = %v", err)
	}
	if len(remoteRevisions) != 0 {
		t.Fatalf("remote revisions = %+v, want none", remoteRevisions)
	}
	localAfter, err := store.ListAgentRevisions(ctx, "local")
	if err != nil {
		t.Fatalf("ListAgentRevisions(local after) error = %v", err)
	}
	if len(localAfter) != len(localBefore) {
		t.Fatalf("local revision count changed from %d to %d for policy metadata", len(localBefore), len(localAfter))
	}
}
