package app

import "testing"

func TestRPCProcessSupervisorIsAppOwnedAndDrained(t *testing.T) {
	app := newAppWithAllDeps(Config{}, nil, nil, nil, nil)
	if app.PluginProcessSupervisor() == nil {
		t.Fatal("app did not create the RPC process supervisor")
	}
	if app.PluginRPCHost() == nil {
		t.Fatal("app did not create the RPC orchestration host")
	}
	app.closeLocalRuntimes()
	if app.PluginProcessSupervisor() != nil {
		t.Fatal("app did not drain the RPC process supervisor")
	}
}
