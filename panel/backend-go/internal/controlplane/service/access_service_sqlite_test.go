//go:build !fast && !integration

package service

import (
	"context"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

func TestAccessServicePhaseSurvivesSQLiteReopen(t *testing.T) {
	if testing.Short() {
		t.Skip("SQLite-backed access service lifecycle runs in the full test tier")
	}
	root := t.TempDir()
	store, err := newServiceSQLiteStore(t, root)
	if err != nil {
		t.Fatal(err)
	}
	h := newAccessHarness(t)
	h.store = nil
	h.svc.store = store
	h.agents.agents["entry-b"] = AgentSummary{ID: "entry-b", DesiredRevision: 1}
	view, err := h.create("sqlite-create", h.directInput("http://10.0.0.8:8096", "http://old.example/emby"))
	if err != nil {
		t.Fatal(err)
	}
	input := h.directInput("http://10.0.0.8:8096", "http://new.example/emby")
	entry := "entry-b"
	port := 9443
	input.EntryAgentID = &entry
	input.RelayListenPort = &port
	host := accessTestRelayHost
	input.RelayPublicHost = &host
	moved, err := h.svc.Update(context.Background(), view.ID, "sqlite-move", mustAccessBody(t, input), input)
	if err != nil {
		t.Fatal(err)
	}
	if moved.SwitchPhase != accessPhaseWait || moved.RuleID == 0 || moved.PendingRuleID == 0 {
		t.Fatalf("phase = %s rule %d pending %d err %s", moved.SwitchPhase, moved.RuleID, moved.PendingRuleID, moved.LastError)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := storage.NewStore(storage.StoreConfig{
		Driver: "sqlite", DataRoot: root, LocalAgentID: "local", SkipBootstrapSchema: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	row, found, err := reopened.GetAccessService(context.Background(), moved.ID)
	if err != nil || !found {
		t.Fatalf("reopened service missing: found %v err %v", found, err)
	}
	if row.SwitchPhase != accessPhaseWait || row.RuleID != moved.RuleID || row.PendingRuleID != moved.PendingRuleID {
		t.Fatalf("reopened phase = %s rule %d pending %d", row.SwitchPhase, row.RuleID, row.PendingRuleID)
	}
}

func TestAccessServiceListenerJoinsEntrySnapshotWithoutRule(t *testing.T) {
	if testing.Short() {
		t.Skip("SQLite-backed access service lifecycle runs in the full test tier")
	}
	root := t.TempDir()
	store, err := newServiceSQLiteStore(t, root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()
	if err := store.SaveAgent(ctx, storage.AgentRow{
		ID: "entry-b", Name: "entry-b", DesiredRevision: 4, CurrentRevision: 4,
		LastApplyRevision: 4, LastApplyStatus: "success",
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveRelayListeners(ctx, "service-a", []storage.RelayListenerRow{
		{ID: 41, Name: "dedicated", Enabled: true, TransportMode: "tls_tcp", TLSMode: "pki_mtls", ListenPort: 9443, PublicPort: 9443, PublicHost: "203.0.113.41"},
		{ID: 42, Name: "shared", Enabled: true, TransportMode: "tls_tcp", TLSMode: "pki_mtls", ListenPort: 9444, PublicPort: 9444, PublicHost: "203.0.113.42"},
		{ID: 43, Name: "other", Enabled: true, TransportMode: "tls_tcp", TLSMode: "pki_mtls", ListenPort: 9445, PublicPort: 9445, PublicHost: "203.0.113.43"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveAccessService(ctx, storage.AccessServiceRow{
		ID: "svc-dedicated", Name: "dedicated", EntryAgentID: "entry-b", ServiceAgentID: "service-a",
		BackendURL: "http://203.0.113.12:8096", EntryURL: "http://entry.example/emby",
		DedicatedListenerID: 41, DedicatedListenerAgentID: "service-a", ProbeJSON: "[]",
		CreatedAt: "2026-10-20T00:00:00Z", UpdatedAt: "2026-10-20T00:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveAccessService(ctx, storage.AccessServiceRow{
		ID: "svc-shared", Name: "shared", EntryAgentID: "entry-b", ServiceAgentID: "service-a",
		BackendURL: "http://203.0.113.13:8096", EntryURL: "http://shared.example/emby",
		ProbeJSON: `[{"agent_id":"entry-b","purpose":"snapshot","has_listener":true,"relay_listener_id":42,"status":"untested"}]`,
		CreatedAt: "2026-10-20T00:00:00Z", UpdatedAt: "2026-10-20T00:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveAccessService(ctx, storage.AccessServiceRow{
		ID: "svc-deleted", Name: "deleted", EntryAgentID: "entry-b", ServiceAgentID: "service-a",
		DedicatedListenerID: 43, DeletionPending: true, ProbeJSON: "[]",
		CreatedAt: "2026-10-20T00:00:00Z", UpdatedAt: "2026-10-20T00:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.LoadAgentSnapshot(ctx, "entry-b", storage.AgentSnapshotInput{})
	if err != nil {
		t.Fatal(err)
	}
	found := map[int]bool{}
	for _, listener := range snapshot.RelayListeners {
		found[listener.ID] = true
	}
	if !found[41] || !found[42] || found[43] {
		t.Fatalf("entry snapshot listeners = %#v", snapshot.RelayListeners)
	}
	desired, err := store.PublishEntryRelayListener(ctx, "entry-b")
	if err != nil {
		t.Fatal(err)
	}
	if desired != 5 {
		t.Fatalf("published desired revision = %d, want 5", desired)
	}
	again, err := store.PublishEntryRelayListener(ctx, "entry-b")
	if err != nil {
		t.Fatal(err)
	}
	if again != 5 {
		t.Fatalf("second publish bumped the in-flight revision to %d", again)
	}
}
