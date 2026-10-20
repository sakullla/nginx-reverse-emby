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
