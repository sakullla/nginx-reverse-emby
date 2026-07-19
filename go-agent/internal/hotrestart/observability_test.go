//go:build integration && linux

package hotrestart

import (
	"context"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/observability"
)

func TestSupervisorStartAndAbortEmitUpgradeOutcomes(t *testing.T) {
	var events []observability.Event
	ctx := observability.WithObserver(t.Context(), observability.ObserverFunc(func(_ context.Context, event observability.Event) {
		events = append(events, event)
	}))
	process, err := (Supervisor{ReadyTimeout: 5 * time.Second}).Start(ctx, helperLaunch(t, "ready"))
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := process.Abort(); err == nil {
		t.Fatal("Abort() unexpectedly returned nil child exit status")
	}
	if len(events) == 0 || events[0].Name != observability.HotRestartUpgrade || events[0].Outcome != "started" {
		t.Fatalf("events = %+v", events)
	}
	foundFailure := false
	for _, event := range events {
		if event.Name == observability.HotRestartUpgrade && event.Outcome == "failed" {
			foundFailure = true
		}
	}
	if !foundFailure {
		t.Fatalf("events = %+v, want abort failure outcome", events)
	}
}
